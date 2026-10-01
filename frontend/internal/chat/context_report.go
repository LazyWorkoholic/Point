package chat

import (
	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// requestContextReport is S13d's once-per-load pull. A reload has no
// per-message history to seed a composition snapshot from the way w.usage
// seeds off the newest message's TokenUsage (setSession) -- the numbers
// aren't stored per-message -- so an explicit GetContextReport is the only
// way the Status card shows anything real before the next turn's push.
func (w *Workspace) requestContextReport() {
	if w.client == nil || w.sessionID == "" {
		return
	}
	w.send(protocol.CmdGetContextReport, queryGetContextReport, map[string]any{
		"session_id": w.sessionID,
	})
}

// RefreshContextReport satisfies app.ContextRefresher. Root decides when
// (UI-09 opening, or a task lifecycle event while it is open); this only
// sends the same pull setSession already uses.
func (w *Workspace) RefreshContextReport() { w.requestContextReport() }

var _ app.ContextRefresher = (*Workspace)(nil)

// onContextReport decodes a ContextReport -- the pull's reply (corr_id set)
// or the push after a turn completes (corr_id null) -- through the SAME
// function either way, exactly like every other query/push pair this
// package already routes through one decoder.
//
// Filtered on env.SessionID == w.sessionID: nothing in this codebase
// previously needed to filter an envelope by session, because chat.Workspace
// only ever has one session open and everything it read so far was scoped by
// corr_id/task_id or was root-owned. A ContextReport push is the first
// per-session broadcast this workspace receives with no correlating id at
// all -- once session-switching exists, a background task's push for a
// session the user has since navigated away from would otherwise overwrite
// the open session's numbers with a stranger's.
func (w *Workspace) onContextReport(env protocol.Envelope) {
	var p wireContextReport
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("chat: ContextReport payload did not decode", "err", err)
		return
	}
	if p.SessionID != w.sessionID {
		return
	}
	w.contextReport = &p
	// F8: the same two moments a report ever arrives (this pull, and the
	// push after a completed turn) are the only points the backend's own
	// recorded (mtime, size) baseline could have moved — see
	// attachments.go's refreshAttachmentFreshness doc for why this reads
	// the attachment rows directly rather than the report's own strings.
	w.refreshAttachmentFreshness()
}

// ContextExpandOverlay satisfies app.ContextExpandable -- S13d's one guard,
// shared by the leader action (context.expand), the Status-card click
// handler, and rightPaneOverlay's own enter/click handling. false means
// there is nothing real to show yet (no report pulled or pushed) or the
// backend is down; the caller must not open UI-09 with no data behind it.
// newContextOverlay is therefore only ever constructed from inside the true
// branch here -- it never needs a nil-report guard of its own.
func (w *Workspace) ContextExpandOverlay() (app.Overlay, bool) {
	if w.backendDown || w.contextReport == nil {
		return nil, false
	}
	return newContextOverlay(w), true
}

// OverlayForClick satisfies app.MouseOverlayTarget. x, y are root-space
// screen coordinates, the same frame render.go's rightPane() records
// w.statusCardRect in, so no translation is needed here -- unlike an
// overlay's own Update, which root translates to content-local coordinates
// before delivery (root.go's dispatchMouse).
func (w *Workspace) OverlayForClick(x, y int) (app.Overlay, bool) {
	if w.statusCardRect.IsZero() {
		return nil, false
	}
	r := w.statusCardRect
	if x < r.X || x >= r.X+r.W || y < r.Y || y >= r.Y+r.H {
		return nil, false
	}
	return w.ContextExpandOverlay()
}
