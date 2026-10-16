package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every outgoing event type the backend can produce must survive a round trip.
// The list is transcribed from backend\runtime\events.py; if the two drift,
// this is where it shows.
func TestEveryEventTypeRoundTrips(t *testing.T) {
	types := []string{
		EvHello, EvPing,
		EvTaskStarted, EvTaskToken, EvTaskDone, EvTaskFailed, EvTaskCancelled,
		EvTaskCancelAccepted, EvTaskList,
		EvError,
		EvSessionCreated, EvSession, EvSessionList, EvSessionRenamed,
		EvSessionArchived, EvSessionUnarchived, EvSessionDeleted,
		EvSessionMetadataUpdated, EvSessionSettingsUpdated,
		EvProviderList, EvProviderModelList, EvProviderAPIKeySet,
		EvSessionProviderUpdated,
		EvTextDelta, EvThinkingDelta, EvStatusChanged, EvProgressUpdated,
		EvUsageUpdated,
		EvToolStarted, EvToolCompleted,
		EvProject, EvProjectFileList, EvProjectFile,
		EvProjectFileWritten,
		EvFileLockResult, EvFileLockReleased, EvFileStaleResult,
		EvContextReport,
	}

	seq := 7
	for _, ty := range types {
		t.Run(ty, func(t *testing.T) {
			in := Envelope{
				V: Version, ID: "abc", Type: ty, CorrID: "req-1",
				SessionID: "sess-1", TaskID: "task-1",
				ConnectionID: "conn-1", TraceID: "trace-1",
				Seq: &seq, TS: 1712345678.25,
				Payload: json.RawMessage(`{"text":"hello"}`),
			}

			raw, err := Encode(in)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(raw), "\n") {
				t.Fatal("encoded line is not newline-terminated")
			}

			out, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if out.Type != in.Type || out.ID != in.ID || out.CorrID != in.CorrID ||
				out.SessionID != in.SessionID || out.TaskID != in.TaskID ||
				out.ConnectionID != in.ConnectionID || out.TraceID != in.TraceID ||
				out.TS != in.TS {
				t.Fatalf("round trip differs:\n in %+v\nout %+v", in, out)
			}
			if out.SeqOr(-1) != seq {
				t.Errorf("seq = %d, want %d", out.SeqOr(-1), seq)
			}
			var p struct {
				Text string `json:"text"`
			}
			if err := out.UnmarshalPayload(&p); err != nil || p.Text != "hello" {
				t.Errorf("payload = %+v, err %v", p, err)
			}
		})
	}
}

// Python writes JSON null for every unset metadata field. Decoding a null into
// a string must be an absence, not an error.
func TestNullMetadataDecodesAsAbsent(t *testing.T) {
	line := `{"v":1,"id":"e1","type":"ping","corr_id":null,"session_id":null,` +
		`"task_id":null,"connection_id":null,"trace_id":null,"seq":null,` +
		`"ts":1712345678.25,"payload":{"active_tasks":0}}`

	e, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if e.CorrID != "" || e.SessionID != "" || e.TaskID != "" ||
		e.ConnectionID != "" || e.TraceID != "" {
		t.Fatalf("nulls did not decode as absent: %+v", e)
	}
	if e.Seq != nil {
		t.Errorf("seq should be nil, got %d", *e.Seq)
	}
	if e.SeqOr(-1) != -1 {
		t.Errorf("SeqOr fallback ignored")
	}
}

// seq 0 and no seq at all are different things.
func TestSeqZeroIsNotAbsent(t *testing.T) {
	e, err := Decode([]byte(`{"v":1,"id":"x","type":"TaskToken","seq":0,"payload":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Seq == nil {
		t.Fatal("seq 0 decoded as absent")
	}
	if *e.Seq != 0 {
		t.Fatalf("seq = %d", *e.Seq)
	}
}

// An unknown type is tolerated: logged and ignored by the consumer, never a
// decode failure. A backend that grows a new event must not crash an older UI.
func TestUnknownTypeDecodes(t *testing.T) {
	e, err := Decode([]byte(`{"v":1,"id":"x","type":"SomethingFromNextYear","payload":{"a":1}}`))
	if err != nil {
		t.Fatalf("unknown type rejected: %v", err)
	}
	if e.Type != "SomethingFromNextYear" {
		t.Fatalf("type = %q", e.Type)
	}
	if IsTerminal(e.Type) || IsDroppable(e.Type) || IsConnectionLevel(e.Type) {
		t.Error("an unknown type must not be classified as anything")
	}
}

func TestDecodeRejectsNonEnvelopes(t *testing.T) {
	for _, line := range []string{
		`hi`,
		`{"no":"type"}`,
		`[]`,
		``,
	} {
		if _, err := Decode([]byte(line)); err == nil {
			t.Errorf("accepted %q as an envelope", line)
		}
	}
}

func TestEncodeFillsVersionAndPayload(t *testing.T) {
	raw, err := Encode(Envelope{ID: "x", Type: CmdListSessions})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["v"] != float64(Version) {
		t.Errorf("v = %v, want %d", m["v"], Version)
	}
	if _, ok := m["payload"].(map[string]any); !ok {
		t.Errorf("payload = %v, want an empty object", m["payload"])
	}
	// The backend takes request_id from `id`, so it must always be present.
	if m["id"] != "x" {
		t.Errorf("id = %v", m["id"])
	}
}

// Exactly one terminal event per task, and the cancel ACK is not one of them —
// before B6 it was, which made "exactly one" false.
func TestTerminalClassification(t *testing.T) {
	terminal := []string{EvTaskDone, EvTaskFailed, EvTaskCancelled}
	notTerminal := []string{
		EvTaskStarted, EvTaskToken, EvTaskCancelAccepted, EvTextDelta,
		EvProgressUpdated, EvToolCompleted, EvError, EvPing, EvHello,
	}
	for _, ty := range terminal {
		if !IsTerminal(ty) {
			t.Errorf("%s should be terminal", ty)
		}
	}
	for _, ty := range notTerminal {
		if IsTerminal(ty) {
			t.Errorf("%s should not be terminal", ty)
		}
	}
}

// Only token streams may be dropped. Anything else costs a permanent spinner.
func TestDroppableClassification(t *testing.T) {
	for _, ty := range []string{EvTaskToken, EvTextDelta, EvThinkingDelta} {
		if !IsDroppable(ty) {
			t.Errorf("%s should be droppable", ty)
		}
	}
	for _, ty := range []string{
		EvTaskStarted, EvTaskDone, EvTaskFailed, EvTaskCancelled,
		EvTaskCancelAccepted, EvError, EvToolStarted, EvToolCompleted,
		EvUsageUpdated, EvProgressUpdated, EvStatusChanged, EvHello, EvPing,
	} {
		if IsDroppable(ty) {
			t.Errorf("%s must never be dropped", ty)
		}
	}
}

// No terminal event may also be droppable. This is the invariant, stated once.
func TestNoTerminalEventIsDroppable(t *testing.T) {
	for _, ty := range []string{EvTaskDone, EvTaskFailed, EvTaskCancelled} {
		if IsTerminal(ty) && IsDroppable(ty) {
			t.Fatalf("%s is both terminal and droppable", ty)
		}
	}
}

func TestConnectionLevelEvents(t *testing.T) {
	for _, ty := range []string{EvHello, EvPing} {
		if !IsConnectionLevel(ty) {
			t.Errorf("%s is caused by no request", ty)
		}
	}
	if IsConnectionLevel(EvTaskToken) {
		t.Error("TaskToken is not connection-level")
	}
}

// The real hello payload from backend\runtime\runtime.py emit_hello.
func TestHelloPayloadShape(t *testing.T) {
	line := `{"v":1,"id":"e1","type":"hello","corr_id":null,"session_id":null,` +
		`"task_id":null,"connection_id":null,"trace_id":null,"seq":null,"ts":1.0,` +
		`"payload":{"v":1,"capabilities":{"providers":["ollama","mock"],` +
		`"schema_version":7,"streaming":true,"cancel":true}}}`

	e, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	var hello Hello
	if err := e.UnmarshalPayload(&hello); err != nil {
		t.Fatal(err)
	}
	if hello.V != Version {
		t.Errorf("v = %d, want %d", hello.V, Version)
	}
	if len(hello.Capabilities.Providers) != 2 || hello.Capabilities.SchemaVersion != 7 {
		t.Errorf("capabilities = %+v", hello.Capabilities)
	}
	if !hello.Capabilities.Streaming || !hello.Capabilities.Cancel {
		t.Errorf("capabilities = %+v", hello.Capabilities)
	}
}
