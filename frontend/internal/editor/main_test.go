package editor

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// TestMain pins the colour profile for the whole package.
//
// The same fix app\main_test.go, chat\main_test.go and theme\main_test.go
// all make, for the same reason: lipgloss picks its profile from whether
// its output is a terminal, `go test` is not one, and NO_COLOR may already
// be set in a developer's shell. Either one makes every style render
// identically, which silently turns this package's selection and
// highlighting assertions into no-ops that pass whatever the code does.
//
// No profile redirect is needed here, unlike chat's: this package writes
// nothing. config\state.go stays the only file the frontend writes, and
// the Editor does not touch it this session.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// sameStyle compares two styles by what they RENDER.
//
// lipgloss.Style is not comparable (it holds a func), so the escape
// sequences it produces are the only available identity -- and they are
// the right one anyway: two styles that render the same bytes are the same
// style as far as the terminal and the user are concerned.
func sameStyle(a, b lipgloss.Style) bool {
	return a.Render("x") == b.Render("x")
}

// layoutFor is Compute at a given size with the Editor's own cards, used
// by the benchmarks so they lay out exactly as the real frame does.
func layoutFor(ws *Workspace, w, h int) layout.Layout {
	return layout.Compute(w, h, layout.State{
		Active:        layout.WorkspaceEditor,
		RightPaneOpen: true,
		HeaderAllowed: true,
		TerminalMode:  layout.TermSmall,
		RightCards:    ws.Cards(),
	})
}
