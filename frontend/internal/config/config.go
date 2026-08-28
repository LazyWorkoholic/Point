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
	return Config{UI: UI{Theme: "turquoise", Background: "transparent"}}
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
