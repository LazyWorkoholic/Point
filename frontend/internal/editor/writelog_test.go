package editor

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The gate's write detector, pinned to the string the code actually logs.
//
// gate_f15.ps1 greps frontend.log for these two literals. They used to be
// grepped for a string nothing ever wrote ("WriteProjectFile", which only
// appeared in the code, never in the log), so the assertion passed in every
// run -- including runs whose mtime check in the same step FAILED. An
// assertion that cannot fail is worse than none, because it is read as
// evidence; three rounds of claimed fixes were certified by it.
//
// These constants are the contract. If a rename breaks it, THIS test fails
// rather than the gate going quietly blind again.
const (
	gateWriteLiteral = "editor: WriteProjectFile sent"
	gateDirtyLiteral = "editor: buffer became dirty"
)

// loggedWorkspace is a workspace whose log output the test can read.
func loggedWorkspace(t *testing.T, content string) (*Workspace, *bytes.Buffer, *[]sentQuery) {
	t.Helper()
	var out bytes.Buffer
	ws := New(Options{
		Theme: testTheme(),
		Log:   slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	var sent []sentQuery
	ws.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmd, kind, payload})
	}
	ws.writeFn = func(string, string, map[string]any) error { return nil }
	ws.projectID, ws.projectRoot = "p1", `C:\p`
	ws.holder = "tester@1"
	ws.autosaveMode = config.AutosaveAfterDelay
	ws.autosaveDelay = time.Millisecond
	ws.docs = append(ws.docs, testDoc(t, content))
	ws.focus = focusBuffer
	ws.onScreen = true
	return ws, &out, &sent
}

// A real write logs the literal the gate greps for -- and names the cause
// of the edit behind it.
func TestARealWriteIsLoggedWithTheStringTheGateGrepsFor(t *testing.T) {
	ws, logged, sent := loggedWorkspace(t, "def f():\n    pass")

	// One stray keystroke: the ordinary way a file in a TEXT EDITOR gets
	// dirty, and the thing the gate has been unable to see.
	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	drain(t, ws, cmd)

	if _, ok := lastQueryOf(sent, protocol.CmdWriteProjectFile); !ok {
		t.Fatal("no write went out, so this test is not exercising the write path")
	}

	text := logged.String()
	if !strings.Contains(text, gateWriteLiteral) {
		t.Errorf("a real write produced no %q line, so the gate's detector is blind:\n%s",
			gateWriteLiteral, text)
	}
	if !strings.Contains(text, gateDirtyLiteral) {
		t.Errorf("nothing logged what dirtied the buffer:\n%s", text)
	}
	// And the cause is identifiable, which is the whole point of the line.
	if !strings.Contains(text, `cause="key c"`) {
		t.Errorf("the dirty cause does not name the key:\n%s", text)
	}
	if !strings.Contains(text, "reason=autosave") {
		t.Errorf("the write does not say it was an autosave rather than the user asking:\n%s", text)
	}
	t.Logf("the two lines the gate now greps for:\n%s", relevantLines(text))
}

// And the paths that must NOT write log nothing of the kind -- the same
// detector, used in the negative, which is how the gate uses it.
func TestTheGhostAndUndoPathsLogNoWrite(t *testing.T) {
	ws, logged, _ := loggedWorkspace(t, "def f():\n    pass")

	// A whole fill: prompt, paste the instruction in, stream, proposal,
	// apply, undo.
	ws.BeginFill()
	ws.Update(app.PasteMsg{Text: "add a type check"})
	ws.submitPrompt()
	stream(ws, `{"replacement": "def f():\n    return 1"`)
	taskDone(ws)
	ws.Update(proposalEnvelope(ws, 1, 2, "def f():\n    return 1", 1.5, 2))
	c := ws.card
	applyCard(t, ws, c)
	answerStale(ws, c, false)
	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	drain(t, ws, cmd)

	if text := logged.String(); strings.Contains(text, gateWriteLiteral) {
		t.Errorf("the fill/apply/undo path wrote to disk:\n%s", relevantLines(text))
	}
}

func relevantLines(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, gateWriteLiteral) || strings.Contains(line, gateDirtyLiteral) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// ---------------------------------------------------------------------------
// Step 3's sequence, sub-action by sub-action, with the CORRECTED detector
// ---------------------------------------------------------------------------

// The isolation F15's gate asked for, done through the real Root: every
// sub-action of the gate's Step 3 in order, asserting after each one that
// nothing was written and recording what (if anything) dirtied the buffer.
//
// The conclusion this test establishes is a negative: no part of the
// ghost/fill/switch/card sequence writes. What DOES write is covered by
// the test below it.
func TestNoSubActionOfStepThreeWrites(t *testing.T) {
	const original = "def f():\n    pass"
	rig := newRootRig(t, original, 10*cmdPatience)

	step := func(name string) {
		t.Helper()
		if q, wrote := rig.wroteAnything(); wrote {
			t.Fatalf("%s wrote to disk: %v", name, q.payload)
		}
		if got := rig.ed.docs[0].buf.Text(); got != original {
			t.Fatalf("%s changed the buffer to %q", name, got)
		}
	}

	step("opening the Editor")

	rig.leader('f')
	step("opening the fill prompt")

	for _, r := range "add a type check" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	step("typing the instruction")

	rig.send(app.PasteMsg{Text: " and a docstring"})
	step("PASTING into the prompt")

	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	step("submitting")

	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `{"replacement": "def f():\n`})
	step("the ghost streaming")

	rig.leader('z')
	step("switching away to Chat")

	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `    return 1"`})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	step("the task finishing while backgrounded")

	rig.envelope(protocol.EvFillProposal, map[string]any{
		"proposal": map[string]any{
			"file": "sample.py", "start_line": 1, "end_line": 2,
			"replacement": "def f():\n    return 1", "note": "n",
			"shown_start": 1, "shown_end": 2, "mtime": 1.5, "size": 2,
		},
	})
	step("the proposal arriving while backgrounded")

	rig.leader('e')
	step("switching back to the Editor")

	// `d` discards the card. It is a BARE LETTER, which is only safe
	// because the card is on screen to claim it -- see the test below.
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	step("discarding the card")

	// And a second fill, cancelled mid-stream with esc.
	rig.leader('f')
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `{"replacement": "a`})
	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	step("cancelling a fill mid-stream")

	if rig.ed.docs[0].dirty {
		t.Error("the document is dirty after a sequence that edited nothing")
	}
}

// And the thing that DOES write, which is the answer the gate's own
// instructions have been hiding.
//
// This is not a defect in the fill path. It is what a TEXT EDITOR does: a
// key no surface claimed is a character typed into the file, and autosave
// then persists it, correctly. The gate's step lists instruct BARE LETTER
// keys -- "press c to send it to Chat", "press d to DISCARD it" -- and
// each of those is only safe while the surface that claims it is actually
// on screen. Pressed a moment early, or after the surface has closed, they
// are edits.
//
// `c` is called out specifically because it is the one the gate asks for
// while a panel may or may not be open, and because it is what the new
// dirty-cause log line named in this episode's own verification.
func TestABareLetterWithNoSurfaceOpenIsAnEditAndIsLogged(t *testing.T) {
	ws, logged, sent := loggedWorkspace(t, "def f():\n    pass")

	// No panel, no prompt, no card: the buffer has the keyboard.
	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	drain(t, ws, cmd)

	if got := ws.docs[0].buf.Text(); !strings.HasPrefix(got, "c") {
		t.Fatalf("buffer = %q, want the keystroke inserted", got)
	}
	if _, ok := lastQueryOf(sent, protocol.CmdWriteProjectFile); !ok {
		t.Fatal("autosave did not write a genuinely edited file, which it should")
	}
	if text := logged.String(); !strings.Contains(text, `cause="key c"`) {
		t.Errorf("the log does not name the key that caused it:\n%s", relevantLines(text))
	}

	// With the panel open the same key is CLAIMED and edits nothing --
	// which is why the instruction is safe when the surface is up.
	ws2, logged2, sent2 := loggedWorkspace(t, "def f():\n    pass")
	ws2.BeginExplain()
	if ws2.panel == nil {
		t.Fatal("no panel")
	}
	_, cmd2 := ws2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	drain(t, ws2, cmd2)

	if got := ws2.docs[0].buf.Text(); got != "def f():\n    pass" {
		t.Errorf("buffer = %q: the panel did not claim the key", got)
	}
	if _, ok := lastQueryOf(sent2, protocol.CmdWriteProjectFile); ok {
		t.Error("a claimed key still wrote")
	}
	if strings.Contains(logged2.String(), gateWriteLiteral) {
		t.Error("a claimed key logged a write")
	}
}
