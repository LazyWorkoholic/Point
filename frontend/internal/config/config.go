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

// Config is the whole file, reduced to what the frontend reads. Keys the
// backend owns (database, providers, …) are deliberately absent: this side
// never needed them, and parsing them here would create a second place they
// could disagree.
type Config struct {
	UI UI `toml:"ui"`
}

// Defaults is what an absent config.toml means.
func Defaults() Config {
	return Config{UI: UI{
		Theme: "turquoise", Background: "transparent",
		Scrollback: 500, StreamTimeout: 120, HistoryBatch: 100,
	}}
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
