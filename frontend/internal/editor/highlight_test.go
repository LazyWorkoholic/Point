package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// bigPythonFile is Done-when #1's shape: 5,000 lines averaging under
// twenty bytes, so the whole file lands under index.max_file_kb (100KB)
// and is therefore visible in the tree and openable through it. The gate
// script generates a real file the same way.
func bigPythonFile(lines int) string {
	var sb strings.Builder
	sb.Grow(lines * 19)
	for i := 0; i < lines; i++ {
		switch i % 5 {
		case 0:
			fmt.Fprintf(&sb, "def f%d(a, b):\n", i)
		case 1:
			fmt.Fprintf(&sb, "    x = %d + a\n", i)
		case 2:
			sb.WriteString("    if x > b:\n")
		case 3:
			fmt.Fprintf(&sb, "        return %q\n", "v")
		default:
			sb.WriteString("    return x\n")
		}
	}
	return sb.String()
}

func TestHighlighterMatchesALexerFromTheFilename(t *testing.T) {
	cases := map[string]string{
		"a.py":   "python",
		"a.go":   "go",
		"a.json": "json",
	}
	for name, want := range cases {
		if got := newHighlighter(name, "").name; got != want {
			t.Errorf("%s -> %q, want %q", name, got, want)
		}
	}
}

// F13: "unknown language renders plain". It holds by construction -- an
// unmatched filename falls through to chroma's plaintext lexer, which never
// errors -- so what this checks is that it renders rather than erroring,
// and that the status bar gets no language to claim.
func TestUnknownLanguageRendersPlain(t *testing.T) {
	h := newHighlighter("a.wat-is-dit", "")
	if h.name != "" {
		t.Errorf("name = %q, want empty for an unknown extension", h.name)
	}
	b := newLineBuffer("some ( arbitrary ] text\nsecond line")
	got := h.window(testTheme(), b, 0, 2)
	if len(got) != 2 {
		t.Fatalf("%d lines, want 2", len(got))
	}
	if string(runesOf(got[0])) != "some ( arbitrary ] text" {
		t.Errorf("line 0 = %q", string(runesOf(got[0])))
	}
}

func runesOf(line []styledRune) []rune {
	out := make([]rune, len(line))
	for i, sr := range line {
		out[i] = sr.r
	}
	return out
}

// Whatever the window does to colour, it must return the buffer's own
// runes for every line it reports -- the renderer falls back when the
// lengths disagree, and a silent mismatch would mean colour correct and
// text wrong.
func TestWindowReturnsTheBuffersOwnRunes(t *testing.T) {
	b := newLineBuffer(bigPythonFile(400))
	h := newHighlighter("big.py", "")
	th := testTheme()

	for _, top := range []int{0, 1, 50, 399} {
		for _, rows := range []int{1, 20, 60} {
			got := h.window(th, b, top, rows)
			for i, line := range got {
				want := b.Line(top + i)
				if string(runesOf(line)) != want {
					t.Fatalf("top %d rows %d line %d: %q, want %q",
						top, rows, i, string(runesOf(line)), want)
				}
			}
		}
	}
}

// Python keywords take the accent role, strings take `done`, comments take
// dim -- chat\codeblock.go's mapping, reproduced here (see codeTokenRole's
// doc for why it is reproduced rather than shared).
func TestKeywordsStringsAndCommentsGetDistinctRoles(t *testing.T) {
	th := testTheme()
	b := newLineBuffer("# note\nreturn \"text\"")
	h := newHighlighter("a.py", "")
	got := h.window(th, b, 0, 2)

	_ = th
	if got[0][0].role != theme.TextDim {
		t.Errorf("a comment is %q, want text.dim", got[0][0].role)
	}
	if got[1][0].role != theme.AccentPrimary {
		t.Errorf("a keyword is %q, want accent.primary", got[1][0].role)
	}
	// The opening quote of the string literal.
	if got[1][7].role != theme.Done {
		t.Errorf("a string literal is %q, want done", got[1][7].role)
	}
}

// The cache is keyed on the buffer's version. An edit must change what is
// drawn; nothing else decides "has it changed".
func TestCacheInvalidatesOnAnEdit(t *testing.T) {
	th := testTheme()
	b := newLineBuffer("x = 1")
	h := newHighlighter("a.py", "")
	h.window(th, b, 0, 1)

	b.Splice(Pos{0, 0}, Pos{0, 5}, "return 2")
	got := h.window(th, b, 0, 1)
	if string(runesOf(got[0])) != "return 2" {
		t.Fatalf("stale line after an edit: %q", string(runesOf(got[0])))
	}
	if got[0][0].role != theme.AccentPrimary {
		t.Error("the new keyword was not re-tokenised")
	}
}

// The cache holds ROLES, not resolved styles, so a theme change cannot
// leave stale colour in it -- the colour is resolved per run at render
// time (see styledRune's own doc for why it stopped being a Style).
//
// The themeName guard is kept and still asserted, because the cache is
// keyed on it: a redundant invalidation is cheaper than a key that can
// go stale if anything theme-dependent is ever cached here again.
// chat\cards.go's memo had the live version of that bug.
func TestCacheIsThemeIndependentAndStillKeyedOnIt(t *testing.T) {
	b := newLineBuffer("return 1")
	h := newHighlighter("a.py", "")

	turquoise := theme.New("turquoise", theme.BackgroundTransparent)
	gold := theme.New("gold", theme.BackgroundTransparent)

	if role := h.window(turquoise, b, 0, 1)[0][0].role; role != theme.AccentPrimary {
		t.Fatalf("role = %q", role)
	}
	if h.themeName != turquoise.Name() {
		t.Errorf("themeName = %q", h.themeName)
	}
	if role := h.window(gold, b, 0, 1)[0][0].role; role != theme.AccentPrimary {
		t.Errorf("role changed with the theme: %q", role)
	}
	if h.themeName != gold.Name() {
		t.Errorf("themeName did not follow the theme: %q", h.themeName)
	}

	// And the same role resolves to two different colours, which is the
	// property that makes holding the role rather than the style safe.
	if sameStyle(turquoise.Style(theme.AccentPrimary), gold.Style(theme.AccentPrimary)) {
		t.Error("two palettes resolve accent.primary identically; the test proves nothing")
	}
}

// The scroll path takes the margin and the edit path does not. Asserted
// through the cached RANGE rather than through timing, which is the only
// stable way to say it.
func TestScrollTakesTheMarginAndAnEditDoesNot(t *testing.T) {
	th := testTheme()
	b := newLineBuffer(bigPythonFile(2000))
	h := newHighlighter("big.py", "")

	// A fresh buffer version: the edit path, so exactly the window.
	h.window(th, b, 500, 30)
	if h.from != 500 || h.to != 530 {
		t.Fatalf("edit path cached [%d,%d), want [500,530)", h.from, h.to)
	}

	// Same version, scrolled out of the cached range: the scroll path,
	// so the window plus the margin either side.
	h.window(th, b, 900, 30)
	if h.from != 900-hlMargin || h.to != 930+hlMargin {
		t.Errorf("scroll path cached [%d,%d), want [%d,%d)",
			h.from, h.to, 900-hlMargin, 930+hlMargin)
	}

	// And an edit drops back to the bare window.
	b.Splice(Pos{900, 0}, Pos{900, 0}, "#")
	h.window(th, b, 900, 30)
	if h.from != 900 || h.to != 930 {
		t.Errorf("after an edit cached [%d,%d), want [900,930)", h.from, h.to)
	}
}

// Scrolling WITHIN the cached range must not re-tokenise at all -- that is
// what the margin buys.
func TestScrollingInsideTheMarginReusesTheCache(t *testing.T) {
	th := testTheme()
	b := newLineBuffer(bigPythonFile(2000))
	h := newHighlighter("big.py", "")

	h.window(th, b, 500, 30)
	h.window(th, b, 900, 30) // takes the margin
	from, to := h.from, h.to

	for top := 900; top < 900+hlMargin-30; top += 17 {
		h.window(th, b, top, 30)
		if h.from != from || h.to != to {
			t.Fatalf("top %d re-tokenised: [%d,%d) -> [%d,%d)", top, from, to, h.from, h.to)
		}
	}
}

// ---------------------------------------------------------------------------
// Done-when #1 — the open half, measured
// ---------------------------------------------------------------------------

// BenchmarkHighlightWindow is the number the window rule is justified by.
// It is the per-frame highlighting cost on the EDIT path -- a fresh buffer
// version, so the cache is cold and the bare window is tokenised.
func BenchmarkHighlightWindow(b *testing.B) {
	th := testTheme()
	buf := newLineBuffer(bigPythonFile(5000))
	h := newHighlighter("big.py", "")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Bump the version so every iteration is a cold window, which is
		// what a keystroke produces.
		buf.Splice(Pos{0, 0}, Pos{0, 0}, "")
		h.ver = 0
		h.window(th, buf, 2000, 30)
	}
}

// BenchmarkHighlightWholeFile is what the window rule AVOIDS, kept beside
// it so the ratio is visible rather than asserted in prose.
func BenchmarkHighlightWholeFile(b *testing.B) {
	th := testTheme()
	buf := newLineBuffer(bigPythonFile(5000))
	h := newHighlighter("big.py", "")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ver = 0
		h.tokenise(th, buf, 0, buf.Lines())
	}
}

// BenchmarkKeystroke is the whole per-keystroke cost: the splice, the
// undo record, and one rendered frame of the Code File box.
func BenchmarkKeystroke(b *testing.B) {
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:\\p\\big.py", Content: bigPythonFile(5000),
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true))

	l := layoutFor(ws, 120, 30)
	ws.View(l)
	d := ws.docs[0]
	d.moveTo(Pos{2500, 4}, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.insertRune('x')
		ws.codeFileBox(l.Main)
	}
}

// BenchmarkScrollFrame is the per-frame cost while scrolling, where the
// margin is supposed to make the highlighter free.
func BenchmarkScrollFrame(b *testing.B) {
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:\\p\\big.py", Content: bigPythonFile(5000),
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true))

	l := layoutFor(ws, 120, 30)
	ws.View(l)
	d := ws.docs[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.scrollBy(plainIndex(d), 1)
		if d.top > 4900 {
			d.top = 0
		}
		ws.codeFileBox(l.Main)
	}
}
