package editor

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F15 — invoking the Filler-Explainer from the Editor.
//
// Two paths, one agent, one wire shape; the invocation site decides the
// presentation (F15's own words):
//
//	leader → e   explain   the reply streams into a panel inside the Editor
//	leader → f   fill      the reply streams as GHOST TEXT at the caret,
//	                       then becomes a DiffCard the user applies or not
//
// Nothing here writes to disk, in any phase, and nothing here parses a
// model's JSON into a proposal -- B24 does that in the backend and pushes
// the result as its own validated event. See `onFillProposal`.
//
// # Where the session comes from, and why the Editor has its own
//
// `SendMessage` requires one ("SendMessage requires 'session'"), and
// `protocol.go`'s header is blunt that this workspace has none: "The Editor
// has no session and therefore no project_id." So it creates one, lazily, on
// the first invocation, and reuses it for every later one.
//
// That is not a workaround for a missing seam -- it is what F15's own
// `send to chat` action requires. "`send to chat` moves it into the current
// session" only means anything if the exchange is NOT already in the chat
// session. A shared session would make the action a no-op by construction.
//
// The session is bound to the Editor's OWN resolved project (`w.projectID`),
// which is the scope the files in this workspace were opened under, and is
// what makes `CheckFileStale(session_id=...)` answer about the right project
// rather than whatever `.point\` happens to sit above a path (B36's
// `_attachment_scope`, and B12's bug #2 before it). Demo mode's own
// `ProviderDefault.ProjectID` is deliberately NOT used for this: it names
// the sample project, and the Editor must be scoped to the project it is
// actually editing. In the Demo gate the two are the same project anyway,
// because Point is launched from inside it.
//
// # One invocation at a time
//
// A deliberate limit, not an oversight. The fill UI is a prompt AT THE
// CARET and a ghost AT THE CARET; two of them at once have nowhere to be
// drawn and no way for the user to tell which reply is which. A second
// invoke while one is live says so and does nothing.

// invokeKind is which of the two paths an invocation is on.
type invokeKind int

const (
	invokeExplain invokeKind = iota
	invokeFill
)

func (k invokeKind) String() string {
	if k == invokeFill {
		return "fill"
	}
	return "explain"
}

func (k invokeKind) mode() string {
	if k == invokeFill {
		return modeFill
	}
	return modeExplain
}

// fillerAgentID is the agent both paths invoke, transcribed from
// backend\agents\builtins.py's FILLER_EXPLAINER_AGENT_ID.
//
// Sent as `payload["agent"]`, which is B11's per-message override and is
// never written back to the session's own settings -- so the Editor's
// session stays bound to no agent and a later episode can invoke a
// different one through the same session without un-picking anything.
const fillerAgentID = "filler-explainer"

// proposalSettleDelay is how long after TaskDone a fill waits for its
// FillProposal before concluding there is not going to be one.
//
// A timer is needed because the absence of a proposal is not an event.
// `_push_fill_proposal` emits NOTHING when the reply does not parse -- a
// plain-text refusal (F15's Done-when 6) included -- and the proposal that
// DOES arrive comes after the terminal event, never before it. So on
// TaskDone a fill cannot yet tell the two apart, and nothing further will
// arrive to tell it.
//
// This is the same structural gap B24 hit from the other side: `cli.py
// send --json-proposal` added `_drain_for_fill_proposal()`, "a ~2s bounded
// peek... because call()'s until() already stops draining at TaskDone, one
// envelope before FillProposal arrives". The backend emits both from one
// synchronous call stack with no intervening await, so the real gap is a
// scheduling hop; one second is generous against that and well under B24's
// own 2s.
//
// Generation-guarded (`settleGen`) exactly like autosave's per-document
// timer: a tick whose generation no longer matches is a superseded timer and
// does nothing.
const proposalSettleDelay = time.Second

// proposalSettleMsg fires proposalSettleDelay after a fill's TaskDone.
type proposalSettleMsg struct{ gen int }

// invocation is one live explain-or-fill request.
//
// Held on the workspace rather than in the panel or the prompt, because it
// outlives both: the prompt closes on Enter, the panel can be dismissed
// mid-stream, and the task keeps running either way.
type invocation struct {
	kind invokeKind

	// requestID is the id WE minted for the SendMessage. Every event for
	// this turn carries it as corr_id, which is the only correlation
	// available before TaskStarted has named a task.
	requestID string
	taskID    string
	// sessionID is read off the arriving events rather than copied from
	// w.sessionID. It is what CheckFileStale is later asked under, and the
	// honest source for "which session produced this proposal" is the
	// envelope that delivered it.
	sessionID string

	// path and name are the document this invocation is about. Resolved
	// back to a live document at every use -- the tab can be closed while
	// a task runs.
	path string
	name string

	// question is what the user typed (fill) or the scope label (explain),
	// kept for the panel's header and for `send to chat`.
	question string

	// anchorLine is the 0-based buffer line the ghost is drawn after, and
	// the caret line at invocation time. Frozen at invocation: the ghost
	// has to stay where the question was asked even if the caret moves.
	anchorLine int

	// scope is the enclosing block by kind and name ("function add(a, b)",
	// "class Foo", or "selection"), for the explain panel's header. Taken
	// at invocation time from the same lines and the same range that were
	// sent, so the label cannot describe a different region from the one
	// the model was shown.
	scope string

	// shownStart and shownEnd are the 1-based region this workspace told
	// the backend to use (EditorContext.Enclosing). Kept for the prompt's
	// scope label and the panel's header, never used to validate a
	// proposal -- B24 already validated the range against the region the
	// model was actually shown, and re-checking it here against a
	// different moment is exactly what F9b's header forbids.
	shownStart int
	shownEnd   int

	// text is the accumulated reply, exactly as it arrived.
	text string

	// done, failed and cancelled are the terminal outcome. `failed` holds
	// the message when there is one.
	done      bool
	failed    string
	cancelled bool

	// settled records that the fill has already decided what its reply
	// was: a proposal arrived, or the settle timer concluded one never
	// will. Guards against doing it twice.
	settled bool
}

// liveInvocation reports whether this workspace is mid-invocation.
//
// # It spans the gap between a fill's terminal event and its proposal
//
// It used to be "the task has not reached a terminal event", and that was
// wrong by exactly one envelope. B24's contract is that `FillProposal` is
// pushed AFTER `TaskDone`, never instead of it -- so a definition that goes
// false on `TaskDone` goes false one envelope before the proposal needs it.
// The consequence, found by driving a real Root: with the Editor
// backgrounded, `WantsBackgroundReplies` turned off at `TaskDone` and
// `deliverBackgroundReply` therefore skipped the Editor for the proposal
// itself. No card was built, the ghost was left frozen at whatever the last
// token had made it, and `ctrl+z` did nothing because nothing was ever
// spliced.
//
// So a FILL stays live until it is SETTLED -- a proposal arrived, or the
// settle timer concluded one never will. An EXPLAIN has no such gap (there
// is no second event after its terminal one) and goes false on `done`
// exactly as before.
//
// # Every other caller was checked against the wider meaning
//
// Seven call sites, and six of them want it:
//
//   - WantsBackgroundReplies (autosave.go) -- the one that forced this.
//   - capturesModalKey (editor.go) -- during the gap the ghost is still on
//     screen, so `esc` must still reach this workspace to discard it.
//     Narrower was a latent bug of the same family.
//   - the esc/ctrl+c branch in handleKey -- same, and it is Done-when 3c.
//   - the "already asking" guard in BeginExplain and BeginFill -- REQUIRED
//     wider: a second invocation started inside the gap would overwrite
//     w.inv, and the arriving proposal (matched against w.inv) would be
//     dropped on the floor.
//   - the status-bar segment (status.go) -- "filling app.py… esc cancels"
//     is accurate during the gap, since esc does still discard the ghost.
//
// The seventh genuinely wants the narrow question and now asks it by its
// own name: `taskRunning` below. That is explainpanel.go's `ctrl+c`, whose
// own comment is explicit -- "Once it has finished there is nothing to
// cancel and it closes the panel, rather than sending a CancelTask for a
// task that is already terminal." Widening it in place would have made
// that branch send a CancelTask for a task the backend had already
// finished. Two questions, two names, rather than one name stretched over
// both.
func (w *Workspace) liveInvocation() bool {
	inv := w.inv
	switch {
	case inv == nil, inv.cancelled, inv.failed_():
		return false
	case !inv.done:
		return true
	default:
		return inv.kind == invokeFill && !inv.settled
	}
}

// taskRunning reports whether there is a BACKEND task that has not reached
// a terminal event -- the only question a CancelTask should be sent on.
//
// This is liveInvocation's old definition, kept under the name that says
// what it is for. See liveInvocation's own doc for why the two are separate.
func (w *Workspace) taskRunning() bool {
	inv := w.inv
	return inv != nil && !inv.done && !inv.cancelled && !inv.failed_()
}

func (i *invocation) failed_() bool { return i.failed != "" }

// ---------------------------------------------------------------------------
// Starting one
// ---------------------------------------------------------------------------

// BeginExplain is app.EditorAgent -- leader → e.
//
// Explain sends immediately. There is nothing to type: "This is why the
// Explainer needs no typing -- the common path is select-then-invoke"
// (F15's Build). The panel opens at once so the stream has somewhere to
// land and the user can see it arriving.
func (w *Workspace) BeginExplain() tea.Cmd {
	d := w.activeDoc()
	switch {
	case d == nil:
		return w.say("open a file first — explain works on the code under the caret")
	case w.liveInvocation():
		return w.say("already asking the " + w.inv.kind.String() + " question — esc cancels it")
	}
	return w.startInvocation(invokeExplain, "explain this")
}

// BeginFill is app.EditorAgent -- leader → f.
//
// Fill opens the inline prompt and sends NOTHING. "Nothing is sent until
// Enter" (F15's Build).
//
// A read-only document is refused HERE rather than at apply time, and that
// is a choice: the diff card's own guard would refuse the apply anyway
// (F9b's step 3), but generating a proposal that can never be applied
// spends a model call to arrive at a refusal that was knowable before the
// question was asked.
func (w *Workspace) BeginFill() tea.Cmd {
	d := w.activeDoc()
	switch {
	case d == nil:
		return w.say("open a file first — fill writes at the caret")
	case d.readOnly:
		return w.say(d.name() + " is read-only — " + string(d.roReason) +
			", so a filled region could not be applied")
	case w.liveInvocation():
		return w.say("already asking the " + w.inv.kind.String() + " question — esc cancels it")
	case w.prompt != nil:
		return nil
	}
	w.prompt = newFillPrompt(d, w.theme)
	w.focus = focusBuffer
	return nil
}

// submitPrompt is Enter in the inline prompt.
func (w *Workspace) submitPrompt() tea.Cmd {
	if w.prompt == nil {
		return nil
	}
	text := strings.TrimSpace(w.prompt.text())
	w.prompt = nil
	if text == "" {
		return w.say("nothing to fill — the instruction was empty")
	}
	return w.startInvocation(invokeFill, text)
}

// startInvocation is the one path both kinds take to the wire.
func (w *Workspace) startInvocation(kind invokeKind, question string) tea.Cmd {
	d := w.activeDoc()
	if d == nil {
		return nil
	}
	if w.backendDown {
		return w.say("the backend is not running — nothing was sent")
	}

	ec := editorContextFor(d, kind.mode())
	lines := docLines(d)
	inv := &invocation{
		scope:      scopeName(lines, ec.Enclosing[0], ec.Enclosing[1]),
		kind:       kind,
		requestID:  localID(),
		path:       d.path,
		name:       d.name(),
		question:   question,
		anchorLine: d.cursor.Line,
		shownStart: ec.Enclosing[0],
		shownEnd:   ec.Enclosing[1],
	}
	w.inv = inv
	if kind == invokeFill {
		w.ghost = &ghostText{anchorLine: inv.anchorLine}
		w.ghostFollow = true
	} else {
		w.panel = newExplainPanel(inv)
	}

	// The session, which may not exist yet. The SendMessage is held until
	// its id lands -- not sent speculatively with an empty session, which
	// the backend refuses outright.
	if w.sessionID == "" {
		w.pendingSend = &pendingSend{inv: inv, ec: ec}
		return tea.Batch(w.ensureSession(), w.say("asking the Filler-Explainer…"))
	}
	w.writeInvocation(inv, ec)
	return w.say("asking the Filler-Explainer about " + inv.name + "…")
}

// pendingSend is an invocation waiting for the session that will carry it.
type pendingSend struct {
	inv *invocation
	ec  EditorContext
}

// ensureSession creates the Editor's own session, once.
func (w *Workspace) ensureSession() tea.Cmd {
	if w.sessionID != "" || w.creatingSession {
		return nil
	}
	w.creatingSession = true
	payload := map[string]any{
		// A title, so this row is identifiable in `cli.py session list`
		// and in the tray's own Session column rather than being another
		// "New Session".
		"title":     "Editor",
		"workspace": "editor",
	}
	if w.projectID != "" {
		payload["project_id"] = w.projectID
	}
	// The provider the user has already chosen, exactly as chat forwards
	// it on ITS CreateSession. Without this the session inherits
	// config.toml's own default, which in Demo mode is not the demo
	// provider -- so a Demo-mode fill would silently run against whatever
	// provider the config names.
	if w.provider != nil {
		payload["provider"] = w.provider.Provider
		payload["model"] = w.provider.Model
		if w.provider.Effort != "" {
			payload["effort"] = w.provider.Effort
		}
	}
	w.send(protocol.CmdCreateSession, queryCreateSession, payload)
	return nil
}

func (w *Workspace) onSessionCreated(env protocol.Envelope) tea.Cmd {
	w.creatingSession = false
	var p struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := env.UnmarshalPayload(&p); err != nil || p.Session.ID == "" {
		w.log.Warn("editor: decoding a SessionCreated reply", "err", err)
		return w.failPending("the backend did not return a session")
	}
	w.sessionID = p.Session.ID
	w.log.Info("editor: session created", "session", w.sessionID, "project", w.projectID)

	ps := w.pendingSend
	w.pendingSend = nil
	if ps == nil || ps.inv != w.inv {
		// The invocation was cancelled while the session was being
		// created. The session is still worth keeping -- the next
		// invocation uses it.
		return nil
	}
	w.writeInvocation(ps.inv, ps.ec)
	return nil
}

// failPending ends an invocation that never reached the wire.
//
// A live invocation that is never resolved keeps WantsBackgroundReplies
// true and the ghost on screen forever, which is the same unbounded
// never-terminal state chat's own writeSend guards against.
func (w *Workspace) failPending(reason string) tea.Cmd {
	ps := w.pendingSend
	w.pendingSend = nil
	if ps == nil {
		return nil
	}
	ps.inv.failed = reason
	if w.inv == ps.inv {
		w.ghost, w.ghostFollow = nil, false
		if w.panel != nil {
			w.panel.fail(reason)
		}
	}
	return w.say(reason)
}

// writeInvocation is the wire write.
//
// `client.Send`, not `Request`, and that is chat's own call for the same
// reason: "a send answers with a whole STREAM of events sharing one
// corr_id, and Request's waiter channel holds exactly one". The events
// reach this workspace through Root's ordinary EnvelopeMsg forwarding, and
// client.go routes anything carrying a task_id as RouteTask, so nothing
// here is logged as unroutable.
func (w *Workspace) writeInvocation(inv *invocation, ec EditorContext) {
	payload := map[string]any{
		"session":        w.sessionID,
		"text":           inv.question,
		"agent":          fillerAgentID,
		"editor_context": ec,
	}
	if err := w.writeFn(inv.requestID, protocol.CmdSendMessage, payload); err != nil {
		inv.failed = err.Error()
		w.ghost, w.ghostFollow = nil, false
		if w.panel != nil {
			w.panel.fail(inv.failed)
		}
		return
	}
	w.log.Debug("editor: invoked the filler-explainer",
		"mode", inv.kind.String(), "file", inv.name,
		"shown", []int{inv.shownStart, inv.shownEnd}, "corr_id", inv.requestID)
}

// ---------------------------------------------------------------------------
// Cancelling
// ---------------------------------------------------------------------------

// cancelInvocation is esc or ctrl+c while something is in flight.
//
// "Cancelling discards the ghost entirely and cancels the task" -- and the
// buffer is untouched and NOT marked dirty, which is structural here rather
// than a thing this function has to undo: the ghost was never in the
// buffer. Nothing in this file calls Splice.
func (w *Workspace) cancelInvocation() tea.Cmd {
	inv := w.inv
	if inv == nil {
		return nil
	}
	running := w.taskRunning()
	inv.cancelled = true
	w.ghost, w.ghostFollow = nil, false
	w.pendingSend = nil
	if w.panel != nil {
		w.panel = nil
	}
	// Read BEFORE the cancelled flag is set below, and on taskRunning
	// rather than liveInvocation: a fill cancelled inside the gap between
	// its terminal event and its proposal has no running task to cancel,
	// and asking the backend to cancel a finished one is a round trip
	// whose only possible answer is "already finished".
	if inv.taskID != "" && running {
		// Fire-and-forget, like the tray's own cancel: the ack is not
		// terminal and TaskCancelled arrives later. `accepted: false`
		// means it had already finished, which from here is not worth a
		// message.
		_ = w.writeFn(localID(), protocol.CmdCancelTask,
			map[string]any{"task_id": inv.taskID})
	}
	return w.say("cancelled — the buffer was not touched")
}

// ---------------------------------------------------------------------------
// Receiving
// ---------------------------------------------------------------------------

// invocationEnvelope routes one envelope to the live invocation, reporting
// whether it belonged to it.
//
// Tried BEFORE protocol.go's `pending` lookup, exactly as chat tries its
// stream events first: a SendMessage registers no correlation waiter, so
// these envelopes are not in `pending` and a `!ours` early return would
// drop every one of them.
func (w *Workspace) invocationEnvelope(env protocol.Envelope) (tea.Cmd, bool) {
	inv := w.inv
	if inv == nil {
		return nil, false
	}
	switch {
	case env.CorrID != "" && env.CorrID == inv.requestID:
	case env.TaskID != "" && env.TaskID == inv.taskID:
	default:
		return nil, false
	}
	if env.TaskID != "" {
		inv.taskID = env.TaskID
	}
	if env.SessionID != "" {
		inv.sessionID = env.SessionID
	}

	switch env.Type {
	case protocol.EvTaskStarted:
		return nil, true

	case protocol.EvTextDelta, protocol.EvTaskToken:
		inv.text += deltaText(env)
		w.applyStreamText(inv)
		return nil, true

	case protocol.EvThinkingDelta:
		// Not shown. The ghost is the proposed REGION, and the panel is
		// the explanation; neither is the model's reasoning, and
		// interleaving it with either would misrepresent what is being
		// proposed.
		return nil, true

	case protocol.EvTaskDone:
		inv.done = true
		if inv.kind == invokeFill {
			w.settleGen++
			gen := w.settleGen
			return tea.Tick(w.settleWait(), func(time.Time) tea.Msg {
				return proposalSettleMsg{gen: gen}
			}), true
		}
		w.applyStreamText(inv)
		return nil, true

	case protocol.EvTaskFailed:
		var p struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = env.UnmarshalPayload(&p)
		inv.failed = p.Error
		if inv.failed == "" {
			inv.failed = "the task failed"
		}
		w.ghost, w.ghostFollow = nil, false
		if w.panel != nil {
			w.panel.fail(inv.failed)
		}
		return w.say(inv.name + ": " + inv.failed), true

	case protocol.EvTaskCancelled:
		inv.cancelled = true
		w.ghost, w.ghostFollow = nil, false
		return nil, true

	case protocol.EvFillProposal:
		return w.onFillProposal(inv, env), true

	case protocol.EvError:
		var p struct {
			Message string `json:"message"`
		}
		_ = env.UnmarshalPayload(&p)
		inv.failed = p.Message
		if inv.failed == "" {
			inv.failed = "the backend refused the request"
		}
		w.ghost, w.ghostFollow = nil, false
		if w.panel != nil {
			w.panel.fail(inv.failed)
		}
		return w.say(inv.failed), true
	}
	return nil, true
}

// applyStreamText pushes the accumulated reply into whichever surface this
// invocation's kind owns.
//
// Rebuilt from `inv.text` on every token rather than appended to the
// surface, which is what makes F15's Done-when 3b hold without a second
// mechanism: the ghost (or the panel) is a pure function of everything that
// has arrived, so coming back to the workspace after a switch shows
// everything that arrived while away rather than a snapshot taken when the
// user left. The accumulation itself survives the switch because the
// workspace stays loaded and keeps receiving (see WantsBackgroundReplies).
func (w *Workspace) applyStreamText(inv *invocation) {
	if inv != w.inv {
		return
	}
	if inv.kind == invokeFill {
		if w.ghost != nil {
			w.ghost.setFromReply(inv.text)
		}
		return
	}
	if w.panel != nil {
		w.panel.setText(inv.text, inv.done)
	}
}

// onProposalSettle is the settle timer: TaskDone arrived, and no
// FillProposal followed it.
//
// So the reply was not a proposal -- most often the refusal F15 asks for
// ("A request needing a second file, a new file, or a command is refused by
// the agent with a message. The UI must show that as a NORMAL response, not
// an error state"), and sometimes a reply that simply did not parse (B24
// measured the real local model at 0/5 and logs one WARNING per failure).
// Both are shown the same way, in the explain panel, as text: the one thing
// this must not do is render a refusal as an error, and the other is claim a
// proposal that does not exist.
func (w *Workspace) onProposalSettle(m proposalSettleMsg) tea.Cmd {
	if m.gen != w.settleGen {
		return nil
	}
	inv := w.inv
	if inv == nil || inv.kind != invokeFill || inv.settled {
		return nil
	}
	inv.settled = true
	w.ghost, w.ghostFollow = nil, false

	text := strings.TrimSpace(inv.text)
	if text == "" {
		return w.say("the Filler-Explainer returned nothing")
	}
	w.panel = newExplainPanel(inv)
	w.panel.setText(inv.text, true)
	return w.say("no change proposed — the agent replied instead")
}

// onFillProposal builds the diff card from B24's VALIDATED proposal.
//
// What this function does NOT do is the point of it:
//
//   - it does not parse the model's JSON. The payload is already parsed,
//     shape-checked, and validated against the region the model was shown
//     (B24: inverted ranges, ranges outside the shown region and file
//     mismatches never reach the wire);
//   - it does not compute a diff. `diff.Region` does, called by the card;
//   - it does not re-derive the range, the file, or the (mtime, size) pair
//     from anything local. All of those describe the generation moment and
//     only the event carries them.
func (w *Workspace) onFillProposal(inv *invocation, env protocol.Envelope) tea.Cmd {
	var p struct {
		Proposal struct {
			File        string  `json:"file"`
			StartLine   int     `json:"start_line"`
			EndLine     int     `json:"end_line"`
			Replacement string  `json:"replacement"`
			Note        string  `json:"note"`
			ShownStart  int     `json:"shown_start"`
			ShownEnd    int     `json:"shown_end"`
			Mtime       float64 `json:"mtime"`
			Size        int     `json:"size"`
		} `json:"proposal"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a FillProposal", "err", err)
		return nil
	}
	pr := p.Proposal
	inv.settled = true
	w.settleGen++ // any settle tick already in flight is now superseded
	w.ghost, w.ghostFollow = nil, false

	d := w.docByPath(inv.path)
	if d == nil {
		return w.say(inv.name + " was closed before the proposal arrived")
	}

	w.log.Info("editor: fill proposal",
		"file", pr.File, "lines", []int{pr.StartLine, pr.EndLine},
		"mtime", pr.Mtime, "size", pr.Size, "session", inv.sessionID)

	card := NewDiffCard(DiffCardOptions{
		Path:        d.path,
		Name:        d.name(),
		Note:        pr.Note,
		StartLine:   pr.StartLine,
		EndLine:     pr.EndLine,
		Replacement: pr.Replacement,
		Original:    docLines(d),
		BaseVersion: d.buf.Version(),

		// F9b shipped these two UNPOPULATED and wrote down why. This is
		// the episode that fills them, and the values come from the
		// event and from nowhere else -- never `d.mtime`/`d.size`, which
		// are the open/last-save moment and fail in both directions (a
		// false refusal when the region was generated from content newer
		// than the open, a MISSED refusal when a save refreshed the pair
		// after generation). B36 captured these from the same file
		// descriptor the region was read from.
		RecordedMtime:   pr.Mtime,
		RecordedSize:    pr.Size,
		HasRecordedPair: true,

		// The session that produced the proposal, so the staleness check
		// resolves THAT session's scope (B36's `session_id` branch,
		// `_attachment_scope`) rather than walking up from the path.
		SessionID: inv.sessionID,
	})
	w.card = card
	if !w.onScreen {
		// Backgrounded: held until this workspace is the one on screen
		// again. See onWorkspaceActivated.
		w.deferredCard = card
		w.log.Debug("editor: diff card held until the Editor is active again",
			"file", card.name)
		return nil
	}
	return func() tea.Msg { return app.OpenWorkspaceOverlayMsg{Overlay: card} }
}

// settleWait is proposalSettleDelay, substitutable.
//
// A field rather than the constant at the call site for the reason nowFn
// already exists on this workspace: the Root-dispatch tests have to run the
// commands Root hands back -- that is the whole point of them -- and
// running a one-second tea.Tick for real cost 58 seconds across three
// tests. A suite that sleeps is a suite people stop running.
func (w *Workspace) settleWait() time.Duration {
	if w.settleDelay > 0 {
		return w.settleDelay
	}
	return proposalSettleDelay
}

// AllowBackgroundMsg is app.BackgroundCmdFilter: THE allow-list for what
// this workspace's commands may do while it is loaded but not active.
//
// Default-deny. Three entries, and the reason each one is here:
//
//  1. app.OpenWorkspaceOverlayMsg -> DeliverToRoot. Opening an overlay is
//     Root's own business and Root handles this type itself. In practice
//     this workspace now DEFERS a finished fill's card to
//     WorkspaceActivatedMsg rather than opening it from under Chat (see
//     onWorkspaceActivated), so nothing on the fill path produces this
//     while backgrounded any more -- it is allow-listed so that any other
//     path which does is not silently swallowed, which is precisely the
//     failure this seam was built to end.
//
//  2. proposalSettleMsg -> DeliverToWorkspace. The timer that decides a
//     fill produced no proposal at all. It MUST fire while backgrounded:
//     it is the only thing that clears the ghost and shows the agent's
//     reply when no proposal is coming, so dropping it left a ghost frozen
//     on screen with nothing able to remove it. And it must come back HERE
//     rather than to whatever is on screen, which is what
//     DeliverToWorkspace means and what Root's ordinary fall-through
//     (r.current().Update) would have got wrong.
//
//  3. terminalEventMsg -> DeliverToWorkspace (F14). The same shape as (2)
//     but with a harsher failure, because this message is not a one-shot
//     timer -- it is the link in a LOOP. Each delivered terminal event is
//     what re-arms the read for the next one (terminal.go's next), so
//     dropping one while backgrounded does not lose one chunk of output,
//     it ends the pane's I/O permanently: no further output ever, no exit
//     event, and `running` stuck true for the rest of the session. It
//     routes back here rather than to Root because only this workspace
//     owns the buffer the bytes belong in. Nothing it produces takes the
//     status line -- onTerminalEvent writes no say() -- so the rule (2)
//     is scoped by still holds.
//
// EVERYTHING ELSE IS DROPPED, deliberately and including:
//
//   - app.WorkspaceStatusMsg (every say() in this package). It carries no
//     workspace id, so Root would print this workspace's line over the
//     status bar of the one the user is actually reading. That is
//     deliverBackgroundReply's original rule and it is unchanged.
//   - autosaveTickMsg and heartbeatTickMsg. Both are re-armed by the
//     ordinary paths on the way back in, and neither has a deadline that
//     matters to a workspace nobody is looking at.
//   - tea.BatchMsg, which cannot be judged element-wise without running
//     its parts. Nothing on the list above is produced inside a batch
//     today; see Root.backgroundCmd for where to look if that changes.
func (w *Workspace) AllowBackgroundMsg(msg tea.Msg) app.BackgroundDelivery {
	switch msg.(type) {
	case app.OpenWorkspaceOverlayMsg:
		return app.DeliverToRoot
	case proposalSettleMsg:
		return app.DeliverToWorkspace
	case terminalEventMsg:
		return app.DeliverToWorkspace
	}
	return app.DropWhileBackgrounded
}

// onWorkspaceActivated opens what finished while nobody was looking.
//
// A diff card built from a proposal that arrived while this workspace was
// backgrounded is held, not opened, and this is where it is opened instead.
// Popping it the moment the proposal lands would put a modal overlay over
// CHAT -- and an open overlay takes every key, so the user's next sentence
// would be typed into a diff card about a file in another workspace.
func (w *Workspace) onWorkspaceActivated() tea.Cmd {
	card := w.deferredCard
	w.deferredCard = nil
	if card == nil || card.Applied() {
		return nil
	}
	return func() tea.Msg { return app.OpenWorkspaceOverlayMsg{Overlay: card} }
}

// deltaText reads a token event's text.
//
// Both shapes, because both exist on the wire: the streaming adapter emits
// `{"text": ...}` and the task-token path nests it under `data`. chat's own
// deltaText does the same two-field decode; duplicated rather than shared
// for the reason `localID` is (chat and editor each have one): a six-line
// decode of a wire shape both packages read independently is not a
// dependency worth creating between two workspaces that must not import
// each other.
func deltaText(env protocol.Envelope) string {
	var p struct {
		Text string `json:"text"`
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return ""
	}
	if p.Data.Text != "" {
		return p.Data.Text
	}
	return p.Text
}

// ---------------------------------------------------------------------------
// Send to chat
// ---------------------------------------------------------------------------

// sendPanelToChat is the panel's `send to chat` action.
//
// It PREFILLS Chat's input with the quoted exchange; it does not append a
// message to the chat session, because there is no event that could. The
// wire has `SendMessage` (which runs a turn) and nothing that inserts a
// finished question-and-answer pair into a session's history -- so the
// honest version of this action is to hand the text to Chat's input and let
// the user decide whether to send it.
//
// Recorded as a deliberate narrowing in this episode's completed doc, with
// the backend addition it would need, rather than left looking like an
// oversight.
func (w *Workspace) sendPanelToChat() tea.Cmd {
	if w.panel == nil {
		return nil
	}
	text := w.panel.exchange()
	if strings.TrimSpace(text) == "" {
		return w.say("nothing to send yet")
	}
	w.panel = nil
	return func() tea.Msg {
		return app.SendToChatMsg{Text: text}
	}
}
