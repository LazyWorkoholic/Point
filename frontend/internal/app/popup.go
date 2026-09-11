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

// PopupConfig is the data a consumer supplies. It is intentionally close to
// F07-popup-framework.md's own pseudocode (Title, Tabs, Items, OnSelect,
// Actions, Min) — the two differences are that Items folds into a one-tab
// Tabs entry (one code path for both shapes) and Min is two ints instead of
// a Size type this package has no other use for.
type PopupConfig struct {
	Title string
	Tabs  []PopupTab // len(Tabs) <= 1 draws no tab row

	// OnSelect fires when an item is chosen (Enter, or a click) and is not
	// disabled, and closes the popup. nil means there is nothing TO choose
	// — Help passes nil because browsing an entry has nothing further to
	// run — and Enter/click on a row is then a no-op that leaves the popup
	// open, not a close with nothing behind it.
	OnSelect func(*array.Item) tea.Cmd

	Actions []ItemAction

	MinW, MinH int
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
			p.setFilter("")
			return p, nil
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
			p.setFilter(string(r[:len(r)-1]))
		}
		return p, nil

	case tea.KeySpace:
		p.setFilter(p.filter + " ")
		return p, nil

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
		p.setFilter(p.filter + insertableRunes(m.Runes))
		return p, nil
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

func (p *Popup) setFilter(q string) {
	p.filter = q
	p.menu.Filter(q)
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
	return nil, p.cfg.OnSelect(it)
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

	tabsRow, filterRow, listTop := p.rowOffsets()
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
	case m.Y >= listTop:
		if p.menu.ClickRow(m.Y - listTop) {
			// A click both selects AND chooses — "mouse click selects" in
			// F07's own words, and the fastest path is the one a mouse user
			// expects: one click, not a click then an Enter.
			return p.chooseSelected()
		}
		return p, nil
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

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
	listHeight := h - listTop - 1 // 1 row always reserved for the footer
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

	rows := p.menu.VisibleRows()
	selOffset := p.menu.SelectedOffset()
	for i := 0; i < listHeight; i++ {
		if i < len(rows) {
			b.WriteString(p.renderRow(th, rows[i], i == selOffset, textW))
		}
		if i < listHeight-1 || footer != "" {
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
func rowWidths(avail int) (nameW, bindW int) {
	const maxName, maxBind = 40, 24

	bindW = maxBind
	if half := avail / 3; bindW > half {
		bindW = half
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
	avail := width - 2 // the "  " / marker column
	if avail < 1 {
		avail = 1
	}
	nameW, bindW := rowWidths(avail)

	// Name flush LEFT, binding flush RIGHT, gap between them — the same
	// left/right composition footerLine uses. With the description column
	// gone there is no third thing to sit between them, and two columns
	// huddled together on the left with dead space trailing reads as a
	// layout that lost something rather than one built for two.
	name := runewidth.Truncate(it.GetTitle(), nameW, "…")
	bind := runewidth.Truncate(it.GetBinding(), bindW, "…")

	if selected {
		// THEME.md rule 6: a Fill is for "a selected list row" by name — the
		// whole row, plain text, carries it. Individual highlighted
		// characters would fight the fill for attention on the one row that
		// is already unambiguous.
		plain := padCells("  "+name, width-runewidth.StringWidth(bind)) + bind
		return th.Fill(theme.SelectText, theme.SelectBg).Render(padCells(plain, width))
	}

	normal, hi := th.Style(theme.TextPrimary), th.Style(theme.AccentPrimary)
	bindStyle := th.Style(theme.AccentSecondary)
	if it.IsDisabled() {
		normal, hi, bindStyle = th.Style(theme.TextDim), th.Style(theme.TextDim), th.Style(theme.TextDim)
	}

	left := "  " + renderHighlighted(name, row.Match.Highlights, runewidth.StringWidth(name), normal, hi)
	gap := width - visibleWidth(left) - runewidth.StringWidth(bind)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + bindStyle.Render(bind)
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
