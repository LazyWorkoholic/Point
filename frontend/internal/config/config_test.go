package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	// Compared field-wise, not with ==: Config now carries F12's raw
	// [workspace] map and a struct holding a map is not comparable.
	if cfg.UI != Defaults().UI {
		t.Fatalf("got %+v, want defaults %+v", cfg.UI, Defaults().UI)
	}
	if cfg.Workspace != nil {
		t.Fatalf("missing config produced a [workspace] table: %+v", cfg.Workspace)
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
	if cfg.UI != Defaults().UI || cfg.Workspace != nil {
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

// ---------------------------------------------------------------------------
// F12 — the [workspace] table
// ---------------------------------------------------------------------------

// The table is read as a raw map so a workspace id the frontend has never
// heard of still gets its override — F12's "no constant anywhere; a
// capability workspace added later inherits this for free".
func TestWorkspaceIdleUnloadResolvesNarrowestFirst(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml", `
[workspace]
idle_unload_minutes = 5

[workspace.editor]
idle_unload_minutes = 60

[workspace.unheard-of]
idle_unload_minutes = 2
`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]time.Duration{
		"chat":       5 * time.Minute,  // the table default
		"editor":     60 * time.Minute, // its own override
		"unheard-of": 2 * time.Minute,  // an id no Go code names
	}
	for id, want := range cases {
		if got := cfg.IdleUnload(id); got != want {
			t.Errorf("IdleUnload(%q) = %s, want %s", id, got, want)
		}
	}
}

// An absent table means the documented default, not "never unload" — a
// lifecycle that never fires is a lifecycle nobody ever finds broken.
func TestWorkspaceIdleUnloadDefaultsWithNoTable(t *testing.T) {
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultIdleUnloadMinutes * time.Minute
	if got := cfg.IdleUnload("chat"); got != want {
		t.Fatalf("IdleUnload with no table = %s, want %s", got, want)
	}
}

// Zero in the FILE is the documented way to disable unloading, and a
// negative value is clamped to the same thing rather than producing a
// deadline already in the past (which would unload a workspace the instant
// it stopped being active).
func TestWorkspaceIdleUnloadZeroAndNegativeMeanNever(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml",
		"[workspace]\nidle_unload_minutes = 0\n\n[workspace.editor]\nidle_unload_minutes = -3\n")
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.IdleUnload("chat"); got != 0 {
		t.Errorf("zero resolved to %s, want never", got)
	}
	if got := cfg.IdleUnload("editor"); got != 0 {
		t.Errorf("a negative override resolved to %s, want never", got)
	}
}

// A typo must not stop the app booting, which is config.Load's whole
// contract: the value is ignored and the next level governs.
func TestWorkspaceIdleUnloadIgnoresANonNumber(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "config.toml",
		"[workspace]\nidle_unload_minutes = 7\n\n[workspace.editor]\nidle_unload_minutes = \"soon\"\n")
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.IdleUnload("editor"); got != 7*time.Minute {
		t.Fatalf("a non-numeric override resolved to %s, want the table default", got)
	}
}

// The exact bytes `Add-Content -Encoding utf8` produces in Windows
// PowerShell 5.1: a UTF-8 BOM, CRLF line endings, and a leading blank line.
//
// loadFrom already strips the BOM, for the reason its own comment gives --
// Notepad and PowerShell both add one, and a config saved by either used to
// parse on the Python side and not on this one. This pins that the
// [workspace] table survives it too, because F12's verification gate writes
// that table with exactly this command and a gate that silently configured
// nothing would measure nothing.
func TestWorkspaceTableSurvivesPowerShellsBOMAndCRLF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte("\r\n[workspace]\r\nidle_unload_minutes = 1\r\n")...)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("PowerShell's own output did not parse: %v", err)
	}
	if got := cfg.IdleUnload("chat"); got != time.Minute {
		t.Fatalf("IdleUnload = %s, want 1m", got)
	}
}

// ---------------------------------------------------------------------------
// F12 — two owners, one file
// ---------------------------------------------------------------------------

// The clobber this rule exists to prevent, in both directions.
//
// chat.Workspace writes Drafts/Provider/UI by serialising the WHOLE State
// from a copy taken at startup; app.Root writes LastWorkspace and
// WorkspaceState. Without the keep-what-is-on-disk branch in saveStateTo,
// the loser of any pair of writes erases the winner's fields — type one
// character and the scroll anchor is gone.
func TestTheTwoOwnersOfStateJsonDoNotEraseEachOther(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")

	// Root writes its two fields.
	blobs := map[string]json.RawMessage{
		"chat": json.RawMessage(`{"version":1,"anchor":{"anchor_seq":4211}}`),
	}
	if err := saveWorkspaceStateTo(p, "chat", blobs); err != nil {
		t.Fatal(err)
	}

	// chat then writes a draft, from a copy that has both Root-owned
	// fields zeroed — which is what main.go hands it.
	chatCopy := State{Drafts: map[string]Draft{"s1": {Text: "hello", Cursor: 5}}}
	if err := saveStateTo(p, chatCopy); err != nil {
		t.Fatal(err)
	}

	st, err := loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Drafts["s1"].Text != "hello" {
		t.Errorf("the draft did not survive: %+v", st.Drafts)
	}
	if st.LastWorkspace != "chat" {
		t.Errorf("chat's write erased last_workspace: %q", st.LastWorkspace)
	}
	if !sameJSON(t, st.WorkspaceState["chat"], blobs["chat"]) {
		t.Errorf("chat's write erased or changed the suspend blob: %q",
			st.WorkspaceState["chat"])
	}

	// And Root writing again must not erase the draft, since its own save
	// re-reads the file first.
	if err := saveWorkspaceStateTo(p, "chat", map[string]json.RawMessage{
		"chat": json.RawMessage(`{"version":1,"anchor":{"anchor_seq":9}}`),
	}); err != nil {
		t.Fatal(err)
	}
	st, err = loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Drafts["s1"].Text != "hello" {
		t.Errorf("Root's write erased the draft: %+v", st.Drafts)
	}
	if !sameJSON(t, st.WorkspaceState["chat"], json.RawMessage(`{"version":1,"anchor":{"anchor_seq":9}}`)) {
		t.Errorf("Root's own field did not update: %q", st.WorkspaceState["chat"])
	}
}

// The blob is opaque to this package: its CONTENT round-trips unchanged, so
// a workspace can version its own shape without StateVersion moving.
//
// Content, not bytes: SaveState uses json.MarshalIndent, which re-indents an
// embedded json.RawMessage on the way out. That is harmless -- json.Unmarshal
// ignores whitespace, so chat's own Resume reads a re-indented blob
// identically -- but it does mean nothing downstream may compare these bytes
// for equality, which is what this test pins.
func TestWorkspaceStateBlobsRoundTripUnchanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	raw := json.RawMessage(`{"version":1,"anchor":{"anchor_seq":4211,"row_offset":7,"pinned_to_bottom":false}}`)

	if err := saveWorkspaceStateTo(p, "chat", map[string]json.RawMessage{"chat": raw}); err != nil {
		t.Fatal(err)
	}
	st, err := loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, st.WorkspaceState["chat"], raw) {
		t.Fatalf("blob round-tripped as %q, want the same content as %q",
			st.WorkspaceState["chat"], raw)
	}
}

// sameJSON compares two blobs by content, not by bytes -- see
// TestWorkspaceStateBlobsRoundTripUnchanged for why that distinction is the
// point rather than a convenience.
func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not readable JSON: %q: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not readable JSON: %q: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// Root's save must work against a missing or corrupt file rather than
// losing the position because an unrelated field went bad.
func TestRootSavesPositionEvenWhenTheFileIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "state.json", "{not json")

	if err := saveWorkspaceStateTo(p, "chat", map[string]json.RawMessage{
		"chat": json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	st, err := loadStateFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastWorkspace != "chat" || len(st.WorkspaceState) != 1 {
		t.Fatalf("position was not written over a corrupt file: %+v", st)
	}
}
