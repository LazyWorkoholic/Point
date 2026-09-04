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
		Chord:       "ctrl+c",
		Leader:      "q",
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
			// Unconditional and idempotent — no status noise for pressing esc
			// a second time while already browsing. Tab (above) is the other
			// direction, and also toggles back to the input from here.
			return func() tea.Msg { return EnterBrowseMsg{} }
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
