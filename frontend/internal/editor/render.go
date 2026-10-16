package editor

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// View composes the Editor workspace: Code File over Terminal on the left,
// File Tree over Task on the right.
//
// Every rect comes from Compute and is used as handed over. A component
// never decides its own visibility (LAYOUT.md rule 1) -- a zero rect means
// "not drawn this frame", which is why each box() call can be
// unconditional.
func (w *Workspace) View(l layout.Layout) string {
	w.lastLayout = l

	// The three rects mouse.go hit-tests against are cleared before the
	// frame that will re-record them. Without this they keep their last
	// drawn value after whatever drew them stops being drawn -- toggle the
	// right pane off and a click where the tree used to be still focused
	// the tree, because treeRect was never drawn over and never reset. A
	// zero rect is "not on screen", which inRect already means.
	w.treeRect = layout.Rect{}
	w.textArea = layout.Rect{}
	w.tabStrip = layout.Rect{}
	w.tabSpans = nil

	left := stack(
		w.codeFileBox(l.Main),
		w.terminalBox(l.Secondary),
	)

	if !l.ShowRightPane || len(l.RightPane) == 0 {
		return left
	}

	titles := []string{"File Tree", "Task"}
	bodies := []func(layout.Rect) []string{w.treeCard, w.taskCard}

	cards := make([]string, 0, len(l.RightPane))
	for i, r := range l.RightPane {
		if r.IsZero() || i >= len(titles) {
			continue
		}
		focused := i == 0 && w.focus == focusTree
		cards = append(cards, paneBox(w.edge(focused), w.label(focused), r, titles[i], bodies[i](r), 1))
	}

	return sideBySide(left, l.Main.W, stack(cards...), l.RightPaneWidth, layout.PaneGapX)
}

func (w *Workspace) edge(focused bool) lipgloss.Style {
	if focused {
		return w.theme.Style(theme.BorderFocus)
	}
	return w.theme.Style(theme.BorderIdle)
}

func (w *Workspace) label(focused bool) lipgloss.Style {
	if focused {
		return w.theme.Style(theme.AccentPrimary)
	}
	return w.theme.Style(theme.TextSecondary)
}

// gutterWidth is the line-number column, sized to the file's own line
// count plus one cell of separation.
//
// Sized per file rather than fixed: a fixed four-digit gutter wastes two
// columns on every short file, and a fixed narrow one truncates the line
// numbers of exactly the file F13's Done-when #1 opens.
func gutterWidth(lines int) int {
	digits := len(fmt.Sprint(lines))
	if digits < 2 {
		digits = 2
	}
	return digits + 2
}

// codeFileBox draws the tab strip and the text area.
func (w *Workspace) codeFileBox(r layout.Rect) string {
	title := "Code File"
	focused := w.focus == focusBuffer
	if r.IsZero() {
		return ""
	}

	inner := r.W - 2 - 2 // borders, then paneBox's one cell of padding each side
	rows := r.H - 2
	if inner <= 0 || rows <= 0 {
		return paneBox(w.edge(focused), w.label(focused), r, title, nil, 1)
	}

	if len(w.docs) == 0 {
		return paneBox(w.edge(focused), w.label(focused), r, title, w.emptyState(inner, rows), 1)
	}

	d := w.docs[w.active]
	title = "Code File · " + d.name()

	strip, spans := w.renderTabs(inner)
	w.tabSpans = spans
	// The strip's own rect: one row, starting inside the border and
	// paneBox's one cell of padding, exactly as wide as the content.
	// Both coordinates, so a click in another pane on the same row can
	// never land here.
	w.tabStrip = layout.Rect{X: r.X + 2, Y: r.Y + 1, W: inner, H: 1}

	textRows := rows - 1
	gutter := gutterWidth(d.buf.Lines())
	cells := inner - gutter
	if cells < 1 {
		cells = 1
	}

	// The text area's geometry is recorded for mouse.go before anything is
	// drawn with it, so a click and the glyph it looks like it hit are
	// never computed two different ways.
	w.textArea = layout.Rect{X: r.X + 1 + 1 + gutter, Y: r.Y + 2, W: cells, H: textRows}

	d.ensureVisible(textRows, cells)
	content := make([]string, 0, rows)
	content = append(content, strip)
	content = append(content, w.renderCode(d, gutter, cells, textRows)...)
	return paneBox(w.edge(focused), w.label(focused), r, title, content, 1)
}

// cellPaint is how one visible cell is coloured: the lexer's role, plus
// whether the selection or the caret is on top of it.
//
// Three layers rather than one resolved Style per rune, because the two
// overlays are decided per frame while the role is cached across frames --
// and because the caret has to be able to invert whatever is underneath it
// (see caretStyle).
type cellPaint uint8

const (
	paintPlain cellPaint = iota
	paintSelected
	paintCaret
)

// renderCode draws the gutter and one visible buffer line per row.
func (w *Workspace) renderCode(d *doc, gutter, cells, rows int) []string {
	th := w.theme
	num := th.Style(theme.TextDim)
	cur := th.Style(theme.TextSecondary)

	styled := d.hl.window(th, d.buf, d.top, rows)
	selFrom, selTo := d.selectionRange()
	hasSel := d.hasSelection()

	// The caret is drawn whenever the buffer has focus -- INCLUDING while a
	// selection is open, at the selection's active end.
	//
	// It used to be suppressed during a selection, and that turned a small
	// selection into something unreadable: shift+up from the middle of an
	// indented line selects the tail of the line above plus that line's
	// indent, which renders as a short highlighted block with no caret
	// anywhere on screen. Reported as a selection "artifact appearing with
	// nothing selected"; it was a real, correct selection with its caret
	// hidden. Every editor shows the caret at the active end, and this one
	// does now.
	caretOn := w.focus == focusBuffer
	caretLine := d.cursor.Line

	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		line := d.top + i
		if line >= d.buf.Lines() {
			out = append(out, "")
			continue
		}
		label := fmt.Sprintf("%*d ", gutter-1, line+1)
		style := num
		if line == d.cursor.Line {
			style = cur
		}

		var runes []styledRune
		if i < len(styled) {
			runes = styled[i]
		}
		// The highlighter's slice is the authority on ROLE; the buffer is
		// the authority on CONTENT. They can disagree for exactly one
		// frame -- an edit lands and the window is re-tokenised from the
		// new version, but a cached slab from a stale version could still
		// be in hand if the cache rule is ever changed. Falling back to
		// the buffer's own runes keeps the text correct in that case and
		// loses only the colour.
		if len(runes) != len(d.buf.Runes(line)) {
			runes = plainLine(d.buf.Runes(line))
		}

		paint := w.paintRow(len(runes), line, d, hasSel, selFrom, selTo, caretOn && line == caretLine)
		out = append(out, style.Render(label)+
			w.sliceCells(runes, paint, d.left, cells, caretOn && line == caretLine, d.cursor.Col))
	}
	return out
}

// paintRow decides each rune's overlay on one line: selected, caret, or
// neither.
//
// Done at rune grain over the lexer's own output, which is what makes
// Done-when #4 hold on a line containing CJK or an emoji: the highlight
// covers exactly the runes between the two positions, and their widths are
// whatever runewidth says -- there is no cell arithmetic here that could
// disagree with the arithmetic that draws them.
func (w *Workspace) paintRow(n, line int, d *doc, hasSel bool, from, to Pos, caretHere bool) []cellPaint {
	paint := make([]cellPaint, n)
	if hasSel && line >= from.Line && line <= to.Line {
		lo, hi := 0, n
		if line == from.Line {
			lo = from.Col
		}
		if line == to.Line {
			hi = to.Col
		}
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		for i := lo; i < hi; i++ {
			paint[i] = paintSelected
		}
	}
	if caretHere && d.cursor.Col >= 0 && d.cursor.Col < n {
		paint[d.cursor.Col] = paintCaret
	}
	return paint
}

// caretStyle is REVERSE VIDEO over whatever the cell already was, not a
// colour of its own.
//
// It was Fill(SelectText, AccentPrimary) first, and that was invisible
// exactly where it mattered. THEME.md rule 3 makes accent.primary the
// colour of keywords, the focused border and the streaming indicator all
// at once -- so a caret whose BACKGROUND is accent.primary sitting on a
// keyword whose FOREGROUND is accent.primary is a turquoise block on
// turquoise text. Measured on the real palette: the caret emitted
// `38;2;240;255;254;48;2;46;195;198m` while the rune beside it emitted
// `38;2;46;195;198m` -- the caret's background and its neighbour's
// foreground are the same #2EC4C6. On a space it read as a solid block and
// on a glyph it vanished, which is why it appeared to "toggle" as the caret
// moved along a line one character at a time.
//
// Reverse video is the terminal's own answer and needs no new role (and
// THEME.md's "a colour that is not a role" rule does not apply -- this is
// not a colour). It is legible against every one of the four palettes, in
// light and dark, and it also survives NO_COLOR, where every foreground is
// dropped and a colour-based caret would disappear completely.
func (w *Workspace) caretStyle(role theme.Role) lipgloss.Style {
	return w.theme.Style(role).Reverse(true)
}

// sliceCells renders one line from display column `left` for `cells` cells.
//
// A tab is expanded to spaces HERE rather than in the buffer, which is the
// whole reason the editor can preserve a file's tabs: what is drawn is
// `runeCells` wide, what is stored is one `\t`.
//
// # Runs, not runes
//
// Adjacent cells that share a paint and a role are emitted as ONE styled
// run. Per-rune emission cost about one escape pair per character -- near
// 1,500 per frame on a full window -- and that was a measurable part of a
// per-keystroke frame cost that was overrunning app\paste.go's 6ms
// burst-detection gate (see this episode's notes on paste). Runs cut it to
// one pair per token.
func (w *Workspace) sliceCells(runes []styledRune, paint []cellPaint, left, cells int,
	caretHere bool, caretCol int) string {

	var b strings.Builder
	col := 0   // display column within the line
	drawn := 0 // cells written

	var run strings.Builder
	runPaint := paintPlain
	runRole := theme.CodeText
	runOpen := false

	flush := func() {
		if !runOpen {
			return
		}
		switch runPaint {
		case paintCaret:
			b.WriteString(w.caretStyle(runRole).Render(run.String()))
		case paintSelected:
			b.WriteString(w.theme.Fill(theme.SelectText, theme.SelectBg).Render(run.String()))
		default:
			b.WriteString(w.theme.Style(runRole).Render(run.String()))
		}
		run.Reset()
		runOpen = false
	}
	emit := func(text string, p cellPaint, role theme.Role) {
		if runOpen && (p != runPaint || role != runRole) {
			flush()
		}
		runPaint, runRole, runOpen = p, role, true
		run.WriteString(text)
	}

	for i, sr := range runes {
		cw := runeCells(sr.r, col)
		text := string(sr.r)
		if sr.r == '\t' {
			text = strings.Repeat(" ", cw)
		}
		p := paintPlain
		if i < len(paint) {
			p = paint[i]
		}

		if col+cw > left && drawn < cells {
			// A rune straddling the left edge is dropped rather than
			// half-drawn: there is no way to render the right half of a
			// double-width glyph, and padding in its place keeps every
			// later column aligned.
			if col < left {
				pad := col + cw - left
				if pad > cells-drawn {
					pad = cells - drawn
				}
				flush()
				b.WriteString(strings.Repeat(" ", pad))
				drawn += pad
			} else if cw > cells-drawn {
				flush()
				b.WriteString(strings.Repeat(" ", cells-drawn))
				drawn = cells
				break
			} else {
				emit(text, p, sr.role)
				drawn += cw
			}
		}
		col += cw
		if drawn >= cells {
			break
		}
	}
	flush()

	// The caret at end-of-line has no rune to sit on, so it is drawn as a
	// styled space. Without this the cursor simply vanishes whenever it is
	// past the last character, which is where it spends most of its time
	// while typing.
	if caretHere && caretCol >= len(runes) {
		if at := col - left; at >= 0 && at < cells && drawn <= at {
			b.WriteString(strings.Repeat(" ", at-drawn))
			b.WriteString(w.caretStyle(theme.CodeText).Render(" "))
		}
	}
	return b.String()
}

// emptyState is what the Code File box shows with nothing open.
func (w *Workspace) emptyState(width, rows int) []string {
	th := w.theme
	dim := th.Style(theme.TextDim)
	lines := []string{
		th.Style(theme.AccentPrimary).Render("No file open"),
		"",
	}
	if w.projectID == "" {
		// Precise about the consequence, because the backend is stricter
		// than "read-only": with no project resolved, pathguard refuses
		// every read outright, so there is nothing to open rather than
		// something to open read-only. See protocol.go's header.
		lines = append(lines,
			th.Style(theme.Warning).Render("No project here — nothing above the working"),
			th.Style(theme.Warning).Render("directory has a .point\\ marker."),
			"",
			dim.Render("No files can be opened without one: the path guard"),
			dim.Render("refuses every read with no project scope, and a file"),
			dim.Render("that cannot take a lock cannot be saved safely."),
			"",
			dim.Render("Run `project init` against this directory first."),
		)
	} else {
		lines = append(lines,
			dim.Render("ctrl+o   focus the file tree, ⏎ opens"),
			dim.Render("ctrl+n   next tab        ctrl+b  previous"),
			dim.Render("f5       re-index the project"),
		)
	}
	// The last thing say() emitted, repeated here. The line the user
	// actually reads is in the status bar; this is the empty state's own
	// copy, for the case where a refusal is the reason there is nothing
	// open.
	if w.note != "" {
		lines = append(lines, "", th.Style(theme.Warning).Render(w.note))
	}
	_ = width
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// terminalBox is F14's rect, drawn as a titled box with one dim line.
//
// Compute hands the Editor a Secondary rect unconditionally (its
// secondaryHeight branch returns MinBoxHeight for TermSmall), so the three
// rows exist whether or not anything fills them. Drawing the box and
// naming the episode is Placeholder's own precedent for the Query Area in
// F1 -- an empty bordered box with no explanation reads as a bug, and
// leaving the rect blank reads as a rendering failure. Reclaiming the rows
// would be a layout change, which is not this episode's.
func (w *Workspace) terminalBox(r layout.Rect) string {
	if r.IsZero() {
		return ""
	}
	body := []string{w.theme.Style(theme.TextDim).Render("the terminal arrives in F14")}
	return paneBox(w.edge(false), w.label(false), r, "Terminal", body, 1)
}

func (w *Workspace) treeCard(r layout.Rect) []string {
	w.treeRect = r
	return w.tree.render(w.theme, r.W-4, r.H-2, w.focus == focusTree)
}

// taskCard is the same global snapshot Root pushes to every workspace --
// see app\tasks.go's doc on why Root fetches it rather than the workspace.
//
// Deliberately minimal: F6 built the real card in chat, unexported, and
// F13's Build section does not list a Task card at all. What it does list
// is a right pane with two cards in it (LAYOUT.md's Editor sketch), so the
// slot exists and showing the rows honestly is smaller than either leaving
// a titled box empty or reproducing F6's card.
func (w *Workspace) taskCard(r layout.Rect) []string {
	th := w.theme
	rows := r.H - 2
	if rows <= 0 {
		return nil
	}
	if w.backendDown {
		return []string{th.Style(theme.Warning).Render("backend unavailable")}
	}
	out := make([]string, 0, rows)
	for _, t := range w.tasks {
		if len(out) >= rows {
			break
		}
		glyph, role := "·", theme.TextDim
		switch t.State {
		case "running":
			glyph, role = "▶", theme.Running
		case "queued":
			glyph, role = "◇", theme.Queued
		case "done":
			glyph, role = "✓", theme.Done
		case "failed", "cancelled":
			glyph, role = "✕", theme.Error
		}
		id := t.ID
		if len(id) > 8 {
			id = id[:8]
		}
		out = append(out, th.Style(role).Render(glyph+" "+id)+
			th.Style(theme.TextDim).Render(" "+t.State))
	}
	if len(out) == 0 {
		out = append(out, th.Style(theme.TextDim).Render("no tasks"))
	}
	return out
}
