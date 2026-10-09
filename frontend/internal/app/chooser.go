package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Chooser is UI-12, the startup workspace chooser.
//
// It exists because of one measurement F12 makes in words: loading the
// Editor — file tree, project context, open files — when the user only wants
// to ask a question wastes time and memory. So nothing is loaded until
// something is chosen, and `state.json`'s last_workspace is a PRE-SELECTION
// rather than an instruction.
//
// It is a button row rather than a list because it is a two-or-three-item
// choice made once per launch, and the shared Popup's filter box (F7's
// Done-when #5: every plain rune types into the filter) would turn the
// first key of a workspace's name into a search instead of a pick. The
// switcher below, which is a longer list browsed mid-session, IS a Popup for
// exactly the opposite reason.
type Chooser struct {
	rows  []WorkspaceStatus
	last  WorkspaceID
	focus int

	// question and hint are fields rather than literals inside View so
	// MinSize measures the SAME strings View draws. Two copies of a line,
	// one measured and one rendered, is how a box ends up one cell too
	// narrow for its own content.
	question string
	hint     string

	onPick func(WorkspaceID) tea.Cmd
}

// newChooser builds UI-12 from the registry, with the last-active workspace
// pre-selected.
func newChooser(root *Root) *Chooser {
	c := &Chooser{
		rows:     root.WorkspaceStatuses(root.now()),
		last:     WorkspaceID(root.lastWorkspace),
		question: "Resume previous session?",
		hint:     "←/→ move   ⏎ open   esc skip",
		onPick:   root.switchWorkspace,
	}
	for i, s := range c.rows {
		if s.Def.ID == c.last {
			c.focus = i
			break
		}
	}
	return c
}

func (c *Chooser) Title() string { return "Point" }

// MinSize is sized from the button row it actually has to draw, floored at
// UI-13's 40×7 so the narrowest case matches every other confirm-shaped
// box. Six content rows: the question, a blank, the buttons, a blank, the
// `last:` line and the key hint.
func (c *Chooser) MinSize() (int, int) {
	// Measured from EVERY line this box draws, not just the button row.
	//
	// It used to measure only the buttons, and the `last:` line is the
	// widest thing here: "last: Chat · position restored on open" is 39
	// cells against the 36 a 40-column box leaves, so it rendered as
	// "...restored on op" — found on the first run against the real app.
	// A box sized from one of its own rows and then asked to draw a wider
	// one has no way to report that; padCells simply cuts, silently and
	// with no ellipsis, which is why it reads as a typo rather than as an
	// overflow.
	//
	// Both button forms are label+4 cells wide ("[ x ]" and "  x  "), so
	// the row's width is exact without rendering anything, and MinSize
	// never has to invent a Theme it was not given.
	buttons := 0
	for i, s := range c.rows {
		if i > 0 {
			buttons += runewidth.StringWidth(buttonGap)
		}
		buttons += runewidth.StringWidth(s.Def.Name) + 4
	}

	widest := buttons
	for _, line := range []string{c.question, c.lastLine(), c.hint} {
		if n := runewidth.StringWidth(line); n > widest {
			widest = n
		}
	}

	// +4: box.go's border (2) and fitCells' one cell of padding per side.
	w := widest + 4
	if w < 40 {
		w = 40 // LAYOUT.md's confirm floor, so the narrowest case matches
	}
	return w, 8
}

func (c *Chooser) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	switch k.Type {
	case tea.KeyLeft:
		if c.focus > 0 {
			c.focus--
		}
		return c, nil
	case tea.KeyRight:
		if c.focus < len(c.rows)-1 {
			c.focus++
		}
		return c, nil
	case tea.KeyTab:
		if len(c.rows) > 0 {
			c.focus = (c.focus + 1) % len(c.rows)
		}
		return c, nil
	case tea.KeyEnter:
		if c.focus < 0 || c.focus >= len(c.rows) {
			return nil, nil
		}
		return nil, c.onPick(c.rows[c.focus].Def.ID)
	case tea.KeyEsc:
		// Dismissable, and nothing is loaded afterwards. The chooser is
		// not a modal gate — it is an offer — and the diagnostics screen
		// behind it is a real, useful state (it is what F1 shipped, and
		// it is where the connection banner and the protocol errors
		// live). Root latches chooserShown so this does not immediately
		// reopen; leader → w brings it back.
		return nil, nil
	}
	return c, nil
}

func (c *Chooser) View(th theme.Theme, w, h int) string {
	textW := w - 2 // popup.go's View doc: box.go's fitCells pads one more cell each side
	if textW < 1 {
		textW = 1
	}
	line := func(s string, role theme.Role) string {
		return th.Style(role).Render(padCells(s, textW))
	}

	var b strings.Builder
	b.WriteString(line(c.question, theme.TextPrimary))
	b.WriteString("\n")
	b.WriteString(line("", theme.TextPrimary))
	b.WriteString("\n")
	b.WriteString(centre(c.buttons(th), textW))
	b.WriteString("\n")
	b.WriteString(line("", theme.TextPrimary))
	b.WriteString("\n")
	b.WriteString(line(c.lastLine(), theme.TextSecondary))
	b.WriteString("\n")
	b.WriteString(line(c.hint, theme.TextDim))
	return b.String()
}

// buttons draws one bracketed button per registered workspace, using
// Confirm's own focus convention — the accent foreground, never a filled
// background — so the two look like the same kind of control. Both forms are
// the same width ("[ x ]" and "  x  "), which is what stops the row shifting
// as the focus moves.
func (c *Chooser) buttons(th theme.Theme) string {
	parts := make([]string, 0, len(c.rows))
	for i, s := range c.rows {
		label := s.Def.Name
		if i == c.focus {
			parts = append(parts, th.Style(theme.AccentPrimary).Render("[ "+label+" ]"))
			continue
		}
		parts = append(parts, th.Style(theme.TextSecondary).Render("  "+label+"  "))
	}
	return strings.Join(parts, buttonGap)
}

// lastLine is UI-12's `last:` row.
//
// It names the workspace and nothing else. F12's mockup also shows a session
// title and an age — `last: Chat · "runtime bug" · 2h ago` — and neither is
// available at the moment this box opens: the session list arrives over the
// wire, after the backend handshake, and the chooser's whole point is to be
// the first thing on screen. Showing a title that filled in a second later,
// or an age computed from nothing, would be worse than showing neither.
// Recorded as a shortfall rather than faked.
func (c *Chooser) lastLine() string {
	if c.last == "" {
		return "nothing opened here before"
	}
	for _, s := range c.rows {
		if s.Def.ID == c.last {
			if s.Suspended {
				return "last: " + s.Def.Name + " · position restored on open"
			}
			return "last: " + s.Def.Name
		}
	}
	return "last: " + string(c.last)
}

// ---------------------------------------------------------------------------
// The switcher (leader → w)
// ---------------------------------------------------------------------------

// newSwitcherOverlay is F12's workspace switcher: every registered
// workspace, its lifecycle state, and the live idle readout —
// `Chat · active`, `Editor · idle 3m0s of 5m0s`, `Data · not loaded`.
//
// The readout is one of the two affordances F12 requires alongside the idle
// timer, "or unloading feels like the app losing your work at random". It is
// computed here, at open time, from WorkspaceStatuses — the same derivation
// the chooser reads, so the two cannot describe one workspace two different
// ways.
//
// A Popup rather than a button row, unlike the chooser: this is a list
// browsed mid-session, it grows with the parked capability workspaces, and
// it wants the filter box the chooser specifically cannot have.
func newSwitcherOverlay(root *Root) *workspaceSwitcher {
	rows := root.WorkspaceStatuses(root.now())
	notes := make(map[string]string, len(rows))
	icons := make(map[string]WorkspaceStatus, len(rows))
	items := make([]*array.Item, 0, len(rows))
	for _, s := range rows {
		// The readout ALONE, with no leader key in front of it.
		//
		// rowWidths caps the note column at 24 cells and at a third of the
		// row, so "ctrl+w e · idle 59m59s of 59m59s" cannot fit at any
		// width this popup will ever be given, and prefixing it would cost
		// the thing the column exists for. Bindings already have a home:
		// the palette and Help both render them from the same registry via
		// bindingLabel.
		notes[string(s.Def.ID)] = s.Label()
		icons[string(s.Def.ID)] = s
		items = append(items, array.NewItem(string(s.Def.ID), s.Def.Name).
			SetDescription(s.Label()))
	}
	return &workspaceSwitcher{rows: len(rows), Popup: NewPopup(PopupConfig{
		Title: "Workspaces",
		Tabs:  []PopupTab{{Items: items}},
		// LAYOUT.md's "task tray 50×12" as the refusal FLOOR; the real
		// width comes from PreferredSize below.
		MinW: 50, MinH: 12,
		// Decor, not SetBinding/SetDescription, and this is the whole
		// reason the readout was invisible on first verification.
		// renderRow draws exactly two columns — the title and the NOTE —
		// and the description column was dropped from Popup outright
		// (see its own doc on why it was not chased with more width).
		// So SetDescription("active") rendered nowhere at all and the
		// row showed its binding, which for the one workspace that
		// exists is "—". Decor is the supported way to supply a note
		// computed at render time, which is also what F9a's tray uses
		// for the identical reason: this value changes while the popup
		// is open.
		Decor: func(it *array.Item) RowDecor {
			s := icons[it.GetID()]
			d := RowDecor{Note: notes[it.GetID()]}
			switch {
			case s.Active:
				d.Icon, d.IconRole = "●", theme.AccentPrimary
			case s.Loaded:
				d.Icon, d.IconRole = "○", theme.TextSecondary
			default:
				d.Icon, d.IconRole = "·", theme.TextDim
			}
			return d
		},
		OnSelect: func(it *array.Item) (tea.Cmd, bool) {
			return root.switchWorkspace(WorkspaceID(it.GetID())), true
		},
	})}
}

// switcherWidth is what the switcher asks for when there is room.
//
// rowWidths gives the note column min(24, avail/3) cells, and avail is the
// box width less the marker column, the icon and the border -- so the
// longest readout this popup can produce, "idle 59m59s of 59m59s" at 21
// cells, needs a 70-cell box before the column is wide enough to hold it.
// At the 50-cell floor it rendered "idle 1m30s of…", which is honest (it
// elides) but is the one value the popup exists to show.
//
// 72, not "whatever is offered" like the tray: this is a list of two or
// three rows, and a three-row list centred in a 200-column box reads as a
// panel that lost its contents. A couple of cells of slack above 70 so a
// longer timeout than an hour does not immediately start eliding again.
const switcherWidth = 72

// workspaceSwitcher is the switcher's preferred-vs-minimum wrapper.
//
// Same shape as F9a's taskTray, including the Update forwarding, and for
// the same two reasons: MinSize has to stay the refusal threshold
// (F06-right-pane.md's own "Known limitation" note), and a Popup that
// returned the bare *Popup from its own Update would silently drop this
// type -- and with it the preferred size -- at the first keystroke.
type workspaceSwitcher struct {
	*Popup
	// rows is how many workspaces were registered when this was built --
	// captured rather than asked of the Popup, which has no accessor for
	// it and does not need one.
	rows int
}

func (w *workspaceSwitcher) PreferredSize(availW, availH int) (int, int) {
	width := switcherWidth
	if availW < width {
		width = availW
	}
	// Tall enough for every row plus the popup's own filter line, blank
	// separators and footer, and no taller: see switcherWidth.
	height := w.rows + 6
	if height > availH {
		height = availH
	}
	return width, height
}

func (w *workspaceSwitcher) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	next, cmd := w.Popup.Update(msg)
	if next == nil {
		return nil, cmd
	}
	if p, ok := next.(*Popup); ok && p == w.Popup {
		return w, cmd
	}
	return next, cmd
}
