package app

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
)

// F15: the leader and quit each answer to TWO keys, and both are shown.
//
// Why there are two is bubbletea's Windows key reader, not this codebase:
// it returns KeyRunes when a CTRL bit is set but neither the control
// character nor the virtual-key code matches its tables, and KeyMsg has no
// Ctrl field to carry the modifier -- so Ctrl+Shift+W arrives as a bare
// 'W' and, in an editor, is typed into the file. F2 and F3 cannot degrade
// that way: a virtual-key code resolves before the ctrl/rune branch and
// carries no rune.
//
// ctrl+w and ctrl+q STAY. Removing them would turn every muscle-memory
// press into a guaranteed stray letter rather than an occasional one.

func TestBothLeaderKeysArmTheChord(t *testing.T) {
	for _, k := range []string{"f2", "ctrl+w"} {
		if !actions.IsLeader(k) {
			t.Errorf("actions.IsLeader(%q) is false", k)
		}
		r := newRoot(t)
		size(t, r, 120, 30)
		m, _ := r.Update(key(k))
		if !m.(*Root).leader.pending {
			t.Errorf("%s did not arm the leader", k)
		}
	}
	// And an ordinary letter does not.
	if actions.IsLeader("w") {
		t.Error("a bare w is treated as the leader")
	}
}

// Either leader key reaches the same action.
func TestEitherLeaderKeyRunsTheSameAction(t *testing.T) {
	for _, k := range []string{"f2", "ctrl+w"} {
		r := newRoot(t)
		size(t, r, 120, 30)
		r.Update(key(k))
		m, _ := r.Update(key("?")) // ui.help
		if r2 := m.(*Root); r2.overlay == nil {
			t.Errorf("%s then ? did not open Help", k)
		}
	}
}

// Both quit keys are bound, to quit.
func TestBothQuitKeysAreBound(t *testing.T) {
	r := newRoot(t)
	for _, chord := range []string{"f3", "ctrl+q"} {
		a, ok := r.actions.ByChord(chord)
		if !ok {
			t.Fatalf("%s is bound to nothing", chord)
		}
		if a.ID != "app.quit" {
			t.Errorf("%s is bound to %q, want app.quit", chord, a.ID)
		}
	}
}

// NEITHER key is a secret: every surface that names a binding names both.
//
// This is the half most likely to rot -- a second binding that works and
// is undocumented is a trap, which is the opposite of the problem it was
// added to solve.
func TestEveryBindingSurfaceNamesBothKeys(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	if !strings.Contains(actions.LeaderLabel, "F2") ||
		!strings.Contains(actions.LeaderLabel, "ctrl+w") {
		t.Fatalf("actions.LeaderLabel = %q, must name both leader keys", actions.LeaderLabel)
	}

	// "F3", upper-case, the way the key is engraved and the way
	// LeaderLabel has always spelled F2. ChordLabel printed the raw
	// bubbletea key name until F15's close, so this line read "f3 /
	// ctrl+q" next to "F2 / ctrl+w" one row above it in the same help
	// screen. actions.KeyLabel is the single place that decides it now.
	quit, _ := r.actions.ByID("app.quit")
	if label := quit.ChordLabel(); !strings.Contains(label, "F3") ||
		!strings.Contains(label, "ctrl+q") {
		t.Errorf("quit's ChordLabel = %q, must name both keys", label)
	}

	// The HELP screen.
	help := stripANSI(newHelpOverlay(r).View(r.theme, 120, 30))
	for _, want := range []string{"F3 / ctrl+q", "F2 / ctrl+w"} {
		if !strings.Contains(help, want) {
			t.Errorf("the help screen does not show %q:\n%s", want, help)
		}
	}

	// The LEADER HINT bar, after arming the chord.
	m, _ := r.Update(key("f2"))
	r2 := m.(*Root)
	r2.leader.showHint = true
	frame := stripANSI(r2.View())
	if !strings.Contains(frame, actions.LeaderLabel) {
		t.Errorf("the leader hint does not name both keys (%q):\n%s",
			actions.LeaderLabel, frame)
	}
}

// The palette's binding column is wide enough for the longest label it can
// render, measured rather than hardcoded -- so a future action with a
// longer binding widens the column instead of losing a key on screen.
func TestTheBindingColumnFitsTheLongestLabel(t *testing.T) {
	r := newRoot(t)
	widest := ""
	for _, a := range r.actions.All() {
		if l := bindingLabel(a); len(l) > len(widest) {
			widest = l
		}
	}
	if widest == "" {
		t.Fatal("no action has a binding")
	}
	p := newPaletteOverlay(r)
	w, h := p.MinSize()
	out := stripANSI(p.View(r.theme, w-2, h-2))
	// The widest label belongs to Quit, which sorts into the first
	// section, so it is on screen without scrolling.
	if !strings.Contains(out, widest) {
		t.Errorf("the longest binding %q is truncated at the palette's own MinSize:\n%s",
			widest, out)
	}
}

var _ = tea.KeyMsg{}

// No surface prints a bare function key in lower case.
//
// The bug this guards was not that any one label was wrong -- it was that
// TWO places decided the spelling. actions.LeaderLabel was hand-typed with
// "F2" while ChordLabel passed bubbletea's own key names ("f1", "f3")
// straight through, so the help screen showed both conventions in adjacent
// rows and the Editor's empty state printed "f5 f6 f7" underneath. Now
// actions.KeyLabel is the only place that decides, and this is what notices
// if a third place appears.
//
// Modifier chords are deliberately NOT covered: "ctrl+p" is lower-case
// everywhere in this app by choice, and KeyLabel leaves them alone.
func TestNoSurfacePrintsALowerCaseFunctionKey(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 40)

	// A bare fN, not preceded by a letter or a '+' (so "ctrl+f1" and any
	// word ending in f are not matches) and not followed by one.
	bare := regexp.MustCompile(`(^|[^a-zA-Z+])f[0-9]{1,2}($|[^a-zA-Z0-9])`)

	surfaces := map[string]string{
		"help screen": stripANSI(newHelpOverlay(r).View(r.theme, 120, 40)),
		"palette":     stripANSI(newPaletteOverlay(r).View(r.theme, 120, 40)),
	}
	for _, a := range r.actions.All() {
		if l := a.ChordLabel(); l != "" {
			surfaces["ChordLabel("+a.ID+")"] = l
		}
	}
	surfaces["actions.LeaderLabel"] = actions.LeaderLabel

	for name, text := range surfaces {
		for _, line := range strings.Split(text, "\n") {
			if bare.MatchString(line) {
				t.Errorf("%s prints a lower-case function key: %q", name, strings.TrimSpace(line))
			}
		}
	}
}
