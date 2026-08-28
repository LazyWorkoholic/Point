package app

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Force truecolor for the whole package's tests.
//
// lipgloss picks its colour profile from whether its output is a terminal, and
// `go test` is not one — so every style renders bare and NOTHING about colour
// is assertable. That is precisely how the Help overlay shipped rendering in
// the terminal's default foreground: the bug was invisible to the test suite
// because the test suite had no colour to compare.
//
// Pinning the profile here is what lets the theme regressions be caught by a
// test rather than by looking at a screenshot.
// NO_COLOR is cleared for the same reason: it is a REAL setting that a
// developer may have in their profile — this repo's own F1 verification tells
// you to set it — and theme.New honours it by dropping every colour. A test
// suite that inherits it silently stops asserting anything about colour and
// reports green. Tests that want NO_COLOR set it themselves with t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
