package chat

import (
	tea "github.com/charmbracelet/bubbletea"
)

// gapLines is Design Change C's exchange spacing: 1 blank row between a
// user question and its own reply, 2 between a reply and the next
// question, 1 for anything else (a system/error/tool message involved, or
// two messages of the same role back to back) — not specified, and 1 is the
// least surprising default. Only ever called for a message that is not the
// first one in the current scroll window; the first visible message never
// gets a leading gap.
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

// visibleWindow returns the absolute index of the first rendered message and
// exactly `rows` lines (padded/clipped) for the current scroll state.
//
// Cost is O(rows) message-height lookups per call, each cached — never
// O(len(all)) — which is what keeps a 10,000-message session's scroll smooth:
// nothing here ever measures more than one screenful of messages at a time.
func (w *Workspace) visibleWindow(rows, width int) (topAbs int, lines []string) {
	if len(w.all) == 0 || rows <= 0 {
		return 0, nil
	}

	if w.pinned {
		topAbs = w.bottomAnchoredTop(rows, width)
	} else {
		topAbs = clampInt(w.topAbs, 0, len(w.all)-1)
		if w.focus >= 0 {
			topAbs = w.ensureFocusInWindow(topAbs, rows, width)
		}
	}
	w.topAbs = topAbs

	out := make([]string, 0, rows)
	for i := topAbs; i < len(w.all) && len(out) < rows; i++ {
		if i > topAbs {
			for g := 0; g < gapLines(w.all[i-1].Role, w.all[i].Role); g++ {
				out = append(out, "")
			}
		}
		var block []string
		if i == w.focus {
			block = renderMessage(w.theme, w.all[i], width, true)
		} else {
			block = w.renderCached(w.all[i], width)
		}
		out = append(out, block...)
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return topAbs, out
}

// bottomAnchoredTop walks backward from the newest message until rows fills,
// so pinned mode always shows the tail — the same "always O(rows)" property
// as the focused path. The gap between each pair of included messages counts
// toward `rows` too, so a screen near the exchange spacing's boundary does
// not overflow once the blank rows are actually drawn.
func (w *Workspace) bottomAnchoredTop(rows, width int) int {
	total := 0
	i := len(w.all) - 1
	next := -1 // index of the previously-included (more recent) message
	for i >= 0 {
		n := len(w.renderCached(w.all[i], width))
		gap := 0
		if next >= 0 {
			gap = gapLines(w.all[i].Role, w.all[next].Role)
		}
		if total+n+gap > rows && total > 0 {
			break
		}
		total += n + gap
		next = i
		i--
	}
	return i + 1
}

// ensureFocusInWindow slides topAbs just far enough that the focused
// message's whole block is inside the rows-tall window — scrolling up
// immediately if focus moved above it, or forward message-by-message if
// focus's bottom would spill past the last row.
func (w *Workspace) ensureFocusInWindow(topAbs, rows, width int) int {
	if w.focus < topAbs {
		return w.focus
	}
	total := 0
	focusBottom := -1
	for i := topAbs; i <= w.focus && i < len(w.all); i++ {
		if i > topAbs {
			total += gapLines(w.all[i-1].Role, w.all[i].Role)
		}
		total += len(w.renderCached(w.all[i], width))
		if i == w.focus {
			focusBottom = total
		}
	}
	if focusBottom <= rows {
		return topAbs
	}
	over := focusBottom - rows
	for over > 0 && topAbs < w.focus {
		removed := len(w.renderCached(w.all[topAbs], width))
		if topAbs+1 <= w.focus {
			removed += gapLines(w.all[topAbs].Role, w.all[topAbs+1].Role)
		}
		topAbs++
		over -= removed
	}
	return topAbs
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

// handleKey is F3's whole input surface: j/k or arrows move message focus
// (F3.md's "Selection and copy"), G re-pins to the latest message, y copies
// the focused message's raw text. Everything else falls through untouched —
// F4 owns the query input, and a key this package does not recognise must
// not consume the event.
func (w *Workspace) handleKey(k tea.KeyMsg) tea.Cmd {
	return w.handleKeyString(k.String())
}

// handleKeyString is handleKey's actual switch, factored out so
// handlePastedBurst (below) can replay a recovered keystroke without
// fabricating a tea.KeyMsg.
func (w *Workspace) handleKeyString(s string) tea.Cmd {
	switch s {
	case "j", "down":
		w.moveFocus(1)
	case "k", "up":
		w.moveFocus(-1)
	case "G":
		w.pinned = true
		w.focus = -1
	case "y":
		w.copyFocused()
	}
	return nil
}

// handlePastedBurst recovers a hotkey that Root's paste-burst detector
// (app\paste.go) misread as the start of a paste.
//
// That detector exists for F4's eventual text input and has no way to know
// this workspace's single-letter keys are hotkeys, not typing: it treats
// any two plain keystrokes arriving closer together than pasteBurstGap
// (6ms) as one paste. A HELD key's OS auto-repeat can look exactly like
// that once Windows Console batches the repeat records — paste.go's own
// comment notes a real paste "is delivered ... as a single batch of
// records", and a held key's repeats are drained the same way. The result:
// pressing (or holding) G arrives here as app.PasteMsg{Text: "GGGG"}, which
// nothing in this workspace's Update ever consumed — G looked like it did
// nothing.
//
// F3 has no text input for a genuine multi-character paste to go into yet
// (F03-chat-viewport.md's own Handoff: "Nothing can be typed yet"), so only
// a burst that is entirely ONE repeated character is replayed as that many
// keystrokes here. Anything else — a real accidental paste of prose — is
// left alone: replaying arbitrary pasted text char-by-char would let a
// stray 'j', 'k', 'y' or 'G' inside it move focus or overwrite the
// clipboard, which is a worse outcome than doing nothing.
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

// moveFocus is relative to the newest message when nothing is explicitly
// focused yet (the pinned/no-focus state), so the first press of k always
// moves off the message currently at the bottom of the screen.
func (w *Workspace) moveFocus(delta int) {
	if len(w.all) == 0 {
		return
	}
	cur := w.focus
	if cur < 0 {
		cur = len(w.all) - 1
	}
	next := clampInt(cur+delta, 0, len(w.all)-1)
	w.focus = next
	w.pinned = next == len(w.all)-1 && delta > 0
}
