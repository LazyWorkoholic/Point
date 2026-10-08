// F18: the bounded, contiguous resident window.
//
// Before this file, opening a session fetched ALL of it: onSessionList sent
// GetSession with no message_limit, setSession dropped every returned row
// into w.all, and nothing ever released one. A 50,000-message session cost
// 50,000 messages of heap for as long as it stayed open, on a device that is
// very likely also holding 2-4 GB of local model. History was the one
// subsystem opting out of the rule the rest of the app already commits to --
// memory is bounded and reclaimable, and re-deriving state from SQLite is
// cheap.
//
// # The model, in one paragraph
//
// w.raw is a CONTIGUOUS run of persisted rows, oldest-first, exactly as B18's
// keyset query returned them: [oldestSeq, newestSeq], never a set of ranges
// with gaps. w.all and w.hist are DERIVED from it on every mutation
// (rebuildWindow -> pairReloaded -> decorateReloaded -> applyTaskRows), so
// F3's renderer's input shape is unchanged -- it still renders "a window",
// just of a smaller, managed set. Scroll past an edge, fetch the adjoining
// batch and prepend/append; grow past the bound, evict from the far edge and
// let the GC reclaim it.
//
// Contiguity rather than a cache-with-holes is not a simplification made
// reluctantly. A hole-y cache makes eviction, anchor arithmetic and the
// renderer's input all harder, and buys nothing at this scale: re-fetching a
// dropped batch is a local SQLite keyset seek against an index B18 built for
// exactly this, almost certainly already in the OS file cache. Re-fetching is
// CHEAPER THAN HOLDING, which is what makes aggressive eviction correct here
// and would make it insane in a client/server app.
//
// # Why the scroll anchor needs no height arithmetic
//
// F18's own task doc calls scroll-anchor preservation on prepend "the bug most
// likely to ship broken": when N older messages arrive above the viewport, the
// view must shift by their RENDERED HEIGHT, not by message count, or content
// jumps under the user.
//
// It ships correct here for a structural reason, not a careful one. F3 already
// stores the scroll position as the PAIR (topAbs, topRow) -- an entry and a
// row offset inside that entry's block -- precisely because an absolute row
// number would have to be recomputed against every entry's height on every
// render (scroll.go's own doc comment). Inserting rows above an (entry,
// offset) address changes the entry's INDEX and nothing else. So the whole of
// anchor preservation is: remember WHICH entry the index pointed at, mutate,
// then look that entry up again (captureAnchor/restoreAnchor below). No height
// is ever measured, which is also why there is no height for a measurement
// bug to be wrong about.
//
// # What this file does NOT do
//
//   - It adds no backend surface. B18 gave us SessionMessages and
//     GetSession(message_limit); those are the only two calls here.
//   - It never pages on anything but `seq`. Not a row index, not created_at.
//   - It never background-fetches the whole session "to make scrolling
//     instant". That is the exact anti-pattern the episode exists to remove.
package chat

import (
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// histDir is which way a fetch reaches, and it doubles as "is one in flight".
// histNone is the zero value and means idle.
type histDir int

const (
	histNone histDir = iota
	// histBefore: older rows, prepended at the head. Fired when the
	// viewport reaches the top of the resident window.
	histBefore
	// histAfter: newer rows, appended at the tail. Only reachable AFTER a
	// prior eviction from the tail -- with the window at the tail of
	// history there is nothing newer to ask for.
	histAfter
	// histTail: replace the window with the newest batch. G, when the tail
	// is no longer resident.
	histTail
	// histAnchored: replace the window with one STARTING at a stored
	// anchor seq. Resume's path, and the only fetch that is not driven by
	// the scroll position.
	histAnchored
)

// historyWindow is every fact about the resident slice that is not the slice
// itself. Kept in one struct rather than as eight fields on Workspace so that
// "what does the window currently know" has one answer to read.
type historyWindow struct {
	// oldestSeq/newestSeq bound the resident run. Both zero when w.raw is
	// empty, which is a real state (a brand-new session) and not an error.
	oldestSeq int
	newestSeq int

	// ceilingSeq is the newest seq that existed when this session was
	// OPENED, and it is the whole of F18's live-send dedupe.
	//
	// w.live holds every send made this session -- the question and the
	// reply, as entries, appended by stream.go's startSend. The backend
	// ALSO persists both, with real seqs above this ceiling. They never
	// collided before F18 because w.all was a snapshot taken at open and
	// nothing re-fetched. The moment a re-fetch exists, an `after=` fetch
	// would hand back rows w.live is already rendering and the transcript
	// would show every live send twice.
	//
	// So: nothing above the ceiling is ever ingested into w.raw. w.live
	// owns everything past it, for this session's lifetime. That also
	// makes "does the window reach the tail" a comparison against a fixed
	// number instead of a wire flag that a send invalidates -- see
	// reachesTail.
	//
	// The cost, stated rather than hidden: rows another client appends to
	// this session while it is open are not picked up until the next
	// session load. That was already true before F18 (nothing re-fetched
	// after a send at all), so it is not a regression this introduced.
	ceilingSeq int

	// totalCount is B18's total_message_count, from the bounded GetSession
	// at open. Zero means the backend did not send one.
	totalCount int

	// moreBefore is "does history exist older than oldestSeq". It comes
	// from the wire's has_more_before, which B18 defines as "beyond THIS
	// slice", not "beyond this batch" -- so it is the honest signal for
	// when to stop asking. It is also forced true by an eviction from the
	// head, since that drops rows we know exist.
	//
	// There is deliberately no moreAfter twin: reachesTail answers that
	// from the ceiling, which cannot go stale the way a flag can.
	moreBefore bool

	// inFlight is the one outstanding fetch, and fetchID is its correlation
	// id. One at a time, by design: two concurrent fetches on the same
	// window would each prepend against the other's idea of oldestSeq.
	// A reply whose corr id is not fetchID is a superseded fetch and is
	// dropped -- which is how a G mid-scroll cancels a scroll-fetch
	// without a cancel call.
	inFlight histDir
	fetchID  string

	// deferred is F18's backpressure: the direction a fetch WOULD have
	// gone, parked because a task is streaming. A local model is CPU-bound
	// and stealing cycles to hydrate scrollback the user is not looking at
	// degrades tokens/sec on the thing they are. Replayed by
	// flushDeferredHistory when the stream settles.
	deferred histDir

	// anchorTarget/anchorRow carry a Resume anchor across its own fetch:
	// the seq to land on and the row offset within it. See
	// applyResumeAnchor.
	anchorTarget int
	anchorRow    int
}

// reachesTail reports whether the resident window's newest row is the newest
// row this session had at open -- i.e. whether the bottom of the transcript
// is the real bottom.
//
// Asked against the ceiling rather than a has_more_after flag on purpose: the
// first send of the session persists rows past the ceiling, so the wire's flag
// would say "more exists" forever afterwards and every honest-bottom test in
// this file would start lying. The ceiling is fixed at open and cannot.
func (h *historyWindow) reachesTail() bool {
	return h.ceilingSeq == 0 || h.newestSeq >= h.ceilingSeq
}

// ---------------------------------------------------------------------------
// Sizing
// ---------------------------------------------------------------------------

// defaultHistoryBatch matches config.Defaults().UI.HistoryBatch, repeated
// here for the same reason defaultStreamTimeout is repeated in stream.go: a
// Workspace built without an Options value must still have a working batch
// size rather than silently never paging, and this package cannot import the
// config package's defaults without inverting the dependency main.go already
// establishes (config -> Options -> chat).
//
// Conservative on purpose. The batch is the unit of work one scroll step can
// cost, and the machine this episode is written for is the one with a local
// model already holding the CPU.
const defaultHistoryBatch = 100

// historyBatch is how many messages one fetch pulls -- `ui.historyBatch`.
func (w *Workspace) historyBatch() int {
	if w.historyBatchSize > 0 {
		return w.historyBatchSize
	}
	return defaultHistoryBatch
}

// historyBound is the resident window's message-count bound.
//
// `ui.scrollback` names it, which is the first time that key has governed
// anything but the render cache -- see config.go's own note on the comment
// that described this behaviour long before any code had it.
//
// Two floors, both for correctness rather than taste:
//
//   - A FEW SCREENFULS. A message occupies at least one row, so a terminal
//     tall enough to show more messages than the window may hold could never
//     fill its own viewport, and every render would sit at both edges at
//     once. Three screenfuls leaves a screenful of slack above and below.
//   - TWICE THE BATCH. With a bound below one batch, a prepend would
//     immediately evict most of what it just fetched and the next scroll step
//     would re-fetch it: a thrash loop that reads as "scrolling is slow" and
//     is really "the config is impossible".
func (w *Workspace) historyBound() int {
	bound := w.scrollback
	if floor := 3 * w.viewRows; bound < floor {
		bound = floor
	}
	if floor := 2 * w.historyBatch(); bound < floor {
		bound = floor
	}
	if bound < 1 {
		bound = 1
	}
	return bound
}

// ---------------------------------------------------------------------------
// Seeding and rebuilding
// ---------------------------------------------------------------------------

// seedWindow installs the tail that the bounded GetSession returned. Called
// from setSession, which has already reset every other session-shaped field.
//
// has_more_before is not on a GetSession reply (it is SessionMessages' own
// field), so it is DERIVED here from the two numbers that are: a tail shorter
// than the total means older rows exist. A backend that sent no
// total_message_count leaves it false, which is the safe direction -- the app
// renders exactly what it was given and simply never asks for more, rather
// than paging against a count it does not have.
func (w *Workspace) seedWindow(s wireSession) {
	w.raw = s.Messages
	w.window = historyWindow{totalCount: s.TotalMessageCount}
	if n := len(w.raw); n > 0 {
		w.window.oldestSeq = w.raw[0].Seq
		w.window.newestSeq = w.raw[n-1].Seq
	}
	w.window.ceilingSeq = w.window.newestSeq
	w.window.moreBefore = w.window.totalCount > len(w.raw)
	w.taskRows = nil
	w.rebuildWindow()
}

// rebuildWindow derives w.all and w.hist from w.raw.
//
// It is the single place S11b's three reload transforms run, and it runs on
// EVERY window mutation rather than once per session load. That is the whole
// adaptation windowing required of them: they were written against "the
// session", and they now run against "the resident slice". Both are a
// contiguous run of rows in database order, which is the only property any of
// them actually depended on -- see pairReloaded's doc, where every position-,
// adjacency- and time-based rule was already ruled out permanently in favour
// of the persisted task_id.
//
// Idempotent, and it has to be: applyTaskRows INSERTS placeholder rows, so
// running it against its own output would compound. Rebuilding from w.raw
// every time means it always runs against clean input.
func (w *Workspace) rebuildWindow() {
	ui := w.captureSlotUI()
	w.all = pairReloaded(w.raw)
	w.decorateReloaded()
	w.applyTaskRows(w.taskRows)
	w.restoreSlotUI(ui)
}

// slotUI is the part of a decorated row's state that belongs to the USER
// rather than to the row: whether a task slot is open (`o`) and whether a
// thinking block is expanded (`t`).
//
// It has to survive a rebuild, and before this it did not. decorateReloaded
// allocates FRESH liveEntry values from the message columns every time, and
// rebuildWindow now runs on every prepend, append and eviction rather than
// once per session load -- so a slot the user had just opened silently
// snapped shut the moment a scroll-fetch landed. Found during F18's own
// verification; see the completed doc's bug 4.
//
// Only these two fields are carried. Everything else on the entry -- status,
// text, the clock, the error -- is derived from the row and must be
// re-derived, or a rebuild would preserve a stale rendering of a row that
// has since changed.
type slotUI struct {
	slotOpen bool
	expanded bool
}

// slotUIKey identifies a decorated row across a rebuild.
//
// The message id first, because it is the one truly unique handle. The
// (task_id, role) pair second, because applyTaskRows' placeholder reply has
// NO id and no seq -- it has no database row at all -- and task_id plus role
// is unique within a window anyway: pairReloaded guarantees at most one
// assistant row per task (runtime.py's terminal handler appends exactly one,
// or none).
func slotUIKey(m wireMessage) string {
	if m.ID != "" {
		return "id:" + m.ID
	}
	if m.TaskID != "" {
		return "task:" + m.Role + ":" + m.TaskID
	}
	return ""
}

func (w *Workspace) captureSlotUI() map[string]slotUI {
	var out map[string]slotUI
	for i, m := range w.all {
		e := w.histAt(i)
		if e == nil || (!e.slotOpen && !e.expanded) {
			continue
		}
		key := slotUIKey(m)
		if key == "" {
			continue
		}
		if out == nil {
			out = make(map[string]slotUI, 4)
		}
		out[key] = slotUI{slotOpen: e.slotOpen, expanded: e.expanded}
	}
	return out
}

func (w *Workspace) restoreSlotUI(ui map[string]slotUI) {
	if len(ui) == 0 {
		return
	}
	for i, m := range w.all {
		e := w.histAt(i)
		if e == nil {
			continue
		}
		st, ok := ui[slotUIKey(m)]
		if !ok {
			continue
		}
		e.slotOpen, e.expanded = st.slotOpen, st.expanded
	}
}

// ---------------------------------------------------------------------------
// The scroll anchor
// ---------------------------------------------------------------------------

// anchorID identifies an entry by WHAT IT IS rather than where it sits, so a
// position survives an insertion or an eviction above it. See this file's
// header for why that is the entirety of anchor preservation.
type anchorID struct {
	ok bool
	// inLive: the entry is one of this session's own sends. w.live is
	// append-only and never evicted, so its index is already stable --
	// nothing has to be matched.
	inLive  bool
	liveIdx int
	// msgID/seq identify a persisted row. Both are carried because either
	// can be absent: a pre-B18 row has no seq, and the placeholder reply
	// applyTaskRows inserts has neither (it has no database row at all).
	msgID string
	seq   int
	// taskID/role are what address that placeholder, and they are why a
	// FOCUS is never resolved by the walk-up below.
	//
	// The walk-up is right for a scroll position: any nearby row is a fine
	// place to resume composing a frame from. It is WRONG for a focus,
	// which names the entry every transcript action acts on -- `o` on a
	// cancelled research task resolved to the question above its own
	// placeholder slot, where liveAt returns nil and `o` is a silent
	// no-op. Found during F18's own verification; see the completed doc's
	// bug 5.
	taskID string
	role   string
	row    int
}

// captureAnchor records a position as an identity.
//
// A position pointing at applyTaskRows' placeholder row cannot be addressed
// (no id, no seq), so it walks UP to the nearest row that can be and resets
// the row offset to 0. That is a shift of a few rows in the one case where a
// task produced no message at all, and the alternative -- addressing a row
// that is rebuilt from scratch on every mutation -- is a position that
// silently resolves to the wrong entry.
func (w *Workspace) captureAnchor(abs, row int) anchorID {
	if abs < 0 || abs >= w.entryCount() {
		return anchorID{}
	}
	kind, idx := w.entryAt(abs)
	switch kind {
	case entryLive:
		return anchorID{ok: true, inLive: true, liveIdx: idx, row: row}
	case entryPending:
		return anchorID{}
	}
	for i := idx; i >= 0; i-- {
		m := w.all[i]
		if m.ID == "" && m.Seq == 0 && m.TaskID == "" {
			// Genuinely unaddressable. Walk up, and reset the offset so the
			// position lands at the head of whatever row answers instead.
			row = 0
			continue
		}
		return anchorID{
			ok: true, msgID: m.ID, seq: m.Seq,
			taskID: m.TaskID, role: m.Role, row: row,
		}
	}
	return anchorID{}
}

// resolveAnchor finds an identity's absolute index in the rebuilt transcript,
// or reports that it is gone (evicted, or never there).
func (w *Workspace) resolveAnchor(a anchorID) (int, bool) {
	if !a.ok {
		return 0, false
	}
	if a.inLive {
		if a.liveIdx < 0 || a.liveIdx >= len(w.live) {
			return 0, false
		}
		return len(w.all) + a.liveIdx, true
	}
	// Most specific handle first. A row with an id is matched by it alone;
	// only a row that HAS no id falls through to the pair that addresses
	// applyTaskRows' placeholder, and only a row with neither to the seq.
	for i, m := range w.all {
		switch {
		case a.msgID != "":
			if m.ID == a.msgID {
				return i, true
			}
		case a.taskID != "":
			if m.TaskID == a.taskID && m.Role == a.role {
				return i, true
			}
		case a.seq != 0:
			if m.Seq == a.seq {
				return i, true
			}
		}
	}
	return 0, false
}

// restoreAnchor puts the scroll position back on the entry it was on.
//
// A missing anchor degrades to the bottom rather than to index 0: the anchor
// can only go missing by being evicted, and this file only ever evicts the
// edge the user is scrolling AWAY from, so "it is gone" and "you are no
// longer looking at it" are the same event.
func (w *Workspace) restoreAnchor(a anchorID) {
	abs, ok := w.resolveAnchor(a)
	if !ok {
		w.pinned = true
		return
	}
	w.topAbs, w.topRow = abs, a.row
	w.clampScroll(w.viewWidth)
}

// restoreFocus re-resolves the entry focus the same way, and drops it to "no
// explicit focus" if its entry is gone. w.focus is an absolute index, so
// leaving it alone across a prepend would silently move the focus to whatever
// row happened to inherit the number.
func (w *Workspace) restoreFocus(a anchorID) {
	if !a.ok {
		return
	}
	abs, ok := w.resolveAnchor(a)
	if !ok {
		w.focus = -1
		return
	}
	w.focus = abs
}

// ---------------------------------------------------------------------------
// Fetching
// ---------------------------------------------------------------------------

// requestHistory issues one fetch, or parks it.
//
// Two gates, in this order, and the order matters:
//
//  1. BACKPRESSURE. While anything is streaming, the fetch is parked rather
//     than sent. This is the low-end-device rule and it is close to the
//     reason the episode exists at all: the CPU a local model is saturating
//     is the same CPU that would hydrate scrollback nobody is looking at.
//  2. ONE AT A TIME. A second concurrent fetch would compute its cursor
//     against a window the first is about to change.
//
// A fetch that cannot be sent is simply not sent. Nothing retries on a timer:
// the next scroll step re-checks the same edge, and the edge check is
// idempotent, so a dropped request costs one keystroke and never leaves a
// stuck state to recover from.
func (w *Workspace) requestHistory(dir histDir, cursor int) {
	if dir == histNone || w.sessionID == "" {
		return
	}
	// Whether a wire exists at all is sendHistoryFn's to answer, not this
	// function's -- doSendHistory checks w.client and a test's substitute
	// has no client to check. Keeping the transport concern on the
	// substitutable side is what lets this decision logic be tested
	// without a live protocol.Client, the same split sendFn/sendMsgFn
	// already use.
	if w.Streaming() {
		w.window.deferred = dir
		return
	}
	if w.window.inFlight != histNone {
		return
	}

	payload := map[string]any{
		"session_id": w.sessionID,
		"limit":      w.historyBatch(),
	}
	switch dir {
	case histBefore:
		payload["before"] = cursor
	case histAfter, histAnchored:
		payload["after"] = cursor
	case histTail:
		// The newest batch AT OR BELOW the ceiling. Not a bare cursorless
		// tail: that would return this session's own live sends, which
		// w.live is already rendering -- see historyWindow.ceilingSeq.
		//
		// `before` is exclusive (B18's query plan shows `seq<?`), so
		// ceiling+1 is "everything up to and including the ceiling".
		payload["before"] = w.window.ceilingSeq + 1
	}

	id, ok := w.sendHistoryFn(payload)
	if !ok {
		return
	}
	w.window.inFlight = dir
	w.window.fetchID = id
	w.window.deferred = histNone
}

// doSendHistory is sendHistoryFn's default. It exists beside doSend rather
// than reusing it for one reason: it RETURNS the correlation id, which a
// history fetch needs and no other query in this package does. A reply has to
// be matched against the window state it was issued against, because a
// superseded fetch (a G pressed mid-scroll) must be dropped rather than
// prepended against a window that has since moved.
func (w *Workspace) doSendHistory(payload map[string]any) (string, bool) {
	if w.client == nil {
		return "", false
	}
	id := localID()
	env, err := newQueryEnvelope(id, protocol.CmdSessionMessages, payload)
	if err != nil {
		w.log.Warn("chat: history fetch not encodable", "err", err)
		return "", false
	}
	if _, err := w.client.Request(env, 0); err != nil {
		// NOT w.loadErr. The resident window already on screen is
		// correct; a fetch that never left costs the user one more
		// keystroke at the same edge. Blanking a correct transcript over
		// it would be the larger failure -- the same posture every
		// decorative query in protocol.go already takes.
		w.log.Warn("chat: history fetch not sent", "err", err)
		return "", false
	}
	w.pending[id] = querySessionMessages
	return id, true
}

// flushDeferredHistory replays a fetch that backpressure parked. Called from
// the one place a stream can end (handleStreamEvent's single exit), and a
// no-op unless something is actually parked and everything has settled.
func (w *Workspace) flushDeferredHistory() {
	dir := w.window.deferred
	if dir == histNone || w.Streaming() {
		return
	}
	w.window.deferred = histNone
	switch dir {
	case histBefore:
		w.requestHistory(histBefore, w.window.oldestSeq)
	case histAfter:
		w.requestHistory(histAfter, w.window.newestSeq)
	case histTail:
		w.requestHistory(histTail, 0)
	case histAnchored:
		w.requestHistory(histAnchored, w.window.anchorTarget-1)
	}
}

// onSessionMessages applies one SessionMessageList reply.
func (w *Workspace) onSessionMessages(env protocol.Envelope) {
	if env.CorrID == "" || env.CorrID != w.window.fetchID {
		// A superseded fetch, or the backend's heartbeat arriving with a
		// null corr_id. F8's Bug A was exactly this: a cleared
		// correlation-id field is a VALID switch case, and the 5-second
		// idle heartbeat then matched it forever. Guarded here for the
		// same reason handlePickerEnvelope and handleTaskListEnvelope
		// both guard it.
		return
	}
	dir := w.window.inFlight
	w.window.inFlight = histNone
	w.window.fetchID = ""

	var p struct {
		Messages      []wireMessage `json:"messages"`
		HasMoreBefore bool          `json:"has_more_before"`
		HasMoreAfter  bool          `json:"has_more_after"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("chat: SessionMessageList unreadable, window unchanged", "err", err)
		return
	}

	// Decision 4, enforced at the one point rows enter the window: nothing
	// above the ceiling, ever. w.live owns that range.
	msgs := make([]wireMessage, 0, len(p.Messages))
	for _, m := range p.Messages {
		if w.window.ceilingSeq > 0 && m.Seq > w.window.ceilingSeq {
			continue
		}
		msgs = append(msgs, m)
	}

	anchor := w.captureAnchor(w.topAbs, w.topRow)
	// Only an EXPLICIT focus is captured. focusedIndex() falls back to the
	// newest entry when w.focus is -1, so capturing through it would hand
	// restoreFocus a real identity and turn "nothing is focused" into
	// "the newest entry is focused" on the first scroll-fetch -- a focus
	// ring appearing from a background fetch, and a row protected from
	// eviction, both out of nowhere.
	var focus anchorID
	if w.focus >= 0 {
		focus = w.captureAnchor(w.focus, 0)
	}
	wasPinned := w.pinned

	switch dir {
	case histBefore:
		w.prependRows(msgs, p.HasMoreBefore)
	case histAfter:
		w.appendRows(msgs)
	case histTail:
		w.replaceRows(msgs, p.HasMoreBefore)
		w.pinned = true
		w.focus = -1
		return
	case histAnchored:
		w.replaceRows(msgs, p.HasMoreBefore)
		w.landOnAnchorTarget()
		return
	default:
		return
	}

	// The FOCUS is re-resolved either way, and that is deliberate. `pinned`
	// is a statement about the SCROLL POSITION tracking the live edge; it
	// says nothing about the focus, and moveFocus can set it while an
	// explicit focus exists (focusing the newest entry pins). Leaving a
	// stale absolute index behind points the focus at whichever row
	// inherited the number -- a third spelling of the same defect the slot
	// state and the placeholder walk-up both had.
	w.restoreFocus(focus)
	if wasPinned {
		// A prepend or append does not move the live edge, so a pinned
		// position needs no restoring -- and restoring it would unpin.
		return
	}
	w.restoreAnchor(anchor)
}

// prependRows puts older rows at the head.
func (w *Workspace) prependRows(msgs []wireMessage, moreBefore bool) {
	if len(msgs) == 0 {
		// An empty batch at a cursor that claimed more existed is the end
		// of history, honestly reported. Recording it stops the edge check
		// asking again on every single keystroke.
		w.window.moreBefore = false
		return
	}
	if last := msgs[len(msgs)-1].Seq; w.window.oldestSeq != 0 && last >= w.window.oldestSeq {
		// Not adjacent below the head. A contiguous window must never
		// splice in a slice that overlaps it or floats free of it, so this
		// is dropped rather than merged -- the invariant is worth more
		// than the batch.
		w.log.Warn("chat: history batch not adjacent, dropped",
			"batch_newest", last, "window_oldest", w.window.oldestSeq)
		return
	}
	w.raw = append(msgs, w.raw...)
	w.window.oldestSeq = w.raw[0].Seq
	if w.window.newestSeq == 0 {
		w.window.newestSeq = w.raw[len(w.raw)-1].Seq
	}
	w.window.moreBefore = moreBefore
	// Scrolling UP, so the far edge is the newest end.
	w.evictWindow(histBefore)
	w.rebuildWindow()
}

// appendRows puts newer rows at the tail.
func (w *Workspace) appendRows(msgs []wireMessage) {
	if len(msgs) == 0 {
		return
	}
	if first := msgs[0].Seq; w.window.newestSeq != 0 && first <= w.window.newestSeq {
		w.log.Warn("chat: history batch not adjacent, dropped",
			"batch_oldest", first, "window_newest", w.window.newestSeq)
		return
	}
	w.raw = append(w.raw, msgs...)
	w.window.newestSeq = w.raw[len(w.raw)-1].Seq
	if w.window.oldestSeq == 0 {
		w.window.oldestSeq = w.raw[0].Seq
	}
	// Scrolling DOWN, so the far edge is the oldest end.
	w.evictWindow(histAfter)
	w.rebuildWindow()
}

// replaceRows swaps the whole window -- G's tail fetch and Resume's anchored
// fetch, the two that do not extend an existing run.
func (w *Workspace) replaceRows(msgs []wireMessage, moreBefore bool) {
	w.forgetRendered(w.raw)
	w.raw = msgs
	w.window.oldestSeq, w.window.newestSeq = 0, 0
	if n := len(w.raw); n > 0 {
		w.window.oldestSeq = w.raw[0].Seq
		w.window.newestSeq = w.raw[n-1].Seq
	}
	w.window.moreBefore = moreBefore
	w.rebuildWindow()
}

// landOnAnchorTarget positions the viewport on a Resume anchor, degrading to
// the bottom when the seq is not in the window that came back.
//
// Degrade, never fail: the restore path can never be the thing that breaks a
// resume. A session whose content shifted (or, later, was archived) must still
// open.
func (w *Workspace) landOnAnchorTarget() {
	target, row := w.window.anchorTarget, w.window.anchorRow
	w.window.anchorTarget, w.window.anchorRow = 0, 0
	for i, m := range w.all {
		if m.Seq != target {
			continue
		}
		w.pinned = false
		w.topAbs, w.topRow = i, row
		w.clampScroll(w.viewWidth)
		return
	}
	w.log.Info("chat: resume anchor not found, degrading to the bottom", "seq", target)
	w.pinned = true
	w.focus = -1
}

// ---------------------------------------------------------------------------
// Eviction
// ---------------------------------------------------------------------------

// evictWindow trims w.raw back to its bound by dropping from the FAR edge --
// the end away from the direction the user is scrolling.
//
// Two rows are never dropped: the one the scroll position is anchored to and
// the one the entry focus is on. Everything the transcript's actions act on
// (y, o, t, ^R) resolves through the focused entry, and the anchor is where
// the next frame is composed from; evicting either turns a correct position
// into a guess. Honouring them can leave the window temporarily OVER its
// bound, which is the right trade -- the bound exists to keep memory flat
// over a long scroll, and one batch of slack while a single row sits under
// the user's cursor does not threaten that.
func (w *Workspace) evictWindow(dir histDir) {
	bound := w.historyBound()
	if len(w.raw) <= bound {
		return
	}
	// The hard ceiling on that slack. Protection has to be capped, and the
	// test that proved it is worth keeping in mind: with an uncapped
	// protection, focusing the newest message and then scrolling to the
	// top repeatedly grew the window by one batch PER FETCH, forever --
	// 50 rows, 60, 70, against a bound of 40. "Never evict the focused
	// row" and "memory is flat regardless of scroll depth" cannot both
	// hold unconditionally, and Done-when #2 is the one that is a hard
	// stop. So past a batch of slack the protection is dropped and the
	// position degrades (restoreAnchor pins to the bottom, restoreFocus
	// clears to -1, both honest).
	//
	// Unreachable by scrolling, which is why the trade costs nothing in
	// practice: a prepend fires only from the TOP of the window and an
	// append only from its bottom (maybeFetchAtEdge), so the anchor is
	// always at the NEAR edge when a batch lands and is never the far-edge
	// blocker. Only a focus left behind by j/k can be, and losing a focus
	// you have scrolled a full window and a batch away from is the right
	// answer anyway.
	ceiling := bound + w.historyBatch()
	protect := w.protectedSeqs()

	switch dir {
	case histBefore:
		cut := bound
		for i := len(w.raw) - 1; i >= 0; i-- {
			if protect[w.raw[i].Seq] {
				if i+1 > cut {
					cut = i + 1
				}
				break
			}
		}
		if cut > ceiling {
			cut = ceiling
		}
		if cut >= len(w.raw) {
			return
		}
		w.forgetRendered(w.raw[cut:])
		w.raw = w.raw[:cut]
		w.window.newestSeq = w.raw[len(w.raw)-1].Seq
		// Nothing has to be recorded about what was just dropped:
		// reachesTail now reads false on its own, because newestSeq fell
		// below the ceiling. One fact, one reader.

	case histAfter:
		drop := len(w.raw) - bound
		for i, m := range w.raw {
			if protect[m.Seq] {
				if i < drop {
					drop = i
				}
				break
			}
		}
		if floor := len(w.raw) - ceiling; drop < floor {
			drop = floor
		}
		if drop <= 0 {
			return
		}
		w.forgetRendered(w.raw[:drop])
		w.raw = w.raw[drop:]
		w.window.oldestSeq = w.raw[0].Seq
		// Dropping from the head drops rows we KNOW exist, so older
		// history exists whatever the last reply's flag said.
		w.window.moreBefore = true
	}
}

// protectedSeqs is the seq of the anchored row and of the EXPLICITLY focused
// row.
//
// Keyed on seq rather than on index because the index is about to change, and
// on seq rather than on id because that is what w.raw is ordered by. Seq 0
// (applyTaskRows' placeholder, which has no database row) is never a real
// key, so a zero entry protects nothing -- correct, since a placeholder is
// rebuilt from the question beside it rather than evicted.
//
// It reads w.focus DIRECTLY rather than focusedIndex(), and that distinction
// is the whole correctness of eviction. focusedIndex() falls back to the
// NEWEST entry when nothing is explicitly focused (F3's convention, so `y`
// works without pressing a focus key first) -- which is not a focus at all,
// it is a default. Protecting it pins the newest resident row permanently,
// so the far-edge cut can never reach past it and the window grows by one
// batch per scroll-fetch forever. Caught by test: a bound of 40 held 50 rows
// after one prepend, 80 after four, climbing with scroll depth on a session
// of any size -- exactly the failure Done-when #2 calls a hard stop.
func (w *Workspace) protectedSeqs() map[int]bool {
	out := make(map[int]bool, 2)
	add := func(abs int) {
		if abs < 0 || abs >= len(w.all) {
			return
		}
		if s := w.all[abs].Seq; s != 0 {
			out[s] = true
		}
	}
	add(w.topAbs)
	if w.focus >= 0 {
		add(w.focus)
	}
	return out
}

// forgetRendered drops evicted rows from F3's render cache.
//
// cache.go already bounds itself, so this is not what makes memory flat -- it
// is what keeps the cache's contents MEANINGFUL. Without it the cache spends
// its whole budget on rendered lines for messages no longer in the window,
// evicting live ones in FIFO order to hold them.
func (w *Workspace) forgetRendered(dropped []wireMessage) {
	if len(dropped) == 0 || len(w.cache) == 0 {
		return
	}
	gone := make(map[string]bool, len(dropped))
	for _, m := range dropped {
		if m.ID == "" {
			continue
		}
		if _, ok := w.cache[m.ID]; !ok {
			continue
		}
		delete(w.cache, m.ID)
		gone[m.ID] = true
	}
	if len(gone) == 0 {
		return
	}
	kept := w.cacheOrder[:0]
	for _, id := range w.cacheOrder {
		if !gone[id] {
			kept = append(kept, id)
		}
	}
	w.cacheOrder = kept
}

// ---------------------------------------------------------------------------
// Edges, and G
// ---------------------------------------------------------------------------

// maybeFetchAtEdge is the one place a scroll turns into a fetch. Called at the
// end of every scroll step and focus move, and idempotent by construction --
// requestHistory's own one-at-a-time gate means re-asking at the same edge
// costs nothing.
func (w *Workspace) maybeFetchAtEdge() {
	if w.topAbs == 0 && w.topRow == 0 && w.window.moreBefore {
		w.requestHistory(histBefore, w.window.oldestSeq)
		return
	}
	if !w.window.reachesTail() && w.atResidentBottom() {
		w.requestHistory(histAfter, w.window.newestSeq)
	}
}

// atResidentBottom reports whether the viewport is showing the last row the
// window currently holds. Unlike w.pinned this is a question about the
// RESIDENT slice, which is exactly the distinction that matters once the tail
// can be absent -- see scroll.go's re-pin branch.
func (w *Workspace) atResidentBottom() bool {
	if w.entryCount() == 0 {
		return true
	}
	bAbs, bRow := w.bottomAnchored(w.viewRows, w.viewWidth)
	return w.topAbs > bAbs || (w.topAbs == bAbs && w.topRow >= bRow)
}

// historyAtTail is scroll.go's and moveFocus's gate on re-pinning: pinned must
// keep meaning "at the LIVE edge", not "at the bottom of whatever happens to
// be loaded". Without this, reaching the bottom of a window whose tail had
// been evicted would pin, the pinned render would bottom-anchor to a stale
// newest message, and the "jump to latest (G)" affordance would vanish at
// exactly the moment it was needed.
func (w *Workspace) historyAtTail() bool { return w.window.reachesTail() }

// jumpToBottom is G's new half.
//
// G used to be free: everything was in memory, so pinning was the whole
// operation. With eviction the newest rows may not be resident, so G becomes
// a fetch. F18 owns that behaviour change explicitly -- it shows the same
// loading affordance a scroll-fetch does and re-pins when the batch lands.
//
// It stays instant in the common case: a session smaller than the bound, or
// one never scrolled away from the tail, already reaches the tail and this
// returns without sending anything.
func (w *Workspace) jumpToBottom() {
	if w.window.reachesTail() {
		return
	}
	w.requestHistory(histTail, 0)
}

// historyNotice is the loading affordance's text, or "" when nothing is
// pending, plus where it belongs (top edge or bottom edge). Functional, not
// final -- F16 may restyle it.
//
// The parked case says so rather than saying "loading": a fetch held back by
// backpressure is not slow, it is deliberately waiting, and telling the user
// it is loading would be a hint that lies. F04's Bug 12 (a browse indicator
// advertising an Esc binding that did not exist) is this codebase's standing
// rule against exactly that.
func (w *Workspace) historyNotice() (text string, atTop bool) {
	if d := w.window.deferred; d != histNone {
		return "⏸ history paused while generating", d == histBefore
	}
	switch w.window.inFlight {
	case histBefore, histAnchored:
		return "◐ loading older messages…", true
	case histAfter:
		return "◐ loading newer messages…", false
	case histTail:
		return "◐ loading latest…", false
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Suspend / Resume — the F12 forward contract
// ---------------------------------------------------------------------------

// suspendBlob is what Suspend returns and Resume reads.
//
// This is the FIRST real implementation of either, not a change to one: both
// were `return nil, nil` from F3 until here, because F3 had nowhere to write
// them and F12 owns workspace-state persistence. F18 fills them because F18
// is the episode that finally has something that must survive an unload, and
// because doing it now is what stops F12 from designing a blob schema that
// has never heard of a scroll anchor -- the B11 AgentConfig.background
// retrofit trap, avoided by ordering.
//
// Versioned from the start: an unrecognised version resumes at the bottom
// rather than half-reading a shape it does not know.
//
// The anchor deliberately does NOT also live in state.json beside F4's
// Drafts. Two homes would give F12 two anchors to reconcile, and full-restart
// survival is F12's to decide, not this episode's to pre-empt.
type suspendBlob struct {
	Version int         `json:"version"`
	Anchor  *blobAnchor `json:"anchor,omitempty"`
}

// suspendVersion is this blob's shape. F12's real workspace-state type must
// ADAPT to this, not redefine it -- the same CardSlot/Drafts precedent F1 and
// F4 both set.
const suspendVersion = 1

// blobAnchor is F18's scroll anchor, and the three fields are the whole
// contract:
//
//   - AnchorSeq: WHICH message, keyed on B18's seq and nothing else. Zero
//     means "no addressable anchor" -- the position was on one of this
//     session's own live sends, whose persisted seq the frontend never learns
//     (the backend persists it and never echoes it back), or on
//     applyTaskRows' placeholder, which has no row to have a seq.
//   - RowOffset: how far INTO that message's block the top row sits. Without
//     it a resume lands at the head of a reply the user was reading the
//     middle of, which on a long reply is a different screen entirely.
//   - PinnedToBottom: whether the user was at the live edge.
//
// PinnedToBottom is not redundant with AnchorSeq, and it is the field most
// easily got wrong later. Restoring a pinned user to their old anchor seq
// would strand them at a stale message with unread content below, because a
// session can RECEIVE MESSAGES WHILE UNLOADED -- an F5 background task, or an
// SSH peer. Pinned means "the newest message", resolved at Resume time, not
// the newest message as of Suspend. It earns its keep only once F5 streams
// into unloaded workspaces, which is precisely why it is easy to forget later
// and cheap to specify now.
type blobAnchor struct {
	AnchorSeq      int  `json:"anchor_seq"`
	RowOffset      int  `json:"row_offset"`
	PinnedToBottom bool `json:"pinned_to_bottom"`
}

// currentBlobAnchor reads the live position into the blob's shape.
func (w *Workspace) currentBlobAnchor() *blobAnchor {
	a := &blobAnchor{PinnedToBottom: w.pinned, RowOffset: w.topRow}
	if id := w.captureAnchor(w.topAbs, w.topRow); id.ok && !id.inLive {
		a.AnchorSeq = id.seq
		a.RowOffset = id.row
	}
	return a
}

// applyResumeAnchor consumes a stored anchor, once, at the end of setSession
// -- the first moment the session id, the window and the transcript all
// exist. Every branch ends somewhere valid; none can fail the load.
func (w *Workspace) applyResumeAnchor() {
	a := w.resumeAnchor
	w.resumeAnchor = nil
	if a == nil || a.PinnedToBottom || a.AnchorSeq == 0 {
		// setSession has already pinned to the current newest, which is
		// exactly what PinnedToBottom means -- and the right degradation
		// for an anchor that was never addressable.
		return
	}
	if a.AnchorSeq > w.window.ceilingSeq {
		w.log.Info("chat: resume anchor is newer than this session, degrading to the bottom",
			"seq", a.AnchorSeq, "ceiling", w.window.ceilingSeq)
		return
	}
	for i, m := range w.all {
		if m.Seq != a.AnchorSeq {
			continue
		}
		// Already resident: the stored position lands without a fetch.
		w.pinned = false
		w.topAbs, w.topRow = i, a.RowOffset
		w.clampScroll(w.viewWidth)
		return
	}
	// Older than the tail that opened. Fetch a window STARTING at the
	// anchor (`after = seq-1`, i.e. "everything with seq > seq-1") rather
	// than ending at it, so the anchor sits at the head of the window with
	// content below it to fill the viewport. `after = 0` is a real boundary
	// meaning the start of history, which is why this can use seq-1
	// unconditionally -- see B18's own note on never seeding a page-through
	// with a bare nil cursor.
	w.window.anchorTarget = a.AnchorSeq
	w.window.anchorRow = a.RowOffset
	w.requestHistory(histAnchored, a.AnchorSeq-1)
}
