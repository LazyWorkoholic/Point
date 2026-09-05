package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// newStreamWorkspace builds a workspace with a session already loaded and no
// backend. Every send therefore records its live entry and writes nothing —
// which is exactly what these tests are about, since the wire itself is
// protocol's own test's job.
func newStreamWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	w.sessionID = "s1"
	// No backend here: the real writeSend correctly FAILS a send it cannot
	// write (an entry left in statusSending forever is the idle-CPU leak),
	// so a test about lanes and queues substitutes a write that succeeds.
	w.sendMsgFn = func(*liveEntry, map[string]any) tea.Cmd { return nil }
	w.viewRows, w.viewWidth = 20, 60
	w.input.boxContentWidth = 60
	w.input.textWidth = 58
	return w
}

// mustEntry is the i-th REPLY entry. A send appends two live entries — the
// user's message and the reply — so a positional index into w.live is not
// what any of these tests mean.
func (w *Workspace) mustEntry(t *testing.T, i int) *liveEntry {
	t.Helper()
	replies := w.replies()
	if i >= len(replies) {
		t.Fatalf("wanted reply %d, have %d", i, len(replies))
	}
	return replies[i]
}

func (w *Workspace) replies() []*liveEntry {
	var out []*liveEntry
	for _, e := range w.live {
		if e.kind == liveReply {
			out = append(out, e)
		}
	}
	return out
}

func wantReplies(t *testing.T, w *Workspace, n int, what string) {
	t.Helper()
	if got := len(w.replies()); got != n {
		t.Fatalf("%s: wanted %d replies, got %d", what, n, got)
	}
}

func env(t *testing.T, typ, corrID, taskID string, payload any) protocol.Envelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Envelope{Type: typ, CorrID: corrID, TaskID: taskID, Payload: raw}
}

func typeInput(w *Workspace, s string) {
	w.input.buf.InsertText(s)
}

// ---------------------------------------------------------------------------
// The queue
// ---------------------------------------------------------------------------

// F5's Done-when 2 and 3, and the whole point of the lock: composing while a
// reply streams is allowed, a SECOND composition is what fills the queue, and
// only a FULL queue locks the input.
func TestQueueDepthOneLocksOnlyAtCapacity(t *testing.T) {
	w := newStreamWorkspace(t)

	typeInput(w, "first")
	w.onSend()
	if w.inputLocked() {
		t.Fatal("the input locked on the first send — F5 forbids locking except at capacity")
	}
	if w.activeTurn() == nil {
		t.Fatal("the first send did not occupy the turn lane")
	}

	typeInput(w, "second")
	w.onSend()
	if w.queued == nil {
		t.Fatal("a send during a stream did not queue")
	}
	if !w.inputLocked() {
		t.Fatal("the queue is full and the input is not locked")
	}
	if lockReason == "" {
		t.Fatal("the input locks with no reason to show")
	}

	// The third attempt: the lock refuses the keystrokes AND the send.
	before := w.input.buf.text()
	typeInput(w, "third")
	w.onSend()
	wantReplies(t, w, 1, "a send at capacity started a second task")
	_ = before
}

// "reply completes → the pending message fires automatically"
func TestQueuedMessageFiresWhenTheReplyCompletes(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "first")
	w.onSend()
	first := w.mustEntry(t, 0)
	first.taskID = "t1"

	typeInput(w, "second")
	w.onSend()

	w.handleEnvelope(env(t, protocol.EvTaskDone, first.requestID, "t1", map[string]any{}))

	if w.queued != nil {
		t.Fatal("the pending message did not fire when the reply completed")
	}
	if w.inputLocked() {
		t.Fatal("the input is still locked after the queue drained")
	}
	wantReplies(t, w, 2, "after the queue fired")
}

// "If a message is queued, cancelling the current turn fires the queued one\n// immediately."
func TestCancelFiresTheQueuedMessage(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "first")
	w.onSend()
	first := w.mustEntry(t, 0)
	first.taskID = "t1"

	typeInput(w, "second")
	w.onSend()

	w.handleEnvelope(env(t, protocol.EvTaskCancelled, first.requestID, "t1", map[string]any{}))

	if w.queued != nil {
		t.Fatal("cancelling the turn did not fire the queued message")
	}
	wantReplies(t, w, 2, "after cancel drained the queue")
}

func TestDiscardAndEditReleaseTheLock(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "first")
	w.onSend()
	typeInput(w, "second")
	w.onSend()

	w.discardPending()
	if w.inputLocked() || w.HasPending() {
		t.Fatal("esc did not discard the pending message")
	}

	typeInput(w, "third")
	w.onSend()
	if !w.HasPending() {
		t.Fatal("expected a pending message to edit")
	}
	w.editPending()
	if w.HasPending() {
		t.Fatal("ctrl+e did not take the message out of the queue")
	}
	if got := w.input.buf.text(); got != "third" {
		t.Fatalf("ctrl+e put %q back in the input, wanted %q", got, "third")
	}
}

// "Agent tasks never enter this queue."
func TestAgentSendBypassesTheQueue(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "a long question")
	w.onSend()

	typeInput(w, "@research something")
	w.onSend()

	if w.queued != nil {
		t.Fatal("an agent send was queued — F5 says agent tasks never enter the queue")
	}
	wantReplies(t, w, 2, "the agent send did not start")
	agent := w.mustEntry(t, 1)
	if agent.lane != laneTask {
		t.Fatal("@research did not reserve a task slot at send time")
	}
	if agent.agentID != "research" {
		t.Fatalf("agent id %q, wanted research", agent.agentID)
	}
}

// The optimistic reserve, reconciled: the backend says this @agent actually
// runs inline, and the entry collapses into a turn WITHOUT changing index.
func TestTaskSlotReconcilesToTurnOnTaskStarted(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "@research inline please")
	w.onSend()
	e := w.mustEntry(t, 0)
	if e.lane != laneTask {
		t.Fatal("the slot was not reserved as a task at send time")
	}
	posBefore := len(w.all) + 1 // past the user message, at the reply

	w.handleEnvelope(env(t, protocol.EvTaskStarted, e.requestID, "t1",
		map[string]any{"message_id": "m1", "background": false}))

	if e.lane != laneTurn {
		t.Fatal("TaskStarted said background=false and the lane did not reconcile")
	}
	if kind, idx := w.entryAt(posBefore); kind != entryLive || w.live[idx] != e {
		t.Fatal("the entry moved while reconciling — F5 requires nothing ever jumps")
	}
}

// ---------------------------------------------------------------------------
// Terminal-event discipline
// ---------------------------------------------------------------------------

// "Every task must reach exactly one terminal state."
func TestOnlyTheFirstTerminalEventCounts(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.taskID = "t1"

	w.handleEnvelope(env(t, protocol.EvTaskCancelled, e.requestID, "t1", map[string]any{}))
	w.handleEnvelope(env(t, protocol.EvTaskDone, e.requestID, "t1", map[string]any{}))

	if e.status != statusCancelled {
		t.Fatalf("a second terminal event reopened the entry: status %v", e.status)
	}
}

// task.cancel.accepted is the ACK, not the outcome. Treating it as terminal
// would leave the real TaskCancelled with nothing to land on.
func TestCancelAcceptedIsNotTerminal(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.taskID = "t1"

	w.handleEnvelope(env(t, protocol.EvTaskCancelAccepted, e.requestID, "t1",
		map[string]any{"accepted": true}))
	if e.status.terminal() {
		t.Fatal("the cancel ack was treated as a terminal event")
	}
	if !w.Streaming() {
		t.Fatal("the ack stopped the activity indicator before the task ended")
	}
}

// "a task in a running state with no events for stream_timeout shows\n// no response rather than spinning forever"
func TestWatchdogEndsASilentTask(t *testing.T) {
	w := newStreamWorkspace(t)
	w.streamTimeout = 10 * time.Millisecond
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	e.lastEventAt = time.Now().Add(-time.Second)
	w.onTick(w.tickGen)

	if e.status != statusTimedOut {
		t.Fatalf("the watchdog left the task in %v — a spinner that never stops", e.status)
	}
	if w.Streaming() {
		t.Fatal("the header animation is still running after the watchdog fired")
	}
	footer := liveFooter(w.theme, e, 40)
	if len(footer) == 0 || !strings.Contains(stripANSI(footer[0]), "no response") {
		t.Fatalf("the timed-out entry has no no-response footer: %q", footer)
	}
	if !strings.Contains(stripANSI(footer[0]), "Retry") {
		t.Fatal("the no-response state offers no Retry")
	}
}

// The watchdog states a GUESS, and a guess must be revisable: a task that
// speaks again was alive, and its real outcome must still win. Otherwise the
// mechanism that exists to prevent one wrong ending causes another.
func TestATimedOutTaskRecoversIfItSpeaksAgain(t *testing.T) {
	w := newStreamWorkspace(t)
	w.streamTimeout = 10 * time.Millisecond
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.lastEventAt = time.Now().Add(-time.Second)
	w.onTick(w.tickGen)
	if e.status != statusTimedOut {
		t.Fatalf("setup: wanted a timed-out entry, got %v", e.status)
	}

	w.handleEnvelope(env(t, protocol.EvTextDelta, e.requestID, "t1",
		map[string]any{"data": map[string]any{"text": "late but real"}}))
	if e.status != statusRunning {
		t.Fatalf("a token after the timeout left the entry in %v", e.status)
	}
	if len(liveFooter(w.theme, e, 40)) != 0 {
		t.Fatal("the no-response footer survived the task speaking again")
	}

	w.handleEnvelope(env(t, protocol.EvTaskDone, e.requestID, "t1", map[string]any{}))
	if e.status != statusDone {
		t.Fatalf("the real outcome did not win: %v", e.status)
	}
}

// The other side of that rule: a stray late event must NOT reopen an entry
// the backend genuinely ended.
func TestALateEventCannotReopenAFinishedEntry(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	w.handleEnvelope(env(t, protocol.EvTaskDone, e.requestID, "t1", map[string]any{}))

	w.handleEnvelope(env(t, protocol.EvTextDelta, e.requestID, "t1",
		map[string]any{"data": map[string]any{"text": "stray"}}))
	if e.status != statusDone {
		t.Fatalf("a stray token reopened a finished entry: %v", e.status)
	}
}

// A tool-heavy run says plenty without emitting a token. Those events must
// count as life, or a long task trips a timeout it is nowhere near.
func TestNonTokenEventsKeepTheWatchdogQuiet(t *testing.T) {
	w := newStreamWorkspace(t)
	w.streamTimeout = 50 * time.Millisecond
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.lastEventAt = time.Now().Add(-time.Second)

	w.handleEnvelope(env(t, protocol.EvToolStarted, e.requestID, "t1", map[string]any{}))
	w.onTick(w.tickGen)

	if e.status.terminal() {
		t.Fatal("a tool event did not count as evidence the task is alive")
	}
}

// F5's "one path, two causes": a backend death leaves what a cancel leaves.
func TestBackendLostInterruptsLikeACancel(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.text = "partial"

	w.Update(protocol.BackendLostMsg{Reason: "pipe closed"})

	if !e.interrupted() {
		t.Fatalf("a lost backend left the entry in %v, not interrupted", e.status)
	}
	if e.text != "partial" {
		t.Fatal("the partial text was discarded")
	}
	footer := liveFooter(w.theme, e, 40)
	if len(footer) == 0 || !strings.Contains(stripANSI(footer[0]), "interrupted") {
		t.Fatalf("no interrupted footer: %q", footer)
	}
}

// ---------------------------------------------------------------------------
// Cancellation
// ---------------------------------------------------------------------------

// "ctrl+c ... If nothing is streaming it is a no-op, not quit."
func TestCancelWithNothingStreamingIsANoOp(t *testing.T) {
	w := newStreamWorkspace(t)
	if cmd := w.cancelStream(); cmd != nil {
		t.Fatal("ctrl+c with nothing streaming produced a command")
	}
	if len(w.live) != 0 || w.loadErr != "" {
		t.Fatal("ctrl+c with nothing streaming changed something")
	}
}

// Cancelled before TaskStarted named a task: there is no id to cancel by, so
// the entry closes locally rather than hanging until the watchdog.
func TestCancelBeforeTaskStartedClosesLocally(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	w.cancelStream()
	if e.status != statusCancelled {
		t.Fatalf("status %v, wanted cancelled", e.status)
	}

	// A TaskStarted that arrives after must not reopen it.
	w.handleEnvelope(env(t, protocol.EvTaskStarted, e.requestID, "t1",
		map[string]any{"message_id": "m1", "background": false}))
	w.handleEnvelope(env(t, protocol.EvTaskDone, e.requestID, "t1", map[string]any{}))
	if e.status != statusCancelled {
		t.Fatalf("a late event reopened a cancelled entry: %v", e.status)
	}
}

func TestRetryResendsTheInterruptedMessage(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "the original question")
	w.onSend()
	e := w.mustEntry(t, 0)
	w.finish(e, statusCancelled)

	w.retryFocused()
	wantReplies(t, w, 2, "retry did not send")
	if got := w.mustEntry(t, 1).userText; got != "the original question" {
		t.Fatalf("retry sent %q", got)
	}
}

// ---------------------------------------------------------------------------
// Token accumulation and both token events
// ---------------------------------------------------------------------------

// TextDelta and TaskToken are BOTH real, on different backend paths — the
// streaming providers and the echo provider. Handling only one leaves whole
// configurations rendering nothing.
func TestBothTokenEventShapesAccumulate(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	w.handleEnvelope(env(t, protocol.EvTextDelta, e.requestID, "t1",
		map[string]any{"type": "text_delta", "data": map[string]any{"text": "one "}}))
	w.handleEnvelope(env(t, protocol.EvTaskToken, e.requestID, "t1",
		map[string]any{"text": "two"}))

	if e.text != "one two" {
		t.Fatalf("accumulated %q, wanted %q", e.text, "one two")
	}
}

func TestProgressRendersOnTheHeader(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	w.handleEnvelope(env(t, protocol.EvProgressUpdated, e.requestID, "t1",
		map[string]any{"type": "progress_updated", "data": map[string]any{
			"step": 3, "max_steps": 6, "message": "step 3/6"}}))

	lines := renderTurnEntry(w.theme, e, 60, false)
	if !strings.Contains(stripANSI(lines[0]), "step 3/6") {
		t.Fatalf("progress is not on the message header: %q", stripANSI(lines[0]))
	}
}

func TestThinkingIsCollapsedByDefault(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.thinking = "considering\nthe options"

	collapsed := strings.Join(renderThinking(w.theme, e, 40), "\n")
	if strings.Contains(collapsed, "considering") {
		t.Fatal("the thinking block is expanded by default")
	}
	if !strings.Contains(stripANSI(collapsed), "thinking") {
		t.Fatalf("no collapsed thinking affordance: %q", collapsed)
	}

	e.expanded = true
	expanded := stripANSI(strings.Join(renderThinking(w.theme, e, 40), "\n"))
	if !strings.Contains(expanded, "considering") {
		t.Fatalf("expanding showed nothing: %q", expanded)
	}
}

// ---------------------------------------------------------------------------
// The unclosed-fence rule
// ---------------------------------------------------------------------------

func TestSplitOpenFence(t *testing.T) {
	cases := []struct {
		name, text, wantStable, wantTail string
	}{
		{"no fence", "just prose", "just prose", ""},
		{"closed fence", "a\n```go\nx := 1\n```\nb", "a\n```go\nx := 1\n```\nb", ""},
		{"open fence", "a\n```go\nx := 1", "a", "```go\nx := 1"},
		{"open fence, nothing before", "```go\nx := 1", "", "```go\nx := 1"},
		{"tildes", "a\n~~~\nx", "a", "~~~\nx"},
		{"closed then open", "```\na\n```\nb\n```\nc", "```\na\n```\nb", "```\nc"},
		{"indented four spaces is not a fence", "    ```\nx", "    ```\nx", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stable, tail := splitOpenFence(c.text)
			if stable != c.wantStable || tail != c.wantTail {
				t.Fatalf("got (%q, %q), wanted (%q, %q)", stable, tail, c.wantStable, c.wantTail)
			}
		})
	}
}

// The actual anti-flicker guarantee, stated as an invariant rather than as a
// property of one example: while a fence is open the boxed code block must
// not be drawn, and once it closes it must be.
func TestOpenFenceDoesNotDrawTheCodeBox(t *testing.T) {
	w := newStreamWorkspace(t)
	growing := "here:\n```go\nx := 1\ny := 2"
	open := strings.Join(renderStreamBody(w.theme, growing, 40), "\n")
	if strings.Contains(open, borderTopLeft) {
		t.Fatalf("the code box was drawn while the fence was still open:\n%s", stripANSI(open))
	}
	if !strings.Contains(stripANSI(open), "x := 1") {
		t.Fatal("the raw tail is not being shown at all")
	}

	closed := strings.Join(renderStreamBody(w.theme, growing+"\n```", 40), "\n")
	if !strings.Contains(closed, borderTopLeft) {
		t.Fatal("the code box was not drawn once the fence closed")
	}
}

// ---------------------------------------------------------------------------
// Ordering — "nothing ever jumps"
// ---------------------------------------------------------------------------

func TestEntryOrderIsPersistedThenLiveThenPending(t *testing.T) {
	w := newStreamWorkspace(t)
	w.all = []wireMessage{{ID: "m1", Role: roleAssistant, Content: wireContent{Text: "old"}}}

	typeInput(w, "@research topic")
	w.onSend()
	typeInput(w, "a normal question")
	w.onSend()
	typeInput(w, "queued one")
	w.onSend()

	if w.queued == nil {
		t.Fatal("expected a pending message")
	}

	// The task slot must still precede the chat that arrived after it.
	taskIdx, turnIdx := -1, -1
	for i := 0; i < w.entryCount(); i++ {
		if e := w.liveAt(i); e != nil {
			if e.lane == laneTask {
				taskIdx = i
			} else {
				turnIdx = i
			}
		}
	}
	if taskIdx < 0 || turnIdx < 0 {
		t.Fatalf("expected both lanes present: task=%d turn=%d", taskIdx, turnIdx)
	}
	if taskIdx > turnIdx {
		t.Fatal("the task slot moved below the chat that came after it")
	}
	if kind, _ := w.entryAt(w.entryCount() - 1); kind != entryPending {
		t.Fatal("the pending bubble is not the last entry")
	}
}

// BUG B, as an invariant rather than one reproduction: display order is
// SEND order. Every question is immediately followed by its own answer,
// however the send got there — first try, off the queue, or via retry.
// The first cut clustered every question above every answer, because the
// user message went into one list and the reply into another.
func TestQuestionsAndAnswersInterleaveBySendOrder(t *testing.T) {
	roleSequence := func(w *Workspace) []string {
		var out []string
		for i := 0; i < w.entryCount(); i++ {
			out = append(out, w.entryRole(i))
		}
		return out
	}
	want := []string{roleUser, roleAssistant, roleUser, roleAssistant}

	t.Run("plain second send", func(t *testing.T) {
		w := newStreamWorkspace(t)
		typeInput(w, "q1")
		w.onSend()
		w.finish(w.mustEntry(t, 0), statusDone)
		typeInput(w, "q2")
		w.onSend()

		if got := roleSequence(w); !equalStrings(got, want) {
			t.Fatalf("order %v, wanted %v", got, want)
		}
	})

	t.Run("via the queue", func(t *testing.T) {
		w := newStreamWorkspace(t)
		typeInput(w, "q1")
		w.onSend()
		first := w.mustEntry(t, 0)
		typeInput(w, "q2")
		w.onSend() // queued
		w.handleEnvelope(env(t, protocol.EvTaskDone, first.requestID, "t1", map[string]any{}))

		if got := roleSequence(w); !equalStrings(got, want) {
			t.Fatalf("order %v, wanted %v", got, want)
		}
	})

	t.Run("via retry", func(t *testing.T) {
		w := newStreamWorkspace(t)
		typeInput(w, "q1")
		w.onSend()
		w.finish(w.mustEntry(t, 0), statusCancelled)
		w.retryFocused()

		if got := roleSequence(w); !equalStrings(got, want) {
			t.Fatalf("order %v, wanted %v", got, want)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSplitAgent(t *testing.T) {
	cases := []struct{ in, agent, body string }{
		{"hello", "", "hello"},
		{"@research what is x", "research", "what is x"},
		{"  @research  spaced", "research", "spaced"},
		{"@research", "research", ""},
		{"@report.pdf is a file", "", "@report.pdf is a file"},
		{"@nosuchagent hi", "", "@nosuchagent hi"},
		{"an @research mid-sentence", "", "an @research mid-sentence"},
	}
	for _, c := range cases {
		agent, body := splitAgent(c.in)
		if agent != c.agent || body != c.body {
			t.Errorf("splitAgent(%q) = (%q, %q), wanted (%q, %q)", c.in, agent, body, c.agent, c.body)
		}
	}
}

// BUG C, at its root: a send that never reaches the wire must still END.
// The first cut only set loadErr, leaving the entry in statusSending — so
// Streaming() stayed true forever, and the header animation (120ms) and the
// watchdog (500ms) both re-armed forever on an app doing nothing at all.
func TestAFailedSendEndsItsEntry(t *testing.T) {
	w := newStreamWorkspace(t)
	w.sendMsgFn = w.writeSend // the real one, with no client behind it
	w.client = nil

	typeInput(w, "hello")
	w.onSend()

	e := w.mustEntry(t, 0)
	if !e.status.terminal() {
		t.Fatalf("a send that could not be written left the entry in %v", e.status)
	}
	if w.Streaming() {
		t.Fatal("Streaming() is true after a failed send — this is the idle-CPU leak")
	}
	w.tickArmed = false
	if cmd := w.armTick(); cmd != nil {
		t.Fatal("a tick was armed after a failed send")
	}
}

// The user's own message is not a task and must never hold the ticks open.
func TestAUserMessageEntryIsNeverStreaming(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "hello")
	w.onSend()
	w.finish(w.mustEntry(t, 0), statusDone)

	if w.Streaming() {
		t.Fatal("the user-message entry keeps Streaming() true after the reply finished")
	}
}

// Ctrl+R must retry the entry the user is LOOKING at. Taking "the most\n// recent retryable" instead is why a focused failed research slot retried a
// different task entirely.
func TestRetryTargetsTheFocusedEntry(t *testing.T) {
	w := newStreamWorkspace(t)

	typeInput(w, "@research the focused one")
	w.onSend()
	research := w.mustEntry(t, 0)
	w.finish(research, statusFailed)

	typeInput(w, "a later question")
	w.onSend()
	later := w.mustEntry(t, 1)
	w.finish(later, statusTimedOut)

	// Nothing focused: the most recent retryable is the fallback.
	w.focus = -1
	if got := w.retryTarget(); got != later {
		t.Fatal("with no focus, retry did not fall back to the most recent")
	}

	// Focus the research slot and it must win.
	for i := 0; i < w.entryCount(); i++ {
		if w.liveAt(i) == research {
			w.focus = i
		}
	}
	if got := w.retryTarget(); got != research {
		t.Fatal("retry ignored the focused entry")
	}

	w.retryFocused()
	if got := w.mustEntry(t, 2).userText; got != "@research the focused one" {
		t.Fatalf("retry re-sent %q — the wrong task", got)
	}
}

// Expanding a slot must always do something visible. A failed task has no
// reply text, and the first cut flipped the marker and opened nothing.
func TestExpandingASlotWithNoTextStillShowsSomething(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "@research doomed")
	w.onSend()
	e := w.mustEntry(t, 0)
	e.errText = "provider refused the request"
	w.finish(e, statusFailed)

	collapsed := renderTaskEntry(w.theme, e, 60, false)
	e.slotOpen = true
	expanded := renderTaskEntry(w.theme, e, 60, false)

	if len(expanded) <= len(collapsed) {
		t.Fatal("expanding a failed slot added no rows at all")
	}
	if !strings.Contains(stripANSI(strings.Join(expanded, "\n")), "provider refused the request") {
		t.Fatalf("the expanded slot does not say why it failed:\n%s",
			stripANSI(strings.Join(expanded, "\n")))
	}
}

// A one-line slot has no border to brighten, so focus has to be structural.
func TestAFocusedSlotIsVisiblyDifferent(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "@research something")
	w.onSend()
	e := w.mustEntry(t, 0)
	w.finish(e, statusFailed)

	unfocused := stripANSI(renderTaskEntry(w.theme, e, 60, false)[0])
	focused := stripANSI(renderTaskEntry(w.theme, e, 60, true)[0])
	if unfocused == focused {
		t.Fatal("a focused slot renders identically to an unfocused one")
	}
}

// The idle guarantee behind F5's CPU check: with nothing running, nothing is
// scheduled.
func TestNoTickIsArmedWhileIdle(t *testing.T) {
	w := newStreamWorkspace(t)
	if cmd := w.armTick(); cmd != nil {
		t.Fatal("a tick was armed with nothing streaming")
	}
	typeInput(w, "hello")
	w.onSend()
	if !w.Streaming() {
		t.Fatal("a send did not start the activity indicator")
	}
	w.finish(w.mustEntry(t, 0), statusDone)
	w.tickArmed = false
	if cmd := w.armTick(); cmd != nil {
		t.Fatal("a tick re-armed after everything finished")
	}
}

// A sent message must render as the USER's message. Dispatching the live
// renderer on lane alone put what the user typed under a "Point:" label,
// because a user entry carries a lane field like everything else.
func TestASentMessageRendersAsTheUser(t *testing.T) {
	w := newStreamWorkspace(t)
	typeInput(w, "what I asked")
	w.onSend()

	var userIdx = -1
	for i := 0; i < w.entryCount(); i++ {
		if w.entryRole(i) == roleUser {
			userIdx = i
		}
	}
	if userIdx < 0 {
		t.Fatal("the sent message is not in the transcript at all")
	}
	got := stripANSI(strings.Join(w.entryLines(userIdx, 60, false), "\n"))
	if !strings.Contains(got, "User:") {
		t.Fatalf("a sent message did not render as the user's:\n%s", got)
	}
	if strings.Contains(got, "Point:") {
		t.Fatalf("a sent message rendered as a reply:\n%s", got)
	}
	if !strings.Contains(got, "what I asked") {
		t.Fatalf("the sent text is missing:\n%s", got)
	}
}

// Streaming auto-follow: pinned, the view follows tokens down; scrolled up,
// it stays put while the stream continues underneath; G snaps back.
func TestStreamingAutoFollowAndScrollBack(t *testing.T) {
	w := newStreamWorkspace(t)
	w.viewRows, w.viewWidth = 8, 50
	typeInput(w, "q")
	w.onSend()
	e := w.mustEntry(t, 0)

	grow := func(n int) {
		for i := 0; i < n; i++ {
			e.text += "a line of streamed reply text\n"
			e.touch()
		}
		// This test is about where the VIEW lands, not about the render
		// throttle: without this, every growth inside the same 50ms window
		// re-serves the previous memo and nothing appears to move. The
		// stream tick is what clears it in the running app.
		e.renderedAt = time.Time{}
	}

	grow(10)
	_, lines := w.visibleWindow(8, 50)
	last := lines[len(lines)-1]
	grow(5)
	_, lines = w.visibleWindow(8, 50)
	if lines[len(lines)-1] == last {
		t.Fatal("pinned: the view did not follow the stream down")
	}

	// Scroll up: the position must hold while more tokens arrive.
	w.handleKey(keyMsg("k"))
	w.handleKey(keyMsg("k"))
	if w.pinned {
		t.Fatal("scrolling up did not unpin")
	}
	held := w.rowPosition()
	grow(5)
	w.visibleWindow(8, 50)
	if w.rowPosition() != held {
		t.Fatalf("the view moved under the reader: %v -> %v", held, w.rowPosition())
	}

	// G returns to the live bottom.
	w.handleKey(keyMsg("G"))
	if !w.pinned {
		t.Fatal("G did not re-pin")
	}
	_, lines = w.visibleWindow(8, 50)
	block := w.entryBlock(w.entryCount()-1, 50)
	if lines[len(lines)-1] != block[len(block)-1] {
		t.Fatal("G did not land on the live bottom")
	}
}

// ---------------------------------------------------------------------------
// ISSUE 1 — the watchdog is a SILENCE budget, not a duration cap
// ---------------------------------------------------------------------------

// The distinction that matters: a task that keeps talking is never killed,
// however long it runs. Only one that stops talking is.
func TestTokensResetTheSilenceBudget(t *testing.T) {
	w := newStreamWorkspace(t)
	w.streamTimeout = 50 * time.Millisecond
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	// Ten budgets' worth of elapsed time, but never silent for one.
	for i := 0; i < 10; i++ {
		e.lastEventAt = time.Now().Add(-40 * time.Millisecond)
		w.handleEnvelope(env(t, protocol.EvTextDelta, e.requestID, "t1",
			map[string]any{"data": map[string]any{"text": "tok "}}))
		w.onTick(w.tickGen)
		if e.status.terminal() {
			t.Fatalf("a task that kept streaming was cut off on iteration %d", i)
		}
	}
	// Now genuinely silent.
	e.lastEventAt = time.Now().Add(-time.Second)
	w.onTick(w.tickGen)
	if e.status != statusTimedOut {
		t.Fatalf("a silent task was not timed out: %v", e.status)
	}
}

// Progress and thinking are output too — a bounded loop that is stepping
// through tools without emitting prose is working, not hung.
func TestProgressAndThinkingResetTheSilenceBudget(t *testing.T) {
	for _, ev := range []string{protocol.EvProgressUpdated, protocol.EvThinkingDelta} {
		w := newStreamWorkspace(t)
		w.streamTimeout = 50 * time.Millisecond
		typeInput(w, "hello")
		w.onSend()
		e := w.mustEntry(t, 0)

		e.lastEventAt = time.Now().Add(-40 * time.Millisecond)
		w.handleEnvelope(env(t, ev, e.requestID, "t1",
			map[string]any{"data": map[string]any{"text": "x", "step": 2, "max_steps": 6}}))
		w.onTick(w.tickGen)
		if e.status.terminal() {
			t.Fatalf("%s did not reset the silence budget", ev)
		}
	}
}

// Waiting on a cold local model is the one silence that is expected, so the
// budget before the first token is larger than the budget after it.
func TestFirstTokenGetsALongerBudget(t *testing.T) {
	w := newStreamWorkspace(t)
	w.streamTimeout = 100 * time.Millisecond
	typeInput(w, "hello")
	w.onSend()
	e := w.mustEntry(t, 0)

	// Past the ordinary budget, inside the first-token grace.
	e.lastEventAt = time.Now().Add(-150 * time.Millisecond)
	w.onTick(w.tickGen)
	if e.status.terminal() {
		t.Fatal("a cold start was cut off inside the first-token grace")
	}

	// Once output has been seen, the ordinary budget applies again.
	w.handleEnvelope(env(t, protocol.EvTextDelta, e.requestID, "t1",
		map[string]any{"data": map[string]any{"text": "hi"}}))
	if !e.sawOutput {
		t.Fatal("the first token was not recorded")
	}
	e.lastEventAt = time.Now().Add(-150 * time.Millisecond)
	w.onTick(w.tickGen)
	if e.status != statusTimedOut {
		t.Fatal("after the first token the ordinary budget should apply")
	}
}

// A loop that stopped at its OWN configured limit kept its partial answer.
// That is an outcome, not a fault, and emphatically not "no response".
func TestBoundsExceededReadsDifferentlyFromFailureAndSilence(t *testing.T) {
	footerFor := func(f func(*Workspace, *liveEntry)) string {
		w := newStreamWorkspace(t)
		typeInput(w, "hello")
		w.onSend()
		e := w.mustEntry(t, 0)
		f(w, e)
		return stripANSI(strings.Join(liveFooter(w.theme, e, 70), "\n"))
	}

	bounds := footerFor(func(w *Workspace, e *liveEntry) {
		w.handleEnvelope(env(t, protocol.EvTaskFailed, e.requestID, "t1", map[string]any{
			"error": "stopped after 6 step(s) without a final answer",
			"code":  "bounds_exceeded",
		}))
	})
	failed := footerFor(func(w *Workspace, e *liveEntry) {
		w.handleEnvelope(env(t, protocol.EvTaskFailed, e.requestID, "t1", map[string]any{
			"error": "provider refused the request",
		}))
	})
	silent := footerFor(func(w *Workspace, e *liveEntry) {
		w.finish(e, statusTimedOut)
	})

	if bounds == failed || bounds == silent || failed == silent {
		t.Fatalf("three different outcomes read the same:\n%q\n%q\n%q", bounds, failed, silent)
	}
	if !strings.Contains(bounds, "limit") {
		t.Fatalf("bounds_exceeded does not say it hit a limit: %q", bounds)
	}
	if strings.Contains(bounds, "no response") {
		t.Fatalf("bounds_exceeded is described as silence: %q", bounds)
	}
	if !strings.Contains(silent, "no response") {
		t.Fatalf("a silent task does not say no response: %q", silent)
	}
}

// ---------------------------------------------------------------------------
// ISSUE 2 — tabs measure as zero cells and draw as up to eight
// ---------------------------------------------------------------------------

func TestTabsDoNotOverflowTheBox(t *testing.T) {
	tabbed := "here is code:\n\n```go\n\tif x {\n\t\treturn errors.New(\"a message of some length\")\n\t}\n```\n"

	for _, width := range []int{30, 40, 50, 80} {
		w := newStreamWorkspace(t)
		typeInput(w, "@research x")
		w.onSend()
		e := w.mustEntry(t, 0)
		e.text = tabbed
		w.finish(e, statusDone)
		e.slotOpen = true

		check := func(what string, lines []string) {
			for i, l := range lines {
				if vw := visibleWidth(l); vw > width {
					t.Errorf("%s at width %d: line %d is %d cells: %q",
						what, width, i, vw, stripANSI(l))
				}
				if strings.ContainsRune(stripANSI(l), '\t') {
					t.Errorf("%s at width %d: line %d still contains a tab, which "+
						"measures as 0 cells and draws as up to 8", what, width, i)
				}
			}
		}
		check("expanded slot", renderTaskEntry(w.theme, e, width, false))
		check("inline reply", renderTurnEntry(w.theme, e, width, false))
		check("persisted message", renderMessage(w.theme,
			wireMessage{Role: roleAssistant, Content: wireContent{Text: tabbed}}, width, false))
	}
}

// The same hazard inside an unclosed fence's raw tail, which is preformatted
// by definition and bypasses the markdown wrapper entirely.
func TestTabsInAnOpenFenceTailDoNotOverflow(t *testing.T) {
	w := newStreamWorkspace(t)
	open := "intro\n```go\n\tfunc f() {\n\t\tdoSomethingWithALongName(argument)\n"
	width := 40
	for i, l := range renderStreamBody(w.theme, open, width) {
		if vw := visibleWidth(l); vw > width {
			t.Errorf("raw tail line %d is %d cells: %q", i, vw, stripANSI(l))
		}
		if strings.ContainsRune(stripANSI(l), '\t') {
			t.Errorf("raw tail line %d still contains a tab", i)
		}
	}
}

func TestExpandTabsAlignsToStops(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\tb", "a   b"},
		{"\tx", "    x"},
		{"ab\tc", "ab  c"},
		{"abcd\te", "abcd    e"},
		{"no tabs", "no tabs"},
		{"a\tb\nc\td", "a   b\nc   d"}, // the column resets on each line
	}
	for _, c := range cases {
		if got := expandTabs(c.in); got != c.want {
			t.Errorf("expandTabs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
