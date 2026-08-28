package layout

import "testing"

// chatCards is the Chat workspace's right pane from LAYOUT.md: Status is
// short, Task is medium, Sessions takes the remainder.
var chatCards = []CardSlot{
	{MinHeight: 3},
	{MinHeight: 6},
	{MinHeight: 5, Flex: true},
}

func chat(rightOpen bool) State {
	return State{
		Active:        WorkspaceChat,
		RightPaneOpen: rightOpen,
		HeaderAllowed: true,
		RightCards:    chatCards,
	}
}

func TestWidthBreakpoints(t *testing.T) {
	cases := []struct {
		name          string
		w             int
		wantTooSmall  bool
		wantRightPane bool
		wantPaneW     int
		wantOverlays  bool
	}{
		{"below the floor", 49, true, false, 0, false},
		{"at the floor", 50, false, false, 0, false},
		{"single column, top", 69, false, false, 0, false},
		{"overlays allowed, pane auto-hidden", 70, false, false, 0, true},
		{"pane still auto-hidden", 89, false, false, 0, true},
		{"narrow pane", 90, false, true, RightPaneMedium, true},
		{"narrow pane, top", 119, false, true, RightPaneMedium, true},
		{"full layout", 120, false, true, RightPaneWide, true},
		{"very wide", 200, false, true, RightPaneWide, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := Compute(c.w, 30, chat(true))
			if l.TooSmall != c.wantTooSmall {
				t.Fatalf("TooSmall = %v, want %v (message %q)", l.TooSmall, c.wantTooSmall, l.Message)
			}
			if c.wantTooSmall {
				if l.Message == "" {
					t.Fatal("TooSmall with no message")
				}
				return
			}
			if l.ShowRightPane != c.wantRightPane {
				t.Errorf("ShowRightPane = %v, want %v", l.ShowRightPane, c.wantRightPane)
			}
			if l.RightPaneWidth != c.wantPaneW {
				t.Errorf("RightPaneWidth = %d, want %d", l.RightPaneWidth, c.wantPaneW)
			}
			if l.OverlaysAllowed != c.wantOverlays {
				t.Errorf("OverlaysAllowed = %v, want %v", l.OverlaysAllowed, c.wantOverlays)
			}
		})
	}
}

func TestHeightBreakpoints(t *testing.T) {
	cases := []struct {
		name          string
		h             int
		wantTooSmall  bool
		wantHeader    bool
		wantCompact   bool
		wantWatermark bool
	}{
		{"below the floor", 11, true, false, false, false},
		{"at the floor", 12, false, false, false, true},
		{"no header, top", 23, false, false, false, true},
		{"compact header", 24, false, true, true, false},
		{"compact header, top", 29, false, true, true, false},
		{"full header", 30, false, true, false, false},
		{"tall", 60, false, true, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := Compute(120, c.h, chat(true))
			if l.TooSmall != c.wantTooSmall {
				t.Fatalf("TooSmall = %v, want %v (message %q)", l.TooSmall, c.wantTooSmall, l.Message)
			}
			if c.wantTooSmall {
				return
			}
			if l.ShowHeader != c.wantHeader {
				t.Errorf("ShowHeader = %v, want %v", l.ShowHeader, c.wantHeader)
			}
			if l.HeaderCompact != c.wantCompact {
				t.Errorf("HeaderCompact = %v, want %v", l.HeaderCompact, c.wantCompact)
			}
			if l.ShowWatermark != c.wantWatermark {
				t.Errorf("ShowWatermark = %v, want %v", l.ShowWatermark, c.wantWatermark)
			}
		})
	}
}

// Width and height are evaluated independently, so a too-narrow terminal
// reports narrowness even when it is also too short.
func TestWidthIsReportedBeforeHeight(t *testing.T) {
	l := Compute(20, 5, chat(true))
	if !l.TooSmall {
		t.Fatal("want TooSmall")
	}
	if l.Message != "terminal too narrow — 50 columns minimum" {
		t.Fatalf("message = %q", l.Message)
	}
}

// Nothing may be drawn outside the one-cell margin, and nothing may overlap.
func TestRectsStayInsideTheMargins(t *testing.T) {
	sizes := []struct{ w, h int }{
		{50, 12}, {70, 20}, {90, 24}, {120, 30}, {200, 60}, {80, 15},
	}
	for _, sz := range sizes {
		l := Compute(sz.w, sz.h, chat(true))
		all := []struct {
			name string
			r    Rect
		}{
			{"header", l.Header},
			{"main", l.Main},
			{"secondary", l.Secondary},
			{"status", l.StatusBar},
		}
		for i, r := range l.RightPane {
			all = append(all, struct {
				name string
				r    Rect
			}{"card", r})
			_ = i
		}
		for _, e := range all {
			if e.r.IsZero() {
				continue
			}
			if e.r.X < MarginX || e.r.X+e.r.W > sz.w-MarginX {
				t.Errorf("%dx%d: %s escapes horizontally: %+v", sz.w, sz.h, e.name, e.r)
			}
			if e.r.Y < MarginY || e.r.Y+e.r.H > sz.h-MarginY {
				t.Errorf("%dx%d: %s escapes vertically: %+v", sz.w, sz.h, e.name, e.r)
			}
		}
	}
}

// LAYOUT.md: toggling the right pane off reflows Main and Secondary to full
// width with no gap left behind.
func TestClosingTheRightPaneReclaimsTheWidth(t *testing.T) {
	open := Compute(120, 30, chat(true))
	shut := Compute(120, 30, chat(false))

	if shut.ShowRightPane {
		t.Fatal("pane should be closed")
	}
	full := 120 - 2*MarginX
	if shut.Main.W != full || shut.Secondary.W != full {
		t.Fatalf("main %d secondary %d, want %d", shut.Main.W, shut.Secondary.W, full)
	}
	if open.Main.W >= shut.Main.W {
		t.Fatalf("open main (%d) should be narrower than closed (%d)", open.Main.W, shut.Main.W)
	}
	if len(shut.RightPane) != 0 {
		t.Fatalf("closed pane still has %d card rects", len(shut.RightPane))
	}
}

// An open pane with no cards is not a layout the app can produce.
func TestRightPaneNeedsCards(t *testing.T) {
	s := chat(true)
	s.RightCards = nil
	l := Compute(120, 30, s)
	if l.ShowRightPane || l.RightPaneWidth != 0 {
		t.Fatalf("pane shown with no cards: %+v", l)
	}
	if l.Main.W != 120-2*MarginX {
		t.Fatalf("main did not reclaim the width: %d", l.Main.W)
	}
}

// The cards are NOT an even split, and they fill the pane exactly.
func TestCardHeightsAreUnevenAndExact(t *testing.T) {
	l := Compute(120, 40, chat(true))
	if len(l.RightPane) != len(chatCards) {
		t.Fatalf("got %d card rects, want %d", len(l.RightPane), len(chatCards))
	}

	total := 0
	for _, r := range l.RightPane {
		if r.H < MinBoxHeight {
			t.Errorf("card too short to draw a border: %+v", r)
		}
		total += r.H
	}
	body := l.Main.H + l.Secondary.H
	if total != body {
		t.Errorf("cards total %d rows, left column is %d", total, body)
	}
	if l.RightPane[0].H == l.RightPane[1].H && l.RightPane[1].H == l.RightPane[2].H {
		t.Error("cards were split evenly; LAYOUT.md says they are not")
	}
	// The Flex card absorbs the remainder.
	if l.RightPane[2].H <= chatCards[2].MinHeight {
		t.Errorf("flex card did not absorb the leftover: %d", l.RightPane[2].H)
	}
}

// A pane too short for every card drops trailing cards rather than squeezing
// all of them below a drawable height.
func TestCardsDropRatherThanSqueeze(t *testing.T) {
	l := Compute(120, 14, chat(true))
	drawn := 0
	for _, r := range l.RightPane {
		if r.IsZero() {
			continue
		}
		drawn++
		if r.H < MinBoxHeight {
			t.Errorf("undrawable card survived: %+v", r)
		}
	}
	if drawn == 0 {
		t.Fatal("every card was dropped at 120x14")
	}
	if drawn == len(chatCards) {
		t.Fatalf("all %d cards fit in a %d-row pane; expected some dropped", drawn, l.Main.H+l.Secondary.H)
	}
}

// Cards stack with no vertical gap and never overlap.
func TestCardsStackWithoutGaps(t *testing.T) {
	l := Compute(160, 50, chat(true))
	for i := 1; i < len(l.RightPane); i++ {
		prev, cur := l.RightPane[i-1], l.RightPane[i]
		if cur.IsZero() {
			break
		}
		if cur.Y != prev.Y+prev.H+BoxGapY {
			t.Errorf("card %d starts at %d, previous ends at %d", i, cur.Y, prev.Y+prev.H)
		}
	}
}

// The gap between the panes is exactly one cell, always.
func TestPaneGapIsOneCell(t *testing.T) {
	for _, w := range []int{90, 100, 119, 120, 200} {
		l := Compute(w, 30, chat(true))
		if !l.ShowRightPane {
			t.Fatalf("w=%d: pane hidden", w)
		}
		gap := l.RightPane[0].X - (l.Main.X + l.Main.W)
		if gap != PaneGapX {
			t.Errorf("w=%d: gap = %d, want %d", w, gap, PaneGapX)
		}
	}
}

// Main and Secondary stack with no gap, and the status bar sits one row above
// the bottom edge with nothing overlapping it.
func TestLeftColumnStacksOntoTheStatusBar(t *testing.T) {
	l := Compute(120, 30, chat(true))
	if l.Secondary.Y != l.Main.Y+l.Main.H+BoxGapY {
		t.Errorf("secondary at %d, main ends at %d", l.Secondary.Y, l.Main.Y+l.Main.H)
	}
	if l.StatusBar.Y != l.Secondary.Y+l.Secondary.H {
		t.Errorf("status at %d, secondary ends at %d", l.StatusBar.Y, l.Secondary.Y+l.Secondary.H)
	}
	if l.StatusBar.Y != 30-MarginY-1 {
		t.Errorf("status bar not one row above the bottom margin: %d", l.StatusBar.Y)
	}
}

// The header occupies exactly one row and the first box begins on the next
// one — zero rows between them.
func TestHeaderTouchesTheFirstBox(t *testing.T) {
	l := Compute(120, 30, chat(true))
	if l.Header.H != 1 {
		t.Fatalf("header height = %d, want 1", l.Header.H)
	}
	if l.Main.Y != l.Header.Y+1 {
		t.Errorf("main at %d, header at %d", l.Main.Y, l.Header.Y)
	}
}

// With no header the body starts at the top margin instead — the row is
// reclaimed, not left blank.
func TestHiddenHeaderReclaimsItsRow(t *testing.T) {
	with := Compute(120, 30, chat(true))
	s := chat(true)
	s.HeaderAllowed = false
	without := Compute(120, 30, s)

	if without.ShowHeader {
		t.Fatal("header should be suppressed")
	}
	if without.Main.Y != MarginY {
		t.Errorf("main at %d, want %d", without.Main.Y, MarginY)
	}
	if without.Main.H != with.Main.H+1 {
		t.Errorf("main gained %d rows, want 1", without.Main.H-with.Main.H)
	}
}

func TestEditorTerminalModes(t *testing.T) {
	editor := func(m TermMode) State {
		return State{
			Active:        WorkspaceEditor,
			RightPaneOpen: true,
			TerminalMode:  m,
			HeaderAllowed: true,
			RightCards:    []CardSlot{{MinHeight: 8}, {MinHeight: 6, Flex: true}},
		}
	}

	small := Compute(120, 40, editor(TermSmall))
	overlay := Compute(120, 40, editor(TermOverlay))
	full := Compute(120, 40, editor(TermFull))

	if small.Secondary.H != MinBoxHeight {
		t.Errorf("small terminal = %d rows, want %d", small.Secondary.H, MinBoxHeight)
	}
	if overlay.Secondary.H <= small.Secondary.H {
		t.Errorf("overlay (%d) should be taller than small (%d)", overlay.Secondary.H, small.Secondary.H)
	}
	if !full.Main.IsZero() {
		t.Errorf("full terminal should leave no Main: %+v", full.Main)
	}
	if full.Secondary.H != small.Main.H+small.Secondary.H {
		t.Errorf("full terminal = %d rows, body is %d", full.Secondary.H, small.Main.H+small.Secondary.H)
	}
}

// The whole F1 verification sequence, in order, with no panic and no rect
// escaping its terminal.
func TestResizeSweepWideToTinyAndBack(t *testing.T) {
	sizes := []struct{ w, h int }{
		{200, 50}, {160, 40}, {120, 30}, {119, 29}, {100, 26}, {90, 24},
		{89, 23}, {70, 20}, {69, 18}, {60, 14}, {50, 12}, {49, 11}, {45, 10},
		{50, 12}, {70, 20}, {90, 24}, {120, 30}, {200, 50},
	}
	for _, sz := range sizes {
		l := Compute(sz.w, sz.h, chat(true))
		if l.TooSmall {
			if l.Message == "" {
				t.Fatalf("%dx%d: TooSmall with no message", sz.w, sz.h)
			}
			continue
		}
		if l.Main.W <= 0 {
			t.Fatalf("%dx%d: main has no width", sz.w, sz.h)
		}
		if l.Main.H+l.Secondary.H+1+2*MarginY+btoi(l.ShowHeader) != sz.h {
			t.Fatalf("%dx%d: rows do not add up (main %d, secondary %d, header %v)",
				sz.w, sz.h, l.Main.H, l.Secondary.H, l.ShowHeader)
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Compute is pure: the same inputs give the same answer, and it does not
// retain or mutate the caller's slice.
func TestComputeIsPure(t *testing.T) {
	s := chat(true)
	a := Compute(120, 30, s)
	b := Compute(120, 30, s)

	if len(a.RightPane) != len(b.RightPane) {
		t.Fatal("card count differs between identical calls")
	}
	for i := range a.RightPane {
		if a.RightPane[i] != b.RightPane[i] {
			t.Fatalf("card %d differs: %+v vs %+v", i, a.RightPane[i], b.RightPane[i])
		}
	}

	a.RightPane[0].H = 999
	c := Compute(120, 30, s)
	if c.RightPane[0].H == 999 {
		t.Fatal("Compute returned a shared slice")
	}
	if s.RightCards[0].MinHeight != chatCards[0].MinHeight {
		t.Fatal("Compute mutated the caller's cards")
	}
}
