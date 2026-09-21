package app

import (
	"encoding/json"
	"log/slog"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// handleTaskListEnvelope has two jobs: claim Root's own ListTasks reply by
// corr_id, and treat any TaskStarted/terminal event as a reason the global
// snapshot might be stale. Neither should need a live client to verify —
// refreshTasks itself no-ops without one (r.client == nil), which is exactly
// what lets this test assert "it tried" without one.
func TestHandleTaskListEnvelopeClaimsItsOwnReply(t *testing.T) {
	r := &Root{tasksReqID: "req-1", log: discardLogger()}

	payload, err := json.Marshal(map[string]any{
		"tasks": []TaskRow{
			{ID: "t1", Kind: "research", State: TaskStateRunning},
			{ID: "t2", Kind: "research", State: TaskStateQueued},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	r.handleTaskListEnvelope(protocol.Envelope{
		Type:    protocol.EvTaskList,
		CorrID:  "req-1",
		Payload: payload,
	})

	if r.tasksReqID != "" {
		t.Errorf("tasksReqID = %q, want cleared once its own reply arrives", r.tasksReqID)
	}
	if len(r.tasks) != 2 || r.tasks[0].ID != "t1" || r.tasks[1].State != TaskStateQueued {
		t.Errorf("tasks = %+v, want the two parsed rows", r.tasks)
	}
}

// An envelope that is not this root's own reply — a stray corr_id, or none —
// must be left alone: only a lifecycle event (TaskStarted/terminal) may
// trigger a fresh request, and a mismatched corr_id must never be mistaken
// for Root's own pending query.
func TestHandleTaskListEnvelopeIgnoresUnrelatedReplies(t *testing.T) {
	r := &Root{tasksReqID: "req-1"}

	r.handleTaskListEnvelope(protocol.Envelope{
		Type:   protocol.EvSessionList,
		CorrID: "some-other-request",
	})

	if r.tasksReqID != "req-1" {
		t.Errorf("tasksReqID = %q, want untouched by an unrelated reply", r.tasksReqID)
	}
	if r.tasks != nil {
		t.Errorf("tasks = %+v, want untouched", r.tasks)
	}
}

// This is the bug report from the field: three @research tasks fired
// together, and the Task card showed one entry at a time, swapping identity
// as each finished, instead of all three at once. Root cause: refreshTasks
// used to DROP a trigger outright whenever a request was already in flight,
// with nothing remembering that the eventual reply was already stale by the
// time it landed. tasksDirty is the fix — verify it actually fires the
// follow-up request once the in-flight one resolves, rather than silently
// losing the second and third TaskStarted that arrived while the first
// request was still in the air.
func TestRefreshTasksCoalescesWhileBusy(t *testing.T) {
	var sent []string
	r := &Root{
		client: protocol.NewClient(protocol.Options{}), // non-nil is all refreshTasks checks
		log:    discardLogger(),
	}
	r.ListTasksFn = func(env protocol.Envelope) bool {
		sent = append(sent, env.ID)
		return true
	}
	r.layout.ShowRightPane = true

	r.refreshTasks() // first trigger: sends a request
	firstReqID := r.tasksReqID
	if firstReqID == "" || len(sent) != 1 {
		t.Fatalf("first refreshTasks() did not send a request (tasksReqID=%q, sent=%v)", firstReqID, sent)
	}

	// Two more lifecycle events land while the first is still in flight —
	// exactly the burst three simultaneous @research sends produce.
	r.refreshTasks()
	r.refreshTasks()
	if !r.tasksDirty {
		t.Fatal("tasksDirty = false, want true: a trigger arrived while a request was already in flight")
	}
	if r.tasksReqID != firstReqID {
		t.Fatalf("tasksReqID changed to %q while still waiting on %q — a second request should never be sent while one is outstanding", r.tasksReqID, firstReqID)
	}

	// The first request's reply arrives. Because tasksDirty was set, this
	// must immediately fire a follow-up rather than going quiet — that
	// follow-up is what lets the snapshot catch up to state that changed
	// during the first round trip.
	r.handleTaskListEnvelope(protocol.Envelope{
		Type:    protocol.EvTaskList,
		CorrID:  firstReqID,
		Payload: []byte(`{"tasks":[]}`),
	})

	if r.tasksDirty {
		t.Error("tasksDirty = true after the reply was handled, want cleared")
	}
	if r.tasksReqID == "" || r.tasksReqID == firstReqID {
		t.Errorf("tasksReqID = %q, want a NEW request sent to catch up on what was missed", r.tasksReqID)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fakeRightPaneFallback stands in for chat.rightPaneOverlay, which this
// package cannot import (chat already imports app) — the same reason every
// other fake workspace/overlay in this package's tests exists.
type fakeRightPaneFallback struct{}

func (fakeRightPaneFallback) MinSize() (int, int)                 { return 36, 20 }
func (fakeRightPaneFallback) Title() string                       { return "Right Pane" }
func (fakeRightPaneFallback) View(theme.Theme, int, int) string   { return "" }
func (f fakeRightPaneFallback) Update(tea.Msg) (Overlay, tea.Cmd) { return f, nil }
func (fakeRightPaneFallback) IsRightPaneFallback() bool           { return true }

var _ RightPaneFallback = fakeRightPaneFallback{}

// This is the "new bug" from the field: the fallback overlay's Task section
// never updated live, only showing whatever r.tasks held at the moment it
// opened. Root cause: refreshTasks's visibility gate checked ONLY
// r.layout.ShowRightPane, which is unconditionally false while this overlay
// is the thing on screen (that is precisely WHY the overlay exists — the
// real pane does not fit) — so every TaskStarted/terminal event while the
// overlay was open triggered a refresh that refreshTasks silently refused
// to send.
func TestRightPaneVisibleIncludesFallbackOverlay(t *testing.T) {
	r := &Root{}
	r.layout.ShowRightPane = false
	if r.rightPaneVisible() {
		t.Fatal("rightPaneVisible() = true with nothing open, want false")
	}

	r.overlay = fakeRightPaneFallback{}
	if !r.rightPaneVisible() {
		t.Error("rightPaneVisible() = false with the fallback overlay open, want true — its Task section needs the same live data the real pane does")
	}
}

// Widening the terminal back past the breakpoint must retire the fallback
// overlay by itself — before this fix, only closeOverlayIfTooSmall existed,
// and its condition (the terminal shrinking BELOW the overlay's own
// MinSize) is the opposite one, so nothing ever closed an overlay on a
// resize that GREW past the point where the real pane fits again.
func TestCloseRightPaneFallbackIfObsolete(t *testing.T) {
	r := newRoot(t) // recompute() needs a properly initialised Root, not a bare literal
	r.overlay = fakeRightPaneFallback{}
	r.w = 80 // below layout.MediumWidth: the overlay is still doing its job
	r.closeRightPaneFallbackIfObsolete()
	if r.overlay == nil {
		t.Fatal("overlay closed while still narrower than the breakpoint")
	}

	r.w = 120 // past it: the real pane fits now
	r.closeRightPaneFallbackIfObsolete()
	if r.overlay != nil {
		t.Error("overlay = non-nil after widening past the breakpoint, want closed")
	}
}

// An ordinary overlay (a Popup, Confirm — anything that is not this
// episode's fallback) must never be swept up by this: it has nothing to do
// with the right pane's width breakpoint.
func TestCloseRightPaneFallbackIfObsoleteLeavesOtherOverlaysAlone(t *testing.T) {
	r := newRoot(t)
	r.overlay = fakeNonFallbackOverlay{}
	r.w = 120
	r.closeRightPaneFallbackIfObsolete()
	if r.overlay == nil {
		t.Error("a non-fallback overlay was closed; only RightPaneFallback overlays should be")
	}
}

type fakeNonFallbackOverlay struct{}

func (fakeNonFallbackOverlay) MinSize() (int, int)                 { return 40, 7 }
func (fakeNonFallbackOverlay) Title() string                       { return "Confirm" }
func (fakeNonFallbackOverlay) View(theme.Theme, int, int) string   { return "" }
func (f fakeNonFallbackOverlay) Update(tea.Msg) (Overlay, tea.Cmd) { return f, nil }

var _ Overlay = fakeNonFallbackOverlay{}

// refreshTasks must never ask while nothing that shows its answer is on
// screen — F06-right-pane.md's Done-when #6 ("no status/usage subscriptions
// while hidden"). With no client at all, "asked" is observable as "did not
// panic and left tasksReqID empty" either way, but the visibility guard is
// what this test pins down: it must be checked before anything about the
// client.
func TestRefreshTasksNoopsWhenPaneHidden(t *testing.T) {
	r := &Root{}
	r.layout.ShowRightPane = false
	r.refreshTasks()
	if r.tasksReqID != "" {
		t.Errorf("tasksReqID = %q, want no request while the pane is hidden", r.tasksReqID)
	}
}
