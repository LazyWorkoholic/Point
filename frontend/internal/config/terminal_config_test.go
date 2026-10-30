package config

import (
	"os"
	"path/filepath"
	"testing"
)

// F14's [terminal] table must actually REACH the Config, which is not a
// given in this file: the sibling [editor] table is decoded and then
// silently discarded (loadFrom has no cfg.Editor assignment), so a
// hand-edited autosave key has never taken effect. This is the test that
// says F14's own table did not ship the same bug.
func TestTerminalTableIsActuallyRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[ui]\ntheme = \"gold\"\n\n[terminal]\nshell = \"cmd\"\nexternal = true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFrom(path)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got := cfg.TerminalShell(); got != ShellCmd {
		t.Errorf("TerminalShell() = %q, want %q -- the [terminal] table was discarded", got, ShellCmd)
	}
	if !cfg.Terminal.External {
		t.Error("terminal.external = true did not reach the Config")
	}
	// And the neighbouring table still works, so this is not a parse
	// accident that happens to favour one key.
	if cfg.UI.Theme != "gold" {
		t.Errorf("UI.Theme = %q, want gold", cfg.UI.Theme)
	}
}

// An absent table, and a typo'd shell, both leave PowerShell in place --
// AutosaveMode's rule, for the same reason: a misspelt key must not
// silently change which shell every command runs through.
func TestTerminalShellNormalisation(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ShellPowerShell},
		{"powershell", ShellPowerShell},
		{"cmd", ShellCmd},
		{"CMD", ShellPowerShell},
		{"bash", ShellPowerShell},
	} {
		got := Config{Terminal: Terminal{Shell: c.in}}.TerminalShell()
		if got != c.want {
			t.Errorf("shell %q -> %q, want %q", c.in, got, c.want)
		}
	}
	if got := Defaults().TerminalShell(); got != ShellPowerShell {
		t.Errorf("Defaults().TerminalShell() = %q", got)
	}
}
