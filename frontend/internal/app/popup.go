package app

import (
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Popup is F07-popup-framework.md's shared widget: the command palette, the
// help screen, and every later tab-scoped filterable overlay (memory,
// settings, the @ picker, the task tray) are this ONE type with different
// data. array.Menu holds the selection/scroll/filter state; Popup adds tabs,
// chrome, per-item actions and the fit check, and does all of the rendering
// — Menu is deliberately headless (see array\menu.go's doc), so a matched
// character can be coloured in the accent role and a selected row can carry
// a Fill, neither of which a package outside `theme` is allowed to know how
// to do.
type Popup struct {
	cfg  PopupConfig
	menu array.Menu

	active int // index into cfg.Tabs
	filter string

	// geom is the geometry View actually drew, recorded for handleMouse.
	//
	// # Why this is recorded and not re-derived
	//
	// rowOffsets shares the TOP of the list between View and handleMouse,
	// and that was enough while every line below it was a list row. It is
	// not: the hint line, the footer, and any blank tail below the last row
	// all sit at y >= listTop, and handleMouse treated the whole half-plane
	// as list rows. Clicking the footer at the top of a 22-task list
	// selected and OPENED a task seven rows below the window.
	//
	// It cannot be re-derived either, which is the structural half of the
	// bug: how many row lines were drawn depends on the box HEIGHT, and
	// handleMouse never receives one — only View does. So the choice is a
	// recorded value or a second, independent, drifting derivation. One
	// writer (View), one reader (handleMouse), written every frame before
	// any mouse event can target what that frame drew.
	//
	// Same lesson as stickyHeader's "one helper, two callers", applied to a
	// value only one of the two callers can compute.
	geom popupGeom
}

// popupGeom is what one render of the list actually put on screen.
type popupGeom struct {
	// drawn is true once View has run at least once. Before that there is
	// nothing on screen, so no click can be in bounds.
	drawn bool
	// rowsTop is the first screen line that holds a LIST ROW — one below
	// listTop when a pinned heading is drawn, listTop itself otherwise.
	rowsTop int
	// rowCount is how many row lines were drawn, which is NOT listHeight:
	// at the end of a list, or with a pinned heading standing in for the
	// first row, fewer rows are drawn than there is room for.
	rowCount int
	// dropped is stickyHeader's own value at render time, which the click
	// offset has to add back to reach the menu's own indexing.
	dropped int
}

// PopupTab is one tab's item set. A Popup with zero or one tab draws no tab
// row at all — "Tabs []Tab // optional; empty = single list" from the spec's
// own pseudocode.
type PopupTab struct {
	Label string
	Items []*array.Item
}

// ItemAction is a per-item key, e.g. "a" for accept, "d" for delete.
//
// Key must be a KEY NAME a to bubbletea's tea.KeyMsg.String() other than a
// bare printable rune: the filter is always live (F07's own rule), so any
// plain letter always types into it. "ctrl+d", "delete", "alt+a" and similar
// chords are the only keys that can reach here at all — see Popup.Update.
type ItemAction struct {
	Key   string
	Label string
	When  func(*array.Item) bool
	Run   func(*array.Item) tea.Cmd
}

// RowDecor is per-row chrome a consumer supplies, computed fresh on every
// render.
//
// # Why this is a func and not fields on array.Item
//
// Exactly the reason PopupConfig.Hint is a func: "it is called during render,
// so what it returns is always current with whatever state it reads... A
// stored string would be a second copy of the mode to keep in sync; a func
// cannot go stale."
//
// F9a learned that the hard way. The tray put its state glyph in the item's
// TITLE and its elapsed clock in the item's BINDING — both baked in at
// trayItems() time. A tick that redrew the frame re-rendered the identical
// stored strings, so a running task's m:ss clock sat frozen at whatever it
// read when the items were last built: 0:00 on open, and only ever moving
// when something REBUILT the items (reopening the tray, or the task changing
// state). Scheduling a redraw cannot fix a value that was computed before the
// redraw existed.
//
// The glyph had the matching problem in the other dimension: inside the
// title, Popup rendered it with the title's own role (TextPrimary), so a
// done/failed task's ✓/✕ came out uncoloured in the tray while the right-pane
// Task card — which composes its own line and applies app.TaskRole — coloured
// it. One shared TaskGlyph, two different colours, which is precisely what
// consolidating the glyph was supposed to make impossible. The glyph has to
// be handed over as a glyph, with its role, not smuggled inside text.
type RowDecor struct {
	// Icon is a one-or-two-cell marker drawn before the name, with IconRole
	// as its colour. Empty means none.
	Icon     string
	IconRole theme.Role
	// Note is the flush-right column. When Decor is set this REPLACES
	// array.Item's binding, which stays the right answer for the palette
	// and Help (a key binding genuinely is fixed for the life of the item).
	Note string
}

// PopupConfig is the data a consumer supplies. It is intentionally close to
// F07-popup-framework.md's own pseudocode (Title, Tabs, Items, OnSelect,
// Actions, Min) — the two differences are that Items folds into a one-tab
// Tabs entry (one code path for both shapes) and Min is two ints instead of
// a Size type this package has no other use for.
type PopupConfig struct {
	Title string
	Tabs  []PopupTab // len(Tabs) <= 1 draws no tab row

	// OnSelect fires when an item is chosen (Enter, or a click) and is not
	// disabled. nil means there is nothing TO choose — Help passes nil
	// because browsing an entry has nothing further to run — and
	// Enter/click on a row is then a no-op that leaves the popup open, not
	// a close with nothing behind it.
	//
	// The second return value says whether choosing this item closes the
	// popup. Every consumer before F8 (palette, help) always wants true —
	// choosing a palette action or browsing help both end the interaction.
	// F8's @ picker is the first that sometimes wants false: selecting a
	// directory in browse mode has to fetch that directory's listing and
	// show it in the SAME popup, not close and reopen one. See SetItems.
	OnSelect func(*array.Item) (tea.Cmd, bool)

	Actions []ItemAction

	// Decor, if set, supplies each row's icon and right-hand note at render
	// time instead of reading them off the item. nil (the palette, Help,
	// F8's picker) keeps the previous behaviour exactly: no icon, and the
	// item's own binding as the note. See RowDecor for why it is a func.
	Decor func(*array.Item) RowDecor

	// StickySections pins the current section's label to the top of the
	// list, so rows are never shown without the heading they belong to.
	//
	// false (the palette, Help, F8's picker) renders exactly as before.
	//
	// # Why this is not optional for a long sectioned list
	//
	// array.Menu's section headings are ORDINARY ROWS in the same scrollable
	// slice as the items (menu.go's sectionedRows), and ensureVisible scrolls
	// the minimum distance needed to show the selected ITEM. Nothing relates
	// a heading to the rows beneath it. So a heading one row above the
	// viewport's top edge is simply gone while every row it labels is on
	// screen and selectable -- which is reachable in the most ordinary way
	// there is: scroll down, then come back up. Reproduced with 22 tasks at
	// a 14-row box: selecting the Running task put the cursor exactly on it
	// and left "Running" one line above the window, so the only running task
	// in the list rendered with no heading at all.
	//
	// It is the same shape as the two bugs before it in this episode -- a
	// value fixed when the list was BUILT and never reconsidered at render
	// time (the m:ss clock, then the glyph's colour). The fix is the same
	// shape too: compute it per render from what is actually visible.
	StickySections bool

	// QueryFor, if set, computes the string actually handed to the menu's
	// fuzzy filter from the raw typed filter text. nil (every consumer
	// before F8) means "use the typed text as-is". F8's picker needs this
	// in filesystem-browse mode: the filter box shows a literal path being
	// typed (`D:\Projects\Poi`), but fuzzy-matching that WHOLE string
	// against an entry name like "Point" would never match — only the
	// path's own leaf segment (the part after the last separator) is a
	// filter over the current directory's entries at all.
	QueryFor func(raw string) string

	// OnFilterChange, if set, fires whenever the raw typed filter text
	// changes, before QueryFor runs. nil (every consumer before F8) means
	// nothing extra happens. F8's picker uses this to notice the typed
	// text has grown a new path segment (a `\` or `/` just typed) and
	// fetch that directory's listing — the filter box is the only place
	// that growing path is typed, so this is the only hook that can see it
	// change.
	OnFilterChange func(raw string) tea.Cmd

	// Hint, if set, supplies one dim line of consumer-specific hints,
	// rendered directly above the key-binding footer. nil (the palette,
	// Help) reserves no row at all, so nothing about those changes.
	//
	// A FUNC, not a string, deliberately: it is called during render, so
	// what it returns is always current with whatever state it reads.
	// F8's picker needs that — '?' is a command in fuzzy mode but
	// ordinary text once browsing, and F04's Bug 12 is this codebase's
	// standing rule that a hint must never advertise a binding that does
	// not currently work. A stored string would be a second copy of the
	// mode to keep in sync; a func cannot go stale.
	Hint func() string

	MinW, MinH int

	// NoteW is the width the note column wants, in cells. Zero takes
	// rowWidths' default.
	//
	// The default caps the note at a third of the row, which is right
	// for a popup whose NAMES are the content: the palette's rows are
	// action names with a short binding beside them, and a note that
	// grew would eat the thing the reader is scanning. It is wrong for
	// the workspace switcher, where the names are "Chat" and "Editor"
	// and the readout IS the content -- `idle 1m00s of 1m00s · pinned`
	// is 28 cells against a 24-cell ceiling, so the pinned label was
	// truncated away entirely and the row rendered `idle 1m00s of 1m00s
	// …`. Measured, not inferred: rowWidths(86) still answers 24,
	// because the 24-cell cap binds before the third-of-the-row rule
	// does, so no amount of extra popup width could have fixed it.
	NoteW int
}

// NewPopup builds a Popup and loads its first tab.
func NewPopup(cfg PopupConfig) *Popup {
	if len(cfg.Tabs) == 0 {
		cfg.Tabs = []PopupTab{{}}
	}
	p := &Popup{cfg: cfg, menu: array.NewMenu()}
	p.menu.SetItems(p.cfg.Tabs[p.active].Items)
	return p
}

func (p *Popup) MinSize() (int, int) { return p.cfg.MinW, p.cfg.MinH }
func (p *Popup) Title() string       { return p.cfg.Title }

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update returns (nil, cmd) to signal "close me" — Root's dispatch already
// treats a nil Overlay as the case an overlay closing itself, the same path
// esc used to take unconditionally. See root.go's dispatchKey.
func (p *Popup) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return p.handleKey(m)
	case tea.MouseMsg:
		return p.handleMouse(m)
	}
	return p, nil
}

func (p *Popup) handleKey(m tea.KeyMsg) (Overlay, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		// Clears a non-empty filter first, closes on the second press —
		// F07's own rule, and Done-when #6.
		if p.filter != "" {
			return p, p.setFilter("")
		}
		return nil, nil

	case tea.KeyUp:
		p.menu.MoveUp()
		return p, nil
	case tea.KeyDown:
		p.menu.MoveDown()
		return p, nil
	case tea.KeyPgUp:
		p.menu.PageUp()
		return p, nil
	case tea.KeyPgDown:
		p.menu.PageDown()
		return p, nil
	case tea.KeyHome:
		p.menu.Top()
		return p, nil
	case tea.KeyEnd:
		p.menu.Bottom()
		return p, nil

	case tea.KeyTab:
		p.switchTab(1)
		return p, nil
	case tea.KeyShiftTab:
		p.switchTab(-1)
		return p, nil

	case tea.KeyEnter:
		return p.chooseSelected()

	case tea.KeyBackspace:
		if p.filter != "" {
			r := []rune(p.filter)
			return p, p.setFilter(string(r[:len(r)-1]))
		}
		return p, nil

	case tea.KeySpace:
		return p, p.setFilter(p.filter + " ")

	case tea.KeyRunes:
		// The filter is ALWAYS live: a plain rune types into it rather than
		// moving the selection. This is what keeps `j` typed here from
		// reading as "scroll" — Done-when #5, and the reason this Popup does
		// not bind j/k for movement the way the old menu.go did.
		//
		// insertableRunes guards against a bare modifier keydown/up (Ctrl,
		// Alt, Win, Caps Lock alone) — bubbletea's Windows reader has no
		// named case for these and falls back to KeyRunes with Runes set to
		// the raw, often-zero e.Char, which looks exactly like typed text.
		// chat\input.go's filterInsertable is the original fix for the
		// query input; the popup's filter is a new surface it never
		// reached, so it needs the identical rejection, not a new one.
		return p, p.setFilter(p.filter + insertableRunes(m.Runes))
	}

	// Anything else is a chord (ctrl+_, alt+_, function keys, …) — the only
	// shapes an ItemAction.Key can be, precisely because every plain rune is
	// claimed by the filter above.
	if it, ok := p.menu.Selected(); ok {
		for _, a := range p.cfg.Actions {
			if a.Key != m.String() {
				continue
			}
			if a.When != nil && !a.When(it) {
				continue
			}
			if a.Run == nil {
				return p, nil
			}
			return p, a.Run(it)
		}
	}
	return p, nil
}

func (p *Popup) setFilter(q string) tea.Cmd {
	p.filter = q
	query := q
	if p.cfg.QueryFor != nil {
		query = p.cfg.QueryFor(q)
	}
	p.menu.Filter(query)
	if p.cfg.OnFilterChange != nil {
		return p.cfg.OnFilterChange(q)
	}
	return nil
}

// insertableRunes drops every rune that is not real typed/pasted content —
// the same rule chat\input.go's filterInsertable applies to the query
// input, reproduced here because that package cannot be imported from this
// one (chat already imports app; the reverse would cycle). Keep '\n'/'\t'
// plus anything unicode.IsPrint accepts; drop everything else, silently —
// which is what makes a bare Ctrl/Alt/Win/Caps-Lock keydown, decoded on
// Windows as a zero rune, invisible instead of typed.
func insertableRunes(runes []rune) string {
	clean := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			clean = append(clean, r)
		}
	}
	return string(clean)
}

func (p *Popup) switchTab(dir int) {
	if len(p.cfg.Tabs) <= 1 {
		return
	}
	p.active = (p.active + dir + len(p.cfg.Tabs)) % len(p.cfg.Tabs)
	// Filter persists across tabs — F07 leaves the choice to this episode
	// ("persist or reset — pick one"). Persisting is what keeps a query you
	// just typed from vanishing because you brushed `tab`.
	p.menu.SetItems(p.cfg.Tabs[p.active].Items)
	p.menu.Filter(p.filter)
}

func (p *Popup) chooseSelected() (Overlay, tea.Cmd) {
	it, ok := p.menu.Selected()
	if !ok {
		return p, nil
	}
	if it.IsDisabled() {
		return p, nil
	}
	if p.cfg.OnSelect == nil {
		// Nothing to run — Help is the case: browsing an entry has nothing
		// further to do, so Enter/click on a row is a no-op that leaves the
		// popup open for more browsing, not a close with nothing behind it.
		return p, nil
	}
	cmd, closes := p.cfg.OnSelect(it)
	if closes {
		return nil, cmd
	}
	return p, cmd
}

// SetItems replaces the active tab's items in place — F8's @ picker
// descending into a directory, or an initial async fetch (ListAgents/
// ListProjectFiles/ListDirectory) landing after the popup already opened
// with nothing to show yet.
//
// The filter always clears and the selection always resets to the top: a
// filter typed against the OLD item set (a different directory's contents)
// has no meaning against the new one, and there is no "same row" across two
// unrelated listings — leaving either would silently hide every new entry
// that does not happen to match old, stale filter text.
func (p *Popup) SetItems(items []*array.Item) {
	p.cfg.Tabs[p.active].Items = items
	p.filter = ""
	// array.Menu's own query is a SEPARATE field from p.filter — clearing
	// one without the other left the menu re-filtering the NEW items
	// against the OLD query text, which is how this looked "cleared" (the
	// filter box read empty) while the list underneath was actually still
	// empty too (nothing in a fresh directory listing matches a stale
	// query left over from the last one).
	p.menu.Filter("")
	p.menu.SetItems(items)
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

func (p *Popup) handleMouse(m tea.MouseMsg) (Overlay, tea.Cmd) {
	if m.Action == tea.MouseActionMotion {
		return p, nil
	}
	switch m.Button {
	case tea.MouseButtonWheelUp:
		p.menu.ScrollUp()
		return p, nil
	case tea.MouseButtonWheelDown:
		p.menu.ScrollDown()
		return p, nil
	}
	if m.Button != tea.MouseButtonLeft || m.Action != tea.MouseActionPress {
		return p, nil
	}

	tabsRow, filterRow, _ := p.rowOffsets()
	switch {
	case len(p.cfg.Tabs) > 1 && m.Y == tabsRow:
		if i, ok := tabAt(p.cfg.Tabs, m.X); ok {
			p.active = i
			p.menu.SetItems(p.cfg.Tabs[p.active].Items)
			p.menu.Filter(p.filter)
		}
		return p, nil
	case m.Y == filterRow:
		return p, nil
	}

	// ONLY the row lines this frame actually drew. The test used to be
	// `m.Y >= listTop`, an unbounded half-plane that swallowed the pinned
	// heading, the hint line, the footer and any blank tail -- every one of
	// which then resolved to a row offset and opened a task that was not on
	// screen. Three symptoms, one cause: the click handler had no idea where
	// the list ENDED, because only View knows the box height. See Popup.geom.
	g := p.geom
	if !g.drawn || m.Y < g.rowsTop || m.Y >= g.rowsTop+g.rowCount {
		return p, nil
	}
	if p.menu.ClickRow(m.Y - g.rowsTop + g.dropped) {
		// A click both selects AND chooses — "mouse click selects" in F07's
		// own words, and the fastest path is the one a mouse user expects:
		// one click, not a click then an Enter.
		return p.chooseSelected()
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// hasSections reports whether the active tab's items carry any section at
// all. Without one there is no heading to pin and the sticky row would be a
// blank line stealing a row from the list.
func (p *Popup) hasSections() bool {
	for _, it := range p.cfg.Tabs[p.active].Items {
		if it.GetSection() != "" {
			return true
		}
	}
	return false
}

// stickyActive: a pinned heading is drawn this frame.
//
// Suppressed under a filter, and that is a correctness requirement rather
// than a simplification: a filtered list is RANKED, prefix-matches first
// (menu.go's rankedRows), which interleaves sections and emits no headings at
// all. A single pinned label over interleaved sections would be actively
// wrong about where the reader is, which is worse than no label.
func (p *Popup) stickyActive() bool {
	return p.cfg.StickySections && p.filter == "" && p.hasSections()
}

// stickyHeader is the pinned label and how many leading rows of
// VisibleRows() it stands in for (0 or 1).
//
// ONE function, consulted by both View and handleMouse, for exactly the
// reason rowOffsets is: a click has to land on the row it visually sits on,
// and two independent derivations of "is a row being replaced this frame"
// disagree the moment either changes. Derived from menu state alone -- no
// theme, no width -- so it cannot drift between a render and a click that
// follows it.
//
// `dropped` is 1 when the first visible row is ITSELF the heading the sticky
// line would show. Rendering both would print the same label twice, so the
// in-list copy yields; the list then shows one fewer item, which costs a
// trailing blank line at the very top of the list and nothing else. The
// alternative -- suppressing the sticky line in that one position -- changes
// the list's height by a row as you scroll past it, and a viewport that
// changes size under a moving selection is a worse defect than a blank line
// below the last item.
func (p *Popup) stickyHeader() (label string, dropped int) {
	if !p.stickyActive() {
		return "", 0
	}
	rows := p.menu.VisibleRows()
	if len(rows) == 0 {
		return "", 0
	}
	switch rows[0].Kind {
	case array.RowHeader:
		return rows[0].Header, 1
	default:
		if rows[0].Item == nil {
			return "", 0
		}
		return rows[0].Item.GetSection(), 0
	}
}

// rowOffsets are the y-coordinates (relative to Popup's own content origin)
// of the tab row, the filter row, and the first list row. Both View and
// handleMouse compute them the same way, from the same inputs, so a click
// always lands on the row it visually sits on.
func (p *Popup) rowOffsets() (tabsRow, filterRow, listTop int) {
	y := 0
	if len(p.cfg.Tabs) > 1 {
		tabsRow = y
		y++
	} else {
		tabsRow = -1
	}
	filterRow = y
	y++ // filter
	y++ // blank separator, matching F07's own mockup
	listTop = y
	return
}

func (p *Popup) View(th theme.Theme, w, h int) string {
	// box.go's fitCells pads whatever Popup returns with one more cell of
	// space on each side before drawing it inside the border — the actual
	// text budget is w-2, not w. See popup.go's own tests for why this
	// matters: filling the full w would silently lose its last two cells.
	textW := w - 2
	if textW < 1 {
		textW = 1
	}

	// The footer's own text (the "12/40" scroll indicator) depends on how
	// many rows are visible, which depends on the footer's height, which is
	// why the row budget is fixed FIRST (a footer row is always reserved —
	// the move/select/close hints are never empty) and the footer's TEXT is
	// only read after SetHeight has run for this render. Computing it before
	// SetHeight used whatever height the PREVIOUS render left behind, which
	// is exactly how two renders of the same, unchanged Popup at the same
	// size produced two different indicators.
	_, _, listTop := p.rowOffsets()
	// The hint row (if a consumer supplies one) is reserved the same way
	// the footer is — BEFORE SetHeight, so the list never renders into a
	// row something else is about to occupy.
	hint := ""
	if p.cfg.Hint != nil {
		hint = p.cfg.Hint()
	}
	reserved := 1 // the footer, always
	if hint != "" {
		reserved++
	}
	// The pinned heading is reserved the same way, BEFORE SetHeight, so the
	// menu is never told it has a row the sticky line is about to occupy.
	// stickyActive reads only p.cfg and p.filter, neither of which SetHeight
	// can change, so asking before is safe.
	if p.stickyActive() {
		reserved++
	}
	listHeight := h - listTop - reserved
	if listHeight < 1 {
		listHeight = 1
	}
	p.menu.SetHeight(listHeight)
	footer := p.footerLine(th, textW)

	var b strings.Builder
	if len(p.cfg.Tabs) > 1 {
		b.WriteString(p.renderTabs(th, textW))
		b.WriteByte('\n')
	}
	b.WriteString(p.renderFilter(th, textW))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// When the pinned heading stands in for the first visible row, the list
	// would show one FEWER item than everywhere else in the same box --
	// visible as a blank line that appears above the footer at the top of
	// the list and vanishes as you scroll, with a tenth task filling it.
	// Asking the menu for one more row gives back the line the heading
	// took, so the number of tasks on screen is the same at every scroll
	// position. Done in two passes because `dropped` is a fact about the
	// window and the window is what is being chosen; it converges in one
	// step, since widening a window can only move the cursor up, which
	// cannot turn a leading header row into a non-header one.
	if _, dropped := p.stickyHeader(); dropped > 0 {
		p.menu.SetHeight(listHeight + dropped)
	}

	rows := p.menu.VisibleRows()
	selOffset := p.menu.SelectedOffset()
	sticky, dropped := p.stickyHeader()

	rowsTop := listTop
	// The pinned heading, drawn in the same role an in-list heading uses so
	// it reads as one -- a sticky header that looks different from the
	// headers it stands in for reads as a separate widget.
	if p.stickyActive() {
		b.WriteString(th.Style(theme.TextDim).Render(padCells(sticky, textW)))
		b.WriteByte('\n')
		rowsTop++
		if dropped > 0 {
			rows = rows[dropped:]
			// SelectedOffset is relative to the UNDROPPED slice.
			if selOffset >= 0 {
				selOffset -= dropped
			}
		}
	}

	drawn := len(rows)
	if drawn > listHeight {
		drawn = listHeight
	}
	// Recorded BEFORE the loop, from the same numbers the loop uses, so a
	// click can only ever resolve against rows this frame really drew.
	p.geom = popupGeom{drawn: true, rowsTop: rowsTop, rowCount: drawn, dropped: dropped}

	for i := 0; i < listHeight; i++ {
		if i < drawn {
			b.WriteString(p.renderRow(th, rows[i], i == selOffset, textW))
		}
		if i < listHeight-1 || hint != "" || footer != "" {
			b.WriteByte('\n')
		}
	}
	// Dim, and ABOVE the footer: subordinate to the key bindings, and in a
	// different region entirely from the list, so it never competes with
	// whatever the list is showing (F8's refusal notices and its
	// "still showing <path>" line both live in the list).
	if hint != "" {
		b.WriteString(padCells(th.Style(theme.TextDim).Render(
			runewidth.Truncate(hint, textW, "…")), textW))
		if footer != "" {
			b.WriteByte('\n')
		}
	}
	if footer != "" {
		b.WriteString(footer)
	}
	return b.String()
}

func (p *Popup) renderTabs(th theme.Theme, width int) string {
	var parts []string
	for i, t := range p.cfg.Tabs {
		label := " " + t.Label + " "
		if i == p.active {
			parts = append(parts, th.Fill(theme.SelectText, theme.SelectBg).Render(label))
		} else {
			parts = append(parts, th.Style(theme.TextSecondary).Render(label))
		}
	}
	return padCells(strings.Join(parts, "  "), width)
}

// tabAt is renderTabs' inverse: which tab (if any) covers display column x.
// It mirrors renderTabs' own layout exactly — " "+label+" " tabs joined by a
// 2-cell gap — so a click always resolves to the tab it looks like it hit.
func tabAt(tabs []PopupTab, x int) (int, bool) {
	col := 0
	for i, t := range tabs {
		w := visibleWidth(" " + t.Label + " ")
		if x >= col && x < col+w {
			return i, true
		}
		col += w + 2
	}
	return 0, false
}

func (p *Popup) renderFilter(th theme.Theme, width int) string {
	label := th.Style(theme.TextDim).Render("filter: ")
	text := th.Style(theme.TextPrimary).Render(p.filter + "▏")
	return padCells(label+text, width)
}

// footerLine always shows the position ("N/total"), not only once scrolling
// is needed — a verification finding: showing it conditionally made the
// palette and help disagree with each other for no reason a user could see,
// since whether scrolling happens to be needed depends on box height, not on
// anything about the list itself.
//
// The indicator is placed LAST and given its width FIRST: it is the one
// thing that must always be legible, so on a narrow popup the hints (which
// only repeat what the key bindings already say) are what gets truncated,
// never it. Before this, the palette's narrower box silently cut "1/10" down
// to "1/" every row but the last, where the truncated NUMERATOR alone
// ("10") happened to look like the full fraction.
func (p *Popup) footerLine(th theme.Theme, width int) string {
	hints := []string{"↑/↓ move"}
	// "⏎ select" implies Enter DOES something — true for the palette, false
	// for Help, which is browse-only (nil OnSelect). Leaving the hint in
	// unconditionally was a leftover contradiction: Enter and a click both
	// already did nothing on Help, but the footer kept promising an action
	// that was never there to invoke.
	if p.cfg.OnSelect != nil {
		hints = append(hints, "⏎ select")
	}
	hints = append(hints, "esc close")
	if it, ok := p.menu.Selected(); ok {
		for _, a := range p.cfg.Actions {
			if a.When != nil && !a.When(it) {
				continue
			}
			hints = append(hints, a.Key+" "+a.Label)
		}
	}
	left := strings.Join(hints, "   ")

	right := ""
	if p.menu.Len() > 0 {
		right = strconv.Itoa(p.menu.SelectedRank()) + "/" + strconv.Itoa(p.menu.Len())
	}
	if right == "" {
		return th.Style(theme.TextDim).Render(runewidth.Truncate(left, width, "…"))
	}

	rightW := runewidth.StringWidth(right)
	leftBudget := width - rightW - 3 // 3-cell gap between hints and the indicator
	if leftBudget < 0 {
		leftBudget = 0
	}
	left = runewidth.Truncate(left, leftBudget, "…")
	gap := width - runewidth.StringWidth(left) - rightW
	if gap < 1 {
		gap = 1
	}
	return th.Style(theme.TextDim).Render(left + strings.Repeat(" ", gap) + right)
}

// rowWidths caps the name and binding columns. There is no description
// column — see the doc on renderRow for why it was removed rather than
// shrunk further.
//
// These are TRUNCATION ceilings, not layout positions: with two columns
// left the row is composed flush-left/flush-right (renderRow) and the space
// between them is whatever is left over, so neither column needs a fixed
// x-position. The ceilings exist only so a pathologically long name can
// never grow far enough to collide with the binding.
func rowWidths(avail, want int) (nameW, bindW int) {
	const maxName, maxBind = 40, 24

	// minName is what the name column keeps whatever the note asked for.
	// It is the guard a declared NoteW is clamped against, and it is the
	// RIGHT invariant: "a note never takes more than a third of the row"
	// protects nothing a caller cares about, while "a name is still
	// readable" is the thing that would actually break. Measured while
	// fixing the switcher -- a third of the row answered 27 cells at a
	// 90-column popup, one short of the 28 its readout needs, and
	// chasing that with more popup width is the layout wagging the text.
	const minName = 16

	if want > 0 {
		bindW = want
		if room := avail - minName - 1; bindW > room {
			bindW = room
		}
	} else {
		// The DEFAULT, unchanged: a third of the row, capped. Right for
		// a popup whose names are the content -- the palette and Help --
		// where a note that grew would eat what the reader is scanning.
		bindW = maxBind
		if third := avail / 3; bindW > third {
			bindW = third
		}
	}
	if bindW < 1 {
		bindW = 1
	}
	nameW = avail - bindW - 1 // at least one cell of gap between them
	if nameW > maxName {
		nameW = maxName
	}
	if nameW < 1 {
		nameW = 1
	}
	return
}

// renderRow draws one item as name + binding — NOT name + binding +
// description.
//
// A verification finding: at any box width the registry's own longest
// description ("Stop the reply that is streaming, keeping what has arrived
// so far") either gets silently cut (the original bug — "Leave Point,
// restoring the terminal" read as "Leave Poin", no ellipsis, indistinguishable
// from a typo) or forces the popup to a width no terminal should be asked to
// spare. Growing the box cannot fix this in general: the registry is
// open-ended, so any width chosen today is one long description away from
// truncating again. Name and binding do not have this problem — the
// longest of each across the current registry ("Edit Queued Message", 20
// cells; "ctrl+q · ctrl+w q", 17 cells) fits inside the caps below with
// room to spare, so those two columns are guaranteed complete, not merely
// "usually" complete.
func (p *Popup) renderRow(th theme.Theme, row array.Row, selected bool, width int) string {
	if row.Kind == array.RowHeader {
		return th.Style(theme.TextDim).Render(padCells(row.Header, width))
	}

	it := row.Item

	// Decor is asked HERE, inside the render, which is the whole point of it
	// being a func — see RowDecor.
	var decor RowDecor
	if p.cfg.Decor != nil {
		decor = p.cfg.Decor(it)
	}
	icon := decor.Icon
	iconW := runewidth.StringWidth(icon)
	if iconW > 0 {
		iconW++ // the space after it
	}

	avail := width - 2 - iconW // the "  " marker column, then the icon
	if avail < 1 {
		avail = 1
	}
	nameW, bindW := rowWidths(avail, p.cfg.NoteW)

	// Name flush LEFT, note flush RIGHT, gap between them — the same
	// left/right composition footerLine uses. With the description column
	// gone there is no third thing to sit between them, and two columns
	// huddled together on the left with dead space trailing reads as a
	// layout that lost something rather than one built for two.
	name := runewidth.Truncate(it.GetTitle(), nameW, "…")
	note := it.GetBinding()
	if p.cfg.Decor != nil {
		note = decor.Note
	}
	note = runewidth.Truncate(note, bindW, "…")

	iconGap := ""
	if icon != "" {
		iconGap = " "
	}

	if selected {
		// THEME.md rule 6: a Fill is for "a selected list row" by name — the
		// whole row, plain text, carries it. Individual highlighted
		// characters would fight the fill for attention on the one row that
		// is already unambiguous.
		//
		// The icon is PLAIN here too, and not only by that convention: a
		// pre-styled icon inside a Fill would put its own foreground escape
		// (and its reset) in the middle of the fill's run, and the reset
		// terminates the fill for everything after it on the row. That is
		// the severed-escape failure this package has already been bitten
		// by twice (renderHighlighted's batching, spliceRow's column-exact
		// cutting), so the selected row's colour is the Fill's, entirely.
		plain := padCells("  "+icon+iconGap+name, width-runewidth.StringWidth(note)) + note
		return th.Fill(theme.SelectText, theme.SelectBg).Render(padCells(plain, width))
	}

	normal, hi := th.Style(theme.TextPrimary), th.Style(theme.AccentPrimary)
	noteStyle := th.Style(theme.AccentSecondary)
	iconStyle := th.Style(decor.IconRole)
	if it.IsDisabled() {
		dim := th.Style(theme.TextDim)
		normal, hi, noteStyle, iconStyle = dim, dim, dim, dim
	}

	left := "  "
	if icon != "" {
		// The icon AND its following space are styled as one run, which is
		// how chat\cards.go's task line composes the identical glyph
		// (app.TaskRole(state).Render(app.TaskGlyph(state) + " ")). Styling
		// only the glyph and emitting the space plain would render the same
		// pixels but produce different bytes, and the regression tests on
		// both sides compare against that one expression -- see
		// TestTrayRowGlyphIsStyledLikeTheTaskCards.
		left += iconStyle.Render(icon + iconGap)
	}
	left += renderHighlighted(name, row.Match.Highlights, runewidth.StringWidth(name), normal, hi)
	gap := width - visibleWidth(left) - runewidth.StringWidth(note)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + noteStyle.Render(note)
}

// renderHighlighted styles title up to width DISPLAY cells, colouring the
// rune indices in highlights with hi and everything else with normal. A cut
// character becomes "…" rather than being left half-drawn — box.go's own
// truncation discipline.
//
// Consecutive runes of the same style are batched into ONE Render() call,
// not one per rune. Styling every rune individually would still LOOK
// correct, but it wraps every character in its own escape sequence, and a
// plain substring search (strings.Contains, as TestHelpListsEveryAction
// does directly against styled output) can then never find the word again —
// "Quit" survives such a search only because it happens to be the one row
// selected by default and rendered as a single Fill()'d string instead.
func renderHighlighted(title string, highlights []int, width int, normal, hi lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	set := make(map[int]bool, len(highlights))
	for _, i := range highlights {
		set[i] = true
	}

	var b strings.Builder
	used := 0
	runes := []rune(title)

	var run strings.Builder
	runHi := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		style := normal
		if runHi {
			style = hi
		}
		b.WriteString(style.Render(run.String()))
		run.Reset()
	}

	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if used+rw > width {
			if used < width {
				flush()
				b.WriteString(normal.Render("…"))
				used++
			}
			break
		}
		isHi := set[i]
		if run.Len() > 0 && isHi != runHi {
			flush()
		}
		runHi = isHi
		run.WriteRune(r)
		used += rw
	}
	flush()
	for used < width {
		b.WriteByte(' ')
		used++
	}
	return b.String()
}

// RefreshItems replaces the active tab's items with a NEWER VIEW OF THE SAME
// LIST, preserving the typed filter and keeping the same item selected if it
// is still present.
//
// This is deliberately not SetItems, and the difference is the whole reason
// it exists. SetItems answers "show me something else" — F8's picker
// descending into a different directory — where a filter typed against the
// old contents is meaningless and there is no "same row" to keep, so both are
// discarded. RefreshItems answers "the same list, a moment later": F9's task
// tray re-rendering after a task finished. Discarding the filter there would
// wipe what the user is in the middle of typing every time any background
// task changed state, and resetting the selection would move the highlight
// off the row they were about to press ⏎ on.
//
// Selection is re-found by item ID, not by position: the list is re-sorted
// (a task moving from Running to Finished changes its index and its section),
// so an offset carried across a refresh would land on an unrelated row. An
// item that has gone entirely leaves the selection where array.Menu's own
// filter puts it, which is the first selectable row — nothing else is a
// defensible answer once the thing that was selected does not exist.
func (p *Popup) RefreshItems(items []*array.Item) {
	var selectedID string
	if it, ok := p.menu.Selected(); ok {
		selectedID = it.GetID()
	}
	p.cfg.Tabs[p.active].Items = items
	p.menu.SetItems(items)
	// Re-applied, not left alone: Menu's query is its own field and
	// SetItems does not re-filter against it (see SetItems' note on the two
	// being separate), so without this the new items would be shown
	// unfiltered under a filter box that still reads as non-empty.
	p.menu.Filter(p.filter)
	if selectedID != "" {
		p.menu.SelectByID(selectedID)
	}
}
