package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// This mirrors app\box.go's box() algorithm — rounded border, title on the
// top edge, LAYOUT.md's rule for every pane, popup, card and diff — as its
// own package-local copy for the reason textutil.go's doc comment gives.
const (
	borderTopLeft     = "╭"
	borderTopRight    = "╮"
	borderBottomLeft  = "╰"
	borderBottomRight = "╯"
	borderHorizontal  = "─"
	borderVertical    = "│"
)

// paneBox draws a titled box sized to exactly rect.W×rect.H, with `padding`
// cells between the border and content on each horizontal side — 2 for the
// View Port (LAYOUT.md's "viewport inner padding"), 1 (the generic rule)
// everywhere else in this package.
func paneBox(edge lipgloss.Style, label lipgloss.Style, rect layout.Rect, title string, contentLines []string, padding int) string {
	if rect.W < 2*padding+2 || rect.H < 2 {
		return ""
	}
	inner := rect.W - 2
	var b strings.Builder

	b.WriteString(edge.Render(borderTopLeft))
	switch {
	case title == "":
		b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
	default:
		maxTitle := inner - 4
		if maxTitle < 1 {
			b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
		} else {
			shown := runewidth.Truncate(title, maxTitle, "…")
			used := 3 + runewidth.StringWidth(shown)
			b.WriteString(edge.Render(borderHorizontal + " "))
			b.WriteString(label.Render(shown))
			b.WriteString(edge.Render(" "))
			if fill := inner - used; fill > 0 {
				b.WriteString(edge.Render(strings.Repeat(borderHorizontal, fill)))
			}
		}
	}
	b.WriteString(edge.Render(borderTopRight))

	rows := rect.H - 2
	pad := strings.Repeat(" ", padding)
	contentWidth := inner - 2*padding
	for i := 0; i < rows; i++ {
		b.WriteString("\n")
		b.WriteString(edge.Render(borderVertical))
		var text string
		if i < len(contentLines) {
			text = contentLines[i]
		}
		b.WriteString(pad + padCells(text, contentWidth) + pad)
		b.WriteString(edge.Render(borderVertical))
	}

	b.WriteString("\n")
	b.WriteString(edge.Render(borderBottomLeft))
	b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
	b.WriteString(edge.Render(borderBottomRight))
	return b.String()
}

// messageBox draws a titleless rounded box exactly `width` columns wide,
// wrapping contentLines — a user message's own box, per F3.md: "rounded box
// with 1-cell horizontal padding", never a title (only panes/cards/popups
// get one).
func messageBox(edge lipgloss.Style, width int, contentLines []string, padding int) []string {
	if width < 2*padding+2 {
		return nil
	}
	inner := width - 2
	pad := strings.Repeat(" ", padding)
	contentWidth := inner - 2*padding

	out := make([]string, 0, len(contentLines)+2)
	out = append(out, edge.Render(borderTopLeft+strings.Repeat(borderHorizontal, inner)+borderTopRight))
	for _, l := range contentLines {
		out = append(out, edge.Render(borderVertical)+pad+padCells(l, contentWidth)+pad+edge.Render(borderVertical))
	}
	out = append(out, edge.Render(borderBottomLeft+strings.Repeat(borderHorizontal, inner)+borderBottomRight))
	return out
}
