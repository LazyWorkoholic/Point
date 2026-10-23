package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	// The function keys F15 added as EQUAL bindings for the leader and
	// quit -- see actions.Leader. Without these a test driving
	// actions.Leader through this helper sends the literal rune "f2" and
	// the chord never arms.
	case "f1":
		return tea.KeyMsg{Type: tea.KeyF1}
	case "f2":
		return tea.KeyMsg{Type: tea.KeyF2}
	case "f3":
		return tea.KeyMsg{Type: tea.KeyF3}
	case "ctrl+q":
		return tea.KeyMsg{Type: tea.KeyCtrlQ}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func newIntegrationRoot(t *testing.T) *app.Root {
	t.Helper()
	th := testTheme()
	r, err := app.New(app.Options{Theme: th, State: testProviderDefault})
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspace(app.WorkspaceChat, New(Options{Theme: th}))
	return r
}

func update(t *testing.T, r *app.Root, msg tea.Msg) string {
	t.Helper()
	m, _ := r.Update(msg)
	return m.(*app.Root).View()
}

// statusBarRow returns the status bar line, plain-text — Root.View's last
// two lines are always the status bar then the blank bottom margin (see
// app\render.go's View: "rows = append(rows, indent(r.statusBar(l), ...))"
// followed by one more blank append), so it is the SECOND-to-last line, not
// the last.
func statusBarRow(view string) string {
	rows := strings.Split(view, "\n")
	if len(rows) < 2 {
		return ""
	}
	return stripANSI(rows[len(rows)-2])
}

// This is the reported field bug, reproduced through the REAL Root and a
// REAL chat.Workspace — not a single static layout.Compute() call, which
// passed because the bug is not in the arithmetic distribute()/splitGrowth()
// do; it is in something that differs between "the pane has always been
// open" and "the pane was toggled off and back on" despite both ending at
// the identical layout.State. A test that only calls Compute() once can
// never see that difference. Comparing the WHOLE rendered frame, not just
// one row, is deliberate: any stray extra/missing line anywhere shifts
// everything below it, and a full-string diff catches that unconditionally.
func TestRightPaneStaysAlignedAcrossToggle(t *testing.T) {
	r := newIntegrationRoot(t)

	fresh := update(t, r, tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(statusBarRow(fresh), "task(s)") {
		t.Fatalf("fresh launch: status bar not found where expected, got %q", statusBarRow(fresh))
	}

	// close and reopen: ctrl+w c, ctrl+w c
	update(t, r, key("ctrl+w"))
	update(t, r, key("c"))
	update(t, r, key("ctrl+w"))
	toggled := update(t, r, key("c"))

	if toggled != fresh {
		t.Errorf("frame after toggle off/on differs from a fresh launch at the same size.\nfresh:\n%s\ntoggled:\n%s",
			stripANSI(fresh), stripANSI(toggled))
	}
}

// Same claim, via resize instead of the toggle action: narrow past the
// auto-hide breakpoint, then widen back past it.
func TestRightPaneStaysAlignedAcrossResizeNarrowThenWide(t *testing.T) {
	r := newIntegrationRoot(t)

	fresh := update(t, r, tea.WindowSizeMsg{Width: 120, Height: 30})

	update(t, r, tea.WindowSizeMsg{Width: 80, Height: 30})
	widened := update(t, r, tea.WindowSizeMsg{Width: 120, Height: 30})

	if widened != fresh {
		t.Errorf("frame after narrow-then-wide differs from a fresh launch at the same size.\nfresh:\n%s\nwidened:\n%s",
			stripANSI(fresh), stripANSI(widened))
	}
}

// The narrow-width fallback overlay must retire itself once a resize widens
// the terminal back past the breakpoint where the real pane fits — before
// this fix, nothing ever closed it on a resize-GROW (only
// closeOverlayIfTooSmall existed, and its condition is the opposite one:
// shrinking below the overlay's own MinSize), so widening back left the
// overlay and the real pane on screen at once.
func TestFallbackOverlayClosesOnWidenPastBreakpoint(t *testing.T) {
	r := newIntegrationRoot(t)

	update(t, r, tea.WindowSizeMsg{Width: 80, Height: 30})
	update(t, r, key("ctrl+w"))
	narrow := update(t, r, key("c"))
	if !strings.Contains(narrow, "Right Pane") {
		t.Fatalf("overlay did not open at 80 columns:\n%s", stripANSI(narrow))
	}

	widened := update(t, r, tea.WindowSizeMsg{Width: 120, Height: 30})
	if strings.Contains(widened, "Right Pane") {
		t.Errorf("overlay is still open after widening past the breakpoint:\n%s", stripANSI(widened))
	}

	// It must match a fresh launch at that width exactly — the real pane
	// alone, nothing left over from the overlay.
	freshWide := update(t, newIntegrationRoot(t), tea.WindowSizeMsg{Width: 120, Height: 30})
	if widened != freshWide {
		t.Errorf("post-overlay frame differs from a frame that never had an overlay open.\nwidened:\n%s\nfresh:\n%s",
			stripANSI(widened), stripANSI(freshWide))
	}
}
