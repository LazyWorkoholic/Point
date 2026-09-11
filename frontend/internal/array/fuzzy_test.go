package array

import (
	"reflect"
	"testing"
)

func TestFuzzyMatchKinds(t *testing.T) {
	cases := []struct {
		title, query string
		want         MatchKind
	}{
		{"runtime", "run", MatchPrefix},
		{"bar_run_x", "run", MatchSubstring},
		{"bar run baz", "brb", MatchSubsequence},
		{"anything", "xyz", MatchNone},
		{"Runtime", "RUN", MatchPrefix}, // case-insensitive
	}
	for _, c := range cases {
		m, ok := fuzzyMatch(c.title, c.query)
		if c.want == MatchNone {
			if ok {
				t.Errorf("fuzzyMatch(%q,%q) matched, want no match", c.title, c.query)
			}
			continue
		}
		if !ok {
			t.Fatalf("fuzzyMatch(%q,%q) did not match, want %v", c.title, c.query, c.want)
		}
		if m.Kind != c.want {
			t.Errorf("fuzzyMatch(%q,%q).Kind = %v, want %v", c.title, c.query, m.Kind, c.want)
		}
	}
}

// F07's own example: typing "run" must rank "runtime" (a prefix match) above
// "bar_run_x" (only a substring match).
func TestPrefixOutranksSubstring(t *testing.T) {
	m := NewMenu()
	m.SetItems([]*Item{NewItem("a", "bar_run_x"), NewItem("b", "runtime")})
	m.Filter("run")

	rows := m.VisibleRows()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Item.GetID() != "b" {
		t.Errorf("first result = %q, want the prefix match \"runtime\"", rows[0].Item.GetID())
	}
}

func TestPrefixHighlightsCoverTheWholeQuery(t *testing.T) {
	m, _ := fuzzyMatch("runtime", "run")
	want := []int{0, 1, 2}
	if !reflect.DeepEqual(m.Highlights, want) {
		t.Errorf("Highlights = %v, want %v", m.Highlights, want)
	}
}

func TestSubsequenceHighlightsAreInOrder(t *testing.T) {
	m, ok := fuzzyMatch("bar run baz", "brb")
	if !ok {
		t.Fatal("expected a subsequence match")
	}
	for i := 1; i < len(m.Highlights); i++ {
		if m.Highlights[i] <= m.Highlights[i-1] {
			t.Fatalf("highlights not ascending: %v", m.Highlights)
		}
	}
}
