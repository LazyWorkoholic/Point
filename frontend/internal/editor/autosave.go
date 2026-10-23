package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
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
	// wasDirty is whether the document was ALREADY diverged from disk
	// when the probe was taken, so autosaveAfter can log the transition
	// from clean to dirty once rather than every keystroke after it.
	wasDirty bool
}

func (w *Workspace) probeActive() editProbe {
	if len(w.docs) == 0 {
		return editProbe{}
	}
	d := w.docs[w.active]
	return editProbe{path: d.path, ver: d.buf.Version(), wasDirty: d.dirty}
}

// autosaveAfter arms this document's debounce if the buffer moved.
//
// Matched on PATH, not on the active index: a tab switch inside the same
// Update would otherwise compare one document's version against
// another's and read as an edit.
//
// `cause` names the input that moved it, and is LOGGED the first time a
// clean buffer becomes dirty. That one line is the answer to a question
// this episode's gate has now asked three times -- "what wrote app.py?" --
// and could not answer, because the editor is a text editor: any key no
// surface claimed is a character typed into the file, and autosave then
// persists it entirely correctly. A write with no visible cause looks like
// the agent path misbehaving when it is usually a stray keystroke, and the
// two need telling apart from the log alone.
func (w *Workspace) autosaveAfter(p editProbe, cause string) tea.Cmd {
	if p.path == "" {
		return nil
	}
	d := w.docByPath(p.path)
	if d == nil || d.buf.Version() == p.ver {
		return nil
	}
	if !p.wasDirty {
		// The transition that matters: a file that was in step with the
		// disk no longer is, and this says what did it.
		w.log.Info("editor: buffer became dirty",
			"file", d.name(), "cause", cause, "version", d.buf.Version())
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
	if w.autosavePaused {
		// A modal question about these buffers is open. The generation
		// bump above has already retired whatever was in flight; this
		// just declines to schedule a replacement. Not autosaveBlocked:
		// nothing is wrong with the document and nothing should be said
		// about it in the status bar -- the dialog on screen is the
		// explanation.
		return nil
	}
	if !d.canSave() {
		// Rule 1, applied before the timer rather than at its end:
		// scheduling a tick that is certain to refuse is how "does not
		// spam refusals" becomes "refuses silently once a second".
		//
		// RECORDED, not just skipped. This early return used to fire
		// before saveDoc -- the only place that set autosaveBlocked --
		// so a file held by another holder was silently never
		// autosaved and the status bar's "autosave stopped" segment,
		// which is gated on that flag, never appeared. The file stayed
		// correctly untouched and nothing said why, which is the
		// hint-that-lies failure in reverse: silence where a state
		// exists. Found by gate_f13b step 7.
		//
		// canSave() is exactly the right predicate to latch on: every
		// reason it returns false for -- read-only, held by another
		// holder, no path -- is a reason that cannot change without an
		// event that clears the flag again (a reload, a successful
		// write, a lock taken back, a manual save).
		d.autosaveBlocked = true
		return nil
	}
	if d.autosaveBlocked {
		return nil
	}
	gen, path := d.autosaveGen, d.path
	return tea.Tick(w.autosaveDelay, func(time.Time) tea.Msg {
		return autosaveTickMsg{path: path, gen: gen}
	})
}

// onAutosaveTick fires one document's save, if nothing superseded it.
func (w *Workspace) onAutosaveTick(m autosaveTickMsg) tea.Cmd {
	if w.autosavePaused {
		// Belt to the generation bump's braces. PauseAutosave retires
		// every outstanding tick by bumping each document's counter, so
		// a tick arriving here while paused should already be
		// superseded -- but "should already be" is how the close
		// confirm's first fix got it wrong (the flag alone missed the
		// tick that was already in flight), and the cost of checking
		// both is one comparison.
		return nil
	}
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

// PauseAutosave and ResumeAutosave are app.AutosavePauser: UI-13's unload
// confirm, which Root opens, asking the Editor to hold still while it is
// up.
//
// confirmCloseDirty (tabs.go) does the same thing for its own dialog and
// its doc carries the full reasoning. The short version is that BOTH
// halves are required: the flag stops a NEW tick being scheduled, and the
// generation bump retires the one that is already in flight -- armAutosave
// is consulted when a timer is set, and the dangerous tick here was set a
// moment ago by the keystroke that made the file dirty.
//
// Every document is bumped, not just the active one. The dialog counts
// every dirty buffer in the workspace and the unload would take all of
// them, so a pause that covered only the visible file would leave the
// other two racing the same dialog.
func (w *Workspace) PauseAutosave() {
	w.autosavePaused = true
	for _, d := range w.docs {
		d.autosaveGen++
	}
}

// ResumeAutosave puts the normal schedule back after a cancelled unload.
//
// It re-arms every document that is still dirty rather than waiting for
// the next keystroke. Without that, cancelling the dialog would leave a
// dirty file with no pending tick and no edit coming -- autosave would
// look broken until the user typed one more character, which is the
// quietest possible way to lose the next crash's worth of work.
//
// armAutosave is the only scheduler, so each document goes back through
// the same five rules it would have on an ordinary edit: a read-only or
// other-holder-locked file stays blocked and stays silent, and a clean
// document is skipped here rather than relying on saveDoc's own
// not-dirty guard.
func (w *Workspace) ResumeAutosave() tea.Cmd {
	w.autosavePaused = false
	var cmds []tea.Cmd
	for _, d := range w.docs {
		if !d.dirty {
			continue
		}
		if cmd := w.armAutosave(d); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// WantsBackgroundReplies is app.BackgroundReplies.
//
// True exactly while a write is outstanding, which is the only reply this
// workspace cannot afford to have dropped -- see app\contribute.go's
// BackgroundReplies for the failure it prevents. A lock heartbeat or a
// tree listing that misses its reply while the user is in Chat costs
// nothing: switchWorkspace replays ConnectedMsg on the way back in and
// both are re-issued.
//
// Scanning the open documents rather than keeping a counter is the same
// call effectiveFocus and refreshContextIfShown already make here: a
// derivation at the moment of asking cannot drift, and a counter
// incremented in saveDoc and decremented in onWritten has two paths that
// must agree forever -- including the Error path in protocol.go, which is
// exactly the one that would be forgotten.
// F15 adds the second such reply: a live explain OR fill invocation.
//
// Both, not fill alone. F15's Done-when 3b is written about the ghost
// ("switch to Chat mid-stream and back -> the ghost shows everything that
// arrived while away, not a frozen snapshot"), but the explanation streams
// into this workspace through the identical path, and a panel that stopped
// accumulating while the user was in Chat would be the same bug one surface
// over -- a frozen snapshot, arrived at by a different route. There is no
// version of "survives a workspace switch" that is true for one and false
// for the other.
//
// This is what makes 3b hold WITHOUT a second rehydrate mechanism: the
// workspace stays loaded, keeps receiving its own events while inactive,
// and the ghost is rebuilt from the whole accumulated reply on every token
// (invoke.go's applyStreamText). Nothing is snapshotted at the moment of
// leaving, so nothing can be stale on return.
//
// KNOWN GAP, recorded rather than papered over: this keeps a BACKGROUNDED
// workspace receiving, and it does not stop an IDLE one being unloaded.
// lifecycle.go's idle walk pins a workspace only for unsaved buffers
// (DirtyBuffers), and a fill in flight dirties nothing by design -- so a
// long enough idle period mid-stream can still unload the Editor and take
// the ghost with it. The task itself keeps running (that is F12's whole
// demo beat) and the reply is still in the session, so nothing is lost
// except the ghost; a returning user sees no proposal. Not fixed here
// because the fix is a change to what pins a workspace, which is
// lifecycle.go's policy and not this episode's Build section.
func (w *Workspace) WantsBackgroundReplies() bool {
	if w.liveInvocation() {
		return true
	}
	for _, d := range w.docs {
		if d.saving {
			return true
		}
	}
	return false
}

// Compile-time proof that Root will actually find these.
//
// Both interfaces are consulted through a type assertion that fails
// SILENTLY by design -- "a workspace that does not implement one is
// unaffected", as contribute.go puts it -- which is right for an optional
// capability and wrong for one this package depends on. A renamed method
// or a changed signature would not break the build, it would quietly turn
// the fix off: writes would stop being delivered in the background again,
// and the unload dialog would stop pausing anything. Neither has a test
// that fails obviously at the unit level, so the assertion is the guard.
var (
	_ app.AutosavePauser    = (*Workspace)(nil)
	_ app.BackgroundReplies = (*Workspace)(nil)
	// F15's own, for the identical reason: the two leader actions find
	// this workspace through a type assertion that fails silently, so a
	// renamed method would turn explain and fill off rather than break
	// the build.
	_ app.EditorAgent = (*Workspace)(nil)
	// F15's background filter, for the same reason again: Root asks for
	// it through a type assertion, so a renamed method would turn it back
	// into "drop everything" -- which is the state the two backgrounded
	// faults were found in.
	_ app.BackgroundCmdFilter = (*Workspace)(nil)
)
