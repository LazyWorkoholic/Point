package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// fakeStreamWorkspace stands in for chat.Workspace, which this package
// cannot import (chat already imports app; the reverse would cycle) — the
// same reason focus_test.go and draft_test.go each have one.
type fakeStreamWorkspace struct {
	id        WorkspaceID
	streaming bool
	pending   bool
	got       []tea.Msg
}

func (f *fakeStreamWorkspace) ID() WorkspaceID           { return f.id }
func (f *fakeStreamWorkspace) Init() tea.Cmd             { return nil }
func (f *fakeStreamWorkspace) View(layout.Layout) string { return "" }
func (f *fakeStreamWorkspace) Suspend() ([]byte, error)  { return nil, nil }
func (f *fakeStreamWorkspace) Resume([]byte) error       { return nil }
func (f *fakeStreamWorkspace) Cards() []layout.CardSlot  { return nil }
func (f *fakeStreamWorkspace) Streaming() bool           { return f.streaming }
func (f *fakeStreamWorkspace) HasPending() bool          { return f.pending }
func (f *fakeStreamWorkspace) Update(m tea.Msg) (Workspace, tea.Cmd) {
	f.got = append(f.got, m)
	return f, nil
}

var (
	_ Workspace   = (*fakeStreamWorkspace)(nil)
	_ StreamAware = (*fakeStreamWorkspace)(nil)
	_ QueueAware  = (*fakeStreamWorkspace)(nil)
)

func newStreamRoot(t *testing.T) (*Root, *fakeStreamWorkspace) {
	t.Helper()
	r := newRoot(t)
	ws := &fakeStreamWorkspace{id: r.active}
	r.SetWorkspace(r.active, ws)
	return r, ws
}

// F5 moves quit off ctrl+c because ctrl+c now cancels. Both halves are
// asserted together: a rebind that left ctrl+c on quit, or left cancel
// unbound, would each pass one of these alone.
func TestQuitIsCtrlQAndCancelIsCtrlC(t *testing.T) {
	r := newRoot(t)

	quit, ok := r.actions.ByID("app.quit")
	if !ok {
		t.Fatal("app.quit is gone")
	}
	if quit.Chord != "ctrl+q" {
		t.Fatalf("app.quit is bound to %q, wanted ctrl+q", quit.Chord)
	}

	byChord, ok := r.actions.ByChord("ctrl+c")
	if !ok {
		t.Fatal("ctrl+c is bound to nothing — F5 needs it for cancel")
	}
	if byChord.ID != "chat.cancel" {
		t.Fatalf("ctrl+c runs %s, wanted chat.cancel", byChord.ID)
	}
}

// The F5 verification step that says, in as many words: it must not quit.
func TestCtrlCDoesNotQuit(t *testing.T) {
	r, ws := newStreamRoot(t)

	m, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	root := m.(*Root)
	if root.quitting {
		t.Fatal("ctrl+c set quitting — F5 requires cancel, not quit")
	}
	if cmd == nil {
		t.Fatal("ctrl+c produced no command at all")
	}
	if _, isQuit := cmd().(tea.QuitMsg); isQuit {
		t.Fatal("ctrl+c returned tea.Quit")
	}

	// And the message reaches the workspace, which is what decides whether
	// there is anything to cancel.
	root.Update(cmd())
	found := false
	for _, msg := range ws.got {
		if _, ok := msg.(CancelStreamMsg); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("the workspace never saw CancelStreamMsg")
	}
}

// Ctrl+C with nothing streaming must be silent — not "not available right
// now", which is what a When predicate would have produced.
func TestCancelIsSilentWhenNothingStreams(t *testing.T) {
	r, ws := newStreamRoot(t)
	ws.streaming = false

	m, _ := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if s := m.(*Root).status; s != "" {
		t.Fatalf("ctrl+c said %q with nothing streaming; F5 wants nothing to happen", s)
	}
}

// esc keeps browsing until the queue is full, and then discards instead.
func TestEscBrowsesUntilTheQueueIsFull(t *testing.T) {
	r, ws := newStreamRoot(t)

	a, ok := r.actions.ByChord("esc")
	if !ok {
		t.Fatal("esc is unbound")
	}

	ws.pending = false
	if _, isBrowse := a.Run(r)().(EnterBrowseMsg); !isBrowse {
		t.Fatal("esc did not browse while the queue was empty")
	}

	ws.pending = true
	if _, isDiscard := a.Run(r)().(DiscardPendingMsg); !isDiscard {
		t.Fatal("esc did not discard the pending message at capacity")
	}
}

// F5's Done-when 7 and its idle-CPU check, as an invariant: the animation
// ticks while something streams and schedules NOTHING once it stops.
func TestHeaderAnimationRunsOnlyWhileStreaming(t *testing.T) {
	r, ws := newStreamRoot(t)

	ws.streaming = false
	if _, cmd := r.Update(statusMsg("idle")); cmd != nil {
		if _, isTick := cmd().(animTickMsg); isTick {
			t.Fatal("an animation tick was scheduled with nothing streaming")
		}
	}
	if r.anim.running {
		t.Fatal("the animation is running with nothing streaming")
	}

	ws.streaming = true
	m, cmd := r.Update(statusMsg("streaming"))
	root := m.(*Root)
	if !root.anim.running || cmd == nil {
		t.Fatal("streaming did not start the animation")
	}

	// A tick while streaming advances the frame and re-arms.
	before := root.anim.frame
	m, cmd = root.Update(animTickMsg{gen: root.anim.gen})
	root = m.(*Root)
	if root.anim.frame == before {
		t.Fatal("a tick while streaming did not advance the frame")
	}
	if cmd == nil {
		t.Fatal("a tick while streaming did not re-arm")
	}

	// The stream ends: the next tick freezes and re-arms nothing.
	ws.streaming = false
	frozen := root.anim.frame
	m, cmd = root.Update(animTickMsg{gen: root.anim.gen})
	root = m.(*Root)
	if root.anim.running {
		t.Fatal("the animation kept running after the stream ended")
	}
	if root.anim.frame != frozen {
		t.Fatal("the animation advanced after freezing")
	}
	if cmd != nil {
		if _, isTick := cmd().(animTickMsg); isTick {
			t.Fatal("the frozen animation re-armed itself — this is the idle-CPU failure")
		}
	}
}

// A stale tick from an earlier loop must not start a second one running in
// parallel with the current loop.
func TestStaleAnimationTickIsIgnored(t *testing.T) {
	r, ws := newStreamRoot(t)
	ws.streaming = true
	m, _ := r.Update(statusMsg("go"))
	root := m.(*Root)

	before := root.anim.frame
	m, cmd := root.Update(animTickMsg{gen: root.anim.gen - 1})
	root = m.(*Root)
	if root.anim.frame != before {
		t.Fatal("a stale tick advanced the frame")
	}
	if cmd != nil {
		if _, isTick := cmd().(animTickMsg); isTick {
			t.Fatal("a stale tick started a second animation loop")
		}
	}
}
