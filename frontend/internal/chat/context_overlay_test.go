package chat

import (
	"fmt"
	"strings"
	"testing"
)

// evictionSection is the low-level capping logic (cards.go's
// capTerminalTail/taskCardLines precedent, applied to next_evictions): it
// must never drop an entry without saying so, and the count it reports must
// be exact.
func TestEvictionSectionReportsExactHiddenCount(t *testing.T) {
	items := make([]wireContextItem, 6)
	for i := range items {
		items[i] = wireContextItem{Kind: "message", Ref: fmt.Sprintf("orphan-%d", i+1), Reason: "oldest"}
	}

	// budget=4: header + 2 items + "+N more" — the exact shape found short
	// live during manual verification (6 real evictions, only 2 shown, and
	// nothing said about the other 4).
	out := evictionSection(testTheme(), items, 40, 4)
	if len(out) != 4 {
		t.Fatalf("evictionSection returned %d lines, want 4 (header + 2 items + more): %v", len(out), out)
	}
	if !strings.Contains(out[1], "orphan-1") || !strings.Contains(out[2], "orphan-2") {
		t.Errorf("first two items missing: %v", out)
	}
	if !strings.Contains(out[3], "+4 more") {
		t.Errorf("last line = %q, want \"+4 more\"", out[3])
	}

	// Every item fits: no "+N more" line at all.
	out = evictionSection(testTheme(), items, 40, 8)
	if strings.Contains(strings.Join(out, "\n"), "more") {
		t.Errorf("output contains a spurious more-count when everything fit: %v", out)
	}
	for i := 1; i <= 6; i++ {
		if !strings.Contains(strings.Join(out, "\n"), fmt.Sprintf("orphan-%d", i)) {
			t.Errorf("orphan-%d missing even though the budget fit all 6: %v", i, out)
		}
	}

	if got := evictionSection(testTheme(), nil, 40, 4); len(got) != 1 || !strings.Contains(got[0], "nothing") {
		t.Errorf("no evictions = %v, want a single \"nothing\" line", got)
	}
}

// TestContextOverlayCapsEvictionsWithAccurateCount is BUG 1 from manual
// verification, at the full View level rather than the helper in
// isolation: a live 6-orphan session showed only 2 evictions in the
// overlay, with no "+N more" anywhere, while `cli.py context report --json`
// on the same session at the same moment named all 6. Root cause was
// contextOverlayEvictionRows, a FIXED cap checked independently of the
// actual row budget — the real truncation happened afterward, silently, in
// View's own outer clip(out, h) call. This pins the fix at the render
// entry point a future refactor is most likely to touch.
func TestContextOverlayCapsEvictionsWithAccurateCount(t *testing.T) {
	items := make([]wireContextItem, 6)
	for i := range items {
		items[i] = wireContextItem{Kind: "message", Ref: fmt.Sprintf("orphan-%d", i+1), Reason: "oldest"}
	}
	w := &Workspace{
		theme: testTheme(),
		contextReport: &wireContextReport{
			BudgetTokens: 8000, UsedTokens: 100,
			NextEvictions: items,
		},
	}
	o := newContextOverlay(w)

	// h=13: six section rows + rule + free + blank take 9, leaving a 4-row
	// eviction budget — header + 2 items + "+4 more". This is the exact
	// "only fits 2" MinSize the field report described.
	out := o.View(testTheme(), 50, 13)

	for i := 1; i <= 2; i++ {
		if !strings.Contains(out, fmt.Sprintf("orphan-%d", i)) {
			t.Errorf("output missing orphan-%d, want the first 2 entries rendered", i)
		}
	}
	for i := 3; i <= 6; i++ {
		if strings.Contains(out, fmt.Sprintf("orphan-%d", i)) {
			t.Errorf("output contains orphan-%d, want only the first 2 shown at this height", i)
		}
	}
	if !strings.Contains(out, "+4 more") {
		t.Errorf("output does not name the 4 hidden entries:\n%s", out)
	}
}

// BUG 2 from manual verification: opening UI-09 with real data, then
// killing the backend, left the last real numbers frozen on screen forever
// — nothing checked w.backendDown at all. The fix must degrade every row to
// the same placeholder the other three cards already use, WITHOUT closing
// the overlay, and recover automatically once the backend comes back —
// exercised here against the SAME *contextOverlay value throughout, which
// is what proves no close/reopen cycle is needed.
func TestContextOverlayDegradesOnBackendDownAndRecovers(t *testing.T) {
	w := &Workspace{
		theme: testTheme(),
		contextReport: &wireContextReport{
			BudgetTokens: 32000, UsedTokens: 12400,
			Sections: map[string]int{"system": 400},
		},
	}
	o := newContextOverlay(w)

	// "400" is the system section's real token count -- the body has no
	// used/budget total of its own (that lives only in Title()), so the
	// section row is what proves the body is showing real data.
	live := o.View(testTheme(), 50, contextOverlayMinH-2)
	if !strings.Contains(live, "400") {
		t.Fatalf("live render = %q, want the real system-section token count", live)
	}
	if !strings.Contains(o.Title(), "12.4k") {
		t.Errorf("live Title = %q, want the real used/budget numbers", o.Title())
	}

	w.backendDown = true
	down := o.View(testTheme(), 50, contextOverlayMinH-2)
	if strings.Contains(down, "400") {
		t.Errorf("backend-down render still shows stale numbers: %q", down)
	}
	if !strings.Contains(down, "—") {
		t.Errorf("backend-down render = %q, want the dim placeholder every other card uses", down)
	}
	if o.Title() != "Context" {
		t.Errorf("backend-down Title = %q, want the bare title with no stale numbers", o.Title())
	}

	// The SAME overlay value renders again — nothing closed it, nothing
	// rebuilt it. Recovery is just View reading a flag that flipped back.
	w.backendDown = false
	recovered := o.View(testTheme(), 50, contextOverlayMinH-2)
	if !strings.Contains(recovered, "400") {
		t.Errorf("recovered render = %q, want real numbers back with no reopen", recovered)
	}
	if !strings.Contains(o.Title(), "12.4k") {
		t.Errorf("recovered Title = %q, want real numbers back with no reopen", o.Title())
	}
}
