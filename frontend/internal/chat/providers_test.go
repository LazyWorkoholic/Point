package chat

import (
	"encoding/json"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// recordingWorkspace captures what the bootstrap actually asks the backend
// for, in order, so the two-step ListProviders -> ListSessions handshake and
// the session it settles on are both observable without a live client.
type sentQuery struct {
	cmd     string
	kind    queryKind
	payload map[string]any
}

func recordingWorkspace(t *testing.T) (*Workspace, *[]sentQuery) {
	t.Helper()
	w := newTestWorkspace(t)
	var sent []sentQuery
	w.sendFn = func(cmdType string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmdType, kind, payload})
		w.pending[localID()] = kind
	}
	return w, &sent
}

func providerListEnvelope(t *testing.T, names ...string) protocol.Envelope {
	t.Helper()
	entries := make([]map[string]any, 0, len(names))
	for _, n := range names {
		entries = append(entries, map[string]any{"name": n, "status": "ok"})
	}
	raw, err := json.Marshal(map[string]any{"providers": entries})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Envelope{Type: protocol.EvProviderList, Payload: raw}
}

func sessionListEnvelope(t *testing.T, sessions ...wireSession) protocol.Envelope {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"sessions": sessions})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Envelope{Type: protocol.EvSessionList, Payload: raw}
}

func lastQuery(sent []sentQuery) sentQuery {
	if len(sent) == 0 {
		return sentQuery{}
	}
	return sent[len(sent)-1]
}

// The robustness gap this closes, end to end.
//
// A session is pinned to a provider, the backend only resolves that name on
// SEND, and CreateSession is reachable only from an empty session list -- so
// a stale row at the top of the list (a `provider='mock'` session left by
// old cancellation testing, say) used to be opened on every launch, fail
// every send, and offer no way out from inside the app.
func TestAnUnresolvableNewestSessionIsSkipped(t *testing.T) {
	w, sent := recordingWorkspace(t)

	w.onProviderList(providerListEnvelope(t, "ollama", "anthropic"))
	w.onSessionList(sessionListEnvelope(t,
		wireSession{ID: "stale", Provider: "mock"},  // newest, unusable
		wireSession{ID: "good", Provider: "ollama"}, // the one to land on
	))

	got := lastQuery(*sent)
	if got.kind != queryGetSession {
		t.Fatalf("the workspace did not open a session at all: %+v", got)
	}
	if got.payload["session_id"] != "good" {
		t.Errorf("opened %v, want the newest session with a provider this backend has",
			got.payload["session_id"])
	}
}

// Every session unusable is still a usable app: a fresh session inherits the
// global config, so it is resolvable by construction.
func TestAllSessionsUnresolvableStartsAFreshOne(t *testing.T) {
	w, sent := recordingWorkspace(t)

	w.onProviderList(providerListEnvelope(t, "ollama"))
	w.onSessionList(sessionListEnvelope(t,
		wireSession{ID: "stale-1", Provider: "mock"},
		wireSession{ID: "stale-2", Provider: "mock"},
	))

	if got := lastQuery(*sent); got.kind != queryCreateSession {
		t.Fatalf("expected a fresh session, got %+v", got)
	}
}

// A session that inherits the global provider (NULL in the database, empty
// on the wire) is always resolvable and must never be skipped.
func TestASessionInheritingTheGlobalProviderIsUsable(t *testing.T) {
	w, sent := recordingWorkspace(t)

	w.onProviderList(providerListEnvelope(t, "ollama"))
	w.onSessionList(sessionListEnvelope(t, wireSession{ID: "inherits", Provider: ""}))

	got := lastQuery(*sent)
	if got.kind != queryGetSession || got.payload["session_id"] != "inherits" {
		t.Fatalf("a session inheriting the global provider was not opened: %+v", got)
	}
}

// No provider list means no basis to reject anything, so the old behaviour
// stands: open the newest. A check this workspace could not run must never
// become a reason to refuse the user their own most recent session.
func TestWithoutAProviderListTheNewestSessionIsOpened(t *testing.T) {
	w, sent := recordingWorkspace(t)

	// No onProviderList call at all — w.providers stays nil.
	w.onSessionList(sessionListEnvelope(t,
		wireSession{ID: "newest", Provider: "mock"},
		wireSession{ID: "older", Provider: "ollama"},
	))

	got := lastQuery(*sent)
	if got.payload["session_id"] != "newest" {
		t.Errorf("opened %v, want the newest — an unknown provider set rejects nothing",
			got.payload["session_id"])
	}
}

// A backend reporting no providers at all leaves the set unknown rather than
// empty: an empty set would reject every pinned session and bootstrap a new
// one on every launch, which is a worse failure than the one being fixed.
func TestAnEmptyProviderListDoesNotRejectEverything(t *testing.T) {
	w, sent := recordingWorkspace(t)

	w.onProviderList(providerListEnvelope(t))
	if w.providers != nil {
		t.Fatalf("an empty provider list should leave the set unknown, got %v", w.providers)
	}
	w.onSessionList(sessionListEnvelope(t, wireSession{ID: "pinned", Provider: "ollama"}))

	if got := lastQuery(*sent); got.payload["session_id"] != "pinned" {
		t.Errorf("opened %+v, want the pinned session opened anyway", got)
	}
}

// The provider list is asked for FIRST, and asking for it is not optional:
// the session list has to follow whether it succeeded or not.
func TestBootstrapAsksForProvidersThenSessions(t *testing.T) {
	w, sent := recordingWorkspace(t)
	w.client = protocol.NewClient(protocol.Options{})

	w.beginBootstrap()
	if len(*sent) != 1 || (*sent)[0].kind != queryListProviders {
		t.Fatalf("bootstrap did not start with ListProviders: %+v", *sent)
	}

	w.onProviderList(providerListEnvelope(t, "ollama"))
	if got := lastQuery(*sent); got.kind != queryListSessions {
		t.Fatalf("the session list did not follow the provider list: %+v", got)
	}
}

// A refused ListProviders still has to reach the session list, and must not
// put the transcript behind an error banner: it is a soft dependency.
func TestARefusedProviderListStillOpensASession(t *testing.T) {
	w, sent := recordingWorkspace(t)
	withPending(w, "corr-1", queryListProviders)

	raw, err := json.Marshal(map[string]any{"error": "no such command"})
	if err != nil {
		t.Fatal(err)
	}
	w.handleEnvelope(protocol.Envelope{
		Type: protocol.EvError, CorrID: "corr-1", Payload: raw,
	})

	if w.loadErr != "" {
		t.Errorf("a refused ListProviders set loadErr = %q; it is a soft dependency", w.loadErr)
	}
	if got := lastQuery(*sent); got.kind != queryListSessions {
		t.Fatalf("the session list was never asked for: %+v", got)
	}
}

// TestApplyProviderDefaultRetrofitsTheOpenSession is the fix for a
// verification finding: the right pane's Status card ("provider · model",
// cards.go) never moved no matter what was picked in Settings, because
// persisting the default (state.json) only ever changes what a FUTURE
// session is created with — the session already open keeps whatever NULL
// provider it was given at ITS OWN creation. applyProviderDefault must also
// retrofit the session that is actually open right now.
func TestApplyProviderDefaultRetrofitsTheOpenSession(t *testing.T) {
	w, sent := recordingWorkspace(t)
	w.sessionID = "s1"

	w.applyProviderDefault(config.ProviderDefault{Provider: "ollama", Model: "qwen2.5-coder:3b"})

	got := lastQuery(*sent)
	if got.kind != querySetSessionProvider {
		t.Fatalf("SetSessionProvider was never sent: %+v", got)
	}
	if got.payload["session_id"] != "s1" || got.payload["provider"] != "ollama" ||
		got.payload["model"] != "qwen2.5-coder:3b" {
		t.Errorf("wrong payload: %+v", got.payload)
	}
}

// TestApplyProviderDefaultSkipsRetrofitWithNoOpenSession: mid-bootstrap,
// before the first GetSession/SessionCreated reply has landed, there is no
// session yet to retrofit — onSessionList's own CreateSession payload picks
// the default up a moment later regardless, so this must be a silent no-op,
// not a request naming an empty session_id.
func TestApplyProviderDefaultSkipsRetrofitWithNoOpenSession(t *testing.T) {
	w, sent := recordingWorkspace(t)
	w.sessionID = ""

	w.applyProviderDefault(config.ProviderDefault{Provider: "ollama", Model: "qwen3:8b"})

	if len(*sent) != 0 {
		t.Fatalf("SetSessionProvider was sent with no open session: %+v", *sent)
	}
}

// TestApplyBehaviourOverridesReplacesRatherThanMerges is F10b's own
// precedent, exercised at the persistence boundary: BehaviourOverrideSet's
// wire reply already carries the FULL resulting override map (B26 §1b), so
// applyBehaviourOverrides must replace w.state.BehaviourOverrides wholesale
// — unlike applyUIOverride's PATCH semantics (theme/background/mouse are
// independent fields), there is nothing here to merge.
func TestApplyBehaviourOverridesReplacesRatherThanMerges(t *testing.T) {
	w := newTestWorkspace(t)
	w.state.BehaviourOverrides = map[string]any{"chat.queue_depth": float64(9)} // stale
	var saved config.State
	saveCalls := 0
	w.saveStateFn = func(s config.State) error { saved = s; saveCalls++; return nil }

	w.applyBehaviourOverrides(map[string]any{"project.allow_outside_project": true})

	if saveCalls != 1 {
		t.Fatalf("saveStateFn called %d times, want 1", saveCalls)
	}
	if len(saved.BehaviourOverrides) != 1 {
		t.Fatalf("persisted overrides = %v, want exactly the new map", saved.BehaviourOverrides)
	}
	if v, _ := saved.BehaviourOverrides["project.allow_outside_project"].(bool); !v {
		t.Errorf("project.allow_outside_project = %v, want true", saved.BehaviourOverrides["project.allow_outside_project"])
	}
	if _, stale := saved.BehaviourOverrides["chat.queue_depth"]; stale {
		t.Error("the stale queue_depth entry survived — applyBehaviourOverrides must replace, not merge")
	}
}

// TestOnSessionProviderUpdatedRefreshesTheStatusCard confirms the reply
// actually reaches cards.go's own read of this workspace's state, and that
// a reply for a DIFFERENT session (the active session changed while the
// request was in flight) is dropped rather than relabelling the wrong card.
func TestOnSessionProviderUpdatedRefreshesTheStatusCard(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"

	raw, err := json.Marshal(map[string]any{
		"session_id": "s1", "provider": "ollama", "model": "qwen2.5-coder:3b",
	})
	if err != nil {
		t.Fatal(err)
	}
	w.onSessionProviderUpdated(protocol.Envelope{Payload: raw})

	if w.sessionProvider != "ollama" || w.sessionModel != "qwen2.5-coder:3b" {
		t.Fatalf("Status card fields not updated: provider=%q model=%q", w.sessionProvider, w.sessionModel)
	}

	stale, err := json.Marshal(map[string]any{
		"session_id": "s0-no-longer-open", "provider": "openai", "model": "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	w.onSessionProviderUpdated(protocol.Envelope{Payload: stale})

	if w.sessionProvider != "ollama" || w.sessionModel != "qwen2.5-coder:3b" {
		t.Fatalf("a reply for a session that is no longer open overwrote the card: provider=%q model=%q",
			w.sessionProvider, w.sessionModel)
	}
}
