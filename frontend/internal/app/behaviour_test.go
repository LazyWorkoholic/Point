package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// TestBehaviourRowDecorDefaultsWhenNothingIsOverridden is F10b Decision 2:
// a row with no active override reads "default" — never a guessed
// config.toml value, since this process cannot read that table and B26
// built no way to ask for it.
func TestBehaviourRowDecorDefaultsWhenNothingIsOverridden(t *testing.T) {
	r := newRoot(t)
	for _, k := range behaviourKeys {
		// Every row, inert or not, reads the same plain "default" text —
		// behaviourInertKeys' row carries no note suffix (see its own doc
		// for why one was tried and removed: a verification round found
		// it genuinely overflows the note column at the real popup
		// width, not merely at a mentally-counted ceiling). The ICON is
		// what distinguishes it; TestBehaviourInertRowIsVisuallyDistinct
		// and TestBehaviourInertRowNeverOverflowsAtRealWidth cover that.
		decor := r.behaviourRowDecor(k.Key)
		if decor.Note != "default" {
			t.Errorf("%s: note = %q, want %q", k.Key, decor.Note, "default")
		}
	}
}

// TestBehaviourInertRowIsVisuallyDistinct is the direct ask behind F10b's
// Decision 4 follow-up: chat.queue_depth has no reachable, observable
// effect through the shipped app today (see that decision's own
// file:line trace), so its row must never render identically to the
// four that actually work — a silently inert control is worse than an
// honestly labelled one. The distinguishing signal lives entirely in the
// icon (glyph + role) — see TestBehaviourInertRowNeverOverflowsAtRealWidth
// for why the note itself deliberately carries no extra text.
func TestBehaviourInertRowIsVisuallyDistinct(t *testing.T) {
	r := newRoot(t)

	inert := r.behaviourRowDecor("chat.queue_depth")
	working := r.behaviourRowDecor("research.stale_after_days") // a key that DOES work

	if inert.IconRole != theme.Warning {
		t.Errorf("chat.queue_depth's icon role = %v, want theme.Warning", inert.IconRole)
	}
	if inert.Icon == working.Icon && inert.IconRole == working.IconRole {
		t.Fatal("the inert row's icon is indistinguishable from a working row's")
	}
}

// TestSettingsHintExplainsTheInertRowWhenSelected is the hint half of the
// same follow-up: the icon/note flag THAT something is off, the hint
// explains WHY, the moment this specific row is actually selected.
// chat.queue_depth is behaviourKeys' own first entry, so a freshly built
// Behaviour tab already starts with it selected — no navigation needed.
func TestSettingsHintExplainsTheInertRowWhenSelected(t *testing.T) {
	r := newRoot(t)
	r.settings.popup = NewPopup(PopupConfig{
		Title: "Settings",
		Tabs:  []PopupTab{{Label: "Behaviour", Items: newBehaviourTabItems(r)}},
		MinW:  74, MinH: 16,
	})

	it, ok := r.settings.popup.menu.Selected()
	if !ok || it.GetID() != "behaviour:chat.queue_depth" {
		t.Fatalf("expected chat.queue_depth selected by default, got %+v ok=%v", it, ok)
	}

	if hint := r.settingsHint(); hint == "" {
		t.Fatal("selecting the inert row produced no explanatory hint")
	}
}

// TestBehaviourInertRowNeverOverflowsAtRealWidth renders the ACTUAL
// Settings popup at its ACTUAL floor width (MinW: 74, the same the real
// app opens it at) and inspects the real bytes — not a mental or rune
// count against an assumed ceiling. That distinction matters here
// specifically: a prior version of this coverage asserted
// len([]rune(note)) <= 24, which PASSED while the real render TRUNCATED
// the same string, because rowWidths' note-column budget is
// avail/3 when that falls below the 24-cell ceiling — at this popup's
// real width it is 23, not 24, and the 24-cell mental ceiling was simply
// the wrong number to check against. Only a real render at the real
// width can catch that class of mismatch, which is why this test calls
// Popup.View directly instead of behaviourRowDecor in isolation.
//
// Also pins Bug 1's fix structurally: the inert row's icon-to-label gap
// must be byte-for-byte the same shape a working row's is ("<icon><one
// space><label>"), not just "looks about right".
func TestBehaviourInertRowNeverOverflowsAtRealWidth(t *testing.T) {
	r := newRoot(t)
	r.theme = theme.New("turquoise", theme.BackgroundTransparent)
	popup := NewPopup(PopupConfig{
		Title:    "Settings",
		Tabs:     []PopupTab{{Label: "Behaviour", Items: newBehaviourTabItems(r)}},
		OnSelect: r.onSettingsSelect,
		Decor:    r.settingsDecor,
		Hint:     r.settingsHint,
		MinW:     74, MinH: 16,
	})
	r.settings.popup = popup

	for _, depth := range []float64{0, 25, 100} {
		r.behaviourOverrides = map[string]any{"chat.queue_depth": depth}
		frame := stripANSI(popup.View(r.theme, 74, 16))

		for _, line := range strings.Split(frame, "\n") {
			if !strings.Contains(line, "Queue depth") && !strings.Contains(line, "not yet enforced") {
				continue
			}
			if strings.Contains(line, "…") {
				t.Fatalf("depth=%v: a Behaviour-tab line truncated with an ellipsis: %q", depth, line)
			}
			if strings.Contains(line, "Queue depth") && !strings.Contains(line, "! Queue depth") {
				t.Fatalf("depth=%v: icon-to-label spacing does not match a working row's shape: %q", depth, line)
			}
		}
	}
}

// TestBehaviourRowDecorShowsTheOverriddenValue pins both value shapes a
// row can render: a plain int (decoded off JSON as float64, the same
// shape every map[string]any payload in this package carries a number in)
// and a bool.
func TestBehaviourRowDecorShowsTheOverriddenValue(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{
		"chat.queue_depth":              float64(0),
		"project.allow_outside_project": true,
	}

	// chat.queue_depth's note is identical in SHAPE to a working row's —
	// only the icon distinguishes it (TestBehaviourInertRowIsVisuallyDistinct).
	if note := r.behaviourRowDecor("chat.queue_depth").Note; note != "overridden → 0" {
		t.Errorf("queue_depth note = %q, want %q", note, "overridden → 0")
	}
	if note := r.behaviourRowDecor("project.allow_outside_project").Note; note != "overridden → true" {
		t.Errorf("allow_outside_project note = %q, want %q", note, "overridden → true")
	}
	// Untouched keys stay at default even once SOME override is active —
	// each row's state is independent.
	if note := r.behaviourRowDecor("research.stale_after_days").Note; note != "default" {
		t.Errorf("stale_after_days note = %q, want %q", note, "default")
	}
}

// TestOnBehaviourSelectBoolTogglesTheInverse is the bool-row half of
// F10b's Build §1: Enter on a bool row sends SetBehaviourOverride with the
// CURRENT value's inverse, not a hardcoded true/false.
func TestOnBehaviourSelectBoolTogglesTheInverse(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{"project.allow_outside_project": true}

	var gotPayload map[string]any
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		if cmdType != protocol.CmdSetBehaviourOverride {
			t.Fatalf("cmdType = %q, want %q", cmdType, protocol.CmdSetBehaviourOverride)
		}
		gotPayload = payload
		return "corr-1", true
	}

	r.onBehaviourSelect("project.allow_outside_project")

	if gotPayload["key"] != "project.allow_outside_project" {
		t.Fatalf("payload key = %v", gotPayload["key"])
	}
	if gotPayload["value"] != false {
		t.Errorf("payload value = %v, want false (the inverse of the active true override)", gotPayload["value"])
	}
}

// TestOnBehaviourSelectNumericPrefillsTheCurrentOverride checks the prompt
// opened for an int row starts with whatever is already overridden, not
// blank — editing an existing value should not require retyping it from
// scratch.
func TestOnBehaviourSelectNumericPrefillsTheCurrentOverride(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{"chat.queue_depth": float64(3)}
	r.settings.popup = NewPopup(PopupConfig{Title: "Settings", MinW: 74, MinH: 16})

	cmd := r.onBehaviourSelect("chat.queue_depth")
	if cmd == nil {
		t.Fatal("expected a command opening the numeric prompt")
	}
	msg, ok := cmd().(openOverlayMsg)
	if !ok {
		t.Fatalf("got %T, want openOverlayMsg", cmd())
	}
	cp, ok := msg.o.(*CredentialPrompt)
	if !ok {
		t.Fatalf("got %T, want *CredentialPrompt", msg.o)
	}
	if got := string(cp.runes); got != "3" {
		t.Errorf("prefill = %q, want %q", got, "3")
	}
}

// TestSendBehaviourClearOmitsTheValueField pins B26 §1b's clear shape: the
// SAME key, with "value" absent from the payload entirely (not present
// with a nil/zero value) — that absence is what the backend reads as
// "clear", per DetachContext's own precedent.
func TestSendBehaviourClearOmitsTheValueField(t *testing.T) {
	r := newRoot(t)
	var gotPayload map[string]any
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		gotPayload = payload
		return "corr-1", true
	}

	r.sendBehaviourClear("chat.queue_depth")

	if _, present := gotPayload["value"]; present {
		t.Errorf("clear payload still has a 'value' field: %v", gotPayload)
	}
	if gotPayload["key"] != "chat.queue_depth" {
		t.Errorf("clear payload key = %v", gotPayload["key"])
	}
}

// TestOnBehaviourOverrideSetAppliesTheFullEchoedMap is B26 §1b's own
// precedent (echo back full resulting state, not a delta) exercised from
// this side: the reply's "overrides" field REPLACES r.behaviourOverrides
// wholesale.
func TestOnBehaviourOverrideSetAppliesTheFullEchoedMap(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{"chat.queue_depth": float64(9)} // stale, about to be replaced

	payload, _ := json.Marshal(map[string]any{
		"key":       "project.allow_outside_project",
		"overrides": map[string]any{"project.allow_outside_project": true},
	})
	r.onBehaviourOverrideSet("project.allow_outside_project",
		protocol.Envelope{Type: protocol.EvBehaviourOverrideSet, Payload: payload})

	if len(r.behaviourOverrides) != 1 {
		t.Fatalf("behaviourOverrides = %v, want exactly the echoed map", r.behaviourOverrides)
	}
	if v, _ := r.behaviourOverrides["project.allow_outside_project"].(bool); !v {
		t.Errorf("project.allow_outside_project = %v, want true", r.behaviourOverrides["project.allow_outside_project"])
	}
	if _, stale := r.behaviourOverrides["chat.queue_depth"]; stale {
		t.Error("the stale queue_depth entry survived a wholesale replace — it should not have")
	}
}

// TestOnBehaviourOverrideSetErrorLeavesOverridesUntouched is Decision 2's
// other half: a rejected request must never make this process CLAIM an
// override that was never actually confirmed.
func TestOnBehaviourOverrideSetErrorLeavesOverridesUntouched(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{"chat.queue_depth": float64(0)}

	payload, _ := json.Marshal(map[string]any{"error": "unknown behaviour override key"})
	r.onBehaviourOverrideSet("chat.queue_depth", protocol.Envelope{Type: protocol.EvError, Payload: payload})

	if v, _ := r.behaviourOverrides["chat.queue_depth"].(float64); v != 0 {
		t.Errorf("an Error reply changed r.behaviourOverrides: %v", r.behaviourOverrides)
	}
}

// TestReplayBehaviourOverridesResendsEveryKey is F10b Done-when #9's own
// mechanism, pinned directly: with MULTIPLE overrides active, a replay
// (fired from every protocol.ConnectedMsg) must resend ALL of them in one
// pass, not just whichever key happens to be last in iteration order —
// the exact distinction #7/#8 alone (a single active override) cannot
// prove, since "replay the most recent key" and "replay the whole map"
// look identical with only one entry.
func TestReplayBehaviourOverridesResendsEveryKey(t *testing.T) {
	r := newRoot(t)
	r.behaviourOverrides = map[string]any{
		"chat.queue_depth":              float64(0),
		"project.allow_outside_project": true,
	}

	sent := map[string]any{}
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		key, _ := payload["key"].(string)
		sent[key] = payload["value"]
		return "corr-" + key, true
	}

	r.replayBehaviourOverrides()

	if len(sent) != 2 {
		t.Fatalf("replay sent %d requests, want 2: %v", len(sent), sent)
	}
	if sent["chat.queue_depth"] != float64(0) {
		t.Errorf("chat.queue_depth replayed as %v, want 0", sent["chat.queue_depth"])
	}
	if sent["project.allow_outside_project"] != true {
		t.Errorf("project.allow_outside_project replayed as %v, want true", sent["project.allow_outside_project"])
	}
}

// TestReplayBehaviourOverridesIsANoOpWithNothingActive guards the common
// case (no override has ever been set) against sending anything at all —
// every connect would otherwise fire a pointless round trip forever.
func TestReplayBehaviourOverridesIsANoOpWithNothingActive(t *testing.T) {
	r := newRoot(t)
	sendCount := 0
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		sendCount++
		return "corr-1", true
	}
	r.replayBehaviourOverrides()
	if sendCount != 0 {
		t.Errorf("replay sent %d requests with nothing overridden, want 0", sendCount)
	}
}

// TestNumericBehaviourPromptRejectsNonNumericInputLocally is F10b
// Done-when #5: a bad value never reaches the network at all.
//
// submit() is called directly rather than via Update(key("enter")) — going
// through Update would trip credential.go's own pasteSettleWindow guard
// (an Enter within 200ms of the last typed rune is swallowed as a paste's
// trailing newline), which is the right behaviour for real typing but
// irrelevant to what THIS test checks and would make it flaky by
// construction.
func TestNumericBehaviourPromptRejectsNonNumericInputLocally(t *testing.T) {
	cp := newBehaviourValuePrompt("chat.queue_depth", "Queue depth", "", nil)
	cp.runes = []rune("a")
	ov, cmd := cp.submit()

	if ov == nil {
		t.Fatal("a rejected submit must not close the prompt")
	}
	if cmd != nil {
		t.Fatal("a non-numeric value must never produce a command (no request should be sent)")
	}
	if cp.status == "" || cp.statusRole != theme.Warning {
		t.Errorf("status = %q/%v, want a warning explaining the rejection", cp.status, cp.statusRole)
	}
}

// TestNumericBehaviourPromptSubmitsAValidInt is the accepting half of the
// same path: a real whole number produces submitBehaviourValueMsg, not
// submitCredentialMsg — the two prompts share one Update/submit, and this
// pins that the Behaviour branch is actually the one taken.
func TestNumericBehaviourPromptSubmitsAValidInt(t *testing.T) {
	cp := newBehaviourValuePrompt("research.stale_after_days", "Stale after (days)", "", nil)
	cp.runes = []rune("7")
	_, cmd := cp.submit()

	if cmd == nil {
		t.Fatal("a valid whole number should produce a command")
	}
	msg, ok := cmd().(submitBehaviourValueMsg)
	if !ok {
		t.Fatalf("got %T, want submitBehaviourValueMsg", cmd())
	}
	if msg.key != "research.stale_after_days" || msg.value != 7 {
		t.Errorf("got %+v, want key=research.stale_after_days value=7", msg)
	}
}
