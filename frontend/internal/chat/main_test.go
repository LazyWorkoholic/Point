package chat

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// testProviderDefault opts a directly-constructed app.Root out of F10's
// first-run chooser — none of this package's tests are about it, and an
// app.Options left at its zero value (config.State{}, Provider nil) makes
// needsProviderSetup true, which auto-opens Settings on the first
// tea.WindowSizeMsg and collides with whatever overlay the test itself
// expected to see there instead.
var testProviderDefault = config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}}

// Same fix as app\main_test.go and theme\main_test.go: lipgloss picks its
// colour profile from whether its output is a terminal, `go test` is not
// one, and NO_COLOR may already be set in a developer's shell — either one
// silently turns every colour assertion in this package into a no-op.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
