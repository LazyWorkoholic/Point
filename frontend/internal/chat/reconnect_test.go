package chat

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// A send attempted while the backend was dead leaves the client's own
// "backend is not connected" in w.loadErr, which render.go draws as the
// "! ..." banner in place of the transcript. Reconnecting (ctrl+w r) must
// clear it -- before onConnected, only beginBootstrap and setSession ever
// did, and beginBootstrap returns immediately once bootstrapped, so the
// banner outlived the outage until the app was restarted.
func TestReconnectClearsTheOutageBanner(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.bootstrapped = true
	w.sessionID = "sess-1"
	w.loadErr = "backend is not connected"

	w.Update(protocol.ConnectedMsg{Restarted: true})

	if w.loadErr != "" {
		t.Fatalf("loadErr = %q after reconnect, want cleared", w.loadErr)
	}
	lines := w.viewportLines(layout.Rect{W: 60, H: 12})
	if strings.Contains(strings.Join(lines, "\n"), "not connected") {
		t.Errorf("viewport still shows the outage banner after reconnect:\n%s", strings.Join(lines, "\n"))
	}
	if len(w.pending) != 0 {
		t.Errorf("a LOADED session re-bootstrapped on reconnect (%d queries sent); that litters the database", len(w.pending))
	}
}

// A bootstrap whose replies died with the old process (no session ever
// loaded) must start again on reconnect -- otherwise w.loading stays true
// and the viewport says "loading session…" forever.
func TestReconnectRebootstrapsAnUnfinishedLoad(t *testing.T) {
	w := connectedTestWorkspace(t)
	w.bootstrapped = true
	w.loading = true
	w.loadErr = "backend is not connected"

	w.Update(protocol.ConnectedMsg{Restarted: true})

	if w.loadErr != "" {
		t.Errorf("loadErr = %q after reconnect, want cleared", w.loadErr)
	}
	asked := false
	for _, kind := range w.pending {
		if kind == queryListProviders {
			asked = true
		}
	}
	if !asked {
		t.Error("reconnect did not restart the bootstrap for a session that never loaded")
	}
}
