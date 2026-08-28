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
			// F1 has one pane worth focusing. The action exists because the
			// registry is the contract every later episode registers against,
			// and a binding that appears in F6 is a binding nobody tested.
			return status("pane focus arrives with the panes, in F3–F6")
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
