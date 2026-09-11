package app

import (
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

	// lastFrame is the most recently rendered screen, re-served while a
	// paste burst is assembling — see View's doc comment for why that is a
	// correctness fix and not just a speed one.
	lastFrame string

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
		active:     active,
		workspaces: make(map[WorkspaceID]Workspace),
		suspended:  make(map[WorkspaceID][]byte),
		theme:      opts.Theme,
		client:     opts.Client,
		log:        opts.Log,
		env:        opts.Env,
		conn:       connection{state: protocol.StateStarting},
	}

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
	if anim := r.ensureAnimation(); anim != nil {
		return model, tea.Batch(cmd, anim)
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
		return r, nil

	case tea.KeyMsg:
		return r.handleKey(m)

	case tea.MouseMsg:
		return r.dispatchMouse(m)

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
		return r, tea.Batch(r.afterBackend(), wsCmd)

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
		ws, wsCmd := r.current().Update(m)
		r.workspaces[r.active] = ws
		return r, tea.Batch(r.afterBackend(), wsCmd)

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

// pushDiagnostics hands the current workspace a fresh snapshot. The workspace
// renders what it is given; it never reaches into the root.
func (r *Root) pushDiagnostics() tea.Cmd {
	d := Diagnostics{
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
	}
	return func() tea.Msg { return diagnosticsMsg{d: d} }
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
		r.overlay = o
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
		RightPaneOpen: true,
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
func (r *Root) OpenOverlay(o Overlay) {
	if ok, msg := r.overlayFits(o); !ok {
		r.status = msg
		return
	}
	r.overlay = o
	r.recompute()
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
	r.overlay = o
	return r, cmd
}

func (r *Root) retryHint() string {
	if a, ok := r.actions.ByID("backend.retry"); ok && a.Leader != "" {
		return fmt.Sprintf("%s %s to retry", actions.Leader, a.Leader)
	}
	return "retry from the palette"
}
