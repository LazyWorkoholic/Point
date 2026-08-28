package chat

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func testTheme() theme.Theme {
	return theme.New("turquoise", theme.BackgroundTransparent)
}

// The exact fixture F03-chat-viewport.md's manual verification sends:
// heading, bold, inline code, CJK, emoji, and a fenced block in an unknown
// language.
const verificationFixture = "# heading\n" +
	"**bold** and `inline` and 日本語テキスト and 🎉\n\n" +
	"```unknownlang\n" +
	"some text\n" +
	"```\n"

func TestVerificationFixtureNeverExceedsWidth(t *testing.T) {
	th := testTheme()
	for _, width := range []int{20, 40, 60, 76, 120} {
		lines := renderMarkdown(th, verificationFixture, width)
		for i, l := range lines {
			if w := visibleWidth(l); w > width {
				t.Fatalf("width %d: line %d is %d cells wide (%q)", width, i, w, stripANSI(l))
			}
		}
	}
}

func TestUnknownLanguageRendersPlainNeverErrors(t *testing.T) {
	th := testTheme()
	lines := renderMarkdown(th, verificationFixture, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(stripANSI(joined), "some text") {
		t.Fatalf("code block content missing from render:\n%s", stripANSI(joined))
	}
	// A rounded box was drawn around it.
	if !strings.Contains(joined, "╭") || !strings.Contains(joined, "╰") {
		t.Fatalf("no code box border found:\n%s", stripANSI(joined))
	}
}

func TestHeadingAndBoldAreStyled(t *testing.T) {
	th := testTheme()
	lines := renderMarkdown(th, verificationFixture, 76)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("expected ANSI styling in output, got none:\n%s", joined)
	}
	plain := stripANSI(joined)
	if !strings.Contains(plain, "heading") {
		t.Fatalf("heading text missing:\n%s", plain)
	}
	if !strings.Contains(plain, "bold") || !strings.Contains(plain, "inline") {
		t.Fatalf("bold/inline text missing:\n%s", plain)
	}
}

func TestCJKAndEmojiKeepDisplayWidthCorrect(t *testing.T) {
	th := testTheme()
	// Isolate the exact line the manual test worries about.
	lines := renderMarkdown(th, "日本語テキスト and 🎉", 76)
	plain := stripANSI(strings.Join(lines, "\n"))
	wantWidth := runewidth.StringWidth("日本語テキスト and 🎉")
	gotWidth := runewidth.StringWidth(strings.ReplaceAll(plain, "\n", ""))
	if gotWidth != wantWidth {
		t.Fatalf("display width changed across render: want %d got %d (%q)", wantWidth, gotWidth, plain)
	}
}

func TestListsRenderBulletsAndNumbers(t *testing.T) {
	th := testTheme()
	src := "- one\n- two\n\n1. first\n2. second\n"
	lines := renderMarkdown(th, src, 40)
	plain := stripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"• one", "• two", "1. first", "2. second"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in:\n%s", want, plain)
		}
	}
}

func TestLinksRenderText(t *testing.T) {
	th := testTheme()
	lines := renderMarkdown(th, "see [the docs](https://example.com/x) for more", 76)
	plain := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "the docs") {
		t.Fatalf("link text missing: %q", plain)
	}
}

// Real multi-line content, the shape a fixed echo provider now produces
// (backend\agents\executor.py's _echo_body, fixed for the newline-flattening
// bug found while retesting F3 — see design/completed/backend/B01-runtime.md's
// correction note): a heading, then a fenced code block on its OWN lines,
// separated by a real blank line. Before the fix this arrived as one run-on
// line and the fence was never recognised as a fence at all.
const multilineEchoFixture = "# heading\n\nSome text before.\n\n```python\ndef f(x):\n    return x + 1\n```\n\nSome text after.\n"

func TestMultilineFenceIsOneProperCodeBox(t *testing.T) {
	th := testTheme()
	lines := renderMarkdown(th, multilineEchoFixture, 76)
	joined := strings.Join(lines, "\n")
	plain := stripANSI(joined)

	if !strings.Contains(plain, "heading") {
		t.Fatalf("heading missing:\n%s", plain)
	}
	if !strings.Contains(plain, "Some text before.") || !strings.Contains(plain, "Some text after.") {
		t.Fatalf("surrounding prose missing:\n%s", plain)
	}
	// Exactly one code box: one top border, one bottom border.
	if got := strings.Count(joined, "╭"); got != 1 {
		t.Fatalf("expected exactly one code box top border, found %d:\n%s", got, plain)
	}
	if got := strings.Count(joined, "╰"); got != 1 {
		t.Fatalf("expected exactly one code box bottom border, found %d:\n%s", got, plain)
	}
	if !strings.Contains(plain, "def f(x):") || !strings.Contains(plain, "return x + 1") {
		t.Fatalf("code content missing or split across boxes:\n%s", plain)
	}
	// The two code lines must be INSIDE the one box, i.e. between the two
	// borders, not each in their own. All four indices are taken from the
	// same (ANSI-stripped) string so the positions are comparable.
	top := strings.Index(plain, "╭")
	bottom := strings.Index(plain, "╰")
	defLine := strings.Index(plain, "def f(x):")
	returnLine := strings.Index(plain, "return x + 1")
	if defLine < top || defLine > bottom || returnLine < top || returnLine > bottom {
		t.Fatalf("code lines fell outside the single box (top=%d bottom=%d def=%d return=%d)",
			top, bottom, defLine, returnLine)
	}
}

func TestEveryCodeBoxLineIsExactlyTheGivenWidth(t *testing.T) {
	th := testTheme()
	for _, width := range []int{20, 41, 60, 100} {
		lines := renderCodeBlock(th, "python", "def f(x):\n    return x + 1\n", width)
		for i, l := range lines {
			if w := visibleWidth(l); w != width {
				t.Fatalf("width %d: code box line %d is %d cells (%q)", width, i, w, stripANSI(l))
			}
		}
	}
}
