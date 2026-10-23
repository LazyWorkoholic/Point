package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/diff"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// DiffCard is F09's UI-07: review an agent's proposed change, edit it, and
// apply it into a buffer.
//
//	╭─ Proposed change · config.py ─────────────────────────────╮
//	│  12    def parse_config(path):                            │
//	│  13 +      if not path.exists():                          │
//	│  14 +          raise ConfigError(path)                    │
//	│  15        with open(path) as f:                          │
//	│ ───────────────────────────────────────────────────────── │
//	│ ⏎ apply    e edit    d discard                            │
//	╰───────────────────────────────────────────────────────────╯
//
// # Scope: Done-when #5 and #6 only
//
// This episode builds the card, the diff, the edit mode and the apply.
// Done-when #7 -- "modify the file on disk after generation, then apply ->
// refused with a reason" -- is DEFERRED, and deferred with a reason rather
// than forgotten. See "The staleness guard is not built" below.
//
// # Nothing constructs this yet, and that is the plan
//
// `FillProposal` does not exist on the Go side of the wire: there is no
// `EvFillProposal` in `protocol\types.go`, nothing in this tree sends an
// `editor_context`, and INDEX.md puts F15 (Filler-Explainer Integration)
// DOWNSTREAM of F9b, not upstream -- "Reads B24's payload and calls F9b's
// diff function rather than re-implementing it. Its dependency is F9b, not
// 'F9'" (F09a's handoff). So this card ships with no production caller, for
// exactly the reason `doc.ApplyRegion` shipped with none in 18a: the
// behaviour either holds by construction or has to be rebuilt to get it,
// and the tests are what hold it. F15 wires the producer.
//
// # Where this lives, and the packaging collision it does NOT resolve
//
// F09 says "Same card, same keys, in both workspaces" -- Chat and Editor.
// That is not buildable today, and the blocker is specific:
//
//   - `doc.ApplyRegion` is a method on `doc`, which is UNEXPORTED, and
//     `Workspace` exposes no apply wrapper. The apply entry point F13a
//     built for this card is reachable only from inside `package editor`.
//   - `app` imports neither `chat` nor `editor`; both import `app`. So a
//     card in `app` -- where F9a's `ResultCard` lives, beside the tray that
//     opens it -- cannot reach `ApplyRegion` at all.
//   - A card in `editor`, which is this one, cannot be opened by `chat`
//     without `chat` importing the Editor workspace package.
//
// This is F9a's own markdown-packaging problem one package over ("`chat`
// imports `app` -- so this package cannot reach it, and the only ways to
// get it here are to move it into a third package or to move this card into
// `chat`"). It is left OPEN rather than settled under this episode's scope:
// the options are an exported apply seam on `Workspace`, or a third package
// holding the card with the apply passed in as a function. Recorded in the
// handoff.
//
// What this episode did do is keep the choice cheap: the DIFF is in
// `internal\diff`, which depends on nothing, and this card holds no `*doc`
// for its target -- only a snapshot of its lines (see `original`). So the
// card is a pure review surface over data, and whichever way the packaging
// goes, the computation and the review logic move with it.
//
// # The apply path, and who owns the mutation
//
// This card NEVER touches the target buffer. `enter` emits a
// `diffApplyMsg` and `Workspace.onDiffApply` does the work, for the reason
// `stale.go` already records: "an Overlay's Update runs inside Root's
// Update and any callback it fired would be mutating a workspace from code
// that does not own it. The editor handles these in its own Update, where
// it does."
//
// The card is then marked applied by that handler, AFTER the splice
// returns and only if the buffer's `Version()` actually moved -- F9a's
// hazard 3, which is F8's chip bug ("a card that looks applied when it was
// not"). Since the apply goes into an in-memory buffer and never to disk, a
// failed apply is atomic by construction, but only with the ordering this
// way round.
//
// # The staleness guard is not built (Done-when #7), and the guard ORDER
// is incomplete with it
//
// ---- HISTORY. BOTH HALVES ARE BUILT NOW (F15 and F9d) ----------------
//
// Kept verbatim because it is the record of two guards that were left
// ABSENT rather than approximated, and of why each cheap substitute was
// refused. What has changed since:
//
//   - Done-when #7 (the file ON DISK) was closed by F15 as its 7b. B36
//     made `_render_editor_context` stat the descriptor it reads the
//     region from and put the pair on `FillProposal`; `RecordedMtime`/
//     `RecordedSize`/`HasRecordedPair` are populated from THAT event, and
//     `onDiffApply`'s guard 6 asks `CheckFileStale` before any splice.
//   - The guard ORDER (F9a hazard 1) was closed by F9d. `SessionID` is
//     carried off the `FillProposal` envelope, the staleness check sends
//     it so the right scope answers, and `applyNow` records it on the
//     target document (`doc.agentSession`) so the SAVE is resolved
//     against that session too -- `_handle_write_project_file` takes
//     `session_id` and ignores `project_id` outright when it is present.
//     The third bullet below ("there is no session scope available") is
//     what stopped being true: F15 gave the Editor its own session, and
//     Go does receive `FillProposal` now.
//
// The paragraphs below describe the state at F9b and are not the current
// behaviour of this file.
//
// `RecordedMtime`/`RecordedSize` are carried and stay ZERO this session.
// They are not wired to anything, deliberately: the (mtime, size) pair as
// of REGION GENERATION is captured nowhere in the backend --
// `_render_editor_context` (`managers\context.py`) reads the file with
// `path.read_text()` and never stats it, `RegisteredEditorContext` carries
// only `shown_start`/`shown_end`, and `FillProposal.to_dict()` is seven
// fields with no stat among them. `CheckFileStale` takes `(path, mtime)`
// only -- no size -- so there is nothing to compare a size against either.
//
// `doc.mtime`/`doc.size` are RIGHT HERE and are NOT used for this, which is
// the point: they are the mtime of the read that opened the file, replaced
// from every successful write's reply (autosave rule 2). That is the
// open/last-save moment, not the generation moment, and substituting it is
// precisely the "cache a stat and trust it" error B12's `set_mode` clears
// its pair to avoid and F8's chip staleness repeated one layer up. An
// approximation here would produce both failure directions: a false refusal
// when the region was generated from content newer than the open, and a
// missed refusal when a save refreshed the pair after generation.
//
// The guard ORDER of F9a hazard 1 -- "resolve the SESSION's scope first,
// guard against that, and commit nothing until the guard passes" -- is
// blocked by the same gap, and the honest statement of where is this:
//
//   - This episode's apply makes NO backend call and NO disk write. It
//     splices an in-memory buffer. So there is no project resolution in the
//     apply path to get into the wrong order yet, and nothing here falls
//     back to `_resolve_optional_project` to make one "work".
//   - There is no session scope available to resolve even if there were.
//     "The Editor has no session and therefore no project_id" (protocol.go)
//     -- B30 binds a project to a session and this workspace has none -- and
//     the proposal's own `session_id` is not in hand because Go never
//     receives `FillProposal`.
//   - The scope a target file WAS resolved under is the path-walked one:
//     `openPath` resolves a file's own project from its own path, which is
//     `_resolve_optional_project`'s behaviour and the drift B12's bug #2
//     names.
//
// It becomes load-bearing the moment the applied buffer is SAVED: F13b's
// write goes to `WriteProjectFile`, which `_require_project`s the EDITOR's
// path-resolved `project_id`, so a saved agent-applied change would be
// guarded against whatever `.point\` sits above the file rather than
// against the session that produced the proposal. Not reachable from this
// episode -- there is no producer -- and reachable as soon as F15 adds one.
// Folded into the same deferred note as #7 in the handoff.
type DiffCard struct {
	// path is the absolute path of the file this proposal is for, as the
	// backend resolved it. The apply handler resolves it to an open
	// document by this value; the card never holds the document itself.
	path string
	name string
	// note is the Filler's own one-line `note` field, when it sent one.
	note string

	// startLine and endLine are 1-based inclusive, straight from B24's
	// VALIDATED proposal. Never recomputed here: B24 checked them against
	// the region the model was actually shown, and re-deriving a region
	// from the file at this point would validate against a different
	// moment than the one the model saw.
	startLine int
	endLine   int

	// original is a SNAPSHOT of the target buffer's lines, taken when this
	// card was built. A snapshot rather than a live `*doc`, for two
	// reasons: the card must not reach into a document it does not own,
	// and the diff on screen has to be a statement about one moment --
	// re-reading a buffer per frame would make the diff change under the
	// reader for reasons the reader did not cause.
	original []string

	// baseVersion is the target buffer's Buffer.Version() at that same
	// moment. The apply refuses if it has moved, because the proposal's
	// line numbers describe `original` and nothing else.
	//
	// This is NOT Done-when #7 and is not a substitute for it: it is a
	// fact about the in-memory buffer being spliced, not about the file on
	// disk, and it is checkable here precisely because it needs no stat
	// and no backend call. See the type's header.
	baseVersion uint64

	// scratch holds the PROPOSED RESULT, editable.
	//
	// F09: "`e` opens the proposed result in a scratch editing buffer, not
	// the patch. The diff re-renders live against the original as you edit.
	// Editing a new version is typing; editing a patch is punishment."
	//
	// It is a real `doc`, so the region is edited with the same cursor,
	// selection, auto-pair, indent and undo the editor itself has, and it
	// has no `path`, so `canSave()` is false and it can never be written
	// anywhere by any path.
	//
	// It is also the SINGLE source of truth for both the diff and the
	// apply. The proposal's replacement text is seeded into it once, at
	// construction, and never kept separately -- which is what makes
	// Done-when #5 ("diff apply lands the edited text, not the original")
	// structural rather than a thing the apply has to remember to do.
	scratch *doc

	mode diffCardMode

	// applied and refusal are the card's outcome state. Set by the apply
	// handler, never optimistically by Update.
	applied bool
	refusal string

	// checking is true while the on-disk staleness round trip is in
	// flight (F15). It makes `⏎` a no-op for the duration rather than
	// stacking a second check -- doc.saving's own reason, one layer up.
	checking bool

	// scroll is the first visible diff row, and editRows/diffRows are the
	// pane heights the last render used -- remembered so paging moves by a
	// real page and the clamp counts the page actually on screen, which is
	// `ResultCard`'s own reason for keeping bodyRows.
	scroll   int
	editRows int
	diffRows int

	// cached memoises the diff for one buffer version. Not an
	// optimization: View runs every frame and recomputing an LCS per frame
	// to render the same rows is work with no reader.
	cached     diff.Result
	cachedVer  uint64
	cachedIsOK bool

	// RecordedMtime and RecordedSize are the file's (mtime, size) AS OF
	// REGION GENERATION, and they are POPULATED as of F15.
	//
	// F9b shipped them zero and recorded why at length (the type header
	// below still carries that history, which is worth keeping: it is the
	// record of a guard that was left absent rather than approximated).
	// B36 then made `_render_editor_context` stat the same file descriptor
	// it reads the region from and put the pair on `FillProposal`, and
	// F15's `onFillProposal` fills these from THAT event and from nothing
	// else.
	//
	// Never from `doc.mtime`/`doc.size`. Those describe the open or
	// last-save moment and fail in both directions -- a false refusal when
	// the region was generated from content newer than the open, and a
	// MISSED refusal when a successful save refreshed the pair after
	// generation. The second one is the dangerous direction, because a
	// check that permits the unsafe apply is believed.
	RecordedMtime float64
	RecordedSize  int

	// HasRecordedPair says whether RecordedMtime/RecordedSize describe a
	// real generation-time stat.
	//
	// An explicit flag rather than testing RecordedMtime against zero,
	// which is what this shipped with and which cannot be justified: a
	// zero mtime is a real possible value (a file stamped at the epoch),
	// and a zero SIZE is entirely ordinary (an empty file), so the
	// float's zero value is a sentinel that can collide with the fact it
	// is standing in for. The flag cannot.
	//
	// It is also the honest shape of the question: "did a producer give
	// me a pair" is not a property of the pair's value.
	HasRecordedPair bool

	// SessionID is the session that produced the proposal (F15), taken off
	// the FillProposal envelope.
	//
	// It is what makes the staleness check resolve the right scope: B36's
	// `CheckFileStale` resolves the SESSION's own project via
	// `_attachment_scope` when given a session_id, and falls back to
	// `_resolve_optional_project`'s walk-up from the path when not --
	// which answers a different question and is B12's bug #2. F9a's
	// hazard 1 ("resolve the SESSION's scope first") is why this field
	// exists rather than being derived here from the path.
	SessionID string
}

type diffCardMode int

const (
	// diffReview: reading the diff. ⏎ applies, e edits, d discards.
	diffReview diffCardMode = iota
	// diffEdit: typing in the proposed result, with the diff live below
	// it. ⏎ is a NEWLINE here, not apply -- see editFooter.
	diffEdit
)

// diffApplyMsg asks the Editor workspace to apply a card's current text.
//
// It carries the card POINTER, unlike `staleReloadMsg`'s path, because the
// handler has to mark this exact card applied after the splice returns and
// the card is owned by Root's overlay slot, not by the workspace. The
// precedent is `reload.go` filling in `staleDialog.disk` after a re-read:
// the workspace built the overlay and may complete it.
type diffApplyMsg struct{ card *DiffCard }

// DiffCardOptions is NewDiffCard's input.
//
// A struct rather than eight positional parameters: the four that come off
// B24's proposal and the two that come off the target buffer are easy to
// transpose, and `StartLine`/`EndLine`/`BaseVersion` are all integers.
type DiffCardOptions struct {
	// Path, Name and Note are display and resolution.
	Path string
	Name string
	Note string

	// StartLine, EndLine and Replacement are B24's validated proposal.
	StartLine   int
	EndLine     int
	Replacement string

	// Original and BaseVersion are the target buffer, as of now.
	Original    []string
	BaseVersion uint64

	// RecordedMtime and RecordedSize are the generation-moment pair, from
	// B36's FillProposal fields and from nowhere else, and
	// HasRecordedPair says they are real. SessionID is the session that
	// produced the proposal, so the check resolves that session's scope.
	// See the fields of the same name on DiffCard.
	RecordedMtime   float64
	RecordedSize    int
	HasRecordedPair bool
	SessionID       string
}

// NewDiffCard builds the card from a validated proposal and a snapshot of
// the buffer it is for.
//
// `Original` is COPIED. The caller's slice is a buffer's line list in every
// real case, and retaining it would make the card's "original" change as
// the user types in the file behind it.
func NewDiffCard(o DiffCardOptions) *DiffCard {
	original := make([]string, len(o.Original))
	copy(original, o.Original)

	return &DiffCard{
		path:            o.Path,
		name:            o.Name,
		note:            o.Note,
		startLine:       o.StartLine,
		endLine:         o.EndLine,
		original:        original,
		baseVersion:     o.BaseVersion,
		scratch:         newScratchDoc(o.Replacement, o.Path),
		mode:            diffReview,
		RecordedMtime:   o.RecordedMtime,
		RecordedSize:    o.RecordedSize,
		HasRecordedPair: o.HasRecordedPair,
		SessionID:       o.SessionID,
	}
}

// newScratchDoc is an editable document that is not a file.
//
// `path` is empty, which makes `canSave()` false: a scratch buffer cannot
// be saved, locked, autosaved or reloaded, and that is enforced by the
// field every one of those paths already checks rather than by a new flag.
// `langPath` is used for the lexer only.
//
// Not read-only: it is the thing the user is meant to type in. The
// read-only question belongs to the TARGET document, and the apply handler
// asks it there.
func newScratchDoc(content, langPath string) *doc {
	d := &doc{
		buf:      newLineBuffer(content),
		pairedAt: Pos{Line: -1},
	}
	d.indent = detectIndent(d.buf)
	d.hl = newHighlighter(langPath, d.lineEndingHint())
	d.lang = d.hl.name
	return d
}

// ResultText is what an apply would land: the scratch buffer's text.
//
// Exported because the apply handler is the only caller and lives in
// another file, and because F15 will want to read a card's current text
// without applying it.
func (c *DiffCard) ResultText() string { return c.scratch.buf.Text() }

// Applied reports whether this card's change reached a buffer.
func (c *DiffCard) Applied() bool { return c.applied }

// result is the diff of `original` against the scratch buffer, memoised per
// buffer version.
func (c *DiffCard) result() diff.Result {
	v := c.scratch.buf.Version()
	if !c.cachedIsOK || c.cachedVer != v {
		c.cached = diff.Region(c.original, c.startLine, c.endLine,
			c.scratch.buf.Text(), diff.DefaultContext)
		c.cachedVer, c.cachedIsOK = v, true
	}
	return c.cached
}

// regionRange is the proposal's line range as a buffer position pair.
//
// `to` is the END of the last line rather than the start of the next one,
// and the replacement text carries no trailing newline (Buffer.Text never
// does). That pairing is what leaves the newline after the region intact
// and removes the need for a special case on the file's last line, where
// there is no next line to point at.
func (c *DiffCard) regionRange(b Buffer) (Pos, Pos) {
	from := Pos{Line: c.startLine - 1, Col: 0}
	last := c.endLine - 1
	if last >= b.Lines() {
		last = b.Lines() - 1
	}
	if last < 0 {
		last = 0
	}
	return from, Pos{Line: last, Col: len(b.Runes(last))}
}

// markApplied records the outcome. Called by the apply handler AFTER the
// splice returned and the buffer's version moved -- never by Update.
func (c *DiffCard) markApplied() {
	c.applied = true
	c.refusal = ""
	c.mode = diffReview
}

// refuse records why an apply did not happen. The card stays open and
// stays unapplied: a refusal the user cannot read is a card that looks
// inert.
func (c *DiffCard) refuse(why string) {
	c.applied = false
	c.refusal = why
	c.checking = false
}

// ---------------------------------------------------------------------------
// app.MaximisedOverlay
// ---------------------------------------------------------------------------

// MinSize is LAYOUT.md's "diff card 70×20", which `stale.go`'s read-only
// diffView already uses for the same content shape.
func (c *DiffCard) MinSize() (int, int) { return 70, 20 }

// PreferredSize makes this a MaximisedOverlay for the reason the tray, the
// result card and the diffView are all one: it is a CONTENT surface, and
// code read through a 70-column window wraps lines that are meaningful
// exactly because they are not wrapped.
func (c *DiffCard) PreferredSize(availW, availH int) (int, int) { return availW, availH }

func (c *DiffCard) Title() string {
	if c.applied {
		return "Applied · " + c.name
	}
	return "Proposed change · " + c.name
}

func (c *DiffCard) Update(msg tea.Msg) (app.Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	if c.mode == diffEdit {
		return c, c.editKey(k)
	}
	return c.reviewKey(k)
}

// reviewKey is the card's own three bindings plus scrolling.
//
// # ctrl+z is deliberately NOT bound here
//
// Written down because its absence was read as a bug once (F15's gate
// reported "ctrl+z does nothing" with a card open). Review mode has
// nothing to undo: no edit has been made, the card is a view over a
// proposal, and the buffer behind it is untouched until an apply. The
// undo that matters is the one AFTER an apply, and it belongs to the
// editor -- which is where it lands, in one press, once the card is
// closed (Done-when #6). Binding ctrl+z here to mean "undo the apply"
// would need the card to outlive its own apply and to know what to
// reverse, when `ctrl+z` in the buffer already does exactly that.
//
// Edit mode is different and does bind it: there, ctrl+z undoes typing in
// the SCRATCH buffer. See editKey.
func (c *DiffCard) reviewKey(k tea.KeyMsg) (app.Overlay, tea.Cmd) {
	switch k.String() {
	case "enter":
		if c.applied || c.checking {
			// Already landed, or the on-disk check for this same press is
			// still in flight. Re-applying would splice the same region a
			// second time, against line numbers that no longer describe
			// the buffer.
			return c, nil
		}
		card := c
		return c, func() tea.Msg { return diffApplyMsg{card: card} }

	case "e":
		if c.applied {
			return c, nil
		}
		c.mode = diffEdit
		return c, nil

	case "d", "esc":
		// Discard closes the card. It does not undo an apply that already
		// happened -- that is what ctrl+z is for, in one press (Done-when
		// #6) -- and it deletes nothing.
		return nil, nil

	case "up", "k":
		if c.scroll > 0 {
			c.scroll--
		}
	case "down", "j":
		c.scrollBy(+1)
	case "pgup":
		c.scrollBy(-c.pageRows())
	case "pgdown":
		c.scrollBy(+c.pageRows())
	case "home":
		c.scroll = 0
	case "end":
		c.scrollBy(len(c.result().Rows))
	}
	return c, nil
}

func (c *DiffCard) pageRows() int {
	if c.diffRows > 1 {
		return c.diffRows
	}
	return 10
}

func (c *DiffCard) scrollBy(delta int) {
	c.scroll += delta
	max := len(c.result().Rows) - 1
	if c.scroll > max {
		c.scroll = max
	}
	if c.scroll < 0 {
		c.scroll = 0
	}
}

// editKey routes a keystroke into the scratch buffer.
//
// The mapping is `keys.go`'s bufferKey, less the four bindings that mean
// something else here: `esc` leaves edit mode, and `ctrl+s`, `ctrl+w` and
// the tab-strip keys have no meaning on a buffer that is not a file. Not
// factored out of bufferKey and shared: that method needs `w.docs`,
// `w.active` and `w.textArea`, none of which exist inside an overlay, and
// an extracted version taking (doc, rows) would leave bufferKey a wrapper
// around it for no gain. If a third buffer surface appears, extract then.
//
// `enter` is a NEWLINE here. It is the one place this card diverges from
// F09's footer, and it has to: a text pane in which Return does not insert
// a line is broken, so apply moves to review mode, one `esc` away. The
// footer says so.
func (c *DiffCard) editKey(k tea.KeyMsg) tea.Cmd {
	d := c.scratch
	rows := c.editRows
	if rows < 1 {
		rows = 1
	}

	switch k.String() {
	case "esc":
		c.mode = diffReview
		return nil

	// -- movement -------------------------------------------------------
	case "left":
		d.moveLeft(false)
	case "right":
		d.moveRight(false)
	case "up":
		d.moveVertical(-1, false)
	case "down":
		d.moveVertical(+1, false)
	case "shift+left":
		d.moveLeft(true)
	case "shift+right":
		d.moveRight(true)
	case "shift+up":
		d.moveVertical(-1, true)
	case "shift+down":
		d.moveVertical(+1, true)
	case "ctrl+left":
		d.moveWord(-1, false)
	case "ctrl+right":
		d.moveWord(+1, false)
	case "ctrl+shift+left":
		d.moveWord(-1, true)
	case "ctrl+shift+right":
		d.moveWord(+1, true)
	case "home":
		d.moveHome(false)
	case "end":
		d.moveEnd(false)
	case "shift+home":
		d.moveHome(true)
	case "shift+end":
		d.moveEnd(true)
	case "pgup":
		d.moveVertical(-rows, false)
	case "pgdown":
		d.moveVertical(+rows, false)
	case "ctrl+home":
		d.moveDocStart(false)
	case "ctrl+end":
		d.moveDocEnd(false)

	// -- selection ------------------------------------------------------
	case "ctrl+a":
		d.selectAll()

	// -- indent ---------------------------------------------------------
	case "tab":
		d.indentSelection()
	case "shift+tab":
		d.dedentSelection()

	// -- edit -----------------------------------------------------------
	case "enter":
		d.newline()
	case "backspace":
		d.backspace()
	case "delete":
		d.deleteForward()
	case "ctrl+h":
		d.deleteWordLeft()
	case "ctrl+z":
		// The SCRATCH buffer's undo, not the target's. Undoing an applied
		// diff is ctrl+z in the editor, where the apply landed.
		d.Undo()
	case "ctrl+y", "ctrl+shift+z":
		d.Redo()

	default:
		if k.Type == tea.KeyRunes && !k.Alt {
			for _, r := range k.Runes {
				d.insertRune(r)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Render
// ---------------------------------------------------------------------------

func (c *DiffCard) View(th theme.Theme, w, h int) string {
	textW := w - 2
	if textW < 1 {
		textW = 1
	}
	rows := h - 1
	if rows < 1 {
		rows = 1
	}

	// The row budget, spent in priority order, so the total is EXACTLY
	// `rows` at every height -- including one too short for everything.
	//
	// The footer goes first and is dropped last, because it is the keys:
	// F13b's stale dialog put its button row and its key hint below the
	// fold and both reported symptoms were that. Then the rule, which is
	// only worth a row if content survives above it. The head is trimmed
	// from the FRONT, so the status line -- which carries "applied" and
	// any refusal -- outlives the note.
	head := c.headLines(th, textW)
	avail := rows

	footer := avail >= 1
	if footer {
		avail--
	}
	rule := avail >= 2
	if rule {
		avail--
	}
	maxHead := avail - 1
	if maxHead < 0 {
		maxHead = 0
	}
	if len(head) > maxHead {
		head = head[len(head)-maxHead:]
	}
	body := avail - len(head)
	if body < 0 {
		body = 0
	}

	out := make([]string, 0, rows)
	out = append(out, head...)
	if c.mode == diffEdit {
		out = append(out, c.editPanes(th, textW, body)...)
	} else {
		c.editRows = 0
		c.diffRows = body
		out = append(out, c.diffRowsView(th, textW, body)...)
	}
	if rule {
		out = append(out, th.Style(theme.BorderSubtle).Render(strings.Repeat("─", textW)))
	}
	if footer {
		out = append(out, th.Style(theme.TextDim).Render(
			padCells(truncateCells(c.footer(), textW), textW)))
	}
	return strings.Join(out, "\n")
}

// headLines is the note and the status line, in that order -- the two rows
// above the content. Built as a slice so View can budget and trim them
// rather than three places each knowing how tall the header is, which is
// the duplication `stale.go`'s staleBody records as the cause of its own
// rendering bug.
func (c *DiffCard) headLines(th theme.Theme, textW int) []string {
	var head []string
	if c.note != "" {
		head = append(head, th.Style(theme.TextSecondary).Render(
			padCells(truncateCells("note: "+c.note, textW), textW)))
	}
	if s := c.statusLine(); s != "" {
		role := theme.TextDim
		switch {
		case c.refusal != "":
			role = theme.Error
		case c.applied:
			role = theme.Done
		}
		head = append(head, th.Style(role).Render(padCells(truncateCells(s, textW), textW)))
	}
	return head
}

// statusLine is the one line that says what state this card is in: the
// change's size, or the outcome once there is one.
func (c *DiffCard) statusLine() string {
	switch {
	case c.refusal != "":
		return "not applied — " + c.refusal
	case c.applied:
		return "applied to the buffer — ctrl+z in the editor undoes it in one press"
	}
	r := c.result()
	s := fmt.Sprintf("lines %d-%d   +%d −%d", c.startLine, c.endLine, r.Added, r.Removed)
	if r.Added == 0 && r.Removed == 0 {
		s += "   (the proposal matches the file as it is)"
	}
	if r.Coarse {
		s += "   (region too large for a line-by-line diff)"
	}
	if r.Truncated {
		s += "   (diff truncated)"
	}
	return s
}

func (c *DiffCard) footer() string {
	switch {
	case c.mode == diffEdit:
		return "esc review   ⏎ newline   ctrl+z undo edit   (apply from review)"
	case c.applied:
		// ONE key, not the two this shipped with ("d close   esc close").
		//
		// Both still close -- reviewKey takes `d` and `esc` in the same
		// case, and that is unchanged -- but advertising two bindings for
		// one action asks the reader to look for a difference that is not
		// there. `esc` is the one kept because it is what dismisses every
		// overlay in this app; `d` is F09's own footer key for DISCARD,
		// and there is nothing left to discard once the change has landed.
		// Advertising fewer keys than work is allowed; the reverse is
		// what F04's rule forbids.
		return "esc close"
	case c.checking:
		return "checking the file on disk…"
	case c.refusal != "":
		// The RE-GENERATE OFFER, which F15's Done-when 7 and 7b both
		// require of a refusal ("refused, with re-generate offered").
		// It is a sentence rather than a key because re-generating is
		// not something this card can do: the proposal came from a task
		// against a region at the caret, so the offer is to ask again
		// where asking happens. Naming the real binding keeps F04's rule
		// -- no hint for a key that does nothing here.
		return "d discard   esc close   (re-generate: close, then leader → f at the caret)"
	default:
		return "⏎ apply   e edit   d discard   ↑/↓ scroll"
	}
}

// editPanes is edit mode: the proposed result on top, the live diff below.
//
// Split rather than tabbed, because the whole claim of `e` is that the diff
// re-renders against the original AS YOU EDIT -- which is only true if both
// are on screen at once. The editor gets the larger share; the diff below
// it is the feedback, not the subject.
// The row arithmetic keeps editRows + 1 (the rule) + diffRows == body
// EXACTLY, at every height including the floor. That invariant is not
// decoration: F13b's stale dialog shipped with a declared height two rows
// short of its own output, and both reported symptoms were that one
// number. TestTheCardRendersBothModesAtItsDeclaredSize holds it.
func (c *DiffCard) editPanes(th theme.Theme, textW, body int) []string {
	if body < 3 {
		// No room for a split and a rule between them. The editor pane is
		// the subject, so it takes what there is.
		c.editRows, c.diffRows = body, 0
		c.scratch.ensureVisible(plainIndex(c.scratch), body, textW-c.scratchGutter()-1, 0)
		return c.scratchView(th, textW, body)
	}

	editRows := body / 2
	if editRows < 3 {
		editRows = 3
	}
	if editRows > body-2 {
		editRows = body - 2
	}
	diffRows := body - editRows - 1
	c.editRows, c.diffRows = editRows, diffRows

	c.scratch.ensureVisible(plainIndex(c.scratch), editRows, textW-c.scratchGutter()-1, 0)

	out := c.scratchView(th, textW, editRows)
	out = append(out, th.Style(theme.BorderSubtle).Render(
		padCells(truncateCells("── live diff ", textW), textW)))
	return append(out, c.diffRowsView(th, textW, diffRows)...)
}

func (c *DiffCard) scratchGutter() int { return gutterWidth(c.scratch.buf.Lines()) }

// scratchView renders the editable proposed result, with a caret.
//
// Plain text, not syntax-highlighted, and the reason is specific rather
// than "it was easier": the highlighter IS reachable here (`c.scratch.hl`),
// but painting a caret and a selection over its styled runes is
// `render.go`'s `paintRow`/`sliceCells`, both methods on `Workspace` and
// both needing `w.theme`, the selection range and the focus state. Reusing
// them means either duplicating that logic for a second surface or lifting
// it off `Workspace`, which is a refactor of F13a's shipped render path in
// an episode whose Build list is two Done-whens. Recorded in the handoff
// alongside F9a's own deferred markdown rendering -- the same shape of
// problem and plausibly the same fix.
func (c *DiffCard) scratchView(th theme.Theme, textW, rows int) []string {
	gutter := c.scratchGutter()
	cells := textW - gutter - 1
	if cells < 1 {
		cells = 1
	}

	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		n := c.scratch.top + i
		if n >= c.scratch.buf.Lines() {
			out = append(out, padCells("", textW))
			continue
		}
		num := th.Style(theme.TextDim).Render(
			fmt.Sprintf("%*d ", gutter, n+1))
		out = append(out, num+c.scratchRow(th, n, cells))
	}
	return out
}

// scratchRow renders one line of the scratch buffer from its horizontal
// scroll offset, drawing the caret as a reversed cell.
//
// Per-rune rather than in styled runs -- `sliceCells`'s own measured
// optimisation -- because this pane is at most half a card and is not the
// per-keystroke full-window path that optimisation was for.
func (c *DiffCard) scratchRow(th theme.Theme, line, cells int) string {
	d := c.scratch
	runes := d.buf.Runes(line)
	caretHere := d.cursor.Line == line
	caret := th.Style(theme.TextPrimary).Reverse(true)
	plain := th.Style(theme.CodeText)

	var b strings.Builder
	col, used := 0, 0
	for i := 0; i <= len(runes); i++ {
		s := " "
		if i < len(runes) {
			if runes[i] == '\t' {
				s = strings.Repeat(" ", runeCells('\t', col))
			} else {
				s = string(runes[i])
			}
		} else if !caretHere || d.cursor.Col != i {
			// Past the end of the line, and the caret is not sitting
			// there: nothing left to draw.
			break
		}

		width := runewidthOf(s)
		// Skip whatever is scrolled off to the left, counting cells so a
		// tab or a wide rune cannot be half-skipped.
		if used+width <= d.left {
			used += width
			col += width
			continue
		}
		if used-d.left+width > cells {
			break
		}
		if caretHere && i == d.cursor.Col {
			b.WriteString(caret.Render(s))
		} else {
			b.WriteString(plain.Render(s))
		}
		used += width
		col += width
	}
	return padCells(b.String(), cells)
}

// runewidthOf is the display width of an already-expanded cell string.
// Named rather than inlined so the tab case above reads as one rule.
func runewidthOf(s string) int { return visibleWidth(s) }

// diffRowsView renders the diff itself.
//
// The gutter shows the PROPOSED line number on context and inserted rows
// and the ORIGINAL one on deleted rows, because a deleted line has no
// number in the result and the reader's question about it is which line it
// was. F09's mockup has no deletions to disambiguate; this is the rule it
// implies extended to the case it does not show.
func (c *DiffCard) diffRowsView(th theme.Theme, textW, rows int) []string {
	r := c.result()
	gutter := diffGutterWidth(r)
	out := make([]string, 0, rows)

	for i := 0; i < rows; i++ {
		n := c.scroll + i
		if n >= len(r.Rows) {
			out = append(out, padCells("", textW))
			continue
		}
		row := r.Rows[n]
		mark, num, role := " ", row.New, theme.TextSecondary
		switch row.Op {
		case diff.Insert:
			mark, role = "+", theme.Done
		case diff.Delete:
			mark, num, role = "-", row.Old, theme.Error
		}
		line := fmt.Sprintf("%*d %s %s", gutter, num, mark, row.Text)
		out = append(out, th.Style(role).Render(padCells(truncateCells(line, textW), textW)))
	}
	return out
}

// diffGutterWidth sizes the number column from the largest number that
// will actually be printed, the same way render.go's gutterWidth does for
// the editor's own.
func diffGutterWidth(r diff.Result) int {
	high := 0
	for _, row := range r.Rows {
		if row.Old > high {
			high = row.Old
		}
		if row.New > high {
			high = row.New
		}
	}
	return gutterWidth(high)
}

// ---------------------------------------------------------------------------
// The apply, which is the workspace's and not the card's
// ---------------------------------------------------------------------------

// onDiffApply lands a card's current text in the buffer it names.
//
// This is the ONLY place a DiffCard reaches a document, and it runs in the
// Editor's own Update, where this workspace owns its documents -- the
// separation `stale.go` records for the three-button dialog and the same
// reason: an Overlay's Update runs inside Root's.
//
// # The order, which is the whole point
//
// Every guard runs and every refusal returns BEFORE the splice. The card is
// marked applied only after `ApplyRegion` has returned AND the buffer's
// Version has actually moved. That ordering is F9a's hazard 3 -- mark it
// optimistically and the card says "applied" over a buffer that refused,
// which is F8's chip bug exactly. The post-splice check is not belt and
// braces either: `ApplyRegion` returns nothing and no-ops silently on a
// read-only document, so the version is the only evidence available that it
// did anything.
//
// # What it deliberately does NOT guard
//
// # F15 made this TWO-PHASE, and guard 6 is new
//
// Guards 1-5 below are synchronous and unchanged. Guard 6 -- "did the FILE
// change on disk since the region was generated" -- is F9's Done-when #7,
// which F9b deferred as its Hazard 2 because no generation-time
// (mtime, size) pair existed to compare against. B36 added it;
// `onFillProposal` populates it; this function now asks `CheckFileStale`
// about it and splices nothing until the answer comes back. The splice
// itself moved to `applyNow`.
//
// Still nothing here approximates the pair from `d.mtime`/`d.size`, which
// is what DiffCard's header has always forbidden and why the fields are
// filled from the event instead.
func (w *Workspace) onDiffApply(m diffApplyMsg) tea.Cmd {
	c := m.card
	if c == nil || c.applied {
		return nil
	}

	_, d := w.docIndexByPath(c.path)
	switch {
	case d == nil:
		c.refuse(c.name + " is no longer open")
		return w.say(c.name + " is no longer open — nothing was applied")
	case d.readOnly:
		c.refuse("the buffer is read-only — " + string(d.roReason))
		return w.say(c.name + " is read-only — nothing was applied")
	case d.buf.Version() != c.baseVersion:
		// The proposal's line numbers describe the snapshot this card was
		// built from. This is NOT the on-disk staleness check (#7, see
		// DiffCard's header); it is the in-memory one, and it is checkable
		// here precisely because it needs no stat and no round trip.
		c.refuse("the buffer changed after this proposal was generated")
		return w.say(c.name + " changed since the proposal — nothing was applied")
	}
	if r := c.result(); r.Added == 0 && r.Removed == 0 {
		c.refuse("the proposal matches the file as it is")
		return w.say("nothing to apply — " + c.name + " already matches the proposal")
	}

	// ---- F15: guard 6, the file ON DISK ------------------------------
	//
	// F9's Done-when #7, which F9b deferred as its Hazard 2 and B36
	// unblocked. It is a DIFFERENT question from the version check above,
	// over a different thing: that one is the in-memory buffer being
	// spliced, this one is the bytes on disk. Both have to pass.
	//
	// It is also the one guard here that cannot be answered locally, which
	// is why the apply is two-phase: every synchronous guard runs first and
	// nothing is spliced, then this asks the backend and the splice happens
	// only in the reply, in `applyNow`, with the version guard re-run
	// because the buffer can move during the round trip.
	//
	// Nothing stats the file from Go. `CheckFileStale` is the backend's
	// own comparison, it is where the path guard lives, and B36 taught it
	// `size` and `session_id` for exactly this caller.
	if c.HasRecordedPair {
		c.checking = true
		w.staleCard = c
		payload := map[string]any{
			"path":  c.path,
			"mtime": c.RecordedMtime,
			"size":  c.RecordedSize,
		}
		if c.SessionID != "" {
			// The session that produced the proposal, so the scope
			// resolved is that session's own (B36's `_attachment_scope`
			// branch) rather than a walk-up from the path. F9a's hazard
			// 1, finally with something to call.
			payload["session_id"] = c.SessionID
		}
		w.send(protocol.CmdCheckFileStale, queryStaleCheck, payload)
		return w.say("checking " + c.name + " on disk…")
	}

	// A card with NO recorded pair is one no F15 producer built -- the
	// only constructor that leaves the flag false is a test, or a future
	// caller holding a proposal from somewhere other than B24's event. It
	// applies without the disk guard, and it says so in the log rather
	// than pretending the guard ran.
	w.log.Warn("editor: applying a diff card with no generation-time mtime — "+
		"the on-disk staleness guard did not run", "file", c.name)
	return w.applyNow(c)
}

// applyNow is phase two: every guard has passed, including the one that
// needed a round trip, so the buffer is spliced.
//
// The order inside it is F9b's, unchanged and for F9b's reasons -- probe,
// splice, prove the version moved, THEN mark the card applied. Marking it
// before the splice is proven is F9a's hazard 3 and F8's chip bug: a card
// that looks applied when it was not.
func (w *Workspace) applyNow(c *DiffCard) tea.Cmd {
	c.checking = false
	i, d := w.docIndexByPath(c.path)
	if d == nil {
		c.refuse(c.name + " is no longer open")
		return w.say(c.name + " is no longer open — nothing was applied")
	}

	// The version BEFORE the splice. It is the only evidence available
	// that ApplyRegion did anything: it returns nothing and no-ops
	// silently on a read-only document.
	//
	// Taken as a plain uint64 rather than an `editProbe` as of F15,
	// because the probe's other field (the path) existed only to hand to
	// `autosaveAfter`, which this function no longer calls -- see the
	// block at the end.
	before := d.buf.Version()

	from, to := c.regionRange(d.buf)
	d.ApplyRegion(from, to, c.ResultText())

	if d.buf.Version() == before {
		// ApplyRegion declined and said nothing. No condition reaching
		// here is known -- the guards above cover read-only, and an empty
		// change is refused -- so this is the honest report of an
		// unexpected no-op rather than a silent success.
		c.refuse("the buffer did not change")
		return w.say("the apply did not change " + c.name)
	}

	// The splice landed, so now put the keyboard where its consequences
	// are. Done-when #6 is "applying in the Editor is undone by a SINGLE
	// ctrl+z", and that key reaches `w.docs[w.active]` and only when the
	// buffer pane has the keyboard -- so an apply into a background tab, or
	// with the tree focused, would otherwise leave the change in one place
	// and the undo in another.
	//
	// This is a deliberate divergence from onFile's "opening does not move
	// the keyboard": opening a file is the user asking to SEE something,
	// and applying a change is the user having just changed it. Done after
	// the guards rather than before, so a refused apply leaves the user
	// exactly where they were.
	w.active = i
	w.focus = focusBuffer

	// F9d: the buffer now holds an agent-proposed region, so the session
	// that proposed it is what the next save has to be scoped by --
	// recorded here, where the splice is PROVEN, and not in `onDiffApply`
	// where it was only intended. A refused apply leaves the document
	// with whatever origin it already had.
	//
	// F9a's hazard 1 is "resolve the SESSION's scope first, guard against
	// that, and commit nothing until the guard passes". The guard that
	// matters for the write is not a second round trip from here: it is
	// WHICH PROJECT the write is performed against, which is decided in
	// `_handle_write_project_file` from this value. See save.go.
	if c.SessionID != "" {
		d.agentSession = c.SessionID
	}

	c.markApplied()

	// And the card GETS OUT OF THE WAY.
	//
	// It used to stay open showing "Applied · name", which looks like a
	// confirmation and is actually a trap: an open overlay takes every
	// key, and review mode binds no ctrl+z (see reviewKey) -- so the undo
	// this very function's status line promises could not reach the
	// buffer until the user guessed that they had to close the card
	// first. F9b's Done-when #6 ("applying in the Editor is undone by a
	// SINGLE ctrl+z") was therefore false through the UI while passing at
	// the workspace level, which is where its own test drove it.
	//
	// Nothing is lost by closing: the result is in the buffer, which is
	// the thing the user wanted to see, and the status line below says
	// what happened and names both keys.
	closeCard := func() tea.Msg { return app.CloseWorkspaceOverlayMsg{Overlay: c} }

	// AUTOSAVE IS NOT ARMED HERE, and that is a change from what F9b
	// shipped on this line (`w.autosaveAfter(probe, "test")`).
	//
	// # Why it had to change
	//
	// F15's Done-when 5 is "the file's mtime is unchanged until the user
	// saves", verified by comparing `LastWriteTime` across
	// apply-then-undo. With autosave in its DEFAULT mode (after_delay,
	// 1000ms -- D90), arming the timer here means an agent-proposed region
	// reaches disk one second after the apply with no further keypress at
	// all. That is CLAUDE.md's first hard rule read the wrong way round:
	// "an agent-proposed change reaches disk only through a buffer the
	// user saves" is about an act, and a debounce firing on its own is not
	// one.
	//
	// F9b could not observe this: nothing constructed a DiffCard, so the
	// line never ran. F15 is the first producer, which makes it the first
	// episode where the behaviour exists -- recorded in this episode's
	// completed doc as a defect this episode found rather than as a
	// refactor of a shipped line.
	//
	// What is NOT changed: the apply still marks the buffer dirty (that is
	// `ApplyRegion`'s own splice), so the file is saveable, the status bar
	// says "• modified", the unload dialog pins the workspace, and the
	// very next keystroke arms the timer through `handleKey`'s own probe
	// exactly as it always did. The user is one ctrl+s, or one character,
	// away from the file being written -- they are simply not zero
	// keypresses away.
	return tea.Batch(
		closeCard,
		w.say("applied to "+c.name+" — ctrl+z undoes it in one press, ctrl+s saves it"),
	)
}

// onStaleResult is CheckFileStale's reply: the on-disk half of the guard.
//
// `{"changed": bool}` and nothing else -- no path, no mtime, no echo of
// what was compared -- which is why the request carried the card
// (protocol.go's pendingReq.card).
//
// # The refusal wording is deliberately NOT the buffer one
//
// F15's gate says a 7b that refuses for the wrong reason fails: "check the
// message names the file changing on disk, not the buffer (that is
// Done-when 7, and F9b's baseVersion guard would catch it either way, which
// is exactly how a disk check that never ran could look like it passed)".
// Two different sentences, over two different things, so the card itself is
// the evidence of which guard fired.
func (w *Workspace) onStaleResult(req pendingReq, env protocol.Envelope) tea.Cmd {
	c := req.card
	if c == nil {
		return nil
	}
	var p struct {
		Changed bool `json:"changed"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		// An undecodable answer is an unanswered question, and an
		// unanswered staleness question refuses.
		w.log.Warn("editor: decoding a FileStaleResult", "err", err)
		c.refuse("could not check the file on disk")
		return w.say("not applied — the file on disk could not be checked")
	}
	if p.Changed {
		c.refuse(c.name + " changed on disk after this region was generated")
		w.log.Info("editor: apply refused, file changed on disk",
			"file", c.name, "recorded_mtime", c.RecordedMtime, "recorded_size", c.RecordedSize)
		return w.say(c.name + " changed on disk — nothing was applied")
	}
	return w.applyNow(c)
}

// docIndexByPath is docByPath with the index, which the apply needs in
// order to activate the tab it edited.
func (w *Workspace) docIndexByPath(path string) (int, *doc) {
	if path == "" {
		return -1, nil
	}
	for i, d := range w.docs {
		if d.path == path {
			return i, d
		}
	}
	return -1, nil
}
