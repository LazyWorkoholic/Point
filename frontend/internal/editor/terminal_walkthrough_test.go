package editor

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// The F14 verification walkthrough, run against REAL child processes and
// printing the REAL rendered pane at each step.
//
// It exists because this episode's first round of "verification" was a
// list of steps for a human plus a green unit suite, and the two together
// still shipped a pane that showed no output at all. Everything here that
// CAN be checked without a human driving a terminal is checked here, with
// the frame printed so the output can be read rather than trusted.
//
// What it deliberately does NOT claim to cover: tea.ExecProcess (the
// hatch) suspends Bubble Tea and hands over the real terminal, so it
// cannot run in a test process -- and neither can "a backend task is
// still running after the hatch", which needs the Python runtime and a
// real suspend. Those two stay human steps and are named as such.
//
// Run it with:
//
//	go test ./frontend/internal/editor/ -run TestF14Walkthrough -v

// typeAndRun drives the REAL keystroke path: every rune through
// handleKey, then Enter. That matters more than it looks -- the first
// version of this walkthrough called runTerminalCommand directly, which
// skips terminalKey, insertRunes, submitTerminalLine AND
// recordTerminalHistory, so it "verified" the pane while bypassing most
// of what a user actually touches. The history steps below were silently
// empty because of it.
func typeAndRun(t *testing.T, w *Workspace, line string) {
	t.Helper()
	for _, r := range line {
		w.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := string(w.term.input); got != line {
		t.Fatalf("typing %q produced %q on the input line", line, got)
	}
	w.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
}

// show renders the pane at a given mode and prints it with a caption.
func show(t *testing.T, w *Workspace, caption string, mode layout.TermMode) {
	t.Helper()
	l := layout.Compute(100, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: false,
		HeaderAllowed: true, TerminalMode: mode, RightCards: w.Cards(),
	})
	w.termRect = l.Secondary
	frame := stripANSI(w.terminalBox(l.Secondary))
	t.Logf("%s  (mode=%s, rect=%dx%d)\n%s", caption, mode, l.Secondary.W, l.Secondary.H, frame)
}

func TestF14Walkthrough(t *testing.T) {
	dir := t.TempDir()
	// CLAUDE.md's rule, in-process: the real %APPDATA%\Point and
	// %LOCALAPPDATA%\Point are never the target of anything here.
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)

	w := New(Options{Theme: testTheme()})
	w.sendFn = func(string, queryKind, map[string]any) {}
	w.projectID, w.projectRoot = "proj-walkthrough", `D:\Projects\Point`
	w.focus = focusTerminal

	// ---- 1. a real command, streamed, with a visible exit code --------
	t.Log("\n########## 1. a real command: output + exit code ##########")
	typeAndRun(t, w, "Write-Output 'collected 3 items'; Write-Output '3 passed in 0.12s'")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "after the command finished", layout.TermSmall)

	// ---- 2. the same thing in the real repo, via the real toolchain ---
	t.Log("\n########## 2. a real repo command (uv run python) ##########")
	typeAndRun(t, w, `uv run python -c "print('hello from the backend venv')"`)
	if !pumpTerminal(t, w, 120*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "uv run python", layout.TermOverlay)

	// ---- 3. a failing command's exit code is visible -----------------
	t.Log("\n########## 3. a non-zero exit code ##########")
	typeAndRun(t, w, "Write-Output 'about to fail'; exit 3")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "a command that exits 3", layout.TermSmall)

	// ---- 4. stderr is interleaved, not dropped -----------------------
	t.Log("\n########## 4. stderr interleaved with stdout ##########")
	typeAndRun(t, w, "Write-Output 'on stdout'; [Console]::Error.WriteLine('on stderr')")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "stdout and stderr together", layout.TermOverlay)

	// ---- 5. history recall with the up arrow -------------------------
	t.Log("\n########## 5. history: up arrow recalls ##########")
	t.Logf("history now holds %d entries: %q", len(w.term.history), w.term.history)
	w.terminalKey(tea.KeyMsg{Type: tea.KeyUp}, "up")
	t.Logf("after one ↑, the input line reads: %q", string(w.term.input))
	w.terminalKey(tea.KeyMsg{Type: tea.KeyUp}, "up")
	t.Logf("after two ↑: %q", string(w.term.input))
	w.terminalKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	t.Logf("after one ↓: %q", string(w.term.input))
	show(t, w, "a recalled command sitting on the input line", layout.TermSmall)
	w.term.setInput("")

	// ---- 6. history is persisted per PROJECT -------------------------
	t.Log("\n########## 6. history persists per project, in state.json ##########")
	st, err := config.LoadState()
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	for key, lines := range st.TerminalHistory {
		t.Logf("state.json bucket %q holds %d commands: %q", key, len(lines), lines)
	}
	if _, ok := st.TerminalHistory["project:proj-walkthrough"]; !ok {
		t.Errorf("no bucket for this project; got keys %v", keysOf(st.TerminalHistory))
	}
	// A DIFFERENT project must not see them.
	w.projectID = "a-different-project"
	w.reloadTerminalHistory()
	t.Logf("after rebinding to another project, history holds %d entries: %q",
		len(w.term.history), w.term.history)
	if len(w.term.history) != 0 {
		t.Errorf("another project inherited this one's history: %q", w.term.history)
	}
	w.projectID = "proj-walkthrough"
	w.reloadTerminalHistory()
	t.Logf("back on the original project, history holds %d entries again", len(w.term.history))

	// ---- 7. an interactive input() prompt, answered from the pane -----
	t.Log("\n########## 7. an input() prompt answered from the pane ##########")
	typeAndRun(t, w, `uv run python -c "name = input('your name: '); print('hi', name)"`)
	drainFor(w, 4*time.Second)
	show(t, w, "the child is waiting -- note the prompt and the » marker", layout.TermOverlay)
	for _, r := range "Srijan" {
		w.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	w.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "after answering", layout.TermOverlay)

	// ---- 8. a bare-CR progress bar does not flood the buffer ---------
	t.Log("\n########## 8. \\r progress redraws ##########")
	before := len(w.term.lines)
	typeAndRun(t, w, "1..40 | ForEach-Object { Write-Host -NoNewline \"`rdownloading $_/40\" }; Write-Host ''")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	t.Logf("40 redraws added %d lines to the buffer", len(w.term.lines)-before)
	show(t, w, "the collapsed progress line", layout.TermSmall)

	// ---- 9. ctrl+c kills the child (and its grandchildren) -----------
	t.Log("\n########## 9. ctrl+c on a command with a grandchild ##########")
	typeAndRun(t, w, "Start-Process -NoNewWindow -Wait powershell '-NoProfile','-Command','Start-Sleep 90'")
	drainFor(w, 1500*time.Millisecond)
	pid := w.term.cmd.Process.Pid
	show(t, w, "while it runs", layout.TermSmall)
	t.Logf("tree root pid %d, alive=%v", pid, alive(pid))
	started := time.Now()
	w.killRunning()
	if !pumpTerminal(t, w, killGrace+30*time.Second) {
		t.Fatal("the pane never left the running state")
	}
	t.Logf("the pane left the running state %v after ctrl+c; pid %d alive=%v",
		time.Since(started).Round(10*time.Millisecond), pid, alive(pid))
	show(t, w, "after ctrl+c", layout.TermSmall)

	// ---- 10. mode cycling, and the mode surviving Suspend/Resume -----
	t.Log("\n########## 10. mode cycling and persistence ##########")
	t.Logf("mode starts at %q", w.term.mode)
	for i := 0; i < 4; i++ {
		w.cycleTerminalMode()
		t.Logf("after f8 #%d: mode=%q, Compute gives Secondary.H=%d",
			i+1, w.term.mode, secondaryFor(w, w.term.mode))
	}
	w.cycleTerminalMode() // -> full
	w.cycleTerminalMode() // -> small, which is what the blob below should hold
	blob, err := w.Suspend()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Suspend() blob: %s", string(blob))
	fresh := New(Options{Theme: testTheme()})
	fresh.sendFn = func(string, queryKind, map[string]any) {}
	if err := fresh.Resume(blob); err != nil {
		t.Fatal(err)
	}
	t.Logf("a freshly constructed workspace resumed with mode=%q (was %q)", fresh.term.mode, w.term.mode)
	if fresh.term.mode != w.term.mode {
		t.Errorf("the mode did not survive Suspend/Resume")
	}

	// ---- 11. focus collapses the pane; returning restores the mode ---
	t.Log("\n########## 11. focus collapse and restore ##########")
	w.docs = append(w.docs, testDoc(t, "print('hi')\n"))
	w.term.mode = layout.TermFull
	w.focus = focusTerminal
	t.Logf("terminal focused, remembered mode=%q -> drawn as %q", w.term.mode, w.effectiveTerminalMode())
	w.focus = focusBuffer
	t.Logf("code pane focused              -> drawn as %q (remembered is still %q)",
		w.effectiveTerminalMode(), w.term.mode)
	w.focusTerminalPane()
	t.Logf("terminal focused again         -> drawn as %q", w.effectiveTerminalMode())

	// ---- 12. output while the code pane has focus auto-expands -------
	t.Log("\n########## 12. auto-expand on background output ##########")
	w.term.mode = layout.TermSmall
	w.focus = focusBuffer
	w.term.autoExpanded = false
	t.Logf("before any output: drawn as %q, focus=%v", w.effectiveTerminalMode(), w.focus)
	w.onTerminalEvent(termEvent{gen: w.term.gen, kind: termChunk, text: "FAILED tests/test_a.py::test_one\r\n"})
	t.Logf("after output arrived while the code pane had focus: drawn as %q, focus=%v (unchanged)",
		w.effectiveTerminalMode(), w.focus)
	if w.effectiveTerminalMode() != layout.TermOverlay {
		t.Errorf("no auto-expand: %q", w.effectiveTerminalMode())
	}
	if w.focus != focusBuffer {
		t.Errorf("focus was stolen: %v", w.focus)
	}
	// Keys still go to the buffer, which is the "never stealing focus" half.
	d := w.docs[w.active]
	versionBefore := d.buf.Version()
	w.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	t.Logf("a keystroke while auto-expanded: buffer version %d -> %d (it reached the code, not the terminal); terminal input is %q",
		versionBefore, d.buf.Version(), string(w.term.input))
	if d.buf.Version() == versionBefore {
		t.Error("the keystroke did not reach the buffer")
	}
	if len(w.term.input) != 0 {
		t.Errorf("the keystroke leaked into the terminal input: %q", string(w.term.input))
	}
	w.focusTerminalPane()
	t.Logf("after returning to the terminal: drawn as %q, autoExpanded=%v",
		w.effectiveTerminalMode(), w.term.autoExpanded)

	// ---- 13. scrollback -----------------------------------------------
	t.Log("\n########## 13. scrollback ##########")
	typeAndRun(t, w, "1..30 | ForEach-Object { Write-Output \"line $_\" }")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	show(t, w, "pinned to the live edge", layout.TermSmall)
	w.scrollTerminal(-12)
	show(t, w, "after pgup x1 (scrolled up 12)", layout.TermSmall)
	w.scrollTerminal(+12)
	show(t, w, "after pgdown (back at the edge)", layout.TermSmall)
	t.Logf("buffer holds %d lines; cap is %d", len(w.term.lines), terminalLineCap)

	// ---- 14. paste into the input line -------------------------------
	t.Log("\n########## 14. paste ##########")
	w.term.setInput("")
	w.Update(app.PasteMsg{Text: "git commit -m \"line one\nline two\"", Lines: 2})
	t.Logf("after pasting a two-line string: input = %q", string(w.term.input))

	// ---- 15. the hatch: what can and cannot be checked here ----------
	t.Log("\n########## 15. the hatch ##########")
	w.term.setInput("!python")
	cmd := w.submitTerminalLine()
	t.Logf("a `!python` line produces: %#v -- Root turns that into tea.ExecProcess (app\\hatch.go)", cmd())
	t.Log("the suspend/restore itself CANNOT be exercised in a test process:")
	t.Log("  tea.ExecProcess releases the real terminal and blocks Bubble Tea's event loop.")
	t.Log("  Items 4, 5 and 7 of the manual list stay human steps.")

	// ---- 16. the output buffer cap -----------------------------------
	t.Log("\n########## 16. the 5k-line cap ##########")
	for i := 0; i < terminalLineCap+500; i++ {
		w.term.appendLine("filler")
	}
	t.Logf("after appending %d lines, the buffer holds %d (cap %d)",
		terminalLineCap+500, len(w.term.lines), terminalLineCap)
	if len(w.term.lines) > terminalLineCap {
		t.Errorf("the cap leaked: %d lines", len(w.term.lines))
	}
}

func secondaryFor(w *Workspace, mode layout.TermMode) int {
	l := layout.Compute(100, 30, layout.State{
		Active: layout.WorkspaceEditor, HeaderAllowed: true,
		TerminalMode: mode, RightCards: w.Cards(),
	})
	return l.Secondary.H
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
