package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// ---------------------------------------------------------------------------
// #1 -- the pending line must be INLINE with what is being typed
// ---------------------------------------------------------------------------

// Reported: while typing an answer, the pane showed
//
//	your name:
//	»> srijan
//
// two rows -- and only collapsed to one line after Enter. A C programmer's
// scanf shows the prompt and the characters on the SAME line the whole
// time, which is what this asserts, live, mid-typing, against a real
// child.
func TestThePendingPromptAndTypingShareOneLineWhileTyping(t *testing.T) {
	w := termWorkspace(t)
	w.projectRoot = `D:\Projects\Point`
	w.term.historyKey = "test"
	w.runTerminalCommand(`uv run python -c "name = input('your name: '); print('hi', name)"`)
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	drainFor(w, 5*time.Second)
	if got := string(w.term.pendingRunes); !strings.Contains(got, "your name:") {
		t.Fatalf("no pending prompt to type into: %q", got)
	}

	// Type, WITHOUT submitting.
	for _, r := range "srijan" {
		w.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	l := layout.Compute(100, 30, layout.State{
		Active: layout.WorkspaceEditor, HeaderAllowed: true,
		TerminalMode: layout.TermOverlay, RightCards: w.Cards(),
	})
	w.termRect = l.Secondary
	frame := stripANSI(w.terminalBox(l.Secondary))
	t.Logf("mid-typing frame:\n%s", frame)

	// Count every row, ignoring the echoed command line -- it quotes the
	// prompt text verbatim and is not the prompt.
	var promptRows, typingRows, sharedRows int
	for _, row := range strings.Split(frame, "\n") {
		if strings.Contains(row, "$ uv run") {
			continue
		}
		hasPrompt := strings.Contains(row, "your name:")
		hasTyping := strings.Contains(row, "srijan")
		if hasPrompt {
			promptRows++
		}
		if hasTyping {
			typingRows++
		}
		if hasPrompt && hasTyping {
			sharedRows++
		}
	}
	if sharedRows != 1 {
		t.Errorf("the prompt and the typing share %d rows, want exactly 1 "+
			"(prompt on %d rows, typing on %d):\n%s",
			sharedRows, promptRows, typingRows, frame)
	}
	// And the prompt must not ALSO appear as its own output row above.
	if promptRows != 1 {
		t.Errorf("the prompt appears on %d rows, want 1 (it is duplicated above the input):\n%s",
			promptRows, frame)
	}

	// And submitting still produces the committed single line.
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit")
	}
	got := strings.Join(w.term.lines, "\n")
	if !strings.Contains(got, "your name: srijan") {
		t.Errorf("the committed line is wrong; buffer:\n%s", got)
	}
}

// With nothing pending, the row is the ordinary prompt -- the inline path
// must not change the idle case.
func TestTheInputRowIsUnchangedWithNothingPending(t *testing.T) {
	w := termWorkspace(t)
	row := stripANSI(w.terminalInputRow(true, "", 60))
	if !strings.HasPrefix(row, "$ ") {
		t.Errorf("idle row = %q, want it to start with `$ `", row)
	}
}

// A prompt longer than the box must keep the CARET visible: the head is
// dropped, not the tail, or you cannot see what you are typing.
func TestALongPromptKeepsTheTypingVisible(t *testing.T) {
	w := termWorkspace(t)
	w.term.running = true
	w.term.setInput("srijan")
	row := stripANSI(w.terminalInputRow(true, strings.Repeat("x", 200), 40))
	if !strings.Contains(row, "srijan") {
		t.Errorf("the typing was cut off by a long prompt: %q", row)
	}
	if visibleWidth(row) > 40 {
		t.Errorf("row is %d cells, want <= 40: %q", visibleWidth(row), row)
	}
}

// ---------------------------------------------------------------------------
// #4 -- the status bar cannot be overflowed by this note
// ---------------------------------------------------------------------------

// The bar is one unboxed line sharing its width with the right-hand
// state, so the note it shows has to be short. The PANE keeps the full
// text, which wraps.
func TestTheCdStatusNoteIsShortAndAscii(t *testing.T) {
	if n := len(statelessDirNoteShort); n > 40 {
		t.Errorf("the status note is %d chars; the bar shares its width with `connected · N task(s) · W×H`", n)
	}
	for _, r := range statelessDirNoteShort {
		if r > 127 {
			t.Errorf("non-ASCII %q in the status note: runewidth and the terminal need not agree about it", r)
		}
	}
	// The long form is what the pane gets, and it says what to do.
	if !strings.Contains(statelessDirNoteText, "same line") {
		t.Error("the pane note no longer explains the workaround")
	}
}
