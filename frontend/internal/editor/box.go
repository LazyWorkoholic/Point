package editor

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// A package-local copy of app\box.go's box() — rounded border, title on the
// top edge, LAYOUT.md's rule for every pane, popup, card and diff — for the
// reason textutil.go's header gives.
const (
	borderTopLeft     = "╭"
	borderTopRight    = "╮"
	borderBottomLeft  = "╰"
	borderBottomRight = "╯"
	borderHorizontal  = "─"
	borderVertical    = "│"
)

// paneBox draws a titled box filling exactly rect.W×rect.H.
//
// `pad` is the cells between the border and the content on each horizontal
// side: 1 everywhere in this package (LAYOUT.md's generic rule). The Code
// File box takes its own gutter out of the content width instead of asking
// for more padding, because a line number IS the left margin.
func paneBox(edge, label lipgloss.Style, rect layout.Rect, title string, content []string, pad int) string {
	if rect.W < 4 || rect.H < 2 {
		return ""
	}
	inner := rect.W - 2
	var b strings.Builder

	b.WriteString(edge.Render(borderTopLeft))
	if maxTitle := inner - 4; title == "" || maxTitle < 1 {
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
	b.WriteString(edge.Render(borderTopRight))

	gap := strings.Repeat(" ", pad)
	body := inner - 2*pad
	for i := 0; i < rect.H-2; i++ {
		var text string
		if i < len(content) {
			text = content[i]
		}
		b.WriteString("\n")
		b.WriteString(edge.Render(borderVertical))
		if body > 0 {
			b.WriteString(gap + padCells(text, body) + gap)
		} else {
			b.WriteString(strings.Repeat(" ", inner))
		}
		b.WriteString(edge.Render(borderVertical))
	}

	b.WriteString("\n")
	b.WriteString(edge.Render(borderBottomLeft))
	b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
	b.WriteString(edge.Render(borderBottomRight))
	return b.String()
}

// stack joins pre-rendered blocks with no separator — LAYOUT.md's zero-row
// gap between stacked boxes.
func stack(blocks ...string) string {
	kept := make([]string, 0, len(blocks))
	for _, s := range blocks {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n")
}

// sideBySide joins two blocks column-wise with a gap of `gap` cells.
func sideBySide(left string, leftW int, right string, rightW, gap int) string {
	if right == "" {
		return left
	}
	l := strings.Split(left, "\n")
	r := strings.Split(right, "\n")

	n := len(l)
	if len(r) > n {
		n = len(r)
	}

	spacer := strings.Repeat(" ", gap)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		var lt, rt string
		if i < len(l) {
			lt = l[i]
		}
		if i < len(r) {
			rt = r[i]
		}
		out[i] = padCells(lt, leftW) + spacer + padCells(rt, rightW)
	}
	return strings.Join(out, "\n")
}
