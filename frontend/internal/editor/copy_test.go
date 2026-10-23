package editor

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// Copy in the code file box.
//
// F15's gate: "trying to copy selected text still shows 'cancel stream is
// not available now' instead of copying". Two things were true -- ctrl+c
// was chat.cancel's chord with a Chat-only When, so the Editor never saw
// it, AND this workspace had no copy implementation at all (F13a built
// selection and nothing that could copy it).
//
// These assert the CONTENT reaching the terminal, not merely that the
// cancel message stopped appearing: the OSC 52 sequence carries the text
// base64-encoded, so the payload is checkable.

// copyWorkspace is a workspace whose clipboard writes the test can read.
func copyWorkspace(t *testing.T, content string) (*Workspace, *bytes.Buffer) {
	t.Helper()
	ws, _ := invokeWorkspace(t, content)
	var clip bytes.Buffer
	ws.clipOut = &clip
	return ws, &clip
}

// copied returns the text the OSC 52 sequence carries, or "" if none was
// written. The sequence is ESC ] 52 ; c ; <base64> BEL (or ST).
func copied(t *testing.T, clip *bytes.Buffer) string {
	t.Helper()
	raw := clip.String()
	if raw == "" {
		return ""
	}
	i := strings.Index(raw, ";c;")
	if i < 0 {
		t.Fatalf("no OSC 52 clipboard sequence in %q", raw)
	}
	payload := raw[i+3:]
	for _, end := range []string{"\a", "\x1b\\"} {
		if j := strings.Index(payload, end); j >= 0 {
			payload = payload[:j]
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("the clipboard payload %q is not base64: %v", payload, err)
	}
	return string(decoded)
}

// Plain copy, nothing streaming: the thing that was broken.
func TestCtrlCCopiesTheSelectionWithNothingStreaming(t *testing.T) {
	const file = "import os\n\ndef parse(path):\n    return path"
	ws, clip := copyWorkspace(t, file)
	d := ws.docs[0]

	// Select "def parse(path):" -- the whole of line 3.
	d.anchor = Pos{Line: 2, Col: 0}
	d.cursor = Pos{Line: 2, Col: len("def parse(path):")}
	d.selecting = true

	if ws.liveInvocation() {
		t.Fatal("something is streaming; this test is about the idle case")
	}
	cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})

	if got := copied(t, clip); got != "def parse(path):" {
		t.Errorf("clipboard = %q, want the selected line", got)
	}
	// And the user is told, rather than left guessing.
	if cmd == nil {
		t.Fatal("no status line was produced")
	}
	if msg, ok := cmd().(app.WorkspaceStatusMsg); !ok || !strings.Contains(msg.Text, "copied") {
		t.Errorf("status = %#v, want it to say something was copied", cmd())
	}
}

// A multi-line selection copies the text BETWEEN the two positions, LF
// joined -- what Buffer.Slice returns and what a paste elsewhere needs.
func TestCopyingAcrossLinesKeepsTheLineBreaks(t *testing.T) {
	const file = "alpha\nbeta\ngamma"
	ws, clip := copyWorkspace(t, file)
	d := ws.docs[0]
	d.anchor = Pos{Line: 0, Col: 0}
	d.cursor = Pos{Line: 2, Col: 0}
	d.selecting = true

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := copied(t, clip); got != "alpha\nbeta\n" {
		t.Errorf("clipboard = %q, want the two full lines", got)
	}
}

// Nothing selected copies NOTHING and says so. Deliberately not "copy the
// whole line", which would let an accidental ctrl+c replace the clipboard
// with a line the user never chose.
func TestCtrlCWithNoSelectionCopiesNothingAndSaysSo(t *testing.T) {
	ws, clip := copyWorkspace(t, "alpha\nbeta")
	cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})

	if got := clip.String(); got != "" {
		t.Errorf("something was written to the clipboard: %q", got)
	}
	if cmd == nil {
		t.Fatal("no status line")
	}
	if msg, ok := cmd().(app.WorkspaceStatusMsg); !ok ||
		!strings.Contains(msg.Text, "nothing selected") {
		t.Errorf("status = %#v, want it to say nothing was selected", cmd())
	}
}

// ctrl+c COPIES even mid-stream, and does NOT cancel.
//
// This is the design decision copy.go records: a ctrl+c whose meaning
// depended on whether something was streaming would fail silently in the
// dangerous direction -- the user believes they copied, and the next paste
// puts something stale somewhere else.
func TestCtrlCCopiesRatherThanCancellingMidStream(t *testing.T) {
	ws, clip := copyWorkspace(t, "alpha\nbeta")
	ws.BeginFill()
	ws.prompt.runes = []rune("do it")
	ws.submitPrompt()
	ws.inv.taskID = "task-1"
	stream(ws, `{"replacement": "alpha-ish`)
	if !ws.taskRunning() {
		t.Fatal("the task is not running; this test is about the live case")
	}

	d := ws.docs[0]
	d.anchor, d.cursor, d.selecting = Pos{Line: 0, Col: 0}, Pos{Line: 0, Col: 5}, true
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})

	if got := copied(t, clip); got != "alpha" {
		t.Errorf("clipboard = %q, want the selection -- ctrl+c must copy, not cancel", got)
	}
	if ws.inv.cancelled {
		t.Error("ctrl+c cancelled the stream; esc is the only cancel key here")
	}
	if ws.ghost == nil {
		t.Error("the ghost was discarded by a copy")
	}

	// esc, by contrast, still cancels.
	ws.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !ws.inv.cancelled {
		t.Error("esc did not cancel")
	}
}

// And neither the prompt nor the panel takes ctrl+c as a cancel any more.
func TestCtrlCDoesNotCancelTheModalSurfaces(t *testing.T) {
	ws, clip := copyWorkspace(t, "alpha\nbeta")
	d := ws.docs[0]
	d.anchor, d.cursor, d.selecting = Pos{Line: 0, Col: 0}, Pos{Line: 0, Col: 5}, true

	ws.BeginFill()
	ws.prompt.runes = []rune("kept")
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if ws.prompt == nil {
		t.Error("ctrl+c closed the fill prompt")
	} else if ws.prompt.text() != "kept" {
		t.Errorf("the prompt's text changed to %q", ws.prompt.text())
	}
	// And it COPIED. Asserted, not assumed: handleKey's ctrl+c check used
	// to sit BELOW the prompt/panel dispatch, so ctrl+c with either open
	// fell into the prompt's own switch, matched nothing, and did nothing
	// at all -- silent, which is the failure mode copy.go exists to
	// avoid. Three comments (here, prompt.go and panelKey) already
	// claimed handleKey took it first; the code did not. Without this
	// assertion that ordering is not load-bearing and the bug reads as
	// passing.
	if got := copied(t, clip); got != "alpha" {
		t.Errorf("clipboard = %q, want the buffer selection copied with the prompt open", got)
	}
	clip.Reset()
	ws.prompt = nil

	ws.inv = nil
	ws.BeginExplain()
	if ws.panel == nil {
		t.Fatal("no panel")
	}
	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if ws.panel == nil {
		t.Error("ctrl+c closed the explain panel; esc is what dismisses it")
	}
	// Same for the panel. Note this copies the CODE selection, not the
	// explanation -- the panel has no selection of its own, and inventing
	// one so ctrl+c could mean two things is the design copy.go rejects.
	if got := copied(t, clip); got != "alpha" {
		t.Errorf("clipboard = %q, want the buffer selection copied with the panel open", got)
	}
}

// ---------------------------------------------------------------------------
// Through the REAL Root
// ---------------------------------------------------------------------------

// The gate's actual report, reproduced through Root.Update.
//
// Every test above calls handleKey directly, which is exactly the gap that
// let this ship: the key never GOT to handleKey. ctrl+c is chat.cancel's
// chord, dispatchKey consults the chord registry before the workspace, and
// an action whose `When` is false is SWALLOWED with a status line rather
// than passed on -- so the Editor saw nothing and the user read "Cancel
// Stream is not available right now" while their selection sat uncopied.
//
// CapturesKey is what gets the key back, and only a dispatch through Root
// exercises that. Drop ctrl+c from CapturesKey and this is the one test in
// the file that goes red.
func TestCtrlCReachesTheEditorThroughRootRatherThanBeingSwallowed(t *testing.T) {
	rig := newRootRig(t, "import os\n\ndef parse(path):\n    return path", time.Millisecond)
	var clip bytes.Buffer
	rig.ed.clipOut = &clip
	// The rig shortens StatusLinger to a millisecond so its other tests
	// do not wait on it; this one READS the status line, so it needs the
	// message to still be there when View is called.
	rig.root.StatusLinger = time.Minute

	d := rig.ed.docs[0]
	d.anchor = Pos{Line: 2, Col: 0}
	d.cursor = Pos{Line: 2, Col: len("def parse(path):")}
	d.selecting = true

	rig.send(tea.KeyMsg{Type: tea.KeyCtrlC})

	if got := copied(t, &clip); got != "def parse(path):" {
		t.Errorf("clipboard = %q, want the selection", got)
	}
	if frame := stripANSI(rig.root.View()); strings.Contains(frame, "not available right now") {
		t.Error("ctrl+c was swallowed by a Chat-only chord instead of reaching the Editor")
	}
}
