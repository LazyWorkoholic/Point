package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ---------------------------------------------------------------------------
// Grouping and ordering
// ---------------------------------------------------------------------------

// F09's mockup order is Running · Queued · Finished, and array.Menu draws a
// section header every time an item's Section changes IN THE ORDER GIVEN
// (menu.go's sectionedRows). So the sort is the rendered grouping, and a
// wrong sort does not mis-order rows — it produces repeated headers.
func TestTraySectionsRunningQueuedFinishedInOrder(t *testing.T) {
	rows := SortTaskRows([]TaskRow{
		{ID: "done-old", State: TaskStateCompleted, FinishedAt: 100},
		{ID: "queued", State: TaskStateQueued, CreatedAt: 50},
		{ID: "done-new", State: TaskStateCompleted, FinishedAt: 200},
		{ID: "running", State: TaskStateRunning, CreatedAt: 60},
		{ID: "failed", State: TaskStateFailed, FinishedAt: 150},
	})

	var gotIDs, gotSections []string
	for _, r := range rows {
		gotIDs = append(gotIDs, r.ID)
		gotSections = append(gotSections, traySection(r.State))
	}

	wantIDs := []string{"running", "queued", "done-new", "failed", "done-old"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("order = %v, want %v", gotIDs, wantIDs)
	}

	// Each section must appear as ONE contiguous run — the property that
	// makes the headers correct.
	seen := map[string]bool{}
	prev := ""
	for _, s := range gotSections {
		if s != prev {
			if seen[s] {
				t.Fatalf("section %q appears in two separate runs: %v", s, gotSections)
			}
			seen[s] = true
			prev = s
		}
	}
}

// The finished group is ordered most-recently-finished first: that is the one
// a user opening the tray has come to look at.
func TestTrayFinishedGroupIsNewestFirst(t *testing.T) {
	rows := SortTaskRows([]TaskRow{
		{ID: "a", State: TaskStateCompleted, FinishedAt: 10},
		{ID: "b", State: TaskStateCompleted, FinishedAt: 30},
		{ID: "c", State: TaskStateCancelled, FinishedAt: 20},
	})
	if rows[0].ID != "b" || rows[1].ID != "c" || rows[2].ID != "a" {
		t.Errorf("got %s,%s,%s want b,c,a", rows[0].ID, rows[1].ID, rows[2].ID)
	}
}

// Done-when #1 begins with the tray being openable at all. An empty snapshot
// must produce a row that SAYS so — a popup with zero items renders an empty
// box with a blank footer indicator, which is indistinguishable from a tray
// that failed to load.
func TestTrayItemsEmptySnapshotSaysSo(t *testing.T) {
	items := trayItems(nil)
	if len(items) != 1 {
		t.Fatalf("len = %d, want exactly one explanatory row", len(items))
	}
	if !items[0].IsDisabled() {
		t.Error("the empty row must be disabled — there is nothing to choose")
	}
	if !strings.Contains(items[0].GetTitle(), "no background tasks") {
		t.Errorf("title = %q", items[0].GetTitle())
	}
}

// The item id is the task id, which is what onTraySelect resolves back to a
// row. Breaking this silently turns every ⏎ into a no-op.
func TestTrayItemIDIsTheTaskID(t *testing.T) {
	items := trayItems([]TaskRow{{ID: "task-xyz", State: TaskStateCompleted, Agent: "research"}})
	if items[0].GetID() != "task-xyz" {
		t.Errorf("id = %q, want the task id", items[0].GetID())
	}
}

// TaskLabel prefers the agent over the kind: a card keyed on kind showed
// "chat" for an @research task, which is F6's own recorded finding.
func TestTaskLabelPrefersAgentOverKind(t *testing.T) {
	if got := TaskLabel(TaskRow{Agent: "research", Kind: "chat"}); got != "research" {
		t.Errorf("got %q, want research", got)
	}
	if got := TaskLabel(TaskRow{Kind: "chat"}); got != "chat" {
		t.Errorf("got %q, want chat", got)
	}
	if got := TaskLabel(TaskRow{}); got != "task" {
		t.Errorf("got %q, want task", got)
	}
}

// A failure's note carries the backend's own error_code. This is the one
// label in F09's mockup ("failed: tool_timeout") that the wire actually
// supplies, so getting it wrong would be dropping real information.
func TestTrayNoteCarriesTheErrorCode(t *testing.T) {
	got := trayNote(TaskRow{State: TaskStateFailed, ErrorCode: "tool_timeout"})
	if got != "failed: tool_timeout" {
		t.Errorf("got %q", got)
	}
	if got := trayNote(TaskRow{State: TaskStateFailed}); got != "failed" {
		t.Errorf("no code: got %q, want a bare 'failed', never 'failed: '", got)
	}
}

// A duration needs both endpoints. A missing or inverted pair must render
// nothing rather than a fabricated 0:00 — an unknown cost and a zero cost are
// different claims.
func TestTaskDurationRefusesToInventOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  TaskRow
		want string
	}{
		{"both present", TaskRow{StartedAt: 1000, FinishedAt: 1063}, "1:03"},
		{"no start", TaskRow{FinishedAt: 1063}, ""},
		{"no finish", TaskRow{StartedAt: 1000}, ""},
		{"inverted", TaskRow{StartedAt: 1063, FinishedAt: 1000}, ""},
	} {
		if got := TaskDuration(tc.row); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Keeping the snapshot live while the tray is the only thing showing it
// ---------------------------------------------------------------------------

// The bug this guards is the one rightPaneVisible already had to learn once:
// refreshTasks sends nothing while no surface displays its answer, so a tray
// open over a hidden right pane would show a snapshot frozen at open time —
// a running task that never moves to Finished in the list whose entire job
// is to say that it did.
func TestTrayCountsAsASurfaceForTheTaskSnapshot(t *testing.T) {
	r := &Root{log: discardLogger()}
	if r.tasksOnScreen() {
		t.Fatal("nothing open: tasksOnScreen must be false")
	}

	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(nil)}}, MinW: 50, MinH: 12})
	tray := &taskTray{Popup: popup}
	r.tray = trayState{popup: popup}
	r.overlay = tray

	if !r.trayOpen() {
		t.Error("trayOpen = false with the tray as the overlay")
	}
	if !r.tasksOnScreen() {
		t.Error("tasksOnScreen = false with the tray open and the pane hidden")
	}
}

// The embedded Popup returns ITSELF from Update. If taskTray passed that
// through, the first keystroke would replace the overlay with the bare Popup,
// the TaskTrayView marker would be gone, and the refresh would stop — a bug
// that only shows up after you type, which is the worst kind to find by hand.
func TestTaskTrayKeepsItsMarkerAcrossUpdate(t *testing.T) {
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(
		[]TaskRow{{ID: "t1", State: TaskStateRunning, Agent: "research"}})}}})
	tray := &taskTray{Popup: popup}

	next, _ := tray.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if _, ok := next.(TaskTrayView); !ok {
		t.Fatalf("after a keystroke the overlay is %T, which no longer answers IsTaskTray", next)
	}

	// esc on an empty filter still closes.
	tray2 := &taskTray{Popup: NewPopup(PopupConfig{Tabs: []PopupTab{{}}})}
	if n, _ := tray2.Update(tea.KeyMsg{Type: tea.KeyEsc}); n != nil {
		t.Errorf("esc returned %T, want nil (closed)", n)
	}
}

// refreshTray must be a no-op when nothing a row renders has changed —
// otherwise every lifecycle event re-ranks the list and the fuzzy-match
// highlights flicker under a filter the user is still typing.
func TestRefreshTrayIsANoOpWhenNothingRenderableChanged(t *testing.T) {
	rows := []TaskRow{{ID: "t1", State: TaskStateRunning, Agent: "research", StartedAt: 10}}
	if !sameTrayRows(rows, []TaskRow{{ID: "t1", State: TaskStateRunning, Agent: "research", StartedAt: 10}}) {
		t.Error("identical rows compared unequal")
	}
	if sameTrayRows(rows, []TaskRow{{ID: "t1", State: TaskStateCompleted, Agent: "research", StartedAt: 10}}) {
		t.Error("a state change must force a rebuild")
	}
	if sameTrayRows(rows, nil) {
		t.Error("a length change must force a rebuild")
	}
}

// RefreshItems is the reason the tray can refresh live at all: it keeps the
// typed filter and re-finds the same row by id across a re-sort. SetItems
// (F8's, for an unrelated listing) discards both, which here would wipe a
// half-typed filter every time any background task changed state.
func TestRefreshItemsKeepsFilterAndSelectionAcrossAResort(t *testing.T) {
	first := trayItems(SortTaskRows([]TaskRow{
		{ID: "t1", State: TaskStateRunning, Agent: "research"},
		{ID: "t2", State: TaskStateRunning, Agent: "filler"},
	}))
	p := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: first}}, OnSelect: nil})

	// Type a filter that matches both, then select the second row.
	p.setFilter("e") // "research" and "filler" both contain an 'e'
	p.menu.MoveDown()
	before, ok := p.menu.Selected()
	if !ok {
		t.Fatal("nothing selected after moving down")
	}

	// t1 finishes: it changes section AND moves position in the sort.
	second := trayItems(SortTaskRows([]TaskRow{
		{ID: "t1", State: TaskStateCompleted, Agent: "research", FinishedAt: 99},
		{ID: "t2", State: TaskStateRunning, Agent: "filler"},
	}))
	p.RefreshItems(second)

	if p.filter != "e" {
		t.Errorf("filter = %q, want it preserved as \"e\"", p.filter)
	}
	after, ok := p.menu.Selected()
	if !ok {
		t.Fatal("nothing selected after the refresh")
	}
	if after.GetID() != before.GetID() {
		t.Errorf("selection moved from %q to %q across a re-sort",
			before.GetID(), after.GetID())
	}
}

// SetItems must keep its old behaviour — F8's picker depends on the clear.
func TestSetItemsStillClearsFilterAndSelection(t *testing.T) {
	p := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: []*array.Item{
		array.NewItem("a", "alpha"), array.NewItem("b", "beta"),
	}}}})
	p.setFilter("bet")
	p.SetItems([]*array.Item{array.NewItem("c", "gamma")})
	if p.filter != "" {
		t.Errorf("filter = %q, want cleared by SetItems", p.filter)
	}
	if p.menu.Len() != 1 {
		t.Errorf("Len = %d, want the new item visible under no filter", p.menu.Len())
	}
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

// A finished row fetches; a running one offers cancel. Both keep the tray
// open, so a fetch that fails leaves the user on the list they chose from.
func TestTraySelectBranchesOnFinished(t *testing.T) {
	r := &Root{log: discardLogger()}
	r.tray.rows = []TaskRow{
		{ID: "fin", State: TaskStateCompleted, Session: "s1", Agent: "research"},
		{ID: "run", State: TaskStateRunning, Session: "s1", Agent: "research"},
	}

	// Finished, no client: fetchTaskResult refuses and says why, and the
	// tray stays open either way.
	_, closes := r.onTraySelect(array.NewItem("fin", "x"))
	if closes {
		t.Error("a finished selection must leave the tray open")
	}
	if !strings.Contains(r.status, "not connected") {
		t.Errorf("status = %q, want it to name the missing backend", r.status)
	}

	// Running: a confirm overlay, via openOverlayMsg — never assigned
	// directly, since OnSelect runs inside the Popup's own Update.
	cmd, closes := r.onTraySelect(array.NewItem("run", "x"))
	if closes {
		t.Error("a running selection must leave the tray open")
	}
	if cmd == nil {
		t.Fatal("no cmd for a running task — expected a cancel confirmation")
	}
	open, ok := cmd().(openOverlayMsg)
	if !ok {
		t.Fatalf("got %T, want openOverlayMsg", cmd())
	}
	if _, ok := open.o.(*Confirm); !ok {
		t.Fatalf("overlay is %T, want *Confirm", open.o)
	}
}

// An id with no row behind it must do nothing — the empty placeholder row is
// exactly this case, and it is disabled, but a stale id after a refresh is
// reachable too.
func TestTraySelectIgnoresAnUnknownRow(t *testing.T) {
	r := &Root{log: discardLogger()}
	cmd, closes := r.onTraySelect(array.NewItem("__empty__", "no background tasks"))
	if cmd != nil || closes {
		t.Errorf("cmd = %v, closes = %v; want a no-op that leaves the tray open", cmd, closes)
	}
}

// ---------------------------------------------------------------------------
// Fetching a result — Done-when #1's second half
// ---------------------------------------------------------------------------

// The join is on task_id and NOTHING else, which is reload.go's permanent
// rule for this codebase: adjacency and arrival order both break the moment
// two sends overlap, and B12 added the column so they do not have to be
// guessed at. Here that means messages from a DIFFERENT task in the same
// session must not leak into the card.
func TestSplitTaskMessagesKeysOnTaskIDOnly(t *testing.T) {
	msgs := []trayMessage{
		mkMsg("other", trayRoleUser, "a different question", 0),
		mkMsg("other", trayRoleAssistant, "a different answer", 500),
		mkMsg("mine", trayRoleUser, "tokio vs asyncio", 0),
		mkMsg("mine", trayRoleAssistant, "first part", 1200),
		mkMsg("mine", trayRoleAssistant, "second part", 800),
	}
	q, a, tok := splitTaskMessages("mine", msgs)

	if q != "tokio vs asyncio" {
		t.Errorf("question = %q", q)
	}
	if a != "first part\n\nsecond part" {
		t.Errorf("answer = %q, want both assistant messages joined", a)
	}
	if tok != 2000 {
		t.Errorf("tokens = %d, want 2000 summed across the turn", tok)
	}
	if strings.Contains(a, "different") {
		t.Error("another task's answer leaked into this one")
	}
}

// A row written before total_tokens was populated must fall back to the two
// halves rather than report 0 — "unknown" and "free" are different claims.
func TestSplitTaskMessagesFallsBackToTheHalves(t *testing.T) {
	m := mkMsg("t", trayRoleAssistant, "body", 0)
	m.Usage.InputTokens, m.Usage.OutputTokens = 300, 150
	_, _, tok := splitTaskMessages("t", []trayMessage{m})
	if tok != 450 {
		t.Errorf("tokens = %d, want 450", tok)
	}
}

// The tray claims its reply by corr_id, which is also what keeps the Chat
// workspace from reacting to it: chat drops any envelope whose corr_id is not
// in its own pending map. An unrelated corr_id must leave the fetch pending.
func TestHandleTrayEnvelopeOnlyClaimsItsOwnReply(t *testing.T) {
	r := &Root{log: discardLogger()}
	r.tray.resultReqID = "mine"
	r.tray.resultTask = TaskRow{ID: "t1"}

	r.handleTrayEnvelope(protocol.Envelope{Type: protocol.EvSession, CorrID: "someone-else"})
	if r.tray.resultReqID != "mine" {
		t.Error("a foreign corr_id consumed the tray's pending fetch")
	}
	if r.overlay != nil {
		t.Errorf("a foreign reply opened %T", r.overlay)
	}
}

// Done-when #1: a result opens even though no workspace has that session
// loaded. Root fetches it by session id and joins on task_id; nothing here
// touches the Chat workspace at all.
func TestResultOpensForAnUnloadedSession(t *testing.T) {
	r := newOverlayCapableRoot()
	r.tray.resultReqID = "req"
	r.tray.resultTask = TaskRow{
		ID: "t1", State: TaskStateCompleted, Agent: "research",
		Session: "a-session-no-workspace-has", StartedAt: 1000, FinishedAt: 1063,
	}

	payload, err := json.Marshal(map[string]any{
		"session": map[string]any{
			"messages": []trayMessage{
				mkMsg("t1", trayRoleUser, "tokio vs asyncio", 0),
				mkMsg("t1", trayRoleAssistant, "asyncio is cooperative.", 12400),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	r.handleTrayEnvelope(protocol.Envelope{Type: protocol.EvSession, CorrID: "req", Payload: payload})

	card, ok := r.overlay.(*ResultCard)
	if !ok {
		t.Fatalf("overlay = %T, want *ResultCard", r.overlay)
	}
	if !strings.Contains(card.Title(), "tokio vs asyncio") {
		t.Errorf("title = %q, want the real question — the tray cannot show it, this card can",
			card.Title())
	}
	if !strings.Contains(card.body, "cooperative") {
		t.Errorf("body = %q", card.body)
	}
	if card.tokens != 12400 {
		t.Errorf("tokens = %d, want 12400", card.tokens)
	}
	if r.tray.resultReqID != "" {
		t.Error("resultReqID not cleared after its own reply")
	}
}

// A refused fetch must say so and leave the tray as the overlay — not open an
// empty card, and not close everything leaving only a status line.
func TestHandleTrayEnvelopeSurfacesAnError(t *testing.T) {
	r := newOverlayCapableRoot()
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(nil)}}})
	tray := &taskTray{Popup: popup}
	r.tray = trayState{popup: popup, rows: nil, resultReqID: "req", resultTask: TaskRow{ID: "t1"}}
	r.overlay = tray

	payload, _ := json.Marshal(map[string]any{"error": "SessionNotFoundError"})
	r.handleTrayEnvelope(protocol.Envelope{Type: protocol.EvError, CorrID: "req", Payload: payload})

	if _, ok := r.overlay.(TaskTrayView); !ok {
		t.Errorf("overlay = %T, want the tray still open", r.overlay)
	}
	if !strings.Contains(r.status, "SessionNotFoundError") {
		t.Errorf("status = %q, want the backend's own reason", r.status)
	}
}

// A task whose session row is gone (ON DELETE CASCADE took its messages)
// must be answered locally rather than with a round trip that returns
// nothing.
func TestFetchTaskResultRefusesWithoutASession(t *testing.T) {
	r := &Root{log: discardLogger()}
	if cmd := r.fetchTaskResult(TaskRow{ID: "t1", State: TaskStateCompleted}); cmd != nil {
		t.Error("expected no request for a session-less task")
	}
	if !strings.Contains(r.status, "no session") {
		t.Errorf("status = %q", r.status)
	}
}

// newOverlayCapableRoot is a Root wide enough for an overlay, with the one
// layout flag overlayFits reads actually set. A zero-valued Root has
// OverlaysAllowed false, so every OpenOverlay silently refuses -- which is
// correct behaviour under the real layout engine and a silent no-op in a
// test that forgot it.
func newOverlayCapableRoot() *Root {
	r := &Root{
		w: 100, h: 40,
		theme: theme.New("turquoise", theme.BackgroundTransparent),
		log:   discardLogger(),
		// OpenOverlay calls recompute, which calls current() -- a Root with
		// no workspaces panics there. fakePickerWorkspace already
		// implements the whole interface for this package's other tests.
		workspaces: map[WorkspaceID]Workspace{WorkspaceChat: &fakePickerWorkspace{}},
	}
	r.layout.OverlaysAllowed = true
	return r
}

func mkMsg(taskID, role, text string, total int) trayMessage {
	var m trayMessage
	m.TaskID, m.Role = taskID, role
	m.Content.Text = text
	m.Usage.TotalTokens = total
	return m
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// F09: "F1 and a palette action open it." Registration is what makes BOTH
// true at once — the palette, the help screen, the leader hints and rebinding
// all read the one registry, so a tray wired to a key without an entry here
// would be invisible in three of the four.
func TestTrayActionIsRegisteredAndBound(t *testing.T) {
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent), State: testProviderDefault})
	if err != nil {
		t.Fatal(err)
	}

	a, ok := r.actions.ByID("tasks.tray")
	if !ok {
		t.Fatal("tasks.tray is not registered — it would not appear in the palette or help")
	}
	if a.Chord != "f1" {
		t.Errorf("chord = %q, want f1 (F09's own key)", a.Chord)
	}
	if a.Leader == "" {
		t.Error("no leader sequence — ctrl+alt chords are unreliable on Windows " +
			"Terminal, so the leader is the supported path (INDEX.md's Windows notes)")
	}

	byChord, ok := r.actions.ByChord("f1")
	if !ok || byChord.ID != "tasks.tray" {
		t.Errorf("f1 resolves to %v, want tasks.tray", byChord.ID)
	}
	byLeader, ok := r.actions.ByLeader(a.Leader)
	if !ok || byLeader.ID != "tasks.tray" {
		t.Errorf("leader %q resolves to %v, want tasks.tray", a.Leader, byLeader.ID)
	}
}

// The tray must be reachable with ZERO tasks: an action greyed out when
// nothing is running makes "is anything running?" unanswerable by the one key
// that exists to answer it.
func TestTrayActionAvailableWithNoTasks(t *testing.T) {
	a := trayActionFrom(t)
	withBackend := newOverlayCapableRoot()
	withBackend.client = &protocol.Client{}
	if !a.Available(withBackend) {
		t.Error("tasks.tray unavailable with an empty snapshot")
	}

	// But not without a backend: the list could never refresh, and a frozen
	// snapshot of nothing is indistinguishable from genuinely nothing.
	noBackend := newOverlayCapableRoot()
	if a.Available(noBackend) {
		t.Error("tasks.tray available with no backend to ask")
	}
}

func trayActionFrom(t *testing.T) actions.Action[*Root] {
	t.Helper()
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent), State: testProviderDefault})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.actions.ByID("tasks.tray")
	if !ok {
		t.Fatal("tasks.tray is not registered")
	}
	return a
}

// ---------------------------------------------------------------------------
// Background scope
// ---------------------------------------------------------------------------

// F09's "Global" bullet and Done-when #1 both say "tasks from every
// WORKSPACE" — a statement about scope across workspaces and sessions, not a
// claim that every task record belongs here. The episode's Purpose is "where
// BACKGROUND work becomes visible", and an inline turn's reply is already in
// the transcript.
func TestTrayShowsBackgroundWorkOnly(t *testing.T) {
	r := &Root{log: discardLogger()}
	r.tasks = []TaskRow{
		{ID: "res", Agent: "research", State: TaskStateCompleted, FinishedAt: 30},
		{ID: "gen", Agent: "general", State: TaskStateCompleted, FinishedAt: 20},
		{ID: "fil", Agent: "filler-explainer", State: TaskStateCompleted, FinishedAt: 10},
	}

	var ids []string
	for _, row := range r.trayRows() {
		ids = append(ids, row.ID)
	}
	if len(ids) != 1 || ids[0] != "res" {
		t.Errorf("tray rows = %v, want only the background task (res)", ids)
	}
}

// The agent table is the fallback, not the authority. TaskStarted carries the
// backend's own `background` flag (computed as max_steps > 1), and a user
// agent with a real bounded loop is not in the table — so a seen flag must
// PROMOTE it in. Hiding a real background result is the costly error here.
func TestSeenBackgroundFlagPromotesAnUnknownAgent(t *testing.T) {
	r := &Root{log: discardLogger()}
	r.tasks = []TaskRow{{ID: "custom", Agent: "my-own-agent", State: TaskStateCompleted}}

	if len(r.trayRows()) != 0 {
		t.Fatal("an unknown agent should default to not-background")
	}

	payload, err := json.Marshal(map[string]any{"background": true})
	if err != nil {
		t.Fatal(err)
	}
	r.handleTaskListEnvelope(protocol.Envelope{
		Type: protocol.EvTaskStarted, TaskID: "custom", Payload: payload,
	})

	rows := r.trayRows()
	if len(rows) != 1 || rows[0].ID != "custom" {
		t.Errorf("tray rows = %v, want the promoted task", rows)
	}
}

// Promote-only: a `background: false` flag must not be recorded, because the
// absence of a record already means "not known to be background" and storing
// false would invite a reader to treat it as "known inline" — which a durable
// row from a previous run never is.
func TestFalseBackgroundFlagIsNotRecorded(t *testing.T) {
	r := &Root{log: discardLogger()}
	payload, _ := json.Marshal(map[string]any{"background": false})
	r.noteTaskBackground(protocol.Envelope{
		Type: protocol.EvTaskStarted, TaskID: "t1", Payload: payload,
	})
	if len(r.backgroundSeen) != 0 {
		t.Errorf("backgroundSeen = %v, want nothing recorded for a false flag", r.backgroundSeen)
	}
}

// The promotion map must not grow for the life of the process. ListTasks
// answers with a bounded recent window, so a task that has aged out of it can
// never reach the tray again and its record is dead weight.
func TestBackgroundSeenIsPrunedToTheSnapshot(t *testing.T) {
	r := &Root{log: discardLogger(), tasksReqID: "req"}
	r.backgroundSeen = map[string]bool{"old": true, "still-here": true}

	payload, err := json.Marshal(map[string]any{
		"tasks": []TaskRow{{ID: "still-here", State: TaskStateCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.handleTaskListEnvelope(protocol.Envelope{
		Type: protocol.EvTaskList, CorrID: "req", Payload: payload,
	})

	if r.backgroundSeen["old"] {
		t.Error("a record for a task no longer in the snapshot survived the prune")
	}
	if !r.backgroundSeen["still-here"] {
		t.Error("the prune dropped a record for a task still in the snapshot")
	}
}

// The agent fact has ONE copy, in app, read by chat's lane table. Asserted
// here so a re-fork in either direction fails.
func TestBackgroundAgentTableMatchesB11(t *testing.T) {
	for id, want := range map[string]bool{
		"research": true, "general": false, "filler-explainer": false,
	} {
		if got := IsBackgroundAgent(id); got != want {
			t.Errorf("IsBackgroundAgent(%q) = %v, want %v (B11's max_steps table)", id, got, want)
		}
		if !IsKnownAgent(id) {
			t.Errorf("IsKnownAgent(%q) = false", id)
		}
	}
	if IsBackgroundAgent("not-a-builtin") || IsKnownAgent("not-a-builtin") {
		t.Error("an unregistered id must answer false to both")
	}

	ids := KnownAgentIDs()
	if len(ids) != 3 {
		t.Fatalf("KnownAgentIDs() = %v", ids)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Errorf("KnownAgentIDs() is not sorted: %v -- chat's prefix test "+
				"iterates these and Go randomises map order per run", ids)
		}
	}
}

// ---------------------------------------------------------------------------
// The m:ss clock
// ---------------------------------------------------------------------------

// The observed bug: the clock advanced only when something else caused a
// frame (opened at 0:00, reopened at 1:03, correct at 1:43 only because the
// task finishing forced a redraw). It needs its own redraw loop, because
// TaskElapsed reads the wall clock and no backend event reports that.
func TestTrayClockRunsOnlyWhileThereIsAClockToRun(t *testing.T) {
	r := newOverlayCapableRoot()

	if r.trayClockWanted() {
		t.Error("wanted with no tray open")
	}
	if cmd := r.ensureTrayClock(); cmd != nil {
		t.Error("armed a tick with no tray open")
	}

	openTray := func(rows []TaskRow) {
		popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(rows)}}, MinW: 50, MinH: 12})
		r.tray = trayState{popup: popup, rows: rows}
		r.overlay = &taskTray{Popup: popup}
	}

	// Open, but only finished rows: nothing changes, so no tick.
	openTray([]TaskRow{{ID: "t1", Agent: "research", State: TaskStateCompleted}})
	if r.trayClockWanted() {
		t.Error("wanted with only finished rows -- that frame is identical every second")
	}

	// A running row: wanted, and armed exactly once.
	openTray([]TaskRow{{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1}})
	if !r.trayClockWanted() {
		t.Fatal("not wanted with a running row")
	}
	if first := r.ensureTrayClock(); first == nil {
		t.Fatal("no tick armed for a running row")
	}
	if again := r.ensureTrayClock(); again != nil {
		t.Error("armed a second concurrent tick loop")
	}

	// The tick re-arms while wanted...
	gen := r.trayClock.gen
	if _, cmd := r.update(trayTickMsg{gen: gen}); cmd == nil {
		t.Error("the tick did not re-arm while a task is still running")
	}

	// ...and self-terminates once it is not, which is the only way to end a
	// tea.Tick that has already been scheduled.
	r.overlay = nil
	if _, cmd := r.update(trayTickMsg{gen: gen}); cmd != nil {
		t.Error("the tick re-armed after the tray closed")
	}
	if r.trayClock.running {
		t.Error("running flag not cleared, so a later open cannot re-arm")
	}

	// A stale tick from an earlier loop is dropped.
	openTray([]TaskRow{{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1}})
	r.ensureTrayClock()
	if _, cmd := r.update(trayTickMsg{gen: r.trayClock.gen - 1}); cmd != nil {
		t.Error("a tick from a superseded generation re-armed")
	}
}

// The clock must restart when the result card returns the tray -- a path that
// runs no case in root.go's own switch, which is why ensureTrayClock is asked
// from Update's tail rather than at each call site.
func TestTrayClockRestartsAfterReturningFromAResultCard(t *testing.T) {
	r := newOverlayCapableRoot()
	rows := []TaskRow{{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1}}
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(rows)}}, MinW: 50, MinH: 12})
	tray := &taskTray{Popup: popup}
	r.tray = trayState{popup: popup, rows: rows}
	r.overlay = tray
	r.ensureTrayClock()

	// A card is open: the next tick stops the loop.
	card := newResultCard(rows[0], nil)
	card.back = tray
	r.overlay = card
	_, _ = r.update(trayTickMsg{gen: r.trayClock.gen})
	if r.trayClock.running {
		t.Fatal("the loop kept running with a card over the tray")
	}

	// esc returns the tray through Update, whose tail must re-arm.
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := r.overlay.(TaskTrayView); !ok {
		t.Fatalf("overlay = %T after esc, want the tray", r.overlay)
	}
	if !r.trayClock.running || cmd == nil {
		t.Error("the clock did not restart on returning to the tray")
	}
}

// ---------------------------------------------------------------------------
// Sizing
// ---------------------------------------------------------------------------

// The tray and the card are sized to the TRANSCRIPT PANE (layout.Main) less
// one cell per side -- not to the terminal, which is wider and taller once
// the right pane, the header, the status bar and the query box are accounted
// for. LAYOUT.md's 50x12 / 60x18 stay as the floors overlayFits refuses
// below.
func TestMaximisedOverlaysMatchTheTranscriptPane(t *testing.T) {
	r := newOverlayCapableRoot()
	// A real Compute, so Main is the rect the frame is actually laid out
	// with rather than something this test made up.
	r.w, r.h = 160, 48
	r.rightPaneOpen = true
	r.recompute()

	main := r.layout.Main
	if main.W <= 0 || main.H <= 0 {
		t.Fatalf("Compute gave no transcript rect: %+v", main)
	}
	if main.W >= r.w || main.H >= r.h {
		t.Fatalf("Main %dx%d is not smaller than the terminal %dx%d -- this "+
			"test cannot tell the two sources apart", main.W, main.H, r.w, r.h)
	}

	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(nil)}}, MinW: 50, MinH: 12})
	for _, o := range []Overlay{
		&taskTray{Popup: popup},
		newResultCard(TaskRow{ID: "t", State: TaskStateCompleted}, nil),
	} {
		r.overlay = o
		rect := r.overlayRect()
		if rect.W != main.W-2 || rect.H != main.H-2 {
			t.Errorf("%T rect = %dx%d, want %dx%d (transcript pane less one cell each side)",
				o, rect.W, rect.H, main.W-2, main.H-2)
		}
		// Centred in the whole TERMINAL, not placed over Main -- so the
		// right pane and the status bar stay visible around it.
		if rect.X != (r.w-rect.W)/2 || rect.Y != (r.h-rect.H)/2 {
			t.Errorf("%T at (%d,%d), want it centred in the terminal at (%d,%d)",
				o, rect.X, rect.Y, (r.w-rect.W)/2, (r.h-rect.H)/2)
		}
		if rect.X <= 0 || rect.Y <= 0 {
			t.Errorf("%T at (%d,%d): no scrim margin left", o, rect.X, rect.Y)
		}
	}

	// Hiding the right pane widens Main, so the overlay widens with it --
	// the property a fraction-of-the-terminal constant could not have.
	narrowW := r.overlayRect().W
	r.rightPaneOpen = false
	r.recompute()
	if wideW := r.overlayRect().W; wideW <= narrowW {
		t.Errorf("overlay width %d with the right pane hidden, %d with it shown "+
			"-- it is not tracking the transcript pane", wideW, narrowW)
	}

	// A plain Overlay is untouched: the palette would look absurd stretched
	// across 160 columns, which is why this is opt-in. Built through New()
	// because the palette reads the action registry.
	real, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent), State: testProviderDefault})
	if err != nil {
		t.Fatal(err)
	}
	real.w, real.h = 160, 48
	real.layout.OverlaysAllowed = true
	palette := newPaletteOverlay(real)
	wantW, wantH := palette.MinSize()
	real.overlay = palette
	if rect := real.overlayRect(); rect.W != wantW || rect.H != wantH {
		t.Errorf("palette rect = %dx%d, want its own %dx%d — MaximisedOverlay is opt-in",
			rect.W, rect.H, wantW, wantH)
	}
}

// A terminal too small to grant the preference must still get a rect that
// honours the overlay's own floor, not a shrunken one.
func TestMaximisedOverlayNeverGoesBelowItsFloor(t *testing.T) {
	r := newOverlayCapableRoot()
	r.w, r.h = 72, 20
	r.recompute()
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(nil)}}, MinW: 50, MinH: 12})
	r.overlay = &taskTray{Popup: popup}

	rect := r.overlayRect()
	if rect.W < 50 || rect.H < 12 {
		t.Errorf("rect = %dx%d, below the 50x12 floor", rect.W, rect.H)
	}
	if rect.W > r.w-2 || rect.H > r.h-2 {
		t.Errorf("rect = %dx%d, past the margin in a %dx%d terminal",
			rect.W, rect.H, r.w, r.h)
	}
}

// The footer hint was truncated at the old 50-column size. Confirmed here to
// be a width problem and nothing else: it renders in FULL once there is room,
// so there is no second fixed character budget clipping it.
func TestTrayHintIsNotClippedOnceThereIsRoom(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	r := newOverlayCapableRoot()
	rows := SortTaskRows([]TaskRow{{ID: "t1", Agent: "research", State: TaskStateCompleted}})
	popup := NewPopup(PopupConfig{
		Title: "Tasks", Tabs: []PopupTab{{Items: trayItems(rows)}},
		MinW: 50, MinH: 12, OnSelect: r.onTraySelect, Hint: r.trayHint,
	})
	r.tray = trayState{popup: popup, rows: rows}
	r.overlay = &taskTray{Popup: popup}

	want := r.trayHint()
	if want == "" {
		t.Fatal("no hint to check")
	}

	// At the old fixed size it did not fit -- recorded so the regression is
	// legible if someone reverts the sizing.
	if narrow := stripANSI(popup.View(th, 48, 12)); strings.Contains(narrow, want) {
		t.Log("note: the hint now fits even at 48 columns; the sizing fix is " +
			"no longer load-bearing for this particular string")
	}

	wide := stripANSI(popup.View(th, 158, 46))
	if !strings.Contains(wide, want) {
		t.Errorf("hint %q is still clipped at 158 columns -- something other "+
			"than width is budgeting it.\nrendered:\n%s", want, wide)
	}
	if strings.Contains(wide, "…") {
		t.Errorf("an ellipsis survived at full width:\n%s", wide)
	}
}

// ---------------------------------------------------------------------------
// The cancel confirmation
// ---------------------------------------------------------------------------

// Both button labels must render in full, and the two must occupy the same
// columns whichever has focus. The original bug was one cause with two
// symptoms: the row was 44 cells inside a 38-cell budget, so `centre` fell
// through to truncateCells -- which lost "Leave it running"'s tail AND, since
// the focused and unfocused forms carry their escape sequences at different
// offsets, cut at a different visible column depending on focus, which read
// as the brackets jumping.
func TestCancelConfirmFitsBothLabelsAndStaysAligned(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	c := NewConfirm("Cancel task",
		"Stop research? Whatever it has produced so far is kept.",
		"Cancel it", "Leave it running", nil)

	w, h := c.MinSize()
	if w <= 40 {
		t.Errorf("MinSize width = %d; these labels need more than LAYOUT.md's 40 floor", w)
	}

	noFocused := strings.Split(stripANSI(c.View(th, w-2, h-2)), "\n")
	c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	yesFocused := strings.Split(stripANSI(c.View(th, w-2, h-2)), "\n")

	for _, lines := range [][]string{noFocused, yesFocused} {
		row := ""
		for _, l := range lines {
			if strings.Contains(l, "Cancel it") {
				row = l
			}
		}
		if row == "" {
			t.Fatalf("no button row found in:\n%s", strings.Join(lines, "\n"))
		}
		if !strings.Contains(row, "Leave it running") {
			t.Errorf("button row %q lost the second label's tail", row)
		}
		if strings.Count(row, "[") != 1 || strings.Count(row, "]") != 1 {
			t.Errorf("button row %q does not have exactly one bracketed side", row)
		}
	}

	// Same columns either way: the bracketed side starts where the other
	// side's padding ends, so neither appears to shift.
	colOf := func(lines []string, label string) int {
		for _, l := range lines {
			if i := strings.Index(l, label); i >= 0 && strings.Contains(l, "Cancel it") {
				return i
			}
		}
		return -1
	}
	if a, b := colOf(noFocused, "Cancel it"), colOf(yesFocused, "Cancel it"); a != b {
		t.Errorf("\"Cancel it\" sits at column %d unfocused and %d focused", a, b)
	}
	if a, b := colOf(noFocused, "Leave it running"), colOf(yesFocused, "Leave it running"); a != b {
		t.Errorf("\"Leave it running\" sits at column %d and %d", a, b)
	}
}

// The quit confirm is the pre-existing caller and must be unaffected -- the
// MinSize change can only ever grow a confirm that would not have fitted.
func TestShortLabelledConfirmKeepsTheLayoutFloor(t *testing.T) {
	c := NewConfirm("Quit", "Tasks are running.", "Quit", "Cancel", nil)
	if w, h := c.MinSize(); w != 40 || h != 7 {
		t.Errorf("MinSize = %dx%d, want LAYOUT.md's 40x7 unchanged", w, h)
	}
}

// ---------------------------------------------------------------------------
// The clock, end to end
// ---------------------------------------------------------------------------

// The clock bug, pinned at the level it actually broke.
//
// The first fix scheduled the tick correctly and it was delivered and
// re-armed correctly -- and the clock still read 0:00, because the note was
// computed at trayItems() time and baked into the item. A redraw cannot
// refresh a value computed before the redraw existed. So this test does not
// assert that a tick fires; it asserts that the RENDERED FRAME changes across
// real ticks, which is the only claim a user can check.
//
// It spends ~3 real seconds, deliberately: trayClockInterval is one second
// and the point is to observe actual elapsed wall time rather than to
// simulate it.
func TestTrayClockAdvancesInTheRenderedFrame(t *testing.T) {
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent), State: testProviderDefault})
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.tasks = []TaskRow{{
		ID: "t1", Agent: "research", State: TaskStateRunning,
		StartedAt: float64(time.Now().Unix()), CreatedAt: 1,
	}}
	_ = r.openTaskTray()
	if !r.trayOpen() {
		t.Fatal("the tray did not open")
	}

	cmd := r.ensureTrayClock()
	if cmd == nil {
		t.Fatal("no redraw scheduled with a running row on screen")
	}

	seen := map[string]bool{}
	var order []string
	for i := 0; i < 3; i++ {
		var tick tea.Msg
		for _, m := range collectMsgs(cmd) {
			if _, ok := m.(trayTickMsg); ok {
				tick = m
			}
		}
		if tick == nil {
			t.Fatalf("iteration %d: the scheduled cmd produced no trayTickMsg", i)
		}
		_, next := r.Update(tick)
		if next == nil {
			t.Fatalf("iteration %d: the loop stopped re-arming while a task is still running", i)
		}
		cmd = next

		clock := trayClockOnScreen(r.View())
		if clock == "" {
			t.Fatalf("iteration %d: no running row in the rendered frame", i)
		}
		seen[clock] = true
		order = append(order, clock)
	}

	if len(seen) < 3 {
		t.Errorf("the clock did not advance in the FRAME across 3 real ticks: %v\n"+
			"a frozen value here means the note is being read off the item "+
			"instead of recomputed in PopupConfig.Decor -- see RowDecor", order)
	}
}

// collectMsgs executes a cmd and flattens one level of batch. It BLOCKS for a
// tea.Tick's real interval, which is the point.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch v := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, collectMsgs(c)...)
		}
		return out
	default:
		return []tea.Msg{v}
	}
}

// trayClockOnScreen digs the m:ss out of the rendered running row.
func trayClockOnScreen(frame string) string {
	for _, line := range strings.Split(stripANSI(frame), "\n") {
		if !strings.Contains(line, TaskGlyph(TaskStateRunning)) {
			continue
		}
		fields := strings.Fields(line)
		for i := len(fields) - 1; i >= 0; i-- {
			if strings.Contains(fields[i], ":") {
				return fields[i]
			}
		}
	}
	return ""
}

// The root cause, pinned directly: the note must be recomputed per render.
// This fails the moment someone puts it back on the item.
func TestTrayNoteIsRecomputedPerRenderNotStoredOnTheItem(t *testing.T) {
	r := newOverlayCapableRoot()
	row := TaskRow{ID: "t1", Agent: "research", State: TaskStateRunning,
		StartedAt: float64(time.Now().Unix()) - 10}
	r.tray.rows = []TaskRow{row}

	items := trayItems(r.tray.rows)
	if b := items[0].GetBinding(); b != "" {
		t.Errorf("the item carries a baked note %q -- it will freeze; "+
			"the note belongs in trayDecor", b)
	}
	if title := items[0].GetTitle(); strings.ContainsAny(title, TaskGlyph(TaskStateRunning)) {
		t.Errorf("the item's title %q carries the glyph -- Popup then renders "+
			"it in the title's own role and the colour is lost", title)
	}

	// Same item, later wall clock, different decor.
	first := r.trayDecor(items[0]).Note
	r.tray.rows[0].StartedAt -= 5
	second := r.trayDecor(items[0]).Note
	if first == second {
		t.Errorf("decor returned %q both times -- it is not reading the row fresh", first)
	}
}

// ---------------------------------------------------------------------------
// Colour parity with the right-pane Task card
// ---------------------------------------------------------------------------

// Both surfaces share app.TaskGlyph and app.TaskRole after the earlier
// consolidation, and sharing them was supposed to make colour drift
// impossible -- but the tray put the glyph inside the item's TITLE, which
// Popup renders in the title's own TextPrimary role, so ✓/✕ came out
// uncoloured in the tray while the card coloured them.
//
// This asserts the tray's rendered row contains the EXACT styled run the card
// composes. Its counterpart is chat's TestTaskCardReadsTheSharedVocabulary,
// which asserts the card's line starts with the identical expression --
// `th.Style(app.TaskRole(s)).Render(app.TaskGlyph(s) + " ")`. The two tests
// anchor on one expression on purpose: neither package can render the other's
// surface (chat imports app, not the reverse, and trayDecor is unexported),
// so agreeing on the expression is what makes them agree on the bytes.
func TestTrayRowGlyphIsStyledLikeTheTaskCards(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)

	for _, state := range []string{
		TaskStateRunning, TaskStateQueued, TaskStateCompleted,
		TaskStateFailed, TaskStateCancelled, "a_state_no_build_knows",
	} {
		r := newOverlayCapableRoot()
		// Two rows, so the one under test is NOT the selected one: the
		// selected row is a Fill by THEME.md rule 6 and carries no
		// per-glyph colour (see renderRow for why that is also the only
		// escape-safe option).
		r.tray.rows = []TaskRow{
			{ID: "other", Agent: "research", State: TaskStateQueued},
			{ID: "subject", Agent: "research", State: state},
		}
		popup := NewPopup(PopupConfig{
			Tabs: []PopupTab{{Items: trayItems(r.tray.rows)}},
			MinW: 50, MinH: 12,
			Decor: r.trayDecor,
		})

		want := th.Style(TaskRole(state)).Render(TaskGlyph(state) + " ")
		got := popup.View(th, 60, 12)
		if !strings.Contains(got, want) {
			t.Errorf("state %q: the tray row does not contain the card's own "+
				"styled glyph run.\nwant substring %q\nin %q",
				state, want, got)
		}
	}
}

// The selected row is a Fill, whole-row, and the glyph inside it is plain --
// both by THEME.md rule 6 and because a styled icon's own reset would
// terminate the fill for everything after it on the row, which is the
// severed-escape failure this package has already been bitten by twice.
func TestSelectedTrayRowIsFilledNotPerGlyphColoured(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	r := newOverlayCapableRoot()
	r.tray.rows = []TaskRow{{ID: "only", Agent: "research", State: TaskStateCompleted}}
	popup := NewPopup(PopupConfig{
		Tabs: []PopupTab{{Items: trayItems(r.tray.rows)}},
		MinW: 50, MinH: 12,
		Decor: r.trayDecor,
	})

	out := popup.View(th, 60, 12)
	// The glyph is on screen...
	if !strings.Contains(stripANSI(out), TaskGlyph(TaskStateCompleted)) {
		t.Fatalf("the glyph is missing entirely from %q", stripANSI(out))
	}
	// ...but not wrapped in its own role, which would break the fill.
	if strings.Contains(out, th.Style(TaskRole(TaskStateCompleted)).Render(
		TaskGlyph(TaskStateCompleted)+" ")) {
		t.Error("the SELECTED row carries a per-glyph colour run inside its Fill")
	}
}

// A row with no task behind it -- the "no background tasks" placeholder --
// gets no glyph at all. A state marker beside a sentence would imply a state.
func TestPlaceholderRowHasNoGlyph(t *testing.T) {
	r := newOverlayCapableRoot()
	d := r.trayDecor(trayItems(nil)[0])
	if d.Icon != "" || d.Note != "" {
		t.Errorf("placeholder decor = %+v, want nothing", d)
	}
}

// Decor is opt-in: the palette, Help and F8's picker pass nil and must keep
// rendering their item bindings as the right-hand column, with no icon.
func TestPopupWithoutDecorKeepsTheBindingColumn(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: []*array.Item{
		array.NewItem("a", "Alpha").SetBinding("ctrl+a"),
		array.NewItem("b", "Beta").SetBinding("ctrl+b"),
	}}}})
	out := stripANSI(popup.View(th, 60, 12))
	for _, want := range []string{"Alpha", "ctrl+a", "Beta", "ctrl+b"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from a Decor-less popup:\n%s", want, out)
		}
	}
}

// ---------------------------------------------------------------------------
// Confirm button balance
// ---------------------------------------------------------------------------

// Every button gets the SAME padding inside its own box -- two cells either
// side, bracketed or not -- which is the property that reads as balanced. The
// shared-label-width padding it replaced gave the shorter label five and six
// cells of slack while its neighbour hugged its own text, producing a ragged
// middle.
func TestConfirmButtonsHugTheirOwnLabels(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	c := NewConfirm("Cancel task", "body", "Cancel it", "Leave it running", nil)

	row := stripANSI(c.renderButtons(th))
	for _, want := range []string{"  Cancel it  ", "[ Leave it running ]"} {
		if !strings.Contains(row, want) {
			t.Errorf("button row %q does not contain %q", row, want)
		}
	}

	// Focus-invariant width: "[ l ]" and "  l  " are both l+4, so the pair
	// cannot shift as focus moves between them.
	before := visibleWidth(c.renderButtons(th))
	c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if after := visibleWidth(c.renderButtons(th)); after != before {
		t.Errorf("row width changed with focus: %d then %d", before, after)
	}
	row = stripANSI(c.renderButtons(th))
	for _, want := range []string{"[ Cancel it ]", "  Leave it running  "} {
		if !strings.Contains(row, want) {
			t.Errorf("focused row %q does not contain %q", row, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Section headings must never scroll away from the rows they label
// ---------------------------------------------------------------------------

// buildLongTray is a list long enough to need scrolling, with a Running
// section and many Finished ones -- the shape the bug was found in.
func buildLongTray(t *testing.T, running, finished int) (*Root, *Popup) {
	t.Helper()
	r := newOverlayCapableRoot()
	var rows []TaskRow
	for i := 0; i < running; i++ {
		rows = append(rows, TaskRow{
			ID: fmt.Sprintf("run%02d", i), Agent: "research",
			State: TaskStateRunning, StartedAt: float64(time.Now().Unix()) - 30,
			CreatedAt: float64(100 - i),
		})
	}
	for i := 0; i < finished; i++ {
		rows = append(rows, TaskRow{
			ID: fmt.Sprintf("fin%02d", i), Agent: "research",
			State: TaskStateCompleted, StartedAt: 1000, FinishedAt: float64(2000 - i),
		})
	}
	rows = SortTaskRows(rows)
	r.tasks, r.tray.rows = rows, rows

	popup := NewPopup(PopupConfig{
		Title: "Tasks", Tabs: []PopupTab{{Items: trayItems(rows)}},
		MinW: 50, MinH: 12,
		OnSelect:       r.onTraySelect,
		Decor:          r.trayDecor,
		StickySections: true,
		Hint:           r.trayHint,
	})
	return r, popup
}

// renderedSections walks the sequence Popup.View actually draws -- the pinned
// heading, then the visible rows with the duplicate leading heading dropped --
// and returns, for each ITEM row, the heading in force above it.
//
// Derived from the same two functions View uses (VisibleRows, stickyHeader)
// rather than by parsing the frame, so it checks POSITION and not merely
// presence: the label in force for a row is the nearest label above it.
func renderedSections(p *Popup) (labels []string, wants []string) {
	rows := p.menu.VisibleRows()
	current, dropped := p.stickyHeader()
	if p.stickyActive() {
		rows = rows[dropped:]
	}
	for _, row := range rows {
		if row.Kind == array.RowHeader {
			current = row.Header
			continue
		}
		if row.Item == nil {
			continue
		}
		labels = append(labels, current)
		wants = append(wants, row.Item.GetSection())
	}
	return labels, wants
}

// The regression, at every reachable scroll offset rather than only at the
// ends: no visible row may be shown under the wrong heading, or under none.
//
// The bug this replaces: array.Menu's headings are ordinary rows and
// ensureVisible scrolls the minimum needed to show the selected ITEM, so a
// heading one line above the viewport was simply gone while every row it
// labelled was on screen and selectable. Reproduced by scrolling to the
// bottom and back -- the state the list ends in after that is the state the
// bug lives in, which is why walking BOTH directions matters.
func TestSectionHeadingsSurviveEveryScrollOffset(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 60, 14

	check := func(where string) {
		// Render first: View is what sets the menu's height, and the
		// heading's position depends on it.
		frame := stripANSI(popup.View(th, w, h))
		labels, wants := renderedSections(popup)
		if len(labels) == 0 {
			t.Fatalf("%s: no item rows rendered at all", where)
		}
		for i := range labels {
			if labels[i] == "" {
				t.Errorf("%s: row %d (%s) is rendered under NO heading\n%s",
					where, i, wants[i], frame)
				return
			}
			if labels[i] != wants[i] {
				t.Errorf("%s: row %d is under heading %q but belongs to %q\n%s",
					where, i, labels[i], wants[i], frame)
				return
			}
		}
		// And the heading in force is really on screen, not merely computed.
		if !strings.Contains(frame, labels[0]) {
			t.Errorf("%s: heading %q is in force but absent from the frame\n%s",
				where, labels[0], frame)
		}
	}

	check("fresh")

	// Down one row at a time, to the end.
	for i := 0; ; i++ {
		before, _ := popup.menu.Selected()
		popup.menu.MoveDown()
		after, _ := popup.menu.Selected()
		check(fmt.Sprintf("down x%d", i+1))
		if before == after {
			break
		}
		if i > 200 {
			t.Fatal("MoveDown never reached the end")
		}
	}

	// And all the way back up -- the direction the bug was found in.
	for i := 0; ; i++ {
		before, _ := popup.menu.Selected()
		popup.menu.MoveUp()
		after, _ := popup.menu.Selected()
		check(fmt.Sprintf("up x%d", i+1))
		if before == after {
			break
		}
		if i > 200 {
			t.Fatal("MoveUp never reached the start")
		}
	}

	// The paged and jump paths move the cursor differently from the stepped
	// ones, so they are their own offsets.
	popup.menu.Bottom()
	check("Bottom()")
	popup.menu.Top()
	check("Top()")
	for i := 0; i < 6; i++ {
		popup.menu.PageDown()
		check(fmt.Sprintf("PageDown x%d", i+1))
	}
	for i := 0; i < 6; i++ {
		popup.menu.PageUp()
		check(fmt.Sprintf("PageUp x%d", i+1))
	}

	// And at a range of box heights, since the heading's position depends on
	// how many rows the list has.
	for _, height := range []int{12, 13, 14, 18, 24, 40} {
		popup.menu.Bottom()
		_ = popup.View(th, w, height)
		labels, wants := renderedSections(popup)
		for i := range labels {
			if labels[i] != wants[i] {
				t.Errorf("h=%d: row %d under %q, belongs to %q", height, i, labels[i], wants[i])
			}
		}
	}
}

// The reported case exactly: ONE running task among many finished ones. The
// running row ends up selected with the cursor on it, so "Running" sat one
// line above the window -- a row that was still selectable and openable with
// no heading anywhere.
func TestTheRunningRowIsNeverUnlabelled(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)

	// Scroll to the bottom and come back, which is how it was found.
	popup.menu.Bottom()
	_ = popup.View(th, 60, 14)
	for i := 0; i < 40; i++ {
		popup.menu.MoveUp()
	}
	frame := stripANSI(popup.View(th, 60, 14))

	sel, ok := popup.menu.Selected()
	if !ok || sel.GetID() != "run00" {
		t.Fatalf("expected the running task selected, got %v", sel)
	}
	if !strings.Contains(frame, traySectionRunning) {
		t.Errorf("the running task is selected and visible with no %q heading:\n%s",
			traySectionRunning, frame)
	}
}

// Finished on its own -- no Running section at all -- was the second repro.
func TestTheOnlySectionIsNeverUnlabelled(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 0, 20)

	for i := 0; i < 20; i++ {
		popup.menu.MoveDown()
		frame := stripANSI(popup.View(th, 60, 12))
		if !strings.Contains(frame, traySectionFinished) {
			t.Fatalf("after %d rows the only heading is gone:\n%s", i+1, frame)
		}
	}
}

// The pinned heading must not print twice when the in-list copy is also at
// the top of the window.
func TestStickyHeadingIsNotDuplicated(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	popup.menu.Top()
	frame := stripANSI(popup.View(th, 60, 14))
	if n := strings.Count(frame, traySectionRunning); n != 1 {
		t.Errorf("%q appears %d times at the top of the list:\n%s",
			traySectionRunning, n, frame)
	}
}

// A click has to land on the row it visually sits on, with the pinned
// heading occupying a line and sometimes standing in for a row. View and
// handleMouse both derive that from stickyHeader for this reason.
func TestClickingARowWithAStickyHeadingSelectsThatRow(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 60, 14

	for _, scrolled := range []int{0, 3, 9, 17} {
		popup.menu.Top()
		for i := 0; i < scrolled; i++ {
			popup.menu.MoveDown()
		}
		_ = popup.View(th, w, h)

		_, _, listTop := popup.rowOffsets()
		_, dropped := popup.stickyHeader()
		rows := popup.menu.VisibleRows()[dropped:]

		for line, row := range rows {
			if row.Kind != array.RowItem {
				continue
			}
			want := row.Item.GetID()
			// +1 for the pinned heading's own line.
			popup.handleMouse(tea.MouseMsg{
				Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
				X: 4, Y: listTop + 1 + line,
			})
			got, _ := popup.menu.Selected()
			if got == nil || got.GetID() != want {
				t.Fatalf("scrolled=%d: clicking visual line %d selected %v, want %q",
					scrolled, line, got, want)
			}
		}

		// The pinned heading itself is not a row.
		before, _ := popup.menu.Selected()
		popup.handleMouse(tea.MouseMsg{
			Button: tea.MouseButtonLeft, Action: tea.MouseActionPress,
			X: 4, Y: listTop,
		})
		if after, _ := popup.menu.Selected(); after != before {
			t.Errorf("scrolled=%d: clicking the pinned heading changed the selection",
				scrolled)
		}
	}
}

// Under a filter the list is RANKED and interleaves sections, emitting no
// headings at all -- so a single pinned label would be actively wrong about
// where the reader is. It must switch off.
func TestStickyHeadingIsSuppressedUnderAFilter(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)

	if !popup.stickyActive() {
		t.Fatal("sticky should be active with no filter")
	}
	popup.setFilter("res")
	if popup.stickyActive() {
		t.Error("sticky is still active under a filter, over a ranked list")
	}
	frame := stripANSI(popup.View(th, 60, 14))
	for _, heading := range []string{traySectionRunning, traySectionFinished} {
		if strings.Contains(frame, heading) {
			t.Errorf("heading %q rendered over a filtered, ranked list:\n%s", heading, frame)
		}
	}

	// Clearing the filter brings it back.
	popup.setFilter("")
	if !popup.stickyActive() {
		t.Error("sticky did not come back when the filter cleared")
	}
}

// Opt-in: every consumer that does not ask for it renders exactly as before,
// INCLUDING Help, which does use sections.
func TestStickyHeadingIsOptIn(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	// Long enough that a short box cannot hold it, so the two configurations
	// have something to differ ABOUT. Five per section puts the second
	// heading far enough from the end that scrolling to the bottom pushes it
	// off a 5-row window but not off a 4-row one -- which is exactly the
	// situation the pinned line exists for.
	items := func() []*array.Item {
		var out []*array.Item
		for i := 0; i < 5; i++ {
			out = append(out, array.NewItem(fmt.Sprintf("a%d", i),
				fmt.Sprintf("Alpha %d", i)).SetSection("One"))
		}
		for i := 0; i < 5; i++ {
			out = append(out, array.NewItem(fmt.Sprintf("b%d", i),
				fmt.Sprintf("Beta %d", i)).SetSection("Two"))
		}
		return out
	}
	plain := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: items()}}})
	sticky := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: items()}}, StickySections: true})

	if plain.stickyActive() {
		t.Fatal("a popup that did not ask for sticky sections has them")
	}
	if !sticky.stickyActive() {
		t.Fatal("a popup that asked for them does not have them")
	}

	// At the TOP of a box tall enough for the WHOLE list, the two render
	// identically -- and that is correct, not a failure to exercise the
	// flag: the pinned line shows the same heading the inline one would, and
	// the inline copy yields to it. A sticky header is meant to be invisible
	// until it is needed. (16 rows: 12 list rows + the filter, its blank,
	// the footer and the pinned line.)
	if plain.View(th, 40, 16) != sticky.View(th, 40, 16) {
		t.Error("sticky changed the top-of-list rendering, which should be " +
			"indistinguishable from the inline heading it stands in for")
	}

	// Scrolled to the end of a box too short to hold the list, the opt-out
	// popup loses its heading and the opt-in one keeps it. This is the whole
	// behavioural difference, asserted in both directions.
	const w, h = 40, 8
	plain.menu.Bottom()
	sticky.menu.Bottom()
	plainOut := stripANSI(plain.View(th, w, h))
	stickyOut := stripANSI(sticky.View(th, w, h))

	if strings.Contains(plainOut, "Two") {
		t.Fatalf("the opt-out popup still shows its heading at %dx%d, so this "+
			"comparison proves nothing -- pick a size where it does not:\n%s",
			w, h, plainOut)
	}
	if !strings.Contains(stickyOut, "Two") {
		t.Errorf("scrolled, the opt-in popup lost its heading too:\n%s", stickyOut)
	}

	// And the opt-out popup still shows its first heading inline, losing no
	// row to a pinned line.
	plain.menu.Top()
	if !strings.Contains(stripANSI(plain.View(th, 40, 16)), "One") {
		t.Error("the opt-out popup lost its inline heading")
	}
}

// A list with no sections at all must not reserve a pinned line, even with
// the flag on — it would be a blank row stealing space from the list.
func TestNoSectionsMeansNoPinnedLine(t *testing.T) {
	popup := NewPopup(PopupConfig{StickySections: true, Tabs: []PopupTab{{Items: []*array.Item{
		array.NewItem("a", "Alpha"), array.NewItem("b", "Beta"),
	}}}})
	if popup.hasSections() {
		t.Fatal("hasSections true with no sections set")
	}
	if popup.stickyActive() {
		t.Error("a pinned line is reserved for a list with no sections")
	}
}

// ---------------------------------------------------------------------------
// Hit-testing: a screen line resolves to the row drawn there, or to nothing
// ---------------------------------------------------------------------------

// clickResult is what one synthetic click did.
type clickResult struct {
	selectedID string
	chose      bool
}

func clickLine(p *Popup, y int) clickResult {
	before, _ := p.menu.Selected()
	_, cmd := p.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 4, Y: y,
	})
	after, _ := p.menu.Selected()
	id := ""
	if after != nil {
		id = after.GetID()
	}
	// chooseSelected having run is what opens a result. It is detectable as
	// "the selection moved", plus the cmd for the cases that produce one --
	// a click that resolves to the ALREADY-selected row also chooses, which
	// is correct and is why the id is reported rather than just a bool.
	return clickResult{selectedID: id, chose: before != after || cmd != nil}
}

// itemDrawnAt returns the id of the item Popup's last render put on screen
// line y, or "" when that line is not a drawn item row.
//
// Derived from the SAME recorded geometry handleMouse uses, which is the
// point: if the two ever disagree this helper and the hit-test are wrong
// together, and the independent frame-text check below catches it.
func itemDrawnAt(p *Popup, y int) string {
	g := p.geom
	if !g.drawn || y < g.rowsTop || y >= g.rowsTop+g.rowCount {
		return ""
	}
	rows := p.menu.VisibleRows()
	i := y - g.rowsTop + g.dropped
	if i < 0 || i >= len(rows) || rows[i].Kind != array.RowItem || rows[i].Item == nil {
		return ""
	}
	return rows[i].Item.GetID()
}

// The bug, at every screen line and every scroll offset: a click may only
// ever resolve to the task actually drawn on that line.
//
// What it replaces: handleMouse's list case was `m.Y >= listTop`, an
// unbounded half-plane. The pinned heading, the hint line, the footer and any
// blank tail all sit below listTop, so all four resolved to a row offset --
// and array.Menu.ClickRow bounded that offset against the WHOLE list rather
// than the visible window, so it happily selected and opened a task seven
// rows below the window. Measured before the fix, 22 tasks in a 15-row box at
// the top of the list: the blank line opened fin07, the hint line fin08, and
// the footer fin11, none of which were on screen.
func TestOnlyADrawnRowRespondsToAClick(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 66, 15

	check := func(where string) {
		frame := stripANSI(popup.View(th, w, h))
		lines := strings.Split(frame, "\n")

		for y := 0; y < len(lines); y++ {
			want := itemDrawnAt(popup, y)
			// Re-establish a known selection so "did not move" is
			// distinguishable from "re-selected the same row".
			popup.menu.Top()
			_ = popup.View(th, w, h)
			anchor, _ := popup.menu.Selected()
			anchorID := anchor.GetID()

			// The frame may differ after Top(); re-ask for this line.
			want = itemDrawnAt(popup, y)
			got := clickLine(popup, y)

			if want == "" {
				if got.selectedID != anchorID || got.chose {
					t.Errorf("%s: line %d draws no task row, but a click there "+
						"selected %q (chose=%v)\nline was %q\n%s",
						where, y, got.selectedID, got.chose,
						strings.TrimRight(lines[y], " "), frame)
					return
				}
				continue
			}
			if got.selectedID != want {
				t.Errorf("%s: line %d draws %q but a click there selected %q\n%s",
					where, y, want, got.selectedID, frame)
				return
			}
		}
	}

	check("fresh")
	popup.menu.Bottom()
	check("Bottom()")
	for i := 0; i < 6; i++ {
		popup.menu.Top()
		for n := 0; n <= i*3; n++ {
			popup.menu.MoveDown()
		}
		check(fmt.Sprintf("down x%d", i*3+1))
	}
}

// The exact boundary reported: scrolled so the LAST task sits on the bottom
// row of the box, clicking the footer and the hint must do nothing at all.
// Checked for the keyboard and the mouse wheel separately, since they move
// the cursor by different code paths (move() vs ScrollDown()).
func TestFooterAndHintAreNeverClickable(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	const w, h = 66, 15

	for _, how := range []string{"keyboard", "wheel"} {
		_, popup := buildLongTray(t, 1, 21)
		_ = popup.View(th, w, h)

		// Drive to the very end the way a user would.
		for i := 0; i < 60; i++ {
			if how == "keyboard" {
				popup.menu.MoveDown()
			} else {
				popup.menu.ScrollDown()
			}
			_ = popup.View(th, w, h)
		}
		frame := stripANSI(popup.View(th, w, h))
		lines := strings.Split(frame, "\n")

		last, _ := popup.menu.Selected()
		if last == nil || last.GetID() != "fin20" {
			t.Fatalf("%s: expected to be at the last task, got %v", how, last)
		}

		g := popup.geom
		// Every line at or below the end of the drawn rows: the blank tail
		// (if any), the hint, and the footer.
		for y := g.rowsTop + g.rowCount; y < len(lines); y++ {
			before, _ := popup.menu.Selected()
			got := clickLine(popup, y)
			after, _ := popup.menu.Selected()
			if after != before || got.chose {
				t.Errorf("%s: clicking line %d (%q) below the last drawn row "+
					"selected %q and chose=%v\n%s",
					how, y, strings.TrimRight(lines[y], " "),
					got.selectedID, got.chose, frame)
			}
		}
	}
}

// The same bound, exercised through the REAL tray fixture rather than a
// hand-built menu.
//
// The authoritative component-level test is
// array.TestClickRowRejectsOffsetsPastTheVisibleWindow, which lives beside
// the code it guards so `go test ./frontend/internal/array/` runs it. This
// one is not redundant: it drives the same bound through the geometry the
// tray actually produces, which is where the mis-mapping originated.
func TestMenuClickRowRejectsOffsetsOutsideTheWindow(t *testing.T) {
	_, popup := buildLongTray(t, 1, 21)
	popup.menu.SetHeight(5)
	popup.menu.Top()

	visible := len(popup.menu.VisibleRows())
	if visible != 5 {
		t.Fatalf("window = %d rows, expected 5", visible)
	}
	before, _ := popup.menu.Selected()

	for _, offset := range []int{visible, visible + 1, visible + 5, -1, -10} {
		if popup.menu.ClickRow(offset) {
			t.Errorf("ClickRow(%d) resolved with a %d-row window", offset, visible)
		}
	}
	if after, _ := popup.menu.Selected(); after != before {
		t.Error("an out-of-window ClickRow moved the selection")
	}

	// In-window offsets still work, including the last one.
	if !popup.menu.ClickRow(visible - 1) {
		t.Error("ClickRow rejected the last row of the window")
	}
}

// Enter chooses whatever is SELECTED, and the selection must always be a row
// inside the window -- otherwise Enter has the same defect the click had.
func TestSelectionIsAlwaysInsideTheVisibleWindow(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 66, 15

	assertInWindow := func(where string) {
		_ = popup.View(th, w, h)
		sel, ok := popup.menu.Selected()
		if !ok {
			t.Fatalf("%s: nothing selected", where)
		}
		for _, row := range popup.menu.VisibleRows() {
			if row.Kind == array.RowItem && row.Item == sel {
				return
			}
		}
		t.Errorf("%s: the selected row %q is not in the visible window",
			where, sel.GetID())
	}

	assertInWindow("fresh")
	for i := 0; i < 30; i++ {
		popup.menu.MoveDown()
		assertInWindow(fmt.Sprintf("down x%d", i+1))
	}
	for i := 0; i < 30; i++ {
		popup.menu.MoveUp()
		assertInWindow(fmt.Sprintf("up x%d", i+1))
	}
	popup.menu.Bottom()
	assertInWindow("Bottom()")
	popup.menu.Top()
	assertInWindow("Top()")
}

// The pinned heading's own line is not a row, at any scroll offset.
func TestPinnedHeadingLineIsNeverClickable(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 66, 15

	for _, scrolled := range []int{0, 1, 5, 12, 21} {
		popup.menu.Top()
		for i := 0; i < scrolled; i++ {
			popup.menu.MoveDown()
		}
		_ = popup.View(th, w, h)
		_, _, listTop := popup.rowOffsets()

		before, _ := popup.menu.Selected()
		got := clickLine(popup, listTop)
		if after, _ := popup.menu.Selected(); after != before || got.chose {
			t.Errorf("scrolled=%d: clicking the pinned heading at y=%d selected "+
				"%q (chose=%v)", scrolled, listTop, got.selectedID, got.chose)
		}
	}
}

// No blank gap between the last list row and the hint/footer, at any scroll
// offset.
//
// The pinned heading standing in for the first visible row used to cost a
// line: at the top of the list the list area drew one row fewer than it had
// room for, which read as a blank line above the hint that vanished as you
// scrolled further down, with another task filling it. View now asks the menu
// for one extra row when the heading stands in for one, so the list area is
// always fully used.
//
// Note what is NOT asserted: that the number of TASKS is constant. An inline
// section boundary ("Finished") is a row and legitimately takes a line, so a
// window showing a boundary shows one task fewer than one that does not. That
// is correct; the blank line was not.
func TestNoBlankGapBelowTheLastListRow(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	_, popup := buildLongTray(t, 1, 21)
	const w, h = 66, 15

	var wantRows int
	at := func(where string) {
		frame := stripANSI(popup.View(th, w, h))
		lines := strings.Split(frame, "\n")
		g := popup.geom

		// The list area is fully used -- same row count every time, while
		// there are more rows than fit.
		if wantRows == 0 {
			wantRows = g.rowCount
			if wantRows == 0 {
				t.Fatalf("%s: no rows drawn at all", where)
			}
		} else if g.rowCount != wantRows {
			t.Errorf("%s: %d row lines drawn, %d elsewhere -- the list area is "+
				"not fully used at every offset\n%s", where, g.rowCount, wantRows, frame)
			return
		}

		// And no drawn row line is blank.
		for y := g.rowsTop; y < g.rowsTop+g.rowCount; y++ {
			if y >= len(lines) {
				t.Errorf("%s: row line %d is past the frame", where, y)
				return
			}
			if strings.TrimSpace(lines[y]) == "" {
				t.Errorf("%s: line %d is a blank gap inside the list area\n%s",
					where, y, frame)
				return
			}
		}
	}

	popup.menu.Top()
	at("Top()")
	for i := 0; i < 20; i++ {
		popup.menu.MoveDown()
		at(fmt.Sprintf("down x%d", i+1))
	}
	popup.menu.Bottom()
	at("Bottom()")
	for i := 0; i < 20; i++ {
		popup.menu.MoveUp()
		at(fmt.Sprintf("up x%d", i+1))
	}
}
