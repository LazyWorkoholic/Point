package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// Opening a file loads it and does NOT move the keyboard (the VS Code
// explorer convention). The editor takes focus only when asked for:
// ctrl+o, or a click into its pane or tab strip.
//
// Scope note: this is beyond F13b's own doc -- a deliberate behaviour
// change, not a bug fix.

// ---------------------------------------------------------------------------
// The request
// ---------------------------------------------------------------------------

func TestOpeningAFileFromTheTreeLeavesTheTreeFocused(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.tree.build([]string{"a.py", "b.py", "c.py"})

	// Nothing open: the tree has the keyboard by derivation.
	if ws.effectiveFocus() != focusTree {
		t.Fatal("precondition: the tree does not have the keyboard")
	}

	openFile(t, ws, "a.py", wireFile{Writable: true})

	if ws.effectiveFocus() != focusTree {
		t.Fatalf("effectiveFocus = %v after an open; opening must not move the keyboard",
			ws.effectiveFocus())
	}
	if len(ws.docs) != 1 {
		t.Fatalf("%d documents open", len(ws.docs))
	}

	// ...and the arrows still drive the TREE, which is the whole point:
	// browsing and opening several files for later must not throw you
	// out of the explorer each time.
	sel := ws.tree.sel
	ws.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if ws.tree.sel == sel {
		t.Error("the arrows stopped driving the tree after a file opened")
	}
	// Nothing was typed into the buffer.
	if ws.docs[0].dirty {
		t.Error("a tree keystroke reached the buffer")
	}
}

// Opening SEVERAL in a row keeps the tree the whole time -- the
// behaviour the convention exists for.
func TestOpeningSeveralFilesNeverLeavesTheTree(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.tree.build([]string{"a.py", "b.py", "c.py"})

	for _, name := range []string{"a.py", "b.py", "c.py"} {
		openFile(t, ws, name, wireFile{Writable: true})
		if ws.effectiveFocus() != focusTree {
			t.Fatalf("lost the tree after opening %s", name)
		}
	}
	if len(ws.docs) != 3 {
		t.Fatalf("%d documents open, want 3", len(ws.docs))
	}
}

// Re-selecting a file that is ALREADY open switches the active document
// and still does not move the keyboard.
func TestReopeningAnOpenFileSwitchesTabsWithoutTakingFocus(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.tree.build([]string{"a.py", "b.py"})
	a := openFile(t, ws, "a.py", wireFile{Writable: true})
	openFile(t, ws, "b.py", wireFile{Writable: true})
	if ws.active != 1 {
		t.Fatalf("active = %d", ws.active)
	}

	ws.openPath(a.path)

	if ws.active != 0 {
		t.Errorf("active = %d, want the re-selected document", ws.active)
	}
	if ws.effectiveFocus() != focusTree {
		t.Error("re-selecting an open file took the keyboard")
	}
}

// ---------------------------------------------------------------------------
// ...and the document is fully functional once focus is asked for
// ---------------------------------------------------------------------------

// ctrl+o hands it over, and the document that was opened while the tree
// held the keyboard edits, undoes and saves exactly as one opened any
// other way.
func TestAFileOpenedWithoutFocusWorksFullyOnceCtrlOGivesItFocus(t *testing.T) {
	ws, rec := lockWorkspace(t)
	ws.tree.build([]string{"a.py"})
	d := openFile(t, ws, "a.py", wireFile{Writable: true, Content: "a\nb"})
	d.lock = lockHeld
	before := d.buf.Text()

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if ws.effectiveFocus() != focusBuffer {
		t.Fatalf("ctrl+o did not hand the keyboard to the buffer (focus = %v)",
			ws.effectiveFocus())
	}

	// It types.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Z'}})
	if d.buf.Text() == before {
		t.Fatal("the buffer did not take the keystroke")
	}
	if !d.dirty {
		t.Error("the edit did not mark the document dirty")
	}

	// It undoes.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if d.buf.Text() != before {
		t.Errorf("ctrl+z left %q, want %q", d.buf.Text(), before)
	}

	// It saves.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	rec.sent = nil
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if n := len(writesIn(rec)); n != 1 {
		t.Errorf("%d writes from ctrl+s, want 1", n)
	}
}

// A direct CLICK into the editor pane is the other explicit route.
func TestAClickIntoTheEditorPaneTakesFocus(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	// testWorkspace raw-appends its document, so the stored zero value
	// governs; put the keyboard on the tree the way ctrl+o would.
	ws.focus = focusTree
	if ws.effectiveFocus() != focusTree {
		t.Fatalf("precondition: focus = %v", ws.effectiveFocus())
	}

	ws.handleMouse(mouse(ws.textArea.X+2, ws.textArea.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if ws.effectiveFocus() != focusBuffer {
		t.Error("a click into the editor pane did not take the keyboard")
	}
}

// A click in the TREE keeps the tree, including the single-click open
// that clickTree performs on the way. This is the path the old code
// broke: clickTree set focusTree and openPath then set it straight back
// to focusBuffer.
func TestASingleClickOpenInTheTreeKeepsTheTree(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.focus = focusBuffer
	if ws.effectiveFocus() != focusBuffer {
		t.Fatalf("precondition: focus = %v", ws.effectiveFocus())
	}

	// A click on a tree row: selects, opens, and takes the keyboard.
	ws.handleMouse(mouse(ws.treeRect.X+2, ws.treeRect.Y+1,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if ws.effectiveFocus() != focusTree {
		t.Error("a click in the tree did not keep the keyboard there")
	}
}

// ---------------------------------------------------------------------------
// ...without reintroducing the arrow-key leak
// ---------------------------------------------------------------------------

// The guard from two rounds ago, re-asserted against the new carry-over:
// the focus RING must still agree with where the keys go. With the tree
// holding the keyboard and a document open, the frame must be the one
// the stored focusTree produces -- not the buffer-focused frame.
func TestTheFocusRingStillAgreesWithTheRoutingAfterAnOpen(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.tree.build([]string{"a.py"})
	openFile(t, ws, "a.py", wireFile{Writable: true})
	l := layoutFor(ws, 120, 30)

	treeFocused := ws.View(l)

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	bufferFocused := ws.View(l)

	if treeFocused == bufferFocused {
		t.Error("the frame does not change when focus moves between the panes — " +
			"the focus ring is not tracking the keyboard")
	}
	// And the one that was rendered after the open is the TREE's.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if ws.View(l) != treeFocused {
		t.Error("toggling back did not restore the tree-focused frame")
	}
}

// Nothing stored can drift: a document arriving by any path leaves the
// keyboard where the derivation says it is, with no bookkeeping call.
func TestNoStoredFlagDecidesFocusAfterAnOpen(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.tree.build([]string{"a.py", "b.py"})

	// Append a document the way a test or a future caller might, with
	// no call to anything that updates focus state.
	ws.docs = append(ws.docs, testDoc(t, "raw"))
	// The stored value is still its zero value; the derivation governs
	// and must not have left the keyboard on a pane by accident.
	if got := ws.effectiveFocus(); got != focusBuffer {
		t.Errorf("effectiveFocus = %v for a raw append; the derivation should "+
			"follow w.focus once a document exists", got)
	}

	// Dropping it hands the keyboard back with nothing to update.
	ws.dropTab(0)
	if ws.effectiveFocus() != focusTree {
		t.Error("closing the last document left the keyboard on an empty pane")
	}
}

// ---------------------------------------------------------------------------
// Step 3c -- NOT a product bug
// ---------------------------------------------------------------------------

// The gate's Step 3c assertion failed; this is the trace that showed it
// is the ASSERTION that is wrong, not the save.
//
// Two separate facts, both pinned here because the gate could not tell
// them apart from the human side:
//
//  1. A take-over leaves dirty-tracking INTACT. Nothing in the lock
//     path touches d.dirty, and saveDoc's only dirty-gated skip is
//     `reason == saveAuto`, so a manual ctrl+s after a take-over always
//     sends the edited content.
//  2. A manual ctrl+s on a CLEAN document still writes -- with content
//     identical to what is on disk. os.replace then writes the same
//     bytes, so the file's hash does not change. A successful save and
//     a refused one are hash-indistinguishable, which is exactly what
//     the gate was asserting on.
func TestATakeOverLeavesDirtyTrackingIntactAndTheSaveCarriesTheEdit(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "someone-else@other-id", 3
	onDisk := d.buf.Text()

	// A lock-held file is editable in the buffer and refused on disk --
	// by design, and the reason the lock segment exists.
	d.insertRune('X')
	if !d.dirty || d.readOnly {
		t.Fatalf("a lock-held file was not editable: dirty=%v readOnly=%v", d.dirty, d.readOnly)
	}

	// The refused save leaves nothing stale behind.
	ws.saveActive()
	if n := len(writesIn(rec)); n != 0 {
		t.Fatalf("%d writes to a lock-held file", n)
	}
	if d.saving {
		t.Fatal("saving still set after a local refusal")
	}
	if !d.dirty {
		t.Fatal("the refusal cleared the dirty marker")
	}

	// f6 takes it over.
	rec.sent = nil
	ws.takeOverActive()
	replyFor(t, ws, queryRetakeLock, d.path, protocol.EvFileLockResult,
		map[string]any{"ok": true})
	if d.lock != lockHeld {
		t.Fatalf("lock = %v after a successful take-over", d.lock)
	}
	if !d.dirty {
		t.Fatal("the take-over cleared the dirty marker — THIS would be the " +
			"product bug the gate suspected")
	}

	// ...and ctrl+s carries the edit.
	rec.sent = nil
	ws.saveActive()
	w := writesIn(rec)
	if len(w) != 1 {
		t.Fatalf("%d writes after the take-over, want 1", len(w))
	}
	sent, _ := w[0].payload["content"].(string)
	if sent == onDisk {
		t.Error("the save carried the ON-DISK content, not the edit")
	}
	if !strings.Contains(sent, "X") {
		t.Errorf("the save carried %q, which does not contain the edit", sent)
	}
}

// The half the gate's assertion could not see: no edit, still a write,
// identical bytes, identical hash.
func TestAManualSaveWithNoEditWritesIdenticalBytes(t *testing.T) {
	ws, rec, d := savableWorkspace(t)
	d.lock = lockHeld
	onDisk := d.buf.Text()

	ws.saveActive()

	w := writesIn(rec)
	if len(w) != 1 {
		t.Fatalf("%d writes, want 1 — a manual save on a clean document still goes out", len(w))
	}
	if sent, _ := w[0].payload["content"].(string); sent != onDisk {
		t.Fatalf("content = %q, want the unchanged %q", sent, onDisk)
	}
	// So the gate cannot assert "the save landed" by comparing hashes:
	// this save lands and changes nothing. It has to assert on content
	// the user was told to type.
}
