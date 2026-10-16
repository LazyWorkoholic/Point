package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The scroll-offset bound, and the one-key close.

// ---------------------------------------------------------------------------
// 4 -- the off-by-one that was written three times
// ---------------------------------------------------------------------------

// A scroll offset is bounded by count-rows, never count-1.
//
// With count-1, scrolling to the end of any list leaves ONE item
// visible however tall the viewport is. Measured before the fix: a
// ten-line file in a twenty-row viewport -- which should not scroll at
// all -- came to rest showing one line, and one wheel notch back showed
// four, which is the reported "single-frame flash of the last 4 items".
func TestClampTopBoundsTheOffsetByTheViewportNotByOne(t *testing.T) {
	cases := []struct {
		name             string
		top, count, rows int
		want             int
	}{
		{"content shorter than the viewport never scrolls", 1000, 10, 20, 0},
		{"content exactly the viewport never scrolls", 1000, 20, 20, 0},
		{"one line more than the viewport scrolls by one", 1000, 21, 20, 1},
		{"scrolling past the end stops a full screen from the bottom", 1000, 100, 20, 80},
		{"negative is pulled back to the top", -5, 100, 20, 0},
		{"a zero-row viewport is treated as one row", 1000, 10, 0, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampTop(c.top, c.count, c.rows); got != c.want {
				t.Errorf("clampTop(%d, %d, %d) = %d, want %d",
					c.top, c.count, c.rows, got, c.want)
			}
		})
	}
}

// The property, stated once: after ANY scroll, a full viewport of
// content is showing unless there is not enough content to fill one.
func TestScrollingNeverLeavesLessThanAFullViewportShowing(t *testing.T) {
	body := strings.TrimSuffix(strings.Repeat("line\n", 60), "\n")
	ws := testWorkspace(t, body, 120, 30)
	d := ws.docs[0]
	rows := ws.textArea.H
	if rows < 2 || d.buf.Lines() <= rows {
		t.Fatalf("precondition: %d lines in a %d-row viewport", d.buf.Lines(), rows)
	}

	for _, by := range []int{+1, +3, +1000, -3, +1000, +1000} {
		d.scrollBy(by, rows)
		visible := d.buf.Lines() - d.top
		if visible < rows {
			t.Fatalf("after scrollBy(%d): top=%d leaves %d lines visible in a "+
				"%d-row viewport", by, d.top, visible, rows)
		}
	}
}

// The reported shape exactly: a file that FITS the viewport must not
// scroll at all, so the one-item end state is unreachable.
func TestAFileThatFitsTheViewportDoesNotScroll(t *testing.T) {
	ws := testWorkspace(t, "l1\nl2\nl3\nl4\nl5", 120, 30)
	d := ws.docs[0]
	if d.buf.Lines() >= ws.textArea.H {
		t.Skip("the viewport is smaller than the fixture here")
	}
	d.scrollBy(1000, ws.textArea.H)
	if d.top != 0 {
		t.Errorf("top = %d after scrolling a file that fits; want 0 — "+
			"this is the state that showed one line and flashed four", d.top)
	}
}

// ...and the same for the tree, which had the same expression.
func TestTheTreeDoesNotScrollPastAFullViewport(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.tree.build([]string{"a.py", "b.py", "c.py", "d.py", "e.py", "f.py"})
	tr := ws.tree

	tr.top = 1000
	tr.ensureVisible(4)
	if visible := len(tr.rows) - tr.top; visible != 4 {
		t.Errorf("%d rows visible in a 4-row viewport (top=%d), want 4",
			visible, tr.top)
	}

	// A tree shorter than its viewport does not scroll.
	tr.top = 1000
	tr.ensureVisible(20)
	if tr.top != 0 {
		t.Errorf("top = %d for a tree that fits, want 0", tr.top)
	}
}

// ---------------------------------------------------------------------------
// 3 -- the one-key close
// ---------------------------------------------------------------------------

// f7 closes the active tab through the SAME path the tab strip's x
// uses, so a dirty file still routes through C3's confirm.
func TestF7ClosesACleanTabAndConfirmsADirtyOne(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"), testDoc(t, "b"))
	ws.active = 1

	// Clean: closes outright.
	cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyF7})
	if len(ws.docs) != 1 {
		t.Fatalf("%d documents after f7 on a clean tab, want 1", len(ws.docs))
	}
	if said := saidBy(cmd); !strings.Contains(said, "closed") {
		t.Errorf("said %q", said)
	}

	// Dirty: confirms, and closes nothing yet (Done-when 9 / C3).
	ws.docs[0].insertRune('x')
	cmd = ws.handleKey(tea.KeyMsg{Type: tea.KeyF7})
	if len(ws.docs) != 1 {
		t.Fatal("f7 discarded a dirty document without confirming")
	}
	if !openedConfirm(t, cmd) {
		t.Error("f7 on a dirty tab did not open the confirm")
	}
}

// With nothing open it is a no-op rather than a panic.
func TestF7WithNothingOpenDoesNothing(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	if cmd := ws.handleKey(tea.KeyMsg{Type: tea.KeyF7}); cmd != nil {
		t.Error("f7 produced a command with nothing open")
	}
}

// f7 is reachable: not a chord the registry claims first, and not
// claimed by CapturesKey.
func TestF7IsReachableAndNotAChord(t *testing.T) {
	chords := []string{"ctrl+c", "ctrl+e", "ctrl+l", "ctrl+p", "ctrl+q",
		"ctrl+r", "esc", "f1", "tab"}
	key := tea.KeyMsg{Type: tea.KeyF7}.String()
	for _, c := range chords {
		if key == c {
			t.Fatalf("close-tab is bound to %q, which dispatchKey swallows first", key)
		}
	}
	ws, _ := newWorkspaceWithRecorder()
	if ws.CapturesKey(key) {
		t.Errorf("%q is claimed by CapturesKey and does not need to be", key)
	}
}

// The per-frame clamp in ensureVisible matters on the paths that set
// `top` WITHOUT going through scrollBy -- and there are two.
//
//  1. A RESIZE. The viewport grows, so a `top` that was legal against
//     the old height now leaves a part-empty window. Nothing calls
//     scrollBy on a resize; ensureVisible runs every frame and is the
//     only thing that can pull it back.
//  2. RESUME. applyResumeTo assigns d.top straight from the suspend
//     blob, which was written against whatever height that session had
//     -- or by an older build with the old bound.
//
// Reverting the clamp leaves both showing a short window with blank
// rows under the text, which is the glitch's other face.
func TestTheFrameClampFixesAnOffsetSetWithoutScrolling(t *testing.T) {
	body := strings.TrimSuffix(strings.Repeat("line\n", 60), "\n")
	d := testDoc(t, body)

	// 1 -- legal at 10 rows, then the pane grows to 40.
	d.top = clampTop(50, d.buf.Lines(), 10)
	if d.top != 50 {
		t.Fatalf("precondition: top = %d at 10 rows, want 50", d.top)
	}
	// reveal is FALSE: a resize is not a request to show the caret, so
	// ensureVisible returns early at its reveal gate and the PRE-clamp
	// is the only thing that can run. That is the whole point of having
	// it before the gate rather than only after.
	d.reveal = false
	d.ensureVisible(40, 80)
	if visible := d.buf.Lines() - d.top; visible < 40 {
		t.Errorf("after growing the viewport to 40 rows, top=%d leaves %d lines "+
			"visible — the frame clamp did not pull it back", d.top, visible)
	}

	// 2 -- a suspend blob restoring a top past the end.
	d.top = 9999
	d.reveal = false
	d.ensureVisible(20, 80)
	if visible := d.buf.Lines() - d.top; visible != 20 {
		t.Errorf("a restored out-of-range top left %d lines visible in a "+
			"20-row viewport, want 20 (top=%d)", visible, d.top)
	}
}
