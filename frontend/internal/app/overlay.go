package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Overlay minimum sizes, from LAYOUT.md. F7 onwards each overlay declares its
// own; these two are what F1 and F2 actually open.
//
//	palette 40×10 · memory 60×16 · settings 60×20 · task tray 50×12
//	diff card 70×20 · result card 60×18 · confirm 40×7

// pendingOverlay stands in for something a later episode owns. It renders
// what it is, not a fake version of what it will be.
type pendingOverlay struct {
	title string
	w, h  int
	body  string
}

func (o pendingOverlay) MinSize() (int, int) { return o.w, o.h }
func (o pendingOverlay) Title() string       { return o.title }

func (o pendingOverlay) View(th theme.Theme, w, h int) string {
	lines := strings.Split(o.body, "\n")
	for i, l := range lines {
		lines[i] = th.Style(theme.TextSecondary).Render(l)
	}
	return strings.Join(lines, "\n")
}

func (o pendingOverlay) Update(tea.Msg) (Overlay, tea.Cmd) {
	return o, nil
}

// helpOverlay is the help screen, and it is a registry reader — which is the
// point. It hardcodes nothing: an action registered in a later episode appears
// here the moment it is registered.
type helpOverlay struct{ root *Root }

func (o helpOverlay) MinSize() (int, int) { return 60, 14 }
func (o helpOverlay) Title() string       { return "Help" }

func (o helpOverlay) Update(tea.Msg) (Overlay, tea.Cmd) { return o, nil }

// View renders through theme ROLES, like every other component.
//
// It used to return bare text, which meant the help screen rendered in the
// terminal's own default foreground and was the one part of the app a palette
// could not repaint. A component that returns unstyled text is not
// theme-neutral — it is hardcoded to whatever the terminal happens to use.
func (o helpOverlay) View(th theme.Theme, w, h int) string {
	var (
		b        strings.Builder
		category string
	)

	heading := th.Style(theme.AccentPrimary)
	name := th.Style(theme.TextPrimary)
	binding := th.Style(theme.AccentSecondary)
	desc := th.Style(theme.TextDim)

	for _, a := range o.root.actions.All() {
		if a.Category != category {
			if category != "" {
				b.WriteString("\n")
			}
			category = a.Category
			b.WriteString(heading.Render(category))
			b.WriteByte('\n')
		}

		bind := "—"
		switch {
		case a.Chord != "" && a.Leader != "":
			bind = fmt.Sprintf("%s · %s %s", a.Chord, actions.Leader, a.Leader)
		case a.Chord != "":
			bind = a.Chord
		case a.Leader != "":
			bind = fmt.Sprintf("%s %s", actions.Leader, a.Leader)
		}

		// Padded BEFORE styling, and the padding is still done by padCells
		// rather than by %-20s: an escape sequence has no display width, so a
		// width verb lines the columns up against the invisible bytes instead
		// of against the visible ones. Writing the pieces straight into the
		// builder rather than concatenating them changes nothing about that
		// order — only the allocations.
		b.WriteString("  ")
		b.WriteString(name.Render(padCells(a.Name, 20)))
		b.WriteByte(' ')
		b.WriteString(binding.Render(padCells(bind, 18)))
		b.WriteByte(' ')
		b.WriteString(desc.Render(a.Description))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	b.WriteString(desc.Render("esc to close"))
	return b.String()
}
