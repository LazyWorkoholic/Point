package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// Reload: re-read a file that changed on disk, into a buffer that is
// already open.
//
// Used two ways and the difference matters:
//
//   - [reload] on the stale dialog REPLACES the buffer with what is on
//     disk, in one splice, so one ctrl+z puts the user's edits back. A
//     reload that cleared the undo history would make "reload" the only
//     irreversible button on a dialog whose whole job is to stop
//     irreversible things happening by accident.
//   - [diff] needs the same bytes to compare against, and must NOT
//     touch the buffer at all.
//
// Both go out as the same ReadProjectFile; what distinguishes them is
// where the reply lands, which is `reloadIntent` on the document.

type reloadIntent int

const (
	reloadNone reloadIntent = iota
	// reloadIntoBuffer: [reload] was pressed.
	reloadIntoBuffer
	// reloadForDiff: the dialog is open and wants the disk bytes so
	// [diff] has something to show.
	reloadForDiff
)

// onStaleRefusal is B32's `stale` slug: the file changed on disk since
// the read that gave us expected_mtime.
//
// A MANUAL save opens the three-button dialog. An AUTOSAVE does not --
// a modal appearing a second after you stopped typing, for a change
// somebody else made, is the kind of interruption that makes people
// turn autosave off. It says one line and stops autosaving this
// document until the user acts, which is the same "once, not once a
// second" rule every other refusal here follows. ctrl+s then opens the
// dialog deliberately.
//
// This is not in F13b's doc as written: the doc specifies the refusal
// UI without distinguishing the two callers, because when it was
// written there was no autosave to distinguish. Recorded in the handoff
// as a decision rather than left implicit.
func (w *Workspace) onStaleRefusal(d *doc, p wireWritten) tea.Cmd {
	d.autosaveBlocked = true

	if !w.lastSaveWasManual(d) {
		return w.say(d.name() + " changed on disk — autosave stopped; press ctrl+s to resolve")
	}
	return w.openStaleDialog(d, p.Mtime)
}

// lastSaveWasManual reports how the write that just came back was
// started.
//
// Recorded on the document at send time rather than carried on the
// wire: the reply has no room for it and the backend has no business
// knowing. One field rather than a second pendingReq member because a
// document can only have one write in flight -- `saving` is what
// guarantees that.
func (w *Workspace) lastSaveWasManual(d *doc) bool { return d.manualSave }

// openStaleDialog builds the dialog, asks Root to show it (B9), and
// starts the re-read [diff] will need.
//
// The re-read is issued NOW rather than when [diff] is pressed, for one
// reason worth stating: the content it fetches is evidence about a
// moment -- "what was on disk when the save was refused" -- and fetching
// it later would show a different moment if the file changed again in
// between. The dialog would then be offering [overwrite] against one
// version while showing a diff of another.
func (w *Workspace) openStaleDialog(d *doc, diskMtime float64) tea.Cmd {
	dlg := newStaleDialog(d, diskMtime)
	d.dialog = dlg
	d.reload = reloadForDiff
	w.send(protocol.CmdReadProjectFile, queryReloadFile, w.readPayload(d))

	return func() tea.Msg { return app.OpenWorkspaceOverlayMsg{Overlay: dlg} }
}

func (w *Workspace) readPayload(d *doc) map[string]any {
	p := map[string]any{"path": d.path}
	if w.projectID != "" && relativeTo(w.projectRoot, d.path) != "" {
		p["project_id"] = w.projectID
	}
	return p
}

// onReloaded is the re-read's reply, for either intent.
func (w *Workspace) onReloaded(req pendingReq, env protocol.Envelope) tea.Cmd {
	var p struct {
		File wireFile `json:"file"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a reload reply", "err", err)
		return nil
	}
	d := w.docByPath(req.path)
	if d == nil {
		return nil
	}
	intent := d.reload
	d.reload = reloadNone

	switch intent {
	case reloadForDiff:
		if d.dialog != nil {
			d.dialog.disk = p.File.Content
		}
		return nil

	case reloadIntoBuffer:
		return w.applyReload(d, p.File)
	}
	return nil
}

// applyReload replaces the buffer with the file on disk.
//
// ONE splice over the whole buffer, which is what makes the reload a
// single undo step -- the same property ApplyRegion has and for the same
// structural reason (undo.go: everything that mutates goes through
// Splice, so a record is the splice). Pressing ctrl+z after a reload
// puts your edits back, which is the difference between a reload you can
// change your mind about and a destructive one.
//
// The METADATA is replaced too, and has to be: the file on disk may have
// a different line ending, a BOM it did not have, or a trailing newline
// it lost. Carrying the old values forward would make the next save
// write the new content with the old file's byte-level shape -- which is
// precisely the loss B32 exists to prevent, arriving one indirection
// later.
func (w *Workspace) applyReload(d *doc, f wireFile) tea.Cmd {
	d.spliceAs(editOther, Pos{}, d.buf.End(), f.Content)

	d.mtime = f.Mtime
	d.size = f.Size
	d.lineEnding = f.LineEnding
	d.crlfCount = f.CRLFCount
	d.lfCount = f.LFCount
	d.bom = f.Bom
	d.trailingNewline = f.TrailingNewline

	// The buffer now matches disk, so it is not dirty -- regardless of
	// what the undo history still holds. That history is the user's way
	// back, not a claim about what is on disk.
	d.dirty = false
	d.setDiskBaseline()
	d.autosaveBlocked = false
	// F9d: the buffer is the file again, so whatever agent-applied region
	// it used to hold is gone -- and with it the session a later save
	// would have been scoped by. Left set, the next save of a reloaded
	// buffer would be scoped by a session whose proposal is no longer in
	// it, which is a claim about content that is not there.
	d.agentSession = ""
	d.cursor = d.buf.Clamp(d.cursor)
	d.anchor = d.cursor
	d.selecting = false
	d.reveal = true
	d.hl = newHighlighter(f.Path, d.lineEndingHint())
	d.lang = d.hl.name

	return w.say("reloaded " + d.name() + " from disk — ctrl+z puts your edits back")
}

// onStaleReload is [reload]'s message, handled in the workspace rather
// than in the overlay's own Update for the reason stale.go's header
// gives: an Overlay's Update runs inside Root's, and a callback firing
// there would be mutating a workspace from code that does not own it.
func (w *Workspace) onStaleReload(m staleReloadMsg) tea.Cmd {
	d := w.docByPath(m.path)
	if d == nil {
		return nil
	}
	d.dialog = nil
	d.reload = reloadIntoBuffer
	w.send(protocol.CmdReadProjectFile, queryReloadFile, w.readPayload(d))
	return nil
}

// onStaleOverwrite is [overwrite]: the one call in this package that
// sends `overwrite: true`.
//
// B32's handoff calls that field "the one in this payload that can
// destroy someone else's work", and it reaches the wire from exactly
// here -- a message that only the dialog's own button produces. There is
// no state, no config and no heuristic that can set it.
func (w *Workspace) onStaleOverwrite(m staleOverwriteMsg) tea.Cmd {
	d := w.docByPath(m.path)
	if d == nil {
		return nil
	}
	d.dialog = nil
	return w.saveDoc(d, saveManual, true)
}
