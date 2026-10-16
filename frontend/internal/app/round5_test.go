package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ---------------------------------------------------------------------------
// BUG A — the dirty notice has to re-arm when the idle clock resets
// ---------------------------------------------------------------------------

// idleTwiceRoot builds a Root with a dirty workspace and an active one,
// both loaded, on a controllable clock.
func idleTwiceRoot(t *testing.T) (*Root, *capturingWorkspace, func(time.Duration)) {
	t.Helper()
	dirty := newCapturingWorkspace(WorkspaceEditor)
	dirty.dirty = 2
	other := newCapturingWorkspace(WorkspaceChat)

	clock := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	r, err := New(Options{
		Theme:  theme.New("turquoise", theme.BackgroundTransparent),
		State:  config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Config: config.Config{Workspace: map[string]any{"idle_unload_minutes": int64(1)}},
		Workspaces: []WorkspaceDef{
			{ID: WorkspaceEditor, Name: "Editor", New: func() Workspace { return dirty }},
			{ID: WorkspaceChat, Name: "Chat", New: func() Workspace { return other }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Now = func() time.Time { return clock }
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if _, ok := r.overlay.(*Chooser); ok {
		r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	for _, id := range []WorkspaceID{WorkspaceEditor, WorkspaceChat} {
		if cmd := r.switchWorkspace(id); cmd != nil {
			cmd()
		}
	}
	return r, dirty, func(d time.Duration) { clock = clock.Add(d) }
}

// noticeFires runs one sweep and says whether the dirty notice appeared.
func noticeFires(r *Root) bool {
	r.status = ""
	r.walkIdle(r.now())
	return strings.Contains(r.status, "unsaved changes")
}

// The report: background a dirty Editor, see the notice, switch BACK to it,
// background it again, wait the same period. The notice must fire again.
//
// idle.dirtyNoticed was cleared in exactly one place -- walkIdle's
// not-dirty branch -- so for a workspace that stayed dirty the latch was
// permanent for the whole of its loaded life. It was scoped per-dirty-run
// when it should be scoped per-idle-period.
func TestTheDirtyNoticeFiresAgainAfterReactivation(t *testing.T) {
	r, _, advance := idleTwiceRoot(t)

	// First idle period: the Editor is backgrounded, Chat is active.
	advance(90 * time.Second)
	if !noticeFires(r) {
		t.Fatal("the notice did not fire the first time")
	}
	// ...and only once within that period.
	if noticeFires(r) {
		t.Fatal("the notice repeated inside one idle period")
	}

	// Reactivate the Editor, which resets its idle accounting...
	if cmd := r.switchWorkspace(WorkspaceEditor); cmd != nil {
		cmd()
	}
	if r.idle.dirtyNoticed[WorkspaceEditor] {
		t.Error("reactivating left the dirty latch set")
	}

	// ...then background it and idle it again.
	if cmd := r.switchWorkspace(WorkspaceChat); cmd != nil {
		cmd()
	}
	advance(90 * time.Second)
	if !noticeFires(r) {
		t.Error("the notice did not fire the second time")
	}
}

// Three idle periods, to show it is not a one-shot re-arm either.
func TestTheDirtyNoticeFiresOncePerIdlePeriod(t *testing.T) {
	r, _, advance := idleTwiceRoot(t)

	for round := 1; round <= 3; round++ {
		if cmd := r.switchWorkspace(WorkspaceEditor); cmd != nil {
			cmd()
		}
		if cmd := r.switchWorkspace(WorkspaceChat); cmd != nil {
			cmd()
		}
		advance(90 * time.Second)

		if !noticeFires(r) {
			t.Errorf("round %d: the notice did not fire", round)
		}
		if noticeFires(r) {
			t.Errorf("round %d: the notice repeated inside one period", round)
		}
	}
}

// A keypress in the ACTIVE workspace resets that workspace's accounting --
// including the dirty latch, since touchActive goes through the same
// resetIdle. It must NOT reset a backgrounded workspace's (round 3).
func TestAKeyResetsOnlyTheActiveWorkspacesDirtyLatch(t *testing.T) {
	r, _, advance := idleTwiceRoot(t)

	advance(90 * time.Second)
	if !noticeFires(r) {
		t.Fatal("the notice did not fire")
	}
	if !r.idle.dirtyNoticed[WorkspaceEditor] {
		t.Fatal("the latch was not set")
	}

	// Chat is active; typing there must not re-arm the Editor's latch,
	// which is round 3's bug and must stay fixed.
	for i := 0; i < 20; i++ {
		r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	}
	if !r.idle.dirtyNoticed[WorkspaceEditor] {
		t.Error("typing in another workspace re-armed the dirty latch")
	}
	if noticeFires(r) {
		t.Error("the notice repeated after typing elsewhere")
	}
}

// The latch does not survive an unload either, so a workspace reloaded
// later reports itself afresh.
func TestTheDirtyLatchDoesNotSurviveAnUnload(t *testing.T) {
	r, dirty, advance := idleTwiceRoot(t)

	advance(90 * time.Second)
	if !noticeFires(r) {
		t.Fatal("the notice did not fire")
	}

	// Clean it so the sweep will actually unload it, then unload.
	dirty.dirty = 0
	advance(90 * time.Second)
	r.walkIdle(r.now())
	if _, loaded := r.workspaces[WorkspaceEditor]; loaded {
		t.Fatal("a clean idle workspace was not unloaded")
	}
	if r.idle.dirtyNoticed[WorkspaceEditor] {
		t.Error("the dirty latch survived the unload")
	}
}

// resetIdle is the one function that knows what "this workspace's idle
// accounting starts over" means. All three latches/clocks go together --
// the bug was a second latch added to one of three sites.
func TestResetIdleClearsEveryPieceOfIdleAccounting(t *testing.T) {
	r, _, _ := idleTwiceRoot(t)
	r.lastActive[WorkspaceEditor] = r.now()
	r.idle.warned[WorkspaceEditor] = true
	r.idle.dirtyNoticed[WorkspaceEditor] = true

	r.resetIdle(WorkspaceEditor)

	if _, ok := r.lastActive[WorkspaceEditor]; ok {
		t.Error("lastActive survived resetIdle")
	}
	if r.idle.warned[WorkspaceEditor] {
		t.Error("warned survived resetIdle")
	}
	if r.idle.dirtyNoticed[WorkspaceEditor] {
		t.Error("dirtyNoticed survived resetIdle")
	}
}

// ---------------------------------------------------------------------------
// BUG B — every status message expires, not only the ones that remember to
// ---------------------------------------------------------------------------

// runExpiry executes what Update handed back, the way the Bubble Tea loop
// does, and reports whether an expiry for `text` actually arrived.
//
// It RUNS the commands rather than synthesising the message: a test that
// delivered statusExpireMsg by hand would pass whether or not anything
// scheduled one, which is exactly how the first version of this file's
// paste test passed against the unfixed code. The fixtures compress
// Root.StatusLinger to a millisecond (see newRoot), so running the real
// tea.Tick is free.
func runExpiry(t *testing.T, r *Root, cmd tea.Cmd, text string) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	// Each command runs with a deadline. A batch carries unrelated ticks
	// -- the idle walk's is a whole second -- and this only cares whether
	// a statusExpireMsg for `text` turns up promptly, which with
	// StatusLinger compressed it does. Anything slower is abandoned
	// rather than waited for.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(250 * time.Millisecond):
		return false
	}
	switch m := msg.(type) {
	case tea.BatchMsg:
		found := false
		for _, c := range m {
			if runExpiry(t, r, c, text) {
				found = true
			}
		}
		return found
	case statusExpireMsg:
		if m.text != text {
			return false
		}
		r.Update(m)
		return true
	case nil:
		return false
	default:
		return false
	}
}

// The report: paste ~300 lines, the paste counter appears, and it never
// fades -- masking the Editor's file segments, with closing and reopening
// the file the only way out.
//
// recordPaste set r.status by bare assignment and scheduled nothing, so no
// statusExpireMsg was ever produced for it.
func TestThePasteStatusMessageExpires(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"big.py", "1:1", "python", "LF", "4sp"}
	r := rootWith(t, ws)

	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, "a line of pasted code")
	}
	_, cmd := r.Update(PasteMsg{Text: strings.Join(lines, "\n"), Lines: 300})

	if r.status == "" {
		t.Fatal("the paste left no status message at all")
	}
	paste := r.status
	if !strings.Contains(stripANSI(size(t, r, 160, 40)), paste) {
		t.Fatalf("the paste message is not on screen: %q", paste)
	}
	// While it is up, the file segments are masked -- which is why it
	// mattered that it never cleared.
	if strings.Contains(stripANSI(size(t, r, 160, 40)), "1:1") {
		t.Error("the segments are shown beside a status message")
	}

	if !runExpiry(t, r, cmd, paste) {
		t.Fatal("the paste scheduled no expiry for its own message")
	}

	if r.status != "" {
		t.Errorf("status is still %q after its expiry fired", r.status)
	}
	frame := stripANSI(size(t, r, 160, 40))
	if strings.Contains(frame, paste) {
		t.Errorf("the paste message is still on screen:\n%s", frame)
	}
	// ...and the segments come back with no file close and reopen.
	for _, seg := range ws.segments {
		if !strings.Contains(frame, seg) {
			t.Errorf("segment %q did not come back after the message expired", seg)
		}
	}
}

// The same guarantee for a bare assignment made from a path with no
// tea.Cmd to hang a tick on -- which is why forty of them are bare.
// Pressing esc in the Editor takes exactly this route: the chord belongs
// to a Chat-only action, and dispatchKey's unavailable branch sets the
// status and returns nil.
func TestAnUnavailableChordsStatusMessageExpires(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceEditor)
	ws.segments = []string{"big.py", "1:1", "python"}
	r := rootWith(t, ws)

	// chat.browse owns esc and is unavailable outside Chat.
	a, ok := r.actions.ByChord("esc")
	if !ok {
		t.Skip("nothing is bound to esc in this build")
	}
	r.active = WorkspaceEditor
	if a.Available(r) {
		t.Skip("esc's action is available here; this test needs the refusal path")
	}

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if r.status == "" {
		t.Fatal("the refusal left no status message")
	}
	if !runExpiry(t, r, cmd, r.status) {
		t.Fatal("the refusal scheduled no expiry for its own message")
	}
	if r.status != "" {
		t.Errorf("status is still %q after its expiry fired", r.status)
	}
}

// Every message that was already expiring still expires exactly once --
// the derived tick must not have broken the five that worked.
func TestAWorkspaceStatusMessageStillExpiresExactlyOnce(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"a.py", "1:1"}
	r := rootWith(t, ws)

	_, cmd := r.Update(WorkspaceStatusMsg{Text: "save is not wired yet"})
	if r.status != "save is not wired yet" {
		t.Fatalf("status = %q", r.status)
	}
	if !runExpiry(t, r, cmd, "save is not wired yet") {
		t.Fatal("no expiry was scheduled")
	}
	if r.status != "" {
		t.Errorf("status = %q after expiry", r.status)
	}

	// A second message after the first expired behaves the same way.
	_, cmd = r.Update(WorkspaceStatusMsg{Text: "re-indexing the project"})
	if r.status != "re-indexing the project" {
		t.Fatalf("status = %q", r.status)
	}
	if !runExpiry(t, r, cmd, "re-indexing the project") {
		t.Fatal("no expiry was scheduled for the second message")
	}
	if r.status != "" {
		t.Errorf("status = %q after the second expiry", r.status)
	}
}

// A message that is REPLACED before its expiry fires does not take the
// replacement down with it -- statusExpireMsg compares by text, and the
// derived tick relies on that.
func TestANewMessageIsNotClearedByAnOlderExpiry(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)

	r.Update(PasteMsg{Text: "one\ntwo", Lines: 2})
	stale := r.status
	r.Update(WorkspaceStatusMsg{Text: "second message"})
	if r.status != "second message" {
		t.Fatalf("status = %q", r.status)
	}

	r.Update(statusExpireMsg{text: stale}) // the PASTE's expiry, now stale
	if r.status != "second message" {
		t.Errorf("status = %q; a stale expiry cleared a newer message", r.status)
	}
}

// A message that does not CHANGE the status arms nothing, so a stream of
// ticks cannot hold a line on screen indefinitely by repeatedly pushing
// its expiry out.
func TestAnUnchangedStatusArmsNoFurtherExpiry(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	r.status = "already up"

	if cmd := r.expireStatusIfChanged("already up"); cmd != nil {
		t.Error("an unchanged status armed an expiry")
	}
	r.status = ""
	if cmd := r.expireStatusIfChanged(""); cmd != nil {
		t.Error("an empty status armed an expiry")
	}
}
