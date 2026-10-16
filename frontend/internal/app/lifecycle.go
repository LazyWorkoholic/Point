// F12: workspace lifecycle. The differentiator — views load on demand,
// unload when idle, and NEVER take a running task down with them.
//
// # The state machine is the map
//
//	NOT_LOADED    id absent from r.workspaces
//	LOAD          def.New() + Init()          on first switch to it
//	ACTIVE        the one r.active names AND that is in the map
//	INACTIVE      in the map, not active, r.lastActive[id] running
//	UNLOAD        Suspend() -> store bytes -> delete key -> GC frees the rest
//
// There is no enum, and that is deliberate: an enum is a second
// representation of a fact the map already holds, and the two would
// eventually disagree. "Is it loaded" is a map lookup, everywhere, with no
// cached answer anybody can forget to update.
//
// # Why one timer per workspace (D87)
//
// Each loaded workspace has its OWN lastActive, and one tick walks whatever
// is currently in the map, so the count is dynamic — four workspaces or
// twelve, with no constant anywhere. A single global timer would unload
// three workspaces at the same moment for no reason; leaving a workspace at
// 3:00 and another at 3:30 has to unload them thirty seconds apart, because
// that is what the user actually did.
//
// The ACTIVE workspace never unloads. Its timer only starts when you leave
// it, which is why lastActive is written on the way OUT of a workspace and
// deleted on the way in.
//
// # Unload never cancels a task
//
// The task is in another process. The frontend unloading a view has no
// bearing on it, and F9a's tray is the proof surface: unload Chat with
// @research running, press F1, the task is still listed and its result
// still opens. Nothing in this file touches a task, sends a cancel, or
// waits for one — and `workspace.unload(id)` (F12's one backend-side
// bullet) is NOT sent here either, because B15 does not exist yet. See this
// episode's Handoff.
//
// Quit is the asymmetry: quitting kills the backend, so tasks genuinely die
// there. That is why unload shows a reassurance and quit shows a warning
// (actions.go, UI-13 and UI-14).
package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// idleInterval is one step of the idle walk.
//
// One second, not one minute: the grace warning fires at a 30-second
// boundary and the switcher's readout counts down live, so a minute-grained
// tick would overshoot the warning entirely at a one-minute timeout — the
// value F12's own verification gate sets. It costs nothing at rest because
// it only ever runs while at least one loaded workspace is both inactive and
// unloadable, which with a single workspace loaded is never.
const idleInterval = time.Second

// graceSeconds formats the grace warning's countdown, and ONLY that.
//
//	Chat unloads in 29s — ctrl+w w to keep it
//
// # Why this is not shortDuration
//
// shortDuration is the paired formatter: it renders both halves of the
// switcher's `idle X of Y`, so its output has to have ONE shape at every
// value. It had an under-a-minute special case that gave it two, which
// produced `idle 58s of 1m00s` — one line in two formats — and removing
// that branch is what made the readout correct. It stays unconditional.
//
// This sentence has no pair. It is a countdown to an imminent event,
// bounded by idleGrace below, and `in 29s` is how a countdown that short
// reads. Routing it through the paired formatter spelled it `in 0m29s`,
// which was a side effect of fixing the readout rather than anything
// anybody wanted — and it contradicted gate_f13a.ps1's own
// already-verified Step 10b text.
//
// So: its own formatter at its own call site, rather than a conditional
// put back inside shortDuration. The conditional is what caused the bug;
// a second function for a second purpose is not the same thing as a
// special case inside one.
//
// # Seconds only, by construction
//
// `remaining` is at most idleGrace, which is well under a minute, so
// there are never minutes to show. That is an assumption rather than a
// guarantee, so TestGraceSecondsAssumesASubMinuteWindow asserts it: if
// idleGrace is ever raised past a minute, that test fails and names this
// function rather than letting it quietly print `in 90s`.
func graceSeconds(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

// idleGrace is how long before an unload the warning appears. F12: "a grace
// warning at ~30s — a toast: `Editor unloads in 30s — any key to keep`".
const idleGrace = 30 * time.Second

// suspendDebounce is how long after a change the active workspace's blob is
// re-persisted.
//
// F12 asks for suspended state in state.json "(debounced), so it survives
// not just unload but a kill" — a kill never calls Suspend, so something has
// to have written the blob in advance. Two seconds rather than F4's 500ms
// for the draft: a scroll position is cheaper to lose than typed text (it
// degrades to the bottom by F18's own rule, where a lost draft is lost
// work), and scrolling generates far more events than typing does.
const suspendDebounce = 2 * time.Second

type idleTickMsg struct{ gen int }
type suspendSaveMsg struct{ gen int }

// idleState drives the walk. running/gen follow animState's shape exactly,
// for the same reason: a tick already in flight when the last unloadable
// workspace went away must be a harmless no-op rather than a second,
// parallel loop.
type idleState struct {
	running bool
	gen     int

	// warned records which workspaces have already had their grace
	// warning shown for the CURRENT idle period, so the warning appears
	// once rather than once per second. Cleared for a workspace whenever
	// its timer resets, which is what makes "any key to keep" able to
	// warn again later.
	warned map[WorkspaceID]bool

	// dirtyNoticed records which workspaces have already been reported
	// as pinned by unsaved work (F13), for the CURRENT idle period.
	//
	// A separate map from warned, because the two are cleared by
	// different things and were once cleared by wildly different
	// frequencies -- see the history in walkIdle's dirty branch. They are
	// now reset together by resetIdle, which is what makes "the current
	// idle period" the actual scope rather than the intended one: this
	// used to be cleared only when a workspace stopped being dirty, so a
	// workspace that stayed dirty reported itself once and then never
	// again, however many times it was reactivated and re-idled.
	dirtyNoticed map[WorkspaceID]bool
}

// ---------------------------------------------------------------------------
// Load and switch
// ---------------------------------------------------------------------------

// switchWorkspace makes a workspace active, loading it if it is not.
//
// Switching AWAY leaves the old one INACTIVE — loaded, not visible, its own
// idle timer now running. All loaded workspaces coexist; only the active one
// renders (render.go's View calls current().View, singular).
func (r *Root) switchWorkspace(id WorkspaceID) tea.Cmd {
	def, ok := r.defByID[id]
	if !ok {
		return status(fmt.Sprintf("no workspace registered as %q", id))
	}
	if id == r.active {
		if _, loaded := r.workspaces[id]; loaded {
			r.touchActive()
			return nil
		}
	}

	// The outgoing workspace's timer starts NOW, not at the moment the
	// next tick happens to notice.
	if r.active != "" && r.active != id {
		if _, loaded := r.workspaces[r.active]; loaded {
			r.lastActive[r.active] = r.now()
		}
	}

	var cmds []tea.Cmd
	if _, loaded := r.workspaces[id]; !loaded {
		if cmd, err := r.loadWorkspace(def); err != nil {
			return status(err.Error())
		} else if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	r.active = id
	r.vacant = nil // the vacant screen was for a different id
	r.resetIdle(id)
	r.recompute()
	cmds = append(cmds, r.pushDiagnostics(), r.persistWorkspaceState())

	// A workspace loaded on demand has missed every message that already
	// happened, and two of them it cannot do without.
	//
	// This is the failure mode lazy loading introduces, and every part of
	// it was found by running the app rather than by reading it: before
	// F12 a workspace existed before the first WindowSizeMsg, before the
	// handshake, and before any provider could be chosen, so none of these
	// could be missed.
	//
	// The shape generalises, and anything added later that Root announces
	// to the active workspace by MESSAGE rather than by asking belongs
	// here too. Root does not currently keep a copy of F10's UI override
	// (it applies the theme to r.theme directly and hands it over via
	// SetTheme above), so an Appearance change made while nothing is
	// loaded still fails to reach state.json — same class, no state to
	// replay from, recorded in this episode's Handoff rather than guessed
	// at here.
	//
	//   - ProviderDefaultSetMsg: the provider chosen before this workspace
	//     existed. See the block below for the whole trace.
	//   - WindowSizeMsg: without it the workspace renders at its zero size
	//     until the terminal happens to be resized.
	//   - ConnectedMsg: chat.Workspace does not bootstrap in Init() at all
	//     -- its own doc says Update watches for ConnectedMsg and starts
	//     the ListSessions/GetSession/CreateSession sequence from there,
	//     because a query fails while the client is not connected. The
	//     backend handshake almost always completes while the chooser is
	//     still on screen, so the real ConnectedMsg arrived before this
	//     workspace existed and no second one is ever coming. Replayed
	//     from the connection state Root already holds, which is the same
	//     state the diagnostics screen renders, so there is no second copy
	//     of "are we connected" to keep in step.
	// The provider/model the user has ALREADY chosen, before this
	// workspace existed to hear about it.
	//
	// Settings and the first-run chooser announce a choice by emitting
	// ProviderDefaultSetMsg, which Root routes to the ACTIVE WORKSPACE.
	// On a fresh profile the first-run chooser opens before anything is
	// loaded (it has to — it is a prerequisite, not a peer), so that
	// message reached the vacant diagnostics screen, which ignores it, and
	// was gone. chat.Workspace was then constructed from the State main.go
	// captured at STARTUP, where Provider is still nil, so onSessionList
	// sent CreateSession with an empty payload and the session was created
	// with no provider at all: the Status card read "— · —" until the user
	// opened Settings again from inside Chat and re-picked the same
	// provider, which is what the bug report described.
	//
	// It also meant the choice never reached state.json, since
	// chat.applyProviderDefault is the only writer of that field — so a
	// restart re-prompted for a provider that had already been chosen.
	//
	// BEFORE the handshake replay below, deliberately. applyProviderDefault
	// with no session yet is a documented no-op for the SetSessionProvider
	// retrofit and just records the default; the handshake is what starts
	// the ListSessions/CreateSession chain that reads it. The other order
	// creates the session first and then retrofits it, which works but
	// makes a wire round trip out of something that is free here.
	if r.activeProvider != nil {
		ws, cmd := r.current().Update(ProviderDefaultSetMsg{Default: *r.activeProvider})
		r.storeCurrent(ws)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if r.conn.handshaked {
		ws, cmd := r.current().Update(protocol.ConnectedMsg{
			ProtocolVersion: r.conn.protocolVersion,
			Capabilities:    r.conn.capabilities,
			// Not a restart from THIS workspace's point of view: it has
			// never been connected before. Saying otherwise would make a
			// freshly chosen workspace report "backend restarted".
			Restarted: false,
		})
		r.storeCurrent(ws)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if r.w > 0 && r.h > 0 {
		ws, cmd := r.current().Update(tea.WindowSizeMsg{Width: r.w, Height: r.h})
		r.storeCurrent(ws)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// loadWorkspace constructs a workspace and hands it back whatever it had
// suspended.
//
// Resume BEFORE Init, and the order matters: Init is what issues the query
// that rehydrates content, and F18's Resume stores the anchor for the load
// to consume rather than positioning anything itself (there is no session,
// no window and no transcript to address at Resume time). Resuming after
// Init would hand the anchor over after the thing that reads it had already
// run.
func (r *Root) loadWorkspace(def WorkspaceDef) (tea.Cmd, error) {
	// Refused with the ACTUAL numbers rather than rendered wrong, the same
	// rule OpenOverlay's own fit check follows. Skipped before the first
	// WindowSizeMsg, where r.w/r.h are still 0 and the question cannot be
	// answered honestly.
	if min := def.MinSize; r.w > 0 && r.h > 0 && (r.w < min.W || r.h < min.H) {
		return nil, fmt.Errorf("%s needs %d×%d — terminal is %d×%d",
			def.Name, min.W, min.H, r.w, r.h)
	}

	ws := def.New()
	if ws == nil {
		return nil, fmt.Errorf("%s failed to construct", def.Name)
	}
	if ta, ok := ws.(ThemeAware); ok {
		ta.SetTheme(r.theme)
	}
	if blob, ok := r.suspended[def.ID]; ok {
		// A Resume that fails is logged and ignored, never fatal: F18's
		// own contract is that the restore path can never be the thing
		// that breaks a resume, and the five unusable-blob cases all
		// degrade to the bottom inside chat's own Resume. This handles the
		// sixth possibility — a workspace whose Resume returns a real
		// error anyway — the same way.
		if err := ws.Resume(blob); err != nil {
			r.log.Warn("workspace resumed without its stored position",
				"workspace", def.ID, "err", err)
		}
	}
	r.workspaces[def.ID] = ws
	r.log.Info("workspace loaded", "workspace", def.ID,
		"had_suspended_state", len(r.suspended[def.ID]) > 0)
	return ws.Init(), nil
}

// resetIdle starts a workspace's idle accounting over: the clock, and
// BOTH say-once latches that hang off it.
//
// There is one function because there were three sites and two latches,
// and the second latch was added to only one of them. idle.warned was
// cleared by switchWorkspace, touchActive and unloadWorkspace;
// idle.dirtyNoticed was cleared by exactly one line in walkIdle, on the
// branch that runs when a workspace is NOT dirty. So for a workspace that
// stayed dirty the latch was permanent for the whole of its loaded life:
// the notice fired once, ever, and reactivating the workspace and
// re-idling it never re-armed it.
//
// Reported as "background a dirty Editor, see the notice, switch back,
// background it again -- the notice does not fire the second time". The
// latch was scoped per-dirty-run when it should be scoped per-idle-period,
// and the trigger for a new idle period is exactly the trigger for
// resetting the clock. Hence one function rather than a third `delete`
// added to three places, which is how the two drifted apart in the first
// place.
func (r *Root) resetIdle(id WorkspaceID) {
	delete(r.lastActive, id)
	delete(r.idle.warned, id)
	delete(r.idle.dirtyNoticed, id)
}

// touchActive resets the active workspace's idle accounting. Called from
// every key, because "the active workspace never unloads" has to be true of
// a workspace you are typing in even if some other path forgot to say so.
func (r *Root) touchActive() {
	r.resetIdle(r.active)
}

// ---------------------------------------------------------------------------
// Unload
// ---------------------------------------------------------------------------

// unloadWorkspace suspends a workspace and drops it.
//
// Reports whether anything was unloaded, so a caller can tell "done" from
// "there was nothing there" without a second map lookup of its own.
func (r *Root) unloadWorkspace(id WorkspaceID) (tea.Cmd, bool) {
	ws, loaded := r.workspaces[id]
	if !loaded {
		return nil, false
	}

	// The draft first, synchronously, for app.quit's own Bug 17 reason:
	// chat's draft save is debounced, and an unload that only waited for
	// the next tick would drop whatever was typed since the last one. The
	// workspace is about to stop receiving messages entirely, so this is
	// the last chance that tick has.
	if f, ok := ws.(DraftFlusher); ok {
		f.FlushDraft()
	}
	// F13b: and the backend-side equivalent. A file lock outlives the
	// workspace that took it -- the only thing that would eventually free
	// it is B7's 60-second staleness timeout, so an unload without this
	// makes Point wait out its OWN lock on the next open. Synchronous,
	// here, for the same reason FlushDraft is: the workspace is about to
	// stop receiving messages, so there is no later tick to do it on.
	if lh, ok := ws.(LockHolder); ok {
		lh.ReleaseLocks()
	}

	blob, err := ws.Suspend()
	if err != nil {
		// Unload proceeds. A workspace that cannot describe its own
		// position still has to be unloadable — refusing would make a
		// broken Suspend into a memory leak, and the position degrades to
		// the bottom on the next load, which is exactly what F18 says a
		// missing anchor means.
		r.log.Warn("workspace suspended without storing its position",
			"workspace", id, "err", err)
		blob = nil
	}
	if len(blob) > 0 {
		r.suspended[id] = blob
	} else {
		delete(r.suspended, id)
	}

	delete(r.workspaces, id)
	r.resetIdle(id)
	r.log.Info("workspace unloaded", "workspace", id, "blob_bytes", len(blob))

	var cmds []tea.Cmd
	if id == r.active {
		// Hand the screen to another LOADED workspace if there is one;
		// otherwise there is nothing loaded at all and the chooser is the
		// honest thing to show. r.active keeps naming the unloaded
		// workspace in that case — it is what the frame is FOR and what
		// the chooser pre-selects, and current() returns the vacant
		// screen because the key is gone.
		r.vacant = nil
		if next, ok := r.anyLoaded(); ok {
			cmds = append(cmds, r.switchWorkspace(next))
		} else {
			r.chooserShown = false
		}
	}
	r.recompute()
	return tea.Batch(append(cmds, r.pushDiagnostics(), r.persistWorkspaceState())...), true
}

// anyLoaded picks a loaded workspace other than r.active, in registration
// order rather than map order — map iteration is randomised, and "which
// workspace you land on after an unload" has to be the same every time or
// it reads as a bug.
func (r *Root) anyLoaded() (WorkspaceID, bool) {
	for _, def := range r.defs {
		if def.ID == r.active {
			continue
		}
		if _, ok := r.workspaces[def.ID]; ok {
			return def.ID, true
		}
	}
	return "", false
}

// unloadNotice is F12's demo beat, verbatim:
//
//	unloading view — Research task continues in background
//
// "Do not replace it with a block." The whole property this episode exists
// to demonstrate is that a running task and a loaded view are independent,
// and refusing to unload would be the app asserting the opposite.
//
// The name comes from the global task snapshot (TaskLabel — the agent, so an
// @research task reads "Research" rather than "chat"), which refreshTasks
// now keeps current whether or not the pane or the tray is on screen; see
// its own doc for why that gate had to go.
func (r *Root) unloadNotice(name string) string {
	running := r.runningTasks()
	if len(running) == 0 {
		// conn.inFlight is the backend's own live count and is never
		// stale; the task SNAPSHOT is only kept current while a surface
		// that shows it is on screen (refreshTasks' F6 gate, deliberately
		// left in place). So with the right pane auto-hidden below 90
		// columns and the tray never opened, something can be running
		// that cannot be NAMED. Unload is immediate by design -- delaying
		// it on a round trip would undercut the very property this notice
		// reports -- so the wording degrades to the unnamed form rather
		// than either guessing a name or claiming nothing is running.
		if len(r.conn.inFlight) > 0 {
			return "unloading view — background work continues"
		}
		return "unloaded " + name
	}
	label := capitalise(TaskLabel(running[0]))
	if len(running) > 1 {
		return fmt.Sprintf("unloading view — %s and %d more continue in background",
			label, len(running)-1)
	}
	return fmt.Sprintf("unloading view — %s task continues in background", label)
}

// runningTasks is the global snapshot filtered to what is actually in
// flight, newest first — the data behind UI-13's "background tasks will keep
// running", UI-14's list of what quitting cancels, and unloadNotice above.
func (r *Root) runningTasks() []TaskRow {
	out := make([]TaskRow, 0, len(r.tasks))
	for _, t := range r.tasks {
		if t.State == TaskStateRunning || t.State == TaskStateQueued {
			out = append(out, t)
		}
	}
	return SortTaskRows(out)
}

// capitalise upper-cases the first rune only.
//
// strings.Title is deprecated and word-wise: it would also capitalise the
// second word of a two-word agent name, which is not what an agent is
// called. TaskLabel returns an agent id ("research"), and the notice reads
// "Research task continues in background" — the one capital is the sentence
// position, not a title case rule.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

// ---------------------------------------------------------------------------
// Idle auto-unload
// ---------------------------------------------------------------------------

// idleTimeout is this workspace's own `idle_unload_minutes`, per-workspace
// override first. Zero disables unloading for it.
func (r *Root) idleTimeout(id WorkspaceID) time.Duration {
	return r.cfg.IdleUnload(string(id))
}

// unloadableLoaded is how many loaded workspaces are both inactive and
// configured to unload — the tick's own reason to exist, and therefore the
// one predicate that decides whether it runs at all.
func (r *Root) unloadableLoaded() int {
	n := 0
	for id := range r.workspaces {
		if id == r.active || r.idleTimeout(id) == 0 {
			continue
		}
		n++
	}
	return n
}

// ensureIdleWalk starts the tick if anything can unload and it is not
// already running. It never STOPS one — a tick that finds nothing simply
// does not re-arm, which is the only way to end a tea.Tick already
// scheduled. Same shape as ensureAnimation, same reason.
func (r *Root) ensureIdleWalk() tea.Cmd {
	if r.idle.running || r.unloadableLoaded() == 0 {
		return nil
	}
	r.idle.running = true
	r.idle.gen++
	return idleTick(r.idle.gen)
}

func idleTick(gen int) tea.Cmd {
	return tea.Tick(idleInterval, func(time.Time) tea.Msg {
		return idleTickMsg{gen: gen}
	})
}

// walkIdle is one step: unload whatever is over its own timeout, warn
// whatever is within the grace window of it.
//
// It walks r.workspaces, so the number of timers is whatever is loaded right
// now. Unloads are collected and applied after the walk rather than during
// it, because unloadWorkspace deletes from the map being iterated.
func (r *Root) walkIdle(now time.Time) tea.Cmd {
	var (
		due   []WorkspaceID
		cmds  []tea.Cmd
		warns []string
	)
	for _, def := range r.defs {
		id := def.ID
		if _, loaded := r.workspaces[id]; !loaded || id == r.active {
			continue
		}
		timeout := r.idleTimeout(id)
		if timeout == 0 {
			continue
		}
		since, ok := r.lastActive[id]
		if !ok {
			// Loaded but never left — only reachable if something set
			// the active workspace without going through
			// switchWorkspace. Start the clock now rather than treating
			// an absent timestamp as "idle since the zero time", which
			// would unload it on this very tick.
			r.lastActive[id] = now
			continue
		}
		// F13: an idle unload must never be the thing that discards an
		// unsaved buffer.
		//
		// requestUnload (leader → u) has asked DirtyBuffers since F12 and
		// confirms before unloading; this sweep never did, because until
		// F13 nothing in the app could BE dirty — chat's draft is flushed
		// to state.json on the way out, so a Chat unload loses nothing
		// whether or not anyone is watching. An editor buffer is
		// different: it is the only unsaved thing in Point that has no
		// other copy, and unloading it is silent by this sweep's own
		// design ("Expect: Chat unloads silently").
		//
		// So a dirty workspace is skipped rather than confirmed. A modal
		// about a view you are not looking at, fired by a timer you did
		// not start, is the worst possible place to ask a question — and
		// the manual path already asks it properly. The workspace simply
		// stays loaded, which is also what the switcher's readout then
		// says, so the state is visible rather than inferred.
		if db, ok := r.workspaces[id].(DirtyBuffers); ok && db.DirtyBufferCount() > 0 {
			// Latched on its OWN map, not on idle.warned.
			//
			// It used idle.warned as a say-this-once flag, and
			// keepWarnedAlive DELETED every entry in that map on every
			// keypress -- it was the "any key to keep" rescue. So the
			// notice re-armed on every keystroke and re-fired on the
			// next tick, which is how one message ended up repeating
			// forever while typing in another workspace. Two
			// mechanisms, one map, and the one that cleared it ran a
			// thousand times more often.
			//
			// That rescue is gone now (see below), and these are still
			// two separate latches: whatever clears `warned` next must
			// not silently re-arm this one.
			//
			// This notice is also deliberately not a countdown: there is
			// nothing to count down to while the workspace is pinned,
			// and a countdown would be a promise about an unload that
			// is not going to happen.
			if !r.idle.dirtyNoticed[id] {
				r.idle.dirtyNoticed[id] = true
				warns = append(warns, fmt.Sprintf(
					"%s stays loaded — %d file(s) have unsaved changes",
					def.Name, db.DirtyBufferCount()))
			}
			continue
		}
		delete(r.idle.dirtyNoticed, id)

		idle := now.Sub(since)
		if idle >= timeout {
			due = append(due, id)
			continue
		}
		if remaining := timeout - idle; remaining <= idleGrace && !r.idle.warned[id] {
			r.idle.warned[id] = true
			warns = append(warns, fmt.Sprintf("%s unloads in %s — %s to keep it",
				def.Name, graceSeconds(remaining), r.keepHint()))
		}
	}

	for _, id := range due {
		name := r.defByID[id].Name
		if cmd, did := r.unloadWorkspace(id); did {
			cmds = append(cmds, cmd)
			r.log.Info("workspace idle-unloaded", "workspace", id)
			// Silently, per F12's own verification step ("Expect: Chat
			// unloads silently") — the readout and the grace warning are
			// the affordances, and a notice about a view you are not
			// looking at, after you were already warned, is noise. The
			// running-task reassurance is the exception: it is the one
			// thing the user cannot see for themselves.
			if len(r.runningTasks()) > 0 {
				cmds = append(cmds, r.expiringStatus(r.unloadNotice(name), noticeLinger))
			}
		}
	}
	// One warning at a time in a single-line status bar; the rest warn on
	// their own next tick, by which point this one has expired.
	if len(warns) > 0 {
		cmds = append(cmds, r.expiringStatus(warns[0], idleInterval*4))
	}

	// Re-arm only if there is still something to watch. This is where the
	// tick actually ends: the last unload makes unloadableLoaded zero and
	// nothing re-schedules.
	if r.unloadableLoaded() > 0 {
		cmds = append(cmds, idleTick(r.idle.gen))
	} else {
		r.idle.running = false
	}
	return tea.Batch(cmds...)
}

// keepHint is what the grace warning tells the user to do.
//
// It names the switcher's own binding rather than a literal, so the
// sentence cannot drift from the registry the way a hardcoded one would
// — the same rule bindingsHint already follows.
func (r *Root) keepHint() string {
	if a, ok := r.actions.ByID("workspace.switcher"); ok && a.Leader != "" {
		return actions.Leader + " " + a.Leader
	}
	return "switching to it"
}

// A keystroke somewhere else is NOT a reason to keep a workspace loaded,
// and this is where the function that thought it was used to be.
//
// # What it did
//
// keepWarnedAlive reset the idle clock of every workspace inside its grace
// window, from Root.Update's KeyMsg case — so from any key, in any
// workspace. F12 built it that way deliberately and argued the scoping was
// already narrow: only the WARNED workspaces, not every loaded one, "which
// would make the whole mechanism unreachable while the user types anywhere
// in the app".
//
// # Why that argument proves too little
//
// It makes the mechanism unreachable anyway, just later. Once a
// backgrounded workspace enters its grace window, one keystroke anywhere
// resets it to a FULL timeout — so while anybody is typing, the warned
// workspace cycles idle → warn → rescued → idle → warn → rescued and can
// never unload. Reproduced: open the Editor, visit Chat, come back, wait
// for "Chat unloads in 29s", then keep typing in the Editor. Chat's
// countdown returns to full, forever.
//
// A keypress in the Editor is not evidence of interest in Chat at 29
// seconds remaining for exactly the reason it is not at four seconds. The
// honest scope is activity in the workspace BEING EVALUATED — and a
// backgrounded workspace receives no keys at all, so there is nothing left
// for a key to rescue. That is not a gap: switching to a workspace already
// clears its clock (switchWorkspace's own delete of lastActive), which is
// the only act that actually evidences interest, and it is what the
// warning now names.
//
// touchActive stays, and is a different thing: it keeps the ACTIVE
// workspace's accounting clear, which is "the active workspace never
// unloads" being true of the one you are typing in.

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// persistWorkspaceState schedules a debounced write of state.json's
// workspace_state and last_workspace.
//
// Debounced for F4's reason, with F4's shape (a generation counter a later
// change bumps), so a burst of switches produces one write rather than one
// per switch.
func (r *Root) persistWorkspaceState() tea.Cmd {
	r.suspendGen++
	gen := r.suspendGen
	return tea.Tick(suspendDebounce, func(time.Time) tea.Msg {
		return suspendSaveMsg{gen: gen}
	})
}

// saveWorkspaceStateIfDue is suspendSaveMsg's handler.
//
// It asks the ACTIVE workspace for a fresh blob first, which is what makes
// the blob survive a kill: an unloaded workspace's bytes are already in
// r.suspended, but the one on screen has never been suspended at all, and
// after a kill Suspend is never going to be called. This is the only path
// that writes a blob for a workspace that is still loaded.
func (r *Root) saveWorkspaceStateIfDue(gen int) {
	if gen != r.suspendGen {
		return // superseded
	}
	r.suspendPending = false
	if ws, loaded := r.workspaces[r.active]; loaded {
		if blob, err := ws.Suspend(); err != nil {
			r.log.Warn("suspending the active workspace for persistence",
				"workspace", r.active, "err", err)
		} else if len(blob) > 0 {
			r.suspended[r.active] = blob
		}
	}
	if r.SaveWorkspaceStateFn == nil || len(r.defs) == 0 {
		// No registry means no lifecycle: nothing can be loaded, nothing
		// can be suspended, and last_workspace would name a workspace
		// that could never be opened. So there is nothing to write.
		//
		// This is also the structural reason `go test ./frontend/...`
		// cannot touch the real %LOCALAPPDATA%\Point\state.json, which
		// CLAUDE.md forbids outside a scratch profile. Every pre-F12
		// test in this package builds a Root with no Options.Workspaces,
		// and several of them run app.quit -- whose flush lands here. A
		// test that DOES register workspaces must substitute
		// SaveWorkspaceStateFn, the way lifecycle_test.go's
		// lifecycleRoot and chat's own chatRegistered both do; relying
		// on this guard instead would be relying on a test staying
		// unrealistic.
		return
	}
	blobs := make(map[string]json.RawMessage, len(r.suspended))
	for id, raw := range r.suspended {
		blobs[string(id)] = json.RawMessage(raw)
	}
	if err := r.SaveWorkspaceStateFn(string(r.active), blobs); err != nil {
		r.log.Warn("saving workspace state", "err", err)
	}
}

// schedulePositionSave re-arms the debounced save because something that
// can MOVE the position just happened — a key or a scroll.
//
// Without it the blob only ever reached disk on a switch, an unload or a
// quit, and F12 asks for suspended state that "survives not just unload but
// a kill". A kill never calls Suspend, so the only blob a restart can find
// is one written in advance, and scrolling to the middle of a long session
// and then being killed has to leave something behind.
//
// Coalesced on a FLAG rather than only on the generation counter: a burst of
// scrolling is hundreds of keys, and while a stale tick is already a
// harmless no-op by generation (F4's draftGen rule), scheduling one tea.Tick
// per keystroke is hundreds of goroutines for one write. One pending tick at
// a time costs one.
func (r *Root) schedulePositionSave() tea.Cmd {
	if r.suspendPending {
		return nil
	}
	if _, loaded := r.workspaces[r.active]; !loaded {
		return nil
	}
	r.suspendPending = true
	return r.persistWorkspaceState()
}

// flushWorkspaceState persists immediately, bypassing the debounce — quit's
// escape hatch from it, the same one FlushDraft is for the draft and for the
// same reason: once bubbletea has tea.Quit, a sleeping Tick's message is
// never delivered.
func (r *Root) flushWorkspaceState() {
	r.suspendGen++
	r.saveWorkspaceStateIfDue(r.suspendGen)
}

// restoreSuspended seeds r.suspended from state.json at startup.
//
// Loading nothing, deliberately: a stored blob existing does NOT mean its
// workspace is loaded (F12's own words). The bytes sit here until the user
// chooses that workspace, which is the whole of "do not auto-load".
func (r *Root) restoreSuspended(st config.State) {
	for id, raw := range st.WorkspaceState {
		if len(raw) == 0 {
			continue
		}
		r.suspended[WorkspaceID(id)] = []byte(raw)
	}
}

// ---------------------------------------------------------------------------
// The unload action — UI-13
// ---------------------------------------------------------------------------

// DirtyBuffers is implemented by a workspace that holds unsaved editable
// content. UI-13's whole reason to exist is "prevent losing an unsaved
// editor buffer by reflex", so the count has to come from the workspace
// rather than be guessed by the root.
//
// NOTHING implements this yet. The Editor is F13's, and
// design/completed/frontend/F09a-task-tray-cards.md already established with
// evidence that there is no editor buffer and no apply target anywhere in
// this repo. So leader → u never confirms today, which is exactly what
// UI-13's own empty state prescribes: "if nothing is dirty and no task runs,
// unload without asking".
//
// Declared now rather than when F13 arrives for the reason the narrow
// interfaces around it (FocusAware, OverlayProvider, DraftFlusher,
// ThemeAware) are all shaped this way: the root asks "can you tell me this",
// never reaches into a workspace's fields to work it out. A workspace that
// grows a dirty buffer later implements one method and the confirmation
// starts appearing, with no edit to this file.
type DirtyBuffers interface {
	// DirtyBufferCount is how many of this workspace's buffers have
	// unsaved changes.
	DirtyBufferCount() int
}

// activeIsLoaded is workspace.unload's When predicate. Unloading the vacant
// diagnostics screen is not a thing — there is nothing to release and
// nothing to suspend — so the action reports itself unavailable rather than
// doing nothing when run.
func (r *Root) activeIsLoaded() bool {
	_, ok := r.workspaces[r.active]
	return ok
}

// requestUnload is leader → u: UI-13 when there is unsaved work, straight
// through when there is not.
//
// A running task is deliberately NOT a reason to confirm. F12 is explicit:
// "Unload proceeds even when the workspace has a running task — the task is
// in another process", and the notice it shows instead "is that demo beat.
// Do not replace it with a block." A confirmation here would be the app
// asking permission to do the thing this episode exists to prove is safe.
func (r *Root) requestUnload() tea.Cmd {
	ws, loaded := r.workspaces[r.active]
	if !loaded {
		return nil
	}
	id, name := r.active, r.defByID[r.active].Name

	dirty := 0
	if db, ok := ws.(DirtyBuffers); ok {
		dirty = db.DirtyBufferCount()
	}
	if dirty == 0 {
		return r.unloadAndReport(id, name)
	}

	body := fmt.Sprintf("%d file(s) have unsaved changes.", dirty)
	if n := len(r.runningTasks()); n > 0 {
		body += fmt.Sprintf("\n%d background task(s) will keep running.", n)
	}
	r.OpenOverlay(NewConfirm(
		"Unload "+name+"?", body, "Unload", "Cancel",
		func() tea.Msg { return unloadConfirmedMsg{id: id, name: name} },
	))
	return nil
}

// unloadConfirmedMsg is UI-13's answer. A tea.Msg rather than a closure
// calling unloadWorkspace directly: Confirm's OnConfirm runs as a tea.Cmd,
// off the update loop, and unloadWorkspace mutates three maps and the
// active workspace. Everything that touches those has to arrive through
// Update, which is the same rule connection state already follows ("written
// only from client messages arriving in Update — never from another
// goroutine").
type unloadConfirmedMsg struct {
	id   WorkspaceID
	name string
}

// unloadAndReport unloads and says what happened, including F12's demo-beat
// reassurance when a task is still running.
func (r *Root) unloadAndReport(id WorkspaceID, name string) tea.Cmd {
	notice := r.unloadNotice(name) // composed BEFORE the unload, while r.active still names it
	cmd, did := r.unloadWorkspace(id)
	if !did {
		return nil
	}
	return tea.Batch(cmd, r.expiringStatus(notice, noticeLinger))
}

// ---------------------------------------------------------------------------
// A task snapshot for a dialog, not a pane
// ---------------------------------------------------------------------------

// quitConfirmWait bounds how long app.quit waits for a fresh task snapshot
// before showing UI-14 with whatever is already in hand.
//
// A dialog that never appears because a reply never arrived would be worse
// than a dialog listing one task fewer than it should: the user pressed
// ctrl+q and nothing happened. Half a second is long enough for a local
// SQLite query over a pipe and short enough not to read as a hang.
const quitConfirmWait = 500 * time.Millisecond

type quitConfirmTimeoutMsg struct{}

// requestTasksOffScreen asks for the global task snapshot with the
// on-screen gate deliberately bypassed, for a consumer that is a DIALOG
// rather than a pane.
//
// refreshTasks exists for the right pane and the tray and is gated on one of
// them being visible -- F6's Done-when #6, that a hidden pane costs no
// requests, which chat's own reopen test asserts directly. F12 adds a
// consumer that gate cannot serve: UI-14 has to LIST what quitting will
// cancel ("1 task is still running: ⟳ Research 0:42"), and with the pane
// hidden and the tray never opened the snapshot in hand is empty. Rendering
// that would be a quit dialog saying nothing is running while something is
// — and this codebase already has a standing rule against a hint that lies
// (F04's Bug 12), which a dialog is a worse place to break than a hint.
//
// Asking and then WAITING for the answer, rather than opening the dialog and
// filling it in later, is what keeps UI-14's own "Loading state: n/a"
// honest. It reports whether a request is outstanding; false means ask no
// further and open with what is in hand.
func (r *Root) requestTasksOffScreen() bool {
	if r.client == nil {
		return false
	}
	if r.tasksReqID != "" {
		// One is already in flight. Its answer was decided before this
		// keypress, which is refreshTasks' own caveat and is harmless
		// here: a task cannot have started in the microseconds since,
		// and the reply is about to land either way.
		return true
	}
	raw, err := json.Marshal(map[string]any{"scope": "all"})
	if err != nil {
		return false
	}
	env := protocol.Envelope{ID: rootRequestID(), Type: protocol.CmdListTasks, Payload: raw}
	if !r.ListTasksFn(env) {
		return false
	}
	r.tasksReqID = env.ID
	r.log.Debug("root: requesting task snapshot for a dialog", "corr_id", env.ID)
	return true
}

// openQuitConfirm is UI-14, opened once a task snapshot is in hand.
//
// Reached from two places and no others: the task-list reply that
// quitConfirmPending was waiting for, and quitConfirmWait expiring. Both go
// through here so the dialog is built in exactly one place whichever of the
// two wins.
func (r *Root) openQuitConfirm() {
	if !r.quitConfirmPending {
		return
	}
	r.quitConfirmPending = false
	n := len(r.conn.inFlight)
	if n == 0 && len(r.runningTasks()) == 0 {
		// Everything finished while we were asking. Nothing to warn
		// about, so quit rather than showing a dialog whose whole body
		// would be about work that is already done.
		r.pendingQuit = true
		return
	}
	r.OpenOverlay(NewConfirm(
		"Quit Point?", quitBody(r, n), "Quit", "Cancel", quitCmd(r),
	))
}

// drainPendingQuit fires the quit that openQuitConfirm decided needed no
// dialog. Separated from it because openQuitConfirm is called from inside
// handleTaskListEnvelope, which returns nothing a tea.Cmd can ride out on.
func (r *Root) drainPendingQuit() tea.Cmd {
	if !r.pendingQuit {
		return nil
	}
	r.pendingQuit = false
	doQuit(r)
	return tea.Quit
}
