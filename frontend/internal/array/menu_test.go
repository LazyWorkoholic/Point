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
