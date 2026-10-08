package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// histFiller pads a seeded row so its rendered block is several rows tall at
// the 60-cell test width. Needed because a row OFFSET inside a message is
// only a real position when the message has rows to offset into: a one-line
// message's block is two rows (one gap plus one line), so any offset above 1
// is legitimately clamped by clampScroll and a test asserting otherwise would
// be asserting a bug.
const histFiller = " filler text for scrollback and render testing, long " +
	"enough that one message wraps across several rendered rows at the " +
	"width these tests use, which is what makes a row offset inside it mean " +
	"something at all."

// histRow is a seeded persisted row. Seq is what everything here keys on;
// the id is derived from it so a failure message names the row.
func histRow(seq int, role, text string) wireMessage {
	return wireMessage{
		ID:      "m" + itoa(seq),
		Seq:     seq,
		Role:    role,
		Content: wireContent{Text: text},
	}
}

// histRows is `count` alternating rows starting at `from`, the same shape
// backend\cli.py's seed-session writes.
func histRows(from, count int) []wireMessage {
	out := make([]wireMessage, 0, count)
	for i := 0; i < count; i++ {
		seq := from + i
		role := roleUser
		if seq%2 == 0 {
			role = roleAssistant
		}
		out = append(out, histRow(seq, role, "seed message "+itoa(seq)+histFiller))
	}
	return out
}

// histWorkspace is a Workspace with a recording history sender. The
// substitution is the same one stream_test's newStreamWorkspace makes for
// sendMsgFn and for the same reason: what these tests exercise is the
// window's decision logic, not protocol.Client's send path.
type histWorkspace struct {
	*Workspace
	sent []map[string]any
	ids  []string
	next int
}

func newHistWorkspace(t *testing.T, scrollback, batch int) *histWorkspace {
	t.Helper()
	w := newStreamWorkspace(t)
	w.scrollback = scrollback
	w.historyBatchSize = batch
	// viewRows floors historyBound at 3 screenfuls; keep it small enough
	// that the scrollback under test is what governs.
	w.viewRows, w.viewWidth = 4, 60

	h := &histWorkspace{Workspace: w}
	w.sendHistoryFn = func(payload map[string]any) (string, bool) {
		h.next++
		id := "fetch-" + itoa(h.next)
		h.sent = append(h.sent, payload)
		h.ids = append(h.ids, id)
		return id, true
	}
	return h
}

// open seeds the session the way a bounded GetSession reply would.
func (h *histWorkspace) open(tail []wireMessage, total int) {
	h.setSession(wireSession{ID: "s1", Messages: tail, TotalMessageCount: total})
}

// reply delivers a SessionMessageList for the newest fetch.
func (h *histWorkspace) reply(t *testing.T, msgs []wireMessage, moreBefore, moreAfter bool) {
	t.Helper()
	if len(h.ids) == 0 {
		t.Fatal("no fetch was issued, so there is nothing to reply to")
	}
	h.replyTo(t, h.ids[len(h.ids)-1], msgs, moreBefore, moreAfter)
}

func (h *histWorkspace) replyTo(t *testing.T, corrID string, msgs []wireMessage, moreBefore, moreAfter bool) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id":      "s1",
		"messages":        msgs,
		"has_more_before": moreBefore,
		"has_more_after":  moreAfter,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.onSessionMessages(protocol.Envelope{
		Type: protocol.EvSessionMessageList, CorrID: corrID, Payload: raw,
	})
}

func (h *histWorkspace) lastSent(t *testing.T) map[string]any {
	t.Helper()
	if len(h.sent) == 0 {
		t.Fatal("no history fetch was sent")
	}
	return h.sent[len(h.sent)-1]
}

// seqs is the resident window's seq run, for an exact assertion on
// contiguity rather than on a length.
func (h *histWorkspace) seqs() []int {
	out := make([]int, 0, len(h.raw))
	for _, m := range h.raw {
		out = append(out, m.Seq)
	}
	return out
}

func wantContiguous(t *testing.T, got []int, from, to int) {
	t.Helper()
	if len(got) != to-from+1 {
		t.Fatalf("window holds %d rows (%v…%v), want %d (%d…%d)",
			len(got), firstOf(got), lastOf(got), to-from+1, from, to)
	}
	for i, s := range got {
		if s != from+i {
			t.Fatalf("window is not contiguous at index %d: seq %d, want %d (run %v…%v)",
				i, s, from+i, firstOf(got), lastOf(got))
		}
	}
}

func firstOf(s []int) int {
	if len(s) == 0 {
		return 0
	}
	return s[0]
}

func lastOf(s []int) int {
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

// ---------------------------------------------------------------------------
// 1 — open is bounded, and never a full-session fetch
// ---------------------------------------------------------------------------

// The episode's first Done-when, as a unit test rather than a stopwatch:
// opening a 50,000-message session must cost the SAME WORK as opening one
// with ten, because the request is bounded. The timing claim the gate makes
// eyes-on follows from this; what is checkable here is that the frontend
// never asks for more than the bound and never holds more than it asked for.
func TestOpenAsksForABoundedTailAndHoldsOnlyThat(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)

	var asked map[string]any
	h.sendFn = func(cmdType string, _ queryKind, payload map[string]any) {
		if cmdType == protocol.CmdGetSession {
			asked = payload
		}
	}
	h.getSession("s1")

	if asked == nil {
		t.Fatal("GetSession was not sent")
	}
	limit, ok := asked["message_limit"].(int)
	if !ok {
		t.Fatalf("GetSession carries no message_limit: %v — this is the full-session fetch F18 exists to remove", asked)
	}
	if limit != h.historyBound() {
		t.Fatalf("GetSession asked for %d, want the resident bound %d", limit, h.historyBound())
	}

	// Seeding the reply: the tail, plus the total the bounded path reports.
	h.open(histRows(49961, 40), 50000)

	wantContiguous(t, h.seqs(), 49961, 50000)
	if !h.window.moreBefore {
		t.Fatal("a 40-row tail of a 50,000-message session must report that older history exists")
	}
	if !h.window.reachesTail() {
		t.Fatal("the window seeded from a tail must reach the tail")
	}
	if h.window.ceilingSeq != 50000 {
		t.Fatalf("ceiling is %d, want the newest seq at open (50000)", h.window.ceilingSeq)
	}
}

// ---------------------------------------------------------------------------
// 2 — prepend preserves the anchor: Done-when #3, no content jump
// ---------------------------------------------------------------------------

// The bug the episode calls "most likely to ship broken". The message under
// the user's eye must stay put as older content loads above — which means the
// position must still address THE SAME MESSAGE afterwards, at the same row
// offset inside it, with its absolute index having moved by exactly the
// number of rows inserted above.
//
// Note what is deliberately NOT asserted: a row count or a pixel shift. The
// position is (entry, offset), so correctness is "the same entry", and any
// test that checked an absolute row would be re-deriving the bug.
func TestPrependKeepsThePositionOnTheSameMessage(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)

	// Park the viewport mid-window, three rows into a message.
	h.pinned = false
	h.topAbs, h.topRow = 5, 3
	anchored := h.all[h.topAbs]
	if anchored.Seq == 0 {
		t.Fatal("the row under test has no seq")
	}

	h.requestHistory(histBefore, h.window.oldestSeq)
	h.reply(t, histRows(11, 10), true, true)

	wantContiguous(t, h.seqs(), 11, 40)
	if h.pinned {
		t.Fatal("a prepend must not pin the viewport to the bottom")
	}
	if got := h.all[h.topAbs]; got.Seq != anchored.Seq {
		t.Fatalf("the position now addresses seq %d, want the message it was on (seq %d) — "+
			"this is the content jump Done-when #3 forbids", got.Seq, anchored.Seq)
	}
	if h.topRow != 3 {
		t.Fatalf("row offset within the anchored message is %d, want 3 — the view shifted inside the message", h.topRow)
	}
	if h.all[h.topAbs].ID != anchored.ID {
		t.Fatalf("the position addresses message %q, want %q", h.all[h.topAbs].ID, anchored.ID)
	}
	if h.topAbs != 5+10 {
		t.Fatalf("absolute index is %d, want 15 (5 plus the 10 rows inserted above)", h.topAbs)
	}
}

// A prepend while PINNED must leave the user pinned. Pinned means "track the
// live edge", and older content arriving above does not move that edge —
// restoring a position would silently unpin and strand a following user.
func TestPrependWhilePinnedStaysPinned(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)
	h.pinned = true

	h.requestHistory(histBefore, h.window.oldestSeq)
	h.reply(t, histRows(11, 10), true, true)

	if !h.pinned {
		t.Fatal("a prepend unpinned a pinned viewport")
	}
}

// ---------------------------------------------------------------------------
// 3 — eviction: Done-when #4, memory bounded
// ---------------------------------------------------------------------------

// Scrolling up evicts from the NEWEST end — the edge the user is moving away
// from — and the window never exceeds its bound. This is the mechanism behind
// the memlog proof: without it, w.raw grows by one batch per scroll-fetch for
// as long as the user keeps scrolling, which is a heap that climbs with
// SCROLL DEPTH even on a small session.
func TestPrependEvictsFromTheFarEdgeAndStaysBounded(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(61, 40), 1000)
	h.pinned = false
	h.topAbs, h.topRow = 0, 0

	for round := 0; round < 6; round++ {
		// A prepend only ever fires from the TOP of the window
		// (maybeFetchAtEdge), so the anchor is at the head when the batch
		// lands. Replaying that rather than holding a fixed anchor across
		// six batches matters: a protected row is never evicted, so an
		// anchor parked at the far edge legitimately holds the window over
		// its bound -- see evictWindow's own doc. The realistic sequence
		// has the anchor at the near edge, where the bound is exact.
		h.topAbs, h.topRow = 0, 0
		oldest := h.window.oldestSeq
		h.requestHistory(histBefore, oldest)
		h.reply(t, histRows(oldest-10, 10), true, true)

		if len(h.raw) > h.historyBound() {
			t.Fatalf("round %d: window holds %d rows, bound is %d — eviction is not keeping up",
				round, len(h.raw), h.historyBound())
		}
		wantContiguous(t, h.seqs(), h.window.oldestSeq, h.window.newestSeq)
	}

	if h.window.oldestSeq != 1 {
		t.Fatalf("after six batches the oldest resident seq is %d, want 1", h.window.oldestSeq)
	}
	if h.window.reachesTail() {
		t.Fatal("the tail was evicted, so the window must no longer claim to reach it")
	}
}

// The documented slack, pinned down rather than left as prose.
//
// Protecting the anchored and focused rows from eviction cannot be
// unconditional: holding a focus on the newest row and fetching older batches
// forever grew the window by one batch PER FETCH before evictWindow capped it
// (50 rows, then 60, then 70, against a bound of 40) -- a heap climbing with
// scroll depth, which is the exact failure Done-when #2 calls a hard stop. So
// the protection is capped at one batch of slack, past which the row is
// evicted and the position degrades honestly.
//
// This sequence is unreachable by scrolling (a prepend fires only from the
// top of the window, so the anchor is never the far-edge blocker); it is
// driven directly here precisely because no key can drive it.
func TestAProtectedFarEdgeRowHoldsAtMostOneBatchOfSlack(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(61, 40), 1000)
	h.pinned = false
	// Anchor and focus both on the NEWEST row, which is what the far-edge
	// cut wants to drop, and never move them.
	h.topAbs = len(h.all) - 1
	h.focus = len(h.all) - 1

	for round := 0; round < 8; round++ {
		oldest := h.window.oldestSeq
		h.requestHistory(histBefore, oldest)
		h.reply(t, histRows(oldest-10, 10), true, true)

		if max := h.historyBound() + h.historyBatch(); len(h.raw) > max {
			t.Fatalf("round %d: window holds %d rows, bound+batch is %d — protecting a pinned row "+
				"must cost bounded slack, not unbounded growth", round, len(h.raw), max)
		}
		wantContiguous(t, h.seqs(), h.window.oldestSeq, h.window.newestSeq)
		// And whatever happened to the protection, the position is always
		// addressable afterwards.
		if !h.pinned && (h.topAbs < 0 || h.topAbs >= h.entryCount()) {
			t.Fatalf("round %d: the position is %d with %d entries — a dropped protection must "+
				"degrade, not leave an unaddressable anchor", round, h.topAbs, h.entryCount())
		}
		if h.focus >= h.entryCount() {
			t.Fatalf("round %d: focus is %d with %d entries", round, h.focus, h.entryCount())
		}
	}
}

// Scrolling back down re-fetches what was dropped, with no gap and no
// duplicate — the other half of Done-when #4, and the property that makes
// aggressive eviction correct rather than merely cheap.
func TestScrollBackDownRefetchesWithoutGapOrDuplicate(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(61, 40), 100)
	h.pinned = false
	h.topAbs, h.topRow = 0, 0

	// Up far enough to evict the tail.
	for round := 0; round < 4; round++ {
		oldest := h.window.oldestSeq
		h.requestHistory(histBefore, oldest)
		h.reply(t, histRows(oldest-10, 10), oldest-10 > 1, true)
	}
	if h.window.reachesTail() {
		t.Fatal("the tail should have been evicted by now")
	}
	evictedFrom := h.window.newestSeq

	// Back down.
	for h.window.newestSeq < 100 {
		newest := h.window.newestSeq
		h.requestHistory(histAfter, newest)
		h.reply(t, histRows(newest+1, 10), true, newest+10 < 100)
		wantContiguous(t, h.seqs(), h.window.oldestSeq, h.window.newestSeq)
		if len(h.raw) > h.historyBound() {
			t.Fatalf("window holds %d rows, bound is %d", len(h.raw), h.historyBound())
		}
	}

	if h.window.newestSeq != 100 {
		t.Fatalf("scrolling back down reached seq %d, want 100", h.window.newestSeq)
	}
	if !h.window.reachesTail() {
		t.Fatal("the window re-reached the tail but does not say so")
	}
	if h.window.newestSeq <= evictedFrom {
		t.Fatalf("nothing was actually re-fetched: newest is %d, tail was evicted at %d",
			h.window.newestSeq, evictedFrom)
	}
	if !h.window.moreBefore {
		t.Fatal("rows were dropped from the head, so older history must be reported as existing")
	}
}

// The anchored row and the focused row are never evicted. Everything the
// transcript's actions act on (y, o, t, ^R) resolves through the focus, and
// the anchor is where the next frame is composed from.
func TestEvictionNeverDropsTheAnchorOrTheFocus(t *testing.T) {
	h := newHistWorkspace(t, 20, 10)
	h.open(histRows(81, 20), 1000)

	// Anchor and focus both on the NEWEST resident row — the exact row the
	// far-edge eviction wants to drop. ONE batch, so this exercises the
	// protection itself rather than the slack cap that bounds it
	// (TestAProtectedFarEdgeRowHoldsAtMostOneBatchOfSlack owns that).
	h.pinned = false
	h.topAbs = len(h.all) - 1
	h.topRow = 0
	h.focus = len(h.all) - 1
	anchorSeq := h.all[h.topAbs].Seq

	h.requestHistory(histBefore, h.window.oldestSeq)
	h.reply(t, histRows(71, 10), true, true)

	found := false
	for _, m := range h.raw {
		if m.Seq == anchorSeq {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("seq %d was both the anchor and the focus and was evicted anyway", anchorSeq)
	}
	if got := h.all[h.topAbs].Seq; got != anchorSeq {
		t.Fatalf("the position now addresses seq %d, want %d", got, anchorSeq)
	}
	if got := h.all[h.focus].Seq; got != anchorSeq {
		t.Fatalf("the focus now sits on seq %d, want %d", got, anchorSeq)
	}
}

// A background fetch must not CREATE a focus. focusedIndex() falls back to
// the newest entry when nothing is explicitly focused, so re-resolving the
// focus through it would turn "nothing is focused" into "the newest entry
// is focused" on the first scroll-fetch — a focus ring appearing out of a
// fetch the user did not make, and a row protected from eviction for the
// rest of the session.
func TestAScrollFetchDoesNotInventAFocus(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)
	h.pinned = false
	h.topAbs, h.topRow = 3, 0
	if h.focus != -1 {
		t.Fatalf("precondition: focus is %d, want -1", h.focus)
	}

	h.requestHistory(histBefore, h.window.oldestSeq)
	h.reply(t, histRows(11, 10), true, true)

	if h.focus != -1 {
		t.Fatalf("a scroll-fetch set the focus to %d; nothing had been focused", h.focus)
	}
}

// ---------------------------------------------------------------------------
// 4 — the live-send ceiling (decision 4)
// ---------------------------------------------------------------------------

// A re-fetched tail must never hand back rows w.live is already rendering.
// The backend persists both halves of every send; w.live holds the entries
// for them; without the ceiling an `after=` or tail fetch would return those
// same rows and every live send would appear twice.
func TestFetchNeverIngestsAboveTheOpenTimeCeiling(t *testing.T) {
	h := newHistWorkspace(t, 20, 10)
	h.open(histRows(1, 20), 20)
	if h.window.ceilingSeq != 20 {
		t.Fatalf("ceiling is %d, want 20", h.window.ceilingSeq)
	}

	// A send this session, finished. The backend would persist it as seq
	// 21/22. It has to be FINISHED for this test to reach the fetch at all
	// -- backpressure parks a history fetch while anything streams, which
	// TestHistoryFetchIsDeferredWhileStreamingAndFlushedAfter covers.
	h.startSend("a live question", "")
	h.finish(h.mustEntry(t, 0), statusDone)
	before := len(h.raw)

	// Force a tail re-fetch and have the backend hand back the live rows
	// too, as a cursorless tail genuinely would.
	h.window.newestSeq = 15
	h.raw = h.raw[:15]
	h.requestHistory(histTail, 0)

	if got := h.lastSent(t)["before"]; got != 21 {
		t.Fatalf("the tail fetch asked before=%v, want ceiling+1 (21) so it cannot reach the live rows", got)
	}
	h.reply(t, append(histRows(11, 10), histRow(21, roleUser, "a live question"), histRow(22, roleAssistant, "a live reply")), true, false)

	for _, m := range h.raw {
		if m.Seq > 20 {
			t.Fatalf("seq %d is above the ceiling and was ingested anyway — "+
				"this is the duplicate every live send would show", m.Seq)
		}
	}
	if h.window.newestSeq != 20 {
		t.Fatalf("newest resident seq is %d, want the ceiling (20)", h.window.newestSeq)
	}
	if before == 0 {
		t.Fatal("the window was empty, so this proved nothing")
	}
	// And the live entries are still there, exactly once each.
	if len(h.live) != 2 {
		t.Fatalf("w.live holds %d entries, want the send's question and reply", len(h.live))
	}
}

// An actively streaming reply is resident regardless of scroll position,
// because w.live is never evicted and always renders after w.all — so §5's
// "no hole in the window" is structural, not a tail buffer bolted on. The
// ceiling is what guarantees its persisted twin can never enter w.raw and
// open one.
func TestAStreamingReplyStaysResidentThroughATailEviction(t *testing.T) {
	h := newHistWorkspace(t, 20, 10)
	// Deliberately seeded ABOVE the bound so the first eviction has
	// something to drop; a real window arrives at the bound and exceeds it
	// only after a prepend.
	h.open(histRows(1, 40), 40)
	h.startSend("ask", "")
	e := h.mustEntry(t, 0)
	e.text = "partial tokens so far"

	// Scroll deep: evict the tail of history. Done directly rather than
	// through a fetch, because backpressure (correctly) parks a fetch while
	// this very reply is streaming -- which is the point of the other test,
	// not this one.
	h.pinned = false
	h.topAbs, h.topRow = 0, 0
	h.evictWindow(histBefore)
	h.rebuildWindow()
	if h.window.reachesTail() {
		t.Fatal("the tail was not evicted, so this proves nothing")
	}

	if h.Streaming() != true {
		t.Fatal("the reply should still be streaming")
	}
	if len(h.live) != 2 {
		t.Fatalf("w.live holds %d entries after an eviction, want 2 — a streaming reply was lost", len(h.live))
	}
	last := h.entryCount() - 1
	if got, _ := h.entryText(last); !strings.Contains(got, "partial tokens") {
		t.Fatalf("the newest entry is %q, want the streaming reply — it is no longer at the live edge", got)
	}
}

// ---------------------------------------------------------------------------
// 5 — G, when the tail is not resident (Done-when #5)
// ---------------------------------------------------------------------------

func TestJumpToBottomFetchesOnlyWhenTheTailIsGone(t *testing.T) {
	t.Run("instant when the tail is resident", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		h.open(histRows(1, 20), 20)
		h.jumpToBottom()
		if len(h.sent) != 0 {
			t.Fatalf("G sent %d fetches against a resident tail, want none — it must stay free in the common case", len(h.sent))
		}
	})

	t.Run("fetches and re-pins when the tail was evicted", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		h.open(histRows(61, 40), 100)
		h.pinned = false
		h.topAbs, h.topRow = 0, 0
		for round := 0; round < 4; round++ {
			oldest := h.window.oldestSeq
			h.requestHistory(histBefore, oldest)
			h.reply(t, histRows(oldest-10, 10), oldest-10 > 1, true)
		}
		if h.window.reachesTail() {
			t.Fatal("the tail should have been evicted")
		}

		h.handleKeyString("G")
		if h.window.inFlight != histTail {
			t.Fatalf("G did not fetch the tail (inFlight=%v)", h.window.inFlight)
		}
		h.reply(t, histRows(61, 40), true, false)

		if !h.pinned {
			t.Fatal("G did not re-pin after the tail landed")
		}
		if h.window.newestSeq != 100 {
			t.Fatalf("G landed on seq %d, want the true newest (100)", h.window.newestSeq)
		}
		if !h.window.reachesTail() {
			t.Fatal("after G the window must reach the tail")
		}
		if h.focus != -1 {
			t.Fatalf("G left focus at %d, want -1", h.focus)
		}
	})
}

// Reaching the bottom of a window whose tail is absent must NOT pin. Pinned
// has to keep meaning "at the live edge" or the pinned render bottom-anchors
// to a stale message and the jump-to-latest affordance disappears exactly
// when it is the only way back.
func TestReachingAWindowedBottomDoesNotPin(t *testing.T) {
	h := newHistWorkspace(t, 20, 10)
	h.open(histRows(81, 20), 100)
	h.window.newestSeq = 90 // pretend the tail was evicted
	h.raw = h.raw[:10]
	h.rebuildWindow()

	h.pinned = false
	h.scrollRows(500, h.viewWidth)

	if h.pinned {
		t.Fatal("scrolling to the bottom of a windowed transcript pinned to a stale tail")
	}
	if h.window.inFlight != histAfter && h.window.deferred != histAfter {
		t.Fatalf("reaching the resident bottom did not ask for newer rows (inFlight=%v)", h.window.inFlight)
	}
}

// ---------------------------------------------------------------------------
// 6 — backpressure (Done-when #7)
// ---------------------------------------------------------------------------

// While a task streams, a history fetch is PARKED, not sent. A local model is
// CPU-bound and hydrating scrollback nobody is looking at costs tokens/sec on
// the thing they are.
func TestHistoryFetchIsDeferredWhileStreamingAndFlushedAfter(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)
	h.startSend("ask", "")
	if !h.Streaming() {
		t.Fatal("the send should be streaming")
	}

	h.requestHistory(histBefore, h.window.oldestSeq)
	if len(h.sent) != 0 {
		t.Fatalf("a fetch went out while a task was streaming: %v", h.sent)
	}
	if h.window.deferred != histBefore {
		t.Fatalf("the fetch was dropped rather than parked (deferred=%v)", h.window.deferred)
	}
	if note, _ := h.historyNotice(); !strings.Contains(note, "paused") {
		t.Fatalf("the affordance says %q while a fetch is parked — it must not claim to be loading", note)
	}

	// The stream ends.
	e := h.mustEntry(t, 0)
	e.taskID = "t1"
	h.handleStreamEvent(protocol.Envelope{Type: protocol.EvTaskDone, TaskID: "t1"})

	if h.Streaming() {
		t.Fatal("the task did not end")
	}
	if len(h.sent) != 1 {
		t.Fatalf("the parked fetch was not replayed when the stream settled (sent %d)", len(h.sent))
	}
	if got := h.lastSent(t)["before"]; got != 21 {
		t.Fatalf("the replayed fetch asked before=%v, want the window's oldest seq (21)", got)
	}
}

// Only one fetch is ever outstanding: a second would compute its cursor
// against a window the first is about to change.
func TestOnlyOneHistoryFetchIsOutstanding(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)

	h.requestHistory(histBefore, 21)
	h.requestHistory(histBefore, 21)
	h.requestHistory(histAfter, 40)

	if len(h.sent) != 1 {
		t.Fatalf("%d concurrent history fetches went out, want 1", len(h.sent))
	}
}

// A reply for a superseded fetch is dropped, and so is the backend's
// null-corr_id heartbeat. F8's Bug A was exactly this shape: a cleared
// correlation-id field is a VALID switch case, and the 5-second heartbeat
// then matched it forever.
func TestASupersededOrHeartbeatReplyIsDropped(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(21, 20), 1000)
	h.requestHistory(histBefore, 21)
	stale := h.ids[0]

	// G supersedes it.
	h.window.inFlight = histNone
	h.requestHistory(histTail, 0)

	h.replyTo(t, stale, histRows(11, 10), true, true)
	wantContiguous(t, h.seqs(), 21, 40)

	h.replyTo(t, "", histRows(11, 10), true, true)
	wantContiguous(t, h.seqs(), 21, 40)

	if h.window.inFlight != histTail {
		t.Fatalf("a dropped reply cleared the in-flight fetch (inFlight=%v)", h.window.inFlight)
	}
}

// ---------------------------------------------------------------------------
// 7 — window-edge pairing (decision 3)
// ---------------------------------------------------------------------------

// A question whose task failed after streaming tokens HAS a real assistant
// row. With that row outside the window, an ungated tasks join would see an
// unanswered question and insert a fabricated "✗ failed" placeholder into a
// window whose whole contract is that it is a faithful slice.
func TestNoOrphanPlaceholderIsInventedAwayFromTheTail(t *testing.T) {
	q := histRow(1, roleUser, "@research dig")
	q.TaskID = "t1"
	a := histRow(2, roleAssistant, "partial answer before it failed")
	a.TaskID = "t1"
	a.Outcome = outcomeFailed

	t.Run("suppressed while the answer is outside the window", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		// Window holds the question only; the answer is one batch below.
		h.open([]wireMessage{q}, 2)
		h.window.ceilingSeq = 2 // the session's real tail is the answer
		h.taskRows = map[string]taskRow{"t1": {ID: "t1", State: taskFailed, Agent: "research"}}
		h.rebuildWindow()

		if len(h.all) != 1 {
			t.Fatalf("the window holds %d rows, want 1 — a placeholder reply was invented for a task "+
				"whose real answer is simply not resident", len(h.all))
		}
	})

	t.Run("allowed once the window reaches the tail", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		// Same question, but now its task genuinely left no message: the
		// window reaches the tail and there is no answer anywhere in it.
		h.open([]wireMessage{q}, 1)
		h.taskRows = map[string]taskRow{"t1": {ID: "t1", State: taskFailed, Agent: "research"}}
		h.rebuildWindow()

		if len(h.all) != 2 {
			t.Fatalf("the window holds %d rows, want 2 — the orphan footer for a task that really "+
				"produced nothing was suppressed at the true tail", len(h.all))
		}
		if e := h.histAt(1); e == nil || e.status != statusFailed {
			t.Fatalf("the inserted placeholder carries %v, want a failed entry", e)
		}
	})
}

// Pairing across a window seam is not a reorder that fights the user: an
// answer that is not resident is not a candidate, so its question renders
// unpaired, and the pairing resolves on its own once the adjoining batch
// loads. Both halves are asserted, because the second is what makes the first
// a deferral rather than a loss.
func TestPairingResolvesOnceTheSeamFillsIn(t *testing.T) {
	q := histRow(10, roleUser, "@research dig")
	q.TaskID = "t1"
	filler := histRow(11, roleUser, "an unrelated question")
	a := histRow(12, roleAssistant, "the research answer")
	a.TaskID = "t1"
	a.Invocation = &wireInvocation{Agent: "research"}
	a.Outcome = outcomeDone

	h := newHistWorkspace(t, 40, 10)
	// Only the question and the filler are resident; the answer is below.
	h.open([]wireMessage{q, filler}, 3)
	h.window.ceilingSeq = 12
	h.window.moreBefore = false

	if got := len(h.all); got != 2 {
		t.Fatalf("window holds %d rows, want 2", got)
	}
	if h.histAt(0) != nil {
		t.Fatal("a question with no resident answer must not be decorated as an exchange")
	}

	// The seam fills in.
	h.requestHistory(histAfter, h.window.newestSeq)
	h.reply(t, []wireMessage{a}, true, false)

	if got := len(h.all); got != 3 {
		t.Fatalf("window holds %d rows after the append, want 3", got)
	}
	if h.all[1].Seq != 12 {
		t.Fatalf("the answer sits at index %d (seq %d), want directly under its question",
			1, h.all[1].Seq)
	}
	e := h.histAt(1)
	if e == nil {
		t.Fatal("the paired research answer is not decorated as a task slot — pairing did not re-resolve")
	}
	if e.lane != laneTask {
		t.Fatalf("the reloaded research answer is in lane %v, want laneTask — reloadLane/agentLane/"+
			"app.IsBackgroundAgent is the mechanism this must come from", e.lane)
	}
	if e.userText != "@research @research dig" && !strings.Contains(e.userText, "dig") {
		t.Fatalf("the paired entry's retry text is %q, want the question rebuilt from its own row", e.userText)
	}
}

// ---------------------------------------------------------------------------
// 7b — a rebuild must not take the user's own state with it
// ---------------------------------------------------------------------------

// researchWindow is a window holding one research exchange plus filler. The
// `answered` variant has a real assistant row; the other is a CANCELLED task
// that produced no message at all, so applyTaskRows inserts a placeholder
// reply with no id and no seq.
func researchWindow(t *testing.T, answered bool) (*histWorkspace, int) {
	t.Helper()
	q := histRow(41, roleUser, "@research dig")
	q.TaskID = "t1"
	msgs := []wireMessage{q}
	state := taskCancelled
	if answered {
		a := histRow(42, roleAssistant, "the research answer")
		a.TaskID = "t1"
		a.Invocation = &wireInvocation{Agent: "research"}
		a.Outcome = outcomeDone
		msgs = append(msgs, a, histRow(43, roleUser, "unrelated"))
		msgs = append(msgs, histRows(44, 17)...)
		state = taskCompleted
	} else {
		msgs = append(msgs, histRows(42, 19)...)
	}

	h := newHistWorkspace(t, 40, 10)
	h.open(msgs, 1000)
	h.taskRows = map[string]taskRow{"t1": {ID: "t1", State: state, Agent: "research"}}
	h.rebuildWindow()

	for i := 0; i < h.entryCount(); i++ {
		if e := h.liveAt(i); e != nil && e.lane == laneTask {
			h.focus = i
			return h, i
		}
	}
	t.Fatal("no research slot in this window")
	return nil, -1
}

// A slot the user has OPENED must stay open when a scroll-fetch lands.
//
// Found during F18's own verification, reported as "o stops working on a task
// entry after moving the cursor". decorateReloaded allocates fresh liveEntry
// values from the message columns, and F18 made rebuildWindow run on every
// prepend, append and eviction rather than once per session load -- so
// slotOpen and expanded, which are the USER's state and exist nowhere but on
// that pointer, were thrown away by every fetch.
func TestARebuildKeepsASlotOpenAndTheFocusOnIt(t *testing.T) {
	for _, answered := range []bool{true, false} {
		name := "answered slot"
		if !answered {
			name = "cancelled slot (applyTaskRows placeholder: no id, no seq)"
		}
		t.Run(name, func(t *testing.T) {
			h, idx := researchWindow(t, answered)
			e := h.liveAt(idx)

			h.handleKeyString("o")
			if !e.slotOpen {
				t.Fatal("precondition: o did not open the slot")
			}

			// Scroll to the top edge, which fires a prepend.
			h.pinned = false
			h.topAbs, h.topRow = 1, 0
			for i := 0; i < 25; i++ {
				h.handleKeyString("k")
			}
			if h.window.inFlight != histBefore {
				t.Fatalf("scrolling to the top edge did not fire a prepend (inFlight=%v)", h.window.inFlight)
			}
			h.reply(t, histRows(31, 10), true, true)

			cur := h.liveAt(h.focusedIndex())
			if cur == nil {
				t.Fatalf("after the batch landed the focus (%d) resolves to NO live entry, "+
					"so o is a silent no-op", h.focusedIndex())
			}
			if cur.lane != laneTask {
				t.Fatalf("the focus resolves to a non-task entry (lane=%v)", cur.lane)
			}
			if got := h.all[h.focusedIndex()].TaskID; got != "t1" {
				t.Fatalf("the focus is on task %q, want t1 — it moved off the slot", got)
			}
			if !cur.slotOpen {
				t.Fatal("the slot closed itself when the batch landed; slotOpen is the user's state, " +
					"not the row's, and a rebuild may not discard it")
			}
			// And o still toggles, on the rebuilt entry.
			h.handleKeyString("o")
			if cur.slotOpen {
				t.Fatal("o no longer toggles the slot after a rebuild")
			}
		})
	}
}

// The thinking block's expanded state rides along for the same reason, and is
// checked separately because `t` and `o` are different keys on different
// halves of the same entry.
func TestARebuildKeepsAThinkingBlockExpanded(t *testing.T) {
	h, idx := researchWindow(t, true)
	e := h.liveAt(idx)
	e.expanded = true

	h.requestHistory(histBefore, h.window.oldestSeq)
	h.reply(t, histRows(31, 10), true, true)

	cur := h.liveAt(h.focusedIndex())
	if cur == nil || !cur.expanded {
		t.Fatalf("the thinking block collapsed itself when a batch landed (entry=%v)", cur)
	}
}

// ---------------------------------------------------------------------------
// 8 — Suspend / Resume (Done-when #8)
// ---------------------------------------------------------------------------

// The round trip, field for field. Nothing calls Suspend or Resume yet (F12
// owns the lifecycle), so this is the proof the blob is real rather than
// declared.
func TestSuspendResumeRoundTripsTheThreeAnchorFields(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(1, 20), 20)
	h.pinned = false
	h.topAbs, h.topRow = 7, 4
	wantSeq := h.all[7].Seq

	raw, err := h.Suspend()
	if err != nil {
		t.Fatal(err)
	}

	var blob suspendBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	if blob.Version != suspendVersion {
		t.Fatalf("blob version is %d, want %d", blob.Version, suspendVersion)
	}
	if blob.Anchor == nil {
		t.Fatal("the blob carries no anchor")
	}
	if blob.Anchor.AnchorSeq != wantSeq {
		t.Fatalf("anchor_seq is %d, want the seq of the anchored message (%d)", blob.Anchor.AnchorSeq, wantSeq)
	}
	if blob.Anchor.RowOffset != 4 {
		t.Fatalf("row_offset is %d, want 4", blob.Anchor.RowOffset)
	}
	if blob.Anchor.PinnedToBottom {
		t.Fatal("pinned_to_bottom is true for an unpinned viewport")
	}

	// Resume into a fresh workspace, same session.
	h2 := newHistWorkspace(t, 40, 10)
	if err := h2.Resume(raw); err != nil {
		t.Fatalf("Resume returned an error, and it never may: %v", err)
	}
	h2.open(histRows(1, 20), 20)

	if h2.pinned {
		t.Fatal("Resume pinned a viewport that was not pinned at Suspend")
	}
	if got := h2.all[h2.topAbs].Seq; got != wantSeq {
		t.Fatalf("Resume landed on seq %d, want %d", got, wantSeq)
	}
	if h2.topRow != 4 {
		t.Fatalf("Resume landed at row offset %d, want 4", h2.topRow)
	}
}

// An anchor older than the opened tail is fetched, not abandoned.
func TestResumeFetchesAnAnchorOlderThanTheOpenedTail(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	if err := h.Resume(mustBlob(t, &blobAnchor{AnchorSeq: 7, RowOffset: 2})); err != nil {
		t.Fatal(err)
	}
	h.open(histRows(961, 40), 1000)

	if h.window.inFlight != histAnchored {
		t.Fatalf("Resume did not fetch the anchor's own window (inFlight=%v)", h.window.inFlight)
	}
	if got := h.lastSent(t)["after"]; got != 6 {
		t.Fatalf("the anchored fetch asked after=%v, want anchorSeq-1 (6) so the anchor sits at the "+
			"head of the window with content below it", got)
	}

	h.reply(t, histRows(7, 40), true, true)
	if h.pinned {
		t.Fatal("the anchored window pinned instead of landing on the anchor")
	}
	if got := h.all[h.topAbs].Seq; got != 7 {
		t.Fatalf("Resume landed on seq %d, want 7", got)
	}
	if h.topRow != 2 {
		t.Fatalf("row offset is %d, want 2", h.topRow)
	}
}

// Resume must DEGRADE, never fail. Four ways an anchor can be unusable, and
// all four land at the bottom with the session loaded.
func TestResumeDegradesToBottomAndNeverErrors(t *testing.T) {
	cases := []struct {
		name string
		blob []byte
	}{
		{"an empty blob, which is what F3's own Suspend wrote", nil},
		{"not JSON at all", []byte("{not json")},
		{"a version this build does not know", mustRaw(t, suspendBlob{Version: 99, Anchor: &blobAnchor{AnchorSeq: 5}})},
		{"an anchor newer than the session has", mustBlob(t, &blobAnchor{AnchorSeq: 99999, RowOffset: 3})},
		{"an anchor that was never addressable", mustBlob(t, &blobAnchor{AnchorSeq: 0, RowOffset: 3})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHistWorkspace(t, 40, 10)
			if err := h.Resume(tc.blob); err != nil {
				t.Fatalf("Resume errored on %s: %v — the restore path may never break a resume", tc.name, err)
			}
			h.open(histRows(1, 20), 20)

			if !h.pinned {
				t.Fatalf("%s did not degrade to the bottom", tc.name)
			}
			if h.entryCount() != 20 {
				t.Fatalf("the session failed to load: %d entries", h.entryCount())
			}
		})
	}
}

// pinnedToBottom resumes at the CURRENT newest, not the one that was newest
// at Suspend. A session can receive messages while unloaded — an F5
// background task, or an SSH peer — and resuming a pinned user to a stale
// anchor would strand them above unread content.
func TestResumePinnedLandsOnTheCurrentNewest(t *testing.T) {
	h := newHistWorkspace(t, 40, 10)
	h.open(histRows(1, 20), 20)
	h.pinned = true

	raw, err := h.Suspend()
	if err != nil {
		t.Fatal(err)
	}
	var blob suspendBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	if !blob.Anchor.PinnedToBottom {
		t.Fatal("pinned_to_bottom was not recorded")
	}

	// While unloaded the session grew by ten messages.
	h2 := newHistWorkspace(t, 40, 10)
	if err := h2.Resume(raw); err != nil {
		t.Fatal(err)
	}
	h2.open(histRows(1, 30), 30)

	if !h2.pinned {
		t.Fatal("a pinned resume did not pin")
	}
	if h2.window.newestSeq != 30 {
		t.Fatalf("a pinned resume landed with newest seq %d, want the CURRENT newest (30), "+
			"not the one that was newest at Suspend (20)", h2.window.newestSeq)
	}
	if len(h2.sent) != 0 {
		t.Fatalf("a pinned resume issued %d fetches, want none — the opened tail IS the bottom", len(h2.sent))
	}
}

func mustBlob(t *testing.T, a *blobAnchor) []byte {
	t.Helper()
	return mustRaw(t, suspendBlob{Version: suspendVersion, Anchor: a})
}

func mustRaw(t *testing.T, b suspendBlob) []byte {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// 9 — config (Done-when #9) and the invariant guards
// ---------------------------------------------------------------------------

func TestConfigGovernsTheBoundAndTheBatch(t *testing.T) {
	t.Run("ui.scrollback is the bound", func(t *testing.T) {
		h := newHistWorkspace(t, 250, 10)
		if got := h.historyBound(); got != 250 {
			t.Fatalf("bound is %d, want ui.scrollback (250)", got)
		}
	})

	t.Run("floored at three screenfuls", func(t *testing.T) {
		h := newHistWorkspace(t, 10, 2)
		h.viewRows = 40
		if got := h.historyBound(); got != 120 {
			t.Fatalf("bound is %d on a 40-row viewport, want 3 screenfuls (120) — a window smaller "+
				"than the viewport can never fill it", got)
		}
	})

	t.Run("floored at twice the batch", func(t *testing.T) {
		h := newHistWorkspace(t, 10, 100)
		h.viewRows = 1
		if got := h.historyBound(); got != 200 {
			t.Fatalf("bound is %d with a 100-message batch, want 200 — a bound under one batch "+
				"evicts what it just fetched and re-fetches it on the next step", got)
		}
	})

	t.Run("ui.historyBatch is the batch, and defaults", func(t *testing.T) {
		h := newHistWorkspace(t, 500, 37)
		h.open(histRows(1, 20), 1000)
		h.requestHistory(histBefore, 1)
		if got := h.lastSent(t)["limit"]; got != 37 {
			t.Fatalf("the fetch asked for limit=%v, want ui.historyBatch (37)", got)
		}

		d := newHistWorkspace(t, 500, 0)
		if got := d.historyBatch(); got != defaultHistoryBatch {
			t.Fatalf("an unset batch is %d, want the default %d", got, defaultHistoryBatch)
		}
	})
}

// The one invariant everything else rests on: `resident` is a contiguous run,
// never a set of ranges with gaps. A batch that does not abut the window is
// dropped rather than spliced, because the invariant is worth more than the
// batch.
func TestANonAdjacentBatchIsDroppedRatherThanSpliced(t *testing.T) {
	t.Run("overlapping below the head", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		h.open(histRows(21, 20), 1000)
		h.requestHistory(histBefore, 21)
		h.reply(t, histRows(18, 10), true, true) // 18..27 overlaps 21..40
		wantContiguous(t, h.seqs(), 21, 40)
	})

	t.Run("floating free above the tail", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		h.open(histRows(21, 20), 1000)
		h.window.ceilingSeq = 1000
		h.requestHistory(histAfter, 40)
		h.reply(t, histRows(40, 5), true, true) // starts AT the newest
		wantContiguous(t, h.seqs(), 21, 40)
	})

	t.Run("an empty batch records the end of history", func(t *testing.T) {
		h := newHistWorkspace(t, 40, 10)
		h.open(histRows(21, 20), 1000)
		h.requestHistory(histBefore, 21)
		h.reply(t, nil, false, true)
		if h.window.moreBefore {
			t.Fatal("an empty batch must record that history has ended, or the edge check asks forever")
		}
		wantContiguous(t, h.seqs(), 21, 40)
	})
}

// A long scroll holds a flat number of rows AND a flat render cache. This is
// the unit-level shadow of Done-when #2's memlog proof: without eviction both
// grow with scroll depth on a session of any size.
func TestMemoryStaysFlatAcrossAFullTraverse(t *testing.T) {
	const total = 2000
	h := newHistWorkspace(t, 60, 20)
	h.open(histRows(total-59, 60), total)
	h.pinned = false
	h.topAbs, h.topRow = 0, 0

	bound := h.historyBound()
	for h.window.moreBefore {
		// Back to the top edge each round: the only place a prepend fires.
		h.topAbs, h.topRow = 0, 0
		oldest := h.window.oldestSeq
		h.requestHistory(histBefore, oldest)
		from := oldest - 20
		if from < 1 {
			from = 1
		}
		h.reply(t, histRows(from, oldest-from), from > 1, true)

		// Render the top screenful, which is what populates the cache.
		for i := 0; i < len(h.all) && i < 5; i++ {
			h.entryLines(i, 60, false)
		}
		if len(h.raw) > bound {
			t.Fatalf("resident rows %d exceed the bound %d at oldest=%d",
				len(h.raw), bound, h.window.oldestSeq)
		}
		if len(h.cache) > bound*cacheEvictFactor {
			t.Fatalf("render cache holds %d entries, bound is %d",
				len(h.cache), bound*cacheEvictFactor)
		}
		if len(h.cache) != len(h.cacheOrder) {
			t.Fatalf("cache and its order list disagree (%d vs %d) — forgetRendered left one behind",
				len(h.cache), len(h.cacheOrder))
		}
	}

	if h.window.oldestSeq != 1 {
		t.Fatalf("the traverse stopped at seq %d, want 1 — it did not reach the top", h.window.oldestSeq)
	}
	if len(h.raw) > bound {
		t.Fatalf("after a full traverse the window holds %d rows, bound is %d", len(h.raw), bound)
	}
}
