// Package protocol is the link to Python: the wire format, the child process,
// and the recovery ladder that makes the backend dying a normal event rather
// than a broken UI.
//
// It interprets exactly two things about a message — its type and its
// correlation fields. Payloads belong to whoever asked for them.
package protocol

import (
	"encoding/json"
	"fmt"
)

// Version is the envelope version this client speaks. It is the WIRE version,
// matching ENVELOPE_VERSION in backend\runtime\events.py — not the product
// version in the repo's VERSION file, which nothing on the wire reads.
//
// project_idea.md §4 rule 5: a mismatch refuses to proceed with both versions
// shown. That refusal is implemented against this constant.
const Version = 1

// Envelope is the one message shape, both directions.
//
//	{ "v": 1, "id": "...", "type": "TaskToken", "corr_id": "...",
//	  "session_id": "...", "task_id": "...", "connection_id": "...",
//	  "trace_id": "...", "seq": 1, "ts": 1712.34, "payload": {} }
//
// The nullable string fields are plain strings rather than pointers: Python
// writes JSON null for every one of them that is unset, and encoding/json
// decodes null into a string as a no-op, so "" and null are the same absence.
// Seq is a pointer because 0 is a value a sequence number could legitimately
// take, and "absent" has to stay distinguishable from "the first one".
type Envelope struct {
	V            int             `json:"v"`
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	CorrID       string          `json:"corr_id"`
	SessionID    string          `json:"session_id"`
	TaskID       string          `json:"task_id"`
	ConnectionID string          `json:"connection_id"`
	TraceID      string          `json:"trace_id"`
	Seq          *int            `json:"seq"`
	TS           float64         `json:"ts"`
	Payload      json.RawMessage `json:"payload"`
}

// Decode parses one line from the backend.
func Decode(line []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return Envelope{}, err
	}
	if e.Type == "" {
		return Envelope{}, fmt.Errorf("envelope has no type")
	}
	return e, nil
}

// Encode renders one line for the backend, newline included.
//
// The backend reads `id`, `type`, `payload` and the four metadata fields; it
// takes request_id from `id`, not from corr_id. Everything is written anyway,
// so a socket transport later sees the same bytes a pipe does.
func Encode(e Envelope) ([]byte, error) {
	if e.V == 0 {
		e.V = Version
	}
	if e.Payload == nil {
		e.Payload = json.RawMessage("{}")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// UnmarshalPayload decodes an envelope's payload into v. Consumers call this;
// the client never does.
func (e Envelope) UnmarshalPayload(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// SeqOr returns the sequence number, or the fallback when there is none.
func (e Envelope) SeqOr(fallback int) int {
	if e.Seq == nil {
		return fallback
	}
	return *e.Seq
}
