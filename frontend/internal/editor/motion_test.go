package editor

import "testing"

func TestVerticalMovementKeepsTheDesiredColumn(t *testing.T) {
	// A long line, a short one, a long one: walking down and back up has
	// to return to the column it started in, which is what desiredCell
	// exists for.
	d := testDoc(t, "0123456789\nab\n0123456789")
	d.moveTo(Pos{0, 8}, false)

	d.moveVertical(+1, false)
	if d.cursor != (Pos{1, 2}) {
		t.Fatalf("on the short line: %+v, want {1 2}", d.cursor)
	}
	d.moveVertical(+1, false)
	if d.cursor != (Pos{2, 8}) {
		t.Errorf("back on a long line: %+v, want {2 8}", d.cursor)
	}
}

// A horizontal move invalidates the wish, so the next vertical move uses
// the caret's real column rather than one from two moves ago.
func TestHorizontalMovementResetsTheDesiredColumn(t *testing.T) {
	d := testDoc(t, "0123456789\nab\n0123456789")
	d.moveTo(Pos{0, 8}, false)
	d.moveVertical(+1, false) // {1,2}
	d.moveLeft(false)         // {1,1}
	d.moveVertical(+1, false)
	if d.cursor != (Pos{2, 1}) {
		t.Errorf("cursor = %+v, want {2 1}", d.cursor)
	}
}

// Vertical movement across a line with wide runes lands on a glyph
// boundary, never inside one.
func TestVerticalMovementLandsOnAGlyphBoundary(t *testing.T) {
	d := testDoc(t, "abcdefgh\n日本語")
	d.moveTo(Pos{0, 3}, false) // display column 3, mid-way through 日本
	d.moveVertical(+1, false)
	cell := displayCol(d.buf.Runes(1), d.cursor.Col)
	if cell > 3 {
		t.Errorf("landed at cell %d, past the requested 3", cell)
	}
	if d.cursor.Col != 1 {
		t.Errorf("cursor col = %d, want 1 (the start of 本)", d.cursor.Col)
	}
}

func TestMoveLeftAndRightCrossLines(t *testing.T) {
	d := testDoc(t, "ab\ncd")
	d.moveTo(Pos{0, 2}, false)
	d.moveRight(false)
	if d.cursor != (Pos{1, 0}) {
		t.Errorf("right over the line end: %+v", d.cursor)
	}
	d.moveLeft(false)
	if d.cursor != (Pos{0, 2}) {
		t.Errorf("left over the line start: %+v", d.cursor)
	}
}

func TestMoveLeftAtTheStartAndRightAtTheEndDoNothing(t *testing.T) {
	d := testDoc(t, "ab")
	d.moveTo(Pos{0, 0}, false)
	d.moveLeft(false)
	if d.cursor != (Pos{0, 0}) {
		t.Errorf("moved off the start: %+v", d.cursor)
	}
	d.moveTo(d.buf.End(), false)
	d.moveRight(false)
	if d.cursor != d.buf.End() {
		t.Errorf("moved off the end: %+v", d.cursor)
	}
}

// Home goes to the first non-blank first, and to column 0 on a second
// press -- in indented code that is where you actually want to go.
func TestHomeTogglesBetweenTheIndentAndColumnZero(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n")
	d.moveTo(Pos{1, 8}, false)
	d.moveHome(false)
	if d.cursor != (Pos{1, 4}) {
		t.Fatalf("first Home: %+v, want {1 4}", d.cursor)
	}
	d.moveHome(false)
	if d.cursor != (Pos{1, 0}) {
		t.Errorf("second Home: %+v, want {1 0}", d.cursor)
	}
}

func TestWordMovementTreatsPunctuationAsItsOwnClass(t *testing.T) {
	d := testDoc(t, "foo.bar(baz)")
	d.moveTo(Pos{0, 0}, false)

	// Each step walks to the end of one run of a single class, so `.`
	// and `(` are stops of their own. Collapsing punctuation into "not a
	// word" would jump the whole expression in one press, which is not
	// what word-wise movement means anywhere.
	want := []int{3, 4, 7, 8, 11, 12}
	for i, w := range want {
		d.moveWord(+1, false)
		if d.cursor.Col != w {
			t.Fatalf("step %d: col = %d, want %d", i+1, d.cursor.Col, w)
		}
	}
}

func TestWordMovementCrossesLines(t *testing.T) {
	d := testDoc(t, "one\ntwo")
	d.moveTo(Pos{0, 3}, false)
	d.moveWord(+1, false)
	if d.cursor != (Pos{1, 0}) {
		t.Errorf("forward over the line end: %+v", d.cursor)
	}
	d.moveWord(-1, false)
	if d.cursor != (Pos{0, 3}) {
		t.Errorf("back over the line start: %+v", d.cursor)
	}
}

// shift+movement extends from where the selection started; plain movement
// drops it. Both for every mover, which is why they all go through
// moveTo/beginMove.
func TestShiftMovementExtendsAndPlainMovementCollapses(t *testing.T) {
	d := testDoc(t, "hello world")
	d.moveTo(Pos{0, 0}, false)

	d.moveRight(true)
	d.moveRight(true)
	d.moveRight(true)
	if !d.hasSelection() {
		t.Fatal("shift+right did not select")
	}
	from, to := d.selectionRange()
	if got := d.buf.Slice(from, to); got != "hel" {
		t.Fatalf("selection = %q, want hel", got)
	}

	d.moveRight(false)
	if d.hasSelection() {
		t.Error("a plain move did not collapse the selection")
	}
}

func TestSelectAllCoversTheWholeBuffer(t *testing.T) {
	d := testDoc(t, "a\nb\nc")
	d.selectAll()
	from, to := d.selectionRange()
	if from != (Pos{0, 0}) || to != d.buf.End() {
		t.Fatalf("range = %+v..%+v", from, to)
	}
	if got := d.buf.Slice(from, to); got != "a\nb\nc" {
		t.Errorf("selected %q", got)
	}
}

func TestDocStartAndEnd(t *testing.T) {
	d := testDoc(t, "a\nb\nc")
	d.moveTo(Pos{1, 1}, false)
	d.moveDocEnd(false)
	if d.cursor != (Pos{2, 1}) {
		t.Errorf("ctrl+end: %+v", d.cursor)
	}
	d.moveDocStart(false)
	if d.cursor != (Pos{0, 0}) {
		t.Errorf("ctrl+home: %+v", d.cursor)
	}
}

func TestEnsureVisibleScrollsVerticallyAndHorizontally(t *testing.T) {
	var sb []byte
	for i := 0; i < 200; i++ {
		sb = append(sb, []byte("0123456789012345678901234567890123456789\n")...)
	}
	d := testDoc(t, string(sb))

	d.moveTo(Pos{150, 0}, false)
	d.ensureVisible(20, 30)
	if d.cursor.Line < d.top || d.cursor.Line >= d.top+20 {
		t.Fatalf("line %d not inside [%d,%d)", d.cursor.Line, d.top, d.top+20)
	}

	d.moveTo(Pos{150, 38}, false)
	d.ensureVisible(20, 30)
	cell := displayCol(d.buf.Runes(150), 38)
	if cell < d.left || cell >= d.left+30 {
		t.Errorf("cell %d not inside [%d,%d)", cell, d.left, d.left+30)
	}
}

func TestScrollByDoesNotMoveTheCaret(t *testing.T) {
	var sb []byte
	for i := 0; i < 100; i++ {
		sb = append(sb, 'x', '\n')
	}
	d := testDoc(t, string(sb))
	d.moveTo(Pos{5, 0}, false)
	before := d.cursor
	// An explicit viewport height, because the bound is now
	// count-rows rather than count-1 (textutil.go's clampTop).
	const rows = 10
	d.scrollBy(40, rows)
	if d.cursor != before {
		t.Errorf("the wheel moved the caret to %+v", d.cursor)
	}
	if d.top != 40 {
		t.Errorf("top = %d, want 40", d.top)
	}
	if visible := d.buf.Lines() - d.top; visible < rows {
		t.Errorf("%d lines visible in a %d-row viewport", visible, rows)
	}
	d.scrollBy(-1000, rows)
	if d.top != 0 {
		t.Errorf("top = %d, want clamped to 0", d.top)
	}
}

// An arrow key with a selection open collapses to that selection's own
// edge -- the two positions the user was just looking at -- rather than
// stepping one rune off the caret.
func TestArrowCollapsesASelectionToItsEdge(t *testing.T) {
	d := testDoc(t, "hello world")
	d.selectAll()
	d.moveLeft(false)
	if d.cursor != (Pos{0, 0}) {
		t.Errorf("left collapsed to %+v, want the start", d.cursor)
	}

	d.selectAll()
	d.moveRight(false)
	if d.cursor != d.buf.End() {
		t.Errorf("right collapsed to %+v, want the end", d.cursor)
	}
}
