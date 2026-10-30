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

	// LastWorkspace is the workspace that was last ACTIVE. F1 wrote it as
	// "the workspace to open on next launch", and F12 narrows that: it is
	// what the startup chooser (UI-12) pre-selects and names on its
	// `last:` line, and nothing loads a workspace without the user
	// choosing one. F12's Build section is explicit — "Do not auto-load
	// the last workspace", because loading the Editor's file tree and
	// project context when the user only wants to ask a question wastes
	// time and memory. A last-active record and an auto-load are different
	// things, and only the first one is wanted.
	//
	// Owned by app.Root (with WorkspaceState); see saveStateTo.
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

	// BehaviourOverrides is F10b's Behaviour tab: the keys B26's
	// SetBehaviourOverride accepts (chat.queue_depth,
	// limits.max_concurrent_tasks, project.allow_outside_project,
	// index.respect_gitignore, research.stale_after_days), each mapped to
	// whatever value the user last set it to. This is NOT a cache of what
	// config.toml holds — Go never reads that table, and B26 deliberately
	// built no way to ask the backend what it is either (see F10b's own
	// Decision 2) — it exists for exactly one reason: the backend's own
	// override layer is process-lifetime-only BY DESIGN
	// (appconfig_overrides.py), so a restart of either process loses it on
	// the backend side, and this is what lets the frontend re-send every
	// entry via SetBehaviourOverride the moment a connection comes back up
	// (app\root.go's replayBehaviourOverrides, fired from every
	// protocol.ConnectedMsg — first launch and every later reconnect
	// alike) rather than silently leaving an override reverted.
	//
	// nil/empty means nothing has ever been overridden through Point's own
	// UI — there is nothing to replay, and the Behaviour tab's rows all
	// read "default" (F10b's Decision 2: never a guessed config.toml
	// value, only override state this process actually set itself).
	BehaviourOverrides map[string]any `json:"behaviour_overrides,omitempty"`

	// Holder is F13b's lock identity: the WHOLE composed
	// `<username>@<full-id>` string, generated ONCE and persisted here as
	// one opaque value, never regenerated on relaunch.
	//
	// B32's Decision 4 settled the shape and the reason, and the reason
	// is not cosmetic. `file_locks.holder` comparison is whole-string
	// equality, and a username read live is not a stable input -- it
	// changes with a renamed Windows account, a different signed-in user
	// on the same profile, or anything else that alters USERNAME. Any of
	// those would silently make Point a DIFFERENT holder and leave it
	// unable to re-acquire its own lock after a restart. Composing once
	// and persisting the result makes the holder as stable as the id
	// inside it. The backend neither generates nor validates it; it only
	// compares.
	//
	// Owned by main.go, which generates it before chat's copy of this
	// State is taken -- see saveStateTo, and the keep-if-zero clause that
	// stops chat's next draft save from erasing it.
	Holder string `json:"holder,omitempty"`

	// WorkspaceState is F12's suspended state, one opaque blob per
	// workspace id, and "opaque" is the contract rather than laziness.
	//
	// What a workspace puts in its blob is that workspace's own business:
	// app.Root calls Workspace.Suspend(), stores the bytes it is handed
	// under the workspace's id, and hands them back to Resume() unread.
	// It never unmarshals one, which is the only way "the root never reads
	// a workspace's internal fields" survives being given a persistence
	// mechanism. chat.Workspace's blob is F18's `{version, anchor:
	// {anchor_seq, row_offset, pinned_to_bottom}}` and is versioned
	// INSIDE itself, so a workspace can change its own shape without this
	// file's StateVersion moving.
	//
	// It is roughly 2KB per workspace by design — position, not content.
	// Content comes back from the database on Resume, which is the same
	// code path as opening a week-old session.
	//
	// This field and LastWorkspace are the two whose owner is app.Root;
	// every other field here is chat.Workspace's. See saveStateTo for how
	// two owners share one file without erasing each other.
	WorkspaceState map[string]json.RawMessage `json:"workspace_state,omitempty"`

	// TerminalHistory is F14's command history, and it is keyed
	// DIFFERENTLY from WorkspaceState on purpose.
	//
	// The episode doc states two separately-scoped requirements, not one:
	// the terminal pane's SIZE MODE is "remembered per workspace" (that one
	// lives in editor's own suspendBlob, inside WorkspaceState, because it
	// is explicitly specified to "travel in Suspend()"), while command
	// HISTORY is "persisted per project". Those are different axes. One
	// editor workspace's `projectRoot` can rebind to a DIFFERENT project
	// during its own lifetime (editor\protocol.go's onProject, resolved
	// from the active session) -- F12 does not give one project one fixed
	// workspace id -- so keying history by workspace id would silently
	// carry one project's commands into another's pane the moment the
	// binding changes. Keying it by the key terminalHistoryKey computes
	// (the resolved project id, or the resolved directory when no project
	// is found) makes the pane switch to the right bucket the moment
	// onProject reassigns it, with no project ever seeing another's lines.
	//
	// Owned and written by editor.Workspace alone, through
	// SaveTerminalHistory -- a third writer alongside chat.Workspace's
	// whole-struct snapshot and app.Root's LastWorkspace/WorkspaceState/
	// Holder, arbitrated by saveStateTo's keep-if-zero clause the same way
	// Holder is.
	TerminalHistory map[string][]string `json:"terminal_history,omitempty"`
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
	// ProjectID is Demo mode's (F10c): the shipped sample project's id,
	// set once Demo is selected and its ProjectInit reply lands. Empty for
	// every other provider — nothing else ever writes it. Forwarded on the
	// next CreateSession (chat\protocol.go's onSessionList), never used to
	// rebind a session that already exists — see F10c's own Decision 2 for
	// why that is a real, inherited limit rather than solved here.
	ProjectID string `json:"project_id,omitempty"`
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

// SaveWorkspaceState writes ONLY the two fields app.Root owns, keeping
// whatever is on disk for the rest. It is Root's single writer of this file.
func SaveWorkspaceState(lastWorkspace string, blobs map[string]json.RawMessage) error {
	return saveWorkspaceStateTo(StatePath(), lastWorkspace, blobs)
}

// SaveTerminalHistory writes ONE project's bucket, keeping the rest of the
// map -- and the rest of the file -- untouched. F14's own writer, parallel
// to SaveWorkspaceState: re-read, mutate the one thing this call owns,
// write back, exactly so a save racing chat's or Root's own write can
// never carry a stale copy of either.
func SaveTerminalHistory(key string, lines []string) error {
	return saveTerminalHistoryTo(StatePath(), key, lines)
}

func saveTerminalHistoryTo(path, key string, lines []string) error {
	st, err := loadStateFrom(path)
	if err != nil {
		// Advisory, same contract as saveWorkspaceStateTo's own: the State
		// returned is always usable, so the write proceeds rather than
		// losing this project's history because an unrelated field on disk
		// went bad.
		st = DefaultState()
	}
	if st.TerminalHistory == nil {
		st.TerminalHistory = map[string][]string{}
	}
	st.TerminalHistory[key] = lines
	return saveStateTo(path, st)
}

func saveWorkspaceStateTo(path, lastWorkspace string, blobs map[string]json.RawMessage) error {
	st, err := loadStateFrom(path)
	if err != nil {
		// Advisory, exactly as LoadState's own contract says: the State
		// returned is always usable (defaults for a missing or corrupt
		// file), so the write proceeds rather than losing this
		// workspace's position because an unrelated field went bad.
		st = DefaultState()
	}
	st.LastWorkspace = lastWorkspace
	st.WorkspaceState = blobs
	return saveStateTo(path, st)
}

// saveStateTo serialises the whole struct — which is why it has to answer a
// question F1 never had: this file now has TWO owners.
//
// chat.Workspace owns Drafts, Provider, UI and BehaviourOverrides, and
// writes them by serialising an in-memory copy of the WHOLE State taken at
// startup (chat\draft.go's persistDraft). app.Root owns LastWorkspace and
// WorkspaceState (F12). Two writers each serialising their own snapshot
// means the loser of any pair of writes silently erases the winner's
// fields: type a character and the scroll anchor is gone; unload and the
// draft you just typed is gone.
//
// Rather than a second file, a shared mutable store, or a lock, the two
// halves are kept apart by one rule that needs no coordination: a save
// that leaves Root's fields at their zero value keeps whatever the file
// already holds for them. main.go hands chat.Workspace a State with both
// cleared, so chat's writes always take that branch and can never carry a
// stale copy of either; Root's own writes go through
// saveWorkspaceStateTo, which re-reads the file first and so can never
// carry a stale copy of chat's.
//
// This supersedes chat.go's note that LastWorkspace "round-trips" through
// the workspace's copy. It no longer does, and it must not — a round trip
// IS a stale copy, which is the thing this rule exists to stop.
//
// # F13b added a THIRD field to the keep-if-zero clause, and had to
//
// Holder is written exactly once, by main.go, on the launch that first
// generates it. chat.Workspace writes the WHOLE struct from its startup
// snapshot, and that snapshot is taken in main.go at line `chatState :=
// state` — BEFORE the holder exists on a first run. So without the clause
// below, the very next keystroke in the query input would serialise a
// State whose Holder is "" over the one just written, and Point would
// generate a brand new identity on every launch: a lock taken in one
// session could never be re-acquired or released by the next, and every
// reopen would have to wait out B7's 60-second staleness timeout against
// its own abandoned row.
//
// Clearing it in main.go's chatState copy (the way LastWorkspace and
// WorkspaceState are cleared) is what makes chat's writes take this
// branch, and this clause is what makes that branch preserve it. Both
// halves are required; either alone still loses the value.
func saveStateTo(path string, st State) error {
	st.Version = StateVersion

	// F14 adds TerminalHistory to this clause for the same reason Holder
	// was added to it: chat.Workspace's whole-struct snapshot never sets
	// it (chat never reads or writes terminal history), so without this,
	// the very next keystroke's draft save would serialise a State whose
	// TerminalHistory is nil over whatever SaveTerminalHistory last wrote.
	if st.LastWorkspace == "" || st.WorkspaceState == nil || st.Holder == "" || st.TerminalHistory == nil {
		if cur, err := loadStateFrom(path); err == nil {
			if st.LastWorkspace == "" {
				st.LastWorkspace = cur.LastWorkspace
			}
			if st.WorkspaceState == nil {
				st.WorkspaceState = cur.WorkspaceState
			}
			if st.Holder == "" {
				st.Holder = cur.Holder
			}
			if st.TerminalHistory == nil {
				st.TerminalHistory = cur.TerminalHistory
			}
		}
	}

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
