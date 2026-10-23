package editor

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The row mapping, swept against a LONG generated region.
//
// F15's gate reported the caret landing below the line clicked, and could
// only see it with Ollama -- not because Ollama is special but because
// Demo's fixture is two lines long, which is too short for a row offset to
// be visible at all. So these sweep a 40-line ghost, which is what a real
// reply produces.
//
// Nothing here assumes a layout. Each test reads the line number the
// GUTTER actually printed on each row and compares the caret against that,
// so an off-by-one anywhere between layout, render and hit-test shows up as
// a mismatch on every row rather than as a theory about one of them.

// longGhost is a generated region of n lines, indented like real code.
func longGhost(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "    ghost_line_" + strconv.Itoa(i+1)
	}
	return out
}

// gutterNumber reads the line number a rendered row printed, or reports
// that the row is an injected one ("+") or blank.
func gutterNumber(row string) (n int, ghost, ok bool) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(stripANSI(row)), "│"))
	if len(fields) == 0 {
		return 0, false, false
	}
	if fields[0] == "+" {
		return 0, true, true
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false, false
	}
	return n, false, true
}

// clickSweep clicks every visible row and checks the caret against the
// gutter. Returns the number of mismatches, having reported each.
func clickSweep(t *testing.T, ws *Workspace) int {
	t.Helper()
	const firstCodeRowInBlock = 2 // the box border, then the tab strip
	block := strings.Split(ws.View(ws.lastLayout), "\n")
	d := ws.docs[ws.active]

	bad := 0
	for i := firstCodeRowInBlock; i < len(block); i++ {
		screenY := ws.textArea.Y + i - firstCodeRowInBlock
		if screenY >= ws.textArea.Y+ws.textArea.H {
			break
		}
		n, ghost, ok := gutterNumber(block[i])
		if !ok {
			continue
		}
		before := d.cursor
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
			X: ws.textArea.X + 1, Y: screenY,
		})
		switch {
		case ghost:
			if d.cursor != before {
				bad++
				t.Errorf("screen row %d is a GHOST row, yet the click moved the "+
					"caret to line %d", screenY, d.cursor.Line+1)
			}
		case d.cursor.Line != n-1:
			bad++
			t.Errorf("screen row %d draws line %d; the click landed on line %d "+
				"(off by %d)", screenY, n, d.cursor.Line+1, d.cursor.Line+1-n)
		}
	}
	return bad
}

func TestClickingAnyRowLandsOnTheLineThatRowDraws(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ghost []string
		lit   bool
		wheel int
	}{
		{name: "no ghost"},
		{name: "two-line ghost (Demo's fixture length)", ghost: longGhost(2)},
		{name: "forty-line ghost (a real reply)", ghost: longGhost(40)},
		{name: "forty-line ghost, scrolled", ghost: longGhost(40), wheel: 4},
		{name: "literal prose that wraps", lit: true,
			ghost: []string{strings.Repeat("a long refusal sentence ", 12)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, l := editorAt(t, numberedFile(60), 120, 30)
			ws.docs[0].cursor = Pos{Line: 5}
			if tc.ghost != nil {
				ws.ghost = &ghostText{anchorLine: 5, lines: tc.ghost, literal: tc.lit}
			}
			ws.View(l)
			for i := 0; i < tc.wheel; i++ {
				ws.handleMouse(tea.MouseMsg{
					Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
					X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
				})
			}
			if bad := clickSweep(t, ws); bad > 0 {
				t.Errorf("%d row(s) mismatched", bad)
			}
		})
	}
}

// Arrow up/down through a ghost: the same row-mapping logic drives the
// caret, so it gets the same sweep. Each press must move the caret by
// exactly one LINE -- the ghost rows are not lines and must not be
// stepped onto.
func TestArrowKeysStepOneLineAtATimeThroughAGhost(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}
	ws.ghost = &ghostText{anchorLine: 5, lines: longGhost(40)}
	ws.View(l)

	for want := 3; want <= 12; want++ {
		ws.Update(tea.KeyMsg{Type: tea.KeyDown})
		ws.View(l)
		if d.cursor.Line != want {
			t.Fatalf("after a down-arrow the caret is on line %d, want %d -- "+
				"the ghost rows are being stepped onto as if they were lines",
				d.cursor.Line+1, want+1)
		}
	}
	for want := 11; want >= 2; want-- {
		ws.Update(tea.KeyMsg{Type: tea.KeyUp})
		ws.View(l)
		if d.cursor.Line != want {
			t.Fatalf("after an up-arrow the caret is on line %d, want %d",
				d.cursor.Line+1, want+1)
		}
	}
}
