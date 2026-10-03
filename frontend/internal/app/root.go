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
	active     WorkspaceID
	workspaces map[WorkspaceID]Workspace
	suspended  map[WorkspaceID][]byte
	overlay    Overlay

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

	// MouseEnabled is the mouse option main.go actually gave
	// tea.NewProgram (state.json's UI override already applied there) --
	// see Root.mouseEnabled's own doc for why this package cannot ask
	// Bubble Tea for it instead.
	MouseEnabled bool
}

// New builds the root and validates the action registry.
//
// A duplicate ID or binding is a startup failure with both offenders named,
// not a keypress that silently does the wrong thing six weeks later.
func New(opts Options) (*Root, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	active := WorkspaceChat
	if opts.State.LastWorkspace != "" {
		active = WorkspaceID(opts.State.LastWorkspace)
	}

	r := &Root{
		active:        active,
		workspaces:    make(map[WorkspaceID]Workspace),
		suspended:     make(map[WorkspaceID][]byte),
		theme:         opts.Theme,
		client:        opts.Client,
		log:           opts.Log,
		env:           opts.Env,
		conn:          connection{state: protocol.StateStarting},
		rightPaneOpen: true,
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

	r.actions = registerActions()
	if err := r.actions.Validate(); err != nil {
		return nil, err
	}

	// One workspace, absent from the map until it is loaded. A key that is not
	// there is NOT_LOADED; there is no separate enum for it.
	r.workspaces[active] = NewPlaceholder(active, opts.Theme)

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

func (r *Root) current() Workspace {
	if ws, ok := r.workspaces[r.active]; ok {
		return ws
	}
	ws := NewPlaceholder(r.active, r.theme)
	r.workspaces[r.active] = ws
	return ws
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
		return r.handleKey(m)

	case tea.MouseMsg:
		return r.dispatchMouse(m)

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

	case leaderHintMsg:
		if r.leader.pending && m.gen == r.leader.gen {
			r.leader.showHint = true
		}
		return r, nil

	case statusMsg:
		r.status = string(m)
		return r, nil

	case PasteMsg:
		r.recordPaste(m)
		// Forwarded to the workspace too: the burst detector this message
		// comes from (paste.go) cannot tell a genuine paste from a HELD
		// hotkey whose OS auto-repeat got batched the same way a real
		// paste is — F3's Chat workspace recovers that case for its own
		// single-letter keys. See chat\scroll.go's handlePastedBurst.
		ws, wsCmd := r.current().Update(m)
		r.workspaces[r.active] = ws
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
		r.workspaces[r.active] = ws
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
		r.workspaces[r.active] = ws
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
		r.workspaces[r.active] = ws
		return r, tea.Batch(r.afterBackend(), wsCmd, pickerCmd, settingsCmd)

	case openOverlayMsg:
		// tray.go's OnSelect cannot assign r.overlay itself: it runs inside
		// the Popup's own Update, and setOverlay is "the ONE place
		// r.overlay is ever assigned". So it asks, here.
		r.OpenOverlay(m.o)
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

	ws, cmd := r.current().Update(msg)
	r.workspaces[r.active] = ws
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
	}
	return func() tea.Msg { return DiagnosticsMsg{D: d} }
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

type leaderHintMsg struct{ gen int }
type animTickMsg struct{ gen int }
type statusMsg string

func (r *Root) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	if key == actions.Leader {
		r.leader.pending = true
		r.leader.sequence = ""
		r.leader.showHint = false
		r.leader.gen++
		gen := r.leader.gen
		return r, tea.Tick(leaderHintDelay, func(time.Time) tea.Msg {
			return leaderHintMsg{gen: gen}
		})
	}

	if a, ok := r.actions.ByChord(key); ok {
		if !a.Available(r) {
			r.status = a.Name + " is not available right now"
			return r, nil
		}
		return r, a.Run(r)
	}

	ws, cmd := r.current().Update(k)
	r.workspaces[r.active] = ws
	return r, cmd
}

func (r *Root) handleLeaderKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" || key == actions.Leader {
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
	r.status = fmt.Sprintf("no action bound to %s %s", actions.Leader, seq)
	return r, nil
}

// deliverKey hands one key to the current workspace — used for a key the paste
// detector held and then decided was an ordinary keystroke.
func (r *Root) deliverKey(k tea.KeyMsg) tea.Cmd {
	ws, cmd := r.current().Update(k)
	r.workspaces[r.active] = ws
	return cmd
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
		TerminalMode:  layout.TermSmall,
		RightCards:    r.current().Cards(),
	})
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
		ws, cmd := r.current().Update(m)
		r.workspaces[r.active] = ws
		return r, cmd
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
		return fmt.Sprintf("%s %s to retry", actions.Leader, a.Leader)
	}
	return "retry from the palette"
}
