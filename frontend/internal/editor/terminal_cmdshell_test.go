package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// `terminal.shell = "cmd"` is a shipped config option, so it gets the same
// real-process test the PowerShell path does. cmd.exe is a different
// stdio implementation, not a cosmetic switch: it was never exercised
// before this test existed.
func TestTheCmdShellPathRunsRealCommands(t *testing.T) {
	w := termWorkspace(t)
	w.shell = config.ShellCmd
	w.term.historyKey = "test"

	if got := w.terminalShell(); got != config.ShellCmd {
		t.Fatalf("terminalShell() = %q", got)
	}
	w.runTerminalCommand("echo cmd-shell-works && echo second-line")
	if !w.term.running {
		t.Fatal("the command did not start under cmd")
	}
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}

	got := strings.Join(w.term.lines, "\n")
	for _, want := range []string{"cmd-shell-works", "second-line", "[exit 0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q; buffer:\n%s", want, got)
		}
	}
	// The CRLF fix has to hold for cmd.exe too -- it is the same \r\n on
	// the wire, and a blank line here would be the original bug wearing a
	// different shell.
	for i, ln := range w.term.lines {
		if strings.TrimSpace(ln) == "" {
			t.Errorf("line %d is blank under cmd; buffer:\n%s", i, got)
		}
	}
}

// A non-zero exit code survives the cmd wrapper too.
func TestTheCmdShellReportsExitCodes(t *testing.T) {
	w := termWorkspace(t)
	w.shell = config.ShellCmd
	w.term.historyKey = "test"
	w.runTerminalCommand("exit 7")
	if !pumpTerminal(t, w, 60*time.Second) {
		t.Fatal("no exit event")
	}
	if got := strings.Join(w.term.lines, "\n"); !strings.Contains(got, "[exit 7]") {
		t.Errorf("want [exit 7]; buffer:\n%s", got)
	}
}
