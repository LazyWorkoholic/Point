package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Key routing is scoped to whichever pane actually has the keyboard,
// and the focus ring agrees with it.
//
// F13a kept focus on the BUFFER with nothing open and had bufferKey
// forward the arrow keys to the tree, so the keys worked and the focus
// ring lied about why -- reported as "arrow keys leak to the file tree
// even when the editor pane has focus". Both halves are asserted here:
// where the keys go, and which pane is drawn focused.
func TestWithNothingOpenTheTreeHasTheKeyboardAndSaysSo(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py", "c.py"})

	if ws.effectiveFocus() != focusTree {
		t.Fatal("with nothing open the tree does not have the keyboard")
	}

	// The arrows still work -- the frozen-editor problem F13a's
	// forwarding existed to avoid.
	before := ws.tree.sel
	ws.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if ws.tree.sel == before {
		t.Error("down did not move the tree selection")
	}

	// ...and the pane the keys move is the pane drawn as focused. This
	// is the half F13a got wrong, and the half a first version of this
	// test failed to assert at all -- reverting render.go to read
	// w.focus left it green.
	//
	// Asserted without naming a colour: with nothing open,
	// effectiveFocus ignores w.focus entirely, so the RAW frame -- escape
	// sequences and all -- must be byte-identical whichever way w.focus
	// happens to be set. If any render path still reads w.focus, the two
	// frames carry different border roles and differ.
	l := layoutFor(ws, 120, 30)
	ws.focus = focusBuffer
	asBuffer := ws.View(l)
	ws.focus = focusTree
	asTree := ws.View(l)
	if asBuffer != asTree {
		t.Error("the frame changes with w.focus while nothing is open -- a render " +
			"path still reads the stored focus, so the focus ring can point at " +
			"the buffer pane while the tree takes the keys")
	}
	if !strings.Contains(stripANSI(asTree), "No file open") {
		t.Fatal("precondition: the empty state is not showing")
	}
}

// With a file open the BUFFER has it, and the arrows move the caret
// rather than the tree.
func TestWithAFileOpenTheArrowsDoNotReachTheTree(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py", "c.py"})
	ws.docs = append(ws.docs, testDoc(t, "x"))

	if ws.effectiveFocus() != focusBuffer {
		t.Fatalf("effectiveFocus = %v with a file open", ws.effectiveFocus())
	}

	treeBefore := ws.tree.sel
	ws.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if ws.tree.sel != treeBefore {
		t.Error("an arrow key moved the TREE while the buffer had focus")
	}
}

// ctrl+o still hands it over explicitly, and back.
func TestCtrlOSwitchesFocusWhenThereIsAFileToSwitchAwayFrom(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py", "b.py"})
	ws.docs = append(ws.docs, testDoc(t, "x"))

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if ws.effectiveFocus() != focusTree {
		t.Fatal("ctrl+o did not move focus to the tree")
	}
	sel := ws.tree.sel
	ws.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if ws.tree.sel == sel {
		t.Error("the tree does not take the arrows once it has focus")
	}

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if ws.effectiveFocus() != focusBuffer {
		t.Error("ctrl+o did not hand focus back to the buffer")
	}
}

// Closing the last tab hands the keyboard back without anything having
// to remember to do it -- effectiveFocus is derived, not stored.
//
// A stored field was tried first and six tests caught it: a workspace
// whose documents arrive by a path that does not update the field keeps
// the tree focused and swallows every keystroke. "Set this flag
// everywhere the list changes" is the shape of F13a's dirty-notice
// latch, where one flag cleared in one place stood in for state three
// places could move.
func TestFocusFollowsTheDocumentListWithNothingToKeepInStep(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.tree.build([]string{"a.py"})
	ws.docs = append(ws.docs, testDoc(t, "x"))
	ws.focus = focusBuffer

	if ws.effectiveFocus() != focusBuffer {
		t.Fatal("precondition")
	}
	ws.dropTab(0)
	if ws.effectiveFocus() != focusTree {
		t.Error("closing the last tab left the keyboard on an empty buffer pane")
	}

	// And a document arriving by ANY path takes it back, with no
	// bookkeeping call in between.
	ws.docs = append(ws.docs, testDoc(t, "y"))
	if ws.effectiveFocus() != focusBuffer {
		t.Error("a document arrived and the buffer did not get the keyboard")
	}
}
