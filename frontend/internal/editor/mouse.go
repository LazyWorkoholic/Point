package editor

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// Mouse: drag selection in the text area, clicks on the tab strip, clicks
// and the wheel in the file tree.
//
// Mouse capture is on for the whole app (main.go's WithMouseCellMotion),
// which disables Windows Terminal's own selection. F13 is explicit that
// this makes selection rendering the editor's job rather than something
// mouse support gives free -- and it is needed anyway for the Explainer.
//
// Coordinates arrive in SCREEN space, the same frame dispatchMouse works
// in. They are translated against rects this package recorded during
// View, never re-derived: render.go stores textArea, tabRow/tabSpans and
// treeRect as it draws them, so a click resolves against the geometry that
// was actually on screen.

func (w *Workspace) handleMouse(m tea.MouseMsg) tea.Cmd {
	switch m.Action {
	case tea.MouseActionPress:
		return w.mousePress(m)
	case tea.MouseActionMotion:
		// A motion event only arrives while a button is held, and only a
		// left-drag means anything here.
		if m.Button == tea.MouseButtonLeft && w.dragging {
			w.dragTo(m.X, m.Y)
		}
		return nil
	case tea.MouseActionRelease:
		w.dragging = false
		return nil
	}
	return nil
}

func (w *Workspace) mousePress(m tea.MouseMsg) tea.Cmd {
	switch m.Button {
	case tea.MouseButtonWheelUp:
		return w.wheelBy(m, -wheelStep)
	case tea.MouseButtonWheelDown:
		return w.wheelBy(m, +wheelStep)
	case tea.MouseButtonLeft:
	default:
		return nil
	}

	// The tab strip first: it is one row and sits inside the Code File
	// box, so a click on it would otherwise be read as a click on the
	// text area's top line. Hit-tested on BOTH axes -- see tabStrip.
	if inRect(w.tabStrip, m.X, m.Y) {
		return w.clickTabs(m.X)
	}

	if inRect(w.treeRect, m.X, m.Y) {
		return w.clickTree(m.X, m.Y)
	}

	if inRect(w.textArea, m.X, m.Y) && len(w.docs) > 0 {
		w.focus = focusBuffer
		d := w.docs[w.active]
		// The click, resolved, in one log line.
		//
		// F15's gate reported the caret landing a line below the row
		// clicked, and it could not be reproduced from synthesised
		// events: a sweep that clicks EVERY visible row and compares the
		// caret against the line number the gutter actually printed finds
		// no mismatch, with or without a ghost, scrolled or not
		// (rowmap_test.go). The one input this package cannot synthesise
		// is the terminal's own mouse coordinate, so this records what
		// arrived and what was made of it -- the same approach that
		// settled "what wrote app.py" after three rounds of theories.
		line, injected, blockRow := w.viewIndex(d).at(d.top + m.Y - w.textArea.Y)
		w.log.Debug("editor: click resolved",
			"screen_y", m.Y, "text_area_y", w.textArea.Y,
			"row_offset", m.Y-w.textArea.Y, "top_row", d.top,
			"line", line+1, "injected", injected, "block_row", blockRow)

		if injected {
			// A ghost row or the fill prompt. Not a line in the file, so
			// there is no caret position to move to -- the focus change
			// above is the whole of the response. Snapping to the
			// nearest real line instead would move the caret somewhere
			// the user did not click.
			return nil
		}
		d.moveTo(w.posAt(d, m.X, m.Y), false)
		// The anchor is planted even though nothing is selected yet, so
		// the first motion event extends from where the press landed
		// rather than from wherever the caret happened to be before.
		d.anchor = d.cursor
		d.selecting = true
		w.dragging = true
		return nil
	}
	return nil
}

// posAt turns a screen cell into a buffer position.
//
// # One row sequence, one conversion
//
// render.go emits rows the BUFFER does not have: the ghost text of a live
// fill, and the inline fill prompt. They are entries in the document's row
// sequence (viewrows.go), so a screen row is `d.top + rowOffset` in that
// sequence and `viewIndex.at` says which line it is -- the SAME call the
// renderer made to decide what to draw there. F15's gate hit the version of
// this that counted screen rows as lines: clicking what looked like line 7
// put the caret past line 12.
//
// This was re-derived for the unified model and is not the old arithmetic
// carried over. The previous version subtracted an injected-row count that
// was itself computed from the ghost's own scroll offset, which only held
// while the ghost could never be above the top of the pane. It can be now
// -- scrolling past a region is the point of the rewrite -- and `at` is
// correct for that case by construction, because it is the same walk in
// both directions.
//
// A click on an injected row itself has NO buffer position -- it is not in
// the file. posAt still has to return something (every caller wants a Pos),
// so it returns the line the block hangs off, which is where a caret put
// there would sit anyway. The CALLER is what declines to move the caret at
// all (see mousePress), so a click on a ghost row does nothing rather than
// snapping somewhere arbitrary.
func (w *Workspace) posAt(d *doc, x, y int) Pos {
	line, _, _ := w.viewIndex(d).at(d.top + y - w.textArea.Y)
	if line < 0 {
		line = 0
	}
	if line >= d.buf.Lines() {
		line = d.buf.Lines() - 1
	}
	cell := d.left + (x - w.textArea.X)
	if cell < 0 {
		cell = 0
	}
	return Pos{Line: line, Col: colAtDisplay(d.buf.Runes(line), cell)}
}

// dragTo extends the selection to the pointer, scrolling when it leaves the
// text area.
//
// Dragging past the top or bottom edge scrolls by one line per event rather
// than jumping to the pointer's notional line: the pointer is outside the
// box, so there is no line under it, and one line per event is what makes
// a drag off the edge feel like a drag rather than a teleport.
func (w *Workspace) dragTo(x, y int) {
	if len(w.docs) == 0 {
		return
	}
	d := w.docs[w.active]
	switch {
	case y < w.textArea.Y:
		d.scrollBy(w.viewIndex(d), -1)
		y = w.textArea.Y
	case y >= w.textArea.Y+w.textArea.H:
		d.scrollBy(w.viewIndex(d), +1)
		y = w.textArea.Y + w.textArea.H - 1
	}
	d.cursor = d.buf.Clamp(w.posAt(d, x, y))
	d.selecting = true
	d.hist.breakGroup()
	d.desiredValid = false
}

// wheelStep is how many rows one notch of the wheel moves.
const wheelStep = 3

// wheelBy scrolls whichever card the pointer is over.
//
// # The follow, and why a scroll down is never eaten any more
//
// Any manual scroll stops the auto-follow, exactly as scrolling up out of
// Chat's transcript unpins it. The follow RESUMES only when a scroll down
// CROSSES the live edge from above -- not merely when it ends up at or past
// it.
//
// That distinction is the whole fix for "scrolling down gets stuck inside
// the ghost". The previous version re-armed the follow whenever a scroll
// down reached the ghost's own last row, so the next frame pulled the
// viewport straight back to the region and the code after the insertion
// point was unreachable. Crossing is a one-time event: once the follow has
// pinned the view to the edge, the next notch starts AT the edge, does not
// cross it, and carries on down into the rest of the file.
func (w *Workspace) wheelBy(m tea.MouseMsg, rows int) tea.Cmd {
	if inRect(w.treeRect, m.X, m.Y) {
		w.tree.top += rows
		if w.tree.top < 0 {
			w.tree.top = 0
		}
		if max := len(w.tree.rows) - 1; w.tree.top > max && max >= 0 {
			w.tree.top = max
		}
		return nil
	}
	d := w.activeDoc()
	if d == nil {
		return nil
	}
	v := w.viewIndex(d)
	target, haveEdge := w.followTarget(d, v, w.textArea.H, w.textArea.W)

	before := d.top
	w.ghostFollow = false
	d.scrollBy(v, rows)
	if haveEdge && rows > 0 && before < target && d.top >= target {
		w.ghostFollow = true
	}
	return nil
}

func (w *Workspace) clickTabs(x int) tea.Cmd {
	// x is a screen column; the spans were recorded in the strip's own
	// coordinate space, whose origin is the rect render.go recorded.
	rel := x - w.tabStrip.X
	for _, s := range w.tabSpans {
		if rel < s.from || rel >= s.to {
			continue
		}
		if s.closeAt >= 0 && rel == s.closeAt {
			note, cmd := w.closeTab(s.index)
			return tea.Batch(w.say(note), cmd)
		}
		prev := w.activeDoc()
		w.active = s.index
		w.focus = focusBuffer
		return w.autosaveOnFocusChange(prev)
	}
	return nil
}

// clickTree selects and OPENS in one click.
//
// It took two before: the first click selected and only a second one on
// the same row opened, on the reasoning that browsing the tree with the
// mouse should not load every file it passed over. That reasoning was
// wrong twice. F13's spec says "open on ⏎ or click", singular; and
// browsing happens with the keyboard, where a click is never involved, so
// the case it was protecting against does not exist. In practice it
// produced exactly one confusing behaviour -- the row that happened to be
// selected already (row 0 on a fresh tree) opened on one click while every
// other row needed two.
//
// The border rows are NOT rows. inRect covers the whole card including its
// box, so a click on the top border used to map to row -1 and be silently
// swallowed -- which is what "clicking the row right at the top does not
// open it" was. A click outside the content area now leaves the selection
// alone and only moves focus, which is what clicking a pane's frame means
// everywhere else.
func (w *Workspace) clickTree(x, y int) tea.Cmd {
	w.focus = focusTree
	_ = x

	// One border row at the top, one at the bottom (box.go's own layout).
	if y <= w.treeRect.Y || y >= w.treeRect.Y+w.treeRect.H-1 {
		return nil
	}
	row := w.tree.top + (y - w.treeRect.Y - 1)
	if row < 0 || row >= len(w.tree.rows) {
		return nil
	}
	w.tree.sel = row
	w.tree.reveal = true
	return w.openSelected()
}

func inRect(r layout.Rect, x, y int) bool {
	return !r.IsZero() && x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}
