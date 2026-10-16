package editor

import (
	"strings"
	"testing"
	"time"
)

// typeString types one rune at a time through the same path a keystroke
// takes, so coalescing sees the same sequence the user produces.
func typeString(d *doc, s string) {
	for _, r := range s {
		d.insertRune(r)
	}
}

// ---------------------------------------------------------------------------
// Done-when #3 — undo groups keystrokes
// ---------------------------------------------------------------------------

// "Type a sentence, one ctrl+z removes a word-group, not one character."
func TestUndoRemovesAWordNotACharacter(t *testing.T) {
	d := testDoc(t, "")
	typeString(d, "hello world")

	if got := d.buf.Text(); got != "hello world" {
		t.Fatalf("typed text = %q", got)
	}
	if !d.Undo() {
		t.Fatal("Undo reported nothing to do")
	}
	if got := d.buf.Text(); got != "hello " {
		t.Fatalf("after one undo = %q, want %q", got, "hello ")
	}
	if !d.Undo() {
		t.Fatal("second Undo reported nothing to do")
	}
	if got := d.buf.Text(); got != "" {
		t.Fatalf("after two undos = %q, want empty", got)
	}
}

// A longer sentence takes one undo per word, not one per character -- the
// property the Done-when is really about. Five words must not take
// twenty-nine undos.
func TestUndoStepsPerWordNotPerRune(t *testing.T) {
	d := testDoc(t, "")
	sentence := "the quick brown fox jumps"
	typeString(d, sentence)

	steps := 0
	for d.Undo() {
		steps++
		if steps > 12 {
			t.Fatalf("more than 12 undo steps for %d runes", len(sentence))
		}
	}
	if got := d.buf.Text(); got != "" {
		t.Fatalf("text after undoing everything = %q", got)
	}
	if steps > 6 {
		t.Errorf("%d undo steps for 5 words; want about one per word", steps)
	}
	if steps < 2 {
		t.Errorf("%d undo steps; the whole sentence collapsed into one", steps)
	}
}

// A pause breaks the group even mid-word, which is the other half of F13's
// "group by pause and by boundary characters".
func TestUndoGroupBreaksOnAPause(t *testing.T) {
	d := testDoc(t, "")
	typeString(d, "abc")
	// Reach past the clock rather than sleeping: the records are the only
	// thing the gap is measured against.
	for i := range d.hist.undo {
		d.hist.undo[i].at = time.Now().Add(-2 * undoCoalesceGap)
	}
	typeString(d, "def")

	d.Undo()
	if got := d.buf.Text(); got != "abc" {
		t.Errorf("after undo = %q, want abc", got)
	}
}

// Moving the caret ends the run: otherwise typing here, clicking there and
// typing again is one undo step spanning two places in the file.
func TestUndoGroupBreaksOnACursorMove(t *testing.T) {
	d := testDoc(t, "xy")
	d.moveTo(Pos{0, 0}, false)
	typeString(d, "A")
	d.moveTo(Pos{0, 3}, false)
	typeString(d, "B")

	if got := d.buf.Text(); got != "AxyB" {
		t.Fatalf("text = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "Axy" {
		t.Errorf("after undo = %q, want Axy", got)
	}
}

// Backspacing collapses the same way, in the other direction.
func TestUndoCoalescesBackspaces(t *testing.T) {
	d := testDoc(t, "hello")
	d.moveTo(d.buf.End(), false)
	for i := 0; i < 3; i++ {
		d.backspace()
	}
	if got := d.buf.Text(); got != "he" {
		t.Fatalf("after backspaces = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "hello" {
		t.Errorf("after undo = %q, want hello", got)
	}
}

func TestRedoReplaysAndNewInputClearsIt(t *testing.T) {
	d := testDoc(t, "")
	typeString(d, "abc")
	d.Undo()
	if got := d.buf.Text(); got != "" {
		t.Fatalf("after undo = %q", got)
	}
	if !d.Redo() {
		t.Fatal("Redo reported nothing to do")
	}
	if got := d.buf.Text(); got != "abc" {
		t.Fatalf("after redo = %q", got)
	}

	d.Undo()
	typeString(d, "z")
	if d.Redo() {
		t.Error("redo survived new input")
	}
}

// A paste is ONE undo step however long it is -- the same mechanism an
// applied diff uses.
func TestPasteIsOneUndoStep(t *testing.T) {
	d := testDoc(t, "")
	d.insertText("one\ntwo\nthree\n", editOther)
	if d.buf.Lines() != 4 {
		t.Fatalf("Lines = %d", d.buf.Lines())
	}
	d.Undo()
	if got := d.buf.Text(); got != "" {
		t.Errorf("after one undo = %q, want empty", got)
	}
}

// F13: "an agent-applied diff is a single undoable operation ... without
// this the diff card is unusable in practice". Nothing calls ApplyRegion
// this session; the property is tested now because the undo model either
// has it by construction or has to be rebuilt to get it.
func TestApplyRegionIsOneUndoStep(t *testing.T) {
	d := testDoc(t, "one\ntwo\nthree\nfour")
	d.ApplyRegion(Pos{1, 0}, Pos{2, 5}, "REPLACED\nWITH\nTHREE\nLINES")

	if got := d.buf.Text(); got != "one\nREPLACED\nWITH\nTHREE\nLINES\nfour" {
		t.Fatalf("after apply = %q", got)
	}
	if !d.Undo() {
		t.Fatal("Undo reported nothing to do")
	}
	if got := d.buf.Text(); got != "one\ntwo\nthree\nfour" {
		t.Errorf("after one undo = %q, want the original", got)
	}
	if d.Undo() {
		t.Error("the apply left more than one undo step")
	}
}

func TestApplyRegionRefusedOnAReadOnlyDocument(t *testing.T) {
	d := newDoc(wireFile{
		Path: "C:\\p\\x.py", Content: "a", LineEnding: lineEndingLF, Writable: false,
	}, "C:\\p", true)
	d.ApplyRegion(Pos{0, 0}, Pos{0, 1}, "z")
	if got := d.buf.Text(); got != "a" {
		t.Errorf("text = %q, want unchanged", got)
	}
}

// ---------------------------------------------------------------------------
// Done-when #5 — tab / shift+tab on a multi-line selection
// ---------------------------------------------------------------------------

func TestIndentAndDedentAMultiLineSelection(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n    b = 2\n    c = 3\n")
	// Select from the middle of line 1 into line 2.
	d.anchor = Pos{1, 2}
	d.cursor = Pos{2, 3}
	d.selecting = true

	d.indentSelection()
	want := "def f():\n        a = 1\n        b = 2\n    c = 3"
	if got := d.buf.Text(); got != want {
		t.Fatalf("after indent:\n%q\nwant:\n%q", got, want)
	}
	if !d.hasSelection() {
		t.Error("the selection was dropped by the indent")
	}

	d.dedentSelection()
	want = "def f():\n    a = 1\n    b = 2\n    c = 3"
	if got := d.buf.Text(); got != want {
		t.Fatalf("after dedent:\n%q\nwant:\n%q", got, want)
	}

	// And the whole block indent is one undo step, not one per line.
	d.Undo()
	if got := d.buf.Text(); got != "def f():\n        a = 1\n        b = 2\n    c = 3" {
		t.Errorf("one undo did not take back the whole dedent: %q", d.buf.Text())
	}
}

// A selection ending at column 0 of a line has not reached into that line,
// and indenting it would shift a line the user highlighted no character of.
func TestIndentIgnoresALineTheSelectionOnlyTouchesAtColumnZero(t *testing.T) {
	d := testDoc(t, "a\nb\nc\n")
	d.anchor = Pos{0, 0}
	d.cursor = Pos{1, 0}
	d.selecting = true
	d.indentSelection()
	if got := d.buf.Text(); got != "    a\nb\nc" {
		t.Errorf("text = %q, want only line 0 indented", got)
	}
}

// Blank lines are left alone so a block indent does not manufacture
// trailing whitespace.
func TestIndentLeavesBlankLinesAlone(t *testing.T) {
	d := testDoc(t, "a\n\nb\n")
	d.selectAll()
	d.indentSelection()
	if got := d.buf.Line(1); got != "" {
		t.Errorf("blank line became %q", got)
	}
}

// A dedent of a line indented by six with a width of four lands on four,
// not on two: it aligns the line rather than pushing it one further out.
func TestDedentAlignsToTheIndentGrid(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n    b = 2\n")
	if d.indent.width != 4 {
		t.Fatalf("precondition: width = %d", d.indent.width)
	}
	d.buf.Splice(Pos{1, 0}, Pos{1, 4}, "      ") // six spaces
	d.anchor, d.cursor, d.selecting = Pos{1, 7}, Pos{1, 8}, true
	d.dedentSelection()
	if got := d.buf.Line(1); got != "    a = 1" {
		t.Errorf("line = %q, want four spaces", got)
	}
}

// Tab with no selection inserts one indent unit -- what Tab means while
// typing -- and respects a tab-indented file.
func TestTabWithNoSelectionInsertsOneUnit(t *testing.T) {
	spaces := testDoc(t, "def f():\n    a = 1\n    b = 2\n")
	spaces.moveTo(Pos{0, 0}, false)
	spaces.indentSelection()
	if got := spaces.buf.Line(0); got != "    def f():" {
		t.Errorf("spaces: %q", got)
	}

	tabs := testDoc(t, "func a() {\n\tx := 1\n\tif x {\n\t\ty := 2\n")
	if !tabs.indent.tabs {
		t.Fatal("precondition: the tab file was not detected as tabs")
	}
	tabs.moveTo(Pos{0, 0}, false)
	tabs.indentSelection()
	if got := tabs.buf.Line(0); got != "\tfunc a() {" {
		t.Errorf("tabs: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Done-when #6 — Enter after an indented line preserves the indentation
// ---------------------------------------------------------------------------

func TestEnterCopiesThePreviousIndentation(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n")
	d.moveTo(Pos{1, 9}, false) // end of "    a = 1"
	d.newline()

	if got := d.buf.Line(2); got != "    " {
		t.Fatalf("new line = %q, want four spaces", got)
	}
	if d.cursor != (Pos{2, 4}) {
		t.Errorf("cursor = %+v, want {2 4}", d.cursor)
	}
}

func TestEnterCopiesTabIndentation(t *testing.T) {
	d := testDoc(t, "func a() {\n\tx := 1\n\tif x {\n\t\ty := 2\n")
	d.moveTo(Pos{1, 7}, false)
	d.newline()
	if got := d.buf.Line(2); got != "\t" {
		t.Errorf("new line = %q, want one tab", got)
	}
}

// Only the indentation BEFORE the caret is copied: pressing Enter in the
// middle of a line's leading whitespace must not manufacture more of it
// than the caret has passed.
func TestEnterInsideTheIndentCopiesOnlyWhatIsBehindTheCaret(t *testing.T) {
	d := testDoc(t, "def f():\n        a = 1\n")
	d.moveTo(Pos{1, 4}, false) // halfway through eight spaces
	d.newline()
	if got := d.buf.Line(2); got != "        a = 1" {
		t.Errorf("line 2 = %q", got)
	}
	if got := d.buf.Line(1); got != "    " {
		t.Errorf("line 1 = %q, want the four spaces behind the caret", got)
	}
}

// Enter is editOther, so it never merges with the typing either side of it
// -- one undo takes back the line break and nothing more.
func TestEnterIsItsOwnUndoStep(t *testing.T) {
	d := testDoc(t, "")
	typeString(d, "abc")
	d.newline()
	typeString(d, "def")
	d.Undo()
	if got := d.buf.Text(); got != "abc\n" {
		t.Fatalf("after one undo = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "abc" {
		t.Errorf("after two undos = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Backspace deletes a full indent level
// ---------------------------------------------------------------------------

func TestBackspaceDeletesAWholeIndentLevel(t *testing.T) {
	d := testDoc(t, "def f():\n        a = 1\n    b = 2\n")
	d.moveTo(Pos{1, 8}, false) // just before "a", eight spaces behind
	d.backspace()
	if got := d.buf.Line(1); got != "    a = 1" {
		t.Errorf("line = %q, want one level removed", got)
	}
}

// It fires only in leading whitespace: inside code, backspace still
// deletes one character.
func TestBackspaceInsideCodeDeletesOneRune(t *testing.T) {
	d := testDoc(t, "def f():\n    abc = 1\n    d = 2\n")
	d.moveTo(Pos{1, 7}, false) // after "abc"
	d.backspace()
	if got := d.buf.Line(1); got != "    ab = 1" {
		t.Errorf("line = %q", got)
	}
}

// A misaligned line gets ALIGNED rather than pushed one further out.
func TestBackspaceAlignsAMisalignedIndent(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n    b = 2\n")
	d.buf.Splice(Pos{1, 0}, Pos{1, 4}, strings.Repeat(" ", 6))
	d.moveTo(Pos{1, 6}, false)
	d.backspace()
	if got := d.buf.Line(1); got != "    a = 1" {
		t.Errorf("line = %q, want alignment to four", got)
	}
}

func TestBackspaceAtColumnZeroJoinsLines(t *testing.T) {
	d := testDoc(t, "ab\ncd")
	d.moveTo(Pos{1, 0}, false)
	d.backspace()
	if got := d.buf.Text(); got != "abcd" {
		t.Errorf("text = %q", got)
	}
	if d.cursor != (Pos{0, 2}) {
		t.Errorf("cursor = %+v", d.cursor)
	}
}

// ---------------------------------------------------------------------------
// Bracket auto-pairing
// ---------------------------------------------------------------------------

func TestBracketPairingInsertsAndTypesThrough(t *testing.T) {
	d := testDoc(t, "")
	d.insertRune('(')
	if got := d.buf.Text(); got != "()" {
		t.Fatalf("after ( = %q", got)
	}
	if d.cursor != (Pos{0, 1}) {
		t.Fatalf("cursor = %+v, want between the pair", d.cursor)
	}
	d.insertRune(')')
	if got := d.buf.Text(); got != "()" {
		t.Errorf("after ) = %q, want the closer typed through", got)
	}
	if d.cursor != (Pos{0, 2}) {
		t.Errorf("cursor = %+v, want past the closer", d.cursor)
	}
}

// Typing an opener before an existing identifier almost never means
// wrapping it, and a stray closer there is worse than no help.
func TestBracketPairingSkippedBeforeAWordRune(t *testing.T) {
	d := testDoc(t, "name")
	d.moveTo(Pos{0, 0}, false)
	d.insertRune('(')
	if got := d.buf.Text(); got != "(name" {
		t.Errorf("text = %q, want no closer", got)
	}
}

func TestBracketPairingWrapsASelection(t *testing.T) {
	d := testDoc(t, "value")
	d.selectAll()
	d.insertRune('(')
	if got := d.buf.Text(); got != "(value)" {
		t.Fatalf("text = %q", got)
	}
	if !d.hasSelection() {
		t.Fatal("the wrapped text is no longer selected")
	}
	from, to := d.selectionRange()
	if got := d.buf.Slice(from, to); got != "value" {
		t.Errorf("selection = %q, want value", got)
	}
	// One undo takes the whole wrap back.
	d.Undo()
	if got := d.buf.Text(); got != "value" {
		t.Errorf("after undo = %q", got)
	}
}

// A move between the opener and the closer cancels the type-through, so a
// closer typed later is a real character rather than a skip over one that
// is no longer in front of the caret.
func TestTypeThroughIsCancelledByAMove(t *testing.T) {
	d := testDoc(t, "")
	d.insertRune('(')
	d.moveTo(Pos{0, 0}, false)
	d.moveTo(Pos{0, 1}, false)
	d.insertRune(')')
	if got := d.buf.Text(); got != "())" {
		t.Errorf("text = %q, want a real closer inserted", got)
	}
}

// ---------------------------------------------------------------------------
// Selection editing
// ---------------------------------------------------------------------------

func TestTypingReplacesASelectionInOneStep(t *testing.T) {
	d := testDoc(t, "one two three")
	d.anchor, d.cursor, d.selecting = Pos{0, 4}, Pos{0, 7}, true
	d.insertRune('X')
	if got := d.buf.Text(); got != "one X three" {
		t.Fatalf("text = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "one two three" {
		t.Errorf("after undo = %q", got)
	}
}

func TestDeleteSelectionAcrossLines(t *testing.T) {
	d := testDoc(t, "one\ntwo\nthree")
	d.anchor, d.cursor, d.selecting = Pos{0, 1}, Pos{2, 2}, true
	d.deleteForward()
	if got := d.buf.Text(); got != "oree" {
		t.Errorf("text = %q", got)
	}
}

func TestSelectAllThenTypeReplacesEverything(t *testing.T) {
	d := testDoc(t, "a\nb\nc")
	d.selectAll()
	d.insertText("z", editInsert)
	if got := d.buf.Text(); got != "z" {
		t.Errorf("text = %q", got)
	}
}

// Reported as: "in tabs.go, pressing tab inserts 4 spaces, not a tab
// character. Confirmed, not ambiguous."
//
// It does not. The character inserted is a real tab, at every caret
// position, through the key path -- asserted here against the gate
// fixture's EXACT content (LF-normalised, as B32 puts it on the wire).
//
// What varies is how WIDE a tab DRAWS: tabStop is 8, so a tab advances to
// the next multiple of 8 and occupies four cells at display column 4, 12,
// 20 and so on. A tab typed into the middle of a line therefore very
// often looks like four spaces. That is correct, and it is checked below
// so the distinction is pinned rather than argued.
func TestTabInTheGateFixtureInsertsARealTabAtEveryPosition(t *testing.T) {
	const content = "package x\n\nfunc a() {\n\tif true {\n\t\tprintln(1)\n\t}\n}\n"

	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:/p/tabs.go", Content: content,
		LineEnding: lineEndingCRLF, TrailingNewline: true, Writable: true,
	}, "C:/p", true))
	ws.View(layoutFor(ws, 120, 30))
	d := ws.docs[0]

	if !d.indent.tabs {
		t.Fatalf("indent = %+v, want tabs — the status bar would read 4sp", d.indent)
	}
	if d.indent.unit() != "\t" {
		t.Fatalf("unit = %q, want a tab", d.indent.unit())
	}

	for _, at := range []Pos{{0, 0}, {2, 0}, {3, 0}, {3, 1}, {4, 2}, {4, 6}} {
		fresh := newDoc(wireFile{
			Path: "C:/p/tabs.go", Content: content,
			LineEnding: lineEndingCRLF, TrailingNewline: true, Writable: true,
		}, "C:/p", true)
		before := fresh.buf.Line(at.Line)
		fresh.moveTo(at, false)
		fresh.indentSelection()
		got := fresh.buf.Line(at.Line)

		if strings.Count(got, "\t") != strings.Count(before, "\t")+1 {
			t.Errorf("tab at %+v: %q -> %q, tab count did not go up by exactly one",
				at, before, got)
		}
		if strings.Count(got, " ") != strings.Count(before, " ") {
			t.Errorf("tab at %+v: %q -> %q, spaces were inserted", at, before, got)
		}
		// And one ctrl+z takes the whole thing back, which is the other
		// half of "a tab, not four spaces".
		fresh.Undo()
		if fresh.buf.Line(at.Line) != before {
			t.Errorf("tab at %+v: one undo left %q, want %q",
				at, fresh.buf.Line(at.Line), before)
		}
	}

	// The drawn width: 8 cells at column 0, 4 cells at column 4.
	if got := runeCells('\t', 0); got != 8 {
		t.Errorf("a tab at column 0 draws %d cells, want 8", got)
	}
	if got := runeCells('\t', 4); got != 4 {
		t.Errorf("a tab at column 4 draws %d cells, want 4 — this is what reads as spaces", got)
	}
}
