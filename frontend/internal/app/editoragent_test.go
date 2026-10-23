package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F15's Root-side half: the two leader actions reach the Editor through
// EditorAgent, and `send to chat` reaches Chat through ChatDraftTarget.

// agentWorkspace is a stub that records which of the two it was asked for.
type agentWorkspace struct {
	Workspace
	explained bool
	filled    bool
	prefilled string
}

func (a *agentWorkspace) BeginExplain() tea.Cmd { a.explained = true; return nil }
func (a *agentWorkspace) BeginFill() tea.Cmd    { a.filled = true; return nil }
func (a *agentWorkspace) PrefillDraft(s string) { a.prefilled = s }

// stubWorkspace is the minimum app.Workspace a Root will accept.
type stubWorkspace struct{ id WorkspaceID }

func (s *stubWorkspace) Init() tea.Cmd { return nil }
func (s *stubWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) {
	return s, nil
}
func (s *stubWorkspace) View(layout.Layout) string  { return "" }
func (s *stubWorkspace) Suspend() ([]byte, error)   { return nil, nil }
func (s *stubWorkspace) Resume([]byte) error        { return nil }
func (s *stubWorkspace) ID() WorkspaceID            { return s.id }
func (s *stubWorkspace) Cards() []layout.CardSlot   { return nil }
func (s *stubWorkspace) SetTheme(theme.Theme)       {}
func (a *agentWorkspace) ID() WorkspaceID           { return WorkspaceEditor }
func (a *agentWorkspace) View(layout.Layout) string { return "" }
func (a *agentWorkspace) Init() tea.Cmd             { return nil }
func (a *agentWorkspace) Suspend() ([]byte, error)  { return nil, nil }
func (a *agentWorkspace) Resume([]byte) error       { return nil }
func (a *agentWorkspace) Cards() []layout.CardSlot  { return nil }
func (a *agentWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) {
	return a, nil
}

// `a` and `f` must be free against the WHOLE shipped action set, including
// the switch actions registerWorkspaces DERIVES from WorkspaceDef.LeaderKey
// -- which is exactly what a grep for `Leader: "` misses, and how this
// episode first picked `e` (the Editor's own switch key, guarded by
// TestLeaderEIsNotTakenByAShippedAction).
//
// A duplicate leader sequence is a BOOT failure, so this is an "app starts"
// test wearing a binding test's clothes.
func TestTheEditorAgentLeaderKeysAreTheOnesTheyClaim(t *testing.T) {
	r := newRoot(t)
	want := map[string]string{"editor.explain": "a", "editor.fill": "f"}
	seen := map[string]string{}
	for _, action := range r.actions.All() {
		if l, ok := want[action.ID]; ok {
			seen[action.ID] = action.Leader
			if action.Leader != l {
				t.Errorf("%s has leader %q, want %q", action.ID, action.Leader, l)
			}
			continue
		}
		for id, l := range want {
			if action.Leader == l {
				t.Errorf("leader %q is already %s, so %s cannot have it",
					l, action.ID, id)
			}
		}
	}
	if len(seen) != len(want) {
		t.Errorf("registered %v, want both of %v", seen, want)
	}
}

// Both actions are unavailable where there is no Editor, with the honest
// greyed-out reason rather than being invisible -- context.expand's own
// treatment from the same shape of predicate.
func TestTheEditorAgentActionsAreUnavailableOutsideTheEditor(t *testing.T) {
	r := newRoot(t)
	r.workspaces = map[WorkspaceID]Workspace{WorkspaceChat: &stubWorkspace{id: WorkspaceChat}}
	r.active = WorkspaceChat

	for _, id := range []string{"editor.explain", "editor.fill"} {
		action, ok := r.actions.ByID(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if action.Available(r) {
			t.Errorf("%s is available in Chat", id)
		}
	}
}

func TestTheLeaderKeysReachTheEditor(t *testing.T) {
	r := newRoot(t)
	ws := &agentWorkspace{}
	r.workspaces = map[WorkspaceID]Workspace{WorkspaceEditor: ws}
	r.active = WorkspaceEditor

	for _, tc := range []struct {
		id   string
		want func() bool
	}{
		{"editor.explain", func() bool { return ws.explained }},
		{"editor.fill", func() bool { return ws.filled }},
	} {
		action, ok := r.actions.ByID(tc.id)
		if !ok {
			t.Fatalf("%s is not registered", tc.id)
		}
		if !action.Available(r) {
			t.Fatalf("%s is unavailable with an Editor active", tc.id)
		}
		action.Run(r)
		if !tc.want() {
			t.Errorf("%s did not reach the workspace", tc.id)
		}
	}
}

// SendToChatMsg switches to Chat and hands the text over. It is Root's job
// because workspaces never talk to each other and because Chat may not be
// loaded.
func TestSendToChatSwitchesAndPrefills(t *testing.T) {
	r := newRoot(t)
	chat := &agentWorkspace{}
	r.workspaces = map[WorkspaceID]Workspace{
		WorkspaceEditor: &agentWorkspace{},
		WorkspaceChat:   chat,
	}
	r.defByID[WorkspaceChat] = WorkspaceDef{
		ID: WorkspaceChat, Name: "Chat",
		New: func() Workspace { return chat },
	}
	r.active = WorkspaceEditor

	r.Update(SendToChatMsg{Text: "About a.py — explain this\n\n> It returns early."})

	if r.active != WorkspaceChat {
		t.Errorf("active = %q, want Chat", r.active)
	}
	if !strings.Contains(chat.prefilled, "It returns early.") {
		t.Errorf("prefilled = %q", chat.prefilled)
	}
}

// And a Chat that cannot be reached says so rather than swallowing the
// text: the panel has already been dismissed to make this happen.
func TestSendToChatSaysSoWhenChatCannotTakeIt(t *testing.T) {
	r := newRoot(t)
	r.workspaces = map[WorkspaceID]Workspace{
		WorkspaceChat: &stubWorkspace{id: WorkspaceChat},
	}
	r.active = WorkspaceChat

	cmd := r.sendToChat(SendToChatMsg{Text: "something"})
	if cmd == nil {
		t.Fatal("no status was produced")
	}
}
