package editor

import "unicode"

// Cursor and selection movement.
//
// Every move goes through moveTo, which is the one place that decides what
// happens to the selection and to the undo group. That matters more than it
// looks: "shift+arrow extends, arrow collapses" and "moving the caret ends
// the coalescing run" are two rules that have to hold for every one of the
// dozen keys below, and a per-key implementation of either is a per-key
// opportunity to get it wrong.

// beginMove sets the selection up for a move. extend true starts or
// continues a selection from wherever the anchor already is; extend false
// drops it.
//
// It also cancels any pending auto-paired closer: typing `(`, moving away
// and coming back must not let the NEXT `)` type through a closer the
// caret is no longer sitting in front of.
func (d *doc) beginMove(extend bool) {
	d.pairedAt = Pos{Line: -1}
	d.reveal = true
	if extend {
		if !d.selecting {
			d.anchor = d.cursor
			d.selecting = true
		}
		return
	}
	d.selecting = false
	d.anchor = d.cursor
}

// moveTo is the single cursor setter.
func (d *doc) moveTo(p Pos, extend bool) {
	d.beginMove(extend)
	d.cursor = d.buf.Clamp(p)
	if !extend {
		d.anchor = d.cursor
	}
	d.hist.breakGroup()
	d.desiredValid = false
}

// moveVertical is the one mover that keeps desiredCell rather than
// recomputing it, so walking down past a short line and back up returns to
// the column you started in.
func (d *doc) moveVertical(delta int, extend bool) {
	d.beginMove(extend)
	if !d.desiredValid {
		d.desiredCell = displayCol(d.buf.Runes(d.cursor.Line), d.cursor.Col)
		d.desiredValid = true
	}
	line := d.cursor.Line + delta
	if line < 0 {
		line = 0
	}
	if line >= d.buf.Lines() {
		line = d.buf.Lines() - 1
	}
	d.cursor = Pos{Line: line, Col: colAtDisplay(d.buf.Runes(line), d.desiredCell)}
	if !extend {
		d.anchor = d.cursor
	}
	d.hist.breakGroup()
}

// moveLeft and moveRight COLLAPSE an open selection to its own edge
// rather than stepping off the caret.
//
// Left with "hello" selected goes to column 0, not to column 4. That is
// what every editor does and what the user means -- the selection's edges
// are the two positions they were just looking at, and stepping one rune
// back from the caret lands somewhere neither of them is. Found by
// TestSelectAllAndShiftArrowThroughTheKeyPath, which selected everything,
// pressed Left, and got a caret in the middle of the word.
func (d *doc) moveLeft(extend bool) {
	if !extend && d.hasSelection() {
		from, _ := d.selectionRange()
		d.moveTo(from, false)
		return
	}
	c := d.cursor
	switch {
	case c.Col > 0:
		c.Col--
	case c.Line > 0:
		c.Line--
		c.Col = len(d.buf.Runes(c.Line))
	}
	d.moveTo(c, extend)
}

func (d *doc) moveRight(extend bool) {
	if !extend && d.hasSelection() {
		_, to := d.selectionRange()
		d.moveTo(to, false)
		return
	}
	c := d.cursor
	switch {
	case c.Col < len(d.buf.Runes(c.Line)):
		c.Col++
	case c.Line < d.buf.Lines()-1:
		c.Line++
		c.Col = 0
	}
	d.moveTo(c, extend)
}

// moveHome lands on the first non-blank rune first, and only on column 0 on
// a second press. In indented code that is where you actually want to go,
// and the plain-column-0 behaviour is still one keypress away.
func (d *doc) moveHome(extend bool) {
	runes := d.buf.Runes(d.cursor.Line)
	first := len([]rune(indentOf(runes)))
	target := first
	if d.cursor.Col == first || first == len(runes) {
		target = 0
	}
	d.moveTo(Pos{Line: d.cursor.Line, Col: target}, extend)
}

func (d *doc) moveEnd(extend bool) {
	d.moveTo(Pos{Line: d.cursor.Line, Col: len(d.buf.Runes(d.cursor.Line))}, extend)
}

func (d *doc) moveDocStart(extend bool) { d.moveTo(Pos{}, extend) }
func (d *doc) moveDocEnd(extend bool)   { d.moveTo(d.buf.End(), extend) }

// moveWord walks by word class, crossing line boundaries.
//
// Three classes, not two: word runes, whitespace, and everything else
// (punctuation). Collapsing punctuation into "not a word" makes ctrl+right
// jump the whole of `foo.bar(baz)` in one go, which is not what word-wise
// movement means in any editor.
func (d *doc) moveWord(dir int, extend bool) {
	p := d.cursor
	if dir < 0 {
		p = d.wordLeft(p)
	} else {
		p = d.wordRight(p)
	}
	d.moveTo(p, extend)
}

func (d *doc) wordLeft(p Pos) Pos {
	runes := d.buf.Runes(p.Line)
	if p.Col == 0 {
		if p.Line == 0 {
			return p
		}
		return Pos{Line: p.Line - 1, Col: len(d.buf.Runes(p.Line - 1))}
	}
	i := p.Col
	for i > 0 && runeClass(runes[i-1]) == classSpace {
		i--
	}
	if i > 0 {
		cls := runeClass(runes[i-1])
		for i > 0 && runeClass(runes[i-1]) == cls {
			i--
		}
	}
	return Pos{Line: p.Line, Col: i}
}

func (d *doc) wordRight(p Pos) Pos {
	runes := d.buf.Runes(p.Line)
	if p.Col >= len(runes) {
		if p.Line >= d.buf.Lines()-1 {
			return p
		}
		return Pos{Line: p.Line + 1, Col: 0}
	}
	i := p.Col
	cls := runeClass(runes[i])
	if cls != classSpace {
		for i < len(runes) && runeClass(runes[i]) == cls {
			i++
		}
	}
	for i < len(runes) && runeClass(runes[i]) == classSpace {
		i++
	}
	return Pos{Line: p.Line, Col: i}
}

type class int

const (
	classWord class = iota
	classSpace
	classPunct
)

func runeClass(r rune) class {
	switch {
	case isWordRune(r):
		return classWord
	case r == ' ' || r == '\t' || unicode.IsSpace(r):
		return classSpace
	default:
		return classPunct
	}
}

// selectAll is ctrl+a. The anchor goes to the start and the caret to the
// end, rather than the other way round, so a following shift+arrow shrinks
// the selection from the end -- which is where the user was looking.
func (d *doc) selectAll() {
	d.pairedAt = Pos{Line: -1}
	d.reveal = true
	d.anchor = Pos{}
	d.cursor = d.buf.End()
	d.selecting = true
	d.hist.breakGroup()
	d.desiredValid = false
}

// ensureVisible scrolls the viewport so the caret is inside it, but ONLY
// when something asked for it -- see doc.reveal.
//
// rows and cells are the Code File box's CONTENT area -- the gutter and the
// tab strip are already subtracted by the caller, because the viewport this
// scrolls is the text area and not the box.
//
// The clamp of `top` against the line count is outside the reveal gate: a
// buffer that shrank under a scrolled-away viewport has to be pulled back
// into range whether or not the caret moved, or the window renders blank.
func (d *doc) ensureVisible(rows, cells int) {
	if rows <= 0 || cells <= 0 {
		return
	}
	if d.top > d.buf.Lines()-1 {
		d.top = d.buf.Lines() - 1
	}
	if d.top < 0 {
		d.top = 0
	}
	if !d.reveal {
		return
	}
	d.reveal = false
	if d.cursor.Line < d.top {
		d.top = d.cursor.Line
	}
	if d.cursor.Line >= d.top+rows {
		d.top = d.cursor.Line - rows + 1
	}
	if d.top < 0 {
		d.top = 0
	}

	cell := displayCol(d.buf.Runes(d.cursor.Line), d.cursor.Col)
	if cell < d.left {
		d.left = cell
	}
	// One cell of headroom on the right: a caret drawn exactly on the last
	// column of the text area sits on top of the border, and a wide rune
	// there would straddle it.
	if cell > d.left+cells-1 {
		d.left = cell - cells + 1
	}
	if d.left < 0 {
		d.left = 0
	}
}

// scrollBy moves the viewport without moving the caret -- the wheel, and
// nothing else. The caret is deliberately left where it is: a wheel scroll
// that dragged the caret along would lose the user's place the moment they
// looked somewhere else in the file.
func (d *doc) scrollBy(lines int) {
	d.top += lines
	if max := d.buf.Lines() - 1; d.top > max {
		d.top = max
	}
	if d.top < 0 {
		d.top = 0
	}
}
