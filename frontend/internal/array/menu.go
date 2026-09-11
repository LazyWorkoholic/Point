// Package array is Point's shared, filterable list engine — the internal
// list model F07-popup-framework.md folds into app.Popup rather than have a
// second one.
//
// Menu is deliberately headless: it owns selection, scrolling and fuzzy
// filtering, and renders nothing. A matched character needs the ACCENT role
// and a selected row needs a Fill, and Theme lives in a package this one
// must not import — box.go's own note ("F3 or F7 should lift them into a
// shared ui package") is the same boundary. Popup turns a Row into text; this
// file only ever decides WHICH rows, in WHICH order, and which one is
// selected.
package array

import "sort"

// RowKind distinguishes a selectable item row from a non-selectable section
// header.
type RowKind int

const (
	RowItem RowKind = iota
	RowHeader
)

// Row is one line of the menu's current display list.
//
// Headers and items live in ONE ordered list, not two — the same fix F5 made
// to the transcript (chat\entries.go's doc: "two append-only lists cannot
// express one interleaved order"). A header consumes a row exactly like an
// item does, so scrolling and the "N/M" position indicator both stay correct
// without a separate accounting for how many headers are on screen.
type Row struct {
	Kind   RowKind
	Item   *Item  // nil for RowHeader
	Header string // "" for RowItem
	Match  Match  // zero value for RowHeader, and for RowItem when unfiltered
}

// Menu is a headless, scrollable, filterable list.
type Menu struct {
	all      []*Item // every item, in the order the caller supplied
	rows     []Row   // the current display list — filtered/ranked items, or sectioned items when unfiltered
	selected int     // index into rows; always a RowItem index once rows is non-empty
	cursor   int     // index into rows of the first visible row
	height   int     // rows visible at once; the caller's rect decides this, not a clamp band
	query    string
}

func NewMenu() Menu {
	m := Menu{height: 7}
	m.applyFilter()
	return m
}

// SetItems replaces the full item set and re-applies the current filter.
//
// The slice is copied. The original Menu aliased the caller's slice
// directly, so a later append by the caller silently mutated the menu —
// harmless nowhere Menu was actually used yet, but every real caller (Popup)
// rebuilds its item list from data-driven source (the action registry, a
// future memory/session list) on each open, so the copy costs nothing that
// matters and removes the aliasing hazard for good.
func (m *Menu) SetItems(items []*Item) {
	m.all = append([]*Item(nil), items...)
	m.applyFilter()
}

// AppendItems adds one item to the full set, not to the filtered view.
func (m *Menu) AppendItems(item *Item) {
	m.all = append(m.all, item)
	m.applyFilter()
}

// Filter sets the live query. An empty query restores the full, sectioned
// list; a non-empty one fuzzy-matches and ranks.
func (m *Menu) Filter(query string) {
	if query == m.query {
		return
	}
	m.query = query
	m.applyFilter()
}

func (m *Menu) Query() string { return m.query }

func (m *Menu) applyFilter() {
	// Keep the same item selected across a filter edit, if it is still
	// visible after the edit — losing your place on every keystroke would
	// be worse than a filter that feels sticky.
	prev := m.selectedItem()

	if m.query == "" {
		m.rows = sectionedRows(m.all)
	} else {
		m.rows = rankedRows(m.all, m.query)
	}

	m.selected = m.firstItemRow()
	if prev != nil {
		for i, row := range m.rows {
			if row.Kind == RowItem && row.Item == prev {
				m.selected = i
				break
			}
		}
	}
	m.cursor = 0
	m.ensureVisible()
}

// sectionedRows is the unfiltered view: original order, with a dim header
// row inserted every time an item's Section changes. Items with no Section
// never get a header. This is what lets Help show its existing
// category-grouped shape and a future tabbed consumer (memory) show its
// pending/session/project groups, from the same field.
func sectionedRows(items []*Item) []Row {
	rows := make([]Row, 0, len(items))
	section := ""
	first := true
	for _, it := range items {
		if it.section != "" && (first || it.section != section) {
			rows = append(rows, Row{Kind: RowHeader, Header: it.section})
		}
		section = it.section
		first = false
		rows = append(rows, Row{Kind: RowItem, Item: it})
	}
	return rows
}

// rankedRows is the filtered view: every item that matches, ranked
// prefix-first, flat — no section headers. Ranking necessarily interleaves
// items from different sections, so a header could not label a contiguous
// run of them even if one were drawn; the mockup in F07-popup-framework.md
// shows the same thing — no sub-headers appear under an active filter.
func rankedRows(items []*Item, query string) []Row {
	type scored struct {
		idx   int
		item  *Item
		match Match
	}
	scoredItems := make([]scored, 0, len(items))
	for i, it := range items {
		if mt, ok := fuzzyMatch(it.title, query); ok {
			scoredItems = append(scoredItems, scored{i, it, mt})
		}
	}
	sort.Slice(scoredItems, func(i, j int) bool {
		a, b := scoredItems[i], scoredItems[j]
		if a.match.Kind != b.match.Kind || a.match.span != b.match.span {
			return lessMatch(a.match, b.match)
		}
		return a.idx < b.idx // stable: original order breaks an exact tie
	})
	rows := make([]Row, len(scoredItems))
	for i, s := range scoredItems {
		rows[i] = Row{Kind: RowItem, Item: s.item, Match: s.match}
	}
	return rows
}

func (m *Menu) selectedItem() *Item {
	if m.selected >= 0 && m.selected < len(m.rows) && m.rows[m.selected].Kind == RowItem {
		return m.rows[m.selected].Item
	}
	return nil
}

func (m *Menu) firstItemRow() int {
	for i, row := range m.rows {
		if row.Kind == RowItem {
			return i
		}
	}
	return 0
}

// ---- movement — all skip header rows, which are not selectable ----

func (m *Menu) MoveUp()   { m.move(-1) }
func (m *Menu) MoveDown() { m.move(1) }

func (m *Menu) move(dir int) {
	i := m.selected
	for {
		i += dir
		if i < 0 || i >= len(m.rows) {
			return // no further selectable row in that direction
		}
		if m.rows[i].Kind == RowItem {
			m.selected = i
			m.ensureVisible()
			return
		}
	}
}

func (m *Menu) PageUp()   { m.page(-1) }
func (m *Menu) PageDown() { m.page(1) }

func (m *Menu) page(dir int) {
	step := m.height
	if step < 1 {
		step = 1
	}
	for n := 0; n < step; n++ {
		before := m.selected
		m.move(dir)
		if m.selected == before {
			return // hit an end before using the whole page
		}
	}
}

func (m *Menu) Top() {
	m.selected = m.firstItemRow()
	m.ensureVisible()
}

func (m *Menu) Bottom() {
	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].Kind == RowItem {
			m.selected = i
			break
		}
	}
	m.ensureVisible()
}

// ---- sizing and scrolling ----

// SetHeight tells the menu how many rows the caller's rect can show. Unlike
// the original menu.go, this is not clamped into 3/7/passthrough bands —
// F1's handoff flagged that as incompatible with "components render into the
// rect they are given," and the fix is simply to honour the number.
func (m *Menu) SetHeight(height int) {
	if height < 1 {
		height = 1
	}
	m.height = height
	m.ensureVisible()
}

func (m *Menu) ensureVisible() {
	if m.selected < m.cursor {
		m.cursor = m.selected
	}
	if m.selected >= m.cursor+m.height {
		m.cursor = m.selected - m.height + 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if max := len(m.rows) - m.height; max >= 0 && m.cursor > max {
		m.cursor = max
	}
}

// VisibleRows is exactly what the caller should render this frame, top to
// bottom — at most SetHeight's row count, fewer at the end of the list.
func (m *Menu) VisibleRows() []Row {
	end := m.cursor + m.height
	if end > len(m.rows) {
		end = len(m.rows)
	}
	if m.cursor >= end {
		return nil
	}
	return m.rows[m.cursor:end]
}

// SelectedOffset is the selected row's position within VisibleRows(), or -1
// if — which should not happen once ensureVisible has run — it is not
// currently on screen.
func (m *Menu) SelectedOffset() int {
	if m.selected < m.cursor || m.selected >= m.cursor+m.height {
		return -1
	}
	return m.selected - m.cursor
}

// ClickRow selects the row at `offset` within VisibleRows(), if any. It
// reports whether the click landed on a selectable row — a click on a header
// or past the end of the list changes nothing.
func (m *Menu) ClickRow(offset int) bool {
	i := m.cursor + offset
	if i < 0 || i >= len(m.rows) || m.rows[i].Kind != RowItem {
		return false
	}
	m.selected = i
	m.ensureVisible()
	return true
}

// ScrollUp and ScrollDown are the mouse wheel's binding — one row at a time,
// same as the original menu.go.
func (m *Menu) ScrollUp()   { m.MoveUp() }
func (m *Menu) ScrollDown() { m.MoveDown() }

// ---- reading ----

func (m *Menu) Selected() (*Item, bool) {
	it := m.selectedItem()
	return it, it != nil
}

// Len is the number of SELECTABLE rows after filtering — headers are not
// counted, matching TotalLen below.
func (m *Menu) Len() int {
	n := 0
	for _, r := range m.rows {
		if r.Kind == RowItem {
			n++
		}
	}
	return n
}

// TotalLen is the item count before filtering.
func (m *Menu) TotalLen() int { return len(m.all) }

// SelectedRank is the 1-based position of the selected item among the
// visible (filtered) items — "12" in a "12/40" indicator. 0 if nothing is
// selected.
func (m *Menu) SelectedRank() int {
	rank := 0
	for i, r := range m.rows {
		if r.Kind == RowItem {
			rank++
		}
		if i == m.selected {
			return rank
		}
	}
	return 0
}

// VisibleItemCount is how many of VisibleRows() are selectable items, as
// opposed to headers — what the caller compares against Len() to decide
// whether a scroll position indicator is needed at all.
func (m *Menu) VisibleItemCount() int {
	n := 0
	for _, r := range m.VisibleRows() {
		if r.Kind == RowItem {
			n++
		}
	}
	return n
}
