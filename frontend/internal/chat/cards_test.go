package chat

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{12400, "12.4k"},
	}
	for _, c := range cases {
		if got := formatTokens(c.in); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSortStableTaskRows(t *testing.T) {
	rows := []app.TaskRow{
		{ID: "a", State: app.TaskStateCompleted, CreatedAt: 3},
		{ID: "b", State: app.TaskStateRunning, CreatedAt: 2},
		{ID: "c", State: app.TaskStateQueued, CreatedAt: 1},
		{ID: "d", State: app.TaskStateRunning, CreatedAt: 0},
	}
	sortStableTaskRows(rows)

	// Running first (in original relative order: b then d), queued next,
	// terminal last.
	want := []string{"b", "d", "c", "a"}
	for i, id := range want {
		if rows[i].ID != id {
			t.Fatalf("position %d = %q, want %q (order: %v)", i, rows[i].ID, id, rowIDs(rows))
		}
	}
}

func rowIDs(rows []app.TaskRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestElapsedSince(t *testing.T) {
	if got := elapsedSince(0); got != "" {
		t.Errorf("elapsedSince(0) = %q, want empty", got)
	}
	started := float64(time.Now().Add(-42 * time.Second).Unix())
	if got := elapsedSince(started); got != "0:42" {
		t.Errorf("elapsedSince(-42s) = %q, want \"0:42\"", got)
	}
}

// The Status/Task/Sessions cards must all fall back to a dim "—" the moment
// the backend is down, never the last real numbers they had — F06's
// Done-when #7.
func TestCardsShowPlaceholderWhenBackendDown(t *testing.T) {
	w := &Workspace{
		theme:           testTheme(),
		backendDown:     true,
		usage:           wireUsage{InputTokens: 500, OutputTokens: 200, TotalTokens: 700},
		sessionProvider: "anthropic",
		tasks:           []app.TaskRow{{ID: "t1", State: app.TaskStateRunning}},
		sessions:        []wireSession{{ID: "s1", Title: "hello"}},
	}

	for name, lines := range map[string][]string{
		"status":   w.statusCardLines(28, 5),
		"task":     w.taskCardLines(28, 5),
		"sessions": w.sessionsCardLines(28, 5),
	} {
		if len(lines) != 1 || !strings.Contains(lines[0], "—") {
			t.Errorf("%s card while backend down = %v, want a single dim placeholder", name, lines)
		}
	}
}

func TestTaskCardEmptyState(t *testing.T) {
	w := &Workspace{theme: testTheme()}
	lines := w.taskCardLines(28, 5)
	if len(lines) != 1 || !strings.Contains(lines[0], "no background tasks") {
		t.Errorf("empty task card = %v, want \"no background tasks\"", lines)
	}
}

func TestSessionsCardEmptyStateAndActiveMark(t *testing.T) {
	w := &Workspace{theme: testTheme()}
	if lines := w.sessionsCardLines(28, 5); len(lines) != 1 || !strings.Contains(lines[0], "no sessions yet") {
		t.Errorf("empty sessions card = %v, want \"no sessions yet\"", lines)
	}

	w.sessionID = "active-id"
	w.sessions = []wireSession{
		{ID: "other-id", Title: "old chat"},
		{ID: "active-id", Title: "current chat"},
	}
	lines := w.sessionsCardLines(28, 5)
	if len(lines) != 2 {
		t.Fatalf("sessions card = %v, want 2 rows", lines)
	}
	if !strings.Contains(lines[1], "current chat") {
		t.Errorf("active session row = %q, want it to name the active session", lines[1])
	}
}

// Session titles must truncate by DISPLAY WIDTH with an ellipsis
// (F06-right-pane.md's Sessions card), never a hard byte/rune cut.
func TestSessionsCardTruncatesLongTitles(t *testing.T) {
	w := &Workspace{
		theme:    testTheme(),
		sessions: []wireSession{{ID: "s1", Title: strings.Repeat("a very long session title ", 5)}},
	}
	lines := w.sessionsCardLines(20, 5)
	if len(lines) != 1 {
		t.Fatalf("sessions card = %v, want 1 row", lines)
	}
	if !strings.Contains(lines[0], "…") {
		t.Errorf("truncated title = %q, want an ellipsis", lines[0])
	}
}

// A session title that happens to contain markdown-looking punctuation
// ("#", "**") is still just literal text here — the Sessions card never
// parses markdown. Regression test for a field report that suspected a
// border-width bug tied to heading/bold-looking content; every row must
// still close with the box's own border character at the box's own width.
func TestSessionsCardBorderHoldsWithMarkdownLikePunctuation(t *testing.T) {
	th := testTheme()
	w := &Workspace{
		theme: th,
		sessions: []wireSession{
			{ID: "1", Title: "# A heading-like title"},
			{ID: "2", Title: "**Bold** title here"},
			{ID: "3", Title: "normal title"},
		},
	}
	rect := layout.Rect{X: 0, Y: 0, W: 26, H: 8}
	lines := w.sessionsCardLines(rect.W-2*genericBoxPadding-2, rect.H-2)
	box := paneBox(th.Style(theme.BorderIdle), th.Style(theme.TextSecondary), rect, "Sessions", lines, genericBoxPadding)

	for i, row := range strings.Split(box, "\n") {
		if got := visibleWidth(row); got != rect.W {
			t.Errorf("row %d visible width = %d, want %d (row: %q)", i, got, rect.W, stripANSI(row))
		}
		if !strings.HasSuffix(stripANSI(row), "│") && !strings.HasSuffix(stripANSI(row), "╮") && !strings.HasSuffix(stripANSI(row), "╯") {
			t.Errorf("row %d missing its closing border character: %q", i, stripANSI(row))
		}
	}
}

// The actual root cause, confirmed from two real database rows: a session
// title containing a raw embedded newline. sessionsCardLines measures and
// truncates the title as a single line by construction (singleLineTitle),
// so the border must hold with the EXACT title text found in the field,
// CJK content included — runewidth.StringWidth, not len(), is what has to
// agree with the terminal's own column count for that content to land
// right, and this pins that down with the literal bytes rather than an
// ASCII stand-in.
func TestSessionsCardBorderHoldsWithEmbeddedNewline(t *testing.T) {
	th := testTheme()
	// Reported as "length: 40" (Python's len(), i.e. code points — Go's
	// len() would count more bytes because of the CJK runes). Sanity-check
	// with utf8.RuneCountInString that this is the same title, not an
	// exact byte-for-byte transcription of the database row.
	const fieldTitle = "# heading\n**bold** and `inline` and 日本語"
	if n := utf8.RuneCountInString(fieldTitle); n < 35 || n > 45 {
		t.Fatalf("test fixture looks nothing like the reported title: %d code points, want ~40", n)
	}

	w := &Workspace{
		theme:    th,
		sessions: []wireSession{{ID: "1", Title: fieldTitle}},
	}
	rect := layout.Rect{X: 0, Y: 0, W: 26, H: 8}
	lines := w.sessionsCardLines(rect.W-2*genericBoxPadding-2, rect.H-2)
	if len(lines) != 1 {
		t.Fatalf("sessions card = %v, want the title collapsed onto exactly 1 row", lines)
	}
	if strings.Contains(lines[0], "\n") {
		t.Fatalf("rendered line still contains a raw newline: %q", lines[0])
	}

	box := paneBox(th.Style(theme.BorderIdle), th.Style(theme.TextSecondary), rect, "Sessions", lines, genericBoxPadding)
	boxRows := strings.Split(box, "\n")
	if len(boxRows) != rect.H {
		t.Fatalf("box has %d rows, want exactly %d — a stray newline in the title expanded it", len(boxRows), rect.H)
	}
	for i, row := range boxRows {
		if got := visibleWidth(row); got != rect.W {
			t.Errorf("row %d visible width (runewidth, not len) = %d, want %d: %q", i, got, rect.W, stripANSI(row))
		}
		if !strings.HasSuffix(stripANSI(row), "│") && !strings.HasSuffix(stripANSI(row), "╮") && !strings.HasSuffix(stripANSI(row), "╯") {
			t.Errorf("row %d missing its closing border character: %q", i, stripANSI(row))
		}
	}
}

func TestSingleLineTitleStripsNewlinesAndControlCharacters(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"# heading\n**bold** and `inline` and 日本語", "# heading **bold** and `inline` and 日本語"},
		{"a\r\nb", "a b"},
		{"tab\there", "tab here"},
		{"null\x00byte", "nullbyte"},
		{"  leading and trailing  \n", "leading and trailing"},
		{"no newline here", "no newline here"},
	}
	for _, c := range cases {
		if got := singleLineTitle(c.in); got != c.want {
			t.Errorf("singleLineTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The right column (Status/Task/Sessions) must always sum to exactly the
// same height as the left column (View Port/Query Area) — layout.Compute's
// distribute() and splitGrowth's redistribution both preserve the total
// independently, so the two columns can never drift apart. Regression test
// for a field report of the Sessions card's bottom edge sitting 1-2 rows
// below the Query Area's, which pushed the status bar off-screen.
func TestRightColumnHeightMatchesLeftColumn(t *testing.T) {
	cases := []struct {
		name       string
		width      int
		height     int
		inputLines int // lines typed into the query box, to exercise splitGrowth
		populated  bool
	}{
		{"wide, no growth", 120, 30, 0, false},
		{"medium, grown query box", 95, 30, 6, false},
		// 110x35: a field report of the Sessions card sitting below the
		// Query Area's bottom edge on a completely fresh launch at exactly
		// this size, no toggle or resize involved.
		{"110x35, fresh, empty", 110, 35, 0, false},
		{"110x35, fresh, with real session/task data", 110, 35, 0, true},
		// Odd, non-round sizes either side of every breakpoint
		// (WideWidth=120, MediumWidth=90, NarrowWidth=70;
		// HeaderFullHeight=30, HeaderCompactHeight=24).
		{"89x24 (just under medium, just at compact-header floor)", 89, 24, 0, false},
		{"91x29 (just over medium, just under full-header)", 91, 29, 0, false},
		{"119x31", 119, 31, 0, false},
		{"121x37", 121, 37, 0, false},
		{"73x25", 73, 25, 0, false},
		{"105x41, with real data", 105, 41, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Workspace{theme: testTheme(), focus: -1, pinned: true}
			w.input.buf = newInputBuffer()
			w.input.focused = true
			if c.inputLines > 0 {
				var lines []string
				for i := 0; i < c.inputLines; i++ {
					lines = append(lines, "a line of text")
				}
				text := strings.Join(lines, "\n")
				w.input.buf.setText(text, len(text))
			}
			if c.populated {
				w.sessionID = "2"
				w.sessions = []wireSession{
					{ID: "1", Title: "an older conversation"},
					{ID: "2", Title: "the active one right now"},
					{ID: "3", Title: "something from yesterday"},
				}
				w.tasks = []app.TaskRow{
					{ID: "t1", Agent: "research", State: app.TaskStateRunning, StartedAt: 1000},
					{ID: "t2", Agent: "research", State: app.TaskStateQueued},
				}
				w.usage = wireUsage{InputTokens: 500, OutputTokens: 120, TotalTokens: 620}
				w.sessionProvider = "anthropic"
				w.sessionModel = "claude"
			}

			l := layout.Compute(c.width, c.height, layout.State{
				Active:        layout.WorkspaceChat,
				RightPaneOpen: true,
				HeaderAllowed: true,
				RightCards:    w.Cards(),
			})
			if l.TooSmall {
				t.Skipf("terminal too small at %dx%d: %s", c.width, c.height, l.Message)
			}

			body := w.View(l)
			rows := strings.Split(body, "\n")
			want := l.Main.H + l.Secondary.H
			if len(rows) != want {
				t.Fatalf("rendered %d rows, want %d (l.Main.H=%d l.Secondary.H=%d)",
					len(rows), want, l.Main.H, l.Secondary.H)
			}
			for i, row := range rows {
				if got := visibleWidth(row); got != l.W-2 {
					t.Errorf("row %d width = %d, want %d: %q", i, got, l.W-2, stripANSI(row))
				}
			}
		})
	}
}

// The Task card is an ACTIVITY view, not a history browser. ListTasks
// answers with up to 50 rows — every recent task in the database — and all
// but a few finished ones are dropped before anything is rendered. Active
// work is never capped; that is the question the card exists to answer.
func TestTaskCardCapsTheTerminalTailButNeverActiveWork(t *testing.T) {
	var tasks []app.TaskRow
	for i := 0; i < 6; i++ {
		tasks = append(tasks, app.TaskRow{ID: "run", Agent: "research", State: app.TaskStateRunning})
	}
	for i := 0; i < 44; i++ {
		tasks = append(tasks, app.TaskRow{ID: "old", Agent: "research", State: app.TaskStateCompleted})
	}
	w := &Workspace{theme: testTheme(), tasks: tasks}

	// 6 active + recentTerminalLimit finished are drawn, and the 41 rows
	// the scope cap dropped are COUNTED rather than silently vanishing.
	lines := w.taskCardLines(32, 40)
	if len(lines) != 6+recentTerminalLimit+1 {
		t.Fatalf("got %d rows, want 6 active + %d finished + 1 count row", len(lines), recentTerminalLimit)
	}
	if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, "+41 more") {
		t.Errorf("last row = %q, want \"+41 more\" (50 tasks, 9 shown)", last)
	}
	running := 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "⟳") {
			running++
		}
	}
	if running != 6 {
		t.Errorf("got %d running rows, want all 6 — active work is never capped", running)
	}
}

// Truncation of what the card DOES claim to show must be counted, never
// silent — a card showing 4 of 13 with no mark reads exactly like a card
// showing all there is, which is how "my new task never appears in the
// overlay" survived several rounds of investigation.
func TestTaskCardCountsWhatItCannotShow(t *testing.T) {
	var tasks []app.TaskRow
	for i := 0; i < 10; i++ {
		tasks = append(tasks, app.TaskRow{ID: "run", Agent: "research", State: app.TaskStateRunning})
	}
	for i := 0; i < 40; i++ {
		tasks = append(tasks, app.TaskRow{ID: "old", Agent: "research", State: app.TaskStateCompleted})
	}
	w := &Workspace{theme: testTheme(), tasks: tasks}

	lines := w.taskCardLines(32, 5)
	if len(lines) != 5 {
		t.Fatalf("got %d rows, want exactly 5", len(lines))
	}
	// 50 tasks in, 4 drawn. The count covers BOTH what the scope cap
	// dropped (37 old finished) and what the row budget could not fit
	// (9 more of the active ones) — 46 either way, because from the
	// reader's seat "not shown" is one number, not two.
	if last := stripANSI(lines[4]); !strings.Contains(last, "+46 more") {
		t.Errorf("last row = %q, want \"+46 more\"", last)
	}
	if first := stripANSI(lines[0]); !strings.Contains(first, "⟳") {
		t.Errorf("first row = %q, want running work to survive the cut", first)
	}
}

// A list that fits needs no overflow row at all.
func TestTaskCardHasNoOverflowRowWhenEverythingFits(t *testing.T) {
	w := &Workspace{theme: testTheme(), tasks: []app.TaskRow{
		{ID: "a", Agent: "research", State: app.TaskStateRunning},
		{ID: "b", Agent: "research", State: app.TaskStateCompleted},
	}}
	for _, l := range w.taskCardLines(32, 5) {
		if strings.Contains(stripANSI(l), "more") {
			t.Errorf("unexpected overflow row when everything fits: %q", stripANSI(l))
		}
	}
}

// Case 2 from the field review: the card claims to show recently FINISHED
// work, so it must be keyed on FinishedAt, not on created_at.
//
// A long @research created BEFORE three quick turns, and finishing AFTER
// all of them, is exactly this app's normal workload. Keyed on created_at
// the backend's newest-first order put the three quick turns ahead of it
// and the cap evicted the one task that had actually just finished.
func TestTaskCardKeepsTheMostRecentlyFinishedNotTheMostRecentlyStarted(t *testing.T) {
	// Delivered in the backend's own created_at DESC order.
	w := &Workspace{theme: testTheme(), tasks: []app.TaskRow{
		{ID: "s3", Agent: "quick-three", State: app.TaskStateCompleted, CreatedAt: 400, FinishedAt: 401},
		{ID: "s2", Agent: "quick-two", State: app.TaskStateCompleted, CreatedAt: 300, FinishedAt: 301},
		{ID: "s1", Agent: "quick-one", State: app.TaskStateCompleted, CreatedAt: 200, FinishedAt: 201},
		{ID: "lg", Agent: "long-research", State: app.TaskStateCompleted, CreatedAt: 100, FinishedAt: 999},
	}}

	rendered := strings.Join(w.taskCardLines(32, 10), "\n")
	plain := stripANSI(rendered)

	if !strings.Contains(plain, "long-research") {
		t.Errorf("the task that finished most recently was evicted:\n%s", plain)
	}
	if strings.Contains(plain, "quick-one") {
		t.Errorf("the oldest completion survived the cap:\n%s", plain)
	}
	// It finished last, so it must also sort FIRST among the finished rows.
	if first := stripANSI(w.taskCardLines(32, 10)[0]); !strings.Contains(first, "long-research") {
		t.Errorf("first finished row = %q, want the most recent completion", first)
	}
}

// Active rows keep created_at order — "when it started" is the right key
// for work still in flight, and it is what the elapsed clock counts from.
// Only the terminal group is re-keyed on FinishedAt.
func TestTaskCardLeavesActiveRowsInCreatedAtOrder(t *testing.T) {
	w := &Workspace{theme: testTheme(), tasks: []app.TaskRow{
		{ID: "newest", Agent: "started-last", State: app.TaskStateRunning, CreatedAt: 300},
		{ID: "middle", Agent: "started-second", State: app.TaskStateRunning, CreatedAt: 200},
		{ID: "oldest", Agent: "started-first", State: app.TaskStateRunning, CreatedAt: 100},
	}}
	lines := w.taskCardLines(32, 10)
	for i, want := range []string{"started-last", "started-second", "started-first"} {
		if got := stripANSI(lines[i]); !strings.Contains(got, want) {
			t.Errorf("active row %d = %q, want %q — created_at order must survive", i, got, want)
		}
	}
}

// A finished row whose FinishedAt was never recorded — a task that was
// live when the backend was killed, which this database has real examples
// of — must sort LAST among finished rows, never ahead of a real
// completion.
func TestTaskCardSortsUnrecordedFinishTimesLast(t *testing.T) {
	w := &Workspace{theme: testTheme(), tasks: []app.TaskRow{
		{ID: "zombie", Agent: "never-recorded", State: app.TaskStateCompleted, CreatedAt: 900, FinishedAt: 0},
		{ID: "real", Agent: "real-completion", State: app.TaskStateCompleted, CreatedAt: 100, FinishedAt: 500},
	}}
	lines := w.taskCardLines(32, 10)
	if got := stripANSI(lines[0]); !strings.Contains(got, "real-completion") {
		t.Errorf("first row = %q, want the row with a real finish time ahead of the unrecorded one", got)
	}
}
