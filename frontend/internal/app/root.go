package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// leaderHintDelay is how long the leader waits before showing the hint bar.
// Short enough to help, long enough that a fluent two-key sequence never sees
// it.
const leaderHintDelay = 400 * time.Millisecond

// animInterval is one frame of the header animation. Slow enough to read as
// a pulse rather than a flicker, and — since it only ever runs while
// something is streaming — never a cost the app pays at rest.
const animInterval = 120 * time.Millisecond

// maxProtocolErrors bounds what the placeholder shows; a backend printing on
// every line must not grow the model without limit.
const maxProtocolErrors = 5

// Root is the one model Bubble Tea drives.
//
// Two rules keep it from rotting: the root never reads a workspace's internal
// fields, and workspaces never talk to each other — they emit messages the
// root routes.
type Root struct {
	// active is which workspace the frame is FOR. It is NOT "the loaded
	// workspace": a key absent from r.workspaces is NOT_LOADED, and
	// r.active can perfectly well name one (at startup before the chooser
	// is answered, and after unloading the only loaded workspace), in
	// which case current() returns the vacant diagnostics screen. F12
	// needs those two facts separate — "which workspace would I be
	// looking at" and "is it in memory" are the whole subject of this
	// episode.
	active     WorkspaceID
	workspaces map[WorkspaceID]Workspace

	// suspended is each workspace's own ~2KB blob, OPAQUE here. Root
	// stores what Suspend returned and hands it back to Resume unread —
	// the only way "the root never reads a workspace's internal fields"
	// survives being given a persistence mechanism. chat's blob is F18's
	// versioned anchor; see config\state.go's WorkspaceState.
	suspended map[WorkspaceID][]byte

	// vacant is what renders when r.active names nothing loaded: F1's
	// diagnostics placeholder, held HERE rather than in r.workspaces so
	// the map keeps meaning exactly "what is loaded". It is a live model,
	// not a rebuilt one — it receives DiagnosticsMsg through storeCurrent
	// like any workspace, so the connection banner and protocol errors
	// stay current on it.
	vacant Workspace

	// defs/defByID are F12's workspace registry (registry.go). Empty in
	// most of this package's own tests, which build a Root with no
	// Options.Workspaces: nothing registered means nothing to choose, so
	// the startup chooser never opens underfoot — the same opt-out F10's
	// first-run chooser needed and got through State.Provider.
	defs    []WorkspaceDef
	defByID map[WorkspaceID]WorkspaceDef

	// lastActive is when each LOADED, INACTIVE workspace stopped being
	// active — D87's one timer per workspace, with the map's own length as
	// the count. The active workspace is deliberately absent: its timer
	// starts when you leave it.
	lastActive map[WorkspaceID]time.Time

	idle idleState

	// lastWorkspace is state.json's last_workspace as it was at startup:
	// what UI-12 pre-selects and names. Read once, never used to load
	// anything.
	lastWorkspace string

	// chooserShown latches UI-12 so it opens once per reason rather than
	// once per frame — see ensureStartupChooser.
	chooserShown bool

	// suspendGen is the debounced workspace-state save's generation
	// counter, F4's draftGen shape for F4's reason.
	suspendGen int

	// quitConfirmPending is app.quit waiting on a fresh task snapshot
	// before it can draw UI-14 -- see requestTasksOffScreen. pendingQuit
	// is the opposite answer: the snapshot came back with nothing to warn
	// about, so there is no dialog and the quit proceeds on the next
	// Update.
	quitConfirmPending bool
	pendingQuit        bool

	// suspendPending is whether a debounced position save is already
	// scheduled -- see schedulePositionSave for why a flag and not only
	// the generation counter.
	suspendPending bool

	// cfg is config.toml as main.go read it, kept for the ONE table this
	// package resolves per workspace at runtime: [workspace]'s
	// idle_unload_minutes, which is per-workspace-overridable and so
	// cannot be flattened to a single value at startup.
	cfg config.Config

	// restoreTerminal is Options.RestoreTerminal -- see that field for
	// the one failure it exists for. Nil is normal.
	restoreTerminal func() error

	// SaveWorkspaceStateFn writes state.json's two Root-owned fields.
	// Defaults to config.SaveWorkspaceState; a test substitutes a
	// recorder, for the reason chat's own saveStateFn exists — the real
	// one writes %LOCALAPPDATA%\Point\state.json, and CLAUDE.md is
	// explicit that nothing automated may touch that file outside a
	// scratch profile.
	SaveWorkspaceStateFn func(lastWorkspace string, blobs map[string]json.RawMessage) error

	// Now is the clock the idle timers read, defaulting to time.Now. A
	// test substitutes one so a five-minute timeout can be exercised
	// without waiting five minutes -- and so "three workspaces left at
	// three different moments unload at three different moments" is
	// assertable at all, which is Done-when #4b.
	Now func() time.Time

	// StatusLinger is how long a status message stays before its expiry
	// fires. Zero means noticeLinger.
	//
	// Substitutable for the reason Now is, and for a sharper one: the
	// expiry is now derived in Update (see expireStatusIfChanged), so it
	// rides in the command batch of EVERY message that changes the
	// status. tea.Batch returns a lone command unwrapped, so a test that
	// executes what Update handed back executes a real tea.Tick and parks
	// for the whole duration -- which is what turned
	// TestHeaderAnimationRunsOnlyWhileStreaming into a six-second test
	// the moment this was added. A suite that sleeps is a suite people
	// stop running.
	StatusLinger time.Duration

	overlay Overlay

	layout  layout.Layout
	theme   theme.Theme
	actions *actions.Registry[*Root]
	w, h    int

	client *protocol.Client
	conn   connection

	leader leaderState
	paste  pasteState
	anim   animState

	// rightPaneOpen is F6's toggle — the user's own preference, independent
	// of the width breakpoint that auto-hides the pane below 90 columns
	// (layout.Compute applies both: RightPaneOpen && width). Defaults open.
	rightPaneOpen bool

	// tasks is the global snapshot behind the Task card — see tasks.go's
	// doc comment on why Root, not the workspace, owns it. tasksReqID is
	// the correlation id of a ListTasks request this root issued itself,
	// distinct from any workspace's own pending queries.
	tasks              []TaskRow
	tasksReqID         string
	tasksDirty         bool
	taskCardWasVisible bool

	// contextViewWasShown is refreshContextIfShown's transition memory.
	// Written ONLY there — see its doc comment for why.
	contextViewWasShown bool

	// diagnosticsSeq is pushDiagnostics's monotonic counter — see its own
	// doc comment for why a sequence number, not arrival order, is what a
	// DiagnosticsMsg's receiver has to trust.
	diagnosticsSeq int
	// ListTasksFn sends one ListTasks request, reporting whether it went
	// out. Defaults to sendListTasks (the real r.client.Request call); a
	// test substitutes a recorder — the same seam chat.Workspace's sendFn
	// already uses, for the same reason: exercising refreshTasks's
	// coalescing logic (tasksDirty) needs no live protocol.Client.
	ListTasksFn func(protocol.Envelope) bool

	// SettingsSendFn is settings.go's equivalent of ListTasksFn, for the
	// identical reason: a unit test's protocol.Client is never Start()ed
	// (it cannot spin up a real backend), so the real settingsSend always
	// fails there — which is enough to prove an ATTEMPT was made (picker.go's
	// own tests already rely on this for awaitingConnection), but not
	// enough to exercise what happens once a request genuinely succeeds,
	// such as whether r.settings.modelsReqID is set before or after the
	// item list is rebuilt (the exact ordering bug a verification round
	// found in finalizeProviderSelection). Defaults to settingsSend.
	SettingsSendFn func(cmdType string, payload map[string]any) (string, bool)

	// lastFrame is the most recently rendered screen, re-served while a
	// paste burst is assembling — see View's doc comment for why that is a
	// correctness fix and not just a speed one.
	lastFrame string

	// trayClock drives the tray's own m:ss redraw — see trayTickWanted.
	trayClock struct {
		running bool
		gen     int
	}

	// backgroundSeen records, per task id, that this client saw a
	// TaskStarted saying the task is a background one. Authoritative where
	// it has an answer (the backend computed it from max_steps), and used
	// ONLY to promote a task into the tray, never to demote one. Pruned to
	// the current snapshot in handleTaskListEnvelope, so it cannot grow
	// without bound across a long session.
	backgroundSeen map[string]bool

	// picker is F8's @ picker controller — see picker.go's own doc comment
	// for why Root, not chat.Workspace, owns the backend round trips it
	// needs. Zero value: no picker open.
	picker pickerState

	// tray is F9a's task tray controller -- see tray.go. Root, not a
	// workspace, owns it for the same reason it owns the Task card's
	// ListTasks cycle: the list is global, and a task whose originating
	// view has been unloaded still has to appear and still has to open.
	// Zero value: no tray open.
	tray trayState

	// settings is F10's Settings popup controller (settings.go) -- the
	// provider chooser, credential entry and the Appearance tab. Root owns
	// it for the same reason it owns the tray and the picker: an open
	// Overlay never receives protocol.EnvelopeMsg (every envelope routes to
	// the active WORKSPACE only), so the ListProviders/ListProviderModels/
	// SetProviderApiKey round trips need an owner outside any Overlay.
	settings settingsState

	// activeProvider is the provider+model Settings/the first-run chooser
	// has picked, read-only here (chat.Workspace is the sole writer of
	// state.json's own copy -- see config\state.go's Provider doc and
	// chat\providers.go). Root keeps its own copy only because it is what
	// Settings' Providers tab renders "active" against and what the
	// first-run trigger below reads; every CHANGE still flows through
	// ProviderDefaultSetMsg to chat, never written here directly to disk.
	activeProvider *config.ProviderDefault

	// needsProviderSetup is opts.State.Provider == nil, latched at New()
	// and consumed on the first tea.WindowSizeMsg -- see that case's own
	// comment for why an overlay cannot open any earlier than that.
	needsProviderSetup bool

	// behaviourOverrides is F10b's own copy of the Behaviour tab's active
	// overrides -- what behaviourRowDecor renders (behaviour.go) and what
	// replayBehaviourOverrides resends on every protocol.ConnectedMsg.
	// Seeded at New() from opts.State.BehaviourOverrides (chat.Workspace is
	// the sole state.json writer of its own copy, same rule activeProvider
	// already follows -- see config\state.go's own doc on
	// BehaviourOverrides and chat\providers.go's applyBehaviourOverrides),
	// then kept current from each SetBehaviourOverride reply's own echoed
	// map (behaviour.go's onBehaviourOverrideSet) -- never written here
	// optimistically, so this field only ever holds what the backend has
	// actually confirmed (F10b's Decision 2).
	behaviourOverrides map[string]any

	// mouseEnabled mirrors whatever mouse option main.go actually gave
	// tea.NewProgram, the one piece of runtime state this package cannot
	// read back from Bubble Tea itself -- Settings' Appearance tab (F10)
	// needs it both to render the toggle's current state and to know
	// which of tea.EnableMouseCellMotion/tea.DisableMouse to send.
	mouseEnabled bool

	status   string
	log      *slog.Logger
	quitting bool

	env Environment
}

// connection is everything the root knows about the backend. It is written
// only from client messages arriving in Update — never from another goroutine.
type connection struct {
	state           protocol.ConnState
	handshaked      bool
	protocolVersion int
	capabilities    protocol.Capabilities
	restarts        int
	banner          string
	bannerDetail    string
	inFlight        []string
	unknown         []string
	interrupted     []string
	protocolErrors  []string
}

// animState drives the header animation (render.go's header).
//
// running is the whole idle-CPU guarantee: the tick re-arms itself ONLY
// while the active workspace reports Streaming(), so an untouched app
// schedules nothing at all and its frame count stops changing. gen makes a
// stale tick — one already in flight when streaming ended — harmless
// instead of a second, parallel loop, the same generation-counter shape
// draft.go and paste.go both use.
type animState struct {
	running bool
	frame   int
	gen     int
}

type leaderState struct {
	pending  bool
	sequence string
	showHint bool
	gen      int
}

// Environment is what main resolved before the program started: paths, config
// outcomes, and anything the placeholder should show but the root should not
// have to discover for itself.
type Environment struct {
	BackendDir  string
	LogFile     string
	ConfigPath  string
	ConfigError string
	StatePath   string
	StateError  string
	PasteEvents int
	PasteLines  int
	Notes       []string
}

// Options builds a Root.
type Options struct {
	Theme  theme.Theme
	Client *protocol.Client
	Log    *slog.Logger
	State  config.State
	Env    Environment

	// RestoreTerminal is tea.Program.RestoreTerminal, handed in by
	// main.go because only main.go holds the Program.
	//
	// It exists for exactly one case, and that case is unrecoverable
	// without it: tea.ExecProcess's own ReleaseTerminal can FAIL (a
	// Windows console whose saved input mode SetConsoleMode refuses --
	// see cmd\point\consolemode.go), and bubbletea then returns from
	// exec early, before starting the child and before its own
	// RestoreTerminal. That leaves the input reader cancelled and the
	// renderer stopped: the program is running and can never receive
	// another key. Calling this puts both back.
	//
	// Optional -- nil in every test and on any path that has no Program
	// -- so a nil check is the contract, not an oversight.
	RestoreTerminal func() error

	// MouseEnabled is the mouse option main.go actually gave
	// tea.NewProgram (state.json's UI override already applied there) --
	// see Root.mouseEnabled's own doc for why this package cannot ask
	// Bubble Tea for it instead.
	MouseEnabled bool

	// Config is config.toml as main.go read it. Only F12's [workspace]
	// table is used from it here; everything else main.go already applied
	// itself before building this.
	Config config.Config

	// Workspaces is F12's registry (registry.go). Each entry's New is
	// called on the first switch to it and NEVER at startup, which is how
	// "do not auto-load the last workspace" is expressed structurally
	// rather than as a rule somewhere to remember.
	//
	// Empty is a supported state and is what most of this package's own
	// tests pass: nothing registered means nothing to choose, so UI-12
	// never opens, and current() renders F1's diagnostics screen exactly
	// as it did before this episode.
	Workspaces []WorkspaceDef
}

// New builds the root and validates the action registry.
//
// A duplicate ID or binding is a startup failure with both offenders named,
// not a keypress that silently does the wrong thing six weeks later.
func New(opts Options) (*Root, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	// What the frame is FOR, not what is loaded -- nothing is loaded here.
	// last_workspace governs it so UI-12 opens with the right button
	// pre-selected and so the chat.* actions' When predicates
	// (root.active == WorkspaceChat) read the same before and after this
	// episode.
	active := WorkspaceChat
	if opts.State.LastWorkspace != "" {
		active = WorkspaceID(opts.State.LastWorkspace)
	}

	r := &Root{
		active:     active,
		workspaces: make(map[WorkspaceID]Workspace),
		suspended:  make(map[WorkspaceID][]byte),
		defByID:    make(map[WorkspaceID]WorkspaceDef),
		lastActive: make(map[WorkspaceID]time.Time),
		idle: idleState{
			warned:       make(map[WorkspaceID]bool),
			dirtyNoticed: make(map[WorkspaceID]bool),
		},
		lastWorkspace:   opts.State.LastWorkspace,
		cfg:             opts.Config,
		theme:           opts.Theme,
		client:          opts.Client,
		log:             opts.Log,
		env:             opts.Env,
		restoreTerminal: opts.RestoreTerminal,
		conn:            connection{state: protocol.StateStarting},
		rightPaneOpen:   true,
		// F10: nil exactly when nothing has been chosen through Point's own
		// UI yet (config\state.go's Provider doc). needsProviderSetup is
		// read exactly once, on the first WindowSizeMsg -- see that case.
		activeProvider:     opts.State.Provider,
		needsProviderSetup: opts.State.Provider == nil,
		mouseEnabled:       opts.MouseEnabled,
		behaviourOverrides: opts.State.BehaviourOverrides,
	}
	r.ListTasksFn = r.sendListTasks
	r.SettingsSendFn = r.settingsSend
	r.SaveWorkspaceStateFn = config.SaveWorkspaceState
	r.Now = time.Now

	r.actions = registerActions()
	// Registered before Validate, so a workspace's leader key colliding
	// with a hand-registered action is a boot failure naming both
	// offenders -- not a key that silently does the other thing.
	if err := r.registerWorkspaces(opts.Workspaces); err != nil {
		return nil, err
	}
	if err := r.actions.Validate(); err != nil {
		return nil, err
	}

	// NOTHING is loaded. A key absent from r.workspaces is NOT_LOADED;
	// there is no separate enum for it, and at startup every key is
	// absent -- F12's "do not auto-load anything at startup", which is
	// also F1's own stated intent ("absent from the map until it is
	// loaded") finally made true. F1 pre-seeded a Placeholder here, which
	// meant the map was never empty and NOT_LOADED was never observable;
	// the vacant screen is current()'s job now instead.
	r.restoreSuspended(opts.State)

	return r, nil
}

func (r *Root) Init() tea.Cmd {
	cmds := []tea.Cmd{r.current().Init(), r.pushDiagnostics()}
	if r.client != nil {
		r.client.Start()
		cmds = append(cmds, r.client.Next())
	}
	return tea.Batch(cmds...)
}

// SetWorkspace installs a workspace built outside this package.
//
// A real workspace's Update must return app.Workspace, which means its
// package has to import app — so app cannot import it back without a cycle.
// main.go imports both and wires them together after New, before Init runs.
func (r *Root) SetWorkspace(id WorkspaceID, ws Workspace) {
	r.workspaces[id] = ws
}

// current is the active workspace, or the vacant screen when r.active names
// nothing loaded.
//
// # The F1 invariant edit
//
// This used to CREATE a Placeholder and store it under r.active, which was
// invisible while a workspace was always loaded and is wrong now that one
// can be unloaded: every read of the active workspace resurrected the key,
// so "absent from the map is NOT_LOADED" became untrue one frame after any
// unload, and Done-when #1's NOT_LOADED state would never have been
// observable no matter which workspace was unloaded. The vacant screen now
// lives in its own field, outside the map, and the map means exactly what
// the lifecycle says it means.
//
// It is still a LIVE model and not a value rebuilt per call: storeCurrent
// writes Update's result back into r.vacant, so the diagnostics screen's
// connection banner and protocol error list keep updating while nothing is
// loaded -- which is the state a user with a broken backend spends their
// whole session in.
func (r *Root) current() Workspace {
	if ws, ok := r.workspaces[r.active]; ok {
		return ws
	}
	if r.vacant == nil {
		r.vacant = NewPlaceholder(r.active, r.theme)
	}
	return r.vacant
}

// storeCurrent writes back whatever the active workspace's Update returned.
//
// It writes to a key that is ALREADY in the map, never one that is not: an
// unconditional assignment to r.workspaces[r.active] is how the old
// current() resurrected an unloaded workspace, and there were eleven of
// those assignments. The vacant screen gets the same write so it stays
// live.
func (r *Root) storeCurrent(ws Workspace) {
	if _, loaded := r.workspaces[r.active]; loaded {
		r.workspaces[r.active] = ws
		return
	}
	r.vacant = ws
}

// now is the clock, substitutable for the idle tests.
func (r *Root) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update wraps the real update so ONE place decides whether the header
// animation should be running. Streaming starts and stops in a dozen
// different messages (an envelope, a key, a tick of chat's own); asking
// afterwards, once, is what keeps "the animation runs only while something
// streams" from being a rule every new case has to remember.
func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	statusBefore := r.status
	model, cmd := r.update(msg)
	// The same "ask once, afterwards" rule the animation uses below, and
	// for the same reason. The right pane becomes visible or stops being
	// visible from at least five different places — a resize crossing the
	// breakpoint, the toggle action, OpenOverlay, esc or a click closing
	// the overlay, closeOverlayIfTooSmall — and this used to be asked only
	// from recompute(). Three of those five close paths do not call
	// recompute, so taskCardWasVisible stayed true after the overlay
	// closed, and REOPENING it then looked like no transition at all and
	// refreshed nothing. Tasks that started while it was closed could not
	// appear until some later lifecycle event happened to fire a refresh
	// on its own — which, when every task started before the overlay was
	// opened, is the first TaskDone. That is exactly the reported
	// "the overlay shows nothing until the first task completes".
	r.refreshTasksIfVisible()
	r.refreshContextIfShown(msg)
	// The layout is DERIVED state and it has to be derived here, for the
	// exact reason the three refreshes around it are.
	//
	// F14 shipped broken on this and the symptom was reported as three
	// separate bugs. r.layout was recomputed only on a real resize
	// (View's own `r.layout.W != r.w` check), on ui.redraw, on the
	// right-pane toggle, on an overlay change and on a workspace
	// load/unload -- but NOT when anything a workspace feeds into
	// layout.State changed. F14 is the first episode where a workspace
	// feeds it anything: TerminalMode. So pressing the mode key did
	// nothing visible, and the change then appeared at the next unrelated
	// event -- switching workspace, or alt-tabbing (which delivers a
	// WindowSizeMsg) -- which read as "the terminal resizes itself at
	// random" and left no way to shrink it back, because the key that
	// shrinks it was equally invisible.
	//
	// Compute is pure arithmetic over a handful of ints (that is
	// LAYOUT.md's whole premise), so deriving it on every message costs
	// nothing measurable and cannot miss a path -- including the paths
	// with no tea.Cmd to hang it off, which is the same argument the
	// status expiry below is built on.
	r.recompute()
	// Asked here for the same "ask once, afterwards" reason the animation
	// and the two refreshes above are: the tray opens, closes, and is
	// swapped for a result card and back from several different messages,
	// and a flag updated at each of those call sites is the
	// taskCardWasVisible bug again. Derived here, it cannot miss a path --
	// including the result card returning the tray in its own place, where
	// nothing in root.go's own switch runs at all.
	extra := []tea.Cmd{}
	if anim := r.ensureAnimation(); anim != nil {
		extra = append(extra, anim)
	}
	if clock := r.ensureTrayClock(); clock != nil {
		extra = append(extra, clock)
	}
	// Asked here for the same "ask once, afterwards" reason as the three
	// above, and in this order. The chooser must wait for whatever else
	// wants the single overlay slot -- on a genuine first run that is
	// F10's provider chooser, which opens at the same WindowSizeMsg and
	// is a prerequisite rather than a peer -- so this asks whether
	// anything is open rather than racing it. The idle walk is asked
	// afterwards because the thing that makes it necessary (a workspace
	// becoming inactive) happens inside the message that just ran.
	if ch := r.ensureStartupChooser(); ch != nil {
		extra = append(extra, ch)
	}
	if walk := r.ensureIdleWalk(); walk != nil {
		extra = append(extra, walk)
	}
	// Asked here because the place that DECIDES it -- openQuitConfirm,
	// called from inside handleTaskListEnvelope -- has no tea.Cmd to
	// return one on.
	if q := r.drainPendingQuit(); q != nil {
		extra = append(extra, q)
	}
	// Asked here for the same "ask once, afterwards" reason, and it is
	// the only place that can be.
	//
	// r.status is assigned DIRECTLY from about forty places in this
	// package -- a refusal, a theme change, a backend transition, a
	// settings result, a paste -- and statusExpireMsg's own doc says so
	// and treats that as fine: "whatever wrote last wins". It was fine
	// while the status bar's left half was the bindings hint, because a
	// message that never faded only hid something recoverable from the
	// palette and from Help.
	//
	// F13's StatusContributor changed the stakes. The left half is now
	// the Editor's file segments -- name, line:col, language, line
	// ending, indent, read-only reason -- and r.status outranks them, so
	// a message that never expires permanently masks the only surface
	// those appear on. Reported against the paste counter, which is set
	// by a bare assignment in recordPaste and so never had a timer at
	// all: pasting 300 lines left "pasted 300 lines as one event" on
	// screen until something else happened to overwrite it, with the file
	// segments gone the whole time. Pressing esc in the Editor does the
	// same thing by a different route ("Browse Transcript is not
	// available right now", from dispatchKey's unavailable-chord branch).
	//
	// So the expiry is derived here rather than added to forty call
	// sites: Update is the one funnel every one of them passes through,
	// and comparing the value before and after cannot miss a path --
	// including the ones with no tea.Cmd to return a tick on, which is
	// why several of them are bare assignments in the first place.
	//
	// expiringStatus's own callers now schedule two ticks for one
	// message. That is harmless by statusExpireMsg's existing design: it
	// clears only if the text still matches, so the second tick finds
	// nothing to do.
	if exp := r.expireStatusIfChanged(statusBefore); exp != nil {
		extra = append(extra, exp)
	}
	if len(extra) > 0 {
		return model, tea.Batch(append([]tea.Cmd{cmd}, extra...)...)
	}
	return model, cmd
}

// ensureAnimation starts the tick loop if something is streaming and it is
// not already running. It never STOPS one: a tick that finds nothing
// streaming simply does not re-arm (see the animTickMsg case), which is the
// only way to end a tea.Tick that has already been scheduled.
func (r *Root) ensureAnimation() tea.Cmd {
	if !r.streaming() || r.anim.running {
		return nil
	}
	r.anim.running = true
	r.anim.gen++
	return animTick(r.anim.gen)
}

func animTick(gen int) tea.Cmd {
	return tea.Tick(animInterval, func(time.Time) tea.Msg {
		return animTickMsg{gen: gen}
	})
}

func (r *Root) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {

	case tea.WindowSizeMsg:
		r.w, r.h = m.Width, m.Height
		r.recompute()
		r.closeOverlayIfTooSmall()
		r.closeRightPaneFallbackIfObsolete()
		// F10's first-run chooser cannot open any earlier than this: Init()
		// runs before the terminal's real size is known (r.w/r.h are still
		// 0,0), and OpenOverlay's own fit check (overlayFits) would refuse
		// anything against a 0x0 terminal unconditionally. This is the
		// first moment a real size exists, so it is also the first moment
		// "does Settings fit" can be answered honestly. Latched so a LATER
		// resize — the terminal shrinking and growing again — never reopens
		// it a second time.
		if r.needsProviderSetup {
			r.needsProviderSetup = false
			return r, r.openSettingsPopup()
		}
		return r, nil

	case tea.KeyMsg:
		// F12's two per-keypress lifecycle rules, in the ONE place every
		// key arrives -- the taskCardWasVisible lesson applied to the
		// timer:
		//
		//   - the active workspace never unloads, so every key resets
		//     its clock;
		//   - and that is ALL a key does: a workspace you are not
		//     looking at is not kept alive by activity in one you are.
		//
		// Here rather than in dispatchKey, and the difference is not
		// cosmetic: handleKey's paste-burst detector CONSUMES a lone
		// pasteable key and holds it (paste.go), so a plain letter never
		// reaches dispatchKey at all on the frame it is pressed, and the
		// ACTIVE workspace's clock has to be reset whether or not the
		// key gets that far.
		//
		// What is NOT here any more is the global grace rescue: a key
		// pressed in one workspace used to reset the idle clock of every
		// OTHER workspace inside its grace window, which made a
		// backgrounded workspace unable to unload for as long as anybody
		// was typing anywhere. See lifecycle.go, where keepWarnedAlive
		// was.
		r.touchActive()
		return r.handleKey(m)

	case tea.MouseMsg:
		return r.dispatchMouse(m)

	case OpenHatchMsg:
		// The terminal pane's own `!` prefix, routed through a message
		// rather than a raw tea.ExecProcess Cmd -- see hatch.go's header.
		return r, r.openHatch(m.Command)

	case hatchDoneMsg:
		// F14's ExecProcess hatch. On the ordinary path tea.ExecProcess's
		// own exec() has already called RestoreTerminal and
		// resetLinesRendered before this message is delivered, which is
		// most of "full repaint" for free; the ClearScreen is belt and
		// suspenders, the same thing ui.redraw does for the same reason.
		//
		// refreshTasks() is Done-when 5's "subtle tray marker rather than
		// a missed toast": there is no toast mechanism in this app to
		// divert from, and refreshTasks is already gated on a task
		// surface being ON SCREEN, so this is a no-op unless the tray or
		// the right pane was open -- exactly the cases where a marker
		// would be seen.
		r.refreshTasks()

		// An ERROR here is not cosmetic and must never be reported as
		// "back from the shell", which is what this did first.
		//
		// bubbletea's exec() has two failure shapes and they need
		// different handling. If ReleaseTerminal failed, it returned
		// BEFORE starting the child and before RestoreTerminal -- so the
		// input reader is cancelled, the renderer is stopped and the alt
		// screen is already gone. The program is alive and cannot
		// receive another keystroke, which to the user is
		// indistinguishable from a crash. Recovering needs the Program
		// itself, which only main.go holds, hence RestoreTerminal in
		// Options: without it there is no way back from here at all.
		if m.err != nil {
			r.log.Warn("hatch failed", "err", m.err)
			restored := ""
			if r.restoreTerminal != nil {
				if err := r.restoreTerminal(); err != nil {
					r.log.Warn("could not restore the terminal after a failed hatch", "err", err)
					restored = " — and the terminal could not be reinitialised; quit with F3 / ctrl+q"
				} else {
					restored = " — the terminal has been reinitialised"
				}
			}
			return r, tea.Batch(tea.ClearScreen,
				status("the shell could not open: "+m.err.Error()+restored))
		}
		return r, tea.Batch(tea.ClearScreen, status("back from the shell"))

	case trayTickMsg:
		// The Running row's m:ss clock. Mirrors animTickMsg exactly — a
		// generation counter so a tick left over from an earlier loop is
		// dropped, and re-arming only while there is still a reason to,
		// which is the only way to end a tea.Tick already scheduled.
		if m.gen != r.trayClock.gen {
			return r, nil
		}
		if !r.trayClockWanted() {
			r.trayClock.running = false
			return r, nil
		}
		// Nothing to mutate, and that is only true because the note is
		// computed inside the render (PopupConfig.Decor -> trayDecor ->
		// trayNote). Arriving here causes a frame; the frame recomputes
		// the clock. It was NOT true when the note was baked into the
		// item at build time -- the tick fired, re-armed correctly, and
		// redrew the identical stored string, which is how a clock that
		// "ticked" still read 0:00. See RowDecor.
		return r, trayTick(m.gen)

	case animTickMsg:
		if m.gen != r.anim.gen {
			return r, nil // a tick left over from an earlier loop
		}
		if !r.streaming() {
			// Freeze, and re-arm nothing. The frame stays where it stopped
			// rather than snapping back, which is what makes a frozen
			// animation read as "idle" instead of "broken".
			r.anim.running = false
			return r, nil
		}
		r.anim.frame++
		return r, animTick(m.gen)

	case idleTickMsg:
		if !r.idle.running || m.gen != r.idle.gen {
			return r, nil // superseded, or the walk has already stopped
		}
		return r, r.walkIdle(r.now())

	case quitConfirmTimeoutMsg:
		r.openQuitConfirm()
		return r, r.drainPendingQuit()

	case unloadConfirmedMsg:
		return r, r.unloadAndReport(m.id, m.name)

	case unloadCancelledMsg:
		// The workspace is staying, so the pause requestUnload took has
		// to come off -- and only for the workspace that was asked
		// about, which is why the id is carried rather than assuming
		// r.active (nothing stops the question outliving a switch).
		if ws, loaded := r.workspaces[m.id]; loaded {
			if ap, ok := ws.(AutosavePauser); ok {
				return r, ap.ResumeAutosave()
			}
		}
		return r, nil

	case suspendSaveMsg:
		r.saveWorkspaceStateIfDue(m.gen)
		return r, nil

	case statusExpireMsg:
		// Compared by TEXT, not by a generation counter: r.status is
		// assigned directly from a dozen places in this package, and a
		// counter would mean every one of them had to remember to bump
		// it. Whatever wrote last wins, and an expiry whose text is no
		// longer on screen does nothing.
		if r.status == m.text {
			r.status = ""
		}
		return r, nil

	case leaderHintMsg:
		if r.leader.pending && m.gen == r.leader.gen {
			r.leader.showHint = true
		}
		return r, nil

	case statusMsg:
		r.status = string(m)
		return r, nil

	case WorkspaceStatusMsg:
		// F13: the active workspace's one transient line. Given an expiry
		// rather than left on screen, because a note is an event and the
		// bindings hint or the workspace's own segments are what belong
		// there the rest of the time. Same linger as an unload notice --
		// long enough to read a sentence, short enough not to outlive
		// what it is about.
		if m.Text == "" {
			return r, nil
		}
		r.status = m.Text
		return r, r.expiringStatus(m.Text, r.statusLinger())

	case PasteMsg:
		r.recordPaste(m)
		// Forwarded to the workspace too: the burst detector this message
		// comes from (paste.go) cannot tell a genuine paste from a HELD
		// hotkey whose OS auto-repeat got batched the same way a real
		// paste is — F3's Chat workspace recovers that case for its own
		// single-letter keys. See chat\scroll.go's handlePastedBurst.
		ws, wsCmd := r.current().Update(m)
		r.storeCurrent(ws)
		return r, tea.Batch(r.pushDiagnostics(), wsCmd)

	case pasteFlushMsg:
		return r, r.onFlushTick()

	case pasteReleaseMsg:
		if m.gen != r.paste.gen {
			return r, nil
		}
		return r, r.releaseHeldKey()

	// ---- backend ------------------------------------------------------

	case protocol.ConnectingMsg:
		r.conn.state = protocol.StateStarting
		r.conn.banner = ""
		r.conn.bannerDetail = ""
		return r, r.afterBackend()

	case protocol.ConnectedMsg:
		r.conn.state = protocol.StateConnected
		r.conn.handshaked = true
		r.conn.protocolVersion = m.ProtocolVersion
		r.conn.capabilities = m.Capabilities
		r.conn.banner = ""
		r.conn.bannerDetail = ""
		r.conn.unknown = nil
		// The visible counter is every restart, however it was triggered.
		// Reading it from the client rather than incrementing here is what
		// keeps it honest: a manual retry is a real restart, and it used to be
		// invisible because only the automatic path bumped a number.
		if r.client != nil {
			r.conn.restarts = r.client.Restarts()
		}
		if m.Restarted {
			r.status = "backend restarted"
		} else {
			r.status = "backend connected"
		}
		// The workspace sees this too — F3's Chat workspace waits for it
		// before issuing its first query, since a query fails while
		// c.backend is nil.
		ws, wsCmd := r.current().Update(m)
		r.storeCurrent(ws)
		r.refreshTasks()
		// F10b Decision 1: every connect AND every later reconnect --
		// including one caused by the BACKEND restarting while this
		// process keeps running -- replays every currently-confirmed
		// Behaviour override, since B26's own override layer forgets them
		// on that side by design. See replayBehaviourOverrides' own doc
		// for why resending r.behaviourOverrides wholesale here is also
		// what makes a reconnect with MULTIPLE active overrides correct,
		// not just the most recently changed one.
		return r, tea.Batch(r.afterBackend(), wsCmd, r.retryPickerIfAwaiting(),
			r.retrySettingsIfAwaiting(), r.replayBehaviourOverrides())

	case protocol.VersionMismatchMsg:
		// Refuse, with BOTH versions named. This is the envelope version from
		// backend\runtime\events.py, not the repo's VERSION file — nothing on
		// the wire reads that.
		r.conn.banner = fmt.Sprintf(
			"protocol version mismatch: Point speaks v%d, the backend speaks v%d",
			m.Client, m.Backend)
		return r, r.afterBackend()

	case protocol.BackendLostMsg:
		r.conn.state = protocol.StateLost
		// UNKNOWN, not FAILED — at this moment nobody knows.
		r.conn.unknown = m.UnknownTasks
		r.conn.inFlight = nil
		r.conn.banner = "backend lost — restarting"
		r.conn.bannerDetail = m.Reason
		r.status = "backend lost"
		// F5: the workspace sees this too. A task that was streaming when
		// the backend died has to end SOMEWHERE, and F5 requires it end in
		// the same interrupted state a user cancel produces — one
		// rendering path, two causes. Without this forward the only thing
		// that ever stopped such a spinner was the stream_timeout
		// watchdog, two minutes later.
		ws, wsCmd := r.current().Update(m)
		r.storeCurrent(ws)
		return r, tea.Batch(r.afterBackend(), wsCmd)

	case protocol.TasksInterruptedMsg:
		r.conn.interrupted = append(r.conn.interrupted, m.TaskIDs...)
		r.conn.unknown = nil
		r.conn.inFlight = nil
		return r, r.afterBackend()

	case protocol.BackendFailedMsg:
		r.conn.state = protocol.StateFailed
		// A status-bar banner with a retry action, never a modal — the rest of
		// the app stays usable.
		r.conn.banner = m.Reason
		r.conn.bannerDetail = m.Detail
		r.status = "backend unavailable · " + r.retryHint()
		return r, r.afterBackend()

	case protocol.ProtocolErrorMsg:
		r.conn.protocolErrors = append(r.conn.protocolErrors,
			fmt.Sprintf("line %d: %s (%s)", m.Line, m.Text, m.Message))
		if n := len(r.conn.protocolErrors); n > maxProtocolErrors {
			r.conn.protocolErrors = r.conn.protocolErrors[n-maxProtocolErrors:]
		}
		return r, r.afterBackend()

	case protocol.EnvelopeMsg:
		// F2 does not interpret payloads beyond routing. What the root keeps
		// for itself is the task lifecycle, which is what makes "every
		// spinner stops" checkable — the payload itself goes to the
		// workspace, which is the first consumer (F3's GetSession/
		// ListSessions/CreateSession replies).
		if r.client != nil {
			r.conn.inFlight = r.client.InFlight()
		}
		r.handleTaskListEnvelope(m.Envelope)
		r.handleTrayEnvelope(m.Envelope)
		pickerCmd := r.handlePickerEnvelope(m.Envelope)
		settingsCmd := r.handleSettingsEnvelope(m.Envelope)
		ws, wsCmd := r.current().Update(m)
		r.storeCurrent(ws)
		bgCmd := r.deliverBackgroundReply(m)
		return r, tea.Batch(r.afterBackend(), wsCmd, pickerCmd, settingsCmd, bgCmd)

	case openOverlayMsg:
		// tray.go's OnSelect cannot assign r.overlay itself: it runs inside
		// the Popup's own Update, and setOverlay is "the ONE place
		// r.overlay is ever assigned". So it asks, here.
		r.OpenOverlay(m.o)
		return r, nil

	case CloseWorkspaceOverlayMsg:
		// Only if it is still the one on screen -- see the message's doc.
		// setOverlay is the one place r.overlay is ever assigned, so this
		// goes through it rather than clearing the field.
		if m.Overlay != nil && r.overlay == m.Overlay {
			r.setOverlay(nil)
			r.recompute()
		}
		return r, nil

	case backgroundWorkspaceMsg:
		// A DeliverToWorkspace message, back at the workspace that
		// produced it -- whether or not that workspace is active now.
		// Re-resolved from the map rather than from a captured pointer,
		// so one addressed to a workspace that has since been unloaded is
		// dropped instead of reviving it.
		ws, loaded := r.workspaces[m.id]
		if !loaded {
			return r, nil
		}
		updated, cmd := ws.Update(m.msg)
		r.workspaces[m.id] = updated
		if m.id == r.active {
			// It is on screen again, so its commands are ordinary
			// commands.
			return r, cmd
		}
		// Still backgrounded: the same rule applies to whatever this
		// produced, so a chain of allow-listed messages keeps working.
		return r, r.backgroundCmd(m.id, cmd)

	case SendToChatMsg:
		// F15's `send to chat`. Root routes it because workspaces never
		// talk to each other and because Chat may not be loaded — see
		// editoragent.go.
		return r, r.sendToChat(m)

	case OpenWorkspaceOverlayMsg:
		// B9, F13b: the same destination, reached from OUTSIDE this
		// package. The Editor's stale-save dialog is three buttons over a
		// file and an mtime app knows nothing about, so it arrives built
		// rather than named. OpenOverlay still applies the fit check, so
		// a workspace cannot force one onto a terminal too small for it.
		if m.Overlay != nil {
			r.OpenOverlay(m.Overlay)
		}
		return r, nil

	case cancelTaskMsg:
		r.sendCancelTask(m.taskID)
		return r, nil

	case resultDiscardedMsg:
		// Nothing to clean up -- discard deletes nothing, which is
		// Done-when #8 and is structural: this build has no event that
		// could delete a task or a message. The tray's rows are left in
		// trayState exactly as they were, so reopening finds the same task
		// and re-fetches the same text.
		r.status = "card discarded -- the result is still in the tray"
		r.log.Debug("tray: result card discarded", "task", m.taskID)
		return r, nil

	case submitCredentialMsg:
		// CredentialPrompt's own Update cannot reach protocol.Client —
		// see credential.go's doc on submitCredentialMsg.
		return r, r.settingsSubmitKey(m.provider, m.key)

	case submitBehaviourValueMsg:
		// Same reason, the Behaviour tab's numeric-entry counterpart — see
		// credential.go's doc on submitBehaviourValueMsg.
		return r, r.settingsSubmitBehaviourValue(m.key, m.value)

	case OpenAttachmentPickerMsg:
		// F8's @ picker — see picker.go. Root, not the workspace, opens
		// this one: it needs OpenOverlay and its own backend round trips
		// (ListAgents, ListProjectFiles/ListDirectory), the same reason
		// Root owns the Task card's own ListTasks cycle.
		return r, r.openAttachmentPicker(m)

	case protocol.ClosedMsg:
		if !r.quitting {
			r.conn.state = protocol.StateFailed
			r.conn.banner = "backend client stopped"
		}
		return r, nil
	}

	// A message that names its own workspace goes THERE, active or not --
	// see WorkspaceAddressed for the F14 loop this exists for. Re-resolved
	// from the map, like backgroundWorkspaceMsg's own delivery, so one
	// addressed to a workspace that has since been unloaded is dropped
	// rather than reviving it.
	if addr, ok := msg.(WorkspaceAddressed); ok {
		id := addr.TargetWorkspace()
		ws, loaded := r.workspaces[id]
		if !loaded {
			return r, nil
		}
		updated, cmd := ws.Update(msg)
		r.workspaces[id] = updated
		if id == r.active {
			return r, cmd
		}
		// Backgrounded: the same default-deny filter every other
		// background command goes through, so the loop keeps turning
		// without the workspace being able to take the status line.
		return r, r.backgroundCmd(id, cmd)
	}

	ws, cmd := r.current().Update(msg)
	r.storeCurrent(ws)
	return r, cmd
}

// afterBackend refreshes the placeholder and asks for the next backend
// message. Every handler of a client message must return this, or the stream
// stops after one event.
func (r *Root) afterBackend() tea.Cmd {
	cmds := []tea.Cmd{r.pushDiagnostics()}
	if r.client != nil {
		cmds = append(cmds, r.client.Next())
	}
	return tea.Batch(cmds...)
}

// rootRequestID mints a correlation id for a query Root sends itself —
// chat.localID's equivalent. Root cannot reuse that one without importing
// chat, which would cycle (chat already imports app).
func rootRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("root-%p", &b)
	}
	return hex.EncodeToString(b[:])
}

// refreshTasks asks the backend for the current global task snapshot — F6's
// Task card, which Root owns (tasks.go's doc comment). Only fires while the
// pane that shows it is actually on screen, and never while a request is
// already outstanding: this is the honest replacement for a "queued" state
// the wire has no push event for (B12's scheduler sets TaskState.QUEUED
// in-process and emits nothing), called from real lifecycle moments — a
// fresh connection, and any TaskStarted/terminal event this client saw
// (handleTaskListEnvelope) — never from a timer, so Done-when #6 ("no
// subscriptions while hidden") holds: a hidden pane issues zero requests.
// rightPaneVisible is broader than r.layout.ShowRightPane: it is also true
// while the narrow-width fallback overlay is open (RightPaneFallback), since
// that overlay shows the SAME cards and needs the SAME live data — a gate
// that only checked the real pane left the overlay's Task section frozen at
// whatever r.tasks held the moment it was opened, never refreshing again
// until it was closed and reopened.
func (r *Root) rightPaneVisible() bool {
	if r.layout.ShowRightPane {
		return true
	}
	_, ok := r.overlay.(RightPaneFallback)
	return ok
}

// tasksOnScreen is rightPaneVisible widened by F9a's tray.
//
// refreshTasks deliberately sends NOTHING while the surface that shows its
// answer is hidden (F6's Done-when #6), so every surface that displays the
// global snapshot has to be counted here or it renders one frozen at the
// moment it opened. rightPaneVisible already learned this once -- the
// narrow-width fallback overlay shows the same cards and was left with a
// Task section that never updated until it was closed and reopened. The
// tray is the third such surface, and the failure would be worse: a running
// task that never moves to Finished while you are watching the list whose
// whole job is to tell you it did.
func (r *Root) tasksOnScreen() bool {
	return r.rightPaneVisible() || r.trayOpen()
}

// refreshTasks asks for the global task snapshot, event-driven and ONLY
// while a surface that shows the answer is on screen -- F6's Done-when #6,
// which chat's own TestReopeningTheOverlayRefreshesWorkStartedWhileItWas
// Closed asserts directly. F12 leaves that gate exactly where it was; what
// it adds is requestTasksOffScreen for the two consumers that are dialogs
// rather than panes. See that function.
func (r *Root) refreshTasks() {
	if r.client == nil || !r.tasksOnScreen() {
		return
	}
	if r.tasksReqID != "" {
		// A request is already in flight, so whatever answer it brings
		// back was already decided on the backend before THIS trigger
		// happened — it cannot reflect this event. Dropping the trigger
		// outright (the original bug) is why three @research fired
		// together showed one task at a time, swapping identity as each
		// one finished: every lifecycle event that landed inside the
		// first request's round trip was silently discarded, and nothing
		// remembered that the snapshot it eventually got back was already
		// stale. Setting the flag instead means handleTaskListEnvelope
		// fires exactly one follow-up once this request resolves, so the
		// snapshot converges within one extra round trip instead of only
		// updating on whichever lifecycle event happens to land while
		// nothing else is in flight.
		r.tasksDirty = true
		return
	}
	raw, err := json.Marshal(map[string]any{"scope": "all"})
	if err != nil {
		return
	}
	env := protocol.Envelope{ID: rootRequestID(), Type: protocol.CmdListTasks, Payload: raw}
	if !r.ListTasksFn(env) {
		return
	}
	r.tasksReqID = env.ID
	r.log.Debug("root: requesting task snapshot", "corr_id", env.ID)
}

// sendListTasks is ListTasksFn's real implementation.
func (r *Root) sendListTasks(env protocol.Envelope) bool {
	_, err := r.client.Request(env, 0)
	return err == nil
}

// refreshTasksIfVisible asks again when the pane just BECAME visible —
// a resize crossing 90 columns, the toggle action, or the fallback overlay
// being opened — and not while it merely stays visible, where
// refreshTasks's own in-flight guard would make a repeat harmless but
// pointless.
//
// Called from Root.Update, once, after every message: see the comment
// there for why hanging this off recompute() instead was wrong. The
// transition it detects is the ONLY chance to learn about work that
// started while nothing was on screen to show it — refreshTasks
// deliberately sends nothing while hidden, so a missed transition is not
// a delayed update, it is a permanently missed one.
func (r *Root) refreshTasksIfVisible() {
	// tasksOnScreen, not rightPaneVisible: the predicate that gates the
	// REQUEST and the predicate that detects the TRANSITION have to be the
	// same one, or opening the tray with the pane hidden is a transition
	// nothing notices and a request refreshTasks would have sent.
	visible := r.tasksOnScreen()
	if visible && !r.taskCardWasVisible {
		r.refreshTasks()
	}
	r.taskCardWasVisible = visible
}

// refreshContextIfShown keeps UI-09 current while it is open. Before this,
// the overlay only ever drew whatever the workspace last received — the
// once-per-load pull or the backend's push after a turn PERSISTS — so a
// task that started (a new unanswered question) or ended without an
// answer never reached an open overlay. Found live: one @research running,
// UI-09 open, "next turn evicts: nothing", while a fresh report for that
// same moment said "unanswered: task X (running) has no reply".
//
// Asked ONCE, after every message, exactly like refreshTasksIfVisible and
// for the same reason: UI-09 opens from three places (the leader action, a
// click on the Status card, rightPaneOverlay's own enter/click swap) and
// closes from more (esc, a click outside, closeOverlayIfTooSmall). A flag
// updated at each of them is the taskCardWasVisible bug again; one derived
// here cannot miss a path. Refreshes on the transition to shown, and on
// every task lifecycle event while shown — event-driven, never a timer,
// and nothing at all while UI-09 is closed.
func (r *Root) refreshContextIfShown(msg tea.Msg) {
	cv, ok := r.overlay.(ContextView)
	shown := ok && cv.IsContextView()
	trigger := shown && !r.contextViewWasShown
	if shown && !trigger {
		if e, isEnv := msg.(protocol.EnvelopeMsg); isEnv {
			t := e.Envelope.Type
			trigger = t == protocol.EvTaskStarted || protocol.IsTerminal(t)
		}
	}
	r.contextViewWasShown = shown
	if !trigger {
		return
	}
	if cr, ok := r.current().(ContextRefresher); ok {
		cr.RefreshContextReport()
	}
}

// closeRightPaneFallbackIfObsolete retires the narrow-width fallback overlay
// the moment a resize widens the terminal back past the breakpoint where
// the real pane fits — the real pane's own visibility is entirely
// layout.Compute's decision and needs no help, but nothing previously
// closed the OVERLAY once the reason it was opened (the real pane not
// fitting) stopped being true, so widening back left both on screen at
// once. closeOverlayIfTooSmall does not cover this: its condition is the
// opposite one, a terminal that shrank BELOW an overlay's own MinSize.
func (r *Root) closeRightPaneFallbackIfObsolete() {
	if r.overlay == nil {
		return
	}
	// Width alone, not r.layout.ShowRightPane: the overlay's whole reason to
	// exist is "the terminal is too narrow for the real pane", which is a
	// width fact independent of whether rightPaneOpen happens to be true —
	// a user who manually closed the pane, then opened this overlay while
	// narrow, still gets it retired on widening, because the narrowness
	// that justified it is gone either way.
	if _, ok := r.overlay.(RightPaneFallback); ok && r.w >= layout.MediumWidth {
		r.overlay = nil
		r.recompute()
	}
}

// handleTaskListEnvelope is EnvelopeMsg's hook into the global task
// snapshot: it claims Root's own ListTasks reply (matched by corr_id, same
// pattern chat\protocol.go's pending map uses) and treats any TaskStarted or
// terminal event — from ANY workspace's send, since routing does not filter
// by origin — as a reason the snapshot might be stale.
func (r *Root) handleTaskListEnvelope(env protocol.Envelope) {
	if env.CorrID != "" && env.CorrID == r.tasksReqID {
		r.tasksReqID = ""
		if r.client != nil {
			r.client.Release(env.CorrID)
		}
		var p struct {
			Tasks []TaskRow `json:"tasks"`
		}
		if err := env.UnmarshalPayload(&p); err == nil {
			r.tasks = p.Tasks
			r.log.Debug("root: task snapshot applied", "corr_id", env.CorrID,
				"count", len(p.Tasks), "right_pane_visible", r.rightPaneVisible(),
				"tray_open", r.trayOpen())
		}
		// F9a: the open tray re-renders against the snapshot that just
		// landed. Inside this branch, not after it -- the rows only
		// changed because this reply changed them.
		r.pruneBackgroundSeen()
		r.refreshTray()
		// F12: app.quit is waiting on exactly this, so UI-14 can list
		// what quitting cancels rather than guess. Before the dirty
		// re-ask below, so the dialog opens on the snapshot that just
		// landed rather than one round trip later.
		r.openQuitConfirm()
		if r.tasksDirty {
			r.tasksDirty = false
			r.refreshTasks()
		}
		return
	}
	if env.Type == protocol.EvTaskStarted {
		r.noteTaskBackground(env)
	}
	if env.Type == protocol.EvTaskStarted || protocol.IsTerminal(env.Type) {
		r.refreshTasks()
		// Also re-render NOW, from the snapshot already in hand, rather
		// than only when the refresh's own reply lands. A terminal event
		// is itself news about a row the tray is displaying, and
		// refreshTasks may not even send (a request already in flight
		// sets tasksDirty instead), so waiting for a reply would leave a
		// just-finished task sitting under "Running" for an extra round
		// trip. r.tasks has not changed yet, so sameTrayRows makes this a
		// no-op whenever it adds nothing -- which is most of the time.
		r.refreshTray()
	}
}

// pushDiagnostics hands the current workspace a fresh snapshot. The workspace
// renders what it is given; it never reaches into the root.
func (r *Root) pushDiagnostics() tea.Cmd {
	// Seq is what makes this message safe under bubbletea's own concurrency:
	// each returned tea.Cmd runs in its own goroutine, with no guarantee
	// that an EARLIER push's goroutine finishes before a LATER push's does.
	// Two pushDiagnostics calls close together (e.g. a TaskDone envelope
	// immediately followed by the ListTasks reply it triggered) can have
	// their DiagnosticsMsg values delivered to Update in either order —
	// confirmed by a field report where the fallback overlay's Task
	// section locked onto a stale snapshot and never let go until the
	// overlay was closed and reopened. Seq lets the RECEIVER (chat.
	// Workspace) discard a message that arrived late but was built early,
	// rather than trusting arrival order.
	r.diagnosticsSeq++
	d := Diagnostics{
		Seq:             r.diagnosticsSeq,
		ConnState:       r.conn.state,
		ProtocolVersion: r.conn.protocolVersion,
		ClientVersion:   protocol.Version,
		Capabilities:    r.conn.capabilities,
		Handshaked:      r.conn.handshaked,
		Restarts:        r.conn.restarts,
		Banner:          r.conn.banner,
		BannerDetail:    r.conn.bannerDetail,
		BackendDir:      r.env.BackendDir,
		LogFile:         r.env.LogFile,
		InFlight:        r.conn.inFlight,
		Unknown:         r.conn.unknown,
		Interrupted:     r.conn.interrupted,
		Tasks:           r.tasks,
		ProtocolErrors:  r.conn.protocolErrors,
		ThemeName:       r.theme.Name(),
		Background:      string(r.theme.Background()),
		NoColor:         r.theme.NoColor(),
		ActionCount:     r.actions.Len(),
		PasteEvents:     r.env.PasteEvents,
		PasteLines:      r.env.PasteLines,
		ConfigPath:      r.env.ConfigPath,
		ConfigError:     r.env.ConfigError,
		StatePath:       r.env.StatePath,
		StateError:      r.env.StateError,
		Notes:           r.env.Notes,
		// F10: nil exactly when nothing has been chosen through Point's own
		// UI yet — see Diagnostics.NoProviderConfigured's own doc for why
		// this is pushed rather than read off chat's own state.json copy.
		NoProviderConfigured: r.activeProvider == nil,
		NoWorkspaceLoaded:    len(r.workspaces) == 0,
	}
	return func() tea.Msg { return DiagnosticsMsg{D: d} }
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

type leaderHintMsg struct{ gen int }
type animTickMsg struct{ gen int }
type statusMsg string

// statusExpireMsg clears a status line that has had its say.
//
// F12 asks for the idle grace warning as "a toast", and there is no toast in
// this codebase -- only r.status, which is PERMANENT until something else
// overwrites it. That is deliberate for most of what it carries, and wrong
// for a countdown: actions.go's pane.right.toggle already records a round of
// debugging spent on a stale status string that read as a compositor bug,
// and "Editor unloads in 30s" left on screen after the Editor unloaded
// would be the same lie. Rather than a new overlay for a one-line
// notice, the status line gains an expiry -- the smallest thing that
// makes the warning honest.
type statusExpireMsg struct{ text string }

// noticeLinger is how long an unload notice stays. Long enough to read a
// sentence, short enough that it is gone before it can be mistaken for the
// current state.
const noticeLinger = 6 * time.Second

// expireStatusIfChanged schedules an expiry when this message left a NEW
// status line on screen. See Update for the whole account.
//
// Only a CHANGE arms it, so a message that leaves the status untouched
// does not keep pushing the expiry of whatever is already up -- otherwise
// a stream of ticks would hold a line on screen indefinitely, which is
// the bug in reverse.
func (r *Root) expireStatusIfChanged(before string) tea.Cmd {
	if r.status == "" || r.status == before {
		return nil
	}
	text := r.status
	return tea.Tick(r.statusLinger(), func(time.Time) tea.Msg {
		return statusExpireMsg{text: text}
	})
}

// statusLinger is StatusLinger, defaulted.
func (r *Root) statusLinger() time.Duration {
	if r.StatusLinger > 0 {
		return r.StatusLinger
	}
	return noticeLinger
}

// expiringStatus shows a status line and schedules its removal.
func (r *Root) expiringStatus(text string, d time.Duration) tea.Cmd {
	r.status = text
	return tea.Tick(d, func(time.Time) tea.Msg {
		return statusExpireMsg{text: text}
	})
}

// ensureStartupChooser opens UI-12 when nothing is loaded and nothing else
// has the overlay slot.
//
// Not in the WindowSizeMsg case, where F10's provider chooser is, even
// though both need a real terminal size before OpenOverlay's own fit check
// can answer honestly: only ONE overlay can be open, and on a genuine first
// run both want that slot at the same instant. Asked after every message
// instead, this opens on the first frame when a provider is already
// configured, and on the frame after the provider chooser closes when one
// is not -- with no ordering rule written down anywhere for a later episode
// to break.
//
// chooserShown latches it so dismissing it with esc leaves the user on the
// diagnostics screen rather than reopening it forever. unloadWorkspace
// clears the latch when it unloads the last loaded workspace, which is the
// other moment there is genuinely nothing to look at.
func (r *Root) ensureStartupChooser() tea.Cmd {
	if r.chooserShown || len(r.defs) == 0 || r.w == 0 || r.h == 0 {
		return nil
	}
	if r.overlay != nil || len(r.workspaces) > 0 {
		return nil
	}
	r.chooserShown = true
	r.OpenOverlay(newChooser(r))
	return nil
}

func (r *Root) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A BARE MODIFIER key-down is not a keystroke. Dropped here, once,
	// before anything can mistake it for typed text.
	//
	// # What arrives, and why
	//
	// bubbletea's Windows console reader (key_windows.go's readConInputs)
	// skips key-UP records and VK_SHIFT, and nothing else. VK_CONTROL,
	// VK_MENU, VK_LWIN and VK_CAPITAL all reach keyType, which has no case
	// for any of them and ends in `return KeyRunes` -- and a modifier
	// record's Char is 0. So pressing Ctrl on its own, BEFORE the chord it
	// belongs to, delivers:
	//
	//	KeyMsg{Type: KeyRunes, Runes: []rune{0}}
	//
	// which every consumer of this app reads as "the user typed a
	// character".
	//
	// # What it cost, measured
	//
	// All three of these were reported as separate editor bugs and are one
	// NUL rune:
	//
	//   - a selection collapsed the instant Ctrl went down, before `v`
	//     arrived: the NUL was spliced over it (record removed="hello
	//     world" inserted="\x00");
	//   - ctrl+z took two presses to undo one real edit: the NUL had
	//     pushed an undo record of its own, so the first press undid the
	//     invisible character and the second undid the edit. Redo was
	//     cleared outright by the same push;
	//   - the caret vanished and "skipped" characters: runewidth gives NUL
	//     zero cells, so the caret drawn on one is a zero-width glyph, and
	//     two different cursor columns map to the same display cell -- a
	//     right-arrow that moves the caret and not the cursor.
	//
	// # Why here and not in each consumer
	//
	// Because it is an artefact of this platform's input layer, and this
	// is the one function every key passes through. paste.go's pasteable()
	// already carries `k.Runes[0] != 0` for exactly this reason -- F1 or
	// F4 hit the same record and filtered it locally, on the paste path
	// only. chat's query input has the same hazard through
	// handleInputKey's rune insert and was one Ctrl press away from
	// inserting a NUL into a draft. One filter, both workspaces, and any
	// workspace added later.
	//
	// Only an ALL-NUL rune slice is dropped. A KeyRunes carrying real text
	// alongside a NUL is not something the reader can produce (it writes
	// exactly one rune per record), and a legitimate NUL is not something
	// a keyboard can produce either -- but checking every rune rather than
	// just the first means a future reader that batches runes cannot
	// reintroduce this quietly.
	if isBareModifier(k) {
		return r, nil
	}

	// A multi-line paste is ONE event, never N key events.
	//
	// Two routes reach that. Where the terminal brackets its pastes, the
	// KeyMsg arrives with Paste set and that is the whole of it. On Windows it
	// never does — see paste.go for why — so a burst detector reconstructs it.
	// Both routes end in the same PasteMsg.
	if k.Paste {
		return r, func() tea.Msg { return PasteMsg{Text: string(k.Runes)} }
	}

	if pasteable(k) && r.overlay == nil && !r.leader.pending {
		if cmd, consumed := r.handlePasteable(k); consumed {
			return r, cmd
		}
	}

	// Any other key ends a burst at once rather than waiting it out.
	var pending tea.Cmd
	switch {
	case r.paste.active:
		pending = r.flushPaste()
	case r.paste.held != nil:
		pending = r.releaseHeldKey()
	}
	if pending != nil {
		model, cmd := r.dispatchKey(k)
		return model, tea.Batch(pending, cmd)
	}
	return r.dispatchKey(k)
}

// dispatchKey is the ordinary key path, with paste already accounted for.
func (r *Root) dispatchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()

	// An overlay traps focus while it is open. Every key — esc included —
	// goes to the overlay itself now, not just handled generically here: F7's
	// Popup needs a FIRST esc to clear its filter and only a SECOND to
	// close, which an unconditional "esc always closes" (the old behaviour)
	// never gives it a chance to do. An overlay asks to close by returning a
	// nil Overlay from Update, exactly like any other state change.
	if r.overlay != nil {
		o, cmd := r.overlay.Update(k)
		r.setOverlay(o)
		return r, cmd
	}

	if r.leader.pending {
		return r.handleLeaderKey(key)
	}

	// EITHER leader key starts a chord -- see actions.Leader's own doc on
	// why there are two and why neither is a fallback.
	if actions.IsLeader(key) {
		r.leader.pending = true
		r.leader.sequence = ""
		r.leader.showHint = false
		r.leader.gen++
		gen := r.leader.gen
		return r, tea.Tick(leaderHintDelay, func(time.Time) tea.Msg {
			return leaderHintMsg{gen: gen}
		})
	}

	// F13: the ACTIVE workspace may own a key the chord table has already
	// claimed. Asked HERE, after the leader (so leader → anything stays
	// reachable from every workspace) and before the chord lookup (so a
	// claim actually wins), through KeyCapturer — see that interface for
	// why `tab` forced this and why moving pane.focus.next was rejected.
	if kc, ok := r.current().(KeyCapturer); ok && kc.CapturesKey(key) {
		return r.forwardKey(k)
	}

	if a, ok := r.actions.ByChord(key); ok {
		if !a.Available(r) {
			r.status = a.Name + " is not available right now"
			return r, nil
		}
		return r, a.Run(r)
	}

	return r.forwardKey(k)
}

// forwardKey hands a key to the active workspace.
//
// Only a key the workspace actually RECEIVES can have moved its position,
// so the blob is re-armed here rather than in Update's KeyMsg case. A chord
// an action consumed (ctrl+c, ctrl+q) scrolls nothing, and arming on it
// would also wrap every such key's command in a tea.Batch for no reason --
// which is how this was first written and what TestCtrlCDoesNotQuit caught.
func (r *Root) forwardKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	save := r.schedulePositionSave()
	ws, cmd := r.current().Update(k)
	r.storeCurrent(ws)
	if save == nil {
		return r, cmd
	}
	return r, tea.Batch(cmd, save)
}

func (r *Root) handleLeaderKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" || actions.IsLeader(key) {
		r.clearLeader()
		return r, nil
	}
	if len(key) != 1 {
		r.clearLeader()
		r.status = "not a leader key: " + key
		return r, nil
	}

	seq := r.leader.sequence + key

	if a, ok := r.actions.ByLeader(seq); ok {
		r.clearLeader()
		if !a.Available(r) {
			r.status = a.Name + " is not available right now"
			return r, nil
		}
		return r, a.Run(r)
	}
	if r.actions.LeaderPrefix(seq) {
		r.leader.sequence = seq
		return r, nil
	}

	r.clearLeader()
	r.status = fmt.Sprintf("no action bound to %s %s", actions.LeaderLabel, seq)
	return r, nil
}

// deliverKey hands one key to the current workspace — used for a key the paste
// detector held and then decided was an ordinary keystroke.
// deliverKey replays a key the burst detector held or reassembled.
//
// It goes through dispatchKey, NOT straight to the workspace, and that is a
// correction: it used to call r.current().Update(k) directly, which skipped
// the whole dispatch chain -- the overlay trap, the leader state, the chord
// table and KeyCapturer.
//
// A replayed key must take the same path as a live one. The detector holds
// EVERY pasteable key for 6ms (pasteBurstGap) before letting it through, so
// a key pressed a moment before an overlay opened, or before a leader chord
// began, was delivered into the buffer afterwards as though neither had
// happened. That window is small and the conditions are exactly the ones
// F15's gate hit: a busy Update loop during a ghost stream, and a workspace
// load.
//
// This is NOT the chord leak the gate's cause log recorded (see
// editor\keys.go's note on modifier-dropped chords -- that one happens
// inside bubbletea's Windows key reader, before Point sees the event). It
// is a second, independent way for a keystroke to arrive somewhere it was
// not aimed, found while looking for the first.
func (r *Root) deliverKey(k tea.KeyMsg) tea.Cmd {
	save := r.schedulePositionSave()
	_, cmd := r.dispatchKey(k)
	return tea.Batch(cmd, save)
}

func (r *Root) clearLeader() {
	r.leader.pending = false
	r.leader.sequence = ""
	r.leader.showHint = false
}

// ---------------------------------------------------------------------------
// Layout and overlays
// ---------------------------------------------------------------------------

func (r *Root) recompute() {
	r.layout = layout.Compute(r.w, r.h, layout.State{
		Active:        r.active,
		RightPaneOpen: r.rightPaneOpen,
		HeaderAllowed: true,
		OverlayOpen:   r.overlay != nil,
		TerminalMode:  terminalModeOf(r.current()),
		RightCards:    r.current().Cards(),
	})
}

// terminalModeOf reads F14's TerminalModeAware off the active workspace,
// falling back to TermSmall for every workspace that is not the Editor --
// the same fallback recompute hardcoded before F14 existed.
func terminalModeOf(ws Workspace) layout.TermMode {
	if tm, ok := ws.(TerminalModeAware); ok {
		return tm.TerminalMode()
	}
	return layout.TermSmall
}

// overlayFits is the ONE fit check for an overlay against the current
// terminal — used both to refuse opening one (OpenOverlay) and to close one
// already open that a subsequent resize has outgrown
// (closeOverlayIfTooSmall). Before F7's verification, only the first of
// those existed: an overlay that fit when it opened kept shrinking as the
// terminal shrank, with no floor, because nothing ever checked again.
func (r *Root) overlayFits(o Overlay) (fits bool, refusal string) {
	w, h := o.MinSize()
	if !r.layout.OverlaysAllowed {
		return false, fmt.Sprintf("%s needs %d columns — overlays are off below %d",
			o.Title(), w, layout.OverlayFloor)
	}
	if r.w < w || r.h < h {
		return false, fmt.Sprintf("%s needs %d×%d — terminal is %d×%d",
			o.Title(), w, h, r.w, r.h)
	}
	return true, ""
}

// OpenOverlay refuses one that does not fit and shows the ACTUAL numbers.
// recompute only runs when the overlay actually changed — a refusal leaves
// r.overlay (and therefore the layout it feeds) exactly as it was, same as
// before setOverlay existed.
func (r *Root) OpenOverlay(o Overlay) {
	if r.setOverlay(o) {
		r.recompute()
	}
}

// setOverlay applies whatever an Overlay's own Update returned in place of
// the one that was open, or a fresh overlay from OpenOverlay -- the ONE
// place r.overlay is ever assigned. Reports whether it actually changed.
// nil always closes. Anything else is choosing to open a (possibly
// different) overlay -- rightPaneOverlay swapping itself for UI-09's
// composition card (S13d) does exactly this on enter or a click on its own
// embedded Status section -- and must clear the same MinSize floor a fresh
// OpenOverlay already enforces. Before this, dispatchKey and dispatchMouse
// both assigned whatever Update returned unconditionally, which was never a
// problem while the only non-nil, non-self value any Overlay ever returned
// was... none — every Overlay in this codebase returned only itself or nil
// until S13d's swap. An Overlay's own View() cannot check this itself: it
// only ever receives its OWN rendered box size (overlayRect never grows an
// overlay past its MinSize), never the real terminal dimensions, so a
// self-returned swap without this check could render an overlay larger
// than the terminal actually has room for.
func (r *Root) setOverlay(next Overlay) bool {
	if next == nil {
		if r.overlay == nil {
			return false
		}
		r.overlay = nil
		return true
	}
	if next == r.overlay {
		return false // unchanged -- most Update calls hit this path
	}
	if ok, msg := r.overlayFits(next); !ok {
		r.status = msg
		return false // keep whatever is already open; do not clip, do not close
	}
	r.overlay = next
	return true
}

// closeOverlayIfTooSmall re-runs the SAME fit check on an overlay that is
// already open. Called after every resize: an overlay holds its own
// MinSize exactly for as long as the terminal has room for it, and closes —
// naming the same numbers OpenOverlay would have refused with — the moment
// it does not, rather than continuing to shrink through its own floor.
// overlayRect's own clamping (render.go's withOverlay) never actually needs
// to produce a rect smaller than MinSize once this runs first; it is kept
// as the defence for any caller that resizes without going through this
// path.
func (r *Root) closeOverlayIfTooSmall() {
	if r.overlay == nil {
		return
	}
	if ok, msg := r.overlayFits(r.overlay); !ok {
		r.status = msg
		r.overlay = nil
	}
}

// overlayRect is the on-screen rect the open overlay is drawn into — the
// SAME formula withOverlay (render.go) uses to composite it, so a mouse
// click and the panel it looks like it hit are never computed two different
// ways. Only valid while r.overlay != nil.
func (r *Root) overlayRect() layout.Rect {
	minW, minH := r.overlay.MinSize()

	w := minW
	if max := r.w - 8; w > max && max > 10 {
		w = max
	}
	h := minH
	if max := r.h - 4; h > max && max > 4 {
		h = max
	}

	// A MaximisedOverlay may ask for more than its MinSize. What it is
	// OFFERED is the transcript pane's own rect (r.layout.Main) less one
	// cell per side -- not the terminal's. See MaximisedOverlay for why:
	// Main already has the right pane's columns and the header/status/query
	// rows subtracted by Compute, so an overlay sized to it is the size of
	// the thing the reader is already reading, and it tracks pane
	// proportions with no fraction-of-the-screen constant to drift.
	//
	// Read off r.layout rather than threaded in: overlayRect is a *Root
	// method and Compute's answer is already stored there. View recomputes
	// it whenever the terminal's dimensions have changed before using it,
	// and recompute() runs on every WindowSizeMsg and on the right-pane
	// toggle -- so the rect this reads is the one the current frame was
	// laid out with, which is also what keeps dispatchMouse's hit-test and
	// withOverlay's composite agreeing (the whole reason they share this
	// function).
	if mo, ok := r.overlay.(MaximisedOverlay); ok {
		availW, availH := r.layout.Main.W-2, r.layout.Main.H-2
		pw, ph := mo.PreferredSize(availW, availH)
		// Capped against the TERMINAL as well, not just against what was
		// offered: PreferredSize may return anything, and a panel wider
		// than the screen is the one failure overlayFits exists to
		// prevent. Main is always inside the terminal, so for an overlay
		// that simply accepts the offer this cap never fires -- it is
		// here for the one that does not.
		if maxW := r.w - 2; pw > maxW {
			pw = maxW
		}
		if maxH := r.h - 2; ph > maxH {
			ph = maxH
		}
		if pw > w {
			w = pw
		}
		if ph > h {
			h = ph
		}
	}
	// Centred in the whole TERMINAL, not over Main: the right pane and the
	// status bar stay visible around the panel under the scrim. Size comes
	// from the pane, position from the screen.
	return layout.Rect{X: (r.w - w) / 2, Y: (r.h - h) / 2, W: w, H: h}
}

// dispatchMouse routes a mouse event to the open overlay, or to the active
// workspace when nothing is open.
//
// Coordinates handed to the overlay are translated to its own content
// origin — one cell in from each border edge, matching box()'s own layout —
// so an overlay never has to know where on the real screen it was drawn. A
// LEFT CLICK outside the panel closes it: LAYOUT.md's "close on esc or a
// click outside", which nothing implemented before F7 gave the overlay
// contract anything to click on at all.
//
// Only a left click qualifies — bubbletea reports a wheel tick as
// Action == MouseActionPress too (mouse.go's decode never sets any other
// Action for a wheel event), and the mouse cursor is not required to be
// hovering the panel to scroll it. Closing on ANY outside press meant the
// popup closed itself out from under a scroll the instant the pointer
// happened to be elsewhere on screen — indistinguishable, from the user's
// seat, from "the popup ignored my click": it was already gone.
func (r *Root) dispatchMouse(m tea.MouseMsg) (tea.Model, tea.Cmd) {
	if r.overlay == nil {
		// S13d: a left-click-press may target something the active
		// workspace wants to open as an overlay -- chat.Workspace's Status
		// card, expanding into the context composition card. Asked through
		// MouseOverlayTarget, the same narrow-interface shape as
		// OverlayProvider/FocusAware, so root decides WHETHER to open
		// (OpenOverlay's fit check) while the workspace decides WHAT
		// on-screen rect it owns and whether it has real data to show
		// (chat's own ContextExpandOverlay guard). Root never reads the
		// workspace's rect or report fields directly.
		if m.Button == tea.MouseButtonLeft && m.Action == tea.MouseActionPress {
			if mo, ok := r.current().(MouseOverlayTarget); ok {
				if o, hit := mo.OverlayForClick(m.X, m.Y); hit {
					r.OpenOverlay(o)
					return r, nil
				}
			}
		}
		// The wheel scrolls the transcript, which moves the position just
		// as j/k do -- see schedulePositionSave.
		save := r.schedulePositionSave()
		ws, cmd := r.current().Update(m)
		r.storeCurrent(ws)
		if save == nil {
			return r, cmd
		}
		return r, tea.Batch(cmd, save)
	}

	rect := r.overlayRect()
	inside := m.X >= rect.X && m.X < rect.X+rect.W && m.Y >= rect.Y && m.Y < rect.Y+rect.H

	if !inside {
		if m.Button == tea.MouseButtonLeft && m.Action == tea.MouseActionPress {
			r.overlay = nil
		}
		return r, nil
	}

	m.X -= rect.X + 1
	m.Y -= rect.Y + 1
	o, cmd := r.overlay.Update(m)
	r.setOverlay(o)
	return r, cmd
}

func (r *Root) retryHint() string {
	if a, ok := r.actions.ByID("backend.retry"); ok && a.Leader != "" {
		return fmt.Sprintf("%s %s to retry", actions.LeaderLabel, a.Leader)
	}
	return "retry from the palette"
}

// deliverBackgroundReply hands one envelope to every LOADED, INACTIVE
// workspace that says it is waiting on a reply.
//
// See app.BackgroundReplies for why this exists at all: an autosave write
// started a moment before a workspace switch has its reply land while
// something else is active, and the Editor cannot recover from losing it.
//
// Three things about the shape, each deliberate:
//
//   - It runs AFTER r.current().Update, so the active workspace keeps
//     first claim on an envelope and ordering between the two is fixed
//     rather than map-iteration order.
//
//   - A workspace matches replies by its own corr_id (editor\protocol.go's
//     w.pending), so an envelope that is not its own is a map miss and
//     costs one lookup. Nothing here has to know which envelopes belong to
//     whom, which is the same division F2 already draws.
//
//   - The returned tea.Cmd is FILTERED, default-deny, through
//     BackgroundCmdFilter (contribute.go). It used to be dropped outright,
//     and the reasoning for that is still why the default is deny: the one
//     thing a write reply produces is say(), which becomes a
//     WorkspaceStatusMsg -- and that message carries no workspace id, so
//     Root would print "saved main.go" over the status bar of whatever the
//     user is actually looking at. A background workspace may reconcile
//     its own state; it may not take the status line.
//
//     What F15 found is that "drop everything" is too wide by exactly two
//     messages: the command that OPENS a finished fill's diff card, and
//     the timer that decides a fill produced no proposal. Both were
//     thrown away, so a fill that completed while the user was in Chat
//     left a frozen ghost and no card. say() is still dropped -- it is
//     not on any allow-list -- so nothing about the status-line rule
//     changed.
//
// Nothing is delivered to the active workspace twice: r.active is skipped
// explicitly.
func (r *Root) deliverBackgroundReply(m protocol.EnvelopeMsg) tea.Cmd {
	var cmds []tea.Cmd
	for id, ws := range r.workspaces {
		if id == r.active {
			continue
		}
		br, ok := ws.(BackgroundReplies)
		if !ok || !br.WantsBackgroundReplies() {
			continue
		}
		updated, cmd := ws.Update(m)
		r.workspaces[id] = updated
		if c := r.backgroundCmd(id, cmd); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// backgroundCmd wraps one command an INACTIVE workspace returned so that
// whatever it produces is judged on arrival.
//
// Judged on arrival rather than before being handed over, because a
// tea.Cmd is a function and the only way to see its message is to run it --
// and running a tea.Tick here would block Update for the length of the
// timer. So the command is passed to the runtime as normal, in its own
// goroutine, and the verdict is applied to the message it eventually
// yields. A denied message becomes nil, which is what every tea.Cmd with
// nothing to say returns.
//
// The filter is captured rather than re-looked-up, and that is safe: it is
// a pure predicate over a message type, so a workspace unloaded between the
// arming and the firing still answers the same question. WHERE the message
// is delivered is re-resolved from the map at delivery time instead (see
// the backgroundWorkspaceMsg case in Update), so a message addressed to a
// workspace that has since gone away is dropped rather than reviving it.
//
// A tea.BatchMsg is denied like anything else not on the list. It cannot be
// filtered element-wise without running its parts, and nothing on the
// allow-list is produced inside a batch today -- stated because a future
// allow-listed message wrapped in a tea.Batch would silently stop arriving,
// and this is where to look.
func (r *Root) backgroundCmd(id WorkspaceID, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	filter, ok := r.workspaces[id].(BackgroundCmdFilter)
	if !ok {
		// Default deny: exactly the behaviour every workspace had before
		// this seam existed.
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		switch filter.AllowBackgroundMsg(msg) {
		case DeliverToRoot:
			return msg
		case DeliverToWorkspace:
			return backgroundWorkspaceMsg{id: id, msg: msg}
		}
		return nil
	}
}
