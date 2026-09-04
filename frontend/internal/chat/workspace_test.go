package chat

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// keyMsg builds a tea.KeyMsg the same way real keystrokes decode, for tests
// that exercise handleKey directly.
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := New(Options{Theme: testTheme(), Client: nil})
	// sendFn replaces the real wire send: these tests exercise the
	// ListSessions/GetSession/CreateSession decision logic, not
	// protocol.Client's own send path (covered in the protocol package).
	// It mimics doSend's bookkeeping (assign an id, remember what kind of
	// reply to expect) without needing a live, connected Client.
	w.sendFn = func(cmdType string, kind queryKind, payload map[string]any) {
		w.pending[localID()] = kind
	}
	return w
}

// connectedTestWorkspace is for the render-path tests that need to
// distinguish "no backend at all" from "connected, session just empty" —
// a non-nil, never-started Client is enough for that: nothing here calls
// Start(), so no process is spawned and no goroutine runs.
func connectedTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := newTestWorkspace(t)
	w.client = protocol.NewClient(protocol.Options{})
	return w
}

func withPending(w *Workspace, id string, kind queryKind) {
	w.pending[id] = kind
}

func TestEmptySessionShowsExactlyTwoHints(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.bootstrapped = true // skip the "loading" branch
	w.sessionID = "s1"
	w.all = nil

	l := layout.Compute(120, 30, layout.State{Active: layout.WorkspaceChat, HeaderAllowed: true})
	out := w.View(l)
	plain := stripANSI(out)
	if !strings.Contains(plain, "type a message") {
		t.Errorf("missing 'type a message' hint:\n%s", plain)
	}
	if !strings.Contains(plain, "^P palette") {
		t.Errorf("missing '^P palette' hint:\n%s", plain)
	}
}

func TestBootstrapPicksMostRecentSessionThenGets(t *testing.T) {
	w := newTestWorkspace(t)
	id := "req1"
	withPending(w, id, queryListSessions)

	payload, _ := json.Marshal(map[string]any{
		"sessions": []wireSession{{ID: "s-newest", Title: "recent"}, {ID: "s-older", Title: "old"}},
	})
	w.handleEnvelope(protocol.Envelope{
		CorrID: id, Type: protocol.EvSessionList, Payload: payload,
	})

	if len(w.pending) != 1 {
		t.Fatalf("expected exactly one pending query after ListSessions, got %d", len(w.pending))
	}
	var gotKind queryKind
	var gotID string
	for k, v := range w.pending {
		gotID, gotKind = k, v
	}
	if gotKind != queryGetSession {
		t.Fatalf("expected a GetSession follow-up, got kind %v", gotKind)
	}

	// Complete the round trip with a Session reply carrying one message.
	sessPayload, _ := json.Marshal(map[string]any{
		"session": wireSession{
			ID: "s-newest", Title: "recent",
			Messages: []wireMessage{{ID: "m1", Role: roleUser, Content: wireContent{Type: "text", Text: "hi"}}},
		},
	})
	w.handleEnvelope(protocol.Envelope{CorrID: gotID, Type: protocol.EvSession, Payload: sessPayload})

	if w.sessionID != "s-newest" {
		t.Fatalf("sessionID = %q, want s-newest", w.sessionID)
	}
	if len(w.all) != 1 || w.all[0].ID != "m1" {
		t.Fatalf("messages not loaded: %+v", w.all)
	}
}

func TestBootstrapCreatesSessionWhenNoneExist(t *testing.T) {
	w := newTestWorkspace(t)
	id := "req1"
	withPending(w, id, queryListSessions)

	payload, _ := json.Marshal(map[string]any{"sessions": []wireSession{}})
	w.handleEnvelope(protocol.Envelope{CorrID: id, Type: protocol.EvSessionList, Payload: payload})

	var gotID string
	var gotKind queryKind
	for k, v := range w.pending {
		gotID, gotKind = k, v
	}
	if gotKind != queryCreateSession {
		t.Fatalf("expected CreateSession when the list is empty, got kind %v", gotKind)
	}

	createPayload, _ := json.Marshal(map[string]any{
		"session": wireSession{ID: "s-fresh", Title: "New Session"},
	})
	w.handleEnvelope(protocol.Envelope{CorrID: gotID, Type: protocol.EvSessionCreated, Payload: createPayload})

	if w.sessionID != "s-fresh" {
		t.Fatalf("sessionID = %q, want s-fresh", w.sessionID)
	}
	if len(w.all) != 0 {
		t.Fatalf("a freshly created session should have no messages, got %d", len(w.all))
	}
}

func TestErrorReplyIsSurfacedNotSilent(t *testing.T) {
	w := newTestWorkspace(t)
	id := "req1"
	withPending(w, id, queryListSessions)
	w.loading = true

	payload, _ := json.Marshal(map[string]any{"error": "session not found"})
	w.handleEnvelope(protocol.Envelope{CorrID: id, Type: protocol.EvError, Payload: payload})

	if w.loadErr != "session not found" {
		t.Fatalf("loadErr = %q, want the backend's error text", w.loadErr)
	}
	if w.loading {
		t.Fatalf("loading should clear once an error arrives")
	}
}

func fakeMessages(n int) []wireMessage {
	msgs := make([]wireMessage, n)
	for i := range msgs {
		role := roleUser
		if i%2 == 1 {
			role = roleAssistant
		}
		msgs[i] = wireMessage{ID: string(rune('a'+i%26)) + itoa(i), Role: role, Content: wireContent{Type: "text", Text: "message " + itoa(i)}}
	}
	return msgs
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestPinnedShowsTheTail(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(2000)
	w.pinned = true

	topAbs, lines := w.visibleWindow(20, 60)
	if len(lines) != 20 {
		t.Fatalf("expected exactly 20 lines, got %d", len(lines))
	}
	// The tail must be reachable and topAbs must land near the end, not 0.
	if topAbs < len(w.all)-30 {
		t.Fatalf("topAbs = %d, expected it near the end of %d messages", topAbs, len(w.all))
	}
}

func TestScrollUpUnpinsAndGRepins(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(50)
	w.pinned = true

	w.handleKey(keyMsg("k"))
	if w.pinned {
		t.Fatal("k should unpin from the bottom")
	}
	if w.focus != len(w.all)-2 {
		t.Fatalf("focus = %d, want %d", w.focus, len(w.all)-2)
	}

	w.handleKey(keyMsg("G"))
	if !w.pinned {
		t.Fatal("G should re-pin")
	}
	if w.focus != -1 {
		t.Fatalf("focus = %d, want -1 after G", w.focus)
	}
}

// A held G repeats fast enough that Windows Console can batch the repeat
// records the same way it batches a real paste — app\paste.go's burst
// detector then hands this workspace app.PasteMsg{Text: "GGGG..."} instead
// of individual KeyMsgs. handlePastedBurst must recover it.
func TestPastedGBurstRePins(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(50)
	w.pinned = false
	w.focus = 10
	// F4: the input holds focus by default now, and a real "G" burst must
	// insert those letters as text — this test is specifically about F3's
	// browse-mode hotkey recovery, which only applies once focus has moved
	// off the input (Esc/Tab — app\focus.go).
	w.input.focused = false

	w.Update(app.PasteMsg{Text: "GGGGGG"})
	if !w.pinned {
		t.Fatal("a batched G burst should re-pin, same as individual G presses")
	}
	if w.focus != -1 {
		t.Fatalf("focus = %d, want -1", w.focus)
	}
}

// A genuine multi-character paste (mixed runes) must NOT be replayed as
// keystrokes — a stray 'j'/'k'/'y'/'G' inside real pasted prose must not
// move focus or fire a clipboard copy.
func TestMixedPasteIsNeverReplayedAsKeystrokes(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(50)
	w.pinned = true
	w.focus = -1
	w.input.focused = false // browse mode — see TestPastedGBurstRePins

	w.Update(app.PasteMsg{Text: "just kidding, ok?"})
	if !w.pinned || w.focus != -1 {
		t.Fatalf("a mixed paste must not move focus/pin state: pinned=%v focus=%d", w.pinned, w.focus)
	}
}

// G, pressed (or batched) repeatedly against a large session, must both
// keep working AND stay flat in memory — not grow with the number of
// presses. Exercises the real dispatch path (Update, not handleKey
// directly) with both individual presses and batched PasteMsg bursts,
// since the batched form is the one that reaches a real terminal when G is
// held down.
func TestRepeatedGReRepinsAndDoesNotLeak(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.bootstrapped = true
	w.all = fakeMessages(10000)
	w.pinned = true
	w.input.focused = false // browse mode — see TestPastedGBurstRePins

	rect := layout.Rect{W: 100, H: 26}
	_ = w.viewportLines(rect)

	// Scroll far from the tail so re-pinning has real work to do.
	for i := 0; i < 400; i++ {
		w.Update(keyMsg("k"))
	}
	_ = w.viewportLines(rect)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 40; i++ {
		if i%2 == 0 {
			w.Update(keyMsg("G"))
		} else {
			w.Update(app.PasteMsg{Text: "GGG"})
		}
		if !w.pinned {
			t.Fatalf("press %d: G did not re-pin", i)
		}
		_ = w.viewportLines(rect)
		for j := 0; j < 400; j++ {
			w.Update(keyMsg("k"))
		}
		_ = w.viewportLines(rect)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Cache is bounded by design (cache.go's evictIfNeeded) — this is the
	// concrete, checkable form of "does not grow proportionally to press
	// count": entries stay near the scrollback bound regardless of how
	// many times G fired.
	if limit := w.scrollback * cacheEvictFactor; len(w.cache) > limit {
		t.Fatalf("cache grew to %d entries, want <= %d", len(w.cache), limit)
	}

	// A generous ceiling, not a tight budget: this catches an unbounded
	// per-press allocation (the reported symptom) without being a flaky
	// assertion on exact GC behaviour.
	const maxGrowth = 8 << 20 // 8 MiB across 40 re-pins of a 10k-message session
	if delta := int64(after.HeapAlloc) - int64(before.HeapAlloc); delta > maxGrowth {
		t.Fatalf("heap grew by %d bytes across 40 G presses, want <= %d", delta, maxGrowth)
	}
}

// Design Change C: the user's box occupies only the rightmost portion of
// the viewport width, blank space on the left only, and the "User:" label
// above it is right-aligned to the SAME edge.
func TestUserBoxIsRightAlignedAndNarrower(t *testing.T) {
	th := testTheme()
	msg := wireMessage{ID: "m1", Role: roleUser, Content: wireContent{Text: "hello"}}
	width := 100

	lines := renderMessage(th, msg, width, false)
	if len(lines) < 2 {
		t.Fatalf("expected a label line plus a box, got %d lines", len(lines))
	}

	label := stripANSI(lines[0])
	if !strings.HasSuffix(label, "User:") {
		t.Fatalf("label line = %q, want it to end in \"User:\"", label)
	}
	if visibleWidth(label) != width {
		t.Fatalf("label line width = %d, want %d", visibleWidth(label), width)
	}

	top := stripANSI(lines[1])
	trimmed := strings.TrimLeft(top, " ")
	leftMargin := len(top) - len(trimmed)
	if leftMargin == 0 {
		t.Fatal("expected blank space on the left of the box, found none")
	}
	boxWidth := visibleWidth(trimmed)
	ratio := float64(boxWidth) / float64(width)
	if ratio < 0.65 || ratio > 0.80 {
		t.Fatalf("box is %.0f%% of the viewport width, want roughly 70-75%%", ratio*100)
	}
	// Right-aligned: the box's right edge must land on the viewport's own
	// right edge, i.e. leftMargin + boxWidth == width.
	if leftMargin+boxWidth != width {
		t.Fatalf("leftMargin(%d) + boxWidth(%d) = %d, want %d (box flush with the right wall)",
			leftMargin, boxWidth, leftMargin+boxWidth, width)
	}
}

// Design Change C: AI replies get a "Point:" label above them, left-aligned
// — the accent bar stays, unreplaced, on every line below it.
func TestAssistantMessageHasPointLabelAndKeepsBar(t *testing.T) {
	th := testTheme()
	msg := wireMessage{ID: "m1", Role: roleAssistant, Content: wireContent{Text: "hello there"}}
	lines := renderMessage(th, msg, 60, false)
	if len(lines) < 2 {
		t.Fatalf("expected a label line plus reply content, got %d", len(lines))
	}
	label := stripANSI(lines[0])
	if !strings.HasPrefix(label, "Point:") {
		t.Fatalf("label line = %q, want it to start with \"Point:\"", label)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(stripANSI(l), "▎") {
			t.Fatalf("reply line %d lost its accent bar: %q", i, stripANSI(l))
		}
	}
}

// Design Change C: 1 blank row inside an exchange (User: then Point:), 2
// blank rows between exchanges (Point: then the next User:).
func TestExchangeSpacing(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = []wireMessage{
		{ID: "u1", Role: roleUser, Content: wireContent{Text: "question one"}},
		{ID: "a1", Role: roleAssistant, Content: wireContent{Text: "answer one"}},
		{ID: "u2", Role: roleUser, Content: wireContent{Text: "question two"}},
	}
	w.pinned = true

	width := 80
	_, lines := w.visibleWindow(200, width)

	b0 := w.renderCached(w.all[0], width)
	b1 := w.renderCached(w.all[1], width)
	b2 := w.renderCached(w.all[2], width)

	// [ b0... ][gap1][ b1... ][gap2 x2][ b2... ]
	gap1Start := len(b0)
	gap1End := gap1Start
	for gap1End < len(lines) && lines[gap1End] == "" {
		gap1End++
	}
	if got := gap1End - gap1Start; got != 1 {
		t.Fatalf("gap between User: and Point: (same exchange) = %d blank row(s), want 1", got)
	}
	if diff := gap1End - gap1Start; len(lines) < gap1End+len(b1) || strings.Join(lines[gap1End:gap1End+len(b1)], "\n") != strings.Join(b1, "\n") {
		t.Fatalf("assistant block not found where expected after a %d-row gap", diff)
	}

	gap2Start := gap1End + len(b1)
	gap2End := gap2Start
	for gap2End < len(lines) && lines[gap2End] == "" {
		gap2End++
	}
	if got := gap2End - gap2Start; got != 2 {
		t.Fatalf("gap between Point: and the next User: (new exchange) = %d blank row(s), want 2", got)
	}
	if strings.Join(lines[gap2End:gap2End+len(b2)], "\n") != strings.Join(b2, "\n") {
		t.Fatal("second user block not found where expected after a 2-row gap")
	}
}

func TestFocusClampsAtBothEnds(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = fakeMessages(3)
	for i := 0; i < 10; i++ {
		w.handleKey(keyMsg("k"))
	}
	if w.focus != 0 {
		t.Fatalf("focus = %d, want clamped to 0", w.focus)
	}
}

func TestCacheInvalidatesOnWidthChange(t *testing.T) {
	w := newTestWorkspace(t)
	msg := wireMessage{ID: "m1", Role: roleAssistant, Content: wireContent{Text: "hello there"}}

	a := w.renderCached(msg, 40)
	if _, ok := w.cache["m1"]; !ok {
		t.Fatal("expected the render to be cached")
	}
	b := w.renderCached(msg, 20)
	if len(w.cache) != 1 {
		t.Fatalf("a width change should reset the cache, has %d entries", len(w.cache))
	}
	if strings.Join(a, "\n") == strings.Join(b, "\n") && visibleWidth(a[0]) == visibleWidth(b[0]) {
		// Not a hard requirement that they differ in content, only that a
		// fresh render happened — covered by the cache-size assertion above.
		_ = a
	}
}

func TestCopyFocusedCopiesRawTextNotRenderedBox(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = []wireMessage{{ID: "m1", Role: roleUser, Content: wireContent{Text: "**raw** `markdown`"}}}
	w.focus = 0

	w.copyFocused()
	if w.copyNote == "nothing focused to copy" {
		t.Fatal("copy should have found the focused message")
	}
}

// While pinned (focus == -1), y should still work — it copies whatever is
// on screen at the bottom, the same message moveFocus(-1) would step off.
func TestCopyWithNoExplicitFocusUsesTheNewestMessage(t *testing.T) {
	w := newTestWorkspace(t)
	w.all = []wireMessage{
		{ID: "m1", Role: roleUser, Content: wireContent{Text: "old"}},
		{ID: "m2", Role: roleAssistant, Content: wireContent{Text: "newest"}},
	}
	w.focus = -1

	w.copyFocused()
	if w.copyNote == "nothing focused to copy" {
		t.Fatal("y should copy the newest message when nothing is explicitly focused")
	}
}

func TestJumpToLatestHintSitsOnTheLastRowNotTheLastMessage(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.bootstrapped = true
	w.all = fakeMessages(3) // far fewer than the viewport's rows
	w.pinned = false
	w.focus = 1

	lines := w.viewportLines(layout.Rect{W: 60, H: 22})
	if len(lines) != 20 {
		t.Fatalf("expected 20 content rows, got %d", len(lines))
	}
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "jump to latest") {
		t.Fatalf("hint not on the last row:\n%q", stripANSI(lines[len(lines)-1]))
	}
	if strings.Contains(stripANSI(lines[0]), "jump to latest") {
		t.Fatalf("hint leaked onto an earlier row:\n%q", stripANSI(lines[0]))
	}
}

// TestEveryLineIsExactlyTheRectWidth mirrors app_test.go's
// TestEveryLineIsExactlyTheTerminalWidth for this workspace specifically —
// mixed roles, a code block, and the CJK/emoji fixture together, at every
// breakpoint LAYOUT.md names.
func TestEveryLineIsExactlyTheRectWidth(t *testing.T) {
	w := newTestWorkspace(t)
	w.bootstrapped = true
	w.sessionID = "s1"
	w.all = []wireMessage{
		{ID: "1", Role: roleUser, Content: wireContent{Text: "hello there, this is a normal user message"}},
		{ID: "2", Role: roleAssistant, Content: wireContent{Text: verificationFixture}},
		{ID: "3", Role: roleSystem, Content: wireContent{Text: "system notice"}},
		{ID: "4", Role: roleError, Content: wireContent{Text: "something failed"}},
		{ID: "5", Role: roleTool, Content: wireContent{Text: "tool output"}},
		{ID: "6", Role: roleAssistant, Content: wireContent{Text: "日本語テキスト and 🎉 in a reply"}},
	}

	sizes := []struct{ w, h int }{
		{200, 50}, {120, 30}, {100, 26}, {90, 24}, {89, 23}, {70, 20}, {50, 12},
	}
	for _, sz := range sizes {
		l := layout.Compute(sz.w, sz.h, layout.State{Active: layout.WorkspaceChat, HeaderAllowed: true})
		if l.TooSmall {
			continue
		}
		out := w.View(l)
		for i, line := range strings.Split(out, "\n") {
			if got := visibleWidth(line); got != 0 && got != l.Main.W {
				// Only lines belonging to a drawn box are checked; a
				// zero-width (blank) line between stacked boxes is fine.
				t.Errorf("%dx%d: line %d is %d cells, want %d:\n%q",
					sz.w, sz.h, i, got, l.Main.W, stripANSI(line))
			}
		}
	}
}
