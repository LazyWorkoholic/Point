package editor

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The unified row sequence: ONE scroll position, ONE row index.
//
// These are the proof for the rewrite viewrows.go describes. The old model
// gave the ghost its own scroll offset nested at the insertion line, and
// every symptom F15's gate reported was a consequence of having two scroll
// states:
//
//	1. the insertion line pinned on screen with text scrolling inside it
//	2. real code AFTER the insertion point unreachable while a ghost showed
//	3. arrow keys snapping the viewport once the region got taller than the pane
//
// Each has a test below that FAILS under the old model and passes under this
// one. The reachability test that already existed (ghostrender_test.go's
// TestTheWholeOfALongGhostIsReachableByScrolling) still holds -- it just no
// longer needs a second coordinate to hold it up.

// visibleLines is the set of real buffer line numbers (1-based, as the
// gutter prints them) on screen, plus how many ghost rows are showing.
func visibleLines(ws *Workspace) (lines map[int]bool, ghostRows int) {
	lines = map[int]bool{}
	const firstCodeRowInBlock = 2 // the box border, then the tab strip
	block := strings.Split(ws.View(ws.lastLayout), "\n")
	for i := firstCodeRowInBlock; i < len(block); i++ {
		if i-firstCodeRowInBlock >= ws.textArea.H {
			break
		}
		n, ghost, ok := gutterNumber(block[i])
		switch {
		case !ok:
			continue
		case ghost:
			ghostRows++
		default:
			lines[n] = true
		}
	}
	return lines, ghostRows
}

func wheelDown(ws *Workspace, n int) {
	for i := 0; i < n; i++ {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
	}
}

// ---------------------------------------------------------------------------
// Symptom 2: the code after the insertion point
// ---------------------------------------------------------------------------

// Scrolling down through a long mid-file ghost carries straight on into the
// real lines AFTER the insertion point, in the SAME scroll sequence.
//
// This is the test the old model could not pass at any scroll position. Its
// renderer only emitted ghost rows while it was drawing the anchor line, so
// the file could not scroll past the anchor without the region vanishing --
// and a scroll down that reached the ghost's own last row re-armed the
// follow, which pulled the viewport back to the anchor on the next frame.
// The rest of the file was behind a wall.
func TestScrollingDownPastAGhostReachesTheCodeAfterIt(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	const anchor = 5 // 0-based; line 6 in the gutter
	d.cursor = Pos{Line: anchor}
	ws.ghost = &ghostText{anchorLine: anchor, lines: longGhost(40)}
	ws.ghostFollow = true
	ws.View(l)

	// Everything seen across the whole downward sweep, and the positions
	// the single scroll coordinate took.
	seen := map[int]bool{}
	var tops []int
	for i := 0; i < 25; i++ {
		lines, _ := visibleLines(ws)
		for n := range lines {
			seen[n] = true
		}
		tops = append(tops, d.top)
		wheelDown(ws, 1)
	}

	// Lines after the insertion point, which is what was unreachable.
	var missing []string
	for n := anchor + 2; n <= 40; n++ { // gutter numbers, so anchor+2 is the first line below
		if !seen[n] {
			missing = append(missing, strconv.Itoa(n))
		}
	}
	if len(missing) > 0 {
		t.Errorf("scrolling down never reached these lines AFTER the insertion "+
			"point: %s -- the scroll is stuck inside the ghost's own region",
			strings.Join(missing, ","))
	}

	// And it got there by moving ONE coordinate, monotonically. A second
	// scroll state would show up here as the top standing still while the
	// frame changed.
	for i := 1; i < len(tops); i++ {
		if tops[i] < tops[i-1] {
			t.Fatalf("the scroll position went backwards at notch %d: %v", i, tops)
		}
	}
	if tops[len(tops)-1] <= tops[0] {
		t.Fatalf("the scroll position never advanced: %v", tops)
	}

	// The end state is the plain file: the region is above the pane and
	// the follow is off, so nothing drags the view back to it.
	lines, ghostRows := visibleLines(ws)
	if ghostRows != 0 {
		t.Errorf("after scrolling well past it, %d ghost rows are still on "+
			"screen", ghostRows)
	}
	if len(lines) == 0 {
		t.Error("no real lines on screen at the end of the sweep")
	}
	if ws.ghostFollow {
		t.Error("the follow re-armed itself and will yank the viewport back")
	}
}

// Symptom 1: the insertion line is an ordinary line and scrolls away like
// one. Under the old model it was pinned on screen for as long as the
// region was taller than the pane, because followGhost set the file's top
// TO the anchor and moved the ghost's own offset instead.
//
// The follow is OFF here, which is the state the symptom was reported in
// (the user had scrolled). With it ON and a region taller than the pane the
// anchor is ALREADY above the top -- the pane is filled by the region's
// tail, which is what "the live edge is at the bottom" means and is correct;
// the first version of this test asserted the opposite and failed on its own
// premise.
func TestTheInsertionLineIsNotPinnedOnScreen(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	const anchor = 5
	d.cursor = Pos{Line: anchor}
	ws.ghost = &ghostText{anchorLine: anchor, lines: longGhost(40)}
	ws.ghostFollow = false
	ws.View(l)

	if lines, _ := visibleLines(ws); !lines[anchor+1] {
		t.Fatalf("the anchor line %d is not on screen to begin with", anchor+1)
	}
	wheelDown(ws, 4)
	if lines, _ := visibleLines(ws); lines[anchor+1] {
		t.Errorf("line %d is STILL on screen after scrolling down 4 notches -- "+
			"the insertion line is pinned and the ghost is scrolling inside it",
			anchor+1)
	}
}

// ---------------------------------------------------------------------------
// Symptom 3: arrow keys
// ---------------------------------------------------------------------------

// Arrowing through a region taller than the pane moves the viewport
// MONOTONICALLY -- down while going down, up while going up -- and keeps
// the caret on screen at every step.
//
// That is the honest statement of the reported glitch. The old model handed
// ensureVisible `textRows - injectedRows`, where the injected count was
// itself `total - ghost.top` -- a number that changed as the ghost's own
// offset moved -- so the reveal's row budget wobbled frame to frame and the
// view jumped in both directions. One sequence and one reveal means a
// keystroke moves the view by what the caret needed and nothing else.
//
// What this test is verified to CATCH, by breaking it and watching it fail:
// a reveal that works in buffer lines instead of view rows (the plausible
// future mistake) puts the caret off the pane on the first keystroke. It
// does NOT reproduce the original glitch, and cannot: that depended on the
// ghost having an offset of its own, so there is nothing left to revert it
// against short of restoring the whole two-coordinate model. Recorded
// rather than implied, because a test whose failure mode has not been
// observed is a test nobody should trust.
//
// Note what is NOT asserted: that arrowing down N and up N returns the top
// to where it started. It does not, and should not -- a reveal-based
// viewport lands the caret at the edge it approached from, which is how
// this pane has behaved for every file since F13a and has nothing to do
// with the ghost. The first version of this test asserted that and failed
// on its own premise.
func TestArrowingThroughALongGhostMovesTheViewportMonotonically(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 5}
	ws.ghost = &ghostText{anchorLine: 5, lines: longGhost(40)}
	ws.ghostFollow = false // the user's viewport, not the stream's
	ws.View(l)

	caretVisible := func(step string) {
		t.Helper()
		v := ws.viewIndexAt(d, ws.textArea.W)
		row := v.rowOf(d.cursor.Line)
		if row < d.top || row >= d.top+ws.textArea.H {
			t.Fatalf("%s: the caret is on row %d, outside the pane [%d,%d)",
				step, row, d.top, d.top+ws.textArea.H)
		}
	}

	const steps = 12
	for i := 0; i < steps; i++ {
		was := d.top
		ws.Update(tea.KeyMsg{Type: tea.KeyDown})
		ws.View(l)
		caretVisible("down " + strconv.Itoa(i+1))
		if d.top < was {
			t.Fatalf("down %d: the viewport moved UP, from %d to %d", i+1, was, d.top)
		}
	}
	for i := 0; i < steps; i++ {
		was := d.top
		ws.Update(tea.KeyMsg{Type: tea.KeyUp})
		ws.View(l)
		caretVisible("up " + strconv.Itoa(i+1))
		if d.top > was {
			t.Fatalf("up %d: the viewport moved DOWN, from %d to %d", i+1, was, d.top)
		}
	}
	if d.cursor.Line != 5 {
		t.Fatalf("the caret ended on line %d, want 5", d.cursor.Line)
	}
	// Back at the top of the region, the caret is the pane's first row --
	// the edge it arrived from, consistently.
	if v := ws.viewIndexAt(d, ws.textArea.W); d.top != v.rowOf(d.cursor.Line) {
		t.Errorf("top = %d, want the caret's own row %d", d.top, v.rowOf(d.cursor.Line))
	}
}

// ---------------------------------------------------------------------------
// The click mapping, RE-DERIVED against the unified model
// ---------------------------------------------------------------------------
//
// The earlier fix's arithmetic (screen_y, text_area_y, row_offset,
// injected) was built and verified when the ghost had its own offset, where
// the anchor could never be above the top of the pane. It can be now --
// scrolling past a region is the point of the rewrite -- so these do not
// reuse the old case. They cover a real line on EACH side of a region in
// one frame, and a real line below a region whose rows have scrolled off
// the top entirely.

// Real lines on both sides of a mid-file ghost, in ONE frame, all clicking
// correctly.
//
// The ghost is short enough that both sides fit, which is what makes the
// frame prove something: a sweep over a frame containing only pre-anchor
// lines would pass under the old model too.
func TestClickingRealLinesOnBothSidesOfAGhostInOneFrame(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	const anchor = 5
	d.cursor = Pos{Line: anchor}
	ws.ghost = &ghostText{anchorLine: anchor, lines: longGhost(8)}
	ws.View(l)

	lines, ghostRows := visibleLines(ws)
	if ghostRows != 8 {
		t.Fatalf("%d ghost rows on screen, want all 8", ghostRows)
	}
	var before, after int
	for n := range lines {
		if n <= anchor+1 {
			before++
		} else {
			after++
		}
	}
	if before == 0 || after == 0 {
		t.Fatalf("the frame has %d lines before the ghost and %d after; this "+
			"test needs both", before, after)
	}

	if bad := clickSweep(t, ws); bad > 0 {
		t.Errorf("%d row(s) mismatched with real lines on both sides of the ghost", bad)
	}
}

// A real line below a ghost whose rows are ABOVE the top of the pane.
//
// This state did not exist under the old model and is the one the mapping
// had to be re-derived for: `d.top` is past the anchor, so the injected
// block is entirely above the viewport and contributes rows to the
// sequence that the frame never draws. An index that counted injected rows
// "at the caret line" or "below the top" rather than resolving the row
// would be off by the whole region here.
func TestClickingBelowAGhostThatHasScrolledOffTheTop(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	const anchor = 5
	d.cursor = Pos{Line: anchor}
	ws.ghost = &ghostText{anchorLine: anchor, lines: longGhost(40)}
	ws.ghostFollow = true
	ws.View(l)

	// Far enough down that the whole region is above the pane.
	wheelDown(ws, 20)
	lines, ghostRows := visibleLines(ws)
	if ghostRows != 0 {
		t.Fatalf("%d ghost rows still on screen; this test needs the region "+
			"scrolled past", ghostRows)
	}
	for n := range lines {
		if n <= anchor+1 {
			t.Fatalf("line %d is on screen; the view has not passed the ghost", n)
		}
	}
	if bad := clickSweep(t, ws); bad > 0 {
		t.Errorf("%d row(s) mismatched below a ghost that scrolled off the top", bad)
	}
}

// ---------------------------------------------------------------------------
// The index itself
// ---------------------------------------------------------------------------

// rowOf and at are exact inverses, and `at` partitions the sequence with no
// gaps and no overlaps.
//
// A property test rather than examples, because this is the one piece every
// other behaviour above is derived from: the renderer, the hit-test and the
// viewport arithmetic all ask these two functions, and the whole point of
// having one index is that they cannot disagree.
func TestTheRowIndexAndItsInverseAgree(t *testing.T) {
	for _, v := range []viewIndex{
		{lines: 20},
		{lines: 20, blocks: []injBlock{{line: 0, rows: 1}}},
		{lines: 20, blocks: []injBlock{{line: 5, rows: 40}}},
		{lines: 20, blocks: []injBlock{{line: 19, rows: 3}}},
		{lines: 20, blocks: []injBlock{{line: 2, rows: 1}, {line: 7, rows: 9}}},
	} {
		// Every line round-trips.
		for line := 0; line < v.lines; line++ {
			got, injected, _ := v.at(v.rowOf(line))
			if injected || got != line {
				t.Errorf("%+v: line %d -> row %d -> (line %d, injected %v)",
					v, line, v.rowOf(line), got, injected)
			}
		}
		// Walking every row visits each line exactly once, in order, with
		// the injected rows in between.
		wantLine := 0
		for row := 0; row < v.total(); row++ {
			line, injected, blockRow := v.at(row)
			if injected {
				if n := v.injectedAfter(line); blockRow < 0 || blockRow >= n {
					t.Errorf("%+v: row %d is block row %d of %d at line %d",
						v, row, blockRow, n, line)
				}
				if line != wantLine-1 {
					t.Errorf("%+v: row %d injects after line %d, but the walk "+
						"has reached line %d", v, row, line, wantLine)
				}
				continue
			}
			if line != wantLine {
				t.Fatalf("%+v: row %d is line %d, want %d", v, row, line, wantLine)
			}
			wantLine++
		}
		if wantLine != v.lines {
			t.Errorf("%+v: the walk covered %d lines of %d", v, wantLine, v.lines)
		}
	}
}
