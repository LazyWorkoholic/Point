package chat

import (
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
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

	// State is F1's already-loaded state.json (main.go loads it once for
	// app.Options; this is the same value). F4 reads Drafts out of it on
	// session load and writes it back, debounced, as the buffer changes —
	// see draft.go. Everything else in it (LastWorkspace) round-trips
	// unchanged: this package never touches those fields.
	State config.State
}

// inputState is F4's query input: the editing model (input.go/
// input_wrap.go), the mode chip, attachment chips (F8 populates; always
// empty here), focus, and the bookkeeping render.go's growth calculation
// and draft.go's debounce both need.
type inputState struct {
	buf   inputBuffer
	chips []AttachmentChip

	// focused: true while the input holds keyboard focus (composing).
	// false: F3's j/k/y/G browse the transcript instead — see
	// input_keys.go's enterBrowseMode/enterInputMode and app\focus.go.
	focused bool

	// scrollTop is the first wrapped VISUAL line shown, in the box's own
	// coordinate space — only relevant once growth has hit its cap and the
	// input has gone internally scrollable (render.go).
	scrollTop int

	// textWidth and boxContentWidth are cached from the most recent render
	// (render.go's splitGrowth) so handleInputKey's Up/Down/Home/End
	// (input_keys.go) and queryLines' own render both use the exact same
	// numbers splitGrowth's growth calculation used. textWidth already has
	// the mode chip's prefix subtracted (what cursor movement wraps
	// against); boxContentWidth has not (what renderInputLines takes, since
	// it does that subtraction itself) — passing the wrong one double- or
	// zero-subtracts the prefix and the two would silently disagree about
	// where the cursor is.
	textWidth       int
	boxContentWidth int

	draftGen int // draft.go's save-debounce generation counter

	// lastPasteAt is when the most recent PasteMsg landed. input_keys.go's
	// Enter case reads it so a paste's own trailing newline can never
	// reach Send -- see pasteSettle there.
	lastPasteAt time.Time
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

	input inputState

	// state and saveStateFn back draft persistence (draft.go). saveStateFn
	// defaults to config.SaveState; a test substitutes a recorder for the
	// same reason sendFn does — see draft.go's persistDraft doc.
	state       config.State
	saveStateFn func(config.State) error

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
		state:      opts.State,
	}
	w.sendFn = w.doSend
	w.saveStateFn = config.SaveState
	w.input.buf = newInputBuffer()
	w.input.focused = true // the input holds focus by default — F4
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
var _ app.FocusAware = (*Workspace)(nil)
