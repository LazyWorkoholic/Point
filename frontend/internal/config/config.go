// Package config reads what the user owns and writes what the machine owns.
//
//	%APPDATA%\Point\config.toml       user-owned, READ ONLY, never rewritten
//	%LOCALAPPDATA%\Point\state.json   machine-written
//
// The split mirrors backend\appconfig.py exactly, including reading the same
// two environment variables, so a scratch profile made by setting APPDATA and
// LOCALAPPDATA moves both sides of the app at once.
//
// A missing config is normal — Point must boot from nothing. A broken config
// is also non-fatal, but it is not silent: Load returns the parse error
// alongside the defaults so the UI can say so.
package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
)

const appName = "Point"

// ConfigDir is user-owned configuration. It roams with the profile on Windows.
func ConfigDir() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, appName)
		}
		return filepath.Join(homeDir(), "AppData", "Roaming", appName)
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, appName)
	}
	return filepath.Join(homeDir(), ".config", appName)
}

// StateDir is machine-written state. It never roams and is never backed up.
func StateDir() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, appName)
		}
		return filepath.Join(homeDir(), "AppData", "Local", appName)
	}
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, appName)
	}
	return filepath.Join(homeDir(), ".local", "state", appName)
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func ConfigPath() string { return filepath.Join(ConfigDir(), "config.toml") }
func StatePath() string  { return filepath.Join(StateDir(), "state.json") }
func LogDir() string     { return filepath.Join(StateDir(), "logs") }

// UI is the [ui] table. These key names are chosen here, in F1 — nothing in
// design\ had named them.
type UI struct {
	Theme      string `toml:"theme"`
	Background string `toml:"background"`

	// Scrollback bounds how many messages the Chat workspace holds
	// RESIDENT at once. Named here, not in LAYOUT.md, which only names the
	// default (500) and the key (`ui.scrollback`).
	//
	// F18 is what made this a real bound. From F1 until then it governed
	// only the render cache (chat\cache.go) while the transcript itself
	// held every message GetSession returned, so this comment's original
	// wording -- "caps how many messages F3's Chat workspace renders per
	// session" -- described behaviour no code had. It is accurate now: it
	// is the message-count bound on F18's contiguous resident window, and
	// the render cache stays a multiple of it.
	//
	// The workspace floors it at a few screenfuls (chat\history.go's
	// historyBound) so a very tall terminal can never want more rows than
	// the window is allowed to hold.
	Scrollback int `toml:"scrollback"`

	// StreamTimeout is how many seconds a task may sit in a running state
	// with no events before F5's Chat workspace stops its spinner and shows
	// "no response". F05-streaming-queue.md asks for it as "(config,
	// default 120s)" and names no key, the same gap ui.scrollback had, so
	// F5 names it here for the same reason F1 named that one: [ui] is the
	// table the frontend reads, and this is a CLIENT-side watchdog. The
	// backend has its own, unrelated per-request timeouts and does not read
	// this key.
	StreamTimeout int `toml:"stream_timeout"`

	// HistoryBatch is how many messages one scroll-fetch pulls -- F18's
	// `ui.historyBatch`, asked for by that episode as "config, not a
	// constant" and named here in the file's own snake_case convention
	// (`scrollback`, `stream_timeout`), so it reads like its neighbours
	// rather than like the episode doc.
	//
	// It is tunable for one measured reason: the right batch differs
	// between a fast machine and a low-end laptop with a local model
	// loaded, and the whole point of F18's backpressure rule is that
	// hydrating scrollback must never cost the generation the user is
	// actually watching. The default is deliberately conservative.
	HistoryBatch int `toml:"history_batch"`
}

// Editor is the `[editor]` table (F13b / D90).
//
//	[editor]
//	autosave = "off" | "after_delay" | "on_focus_change"
//	autosave_delay_ms = 1000
type Editor struct {
	// Autosave is one of the three modes. An unrecognised value is
	// treated as the default rather than as "off", because a typo'd
	// config key silently disabling the thing that saves your work is the
	// worst of the available failures -- see AutosaveMode.
	Autosave string `toml:"autosave"`

	// AutosaveDelayMs is how long after typing STOPS a save fires.
	//
	// 1000 is VS Code's number and F13's own doc gives the reason it was
	// taken rather than chosen: it is long enough that you rarely save
	// mid-token, which matters because an autosave with the preview
	// running (F17) reloads the browser on a possibly-broken file.
	AutosaveDelayMs int `toml:"autosave_delay_ms"`
}

// The three autosave modes, as F13's own doc spells them.
const (
	AutosaveOff           = "off"
	AutosaveAfterDelay    = "after_delay"
	AutosaveOnFocusChange = "on_focus_change"
)

// DefaultAutosaveDelayMs is `[editor] autosave_delay_ms` when absent.
const DefaultAutosaveDelayMs = 1000

// AutosaveMode is the configured mode, normalised.
//
// An absent value and an UNRECOGNISED value both give the default
// (after_delay), which is deliberate and is the one place this differs
// from IdleUnload's "ignored, next level used": there is no next level
// here, and the alternatives for a typo are "off" or "after_delay". Off
// means a misspelt key silently stops saving your work and nothing says
// so; after_delay means a misspelt key leaves the default behaviour in
// place, which is what the user had before they typed it. Only an exact
// "off" turns it off.
func (c Config) AutosaveMode() string {
	switch c.Editor.Autosave {
	case AutosaveOff, AutosaveAfterDelay, AutosaveOnFocusChange:
		return c.Editor.Autosave
	default:
		return AutosaveAfterDelay
	}
}

// AutosaveDelay is the after_delay interval. A non-positive value in the
// file falls back to the default rather than producing a timer that fires
// on the next tick -- which would be a save per keystroke, i.e. exactly
// the thing Done-when 10 ("one save at the end, not thirty") forbids.
func (c Config) AutosaveDelay() time.Duration {
	ms := c.Editor.AutosaveDelayMs
	if ms <= 0 {
		ms = DefaultAutosaveDelayMs
	}
	return time.Duration(ms) * time.Millisecond
}

// Config is the whole file, reduced to what the frontend reads. Keys the
// backend owns (database, providers, …) are deliberately absent: this side
// never needed them, and parsing them here would create a second place they
// could disagree.
type Config struct {
	UI UI `toml:"ui"`

	// Editor is F13b's `[editor]` table: autosave and its delay. Like
	// `[workspace]`, these are FRONTEND keys -- the timer is a Bubble Tea
	// tick in this process and nothing in backend\appconfig.py reads
	// either of them.
	//
	// `editor.max_file_kb` is NOT here and must not be added: that one is
	// the backend's (appconfig.py's EDITOR_MAX_FILE_KB_DEFAULT, enforced
	// in ProjectManager.write_file's step 3), and a second copy on this
	// side would be a number that can disagree with the one actually
	// doing the refusing. The editor learns the limit from the
	// `too_large` refusal itself, which reports `limit_kb`.
	Editor Editor `toml:"editor"`

	// Workspace is F12's `[workspace]` table, held RAW rather than as a
	// struct, and that is the whole reason it is a map.
	//
	// The table mixes two kinds of key:
	//
	//	[workspace]
	//	idle_unload_minutes = 5      # the default, for every workspace
	//
	//	[workspace.editor]
	//	idle_unload_minutes = 60     # one workspace's override
	//
	// A struct would have to name every workspace id as a field, and
	// F12-workspace-manager.md's own requirement is the opposite of that:
	// "no constant anywhere; a capability workspace added later inherits
	// this for free because it comes through the same registry". A map
	// keyed on whatever the file actually says has no list to keep in step
	// with the registry. Read it through IdleUnload, never directly.
	//
	// This is the FIRST table outside `[ui]` the Go side parses. It is the
	// frontend's own key — the per-workspace idle timer is a Bubble Tea
	// tick in this process and nothing in `backend\appconfig.py` reads it.
	// Like every other config.toml value, Point only ever reads it:
	// Settings has no Workspace tab and never writes this file (F10b's
	// same rule), so it is hand-edited or absent.
	Workspace map[string]any `toml:"workspace"`
}

// DefaultIdleUnloadMinutes is `[workspace] idle_unload_minutes` when the
// table is absent — F12's own example value. Zero in the FILE disables
// unloading; zero is deliberately not the default, because a lifecycle that
// never fires is a lifecycle nobody ever finds broken.
const DefaultIdleUnloadMinutes = 5

// IdleUnload is how long a loaded, inactive workspace may sit before it is
// unloaded. Zero means never.
//
// Resolution order, narrowest first: `[workspace.<id>] idle_unload_minutes`,
// then `[workspace] idle_unload_minutes`, then DefaultIdleUnloadMinutes. A
// value that is present but not a number is ignored with the next level
// used instead — the same "advisory, always usable" rule Load itself
// follows, since a config.toml typo must not stop the app booting.
func (c Config) IdleUnload(id string) time.Duration {
	if sub, ok := c.Workspace[id].(map[string]any); ok {
		if n, ok := tomlInt(sub["idle_unload_minutes"]); ok {
			return minutes(n)
		}
	}
	if n, ok := tomlInt(c.Workspace["idle_unload_minutes"]); ok {
		return minutes(n)
	}
	return minutes(DefaultIdleUnloadMinutes)
}

// minutes clamps a negative value to zero — "never unload" — rather than
// producing a deadline already in the past, which would unload a workspace
// the instant it stopped being active.
func minutes(n int64) time.Duration {
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Minute
}

// tomlInt accepts the two shapes BurntSushi's decoder produces for a bare
// number landing in an `any`: int64 for an integer, float64 for `5.0`.
// Anything else is not a number and is reported as absent.
func tomlInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// Defaults is what an absent config.toml means.
func Defaults() Config {
	return Config{
		UI: UI{
			Theme: "turquoise", Background: "transparent",
			Scrollback: 500, StreamTimeout: 120, HistoryBatch: 100,
		},
		Editor: Editor{
			Autosave: AutosaveAfterDelay, AutosaveDelayMs: DefaultAutosaveDelayMs,
		},
	}
}

// Load reads config.toml.
//
// The error is advisory: the Config returned is always usable. A missing file
// returns defaults and a nil error, because "no config" is a supported state
// and not a problem. A malformed file returns defaults and the parse error, so
// the app still starts but the UI can tell the user their file was ignored
// rather than silently applying settings they did not write.
func Load() (Config, error) {
	return loadFrom(ConfigPath())
}

func loadFrom(path string) (Config, error) {
	cfg := Defaults()

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}

	// tomllib on the Python side strips a UTF-8 BOM before parsing because
	// Notepad and PowerShell 5.1's Set-Content -Encoding utf8 both add one.
	// Go's TOML decoder has the same objection, so it gets the same fix —
	// otherwise a config saved by either tool parses on one side of the app
	// and not the other.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	parsed := Defaults()
	if _, err := toml.Decode(string(raw), &parsed); err != nil {
		return cfg, err
	}

	// An empty string in the file means "not set", not "set to nothing".
	if parsed.UI.Theme != "" {
		cfg.UI.Theme = parsed.UI.Theme
	}
	if parsed.UI.Background != "" {
		cfg.UI.Background = parsed.UI.Background
	}
	if parsed.UI.Scrollback != 0 {
		cfg.UI.Scrollback = parsed.UI.Scrollback
	}
	if parsed.UI.StreamTimeout != 0 {
		cfg.UI.StreamTimeout = parsed.UI.StreamTimeout
	}
	if parsed.UI.HistoryBatch != 0 {
		cfg.UI.HistoryBatch = parsed.UI.HistoryBatch
	}
	// Carried wholesale rather than field-by-field: the "empty means not
	// set" dance above exists because UI's fields have meaningful zero
	// values this package has to distinguish from absence. A raw map
	// already distinguishes them -- an absent key is simply not in it --
	// and IdleUnload reads it that way.
	cfg.Workspace = parsed.Workspace
	return cfg, nil
}

// EnsureDirs creates the directories Point writes to. config.toml itself is
// never created — it is the user's file, and Point only ever reads it.
func EnsureDirs() error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(LogDir(), 0o755)
}
