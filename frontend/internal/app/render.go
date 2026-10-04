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

// View renders the whole frame — or, while a paste burst is still
// assembling, re-serves the last one.
//
// Bubble Tea calls View after EVERY Update (tea.go's eventLoop:
// `model.Update(msg)` then `p.renderer.write(model.View())`), and on
// Windows a paste arrives as one KeyMsg per pasted character (paste.go).
// A 391-line paste is therefore ~13,000 full re-renders of a frame whose
// visible content cannot have changed: the pasted text is still
// accumulating in pasteState.buf and reaches the workspace only at flush.
// Measured, that frame costs ~4.4ms with a large session and a grown input
// — several seconds of pure wasted rendering per paste, and it gets worse
// as the input buffer grows because the whole buffer is re-wrapped per
// frame.
//
// That backlog is not just slow, it is CORRECTING: bubbletea's msgs
// channel is unbuffered, so while the loop renders, key records queue in
// the OS console buffer and are then drained back-to-back. paste.go's
// burst detector times keys at UPDATE time, not arrival time, so a backlog
// makes ordinary typing look like a paste (a verification round's Bug 4)
// and, worse, makes a real paste's own records look far enough apart to
// end the burst early — after which its trailing newline arrives as a
// LONE Enter and reaches Send (Bug 1). Not rendering a frame nobody can
// see is what keeps the loop ahead of the input.
//
// Suppression covers a merely HELD key as well as an active burst, and that
// is not an optimisation -- it is what stops the failure from re-arming
// itself. A burst only ACTIVATES when two pasteable keys are processed
// within pasteBurstGap (6ms) of each other, and that is measured at Update
// time. If rendering one keystroke costs more than the gap, the second
// pasted character always looks too far apart to belong to a paste, the
// burst never activates, suppression never engages, and the paste degrades
// into thousands of individually-rendered keystrokes until the console
// input buffer overflows. Measured, one keystroke reached ~5ms at 16k runes
// in the input and passed 6ms at 33k, which is exactly the ~229-line
// ceiling a verification round found. Skipping the frame for the 6ms a key
// is held costs nothing visible -- the release tick repaints it -- and
// makes activation independent of how expensive the frame is.
func (r *Root) View() string {
	if r.w == 0 || r.h == 0 {
		return ""
	}
	if r.pasteInFlight() && r.lastFrame != "" {
		return r.lastFrame
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
	r.lastFrame = screen
	return screen
}

// animFrames is the header's activity indicator: one bright diamond
// travelling across three dim ones. Every frame is the same five display
// cells wide, so the centred title never shifts as it animates.
//
// animFrames[0] is also the IDLE frame, and is exactly what the header drew
// before F5 — an untouched app looks identical to how it always did.
var animFrames = []string{"◇ ◈ ◇", "◈ ◇ ◇", "◇ ◈ ◇", "◇ ◇ ◈"}

// animFrame is the current frame. When nothing is streaming this is frozen
// wherever the last tick left it (root.go's animTickMsg case) rather than
// reset — F05-streaming-queue.md wants the animation to be an activity
// indicator, and an indicator that snaps home on completion is telling the
// user about the reset, not about the work.
func (r *Root) animFrame() string {
	return animFrames[r.anim.frame%len(animFrames)]
}

// header is a single UNBOXED line: animation left, POINT centre, trademark
// right. Below 30 rows it collapses to the title only.
func (r *Root) header(l layout.Layout) string {
	th := r.theme
	width := l.Header.W

	title := th.Style(theme.AccentPrimary).Render("P O I N T")
	badge := r.demoBadge()

	if l.HeaderCompact {
		if badge != "" {
			title = badge + " " + title
		}
		return centre(title, width)
	}

	left := th.Style(theme.TextDim).Render(r.animFrame())
	right := th.Style(theme.TextDim).Render("◈ " + r.connWord())
	if badge != "" {
		// Folded into right BEFORE its width is measured below, so the
		// existing gap math (which only ever knew about left/title/right)
		// accounts for it automatically — no fourth segment to thread
		// through gapL/gapR separately.
		right = badge + "  " + right
	}

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

// demoBadge is D55's "it must announce itself" (F10, F10c): a persistent
// marker shown in the header for as long as Demo mode is the active
// provider — in both the normal and compact header layouts (header,
// above). theme.Warning is the fixed, outside-the-palette role every other
// "this must read as distinct regardless of theme" surface in this
// codebase already uses (behaviour.go's inert-key icon, credential.go's
// validation errors, placeholder.go's diagnostics notes), picked for the
// same reason here: a badge that could blend into some future palette
// would not be announcing anything.
func (r *Root) demoBadge() string {
	if r.activeProvider == nil || r.activeProvider.Provider != "demo" {
		return ""
	}
	return r.theme.Style(theme.Warning).Render("● DEMO")
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
	rect := r.overlayRect() // root.go: the SAME rect dispatchMouse hit-tests against
	panel := box(r.theme, rect, o.Title(), o.View(r.theme, rect.W-2, rect.H-2), true)

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
