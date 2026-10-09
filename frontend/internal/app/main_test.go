package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// TestMain does two things for the whole package: pins the colour profile,
// and redirects Point's profile directories at a scratch location.
//
// # Colour
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

	cleanup, err := redirectProfileToScratch()
	if err != nil {
		// Refusing to run is the only safe answer. A test run that
		// could not redirect the profile would write to the real one.
		fmt.Fprintln(os.Stderr, "app tests:", err)
		os.Exit(1)
	}

	// os.Exit skips deferred calls, so the scratch directory is removed
	// explicitly between the run and the exit.
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// redirectProfileToScratch points Point's config and state directories at a
// throwaway directory for the whole package's test run.
//
// # Why this is here even though nothing in this package reaches the real file
//
// The bug this guard exists for was in the CHAT package — `go test` there was
// writing the real %LOCALAPPDATA%\Point\state.json, found during F12's
// verification and confirmed against a clean tree. See chat\main_test.go's
// own note for the route it took.
//
// This package's equivalent writer, Root.SaveWorkspaceStateFn, also defaults
// to the real function, and saveWorkspaceStateIfDue currently declines when
// no workspaces are registered — which every test here satisfies, and which
// is why nothing in this package writes the file today.
//
// "Nothing today triggers it" is not a guarantee, and in chat it was the
// exact shape of the assumption that failed: safety resting on every fixture
// remembering to substitute a recorder. A test added later that registers a
// workspace and does not override SaveWorkspaceStateFn reopens the whole
// thing, silently, in a package where the recorder is five lines away in a
// file the author may never open. The guard costs one directory per run and
// removes the possibility rather than arguing about its likelihood.
//
// Not fixed by changing the production default, in either package: the
// running app absolutely should write the user's state.json, and making the
// shipped code safer for tests would be making the product worse to make the
// suite better.
// realProfileDir is where Point's state WOULD live on this machine, captured
// before the redirect below replaces it. Read only by
// TestTheProfileIsRedirectedAwayFromTheRealOne, which is what makes that
// test's comparison the real thing rather than a reconstruction of it.
var realProfileDir string

func redirectProfileToScratch() (func(), error) {
	realProfileDir = config.StateDir()

	dir, err := os.MkdirTemp("", "point-test-profile-")
	if err != nil {
		return nil, fmt.Errorf("creating a scratch profile: %w", err)
	}

	// Both variables, because config.go reads APPDATA for the config
	// directory and LOCALAPPDATA for the state directory — the same two the
	// Python side reads (backend\appconfig.py), by design, so one override
	// moves the whole app. The XDG pair is set too: config.go branches on
	// runtime.GOOS, and a guard that silently stops guarding when the suite
	// is run on Linux is worse than no guard, because it still reports
	// green.
	for _, k := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		if err := os.Setenv(k, dir); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("setting %s: %w", k, err)
		}
	}

	// Verified rather than assumed. If ConfigDir/StateDir ever stop
	// resolving from these variables, this guard would quietly do nothing
	// while continuing to pass — B18's "green while verifying almost
	// nothing", pointed at the one thing that must never be wrong.
	for _, got := range []string{config.ConfigDir(), config.StateDir(), config.StatePath()} {
		if !strings.HasPrefix(filepath.Clean(got), filepath.Clean(dir)) {
			os.RemoveAll(dir)
			return nil, fmt.Errorf(
				"the scratch redirect did not take: a Point profile path resolves to %q, outside %q",
				got, dir)
		}
	}

	return func() { os.RemoveAll(dir) }, nil
}

// The guard in TestMain is load-bearing, so its absence has to FAIL rather
// than quietly let a write through.
//
// Remove or break redirectProfileToScratch and this test fails immediately,
// which is the property an mtime check performed once by hand cannot give:
// the original bug was found only because someone happened to look at the
// real file's timestamp. realProfileDir is captured before the redirect, so
// this compares against the actual directory the running app would use on
// this machine rather than against a reconstruction of it.
func TestTheProfileIsRedirectedAwayFromTheRealOne(t *testing.T) {
	if realProfileDir == "" {
		t.Skip("no real profile directory on this platform")
	}
	for _, p := range []string{config.ConfigDir(), config.StateDir(), config.StatePath(), config.LogDir()} {
		clean := filepath.Clean(p)
		if strings.HasPrefix(clean, filepath.Clean(realProfileDir)) {
			t.Fatalf("%q is inside the REAL profile %q -- this package's tests can write the user's own files",
				clean, realProfileDir)
		}
		if !strings.HasPrefix(clean, filepath.Clean(os.TempDir())) {
			t.Errorf("%q is not under the temp directory; where is it?", clean)
		}
	}
}
