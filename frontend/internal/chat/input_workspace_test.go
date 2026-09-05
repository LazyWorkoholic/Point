package chat

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// The input holds focus by default, and typing routes to it — including
// letters F3 used as global hotkeys before there was anything to type into.
func TestInputFocusedByDefaultAndTypingInsertsText(t *testing.T) {
	w := newTestWorkspace(t)
	if !w.InputFocused() {
		t.Fatal("input should be focused by default")
	}
	w.Update(keyMsg("j"))
	w.Update(keyMsg("k"))
	if got := w.input.buf.text(); got != "jk" {
		t.Fatalf("got %q, want %q — j/k should type, not move message focus", got, "jk")
	}
	if w.focus != -1 {
		t.Fatalf("message focus moved to %d; typing must not touch it while composing", w.focus)
	}
}

// app.EnterBrowseMsg / app.FocusInputMsg — the two registered actions'
// payloads (app\actions.go's chat.browse and pane.focus.next) — toggle
// focus, and F3's hotkeys only fire once browse mode is entered.
func TestEnterBrowseAndFocusInputMessagesToggleRouting(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(5)
	w.pinned = true

	w.Update(app.EnterBrowseMsg{})
	if w.InputFocused() {
		t.Fatal("EnterBrowseMsg should have blurred the input")
	}
	w.Update(keyMsg("p")) // now a hotkey, not a keystroke
	if got := w.input.buf.text(); got != "" {
		t.Fatalf("browse mode: p typed %q into the input, want nothing", got)
	}
	// F5 moved entry focus onto n/p; j/k scroll by row instead.
	if w.focus == -1 {
		t.Fatal("p in browse mode should have moved entry focus")
	}

	w.Update(app.FocusInputMsg{})
	if !w.InputFocused() {
		t.Fatal("FocusInputMsg should have refocused the input")
	}
	w.Update(keyMsg("k"))
	if got := w.input.buf.text(); got != "k" {
		t.Fatalf("got %q, want %q — k should type once focus is back on the input", got, "k")
	}
}

// A real multi-line paste, while the input is focused, lands as one
// InsertText call — never replayed as keystrokes, never sent.
func TestPasteWhileFocusedInsertsIntoInput(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(app.PasteMsg{Text: "line one\nline two\nline three"})
	if got := w.input.buf.text(); got != "line one\nline two\nline three" {
		t.Fatalf("got %q", got)
	}
	if w.input.buf.cursor != len([]rune("line one\nline two\nline three")) {
		t.Fatalf("cursor = %d, not left at the end of the pasted text", w.input.buf.cursor)
	}
}

// Pasting into a buffer that already has content APPENDS at the cursor — it
// must never replace what was already there. A verification round's Bug 1
// reported the opposite: after several pastes, the buffer was found wiped
// down to just the newest paste's content.
func TestPasteIntoNonEmptyBufferAppends(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(app.PasteMsg{Text: "first paste\nwith two lines"})
	if got := w.input.buf.text(); got != "first paste\nwith two lines" {
		t.Fatalf("got %q", got)
	}

	w.Update(app.PasteMsg{Text: "\nsecond paste"})
	want := "first paste\nwith two lines\nsecond paste"
	if got := w.input.buf.text(); got != want {
		t.Fatalf("got %q, want %q — the first paste must survive the second", got, want)
	}
}

// A second paste arriving while the workspace already holds unrelated
// typed/pasted content must not disturb it either way — same append
// discipline, exercised with typing between two pastes.
func TestSecondPasteAfterTypingStillAppends(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(app.PasteMsg{Text: "391 lines worth of pasted content"})
	w.Update(keyMsg(" "))
	w.Update(app.PasteMsg{Text: "more pasted content"})

	want := "391 lines worth of pasted content more pasted content"
	if got := w.input.buf.text(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// No control character ever survives into the buffer, however it arrives —
// typed, pasted, or (Bug 2's actual trigger) a bare modifier keydown/up
// bubbletea's Windows reader decodes as a zero-rune KeyRunes event mixed in
// among real keystrokes.
func TestNoControlCharactersSurviveMixedInput(t *testing.T) {
	w := newTestWorkspace(t)
	mixed := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{0}}, // bare modifier artifact
		{Type: tea.KeyRunes, Runes: []rune{'T'}},
		{Type: tea.KeyRunes, Runes: []rune{'h'}},
		{Type: tea.KeyRunes, Runes: []rune{'i'}},
		{Type: tea.KeyRunes, Runes: []rune{'s'}},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune{0}},
		{Type: tea.KeyRunes, Runes: []rune{'i'}},
		{Type: tea.KeyRunes, Runes: []rune{'s'}},
	}
	for _, k := range mixed {
		w.Update(k)
	}
	got := w.input.buf.text()
	if got != "This is" {
		t.Fatalf("got %q, want %q", got, "This is")
	}
	for _, r := range got {
		if r < 0x20 {
			t.Fatalf("control character %U survived into the buffer: %q", r, got)
		}
	}
}

func TestFilterInsertableKeepsNewlinesAndTabs(t *testing.T) {
	in := "a\x00b\nc\td\x01e"
	got := filterInsertable(in)
	want := "ab\nc\tde"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// onSend's clear is undoable — a stray or spurious Enter (Bug 1's worst
// consequence, whatever its exact trigger) is recoverable with one Ctrl+Z
// rather than being permanent data loss.
func TestSendClearIsUndoable(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.buf.InsertText("important unsent text")
	w.onSend()
	if got := w.input.buf.text(); got != "" {
		t.Fatalf("send did not clear: %q", got)
	}
	w.input.buf.Undo()
	if got := w.input.buf.text(); got != "important unsent text" {
		t.Fatalf("undo after send = %q, want the text back", got)
	}
}

func enterKey(alt bool) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter, Alt: alt} }
func backspaceKey(alt bool) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyBackspace, Alt: alt}
}

func TestAltEnterInsertsNewlineNotSend(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(keyMsg("a"))
	w.Update(enterKey(true))
	w.Update(keyMsg("b"))
	if got := w.input.buf.text(); got != "a\nb" {
		t.Fatalf("got %q, want %q", got, "a\nb")
	}
}

// Bug 5: Alt+Enter is Windows Console's OWN fullscreen-toggle shortcut and
// never reaches the process. Ctrl+J ('\n' decodes to KeyCtrlJ
// unconditionally in key_windows.go) is newline's actual working binding.
func TestCtrlJInsertsNewline(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(keyMsg("a"))
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	w.Update(keyMsg("b"))
	if got := w.input.buf.text(); got != "a\nb" {
		t.Fatalf("got %q, want %q", got, "a\nb")
	}
}

// F4's whole implementation of "Enter sends": clear the buffer, the chips,
// and the persisted draft — nothing is sent over the wire (F5's job). See
// input_keys.go's onSend doc for exactly what F5 replaces.
func TestEnterClearsInputAndDraft(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"
	var saved config.State
	saveCalls := 0
	w.saveStateFn = func(s config.State) error {
		saved = s
		saveCalls++
		return nil
	}

	w.Update(keyMsg("h"))
	w.Update(keyMsg("i"))
	w.input.chips = []AttachmentChip{{Name: "task.py", Current: true}}

	// clearDraft saves synchronously and returns no command of its own —
	// there is nothing left to schedule once the write has already
	// happened, so onSend's return value is not itself the signal here.
	w.onSend()
	if w.input.buf.text() != "" || w.input.buf.cursor != 0 {
		t.Fatalf("input not cleared: %q cursor=%d", w.input.buf.text(), w.input.buf.cursor)
	}
	if len(w.input.chips) != 0 {
		t.Fatal("chips not cleared on send")
	}
	if saveCalls != 1 {
		t.Fatalf("saveStateFn called %d times, want 1 (clearDraft saves immediately)", saveCalls)
	}
	if _, ok := saved.Drafts["s1"]; ok {
		t.Fatal("draft for s1 should have been removed, not written empty")
	}

	// Enter on an already-empty input is a no-op — nothing to send.
	if cmd := w.onSend(); cmd != nil {
		t.Fatal("onSend on an empty input should do nothing")
	}
}

// The debounce: typing schedules a save, but only the LAST scheduled
// generation actually writes — an earlier, superseded tick must not
// overwrite what a later keystroke produced (or resurrect text that was
// since cleared).
func TestDraftSaveDebounceOnlyLastGenerationWrites(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"
	saveCalls := 0
	w.saveStateFn = func(config.State) error { saveCalls++; return nil }

	w.Update(keyMsg("a"))
	staleGen := w.input.draftGen
	w.Update(keyMsg("b"))
	currentGen := w.input.draftGen

	if staleGen == currentGen {
		t.Fatal("a second keystroke should bump the generation")
	}
	w.saveDraftIfDue(staleGen) // the stale tick, arriving late
	if saveCalls != 0 {
		t.Fatalf("a superseded tick saved anyway (%d calls)", saveCalls)
	}
	w.saveDraftIfDue(currentGen)
	if saveCalls != 1 {
		t.Fatalf("the current tick did not save (%d calls)", saveCalls)
	}
}

// Bug 17: app.quit must not depend on the 500ms debounce having already
// fired. FlushDraft is quit's synchronous escape hatch from it -- called
// with the debounce tick still outstanding (never fired, never even
// simulated via saveDraftIfDue), it must still persist exactly what the
// live buffer holds right now.
func TestFlushDraftPersistsWithoutWaitingForTheDebounce(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"
	var saved config.State
	saveCalls := 0
	w.saveStateFn = func(s config.State) error { saved = s; saveCalls++; return nil }

	w.Update(keyMsg("h"))
	w.Update(keyMsg("i"))
	genBeforeFlush := w.input.draftGen

	w.FlushDraft()
	if saveCalls != 1 {
		t.Fatalf("FlushDraft did not save synchronously (%d calls)", saveCalls)
	}
	d, ok := saved.Drafts["s1"]
	if !ok || d.Text != "hi" || d.Cursor != 2 {
		t.Fatalf("flushed draft = %+v ok=%v, want text=%q cursor=2", d, ok, "hi")
	}

	// The still-outstanding tick from before the flush must be a harmless
	// no-op if it ever does arrive -- not a second write, and not an
	// overwrite of what FlushDraft already committed.
	w.saveDraftIfDue(genBeforeFlush)
	if saveCalls != 1 {
		t.Fatalf("a pre-flush tick fired anyway after FlushDraft (%d calls)", saveCalls)
	}
}

// Bug 17's actual repro shape: several characters typed in a row from a
// gap-adjacent cursor, then FlushDraft called immediately -- standing in
// for app.quit firing before the debounce ever gets its 500ms. What lands
// on disk must be the FULL live buffer, not whatever an earlier keystroke
// in the same burst would have saved.
func TestFlushDraftCapturesATypedBurstFromAGapAdjacentCursor(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"
	var saved config.State
	w.saveStateFn = func(s config.State) error { saved = s; return nil }

	w.input.textWidth = 6
	w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("aaaaa")})
	for i := 0; i < 8; i++ {
		w.Update(tea.KeyMsg{Type: tea.KeySpace})
	}
	w.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bbbbb")})
	w.Update(tea.KeyMsg{Type: tea.KeyEnd}) // land on the gap-adjacent boundary, no move after
	beforeSpaces := w.input.buf.cursor

	for i := 0; i < 4; i++ {
		w.Update(tea.KeyMsg{Type: tea.KeySpace})
	}
	wantCursor := beforeSpaces + 4
	wantText := w.input.buf.text()

	w.FlushDraft() // no arrow key, no wait -- exactly the un-fixed repro's shape
	d, ok := saved.Drafts["s1"]
	if !ok {
		t.Fatal("FlushDraft did not persist a draft at all")
	}
	if d.Cursor != wantCursor {
		t.Fatalf("persisted cursor = %d, want %d (live buffer at flush time)", d.Cursor, wantCursor)
	}
	if d.Text != wantText {
		t.Fatalf("persisted text = %q, want %q (live buffer at flush time)", d.Text, wantText)
	}
}

// Kill the app with text in the input, restart: the draft and cursor come
// back, scoped to the session that had it.
func TestDraftRoundTripsThroughSetSession(t *testing.T) {
	w := newTestWorkspace(t)
	w.state.Drafts = map[string]config.Draft{
		"s1": {Text: "half a thought", Cursor: 4},
	}
	w.setSession(wireSession{ID: "s1"})
	if got := w.input.buf.text(); got != "half a thought" {
		t.Fatalf("got %q, want the restored draft", got)
	}
	if w.input.buf.cursor != 4 {
		t.Fatalf("cursor = %d, want 4", w.input.buf.cursor)
	}

	// A session with no draft on file starts empty, not carrying over the
	// previous session's text.
	w.setSession(wireSession{ID: "s2"})
	if got := w.input.buf.text(); got != "" {
		t.Fatalf("s2 should start with no draft, got %q", got)
	}
}

// "Backspace at position 0 removes the last chip."
func TestBackspaceAtPositionZeroRemovesLastChip(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{{Name: "a.py"}, {Name: "b.py"}}
	w.Update(backspaceKey(false))
	if len(w.input.chips) != 1 || w.input.chips[0].Name != "a.py" {
		t.Fatalf("chips = %+v, want just a.py left", w.input.chips)
	}
	// Backspace with text present must not touch chips.
	w.Update(keyMsg("x"))
	w.Update(backspaceKey(false))
	if len(w.input.chips) != 1 {
		t.Fatalf("chips changed while deleting text: %+v", w.input.chips)
	}
}

// Alt+Backspace is word-delete's actual working binding — see
// input_keys.go's doc comment on why plain ctrl+backspace cannot be told
// apart from plain Backspace in this environment.
func TestAltBackspaceDeletesWordLeft(t *testing.T) {
	w := newTestWorkspace(t)
	for _, r := range "hello world" {
		w.Update(keyMsg(string(r)))
	}
	w.Update(backspaceKey(true))
	if got := w.input.buf.text(); got != "hello " {
		t.Fatalf("got %q, want %q", got, "hello ")
	}
}

// splitGrowth: the combined row budget never changes (Root.View pads/clips
// to it), the input grows by shrinking the viewport, and the viewport never
// gives up more than half its own nominal height.
func TestSplitGrowthShrinksMainAndCapsAtHalf(t *testing.T) {
	w := newTestWorkspace(t)
	l := layout.Layout{
		Main:      layout.Rect{X: 1, Y: 1, W: 60, H: 20},
		Secondary: layout.Rect{X: 1, Y: 21, W: 60, H: 3},
	}
	totalH := l.Main.H + l.Secondary.H

	// Empty input: base height, unchanged from what Compute gave.
	mainRect, secRect := w.splitGrowth(l)
	if secRect.H != l.Secondary.H || mainRect.H != l.Main.H {
		t.Fatalf("empty input: main=%d secondary=%d, want %d/%d",
			mainRect.H, secRect.H, l.Main.H, l.Secondary.H)
	}
	if mainRect.H+secRect.H != totalH {
		t.Fatalf("row budget changed: %d+%d != %d", mainRect.H, secRect.H, totalH)
	}

	// A great deal of text: grows, but the viewport never drops below half
	// its own original height, and the total stays fixed.
	for i := 0; i < 30; i++ {
		w.input.buf.InsertText("one line of typed text\n")
	}
	mainRect, secRect = w.splitGrowth(l)
	if mainRect.H+secRect.H != totalH {
		t.Fatalf("row budget changed under growth: %d+%d != %d", mainRect.H, secRect.H, totalH)
	}
	minMain := (l.Main.H + 1) / 2
	if mainRect.H < minMain {
		t.Fatalf("viewport shrank to %d, below half of %d", mainRect.H, l.Main.H)
	}
	if secRect.H <= l.Secondary.H {
		t.Fatalf("secondary did not grow at all: %d", secRect.H)
	}
}

// "past that maximum, the input becomes internally scrollable — no more
// growth." Once the buffer has more wrapped lines than the capped box can
// show, ensureCursorVisible keeps the cursor's own line in the visible
// window, following it back up when the cursor moves away from the tail.
func TestInternalScrollKeepsCursorVisiblePastGrowthCap(t *testing.T) {
	w := newTestWorkspace(t)
	l := layout.Layout{
		Main:      layout.Rect{X: 1, Y: 1, W: 60, H: 20},
		Secondary: layout.Rect{X: 1, Y: 21, W: 60, H: 3},
	}
	for i := 0; i < 60; i++ {
		w.input.buf.InsertText("line\n")
	}

	_, secRect := w.splitGrowth(l)
	rows := secRect.H - 2
	lines := wrapInputRunes(w.input.buf.runes, w.input.textWidth)
	if len(lines) <= rows {
		t.Fatal("test setup did not actually exceed the growth cap")
	}
	cursorLine := lineAt(lines, w.input.buf.cursor)
	if w.input.scrollTop > cursorLine || cursorLine >= w.input.scrollTop+rows {
		t.Fatalf("cursor line %d not within the visible window [%d,%d)",
			cursorLine, w.input.scrollTop, w.input.scrollTop+rows)
	}

	// Jump the cursor to the very start and re-render: the window must
	// follow it back up, not stay pinned to the tail.
	w.input.buf.cursor = 0
	w.splitGrowth(l)
	if w.input.scrollTop != 0 {
		t.Fatalf("scrollTop = %d after jumping to the start, want 0", w.input.scrollTop)
	}
}

// Every drawn line stays exactly the rect's width once the query input has
// actually grown — the same check workspace_test.go's
// TestEveryLineIsExactlyTheRectWidth runs for an empty input, extended to
// the box heights splitGrowth produces once there is real multi-line,
// CJK-and-emoji content to wrap.
func TestEveryLineIsExactlyTheRectWidthWhileInputGrows(t *testing.T) {
	w := newTestWorkspace(t)
	w.bootstrapped = true
	w.sessionID = "s1"
	w.all = fakeMessages(20)
	w.input.buf.InsertText("a message spanning several lines\nwith an explicit break\nand 日本語 🎉 text\nand another line to force real growth")
	w.input.chips = []AttachmentChip{{Name: "task.py", Current: true}, {Name: "notes.md", Current: false}}

	sizes := []struct{ w, h int }{
		{200, 50}, {120, 30}, {100, 26}, {90, 24}, {70, 20}, {50, 12},
	}
	for _, sz := range sizes {
		l := layout.Compute(sz.w, sz.h, layout.State{Active: layout.WorkspaceChat, HeaderAllowed: true})
		if l.TooSmall {
			continue
		}
		out := w.View(l)
		for i, line := range strings.Split(out, "\n") {
			if got := visibleWidth(line); got != 0 && got != l.Main.W {
				t.Errorf("%dx%d: line %d is %d cells, want %d:\n%q",
					sz.w, sz.h, i, got, l.Main.W, stripANSI(line))
			}
		}
	}
}

// DESIGN GAP (a verification round): browse mode silently swallowed every
// key that was not j/k/y/G, with nothing on screen saying which mode was
// active or how to leave it. Chosen fix: option (b) — browse stays
// strictly modal, but the chip is unmistakable about it.
func TestBrowseModeShowsAClearIndicator(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.boxContentWidth = 50
	w.input.buf.InsertText("hello")

	w.input.focused = false
	lines := w.queryLines(layout.Rect{W: 54, H: 3})
	if len(lines) == 0 || !strings.Contains(stripANSI(lines[0]), "BROWSE") {
		t.Fatalf("browse mode did not show an indicator: %q", lines)
	}

	w.input.focused = true
	lines = w.queryLines(layout.Rect{W: 54, H: 3})
	if len(lines) == 0 || strings.Contains(stripANSI(lines[0]), "BROWSE") {
		t.Fatalf("focused mode should not show the browse indicator: %q", lines)
	}
}

func TestQueryAndMainBorderRolesMirrorFocus(t *testing.T) {
	if mainBorderRole(true) == mainBorderRole(false) {
		t.Fatal("the viewport border must differ between focused/unfocused input")
	}
	if queryBorderRole(modeChat, true) == queryBorderRole(modeChat, false) {
		t.Fatal("the query border must differ between focused/unfocused")
	}
	if queryBorderRole(modeChat, true) == queryBorderRole(modeResearch, true) {
		t.Fatal("research mode must change the query border while focused")
	}
}

// Done criterion 8: "The mode chip changes on @, and the border colour
// follows." currentMode() derives the mode from the buffer itself, resolved
// against the known agent ids (matchedAgentID) rather than by position
// alone — Bug 10 from a verification round: "@report.pdf" is an attachment,
// not an agent, and must not flip the chip just because the message starts
// with '@'. Only an EXACT match on a registered agent id counts, mirroring
// backend\cli.py's own _strip_agent_prefix rule.
func TestModeChipMatchesRegisteredAgentsOnly(t *testing.T) {
	w := newTestWorkspace(t)
	if got := w.input.currentMode(); got != modeChat {
		t.Fatalf("empty input mode = %v, want modeChat", got)
	}

	// A bare '@' names no agent yet, but it IS a viable prefix of one, so
	// it shows the pending chip rather than nothing -- a verification
	// round asked for feedback that the @ registered at all.
	w.Update(keyMsg("@"))
	if got := w.input.currentMode(); got != modePending {
		t.Fatalf("mode after a bare @ = %v, want modePending", got)
	}

	// The false positive Bug 10 named directly: a file-like token after @
	// must never read as an agent.
	for _, r := range "report.pdf" {
		w.Update(keyMsg(string(r)))
	}
	if got := w.input.currentMode(); got != modeChat {
		t.Fatalf("mode for @report.pdf = %v, want modeChat", got)
	}

	// Clear it and type a real, exact agent id.
	for range "@report.pdf" {
		w.Update(backspaceKey(false))
	}
	w.Update(keyMsg("@"))
	for _, r := range "research" {
		w.Update(keyMsg(string(r)))
	}
	if got := w.input.currentMode(); got != modeResearch {
		t.Fatalf("mode for @research = %v, want modeResearch", got)
	}
	if queryBorderRole(w.input.currentMode(), true) != queryBorderRole(modeResearch, true) {
		t.Fatal("the border role should follow the mode")
	}

	// Backspace the whole thing back out — the mode reverts on its own, no
	// sticky flag to fall out of sync.
	for range "@research" {
		w.Update(backspaceKey(false))
	}
	if got := w.input.currentMode(); got != modeChat {
		t.Fatalf("mode after clearing the input = %v, want modeChat", got)
	}

	// An '@' that is NOT the first character is not a mode switch either.
	w.Update(keyMsg("hi @research"))
	if got := w.input.currentMode(); got != modeChat {
		t.Fatalf("mode with a mid-message @ = %v, want modeChat", got)
	}
}

// A real agent (general, filler-explainer) resolves — matchedAgentID
// reports it — but has no chip of its own defined yet, so the mode chip
// stays modeChat. This is a deliberate scope limit (F4.md defines only
// ▸ chat and ▸ @research), not a bug: matchedAgentID and currentMode are
// separate on purpose so a later chip only needs a currentMode change.
func TestNonResearchAgentMatchesButHasNoChipYet(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.buf.setText("@general", 8)

	id, ok := matchedAgentID(w.input.buf.runes, w.input.buf.cursor)
	if !ok || id != "general" {
		t.Fatalf("matchedAgentID = (%q, %v), want (\"general\", true)", id, ok)
	}
	if got := w.input.currentMode(); got != modeChat {
		t.Fatalf("mode for @general = %v, want modeChat (no chip defined for it yet)", got)
	}
}

// Both mode-chip states render distinctly.
func TestModeChipRendersBothStates(t *testing.T) {
	w := newTestWorkspace(t)
	// queryLines reads boxContentWidth, which splitGrowth normally sets as
	// part of View() — set it directly here since this test calls
	// queryLines on its own, without going through a full layout.Layout.
	w.input.boxContentWidth = 36

	w.input.buf.InsertText("hello")
	chatLines := w.queryLines(layout.Rect{W: 40, H: 3})

	w.input.buf.setText("@research", 9)
	researchLines := w.queryLines(layout.Rect{W: 40, H: 3})

	if len(chatLines) == 0 || len(researchLines) == 0 {
		t.Fatal("expected rendered content in both modes")
	}
	if chatLines[0] == researchLines[0] {
		t.Fatal("the mode chip line should differ between chat and research")
	}
}

// Bug 1, re-reported: it fails on a fixed COUNT of pastes, not on timing.
// Fifteen sequential pastes must lose nothing at any count.
func TestFifteenSequentialPastesLoseNothing(t *testing.T) {
	w := newTestWorkspace(t)
	one := strings.Repeat("this is one line of pasted content\n", 391)
	for i := 1; i <= 15; i++ {
		w.Update(app.PasteMsg{Text: one})
		wantLines := i * 391
		if got := strings.Count(w.input.buf.text(), "\n"); got != wantLines {
			t.Fatalf("after paste %d: %d newlines, want %d", i, got, wantLines)
		}
	}
}

// The guard behind Bug 1's actual mechanism: a paste's own trailing newline,
// delivered on its own because the burst detector ended the burst early
// under load, must insert a newline rather than reaching Send and clearing
// the whole composed message.
func TestEnterRightAfterAPasteDoesNotSend(t *testing.T) {
	w := newTestWorkspace(t)
	w.Update(app.PasteMsg{Text: "several lines\nof pasted text"})
	before := w.input.buf.text()

	// The paste's trailing Enter, arriving as its own key event.
	w.Update(enterKey(false))

	if got := w.input.buf.text(); got != before+"\n" {
		t.Fatalf("got %q, want the pasted text plus a newline — Enter must not send here", got)
	}

	// Once the settle window has passed, Enter sends normally again.
	w.input.lastPasteAt = time.Now().Add(-2 * pasteSettle)
	w.Update(enterKey(false))
	if got := w.input.buf.text(); got != "" {
		t.Fatalf("Enter well after a paste should send (clear): got %q", got)
	}
}

// Bug 3: a draft written before filterInsertable existed can carry control
// characters, and faithfully restoring it restored the bug across every
// restart. Loading repairs it instead.
func TestCorruptedDraftIsRepairedOnLoad(t *testing.T) {
	w := newTestWorkspace(t)
	w.state.Drafts = map[string]config.Draft{
		"s1": {Text: "\x00\x00\x00This is my \x00unsent draft", Cursor: 15},
	}
	w.setSession(wireSession{ID: "s1"})

	got := w.input.buf.text()
	if got != "This is my unsent draft" {
		t.Fatalf("got %q, want the repaired text", got)
	}
	for _, r := range got {
		if r < 0x20 {
			t.Fatalf("control character %U survived the repair", r)
		}
	}
	if w.input.buf.cursor > len(w.input.buf.runes) {
		t.Fatalf("cursor %d is past the end of the repaired text (%d runes)",
			w.input.buf.cursor, len(w.input.buf.runes))
	}

	// And the repaired draft leaves @ detection working again.
	w.input.buf.setText("@research", 9)
	if w.input.currentMode() != modeResearch {
		t.Fatal("@research still does not switch the chip after a repair")
	}
}

// Bug 3, settled: an @token still being typed shows a PENDING chip while it
// is a viable prefix of a registered agent, the agent's own chip once it
// matches exactly, and nothing once it cannot match at all.
func TestPendingAgentChipWhileTyping(t *testing.T) {
	cases := []struct {
		text string
		want editorMode
	}{
		{"", modeChat},
		{"@", modePending},          // feedback that @ registered
		{"@r", modePending},         // prefix of "research"
		{"@rese", modePending},      // still viable
		{"@research", modeResearch}, // exact
		{"@g", modePending},         // prefix of "general"
		{"@general", modeChat},      // a real agent, but no chip defined yet
		{"@report.pdf", modeChat},   // never viable: an attachment, not an agent
		{"@zzz", modeChat},          // not a prefix of anything
		{"@r hello", modeChat},      // finished token that named nothing
		{"hi @research", modeChat},  // not the leading token
	}
	for _, c := range cases {
		w := newTestWorkspace(t)
		w.input.buf.setText(c.text, len([]rune(c.text)))
		if got := w.input.currentMode(); got != c.want {
			t.Errorf("%q: mode = %v, want %v", c.text, got, c.want)
		}
	}
}

// The pending chip must be visibly distinct from both other states, and must
// not claim the accent border that means "an agent resolved".
func TestPendingChipRendersDistinctly(t *testing.T) {
	if modeChipPrefix(modePending) == modeChipPrefix(modeChat) ||
		modeChipPrefix(modePending) == modeChipPrefix(modeResearch) {
		t.Fatal("the pending chip must read differently from chat and research")
	}
	if queryBorderRole(modePending, true) == queryBorderRole(modeResearch, true) {
		t.Fatal("pending must not take the research accent border")
	}
}

// Bug 12: the browse indicator must only advertise bindings that work. Esc
// enters browse mode; only Tab comes back.
func TestBrowseIndicatorOnlyAdvertisesWorkingBindings(t *testing.T) {
	if strings.Contains(browseModePrefix, "Esc") {
		t.Fatalf("browse indicator advertises Esc, which does not return focus: %q", browseModePrefix)
	}
	if !strings.Contains(browseModePrefix, "Tab") {
		t.Fatalf("browse indicator does not mention Tab: %q", browseModePrefix)
	}
}

// The pending chip resolves the token AT THE CURSOR, not the buffer as a
// whole. A verification round's exact repro: with content already in the
// buffer, moving to position 0 and typing '@' gave no pending chip, because
// the whitespace-bounded token swallowed the text that was already there.
func TestPendingResolvesTheTokenAtTheCursor(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		cursor int
		want   editorMode
	}{
		{"bare @ on an empty buffer", "@", 1, modePending},
		{"@ typed before existing content", "@hello world", 1, modePending},
		{"@r typed before existing content", "@rhello world", 2, modePending},
		{"full agent typed before content", "@researchhello world", 9, modeResearch},
		{"agent then a space and a message", "@research explain the runtime", 29, modeResearch},
		{"cursor before the @ entirely", "@research explain", 0, modeResearch},
		{"cursor past the token", "@hello world", 12, modeChat},
		{"not viable, before content", "@zzhello world", 3, modeChat},
		{"attachment-shaped token", "@report.pdf", 11, modeChat},
	}
	for _, c := range cases {
		w := newTestWorkspace(t)
		w.input.buf.setText(c.text, c.cursor)
		if got := w.input.currentMode(); got != c.want {
			t.Errorf("%s (%q cursor=%d): mode = %v, want %v",
				c.name, c.text, c.cursor, got, c.want)
		}
	}
}

// Driven through real keystrokes rather than setText, which is how the
// verification round hit it: content first, cursor home, then '@'.
func TestTypingAtBeforeExistingContentShowsPending(t *testing.T) {
	w := newTestWorkspace(t)
	// A real frame sets this; nothing has rendered in this test, and Home
	// wraps against it.
	w.input.textWidth = 60
	for _, r := range "hello world" {
		w.Update(keyMsg(string(r)))
	}
	w.Update(tea.KeyMsg{Type: tea.KeyHome})
	if w.input.buf.cursor != 0 {
		t.Fatalf("Home left the cursor at %d, want 0", w.input.buf.cursor)
	}

	w.Update(keyMsg("@"))
	if got := w.input.currentMode(); got != modePending {
		t.Fatalf("typing @ before existing content gave %v, want modePending", got)
	}
	for _, r := range "research" {
		w.Update(keyMsg(string(r)))
	}
	if got := w.input.currentMode(); got != modeResearch {
		t.Fatalf("completing @research gave %v, want modeResearch", got)
	}
	if got := w.input.buf.text(); got != "@researchhello world" {
		t.Fatalf("buffer = %q", got)
	}
}

// Mixed case activates the research chip, and that is CORRECT: verified
// against the real backend, AgentRegistry.resolve falls back to a
// case-insensitive NAME match after the exact id lookup misses, and every
// built-in's name lowercases to its id ("Research" -> "research"). The chip
// promises an agent that really will run.
func TestMixedCaseAgentTokenMatchesLikeTheBackend(t *testing.T) {
	for _, tok := range []string{"@research", "@RESEARCH", "@Research", "@researcH", "@REsearcH"} {
		w := newTestWorkspace(t)
		w.input.buf.setText(tok, len([]rune(tok)))
		if got := w.input.currentMode(); got != modeResearch {
			t.Errorf("%q: mode = %v, want modeResearch", tok, got)
		}
	}
	// Case folding must not invent a match that the backend would refuse.
	for _, tok := range []string{"@REPORT.PDF", "@Zzz"} {
		w := newTestWorkspace(t)
		w.input.buf.setText(tok, len([]rune(tok)))
		if got := w.input.currentMode(); got != modeChat {
			t.Errorf("%q: mode = %v, want modeChat", tok, got)
		}
	}
}
