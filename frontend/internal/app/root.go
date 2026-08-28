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

	leader   leaderState
	paste    pasteState
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

func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {

	case tea.WindowSizeMsg:
		r.w, r.h = m.Width, m.Height
		r.recompute()
		return r, nil

	case tea.KeyMsg:
		return r.handleKey(m)

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
		if m.gen != r.paste.gen {
			return r, nil // a later key superseded this burst
		}
		return r, r.flushPaste()

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
		return r, r.afterBackend()

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

	// An overlay traps focus while it is open.
	if r.overlay != nil {
		if key == "esc" {
			r.overlay = nil
			return r, nil
		}
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

// OpenOverlay refuses one that does not fit and shows the ACTUAL numbers.
func (r *Root) OpenOverlay(o Overlay) {
	w, h := o.MinSize()

	if !r.layout.OverlaysAllowed {
		r.status = fmt.Sprintf("%s needs %d columns — overlays are off below %d",
			o.Title(), w, layout.OverlayFloor)
		return
	}
	if r.w < w || r.h < h {
		r.status = fmt.Sprintf("%s needs %d×%d — terminal is %d×%d",
			o.Title(), w, h, r.w, r.h)
		return
	}
	r.overlay = o
	r.recompute()
}

func (r *Root) retryHint() string {
	if a, ok := r.actions.ByID("backend.retry"); ok && a.Leader != "" {
		return fmt.Sprintf("%s %s to retry", actions.Leader, a.Leader)
	}
	return "retry from the palette"
}
