package chat

import (
	"reflect"
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Every rune index in the buffer belongs to exactly one wrapped line, and
// the ranges are contiguous — the property everything else in this file
// (lineAt, colOf, posAtColumn) assumes without re-checking.
func TestWrapInputRunesPartitionsExactly(t *testing.T) {
	cases := []string{
		"",
		"short",
		"a line that is long enough to wrap across more than one row of a narrow box",
		"hard\nbreaks\nin\nhere",
		"trailing space at the end of a line   ",
		"日本語のテキストです、長い一行のテスト",
		"hello world", // wraps at a space, once width forces it below
		"helloworld",  // wraps mid-word, no space anywhere
	}
	for _, s := range cases {
		runes := []rune(s)
		lines := wrapInputRunes(runes, 12)
		if len(lines) == 0 {
			t.Fatalf("%q: no lines produced", s)
		}
		if lines[0].Start != 0 {
			t.Fatalf("%q: first line starts at %d, want 0", s, lines[0].Start)
		}
		if got := lines[len(lines)-1].End; got != len(runes) {
			t.Fatalf("%q: last line ends at %d, want %d", s, got, len(runes))
		}
		for i := 1; i < len(lines); i++ {
			gap := lines[i].Start - lines[i-1].End
			if gap < 0 {
				t.Fatalf("%q: line %d starts at %d, before the previous line ends at %d",
					s, i, lines[i].Start, lines[i-1].End)
			}
			// A gap is a consumed separator between the two lines -- a
			// newline (always exactly one rune) or, since Bug 14, a
			// wrap-consumed whitespace run (one or more). Either way,
			// EVERY rune in the gap must be whitespace: a line never
			// simply stops short of content it could have held.
			for k := lines[i-1].End; k < lines[i].Start; k++ {
				if !unicode.IsSpace(runes[k]) {
					t.Fatalf("%q: gap [%d,%d) between lines %d and %d contains non-whitespace %q",
						s, lines[i-1].End, lines[i].Start, i-1, i, runes[k])
				}
			}
		}
	}
}

// Bug 14: a line that wraps exactly AT a space must not keep that space as
// its own trailing, invisible content -- it becomes a gap between the two
// lines, exactly like the newline between paragraphs, so End/Left/Right/
// Up/Down all treat the boundary as hard rather than landing on a rendered
// blank column nobody can see.
func TestWrapConsumesASpaceAtTheWrapPointAsAGap(t *testing.T) {
	runes := []rune("hello world")
	lines := wrapInputRunes(runes, 6) // "hello" (5) + space (1) = 6, fits; "world" doesn't
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if got := string(runes[lines[0].Start:lines[0].End]); got != "hello" {
		t.Fatalf("line 0 = %q, want %q (the trailing space must not be included)", got, "hello")
	}
	if got := string(runes[lines[1].Start:lines[1].End]); got != "world" {
		t.Fatalf("line 1 = %q, want %q", got, "world")
	}
	if lines[1].Start != lines[0].End+1 {
		t.Fatalf("gap is %d rune(s), want exactly 1 (the space)", lines[1].Start-lines[0].End)
	}
	if atSoftBoundary(lines, lines[0].End) {
		t.Fatal("a space-consumed boundary must not read as a soft boundary")
	}
}

// The mid-word case must be UNCHANGED by the space fix: no space exists to
// consume, so the boundary stays a true soft wrap (gap 0), same as before.
func TestWrapMidWordStaysASoftBoundary(t *testing.T) {
	runes := []rune("helloworld")
	lines := wrapInputRunes(runes, 6)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if lines[1].Start != lines[0].End {
		t.Fatalf("gap is %d rune(s), want 0 (mid-word wrap, nothing to consume)", lines[1].Start-lines[0].End)
	}
	if !atSoftBoundary(lines, lines[0].End) {
		t.Fatal("a mid-word wrap must still read as a soft boundary")
	}
}

// Bug 15: a separating whitespace run WIDER than the box itself must still
// become one whole gap, not be hard-broken like an overlong word into a
// line of nothing but blank columns.
func TestWrapConsumesAnOverwideSpaceRunAsOneGap(t *testing.T) {
	runes := []rune("aaaaaaaaaa" + strings.Repeat(" ", 20) + "bbbbb") // 10 a's, 20 spaces, 5 b's
	lines := wrapInputRunes(runes, 15)                                // the 20-space run alone exceeds width 15
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if got := string(runes[lines[0].Start:lines[0].End]); got != "aaaaaaaaaa" {
		t.Fatalf("line 0 = %q, want %q (no trailing spaces)", got, "aaaaaaaaaa")
	}
	if got := string(runes[lines[1].Start:lines[1].End]); got != "bbbbb" {
		t.Fatalf("line 1 = %q, want %q (no leading spaces)", got, "bbbbb")
	}
	if gap := lines[1].Start - lines[0].End; gap != 20 {
		t.Fatalf("gap is %d rune(s), want 20 (the whole run, preserved, just not rendered)", gap)
	}
	for k := lines[0].End; k < lines[1].Start; k++ {
		if runes[k] != ' ' {
			t.Fatalf("gap rune at %d = %q, want a space -- content must be preserved verbatim, not collapsed", k, runes[k])
		}
	}
}

// Trailing whitespace with NOTHING after it in the paragraph is real,
// standalone content -- not a wrap artifact -- and still hard-breaks
// normally if it is wider than the box, exactly like an overlong word.
func TestOverwideTrailingSpaceAtParagraphEndIsNotGapped(t *testing.T) {
	runes := []rune("aaaaaaaaaa" + strings.Repeat(" ", 20)) // nothing follows
	lines := wrapInputRunes(runes, 15)
	if got := lines[len(lines)-1].End; got != len(runes) {
		t.Fatalf("last line ends at %d, want %d (the buffer end) — trailing content must not be dropped", got, len(runes))
	}
	// It is real, visible content: some line must actually render at least
	// one of these trailing spaces (unlike the gapped, separating case
	// above, where NO line renders any of the run).
	rendered := 0
	for _, l := range lines {
		for k := l.Start; k < l.End; k++ {
			if k >= 10 { // past the a's
				rendered++
			}
		}
	}
	if rendered != 20 {
		t.Fatalf("rendered %d of the 20 trailing spaces, want all 20 (standalone content, not gapped)", rendered)
	}
}

// A single token wider than the box (a long unbroken run, or CJK where every
// rune is 2 cells) is hard-broken cell by cell — the same rule wrap.go's
// hardBreak uses for the transcript, and for the same reason: an unbroken
// run must not push the box's border off column.
func TestWrapHardBreaksAnOverlongToken(t *testing.T) {
	runes := []rune("supercalifragilisticexpialidocious")
	lines := wrapInputRunes(runes, 8)
	for i, l := range lines {
		w := 0
		for _, r := range runes[l.Start:l.End] {
			w += runewidth.RuneWidth(r)
		}
		if w > 8 {
			t.Fatalf("line %d is %d cells wide, want <= 8", i, w)
		}
	}
	if len(lines) < 4 {
		t.Fatalf("got %d lines for a 35-rune word at width 8, expected several", len(lines))
	}
}

func TestWrapPreservesHardNewlines(t *testing.T) {
	runes := []rune("one\ntwo\nthree")
	lines := wrapInputRunes(runes, 100)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	want := []string{"one", "two", "three"}
	for i, w := range want {
		if got := string(runes[lines[i].Start:lines[i].End]); got != w {
			t.Fatalf("line %d = %q, want %q", i, got, w)
		}
	}
}

func TestMoveUpDownPreservesColumnAcrossWrap(t *testing.T) {
	b := newInputBuffer()
	// At width 5: "abcde" / "fghij" / "klm" — three wrapped lines, no
	// explicit newlines, so this exercises the SOFT-wrap boundary case in
	// lineAt.
	b.InsertText("abcdefghijklm")
	width := 5

	b.cursor = 7 // 'h', column 2 of line 1 ("fghij")
	b.MoveUp(false, width)
	if b.cursor != 2 { // column 2 of line 0 ("abcde") -> 'c'
		t.Fatalf("MoveUp: cursor = %d, want 2", b.cursor)
	}
	b.MoveDown(false, width)
	if b.cursor != 7 {
		t.Fatalf("MoveDown: cursor = %d, want 7 (back to the same column)", b.cursor)
	}

	// Line 2 ("klm") is shorter than column 2 would ask for at line widths
	// 5 — moving down from a longer line onto a shorter one clamps to the
	// short line's own end rather than overshooting into the next line.
	b.cursor = 9 // column 4 of line 1 ("fghij")
	b.MoveDown(false, width)
	if b.cursor != 13 { // line 2 is only 3 runes; clamp to its end
		t.Fatalf("MoveDown onto a short line: cursor = %d, want 13", b.cursor)
	}
}

// A cursor sitting EXACTLY on a soft-wrap boundary belongs to the START of
// the next line, never the end of the previous one — lineAt's own
// documented convention. Bug 6 (a verification round): the input's
// rendering used to let each wrapped line decide independently whether it
// owned the caret at this exact position, so BOTH the previous line (via a
// trailing caret-space) and the next line (via its own first rune) drew
// one — visually indistinguishable from "Home landed on the wrong line."
// This is the single source of truth both MoveHome/MoveEnd and the
// renderer (input_render.go's renderInputLines) now read the SAME way.
func TestLineAtBoundaryBelongsToTheNextLine(t *testing.T) {
	lines := wrapInputRunes([]rune("abcdefghij"), 5) // {0,5} "abcde", {5,10} "fghij"
	boundary := lines[0].End
	if got := lineAt(lines, boundary); got != 1 {
		t.Fatalf("lineAt(boundary) = %d, want 1 (start of the next line)", got)
	}
}

func TestMoveHomeEndOnWrappedLine(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("abcdefghij") // width 5: "abcde" / "fghij"
	width := 5

	b.cursor = 7
	b.MoveHome(false, width)
	if b.cursor != 5 {
		t.Fatalf("MoveHome: cursor = %d, want 5 (start of second wrapped line)", b.cursor)
	}
	b.MoveEnd(false, width)
	if b.cursor != 10 {
		t.Fatalf("MoveEnd: cursor = %d, want 10", b.cursor)
	}
}

// Home/End on a HARD-broken line (Alt+Enter) go to the line's own start/end
// — the '\n' itself, not the following paragraph — matching lineAt's
// documented convention for a position sitting exactly on that boundary.
func TestMoveHomeEndAroundHardNewline(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("first\nsecond")
	width := 1000

	b.cursor = 2 // inside "first"
	b.MoveEnd(false, width)
	if b.cursor != 5 { // just before the \n
		t.Fatalf("MoveEnd: cursor = %d, want 5", b.cursor)
	}

	b.cursor = 8 // inside "second"
	b.MoveHome(false, width)
	if b.cursor != 6 { // just after the \n, start of "second"
		t.Fatalf("MoveHome: cursor = %d, want 6", b.cursor)
	}
}

// Bug 6, re-reported: End on a SOFT-WRAPPED line landed on the first letter
// of the NEXT line, and pressing it again walked down another line each
// time. The rune index was right; what was missing was the cursor's
// affinity at the boundary (inputBuffer.trailing).
func TestMoveEndOnSoftWrappedLineStaysOnThatLine(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("abcdefghijklm") // width 5: "abcde" / "fghij" / "klm"
	width := 5
	lines := wrapInputRunes(b.runes, width)

	// From position 0, End must reach the end of the FIRST visual line and
	// render there — not on the next line's first letter.
	b.cursor = 0
	b.MoveEnd(false, width)
	if b.cursor != 5 {
		t.Fatalf("End from 0: cursor = %d, want 5", b.cursor)
	}
	if got := lineForCursor(lines, b.cursor, b.trailing); got != 0 {
		t.Fatalf("End from 0 renders on line %d, want 0", got)
	}

	// Pressing End again must not walk to the next line.
	b.MoveEnd(false, width)
	if b.cursor != 5 {
		t.Fatalf("End twice: cursor = %d, want 5 (idempotent on a line)", b.cursor)
	}
	if got := lineForCursor(lines, b.cursor, b.trailing); got != 0 {
		t.Fatalf("End twice renders on line %d, want 0", got)
	}

	// Home from there goes to the start of that SAME visual line.
	b.MoveHome(false, width)
	if b.cursor != 0 {
		t.Fatalf("Home after End: cursor = %d, want 0", b.cursor)
	}

	// From mid-line, End reaches that line's end, not the line below.
	b.cursor = 7 // inside "fghij"
	b.trailing = false
	b.MoveEnd(false, width)
	if b.cursor != 10 {
		t.Fatalf("End from mid-line: cursor = %d, want 10", b.cursor)
	}
	if got := lineForCursor(lines, b.cursor, b.trailing); got != 1 {
		t.Fatalf("End from mid-line renders on line %d, want 1", got)
	}
}

// The hard-newline case (Ctrl+J) was already correct and must stay so.
func TestMoveHomeEndWithHardNewlinesUnchanged(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("first\nsecond")
	width := 1000

	b.cursor = 2
	b.MoveEnd(false, width)
	if b.cursor != 5 {
		t.Fatalf("End on a hard-broken line: cursor = %d, want 5", b.cursor)
	}
	b.MoveEnd(false, width)
	if b.cursor != 5 {
		t.Fatalf("End twice: cursor = %d, want 5", b.cursor)
	}
	b.MoveHome(false, width)
	if b.cursor != 0 {
		t.Fatalf("Home: cursor = %d, want 0", b.cursor)
	}

	b.cursor = 8
	b.trailing = false
	b.MoveHome(false, width)
	if b.cursor != 6 {
		t.Fatalf("Home on the second paragraph: cursor = %d, want 6", b.cursor)
	}
	b.MoveEnd(false, width)
	if b.cursor != 12 {
		t.Fatalf("End on the second paragraph: cursor = %d, want 12", b.cursor)
	}
}

// Bug 11: vertical movement must carry cursor affinity. Pressing End on a
// soft-wrapped line and then Up used to land on the line above at that
// line's End index -- the same rune index as the CURRENT line's Start -- and
// beginMove had just cleared the affinity, so the caret drew at the first
// letter of the line it started on. It only did so on lines wide enough for
// the column to reach their end, which is why it looked intermittent.
// allMovementOps is every inputBuffer movement function, wired to a common
// signature so it can be run generically. TestEveryMoveMethodIsRegistered,
// below, enumerates inputBuffer's actual method set via reflection and
// fails if a Move* method exists that is not in this table -- Bug 6
// (Home/End), Bug 11 (Up/Down) and Bug 13 (Left/Right) were the same
// affinity omission discovered three separate times, once per new
// movement function; this is what stops a fourth from being silently
// exempt from the scrutiny the first three needed.
var allMovementOps = map[string]func(b *inputBuffer, extend bool, width int){
	"MoveLeft":      func(b *inputBuffer, extend bool, width int) { b.MoveLeft(extend, width) },
	"MoveRight":     func(b *inputBuffer, extend bool, width int) { b.MoveRight(extend, width) },
	"MoveWordLeft":  func(b *inputBuffer, extend bool, width int) { b.MoveWordLeft(extend) },
	"MoveWordRight": func(b *inputBuffer, extend bool, width int) { b.MoveWordRight(extend) },
	"MoveUp":        func(b *inputBuffer, extend bool, width int) { b.MoveUp(extend, width) },
	"MoveDown":      func(b *inputBuffer, extend bool, width int) { b.MoveDown(extend, width) },
	"MoveHome":      func(b *inputBuffer, extend bool, width int) { b.MoveHome(extend, width) },
	"MoveEnd":       func(b *inputBuffer, extend bool, width int) { b.MoveEnd(extend, width) },
}

func TestEveryMoveMethodIsRegistered(t *testing.T) {
	typ := reflect.TypeOf(&inputBuffer{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Move") {
			continue
		}
		if _, ok := allMovementOps[name]; !ok {
			t.Errorf("%s is a new movement function not registered in allMovementOps -- "+
				"add it there, decide what affinity it should leave the cursor with "+
				"(see beginMove's doc), and add it to the boundary sweep below", name)
		}
	}
	// The reverse direction: everything registered must still exist, so a
	// renamed or removed method does not leave a stale, silently-unused entry.
	for name := range allMovementOps {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("allMovementOps registers %s, which no longer exists on inputBuffer", name)
		}
	}
}

// Bug 13, swept across EVERY soft-wrap boundary in a multi-line buffer, not
// just the one hand-picked case a bug report happened to describe.
//
// End sets trailing=true (Bug 6's fix). From there, Right must land on the
// first letter of the next line -- one rendered column right of "just past
// the last letter of this line" -- not the second, which is what a plain
// cursor++ produced: the boundary index already reads as the next line's
// first character once read leadingly, so incrementing past it skips one.
// wrapKindCases covers the two ways a line can END at a wrap point: AT a
// space (the wrap-consumed-space gap Bug 14 fixed) and MID-WORD (no space
// involved at all, a plain soft boundary, unaffected by that fix). The
// earlier round's sweep only ever used the mid-word text below, which is
// exactly why the space case survived it -- every boundary-sweep test from
// here on runs against BOTH.
var wrapKindCases = []struct {
	name  string
	text  string
	width int
}{
	{
		name: "mid-word wrap (no space at any boundary)",
		// 26 letters, no spaces at all: every wrap point falls strictly
		// inside a word.
		text: "abcdefghijklmnopqrstuvwxyz", width: 5,
	},
	{
		name: "space wrap (every boundary is a single consumed space)",
		// Five 5-letter words separated by single spaces, at a width that
		// makes each word-plus-space fit EXACTLY (5+1=6) before the next
		// word overflows -- the precise shape Bug 14 was reported against:
		// the space is absorbed into `used` before the flush realizes it
		// should not have been rendered.
		text: "aaaaa bbbbb ccccc ddddd eeeee", width: 6,
	},
	{
		name: "multi-space wrap (a whole run wider than the box is the gap)",
		// Five 5-letter words separated by RUNS of 8 spaces each, at a
		// width narrower than one run (6 < 8) -- Bug 15's exact shape: the
		// separating run's own width exceeds the box, so it used to hit
		// the tokWidth>width hard-break case (meant for an overlong WORD)
		// instead of being gapped, rendering an entire line of nothing but
		// individually-landable blank space columns.
		text: "aaaaa        bbbbb        ccccc        ddddd        eeeee", width: 6,
	},
}

func TestRightFromEndOfEverySoftWrappedLineLandsOnFirstLetter(t *testing.T) {
	for _, wk := range wrapKindCases {
		t.Run(wk.name, func(t *testing.T) {
			b := newInputBuffer()
			b.InsertText(wk.text)
			width := wk.width
			lines := b.lines(width)
			if len(lines) < 3 {
				t.Fatalf("setup: only %d lines, want several", len(lines))
			}

			for li := 0; li < len(lines); li++ {
				last := li == len(lines)-1
				b.setCursor(lines[li].Start, false)
				b.MoveEnd(false, width)
				// Every line but the last is a genuine boundary; MoveEnd
				// sets trailing from atSoftBoundary, which is true for a
				// zero-width gap and false for a consumed-space gap (Bug
				// 14) or the buffer's very end. Either way this must match.
				wantTrailing := !last && atSoftBoundary(lines, lines[li].End)
				if b.trailing != wantTrailing {
					t.Fatalf("line %d (last=%v): MoveEnd left trailing=%v, want %v", li, last, b.trailing, wantTrailing)
				}

				b.MoveRight(false, width)

				if last {
					if b.cursor != len(b.runes) {
						t.Errorf("line %d (last): Right after End landed at %d, want end of buffer %d",
							li, b.cursor, len(b.runes))
					}
					continue
				}
				wantPos := lines[li+1].Start
				if b.cursor != wantPos {
					t.Errorf("line %d: Right after End landed at cursor %d, want %d (first letter of the next line)",
						li, b.cursor, wantPos)
				}
				if b.trailing {
					t.Errorf("line %d: Right after End left trailing=true -- should read leading, on the next line's first letter", li)
				}
				gotLine := lineForCursor(lines, b.cursor, b.trailing)
				if gotLine != li+1 {
					t.Errorf("line %d: Right after End renders on line %d, want %d", li, gotLine, li+1)
				}
				if col := colOf(b.runes, lines[li+1], b.cursor); col != 0 {
					t.Errorf("line %d: Right after End is at column %d of the next line, want 0 (first letter)", li, col)
				}
			}
		})
	}
}

// Left's mirror, swept the same way. Left has no analogous bug: decrementing
// from a boundary always lands unambiguously on the previous line's own
// last character, regardless of which affinity it started from -- this
// confirms that explicitly rather than only by omission, for both wrap
// kinds.
func TestLeftFromEndOfEverySoftWrappedLineLandsOnLastLetterOfSameLine(t *testing.T) {
	for _, wk := range wrapKindCases {
		t.Run(wk.name, func(t *testing.T) {
			b := newInputBuffer()
			b.InsertText(wk.text)
			width := wk.width
			lines := b.lines(width)

			for li := 0; li < len(lines); li++ {
				if lines[li].End == lines[li].Start {
					continue // an empty line has no "last letter" to land on
				}
				b.setCursor(lines[li].Start, false)
				b.MoveEnd(false, width)

				b.MoveLeft(false, width)

				wantPos := lines[li].End - 1
				if b.cursor != wantPos {
					t.Errorf("line %d: Left after End landed at cursor %d, want %d (last letter of the same line)",
						li, b.cursor, wantPos)
				}
				if b.trailing {
					t.Errorf("line %d: Left after End left trailing=true", li)
				}
				if gotLine := lineForCursor(lines, b.cursor, b.trailing); gotLine != li {
					t.Errorf("line %d: Left after End renders on line %d, want %d (stayed on the same line)", li, gotLine, li)
				}
				// Bug 14's exact symptom, named directly: the landing rune
				// must be VISIBLE content, never a wrap-consumed space --
				// with the gap fix that space no longer lives inside
				// [Start,End) at all, so this should never trip.
				if wantPos != lines[li].Start && unicode.IsSpace(b.runes[wantPos]) {
					t.Errorf("line %d: Left after End landed on whitespace %q at %d -- the wrap space was not consumed as a gap",
						li, string(b.runes[wantPos]), wantPos)
				}
			}
		})
	}
}

// From an End position on every line of both wrap kinds, EVERY registered
// movement operation must leave the cursor in range and, if it claims
// trailing affinity, at a position where that is actually true -- the
// invariant a future movement function (or a future wrap change) could
// violate the same way Bug 13's MoveRight, and Bug 14's un-gapped space,
// both did.
func TestEveryMovementFromAnEndPositionStaysConsistent(t *testing.T) {
	for _, wk := range wrapKindCases {
		t.Run(wk.name, func(t *testing.T) {
			setup := newInputBuffer()
			setup.InsertText(wk.text)
			width := wk.width
			lines := setup.lines(width)

			for li := 0; li < len(lines); li++ {
				for name, op := range allMovementOps {
					b := newInputBuffer()
					b.InsertText(wk.text)
					b.setCursor(lines[li].Start, false)
					b.MoveEnd(false, width)

					op(&b, false, width)

					if b.cursor < 0 || b.cursor > len(b.runes) {
						t.Errorf("%s from End of line %d: cursor %d out of range [0,%d]",
							name, li, b.cursor, len(b.runes))
					}
					if b.trailing {
						got := b.lines(width)
						if !atSoftBoundary(got, b.cursor) {
							t.Errorf("%s from End of line %d: left trailing=true at cursor %d, which is not a soft boundary",
								name, li, b.cursor)
						}
					}
				}
			}
		})
	}
}

func TestVerticalMovementCarriesAffinity(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("abcdefghijklmno") // width 5: "abcde"/"fghij"/"klmno"
	width := 5
	lines := b.lines(width)
	if len(lines) != 3 {
		t.Fatalf("setup: got %d lines, want 3", len(lines))
	}

	// From End of the middle line, Up must render on the line ABOVE.
	b.cursor = 7
	b.trailing = false
	b.MoveEnd(false, width) // cursor 10, trailing, end of "fghij"
	b.MoveUp(false, width)
	if got := lineForCursor(b.lines(width), b.cursor, b.trailing); got != 0 {
		t.Fatalf("Up from End of line 1 renders on line %d, want 0 (cursor=%d trailing=%v)",
			got, b.cursor, b.trailing)
	}

	// And Down from an End position renders on the line BELOW.
	b.cursor = 2
	b.trailing = false
	b.MoveEnd(false, width) // end of "abcde"
	b.MoveDown(false, width)
	if got := lineForCursor(b.lines(width), b.cursor, b.trailing); got != 1 {
		t.Fatalf("Down from End of line 0 renders on line %d, want 1 (cursor=%d trailing=%v)",
			got, b.cursor, b.trailing)
	}

	// Mid-line vertical movement is unaffected and stays leading.
	b.cursor = 7 // column 2 of "fghij"
	b.trailing = false
	b.MoveUp(false, width)
	if b.cursor != 2 {
		t.Fatalf("Up from mid-line: cursor = %d, want 2", b.cursor)
	}
	if b.trailing {
		t.Fatal("mid-line Up should not set the trailing affinity")
	}
	b.MoveDown(false, width)
	if b.cursor != 7 {
		t.Fatalf("Down back from mid-line: cursor = %d, want 7", b.cursor)
	}
}

// Hard-newline text has no ambiguous boundary, so vertical movement there
// must never set the affinity at all.
func TestVerticalMovementWithHardNewlinesTakesNoAffinity(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("one\ntwo\nthree")
	width := 100

	b.cursor = 5 // inside "two"
	b.MoveEnd(false, width)
	b.MoveUp(false, width)
	if b.trailing {
		t.Fatal("a hard-broken line must not take the soft-wrap affinity")
	}
	if got := lineForCursor(b.lines(width), b.cursor, b.trailing); got != 0 {
		t.Fatalf("Up rendered on line %d, want 0", got)
	}
}

// Bug 15's gap-skip fix in MoveRight/MoveLeft has two edges that are NOT
// gaps: the very start and the very end of the buffer, where lineContaining
// also reports "not found" but for an entirely different reason (nothing
// exists there at all, not a consumed separator between two real lines).
// Confusing the two crashed MoveLeft outright (lines[-1]) and made
// MoveRight land one short of the true end.
func TestMoveRightLeftAtBufferEdgesDoNotMistakeOverflowForAGap(t *testing.T) {
	for _, wk := range wrapKindCases {
		t.Run(wk.name, func(t *testing.T) {
			b := newInputBuffer()
			b.InsertText(wk.text)
			width := wk.width

			b.setCursor(0, false)
			b.MoveLeft(false, width) // must not panic (lines[-1])
			if b.cursor != 0 {
				t.Fatalf("Left at buffer start: cursor = %d, want 0", b.cursor)
			}

			b.setCursor(len(b.runes), false)
			b.MoveRight(false, width)
			if b.cursor != len(b.runes) {
				t.Fatalf("Right at buffer end: cursor = %d, want %d", b.cursor, len(b.runes))
			}
		})
	}
}

// The incremental re-wrap must produce byte-for-byte the same lines a full
// wrap would, for every edit position -- a prefix reused wrongly would put
// the cursor somewhere the text is not.
func TestIncrementalRewrapMatchesAFullWrap(t *testing.T) {
	widths := []int{5, 13, 40}
	edits := []struct {
		name string
		do   func(b *inputBuffer)
	}{
		{"append", func(b *inputBuffer) { b.InsertText("appended text at the end") }},
		{"append newline", func(b *inputBuffer) { b.InsertText("\na new paragraph") }},
		{"insert at start", func(b *inputBuffer) { b.cursor = 0; b.InsertText("start ") }},
		{"insert mid", func(b *inputBuffer) { b.cursor = 20; b.InsertText(" MID ") }},
		{"delete mid", func(b *inputBuffer) { b.cursor = 25; b.Backspace() }},
		{"cjk", func(b *inputBuffer) { b.InsertText("日本語のテキスト") }},
	}

	for _, w := range widths {
		for _, e := range edits {
			b := newInputBuffer()
			b.InsertText("first paragraph with several words in it\nsecond paragraph also with words\nthird")
			_ = b.lines(w) // prime the cache
			e.do(&b)
			got := b.lines(w)
			want := wrapInputRunes(b.runes, w)

			if len(got) != len(want) {
				t.Fatalf("width %d, %s: %d lines incrementally, %d in a full wrap",
					w, e.name, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("width %d, %s: line %d = %+v, full wrap says %+v",
						w, e.name, i, got[i], want[i])
				}
			}
		}
	}
}

// A width change must discard the incremental prefix entirely.
func TestWrapCacheInvalidatesOnWidthChange(t *testing.T) {
	b := newInputBuffer()
	b.InsertText("a paragraph long enough to wrap differently at different widths")
	narrow := append([]inputLine(nil), b.lines(7)...)
	b.InsertText(" more")
	wide := b.lines(40)

	if len(wide) >= len(narrow) {
		t.Fatalf("width change did not re-wrap: %d lines at 40, %d at 7", len(wide), len(narrow))
	}
	want := wrapInputRunes(b.runes, 40)
	if len(wide) != len(want) {
		t.Fatalf("got %d lines, full wrap says %d", len(wide), len(want))
	}
}

// Bug 16: typing a SPACE right at a wrap boundary invisibly widens the gap
// it lands next to. splice sets the cursor by plain arithmetic
// (lo+len(repl)) with no idea a wrap gap exists there, unlike MoveRight/
// MoveLeft, which deliberately skip over one -- so the new index can land
// one past the OLD gap's edge and still be inside the new, wider one.
// resolveCaretPosition (input_render.go) is the fix: it must still resolve
// a visible column on some line no matter how the cursor got there.
func TestInsertingASpaceAtAWrapBoundaryStillRendersACaret(t *testing.T) {
	width := 6
	b := newInputBuffer()
	b.InsertText("aaaaa" + strings.Repeat(" ", 8) + "bbbbb") // line0 "aaaaa", gap [5,13), line1 "bbbbb"

	b.cursor = 0
	b.trailing = false
	b.MoveEnd(false, width)
	if b.cursor != 5 || b.trailing {
		t.Fatalf("setup: End landed at cursor=%d trailing=%v, want 5/false", b.cursor, b.trailing)
	}

	b.InsertText(" ") // widens the gap to [5,14); splice leaves cursor at 6 -- inside it
	if b.cursor != 6 {
		t.Fatalf("after insert: cursor = %d, want 6 (splice's lo+len(repl))", b.cursor)
	}

	lines := wrapInputRunes(b.runes, width)
	cursorLine, displayCursor := resolveCaretPosition(lines, b.cursor, b.trailing)
	l := lines[cursorLine]
	if displayCursor < l.Start || displayCursor > l.End {
		t.Fatalf("cursor %d has no visible column on resolved line %+v (gap swallowed the caret)", b.cursor, l)
	}
	if displayCursor != l.Start {
		t.Fatalf("displayCursor = %d, want %d (the following line's own first rune, same landing MoveRight uses for a gap)", displayCursor, l.Start)
	}
}

// Bug 16, via Undo/Redo rather than a live edit: inputSnapshot.cursor is
// exactly as arithmetic as splice's lo+len(repl) -- a plain saved int, no
// gap awareness at all. A snapshot recorded (or replayed) against a
// different wrap than the one currently on screen can restore a cursor
// that used to sit cleanly on a line and now sits inside a gap.
func TestUndoLandingInAWrapGapStillRendersACaret(t *testing.T) {
	width := 6
	text := "aaaaa" + strings.Repeat(" ", 8) + "bbbbb" // gap [5,13) at width 6

	b := newInputBuffer()
	b.setText(text, 0)
	// Install an undo snapshot directly, the same shape Undo() itself
	// would restore from (inputSnapshot{text, cursor}) -- cursor 8 sits
	// strictly inside the gap above, something no movement function would
	// ever produce but a snapshot captured under a different wrap width
	// (or a different buffer state) can.
	b.undo = append(b.undo, inputSnapshot{text: text, cursor: 8})

	b.Undo()
	if b.cursor != 8 {
		t.Fatalf("Undo cursor = %d, want 8 (restored verbatim from the snapshot)", b.cursor)
	}

	lines := wrapInputRunes(b.runes, width)
	cursorLine, displayCursor := resolveCaretPosition(lines, b.cursor, b.trailing)
	l := lines[cursorLine]
	if displayCursor < l.Start || displayCursor > l.End {
		t.Fatalf("cursor %d has no visible column on resolved line %+v (gap swallowed the caret)", b.cursor, l)
	}
}

// Bug 17: a verification round suspected splice's cursor arithmetic
// (lo+len(repl)) could drift once it starts landing inside a gap it just
// widened, the same mechanism as Bug 16 but surviving the edit rather than
// only being a render problem. Traced before fixing anything (per the
// round's own request): it does not. Every single insert in a run starting
// from a gap-adjacent cursor, with no intervening move, leaves b.cursor
// exactly equal to (start position + characters typed so far) -- a plain
// manual recount -- even while it is genuinely landing inside a
// progressively wider gap (confirmed via lineContaining returning -1 at
// every step). The real Bug 17 was never in splice; see draft.go's
// FlushDraft doc for where it actually was (app.quit had no synchronous
// flush of the debounced draft save).
func TestTypingASequenceFromAGapAdjacentCursorMatchesManualRecount(t *testing.T) {
	width := 6
	b := newInputBuffer()
	b.InsertText("aaaaa" + strings.Repeat(" ", 8) + "bbbbb") // gap [5,13) at width 6

	b.cursor = 0
	b.trailing = false
	b.MoveEnd(false, width) // the gap-adjacent boundary itself
	if b.cursor != 5 || b.trailing {
		t.Fatalf("setup: End landed at cursor=%d trailing=%v, want 5/false", b.cursor, b.trailing)
	}

	start := b.cursor
	for i := 1; i <= 4; i++ {
		b.InsertText(" ") // no intervening move -- exactly the repro's shape
		want := start + i
		if b.cursor != want {
			t.Fatalf("insert #%d: cursor = %d, manual recount says %d (start %d + %d typed)",
				i, b.cursor, want, start, i)
		}
		// It DOES land inside a gap at every step -- that is Bug 16's
		// territory (rendering), not this test's. The point here is that
		// the raw index itself, landing there or not, is never ambiguous.
		lines := wrapInputRunes(b.runes, width)
		if lineContaining(lines, b.cursor) != -1 {
			t.Fatalf("insert #%d: test setup stopped reproducing a gap-interior cursor (cursor=%d)", i, b.cursor)
		}
	}
}

// Bug 17b: a verification round reported N inserts followed by N backspaces
// NOT returning to the starting cursor at a trailing-space wrap gap
// boundary (89 -> 5 backspaces -> 88, not 84) -- insertion and deletion had
// only ever been tested separately (Bug 17's round-trip covered inserts
// alone; nothing exercised delete-then-recheck). Traced exhaustively before
// touching any code: splice's delete arithmetic (Backspace -> splice(cursor
// -1, cursor, nil) -> cursor = lo+len(nil) = cursor-1) is just as
// unconditional as its insert arithmetic, is called through both a direct
// buffer trace AND the real app.Root paste/burst-detection layer (even
// deliberately forcing an active burst immediately before backspacing, the
// exact shape a fast typist could produce), and round-trips correctly in
// every construction tried. This test is the permanent version of that
// trace: the round-trip invariant itself, so it stands guard regardless of
// where a future regression enters -- insert path, delete path, or the
// gap-skip logic in between.
func TestInsertThenBackspaceRoundTripsToTheStartingState(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		// No gap at all -- a true mid-word wrap boundary. Establishes the
		// baseline: this must already pass.
		{"mid-word, no gap", "helloworld"},
		// The failing case reported: a trailing-space wrap gap, End lands
		// gap-adjacent (trailing=false), and repeated inserts widen it.
		{"trailing-space gap", "aaaaa" + strings.Repeat(" ", 2) + "bbbbb"},
		{"wider trailing-space gap", "aaaaa" + strings.Repeat(" ", 8) + "bbbbb"},
	}
	width := 6

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newInputBuffer()
			b.InsertText(c.text)

			b.cursor = 0
			b.trailing = false
			b.MoveEnd(false, width) // the wrap boundary itself -- gap-adjacent where one exists

			startCursor := b.cursor
			startText := b.text()

			const n = 5
			for i := 0; i < n; i++ {
				b.InsertText(" ")
			}
			if want := startCursor + n; b.cursor != want {
				t.Fatalf("after %d inserts: cursor = %d, want %d", n, b.cursor, want)
			}

			for i := 0; i < n; i++ {
				b.Backspace()
			}

			// NOT asserted: b.trailing. splice always sets trailing=false
			// (setCursor(lo+len(repl), false), input.go) -- an edit that
			// lands back on the exact starting INDEX has no reason to also
			// reconstruct whatever affinity a DIFFERENT prior move left
			// there; trailing is a render-affinity bit, not part of the
			// buffer's content identity, and the user's own ask was cursor
			// and text "byte-identical to the start", not affinity.
			if b.cursor != startCursor {
				t.Errorf("round trip FAILED: cursor = %d, want %d (the starting position)", b.cursor, startCursor)
			}
			if b.text() != startText {
				t.Errorf("round trip FAILED: text = %q, want %q (the starting text)", b.text(), startText)
			}
		})
	}
}

// The same round trip, driven through REAL key delivery (app.Root's paste/
// burst detector, not raw buffer calls) with NO pause between the last
// space and the first backspace -- confirmed empirically (frontend/internal
// /app's own burst-detection tests) to activate an active paste burst in
// exactly this shape, which flushes all 5 buffered spaces as individual
// keystrokes from the SAME handleKey call that also delivers the first
// backspace. Reproduces the user's literal repro shape as closely as a
// deterministic Go test can, rather than only the buffer's own arithmetic.
func TestInsertThenBackspaceRoundTripsThroughRealKeyDeliveryOrder(t *testing.T) {
	width := 6
	w := newTestWorkspace(t)
	w.input.textWidth = width
	w.input.buf.InsertText("aaaaa" + strings.Repeat(" ", 2) + "bbbbb")

	w.input.buf.cursor = 0
	w.input.buf.trailing = false
	w.Update(tea.KeyMsg{Type: tea.KeyEnd})
	startCursor := w.input.buf.cursor
	startText := w.input.buf.text()

	// The real delivery order for 5 fast spaces immediately followed by
	// backspaces: the burst flushes as individual KeyRunes events, all
	// arriving before -- or alongside -- the first backspace.
	for i := 0; i < 5; i++ {
		w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	}
	for i := 0; i < 5; i++ {
		w.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}

	if w.input.buf.cursor != startCursor {
		t.Errorf("round trip FAILED: cursor = %d, want %d", w.input.buf.cursor, startCursor)
	}
	if w.input.buf.text() != startText {
		t.Errorf("round trip FAILED: text = %q, want %q", w.input.buf.text(), startText)
	}
}
