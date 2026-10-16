package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// The stale-save refusal UI (C1, C2, B10).
//
//	save   if the file changed on disk since open -> REFUSE:
//	       "changed on disk since you opened it — [reload] [overwrite] [diff]"
//
// # Why this is not a Confirm (C1)
//
// Confirm is TWO buttons by construction -- focusYes is a bool, and its
// whole shape (yes/no, No focused by default) is built around a binary
// destructive choice. This refusal has three outcomes and none of them
// is "no": reload discards your edits, overwrite discards theirs, and
// diff is how you decide which. Widening Confirm to n buttons to serve
// one caller would make every existing two-button dialog pay for it, so
// this is a narrow overlay purpose-built for this one case, exactly as
// C1 settles it.
//
// # Why closing [diff] comes back here (C2)
//
// The save is still refused and still unresolved. Returning to the
// editor on esc would leave the buffer dirty, the file changed on disk,
// and nothing on screen saying so -- the user would have asked "what
// changed?", been told, and then had the question silently dropped.
// Overlay.Update returning a DIFFERENT overlay swaps it in place
// (app\root.go's setOverlay, the mechanism S13d's context card already
// uses), so the diff view holds a pointer back to the dialog and returns
// it.

// staleAction is what the user chose. It travels as a tea.Msg rather
// than as a closure over the workspace, because an Overlay's Update runs
// inside Root's Update and any callback it fired would be mutating a
// workspace from code that does not own it. The editor handles these in
// its own Update, where it does.
type staleReloadMsg struct{ path string }
type staleOverwriteMsg struct{ path string }

// staleDialog is the three-button refusal.
type staleDialog struct {
	path string
	name string
	// diskMtime is the mtime the REFUSAL reported -- the file's current
	// one, not the one we sent. B32's eyes-on 1 confirms the backend
	// reports the current value rather than echoing expected_mtime, and
	// it is what makes "changed at ..." a true statement.
	diskMtime float64
	// disk is the file's content as of the refusal, fetched for [diff].
	// Empty until the re-read lands; the diff view says so rather than
	// rendering an empty file as "everything deleted".
	disk    string
	diskErr string
	buffer  string

	focus int // 0 reload, 1 overwrite, 2 diff
}

const (
	staleReload = iota
	staleOverwrite
	staleDiff
)

var staleLabels = [...]string{"reload", "overwrite", "diff"}

func newStaleDialog(d *doc, diskMtime float64) *staleDialog {
	return &staleDialog{
		path:      d.path,
		name:      d.name(),
		diskMtime: diskMtime,
		buffer:    d.buf.Text(),
		// reload is focused by default, and that is Confirm's "No is
		// focused by default" rule carried over rather than ignored:
		// of the three, reload is the one that cannot destroy work
		// belonging to someone who is not in the room. It discards
		// YOUR unsaved edits, which you still have in undo history
		// after a reload replaces the buffer in one splice; overwrite
		// discards a change on disk that nothing here can recover.
		focus: staleReload,
	}
}

// staleBody is the dialog's text, as rows, so MinSize, PreferredSize
// and View all count the SAME rows.
//
// It is a method returning the lines rather than three places that each
// know how tall the dialog is. That duplication is what produced the
// rendering bug below, and a shared accessor is what stops it coming
// back the next time a line is added.
func (s *staleDialog) staleBody() []string {
	return []string{
		s.name + " changed on disk since you opened it.",
		"",
		"Nothing has been written. Choose what to keep:",
		"  reload     throw away your edits, take the file on disk",
		"  overwrite  throw away the change on disk, write your buffer",
		"  diff       see what is different first",
	}
}

// chromeRows is what View adds below the body: a blank row, the button
// row, and the key hint.
const chromeRows = 3

// MinSize is the REFUSAL threshold -- the size below which Root declines
// to open this at all -- not the rendered size. See PreferredSize.
//
// Width is measured from the button row rather than guessed, which is
// the trap Confirm's own naturalWidth doc records: a widget that
// truncates its only two controls is broken for every future caller.
// Three bracketed labels plus two gaps do not fit forty cells.
func (s *staleDialog) MinSize() (int, int) {
	w := 4 // box.go's fitCells padding plus the border
	for i, l := range staleLabels {
		w += len(l) + 4
		if i > 0 {
			w += len(buttonGapCells)
		}
	}
	if w < staleMinW {
		w = staleMinW
	}
	// The floor stays LAYOUT.md's "confirm 40x7" height. Raising it
	// would mean terminals that can open this today stop being able to,
	// and a dialog that warns about losing work must not vanish instead
	// -- F06's own "Known limitation" note, and Confirm's reason for
	// splitting the two sizes.
	return w, confirmFloorRows
}

// staleMinW and confirmFloorRows: the dialog's own floors.
const (
	staleMinW        = 60
	confirmFloorRows = 7
)

// PreferredSize is what makes this a MaximisedOverlay, and it is the fix
// for a real rendering bug the gate found.
//
// # What was wrong
//
// MinSize returned a flat height of 9. For a plain Overlay, overlayRect
// treats MinSize as the ACTUAL rendered size (overlay.go), so the box
// got 9 - 2 = SEVEN content rows -- while View emits nine. Measured, by
// rendering it:
//
//	row 0..5  the body
//	row 6     the blank
//	row 7     "[ reload ]   overwrite   diff"      <- CUT
//	row 8     "<-/-> move  return choose  ..."     <- CUT
//
// Both reported symptoms fall out of that one number. The three options
// "render as plain unbracketed text" because rows 3-5 ARE plain
// unbracketed text -- the body's own explanatory list -- and the
// bracketed, accented button row that would have shown which one had
// focus was below the fold. The missing footer is the same cut, one row
// further down.
//
// So it was neither a width bug nor a generic popup component rendering
// the wrong convention: this is a bespoke overlay whose declared height
// was two rows short of its own output.
//
// # Why PreferredSize rather than a taller MinSize
//
// Exactly Confirm's reasoning, which fixed this identical defect on
// this identical axis ("MinSize returned a FLAT height of 7 ... Same
// defect, one axis over"). MinSize is also the refusal threshold, so
// raising it shrinks the set of terminals that can show the dialog at
// all. PreferredSize lets it ask for what it needs and leaves the floor
// alone; overlayRect clamps the answer to [MinSize, terminal], so a
// terminal too short to fit clips rather than refuses -- the previous
// behaviour, now reached only when it is genuinely necessary.
func (s *staleDialog) PreferredSize(availW, availH int) (int, int) {
	w, _ := s.MinSize()
	// ...and the same accounting on the WIDTH axis, for the same reason.
	// The explanatory lines are what tell the user what each button
	// DOES, so losing their tails to padCells is losing the only place
	// the difference between reload and overwrite is written down.
	// Measured from the longest line rather than assumed: the widest is
	// the overwrite row at 61 cells, which a 60-cell floor cuts.
	for _, line := range s.staleBody() {
		if need := visibleWidth(line) + 4; need > w {
			w = need
		}
	}
	if availW > 0 && w > availW {
		w = availW
	}
	h := len(s.staleBody()) + chromeRows + 2 // +2 for the border
	if h < confirmFloorRows {
		h = confirmFloorRows
	}
	return w, h
}

func (s *staleDialog) Title() string { return "Changed on disk" }

func (s *staleDialog) Update(msg tea.Msg) (app.Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch k.Type {
	case tea.KeyLeft:
		if s.focus > 0 {
			s.focus--
		}
		return s, nil
	case tea.KeyRight:
		if s.focus < len(staleLabels)-1 {
			s.focus++
		}
		return s, nil
	case tea.KeyEsc:
		// Dismissing leaves the save refused and the buffer dirty,
		// which is the honest outcome: nothing was decided. The status
		// bar still shows the dirty marker and ctrl+s reopens this.
		return nil, nil
	case tea.KeyEnter:
		return s.choose(s.focus)
	case tea.KeyRunes:
		switch strings.ToLower(string(k.Runes)) {
		case "r":
			return s.choose(staleReload)
		case "o":
			return s.choose(staleOverwrite)
		case "d":
			return s.choose(staleDiff)
		}
	}
	return s, nil
}

func (s *staleDialog) choose(what int) (app.Overlay, tea.Cmd) {
	switch what {
	case staleReload:
		path := s.path
		return nil, func() tea.Msg { return staleReloadMsg{path: path} }
	case staleOverwrite:
		path := s.path
		return nil, func() tea.Msg { return staleOverwriteMsg{path: path} }
	default:
		// C2: the diff REPLACES this overlay and holds a way back to it.
		return newDiffView(s), nil
	}
}

func (s *staleDialog) View(th theme.Theme, w, h int) string {
	textW := w - 2
	if textW < 1 {
		textW = 1
	}
	var b strings.Builder
	for _, line := range s.staleBody() {
		b.WriteString(th.Style(theme.TextPrimary).Render(padCells(truncateCells(line, textW), textW)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(centreCells(s.renderButtons(th), textW))
	b.WriteByte('\n')
	// Short enough to fit the 60-cell floor less its four cells of box
	// and padding. The previous hint was 57 characters against a 50-cell
	// budget and lost its tail to padCells' silent cut -- the same
	// no-ellipsis truncation popup.go's renderRow doc warns about, and
	// a key hint that stops mid-word is worse than a shorter one.
	b.WriteString(th.Style(theme.TextDim).Render(
		padCells(truncateCells("←/→ move   ⏎ choose   r/o/d direct   esc cancel", textW), textW)))
	return b.String()
}

// buttonGapCells matches confirm.go's buttonGap. Duplicated rather than
// exported from app for the reason textutil.go's header already records
// about the width primitives: this package cannot import an unexported
// const, and a four-space gap is not a shared abstraction worth a new
// package on its own. Noted so the eventual `ui` package takes both.
const buttonGapCells = "    "

func (s *staleDialog) renderButtons(th theme.Theme) string {
	var out []string
	for i, l := range staleLabels {
		if i == s.focus {
			out = append(out, th.Style(theme.AccentPrimary).Render("[ "+l+" ]"))
		} else {
			out = append(out, th.Style(theme.TextSecondary).Render("  "+l+"  "))
		}
	}
	return strings.Join(out, buttonGapCells)
}

// ---------------------------------------------------------------------------
// [diff]
// ---------------------------------------------------------------------------

// diffView is B10: a plain unified diff of buffer-vs-disk, with NO apply
// mechanism.
//
// Deliberately not named like F9b's diff card, per CLAUDE.md's rule
// about reusing a later episode's name: F9b owns `DiffCard`, which is an
// interactive review surface with an apply button and an edit mode. This
// is a read-only view that exists to answer one question before the user
// presses a button on the dialog behind it. The two will coexist, and
// the one that got here first must not take the name.
type diffView struct {
	back  *staleDialog
	lines []string
	top   int
}

func newDiffView(back *staleDialog) *diffView {
	v := &diffView{back: back}
	switch {
	case back.diskErr != "":
		v.lines = []string{"could not re-read the file: " + back.diskErr}
	case back.disk == "" && back.diskMtime == 0:
		v.lines = []string{"the file on disk has not been re-read yet"}
	default:
		v.lines = unifiedDiff(back.disk, back.buffer, "on disk", "your buffer")
	}
	return v
}

func (v *diffView) MinSize() (int, int) { return 70, 20 } // LAYOUT.md's diff card size

func (v *diffView) Title() string { return "Diff — " + v.back.name }

// PreferredSize makes this a MaximisedOverlay for the same reason the
// task tray and the result card are: it is a CONTENT surface, and a diff
// read through a 70-column window wraps lines that are meaningful
// exactly because they are not wrapped.
func (v *diffView) PreferredSize(availW, availH int) (int, int) { return availW, availH }

func (v *diffView) Update(msg tea.Msg) (app.Overlay, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return v, nil
	}
	switch k.String() {
	case "esc", "q", "d":
		// C2: back to the dialog, NOT to the editor. The save is still
		// refused and still unresolved.
		return v.back, nil
	case "up", "k":
		if v.top > 0 {
			v.top--
		}
	case "down", "j":
		if v.top < len(v.lines)-1 {
			v.top++
		}
	case "pgup":
		v.top -= 10
		if v.top < 0 {
			v.top = 0
		}
	case "pgdown":
		v.top += 10
		if v.top > len(v.lines)-1 {
			v.top = len(v.lines) - 1
		}
	case "home":
		v.top = 0
	}
	return v, nil
}

func (v *diffView) View(th theme.Theme, w, h int) string {
	textW := w - 2
	if textW < 1 {
		textW = 1
	}
	rows := h - 1
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder
	for i := 0; i < rows-1; i++ {
		n := v.top + i
		if n >= len(v.lines) {
			b.WriteString(padCells("", textW))
			b.WriteByte('\n')
			continue
		}
		line := v.lines[n]
		b.WriteString(th.Style(diffRole(line)).Render(padCells(truncateCells(line, textW), textW)))
		b.WriteByte('\n')
	}
	b.WriteString(th.Style(theme.TextDim).Render(padCells(
		fmt.Sprintf("↑/↓ scroll   esc back to [reload] [overwrite] [diff]   %d lines", len(v.lines)),
		textW)))
	return b.String()
}

// diffRole colours a unified-diff line from the semantic palette.
//
// THEME.md roles, not literal red and green: a palette that has no red
// still has to render a diff, and NO_COLOR has to render it as plain
// text that is still readable because the +/- characters carry the
// meaning on their own.
func diffRole(line string) theme.Role {
	switch {
	case strings.HasPrefix(line, "+"):
		return theme.Done
	case strings.HasPrefix(line, "-"):
		return theme.Error
	case strings.HasPrefix(line, "@@"):
		return theme.AccentPrimary
	default:
		return theme.TextSecondary
	}
}
