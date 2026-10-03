package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// sampleDoctorReport mirrors build_doctor_report()'s own keys
// (backend\cli.py) closely enough to exercise formatDoctorReport's whole
// section list, including the one fact this episode actually adds beyond
// parity with `point doctor`: behaviour_overrides.
func sampleDoctorReport() map[string]any {
	return map[string]any{
		"version_file":                      "/repo/VERSION",
		"version":                           "0.9.0",
		"go_frontend_status":                "built",
		"envelope_version":                  float64(1),
		"python_version":                    "3.12.0",
		"python_executable":                 "/usr/bin/python3",
		"dependency_report":                 "all present",
		"database_path":                     "/profile/runtime.db",
		"database_state":                    "schema v7 (expected v7)",
		"legacy_db_path":                    nil,
		"config_path":                       "/profile/config.toml",
		"config_exists":                     true,
		"config_error":                      "",
		"credentials_path":                  "/profile/credentials.toml",
		"credentials_exists":                false,
		"credentials_permissions_inherited": false,
		"state_path":                        "/profile/state.json",
		"state_exists":                      true,
		"log_path":                          "/profile/logs/point.log",
		"log_exists":                        true,
		"providers": []any{
			map[string]any{"name": "openai", "configured": true, "source": "env", "masked_key": "sk-…abcd"},
			map[string]any{"name": "anthropic", "configured": false},
		},
		"providers_configured_count": float64(1),
		"ollama_report":              "detected, 3 models",
		"mock_enabled":               false,
		"behaviour_overrides": map[string]any{
			"chat.queue_depth": float64(0),
		},
	}
}

// TestFormatDoctorReportIncludesBehaviourOverrides is this episode's one
// addition beyond `point doctor` parity (B26's own report gained the
// field; this is the first renderer of it) — a human reading the About
// tab's card must be able to see what is currently overridden without a
// second action.
func TestFormatDoctorReportIncludesBehaviourOverrides(t *testing.T) {
	text := formatDoctorReport(sampleDoctorReport())
	if !strings.Contains(text, "behaviour overrides") {
		t.Fatal("formatted report has no behaviour overrides section")
	}
	if !strings.Contains(text, "chat.queue_depth") || !strings.Contains(text, "0") {
		t.Errorf("formatted report does not show the active override:\n%s", text)
	}
}

// TestFormatDoctorReportWithNoOverridesSaysSo is the honest-empty-state
// case — "none active", not a blank section that reads as a rendering bug.
func TestFormatDoctorReportWithNoOverridesSaysSo(t *testing.T) {
	r := sampleDoctorReport()
	r["behaviour_overrides"] = map[string]any{}
	text := formatDoctorReport(r)
	if !strings.Contains(text, "none active") {
		t.Errorf("empty behaviour_overrides should say so plainly:\n%s", text)
	}
}

// TestFormatDoctorReportCoversEveryCliSection pins the same grouping
// cli.py's _print_doctor_report uses (version, go frontend, python,
// dependencies, database, config/credentials/state/logs, providers,
// ollama, mock) so a report that looks right on one side looks right on
// the other — not byte-identical (that guarantee is cli.py's own, B26
// Done-when #4), but every fact present.
func TestFormatDoctorReportCoversEveryCliSection(t *testing.T) {
	text := formatDoctorReport(sampleDoctorReport())
	for _, want := range []string{
		"0.9.0", "built", "3.12.0", "all present", "runtime.db",
		"schema v7", "config.toml", "credentials.toml", "state.json",
		"point.log", "openai", "anthropic", "no credentials",
		"detected, 3 models", "not enabled",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("formatted report is missing %q:\n%s", want, text)
		}
	}
}

// TestOnAboutSelectTracksTheDoctorRequest checks the plumbing side: Enter
// on "Run doctor" sends GetDoctorReport and remembers the correlation id
// so the reply can be claimed in handleSettingsEnvelope.
func TestOnAboutSelectTracksTheDoctorRequest(t *testing.T) {
	r := newRoot(t)
	var gotCmd string
	r.SettingsSendFn = func(cmdType string, payload map[string]any) (string, bool) {
		gotCmd = cmdType
		return "corr-doctor", true
	}

	r.onAboutSelect()

	if gotCmd != protocol.CmdGetDoctorReport {
		t.Fatalf("sent %q, want %q", gotCmd, protocol.CmdGetDoctorReport)
	}
	if r.settings.doctorReqID != "corr-doctor" {
		t.Errorf("doctorReqID = %q, want corr-doctor", r.settings.doctorReqID)
	}
}

// TestOnDoctorReportOpensACardOverSettings is Build §3: the reply opens a
// scrollable card, back-returning to the SAME live Settings popup instance
// — the same "d/esc both return to the tray" shape ResultCard already
// established, reused here for "esc returns to Settings".
func TestOnDoctorReportOpensACardOverSettings(t *testing.T) {
	r := newRoot(t)
	r.settings.popup = NewPopup(PopupConfig{Title: "Settings", MinW: 74, MinH: 16})

	payload, _ := json.Marshal(sampleDoctorReport())
	cmd := r.onDoctorReport(protocol.Envelope{Type: protocol.EvDoctorReport, Payload: payload})
	if cmd == nil {
		t.Fatal("a successful DoctorReport should open a card")
	}
	msg, ok := cmd().(openOverlayMsg)
	if !ok {
		t.Fatalf("got %T, want openOverlayMsg", cmd())
	}
	card, ok := msg.o.(*DoctorCard)
	if !ok {
		t.Fatalf("got %T, want *DoctorCard", msg.o)
	}
	if card.back != r.settings.popup {
		t.Error("the card's back-target is not the live Settings popup instance")
	}
	if !strings.Contains(card.body, "chat.queue_depth") {
		t.Error("the card's body does not carry the report's own facts")
	}
}

// TestOnDoctorReportErrorNeverOpensACard — a refused GetDoctorReport must
// leave the user exactly where they were, with a status line, not a card
// showing nothing.
func TestOnDoctorReportErrorNeverOpensACard(t *testing.T) {
	r := newRoot(t)
	payload, _ := json.Marshal(map[string]any{"error": "boom"})
	cmd := r.onDoctorReport(protocol.Envelope{Type: protocol.EvError, Payload: payload})
	if cmd != nil {
		t.Error("an Error reply must not open a card")
	}
	if r.status == "" {
		t.Error("an Error reply should leave a status line explaining what happened")
	}
}
