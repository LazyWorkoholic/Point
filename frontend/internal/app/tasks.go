package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// TaskRow mirrors one entry of ListTasks' payload (backend\runtime\runtime.py
// _handle_list_tasks), trimmed to what the right pane's Task card needs.
//
// Root fetches this, not any one workspace: F06-right-pane.md's Task card is
// explicit that the list is "Global list: tasks from every workspace,
// because the root owns it" — the same reason conn.inFlight already lives
// here rather than in chat.Workspace.
type TaskRow struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	Session    string  `json:"session"`
	Agent      string  `json:"agent"`
	CreatedAt  float64 `json:"created_at"`
	StartedAt  float64 `json:"started_at"`
	FinishedAt float64 `json:"finished_at"`
	ErrorCode  string  `json:"error_code"`
}

// Task state values, transcribed from backend\runtime\task.py's TaskState.
// Held as plain strings for the same reason protocol/types.go keeps event
// names as strings: a state this build does not recognise must still render
// as *something*.
const (
	TaskStateQueued    = "queued"
	TaskStateRunning   = "running"
	TaskStateCompleted = "completed"
	TaskStateFailed    = "failed"
	TaskStateCancelled = "cancelled"
)

// ---------------------------------------------------------------------------
// Task-row vocabulary — glyphs, grouping, labels, durations
// ---------------------------------------------------------------------------
//
// These live here, beside TaskRow and the TaskState constants, because both
// consumers of a task row have to agree on them: F6's Task card in the right
// pane (chat\cards.go) and F9's task tray (tray.go). They are NOT new rules —
// they are F6's rules, moved here from cards.go, which calls them now.
//
// **There is exactly one copy of each of these facts, and this is it.** F9a
// first added them here while leaving cards.go's originals in place, on the
// grounds that editing F6's shipped rendering was out of scope; that was
// reversed in the same session, because this codebase has already paid for
// two independently-maintained copies of one fact three separate times (F5's
// Seq, F6's taskCardWasVisible, F8's Bug A), and the specific risk here was
// concrete rather than theoretical: cards.go's own comment records a round of
// debugging spent on a glyph that degraded in Windows Terminal ('⋯' rendering
// as three literal dots, replaced by '◌'). A fix like that landing in one
// copy and not the other is the exact failure mode. Byte-identical rendering
// before and after was verified against a golden dump of taskCardLines at
// three widths and three heights.
//
// What did NOT collapse, and must not: the ORDER POLICY and the NOTE TEXT.
// Both consumers share the grouping (TaskPriority) and now compute it from
// one place, but they legitimately differ in what they do with it —
// cards.go's sortStableTaskRows leaves active rows in the backend's arrival
// order while SortTaskRows below sorts them, and the card's 26-column note
// shows a bare state word where the tray has room for an error code and a
// duration. Those are two policies over one shared fact, which is the
// correct shape; collapsing them further would change what one of the two
// renders. See sortStableTaskRows' and trayNote's own docs.

// TerminalPriority is the group TaskPriority puts everything finished in —
// completed, failed, cancelled, and any state this build does not recognise.
const TerminalPriority = 2

// TaskPriority orders running above queued above everything terminal, which
// is both LAYOUT.md's mockup order for the Task card and F09's section order
// for the tray (Running · Queued · Finished).
func TaskPriority(state string) int {
	switch state {
	case TaskStateRunning:
		return 0
	case TaskStateQueued:
		return 1
	default:
		return TerminalPriority
	}
}

// TaskFinished is TaskPriority's terminal group as a predicate. Written in
// terms of TaskPriority rather than as its own switch so a state added to one
// cannot be forgotten in the other.
func TaskFinished(state string) bool { return TaskPriority(state) == TerminalPriority }

// TaskLabel is what a row is called: the agent that ran it, falling back to
// the task kind and then to "task".
//
// Agent first, not kind: kind is the runtime's own word for the shape of the
// work ("chat"), so a card keyed on it showed "chat" for an @research task —
// a real F6 finding.
//
// This is NOT the user's question. The tray's mockup shows a topic
// ("Research · tokio vs asyncio") and ListTasks does not carry one — there is
// no title, summary or prompt field on a task row, and `tasks` has no result
// column. The topic is recoverable only from the session's own messages, for
// one session at a time, which is why the result card shows it (resultcard.go
// fetches those messages) and the tray does not. Showing it for whichever
// session happens to be loaded and not for the rest would make the list look
// broken rather than partial.
func TaskLabel(t TaskRow) string {
	if t.Agent != "" {
		return t.Agent
	}
	if t.Kind != "" {
		return t.Kind
	}
	return "task"
}

// SortTaskRows orders rows the way both the Task card and the tray want
// them: running, then queued, then finished — and within the finished group,
// most recently finished first, since that is the one a user has come to look
// at. Stable, so rows the backend already ordered keep that order inside a
// group.
func SortTaskRows(rows []TaskRow) []TaskRow {
	out := make([]TaskRow, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := TaskPriority(out[i].State), TaskPriority(out[j].State)
		if pi != pj {
			return pi < pj
		}
		if pi == TerminalPriority {
			return out[i].FinishedAt > out[j].FinishedAt
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// ---------------------------------------------------------------------------
// Which agents are background agents
// ---------------------------------------------------------------------------

// builtinAgentBackground is the registered built-in agents and whether each
// one's work belongs in the background.
//
// **This is a hardcoded backend fact, and a deliberate one.** The backend's
// real rule is `agent.max_steps > 1` (runtime.py:822's `is_bounded_loop`),
// which it rides out to clients as `TaskStarted.background`. `max_steps` is
// not on the wire, and `ListAgents` (B23) returns only {id, name,
// description} — so the one built-in that satisfies the rule (research, at
// max_steps=8; general and filler-explainer are both max_steps=1) is written
// down here instead. The correct long-term rule is "background iff the agent
// is a background agent", read from a real registry.
//
// It lives HERE rather than in `chat` because two surfaces now need it and
// `chat` imports `app`: chat\input_render.go's lane table (which lane a
// REPLY renders in) and tray.go's filter (whether a TASK belongs in the
// tray). Those are two questions over one fact, and chat\input_render.go's
// own comment had already flagged its copy as standing in for a missing
// backend capability. One copy, so the day `max_steps` or a `background`
// flag reaches the wire there is one place to delete.
var builtinAgentBackground = map[string]bool{
	"general":          false,
	"filler-explainer": false,
	"research":         true,
}

// IsKnownAgent reports whether this id is a registered built-in.
//
// False for a user-defined agent, which is correct for every current caller
// (chat's @token chip, which must not claim an agent the backend would not
// route) and is why IsBackgroundAgent is not simply this plus a lookup.
func IsKnownAgent(id string) bool {
	_, ok := builtinAgentBackground[id]
	return ok
}

// IsBackgroundAgent reports whether this agent's work belongs in the
// background — a bounded multi-step loop, minutes long, with a result worth
// opening later.
//
// An UNKNOWN agent answers false, which is the deliberate default for the
// same reason chat's lane table defaults to laneTurn: a background task
// wrongly treated as inline loses a tray row, while an inline turn wrongly
// treated as background puts a row in the tray for a reply that is already
// sitting in the transcript. The tray corrects the first case the only way
// it honestly can — `TaskStarted.background` is authoritative when this
// client actually saw it, and Root uses it to PROMOTE a task into the tray,
// never to demote one. See Root.noteTaskBackground.
func IsBackgroundAgent(id string) bool { return builtinAgentBackground[id] }

// KnownAgentIDs is every registered built-in id, sorted.
//
// Sorted rather than handed out as the map: chat's @token prefix test
// iterates these, and map order in Go is randomised per run — so an
// unsorted answer would make "is this a viable prefix of some agent" depend
// on iteration luck the moment two agents shared a prefix. None do today,
// which is exactly the kind of thing that stays true until it does not.
func KnownAgentIDs() []string {
	ids := make([]string, 0, len(builtinAgentBackground))
	for id := range builtinAgentBackground {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// TaskGlyph is the one-character state marker.
//
// '◌' for queued, not F09's mockup '⋯': the mockup's character degraded to
// three literal dots in Windows Terminal during F6's verification — one clue
// among several that pointed at an unmeasured glyph rather than a math bug —
// and '◌' is the one already proven in this terminal, the same character
// chat\stream_render.go's task slot uses for "◌ queued".
//
// Plain text, never pre-styled: the caller owns row styling (Popup fills a
// selected row; the Task card composes its own line), and a glyph arriving
// with escape sequences already in it would fight both. TaskRole is the
// matching colour, handed over separately for exactly that reason.
func TaskGlyph(state string) string {
	switch state {
	case TaskStateRunning:
		return "⟳"
	case TaskStateQueued:
		return "◌"
	case TaskStateFailed, TaskStateCancelled:
		return "✕"
	default: // completed, or anything this build does not recognise
		return "✓"
	}
}

// TaskRole is TaskGlyph's colour. Kept as a separate function rather than
// returned as a pair because two of the three call sites want only the glyph.
func TaskRole(state string) theme.Role {
	switch state {
	case TaskStateRunning:
		return theme.Running
	case TaskStateQueued:
		return theme.Queued
	case TaskStateFailed, TaskStateCancelled:
		return theme.Error
	default:
		return theme.Done
	}
}

// TaskElapsed renders a running task's runtime as m:ss, and "" when it has
// not started. StartedAt is an epoch float second count, the shape every
// wire timestamp in this codebase uses.
func TaskElapsed(startedAt float64) string {
	if startedAt <= 0 {
		return ""
	}
	return formatMMSS(time.Since(time.Unix(int64(startedAt), 0)))
}

// TaskDuration renders a finished task's wall time as m:ss, and "" when
// either endpoint is missing. Both endpoints come off ListTasks, so this is
// the one honest cost label the tray can show without inventing a number —
// see TaskLabel on why the token count in F09's mockup is not one of them.
func TaskDuration(t TaskRow) string {
	if t.StartedAt <= 0 || t.FinishedAt <= 0 || t.FinishedAt < t.StartedAt {
		return ""
	}
	return formatMMSS(time.Duration((t.FinishedAt - t.StartedAt) * float64(time.Second)))
}

func formatMMSS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
