package editor

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Syntax highlighting: chroma, over a window rather than the whole file.
//
// # Why a window at all
//
// F13 asks for "the visible window plus a margin, not the whole file --
// this is what keeps a 5k-line file smooth", and the measurement is the
// reason. On an i5-1135G7, tokenising a 5,000-line Python file costs
// 302ms; tokenising a 30-line window costs about 3ms. Those are
// BenchmarkHighlightWholeFile and BenchmarkHighlightWindow, both in this
// package, and they are the only basis on which anything here claims to be
// faster than anything else. A 302ms frame per keystroke is not a slow
// editor, it is an unusable one.
//
// Chroma is regex-driven and its cost is per LINE of source, not per byte:
// roughly 110us a line for Python at both sizes, so the window is cheap
// because it is 30 lines rather than because it is short.
//
// # The window is still the per-keystroke floor, and that was measured too
//
// A verification round found that the whole-frame cost mattered more than
// this one number suggested: app\paste.go reconstructs a paste by timing
// the gap between Update calls (6ms), so a frame slower than that stops
// pastes from being recognised at all. Measured over 400 keystrokes in a
// 5,000-line file, before this episode's render work: p50 6.0ms, p90
// 9.0ms, max 18.3ms, and HALF of all frames over the 6ms gate. After it
// (roles instead of styles here, run-coalescing in render.go, and
// buffer.go's single-line Splice fast path): p50 3.5ms, p90 5.9ms, p99
// 7.1ms, max 8.1ms, 6.75% over the gate.
//
// The ~3ms tokenise is what is left, and cutting it further means
// re-tokenising less than the visible window -- which means lexing a line
// without the lines above it, and that is a visible colour regression
// rather than an invisible one. Not taken. See the episode's notes.
//
// # The cache rule, and why it has two cases
//
//	version CHANGED   -> tokenise exactly the visible window
//	version UNCHANGED -> tokenise the window plus hlMargin either side
//
// The two cases exist because the two paths have opposite shapes. An edit
// invalidates whatever is cached and must do the least possible work before
// the next frame, so it tokenises only what is about to be drawn. A scroll
// does not invalidate anything, so the margin is pure win there: it is
// re-tokenised once and then reused for however many frames it takes to
// scroll out of it.
//
// Keying on the buffer's own Version is what makes this safe. Nothing else
// in this package decides "has it changed"; a cache that compared line
// counts or hashes would be a second answer to a question the buffer
// already answers exactly.
//
// # The accepted limitation
//
// A window does not know what came before it. A construct that OPENS above
// the window and closes below it -- a Python triple-quoted string, a C
// block comment, a Go raw literal -- is tokenised as if it started at the
// top of the window, so its first screenful colours as code rather than as
// a string. This is accepted for v1, not a bug to chase: the alternative is
// tokenising from byte zero on every scroll, which is the cost the window
// exists to avoid, and the only real fix is an incremental lexer with
// per-line state carried forward (tree-sitter, which F13 puts post-v1).
// Scrolling past the opener and back re-tokenises from a different top and
// the colour corrects itself, which is why this reads as a transient
// oddity rather than as damage.
const hlMargin = 300

// styledRune is one display cell's worth of code, carrying the ROLE the
// lexer resolved it to -- not a lipgloss.Style.
//
// Rendering works at this grain -- the same discipline chat\codeblock.go
// documents -- for two reasons here. Horizontal scrolling has to cut a line
// at a display column without cutting an escape sequence in half, and the
// selection has to repaint arbitrary rune ranges on top of whatever the
// lexer decided. Both are trivial over a slice of runes, and both are
// fiddly string surgery otherwise.
//
// # Why a Role and not a Style
//
// It was a lipgloss.Style first, and that was measurably wrong.
// lipgloss.Style is a large struct (it carries every property it can set,
// plus a renderer pointer), and one per rune meant the window cache copied
// about thirty words per character of visible code on every rebuild. A
// theme.Role is a short string, the cache gets an order of magnitude
// smaller, and the style is resolved once per RUN at render time rather
// than once per rune at tokenise time.
//
// It also removed the reason the cache had to be invalidated on a theme
// change: a role is theme-independent. The themeName guard stays anyway,
// because the cache is keyed on it and a stale key is worse than a
// redundant one.
type styledRune struct {
	r    rune
	role theme.Role
}

// highlighter is one document's lexer plus its window cache.
type highlighter struct {
	lexer chroma.Lexer
	// name is the lexer's own name, lowercased, for the status bar. Empty
	// when nothing matched the filename and the plaintext fallback is in
	// use -- F13's "unknown language renders plain", which holds by
	// construction here rather than by a special case.
	name string

	ver      uint64
	from, to int
	lines    [][]styledRune

	// themeName is baked into the cached styles, so a theme change has to
	// invalidate them. chat\cards.go's memo had exactly this bug found
	// live; this is the same guard, declared at the same time as the cache
	// rather than after someone notices the colours stopped changing.
	themeName string
}

func newHighlighter(filename, langHint string) *highlighter {
	h := &highlighter{from: -1, to: -1}
	lexer := lexers.Match(filename)
	if lexer == nil && langHint != "" {
		lexer = lexers.Get(langHint)
	}
	if lexer == nil {
		h.lexer = chroma.Coalesce(lexers.Fallback)
		return h
	}
	h.name = strings.ToLower(lexer.Config().Name)
	h.lexer = chroma.Coalesce(lexer)
	return h
}

// window returns one styled slice per buffer line in [top, top+rows),
// tokenising and caching as the rule above describes.
func (h *highlighter) window(th theme.Theme, b Buffer, top, rows int) [][]styledRune {
	if rows <= 0 {
		return nil
	}
	if top < 0 {
		top = 0
	}
	end := top + rows
	if end > b.Lines() {
		end = b.Lines()
	}
	if top >= end {
		return nil
	}

	stale := h.ver != b.Version() || h.themeName != th.Name()
	if stale || top < h.from || end > h.to {
		lo, hi := top, end
		if !stale {
			// The scroll path: pay for the margin, because this slab is
			// about to be reused for many frames.
			lo, hi = top-hlMargin, end+hlMargin
			if lo < 0 {
				lo = 0
			}
			if hi > b.Lines() {
				hi = b.Lines()
			}
		}
		h.tokenise(th, b, lo, hi)
	}

	out := make([][]styledRune, 0, end-top)
	for i := top; i < end; i++ {
		if i < h.from || i >= h.to {
			out = append(out, plainLine(b.Runes(i)))
			continue
		}
		out = append(out, h.lines[i-h.from])
	}
	return out
}

// tokenise fills the cache for [lo, hi).
func (h *highlighter) tokenise(th theme.Theme, b Buffer, lo, hi int) {
	h.ver = b.Version()
	h.themeName = th.Name()
	h.from, h.to = lo, hi
	h.lines = make([][]styledRune, hi-lo)

	var sb strings.Builder
	for i := lo; i < hi; i++ {
		if i > lo {
			sb.WriteByte('\n')
		}
		sb.WriteString(b.Line(i))
	}

	iter, err := h.lexer.Tokenise(nil, sb.String())
	if err != nil {
		// A lexer that cannot tokenise its own input renders plain rather
		// than rendering nothing. Reachable in principle for a
		// pathological file; the fallback lexer never errors.
		for i := lo; i < hi; i++ {
			h.lines[i-lo] = plainLine(b.Runes(i))
		}
		return
	}

	row := 0
	cur := make([]styledRune, 0, 80)
	for _, tok := range iter.Tokens() {
		role := codeTokenRole(tok.Type)
		for _, r := range tok.Value {
			if r == '\n' {
				if row < len(h.lines) {
					h.lines[row] = cur
				}
				row++
				cur = make([]styledRune, 0, 80)
				continue
			}
			cur = append(cur, styledRune{r: r, role: role})
		}
	}
	if row < len(h.lines) {
		h.lines[row] = cur
	}
	// Any line the tokeniser did not reach (an empty tail) is an empty
	// slice rather than nil, so the renderer never has to tell them apart.
	for i := range h.lines {
		if h.lines[i] == nil {
			h.lines[i] = []styledRune{}
		}
	}
}

func plainLine(runes []rune) []styledRune {
	out := make([]styledRune, len(runes))
	for i, r := range runes {
		out[i] = styledRune{r: r, role: theme.CodeText}
	}
	return out
}

// codeTokenRole maps a chroma token category onto one of Point's EXISTING
// roles.
//
// This is chat\codeblock.go's mapping, deliberately reproduced rather than
// shared, and the reason is narrower than textutil.go's: codeTokenRole
// there is unexported in a package that renders fenced blocks inside chat
// messages, and exporting it would make this package import chat -- a
// workspace importing another workspace, which is the one dependency the
// Workspace contract forbids outright ("workspaces never talk to each
// other"). The alternative, a shared package that imports chroma so theme
// does not have to, is the same `ui` package the width primitives are
// already owed and is no more this episode's to create.
//
// THEME.md gives code exactly three roles (border, text, inline). There is
// no keyword/string/comment role, and adding one is a THEME.md change -- a
// new column on all four palettes -- that F13 does not make. Reusing
// general-purpose roles that already read as distinct hues gets real
// syntax colour without a new role.
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
