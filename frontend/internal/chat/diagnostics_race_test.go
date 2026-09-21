package chat

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// A DiagnosticsMsg carrying a STALE Seq must never overwrite state a NEWER
// one already applied — this is the field bug, reduced to its actual
// mechanism. root.go's pushDiagnostics returns a tea.Cmd, and bubbletea
// runs every Cmd in its own goroutine with no guarantee that an earlier
// push's goroutine finishes first; two pushes close together (a TaskDone
// envelope immediately followed by the ListTasks reply it triggered) can
// have their messages delivered to Update in either order. Delivering them
// out of order here, directly, is what proves the guard — not just that a
// realistic in-order sequence happens to work.
func TestDiagnosticsMsgIgnoresStaleSeq(t *testing.T) {
	w := &Workspace{theme: testTheme()}

	fresh := []app.TaskRow{{ID: "t3", Agent: "research", State: app.TaskStateRunning}}
	stale := []app.TaskRow{{ID: "t2", Agent: "research", State: app.TaskStateRunning}}

	// The NEWER push (Seq 2) arrives FIRST...
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 2, Tasks: fresh}})
	// ...then the OLDER one (Seq 1), delayed by its own goroutine, arrives
	// SECOND. Without the guard this silently wins because it arrived last.
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 1, Tasks: stale}})

	if len(w.tasks) != 1 || w.tasks[0].ID != "t3" {
		t.Fatalf("tasks = %+v, want the Seq-2 snapshot to survive a late Seq-1 delivery", w.tasks)
	}
}

// Two Diagnostics pushes must never partially apply — Seq 3 fully replaces
// Seq 2's picture, never a mix where one field is Seq 2's and another is
// Seq 3's, which a per-field comparison (instead of gating the whole
// message) could produce.
func TestDiagnosticsMsgAppliesNewerInOrder(t *testing.T) {
	w := &Workspace{theme: testTheme()}
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 1, Tasks: []app.TaskRow{{ID: "a"}}}})
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 2, Tasks: []app.TaskRow{{ID: "b"}}}})
	if len(w.tasks) != 1 || w.tasks[0].ID != "b" {
		t.Fatalf("tasks = %+v, want the Seq-2 snapshot applied", w.tasks)
	}
}

// drainIdle is how long drainOnce waits with NOTHING arriving before it
// concludes the rest of the cmds are never going to report. It is a
// ceiling on quiet, not a guess at how fast a cmd runs: every cmd is
// spawned concurrently and each message resets the clock, so a fast cmd
// is collected immediately no matter how generous this is. It has to be
// generous because the alternative is a flaky suite — at 50ms, and with
// cmds drained one at a time instead of together, this harness
// intermittently dropped pushDiagnostics's DiagnosticsMsg under -race and
// failed a test that had nothing wrong with it.
const drainIdle = 500 * time.Millisecond

// drainOnce runs cmd — and every cmd inside a tea.BatchMsg — concurrently,
// collecting whatever messages they produce. A real bubbletea Program
// executes every returned Cmd automatically; a bare r.Update(msg) call
// does not, so a message produced ONLY by a Cmd (pushDiagnostics's
// DiagnosticsMsg, batched alongside afterBackend's client.Next()) would
// otherwise never reach the workspace in a test.
//
// Concurrently, not one at a time, for a specific reason: client.Next()'s
// cmd blocks forever with no live backend behind it. Draining sequentially
// made every single call pay the full timeout waiting on that one cmd,
// which is what forced the timeout to be small enough to be flaky.
func drainOnce(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	out := make(chan tea.Msg, 64)

	var spawn func(tea.Cmd)
	spawn = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if msg == nil {
				return
			}
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, inner := range batch {
					spawn(inner)
				}
				return
			}
			out <- msg
		}()
	}
	spawn(cmd)

	var msgs []tea.Msg
	for {
		select {
		case m := <-out:
			msgs = append(msgs, m)
		case <-time.After(drainIdle):
			return msgs
		}
	}
}

// updateAndDrain is update() (root_toggle_test.go) plus draining any
// Cmd-produced messages one level deep — enough to deliver
// pushDiagnostics's DiagnosticsMsg, which a bare r.Update alone does not.
func updateAndDrain(t *testing.T, r *app.Root, msg tea.Msg) string {
	t.Helper()
	m, cmd := r.Update(msg)
	r = m.(*app.Root)
	for _, produced := range drainOnce(cmd) {
		m, _ = r.Update(produced)
		r = m.(*app.Root)
	}
	return r.View()
}

// TaskStarted must trigger an IMMEDIATE refresh while the fallback overlay
// is the visible surface — not just once tasks are already in flight when
// the overlay opens (TestOverlayTracksLiveTaskIdentityAcrossTransitions's
// setup), but for a brand-new task that starts AFTER the overlay has
// already settled into its "nothing running" state. refreshTasks,
// refreshTasksIfVisible, handleTaskListEnvelope, recompute and OpenOverlay
// are the same five functions for both surfaces, gated only by
// rightPaneVisible() — there is no branch anywhere that treats "the real
// pane is visible" differently from "the fallback overlay is visible" — so
// this pins that down as an executable claim, not just a code-reading one.
func TestTaskStartedRefreshesImmediatelyWhileOverlayIsTheVisibleSurface(t *testing.T) {
	th := testTheme()
	r, err := app.New(app.Options{Theme: th, Client: protocol.NewClient(protocol.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 80, Height: 30})

	var reqIDs []string
	r.ListTasksFn = func(env protocol.Envelope) bool {
		reqIDs = append(reqIDs, env.ID)
		return true
	}
	replyTo := func(id string, tasks []app.TaskRow) string {
		payload, _ := json.Marshal(map[string]any{"tasks": tasks})
		return updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{
			Type: protocol.EvTaskList, CorrID: id, Payload: payload,
		}})
	}

	// Open the overlay with nothing running, and let it settle: request 1,
	// answered empty.
	updateAndDrain(t, r, key("ctrl+w"))
	out := updateAndDrain(t, r, key("c"))
	if !strings.Contains(out, "Right Pane") {
		t.Fatalf("overlay did not open:\n%s", stripANSI(out))
	}
	if len(reqIDs) != 1 {
		t.Fatalf("opening the overlay sent %d requests, want 1", len(reqIDs))
	}
	out = replyTo(reqIDs[0], nil)
	if !strings.Contains(stripANSI(out), "no background tasks") {
		t.Fatalf("overlay settled state = %q, want \"no background tasks\"", stripANSI(out))
	}

	// A brand-new task starts. This must fire request 2 IMMEDIATELY —
	// before any TaskDone — while the overlay, not the real pane, is what
	// is on screen.
	updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		Type: protocol.EvTaskStarted, TaskID: "new1",
	}})
	if len(reqIDs) != 2 {
		t.Fatalf("TaskStarted while the overlay was open sent %d requests total, want 2 (no TaskDone happened yet)", len(reqIDs))
	}

	out = replyTo(reqIDs[1], []app.TaskRow{
		{ID: "new1", Agent: "research", State: app.TaskStateRunning},
	})
	if !strings.Contains(stripANSI(out), "⟳ research") {
		t.Fatalf("overlay after TaskStarted's reply = %q, want the new task showing running", stripANSI(out))
	}
}

// This is the full field repro, end to end through the real Root, chat
// Workspace and fallback overlay: three @research tasks are ALREADY
// running before the overlay ever opens (order B — order A, opening the
// overlay first, is correctly blocked by requiring Esc before a send, and
// is not this bug). It then walks three real terminal-state transitions —
// task A finishes while B runs, B finishes while C runs, C finishes — and
// asserts the overlay's displayed running-task identity AND its elapsed
// time track whichever task is ACTUALLY running at each step, not a
// snapshot latched at open time.
func TestOverlayTracksLiveTaskIdentityAcrossTransitions(t *testing.T) {
	th := testTheme()
	r, err := app.New(app.Options{Theme: th, Client: protocol.NewClient(protocol.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 80, Height: 30})

	var lastReqID string
	r.ListTasksFn = func(env protocol.Envelope) bool {
		lastReqID = env.ID
		return true
	}
	reply := func(tasks []app.TaskRow) string {
		payload, err := json.Marshal(map[string]any{"tasks": tasks})
		if err != nil {
			t.Fatal(err)
		}
		if lastReqID == "" {
			t.Fatal("no ListTasks request is outstanding to reply to")
		}
		out := updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{
			Type: protocol.EvTaskList, CorrID: lastReqID, Payload: payload,
		}})
		lastReqID = ""
		return out
	}
	lifecycleTriggersRefresh := func() {
		lastReqID = ""
		updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{Type: protocol.EvTaskDone}})
		if lastReqID == "" {
			t.Fatal("a terminal event did not trigger a follow-up ListTasks request")
		}
	}
	secondsAgo := func(n int) float64 { return float64(time.Now().Add(-time.Duration(n) * time.Second).Unix()) }

	// Open the overlay: A, B and C already running/queued in the
	// background (order B). This alone sends the first request.
	updateAndDrain(t, r, key("ctrl+w"))
	out := updateAndDrain(t, r, key("c"))
	if !strings.Contains(out, "Right Pane") {
		t.Fatalf("overlay did not open:\n%s", stripANSI(out))
	}

	// Step 1: A already finished, B has been running 90s, C is queued.
	out = reply([]app.TaskRow{
		{ID: "A", Agent: "research", State: app.TaskStateCompleted},
		{ID: "B", Agent: "research", State: app.TaskStateRunning, StartedAt: secondsAgo(90)},
		{ID: "C", Agent: "research", State: app.TaskStateQueued},
	})
	plain := stripANSI(out)
	if !strings.Contains(plain, "⟳ research") {
		t.Fatalf("step 1: want B showing as running, got:\n%s", plain)
	}
	// A BAND, not an exact reading: the rendered clock derives from
	// time.Now(), so real wall time passes between building the fixture
	// and rendering it — under -race that was enough to turn an exact
	// "1:30" assertion into a spurious failure at "1:31". The claim that
	// matters is that the clock tracks B's REAL StartedAt (~90s ago)
	// instead of restarting from zero when the overlay first drew it,
	// which is exactly what the field report described ("0:05" for a task
	// already running over a minute). A band asserts that, and asserts
	// nothing about how fast the test itself runs.
	if got, ok := elapsedSeconds(plain); !ok || got < 85 || got > 150 {
		t.Fatalf("step 1: elapsed reads %ds (parsed=%v), want ~90s from B's real StartedAt, not a clock restarted at render time:\n%s", got, ok, plain)
	}
	if strings.Count(plain, "◌ research") != 1 {
		t.Fatalf("step 1: want exactly one queued row (C), got:\n%s", plain)
	}

	// Step 2: B finishes, C starts running (fresh — 0 elapsed).
	lifecycleTriggersRefresh()
	out = reply([]app.TaskRow{
		{ID: "A", Agent: "research", State: app.TaskStateCompleted},
		{ID: "B", Agent: "research", State: app.TaskStateCompleted},
		{ID: "C", Agent: "research", State: app.TaskStateRunning, StartedAt: secondsAgo(0)},
	})
	plain = stripANSI(out)
	if !strings.Contains(plain, "⟳ research") {
		t.Fatalf("step 2: want C showing as running, got:\n%s", plain)
	}
	// C started just now, so its clock must read near zero — the point
	// being that the running row swapped to a DIFFERENT task rather than
	// carrying B's ~90s clock forward.
	if got, ok := elapsedSeconds(plain); !ok || got > 30 {
		t.Fatalf("step 2: elapsed reads %ds (parsed=%v), want ~0s — a FRESH task, not B's stale clock carried over:\n%s", got, ok, plain)
	}
	if strings.Count(plain, "✓ research") != 2 {
		t.Fatalf("step 2: want both A and B showing done, got:\n%s", plain)
	}

	// Step 3: C finishes. Nothing is running any more.
	lifecycleTriggersRefresh()
	out = reply([]app.TaskRow{
		{ID: "A", Agent: "research", State: app.TaskStateCompleted},
		{ID: "B", Agent: "research", State: app.TaskStateCompleted},
		{ID: "C", Agent: "research", State: app.TaskStateCompleted},
	})
	plain = stripANSI(out)
	if strings.Contains(plain, "⟳") {
		t.Fatalf("step 3: want nothing running any more, got:\n%s", plain)
	}
	if strings.Count(plain, "✓ research") != 3 {
		t.Fatalf("step 3: want all three showing done, got:\n%s", plain)
	}
}

// elapsedSeconds pulls the first "M:SS" clock out of a rendered frame and
// returns it in seconds. Used to assert an elapsed BAND rather than an
// exact reading — see its callers for why an exact reading is a wall-clock
// assertion in disguise.
func elapsedSeconds(plain string) (int, bool) {
	m := regexp.MustCompile(`(\d+):(\d{2})`).FindStringSubmatch(plain)
	if m == nil {
		return 0, false
	}
	mins, err1 := strconv.Atoi(m[1])
	secs, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return mins*60 + secs, true
}

// The field bug, finally: the overlay showed nothing until the FIRST task
// completed. Not a render problem and not a data-ordering one — a missed
// visibility TRANSITION.
//
// The sequence is forced by the app itself. Sending requires the overlay
// closed (esc), so any task started from a narrow terminal starts while
// nothing that shows it is on screen — and refreshTasks deliberately sends
// nothing while hidden. Reopening the overlay is therefore the ONLY chance
// to learn those tasks exist. taskCardWasVisible was updated only inside
// recompute(), and three of the five paths that close an overlay (esc,
// click-outside, closeOverlayIfTooSmall) never call it — so the flag stayed
// true across the close, the reopen registered as "no transition", and
// nothing refreshed. The next refresh came from whatever lifecycle event
// fired next while the overlay happened to be open: the first TaskDone.
//
// A test that only ever opens the overlay for the first time cannot see
// this, which is why four earlier reproduction attempts all passed.
func TestReopeningTheOverlayRefreshesWorkStartedWhileItWasClosed(t *testing.T) {
	th := testTheme()
	r, err := app.New(app.Options{Theme: th, Client: protocol.NewClient(protocol.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))
	updateAndDrain(t, r, tea.WindowSizeMsg{Width: 80, Height: 30})

	var reqIDs []string
	r.ListTasksFn = func(env protocol.Envelope) bool {
		reqIDs = append(reqIDs, env.ID)
		return true
	}
	reply := func(id string, tasks []app.TaskRow) string {
		payload, err := json.Marshal(map[string]any{"tasks": tasks})
		if err != nil {
			t.Fatal(err)
		}
		return updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{
			Type: protocol.EvTaskList, CorrID: id, Payload: payload,
		}})
	}

	// Open once and settle: nothing running yet.
	updateAndDrain(t, r, key("ctrl+w"))
	updateAndDrain(t, r, key("c"))
	if len(reqIDs) != 1 {
		t.Fatalf("first open sent %d requests, want 1", len(reqIDs))
	}
	reply(reqIDs[0], nil)

	// Close it — which is what sending a message requires.
	updateAndDrain(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	// Work starts while it is closed. Sending nothing here is correct:
	// Done-when #6 is that a hidden pane costs no requests.
	updateAndDrain(t, r, protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		Type: protocol.EvTaskStarted, TaskID: "started-while-closed",
	}})
	if len(reqIDs) != 1 {
		t.Fatalf("a hidden pane sent %d requests, want none while hidden", len(reqIDs))
	}

	// Reopen: the only chance to learn that task exists.
	updateAndDrain(t, r, key("ctrl+w"))
	view := updateAndDrain(t, r, key("c"))
	if len(reqIDs) < 2 {
		t.Fatalf("reopening sent no fresh request (%d total) — work started while closed can never appear:\n%s",
			len(reqIDs), stripANSI(view))
	}

	view = reply(reqIDs[1], []app.TaskRow{
		{ID: "started-while-closed", Agent: "research", State: app.TaskStateRunning},
	})
	if !strings.Contains(stripANSI(view), "⟳ research") {
		t.Errorf("reopened overlay does not show the task that started while it was closed:\n%s", stripANSI(view))
	}
}

// Two independent tasks in flight at once must never mix their streamed
// content. A background @research holds one corr_id/task_id; a plain
// message sent immediately after holds its own. Every delta must land on
// the entry tagged with ITS OWN ids, and on no other.
//
// This was written to chase a field report of one task's slot showing both
// tasks' answers concatenated. It does not reproduce it — routing is
// clean — so it stands as a guard on the property rather than a fix.
func TestConcurrentTasksNeverMixTheirStreamedContent(t *testing.T) {
	w := &Workspace{theme: testTheme(), focus: -1, sessionID: "s1"}
	w.input.buf = newInputBuffer()
	w.sendMsgFn = func(e *liveEntry, payload map[string]any) tea.Cmd { return nil }

	delta := func(corrID, taskID, text string) protocol.Envelope {
		p, _ := json.Marshal(map[string]any{"data": map[string]any{"text": text}})
		return protocol.Envelope{Type: protocol.EvTextDelta, CorrID: corrID, TaskID: taskID, Payload: p}
	}
	started := func(corrID, taskID string, background bool) protocol.Envelope {
		p, _ := json.Marshal(map[string]any{"background": background})
		return protocol.Envelope{Type: protocol.EvTaskStarted, CorrID: corrID, TaskID: taskID, Payload: p}
	}

	// A background @research starts streaming.
	w.startSend("explain merge sort", "research")
	research := w.live[len(w.live)-1]
	w.handleStreamEvent(started(research.requestID, "TASK-R", true))
	w.handleStreamEvent(delta(research.requestID, "TASK-R", "SORTING "))

	// A plain message goes out while it is still in flight. onSend, not
	// startSend, so the real queue-or-send decision runs: an @agent task
	// must NOT block a plain turn (F5's "agent tasks never enter this
	// queue").
	const beesText = "write an essay on bees"
	w.input.buf.setText(beesText, len(beesText))
	w.onSend()
	if w.queued != nil {
		t.Fatal("a plain message queued behind a BACKGROUND task — the two lanes are supposed to be independent")
	}
	bees := w.live[len(w.live)-1]
	if bees.requestID == research.requestID {
		t.Fatal("the two sends share a requestID — every stream event would be ambiguous")
	}

	// Interleave both streams, ending with more of the FIRST one.
	w.handleStreamEvent(started(bees.requestID, "TASK-B", false))
	w.handleStreamEvent(delta(bees.requestID, "TASK-B", "BEES "))
	w.handleStreamEvent(delta(research.requestID, "TASK-R", "MORE-SORTING "))

	if strings.Contains(research.text, "BEES") {
		t.Errorf("research entry absorbed the other task's content: %q", research.text)
	}
	if strings.Contains(bees.text, "SORTING") {
		t.Errorf("plain-message entry absorbed the research task's content: %q", bees.text)
	}
	if research.text != "SORTING MORE-SORTING " {
		t.Errorf("research.text = %q, want only its own deltas", research.text)
	}
	if bees.text != "BEES " {
		t.Errorf("bees.text = %q, want only its own deltas", bees.text)
	}
}
