package app

import (
	"fmt"
	"strings"
	"time"

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
		// BOTH, equally. ctrl+q has the same Windows input-layer hazard
		// the leader does -- F15's gate logged `cause="key q"` at the
		// moment of a quit press, the letter landing in the file -- and
		// f3 is immune for the same reason f2 is: a virtual-key code
		// resolved before the ctrl/rune branch, carrying no rune.
		// Neither is a fallback; ChordLabel() prints both.
		Chord:    "f3",
		AltChord: "ctrl+q",
		Leader:   "q",
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
			// F12 turns this into UI-14 proper: not a COUNT but the
			// list, because the asymmetry with unload is the whole
			// point. Unloading a view leaves its tasks running in the
			// backend; quitting kills the backend, so they genuinely
			// die here, and the wording has to make that visible
			// rather than leave the user to infer it from two
			// dialogs that look the same.
			//
			// root.conn.inFlight stays the TRIGGER -- it is the
			// backend's own live count of what is in flight and
			// cannot be stale -- while the rows come from the task
			// snapshot, which refreshTasks now keeps current whether
			// or not any pane is showing it (see its own doc). If the
			// snapshot has no rows for a non-zero in-flight count,
			// the dialog still warns, in the honest wording for
			// "something is running and I cannot name it".
			if len(root.conn.inFlight) > 0 {
				// Ask for a fresh snapshot FIRST and draw the dialog
				// once it lands (root.openQuitConfirm), rather than
				// drawing it now from whichever snapshot happens to be
				// in hand. With the right pane hidden and the tray
				// never opened that snapshot is empty, and a quit
				// dialog saying nothing is running while something is
				// would be the worst place in the app to break the
				// standing rule against a hint that lies. See
				// requestTasksOffScreen.
				root.quitConfirmPending = true
				if !root.requestTasksOffScreen() {
					root.openQuitConfirm() // no backend to ask; use what we have
					return nil
				}
				return tea.Tick(quitConfirmWait, func(time.Time) tea.Msg {
					return quitConfirmTimeoutMsg{}
				})
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

	// ---- F12 ------------------------------------------------------------

	r.Register(actions.Action[*Root]{
		ID:          "workspace.switcher",
		Name:        "Workspaces",
		Description: "Switch workspace -- every one, loaded or not, with its idle countdown",
		Category:    "Workspace",
		// `w`, not F12's `c`/`e`: leader `c` has been pane.right.toggle
		// since F6 and `x` has been context.expand since S13d, and a
		// duplicate leader sequence is a boot failure
		// (actions.Registry.Validate), not a key that quietly does the
		// other thing. One switcher listing every workspace also scales
		// to the parked capability workspaces without needing a free
		// letter per workspace, which is the thing that ran out here.
		Leader: "w",
		When: func(root *Root) bool {
			return len(root.Defs()) > 0
		},
		Run: func(root *Root) tea.Cmd {
			root.OpenOverlay(newSwitcherOverlay(root))
			return nil
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "workspace.unload",
		Name:        "Unload Workspace",
		Description: "Release this view from memory -- background tasks keep running",
		Category:    "Workspace",
		Leader:      "u",
		When: func(root *Root) bool {
			return root.activeIsLoaded()
		},
		Run: func(root *Root) tea.Cmd {
			return root.requestUnload()
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

	// ---- F15 ------------------------------------------------------------
	//
	// leader → a (explain, "ask") and leader → f (fill), NOT the `x`
	// F15's Build asks for -- and not `e` either, which was this
	// episode's first substitution and was also wrong.
	//
	// Both unavailable letters are unavailable the same way, and it is
	// not a conflict to live with: actions.Registry.Validate rejects a
	// duplicate leader sequence as a BOOT FAILURE (registry.go: "called
	// at boot, before the program starts, so a duplicate binding is a
	// boot failure"), so either one would have produced an app that does
	// not start, not a key that does the wrong thing.
	//
	//   `x`  context.expand, since S13d.
	//   `e`  workspace.switch.editor -- NOT a hand-registered action and
	//        so not visible in a grep for `Leader: "`, which is how this
	//        episode picked it in the first place. registerWorkspaces
	//        derives one switch action per workspace from
	//        WorkspaceDef.LeaderKey (registry.go), main.go sets `e` for
	//        the Editor, and contribute_test.go's
	//        TestLeaderEIsNotTakenByAShippedAction exists specifically to
	//        keep it free. Caught by that test on the first run.
	//
	// This is the same substitution S13d itself made and recorded: F12
	// asked for `c`/`e` for the workspace switcher, `c` had been
	// pane.right.toggle's since S13d, and the switcher moved to `w`.
	// `a` and `f` are free against the WHOLE shipped set, including the
	// derived ones -- asserted by this episode's own test rather than by
	// reading a list.
	//
	// Both are registered unconditionally and gated by When, which is what
	// makes them appear in the palette and the help screen in Chat too --
	// greyed, with the honest reason, rather than invisible. That is the
	// same treatment context.expand gets from the same shape of predicate.
	r.Register(actions.Action[*Root]{
		ID:          "editor.explain",
		Name:        "Explain Code",
		Description: "Ask the Filler-Explainer about the code under the caret -- the reply opens in a panel beside it",
		Category:    "Editor",
		Leader:      "a",
		When: func(root *Root) bool {
			_, ok := root.current().(EditorAgent)
			return ok
		},
		Run: func(root *Root) tea.Cmd {
			ea, ok := root.current().(EditorAgent)
			if !ok {
				return nil
			}
			return ea.BeginExplain()
		},
	})

	r.Register(actions.Action[*Root]{
		ID:          "editor.fill",
		Name:        "Fill at the Caret",
		Description: "Ask the Filler-Explainer to write a region at the caret -- reviewed as a diff before it reaches the buffer",
		Category:    "Editor",
		Leader:      "f",
		When: func(root *Root) bool {
			_, ok := root.current().(EditorAgent)
			return ok
		},
		Run: func(root *Root) tea.Cmd {
			ea, ok := root.current().(EditorAgent)
			if !ok {
				return nil
			}
			return ea.BeginFill()
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
	// F13b: every LOADED workspace, not just the active one -- a lock is
	// held by whichever workspace took it, and the Editor can be loaded
	// while Chat is on screen. The release is written to the backend's
	// stdin now; main.go's deferred client.Close() closes that stdin,
	// which is the shutdown main.py drains in-flight handlers for, so
	// these are processed rather than racing the exit.
	for _, ws := range root.workspaces {
		if lh, ok := ws.(LockHolder); ok {
			lh.ReleaseLocks()
		}
	}
	// F12's suspended state has exactly the same problem for exactly the
	// same reason: it is written debounced, and once bubbletea has
	// tea.Quit the pending Tick is never delivered. Without this, quitting
	// loses whatever moved since the last two-second window closed --
	// which on a deliberate quit is almost always the position the user
	// last chose.
	root.flushWorkspaceState()
	root.quitting = true
}

// quitBody is UI-14's text: what is running, and that quitting cancels it.
//
// Listed, with durations, up to a bound -- a session with forty queued
// tasks must not produce a dialog taller than the terminal, and Confirm
// sizes itself from its body.
func quitBody(root *Root, inFlight int) string {
	running := root.runningTasks()
	if len(running) == 0 {
		return fmt.Sprintf(
			"%d request(s) still in flight. Quitting stops the backend and cancels them.", inFlight)
	}

	var b strings.Builder
	if len(running) == 1 {
		b.WriteString("1 task is still running:\n")
	} else {
		fmt.Fprintf(&b, "%d tasks are still running:\n", len(running))
	}
	const shown = 4
	for i, t := range running {
		if i == shown {
			fmt.Fprintf(&b, "  … and %d more\n", len(running)-shown)
			break
		}
		fmt.Fprintf(&b, "  %s %s  %s\n",
			TaskGlyph(t.State), capitalise(TaskLabel(t)), TaskElapsed(t.StartedAt))
	}
	// The asymmetry with unload, stated rather than implied.
	b.WriteString("Quitting will cancel them -- unloading a view would not.")
	return b.String()
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
