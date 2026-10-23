package editor

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F9d's gate at the unit level: WHICH SCOPE a write is resolved against.
//
// F9a's hazard 1 is "resolve the SESSION's scope first, guard against that,
// and commit nothing until the guard passes". F9b built the apply with no
// backend call, so the hazard was dormant; F15 added the first caller of
// WriteProjectFile from an agent-applied diff, which is what makes it live.
// The fix is in two halves and both are asserted here:
//
//   - the Editor sends `session_id` -- the session that produced the
//     proposal -- on a save of a buffer that holds an applied region, and
//     sends `project_id` on every other save exactly as F13b always did;
//   - it does NOT send one when nothing was applied, when the apply was
//     refused, or after a reload has replaced the buffer with the file.
//
// The backend half (`_handle_write_project_file` resolving that session's
// own scope via `_attachment_scope` and ignoring `project_id` outright) is
// Python and is covered by `backend\tests\manual_f9d_check.py`.

// guardWorkspace is one writable, lock-held document inside the Editor's
// own project, with a holder -- the minimum for saveDoc to actually reach
// the wire rather than refusing for a reason that has nothing to do with
// this episode.
func guardWorkspace(t *testing.T) (*Workspace, *recorder, *doc) {
	t.Helper()
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.holder = "aryan@id"
	ws.autosaveMode = config.AutosaveOff

	d := newDoc(wireFile{
		Path: "C:\\p\\sample.py", Content: cardFile, Mtime: 100, Size: 42,
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true)
	d.lock = lockHeld
	ws.docs = append(ws.docs, d)
	return ws, rec, d
}

// applyFrom builds a card against d as F15's onFillProposal would, with
// the proposal's own session on it, and drives one review-mode enter
// through the card AND the workspace.
//
// HasRecordedPair is deliberately left false so the apply is synchronous:
// the on-disk staleness guard is F15's Done-when 7b and is tested there,
// and routing every test in this file through a CheckFileStale round trip
// would be testing that guard again rather than the scope of the write.
func applyFrom(t *testing.T, ws *Workspace, d *doc, session string) *DiffCard {
	t.Helper()
	c := NewDiffCard(DiffCardOptions{
		Path:        d.path,
		Name:        d.name(),
		StartLine:   3,
		EndLine:     4,
		Replacement: cardReplacement,
		Original:    bufferLines(d.buf),
		BaseVersion: d.buf.Version(),
		SessionID:   session,
	})
	apply(t, ws, c)
	if !c.Applied() {
		t.Fatalf("the apply was refused (%q), so this test is not testing what it means to", c.refusal)
	}
	return c
}

// writeQuery is the last WriteProjectFile payload that reached the wire.
func writeQuery(t *testing.T, rec *recorder) map[string]any {
	t.Helper()
	for i := len(rec.sent) - 1; i >= 0; i-- {
		if rec.sent[i].cmd == protocol.CmdWriteProjectFile {
			return rec.sent[i].payload
		}
	}
	t.Fatalf("no WriteProjectFile was sent (sent: %v)", rec.sent)
	return nil
}

// ---------------------------------------------------------------------------
// The fix itself
// ---------------------------------------------------------------------------

// A save after an agent apply is scoped by the session that proposed the
// region, and carries no project_id at all.
//
// The second half matters as much as the first. The handler ignores
// `project_id` when `session_id` is present, so sending both would put a
// dead key on the wire -- which is how a later reader comes to believe the
// path-resolved project is a fallback. It is not: a session with no project
// is refused.
func TestASaveAfterAnAgentApplyIsScopedToTheProposalsSession(t *testing.T) {
	ws, rec, d := guardWorkspace(t)
	applyFrom(t, ws, d, "sess-editor")

	ws.saveActive()
	p := writeQuery(t, rec)

	if got := p["session_id"]; got != "sess-editor" {
		t.Errorf("session_id = %v, want the session that produced the proposal — "+
			"without it the write resolves whatever .point\\ sits above the file", got)
	}
	if _, ok := p["project_id"]; ok {
		t.Errorf("project_id = %v was sent beside session_id; the handler ignores it "+
			"outright and a dead key reads as a fallback that does not exist", p["project_id"])
	}
}

// An ordinary save -- no agent anywhere near the buffer -- is byte-for-byte
// what F13b sent. F9d narrows the hazard it is for; it does not re-scope
// every write in the workspace.
func TestAnOrdinarySaveIsUnchangedByF9d(t *testing.T) {
	ws, rec, d := guardWorkspace(t)
	d.spliceAs(editOther, Pos{}, Pos{}, "x")

	ws.saveActive()
	p := writeQuery(t, rec)

	if _, ok := p["session_id"]; ok {
		t.Errorf("session_id = %v on a save of a buffer no agent touched", p["session_id"])
	}
	if got := p["project_id"]; got != "p1" {
		t.Errorf("project_id = %v, want the Editor's own project (F13b's rule, unchanged)", got)
	}
}

// A file outside the Editor's project still resolves by the backend's
// walk-up when no agent applied anything -- the case that would REGRESS if
// F9d had simply sent `w.sessionID` on every save, because that session is
// bound to the Editor's own project and a file outside it would then be
// refused.
func TestAnOutsideFileStillWalksUpWhenNoAgentApplied(t *testing.T) {
	ws, rec, _ := guardWorkspace(t)
	outside := newDoc(wireFile{
		Path: "D:\\elsewhere\\other.py", Content: "a\nb", Mtime: 100, Size: 4,
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true)
	outside.lock = lockHeld
	ws.docs = append(ws.docs, outside)
	ws.active = len(ws.docs) - 1
	outside.spliceAs(editOther, Pos{}, Pos{}, "x")

	ws.saveActive()
	p := writeQuery(t, rec)

	if _, ok := p["session_id"]; ok {
		t.Errorf("session_id = %v on a file no agent touched", p["session_id"])
	}
	if _, ok := p["project_id"]; ok {
		t.Errorf("project_id = %v on a file outside the Editor's project", p["project_id"])
	}
}

// A refused apply records nothing. The origin is set where the splice is
// PROVEN, not where it was attempted -- F9a's hazard 3 applied to this
// field: a document that claims an agent origin it never received would be
// scoped by a session whose proposal is not in it.
func TestARefusedApplyRecordsNoSession(t *testing.T) {
	ws, rec, d := guardWorkspace(t)
	c := NewDiffCard(DiffCardOptions{
		Path:        d.path,
		Name:        d.name(),
		StartLine:   3,
		EndLine:     4,
		Replacement: cardReplacement,
		Original:    bufferLines(d.buf),
		BaseVersion: d.buf.Version(),
		SessionID:   "sess-editor",
	})
	// The buffer moves after the proposal was built, which is F9b's
	// baseVersion guard: the apply is refused before any splice.
	d.spliceAs(editOther, Pos{}, Pos{}, "x")
	apply(t, ws, c)
	if c.Applied() {
		t.Fatal("the apply was not refused, so this test proves nothing")
	}
	if d.agentSession != "" {
		t.Fatalf("agentSession = %q after a REFUSED apply", d.agentSession)
	}

	ws.saveActive()
	if p := writeQuery(t, rec); p["session_id"] != nil {
		t.Errorf("session_id = %v after a refused apply", p["session_id"])
	}
}

// A reload drops it. The buffer is the file again, so there is no applied
// region left in it for a session to scope.
func TestAReloadDropsTheApplyingSession(t *testing.T) {
	ws, rec, d := guardWorkspace(t)
	applyFrom(t, ws, d, "sess-editor")

	ws.applyReload(d, wireFile{
		Path: d.path, Content: cardFile, Mtime: 200, Size: 42,
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	})
	if d.agentSession != "" {
		t.Fatalf("agentSession = %q after a reload replaced the buffer", d.agentSession)
	}

	d.spliceAs(editOther, Pos{}, Pos{}, "x")
	ws.saveActive()
	p := writeQuery(t, rec)
	if _, ok := p["session_id"]; ok {
		t.Errorf("session_id = %v after a reload", p["session_id"])
	}
	if got := p["project_id"]; got != "p1" {
		t.Errorf("project_id = %v, want the ordinary rule back", got)
	}
}

// Autosave is scoped the same way. It is the SAME saveDoc, which is why it
// is asserted rather than assumed: a guard that only the ctrl+s path
// carried would leave the timer writing under the wrong scope, and the
// timer is the path nobody is watching when it fires.
func TestAutosaveCarriesTheSameScope(t *testing.T) {
	ws, rec, d := guardWorkspace(t)
	applyFrom(t, ws, d, "sess-editor")

	ws.saveDoc(d, saveAuto, false)
	if got := writeQuery(t, rec)["session_id"]; got != "sess-editor" {
		t.Errorf("session_id = %v on the autosave path", got)
	}
}

// A refused write is READABLE.
//
// This episode's refusals arrive as Error envelopes -- a session with no
// project, and a path outside the session's scope -- and the backend's own
// key for the text is `error` (runtime.py's `_emit`: `{"error", "code",
// "offer"}`), which `onError` did not decode at all. Every Error envelope
// in this workspace therefore rendered as "the backend refused the
// request". A pre-existing F13b defect; fixed under F9d because a
// wrong-scope save that refuses unreadably is a guard the user cannot act
// on, which is F9b's own rule about a card that looks inert.
//
// Both keys are asserted: the tests written before this one all send
// `message`, and the fallback is what keeps them meaningful rather than
// merely passing.
func TestARefusedWriteSaysWhatTheBackendSaid(t *testing.T) {
	for _, key := range []string{"error", "message"} {
		ws, _, d := guardWorkspace(t)
		applyFrom(t, ws, d, "sess-editor")
		ws.saveActive()

		cmd := replyFor(t, ws, queryWriteFile, d.path, protocol.EvError,
			map[string]any{"code": "project_not_found", key: "Session sess-editor has no project"})

		if said := saidBy(cmd); !strings.Contains(said, "has no project") {
			t.Errorf("%s: said %q, want the backend's own reason", key, said)
		}
		if d.saving {
			t.Errorf("%s: saving still set after an Error", key)
		}
	}
}

// The log line says which scope was used, in words. The gate greps it:
// "was there a write" was never the whole question, and after F9d "under
// which scope" is the rest of it.
func TestTheScopeLabelNamesAllThreeStates(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"session", map[string]any{"session_id": "s1"}, "session s1 (agent-applied region)"},
		{"project", map[string]any{"project_id": "p1"}, "project p1"},
		{"neither", map[string]any{}, "path walk-up"},
	}
	for _, c := range cases {
		if got := scopeLabel(c.payload); got != c.want {
			t.Errorf("%s: scopeLabel = %q, want %q", c.name, got, c.want)
		}
	}
}
