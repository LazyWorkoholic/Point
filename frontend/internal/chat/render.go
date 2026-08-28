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
	edge := th.Style(theme.BorderIdle)
	label := th.Style(theme.TextSecondary)

	return stackBlocks(
		paneBox(edge, label, l.Main, "View Port", w.viewportLines(l.Main), paneHorizontalPadding),
		paneBox(edge, label, l.Secondary, "Query Area", w.queryLines(l.Secondary), genericBoxPadding),
	)
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

func (w *Workspace) queryLines(rect layout.Rect) []string {
	rows := rect.H - 2
	if rows <= 0 {
		return nil
	}
	return clip([]string{w.theme.Style(theme.TextDim).Render("nothing can be typed yet — F4")}, rows)
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
