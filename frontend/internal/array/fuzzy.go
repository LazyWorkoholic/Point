package array

import "strings"

// MatchKind ranks HOW a query matched a title. Higher is better — sorting by
// Kind descending is what keeps typing "run" from putting "runtime" below
// "bar_run_x" (F07-popup-framework.md's own example): "runtime" is a prefix
// match, "bar_run_x" only a substring one, and prefix always outranks it.
type MatchKind int

const (
	MatchNone        MatchKind = iota
	MatchSubsequence           // scattered, but in order — the fallback
	MatchSubstring             // contiguous, anywhere in the title
	MatchPrefix                // contiguous, at the very start
)

// Match is one item's result against a query.
type Match struct {
	Kind MatchKind

	// Highlights are rune indices into the title that matched, ascending —
	// what the renderer colours in the accent role.
	Highlights []int

	// span is Highlights[last]-Highlights[0]: how spread out the match is.
	// Unexported — it is a ranking detail, not something a caller should
	// read or depend on.
	span int
}

// fuzzyMatch tests title against a non-empty query: case-insensitive,
// subsequence at worst. The empty query is handled by the caller (Menu),
// which skips matching entirely when there is nothing to filter by.
func fuzzyMatch(title, query string) (Match, bool) {
	lower := []rune(strings.ToLower(title))
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return Match{}, false
	}

	if len(q) <= len(lower) && runesEqual(lower[:len(q)], q) {
		return Match{Kind: MatchPrefix, Highlights: identityRun(len(q)), span: len(q) - 1}, true
	}

	if idx := indexOfRunes(lower, q); idx >= 0 {
		return Match{Kind: MatchSubstring, Highlights: offsetRun(idx, len(q)), span: len(q) - 1}, true
	}

	// Greedy leftmost subsequence: the first title rune that matches each
	// query rune, in order. Not globally optimal (a tighter match may exist
	// further right), but O(len(title)), deterministic, and good enough to
	// rank and to highlight — this is a filter, not a search index.
	hl := make([]int, 0, len(q))
	qi := 0
	for i, r := range lower {
		if qi >= len(q) {
			break
		}
		if r == q[qi] {
			hl = append(hl, i)
			qi++
		}
	}
	if qi < len(q) {
		return Match{}, false
	}
	return Match{Kind: MatchSubsequence, Highlights: hl, span: hl[len(hl)-1] - hl[0]}, true
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexOfRunes(haystack, needle []rune) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if runesEqual(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func identityRun(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func offsetRun(start, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = start + i
	}
	return out
}

// lessMatch orders two scored rows: Kind descending (prefix beats substring
// beats subsequence), then span ascending (a tighter match of the same kind
// beats a looser one), with the caller responsible for the final, stable
// tiebreak (original order).
func lessMatch(a, b Match) bool {
	if a.Kind != b.Kind {
		return a.Kind > b.Kind
	}
	return a.span < b.span
}
