// F6's right pane: the Status, Task and Sessions cards, and the fallback
// overlay LAYOUT.md requires below the 90-column breakpoint where the pane
// itself auto-hides.
//
// Two things this file deliberately does NOT build, both recorded in this
// episode's completed doc rather than stubbed here:
//
//   - The context fill bar and its numbers (Status card, row 1) are B8's
//     context.report, which has no wire path at all yet — no GetContextReport
//     request, no push event. The row renders a plainly-labelled placeholder
//     instead of fabricating a bar B8 never sent. B20 (a backend episode)
//     wires the real event; a frontend follow-up then replaces the stub with
//     it, the same S11b/S12b shape this project already uses for a gap found
//     mid-episode.
//   - The "⌂ local · 4.7GB" Ollama marker is cut, not stubbed: nothing in the
//     backend queries Ollama's /api/ps, so there is no data to place behind
//     even a placeholder, and a stub for it would be decoration.
package chat

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// sideBySide joins two blocks column-wise with a gap of `gap` cells.
//
// This duplicates app\box.go's sideBySide — the same call textutil.go's own
// doc comment already made for padCells/visibleWidth/clip: a shared `ui`
// package is bigger than this episode's Build section asks for, and it is
// now owed by four episodes (F3, F5, F7, F6), not three.
func sideBySide(left string, leftW int, right string, rightW, gap int) string {
	if right == "" {
		return left
	}
	l := strings.Split(left, "\n")
	r := strings.Split(right, "\n")

	n := len(l)
	if len(r) > n {
		n = len(r)
	}

	spacer := strings.Repeat(" ", gap)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		var lt, rt string
		if i < len(l) {
			lt = l[i]
		}
		if i < len(r) {
			rt = r[i]
		}
		out[i] = padCells(lt, leftW) + spacer + padCells(rt, rightW)
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// Status card
// ---------------------------------------------------------------------------

func (w *Workspace) statusCardLines(width, rows int) []string {
	th := w.theme
	dim := th.Style(theme.TextDim)

	if w.backendDown {
		return clip([]string{dim.Render("—")}, rows)
	}

	var lines []string

	// 1. Context fill + numbers — always attempted first, per
	// F06-right-pane.md's priority order, so it is the LAST thing dropped
	// as the card shrinks. See this file's header: there is nothing real
	// to show yet.
	lines = append(lines, dim.Render("context: not wired yet (B20)"))

	// 2. Tokens / cost — real, from the latest UsageUpdated or reload.
	// Cost is never computed anywhere in the backend yet, so it is omitted
	// rather than shown as zero.
	switch {
	case w.usage.TotalTokens > 0:
		lines = append(lines, th.Style(theme.TextPrimary).Render(
			fmt.Sprintf("%s in · %s out", formatTokens(w.usage.InputTokens), formatTokens(w.usage.OutputTokens))))
	default:
		lines = append(lines, dim.Render("no turns yet"))
	}

	// 3. Provider · model. The Ollama "local" marker is cut — see the file
	// header.
	switch {
	case w.sessionProvider != "":
		label := w.sessionProvider
		if w.sessionModel != "" {
			label += " · " + w.sessionModel
		}
		lines = append(lines, dim.Render(label))
	default:
		lines = append(lines, dim.Render("—"))
	}

	return clip(lines, rows)
}

// formatTokens renders a token count the way the mockup's "12.4k / 32k"
// does: thousands as one decimal plus "k", anything smaller verbatim.
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// ---------------------------------------------------------------------------
// Task card
// ---------------------------------------------------------------------------

// terminalPriority is the group taskPriority puts everything finished in —
// completed, failed, cancelled, and any state this build does not
// recognise. Named because two places branch on it: sortStableTaskRows
// keys this group on FinishedAt, and capTerminalTail caps only this group.
const terminalPriority = 2

// taskPriority orders running above queued above everything terminal —
// LAYOUT.md's mockup order. What breaks ties WITHIN each group is
// sortStableTaskRows's business, and it is not the same key for both.
func taskPriority(state string) int {
	switch state {
	case app.TaskStateRunning:
		return 0
	case app.TaskStateQueued:
		return 1
	default:
		return terminalPriority
	}
}

func (w *Workspace) taskCardLines(width, rows int) []string {
	th := w.theme

	if w.backendDown {
		return clip([]string{th.Style(theme.TextDim).Render("—")}, rows)
	}
	if len(w.tasks) == 0 {
		return clip([]string{th.Style(theme.TextDim).Render("no background tasks")}, rows)
	}

	ordered := make([]app.TaskRow, len(w.tasks))
	copy(ordered, w.tasks)
	sortStableTaskRows(ordered)
	capped := capTerminalTail(ordered, recentTerminalLimit)

	// Rows dropped by the SCOPE cap count as hidden too.
	//
	// They did not, originally, on the reasoning that history is "out of
	// scope rather than hidden". That reasoning was wrong in the only way
	// that matters: firing ten tasks and seeing three, with nothing saying
	// so, is indistinguishable from a card that is broken. Whatever the
	// card's scope is, the number it is not showing you is information the
	// reader needs, and silence is the one answer that cannot be checked.
	hidden := len(ordered) - len(capped)

	lines := make([]string, 0, len(capped))
	for _, t := range capped {
		lines = append(lines, taskLine(th, t, width))
	}

	// The sort is what makes the row-budget cut safe — running and queued
	// are already at the front, so the tail gives up finished work first.
	if rows > 0 && (hidden > 0 || len(lines) > rows) {
		keep := rows - 1 // one row goes to the count itself
		if keep < 0 {
			keep = 0
		}
		if len(lines) > keep {
			hidden += len(lines) - keep
			lines = lines[:keep]
		}
		if hidden > 0 {
			lines = append(lines, th.Style(theme.TextDim).Render(
				fmt.Sprintf("+%d more", hidden)))
		}
	}
	return clip(lines, rows)
}

// recentTerminalLimit is how many FINISHED tasks the card keeps. Active
// work is never capped — that is the honest answer to "what is happening
// now", and it is the whole reason the card exists.
const recentTerminalLimit = 3

// capTerminalTail drops all but the `keep` most recent finished tasks.
//
// ListTasks is asked with scope=all, which answers with up to 50 rows —
// every recent task in the database, live or not. Rendered into a five-row
// card that made it a history browser, when F06-right-pane.md's own mockup
// is plainly an activity view: one running, one queued, one recently done.
// History belongs to F9's result cards and task tray, not here.
//
// Capping frontend-side rather than asking the backend for scope=active:
// that scope returns NO terminal rows at all (runtime.py's
// _handle_list_tasks), so the "one recently done" the mockup shows would
// need a second request. One request plus a cap is simpler and needs no new
// backend capability.
//
// Input must already be sorted by sortStableTaskRows, which puts active
// rows first and orders the terminal group by FinishedAt DESC — that
// ordering is what makes "the first `keep`" mean "the most recently
// FINISHED", rather than merely the most recently started.
func capTerminalTail(rows []app.TaskRow, keep int) []app.TaskRow {
	kept := 0
	for i, t := range rows {
		if taskPriority(t.State) < terminalPriority {
			continue // active: never capped
		}
		kept++
		if kept > keep {
			return rows[:i]
		}
	}
	return rows
}

// sortStableTaskRows groups rows by state — running, then queued, then
// everything terminal — and orders the terminal group by FinishedAt,
// newest completion first.
//
// The two groups are keyed on DIFFERENT times on purpose:
//
//   - Active rows keep the order they arrived in, which is the backend's
//     created_at DESC (runtime.py's _handle_list_tasks). "When it started"
//     is the right key for work still in flight — it is what the elapsed
//     clock counts from.
//   - Terminal rows are re-ordered by FinishedAt DESC, because the card
//     claims to show recently FINISHED work and capTerminalTail keeps the
//     first few. Keyed on created_at instead, a long @research created
//     before three quick turns — and finishing after all of them — was
//     evicted in favour of work that completed earlier. That is this app's
//     normal workload, not a corner case.
//
// A FinishedAt of 0 (a row whose finish was never recorded — a task that
// was live when the backend died) sorts LAST among terminal rows, which is
// what a DESC comparison gives for free.
//
// Stable, so rows the comparison calls equal — two active rows, or two
// terminal rows finishing in the same second — keep created_at DESC.
func sortStableTaskRows(rows []app.TaskRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		pi, pj := taskPriority(rows[i].State), taskPriority(rows[j].State)
		if pi != pj {
			return pi < pj
		}
		if pi == terminalPriority {
			return rows[i].FinishedAt > rows[j].FinishedAt
		}
		return false // active: leave the incoming created_at order alone
	})
}

func taskLine(th theme.Theme, t app.TaskRow, width int) string {
	// Agent first, Kind second: every task the agent executor creates —
	// a plain turn included — is submitted with kind="chat" hardcoded
	// (backend\agents\executor.py's create_task call); the capability
	// that actually ran ("research", "general", ...) lives in agent_id.
	// Kind is opaque plumbing (runtime\task.py's own doc calls it that),
	// never the label a user would recognise, so it is the fallback, not
	// the first choice — the reverse of what shipped originally and
	// showed "chat" for an @research task.
	label := t.Agent
	if label == "" {
		label = t.Kind
	}
	if label == "" {
		label = "task"
	}

	var icon string
	var role theme.Role
	var right string
	switch t.State {
	case app.TaskStateRunning:
		icon, role = "⟳", theme.Running
		right = elapsedSince(t.StartedAt)
	case app.TaskStateQueued:
		// "◌", not a new glyph: the same character stream_render.go's
		// task slot already uses for "◌ queued", proven safe in this
		// terminal. The '⋯' this used to render as visibly degraded to
		// three literal dots in testing — one clue among several
		// (truncated state words, a misaligned Sessions card) that
		// pointed at an unmeasured glyph, not a math bug in this file.
		icon, role = "◌", theme.Queued
		right = "queued"
	case app.TaskStateFailed, app.TaskStateCancelled:
		icon, role = "✕", theme.Error
		right = t.State
	default: // completed, or anything this build does not recognise
		icon, role = "✓", theme.Done
		right = "done"
	}

	left := th.Style(role).Render(icon+" ") + th.Style(theme.TextPrimary).Render(label)
	note := th.Style(theme.TextDim).Render(right)
	return overlayRight(padCells(left, width), note, width)
}

// elapsedSince renders a task's own runtime as m:ss — StartedAt is an epoch
// float second count, the same shape every other wire timestamp in this
// package uses.
func elapsedSince(startedAt float64) string {
	if startedAt <= 0 {
		return ""
	}
	d := time.Since(time.Unix(int64(startedAt), 0))
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

// ---------------------------------------------------------------------------
// Sessions card
// ---------------------------------------------------------------------------

// singleLineTitle collapses a title to one line and strips control
// characters before it is ever measured or truncated. Confirmed root cause
// of a "broken border" report that looked like a markdown- or CJK-width
// bug: two real database rows carried a raw embedded "\n" in their title
// ("# heading\n**bold** and `inline` and 日本語"), and sessionsCardLines
// measured and truncated that as ONE line by assumption — the string's
// display width was correct, but the terminal drew the text on two rows,
// and the second one landed outside the paneBox border that had already
// closed one row up. Neither markdown syntax nor the CJK content was the
// problem; both render and truncate correctly once the newline is gone.
// A title is treated as single-line BY CONSTRUCTION here, not by
// assumption — this is the one place every title reaches before display
// width is ever computed.
func singleLineTitle(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			// Dropped, not spaced: a stray escape or null byte has no
			// legitimate reason to reach a rendered title at all.
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (w *Workspace) sessionsCardLines(width, rows int) []string {
	th := w.theme

	if w.backendDown {
		return clip([]string{th.Style(theme.TextDim).Render("—")}, rows)
	}
	if len(w.sessions) == 0 {
		return clip([]string{th.Style(theme.TextDim).Render("no sessions yet")}, rows)
	}

	lines := make([]string, 0, len(w.sessions))
	for _, s := range w.sessions {
		title := singleLineTitle(s.Title)
		if title == "" {
			title = "untitled"
		}
		mark := "  "
		style := th.Style(theme.TextPrimary)
		if s.ID == w.sessionID {
			mark = th.Style(theme.AccentPrimary).Render("• ")
			style = th.Style(theme.AccentPrimary)
		}
		avail := width - 2
		if avail < 1 {
			avail = 1
		}
		lines = append(lines, mark+style.Render(runewidth.Truncate(title, avail, "…")))
	}
	return clip(lines, rows)
}

// ---------------------------------------------------------------------------
// Fallback overlay — LAYOUT.md's "auto-hides below 90 columns; still
// openable as an overlay"
// ---------------------------------------------------------------------------

// RightPaneOverlay implements app.OverlayProvider. It is a bespoke Overlay
// rather than a Popup: the cards it shows are ambient status, not a list to
// choose from, and neither Sessions ("No search here — that is the
// palette's job") nor Status is item-shaped. Sections are dim header rows
// over their own lines, the same convention Popup uses between its own
// sections, rather than each card getting a second nested border — one
// outer panel is already drawn around whatever this returns (root.go's
// withOverlay).
func (w *Workspace) RightPaneOverlay() app.Overlay {
	return &rightPaneOverlay{w: w}
}

type rightPaneOverlay struct {
	w *Workspace

	// lastLogged is a diagnostic signature of what the Task section last
	// drew. Root already logs both ends of the data path ("requesting task
	// snapshot", "task snapshot applied"); this is the third point, the
	// one neither of those can see — what THIS surface actually had in
	// hand at render time. Logged only when it CHANGES, because View runs
	// on every frame and an unconditional line would bury the log.
	lastLogged string
}

func (o *rightPaneOverlay) MinSize() (int, int)       { return 36, 20 }
func (o *rightPaneOverlay) Title() string             { return "Right Pane" }
func (o *rightPaneOverlay) IsRightPaneFallback() bool { return true }

func (o *rightPaneOverlay) Update(msg tea.Msg) (app.Overlay, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		return nil, nil
	}
	return o, nil
}

// View's `w` is NOT the text budget — F07-popup-framework.md's own gotcha
// #3 for any Popup/Confirm consumer, and this overlay missed it too:
// root.go's withOverlay wraps whatever View returns in box(), and box's
// fitCells pads one more cell on each side on top of the width View was
// told. Passing `w` straight through to the card renderers sized every
// line to fill the full budget this overlay was GIVEN, and fitCells then
// silently chopped the last 2 cells off every one of them — a state word
// truncated ("cancelled" -> "canc") without the renderer's own ellipsis
// logic ever running, because the cut happens one layer up, in box.go's
// blind truncateCells. contentW is the fix: the same `w-2` every other
// overlay budgets against.
func (o *rightPaneOverlay) View(th theme.Theme, w, h int) string {
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}

	label := th.Style(theme.AccentPrimary)
	section := func(title string, lines []string) []string {
		out := make([]string, 0, len(lines)+1)
		out = append(out, label.Render(title))
		out = append(out, lines...)
		return out
	}

	rows := h / 3
	if rows < 3 {
		rows = 3
	}

	taskLines := o.w.taskCardLines(contentW, rows-1)
	o.logRendered(len(o.w.tasks), taskLines)

	var out []string
	out = append(out, section("Status", o.w.statusCardLines(contentW, rows-1))...)
	out = append(out, "")
	out = append(out, section("Task", taskLines)...)
	out = append(out, "")
	out = append(out, section("Sessions", o.w.sessionsCardLines(contentW, rows-1))...)

	return strings.Join(clip(out, h), "\n")
}

// logRendered records what this surface actually drew, the moment it
// changes. Correlated against Root's "task snapshot applied count=N", it
// splits the one question left: if applied says N>0 while this keeps
// saying tasks=0, the snapshot is not reaching the workspace; if both say
// N>0 and the screen still shows nothing, the data is here and the
// problem is the draw.
//
// The nil check is for a Workspace built as a bare struct literal in a
// test rather than through New (which always installs a logger) — a
// diagnostic must not be the thing that panics.
func (o *rightPaneOverlay) logRendered(taskCount int, lines []string) {
	if o.w.log == nil {
		return
	}
	sig := fmt.Sprintf("%d|%s", taskCount, strings.Join(lines, "\x00"))
	if sig == o.lastLogged {
		return
	}
	o.lastLogged = sig
	o.w.log.Debug("overlay: task section changed", "tasks", taskCount, "rows", len(lines))
}

var _ app.OverlayProvider = (*Workspace)(nil)
