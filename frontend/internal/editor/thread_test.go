package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Regressions from 18a's second verification round.

// ---------------------------------------------------------------------------
// Thread 1 — the bare-modifier NUL KeyMsg
// ---------------------------------------------------------------------------

// bareModifierKey is exactly what bubbletea's Windows console reader
// produces for a BARE Ctrl key-down, and Alt, Win and CapsLock alike:
// readConInputs skips key-UP records and VK_SHIFT and nothing else,
// keyType has no case for VK_CONTROL so it ends in `return KeyRunes`, and
// a modifier record's Char is 0.
//
// app.Root.handleKey drops it. These tests go through the EDITOR's own key
// path, which is downstream of that filter, to pin what the editor must do
// if one ever reaches it again -- the filter is one `if` in another
// package and this is the layer that paid for its absence.
func bareModifierKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}
}

// Reported as: "selection collapses the instant Ctrl is pressed, before
// the paired key arrives". Measured before the fix: the record read
// removed="hello world" inserted="\x00".
func TestBareModifierDoesNotCollapseASelection(t *testing.T) {
	ws := testWorkspace(t, "hello world", 120, 30)
	d := ws.docs[0]
	d.selectAll()

	ws.handleKey(bareModifierKey())

	if got := d.buf.Text(); got != "hello world" {
		t.Errorf("text = %q, want it untouched", got)
	}
	if !d.hasSelection() {
		t.Error("the selection was collapsed by a bare modifier")
	}
	if d.dirty {
		t.Error("a bare modifier marked the document dirty")
	}
}

// Reported as: "ctrl+z needs two presses to undo one real edit".
// Measured before the fix: `typed+paused: recs=1` then `bare ctrl: recs=2`,
// so the first ctrl+z undid an invisible character.
func TestBareModifierCostsNoUndoStep(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	typeString(d, "word")
	// The pause a user takes before reaching for ctrl+z, so a NUL could
	// not hide inside the typing record by coalescing with it.
	for i := range d.hist.undo {
		d.hist.undo[i].at = d.hist.undo[i].at.Add(-2 * undoCoalesceGap)
	}
	recs := len(d.hist.undo)

	ws.handleKey(bareModifierKey())
	if got := len(d.hist.undo); got != recs {
		t.Fatalf("%d undo records after a bare modifier, want %d", got, recs)
	}

	// ONE press undoes the real edit.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := d.buf.Text(); got != "" {
		t.Errorf("after one ctrl+z = %q, want empty", got)
	}
}

// Reported as: "ctrl+y needs two presses to redo one". The same NUL push
// cleared the redo stack outright -- measured redo 1 -> 0.
func TestBareModifierDoesNotClearRedo(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	typeString(d, "abc")
	d.Undo()

	ws.handleKey(bareModifierKey())

	if len(d.hist.redo) != 1 {
		t.Fatalf("%d redo records after a bare modifier, want 1", len(d.hist.redo))
	}
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	if got := d.buf.Text(); got != "abc" {
		t.Errorf("after one ctrl+y = %q, want abc", got)
	}
}

// Reported as: "right-arrow and ctrl+right-arrow intermittently make the
// caret disappear and skip characters".
//
// A NUL in the line is zero display cells wide, so TWO cursor columns map
// to the SAME display cell -- an arrow press that moves the cursor and not
// the caret -- and the caret drawn on one is a zero-width glyph. Measured
// before the fix on "a\x00bc": columns 1 and 2 both at cell 1.
func TestBareModifierLeavesNoZeroWidthRuneInTheLine(t *testing.T) {
	ws := testWorkspace(t, "abc", 120, 30)
	d := ws.docs[0]
	d.moveTo(Pos{0, 1}, false)

	for i := 0; i < 5; i++ {
		ws.handleKey(bareModifierKey())
	}
	if got := d.buf.Line(0); got != "abc" {
		t.Fatalf("line = %q", got)
	}

	// Every column maps to its own cell, and every arrow press advances
	// the caret by at least one cell.
	runes := d.buf.Runes(0)
	seen := map[int]int{}
	for col := 0; col <= len(runes); col++ {
		cell := displayCol(runes, col)
		if prev, dup := seen[cell]; dup {
			t.Errorf("columns %d and %d both map to cell %d", prev, col, cell)
		}
		seen[cell] = col
	}
}

// Every modifier the reader lets through, not just Ctrl: Alt, Win and
// CapsLock reach keyType the same way and produce the same NUL.
func TestEveryBareModifierShapeIsHarmless(t *testing.T) {
	shapes := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{0}},
		{Type: tea.KeyRunes, Runes: []rune{0, 0}},
	}
	for _, k := range shapes {
		ws := testWorkspace(t, "text", 120, 30)
		d := ws.docs[0]
		d.selectAll()
		ws.handleKey(k)
		if got := d.buf.Text(); got != "text" {
			t.Errorf("%v: text = %q", k.Runes, got)
		}
		if !d.hasSelection() {
			t.Errorf("%v: the selection was collapsed", k.Runes)
		}
	}
}

// ...and a REAL keystroke is still a keystroke. The filter must not have
// turned the editor deaf.
func TestOrdinaryTypingStillWorks(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	for _, r := range "hello" {
		ws.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := d.buf.Text(); got != "hello" {
		t.Errorf("text = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Group A residual — the tab strip and the tree's first row share a row
// ---------------------------------------------------------------------------

// The Code File box's first content row and the File Tree card's first
// content row are the SAME screen row: Compute starts both panes at
// bodyTop. The tab strip used to be hit-tested on Y alone, so every click
// on that row -- right across the terminal, File Tree included -- was
// routed to the strip, matched no tab span, and was dropped.
//
// Reported as "backend\ at the top of the tree does not collapse/expand
// and files under it don't open". The fix is in the DISPATCH, not in
// either handler: both were correct and unreachable.
func TestTheTabRowAndTheTreesFirstRowAreTheSameRow(t *testing.T) {
	ws := testWorkspace(t, "x\n", 120, 30)
	ws.View(ws.lastLayout)

	if ws.tabStrip.IsZero() {
		t.Fatal("no tab strip was recorded")
	}
	if ws.tabStrip.Y != ws.treeRect.Y+1 {
		t.Skipf("the panes no longer collide (strip y=%d, tree row 0 y=%d); "+
			"this test exists for the case where they do",
			ws.tabStrip.Y, ws.treeRect.Y+1)
	}
	// They collide, which is exactly why the strip must be bounded on X.
	if ws.tabStrip.X+ws.tabStrip.W > ws.treeRect.X {
		t.Errorf("the strip rect (x=%d w=%d) overlaps the tree (x=%d)",
			ws.tabStrip.X, ws.tabStrip.W, ws.treeRect.X)
	}
}

// A DIRECTORY on the tree's first row expands on one click, with a file
// open so the tab strip exists on that same row.
//
// The previous round's test for this passed for the wrong reason: it had
// no file open, so codeFileBox returned through its empty state and never
// recorded a tab row at all. Every test here opens a file first.
func TestTheTopTreeRowExpandsADirectoryOnOneClick(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	ws.docs = append(ws.docs, testDoc(t, "x\n"))
	ws.tree.build([]string{"backend/a.py", "backend/b.py", "design/c.md", "top.md"})
	ws.View(layoutFor(ws, 120, 30))

	if got := ws.tree.rows[0].node.path; got != "backend" {
		t.Fatalf("row 0 is %q, want backend", got)
	}
	if ws.tree.rows[0].node.expanded {
		t.Fatal("precondition: backend starts expanded")
	}

	ws.handleMouse(mouse(ws.treeRect.X+3, ws.treeRect.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if !ws.tree.rows[0].node.expanded {
		t.Error("one click on the top row did not expand the directory")
	}
	if len(rec.sent) != 0 {
		t.Errorf("expanding a directory read a file: %+v", rec.sent)
	}

	// And it collapses again on a second click.
	ws.View(ws.lastLayout)
	ws.handleMouse(mouse(ws.treeRect.X+3, ws.treeRect.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))
	if ws.tree.rows[0].node.expanded {
		t.Error("a second click did not collapse the directory")
	}
}

// A FILE on the tree's first row opens on one click, with a file open.
func TestTheTopTreeRowOpensAFileOnOneClickWithATabStripPresent(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	ws.docs = append(ws.docs, testDoc(t, "x\n"))
	ws.tree.build([]string{"a.py", "b.py"})
	ws.View(layoutFor(ws, 120, 30))

	ws.handleMouse(mouse(ws.treeRect.X+3, ws.treeRect.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if len(rec.sent) != 1 {
		t.Fatalf("sent %+v, want one read", rec.sent)
	}
	if got := rec.sent[0].payload["path"]; got != "C:/p\\a.py" {
		t.Errorf("opened %v, want a.py", got)
	}
}

// EVERY visible tree row opens or toggles on one click, at every size --
// the property the per-row tests only sample.
func TestEveryTreeRowIsClickableAtEverySize(t *testing.T) {
	for _, dim := range [][2]int{{120, 30}, {100, 26}, {90, 24}, {200, 50}} {
		ws, rec := newWorkspaceWithRecorder()
		ws.projectID, ws.projectRoot = "p1", "C:/p"
		ws.docs = append(ws.docs, testDoc(t, "x\n"))
		ws.tree.build([]string{"a.py", "b.py", "c.py", "d.py", "e.py"})
		ws.View(layoutFor(ws, dim[0], dim[1]))
		if ws.treeRect.IsZero() {
			continue // the pane is hidden at this size
		}

		rows := ws.treeRect.H - 2
		if rows > len(ws.tree.rows) {
			rows = len(ws.tree.rows)
		}
		for i := 0; i < rows; i++ {
			before := len(rec.sent)
			ws.handleMouse(mouse(ws.treeRect.X+3, ws.treeRect.Y+1+i,
				tea.MouseButtonLeft, tea.MouseActionPress))
			if len(rec.sent) != before+1 {
				t.Errorf("%dx%d: clicking tree row %d (y=%d) opened nothing",
					dim[0], dim[1], i, ws.treeRect.Y+1+i)
			}
		}
	}
}

// And a click on the tab strip still hits the strip -- the X bound must
// not have broken the thing it was added to protect.
func TestTheTabStripStillTakesItsOwnClicks(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	second := testDoc(t, "b")
	second.path, second.rel = "C:/p/second.py", "second.py"
	ws.docs = append(ws.docs, second)
	ws.View(ws.lastLayout)

	if len(ws.tabSpans) != 2 {
		t.Fatalf("%d spans", len(ws.tabSpans))
	}
	ws.handleMouse(mouse(ws.tabStrip.X+ws.tabSpans[1].from, ws.tabStrip.Y,
		tea.MouseButtonLeft, tea.MouseActionPress))
	if ws.active != 1 {
		t.Errorf("active = %d after clicking the second tab", ws.active)
	}
}

// A click on the tab strip's ROW but outside the Code File box reaches
// whatever is actually there, not the strip.
func TestAClickOnTheStripRowOutsideTheBoxIsNotATabClick(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.docs = append(ws.docs, testDoc(t, "b"))
	ws.View(ws.lastLayout)
	before := ws.active

	// Far right, on the strip's row: inside the tree card.
	ws.handleMouse(mouse(ws.treeRect.X+5, ws.tabStrip.Y,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if ws.active != before {
		t.Errorf("active changed to %d; a tree click was read as a tab click", ws.active)
	}
	if ws.focus != focusTree {
		t.Error("the click did not reach the tree")
	}
}

// The strip's rect is cleared when there is nothing to draw it for, so a
// stale row cannot swallow clicks after the last file is closed.
func TestTheStripRectIsClearedWithNoFileOpen(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	if ws.tabStrip.IsZero() {
		t.Fatal("precondition: no strip with a file open")
	}
	ws.dropTab(0)
	ws.View(ws.lastLayout)
	if !ws.tabStrip.IsZero() {
		t.Errorf("tabStrip = %+v after the last file closed, want zero", ws.tabStrip)
	}
	if !strings.Contains(stripANSI(ws.View(ws.lastLayout)), "No file open") {
		t.Error("the empty state is not showing")
	}
}
