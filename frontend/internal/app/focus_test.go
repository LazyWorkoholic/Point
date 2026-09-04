package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// fakeFocusWorkspace is a minimal app.Workspace that also implements
// FocusAware, standing in for chat.Workspace — this package cannot import
// chat (chat already imports app; the reverse would cycle), so a local fake
// is what actions.go's type-assertion branch is tested against.
type fakeFocusWorkspace struct {
	id      WorkspaceID
	focused bool
}

func (f *fakeFocusWorkspace) ID() WorkspaceID                     { return f.id }
func (f *fakeFocusWorkspace) Init() tea.Cmd                       { return nil }
func (f *fakeFocusWorkspace) View(layout.Layout) string           { return "" }
func (f *fakeFocusWorkspace) Suspend() ([]byte, error)            { return nil, nil }
func (f *fakeFocusWorkspace) Resume([]byte) error                 { return nil }
func (f *fakeFocusWorkspace) Cards() []layout.CardSlot            { return nil }
func (f *fakeFocusWorkspace) InputFocused() bool                  { return f.focused }
func (f *fakeFocusWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) { return f, nil }

var _ Workspace = (*fakeFocusWorkspace)(nil)
var _ FocusAware = (*fakeFocusWorkspace)(nil)

func execCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("action returned a nil command")
	}
	return cmd()
}

// pane.focus.next toggles: EnterBrowseMsg when the input has focus,
// FocusInputMsg when it does not — read through FocusAware, never by
// reaching into the workspace's fields.
func TestPaneFocusNextTogglesByReadingFocusAware(t *testing.T) {
	r := newRoot(t)
	ws := &fakeFocusWorkspace{id: WorkspaceChat, focused: true}
	r.SetWorkspace(WorkspaceChat, ws)

	a, ok := r.actions.ByID("pane.focus.next")
	if !ok {
		t.Fatal("pane.focus.next is not registered")
	}

	if _, ok := execCmd(t, a.Run(r)).(EnterBrowseMsg); !ok {
		t.Fatal("expected EnterBrowseMsg while the input is focused")
	}

	ws.focused = false
	if _, ok := execCmd(t, a.Run(r)).(FocusInputMsg); !ok {
		t.Fatal("expected FocusInputMsg while the input is not focused")
	}
}

// A workspace with nothing to focus (no FocusAware) falls back to F1's
// original placeholder status — unchanged for the future Editor workspace,
// which has not been touched by F4.
func TestPaneFocusNextFallsBackWithoutFocusAware(t *testing.T) {
	r := newRoot(t)
	msg := execCmd(t, mustAction(t, r, "pane.focus.next").Run(r))
	if _, ok := msg.(statusMsg); !ok {
		t.Fatalf("expected the F1 placeholder status, got %T", msg)
	}
}

// chat.browse (esc) always emits EnterBrowseMsg, and is only available in
// the Chat workspace.
func TestChatBrowseAction(t *testing.T) {
	r := newRoot(t)
	a := mustAction(t, r, "chat.browse")

	if !a.Available(r) {
		t.Fatal("chat.browse should be available in the Chat workspace")
	}
	if _, ok := execCmd(t, a.Run(r)).(EnterBrowseMsg); !ok {
		t.Fatal("chat.browse should emit EnterBrowseMsg")
	}

	r.active = WorkspaceEditor
	if a.Available(r) {
		t.Fatal("chat.browse should not be available outside the Chat workspace")
	}
}

func mustAction(t *testing.T, r *Root, id string) actions.Action[*Root] {
	t.Helper()
	a, ok := r.actions.ByID(id)
	if !ok {
		t.Fatalf("%s is not registered", id)
	}
	return a
}
