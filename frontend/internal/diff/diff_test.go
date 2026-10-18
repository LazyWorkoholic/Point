package diff

import (
	"strings"
	"testing"
)

// render turns a Result into the compact form these tests assert on, so a
// failure prints the whole diff rather than one mismatched field:
//
//	"12 = def f():" / "13 + added" / "14 - removed"
func render(r Result) string {
	var b strings.Builder
	for _, row := range r.Rows {
		var mark byte
		var no int
		switch row.Op {
		case Equal:
			mark, no = '=', row.New
		case Insert:
			mark, no = '+', row.New
		case Delete:
			mark, no = '-', row.Old
		}
		b.WriteString(string(mark))
		b.WriteByte(' ')
		b.WriteString(itoa(no))
		b.WriteByte(' ')
		b.WriteString(row.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// F09's UI-07 mockup, exactly: two lines inserted into the middle of a
// four-line function must read as TWO inserts, with the surrounding lines
// as context and the gutter in the PROPOSED numbering (12, 13, 14, 15).
//
// This is the property `editor\diff.go`'s coarse prefix/suffix diff cannot
// express and the reason this package exists.
func TestAnInsertionReadsAsInsertedLinesNotAReplacedBlock(t *testing.T) {
	original := []string{
		"import os",                 // 10
		"",                          // 11
		"def parse_config(path):",   // 12
		"    with open(path) as f:", // 13
		"        return f.read()",   // 14
	}
	// Region is lines 3-4 ("def ...", "    with ..."), rewritten with two
	// new lines between them.
	got := render(Region(original, 3, 4,
		"def parse_config(path):\n    if not path.exists():\n        raise ConfigError(path)\n    with open(path) as f:",
		DefaultContext))

	want := "" +
		"= 1 import os\n" +
		"= 2 \n" +
		"= 3 def parse_config(path):\n" +
		"+ 4     if not path.exists():\n" +
		"+ 5         raise ConfigError(path)\n" +
		"= 6     with open(path) as f:\n" +
		"= 7         return f.read()\n"
	if got != want {
		t.Errorf("diff =\n%s\nwant\n%s", got, want)
	}

	r := Region(original, 3, 4,
		"def parse_config(path):\n    if not path.exists():\n        raise ConfigError(path)\n    with open(path) as f:",
		DefaultContext)
	if r.Added != 2 || r.Removed != 0 {
		t.Errorf("Added/Removed = %d/%d, want 2/0", r.Added, r.Removed)
	}
	if r.Coarse {
		t.Error("Coarse set for a two-line change")
	}
}

// A replaced line is one delete and one insert, in that order, and the
// trailing context keeps its ORIGINAL numbering because the net line delta
// is zero.
func TestAReplacedLineIsDeleteThenInsert(t *testing.T) {
	original := []string{"a", "b", "c"}
	got := render(Region(original, 2, 2, "B", DefaultContext))
	want := "" +
		"= 1 a\n" +
		"- 2 b\n" +
		"+ 2 B\n" +
		"= 3 c\n"
	if got != want {
		t.Errorf("diff =\n%s\nwant\n%s", got, want)
	}
}

// A deletion shortens the file, so every line after the region renumbers
// DOWN. The trailing context is where that is visible, and it is the
// arithmetic Row's two line numbers exist to keep straight.
func TestTrailingContextRenumbersAfterADeletion(t *testing.T) {
	original := []string{"a", "b", "c", "d", "e"}
	// Lines 2-3 ("b", "c") collapse to one line.
	got := render(Region(original, 2, 3, "bc", DefaultContext))
	want := "" +
		"= 1 a\n" +
		"- 2 b\n" +
		"- 3 c\n" +
		"+ 2 bc\n" +
		"= 3 d\n" +
		"= 4 e\n"
	if got != want {
		t.Errorf("diff =\n%s\nwant\n%s", got, want)
	}
}

// Context is clamped at both ends of the file rather than producing
// negative indices or running past the last line.
func TestContextIsClampedAtBothEndsOfTheFile(t *testing.T) {
	original := []string{"one", "two"}
	got := render(Region(original, 1, 1, "ONE", DefaultContext))
	want := "" +
		"- 1 one\n" +
		"+ 1 ONE\n" +
		"= 2 two\n"
	if got != want {
		t.Errorf("first-line diff =\n%s\nwant\n%s", got, want)
	}

	got = render(Region(original, 2, 2, "TWO", DefaultContext))
	want = "" +
		"= 1 one\n" +
		"- 2 two\n" +
		"+ 2 TWO\n"
	if got != want {
		t.Errorf("last-line diff =\n%s\nwant\n%s", got, want)
	}
}

// ONE trailing newline is stripped, matching editor\buffer.go's
// newLineBuffer. Without this the diff shows a phantom empty line the apply
// would never produce -- the two must agree, because F9b diffs the scratch
// BUFFER's text and applies that same text.
func TestASingleTrailingNewlineIsStripped(t *testing.T) {
	original := []string{"a", "b"}
	with := render(Region(original, 1, 1, "A\n", 0))
	without := render(Region(original, 1, 1, "A", 0))
	if with != without {
		t.Errorf("a trailing newline changed the diff:\n%s\nvs\n%s", with, without)
	}
	// A SECOND newline is a real empty line and must survive.
	two := Region(original, 1, 1, "A\n\n", 0)
	if two.Added != 2 {
		t.Errorf("Added = %d for %q, want 2 (the empty line is real)", two.Added, "A\n\n")
	}
}

// An out-of-range region is clamped, not panicked on. B24 validated the
// range against the file the CONTEXT BUILDER read; the original here is an
// editor buffer, which may be shorter.
func TestAnOutOfRangeRegionIsClampedNotPanicked(t *testing.T) {
	original := []string{"a", "b"}
	for _, c := range []struct{ start, end int }{
		{0, 0}, {1, 99}, {99, 99}, {5, 2}, {-3, -1},
	} {
		r := Region(original, c.start, c.end, "X", DefaultContext)
		if len(r.Rows) == 0 {
			t.Errorf("Region(%d,%d) produced no rows", c.start, c.end)
		}
		for _, row := range r.Rows {
			if row.Old < 0 || row.New < 0 {
				t.Errorf("Region(%d,%d) produced a negative line number: %+v",
					c.start, c.end, row)
			}
		}
	}
	// An empty original is one empty line, per Buffer.Lines' convention.
	if r := Region(nil, 1, 1, "X", DefaultContext); len(r.Rows) == 0 {
		t.Error("an empty original produced no rows")
	}
}

// Past maxLCSLines the quadratic step is skipped and the result SAYS so.
// The bound is on the differing middle, so a huge region whose edit is one
// line still gets a per-line diff.
func TestTheLCSBoundDegradesToACoarseHunkAndReportsIt(t *testing.T) {
	big := make([]string, maxLCSLines+10)
	for i := range big {
		big[i] = "line " + itoa(i)
	}

	// Differing middle larger than the bound: every line shifted.
	shifted := make([]string, len(big))
	for i := range shifted {
		shifted[i] = "moved " + itoa(i)
	}
	coarse := Region(big, 1, len(big), strings.Join(shifted, "\n"), 0)
	if !coarse.Coarse {
		t.Error("a differing middle past the bound did not report Coarse")
	}

	// Same region size, but prefix/suffix trimming leaves a one-line
	// middle -- the bound must not trigger.
	oneEdit := make([]string, len(big))
	copy(oneEdit, big)
	oneEdit[len(oneEdit)/2] = "edited"
	fine := Region(big, 1, len(big), strings.Join(oneEdit, "\n"), 0)
	if fine.Coarse {
		t.Error("a one-line edit inside a large region reported Coarse")
	}
	if fine.Added != 1 || fine.Removed != 1 {
		t.Errorf("Added/Removed = %d/%d, want 1/1", fine.Added, fine.Removed)
	}
}

// Added and Removed are counted in the one funnel every row passes
// through, so they cannot disagree with Rows.
func TestCountsAgreeWithTheRows(t *testing.T) {
	original := []string{"a", "b", "c", "d"}
	r := Region(original, 2, 3, "B\nC\nD", DefaultContext)

	var added, removed int
	for _, row := range r.Rows {
		switch row.Op {
		case Insert:
			added++
		case Delete:
			removed++
		}
	}
	if added != r.Added || removed != r.Removed {
		t.Errorf("walked %d/%d, Result says %d/%d", added, removed, r.Added, r.Removed)
	}
}

// An unchanged region produces context only -- no spurious +/- rows. This
// is the case a refusal-to-apply would otherwise be built on top of, and
// the shape a model that echoes the region back unchanged produces.
func TestAnUnchangedRegionProducesNoChanges(t *testing.T) {
	original := []string{"a", "b", "c"}
	r := Region(original, 2, 2, "b", DefaultContext)
	if r.Added != 0 || r.Removed != 0 {
		t.Errorf("Added/Removed = %d/%d, want 0/0", r.Added, r.Removed)
	}
	for _, row := range r.Rows {
		if row.Op != Equal {
			t.Errorf("unchanged region produced %+v", row)
		}
	}
}
