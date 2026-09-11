package chat

import (
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
