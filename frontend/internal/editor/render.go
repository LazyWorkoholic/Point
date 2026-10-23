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

	// F15: the explain panel SPLITS the Code File pane rather than
	// overlaying it -- see explainpanel.go for why an app.Overlay was the
	// wrong shape for a surface whose whole purpose is to be read beside
	// the code. Compute is untouched: this is the workspace subdividing a
	// rect it was already handed.
	main, panelRect := l.Main, layout.Rect{}
	if w.panel != nil && !l.Main.IsZero() {
		pw := panelWidth(l.Main.W)
		panelRect = layout.Rect{X: l.Main.X + l.Main.W - pw, Y: l.Main.Y, W: pw, H: l.Main.H}
		main.W = l.Main.W - pw
	}

	left := stack(
		sideBySide(w.codeFileBox(main), main.W, w.panelBox(panelRect), panelRect.W, 0),
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
		focused := i == 0 && w.effectiveFocus() == focusTree
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
	focused := w.effectiveFocus() == focusBuffer
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

	// ONE index and ONE scroll position for this frame, built at the
	// width about to be drawn into so the count and the drawing cannot
	// disagree. Everything below works in VIEW ROWS -- see viewrows.go
	// for what that means and for the two-coordinate design it replaced.
	vi := w.viewIndexAt(d, cells)

	// Reveal the caret, plus the one row under it when the fill prompt is
	// open: the prompt is what the user is typing into, so it must not be
	// the row that falls off the bottom.
	//
	// A tall ghost is deliberately NOT revealed here. Showing the whole
	// region would mean every arrow key jumped to the region's end, and
	// the live edge is the follow's job. This is also what fixes the
	// arrow-key glitch: the old code shrank the reveal's row budget by
	// the injected row count, which for a region taller than the pane
	// clamped to ONE row and snapped the viewport on every keystroke.
	extra := 0
	if w.prompt != nil {
		extra = 1
	}
	d.ensureVisible(vi, textRows, cells, extra)

	// AUTO-FOLLOW a growing ghost, the way Chat's transcript follows a
	// growing reply -- now by moving the single scroll position to an
	// ordinary row in the sequence, rather than pinning the file to the
	// anchor and scrolling a nested window inside it.
	w.followGhost(d, vi, textRows, cells)

	content := make([]string, 0, rows)
	content = append(content, strip)
	content = append(content, w.renderCode(d, vi, gutter, cells, textRows)...)
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

// renderCode draws one row of the unified sequence per pane row.
//
// The walk is over VIEW ROWS, not buffer lines: `vi.at` says whether a row
// is a line of the file or one of the rows injected into it, and the same
// function answers the hit-test (mouse.go). That is the whole of what makes
// a long proposed region scroll, arrow and click like the text it will
// become -- see viewrows.go.
func (w *Workspace) renderCode(d *doc, vi viewIndex, gutter, cells, rows int) []string {
	th := w.theme
	num := th.Style(theme.TextDim)
	cur := th.Style(theme.TextSecondary)

	// The highlighter is asked for a window starting at the first REAL
	// line this frame draws, which is not d.top when the top of the pane
	// has scrolled into an injected block. `rows` lines is always at
	// least as many as the frame can show.
	firstLine, topInjected, _ := vi.at(d.top)
	if topInjected {
		firstLine++
	}
	styled := d.hl.window(th, d.buf, firstLine, rows)
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
		line, injected, blockRow := vi.at(d.top + i)

		// F15's two display-only injections. Neither is in the buffer;
		// both are rows this function emits and nothing else knows about
		// (ghost.go's header is the record of why that is the design
		// rather than a shortcut).
		//
		// They are rows of the sequence like any other, so the lines
		// below them are simply further down it -- which is what makes a
		// proposed insertion read the way it would if applied, and what
		// lets the scroll carry straight on into the code after it.
		if injected {
			out = append(out, w.injectedRowView(d, line, blockRow, gutter, cells))
			continue
		}
		if line < 0 || line >= d.buf.Lines() {
			out = append(out, "")
			continue
		}

		label := fmt.Sprintf("%*d ", gutter-1, line+1)
		style := num
		if line == d.cursor.Line {
			style = cur
		}

		var runes []styledRune
		if k := line - firstLine; k >= 0 && k < len(styled) {
			runes = styled[k]
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

// injectedRowView draws ONE injected row: the blockRow-th row of the block
// that hangs off `line`.
//
// The prompt occupies the block's first row when it is open on that line,
// which is the order viewIndexAt adds the two in -- stated in both places
// because a disagreement would silently draw a ghost row where the prompt
// should be.
func (w *Workspace) injectedRowView(d *doc, line, blockRow, gutter, cells int) string {
	th := w.theme

	// The prompt sits at the CARET row (F15: "a one-line prompt at the
	// cursor row"), after it, so the line being asked about stays visible
	// above the question.
	if w.prompt != nil && line == d.cursor.Line {
		if blockRow == 0 {
			return w.prompt.view(w, gutter+cells)
		}
		blockRow--
	}

	// The ghost sits after its anchor -- the caret line as of the moment
	// the question was asked, not the caret now. A ghost whose anchor has
	// been scrolled out of view simply is not drawn this frame; it is not
	// moved to the top of the viewport, because a proposed insertion
	// drawn somewhere other than where it would land is a lie about where
	// it would land.
	rows := w.ghostRows(cells)
	if blockRow < 0 || blockRow >= len(rows) {
		return ""
	}
	// A marker column instead of a line number, because these rows have
	// no line numbers: they are not in the file. `+` is what every diff
	// uses for a line that is not there yet.
	marker := th.Style(theme.AccentSecondary).Render(
		fmt.Sprintf("%*s ", gutter-1, "+"))
	role := theme.TextDim
	if w.ghost != nil && w.ghost.literal {
		// Not a proposed region: the agent's own words (a refusal, or a
		// reply that is not a proposal). Rendered as prose in a different
		// role so it cannot be mistaken for code it wants to insert.
		role = theme.TextSecondary
	}
	return marker + th.Style(role).Render(padCells(rows[blockRow], cells))
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
			dim.Render("ctrl+o   move between tree and file, ⏎ opens"),
			dim.Render("in the tree: j/k or arrows move, l expand/open, h collapse"),
			dim.Render("ctrl+n   next tab        ctrl+b  previous"),
			dim.Render("F5       re-index        F6  take over a stale lock"),
			dim.Render("F7       close the tab   ctrl+s  save"),
		)
	}
	// NO note here, deliberately, and this is a change from F13a.
	//
	// It used to repeat the last thing say() emitted, "for the case
	// where a refusal is the reason there is nothing open". The cost
	// was that the line never went away: every other transient message
	// in this app expires through r.status (app\root.go's
	// expireStatusIfChanged), and this copy expired never -- so after
	// closing the last tab the placeholder read "closed lf.py"
	// permanently, under a hint about opening one.
	//
	// The status bar is the one surface for a transient line, and it
	// already has the expiry. w.note stays as the field say() writes,
	// because WorkspaceStatusMsg carries it to Root from there; nothing
	// renders it twice.
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
	return w.tree.render(w.theme, r.W-4, r.H-2, w.effectiveFocus() == focusTree)
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

// ---------------------------------------------------------------------------
// Injected rows: the one place that knows where they are
// ---------------------------------------------------------------------------
//
// renderCode emits rows the buffer does not have (ghost text, the inline
// fill prompt). Two other things have to agree with it about exactly where
// they fall: the click mapping (mouse.go's posAt) and the "did they click a
// real line" test. Both go through viewrows.go's `viewIndex` rather than
// re-deriving it, because three copies of "which row is which" is the shape
// of drift this package has already paid for in render/mouse arithmetic --
// and then a second time in the ghost's own scroll offset, which this
// helper set no longer has.

// ghostRows is what the ghost actually DRAWS at a given content width, and
// the only place that decides it.
//
// A proposed region is CODE and is truncated: re-flowing code changes what
// it means, and a long line is read by scrolling horizontally, as it is
// everywhere else in this pane.
//
// Literal text -- a refusal, or any reply that is not a proposal -- is
// PROSE and is wrapped. It used to be truncated like code, which is what
// F15's gate saw as text running past the pane's border: a sentence cut at
// the edge mid-word reads as overflow.
//
// ALL of the rows, every time. There is no offset applied here any more:
// the rows are entries in the document's one row sequence, and which of
// them is on screen is decided by the single scroll position like any other
// row. The slice this used to return -- `all[g.top:]` -- was the nested
// sub-viewport viewrows.go's header describes.
func (w *Workspace) ghostRows(cells int) []string {
	g := w.ghost
	if g == nil || cells < 1 {
		return nil
	}
	if !g.literal {
		out := make([]string, 0, len(g.lines))
		for _, l := range g.lines {
			out = append(out, truncateCells(l, cells))
		}
		return out
	}
	var out []string
	for _, l := range g.lines {
		out = append(out, wrapCells(l, cells)...)
	}
	return out
}

// followGhost keeps the pane at the live edge of a streaming region, by
// moving the ONE scroll position to an ordinary row of the sequence.
//
// Same semantics as chat\scroll.go's `pinned`: while following, the top is
// recomputed every frame from the region's end, so arriving rows push the
// view along; scrolling away stops it (mouse.go). Nothing is pinned to the
// anchor and nothing stops the user scrolling straight past the region into
// the code below it -- which the previous two-coordinate version did, and
// which is the defect that prompted this rewrite.
func (w *Workspace) followGhost(d *doc, v viewIndex, rows, cells int) {
	if !w.ghostFollow {
		return
	}
	if top, ok := w.followTarget(d, v, rows, cells); ok {
		d.top = v.clampTop(top)
	}
}

// injectedRow reports whether a screen row offset within the text area is
// an injected row rather than a line of the file.
//
// The caller that matters is mousePress: a click on a ghost row has no
// buffer position, so it moves focus and nothing else.
func (w *Workspace) injectedRow(rowOffset int) bool {
	d, v := w.activeIndex()
	if d == nil || len(v.blocks) == 0 {
		return false
	}
	_, injected, _ := v.at(d.top + rowOffset)
	return injected
}
