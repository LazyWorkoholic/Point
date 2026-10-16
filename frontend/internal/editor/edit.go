package editor

import "strings"

// The edit operations. Every one of them ends in spliceAs (undo.go), which
// is the single mutation path.

// insertText is ordinary typing and paste alike. kind decides whether it
// may coalesce with the keystroke before it: editInsert for a typed rune,
// editOther for a paste, which is one undo step however long it is.
func (d *doc) insertText(s string, kind editKind) {
	if d.readOnly || s == "" {
		return
	}
	from, to := d.selectionRange()
	if d.hasSelection() {
		// Replacing a selection is its own step: merging it into the
		// typing that follows would make one ctrl+z both restore the
		// selection and remove the new text, which is two thoughts.
		//
		// A PASTE over a selection keeps its paste kind, so the rest of
		// a split paste still joins it -- otherwise the first chunk
		// (which replaced the selection) and the rest are two steps
		// again, which is the exact bug editPaste exists to fix.
		if kind != editPaste {
			kind = editOther
		}
	}
	d.spliceAs(kind, from, to, s)
}

// insertRune is one typed character, with bracket auto-pairing.
//
// A C0 control rune is REFUSED, and that is defence in depth rather than
// belt-and-braces for its own sake.
//
// app.Root.handleKey already drops the bare-modifier KeyMsg that made this
// matter (a Ctrl key-down on Windows arrives as KeyRunes carrying a single
// NUL), and that filter is the right fix in the right layer. This is the
// second half of the same argument from the other end: a buffer whose
// bytes go to disk must not be able to gain an unprintable character from
// a key event at all, whatever the input layer does next. The symptoms it
// caused -- a selection replaced by an invisible character, an undo step
// spent on it, two cursor columns sharing one display cell -- were all
// downstream of this function accepting a rune no keyboard can type.
//
// Tab and Enter are unaffected: they arrive as KeyTab and KeyEnter and are
// handled before this. Paste is unaffected too -- insertText is a separate
// path, and a pasted tab is legitimate content.
func (d *doc) insertRune(r rune) {
	if d.readOnly {
		return
	}
	if r < 0x20 || r == 0x7f {
		return
	}
	if d.maybePair(r) {
		return
	}
	d.insertText(string(r), editInsert)
}

// bracketPairs is what auto-pairing closes. Quotes are included because an
// unclosed quote is the commonest way a Python or Go file stops lexing
// correctly mid-edit, which is visible immediately in the highlighting.
var bracketPairs = map[rune]rune{
	'(':  ')',
	'[':  ']',
	'{':  '}',
	'"':  '"',
	'\'': '\'',
	'`':  '`',
}

// maybePair handles the three auto-pairing cases and reports whether it
// consumed the keystroke.
//
//  1. a selection is open  -> wrap it in the pair
//  2. the closer is already the next rune and it was auto-inserted
//     -> type THROUGH it rather than doubling it
//  3. the next rune is a word rune -> do NOT pair; typing `(` before an
//     existing identifier almost never means wrapping it, and a stray `)`
//     there is worse than no help at all
//
// Each is one undo step: wrapping a selection in brackets is one thought,
// and so is typing an opener.
func (d *doc) maybePair(r rune) bool {
	closer, isOpener := bracketPairs[r]

	if isOpener && d.hasSelection() {
		from, to := d.selectionRange()
		inner := d.buf.Slice(from, to)
		d.spliceAs(editOther, from, to, string(r)+inner+string(closer))
		// The caret lands after the closer; put the selection back around
		// the wrapped text so a second wrap composes. Through
		// setCaretAfterEdit, so the undo record describes this state and
		// not the bare post-splice one.
		anchor := Pos{Line: from.Line, Col: from.Col + 1}
		cursor := Pos{Line: to.Line, Col: to.Col}
		if from.Line == to.Line {
			cursor.Col++
		}
		d.setCaretAfterEdit(cursor, anchor, true)
		return true
	}

	runes := d.buf.Runes(d.cursor.Line)
	next := rune(0)
	if d.cursor.Col < len(runes) {
		next = runes[d.cursor.Col]
	}

	// Type through a closer this editor put there.
	if d.pairedAt == d.cursor && next == r && isCloser(r) {
		d.moveRight(false)
		d.pairedAt = Pos{Line: -1}
		return true
	}

	if !isOpener {
		return false
	}
	if next != 0 && isWordRune(next) {
		return false
	}

	d.spliceAs(editOther, d.cursor, d.cursor, string(r)+string(closer))
	// Back over the closer, and remember where it is so the next keystroke
	// can type through it.
	back := Pos{Line: d.cursor.Line, Col: d.cursor.Col - 1}
	d.setCaretAfterEdit(back, back, false)
	d.pairedAt = d.cursor
	return true
}

func isCloser(r rune) bool {
	for _, c := range bracketPairs {
		if c == r {
			return true
		}
	}
	return false
}

// newline is Enter, with Tier 1 auto-indent: copy the previous line's
// indentation, and nothing else.
//
// Tier 2 -- increase after `:` or `{`, dedent on `}` -- is parked to F16 by
// F13's own Build section, and tree-sitter is post-v1. Copying the previous
// line is deliberately the whole rule: it is right in every language, needs
// no parser, and never fights the user by guessing wrong.
func (d *doc) newline() {
	if d.readOnly {
		return
	}
	lead := indentOf(d.buf.Runes(d.cursor.Line))
	// Only the indentation BEFORE the caret is copied. Pressing Enter in
	// the middle of a line's leading whitespace should not manufacture
	// more of it than the caret has passed.
	if d.cursor.Col < len([]rune(lead)) {
		lead = string([]rune(lead)[:d.cursor.Col])
	}
	from, to := d.selectionRange()
	d.spliceAs(editOther, from, to, "\n"+lead)
}

// backspace deletes the selection, or one indent level, or one rune -- in
// that order.
//
// The indent-level case is F13's, and it is bounded deliberately: it fires
// only when everything before the caret is whitespace, so backspacing
// inside code still deletes one character at a time. It steps back to the
// previous multiple of the indent width rather than removing a fixed
// number of spaces, so a line that is misaligned by one gets aligned
// rather than pushed one further out.
func (d *doc) backspace() {
	if d.readOnly {
		return
	}
	if d.hasSelection() {
		from, to := d.selectionRange()
		d.spliceAs(editOther, from, to, "")
		return
	}
	c := d.cursor
	if c.Col == 0 {
		if c.Line == 0 {
			return
		}
		prev := Pos{Line: c.Line - 1, Col: len(d.buf.Runes(c.Line - 1))}
		d.spliceAs(editDelete, prev, c, "")
		return
	}

	runes := d.buf.Runes(c.Line)
	if !d.indent.tabs && isBlank(runes[:c.Col]) {
		w := d.indent.width
		target := ((c.Col - 1) / w) * w
		if target < c.Col-1 {
			d.spliceAs(editOther, Pos{Line: c.Line, Col: target}, c, "")
			return
		}
	}

	d.spliceAs(editDelete, Pos{Line: c.Line, Col: c.Col - 1}, c, "")
}

// deleteForward is Delete.
func (d *doc) deleteForward() {
	if d.readOnly {
		return
	}
	if d.hasSelection() {
		from, to := d.selectionRange()
		d.spliceAs(editOther, from, to, "")
		return
	}
	c := d.cursor
	runes := d.buf.Runes(c.Line)
	switch {
	case c.Col < len(runes):
		d.spliceAs(editDelete, c, Pos{Line: c.Line, Col: c.Col + 1}, "")
	case c.Line < d.buf.Lines()-1:
		d.spliceAs(editDelete, c, Pos{Line: c.Line + 1, Col: 0}, "")
	}
}

// deleteWordLeft is ctrl+backspace.
func (d *doc) deleteWordLeft() {
	if d.readOnly {
		return
	}
	if d.hasSelection() {
		from, to := d.selectionRange()
		d.spliceAs(editOther, from, to, "")
		return
	}
	from := d.wordLeft(d.cursor)
	if from == d.cursor {
		return
	}
	d.spliceAs(editOther, from, d.cursor, "")
}

// indentSelection is Tab, and dedentSelection is shift+tab.
//
// With no selection Tab inserts one indent unit at the caret, which is what
// Tab means while typing. With a selection -- even a collapsed-looking one
// that spans a line boundary -- it shifts every touched line, which is what
// Tab means in a block, and it is F13's Done-when #5.
//
// The whole block is ONE splice and therefore one undo step. Doing it as N
// per-line splices would make ctrl+z walk back up a ten-line indent one
// line at a time.
func (d *doc) indentSelection() {
	if d.readOnly {
		return
	}
	if !d.hasSelection() {
		d.insertText(d.indent.unit(), editOther)
		return
	}
	d.shiftLines(+1)
}

func (d *doc) dedentSelection() {
	if d.readOnly {
		return
	}
	if !d.hasSelection() {
		// Shift+Tab with no selection still dedents the current line --
		// there is nothing else it could usefully mean, and it is how you
		// back a single line out one level.
		d.shiftCurrentLine()
		return
	}
	d.shiftLines(-1)
}

func (d *doc) shiftCurrentLine() {
	line := d.cursor.Line
	runes := d.buf.Runes(line)
	lead := []rune(indentOf(runes))
	if len(lead) == 0 {
		return
	}
	cut := d.dedentWidth(lead)
	if cut == 0 {
		return
	}
	col := d.cursor.Col - cut
	if col < 0 {
		col = 0
	}
	d.spliceAs(editOther, Pos{Line: line}, Pos{Line: line, Col: cut}, "")
	at := Pos{Line: line, Col: col}
	d.setCaretAfterEdit(at, at, false)
}

// shiftLines indents or dedents every line the selection touches, in one
// splice, and restores a selection covering the same lines afterwards.
func (d *doc) shiftLines(dir int) {
	from, to := d.selectionRange()
	first, last := from.Line, to.Line
	// A selection ending at column 0 of a line has not actually reached
	// into that line; shifting it would indent a line the user did not
	// highlight a single character of.
	if last > first && to.Col == 0 {
		last--
	}

	unit := d.indent.unit()
	out := make([]string, 0, last-first+1)
	firstDelta, lastDelta := 0, 0

	for i := first; i <= last; i++ {
		runes := d.buf.Runes(i)
		if dir > 0 {
			// Blank lines are left alone: trailing whitespace on an empty
			// line is the one thing every linter in this repo complains
			// about, and a block indent should not manufacture it.
			if isBlank(runes) {
				out = append(out, string(runes))
				continue
			}
			out = append(out, unit+string(runes))
			if i == first {
				firstDelta = len([]rune(unit))
			}
			if i == last {
				lastDelta = len([]rune(unit))
			}
			continue
		}
		lead := []rune(indentOf(runes))
		cut := d.dedentWidth(lead)
		out = append(out, string(runes[cut:]))
		if i == first {
			firstDelta = -cut
		}
		if i == last {
			lastDelta = -cut
		}
	}

	start := Pos{Line: first}
	end := Pos{Line: last, Col: len(d.buf.Runes(last))}
	d.spliceAs(editOther, start, end, strings.Join(out, "\n"))

	// Put the selection back over the same lines, with the two edge
	// columns moved by what their own line actually changed by -- not by
	// the unit width, which is wrong for a dedent that found less
	// whitespace than a full level.
	anchor := Pos{Line: first, Col: maxInt(0, from.Col+firstDelta)}
	cursor := Pos{Line: last, Col: maxInt(0, to.Col+lastDelta)}
	if to.Line > last {
		cursor = Pos{Line: to.Line, Col: to.Col}
	}
	d.setCaretAfterEdit(cursor, anchor, true)
}

// dedentWidth is how many leading runes one dedent removes.
//
// For tabs it is one tab. For spaces it is back to the previous multiple of
// the indent width, which is what makes a block that is indented by 6 with
// a width of 4 land on 4 rather than on 2.
func (d *doc) dedentWidth(lead []rune) int {
	if len(lead) == 0 {
		return 0
	}
	if lead[0] == '\t' {
		return 1
	}
	w := d.indent.width
	n := 0
	for n < len(lead) && lead[n] == ' ' {
		n++
	}
	if n == 0 {
		return 0
	}
	rem := n % w
	if rem == 0 {
		rem = w
	}
	if rem > n {
		rem = n
	}
	return rem
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
