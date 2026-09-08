package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// The payload below is not hand-written. It is the literal output of
//
//	uv run python -c "... UserMessage(...).to_dict(), AssistantMessage(...).to_dict()"
//
// run against backend\managers\message.py, with only the ids shortened. It
// exists because every other test in this package builds a wireMessage in
// Go, and a field name that Go and Python disagree about would pass all of
// them and fail against the real backend — which is exactly how F5 ended up
// shipping a reload path that could not see `invocation` at all.
//
// Note the shapes that are easy to get wrong and are asserted here:
// `invocation` is null on a user row and an object on an assistant row;
// `capability` inside it is null; `outcome`/`error_code` are null on the
// user row; and the error prose lives in `metadata.error`, not in `content`.
const wireGetSessionPayload = `{"session": {"id": "s1", "title": "t", "messages": [
 {"id": "u1", "session_id": "s1", "task_id": "t1", "role": "user",
  "content": {"type": "text", "text": "a question"},
  "timestamp": 1788794451.872091, "metadata": {}, "invocation": null,
  "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
  "outcome": null, "interrupted": false, "error_code": null},
 {"id": "a1", "session_id": "s1", "task_id": "t1", "role": "assistant",
  "content": {"type": "text", "text": "partial"},
  "timestamp": 1788794491.872091,
  "metadata": {"reason": "failed", "error": "ollama: connection refused"},
  "invocation": {"workspace": "chat", "capability": null, "agent": "research",
                 "provider": "ollama", "model": "m"},
  "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
  "outcome": "bounds_exceeded", "interrupted": true,
  "error_code": "bounds_exceeded"}]}}`

func TestABackendPayloadRebuildsTheSlotAndFooter(t *testing.T) {
	var p struct {
		Session wireSession `json:"session"`
	}
	if err := json.Unmarshal([]byte(wireGetSessionPayload), &p); err != nil {
		t.Fatalf("the backend's own GetSession shape does not unmarshal: %v", err)
	}

	got := p.Session.Messages[1]
	if got.TaskID != "t1" {
		t.Fatalf("task_id did not decode: %q", got.TaskID)
	}
	if got.Invocation == nil || got.Invocation.Agent != "research" {
		t.Fatalf("invocation.agent did not decode: %+v", got.Invocation)
	}
	if got.Outcome != outcomeBoundsExceeded || got.ErrorCode != boundsExceededCode {
		t.Fatalf("outcome/error_code did not decode: %q / %q", got.Outcome, got.ErrorCode)
	}
	if !got.Interrupted {
		t.Fatal("interrupted did not decode")
	}
	// A user row's null invocation must be nil, not a zero struct that
	// reloadLane would then ask about.
	if p.Session.Messages[0].Invocation != nil {
		t.Fatal("a null invocation decoded as a value")
	}

	w := newStreamWorkspace(t)
	w.setSession(p.Session)
	w.viewRows, w.viewWidth = 20, 100

	if seq := roleSeq(w); seq != "QA" {
		t.Fatalf("the payload reloaded as %q, want QA", seq)
	}
	out := rendered(w, 1)
	if !strings.Contains(out, "@research") {
		t.Fatalf("a research answer did not reload as a slot:\n%s", out)
	}
	if !strings.Contains(out, "⚠ stopped at its limit") {
		t.Fatalf("bounds_exceeded lost its footer:\n%s", out)
	}
	if !strings.Contains(out, "ollama: connection refused") {
		t.Fatalf("the footer lost metadata.error:\n%s", out)
	}
	// Collapsed: the partial text is behind `o`, exactly as live.
	if strings.Contains(out, "partial") {
		t.Fatalf("the slot is not collapsed:\n%s", out)
	}
	w.focus = 1
	w.handleKeyString("o")
	if opened := rendered(w, 1); !strings.Contains(opened, "partial") {
		t.Fatalf("o did not reveal the partial text:\n%s", opened)
	}
	// A FAILED slot shows no clock, exactly as the live one does not:
	// renderTaskEntry puts the elapsed time on `✓ done` alone. The paired
	// timestamps do still feed it — see the done-slot case in
	// reload_pairing_test.go.
	if strings.Contains(out, "0:40") {
		t.Fatalf("a failed slot grew a clock the live one does not show:\n%s", out)
	}
}
