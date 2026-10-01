package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/array"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func testItems() []*array.Item {
	return []*array.Item{
		array.NewItem("apple", "Apple").SetDescription("a fruit").SetBinding("a"),
		array.NewItem("apricot", "Apricot").SetDescription("also a fruit").SetBinding("b"),
		array.NewItem("banana", "Banana").SetDescription("a different fruit").SetBinding("c"),
	}
}

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Bug 1 (F7 verification): a bare modifier keydown/up (Ctrl, Alt, Win, Caps
// Lock, pressed or released alone) decodes on Windows as KeyRunes carrying
// the raw, often-zero e.Char — indistinguishable from typed text to
// anything that does not filter for it. chat\input.go's filterInsertable is
// the traced fix for the query input; this is the identical rejection,
// applied here because the popup's filter never had it.
func TestModifierArtifactsDoNotReachTheFilter(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})

	o, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	p = o.(*Popup)
	if p.filter != "" {
		t.Errorf("filter = %q after a zero-rune event, want it rejected silently", p.filter)
	}

	o, _ = p.Update(runeKey("a"))
	p = o.(*Popup)
	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	p = o.(*Popup)
	if p.filter != "a" {
		t.Errorf("filter = %q, want \"a\" unchanged by a zero-rune event mixed in", p.filter)
	}
}

// Done-when #5: a plain rune types into the filter rather than moving the
// selection — the thing that keeps `j` from reading as "scroll".
func TestPlainRuneTypesIntoTheFilterNotSelection(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})

	// "j" is deliberately the probe: it is menu.go's OLD movement key, and
	// none of testItems() contain the letter, so if a rune were ever
	// misrouted to MoveDown instead of the filter, this would either move
	// past the list (no-op, since there is nowhere to go from row 0) or,
	// with a richer fixture, silently change the selection instead of the
	// filter text. The filter assertion below is the one that actually
	// proves it went to the right place.
	o, _ := p.Update(runeKey("j"))
	p = o.(*Popup)
	if p.filter != "j" {
		t.Fatalf("filter = %q, want \"j\" — a plain rune must type into the filter", p.filter)
	}
	if _, ok := p.menu.Selected(); ok {
		t.Error("no item contains \"j\"; the filtered list should be empty")
	}
}

// Done-when #6: esc clears a non-empty filter first, closes on the second
// press.
func TestEscClearsFilterThenCloses(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})

	o, _ := p.Update(runeKey("ap"))
	p = o.(*Popup)
	if p.filter != "ap" {
		t.Fatalf("filter = %q, want \"ap\"", p.filter)
	}

	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if o == nil {
		t.Fatal("the first esc closed the popup; it should have cleared the filter")
	}
	p = o.(*Popup)
	if p.filter != "" {
		t.Errorf("filter = %q after esc, want empty", p.filter)
	}

	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if o != nil {
		t.Error("the second esc (empty filter) should close the popup")
	}
}

// Fuzzy filtering ranks prefix matches first and highlights the match.
func TestFilteringRanksAndHighlights(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})
	o, _ := p.Update(runeKey("ap"))
	p = o.(*Popup)

	it, ok := p.menu.Selected()
	if !ok || it.GetID() != "apple" {
		t.Fatalf("selected = %v, want apple to rank first (a prefix match ties broken by original order)", it)
	}

	out := stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), 40, 20))
	if !strings.Contains(out, "Apple") || !strings.Contains(out, "Apricot") {
		t.Errorf("expected both matches rendered:\n%s", out)
	}
	if strings.Contains(out, "Banana") {
		t.Errorf("Banana does not match \"ap\" and should not be listed:\n%s", out)
	}
}

// A disabled item stays visible and reachable, but choosing it does nothing.
func TestDisabledItemCannotBeChosen(t *testing.T) {
	chosen := false
	items := []*array.Item{
		array.NewItem("a", "Available"),
		array.NewItem("b", "Unavailable").SetDisabled(true),
	}
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: items}}, MinW: 40, MinH: 10,
		OnSelect: func(*array.Item) (tea.Cmd, bool) { chosen = true; return nil, true },
	})
	p.menu.MoveDown() // select the disabled item

	o, _ := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o == nil {
		t.Fatal("choosing a disabled item must not close the popup")
	}
	if chosen {
		t.Error("OnSelect fired for a disabled item")
	}
}

// A nil OnSelect (Help's case) means there is nothing TO choose — Enter or
// a click on a row is a no-op that leaves the popup open for more browsing,
// not a close with nothing behind it.
func TestChoosingWithNoOnSelectStaysOpen(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})

	o, _ := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o == nil {
		t.Fatal("choosing an item with no OnSelect closed the popup; it should stay open")
	}
}

// Choosing an enabled item runs OnSelect and closes the popup.
func TestChoosingAnItemRunsOnSelectAndCloses(t *testing.T) {
	var got *array.Item
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10,
		OnSelect: func(it *array.Item) (tea.Cmd, bool) { got = it; return nil, true },
	})

	o, _ := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o != nil {
		t.Error("choosing an item should close the popup")
	}
	if got == nil || got.GetID() != "apple" {
		t.Errorf("OnSelect got %v, want the selected item", got)
	}
}

// Tabs cycle with tab/shift+tab, and the filter persists across the switch
// (this episode's pick, per F07's "persist or reset — pick one").
func TestTabsCycleAndFilterPersists(t *testing.T) {
	tabs := []PopupTab{
		{Label: "One", Items: []*array.Item{array.NewItem("a", "Alpha")}},
		{Label: "Two", Items: []*array.Item{array.NewItem("b", "Beta")}},
	}
	p := NewPopup(PopupConfig{Title: "T", Tabs: tabs, MinW: 40, MinH: 10})

	o, _ := p.Update(runeKey("x"))
	p = o.(*Popup)

	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyTab})
	p = o.(*Popup)
	if p.active != 1 {
		t.Fatalf("active tab = %d, want 1", p.active)
	}
	if p.filter != "x" {
		t.Errorf("filter = %q after switching tabs, want it to persist as \"x\"", p.filter)
	}

	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	p = o.(*Popup)
	if p.active != 0 {
		t.Errorf("shift+tab did not cycle back to tab 0: active = %d", p.active)
	}
}

// A left click on a row selects it AND chooses it — one click, not a click
// then an Enter.
func TestClickSelectsAndChooses(t *testing.T) {
	var got *array.Item
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10,
		OnSelect: func(it *array.Item) (tea.Cmd, bool) { got = it; return nil, true },
	})
	// Force a render so the menu's height (and thus row layout) is set.
	p.View(theme.New("turquoise", theme.BackgroundTransparent), 38, 8)

	_, _, listTop := p.rowOffsets()
	o, _ := p.Update(tea.MouseMsg{
		X: 2, Y: listTop + 1, // the second visible row: Apricot
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if o != nil {
		t.Error("clicking an item should close the popup, same as Enter")
	}
	if got == nil || got.GetID() != "apricot" {
		t.Errorf("click chose %v, want apricot", got)
	}
}

func TestPaletteAndHelpReportTheirOwnMinSize(t *testing.T) {
	r := newRoot(t)

	if w, h := newPaletteOverlay(r).MinSize(); w != 70 || h != 16 {
		t.Errorf("palette MinSize = %d×%d, want 70×16 (matching Help)", w, h)
	}
	if w, h := newHelpOverlay(r).MinSize(); w != 70 || h != 16 {
		t.Errorf("help MinSize = %d×%d, want 70×16", w, h)
	}
}

// DESIGN CHANGE (F7 verification): the position indicator shows on every
// row, not only once scrolling is needed — the palette and help must agree
// with each other regardless of box height.
func TestPositionIndicatorAlwaysShows(t *testing.T) {
	items := testItems() // 3 items, well under any reasonable listHeight
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: items}}, MinW: 60, MinH: 14})

	out := stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), 58, 12))
	if !strings.Contains(out, "1/3") {
		t.Errorf("expected the indicator \"1/3\" even though every item fits on screen:\n%s", out)
	}
}

// A verification finding: on a narrow popup the footer's hints (which only
// repeat the key bindings) must give way to the position indicator, not the
// other way around — a truncated "1/" that only reads correctly by
// coincidence on the last row is what this replaces.
func TestPositionIndicatorSurvivesNarrowTruncation(t *testing.T) {
	items := testItems()
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: items}}, MinW: 40, MinH: 10})

	// Narrow enough that the hints alone ("↑/↓ move   ⏎ select   esc
	// close") do not fit alongside "1/3" — 36 was the palette's own
	// textW at its old 40-column MinW.
	out := stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), 38, 8))
	if !strings.Contains(out, "1/3") {
		t.Errorf("the full indicator must survive truncation on a narrow popup:\n%s", out)
	}

	p.menu.MoveDown()
	out = stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), 38, 8))
	if !strings.Contains(out, "2/3") {
		t.Errorf("indicator should read 2/3 after moving down once:\n%s", out)
	}
}

// Leftover contradiction (F7 re-verification): Help's footer must not
// promise an Enter action it does not have. The palette, which DOES act on
// Enter, keeps the hint.
func TestHelpFooterDoesNotPromiseSelect(t *testing.T) {
	r := newRoot(t)
	out := stripANSI(newHelpOverlay(r).View(theme.New("turquoise", theme.BackgroundTransparent), 70, 20))
	if strings.Contains(out, "⏎ select") {
		t.Errorf("Help's footer still promises \"⏎ select\", but Enter does nothing there:\n%s", out)
	}
	if !strings.Contains(out, "esc close") {
		t.Errorf("Help's footer should still explain how to close it:\n%s", out)
	}

	out = stripANSI(newPaletteOverlay(r).View(theme.New("turquoise", theme.BackgroundTransparent), 70, 20))
	if !strings.Contains(out, "⏎ select") {
		t.Errorf("the palette DOES act on Enter and should keep the hint:\n%s", out)
	}
}

// Re-verification, explicitly: clicking OR pressing Enter on a help row
// does nothing except select/browse — it must NOT close the popup. Only
// Esc closes Help. Run through the real Root pipeline, not a synthetic
// Popup, since the earlier fix was reported working from a description
// alone and Aryan asked for it re-confirmed, not assumed.
func TestHelpRowNeverClosesOnEnterOrClick_OnlyEscDoes(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)
	r.OpenOverlay(newHelpOverlay(r))
	if r.overlay == nil {
		t.Fatal("help did not open")
	}
	r.View() // establish menu height for click math

	m, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	root := m.(*Root)
	if root.overlay == nil {
		t.Fatal("Enter on a help row closed the popup; it must only select/browse")
	}

	rect := root.overlayRect()
	p := root.overlay.(*Popup)
	_, _, listTop := p.rowOffsets()
	m, _ = root.Update(tea.MouseMsg{
		X: rect.X + 10, Y: rect.Y + 1 + listTop,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	root = m.(*Root)
	if root.overlay == nil {
		t.Fatal("clicking a help row closed the popup; it must only select/browse")
	}

	m, _ = root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = m.(*Root)
	if root.overlay != nil {
		t.Fatal("esc should close help (with an empty filter, first press)")
	}
}

// SIZING (F7 re-verification): every registered action's full name and
// binding render intact at the popup's own MinSize — "zero truncated text,
// not usually fits." Descriptions are deliberately not checked: they were
// removed from the row entirely (renderRow's own doc explains why).
//
// Scrolls through the WHOLE list rather than checking one static render:
// MinSize's box does not show all ~10 actions at once, and a row simply
// being below the fold is a scrolling fact, not a truncation bug — the
// thing actually under test is whether a row's OWN text is cut mid-line
// once it IS on screen, which ensureVisible() guarantees for every item as
// it becomes selected.
func TestNoNameOrBindingTruncatesAtMinSize(t *testing.T) {
	r := newRoot(t)
	minW, minH := newHelpOverlay(r).MinSize()
	th := theme.New("turquoise", theme.BackgroundTransparent)
	p := newHelpOverlay(r)

	seen := map[string]bool{}
	for i := 0; i < r.actions.Len(); i++ {
		out := stripANSI(p.View(th, minW-2, minH-2))
		it, ok := p.menu.Selected()
		if !ok {
			t.Fatal("nothing selected while scrolling through help")
		}
		seen[it.GetID()] = true
		if !strings.Contains(out, it.GetTitle()) {
			t.Errorf("name %q is truncated once on screen, at MinSize %d×%d:\n%s", it.GetTitle(), minW, minH, out)
		}
		if !strings.Contains(out, it.GetBinding()) {
			t.Errorf("binding %q (for %q) is truncated once on screen:\n%s", it.GetBinding(), it.GetTitle(), out)
		}

		// The two-column arrangement itself, not just "the text is
		// present": on EVERY item row -- not only the selected one, which
		// renders through the Fill branch and would have hidden this --
		// the name starts at the left edge and the binding ends at the
		// right one, with the row spanning the full content width. The
		// checks above would still pass with both columns huddled on the
		// left and dead space trailing, which is the layout this replaced.
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
				continue // filter row, section header, footer, padding
			}
			if w := visibleWidth(line); w != minW-4 {
				t.Errorf("item row spans %d cells, want the full %d: %q", w, minW-4, line)
			}
			if strings.HasSuffix(line, " ") {
				t.Errorf("item row has padding after its binding, so the binding is not flush right: %q", line)
			}
		}

		p.menu.MoveDown()
	}

	for _, a := range r.actions.All() {
		if !seen[a.ID] {
			t.Errorf("%s was never scrolled into view — the test did not actually check it", a.ID)
		}
	}
}

// F8: OnSelect's close bool, SetItems, QueryFor, OnFilterChange — additive
// extensions for the @ picker's directory-descent and path-typing needs.
// The two existing consumers (palette, Help) must be unaffected: both
// always report closes=true, byte-for-byte the same as before this change.

func TestPaletteAndHelpOnSelectAlwaysReportClose(t *testing.T) {
	r := newRoot(t)

	p := newPaletteOverlay(r)
	it, ok := p.menu.Selected()
	if !ok {
		t.Fatal("palette has no selected item to test with")
	}
	_, closes := p.cfg.OnSelect(it)
	if !closes {
		t.Error("palette's OnSelect must always report closes=true")
	}

	unavailable := array.NewItem("does-not-exist", "Does Not Exist")
	_, closes = p.cfg.OnSelect(unavailable)
	if !closes {
		t.Error("palette's OnSelect must report closes=true even for an unresolvable item")
	}

	if newHelpOverlay(r).cfg.OnSelect != nil {
		t.Fatal("Help must keep OnSelect nil — browsing an entry has nothing to run")
	}
}

func TestOnSelectCanKeepThePopupOpen(t *testing.T) {
	var chosen *array.Item
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10,
		OnSelect: func(it *array.Item) (tea.Cmd, bool) { chosen = it; return nil, false },
	})

	o, _ := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if o == nil {
		t.Fatal("OnSelect reporting closes=false must keep the popup open")
	}
	if chosen == nil || chosen.GetID() != "apple" {
		t.Errorf("OnSelect got %v, want the selected item", chosen)
	}
}

func TestSetItemsClearsFilterAndSelectsTop(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})

	o, _ := p.Update(runeKey("banana"))
	p = o.(*Popup)
	if p.filter != "banana" {
		t.Fatalf("filter = %q, want \"banana\" before SetItems", p.filter)
	}
	if _, ok := p.menu.Selected(); !ok {
		t.Fatal("expected a match for \"banana\" before SetItems")
	}

	next := []*array.Item{
		array.NewItem("readme.md", "README.md"),
		array.NewItem("main.go", "main.go"),
	}
	p.SetItems(next)

	if p.filter != "" {
		t.Errorf("filter = %q after SetItems, want cleared", p.filter)
	}
	it, ok := p.menu.Selected()
	if !ok || it.GetID() != "readme.md" {
		t.Errorf("selection after SetItems = %v, want the first new item", it)
	}
	if p.menu.Len() != len(next) {
		t.Errorf("menu holds %d items after SetItems, want %d (the OLD items must be gone, not merged)", p.menu.Len(), len(next))
	}
}

func TestQueryForOverridesWhatTheMenuActuallyFilters(t *testing.T) {
	items := []*array.Item{array.NewItem("point", "Point"), array.NewItem("other", "Other")}
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: items}}, MinW: 40, MinH: 10,
		QueryFor: func(raw string) string {
			if i := strings.LastIndexByte(raw, '\\'); i >= 0 {
				return raw[i+1:]
			}
			return raw
		},
	})

	for _, r := range "D:\\Projects\\Poi" {
		o, _ := p.Update(runeKey(string(r)))
		p = o.(*Popup)
	}
	if p.filter != "D:\\Projects\\Poi" {
		t.Errorf("filter (display text) = %q, want the whole typed path kept verbatim", p.filter)
	}
	if p.menu.Len() != 1 {
		t.Fatalf("menu matched %d items for leaf query \"Poi\", want exactly 1 (Point)", p.menu.Len())
	}
	it, _ := p.menu.Selected()
	if it.GetID() != "point" {
		t.Errorf("matched item = %v, want Point — QueryFor's leaf extraction did not reach the menu", it)
	}
}

func TestNilQueryForFiltersOnRawText(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})
	o, _ := p.Update(runeKey("ap"))
	p = o.(*Popup)
	if p.menu.Len() != 2 {
		t.Errorf("nil QueryFor: matched %d items for \"ap\", want 2", p.menu.Len())
	}
}

func TestOnFilterChangeFiresWithTheRawTypedText(t *testing.T) {
	var seen []string
	p := NewPopup(PopupConfig{
		Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10,
		OnFilterChange: func(raw string) tea.Cmd {
			seen = append(seen, raw)
			return nil
		},
	})

	o, _ := p.Update(runeKey("a"))
	p = o.(*Popup)
	o, _ = p.Update(runeKey("p"))
	p = o.(*Popup)
	o, _ = p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	p = o.(*Popup)

	want := []string{"a", "ap", "a"}
	if len(seen) != len(want) {
		t.Fatalf("OnFilterChange fired %d times (%v), want %d (%v)", len(seen), seen, len(want), want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("call %d: raw = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestNilOnFilterChangeIsSafe(t *testing.T) {
	p := NewPopup(PopupConfig{Title: "T", Tabs: []PopupTab{{Items: testItems()}}, MinW: 40, MinH: 10})
	o, cmd := p.Update(runeKey("a"))
	if cmd != nil {
		t.Error("a nil OnFilterChange must never produce a command")
	}
	if o.(*Popup).filter != "a" {
		t.Error("filter typing must still work with no OnFilterChange set")
	}
}
