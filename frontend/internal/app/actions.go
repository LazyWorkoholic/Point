package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
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
			// F7: quit with a backend task running is a destructive action
			// — it abandons work in progress — and routes through Confirm
			// before anything else happens. r.conn.inFlight is the
			// backend's own count, not the active workspace's Streaming():
			// a background task keeps running whether or not its
			// workspace is the one on screen, which is the point of the
			// background lane (F5's contract). Discarding a queued draft
			// (chat's esc-at-capacity) stays unconfirmed on purpose — a
			// draft persists to disk and is trivially recoverable, so it
			// is not the losable-work case Confirm exists for.
			if n := len(root.conn.inFlight); n > 0 {
				root.OpenOverlay(NewConfirm(
					"Quit With Tasks Running",
					fmt.Sprintf("%d task(s) still running. Quitting now abandons their progress.", n),
					"Quit", "Cancel",
					quitCmd(root),
				))
				return nil
			}
			doQuit(root)
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
			root.OpenOverlay(newPaletteOverlay(root))
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
			root.OpenOverlay(newHelpOverlay(root))
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

	// ---- F6 ------------------------------------------------------------

	r.Register(actions.Action[*Root]{
		ID:          "pane.right.toggle",
		Name:        "Toggle Right Pane",
		Description: "Show or hide the status, task and session cards",
		Category:    "Interface",
		Leader:      "c",
		When: func(root *Root) bool {
			return len(root.current().Cards()) > 0
		},
		Run: func(root *Root) tea.Cmd {
			// Above the 90-column breakpoint the pane is real screen real
			// estate and the toggle just shows or hides it. Below it,
			// layout.Compute never draws the pane at all regardless of
			// this flag — LAYOUT.md's "auto-hides below 90 columns, still
			// openable as an overlay" — so the same key falls back to the
			// overlay instead of doing nothing.
			//
			// No status(...) text here on purpose: the pane's own
			// presence or absence IS the feedback, and it is always live.
			// A status string is not — nothing clears "right pane shown"
			// when a later RESIZE auto-hides the pane through a path that
			// never touches r.status, so it goes stale and sits in the
			// status bar's left slot indefinitely, dimmed but still
			// legible under a later overlay's scrim. That looked like a
			// compositor bug in testing; it was a stale status string.
			if root.w >= layout.MediumWidth {
				root.rightPaneOpen = !root.rightPaneOpen
				root.recompute()
				return nil
			}
			if op, ok := root.current().(OverlayProvider); ok {
				root.OpenOverlay(op.RightPaneOverlay())
				return nil
			}
			return status(fmt.Sprintf("right pane needs %d columns or more", layout.MediumWidth))
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "context.expand",
		Name:        "Expand Context",
		Description: "Open the context composition card -- per-section token counts and what the next turn would evict",
		Category:    "Interface",
		Leader:      "x",
		When: func(root *Root) bool {
			ce, ok := root.current().(ContextExpandable)
			if !ok {
				return false
			}
			_, ready := ce.ContextExpandOverlay()
			return ready
		},
		Run: func(root *Root) tea.Cmd {
			ce, ok := root.current().(ContextExpandable)
			if !ok {
				return nil
			}
			o, ready := ce.ContextExpandOverlay()
			if !ready {
				return nil
			}
			root.OpenOverlay(o)
			return nil
		},
	})

	// ---- F9a ------------------------------------------------------------

	r.Register(actions.Action[*Root]{
		ID:          "tasks.tray",
		Name:        "Task Tray",
		Description: "Open the global task tray -- every workspace's background work, running, queued and finished",
		Category:    "Interface",
		Chord:       "f1",
		Leader:      "t",
		When: func(root *Root) bool {
			// Reachable with ZERO tasks on purpose. A tray that greys out
			// when nothing is running makes "is anything running?"
			// unanswerable by the one key that exists to answer it, and
			// trayItems has an explicit empty row for exactly this case.
			// Gated only on a backend to ask -- without one the list could
			// never refresh and the tray would be a snapshot of nothing,
			// with no way to tell that apart from genuinely nothing.
			return root.client != nil
		},
		Run: func(root *Root) tea.Cmd {
			return root.openTaskTray()
		},
	})

	// ---- F10 ------------------------------------------------------------

	r.Register(actions.Action[*Root]{
		ID:          "settings.open",
		Name:        "Settings",
		Description: "Providers, appearance -- add or change a provider's key, switch the theme",
		Category:    "Application",
		Leader:      "s",
		Run: func(root *Root) tea.Cmd {
			return root.openSettingsPopup()
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

// doQuit is app.quit's synchronous side effect: flush, then mark quitting.
//
// Bug 17: flush the active workspace's own debounced state (chat's draft
// save) BEFORE tea.Quit — once bubbletea has that, a still-pending tea.Tick
// never gets delivered, so this is the only chance a debounced write gets to
// catch up with whatever was typed since its last successful save. See
// app\draft.go's DraftFlusher doc for the full trace.
func doQuit(root *Root) {
	if f, ok := root.current().(DraftFlusher); ok {
		f.FlushDraft()
	}
	root.quitting = true
}

// quitCmd is app.quit's effect wrapped as a tea.Cmd, for the ONE case that
// cannot run it synchronously: Confirm's OnConfirm fires only if and when
// the user actually confirms, so doQuit here must wait for that — unlike
// the direct call in app.quit's own Run above, which runs immediately
// because there is nothing to wait for.
func quitCmd(root *Root) tea.Cmd {
	return func() tea.Msg {
		doQuit(root)
		return tea.QuitMsg{}
	}
}
