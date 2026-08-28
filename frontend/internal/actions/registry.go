// Package actions is the single registration point for everything Point can
// do.
//
// The command palette, the leader hint bar, the help screen and config
// rebinding all read from this one registry. Adding a feature is one entry
// here, not edits in four files — and that is the whole reason the package
// exists.
//
// The registry is generic over the context type an action acts on. The root
// model uses Registry[*app.Root]; parameterising it is what keeps `actions`
// from importing `app`, which imports `actions`.
package actions

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Leader is the prefix key for the leader sequences. Ctrl+Alt+<letter> is
// unreliable on Windows Terminal, so the leader plus the palette is the
// supported path for everything that is not a top-tier chord.
const Leader = "ctrl+w"

// Action is one registered capability.
//
// Leader is the sequence AFTER the leader key: "we" means ctrl+w then e.
// Chord is a direct binding like "ctrl+p"; most actions have none.
// When decides availability — a false predicate greys the entry out in the
// palette rather than hiding it, so a feature never silently disappears.
type Action[T any] struct {
	ID          string
	Name        string
	Description string
	Category    string
	Leader      string
	Chord       string
	When        func(T) bool
	Run         func(T) tea.Cmd
}

// Available reports whether this action can run right now. An action with no
// predicate is always available.
func (a Action[T]) Available(ctx T) bool {
	if a.When == nil {
		return true
	}
	return a.When(ctx)
}

// Registry holds every action, plus the two binding indexes.
type Registry[T any] struct {
	list     []Action[T]
	byID     map[string]int
	byChord  map[string]int
	byLeader map[string]int

	// problems accumulates registration conflicts instead of panicking at the
	// call site, so Validate can report ALL of them at boot rather than the
	// first one.
	problems []string
}

func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{
		byID:     make(map[string]int),
		byChord:  make(map[string]int),
		byLeader: make(map[string]int),
	}
}

// Register adds an action. Conflicts are recorded, not raised — Validate is
// where they surface, all at once.
func (r *Registry[T]) Register(a Action[T]) {
	if a.ID == "" {
		r.problems = append(r.problems, fmt.Sprintf("action %q has no ID", a.Name))
		return
	}
	if a.Run == nil {
		r.problems = append(r.problems, fmt.Sprintf("action %q has no Run function", a.ID))
		return
	}

	if prev, dup := r.byID[a.ID]; dup {
		r.problems = append(r.problems, fmt.Sprintf(
			"duplicate action ID %q: already registered as %q (%s), now again as %q (%s)",
			a.ID, r.list[prev].Name, r.list[prev].Category, a.Name, a.Category))
		return
	}

	// Chords and leader sequences live in separate namespaces: a leader "wp"
	// and a chord "ctrl+p" do not collide, because you cannot type one when
	// the other is expected.
	if a.Chord != "" {
		if prev, dup := r.byChord[a.Chord]; dup {
			r.problems = append(r.problems, fmt.Sprintf(
				"duplicate chord %q: bound to %q (%s) and %q (%s)",
				a.Chord, r.list[prev].ID, r.list[prev].Name, a.ID, a.Name))
			return
		}
	}
	if a.Leader != "" {
		if prev, dup := r.byLeader[a.Leader]; dup {
			r.problems = append(r.problems, fmt.Sprintf(
				"duplicate leader sequence %q (%s then %s): bound to %q (%s) and %q (%s)",
				a.Leader, Leader, humanLeader(a.Leader),
				r.list[prev].ID, r.list[prev].Name, a.ID, a.Name))
			return
		}
		// A leader sequence that is a prefix of another can never fire — the
		// longer one always wins the next keystroke.
		for existing, idx := range r.byLeader {
			if strings.HasPrefix(existing, a.Leader) || strings.HasPrefix(a.Leader, existing) {
				r.problems = append(r.problems, fmt.Sprintf(
					"leader sequence %q (%s) shadows %q (%s): one is a prefix of the other, so the shorter can never fire",
					a.Leader, a.ID, existing, r.list[idx].ID))
				return
			}
		}
	}

	idx := len(r.list)
	r.list = append(r.list, a)
	r.byID[a.ID] = idx
	if a.Chord != "" {
		r.byChord[a.Chord] = idx
	}
	if a.Leader != "" {
		r.byLeader[a.Leader] = idx
	}
}

// Validate reports every conflict found during registration.
//
// It is called at boot, before the program starts, so a duplicate binding is a
// startup failure with both offenders named — not a keypress that silently
// does the wrong thing six weeks later.
func (r *Registry[T]) Validate() error {
	if len(r.problems) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "action registry has %d conflict(s):", len(r.problems))
	for _, p := range r.problems {
		b.WriteString("\n  · ")
		b.WriteString(p)
	}
	return fmt.Errorf("%s", b.String())
}

// ByID finds an action by its stable identifier — the key config rebinding
// writes against.
func (r *Registry[T]) ByID(id string) (Action[T], bool) {
	i, ok := r.byID[id]
	if !ok {
		return Action[T]{}, false
	}
	return r.list[i], true
}

// ByChord resolves a direct binding.
func (r *Registry[T]) ByChord(chord string) (Action[T], bool) {
	i, ok := r.byChord[chord]
	if !ok {
		return Action[T]{}, false
	}
	return r.list[i], true
}

// ByLeader resolves a completed leader sequence.
func (r *Registry[T]) ByLeader(seq string) (Action[T], bool) {
	i, ok := r.byLeader[seq]
	if !ok {
		return Action[T]{}, false
	}
	return r.list[i], true
}

// LeaderPrefix reports whether seq could still grow into a binding. This is
// what keeps the hint bar open while a multi-key sequence is half typed.
func (r *Registry[T]) LeaderPrefix(seq string) bool {
	for existing := range r.byLeader {
		if len(existing) > len(seq) && strings.HasPrefix(existing, seq) {
			return true
		}
	}
	return false
}

// All returns every action in registration order. The palette and help screen
// read this.
func (r *Registry[T]) All() []Action[T] {
	out := make([]Action[T], len(r.list))
	copy(out, r.list)
	return out
}

// Len is the count, for the "N actions registered" line the placeholder shows.
func (r *Registry[T]) Len() int { return len(r.list) }

// LeaderHints lists every leader-bound action, sorted by sequence — exactly
// what the hint bar renders after the leader-pending delay.
func (r *Registry[T]) LeaderHints() []Action[T] {
	out := make([]Action[T], 0, len(r.byLeader))
	for _, i := range r.byLeader {
		out = append(out, r.list[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Leader < out[j].Leader })
	return out
}

// humanLeader renders "we" as "w e" for a message a person has to read.
func humanLeader(seq string) string {
	parts := make([]string, 0, len(seq))
	for _, r := range seq {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, " ")
}
