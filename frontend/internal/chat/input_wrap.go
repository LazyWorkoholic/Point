package chat

import (
	"unicode"

	"github.com/mattn/go-runewidth"
)

// This file wraps the INPUT buffer, not the transcript. wrap.go's
// wrapWords/splitWords collapse internal whitespace to a single space
// (`strings.Fields`) because that is correct for read-only prose — it is
// wrong here: an editable buffer must keep every character's exact
// position, or the cursor lands somewhere other than where it was typed.
// This is a separate, position-preserving wrap for exactly that reason.

// inputLine is one VISUAL (wrapped) line: the half-open rune range
// [Start, End) of the buffer it covers. Every rune index in the buffer
// belongs to exactly one line — the ranges partition it completely, with no
// gap except the single rune consumed by a real newline between paragraphs
// (see lineAt).
type inputLine struct {
	Start, End int
}

// wrapInputRunes wraps runes at width display cells, preserving exact
// positions. A literal '\n' (Alt+Enter) always starts a new paragraph;
// within a paragraph, whitespace and non-whitespace runs are packed
// greedily, and a single run wider than width is hard-broken cell by cell —
// the same rule wrap.go's hardBreak uses, for the same reason: an unbroken
// CJK run or a pasted URL must not push the box's border off column.
func wrapInputRunes(runes []rune, width int) []inputLine {
	if width < 1 {
		width = 1
	}
	var lines []inputLine
	paraStart := 0
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			lines = append(lines, wrapParagraph(runes, paraStart, i, width)...)
			paraStart = i + 1
		}
	}
	if len(lines) == 0 {
		lines = []inputLine{{Start: 0, End: 0}}
	}
	return lines
}

// wrapParagraph wraps runes[lo:hi] -- guaranteed to contain no newline --
// into one or more inputLines, using absolute buffer indices throughout.
//
// A whitespace run that SEPARATES two words (there is a word after it in
// this paragraph) and falls at a wrap point is excluded from both the line
// it would trail and the line it would lead -- it becomes a GAP, exactly
// like the newline between paragraphs (wrapInputRunes, above): still real
// content at its own buffer index -- undo, selection, and the eventual
// sent message all still see it, unmangled -- but not rendered by any
// line, and so not a position a caret can visit "mid-line" either. Interior
// whitespace that ISN'T at a wrap point (an ordinary separator with room
// to spare) renders exactly as typed, unaffected.
//
// Bug 14: without this, a line that happened to wrap exactly at a SINGLE
// space kept that space as the LAST rune of its own [Start,End) range --
// real content, but rendered as a blank column nobody can see is there.
// Arriving at it (End, then one Left) looked like landing on empty space
// past the visible text, needing a second Left to reach the actual last
// letter. A line that wrapped mid-word had no such rune, which is why it
// was inconsistent between lines.
//
// Bug 15: a separating run WIDER than the box itself was still hard-broken
// as if it were visible content (like an overlong word) rather than
// gapped, because it hit the tokWidth>width case before the "does it fit"
// check ever ran -- rendering an entire line of nothing but blank,
// individually-landable space columns, with leftover leading spaces
// carried onto the line after. The fix is the same rule Bug 14 already
// established, applied regardless of how wide the run is: a separating
// whitespace run becomes ONE gap in full, never partially rendered.
//
// This is the ONLY place either fix needs to live. lineAt, lineAtTrailing
// and atSoftBoundary (below) already treat ANY gap between two lines as a
// hard boundary -- they compare indices, never inspect what rune sits in
// the gap, or how wide it is -- so classifying a wrap-consumed run as a
// gap the same way a newline already is makes every movement function
// (Left, Right, Up, Down, Home, End) correct for free, without touching
// any of them.
func wrapParagraph(runes []rune, lo, hi, width int) []inputLine {
	if lo >= hi {
		return []inputLine{{Start: lo, End: hi}}
	}

	var lines []inputLine
	lineStart := lo
	used := 0
	i := lo

	// pendingSpace is a whitespace run already folded into used because
	// it fit, with nothing else added to the line since. If the line
	// flushes right after it, the space turns out to have been sitting
	// exactly at the wrap point after all, and is excluded retroactively.
	// Any non-space token clears it: once real content follows the space
	// on the SAME line, it was a genuine mid-line separator, not a
	// boundary artifact.
	pendingSpace := -1

	for i < hi {
		j := i
		space := unicode.IsSpace(runes[i])
		for j < hi && unicode.IsSpace(runes[j]) == space {
			j++
		}
		// Width summed rune by rune rather than via
		// StringWidth(string(runes[i:j])): that allocated a fresh string
		// per token, thousands of them per wrap of a pasted message.
		tokWidth := 0
		for k := i; k < j; k++ {
			tokWidth += runewidth.RuneWidth(runes[k])
		}

		switch {
		case tokWidth > width && space && j < hi:
			// A SEPARATING whitespace run (a word follows it in this
			// paragraph) that is wider than the box itself -- Bug 15.
			// Bug 14's fix already gaps a separating run that merely
			// doesn't fit in the REMAINING width of the current line (the
			// `used+tokWidth > width` case below); this is the same rule
			// extended to a run wide enough to overflow a FRESH line too.
			//
			// Without this, such a run fell into the tokWidth>width
			// hard-break case below (meant for an overlong WORD, which
			// genuinely has no wrap point to hide behind) and was hard-
			// broken cell by cell like one -- rendering an entire line of
			// nothing but blank, individually-landable space columns, with
			// more leftover leading spaces on the line after that. The fix
			// is the same one Bug 14 already established: however wide,
			// the whole run becomes ONE gap, never rendered, never a
			// position a caret lands on mid-crossing.
			//
			// Trailing whitespace with NOTHING after it in the paragraph
			// does not take this path (space && j < hi both required) --
			// that is genuine standalone content, not a wrap artifact, and
			// still hard-breaks normally below if it is too wide to fit.
			if used > 0 {
				lines = append(lines, inputLine{Start: lineStart, End: i})
			}
			lineStart = j
			used = 0
			pendingSpace = -1
			i = j
			continue

		case tokWidth > width:
			// The token alone is wider than the line. Flush whatever is
			// pending -- trimming a pending trailing space the same way
			// any other flush below does -- then hard-break the token
			// cell by cell. A SEPARATING space run this wide already left
			// via the case above; what reaches here is either an overlong
			// WORD, or trailing whitespace with nothing after it in the
			// paragraph (real, standalone content -- there is no next word
			// for it to be hiding a boundary from), and both hard-break
			// exactly the same way.
			if used > 0 {
				end := i
				if !space && pendingSpace >= 0 {
					end = pendingSpace
				}
				lines = append(lines, inputLine{Start: lineStart, End: end})
			}
			segStart, segUsed := i, 0
			for k := i; k < j; k++ {
				rw := runewidth.RuneWidth(runes[k])
				if segUsed+rw > width && segUsed > 0 {
					lines = append(lines, inputLine{Start: segStart, End: k})
					segStart, segUsed = k, 0
				}
				segUsed += rw
			}
			lineStart, used = segStart, segUsed
			pendingSpace = -1
			i = j

		case used > 0 && used+tokWidth > width:
			if space && j < hi {
				// A whitespace run that does not fit, with more content
				// still to come: it becomes the gap, never the next
				// line's leading content.
				lines = append(lines, inputLine{Start: lineStart, End: i})
				lineStart = j
				used = 0
				pendingSpace = -1
				i = j
				continue
			}
			// A word that does not fit, OR trailing whitespace with
			// nothing after it in the paragraph (real content, not a wrap
			// artifact -- there is no next word for it to be separating
			// from): flush, trimming a pending trailing space from THIS
			// line first if one is waiting.
			end := i
			if !space && pendingSpace >= 0 {
				end = pendingSpace
			}
			lines = append(lines, inputLine{Start: lineStart, End: end})
			lineStart = i
			used = tokWidth
			pendingSpace = -1
			i = j

		default:
			used += tokWidth
			if space {
				pendingSpace = i
			} else {
				pendingSpace = -1
			}
			i = j
		}
	}
	lines = append(lines, inputLine{Start: lineStart, End: hi})
	return lines
}

// wrapFrom wraps only runes[from:], which must start at a paragraph
// boundary (index 0, or just after a newline). Used by the incremental
// re-wrap in input.go: an edit can only change the wrapping of the
// paragraph it lands in and everything after it, so everything before that
// paragraph is reused verbatim.
func wrapFrom(runes []rune, from, width int) []inputLine {
	if width < 1 {
		width = 1
	}
	var lines []inputLine
	paraStart := from
	for i := from; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			lines = append(lines, wrapParagraph(runes, paraStart, i, width)...)
			paraStart = i + 1
		}
	}
	return lines
}

// paragraphStart is the index just after the newline preceding pos, i.e.
// the start of the paragraph pos falls in.
func paragraphStart(runes []rune, pos int) int {
	if pos > len(runes) {
		pos = len(runes)
	}
	for i := pos - 1; i >= 0; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineAt returns the index of the line containing rune position pos.
//
// A position sitting exactly on a line's End is ambiguous by construction —
// it could mean "the end of this line" or "the start of the next" — and the
// two cases that produce it resolve differently: a SOFT wrap boundary
// (lines[i+1].Start == l.End, nothing was consumed between them) resolves
// to the START of the next line, matching where a caret visually continues
// after wrapping. A HARD break (lines[i+1].Start == l.End+1, the skipped
// rune is the '\n' itself) resolves to the END of THIS line — the position
// is the newline character, which reads as "just before the break," the
// same place End/Home already put a caret in every mainstream editor.
func lineAt(lines []inputLine, pos int) int {
	for i, l := range lines {
		last := i == len(lines)-1
		if pos < l.End {
			return i
		}
		if pos == l.End {
			if last || lines[i+1].Start != l.End {
				return i
			}
			// Soft wrap: let the next iteration's Start==pos catch it.
		}
	}
	return len(lines) - 1
}

// lineAtTrailing is lineAt's mirror: at a SOFT wrap boundary it resolves to
// the END of the earlier line rather than the start of the next one. This
// is the cursor-affinity half of Bug 6's fix — see inputBuffer.trailing.
// Only End (and the rendering that follows it) asks for this reading.
func lineAtTrailing(lines []inputLine, pos int) int {
	for i, l := range lines {
		if pos <= l.End {
			return i
		}
	}
	return len(lines) - 1
}

// lineForCursor is the ONE lookup both cursor movement (input_wrap.go) and
// rendering (input_render.go) use, so the line End moved the cursor to is
// the same line the caret is drawn on. Passing the buffer's own affinity
// through here is what keeps those two from disagreeing.
func lineForCursor(lines []inputLine, pos int, trailing bool) int {
	if trailing {
		return lineAtTrailing(lines, pos)
	}
	return lineAt(lines, pos)
}

// colOf is pos's display-width offset from the start of line l.
func colOf(runes []rune, l inputLine, pos int) int {
	if pos < l.Start {
		pos = l.Start
	}
	if pos > l.End {
		pos = l.End
	}
	w := 0
	for i := l.Start; i < pos; i++ {
		w += runewidth.RuneWidth(runes[i])
	}
	return w
}

// posAtColumn is colOf's inverse: the rune index on line l whose display
// column is closest to (without exceeding) col, clamped to the line.
func posAtColumn(runes []rune, l inputLine, col int) int {
	used := 0
	for i := l.Start; i < l.End; i++ {
		rw := runewidth.RuneWidth(runes[i])
		if used+rw > col {
			return i
		}
		used += rw
	}
	return l.End
}

// ---------------------------------------------------------------------------
// Width-dependent cursor movement
//
// Every function here ends with exactly one call to setCursor (input.go),
// giving both the new position AND its affinity explicitly — see
// beginMove's doc for why that is the point: Home/End (Bug 6), Up/Down
// (Bug 11) and Left/Right (Bug 13) were the same omission at three
// different sites, each one a movement that computed a new cursor index
// and left affinity to whatever it happened to already be.
// ---------------------------------------------------------------------------

func (b *inputBuffer) MoveUp(extend bool, width int) {
	lines := b.lines(width)
	li := lineForCursor(lines, b.cursor, b.trailing)
	b.beginMove(extend)
	if li == 0 {
		b.setCursor(0, false)
		return
	}
	col := colOf(b.runes, lines[li], b.cursor)
	pos := posAtColumn(b.runes, lines[li-1], col)
	b.setCursor(pos, trailingFor(lines, li-1, pos))
}

func (b *inputBuffer) MoveDown(extend bool, width int) {
	lines := b.lines(width)
	li := lineForCursor(lines, b.cursor, b.trailing)
	b.beginMove(extend)
	if li == len(lines)-1 {
		b.setCursor(len(b.runes), false)
		return
	}
	col := colOf(b.runes, lines[li], b.cursor)
	pos := posAtColumn(b.runes, lines[li+1], col)
	b.setCursor(pos, trailingFor(lines, li+1, pos))
}

func (b *inputBuffer) MoveHome(extend bool, width int) {
	lines := b.lines(width)
	li := lineForCursor(lines, b.cursor, b.trailing)
	b.beginMove(extend)
	b.setCursor(lines[li].Start, false) // Home always lands leading
}

// MoveEnd lands on the end of the CURRENT visual line — the wrapped one, not
// the logical paragraph. Bug 6: the rune index it sets is also the index of
// the next line's first character when the break is a SOFT wrap, so without
// the trailing affinity the caret drew one line lower and a second End
// walked down again instead of staying put.
//
// trailing is set from atSoftBoundary, not unconditionally: on the LAST
// line of the buffer there is no next line for "trailing" to mean anything
// relative to, and a generic consistency check across every movement
// function (TestEveryMovementFromAnEndPositionStaysConsistent) is what
// caught this — trailing=true there rendered correctly regardless (nothing
// else depends on the flag once lineAt and lineAtTrailing already agree),
// but leaving it true anyway made the invariant "trailing implies a genuine
// soft boundary" false for no reason.
func (b *inputBuffer) MoveEnd(extend bool, width int) {
	lines := b.lines(width)
	li := lineForCursor(lines, b.cursor, b.trailing)
	b.beginMove(extend)
	pos := lines[li].End
	b.setCursor(pos, atSoftBoundary(lines, pos))
}

// atSoftBoundary reports whether pos is the shared index between two
// adjacent lines with nothing consumed between them -- see lineAt's own doc
// on the soft-vs-hard distinction this mirrors.
func atSoftBoundary(lines []inputLine, pos int) bool {
	li := lineAtTrailing(lines, pos)
	return li >= 0 && li+1 < len(lines) && lines[li].End == pos && lines[li+1].Start == pos
}

// lineContaining returns the index of the line whose [Start,End] range
// (End inclusive, a valid boundary caret position) actually contains pos,
// or -1 if pos falls strictly INSIDE a gap between two lines.
//
// Bug 15: a gap can now be wider than one rune (a whole consumed
// whitespace run, not just a newline or a single space), which a plain
// cursor+1/cursor-1 step can land inside of -- a real rune index, but one
// no line renders, so renderInputLines' cursorLine == i check never
// matches ANY row and the caret simply does not appear anywhere on
// screen. MoveRight/MoveLeft, below, use this to detect exactly that and
// skip the rest of the gap in one step, the same way MoveEnd already
// jumps straight to a line's own edge rather than walking there rune by
// rune.
func lineContaining(lines []inputLine, pos int) int {
	for i, l := range lines {
		if pos >= l.Start && pos <= l.End {
			return i
		}
	}
	return -1
}

// MoveRight moves one rune right, EXCEPT:
//
//   - crossing a SOFT wrap boundary from its TRAILING reading (End reached
//     via MoveEnd or a vertical move) takes only an affinity flip, not an
//     index increment (Bug 13: at a soft wrap the boundary index is BOTH
//     "one past the last rune of this line" and "the first rune of the
//     next line" -- the same index, two screen positions. Reached
//     leadingly, one more Right correctly steps onto the next line's
//     first rune, because the boundary index itself already reads as
//     sitting there; reached trailingly, a plain cursor++ skips straight
//     over that reading, landing on the SECOND rune of the next line
//     instead of the first).
//   - stepping into a GAP wider than one rune lands past it entirely, on
//     the following line's first rune, rather than somewhere inside it
//     where no line would ever render a caret (Bug 15).
//
// A HARD newline is exactly one rune wide, so it is caught by the second
// case too -- this subsumes what a dedicated newline check would do.
func (b *inputBuffer) MoveRight(extend bool, width int) {
	lines := b.lines(width)
	if b.trailing && atSoftBoundary(lines, b.cursor) {
		b.beginMove(extend)
		b.setCursor(b.cursor, false) // same index, now read leadingly
		return
	}
	b.beginMove(extend)
	next := b.cursor + 1
	// The gap-skip only applies to a position genuinely BETWEEN two real
	// lines. next > len(b.runes) means the cursor was already at the very
	// end of the buffer -- lineContaining correctly reports "not found"
	// there too, but that is buffer-end overflow, not a gap, and belongs
	// to setCursor's own clamp, not a skip forward past content that does
	// not exist.
	if next <= len(b.runes) && lineContaining(lines, next) < 0 {
		next = lines[lineAt(lines, next)].Start // skip the rest of the gap
	}
	b.setCursor(next, false)
}

// MoveLeft mirrors MoveRight's gap handling (Bug 15) but needs no affinity
// special case the way Right does: decrementing from a boundary always
// lands unambiguously on the PREVIOUS line's own content once any gap
// between them is skipped, regardless of which affinity the move started
// from -- Bug 13 confirmed this holds for a one-rune gap; the reasoning is
// unchanged for a wider one.
func (b *inputBuffer) MoveLeft(extend bool, width int) {
	lines := b.lines(width)
	b.beginMove(extend)
	prev := b.cursor - 1
	// Symmetric to MoveRight's guard: prev < 0 means the cursor was
	// already at the very start of the buffer -- lineContaining reports
	// "not found" there too (no line starts before 0), but indexing
	// lines[lineAt(lines,prev)-1] on that would be lines[-1], a crash, not
	// a gap to skip. setCursor's own clamp is what buffer-start overflow
	// needs, same as MoveRight's buffer-end case.
	if prev >= 0 && lineContaining(lines, prev) < 0 {
		// lineAt resolves a gap-interior position to the line AFTER the
		// gap (see lineContaining's own doc); the line the gap follows,
		// which is where Left should land, is simply the one before it.
		prev = lines[lineAt(lines, prev)-1].End
	}
	b.setCursor(prev, false)
}

// trailingFor is MoveUp/MoveDown's affinity choice, kept as one function so
// both directions decide it identically: true only when the vertical move
// landed EXACTLY on line li's own End at a genuine soft boundary (Bug 11) —
// column-clamping onto a shorter line, or landing mid-line, both read
// leading, same as everywhere else a plain cursor index is unambiguous.
func trailingFor(lines []inputLine, li, pos int) bool {
	return li >= 0 && li < len(lines) && pos == lines[li].End && atSoftBoundary(lines, pos)
}

// visualLineCount is what render.go's growth calculation asks for: how many
// wrapped rows the buffer currently needs at this width.
func (b *inputBuffer) visualLineCount(width int) int {
	return len(b.lines(width))
}
