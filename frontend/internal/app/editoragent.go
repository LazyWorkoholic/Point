package app

import tea "github.com/charmbracelet/bubbletea"

// F15's seams between Root and the two workspaces the Filler-Explainer
// touches.
//
// The same narrow-interface pattern focus.go, draft.go, stream.go and
// context_expand.go all already use: Root never reads a workspace's fields,
// it asks a one-method interface through a type assertion that fails
// silently for a workspace that does not implement it.

// EditorAgent is implemented by a workspace that can invoke the
// Filler-Explainer against whatever its caret is on — editor.Workspace is
// the only implementer, and the two leader actions below are the only
// callers.
//
// Two methods rather than one taking a mode, because the two paths are not
// symmetrical and the asymmetry is the spec's: explain SENDS immediately
// (there is nothing to type — "this is why the Explainer needs no typing"),
// while fill opens an inline prompt and sends nothing until Enter. A single
// Invoke(mode) would have to document that one of its two values returns
// after a round trip and the other after a keystroke.
//
// Each returns the tea.Cmd it wants run, including the status line for a
// refusal — "open a file first", "already asking". Root does not decide
// whether the invocation is possible; the workspace does, because every
// fact it turns on (a document open, read-only, one already in flight)
// lives there.
type EditorAgent interface {
	BeginExplain() tea.Cmd
	BeginFill() tea.Cmd
}

// SendToChatMsg carries an exchange from the Editor's explain panel into
// Chat's input.
//
// # It PREFILLS the input; it does not append to the session
//
// F15's Build says `send to chat` "copies the exchange into the current
// chat session", and there is no event on the wire that could do that:
// `SendMessage` RUNS A TURN, and nothing inserts a finished
// question-and-answer pair into a session's history. Writing one would be a
// backend episode (a message-append event, with the provenance and
// ordering questions that go with it), and F15 is a frontend episode whose
// own Do-NOT list is about not widening scope.
//
// So the honest version is this: the text reaches Chat's input, quoted and
// labelled with the file it is about, and the user decides whether to send
// it. Recorded in F15's completed doc as a deliberate narrowing with the
// backend addition it would need — not as a limitation discovered late, and
// not as a decided dead end.
//
// Routed by Root rather than sent workspace-to-workspace, because
// workspaces never talk to each other (workspace.go's two rules) — and
// because Chat may not be loaded, which Root is the only thing that can fix.
type SendToChatMsg struct{ Text string }

// ChatDraftTarget is implemented by a workspace that can take over a
// prepared draft — chat.Workspace, for SendToChatMsg.
//
// chat's implementation APPENDS below whatever was already typed rather
// than replacing it, and the reason is specific: `setText` clears the
// input's undo history, so a replacement reached from a keypress in
// ANOTHER workspace could destroy typing with no key to get it back. An
// empty input is simply filled. See chat\prefill.go.
type ChatDraftTarget interface {
	PrefillDraft(text string)
}

// sendToChat routes SendToChatMsg: switch to Chat, then hand it the text.
//
// In that order, and loading Chat if it is not loaded — the Editor's panel
// is being dismissed to make this happen, so an unloaded Chat must not
// silently swallow the exchange. switchWorkspace replays the handshake and
// the window size to a freshly loaded workspace, so the prefill lands in a
// workspace that is ready to render it.
func (r *Root) sendToChat(m SendToChatMsg) tea.Cmd {
	cmd := r.switchWorkspace(WorkspaceChat)
	t, ok := r.current().(ChatDraftTarget)
	if !ok {
		// Chat could not be loaded (its MinSize refused the terminal, or
		// it is not registered in this build). Said out loud rather than
		// dropped: the text existed and is now gone.
		return tea.Batch(cmd, status("could not open Chat — the explanation was not copied"))
	}
	t.PrefillDraft(m.Text)
	return tea.Batch(cmd, status("copied into Chat's input — not sent"))
}
