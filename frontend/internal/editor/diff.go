package editor

import (
	"fmt"
	"strings"
)

// A plain unified diff, line-based (B10).
//
// # Why this is here and not from a library
//
// No new dependency, per CLAUDE.md, and nothing in go.mod does diffs.
// What [diff] has to answer is one question -- "what is different
// between the file on disk and what I have been typing" -- for a file
// small enough to be open in this editor. A Myers implementation over an
// LCS table is the right tool when the answer has to be minimal; this
// only has to be TRUE and readable.
//
// # The algorithm, and its honest limit
//
// Common prefix, common suffix, and everything between them reported as
// one replaced hunk. That is exact for the overwhelmingly common shape
// -- somebody appended to the file, or edited one region -- and it is
// correct but COARSE when the two versions differ in two widely
// separated places: it reports one hunk spanning both rather than two
// small ones. It never reports a difference that is not there and never
// hides one that is, which are the two properties a refusal dialog's
// evidence has to have.
//
// Stated as a limit rather than left to be discovered: F9b owns the real
// diff (its card computes one from B24's validated proposal and F15
// reuses it), and when that function exists this should call it instead
// of keeping a second implementation. Recorded in the handoff.

// diffContext is how many unchanged lines are shown either side of a
// hunk. Three is `diff -u`'s own default and what every reader of a
// patch expects.
const diffContext = 3

// diffMaxLines bounds the rendered output.
//
// A diff of a 5,000-line file against an empty buffer is 10,000 lines of
// overlay nobody scrolls through, and building it costs more than the
// save it is explaining. The cap is stated in the output rather than
// applied silently.
const diffMaxLines = 2000

// unifiedDiff renders the difference between two LF-joined texts.
func unifiedDiff(oldText, newText, oldLabel, newLabel string) []string {
	if oldText == newText {
		return []string{"--- " + oldLabel, "+++ " + newLabel, "",
			"The file on disk and your buffer are identical.",
			"",
			"Only the modification TIME differs -- something rewrote the file with",
			"the same contents. [overwrite] is safe here.",
		}
	}

	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Common prefix.
	head := 0
	for head < len(oldLines) && head < len(newLines) && oldLines[head] == newLines[head] {
		head++
	}
	// Common suffix, never overlapping the prefix.
	tail := 0
	for tail < len(oldLines)-head && tail < len(newLines)-head &&
		oldLines[len(oldLines)-1-tail] == newLines[len(newLines)-1-tail] {
		tail++
	}

	oldHunk := oldLines[head : len(oldLines)-tail]
	newHunk := newLines[head : len(newLines)-tail]

	ctxStart := head - diffContext
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := tail
	if ctxEnd > diffContext {
		ctxEnd = diffContext
	}

	out := []string{
		"--- " + oldLabel,
		"+++ " + newLabel,
		fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			head+1, len(oldHunk), head+1, len(newHunk)),
	}
	for i := ctxStart; i < head; i++ {
		out = append(out, " "+oldLines[i])
	}
	for _, l := range oldHunk {
		out = append(out, "-"+l)
		if len(out) >= diffMaxLines {
			return append(out, fmt.Sprintf("… truncated at %d lines", diffMaxLines))
		}
	}
	for _, l := range newHunk {
		out = append(out, "+"+l)
		if len(out) >= diffMaxLines {
			return append(out, fmt.Sprintf("… truncated at %d lines", diffMaxLines))
		}
	}
	for i := 0; i < ctxEnd; i++ {
		out = append(out, " "+oldLines[len(oldLines)-tail+i])
	}
	return out
}
