package app

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ---------------------------------------------------------------------------
// Done-when #8 — discard removes the CARD, never the RESULT
// ---------------------------------------------------------------------------

// The whole criterion, end to end: open a result from the tray, discard it,
// and the same task is still in the tray and still re-fetchable with the same
// text.
//
// The property that makes this true is structural rather than careful — the
// card has no delete path to call, because this build has no event that could
// remove a task or a message. The test asserts the observable consequence so
// a later episode that DOES gain such a path cannot wire it here by accident.
func TestDiscardKeepsTheResultRetrievable(t *testing.T) {
	r := newOverlayCapableRoot()
	row := TaskRow{
		ID: "t1", State: TaskStateCompleted, Agent: "research",
		Session: "s1", StartedAt: 1000, FinishedAt: 1063,
	}
	rows := SortTaskRows([]TaskRow{row})
	r.tasks = rows

	// Open the tray, then select the finished row.
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(rows)}}, MinW: 50, MinH: 12})
	tray := &taskTray{Popup: popup}
	r.tray = trayState{popup: popup, rows: rows}
	r.overlay = tray

	// The fetch's reply opens the card.
	r.tray.resultReqID, r.tray.resultTask = "req", row
	r.handleTrayEnvelope(protocol.Envelope{
		Type: protocol.EvSession, CorrID: "req", Payload: resultPayload(t, "t1", "the answer"),
	})
	card, ok := r.overlay.(*ResultCard)
	if !ok {
		t.Fatalf("overlay = %T, want *ResultCard", r.overlay)
	}

	// `d` discards — and returns the TRAY in the card's place, not nil.
	next, cmd := card.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if next == nil {
		t.Fatal("discard closed everything; it should return to the tray list")
	}
	if next != Overlay(tray) {
		t.Errorf("discard returned %T, want the same tray instance it was opened from", next)
	}
	if cmd == nil {
		t.Fatal("discard produced no cmd — Root never learns which card went")
	}
	if _, ok := cmd().(resultDiscardedMsg); !ok {
		t.Fatalf("discard cmd produced %T, want resultDiscardedMsg", cmd())
	}

	// The task is STILL in the tray's rows, and still in Root's snapshot.
	if _, found := r.trayRow("t1"); !found {
		t.Error("the discarded card's task is gone from the tray — #8 broken")
	}
	if len(r.tasks) != 1 || r.tasks[0].ID != "t1" {
		t.Errorf("Root's snapshot lost the task: %v", r.tasks)
	}
	// And the tray would still offer it as a finished row to open.
	items := trayItems(r.tray.rows)
	if len(items) != 1 || items[0].GetID() != "t1" {
		t.Fatalf("tray items after discard = %v", items)
	}

	// Re-fetching yields the same text — nothing was consumed.
	r.tray.resultReqID, r.tray.resultTask = "req2", row
	r.handleTrayEnvelope(protocol.Envelope{
		Type: protocol.EvSession, CorrID: "req2", Payload: resultPayload(t, "t1", "the answer"),
	})
	again, ok := r.overlay.(*ResultCard)
	if !ok {
		t.Fatalf("reopen gave %T", r.overlay)
	}
	if again.body != "the answer" {
		t.Errorf("reopened body = %q, want the same text", again.body)
	}
}

// Root's handler for the discard message must not clean anything up — there
// is nothing to clean, and a future edit that starts pruning r.tasks here
// would break #8 silently.
func TestRootDiscardHandlerTouchesNothing(t *testing.T) {
	r := newOverlayCapableRoot()
	r.tasks = []TaskRow{{ID: "t1", State: TaskStateCompleted}}
	r.tray.rows = r.tasks

	_, _ = r.update(resultDiscardedMsg{"t1"})

	if len(r.tasks) != 1 {
		t.Errorf("r.tasks = %v, want untouched", r.tasks)
	}
	if len(r.tray.rows) != 1 {
		t.Errorf("tray rows = %v, want untouched", r.tray.rows)
	}
	if !strings.Contains(r.status, "still in the tray") {
		t.Errorf("status = %q, want it to say the result survived", r.status)
	}
}

// esc and d both dismiss in this scope — deliberately, since with no attach
// and no save there is nothing for them to differ about. Both are asserted so
// a later episode that makes them diverge has to change a test that says why.
//
// Dismiss means "return to where this came from": the tray when there is one,
// nil (close outright) when there is not.
func TestEscAndDiscardBothDismiss(t *testing.T) {
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(nil)}}})
	tray := &taskTray{Popup: popup}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("d")},
	} {
		withTray := newResultCard(TaskRow{ID: "t", State: TaskStateCompleted}, nil)
		withTray.back = tray
		if next, _ := withTray.Update(key); next != Overlay(tray) {
			t.Errorf("%v returned %T, want the tray", key, next)
		}

		// No tray behind it (a card opened from anywhere else): closes.
		alone := newResultCard(TaskRow{ID: "t", State: TaskStateCompleted}, nil)
		if next, _ := alone.Update(key); next != nil {
			t.Errorf("%v on a card with no tray behind it left %T open", key, next)
		}
	}
}

// Returning the SAME tray instance is what keeps the user where they were.
// Rebuilding one would reset a typed filter and move the selection, which is
// the whole reason `back` holds an Overlay rather than a bool.
func TestReturningToTheTrayKeepsItsFilterAndSelection(t *testing.T) {
	r := newOverlayCapableRoot()
	rows := SortTaskRows([]TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateCompleted, Session: "s1", FinishedAt: 20},
		{ID: "t2", Agent: "research", State: TaskStateCompleted, Session: "s1", FinishedAt: 10},
	})
	popup := NewPopup(PopupConfig{Tabs: []PopupTab{{Items: trayItems(rows)}}, MinW: 50, MinH: 12})
	tray := &taskTray{Popup: popup}
	r.tray = trayState{popup: popup, rows: rows}
	r.overlay = tray

	popup.setFilter("re")
	popup.menu.MoveDown()
	wantSel, _ := popup.menu.Selected()

	r.tray.resultReqID, r.tray.resultTask = "req", rows[1]
	r.handleTrayEnvelope(protocol.Envelope{
		Type: protocol.EvSession, CorrID: "req", Payload: resultPayload(t, "t2", "answer"),
	})
	card, ok := r.overlay.(*ResultCard)
	if !ok {
		t.Fatalf("overlay = %T", r.overlay)
	}

	next, _ := card.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next != Overlay(tray) {
		t.Fatalf("esc returned %T, want the tray", next)
	}
	if popup.filter != "re" {
		t.Errorf("filter = %q after the round trip, want %q", popup.filter, "re")
	}
	gotSel, _ := popup.menu.Selected()
	if gotSel != wantSel {
		t.Errorf("selection moved across the round trip")
	}
}

// ---------------------------------------------------------------------------
// Honest labels
// ---------------------------------------------------------------------------

// A zero token count is omitted, not printed as "0 tok". A row written before
// usage was populated does not cost nothing — it costs an unknown amount, and
// rendering the two the same is the kind of confident wrong number B12's
// estimate bug was about.
func TestResultHeaderOmitsAnUnknownTokenCount(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)

	withTokens := newResultCard(
		TaskRow{ID: "t", State: TaskStateCompleted, StartedAt: 1000, FinishedAt: 1063},
		[]trayMessage{mkMsg("t", trayRoleAssistant, "body", 12400)})
	if h := stripANSI(withTokens.headerLine(th, 60)); !strings.Contains(h, "12.4k tok") {
		t.Errorf("header = %q, want the real token count", h)
	}

	without := newResultCard(
		TaskRow{ID: "t", State: TaskStateCompleted, StartedAt: 1000, FinishedAt: 1063},
		[]trayMessage{mkMsg("t", trayRoleAssistant, "body", 0)})
	h := stripANSI(without.headerLine(th, 60))
	if strings.Contains(h, "tok") {
		t.Errorf("header = %q, want no token claim at all when it is unknown", h)
	}
	if !strings.Contains(h, "1:03") {
		t.Errorf("header = %q, want the duration, which IS known", h)
	}
}

func TestFormatTokens(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1.0k"}, {12400, "12.4k"}} {
		if got := formatTokens(tc.n); got != tc.want {
			t.Errorf("formatTokens(%d) = %q want %q", tc.n, got, tc.want)
		}
	}
}

// A failed task's header names its error code — the same information the
// tray row carries, so the two cannot disagree about why it failed.
func TestResultHeaderNamesTheFailureCode(t *testing.T) {
	c := newResultCard(TaskRow{ID: "t", State: TaskStateFailed, ErrorCode: "tool_timeout"}, nil)
	if got := c.stateWord(); got != "failed: tool_timeout" {
		t.Errorf("got %q", got)
	}
}

// The card's title carries the user's real question — the thing the tray
// cannot show, because ListTasks has no prompt field. This is the only place
// in the UI where F09's "Research · tokio vs asyncio" becomes true.
func TestResultTitleCarriesTheQuestion(t *testing.T) {
	c := newResultCard(TaskRow{ID: "t", Agent: "research", State: TaskStateCompleted},
		[]trayMessage{mkMsg("t", trayRoleUser, "tokio vs asyncio", 0)})
	if c.Title() != "research · tokio vs asyncio" {
		t.Errorf("title = %q", c.Title())
	}

	// A multi-line question collapses to its first line: the title sits in
	// a border and a second line would push the box apart.
	multi := newResultCard(TaskRow{ID: "t", Agent: "research", State: TaskStateCompleted},
		[]trayMessage{mkMsg("t", trayRoleUser, "first line\nsecond line", 0)})
	if strings.Contains(multi.Title(), "\n") {
		t.Errorf("title = %q, want one line", multi.Title())
	}
	if !strings.Contains(multi.Title(), "first line") {
		t.Errorf("title = %q", multi.Title())
	}

	// No question on record: just the label, never a dangling separator.
	bare := newResultCard(TaskRow{ID: "t", Agent: "research", State: TaskStateCompleted}, nil)
	if bare.Title() != "research" {
		t.Errorf("title = %q, want a bare label with no trailing ' · '", bare.Title())
	}
}

// A task that persisted no assistant text still OPENS, with an explanation.
// Done-when #1 is that the result opens; a card that refuses cannot be told
// apart from a broken tray.
func TestEmptyResultStillOpensAndExplains(t *testing.T) {
	c := newResultCard(TaskRow{ID: "t", State: TaskStateCancelled}, nil)
	if c.body == "" {
		t.Fatal("empty body — the card would render a blank box")
	}
	if !strings.Contains(c.body, "persisted no result text") {
		t.Errorf("body = %q", c.body)
	}
	if !strings.Contains(c.body, "Nothing has been deleted") {
		t.Error("the empty state must say the result was not deleted — that is #8's promise")
	}
}

// ---------------------------------------------------------------------------
// Wrapping and scrolling
// ---------------------------------------------------------------------------

// wrapParagraphs must preserve blank lines. placeholder.go's wrapPlain runs
// strings.Fields over the whole string and collapses every newline, which is
// right for one-line status text and would turn a research result into one
// wall of prose — the reason the two are separate functions.
func TestWrapParagraphsPreservesStructure(t *testing.T) {
	got := wrapParagraphs("first para\n\nsecond para", 40)
	if len(got) != 3 || got[0] != "first para" || got[1] != "" || got[2] != "second para" {
		t.Errorf("got %q, want the blank line kept", got)
	}

	// And the existing wrapPlain must still collapse it — F1's placeholder
	// depends on that, and this episode did not change it.
	if flat := wrapPlain("first para\n\nsecond para", 40); len(flat) != 1 {
		t.Errorf("wrapPlain = %q, want one collapsed line (unchanged behaviour)", flat)
	}
}

// Rule 8: display width, not len(). A wrap computed on runes lets a CJK line
// overflow by its own width again and tears the border.
func TestWrapParagraphsRespectsDisplayWidth(t *testing.T) {
	for _, line := range wrapParagraphs(strings.Repeat("日本語 ", 20), 20) {
		if w := runewidth.StringWidth(line); w > 20 {
			t.Errorf("line %q is %d cells wide, over the 20-cell budget", line, w)
		}
	}
}

// A single word longer than the width is hard-split rather than allowed to
// overflow — a long path or URL in a research result is the real case.
func TestWrapParagraphsHardSplitsAnOverlongWord(t *testing.T) {
	lines := wrapParagraphs(strings.Repeat("x", 50), 10)
	if len(lines) < 5 {
		t.Fatalf("got %d lines for 50 chars at width 10: %q", len(lines), lines)
	}
	for _, l := range lines {
		if runewidth.StringWidth(l) > 10 {
			t.Errorf("line %q exceeds the width", l)
		}
	}
	if joined := strings.Join(lines, ""); joined != strings.Repeat("x", 50) {
		t.Errorf("hard split lost or duplicated content: %q", joined)
	}
}

// A resize that shortens the body must re-clamp the scroll offset. Without
// it, a terminal widened from 60 to 120 columns roughly halves the line count
// and leaves the view parked past the end, rendering blank rows with no key
// pressed to fix it.
func TestResizeReclampsScroll(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	c := newResultCard(TaskRow{ID: "t", State: TaskStateCompleted},
		[]trayMessage{mkMsg("t", trayRoleAssistant, strings.Repeat("word ", 400), 0)})

	_ = c.View(th, 62, 20) // narrow: many lines
	c.scroll = c.maxScroll()
	parked := c.scroll
	if parked == 0 {
		t.Fatal("no scrollable content at 62 columns — the fixture is too small")
	}

	_ = c.View(th, 160, 20) // wide: far fewer lines
	if c.scroll > c.maxScroll() {
		t.Errorf("scroll = %d past maxScroll = %d after widening", c.scroll, c.maxScroll())
	}
	if c.scroll == parked && parked > c.maxScroll() {
		t.Error("the offset survived a resize that made it invalid")
	}
}

// Scrolling clamps at both ends: no negative offset, and no scrolling into
// blank space past the last line.
func TestScrollClampsAtBothEnds(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	c := newResultCard(TaskRow{ID: "t", State: TaskStateCompleted},
		[]trayMessage{mkMsg("t", trayRoleAssistant, strings.Repeat("word ", 400), 0)})
	_ = c.View(th, 62, 20)

	c.scrollBy(-50)
	if c.scroll != 0 {
		t.Errorf("scroll = %d after scrolling up from the top", c.scroll)
	}
	c.scrollBy(100000)
	if c.scroll != c.maxScroll() {
		t.Errorf("scroll = %d, want maxScroll = %d", c.scroll, c.maxScroll())
	}
}

// The rendered card must fit the box it is given, on every row. An overflowing
// row is what breaks a rounded border, and it is invisible in a unit test that
// only checks content.
func TestResultCardViewFitsItsBox(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	c := newResultCard(
		TaskRow{ID: "t", Agent: "research", State: TaskStateCompleted,
			StartedAt: 1000, FinishedAt: 1063},
		[]trayMessage{
			mkMsg("t", trayRoleUser, "a question long enough to need truncating in a narrow box", 0),
			mkMsg("t", trayRoleAssistant, strings.Repeat("word ", 200), 9000),
		})

	for _, size := range [][2]int{{60, 18}, {80, 24}, {120, 40}} {
		w, h := size[0], size[1]
		out := c.View(th, w, h)
		lines := strings.Split(out, "\n")
		// Exactly h lines, filling the content area it was handed and not
		// one row more -- the same accounting Popup.View does (header +
		// blank + body + rule + footer). A card one row too tall pushes
		// the bottom border off; one row short leaves a gap inside it.
		if len(lines) != h {
			t.Errorf("%dx%d: %d lines, want %d", w, h, len(lines), h)
		}
		for i, l := range lines {
			if got := visibleWidth(stripANSI(l)); got > w-2 {
				t.Errorf("%dx%d: line %d is %d cells, over the %d budget: %q",
					w, h, i, got, w-2, stripANSI(l))
			}
		}
	}
}

func resultPayload(t *testing.T, taskID, answer string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session": map[string]any{
			"messages": []trayMessage{mkMsg(taskID, trayRoleAssistant, answer, 100)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Leading whitespace is content. strings.Fields drops it, which rendered an
// indented code block left-flush -- `await` at the same level as `async def`,
// which reads as different code rather than merely worse-looking code. With
// markdown unavailable in this package, the indentation is the only structure
// a code block has left.
func TestWrapParagraphsPreservesIndentation(t *testing.T) {
	got := wrapParagraphs("Example:\n\n    async def main():\n        await gather(a(), b())", 60)
	want := []string{
		"Example:",
		"",
		"    async def main():",
		"        await gather(a(), b())",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A wrapped continuation of an indented line keeps that line's indentation,
// so a long code line's tail stays inside its own block instead of escaping
// to column zero -- which would read as a new statement at the outer level.
func TestWrapParagraphsIndentsContinuationLines(t *testing.T) {
	got := wrapParagraphs("        "+strings.Repeat("word ", 12), 30)
	if len(got) < 2 {
		t.Fatalf("expected a wrap, got %q", got)
	}
	for i, l := range got {
		if !strings.HasPrefix(l, "        ") {
			t.Errorf("line %d = %q, want the source line's 8-space indent", i, l)
		}
		if w := runewidth.StringWidth(l); w > 30 {
			t.Errorf("line %d is %d cells, over the budget: %q", i, w, l)
		}
	}
}

// Tabs are expanded rather than emitted: a terminal advances to its own next
// tab stop, which no width calculation here can predict, so a literal tab
// would make every downstream padCells wrong by however much the terminal
// decided -- and that is what tears a border.
func TestWrapParagraphsExpandsTabIndentation(t *testing.T) {
	got := wrapParagraphs("\tif x:\n\t\treturn", 40)
	if len(got) != 2 {
		t.Fatalf("got %q", got)
	}
	for _, l := range got {
		if strings.Contains(l, "\t") {
			t.Errorf("a literal tab survived: %q", l)
		}
	}
	if got[0] != "    if x:" || got[1] != "        return" {
		t.Errorf("got %q, want 4-space-per-tab expansion", got)
	}
}

// Indented further than the box is wide: keep the text, drop the
// indentation. The alternative is a column of single characters.
func TestWrapParagraphsDropsIndentItCannotAfford(t *testing.T) {
	got := wrapParagraphs(strings.Repeat(" ", 30)+"hello world", 10)
	if len(got) == 0 {
		t.Fatal("no output")
	}
	for i, l := range got {
		if w := runewidth.StringWidth(l); w > 10 {
			t.Errorf("line %d is %d cells, over the budget: %q", i, w, l)
		}
	}
	if joined := strings.Join(got, " "); !strings.Contains(joined, "hello") ||
		!strings.Contains(joined, "world") {
		t.Errorf("content lost: %q", got)
	}
}
