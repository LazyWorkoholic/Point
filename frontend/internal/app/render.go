package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// Watermark is what replaces the header below 24 rows: dim, bottom-right,
// overlaid rather than given a row of its own.
const watermarkText = "◈ point"

func (r *Root) View() string {
	if r.w == 0 || r.h == 0 {
		return ""
	}
	if r.layout.W != r.w || r.layout.H != r.h {
		r.recompute()
	}
	l := r.layout

	if l.TooSmall {
		return r.tooSmall(l.Message)
	}

	rows := make([]string, 0, r.h)
	rows = append(rows, "") // top margin

	if l.ShowHeader {
		rows = append(rows, indent(r.header(l), layout.MarginX))
	}

	body := r.current().View(l)
	for _, line := range strings.Split(body, "\n") {
		rows = append(rows, indent(line, layout.MarginX))
	}

	rows = append(rows, indent(r.statusBar(l), layout.MarginX))
	rows = append(rows, "") // bottom margin

	// Pad or clip to exactly the terminal height so a stale frame cannot
	// leave artefacts behind after a resize.
	for len(rows) < r.h {
		rows = append(rows, "")
	}
	rows = rows[:r.h]

	screen := strings.Join(rows, "\n")

	if r.overlay != nil {
		screen = r.withOverlay(screen)
	}
	return screen
}

// header is a single UNBOXED line: animation left, POINT centre, trademark
// right. Below 30 rows it collapses to the title only.
func (r *Root) header(l layout.Layout) string {
	th := r.theme
	width := l.Header.W

	title := th.Style(theme.AccentPrimary).Render("P O I N T")

	if l.HeaderCompact {
		return centre(title, width)
	}

	left := th.Style(theme.TextDim).Render("◇ ◈ ◇")
	right := th.Style(theme.TextDim).Render("◈ " + r.connWord())

	lw := visibleWidth(left)
	rw := visibleWidth(right)
	tw := visibleWidth(title)

	gapL := (width-tw)/2 - lw
	if gapL < 1 {
		gapL = 1
	}
	gapR := width - lw - gapL - tw - rw
	if gapR < 1 {
		gapR = 1
	}
	line := left + strings.Repeat(" ", gapL) + title + strings.Repeat(" ", gapR) + right
	return padCells(line, width)
}

func (r *Root) connWord() string {
	switch r.conn.state {
	case "":
		return "idle"
	default:
		return string(r.conn.state)
	}
}

// statusBar is an unboxed line at the bottom: bindings on the left, state on
// the right. The failure banner lives here too — a status-bar line with a
// retry action, never a modal, so the rest of the app stays usable.
func (r *Root) statusBar(l layout.Layout) string {
	th := r.theme
	width := l.StatusBar.W

	var left string
	switch {
	case r.leader.pending && r.leader.showHint:
		left = th.Style(theme.AccentPrimary).Render(r.leaderHint())
	case r.conn.banner != "":
		left = th.Style(theme.Error).Render("! "+r.conn.banner) + " " +
			th.Style(theme.TextDim).Render(r.retryHint())
	case r.status != "":
		left = th.Style(theme.TextSecondary).Render(r.status)
	default:
		left = th.Style(theme.TextDim).Render(r.bindingsHint())
	}

	right := th.Style(theme.TextDim).Render(fmt.Sprintf(
		"%s · %d task(s) · %d×%d", r.connWord(), len(r.conn.inFlight), r.w, r.h))

	if l.ShowWatermark {
		right = th.Style(theme.Watermark).Render(watermarkText)
	}

	gap := width - visibleWidth(left) - visibleWidth(right)
	if gap < 1 {
		// The right half is the shorter and the more perishable; drop it
		// rather than let the line wrap and cost a row.
		return padCells(truncateCells(left, width), width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (r *Root) bindingsHint() string {
	parts := make([]string, 0, 3)
	for _, id := range []string{"app.palette", "app.quit", "ui.help"} {
		a, ok := r.actions.ByID(id)
		if !ok {
			continue
		}
		switch {
		case a.Chord != "":
			parts = append(parts, a.Chord+" "+strings.ToLower(a.Name))
		case a.Leader != "":
			parts = append(parts, actions.Leader+" "+a.Leader+" "+strings.ToLower(a.Name))
		}
	}
	return strings.Join(parts, "   ")
}

// leaderHint is the hint bar shown after a short delay, built from the
// registry — never from a hardcoded list.
func (r *Root) leaderHint() string {
	var parts []string
	for _, a := range r.actions.LeaderHints() {
		if r.leader.sequence != "" && !strings.HasPrefix(a.Leader, r.leader.sequence) {
			continue
		}
		if !a.Available(r) {
			continue
		}
		parts = append(parts, a.Leader+" "+a.Name)
	}
	if len(parts) == 0 {
		return actions.Leader + r.leader.sequence + " — nothing bound"
	}
	return actions.Leader + " " + r.leader.sequence + "   " + strings.Join(parts, "   ")
}

// tooSmall is the whole screen when the terminal is below a floor.
func (r *Root) tooSmall(message string) string {
	rows := make([]string, r.h)
	line := r.theme.Style(theme.Warning).Render(message)
	mid := r.h / 2
	for i := range rows {
		if i == mid {
			rows[i] = centre(line, r.w)
		} else {
			rows[i] = ""
		}
	}
	return strings.Join(rows, "\n")
}

// withOverlay draws the overlay centred over a dimmed screen.
//
// The scrim is applied by re-rendering the underlying text in the dim role
// rather than by compositing a translucent layer, which a terminal cannot do.
func (r *Root) withOverlay(screen string) string {
	o := r.overlay
	minW, minH := o.MinSize()

	w := minW
	if max := r.w - 8; w > max && max > 10 {
		w = max
	}
	h := minH
	if max := r.h - 4; h > max && max > 4 {
		h = max
	}

	rect := layout.Rect{
		X: (r.w - w) / 2,
		Y: (r.h - h) / 2,
		W: w, H: h,
	}
	panel := box(r.theme, rect, o.Title(), o.View(r.theme, w-2, h-2), true)

	// The scrim flattens the whole frame to one dim colour, so everything
	// behind the panel can be composited as PLAIN TEXT and re-styled once per
	// segment. That is what makes the splice column-exact: no escape sequence
	// is ever cut in half, because none is present while the cutting happens.
	base := strings.Split(screen, "\n")
	scrim := r.theme.Style(theme.TextDim)
	plain := make([]string, len(base))
	for i := range base {
		plain[i] = padCells(stripANSI(base[i]), r.w)
		base[i] = scrim.Render(plain[i])
	}

	panelLines := strings.Split(panel, "\n")
	for i, line := range panelLines {
		row := rect.Y + i
		if row < 0 || row >= len(base) {
			continue
		}
		base[row] = spliceRow(plain[row], line, rect.X, r.w, scrim)
	}
	return strings.Join(base, "\n")
}

// spliceRow puts `patch` into `plain` starting at display column x, keeping
// the row's own content on either side of it.
//
// Both halves are cut by VISIBLE COLUMN from unstyled text and re-styled
// afterwards. Cutting a styled string by byte or rune index — which is what
// this used to do — severs an escape sequence and leaks an unterminated colour
// into everything after it on the row, which is why the corruption showed up
// on borders the overlay never covered.
func spliceRow(plain, patch string, x, width int, style lipgloss.Style) string {
	patchWidth := visibleWidth(patch)

	var b strings.Builder
	if x > 0 {
		b.WriteString(style.Render(sliceCells(plain, 0, x)))
	}
	b.WriteString(patch)
	if end := x + patchWidth; end < width {
		b.WriteString(style.Render(sliceCells(plain, end, width)))
	}
	return b.String()
}

// sliceCells returns the columns [from, to) of an UNSTYLED string, padded to
// exactly to-from cells.
//
// A double-width rune straddling either boundary becomes a space for the cells
// that fall inside the range — half a wide character is not a character, and
// printing it would shift every column after it.
func sliceCells(s string, from, to int) string {
	if to <= from {
		return ""
	}
	var (
		b   strings.Builder
		col int
	)
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if col >= to {
			break
		}
		switch {
		case col >= from && col+w <= to:
			b.WriteRune(r)
		case col+w > from:
			// Straddles a boundary: emit spaces for the covered cells only.
			lo, hi := max(col, from), min(col+w, to)
			b.WriteString(strings.Repeat(" ", hi-lo))
		}
		col += w
	}
	return padCells(b.String(), to-from)
}

func indent(s string, n int) string {
	return strings.Repeat(" ", n) + s
}

func centre(s string, width int) string {
	w := visibleWidth(s)
	if w >= width {
		return truncateCells(s, width)
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-w-left)
}
