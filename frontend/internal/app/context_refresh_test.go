package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// fakeContextView stands in for chat's UI-09 (this package cannot import
// chat): small enough to always fit, closes on esc.
type fakeContextView struct{}

func (fakeContextView) MinSize() (int, int)               { return 40, 10 }
func (fakeContextView) Title() string                     { return "Context" }
func (fakeContextView) View(theme.Theme, int, int) string { return "" }
func (fakeContextView) IsContextView() bool               { return true }
func (f fakeContextView) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyEsc {
		return nil, nil
	}
	return f, nil
}

// fakeSwapToContext stands in for rightPaneOverlay: enter swaps it for UI-09.
type fakeSwapToContext struct{}

func (fakeSwapToContext) MinSize() (int, int)               { return 36, 20 }
func (fakeSwapToContext) Title() string                     { return "Right Pane" }
func (fakeSwapToContext) View(theme.Theme, int, int) string { return "" }
func (f fakeSwapToContext) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyEnter {
		return fakeContextView{}, nil
	}
	return f, nil
}

// fakeContextWorkspace counts refresh requests and can produce UI-09 for
// the context.expand leader action.
type fakeContextWorkspace struct{ refreshes int }

func (f *fakeContextWorkspace) ID() WorkspaceID                     { return WorkspaceChat }
func (f *fakeContextWorkspace) Init() tea.Cmd                       { return nil }
func (f *fakeContextWorkspace) View(layout.Layout) string           { return "" }
func (f *fakeContextWorkspace) Suspend() ([]byte, error)            { return nil, nil }
func (f *fakeContextWorkspace) Resume([]byte) error                 { return nil }
func (f *fakeContextWorkspace) Cards() []layout.CardSlot            { return nil }
func (f *fakeContextWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) { return f, nil }
func (f *fakeContextWorkspace) RefreshContextReport()               { f.refreshes++ }
func (f *fakeContextWorkspace) ContextExpandOverlay() (Overlay, bool) {
	return fakeContextView{}, true
}

var _ ContextView = fakeContextView{}
var _ ContextRefresher = (*fakeContextWorkspace)(nil)
var _ ContextExpandable = (*fakeContextWorkspace)(nil)

func envelope(eventType string) protocol.EnvelopeMsg {
	return protocol.EnvelopeMsg{Envelope: protocol.Envelope{Type: eventType, TaskID: "t1"}}
}

// UI-09 used to draw only what the workspace last received — the load-time
// pull or the push after a turn PERSISTED — so one @research running with
// UI-09 open showed "next turn evicts: nothing" while a fresh report for the
// same moment named that task's question as unanswered. Root now refreshes
// on the transition to shown and on every task lifecycle event while shown,
// and never while UI-09 is closed.
func TestContextViewRefreshesOnOpenAndLifecycleOnly(t *testing.T) {
	r := newRoot(t)
	ws := &fakeContextWorkspace{}
	r.SetWorkspace(WorkspaceChat, ws)
	size(t, r, 120, 30)
	ws.refreshes = 0

	// Closed: lifecycle events must cost nothing.
	r.Update(envelope(protocol.EvTaskStarted))
	r.Update(envelope(protocol.EvTaskDone))
	if ws.refreshes != 0 {
		t.Fatalf("%d refresh(es) while UI-09 was closed, want 0", ws.refreshes)
	}

	// Open through the real leader chord (context.expand, leader x).
	r.Update(key("ctrl+w"))
	r.Update(key("x"))
	if _, open := r.overlay.(fakeContextView); !open {
		t.Fatalf("leader x did not open UI-09; overlay = %#v", r.overlay)
	}
	if ws.refreshes != 1 {
		t.Fatalf("opening UI-09 sent %d refresh(es), want exactly 1", ws.refreshes)
	}

	// Open: a key, or a stream token, is not a reason to re-pull.
	r.Update(key("j"))
	r.Update(envelope(protocol.EvTextDelta))
	if ws.refreshes != 1 {
		t.Fatalf("non-lifecycle messages while open refreshed (%d total), want still 1", ws.refreshes)
	}

	// Open: task lifecycle events are.
	r.Update(envelope(protocol.EvTaskStarted))
	r.Update(envelope(protocol.EvTaskFailed))
	if ws.refreshes != 3 {
		t.Fatalf("lifecycle events while open gave %d refreshes total, want 3", ws.refreshes)
	}

	// Closed again: silent again.
	r.Update(key("esc"))
	r.Update(envelope(protocol.EvTaskStarted))
	if ws.refreshes != 3 {
		t.Fatalf("a lifecycle event after closing refreshed (%d total), want still 3", ws.refreshes)
	}
}

// The fallback overlay's enter-swap opens UI-09 from INSIDE another
// overlay's Update — no action, no OpenOverlay. The transition is derived
// after the message, so it is caught all the same.
func TestContextViewRefreshesWhenOpenedBySwap(t *testing.T) {
	r := newRoot(t)
	ws := &fakeContextWorkspace{}
	r.SetWorkspace(WorkspaceChat, ws)
	size(t, r, 120, 30)
	r.overlay = fakeSwapToContext{}
	ws.refreshes = 0

	r.Update(key("enter"))

	if _, open := r.overlay.(fakeContextView); !open {
		t.Fatalf("enter did not swap to UI-09; overlay = %#v", r.overlay)
	}
	if ws.refreshes != 1 {
		t.Fatalf("a swap-open sent %d refresh(es), want exactly 1", ws.refreshes)
	}
}
