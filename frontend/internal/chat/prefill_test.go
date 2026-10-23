package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F15's `send to chat`, from this side.

func TestPrefillDraftFillsAnEmptyInput(t *testing.T) {
	w := newTestWorkspace(t)
	w.PrefillDraft("About a.py — explain this\n\n> It returns early.\n")

	got := string(w.input.buf.runes)
	if !strings.Contains(got, "It returns early.") {
		t.Errorf("input = %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("input = %q, want the trailing newline trimmed", got)
	}
	if w.input.buf.cursor != len(w.input.buf.runes) {
		t.Errorf("cursor = %d, want the end (%d)",
			w.input.buf.cursor, len(w.input.buf.runes))
	}
}

// The half that matters: this is reached from a keypress in ANOTHER
// workspace, and `setText` clears the input's undo history -- so replacing
// a half-typed question would destroy it with no key to get it back.
func TestPrefillDraftDoesNotDestroyWhatWasTyped(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.buf.setText("why does this fail?", len("why does this fail?"))

	w.PrefillDraft("> It returns early.")

	got := string(w.input.buf.runes)
	if !strings.Contains(got, "why does this fail?") {
		t.Errorf("input = %q, want the typed question kept", got)
	}
	if !strings.Contains(got, "It returns early.") {
		t.Errorf("input = %q, want the exchange appended", got)
	}
	if !strings.Contains(got, "fail?\n\n>") {
		t.Errorf("input = %q, want a blank line between the two", got)
	}
}

func TestPrefillDraftIgnoresEmptyText(t *testing.T) {
	w := newTestWorkspace(t)
	w.input.buf.setText("kept", 4)
	w.PrefillDraft("\n\n")
	if got := string(w.input.buf.runes); got != "kept" {
		t.Errorf("input = %q", got)
	}
}

// F15's gate found these two together, and they are two bugs rather than
// one: Chat was ADOPTING the Editor's session (so its turns rendered as
// Chat's own transcript), and the prefill was being overwritten by the
// session load that followed it. Either one alone looks like "c sent a
// message instead of prefilling".

// A session another workspace owns is neither opened nor listed.
func TestChatNeverAdoptsAnotherWorkspacesSession(t *testing.T) {
	w := newTestWorkspace(t)

	payload := map[string]any{"sessions": []map[string]any{
		// Most recently updated FIRST, which is the order the backend
		// sends and the order the pick below trusts -- so the Editor's
		// own session is exactly what Chat used to grab.
		{"id": "editor-sess", "provider": "demo", "workspace": "editor", "updated_at": 200.0},
		{"id": "chat-sess", "provider": "demo", "workspace": "", "updated_at": 100.0},
	}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	w.onSessionList(protocol.Envelope{Type: protocol.EvSessionList, Payload: raw})

	for _, s := range w.sessions {
		if s.ID == "editor-sess" {
			t.Error("the Editor's session is listed as one of Chat's")
		}
	}
	if len(w.sessions) != 1 || w.sessions[0].ID != "chat-sess" {
		t.Fatalf("sessions = %+v, want only chat-sess", w.sessions)
	}
}

// A session with no workspace recorded is Chat's -- every session created
// before F15 has a NULL column, and a "only workspace == chat" rule would
// make all of them invisible.
func TestASessionWithNoWorkspaceIsStillChats(t *testing.T) {
	w := newTestWorkspace(t)

	raw, err := json.Marshal(map[string]any{"sessions": []map[string]any{
		{"id": "old-sess", "provider": "demo", "updated_at": 100.0},
	}})
	if err != nil {
		t.Fatal(err)
	}
	w.onSessionList(protocol.Envelope{Type: protocol.EvSessionList, Payload: raw})

	if len(w.sessions) != 1 || w.sessions[0].ID != "old-sess" {
		t.Fatalf("sessions = %+v, want the pre-F15 session kept", w.sessions)
	}
}

// The prefill survives the session load that Root's switch starts.
func TestAPrefillSurvivesTheSessionLoadThatFollowsIt(t *testing.T) {
	w := newTestWorkspace(t)

	// Root switches here and prefills SYNCHRONOUSLY, before any reply.
	w.PrefillDraft("About app.py - explain this\n\n> It returns early.")
	if got := string(w.input.buf.runes); !strings.Contains(got, "It returns early.") {
		t.Fatalf("input = %q", got)
	}

	// Now the bootstrap's GetSession reply lands, which restores a draft.
	w.setSession(wireSession{ID: "s1"})

	if got := string(w.input.buf.runes); !strings.Contains(got, "It returns early.") {
		t.Errorf("input = %q -- the session load overwrote the prefill, which is "+
			"indistinguishable from `c` having sent the message instead", got)
	}
	if w.pendingPrefill != "" {
		t.Error("the pending prefill was not cleared, so it will be re-applied again")
	}
}

// And the draft the session load restores is not destroyed by it either:
// both survive, draft first.
//
// NO sessionID before the prefill, deliberately -- that is the real
// sequence. With a session already open, PrefillDraft persists the input
// immediately (FlushDraft) and there is no pending prefill to re-apply,
// because nothing is going to load over the top of it.
func TestAStoredDraftAndAPrefillBothSurvive(t *testing.T) {
	w := newTestWorkspace(t)
	w.state.Drafts = map[string]config.Draft{"s1": {Text: "half a question", Cursor: 5}}

	w.PrefillDraft("> the answer")
	w.setSession(wireSession{ID: "s1"})

	got := string(w.input.buf.runes)
	if !strings.Contains(got, "half a question") {
		t.Errorf("input = %q, want the stored draft kept", got)
	}
	if !strings.Contains(got, "the answer") {
		t.Errorf("input = %q, want the prefill kept", got)
	}
}

// F15's gate read a Task card row as "filler-explaine done" and reported it
// as a mis-built agent name. It was an unmarked one-cell truncation at a
// narrow pane width. A cut that says it is a cut cannot be read that way.
func TestALongAgentNameIsTruncatedWithAMarker(t *testing.T) {
	th := testTheme()
	row := app.TaskRow{ID: "t1", Agent: "filler-explainer", State: app.TaskStateCompleted}

	// 22 cells is the width that produced the report: 4 for "done", 1 for
	// its gap, 2 for the glyph and its space, leaving 15 for a 16-cell
	// name.
	line := stripANSI(taskLine(th, row, 22))
	if strings.Contains(line, "filler-explaine ") {
		t.Errorf("line = %q, want the cut marked rather than silent", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("line = %q, want an ellipsis", line)
	}
	if !strings.Contains(line, "done") {
		t.Errorf("line = %q, want the state note kept", line)
	}
	if got := visibleWidth(line); got != 22 {
		t.Errorf("line is %d cells, want 22", got)
	}

	// And a width with room for the whole name shows the whole name.
	wide := stripANSI(taskLine(th, row, 40))
	if !strings.Contains(wide, "filler-explainer") {
		t.Errorf("line = %q, want the full name when it fits", wide)
	}
	if strings.Contains(wide, "…") {
		t.Errorf("line = %q, want no ellipsis when it fits", wide)
	}
}
