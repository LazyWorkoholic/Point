package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Regressions from 18a's first verification round. Each test names the
// symptom it was reported as, because the symptom and the cause were
// different things in every one of these.

// caretRun finds the caret's rendered run on its own line and reports the
// escape prefix it was drawn with, plus the display cell it starts at.
// -1 means the caret was not drawn at all.
func caretRun(ws *Workspace, d *doc) (prefix string, cell int) {
	gutter := gutterWidth(d.buf.Lines())
	rows := ws.textArea.H
	if rows <= 0 {
		rows = 1
	}
	out := ws.renderCode(d, gutter, ws.textArea.W, rows)

	i := d.cursor.Line - d.top
	if i < 0 || i >= len(out) {
		return "", -1
	}
	// The caret is reverse video over whatever role the cell had, so its
	// run is the one carrying the reverse attribute (SGR 7).
	row := out[i]
	for pos := 0; pos < len(row); {
		next := strings.Index(row[pos:], "\x1b")
		if next < 0 {
			break
		}
		pos += next
		end := strings.IndexByte(row[pos:], 'm')
		if end < 0 {
			break
		}
		seq := row[pos : pos+end+1]
		// lipgloss emits the reverse attribute FIRST: "[7;38;2;R;G;Bm".
		if strings.HasPrefix(seq, "[7;") || seq == "[7m" {
			return seq, visibleWidth(row[:pos]) - gutter
		}
		pos += end + 1
	}
	return "", -1
}

// ---------------------------------------------------------------------------
// Reported as: "the cursor disappears, and reappears after N right-arrows,
// and N keeps growing" / "it toggles on alternating left-arrow presses"
// ---------------------------------------------------------------------------

// The caret is drawn at EVERY column of a line, on glyphs and on spaces
// alike, and always at the cell its column maps to.
//
// It always was -- the caret was emitted at the right cell at every
// position, measured. What was wrong was its COLOUR: it was
// Fill(SelectText, AccentPrimary), and THEME.md rule 3 makes
// accent.primary the colour of keywords too, so the caret's background
// was the same #2EC4C6 as the foreground of the keyword it sat on. On a
// space it read as a solid block and on a keyword glyph it vanished,
// which is what "toggles as it moves" was.
func TestCaretIsDrawnAtEveryColumn(t *testing.T) {
	ws := testWorkspace(t, "def f0(a, b):\n    return x", 120, 30)
	d := ws.docs[0]

	for _, line := range []int{0, 1} {
		n := len(d.buf.Runes(line))
		for col := 0; col <= n; col++ {
			d.moveTo(Pos{line, col}, false)
			ws.View(ws.lastLayout)
			_, cell := caretRun(ws, d)
			want := displayCol(d.buf.Runes(line), col)
			if cell != want {
				t.Errorf("line %d col %d: caret at cell %d, want %d (%q)",
					line, col, cell, want, d.buf.Line(line))
			}
		}
	}
}

// The caret's own style must DIFFER from the style of the run beside it,
// whatever the lexer coloured that run. This is the assertion the old
// caret failed: it is what makes the caret visible rather than merely
// emitted.
func TestCaretStyleDiffersFromEveryRoleItSitsOn(t *testing.T) {
	th := testTheme()
	roles := []theme.Role{
		theme.AccentPrimary, theme.AccentSecondary, theme.CodeText,
		theme.TextDim, theme.Done, theme.InlineCode, theme.Error,
	}
	ws := New(Options{Theme: th})
	for _, role := range roles {
		plain := th.Style(role)
		caret := ws.caretStyle(role)
		if sameStyle(plain, caret) {
			t.Errorf("role %q: the caret renders identically to the text under it", role)
		}
	}
}

// Reverse video also survives NO_COLOR, where every foreground is dropped
// and a colour-based caret would disappear completely. theme.Style honours
// NO_COLOR by returning a bare style; Reverse still applies.
func TestCaretSurvivesNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ws := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	plain := ws.theme.Style(theme.AccentPrimary)
	caret := ws.caretStyle(theme.AccentPrimary)
	if sameStyle(plain, caret) {
		t.Error("with NO_COLOR set the caret is indistinguishable from the text")
	}
}

// The caret at the very end of the buffer, and one column back from it --
// the exact two positions the "toggles on alternating presses" report
// named (ctrl+end on a line ending "return x", then left).
func TestCaretAtAndBeforeTheEndOfTheBuffer(t *testing.T) {
	ws := testWorkspace(t, "a\n    return x", 120, 30)
	d := ws.docs[0]

	d.moveDocEnd(false)
	ws.View(ws.lastLayout)
	if _, cell := caretRun(ws, d); cell != 12 {
		t.Errorf("at the end: caret at cell %d, want 12", cell)
	}
	for want := 11; want >= 8; want-- {
		d.moveLeft(false)
		ws.View(ws.lastLayout)
		if _, cell := caretRun(ws, d); cell != want {
			t.Errorf("after a left arrow: caret at cell %d, want %d", cell, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Reported as: "shift+up produces a 4-space highlighted block as if
// something got selected, with nothing actually selected"
// ---------------------------------------------------------------------------

// shift+arrow selecting is correct -- it is the select gesture. What made
// it read as an artifact is that the caret was SUPPRESSED while a
// selection existed, so a short selection appeared on screen with no
// caret anywhere. The caret is now drawn at the selection's active end.
func TestCaretIsDrawnAtTheActiveEndOfASelection(t *testing.T) {
	ws := testWorkspace(t, "def f5(a, b):\n    x = 1\n    y = 2", 120, 30)
	d := ws.docs[0]

	d.moveTo(Pos{2, 4}, false)
	press(ws, "shift+up")
	ws.View(ws.lastLayout)

	if !d.hasSelection() {
		t.Fatal("shift+up did not select")
	}
	if d.cursor != (Pos{1, 4}) {
		t.Fatalf("cursor = %+v, want {1 4}", d.cursor)
	}
	_, cell := caretRun(ws, d)
	if cell != 4 {
		t.Errorf("caret at cell %d on the selection's active line, want 4", cell)
	}

	// And the selection is still exactly what shift+up means: the tail of
	// the line above plus the indent of the line the caret came from.
	from, to := d.selectionRange()
	if got := d.buf.Slice(from, to); got != "x = 1\n    " {
		t.Errorf("selection = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Reported as: "mouse wheel cannot scroll past a fixed point; it visually
// attempts to scroll then snaps back. Same in the File Tree."
// ---------------------------------------------------------------------------

// ensureVisible ran on every frame, so any scroll the caret did not ask
// for was undone on the same frame. Measured before the fix: `top after
// wheel=3, top after View=0`, every time.
func TestWheelScrollSurvivesTheFrame(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line of code\n")
	}
	ws := testWorkspace(t, sb.String(), 120, 30)
	d := ws.docs[0]

	last := 0
	for n := 1; n <= 10; n++ {
		ws.handleMouse(mouse(ws.textArea.X, ws.textArea.Y,
			tea.MouseButtonWheelDown, tea.MouseActionPress))
		ws.View(ws.lastLayout)
		if d.top <= last {
			t.Fatalf("wheel %d: top = %d, did not advance past %d", n, d.top, last)
		}
		last = d.top
	}
	if d.top < 30 {
		t.Errorf("after ten wheel notches top = %d; the view barely moved", d.top)
	}

	// Scrolling up again works too, and stops at the top.
	for n := 0; n < 40; n++ {
		ws.handleMouse(mouse(ws.textArea.X, ws.textArea.Y,
			tea.MouseButtonWheelUp, tea.MouseActionPress))
		ws.View(ws.lastLayout)
	}
	if d.top != 0 {
		t.Errorf("top = %d after scrolling back up, want 0", d.top)
	}
}

// A caret move still pulls the view back to the caret -- the reveal gate
// must not have turned scrolling into something the caret cannot do.
func TestACaretMoveStillRevealsAfterAWheelScroll(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line of code\n")
	}
	ws := testWorkspace(t, sb.String(), 120, 30)
	d := ws.docs[0]

	for n := 0; n < 20; n++ {
		ws.handleMouse(mouse(ws.textArea.X, ws.textArea.Y,
			tea.MouseButtonWheelDown, tea.MouseActionPress))
	}
	ws.View(ws.lastLayout)
	if d.top == 0 {
		t.Fatal("precondition: the wheel did not scroll")
	}

	press(ws, "down")
	ws.View(ws.lastLayout)
	if d.cursor.Line < d.top || d.cursor.Line >= d.top+ws.textArea.H {
		t.Errorf("caret line %d is outside the window [%d,%d) after a move",
			d.cursor.Line, d.top, d.top+ws.textArea.H)
	}
}

// An edit reveals too: typing somewhere you cannot see has to bring you
// back to it.
func TestAnEditRevealsTheCaret(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line of code\n")
	}
	ws := testWorkspace(t, sb.String(), 120, 30)
	d := ws.docs[0]
	for n := 0; n < 20; n++ {
		ws.handleMouse(mouse(ws.textArea.X, ws.textArea.Y,
			tea.MouseButtonWheelDown, tea.MouseActionPress))
	}
	ws.View(ws.lastLayout)

	press(ws, "z")
	ws.View(ws.lastLayout)
	if d.cursor.Line < d.top || d.cursor.Line >= d.top+ws.textArea.H {
		t.Errorf("caret line %d outside [%d,%d) after an edit",
			d.cursor.Line, d.top, d.top+ws.textArea.H)
	}
}

func TestTreeWheelScrollSurvivesTheFrame(t *testing.T) {
	paths := make([]string, 80)
	for i := range paths {
		paths[i] = "f" + itoa(i) + ".py"
	}
	ws := testWorkspace(t, "x", 120, 30)
	ws.tree.build(paths)
	ws.View(ws.lastLayout)

	last := 0
	for n := 1; n <= 5; n++ {
		ws.handleMouse(mouse(ws.treeRect.X+2, ws.treeRect.Y+2,
			tea.MouseButtonWheelDown, tea.MouseActionPress))
		ws.View(ws.lastLayout)
		if ws.tree.top <= last {
			t.Fatalf("tree wheel %d: top = %d, did not advance past %d", n, ws.tree.top, last)
		}
		last = ws.tree.top
	}
}

// ---------------------------------------------------------------------------
// Reported as: "shift+tab on one line did not dedent correctly; behaviour
// was inconsistent"
// ---------------------------------------------------------------------------

// Characterised: shift+tab with no selection dedents the current line, and
// is a clean NO-OP on a line with no indentation. In big.py one line in
// five has no indent, so the same keystroke both dedents and does nothing
// depending on where the caret is -- which is what read as inconsistent.
// Both halves are asserted here so the behaviour is pinned rather than
// described.
func TestShiftTabWithNoSelectionIsDedentOrACleanNoOp(t *testing.T) {
	body := "def f(a, b):\n    x = 1\n    if x:\n        return 1\n    return x\n"
	cases := []struct {
		at         Pos
		wantLine   string
		wantCursor Pos
	}{
		{Pos{0, 0}, "def f(a, b):", Pos{0, 0}}, // no indent: no-op
		{Pos{0, 5}, "def f(a, b):", Pos{0, 5}}, // no indent, mid-line: no-op
		{Pos{1, 0}, "x = 1", Pos{1, 0}},        // dedent from the line start
		{Pos{1, 4}, "x = 1", Pos{1, 0}},        // dedent from the first non-blank
		{Pos{1, 9}, "x = 1", Pos{1, 5}},        // dedent from inside the code
		{Pos{3, 8}, "    return 1", Pos{3, 4}}, // two levels: one comes off
	}
	for _, c := range cases {
		d := testDoc(t, body)
		d.moveTo(c.at, false)
		d.dedentSelection()
		if got := d.buf.Line(c.at.Line); got != c.wantLine {
			t.Errorf("at %+v: line = %q, want %q", c.at, got, c.wantLine)
		}
		if d.cursor != c.wantCursor {
			t.Errorf("at %+v: cursor = %+v, want %+v", c.at, d.cursor, c.wantCursor)
		}
		if d.hasSelection() {
			t.Errorf("at %+v: a selection was left behind", c.at)
		}
	}
}

// A no-op leaves NO undo step: pressing shift+tab on an unindented line
// and then ctrl+z must not take back the edit before it.
func TestShiftTabNoOpLeavesNoUndoStep(t *testing.T) {
	d := testDoc(t, "def f():\n    x = 1")
	typeString(d, "Z")
	before := len(d.hist.undo)
	d.moveTo(Pos{0, 0}, false)
	d.dedentSelection()
	if got := len(d.hist.undo); got != before {
		t.Errorf("%d undo records, want %d — the no-op pushed one", got, before)
	}
}

// ---------------------------------------------------------------------------
// Undo/redo must restore the caret the EDIT left, not the bare
// post-splice one. shiftLines, shiftCurrentLine and maybePair all adjusted
// the caret after spliceAs had already recorded it.
// ---------------------------------------------------------------------------

func TestUndoRedoRestoreTheSelectionABlockIndentLeft(t *testing.T) {
	d := testDoc(t, "def f():\n    a = 1\n    b = 2\n    c = 3")
	d.anchor, d.cursor, d.selecting = Pos{1, 2}, Pos{2, 3}, true
	d.indentSelection()

	curAfter, anchorAfter, selAfter := d.cursor, d.anchor, d.selecting
	if !selAfter {
		t.Fatal("the indent did not leave a selection")
	}

	// The pre-indent state: the caret was at {2,3} and the anchor at
	// {1,2}, which is what the record captured and what undo restores.
	d.Undo()
	if d.cursor != (Pos{2, 3}) || d.anchor != (Pos{1, 2}) || !d.selecting {
		t.Errorf("after undo: cursor=%+v anchor=%+v sel=%v, want {2 3}/{1 2}/true",
			d.cursor, d.anchor, d.selecting)
	}

	d.Redo()
	if d.cursor != curAfter || d.anchor != anchorAfter || d.selecting != selAfter {
		t.Errorf("after redo: cursor=%+v anchor=%+v sel=%v, want %+v/%+v/%v",
			d.cursor, d.anchor, d.selecting, curAfter, anchorAfter, selAfter)
	}
}

func TestUndoRedoRestoreTheCaretADedentLeft(t *testing.T) {
	d := testDoc(t, "def f():\n        a = 1\n    b = 2")
	d.moveTo(Pos{1, 8}, false)
	d.dedentSelection()
	after := d.cursor

	d.Undo()
	if d.cursor != (Pos{1, 8}) {
		t.Errorf("after undo: cursor = %+v, want {1 8}", d.cursor)
	}
	d.Redo()
	if d.cursor != after {
		t.Errorf("after redo: cursor = %+v, want %+v", d.cursor, after)
	}
}

func TestUndoRedoRestoreTheSelectionABracketWrapLeft(t *testing.T) {
	d := testDoc(t, "value")
	d.selectAll()
	d.insertRune('(')

	curAfter, anchorAfter := d.cursor, d.anchor
	if !d.selecting {
		t.Fatal("the wrap did not leave the wrapped text selected")
	}

	d.Undo()
	if got := d.buf.Text(); got != "value" {
		t.Fatalf("after undo = %q", got)
	}
	d.Redo()
	if got := d.buf.Text(); got != "(value)" {
		t.Fatalf("after redo = %q", got)
	}
	if d.cursor != curAfter || d.anchor != anchorAfter || !d.selecting {
		t.Errorf("after redo: cursor=%+v anchor=%+v sel=%v, want %+v/%+v/true",
			d.cursor, d.anchor, d.selecting, curAfter, anchorAfter)
	}
}

func TestUndoRedoRestoreTheCaretAnAutoPairLeft(t *testing.T) {
	d := testDoc(t, "")
	d.insertRune('(')
	if d.cursor != (Pos{0, 1}) {
		t.Fatalf("cursor = %+v, want between the pair", d.cursor)
	}
	d.Undo()
	d.Redo()
	if d.cursor != (Pos{0, 1}) {
		t.Errorf("after undo+redo: cursor = %+v, want {0 1}", d.cursor)
	}
	if d.selecting {
		t.Error("an auto-pair left a selection")
	}
}

// ---------------------------------------------------------------------------
// Reported as: "ctrl+y stops working if 2-3 seconds pass after ctrl+z"
// ---------------------------------------------------------------------------

// Redo is cleared by NEW INPUT and by nothing else -- no timer, no idle
// pass, no save. Asserted against a real elapsed wall-clock gap longer
// than both of this package's time constants (undoCoalesceGap, 700ms) and
// app\lifecycle.go's suspendDebounce (2s), which is the only timer in the
// app that touches this workspace at all.
func TestRedoSurvivesARealTimeGap(t *testing.T) {
	d := testDoc(t, "")
	typeString(d, "abc")
	d.Undo()
	if got := d.buf.Text(); got != "" {
		t.Fatalf("after undo = %q", got)
	}

	// Longer than undoCoalesceGap and suspendDebounce together.
	time.Sleep(2800 * time.Millisecond)

	if !d.Redo() {
		t.Fatal("redo stopped working across a 2.8s gap")
	}
	if got := d.buf.Text(); got != "abc" {
		t.Errorf("after redo = %q, want abc", got)
	}
}

// Suspend is what the 2s timer actually calls on this workspace. It must
// not disturb the history.
func TestSuspendDoesNotTouchUndoHistory(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	typeString(d, "abc")
	d.Undo()
	undo, redo := len(d.hist.undo), len(d.hist.redo)

	for i := 0; i < 3; i++ {
		if _, err := ws.Suspend(); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.hist.undo) != undo || len(d.hist.redo) != redo {
		t.Errorf("history moved across Suspend: undo %d->%d redo %d->%d",
			undo, len(d.hist.undo), redo, len(d.hist.redo))
	}
	if !d.Redo() {
		t.Error("redo stopped working after Suspend")
	}
}

// ---------------------------------------------------------------------------
// Reported as: "the first paste in a session lands character by character
// and takes several ctrl+z presses; later pastes still take 2"
// ---------------------------------------------------------------------------

// One PHYSICAL paste can reach the workspace as several PasteMsgs:
// app\paste.go reconstructs it from a burst of console key events and
// ends the burst after 25ms of quiet, so a long paste delivered in more
// than one console batch arrives in pieces. They collapse into ONE undo
// step.
func TestASplitPasteIsStillOneUndoStep(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]

	ws.Update(app.PasteMsg{Text: "line one\nline two\n"})
	ws.Update(app.PasteMsg{Text: "line three\nline four"})

	if got := d.buf.Text(); got != "line one\nline two\nline three\nline four" {
		t.Fatalf("text = %q", got)
	}
	if !d.Undo() {
		t.Fatal("nothing to undo")
	}
	if got := d.buf.Text(); got != "" {
		t.Errorf("after one ctrl+z = %q, want empty", got)
	}
}

// A paste over a selection keeps its paste kind, so the chunk that
// replaced the selection and the chunks after it are still one step.
func TestASplitPasteOverASelectionIsOneUndoStep(t *testing.T) {
	ws := testWorkspace(t, "replace me", 120, 30)
	d := ws.docs[0]
	d.selectAll()

	ws.Update(app.PasteMsg{Text: "first "})
	ws.Update(app.PasteMsg{Text: "second"})

	if got := d.buf.Text(); got != "first second" {
		t.Fatalf("text = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "replace me" {
		t.Errorf("after one ctrl+z = %q, want the original", got)
	}
}

// Two pastes that are NOT adjacent stay two steps -- coalescing is on
// contiguity, not on kind alone.
func TestTwoPastesAtDifferentPlacesStayTwoSteps(t *testing.T) {
	ws := testWorkspace(t, "ab", 120, 30)
	d := ws.docs[0]

	d.moveTo(Pos{0, 0}, false)
	ws.Update(app.PasteMsg{Text: "X"})
	d.moveTo(Pos{0, 3}, false)
	ws.Update(app.PasteMsg{Text: "Y"})

	if got := d.buf.Text(); got != "XabY" {
		t.Fatalf("text = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "Xab" {
		t.Errorf("after one undo = %q, want Xab", got)
	}
}

// A paste still never merges with TYPING either side of it.
func TestAPasteDoesNotMergeWithTyping(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	typeString(d, "before")
	ws.Update(app.PasteMsg{Text: "PASTED"})
	typeString(d, "after")

	d.Undo()
	if got := d.buf.Text(); got != "beforePASTED" {
		t.Fatalf("after one undo = %q", got)
	}
	d.Undo()
	if got := d.buf.Text(); got != "before" {
		t.Errorf("after two undos = %q", got)
	}
}

// ---------------------------------------------------------------------------
// The single-line Splice fast path
// ---------------------------------------------------------------------------

// The fast path writes into one line of the slice in place. The aliasing
// hazard the general path's own comment warns about applies here too, at
// every line length, and this is the test that would catch it.
func TestSingleLineSpliceDoesNotCorruptNeighbours(t *testing.T) {
	for n := 0; n < 40; n++ {
		body := strings.Repeat("x", n)
		b := newLineBuffer("FIRST\n" + body + "\nTHIRD\nFOURTH")
		for _, col := range []int{0, n / 2, n} {
			fresh := newLineBuffer("FIRST\n" + body + "\nTHIRD\nFOURTH")
			fresh.Splice(Pos{1, col}, Pos{1, col}, "INS")
			if got := fresh.Line(0); got != "FIRST" {
				t.Fatalf("n=%d col=%d: line 0 = %q", n, col, got)
			}
			if got := fresh.Line(2); got != "THIRD" {
				t.Fatalf("n=%d col=%d: line 2 = %q", n, col, got)
			}
			if got := fresh.Line(3); got != "FOURTH" {
				t.Fatalf("n=%d col=%d: line 3 = %q", n, col, got)
			}
			want := body[:col] + "INS" + body[col:]
			if got := fresh.Line(1); got != want {
				t.Fatalf("n=%d col=%d: line 1 = %q, want %q", n, col, got, want)
			}
		}
		if b.Lines() != 4 {
			t.Fatalf("n=%d: Lines = %d", n, b.Lines())
		}
	}
}

// The fast path and the general path must agree on the returned end
// position and on the resulting text, for every shape that can take
// either route.
func TestSingleLineAndGeneralSpliceAgree(t *testing.T) {
	cases := []struct {
		from, to Pos
		repl     string
	}{
		{Pos{1, 0}, Pos{1, 0}, "x"},
		{Pos{1, 2}, Pos{1, 4}, ""},
		{Pos{1, 2}, Pos{1, 2}, "abc"},
		{Pos{1, 0}, Pos{1, 5}, "Z"},
		{Pos{1, 3}, Pos{1, 3}, "a\nb"}, // multi-line: the general path
		{Pos{0, 1}, Pos{2, 1}, "J"},    // spans lines: the general path
	}
	for _, c := range cases {
		b := newLineBuffer("one\ntwothree\nfour")
		// The expected answer, computed independently of either path.
		full := b.Text()
		lines := strings.Split(full, "\n")
		offset := func(p Pos) int {
			n := 0
			for i := 0; i < p.Line; i++ {
				n += len([]rune(lines[i])) + 1
			}
			return n + p.Col
		}
		runes := []rune(full)
		want := string(runes[:offset(c.from)]) + c.repl + string(runes[offset(c.to):])

		end := b.Splice(c.from, c.to, c.repl)
		if got := b.Text(); got != want {
			t.Errorf("%+v..%+v %q: text = %q, want %q", c.from, c.to, c.repl, got, want)
		}
		if got := advance(c.from, c.repl); end != got {
			t.Errorf("%+v..%+v %q: end = %+v, advance says %+v", c.from, c.to, c.repl, end, got)
		}
	}
}
