// Package layout is the one place breakpoints live.
//
// Compute is pure: no globals, no I/O, no terminal calls. That is what makes
// every number in LAYOUT.md testable without a terminal, and it is the reason
// components are forbidden from deciding their own visibility — a component
// renders into the rect it is handed, and a zero-size rect means "not drawn
// this frame".
package layout

// WorkspaceID lives here, below both app and layout, because Compute genuinely
// branches on which workspace is active (Chat stacks a query area under the
// viewport; Editor stacks a terminal). Putting it in app would make layout
// import app, which imports layout.
type WorkspaceID string

const (
	WorkspaceChat   WorkspaceID = "chat"
	WorkspaceEditor WorkspaceID = "editor"
)

// TermMode is the editor terminal's three states (F14 implements them; the
// layout consequences are decided here).
type TermMode string

const (
	TermSmall   TermMode = "small"
	TermOverlay TermMode = "overlay"
	TermFull    TermMode = "full"
)

// Rect is a region in terminal cells. Zero W or H means "not drawn".
type Rect struct {
	X, Y, W, H int
}

func (r Rect) IsZero() bool { return r.W <= 0 || r.H <= 0 }

// CardSlot describes ONE right-pane card's height appetite.
//
// LAYOUT.md is explicit that RightPane is "a slice of rects with different
// heights, not an even split": Status is short, Task is medium, the last card
// takes the remainder. Compute cannot know that without being told, and it
// must not know what a "Status card" is — so the caller hands it slots.
//
// F6 owns the real card type. This is deliberately a narrower, differently
// named struct: F6's card must be adaptable TO this, not identical to it.
type CardSlot struct {
	MinHeight int  // rows this card wants, borders included
	Flex      bool // may absorb leftover rows; the last Flex card gets them
}

// State is everything outside the terminal size that changes the layout.
type State struct {
	Active        WorkspaceID
	RightPaneOpen bool
	TreeOpen      bool
	TerminalMode  TermMode
	OverlayOpen   bool
	HeaderAllowed bool

	// RightCards is the right pane's contents, top to bottom. Nil or empty
	// means there is no right pane to draw, regardless of RightPaneOpen —
	// an open pane with nothing in it is not a layout the app can produce.
	RightCards []CardSlot
}

// Layout is the answer. Every rect is absolute, in terminal cells, with gaps
// already subtracted — components add no margins of their own.
type Layout struct {
	Header    Rect
	Main      Rect
	Secondary Rect
	RightPane []Rect
	StatusBar Rect

	ShowHeader    bool
	ShowRightPane bool
	ShowWatermark bool

	// HeaderCompact is the 24–29 row band: the header is still one line, but
	// it is title-only — no animation, no trademark.
	HeaderCompact bool

	// OverlaysAllowed is false below 70 columns, where an overlay would be
	// wider than the screen. The root refuses to open one and says so.
	OverlaysAllowed bool

	// TooSmall short-circuits everything: every rect is zero and Message is
	// the sentence to print.
	TooSmall bool
	Message  string

	// RightPaneWidth is 0 when hidden. Kept so the status bar and tests can
	// assert the 32/26 split without re-deriving it.
	RightPaneWidth int

	W, H int
}

// Breakpoints. Width and height are evaluated independently (LAYOUT.md).
//
// These are provisional by the spec's own admission: separate bordered boxes
// plus outer margins cost more rows and columns than a shared-border layout,
// and the true minimums are only visible once F3–F6 render. Changing them is
// this table plus the test table.
const (
	MinWidth  = 50
	MinHeight = 12

	WideWidth    = 120 // ≥ this: right pane 32 columns
	MediumWidth  = 90  // ≥ this: right pane 26 columns
	NarrowWidth  = 70  // ≥ this: overlays allowed; below, single column
	OverlayFloor = NarrowWidth

	RightPaneWide   = 32
	RightPaneMedium = 26

	HeaderFullHeight    = 30 // ≥ this: header line with animation
	HeaderCompactHeight = 24 // ≥ this: title-only header; below, watermark only

	// A bordered box is two border rows plus at least one content row.
	MinBoxHeight = 3
)

// Margins and gaps, exactly as LAYOUT.md specifies them. Vertical gaps between
// stacked boxes are 0 on purpose: two adjacent rounded borders already read as
// a separation, and a 0-row gap buys back a row that matters at 24 rows.
const (
	MarginX  = 1 // terminal edge → everything
	MarginY  = 1 // terminal edge → everything
	PaneGapX = 1 // left pane ↔ right pane
	BoxGapY  = 0 // between stacked boxes, either pane
)

// Compute decides the whole frame.
func Compute(w, h int, s State) Layout {
	l := Layout{W: w, H: h}

	// Width is checked before height so the message is deterministic when
	// both are violated. A terminal that is too narrow is the one you notice.
	switch {
	case w < MinWidth:
		l.TooSmall = true
		l.Message = "terminal too narrow — 50 columns minimum"
		return l
	case h < MinHeight:
		l.TooSmall = true
		l.Message = "terminal too short — 12 rows minimum"
		return l
	}

	l.OverlaysAllowed = w >= OverlayFloor

	// -- vertical bands ----------------------------------------------------
	//
	// top margin · [header] · main · secondary · status bar · bottom margin
	// with 0 rows between the header and the first box, and 0 rows between
	// stacked boxes.

	l.ShowHeader = s.HeaderAllowed && h >= HeaderCompactHeight
	l.HeaderCompact = l.ShowHeader && h < HeaderFullHeight
	l.ShowWatermark = !l.ShowHeader

	y := MarginY
	if l.ShowHeader {
		l.Header = Rect{X: MarginX, Y: y, W: w - 2*MarginX, H: 1}
		y++
	}

	// The status bar and the bottom margin are reserved before anything is
	// allocated, so a growing pane can never eat them.
	bodyTop := y
	bodyHeight := h - bodyTop - 1 /* status */ - MarginY
	if bodyHeight < 0 {
		bodyHeight = 0
	}

	l.StatusBar = Rect{X: MarginX, Y: h - MarginY - 1, W: w - 2*MarginX, H: 1}

	// -- horizontal split --------------------------------------------------

	contentW := w - 2*MarginX
	l.RightPaneWidth = rightPaneWidth(w)
	l.ShowRightPane = s.RightPaneOpen && l.RightPaneWidth > 0 && len(s.RightCards) > 0

	leftW := contentW
	if l.ShowRightPane {
		leftW = contentW - l.RightPaneWidth - PaneGapX
		// A right pane that leaves the left column unusable is not a layout;
		// drop it rather than render two useless slivers.
		if leftW < MinWidth/2 {
			l.ShowRightPane = false
			leftW = contentW
		}
	}
	if !l.ShowRightPane {
		l.RightPaneWidth = 0
	}

	// -- left column: main over secondary ----------------------------------

	secondaryH := secondaryHeight(s, bodyHeight)
	mainH := bodyHeight - secondaryH - BoxGapY
	if mainH < 0 {
		mainH = 0
		secondaryH = bodyHeight
	}

	l.Main = Rect{X: MarginX, Y: bodyTop, W: leftW, H: mainH}
	if secondaryH > 0 {
		l.Secondary = Rect{X: MarginX, Y: bodyTop + mainH + BoxGapY, W: leftW, H: secondaryH}
	}

	// -- right column: one rect per card -----------------------------------

	if l.ShowRightPane {
		l.RightPane = distribute(s.RightCards, Rect{
			X: MarginX + leftW + PaneGapX,
			Y: bodyTop,
			W: l.RightPaneWidth,
			H: bodyHeight,
		})
	}

	return l
}

func rightPaneWidth(w int) int {
	switch {
	case w >= WideWidth:
		return RightPaneWide
	case w >= MediumWidth:
		return RightPaneMedium
	default:
		// 70–89 auto-hides the pane (it stays openable as an overlay, which
		// is the root's decision, not this function's); below 70 is single
		// column by definition.
		return 0
	}
}

// secondaryHeight is the query area (chat) or terminal (editor).
func secondaryHeight(s State, bodyHeight int) int {
	if bodyHeight <= 0 {
		return 0
	}

	want := MinBoxHeight
	if s.Active == WorkspaceEditor {
		switch s.TerminalMode {
		case TermFull:
			return bodyHeight
		case TermOverlay:
			want = bodyHeight / 2
			if want < MinBoxHeight {
				want = MinBoxHeight
			}
		default:
			want = MinBoxHeight
		}
	}

	// Main is the reason the app exists; the secondary box yields first.
	if want > bodyHeight-MinBoxHeight {
		want = bodyHeight - MinBoxHeight
	}
	if want < 0 {
		want = 0
	}
	return want
}

// distribute lays cards down the right pane, top to bottom.
//
// Each card gets its MinHeight; whatever is left over goes to the last Flex
// card, or to the last card if none flex. When the cards do not fit, trailing
// cards are dropped to zero rather than every card being squeezed below the
// three rows a bordered box needs — a row of empty borders is worse than one
// fewer card.
func distribute(cards []CardSlot, pane Rect) []Rect {
	out := make([]Rect, len(cards))
	if pane.H <= 0 {
		return out
	}

	heights := make([]int, len(cards))
	remaining := pane.H
	fitted := 0
	for i, c := range cards {
		want := c.MinHeight
		if want < MinBoxHeight {
			want = MinBoxHeight
		}
		if remaining-want < 0 {
			// No room for this card at its minimum. If there is room for a
			// bare box, give it the remainder and stop; otherwise stop here.
			if remaining >= MinBoxHeight {
				heights[i] = remaining
				remaining = 0
				fitted = i + 1
			}
			break
		}
		heights[i] = want
		remaining -= want + BoxGapY
		fitted = i + 1
	}

	if remaining > 0 && fitted > 0 {
		target := fitted - 1
		for i := fitted - 1; i >= 0; i-- {
			if cards[i].Flex {
				target = i
				break
			}
		}
		heights[target] += remaining
	}

	y := pane.Y
	for i := 0; i < fitted; i++ {
		out[i] = Rect{X: pane.X, Y: y, W: pane.W, H: heights[i]}
		y += heights[i] + BoxGapY
	}
	return out
}
