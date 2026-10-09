// S11b: making a RELOADED transcript render exactly like the live one it
// replaces.
//
// F5 shipped correct for a live session and wrong for a reloaded one, for
// one reason: the data needed to reconstruct an exchange did not exist.
// B12 added it — `task_id` on user messages, `invocation` on every
// persisted assistant message, and a terminal `outcome` beside it — and
// this file is the whole of spending it.
//
// Three transforms, in the order they run:
//
//  1. pairReloaded  — reorder rows so an answer sits under its own
//     question, keyed on task_id and NOTHING else.
//  2. decorate      — build the live-shaped entries that turn a research
//     answer back into a collapsed slot and a non-clean ending back into
//     its footer.
//  3. applyTaskRows — the second pass, once ListTasks answers: the one
//     case whose outcome is not on the message at all, because there is
//     no message.
//
// # Why pairing may key on nothing but task_id
//
// F5's verification established the defect precisely, and it is NOT
// "background tasks misorder":
//
//	Anything in flight at the same time splits on reload. Two sends that
//	overlap both persist their questions before either answer arrives, so
//	the database holds Q,Q,A,A and reload rendered it that way.
//
// Sequential and queued sends reloaded correctly only by accident of
// timing — a queued turn genuinely waits, so its rows happen to land in
// order. That is why F5's adjacency-based retry fold was DELETED rather
// than refined: it inferred structure from position and corrupted correct
// transcripts, orphaning answers and inventing "retried" labels on sends
// that were never retried. Adjacency, arrival order and elapsed time are
// all ruled out here permanently. The persisted task_id is the only key.
package chat

import (
	"strings"
	"time"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// taskRowLimit is what ListTasks is asked for on session load.
//
// Its own default is 50, and it answers across ALL sessions (runtime.py's
// _handle_list_tasks says so, and that is deliberate — F6's task panel is
// global). A busy history would therefore push this session's own older
// tasks out of a 50-row answer, and the rows that fell out are exactly the
// ones whose footers would go missing. This is not a page size the user
// scrolls; it is a lookup table consulted once, so it is set high enough
// that the join is not silently partial.
const taskRowLimit = 500

// ---------------------------------------------------------------------------
// 1 — pairing
// ---------------------------------------------------------------------------

// pairReloaded reorders persisted rows so that every answer sits directly
// under the question it answered, ordered by the exchange's own start.
//
// The rules, and each one is load-bearing:
//
//   - A user row with a task_id claims the FIRST later assistant row
//     carrying the same task_id. Later, because the backend persists the
//     question before the task can produce anything (executor.py generates
//     task_id, then appends the UserMessage, then runs), so an answer is
//     always downstream of its question. There is at most one assistant
//     row per task — runtime.py's terminal handler appends exactly one, or
//     none — so this is 1:1, never 1:many.
//   - Everything else keeps its database position: rows with no task_id
//     (every row written before B12), assistant rows nobody claimed, tool
//     and system rows. Nothing is dropped and nothing is invented.
//   - A question whose answer does not exist stays a question with no
//     reply. It is NOT quietly given the next unclaimed answer — that is
//     precisely the corruption F5 removed, in a new disguise.
//
// It is a stable reorder: with one send at a time, or with rows that
// predate B12, the output is the input, verbatim.
//
// F18 changed what `msgs` IS — the resident window rather than the whole
// session — and changed nothing else, because the no-reorder-across-an-edge
// property the windowed case needs is already structural here. `answerAt` is
// built from the slice it was handed, so an answer that is not in the slice
// is not a candidate: a question whose answer sits one batch below is simply
// left unpaired, exactly as a question whose answer does not exist is. When
// that batch loads, the rebuild re-runs this over the longer run and the
// pairing resolves. The visible cost is that a slot or footer at the very
// edge of the window appears one batch late, and that rows near a batch seam
// settle into their paired order as the seam fills in — expected behaviour
// during a fast scroll, not a content jump.
func pairReloaded(msgs []wireMessage) []wireMessage {
	if len(msgs) < 2 {
		return msgs
	}

	// The first unclaimed assistant row per task, by index. Built in one
	// forward pass so "first" means first in database order.
	answerAt := make(map[string]int, len(msgs))
	for i, m := range msgs {
		if m.Role != roleAssistant || m.TaskID == "" {
			continue
		}
		if _, seen := answerAt[m.TaskID]; !seen {
			answerAt[m.TaskID] = i
		}
	}

	out := make([]wireMessage, 0, len(msgs))
	taken := make([]bool, len(msgs))
	for i, m := range msgs {
		if taken[i] {
			continue // already emitted, directly under its question
		}
		out = append(out, m)
		if m.Role != roleUser || m.TaskID == "" {
			continue
		}
		j, ok := answerAt[m.TaskID]
		if !ok || j <= i || taken[j] {
			continue
		}
		taken[j] = true
		out = append(out, msgs[j])
	}
	return out
}

// ---------------------------------------------------------------------------
// 2 — decoration
// ---------------------------------------------------------------------------

// decorateReloaded builds w.hist from w.all: the live-shaped entry for each
// persisted row that must render as a slot or carry a footer, and nil for
// every row that renders as the ordinary message F3 already drew.
//
// It runs on the paired list, so an entry's index is its display index.
func (w *Workspace) decorateReloaded() {
	w.hist = make([]*liveEntry, len(w.all))
	for i, m := range w.all {
		q, _ := w.questionFor(i)
		w.hist[i] = reloadedEntry(m, q)
	}
}

// questionFor is the question the answer at index i replied to — the user
// row pairReloaded just placed above it, found by the same task_id and by
// nothing else.
//
// Two things on the entry come from it. The obvious one is the clock: a
// finished slot reads `✓ @research done 0:42`, and the span the live clock
// measured is send → finish, which is exactly this row's timestamp to the
// answer's. The other is Retry's text — see reloadUserText.
func (w *Workspace) questionFor(i int) (wireMessage, bool) {
	m := w.all[i]
	if m.Role != roleAssistant || m.TaskID == "" {
		return wireMessage{}, false
	}
	// Only the row directly above, because pairReloaded has already run:
	// a paired answer sits immediately under its own question, so that is
	// the only place its question can be. The task_id check still does the
	// deciding — an unclaimed answer that merely happens to follow some
	// other question correctly finds nothing.
	if i == 0 {
		return wireMessage{}, false
	}
	if q := w.all[i-1]; q.Role == roleUser && q.TaskID == m.TaskID {
		return q, true
	}
	return wireMessage{}, false
}

// reloadUserText rebuilds the question in the "shown" form startSend
// stores, with the `@agent` prefix put back.
//
// The prefix has to be rebuilt because the backend does not persist it:
// runtime.py parses the invocation and stores `invocation.text`, the body
// with the agent token already stripped. This value is never rendered — the
// question renders from its own row, verbatim — and exists for exactly one
// consumer: Retry, which re-sends through splitAgent and needs the same
// string a live entry would have been holding.
func reloadUserText(text, agent string) string {
	if agent == "" {
		return text
	}
	return "@" + agent + " " + text
}

// reloadedEntry is the whole of "reload renders like live": one persisted
// row in, either a liveEntry that F5's own renderers draw, or nil for the
// plain path.
//
// A row earns an entry when either half of what F5 shows is not derivable
// from the message alone:
//
//   - it came from a BACKGROUND agent, so live drew a collapsed slot;
//   - it ended in something other than `done`, so live drew a footer.
//
// Anything else stays nil and renders through render.go and the cache
// exactly as it did before this session. That matters: an ordinary reply
// is the overwhelmingly common row, and it keeps F3's shared render cache
// rather than paying for a per-entry memo it has no use for.
func reloadedEntry(m wireMessage, question wireMessage) *liveEntry {
	if m.Role != roleAssistant {
		return nil
	}
	agent := ""
	if m.Invocation != nil {
		agent = m.Invocation.Agent
	}
	ln := reloadLane(agent)
	status, errCode := reloadStatus(m.Outcome, m.ErrorCode)

	// Nothing to add: an inline turn that finished cleanly IS an ordinary
	// assistant message, and rendering it through the live path would only
	// cost it the shared cache.
	//
	// An empty Outcome lands here too, and deliberately. NULL means the row
	// predates migration v5, which is not the same claim as "done" — but
	// "no footer" is the honest rendering of an ending nobody recorded,
	// and asserting a failure we cannot see would be worse than silence.
	if ln == laneTurn && status == statusDone {
		return nil
	}

	e := &liveEntry{
		kind:     liveReply,
		taskID:   m.TaskID,
		lane:     ln,
		status:   status,
		agentID:  agent,
		userText: reloadUserText(question.Content.Text, agent),
		text:     m.Content.Text,
		errCode:  errCode,
		errText:  messageErrText(m, status, errCode),
	}
	// The slot's clock. finishedAt - startedAt is what elapsed() renders,
	// so the two ends are the question's timestamp and the answer's — send
	// to finish, the span the live clock was measuring. With no paired
	// question the two collapse and the slot reads `done 0:00`, which is
	// the honest answer to "how long did this take" when the send is not
	// recorded, and is what a pre-B12 row gets.
	e.finishedAt = unixTime(m.Timestamp)
	e.startedAt = e.finishedAt
	if start := unixTime(question.Timestamp); !start.IsZero() && start.Before(e.finishedAt) {
		e.startedAt = start
	}
	e.lastEventAt = e.finishedAt
	return e
}

// reloadLane decides slot-or-inline for a reloaded reply.
//
// This is the THIRD reader of input_render.go's knownAgents stopgap (the
// other two are agentTokenState and splitAgent), and the one that most
// needs replacing: the backend's actual rule is `agent.max_steps > 1`,
// which it computes as `is_bounded_loop` and rides out on
// TaskStarted.background — the sole authority on the lane per F5's
// contract. A reload has no TaskStarted to reconcile against, so it has to
// answer the same question from the agent id alone.
//
// Note what this is NOT: "the agent is not the session default". That rule
// is wrong in both directions. `@filler-explainer` names a non-default
// agent at max_steps=1, so live runs it INLINE and reload must too; and a
// session whose settings.agent_id names a background agent runs its plain
// sends in the task lane, which reload must also honour. Only max_steps
// separates them, and only the agent id is on the wire.
func reloadLane(agent string) lane {
	if agent == "" {
		return laneTurn
	}
	return agentLane(agent)
}

// reloadStatus maps B12's persisted Outcome onto F5's liveStatus.
//
// `timed_out` and `bounds_exceeded` both land on statusFailed carrying the
// bounds code, because that pair is exactly what liveFooter already reads
// to say "⚠ stopped at its limit" rather than "✗ failed". B12 split the two
// outcomes apart from one another (via BoundsExceededError's `.bound`) for
// a reader's benefit, but B10 raises one class for both, so error_code is
// `bounds_exceeded` either way and the footer they produce is the same
// one. Forcing the code here means a row whose error_code is NULL still
// gets the right footer from its outcome alone.
//
// An unrecognised outcome falls to statusFailed, matching the backend's own
// _outcome_for: a slug nobody has mapped is a failure whose kind we do not
// know, and "done" is the one answer that is definitely wrong.
func reloadStatus(outcome, errCode string) (liveStatus, string) {
	switch outcome {
	case "", outcomeDone:
		return statusDone, ""
	case outcomeCancelled:
		return statusCancelled, ""
	case outcomeTimedOut, outcomeBoundsExceeded:
		return statusFailed, boundsExceededCode
	case outcomeFailed:
		return statusFailed, errCode
	default:
		return statusFailed, errCode
	}
}

// messageErrText is the footer's text for a persisted row, preferring the
// error the backend actually reported.
//
// StreamAccumulator.finalize_interrupted writes it to `metadata["error"]`
// whenever it was given one, which is the same string TaskFailed's payload
// carried to the live session. Using it means a reloaded failure names the
// provider's real error exactly as the live footer did, rather than a
// generic stand-in — the whole "reload and live render identically" claim,
// applied to the one line that carries the most information.
//
// It falls back to reloadErrText when the row has none. That is not a rare
// path: the tasks-row reconstruction (orphanReply) has no message at all to
// read from, so it always lands there.
func messageErrText(m wireMessage, status liveStatus, errCode string) string {
	if status == statusFailed {
		if s, ok := m.Metadata["error"].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return reloadErrText(status, errCode)
}

// reloadErrText is what the footer says when the row recorded no error
// prose of its own — the tasks row keeps a code and nothing else, and a
// pre-v5 message keeps neither.
//
// It is the same fallback the LIVE path uses when TaskFailed arrives with
// an empty `error` (see handleStreamEvent). Using live's own wording rather
// than inventing a second one is the point: with the same information, the
// two paths must produce the same line.
func reloadErrText(status liveStatus, errCode string) string {
	if status != statusFailed {
		return ""
	}
	if errCode == boundsExceededCode {
		return "it stopped at its configured limit"
	}
	return "the task failed"
}

// ---------------------------------------------------------------------------
// 3 — the tasks join
// ---------------------------------------------------------------------------

// taskRow is one entry of ListTasks' payload, trimmed to what the join
// needs. Field names are the wire's.
type taskRow struct {
	ID        string  `json:"id"`
	State     string  `json:"state"`
	Agent     string  `json:"agent"`
	ErrorCode string  `json:"error_code"`
	CreatedAt float64 `json:"created_at"`
}

// TaskState values, transcribed from backend\runtime\task.py. Held as plain
// strings for the same reason the event names are.
const (
	taskCompleted = "completed"
	taskFailed    = "failed"
	taskCancelled = "cancelled"
)

// requestTaskRows asks for the durable task rows this session's footers may
// need. Fired right after the transcript is on screen rather than before
// it: the transcript is already correct without this, and blocking a whole
// session load on a decoration would be the wrong trade.
func (w *Workspace) requestTaskRows() {
	if w.client == nil || w.sessionID == "" {
		return
	}
	w.send(protocol.CmdListTasks, queryListTasks, map[string]any{
		"scope": "all",
		"limit": taskRowLimit,
	})
}

// onTaskList is the second pass, and it exists for ONE case: a task
// cancelled or failed before its first token persists no assistant row at
// all.
//
// B12 accepted that deliberately and recorded it. StreamAccumulator's
// finalize_interrupted returns None for empty text, because an empty
// AssistantMessage fails B3's validation, and neither relaxing that
// validation nor fabricating placeholder text was worth what it cost. The
// outcome lives on the `tasks` row instead — and it is fully recoverable,
// just not from one row: the user message carries task_id (B12's own
// change), and the tasks row carries status, error_code and agent_id.
// This is that two-table join.
//
// A failed reply reconstructed this way says "✗ failed — the task failed"
// where a live one would have named the provider's actual error, because
// the tasks row keeps a code and no message. That is a real loss of detail
// and an accepted one: it applies only to replies that produced NO text,
// and any task that streamed even one token persisted a real message row
// with its own outcome, which needs none of this.
func (w *Workspace) onTaskList(env protocol.Envelope) {
	var p struct {
		Tasks []taskRow `json:"tasks"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		// Deliberately silent, and NOT w.loadErr. The transcript on
		// screen is already correct; the only thing lost is a footer on
		// a reply that produced nothing. Replacing a loaded session with
		// an error banner over that would be a far larger failure than
		// the one being reported.
		w.log.Warn("chat: ListTasks reply unreadable, footers not rebuilt", "err", err)
		return
	}
	byID := make(map[string]taskRow, len(p.Tasks))
	for _, t := range p.Tasks {
		byID[t.ID] = t
	}
	// F18: KEPT, not consumed. A windowed transcript rebuilds w.all from
	// w.raw on every prepend, append and eviction, and each rebuild has to
	// re-run this join -- otherwise a reloaded footer would survive exactly
	// until the first scroll-fetch and then quietly disappear. Rebuilding
	// here rather than calling applyTaskRows directly is what keeps "w.all
	// is derived from w.raw" true with no second path.
	w.taskRows = byID
	w.rebuildWindow()
}

// applyTaskRows inserts a reply for each question whose task produced no
// message, and gives every paired slot the start time only the tasks row
// knows.
//
// The inserted row is a placeholder, never a fabricated message: empty
// text, no id, and it renders solely through its liveEntry — the same
// entry a live session holds after cancelling before the first token, and
// therefore the same footer. It is emphatically not "attaching the question
// to someone else's answer"; there is no answer, and the entry carries
// none.
//
// # F18: why this does nothing unless the window reaches the tail
//
// `answered` can only be built from rows that are RESIDENT. Under windowing
// that makes "this question's task produced no message" unprovable in the
// general case, because an answer may sit arbitrarily far below its question
// (two overlapping sends both persist their questions before either answer
// lands — pairReloaded's own doc records this as the defect that episode
// existed to fix) and so may be outside the window entirely.
//
// A task that failed AFTER streaming some tokens has a real assistant row.
// With that row evicted, an ungated join would see an unanswered question, a
// failed tasks row, and insert a placeholder "✗ failed" reply — a FABRICATED
// row in a window whose whole contract is that it is a faithful slice. That
// is strictly worse than a missing footer.
//
// So the join is gated on the window reaching the open-time tail, where an
// absent answer really is absent. Everywhere else a question at or near a
// window edge renders plain and unpaired, and re-resolves normally once the
// adjoining batch loads. Deciding it any other way would need either an
// overlap fetch past each edge or a guess, and a guess here invents rows.
func (w *Workspace) applyTaskRows(byID map[string]taskRow) {
	if len(byID) == 0 {
		return
	}
	if !w.window.reachesTail() {
		// The decorations that come off a message's OWN columns (a slot,
		// a footer) are unaffected — decorateReloaded has already run and
		// needs nothing from here. Only the two-table join for a task
		// with no message at all is suppressed.
		return
	}

	// Which tasks already have a message. A question whose task IS here
	// needs nothing: its outcome is on its own assistant row.
	answered := make(map[string]bool, len(w.all))
	for _, m := range w.all {
		if m.Role == roleAssistant && m.TaskID != "" {
			answered[m.TaskID] = true
		}
	}

	msgs := make([]wireMessage, 0, len(w.all))
	hist := make([]*liveEntry, 0, len(w.all))
	for i, m := range w.all {
		msgs = append(msgs, m)
		hist = append(hist, w.histAt(i))

		if m.Role != roleUser || m.TaskID == "" || answered[m.TaskID] {
			continue
		}
		t, ok := byID[m.TaskID]
		if !ok {
			continue
		}
		if orphan := orphanReply(m, t); orphan != nil {
			msgs = append(msgs, wireMessage{Role: roleAssistant, TaskID: m.TaskID})
			hist = append(hist, orphan)
		}
	}
	w.all, w.hist = msgs, hist
}

// orphanReply builds the entry for a question whose task left no message,
// or nil when the task's state says nothing worth showing.
//
// nil for a task still PENDING, QUEUED or RUNNING — the question renders
// with no reply, which is the truth. nil for a COMPLETED task too: a task
// that finished cleanly and wrote nothing has no ending to report, and
// "✓ done" over an empty reply would assert an answer that does not exist.
//
// # Why the non-terminal decline, precisely
//
// This used to say "a non-terminal entry in w.hist would keep Streaming()
// true and the watchdog re-arming forever". **That reason is wrong and the
// conclusion is right**, which is the worst combination to leave in place —
// found while investigating whether F12 could add a static marker here, and
// corrected rather than deleted because the real reasons are what decide
// whether anyone may lift this later.
//
// Streaming() and tickInterval (stream.go) scan `w.live` ONLY. A *liveEntry
// in `w.hist` has never been able to keep either alive. The reasoning was
// transplanted from newUserEntry, where it genuinely does apply because
// that entry really is in `w.live`.
//
// The two real reasons:
//
//  1. **The renderer.** renderTaskEntry's `statusSending, statusRunning`
//     branch draws `⟳ Research 0:42` from `time.Since(e.startedAt)` — a
//     clock. In `w.hist` no tick is armed (nothing re-arms for a hist
//     entry), so it would render a live-looking clock frozen at whatever
//     the first frame computed, and `e.status.terminal()` being false also
//     withholds the `▸ o` affordance.
//  2. **Nothing would ever correct it.** requestTaskRows fires exactly
//     once, from setSession, and entryFor resolves task envelopes against
//     `w.live` only — so a reloaded task's own TaskDone is dropped before
//     anything sees it. An entry placed here from a RUNNING tasks row
//     would keep saying "running" for the life of the session, however
//     long ago the task actually ended.
//
// Reason 2 is also why F12 did not add the obvious static alternative
// ("→ running elsewhere", no clock, sourced only from the status column):
// static or not, it has no correction path, and a permanent lie is not an
// improvement on a missing footer. See tasks/frontend/INDEX.md's parking
// lot for what closing it would actually take.
func orphanReply(q wireMessage, t taskRow) *liveEntry {
	var status liveStatus
	var errCode string
	switch t.State {
	case taskFailed:
		status = statusFailed
		errCode = t.ErrorCode
	case taskCancelled:
		status = statusCancelled
	default:
		return nil
	}

	start := unixTime(t.CreatedAt)
	if t.CreatedAt <= 0 {
		start = unixTime(q.Timestamp)
	}
	return &liveEntry{
		kind:        liveReply,
		taskID:      t.ID,
		lane:        reloadLane(t.Agent),
		status:      status,
		agentID:     t.Agent,
		userText:    reloadUserText(q.Content.Text, t.Agent),
		errCode:     errCode,
		errText:     reloadErrText(status, errCode),
		startedAt:   start,
		finishedAt:  start,
		lastEventAt: start,
	}
}

// unixTime converts a wire timestamp (fractional seconds since the epoch)
// to a time.Time. Zero in, zero out: a missing timestamp must not become
// 1970, which an elapsed() call would render as a 55-year task.
func unixTime(ts float64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	sec := int64(ts)
	return time.Unix(sec, int64((ts-float64(sec))*1e9))
}
