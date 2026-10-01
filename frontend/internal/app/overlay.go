package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/array"
)

// Overlay minimum sizes, from LAYOUT.md:
//
//	palette 40×10 · memory 60×16 · settings 60×20 · task tray 50×12
//	diff card 70×20 · result card 60×18 · confirm 40×7
//
// F07 builds the shared Popup and Confirm these sizes belong to (popup.go,
// confirm.go) and its first two consumers below. Help is not in LAYOUT.md's
// table; F1's placeholder chose 60×14 so its own text had room to read.
//
// Both are now 70×16, raised twice across verification: first to 60×14 to
// match each other, then to 70×16 because 60 columns was still forcing a
// description column down to single digits (renderRow's own doc explains
// why the description column was dropped instead of chased with more
// width). 70 also lines up with layout.OverlayFloor — the terminal width
// below which overlays refuse outright — so nothing between "too narrow for
// any overlay" and "fits this one" is left to check separately.

// newPaletteOverlay is the real command palette (UI-01), reading the F1
// action registry — the same registry the leader hint bar and the status
// bar's binding hints already read, so a feature added there needs no
// second registration anywhere near this file.
func newPaletteOverlay(root *Root) *Popup {
	items := make([]*array.Item, 0, root.actions.Len())
	for _, a := range root.actions.All() {
		items = append(items, array.NewItem(a.ID, a.Name).
			SetDescription(a.Description).
			SetBinding(bindingLabel(a)).
			SetDisabled(!a.Available(root)))
	}

	return NewPopup(PopupConfig{
		Title: "Command Palette",
		Tabs:  []PopupTab{{Items: items}},
		MinW:  70, MinH: 16, // matches Help's size — see the file doc above
		OnSelect: func(it *array.Item) (tea.Cmd, bool) {
			a, ok := root.actions.ByID(it.GetID())
			if !ok {
				return nil, true
			}
			if !a.Available(root) {
				root.status = a.Name + " is not available right now"
				return nil, true
			}
			return a.Run(root), true
		},
	})
}

// newHelpOverlay is the help screen (UI-11): every action, its binding and
// what it does, grouped by category — generated from the registry, never a
// hand-maintained list that drifts on the first rename.
func newHelpOverlay(root *Root) *Popup {
	items := make([]*array.Item, 0, root.actions.Len())
	for _, a := range root.actions.All() {
		items = append(items, array.NewItem(a.ID, a.Name).
			SetDescription(a.Description).
			SetBinding(bindingLabel(a)).
			SetSection(a.Category).
			SetDisabled(!a.Available(root)))
	}

	return NewPopup(PopupConfig{
		Title: "Help",
		Tabs:  []PopupTab{{Items: items}},
		MinW:  70, MinH: 16,
		// No OnSelect: browsing help has nothing to run. Enter on a row
		// therefore does nothing; esc is how you leave.
	})
}

// bindingLabel renders an action's binding the way the old hand-written help
// screen did — "chord · leader sequence" when both exist, one or the other
// otherwise, "—" for neither. Shared by the palette and help so the two
// consumers can never drift into describing the same action two different
// ways.
func bindingLabel(a actions.Action[*Root]) string {
	switch {
	case a.Chord != "" && a.Leader != "":
		return fmt.Sprintf("%s · %s %s", a.Chord, actions.Leader, a.Leader)
	case a.Chord != "":
		return a.Chord
	case a.Leader != "":
		return fmt.Sprintf("%s %s", actions.Leader, a.Leader)
	default:
		return "—"
	}
}
