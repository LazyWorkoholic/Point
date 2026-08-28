package app

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Rounded border characters. LAYOUT.md: rounded borders everywhere, with the
// title on the top edge — `╭─ View Port ────╮` — for every pane, popup, card
// and diff.
//
// The border is drawn here rather than by lipgloss because lipgloss v1 has no
// notion of a title on a border edge, and every box in Point has one.
const (
	borderTopLeft     = "╭"
	borderTopRight    = "╮"
	borderBottomLeft  = "╰"
	borderBottomRight = "╯"
	borderHorizontal  = "─"
	borderVertical    = "│"
)

// box renders content inside a rounded border with a title, sized to exactly
// r.W columns and r.H rows.
//
// Every width here is a DISPLAY width, not a byte or rune count. CJK and emoji
// occupy two cells, and getting this wrong puts the right border one or two
// cells off on one line — which is the single most common way a TUI layout
// breaks.
func box(th theme.Theme, r layout.Rect, title, content string, focused bool) string {
	if r.W < 4 || r.H < 2 {
		return ""
	}

	role := theme.BorderIdle
	if focused {
		role = theme.BorderFocus
	}
	edge := th.Style(role)
	label := th.Style(theme.TextSecondary)
	if focused {
		label = th.Style(theme.AccentPrimary)
	}

	inner := r.W - 2
	var b strings.Builder

	// ╭─ Title ──────╮
	b.WriteString(edge.Render(borderTopLeft))
	if title == "" {
		b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
	} else {
		// "─ " + title + " " then fill. Truncate the title before the fill so
		// a long one shortens rather than pushing the corner off the end.
		maxTitle := inner - 4
		if maxTitle < 1 {
			b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
		} else {
			shown := runewidth.Truncate(title, maxTitle, "…")
			used := 3 + runewidth.StringWidth(shown) // "─ " + title + " "
			b.WriteString(edge.Render(borderHorizontal + " "))
			b.WriteString(label.Render(shown))
			b.WriteString(edge.Render(" "))
			if fill := inner - used; fill > 0 {
				b.WriteString(edge.Render(strings.Repeat(borderHorizontal, fill)))
			}
		}
	}
	b.WriteString(edge.Render(borderTopRight))

	// Content rows. Border → content is one cell horizontally, zero rows
	// vertically (LAYOUT.md), so the padding is a leading and trailing space
	// and no blank rows.
	rows := r.H - 2
	lines := strings.Split(content, "\n")
	for i := 0; i < rows; i++ {
		b.WriteString("\n")
		b.WriteString(edge.Render(borderVertical))

		var text string
		if i < len(lines) {
			text = lines[i]
		}
		b.WriteString(fitCells(text, inner))
		b.WriteString(edge.Render(borderVertical))
	}

	// ╰──────────────╯
	b.WriteString("\n")
	b.WriteString(edge.Render(borderBottomLeft))
	b.WriteString(edge.Render(strings.Repeat(borderHorizontal, inner)))
	b.WriteString(edge.Render(borderBottomRight))

	return b.String()
}

// fitCells pads or truncates a possibly-styled line to exactly width cells,
// with the one-cell horizontal padding LAYOUT.md specifies already applied.
func fitCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if width <= 2 {
		return strings.Repeat(" ", width)
	}
	body := padCells(text, width-2)
	return " " + body + " "
}

// padCells pads or truncates to exactly width display cells.
//
// It measures the visible text, not the escape sequences, so a styled string
// keeps its colour and still lands on the right column.
func padCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	w := visibleWidth(text)
	switch {
	case w == width:
		return text
	case w < width:
		return text + strings.Repeat(" ", width-w)
	default:
		return truncateCells(text, width)
	}
}

// visibleWidth is the display width of a string, ignoring ANSI escapes.
func visibleWidth(s string) int {
	return runewidth.StringWidth(stripANSI(s))
}

// truncateCells cuts to width display cells, preserving escape sequences that
// appear before the cut so colour is not left half-applied.
func truncateCells(s string, width int) string {
	var (
		b     strings.Builder
		used  int
		inEsc bool
	)
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		rw := runewidth.RuneWidth(r)
		if used+rw > width {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	for used < width {
		b.WriteByte(' ')
		used++
	}
	return b.String()
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stack joins pre-rendered blocks with no separator, which is the zero-row gap
// between stacked boxes.
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
//
// Blocks of unequal height are padded to the taller one; each block's lines
// are padded to its own width first, so the right column starts on the same
// column on every row.
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
