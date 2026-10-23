package editor

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F15's render half: the two display-only injections (ghost rows and the
// inline prompt) and the explain panel's split of the Code File pane.
//
// Every assertion here is about a ROW BUDGET or a column count, because
// that is the class of defect this codebase has shipped three times -- F13b's
// stale dialog declaring a height two rows short of its own output, F9b's
// card emitting five rows into a two-row budget, and the diff card's
// editPanes before its own invariant was written down. An injected row is
// the same shape of risk: it adds output to a pane whose height was already
// decided.

func editorAt(t *testing.T, content string, w, h int) (*Workspace, layout.Layout) {
	t.Helper()
	ws := New(Options{Theme: testTheme()})
	ws.sendFn = func(string, queryKind, map[string]any) {}
	ws.writeFn = func(string, string, map[string]any) error { return nil }
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.sessionID = "sess-editor"
	ws.docs = append(ws.docs, testDoc(t, content))
	ws.focus = focusBuffer
	l := layout.Compute(w, h, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: true,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: ws.Cards(),
	})
	ws.View(l) // one frame, so the geometry fields are recorded
	return ws, l
}

// twenty lines, so there is always something below the anchor to be pushed
// off the bottom.
func numberedFile(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteString("_x\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// The ghost is drawn, after its anchor, and the pane is still exactly as
// tall as it was.
func TestGhostRowsAppearAfterTheAnchorWithoutGrowingThePane(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}

	before := strings.Count(ws.View(l), "\n")

	ws.ghost = &ghostText{anchorLine: 2, lines: []string{"GHOST_A", "GHOST_B"}}
	frame := ws.View(l)

	out := stripANSI(frame)
	if !strings.Contains(out, "GHOST_A") || !strings.Contains(out, "GHOST_B") {
		t.Fatal("the ghost was not drawn")
	}
	if got := strings.Count(frame, "\n"); got != before {
		t.Errorf("the frame grew from %d to %d rows — the injected rows must be "+
			"paid for out of the pane's own budget", before+1, got+1)
	}

	// Order: after the anchor line, before the line that follows it.
	lines := strings.Split(out, "\n")
	idxA, idxGhost := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "line3_x") && idxA < 0 { // the anchor, 1-based line 3
			idxA = i
		}
		if strings.Contains(line, "GHOST_A") {
			idxGhost = i
		}
	}
	if idxA < 0 || idxGhost < 0 {
		t.Fatalf("anchor at %d, ghost at %d", idxA, idxGhost)
	}
	if idxGhost != idxA+1 {
		t.Errorf("the ghost is at row %d and its anchor at %d — it must sit "+
			"immediately after the line the question was asked at", idxGhost, idxA)
	}
}

// A ghost whose anchor has scrolled out of view is not drawn somewhere else.
// A proposed insertion rendered away from where it would land is a lie
// about where it would land.
func TestAGhostOffScreenIsNotDrawnElsewhere(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	ws.ghost = &ghostText{anchorLine: 0, lines: []string{"GHOST_A"}}
	ws.docs[0].top = 40

	if out := stripANSI(ws.View(l)); strings.Contains(out, "GHOST_A") {
		t.Error("a ghost anchored off screen was drawn anyway")
	}
}

// The prompt is one row at the caret, and the frame does not grow.
func TestThePromptIsOneRowAtTheCaret(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 4}
	before := strings.Count(ws.View(l), "\n")

	ws.BeginFill()
	ws.prompt.runes = []rune("add error handling")
	frame := ws.View(l)
	out := stripANSI(frame)

	if !strings.Contains(out, "fill · after line 5") {
		t.Errorf("the prompt does not name the mode and the anchor:\n%s", out)
	}
	if !strings.Contains(out, "add error handling") {
		t.Error("the typed instruction is not shown")
	}
	if got := strings.Count(frame, "\n"); got != before {
		t.Errorf("the frame grew from %d to %d rows", before+1, got+1)
	}
}

// The panel splits the pane rather than covering it: the code is still
// there, side by side, and the frame is the same size.
func TestTheExplainPanelSplitsThePaneAndKeepsTheFrameSize(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	before := ws.View(l)
	beforeRows := strings.Split(before, "\n")

	ws.BeginExplain()
	ws.panel.setText("Because it returns early.", false)
	frame := ws.View(l)
	rows := strings.Split(frame, "\n")
	out := stripANSI(frame)

	if len(rows) != len(beforeRows) {
		t.Errorf("the frame went from %d to %d rows", len(beforeRows), len(rows))
	}
	if !strings.Contains(out, "Explaining") {
		t.Error("the panel's title is missing")
	}
	if !strings.Contains(out, "Because it returns early.") {
		t.Error("the explanation is not shown")
	}
	if !strings.Contains(out, "c send to chat") {
		t.Error("the panel does not advertise the key it claims")
	}
	// The code it is explaining is still on screen -- the whole reason
	// this is a split and not an app.Overlay.
	if !strings.Contains(out, "line1_x") {
		t.Error("the panel covered the code it is about")
	}
	// Every row the same width, which is what sideBySide's padCells is
	// for and what a mis-sized sub-rect would break.
	width := visibleWidth(rows[0])
	for i, row := range rows {
		if got := visibleWidth(row); got != width {
			t.Fatalf("row %d is %d cells wide, row 0 is %d", i, got, width)
		}
	}
}

// Narrow terminals: below the split's floor the panel takes the pane
// rather than leaving two useless columns.
func TestThePanelTakesTheWholePaneWhenThereIsNoRoomToSplit(t *testing.T) {
	for _, mainW := range []int{20, 40, 55, 56, 80, 200} {
		got := panelWidth(mainW)
		switch {
		case mainW < 56 && got != mainW:
			t.Errorf("panelWidth(%d) = %d, want the whole pane", mainW, got)
		case mainW >= 56 && got > mainW-24:
			t.Errorf("panelWidth(%d) = %d, leaves the code %d cells",
				mainW, got, mainW-got)
		case mainW >= 56 && got < 30:
			t.Errorf("panelWidth(%d) = %d, too narrow to read", mainW, got)
		}
	}
}

// And it renders at every height the workspace can be given, including
// below its own floor, without overrunning.
func TestThePanelRendersAtEveryHeight(t *testing.T) {
	for _, h := range []int{16, 17, 20, 24, 40} {
		ws, l := editorAt(t, numberedFile(20), 120, h)
		ws.BeginExplain()
		ws.panel.setText(strings.Repeat("a long explanation that wraps. ", 20), true)
		frame := ws.View(l)
		rows := strings.Split(frame, "\n")
		width := visibleWidth(rows[0])
		for i, row := range rows {
			if got := visibleWidth(row); got != width {
				t.Fatalf("h=%d: row %d is %d cells, row 0 is %d", h, i, got, width)
			}
		}
	}
}

// wrapCells is the panel's own wrap. Indentation survives, because an
// explanation quotes code and "losing it makes code read as DIFFERENT
// code" (F9a's result card, same problem).
func TestWrapCellsKeepsIndentationAndHardCutsLongWords(t *testing.T) {
	got := wrapCells("    if not path.exists():\n\nplain", 14)
	if len(got) < 2 {
		t.Fatalf("wrapped to %#v", got)
	}
	if !strings.HasPrefix(got[0], "    ") {
		t.Errorf("first row %q lost its indent", got[0])
	}
	for i, row := range got {
		if visibleWidth(row) > 14 {
			t.Errorf("row %d is %d cells: %q", i, visibleWidth(row), row)
		}
	}

	long := wrapCells(strings.Repeat("x", 40), 10)
	for i, row := range long {
		if visibleWidth(row) > 10 {
			t.Errorf("long-word row %d is %d cells", i, visibleWidth(row))
		}
	}
	if strings.Count(strings.Join(long, ""), "x") != 40 {
		t.Errorf("hard-cutting a long word dropped characters: %#v", long)
	}
}

// F15's gate: a question longer than the panel was cut at one row and the
// rest was unreachable -- the scroll offset belongs to the body, so no
// amount of scrolling could ever have shown it.
func TestALongQuestionWrapsInsideThePanel(t *testing.T) {
	const question = "explain why this function validates its input the way it " +
		"does and what happens otherwise"

	ws, l := editorAt(t, numberedFile(20), 120, 30)
	ws.BeginExplain()
	ws.inv.question = question
	ws.panel = newExplainPanel(ws.inv)
	ws.panel.setText("short", true)

	frame := ws.View(l)
	out := stripANSI(frame)

	// The whole question is on screen, across however many rows it takes.
	// Checked word by word rather than as one string, because it is
	// wrapped and therefore legitimately broken by newlines and padding.
	for _, word := range strings.Fields(question) {
		if !strings.Contains(out, word) {
			t.Errorf("%q is missing from the panel:\n%s", word, out)
		}
	}
	// And the panel still does not overflow its own box.
	rows := strings.Split(frame, "\n")
	width := visibleWidth(rows[0])
	for i, row := range rows {
		if got := visibleWidth(row); got != width {
			t.Fatalf("row %d is %d cells, row 0 is %d: %q", i, got, width, stripANSI(row))
		}
	}
	// The answer is still visible: the question cannot take the panel.
	if !strings.Contains(out, "short") {
		t.Error("the wrapped question pushed the answer off the panel")
	}
}

// A pathologically long question is capped, with the cut marked, so the
// answer survives.
func TestAnEnormousQuestionIsCappedAndMarked(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	ws.BeginExplain()
	ws.inv.question = strings.Repeat("why does this happen ", 40)
	ws.panel = newExplainPanel(ws.inv)
	ws.panel.setText("the answer", true)

	out := stripANSI(ws.View(l))
	if !strings.Contains(out, "…") {
		t.Error("a capped question does not say it was cut")
	}
	if !strings.Contains(out, "the answer") {
		t.Error("the question pushed the answer off the panel")
	}
}

// ---------------------------------------------------------------------------
// Injected rows and the click mapping
// ---------------------------------------------------------------------------

// The counter and the renderer must agree, or the click mapping is wrong by
// however much they differ. Asserted over a matrix rather than trusted.
func TestTheInjectionCountMatchesWhatIsRendered(t *testing.T) {
	ws, _ := editorAt(t, numberedFile(20), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 3}

	for _, tc := range []struct {
		name   string
		set    func()
		line   int
		expect int
	}{
		{"nothing", func() { ws.prompt, ws.ghost = nil, nil }, 3, 0},
		{"prompt at the caret", func() { ws.prompt = newFillPrompt(d, ws.theme) }, 3, 1},
		{"prompt elsewhere", func() {}, 5, 0},
		{"ghost of three", func() {
			ws.prompt = nil
			ws.ghost = &ghostText{anchorLine: 3, lines: []string{"a", "b", "c"}}
		}, 3, 3},
		{"both at once", func() { ws.prompt = newFillPrompt(d, ws.theme) }, 3, 4},
	} {
		tc.set()
		counted := ws.viewIndexAt(d, 40).injectedAfter(tc.line)
		if counted != tc.expect {
			t.Errorf("%s: counted %d, want %d", tc.name, counted, tc.expect)
		}
		// The index and the renderer must agree EXACTLY: every row the
		// index claims draws something, and the row after the last one
		// draws nothing. A disagreement here is a click landing on the
		// wrong line by the difference.
		for row := 0; row < counted; row++ {
			if ws.injectedRowView(d, tc.line, row, 4, 40) == "" {
				t.Errorf("%s: the index claims %d rows but row %d draws nothing",
					tc.name, counted, row)
			}
		}
		if got := ws.injectedRowView(d, tc.line, counted, 4, 40); got != "" {
			t.Errorf("%s: the index claims %d rows but row %d still draws %q",
				tc.name, counted, counted, stripANSI(got))
		}
	}
}

// F15's gate: clicking what looked like line 7 put the caret past line 12,
// because the ghost rows above it were not subtracted out.
func TestAClickBelowGhostRowsLandsOnTheLineItLooksLike(t *testing.T) {
	ws, _ := editorAt(t, numberedFile(20), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}
	ws.ghost = &ghostText{anchorLine: 2, lines: []string{"g1", "g2", "g3", "g4", "g5"}}

	target, found := screenRowDrawing(ws, "line7_x")
	if !found {
		t.Fatal("line7 is not on screen")
	}

	ws.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: ws.textArea.X + 1, Y: target,
	})
	if got := d.cursor.Line; got != 6 {
		t.Errorf("clicking the row that draws line 7 moved the caret to line %d "+
			"(0-based %d) -- the %d ghost rows above it were not subtracted",
			got+1, got, ws.ghost.rows())
	}
}

// And a click on a ghost row itself moves nothing: it is not a line in the
// file, so snapping to a nearby real line would move the caret somewhere
// the user did not click.
func TestAClickOnAGhostRowMovesNothing(t *testing.T) {
	ws, _ := editorAt(t, numberedFile(20), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}
	before := d.cursor
	ws.ghost = &ghostText{anchorLine: 2, lines: []string{"GHOST_A", "GHOST_B"}}

	target, found := screenRowDrawing(ws, "GHOST_A")
	if !found {
		t.Fatal("the ghost is not on screen")
	}

	ws.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: ws.textArea.X + 1, Y: target,
	})
	if d.cursor != before {
		t.Errorf("a click on a ghost row moved the caret to %+v", d.cursor)
	}
	if ws.focus != focusBuffer {
		t.Error("the click did not even focus the pane")
	}
}

// screenRowDrawing finds the SCREEN row that is drawing `needle`.
//
// The conversion is the point of this helper and is easy to get wrong --
// it was, first time round, and the resulting off-by-two looked exactly
// like the mapping bug under test. Workspace.View returns the WORKSPACE's
// own block, with no header above it; textArea.Y is in SCREEN coordinates,
// which include whatever chrome Root draws. So a row's index inside this
// block is not its screen row, and the two are related through the block's
// own chrome: index 0 is the box's top border, index 1 the tab strip, and
// index 2 is the first code row -- which is the row textArea.Y names.
func screenRowDrawing(ws *Workspace, needle string) (int, bool) {
	const firstCodeRowInBlock = 2 // the box border, then the tab strip
	block := strings.Split(stripANSI(ws.View(ws.lastLayout)), "\n")
	for i, row := range block {
		if strings.Contains(row, needle) {
			return ws.textArea.Y + i - firstCodeRowInBlock, true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// The panel's three header fields, and the prompt's accent frame
// ---------------------------------------------------------------------------

// Three DISTINCT fields: what was explained, which lines, and what was
// asked -- not one decorative sentence.
func TestThePanelHeaderShowsScopeRangeAndQuestionSeparately(t *testing.T) {
	const file = "import os\n" + // 1
		"\n" + // 2
		"def add(a, b):\n" + // 3
		"    return a + b" // 4

	ws, l := editorAt(t, file, 120, 30)
	ws.docs[0].cursor = Pos{Line: 3} // inside add()
	cmd := ws.BeginExplain()
	if cmd != nil {
		cmd()
	}
	if ws.panel == nil {
		t.Fatal("no panel")
	}
	ws.panel.setText("It sums its two arguments.", true)

	out := stripANSI(ws.View(l))
	for _, want := range []string{"function add(a, b)", "lines 3-4", `"explain this"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel does not show %q:\n%s", want, out)
		}
	}
	// And they are on DIFFERENT rows -- the whole point of the change.
	var scopeRow, questionRow = -1, -1
	for i, row := range strings.Split(out, "\n") {
		if strings.Contains(row, "function add(a, b)") {
			scopeRow = i
		}
		if strings.Contains(row, `"explain this"`) {
			questionRow = i
		}
	}
	if scopeRow < 0 || questionRow < 0 {
		t.Fatalf("scope row %d, question row %d", scopeRow, questionRow)
	}
	if scopeRow == questionRow {
		t.Error("the scope and the question are folded into one line")
	}
}

// scopeName names the kind and the name, and says "selection" when nothing
// encloses the target -- which is the honest answer for top-level code.
func TestScopeNameNamesTheBlock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		start int
		end   int
		want  string
	}{
		{"python def", []string{"def add(a, b):", "    return a + b"}, 1, 2, "function add(a, b)"},
		{"async def", []string{"async def go(x):", "    pass"}, 1, 2, "function go(x)"},
		{"python class", []string{"class Foo:", "    pass"}, 1, 2, "class Foo"},
		{"go func", []string{"func Add(a, b int) int {", "\treturn a + b", "}"}, 1, 3, "function Add(a, b int) int"},
		{"go type", []string{"type Foo struct {", "\tA int", "}"}, 1, 3, "type Foo struct"},
		{"top level", []string{"a = 1", "b = 2"}, 1, 2, "selection"},
		{"out of range", []string{"a = 1"}, 0, 0, "selection"},
	} {
		if got := scopeName(tc.lines, tc.start, tc.end); got != tc.want {
			t.Errorf("%s: scopeName = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The prompt's frame and label are theme ROLES, so a palette change
// repaints them. Asserted by rendering the same prompt under two palettes
// and requiring the escape sequences to differ -- a hardcoded colour would
// make them identical.
func TestThePromptFrameIsThemed(t *testing.T) {
	ws, l := editorAt(t, numberedFile(20), 120, 30)
	ws.docs[0].cursor = Pos{Line: 5}
	ws.BeginFill()
	ws.prompt.runes = []rune("add error handling")

	first := ws.View(l)
	if !strings.Contains(stripANSI(first), promptEdge) {
		t.Errorf("the prompt has no accent frame:\n%s", stripANSI(first))
	}

	ws.SetTheme(theme.New("butterfly", theme.BackgroundTransparent))
	second := ws.View(l)

	rowOf := func(frame string) string {
		for _, row := range strings.Split(frame, "\n") {
			if strings.Contains(stripANSI(row), "add error handling") {
				return row
			}
		}
		return ""
	}
	a, b := rowOf(first), rowOf(second)
	if a == "" || b == "" {
		t.Fatal("the prompt row is not on screen")
	}
	if a == b {
		t.Error("the prompt renders identically under two palettes -- its " +
			"colours are hardcoded rather than theme roles")
	}
	if stripANSI(a) != stripANSI(b) {
		t.Errorf("the TEXT changed with the palette:\n%q\n%q", stripANSI(a), stripANSI(b))
	}
}

// Mouse scrolling while a ghost is live.
//
// The gate reported "scrolling glitches while a ghost stream is active".
// What was provably wrong: codeFileBox handed ensureVisible the FULL row
// count, ignoring the rows the ghost and the prompt take out of the same
// budget -- so a caret the arithmetic called visible could in fact have
// been pushed off the bottom, and the reveal then fought the wheel.
//
// The invariants asserted here are the ones a reader would call "not
// glitching", and NOT "one line per tick": a tick is three lines by design
// (mouse.go's wheel(m, +/-3)), which is the ordinary convention and was the
// first thing this test got wrong about its own subject.
func TestWheelScrollingWithAGhostIsStableAndReversible(t *testing.T) {
	const wheelStep = 3 // mouse.go's own constant, stated rather than assumed

	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}
	ws.ghost = &ghostText{anchorLine: 2, lines: []string{"g1", "g2", "g3", "g4", "g5"}}
	ws.View(l)

	wheel := func(button tea.MouseButton) {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: button,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
	}

	for tick := 1; tick <= 5; tick++ {
		wheel(tea.MouseButtonWheelDown)
		moved := d.top
		// The frame that could snap it back -- which is the glitch.
		ws.View(l)
		if d.top != moved {
			t.Fatalf("tick %d: the frame moved the viewport from %d to %d after the "+
				"wheel had set it -- the reveal is fighting the scroll", tick, moved, d.top)
		}
		if want := tick * wheelStep; d.top != want {
			t.Fatalf("tick %d: top = %d, want %d (%d lines per tick)",
				tick, d.top, want, wheelStep)
		}
	}

	// And back: scrolling up returns exactly where it came from.
	for tick := 4; tick >= 0; tick-- {
		wheel(tea.MouseButtonWheelUp)
		ws.View(l)
		if want := tick * wheelStep; d.top != want {
			t.Fatalf("scrolling back: top = %d, want %d", d.top, want)
		}
	}

	// The ghost is still anchored to its own line, not to the viewport.
	if ws.ghost.anchorLine != 2 {
		t.Errorf("the ghost moved with the scroll: anchor = %d", ws.ghost.anchorLine)
	}
}

// ---------------------------------------------------------------------------
// Auto-follow a growing ghost, the way Chat follows a growing reply
// ---------------------------------------------------------------------------

// A region longer than the pane streams INTO VIEW rather than off the
// bottom. F15's gate: "ghost content streams past the bottom of the
// viewport and just disappears; scrolling down shows blank content".
func TestAGrowingGhostPullsTheViewportAlong(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 5}
	ws.ghost = &ghostText{anchorLine: 5}
	ws.ghostFollow = true
	ws.View(l)

	lastVisible := func() string {
		block := strings.Split(stripANSI(ws.View(l)), "\n")
		// The last non-blank content row of the code pane.
		for i := 2 + ws.textArea.H - 1; i >= 2; i-- {
			if i < len(block) && strings.TrimSpace(strings.Trim(block[i], "│ ")) != "" {
				return block[i]
			}
		}
		return ""
	}

	// Grow it well past the pane's height, one row at a time.
	for n := 1; n <= 40; n++ {
		ws.ghost.lines = longGhost(n)
		if n < ws.textArea.H {
			continue
		}
		row := lastVisible()
		want := "ghost_line_" + strconv.Itoa(n)
		if !strings.Contains(row, want) {
			t.Fatalf("with %d ghost rows the pane's last row is %q, want it to show "+
				"%q -- the stream is running off the bottom instead of pulling the "+
				"viewport along", n, strings.TrimSpace(row), want)
		}
	}
}

// Scrolling away stops the follow; scrolling back to the edge resumes it.
// Chat's `pinned` semantics, and the resume half is the one most likely to
// be left out.
func TestScrollingAwayFromAGhostStopsTheFollowAndComingBackResumesIt(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 5}
	ws.ghost = &ghostText{anchorLine: 5, lines: longGhost(40)}
	ws.ghostFollow = true
	ws.View(l)

	followed := d.top
	if followed == 0 {
		t.Fatal("a 40-row ghost in a 20-row pane did not move the viewport at all")
	}

	// Up and away.
	ws.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp,
		X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
	})
	if ws.ghostFollow {
		t.Error("scrolling up did not stop the follow")
	}
	scrolled := d.top
	ws.View(l)
	if d.top != scrolled {
		t.Errorf("the frame dragged the viewport back from %d to %d after the user "+
			"scrolled away", scrolled, d.top)
	}

	// And back down to the edge.
	for i := 0; i < 4 && !ws.ghostFollow; i++ {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
		ws.View(l)
	}
	if !ws.ghostFollow {
		t.Error("scrolling back down to the live edge did not resume the follow")
	}
}

// A ghost that FITS is not followed at all: the viewport is the user's.
func TestAGhostThatFitsDoesNotMoveTheViewport(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 2}
	ws.ghost = &ghostText{anchorLine: 2, lines: longGhost(4)}
	ws.ghostFollow = true

	ws.View(l)
	if d.top != 0 {
		t.Errorf("top = %d, want 0: a ghost that fits has no live edge to follow", d.top)
	}

	// And a deliberate scroll is left alone.
	for i := 0; i < 3; i++ {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
	}
	scrolled := d.top
	ws.View(l)
	if d.top != scrolled {
		t.Errorf("the frame moved the viewport from %d to %d; a ghost that fits "+
			"must not reposition the pane", scrolled, d.top)
	}
}

// The whole of a long region is reachable by scrolling, which it was not
// before the ghost had its own window: its rows hang off one buffer line,
// so no file scroll position could ever show more than (pane height - 1)
// of them.
func TestTheWholeOfALongGhostIsReachableByScrolling(t *testing.T) {
	ws, l := editorAt(t, numberedFile(60), 120, 30)
	d := ws.docs[0]
	d.cursor = Pos{Line: 5}
	ws.ghost = &ghostText{anchorLine: 5, lines: longGhost(40)}
	ws.ghostFollow = true
	ws.View(l)

	seen := map[string]bool{}
	note := func() {
		for _, row := range strings.Split(stripANSI(ws.View(l)), "\n") {
			for n := 1; n <= 40; n++ {
				if strings.Contains(row, "ghost_line_"+strconv.Itoa(n)+" ") ||
					strings.HasSuffix(strings.TrimRight(row, " │"), "ghost_line_"+strconv.Itoa(n)) {
					seen["ghost_line_"+strconv.Itoa(n)] = true
				}
			}
		}
	}
	note()

	// All the way up through the region, then back down.
	for i := 0; i < 20; i++ {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
		note()
	}
	for i := 0; i < 20; i++ {
		ws.handleMouse(tea.MouseMsg{
			Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown,
			X: ws.textArea.X + 1, Y: ws.textArea.Y + 1,
		})
		note()
	}

	var missing []string
	for n := 1; n <= 40; n++ {
		if !seen["ghost_line_"+strconv.Itoa(n)] {
			missing = append(missing, strconv.Itoa(n))
		}
	}
	if len(missing) > 0 {
		t.Errorf("these ghost rows were never visible at any scroll position: %s",
			strings.Join(missing, ","))
	}
	// The follow is deliberately NOT asserted here any more. Under the
	// unified model (viewrows.go) a downward sweep this long does not stop
	// at the region's last row -- it carries on into the code after the
	// insertion point, which is the whole point of the rewrite, and
	// leaves the follow off. The resume is a separate behaviour with a
	// test of its own above
	// (TestScrollingAwayFromAGhostStopsTheFollowAndComingBackResumesIt),
	// and asserting it here as well was what made that sweep and the
	// reachability it is about disagree.
}
