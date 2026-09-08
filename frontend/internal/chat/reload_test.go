package chat

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Reload renders what the database holds, and nothing else
// ---------------------------------------------------------------------------

// The regression this locks in: F5 briefly folded adjacent identical user
// messages, to hide the duplicate question a retry leaves behind. Two
// SUCCESSFUL task-lane sends of the same question persist as Q · Q · A · A —
// both questions land before either answer — and the fold merged the two
// questions, orphaned one answer under the survivor, and labelled a send
// that was never retried as "retried". It corrupted a correct transcript.
//
// Adjacency does not identify a retry, and it never will. B12 has since
// supplied the one key that CAN pair a question to its answer -- task_id on
// both sides -- and S11b pairs on that and on nothing else. What this test
// now locks in is the other half of the same rule: rows carrying NO task_id
// (every row written before B12's migration) are still rendered exactly as
// the database returned them, in order, with no fold and no invented label.
func TestReloadRendersEveryRowVerbatim(t *testing.T) {
	msg := func(id, role, text string) wireMessage {
		return wireMessage{ID: id, Role: role, Content: wireContent{Text: text}}
	}

	t.Run("two successful sends of the same question keep both answers", func(t *testing.T) {
		w := newStreamWorkspace(t)
		w.setSession(wireSession{ID: "s1", Messages: []wireMessage{
			msg("u1", roleUser, "@research the same question"),
			msg("u2", roleUser, "@research the same question"),
			msg("a1", roleAssistant, "first answer"),
			msg("a2", roleAssistant, "second answer"),
		}})

		if got := w.entryCount(); got != 4 {
			t.Fatalf("reload shows %d entries, want all 4 rows — an answer was orphaned", got)
		}
		for i, want := range []string{roleUser, roleUser, roleAssistant, roleAssistant} {
			if got := w.entryRole(i); got != want {
				t.Fatalf("entry %d is %s, want %s", i, got, want)
			}
		}
		for i := 0; i < w.entryCount(); i++ {
			got := stripANSI(strings.Join(w.entryLines(i, 60, false), "\n"))
			if strings.Contains(got, "retried") {
				t.Fatalf("entry %d invents a retried label for a send that was never retried:\n%s", i, got)
			}
		}
	})

	t.Run("every row survives, in order", func(t *testing.T) {
		rows := []wireMessage{
			msg("u1", roleUser, "q"),
			msg("u2", roleUser, "q"),
			msg("u3", roleUser, "q"),
			msg("a1", roleAssistant, "a"),
		}
		w := newStreamWorkspace(t)
		w.setSession(wireSession{ID: "s1", Messages: rows})

		if w.entryCount() != len(rows) {
			t.Fatalf("reload shows %d entries for %d rows", w.entryCount(), len(rows))
		}
		for i, r := range rows {
			text, ok := w.entryText(i)
			if !ok || text != r.Content.Text {
				t.Fatalf("entry %d is %q, want the row's own %q", i, text, r.Content.Text)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// ISSUE 6 — does the reload path cost more per frame than the live one?
// ---------------------------------------------------------------------------

func reloadedSession(n int) []wireMessage {
	msgs := make([]wireMessage, 0, n)
	for i := 0; i < n; i++ {
		role, text := roleUser, fmt.Sprintf("question number %d, reasonably short", i)
		if i%2 == 1 {
			role = roleAssistant
			text = fmt.Sprintf("answer number %d. %s", i,
				strings.Repeat("some prose that wraps across several lines. ", 6))
		}
		msgs = append(msgs, wireMessage{
			ID: fmt.Sprintf("m%d", i), Role: role, Content: wireContent{Text: text},
		})
	}
	return msgs
}

// The reload path must reuse F3's render cache exactly as the live path
// does. If it did not, every frame would re-parse markdown for every visible
// message and scrolling a reloaded session would cost more than a fresh one.
func TestReloadedMessagesUseTheRenderCache(t *testing.T) {
	w := newStreamWorkspace(t)
	w.setSession(wireSession{ID: "s1", Messages: reloadedSession(40)})

	w.visibleWindow(20, 60)
	if len(w.cache) == 0 {
		t.Fatal("a reloaded session rendered nothing through the cache")
	}
	cached := len(w.cache)

	// Re-rendering the same window must not add cache entries — it must hit
	// the ones already there.
	for i := 0; i < 20; i++ {
		w.visibleWindow(20, 60)
	}
	if len(w.cache) != cached {
		t.Fatalf("re-rendering grew the cache %d -> %d: the reload path is not reusing it",
			cached, len(w.cache))
	}
}

// BenchmarkScrollReloaded measures a scroll step over a session loaded from
// the database; BenchmarkScrollLive measures the same over entries created
// this session. If the reload path defeated the cache or the row-based
// scroll, these two would diverge sharply.
func BenchmarkScrollReloaded(b *testing.B) {
	w := New(Options{Theme: testTheme()})
	w.sessionID = "s1"
	w.viewRows, w.viewWidth = 20, 60
	w.setSession(wireSession{ID: "s1", Messages: reloadedSession(400)})
	w.visibleWindow(20, 60)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			w.handleKeyString("k")
		} else {
			w.handleKeyString("j")
		}
		w.visibleWindow(20, 60)
	}
}

func BenchmarkScrollLive(b *testing.B) {
	w := New(Options{Theme: testTheme()})
	w.sessionID = "s1"
	w.viewRows, w.viewWidth = 20, 60
	for _, m := range reloadedSession(400) {
		if m.Role == roleUser {
			w.live = append(w.live, &liveEntry{
				kind: liveUserMsg, status: statusDone, userText: m.Content.Text,
			})
			continue
		}
		w.live = append(w.live, &liveEntry{
			kind: liveReply, status: statusDone, text: m.Content.Text,
		})
	}
	w.visibleWindow(20, 60)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			w.handleKeyString("k")
		} else {
			w.handleKeyString("j")
		}
		w.visibleWindow(20, 60)
	}
}
