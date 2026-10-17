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
		return w.wheel(m, -3)
	case tea.MouseButtonWheelDown:
		return w.wheel(m, +3)
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
func (w *Workspace) posAt(d *doc, x, y int) Pos {
	line := d.top + (y - w.textArea.Y)
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
		d.scrollBy(-1)
		y = w.textArea.Y
	case y >= w.textArea.Y+w.textArea.H:
		d.scrollBy(+1)
		y = w.textArea.Y + w.textArea.H - 1
	}
	d.cursor = d.buf.Clamp(w.posAt(d, x, y))
	d.selecting = true
	d.hist.breakGroup()
	d.desiredValid = false
}

func (w *Workspace) wheel(m tea.MouseMsg, lines int) tea.Cmd {
	if inRect(w.treeRect, m.X, m.Y) {
		w.tree.top += lines
		if w.tree.top < 0 {
			w.tree.top = 0
		}
		if max := len(w.tree.rows) - 1; w.tree.top > max && max >= 0 {
			w.tree.top = max
		}
		return nil
	}
	if len(w.docs) > 0 {
		w.docs[w.active].scrollBy(lines)
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
