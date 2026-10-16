package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "shift+right":
		return tea.KeyMsg{Type: tea.KeyShiftRight}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+z":
		return tea.KeyMsg{Type: tea.KeyCtrlZ}
	case "ctrl+y":
		return tea.KeyMsg{Type: tea.KeyCtrlY}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func press(ws *Workspace, keys ...string) {
	for _, k := range keys {
		ws.handleKey(key(k))
	}
}

// Tab indents through the key path, which is only reachable because
// CapturesKey claims it -- this is the end of the chain the app-level test
// (app\keycapture_test.go) starts.
func TestTabKeyIndents(t *testing.T) {
	ws := testWorkspace(t, "def f():\n    a = 1\n    b = 2\n", 120, 30)
	d := ws.docs[0]
	d.anchor, d.cursor, d.selecting = Pos{1, 0}, Pos{2, 1}, true

	press(ws, "tab")
	if got := d.buf.Line(1); got != "        a = 1" {
		t.Errorf("line 1 = %q", got)
	}
	press(ws, "shift+tab")
	if got := d.buf.Line(1); got != "    a = 1" {
		t.Errorf("after shift+tab = %q", got)
	}
}

func TestTypingAndEditingThroughTheKeyPath(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]

	press(ws, "h", "i", "space", "t", "h", "e", "r", "e")
	if got := d.buf.Text(); got != "hi there" {
		t.Fatalf("text = %q", got)
	}
	press(ws, "enter", "x")
	if got := d.buf.Text(); got != "hi there\nx" {
		t.Fatalf("after enter = %q", got)
	}
	press(ws, "backspace", "backspace")
	if got := d.buf.Text(); got != "hi there" {
		t.Errorf("after backspaces = %q", got)
	}
}

func TestUndoRedoThroughTheKeyPath(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	d := ws.docs[0]
	press(ws, "a", "b", "c")
	press(ws, "ctrl+z")
	if got := d.buf.Text(); got != "" {
		t.Fatalf("after ctrl+z = %q", got)
	}
	press(ws, "ctrl+y")
	if got := d.buf.Text(); got != "abc" {
		t.Errorf("after ctrl+y = %q", got)
	}
}

func TestSelectAllAndShiftArrowThroughTheKeyPath(t *testing.T) {
	ws := testWorkspace(t, "hello", 120, 30)
	d := ws.docs[0]

	press(ws, "ctrl+a")
	if !d.hasSelection() {
		t.Fatal("ctrl+a did not select")
	}

	press(ws, "left") // collapse
	press(ws, "shift+right", "shift+right")
	from, to := d.selectionRange()
	if got := d.buf.Slice(from, to); got != "he" {
		t.Errorf("selection = %q, want he", got)
	}
}

// ctrl+n / ctrl+b cycle tabs, because ctrl+tab is indistinguishable from
// tab on Windows (keys.go's header). The empty state advertises them.
func TestCtrlNAndCtrlBCycleTabs(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.docs = append(ws.docs, testDoc(t, "b"), testDoc(t, "c"))

	press(ws, "ctrl+n")
	if ws.active != 1 {
		t.Fatalf("active = %d after ctrl+n", ws.active)
	}
	press(ws, "ctrl+b", "ctrl+b")
	if ws.active != 2 {
		t.Errorf("active = %d after two ctrl+b, want a wrap to 2", ws.active)
	}
}

func TestCtrlOTogglesFocusBetweenBufferAndTree(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	if ws.focus != focusBuffer {
		t.Fatalf("focus starts at %v", ws.focus)
	}
	press(ws, "ctrl+o")
	if ws.focus != focusTree {
		t.Fatal("ctrl+o did not focus the tree")
	}
	press(ws, "ctrl+o")
	if ws.focus != focusBuffer {
		t.Error("ctrl+o did not focus the buffer again")
	}
}

// With the tree focused, typing does NOT reach the buffer -- that is the
// whole reason the focus split exists.
func TestTypingDoesNotReachTheBufferWhileTheTreeHasFocus(t *testing.T) {
	ws := testWorkspace(t, "x", 120, 30)
	before := ws.docs[0].buf.Text()
	press(ws, "ctrl+o", "a", "b", "c")
	if got := ws.docs[0].buf.Text(); got != before {
		t.Errorf("text = %q, want unchanged", got)
	}
}

// j/k navigate the tree, matching chat's own browse keys, and the arrows
// work too.
func TestTreeNavigationKeys(t *testing.T) {
	ws := testWorkspace(t, "x", 120, 30)
	ws.tree.build([]string{"a.py", "b.py", "c.py"})
	press(ws, "ctrl+o")

	press(ws, "j", "j")
	if ws.tree.sel != 2 {
		t.Fatalf("sel = %d after two j", ws.tree.sel)
	}
	press(ws, "k")
	if ws.tree.sel != 1 {
		t.Fatalf("sel = %d after k", ws.tree.sel)
	}
	press(ws, "home")
	if ws.tree.sel != 0 {
		t.Errorf("sel = %d after home", ws.tree.sel)
	}
}

// Enter on a file in the tree opens it; on a directory it expands.
func TestEnterOpensAFileAndExpandsADirectory(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"pkg/b.py", "a.py"})
	ws.focus = focusTree

	ws.tree.sel = 0 // pkg
	press(ws, "enter")
	if len(rec.sent) != 0 {
		t.Fatalf("Enter on a directory read a file: %+v", rec.sent)
	}
	if !ws.tree.rows[0].node.expanded {
		t.Fatal("Enter did not expand the directory")
	}

	ws.tree.sel = 1 // pkg/b.py
	press(ws, "enter")
	if len(rec.sent) != 1 {
		t.Fatalf("sent %+v, want one read", rec.sent)
	}
	if got := rec.sent[0].payload["path"]; got != "C:\\p\\pkg\\b.py" {
		t.Errorf("path = %v, want a Windows path under the project root", got)
	}
}

// With nothing open, the arrows drive the tree rather than doing nothing:
// it is the only navigable thing on screen, and an editor that ignores
// every key until a file is open reads as frozen.
func TestArrowsDriveTheTreeWithNothingOpen(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py"})

	press(ws, "down")
	if ws.tree.sel != 1 {
		t.Errorf("sel = %d, want the arrow to have moved the tree", ws.tree.sel)
	}
}

// A paste is ONE insert and therefore one undo step, through the message
// Root synthesises on Windows (app\paste.go).
func TestPasteMsgInsertsOnceAndUndoesOnce(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	ws.Update(app.PasteMsg{Text: "one\ntwo\nthree"})

	d := ws.docs[0]
	if got := d.buf.Text(); got != "one\ntwo\nthree" {
		t.Fatalf("text = %q", got)
	}
	press(ws, "ctrl+z")
	if got := d.buf.Text(); got != "" {
		t.Errorf("after one ctrl+z = %q, want empty", got)
	}
}

// A paste while the tree has focus does not land in the buffer.
func TestPasteMsgIgnoredWhileTheTreeHasFocus(t *testing.T) {
	ws := testWorkspace(t, "", 120, 30)
	ws.focus = focusTree
	ws.Update(app.PasteMsg{Text: "nope"})
	if got := ws.docs[0].buf.Text(); got != "" {
		t.Errorf("text = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

func mouse(x, y int, btn tea.MouseButton, action tea.MouseAction) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: btn, Action: action}
}

// A press plants the caret and the anchor, motion extends, release ends
// the drag -- F13's "selection renders correctly with mouse drag".
func TestMouseDragSelects(t *testing.T) {
	ws := testWorkspace(t, "one two three\nfour five six", 120, 30)
	d := ws.docs[0]
	ta := ws.textArea

	ws.handleMouse(mouse(ta.X+4, ta.Y, tea.MouseButtonLeft, tea.MouseActionPress))
	if d.cursor != (Pos{0, 4}) {
		t.Fatalf("press put the caret at %+v, want {0 4}", d.cursor)
	}
	if !ws.dragging {
		t.Fatal("the press did not start a drag")
	}

	ws.handleMouse(mouse(ta.X+7, ta.Y+1, tea.MouseButtonLeft, tea.MouseActionMotion))
	from, to := d.selectionRange()
	if got := d.buf.Slice(from, to); got != "two three\nfour fi" {
		t.Errorf("selection = %q", got)
	}

	ws.handleMouse(mouse(ta.X+7, ta.Y+1, tea.MouseButtonLeft, tea.MouseActionRelease))
	if ws.dragging {
		t.Error("the release did not end the drag")
	}
}

// Motion with no button held is not a drag.
func TestMouseMotionWithoutAPressDoesNotSelect(t *testing.T) {
	ws := testWorkspace(t, "one two", 120, 30)
	ta := ws.textArea
	ws.handleMouse(mouse(ta.X+3, ta.Y, tea.MouseButtonNone, tea.MouseActionMotion))
	if ws.docs[0].hasSelection() {
		t.Error("a bare motion selected text")
	}
}

// A click on a wide rune resolves to the START of that glyph: there is no
// position between a double-width rune's two cells.
func TestClickOnAWideRuneLandsOnItsStart(t *testing.T) {
	ws := testWorkspace(t, "a日本b", 120, 30)
	d := ws.docs[0]
	ta := ws.textArea
	// Cell 2 is the second half of 日 (a=1 cell, 日=cells 1-2).
	ws.handleMouse(mouse(ta.X+2, ta.Y, tea.MouseButtonLeft, tea.MouseActionPress))
	if d.cursor.Col != 1 {
		t.Errorf("cursor col = %d, want 1 (the start of 日)", d.cursor.Col)
	}
}

// Dragging off the bottom edge scrolls one line per event rather than
// jumping: there is no line under a pointer outside the box.
func TestDraggingOffTheEdgeScrolls(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("line\n")
	}
	ws := testWorkspace(t, sb.String(), 120, 30)
	d := ws.docs[0]
	ta := ws.textArea

	ws.handleMouse(mouse(ta.X, ta.Y, tea.MouseButtonLeft, tea.MouseActionPress))
	before := d.top
	for i := 0; i < 3; i++ {
		ws.handleMouse(mouse(ta.X, ta.Y+ta.H+5, tea.MouseButtonLeft, tea.MouseActionMotion))
	}
	if d.top != before+3 {
		t.Errorf("top = %d, want %d", d.top, before+3)
	}
}

func TestWheelScrollsTheBufferWithoutMovingTheCaret(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("line\n")
	}
	ws := testWorkspace(t, sb.String(), 120, 30)
	d := ws.docs[0]
	before := d.cursor

	ws.handleMouse(mouse(ws.textArea.X, ws.textArea.Y,
		tea.MouseButtonWheelDown, tea.MouseActionPress))
	if d.top == 0 {
		t.Error("the wheel did not scroll")
	}
	if d.cursor != before {
		t.Errorf("the wheel moved the caret to %+v", d.cursor)
	}
}

// A click on the tab strip selects that tab, and a click on its × closes
// it. Hit-tested against the spans renderTabs recorded, so the click and
// the glyph it looks like it hit can never disagree.
func TestClickOnTheTabStrip(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	second := testDoc(t, "b")
	second.path, second.rel = "C:\\p\\second.py", "second.py"
	ws.docs = append(ws.docs, second)
	ws.View(ws.lastLayout) // re-render so the spans cover both tabs

	if len(ws.tabSpans) != 2 {
		t.Fatalf("%d spans", len(ws.tabSpans))
	}
	base := ws.tabStrip.X

	ws.handleMouse(mouse(base+ws.tabSpans[1].from, ws.tabStrip.Y,
		tea.MouseButtonLeft, tea.MouseActionPress))
	if ws.active != 1 {
		t.Fatalf("active = %d after clicking the second tab", ws.active)
	}

	ws.handleMouse(mouse(base+ws.tabSpans[1].closeAt, ws.tabStrip.Y,
		tea.MouseButtonLeft, tea.MouseActionPress))
	if len(ws.docs) != 1 {
		t.Errorf("%d docs after clicking ×, want 1", len(ws.docs))
	}
}

// ONE click selects and opens — F13's "open on ⏎ or click", singular.
//
// It took two clicks until a verification round found that the row which
// happened to be selected already (row 0 on a fresh tree) opened on the
// first click while every other row needed a second one.
func TestOneClickInTheTreeOpens(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py"})
	ws.View(layoutFor(ws, 120, 30))

	y := ws.treeRect.Y + 1 + 1 // the top border, then content row 1
	ws.handleMouse(mouse(ws.treeRect.X+2, y, tea.MouseButtonLeft, tea.MouseActionPress))

	if ws.tree.sel != 1 {
		t.Fatalf("sel = %d, want 1", ws.tree.sel)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("one click sent %+v, want one read", rec.sent)
	}
	if got := rec.sent[0].payload["path"]; got != "C:\\p\\b.py" {
		t.Errorf("opened %v, want b.py", got)
	}
}

// The FIRST content row opens on one click too — it is the row the
// reported symptom was about, and the one most likely to be confused with
// the border above it.
func TestTheTopTreeRowOpensOnOneClick(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py"})
	ws.View(layoutFor(ws, 120, 30))

	ws.handleMouse(mouse(ws.treeRect.X+2, ws.treeRect.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))
	if len(rec.sent) != 1 {
		t.Fatalf("the top row sent %+v, want one read", rec.sent)
	}
	if got := rec.sent[0].payload["path"]; got != "C:\\p\\a.py" {
		t.Errorf("opened %v, want a.py", got)
	}
}

// A click on the card's BORDER is not a click on a row: it moves focus and
// changes nothing else. Previously it mapped to row -1 and was swallowed,
// which is indistinguishable from the tree ignoring the click.
func TestClickOnTheTreeBorderSelectsNothing(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py"})
	ws.View(layoutFor(ws, 120, 30))
	ws.tree.sel = 1

	for _, y := range []int{ws.treeRect.Y, ws.treeRect.Y + ws.treeRect.H - 1} {
		ws.handleMouse(mouse(ws.treeRect.X+2, y, tea.MouseButtonLeft, tea.MouseActionPress))
		if len(rec.sent) != 0 {
			t.Fatalf("a border click at y=%d opened %+v", y, rec.sent)
		}
		if ws.tree.sel != 1 {
			t.Fatalf("a border click at y=%d moved the selection to %d", y, ws.tree.sel)
		}
	}
	if ws.focus != focusTree {
		t.Error("a border click did not move focus to the tree")
	}
}
