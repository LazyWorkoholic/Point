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
	// queryListTasks is S11b's: the durable task rows a reloaded
	// transcript joins against to rebuild the footer of a reply that was
	// never persisted. See reload.go's onTaskList.
	queryListTasks
	// queryListProviders runs BEFORE the session list, and only so that
	// onSessionList can skip a session pinned to a provider this backend
	// does not have. See beginBootstrap.
	queryListProviders
	// queryGetContextReport is S13d's once-per-load pull: a reloaded
	// session has no per-message composition snapshot to seed w.usage-style
	// from, so setSession asks explicitly. See requestContextReport and
	// onContextReport.
	queryGetContextReport
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
		if kind.decorative() {
			// Same treatment handleEnvelope already gives these kinds when
			// the backend REFUSES them: a lost ListTasks or GetContextReport
			// costs a footer or the Status card's row 1, never the
			// transcript. Before this, the transport error became loadErr
			// and blanked the viewport — the same "banner from whichever
			// path failed" defect writeSend had (see connection.go).
			w.log.Warn("chat: query not sent", "type", cmdType, "err", err)
			return
		}
		w.loadErr = err.Error()
		return
	}
	w.pending[id] = kind
}

// decorative reports whether a query only DECORATES a session that is
// already on screen. A load query failing leaves nothing to show, so it
// earns the loadErr screen; a decorative one never does.
func (k queryKind) decorative() bool {
	return k == queryListTasks || k == queryGetContextReport
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
	// Providers first, sessions second. The frontend cannot otherwise tell
	// that the session it is about to open names a provider this backend
	// never registered -- and since CreateSession is only reachable from an
	// EMPTY session list, opening such a session leaves no way back out of
	// it from inside the app: every send fails, forever, against whichever
	// session happens to be newest. See onSessionList.
	w.send(protocol.CmdListProviders, queryListProviders, map[string]any{})
}

// onConnected handles every ConnectedMsg: the first one bootstraps, and a
// RECONNECT (Root forwards those too, Restarted=true) clears what the outage
// left behind.
//
// Before this, a reconnect called beginBootstrap and nothing else — which
// returns at once when bootstrapped is already true. A send attempted while
// the backend was dead sets w.loadErr from the client's own "backend is not
// connected" (stream.go's writeSend), and render.go shows a non-empty loadErr
// as the "! ..." banner IN PLACE of the transcript. Only beginBootstrap and
// setSession ever cleared it, so after ctrl+w r the backend was genuinely
// back while the viewport kept saying it was not, until the app restarted.
//
// Clearing unconditionally is safe: the transcript under the banner was
// never discarded, and the send that failed keeps its own errText footer on
// its reply entry, so the failure stays visible where it happened. A session
// that never finished loading (its replies died with the old process, which
// would also leave w.loading stuck true) bootstraps again instead — the new
// process is a legitimate reason to retry, and this cannot litter the
// database with sessions the way re-bootstrapping a LOADED session would.
func (w *Workspace) onConnected() {
	if !w.bootstrapped {
		w.beginBootstrap()
		return
	}
	w.loadErr = ""
	if w.sessionID == "" {
		w.bootstrapped = false
		w.beginBootstrap()
	}
}

// listSessions is the second half of the bootstrap, reached however the
// provider list turned out — including when it failed. A provider list this
// workspace could not get is a reason to fall back to the old "just open the
// newest" behaviour, never a reason not to open a session at all.
func (w *Workspace) listSessions() {
	w.send(protocol.CmdListSessions, queryListSessions, map[string]any{"include_archived": false})
}

// handleEnvelope routes a reply. F5's stream events are tried FIRST: they
// share a corr_id with the CmdSendMessage that caused them, arrive many to
// a request, and are matched by live entry rather than by the one-shot
// query map below — which would consume the first of them and drop the
// rest.
func (w *Workspace) handleEnvelope(env protocol.Envelope) tea.Cmd {
	if cmd, taken := w.handleStreamEvent(env); taken {
		return cmd
	}
	// S13d: the push half of ContextReport. It shares its wire type with
	// GetContextReport's reply but carries no corr_id (runtime.py's
	// _push_context_report emits with request_id=None) and no task_id, so
	// neither handleStreamEvent above nor the pending-map lookup below ever
	// matches it -- checked directly against client.go's routing order,
	// which is corr_id, then task_id, then session_id: this is exactly the
	// RouteSession case. Caught here, before the pending lookup would
	// otherwise silently drop it as "not one of ours".
	if env.Type == protocol.EvContextReport && env.CorrID == "" {
		w.onContextReport(env)
		return nil
	}
	kind, ok := w.pending[env.CorrID]
	if !ok {
		return nil // not one of ours
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
		if kind == queryListProviders {
			// Soft: the session list still has to be asked for. Without
			// the provider names onSessionList simply falls back to
			// opening the newest session, which is what it did before
			// this query existed.
			w.log.Warn("chat: ListProviders refused, opening the newest session unchecked",
				"err", p.Error)
			w.listSessions()
			return nil
		}
		if kind == queryListTasks {
			// NOT w.loadErr. A refused ListTasks costs a footer on a
			// reply that produced no text; the transcript already on
			// screen is correct without it. Replacing a loaded session
			// with an error banner over that would be a far bigger
			// failure than the one being reported.
			w.log.Warn("chat: ListTasks refused, reloaded footers not rebuilt", "err", p.Error)
			return nil
		}
		if kind == queryGetContextReport {
			// NOT w.loadErr, same reasoning as queryListTasks: a refused
			// pull costs the Status card's row 1 (it stays the dim
			// placeholder) and nothing else. The transcript already on
			// screen is correct without it, and the next completed turn's
			// push will populate it anyway.
			w.log.Warn("chat: GetContextReport refused, Status card stays unwired this load", "err", p.Error)
			return nil
		}
		w.loading = false
		w.loadErr = p.Error
		if w.loadErr == "" {
			w.loadErr = "the backend reported an error"
		}
		return nil
	}

	switch kind {
	case queryListProviders:
		w.onProviderList(env)
	case queryListSessions:
		w.onSessionList(env)
	case queryCreateSession:
		w.onSessionCreated(env)
	case queryGetSession:
		w.onSession(env)
	case queryListTasks:
		w.onTaskList(env)
	case queryGetContextReport:
		w.onContextReport(env)
	}
	return nil
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
	// F6's Sessions card: kept beyond the bootstrap decision below, which
	// only reads it to pick one to open.
	w.sessions = p.Sessions
	// Already sorted most-recently-updated first, so the newest session
	// this backend can actually USE is the first one whose provider it
	// registered. Skipping is what stops one stale row -- a session
	// pinned to a provider that no longer exists -- from making every
	// send fail on every launch with no way out from inside the app.
	// See providers.go for the full mechanism and the real case.
	for _, session := range p.Sessions {
		if !w.knownProvider(session.Provider) {
			w.log.Warn("chat: skipping a session pinned to an unavailable provider",
				"session", session.ID, "provider", session.Provider)
			continue
		}
		w.send(protocol.CmdGetSession, queryGetSession, map[string]any{"session_id": session.ID})
		return
	}
	// Nothing usable, including the empty-list case: start fresh. A new
	// session inherits the global config, so it is resolvable by
	// construction — which makes this the branch that guarantees the app
	// always reaches a usable state.
	w.send(protocol.CmdCreateSession, queryCreateSession, map[string]any{})
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
	// Not in w.sessions yet — ListSessions ran before this one existed.
	w.sessions = append(w.sessions, p.Session)
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
	w.sessionProvider = s.Provider
	w.sessionModel = s.Model
	// Every row the database returned, rendered verbatim — nothing merged,
	// relabelled or dropped. See chat.go's `all` field for why the
	// retry-fold that briefly lived here was removed, and stays removed.
	//
	// What S11b adds is a REORDER and a decoration, both keyed on B12's
	// persisted task_id: an answer moves under the question it answered,
	// and a row that live rendered as a slot or with a footer gets the
	// entry that renders it that way again. Neither invents a row and
	// neither reads position, adjacency or time. See reload.go.
	w.all = pairReloaded(s.Messages)
	w.decorateReloaded()
	// F6's Status card "tokens / cost" line: the last turn this session
	// actually ran, not this session's total — the newest message that
	// carries a non-zero usage, scanning backward since a live entry (below)
	// takes over once one streams. Zero value (nothing yet) renders as
	// nothing, not a fabricated 0.
	w.usage = wireUsage{}
	for i := len(w.all) - 1; i >= 0; i-- {
		if w.all[i].Usage.TotalTokens > 0 {
			w.usage = w.all[i].Usage
			break
		}
	}
	// S13d's Status card row 1: no per-message composition snapshot exists
	// to seed from the way w.usage seeds above, so this resets to "nothing
	// yet" here and requestContextReport (below) is what actually populates
	// it for the newly-open session.
	w.contextReport = nil
	// F5: live entries and the queue belong to the session that made them.
	// Nothing switches sessions yet (F6 owns that), but leaving them
	// attached across a setSession would render one session's stream into
	// another's transcript the day it does.
	w.live = nil
	w.queued = nil
	w.pinned = true
	w.focus = -1
	w.topAbs = 0
	w.topRow = 0
	w.invalidateCache()
	// F4: the draft is scoped per session, so it is only loadable once
	// sessionID is known — this is also the point F3 already resets
	// everything else session-shaped, so a draft never bleeds across
	// sessions.
	w.restoreDraft()
	// Last, and deliberately after the transcript is complete: the tasks
	// join only decorates, so nothing waits on it. See requestTaskRows.
	w.requestTaskRows()
	w.requestContextReport()
}

// Update is the Workspace interface's entry point. Root's Update forwards
// protocol.ConnectedMsg (so bootstrap can wait for a live connection
// instead of failing against a nil backend), protocol.EnvelopeMsg (query
// replies), app.PasteMsg, app.EnterBrowseMsg/app.FocusInputMsg (F4's focus
// arbitration — see app\focus.go), and every tea.KeyMsg the action registry
// did not consume, via Root.dispatchKey's fallthrough. draftSaveMsg and
// OpenAttachmentPickerMsg are this package's own messages, arriving the
// same way: Root has no case for them, so its Update falls through to
// r.current().Update, which is here.
//
// tea.KeyMsg and app.PasteMsg both branch on w.input.focused: focused
// routes to the query input (input_keys.go); unfocused routes to F3's
// existing transcript browsing (handleKey/handlePastedBurst, scroll.go) —
// F4's whole answer to the two now colliding letter-for-letter on plain
// typing.
func (w *Workspace) Update(msg tea.Msg) (app.Workspace, tea.Cmd) {
	switch m := msg.(type) {
	case protocol.ConnectedMsg:
		w.onConnected()
	case app.DiagnosticsMsg:
		// F6: the root-owned Task card snapshot and connection state — see
		// chat.go's doc comment on why this workspace does not fetch either
		// itself. ConnState drives the "backend down -> dim placeholders"
		// rule (F06-right-pane.md's Done-when #7): a card must never keep
		// showing its last real numbers once the connection Placeholder
		// itself would call StateFailed.
		//
		// Seq guards against a real race, not a hypothetical one: each
		// DiagnosticsMsg is delivered by its own tea.Cmd goroutine
		// (root.go's pushDiagnostics), and bubbletea gives no guarantee
		// that an earlier push's goroutine finishes before a later one
		// does. Two pushes close together — a TaskDone envelope
		// immediately followed by the ListTasks reply it triggered — can
		// therefore arrive out of order. Without this guard, an OLDER
		// snapshot landing AFTER a newer one silently overwrites it, and
		// nothing corrects it until some later event pushes again — this
		// is what a field report described as the Task card "locking onto
		// the first snapshot's identity and never letting go" until the
		// overlay was closed and reopened. Seq lets this handler trust
		// build order over arrival order.
		if m.D.Seq > w.diagnosticsSeq {
			w.diagnosticsSeq = m.D.Seq
			w.tasks = m.D.Tasks
			w.backendDown = m.D.ConnState == protocol.StateFailed
			w.connState = m.D.ConnState
		}
	case protocol.EnvelopeMsg:
		return w, w.handleEnvelope(m.Envelope)

	case protocol.BackendLostMsg:
		// F5's "one path, two causes": a backend that died under a running
		// task leaves exactly what a cancel leaves — partial text, marked
		// interrupted, with Retry. Nothing here is a separate crash-
		// recovery renderer, which is precisely what stops it rotting
		// against the cancellation path that gets exercised daily.
		for _, e := range w.live {
			w.finish(e, statusCancelled)
		}
	case app.PasteMsg:
		if w.input.focused {
			return w, w.handleInputPaste(m.Text)
		}
		return w, w.handlePastedBurst(m.Text)
	case app.EnterBrowseMsg:
		return w, w.enterBrowseMode()
	case app.FocusInputMsg:
		return w, w.enterInputMode()
	case draftSaveMsg:
		w.saveDraftIfDue(m.gen)
	case app.CancelStreamMsg, app.DiscardPendingMsg, app.EditPendingMsg,
		app.RetryMsg, streamTickMsg:
		// F5's own messages — cancel, the queue's edit/discard, retry, and
		// the render/watchdog tick. See stream.go's handleStreamMsg.
		cmd, _ := w.handleStreamMsg(msg)
		return w, cmd
	case OpenAttachmentPickerMsg:
		// F8's seam — see input_keys.go's doc comment on this type.
	case tea.KeyMsg:
		if w.input.focused {
			return w, w.handleInputKey(m)
		}
		return w, w.handleKey(m)
	}
	return w, nil
}
