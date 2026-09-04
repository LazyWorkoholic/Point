package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// "Missing config is normal — defaults apply and nothing crashes."
func TestMissingConfigIsNotAnError(t *testing.T) {
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("missing config reported an error: %v", err)
	}
	if cfg != Defaults() {
		t.Fatalf("got %+v, want defaults %+v", cfg, Defaults())
	}
}

func TestReadsUISection(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "[ui]\ntheme = \"gold\"\nbackground = \"dark\"\n")

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != "gold" || cfg.UI.Background != "dark" {
		t.Fatalf("got %+v", cfg.UI)
	}
}

// F3's scrollback cap follows the same "empty/zero means not set" rule as the
// two string fields.
func TestReadsScrollback(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "[ui]\nscrollback = 2000\n")

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Scrollback != 2000 {
		t.Fatalf("scrollback = %d, want 2000", cfg.UI.Scrollback)
	}
}

func TestScrollbackZeroKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "[ui]\ntheme = \"green\"\n")

	cfg, _ := loadFrom(p)
	if cfg.UI.Scrollback != Defaults().UI.Scrollback {
		t.Fatalf("scrollback = %d, want the default %d", cfg.UI.Scrollback, Defaults().UI.Scrollback)
	}
}

// A partial config keeps the defaults for everything it does not mention.
func TestPartialConfigKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "[ui]\ntheme = \"green\"\n")

	cfg, _ := loadFrom(p)
	if cfg.UI.Theme != "green" {
		t.Errorf("theme = %q", cfg.UI.Theme)
	}
	if cfg.UI.Background != Defaults().UI.Background {
		t.Errorf("background = %q, want the default %q", cfg.UI.Background, Defaults().UI.Background)
	}
}

// Sections the backend owns must not make this side fail — config.toml is one
// file shared by two processes that read different parts of it.
func TestBackendSectionsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", `
[database]
path = "D:\\somewhere\\runtime.db"

[ui]
theme = "butterfly"

[providers.anthropic]
model = "claude-opus-5"
`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("backend sections rejected: %v", err)
	}
	if cfg.UI.Theme != "butterfly" {
		t.Fatalf("theme = %q", cfg.UI.Theme)
	}
}

// Notepad and PowerShell 5.1 both write a UTF-8 BOM. The Python side strips
// one; so must this one, or the same file parses on one side and not the other.
func TestUTF8BOMIsStripped(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "\xef\xbb\xbf[ui]\ntheme = \"gold\"\n")

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("BOM'd config rejected: %v", err)
	}
	if cfg.UI.Theme != "gold" {
		t.Fatalf("theme = %q", cfg.UI.Theme)
	}
}

// A broken config must not stop the app from starting, but it must not be
// silent either.
func TestBrokenConfigReturnsDefaultsAndTheError(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", "[ui\ntheme = ")

	cfg, err := loadFrom(p)
	if err == nil {
		t.Fatal("a malformed config parsed silently")
	}
	if cfg != Defaults() {
		t.Fatalf("got %+v, want defaults", cfg)
	}
}

// Both processes read the same two environment variables, so one scratch
// profile moves the whole app.
func TestDirsFollowTheEnvironment(t *testing.T) {
	if os.Getenv("OS") == "" && filepath.Separator == '/' {
		t.Skip("Windows path layout")
	}
	t.Setenv("APPDATA", `D:\scratch\Roaming`)
	t.Setenv("LOCALAPPDATA", `D:\scratch\Local`)

	if got, want := ConfigDir(), filepath.Join(`D:\scratch\Roaming`, "Point"); got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
	if got, want := StateDir(), filepath.Join(`D:\scratch\Local`, "Point"); got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
	if got, want := LogDir(), filepath.Join(`D:\scratch\Local`, "Point", "logs"); got != want {
		t.Errorf("LogDir = %q, want %q", got, want)
	}
}

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")

	if err := saveStateTo(p, State{LastWorkspace: "chat"}); err != nil {
		t.Fatal(err)
	}
	st, err := loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastWorkspace != "chat" {
		t.Errorf("LastWorkspace = %q", st.LastWorkspace)
	}
	if st.Version != StateVersion {
		t.Errorf("Version = %d, want %d", st.Version, StateVersion)
	}
}

func TestMissingStateIsAFirstRun(t *testing.T) {
	st, err := loadStateFrom(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("missing state reported an error: %v", err)
	}
	if !reflect.DeepEqual(st, DefaultState()) {
		t.Fatalf("got %+v, want %+v", st, DefaultState())
	}
}

func TestCorruptStateFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "state.json", "{not json")

	st, err := loadStateFrom(p)
	if err == nil {
		t.Fatal("corrupt state parsed silently")
	}
	if !reflect.DeepEqual(st, DefaultState()) {
		t.Fatalf("got %+v, want defaults", st)
	}
}

// A file written by a future Point is ignored rather than half-read.
func TestFutureStateVersionIsIgnored(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "state.json", `{"version": 99, "last_workspace": "hologram"}`)

	st, err := loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastWorkspace != "" {
		t.Fatalf("read state from a future version: %+v", st)
	}
}

// An interrupted write must leave the previous file intact, so the save is
// temp-file-then-rename and never truncates in place.
func TestSaveLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")

	for i := 0; i < 3; i++ {
		if err := saveStateTo(p, State{LastWorkspace: "chat"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory holds %v, want only state.json", names)
	}
}

// Point never creates or rewrites config.toml — it is the user's file.
func TestEnsureDirsNeverCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(dir, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "Roaming"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "Local"))

	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ConfigPath()); err == nil {
		t.Fatal("EnsureDirs created config.toml")
	}
	if _, err := os.Stat(LogDir()); err != nil {
		t.Fatalf("log directory not created: %v", err)
	}
}
