package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// Root's half of the two F13b save/autosave regressions. The Editor's
// half is in editor\hotfix_test.go.

// pausableWorkspace is a fakeWorkspace that also implements the two
// interfaces this fix added, so the ROUTING can be tested without
// standing up a real Editor (which needs a project, a tree and a wire).
//
// Embedded rather than copied: anything lifecycleRoot already does to a
// fakeWorkspace keeps working, and the only new behaviour is the three
// methods below.
type pausableWorkspace struct {
	fakeWorkspace

	wants    bool
	paused   int
	resumed  int
	envelope int
}

func (p *pausableWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	if _, ok := msg.(protocol.EnvelopeMsg); ok {
		p.envelope++
	}
	p.fakeWorkspace.Update(msg)
	return p, nil
}

func (p *pausableWorkspace) WantsBackgroundReplies() bool { return p.wants }
func (p *pausableWorkspace) PauseAutosave()               { p.paused++ }
func (p *pausableWorkspace) ResumeAutosave() tea.Cmd      { p.resumed++; return nil }

// swapIn replaces a loaded fake with a pausable one under the same id.
//
// lifecycleRoot's defs close over the fakeWorkspace they built, so the
// substitution has to happen after the workspace is loaded rather than at
// registration.
func swapIn(t *testing.T, f *lifecycleFixture, id WorkspaceID) *pausableWorkspace {
	t.Helper()
	old, loaded := f.root.workspaces[id]
	if !loaded {
		t.Fatalf("%s is not loaded — nothing to swap", id)
	}
	fw, ok := old.(*fakeWorkspace)
	if !ok {
		t.Fatalf("%s is a %T, not a fakeWorkspace", id, old)
	}
	p := &pausableWorkspace{fakeWorkspace: *fw}
	f.root.workspaces[id] = p
	return p
}

// ---------------------------------------------------------------------------
// Bug 1 -- the reply has to reach a workspace that is loaded but not active
// ---------------------------------------------------------------------------

// A loaded, INACTIVE workspace with work in flight still gets its reply.
//
// Before this, Root delivered every envelope to r.current() and nowhere
// else. An autosave write started a moment before the user pressed
// ctrl+w w had its EvProjectFileWritten land while Chat was active, and
// the Editor never saw it -- leaving doc.saving true for ever, the status
// bar stuck on "saving…", and the file refused by saveDoc's in-flight
// guard from then on.
func TestABackgroundWorkspaceWithWorkInFlightStillGetsItsReply(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.wants = true
	mustSwitch(t, f, "chat")

	if f.root.active != "chat" {
		t.Fatalf("active is %q, want chat — the premise is that editor is BACKGROUND", f.root.active)
	}
	before := ed.envelope

	f.root.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "c1", Type: protocol.EvProjectFileWritten,
	}})

	if ed.envelope != before+1 {
		t.Errorf("the backgrounded workspace received %d envelopes, want %d — "+
			"its in-flight write will never be acknowledged and the document "+
			"is wedged", ed.envelope-before, 1)
	}
}

// ...and one with nothing in flight is left alone.
//
// The predicate is the whole reason this is an interface rather than
// "deliver everything to everyone": a workspace that is not waiting on
// anything should not be woken for every envelope in the app.
func TestAnIdleBackgroundWorkspaceIsNotWoken(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.wants = false
	mustSwitch(t, f, "chat")
	before := ed.envelope

	f.root.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "c1", Type: protocol.EvProjectFileWritten,
	}})

	if ed.envelope != before {
		t.Errorf("an idle background workspace received %d envelope(s), want 0",
			ed.envelope-before)
	}
}

// The ACTIVE workspace must not be handed the same envelope twice.
//
// deliverBackgroundReply skips r.active explicitly. Without that skip the
// active Editor would process every write reply two times -- harmless for
// a dirty-flag assignment, not harmless for the status line, and exactly
// the kind of double-delivery that is invisible until something counts.
func TestTheActiveWorkspaceIsNotDeliveredTwice(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.wants = true
	before := ed.envelope

	f.root.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "c1", Type: protocol.EvProjectFileWritten,
	}})

	if got := ed.envelope - before; got != 1 {
		t.Errorf("the ACTIVE workspace received %d copies of one envelope, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Bug 2 -- the unload confirm pauses autosave for as long as it is open
// ---------------------------------------------------------------------------

// Opening UI-13's unload confirm pauses the workspace's autosave.
//
// The dialog's question is whether these buffers survive; a tick landing
// while it is open answers the question for the user and makes both
// buttons meaningless.
func TestTheUnloadConfirmPausesAutosaveWhileItIsOpen(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.dirty = 2 // the branch that opens the dialog at all

	f.root.requestUnload()

	if f.root.overlay == nil {
		t.Fatal("no confirm opened — a dirty workspace must ask before unloading")
	}
	if ed.paused != 1 {
		t.Errorf("PauseAutosave called %d times, want 1 — autosave is racing the dialog",
			ed.paused)
	}
	if ed.resumed != 0 {
		t.Errorf("ResumeAutosave was called %d times while the dialog was still open",
			ed.resumed)
	}
}

// Cancel resumes it; Unload does not, and must not.
//
// The unload takes the documents with it, so there is nothing to resume
// and a resume would be re-arming timers against a workspace that is
// being torn down.
func TestCancelResumesAutosaveAndUnloadDoesNot(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.dirty = 1
	f.root.requestUnload()

	f.root.Update(unloadCancelledMsg{id: "editor"})
	if ed.resumed != 1 {
		t.Errorf("cancel resumed autosave %d times, want 1 — a dirty file is now "+
			"left with no pending tick and no edit coming", ed.resumed)
	}

	// The confirmed path: no resume, because the workspace is going away.
	ed.resumed = 0
	f.root.requestUnload()
	f.root.Update(unloadConfirmedMsg{id: "editor", name: "Editor"})
	if ed.resumed != 0 {
		t.Errorf("the UNLOAD path resumed autosave %d times, want 0", ed.resumed)
	}
}

// A clean workspace unloads without asking, and so must not pause
// anything: there is no dialog to race.
func TestACleanUnloadNeitherAsksNorPauses(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	mustSwitch(t, f, "editor")
	ed := swapIn(t, f, "editor")
	ed.dirty = 0

	f.root.requestUnload()

	if ed.paused != 0 {
		t.Errorf("PauseAutosave called %d times for an unload that never asked", ed.paused)
	}
}

// mustSwitch switches and fails loudly if the workspace did not load.
//
// It sizes the terminal first. OpenOverlay applies a fit check and
// refuses an overlay that does not fit, and r.w/r.h are 0 until a
// WindowSizeMsg arrives -- so without this the unload confirm is
// silently never opened and a test asserting on it passes or fails for
// the wrong reason.
func mustSwitch(t *testing.T, f *lifecycleFixture, id WorkspaceID) {
	t.Helper()
	f.root.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	f.root.switchWorkspace(id)
	if f.root.active != id {
		t.Fatalf("switch to %s left active = %q", id, f.root.active)
	}
	if _, loaded := f.root.workspaces[id]; !loaded {
		t.Fatalf("%s did not load", id)
	}
}
