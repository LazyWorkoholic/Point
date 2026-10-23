package editor

import (
	"fmt"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// StatusSegments is app.StatusContributor: the left half of the status
// bar, as LAYOUT.md's Editor line specifies it.
//
//	task.py  12:4  python  CRLF  lock: you   ^P ^W ^E tree
//
// Parts, not a formatted string, because Root puts them through fitHints,
// which drops whole parts and appends "+N" rather than cutting one in half.
//
// # The lock segment, added by F13b
//
// F13a shipped every segment except `lock state`, deliberately: nothing
// acquired a lock, and a lock readout with no lock behind it is the
// hint-that-lies this project refuses (F04's Bug 12). The lock
// lifecycle landed with the save path, so the segment is here now --
// and it still renders NOTHING in the one case F13a was worried about,
// a document that cannot take a lock at all. See lockSegment.
//
// `^E tree` is `^O tree` instead. ctrl+e has been chat.queue.edit's chord
// since F5, and because dispatchKey SWALLOWS a chord whose When predicate
// is false (status line, no fall-through), pressing ctrl+e in the Editor
// prints "Edit Queued Message is not available right now" rather than
// reaching this workspace at all. KeyCapturer could claim it, but this
// episode's claim list is deliberately two keys long, so the tree gets a
// free chord instead of a contested one.
// workspaceHint names the leader's PRIMARY key only -- "F2 w workspace".
//
// It said "ctrl+w w workspace" until F15's close, which was the legacy
// binding and the one that cannot be relied on (actions.Leader's own doc
// has the mechanism): the status bar was advertising the broken half of a
// dual binding as if it were the only one.
//
// Primary only, NOT actions.LeaderLabel's "F2 / ctrl+w". The help screen
// and the palette print both, because they are reference surfaces and
// neither key should be a secret there. This is the status bar, where
// every other segment is one terse binding ("ctrl+p palette", "F7 close")
// and Root's fitHints drops WHOLE segments when the row is too narrow --
// so a segment five cells wider is a segment that disappears sooner, and
// what disappears here is the only on-screen pointer to the workspace
// switcher. Built from actions.Leader rather than typed out, so a change
// to the leader key cannot leave this naming the old one.
var workspaceHint = actions.KeyLabel(actions.Leader) + " w workspace"

func (w *Workspace) StatusSegments() []string {
	if len(w.docs) == 0 {
		if w.projectID == "" {
			return []string{"no project", "ctrl+p palette", workspaceHint}
		}
		return []string{"no file open", ctrlOHint, "F5 re-index",
			"ctrl+p palette", workspaceHint}
	}

	d := w.docs[w.active]
	segs := make([]string, 0, 8)

	name := d.rel
	if name == "" {
		name = d.name()
	}
	segs = append(segs, name)
	// Line and column are 1-based and the column is a DISPLAY column, not
	// a rune index: it is the number a compiler error or a linter reports,
	// and a rune index would disagree with both the moment a tab appears.
	segs = append(segs, fmt.Sprintf("%d:%d",
		d.cursor.Line+1, displayCol(d.buf.Runes(d.cursor.Line), d.cursor.Col)+1))

	if d.lang != "" {
		segs = append(segs, d.lang)
	} else {
		segs = append(segs, "plain")
	}

	segs = append(segs, lineEndingLabel(d))

	if seg := lockSegment(d); seg != "" {
		segs = append(segs, seg)
	}

	if d.bom {
		segs = append(segs, "BOM")
	}
	if d.indent.tabs {
		segs = append(segs, "tabs")
	} else {
		segs = append(segs, fmt.Sprintf("%dsp", d.indent.width))
	}
	if !d.trailingNewline {
		// Worth a segment: a missing final newline is a one-byte diff
		// that every linter in this repo notices and no editor shows by
		// default, and 18b's save is what has to preserve it.
		segs = append(segs, "no eol")
	}
	if d.readOnly {
		segs = append(segs, "read-only ("+string(d.roReason)+")")
	}
	if d.saving {
		// A write in flight is a real state and a short one. Shown
		// because a save that is slow -- a large file, a busy disk --
		// otherwise looks like ctrl+s did nothing.
		segs = append(segs, "saving…")
	} else if d.dirty {
		segs = append(segs, "• modified")
	}
	if d.autosaveBlocked && w.autosaveMode == config.AutosaveAfterDelay {
		// Said out loud. The whole promise of autosave is that unsaved
		// work does not stay unsaved, and a file where that promise is
		// suspended must not look like a file where it is being kept.
		segs = append(segs, "autosave stopped")
	}

	// F15: a live invocation, for `d.saving`'s own reason one surface
	// over -- an explain whose panel has been dismissed, or a fill whose
	// ghost anchor has scrolled out of view, is still running and would
	// otherwise be invisible. It also tells a user who switched back from
	// Chat that the thing they left is still going.
	//
	// Not per-document: the invocation is the workspace's (one at a time),
	// and it names its own file.
	if w.liveInvocation() {
		segs = append(segs, w.inv.kind.String()+"ing "+w.inv.name+"… esc cancels")
	}

	// f7 goes LAST so fitHints -- which drops whole parts from the end
	// -- sheds it first on a narrow terminal. Discoverable when there is
	// room, never at the cost of a segment that describes the file.
	segs = append(segs, ctrlOHint, "ctrl+p palette", "F7 close")
	return segs
}

// lockSegment is LAYOUT.md's `lock state`.
//
//	lock: you                     this process holds it
//	held by aryan (12s ago)       someone else does, and is alive
//	held by aryan (4m ago) · f6 takes over
//	                              someone else does, and has gone quiet
//
// Empty for a document that cannot hold a lock at all -- no project
// above it, or no holder key for this session. That is F13a's reasoning
// preserved rather than overridden: for those files there is no lock
// state, and "lock: none" would be a segment that exists to say nothing.
//
// `lock: you` is shown rather than omitted even though it is the normal
// case, because LAYOUT.md's line puts it there and because its ABSENCE
// is then meaningful: a file with no lock segment is one Point could not
// lock, which is exactly when a user needs to know.
func lockSegment(d *doc) string {
	switch d.lock {
	case lockHeld:
		return "lock: you"
	case lockPending:
		return "lock: …"
	case lockHeldByOther:
		if d.lockedAge >= lockStaleAfter.Seconds() {
			return d.lockLabel() + " · f6 takes over"
		}
		return d.lockLabel()
	default:
		return ""
	}
}

// ctrlOHint is the ctrl+o hint, STATIC, everywhere it appears.
//
// It names both targets and does not change with the current focus,
// deliberately. A label that flipped between "tree" and "file"
// depending on which pane held the keyboard would be a third place
// focus state has to be tracked correctly -- and this episode has
// already shipped that bug twice: the stored focus flag that drifted
// the moment a document arrived by a path which forgot to update it,
// and the "closed <file>" note that was rendered by a surface with no
// expiry. A fixed string needs no tracking, so it cannot drift.
//
// It reads as a destination pair rather than an action for the same
// reason the rest of the status line does: these segments go through
// fitHints, which drops whole parts, so each one has to be legible on
// its own.
const ctrlOHint = "ctrl+o tree/file"

// lineEndingLabel is B32's line_ending, displayed and never re-derived.
//
// `MIXED` is shown verbatim rather than resolved to a majority, and that
// is the display half of this episode's mixed-file decision. B32 reports
// crlf_count and lf_count and deliberately stops there, noting that a
// mixed file is the one case that cannot round-trip byte-exact: any single
// line ending on the way back rewrites the minority endings. So the file
// opens normally editable and the status bar says MIXED, because the one
// thing it must not do is claim CRLF or LF for a file that is neither.
// Which ending a save WRITES is 18b's, once there is a save.
func lineEndingLabel(d *doc) string {
	switch d.lineEnding {
	case lineEndingCRLF:
		return "CRLF"
	case lineEndingLF:
		return "LF"
	case lineEndingMixed:
		return fmt.Sprintf("MIXED %d/%d", d.crlfCount, d.lfCount)
	default:
		// Not dead: it is the forward-compatible path. describe_bytes
		// returns exactly three values today, and a fourth from a later
		// backend renders as itself rather than as a guess or a blank.
		return d.lineEnding
	}
}

// DirtyBufferCount is app.DirtyBuffers, declared by F12 and unimplemented
// until now.
//
// It gates two things: leader → u's confirmation, which F12 built, and the
// idle sweep, which F13a taught to skip a dirty workspace rather than
// discard it silently (app\lifecycle.go's walkIdle).
//
// F13b made it fire far less often and did not make it unnecessary.
// With autosave on, a buffer is dirty for about a second per edit; with
// `autosave = "off"`, or after a refusal that stopped autosave for a
// file (locked, read-only, stale), it stays dirty indefinitely. The
// skip is what stands between that state and a silent loss, so it is
// still load-bearing -- just no longer the only thing holding the line.
func (w *Workspace) DirtyBufferCount() int {
	n := 0
	for _, d := range w.docs {
		if d.dirty {
			n++
		}
	}
	return n
}
