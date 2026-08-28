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

// pasteFlushMsg fires when a burst has gone quiet.
type pasteFlushMsg struct{ gen int }

// pasteReleaseMsg releases a single held key that turned out not to be the
// start of a burst.
type pasteReleaseMsg struct{ gen int }

type pasteState struct {
	gen int

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
func pasteable(k tea.KeyMsg) bool {
	if k.Alt {
		return false
	}
	return k.Type == tea.KeyRunes || k.Type == tea.KeyEnter || k.Type == tea.KeySpace
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

func (p *pasteState) scheduleFlush() tea.Cmd {
	p.gen++
	gen := p.gen
	return tea.Tick(pasteQuiet, func(time.Time) tea.Msg {
		return pasteFlushMsg{gen: gen}
	})
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
func (r *Root) recordPaste(m PasteMsg) {
	lines := m.Lines
	if lines < 1 {
		lines = strings.Count(m.Text, "\n") + 1
	}
	r.env.PasteEvents++
	r.env.PasteLines += lines
	r.status = pasteStatus(lines)
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
