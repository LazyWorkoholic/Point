package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// testWorkspace is a workspace with one document open and no client, laid
// out at w×h. Enough to render and to hit-test; nothing reaches the wire.
// renderRows is renderCode with the frame's row index built for it -- the
// one thing every caller would otherwise repeat. The index is derived from
// the SAME width being drawn into, which is the invariant viewrows.go
// depends on.
func renderRows(ws *Workspace, d *doc, gutter, cells, rows int) []string {
	return ws.renderCode(d, ws.viewIndexAt(d, cells), gutter, cells, rows)
}

func testWorkspace(t *testing.T, content string, w, h int) *Workspace {
	t.Helper()
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, testDoc(t, content))
	ws.tree.build([]string{"a.py", "pkg/b.py", "pkg/c.py"})

	l := layout.Compute(w, h, layout.State{
		Active:        layout.WorkspaceEditor,
		RightPaneOpen: true,
		HeaderAllowed: true,
		TerminalMode:  layout.TermSmall,
		RightCards:    ws.Cards(),
	})
	ws.View(l)
	return ws
}

// ---------------------------------------------------------------------------
// Done-when #4 — selection on a line containing CJK and an emoji
// ---------------------------------------------------------------------------

// "The selection highlight covers exactly the right cells."
//
// Measured in CELLS, not runes: the whole failure this guards against is a
// highlight that counts 日本語 as three cells when it draws as six. The
// styled runes are checked rather than the escape soup, because that is the
// grain the renderer actually works at and it is what makes the assertion
// exact.
func TestSelectionCoversExactlyTheRightCells(t *testing.T) {
	const line = "x 日本語 🎉 y"
	ws := testWorkspace(t, line, 120, 30)
	d := ws.docs[0]
	runes := []rune(line)

	// Select 日本語 — runes 2..5.
	d.anchor, d.cursor, d.selecting = Pos{0, 2}, Pos{0, 5}, true
	from, to := d.selectionRange()

	paint := ws.paintRow(len(runes), 0, d, true, from, to, false)

	cells, selectedRunes := 0, 0
	col := 0
	for i, r := range runes {
		cw := runeCells(r, col)
		if paint[i] == paintSelected {
			selectedRunes++
			cells += cw
			if i < 2 || i >= 5 {
				t.Errorf("rune %d (%q) is selected but is outside 2..5", i, r)
			}
		}
		col += cw
	}

	if selectedRunes != 3 {
		t.Fatalf("%d runes selected, want 3", selectedRunes)
	}
	want := runewidth.StringWidth("日本語")
	if want != 6 {
		t.Fatalf("precondition: 日本語 measures %d cells, expected 6", want)
	}
	if cells != want {
		t.Errorf("selection covers %d cells, want %d", cells, want)
	}

	// And the rendered row still measures the full line: a selection
	// never changes how many cells its characters occupy.
	row := renderRows(ws, d, gutterWidth(d.buf.Lines()), ws.textArea.W, 1)[0]
	if got, want := visibleWidth(row), gutterWidth(d.buf.Lines())+ws.textArea.W; got > want {
		t.Errorf("selected row is %d cells, max %d", got, want)
	}
}

func TestSelectionCoversAnEmojiWholly(t *testing.T) {
	const line = "a 🎉 b"
	ws := testWorkspace(t, line, 120, 30)
	d := ws.docs[0]
	runes := []rune(line)

	paint := ws.paintRow(len(runes), 0, d, true, Pos{0, 2}, Pos{0, 3}, false)

	cells := 0
	col := 0
	for i, r := range runes {
		cw := runeCells(r, col)
		if paint[i] == paintSelected {
			cells += cw
		}
		col += cw
	}
	if want := runewidth.RuneWidth('🎉'); cells != want {
		t.Errorf("emoji selection = %d cells, want %d", cells, want)
	}
}

// A multi-line selection covers to the end of the first line, all of the
// middle ones, and up to the caret on the last.
func TestMultiLineSelectionBoundaries(t *testing.T) {
	ws := testWorkspace(t, "one\ntwo\nthree", 120, 30)
	d := ws.docs[0]
	from, to := Pos{0, 1}, Pos{2, 2}

	count := func(line int, text string) int {
		paint := ws.paintRow(len([]rune(text)), line, d, true, from, to, false)
		n := 0
		for _, p := range paint {
			if p == paintSelected {
				n++
			}
		}
		return n
	}
	if got := count(0, "one"); got != 2 {
		t.Errorf("first line: %d selected, want 2", got)
	}
	if got := count(1, "two"); got != 3 {
		t.Errorf("middle line: %d selected, want 3", got)
	}
	if got := count(2, "three"); got != 2 {
		t.Errorf("last line: %d selected, want 2", got)
	}
}

func TestSelectionDoesNotPaintLinesOutsideIt(t *testing.T) {
	ws := testWorkspace(t, "a\nb\nc", 120, 30)
	d := ws.docs[0]
	for _, line := range []int{0, 2} {
		paint := ws.paintRow(1, line, d, true, Pos{1, 0}, Pos{1, 1}, false)
		for _, p := range paint {
			if p == paintSelected {
				t.Errorf("line %d was painted", line)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Horizontal slicing: tabs, wide runes, and the box's right wall
// ---------------------------------------------------------------------------

// Every rendered row is exactly as wide as the text area, whatever is on
// it. A row one cell too wide pushes the box's right border off, which is
// LAYOUT.md's named most-common TUI failure.
func TestRenderedRowsNeverExceedTheTextArea(t *testing.T) {
	bodies := []string{
		"\tif x:\n\t\treturn 日本語 🎉\n" + strings.Repeat("y", 400),
		"短\n" + strings.Repeat("日", 200),
		"",
	}
	for _, body := range bodies {
		for _, width := range []int{70, 90, 120, 200} {
			ws := testWorkspace(t, body, width, 30)
			d := ws.docs[0]
			gutter := gutterWidth(d.buf.Lines())
			cells := ws.textArea.W
			for _, left := range []int{0, 1, 3, 50} {
				d.left = left
				for _, row := range renderRows(ws, d, gutter, cells, 5) {
					if got := visibleWidth(row); got > gutter+cells {
						t.Fatalf("width %d, left %d: row is %d cells, max %d\n%q",
							width, left, got, gutter+cells, stripANSI(row))
					}
				}
			}
		}
	}
}

// A tab draws as spaces and is STORED as one rune. That is the whole
// mechanism by which a tab-indented file survives being opened.
func TestTabsDrawWideAndStoreNarrow(t *testing.T) {
	ws := testWorkspace(t, "\tx", 120, 30)
	d := ws.docs[0]
	if d.buf.Line(0) != "\tx" {
		t.Fatalf("buffer holds %q", d.buf.Line(0))
	}
	row := renderRows(ws, d, gutterWidth(1), 40, 1)[0]
	plain := stripANSI(row)
	if strings.ContainsRune(plain, '\t') {
		t.Error("a literal tab reached the rendered row")
	}
	// The gutter, then tabStop spaces, then the x.
	if i := strings.IndexRune(plain, 'x'); i != gutterWidth(1)+tabStop {
		t.Errorf("x at cell %d, want %d", i, gutterWidth(1)+tabStop)
	}
}

// A double-width rune straddling the left edge after a horizontal scroll is
// dropped and padded rather than half-drawn: there is no way to render the
// right half of a glyph, and a half-drawn one shifts every later column.
func TestWideRuneStraddlingTheLeftEdgeIsPadded(t *testing.T) {
	ws := testWorkspace(t, "日本語abc", 120, 30)
	d := ws.docs[0]
	d.left = 1 // one cell into 日
	row := renderRows(ws, d, gutterWidth(1), 20, 1)[0]
	plain := stripANSI(row)[gutterWidth(1):]
	if !strings.HasPrefix(plain, " 本") {
		t.Errorf("row starts %q, want a pad then 本", plain)
	}
}

// ---------------------------------------------------------------------------
// The gutter and the frame
// ---------------------------------------------------------------------------

func TestGutterWidthFollowsTheLineCount(t *testing.T) {
	cases := map[int]int{1: 4, 9: 4, 10: 4, 99: 4, 100: 5, 5000: 6}
	for lines, want := range cases {
		if got := gutterWidth(lines); got != want {
			t.Errorf("gutterWidth(%d) = %d, want %d", lines, got, want)
		}
	}
}

func TestCodeFileBoxFillsItsRectExactly(t *testing.T) {
	for _, dim := range [][2]int{{70, 16}, {90, 24}, {120, 30}, {200, 50}} {
		ws := testWorkspace(t, "def f():\n    return 1\n", dim[0], dim[1])
		l := ws.lastLayout
		out := ws.codeFileBox(l.Main)
		rows := strings.Split(out, "\n")
		if len(rows) != l.Main.H {
			t.Errorf("%dx%d: %d rows, want %d", dim[0], dim[1], len(rows), l.Main.H)
		}
		for i, r := range rows {
			if got := visibleWidth(r); got != l.Main.W {
				t.Errorf("%dx%d row %d: %d cells, want %d", dim[0], dim[1], i, got, l.Main.W)
			}
		}
	}
}

// B8: the Terminal rect Compute always reserves for the Editor is drawn as
// a titled box naming F14, not left blank -- an empty bordered box with no
// explanation reads as a bug.
func TestTerminalRectIsDrawnAndNamesF14(t *testing.T) {
	ws := testWorkspace(t, "x", 120, 30)
	l := ws.lastLayout
	if l.Secondary.IsZero() {
		t.Fatal("Compute gave the Editor no Secondary rect")
	}
	out := ws.terminalBox(l.Secondary)
	if !strings.Contains(stripANSI(out), "Terminal") {
		t.Error("the box has no title")
	}
	if !strings.Contains(stripANSI(out), "F14") {
		t.Error("the box does not say which episode fills it")
	}
}

func TestViewFillsEveryRectAtEveryBreakpoint(t *testing.T) {
	for _, dim := range [][2]int{{70, 16}, {89, 20}, {90, 24}, {119, 29}, {120, 30}, {200, 60}} {
		ws := testWorkspace(t, "a\nb\nc\n", dim[0], dim[1])
		l := ws.lastLayout
		frame := ws.View(l)
		want := l.Main.W
		if l.ShowRightPane {
			want += layout.PaneGapX + l.RightPaneWidth
		}
		for i, row := range strings.Split(frame, "\n") {
			if got := visibleWidth(row); got != want {
				t.Errorf("%dx%d row %d: %d cells, want %d", dim[0], dim[1], i, got, want)
			}
		}
	}
}

// With nothing open the box still fills its rect and still says how to get
// a file into it.
func TestEmptyStateIsRenderedAndActionable(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	l := layout.Compute(120, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: true,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: ws.Cards(),
	})
	out := stripANSI(ws.View(l))
	if !strings.Contains(out, "No file open") {
		t.Error("the empty state does not say so")
	}
	if !strings.Contains(out, "ctrl+o") {
		t.Error("the empty state does not name the key that opens one")
	}
}

// No project means the empty state says so, and says what the consequence
// is -- read-only -- rather than leaving the user to discover it on open.
func TestEmptyStateExplainsAMissingProject(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	l := layout.Compute(120, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: true,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: ws.Cards(),
	})
	out := stripANSI(ws.View(l))
	if !strings.Contains(out, "No project here") {
		t.Error("a missing project is not reported")
	}
	// The consequence has to be stated, and stated accurately: with no
	// project resolved the path guard refuses every read, so nothing can
	// be opened at all -- not "opened read-only", which is what this
	// screen said until pathguard.py was read properly.
	if !strings.Contains(out, "No files can be opened") {
		t.Error("the consequence of a missing project is not stated")
	}
	if !strings.Contains(out, "project init") {
		t.Error("the way out is not named")
	}
}

// View must not panic at any size, including sizes Root would refuse to
// render at (below the Editor's own MinSize) and sizes Compute reports as
// TooSmall. A component never decides its own visibility -- it renders into
// the rect it is handed -- so a zero or near-zero rect has to be survivable
// rather than prevented upstream.
func TestViewSurvivesEverySize(t *testing.T) {
	for w := 1; w <= 130; w += 7 {
		for h := 1; h <= 40; h += 3 {
			ws := New(Options{Theme: testTheme()})
			ws.sendFn = func(string, queryKind, map[string]any) {}
			ws.projectID, ws.projectRoot = "p1", "C:/p"
			ws.docs = append(ws.docs, testDoc(t, "def f():\n    return 1\n"))
			ws.tree.build([]string{"a.py", "pkg/b.py"})

			for _, pane := range []bool{true, false} {
				for _, focus := range []focusTarget{focusBuffer, focusTree} {
					ws.focus = focus
					l := layout.Compute(w, h, layout.State{
						Active: layout.WorkspaceEditor, RightPaneOpen: pane,
						HeaderAllowed: true, TerminalMode: layout.TermSmall,
						RightCards: ws.Cards(),
					})
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("%dx%d pane=%v focus=%v panicked: %v",
									w, h, pane, focus, r)
							}
						}()
						ws.View(l)
					}()
				}
			}
		}
	}
}

// And the same for the key and mouse paths at a degenerate size, where
// textArea is zero and every derived row count is zero or negative.
func TestKeysAndMouseSurviveAZeroTextArea(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	ws.docs = append(ws.docs, testDoc(t, "x"))
	ws.tree.build([]string{"a.py"})
	ws.View(layout.Compute(4, 3, layout.State{Active: layout.WorkspaceEditor}))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked at a degenerate size: %v", r)
		}
	}()
	for _, k := range []string{
		"up", "down", "left", "right", "pgup", "pgdown", "home", "end",
		"tab", "shift+tab", "enter", "backspace", "delete", "ctrl+a",
		"ctrl+z", "ctrl+y", "ctrl+o", "ctrl+n", "ctrl+b", "ctrl+s", "f5", "a",
	} {
		press(ws, k)
	}
	ws.handleMouse(mouse(0, 0, tea.MouseButtonLeft, tea.MouseActionPress))
	ws.handleMouse(mouse(0, 0, tea.MouseButtonLeft, tea.MouseActionMotion))
	ws.handleMouse(mouse(99, 99, tea.MouseButtonWheelDown, tea.MouseActionPress))
}
