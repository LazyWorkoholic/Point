package chat

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// This file duplicates app\box.go's width primitives rather than importing
// them: they are unexported, and lifting them into a shared `ui` package —
// which F1's handoff flagged as F3-or-F7 work — is bigger than this
// episode's Build section asks for. Noted in the handoff as still owed.

// visibleWidth is the display width of a string, ignoring ANSI escapes.
func visibleWidth(s string) int {
	return runewidth.StringWidth(stripANSI(s))
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

// padCells pads or truncates a possibly-styled string to exactly width
// display cells, measuring visible width so a styled string keeps its colour
// and still lands on the right column.
func padCells(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := visibleWidth(s)
	switch {
	case w == width:
		return s
	case w < width:
		return s + strings.Repeat(" ", width-w)
	default:
		return truncateCells(s, width)
	}
}

// truncateCells cuts to width display cells, preserving escape sequences that
// appear before the cut so colour is never left half-applied.
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

// clip keeps at most rows lines.
func clip(lines []string, rows int) []string {
	if rows <= 0 {
		return nil
	}
	if len(lines) > rows {
		return lines[:rows]
	}
	return lines
}

// tabStop is the column interval expandTabs aligns to.
//
// The VALUE is cosmetic; the expansion is not. Once the tabs are gone, what
// this package measures and what the terminal draws agree whatever stop is
// used — which is the entire point. 4 is the common convention for code and
// costs less of a narrow box than the terminal's own default of 8.
const tabStop = 4

// expandTabs replaces tabs with spaces, aligned to the next tab stop.
//
// runewidth.StringWidth reports a tab as ZERO cells; every terminal draws it
// as up to eight. Preformatted text carrying tabs therefore measured as
// fitting and then overflowed on screen, wrapping to the far left — the
// symptom reads as broken wrapping, but the wrapping was correct and the
// measurement was wrong.
//
// Prose escapes this because splitWords goes through strings.Fields, which
// drops tabs entirely. Code blocks, an unclosed fence's raw tail and a
// thinking block all keep their text verbatim, so they are exactly the paths
// that need this.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n':
			b.WriteRune(r)
			col = 0
		default:
			b.WriteRune(r)
			col += runewidth.RuneWidth(r)
		}
	}
	return b.String()
}
