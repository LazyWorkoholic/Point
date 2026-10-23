package editor

import (
	"crypto/sha256"
	"path"
	"strings"
)

// wireFile mirrors ProjectManager.read_file's dict exactly, field for
// field, so a diff against backend\managers\project_manager.py stays a
// visual diff.
//
// The last seven fields are B32's additive metadata. This package DISPLAYS
// and CARRIES them; it does not re-derive any of them, and in particular it
// does not look for a BOM in Content -- B32 strips it and reports it in
// Bom, which is that episode's one non-additive change and the reason a
// CRLF+BOM file can round-trip byte-identical using only what the read
// reported.
type wireFile struct {
	Path           string  `json:"path"`
	Content        string  `json:"content"`
	Mtime          float64 `json:"mtime"`
	Size           int     `json:"size"`
	OutsideProject bool    `json:"outside_project"`

	LineEnding         string `json:"line_ending"`
	CRLFCount          int    `json:"crlf_count"`
	LFCount            int    `json:"lf_count"`
	Bom                bool   `json:"bom"`
	TrailingNewline    bool   `json:"trailing_newline"`
	DecodeReplacements int    `json:"decode_replacements"`
	Writable           bool   `json:"writable"`
}

// The line_ending vocabulary, transcribed from describe_bytes(). Plain
// strings for the same reason protocol\types.go keeps event names as
// strings: a value this build does not recognise must render as something
// rather than crash the workspace.
// Three values, and three is all there are: describe_bytes returns "mixed"
// when both styles appear, "crlf" when only CRLF does, and "lf" otherwise.
// A file with no line break at all therefore reports "lf" with both counts
// zero -- it is not a fourth value, and the fact that the file does not end
// in a newline lives in trailing_newline instead. (A "none" constant was
// written here first, for a value the backend cannot produce, and removed
// once describe_bytes was read rather than assumed.)
const (
	lineEndingCRLF  = "crlf"
	lineEndingLF    = "lf"
	lineEndingMixed = "mixed"
)

// readOnlyReason is why a document cannot be edited. Empty means it can.
//
// Each value is a different fact about the file or the environment, and
// they are kept apart rather than collapsed into one bool because the
// status bar has to say WHICH -- "read-only" with no reason is the kind of
// message that gets reported as the editor being broken.
type readOnlyReason string

const (
	roNone readOnlyReason = ""
	// roNoProject: nothing above this file has a .point\ marker, so it
	// cannot take a lock, so it is read-only by construction rather than
	// writable-but-unlockable. This is the task doc's own note 4, and it
	// is the same call _handle_write_project_file already makes on the
	// backend side by requiring a project rather than resolving one
	// optionally.
	roNoProject readOnlyReason = "no project"
	// roNotWritable: B32's `writable` came back false -- the read-only
	// attribute is set on the file.
	roNotWritable readOnlyReason = "file is read-only"
	// roLossyDecode: B32's `decode_replacements` is non-zero, so the
	// content on the wire is not what is on disk -- errors="replace" has
	// already turned bytes into U+FFFD, and saving would write those
	// replacements back over the original. B32's handoff asks for exactly
	// this, and names it as the failure that episode exists to prevent
	// "arriving by the back door".
	roLossyDecode readOnlyReason = "not valid UTF-8"
)

// doc is one open file: the buffer, everything B32 said about its bytes,
// the caret, the viewport, the undo history and the highlighter.
type doc struct {
	// path is the absolute path the backend resolved and echoed back, and
	// rel is it relative to the project root when there is one -- the tab
	// strip and the status bar show rel, every wire call sends path.
	path string
	rel  string

	buf  Buffer
	hist history

	// The metadata, verbatim from the read. mtime and size are what the
	// save sends as expected_mtime; they are stored rather than dropped
	// because re-reading a file to learn its mtime at save time is
	// exactly the race the precondition exists to close.
	//
	// mtime is REPLACED from every successful write's reply, which is the
	// whole of autosave rule 2 ("record Point's own write mtimes") and
	// the reason a save needs no follow-up CheckFileStale -- B32 built
	// the reply to carry the new mtime for exactly this.
	mtime           float64
	size            int
	lineEnding      string
	crlfCount       int
	lfCount         int
	bom             bool
	trailingNewline bool

	// diskHash is a hash of the buffer's text as of the last read, the
	// last successful write, or the last reload -- i.e. of the bytes that
	// are on disk right now, in the one form this package can compare
	// against (Buffer.Text: LF-joined, no BOM, no trailing newline, which
	// is exactly what the save path sends as `content`).
	//
	// # Why a hash of the content and not a version number
	//
	// Because `Version()` cannot answer the question this exists for.
	// Undo is a Splice like any other, so undoing an edit moves the
	// version FORWARD while returning the content to what is on disk. A
	// version comparison therefore reports "changed" for a buffer that is
	// byte-identical to the file, which is what made F15's Done-when 5
	// fail: apply a proposal, press ctrl+z once, and the autosave timer
	// armed by the undo wrote the file back with identical bytes and a
	// brand-new mtime.
	//
	// A hash rather than a retained copy of the text: the comparison runs
	// at most once per autosave tick, and keeping a second full copy of
	// every open file to answer it would be the larger cost.
	diskHash [32]byte

	readOnly bool
	roReason readOnlyReason

	// indent is what Tab and auto-indent INSERT, detected from the file
	// (indentStyle). Independent of tabStop, which is how wide an existing
	// tab DRAWS.
	indent indentStyle

	// lang is the chroma lexer's own name for this file, lowercased, for
	// the status bar. Empty when nothing matched and the plaintext
	// fallback is in use.
	lang string
	hl   *highlighter

	cursor Pos
	anchor Pos
	// selecting is kept rather than derived from cursor != anchor: a
	// collapsed selection the user is still dragging is a real state, and
	// treating it as "no selection" makes a drag that happens to pass back
	// through its own origin lose the selection it had.
	selecting bool

	// desiredCell is the display column vertical movement tries to keep.
	// Without it, walking down past a short line pulls the caret left and
	// it never comes back -- the oldest bug in text editing.
	desiredCell int
	// desiredValid is false after any horizontal move, so the next
	// up/down re-reads the caret's real column instead of an old wish.
	desiredValid bool

	// pairedAt is where auto-pairing last left a closer, so the NEXT
	// keystroke can type through it instead of doubling it. Pos{Line: -1}
	// means "nothing pending", which is why it cannot be the zero value:
	// Pos{0, 0} is a real caret position, and treating it as "none" would
	// make the very first character typed in a file behave differently
	// from every other one.
	pairedAt Pos

	top  int // first visible buffer line
	left int // horizontal scroll, in display cells

	// reveal asks the next frame to scroll the caret into view, and is
	// the fix for a mouse wheel that could not move the viewport.
	//
	// ensureVisible used to run on EVERY frame, which meant any scroll
	// the caret did not ask for was undone before it could be seen: the
	// wheel moved `top`, the frame snapped it straight back to the
	// caret, and the view appeared to refuse to scroll at all. Measured:
	// `top after wheel=3, top after View=0`, five times in a row.
	//
	// So revealing is now something an action REQUESTS. Every caret move
	// and every edit sets it; the wheel deliberately does not, which is
	// what lets the view sit somewhere the caret is not -- the normal
	// state of affairs while reading code.
	reveal bool

	dirty bool

	// ---- F13b: the save and lock half -------------------------------

	// saving is true while a WriteProjectFile for this document is in
	// flight. It stops a second save stacking on the first, which
	// matters most for autosave: without it a slow write plus a fast
	// typist produces N concurrent writes of N different buffer states,
	// and the one that lands last is whichever the backend finished
	// last, not the newest.
	saving bool

	// savedWhileSaving records that the buffer changed while a write was
	// in flight, so the save that lands knows the file is dirty again
	// rather than clearing the marker on content that is already stale.
	savedWhileSaving bool

	// lock is this document's soft lock (B7) state.
	lock lockState
	// lockedBy is the OTHER holder's whole key, when lock is
	// lockHeldByOther. Compared whole-string; split on the last '@' only
	// for display (config.HolderDisplay).
	lockedBy string
	// lockedAge is the other holder's heartbeat age in seconds, as the
	// refusal reported it. A snapshot, not a live clock: it is what the
	// backend said when it said it, and the status bar labels it that
	// way rather than counting up from a number it cannot refresh
	// without another round trip.
	lockedAge float64

	// autosaveGen is this DOCUMENT's own timer generation (C5).
	//
	// Every edit to this document increments it and schedules a tick
	// carrying the new value; a tick whose value no longer matches is a
	// superseded timer and does nothing. That is what makes the debounce
	// per-document: editing file B touches only B's counter, so A's
	// already-pending tick still fires on A's own schedule. A single
	// workspace-level counter would have made every keystroke anywhere
	// cancel every other file's pending save -- which is the
	// idle-notice-latch / theme-propagation / status-expiry failure
	// again, one entity's state standing in for another's.
	autosaveGen int

	// manualSave records how the write currently in flight was started
	// -- ctrl+s or a timer. The `stale` refusal is the only place it is
	// read, and it is the whole difference between opening a modal
	// dialog and printing one line.
	manualSave bool

	// reload says what the ReadProjectFile currently in flight for this
	// document is FOR: replacing the buffer, or supplying [diff] with
	// the bytes on disk. Two intents, one event.
	reload reloadIntent

	// dialog is the open stale dialog for this document, if any. Held
	// so the [diff] re-read has somewhere to deliver, and cleared the
	// moment a button is pressed -- a pointer to a dismissed overlay is
	// how a later reply redraws a dialog the user already closed.
	dialog *staleDialog

	// ---- F9d: the session an agent-applied region came from ---------

	// agentSession is the session that produced the proposal whose
	// region was last applied into this buffer, and it is what the save
	// path sends as `session_id` so the write resolves THAT session's
	// scope (F9a's hazard 1; B36's `_attachment_scope` branch).
	//
	// Empty for every ordinary buffer, and that is the point: a document
	// nobody applied an agent region into has no originating session, so
	// its save resolves exactly as it always has (the Editor's own
	// project when the file is inside the tree, otherwise the backend's
	// walk-up from the file's path). F9d narrows the hazard it is for
	// rather than re-scoping every save in the workspace -- the Editor's
	// own `w.sessionID` exists for a file this workspace merely has
	// open, and sending it for all writes would refuse ordinary saves of
	// files outside the session's project that work today.
	//
	// It is NOT cleared by `ctrl+z`. An undo is a Splice like any other
	// and leaves the content matching disk, where autosave declines
	// (`matchesDisk`) and a manual save writes the same bytes under the
	// same session's scope, which is the correct scope either way. What
	// DOES clear it is a reload: `applyReload` replaces the whole buffer
	// with the bytes on disk, so there is no longer an applied region in
	// it to be scoped by whoever proposed one.
	agentSession string

	// autosaveBlocked stops the timer re-arming for this document until
	// the user does something about it. Set by a refusal autosave cannot
	// resolve on its own -- locked, read-only, stale, too_large, an
	// Error -- because the alternative is a refusal once a second
	// forever, which is exactly what Done-when 11 forbids. Cleared by a
	// manual save attempt, a reload, or a successful write.
	autosaveBlocked bool
}

// lockState is what this editor knows about a document's soft lock.
//
// Four states rather than a bool, because the status bar has to say WHICH
// -- the same reason readOnlyReason is not a bool. "Not asked" and
// "refused" look identical from a `locked == false` check and mean
// opposite things for whether a save will work.
type lockState int

const (
	// lockNone: no lock has been asked for, or one was released. A file
	// opened with no project is permanently here -- it cannot take a
	// lock at all, which is why it is read-only by construction.
	lockNone lockState = iota
	// lockPending: AcquireFileLock is in flight.
	lockPending
	// lockHeld: ours, and being heartbeated.
	lockHeld
	// lockHeldByOther: someone else holds it and their heartbeat was
	// fresh (B7's LOCK_STALE_AFTER_S is 60s). The document opens
	// read-only with the holder named, and take-over is offered once the
	// lock goes stale.
	lockHeldByOther
)

// canSave reports whether a write is worth attempting at all.
//
// It is deliberately NOT the same question as "is this document
// editable": a document held by another holder is editable in the buffer
// and refused on disk, which is the state the status bar's lock segment
// exists to show. Autosave asks this (rule 1); a manual ctrl+s asks it
// too, and says why when the answer is no.
func (d *doc) canSave() bool {
	return !d.readOnly && d.lock != lockHeldByOther && d.path != ""
}

// newDoc builds a document from one ReadProjectFile reply.
func newDoc(f wireFile, projectRoot string, hasProject bool) *doc {
	d := &doc{
		path:            f.Path,
		rel:             relativeTo(projectRoot, f.Path),
		buf:             newLineBuffer(f.Content),
		mtime:           f.Mtime,
		size:            f.Size,
		lineEnding:      f.LineEnding,
		crlfCount:       f.CRLFCount,
		lfCount:         f.LFCount,
		bom:             f.Bom,
		trailingNewline: f.TrailingNewline,
		pairedAt:        Pos{Line: -1},
	}

	switch {
	case !hasProject || f.OutsideProject:
		d.readOnly, d.roReason = true, roNoProject
	case f.DecodeReplacements > 0:
		d.readOnly, d.roReason = true, roLossyDecode
	case !f.Writable:
		d.readOnly, d.roReason = true, roNotWritable
	}

	d.indent = detectIndent(d.buf)
	d.hl = newHighlighter(f.Path, d.lineEndingHint())
	d.lang = d.hl.name
	// The buffer IS what is on disk at this instant.
	d.setDiskBaseline()
	return d
}

// setDiskBaseline records that the buffer now matches the file.
//
// Called from exactly three places, and they are the only three moments
// at which that is true: the read that opens a document (newDoc), a
// successful write that was not overtaken (onWriteSucceeded), and a
// reload that replaced the buffer with the bytes on disk (applyReload).
// Anything else calling it would be asserting a match it has not made.
func (d *doc) setDiskBaseline() {
	d.diskHash = sha256.Sum256([]byte(d.buf.Text()))
}

// matchesDisk reports whether the buffer is byte-identical to the file.
//
// True after an edit and its undo, which is the case this exists for --
// see diskHash. It is a real answer rather than a conservative one: the
// content sent by a save is `Buffer.Text()` and nothing else, so a
// matching hash means a write would produce the same bytes already there.
func (d *doc) matchesDisk() bool {
	return d.diskHash == sha256.Sum256([]byte(d.buf.Text()))
}

// lineEndingHint is the language hint passed to the lexer when the
// filename alone does not match one. It is deliberately empty: chroma
// matches on the filename here, and there is no fenced-code-style language
// tag to fall back on the way chat\codeblock.go has. Kept as a named seam
// so F16 can add one (a shebang, an editorconfig) without changing
// newHighlighter's signature.
func (d *doc) lineEndingHint() string { return "" }

func (d *doc) markDirty() {
	d.dirty = true
	d.desiredValid = false
}

// hasSelection reports a non-empty selection. A collapsed drag is
// `selecting` but not `hasSelection`, which is the distinction every edit
// path needs and no render path does.
func (d *doc) hasSelection() bool { return d.selecting && d.cursor != d.anchor }

func (d *doc) selectionRange() (Pos, Pos) {
	if !d.hasSelection() {
		return d.cursor, d.cursor
	}
	return order(d.anchor, d.cursor)
}

// name is what the tab strip and the status bar call this file.
func (d *doc) name() string {
	if d.rel != "" {
		return path.Base(d.rel)
	}
	return path.Base(strings.ReplaceAll(d.path, "\\", "/"))
}

// relativeTo expresses an absolute path relative to a project root, in the
// POSIX form ListProjectFiles already uses.
//
// Done with string prefixes rather than filepath.Rel because both sides
// come off the wire as whatever the backend's Path str()-ed to, and
// filepath.Rel on Windows will happily return a ..\..\ walk for two paths
// on different volumes -- which is not a relative path a tab strip should
// ever show. An unrelated path simply gets no relative form, and name()
// falls back to the base.
func relativeTo(root, abs string) string {
	if root == "" {
		return ""
	}
	r := strings.ToLower(strings.ReplaceAll(strings.TrimRight(root, "\\/"), "\\", "/"))
	a := strings.ReplaceAll(abs, "\\", "/")
	if !strings.HasPrefix(strings.ToLower(a), r+"/") {
		return ""
	}
	return a[len(r)+1:]
}

// indentStyle is what one press of Tab inserts, and what auto-indent adds
// a level of.
type indentStyle struct {
	// tabs is true when this file indents with hard tabs.
	tabs bool
	// width is the number of spaces per level when tabs is false. Also
	// what backspace-deletes-an-indent-level steps back by.
	width int
}

// unit is one level of indentation as text.
func (s indentStyle) unit() string {
	if s.tabs {
		return "\t"
	}
	return strings.Repeat(" ", s.width)
}

// defaultIndentWidth is used when a file gives no evidence either way --
// a new file, a file with no indented lines, a one-line config.
const defaultIndentWidth = 4

// detectIndent reads the file's own indentation rather than imposing one.
//
// F13 asks for "tab-vs-space detection from the file" and that is the
// whole of it: whichever character more indented lines START with wins,
// and for spaces the width is the commonest positive difference between
// consecutive indent depths -- which is the step the file actually uses,
// not the depth of its deepest line.
//
// Only the first 500 indented lines are sampled. A file's indentation is
// not a property that reveals itself more clearly at line 4,000, and
// scanning 5,000 lines at open time is work the open path does not need to
// do.
func detectIndent(b Buffer) indentStyle {
	const sample = 500

	tabs, spaces := 0, 0
	diffs := map[int]int{}
	prev := -1
	seen := 0

	for i := 0; i < b.Lines() && seen < sample; i++ {
		runes := b.Runes(i)
		if isBlank(runes) {
			continue
		}
		lead := indentOf(runes)
		if lead == "" {
			prev = 0
			continue
		}
		seen++
		if strings.HasPrefix(lead, "\t") {
			tabs++
			prev = -1
			continue
		}
		spaces++
		depth := len(lead)
		if prev > 0 && depth > prev {
			diffs[depth-prev]++
		} else if prev == 0 {
			diffs[depth]++
		}
		prev = depth
	}

	if tabs > spaces {
		return indentStyle{tabs: true, width: tabStop}
	}

	// The width is the MOST COMMON step that the file demonstrates at
	// least twice, with a tie broken toward the smaller.
	//
	// Both halves of that are load-bearing, and the first was found by
	// the test that asserts it:
	//
	//   - Two observations minimum. One indented line proves nothing: a
	//     file whose only indent is eight spaces is indistinguishable
	//     from a four-space file that skipped a level, and guessing 8
	//     would make every later Tab press insert two levels. One sample
	//     is not evidence, so such a file gets the default instead of a
	//     coin flip.
	//
	//   - Most common, not smallest. A four-space file with two
	//     hand-aligned continuation lines two spaces deep would otherwise
	//     be read as a two-space file on the strength of its two
	//     exceptions.
	const minEvidence = 2

	best, bestN := 0, 0
	for w, n := range diffs {
		if n < minEvidence || w > tabStop {
			continue
		}
		if n > bestN || (n == bestN && w < best) {
			best, bestN = w, n
		}
	}
	if best <= 0 {
		best = defaultIndentWidth
	}
	return indentStyle{width: best}
}
