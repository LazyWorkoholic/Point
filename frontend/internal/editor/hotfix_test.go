package editor

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The two regressions F13b's save/autosave shipped with, found by manual
// re-testing after B33 and F16 landed.
//
// Both are races against something the user does with the keyboard -- a
// workspace switch, and a dialog -- which is why neither showed up in
// gate_f13b's own run: the gate drives one thing at a time and waits.

// ---------------------------------------------------------------------------
// Bug 1 -- a write whose reply arrives while the Editor is backgrounded
// ---------------------------------------------------------------------------

// A write reply has to be ACCEPTED whenever it arrives, because the
// document is wedged until it does.
//
// The live bug: autosave starts a write, the user switches to Chat inside
// the one-second debounce, and Root delivers EvProjectFileWritten to
// r.current() -- which is now Chat. The editor never sees it, doc.saving
// stays true for ever, and saveDoc's "a write is already in flight" guard
// then refuses every later save. The status bar reads "saving…" and the
// dirty marker never clears, on a file that cannot be saved again by any
// means including ctrl+s.
//
// This test is the editor half: given the reply, the document recovers
// completely. TestAnInFlightWriteKeepsTheEditorListening is the half that
// proves Root will still deliver it.
func TestAWriteReplyUnwedgesTheDocumentWhereverItArrives(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	runTick(t, ws, ws.armAutosave(a))

	writes := writesIn(rec)
	if len(writes) != 1 {
		t.Fatalf("expected one write in flight, got %d", len(writes))
	}
	if !a.saving {
		t.Fatal("doc.saving was not set — the rest of this test proves nothing")
	}
	if !a.dirty {
		t.Fatal("the document should still be dirty until the reply lands")
	}

	// The reply, arriving after a workspace switch would have happened.
	deliverWritten(t, ws, a.path, 200)

	if a.saving {
		t.Error("doc.saving is still set after the reply — the document is wedged, " +
			"and saveDoc will refuse every later write")
	}
	if a.dirty {
		t.Error("the dirty marker never cleared")
	}
	if a.mtime != 200 {
		t.Errorf("mtime is %v, want the 200 the reply reported — a stale mtime "+
			"makes the NEXT save fail the expected_mtime precondition", a.mtime)
	}

	// And the proof that it is really unwedged: another edit saves.
	a.insertRune('y')
	runTick(t, ws, ws.armAutosave(a))
	if n := len(writesIn(rec)); n != 2 {
		t.Errorf("a second autosave produced %d writes in total, want 2 — "+
			"the in-flight guard is still latched", n)
	}
}

// WantsBackgroundReplies is what makes Root keep delivering, and it has
// to be true for exactly as long as a write is outstanding.
//
// Asserted on both edges. Always-false is the bug this episode is fixing;
// always-true would quietly hand every envelope in the app to a workspace
// nobody is looking at, which is the cost this narrow predicate exists to
// avoid.
func TestAnInFlightWriteKeepsTheEditorListening(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)

	if ws.WantsBackgroundReplies() {
		t.Error("an idle editor should not be asking for background replies")
	}

	a.insertRune('x')
	runTick(t, ws, ws.armAutosave(a))
	if !ws.WantsBackgroundReplies() {
		t.Fatal("a write is in flight and the editor is NOT asking for its reply — " +
			"this is the whole of the wedged-save bug")
	}

	deliverWritten(t, ws, a.path, 200)
	if ws.WantsBackgroundReplies() {
		t.Error("nothing is in flight any more, but the editor is still asking")
	}
}

// deliverWritten feeds one EvProjectFileWritten in through Update.
//
// Through Update, not handleEnvelope directly: Update is the entry point
// Root calls, and the whole fix is about Root calling it on a workspace
// that is not active. Correlation goes through w.pending the same way
// workspace_test.go's replyFor does -- the recorder replaces sendFn, so
// doSend (which is what normally fills pending) never runs.
func deliverWritten(t *testing.T, ws *Workspace, path string, mtime float64) {
	t.Helper()
	id := "corr-write-" + path
	ws.pending[id] = pendingReq{kind: queryWriteFile, path: path}
	payload, err := json.Marshal(map[string]any{
		"ok": true, "path": path, "mtime": mtime, "size": 4,
		"line_ending": lineEndingLF, "bom": false, "trailing_newline": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: id, Type: protocol.EvProjectFileWritten, Payload: payload,
	}})
}

// ---------------------------------------------------------------------------
// Bug 2 -- autosave racing the unload confirm
// ---------------------------------------------------------------------------

// Nothing may be written while UI-13's unload dialog is on screen.
//
// The dialog asks whether these buffers survive. A tick armed by the
// keystroke that made the file dirty lands a second later, while the user
// is still reading it -- so without a pause the edits are already on disk
// before either button is pressed, and both buttons become meaningless.
// That is gate_f13b step 5's finding; the editor's own close confirm
// fixed it for itself and this dialog, which Root opens, never had it.
func TestNoWriteHappensWhileTheUnloadConfirmIsOpen(t *testing.T) {
	ws, rec, a, b := autosaveWorkspace(t)

	// Both files dirty, both with a tick already in flight -- the
	// dangerous state, and the reason the pause cannot be a flag alone.
	a.insertRune('x')
	aTick := ws.armAutosave(a)
	b.insertRune('y')
	bTick := ws.armAutosave(b)

	ws.PauseAutosave()

	// The ticks that were ALREADY scheduled when the dialog opened.
	runTick(t, ws, aTick)
	runTick(t, ws, bTick)
	if n := len(writesIn(rec)); n != 0 {
		t.Fatalf("%d write(s) landed while the unload confirm was open — "+
			"Unload would discard a buffer that is already on disk", n)
	}

	// And an edit made while it is open schedules nothing new.
	a.insertRune('z')
	if cmd := ws.armAutosave(a); cmd != nil {
		t.Error("an edit scheduled a new autosave while the dialog was open")
	}
	if n := len(writesIn(rec)); n != 0 {
		t.Errorf("%d write(s) after an edit during the dialog, want 0", n)
	}
}

// Cancel puts it back, and re-arms rather than waiting for a keystroke.
//
// The re-arm is the half that is easy to leave out and quiet when it is
// missing: without it a cancelled dialog leaves a dirty file with no
// pending tick and no edit coming, so autosave looks dead until the user
// types one more character.
func TestCancellingTheUnloadConfirmResumesAndReArmsAutosave(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	ws.armAutosave(a)
	ws.PauseAutosave()

	cmd := ws.ResumeAutosave()
	if cmd == nil {
		t.Fatal("resume re-armed nothing for a file that is still dirty — " +
			"autosave is dead until the next keystroke")
	}
	runTick(t, ws, cmd)

	writes := writesIn(rec)
	if len(writes) != 1 {
		t.Fatalf("after cancel, %d write(s), want 1", len(writes))
	}
	if writes[0].payload["path"] != a.path {
		t.Errorf("the re-armed write was for %v, not the dirty file", writes[0].payload["path"])
	}
}

// Resuming must not re-arm a file that is blocked for a reason of its
// own, and must not clear that block.
//
// doc.autosaveBlocked is the document's own refusal -- read-only, or held
// by another holder -- and the pause is a workspace-wide, temporary
// thing. Folding the two together is the obvious simplification and it
// would silently re-enable autosave on a file the backend has already
// refused, once per cancelled dialog.
func TestResumeDoesNotUnblockADocumentStoppedForItsOwnReason(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	a.lock = lockHeldByOther
	a.lockedBy = "someone-else@other-id"
	ws.armAutosave(a) // latches autosaveBlocked via canSave()
	if !a.autosaveBlocked {
		t.Fatal("the lock-held file was not blocked — the test's premise is wrong")
	}

	ws.PauseAutosave()
	runTick2(t, ws, ws.ResumeAutosave())

	if !a.autosaveBlocked {
		t.Error("resume cleared a block the document owns — autosave will now " +
			"hammer a file another holder has")
	}
	if n := len(writesIn(rec)); n != 0 {
		t.Errorf("%d write(s) to a file held by another holder, want 0", n)
	}
}

// runTick2 is runTick without the "something must have been scheduled"
// assertion: these cases are about nothing being scheduled.
func runTick2(t *testing.T, ws *Workspace, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		ws.Update(msg)
	}
}
