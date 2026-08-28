// Query plumbing: ListSessions → GetSession (or CreateSession, if the
// backend has no sessions yet) → the fetched message list.
//
// F6 (the sessions list) and F12 (workspace-state persistence) do not exist
// yet, so there is no "current session" to open. Per the F3 build decision,
// this workspace picks one itself: the most recently updated session if any
// exist, else a fresh one — enough to have something real to render, with no
// switcher UI. F6/F12 replace this outright.
package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

type queryKind int

const (
	queryListSessions queryKind = iota
	queryGetSession
	queryCreateSession
)

// localID mints a request id. protocol.newID does the equivalent job inside
// that package but is unexported — a 16-byte random id needs no shared
// helper to duplicate correctly.
func localID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("chat-%p", &b)
	}
	return hex.EncodeToString(b[:])
}

// send calls w.sendFn, a struct field defaulting to doSend (set in New) so
// tests can substitute a recorder in place of a real wire send — what those
// tests want to exercise is the decision logic in
// onSessionList/onSessionCreated/onSession, not protocol.Client's own send
// path, which the protocol package already tests directly.
func (w *Workspace) send(cmdType string, kind queryKind, payload map[string]any) {
	w.sendFn(cmdType, kind, payload)
}

// doSend builds and sends one query envelope, remembering what kind of reply
// to expect. The reply arrives through Update's EnvelopeMsg case, which Root
// forwards to the active workspace — see root.go's ConnectedMsg and
// EnvelopeMsg cases.
//
// It calls Request, not the plainer Send, and never reads the channel
// Request returns — client.go's own doc says routing does not consume: a
// correlated reply reaches both its waiter and the model, so the model
// still renders it via the ordinary EnvelopeMsg path regardless. What
// Request buys here is registering a correlation waiter at all: without
// one, routeToWaiter never matches, every reply logs as "unroutable event"
// (client.go's signal for an actual contract bug — not the right label for
// a workspace's own query), and the ID this workspace picked is never the
// reason. handleEnvelope calls Release once it has consumed a reply, so the
// unread channel does not linger in the client's pending map.
func (w *Workspace) doSend(cmdType string, kind queryKind, payload map[string]any) {
	if w.client == nil {
		w.loadErr = "no backend connection"
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		w.loadErr = err.Error()
		return
	}
	id := localID()
	env := protocol.Envelope{ID: id, Type: cmdType, Payload: raw}
	if _, err := w.client.Request(env, 0); err != nil {
		w.loadErr = err.Error()
		return
	}
	w.pending[id] = kind
}

// beginBootstrap fires once — the first time the backend is known to be
// connected. A reconnect does not repeat it: F3 has no requirement to
// refresh a session that is already loaded, and re-issuing CreateSession on
// every reconnect would litter the database with empty sessions.
func (w *Workspace) beginBootstrap() {
	if w.bootstrapped || w.client == nil {
		return
	}
	w.bootstrapped = true
	w.loading = true
	w.loadErr = ""
	w.send(protocol.CmdListSessions, queryListSessions, map[string]any{"include_archived": false})
}

func (w *Workspace) handleEnvelope(env protocol.Envelope) {
	kind, ok := w.pending[env.CorrID]
	if !ok {
		return // not one of ours
	}
	delete(w.pending, env.CorrID)
	if w.client != nil {
		// The waiter Request registered is done once this workspace has its
		// copy — nothing here ever reads its channel, so releasing it is
		// what keeps it from sitting in the client's pending map forever.
		w.client.Release(env.CorrID)
	}

	if env.Type == protocol.EvError {
		var p struct {
			Error string `json:"error"`
		}
		_ = env.UnmarshalPayload(&p)
		w.loading = false
		w.loadErr = p.Error
		if w.loadErr == "" {
			w.loadErr = "the backend reported an error"
		}
		return
	}

	switch kind {
	case queryListSessions:
		w.onSessionList(env)
	case queryCreateSession:
		w.onSessionCreated(env)
	case queryGetSession:
		w.onSession(env)
	}
}

func (w *Workspace) onSessionList(env protocol.Envelope) {
	var p struct {
		Sessions []wireSession `json:"sessions"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.loading = false
		w.loadErr = err.Error()
		return
	}
	if len(p.Sessions) == 0 {
		// SessionRepository.fetch_all orders by updated_at DESC — [0] is
		// unused here on purpose: there is nothing to pick when the list is
		// empty, which is exactly this branch.
		w.send(protocol.CmdCreateSession, queryCreateSession, map[string]any{})
		return
	}
	// Already sorted most-recently-updated first.
	w.send(protocol.CmdGetSession, queryGetSession, map[string]any{"session_id": p.Sessions[0].ID})
}

func (w *Workspace) onSessionCreated(env protocol.Envelope) {
	var p struct {
		Session wireSession `json:"session"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.loading = false
		w.loadErr = err.Error()
		return
	}
	w.setSession(p.Session)
}

func (w *Workspace) onSession(env protocol.Envelope) {
	var p struct {
		Session wireSession `json:"session"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.loading = false
		w.loadErr = err.Error()
		return
	}
	w.setSession(p.Session)
}

func (w *Workspace) setSession(s wireSession) {
	w.loading = false
	w.loadErr = ""
	w.sessionID = s.ID
	w.sessionTitle = s.Title
	w.all = s.Messages
	w.pinned = true
	w.focus = -1
	w.topAbs = 0
	w.invalidateCache()
}

// Update is the Workspace interface's entry point. Root's Update forwards
// three things here: protocol.ConnectedMsg (so bootstrap can wait for a
// live connection instead of failing against a nil backend),
// protocol.EnvelopeMsg (query replies), and app.PasteMsg (see
// handlePastedBurst in scroll.go — recovering a hotkey the shared paste
// detector misclassified). Everything else — including every tea.KeyMsg the
// action registry did not consume — reaches here too, via
// Root.dispatchKey's fallthrough.
func (w *Workspace) Update(msg tea.Msg) (app.Workspace, tea.Cmd) {
	switch m := msg.(type) {
	case protocol.ConnectedMsg:
		w.beginBootstrap()
	case protocol.EnvelopeMsg:
		w.handleEnvelope(m.Envelope)
	case app.PasteMsg:
		return w, w.handlePastedBurst(m.Text)
	case tea.KeyMsg:
		return w, w.handleKey(m)
	}
	return w, nil
}
