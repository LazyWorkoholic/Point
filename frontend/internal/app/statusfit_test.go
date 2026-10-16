package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// The status bar's width budget.
//
// Reported as "a trailing segment disappears once the cursor's column
// reaches double digits, even though fullscreening shows there was room
// for it all along".
//
// Measured, that is TWO things, and neither is a miscounted digit --
// fitHints measures every part with runewidth and its drop boundary is
// exactly `total <= width` (TestFitHintsDropBoundaryIsExact below):
//
//  1. the left half was built against `width - right - 1`, so a segment
//     was dropped while thirty cells of the line went to Root's own
//     `connected · N task(s) · W×H` readout. At a 100-column terminal:
//     width 98, right 30, budget 67, and a dirty file's segments need 77 --
//     dropped, with 21 cells spare in the line. At 110 the budget is
//     exactly 77, so one more digit in `1:10` tips it over, which is the
//     double-digit trigger;
//  2. fitHints reserved room for its own "+N" suffix while deciding each
//     part, which at a width the full set fits EXACTLY dropped parts that
//     did not need to go.

// segWidth is the width a joined segment list renders at.
func segWidth(parts []string) int {
	n := 0
	for i, p := range parts {
		if i > 0 {
			n += runewidth.StringWidth(hintGap)
		}
		n += runewidth.StringWidth(p)
	}
	return n
}

// ---------------------------------------------------------------------------
// fitParts / fitHints
// ---------------------------------------------------------------------------

// The drop boundary is exactly "does the whole set fit", at every digit
// count of the line:col segment. This is the assertion that says the digit
// count was never the problem.
func TestFitHintsDropBoundaryIsExact(t *testing.T) {
	base := []string{"big.py", "1:6", "python", "CRLF", "4sp", "ctrl+o tree", "ctrl+p palette"}

	for _, col := range []string{"1:6", "1:10", "1:100", "1:1000", "1:10000"} {
		parts := append([]string(nil), base...)
		parts[1] = col
		total := segWidth(parts)

		for width := total - 4; width <= total+4; width++ {
			out, kept := fitParts(parts, width)
			all := kept == len(parts)
			if want := total <= width; all != want {
				t.Errorf("col %s at width %d (total %d): kept all = %v, want %v (%q)",
					col, width, total, all, want, out)
			}
			if all && visibleWidth(out) != total {
				t.Errorf("col %s at width %d: kept all but rendered %d cells, want %d",
					col, width, visibleWidth(out), total)
			}
			if visibleWidth(out) > width {
				t.Errorf("col %s at width %d: rendered %d cells, over budget",
					col, width, visibleWidth(out))
			}
		}
	}
}

// Growing line:col from one digit to five drops nothing that would fit --
// the test the report asks for, stated as the invariant rather than as a
// single width.
func TestGrowingLineColDropsNothingThatFits(t *testing.T) {
	base := []string{"pkg/file.py", "python", "CRLF", "4sp", "ctrl+o tree"}

	for _, col := range []string{"1:1", "1:9", "1:10", "1:99", "1:100", "12:1000", "123:10000"} {
		parts := append([]string{base[0], col}, base[1:]...)
		total := segWidth(parts)
		// A width with room to spare: nothing may be dropped.
		for _, slack := range []int{0, 1, 5, 40} {
			_, kept := fitParts(parts, total+slack)
			if kept != len(parts) {
				t.Errorf("col %s at width %d (total %d, slack %d): kept %d of %d",
					col, total+slack, total, slack, kept, len(parts))
			}
		}
	}
}

// The reserve bug: three parts totalling 27 cells at width 27 rendered
// "aaaaaaaaaa   +2", dropping two parts to make room for a suffix that
// was only needed because they were dropped.
func TestTheSuffixReserveDoesNotDropAPartThatFits(t *testing.T) {
	cases := [][]string{
		{"aaaaaaaaaa", "bbbbbbbbbb", "c"},
		{"aaaa", "b"},
		{"file.py", "1:1", "py", "LF", "x"},
		{"a", "b", "c", "d", "e"},
	}
	for _, parts := range cases {
		total := segWidth(parts)
		out, kept := fitParts(parts, total)
		if kept != len(parts) {
			t.Errorf("%v at its exact total width %d: kept %d of %d (%q)",
				parts, total, kept, len(parts), out)
		}
		if strings.Contains(out, "+") {
			t.Errorf("%v at its exact total width: rendered a drop count (%q)", parts, out)
		}
	}
}

// ...and a set that genuinely does not fit still drops whole parts and
// counts them, which is fitHints' own contract.
func TestAnOverlongSetStillDropsWholePartsAndCounts(t *testing.T) {
	parts := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"}
	out, kept := fitParts(parts, 20)
	if kept != 1 {
		t.Fatalf("kept %d, want 1 (%q)", kept, out)
	}
	if !strings.HasSuffix(out, "+2") {
		t.Errorf("out = %q, want a +2 suffix", out)
	}
	if visibleWidth(out) > 20 {
		t.Errorf("out is %d cells, over the 20-cell budget", visibleWidth(out))
	}
	// No part was cut in half.
	if strings.Contains(out, "bbb") {
		t.Errorf("out = %q, a dropped part leaked a fragment", out)
	}
}

// ---------------------------------------------------------------------------
// statusLeft: who yields to whom
// ---------------------------------------------------------------------------

// A contributor's segments take the line and the right half yields --
// but ONLY when a segment would otherwise be lost.
func TestSegmentsTakeTheLineFromTheRightHalfOnlyWhenTheyMust(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)

	// Short segments: both halves fit, so the readout stays.
	ws.segments = []string{"a.py", "1:1"}
	text, keepRight := r.statusLeft(118, 30)
	if !keepRight {
		t.Error("the right half was dropped for segments that fitted beside it")
	}
	if visibleWidth(text) != segWidth(ws.segments) {
		t.Errorf("segments rendered %d cells, want %d", visibleWidth(text), segWidth(ws.segments))
	}

	// Segments that fit the LINE but not beside the readout: the readout
	// yields, and nothing is dropped.
	ws.segments = []string{
		"tests/fixtures/f13a/big.py", "1:10", "python", "LF", "4sp",
		"• modified", "ctrl+o tree", "ctrl+p palette",
	}
	total := segWidth(ws.segments)
	width := total + 2 // room in the line, none beside a 30-cell readout
	text, keepRight = r.statusLeft(width, 30)
	if keepRight {
		t.Errorf("the right half was kept and a segment lost: %q", text)
	}
	if visibleWidth(text) != total {
		t.Errorf("segments rendered %d cells, want all %d", visibleWidth(text), total)
	}
	if strings.Contains(text, "+1") {
		t.Errorf("a segment was dropped with room in the line: %q", text)
	}
}

// The BINDINGS hint keeps F1's rule untouched: the right half stays unless
// the hint would be squeezed below hintFloor. A dropped hint is recoverable
// from the palette and Help; a dropped file segment is not recoverable
// from anywhere, which is the whole reason the two differ.
func TestTheBindingsHintStillYieldsToTheRightHalf(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = nil // no contributor segments
	r := rootWith(t, ws)

	_, keepRight := r.statusLeft(118, 30)
	if !keepRight {
		t.Error("the right half was dropped at a comfortable width")
	}
	// Below hintFloor for the left half, the right half goes -- F1's rule.
	_, keepRight = r.statusLeft(hintFloor+30, 30)
	if keepRight {
		t.Error("the right half was kept while the hint had under hintFloor cells")
	}
}

// ---------------------------------------------------------------------------
// End to end, through the rendered frame
// ---------------------------------------------------------------------------

// The repro: grow the column through the digit boundaries at a fixed
// terminal size and assert no segment is lost at a width where the whole
// set fits the line.
func TestNoSegmentIsLostAsTheColumnGrows(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)

	for _, tw := range []int{90, 100, 110, 120, 160, 200} {
		for _, col := range []string{"1:6", "1:10", "1:100", "1:1000"} {
			ws.segments = []string{
				"tests/fixtures/f13a/big.py", col, "python", "CRLF", "4sp",
				"• modified", "ctrl+o tree", "ctrl+p palette",
			}
			total := segWidth(ws.segments)
			width := tw - 2 // the status bar's own rect, less the margins

			frame := stripANSI(size(t, r, tw, 30))
			lines := strings.Split(frame, "\n")
			bar := lines[len(lines)-2] // the status bar sits above the bottom margin

			if total > width {
				continue // genuinely does not fit; dropping is correct
			}
			for _, seg := range ws.segments {
				if !strings.Contains(bar, seg) {
					t.Errorf("term %d col %s (total %d, width %d): segment %q is missing\n%s",
						tw, col, total, width, seg, bar)
				}
			}
			if strings.Contains(bar, "+1") || strings.Contains(bar, "+2") {
				t.Errorf("term %d col %s: a drop count is shown with room to spare\n%s",
					tw, col, bar)
			}
		}
	}
}

// Every line is still within the terminal with the segments taking the
// line -- dropping the right half must not have let the left overflow.
func TestTheStatusBarStaysInsideTheTerminalWhenSegmentsTakeTheLine(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	ws.segments = []string{
		"a/very/long/path/to/some/file/in/the/project.py", "1234:5678",
		"python", "CRLF", "8sp", "• modified", "read-only (file is read-only)",
		"ctrl+o tree", "ctrl+p palette",
	}
	for _, tw := range []int{60, 80, 100, 120, 200} {
		for i, line := range strings.Split(size(t, r, tw, 30), "\n") {
			if got := visibleWidth(line); got > tw {
				t.Errorf("term %d row %d: %d cells\n%q", tw, i, got, stripANSI(line))
			}
		}
	}
}

// A transient status message still outranks everything, and the right half
// comes back with it -- the yielding is the segments' affair alone.
func TestATransientMessageKeepsTheRightHalf(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	ws.segments = []string{"a.py", "1:1"}
	r.status = "save is not wired yet"

	bar := stripANSI(size(t, r, 120, 30))
	if !strings.Contains(bar, "save is not wired yet") {
		t.Error("the message is not shown")
	}
	if !strings.Contains(bar, fmt.Sprintf("%d×%d", 120, 30)) {
		t.Error("the right half was dropped for a transient message")
	}
}
