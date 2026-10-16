package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Paste on Windows, and why this file exists.
//
// Bubble Tea v1 enables bracketed paste (`\x1b[?2004h`) and decodes it in
// detectBracketedPaste — but ONLY on its ANSI input path. On Windows it takes
// a different path entirely:
//
//   - initInput() sets ENABLE_VIRTUAL_TERMINAL_INPUT on stdin, and then
//   - newInputReader() calls prepareConsole(conin, ENABLE_WINDOW_INPUT |
//     ENABLE_EXTENDED_FLAGS [| ENABLE_MOUSE_INPUT]), which builds the new mode
//     from ZERO and so CLEARS the VT-input bit that was just set, and
//   - readConInputs() emits one KeyMsg per console KEY_EVENT_RECORD and never
//     writes the Paste field at all.
//
// So on Windows `KeyMsg.Paste` is structurally always false, the terminal
// never sends the \x1b[200~ … \x1b[201~ markers, and a paste arrives as N
// ordinary key events. (My first pass through F1 asserted the opposite in a
// comment in main.go. It was wrong on this platform.)
//
// The obvious fix — tea.WithInputTTY(), which opens CONIN$ on a different file
// descriptor and therefore drops Bubble Tea onto the ANSI path where bracketed
// paste works — cannot be used: on Windows, listenForResize is a no-op and
// WindowSizeMsg is produced ONLY by the console-record reader. Taking the ANSI
// path trades working paste for a layout engine that never learns the terminal
// resized, which is a strictly worse bug and the one F1 exists to get right.
//
// What is left is to recognise the burst. A paste is delivered by
// peekAndReadConsInput as a single batch of records, so its key events reach
// Update microseconds apart; a human types at tens of milliseconds. That gap is
// three orders of magnitude, which is enough to classify on.
const (
	// pasteBurstGap: two key events closer together than this were not typed.
	// A 6ms rollover would be roughly 5,000 words per minute.
	pasteBurstGap = 6 * time.Millisecond

	// pasteQuiet: how long after the last key a burst is considered finished.
	pasteQuiet = 25 * time.Millisecond

	// minPasteRunes: a burst with no newline in it has to be at least this
	// long to be a paste rather than typing.
	//
	// Timing alone CANNOT separate the two. Pressing two different keys at
	// once -- ordinary rollover, which fast typing produces constantly --
	// delivers two console records microseconds apart, exactly like a paste
	// (a verification round's Bug 4: one key at a time behaved, two at once
	// flipped the status line to "pasted 1 line as one event"). The event
	// source does carry a real distinction -- a paste injected with
	// WriteConsoleInput has VirtualKeyCode 0 where a physical key never
	// does -- but bubbletea's KeyMsg keeps only Type/Runes/Alt/Paste, and
	// reaching the raw record means replacing its input reader, which is
	// what this file's header explains cannot be done without losing
	// WindowSizeMsg on Windows.
	//
	// Shape is the distinction that IS available: rollover is two or three
	// characters, a paste worth coalescing is dozens or has a line break in
	// it. Below the bar the keys are delivered as the keystrokes they were,
	// so the text is identical either way -- only the classification, the
	// counter, and undo granularity differ.
	minPasteRunes = 8
)

// PasteMsg is one paste, whole. The root emits it; F4's input consumes it.
//
// It exists as a message rather than as a flag on a key so that the platform
// difference stops here: on a system where the terminal brackets its pastes,
// the KeyMsg carries Paste and this is emitted directly; on Windows it is
// emitted by the coalescer. Neither the workspace nor the input widget has to
// know which happened.
type PasteMsg struct {
	Text  string
	Lines int
}

// pasteFlushMsg fires when the single flush timer elapses. It carries no
// generation: onFlushTick decides from pasteState.lastAt whether the burst
// has actually been quiet, which is what makes at most one timer enough.
type pasteFlushMsg struct{}

// pasteReleaseMsg releases a single held key that turned out not to be the
// start of a burst.
type pasteReleaseMsg struct{ gen int }

type pasteState struct {
	gen int

	// flushPending is at most ONE outstanding flush timer, ever.
	//
	// scheduleFlush used to return a tea.Tick PER pasted character. Bubble
	// Tea runs every command in its own goroutine and each one then blocks
	// sending its result on the program's UNBUFFERED msgs channel, so a
	// 400-line paste queued ~14,000 goroutines against the same channel the
	// console input reader is trying to send keystrokes through. The reader
	// loses that race, the OS console input buffer fills, and Windows drops
	// the records it can no longer store -- a verification round saw one
	// 400-line paste arrive as 14 lines, the other 386 gone before any of
	// this code ever saw them, which is also why undo could not recover
	// them: they were never typed into the buffer at all.
	//
	// One timer, re-armed from lastAt when it fires early, costs the same
	// 25ms of quiet and nothing per character.
	flushPending bool

	// held is the one-key lookback. A paste's FIRST character is
	// indistinguishable from a keystroke until the second one arrives, so it
	// waits pasteBurstGap before being delivered as a keystroke. That is the
	// entire latency this adds to typing: at most 6ms, well under one frame.
	held   *tea.KeyMsg
	heldAt time.Time

	active bool
	buf    []rune
	lines  int
	lastAt time.Time
}

// pasteable reports whether a key event could be part of pasted text.
// Modified keys and chords never are.
//
// A KeyRunes event additionally needs a real rune in it. bubbletea's
// Windows console reader (key_windows.go's keyType) has no named case for
// a BARE modifier keydown/keyup — Ctrl, Alt, the Windows key, Caps Lock,
// pressed or released with no following character — and its final
// fallback for an unrecognised key is `return KeyRunes`, with Runes set to
// e.Char verbatim; for one of these events e.Char is 0. Without this
// check, such an event looks exactly like a typed/pasted character to the
// burst detector below — it gets folded into a paste (or "held" and later
// released as an ordinary keystroke), inflating the line counter and
// corrupting whatever it lands next to. F04-query-input.md's manual
// verification round found this by its symptom: a NUL rune surviving into
// a persisted draft, and the paste-line counter reading "1" before any
// paste had happened.
func pasteable(k tea.KeyMsg) bool {
	if k.Alt {
		return false
	}
	if k.Type == tea.KeyRunes {
		return len(k.Runes) > 0 && k.Runes[0] != 0
	}
	return k.Type == tea.KeyEnter || k.Type == tea.KeySpace
}

func keyText(k tea.KeyMsg) string {
	switch k.Type {
	case tea.KeyEnter:
		return "\n"
	case tea.KeySpace:
		return " "
	default:
		return string(k.Runes)
	}
}

// handlePasteable runs the burst detector. It returns the command to run and
// whether the key was consumed by the paste machinery.
func (r *Root) handlePasteable(k tea.KeyMsg) (tea.Cmd, bool) {
	now := time.Now()
	p := &r.paste

	// Already inside a burst: everything belongs to it until it goes quiet.
	if p.active {
		p.appendKey(k)
		p.lastAt = now
		return p.scheduleFlush(), true
	}

	if p.held != nil && now.Sub(p.heldAt) < pasteBurstGap {
		// Two keys too close together to have been typed. The held one was the
		// first character of a paste after all.
		p.active = true
		p.buf = nil
		p.lines = 1
		p.appendKey(*p.held)
		p.appendKey(k)
		p.held = nil
		p.lastAt = now
		return p.scheduleFlush(), true
	}

	// Nothing yet. Deliver whatever was held, and hold this one instead.
	var cmds []tea.Cmd
	if p.held != nil {
		cmds = append(cmds, r.deliverKey(*p.held))
	}
	p.gen++
	p.held = &k
	p.heldAt = now

	gen := p.gen
	cmds = append(cmds, tea.Tick(pasteBurstGap, func(time.Time) tea.Msg {
		return pasteReleaseMsg{gen: gen}
	}))
	return tea.Batch(cmds...), true
}

func (p *pasteState) appendKey(k tea.KeyMsg) {
	if k.Type == tea.KeyEnter {
		p.lines++
	}
	p.buf = append(p.buf, []rune(keyText(k))...)
}

// scheduleFlush arms the single flush timer if it is not already armed.
// See pasteState.flushPending.
func (p *pasteState) scheduleFlush() tea.Cmd {
	if p.flushPending {
		return nil
	}
	p.flushPending = true
	return tickFlush(pasteQuiet)
}

func tickFlush(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pasteFlushMsg{} })
}

// onFlushTick is pasteFlushMsg's handler: flush only once the burst has
// genuinely been quiet for pasteQuiet, otherwise re-arm for the remainder.
// Timing is read from lastAt rather than from which tick this was, so a
// burst can never be cut short by a timer scheduled earlier in it.
func (r *Root) onFlushTick() tea.Cmd {
	p := &r.paste
	p.flushPending = false
	if !p.active {
		return nil
	}
	if remaining := pasteQuiet - time.Since(p.lastAt); remaining > 0 {
		p.flushPending = true
		return tickFlush(remaining)
	}
	return r.flushPaste()
}

// looksLikePaste decides whether an assembled burst was really pasted.
// See minPasteRunes for why this is decided on shape rather than timing.
func looksLikePaste(text string) bool {
	if isRepeatedKey(text) {
		return false // a held key's auto-repeat, however long it ran
	}
	if strings.ContainsRune(text, '\n') {
		return true // a line break is never rollover
	}
	return len([]rune(text)) >= minPasteRunes
}

// deliverAsKeystrokes hands a burst that was not a paste to the workspace
// one key at a time, exactly as if the detector had never held them.
func (r *Root) deliverAsKeystrokes(text string) tea.Cmd {
	var cmds []tea.Cmd
	for _, ch := range text {
		k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
		if cmd := r.deliverKey(k); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// flushPaste ends a burst and emits it as one event.
func (r *Root) flushPaste() tea.Cmd {
	p := &r.paste
	if !p.active {
		return nil
	}
	text := string(p.buf)
	lines := p.lines
	if lines < 1 {
		lines = 1
	}

	p.active = false
	p.buf = nil
	p.lines = 0
	p.flushPending = false

	if !looksLikePaste(text) {
		return r.deliverAsKeystrokes(text)
	}
	return func() tea.Msg { return PasteMsg{Text: text, Lines: lines} }
}

// releaseHeldKey delivers a key that turned out to be a keystroke, not the
// start of a paste.
func (r *Root) releaseHeldKey() tea.Cmd {
	p := &r.paste
	if p.held == nil {
		return nil
	}
	k := *p.held
	p.held = nil
	return r.deliverKey(k)
}

// recordPaste is the one place the paste counters move, whichever route the
// paste arrived by.
//
// A burst that is one character repeated is a HELD KEY, not a paste, and
// is not counted as one. bubbletea Windows reader expands a console
// record's RepeatCount into that many identical KeyMsgs emitted back to
// back (inputreader_windows.go: for i := 0; i < int(e.RepeatCount); i++),
// so an auto-repeat is indistinguishable from a paste by timing alone --
// which is how the counter reached "pasted 1 line as one event" during
// ordinary typing, with no paste anywhere (a verification round Bug 4).
// F3 hit the same thing from the other side and recovered it in
// chat\scroll.go's handlePastedBurst; this is that insight applied at
// the counter.
//
// The pasted TEXT is unaffected either way -- it still reaches the input
// as one PasteMsg and lands identically. Only the classification changes,
// so a genuine (rare) paste of one repeated character costs nothing but
// an uncounted event.
func (r *Root) recordPaste(m PasteMsg) {
	lines := m.Lines
	if lines < 1 {
		lines = strings.Count(m.Text, "\n") + 1
	}
	r.env.PasteEvents++
	r.env.PasteLines += lines
	r.status = pasteStatus(lines)
}

// isRepeatedKey reports whether text is the same single rune repeated --
// an auto-repeat batch rather than pasted content. A single rune on its
// own is not one: that is an ordinary keystroke the burst detector held
// and let go.
func isRepeatedKey(text string) bool {
	runes := []rune(text)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes[1:] {
		if r != runes[0] {
			return false
		}
	}
	return true
}

func pasteStatus(lines int) string {
	if lines == 1 {
		return "pasted 1 line as one event"
	}
	return "pasted " + itoa(lines) + " lines as one event"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// pasteInFlight reports whether a paste may still be assembling: a burst is
// running, or a pasteable key is held and its partner has not arrived yet.
// app\render.go's View skips a frame while this holds -- see its doc for why
// covering the HELD case is what keeps burst activation independent of how
// long a frame takes to draw.
func (r *Root) pasteInFlight() bool {
	return r.paste.active || r.paste.held != nil
}

// isBareModifier reports whether this KeyMsg is a modifier key-down with no
// character behind it — see handleKey for the whole account of where it
// comes from and what it cost.
//
// It is in this file because pasteable() below is the other half of the
// same story: that function's `k.Runes[0] != 0` is the same guard applied
// to the paste path alone, and keeping the two together is what stops the
// next person from "simplifying" one without the other.
func isBareModifier(k tea.KeyMsg) bool {
	if k.Type != tea.KeyRunes || len(k.Runes) == 0 {
		return false
	}
	for _, r := range k.Runes {
		if r != 0 {
			return false
		}
	}
	return true
}
