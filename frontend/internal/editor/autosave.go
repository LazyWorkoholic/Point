package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// Autosave (D90), and its five rules.
//
//	[editor]
//	autosave = "off" | "after_delay" | "on_focus_change"   # default: after_delay
//	autosave_delay_ms = 1000
//
// 1000ms is VS Code's number and F13 gives the reason it was taken: it is
// long enough that you rarely save mid-token, which matters because an
// autosave with the preview running (F17) reloads the browser on a
// possibly-broken file.
//
// # The five rules, and where each one lives
//
//  1. Never autosave a read-only or other-holder-locked file.
//     saveDoc, which also sets autosaveBlocked so the refusal happens
//     once rather than once a second.
//  2. Record Point's own write mtimes.
//     onWriteSucceeded, from the reply's own mtime. This is also
//     Done-when 12 and the reason no CheckFileStale follows a save.
//  3. Never autosave a buffer with no path.
//     saveDoc.
//  4. An agent-applied diff autosaves like any other edit, and ctrl+z
//     after it must also save.
//     Both fall out of arming on buffer VERSION rather than on
//     keystrokes -- see autosaveAfter. ApplyRegion and Undo both splice,
//     so both move the version, so both arm the timer. Nothing had to
//     special-case either.
//  5. Autosave does not clear undo history.
//     Nothing here touches history. Stated as a rule because it is the
//     obvious thing to get wrong if saving were ever implemented as
//     "reload what was written".
//
// The dirty marker still appears between the edit and the save, so the
// status bar never lies about the current state.
//
// # Why the timer is PER DOCUMENT (C5)
//
// Each doc carries its own autosaveGen. An edit increments that
// document's counter and schedules a tick carrying the new value; a tick
// whose value no longer matches is superseded and does nothing. So
// editing file B touches B's counter only, and A's already-pending tick
// still fires on A's own schedule.
//
// A single workspace-level counter would have been simpler and wrong in
// a way this project has now shipped three times: the idle-notice latch
// (one flag standing in for per-workspace state), theme propagation (one
// workspace updated standing in for all), and status expiry (one
// assignment standing in for a per-message timer). Per-entity timer and
// state isolation is a standing risk area here, not a one-off, and the
// test for it (TestEditingOneFileDoesNotDelayAnothersAutosave) exists
// because the bug is invisible until two files are dirty at once.

// autosaveTickMsg is one document's debounce expiring.
//
// It carries the PATH rather than a *doc pointer, for the same reason
// pendingReq does: the document may have been closed between the tick
// being armed and delivered, and a pointer would keep a closed
// document's whole buffer alive until the tick fired.
type autosaveTickMsg struct {
	path string
	gen  int
}

// editProbe is a before-and-after snapshot of the document a message
// might edit.
//
// Taken around Update's whole key/mouse/paste handling rather than at
// each edit call site, which is round 5's lesson applied in advance:
// r.status had about forty assignment sites and the expiry had to be
// derived in the one funnel they all passed through. The edit paths here
// are fewer but the shape is identical -- insertRune, backspace,
// deleteForward, deleteWordLeft, newline, indentSelection,
// dedentSelection, maybePair, insertText, Undo, Redo and ApplyRegion all
// mutate -- and arming the timer from the buffer's own Version() cannot
// miss one, including the two F9b and F15 will add.
type editProbe struct {
	path string
	ver  uint64
}

func (w *Workspace) probeActive() editProbe {
	if len(w.docs) == 0 {
		return editProbe{}
	}
	d := w.docs[w.active]
	return editProbe{path: d.path, ver: d.buf.Version()}
}

// autosaveAfter arms this document's debounce if the buffer moved.
//
// Matched on PATH, not on the active index: a tab switch inside the same
// Update would otherwise compare one document's version against
// another's and read as an edit.
func (w *Workspace) autosaveAfter(p editProbe) tea.Cmd {
	if p.path == "" {
		return nil
	}
	d := w.docByPath(p.path)
	if d == nil || d.buf.Version() == p.ver {
		return nil
	}
	return w.armAutosave(d)
}

// armAutosave schedules one document's save.
//
// The generation is bumped whether or not a tick is actually scheduled,
// so turning autosave off does not leave a stale pending tick able to
// fire against a later edit.
func (w *Workspace) armAutosave(d *doc) tea.Cmd {
	d.autosaveGen++
	if w.autosaveMode != config.AutosaveAfterDelay {
		return nil
	}
	if d.autosaveBlocked || !d.canSave() {
		// Rule 1, applied before the timer rather than at its end:
		// scheduling a tick that is certain to refuse is how "does not
		// spam refusals" becomes "refuses silently once a second".
		return nil
	}
	gen, path := d.autosaveGen, d.path
	return tea.Tick(w.autosaveDelay, func(time.Time) tea.Msg {
		return autosaveTickMsg{path: path, gen: gen}
	})
}

// onAutosaveTick fires one document's save, if nothing superseded it.
func (w *Workspace) onAutosaveTick(m autosaveTickMsg) tea.Cmd {
	d := w.docByPath(m.path)
	if d == nil || d.autosaveGen != m.gen {
		// Superseded: another edit landed inside the window and armed a
		// newer tick. This is the whole of Done-when 10's "typing
		// continuously for 30s produces ONE save at the end, not
		// thirty" -- every keystroke replaces the deadline, and only the
		// last one survives to fire.
		return nil
	}
	return w.saveDoc(d, saveAuto, false)
}

// autosaveOnFocusChange is the `on_focus_change` mode.
//
// "Focus change" is read as the thing a user would mean by it here: the
// active DOCUMENT changing -- a tab switch, opening a file, closing one
// -- not the Chat/Editor workspace switch and not the buffer/tree pane
// focus. A pane focus toggle (ctrl+o) is a navigation keystroke inside
// one file and saving on it would make ctrl+o a save key; a workspace
// switch already routes through Suspend, and F12's own unload path is
// what covers that case.
//
// Deliberately NOT also firing in after_delay mode. The two modes are
// alternatives in the config, and a mode that did both would make
// `after_delay` mean "after a delay, or sooner if you switch tabs",
// which is not what it says.
func (w *Workspace) autosaveOnFocusChange(prev *doc) tea.Cmd {
	if w.autosaveMode != config.AutosaveOnFocusChange || prev == nil {
		return nil
	}
	if !prev.dirty || prev.autosaveBlocked {
		return nil
	}
	return w.saveDoc(prev, saveAuto, false)
}
