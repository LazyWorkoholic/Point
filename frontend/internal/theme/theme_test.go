package theme

import (
	"regexp"
	"strings"
	"testing"
)

// "Adding a fifth palette must be a data change, not a code change" is only
// true if every palette is complete. This is the check that keeps it true.
func TestEveryPaletteDefinesEveryRole(t *testing.T) {
	for _, name := range Names() {
		p, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names() lists %q but Lookup does not find it", name)
		}
		for _, r := range Roles() {
			v, ok := p.Colors[r]
			if !ok {
				t.Errorf("palette %q is missing role %q", name, r)
				continue
			}
			if !hexColor.MatchString(v) {
				t.Errorf("palette %q role %q = %q, not a #RRGGBB literal", name, r, v)
			}
		}
		if len(p.Colors) != len(Roles()) {
			t.Errorf("palette %q defines %d colours for %d roles — an extra entry is a role that no component can ask for",
				name, len(p.Colors), len(Roles()))
		}
	}
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func TestFourPalettes(t *testing.T) {
	want := []string{"turquoise", "butterfly", "green", "gold"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("palette %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Switching the palette must change every colour — if two palettes share a
// role's value, one of them was transcribed wrong.
func TestSwitchingPaletteChangesEveryColour(t *testing.T) {
	a := New("turquoise", BackgroundTransparent)
	for _, other := range []string{"butterfly", "green", "gold"} {
		b := New(other, BackgroundTransparent)
		for _, r := range Roles() {
			if r == Error || r == Warning {
				continue // fixed and outside the palette, by design
			}
			if a.Color(r) == b.Color(r) {
				t.Errorf("turquoise and %s share %q = %s", other, r, a.Color(r))
			}
		}
	}
}

// Error and warning ARE meant to be near-identical across palettes: a gold
// error is not readable as an error.
func TestErrorAndWarningAreOutsideThePalette(t *testing.T) {
	for _, other := range Names() {
		th := New(other, BackgroundTransparent)
		for _, r := range []Role{Error, Warning} {
			// Gold nudges them for legibility on a warm ground, so require
			// only that they stay in the red/amber family, not that they are
			// byte-identical.
			if !strings.HasPrefix(strings.ToUpper(string(th.Color(r))), "#E5") &&
				!strings.HasPrefix(strings.ToUpper(string(th.Color(r))), "#F5") {
				t.Errorf("palette %q role %q = %s, which is not a red/amber",
					other, r, th.Color(r))
			}
		}
	}
}

func TestUnknownPaletteFallsBackToDefault(t *testing.T) {
	th := New("chartreuse", BackgroundTransparent)
	if th.Name() != DefaultPalette {
		t.Fatalf("fell back to %q, want %q", th.Name(), DefaultPalette)
	}
}

func TestUnknownBackgroundFallsBackToTransparent(t *testing.T) {
	th := New(DefaultPalette, Background("mauve"))
	if th.Background() != BackgroundTransparent {
		t.Fatalf("background = %q, want transparent", th.Background())
	}
	if _, ok := th.BackgroundColor(); ok {
		t.Fatal("transparent mode painted a background")
	}
}

func TestBackgroundModes(t *testing.T) {
	if _, ok := New(DefaultPalette, BackgroundTransparent).BackgroundColor(); ok {
		t.Error("transparent should paint nothing")
	}
	if _, ok := New(DefaultPalette, BackgroundDark).BackgroundColor(); !ok {
		t.Error("dark should paint a background")
	}
	if _, ok := New(DefaultPalette, BackgroundLight).BackgroundColor(); !ok {
		t.Error("light should paint a background")
	}
}

// NO_COLOR drops to plain text with borders intact.
func TestNoColorDropsColourButKeepsBorders(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	th := New(DefaultPalette, BackgroundDark)

	if !th.NoColor() {
		t.Fatal("NO_COLOR not honoured")
	}
	if s := th.Style(TextPrimary).Render("x"); strings.Contains(s, "\x1b[") {
		t.Errorf("NO_COLOR still emitted an escape sequence: %q", s)
	}
	if th.Box(BorderFocus).GetBorderStyle().Top == "" {
		t.Error("NO_COLOR removed the border characters; only colour should go")
	}
	if _, ok := th.BackgroundColor(); ok {
		t.Error("NO_COLOR still painted a background")
	}
}

func TestRolesAreUnique(t *testing.T) {
	seen := map[Role]bool{}
	for _, r := range Roles() {
		if seen[r] {
			t.Errorf("role %q listed twice in Roles()", r)
		}
		seen[r] = true
	}
}
