package chat

import (
	"encoding/json"
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

	// Scrollback bounds the RESIDENT WINDOW (history.go's historyBound) and,
	// through it, the render cache — see cache.go. <= 0 falls back to 500,
	// matching config.Defaults().UI.Scrollback.
	//
	// Until F18 this bounded only the cache: the transcript itself held
	// every message GetSession returned. It is a real bound on both now.
	Scrollback int

	// HistoryBatch is how many messages one F18 scroll-fetch pulls —
	// `ui.historyBatch`. <= 0 falls back to defaultHistoryBatch, matching
	// config.Defaults().UI.HistoryBatch, so a workspace built without it
	// still pages rather than silently never paging.
	HistoryBatch int

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
	// historyBatchSize is `ui.historyBatch`; see history.go's historyBatch
	// for the accessor that applies the default.
	historyBatchSize int

	sessionID       string
	sessionTitle    string
	sessionProvider string
	sessionModel    string
	bootstrapped    bool

	// pendingPrefill is text `send to chat` (F15) handed over before this
	// workspace had a session to restore a draft for. Applied again by
	// setSession, after restoreDraft, which would otherwise setText over
	// the top of it -- see prefill.go's applyPendingPrefill.
	pendingPrefill string

	// projectID is the session's bound project, or "" when unbound. F8's @
	// picker reads it (PickerSession, picker.go) to decide fuzzy-over-the-
	// project-tree vs filesystem-browse-rooted-at-cwd, and to scope
	// ListDirectory/AttachContext the same way the backend already scopes
	// them by session.
	projectID string

	// chipTickArmed/chipTickGen pace attachments.go's periodic staleness
	// sweep — the same self-re-arming, idle-cost-zero shape as stream.go's
	// tickArmed/tickGen, a separate generation counter because the two
	// ticks pace unrelated things (render/watchdog vs. attachment
	// staleness) and must not stop or re-arm each other.
	chipTickArmed bool
	chipTickGen   int
	// resolvingPath maps an in-flight ResolveAttachment request's own
	// correlation id to the chip path it is pricing — see attachments.go's
	// own doc for why this lives outside the ordinary pending/queryKind map.
	resolvingPath map[string]string

	// providers is the set of provider names this backend registered, or
	// nil when it could not be established. Read only by knownProvider
	// (providers.go), which explains why nil has to mean "accept
	// anything" rather than "accept nothing".
	providers map[string]bool
	loading   bool
	loadErr   string

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
	// sendHistoryFn is F18's own send, and the only one that RETURNS the
	// correlation id it used -- a history fetch has to be matched against
	// the window state it was issued against, so a superseded one can be
	// dropped instead of spliced into a window that has since moved.
	// Substituted by a test for the same reason the two above are.
	sendHistoryFn func(payload map[string]any) (string, bool)

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
	//
	// S11b: B12 supplied it. The rows are still rendered verbatim --
	// nothing is merged, relabelled or dropped -- but they are now
	// REORDERED so each answer sits directly under its own question, keyed
	// on the persisted task_id and on nothing else. See reload.go.
	//
	// F18: `all` is no longer what the wire returned, and no longer the
	// whole session. It is DERIVED from `raw` (below) on every window
	// mutation. Nothing outside history.go assigns it except the package's
	// own tests, which set it directly and get F3's plain rendering with
	// no window behind it -- the same latitude histAt was already
	// bounds-safe for.
	all []wireMessage

	// raw is F18's resident window: a CONTIGUOUS run of persisted rows,
	// oldest-first, exactly as B18's keyset query returned them. It is the
	// single source `all` and `hist` are rebuilt from, which is what makes
	// S11b's three reload transforms idempotent under windowing -- one of
	// them (applyTaskRows) inserts rows, so it must never run against its
	// own output. See history.go.
	raw []wireMessage

	// window is every fact about `raw` that is not `raw` itself: its seq
	// bounds, the open-time ceiling that keeps a re-fetched tail from
	// duplicating w.live, what fetch is in flight, and what backpressure
	// has parked. See history.go's historyWindow.
	window historyWindow

	// taskRows is the last ListTasks answer, kept rather than consumed.
	//
	// Before F18 the tasks join ran exactly once (onTaskList called
	// applyTaskRows and dropped the rows). A windowed transcript rebuilds
	// `all` on every prepend, append and eviction, and each rebuild has to
	// re-run that join or a reloaded footer would survive only until the
	// first scroll-fetch. nil is the ordinary state before the reply
	// lands, and applyTaskRows is a no-op on it.
	taskRows map[string]taskRow

	// resumeAnchor is the scroll anchor Resume read out of F12's blob,
	// waiting for a session to apply itself to. Consumed exactly once, at
	// the end of setSession -- see history.go's applyResumeAnchor.
	resumeAnchor *blobAnchor

	// hist is a decoration parallel to `all`, one slot per row: non-nil
	// where a persisted row renders through F5's LIVE renderers (a
	// collapsed task slot, or a footer saying how the reply ended) instead
	// of render.go's plain message path. That reuse is the point: "a
	// reloaded @research answer is a collapsed slot, identical to live" is
	// true by construction when both go through renderTaskEntry, and would
	// be a claim needing its own tests if reload had a renderer of its own.
	//
	// Parallel rather than a field on wireMessage, because wireMessage is
	// the wire's own shape (model.go: a diff against message.py stays a
	// visual diff) and a liveEntry is UI state -- slotOpen, the render memo
	// -- with no business round-tripping through a wire struct.
	//
	// It may be SHORTER than `all`, and histAt is bounds-safe for exactly
	// that reason: every test that assigns w.all directly leaves this nil
	// and gets F3's plain rendering, unchanged.
	//
	// Every entry in here is terminal, always. A non-terminal one would
	// keep Streaming() true forever and re-arm the watchdog tick forever --
	// the idle-CPU leak F5 records -- so reload.go builds one only from an
	// ending it can actually name.
	hist []*liveEntry

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

	// frame counts renders. It exists so an entry is rendered at most ONCE
	// per frame, whatever the render memo's clock-based staleness rules
	// would otherwise decide -- see renderLive (stream_render.go).
	//
	// One frame renders each visible entry more than once: bottomAnchored
	// walks backward measuring heights, then visibleWindow walks forward
	// composing, and clampScroll and moveFocus measure too. Before this
	// counter, renderLive could answer those calls differently within a
	// single frame -- a stale cached render for the measuring pass, a real
	// one for the composing pass, if the frame happened to straddle the
	// 50ms throttle boundary. The pair (topAbs, topRow) is then an address
	// computed against one height and applied to another, and rows go
	// missing. Most visibly at the moment a code fence closes, which is
	// the one point an entry's height JUMPS (a raw tail becomes a boxed
	// block), and most durably at the end of a stream, where the tick
	// stops and nothing repaints the bad frame.
	frame int

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

	// themeGen is bumped every time SetTheme (F10) installs a new theme —
	// stream_render.go's per-entry render memo (liveEntry.renderedThemeGen)
	// compares against this to notice a theme change the same way it
	// already notices a width or focus change. See SetTheme's own doc.
	themeGen int

	// sessions is F6's Sessions card: the last ListSessions reply, kept
	// around rather than discarded once bootstrap has picked one. Refreshed
	// at bootstrap and after a switch (onSessionList/onSessionCreated) —
	// nothing else mutates a session's title or existence yet, so there is
	// no third moment that would leave this stale.
	sessions []wireSession

	// tasks and backendDown are F6's: the root-owned global task snapshot
	// and connection state, arriving via app.DiagnosticsMsg exactly the way
	// Restarts/InFlight already do for the Placeholder — see chat.go's
	// Update case and cards.go's renderers. Neither is fetched by this
	// package; Root decides when to ask (root.go's refreshTasks) and this
	// workspace only renders what it is handed.
	tasks       []app.TaskRow
	backendDown bool
	// noProviderConfigured is F10's own addition to the DiagnosticsMsg push
	// (app.Diagnostics.NoProviderConfigured) — see providers.go's
	// providerMissing for why this is read off the push rather than off
	// w.state.Provider directly.
	noProviderConfigured bool
	// connState is the connection state Root last pushed (same Seq guard as
	// the fields around it). It is the ONE input to connectionDown — see
	// connection.go for why the banner and a send's failure both read it.
	connState protocol.ConnState
	// diagnosticsSeq is the last app.DiagnosticsMsg.Seq this workspace has
	// applied — see protocol.go's DiagnosticsMsg case for why arrival
	// order alone cannot be trusted.
	diagnosticsSeq int

	// usage is F6's Status card "tokens / cost" line: the most recent
	// TokenUsage this session has seen, real numbers from UsageUpdated as a
	// reply streams (stream.go) or from the last persisted message's own
	// Usage field once reloaded (reload.go's setSession). Cost is never
	// set: the backend has no pricing table anywhere yet (managers/
	// message.py's TokenUsage doc says billing is a later episode's job),
	// so the card omits it, per spec, rather than inventing a number
	// nothing computed.
	usage wireUsage

	// contextReport is S13d's: the last decoded ContextReport, from either
	// the once-per-load pull (setSession) or the push after every turn
	// (onContextReport, protocol.go). nil is the real "nothing yet" state --
	// distinct from a zero-valued report -- same distinction this file
	// already makes for w.usage above.
	contextReport *wireContextReport

	// statusCardRect is the Status card's on-screen rect from the most
	// recent real (non-overlay) right-pane render (render.go's rightPane) --
	// S13d's click-to-expand hit-test target. Zero until the right pane has
	// rendered at least once.
	statusCardRect layout.Rect
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
	hb := opts.HistoryBatch
	if hb <= 0 {
		hb = defaultHistoryBatch
	}
	w := &Workspace{
		theme:            opts.Theme,
		client:           opts.Client,
		log:              opts.Log,
		scrollback:       sb,
		historyBatchSize: hb,
		streamTimeout:    st,
		pending:          make(map[string]queryKind),
		resolvingPath:    make(map[string]string),
		cache:            make(map[string][]string),
		focus:            -1,
		pinned:           true,
		state:            opts.State,
	}
	w.sendFn = w.doSend
	w.sendMsgFn = w.writeSend
	w.sendHistoryFn = w.doSendHistory
	w.saveStateFn = config.SaveState
	w.input.buf = newInputBuffer()
	w.input.focused = true // the input holds focus by default — F4
	return w
}

func (w *Workspace) ID() layout.WorkspaceID { return layout.WorkspaceChat }

// SetTheme is app.ThemeAware (F10): Settings' Appearance tab changes
// Root's own r.theme directly for its own chrome, and reaches this workspace
// through the same type-assertion Root already uses for FocusAware and
// OverlayProvider — this package holds its own copy (every render call
// reads w.theme, never a value threaded in from outside) because View's own
// signature is layout.Layout, not theme.Theme.
//
// Both of this package's render memos bake colour into their cached lines
// at the moment they are first rendered, and neither one's freshness check
// has ever had a reason to know the theme could change underneath it —
// found live, the real bug this fixes: changing the theme repainted every
// OTHER surface immediately, but the transcript kept rendering persisted
// messages (cache.go) and live/streaming entries (stream_render.go's
// per-entry memo) in the OLD palette until some UNRELATED event (a resize,
// a token, scrolling past a message whose id was not yet cached) happened
// to invalidate the right cache entry for an unrelated reason. Clearing
// cache.go's map directly handles persisted messages; themeGen is
// stream_render.go's equivalent for live entries, which are memoised one at
// a time rather than in a map SetTheme can simply wipe.
func (w *Workspace) SetTheme(t theme.Theme) {
	w.theme = t
	w.themeGen++
	w.invalidateCache()
}

// Cards is F6's right pane: Status short, Task medium, Sessions taking the
// remainder — LAYOUT.md's own heights, unchanged since F1 chose them for the
// Placeholder stub this replaces.
func (w *Workspace) Cards() []layout.CardSlot {
	return []layout.CardSlot{
		{MinHeight: 5},
		{MinHeight: 8},
		{MinHeight: 6, Flex: true},
	}
}

// Init has nothing to do at Bubble Tea's Init time: Update watches for
// protocol.ConnectedMsg and starts the ListSessions/GetSession/CreateSession
// sequence from there. If the client is already connected by the time this
// workspace is installed (SetWorkspace runs after New but Init could in
// principle run after a fast handshake), Update's ConnectedMsg case still
// fires once — Root always sends one after the handshake completes.
func (w *Workspace) Init() tea.Cmd { return nil }

// Suspend/Resume persist roughly two kilobytes per F1's Workspace doc — never
// message content, the same rule Resume documents: rehydrate from the
// database, the same path as opening a week-old session.
//
// F3 left both as no-ops because it had nothing that needed to survive an
// unload and nowhere to write it (F12 owns workspace-state persistence).
// **F18 is the first real implementation of either.** It has something that
// must survive: the scroll anchor, which is meaningless to re-derive and
// impossible to guess. Doing it now rather than letting F12 ship first is
// deliberate — F12 would otherwise design the blob schema having never heard
// of an anchor, and F18 would then have to migrate it. That is the exact
// B11 `AgentConfig.background` retrofit trap, avoided by ordering.
//
// What is in the blob is ONLY the anchor: `{anchorSeq, rowOffset,
// pinnedToBottom}`, a narrow F18-owned shape under its own name, which F12's
// real workspace-state type must ADAPT to rather than be identical to — the
// `CardSlot`/`Drafts` precedent. See history.go's suspendBlob.
//
// Nothing calls either method yet; F12 owns the lifecycle that will. The
// round trip is proven by test rather than by use, which is also why Resume
// can never fail on bad input: it degrades to the bottom and returns nil,
// because the restore path must never be the thing that breaks a resume.
func (w *Workspace) Suspend() ([]byte, error) {
	return json.Marshal(suspendBlob{Version: suspendVersion, Anchor: w.currentBlobAnchor()})
}

// Resume stores the anchor for setSession to apply. It cannot position
// anything itself: at Resume time there is no session, no window and no
// transcript to address. See history.go's applyResumeAnchor, which consumes
// it once the load completes.
//
// Every failure is a degradation, never an error. An empty blob (F3's own
// output, which a state file written before this episode will hold), a blob
// that is not JSON, and a version this build does not know all resume at the
// bottom — the same place a session with no stored anchor opens.
func (w *Workspace) Resume(raw []byte) error {
	w.resumeAnchor = nil
	if len(raw) == 0 {
		return nil
	}
	var blob suspendBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		w.log.Warn("chat: suspend blob unreadable, resuming at the bottom", "err", err)
		return nil
	}
	if blob.Version != suspendVersion {
		w.log.Warn("chat: suspend blob is a version this build does not know, resuming at the bottom",
			"version", blob.Version, "want", suspendVersion)
		return nil
	}
	w.resumeAnchor = blob.Anchor
	return nil
}

var _ app.Workspace = (*Workspace)(nil)
var _ app.FocusAware = (*Workspace)(nil)
var _ app.ThemeAware = (*Workspace)(nil)
