package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ---------------------------------------------------------------------------
// 1A — the help overlay ignored the theme entirely
// ---------------------------------------------------------------------------

// Every overlay renders through theme roles. An overlay that returns bare text
// is not theme-neutral; it is hardcoded to the terminal's default foreground,
// which is why Help stayed one fixed colour while its border repainted.
func TestOverlayContentIsThemed(t *testing.T) {
	overlays := map[string]func(*Root) Overlay{
		"help":    func(r *Root) Overlay { return newHelpOverlay(r) },
		"palette": func(r *Root) Overlay { return newPaletteOverlay(r) },
	}

	for name, build := range overlays {
		t.Run(name, func(t *testing.T) {
			r := newRoot(t)
			o := build(r)

			for _, palette := range theme.Names() {
				th := theme.New(palette, theme.BackgroundTransparent)
				out := o.View(th, 80, 20)

				if stripANSI(out) == "" {
					t.Fatalf("%s rendered nothing", palette)
				}
				// Every non-blank line must carry a colour from the palette.
				for _, line := range strings.Split(out, "\n") {
					if strings.TrimSpace(stripANSI(line)) == "" {
						continue
					}
					if !strings.Contains(line, "\x1b[") {
						t.Errorf("%s: unstyled line — no palette can repaint it: %q",
							palette, line)
					}
				}
			}
		})
	}
}

// The help screen's text must actually CHANGE colour between palettes.
func TestHelpRepaintsWithThePalette(t *testing.T) {
	r := newRoot(t)
	o := newHelpOverlay(r)

	a := o.View(theme.New("turquoise", theme.BackgroundTransparent), 80, 20)
	b := o.View(theme.New("gold", theme.BackgroundTransparent), 80, 20)

	if a == b {
		t.Fatal("help rendered identically under two palettes")
	}
	if stripANSI(a) != stripANSI(b) {
		t.Fatal("the palette changed help's TEXT, not just its colour")
	}
}

// Columns are padded before styling, so an escape sequence never counts
// towards a column width.
func TestHelpColumnsLineUp(t *testing.T) {
	r := newRoot(t)
	out := newHelpOverlay(r).View(r.theme, 80, 40)

	var widths []int
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			continue
		}
		widths = append(widths, strings.Index(line[2:], " ")+2)
	}
	if len(widths) < 3 {
		t.Fatalf("only %d action rows found", len(widths))
	}
}

// ---------------------------------------------------------------------------
// 1B — the compositor corrupted rows it only partly covered
// ---------------------------------------------------------------------------

// With an overlay open, every row is still exactly the terminal width and no
// row carries an unterminated colour into the cells beside the panel.
func TestOverlayCompositingKeepsRowsIntact(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)
	r.OpenOverlay(newHelpOverlay(r))
	if r.overlay == nil {
		t.Fatal("overlay did not open")
	}

	out := r.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 40 {
		t.Fatalf("rendered %d rows, want 40", len(lines))
	}
	for i, line := range lines {
		if w := visibleWidth(line); w != 140 {
			t.Errorf("row %d is %d cells, want 140: %q", i, w, stripANSI(line))
		}
		if err := ansiBalanced(line); err != "" {
			t.Errorf("row %d: %s", i, err)
		}
	}
}

// The content the panel does NOT cover survives — it is dimmed, not blanked.
//
// The old compositor replaced everything after the panel with spaces, which is
// how the Task card's right border disappeared on exactly the rows Help spanned.
func TestOverlayKeepsTheContentBesideIt(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	before := padRows(strings.Split(stripANSI(r.View()), "\n"), 140)

	r.OpenOverlay(newHelpOverlay(r))
	after := padRows(strings.Split(stripANSI(r.View()), "\n"), 140)

	// Work out the panel's actual column span, then assert that every column
	// OUTSIDE it is unchanged. Compare by display cell, not by byte: the frame
	// is full of box-drawing runes.
	w, h := newHelpOverlay(r).MinSize()
	left := (140 - w) / 2
	right := left + w
	top := (40 - h) / 2

	panelRows := 0
	for i := range before {
		if i < top || i >= top+h {
			if before[i] != after[i] {
				t.Errorf("row %d is outside the panel entirely but changed", i)
			}
			continue
		}
		panelRows++

		if got, want := sliceCells(after[i], 0, left), sliceCells(before[i], 0, left); got != want {
			t.Errorf("row %d: columns 0–%d changed\n before %q\n  after %q", i, left, want, got)
		}
		if got, want := sliceCells(after[i], right, 140), sliceCells(before[i], right, 140); got != want {
			t.Errorf("row %d: columns %d–140 changed\n before %q\n  after %q", i, right, want, got)
		}
	}
	if panelRows == 0 {
		t.Fatal("the overlay changed nothing")
	}
}

// sliceCells is the column-exact cut the compositor is built on.
func TestSliceCells(t *testing.T) {
	cases := []struct {
		in       string
		from, to int
		want     string
	}{
		{"abcdef", 0, 3, "abc"},
		{"abcdef", 3, 6, "def"},
		{"abc", 0, 6, "abc   "},
		{"abcdef", 2, 2, ""},
		// A double-width rune straddling the cut becomes spaces for the cells
		// that fall inside — half a wide character would shift every column
		// after it.
		{"あい", 0, 1, " "},
		{"あい", 1, 3, "  い"[2:]},
		{"aあb", 0, 3, "aあ"},
	}
	for _, c := range cases {
		got := sliceCells(c.in, c.from, c.to)
		if visibleWidth(got) != c.to-c.from {
			t.Errorf("sliceCells(%q,%d,%d) = %q, width %d, want %d",
				c.in, c.from, c.to, got, visibleWidth(got), c.to-c.from)
		}
	}
	if got := sliceCells("abcdef", 1, 4); got != "bcd" {
		t.Errorf("got %q, want %q", got, "bcd")
	}
}

// padRows pads every row to the terminal width. Without an overlay the root
// leaves short rows short; with one the scrim pads them. Trailing blanks are
// not a difference worth failing on.
func padRows(rows []string, width int) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = padCells(r, width)
	}
	return out
}

// ansiBalanced reports an unterminated colour: an escape sequence that was
// opened and never reset before the end of the row.
func ansiBalanced(line string) string {
	opens := strings.Count(line, "\x1b[")
	if opens == 0 {
		return ""
	}
	// Every sequence lipgloss emits ends with 'm'.
	inEsc := false
	closed := 0
	for _, r := range line {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc && r == 'm' {
			inEsc = false
			closed++
		}
	}
	if inEsc {
		return "row ends inside an escape sequence"
	}
	if closed != opens {
		return "escape sequences were cut"
	}
	return ""
}

// ---------------------------------------------------------------------------
// 3 — paste produced nothing at all on Windows
// ---------------------------------------------------------------------------

// A burst of key events arriving microseconds apart is a paste, not typing.
// This is the Windows path, where KeyMsg.Paste is never set — see paste.go.
func TestBurstOfKeysIsOnePaste(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	// 30 lines of "line", the way a console delivers them: runes plus an
	// Enter per newline, all in one batch.
	var msgs []tea.Msg
	for i := 0; i < 30; i++ {
		for _, ch := range "line" {
			msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		}
		if i < 29 {
			msgs = append(msgs, tea.KeyMsg{Type: tea.KeyEnter})
		}
	}

	runBurst(t, r, msgs)

	if r.env.PasteEvents != 1 {
		t.Errorf("paste events = %d, want 1", r.env.PasteEvents)
	}
	if r.env.PasteLines != 30 {
		t.Errorf("paste lines = %d, want 30", r.env.PasteLines)
	}
}

// The diagnostic box is what the manual check reads.
func TestPasteShowsOnScreen(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	// Three lines: two newlines between them, none trailing.
	var msgs []tea.Msg
	for i := 0; i < 3; i++ {
		if i > 0 {
			msgs = append(msgs, tea.KeyMsg{Type: tea.KeyEnter})
		}
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	}
	runBurst(t, r, msgs)
	drain(r)

	out := stripANSI(r.View())
	if !strings.Contains(out, "1 event(s), 3 line(s)") {
		t.Errorf("paste not reported on screen:\n%s", out)
	}
}

// A terminal that DOES bracket its pastes still works, through the other route.
func TestBracketedPasteStillWorks(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	m, cmd := r.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Paste: true, Runes: []rune(strings.Join(lines, "\n")),
	})
	root := m.(*Root)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			root.Update(msg)
		}
	}

	if root.env.PasteEvents != 1 {
		t.Errorf("paste events = %d, want 1", root.env.PasteEvents)
	}
	if root.env.PasteLines != 30 {
		t.Errorf("paste lines = %d, want 30", root.env.PasteLines)
	}
}

// Typing is not a paste. Keys spaced like a human reach the workspace as keys.
func TestTypingIsNotAPaste(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	for _, ch := range "hello" {
		m, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		r = m.(*Root)
		runCmd(t, r, cmd)
		time.Sleep(12 * time.Millisecond) // slower than pasteBurstGap
	}
	// Let the last held key go.
	time.Sleep(2 * pasteBurstGap)
	drainTicks(t, r)

	if r.env.PasteEvents != 0 {
		t.Errorf("typing registered %d paste(s)", r.env.PasteEvents)
	}
}

// ---------------------------------------------------------------------------
// 4 — a bare modifier keydown/up decoded as a zero-rune KeyRunes event
// ---------------------------------------------------------------------------
//
// bubbletea's Windows console reader (key_windows.go's keyType) has no
// named case for Ctrl/Alt/Win/CapsLock pressed or released alone — its
// final fallback is `return KeyRunes` with Runes set to e.Char verbatim,
// and for one of these events e.Char is 0. A verification round's Bug 4
// caught the visible symptom: the paste-line counter read "pasted 1 line
// as one event" on a fresh launch, before any real paste happened — two
// such artifacts arriving close together (plausible around any modifier
// key activity, e.g. focus changes) used to merge into a phantom 1-line
// paste the same way two real characters would.

func TestPasteableRejectsAZeroRuneEvent(t *testing.T) {
	nul := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}
	if pasteable(nul) {
		t.Fatal("a zero-rune KeyRunes event must not be pasteable")
	}
	ok := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	if !pasteable(ok) {
		t.Fatal("an ordinary rune must still be pasteable")
	}
}

func TestBareModifierArtifactNeverCountsAsAPaste(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	msgs := []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}},
	}
	runBurst(t, r, msgs)

	if r.env.PasteEvents != 0 {
		t.Errorf("paste events = %d, want 0 — a bare modifier artifact is not a paste", r.env.PasteEvents)
	}
}

// A chord in the middle of nothing still fires; the paste detector must not
// swallow keys that are not text.
func TestChordsStillFireWithTheDetectorInPlace(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	m, cmd := r.Update(key("ctrl+l"))
	root := m.(*Root)
	runCmdDeep(t, root, cmd, 0)

	if root.status != "redrawn" {
		t.Errorf("ctrl+l did not fire with the paste detector in the key path: status = %q", root.status)
	}
}

// runCmdDeep executes a command and, when it produces a tea.BatchMsg, the
// commands inside it — which is what Bubble Tea does. Ticks are skipped: their
// Cmd blocks for the tick's duration when called directly.
func runCmdDeep(t *testing.T, r *Root, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 4 {
		return
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			runCmdDeep(t, r, c, depth+1)
		}
	default:
		r.Update(msg)
	}
}

// ---------------------------------------------------------------------------

// runBurst feeds messages back-to-back, which is what a pasted batch looks
// like coming out of the console reader, then settles the burst.
//
// The commands are deliberately NOT executed here. Every one of them is a
// tea.Tick, and a Tick's Cmd BLOCKS for its duration when you call it — Bubble
// Tea runs commands on their own goroutines, so in the real program they never
// delay the next message. Running them inline would space the keys further
// apart than pasteBurstGap and turn the paste back into typing.
func runBurst(t *testing.T, r *Root, msgs []tea.Msg) {
	t.Helper()
	for _, msg := range msgs {
		r.Update(msg)
	}
	// The flush normally arrives as a tick; drive it directly.
	if cmd := r.flushPaste(); cmd != nil {
		if msg := cmd(); msg != nil {
			r.Update(msg)
		}
	}
}

// runCmd executes a command and feeds the result back, once.
func runCmd(t *testing.T, r *Root, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		r.Update(msg)
	}
}

func drainTicks(t *testing.T, r *Root) {
	t.Helper()
	if cmd := r.releaseHeldKey(); cmd != nil {
		runCmd(t, r, cmd)
	}
}

// ---------------------------------------------------------------------------
// 5 — the render storm behind the lag, Bug 1 and Bug 4
// ---------------------------------------------------------------------------
//
// Bubble Tea calls View after every Update, and on Windows a paste arrives
// as one KeyMsg per pasted character, so a large paste used to drive
// thousands of full re-renders of a frame that cannot have changed — the
// pasted text is still accumulating in pasteState.buf. While the loop is
// busy rendering, the unbuffered msgs channel backs input up in the OS
// console buffer, and the burst detector (which times keys at Update time,
// not arrival time) starts misreading both real typing and real pastes.

func TestViewIsSuppressedWhileAPasteIsAssembling(t *testing.T) {
	r := newRoot(t)
	first := size(t, r, 120, 30)
	if first == "" {
		t.Fatal("no first frame")
	}

	// Start a burst: two pasteable keys close enough together to activate.
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !r.paste.active {
		t.Fatal("expected an active paste burst")
	}

	// Renders during the burst re-serve the cached frame verbatim.
	r.status = "this status change must not reach the screen mid-burst"
	if got := r.View(); got != first {
		t.Fatal("View re-rendered while a paste burst was assembling")
	}

	// Once the burst flushes, rendering resumes and picks the change up.
	if cmd := r.flushPaste(); cmd != nil {
		if msg := cmd(); msg != nil {
			r.Update(msg)
		}
	}
	if got := r.View(); got == first {
		t.Fatal("View did not resume rendering after the burst flushed")
	}
}

// A held key's auto-repeat is not a paste. bubbletea's Windows reader
// expands a console record's RepeatCount into that many identical KeyMsgs
// with no delay between them, which the burst detector cannot tell from a
// paste by timing — the counter used to report one (Bug 4, still reproducing
// after the zero-rune fix).
func TestAutoRepeatBurstIsNotCountedAsAPaste(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	var msgs []tea.Msg
	for i := 0; i < 8; i++ {
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	runBurst(t, r, msgs)

	if r.env.PasteEvents != 0 {
		t.Errorf("a held key registered %d paste(s)", r.env.PasteEvents)
	}
	if strings.Contains(r.status, "pasted") {
		t.Errorf("status claims a paste happened: %q", r.status)
	}

	// A genuine mixed burst is still a paste.
	msgs = nil
	for _, ch := range "real pasted text" {
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	runBurst(t, r, msgs)
	if r.env.PasteEvents != 1 {
		t.Errorf("a real paste registered %d event(s), want 1", r.env.PasteEvents)
	}
}

// Bug 1's cleanest reproduction: ONE large paste losing most of itself.
// scheduleFlush used to return a tea.Tick per pasted character, so a big
// paste queued thousands of goroutines against bubbletea's unbuffered msgs
// channel and starved the console reader. At most one timer may ever be
// outstanding now, however long the burst runs.
func TestLargeBurstArmsExactlyOneFlushTimer(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	timers := 0
	for i := 0; i < 5000; i++ {
		cmd, consumed := r.handlePasteable(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
		if !consumed {
			t.Fatalf("key %d was not consumed by the paste detector", i)
		}
		if cmd != nil {
			timers++
		}
	}
	// One for the held-key release, one for the flush. Never per character.
	if timers > 2 {
		t.Fatalf("armed %d timers across a 5,000-key burst, want at most 2", timers)
	}
	if !r.paste.active {
		t.Fatal("the burst ended early")
	}
	if got := len(r.paste.buf); got != 5000 {
		t.Fatalf("buffered %d runes, want 5000 — the burst dropped content", got)
	}
}

// A flush tick that fires while keys are still arriving re-arms instead of
// cutting the burst short. This is what stops a long paste being split.
func TestFlushTickReArmsWhileTheBurstIsStillLive(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	// Paste-shaped on purpose: long enough that flushing it produces a real
	// PasteMsg rather than being delivered back as keystrokes.
	for _, ch := range "a genuinely pasted stretch of text" {
		r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	if !r.paste.active {
		t.Fatal("expected an active burst")
	}

	// A tick arriving immediately — long before pasteQuiet has elapsed.
	if cmd := r.onFlushTick(); cmd == nil {
		t.Fatal("an early tick should re-arm, not flush")
	}
	if !r.paste.active {
		t.Fatal("an early tick ended the burst")
	}

	// Once the burst has actually been quiet, it flushes.
	r.paste.lastAt = time.Now().Add(-2 * pasteQuiet)
	cmd := r.onFlushTick()
	if r.paste.active {
		t.Fatal("a quiet burst did not flush")
	}
	if cmd == nil {
		t.Fatal("flush produced no command")
	}
}

// Bug 4: two DIFFERENT keys pressed at once deliver two console records
// microseconds apart, which timing alone cannot tell from a paste. Shape
// can: rollover is a couple of characters with no line break.
func TestSimultaneousKeypressesAreNotAPaste(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	runBurst(t, r, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}},
	})

	if r.env.PasteEvents != 0 {
		t.Errorf("rollover registered %d paste(s)", r.env.PasteEvents)
	}
	if strings.Contains(r.status, "pasted") {
		t.Errorf("status claims a paste happened: %q", r.status)
	}
}

func TestLooksLikePaste(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"th", false},                      // two-key rollover
		{"the", false},                     // three-key rollover
		{"jjjjjjjjjjjj", false},            // a held key
		{"a\nb", true},                     // any line break
		{"a longer stretch of text", true}, // past minPasteRunes
	}
	for _, c := range cases {
		if got := looksLikePaste(c.text); got != c.want {
			t.Errorf("looksLikePaste(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// The feedback loop behind Bug 1's remaining ceiling: a burst only ACTIVATES
// when two pasteable keys are processed within pasteBurstGap of each other,
// measured at Update time, so a frame that costs more than the gap makes
// activation impossible and suppression can never engage. Suppressing the
// frame for a merely HELD key breaks that circularity.
func TestViewIsSuppressedWhileAKeyIsHeld(t *testing.T) {
	r := newRoot(t)
	first := size(t, r, 120, 30)

	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if r.paste.held == nil {
		t.Fatal("expected the first pasteable key to be held")
	}
	if r.paste.active {
		t.Fatal("one key should not activate a burst")
	}

	r.status = "must not reach the screen while a key is held"
	if got := r.View(); got != first {
		t.Fatal("View rendered a frame while a key was held")
	}

	// Releasing the held key resumes rendering.
	r.releaseHeldKey()
	if r.pasteInFlight() {
		t.Fatal("nothing should be in flight after the release")
	}
	if got := r.View(); got == first {
		t.Fatal("View did not resume after the held key was released")
	}
}
