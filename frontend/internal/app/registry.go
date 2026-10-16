package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
)

// Size is a workspace's declared minimum, in terminal cells.
//
// layout.MinWidth/MinHeight (50×12) stay the FLOOR every workspace must
// clear — Compute refuses to draw anything below them regardless of what a
// registration says — and a workspace may ask for more. The Editor will:
// a file tree beside a buffer beside a right pane does not fit in fifty
// columns, and "refuse below it with real numbers" (TEMPLATES.md's
// capability contract) is only honest if the number is the workspace's own.
type Size struct{ W, H int }

// WorkspaceDef is F12's registration struct — the one place a workspace is
// described, with the palette entry, the leader binding, the startup
// chooser, the switcher and the help screen all derived from it. Adding a
// workspace is an entry here, not edits in five files, which is the same
// property the action registry already has and the hook the parked
// capability system will use (TEMPLATES.md's `Register(Capability{...})`).
type WorkspaceDef struct {
	// ID is the key in Root's workspace map and in state.json's
	// workspace_state.
	ID WorkspaceID

	// Name is what the chooser, the switcher, the palette and the help
	// screen call it.
	Name string

	// LeaderKey is an optional leader sequence that switches straight to
	// this workspace. OPTIONAL, and chat's is deliberately empty: F12's
	// Build section asks for leader → `c`, but `c` has been
	// pane.right.toggle since F6 and `x` has been context.expand since
	// S13d, and actions.Registry.Validate rejects a duplicate leader
	// sequence at boot — app.New would return an error and main.go would
	// refuse to start. Aryan's call was to leave both shipped bindings
	// alone, so the paths to a workspace are the switcher (leader → `w`),
	// which lists every one of them with its idle readout, and the
	// palette. The field is wired and exercised; what is missing is a free
	// key to give the one workspace that exists.
	LeaderKey string

	// New constructs the workspace. Called on the FIRST switch to it and
	// never at startup — that is the whole of "do not auto-load the last
	// workspace", expressed as a factory rather than an instance.
	//
	// It is a func because app cannot import a workspace's package: a real
	// Workspace's Update returns app.Workspace, so chat imports app, and
	// app importing chat back is a cycle. main.go imports both and closes
	// over whatever each constructor needs.
	New func() Workspace

	// MinSize is this workspace's own minimum. A zero value means
	// layout's floor.
	MinSize Size
}

// registerWorkspaces validates Options.Workspaces and derives one switch
// action per workspace from it.
//
// Called from New, BEFORE the single r.actions.Validate(), so a leader key
// that collides with a hand-registered action fails at boot with both
// offenders named — exactly like any other duplicate. A workspace registry
// that could shadow an existing binding at runtime would be the "silently
// does the wrong thing six weeks later" failure registerActions' own doc
// exists to prevent.
func (r *Root) registerWorkspaces(defs []WorkspaceDef) error {
	for _, def := range defs {
		if def.ID == "" {
			return fmt.Errorf("workspace registry: a definition has no ID (Name %q)", def.Name)
		}
		if def.New == nil {
			return fmt.Errorf("workspace registry: %q has no New constructor", def.ID)
		}
		if prev, dup := r.defByID[def.ID]; dup {
			return fmt.Errorf("workspace registry: duplicate ID %q (%s and %s)",
				def.ID, prev.Name, def.Name)
		}
		if def.Name == "" {
			def.Name = string(def.ID)
		}
		r.defs = append(r.defs, def)
		r.defByID[def.ID] = def

		id := def.ID
		r.actions.Register(actions.Action[*Root]{
			ID:          "workspace.switch." + string(id),
			Name:        "Switch to " + def.Name,
			Description: "Make the " + def.Name + " workspace active, loading it if it is not",
			Category:    "Workspace",
			Leader:      def.LeaderKey,
			Run: func(root *Root) tea.Cmd {
				return root.switchWorkspace(id)
			},
		})
	}
	return nil
}

// Defs is the registered workspaces, in registration order. Read by the
// chooser, the switcher and the diagnostics screen.
func (r *Root) Defs() []WorkspaceDef { return r.defs }

// minSizeFor is a workspace's declared minimum, floored at layout's.
func (r *Root) minSizeFor(id WorkspaceID) Size {
	def := r.defByID[id]
	return Size{W: def.MinSize.W, H: def.MinSize.H}
}

// ---------------------------------------------------------------------------
// Idle readout — what the switcher and the palette show
// ---------------------------------------------------------------------------

// WorkspaceStatus is one row of F12's "idle state visible per workspace"
// requirement: `Chat · active`, `Editor · idle 3m of 5m`, `Data · not
// loaded`. Without it, unloading "feels like the app losing your work at
// random" — which is the whole reason the readout is specified alongside the
// timer rather than left to a later polish episode.
type WorkspaceStatus struct {
	Def WorkspaceDef

	Loaded bool
	Active bool

	// Suspended is whether a stored blob exists for this workspace —
	// position state waiting to be handed back on load. It is deliberately
	// independent of Loaded: a blob existing does NOT mean its workspace
	// is loaded, which is the distinction F12's "do not auto-load"
	// requirement turns on, and UI-12's `last:` line is the one place a
	// user is told the difference.
	Suspended bool

	// Idle is how long this workspace has been inactive, and Timeout is
	// how long it is allowed to be. Both zero unless Loaded and not
	// Active; Timeout zero means this workspace never unloads.
	//
	// Idle is the RAW elapsed time and may legitimately exceed Timeout --
	// see Pinned, and see Label for what is displayed instead.
	Idle    time.Duration
	Timeout time.Duration

	// Pinned is whether unsaved work is keeping this workspace loaded
	// past its own timeout (F13: walkIdle skips a workspace whose
	// DirtyBuffers count is non-zero rather than discarding its edits).
	//
	// It is the reason Idle can exceed Timeout: a pinned workspace never
	// unloads, so its elapsed time keeps growing against a budget it has
	// already spent. Reported as the readout showing `idle 2m35s of
	// 1m00s`, which reads as the app being broken when nothing is.
	Pinned bool
}

// Label is the readout: the countdown, CLAMPED so it can never show more
// elapsed than the budget it is counting against.
//
//	active                           the one on screen
//	not loaded                       no instance in memory
//	loaded · stays loaded            idle_unload_minutes = 0
//	idle 0m23s of 1m00s              counting up, normally
//	idle 1m00s of 1m00s · pinned     clamped, and said to be clamped
//
// # Why the clamp, and why the label beside it
//
// Idle is raw elapsed time and can legitimately exceed Timeout: a
// workspace pinned by unsaved work never unloads (F13's dirty branch in
// walkIdle), so its clock keeps running against a budget it has already
// spent. Rendered unclamped that read `idle 2m35s of 1m00s` -- a number
// past its own denominator, which reads as the app being broken when
// nothing is wrong at all.
//
// Clamping alone would be worse than either: `idle 1m00s of 1m00s` with
// no explanation says the workspace is about to unload, and it is not --
// it is the one workspace that definitely will not. So the clamp is
// always applied and the pinned case says why it is sitting at its limit.
// A reading that stops at the budget and names the reason cannot
// contradict itself in either direction.
//
// Minutes and seconds, not bare minutes: at a one-minute timeout -- the
// value F12's own verification sets -- "idle 0m of 1m" would be the only
// thing it ever said.
//
// Still a SNAPSHOT: the switcher builds its rows when the popup opens and
// nothing re-renders them while it is up (app\chooser.go, F12's,
// deliberately unchanged here). So this figure is correct when the popup
// opens and then stops advancing, which is a separate, logged thing.
func (s WorkspaceStatus) Label() string {
	switch {
	case s.Active:
		return "active"
	case !s.Loaded:
		return "not loaded"
	case s.Timeout == 0:
		return "loaded · stays loaded"
	}

	// The clamp is unconditional, not pinned-only. A workspace is also
	// briefly over its budget in the window between the timeout elapsing
	// and the next one-second tick unloading it, and a readout that could
	// print `of` backwards for one frame is not worth the special case.
	shown := s.Idle
	if shown > s.Timeout {
		shown = s.Timeout
	}
	out := fmt.Sprintf("idle %s of %s", shortDuration(shown), shortDuration(s.Timeout))
	if s.Pinned {
		out += " · pinned"
	}
	return out
}

// shortDuration renders a duration as whole minutes and seconds,
// truncated rather than rounded, so a readout never reads one unit higher
// than the thing it is counting down to.
//
// ALWAYS `%dm%02ds`, including under a minute: `0m01s`, not `1s`.
//
// # Why the under-a-minute branch went
//
// There was one, and it was this function's only special case: `if total
// < 60 { return fmt.Sprintf("%ds", total) }`. It contradicted this
// comment's own first line, and it broke the one place the output is
// READ AS A PAIR. The idle readout formats both sides with this
// function, so a sixty-second budget took the minutes branch and
// anything under it did not:
//
//	idle 58s of 1m00s        <- two formats, one line
//	idle 1m00s of 1m00s      <- the only pair that matched
//
// The asymmetry was never two code paths -- both sides always called
// here. It was one path whose output changed shape at sixty seconds,
// which is why it looked like the elapsed side had a formatter of its
// own. Removing the branch fixes the pair without giving either side a
// special case, which is what the alternative (a second formatter for
// elapsed) would have been.
//
// This is the only duration formatter in the package, so the grace
// warning now reads `unloads in 0m29s` rather than `unloads in 29s`.
// Noted rather than special-cased: one shape everywhere is worth more
// than a slightly shorter countdown in one sentence, and a second
// formatter is exactly the duplication that produced this bug.
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	return fmt.Sprintf("%dm%02ds", total/60, total%60)
}

// WorkspaceStatuses is every registered workspace with its current
// lifecycle state, in registration order. The ONE derivation both the
// switcher and the chooser read, so the two can never describe the same
// workspace two different ways — the same rule bindingLabel already
// enforces for the palette and the help screen.
func (r *Root) WorkspaceStatuses(now time.Time) []WorkspaceStatus {
	out := make([]WorkspaceStatus, 0, len(r.defs))
	for _, def := range r.defs {
		s := WorkspaceStatus{Def: def}
		_, s.Loaded = r.workspaces[def.ID]
		s.Active = s.Loaded && def.ID == r.active
		s.Suspended = len(r.suspended[def.ID]) > 0
		if s.Loaded && !s.Active {
			s.Timeout = r.idleTimeout(def.ID)
			if since, ok := r.lastActive[def.ID]; ok {
				s.Idle = now.Sub(since)
			}
			// Asked the same way walkIdle asks it, through the same
			// narrow interface, so the readout and the sweep cannot
			// disagree about which workspaces are pinned.
			if db, ok := r.workspaces[def.ID].(DirtyBuffers); ok {
				s.Pinned = db.DirtyBufferCount() > 0
			}
		}
		out = append(out, s)
	}
	return out
}

// activeTooSmall checks the active workspace's own MinSize against the
// terminal. Reports the message and false when it does not fit.
//
// Only a LOADED workspace is checked: the vacant diagnostics screen is F1's
// and has no registered minimum beyond layout's own floor, and it is
// precisely the screen a user needs to be able to read when something is
// wrong.
func (r *Root) activeTooSmall() (string, bool) {
	if _, loaded := r.workspaces[r.active]; !loaded {
		return "", true
	}
	def, ok := r.defByID[r.active]
	if !ok {
		return "", true
	}
	min := def.MinSize
	if (min.W == 0 || r.w >= min.W) && (min.H == 0 || r.h >= min.H) {
		return "", true
	}
	return fmt.Sprintf("%s needs %d×%d — terminal is %d×%d",
		def.Name, min.W, min.H, r.w, r.h), false
}
