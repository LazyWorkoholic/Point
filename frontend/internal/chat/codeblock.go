package chat

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// renderCodeBlock draws one fenced code block as a rounded code.border box,
// no title — LAYOUT.md's "every box has a title on the top edge" rule is
// about panes, popups, cards and diffs; a code fence inside a message is
// none of those, and a title on every code block in a long reply would be
// noise repeated per block.
//
// width is the OUTER width, matching every other line in the message — the
// border columns are included in it, not added on top of it.
func renderCodeBlock(th theme.Theme, lang, code string, width int) []string {
	// Tabs measure as zero cells and draw as up to eight — see textutil.go's
	// expandTabs. A fenced block is their commonest carrier, and one left
	// unexpanded overflows the box drawn around it.
	code = expandTabs(strings.TrimRight(code, "\n"))
	if width < 4 {
		return []string{padCells(code, width)}
	}
	inner := width - 2
	border := th.Style(theme.CodeBorder)

	out := make([]string, 0, 2)
	out = append(out, border.Render("╭"+strings.Repeat("─", inner)+"╮"))
	for _, line := range highlightLines(th, lang, code, inner) {
		out = append(out, border.Render("│")+padCells(line, inner)+border.Render("│"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

// codeTokenRole maps a chroma token category onto one of Point's EXISTING
// roles. THEME.md gives code exactly three roles (border, text, inline) —
// there is no keyword/string/comment role, and adding one is a THEME.md
// change (a new column on all four palettes) this episode does not make.
// Reusing general-purpose roles that already read as distinct hues gets real
// syntax colouring without a new role: keywords and builtins take the
// accent roles (the same ones a focused border or the streaming indicator
// use, but a code block is never focused at the same time as anything else
// competing for attention), strings reuse `done` (green across all four
// palettes), comments and punctuation fall back to dim/base text.
func codeTokenRole(t chroma.TokenType) theme.Role {
	switch {
	case t.InCategory(chroma.Comment):
		return theme.TextDim
	case t.InCategory(chroma.LiteralString):
		return theme.Done
	case t.InCategory(chroma.LiteralNumber):
		return theme.AccentSecondary
	case t.InCategory(chroma.Keyword):
		return theme.AccentPrimary
	case t.SubCategory() == chroma.NameFunction || t.SubCategory() == chroma.NameBuiltin:
		return theme.InlineCode
	case t.InCategory(chroma.Generic) && t.SubCategory() == chroma.GenericError:
		return theme.Error
	default:
		return theme.CodeText
	}
}

// styledRune is one display cell's worth of code, pre-resolved to a style.
// Wrapping and rendering both work at this grain so a colour never gets cut
// mid-escape-sequence — the same discipline app\placeholder.go's wrapPlain
// comment documents for plain text, extended here to styled runs.
type styledRune struct {
	r     rune
	style lipgloss.Style
}

// highlightLines tokenises code with chroma and returns one already-wrapped,
// already-styled line per output row. An unrecognised language (lexers.Get
// returns nil) falls back to lexers.Fallback, chroma's plaintext lexer, which
// never errors — "unknown languages render plain, never error" holds by
// construction, not by a special case here.
func highlightLines(th theme.Theme, lang, code string, width int) []string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return wrapPlainCode(th, code, width)
	}

	var lines []string
	var cur []styledRune
	flush := func() {
		lines = append(lines, wrapStyledRunes(cur, width)...)
		cur = nil
	}
	for _, tok := range iterator.Tokens() {
		style := th.Style(codeTokenRole(tok.Type))
		for _, r := range tok.Value {
			if r == '\n' {
				flush()
				continue
			}
			cur = append(cur, styledRune{r, style})
		}
	}
	flush()
	return lines
}

func wrapPlainCode(th theme.Theme, code string, width int) []string {
	style := th.Style(theme.CodeText)
	var lines []string
	var cur []styledRune
	flush := func() {
		lines = append(lines, wrapStyledRunes(cur, width)...)
		cur = nil
	}
	for _, r := range code {
		if r == '\n' {
			flush()
			continue
		}
		cur = append(cur, styledRune{r, style})
	}
	flush()
	return lines
}

// wrapStyledRunes cell-wraps a styled code line. Code wraps by cell, not by
// word — a long line with no spaces (a minified line, a URL, a long
// identifier) still has to stay inside the box.
func wrapStyledRunes(runes []styledRune, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	var lines []string
	var b strings.Builder
	used := 0
	flushLine := func() {
		lines = append(lines, b.String())
		b.Reset()
		used = 0
	}
	for _, sr := range runes {
		w := runewidth.RuneWidth(sr.r)
		if used+w > width && used > 0 {
			flushLine()
		}
		b.WriteString(sr.style.Render(string(sr.r)))
		used += w
	}
	if used > 0 || len(lines) == 0 {
		flushLine()
	}
	return lines
}
