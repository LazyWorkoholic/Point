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

	// StreamTimeout is F5's watchdog: how long a running task may say
	// nothing before the UI stops its spinner and offers Retry. <= 0 falls
	// back to defaultStreamTimeout (120s), matching
	// config.Defaults().UI.StreamTimeout — a workspace built without it
	// still has a watchdog rather than silently having none.
	StreamTimeout time.Duration

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
	// sendMsgFn defaults to writeSend — F5's CmdSendMessage write, which is
	// a different shape from sendFn's one-shot queries (one request, a
	// whole stream of replies). Substituted by a test for the same reason
	// sendFn is: what these tests exercise is the lane/queue/lifecycle
	// logic, not protocol.Client's send path, which that package tests.
	sendMsgFn func(e *liveEntry, payload map[string]any) tea.Cmd

	input inputState

	// state and saveStateFn back draft persistence (draft.go). saveStateFn
	// defaults to config.SaveState; a test substitutes a recorder for the
	// same reason sendFn does — see draft.go's persistDraft doc.
	state       config.State
	saveStateFn func(config.State) error

	// all is every message GetSession returned, oldest first, rendered
	// verbatim in that order.
	//
	// F5 briefly folded adjacent identical user messages here, to hide the
	// duplicate question a retry leaves behind (a retry re-sends, and the
	// backend persists a user message per send). That fold was WRONG and is
	// gone. Adjacency does not identify a retry: two successful task-lane
	// sends of the same question persist their questions adjacently while
	// their answers arrive afterwards, so the database legitimately holds
	// Q · Q · A · A — and the fold merged the two questions and orphaned an
	// answer under the survivor, inventing a "retried" label for a send
	// that was never retried. Corrupting a correct transcript is strictly
	// worse than showing the duplicate it was hiding.
	//
	// It cannot be done correctly yet, either. A question and its answer
	// have NO link in the database: agents\executor.py builds its
	// UserMessage before a task_id exists and never assigns one, so only
	// the assistant side carries task_id. Without that link there is no way
	// to pair an answer to its question, let alone to tell a retry from a
	// repeat. B12 is where that data can appear — see this episode's
	// handoff.
	all []wireMessage

	focus  int  // absolute entry index; -1 means "no explicit focus"
	pinned bool // true: viewport tracks the live bottom

	// The scroll position is a ROW, addressed as (entry, row within that
	// entry's block) — see scroll.go's doc comment on why it is a pair and
	// not one absolute row number.
	topAbs int
	topRow int

	// viewRows and viewWidth are the viewport's content dimensions from the
	// most recent render (render.go's viewportLines). The key handlers need
	// them — scrolling a row and paging a screenful are both measured
	// against the box that is actually on screen — and a keypress does not
	// otherwise know how big that box is.
	viewRows  int
	viewWidth int

	copyNote string // last y-copy result, shown briefly at the foot of the viewport

	// F5 — see stream.go. live holds one entry per send made this session,
	// in send order; queued is the depth-1 queue; the tick fields pace the
	// render throttle and the no-response watchdog.
	live          []*liveEntry
	queued        *queuedSend
	streamTimeout time.Duration
	tickArmed     bool
	tickGen       int

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
	st := opts.StreamTimeout
	if st <= 0 {
		st = defaultStreamTimeout
	}
	w := &Workspace{
		theme:         opts.Theme,
		client:        opts.Client,
		log:           opts.Log,
		scrollback:    sb,
		streamTimeout: st,
		pending:       make(map[string]queryKind),
		cache:         make(map[string][]string),
		focus:         -1,
		pinned:        true,
		state:         opts.State,
	}
	w.sendFn = w.doSend
	w.sendMsgFn = w.writeSend
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
