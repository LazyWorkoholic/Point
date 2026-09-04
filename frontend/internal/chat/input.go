// F4's query input: a real editing surface, not a text field.
//
// This file is the width-INDEPENDENT half — the buffer, the cursor, the
// selection, undo/redo, and word-wise boundaries. input_wrap.go is the
// width-DEPENDENT half (up/down/home/end need to know how the buffer is
// currently wrapped); the split mirrors why render.go's message pipeline
// keeps width out of everything that does not need it.
package chat

import (
	"time"
	"unicode"
)

// inputLine is one wrapped VISUAL line — see input_wrap.go. Declared here
// only because inputBuffer's doc comments reference it; the type itself
// lives with the wrapping code that produces it.

// editKind groups edits for undo coalescing (beginEdit's doc below).
type editKind int

const (
	editNone editKind = iota
	editInsert
	editDelete
	// editOther never coalesces with anything, including itself: a paste, a
	// word-delete, an Enter, or replacing a selection are each their own
	// undo step, however close together two of them land.
	editOther
)

// undoCoalesceGap: two edits of the SAME kind closer together than this
// merge into one undo step — plain typing (or plain backspacing) at normal
// speed collapses into a handful of steps instead of one per keystroke,
// which is what makes ctrl+z feel like undoing a thought rather than
// replaying a keylogger.
const undoCoalesceGap = 700 * time.Millisecond

// undoMaxDepth bounds memory for a very long editing session. Draft-sized
// text makes even 200 full-text snapshots cheap.
const undoMaxDepth = 200

type inputSnapshot struct {
	text   string
	cursor int
}

// inputBuffer is the query input's editing model: a rune buffer, a cursor
// (a RUNE index, never a byte offset — CJK and emoji make byte offsets
// wrong the moment one appears), and an optional selection anchor.
type inputBuffer struct {
	runes  []rune
	cursor int
	anchor int // -1 means no selection

	// trailing is the cursor's AFFINITY at a soft-wrap boundary, where one
	// rune index is two different screen positions: the end of the wrapped
	// line, or the start of the next one. false (the default) means the
	// start of the next line — where a caret continues after wrapping, and
	// where Home/Left/Right/Up/Down all want it. true means the end of the
	// previous line, which is what End means and the ONLY thing that sets
	// it. Without this bit End had nowhere to land: setting the cursor to
	// the line's End index put it at the FIRST letter of the next line
	// (a verification round's Bug 6), and pressing End again walked down
	// another line each time instead of staying put.
	trailing bool

	undo         []inputSnapshot
	redo         []inputSnapshot
	lastEditKind editKind
	lastEditAt   time.Time

	// version bumps on every mutation; wrapVer/wrapWidth/wrapLines memoize
	// the last wrap against it. Without this, ONE frame wrapped the whole
	// buffer three times (splitGrowth's visualLineCount, ensureCursorVisible,
	// and renderInputLines) and every cursor move wrapped it again — O(buffer)
	// per keystroke AND per frame, which is what made a large pasted draft
	// progressively slower until the input pipeline could not keep up.
	version   uint64
	wrapVer   uint64
	wrapWidth int
	wrapLines []inputLine

	// dirtyFrom is the lowest rune index any edit has touched since the
	// cached wrap was built. An edit can only change how the paragraph it
	// lands in (and everything after it) wraps, so lines that end before
	// that paragraph starts are reused verbatim and only the tail is
	// re-wrapped -- see lines().
	dirtyFrom int
}

// lines is the buffer wrap, memoized on (version, width) and re-wrapped
// INCREMENTALLY when only the tail changed.
//
// This is the cost that made a large paste unrecoverable rather than
// merely slow. A full wrap is O(buffer) and it ran once per keystroke,
// because every edit bumps version; measured, that reached ~5ms per
// keystroke at ~16k runes and passed 6ms at ~33k -- app\paste.go's
// pasteBurstGap. Once ONE keystroke costs more than that gap, the next
// pasted character looks too far apart to belong to a paste, the burst
// never activates, and the render suppression that keys off an active
// burst never engages: the paste degrades into thousands of individually
// rendered keystrokes, the console input buffer overflows, and everything
// after the first handful of characters is lost. Bounding this bounds
// that entire feedback loop.
func (b *inputBuffer) lines(width int) []inputLine {
	if width < 1 {
		width = 1
	}
	if b.wrapLines != nil && b.wrapWidth == width && b.wrapVer == b.version {
		return b.wrapLines
	}

	// Reusable prefix: only when the cache is for this same width and the
	// edit landed somewhere after the start of the buffer.
	if b.wrapLines != nil && b.wrapWidth == width && b.dirtyFrom > 0 {
		from := paragraphStart(b.runes, b.dirtyFrom)
		keep := 0
		for keep < len(b.wrapLines) && b.wrapLines[keep].End < from {
			keep++
		}
		if keep > 0 {
			head := b.wrapLines[:keep:keep]
			b.wrapLines = append(head, wrapFrom(b.runes, from, width)...)
			b.wrapWidth = width
			b.wrapVer = b.version
			b.dirtyFrom = len(b.runes)
			return b.wrapLines
		}
	}

	b.wrapLines = wrapInputRunes(b.runes, width)
	b.wrapWidth = width
	b.wrapVer = b.version
	b.dirtyFrom = len(b.runes)
	return b.wrapLines
}

// markDirty records the lowest index an edit touched, for lines().
func (b *inputBuffer) markDirty(from int) {
	if b.wrapVer != b.version || from < b.dirtyFrom {
		b.dirtyFrom = from
	}
}

func newInputBuffer() inputBuffer {
	return inputBuffer{anchor: -1}
}

func (b *inputBuffer) text() string { return string(b.runes) }

// setText replaces the buffer wholesale — draft restore (draft.go) and
// Send (input_keys.go) both go through this rather than poking fields
// directly, so the selection is always left in a consistent state.
func (b *inputBuffer) setText(s string, cursor int) {
	b.runes = []rune(s)
	b.dirtyFrom = 0
	b.version++
	b.setCursor(cursor, false)
	b.anchor = -1
	b.undo = nil
	b.redo = nil
	b.lastEditKind = editNone
}

func (b *inputBuffer) hasSelection() bool { return b.anchor >= 0 && b.anchor != b.cursor }

// selectionRange returns the selected span as [lo, hi), or (cursor, cursor)
// when nothing is selected — always safe to slice with.
func (b *inputBuffer) selectionRange() (lo, hi int) {
	if !b.hasSelection() {
		return b.cursor, b.cursor
	}
	if b.anchor < b.cursor {
		return b.anchor, b.cursor
	}
	return b.cursor, b.anchor
}

func (b *inputBuffer) clearSelection() { b.anchor = -1 }

// ClearKeepingUndo empties the buffer as an ordinary, UNDOABLE edit — unlike
// setText, which is a hard reset for a genuine context change (draft
// restore on a session switch) and deliberately wipes undo/redo history
// with it. onSend uses this one: whatever fires Enter — a deliberate send,
// or a spurious keystroke reaching it some other way — a stray Ctrl+Z
// immediately after gets the text back, rather than it being gone for
// good. Cheap insurance against exactly the class of bug a real send
// (F5) would otherwise make permanent.
func (b *inputBuffer) ClearKeepingUndo() {
	if len(b.runes) == 0 {
		return
	}
	b.beginEdit(editOther)
	b.splice(0, len(b.runes), nil)
}

func (b *inputBuffer) SelectAll() {
	if len(b.runes) == 0 {
		return
	}
	b.anchor = 0
	b.setCursor(len(b.runes), false)
}

// ---------------------------------------------------------------------------
// Undo / redo
// ---------------------------------------------------------------------------

// beginEdit records an undo checkpoint unless this edit coalesces with the
// immediately preceding one: same kind, and within undoCoalesceGap.
// editOther never coalesces, so a paste/word-delete/selection-replace is
// always its own step even if it lands inside another group's time window.
func (b *inputBuffer) beginEdit(kind editKind) {
	now := time.Now()
	coalesce := kind != editOther && kind == b.lastEditKind && now.Sub(b.lastEditAt) < undoCoalesceGap
	if !coalesce {
		b.pushUndo()
	}
	b.lastEditKind = kind
	b.lastEditAt = now
}

func (b *inputBuffer) pushUndo() {
	b.undo = append(b.undo, inputSnapshot{text: string(b.runes), cursor: b.cursor})
	if len(b.undo) > undoMaxDepth {
		b.undo = b.undo[len(b.undo)-undoMaxDepth:]
	}
	b.redo = nil
}

func (b *inputBuffer) Undo() {
	if len(b.undo) == 0 {
		return
	}
	cur := inputSnapshot{text: string(b.runes), cursor: b.cursor}
	snap := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.redo = append(b.redo, cur)
	b.runes = []rune(snap.text)
	b.dirtyFrom = 0
	b.version++
	b.setCursor(snap.cursor, false)
	b.clearSelection()
	b.lastEditKind = editNone
}

func (b *inputBuffer) Redo() {
	if len(b.redo) == 0 {
		return
	}
	cur := inputSnapshot{text: string(b.runes), cursor: b.cursor}
	snap := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	b.undo = append(b.undo, cur)
	b.runes = []rune(snap.text)
	b.dirtyFrom = 0
	b.version++
	b.setCursor(snap.cursor, false)
	b.clearSelection()
	b.lastEditKind = editNone
}

// ---------------------------------------------------------------------------
// Mutation
// ---------------------------------------------------------------------------

// splice is the one place the buffer is actually rewritten: replace
// [lo, hi) with repl, and leave the cursor at the end of what was inserted.
// No undo bookkeeping here — every caller has already called beginEdit.
func (b *inputBuffer) splice(lo, hi int, repl []rune) {
	lo = clampInt(lo, 0, len(b.runes))
	hi = clampInt(hi, lo, len(b.runes))
	out := make([]rune, 0, lo+len(repl)+(len(b.runes)-hi))
	out = append(out, b.runes[:lo]...)
	out = append(out, repl...)
	out = append(out, b.runes[hi:]...)
	b.runes = out
	b.markDirty(lo)
	b.version++
	b.setCursor(lo+len(repl), false)
	b.clearSelection()
}

// deleteSelectionAsEdit removes the current selection as its own undo step
// and reports whether there was one. Backspace, Delete and DeleteWordLeft
// all call this first: deleting a selection is one undo step on its own,
// never folded into whatever comes next or coalesced with a run of plain
// deletes.
func (b *inputBuffer) deleteSelectionAsEdit() bool {
	if !b.hasSelection() {
		return false
	}
	b.beginEdit(editOther)
	lo, hi := b.selectionRange()
	b.splice(lo, hi, nil)
	return true
}

// filterInsertable drops every rune that is not real typed/pasted content.
//
// Traced from a real bug report: bubbletea's Windows console reader
// (key_windows.go's keyType) has no named case for a BARE modifier keydown
// or keyup — Ctrl, Alt, the Windows key, Caps Lock, pressed or released on
// their own, with no following character — and its final fallback for an
// unrecognised key is `return KeyRunes`, with the KeyMsg's Runes set to
// e.Char VERBATIM. For one of these events e.Char is 0. The result is a
// KeyMsg that looks exactly like an ordinary typed character to everything
// downstream — `pasteable()` (app\paste.go) accepts it into a paste burst,
// and this package's own key handling accepts it as typed text — so a
// stray NUL rune lands in the buffer, invisible on screen, corrupting the
// persisted draft (a real state.json showed three of them, JSON-escaped,
// mixed into otherwise ordinary typed text) and throwing off every
// position downstream of it.
//
// The fix is at the one place ALL insertion funnels through: keep '\n' and
// '\t' (a real newline or a pasted tab must never be silently dropped —
// tab-indented pasted code is common), keep anything unicode.IsPrint
// accepts (letters, marks, numbers, punctuation, symbols, the ASCII
// space — which is exactly what excludes NUL and every other C0/C1
// control code), drop everything else.
func filterInsertable(s string) string {
	if s == "" {
		return s
	}
	clean := make([]rune, 0, len(s))
	dropped := false
	for _, r := range s {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			clean = append(clean, r)
		} else {
			dropped = true
		}
	}
	if !dropped {
		return s
	}
	return string(clean)
}

// InsertText inserts s at the cursor, or in place of the current selection.
// Typing over a selection is deliberately ONE undo step (delete + insert
// together), which is also what makes a 40-line paste one step: paste.go
// upstream already collapsed the paste into a single PasteMsg, and this is
// where that discipline is preserved into undo history.
//
// s is filtered through filterInsertable first — see that function's doc.
// This is the SINGLE chokepoint every insertion path goes through
// (keystrokes via input_keys.go, pastes via handleInputPaste, draft
// restore does NOT go through here — see setText), so it is where a stray
// control character gets stopped regardless of how it arrived.
func (b *inputBuffer) InsertText(s string) {
	s = filterInsertable(s)
	if s == "" {
		return
	}
	repl := []rune(s)
	if b.hasSelection() {
		b.beginEdit(editOther)
		lo, hi := b.selectionRange()
		b.splice(lo, hi, repl)
		return
	}
	kind := editInsert
	if len(repl) > 1 {
		// A multi-rune insert (a paste) is never folded into a run of
		// single-character typing on either side of it.
		kind = editOther
	}
	b.beginEdit(kind)
	b.splice(b.cursor, b.cursor, repl)
}

func (b *inputBuffer) Backspace() {
	if b.deleteSelectionAsEdit() {
		return
	}
	if b.cursor == 0 {
		return
	}
	b.beginEdit(editDelete)
	b.splice(b.cursor-1, b.cursor, nil)
}

func (b *inputBuffer) Delete() {
	if b.deleteSelectionAsEdit() {
		return
	}
	if b.cursor >= len(b.runes) {
		return
	}
	b.beginEdit(editDelete)
	b.splice(b.cursor, b.cursor+1, nil)
}

// DeleteWordLeft is ctrl+backspace's operation. It is reachable from the key
// handler through alt+backspace too — see input_keys.go's doc comment on
// why plain ctrl+backspace cannot be bound on this platform.
func (b *inputBuffer) DeleteWordLeft() {
	if b.deleteSelectionAsEdit() {
		return
	}
	if b.cursor == 0 {
		return
	}
	b.beginEdit(editOther)
	start := wordLeftBoundary(b.runes, b.cursor)
	b.splice(start, b.cursor, nil)
}

// ---------------------------------------------------------------------------
// Movement — width-independent (rune-wise and word-wise). Up/Down/Home/End
// live in input_wrap.go: they need to know how the buffer is wrapped.
// ---------------------------------------------------------------------------

// beginMove sets up shift-selection: the FIRST shift-move from a non-
// selecting state anchors at the cursor's current position, and every
// following shift-move keeps that anchor. A move without shift always
// collapses any selection to a caret, matching every mainstream editor.
// Any movement — shifted or not — ends undo coalescing, so a run of typing
// interrupted by an arrow key does not silently absorb what comes after it.
//
// It does NOT touch cursor or trailing — setCursor, below, is the only
// place either is ever written, and every movement function calls it
// exactly once, last, with both arguments given explicitly. That is the
// fix for Bug 6/11/13, which were the same omission at three different
// call sites: Home/End, then Up/Down, then Left/Right each independently
// forgot to say what affinity a move should leave the cursor with, because
// nothing forced the question to be asked. A function that computes a new
// cursor position and does not ALSO decide its affinity now fails to
// compile, not fails a manual test months later.
func (b *inputBuffer) beginMove(extend bool) {
	if extend {
		if b.anchor < 0 {
			b.anchor = b.cursor
		}
	} else {
		b.clearSelection()
	}
	b.lastEditKind = editNone
}

// setCursor is the ONLY place b.cursor and b.trailing are ever assigned
// together after construction — see beginMove's doc for why that is the
// point.
func (b *inputBuffer) setCursor(pos int, trailing bool) {
	b.cursor = clampInt(pos, 0, len(b.runes))
	b.trailing = trailing
}

// MoveLeft lives in input_wrap.go now, not here — Bug 15 gave it a width
// dependency Bug 13 did not (see that file's doc on lineContaining).

func (b *inputBuffer) MoveWordLeft(extend bool) {
	b.beginMove(extend)
	b.setCursor(wordLeftBoundary(b.runes, b.cursor), false)
}

func (b *inputBuffer) MoveWordRight(extend bool) {
	b.beginMove(extend)
	b.setCursor(wordRightBoundary(b.runes, b.cursor), false)
}

// isWordRune classifies a rune for word-wise movement: letters, digits and
// underscore are "word" runes; everything else non-space is "punctuation".
// The boundary between the two classes counts as a word edge too, so
// `foo.bar` stops at the dot rather than jumping straight from `foo` to
// `bar` — the same convention most editors use for ctrl+arrow.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func wordLeftBoundary(runes []rune, from int) int {
	i := from
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	if i > 0 {
		word := isWordRune(runes[i-1])
		for i > 0 && !unicode.IsSpace(runes[i-1]) && isWordRune(runes[i-1]) == word {
			i--
		}
	}
	return i
}

func wordRightBoundary(runes []rune, from int) int {
	n := len(runes)
	i := from
	for i < n && unicode.IsSpace(runes[i]) {
		i++
	}
	if i < n {
		word := isWordRune(runes[i])
		for i < n && !unicode.IsSpace(runes[i]) && isWordRune(runes[i]) == word {
			i++
		}
	}
	return i
}
