package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
)

// registerActions is the ONE registration point. The command palette, the
// leader hint bar, the help screen and config rebinding all read from what
// this function builds — adding a feature is an entry here, not edits in four
// files.
//
// F1 seeds the minimum its spec lists. backend.retry is F2's: the failure
// banner is specified as a status-bar line WITH a retry action, and a banner
// with no way to act on it is just a complaint.
func registerActions() *actions.Registry[*Root] {
	r := actions.NewRegistry[*Root]()

	r.Register(actions.Action[*Root]{
		ID:          "app.quit",
		Name:        "Quit",
		Description: "Leave Point, restoring the terminal",
		Category:    "Application",
		// F5: ctrl+q, not ctrl+c. Ctrl+C is cancel-the-running-thing from
		// this episode on (F05-streaming-queue.md: "quit is `ctrl+q`"),
		// which is what that chord means in every other tool that streams.
		// Bug 17's draft flush below moved WITH the action, not with the
		// old chord — leaving it behind would have restored the exact
		// data loss F4 spent a verification round finding.
		Chord:  "ctrl+q",
		Leader: "q",
		Run: func(root *Root) tea.Cmd {
			// Bug 17: flush the active workspace's own debounced state
			// (chat's draft save) BEFORE returning tea.Quit — once bubbletea
			// has that, a still-pending tea.Tick never gets delivered, so
			// this is the only chance a debounced write gets to catch up
			// with whatever was typed since its last successful save. See
			// app\draft.go's DraftFlusher doc for the full trace.
			if f, ok := root.current().(DraftFlusher); ok {
				f.FlushDraft()
			}
			root.quitting = true
			return tea.Quit
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "app.palette",
		Name:        "Command Palette",
		Description: "Search every action by name",
		Category:    "Application",
		Chord:       "ctrl+p",
		Run: func(root *Root) tea.Cmd {
			// F7 builds the real palette on the shared Popup. What exists now
			// is the overlay contract it will be an instance of: a min size,
			// a fit check that names the actual numbers, and focus trapping.
			root.OpenOverlay(pendingOverlay{
				title: "Command Palette",
				w:     40, h: 10,
				body: "The palette is F7.\n\nWhat this proves today is the\noverlay contract: it refuses to\nopen when it does not fit, and\nnames the numbers when it does.",
			})
			return nil
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "ui.help",
		Name:        "Help",
		Description: "Every action, its binding and what it does",
		Category:    "Interface",
		Leader:      "?",
		Run: func(root *Root) tea.Cmd {
			root.OpenOverlay(helpOverlay{root: root})
			return nil
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "ui.redraw",
		Name:        "Redraw",
		Description: "Recompute the layout and repaint the screen",
		Category:    "Interface",
		Chord:       "ctrl+l",
		Run: func(root *Root) tea.Cmd {
			root.recompute()
			return tea.Batch(tea.ClearScreen, status("redrawn"))
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "pane.focus.next",
		Name:        "Focus Next Pane",
		Description: "Move focus to the next pane",
		Category:    "Navigation",
		Chord:       "tab",
		Run: func(root *Root) tea.Cmd {
			// F4: Chat now has two focusable panes — the query input and the
			// transcript — so this stub gets its first real job, toggling
			// between them. A workspace with nothing to focus (Placeholder,
			// the future Editor) does not implement FocusAware, and the type
			// assertion below just falls through to the old placeholder
			// status, unchanged for those.
			if fa, ok := root.current().(FocusAware); ok {
				if fa.InputFocused() {
					return func() tea.Msg { return EnterBrowseMsg{} }
				}
				return func() tea.Msg { return FocusInputMsg{} }
			}
			return status("pane focus arrives with the panes, in F3–F6")
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "chat.browse",
		Name:        "Browse Transcript",
		Description: "Leave the query input and move focus onto the transcript, for j/k/y/G",
		Category:    "Navigation",
		Chord:       "esc",
		When: func(root *Root) bool {
			return root.active == WorkspaceChat
		},
		Run: func(root *Root) tea.Cmd {
			// F5: at queue capacity esc discards the pending message
			// instead — F05's "editable (ctrl+e) and discardable (esc)
			// until it fires". Registering a second action on "esc" would
			// be a duplicate-chord failure at boot (registry.Validate), so
			// the choice is made here, from the one bit QueueAware exposes
			// — the same shape pane.focus.next already uses to choose a
			// direction from FocusAware.InputFocused.
			if root.hasPending() {
				return discardPendingCmd()
			}
			// Unconditional and idempotent — no status noise for pressing esc
			// a second time while already browsing. Tab (above) is the other
			// direction, and also toggles back to the input from here.
			return func() tea.Msg { return EnterBrowseMsg{} }
		},
	})

	// ---- F5 ------------------------------------------------------------

	r.Register(actions.Action[*Root]{
		ID:          "chat.cancel",
		Name:        "Cancel Stream",
		Description: "Stop the reply that is streaming, keeping what has arrived so far",
		Category:    "Chat",
		Chord:       "ctrl+c",
		When: func(root *Root) bool {
			return root.active == WorkspaceChat
		},
		Run: func(root *Root) tea.Cmd {
			// Deliberately NOT gated on root.streaming(): F5 requires
			// ctrl+c with nothing streaming to do nothing at all, and a
			// false When predicate would print "Cancel Stream is not
			// available right now" instead. The workspace decides there is
			// nothing to cancel, silently. See app\stream.go.
			return cancelStreamCmd()
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "chat.queue.edit",
		Name:        "Edit Queued Message",
		Description: "Pull the pending message back into the input before it fires",
		Category:    "Chat",
		Chord:       "ctrl+e",
		When: func(root *Root) bool {
			return root.active == WorkspaceChat && root.hasPending()
		},
		Run: func(root *Root) tea.Cmd {
			return editPendingCmd()
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "chat.retry",
		Name:        "Retry",
		Description: "Send the last interrupted, failed or unanswered message again",
		Category:    "Chat",
		Chord:       "ctrl+r",
		When: func(root *Root) bool {
			return root.active == WorkspaceChat
		},
		Run: func(root *Root) tea.Cmd {
			// The `[Retry]` footer F5 attaches to an interrupted, failed or
			// timed-out reply names this chord, so the affordance is not a
			// label with no key behind it.
			return retryCmd()
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "backend.retry",
		Name:        "Retry Backend",
		Description: "Start the backend again after it has been given up on",
		Category:    "Backend",
		Leader:      "r",
		When: func(root *Root) bool {
			return root.client != nil && root.conn.banner != ""
		},
		Run: func(root *Root) tea.Cmd {
			root.client.Retry()
			return status("retrying the backend")
		},
	})

	return r
}

func status(s string) tea.Cmd {
	return func() tea.Msg { return statusMsg(s) }
}
