package editor

import "sort"

// The unified row index: ONE scroll position for the file and everything
// drawn inside it.
//
// # What this replaces, and why the first design was wrong
//
// The ghost used to carry its own scroll offset (`ghostText.top`), nested
// at the insertion line. Two coordinates: the FILE scrolled with `doc.top`,
// and the ghost's rows scrolled within the one line they hung off. It was
// added to fix a real bug -- the renderer only emits ghost rows when it
// draws the anchor line, so without a second coordinate the tail of a
// region taller than the pane was unreachable at every file position -- and
// it fixed that bug by making the ghost a sub-viewport, which is a
// different shape of wrong:
//
//   - The insertion line was pinned on screen and the ghost scrolled
//     INSIDE it, so a proposed insertion read as a little scrolling box
//     anchored mid-file rather than as text that had been inserted.
//   - Real lines AFTER the insertion point became unreachable while the
//     ghost showed. The file could not scroll past the anchor without the
//     renderer dropping the ghost entirely, and a scroll down that reached
//     the ghost's own end re-armed the follow, which yanked the viewport
//     back to the anchor on the next frame. The rest of the file was behind
//     a wall.
//   - `ensureVisible` was handed `textRows - injectionsAt(caret)`, which
//     for a ghost taller than the pane clamps to ONE row, so every arrow
//     key snapped the viewport to the caret. That was the arrow-key
//     "glitching" once a region got long.
//
// All three are the same defect: the view had two scroll states and two row
// coordinates, and every operation had to decide which one it meant.
//
// # The model
//
// There is one sequence of rows and one position in it:
//
//	line 0 … line anchor │ ghost row 0 … ghost row n-1 │ line anchor+1 …
//
// `doc.top` indexes THAT sequence. Injected rows are entries in it, exactly
// as if they were lines of the file -- which is how the pane already
// behaves the moment the edit is applied and they become real lines. So
// scrolling down through a long region keeps going into the code below it,
// arrowing moves one entry at a time, a click maps one screen row to one
// entry, and none of it needs to know whether a given row came from the
// buffer or from a stream.
//
// With nothing injected a view row IS a buffer line, which is why every
// caller that predates the ghost reads unchanged.
//
// # Why blocks rather than a materialised slice
//
// At most two blocks exist at once (the inline fill prompt at the caret
// line, the ghost at its anchor), so both conversions are a loop over two
// entries rather than an allocation per frame. The important property is
// not the cost -- it is that `rowOf` and `at` are exact inverses over the
// same data, so the renderer, the hit-test and the viewport arithmetic
// cannot disagree about where a row is. Three derivations of that question
// is what this package has already paid for twice (render/mouse offset,
// then the ghost's own window).

// injBlock is a run of injected rows drawn AFTER a buffer line.
type injBlock struct {
	line int // 0-based buffer line the rows follow
	rows int // how many rows; always > 0
}

// viewIndex is the row sequence for one document in one frame.
//
// A VALUE, built per frame from the width actually being drawn into and
// then passed down, rather than a method everything calls: the ghost's row
// count depends on the content width, and a frame that built the index at
// one width and drew at another would reintroduce exactly the disagreement
// this type exists to prevent.
type viewIndex struct {
	lines  int        // the buffer's line count
	blocks []injBlock // ascending by line, no duplicates
}

// plainIndex is the index of a document with nothing injected -- every
// view row is its own buffer line.
func plainIndex(d *doc) viewIndex {
	return viewIndex{lines: d.buf.Lines()}
}

// total is how many rows the sequence has.
func (v viewIndex) total() int {
	n := v.lines
	for _, b := range v.blocks {
		n += b.rows
	}
	return n
}

// injectedAfter is how many rows are injected after a buffer line.
func (v viewIndex) injectedAfter(line int) int {
	for _, b := range v.blocks {
		if b.line == line {
			return b.rows
		}
	}
	return 0
}

// rowOf is the view row a buffer line is drawn on.
func (v viewIndex) rowOf(line int) int {
	row := line
	for _, b := range v.blocks {
		if b.line < line {
			row += b.rows
		}
	}
	return row
}

// at resolves a view row: which buffer line it is, or which row of which
// line's injected block.
//
// For an injected row, `line` is the line the block hangs off -- the real
// line those rows belong to. Callers that must not treat it as a position
// in the file check `injected` (mouse.go's posAt is the one that matters).
//
// Past the end of the sequence it keeps counting, so a pane taller than the
// file resolves its trailing rows to lines beyond the last one and the
// renderer draws them blank. Clamping here instead would make every
// trailing row resolve to the final line, and a click on empty space below
// a short file would move the caret.
func (v viewIndex) at(row int) (line int, injected bool, blockRow int) {
	if row < 0 {
		return 0, false, 0
	}
	above := 0 // injected rows entirely above this one
	for _, b := range v.blocks {
		start := b.line + above + 1 // the block's first view row
		if row < start {
			return row - above, false, 0
		}
		if row < start+b.rows {
			return b.line, true, row - start
		}
		above += b.rows
	}
	return row - above, false, 0
}

// clampTop pulls a scroll position back into range.
//
// The last row is allowed to be the top, matching what doc.scrollBy has
// always allowed for lines (a file can be scrolled until one line is left
// on screen). The alternative -- never scrolling past total-rows -- is a
// different product decision and not one this change is making.
func (v viewIndex) clampTop(top int) int {
	if max := v.total() - 1; top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	return top
}

// viewIndexAt builds the index for a document at a given content width.
//
// Width matters because the ghost's row count does: a proposed region is
// truncated (one row per line) while literal prose is WRAPPED, so the same
// reply is a different number of rows in a narrow pane.
func (w *Workspace) viewIndexAt(d *doc, cells int) viewIndex {
	v := viewIndex{lines: d.buf.Lines()}
	add := func(line, rows int) {
		// A block whose line no longer exists is DROPPED, not clamped: a
		// buffer that shrank under a live ghost should stop drawing it
		// where it no longer belongs, rather than re-anchoring it to
		// whatever the last line now is.
		if rows <= 0 || line < 0 || line >= v.lines {
			return
		}
		for i := range v.blocks {
			if v.blocks[i].line == line {
				v.blocks[i].rows += rows
				return
			}
		}
		v.blocks = append(v.blocks, injBlock{line: line, rows: rows})
	}

	// The prompt is added FIRST so that on the one line where both can
	// live it occupies the block's first row -- the same order
	// injectedRowView draws them in.
	if w.prompt != nil {
		add(d.cursor.Line, 1)
	}
	if g := w.ghost; g != nil {
		add(g.anchorLine, len(w.ghostRows(cells)))
	}
	sort.Slice(v.blocks, func(i, j int) bool { return v.blocks[i].line < v.blocks[j].line })
	return v
}

// viewIndex is viewIndexAt at the width the last frame drew into -- for
// everything outside the render pass (the wheel, the hit-test), where the
// question is about the frame the user is looking at.
func (w *Workspace) viewIndex(d *doc) viewIndex {
	return w.viewIndexAt(d, w.textArea.W)
}

// activeIndex is viewIndex for the active document, or a zero index when
// nothing is open.
func (w *Workspace) activeIndex() (*doc, viewIndex) {
	d := w.activeDoc()
	if d == nil {
		return nil, viewIndex{}
	}
	return d, w.viewIndex(d)
}

// followTarget is the scroll position that puts a streaming ghost's LAST
// row on the pane's last row, and whether there is a live edge at all.
//
// Unlike the position it replaces, this is an ordinary row in the one
// sequence -- it does not pin the file to the anchor and it does not stop
// the user scrolling past it. `ok` is false when the whole region already
// fits above the pane's bottom, because then there is no edge to chase and
// the viewport belongs to the user; returning 0 instead would snap the pane
// to the top of the file on every frame.
//
// The ghostFollow flag is NOT consulted here. The wheel has to be able to
// ask where the edge WOULD be while the follow is off, in order to notice
// the user scrolling back onto it.
func (w *Workspace) followTarget(d *doc, v viewIndex, rows, cells int) (int, bool) {
	g := w.ghost
	if g == nil || rows <= 0 {
		return 0, false
	}
	n := len(w.ghostRows(cells))
	if n == 0 || g.anchorLine < 0 || g.anchorLine >= d.buf.Lines() {
		return 0, false
	}
	end := v.rowOf(g.anchorLine) + 1 + n // one past the ghost's last row
	if end <= rows {
		return 0, false
	}
	return end - rows, true
}
