package editor

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// F14's own tests, and the reason they run REAL child processes rather
// than feeding synthetic bytes into feed().
//
// The episode shipped with a unit-testable feed() and a passing suite, and
// every single line of output was still blank in use: feed() cleared the
// pending line on the CR of every CRLF, and nothing in the suite ever put
// a real Windows line ending through it. A synthetic test written by the
// same person who wrote the bug agrees with the bug. So the tests below
// that matter most start an actual `powershell` and assert on what comes
// back out of the real pipe, which is the only input that could have
// caught it.
//
// powershell.exe is part of base Windows and this project is
// Windows-only (CLAUDE.md), so it is not a new dependency.

// termWorkspace is a workspace with a terminal pane and nothing else --
// no docs, which is also a state the pane has to work in.
func termWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	// A REAL directory, unlike the rest of this package's tests: these
	// start actual processes, and Cmd.Dir pointing at the usual fake
	// "C:\p" fails every Start() with chdir -- which is how the
	// start-failure path in runTerminalCommand came to be tested at all.
	ws.projectID, ws.projectRoot = "p1", t.TempDir()
	ws.focus = focusTerminal
	ws.termRect = layout.Rect{X: 1, Y: 10, W: 60, H: layout.TermSmallHeight}
	return ws
}

// pumpTerminal drains the pane's event channel through Update exactly as
// Bubble Tea would, until the run reports an exit or the deadline passes.
// It returns whether an exit was seen.
func pumpTerminal(t *testing.T, w *Workspace, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case ev := <-w.term.events:
			w.onTerminalEvent(ev)
			if ev.kind == termExited && ev.gen == w.term.gen {
				return true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Symptom 1: no child output ever appeared
// ---------------------------------------------------------------------------

// The regression test for the worst bug in the episode. Run through the
// REAL shell, with REAL CRLF output, and assert the text is there.
func TestRealChildOutputReachesTheBuffer(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test" // keep the test off state.json

	if cmd := w.runTerminalCommand("Write-Output 'alpha'; Write-Output 'beta'"); cmd != nil {
		cmd() // a start failure would be a say(); run it so the test fails loudly
	}
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	if !pumpTerminal(t, w, 30*time.Second) {
		t.Fatal("no exit event within 30s")
	}

	got := strings.Join(w.term.lines, "\n")
	for _, want := range []string{"$ Write-Output", "alpha", "beta", "[exit 0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("buffer is missing %q; whole buffer:\n%s", want, got)
		}
	}
	// The exact shape the bug produced: the right NUMBER of lines, all of
	// them empty. Assert no committed output line is blank.
	for i, ln := range w.term.lines {
		if strings.TrimSpace(ln) == "" {
			t.Errorf("line %d is blank -- the CRLF bug is back; buffer:\n%s", i, got)
		}
	}
	if w.term.running {
		t.Error("still running after the exit event")
	}
}

// The chunk boundary that makes the obvious fix (ReplaceAll on each chunk)
// wrong. Measured against a real child: "alpha" and "\r\n" arrive as two
// separate reads.
func TestCRLFSplitAcrossTwoChunksIsOneLine(t *testing.T) {
	tp := newTerminalPane()
	tp.feed("alpha")
	tp.feed("\r\n")
	tp.feed("beta\r")
	tp.feed("\n")
	want := []string{"alpha", "beta"}
	if len(tp.lines) != len(want) {
		t.Fatalf("lines = %q, want %q", tp.lines, want)
	}
	for i := range want {
		if tp.lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, tp.lines[i], want[i])
		}
	}
}

// Gap 3 still has to hold after the CRLF fix: a bare CR is a redraw.
func TestBareCRRedrawsStillCollapseToOneLine(t *testing.T) {
	tp := newTerminalPane()
	for i := 0; i < 50; i++ {
		tp.feed("\r" + strings.Repeat("#", i%10) + " downloading")
	}
	tp.feed("\r\n")
	if len(tp.lines) != 1 {
		t.Fatalf("50 redraws produced %d lines, want 1: %q", len(tp.lines), tp.lines)
	}
}

// A real \r-driven progress bar, end to end, for the same reason the
// output test runs a real child.
func TestRealProgressRedrawsDoNotSpamTheBuffer(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.runTerminalCommand("0..20 | ForEach-Object { Write-Host -NoNewline \"`r$_/20\" }; Write-Host ''")
	if !pumpTerminal(t, w, 30*time.Second) {
		t.Fatal("no exit event within 30s")
	}
	// "$ ...", the single collapsed progress line, "[exit 0]".
	if len(w.term.lines) > 3 {
		t.Errorf("progress produced %d lines, want 3 at most: %q", len(w.term.lines), w.term.lines)
	}
	if !strings.Contains(strings.Join(w.term.lines, "\n"), "20/20") {
		t.Errorf("the last redraw is missing: %q", w.term.lines)
	}
}

// ---------------------------------------------------------------------------
// Symptom 2: typed input became a new command instead of reaching stdin
// ---------------------------------------------------------------------------

// #4, as reported: a typed answer must land ON the prompt line it
// answers, and the program's next output must start a NEW line.
//
// The first version appended the echo as its own committed line while the
// prompt was still an UNCOMMITTED partial line, so the echo landed above
// the prompt and the reply continued the prompt line -- producing
// "» Srijan" followed by "your name: hi Srijan", with the typed name
// missing from the line it was typed on. Python's input() is the exact
// case: it writes a prompt with no newline and does not echo its stdin.
func TestATypedAnswerLandsOnThePromptLineItAnswers(t *testing.T) {
	w := termWorkspace(t)
	w.projectRoot = `D:\Projects\Point` // uv needs the real project
	w.term.historyKey = "test"
	w.runTerminalCommand(`uv run python -c "name = input('your name: '); print('hi', name)"`)
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	drainFor(w, 5*time.Second)

	// The prompt is a partial line and must be on screen while the child
	// waits for an answer.
	if got := string(w.term.pendingRunes); !strings.Contains(got, "your name:") {
		t.Fatalf("the prompt is not pending (nothing to answer): pending=%q lines=%q",
			got, w.term.lines)
	}

	w.term.setInput("Srijan")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}

	// Exactly the real-terminal shape.
	var promptLine, replyLine = -1, -1
	for i, ln := range w.term.lines {
		if strings.HasPrefix(ln, "$ ") {
			continue // the echoed command line quotes the prompt too
		}
		if strings.Contains(ln, "your name:") {
			promptLine = i
			if !strings.Contains(ln, "Srijan") {
				t.Errorf("the typed name is missing from the prompt line: %q", ln)
			}
			if strings.Contains(ln, "hi Srijan") {
				t.Errorf("the program's reply merged onto the prompt line: %q", ln)
			}
		}
		if strings.TrimSpace(ln) == "hi Srijan" {
			replyLine = i
		}
	}
	got := strings.Join(w.term.lines, "\n")
	if promptLine < 0 {
		t.Fatalf("no prompt line at all; buffer:\n%s", got)
	}
	if replyLine < 0 {
		t.Fatalf("the program's reply is not on its own line; buffer:\n%s", got)
	}
	if replyLine != promptLine+1 {
		t.Errorf("reply is line %d, prompt is line %d -- want consecutive; buffer:\n%s",
			replyLine, promptLine, got)
	}
	// And no marker line of our own invention.
	if strings.Contains(got, stdinMarker+" Srijan") {
		t.Errorf("the old separate echo line is back; buffer:\n%s", got)
	}
}

// The child still RECEIVES the line, which is the half that always
// worked and must keep working.
func TestTypedLineStillReachesTheChild(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.runTerminalCommand("$x = Read-Host 'name'; Write-Output ('hi ' + $x)")
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	drainFor(w, 2*time.Second)
	w.term.setInput("Srijan")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	if got := strings.Join(w.term.lines, "\n"); !strings.Contains(got, "hi Srijan") {
		t.Errorf("the child never got the typed line; buffer:\n%s", got)
	}
}

// While a process runs, the prompt itself has to say so -- that is what
// makes "this goes to the child" visible before you press Enter.
func TestPromptMarkerChangesWhileRunning(t *testing.T) {
	w := termWorkspace(t)
	idle := stripANSI(w.terminalInputRow(true, "", 60))
	if !strings.HasPrefix(idle, "$ ") {
		t.Errorf("idle prompt = %q, want it to start with %q", idle, "$ ")
	}
	w.term.running = true
	busy := stripANSI(w.terminalInputRow(true, "", 60))
	if !strings.HasPrefix(busy, stdinMarker) {
		t.Errorf("running prompt = %q, want it to start with %q", busy, stdinMarker)
	}
}

// An input() prompt is a partial line. It has to be on screen while the
// child waits, or the pane looks dead at the exact moment it needs an
// answer.
func TestPartialLineWithNoNewlineIsRendered(t *testing.T) {
	w := termWorkspace(t)
	w.term.feed("name: ")
	if len(w.term.lines) != 0 {
		t.Fatalf("a partial line should not commit: %q", w.term.lines)
	}
	out := stripANSI(w.terminalBox(w.termRect))
	if !strings.Contains(out, "name:") {
		t.Errorf("the pending prompt is not on screen:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Symptom 3: small mode had one usable row
// ---------------------------------------------------------------------------

func TestSmallModeShowsOutputAndThePrompt(t *testing.T) {
	w := termWorkspace(t)
	w.term.appendLine("$ pytest -q")
	w.term.appendLine("1 passed")
	w.term.appendLine("[exit 0]")

	l := layout.Compute(120, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: true,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: w.Cards(),
	})
	out := stripANSI(w.terminalBox(l.Secondary))
	for _, want := range []string{"1 passed", "[exit 0]", "$"} {
		if !strings.Contains(out, want) {
			t.Errorf("small mode does not show %q:\n%s", want, out)
		}
	}
}

// Output sits directly above the prompt, not at the top of the box with a
// gap under it -- the first version padded on the wrong side.
func TestOutputIsBottomAnchoredAgainstThePrompt(t *testing.T) {
	w := termWorkspace(t)
	w.term.appendLine("only line")
	rows := strings.Split(stripANSI(w.terminalBox(w.termRect)), "\n")
	var outputRow, promptRow = -1, -1
	for i, r := range rows {
		if strings.Contains(r, "only line") {
			outputRow = i
		}
		if strings.Contains(r, "$") {
			promptRow = i
		}
	}
	if outputRow < 0 || promptRow < 0 {
		t.Fatalf("missing rows (output=%d prompt=%d):\n%s", outputRow, promptRow, strings.Join(rows, "\n"))
	}
	if promptRow != outputRow+1 {
		t.Errorf("output at row %d, prompt at row %d -- want them adjacent:\n%s",
			outputRow, promptRow, strings.Join(rows, "\n"))
	}
}

// "small (3-4 rows, scrollable)" -- the scrollable half.
func TestScrollbackScrolls(t *testing.T) {
	w := termWorkspace(t)
	for i := 0; i < 40; i++ {
		w.term.appendLine("line " + string(rune('a'+i%26)) + strings.Repeat("!", i/26))
	}
	w.scrollTerminal(-10)
	if w.term.scrollBack != 10 {
		t.Fatalf("scrollBack = %d, want 10", w.term.scrollBack)
	}
	up := stripANSI(w.terminalBox(w.termRect))
	w.scrollTerminal(+10)
	if w.term.scrollBack != 0 {
		t.Fatalf("scrollBack = %d after scrolling back down, want 0", w.term.scrollBack)
	}
	down := stripANSI(w.terminalBox(w.termRect))
	if up == down {
		t.Error("scrolling changed nothing on screen")
	}
	// It cannot scroll past the top.
	w.scrollTerminal(-10000)
	if w.term.scrollBack > len(w.term.lines) {
		t.Errorf("scrollBack = %d, past %d lines", w.term.scrollBack, len(w.term.lines))
	}
}

// ---------------------------------------------------------------------------
// Symptom 6: ctrl+c left the pane stuck on "running"
// ---------------------------------------------------------------------------

// The measured failure: killing the shell WRAPPER leaves a grandchild
// holding the pipe write handles, so the pipes never EOF, cmd.Wait() is
// never reached and no exit event is ever emitted. This test starts a
// wrapper with a long-lived grandchild, stops it, and insists the pane
// leaves the running state.
func TestCtrlCStopsAGrandchildAndTheRunningStateClears(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.runTerminalCommand("Start-Process -NoNewWindow -Wait powershell '-NoProfile','-Command','Start-Sleep 60'")
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	drainFor(w, 1500*time.Millisecond)

	grandchild := w.term.cmd.Process.Pid
	if cmd := w.killRunning(); cmd != nil {
		cmd()
	}
	if !w.term.killing {
		t.Error("the first ctrl+c did not mark the run as stopping")
	}
	// A second press must not fire another kill.
	if cmd := w.killRunning(); cmd != nil {
		cmd()
	}

	// Assert on the STATE, not on seeing an event: killRunning now calls
	// catchUpTerminalState, so a press can consume and apply the exit
	// itself and there is no event left for a pump to observe. "Did the
	// pane leave the running state" is the actual requirement either way.
	deadline := time.Now().Add(killGrace + 20*time.Second)
	for time.Now().Before(deadline) && w.term.running {
		select {
		case ev := <-w.term.events:
			w.applyTerminalEvent(ev)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if w.term.running {
		t.Error("still running after ctrl+c")
	}
	if w.term.killing {
		t.Error("still marked as stopping")
	}
	if got := strings.Join(w.term.lines, "\n"); !strings.Contains(got, "[stopped]") && !strings.Contains(got, "[exit") {
		t.Errorf("no outcome line after the kill; buffer:\n%s", got)
	}
	// And the tree is actually gone.
	if alive(grandchild) {
		t.Errorf("pid %d survived the kill", grandchild)
	}
}

// ---------------------------------------------------------------------------
// Paste
// ---------------------------------------------------------------------------

func TestPasteIntoTheTerminalInput(t *testing.T) {
	w := termWorkspace(t)
	w.term.setInput("git ")
	w.pasteIntoTerminal("commit -m \"two\nlines\"")
	got := string(w.term.input)
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("a newline survived the paste: %q", got)
	}
	if !strings.HasPrefix(got, "git commit") {
		t.Errorf("paste did not land at the cursor: %q", got)
	}
}

// Paste has to reach the pane through Update, which is where it was
// silently dropped before.
func TestPasteMsgReachesTheTerminalWhenItHasFocus(t *testing.T) {
	w := termWorkspace(t)
	w.Update(app.PasteMsg{Text: "echo hi", Lines: 1})
	if got := string(w.term.input); got != "echo hi" {
		t.Errorf("input = %q, want %q", got, "echo hi")
	}
}

// ---------------------------------------------------------------------------
// Delivery: the loop must survive the Editor not being active
// ---------------------------------------------------------------------------

func TestTerminalEventsAreAddressedToTheEditor(t *testing.T) {
	var msg tea.Msg = terminalEventMsg{}
	addr, ok := msg.(interface {
		TargetWorkspace() layout.WorkspaceID
	})
	if !ok {
		t.Fatal("terminalEventMsg is not app.WorkspaceAddressed -- a background event would be delivered to whatever is active and the pull loop would die")
	}
	if got := addr.TargetWorkspace(); got != layout.WorkspaceEditor {
		t.Errorf("TargetWorkspace() = %q, want %q", got, layout.WorkspaceEditor)
	}
}

func TestBackgroundTerminalEventsAreAllowedBackToTheWorkspace(t *testing.T) {
	w := termWorkspace(t)
	if got := w.AllowBackgroundMsg(terminalEventMsg{}); got != app.DeliverToWorkspace {
		t.Errorf("terminal events while backgrounded = %v, want DeliverToWorkspace (%v)",
			got, app.DeliverToWorkspace)
	}
}

// onTerminalEvent must re-arm on EVERY path, including the stale-gen drop.
// A path that forgets ends the loop just as dead as a lost message.
func TestEveryEventPathReArmsTheLoop(t *testing.T) {
	w := termWorkspace(t)
	w.term.gen = 7
	for _, ev := range []termEvent{
		{gen: 7, kind: termChunk, text: "x\r\n"},
		{gen: 7, kind: termExited, code: 0},
		{gen: 3, kind: termChunk, text: "stale"},
		{gen: 3, kind: termExited, code: 1},
	} {
		if cmd := w.onTerminalEvent(ev); cmd == nil {
			t.Errorf("event %+v did not re-arm the loop", ev)
		}
	}
	if got := strings.Join(w.term.lines, "\n"); strings.Contains(got, "stale") {
		t.Errorf("a stale-generation chunk was applied: %q", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// drainFor pumps events for a fixed window without requiring an exit.
func drainFor(w *Workspace, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case ev := <-w.term.events:
			w.onTerminalEvent(ev)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func alive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), itoa(pid))
}

// itoa already exists in workspace_test.go; this file reuses it.

// Every row of the pane must be exactly rect.W cells, with real command
// echoes, a bare-backtick PowerShell line, the stdin marker, CJK and an
// emoji in the buffer. The pre-existing frame-width test
// (TestViewFillsEveryRectAtEveryBreakpoint) only ever saw an EMPTY
// terminal, so none of this content was covered by it.
func TestTerminalBoxRowsAreExactlyRectWide(t *testing.T) {
	w := termWorkspace(t)
	for _, ln := range []string{
		"$ Write-Output 'collected 3 items'; Write-Output '3 passed in 0.12s'",
		"collected 3 items",
		"$ 1..40 | ForEach-Object { Write-Host -NoNewline \"`rdownloading $_/40\" }; Write-Host ''",
		stdinMarker + " Srijan",
		strings.Repeat("long", 60),
		"日本語 and 🎉 emoji",
	} {
		w.term.appendLine(ln)
	}
	w.term.feed("a pending partial line")
	for _, mode := range []layout.TermMode{layout.TermSmall, layout.TermOverlay, layout.TermFull} {
		l := layout.Compute(100, 30, layout.State{
			Active: layout.WorkspaceEditor, HeaderAllowed: true,
			TerminalMode: mode, RightCards: w.Cards(),
		})
		for i, row := range strings.Split(w.terminalBox(l.Secondary), "\n") {
			if got := visibleWidth(row); got != l.Secondary.W {
				t.Errorf("%s row %d width = %d, want %d: %q",
					mode, i, got, l.Secondary.W, stripANSI(row))
			}
		}
	}
}
