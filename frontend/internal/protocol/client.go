package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ConnState is what the status bar shows.
type ConnState string

const (
	// StateStarting covers spawn and handshake.
	StateStarting ConnState = "starting"
	// StateConnected means hello arrived and the version matched.
	StateConnected ConnState = "connected"
	// StateLost means the child died or went quiet and one automatic restart
	// is under way. The app stays usable.
	StateLost ConnState = "lost"
	// StateFailed means Point has stopped trying. A status-bar banner with a
	// retry action, never a modal — the rest of the app stays usable.
	StateFailed ConnState = "failed"
)

// Defaults for the connection's timing. Every one of them is a field on
// Options so a test can compress them.
const (
	DefaultHandshakeTimeout = 30 * time.Second
	DefaultPingInterval     = 5 * time.Second
	DefaultMissedPings      = 2
	DefaultRequestTimeout   = 15 * time.Second
	DefaultRestartBackoff   = 500 * time.Millisecond

	// failureWindow is the "two failures inside 60s" rule.
	failureWindow = 60 * time.Second

	// busyGrace is GONE as of B15, and it is worth knowing why rather
	// than wondering where it went.
	//
	// It was a 60s stretch of the watchdog deadline whenever this client
	// believed a task was in flight, because the backend suppressed its
	// ping while `active_count() > 0` — on the reasoning that tokens
	// already prove liveness. That holds for a streaming reply and fails
	// for a slow tool call, which is silent by nature: between
	// ToolStarted and ToolCompleted the backend sent no tokens AND no
	// pings, so a working tool call and a deadlocked process looked
	// identical. Stretching the deadline was the only safe response, and
	// it meant a genuine hang mid-task took ~72s to notice instead of
	// ~12s.
	//
	// B15 made the backend's ping unconditional (decisions.md D98), so
	// the heartbeat is now a real heartbeat and the watchdog measures
	// against it in every state. The budget below is the honest one for
	// both idle and busy.

	// replayLimit caps one CmdSessionReplaySince request. A session with
	// more history than this replays its newest page and reports
	// has_more; the mark advances as the consumer renders, so the next
	// request continues rather than repeating.
	replayLimit = 200
)

// Backend is what the client drives. StdioTransport implements it; tests
// implement it with no process at all, which is the point of the seam.
type Backend interface {
	Transport
	Exited() <-chan struct{}
	ExitError() error
	StderrTail() string
}

// Dialer produces a connection. One call per connection attempt.
type Dialer func() (Backend, error)

// Options configures a Client.
type Options struct {
	Dial             Dialer
	Log              Logger
	HandshakeTimeout time.Duration
	PingInterval     time.Duration
	MissedPings      int
	RequestTimeout   time.Duration
	RestartBackoff   time.Duration
}

// StdioDialer is the real one: find backend\, spawn `uv run python main.py`
// with its working directory set to backend\, and send stderr to sink.
func StdioDialer(backendDir string, sink io.Writer, log Logger) Dialer {
	return func() (Backend, error) {
		return Spawn(backendDir, sink, log)
	}
}

// ---------------------------------------------------------------------------
// Messages to the UI
// ---------------------------------------------------------------------------

// RouteKind records how an envelope was matched. An event that matched
// nothing is still delivered — it is logged as unroutable, because an
// unroutable event means a contract bug you want to see, not one to swallow.
type RouteKind string

const (
	RouteCorrelation RouteKind = "corr_id"
	RouteTask        RouteKind = "task_id"
	RouteSession     RouteKind = "session_id"
	RouteConnection  RouteKind = "connection"
	RouteUnroutable  RouteKind = "unroutable"
	// RouteReplay is B15's replay reply, and it is a separate route for
	// one reason: a replayed message must never be handled by the code
	// that handles a new one. It carries a session_id and would
	// otherwise arrive as RouteSession, indistinguishable from live
	// traffic, and a consumer that appended it would duplicate every
	// message it already had.
	RouteReplay RouteKind = "replay"
)

type (
	// ConnectingMsg is emitted before each attempt. Attempt is 1-based.
	ConnectingMsg struct{ Attempt int }

	// ConnectedMsg carries the handshake. Capabilities is stored on the root
	// so later episodes grey out what the backend cannot do rather than
	// offering it and failing.
	ConnectedMsg struct {
		ProtocolVersion int
		Capabilities    Capabilities
		Restarted       bool
	}

	// VersionMismatchMsg refuses to proceed, with both versions named.
	VersionMismatchMsg struct {
		Client  int
		Backend int
	}

	// BackendLostMsg is the first rung of the recovery ladder. The tasks are
	// UNKNOWN, not failed — at this moment nobody knows.
	BackendLostMsg struct {
		Reason       string
		UnknownTasks []string
	}

	// TasksInterruptedMsg is the last rung: these tasks did not survive.
	// Partial text is kept by the consumer and marked interrupted.
	TasksInterruptedMsg struct{ TaskIDs []string }

	// BackendFailedMsg is the banner: no more automatic restarts, manual
	// retry only.
	BackendFailedMsg struct {
		Reason string
		Detail string
	}

	// EnvelopeMsg is a routed event.
	EnvelopeMsg struct {
		Envelope Envelope
		Route    RouteKind
	}

	// ProtocolErrorMsg is a line on stdout that was not an envelope — a
	// stray print() on the Python side, most likely.
	ProtocolErrorMsg struct {
		Line    int
		Text    string
		Message string
	}

	// ClosedMsg means the client has stopped for good.
	ClosedMsg struct{}
)

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client owns the connection, the recovery ladder, and request correlation.
//
// Nothing in it is shared mutable state with the model: everything reaches
// Bubble Tea through the msgs channel, surfaced as a tea.Cmd.
type Client struct {
	opts Options
	log  Logger

	msgs  chan tea.Msg
	retry chan struct{}
	quit  chan struct{}

	mu       sync.Mutex
	state    ConnState
	caps     Capabilities
	backend  Backend
	pending  map[string]*waiter
	inflight map[string]struct{}

	// lastSeq is the per-session replay high-water mark (B15): the
	// highest `messages.seq` a consumer has told us it has rendered.
	//
	// A session appears here with 0 the first time any envelope carrying
	// its session_id arrives, so a reconnect knows WHICH sessions to
	// replay without this package reading a single payload — session_id
	// is a routing field, which is the only thing this package
	// interprets (see the package comment). The mark itself can only come
	// from whoever decodes message payloads, through MarkSeen; 0 means
	// "replay everything you have", which is correct for a session this
	// client has seen traffic for but never rendered history of.
	lastSeq map[string]int

	// failures is the automatic-retry budget: the "two inside 60s" window.
	// It is deliberately NOT the number a user sees.
	failures []time.Time

	// connects counts successful handshakes. Restarts() is derived from it, so
	// a restart is a restart whether it was automatic or the user pressed
	// retry — the visible counter cannot disagree with what happened.
	connects int

	closeOnce sync.Once
	done      chan struct{}
}

type waiter struct {
	ch      chan Envelope
	timer   *time.Timer
	arrived bool
}

// NewClient builds a client. Start actually connects.
func NewClient(opts Options) *Client {
	if opts.Log == nil {
		opts.Log = discardLogger{}
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.MissedPings <= 0 {
		opts.MissedPings = DefaultMissedPings
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = DefaultRequestTimeout
	}
	if opts.RestartBackoff <= 0 {
		opts.RestartBackoff = DefaultRestartBackoff
	}

	return &Client{
		opts:     opts,
		log:      opts.Log,
		msgs:     make(chan tea.Msg, 512),
		retry:    make(chan struct{}, 1),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
		state:    StateStarting,
		pending:  make(map[string]*waiter),
		inflight: make(map[string]struct{}),
		lastSeq:  make(map[string]int),
	}
}

// MarkSeen records that the consumer has rendered `seq` for this session —
// B15's replay high-water mark.
//
// It is the consumer's to report and not this package's to derive: `seq` here
// is `messages.seq`, which lives in a message payload, and this package reads
// types and correlation fields only (see the package comment). Monotonic, so a
// late or out-of-order call cannot walk the mark backwards and cause a replay
// to re-deliver what has already been shown.
//
// A session with no mark replays from 0 on reconnect, which is the honest
// default: this client knows it has seen traffic for that session and knows
// nothing about what the UI holds.
func (c *Client) MarkSeen(sessionID string, seq int) {
	if sessionID == "" || seq <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq > c.lastSeq[sessionID] {
		c.lastSeq[sessionID] = seq
	}
}

// ReplayMarks is the per-session high-water marks, copied. Exposed for the
// status surfaces and for tests; nothing in the ladder reads it from outside.
func (c *Client) ReplayMarks() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	marks := make(map[string]int, len(c.lastSeq))
	for session, seq := range c.lastSeq {
		marks[session] = seq
	}
	return marks
}

// Start launches the supervisor goroutine.
func (c *Client) Start() { go c.supervise() }

// Next is the Bubble Tea integration: one message per call, so the model's
// Update sees backend events like any other message and there is no shared
// state between the reader goroutine and the model.
//
// Every handler that receives a client message must return Next() again, or
// the stream stops after one event.
func (c *Client) Next() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-c.msgs
		if !ok {
			return ClosedMsg{}
		}
		return msg
	}
}

// State is the current connection state.
func (c *Client) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Capabilities is what the backend said it can do. Zero-valued until the
// handshake completes.
func (c *Client) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Restarts is how many times the backend has been started again after the
// first successful connection — automatic restarts and manual retries alike.
//
// It is derived from the connection count rather than incremented by the
// automatic path, because a manual retry starts a real child process and a
// counter that ignored it was simply lying.
func (c *Client) Restarts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connects < 1 {
		return 0
	}
	return c.connects - 1
}

// InFlight lists the tasks the client believes are still running.
func (c *Client) InFlight() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return sortedKeys(c.inflight)
}

// Retry is the manual retry behind the failure banner.
func (c *Client) Retry() {
	select {
	case c.retry <- struct{}{}:
	default:
	}
}

// Send writes a command with no correlation waiter.
func (c *Client) Send(e Envelope) error {
	c.mu.Lock()
	b := c.backend
	c.mu.Unlock()
	if b == nil {
		return errors.New("backend is not connected")
	}
	return b.Send(e)
}

// Request sends and returns the channel that this request's replies arrive on.
//
// The timeout covers the ACK only: a command gets a fast ack and then a
// stream, so once anything has arrived the timer stops and the channel stays
// open until Release. A query, which gets exactly one result, is Released by
// its caller straight after reading it.
//
// The channel is closed on timeout, on Release, and when the connection is
// lost — a caller ranging over it is never left hanging.
func (c *Client) Request(e Envelope, timeout time.Duration) (<-chan Envelope, error) {
	if e.ID == "" {
		e.ID = newID()
	}
	if timeout <= 0 {
		timeout = c.opts.RequestTimeout
	}

	c.mu.Lock()
	b := c.backend
	if b == nil {
		c.mu.Unlock()
		return nil, errors.New("backend is not connected")
	}
	w := &waiter{ch: make(chan Envelope, 16)}
	w.timer = time.AfterFunc(timeout, func() { c.timeoutWaiter(e.ID) })
	c.pending[e.ID] = w
	c.mu.Unlock()

	if err := b.Send(e); err != nil {
		c.Release(e.ID)
		return nil, err
	}
	return w.ch, nil
}

// Release drops a correlation waiter and closes its channel. Calling it twice
// is safe.
func (c *Client) Release(id string) {
	c.mu.Lock()
	w, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if ok {
		w.timer.Stop()
		close(w.ch)
	}
}

func (c *Client) timeoutWaiter(id string) {
	c.mu.Lock()
	w, ok := c.pending[id]
	if !ok || w.arrived {
		c.mu.Unlock()
		return
	}
	delete(c.pending, id)
	c.mu.Unlock()

	c.log.Warn("request timed out with no ack", "corr_id", id)
	close(w.ch)
}

// Close stops the client and the child process.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.quit)
		<-c.done

		c.mu.Lock()
		b := c.backend
		c.backend = nil
		c.mu.Unlock()

		if b != nil {
			err = b.Close()
		}
	})
	return err
}

// ---------------------------------------------------------------------------
// The supervisor and the recovery ladder
// ---------------------------------------------------------------------------

func (c *Client) supervise() {
	defer close(c.done)
	defer close(c.msgs)

	attempt := 0

	for {
		select {
		case <-c.quit:
			return
		default:
		}

		attempt++
		c.emit(ConnectingMsg{Attempt: attempt})
		c.setState(StateStarting)

		b, err := c.opts.Dial()
		if err != nil {
			c.fail("could not start the backend", err.Error())
			if !c.waitForRetry() {
				return
			}
			continue
		}

		hello, herr := c.handshake(b)
		if herr != nil {
			_ = b.Close()
			if herr.refuse {
				// A version mismatch or a backend that cannot start is not
				// something a restart fixes. Refuse and wait for a human.
				if herr.mismatch != nil {
					c.emit(*herr.mismatch)
				}
				c.fail(herr.reason, herr.detail)
				if !c.waitForRetry() {
					return
				}
				continue
			}
			c.fail(herr.reason, herr.detail)
			if !c.waitForRetry() {
				return
			}
			continue
		}

		c.mu.Lock()
		c.backend = b
		c.caps = hello.Capabilities
		c.state = StateConnected
		c.connects++
		// Every connection after the first is a restart, whether the ladder
		// started it or the user pressed retry.
		restarted := c.connects > 1
		c.mu.Unlock()

		c.emit(ConnectedMsg{
			ProtocolVersion: hello.V,
			Capabilities:    hello.Capabilities,
			Restarted:       restarted,
		})

		if restarted {
			c.afterReconnect()
		}

		reason := c.run(b)

		c.mu.Lock()
		c.backend = nil
		c.mu.Unlock()
		c.failPendingWaiters()
		_ = b.Close()

		if reason == "" { // clean shutdown via Close
			return
		}

		unknown := c.InFlight()
		c.setState(StateLost)
		c.emit(BackendLostMsg{Reason: reason, UnknownTasks: unknown})

		if c.tooManyFailures() {
			// Second failure inside 60s: banner, no more automatic restarts.
			// The banner is a status-bar line with a retry action, not a
			// modal, so the rest of the app stays usable.
			c.markInterrupted()
			c.fail("the backend has failed twice in a minute", reason)
			if !c.waitForRetry() {
				return
			}
			continue
		}

		if !c.sleep(c.opts.RestartBackoff) {
			return
		}
	}
}

// handshakeError distinguishes "try again" from "refuse".
type handshakeError struct {
	reason   string
	detail   string
	refuse   bool
	mismatch *VersionMismatchMsg
}

func (e *handshakeError) Error() string { return e.reason + ": " + e.detail }

// handshake waits for hello.
//
// Envelopes that arrive before it are forwarded, not discarded — the backend
// emits hello first by design, but "first" is a property of the backend, not
// something this side should enforce by throwing away anything else.
func (c *Client) handshake(b Backend) (Hello, *handshakeError) {
	deadline := time.NewTimer(c.opts.HandshakeTimeout)
	defer deadline.Stop()

	for {
		select {
		case <-c.quit:
			return Hello{}, &handshakeError{reason: "cancelled", detail: "shutting down", refuse: true}

		case env, ok := <-b.Recv():
			if !ok {
				// stdout closed before hello. The child is gone, and its own
				// last words are the only useful explanation — the
				// single-instance lock refusal is exactly this case, and it
				// is not something a restart would fix.
				<-b.Exited()
				detail := b.StderrTail()
				if detail == "" {
					detail = fmt.Sprintf("exit: %v", b.ExitError())
				}
				return Hello{}, &handshakeError{
					reason: "the backend exited before the handshake",
					detail: detail,
					refuse: true,
				}
			}

			if env.Type != EvHello {
				c.deliver(env)
				continue
			}

			var hello Hello
			if err := env.UnmarshalPayload(&hello); err != nil {
				return Hello{}, &handshakeError{
					reason: "the backend's hello could not be parsed",
					detail: err.Error(),
					refuse: true,
				}
			}
			if hello.V != Version {
				return Hello{}, &handshakeError{
					reason: "protocol version mismatch",
					detail: fmt.Sprintf(
						"Point speaks envelope v%d; the backend speaks v%d. "+
							"Both sides must be on the same version — update whichever is older.",
						Version, hello.V),
					refuse:   true,
					mismatch: &VersionMismatchMsg{Client: Version, Backend: hello.V},
				}
			}
			return hello, nil

		case <-deadline.C:
			return Hello{}, &handshakeError{
				reason: "the backend did not answer the handshake",
				detail: fmt.Sprintf("no hello within %s", c.opts.HandshakeTimeout),
			}
		}
	}
}

// run pumps a live connection. It returns "" for a clean shutdown, or the
// reason the connection was lost.
func (c *Client) run(b Backend) string {
	watchdog := time.NewTicker(c.opts.PingInterval)
	defer watchdog.Stop()

	last := time.Now()

	for {
		select {
		case <-c.quit:
			return ""

		case env, ok := <-b.Recv():
			if !ok {
				<-b.Exited()
				if err := b.ExitError(); err != nil {
					return fmt.Sprintf("the backend exited (%v)", err)
				}
				return "the backend exited"
			}
			last = time.Now()
			c.deliver(env)

		case <-watchdog.C:
			// Two missed pings, OR process exit. The second is the case above;
			// this is the first, and it is what catches a HUNG backend, which
			// process-exit detection misses entirely.
			// One budget, whatever the backend is doing: B15's
			// unconditional ping (D98) is what makes that correct. There
			// is no longer a state in which silence is expected, so
			// there is no longer a reason to forgive it.
			budget := time.Duration(c.opts.MissedPings)*c.opts.PingInterval + c.opts.PingInterval
			if time.Since(last) > budget {
				return fmt.Sprintf("no word from the backend for %s", time.Since(last).Round(time.Second))
			}
		}
	}
}

// afterReconnect is the replay rung of F2's ladder, filled in by B15.
//
// It was a logged no-op until this episode, because F2 specified
// `session.events_since(session, last_seq)` and no such command existed —
// inventing backend surface from a frontend episode is what CLAUDE.md
// forbids. B15 built the command (as CmdSessionReplaySince, message-level
// over `messages.seq`; see types.go for why the name differs) and this is
// where it gets called.
//
// Fire-and-forget on purpose. This runs on the supervisor goroutine BEFORE
// run() starts reading, so a Request here would wait for a reply nobody is
// yet collecting; the replies arrive through the normal reader loop and
// deliver() routes them as RouteReplay, which is what a consumer merges by
// `seq`.
//
// markInterrupted still runs, and still runs here rather than after the
// replies. A reconnect means a NEW backend process — `restarted` is
// `connects > 1`, and a connect is a handshake with a freshly dialled child —
// so every task from the old process is dead whatever the replay says. What
// replay adds is not "maybe it survived": it is the persisted partial text
// and the authoritative FAILED/backend_died state for the task, which is the
// difference between a transcript that says "interrupted" and one that shows
// what was actually written before the crash.
func (c *Client) afterReconnect() {
	marks := c.ReplayMarks()
	sessions := make([]string, 0, len(marks))
	for session := range marks {
		sessions = append(sessions, session)
	}
	sort.Strings(sessions)

	for _, session := range sessions {
		payload, err := json.Marshal(map[string]any{
			"session":   session,
			"since_seq": marks[session],
			"limit":     replayLimit,
		})
		if err != nil { // a map of strings and ints cannot fail to marshal
			continue
		}
		env := Envelope{
			ID:        newID(),
			Type:      CmdSessionReplaySince,
			SessionID: session,
			Payload:   payload,
		}
		if err := c.Send(env); err != nil {
			c.log.Warn("replay request failed",
				"session", session, "since_seq", marks[session], "err", err)
			continue
		}
		c.log.Info("replay requested", "session", session, "since_seq", marks[session])
	}
	if len(sessions) == 0 {
		// Not a failure: this client has seen no session traffic yet, so
		// there is nothing a replay could rebuild.
		c.log.Info("reconnected with nothing to replay", "sessions", 0)
	}
	c.markInterrupted()
}

// markInterrupted is the ladder's last rung: still-missing tasks become
// FAILED, with their partial text kept and marked interrupted by whoever
// holds it.
func (c *Client) markInterrupted() {
	c.mu.Lock()
	ids := sortedKeys(c.inflight)
	c.inflight = make(map[string]struct{})
	c.mu.Unlock()

	if len(ids) > 0 {
		c.emit(TasksInterruptedMsg{TaskIDs: ids})
	}
}

func (c *Client) tooManyFailures() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	kept := c.failures[:0]
	for _, t := range c.failures {
		if now.Sub(t) < failureWindow {
			kept = append(kept, t)
		}
	}
	c.failures = append(kept, now)
	return len(c.failures) >= 2
}

func (c *Client) fail(reason, detail string) {
	c.setState(StateFailed)
	c.log.Error("backend unavailable", "reason", reason, "detail", detail)
	c.emit(BackendFailedMsg{Reason: reason, Detail: detail})
}

// waitForRetry blocks until the user presses retry, or the app quits. It
// returns false when the client should stop for good.
func (c *Client) waitForRetry() bool {
	select {
	case <-c.retry:
		c.mu.Lock()
		c.failures = nil
		c.mu.Unlock()
		return true
	case <-c.quit:
		return false
	}
}

func (c *Client) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-c.quit:
		return false
	}
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

// deliver routes one envelope and forwards it to the UI.
//
// Order is corr_id, then task_id, then session_id, exactly as F2 specifies.
// Routing does not consume: a correlated envelope reaches both its waiter and
// the model, because the model still has to render what a request produced.
func (c *Client) deliver(env Envelope) {
	c.trackTask(env)
	c.trackSession(env)

	if env.Type == EvProtocolError {
		var p struct {
			Line    int    `json:"line"`
			Text    string `json:"text"`
			Message string `json:"message"`
		}
		_ = env.UnmarshalPayload(&p)
		c.emit(ProtocolErrorMsg{Line: p.Line, Text: p.Text, Message: p.Message})
		return
	}

	route := RouteUnroutable
	switch {
	case env.Type == EvSessionReplay:
		// Before the correlation case, not after: a replay reply carries
		// the corr_id of whatever asked for it and a session_id, so it
		// would otherwise arrive as RouteCorrelation or RouteSession and
		// be indistinguishable from live traffic. A waiter still gets it
		// — a consumer that asked explicitly is not punished for it —
		// but the route the model sees says replay.
		_ = c.routeToWaiter(env)
		route = RouteReplay
	case env.CorrID != "" && c.routeToWaiter(env):
		route = RouteCorrelation
	case env.TaskID != "":
		route = RouteTask
	case env.SessionID != "":
		route = RouteSession
	case IsConnectionLevel(env.Type):
		route = RouteConnection
	}

	if route == RouteUnroutable {
		// Logged, never silently dropped: an unroutable event means a
		// contract bug, and the whole reason to log it is to see it.
		c.log.Warn("unroutable event",
			"type", env.Type, "id", env.ID, "corr_id", env.CorrID)
	}

	c.emit(EnvelopeMsg{Envelope: env, Route: route})
}

func (c *Client) routeToWaiter(env Envelope) bool {
	c.mu.Lock()
	w, ok := c.pending[env.CorrID]
	if ok && !w.arrived {
		w.arrived = true
		w.timer.Stop()
	}
	c.mu.Unlock()

	if !ok {
		return false
	}
	select {
	case w.ch <- env:
	default:
		// A caller that stopped reading must not wedge the reader goroutine.
		c.log.Warn("correlation channel full; dropping a reply",
			"corr_id", env.CorrID, "type", env.Type)
	}
	return true
}

// trackTask maintains the in-flight set. Types, not payloads — which task is
// running is routing information, and the guarantee that exactly one terminal
// event arrives per task is what makes the set trustworthy.
func (c *Client) trackTask(env Envelope) {
	if env.TaskID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case env.Type == EvTaskStarted:
		c.inflight[env.TaskID] = struct{}{}
	case IsTerminal(env.Type):
		delete(c.inflight, env.TaskID)
	}
}

// trackSession notes that this connection has seen traffic for a session, so a
// reconnect knows which sessions to replay (B15).
//
// It reads session_id and nothing else — a routing field, which is all this
// package interprets. The mark stays 0 until a consumer reports otherwise
// through MarkSeen; this only answers "which sessions exist as far as this
// client is concerned", which is a question no payload is needed for.
func (c *Client) trackSession(env Envelope) {
	if env.SessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, known := c.lastSeq[env.SessionID]; !known {
		c.lastSeq[env.SessionID] = 0
	}
}

func (c *Client) failPendingWaiters() {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]*waiter)
	c.mu.Unlock()

	for _, w := range pending {
		w.timer.Stop()
		close(w.ch)
	}
}

func (c *Client) setState(s ConnState) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
}

func (c *Client) emit(msg tea.Msg) {
	select {
	case c.msgs <- msg:
	case <-c.quit:
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
