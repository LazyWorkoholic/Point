// Package editor is the Editor workspace: F13's buffer, cursor, selection,
// undo, tab strip, file tree and syntax highlighting.
//
// Like chat, it lives outside app because its Update method has to return
// app.Workspace, which makes this package import app and forbids the
// reverse (see chat\chat.go's doc on the same forced direction).
//
// # What this session does NOT do
//
// Session 18a stops at the save path. There is no WriteProjectFile call
// anywhere in this package, no lock acquire/heartbeat/release, no autosave
// and no refusal UI; ctrl+s says so in one line rather than pretending.
// Everything B32 reports about a file's bytes -- line ending, BOM, trailing
// newline, decode replacements, writability -- is carried through this
// package unchanged and unre-derived, so 18b's save has the values it needs
// without going back to the wire for them.
package editor

import "strings"

// Pos is a position in a Buffer: a line index, and a RUNE index into that
// line.
//
// Runes, never bytes and never display cells. Byte offsets are wrong the
// moment a non-ASCII rune appears; display cells are wrong the moment a tab
// or a CJK rune does, and both are reachable in the first file anybody
// opens in this repo. The one place display cells are the right unit is
// rendering and horizontal movement, where textutil.go's displayCol
// converts on demand.
type Pos struct {
	Line int
	Col  int
}

// Before reports whether p comes strictly before q.
func (p Pos) Before(q Pos) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Col < q.Col
}

// order returns the two positions low-first.
func order(a, b Pos) (Pos, Pos) {
	if b.Before(a) {
		return b, a
	}
	return a, b
}

// Buffer is the editing surface behind one open file.
//
// It is an interface because F13 says so, and for a reason worth keeping: a
// line slice is sufficient at this scale and is the WRONG structure at a
// larger one, so the replacement -- a rope, a gap buffer, "the first
// natural C++ component in the Track-2 book" -- has to be able to arrive
// without every caller knowing which one it got.
//
// Everything that mutates goes through Splice. That is not minimalism for
// its own sake: it is what makes undo a record of one call rather than a
// separate inverse per operation, and it is why "an agent-applied diff is
// ONE undo" is structural here instead of a special case (see undo.go).
type Buffer interface {
	// Lines is the line count. Always at least 1 -- an empty file is one
	// empty line, which is the only representation in which a cursor has
	// somewhere to be.
	Lines() int

	// Runes is line i's runes. The slice is the buffer's own and must not
	// be retained or written to; it is returned rather than copied because
	// rendering and every cursor move read it per frame.
	Runes(i int) []rune

	// Line is line i as a string, never containing a newline.
	Line(i int) string

	// Text is the whole buffer, LF-joined. No BOM and no trailing newline
	// -- both are metadata this package carries separately (see doc.go)
	// and 18b's save re-applies. It allocates the whole file, so it is a
	// save-path call, never a per-frame one.
	Text() string

	// Slice is the text between two positions, LF-joined.
	Slice(from, to Pos) string

	// Splice replaces everything between from and to with repl and returns
	// the position just after the inserted text. The positions may arrive
	// in either order and out of range; both are clamped.
	Splice(from, to Pos, repl string) Pos

	// End is the last position in the buffer.
	End() Pos

	// Clamp pulls a position inside the buffer.
	Clamp(p Pos) Pos

	// Version increments on every Splice. The highlight cache keys on it;
	// nothing derives "has it changed" any other way.
	Version() uint64
}

// lineBuffer is the line-slice implementation.
//
// Lines are stored as []rune rather than string so neither rendering nor
// cursor movement has to re-decode a line per frame. The cost is one
// conversion at load and one per Line() call, both off the hot path.
type lineBuffer struct {
	lines   [][]rune
	version uint64
}

// newLineBuffer builds a buffer from LF-normalised, BOM-free content --
// exactly what B32's ReadProjectFile puts on the wire.
//
// A single trailing newline is STRIPPED, and that is a decision rather than
// a convenience: "a\nb\n" is a two-line file in every tool that counts
// lines, and leaving the split's empty third element in would make the
// gutter, the status bar's line:col and 18b's save all have to
// special-case it. The fact that the file ends with a newline is not lost
// -- it lives in doc.trailingNewline, taken from B32's own trailing_newline
// field and never re-derived from the content.
func newLineBuffer(content string) *lineBuffer {
	content = strings.TrimSuffix(content, "\n")
	parts := strings.Split(content, "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	return &lineBuffer{lines: lines}
}

func (b *lineBuffer) Lines() int      { return len(b.lines) }
func (b *lineBuffer) Version() uint64 { return b.version }

func (b *lineBuffer) Runes(i int) []rune {
	if i < 0 || i >= len(b.lines) {
		return nil
	}
	return b.lines[i]
}

func (b *lineBuffer) Line(i int) string { return string(b.Runes(i)) }

func (b *lineBuffer) End() Pos {
	last := len(b.lines) - 1
	return Pos{Line: last, Col: len(b.lines[last])}
}

func (b *lineBuffer) Clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{}
	}
	if p.Line >= len(b.lines) {
		return b.End()
	}
	if p.Col < 0 {
		p.Col = 0
	}
	if n := len(b.lines[p.Line]); p.Col > n {
		p.Col = n
	}
	return p
}

func (b *lineBuffer) Text() string {
	var sb strings.Builder
	for i, l := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(l))
	}
	return sb.String()
}

func (b *lineBuffer) Slice(from, to Pos) string {
	from, to = order(b.Clamp(from), b.Clamp(to))
	if from == to {
		return ""
	}
	if from.Line == to.Line {
		return string(b.lines[from.Line][from.Col:to.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(b.lines[from.Line][from.Col:]))
	for i := from.Line + 1; i < to.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(string(b.lines[i]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(b.lines[to.Line][:to.Col]))
	return sb.String()
}

func (b *lineBuffer) Splice(from, to Pos, repl string) Pos {
	from, to = order(b.Clamp(from), b.Clamp(to))

	// The single-line fast path: an edit inside one line that produces one
	// line touches ONLY that line.
	//
	// This is every keystroke, and the general path below is O(the whole
	// file) for it. `append(b.lines[:from.Line:from.Line], ...)` caps the
	// capacity at from.Line, which forces a fresh backing array and a copy
	// of every line header in the buffer -- 5,000 of them, about 120KB,
	// allocated and copied per character typed. Measured, that allocation
	// and the garbage it made were most of a per-keystroke frame's long
	// tail (max 11ms, with a p99 that overran app\paste.go's 6ms
	// burst-detection gate and so broke pasting).
	//
	// Correctness is unchanged: the same head/repl/tail concatenation,
	// into the same line, with the line slice itself untouched.
	if from.Line == to.Line && !strings.Contains(repl, "\n") {
		line := b.lines[from.Line]
		merged := make([]rune, 0, len(line)-(to.Col-from.Col)+len(repl))
		merged = append(merged, line[:from.Col]...)
		merged = append(merged, []rune(repl)...)
		end := Pos{Line: from.Line, Col: len(merged)}
		merged = append(merged, line[to.Col:]...)
		b.lines[from.Line] = merged
		b.version++
		return end
	}

	// head and tail are COPIED, not aliased: both are about to be appended
	// to, and appending to a sub-slice of b.lines[i] writes through into
	// that line whenever the capacity happens to allow it -- a corruption
	// that shows up only on certain line lengths, which is the worst kind
	// to find later.
	head := append([]rune(nil), b.lines[from.Line][:from.Col]...)
	tail := append([]rune(nil), b.lines[to.Line][to.Col:]...)

	parts := strings.Split(repl, "\n")
	fresh := make([][]rune, 0, len(parts))
	var end Pos

	if len(parts) == 1 {
		merged := append(head, []rune(parts[0])...)
		end = Pos{Line: from.Line, Col: len(merged)}
		fresh = append(fresh, append(merged, tail...))
	} else {
		fresh = append(fresh, append(head, []rune(parts[0])...))
		for _, mid := range parts[1 : len(parts)-1] {
			fresh = append(fresh, []rune(mid))
		}
		last := []rune(parts[len(parts)-1])
		end = Pos{Line: from.Line + len(parts) - 1, Col: len(last)}
		fresh = append(fresh, append(last, tail...))
	}

	// rest is copied for the same aliasing reason: the three-index slice
	// below caps b.lines at from.Line so the appends cannot write over
	// lines that are still needed, and rest holds what comes after the
	// replaced span while that happens.
	rest := append([][]rune(nil), b.lines[to.Line+1:]...)
	b.lines = append(append(b.lines[:from.Line:from.Line], fresh...), rest...)
	b.version++
	return end
}

// advance is where s ends if it is inserted at from. Undo uses it to
// reconstruct the span a record inserted without asking the buffer, which
// is what lets a record be replayed against a buffer that has since moved
// on and then been wound back to the same shape.
func advance(from Pos, s string) Pos {
	n := strings.Count(s, "\n")
	if n == 0 {
		return Pos{Line: from.Line, Col: from.Col + len([]rune(s))}
	}
	last := s[strings.LastIndexByte(s, '\n')+1:]
	return Pos{Line: from.Line + n, Col: len([]rune(last))}
}
