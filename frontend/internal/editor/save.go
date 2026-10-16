package editor

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The save path: WriteProjectFile, and what each of B32's seven refusal
// slugs does here.
//
// # The frontend writes nothing
//
// This file sends an event and reads a reply. CLAUDE.md's first hard rule
// bans a write from an AGENT path, and B32's Decision 1 went further and
// put every project write in the backend regardless -- pathguard has
// exactly one implementation on purpose, and project_idea.md §12 states
// editor-write permission as backend-enforced policy so a B16 guest can
// be refused. config\state.go stays the only file this process writes.
//
// # Nothing is re-derived
//
// line_ending, bom and trailing_newline all come from doc, which took
// them verbatim from the read, and expected_mtime is the mtime that read
// reported -- never a fresh stat, which would reopen the exact race the
// precondition exists to close. The one value computed here is which
// ending a MIXED file is written with, which B32 deliberately left to
// this side (see writeEnding).

// saveReason is why a write is being attempted. The two differ in exactly
// two places and it is worth naming rather than threading a bool: what a
// refusal DOES (a dialog, or one quiet line), and whether an un-saveable
// document is attempted at all.
type saveReason int

const (
	saveManual saveReason = iota
	saveAuto
)

// saveActive is ctrl+s.
func (w *Workspace) saveActive() tea.Cmd {
	if len(w.docs) == 0 {
		return nil
	}
	return w.saveDoc(w.docs[w.active], saveManual, false)
}

// saveDoc attempts one write.
//
// overwrite is `true` ONLY when the user has pressed [overwrite] on the
// stale dialog. B32's handoff is explicit that it "is the one field in
// this payload that can destroy someone else's work", so it is a
// parameter with exactly one non-default caller rather than anything
// derived from state.
func (w *Workspace) saveDoc(d *doc, reason saveReason, overwrite bool) tea.Cmd {
	if d == nil {
		return nil
	}

	// A manual save is also the user saying "try again": whatever stopped
	// autosave, they have now had a chance to see it and act.
	if reason == saveManual {
		d.autosaveBlocked = false
	}

	// Rule 3: never autosave a buffer with no path. Unreachable today --
	// every document comes from a ReadProjectFile reply that echoes an
	// absolute path -- and kept because F16's "create a new file" item is
	// the surface that introduces one, and it will introduce it here.
	if d.path == "" {
		if reason == saveManual {
			return w.say("this buffer has no file to save to")
		}
		return nil
	}

	if d.readOnly {
		if reason == saveManual {
			return w.say(d.name() + " is read-only — " + string(d.roReason))
		}
		// Rule 1. Silent, and blocked: the reason cannot change without
		// the file being reopened, so re-attempting once a second would
		// be a refusal per second for the rest of the session.
		d.autosaveBlocked = true
		return nil
	}

	if d.lock == lockHeldByOther {
		if reason == saveManual {
			return w.say(d.name() + " is " + d.lockLabel() + " — f6 takes it over once the holder goes quiet")
		}
		// Rule 1 and Done-when 11, which is the same rule stated twice:
		// autosave must not fire on a file held by another holder AND
		// must not spam refusals. Blocked rather than merely skipped so
		// it also stops firing after a `locked` refusal that arrived
		// from a write (the lock can be taken between the open and the
		// save, which is the case a pre-check alone would miss).
		d.autosaveBlocked = true
		return nil
	}

	if reason == saveAuto && !d.dirty {
		return nil
	}

	if d.saving {
		// A write is already in flight. Record that the buffer has moved
		// since it went out, so the reply does not clear the dirty
		// marker on content that is no longer what is on disk.
		d.savedWhileSaving = true
		return nil
	}

	if w.holder == "" {
		// WriteProjectFile refuses an empty holder with an Error
		// envelope, so sending one would turn a configuration problem
		// into a wire error per save. Said once, here, in terms of what
		// it means.
		if reason == saveManual {
			return w.say("no lock identity for this session — saving is disabled")
		}
		d.autosaveBlocked = true
		return nil
	}

	ending := writeEnding(d)
	payload := map[string]any{
		"path":             d.path,
		"content":          d.buf.Text(),
		"holder":           w.holder,
		"expected_mtime":   d.mtime,
		"overwrite":        overwrite,
		"line_ending":      ending,
		"bom":              d.bom,
		"trailing_newline": d.trailingNewline,
	}
	// Same rule openPath uses: name the project explicitly when the file
	// is inside the tree's own, and otherwise let _require_project walk
	// up from the file's own path. A file in a different project saves
	// correctly scoped either way.
	if w.projectID != "" && relativeTo(w.projectRoot, d.path) != "" {
		payload["project_id"] = w.projectID
	}

	d.saving = true
	d.savedWhileSaving = false
	d.manualSave = reason == saveManual
	w.send(protocol.CmdWriteProjectFile, queryWriteFile, payload)
	return nil
}

// writeEnding is C4: which line ending a save actually writes.
//
// A uniform file writes back what it read -- that is the whole of
// Done-when 2, and the reason the read reports it at all.
//
// A MIXED file is the case B32 deliberately left open ("counting bytes is
// resolution, choosing a normalisation is a product decision, and that
// one is F13's"). It cannot round-trip byte-exact by construction: any
// single ending on the way back rewrites the minority endings. So the
// choice is which minority to rewrite, and the answer is the MAJORITY
// ending, with LF on an exact tie.
//
// The tie half matters more than it looks. describe_bytes resolves its
// own ambiguous case -- a file with no line breaks at all -- to "lf", so
// LF is already the backend's answer for "nothing here says otherwise".
// Writing CRLF on a tie would make the read and the write disagree about
// the one input they would each have to resolve on their own, and that
// disagreement would show up as a file that changes ending every time it
// is saved. Writing LF keeps the two sides answering the same way.
//
// encode_for_write ACCEPTS ONLY "lf" or "crlf" and raises
// InvalidEventError on anything else, so passing "mixed" through would
// arrive as an Error envelope rather than a saved file. Resolving here is
// not a nicety; it is the contract.
func writeEnding(d *doc) string {
	switch d.lineEnding {
	case lineEndingCRLF:
		return lineEndingCRLF
	case lineEndingLF:
		return lineEndingLF
	}
	if d.crlfCount > d.lfCount {
		return lineEndingCRLF
	}
	return lineEndingLF
}

// wireWritten mirrors WriteResult.to_dict() field for field, both
// branches of it. to_dict emits only the fields that mean something for
// the outcome it reports, so a field's absence is information: a nil
// Holder on a `stale` refusal is not a missing holder, it is a refusal
// that has nothing to do with one.
type wireWritten struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`

	Path            string  `json:"path"`
	Mtime           float64 `json:"mtime"`
	Size            int     `json:"size"`
	LineEnding      string  `json:"line_ending"`
	Bom             bool    `json:"bom"`
	TrailingNewline bool    `json:"trailing_newline"`

	Holder     string  `json:"holder"`
	AgeSeconds float64 `json:"age_seconds"`
	LimitKB    int     `json:"limit_kb"`
	Message    string  `json:"message"`
}

func (w *Workspace) onWritten(req pendingReq, env protocol.Envelope) tea.Cmd {
	var p wireWritten
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a ProjectFileWritten reply", "err", err)
		return nil
	}
	// Correlated on the request id, not on p.Path: a refusal reports no
	// path at all, and the document may have been closed since.
	d := w.docByPath(req.path)
	if d == nil {
		return nil
	}
	d.saving = false

	if p.OK {
		return w.onWriteSucceeded(d, p)
	}
	return w.onWriteRefused(d, p)
}

func (w *Workspace) onWriteSucceeded(d *doc, p wireWritten) tea.Cmd {
	// Autosave rule 2, and the reason no CheckFileStale follows: the new
	// mtime comes back in the reply, so the NEXT save's expected_mtime
	// already matches what is on disk and Point's own write cannot trip
	// its own "changed on disk" check. That is Done-when 12, satisfied
	// by this one line rather than by a second round trip.
	d.mtime = p.Mtime
	d.size = p.Size
	d.bom = p.Bom
	d.trailingNewline = p.TrailingNewline
	d.autosaveBlocked = false

	// The file on disk is now uniform, whatever it was before, so a
	// status bar still reading MIXED would be describing the previous
	// version of the file.
	//
	// This is the ONE piece of B32 metadata this package derives rather
	// than carries, and it is sound for a narrow reason: the write just
	// expanded every newline to one ending, so the counts are not a
	// guess about unseen bytes -- they are a property of what was sent.
	// WriteResult.to_dict echoes line_ending but not the counts, so
	// there is nothing to carry.
	d.lineEnding = p.LineEnding
	d.crlfCount, d.lfCount = 0, 0
	n := d.buf.Lines() - 1
	if d.trailingNewline {
		n++
	}
	if n < 0 {
		n = 0
	}
	if d.lineEnding == lineEndingCRLF {
		d.crlfCount = n
	} else {
		d.lfCount = n
	}

	if d.savedWhileSaving {
		// The buffer moved while the write was in flight, so what is on
		// disk is already behind it. The marker stays, and the pending
		// autosave tick (or the next keystroke's) catches up.
		d.savedWhileSaving = false
		return nil
	}
	d.dirty = false
	return w.say("saved " + d.name())
}

// onWriteRefused renders each of B32's seven slugs.
//
// Only `stale` opens a dialog, and only for a manual save. Everything
// else is one status line plus, where the condition cannot resolve
// itself, a stop on autosave -- the difference between "tell the user
// once" and "tell the user once a second", which is what Done-when 11 is
// really about.
func (w *Workspace) onWriteRefused(d *doc, p wireWritten) tea.Cmd {
	switch p.Reason {
	case "stale":
		return w.onStaleRefusal(d, p)

	case "locked":
		d.lock = lockHeldByOther
		d.lockedBy = p.Holder
		d.lockedAge = p.AgeSeconds
		d.autosaveBlocked = true
		return w.say(d.name() + " is " + d.lockLabel() + " — f6 takes it over once the holder goes quiet")

	case "read_only":
		// The read reported `writable: true` and the attribute has been
		// set since. Recorded on the document so the status bar and
		// every later save agree, rather than being a one-off message.
		d.readOnly, d.roReason = true, roNotWritable
		d.autosaveBlocked = true
		return w.say(d.name() + " is read-only on disk — nothing was written")

	case "too_large":
		// The limit is reported rather than held as a constant on this
		// side: editor.max_file_kb is the backend's number and a second
		// copy here could disagree with the one doing the refusing.
		d.autosaveBlocked = true
		return w.say(fmt.Sprintf("%s is %d bytes — over the %dKB editor limit, nothing was written",
			d.name(), p.Size, p.LimitKB))

	case "no_parent_dir":
		d.autosaveBlocked = true
		return w.say(d.name() + "'s folder no longer exists — nothing was written")

	case "exists":
		// Only reachable with expected_mtime null, which this episode
		// never sends: every document was read from a file that existed.
		// Kept because F16's "create a new file" is the surface that
		// will send it, and a slug that renders as nothing is worse than
		// one that renders as itself.
		d.autosaveBlocked = true
		return w.say(d.name() + " already exists — nothing was written")

	case "io_error":
		// NOT blocked. Unlike every other refusal here this one can
		// succeed on the next attempt without anything changing on the
		// user's side -- B32's own measured case is another process
		// holding the handle open during os.replace, which ends when
		// that process does.
		return w.say("could not write " + d.name() + " — " + p.Message)

	default:
		// A slug this build does not know renders as itself. The
		// forward-compatible path, same call lineEndingLabel makes.
		d.autosaveBlocked = true
		return w.say("saving " + d.name() + " was refused: " + p.Reason)
	}
}

// lockLabel is the status-bar and message form of "someone else has it".
func (d *doc) lockLabel() string {
	who := config.HolderDisplay(d.lockedBy)
	if who == "" {
		who = "another holder"
	}
	return fmt.Sprintf("held by %s (%s ago)", who, ageLabel(d.lockedAge))
}

// ageLabel renders a heartbeat age the way F13's own spec writes it:
// "held by aryan (12s ago)".
//
// Whole seconds up to a minute, then minutes -- a lock older than 60
// seconds is stale and take-over-able, so the exact number stops
// mattering at precisely the point the units change.
func ageLabel(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", int(sec))
	}
	return fmt.Sprintf("%dm", int(sec)/60)
}
