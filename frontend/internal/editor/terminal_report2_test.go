package editor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// The second round of manual-testing reports, each with the real cause
// under it. See terminal.go for the per-fix reasoning.

// ---------------------------------------------------------------------------
// #2 -- a long line was CUT at the box width with no marker
// ---------------------------------------------------------------------------

// Reported as "garbled, truncated output". It was neither: the buffer
// held every character and the renderer cut the row. A terminal wraps.
func TestALongLineWrapsInsteadOfBeingCut(t *testing.T) {
	w := termWorkspace(t)
	long := "D:\\Srijan\\UV\\uv-python\\cpython-3.11-windows-x86_64-none\\python.exe: " +
		"can't open file 'D:\\Projects\\Point\\cli.py': [Errno 2] No such file or directory"
	w.term.appendLine(long)

	r := layout.Rect{X: 1, Y: 5, W: 60, H: 12}
	out := stripANSI(w.terminalBox(r))

	// Every word of the message is on screen SOMEWHERE, including the
	// tail that used to be cut off after "python.exe:ca".
	for _, want := range []string{"python.exe", "can't open file", "cli.py", "No such file or directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not on screen:\n%s", want, out)
		}
	}
	// And the frame is still exactly rect-wide on every row.
	for i, row := range strings.Split(out, "\n") {
		if got := visibleWidth(row); got != r.W {
			t.Errorf("row %d width = %d, want %d: %q", i, got, r.W, row)
		}
	}
}

// Wrapping must not reflow: program output's spaces ARE its layout, so a
// wrapped `--help` table has to keep every byte in order. (This is why
// explainpanel.go's word-wrapping wrapCells is not reused.)
func TestWrappingPreservesEveryByteAndItsSpacing(t *testing.T) {
	line := "  doctor              report versions, paths, database, providers"
	got := wrapTerminalLine(line, 20)
	if joined := strings.Join(got, ""); joined != line {
		t.Errorf("wrap lost or moved bytes:\n got %q\nwant %q", joined, line)
	}
	for i, seg := range got {
		if visibleWidth(seg) > 20 {
			t.Errorf("segment %d is %d cells wide: %q", i, visibleWidth(seg), seg)
		}
	}
}

func TestWrappingIsCellAwareForWideRunes(t *testing.T) {
	// Each CJK rune is two cells, so eight of them do not fit in ten.
	got := wrapTerminalLine(strings.Repeat("日", 8), 10)
	if len(got) < 2 {
		t.Fatalf("CJK did not wrap: %q", got)
	}
	for i, seg := range got {
		if visibleWidth(seg) > 10 {
			t.Errorf("segment %d is %d cells: %q", i, visibleWidth(seg), seg)
		}
	}
}

// ---------------------------------------------------------------------------
// #3 -- cd looked like a no-op
// ---------------------------------------------------------------------------

func TestCdSaysThatTierZeroIsStatelessPerCommand(t *testing.T) {
	for _, line := range []string{"cd ..", "cd backend", "CD ..", "Set-Location backend", "pushd ..", "popd"} {
		if statelessDirNote(line) == "" {
			t.Errorf("%q got no note", line)
		}
	}
	// A cd CHAINED with the command it is for is the recommended idiom,
	// so it must not be nagged about.
	for _, line := range []string{
		"cd backend; uv run python cli.py --help",
		"cd backend && pytest -q",
	} {
		if note := statelessDirNote(line); note != "" {
			t.Errorf("%q should not be nagged: %q", line, note)
		}
	}
	// And nothing else trips it.
	for _, line := range []string{"go build ./...", "cdr", "echo cd"} {
		if note := statelessDirNote(line); note != "" {
			t.Errorf("%q got a spurious note: %q", line, note)
		}
	}
}

// The note reaches the BUFFER, not just the status line, because the
// status line is transient and the pane is what the user is reading.
func TestTheCdNoteIsInThePane(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.term.setInput("cd ..")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	got := strings.Join(w.term.lines, "\n")
	if !strings.Contains(got, "starts in the project root") {
		t.Errorf("the pane does not explain cd; buffer:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// #5 -- cls wiped Point's own frame
// ---------------------------------------------------------------------------

// `cls` sends NOTHING down the pipe (measured: zero chunks) because
// PowerShell's Clear-Host writes to the console API, clearing the screen
// Point is drawing on. So the pane has to do two things itself: repair
// its own frame, and actually clear its own buffer.
//
// The trigger is the command TEXT, not how much the command printed --
// see clearsScreen for why inferring it from output volume was too wide.
func TestOnlyARecognisedClearAsksForARepaint(t *testing.T) {
	cases := []struct {
		line      string
		wantClear bool
		wantBare  bool
	}{
		{"cls", true, true},
		{"  CLS  ", true, true},
		{"clear", true, true},
		{"Clear-Host", true, true},
		{"cls; pytest -q", true, false},
		{"pytest -q; cls", true, false},

		// `clear all` -- measured in a real console: PowerShell's
		// Clear-Host is a parameterless FUNCTION, so the extra
		// positional argument goes to $args and the screen is cleared
		// anyway, exit 0. Classifying it as not-a-clear is what brought
		// the corruption back.
		{"clear all", true, true},
		{"CLS all", true, true},
		{"Clear-Host whatever", true, true},

		// A help request prints help and clears nothing -- also
		// measured (the marker survived, help went to stdout).
		{"clear -?", false, false},
		{"cls /?", false, false},
		{"clear --help", false, false},

		// The Clear-* cmdlet family must not match: these touch
		// variables, items, files and the recycle bin, not the screen.
		{"Clear-Variable x", false, false},
		{`Clear-Item HKCU:\Foo`, false, false},
		{"Clear-Content out.txt", false, false},
		{"Clear-RecycleBin -Force", false, false},
		{"clear-variable x", false, false},
		{"$x = 5", false, false},
		{"echo cls", false, false},
		{"mkdir out", false, false},
		{"Get-Content clear.txt", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		clears, bare := clearsScreen(c.line)
		if clears != c.wantClear || bare != c.wantBare {
			t.Errorf("clearsScreen(%q) = %v,%v; want %v,%v",
				c.line, clears, bare, c.wantClear, c.wantBare)
		}
	}
}

// A quiet command that is NOT a clear must no longer blink the screen.
func TestASilentNonClearingCommandDoesNotRepaint(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.term.appendLine("earlier output")

	w.term.setInput("$x = 5")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	if !w.term.running {
		t.Fatal("the command did not start")
	}
	if w.term.clearsScreen {
		t.Error("`$x = 5` was classified as clearing the screen")
	}

	cmd := drainUntilExit(t, w, 60*time.Second)
	if sawClearScreen(cmd) {
		t.Error("a silent, non-clearing command still asked for a repaint -- the reported blink")
	}
	if got := strings.Join(w.term.lines, "\n"); !strings.Contains(got, "earlier output") {
		t.Errorf("a non-clearing command wiped the buffer:\n%s", got)
	}
}

// And a real `cls`, after real output, leaves the buffer EMPTY -- not
// repainted, not two lines shorter.
func TestARealClsEmptiesTheBuffer(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"

	// Several lines of genuine output first.
	w.term.setInput("1..5 | ForEach-Object { Write-Output \"line $_\" }")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	drainUntilExit(t, w, 60*time.Second)
	t.Logf("before cls, %d lines: %q", len(w.term.lines), w.term.lines)
	if len(w.term.lines) < 6 {
		t.Fatalf("not enough output to clear: %q", w.term.lines)
	}

	w.term.setInput("cls")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	t.Logf("the instant cls starts, %d lines: %q", len(w.term.lines), w.term.lines)
	cmd := drainUntilExit(t, w, 60*time.Second)

	t.Logf("after cls exited, %d lines: %q  exitLine=%q", len(w.term.lines), w.term.lines, w.term.exitLine)
	if len(w.term.lines) != 0 {
		t.Errorf("cls left %d lines in the buffer, want 0: %q", len(w.term.lines), w.term.lines)
	}
	if !sawClearScreen(cmd) {
		t.Error("cls did not ask for a repaint -- the console it wiped is Point's own")
	}
	if w.term.scrollBack != 0 {
		t.Errorf("scrollBack = %d after a clear", w.term.scrollBack)
	}
}

// A clear CHAINED with real work clears, then keeps that work and its
// exit code.
func TestAChainedClearKeepsTheWorkAfterIt(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.term.appendLine("old line one")
	w.term.appendLine("old line two")

	w.term.setInput("cls; Write-Output survivor")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	drainUntilExit(t, w, 60*time.Second)

	got := strings.Join(w.term.lines, "\n")
	t.Logf("after `cls; Write-Output survivor`:\n%s", got)
	if strings.Contains(got, "old line") {
		t.Errorf("the chained clear did not wipe the old buffer:\n%s", got)
	}
	if !strings.Contains(got, "survivor") {
		t.Errorf("the work after the clear was lost:\n%s", got)
	}
	if !strings.Contains(got, "[exit 0]") {
		t.Errorf("a chained clear dropped the exit code:\n%s", got)
	}
}

// drainUntilExit pumps until the run resolves and returns the command the
// exit produced.
func drainUntilExit(t *testing.T, w *Workspace, within time.Duration) tea.Cmd {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case ev := <-w.term.events:
			cmd := w.applyTerminalEvent(ev)
			if ev.kind == termExited && ev.gen == w.term.gen {
				return cmd
			}
		case <-time.After(50 * time.Millisecond):
		}
		if !w.term.running {
			return nil
		}
	}
	t.Fatal("the run never resolved")
	return nil
}

func sawClearScreen(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return fmt.Sprintf("%T", cmd()) == "tea.clearScreenMsg"
}

// Whatever the repaint decision, the exit must still re-arm the pull
// loop: an exit that returned only a ClearScreen would end the pane's
// I/O for the rest of the session.
func TestTheExitAlwaysReArmsTheLoop(t *testing.T) {
	for _, line := range []string{"cls", "echo hi"} {
		w := termWorkspace(t)
		w.term.gen = 1
		w.term.running = true
		w.term.clearsScreen, w.term.bareClear = clearsScreen(line)

		// Prime the channel so executing the returned command cannot
		// block on an empty one.
		w.term.events <- termEvent{gen: 1, kind: termChunk, text: ""}

		cmd := w.onTerminalEvent(termEvent{gen: 1, kind: termExited, code: 0})
		if cmd == nil {
			t.Fatalf("%q: no command returned", line)
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("%q: the returned command blocked -- the loop re-arm is missing", line)
		}
	}
}

// `clear all` end to end: it really clears the console (measured), so it
// must wipe the pane's buffer AND ask for the repaint that repairs
// Point's own frame. Classifying it as not-a-clear is what brought the
// original corruption back.
func TestClearAllWipesAndRepaintsLikeABareClear(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"

	w.term.setInput(`1..4 | ForEach-Object { Write-Output "row $_" }`)
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	drainUntilExit(t, w, 60*time.Second)
	if len(w.term.lines) < 5 {
		t.Fatalf("not enough output to clear: %q", w.term.lines)
	}
	t.Logf("before `clear all`, %d lines: %q", len(w.term.lines), w.term.lines)

	w.term.setInput("clear all")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	cmd := drainUntilExit(t, w, 60*time.Second)
	t.Logf("after `clear all`, %d lines: %q", len(w.term.lines), w.term.lines)

	if len(w.term.lines) != 0 {
		t.Errorf("`clear all` left %d lines, want 0: %q", len(w.term.lines), w.term.lines)
	}
	if !sawClearScreen(cmd) {
		t.Error("`clear all` asked for no repaint -- this is the reported corruption")
	}
}

// `clear -?` prints help and clears nothing (measured: the marker
// survived). So it must NOT wipe the buffer and must show its output.
func TestClearHelpNeitherWipesNorRepaints(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.term.appendLine("must survive")

	w.term.setInput("clear -?")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	cmd := drainUntilExit(t, w, 60*time.Second)
	got := strings.Join(w.term.lines, "\n")
	t.Logf("after `clear -?`:\n%s", got)

	if !strings.Contains(got, "must survive") {
		t.Errorf("`clear -?` wiped the buffer though it clears nothing:\n%s", got)
	}
	if sawClearScreen(cmd) {
		t.Error("`clear -?` asked for a repaint it does not need")
	}
	if !strings.Contains(got, "$ clear -?") {
		t.Errorf("`clear -?` lost its own echo line:\n%s", got)
	}
}

// A Clear-* cmdlet that touches variables, not the screen, must be left
// entirely alone.
func TestAClearCmdletDoesNotWipeTheBuffer(t *testing.T) {
	w := termWorkspace(t)
	w.term.historyKey = "test"
	w.term.appendLine("keep me")

	w.term.setInput("Clear-Variable nosuchvar -ErrorAction SilentlyContinue")
	if cmd := w.submitTerminalLine(); cmd != nil {
		cmd()
	}
	cmd := drainUntilExit(t, w, 60*time.Second)
	got := strings.Join(w.term.lines, "\n")
	t.Logf("after Clear-Variable:\n%s", got)

	if !strings.Contains(got, "keep me") {
		t.Errorf("Clear-Variable wiped the buffer:\n%s", got)
	}
	if sawClearScreen(cmd) {
		t.Error("Clear-Variable asked for a repaint")
	}
}
