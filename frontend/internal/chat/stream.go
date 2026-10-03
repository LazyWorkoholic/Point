// F5: the send path, the two lanes, the queue, cancellation, and the
// watchdog that guarantees a spinner always stops.
//
// The transcript F3 renders is w.all — messages the backend has already
// persisted. Nothing in flight is in there yet, so this episode adds a
// second, short list beside it: w.live, one entry per send this session
// made, rendered after every persisted message and never reordered. That
// ordering IS F5's "nothing ever jumps" guarantee — an entry's position is
// fixed the moment it is appended, so a task slot reserved at send time
// keeps its place while later chat lands below it, whatever order the
// backend finishes things in.
//
// A finished entry is deliberately NOT folded back into w.all. It would
// need an id w.all's cache keys by, and the only honest id is the backend's
// own message_id, which arrives (TaskStarted) before the text does — so the
// cache would key a growing string by a stable id, which is the one thing
// cache.go's invalidation rule does not cover. Live entries carry their own
// memo instead (renderLive, stream_render.go), and the next session load
// reads the real persisted message from the database like any other.
package chat

import (
	"encoding/json"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// streamThrottle is F5's "markdown re-rendered on a throttle (~30–60ms), not
// per token". A provider emitting 200 tokens a second would otherwise reparse
// the whole reply 200 times a second, each parse over a longer string than
// the last.
const streamThrottle = 50 * time.Millisecond

// idleTickInterval is how often the watchdog looks at a task that is running
// but silent. streamThrottle's rate is only worth paying while text is
// actually arriving; a task waiting on a slow first token needs nothing more
// than a coarse clock, and 500ms is far finer than the timeout it is
// measuring against.
const idleTickInterval = 500 * time.Millisecond

// quietAfter is how long without an event makes a running task "quiet", and
// so eligible for the slower tick above.
const quietAfter = time.Second

// defaultStreamTimeout matches config.Defaults().UI.StreamTimeout. Repeated
// rather than imported so a workspace built without Options.StreamTimeout —
// every test in this package — still has the watchdog, instead of silently
// having none.
const defaultStreamTimeout = 120 * time.Second

// firstTokenGraceFactor multiplies the silence budget until a task has
// produced its first token — see silenceDeadline. Loading a local model off
// disk is the one silence that is expected rather than suspicious, and the
// default budget is not generous enough for a cold Ollama start.
const firstTokenGraceFactor = 2

// boundsExceededCode is B10's `code` on TaskFailed for a loop that stopped
// at its own configured limit (max_steps, max_tool_calls or timeout_s) and
// kept whatever it had produced. It is an OUTCOME, not a fault, and it is
// emphatically not "no response" — the backend even writes its own
// self-describing message for it.
const boundsExceededCode = "bounds_exceeded"

// liveStatus is one entry's place in the lifecycle. Exactly one terminal
// status is ever reached (project_idea.md §4's rule, which the backend keeps
// and this file ASSERTS via the watchdog rather than trusting).
type liveStatus int

const (
	statusSending   liveStatus = iota // sent, no TaskStarted yet
	statusRunning                     // TaskStarted seen
	statusDone                        // TaskDone
	statusFailed                      // TaskFailed
	statusCancelled                   // TaskCancelled
	statusTimedOut                    // no event for streamTimeout — never from the wire
)

// terminal is "this entry is finished as far as the UI is concerned" — it
// stops the spinner, frees the lane and lets the queue fire.
func (s liveStatus) terminal() bool {
	switch s {
	case statusDone, statusFailed, statusCancelled, statusTimedOut:
		return true
	}
	return false
}

// wireTerminal is the narrower thing: an outcome the BACKEND actually
// reported. statusTimedOut is not one of them — it is the UI's own guess
// that nothing more is coming, and a guess has to be revisable:
//
//   - if the task speaks again, it was alive after all, and the entry goes
//     back to running (handleStreamEvent's revive below);
//   - if its real TaskDone arrives late, that outcome must win, or a
//     finished reply keeps a "no response" footer forever.
//
// So it is wireTerminal, not terminal, that enforces "exactly one terminal
// state". Conflating the two makes the watchdog — which exists to prevent
// one wrong ending — the cause of a different one.
func (s liveStatus) wireTerminal() bool {
	switch s {
	case statusDone, statusFailed, statusCancelled:
		return true
	}
	return false
}

// lane is F5's TURN/TASK split.
//
// The backend decides it, not this file: agents\executor.py runs a bounded
// loop when max_steps > 1 and reports the outcome as `background` on
// TaskStarted's payload. But F5 requires the slot be reserved at SEND time,
// before any reply has arrived — so a send whose leading @token names an
// agent is reserved OPTIMISTICALLY as a task, and reconciled the moment
// TaskStarted actually says. laneTask that turns out to be inline collapses
// into laneTurn in place; it has not moved, so nothing jumps either way.
type lane int

const (
	laneTurn lane = iota
	laneTask
)

// liveKind separates the two things a send puts on screen.
//
// BOTH are live entries, in one list, in creation order. The first cut of
// F5 put the user's own message into w.all and only the reply into w.live,
// and since entries.go rendered "all of w.all, then all of w.live", the
// second send in any session came out as Q1 · Q2 · A1 · A2 — every question
// clustered, then every answer. Queue and retry only made it obvious; a
// plain second send did it too. Two append-only lists cannot express one
// interleaved order, so the user message moved here, where creation order
// IS display order and there is nothing left to interleave.
type liveKind int

const (
	liveUserMsg liveKind = iota // what the user sent
	liveReply                   // what came back (turn lane or task lane)
)

// liveEntry is one send and everything that came back from it.
type liveEntry struct {
	kind liveKind

	requestID string // the id WE minted for CmdSendMessage; every reply's corr_id
	taskID    string // the backend's, known from TaskStarted onward
	lane      lane
	status    liveStatus

	agentID  string // "" for a plain turn
	userText string // what was sent — kept for Retry, which needs it verbatim

	text     string // accumulated assistant reply
	thinking string // accumulated thinking, rendered collapsed
	expanded bool   // is the thinking block open
	slotOpen bool   // is a finished TASK slot expanded

	step, maxSteps int
	progressNote   string // a rate-limit notice or other non-step progress

	// usage is F6's: the latest TokenUsage this entry's stream has reported,
	// mirrored onto the Workspace (below) so the Status card reads real
	// numbers instead of a second implementation of the accumulator's math.
	usage wireUsage

	errText string
	// errCode is TaskFailed's `code`. B10 puts `bounds_exceeded` there
	// specifically so a client can tell a loop that stopped AT ITS OWN
	// LIMIT — and kept its partial answer — from a genuine failure, without
	// parsing the message. The footer says two different things because
	// they mean two different things.
	errCode string

	// sawOutput records that this task has produced at least one token or
	// thinking delta. Until it has, the watchdog allows longer — see
	// silenceDeadline.
	sawOutput bool

	startedAt   time.Time
	lastEventAt time.Time // what the watchdog measures
	finishedAt  time.Time

	// Render memo — see renderLive (stream_render.go). Live text changes on
	// every token, so cache.go's by-id cache cannot hold it; this is the
	// same idea scoped to one entry and invalidated by version.
	version         int
	renderedVersion int
	renderedWidth   int
	renderedFocused bool
	renderedAt      time.Time
	renderedSecond  int
	rendered        []string

	// renderedThemeGen is the Workspace.themeGen this entry's rendered
	// lines were coloured with (chat.go's doc on themeGen). None of
	// version/width/focused change when Settings' Appearance tab (F10)
	// repaints the theme, so without this the memo's every freshness check
	// stayed satisfied and a streaming entry's "Point:" label and cursor
	// bar kept their OLD colours until something ELSE happened to bump
	// version/width/focused (a token arriving, a resize) — found live:
	// changing the theme repainted everything except the transcript, which
	// then jumped to the new palette on the next unrelated keypress that
	// forced a real re-render for an unrelated reason.
	renderedThemeGen int

	// renderedFrame/servedVersion describe what this entry ANSWERED WITH
	// in a given frame, which is not the same thing as what is cached:
	// a throttled call serves lines rendered at an older version, and the
	// rest of that frame has to be given those same lines. See renderLive.
	renderedFrame int
	servedVersion int
}

func (e *liveEntry) touch() {
	e.lastEventAt = time.Now()
	e.version++
}

// interrupted is F5's one rendering path for two causes: the user cancelled,
// or the backend died under a running task (F2's crash recovery, which
// arrives as the same partial-reply-plus-marker shape). A timed-out task is
// deliberately NOT interrupted — nothing is known to have been cut short,
// only that nothing came back.
func (e *liveEntry) interrupted() bool { return e.status == statusCancelled }

// retryable: it ended, and it ended without an answer. These are exactly the
// three states liveFooter puts a `[Retry ^R]` on, so the affordance and the
// action cannot drift apart.
func (e *liveEntry) retryable() bool {
	switch e.status {
	case statusCancelled, statusFailed, statusTimedOut:
		return true
	}
	return false
}

// queuedSend is the one pending message. Depth is 1 by design (F5), so this
// is a pointer, not a slice: a queue that cannot hold two cannot be indexed
// wrong.
type queuedSend struct {
	text    string
	agentID string
}

// ---------------------------------------------------------------------------
// Lane and queue predicates
// ---------------------------------------------------------------------------

// activeTurn returns the entry currently occupying the TURN lane, if any.
// The turn lane is sequential — that is the whole reason the queue exists.
func (w *Workspace) activeTurn() *liveEntry {
	for _, e := range w.live {
		if e.kind == liveReply && e.lane == laneTurn && !e.status.terminal() {
			return e
		}
	}
	return nil
}

// Streaming implements app.StreamAware: is anything at all still running.
// Covers both lanes — a background research task is activity too, and the
// header animation is an activity indicator, not a token indicator.
func (w *Workspace) Streaming() bool {
	for _, e := range w.live {
		if e.kind == liveReply && !e.status.terminal() {
			return true
		}
	}
	return false
}

// HasPending implements app.QueueAware.
func (w *Workspace) HasPending() bool { return w.queued != nil }

// inputLocked gates every editing keystroke (input_keys.go's own first
// line). F05-streaming-queue.md's Do NOT was explicit that queue capacity is
// the ONLY reason to lock the input, and for a long time it was the only
// disjunct here. F10 adds a second, independent one: providerMissing, UI-03's
// "no provider configured" state — the spec's own words for it are "input
// disabled, with the reason and the fix inline", and a dead input a user can
// still type into is not that. Note what neither case is: it is not "a
// reply is streaming" — composing while a reply streams is the queue's whole
// point.
func (w *Workspace) inputLocked() bool { return w.queued != nil || w.providerMissing() }

// lockReason is never allowed to be empty while w.queued != nil. A dead
// input with no explanation is the failure F5's verification looks for by
// name, so the reason travels with the lock rather than being rendered
// somewhere that could get out of step with it. providerMissing's own
// reason is a separate constant (render.go's queryLines) since the two
// causes need different words and different fix keys.
const lockReason = "queue full — esc to discard pending"

// providerMissingReason is providerMissing's own "the reason and the fix" —
// the query area shows this instead of the input the moment nothing has
// been chosen yet, naming the exact chord that fixes it (actions.go's
// settings.open, leader "s").
const providerMissingReason = "no provider configured — ctrl+w s to set one up"

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

// splitAgent pulls a leading @agent token off the composed text.
//
// It reuses F4's static agent map (input_render.go's knownAgents) — the same
// stopgap, at a second call site now. Whichever episode adds a real
// ListAgents query has to replace ALL THREE readers: this one, F4's own
// agentTokenState, and S11b's reloadLane (reload.go). See F8's handoff note.
func splitAgent(text string) (agentID, body string) {
	trimmed := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(trimmed, "@") {
		return "", text
	}
	rest := trimmed[1:]
	cut := strings.IndexAny(rest, " \t\n")
	name := rest
	if cut >= 0 {
		name = rest[:cut]
	}
	if !isKnownAgent(name) {
		return "", text
	}
	if cut < 0 {
		return name, ""
	}
	return name, strings.TrimLeft(rest[cut:], " \t")
}

// onSend replaces F4's stub — the seam its own doc comment named.
//
// F4 cleared the input as if the message had been sent; this sends it. The
// clear stays last, exactly as that comment asked, so a send that is refused
// (empty, or locked) never eats what was composed.
func (w *Workspace) onSend() tea.Cmd {
	if len(w.input.buf.runes) == 0 {
		return nil
	}
	if w.inputLocked() {
		// Nothing is consumed and nothing is said beyond the reason
		// already on screen — the lock is not a place to add noise.
		return nil
	}
	raw := w.input.buf.text()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if isDetachCommand(raw) {
		// A command, never text sent to the model — detachChip clears the
		// input's own way (draft save), so this returns straight from here
		// rather than falling into the ordinary send path below.
		w.input.buf.ClearKeepingUndo()
		return w.detachChip(len(w.input.chips) - 1)
	}
	agentID, body := splitAgent(raw)

	// "Agent tasks never enter this queue" — an @agent send fires
	// immediately even while a turn is streaming, because the two lanes are
	// independent. Only a plain turn can be blocked by a turn.
	if agentID == "" && w.activeTurn() != nil {
		w.queued = &queuedSend{text: body}
		w.clearInput()
		return w.clearDraft()
	}

	cmd := w.startSend(body, agentID)
	w.clearInput()
	return tea.Batch(cmd, w.clearDraft())
}

func (w *Workspace) clearInput() {
	w.input.buf.ClearKeepingUndo() // see its doc — recoverable via Ctrl+Z
	// Chips are NOT cleared here. B12: an attachment is per-SESSION, not
	// per-message — "the same file stays attached across turns" is the
	// whole reason its (mtime, size) pair is worth recording at all.
	// Clearing them on every send (F4's original behaviour, written before
	// any attachment was ever real) would detach every file the instant it
	// was first used. A chip goes away only through detachChip
	// (attachments.go) — backspace at position 0, or /detach. F08's own
	// wording also lists "esc" as a way to detach; esc is already bound to
	// entering browse mode (chat.browse) for every OTHER purpose, and this
	// episode did not carve out a chip-focused exception to it — see this
	// episode's handoff notes.
	w.input.scrollTop = 0
}

// startSend appends the user's message and the reply as two entries in ONE
// ordered list, then writes CmdSendMessage.
//
// Both go into w.live, adjacent, in that order. The user message is not
// waited for from the backend — the backend persists it (agents\executor.py's
// run appends a UserMessage) but never echoes it back, and this workspace
// does not re-fetch after a send. On the next session load the real
// persisted row is what renders.
func (w *Workspace) startSend(body, agentID string) tea.Cmd {
	if w.sessionID == "" {
		w.loadErr = "no session yet — the backend has not answered"
		return nil
	}

	shown := body
	if agentID != "" {
		shown = "@" + agentID + " " + body
	}

	// The user's own message. statusDone from birth: it is not a task, and
	// an entry that never reaches a terminal state keeps Streaming() true
	// forever — which keeps the animation and watchdog ticks re-arming
	// forever. That was a real idle-CPU leak; a user message must never be
	// able to cause it.
	w.live = append(w.live, &liveEntry{
		kind:      liveUserMsg,
		status:    statusDone,
		userText:  shown,
		agentID:   agentID,
		startedAt: time.Now(),
	})

	w.pinned = true
	w.focus = -1

	e := &liveEntry{
		kind:        liveReply,
		requestID:   localID(),
		lane:        laneTurn,
		status:      statusSending,
		agentID:     agentID,
		userText:    shown,
		startedAt:   time.Now(),
		lastEventAt: time.Now(),
	}
	if agentID != "" {
		// The optimistic reserve. See lane's doc comment.
		e.lane = laneTask
	}
	w.live = append(w.live, e)

	payload := map[string]any{"session": w.sessionID, "text": body}
	if agentID != "" {
		payload["agent"] = agentID
	}
	return tea.Batch(w.sendMsgFn(e, payload), w.armTick())
}

// writeSend is the wire write. Unlike protocol.go's doSend it registers no
// correlation waiter: a send answers with a whole STREAM of events sharing
// one corr_id, and Request's waiter channel holds exactly one. The events
// reach this workspace through Root's ordinary EnvelopeMsg forwarding
// either way; client.go's deliver() routes anything with a task_id as
// RouteTask, so nothing here is logged as unroutable.
func (w *Workspace) writeSend(e *liveEntry, payload map[string]any) tea.Cmd {
	// A send that never reaches the wire still has a live entry sitting in
	// statusSending, and a non-terminal entry keeps Streaming() true — so
	// the header animation (120ms) and the watchdog (500ms) both re-arm
	// forever, on an app that is doing nothing. That is an unbounded idle
	// CPU leak with no way out, and it is why every failure path here ends
	// the entry.
	//
	// None of them sets w.loadErr any more. That banner used to come from
	// here, which made it depend on which of two timing windows a send
	// landed in — see connection.go. "The backend is not connected" is now
	// drawn from connection state alone; this function only records how its
	// OWN send ended, on its own reply, where the user sees it.
	fail := func(reason string) tea.Cmd {
		e.errText = reason
		w.finish(e, statusFailed)
		return nil
	}
	if w.client == nil {
		return fail("no backend connection")
	}
	if w.connectionDown() {
		// Refused by the same check that draws the banner, so the two can
		// never disagree about whether the backend was there.
		return fail(w.connectionNotice())
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fail(err.Error())
	}
	if err := w.client.Send(protocol.Envelope{
		ID: e.requestID, Type: protocol.CmdSendMessage, Payload: raw,
	}); err != nil {
		return fail(err.Error())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Incoming events
// ---------------------------------------------------------------------------

// entryFor finds the live entry an envelope belongs to. corr_id first — it
// is the id this workspace minted and therefore the only one that is
// unambiguous before TaskStarted has named a task.
func (w *Workspace) entryFor(env protocol.Envelope) *liveEntry {
	if env.CorrID != "" {
		for _, e := range w.live {
			if e.kind == liveReply && e.requestID == env.CorrID {
				return e
			}
		}
	}
	if env.TaskID != "" {
		for _, e := range w.live {
			if e.kind == liveReply && e.taskID == env.TaskID {
				return e
			}
		}
	}
	return nil
}

// handleStreamEvent consumes anything addressed to a live entry. It reports
// whether it took the envelope, so protocol.go's handleEnvelope can fall
// through to F3's query replies for everything else.
func (w *Workspace) handleStreamEvent(env protocol.Envelope) (tea.Cmd, bool) {
	e := w.entryFor(env)
	if e == nil {
		return nil, false
	}

	// The watchdog was wrong: something arrived after all. Whatever this
	// event is, it is proof the task is alive, so the provisional
	// no-response state is withdrawn before the event is applied. A
	// genuinely finished entry (wireTerminal) is never revived — a late
	// stray token cannot restart a reply the backend already ended.
	if e.status == statusTimedOut {
		e.status = statusRunning
	}

	switch env.Type {
	case protocol.EvTaskStarted:
		var p struct {
			MessageID  string `json:"message_id"`
			Background bool   `json:"background"`
		}
		_ = env.UnmarshalPayload(&p)
		e.taskID = env.TaskID
		if e.status.wireTerminal() {
			// The entry already ended — cancelled before the backend had
			// named a task, so cancelStream had no id to send and closed
			// it locally. NOW there is an id, and the task behind it is
			// genuinely running: cancel it for real, or the user sees a
			// stopped reply while the backend keeps generating one.
			// Reopening the entry is not an option (exactly one terminal
			// state), so this is the only place that leak can be closed.
			e.touch()
			return tea.Batch(w.sendCancel(e.taskID), w.armTick()), true
		}
		e.status = statusRunning
		// The reconcile half of the optimistic reserve: the backend has
		// now said which lane this really is, and it wins. An @agent send
		// the backend chose to run inline collapses to a normal streaming
		// turn, in place.
		if p.Background {
			e.lane = laneTask
		} else {
			e.lane = laneTurn
		}
		e.touch()

	case protocol.EvTextDelta, protocol.EvTaskToken:
		e.text += deltaText(env)
		e.sawOutput = true
		e.touch()

	case protocol.EvThinkingDelta:
		e.thinking += deltaText(env)
		e.sawOutput = true
		e.touch()

	case protocol.EvProgressUpdated:
		var p struct {
			Data struct {
				Step     int    `json:"step"`
				MaxSteps int    `json:"max_steps"`
				Message  string `json:"message"`
			} `json:"data"`
		}
		_ = env.UnmarshalPayload(&p)
		e.step, e.maxSteps = p.Data.Step, p.Data.MaxSteps
		e.progressNote = p.Data.Message
		e.touch()

	case protocol.EvToolStarted, protocol.EvToolCompleted, protocol.EvStatusChanged:
		// Not rendered by this episode (tool cards are F9), but they ARE
		// evidence the task is alive, which is exactly what the watchdog
		// measures. Silently ignoring them would let a long, tool-heavy
		// run trip a timeout it is nowhere near.
		e.touch()

	case protocol.EvUsageUpdated:
		// F6's Status card "tokens / cost" line. The context-fill bar above
		// it is a separate, unwired number (this episode's stub — B8's
		// context.report, not this) — this is real per-turn TokenUsage,
		// straight off the provider, and it is evidence of life too.
		var p struct {
			Data struct {
				Usage wireUsage `json:"usage"`
			} `json:"data"`
		}
		_ = env.UnmarshalPayload(&p)
		if p.Data.Usage.TotalTokens > 0 {
			e.usage = p.Data.Usage
			w.usage = p.Data.Usage
		}
		e.touch()

	case protocol.EvTaskDone:
		w.finish(e, statusDone)

	case protocol.EvTaskFailed:
		var p struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = env.UnmarshalPayload(&p)
		e.errText = p.Error
		e.errCode = p.Code
		if e.errText == "" {
			e.errText = "the task failed"
		}
		w.finish(e, statusFailed)

	case protocol.EvTaskCancelled:
		w.finish(e, statusCancelled)

	case protocol.EvTaskCancelAccepted:
		// NOT terminal — types.go's IsTerminal says so, and the sink still
		// emits TaskCancelled afterwards. Treating this as the end would
		// leave the real terminal event with nothing to land on.
		e.touch()

	case protocol.EvError:
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		e.errText = p.Error
		if e.errText == "" {
			e.errText = "the backend reported an error"
		}
		w.finish(e, statusFailed)

	default:
		return nil, false
	}

	return tea.Batch(w.armTick(), w.fireQueued()), true
}

// deltaText reads the text out of either token-shaped event.
//
// There are two, on different backend paths, and neither is a mistake:
// TextDelta wraps a streaming provider's StreamEvent (text under `data`),
// while TaskToken is the echo provider's own simpler envelope (text at the
// top level). Both are in protocol.IsDroppable; both are real.
func deltaText(env protocol.Envelope) string {
	var p struct {
		Text string `json:"text"`
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return ""
	}
	if p.Data.Text != "" {
		return p.Data.Text
	}
	return p.Text
}

func (w *Workspace) finish(e *liveEntry, status liveStatus) {
	if e.status.wireTerminal() {
		return // exactly one terminal state, whatever the wire does
	}
	e.status = status
	e.finishedAt = time.Now()
	e.touch()

	// One line per finished reply, with the ids it was routed by and the
	// head and tail of what it actually accumulated.
	//
	// This exists to settle a field report of one task's slot showing two
	// tasks' answers run together. Both candidate causes are disprovable
	// from this line alone and need opposite fixes: if ONE corr_id/task_id
	// accumulated both topics, the merge happened before the wire and is
	// the backend's; if two entries each carry their own ids but the text
	// is crossed, it is this package's routing. Head and tail rather than
	// the whole body because a reply is thousands of tokens and a log is
	// not a transcript.
	if w.log != nil {
		head, tail := e.text, ""
		if len(head) > 60 {
			tail = head[len(head)-60:]
			head = head[:60]
		}
		w.log.Debug("chat: reply finished",
			"corr_id", e.requestID, "task_id", e.taskID,
			"agent", e.agentID, "lane", int(e.lane), "status", int(status),
			"bytes", len(e.text), "head", head, "tail", tail)
	}
}

// fireQueued sends the pending message once the turn lane is free. F5:
// "reply completes → the pending message fires automatically", and the same
// applies after a cancel, which is what makes cancelling with something
// queued feel like skipping rather than losing.
func (w *Workspace) fireQueued() tea.Cmd {
	if w.queued == nil || w.activeTurn() != nil {
		return nil
	}
	q := w.queued
	w.queued = nil
	return w.startSend(q.text, q.agentID)
}

// ---------------------------------------------------------------------------
// Cancel, discard, edit, retry
// ---------------------------------------------------------------------------

// cancelStream is ctrl+c. With nothing streaming it does nothing at all —
// no status line, no beep, and above all no quit.
func (w *Workspace) cancelStream() tea.Cmd {
	e := w.activeTurn()
	if e == nil {
		// Fall back to the newest running task: with no turn in flight,
		// ctrl+c should still stop a background agent rather than being
		// inert while something is visibly running.
		for i := len(w.live) - 1; i >= 0; i-- {
			if !w.live[i].status.terminal() {
				e = w.live[i]
				break
			}
		}
	}
	if e == nil {
		return nil
	}
	if e.taskID == "" {
		// Cancelled before TaskStarted named a task. There is nothing the
		// backend can be asked to stop by id, so the entry is closed
		// locally; a TaskStarted that arrives afterwards finds a terminal
		// entry and finish() refuses to reopen it.
		w.finish(e, statusCancelled)
		return w.fireQueued()
	}
	// The real TaskCancelled comes back over the wire and is what actually
	// moves the entry to its terminal state — this is a request, not the
	// outcome.
	return w.sendCancel(e.taskID)
}

// sendCancel writes one CancelTask. Its ack (task.cancel.accepted) is not
// terminal and its outcome (TaskCancelled) arrives separately — see
// handleStreamEvent.
func (w *Workspace) sendCancel(taskID string) tea.Cmd {
	if w.client == nil || taskID == "" {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"task_id": taskID})
	if err != nil {
		return nil
	}
	_ = w.client.Send(protocol.Envelope{
		ID: localID(), Type: protocol.CmdCancelTask, Payload: raw,
	})
	return nil
}

// discardPending is esc at queue capacity. It also releases the input lock,
// which is the whole reason the reason line names esc.
func (w *Workspace) discardPending() tea.Cmd {
	w.queued = nil
	return nil
}

// editPending is ctrl+e: the queued text goes back into the input, still
// queued-free, so it can be fixed and sent again.
func (w *Workspace) editPending() tea.Cmd {
	if w.queued == nil {
		return nil
	}
	text := w.queued.text
	if w.queued.agentID != "" {
		text = "@" + w.queued.agentID + " " + text
	}
	w.queued = nil
	w.input.buf.setText(text, len([]rune(text)))
	w.input.focused = true
	return w.scheduleDraftSave()
}

// retryFocused re-sends an entry that ended without an answer — cancelled,
// failed, or timed out.
//
// It retries the FOCUSED entry when that entry is retryable, and only falls
// back to the most recent retryable one when nothing retryable is focused.
// The first cut always took the most recent, which is why ctrl+R on a
// focused failed research slot retried a different task entirely: with
// several dead entries on screen, "the newest" and "the one you are looking
// at" are rarely the same, and only one of them is a target the user chose.
//
// The entry is carried by POINTER from here on. Resolving it again later by
// list index would reintroduce the same class of bug the moment anything
// appends to w.live — which retrying does, immediately.
func (w *Workspace) retryFocused() tea.Cmd {
	e := w.retryTarget()
	if e == nil {
		return nil
	}
	body := e.userText
	agentID := e.agentID
	if agentID != "" {
		_, body = splitAgent(e.userText)
	}
	if agentID == "" && w.activeTurn() != nil {
		w.queued = &queuedSend{text: body}
		return nil
	}
	return w.startSend(body, agentID)
}

// retryTarget is what ctrl+R would act on right now — focus first, then the
// most recent retryable entry. Exported to the renderer's eye only: the
// footer says `[Retry ^R]`, and this is what that promise resolves to.
func (w *Workspace) retryTarget() *liveEntry {
	if e := w.liveAt(w.focusedIndex()); e != nil && e.retryable() {
		return e
	}
	for i := len(w.live) - 1; i >= 0; i-- {
		if e := w.live[i]; e.kind == liveReply && e.retryable() {
			return e
		}
	}
	// S11b: then a reloaded one. AFTER w.live, never before — a live entry
	// is always the newer of the two, and "the most recent retryable" has
	// to keep meaning that. This only ever fires in a transcript with no
	// live sends at all, so it cannot change what ^R picks in a session
	// that has sent anything.
	//
	// It is here because the footer is: a reloaded failure renders
	// `[Retry ^R]` through liveFooter, and an affordance with no key
	// behind it is worse than none — F5's own words, and the reason live
	// has this fallback in the first place.
	for i := len(w.hist) - 1; i >= 0; i-- {
		if e := w.hist[i]; e != nil && e.retryable() {
			return e
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The watchdog
// ---------------------------------------------------------------------------

type streamTickMsg struct{ gen int }

// armTick schedules the next tick if one is not already in flight and
// anything is still running. Nothing running means nothing scheduled, which
// is what makes an untouched app cost nothing — F5's idle-CPU check.
func (w *Workspace) armTick() tea.Cmd {
	if w.tickArmed || !w.Streaming() {
		return nil
	}
	w.tickArmed = true
	w.tickGen++
	gen := w.tickGen
	return tea.Tick(w.tickInterval(), func(time.Time) tea.Msg {
		return streamTickMsg{gen: gen}
	})
}

// tickInterval is fine while text is arriving and coarse while it is not.
// The fast rate exists to serve the render throttle; a task that has said
// nothing for a second has no frames to pace, only a deadline to measure.
func (w *Workspace) tickInterval() time.Duration {
	now := time.Now()
	for _, e := range w.live {
		if !e.status.terminal() && now.Sub(e.lastEventAt) < quietAfter {
			return streamThrottle
		}
	}
	return idleTickInterval
}

// silenceDeadline is how long THIS entry may stay silent before the UI
// stops its spinner.
//
// It is a SILENCE budget, not a duration cap: lastEventAt is reset by
// touch() on every text delta, thinking delta, progress update and tool
// event, so a task that keeps talking never trips it however long it runs.
// Only a task that has said nothing for the whole budget does.
//
// Before the first token the budget is multiplied. Waiting on a cold local
// model is not the same event as a stream going quiet half way through: an
// Ollama model being loaded from disk can legitimately say nothing for a
// long time, and a first-token wait is the one silence that is expected.
// The multiplier keeps it proportional, so a small stream_timeout set for
// testing still behaves predictably instead of being swamped by a constant.
func (w *Workspace) silenceDeadline(e *liveEntry) time.Duration {
	timeout := w.streamTimeout
	if timeout <= 0 {
		timeout = defaultStreamTimeout
	}
	if !e.sawOutput {
		return timeout * firstTokenGraceFactor
	}
	return timeout
}

// onTick repaints throttled text and enforces the terminal-event
// discipline: a task in a running state with no events for streamTimeout is
// moved to its own terminal state here, by the UI, rather than waiting for
// a backend event that — by definition of this branch being reached — is
// not coming. A spinner that can never stop is the failure this prevents.
func (w *Workspace) onTick(gen int) tea.Cmd {
	if gen != w.tickGen {
		return nil
	}
	w.tickArmed = false

	now := time.Now()
	var fired tea.Cmd
	for _, e := range w.live {
		if e.status.terminal() {
			continue
		}
		if now.Sub(e.lastEventAt) >= w.silenceDeadline(e) {
			w.finish(e, statusTimedOut)
			// A queued message must not be stranded behind a task that
			// stopped answering.
			if c := w.fireQueued(); c != nil {
				fired = c
			}
		}
	}
	return tea.Batch(fired, w.armTick())
}

// ---------------------------------------------------------------------------
// Message routing
// ---------------------------------------------------------------------------

// handleStreamMsg takes the app-level F5 messages Root routes here. It
// returns false for anything it does not own so protocol.go's Update keeps
// its existing shape.
func (w *Workspace) handleStreamMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case app.CancelStreamMsg:
		return w.cancelStream(), true
	case app.DiscardPendingMsg:
		return w.discardPending(), true
	case app.EditPendingMsg:
		return w.editPending(), true
	case app.RetryMsg:
		return w.retryFocused(), true
	case streamTickMsg:
		return w.onTick(m.gen), true
	}
	return nil, false
}

var (
	_ app.StreamAware = (*Workspace)(nil)
	_ app.QueueAware  = (*Workspace)(nil)
)
