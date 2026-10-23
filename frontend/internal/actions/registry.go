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

// Leader and LeaderAlt are BOTH the prefix key for the leader sequences.
// Either one starts a chord; neither is a fallback for the other.
//
// # Why there are two
//
// ctrl+w cannot be relied on, through no fault of this codebase.
// bubbletea's Windows key reader (v1 key_windows.go) returns KeyRunes when
// a CTRL bit is set but neither the control character nor the virtual-key
// code matches its tables -- its final `return KeyRunes` -- and KeyMsg has
// no Ctrl field to carry the modifier. Ctrl+Shift+W reports Char 'W', so
// it arrives as an ordinary printable rune, indistinguishable from typing.
// In an editor that means the letter is inserted into the file. F15's gate
// caught it three ways: `cause="key w"` on repeated fast presses,
// `cause="key W"` right after a workspace load, `cause="key q"` at quit --
// and a doubled "ww" when the chord was pressed immediately after
// submitting the fill prompt, where the mangled first key leaves nothing
// pending so the SECOND key lands as text too.
//
// F2 is immune by construction: VK_F2 resolves in the virtual-key table
// BEFORE the ctrl/rune branch is reached, and carries no rune to insert.
//
// Alt+<letter> was read and rejected rather than assumed. It is a cleaner
// path than ctrl -- Alt is set unconditionally from ControlKeyState on
// every KeyMsg, outside keyType's lookups, so it cannot be lost the same
// way -- but AltGr is LEFT_CTRL|RIGHT_ALT, which sets that same Alt bit,
// so on a non-US layout an alt-letter leader would swallow AltGr-composed
// characters. Trading one layout-dependent ambiguity for another is not a
// fix.
//
// # Both stay, and both are shown
//
// Removing ctrl+w would make every muscle-memory press insert a literal
// letter 100% of the time instead of occasionally -- strictly worse. So it
// stays fully wired, and LeaderLabel below is what every hint, help screen
// and status line prints, so neither key is a secret.
const (
	Leader    = "f2"
	LeaderAlt = "ctrl+w"
)

// LeaderLabel is how the leader is NAMED to the user, everywhere.
//
// One value so a hint can never advertise half of it -- F04's rule that a
// hint must not describe a binding that does not work, applied to the
// opposite failure: a binding that works and is not described.
//
// DERIVED from the two keys rather than written out. It was a hand-typed
// "F2 / ctrl+w" first, which is how the casing bug below got in: the
// constant said F2 and every other function key printed itself raw, so the
// help screen read "f3 / ctrl+q" next to "F2 / ctrl+w". Deriving it means
// changing a leader key cannot leave the label describing the old one, and
// the casing is whatever KeyLabel says for everybody.
var LeaderLabel = KeyLabel(Leader) + " / " + KeyLabel(LeaderAlt)

// KeyLabel is how ONE key is named to the user.
//
// # Function keys are upper-case; everything else is printed as it is bound
//
// bubbletea spells function keys lower-case ("f1", "f12") because that is
// the key NAME it matches on, and for a while this package passed those
// straight through to the UI while LeaderLabel spelled its own "F2" by
// hand. The result was visible in one line of the help screen --
// "f3 / ctrl+q · F2 / ctrl+w q" -- and in the Editor's empty state, which
// printed "f5", "f6" and "f7" beside it. Nobody writes F2 as f2 on a
// keyboard; the key is engraved upper-case.
//
// Modifier chords are NOT touched. "ctrl+p" stays lower-case, which is
// what every hint in this app has printed since F1 and what the status bar
// still prints beside these ("ctrl+p palette"). Upper-casing those would
// be a second, larger change to every hint in the app for no reason
// anybody asked for -- and "CTRL+P" is not how this UI talks.
//
// Named keys that are words ("tab", "esc", "enter") fall through
// unchanged for the same reason.
func KeyLabel(key string) string {
	if isFunctionKey(key) {
		return strings.ToUpper(key)
	}
	return key
}

// isFunctionKey matches exactly f1..f99 and nothing else -- not "ctrl+f1"
// (a modifier chord, left alone) and not "friend" (not a key, but cheap
// insurance against a label that is a word beginning with f).
func isFunctionKey(key string) bool {
	if len(key) < 2 || len(key) > 3 || key[0] != 'f' {
		return false
	}
	for i := 1; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	return true
}

// IsLeader reports whether a key starts a leader sequence.
//
// Every caller asks this rather than comparing against Leader, so adding
// or changing a leader key cannot leave one site behind.
func IsLeader(key string) bool {
	return key == Leader || key == LeaderAlt
}

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
	// AltChord is a SECOND direct binding for the same action, equal in
	// standing to Chord rather than a fallback. It exists because quit
	// needs one for the reason the leader does -- see Leader's own doc on
	// bubbletea's Windows key reader -- and both are shown to the user.
	AltChord string
	When     func(T) bool
	Run      func(T) tea.Cmd
}

// chords is every direct binding this action answers to.
func (a Action[T]) chords() []string {
	var out []string
	if a.Chord != "" {
		out = append(out, a.Chord)
	}
	if a.AltChord != "" {
		out = append(out, a.AltChord)
	}
	return out
}

// ChordLabel is how this action's direct binding is NAMED to the user:
// both keys when there are two, so neither is a secret.
func (a Action[T]) ChordLabel() string {
	out := a.chords()
	for i, k := range out {
		out[i] = KeyLabel(k)
	}
	return strings.Join(out, " / ")
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
	for _, chord := range a.chords() {
		if prev, dup := r.byChord[chord]; dup {
			r.problems = append(r.problems, fmt.Sprintf(
				"duplicate chord %q: bound to %q (%s) and %q (%s)",
				chord, r.list[prev].ID, r.list[prev].Name, a.ID, a.Name))
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
	for _, chord := range a.chords() {
		r.byChord[chord] = idx
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
