package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// F9b's gate. Done-when #5 ("diff apply lands the EDITED text, not the
// original") and #6 ("applying in the Editor is undone by a SINGLE
// ctrl+z"), plus the three hazards F09a's handoff names -- the guard order,
// the deferred staleness, and the state transition following the buffer
// edit rather than accompanying it.
//
// Nothing in the repo constructs a DiffCard yet (F15 owns the producer, and
// `FillProposal` has no Go decoder), so these tests ARE the verification
// surface for this episode. That is the same position `TestApplyRegionIs
// OneUndoStep` was in at 18a, and for the same reason.

// The file every test here proposes a change to. Line numbers matter, so
// they are written out:
//
//	1  import os
//	2
//	3  def parse_config(path):
//	4      with open(path) as f:
//	5          return f.read()
const cardFile = "import os\n\ndef parse_config(path):\n    with open(path) as f:\n        return f.read()"

// The proposal: rewrite lines 3-4, inserting a guard clause.
const cardReplacement = "def parse_config(path):\n    if not path.exists():\n        raise ConfigError(path)\n    with open(path) as f:"

// cardWorkspace is a workspace with cardFile open and a DiffCard built
// against it, as the producer F15 will add would have built one.
func cardWorkspace(t *testing.T) (*Workspace, *DiffCard) {
	t.Helper()
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, testDoc(t, cardFile))

	d := ws.docs[0]
	return ws, NewDiffCard(DiffCardOptions{
		Path:        d.path,
		Name:        d.name(),
		Note:        "guard against a missing file",
		StartLine:   3,
		EndLine:     4,
		Replacement: cardReplacement,
		Original:    bufferLines(d.buf),
		BaseVersion: d.buf.Version(),
	})
}

func bufferLines(b Buffer) []string {
	out := make([]string, b.Lines())
	for i := range out {
		out[i] = b.Line(i)
	}
	return out
}

// apply drives one `enter` in review mode the way Root and the workspace
// would: the card returns a command, the command yields a diffApplyMsg, and
// the WORKSPACE handles it. Going through both halves is the point -- the
// card applying the change itself is the thing this design forbids.
func apply(t *testing.T, ws *Workspace, c *DiffCard) {
	t.Helper()
	_, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter in review mode produced no command")
	}
	msg, ok := cmd().(diffApplyMsg)
	if !ok {
		t.Fatalf("enter produced %T, want diffApplyMsg", cmd())
	}
	ws.Update(msg)
}

// typeInto sends runes to a card in edit mode.
func typeInto(c *DiffCard, s string) {
	for _, r := range s {
		c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// ---------------------------------------------------------------------------
// Done-when #5 — the apply lands the EDITED text
// ---------------------------------------------------------------------------

// "Diff apply lands the edited text, not the original."
// "Fails if: the original is applied instead of your edit."
func TestApplyLandsTheEditedTextNotTheProposal(t *testing.T) {
	ws, c := cardWorkspace(t)

	// `e` opens the proposed RESULT, not the patch.
	if _, cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}); cmd != nil {
		t.Error("e produced a command; it only changes mode")
	}
	if c.mode != diffEdit {
		t.Fatal("e did not enter edit mode")
	}
	if got := c.scratch.buf.Text(); got != cardReplacement {
		t.Fatalf("the scratch buffer holds %q, want the proposed result", got)
	}

	// Edit it: change ConfigError to MyError on the scratch buffer's
	// line 2 (the `raise` line).
	c.scratch.cursor = Pos{Line: 2, Col: len("        raise ")}
	c.scratch.anchor = c.scratch.cursor
	for range "Config" {
		c.Update(tea.KeyMsg{Type: tea.KeyDelete})
	}
	typeInto(c, "My")

	edited := c.scratch.buf.Text()
	if !strings.Contains(edited, "raise MyError(path)") {
		t.Fatalf("the edit did not land in the scratch buffer: %q", edited)
	}

	c.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to review
	apply(t, ws, c)

	got := ws.docs[0].buf.Text()
	if !c.Applied() {
		t.Fatalf("the card was not marked applied; refusal = %q", c.refusal)
	}
	if !strings.Contains(got, "raise MyError(path)") {
		t.Errorf("the buffer did not get the EDITED text:\n%s", got)
	}
	if strings.Contains(got, "ConfigError") {
		t.Errorf("the ORIGINAL proposal was applied instead of the edit:\n%s", got)
	}

	// And the rest of the file is untouched -- the apply replaced exactly
	// the lines the proposal named.
	want := "import os\n\n" +
		"def parse_config(path):\n" +
		"    if not path.exists():\n" +
		"        raise MyError(path)\n" +
		"    with open(path) as f:\n" +
		"        return f.read()"
	if got != want {
		t.Errorf("buffer =\n%s\n\nwant\n%s", got, want)
	}
}

// The scratch buffer is the ONE source for both the diff and the apply, so
// the diff on screen is always a statement about what an apply would land.
// This is what makes #5 structural rather than remembered.
func TestTheDiffRerendersAgainstTheOriginalAsYouEdit(t *testing.T) {
	_, c := cardWorkspace(t)
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})

	before := c.result()
	if before.Added != 2 || before.Removed != 0 {
		t.Fatalf("the proposal's diff is +%d −%d, want +2 −0", before.Added, before.Removed)
	}

	// Add a line to the proposed result.
	c.scratch.moveDocEnd(false)
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeInto(c, "    # a comment")

	after := c.result()
	if after.Added != 3 {
		t.Errorf("after typing, Added = %d, want 3", after.Added)
	}
	var found bool
	for _, row := range after.Rows {
		if strings.Contains(row.Text, "# a comment") {
			found = true
		}
	}
	if !found {
		t.Error("the typed line is not in the re-rendered diff")
	}
}

// ---------------------------------------------------------------------------
// Done-when #6 — one ctrl+z
// ---------------------------------------------------------------------------

// "Applying in the Editor is undone by a single ctrl+z."
// "Fails if: undo takes several presses."
//
// Driven through the WORKSPACE's own key handling rather than by calling
// d.Undo(), because "a single ctrl+z" is a claim about the keystroke.
func TestApplyingIsUndoneBySingleCtrlZ(t *testing.T) {
	ws, c := cardWorkspace(t)
	apply(t, ws, c)

	if got := ws.docs[0].buf.Text(); got == cardFile {
		t.Fatal("the apply changed nothing")
	}

	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})

	if got := ws.docs[0].buf.Text(); got != cardFile {
		t.Errorf("after ONE ctrl+z the buffer is\n%s\n\nwant the original\n%s", got, cardFile)
	}
}

// An edited apply is still ONE undo step -- the edit happened in the
// scratch buffer, which has its own history, so the target sees a single
// splice however much typing produced it.
func TestAnEditedApplyIsAlsoOneUndoStep(t *testing.T) {
	ws, c := cardWorkspace(t)
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	c.scratch.moveDocEnd(false)
	typeInto(c, "  # trailing")
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	apply(t, ws, c)

	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.docs[0].buf.Text(); got != cardFile {
		t.Errorf("one ctrl+z left\n%s", got)
	}
	// And nothing further to undo from the apply.
	if ws.docs[0].Undo() {
		t.Error("the apply left more than one undo step")
	}
}

// ctrl+z has to reach the document the apply landed in, which is why the
// apply activates it. A proposal for a background tab would otherwise put
// the change in one buffer and the undo in another.
func TestApplyActivatesTheDocumentItEdited(t *testing.T) {
	ws, c := cardWorkspace(t)
	target := ws.docs[0]

	// Open a second document and make it active, as a user reading
	// something else would have.
	other := newDoc(wireFile{
		Path: "C:\\p\\other.py", Content: "other = 1",
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true)
	ws.docs = append(ws.docs, other)
	ws.active = 1

	apply(t, ws, c)

	if ws.active != 0 {
		t.Errorf("active = %d, want 0 (the document the apply edited)", ws.active)
	}
	if other.buf.Text() != "other = 1" {
		t.Errorf("the apply touched the wrong document: %q", other.buf.Text())
	}

	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if target.buf.Text() != cardFile {
		t.Error("ctrl+z did not reach the document the apply landed in")
	}
}

// ---------------------------------------------------------------------------
// Hazard 3 — the state transition FOLLOWS the buffer edit
// ---------------------------------------------------------------------------

// F9a hazard 3: "Mark it applied only after the edit returns. Optimistic
// marking reproduces F8's chip bug precisely: a card that looks applied
// when it was not."
//
// A read-only target is the available way to make the splice refuse:
// ApplyRegion returns silently on one.
func TestARefusedApplyLeavesTheCardUnapplied(t *testing.T) {
	ws, c := cardWorkspace(t)
	ws.docs[0].readOnly, ws.docs[0].roReason = true, roNotWritable

	apply(t, ws, c)

	if c.Applied() {
		t.Error("the card says applied over a read-only buffer")
	}
	if c.refusal == "" {
		t.Error("the card refused without saying why")
	}
	if got := ws.docs[0].buf.Text(); got != cardFile {
		t.Errorf("the buffer changed despite being read-only:\n%s", got)
	}
}

// Pressing enter must not itself mutate anything: the card hands back a
// message and the workspace does the work. An Overlay's Update runs inside
// Root's, where it does not own the documents.
func TestTheCardDoesNotTouchTheBufferFromItsOwnUpdate(t *testing.T) {
	ws, c := cardWorkspace(t)

	_, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := ws.docs[0].buf.Text(); got != cardFile {
		t.Fatal("the card edited the buffer from its own Update")
	}
	if c.Applied() {
		t.Error("the card marked itself applied before the workspace applied anything")
	}

	ws.Update(cmd().(diffApplyMsg))
	if !c.Applied() {
		t.Error("the workspace applied but did not mark the card")
	}
}

// A second enter is a no-op. Re-applying would splice the same region again
// against line numbers that no longer describe the buffer.
func TestApplyingTwiceIsRefused(t *testing.T) {
	ws, c := cardWorkspace(t)
	apply(t, ws, c)
	afterFirst := ws.docs[0].buf.Text()

	if _, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		ws.Update(cmd().(diffApplyMsg))
	}
	if got := ws.docs[0].buf.Text(); got != afterFirst {
		t.Errorf("a second apply changed the buffer:\n%s", got)
	}
}

// The in-memory guard: the proposal's line numbers describe the snapshot
// the card was built from, so a buffer that moved since is refused.
//
// This is NOT Done-when #7 and must not be read as it: #7 is the file
// changing ON DISK, which needs the (mtime, size) pair as of region
// generation and is deferred. This one needs no stat.
func TestApplyIsRefusedWhenTheBufferMovedSinceTheProposal(t *testing.T) {
	ws, c := cardWorkspace(t)

	ws.docs[0].cursor = Pos{Line: 0, Col: 0}
	ws.docs[0].insertRune('#')

	apply(t, ws, c)

	if c.Applied() {
		t.Error("applied against a buffer that had moved")
	}
	if !strings.Contains(c.refusal, "changed") {
		t.Errorf("refusal = %q, want it to name the change", c.refusal)
	}
}

// A proposal that matches the file is refused as nothing to do, rather than
// spliced as an identical no-op that the version check would then report as
// a mystery failure.
func TestApplyingAnUnchangedProposalIsRefusedWithAReason(t *testing.T) {
	ws, _ := cardWorkspace(t)
	d := ws.docs[0]
	c := NewDiffCard(DiffCardOptions{
		Path: d.path, Name: d.name(),
		StartLine: 3, EndLine: 4,
		// The region exactly as it already is.
		Replacement: "def parse_config(path):\n    with open(path) as f:",
		Original:    bufferLines(d.buf),
		BaseVersion: d.buf.Version(),
	})

	apply(t, ws, c)

	if c.Applied() {
		t.Error("an unchanged proposal was applied")
	}
	if !strings.Contains(c.refusal, "matches") {
		t.Errorf("refusal = %q, want it to say the proposal matches", c.refusal)
	}
	if got := d.buf.Text(); got != cardFile {
		t.Errorf("the buffer changed:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// Hazard 2 / #7 — deferred, and deliberately NOT approximated
// ---------------------------------------------------------------------------

// The (mtime, size) pair is carried and stays zero, and nothing in the card
// reads `doc.mtime`. This test is the tripwire for someone "finishing" #7
// by wiring the open-time stat into it, which is the error F9a hazard 2,
// B12's set_mode and F8's chip staleness all record.
//
// When #7 is really built, this test is the one to change -- deliberately,
// with the backend's generation-time pair arriving through
// DiffCardOptions.
func TestTheStalenessPairIsCarriedAndUnpopulated(t *testing.T) {
	ws, c := cardWorkspace(t)

	if c.RecordedMtime != 0 || c.RecordedSize != 0 {
		t.Errorf("RecordedMtime/Size = %v/%v, want 0/0 this episode",
			c.RecordedMtime, c.RecordedSize)
	}

	// The target has a real mtime from its read; the card must not have
	// borrowed it.
	ws.docs[0].mtime, ws.docs[0].size = 1791623519.07509, 4096
	if c.RecordedMtime == ws.docs[0].mtime {
		t.Error("the card took the document's open-time mtime")
	}

	// And an apply is NOT gated on it: #7 is absent, not half-built, so a
	// proposal applies with the pair at zero.
	apply(t, ws, c)
	if !c.Applied() {
		t.Errorf("the apply was gated on the unpopulated pair: %q", c.refusal)
	}
}

// ---------------------------------------------------------------------------
// The scratch buffer is not a file
// ---------------------------------------------------------------------------

// CLAUDE.md's first hard rule, structurally: the scratch buffer has no
// path, so every save, lock, autosave and reload path -- all of which check
// exactly this -- declines it. There is no WriteProjectFile in this episode
// and no disk write on any agent path.
func TestTheScratchBufferCanNeverBeSaved(t *testing.T) {
	_, c := cardWorkspace(t)
	if c.scratch.path != "" {
		t.Errorf("the scratch buffer has a path: %q", c.scratch.path)
	}
	if c.scratch.canSave() {
		t.Error("canSave() is true for a scratch buffer")
	}
	if c.scratch.readOnly {
		t.Error("the scratch buffer is read-only; it is the thing being typed in")
	}
}

// ---------------------------------------------------------------------------
// It renders
// ---------------------------------------------------------------------------

// The card is an app.MaximisedOverlay at LAYOUT.md's "diff card 70×20",
// and both modes render the rows they declare -- the defect that shipped in
// F13b's stale dialog (MinSize two rows short of its own output) caught
// here by construction rather than at the gate.
func TestTheCardRendersBothModesAtItsDeclaredSize(t *testing.T) {
	var _ app.MaximisedOverlay = (*DiffCard)(nil)

	_, c := cardWorkspace(t)
	w, h := c.MinSize()
	if w != 70 || h != 20 {
		t.Errorf("MinSize = %dx%d, want 70x20 (LAYOUT.md)", w, h)
	}

	for _, mode := range []struct {
		name string
		set  func()
	}{
		{"review", func() { c.mode = diffReview }},
		{"edit", func() { c.mode = diffEdit }},
		{"applied", func() { c.mode = diffReview; c.markApplied() }},
		{"refused", func() { c.refuse("because") }},
	} {
		mode.set()
		// Every height from well below the floor up past it: the row
		// count must equal what View was given, with no mode and no
		// height able to overrun it. This is the defect F13b's stale
		// dialog shipped (a declared height two rows short of its own
		// output) held by construction rather than found at a gate.
		for _, height := range []int{3, 4, 5, 6, 7, 8, h - 1, h, h + 11} {
			got := strings.Split(c.View(testTheme(), w, height), "\n")
			if len(got) != height-1 {
				t.Errorf("%s mode at h=%d rendered %d rows, want %d",
					mode.name, height, len(got), height-1)
			}
			for i, row := range got {
				if n := visibleWidth(row); n != w-2 {
					t.Errorf("%s mode at h=%d: row %d is %d cells, want %d",
						mode.name, height, i, n, w-2)
				}
			}
		}
	}
}

// The diff renders the proposal's own numbering, with the inserted lines
// marked -- F09's mockup, which is the one picture of this card that
// exists.
func TestTheDiffRendersMarkedRowsInTheProposedNumbering(t *testing.T) {
	_, c := cardWorkspace(t)
	rows := c.diffRowsView(testTheme(), 70, 10)

	var plain []string
	for _, r := range rows {
		if s := strings.TrimRight(stripANSI(r), " "); s != "" {
			plain = append(plain, s)
		}
	}
	// The gutter is render.go's own gutterWidth, so the diff's numbers line
	// up with the editor's behind it. Two spaces follow it: the +/-/space
	// mark, then the separator.
	want := []string{
		"   1   import os",
		"   2",
		"   3   def parse_config(path):",
		"   4 +     if not path.exists():",
		"   5 +         raise ConfigError(path)",
		"   6       with open(path) as f:",
		"   7           return f.read()",
	}
	if len(plain) != len(want) {
		t.Fatalf("rendered %d rows:\n%s", len(plain), strings.Join(plain, "\n"))
	}
	for i := range want {
		if strings.TrimRight(plain[i], " ") != strings.TrimRight(want[i], " ") {
			t.Errorf("row %d = %q, want %q", i, plain[i], want[i])
		}
	}
}

// Esc and d close the card; neither undoes an apply that already happened.
func TestEscAndDCloseTheCard(t *testing.T) {
	for _, key := range []string{"esc", "d"} {
		_, c := cardWorkspace(t)
		var msg tea.KeyMsg
		if key == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}
		}
		next, _ := c.Update(msg)
		if next != nil {
			t.Errorf("%s did not close the card", key)
		}
	}
}

// A refused apply must not move the user: the tab they were reading stays
// active and the keyboard stays where it was. The activation exists to make
// ctrl+z reach the change, so it belongs after the change, not before it.
func TestARefusedApplyDoesNotMoveTheUser(t *testing.T) {
	ws, c := cardWorkspace(t)
	ws.docs[0].readOnly, ws.docs[0].roReason = true, roNotWritable

	other := newDoc(wireFile{
		Path: "C:\\p\\other.py", Content: "other = 1",
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
	}, "C:\\p", true)
	ws.docs = append(ws.docs, other)
	ws.active, ws.focus = 1, focusTree

	apply(t, ws, c)

	if ws.active != 1 {
		t.Errorf("a refused apply switched the active tab to %d", ws.active)
	}
	if ws.focus != focusTree {
		t.Error("a refused apply moved the keyboard")
	}
}

// ...and a successful one puts the keyboard on the buffer, so that the
// single ctrl+z Done-when #6 promises actually reaches it.
func TestASuccessfulApplyFocusesTheBuffer(t *testing.T) {
	ws, c := cardWorkspace(t)
	ws.focus = focusTree

	apply(t, ws, c)

	if ws.focus != focusBuffer {
		t.Error("the apply left the keyboard off the buffer it changed")
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.docs[0].buf.Text(); got != cardFile {
		t.Errorf("ctrl+z straight after the apply did not undo it:\n%s", got)
	}
}
