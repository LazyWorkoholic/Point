package chat

import (
	"fmt"
	"strings"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// paneHorizontalPadding is the View Port's own inner padding — 2 cells,
// LAYOUT.md — stacked on top of the box's generic 1-cell border→content
// padding that every other box in Point uses. Text sits 3 cells clear of
// the viewport wall for a user message (2 here + 1 more from the message
// box's own border); 2 clear for an AI reply, which has no box of its own.
const paneHorizontalPadding = 2

// genericBoxPadding is the "box border → content" distance LAYOUT.md gives
// every OTHER box, including a user message's own box and the code block box.
const genericBoxPadding = 1

func (w *Workspace) View(l layout.Layout) string {
	th := w.theme
	label := th.Style(theme.TextSecondary)

	mainRect, secRect := w.splitGrowth(l)

	mainEdge := th.Style(mainBorderRole(w.input.focused))
	secEdge := th.Style(queryBorderRole(w.input.currentMode(), w.input.focused))

	return stackBlocks(
		paneBox(mainEdge, label, mainRect, "View Port", w.viewportLines(mainRect), paneHorizontalPadding),
		paneBox(secEdge, label, secRect, "Query Area", w.queryLines(secRect), genericBoxPadding),
	)
}

// mainBorderRole is queryBorderRole's mirror image (input_render.go): the
// View Port's own pane border goes BorderFocus exactly when the input is
// NOT focused — i.e. while browsing the transcript, which is the pane that
// actually has keyboard focus at that moment. Exactly one of the two panes
// is ever "bright" at a time.
func mainBorderRole(inputFocused bool) theme.Role {
	if inputFocused {
		return theme.BorderIdle
	}
	return theme.BorderFocus
}

// splitGrowth is F4's growth — overlay + auto-scroll: past its base height,
// the query input grows upward over the viewport, to about half of it, and
// the viewport auto-scrolls to compensate (LAYOUT.md's Chat workspace
// section; F04-query-input.md's Growth section).
//
// It never touches layout.Compute, layout.State or the Workspace interface:
// l.Main and l.Secondary already sum to the fixed row budget Root.View
// expects for the whole left column (Root pads/clips to exactly the
// terminal's height — growing one box without shrinking the other by the
// same amount would push the status bar down and off), so this only ever
// TRADES rows between the two, at the SAME width, using the two rects
// Compute already handed this workspace. The transcript's own wrapped
// content is untouched by this — fewer rows are simply drawn from it, and
// the existing pinned/bottom-anchored scroll (scroll.go) already keeps the
// newest message visible as `rows` shrinks, which is the auto-scroll half
// of the requirement, with zero extra state.
func (w *Workspace) splitGrowth(l layout.Layout) (mainRect, secRect layout.Rect) {
	totalH := l.Main.H + l.Secondary.H
	if totalH <= 0 || l.Secondary.H <= 0 {
		return l.Main, l.Secondary
	}

	contentWidth := (l.Secondary.W - 2) - 2*genericBoxPadding
	if contentWidth < 1 {
		contentWidth = 1
	}
	prefixW := visibleWidth(activePrefix(w.input.currentMode(), w.input.focused))
	textWidth := contentWidth - prefixW
	if textWidth < 1 {
		textWidth = 1
	}
	w.input.boxContentWidth = contentWidth
	w.input.textWidth = textWidth

	wantRows := w.input.buf.visualLineCount(textWidth)
	if len(w.input.chips) > 0 {
		wantRows++
	}
	wantSecondaryH := wantRows + 2 // border rows, top and bottom
	if wantSecondaryH < l.Secondary.H {
		// Never shrink below what Compute gave — that is the base height
		// (LAYOUT.md: "base height 1 row, grows with content"), not a
		// growth ceiling.
		wantSecondaryH = l.Secondary.H
	}

	// "to about half of it [the viewport]": the viewport may give up at
	// most half of its OWN nominal height, l.Main.H as Compute computed it
	// — not half of the combined total, and not a moving target as the
	// viewport itself shrinks. Past this cap the input stops growing and
	// goes internally scrollable instead (queryLines/ensureCursorVisible,
	// below).
	minMainH := (l.Main.H + 1) / 2
	maxSecondaryH := totalH - minMainH
	if maxSecondaryH < l.Secondary.H {
		maxSecondaryH = l.Secondary.H
	}
	if wantSecondaryH > maxSecondaryH {
		wantSecondaryH = maxSecondaryH
	}

	secondaryH := wantSecondaryH
	mainH := totalH - secondaryH

	mainRect = layout.Rect{X: l.Main.X, Y: l.Main.Y, W: l.Main.W, H: mainH}
	secRect = layout.Rect{X: l.Secondary.X, Y: l.Main.Y + mainH, W: l.Secondary.W, H: secondaryH}

	contentRows := secondaryH - 2
	if len(w.input.chips) > 0 {
		contentRows--
	}
	w.ensureCursorVisible(textWidth, contentRows)

	return mainRect, secRect
}

// ensureCursorVisible keeps the cursor's wrapped line inside
// [scrollTop, scrollTop+rows) — only relevant once growth has hit its cap
// (splitGrowth, above) and the input has gone internally scrollable, per
// F04-query-input.md: "past that maximum, the input becomes internally
// scrollable — no more growth."
func (w *Workspace) ensureCursorVisible(width, rows int) {
	if rows < 1 {
		rows = 1
	}
	lines := w.input.buf.lines(width)
	li := lineForCursor(lines, w.input.buf.cursor, w.input.buf.trailing)

	top := w.input.scrollTop
	if li < top {
		top = li
	}
	if li >= top+rows {
		top = li - rows + 1
	}
	maxTop := len(lines) - rows
	if maxTop < 0 {
		maxTop = 0
	}
	top = clampInt(top, 0, maxTop)
	w.input.scrollTop = top
}

// viewportLines is the View Port box's content: rows-2 lines, already fit to
// the box's own inner width.
func (w *Workspace) viewportLines(rect layout.Rect) []string {
	th := w.theme
	rows := rect.H - 2
	if rows <= 0 {
		return nil
	}
	contentWidth := (rect.W - 2) - 2*paneHorizontalPadding
	if contentWidth < 4 {
		contentWidth = 4
	}

	switch {
	case w.client == nil:
		return clip(centred(th.Style(theme.TextDim).Render("no backend connection"), rows, contentWidth), rows)
	case w.loadErr != "":
		return clip(centred(th.Style(theme.Error).Render("! "+w.loadErr), rows, contentWidth), rows)
	case w.loading || (!w.bootstrapped && w.sessionID == ""):
		return clip(centred(th.Style(theme.TextDim).Render("◐ loading session…"), rows, contentWidth), rows)
	case len(w.all) == 0:
		hint := th.Style(theme.TextDim).Render("type a message") + "   " +
			th.Style(theme.AccentPrimary).Render("^P palette")
		return clip(centred(hint, rows, contentWidth), rows)
	}

	_, lines := w.visibleWindow(rows, contentWidth)
	if !w.pinned {
		// Pad to the box's actual last row first — with few messages, the
		// last rendered line is not the last row, and the affordance
		// belongs in the corner of the box, not glued to the transcript.
		for len(lines) < rows {
			lines = append(lines, "")
		}
		hint := th.Style(theme.AccentPrimary).Render("↓ jump to latest (G)")
		lines[rows-1] = overlayRight(lines[rows-1], hint, contentWidth)
	}
	return lines
}

// overlayRight replaces the trailing cells of `line` with `note`, used for
// the jump-to-latest affordance in the bottom-right corner of the viewport.
func overlayRight(line, note string, width int) string {
	nw := visibleWidth(note)
	if nw >= width {
		return truncateCells(note, width)
	}
	left := truncateCells(line, width-nw-1)
	return padCells(left, width-nw-1) + " " + note
}

// queryLines is the Query Area box's content: the pinned attachment chips
// (if any), the mode chip, and the input's own wrapped, cursor-painted
// text — see input_render.go's renderInputLines, which does the actual
// work. boxContentWidth is splitGrowth's — the SAME number, not
// w.input.textWidth (which already has the mode chip's prefix subtracted;
// renderInputLines subtracts it again itself, so passing textWidth here
// would double-subtract).
func (w *Workspace) queryLines(rect layout.Rect) []string {
	rows := rect.H - 2
	if rows <= 0 {
		return nil
	}
	lines := renderInputLines(w.theme, &w.input.buf, w.input.currentMode(), w.input.chips,
		w.input.focused, w.input.boxContentWidth, rows, w.input.scrollTop)
	return clip(lines, rows)
}

func centred(s string, rows, width int) []string {
	out := make([]string, rows)
	mid := rows / 2
	for i := range out {
		if i == mid {
			w := visibleWidth(s)
			if w >= width {
				out[i] = truncateCells(s, width)
				continue
			}
			left := (width - w) / 2
			out[i] = strings.Repeat(" ", left) + s
		}
	}
	return out
}

func stackBlocks(blocks ...string) string {
	kept := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b != "" {
			kept = append(kept, b)
		}
	}
	return strings.Join(kept, "\n")
}

// ---------------------------------------------------------------------------
// Per-message rendering
// ---------------------------------------------------------------------------

// renderMessage dispatches by role. width is the content width available
// inside the viewport's own padding — the same number for every role; a user
// message spends some of it on its own box, an AI reply on its accent bar.
func renderMessage(th theme.Theme, msg wireMessage, width int, focused bool) []string {
	switch msg.Role {
	case roleUser:
		return renderUserMessage(th, msg, width, focused)
	case roleAssistant:
		return renderAssistantMessage(th, msg, width, focused)
	case roleSystem:
		return renderPlainRole(th, msg, width, theme.TextDim, "")
	case roleError:
		return renderPlainRole(th, msg, width, theme.Error, "")
	case roleTool:
		return renderPlainRole(th, msg, width, theme.TextDim, "⚙ ")
	default:
		return renderPlainRole(th, msg, width, theme.TextDim, "")
	}
}

// userBoxWidthRatio: the user's box occupies the rightmost ~72% of the
// viewport's content width — Design Change C, "70-75%" — with blank space
// on the left only. 0.72 sits inside that range; nothing in the spec picks
// a more exact number.
const userBoxWidthRatio = 0.72

func renderUserMessage(th theme.Theme, msg wireMessage, width int, focused bool) []string {
	boxWidth := int(float64(width) * userBoxWidthRatio)
	if boxWidth < 8 {
		boxWidth = width // too narrow to shrink meaningfully — use it all
	}
	leftMargin := width - boxWidth

	label := th.Style(theme.TextSecondary).Render("User:")
	// Right-aligned to the SAME edge the box is right-aligned to — the
	// viewport's right wall, not the box's own left edge — so the label
	// sits directly above the box regardless of leftMargin.
	labelLine := strings.Repeat(" ", max0(width-visibleWidth(label))) + label

	inner := boxWidth - 2*genericBoxPadding - 2 // border + padding, both sides
	if inner < 4 {
		inner = 4
	}
	body := renderMarkdown(th, msg.Content.Text, inner)

	edgeRole := theme.UserBorder
	if focused {
		edgeRole = theme.BorderFocus
	}
	box := messageBox(th.Style(edgeRole), boxWidth, body, genericBoxPadding)

	margin := strings.Repeat(" ", leftMargin)
	out := make([]string, 0, len(box)+1)
	out = append(out, padCells(labelLine, width))
	for _, l := range box {
		out = append(out, margin+l)
	}
	return out
}

func renderAssistantMessage(th theme.Theme, msg wireMessage, width int, focused bool) []string {
	label := th.Style(theme.TextSecondary).Render("Point:")

	bar := "▎ "
	barStyle := th.Style(theme.AIBar)
	if focused {
		barStyle = th.Style(theme.BorderFocus)
	}
	inner := width - visibleWidth(bar)
	if inner < 4 {
		inner = 4
	}
	body := renderMarkdown(th, msg.Content.Text, inner)

	out := make([]string, 0, len(body)+1)
	out = append(out, padCells(label, width))
	for _, l := range body {
		out = append(out, barStyle.Render("▎")+" "+l)
	}
	return out
}

func renderPlainRole(th theme.Theme, msg wireMessage, width int, role theme.Role, prefix string) []string {
	style := th.Style(role)
	text := prefix + msg.Content.Text
	words := splitWords(text, style)
	return wrapWords(words, width)
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// renderTaskSlot is the collapsed one-line placeholder a task reserves at
// send time. F4 (input) is what creates a slot and F5 (streaming) is what
// transitions its status; this episode builds neither, so nothing calls this
// yet. It exists because F3.md asks for the slot's OWN rendering to live
// here, ready for F4/F5 to invoke once a task can actually be started.
func renderTaskSlot(th theme.Theme, taskID, status string, width int) string {
	var role theme.Role
	var glyph string
	switch status {
	case "running":
		role, glyph = theme.Running, "▶"
	case "queued":
		role, glyph = theme.Queued, "◌"
	default:
		role, glyph = theme.TextDim, "·"
	}
	line := th.Style(role).Render(fmt.Sprintf("%s task %s — %s", glyph, short(taskID), status))
	return padCells(line, width)
}
