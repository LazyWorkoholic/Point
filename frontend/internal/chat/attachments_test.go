package chat

import (
	"encoding/json"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

func TestIsDetachCommand(t *testing.T) {
	cases := map[string]bool{
		"/detach":             true,
		"  /detach  ":         true,
		"/detach 2":           false, // no indexed form — one rule, the LAST chip
		"/detachfoo":          false,
		"detach":              false,
		"":                    false,
		"please /detach this": false,
	}
	for text, want := range cases {
		if got := isDetachCommand(text); got != want {
			t.Errorf("isDetachCommand(%q) = %v, want %v", text, got, want)
		}
	}
}

// detachChip must remove the chip locally AND tell the backend — F4's
// original backspace-removal only ever did the first half, which looked
// like a detach while leaving the row bound server-side. This is the
// regression test for that fix.
func TestDetachChipRemovesLocallyAndSendsDetachContext(t *testing.T) {
	w := newTestWorkspace(t)
	w.sessionID = "s1"
	var gotType string
	var gotPayload map[string]any
	w.sendFn = func(cmdType string, kind queryKind, payload map[string]any) {
		gotType = cmdType
		gotPayload = payload
		w.pending[localID()] = kind
	}
	w.input.chips = []AttachmentChip{
		{ID: "a1", Name: "one.py"},
		{ID: "a2", Name: "two.py"},
	}

	w.detachChip(1)

	if len(w.input.chips) != 1 || w.input.chips[0].ID != "a1" {
		t.Fatalf("chips = %+v, want only a1 left", w.input.chips)
	}
	if gotType != protocol.CmdDetachContext {
		t.Fatalf("sent %q, want %q", gotType, protocol.CmdDetachContext)
	}
	if gotPayload["session"] != "s1" || gotPayload["attachment"] != "a2" {
		t.Errorf("payload = %+v, want session=s1 attachment=a2", gotPayload)
	}
}

// A chip with no backend ID (should not happen in product code, but this is
// the boundary check) is removed locally with no wire call — there is
// nothing server-side to address.
func TestDetachChipWithNoIDSkipsTheWireCall(t *testing.T) {
	w := newTestWorkspace(t)
	sent := false
	w.sendFn = func(string, queryKind, map[string]any) { sent = true }
	w.input.chips = []AttachmentChip{{Name: "one.py"}}

	w.detachChip(0)

	if len(w.input.chips) != 0 {
		t.Fatal("chip must still be removed locally")
	}
	if sent {
		t.Error("no DetachContext should be sent for a chip with no backend id")
	}
}

func TestDetachChipOutOfRangeIsANoOp(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{{ID: "a1"}}
	w.detachChip(5)
	if len(w.input.chips) != 1 {
		t.Fatal("an out-of-range index must not touch the chip list")
	}
}

// refreshAttachmentFreshness's own reply (ListAttachments): each row's
// RECORDED pair becomes the chip's new baseline, matched by exact
// attachment id — never by parsing ContextReport's `detail` string, which
// this episode tried first and reverted (see attachments.go's own doc: the
// old design never updated the baseline either, so the NEXT periodic sweep
// undid whatever it had just corrected).
func TestOnAttachmentListUpdatesBaselineAndCurrent(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{
		{ID: "a1", Name: "task.py", Current: false}, // was never-sent
		{ID: "a2", Name: "other.py", Current: true}, // not in this reply at all
	}
	payload, _ := json.Marshal(map[string]any{
		"attachments": []map[string]any{
			{"id": "a1", "recorded_mtime": 1000, "recorded_size": 2000},
		},
	})

	w.onAttachmentList(protocol.Envelope{Type: protocol.EvAttachmentList, Payload: payload})

	if !w.input.chips[0].Current {
		t.Error("chip a1 must flip to current once the backend reports a recorded pair for it")
	}
	if w.input.chips[0].RecordedMtime == nil || *w.input.chips[0].RecordedMtime != 1000 {
		t.Errorf("chip a1 RecordedMtime = %v, want 1000", w.input.chips[0].RecordedMtime)
	}
	if !w.input.chips[1].Current {
		t.Error("chip a2, absent from this reply, must be left exactly as it was")
	}
}

// A row whose recorded pair is still NULL (never sent) must not be
// mistaken for current — Bug D's exact mechanism, at the ListAttachments
// boundary this time rather than the periodic sweep's.
func TestOnAttachmentListLeavesNeverSentChipNotCurrent(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{{ID: "a1", Current: true}} // wrongly marked, to prove this corrects it
	payload, _ := json.Marshal(map[string]any{
		"attachments": []map[string]any{
			{"id": "a1", "recorded_mtime": nil, "recorded_size": nil},
		},
	})

	w.onAttachmentList(protocol.Envelope{Type: protocol.EvAttachmentList, Payload: payload})

	if w.input.chips[0].Current {
		t.Error("a NULL recorded pair means never sent, which must read as NOT current")
	}
}

func intp(v int) *int { return &v }

// The periodic staleness sweep's own reply: a live (mtime, size) that
// matches the chip's recorded pair is current; anything else is changed.
func TestHandleResolveAttachmentEnvelopeUpdatesCurrent(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{
		{Path: `D:\p\task.py`, RecordedMtime: intp(100), RecordedSize: intp(50), Current: true},
	}
	w.resolvingPath["req-1"] = `D:\p\task.py`

	payload, _ := json.Marshal(map[string]any{"mtime": 999, "size": 50})
	taken := w.handleResolveAttachmentEnvelope(protocol.Envelope{
		Type: protocol.EvAttachmentResolution, CorrID: "req-1", Payload: payload,
	})

	if !taken {
		t.Fatal("the reply must be claimed by its own correlation id")
	}
	if w.input.chips[0].Current {
		t.Error("mtime changed (100 -> 999): chip must flip to NOT current")
	}
	if _, still := w.resolvingPath["req-1"]; still {
		t.Error("the correlation id must be cleared once its reply is handled")
	}
}

// Bug D's exact reported symptom, reproduced at the layer it actually
// happens: a file with a REAL post-send baseline (not the bogus zero a
// plain `int` decode of a NULL recorded_mtime used to silently produce),
// left completely untouched, must still read as current after a full
// sweep interval — not flip to "changed" on a file nobody modified.
func TestHandleResolveAttachmentEnvelopeUntouchedFileStaysCurrent(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{
		{Path: `D:\p\task.py`, RecordedMtime: intp(1700000000), RecordedSize: intp(4096), Current: true},
	}
	w.resolvingPath["req-1"] = `D:\p\task.py`

	// The live stat ResolveAttachment reports is IDENTICAL to the recorded
	// pair — the file was never touched, so this is exactly what a real
	// unmodified file reports on every sweep.
	payload, _ := json.Marshal(map[string]any{"mtime": 1700000000, "size": 4096})
	w.handleResolveAttachmentEnvelope(protocol.Envelope{
		Type: protocol.EvAttachmentResolution, CorrID: "req-1", Payload: payload,
	})

	if !w.input.chips[0].Current {
		t.Error("an untouched file with a matching recorded baseline must stay current — Bug D")
	}
}

// Bug D, at the periodic sweep's own boundary: a chip that has never been
// sent (RecordedMtime/Size both nil) has no baseline to compare a live stat
// against, and that must read as NOT current — not as "leave it alone" or,
// worse, a false "current" from comparing against a zero value nobody set.
func TestHandleResolveAttachmentEnvelopeNeverSentIsNotCurrent(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.chips = []AttachmentChip{
		{Path: `D:\p\fresh.py`, RecordedMtime: nil, RecordedSize: nil, Current: true},
	}
	w.resolvingPath["req-1"] = `D:\p\fresh.py`

	payload, _ := json.Marshal(map[string]any{"mtime": 12345, "size": 678})
	w.handleResolveAttachmentEnvelope(protocol.Envelope{
		Type: protocol.EvAttachmentResolution, CorrID: "req-1", Payload: payload,
	})

	if w.input.chips[0].Current {
		t.Error("a never-sent chip (nil baseline) must not read as current")
	}
}

// A reply with an unrelated corr_id is not this mechanism's — it must be
// left for the ordinary pending-map path (or whatever else recognises it),
// never silently consumed here.
func TestHandleResolveAttachmentEnvelopeIgnoresUnrelatedReplies(t *testing.T) {
	w := newTestWorkspace(t)
	taken := w.handleResolveAttachmentEnvelope(protocol.Envelope{
		Type: protocol.EvAttachmentResolution, CorrID: "not-mine",
	})
	if taken {
		t.Error("an envelope this workspace never asked for must not be claimed")
	}
}

// The chip tick costs nothing while there is nothing attached — the same
// idle-CPU guarantee stream.go's armTick already established for the
// watchdog.
func TestArmChipTickIdleWithNoChips(t *testing.T) {
	w := newTestWorkspace(t)
	w.client = nil
	if cmd := w.armChipTick(); cmd != nil {
		t.Error("armChipTick must not schedule anything with zero chips attached")
	}
}
