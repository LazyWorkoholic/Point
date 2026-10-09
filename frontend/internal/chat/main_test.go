package chat

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

// testProviderDefault opts a directly-constructed app.Root out of F10's
// first-run chooser — none of this package's tests are about it, and an
// app.Options left at its zero value (config.State{}, Provider nil) makes
// needsProviderSetup true, which auto-opens Settings on the first
// tea.WindowSizeMsg and collides with whatever overlay the test itself
// expected to see there instead.
var testProviderDefault = config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}}

// TestMain does two things for the whole package: pins the colour profile,
// and redirects Point's profile directories at a scratch location.
//
// Same colour fix as app\main_test.go and theme\main_test.go: lipgloss picks
// its colour profile from whether its output is a terminal, `go test` is not
// one, and NO_COLOR may already be set in a developer's shell — either one
// silently turns every colour assertion in this package into a no-op.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	lipgloss.SetColorProfile(termenv.TrueColor)

	cleanup, err := redirectProfileToScratch()
	if err != nil {
		// Refusing to run is the only safe answer. A test run that
		// could not redirect the profile would write to the real one.
		fmt.Fprintln(os.Stderr, "chat tests:", err)
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
// # Why this is mandatory and not hygiene
//
// `go test ./frontend/internal/chat/` was WRITING THE REAL
// %LOCALAPPDATA%\Point\state.json. It was found during F12's verification,
// confirmed against a clean tree with no F12 changes in it at all, and it
// had already wiped a field out of a live profile by the time it was
// noticed. CLAUDE.md is explicit that nothing automated may touch
// %APPDATA%\Point or %LOCALAPPDATA%\Point outside a scratch profile, so this
// was a standing violation in the test suite, not a near miss.
//
// The route: chat.New defaults w.saveStateFn to the real config.SaveState
// (draft.go's persistDraft explains why the seam exists), and persistDraft
// only declines when w.sessionID is empty. Any test that sets a session id —
// via setSession, onSessionCreated, or a direct assignment — and then sends
// (onSend calls clearDraft) or types (draftSaveMsg → saveDraftIfDue) writes
// the user's own file. Five test sites substituted saveStateFn; every other
// one did not, and did not need to for its own assertions to pass, which is
// exactly why nothing caught it.
//
// Fixed HERE rather than by changing that default: the production path is
// correct — the running app absolutely should write the user's state.json —
// and a fix that made the shipped code safer for tests would be making the
// product worse to make the suite better. Fixed once per PACKAGE rather than
// per test for the same reason the colour profile above is: a rule every new
// test has to remember is a rule that will be forgotten, and the forgetting
// is silent.
//
// The same guard is in app\main_test.go; see its own note on why it is there
// even though nothing in that package currently reaches the real file.
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
