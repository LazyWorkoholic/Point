package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// A backend with no process behind it. This is what the Backend seam is for.
// ---------------------------------------------------------------------------

type fakeBackend struct {
	mu     sync.Mutex
	in     []Envelope
	out    chan Envelope
	exited chan struct{}

	exitErr    error
	stderr     string
	closed     bool
	sendErr    error
	closeCalls int
}

func newFake() *fakeBackend {
	return &fakeBackend{
		out:    make(chan Envelope, 64),
		exited: make(chan struct{}),
	}
}

func (f *fakeBackend) Send(e Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.in = append(f.in, e)
	return nil
}

func (f *fakeBackend) Recv() <-chan Envelope { return f.out }

func (f *fakeBackend) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	if !f.closed {
		f.closed = true
		close(f.out)
		close(f.exited)
	}
	return nil
}

func (f *fakeBackend) Exited() <-chan struct{} { return f.exited }
func (f *fakeBackend) ExitError() error        { return f.exitErr }
func (f *fakeBackend) StderrTail() string      { return f.stderr }

// die simulates the child process going away: stdout ends, then the process
// is reaped. It is deliberately NOT Close(), which is our side hanging up.
func (f *fakeBackend) die(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	f.exitErr = err
	close(f.out)
	close(f.exited)
}

func (f *fakeBackend) sent() []Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Envelope, len(f.in))
	copy(out, f.in)
	return out
}

func helloEnvelope(version int) Envelope {
	payload, _ := json.Marshal(Hello{
		V: version,
		Capabilities: Capabilities{
			Providers: []string{"ollama", "anthropic"}, SchemaVersion: 7,
			Streaming: true, Cancel: true,
		},
	})
	return Envelope{V: Version, ID: "hello-1", Type: EvHello, Payload: payload}
}

func taskEnvelope(ty, taskID string) Envelope {
	return Envelope{V: Version, ID: newID(), Type: ty, TaskID: taskID,
		Payload: json.RawMessage(`{}`)}
}

// fastOptions compresses every timer so the ladder runs in milliseconds.
func fastOptions(dial Dialer) Options {
	return Options{
		Dial:             dial,
		HandshakeTimeout: 500 * time.Millisecond,
		PingInterval:     20 * time.Millisecond,
		MissedPings:      2,
		RequestTimeout:   200 * time.Millisecond,
		RestartBackoff:   time.Millisecond,
	}
}

// collect drains the client's messages into a slice until stop is closed.
func collect(t *testing.T, c *Client) (get func() []tea.Msg, stop func()) {
	t.Helper()
	var mu sync.Mutex
	var msgs []tea.Msg
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			m, ok := <-c.msgs
			if !ok {
				return
			}
			mu.Lock()
			msgs = append(msgs, m)
			mu.Unlock()
		}
	}()

	return func() []tea.Msg {
			mu.Lock()
			defer mu.Unlock()
			out := make([]tea.Msg, len(msgs))
			copy(out, msgs)
			return out
		}, func() {
			<-done
		}
}

func waitFor(t *testing.T, get func() []tea.Msg, match func(tea.Msg) bool, what string) tea.Msg {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range get() {
			if match(m) {
				return m
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; saw %v", what, describe(get()))
	return nil
}

// waitUntil polls a condition instead of matching a message, for assertions
// about state that a message may reach before or after the state itself.
func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func describe(msgs []tea.Msg) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = fmt.Sprintf("%T", m)
	}
	return out
}

func is[T tea.Msg](m tea.Msg) bool { _, ok := m.(T); return ok }

// ---------------------------------------------------------------------------
// Handshake
// ---------------------------------------------------------------------------

func TestHandshakePopulatesCapabilities(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	f.out <- helloEnvelope(Version)

	msg := waitFor(t, get, is[ConnectedMsg], "ConnectedMsg")
	conn := msg.(ConnectedMsg)
	if conn.ProtocolVersion != Version {
		t.Errorf("version = %d", conn.ProtocolVersion)
	}
	if len(conn.Capabilities.Providers) != 2 || !conn.Capabilities.Streaming {
		t.Errorf("capabilities = %+v", conn.Capabilities)
	}
	if conn.Restarted {
		t.Error("first connection reported as a restart")
	}
	if c.State() != StateConnected {
		t.Errorf("state = %q", c.State())
	}
	if got := c.Capabilities(); got.SchemaVersion != 7 {
		t.Errorf("stored capabilities = %+v", got)
	}
}

// A version mismatch refuses to proceed, naming BOTH versions.
func TestVersionMismatchRefusesAndNamesBoth(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	f.out <- helloEnvelope(Version + 1)

	mm := waitFor(t, get, is[VersionMismatchMsg], "VersionMismatchMsg").(VersionMismatchMsg)
	if mm.Client != Version || mm.Backend != Version+1 {
		t.Fatalf("mismatch = %+v", mm)
	}

	banner := waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg").(BackendFailedMsg)
	for _, want := range []string{fmt.Sprintf("v%d", Version), fmt.Sprintf("v%d", Version+1)} {
		if !strings.Contains(banner.Detail, want) {
			t.Errorf("detail does not name %s: %q", want, banner.Detail)
		}
	}
	if c.State() != StateFailed {
		t.Errorf("state = %q, want failed", c.State())
	}
	// A mismatch is not something a restart fixes.
	if c.Restarts() != 0 {
		t.Errorf("restarted %d times after a version mismatch", c.Restarts())
	}
}

// A backend that exits before the handshake is explained with its own stderr —
// the single-instance lock refusal is exactly this case.
func TestExitBeforeHandshakeReportsStderr(t *testing.T) {
	f := newFake()
	f.stderr = "Point backend is already running (pid 4242)"
	f.exitErr = errors.New("exit status 1")

	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	f.die(errors.New("exit status 1"))

	banner := waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg").(BackendFailedMsg)
	if !strings.Contains(banner.Detail, "already running") {
		t.Fatalf("detail lost the backend's own explanation: %q", banner.Detail)
	}
	if c.Restarts() != 0 {
		t.Errorf("auto-restarted a backend that refused to start: %d", c.Restarts())
	}
}

// Events that arrive before hello are forwarded, not discarded.
func TestPreHandshakeEventsAreNotLost(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	f.out <- Envelope{V: Version, ID: "e0", Type: EvError, Payload: json.RawMessage(`{}`)}
	f.out <- helloEnvelope(Version)

	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvError
	}, "the pre-hello Error event")
}

func TestHandshakeTimeout(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	// Say nothing at all.
	banner := waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg").(BackendFailedMsg)
	if !strings.Contains(banner.Reason, "handshake") {
		t.Errorf("reason = %q", banner.Reason)
	}
}

func TestDialFailureIsReportedNotRetriedForever(t *testing.T) {
	attempts := 0
	c := NewClient(fastOptions(func() (Backend, error) {
		attempts++
		return nil, errors.New("uv: executable file not found in %PATH%")
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	banner := waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg").(BackendFailedMsg)
	if !strings.Contains(banner.Detail, "uv") {
		t.Errorf("detail = %q", banner.Detail)
	}
	time.Sleep(100 * time.Millisecond)
	if attempts != 1 {
		t.Fatalf("dialled %d times without a manual retry", attempts)
	}
}

// ---------------------------------------------------------------------------
// Recovery ladder
// ---------------------------------------------------------------------------

func TestCrashProducesOneRestartAndInterruptsTheTask(t *testing.T) {
	var mu sync.Mutex
	backends := []*fakeBackend{newFake(), newFake()}
	n := 0

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := backends[n]
		n++
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	backends[0].out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "first ConnectedMsg")

	backends[0].out <- taskEnvelope(EvTaskStarted, "task-9")
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvTaskStarted
	}, "TaskStarted")

	// Kill it mid-stream.
	backends[0].die(errors.New("signal: killed"))

	lost := waitFor(t, get, is[BackendLostMsg], "BackendLostMsg").(BackendLostMsg)
	// UNKNOWN, not FAILED — at this instant nobody knows.
	if len(lost.UnknownTasks) != 1 || lost.UnknownTasks[0] != "task-9" {
		t.Fatalf("unknown tasks = %v", lost.UnknownTasks)
	}

	backends[1].out <- helloEnvelope(Version)
	conn := waitFor(t, get, func(m tea.Msg) bool {
		cm, ok := m.(ConnectedMsg)
		return ok && cm.Restarted
	}, "ConnectedMsg{Restarted: true}").(ConnectedMsg)
	if !conn.Restarted {
		t.Fatal("reconnect not flagged as a restart")
	}

	interrupted := waitFor(t, get, is[TasksInterruptedMsg], "TasksInterruptedMsg").(TasksInterruptedMsg)
	if len(interrupted.TaskIDs) != 1 || interrupted.TaskIDs[0] != "task-9" {
		t.Fatalf("interrupted = %v", interrupted.TaskIDs)
	}
	if c.Restarts() != 1 {
		t.Errorf("restarts = %d, want 1", c.Restarts())
	}
	if len(c.InFlight()) != 0 {
		t.Errorf("task still in flight after being interrupted: %v", c.InFlight())
	}
}

// Two failures inside 60s: a banner and a manual retry, and NO restart loop.
func TestTwoFailuresInsideTheWindowStopAutoRestarting(t *testing.T) {
	var mu sync.Mutex
	var made []*fakeBackend

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := newFake()
		made = append(made, b)
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	nth := func(i int) *fakeBackend {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			if len(made) > i {
				b := made[i]
				mu.Unlock()
				return b
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("backend %d was never dialled", i)
		return nil
	}

	nth(0).out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "first connect")
	nth(0).die(errors.New("boom"))

	nth(1).out <- helloEnvelope(Version)
	waitFor(t, get, func(m tea.Msg) bool {
		cm, ok := m.(ConnectedMsg)
		return ok && cm.Restarted
	}, "reconnect")
	nth(1).die(errors.New("boom again"))

	banner := waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg").(BackendFailedMsg)
	if !strings.Contains(banner.Reason, "twice") {
		t.Errorf("reason = %q", banner.Reason)
	}
	if c.State() != StateFailed {
		t.Errorf("state = %q", c.State())
	}

	// The loop test: nothing more is dialled until a human asks.
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	dialled := len(made)
	mu.Unlock()
	if dialled != 2 {
		t.Fatalf("dialled %d backends after giving up; expected 2 and then a wait", dialled)
	}

	// Manual retry starts exactly one more attempt.
	c.Retry()
	nth(2).out <- helloEnvelope(Version)
	waitFor(t, get, func(m tea.Msg) bool {
		_, ok := m.(ConnectedMsg)
		return ok
	}, "connect after manual retry")

	// A manual retry is a real restart. The visible counter has to say so:
	// one automatic restart plus one manual retry, both after the first
	// connection.
	waitUntil(t, func() bool { return c.Restarts() == 2 },
		"Restarts to reach 2 after an automatic restart and a manual retry")
}

// The manual retry moves the visible counter.
//
// It used to be incremented only by the automatic path, so pressing retry
// started a real child process and the number on screen never moved. The
// counter is now derived from how many times the client has connected, which
// cannot disagree with what actually happened.
func TestManualRetryMovesTheVisibleCounter(t *testing.T) {
	var mu sync.Mutex
	var made []*fakeBackend

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := newFake()
		made = append(made, b)
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	nth := func(i int) *fakeBackend {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			if len(made) > i {
				b := made[i]
				mu.Unlock()
				return b
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("backend %d was never dialled", i)
		return nil
	}

	// First connection is not a restart.
	nth(0).out <- helloEnvelope(Version)
	waitUntil(t, func() bool { return c.State() == StateConnected }, "the first connection")
	if got := c.Restarts(); got != 0 {
		t.Fatalf("Restarts = %d on the first connection", got)
	}

	// Crash once: the ladder restarts on its own. That path always worked.
	nth(0).die(errors.New("boom"))
	nth(1).out <- helloEnvelope(Version)
	waitUntil(t, func() bool { return c.Restarts() == 1 }, "the automatic restart to be counted")

	// Crash again inside the window: banner, and no more automatic restarts.
	nth(1).die(errors.New("boom again"))
	waitFor(t, get, is[BackendFailedMsg], "BackendFailedMsg")
	if got := c.Restarts(); got != 1 {
		t.Fatalf("Restarts = %d while the banner is up, want 1", got)
	}

	// The part that used to be invisible.
	c.Retry()
	nth(2).out <- helloEnvelope(Version)
	waitUntil(t, func() bool { return c.Restarts() == 2 },
		"the manual retry to be counted as a restart")
}

// The visible counter and the automatic-retry budget are different numbers.
// Manual retries must not refill the "two failures in 60s" window's meaning,
// and the window must not be what the user sees.
func TestRestartCounterIsNotTheFailureBudget(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))

	if got := c.Restarts(); got != 0 {
		t.Fatalf("a client that never connected reports %d restarts", got)
	}

	c.mu.Lock()
	c.failures = []time.Time{time.Now(), time.Now()}
	c.mu.Unlock()

	if got := c.Restarts(); got != 0 {
		t.Errorf("failures leaked into the visible restart count: %d", got)
	}
}

// A hung backend — process alive, nothing arriving — is caught by the
// watchdog, which is the failure mode process-exit detection misses entirely.
func TestSilenceIsDetectedEvenWhileTheProcessLives(t *testing.T) {
	var mu sync.Mutex
	var made []*fakeBackend

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := newFake()
		made = append(made, b)
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(made)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never dialled")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	first := made[0]
	mu.Unlock()

	first.out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "connect")

	// Now go quiet. The process never exits.
	lost := waitFor(t, get, is[BackendLostMsg], "BackendLostMsg").(BackendLostMsg)
	if !strings.Contains(lost.Reason, "no word") {
		t.Errorf("reason = %q, want the silence path", lost.Reason)
	}
}

// ---------------------------------------------------------------------------
// Routing and correlation
// ---------------------------------------------------------------------------

// connected is for the routing and correlation tests, which care about
// message flow and not about the ladder. Its ping interval is deliberately
// long: with fastOptions' 20ms watchdog, a test that deliberately waits for a
// request timeout would be declared a hung backend halfway through.
func connected(t *testing.T) (*Client, *fakeBackend, func() []tea.Msg, func()) {
	t.Helper()
	f := newFake()
	opts := fastOptions(func() (Backend, error) { return f, nil })
	opts.PingInterval = 30 * time.Second
	c := NewClient(opts)
	get, stop := collect(t, c)
	c.Start()

	f.out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "ConnectedMsg")

	return c, f, get, func() { _ = c.Close(); stop() }
}

func TestRoutingOrder(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()

	replies, err := c.Request(Envelope{ID: "req-7", Type: CmdListSessions}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		env  Envelope
		want RouteKind
	}{
		{Envelope{V: Version, ID: "a", Type: EvSessionList, CorrID: "req-7", Payload: json.RawMessage(`{}`)}, RouteCorrelation},
		{Envelope{V: Version, ID: "b", Type: EvTaskToken, TaskID: "t1", Payload: json.RawMessage(`{}`)}, RouteTask},
		{Envelope{V: Version, ID: "c", Type: EvSessionRenamed, SessionID: "s1", Payload: json.RawMessage(`{}`)}, RouteSession},
		{Envelope{V: Version, ID: "d", Type: EvPing, Payload: json.RawMessage(`{}`)}, RouteConnection},
		{Envelope{V: Version, ID: "e", Type: EvUsageUpdated, Payload: json.RawMessage(`{}`)}, RouteUnroutable},
	}
	for _, tc := range cases {
		f.out <- tc.env
	}

	for _, tc := range cases {
		id := tc.env.ID
		want := tc.want
		msg := waitFor(t, get, func(m tea.Msg) bool {
			e, ok := m.(EnvelopeMsg)
			return ok && e.Envelope.ID == id
		}, "envelope "+id).(EnvelopeMsg)
		if msg.Route != want {
			t.Errorf("envelope %s routed as %q, want %q", id, msg.Route, want)
		}
	}

	// Routing does not consume: the correlated reply also reached its waiter.
	select {
	case reply := <-replies:
		if reply.ID != "a" {
			t.Errorf("waiter got %q", reply.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("correlated reply never reached its waiter")
	}
}

// An unroutable event is delivered, not swallowed — it means a contract bug,
// and the point of logging it is to see it.
func TestUnroutableEventsStillReachTheModel(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()
	_ = c

	f.out <- Envelope{V: Version, ID: "orphan", Type: "SomethingNobodyAskedFor",
		Payload: json.RawMessage(`{}`)}

	msg := waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.ID == "orphan"
	}, "the orphan event").(EnvelopeMsg)
	if msg.Route != RouteUnroutable {
		t.Errorf("route = %q", msg.Route)
	}
}

// The timeout covers the ack only. Once anything arrives, the stream may take
// as long as it takes.
func TestRequestTimeoutCoversTheAckOnly(t *testing.T) {
	c, f, _, done := connected(t)
	defer done()

	// No ack: the channel closes.
	silent, err := c.Request(Envelope{Type: CmdListSessions}, 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-silent:
		if ok {
			t.Fatal("something arrived on a request nobody answered")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout never fired")
	}

	// Acked, then quiet for longer than the timeout: still open.
	streamed, err := c.Request(Envelope{ID: "req-stream", Type: CmdSendMessage}, 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	f.out <- Envelope{V: Version, ID: "ack", Type: EvTaskStarted, CorrID: "req-stream",
		TaskID: "t9", Payload: json.RawMessage(`{}`)}
	<-streamed

	time.Sleep(120 * time.Millisecond)
	f.out <- Envelope{V: Version, ID: "tok", Type: EvTaskToken, CorrID: "req-stream",
		TaskID: "t9", Payload: json.RawMessage(`{}`)}

	select {
	case env, ok := <-streamed:
		if !ok {
			t.Fatal("the stream channel closed after the ack had already arrived")
		}
		if env.ID != "tok" {
			t.Errorf("got %q", env.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("the streamed event never arrived")
	}

	c.Release("req-stream")
	if _, ok := <-streamed; ok {
		t.Error("Release did not close the channel")
	}
}

func TestRequestSendsAnIDEvenWhenNoneWasGiven(t *testing.T) {
	c, f, _, done := connected(t)
	defer done()

	if _, err := c.Request(Envelope{Type: CmdListSessions}, time.Second); err != nil {
		t.Fatal(err)
	}
	sent := f.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d envelopes", len(sent))
	}
	if sent[0].ID == "" {
		t.Fatal("the backend takes request_id from `id`; it was empty")
	}
}

func TestSendFailsWhenDisconnected(t *testing.T) {
	c := NewClient(fastOptions(func() (Backend, error) { return newFake(), nil }))
	if err := c.Send(Envelope{Type: CmdListSessions}); err == nil {
		t.Fatal("sent to a backend that was never connected")
	}
	if _, err := c.Request(Envelope{Type: CmdListSessions}, time.Second); err == nil {
		t.Fatal("requested from a backend that was never connected")
	}
}

// Losing the connection closes every waiting channel; no caller is left
// hanging on a reply that can no longer come.
func TestConnectionLossClosesPendingWaiters(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	f.out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "connect")

	replies, err := c.Request(Envelope{ID: "req-x", Type: CmdListSessions}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.die(errors.New("gone"))

	select {
	case _, ok := <-replies:
		if ok {
			t.Fatal("a reply arrived from a dead backend")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was left hanging after the connection was lost")
	}
}

// A task that starts and finishes leaves nothing behind; a crash then has
// nothing to interrupt.
func TestTerminalEventClearsTheInFlightSet(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()

	f.out <- taskEnvelope(EvTaskStarted, "t1")
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvTaskStarted
	}, "TaskStarted")
	if len(c.InFlight()) != 1 {
		t.Fatalf("in flight = %v", c.InFlight())
	}

	// The cancel ACK is not terminal.
	f.out <- taskEnvelope(EvTaskCancelAccepted, "t1")
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvTaskCancelAccepted
	}, "cancel ack")
	if len(c.InFlight()) != 1 {
		t.Fatalf("the cancel ack ended the task: %v", c.InFlight())
	}

	f.out <- taskEnvelope(EvTaskCancelled, "t1")
	deadline := time.Now().Add(2 * time.Second)
	for len(c.InFlight()) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(c.InFlight()) != 0 {
		t.Fatalf("in flight after the terminal event: %v", c.InFlight())
	}
}

// A protocol violation reaches the model as a named error, not as silence.
func TestProtocolErrorReachesTheModel(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()
	_ = c

	f.out <- protocolErrorEnvelope(12, `hi`, errors.New("invalid character 'h'"))

	msg := waitFor(t, get, is[ProtocolErrorMsg], "ProtocolErrorMsg").(ProtocolErrorMsg)
	if msg.Line != 12 || msg.Text != "hi" {
		t.Fatalf("got %+v", msg)
	}
	if msg.Message == "" {
		t.Error("no explanation of what was wrong with the line")
	}
}

func TestCloseIsIdempotentAndStopsTheClient(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	get, stop := collect(t, c)
	c.Start()

	f.out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "connect")

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	stop()

	if got := c.Restarts(); got != 0 {
		t.Errorf("a clean close counted as a failure: %d restarts", got)
	}
}

// Next() is the Bubble Tea seam: one message per call, and a closed client
// yields ClosedMsg rather than blocking forever.
func TestNextYieldsMessagesThenClosed(t *testing.T) {
	f := newFake()
	c := NewClient(fastOptions(func() (Backend, error) { return f, nil }))
	c.Start()

	f.out <- helloEnvelope(Version)

	saw := false
	for i := 0; i < 10; i++ {
		if _, ok := c.Next()().(ConnectedMsg); ok {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatal("Next never yielded the handshake")
	}

	_ = c.Close()
	if _, ok := c.Next()().(ClosedMsg); !ok {
		t.Fatal("Next did not report the client as closed")
	}
}

// ---------------------------------------------------------------------------
// B15: the replay rung, the high-water mark, and the deleted busyGrace
// ---------------------------------------------------------------------------

// A replayed message must never look like a new one. It carries a session_id
// and a corr_id, so without its own route it would arrive as RouteSession or
// RouteCorrelation and a consumer that appended it would duplicate history.
func TestSessionReplayGetsItsOwnRoute(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()

	replies, err := c.Request(Envelope{ID: "req-r", Type: CmdSessionReplaySince}, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	f.out <- Envelope{
		V: Version, ID: "rep", Type: EvSessionReplay, CorrID: "req-r",
		SessionID: "s1",
		Payload:   json.RawMessage(`{"session":"s1","since_seq":0,"replay":true,"messages":[],"tasks":[]}`),
	}

	msg := waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.ID == "rep"
	}, "replay envelope").(EnvelopeMsg)
	if msg.Route != RouteReplay {
		t.Errorf("replay routed as %q, want %q", msg.Route, RouteReplay)
	}

	// A caller that asked explicitly still gets its reply: the replay
	// route is additional information, not a consumed delivery.
	select {
	case reply := <-replies:
		if reply.ID != "rep" {
			t.Errorf("waiter got %q", reply.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("replay reply never reached its waiter")
	}
}

// The mark is monotonic and per session, and a session becomes known from its
// session_id alone — no payload is read to learn one exists.
func TestReplayMarksAreMonotonicAndLearnedFromRouting(t *testing.T) {
	c, f, get, done := connected(t)
	defer done()

	f.out <- Envelope{
		V: Version, ID: "m1", Type: EvSessionRenamed, SessionID: "s1",
		Payload: json.RawMessage(`{}`),
	}
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.ID == "m1"
	}, "session envelope")

	if marks := c.ReplayMarks(); marks["s1"] != 0 {
		t.Fatalf("a session seen but never rendered should mark 0, got %v", marks)
	}

	c.MarkSeen("s1", 12)
	c.MarkSeen("s1", 7) // late, out of order: must not walk backwards
	c.MarkSeen("s1", 0) // meaningless; ignored
	c.MarkSeen("", 99)  // no session; ignored

	marks := c.ReplayMarks()
	if marks["s1"] != 12 {
		t.Errorf("mark = %d, want 12 (monotonic)", marks["s1"])
	}
	if _, present := marks[""]; present {
		t.Error("an empty session id was recorded")
	}

	// The copy is a copy.
	marks["s1"] = 999
	if c.ReplayMarks()["s1"] != 12 {
		t.Error("ReplayMarks handed out its own map")
	}
}

// The seam is filled: a reconnect asks for replay from the mark, for every
// session it has seen, and still interrupts what did not survive.
func TestReconnectRequestsReplayFromTheMark(t *testing.T) {
	var mu sync.Mutex
	backends := []*fakeBackend{newFake(), newFake()}
	n := 0

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := backends[n]
		n++
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	backends[0].out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "first ConnectedMsg")

	backends[0].out <- Envelope{
		V: Version, ID: "s", Type: EvSessionRenamed, SessionID: "sess-1",
		Payload: json.RawMessage(`{}`),
	}
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.ID == "s"
	}, "session traffic")
	c.MarkSeen("sess-1", 41)

	backends[0].out <- taskEnvelope(EvTaskStarted, "task-1")
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvTaskStarted
	}, "TaskStarted")

	backends[0].die(errors.New("signal: killed"))
	waitFor(t, get, is[BackendLostMsg], "BackendLostMsg")

	backends[1].out <- helloEnvelope(Version)
	waitFor(t, get, func(m tea.Msg) bool {
		cm, ok := m.(ConnectedMsg)
		return ok && cm.Restarted
	}, "reconnect")

	// The last rung still runs: a reconnect is a new process, so the old
	// process's tasks are gone whatever replay reports.
	waitFor(t, get, is[TasksInterruptedMsg], "TasksInterruptedMsg")

	var replay *Envelope
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range backends[1].sent() {
			if env.Type == CmdSessionReplaySince {
				e := env
				replay = &e
				break
			}
		}
		if replay != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if replay == nil {
		t.Fatal("reconnect sent no SessionReplaySince -- the seam is still a no-op")
	}
	if replay.SessionID != "sess-1" {
		t.Errorf("replay session = %q, want sess-1", replay.SessionID)
	}
	var p struct {
		Session  string `json:"session"`
		SinceSeq int    `json:"since_seq"`
		Limit    int    `json:"limit"`
	}
	if err := replay.UnmarshalPayload(&p); err != nil {
		t.Fatalf("replay payload: %v", err)
	}
	if p.Session != "sess-1" || p.SinceSeq != 41 {
		t.Errorf("replay payload = %+v, want session sess-1 since_seq 41", p)
	}
	if p.Limit != replayLimit {
		t.Errorf("replay limit = %d, want %d", p.Limit, replayLimit)
	}
}

// busyGrace is gone (B15/D98): one watchdog budget, whatever the backend is
// doing. A task in flight no longer buys 60s of forgiven silence, because the
// backend's ping is now unconditional.
func TestSilenceIsDetectedWithATaskInFlight(t *testing.T) {
	var mu sync.Mutex
	var made []*fakeBackend

	c := NewClient(fastOptions(func() (Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		b := newFake()
		made = append(made, b)
		return b, nil
	}))
	get, stop := collect(t, c)
	c.Start()
	defer func() { _ = c.Close(); stop() }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(made)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never dialled")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	first := made[0]
	mu.Unlock()

	first.out <- helloEnvelope(Version)
	waitFor(t, get, is[ConnectedMsg], "connect")

	// A task starts, then the backend goes silent and the process lives:
	// the exact shape busyGrace used to forgive for a full minute.
	first.out <- taskEnvelope(EvTaskStarted, "task-busy")
	waitFor(t, get, func(m tea.Msg) bool {
		e, ok := m.(EnvelopeMsg)
		return ok && e.Envelope.Type == EvTaskStarted
	}, "TaskStarted")

	start := time.Now()
	lost := waitFor(t, get, is[BackendLostMsg], "BackendLostMsg").(BackendLostMsg)
	if !strings.Contains(lost.Reason, "no word") {
		t.Errorf("reason = %q, want the silence path", lost.Reason)
	}
	// fastOptions' watchdog is milliseconds. With busyGrace this test
	// would have waited a minute and the harness would have given up.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s to notice silence during a task", elapsed)
	}
	if len(lost.UnknownTasks) != 1 || lost.UnknownTasks[0] != "task-busy" {
		t.Errorf("unknown tasks = %v, want [task-busy]", lost.UnknownTasks)
	}
}
