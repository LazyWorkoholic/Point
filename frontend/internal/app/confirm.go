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

func (c *Confirm) MinSize() (int, int) { return 40, 7 } // LAYOUT.md's overlay table: "confirm 40×7"
func (c *Confirm) Title() string       { return c.title }

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

// renderButtons pads Yes and No to the SAME width, so the pair reads as one
// evenly-weighted control rather than two differently-sized labels with an
// arbitrary gap, and marks focus with turquoise's own accent colour —
// box.go's OWN convention for "this has focus" (BorderFocus is the same
// role, and THEME.md's rule 3 is explicit: focused border, active tab and
// the streaming indicator are the SAME accent) — rather than a filled
// background, which is one extra step of colour math a terminal that has
// fallen back from truecolor can get wrong in a way a plain accented
// foreground does not.
func (c *Confirm) renderButtons(th theme.Theme) string {
	labelW := runewidth.StringWidth(c.yes)
	if w := runewidth.StringWidth(c.no); w > labelW {
		labelW = w
	}

	render := func(label string, focused bool) string {
		// CENTRED in the shared width, not left-aligned with the slack
		// dumped on the trailing side: padCells pads right, so "Quit" in
		// a 6-cell field rendered as "[ Quit   ]" beside "[ Cancel ]" —
		// same total width, visibly lopsided brackets. Splitting the
		// slack is what makes the two read as one control.
		label = centre(label, labelW)
		if focused {
			return th.Style(theme.AccentPrimary).Render("[ " + label + " ]")
		}
		return th.Style(theme.TextSecondary).Render("  " + label + "  ")
	}
	return render(c.yes, c.focusYes) + "    " + render(c.no, !c.focusYes)
}

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
