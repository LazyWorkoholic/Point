package array

import "testing"

func items(pairs ...[2]string) []*Item {
	out := make([]*Item, len(pairs))
	for i, p := range pairs {
		out[i] = NewItem(p[0], p[1])
	}
	return out
}

func TestUnfilteredListIsSectioned(t *testing.T) {
	a := NewItem("a", "Alpha").SetSection("Group 1")
	b := NewItem("b", "Bravo").SetSection("Group 1")
	c := NewItem("c", "Charlie").SetSection("Group 2")

	m := NewMenu()
	m.SetItems([]*Item{a, b, c})

	rows := m.VisibleRows()
	// header, a, b, header, c
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5: %+v", len(rows), rows)
	}
	if rows[0].Kind != RowHeader || rows[0].Header != "Group 1" {
		t.Errorf("row 0 = %+v, want the Group 1 header", rows[0])
	}
	if rows[3].Kind != RowHeader || rows[3].Header != "Group 2" {
		t.Errorf("row 3 = %+v, want the Group 2 header", rows[3])
	}
	if m.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (headers do not count)", m.Len())
	}
}

// Filtering ranks and flattens — no section headers under an active query,
// matching F07's own mockup (no sub-headers appear under "filter: run_").
func TestFilteredListHasNoHeaders(t *testing.T) {
	a := NewItem("a", "Alpha").SetSection("Group 1")
	b := NewItem("b", "Alarm").SetSection("Group 2")

	m := NewMenu()
	m.SetItems([]*Item{a, b})
	m.Filter("al")

	for _, row := range m.VisibleRows() {
		if row.Kind == RowHeader {
			t.Fatalf("a filtered list must not contain headers: %+v", m.VisibleRows())
		}
	}
	if m.Len() != 2 {
		t.Errorf("Len() = %d, want 2", m.Len())
	}
}

// Movement skips header rows — they are not selectable.
func TestMovementSkipsHeaders(t *testing.T) {
	a := NewItem("a", "Alpha").SetSection("G1")
	b := NewItem("b", "Bravo").SetSection("G2")
	m := NewMenu()
	m.SetItems([]*Item{a, b})

	it, ok := m.Selected()
	if !ok || it.GetID() != "a" {
		t.Fatalf("initial selection = %v, want a", it)
	}
	m.MoveDown()
	it, ok = m.Selected()
	if !ok || it.GetID() != "b" {
		t.Fatalf("after MoveDown, selection = %v, want b (the header must be skipped)", it)
	}
	// One more MoveDown does nothing — already at the end.
	m.MoveDown()
	it, _ = m.Selected()
	if it.GetID() != "b" {
		t.Errorf("MoveDown past the end moved to %v", it)
	}
}

func TestScrollingKeepsSelectionVisible(t *testing.T) {
	m := NewMenu()
	list := make([]*Item, 20)
	for i := range list {
		list[i] = NewItem(string(rune('a'+i)), string(rune('a'+i)))
	}
	m.SetItems(list)
	m.SetHeight(5)

	for i := 0; i < 10; i++ {
		m.MoveDown()
	}
	if off := m.SelectedOffset(); off < 0 || off >= 5 {
		t.Fatalf("selected offset %d is not within the visible window", off)
	}
	it, _ := m.Selected()
	if it.GetID() != "k" { // 10 moves from "a" = "k"
		t.Errorf("selected = %v, want k", it.GetID())
	}
}

func TestClickRowSelectsAndIgnoresHeaders(t *testing.T) {
	a := NewItem("a", "Alpha").SetSection("G1")
	b := NewItem("b", "Bravo").SetSection("G1")
	m := NewMenu()
	m.SetItems([]*Item{a, b})
	m.SetHeight(10)

	// rows: [header, a, b] — offset 0 is the header.
	if m.ClickRow(0) {
		t.Fatal("clicking a header row must not select anything")
	}
	if !m.ClickRow(2) {
		t.Fatal("clicking an item row should select it")
	}
	it, _ := m.Selected()
	if it.GetID() != "b" {
		t.Errorf("selected = %v, want b", it.GetID())
	}
}

// A filter edit keeps the same item selected if it is still visible —
// losing your place on every keystroke would be worse than a sticky filter.
func TestFilterKeepsSelectionWhenStillVisible(t *testing.T) {
	m := NewMenu()
	m.SetItems(items([2]string{"a", "Apple"}, [2]string{"b", "Banana"}, [2]string{"c", "Cherry"}))
	m.MoveDown() // select Banana
	it, _ := m.Selected()
	if it.GetID() != "b" {
		t.Fatalf("setup: selected = %v, want b", it.GetID())
	}

	m.Filter("an") // matches only Banana
	it, ok := m.Selected()
	if !ok || it.GetID() != "b" {
		t.Errorf("after filtering, selected = %v, want b to stay selected", it)
	}
}

func TestSetItemsCopiesTheSlice(t *testing.T) {
	list := items([2]string{"a", "Alpha"})
	m := NewMenu()
	m.SetItems(list)

	list[0] = NewItem("b", "Bravo") // mutate the caller's slice afterwards
	it, _ := m.Selected()
	if it.GetID() != "a" {
		t.Error("SetItems aliased the caller's slice; a later mutation leaked through")
	}
}

func TestEmptyMenuHasNoSelection(t *testing.T) {
	m := NewMenu()
	if _, ok := m.Selected(); ok {
		t.Error("an empty menu must report no selection")
	}
	m.MoveDown() // must not panic
	m.MoveUp()
	m.PageDown()
}

// SelectByID is what lets a live-refreshing list keep the user's place
// across a RE-SORT — F9's task tray, where a task moving from Running to
// Finished changes both its index and its section header, so the offset that
// pointed at it before the refresh points at an unrelated row after it.
func TestSelectByIDFindsItAcrossAReorder(t *testing.T) {
	m := NewMenu()
	m.SetItems([]*Item{
		NewItem("a", "alpha").SetSection("Running"),
		NewItem("b", "beta").SetSection("Running"),
	})
	if !m.SelectByID("b") {
		t.Fatal("SelectByID(b) = false on a list containing b")
	}
	if it, _ := m.Selected(); it.GetID() != "b" {
		t.Fatalf("selected %q, want b", it.GetID())
	}

	// Re-sorted: b moves to the front and into a different section, so a
	// header row is now interleaved differently and every offset shifted.
	m.SetItems([]*Item{
		NewItem("b", "beta").SetSection("Finished"),
		NewItem("a", "alpha").SetSection("Running"),
	})
	if !m.SelectByID("b") {
		t.Fatal("SelectByID(b) = false after the reorder")
	}
	if it, _ := m.Selected(); it.GetID() != "b" {
		t.Errorf("selected %q after the reorder, want b", it.GetID())
	}
}

// An absent id must not move the selection — nothing else is a defensible
// answer once the thing that was selected does not exist.
func TestSelectByIDLeavesTheSelectionAloneWhenAbsent(t *testing.T) {
	m := NewMenu()
	m.SetItems([]*Item{NewItem("a", "alpha"), NewItem("b", "beta")})
	m.MoveDown()
	before, _ := m.Selected()

	if m.SelectByID("nope") {
		t.Error("SelectByID returned true for an id that is not there")
	}
	after, _ := m.Selected()
	if after != before {
		t.Errorf("selection moved from %q to %q", before.GetID(), after.GetID())
	}
}

// A header row must never be selectable through this path — it has no Item.
func TestSelectByIDNeverLandsOnAHeader(t *testing.T) {
	m := NewMenu()
	m.SetItems([]*Item{NewItem("a", "alpha").SetSection("Running")})
	if m.SelectByID("Running") {
		t.Error("a section header matched by its own text")
	}
}

// Top() scrolls to the actual top of the list, not merely far enough to show
// the first selectable item.
//
// ensureVisible alone left the cursor at firstItemRow, which is 1 whenever
// the list opens with a section header — so Home hid the very first heading,
// the one row a reader pressing it is most likely looking for. Found while
// testing F9a's pinned headings; it affects Help too, which is sectioned.
func TestTopShowsTheFirstHeaderNotJustTheFirstItem(t *testing.T) {
	m := NewMenu()
	m.SetItems([]*Item{
		NewItem("a", "alpha").SetSection("First"),
		NewItem("b", "beta").SetSection("First"),
		NewItem("c", "gamma").SetSection("Second"),
	})
	m.SetHeight(3)

	m.Bottom()
	m.Top()

	rows := m.VisibleRows()
	if len(rows) == 0 {
		t.Fatal("no rows visible after Top()")
	}
	if rows[0].Kind != RowHeader || rows[0].Header != "First" {
		t.Errorf("first visible row after Top() = %+v, want the \"First\" header", rows[0])
	}
	if it, ok := m.Selected(); !ok || it.GetID() != "a" {
		t.Errorf("Top() selected %v, want the first item", it)
	}
}

// ClickRow rejects an offset past the VISIBLE WINDOW even when the full list
// has more rows further down.
//
// This is the bound F9a added, and it is the half that was missing: the check
// used to be `i >= len(m.rows)` — the whole list — so an offset below the
// bottom of the window still resolved. TestClickRowSelectsAndIgnoresHeaders
// above cannot catch it: its height (10) exceeds its row count (3), so the
// window covers the whole list and no offset can be outside it.
//
// What it cost in practice: F9a's Popup hit-tested its own footer as a list
// row, and ClickRow then selected and OPENED a task seven rows below the
// window — measured at 22 tasks in a 15-row box, where clicking the footer
// opened fin11 while the screen showed fin00–fin06. The Popup side is fixed
// too (Popup.geom), but the bound belongs here as well: this type owns the
// window, `offset` is documented as an index into VisibleRows(), and a
// contract that holds only because every caller respects it is the one that
// breaks when a caller is added.
func TestClickRowRejectsOffsetsPastTheVisibleWindow(t *testing.T) {
	m := NewMenu()
	items := make([]*Item, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, NewItem(string(rune('a'+i)), "Item"))
	}
	m.SetItems(items)
	m.SetHeight(5)
	m.Top()

	visible := len(m.VisibleRows())
	if visible != 5 {
		t.Fatalf("window = %d rows, want 5", visible)
	}
	if m.TotalLen() <= visible {
		t.Fatal("the list must be longer than the window for this to mean anything")
	}

	before, _ := m.Selected()
	for _, offset := range []int{visible, visible + 1, visible + 9, -1, -5} {
		if m.ClickRow(offset) {
			t.Errorf("ClickRow(%d) resolved with a %d-row window over %d rows",
				offset, visible, m.TotalLen())
		}
	}
	if after, _ := m.Selected(); after != before {
		t.Errorf("an out-of-window ClickRow moved the selection from %q to %q",
			before.GetID(), after.GetID())
	}

	// The last row INSIDE the window still resolves — the bound is exclusive,
	// not off by one.
	if !m.ClickRow(visible - 1) {
		t.Error("ClickRow rejected the last row of the window")
	}
	if it, _ := m.Selected(); it.GetID() != items[visible-1].GetID() {
		t.Errorf("selected %q, want the window's last row %q",
			it.GetID(), items[visible-1].GetID())
	}

	// And the same bound after scrolling, where cursor > 0 — the case where
	// `cursor + offset` can still land inside len(m.rows) and used to.
	m.Bottom()
	before, _ = m.Selected()
	if m.ClickRow(len(m.VisibleRows())) {
		t.Error("ClickRow resolved one past the window at the end of the list")
	}
	if after, _ := m.Selected(); after != before {
		t.Error("an out-of-window ClickRow moved the selection after scrolling")
	}
}
