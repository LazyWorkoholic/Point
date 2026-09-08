package chat

import (
	"strings"
	"testing"
)

// S11b's tests: a reloaded transcript must render the way the live one it
// replaces did. Each block below is one of the session's "Done when"
// criteria, in order.

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func qRow(id, task, text string) wireMessage {
	return wireMessage{
		ID: id, Role: roleUser, TaskID: task,
		Content: wireContent{Text: text}, Timestamp: 1000,
	}
}

// aRow is an answer that finished cleanly. agent is what B12 records on
// every assistant row — never null, the session's current agent for a plain
// turn.
func aRow(id, task, agent, text string) wireMessage {
	return wireMessage{
		ID: id, Role: roleAssistant, TaskID: task,
		Content:    wireContent{Text: text},
		Invocation: &wireInvocation{Agent: agent},
		Outcome:    outcomeDone,
		Timestamp:  1042,
	}
}

func (m wireMessage) endedAs(outcome, code string) wireMessage {
	m.Outcome, m.ErrorCode = outcome, code
	m.Interrupted = outcome != outcomeDone
	return m
}

func loaded(t *testing.T, rows ...wireMessage) *Workspace {
	t.Helper()
	w := newStreamWorkspace(t)
	w.setSession(wireSession{ID: "s1", Messages: rows})
	return w
}

// roleSeq is the transcript's shape: "QA" for a question then its answer.
func roleSeq(w *Workspace) string {
	var b strings.Builder
	for i := 0; i < w.entryCount(); i++ {
		switch w.entryRole(i) {
		case roleUser:
			b.WriteByte('Q')
		default:
			// roleTool is a task slot, roleAssistant an inline reply —
			// both are answers as far as pairing is concerned.
			b.WriteByte('A')
		}
	}
	return b.String()
}

func entryText(t *testing.T, w *Workspace, i int) string {
	t.Helper()
	text, ok := w.entryText(i)
	if !ok {
		t.Fatalf("entry %d has no text", i)
	}
	return text
}

// rendered draws one entry wide enough that a footer stays on one line —
// `⚠ stopped at its limit — … [Retry ^R]` is the longest of them, and a test
// that split it would be asserting against the wrap, not the footer.
func rendered(w *Workspace, i int) string {
	return stripANSI(strings.Join(w.entryLines(i, 100, false), "\n"))
}

// ---------------------------------------------------------------------------
// Done when #1 — all four send patterns reload as Q,A,Q,A
// ---------------------------------------------------------------------------

// The four patterns are F5's own measured cases. Two of them reloaded
// correctly before this session and two split, and the point of pairing on
// task_id is that the DB order they arrive in stops mattering at all: the
// same four exchanges are asserted here in both the ordered and the split
// row order, and both must come out identical.
func TestPairsEverySendPatternIntoQAQA(t *testing.T) {
	cases := []struct {
		name string
		rows []wireMessage
	}{
		{
			// Sequential, and queued: each answer landed before the next
			// question. Correct before S11b too — by accident of timing.
			name: "one at a time, or queued",
			rows: []wireMessage{
				qRow("u1", "t1", "first"),
				aRow("a1", "t1", "general", "first answer"),
				qRow("u2", "t2", "second"),
				aRow("a2", "t2", "general", "second answer"),
			},
		},
		{
			// Two @research fired together: both questions persist before
			// either answer. Q,Q,A,A on disk.
			name: "two research fired together",
			rows: []wireMessage{
				qRow("u1", "t1", "first"),
				qRow("u2", "t2", "second"),
				aRow("a1", "t1", "research", "first answer"),
				aRow("a2", "t2", "research", "second answer"),
			},
		},
		{
			// Plain + research together. F5 measured the PLAIN question
			// separating from its answer here too, which is why no
			// lane-keyed heuristic could have worked.
			name: "plain and research fired together",
			rows: []wireMessage{
				qRow("u1", "t1", "first"),
				qRow("u2", "t2", "second"),
				aRow("a1", "t1", "general", "first answer"),
				aRow("a2", "t2", "research", "second answer"),
			},
		},
		{
			// The answers came back in the opposite order to the sends.
			// Nothing about position can pair these.
			name: "answers arrive reversed",
			rows: []wireMessage{
				qRow("u1", "t1", "first"),
				qRow("u2", "t2", "second"),
				aRow("a2", "t2", "research", "second answer"),
				aRow("a1", "t1", "research", "first answer"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := loaded(t, tc.rows...)
			if got := roleSeq(w); got != "QAQA" {
				t.Fatalf("reloaded as %q, want QAQA", got)
			}
			// Not merely the right shape: the right PAIRS. An answer under
			// the wrong question is the failure this whole session exists
			// to prevent, and it passes a shape check.
			for _, pair := range [][2]string{
				{"first", "first answer"},
				{"second", "second answer"},
			} {
				qi := -1
				for i := 0; i < w.entryCount(); i++ {
					// The QUESTION only. An answer's own text contains the
					// question's words too, and matching one would assert
					// against the row below the one meant.
					if w.entryRole(i) == roleUser && strings.Contains(entryText(t, w, i), pair[0]) {
						qi = i
						break
					}
				}
				if qi < 0 {
					t.Fatalf("question %q is not in the reloaded transcript at all", pair[0])
				}
				if got := entryText(t, w, qi+1); got != pair[1] {
					t.Fatalf("the answer under %q is %q, want %q", pair[0], got, pair[1])
				}
			}
		})
	}
}

// Pairing must never reach across to an answer that is not this question's,
// however inviting the position. Two questions, one answer, and the answer
// belongs to the SECOND: the first must stay unanswered rather than
// swallowing the row sitting next to it.
func TestAnAnswerIsNeverAttachedToTheWrongQuestion(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "the unanswered one"),
		qRow("u2", "t2", "the answered one"),
		aRow("a2", "t2", "general", "the only answer"),
	)
	if got := roleSeq(w); got != "QQA" {
		t.Fatalf("reloaded as %q, want QQA — the answer moved to the wrong question", got)
	}
	if got := entryText(t, w, 2); got != "the only answer" {
		t.Fatalf("entry 2 is %q", got)
	}
}

// Rows written before B12's migration carry no task_id at all. They must
// keep their database position exactly — pairing is an addition, not a
// rewrite of every transcript ever stored.
func TestRowsWithoutATaskIDKeepTheirOrder(t *testing.T) {
	rows := []wireMessage{
		{ID: "u1", Role: roleUser, Content: wireContent{Text: "q1"}},
		{ID: "u2", Role: roleUser, Content: wireContent{Text: "q2"}},
		{ID: "a1", Role: roleAssistant, Content: wireContent{Text: "a1"}},
	}
	got := pairReloaded(rows)
	if len(got) != len(rows) {
		t.Fatalf("pairing changed the row count: %d -> %d", len(rows), len(got))
	}
	for i := range rows {
		if got[i].ID != rows[i].ID {
			t.Fatalf("row %d is %s, want %s — an untagged row moved", i, got[i].ID, rows[i].ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Done when #2 and #4 — slots come back, and only for background agents
// ---------------------------------------------------------------------------

// The rule is BACKGROUND (max_steps > 1), not "not the default agent".
// filler-explainer is the case that separates them: it is a non-default
// agent that the backend runs INLINE, so live never draws it as a slot and
// neither may reload.
func TestOnlyABackgroundAgentReloadsAsASlot(t *testing.T) {
	cases := []struct {
		agent string
		slot  bool
	}{
		{"research", true},
		{"general", false},
		{"filler-explainer", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run("agent="+tc.agent, func(t *testing.T) {
			w := loaded(t,
				qRow("u1", "t1", "a question"),
				aRow("a1", "t1", tc.agent, "the answer"),
			)
			out := rendered(w, 1)
			isSlot := strings.Contains(out, "✓ ") && strings.Contains(out, " done ")
			if isSlot != tc.slot {
				t.Fatalf("agent %q rendered slot=%v, want %v:\n%s", tc.agent, isSlot, tc.slot, out)
			}
			if !tc.slot && !strings.Contains(out, "the answer") {
				t.Fatalf("a plain reply lost its text:\n%s", out)
			}
		})
	}
}

// A reloaded slot has to behave like a live one, not merely look like one:
// n/p reaches it, o expands it, and expanding shows the answer that the
// collapsed line was hiding.
func TestAReloadedSlotFocusesAndExpands(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "a question"),
		aRow("a1", "t1", "research", "the full research answer"),
	)
	w.viewRows, w.viewWidth = 20, 70

	collapsed := rendered(w, 1)
	if strings.Contains(collapsed, "the full research answer") {
		t.Fatalf("a reloaded slot is not collapsed:\n%s", collapsed)
	}
	// The clock spans the question to the answer — the same interval the
	// live slot counted up. qRow stamps 1000, aRow 1042.
	if !strings.Contains(collapsed, "0:42") {
		t.Fatalf("the slot clock is not question-to-answer:\n%s", collapsed)
	}

	// n/p focus: moveFocus must be able to land on it, and liveAt must
	// answer for it — that is what o, t and ^R all resolve through.
	w.focus = 1
	if w.liveAt(w.focusedIndex()) == nil {
		t.Fatal("a focused reloaded slot is invisible to liveAt: o, t and ^R would all be inert")
	}

	w.handleKeyString("o")
	expanded := rendered(w, 1)
	if !strings.Contains(expanded, "the full research answer") {
		t.Fatalf("o did not expand a reloaded slot:\n%s", expanded)
	}
	w.handleKeyString("o")
	if again := rendered(w, 1); strings.Contains(again, "the full research answer") {
		t.Fatalf("o did not collapse it again:\n%s", again)
	}
}

// ---------------------------------------------------------------------------
// Done when #3 — the footer, and the partial text under it
// ---------------------------------------------------------------------------

func TestTerminalOutcomesReloadWithTheirFooter(t *testing.T) {
	cases := []struct {
		outcome string
		code    string
		want    string
	}{
		{outcomeCancelled, "", "⚠ interrupted"},
		{outcomeFailed, "provider_error", "✗ failed"},
		{outcomeTimedOut, boundsExceededCode, "⚠ stopped at its limit"},
		{outcomeBoundsExceeded, boundsExceededCode, "⚠ stopped at its limit"},
	}
	for _, tc := range cases {
		t.Run(tc.outcome, func(t *testing.T) {
			w := loaded(t,
				qRow("u1", "t1", "a question"),
				aRow("a1", "t1", "general", "as far as it got").endedAs(tc.outcome, tc.code),
			)
			out := rendered(w, 1)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("outcome %q reloaded without its footer (want %q):\n%s", tc.outcome, tc.want, out)
			}
			// The footer says how it ended. It never hides what arrived.
			if !strings.Contains(out, "as far as it got") {
				t.Fatalf("outcome %q lost its partial text:\n%s", tc.outcome, out)
			}
			if !strings.Contains(out, "[Retry ^R]") {
				t.Fatalf("outcome %q offers no retry:\n%s", tc.outcome, out)
			}
		})
	}
}

// A reloaded failure names the same error the live footer named. B12
// persists it in metadata["error"] — the same string TaskFailed's payload
// carried — so there is no reason for the two paths to say different things.
func TestAReloadedFailureNamesTheRealError(t *testing.T) {
	row := aRow("a1", "t1", "general", "partial").endedAs(outcomeFailed, "provider_error")
	row.Metadata = map[string]any{"reason": "failed", "error": "ollama: connection refused"}

	w := loaded(t, qRow("u1", "t1", "a question"), row)
	out := rendered(w, 1)
	if !strings.Contains(out, "ollama: connection refused") {
		t.Fatalf("the reloaded footer lost the error the live one showed:\n%s", out)
	}
	if strings.Contains(out, "the task failed") {
		t.Fatalf("the fallback wording won over a real error message:\n%s", out)
	}
}

// A clean reply must not grow a footer, and a row written before migration
// v5 (outcome NULL) must not be assumed to have failed — "no footer" is the
// honest rendering of an ending nobody recorded.
func TestACleanOrUnrecordedEndingHasNoFooter(t *testing.T) {
	for _, outcome := range []string{outcomeDone, ""} {
		w := loaded(t,
			qRow("u1", "t1", "a question"),
			aRow("a1", "t1", "general", "the answer").endedAs(outcome, ""),
		)
		out := rendered(w, 1)
		for _, marker := range []string{"⚠", "✗", "[Retry ^R]"} {
			if strings.Contains(out, marker) {
				t.Fatalf("outcome %q invented a %q footer:\n%s", outcome, marker, out)
			}
		}
	}
}

// The footer's [Retry ^R] has to resolve to something. F5's rule is that an
// affordance with no key behind it is worse than none.
func TestRetryResolvesToAReloadedFailure(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "the question"),
		aRow("a1", "t1", "research", "partial").endedAs(outcomeFailed, "provider_error"),
	)
	target := w.retryTarget()
	if target == nil {
		t.Fatal("^R on a reloaded failure has no target, but its footer offers one")
	}
	if target.userText != "@research the question" {
		t.Fatalf("retry would re-send %q, want the question with its @agent prefix back", target.userText)
	}
}

// ---------------------------------------------------------------------------
// Done when #5 — a task that produced nothing
// ---------------------------------------------------------------------------

// B12's accepted limitation: a task cancelled or failed before its first
// token persists NO assistant row, so its outcome lives only on the tasks
// row. The question must still be present, must not be attached to anyone
// else's answer, and must carry the footer the live session showed.
func TestAQuestionWhoseTaskProducedNothing(t *testing.T) {
	t.Run("before the tasks join, it is a question with no reply", func(t *testing.T) {
		w := loaded(t,
			qRow("u1", "t1", "cancelled before any output"),
			qRow("u2", "t2", "answered"),
			aRow("a2", "t2", "general", "the answer"),
		)
		if got := roleSeq(w); got != "QQA" {
			t.Fatalf("reloaded as %q, want QQA", got)
		}
		if got := entryText(t, w, 2); got != "the answer" {
			t.Fatalf("the unanswered question took someone else's answer: entry 2 = %q", got)
		}
	})

	t.Run("the tasks join rebuilds its footer", func(t *testing.T) {
		w := loaded(t,
			qRow("u1", "t1", "cancelled before any output"),
			qRow("u2", "t2", "answered"),
			aRow("a2", "t2", "general", "the answer"),
		)
		w.applyTaskRows(map[string]taskRow{
			"t1": {ID: "t1", State: taskCancelled, Agent: "research", CreatedAt: 900},
			"t2": {ID: "t2", State: taskCompleted, Agent: "general", CreatedAt: 1000},
		})

		if got := roleSeq(w); got != "QAQA" {
			t.Fatalf("after the join the transcript is %q, want QAQA", got)
		}
		out := rendered(w, 1)
		if !strings.Contains(out, "⚠") || !strings.Contains(out, "interrupted") {
			t.Fatalf("the reconstructed reply carries no interrupted footer:\n%s", out)
		}
		// Reconstructed, not invented: it has no answer text, because
		// there was no answer.
		if got := entryText(t, w, 1); got != "" {
			t.Fatalf("the reconstructed reply invented text %q", got)
		}
		if got := entryText(t, w, 3); got != "the answer" {
			t.Fatalf("the real answer moved: entry 3 = %q", got)
		}
	})

	t.Run("a task still running adds nothing", func(t *testing.T) {
		w := loaded(t, qRow("u1", "t1", "still going"))
		w.applyTaskRows(map[string]taskRow{
			"t1": {ID: "t1", State: "running", Agent: "research"},
		})
		if got := roleSeq(w); got != "Q" {
			t.Fatalf("a running task grew a reply: %q", got)
		}
		// The real damage a non-terminal entry would do is not visual.
		if w.Streaming() {
			t.Fatal("a reloaded row made Streaming() true: the watchdog would re-arm forever")
		}
	})

	t.Run("a completed task that wrote nothing adds nothing", func(t *testing.T) {
		w := loaded(t, qRow("u1", "t1", "asked"))
		w.applyTaskRows(map[string]taskRow{
			"t1": {ID: "t1", State: taskCompleted, Agent: "general"},
		})
		if got := roleSeq(w); got != "Q" {
			t.Fatalf("a clean task with no message asserted an answer: %q", got)
		}
	})
}

// A failed ListTasks must cost a footer, never the transcript.
func TestAFailedTasksJoinLeavesTheTranscriptAlone(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "a question"),
		aRow("a1", "t1", "general", "the answer"),
	)
	before := roleSeq(w)
	w.applyTaskRows(nil)
	if after := roleSeq(w); after != before {
		t.Fatalf("an empty tasks answer changed the transcript: %q -> %q", before, after)
	}
	if w.loadErr != "" {
		t.Fatalf("the tasks join set loadErr = %q, blanking a correct transcript", w.loadErr)
	}
}

// ---------------------------------------------------------------------------
// Done when #6 — no retry label
// ---------------------------------------------------------------------------

// B12 persists no retry link — task_id is the PREREQUISITE for one, not the
// link itself — so a retried question still reloads as two questions. That
// is the honest rendering, and inventing a label from adjacency is the bug
// F5 removed.
func TestARetriedQuestionStillReloadsAsTwoQuestions(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "the same question"),
		qRow("u2", "t2", "the same question"),
		aRow("a2", "t2", "general", "the answer"),
	)
	if got := roleSeq(w); got != "QQA" {
		t.Fatalf("reloaded as %q, want QQA — both questions verbatim", got)
	}
	for i := 0; i < w.entryCount(); i++ {
		if strings.Contains(rendered(w, i), "retried") {
			t.Fatalf("entry %d invented a retried label", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Done when #7 — the live path is untouched
// ---------------------------------------------------------------------------

// A decorated reloaded entry must be invisible to every live-lane predicate.
// All of them are written as "scan w.live", and none of them may start
// seeing history: a reloaded failure that counted as an active turn would
// queue the next plain send behind a task that ended days ago.
func TestReloadedEntriesAreInvisibleToTheLiveLanes(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "a question"),
		aRow("a1", "t1", "research", "partial").endedAs(outcomeCancelled, ""),
	)
	if w.Streaming() {
		t.Fatal("Streaming() true with nothing sent this session")
	}
	if w.activeTurn() != nil {
		t.Fatal("a reloaded entry is holding the turn lane")
	}
	if w.HasPending() || w.inputLocked() {
		t.Fatal("a reloaded entry locked the input")
	}
}

// A live send always wins ^R over a reloaded one: "the most recent
// retryable" has to keep meaning the most recent.
func TestALiveFailureOutranksAReloadedOneForRetry(t *testing.T) {
	w := loaded(t,
		qRow("u1", "t1", "the old question"),
		aRow("a1", "t1", "general", "partial").endedAs(outcomeFailed, "provider_error"),
	)
	live := &liveEntry{kind: liveReply, status: statusFailed, userText: "the new question"}
	w.live = append(w.live, live)

	if got := w.retryTarget(); got != live {
		t.Fatalf("^R picked a reloaded entry over a live one: %+v", got)
	}
}
