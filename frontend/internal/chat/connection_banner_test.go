package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// loadedWorkspace is a session that loaded while the backend was up. Its
// client is real but never started, so Client.Send answers exactly what a
// client whose backend just died answers: "backend is not connected".
func loadedWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := connectedTestWorkspace(t)
	w.sendMsgFn = w.writeSend
	w.bootstrapped = true
	w.sessionID = "sess-1"
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 1, ConnState: protocol.StateConnected}})
	return w
}

// loseBackend is Root's own order for a killed backend: BackendLostMsg is
// forwarded to the workspace (F5 ends live entries), then afterBackend pushes
// the new connection state.
func loseBackend(w *Workspace, seq int) {
	w.Update(protocol.BackendLostMsg{Reason: "the backend exited"})
	w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: seq, ConnState: protocol.StateLost}})
}

func viewport(w *Workspace) string {
	return stripANSI(strings.Join(w.viewportLines(layout.Rect{W: 70, H: 14}), "\n"))
}

// bannerShown matches the banner ROW, not any "not connected" text — a
// failed send's own footer can legitimately contain those words.
func bannerShown(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "! backend not connected") {
			return true
		}
	}
	return false
}

func lastReply(t *testing.T, w *Workspace) *liveEntry {
	t.Helper()
	for i := len(w.live) - 1; i >= 0; i-- {
		if w.live[i].kind == liveReply {
			return w.live[i]
		}
	}
	t.Fatal("no reply entry")
	return nil
}

// doSend was a third path to the same banner: a query that could not be
// sent put the transport error into loadErr — for ANY query, including the
// decorative ListTasks/GetContextReport, which Root now issues on lifecycle
// events while UI-09 is open. A decorative query failing must leave the
// transcript alone; a load query (nothing on screen yet) still reports.
func TestDecorativeQueryFailureNeverBlanksTheTranscript(t *testing.T) {
	w := loadedWorkspace(t) // real client, never started: Request fails

	w.RefreshContextReport()
	w.requestTaskRows()
	if w.loadErr != "" {
		t.Errorf("a decorative query failing set loadErr = %q", w.loadErr)
	}

	w.doSend(protocol.CmdGetSession, queryGetSession, map[string]any{"session_id": "x"})
	if w.loadErr == "" {
		t.Error("a LOAD query failing set no loadErr — nothing else would explain an empty screen")
	}
}

// Manual verification found the banner inconsistent across identical
// kill-backend-then-send repros: shown once, missing the next time, with the
// send's own failure footer present both times. The two outcomes are two
// timing windows, reproduced here deterministically instead of by luck:
//
//	A: the client has already noticed the exit, so Send fails with "backend
//	   is not connected" — writeSend used to set BOTH the banner (loadErr) and
//	   the footer from that error.
//	B: the write lands in the dead process's pipe buffer before the exit is
//	   noticed, so Send succeeds; BackendLostMsg ends the entry a moment
//	   later — footer only, and no banner, because only a Send error ever
//	   set one.
//
// Both must now agree: once the backend is known lost, the failed send has
// its footer, the viewport says the backend is not connected, and the
// transcript is still there under it.
func TestConnectionBannerAgreesAcrossBothSendWindows(t *testing.T) {
	cases := map[string]func(w *Workspace){
		"A: exit noticed before the send": func(w *Workspace) {
			loseBackend(w, 2)
			w.startSend("hello there", "")
		},
		"B: send written before the exit was noticed": func(w *Workspace) {
			w.sendMsgFn = func(*liveEntry, map[string]any) tea.Cmd { return nil }
			w.startSend("hello there", "")
			loseBackend(w, 2)
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			w := loadedWorkspace(t)
			if bannerShown(viewport(w)) {
				t.Fatal("banner shown while the backend was connected")
			}

			run(w)

			if e := lastReply(t, w); !e.status.wireTerminal() {
				t.Errorf("the failed send has no footer: status %v", e.status)
			}
			out := viewport(w)
			if !bannerShown(out) {
				t.Errorf("backend is down but the viewport does not say so:\n%s", out)
			}
			if w.loadErr != "" {
				t.Errorf("loadErr = %q: a send failure must not drive the banner", w.loadErr)
			}
			if !strings.Contains(out, "hello there") {
				t.Errorf("the banner hid the transcript (the failed send should stay visible):\n%s", out)
			}

			// Reconnected: the banner goes, derived from the same state; the
			// footer stays — that send really did fail.
			w.Update(app.DiagnosticsMsg{D: app.Diagnostics{Seq: 3, ConnState: protocol.StateConnected}})
			w.Update(protocol.ConnectedMsg{Restarted: true})
			if out := viewport(w); bannerShown(out) {
				t.Errorf("banner survived the reconnect:\n%s", out)
			}
			if e := lastReply(t, w); !e.status.wireTerminal() {
				t.Errorf("reconnecting erased the failed send's footer")
			}
		})
	}
}
