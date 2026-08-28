package chat

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Same fix as app\main_test.go and theme\main_test.go: lipgloss picks its
// colour profile from whether its output is a terminal, `go test` is not
// one, and NO_COLOR may already be set in a developer's shell — either one
// silently turns every colour assertion in this package into a no-op.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
