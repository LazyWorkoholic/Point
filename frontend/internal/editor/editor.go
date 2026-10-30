package editor

import (
	"encoding/json"
	"io"
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

	// Holder is the composed `<username>@<full-id>` lock key, generated
	// and persisted by main.go (config\holder.go, A2 / B32's Decision 4).
	// Empty disables locking entirely rather than taking a lock under a
	// blank identity -- WriteProjectFile refuses an empty holder outright
	// (`if not p.get("holder")`), so a blank one would make every save
	// fail with an Error envelope instead of a slug.
	Holder string

	// Autosave is config.AutosaveOff / AfterDelay / OnFocusChange. Empty
	// means after_delay, matching config.AutosaveMode's own normalisation
	// -- a zero Options in a test gets the behaviour the default config
	// gives, not a silently different one.
	Autosave string
	// AutosaveDelay is the after_delay interval. Zero means the 1000ms
	// default.
	AutosaveDelay time.Duration

	// Shell is F14's config.ShellPowerShell / config.ShellCmd -- a VALUE
	// taken at construction, the same way Autosave is, rather than a live
	// config.Config this workspace would otherwise have to hold and
	// re-read. Empty means PowerShell, matching config.TerminalShell's
	// own normalisation.
	Shell string

	// Provider is F10's stored provider default, the same value chat
	// forwards on ITS CreateSession (chat\protocol.go's onSessionList).
	//
	// F15 needs it because the Editor now creates a session of its own
	// (invoke.go's header says why it must), and a session created with no
	// provider inherits config.toml's default instead of the one the user
	// picked through Point's own UI -- so in Demo mode a fill would
	// silently run against whichever provider config.toml names rather
	// than the demo one.
	//
	// A VALUE, not a config.State, and the editor still neither reads nor
	// writes state.json: main.go passes this in and chat remains the only
	// workspace that persists it. The two-writer rule config\state.go's
	// saveStateTo arbitrates is unchanged. A default chosen while Point is
	// running arrives as app.ProviderDefaultSetMsg instead (Update), which
	// Root also REPLAYS to a freshly loaded workspace, so a choice made
	// before this workspace existed is not lost either.
	Provider *config.ProviderDefault
}

// focusTarget is which of the Editor's three panes has the keyboard.
//
// Three, now that F14 exists: the Code File buffer, the File Tree, and the
// Terminal. The split exists because the tree needs Enter and the arrow
// keys, the buffer needs every key there is, and the terminal needs its
// own line editing (history, a typed line that may be stdin rather than a
// new command) -- so each of them has to be able to say "not me".
type focusTarget int

const (
	focusBuffer focusTarget = iota
	focusTree
	focusTerminal
)

// Workspace is the Editor.
//
// It holds its own theme copy for the reason chat does: View's signature is
// layout.Layout, not theme.Theme, so every render call reads w.theme, and
// Settings' Appearance tab reaches this through app.ThemeAware.
type Workspace struct {
	theme  theme.Theme
	client *protocol.Client
	log    *slog.Logger

	docs   []*doc
	active int
	focus  focusTarget

	tree      *fileTree
	tabScroll int

	// term is F14's terminal pane: Tier 0's command runner and command
	// history, plus the ExecProcess hatch's `!` prefix. Never nil after
	// New -- see newTerminalPane's own doc for why a single long-lived
	// pane outlives any one command it runs.
	term *terminalPane
	// shell is Options.Shell, read by terminalShell() in terminal.go.
	shell string
	// termRect is the terminal box's geometry, recorded during View for
	// mouse.go to hit-test against -- the same contract treeRect and
	// textArea already follow.
	termRect layout.Rect

	// projectID and projectRoot are this workspace's own resolution, not a
	// session's -- see protocol.go. Empty means no .point\ was found, which
	// makes every file read-only.
	projectID   string
	projectRoot string

	// holder is this process's lock identity, whole-string. See
	// config\holder.go; the display form splits on the LAST '@'.
	holder string

	// Autosave configuration and its per-document timers -- see
	// autosave.go. The mode and delay are values, not a Config: a
	// workspace takes what it needs, the way chat takes Scrollback.
	autosaveMode  string
	autosaveDelay time.Duration

	// autosavePaused suspends the whole workspace's autosave while a
	// modal question about these buffers is on screen -- UI-13's unload
	// confirm, which Root opens and so could not reuse the per-document
	// pause confirmCloseDirty takes for itself.
	//
	// Workspace-level rather than per-document ON PURPOSE, and it is not
	// the C5 trap: that rule is about TIMERS and per-entity deadlines,
	// and this sets no deadline for any file. The dialog is about the
	// workspace ("3 file(s) have unsaved changes"), so the pause is too,
	// and a per-document flag here would have to be set for every
	// document and cleared for exactly the ones it was set for.
	//
	// Deliberately NOT doc.autosaveBlocked, which is a document's own
	// refusal (read-only, held by another holder) and must survive a
	// resume untouched -- clearing that on Cancel would quietly re-arm
	// autosave on a file the backend has already refused.
	autosavePaused bool

	// heartbeatOn guards the single 20s lock heartbeat tick against being
	// armed twice. One ticker beats EVERY held lock, which is not the
	// per-entity-timer trap C5 names: it does not reset any file's
	// deadline, it refreshes all of them on the same schedule.
	heartbeatOn bool

	// StatusLinger's editor-side equivalent: substitutable timers so a
	// test does not have to sleep for a real second. Zero means the real
	// value.
	nowFn func() time.Time

	pending map[string]pendingReq
	// resolving guards against a duplicate ProjectResolve: Init resolves
	// when the client is already connected, and switchWorkspace replays
	// ConnectedMsg to a freshly loaded workspace, so both fire on every
	// load after the handshake. See protocol.go's onConnected.
	resolving bool
	// writeFn is the UNCORRELATED write: one envelope, an id the caller
	// minted, no pending entry and no waiter. F15's SendMessage and
	// CancelTask are the only users, and both need it for chat's own
	// reason -- a send answers with a whole stream of events sharing one
	// corr_id, and Request's waiter channel holds exactly one.
	//
	// A second seam beside sendFn rather than a widened sendFn, which is
	// the shape chat already uses (sendMsgFn beside its own send): the two
	// differ in what they do about correlation, which is the only
	// interesting thing either of them does.
	writeFn func(id, cmdType string, payload map[string]any) error

	// sendFn is a struct field defaulting to doSend so a test can
	// substitute a recorder: what the tests exercise is the decision logic
	// in onProject/onFileList/onFile, not protocol.Client's send path,
	// which that package already tests directly. Same seam chat uses.
	sendFn func(string, queryKind, map[string]any)

	// Geometry recorded during View for mouse.go to hit-test against --
	// never re-derived. See mouse.go's header.
	lastLayout layout.Layout
	textArea   layout.Rect
	treeRect   layout.Rect
	// tabStrip is the tab row as a RECT, not a row number.
	//
	// It was `tabRow int` and hit-tested on Y alone, which made every
	// click on that screen row -- right across the terminal -- a click on
	// the tab strip. The Code File box's first content row and the File
	// Tree card's first content row are the SAME screen row (both panes
	// start at Compute's bodyTop), so the top row of the tree was
	// unreachable: its clicks were routed to the strip, matched no tab
	// span, and were dropped. A rect cannot have that bug.
	tabStrip layout.Rect
	tabSpans []tabSpan
	dragging bool

	// tasks and backendDown come from Root's own DiagnosticsMsg push, the
	// same snapshot chat reads -- see app\tasks.go for why Root fetches it
	// rather than each workspace.
	tasks          []app.TaskRow
	backendDown    bool
	diagnosticsSeq int

	// ---- F15: the Filler-Explainer ----------------------------------
	//
	// All six of these are TRANSIENT and none is suspended: an
	// invocation, a ghost, a prompt, a panel and a card are all about a
	// moment, and a resumed workspace that restored a ghost would be
	// showing model output nothing is still producing. Suspend() is
	// unchanged by this episode.

	// sessionID is the Editor's OWN session (invoke.go's header), created
	// lazily on the first invocation. Empty until then; never read from
	// or written to state.json.
	sessionID       string
	creatingSession bool
	// provider is Options.Provider, kept current by
	// app.ProviderDefaultSetMsg.
	provider *config.ProviderDefault

	// inv is the single live explain-or-fill invocation, or nil. One at a
	// time, deliberately -- see invoke.go.
	inv *invocation
	// pendingSend is an invocation waiting for the session that will
	// carry it.
	pendingSend *pendingSend
	// settleGen generations the post-TaskDone wait for a FillProposal, so
	// a superseded tick does nothing -- autosave's own per-document
	// pattern (C5).
	settleGen int

	// ghostFollow is "keep the pane at the live edge of the streaming
	// ghost", the editor's counterpart to chat's `pinned`.
	//
	// Set when a fill starts, cleared by scrolling up out of the stream,
	// and set again by scrolling back down to the edge. Cleared with the
	// ghost itself -- a finished region must not keep dragging the
	// viewport anywhere.
	ghostFollow bool

	// ghost is the fill reply drawn at the caret. Display-only, not in any
	// buffer; ghost.go's header is the record of why.
	ghost *ghostText
	// prompt is the inline one-line fill instruction, or nil.
	prompt *fillPrompt
	// panel is the explain panel splitting the Code File pane, or nil.
	panel *explainPanel
	// card is the last DiffCard this workspace built. Held only so the
	// staleness reply can find the card it was asked for; the card itself
	// is owned by Root's overlay slot.
	card *DiffCard
	// staleCard hands a card to doSend for one CheckFileStale request --
	// see protocol.go's doSend for why it is a field.
	staleCard *DiffCard
	// deferredCard is a card built while this workspace was backgrounded,
	// waiting for it to be on screen again -- see onWorkspaceActivated.
	deferredCard *DiffCard

	// onScreen is whether this workspace is the active one.
	//
	// Tracked rather than asked, because a workspace cannot ask: Root
	// never tells it anything about the others and there is no "am I
	// active" call. Set by app.WorkspaceActivatedMsg and cleared by
	// app.WorkspaceDeactivatedMsg -- BOTH halves, from Root, explicitly.
	//
	// # This field shipped broken once, and the reason is worth keeping
	//
	// It was set by the activation message and "cleared" by inferring a
	// switch away from that same message naming a different workspace.
	// That cannot work and the comment describing it was incoherent: Root
	// sends the activation message to the workspace BECOMING active and
	// to no one else, so this workspace never saw one naming anyone else
	// and the clearing branch was dead code. The flag latched true on the
	// first activation and stayed true, so the deferral it gates never
	// engaged -- a diff card finished in the background opened over Chat,
	// and its apply message then went to Chat too and was dropped.
	//
	// The general shape: a workspace cannot infer a transition it is not
	// told about. If a state has two edges, both edges need a message.
	//
	// It starts FALSE, which is correct: a workspace that has never been
	// activated has never been on screen.
	onScreen bool

	// clipOut is where copySelection writes its OSC 52 sequence. Nil
	// means stderr -- see copy.go's clipTarget.
	clipOut io.Writer

	// settleDelay substitutes proposalSettleDelay in tests. Zero means
	// the real value -- see settleWait.
	settleDelay time.Duration

	// note is the LAST transient line this workspace emitted. It is kept
	// only so the empty state can repeat it; the line the user actually
	// reads is the one Root puts in the status bar via
	// app.WorkspaceStatusMsg -- see say().
	note string

	resume *suspendBlob
}

func New(opts Options) *Workspace {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	w := &Workspace{
		theme:         opts.Theme,
		client:        opts.Client,
		log:           opts.Log,
		holder:        opts.Holder,
		autosaveMode:  opts.Autosave,
		autosaveDelay: opts.AutosaveDelay,
		provider:      opts.Provider,
		tree:          newFileTree(),
		pending:       make(map[string]pendingReq),
		term:          newTerminalPane(),
		shell:         opts.Shell,
	}
	if w.autosaveMode == "" {
		w.autosaveMode = config.AutosaveAfterDelay
	}
	if w.autosaveDelay <= 0 {
		w.autosaveDelay = config.DefaultAutosaveDelayMs * time.Millisecond
	}
	w.sendFn = w.doSend
	w.writeFn = w.doWrite
	return w
}

func (w *Workspace) ID() layout.WorkspaceID { return layout.WorkspaceEditor }

// TerminalMode is app.TerminalModeAware, read by Root's recompute instead
// of the hardcoded TermSmall it used before F14. See terminal.go's
// effectiveTerminalMode for why this is not simply w.term.mode: the
// remembered mode and what is actually DRAWN differ while focus is on the
// code pane, and recompute wants the drawn one.
func (w *Workspace) TerminalMode() layout.TermMode {
	return w.effectiveTerminalMode()
}

// ProjectRoot is app.ProjectRootProvider, read by the hatch (app\hatch.go)
// so Root can resolve a working directory without reaching into this
// workspace's fields. Empty exactly when w.projectRoot is -- no project
// resolved yet, or none found above the working directory.
func (w *Workspace) ProjectRoot() string {
	return w.projectRoot
}

// Init has nothing to do at Bubble Tea's Init time: Update watches for
// protocol.ConnectedMsg and resolves the project from there, which also
// covers the case where the handshake landed before this workspace was
// constructed (switchWorkspace calls New then Init, and the client may
// already be connected).
func (w *Workspace) Init() tea.Cmd {
	if w.client != nil && w.client.State() == protocol.StateConnected {
		w.onConnected()
	}
	// F14: the terminal pane's event puller, started once and never
	// stopped for the life of this workspace -- the same long-lived
	// "pull one, re-arm" shape protocol.Client.Next() uses for the
	// backend's own events, chosen for the same reason: a goroutine
	// outside Bubble Tea's own loop (here, a running command's stdout/
	// stderr readers) cannot call into the Program directly, so it feeds
	// a channel instead and this Cmd is what drains it. Idle most of the
	// time -- it simply blocks until a command is run -- which is fine;
	// it is one goroutine for the workspace's whole lifetime, not one per
	// command.
	return w.term.next()
}

func (w *Workspace) SetTheme(th theme.Theme) {
	w.theme = th
	// The highlight cache bakes colour into its styled runes, so a theme
	// change has to reach it. It compares theme names itself (highlight.go)
	// rather than being invalidated here, so this is only the copy.
}

// Cards is LAYOUT.md's Editor right pane: File Tree over Task.
//
// The FILE TREE takes the larger share and flexes; Task gets a fixed,
// smaller box. LAYOUT.md's sketch draws it the other way round (an 8-row
// tree over a Task card that runs to the bottom), and that is wrong in
// use: the tree is the only way to open a file in this workspace and it
// has a whole project to show, while the Task card is a summary whose full
// version is one keypress away in F9a's tray (f1). A six-row tree beside a
// fifteen-row Task card spends the pane on the thing that has least to
// say.
//
// Recorded as a deliberate divergence from that sketch rather than a
// misreading of it -- Aryan's call, and the reasoning is the tray's
// existence.
func (w *Workspace) Cards() []layout.CardSlot {
	return []layout.CardSlot{
		{MinHeight: 8, Flex: true},
		{MinHeight: 6},
	}
}

// CapturesKey is app.KeyCapturer.
//
// # F15 made this CONDITIONAL, and the divergence is deliberate
//
// What follows below was written as "EXACTLY TWO KEYS... The claim is
// unconditional rather than gated on focus", and the reasoning for that is
// still right for `tab`/`shift+tab`: a key whose meaning depends on which
// pane has the keyboard is a key the user has to track.
//
// F15 adds a claim of a different kind, for a surface that is MODAL rather
// than focus-dependent: while the inline fill prompt or the explain panel
// is open, `esc` and `ctrl+c` belong to it. Both are required by the spec
// ("`esc` cancels", "Cancelling (`esc` or `ctrl+c`) discards the ghost
// entirely and cancels the task", "dismissed with `esc`") and neither can
// be had any other way: dispatchKey consults the chord registry BEFORE this
// workspace, `esc` is chat.browse's chord and `ctrl+c` is chat.cancel's,
// and both have Chat-only When predicates -- so in this workspace they are
// swallowed with "… is not available right now" and never arrive (keys.go's
// header records that as pre-existing behaviour).
//
// Why that does not reintroduce the problem the unconditional rule exists
// to prevent:
//
//   - the claim is on the existence of a surface the user just opened, not
//     on which pane has focus. With nothing open these keys behave exactly
//     as they did before this episode -- including still printing "not
//     available right now", which is unchanged, not newly broken;
//   - both surfaces advertise the key they claim, in their own footer or
//     label, which is F04's standing rule;
//   - the alternative was to make the explain panel an app.Overlay, which
//     gets `esc` for free and would have covered the code it is explaining.
//     See explainpanel.go's header.
//
// The GHOST counts as one of those surfaces, which is easy to miss and is
// Done-when 3c: a fill whose prompt has already closed on Enter has no
// prompt and no panel, only a ghost growing at the caret -- and `esc` there
// is what cancels the stream. Gated on a LIVE invocation rather than on
// w.ghost being non-nil so a ghost left on screen by a finished task cannot
// hold a key hostage.
//
// ESC ONLY. ctrl+c is claimed UNCONDITIONALLY below, because in this
// workspace it means copy and nothing else -- see copy.go for why that is
// a deliberate divergence from F15's Build and why a scoped ctrl+c would
// be the worse of the two options.
func (w *Workspace) capturesModalKey(key string) bool {
	if w.prompt == nil && w.panel == nil && !w.liveInvocation() {
		return false
	}
	return key == "esc"
}

// The original claim, unchanged:
//
// EXACTLY TWO KEYS, and they are named here rather than left to be
// discovered in a switch statement because every captured key is a key
// whose global binding stops working while this workspace is active:
//
//	tab        indent, or insert one indent unit
//	shift+tab  dedent
//
// `tab` is pane.focus.next's chord (F4) and `shift+tab` is unbound, taken
// together so Tab and Shift+Tab behave as a pair rather than one indenting
// and the other focusing something. Nothing else is claimed: esc, ctrl+c,
// ctrl+e, ctrl+r, ctrl+p, ctrl+l, ctrl+q and F1 all keep their global
// meanings here, including the four whose When predicate is Chat-only and
// which therefore print "… is not available right now" in this workspace
// (recorded in the handoff -- it is pre-existing dispatchKey behaviour, not
// this episode's).
//
// The claim is unconditional rather than gated on focus. A gated claim
// would mean Tab indents or moves focus depending on which pane has the
// keyboard, which is a key whose meaning the user has to track -- and the
// tree does not want Tab for anything.
func (w *Workspace) CapturesKey(key string) bool {
	// ctrl+c is claimed whatever is on screen: it is COPY here, always,
	// and the chord table would otherwise answer "Cancel Stream is not
	// available right now" and swallow it (chat.cancel's chord, whose
	// When is Chat-only). See copy.go.
	return key == "tab" || key == "shift+tab" || key == "ctrl+c" ||
		w.capturesModalKey(key)
}

func (w *Workspace) Update(msg tea.Msg) (app.Workspace, tea.Cmd) {
	switch m := msg.(type) {
	case protocol.ConnectedMsg:
		w.onConnected()
	case protocol.EnvelopeMsg:
		return w, w.handleEnvelope(m.Envelope)
	case protocol.BackendLostMsg:
		w.backendDown = true
	case app.WorkspaceActivatedMsg:
		// Root sends this to the workspace BECOMING active and to no one
		// else, so the id is always this one; the check is an assertion,
		// not the mechanism. (Treating it as the mechanism -- "false when
		// it names someone else" -- is exactly how onScreen came to latch
		// true forever. See the field's own doc.)
		if m.ID != layout.WorkspaceEditor {
			break
		}
		w.onScreen = true
		return w, w.onWorkspaceActivated()

	case app.WorkspaceDeactivatedMsg:
		// The other half, from Root, before it changes which workspace is
		// active. Nothing is inferred.
		if m.ID == layout.WorkspaceEditor {
			w.onScreen = false
		}

	case app.ProviderDefaultSetMsg:
		// A provider chosen while Point is running, and (via
		// switchWorkspace's replay) one chosen before this workspace
		// existed. Kept for the NEXT CreateSession only -- an existing
		// session is never rebound, which is F10c's own inherited limit
		// (Decision 2) and not something this episode solves.
		d := m.Default
		w.provider = &d

	case proposalSettleMsg:
		return w, w.onProposalSettle(m)

	case app.DiagnosticsMsg:
		// Seq-guarded for the race chat\protocol.go documents: each push
		// is its own tea.Cmd goroutine and arrival order is not
		// guaranteed, so an older snapshot landing after a newer one must
		// not overwrite it.
		if m.D.Seq > w.diagnosticsSeq {
			w.diagnosticsSeq = m.D.Seq
			w.tasks = m.D.Tasks
			w.backendDown = m.D.ConnState == protocol.StateFailed
		}
	case app.PasteMsg:
		// F15: whichever surface owns the keyboard owns the paste.
		//
		// This case used to reach the buffer unconditionally, and it is
		// ABOVE the tea.KeyMsg case that routes to the modal surfaces --
		// so a paste while the inline fill prompt was open went into the
		// FILE at the caret instead of into the prompt, invisibly, only
		// showing up once the prompt was dismissed. That is also where
		// F15's gate's "nothing wrote app.py" failures came from: the
		// pasted text was a real buffer edit, so autosave persisted it,
		// correctly, one second later.
		//
		// The prompt takes it. The explain panel SWALLOWS it: the panel
		// is a reading surface that owns the keyboard, and a paste
		// falling past it into the file underneath is the same bug with
		// a different overlay on top.
		if w.prompt != nil {
			w.prompt.paste(m.Text)
			return w, nil
		}
		if w.panel != nil {
			return w, nil
		}
		// F14: and the terminal's input line, which owns the keyboard the
		// same way the two surfaces above do while it has focus. Without
		// this the paste fell through to the branch below, which ignores
		// it unless the BUFFER is focused -- so ctrl+v in the pane did
		// nothing at all.
		if w.effectiveFocus() == focusTerminal {
			w.pasteIntoTerminal(m.Text)
			return w, nil
		}
		// editPaste, not editOther: one physical paste can arrive as
		// several PasteMsgs (see that kind's own doc), and they have to
		// collapse back into one undo step.
		if len(w.docs) > 0 && w.focus == focusBuffer {
			probe := w.probeActive()
			w.docs[w.active].insertText(m.Text, editPaste)
			return w, w.autosaveAfter(probe, "paste into the buffer")
		}

	// ---- F14 ---------------------------------------------------------
	case terminalEventMsg:
		return w, w.onTerminalEvent(m.ev)

	// ---- F13b -------------------------------------------------------
	case heartbeatTickMsg:
		return w, w.onHeartbeatTick()
	case autosaveTickMsg:
		return w, w.onAutosaveTick(m)
	case staleReloadMsg:
		return w, w.onStaleReload(m)
	case staleOverwriteMsg:
		return w, w.onStaleOverwrite(m)
	case diffApplyMsg:
		// F9b: the DiffCard asks, this workspace applies. The card never
		// touches a document itself -- see diffcard.go's onDiffApply.
		return w, w.onDiffApply(m)
	case closeDirtyMsg:
		return w, w.onCloseDirty(m)
	case closeCancelledMsg:
		return w, w.onCloseCancelled(m)
	case app.BehaviourOverridesSetMsg:
		// index.respect_gitignore is one of the keys B26 accepts and
		// F10b's Behaviour tab sets. The tree does not own that toggle
		// (tree.go's header) -- it re-indexes when it moves, which is
		// what makes the toggle visible here without a second control
		// over one backend key.
		w.refreshTree(true)
	case tea.MouseMsg:
		probe := w.probeActive()
		cmd := w.handleMouse(m)
		return w, tea.Batch(cmd, w.autosaveAfter(probe, "mouse"))
	case tea.KeyMsg:
		// The autosave debounce is armed from the buffer's own
		// Version(), compared across the whole key handler, rather than
		// from each of the dozen methods that mutate. Round 5's lesson
		// applied before the bug: r.status had forty assignment sites
		// and the expiry had to be derived in the one funnel they all
		// passed through. Undo, Redo and (later) ApplyRegion all splice,
		// so all three arm the timer here with no special case --
		// which is autosave rule 4, for free.
		probe := w.probeActive()
		cmd := w.handleKey(m)
		return w, tea.Batch(cmd, w.autosaveAfter(probe, "key "+m.String()))
	}
	return w, nil
}

// say emits one transient status line through Root.
//
// Every message this workspace produces goes through here, which is the
// whole of the fix for four separate "no message at all" reports: setting
// w.note reached only the empty state, and the empty state is by
// definition not on screen whenever a file is open. Returning a tea.Cmd
// rather than writing anything is what keeps Root the sole owner of the
// status bar.
// effectiveFocus is which pane actually has the keyboard.
//
// DERIVED, not stored, and that is the whole point. With no file open
// the buffer pane has nothing to type into, so the tree holds the
// keyboard; the moment a document exists, w.focus -- the user's own
// choice via ctrl+o -- governs again.
//
// # Why not a field set when the doc list changes
//
// It was, for one iteration, and it broke immediately: a workspace
// whose documents arrive by any path that forgets to update the field
// keeps the tree focused and swallows every keystroke. Six tests
// caught it, and they were right to -- "set this flag everywhere the
// list changes" is the shape of F13a's dirty-notice latch, where one
// flag cleared in one place stood in for state three places could
// move.
//
// # What it replaces
//
// F13a kept focus on the buffer with nothing open and had bufferKey
// FORWARD the arrow keys to the tree, so "an editor that ignores every
// key until a file is open" would not read as frozen. The keys worked
// and the focus ring lied about why: the buffer pane was drawn focused
// while the tree was the thing moving, and ctrl+o looked inert.
// Reported as "arrow keys leak to the file tree even when the editor
// pane has focus", which is precisely what it was. Deriving it gives
// the same working arrows with the focus ring on the pane they move.
// Two cases decide it, and only the first is here:
//
//  1. NOTHING OPEN -> the tree, whatever w.focus says. Derived, below.
//  2. A DOCUMENT WAS JUST OPENED and the user never asked for the
//     editor -> the tree. NOT a case in this function: it is handled
//     where it belongs, by onFile carrying the effective focus across
//     the append instead of overwriting it with focusBuffer. By the
//     time this function is asked, w.focus already says "tree", so
//     there is nothing extra to derive.
//
// Putting (2) here was the first design and it cannot work: this
// function is a pure read with no way to know whether the active
// document is "just opened" without a timestamp or a flag -- which is
// the stored-state-that-drifts shape again. Moving the decision to the
// one transition that creates the condition keeps this a one-line
// derivation.
func (w *Workspace) effectiveFocus() focusTarget {
	// F14 narrows the override to focusBuffer specifically. It used to
	// fire whenever w.docs was empty regardless of what w.focus actually
	// held, which was correct while buffer and tree were the only two
	// targets -- an empty buffer pane really has nothing to type into --
	// but it also meant cycling TO the terminal with no file open bounced
	// straight back to the tree, because this function silently
	// overrode the choice on every single read. The terminal does not
	// need an open document to run a command in; only the buffer does.
	if len(w.docs) == 0 && w.focus == focusBuffer {
		return focusTree
	}
	return w.focus
}

func (w *Workspace) say(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	w.note = text
	return func() tea.Msg { return app.WorkspaceStatusMsg{Text: text} }
}

// ---------------------------------------------------------------------------
// Suspend / Resume
// ---------------------------------------------------------------------------

// suspendVersion is the blob's OWN version, independent of
// config.StateVersion -- a workspace changes its own shape without moving
// the state file's, which is the contract config\state.go's WorkspaceState
// doc spells out.
const suspendVersion = 1

type suspendBlob struct {
	Version  int          `json:"version"`
	Open     []suspendDoc `json:"open,omitempty"`
	Active   int          `json:"active"`
	Expanded []string     `json:"expanded,omitempty"`

	// TermMode is F14's size mode -- "the mode is remembered per
	// workspace... and travels in Suspend()", LAYOUT.md's own words.
	// Deliberately NOT command history, which is keyed by PROJECT and
	// lives in state.json's own TerminalHistory map instead -- see that
	// field's doc for why the two are kept apart. omitempty rather than a
	// version bump: an old blob without this field decodes to "", and
	// terminalModeFromBlob treats "" the same as TermSmall.
	TermMode string `json:"term_mode,omitempty"`
}

type suspendDoc struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Top  int    `json:"top"`
}

// Suspend stores position, never content: which files were open, where the
// caret was in each, and which directories the tree had expanded. Content
// comes back from the backend on Resume, the same code path as opening the
// file in the first place.
//
// A DIRTY buffer's edits are not in here, and they never will be: the
// right answer to "there is unsaved work" is not to serialise 95KB into
// a 2KB position blob, it is not to unload. app.DirtyBuffers (status.go)
// is what makes the idle sweep skip such a workspace.
//
// F13b narrows how often that matters rather than removing it. With
// autosave on, a buffer is dirty only for the second or so between the
// edit and the write, so the sweep's skip fires rarely -- but
// `autosave = "off"` is a supported setting and a save can be REFUSED
// (locked, read-only, stale), so "dirty for a long time" is still a
// reachable state and the skip is still the thing standing between it
// and a silent loss.
//
// Suspend also does NOT release this workspace's locks. That is
// app.LockHolder's job (lock.go's ReleaseLocks), called by
// unloadWorkspace just before this -- separate because Suspend is also
// called on the debounced persistence path for a workspace that is
// staying loaded, and releasing a lock there would drop it every two
// seconds.
func (w *Workspace) Suspend() ([]byte, error) {
	blob := suspendBlob{
		Version:  suspendVersion,
		Active:   w.active,
		Expanded: w.tree.expandedPaths(),
		TermMode: string(w.term.mode),
	}
	for _, d := range w.docs {
		blob.Open = append(blob.Open, suspendDoc{
			Path: d.path, Line: d.cursor.Line, Col: d.cursor.Col, Top: d.top,
		})
	}
	return json.Marshal(blob)
}

// Resume stores the blob for replay once the project and the tree exist.
//
// It cannot open anything itself: at Resume time there is no connection, no
// project and no file list. Every failure degrades rather than erroring --
// an empty blob, a blob that is not JSON, and a version this build does not
// know all resume to an editor with nothing open, which is where a first
// launch starts anyway.
func (w *Workspace) Resume(raw []byte) error {
	w.resume = nil
	if len(raw) == 0 {
		return nil
	}
	var blob suspendBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		w.log.Warn("editor: unreadable suspend blob", "err", err)
		return nil
	}
	if blob.Version != suspendVersion {
		return nil
	}
	w.tree.restoreExpanded(blob.Expanded)
	// Applied immediately, unlike Open: the mode is pure local UI state
	// that needs no project, no tree and no connection to make sense of,
	// so it does not have to wait for replayResume. An unrecognised or
	// absent value falls back to TermSmall through the same normalisation
	// path layout.TermMode's own callers use everywhere else.
	switch layout.TermMode(blob.TermMode) {
	case layout.TermSmall, layout.TermOverlay, layout.TermFull:
		w.term.mode = layout.TermMode(blob.TermMode)
	default:
		w.term.mode = layout.TermSmall
	}
	w.resume = &blob
	return nil
}

// replayResume reopens what Suspend recorded, once the tree has landed.
func (w *Workspace) replayResume() {
	if w.resume == nil {
		return
	}
	for _, od := range w.resume.Open {
		w.openPath(od.Path)
	}
	// The blob stays until the last file has come back: applyResumeTo
	// consumes it per document, matching on path.
	if len(w.resume.Open) == 0 {
		w.resume = nil
	}
}

// applyResumeTo restores one document's caret and scroll if the blob named
// it, and drops the blob once nothing is left to restore.
func (w *Workspace) applyResumeTo(d *doc) {
	if w.resume == nil {
		return
	}
	for i, od := range w.resume.Open {
		if od.Path != d.path {
			continue
		}
		d.cursor = d.buf.Clamp(Pos{Line: od.Line, Col: od.Col})
		d.anchor = d.cursor
		d.top = od.Top
		w.resume.Open = append(w.resume.Open[:i], w.resume.Open[i+1:]...)
		break
	}
	if len(w.resume.Open) == 0 {
		if w.resume.Active < len(w.docs) {
			w.active = w.resume.Active
		}
		w.resume = nil
	}
}
