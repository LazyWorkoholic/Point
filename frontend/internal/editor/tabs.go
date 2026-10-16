package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// The tab strip: the FIRST CONTENT ROW inside the Code File box, not a
// nested box.
//
// LAYOUT.md is explicit about why -- "a box directly inside a parent border
// reads as clutter and costs two rows" -- and two rows is a real price at a
// 24-row terminal, where the code area is about eight lines to begin with.
//
// It is also a hit-testable row: mouse.go turns a click on it into a tab
// selection or a close, which is why renderTabs reports the spans it drew
// rather than only the string.

// tabSpan is where one tab landed on the strip, in cells from the strip's
// own left edge. mouse.go hit-tests against these rather than
// re-measuring the labels, so a click can never disagree with what was
// drawn -- the same rule app\root.go's overlayRect follows for the overlay.
type tabSpan struct {
	index int
	from  int
	to    int
	// closeAt is the cell holding this tab's `×`, or -1 when the strip is
	// too narrow to have drawn one.
	closeAt int
}

// tabsCollapseWidth is LAYOUT.md's "below 70 columns the tab strip
// collapses to the active filename only".
//
// It is measured against the Code File box's own content width rather than
// the terminal's, because that is what the strip actually has to fit in:
// at 70 columns with the right pane open the box is nowhere near 70 wide,
// and a strip sized off the terminal would overflow a box that was never
// that big. Below 70 columns the right pane is gone entirely (layout's own
// breakpoint), so the box IS roughly the terminal there -- which is the
// case the rule was written for.
const tabsCollapseWidth = 44

// renderTabs draws the strip and reports the spans it drew.
//
// Overflow scrolls horizontally with `‹` and `›` markers, keeping the
// active tab visible. The scroll offset is held by the workspace
// (tabScroll) rather than recomputed per frame, so the strip does not jump
// back to the start every time something else repaints.
func (w *Workspace) renderTabs(width int) (string, []tabSpan) {
	th := w.theme
	if len(w.docs) == 0 || width <= 0 {
		return "", nil
	}

	dim := th.Style(theme.TextDim)
	sep := dim.Render(borderVertical)

	if width < tabsCollapseWidth {
		label := w.tabLabel(w.active)
		return th.Style(theme.AccentPrimary).Render(truncateCells(label, width)),
			[]tabSpan{{index: w.active, from: 0, to: runewidth.StringWidth(label), closeAt: -1}}
	}

	// Build every tab's cells first, then window them. Measuring before
	// deciding what fits is the same ordering app\render.go's statusBar
	// uses, and for the same reason: a strip that renders and then cuts
	// produces a tab with half its name.
	type built struct {
		text  string
		plain string
		close int // offset of the × within this tab's own cells
	}
	tabs := make([]built, len(w.docs))
	for i := range w.docs {
		label := w.tabLabel(i)
		plain := " " + label + " × "
		style := dim
		if i == w.active {
			style = th.Style(theme.AccentPrimary)
		}
		tabs[i] = built{
			text:  style.Render(plain),
			plain: plain,
			close: 1 + runewidth.StringWidth(label) + 1,
		}
	}

	// Keep the active tab in view: widen the window from it outwards.
	avail := width - 2 // the two marker cells
	if avail < 8 {
		avail = width
	}
	first := w.tabScroll
	if first > w.active {
		first = w.active
	}
	for {
		used := 0
		last := first
		for i := first; i < len(tabs); i++ {
			step := runewidth.StringWidth(tabs[i].plain)
			if i > first {
				step++ // the separator
			}
			if used+step > avail {
				break
			}
			used += step
			last = i
		}
		if last >= w.active || first >= w.active {
			break
		}
		first++
	}
	w.tabScroll = first

	var b strings.Builder
	spans := make([]tabSpan, 0, len(tabs))
	col := 0

	if first > 0 {
		b.WriteString(dim.Render("‹"))
		col++
	}
	used := 0
	shownLast := first - 1
	for i := first; i < len(tabs); i++ {
		step := runewidth.StringWidth(tabs[i].plain)
		if i > first {
			step++
		}
		if used+step > avail {
			break
		}
		if i > first {
			b.WriteString(sep)
			col++
		}
		spans = append(spans, tabSpan{
			index:   i,
			from:    col,
			to:      col + runewidth.StringWidth(tabs[i].plain),
			closeAt: col + tabs[i].close,
		})
		b.WriteString(tabs[i].text)
		col += runewidth.StringWidth(tabs[i].plain)
		used += step
		shownLast = i
	}
	if shownLast < len(tabs)-1 {
		b.WriteString(dim.Render("›"))
	}

	return b.String(), spans
}

// tabLabel is one tab's text: the dirty marker, then the filename.
//
// The marker goes BEFORE the name so the strip does not reflow as files
// become dirty, and it is LAYOUT.md's `•`.
//
// A read-only file gets NO marker here, deliberately. The status bar
// already says read-only AND says why (doc.go's readOnlyReason), and a
// second glyph in the strip would either repeat that without the reason or
// invent a vocabulary -- the project has one set of glyphs and "read-only"
// is not in it.
func (w *Workspace) tabLabel(i int) string {
	d := w.docs[i]
	if d.dirty {
		return "• " + d.name()
	}
	return d.name()
}

// cycleTab moves the active tab by delta, wrapping.
// activeDoc is the document on screen, or nil. Added by F13b because
// three call sites now need "the document that was active BEFORE this
// message" and `w.docs[w.active]` panics on an empty set.
func (w *Workspace) activeDoc() *doc {
	if len(w.docs) == 0 {
		return nil
	}
	return w.docs[w.active]
}

func (w *Workspace) cycleTab(delta int) tea.Cmd {
	if len(w.docs) < 2 {
		return nil
	}
	prev := w.activeDoc()
	w.active = (w.active + delta + len(w.docs)) % len(w.docs)
	return w.autosaveOnFocusChange(prev)
}

// closeTab drops one document, after confirming if it is dirty.
//
// Done-when #9. Returns a message and, when the document is dirty, a
// tea.Cmd that opens the Confirm -- the close itself then happens on
// closeDirtyMsg, which is what the confirmation produces.
//
// # C3: the shape of that Confirm
//
//	[ Close ]  [ Cancel ]      Cancel focused
//
// Close DISCARDS. There is deliberately no third "Save and Close"
// option in v1, and no implicit save behind the Close button: a button
// that sometimes writes to disk and sometimes does not, depending on
// state the dialog does not show, is the kind of control people stop
// trusting. A user who wants to keep the edits cancels and presses
// ctrl+s, which is one extra keystroke for the only outcome that is
// irreversible.
//
// Cancel focused by default is F07's own rule and F12's pattern --
// "an Enter pressed out of habit never fires the destructive path".
func (w *Workspace) closeTab(i int) (string, tea.Cmd) {
	if i < 0 || i >= len(w.docs) {
		return "", nil
	}
	if w.docs[i].dirty {
		return "", w.confirmCloseDirty(w.docs[i])
	}
	return w.dropTab(i), nil
}

// confirmCloseDirty builds C3's two-button dialog.
//
// # It PAUSES this document's autosave first, and has to
//
// Found by gate_f13b step 5. The dialog asks a question whose whole
// point is whether these edits survive, and the window it is open for
// is longer than the autosave debounce -- so a tick armed by the last
// keystroke would land while the user was still reading, write the
// edits, and make both buttons meaningless: Close would discard a
// buffer whose contents were already on disk, and C3's "Close
// DISCARDS, no implicit save" would be false.
//
// Paused the same way every other refusal is -- autosaveBlocked, which
// armAutosave checks -- plus a generation bump, which is what retires
// the tick that is ALREADY in flight. The flag alone is not enough:
// armAutosave is consulted when a timer is SET, and the dangerous tick
// here was set a moment ago by the keystroke that made the file dirty.
// onAutosaveTick compares generations, so bumping it makes that tick a
// no-op on arrival.
//
// Cancel resumes it (see onCloseCancelled). Close makes it moot -- the
// document is gone, and docByPath then finds nothing for any tick that
// was still outstanding.
func (w *Workspace) confirmCloseDirty(d *doc) tea.Cmd {
	d.autosaveBlocked = true
	d.autosaveGen++

	path, name := d.path, d.name()
	body := name + " has unsaved changes.\n\n" +
		"Close discards them. Cancel, then ctrl+s, keeps them."
	if d.autosaveBlocked {
		// The user needs to know WHY it is still dirty with autosave
		// on, or "unsaved changes" reads as the editor having simply
		// failed to do its job.
		body += "\n\nAutosave is stopped for this file — see the status bar."
	}
	c := app.NewConfirm("Close without saving?", body, "Close", "Cancel",
		func() tea.Msg { return closeDirtyMsg{path: path} }).
		OnCancel(func() tea.Msg { return closeCancelledMsg{path: path} })
	return func() tea.Msg { return app.OpenWorkspaceOverlayMsg{Overlay: c} }
}

// closeCancelledMsg: the user kept the tab. Resume its autosave.
type closeCancelledMsg struct{ path string }

func (w *Workspace) onCloseCancelled(m closeCancelledMsg) tea.Cmd {
	d := w.docByPath(m.path)
	if d == nil || !d.dirty {
		return nil
	}
	// Unblocked and re-armed, so the edits the user just chose to keep
	// are written on the ordinary schedule rather than sitting unsaved
	// until the next keystroke happens to arm a new timer.
	d.autosaveBlocked = false
	return w.armAutosave(d)
}

// closeDirtyMsg is the confirmed discard.
type closeDirtyMsg struct{ path string }

func (w *Workspace) onCloseDirty(m closeDirtyMsg) tea.Cmd {
	for i, d := range w.docs {
		if d.path == m.path {
			return w.say(w.dropTab(i))
		}
	}
	return nil
}

// dropTab is the close itself, with no questions asked -- reached
// either from a clean document or from a confirmed discard.
func (w *Workspace) dropTab(i int) string {
	if i < 0 || i >= len(w.docs) {
		return ""
	}
	// The lock goes back before the document does. A closed file this
	// process still holds a lock on is the "waiting out my own lock"
	// case B7's staleness exists to recover from, not to be relied on.
	w.releaseLock(w.docs[i])
	name := w.docs[i].name()
	w.docs = append(w.docs[:i], w.docs[i+1:]...)
	if w.active >= len(w.docs) {
		w.active = len(w.docs) - 1
	}
	if w.active < 0 {
		w.active = 0
	}
	if w.tabScroll > 0 && w.tabScroll >= len(w.docs) {
		w.tabScroll = len(w.docs) - 1
	}
	return "closed " + name
}
