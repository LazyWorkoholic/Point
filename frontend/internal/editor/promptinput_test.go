package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// F15's gate reported "pasting into the fill prompt ERASES whatever was
// already typed". It never did: the model was intact and the paste
// inserted at the caret. What happened is that the caret jumped to the end
// of a long instruction, the one-line field scrolled to keep it visible,
// and the earlier text went off the left edge with nothing saying so.
//
// Measured, before the fix:
//   model="add a type check for a missing file and a docstring too" left=32
//   view =|... │ ile and a docstring too   |
//
// So these tests pin BOTH halves: the data is spliced, and the display
// admits that it is scrolled.

func TestAPasteInsertsAtTheCaretAndKeepsWhatWasTyped(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	for _, r := range "add " {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(app.PasteMsg{Text: "a type check"})

	if got := ws.prompt.text(); got != "add a type check" {
		t.Errorf("prompt = %q, want the typed text KEPT and the paste appended", got)
	}
	if got := ws.prompt.col; got != len("add a type check") {
		t.Errorf("caret at %d, want the end (%d)", got, len("add a type check"))
	}
}

func TestAPasteInTheMiddleSplicesRatherThanReplaces(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	for _, r := range "abcd" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyLeft})
	ws.Update(tea.KeyMsg{Type: tea.KeyLeft})
	ws.Update(app.PasteMsg{Text: "ZZ"})

	if got := ws.prompt.text(); got != "abZZcd" {
		t.Errorf("prompt = %q, want abZZcd", got)
	}
}

// The display says it is scrolled, so a field showing its own tail cannot
// be read as a field that lost its head.
func TestAScrolledPromptSaysSo(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.Update(app.PasteMsg{Text: "a type check for a missing file and a docstring too"})

	row := stripANSI(ws.prompt.view(ws, 60))
	if !strings.Contains(row, "‹") {
		t.Errorf("row = %q, want a marker saying text is off the left edge", row)
	}
	if ws.prompt.left == 0 {
		t.Fatal("the field did not scroll, so this test proves nothing")
	}
	if got := visibleWidth(row); got != 60 {
		t.Errorf("the row is %d cells, want exactly 60 -- the markers must come "+
			"out of the field's width, not be added to the row", got)
	}

	// Back to the start: the marker flips to the other side.
	for i := 0; i < 60; i++ {
		ws.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	row = stripANSI(ws.prompt.view(ws, 60))
	if !strings.Contains(row, "›") {
		t.Errorf("row = %q, want a marker saying text continues to the right", row)
	}
	if got := visibleWidth(row); got != 60 {
		t.Errorf("the row is %d cells, want exactly 60", got)
	}
}

// One level of undo, for the changes backspace is not a usable answer to.
func TestThePromptUndoesAPasteAndAClear(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	for _, r := range "keep" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	ws.Update(app.PasteMsg{Text: " and fifty more characters pasted in one go"})
	if !strings.HasPrefix(ws.prompt.text(), "keep and fifty") {
		t.Fatalf("prompt = %q", ws.prompt.text())
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "keep" {
		t.Errorf("after ctrl+z prompt = %q, want %q", got, "keep")
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if !strings.HasPrefix(ws.prompt.text(), "keep and fifty") {
		t.Errorf("ctrl+y did not put the paste back: %q", ws.prompt.text())
	}

	// ctrl+u wipes the field, and that is undoable too.
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if ws.prompt.text() != "" {
		t.Fatalf("ctrl+u left %q", ws.prompt.text())
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if !strings.HasPrefix(ws.prompt.text(), "keep and fifty") {
		t.Errorf("ctrl+z did not undo the clear: %q", ws.prompt.text())
	}
}

// The prompt's undo is a real STACK over typed edits, not a single
// snapshot for paste.
//
// The first version was one level and skipped typing entirely, on the
// grounds that backspace is a one-line field's undo. That was the wrong
// call: a typo noticed three words later is not fixable by backspace
// without retyping those words.
func TestThePromptUndoesTypedEditsAsAStack(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()

	typeIn := func(s string) {
		for _, r := range s {
			ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}

	typeIn("alpha")
	ws.Update(app.PasteMsg{Text: " PASTED"})
	typeIn(" omega")
	if got := ws.prompt.text(); got != "alpha PASTED omega" {
		t.Fatalf("prompt = %q", got)
	}

	// One WORD per press, which is the buffer's own rule (undo.go's
	// startsWord, called from here so the two cannot diverge): a run ends
	// where a word begins, and the space before it stays attached to the
	// word in front.
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "alpha PASTED " {
		t.Fatalf("after 1 undo: %q, want the last WORD gone", got)
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "alpha PASTED" {
		t.Fatalf("after 2 undos: %q, want the space gone with it", got)
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "alpha" {
		t.Fatalf("after 3 undos: %q, want the paste gone as ONE step", got)
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "" {
		t.Fatalf("after 4 undos: %q, want the first word gone too", got)
	}
	// Past the bottom does nothing rather than misbehaving.
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "" {
		t.Fatalf("undo past the bottom of the stack: %q", got)
	}

	// Forward again, step for step.
	for i := 0; i < 4; i++ {
		ws.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	}
	if got := ws.prompt.text(); got != "alpha PASTED omega" {
		t.Errorf("after 4 redos: %q, want the whole instruction back", got)
	}
}

// The reported bug: one ctrl+z took back several words, and the faster
// you typed the more it took.
//
// The cause was a coalescing rule with none of the three guards the
// buffer's has -- it merged any unbroken run of the same kind, so the
// length of an undo step was however long the user went without moving
// the caret. Typing speed is not an input to the rule at all now: the
// word boundary decides, exactly as it does in the buffer.
func TestThePromptsUndoStepIsOneWordHoweverFastYouType(t *testing.T) {
	const sentence = "add error handling for a missing configuration file"

	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	// No pauses at all: every keystroke lands in the same instant, which
	// is the condition that used to merge the lot into one step.
	for _, r := range sentence {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := ws.prompt.text(); got != sentence {
		t.Fatalf("prompt = %q", got)
	}

	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "add error handling for a missing configuration " {
		t.Errorf("one ctrl+z left %q -- it should take back the last WORD, "+
			"not several", got)
	}

	// And the whole sentence comes back one word at a time rather than
	// vanishing at once: nine words, so nine more presses empty it.
	for i := 0; i < 20 && ws.prompt.text() != ""; i++ {
		ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	}
	if got := ws.prompt.text(); got != "" {
		t.Errorf("the stack bottomed out at %q", got)
	}
}

// A PAUSE ends a run too, so two sentences typed a second apart are never
// one undo step even if the caret never moved. undoCoalesceGap is the
// buffer's own constant.
func TestAPauseEndsThePromptsUndoRun(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	for _, r := range "abc" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Backdate the last edit past the gap, which is what a pause does.
	ws.prompt.lastAt = ws.prompt.lastAt.Add(-2 * undoCoalesceGap)
	for _, r := range "def" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "abc" {
		t.Errorf("after the pause, one undo left %q, want only the second run gone", got)
	}
}

// A run of typing is ONE undo step, not one per character -- otherwise
// ctrl+z is just backspace with extra ceremony.
// One WORD is one step -- not one letter, which is what backspace is for.
func TestARunOfTypingIsOneUndoStep(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	for _, r := range "hello" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "" {
		t.Errorf("prompt = %q, want the single typed word undone in one press", got)
	}
}

// Moving the caret ends the run, so typing either side of a move is two
// steps -- the rule history.breakGroup follows in the buffer.
func TestMovingTheCaretSplitsTheUndoRun(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	for _, r := range "abc" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyLeft})
	for _, r := range "ZZ" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := ws.prompt.text(); got != "abZZc" {
		t.Fatalf("prompt = %q", got)
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.prompt.text(); got != "abc" {
		t.Errorf("after undo: %q, want only the edit after the caret move undone", got)
	}
}

// A new edit after an undo throws the redo branch away, as it does in the
// buffer.
func TestAnEditAfterAnUndoDropsTheRedoBranch(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "x")
	ws.BeginFill()
	for _, r := range "first" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	for _, r := range "second" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if got := ws.prompt.text(); got != "second" {
		t.Errorf("prompt = %q, want the stale redo branch discarded", got)
	}
}

// A non-proposal reply WRAPS while it streams rather than being cut at the
// pane's edge -- the gate saw the cut as text running past the border, and
// the rest of the sentence was unreachable because the ghost does not
// scroll.
func TestALiteralGhostWrapsWhileItStreams(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	ws.docs[0].cursor = Pos{Line: 2}
	ws.ghost = &ghostText{
		anchorLine: 2,
		literal:    true,
		lines: []string{"I can only change one file at a time, and this request " +
			"names two of them, so I have not changed anything at all."},
	}

	ws.View(l) // records textArea, which the ghost's width comes from
	rows := make([]string, 0, 4)
	for _, r := range ws.ghostRows(ws.textArea.W) {
		rows = append(rows, stripANSI(r))
	}
	if len(rows) < 2 {
		t.Fatalf("the literal ghost rendered as %d row(s); it should have wrapped", len(rows))
	}
	for _, r := range rows {
		if visibleWidth(r) > ws.textArea.W {
			t.Errorf("a wrapped row is %d cells wide, pane content is %d: %q",
				visibleWidth(r), ws.textArea.W, r)
		}
	}
	// And the whole sentence is there.
	joined := strings.Join(rows, " ")
	for _, word := range []string{"only", "two", "anything"} {
		if !strings.Contains(joined, word) {
			t.Errorf("%q is missing from the wrapped ghost: %q", word, joined)
		}
	}
}
