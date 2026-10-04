// F10c — Settings: Demo Mode. Tests for the provider-catalog row, the
// ProjectInit side effect fired on selection, its reply handling, and the
// header badge — the pieces this episode actually added to app\settings.go
// and app\render.go.
package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// TestDemoIsInTheProviderCatalogNeedingNoKey pins providerCatalog's one new
// row: selectable, no credential prompt ever offered for it.
func TestDemoIsInTheProviderCatalogNeedingNoKey(t *testing.T) {
	cat, ok := providerCatalogByID("demo")
	if !ok {
		t.Fatal("demo is not in providerCatalog")
	}
	if cat.NeedsKey {
		t.Error("demo.NeedsKey = true, want false — DemoProvider is never gated by a credential")
	}
	if cat.Label != "Demo mode" {
		t.Errorf("demo's label = %q, want %q", cat.Label, "Demo mode")
	}
}

// TestSelectingDemoNeverOpensTheCredentialPrompt is Done-when #1: health
// always reports healthy for demo (B28), so selectProvider's existing
// usable-gate must pass it straight through to finalizeProviderSelection,
// never openCredentialPrompt — the same path NeedsKey:false already gives
// Ollama, now exercised for demo specifically.
func TestSelectingDemoNeverOpensTheCredentialPrompt(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{"demo": {status: "healthy"}}
	r.settings.models = map[string][]providerModelInfo{
		"demo": {{id: "point-demo-v1", displayName: "point-demo-v1"}},
	}
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		return "corr", true
	}

	r.selectProvider("demo")

	if r.settings.credential != nil {
		t.Error("selecting demo opened a credential prompt")
	}
	if r.activeProvider == nil || r.activeProvider.Provider != "demo" {
		t.Fatalf("demo did not become the active provider: %+v", r.activeProvider)
	}
}

// TestApplyModelChoiceFiresProjectInitForDemo is Decision 1/Question 2: the
// path is a join onto env.BackendDir (protocol.FindBackendDir's own
// resolution, already done at startup), not a second guessed path, and the
// request is tracked so handleSettingsEnvelope can claim its reply.
func TestApplyModelChoiceFiresProjectInitForDemo(t *testing.T) {
	r := newRoot(t)
	r.env.BackendDir = `D:\Projects\Point\backend`

	var gotCmd string
	var gotPayload map[string]any
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		gotCmd, gotPayload = cmdType, payload
		return "corr-demo-init", true
	}

	r.applyModelChoice("demo", providerModelInfo{id: "point-demo-v1"})

	if gotCmd != protocol.CmdProjectInit {
		t.Fatalf("sent %q, want %q", gotCmd, protocol.CmdProjectInit)
	}
	want := `D:\Projects\Point\backend\demo\sample_project`
	if gotPayload["path"] != want {
		t.Errorf("ProjectInit path = %v, want %v", gotPayload["path"], want)
	}
	if r.settings.demoProjectReqID != "corr-demo-init" {
		t.Errorf("demoProjectReqID = %q, want corr-demo-init", r.settings.demoProjectReqID)
	}
}

// TestApplyModelChoiceFiresNothingForOtherProviders confirms the ProjectInit
// side effect is specific to id=="demo" — selecting any other provider must
// not touch demoProjectReqID or send ProjectInit at all.
func TestApplyModelChoiceFiresNothingForOtherProviders(t *testing.T) {
	r := newRoot(t)
	r.env.BackendDir = `D:\Projects\Point\backend`

	var sentCmds []string
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		sentCmds = append(sentCmds, cmdType)
		return "corr", true
	}

	r.applyModelChoice("ollama", providerModelInfo{id: "qwen3:8b"})

	if len(sentCmds) != 0 {
		t.Errorf("selecting ollama sent %v, want nothing", sentCmds)
	}
	if r.settings.demoProjectReqID != "" {
		t.Errorf("demoProjectReqID = %q, want empty for a non-demo selection", r.settings.demoProjectReqID)
	}
}

// TestApplyModelChoiceSkipsProjectInitWithNoBackend — env.BackendDir empty
// (no backend found at all, F10's own "not fatal" boot path) must not
// panic or send a request naming an empty path.
func TestApplyModelChoiceSkipsProjectInitWithNoBackend(t *testing.T) {
	r := newRoot(t)
	r.env.BackendDir = ""

	sendCalled := false
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		sendCalled = true
		return "corr", true
	}

	r.applyModelChoice("demo", providerModelInfo{id: "point-demo-v1"})

	if sendCalled {
		t.Error("ProjectInit was sent despite no backend directory being known")
	}
}

func demoProjectEnvelope(t *testing.T, projectID string) protocol.Envelope {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"project": map[string]any{"id": projectID}})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Envelope{Type: protocol.EvProject, Payload: raw}
}

// TestOnDemoProjectInitBindsTheReturnedID is the reply half: the project id
// lands on r.activeProvider.ProjectID and is re-emitted via
// ProviderDefaultSetMsg so chat.Workspace persists the completed default —
// Decision 1's two-step sequencing.
func TestOnDemoProjectInitBindsTheReturnedID(t *testing.T) {
	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "demo", Model: "point-demo-v1"}

	cmd := r.onDemoProjectInit(demoProjectEnvelope(t, "proj-sample-123"))
	if cmd == nil {
		t.Fatal("a successful Project reply produced no command")
	}
	msg, ok := cmd().(ProviderDefaultSetMsg)
	if !ok {
		t.Fatalf("got %T, want ProviderDefaultSetMsg", cmd())
	}
	if msg.Default.ProjectID != "proj-sample-123" {
		t.Errorf("ProjectID = %q, want proj-sample-123", msg.Default.ProjectID)
	}
	if msg.Default.Provider != "demo" || msg.Default.Model != "point-demo-v1" {
		t.Errorf("the rest of the default was not preserved: %+v", msg.Default)
	}
	if r.activeProvider.ProjectID != "proj-sample-123" {
		t.Errorf("r.activeProvider.ProjectID = %q, want proj-sample-123", r.activeProvider.ProjectID)
	}
}

// TestOnDemoProjectInitIgnoresAStaleReply is Decision 1's own guard: if the
// user switched to a different provider before this reply landed, it must
// not resurrect demo or stamp a ProjectID onto whatever is active now.
func TestOnDemoProjectInitIgnoresAStaleReply(t *testing.T) {
	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "qwen3:8b"}

	cmd := r.onDemoProjectInit(demoProjectEnvelope(t, "proj-sample-123"))
	if cmd != nil {
		t.Error("a stale ProjectInit reply after switching away from demo should be a no-op")
	}
	if r.activeProvider.Provider != "ollama" || r.activeProvider.ProjectID != "" {
		t.Errorf("a stale reply mutated the active provider: %+v", r.activeProvider)
	}
}

// TestOnDemoProjectInitHandlesRefusal — a refused ProjectInit (EvError, or
// any non-EvProject reply) leaves demo selected but unbound, never panics.
func TestOnDemoProjectInitHandlesRefusal(t *testing.T) {
	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "demo", Model: "point-demo-v1"}

	cmd := r.onDemoProjectInit(protocol.Envelope{Type: protocol.EvError, Payload: []byte(`{"error":"disk full"}`)})
	if cmd != nil {
		t.Error("a refused ProjectInit should produce no command")
	}
	if r.activeProvider.ProjectID != "" {
		t.Errorf("a refused ProjectInit set a ProjectID anyway: %+v", r.activeProvider)
	}
}

// TestHandleSettingsEnvelopeRoutesTheDemoProjectReply confirms the new
// branch in handleSettingsEnvelope actually claims the reply by corr_id and
// clears demoProjectReqID, the same shape every other in-flight request on
// this struct already uses.
func TestHandleSettingsEnvelopeRoutesTheDemoProjectReply(t *testing.T) {
	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "demo", Model: "point-demo-v1"}
	r.settings.demoProjectReqID = "corr-demo-init"

	cmd := r.handleSettingsEnvelope(protocol.Envelope{
		CorrID: "corr-demo-init",
		Type:   protocol.EvProject,
		Payload: mustJSON(t, map[string]any{
			"project": map[string]any{"id": "proj-xyz"},
		}),
	})
	if cmd == nil {
		t.Fatal("the demo project reply was not routed at all")
	}
	if r.settings.demoProjectReqID != "" {
		t.Error("demoProjectReqID was not cleared after its reply landed")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestDemoBadgeAppearsOnlyWhileDemoIsActive is D55/F10's "it must announce
// itself" — present for demo, absent for every other provider and for nil.
func TestDemoBadgeAppearsOnlyWhileDemoIsActive(t *testing.T) {
	r := newRoot(t)

	r.activeProvider = nil
	if got := r.demoBadge(); got != "" {
		t.Errorf("badge shown with no active provider: %q", got)
	}

	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "qwen3:8b"}
	if got := r.demoBadge(); got != "" {
		t.Errorf("badge shown for ollama: %q", got)
	}

	r.activeProvider = &config.ProviderDefault{Provider: "demo", Model: "point-demo-v1"}
	if got := r.demoBadge(); got == "" {
		t.Error("no badge shown while demo is the active provider")
	}
}

// TestDemoBadgeSurvivesCompactHeader — F10's "for its entire duration"
// applies to the compact header too (HeaderCompact, below the breakpoint
// that drops left/right), not just the normal three-segment layout.
func TestDemoBadgeSurvivesCompactHeader(t *testing.T) {
	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "demo", Model: "point-demo-v1"}

	compact := r.header(layout.Layout{Header: layout.Rect{W: 40}, HeaderCompact: true})
	if !strings.Contains(compact, "DEMO") {
		t.Errorf("compact header does not show the DEMO badge: %q", compact)
	}

	full := r.header(layout.Layout{Header: layout.Rect{W: 80}, HeaderCompact: false})
	if !strings.Contains(full, "DEMO") {
		t.Errorf("full header does not show the DEMO badge: %q", full)
	}
}
