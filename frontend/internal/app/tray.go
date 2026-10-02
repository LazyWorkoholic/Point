// F9a — the task tray (UI-05).
//
// Where background work becomes visible from anywhere. The tray is Root's,
// not a workspace's, for the same reason the Task card's ListTasks cycle
// already is (tasks.go's own doc): the list is GLOBAL. A task whose
// originating view has been unloaded still appears here and still opens,
// which is F09's Done-when #1 and the whole point of the episode.
//
// # What this file does NOT do
//
// F09's spec has three cards. This builds the tray and the result card's
// render/discard half. The diff card, attach-from-a-card and save-to-memory
// are all deferred, each on a dependency that does not exist — see
// design/completed/frontend/F09a-task-tray-cards.md for the four blockers
// with their evidence, and WORKFLOW.md sessions 17b/18b/23b for where they
// resume.
package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// TaskTrayView marks an Overlay as the task tray, so Root can tell that the
// global task snapshot is on screen even though the right pane is not.
//
// The same narrow-marker pattern RightPaneFallback already uses, and for the
// identical reason recorded there: refreshTasks sends NOTHING while the thing
// that displays its answer is hidden (Done-when #6 of F6 — "no subscriptions
// while hidden"), so a surface that shows tasks and is not the right pane
// must announce itself or it renders a snapshot frozen at the moment it
// opened. For the right-pane fallback that meant a Task section that never
// updated; for the tray it would mean a running task whose elapsed clock
// never ticks and a finished one that never moves out of Running.
type TaskTrayView interface {
	Overlay
	IsTaskTray() bool
}

// trayState is Root's bookkeeping for one open tray, plus the in-flight
// result fetch a selection starts. Cleared to its zero value on close.
type trayState struct {
	popup *Popup

	// rows is the snapshot the popup's items were built from — kept so
	// refreshTray can tell a real change from a no-op, and so a selection
	// can resolve an item id back to its row without re-querying.
	rows []TaskRow

	// resultReqID / resultTask are the GetSession round trip a selection on
	// a finished task starts. resultTask is what the reply is filtered by:
	// GetSession answers with the whole session, and only the messages
	// carrying this task_id are this task's result.
	resultReqID string
	resultTask  TaskRow
}

func (r *Root) trayOpen() bool {
	if r.tray.popup == nil {
		return false
	}
	_, ok := r.overlay.(TaskTrayView)
	return ok
}

func (r *Root) resetTray() { r.tray = trayState{} }

// ---------------------------------------------------------------------------
// Opening
// ---------------------------------------------------------------------------

// openTaskTray is the tray action's handler.
//
// It opens against whatever snapshot Root already holds and then asks for a
// fresh one. Opening empty and waiting would be wrong twice over: Root's
// snapshot is usually current (every TaskStarted and every terminal event
// already triggers a refresh — handleTaskListEnvelope), and a tray that
// renders "Loading…" over a list it could already draw is slower to read
// than one that draws the list and quietly replaces it.
func (r *Root) openTaskTray() tea.Cmd {
	rows := r.trayRows()
	popup := NewPopup(PopupConfig{
		Title: "Tasks",
		Tabs:  []PopupTab{{Items: trayItems(rows)}},
		// LAYOUT.md's overlay table: "task tray 50×12".
		MinW: 50, MinH: 12,
		OnSelect: r.onTraySelect,
		Decor:    r.trayDecor,
		// Running / Queued / Finished must never scroll away from the rows
		// they label -- with 20+ finished tasks the headings are off screen
		// most of the time otherwise. See PopupConfig.StickySections.
		StickySections: true,
		Hint:           r.trayHint,
	})
	tray := &taskTray{Popup: popup}

	r.tray = trayState{popup: popup, rows: rows}
	r.OpenOverlay(tray)
	if r.overlay != tray {
		// Too narrow even for 50×12 — OpenOverlay already refused and set
		// the status line with the real numbers. Nothing to refresh for.
		r.resetTray()
		return nil
	}
	// Not refreshTasksIfVisible: that fires from Root.Update on the
	// transition it detects, and this open IS that transition — but it
	// happens inside this message's own handling, before Update's tail
	// runs, so asking here is what makes the first snapshot arrive without
	// waiting for an unrelated later message to notice.
	r.refreshTasks()
	return nil
}

// taskTray is a Popup that answers IsTaskTray. Embedding rather than
// wrapping: every key, mouse and render behaviour is the shared Popup's, and
// the only thing this type adds is the marker Root needs to keep the
// snapshot live. Update has to be overridden so the embedded Popup returning
// *itself* does not silently replace the overlay with the bare Popup and
// drop the marker — which would stop the refresh at the first keystroke.
type taskTray struct {
	*Popup
}

func (t *taskTray) IsTaskTray() bool { return true }

// PreferredSize: whatever it is offered, which is the transcript pane's rect
// less a cell per side. A task list and a 50-column box are a bad match —
// LAYOUT.md's "task tray 50×12" is kept as the MinSize floor (it is what
// overlayFits refuses below) but the tray asks for the room it is actually
// given. The tray and the result card return the same answer on purpose; see
// matchTranscriptPane.
func (t *taskTray) PreferredSize(availW, availH int) (int, int) {
	return matchTranscriptPane(availW, availH)
}

func (t *taskTray) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	next, cmd := t.Popup.Update(msg)
	if next == nil {
		return nil, cmd
	}
	if p, ok := next.(*Popup); ok && p == t.Popup {
		return t, cmd
	}
	return next, cmd
}

// trayRows is Root's snapshot, filtered to BACKGROUND work and sorted.
//
// # Why the tray is not every task
//
// F09's "Global" bullet says "tasks from every **workspace**, since the root
// owns the list", and Done-when #1 says "lists tasks from every
// **workspace**". Read literally, that is a statement about SCOPE across
// workspaces and sessions — a task whose originating view is unloaded still
// appears — not a claim that every task record belongs here regardless of
// how it ran.
//
// And the episode's own Purpose is "where **background work** becomes
// visible and actionable". An inline turn has no background to become
// visible from: its reply is already in the transcript, where `o` expands it
// in place. A tray row for it offers nothing the transcript does not already
// show, and B11's research config says so from the other direction —
// "Always a background task -- it is the reason background tasks, result
// cards, and cancellation have to genuinely work" — as does
// project_idea.md's own line that research "takes 60+ seconds, so it *must*
// be a background task, which forces the task panel, result cards, attach
// modes, and cancellation to all genuinely work."
//
// So: background work only. Today that is research, by rule and not by
// hardcode — `app.IsBackgroundAgent` stands in for the backend's
// `max_steps > 1`, and a `TaskStarted.background` this client actually saw
// PROMOTES a task in regardless of what the table guesses (see
// Root.noteTaskBackground). Promote-only: the costly error here is hiding a
// real background result, not showing one extra row.
//
// **One spec disagreement, recorded rather than resolved.**
// project_idea.md:259 says the Filler-Explainer invoked "from chat" is "a
// background task and a result card", which would put it here. The CODE
// makes it inline — `AgentConfig` gives it `max_steps=1`, and
// runtime.py:822's `is_bounded_loop` is `max_steps > 1`. Per CLAUDE.md the
// code wins, so filler-explainer is excluded. If that is wrong it is a
// backend change (its `max_steps`, or a real `background` field), not a
// frontend filter.
func (r *Root) trayRows() []TaskRow {
	kept := make([]TaskRow, 0, len(r.tasks))
	for _, t := range r.tasks {
		if IsBackgroundAgent(t.Agent) || r.backgroundSeen[t.ID] {
			kept = append(kept, t)
		}
	}
	return SortTaskRows(kept)
}

// noteTaskBackground records TaskStarted's authoritative `background` flag.
//
// B11 computes it as `agent.max_steps > 1` (runtime.py:822) and rides it out
// on TaskStarted (runtime.py:1383) precisely so a client can tell without
// knowing the agent's config. ListTasks does not carry it, so this is the
// only place it is ever available — and only for tasks that started while
// this client was connected.
//
// Recorded for TRUE only. A false flag adds nothing (the table already
// defaults to not-background) and storing it would invite a later reader to
// treat the absence of a record as "known inline", which it is not: a
// durable row from a previous run has no record either way.
func (r *Root) noteTaskBackground(env protocol.Envelope) {
	if env.TaskID == "" {
		return
	}
	var p struct {
		Background bool `json:"background"`
	}
	if err := env.UnmarshalPayload(&p); err != nil || !p.Background {
		return
	}
	if r.backgroundSeen == nil {
		r.backgroundSeen = map[string]bool{}
	}
	r.backgroundSeen[env.TaskID] = true
}

// pruneBackgroundSeen drops records for tasks the snapshot no longer lists.
//
// ListTasks answers with up to `limit` recent rows, so a task that has aged
// out of it can never appear in the tray again and its record is dead
// weight. Without this the map grows once per background task for the life
// of the process.
func (r *Root) pruneBackgroundSeen() {
	if len(r.backgroundSeen) == 0 {
		return
	}
	live := make(map[string]bool, len(r.tasks))
	for _, t := range r.tasks {
		live[t.ID] = true
	}
	for id := range r.backgroundSeen {
		if !live[id] {
			delete(r.backgroundSeen, id)
		}
	}
}

func (r *Root) trayHint() string {
	if len(r.tray.rows) == 0 {
		return ""
	}
	if r.tray.resultReqID != "" {
		return "loading the result…"
	}
	return "⏎ opens a finished result · offers cancel on a running one"
}

// ---------------------------------------------------------------------------
// Items
// ---------------------------------------------------------------------------

// Section headings, in the order F09's mockup shows them. array.Menu inserts
// a header row every time an item's Section changes and keeps the items in
// the order they were given (menu.go's sectionedRows), so the order here IS
// the rendered order — which is why rows are sorted by TaskPriority first.
const (
	traySectionRunning  = "Running"
	traySectionQueued   = "Queued"
	traySectionFinished = "Finished"
)

func traySection(state string) string {
	switch TaskPriority(state) {
	case 0:
		return traySectionRunning
	case 1:
		return traySectionQueued
	default:
		return traySectionFinished
	}
}

// trayItems turns a sorted snapshot into Popup items.
//
// The item id is the task id, which is what OnSelect resolves back to a row.
// The binding column carries the state note — elapsed time while running,
// "queued", the failure's own error code, or the wall-clock duration once
// finished — because that is the column Popup draws flush-right, which is
// where F09's mockup puts it.
//
// An empty snapshot returns ONE disabled row rather than nothing. A popup
// with zero items renders an empty box whose footer indicator is blank, and
// "no background tasks" is the answer to the question the user just asked;
// an empty frame is not distinguishable from a tray that failed to load.
func trayItems(rows []TaskRow) []*array.Item {
	if len(rows) == 0 {
		return []*array.Item{
			array.NewItem("__empty__", "no background tasks").SetDisabled(true),
			// Literally accurate now that trayRows filters to background
			// work: an inline turn's reply is in the transcript, not here.
		}
	}
	items := make([]*array.Item, 0, len(rows))
	for _, t := range rows {
		// NO glyph and NO note baked in. Both are state that changes while
		// the item does not — the glyph's colour and the elapsed clock —
		// and an item is a snapshot. trayDecor supplies them per render.
		items = append(items, array.NewItem(t.ID, TaskLabel(t)).
			SetSection(traySection(t.State)).
			SetDescription(t.Session))
	}
	return items
}

// trayDecor is each row's glyph, its colour, and its right-hand note —
// recomputed on every render, which is the only way the m:ss clock can
// advance (see RowDecor, and trayNote).
//
// The glyph is handed over AS a glyph with its role, not concatenated into
// the title. Inside the title, Popup rendered it with the title's own
// TextPrimary role, so a ✓ or ✕ came out uncoloured in the tray while the
// right-pane Task card coloured the same character via app.TaskRole — one
// shared TaskGlyph producing two different colours, which is exactly what
// sharing it was meant to prevent.
//
// An unknown item id (the "__empty__" placeholder) gets no decor at all: it
// is a sentence, not a task, and a state glyph beside it would imply a state.
func (r *Root) trayDecor(it *array.Item) RowDecor {
	row, ok := r.trayRow(it.GetID())
	if !ok {
		return RowDecor{}
	}
	return RowDecor{
		Icon:     TaskGlyph(row.State),
		IconRole: TaskRole(row.State),
		Note:     trayNote(row),
	}
}

// trayNote is the right-hand column, read fresh on every render through
// trayDecor.
//
// Fresh matters for exactly one of these branches and is why RowDecor exists:
// TaskElapsed reads the wall clock, so a running task's note is a different
// string every second. Baked into the item it froze — 0:00 on open, moving
// only when something rebuilt the items.
//
// For a failure it is the backend's own error_code, which ListTasks does
// carry — F09's mockup shows exactly that ("failed: tool_timeout"), and it is
// the one case where the mockup's label and the wire agree. For a finished
// task it is the wall duration and NOT a token count: see TaskLabel's doc.
func trayNote(t TaskRow) string {
	switch t.State {
	case TaskStateRunning:
		if e := TaskElapsed(t.StartedAt); e != "" {
			return e
		}
		return "running"
	case TaskStateQueued:
		return "queued"
	case TaskStateFailed:
		if t.ErrorCode != "" {
			return "failed: " + t.ErrorCode
		}
		return "failed"
	case TaskStateCancelled:
		return "cancelled"
	default:
		if d := TaskDuration(t); d != "" {
			return d
		}
		return "done"
	}
}

// ---------------------------------------------------------------------------
// Live refresh
// ---------------------------------------------------------------------------

// refreshTray re-renders the open tray against Root's current snapshot.
//
// Called from handleTaskListEnvelope, on the same two triggers the Task card
// already refreshes on: Root's own ListTasks reply landing, and any
// TaskStarted or terminal event from any workspace. Never a timer — so a
// running task's elapsed clock advances once per lifecycle event rather than
// once a second. That is a deliberate consequence of F6's no-timer rule, not
// an oversight: the alternative is a tick loop whose only job is redrawing a
// clock, and B12 emits no push event for a scheduler state change, so a
// ticker would still not make the LIST more truthful — only the seconds
// column.
func (r *Root) refreshTray() {
	if !r.trayOpen() {
		return
	}
	rows := r.trayRows()
	if sameTrayRows(r.tray.rows, rows) {
		return
	}
	r.tray.rows = rows
	r.tray.popup.RefreshItems(trayItems(rows))
}

// sameTrayRows reports whether two snapshots would render identically.
//
// Compared field by field over the fields trayItems actually reads, not with
// a whole-struct equality: CreatedAt/StartedAt/FinishedAt are float seconds
// that never change for a given task, so including them would be harmless,
// but `live` (which ListTasks sets and TaskRow does not even decode) and any
// field a later episode adds would start forcing rebuilds for changes nothing
// on screen reflects. A rebuild is not free — it resets nothing now that
// RefreshItems preserves the filter and the selection, but it does discard
// the fuzzy-match highlight positions and re-rank, which flickers.
func sameTrayRows(a, b []TaskRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].State != b[i].State ||
			a[i].ErrorCode != b[i].ErrorCode ||
			a[i].Agent != b[i].Agent ||
			a[i].Kind != b[i].Kind ||
			a[i].StartedAt != b[i].StartedAt ||
			a[i].FinishedAt != b[i].FinishedAt {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// The m:ss clock
// ---------------------------------------------------------------------------

// trayClockInterval is how often the tray redraws to advance a running
// task's m:ss. One second: the clock shows seconds, so anything slower makes
// it visibly skip and anything faster redraws a frame that reads identically.
const trayClockInterval = time.Second

type trayTickMsg struct{ gen int }

func trayTick(gen int) tea.Cmd {
	return tea.Tick(trayClockInterval, func(time.Time) tea.Msg {
		return trayTickMsg{gen: gen}
	})
}

// trayClockWanted: is there a clock on screen that needs advancing?
//
// Both halves matter. The tray has to be the open overlay, and it has to
// hold at least one row whose note is time-derived — a tray showing only
// finished tasks has nothing that changes, and ticking for it would redraw
// the identical frame once a second forever.
func (r *Root) trayClockWanted() bool {
	if !r.trayOpen() {
		return false
	}
	for _, t := range r.tray.rows {
		if t.State == TaskStateRunning {
			return true
		}
	}
	return false
}

// ensureTrayClock starts the redraw loop if one is wanted and not already
// running. It never STOPS one: a tick that finds nothing to advance simply
// does not re-arm, which is the only way to end a tea.Tick that has already
// been scheduled — the same shape, and the same reason, as
// ensureAnimation's.
//
// # Why a timer here, when F6 was explicit about not having one
//
// F6's rule is about REQUESTS: "no subscriptions while hidden", and
// refreshTasks still sends nothing except on a real lifecycle event. This
// sends nothing at all. It is a local redraw of state Root already holds,
// and it exists because TaskElapsed reads the wall clock — which no backend
// event will ever notify us about.
//
// Without it the clock only advanced when something else happened to cause a
// frame, which is exactly what was observed: opened at 0:00, reopened later
// at 1:03, correct at 1:43 only because the task finishing forced a redraw.
// The alternative — asking the backend once a second — would be a request
// per second for a number the backend does not even hold.
func (r *Root) ensureTrayClock() tea.Cmd {
	if !r.trayClockWanted() || r.trayClock.running {
		return nil
	}
	r.trayClock.running = true
	r.trayClock.gen++
	return trayTick(r.trayClock.gen)
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

// onTraySelect is ⏎ (and a click) on a row.
//
// Finished → fetch and open the result card. Running or queued → offer
// cancel. Both leave the tray open until the thing they opened is actually
// open: returning `false` for "closes" on the finished path means a fetch
// that fails leaves the user looking at the tray they chose from, not at an
// empty screen with a status line.
func (r *Root) onTraySelect(it *array.Item) (tea.Cmd, bool) {
	row, ok := r.trayRow(it.GetID())
	if !ok {
		return nil, false
	}
	if TaskFinished(row.State) {
		return r.fetchTaskResult(row), false
	}
	// Running or queued: confirm, then cancel. The confirm REPLACES the
	// tray as the overlay (Root holds one at a time), and declining closes
	// it rather than returning to the tray — a two-level overlay stack is
	// F12's business, not this episode's, and faking one here with a
	// reopen-on-decline would re-run the tray's own open path, including
	// its fit check and its refresh, for a keypress that chose nothing.
	label := TaskLabel(row)
	return func() tea.Msg {
		return openOverlayMsg{NewConfirm(
			"Cancel task",
			fmt.Sprintf("Stop %s? Whatever it has produced so far is kept.", label),
			"Cancel it", "Leave it running",
			r.cancelTaskCmd(row.ID),
		)}
	}, false
}

func (r *Root) trayRow(id string) (TaskRow, bool) {
	for _, t := range r.tray.rows {
		if t.ID == id {
			return t, true
		}
	}
	return TaskRow{}, false
}

// openOverlayMsg lets a tea.Cmd ask Root to open an overlay. OnSelect runs
// inside the Popup's own Update, so assigning r.overlay from there would be
// Root mutating the field whose value that same Update call is about to
// return — setOverlay's "the ONE place r.overlay is ever assigned" rule
// (root.go) exists precisely to stop that.
type openOverlayMsg struct{ o Overlay }

func (r *Root) cancelTaskCmd(taskID string) tea.Cmd {
	return func() tea.Msg { return cancelTaskMsg{taskID} }
}

type cancelTaskMsg struct{ taskID string }

// sendCancelTask cancels one task BY ID, which is not what chat's ctrl+c
// does: that cancels the workspace's own active stream (app\stream.go's
// cancelStreamCmd, handled inside chat). The tray can be looking at a task
// belonging to a session no workspace currently has loaded, so it addresses
// the task directly.
//
// Fire-and-forget, like chat's own sendCancel: the ack
// (task.cancel.accepted) is not terminal, and the real outcome arrives later
// as TaskCancelled, which handleTaskListEnvelope already treats as a reason
// to refresh. `accepted: false` means the task had already finished — from
// here that is not an error worth a modal, and the refreshed snapshot says
// so by showing it finished.
func (r *Root) sendCancelTask(taskID string) {
	if r.client == nil || taskID == "" {
		return
	}
	raw, err := json.Marshal(map[string]any{"task_id": taskID})
	if err != nil {
		return
	}
	_ = r.client.Send(protocol.Envelope{
		ID: rootRequestID(), Type: protocol.CmdCancelTask, Payload: raw,
	})
	r.log.Debug("tray: cancel requested", "task", taskID)
}

// ---------------------------------------------------------------------------
// Fetching a result
// ---------------------------------------------------------------------------

// fetchTaskResult asks for the session a finished task belongs to.
//
// GetSession, not a dedicated event: there is no GetTaskResult on the wire
// and `tasks` has no result column. What a task produced is its assistant
// message(s), joined by `messages.task_id` — the same join B10's
// `load_task_result` tool uses in-process, and the same column S11b's reload
// pairing keys on. The whole session comes back, which is more than this
// needs, and that is the honest cost of reusing an event rather than
// inventing one: a per-task result event is a backend episode, and this
// episode does not get to grow one.
//
// The fetch is keyed to the task, not the session, so a reply for a task the
// user has since navigated away from is dropped rather than opening a card
// they did not ask for.
func (r *Root) fetchTaskResult(row TaskRow) tea.Cmd {
	// The PERMANENT refusal is checked before the TRANSIENT one, so the
	// reason named is the one the user can act on. A session-less row will
	// never be fetchable however long they wait, and telling them the
	// backend is disconnected sends them to reconnect for nothing. B12's
	// own fix for the same shape: a refusal has to name which of the two
	// problems it actually is.
	if row.Session == "" {
		// A durable row whose session was deleted — the ON DELETE CASCADE
		// takes the messages with it, so there is nothing to fetch and
		// saying so beats a round trip that comes back empty.
		r.status = "that task has no session on record — its result is gone"
		return nil
	}
	if r.client == nil {
		r.status = "the backend is not connected"
		return nil
	}
	raw, err := json.Marshal(map[string]any{"session_id": row.Session})
	if err != nil {
		return nil
	}
	env := protocol.Envelope{ID: rootRequestID(), Type: protocol.CmdGetSession, Payload: raw}
	if _, err := r.client.Request(env, 0); err != nil {
		r.status = "could not ask for that result: " + err.Error()
		return nil
	}
	r.tray.resultReqID = env.ID
	r.tray.resultTask = row
	r.log.Debug("tray: fetching a task result",
		"task", row.ID, "session", row.Session, "corr_id", env.ID)
	return nil
}

// handleTrayEnvelope claims the tray's own GetSession reply by corr_id — the
// same pattern handleTaskListEnvelope and chat's pending map both use, and
// the reason chat ignores this reply: its corr_id was never in chat's own
// pending map (chat\protocol.go: "not one of ours"), so a tray fetch cannot
// make the Chat workspace switch sessions.
func (r *Root) handleTrayEnvelope(env protocol.Envelope) {
	if r.tray.resultReqID == "" || env.CorrID != r.tray.resultReqID {
		return
	}
	r.tray.resultReqID = ""
	if r.client != nil {
		r.client.Release(env.CorrID)
	}
	row := r.tray.resultTask
	r.tray.resultTask = TaskRow{}

	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		if p.Error == "" {
			p.Error = "the backend reported an error"
		}
		r.status = "could not load that result: " + p.Error
		r.log.Warn("tray: result fetch refused", "task", row.ID, "err", p.Error)
		return
	}

	var p struct {
		Session struct {
			Messages []trayMessage `json:"messages"`
		} `json:"session"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		r.status = "could not read that result"
		r.log.Warn("tray: result payload did not decode", "task", row.ID, "err", err)
		return
	}
	// `back` is the tray itself, so d/esc on the card RETURNS to the list
	// rather than closing everything. Captured here because this is the
	// only place that knows the card was opened from a tray — a card opened
	// from anywhere else gets a nil `back` and closes, as before.
	//
	// The same self-returned-overlay swap S13d built for UI-09's context
	// card: an Overlay's Update may return a DIFFERENT Overlay, and
	// setOverlay applies it through the identical MinSize floor a fresh
	// OpenOverlay enforces (root.go). Second consumer of a proven pattern,
	// not new machinery.
	card := newResultCard(row, p.Session.Messages)
	if tray, ok := r.overlay.(TaskTrayView); ok {
		card.back = tray
	}
	r.OpenOverlay(card)
	if r.overlay != card {
		// The result card is 60×18 and the tray is 50×12, so a terminal
		// wide enough to have opened the tray can still be too small for
		// the card. OpenOverlay has already said so with the real numbers;
		// the tray stays open, which is the state the user can act from.
		return
	}
	// The tray is no longer the overlay, so trayOpen() is false from here
	// and the snapshot stops being refreshed -- correct, since nothing is
	// displaying it. r.tray.rows are deliberately left in place rather
	// than cleared: they cost nothing, and they are what a reopened tray
	// draws from before its first refresh lands.
	//
	// Closing the card (esc or d) returns to the TRAY it was opened from,
	// via card.back — so reviewing three results is three ⏎/esc pairs, not
	// three F1 presses. esc from the tray itself still closes outright, so
	// the two levels remain distinguishable.
	r.log.Debug("tray: result card opened", "task", row.ID,
		"returns_to_tray", card.back != nil)
}

// trayMessage is the subset of a persisted message this file reads.
//
// Deliberately a local shape, not chat\model.go's wireMessage: `chat` imports
// `app`, so that type is unreachable here, and the three fields below are all
// this needs. Named differently so the two never read as the same type —
// wireMessage decodes B12's outcome columns and the invocation block, none of
// which a result card renders.
type trayMessage struct {
	TaskID  string `json:"task_id"`
	Role    string `json:"role"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// trayRoleUser / trayRoleAssistant are transcribed from managers\message.py's
// MessageRole, as plain strings for the same reason tasks.go keeps the state
// names as strings: a role this build does not recognise must still be
// skippable rather than crash a render.
const (
	trayRoleUser      = "user"
	trayRoleAssistant = "assistant"
)

// splitTaskMessages joins a task to its own messages and returns the question
// and the answer.
//
// Keyed on task_id and NOTHING else — the same rule reload.go established
// permanently for pairing a reply to its question, for the same reason:
// adjacency, arrival order and elapsed time all break the moment two sends
// overlap, and B12 added this column precisely so they do not have to be
// guessed at.
//
// Multiple assistant messages for one task are joined, not reduced to the
// last: a tool loop persists more than one, and `load_task_result` joins them
// the same way ("\n\n".join over the assistant texts).
func splitTaskMessages(taskID string, msgs []trayMessage) (question, answer string, tokens int) {
	var answers []string
	for _, m := range msgs {
		if m.TaskID != taskID {
			continue
		}
		switch m.Role {
		case trayRoleUser:
			if question == "" {
				question = strings.TrimSpace(m.Content.Text)
			}
		case trayRoleAssistant:
			if t := strings.TrimSpace(m.Content.Text); t != "" {
				answers = append(answers, t)
			}
			// Summed across every message of the turn, not taken from one:
			// a tool loop's cost is the whole loop's. TotalTokens is what
			// the accumulator persisted; falling back to the two halves
			// covers rows written before it was populated, where a 0 total
			// beside real input/output counts would understate the cost
			// rather than admit it is unknown.
			if m.Usage.TotalTokens > 0 {
				tokens += m.Usage.TotalTokens
			} else {
				tokens += m.Usage.InputTokens + m.Usage.OutputTokens
			}
		}
	}
	return question, strings.Join(answers, "\n\n"), tokens
}
