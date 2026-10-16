package editor

import (
	"strings"
	"time"
	"unicode"
)

// Undo, and why it records splices rather than snapshots.
//
// chat\input.go's undo stack stores a full copy of the text per step, which
// is right there: a draft is a few hundred runes and 200 copies of it cost
// nothing. The same shape here would copy the whole file on every
// keystroke -- 95KB per character typed in F13's own 5,000-line Done-when
// -- so the cost of undo would scale with the size of the file rather than
// with the size of the edit, which is exactly the thing "no perceptible lag
// while typing" rules out.
//
// So a record is the splice itself: where, what came out, what went in.
// Undo is the same Splice call with the two swapped. Three things fall out
// of that and none of them needed a special case:
//
//   - memory is proportional to what was edited, not to what was opened;
//   - an agent-applied diff is ONE record, because applying it is ONE
//     Splice (see ApplyRegion) -- F13's "without this the diff card is
//     unusable in practice", expressed structurally rather than as a flag
//     somebody has to remember to set;
//   - redo is free, since a record already holds both directions.

// undoCoalesceGap: two edits of the same kind closer together than this may
// merge into one step. chat's number, for chat's reason -- plain typing at
// speed should collapse into a handful of steps rather than one per
// keystroke, so ctrl+z undoes a thought and not a keylogger.
const undoCoalesceGap = 700 * time.Millisecond

// undoMaxDepth bounds the stack. Higher than chat's 200 because a record
// here is a splice (tens of bytes) rather than a copy of the whole text, so
// the budget buys an order of magnitude more history for less memory.
const undoMaxDepth = 2000

type editKind int

const (
	editNone editKind = iota
	editInsert
	editDelete
	// editOther never coalesces with anything, including itself: an
	// Enter, an indent/dedent of a selection, a bracket pair, a replaced
	// selection and an applied diff are each one undo step however close
	// together two of them land.
	editOther
	// editPaste coalesces with an immediately adjacent editPaste, and
	// with nothing else.
	//
	// It exists because ONE physical paste does not always arrive as one
	// message. app\paste.go reconstructs a paste from a burst of console
	// key events and ends the burst after 25ms of quiet, so a long paste
	// delivered in more than one console batch -- or one whose middle
	// straddles a slow frame -- arrives as two or three PasteMsgs. Each
	// was its own undo step, which is why a 300-line paste took two or
	// three ctrl+z presses to take back.
	//
	// Coalescing is safe precisely because the gap is 700ms: two
	// DELIBERATE pastes that land inside the same 700ms, at exactly the
	// position the previous one ended, is not a thing a person can do.
	editPaste
)

// editRecord is one undoable splice, with the caret state either side of
// it.
//
// The caret is part of the record rather than something undo recomputes,
// because where the cursor WAS is not derivable from the text that changed:
// undoing a deletion should restore the selection that was deleted, and
// undoing a paste should put the caret back where the paste started, and
// those are two different answers to the same text transformation.
type editRecord struct {
	from     Pos
	removed  string
	inserted string

	curBefore, anchorBefore Pos
	selBefore               bool
	curAfter, anchorAfter   Pos
	selAfter                bool

	kind editKind
	at   time.Time
}

// history is one document's undo/redo stacks.
type history struct {
	undo []editRecord
	redo []editRecord

	// group is false immediately after anything that must not be merged
	// into -- an undo, a redo, a selection change, a cursor move. The next
	// edit then starts a fresh record even if it would otherwise have
	// coalesced. Without it, moving the caret and typing again extends the
	// step you just finished, so one ctrl+z removes text from two places.
	group bool
}

// breakGroup ends the current coalescing run. Called by every cursor move
// and by save (18b), so the next keystroke starts its own step.
func (h *history) breakGroup() { h.group = false }

// push records an edit, coalescing into the previous one when the rules
// below allow it.
//
// Three conditions, all required:
//
//  1. same kind, and that kind is insert or delete -- editOther never
//     merges;
//  2. within undoCoalesceGap of the previous edit;
//  3. CONTIGUOUS -- an insert must begin exactly where the last one ended,
//     a delete must end exactly where the last one began. This is what
//     makes "type here, click there, type again" two steps without any
//     caret bookkeeping: the positions simply do not meet.
//
// Plus the word rule: a run ends when the next inserted rune STARTS a word
// (a word rune following a non-word one). That is what makes F13's
// Done-when #3 true -- type a sentence, and one ctrl+z takes back "world",
// not "d" -- while leaving the space that preceded it attached to the word
// before, so a second ctrl+z takes back "hello " whole.
func (h *history) push(rec editRecord) {
	rec.at = time.Now()

	if h.group && len(h.undo) > 0 {
		last := &h.undo[len(h.undo)-1]
		if h.canMerge(last, rec) {
			switch rec.kind {
			case editInsert, editPaste:
				last.inserted += rec.inserted
			case editDelete:
				last.from = rec.from
				last.removed = rec.removed + last.removed
			}
			last.curAfter, last.anchorAfter, last.selAfter = rec.curAfter, rec.anchorAfter, rec.selAfter
			last.at = rec.at
			h.redo = nil
			return
		}
	}

	h.undo = append(h.undo, rec)
	if len(h.undo) > undoMaxDepth {
		h.undo = h.undo[len(h.undo)-undoMaxDepth:]
	}
	h.redo = nil
	h.group = true
}

func (h *history) canMerge(last *editRecord, rec editRecord) bool {
	if last.kind != rec.kind {
		return false
	}
	if rec.at.Sub(last.at) > undoCoalesceGap {
		return false
	}
	switch rec.kind {
	case editPaste:
		// Contiguity alone: a paste has no word boundaries to respect,
		// and its first rune very often continues the previous chunk
		// mid-token.
		//
		// Note what is NOT required: that the record being merged into
		// removed nothing. The FIRST chunk of a paste over a selection
		// removes that selection, and the chunks after it have to join
		// it anyway -- otherwise a split paste over a selection is two
		// steps again, which is the whole bug. The combined record is
		// still well formed: it removes what the first chunk removed and
		// inserts the concatenation, both at the first chunk's own
		// position. Only the arriving chunk must be insert-only, which a
		// paste always is once the selection is gone.
		return rec.removed == "" && advance(last.from, last.inserted) == rec.from
	case editInsert:
		if last.removed != "" || rec.removed != "" {
			return false
		}
		if advance(last.from, last.inserted) != rec.from {
			return false
		}
		return !startsWord(last.inserted, rec.inserted)
	case editDelete:
		if last.inserted != "" || rec.inserted != "" {
			return false
		}
		return advance(rec.from, rec.removed) == last.from
	}
	return false
}

// startsWord reports whether next begins a new word relative to prev --
// the boundary that ends a coalescing run.
func startsWord(prev, next string) bool {
	if prev == "" || next == "" {
		return false
	}
	p := []rune(prev)
	n := []rune(next)
	return isWordRune(n[0]) && !isWordRune(p[len(p)-1])
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Undo and Redo are on the document rather than on history, because
// replaying a record means splicing a buffer and moving a caret, and
// history owns neither.

// Undo reverses the most recent record. Reports whether anything happened.
func (d *doc) Undo() bool {
	if len(d.hist.undo) == 0 {
		return false
	}
	rec := d.hist.undo[len(d.hist.undo)-1]
	d.hist.undo = d.hist.undo[:len(d.hist.undo)-1]

	d.buf.Splice(rec.from, advance(rec.from, rec.inserted), rec.removed)
	d.cursor = d.buf.Clamp(rec.curBefore)
	d.anchor = d.buf.Clamp(rec.anchorBefore)
	d.selecting = rec.selBefore
	d.reveal = true

	d.hist.redo = append(d.hist.redo, rec)
	d.hist.breakGroup()
	d.markDirty()
	return true
}

// Redo replays the most recently undone record.
//
// The redo stack is cleared by the next edit (history.push), never here --
// F13's "redo cleared on new input", in the one place that can tell an
// edit from a navigation.
func (d *doc) Redo() bool {
	if len(d.hist.redo) == 0 {
		return false
	}
	rec := d.hist.redo[len(d.hist.redo)-1]
	d.hist.redo = d.hist.redo[:len(d.hist.redo)-1]

	d.buf.Splice(rec.from, advance(rec.from, rec.removed), rec.inserted)
	d.cursor = d.buf.Clamp(rec.curAfter)
	d.anchor = d.buf.Clamp(rec.anchorAfter)
	d.selecting = rec.selAfter
	d.reveal = true

	d.hist.undo = append(d.hist.undo, rec)
	d.hist.breakGroup()
	d.markDirty()
	return true
}

// ApplyRegion replaces the text between two positions in ONE undo step and
// leaves the caret at the end of what it inserted.
//
// This is the entry point F9b's diff card and F15's Explainer apply
// through, and it is the whole of F13's "an agent-applied diff is a single
// undoable operation". Nothing in this session calls it -- there is no
// apply surface in the repo yet, exactly as F09a recorded -- and it is
// built now rather than later because the undo model either supports it by
// construction or has to be rebuilt to, and this one does.
//
// It is also, deliberately, not a write path: it changes a buffer the user
// then saves. CLAUDE.md's first hard rule is intact -- an agent-proposed
// change reaches disk only through a buffer the user saves.
func (d *doc) ApplyRegion(from, to Pos, repl string) {
	if d.readOnly {
		return
	}
	d.spliceAs(editOther, from, to, repl)
}

// spliceAs is the single mutation path: every edit in edit.go funnels
// through it, so there is exactly one place that records history, moves the
// caret and marks the document dirty.
func (d *doc) spliceAs(kind editKind, from, to Pos, repl string) {
	from, to = order(d.buf.Clamp(from), d.buf.Clamp(to))
	removed := d.buf.Slice(from, to)
	if removed == "" && repl == "" {
		return
	}

	rec := editRecord{
		from:         from,
		removed:      removed,
		inserted:     repl,
		curBefore:    d.cursor,
		anchorBefore: d.anchor,
		selBefore:    d.selecting,
		kind:         kind,
	}

	end := d.buf.Splice(from, to, repl)
	d.cursor = end
	d.anchor = end
	d.selecting = false
	d.reveal = true
	// Cleared here, and re-set by maybePair AFTER this returns: any edit
	// that is not the auto-pair itself cancels the pending type-through.
	d.pairedAt = Pos{Line: -1}

	rec.curAfter, rec.anchorAfter, rec.selAfter = d.cursor, d.anchor, d.selecting
	d.hist.push(rec)
	d.markDirty()
}

// setCaretAfterEdit moves the caret and the selection AND corrects the
// record the edit just pushed, so undo and redo restore what the caller
// actually left on screen.
//
// Every caller that adjusts the caret after spliceAs needs this.
// shiftLines, shiftCurrentLine and maybePair all used to assign
// d.cursor/d.anchor/d.selecting directly, AFTER spliceAs had already
// copied the post-splice values into the record -- so the record described
// a caret position that existed for no frame at all. Undoing a block
// indent or a bracket wrap and then redoing it put the caret and the
// selection somewhere the user had never seen, which reads as the
// behaviour being inconsistent rather than as an undo bug.
//
// It touches only the last record, and only if there is one: an edit on a
// read-only document pushes nothing, and a caret adjustment with no record
// behind it is still a legitimate caret adjustment.
func (d *doc) setCaretAfterEdit(cursor, anchor Pos, selecting bool) {
	d.cursor = d.buf.Clamp(cursor)
	d.anchor = d.buf.Clamp(anchor)
	d.selecting = selecting
	d.reveal = true
	if n := len(d.hist.undo); n > 0 {
		rec := &d.hist.undo[n-1]
		rec.curAfter, rec.anchorAfter, rec.selAfter = d.cursor, d.anchor, d.selecting
	}
}

// indentOf is the leading whitespace of a line -- Tier 1 auto-indent's
// whole input (edit.go's Enter case).
func indentOf(runes []rune) string {
	n := 0
	for n < len(runes) && (runes[n] == ' ' || runes[n] == '\t') {
		n++
	}
	return string(runes[:n])
}

// isBlank reports whether a line is empty or whitespace only.
func isBlank(runes []rune) bool {
	return strings.TrimSpace(string(runes)) == ""
}
