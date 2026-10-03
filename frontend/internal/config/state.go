package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// StateVersion is bumped when the shape of state.json changes. A file from a
// future version is ignored rather than half-read.
const StateVersion = 1

// State is the machine-written half.
//
// F1 deliberately stores almost nothing. Workspace state — scroll position,
// draft text, open files, cursor — is F12's, and putting a field here for it
// now would mean F12 inherits a shape it did not choose. What F1 owns is the
// read/write path itself: versioned, atomic, and tolerant of a missing or
// corrupt file, because every later episode depends on those three properties
// and none of them is worth debugging twice.
type State struct {
	Version int `json:"version"`

	// LastWorkspace is the workspace to open on next launch. It is the one
	// piece of state F1 genuinely produces, and it exercises the round trip.
	LastWorkspace string `json:"last_workspace,omitempty"`

	// Drafts is F4's own addition, not F12's eventual workspace-state shape.
	// F12 owns scroll position, open files and cursor in general; F4.md asks
	// specifically for unsent input text and cursor position to survive a
	// restart, keyed by session so switching sessions (F6) does not cross
	// drafts. Per the project's own rule for a shape a later episode will
	// formally own: this is a narrower, differently-named local type, and
	// F12's real WorkspaceState must be adaptable TO Draft, not identical to
	// it — see F04-query-input.md's Handoff for the exact call site.
	Drafts map[string]Draft `json:"drafts,omitempty"`

	// Provider is F10's own addition: the provider+model (+ optional effort)
	// the user picked in the first-run chooser or Settings' Providers tab.
	// nil means no choice has been made yet through Point's own UI — the
	// first-run trigger (app\root.go) reads exactly this nil-ness, and
	// nothing else, to decide whether to open the chooser.
	//
	// This is NOT the same thing as config.toml's `provider.default` /
	// `model.default` (backend\appconfig.py), which Point must never write.
	// A hand-edited config.toml can make SendMessage resolve perfectly well
	// with this field still nil — in that one case the chooser still opens
	// once, harmlessly (picking a provider here only ever ADDS a state.json
	// override; it never touches config.toml), but nothing is broken by
	// leaving it unset. There is no wire query that would let the frontend
	// tell the two cases apart, since Go's own Config type deliberately
	// parses only the [ui] table (config.go's own doc comment) and the
	// resolution chain is backend-only (agents\executor.py's
	// resolve_provider_model_effort). Documented here rather than solved.
	//
	// chat.Workspace is the sole reader and writer of this field, the same
	// single-writer rule Drafts already follows — see chat\providers.go.
	Provider *ProviderDefault `json:"provider,omitempty"`

	// UI is F10's Appearance tab: theme/background/mouse overrides on top
	// of config.toml's [ui] table, for the same reason Provider exists
	// instead of writing config.toml — Settings never writes the user's
	// file. nil means "nothing overridden, config.toml (or its defaults)
	// governs" — main.go applies this on top of config.Load()'s result
	// before building the theme or the Bubble Tea program's mouse option.
	UI *UIOverride `json:"ui,omitempty"`
}

// ProviderDefault is the active provider+model Point's own UI will pass on
// every new CreateSession from here on (chat\protocol.go) — see state.go's
// doc on Provider for why this exists instead of config.toml's
// provider.default/model.default.
type ProviderDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Effort is optional — only a model whose capabilities say
	// supports_effort may ever have this set to anything but "".
	Effort string `json:"effort,omitempty"`
}

// UIOverride is Settings' Appearance tab, on top of config.toml's [ui]
// table. Each field is independently optional: changing the theme must not
// force a background choice, and vice versa.
type UIOverride struct {
	Theme      string `json:"theme,omitempty"`
	Background string `json:"background,omitempty"`
	// Mouse is a pointer so "never touched" (nil, mouse stays on — F1's
	// existing default) is distinguishable from "explicitly turned off"
	// (false).
	Mouse *bool `json:"mouse,omitempty"`
}

// Draft is one session's unsent input.
type Draft struct {
	Text   string `json:"text"`
	Cursor int    `json:"cursor"` // a rune index into Text, not a byte offset
}

func DefaultState() State {
	return State{Version: StateVersion}
}

// LoadState reads state.json.
//
// Like Load, the error is advisory and the State is always usable. A missing
// file is a first run. A corrupt one is a machine-written file that went bad,
// which is Point's problem and not the user's — defaults, plus the error, so
// it can be logged.
func LoadState() (State, error) {
	return loadStateFrom(StatePath())
}

func loadStateFrom(path string) (State, error) {
	st := DefaultState()

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return st, nil
		}
		return st, err
	}

	var parsed State
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return st, err
	}
	if parsed.Version != StateVersion {
		return st, nil
	}
	return parsed, nil
}

// SaveState writes state.json atomically: temp file, then rename.
//
// The same discipline the backend uses for credentials.toml, for the same
// reason — an interrupted write must leave the previous file intact rather
// than a truncated one. On Windows os.Rename over an existing file works;
// what it will not do is rename across volumes, and the temp file is
// deliberately created in the destination directory so that never comes up.
func SaveState(st State) error {
	return saveStateTo(StatePath(), st)
}

func saveStateTo(path string, st State) error {
	st.Version = StateVersion

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
