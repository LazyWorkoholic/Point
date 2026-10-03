package app

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func hasItemID(items []*array.Item, id string) bool {
	for _, it := range items {
		if it.GetID() == id {
			return true
		}
	}
	return false
}

// TestFirstRunOpensSettingsWhenNoProviderIsConfigured is F10 Done-when #1/#2:
// a profile with no stored provider default reaches the chooser on its own,
// with no key pressed. This is the ONE test in this package that
// deliberately leaves State at its zero value — see newRoot's own doc for
// why every other test opts out.
func TestFirstRunOpensSettingsWhenNoProviderIsConfigured(t *testing.T) {
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	if err != nil {
		t.Fatal(err)
	}
	size(t, r, 120, 30)

	if r.overlay == nil {
		t.Fatal("Settings did not auto-open on a profile with no provider configured")
	}
	if r.overlay.Title() != "Settings" {
		t.Errorf("the auto-opened overlay is %q, want Settings", r.overlay.Title())
	}
}

// TestNoFirstRunChooserWhenAProviderIsAlreadyConfigured is the inverse: a
// profile that already has a stored default (newRoot's own fixture) must
// never see Settings open on its own — F10 only ever opens it for a
// profile that has genuinely never been through the chooser.
func TestNoFirstRunChooserWhenAProviderIsAlreadyConfigured(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	if r.overlay != nil {
		t.Fatalf("Settings auto-opened despite an existing provider default: %q", r.overlay.Title())
	}
}

// TestEscapingTheChooserChoosesNothing is the clarification this episode's
// design turned on: dismissing the first-run chooser must be a TRUE no-op —
// nothing is written, and config.toml's own resolution (if any) is left to
// govern exactly as it did before Point's UI ever ran. r.activeProvider
// stays nil, which is also what keeps the chooser reachable again next
// launch (a real backend write would make it permanent either way).
func TestEscapingTheChooserChoosesNothing(t *testing.T) {
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	if err != nil {
		t.Fatal(err)
	}
	size(t, r, 120, 30)
	if r.overlay == nil {
		t.Fatal("Settings did not auto-open")
	}

	m, _ := r.Update(key("esc"))
	root := m.(*Root)
	if root.overlay != nil {
		t.Error("esc did not close the chooser")
	}
	if root.activeProvider != nil {
		t.Errorf("esc chose a provider: %+v", root.activeProvider)
	}
}

// TestNextEffortCyclesThroughAllFourRungs pins nextEffort's own order —
// "" (off) -> low -> medium -> high -> "" — against accidental reordering,
// since the Providers tab's Effort row (settings.go) depends on it wrapping
// back to "off" rather than getting stuck on "high".
func TestNextEffortCyclesThroughAllFourRungs(t *testing.T) {
	got := []string{""}
	cur := ""
	for i := 0; i < 4; i++ {
		cur = nextEffort(cur)
		got = append(got, cur)
	}
	want := []string{"", "low", "medium", "high", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d: got %q, want %q (full sequence %v)", i, got[i], want[i], got)
		}
	}
}

// TestProviderRowDecorNamesTheActiveDefault checks the one row this episode
// most needs to get right: once a provider is the active default, its row
// says so — F10's "Change anytime" promise depends on it being visible
// which provider that currently is.
func TestProviderRowDecorNamesTheActiveDefault(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{"ollama": {status: "healthy"}}
	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "qwen3:8b"}

	decor := r.providerRowDecor("ollama")
	if decor.Note == "" || !strings.Contains(decor.Note, "★") || decor.IconRole != theme.AccentPrimary {
		t.Errorf("active provider's row does not mark itself: note=%q role=%v", decor.Note, decor.IconRole)
	}

	other := r.providerRowDecor("openai")
	if strings.Contains(other.Note, "★") || other.IconRole == theme.AccentPrimary {
		t.Errorf("a provider that is not the default claims to be: note=%q role=%v", other.Note, other.IconRole)
	}
}

// TestSelectSpecificModelOverridesTheAlphabeticalDefault is the fix for a
// verification finding: there was no way to choose a SPECIFIC Ollama model
// other than whichever one sorted first — selecting the provider row again
// always re-picked the same one, which read as "selecting a model does
// nothing". Each model now has its own row (newProvidersTabItems); this
// pins that choosing one that is NOT alphabetically first actually sticks.
func TestSelectSpecificModelOverridesTheAlphabeticalDefault(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{"ollama": {status: "healthy"}}
	r.settings.models = map[string][]providerModelInfo{
		"ollama": {
			{id: "llama3.2:3b", supportsEffort: false}, // sorts first
			{id: "qwen2.5-coder:3b", supportsEffort: false},
		},
	}

	r.selectSpecificModel("ollama", "qwen2.5-coder:3b")

	if r.activeProvider == nil || r.activeProvider.Model != "qwen2.5-coder:3b" {
		t.Fatalf("selecting a specific model did not stick: %+v", r.activeProvider)
	}

	// The provider row's own "quick pick" still resolves to the
	// alphabetically-first model, unaffected by the specific choice above —
	// they are two different actions, not one overriding the other's rule.
	r.applyProviderChoice("ollama", r.settings.models["ollama"])
	if r.activeProvider.Model != "llama3.2:3b" {
		t.Fatalf("the provider row's quick pick changed its own rule: got %q", r.activeProvider.Model)
	}
}

// TestProviderRowNoteNeverExceedsTheBindingColumn pins the actual bug: a
// note this long truncates at ANY overlay width because popup.go's
// rowWidths caps the note column at 24 cells regardless — found when
// "detected · 3 models · local" (28 cells) rendered as "detected · 3
// models" with nothing after the final bullet, no ellipsis visible. Every
// state this function can produce must fit comfortably under that ceiling,
// not just the ones exercised by hand during verification.
func TestProviderRowNoteNeverExceedsTheBindingColumn(t *testing.T) {
	const bindingColumnCeiling = 24

	r := newRoot(t)
	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "qwen3:8b"}
	cases := []struct {
		name   string
		health map[string]providerHealthInfo
	}{
		{"unknown", nil},
		{"healthy", map[string]providerHealthInfo{"ollama": {status: "healthy"}, "openai": {status: "healthy"}}},
		{"unhealthy with a long detail", map[string]providerHealthInfo{
			"openai": {status: "unhealthy", detail: "the upstream API returned a server error"},
		}},
	}
	for _, c := range cases {
		r.settings.health = c.health
		for _, id := range []string{"ollama", "openai", "anthropic"} {
			note := r.providerRowDecor(id).Note
			if n := len([]rune(note)); n > bindingColumnCeiling {
				t.Errorf("%s/%s: note is %d cells, over the %d-cell ceiling: %q",
					c.name, id, n, bindingColumnCeiling, note)
			}
		}
	}
}

// TestExpandedProviderCollapsesWhenSwitching is the fix for a verification
// finding: once a provider's models had been fetched, newProvidersTabItems
// kept showing them FOREVER, for every provider ever looked at in the same
// popup session — so Ollama's whole model list stayed on screen under it
// even after moving on to look at OpenAI. Only expandedProvider's own
// provider's models should ever appear.
func TestExpandedProviderCollapsesWhenSwitching(t *testing.T) {
	r := newRoot(t)
	r.settings.models = map[string][]providerModelInfo{
		"ollama": {{id: "llama3.2:3b"}, {id: "qwen2.5-coder:3b"}},
		"openai": {{id: "gpt-5"}},
	}

	r.settings.expandedProvider = "ollama"
	items := newProvidersTabItems(r)
	if !hasItemID(items, "model:ollama:llama3.2:3b") {
		t.Error("ollama's models should be expanded")
	}
	if hasItemID(items, "model:openai:gpt-5") {
		t.Error("openai's models should not show while ollama is expanded")
	}

	r.settings.expandedProvider = "openai"
	items = newProvidersTabItems(r)
	if hasItemID(items, "model:ollama:llama3.2:3b") {
		t.Error("ollama's models should have collapsed after switching to openai")
	}
	if !hasItemID(items, "model:openai:gpt-5") {
		t.Error("openai's models should now be expanded")
	}
}

// TestSelectingAProviderExpandsItAndCollapsesWhicheverWasExpanded pins
// selectProvider's own side of the same fix — the act of choosing a
// provider is what moves expandedProvider, not a separate UI action.
func TestSelectingAProviderExpandsItAndCollapsesWhicheverWasExpanded(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{
		"ollama": {status: "healthy"}, "openai": {status: "healthy"},
	}
	r.settings.models = map[string][]providerModelInfo{
		"ollama": {{id: "llama3.2:3b"}},
		"openai": {{id: "gpt-5"}},
	}
	r.settings.expandedProvider = "openai"

	r.selectProvider("ollama")

	if r.settings.expandedProvider != "ollama" {
		t.Fatalf("expandedProvider = %q, want ollama", r.settings.expandedProvider)
	}
}

// TestReExpandingTheActiveProviderDoesNotRevertASpecificModelChoice is the
// fix for a real bug found via a live-testing report during F10b: after
// selectSpecificModel sets a NON-alphabetically-first model as the active
// default (restored correctly from state.json after a restart,
// independently of this bug), simply pressing Enter on the provider row
// to RE-EXPAND it and look at the model list — the only way this tab lets
// you see them — used to unconditionally re-run applyProviderChoice's
// quick pick (always models[0]) and PERSIST that as a new choice via a
// real ProviderDefaultSetMsg. The active model reverting to
// alphabetically-first was never cosmetic and was never a restart/replay
// bug: it was this row's own Enter handler treating "look" and "choose"
// as the same action. selectProvider must leave an ALREADY-active
// provider's model choice untouched; only choosing a DIFFERENT provider
// (or this one for the first time) should apply the quick pick.
func TestReExpandingTheActiveProviderDoesNotRevertASpecificModelChoice(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{"ollama": {status: "healthy"}}
	r.settings.models = map[string][]providerModelInfo{
		"ollama": {
			{id: "llama3.2:3b"},      // sorts first -- the quick pick's own default
			{id: "qwen2.5-coder:3b"}, // the user's actual, specific choice
		},
	}
	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "qwen2.5-coder:3b"}

	cmd := r.selectProvider("ollama") // Enter on the provider row, to re-expand and look

	if r.activeProvider.Model != "qwen2.5-coder:3b" {
		t.Fatalf("re-expanding reverted the active model: got %q, want qwen2.5-coder:3b (unchanged)",
			r.activeProvider.Model)
	}
	// Stronger than "if cmd fires a message, that message isn't
	// ProviderDefaultSetMsg": expanding an already-active provider whose
	// models are already cached has NOTHING to do, so the command itself
	// must be nil. ProviderDefaultSetMsg is the ONLY thing that reaches
	// chat.Workspace.applyProviderDefault, which is the ONLY thing that
	// sends SetSessionProvider (chat\providers.go) -- a nil cmd here is
	// what proves neither ever fires, not just that this ONE message type
	// didn't.
	if cmd != nil {
		t.Fatalf("re-expanding an already-active, already-cached provider produced a "+
			"command (%v) -- there is nothing to persist or send, nothing changed", cmd())
	}
	// The row is still expanded, same as before this fix.
	if r.settings.expandedProvider != "ollama" {
		t.Fatalf("expandedProvider = %q, want ollama", r.settings.expandedProvider)
	}
}

// TestSelectingADifferentProviderStillAppliesTheQuickPick is this fix's
// other half: the guard above must not swallow the ordinary "choosing a
// provider for the first time, or switching to a different one" case,
// which still needs applyProviderChoice's quick pick.
func TestSelectingADifferentProviderStillAppliesTheQuickPick(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{
		"ollama": {status: "healthy"}, "openai": {status: "healthy"},
	}
	r.settings.models = map[string][]providerModelInfo{
		"ollama": {{id: "llama3.2:3b"}},
		"openai": {{id: "gpt-5"}},
	}
	r.activeProvider = &config.ProviderDefault{Provider: "ollama", Model: "llama3.2:3b"}

	r.selectProvider("openai")

	if r.activeProvider == nil || r.activeProvider.Provider != "openai" || r.activeProvider.Model != "gpt-5" {
		t.Fatalf("switching to a different provider did not apply its quick pick: %+v", r.activeProvider)
	}
}

// TestProviderRowShowsALoadingIndicatorWhileFetchingModels is the other
// half of a verification question ("is switching providers slow, or is
// there just no indicator?") — ListProviderModels genuinely is a network
// call, so the honest fix is telling the user one is in flight, not making
// it instant.
func TestProviderRowShowsALoadingIndicatorWhileFetchingModels(t *testing.T) {
	r := newRoot(t)
	r.settings.health = map[string]providerHealthInfo{"ollama": {status: "healthy"}}
	r.settings.modelsReqID = map[string]string{"ollama": "corr-1"}

	if note := r.providerRowDecor("ollama").Note; note != "loading models…" {
		t.Errorf("note = %q, want the loading indicator", note)
	}
}

// TestSettingsRetriesTheProvidersFetchOnceConnected is THE actual
// first-launch hang, found by tracing protocol\client.go rather than a
// fourth guess: Client.Request returns "backend is not connected"
// SYNCHRONOUSLY, with no queuing, whenever c.backend is still nil — which it
// is until the handshake completes, and the first-run chooser (root.go's
// WindowSizeMsg case) opens from the terminal's own first resize, which
// arrives from the OS essentially immediately, almost certainly before a
// brand-new profile's backend has finished running every migration from
// scratch. The request was never SENT at all; nothing was ever going to
// make it arrive. picker.go hit the identical shape once already
// ("awaitingConnection" / "Bug B" in its own comments) — this is that same
// fix for Settings' own fetch.
func TestSettingsRetriesTheProvidersFetchOnceConnected(t *testing.T) {
	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		// Never Start()ed -- a unit test cannot spin up a real backend, so
		// every Request fails with exactly "backend is not connected",
		// which is what this test needs: proof an attempt was made, then
		// proof it is retried once a connection is simulated.
		Client: protocol.NewClient(protocol.Options{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	size(t, r, 120, 30) // openSettingsPopup's own OpenOverlay needs a real terminal size to fit against

	r.openSettingsPopup()
	if !r.settings.awaitingConnection {
		t.Fatal("a ListProviders attempt against a not-yet-connected client should set awaitingConnection")
	}
	if r.settings.providersReqID != "" {
		t.Fatal("a failed send must not leave a corr_id behind as if it had gone out")
	}

	var sentCmds []string
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		sentCmds = append(sentCmds, cmdType)
		return "corr-1", true
	}
	r.Update(protocol.ConnectedMsg{})

	if r.settings.awaitingConnection {
		t.Error("ConnectedMsg should have cleared awaitingConnection")
	}
	if len(sentCmds) != 1 || sentCmds[0] != protocol.CmdListProviders {
		t.Fatalf("ConnectedMsg did not retry the Providers fetch: %v", sentCmds)
	}
	if r.settings.providersReqID != "corr-1" {
		t.Errorf("providersReqID = %q, want the retried request's id", r.settings.providersReqID)
	}
}
