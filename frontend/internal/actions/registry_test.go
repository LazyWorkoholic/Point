package actions

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type ctx struct{ ready bool }

func noop(ctx) tea.Cmd { return nil }

func act(id, name string) Action[ctx] {
	return Action[ctx]{ID: id, Name: name, Category: "test", Run: noop}
}

func TestCleanRegistryValidates(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "app.quit", Name: "Quit", Chord: "ctrl+c", Run: noop})
	r.Register(Action[ctx]{ID: "app.palette", Name: "Palette", Chord: "ctrl+p", Run: noop})
	r.Register(Action[ctx]{ID: "ui.help", Name: "Help", Leader: "h", Run: noop})

	if err := r.Validate(); err != nil {
		t.Fatalf("clean registry rejected: %v", err)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
}

// "Fail loudly at boot, not silently at keypress" — and name BOTH offenders,
// because naming one leaves you grepping for the other.
func TestDuplicateIDNamesBoth(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(act("app.quit", "Quit"))
	r.Register(act("app.quit", "Exit"))

	err := r.Validate()
	if err == nil {
		t.Fatal("duplicate ID accepted")
	}
	msg := err.Error()
	for _, want := range []string{"app.quit", "Quit", "Exit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %q: %s", want, msg)
		}
	}
}

func TestDuplicateChordNamesBoth(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "app.palette", Name: "Palette", Chord: "ctrl+p", Run: noop})
	r.Register(Action[ctx]{ID: "ui.print", Name: "Print", Chord: "ctrl+p", Run: noop})

	err := r.Validate()
	if err == nil {
		t.Fatal("duplicate chord accepted")
	}
	for _, want := range []string{"ctrl+p", "app.palette", "ui.print"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %q: %s", want, err)
		}
	}
}

func TestDuplicateLeaderNamesBoth(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "ws.editor", Name: "Editor", Leader: "e", Run: noop})
	r.Register(Action[ctx]{ID: "ws.explain", Name: "Explain", Leader: "e", Run: noop})

	err := r.Validate()
	if err == nil {
		t.Fatal("duplicate leader accepted")
	}
	for _, want := range []string{"ws.editor", "ws.explain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %q: %s", want, err)
		}
	}
}

// A leader sequence that is a prefix of another can never fire.
func TestPrefixLeaderSequenceIsAConflict(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "ws.editor", Name: "Editor", Leader: "e", Run: noop})
	r.Register(Action[ctx]{ID: "ws.explain", Name: "Explain", Leader: "ex", Run: noop})

	err := r.Validate()
	if err == nil {
		t.Fatal("prefix-shadowed leader sequence accepted")
	}
	if !strings.Contains(err.Error(), "prefix") {
		t.Errorf("message does not explain the shadowing: %s", err)
	}
}

// Chords and leader sequences are separate namespaces: "p" after the leader
// and "ctrl+p" directly are different keystrokes.
func TestChordAndLeaderDoNotCollide(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "a", Name: "A", Chord: "ctrl+p", Run: noop})
	r.Register(Action[ctx]{ID: "b", Name: "B", Leader: "p", Run: noop})

	if err := r.Validate(); err != nil {
		t.Fatalf("separate namespaces reported as a conflict: %v", err)
	}
}

// Every conflict is reported at once — fixing one and re-running to find the
// next is the thing this avoids.
func TestValidateReportsEveryConflict(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(act("dup", "First"))
	r.Register(act("dup", "Second"))
	r.Register(Action[ctx]{ID: "x", Name: "X", Chord: "ctrl+k", Run: noop})
	r.Register(Action[ctx]{ID: "y", Name: "Y", Chord: "ctrl+k", Run: noop})

	err := r.Validate()
	if err == nil {
		t.Fatal("conflicts accepted")
	}
	if !strings.Contains(err.Error(), "2 conflict(s)") {
		t.Errorf("did not report both: %s", err)
	}
}

func TestMissingIDAndRunAreConflicts(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{Name: "Nameless", Run: noop})
	r.Register(Action[ctx]{ID: "no.run", Name: "Inert"})

	err := r.Validate()
	if err == nil {
		t.Fatal("malformed actions accepted")
	}
	if !strings.Contains(err.Error(), "no ID") || !strings.Contains(err.Error(), "no Run") {
		t.Errorf("both problems should be named: %s", err)
	}
	if r.Len() != 0 {
		t.Errorf("malformed actions were registered anyway: %d", r.Len())
	}
}

func TestLookups(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "app.palette", Name: "Palette", Chord: "ctrl+p", Leader: "p", Run: noop})

	if _, ok := r.ByID("app.palette"); !ok {
		t.Error("ByID missed")
	}
	if _, ok := r.ByChord("ctrl+p"); !ok {
		t.Error("ByChord missed")
	}
	if _, ok := r.ByLeader("p"); !ok {
		t.Error("ByLeader missed")
	}
	if _, ok := r.ByID("nope"); ok {
		t.Error("ByID invented an action")
	}
}

func TestLeaderPrefixKeepsTheHintBarOpen(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "a", Name: "A", Leader: "ga", Run: noop})
	r.Register(Action[ctx]{ID: "b", Name: "B", Leader: "gb", Run: noop})

	if !r.LeaderPrefix("g") {
		t.Error("g should still be growing into ga/gb")
	}
	if r.LeaderPrefix("ga") {
		t.Error("ga is complete, not a prefix")
	}
	if r.LeaderPrefix("z") {
		t.Error("z leads nowhere")
	}
}

func TestLeaderHintsAreSorted(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "c", Name: "C", Leader: "z", Run: noop})
	r.Register(Action[ctx]{ID: "a", Name: "A", Leader: "a", Run: noop})
	r.Register(Action[ctx]{ID: "b", Name: "B", Leader: "m", Run: noop})

	hints := r.LeaderHints()
	if len(hints) != 3 {
		t.Fatalf("got %d hints", len(hints))
	}
	for i := 1; i < len(hints); i++ {
		if hints[i-1].Leader > hints[i].Leader {
			t.Fatalf("hints unsorted: %v", []string{hints[0].Leader, hints[1].Leader, hints[2].Leader})
		}
	}
}

func TestAvailability(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(Action[ctx]{ID: "always", Name: "Always", Run: noop})
	r.Register(Action[ctx]{ID: "gated", Name: "Gated", Run: noop,
		When: func(c ctx) bool { return c.ready }})

	always, _ := r.ByID("always")
	gated, _ := r.ByID("gated")

	if !always.Available(ctx{}) {
		t.Error("an action with no predicate should always be available")
	}
	if gated.Available(ctx{ready: false}) {
		t.Error("gated action available when it should not be")
	}
	if !gated.Available(ctx{ready: true}) {
		t.Error("gated action unavailable when it should be")
	}
}

// All returns a copy — the palette must not be able to reorder the registry
// by sorting the slice it was handed.
func TestAllReturnsACopy(t *testing.T) {
	r := NewRegistry[ctx]()
	r.Register(act("a", "A"))
	r.Register(act("b", "B"))

	all := r.All()
	all[0] = act("hacked", "Hacked")

	if again := r.All(); again[0].ID != "a" {
		t.Fatalf("registry was mutated through All(): %q", again[0].ID)
	}
}
