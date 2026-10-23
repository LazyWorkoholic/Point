package editor

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// The inline fill prompt: one line at the caret row, in the accent border.
//
//	┃ fill · after line 12 · python  │ add error handling for a missing file
//
// F15's Build: "`esc` cancels. Nothing is sent until Enter."
//
// # Why this is a row and not an overlay
//
// The whole point of the prompt is that it is AT the caret -- "a one-line
// prompt at the cursor row". A centred overlay would ask about code it is
// covering. So it is rendered the same way the ghost is: a display-only
// row injected by `renderCode` at a line number, with nothing in the
// buffer.
//
// Scrolling and the caret are unaffected. The row occupies one display row
// of the text area, paid for out of the rows the viewport had.
type fillPrompt struct {
	// runes is the instruction being typed, and col is the caret within
	// it. Runes rather than a string for the same reason `lineBuffer`
	// holds runes: a cursor column over a string of multi-byte characters
	// is a bug waiting for a non-ASCII instruction.
	runes []rune
	col   int

	// line is the 1-based caret line at the moment the prompt opened, and
	// lang the document's lexer name. Both are frozen for the label --
	// the prompt's own doc says what it says ("after line 12 · python")
	// and must not change under the user while they type.
	line int
	lang string

	// undo/redo are a real STACK, not a snapshot.
	//
	// The first version of this was ONE level, taken only on a paste or a
	// ctrl+u, on the reasoning that backspace is a one-line field's undo.
	// That was the wrong call and is corrected here: a typo noticed three
	// words later is not fixable by backspace without retyping those
	// words, which is what undo is for -- and the main buffer has had it
	// since F13a. A field an instruction is dictated into deserves the
	// same.
	//
	// Entries COALESCE a run of the same kind of edit, the way the
	// buffer's own history does (undo.go's editKind): a new entry is
	// pushed when the kind of edit changes, when the caret has moved
	// since the last one, or on a bulk change. Without that, ctrl+z in a
	// text field takes back one letter and is indistinguishable from
	// holding backspace.
	undo     []promptEdit
	redo     []promptEdit
	lastKind promptEditKind
	// lastText is the text of the previous edit and lastAt when it
	// happened -- the two inputs the buffer's own coalescing rule uses
	// that a "same kind" test alone does not have. See record.
	lastText string
	lastAt   time.Time

	// lastCol is where the caret was after the previous edit -- see
	// caretContiguous.
	lastCol int

	// left is the horizontal scroll, in runes, so a long instruction
	// keeps its caret visible. Cells would be more correct for CJK input
	// and are not worth the arithmetic for a one-line field: the label is
	// cell-truncated and the field is the remainder.
	left int
}

// promptEdit is one entry on the field's undo stack: the whole content
// before the edit, and where the caret was.
//
// Whole-content snapshots rather than deltas. The field holds one
// sentence, so a snapshot is a few dozen runes, and the arithmetic that
// makes a delta history correct -- the thing undo.go needs several hundred
// lines for -- buys nothing at this size.
type promptEdit struct {
	runes []rune
	col   int
}

// promptEditKind is what an edit was, for coalescing a run of the same
// kind into one undo step.
type promptEditKind int

const (
	promptEditNone promptEditKind = iota
	promptEditInsert
	promptEditDelete
	// promptEditBulk is a paste or a clear: never coalesced with
	// anything, including itself.
	promptEditBulk
)

// promptUndoLimit bounds the stack. Far past what a one-line field can
// usefully hold, and it stops a long dictation session growing without
// limit.
const promptUndoLimit = 64

// promptEdge is the left frame of the prompt row, in the accent role.
//
// A box-drawing vertical rather than an arrow: the row sits between two
// lines of code, so what it needs is an EDGE that reads as a frame around
// an input. One cell, because the row is already competing with the gutter
// for width.
const promptEdge = "┃"

func newFillPrompt(d *doc, _ theme.Theme) *fillPrompt {
	return &fillPrompt{line: d.cursor.Line + 1, lang: d.lang}
}

func (p *fillPrompt) text() string { return string(p.runes) }

// label is the fixed left half: the mode, the anchor, the language.
func (p *fillPrompt) label() string {
	s := "fill · after line " + strconv.Itoa(p.line)
	if p.lang != "" {
		s += " · " + p.lang
	}
	return s
}

// record pushes an undo entry for an edit of kind k, coalescing with the
// previous one where that is the right behaviour.
//
// # The rule is the BUFFER's rule, not a second one
//
// The first version coalesced any unbroken run of the same kind, which
// made the length of an undo step depend on how long the user went without
// moving the caret -- so one ctrl+z could take back a whole sentence, and
// the faster you typed the more it took. The editor's own history does not
// have that problem, and the reason is three guards this had none of
// (undo.go's canMerge):
//
//  1. a TIME gap -- a pause ends the run (undoCoalesceGap, 700ms);
//  2. contiguity -- here, that the caret has not moved since the last
//     edit, which is this field's equivalent of "the insert begins where
//     the last one ended";
//  3. the WORD rule -- a run ends when the next rune STARTS a word, which
//     is what makes one ctrl+z take back "world" rather than the sentence.
//
// `startsWord` is undo.go's own function, called here rather than
// reimplemented, so the two surfaces break a run at exactly the same
// boundary and cannot drift apart.
func (p *fillPrompt) record(k promptEditKind) { p.recordText(k, "") }

// recordText is record with the text being inserted, which the word rule
// needs. Deletions and bulk changes pass "".
func (p *fillPrompt) recordText(k promptEditKind, text string) {
	now := time.Now()
	coalesce := k != promptEditBulk && k == p.lastKind && len(p.undo) > 0 &&
		p.caretContiguous() &&
		now.Sub(p.lastAt) <= undoCoalesceGap &&
		!startsWord(p.lastText, text)

	p.lastKind = k
	p.lastText = text
	p.lastAt = now
	// Any new edit invalidates the redo branch, exactly as it does in the
	// buffer's own history.
	p.redo = nil
	if coalesce {
		return
	}
	p.undo = append(p.undo, promptEdit{
		runes: append([]rune{}, p.runes...),
		col:   p.col,
	})
	if len(p.undo) > promptUndoLimit {
		p.undo = p.undo[len(p.undo)-promptUndoLimit:]
	}
}

// caretContiguous reports whether the caret is where the previous edit
// left it.
//
// The field's equivalent of the buffer's position check: "type here, move
// there, type again" must be two steps, and comparing the caret is how
// that holds without any extra bookkeeping. breakCoalescing covers the
// explicit moves; this covers everything else, including a move made by
// an undo or a redo.
func (p *fillPrompt) caretContiguous() bool { return p.col == p.lastCol }

// breakCoalescing ends the current run, so the next edit starts a new undo
// step. Called by every caret move -- the same rule history.breakGroup
// follows in the buffer, and for the same reason: typing, moving, and
// typing again is two edits, not one.
func (p *fillPrompt) breakCoalescing() { p.lastKind = promptEditNone }

func (p *fillPrompt) undoLast() {
	if len(p.undo) == 0 {
		return
	}
	entry := p.undo[len(p.undo)-1]
	p.undo = p.undo[:len(p.undo)-1]
	p.redo = append(p.redo, promptEdit{
		runes: append([]rune{}, p.runes...),
		col:   p.col,
	})
	p.runes = append([]rune{}, entry.runes...)
	p.col = entry.col
	p.lastKind = promptEditNone
	p.clampCol()
	p.lastCol = p.col
}

func (p *fillPrompt) redoLast() {
	if len(p.redo) == 0 {
		return
	}
	entry := p.redo[len(p.redo)-1]
	p.redo = p.redo[:len(p.redo)-1]
	p.undo = append(p.undo, promptEdit{
		runes: append([]rune{}, p.runes...),
		col:   p.col,
	})
	p.runes = append([]rune{}, entry.runes...)
	p.col = entry.col
	p.lastKind = promptEditNone
	p.clampCol()
	p.lastCol = p.col
}

func (p *fillPrompt) clampCol() {
	if p.col > len(p.runes) {
		p.col = len(p.runes)
	}
	if p.col < 0 {
		p.col = 0
	}
}

// promptOutcome is what one keystroke did to the prompt.
//
// Three outcomes rather than two: submitted, cancelled, and still typing.
// Collapsing the first two into "closed" would make the caller guess which
// one happened from whether the text is empty, and an empty instruction is
// a real thing a user can submit.
type promptOutcome int

const (
	promptTyping promptOutcome = iota
	promptSubmit
	promptCancel
)

func (p *fillPrompt) key(k tea.KeyMsg) promptOutcome {
	// Where the caret ends up is remembered AFTER the keystroke, not
	// inside record: record runs BEFORE the edit moves the caret, so
	// setting it there compared the position to itself-before-the-insert
	// and no run ever coalesced -- which made every keystroke its own
	// undo step, the opposite of this round's bug and just as wrong.
	defer func() { p.lastCol = p.col }()

	switch k.String() {
	case "enter":
		return promptSubmit
	case "esc":
		// esc ONLY. ctrl+c is copy in this workspace and never reaches
		// here -- handleKey takes it first. See copy.go.
		return promptCancel

	case "backspace":
		if p.col > 0 {
			p.record(promptEditDelete)
			p.runes = append(p.runes[:p.col-1], p.runes[p.col:]...)
			p.col--
		}
	case "delete":
		if p.col < len(p.runes) {
			p.record(promptEditDelete)
			p.runes = append(p.runes[:p.col], p.runes[p.col+1:]...)
		}

	case "left":
		if p.col > 0 {
			p.col--
		}
		p.breakCoalescing()
	case "right":
		if p.col < len(p.runes) {
			p.col++
		}
		p.breakCoalescing()
	case "home", "ctrl+a":
		p.col = 0
		p.breakCoalescing()
	case "end":
		// ctrl+e never arrives -- it is chat.queue.edit's chord and
		// dispatchKey swallows it here (keys.go's header). Listed because
		// the pair reads as missing otherwise.
		p.col = len(p.runes)
		p.breakCoalescing()

	case "ctrl+u":
		p.record(promptEditBulk)
		p.runes, p.col = nil, 0

	case "ctrl+z":
		p.undoLast()
	case "ctrl+y":
		p.redoLast()

	default:
		switch k.Type {
		case tea.KeyRunes:
			p.recordText(promptEditInsert, string(k.Runes))
			p.runes = append(p.runes[:p.col:p.col],
				append(append([]rune{}, k.Runes...), p.runes[p.col:]...)...)
			p.col += len(k.Runes)
		case tea.KeySpace:
			p.recordText(promptEditInsert, " ")
			p.runes = append(p.runes[:p.col:p.col],
				append([]rune{' '}, p.runes[p.col:]...)...)
			p.col++
		}
	}
	return promptTyping
}

// paste inserts clipboard text at the caret.
//
// Reached from the workspace's app.PasteMsg case, which is ABOVE its
// tea.KeyMsg case -- so without this the paste went past the prompt and
// into the file underneath it.
//
// Newlines are flattened to spaces: this is a one-line field, and a
// multi-line instruction pasted into it would otherwise lose everything
// after the first newline or carry breaks the field cannot show. A
// multi-line fill instruction is a real want and is parked rather than
// improvised here.
func (p *fillPrompt) paste(text string) {
	text = flattenToOneLine(text)
	runes := []rune(text)
	if len(runes) == 0 {
		return
	}
	p.record(promptEditBulk)
	tail := append([]rune{}, p.runes[p.col:]...)
	p.runes = append(append(p.runes[:p.col:p.col], runes...), tail...)
	p.col += len(runes)
	p.lastCol = p.col
}

// flattenToOneLine turns any run of whitespace that would break a one-line
// field into a single space.
//
// The comparisons are numeric (0x0a and friends) rather than rune
// literals, and that is not a style choice: this file is edited by tooling
// that has twice turned an escaped newline inside a rune literal into a
// real one, breaking the build in a way that reads as nonsense. Numbers
// cannot be mangled that way.
func flattenToOneLine(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	lastSpace := false
	for _, r := range text {
		if r == 0x0d || r == 0x0a || r == 0x09 || r == 0x0b || r == 0x0c {
			if !lastSpace {
				b.WriteByte(0x20)
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = r == 0x20
	}
	return b.String()
}

// view renders the prompt into exactly `cells` display cells.
//
// It takes the Workspace rather than a theme because the caret is drawn
// with `caretStyle`, which is a method there -- reverse video over
// whatever is underneath, for the reason render.go records at length.
func (p *fillPrompt) view(w *Workspace, cells int) string {
	if cells < 1 {
		return ""
	}
	th := w.theme

	label := p.label() + "  "
	labelW := visibleWidth(label)
	// the edge and its space, the separator, and one caret cell
	fieldW := cells - labelW - len(promptEdge) - 1 - 2
	if fieldW < 4 {
		// No room for the label and a usable field. The field wins: the
		// user is typing into it, and the label repeats what the status
		// bar already says.
		label, labelW = "fill ", 5
		fieldW = cells - labelW - len(promptEdge) - 1 - 2
	}
	if fieldW < 1 {
		fieldW = 1
	}

	// Keep the caret in view, against the width the text actually gets.
	if p.col-p.left > fieldW-2 {
		p.left = p.col - fieldW + 2
	}
	if p.col < p.left {
		p.left = p.col
	}
	if p.left < 0 {
		p.left = 0
	}

	// The field's own width has to make room for the scroll markers, or
	// adding them would push the caret cell out of the row.
	inner := fieldW
	if p.left > 0 {
		inner--
	}
	if inner < 1 {
		inner = 1
	}

	field := make([]rune, 0, inner)
	for i := p.left; i < len(p.runes) && len(field) < inner; i++ {
		field = append(field, p.runes[i])
	}
	shown := string(field)
	more := p.left+len(field) < len(p.runes)

	// SCROLL MARKERS, and the reason they exist.
	//
	// F15's gate reported that pasting into this field "erases whatever
	// was already typed". It does not -- the model was intact and the
	// paste inserted at the caret, both measured -- but the caret jumped
	// to the end of a long instruction, the field scrolled to keep it
	// visible, and everything before it went off the left edge with
	// NOTHING SAYING SO. A field showing the tail of its own contents is
	// indistinguishable from a field that lost the head of them.
	//
	// Same discipline as the Task card's truncation ellipsis and the
	// explain panel's capped question: a cut that says it is a cut cannot
	// be read as data loss.
	leftMark, rightMark := "", ""
	if p.left > 0 {
		leftMark = "‹"
	}
	if more {
		rightMark = "›"
	}

	accent := th.Style(theme.AccentPrimary)
	out := accent.Render(promptEdge+" ") +
		accent.Render(truncateCells(label, labelW)) +
		th.Style(theme.BorderSubtle).Render("│ ")

	// The caret, drawn the way the buffer's is: reverse video over
	// whatever is under it, so it is visible on every palette (render.go's
	// caretStyle records why a coloured block is not).
	caretAt := p.col - p.left
	body := padCells(shown, inner)
	runes := []rune(body)
	dim := th.Style(theme.TextDim)
	out += dim.Render(leftMark)
	if caretAt >= 0 && caretAt < len(runes) {
		out += th.Style(theme.TextPrimary).Render(string(runes[:caretAt])) +
			w.caretStyle(theme.TextPrimary).Render(string(runes[caretAt])) +
			th.Style(theme.TextPrimary).Render(string(runes[caretAt+1:]))
	} else {
		out += th.Style(theme.TextPrimary).Render(body)
	}
	out += dim.Render(rightMark)
	return truncateCells(out, cells)
}
