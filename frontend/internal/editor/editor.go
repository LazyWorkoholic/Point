package editor

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
}

// focusTarget is which of the Editor's two panes has the keyboard.
//
// Two, not three: the Terminal is F14's. The split exists because the tree
// needs Enter and the arrow keys, and the buffer needs every key there is,
// so one of them has to be able to say "not me".
type focusTarget int

const (
	focusBuffer focusTarget = iota
	focusTree
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
		tree:          newFileTree(),
		pending:       make(map[string]pendingReq),
	}
	if w.autosaveMode == "" {
		w.autosaveMode = config.AutosaveAfterDelay
	}
	if w.autosaveDelay <= 0 {
		w.autosaveDelay = config.DefaultAutosaveDelayMs * time.Millisecond
	}
	w.sendFn = w.doSend
	return w
}

func (w *Workspace) ID() layout.WorkspaceID { return layout.WorkspaceEditor }

// Init has nothing to do at Bubble Tea's Init time: Update watches for
// protocol.ConnectedMsg and resolves the project from there, which also
// covers the case where the handshake landed before this workspace was
// constructed (switchWorkspace calls New then Init, and the client may
// already be connected).
func (w *Workspace) Init() tea.Cmd {
	if w.client != nil && w.client.State() == protocol.StateConnected {
		w.onConnected()
	}
	return nil
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
	return key == "tab" || key == "shift+tab"
}

func (w *Workspace) Update(msg tea.Msg) (app.Workspace, tea.Cmd) {
	switch m := msg.(type) {
	case protocol.ConnectedMsg:
		w.onConnected()
	case protocol.EnvelopeMsg:
		return w, w.handleEnvelope(m.Envelope)
	case protocol.BackendLostMsg:
		w.backendDown = true
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
		// editPaste, not editOther: one physical paste can arrive as
		// several PasteMsgs (see that kind's own doc), and they have to
		// collapse back into one undo step.
		if len(w.docs) > 0 && w.focus == focusBuffer {
			probe := w.probeActive()
			w.docs[w.active].insertText(m.Text, editPaste)
			return w, w.autosaveAfter(probe)
		}

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
		return w, tea.Batch(cmd, w.autosaveAfter(probe))
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
		return w, tea.Batch(cmd, w.autosaveAfter(probe))
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
	if len(w.docs) == 0 {
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
