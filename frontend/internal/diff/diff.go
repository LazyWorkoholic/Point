// Package diff computes a line diff between a file's current contents and a
// REGION REWRITE of it -- B24's validated `FillProposal` applied to the
// lines it names.
//
// # What this is for, and what it refuses to be
//
// F09's UI-07: "The backend returns a rewritten region, not a patch; Go
// computes the diff locally -- models miscount line numbers and emit
// patches that will not apply." So the input here is never model-authored
// patch text and never a unified diff asked of a model. It is
// (`start_line`, `end_line`, `replacement`) -- three fields B24 has already
// validated against the region the model was actually shown
// (`managers\fill_proposal.py`: inverted ranges, ranges outside the shown
// region and file mismatches are all rejected before an event is emitted)
// -- plus the original lines, which the CALLER supplies from whatever it
// considers authoritative.
//
// That last part is why this package exists at all rather than living in
// `editor`. The diff is a pure function of two line slices; the question of
// which buffer or which file is "the original", and where the card that
// shows the result lives, are packaging questions F9b deliberately does not
// answer (see `editor\diffcard.go`'s header). Keeping the computation in a
// package with no dependency on `editor`, `app`, `chat`, Bubble Tea or
// `theme` means resolving them later costs nothing here.
//
// # Why not a library
//
// No new dependency, per CLAUDE.md, and nothing in go.mod does diffs.
//
// # Why not editor\diff.go's algorithm
//
// There is already a line diff in this repo: `editor\diff.go`'s
// `unifiedDiff`, built for B10's stale-save evidence view, which reports
// common prefix, common suffix, and everything between as ONE replaced
// hunk. Its own header states the limit and names this episode:
//
//	"F9b owns the real diff (its card computes one from B24's validated
//	proposal and F15 reuses it), and when that function exists this should
//	call it instead of keeping a second implementation."
//
// A coarse hunk is adequate evidence for "something else changed this file
// while you were editing it". It is not adequate for a review surface with
// an apply button: a proposal that inserts two lines into a twenty-line
// function must read as two inserted lines, not as a twenty-line deletion
// followed by a twenty-two-line insertion. So this one is an LCS diff and
// reports per-line operations.
//
// Swapping `unifiedDiff` over to call this is NOT done here -- it would
// change a surface F13b shipped and verified, in an episode whose Build
// list does not include it. Recorded in F9b's handoff instead, with the
// hook now in place.
package diff

import "strings"

// Op is what happened to one line.
type Op uint8

const (
	// Equal: the line is in both versions. Context, in diff terms.
	Equal Op = iota
	// Delete: the line is in the original and not in the proposal.
	Delete
	// Insert: the line is in the proposal and not in the original.
	Insert
)

// Row is one rendered line of the diff.
//
// Both line numbers are carried, and both are 1-based absolute numbers in
// their own version of the FILE -- not offsets within the region. A card
// rendering a gutter wants to print the number the user would see in the
// editor, and recomputing that from a region offset at render time is the
// kind of arithmetic that goes wrong once and then stays wrong.
//
// Old is 0 on an Insert and New is 0 on a Delete, because the line does not
// exist in that version. Zero rather than -1 so a caller that forgets to
// check prints nothing rather than a negative line number.
type Row struct {
	Op   Op
	Old  int
	New  int
	Text string
}

// Result is the whole diff plus the facts a header needs.
type Result struct {
	Rows []Row

	// Added and Removed count Insert and Delete rows. Counted here rather
	// than by the caller walking Rows, so "+2 -0" in a card header and the
	// rows below it can never disagree.
	Added   int
	Removed int

	// Coarse reports that the LCS step was skipped for a region larger
	// than maxLCSLines and the whole changed span is reported as one
	// delete-then-insert hunk -- `unifiedDiff`'s shape. Surfaced rather
	// than silent: a card that says "+340 -338" for a three-line change
	// is telling the truth about what it computed, and the user is owed
	// the reason.
	Coarse bool

	// Truncated reports that the row list hit maxRows and stops short.
	Truncated bool
}

// DefaultContext is how many unchanged lines flank the change. Three is
// `diff -u`'s default and what every reader of a patch expects.
const DefaultContext = 3

// maxLCSLines bounds the quadratic step, FOR CONTROL -- not as a measured
// performance claim.
//
// The LCS table is len(old) x len(new) cells, so an unbounded one is an
// unbounded allocation driven by a number that arrived from a model's
// reply. B11 caps the region a Filler is shown at roughly 2k tokens, so a
// real proposal is tens of lines and never approaches this; the bound
// exists so that a caller which hands over something else -- a whole file
// as "the region", a future non-Filler producer -- degrades to a coarse
// hunk instead of allocating without limit. Common prefix and suffix are
// trimmed BEFORE this is measured, so it applies to the genuinely differing
// middle only.
const maxLCSLines = 600

// maxRows bounds the output the same way, and for the same reason
// `editor\diff.go` bounds its own: nobody scrolls ten thousand rows, and
// building them costs more than the review they are for.
const maxRows = 2000

// Region diffs `original` against the same lines with [startLine, endLine]
// (1-based, inclusive) replaced by `replacement`.
//
// `context` unchanged lines either side of the region are included; pass
// DefaultContext unless there is a reason not to. A region at the very
// start or end of the file simply gets fewer.
//
// The line range is clamped to `original` rather than trusted. B24 has
// already validated it against the region the model was shown, but that
// check was against the file as the CONTEXT BUILDER read it, and the
// `original` passed here is whatever the caller considers authoritative --
// in F9b's case an editor buffer, which the user may have been typing in.
// Clamping keeps a stale range from indexing out of bounds; noticing that
// it IS stale is a different job, and not this function's (see
// `editor\diffcard.go` on the deferred staleness guard).
func Region(original []string, startLine, endLine int, replacement string, context int) Result {
	// An empty original is ONE EMPTY LINE, not zero lines -- Buffer.Lines'
	// convention ("an empty file is one empty line, which is the only
	// representation in which a cursor has somewhere to be"), and the
	// normalisation clampRange's own n<1 branch assumes. Done here rather
	// than there because clampRange returns numbers while the slicing
	// below needs the line to actually exist: without it, Region(nil, ...)
	// clamps to the range 1-1 and then indexes a zero-length slice.
	if len(original) == 0 {
		original = []string{""}
	}
	start, end := clampRange(len(original), startLine, endLine)
	if context < 0 {
		context = 0
	}

	oldRegion := original[start-1 : end]
	newRegion := splitReplacement(replacement)

	var res Result
	res.appendContext(original, start-context-1, start-1, 0)
	res.appendRegion(oldRegion, newRegion, start)
	// The proposal's lines renumber everything after the region, so the
	// trailing context's New numbers carry the region's net line delta
	// rather than matching their own Old number.
	res.appendContext(original, end, end+context, len(newRegion)-len(oldRegion))
	return res
}

// clampRange pulls a 1-based inclusive range inside a file of n lines.
//
// An empty `original` is one empty line as far as every caller here is
// concerned (the same convention Buffer.Lines documents), so n < 1 is
// treated as 1 and the region is that one line.
func clampRange(n, startLine, endLine int) (int, int) {
	if n < 1 {
		n = 1
	}
	if startLine < 1 {
		startLine = 1
	}
	if startLine > n {
		startLine = n
	}
	if endLine < startLine {
		endLine = startLine
	}
	if endLine > n {
		endLine = n
	}
	return startLine, endLine
}

// splitReplacement splits the rewritten region into lines, stripping ONE
// trailing newline.
//
// That matches `editor\buffer.go`'s newLineBuffer exactly, and it has to:
// F9b seeds a scratch buffer with this same string and diffs the buffer's
// own text, so a mismatch here would show a phantom trailing line in the
// diff that the apply would not produce. "a\nb\n" is a two-line region in
// every tool that counts lines.
func splitReplacement(replacement string) []string {
	return strings.Split(strings.TrimSuffix(replacement, "\n"), "\n")
}

// appendContext adds original[lo:hi) as Equal rows, clamped to the file.
//
// newOffset is what to add to an Old line number to get its New one: zero
// for context before the region (nothing above it moved) and the region's
// net line delta for context after it.
func (r *Result) appendContext(original []string, lo, hi, newOffset int) {
	if lo < 0 {
		lo = 0
	}
	if hi > len(original) {
		hi = len(original)
	}
	for i := lo; i < hi; i++ {
		r.add(Row{Op: Equal, Old: i + 1, New: i + 1 + newOffset, Text: original[i]})
	}
}

// appendRegion diffs the region itself, numbering from `start`.
func (r *Result) appendRegion(oldRegion, newRegion []string, start int) {
	// Common prefix and suffix are trimmed first. This is what keeps the
	// LCS table small for the overwhelmingly common proposal -- one edited
	// line inside an otherwise untouched function -- and it is also what
	// makes maxLCSLines a bound on the DIFFERING middle rather than on the
	// region as a whole.
	head := 0
	for head < len(oldRegion) && head < len(newRegion) && oldRegion[head] == newRegion[head] {
		head++
	}
	tail := 0
	for tail < len(oldRegion)-head && tail < len(newRegion)-head &&
		oldRegion[len(oldRegion)-1-tail] == newRegion[len(newRegion)-1-tail] {
		tail++
	}

	for i := 0; i < head; i++ {
		r.add(Row{Op: Equal, Old: start + i, New: start + i, Text: oldRegion[i]})
	}

	oldMid := oldRegion[head : len(oldRegion)-tail]
	newMid := newRegion[head : len(newRegion)-tail]
	oldNo, newNo := start+head, start+head
	oldIdx, newIdx := 0, 0

	if len(oldMid) > maxLCSLines || len(newMid) > maxLCSLines {
		r.Coarse = true
		for i, line := range oldMid {
			r.add(Row{Op: Delete, Old: oldNo + i, Text: line})
		}
		for i, line := range newMid {
			r.add(Row{Op: Insert, New: newNo + i, Text: line})
		}
	} else {
		for _, o := range lcsOps(oldMid, newMid) {
			switch o {
			case Equal:
				r.add(Row{Op: Equal, Old: oldNo, New: newNo, Text: oldMid[oldIdx]})
				oldNo, newNo = oldNo+1, newNo+1
				oldIdx, newIdx = oldIdx+1, newIdx+1
			case Delete:
				r.add(Row{Op: Delete, Old: oldNo, Text: oldMid[oldIdx]})
				oldNo++
				oldIdx++
			case Insert:
				r.add(Row{Op: Insert, New: newNo, Text: newMid[newIdx]})
				newNo++
				newIdx++
			}
		}
	}

	// The suffix sits at a different offset in each version, so it is
	// walked from the ends rather than from one shared index.
	for i := 0; i < tail; i++ {
		o := len(oldRegion) - tail + i
		n := len(newRegion) - tail + i
		r.add(Row{Op: Equal, Old: start + o, New: start + n, Text: oldRegion[o]})
	}
}

// add appends a row, counting it and enforcing maxRows.
//
// Every row goes through here so Added, Removed and Truncated cannot
// disagree with Rows -- the same "one funnel" reasoning
// `editor\autosave.go`'s editProbe records for arming the debounce.
func (r *Result) add(row Row) {
	if r.Truncated {
		return
	}
	if len(r.Rows) >= maxRows {
		r.Truncated = true
		return
	}
	switch row.Op {
	case Insert:
		r.Added++
	case Delete:
		r.Removed++
	}
	r.Rows = append(r.Rows, row)
}

// lcsOps returns the operation sequence turning a into b, longest-common-
// subsequence minimal.
//
// The table holds int32 rather than int: it is the single largest
// allocation in this package, and a region bounded by maxLCSLines cannot
// overflow an int32 subsequence length. A deletion is emitted before an
// insertion at the same position, so a replaced line reads as `-old` then
// `+new` -- the order every diff tool uses and the order a reader expects.
func lcsOps(a, b []string) []Op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}

	// table[i][j] is the LCS length of a[i:] and b[j:]. One flat
	// allocation, re-sliced per row: an [][]int32 of separately allocated
	// rows is the same arithmetic with n+1 allocations instead of one.
	table := make([][]int32, n+1)
	flat := make([]int32, (n+1)*(m+1))
	for i := range table {
		table[i] = flat[i*(m+1) : (i+1)*(m+1)]
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}

	ops := make([]Op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, Equal)
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, Delete)
			i++
		default:
			ops = append(ops, Insert)
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, Delete)
	}
	for ; j < m; j++ {
		ops = append(ops, Insert)
	}
	return ops
}
