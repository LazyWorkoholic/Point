package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// The size key's escape-hatch behaviour, which is a separate bug from the
// auto-expand trigger itself: whatever got the pane into a large state,
// the key that shrinks it has to work.

// A pane that auto-expanded over the code must shrink on the size key. It
// did not: autoExpanded overrode the drawn mode in effectiveTerminalMode,
// so the one press a user makes in exactly that state changed nothing.
func TestTheSizeKeyEscapesAnAutoExpandedPane(t *testing.T) {
	w := termWorkspace(t)
	w.docs = append(w.docs, testDoc(t, "x = 1\n"))
	w.term.mode = layout.TermSmall
	w.focus = focusBuffer

	// Output arrives while the code pane has focus.
	w.onTerminalEvent(termEvent{gen: w.term.gen, kind: termChunk, text: "FAILED\r\n"})
	if got := w.effectiveTerminalMode(); got != layout.TermOverlay {
		t.Fatalf("no auto-expand to escape from: %q", got)
	}

	// The size key, pressed where a user would actually press it.
	w.handleKey(tea.KeyMsg{Type: tea.KeyF8})
	if w.term.autoExpanded {
		t.Error("the size key did not clear the automatic expansion")
	}
	if got := w.effectiveTerminalMode(); got != layout.TermSmall {
		t.Errorf("the pane is still %q after the size key -- no way back to the code", got)
	}
	// Focus was NOT stolen by the escape.
	if w.focus != focusBuffer {
		t.Errorf("focus moved to %v", w.focus)
	}
}

// The size key always reports something, because the drawn size does not
// always change on the press -- with the code pane focused every mode
// draws as small, so an unreported press looks inert.
func TestTheSizeKeyAlwaysSaysWhatItDid(t *testing.T) {
	w := termWorkspace(t)
	w.focus = focusTerminal
	if cmd := w.cycleTerminalMode(); cmd == nil {
		t.Fatal("the size key reported nothing")
	}
	w.docs = append(w.docs, testDoc(t, "x = 1\n"))
	w.focus = focusBuffer
	if cmd := w.cycleTerminalMode(); cmd == nil {
		t.Fatal("the size key reported nothing with the code pane focused")
	}
}

// The size key is reachable from the terminal pane too, not just from the
// code pane -- it is in handleKey's shared block, above the focus split.
func TestTheSizeKeyWorksFromInsideThePane(t *testing.T) {
	w := termWorkspace(t)
	w.focus = focusTerminal
	before := w.term.mode
	w.handleKey(tea.KeyMsg{Type: tea.KeyF8})
	if w.term.mode == before {
		t.Errorf("mode stayed %q when the pane had focus", before)
	}
	// And it did not land in the input line as text.
	if len(w.term.input) != 0 {
		t.Errorf("the key leaked into the input: %q", string(w.term.input))
	}
}
