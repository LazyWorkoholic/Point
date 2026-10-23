package app

import (
	"github.com/mattn/go-runewidth"

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

// MaximisedOverlay is an Overlay that wants room rather than a fixed size —
// the task tray and the result card (F9a), whose content is a list and a body
// of prose that both get strictly more readable with it.
//
// overlayRect treats a plain Overlay's MinSize as its ACTUAL size, shrinking
// it only when the terminal cannot fit it and never growing it. That is right
// for the palette and for Confirm, which are fixed-shape controls that would
// look absurd stretched across 200 columns. It is wrong for a content
// surface: F9a's tray shipped at LAYOUT.md's "task tray 50×12" and its own
// footer hint did not fit, while a result card at 60×18 wrapped prose into a
// column narrower than the pane it was floating over.
//
// # What "room" means, precisely
//
// The TRANSCRIPT PANE's own rect (layout.Layout.Main) less one cell per side
// — NOT the terminal's.
//
// Those differ, and the difference is the point. `Main` already has the right
// pane's columns, the header's rows, the status bar's rows and the query
// box's rows subtracted by Compute. An overlay sized to the terminal covers
// all of that; one sized to `Main` is the size of the thing a reader is
// already reading, which is the size their eye is calibrated to. It also
// tracks pane proportions for free: hide the right pane and `Main` widens,
// so the overlay widens with it, with no fraction-of-the-screen constant to
// drift out of step.
//
// It is still CENTRED in the whole terminal, not placed over `Main` — so the
// right pane and the status bar stay visible around it under the scrim.
// Position and size come from different places on purpose.
//
// PreferredSize is asked for that available rect and may return anything;
// overlayRect clamps the answer to [MinSize, the terminal less a margin], so
// an overlay can ask for "as much as you have" without knowing any of the
// rules.
type MaximisedOverlay interface {
	Overlay
	PreferredSize(availW, availH int) (w, h int)
}

// matchTranscriptPane is MaximisedOverlay's "all of what you offered" answer,
// shared by the tray and the result card so the two are the same size as each
// other — they swap back and forth (the card returns to the tray on d/esc)
// and a size change across that swap reads as the panel jumping.
func matchTranscriptPane(availW, availH int) (int, int) { return availW, availH }

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
		// The binding column asks for exactly what its longest label
		// needs, rather than taking rowWidths' 24-cell default.
		//
		// F15 gave the leader and quit a SECOND binding each, and both
		// are shown ("f3 / ctrl+q · F2 / ctrl+w q" is 27 cells), so the
		// default cut one mid-key. Widening the POPUP does not fix that
		// -- the cap is on the column, not the row -- and NoteW is the
		// lever rowWidths provides for exactly this, clamped there so
		// the name column keeps its 16 readable cells.
		//
		// Measured from the items rather than written down, so an action
		// with a longer binding widens the column instead of losing a
		// key on screen.
		NoteW: longestBinding(items),
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
		// See the palette's own note on why this is measured.
		NoteW: longestBinding(items),
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
		return fmt.Sprintf("%s · %s %s", a.ChordLabel(), actions.LeaderLabel, a.Leader)
	case a.Chord != "":
		return a.ChordLabel()
	case a.Leader != "":
		return fmt.Sprintf("%s %s", actions.LeaderLabel, a.Leader)
	default:
		return "—"
	}
}

// longestBinding is the widest binding label among these items, for a
// popup's NoteW.
//
// Zero when nothing has a binding, which is what rowWidths reads as "use
// the default" -- so a popup of plain rows is unaffected.
func longestBinding(items []*array.Item) int {
	widest := 0
	for _, it := range items {
		if w := runewidth.StringWidth(it.GetBinding()); w > widest {
			widest = w
		}
	}
	return widest
}
