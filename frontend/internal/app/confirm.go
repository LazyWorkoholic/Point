package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Confirm is F07-popup-framework.md's second shared widget. Every
// destructive action is meant to route through it; this episode wires
// exactly one (quit with tasks running — see actions.go), the highest-stakes
// case that already existed unconfirmed. The rest stay flagged for the
// episodes that build them (F09's discard, F11's delete, …), per Aryan's
// call: draft loss is low-stakes and recoverable, a background task lost to
// a quit is not.
type Confirm struct {
	title, body string
	yes, no     string
	onConfirm   tea.Cmd

	// focusYes is false by default — "No is focused by default" is F07's own
	// rule, so an Enter pressed out of habit never fires the destructive path.
	focusYes bool
}

// NewConfirm builds a Confirm. Empty yes/no default to "Yes"/"No".
func NewConfirm(title, body, yes, no string, onConfirm tea.Cmd) *Confirm {
	if yes == "" {
		yes = "Yes"
	}
	if no == "" {
		no = "No"
	}
	return &Confirm{title: title, body: body, yes: yes, no: no, onConfirm: onConfirm}
}

// MinSize is LAYOUT.md's "confirm 40×7" as a FLOOR, widened when this
// confirm's own buttons need more.
//
// It used to return a flat 40×7, which silently truncated any button pair
// wider than the content budget. Found by F9a's cancel confirmation:
// "Cancel it" / "Leave it running" compose to a 44-cell button row inside a
// 38-cell budget, so `centre` fell through to `truncateCells` and rendered
// "[ Leave it run" — and because the focused and unfocused forms carry their
// escape sequences at different offsets, the cut landed at a different
// visible column depending on which side had focus, which read as the
// brackets jumping around while navigating with ←/→.
//
// Fixed here rather than by shortening the labels: a widget that truncates
// its own only-two controls is broken for every future caller, and F11's
// delete and F9b's apply-anyway are both going to want verbs longer than
// "Quit". The quit confirm ("Quit" / "Cancel") fits 40 and is unaffected —
// this can only ever grow a confirm that would not have fitted.
func (c *Confirm) MinSize() (int, int) {
	return c.naturalWidth(), confirmFloorH
}

// confirmFloorH is LAYOUT.md's "confirm 40×7" height, kept as the REFUSAL
// threshold and no longer used as the rendered height. See PreferredSize.
const confirmFloorH = 7

// naturalWidth is renderButtons' own geometry: each label gets four cells of
// box ("[ " + label + " ]", or two spaces either side), plus buttonGap
// between the two, plus 2 for box.go's fitCells padding and 2 for the
// border — the same accounting View does in reverse when it computes textW.
func (c *Confirm) naturalWidth() int {
	buttons := runewidth.StringWidth(c.yes) + runewidth.StringWidth(c.no) +
		8 + runewidth.StringWidth(buttonGap) // 4 cells of box per button
	w := buttons + 4
	if w < 40 {
		w = 40
	}
	return w
}

// PreferredSize lets a Confirm grow to fit its own body, which is what makes
// it a MaximisedOverlay.
//
// # The bug this fixes
//
// MinSize returned a FLAT height of 7 — five content rows — however long
// the body was, and for a plain Overlay overlayRect treats MinSize as the
// ACTUAL rendered size. F7's one-line bodies fitted; F12's quit
// confirmation (UI-14) does not: it lists the running tasks, wraps to four
// lines, and the buttons and the key hint were rows six and seven. They
// were simply cut off, so the dialog showed no labels and no focus marker
// at all — while still responding to ←/→ and Enter, which is what made it
// read as a frozen box rather than a clipped one.
//
// A verification round had already fixed exactly this bug on the WIDTH axis
// (F9a's "Cancel it" / "Leave it running" overflowing a 40-cell row) and
// left the height a constant. Same defect, one axis over.
//
// # Why PreferredSize and not a bigger MinSize
//
// Because MinSize is also the refusal threshold (overlayFits), and
// F06-right-pane.md's own "Known limitation" note says so: raising it means
// terminals that can open the dialog today stop being able to. For a quit
// confirmation that is the worst possible trade — the dialog exists to warn
// about losing work, and on a short terminal it would vanish instead,
// taking the warning with it. That note asks for "a preferred-vs-minimum
// distinction in the Overlay contract"; F9a built it as MaximisedOverlay for
// the tray and the result card, and this is the same mechanism applied to
// the third surface that needed it. overlayRect caps the answer against the
// terminal, so a body too tall to fit is still clipped rather than refused
// — the previous behaviour, now reached only when it is genuinely necessary.
func (c *Confirm) PreferredSize(availW, availH int) (int, int) {
	w := c.naturalWidth()
	// The width View will wrap at, derived exactly as View derives it:
	// withOverlay passes rect.W-2, and View then takes another 2 for
	// fitCells' padding.
	textW := w - 4
	if textW < 1 {
		textW = 1
	}

	lines := 0
	for _, para := range strings.Split(c.body, "\n") {
		lines += len(wrapLine(para, textW))
	}
	// View's own layout after the body: one blank row, the button row, the
	// key hint. Plus 2 for the border.
	h := lines + 3 + 2
	if h < confirmFloorH {
		h = confirmFloorH
	}
	return w, h
}
func (c *Confirm) Title() string { return c.title }

func (c *Confirm) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	switch k.Type {
	case tea.KeyLeft, tea.KeyRight:
		c.focusYes = !c.focusYes
		return c, nil
	case tea.KeyEnter:
		if c.focusYes {
			return nil, c.onConfirm
		}
		return nil, nil
	case tea.KeyEsc:
		return nil, nil
	case tea.KeyRunes:
		switch strings.ToLower(string(k.Runes)) {
		case "y":
			return nil, c.onConfirm
		case "n":
			return nil, nil
		}
	}
	return c, nil
}

func (c *Confirm) View(th theme.Theme, w, h int) string {
	textW := w - 2 // see popup.go's View doc: box.go's fitCells pads one more cell each side
	if textW < 1 {
		textW = 1
	}

	// Word-wrapped, not padCells-truncated: the same "silent, no ellipsis"
	// cut that lost the description column's text (popup.go's renderRow
	// doc) was happening here too — the real quit-with-tasks message is
	// well over 40 columns and was simply losing its tail.
	var b strings.Builder
	for _, para := range strings.Split(c.body, "\n") {
		for _, line := range wrapLine(para, textW) {
			b.WriteString(th.Style(theme.TextPrimary).Render(padCells(line, textW)))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	b.WriteString(centre(c.renderButtons(th), textW))
	b.WriteByte('\n')
	b.WriteString(th.Style(theme.TextDim).Render(padCells("←/→ move   ⏎ confirm   esc cancel", textW)))
	return b.String()
}

// renderButtons draws the two choices, each hugging its own label with two
// cells of padding inside its own box, separated by buttonGap and centred as
// a pair by View.
//
// Focus is marked with turquoise's own accent colour — box.go's OWN
// convention for "this has focus" (BorderFocus is the same role, and
// THEME.md's rule 3 is explicit: focused border, active tab and the
// streaming indicator are the SAME accent) — rather than a filled
// background, which is one extra step of colour math a terminal that has
// fallen back from truecolor can get wrong in a way a plain accented
// foreground does not.
//
// # Why the two are no longer padded to a SHARED width
//
// They were, originally, so the pair would "read as one evenly-weighted
// control". That works when the labels are close in length ("Quit" /
// "Cancel") and falls apart when they are not. F9a's "Cancel it" /
// "Leave it running" differ by seven cells, so the shorter label floated in
// the middle of a 16-cell field — five spaces of slack on one side, six on
// the other — directly beside a bracketed box that hugged its own text. The
// result was a ragged middle and two boxes that looked like different kinds
// of thing.
//
// Hugging each label instead gives every button the same INTERNAL padding
// (two cells, bracketed or not), which is the property that actually reads
// as balanced. The focused and unfocused forms are still exactly the same
// width as each other — "[ " + l + " ]" and "  " + l + "  " are both l+4 —
// so the row's total width is still invariant under focus, which is what
// stops the pair shifting as you move between them, and is what
// TestConfirmButtonsAreEvenlyPadded actually measures.
func (c *Confirm) renderButtons(th theme.Theme) string {
	render := func(label string, focused bool) string {
		if focused {
			return th.Style(theme.AccentPrimary).Render("[ " + label + " ]")
		}
		return th.Style(theme.TextSecondary).Render("  " + label + "  ")
	}
	return render(c.yes, c.focusYes) + buttonGap + render(c.no, !c.focusYes)
}

// buttonGap separates the two buttons. Four cells: wide enough that the
// bracketed side reads as one control rather than running into its
// neighbour, narrow enough that the pair still reads as a pair.
const buttonGap = "    "

// wrapLine greedily packs words into lines no wider than width display
// cells. A single word wider than width is placed alone on its own line and
// left to padCells' own truncation at render time — pathological (it would
// take an unbroken run of 40+ non-space characters), never the ordinary
// case, and never silent in the sense that mattered here: an ordinary
// sentence now always reads in full.
func wrapLine(s string, width int) []string {
	if width < 1 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 2)
	cur := words[0]
	for _, w := range words[1:] {
		if runewidth.StringWidth(cur)+1+runewidth.StringWidth(w) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	return append(lines, cur)
}
