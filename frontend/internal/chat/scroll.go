package chat

import (
	tea "github.com/charmbracelet/bubbletea"
)

// gapLines is Design Change C's exchange spacing: 1 blank row between a
// user question and its own reply, 2 between a reply and the next
// question, 1 for anything else (a system/error/tool message involved, or
// two messages of the same role back to back) — not specified, and 1 is the
// least surprising default. Only ever called for an entry that is not the
// first one in the transcript; the first entry never gets a leading gap.
func gapLines(prevRole, role string) int {
	switch {
	case prevRole == roleUser && role == roleAssistant:
		return 1
	case prevRole == roleAssistant && role == roleUser:
		return 2
	default:
		return 1
	}
}

// The scroll position is a ROW, expressed as (entry, row-within-entry).
//
// F5's first cut scrolled by ENTRY: j and k moved topAbs by one whole
// message, so a reply taller than the viewport could not be scrolled
// THROUGH — everything below its first screenful was unreachable. Scrolling
// is row-based here, over the flattened rows of all three sources as one
// sequence, which is what F3 did over its single flat list.
//
// The position stays a PAIR rather than one absolute row number on purpose:
// an absolute row would have to be recomputed against every entry's height
// on every render, which is O(all entries) and throws away F3's "never more
// than one screenful is ever measured" property. Anchoring to an entry and
// an offset inside it keeps each scroll step O(1) and each render O(rows),
// at 10,000 messages the same as at ten.

// entryBlock is one entry's rows INCLUDING the blank gap that precedes it.
// The transcript is exactly the concatenation of block(0) … block(n-1), so
// every row of the transcript belongs to exactly one block — which is what
// makes (entry, offset) a complete address for any row.
func (w *Workspace) entryBlock(i, width int) []string {
	lines := w.entryLines(i, width, i == w.focus)
	if i == 0 {
		return lines
	}
	gap := gapLines(w.entryRole(i-1), w.entryRole(i))
	out := make([]string, 0, gap+len(lines))
	for g := 0; g < gap; g++ {
		out = append(out, "")
	}
	return append(out, lines...)
}

// visibleWindow returns the absolute index of the first rendered entry and
// exactly `rows` lines for the current scroll position.
func (w *Workspace) visibleWindow(rows, width int) (topAbs int, lines []string) {
	// One composed window is one FRAME, and this is where it begins. Every
	// height measured from here until the next window -- bottomAnchored's
	// backward walk, the compose loop below, clampScroll, and whatever a
	// key handler measures against the window now on screen -- is the same
	// height. See chat.go's `frame` field for what went missing when it
	// was not.
	//
	// Here rather than in View(): View composes the query area too, and a
	// key handler measuring between frames should see the heights the user
	// is actually looking at, not re-decide them.
	w.frame++

	n := w.entryCount()
	if n == 0 || rows <= 0 {
		return 0, nil
	}

	if w.pinned {
		w.topAbs, w.topRow = w.bottomAnchored(rows, width)
	} else {
		w.clampScroll(width)
	}
	topAbs = w.topAbs

	out := make([]string, 0, rows)
	for i := topAbs; i < n && len(out) < rows; i++ {
		block := w.entryBlock(i, width)
		if i == topAbs {
			// The only partial block: the top one, entered part-way down.
			if w.topRow < len(block) {
				block = block[w.topRow:]
			} else {
				block = nil
			}
		}
		out = append(out, block...)
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return topAbs, out
}

// bottomAnchored is the position that puts the transcript's LAST row on the
// viewport's last row — pinned mode, and where G lands. Walking backward
// keeps it O(rows) rather than O(entries).
func (w *Workspace) bottomAnchored(rows, width int) (topAbs, topRow int) {
	if rows <= 0 {
		// A key can arrive before the first render has recorded the
		// viewport size. One row is a harmless stand-in; the next render
		// re-anchors against the real height.
		rows = 1
	}
	total := 0
	for i := w.entryCount() - 1; i >= 0; i-- {
		h := len(w.entryBlock(i, width))
		if total+h >= rows {
			// This block is the one the top row falls inside.
			return i, h - (rows - total)
		}
		total += h
	}
	return 0, 0
}

// scrollRows moves the position by delta ROWS, one row at a time so that a
// step never lands outside a block. Scrolling up unpins — the stream keeps
// going underneath, and G is how the user comes back to it.
func (w *Workspace) scrollRows(delta, width int) {
	if delta == 0 || w.entryCount() == 0 {
		return
	}
	if w.pinned {
		// Leaving pinned mode has to start from where pinned mode
		// currently IS, or the first press jumps somewhere unrelated.
		w.topAbs, w.topRow = w.bottomAnchored(w.viewRows, width)
	}
	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}
	for ; delta > 0; delta-- {
		if step > 0 {
			w.topRow++
			if w.topRow >= len(w.entryBlock(w.topAbs, width)) {
				if w.topAbs >= w.entryCount()-1 {
					w.topRow-- // already at the last row of the last block
					break
				}
				w.topAbs++
				w.topRow = 0
			}
		} else {
			w.topRow--
			if w.topRow < 0 {
				if w.topAbs == 0 {
					w.topRow = 0
					break
				}
				w.topAbs--
				w.topRow = len(w.entryBlock(w.topAbs, width)) - 1
				if w.topRow < 0 {
					w.topRow = 0
				}
			}
		}
	}

	// Scrolling to (or past) the bottom re-pins, so following a stream
	// resumes without needing G — the same "you are at the live edge"
	// meaning pinned already had.
	//
	// The CLAMP matters as much as the flag: without it the position keeps
	// walking past the bottom-anchored row while the pinned render keeps
	// snapping it back, so every further press moves the state and changes
	// nothing on screen. Holding j at the bottom would drift the position
	// arbitrarily far past the end of the transcript.
	bAbs, bRow := w.bottomAnchored(w.viewRows, width)
	if w.topAbs > bAbs || (w.topAbs == bAbs && w.topRow >= bRow) {
		w.topAbs, w.topRow = bAbs, bRow
		// F18: the bottom of the RESIDENT WINDOW is not necessarily the
		// live edge any more. Pinning here when the tail has been evicted
		// would be a lie with two visible consequences: the pinned render
		// would bottom-anchor to a stale newest message, and the "jump to
		// latest (G)" affordance would disappear at precisely the moment
		// it is the only way back. So pinned keeps meaning "at the live
		// edge", and reaching a windowed bottom instead fires an append
		// fetch (maybeFetchAtEdge, below) and lets the user keep going.
		if w.historyAtTail() {
			w.pinned = true
			return
		}
		w.pinned = false
		w.maybeFetchAtEdge()
		return
	}
	// Anywhere above the live edge is unpinned, and it has to be assigned
	// here rather than left alone: a scroll that STARTED pinned would
	// otherwise stay pinned, the pinned render would snap the position
	// straight back to the bottom, and scrolling up would do nothing at all.
	w.pinned = false
	// F18: and this is where scrolling turns into fetching. Last, after the
	// position is final, so the edge test reads the position the user will
	// actually be looking at.
	w.maybeFetchAtEdge()
}

// clampScroll keeps an unpinned position addressable after the content under
// it changes — a streaming reply grows rows, and a block that shrank could
// otherwise leave topRow past its end.
func (w *Workspace) clampScroll(width int) {
	n := w.entryCount()
	if n == 0 {
		w.topAbs, w.topRow = 0, 0
		return
	}
	w.topAbs = clampInt(w.topAbs, 0, n-1)
	h := len(w.entryBlock(w.topAbs, width))
	if h == 0 {
		w.topRow = 0
		return
	}
	w.topRow = clampInt(w.topRow, 0, h-1)
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleKey is the transcript's input surface, reached only while the query
// input does not hold focus (F4's focus model).
//
//	j/k · ↓/↑   scroll ONE ROW
//	n/p         move entry focus (what y, o, t and ^R act on)
//	G           pin to the live bottom
//	y           copy the focused entry's raw text
//	t           toggle the focused reply's thinking block
//	o / enter   expand the focused task slot
//
// j/k scroll rather than moving focus because a reply taller than the
// viewport has to be readable, and entry-granular movement made its lower
// half unreachable. Focus moved to n/p rather than being dropped: y, o, t
// and Retry all need a target, and "whichever entry happens to be at the
// bottom" is not one a user can aim.
func (w *Workspace) handleKey(k tea.KeyMsg) tea.Cmd {
	return w.handleKeyString(k.String())
}

// handleKeyString is handleKey's actual switch, factored out so
// handlePastedBurst (below) can replay a recovered keystroke without
// fabricating a tea.KeyMsg.
func (w *Workspace) handleKeyString(s string) tea.Cmd {
	width := w.viewWidth
	switch s {
	case "j", "down":
		w.scrollRows(1, width)
	case "k", "up":
		w.scrollRows(-1, width)
	case "pgdown", "ctrl+d":
		w.scrollRows(w.pageRows(), width)
	case "pgup", "ctrl+u":
		w.scrollRows(-w.pageRows(), width)
	case "n":
		w.moveFocus(1, width)
	case "p":
		w.moveFocus(-1, width)
	case "G":
		w.pinned = true
		w.focus = -1
		// F18: G used to be free, because everything was in memory. With
		// eviction the newest messages may not be resident, so this is now
		// a fetch when it has to be — and still instant when it does not.
		// See history.go's jumpToBottom.
		w.jumpToBottom()
	case "y":
		w.copyFocused()
	case "t":
		// F5: the thinking block is collapsible and collapsed by default.
		if e := w.liveAt(w.focusedIndex()); e != nil {
			e.expanded = !e.expanded
			e.version++
		}
	case "o", "enter":
		// The task slot's expandable state — the third step of F5's
		// `⟳ running` → `✓ done` → expandable.
		if e := w.liveAt(w.focusedIndex()); e != nil && e.lane == laneTask {
			e.slotOpen = !e.slotOpen
			e.version++
		}
	}
	return nil
}

// pageRows is one screenful less a row of overlap, so a page never skips a
// line the reader was mid-way through.
func (w *Workspace) pageRows() int {
	if w.viewRows <= 2 {
		return 1
	}
	return w.viewRows - 1
}

// handlePastedBurst recovers a hotkey that Root's paste-burst detector
// (app\paste.go) misread as the start of a paste.
//
// That detector exists for F4's query input and has no way to know this
// workspace's single-letter keys are hotkeys, not typing: it treats any two
// plain keystrokes arriving closer together than pasteBurstGap (6ms) as one
// paste. A HELD key's OS auto-repeat can look exactly like that once
// Windows Console batches the repeat records — so pressing (or holding) G
// arrives as app.PasteMsg{Text: "GGGG"}, which nothing would otherwise
// consume, and G looks like it did nothing.
//
// Only a burst that is entirely ONE repeated character is replayed as that
// many keystrokes. Anything else — a real accidental paste of prose — is
// left alone: replaying arbitrary pasted text char-by-char would let a
// stray j, k, y or G inside it scroll or overwrite the clipboard, which is
// worse than doing nothing.
func (w *Workspace) handlePastedBurst(text string) tea.Cmd {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	first := runes[0]
	for _, r := range runes[1:] {
		if r != first {
			return nil
		}
	}
	var cmd tea.Cmd
	for range runes {
		cmd = w.handleKeyString(string(first))
	}
	return cmd
}

// moveFocus walks the entry focus and scrolls just enough to show it. It is
// relative to the newest entry when nothing is explicitly focused yet, so
// the first press of p always moves off the entry at the bottom.
func (w *Workspace) moveFocus(delta, width int) {
	n := w.entryCount()
	if n == 0 {
		return
	}
	cur := w.focus
	if cur < 0 {
		cur = n - 1
	}
	next := clampInt(cur+delta, 0, n-1)
	w.focus = next

	if next == n-1 && delta > 0 {
		// Same F18 gate as scrollRows': focusing the last RESIDENT entry
		// is not the same as being at the live edge once the tail can be
		// absent.
		if w.historyAtTail() {
			w.pinned = true
			return
		}
	}
	// Anchor the focused entry at the top of the viewport: predictable, and
	// it always shows the head of the entry — which is the line a task slot
	// puts its whole state on.
	w.pinned = false
	w.topAbs = next
	w.topRow = 0
	if next > 0 {
		// Skip the leading gap so the entry's own first row is at the top.
		w.topRow = gapLines(w.entryRole(next-1), w.entryRole(next))
	}
	w.maybeFetchAtEdge()
}
