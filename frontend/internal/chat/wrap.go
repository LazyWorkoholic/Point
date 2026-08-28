package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// word is one wrap unit: a run of non-whitespace runes carrying the style of
// whichever inline span it came from. Wrapping decides breaks from `width`
// (a plain display-width measurement) and calls Render exactly once per word
// — never on a string that later gets concatenated mid-escape, which is the
// bug LAYOUT.md's wrap-width note and app\placeholder.go's wrapPlain comment
// both warn about.
type word struct {
	text  string
	style lipgloss.Style
	width int
}

// splitWords turns one styled run of text into words, preserving the style on
// every word it produces. Internal whitespace is the only thing split on —
// multiple spaces collapse to one word boundary, matching how Markdown
// treats them.
func splitWords(text string, style lipgloss.Style) []word {
	fields := strings.Fields(text)
	words := make([]word, 0, len(fields))
	for _, f := range fields {
		words = append(words, word{text: f, style: style, width: runewidth.StringWidth(f)})
	}
	return words
}

// wrapWords greedily fills lines up to width display cells, joining words
// with a single unstyled space. A word wider than width by itself is hard-
// broken by cell, which is what keeps a long CJK run or a pasted URL from
// pushing a box's right border off column — LAYOUT.md's "wrap width = inner
// width − 4" rule exists because of exactly this case.
func wrapWords(words []word, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	var cur []word
	used := 0

	flush := func() {
		if len(cur) == 0 {
			return
		}
		var b strings.Builder
		for i, w := range cur {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(w.style.Render(w.text))
		}
		lines = append(lines, b.String())
		cur = cur[:0]
		used = 0
	}

	for _, w := range words {
		if w.width > width {
			flush()
			lines = append(lines, hardBreak(w, width)...)
			continue
		}
		add := w.width
		if used > 0 {
			add++ // separating space
		}
		if used > 0 && used+add > width {
			flush()
			add = w.width
		}
		cur = append(cur, w)
		used += add
	}
	flush()
	return lines
}

// hardBreak cuts a single overlong word into width-cell chunks, cell-exact —
// the same rune-width accounting truncateCells uses, so a run of CJK or
// emoji breaks on a cell boundary rather than mid-rune.
func hardBreak(w word, width int) []string {
	var lines []string
	var b strings.Builder
	used := 0
	for _, r := range w.text {
		rw := runewidth.RuneWidth(r)
		if used+rw > width && used > 0 {
			lines = append(lines, w.style.Render(b.String()))
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += rw
	}
	if b.Len() > 0 {
		lines = append(lines, w.style.Render(b.String()))
	}
	return lines
}
