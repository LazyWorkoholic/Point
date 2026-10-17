package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// `f7` closes the active tab. Shipped in 6e28f6b and recorded in
// F16-polish.md's "Editor keymap documentation and a one-key close".
//
// These three tests were originally written in `scroll_test.go`
// alongside the `clampTop` tests, because both shipped in the same
// commit. That fix was later reverted and the file deleted, which took
// f7's only coverage with it while f7 itself stayed in the code. They
// live here now so the binding's tests share the life of the binding,
// not the life of an unrelated fix.

// It routes through the SAME closeTab the tab strip's x uses, so a
// dirty file still gets F13b's C3 confirm rather than being discarded.
func TestF7ClosesACleanTabAndConfirmsADirtyOne(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"), testDoc(t, "b"))
	ws.active = 1

	// Clean: closes outright.
	cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyF7})
	if len(ws.docs) != 1 {
		t.Fatalf("%d documents after f7 on a clean tab, want 1", len(ws.docs))
	}
	if said := saidBy(cmd); !strings.Contains(said, "closed") {
		t.Errorf("said %q", said)
	}

	// Dirty: confirms, and closes nothing yet (F13's Done-when 9 / C3).
	ws.docs[0].insertRune('x')
	cmd = ws.handleKey(tea.KeyMsg{Type: tea.KeyF7})
	if len(ws.docs) != 1 {
		t.Fatal("f7 discarded a dirty document without confirming")
	}
	if !openedConfirm(t, cmd) {
		t.Error("f7 on a dirty tab did not open the confirm")
	}
}

// With nothing open it is a no-op rather than a panic.
func TestF7WithNothingOpenDoesNothing(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	if cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyF7}); cmd != nil {
		t.Error("f7 produced a command with nothing open")
	}
}

// It is reachable: not a chord the registry claims first, and not
// claimed by CapturesKey.
//
// This is the assertion that matters most for this binding, because the
// two obvious choices were both unreachable: ctrl+w is the leader, and
// ctrl+shift+w arrives as ctrl+w on Windows (same control character),
// which is why the lock take-over is on f6 and this is on f7.
func TestF7IsReachableAndNotAChord(t *testing.T) {
	chords := []string{"ctrl+c", "ctrl+e", "ctrl+l", "ctrl+p", "ctrl+q",
		"ctrl+r", "esc", "f1", "tab"}
	key := tea.KeyMsg{Type: tea.KeyF7}.String()
	for _, c := range chords {
		if key == c {
			t.Fatalf("close-tab is bound to %q, which dispatchKey swallows first", key)
		}
	}
	ws, _ := newWorkspaceWithRecorder()
	if ws.CapturesKey(key) {
		t.Errorf("%q is claimed by CapturesKey and does not need to be", key)
	}
}
