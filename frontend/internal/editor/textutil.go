package editor

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// This file is the THIRD copy of app\box.go's width primitives (chat\
// textutil.go is the second). The debt is real and already recorded twice:
// F1's handoff flagged a shared `ui` package as F3-or-F7 work, and chat's
// own copy says the same. Lifting them is still bigger than this episode's
// Build section, and doing it here would touch two shipped packages for no
// behaviour change. Noted in the handoff as still owed, now with three
// callers instead of two.

// tabStop is the display width of a literal tab, and there is deliberately
// only one number for it in Point: this is chat\textutil.go's constant,
// 8, matching every other tool that renders a tab in a terminal.
//
// It is NOT the same thing as the indent this editor INSERTS — that comes
// from the file's own detected style (see indentStyle). A file indented
// with four spaces still draws an embedded `\t` eight cells wide, because
// that is what the file says and what `cat` would show.
const tabStop = 8

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

// padCells pads or truncates to exactly width display cells, measuring the
// visible text rather than the escape sequences.
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

// truncateCells cuts to width display cells, preserving escape sequences
// that appear before the cut so colour is not left half-applied.
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

// clip joins at most rows lines.
func clip(lines []string, rows int) string {
	if rows <= 0 {
		return ""
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

// runeCells is the display width of one rune at display column col.
//
// A tab is the only rune whose width depends on where it sits: it advances
// to the next multiple of tabStop. Every cursor column, every selection
// rect and the status bar's `line:col` all go through this, which is why
// there is one function rather than a runewidth call at each site.
func runeCells(r rune, col int) int {
	if r == '\t' {
		return tabStop - col%tabStop
	}
	w := runewidth.RuneWidth(r)
	if w < 0 {
		return 0
	}
	return w
}

// displayCol is the display column of rune index col within runes.
func displayCol(runes []rune, col int) int {
	if col > len(runes) {
		col = len(runes)
	}
	out := 0
	for i := 0; i < col; i++ {
		out += runeCells(runes[i], out)
	}
	return out
}

// colAtDisplay is displayCol's inverse: the rune index whose display column
// is nearest to want without passing it.
//
// A click or a vertical move that lands in the middle of a wide rune (CJK,
// an emoji, a tab) resolves to the START of that rune rather than splitting
// it — there is no position between a double-width rune's two cells, and
// pretending there is one is how a cursor ends up rendered half a cell off
// its own text.
func colAtDisplay(runes []rune, want int) int {
	if want <= 0 {
		return 0
	}
	col := 0
	for i, r := range runes {
		w := runeCells(r, col)
		if col+w > want {
			return i
		}
		col += w
	}
	return len(runes)
}

// clampTop is the ONE place a scroll offset is bounded.
//
// The maximum is count-rows, NOT count-1, and that distinction is the
// whole of a reported rendering glitch in both the file tree and the
// code pane. With count-1, scrolling to the end of a list leaves ONE
// item visible however tall the viewport is -- measured, before the
// fix: a ten-line file in a twenty-row viewport (which should not
// scroll at all) came to rest with `top=9, 1 line visible`, and one
// wheel notch back showed four, which is exactly the "single-frame
// flash of the last 4 items" in the report. The wheel moves three
// lines at a time.
//
// It was the same expression in THREE places -- doc.scrollBy,
// doc.ensureVisible and fileTree.ensureVisible -- not a shared
// viewport component with one bug. Extracted here so the bound cannot
// be written three ways again, which is the same reason this episode
// derived effectiveFocus instead of storing it.
//
// rows < 1 is treated as 1: a viewport with no rows is a layout
// mid-resize, and returning a negative maximum would put top past the
// end.
func clampTop(top, count, rows int) int {
	if rows < 1 {
		rows = 1
	}
	max := count - rows
	if max < 0 {
		max = 0
	}
	if top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	return top
}

// centreCells centres one already-styled line in a field of display
// cells, truncating rather than overflowing when it does not fit.
//
// The fourth copy of a width primitive in this repo, and the only new
// one F13b adds -- app\render.go's `centre` is identical and
// unexported. See this file's header: the shared `ui` package F1's
// handoff flagged now has four callers waiting for it, and the button
// row of a three-button dialog is what finally needed this one.
func centreCells(s string, width int) string {
	w := visibleWidth(s)
	if w >= width {
		return truncateCells(s, width)
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-w-left)
}
