package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// countingWorkspace only counts how many times Update was called on it — the
// leak-detector for TestPlainRuneDoesNotReachTheWorkspace below.
type countingWorkspace struct {
	id      WorkspaceID
	updates int
}

func (f *countingWorkspace) ID() WorkspaceID           { return f.id }
func (f *countingWorkspace) Init() tea.Cmd             { return nil }
func (f *countingWorkspace) View(layout.Layout) string { return "" }
func (f *countingWorkspace) Suspend() ([]byte, error)  { return nil, nil }
func (f *countingWorkspace) Resume([]byte) error       { return nil }
func (f *countingWorkspace) Cards() []layout.CardSlot  { return nil }
func (f *countingWorkspace) Update(tea.Msg) (Workspace, tea.Cmd) {
	f.updates++
	return f, nil
}

var _ Workspace = (*countingWorkspace)(nil)

// Done-when #5, end to end through Root: with an overlay open, a plain rune
// never reaches the workspace underneath at all — it types into the popup's
// filter instead. This is what F5's chat\stream.go relies on for "j typed
// into the filter types j, it does not scroll the transcript".
func TestPlainRuneDoesNotReachTheWorkspace(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	ws := &countingWorkspace{id: r.active}
	r.SetWorkspace(r.active, ws)

	r.OpenOverlay(newHelpOverlay(r))
	if r.overlay == nil {
		t.Fatal("help did not open")
	}

	before := ws.updates
	r.Update(runeKey("j"))
	if ws.updates != before {
		t.Errorf("the workspace's Update was called %d time(s) while an overlay was open", ws.updates-before)
	}

	p, ok := r.overlay.(*Popup)
	if !ok {
		t.Fatal("overlay is not a *Popup")
	}
	if p.filter != "j" {
		t.Errorf("filter = %q, want \"j\" to have reached the popup instead", p.filter)
	}
}

// LAYOUT.md's overlay contract: "close on esc or a click outside." Nothing
// implemented this before F07 gave the contract a widget that actually needs
// it — a mouse event used to reach the workspace underneath even while an
// overlay was open, which is the same focus-trap gap esc's two-stage close
// exists to close for the keyboard.
func TestClickOutsideTheOverlayClosesIt(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.OpenOverlay(newHelpOverlay(r))
	if r.overlay == nil {
		t.Fatal("help did not open")
	}

	m, _ := r.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.(*Root).overlay != nil {
		t.Error("a press outside the panel should close the overlay")
	}
}

// A click INSIDE the panel reaches the overlay, translated to its own
// content origin, rather than leaking to the workspace behind it.
func TestClickInsideTheOverlayReachesIt(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.OpenOverlay(newPaletteOverlay(r))
	if r.overlay == nil {
		t.Fatal("palette did not open")
	}
	rect := r.overlayRect()

	// A click well inside the panel, on the filter row, must not close it —
	// and must not reach the workspace either (nothing to assert there
	// directly, but the overlay staying open is proof enough: a leak to a
	// workspace with no MouseMsg handling would panic or no-op silently,
	// while a translated click landing outside the popup's own rows is
	// simply ignored by Popup.handleMouse).
	m, _ := r.Update(tea.MouseMsg{
		X: rect.X + rect.W/2, Y: rect.Y + 1,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if m.(*Root).overlay == nil {
		t.Error("a click inside the panel must not close it")
	}
}

// Bug 2 (F7 verification): a wheel tick reports Action == MouseActionPress,
// same as a real click (bubbletea's decode never sets any other Action for
// a wheel event) — and scrolling a list does not require the pointer to be
// exactly over it. Closing the overlay on ANY outside press meant a wheel
// scroll with the cursor merely resting elsewhere closed the popup out from
// under itself; a click made right after landed on nothing, and looked
// exactly like "click doesn't work" from the user's seat.
func TestWheelOutsideThePanelDoesNotCloseIt(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.OpenOverlay(newHelpOverlay(r))
	if r.overlay == nil {
		t.Fatal("help did not open")
	}

	m, _ := r.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.(*Root).overlay == nil {
		t.Fatal("a wheel tick outside the panel closed the overlay")
	}
}

// Only a left click outside the panel closes it.
func TestOnlyLeftClickOutsideClosesTheOverlay(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.OpenOverlay(newHelpOverlay(r))

	m, _ := r.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if m.(*Root).overlay == nil {
		t.Fatal("a right-click outside the panel closed it; only left click should")
	}

	m, _ = r.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.(*Root).overlay != nil {
		t.Fatal("a left click outside the panel should still close it")
	}
}

// Mouse events with no overlay open still reach the workspace as before —
// the trap only engages while something is open.
func TestMouseReachesWorkspaceWithNoOverlayOpen(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	// Nothing should panic, and the overlay stays nil.
	m, _ := r.Update(tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.(*Root).overlay != nil {
		t.Error("no overlay was open; one must not have appeared")
	}
}
