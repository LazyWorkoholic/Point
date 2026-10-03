package chat

import (
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The provider names this backend actually registered, and the rule that
// keeps a stale session row from making the app unusable.
//
// A session is pinned to a provider (`sessions.provider`, nullable). The
// backend resolves that name only when something is SENT -- nothing looks at
// it when the session is opened -- so a session naming a provider this
// backend does not have opens perfectly and then fails every single send
// with "No provider registered as 'x'".
//
// On its own that is survivable. What made it unusable is that the frontend
// opens the MOST RECENTLY UPDATED session at startup and CreateSession is
// only reachable from an EMPTY session list (protocol.go's onSessionList) --
// so there is no way, from inside the app, to get to any other session. One
// stale row at the top of the list and every send fails, on every launch,
// until someone edits the database by hand.
//
// Real example: three `provider='mock'` sessions from B5/B6-era cancellation
// testing, written to the live profile instead of a scratch one. `mock` is
// registered only under POINT_MOCK=1, so a normal launch could not resolve
// it. It looked intermittent because it self-heals the moment any newer
// session is written, and returns as soon as those rows are newest again.

// knownProvider reports whether a session's provider can be resolved here.
//
// Three cases, and the permissive ones are deliberate:
//
//   - An EMPTY provider means "inherit the global config" (managers/
//     session.py: NULL is not "missing"), which is always resolvable.
//   - An UNKNOWN provider set (the list was never fetched, or the request
//     failed) means this workspace has no basis to reject anything, so it
//     rejects nothing and behaves exactly as it did before the check
//     existed.
//   - Otherwise the name has to be one the backend reported.
//
// Health is deliberately NOT consulted. A registered-but-unreachable
// provider (Ollama still starting) is a session worth opening; the send
// failure that follows is honest and temporary, and skipping past it would
// hide the user's own most recent work for a reason that fixes itself.
func (w *Workspace) knownProvider(name string) bool {
	if name == "" || w.providers == nil {
		return true
	}
	return w.providers[name]
}

// onProviderList records the names and then asks for the session list,
// which is the only reason this query runs at all.
func (w *Workspace) onProviderList(env protocol.Envelope) {
	var p struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		// Same posture as a refused ListProviders: no basis to reject a
		// session is not a reason to open none.
		w.log.Warn("chat: ListProviders did not decode, opening the newest session unchecked",
			"err", err.Error())
		w.listSessions()
		return
	}

	// A backend that reports no providers at all leaves the map nil rather
	// than empty: empty would reject every non-null-provider session and
	// bootstrap a new one on every launch, which is a worse failure than
	// the one this is here to prevent.
	if len(p.Providers) > 0 {
		w.providers = make(map[string]bool, len(p.Providers))
		for _, entry := range p.Providers {
			if entry.Name != "" {
				w.providers[entry.Name] = true
			}
		}
	}
	w.listSessions()
}

// ---------------------------------------------------------------------------
// F10: the stored provider default
// ---------------------------------------------------------------------------

// providerMissing is UI-03's "no provider configured" state (F10): true
// once Root has told this workspace, via app.DiagnosticsMsg, that
// r.activeProvider is nil — nothing has been chosen through Point's own UI
// yet. See config\state.go's doc on Provider for the one accepted gap this
// check has — a hand-edited config.toml that already resolves fine would
// still show this as true, since nothing on the wire lets this package tell
// that case apart from a genuinely unconfigured one.
//
// Deliberately NOT w.state.Provider == nil, even though that is this exact
// workspace's own copy of the same fact. A zero-valued config.State (every
// chat test that builds a bare Workspace via New(Options{}), never having
// gone through Root's first-run check at all) would make THAT check true by
// simple default, locking the input for every one of them — which is
// exactly what happened the first time this read w.state.Provider directly.
// Routing it through the same DiagnosticsMsg push ConnState/backendDown
// already use means the lock can only ever turn on because ROOT decided it
// should (ultimately from opts.State.Provider at startup), never as an
// accidental side effect of a field this package was never told to care
// about defaulting to its zero value.
//
// Read by stream.go's inputLocked (query input disabled) and render.go's
// queryLines (the reason shown in its place) — the same two call sites
// connectionDown already feeds, and for the same reason: one check behind
// both halves of the state, so they can never disagree.
func (w *Workspace) providerMissing() bool {
	return w.noProviderConfigured
}

// applyProviderDefault is app.ProviderDefaultSetMsg's handler: persist the
// choice Root just made (Settings/the first-run chooser), immediately, not
// debounced — this is a deliberate, discrete user action, not a keystroke.
//
// Also retrofits the CURRENTLY OPEN session, via SetSessionProvider — a
// verification finding: persisting the default only ever changes what a
// FUTURE session is created with (onSessionList's CreateSession payload);
// the session already open was created before this choice existed (NULL
// provider, "inherit from global config") and nothing was telling it
// otherwise, so cards.go's Status card ("provider · model") sat frozen on
// "—" or a stale value no matter what was picked in Settings afterward.
// SetSessionProvider is the existing, already-wired-on-the-backend event
// for exactly "this session's provider/model, now" (B4) — this is simply
// the first Go caller of it. Session-less (sessionID == "", e.g. mid-
// bootstrap before the first CreateSession/GetSession reply has landed) is
// a silent no-op: there is nothing to retrofit yet, and onSessionList's own
// payload will pick the default up when that session is created a moment
// later anyway.
func (w *Workspace) applyProviderDefault(d config.ProviderDefault) {
	w.state.Provider = &d
	if w.sessionID != "" {
		w.send(protocol.CmdSetSessionProvider, querySetSessionProvider, map[string]any{
			"session_id": w.sessionID, "provider": d.Provider, "model": d.Model,
		})
	}
	if w.saveStateFn == nil {
		return
	}
	if err := w.saveStateFn(w.state); err != nil {
		w.log.Warn("saving the provider default", "err", err)
	}
}

// applyUIOverride is app.UIOverrideSetMsg's handler — Settings' Appearance
// tab (F10), the same immediate-persist rule applyProviderDefault uses.
//
// patch is a PATCH, not a full value (app.UIOverrideSetMsg's own doc): only
// the field the user just changed is set, so this MERGES it into whatever
// override already exists rather than replacing it outright. Replacing
// would mean toggling the mouse after switching the theme throws the theme
// choice away again — each call site only ever sends the one field it owns.
func (w *Workspace) applyUIOverride(patch config.UIOverride) {
	if w.state.UI == nil {
		w.state.UI = &config.UIOverride{}
	}
	if patch.Theme != "" {
		w.state.UI.Theme = patch.Theme
	}
	if patch.Background != "" {
		w.state.UI.Background = patch.Background
	}
	if patch.Mouse != nil {
		w.state.UI.Mouse = patch.Mouse
	}
	if w.saveStateFn == nil {
		return
	}
	if err := w.saveStateFn(w.state); err != nil {
		w.log.Warn("saving the appearance override", "err", err)
	}
}

// applyBehaviourOverrides is app.BehaviourOverridesSetMsg's handler (F10b)
// — the same immediate-persist rule applyProviderDefault/applyUIOverride
// use, but a full REPLACE rather than a merge: unlike UIOverrideSetMsg
// (one field at a time, because theme/background/mouse are independent
// choices), BehaviourOverrideSet's own wire reply already IS the complete
// resulting override map (B26 §1b echoes it back whole on every change),
// so there is nothing to merge — whatever Root just received from the
// backend is, by definition, every override currently active.
func (w *Workspace) applyBehaviourOverrides(overrides map[string]any) {
	w.state.BehaviourOverrides = overrides
	if w.saveStateFn == nil {
		return
	}
	if err := w.saveStateFn(w.state); err != nil {
		w.log.Warn("saving the behaviour overrides", "err", err)
	}
}
