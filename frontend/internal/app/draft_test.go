package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// fakeDraftWorkspace is a minimal app.Workspace that also implements
// DraftFlusher, standing in for chat.Workspace — this package cannot import
// chat (chat already imports app; the reverse would cycle), the same reason
// focus_test.go's fakeFocusWorkspace exists.
type fakeDraftWorkspace struct {
	id      WorkspaceID
	flushed int
}

func (f *fakeDraftWorkspace) ID() WorkspaceID                     { return f.id }
func (f *fakeDraftWorkspace) Init() tea.Cmd                       { return nil }
func (f *fakeDraftWorkspace) View(layout.Layout) string           { return "" }
func (f *fakeDraftWorkspace) Suspend() ([]byte, error)            { return nil, nil }
func (f *fakeDraftWorkspace) Resume([]byte) error                 { return nil }
func (f *fakeDraftWorkspace) Cards() []layout.CardSlot            { return nil }
func (f *fakeDraftWorkspace) FlushDraft()                         { f.flushed++ }
func (f *fakeDraftWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) { return f, nil }

var _ Workspace = (*fakeDraftWorkspace)(nil)
var _ DraftFlusher = (*fakeDraftWorkspace)(nil)

// Bug 17: app.quit must flush the active workspace's debounced draft save
// BEFORE returning tea.Quit — a still-pending tea.Tick never gets delivered
// once bubbletea has already been told to quit, so this is the only chance
// a debounced write gets to catch up. See app\draft.go's DraftFlusher doc.
func TestQuitFlushesADraftFlusherWorkspace(t *testing.T) {
	r := newRoot(t)
	ws := &fakeDraftWorkspace{id: WorkspaceChat}
	r.SetWorkspace(WorkspaceChat, ws)

	a := mustAction(t, r, "app.quit")
	cmd := a.Run(r)

	if ws.flushed != 1 {
		t.Fatalf("FlushDraft called %d times, want 1", ws.flushed)
	}
	if cmd == nil {
		t.Fatal("app.quit returned a nil command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("app.quit did not still return tea.Quit")
	}
	if !r.quitting {
		t.Fatal("app.quit did not set quitting")
	}
}

// A workspace with nothing to flush (no DraftFlusher — the F1 Placeholder,
// the future Editor) must not make quitting panic or behave any
// differently: the type assertion fails harmlessly, same as FocusAware's.
func TestQuitWithoutADraftFlusherWorkspaceStillQuits(t *testing.T) {
	r := newRoot(t) // F1's Placeholder is installed by default, no DraftFlusher

	a := mustAction(t, r, "app.quit")
	cmd := a.Run(r)

	if cmd == nil {
		t.Fatal("app.quit returned a nil command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("app.quit did not still return tea.Quit")
	}
	if !r.quitting {
		t.Fatal("app.quit did not set quitting")
	}
}
