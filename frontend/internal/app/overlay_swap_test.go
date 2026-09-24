package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// fakeSmallOverlay and fakeBigOverlay stand in for chat.rightPaneOverlay and
// UI-09's context composition card, which this package cannot import (chat
// already imports app — the same reason fakeRightPaneFallback exists in
// tasks_test.go). fakeSmallOverlay's MinSize mirrors rightPaneOverlay's real
// 36x20; fakeBigOverlay is deliberately larger than any terminal this test
// uses, so the swap it stands in for is guaranteed to be refused when that
// is what's being tested.
type fakeSmallOverlay struct{}

func (fakeSmallOverlay) MinSize() (int, int)               { return 36, 20 }
func (fakeSmallOverlay) Title() string                     { return "Right Pane" }
func (fakeSmallOverlay) View(theme.Theme, int, int) string { return "" }

// Update returns fakeBigOverlay on enter -- the same shape
// chat.rightPaneOverlay.Update uses to swap itself for UI-09, the first
// place in this codebase an Overlay's Update returns a DIFFERENT non-nil
// overlay rather than nil-to-close or itself-to-stay.
func (f fakeSmallOverlay) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyEnter {
		return fakeBigOverlay{}, nil
	}
	return f, nil
}

type fakeBigOverlay struct{}

func (fakeBigOverlay) MinSize() (int, int)                 { return 90, 30 }
func (fakeBigOverlay) Title() string                       { return "Context · 12.4k/32k" }
func (fakeBigOverlay) View(theme.Theme, int, int) string   { return "" }
func (f fakeBigOverlay) Update(tea.Msg) (Overlay, tea.Cmd) { return f, nil }

var _ Overlay = fakeSmallOverlay{}
var _ Overlay = fakeBigOverlay{}

// TestSelfReturnedOverlaySwapIsFitChecked is S13d's own regression coverage.
// Before root.setOverlay existed, dispatchKey and dispatchMouse both
// assigned whatever an Overlay's own Update returned UNCONDITIONALLY — never
// a problem while the only non-nil, non-self value any Overlay in this
// codebase ever returned was none. chat.rightPaneOverlay swapping itself for
// UI-09's composition card on enter (or a click on its own embedded Status
// section) is the first real case of an Update returning something ELSE,
// and it needs its own coverage rather than inherited confidence from
// OpenOverlay's existing tests, which only ever exercise a FRESH open, never
// a self-returned swap of an overlay already in place.
func TestSelfReturnedOverlaySwapIsFitChecked(t *testing.T) {
	r := newRoot(t)
	// 75x22: comfortably past OverlaysAllowed's 70-column floor (so the
	// refusal below is the WIDTH/HEIGHT branch of overlayFits, not the
	// global "overlays are off below 70" one) and fits fakeSmallOverlay's
	// 36x20 -- the same real numbers rightPaneOverlay's MinSize returns --
	// but well short of fakeBigOverlay's 90x30.
	size(t, r, 75, 22)
	r.overlay = fakeSmallOverlay{}

	m, _ := r.Update(key("enter"))
	root := m.(*Root)

	if _, ok := root.overlay.(fakeSmallOverlay); !ok {
		t.Fatalf("overlay = %#v after a refused swap, want the original fakeSmallOverlay still open (not closed, not the big one, not a crash)", root.overlay)
	}
	for _, want := range []string{"90×30", "75×22"} {
		if !strings.Contains(root.status, want) {
			t.Errorf("status %q does not name %s", root.status, want)
		}
	}
}

// The refusal test above proves setOverlay can say no; this proves it still
// says yes when a self-returned swap genuinely fits, so the fit check is not
// accidentally refusing every swap regardless of size.
func TestSelfReturnedOverlaySwapAppliesWhenItFits(t *testing.T) {
	r := newRoot(t)
	size(t, r, 100, 35)
	r.overlay = fakeSmallOverlay{}

	m, _ := r.Update(key("enter"))
	root := m.(*Root)

	if _, ok := root.overlay.(fakeBigOverlay); !ok {
		t.Fatalf("overlay = %#v, want the swap to have applied once the terminal is large enough", root.overlay)
	}
}
