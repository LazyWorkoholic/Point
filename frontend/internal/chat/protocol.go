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
	// queryDetachAttachment is F8's: the chip is already removed locally
	// (attachments.go's detachChip) by the time this reply arrives, so
	// there is nothing left to apply — only whether a refusal is worth
	// more than a log line, which decorative() below answers "no" to.
	queryDetachAttachment
	// queryListAttachments is F8's chip-freshness refresh (attachments.go's
	// refreshAttachmentFreshness/onAttachmentList) — decorative like the
	// two above: a refused refresh costs a chip staying on its last known
	// state one cycle longer, never the transcript.
	queryListAttachments
	// querySetSessionProvider is F10's: Settings/the first-run chooser
	// (app\settings.go) changes r.activeProvider, but that only affects
	// sessions CREATED from then on (onSessionList's CreateSession
	// payload) — the session already open keeps whatever it was given at
	// ITS OWN creation, which cards.go's Status card reads verbatim
	// (w.sessionProvider/w.sessionModel). A verification round found the
	// Status card simply never moving no matter what was chosen in
	// Settings, which this call exists to fix: see providers.go's
	// applyProviderDefault.
	querySetSessionProvider
	// querySessionMessages is F18's: one batch of B18's keyset scrollback
	// query. Decorative in the sense decorative() means -- the resident
	// window already on screen is correct without it, so a refused or
	// unreadable batch costs one more keystroke at the same edge and never
	// the transcript. Unlike every other kind here it is correlated by id
	// in history.go as well, because a superseded fetch must be dropped
	// rather than applied to a window that has since moved.
	querySessionMessages
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

// newQueryEnvelope marshals a payload into an envelope with a caller-chosen
// id. Extracted from doSend for history.go, which has to KNOW the id it sent
// (doSend mints one privately and returns nothing) so a reply can be matched
// against the window state it was issued against.
func newQueryEnvelope(id, cmdType string, payload map[string]any) (protocol.Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.Envelope{ID: id, Type: cmdType, Payload: raw}, nil
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
	id := localID()
	env, err := newQueryEnvelope(id, cmdType, payload)
	if err != nil {
		w.loadErr = err.Error()
		return
	}
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
	return k == queryListTasks || k == queryGetContextReport ||
		k == queryDetachAttachment || k == queryListAttachments ||
		k == querySessionMessages
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
	// F8: a staleness sweep's own ResolveAttachment reply — see
	// attachments.go's own doc for why it is matched on a private map
	// instead of the pending/queryKind one below.
	if w.handleResolveAttachmentEnvelope(env) {
		return nil
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
		if kind == querySetSessionProvider {
			// NOT w.loadErr: the transcript already on screen is correct
			// either way, and CreateSession's own payload (onSessionList)
			// still carries the chosen default for the NEXT session
			// regardless of whether this one's retrofit succeeded. Costs
			// only the Status card staying on whatever it showed before.
			w.log.Warn("chat: SetSessionProvider refused, Status card not retrofitted", "err", p.Error)
			return nil
		}
		if kind == queryDetachAttachment {
			// NOT w.loadErr: detachChip already removed the chip locally
			// (optimistic, matching backspace's own pre-F8 behaviour) —
			// AttachmentNotFoundError here means the row was already gone
			// server-side, which is the outcome the user wanted anyway, not
			// a reason to blank the transcript.
			w.log.Warn("chat: DetachContext refused", "err", p.Error)
			return nil
		}
		if kind == queryListAttachments {
			// NOT w.loadErr: a refused freshness refresh costs a chip
			// staying on its last known state, never the transcript.
			w.log.Warn("chat: ListAttachments refused, chip freshness not refreshed", "err", p.Error)
			return nil
		}
		if kind == querySessionMessages {
			// NOT w.loadErr, and the in-flight slot has to be cleared or
			// every later edge check would find a fetch permanently
			// outstanding and never ask again. The resident window stays
			// exactly as it is; the next scroll re-asks.
			w.log.Warn("chat: SessionMessages refused, resident window unchanged", "err", p.Error)
			w.window.inFlight = histNone
			w.window.fetchID = ""
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
	case queryListAttachments:
		w.onAttachmentList(env)
	case querySetSessionProvider:
		w.onSessionProviderUpdated(env)
	case querySessionMessages:
		w.onSessionMessages(env)
	}
	return nil
}

// onSessionProviderUpdated applies SessionProviderUpdated to the ONE place
// the Status card actually reads (cards.go's statusCardLines) — see
// providers.go's applyProviderDefault for why this request exists at all.
// Decoded straight off the reply rather than trusted from what was sent:
// the backend's own echo is the only claim this package treats as true
// about what a session's row now holds.
func (w *Workspace) onSessionProviderUpdated(env protocol.Envelope) {
	var p struct {
		SessionID string `json:"session_id"`
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		ProjectID string `json:"project_id"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return
	}
	if p.SessionID != w.sessionID {
		// The active session changed (or this reply is for a session this
		// workspace has since moved away from) while the request was in
		// flight — applying it now would relabel the WRONG session's card.
		return
	}
	w.sessionProvider = p.Provider
	w.sessionModel = p.Model
	w.projectID = p.ProjectID
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
		w.getSession(session.ID)
		return
	}
	// Nothing usable, including the empty-list case: start fresh.
	//
	// w.state.Provider is F10's own stored default (config\state.go) — nil
	// until the user has picked one through Point's own UI (the first-run
	// chooser or Settings' Providers tab, both app\settings.go), in which
	// case the payload is exactly empty, same as before this episode, and
	// the backend's own resolution chain (session > agent default > global
	// config.toml) decides, unchanged. Once a choice exists it is passed
	// explicitly so a fresh session is resolvable even on a profile whose
	// config.toml never set provider.default/model.default at all — see
	// state.go's own doc on why this field exists instead of writing that
	// file (Point must never touch it).
	payload := map[string]any{}
	if d := w.state.Provider; d != nil {
		payload["provider"] = d.Provider
		payload["model"] = d.Model
		if d.Effort != "" {
			payload["effort"] = d.Effort
		}
		// F10c: Demo mode's sample-project binding. Only ever non-empty for
		// Demo (nothing else writes it) — this is the ONE real path that
		// binds a session's project_id, since CreateSession is reachable
		// only from this empty-session-list branch and there is no later
		// event that rebinds an already-created session's project.
		if d.ProjectID != "" {
			payload["project_id"] = d.ProjectID
		}
	}
	w.send(protocol.CmdCreateSession, queryCreateSession, payload)
}

// getSession opens a session, BOUNDED -- F18's "do not fetch the whole
// session at open".
//
// `message_limit` is B18's additive parameter: omitted, GetSession is
// byte-for-byte its old unbounded self (which is how F3 kept working while
// F18 was unbuilt); given, it returns the newest N messages plus
// total_message_count. This is the only caller, and it always passes it --
// the unbounded path now has no reader anywhere in the frontend, which is the
// point of the episode.
//
// The limit is the resident bound rather than one batch: the window has to
// arrive full, or the first render sits at the top edge of a one-batch window
// and immediately fires a prepend the user never asked for.
func (w *Workspace) getSession(id string) {
	w.send(protocol.CmdGetSession, queryGetSession, map[string]any{
		"session_id":    id,
		"message_limit": w.historyBound(),
	})
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
	w.projectID = s.ProjectID
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
	//
	// F18: and both now run over the RESIDENT WINDOW rather than the whole
	// session. seedWindow installs the bounded tail GetSession returned as
	// w.raw and derives w.all/w.hist from it; every later prepend, append
	// or eviction re-derives them the same way. See history.go.
	w.seedWindow(s)
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
	// F18, and deliberately last: a stored scroll anchor needs the session
	// id, the resident window and the transcript all to exist, and it is
	// allowed to issue a fetch of its own. It degrades to the bottom
	// everywhere it cannot land, so nothing after this point can be left
	// in a worse state than a plain load.
	w.applyResumeAnchor()
}

// Update is the Workspace interface's entry point. Root's Update forwards
// protocol.ConnectedMsg (so bootstrap can wait for a live connection
// instead of failing against a nil backend), protocol.EnvelopeMsg (query
// replies), app.PasteMsg, app.EnterBrowseMsg/app.FocusInputMsg (F4's focus
// arbitration — see app\focus.go), app.AgentPickedMsg/app.AttachmentPickedMsg
// (F8's @ picker writing back into this input — see app\picker.go and
// input_keys.go's openPickerCmd), and every tea.KeyMsg the action registry
// did not consume, via Root.dispatchKey's fallthrough. draftSaveMsg is this
// package's own message, arriving the same way: Root has no case for it, so
// its Update falls through to r.current().Update, which is here.
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
			w.noProviderConfigured = m.D.NoProviderConfigured
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
	case chipTickMsg:
		return w, w.onChipTick(m.gen)
	case app.CancelStreamMsg, app.DiscardPendingMsg, app.EditPendingMsg,
		app.RetryMsg, streamTickMsg:
		// F5's own messages — cancel, the queue's edit/discard, retry, and
		// the render/watchdog tick. See stream.go's handleStreamMsg.
		cmd, _ := w.handleStreamMsg(msg)
		return w, cmd
	case app.AgentPickedMsg:
		return w, w.applyAgentPicked(m.ID)
	case app.AttachmentPickedMsg:
		return w, w.applyAttachmentPicked(m.Chip)
	case app.ProviderDefaultSetMsg:
		// F10: Settings/the first-run chooser chose this directly on Root
		// (app\settings.go) — this workspace is only told so it can persist
		// it (the single state.json writer, same rule Drafts already
		// follows) and so CreateSession/providerMissing read it from here on.
		w.applyProviderDefault(m.Default)
	case app.UIOverrideSetMsg:
		w.applyUIOverride(m.Override)
	case app.BehaviourOverridesSetMsg:
		w.applyBehaviourOverrides(m.Overrides)
	case tea.KeyMsg:
		if w.input.focused {
			return w, w.handleInputKey(m)
		}
		return w, w.handleKey(m)
	}
	return w, nil
}
