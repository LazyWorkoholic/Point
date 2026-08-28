package chat

import (
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Options builds a Workspace.
type Options struct {
	Theme  theme.Theme
	Client *protocol.Client
	Log    *slog.Logger

	// Scrollback bounds the render cache — see cache.go. <= 0 falls back to
	// 500, matching config.Defaults().UI.Scrollback.
	Scrollback int
}

// Workspace is F3's Chat workspace: the transcript. Nothing here builds the
// input (F4) or streaming (F5) — see F03-chat-viewport.md's "Do NOT".
//
// It returns app.Workspace from Update, which is why this package imports
// app rather than the other way around: app cannot import Workspace
// implementations without a cycle, since those implementations have to name
// app.Workspace as their own return type. main.go imports both and wires
// them together with Root.SetWorkspace.
type Workspace struct {
	theme  theme.Theme
	client *protocol.Client
	log    *slog.Logger

	scrollback int

	sessionID    string
	sessionTitle string
	bootstrapped bool
	loading      bool
	loadErr      string

	pending map[string]queryKind
	// sendFn defaults to doSend; a test substitutes a recorder so it can
	// exercise the ListSessions/GetSession/CreateSession decision logic
	// without a live protocol.Client — see protocol.go's send doc.
	sendFn func(cmdType string, kind queryKind, payload map[string]any)

	all []wireMessage // every message GetSession returned, oldest first

	focus  int  // absolute index into all; -1 means "no explicit focus"
	pinned bool // true: viewport tracks the newest message
	topAbs int  // absolute index of the first message rendered, when not pinned

	copyNote string // last y-copy result, shown briefly at the foot of the viewport

	cacheWidth int
	cache      map[string][]string
	cacheOrder []string
}

// New builds a Chat workspace. It does nothing over the wire yet — Init and
// the first ConnectedMsg decide when a query can actually be sent, since
// Client.Send fails until the handshake has completed.
func New(opts Options) *Workspace {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	sb := opts.Scrollback
	if sb <= 0 {
		sb = 500
	}
	w := &Workspace{
		theme:      opts.Theme,
		client:     opts.Client,
		log:        opts.Log,
		scrollback: sb,
		pending:    make(map[string]queryKind),
		cache:      make(map[string][]string),
		focus:      -1,
		pinned:     true,
	}
	w.sendFn = w.doSend
	return w
}

func (w *Workspace) ID() layout.WorkspaceID { return layout.WorkspaceChat }

// Cards is empty: F3 does not build the right pane. An open right pane needs
// BOTH RightPaneOpen and a non-empty RightCards (layout.Compute's rule), so
// returning nil here is what keeps it hidden until F6.
func (w *Workspace) Cards() []layout.CardSlot { return nil }

// Init has nothing to do at Bubble Tea's Init time: Update watches for
// protocol.ConnectedMsg and starts the ListSessions/GetSession/CreateSession
// sequence from there. If the client is already connected by the time this
// workspace is installed (SetWorkspace runs after New but Init could in
// principle run after a fast handshake), Update's ConnectedMsg case still
// fires once — Root always sends one after the handshake completes.
func (w *Workspace) Init() tea.Cmd { return nil }

// Suspend/Resume persist roughly two kilobytes per F1's Workspace doc — never
// message content, the same rule Resume documents: rehydrate from the
// database, the same path as opening a week-old session. F3 has nowhere yet
// to write these (F12 owns workspace-state persistence), so both are no-ops
// that satisfy the interface without inventing storage this episode does not
// own.
func (w *Workspace) Suspend() ([]byte, error) { return nil, nil }
func (w *Workspace) Resume([]byte) error      { return nil }

var _ app.Workspace = (*Workspace)(nil)
