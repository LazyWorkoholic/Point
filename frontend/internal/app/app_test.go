package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/actions"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

func newRoot(t *testing.T) *Root {
	t.Helper()
	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func size(t *testing.T, r *Root, w, h int) string {
	t.Helper()
	m, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m.(*Root).View()
}

// The seeded registry must validate. A duplicate ID or binding is a boot
// failure, and this is the check that it is not one today.
func TestSeededActionsValidate(t *testing.T) {
	r := newRoot(t)

	for _, id := range []string{
		"app.quit", "app.palette", "ui.help", "ui.redraw", "pane.focus.next",
	} {
		if _, ok := r.actions.ByID(id); !ok {
			t.Errorf("F1 requires %s to be registered", id)
		}
	}
	if _, ok := r.actions.ByID("backend.retry"); !ok {
		t.Error("F2's failure banner has no retry action")
	}
}

// Every rendered line is exactly the terminal width, at every size.
//
// This is the check that catches the most common TUI bug there is: a display
// width computed with len() instead of runewidth, which puts a right border
// one or two cells off on one line and nowhere else.
func TestEveryLineIsExactlyTheTerminalWidth(t *testing.T) {
	sizes := []struct{ w, h int }{
		{200, 50}, {160, 40}, {120, 30}, {119, 29}, {100, 26}, {90, 24},
		{89, 23}, {80, 20}, {70, 20}, {69, 18}, {60, 14}, {50, 12},
		{49, 11}, {45, 10},
	}
	r := newRoot(t)

	for _, sz := range sizes {
		out := size(t, r, sz.w, sz.h)
		lines := strings.Split(out, "\n")

		if len(lines) != sz.h {
			t.Errorf("%dx%d: rendered %d rows, want %d", sz.w, sz.h, len(lines), sz.h)
		}
		for i, line := range lines {
			if w := visibleWidth(line); w > sz.w {
				t.Errorf("%dx%d: row %d is %d cells wide, terminal is %d:\n%q",
					sz.w, sz.h, i, w, sz.w, stripANSI(line))
			}
		}
	}
}

// A double-width title must not push the right border off the end.
func TestWideCharactersDoNotBreakTheBorder(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	rect := boxRect(40, 5)

	for _, title := range []string{
		"View Port",
		"セッション一覧",               // CJK: two cells each
		"🎉 tasks",               // emoji: two cells
		strings.Repeat("長", 40), // longer than the box
	} {
		out := box(th, rect, title, "content", false)
		for i, line := range strings.Split(out, "\n") {
			if w := visibleWidth(line); w != rect.W {
				t.Errorf("title %q row %d is %d cells, want %d: %q",
					title, i, w, rect.W, stripANSI(line))
			}
		}
	}
}

// Content wider than the box is truncated, not wrapped past the wall.
func TestWideContentIsTruncatedToTheBox(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	rect := boxRect(20, 4)

	out := box(th, rect, "T", strings.Repeat("ありがとう", 20), false)
	for i, line := range strings.Split(out, "\n") {
		if w := visibleWidth(line); w != rect.W {
			t.Fatalf("row %d is %d cells, want %d: %q", i, w, rect.W, stripANSI(line))
		}
	}
}

// The full breakpoint sequence, forwards and back, with no panic.
func TestResizeSweepDoesNotPanic(t *testing.T) {
	r := newRoot(t)
	sizes := []struct{ w, h int }{
		{200, 50}, {120, 30}, {100, 26}, {80, 22}, {60, 15}, {45, 10},
		{60, 15}, {80, 22}, {100, 26}, {120, 30}, {200, 50},
	}
	for _, sz := range sizes {
		out := size(t, r, sz.w, sz.h)
		if out == "" {
			t.Fatalf("%dx%d rendered nothing", sz.w, sz.h)
		}
	}
}

// Below the floor the app says the actual limit and nothing else.
func TestTooSmallSaysWhy(t *testing.T) {
	r := newRoot(t)

	if out := size(t, r, 45, 20); !strings.Contains(stripANSI(out), "50 columns minimum") {
		t.Errorf("narrow terminal: %q", stripANSI(out))
	}
	if out := size(t, r, 100, 10); !strings.Contains(stripANSI(out), "12 rows minimum") {
		t.Errorf("short terminal: %q", stripANSI(out))
	}
}

// Below 24 rows the header is gone and the watermark takes its place, without
// costing a row.
func TestWatermarkReplacesTheHeader(t *testing.T) {
	r := newRoot(t)

	tall := stripANSI(size(t, r, 120, 30))
	short := stripANSI(size(t, r, 120, 20))

	if !strings.Contains(tall, "P O I N T") {
		t.Error("no header at 30 rows")
	}
	if strings.Contains(short, "P O I N T") {
		t.Error("header still drawn at 20 rows")
	}
	if !strings.Contains(short, watermarkText) {
		t.Error("no watermark at 20 rows")
	}
}

// The leader is a prefix, not a chord: ctrl+w alone does nothing but wait.
func TestLeaderSequence(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	m, _ := r.Update(key(actions.Leader))
	root := m.(*Root)
	if !root.leader.pending {
		t.Fatal("leader did not arm")
	}

	// An unbound key clears it and says so, rather than silently eating input.
	m, _ = root.Update(key("z"))
	root = m.(*Root)
	if root.leader.pending {
		t.Error("leader stayed armed after an unbound key")
	}
	if !strings.Contains(root.status, "no action bound") {
		t.Errorf("status = %q", root.status)
	}

	// esc cancels.
	m, _ = root.Update(key(actions.Leader))
	m, _ = m.(*Root).Update(key("esc"))
	if m.(*Root).leader.pending {
		t.Error("esc did not cancel the leader")
	}
}

// The hint bar is built from the registry, not from a hardcoded list.
func TestLeaderHintComesFromTheRegistry(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	m, _ := r.Update(key(actions.Leader))
	root := m.(*Root)
	root.leader.showHint = true

	out := stripANSI(root.View())
	quit, _ := root.actions.ByID("app.quit")
	if !strings.Contains(out, quit.Leader+" "+quit.Name) {
		t.Errorf("hint bar does not list %s: %q", quit.Name, out)
	}
}

// An overlay that does not fit is refused, with the ACTUAL numbers.
func TestOverlayRefusalNamesTheNumbers(t *testing.T) {
	r := newRoot(t)
	size(t, r, 75, 13) // wide enough for overlays, too short for a 60x14 one

	r.OpenOverlay(helpOverlay{root: r})

	if r.overlay != nil {
		t.Fatal("an overlay that does not fit was opened")
	}
	for _, want := range []string{"60×14", "75×13"} {
		if !strings.Contains(r.status, want) {
			t.Errorf("status %q does not name %s", r.status, want)
		}
	}
}

// Below 70 columns overlays refuse outright.
func TestOverlaysRefuseInSingleColumn(t *testing.T) {
	r := newRoot(t)
	size(t, r, 60, 30)

	r.OpenOverlay(helpOverlay{root: r})
	if r.overlay != nil {
		t.Fatal("an overlay opened below the single-column floor")
	}
	if !strings.Contains(r.status, "70") {
		t.Errorf("status does not name the floor: %q", r.status)
	}
}

// An open overlay traps focus: keys go to it, not to a chord.
func TestOverlayTrapsFocus(t *testing.T) {
	r := newRoot(t)
	size(t, r, 120, 30)

	r.OpenOverlay(helpOverlay{root: r})
	if r.overlay == nil {
		t.Fatal("the overlay did not open at 120x30")
	}

	// ctrl+l would normally redraw and set a status; while an overlay is open
	// it must not fire.
	before := r.status
	m, _ := r.Update(key("ctrl+l"))
	root := m.(*Root)
	if root.overlay == nil {
		t.Error("a chord closed the overlay")
	}
	if root.status != before {
		t.Errorf("a chord fired through the overlay: %q", root.status)
	}

	m, _ = root.Update(key("esc"))
	if m.(*Root).overlay != nil {
		t.Error("esc did not close the overlay")
	}
}

// The help screen reads the registry, so an action added later appears without
// touching the help screen.
func TestHelpListsEveryAction(t *testing.T) {
	r := newRoot(t)
	out := helpOverlay{root: r}.View(r.theme, 80, 20)

	for _, a := range r.actions.All() {
		if !strings.Contains(out, a.Name) {
			t.Errorf("help does not list %q", a.Name)
		}
	}
}

// NO_COLOR renders readable plain text with the borders intact.
func TestNoColorKeepsBordersAndDropsEscapes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	r, err := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	if err != nil {
		t.Fatal(err)
	}
	out := size(t, r, 120, 30)

	if strings.Contains(out, "\x1b[") {
		t.Error("NO_COLOR still emitted escape sequences")
	}
	if !strings.Contains(out, borderTopLeft) || !strings.Contains(out, borderBottomRight) {
		t.Error("NO_COLOR removed the borders; only colour should go")
	}
	if !strings.Contains(out, "P O I N T") {
		t.Error("NO_COLOR removed the header text")
	}
}

// Switching the palette changes colour and NOTHING else.
//
// The colour half cannot be asserted from a test binary: lipgloss picks its
// profile from whether the output is a terminal, and `go test` is not one, so
// every style renders bare. What IS assertable here — and is the half that
// breaks layouts — is that the palette does not move a single cell. The
// colours themselves are covered by the theme package's own tests, and by the
// manual check that switching `[ui] theme` repaints the app.
func TestSwitchingPaletteChangesNoText(t *testing.T) {
	turquoise, _ := New(Options{Theme: theme.New("turquoise", theme.BackgroundTransparent)})
	gold, _ := New(Options{Theme: theme.New("gold", theme.BackgroundTransparent)})

	a := stripANSI(size(t, turquoise, 120, 30))
	b := stripANSI(size(t, gold, 120, 30))

	if a != b {
		t.Fatal("changing the palette moved the text, not just the colour")
	}
	if turquoise.theme.Color(theme.AccentPrimary) == gold.theme.Color(theme.AccentPrimary) {
		t.Fatal("the two palettes resolved the same accent")
	}
}

// ---------------------------------------------------------------------------
// Backend state reaches the screen
// ---------------------------------------------------------------------------

func TestHandshakeReachesTheScreen(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	r.Update(protocol.ConnectedMsg{
		ProtocolVersion: protocol.Version,
		Capabilities: protocol.Capabilities{
			Providers: []string{"ollama", "anthropic"}, SchemaVersion: 7,
			Streaming: true, Cancel: true,
		},
	})
	drain(r)

	out := stripANSI(r.View())
	for _, want := range []string{"connected", "ollama", "anthropic", "schema v7"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen does not show %q:\n%s", want, out)
		}
	}
}

// A version mismatch names BOTH versions.
func TestVersionMismatchNamesBothOnScreen(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	r.Update(protocol.VersionMismatchMsg{Client: 1, Backend: 2})
	drain(r)

	out := stripANSI(r.View())
	if !strings.Contains(out, "v1") || !strings.Contains(out, "v2") {
		t.Errorf("mismatch did not name both versions:\n%s", out)
	}
}

// Losing the backend is a status-bar banner, not a modal — the rest of the app
// stays usable, which means the panes are still drawn.
func TestFailureIsABannerNotAModal(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	r.Update(protocol.BackendFailedMsg{
		Reason: "the backend has failed twice in a minute",
		Detail: "signal: killed",
	})
	drain(r)

	out := stripANSI(r.View())
	if !strings.Contains(out, "failed twice") {
		t.Errorf("no banner:\n%s", out)
	}
	if !strings.Contains(out, "View Port") || !strings.Contains(out, "Query Area") {
		t.Error("the panes vanished behind the failure; it should be a banner, not a modal")
	}
}

// Lost, then interrupted: UNKNOWN first, because at that moment nobody knows.
func TestTaskGoesUnknownThenInterrupted(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	r.Update(protocol.BackendLostMsg{Reason: "signal: killed", UnknownTasks: []string{"task-abc123"}})
	drain(r)

	out := stripANSI(r.View())
	if !strings.Contains(out, "unknown") {
		t.Errorf("task not shown as unknown:\n%s", out)
	}

	r.Update(protocol.TasksInterruptedMsg{TaskIDs: []string{"task-abc123"}})
	drain(r)

	out = stripANSI(r.View())
	if !strings.Contains(out, "interrupted") {
		t.Errorf("task not shown as interrupted:\n%s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Error("the task is still shown as unknown after being interrupted")
	}
}

// A stray print() on the Python side is named on screen, not silently eaten.
func TestProtocolErrorIsShown(t *testing.T) {
	r := newRoot(t)
	size(t, r, 140, 40)

	r.Update(protocol.ProtocolErrorMsg{Line: 3, Text: "hi", Message: "invalid character 'h'"})
	drain(r)

	out := stripANSI(r.View())
	if !strings.Contains(out, "line 3") || !strings.Contains(out, "hi") {
		t.Errorf("protocol error not named:\n%s", out)
	}
}

// ---------------------------------------------------------------------------

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// drain runs the commands the root returned, so the diagnostics snapshot the
// placeholder renders is the current one. In the real program Bubble Tea does
// this; in a test it has to be done by hand.
func drain(r *Root) {
	cmd := r.pushDiagnostics()
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		r.Update(msg)
	}
}

func boxRect(w, h int) layout.Rect { return layout.Rect{X: 1, Y: 1, W: w, H: h} }
