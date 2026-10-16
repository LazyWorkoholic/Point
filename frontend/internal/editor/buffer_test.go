package editor

import (
	"strings"
	"testing"
)

// The buffer's own contract: Splice is the only mutation, and everything
// else in this package is expressed in terms of it, so these are the tests
// every other file's correctness rests on.

func TestNewLineBufferStripsExactlyOneTrailingNewline(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		lines []string
	}{
		{"trailing newline", "a\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
		{"blank final line is kept", "a\nb\n\n", []string{"a", "b", ""}},
		{"empty file is one empty line", "", []string{""}},
		{"only a newline", "\n", []string{""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newLineBuffer(c.in)
			if b.Lines() != len(c.lines) {
				t.Fatalf("Lines() = %d, want %d (%q)", b.Lines(), len(c.lines), c.in)
			}
			for i, want := range c.lines {
				if got := b.Line(i); got != want {
					t.Errorf("line %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// Text() must return exactly what was loaded, minus the one trailing
// newline newLineBuffer strips -- that newline lives in
// doc.trailingNewline, and 18b's save re-applies it. If this ever stops
// being true, a save would silently gain or lose bytes.
func TestTextRoundTripsContentWithoutTheTrailingNewline(t *testing.T) {
	for _, in := range []string{"a\nb\n", "a\nb", "", "x", "a\n\nb\n"} {
		b := newLineBuffer(in)
		want := strings.TrimSuffix(in, "\n")
		if got := b.Text(); got != want {
			t.Errorf("Text() for %q = %q, want %q", in, got, want)
		}
	}
}

func TestSpliceSingleLine(t *testing.T) {
	b := newLineBuffer("hello world")
	end := b.Splice(Pos{0, 6}, Pos{0, 11}, "there")
	if got := b.Text(); got != "hello there" {
		t.Fatalf("Text() = %q", got)
	}
	if end != (Pos{0, 11}) {
		t.Errorf("end = %+v, want {0 11}", end)
	}
}

func TestSpliceInsertsNewlines(t *testing.T) {
	b := newLineBuffer("ab")
	end := b.Splice(Pos{0, 1}, Pos{0, 1}, "\nX\n")
	if got := b.Text(); got != "a\nX\nb" {
		t.Fatalf("Text() = %q", got)
	}
	if end != (Pos{2, 0}) {
		t.Errorf("end = %+v, want {2 0}", end)
	}
}

func TestSpliceJoinsLines(t *testing.T) {
	b := newLineBuffer("one\ntwo\nthree")
	b.Splice(Pos{0, 3}, Pos{2, 0}, "")
	if got := b.Text(); got != "onethree" {
		t.Fatalf("Text() = %q", got)
	}
	if b.Lines() != 1 {
		t.Errorf("Lines() = %d, want 1", b.Lines())
	}
}

// The aliasing bug Splice's own comment warns about: appending to a
// sub-slice of a stored line can write through into that line whenever
// capacity allows. It only reproduces at certain lengths, so this exercises
// a range of them rather than one.
func TestSpliceDoesNotCorruptNeighbouringLines(t *testing.T) {
	for n := 1; n < 40; n++ {
		body := strings.Repeat("x", n)
		b := newLineBuffer(body + "\nSECOND\nTHIRD")
		b.Splice(Pos{0, n}, Pos{0, n}, "APPENDED")
		if got := b.Line(1); got != "SECOND" {
			t.Fatalf("n=%d: line 1 = %q, want SECOND", n, got)
		}
		if got := b.Line(2); got != "THIRD" {
			t.Fatalf("n=%d: line 2 = %q, want THIRD", n, got)
		}
		if got := b.Line(0); got != body+"APPENDED" {
			t.Fatalf("n=%d: line 0 = %q", n, got)
		}
	}
}

func TestSpliceAcceptsReversedAndOutOfRangePositions(t *testing.T) {
	b := newLineBuffer("abc")
	b.Splice(Pos{0, 3}, Pos{0, 1}, "-") // reversed
	if got := b.Text(); got != "a-" {
		t.Fatalf("reversed: %q", got)
	}

	b = newLineBuffer("abc")
	b.Splice(Pos{9, 9}, Pos{-1, -1}, "Z") // both out of range
	if got := b.Text(); got != "Z" {
		t.Fatalf("clamped: %q", got)
	}
}

func TestSliceSpansLines(t *testing.T) {
	b := newLineBuffer("one\ntwo\nthree")
	if got := b.Slice(Pos{0, 1}, Pos{2, 2}); got != "ne\ntwo\nth" {
		t.Errorf("Slice = %q", got)
	}
	if got := b.Slice(Pos{1, 1}, Pos{1, 1}); got != "" {
		t.Errorf("empty Slice = %q", got)
	}
	// Reversed is the same span.
	if got := b.Slice(Pos{2, 2}, Pos{0, 1}); got != "ne\ntwo\nth" {
		t.Errorf("reversed Slice = %q", got)
	}
}

func TestVersionAdvancesOnEverySplice(t *testing.T) {
	b := newLineBuffer("a")
	v0 := b.Version()
	b.Splice(Pos{0, 1}, Pos{0, 1}, "b")
	if b.Version() == v0 {
		t.Fatal("Version did not advance")
	}
}

func TestAdvanceMatchesSplicesOwnAnswer(t *testing.T) {
	// advance() is what undo uses to reconstruct an inserted span without
	// asking the buffer. If the two ever disagree, undo splices the wrong
	// range -- so they are checked against each other directly.
	for _, repl := range []string{"", "x", "ab\ncd", "\n", "a\nb\nc", "日本語", "a\n"} {
		b := newLineBuffer("start\nend")
		from := Pos{0, 2}
		got := b.Splice(from, from, repl)
		if want := advance(from, repl); got != want {
			t.Errorf("repl %q: Splice end %+v, advance %+v", repl, got, want)
		}
	}
}

func TestDisplayColAndInverseAgree(t *testing.T) {
	// A tab, a CJK run and an emoji on one line -- the exact shape
	// Done-when #4 names.
	runes := []rune("\tif 日本語 🎉 x")
	for col := 0; col <= len(runes); col++ {
		cell := displayCol(runes, col)
		back := colAtDisplay(runes, cell)
		if back != col {
			t.Errorf("col %d -> cell %d -> col %d", col, cell, back)
		}
	}
	// A tab at the start of a line occupies a full tab stop.
	if got := displayCol(runes, 1); got != tabStop {
		t.Errorf("tab width = %d, want %d", got, tabStop)
	}
	// Landing mid-glyph resolves to the start of that glyph, never past it.
	mid := displayCol(runes, 4) + 1 // one cell into 日
	if got := colAtDisplay(runes, mid); got != 4 {
		t.Errorf("mid-glyph cell %d -> col %d, want 4", mid, got)
	}
}
