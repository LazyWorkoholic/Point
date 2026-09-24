package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

func contextReportPayload(t *testing.T, sessionID string) []byte {
	t.Helper()
	raw, err := json.Marshal(wireContextReport{
		SessionID:    sessionID,
		Provider:     "anthropic",
		Model:        "claude",
		BudgetTokens: 8000,
		Sections: map[string]int{
			"system": 400, "attachments": 0, "memory": 0,
			"summary": 0, "history": 3000, "task_state": 0,
		},
		Counts:     map[string]int{"history": 6},
		UsedTokens: 3400,
		FreeTokens: 4600,
		NextEvictions: []wireContextItem{
			{Section: "history", Ref: "msg-1", Kind: "message", Reason: "oldest"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The push (corr_id null, session_id set) is not tracked in w.pending and
// carries no task_id either, so it must be caught by its own branch in
// handleEnvelope BEFORE the pending-map lookup — this is the exact gap
// found while writing S13d's task doc: without that branch this event was
// silently dropped as "not one of ours".
func TestContextReportPushIsAppliedForTheOpenSession(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "sess-1"

	w.handleEnvelope(protocol.Envelope{
		Type: protocol.EvContextReport, SessionID: "sess-1",
		Payload: contextReportPayload(t, "sess-1"),
	})

	if w.contextReport == nil {
		t.Fatal("contextReport is nil after a push for the open session")
	}
	if w.contextReport.UsedTokens != 3400 {
		t.Errorf("UsedTokens = %d, want 3400", w.contextReport.UsedTokens)
	}
}

// A push for a session other than the one currently open must not alter the
// open session's card — the first per-session-scoped broadcast this
// workspace ever receives with no correlating id at all to gate on
// otherwise.
func TestContextReportPushIgnoresOtherSessions(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "sess-open"

	w.handleEnvelope(protocol.Envelope{
		Type: protocol.EvContextReport, SessionID: "sess-other",
		Payload: contextReportPayload(t, "sess-other"),
	})

	if w.contextReport != nil {
		t.Errorf("contextReport = %+v, want nil — the push named a different session", w.contextReport)
	}
}

// The pull's reply (corr_id set, routed through the ordinary pending-map
// switch) must land through the SAME onContextReport as the push — the one
// decoder this session's own doc insists on, not two.
func TestContextReportPullRoute(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "sess-1"
	withPending(w, "req-1", queryGetContextReport)

	w.handleEnvelope(protocol.Envelope{
		Type: protocol.EvContextReport, CorrID: "req-1", SessionID: "sess-1",
		Payload: contextReportPayload(t, "sess-1"),
	})

	if w.contextReport == nil {
		t.Fatal("contextReport is nil after the pull's reply")
	}
	if _, stillPending := w.pending["req-1"]; stillPending {
		t.Error("req-1 is still pending after its reply arrived")
	}
}

// RefreshContextReport is what Root calls while UI-09 is open; it must send
// the same GetContextReport pull setSession uses, for the open session.
func TestRefreshContextReportSendsAPull(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.sessionID = "sess-1"

	w.RefreshContextReport()

	sent := false
	for _, kind := range w.pending {
		if kind == queryGetContextReport {
			sent = true
		}
	}
	if !sent {
		t.Error("RefreshContextReport did not send a GetContextReport pull")
	}
}

// A refused pull must not touch loadErr — it costs the Status card's row 1
// only, same treatment queryListTasks already gets for the same reason.
func TestContextReportPullRefusalIsSoft(t *testing.T) {
	w := newTestWorkspace(t)
	withPending(w, "req-1", queryGetContextReport)

	w.handleEnvelope(protocol.Envelope{
		Type: protocol.EvError, CorrID: "req-1",
		Payload: json.RawMessage(`{"error":"no such session"}`),
	})

	if w.loadErr != "" {
		t.Errorf("loadErr = %q, want unset — a refused GetContextReport must not blank the transcript", w.loadErr)
	}
}

// ContextExpandOverlay is the one guard every entry point (the leader
// action, a Status-card click, rightPaneOverlay's own enter/click) shares —
// it must refuse before a report exists or while the backend is down, and
// only ever hand back a real overlay once both are clear.
func TestContextExpandOverlayGuard(t *testing.T) {
	w := &Workspace{theme: testTheme()}

	if _, ok := w.ContextExpandOverlay(); ok {
		t.Error("guard passed with no report at all")
	}

	w.contextReport = &wireContextReport{BudgetTokens: 8000, UsedTokens: 100}
	w.backendDown = true
	if _, ok := w.ContextExpandOverlay(); ok {
		t.Error("guard passed while the backend is down")
	}

	w.backendDown = false
	o, ok := w.ContextExpandOverlay()
	if !ok || o == nil {
		t.Fatal("guard refused with a real report and the backend up")
	}
	if _, isContextOverlay := o.(*contextOverlay); !isContextOverlay {
		t.Errorf("ContextExpandOverlay returned %T, want *contextOverlay", o)
	}
}

// The narrow-terminal fallback overlay recognizes enter and swaps itself for
// UI-09 — but only through the SAME guard, never unconditionally.
func TestRightPaneOverlayEnterSwapsToContextOverlay(t *testing.T) {
	w := &Workspace{theme: testTheme()}
	rp := &rightPaneOverlay{w: w}

	// No report yet: enter must be a no-op, not a swap to a card with
	// nothing to show.
	next, _ := rp.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next != rp {
		t.Errorf("enter swapped with no report present: got %T, want the same *rightPaneOverlay unchanged", next)
	}

	w.contextReport = &wireContextReport{BudgetTokens: 8000, UsedTokens: 100}
	next, _ = rp.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := next.(*contextOverlay); !ok {
		t.Fatalf("enter with a real report returned %T, want *contextOverlay", next)
	}

	// esc still closes, unaffected by the new branch.
	next, _ = rp.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next != nil {
		t.Error("esc did not close rightPaneOverlay")
	}
}

// A click inside the Status section (row 0 through the label+lines it drew
// on the last View call) triggers the same swap; a click below it does not.
func TestRightPaneOverlayClickOnStatusSectionSwaps(t *testing.T) {
	w := &Workspace{theme: testTheme(), contextReport: &wireContextReport{BudgetTokens: 8000, UsedTokens: 100}}
	rp := &rightPaneOverlay{w: w}
	rp.View(testTheme(), 40, 24) // populates rp.statusRows for the hit-test below

	below := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: rp.statusRows}
	if next, _ := rp.Update(below); next != rp {
		t.Errorf("a click BELOW the Status section swapped anyway: got %T", next)
	}

	inside := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 0}
	next, _ := rp.Update(inside)
	if _, ok := next.(*contextOverlay); !ok {
		t.Fatalf("a click INSIDE the Status section returned %T, want *contextOverlay", next)
	}
}

// The Status card's row 1 stays the marked stub with no report, and becomes
// a real bar once one arrives — the two states F6's own card idiom already
// uses for "no turns yet" vs a real number.
func TestStatusCardContextRow(t *testing.T) {
	w := &Workspace{theme: testTheme()}
	lines := w.statusCardLines(28, 3)
	if len(lines) == 0 || !strings.Contains(lines[0], "not wired yet") {
		t.Errorf("row 1 with no report = %q, want the marked stub", lines[0])
	}

	w.contextReport = &wireContextReport{BudgetTokens: 32000, UsedTokens: 12400}
	lines = w.statusCardLines(28, 3)
	if len(lines) == 0 || strings.Contains(lines[0], "not wired yet") {
		t.Fatalf("row 1 with a real report still shows the stub: %q", lines[0])
	}
	if !strings.Contains(lines[0], "12.4k") || !strings.Contains(lines[0], "32.0k") {
		t.Errorf("row 1 = %q, want the used/budget numbers", lines[0])
	}
}

// OverlayForClick is root's own entry point (dispatchMouse) into this
// guard — a click inside the last-rendered Status card rect opens the
// overlay, a click anywhere else, or before that rect has ever been set,
// does not.
func TestOverlayForClick(t *testing.T) {
	w := &Workspace{theme: testTheme(), contextReport: &wireContextReport{BudgetTokens: 8000, UsedTokens: 100}}

	if _, ok := w.OverlayForClick(5, 5); ok {
		t.Error("a click hit before statusCardRect was ever set")
	}

	w.statusCardRect = layout.Rect{X: 10, Y: 2, W: 20, H: 5}

	if _, ok := w.OverlayForClick(0, 0); ok {
		t.Error("a click outside statusCardRect hit anyway")
	}
	o, ok := w.OverlayForClick(12, 3)
	if !ok || o == nil {
		t.Fatal("a click inside statusCardRect did not hit")
	}
}
