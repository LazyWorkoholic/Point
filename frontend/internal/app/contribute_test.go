package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F13's three additions to this package, tested here rather than in the
// editor package: what is being checked is ROOT's behaviour -- the key
// dispatch order, the status bar's left half, and the idle sweep -- against
// a workspace that implements the interfaces, not the editor itself.

// capturingWorkspace is a Placeholder that claims keys, contributes status
// segments and can report itself dirty. A stand-in, deliberately: a test
// that imported the real editor would make this package depend on a
// workspace, which is the dependency direction the Workspace contract
// exists to forbid.
type capturingWorkspace struct {
	*Placeholder
	claims   []string
	segments []string
	dirty    int
	gotKeys  []string
}

func newCapturingWorkspace(id WorkspaceID) *capturingWorkspace {
	return &capturingWorkspace{
		Placeholder: NewPlaceholder(id, theme.New("turquoise", theme.BackgroundTransparent)),
	}
}

func (c *capturingWorkspace) CapturesKey(key string) bool {
	for _, k := range c.claims {
		if k == key {
			return true
		}
	}
	return false
}

func (c *capturingWorkspace) StatusSegments() []string { return c.segments }
func (c *capturingWorkspace) DirtyBufferCount() int    { return c.dirty }

func (c *capturingWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		c.gotKeys = append(c.gotKeys, k.String())
	}
	_, cmd := c.Placeholder.Update(msg)
	return c, cmd
}

// rootWith builds a Root whose single registered workspace is ws, already
// loaded and active.
func rootWith(t *testing.T, ws *capturingWorkspace) *Root {
	t.Helper()
	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{{
			ID: WorkspaceChat, Name: "Stand-in",
			New: func() Workspace { return ws },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.StatusLinger = time.Millisecond // see newRoot
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	// The startup chooser (UI-12) opens on the first WindowSizeMsg with
	// nothing loaded, and an open overlay traps every key -- which is
	// exactly what these tests would otherwise be measuring. Dismissed the
	// same way lifecycleFixture.start does.
	if _, ok := r.overlay.(*Chooser); ok {
		r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	if cmd := r.switchWorkspace(WorkspaceChat); cmd != nil {
		cmd()
	}
	return r
}

// ---------------------------------------------------------------------------
// KeyCapturer
// ---------------------------------------------------------------------------

// `tab` has been pane.focus.next's chord since F4. A workspace that claims
// it receives it instead, and the action does not run.
func TestACapturedChordReachesTheWorkspaceInsteadOfTheAction(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.claims = []string{"tab", "shift+tab"}
	r := rootWith(t, ws)

	if _, ok := r.actions.ByChord("tab"); !ok {
		t.Fatal("precondition: nothing is bound to tab")
	}

	r.Update(tea.KeyMsg{Type: tea.KeyTab})
	r.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	if strings.Join(ws.gotKeys, ",") != "tab,shift+tab" {
		t.Errorf("the workspace received %v, want tab and shift+tab", ws.gotKeys)
	}
}

// An UNCLAIMED chord still runs its action. The claim is per-key, not a
// blanket takeover of the key path.
func TestAnUnclaimedChordStillRunsItsAction(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.claims = []string{"tab"}
	r := rootWith(t, ws)

	// ctrl+l is ui.redraw, which sets a status message.
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	for _, k := range ws.gotKeys {
		if k == "ctrl+l" {
			t.Error("ctrl+l reached the workspace; its action should have consumed it")
		}
	}
}

// A workspace that implements nothing is unaffected -- the type assertion
// fails and `tab` focuses the next pane exactly as it did before F13.
func TestAWorkspaceThatClaimsNothingKeepsTheOldBehaviour(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.claims = nil
	r := rootWith(t, ws)

	r.Update(tea.KeyMsg{Type: tea.KeyTab})
	for _, k := range ws.gotKeys {
		if k == "tab" {
			t.Error("tab reached a workspace that did not claim it")
		}
	}
}

// The leader is checked BEFORE the claim, so leader → anything stays
// reachable from a workspace that has claimed keys. Without this, an editor
// could make the switcher and the palette unreachable.
func TestTheLeaderIsNotCapturable(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.claims = []string{"ctrl+w", "tab", "q"}
	r := rootWith(t, ws)

	r.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if !r.leader.pending {
		t.Fatal("a claimed leader key did not arm the leader")
	}
	for _, k := range ws.gotKeys {
		if k == "ctrl+w" {
			t.Error("the leader key reached the workspace")
		}
	}

	// And the sequence after it still resolves to its action rather than
	// being claimed.
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if r.leader.pending {
		t.Error("the leader sequence was not consumed")
	}
}

// An open overlay still traps focus: it is checked before the claim.
func TestAnOverlayOutranksAClaim(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.claims = []string{"tab"}
	r := rootWith(t, ws)
	r.OpenOverlay(NewConfirm("T", "body", "Yes", "No", nil))

	r.Update(tea.KeyMsg{Type: tea.KeyTab})
	for _, k := range ws.gotKeys {
		if k == "tab" {
			t.Error("a claimed key reached the workspace through an open overlay")
		}
	}
}

// ---------------------------------------------------------------------------
// StatusContributor
// ---------------------------------------------------------------------------

func TestStatusSegmentsReplaceTheBindingsHint(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"task.py", "12:4", "python", "CRLF"}
	r := rootWith(t, ws)

	frame := size(t, r, 160, 40)
	plain := stripANSI(frame)
	for _, want := range ws.segments {
		if !strings.Contains(plain, want) {
			t.Errorf("the frame does not contain segment %q", want)
		}
	}
	// Root's own hint is gone: the contributor owns the whole left half.
	if strings.Contains(plain, "palette") {
		t.Error("the bindings hint is still shown beside the segments")
	}
}

// A transient status message outranks the segments -- it is the more
// urgent thing and it expires.
func TestATransientStatusOutranksTheSegments(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"task.py", "12:4"}
	r := rootWith(t, ws)
	r.status = "save is not wired yet"

	plain := stripANSI(size(t, r, 160, 40))
	if !strings.Contains(plain, "save is not wired yet") {
		t.Error("the status message was not shown")
	}
	if strings.Contains(plain, "12:4") {
		t.Error("the segments were shown alongside a status message")
	}
}

// A workspace contributing NO segments falls back to the bindings hint,
// which is what keeps the Chat line unchanged.
func TestNoSegmentsFallsBackToTheBindingsHint(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = nil
	r := rootWith(t, ws)

	plain := stripANSI(size(t, r, 160, 40))
	if !strings.Contains(plain, "palette") {
		t.Error("the bindings hint is missing")
	}
}

// Narrow terminals drop WHOLE segments and say how many, rather than
// cutting one in half -- fitHints' existing contract, now carrying
// somebody else's parts.
func TestSegmentsDropWholeAtNarrowWidths(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"a-long-file-name.py", "120:44", "python", "CRLF", "read-only"}
	r := rootWith(t, ws)

	plain := stripANSI(size(t, r, 60, 20))
	// Either a segment is present whole or it is absent; no fragment.
	if strings.Contains(plain, "read-onl\n") || strings.Contains(plain, "read-on ") {
		t.Errorf("a segment was cut: %q", plain)
	}
	if !strings.Contains(plain, "+") {
		t.Error("dropped segments were not counted")
	}
}

// No line exceeds the terminal with segments in place -- the same bound
// TestEveryLineIsExactlyTheTerminalWidth asserts for the rest of the
// frame, because a status bar is the easiest place to be one cell off.
func TestFrameWidthIsBoundedWithSegments(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"pkg/file.py", "1:1", "python", "LF", "• modified", "ctrl+o tree"}
	r := rootWith(t, ws)

	for _, sz := range []struct{ w, h int }{{200, 50}, {120, 30}, {90, 24}, {70, 20}, {50, 12}} {
		for i, line := range strings.Split(size(t, r, sz.w, sz.h), "\n") {
			if got := visibleWidth(line); got > sz.w {
				t.Errorf("%dx%d row %d: %d cells, terminal is %d\n%q",
					sz.w, sz.h, i, got, sz.w, stripANSI(line))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The idle sweep and dirty buffers
// ---------------------------------------------------------------------------

// A dirty workspace is SKIPPED by the idle sweep, not unloaded.
//
// This is the data-loss path F13 closes. requestUnload (leader → u) has
// confirmed since F12; this sweep never did, because until F13 nothing in
// the app could be dirty. An editor buffer is the only unsaved thing in
// Point with no other copy, and this sweep unloads silently by design.
func TestTheIdleSweepSkipsADirtyWorkspace(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.dirty = 2
	r := rootWith(t, ws)

	// Leave it, so its idle clock starts, then put that clock well past
	// any timeout.
	r.active = WorkspaceEditor
	r.lastActive[WorkspaceChat] = time.Now().Add(-24 * time.Hour)

	r.walkIdle(time.Now())

	if _, loaded := r.workspaces[WorkspaceChat]; !loaded {
		t.Fatal("a dirty workspace was idle-unloaded")
	}
	if r.status == "" || !strings.Contains(r.status, "unsaved") {
		t.Errorf("status = %q, want it to say why the workspace stayed", r.status)
	}
}

// A CLEAN workspace still unloads: the guard is about unsaved work, not
// about disabling the lifecycle F12 exists to prove.
func TestTheIdleSweepStillUnloadsACleanWorkspace(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.dirty = 0
	r := rootWith(t, ws)

	r.active = WorkspaceEditor
	r.lastActive[WorkspaceChat] = time.Now().Add(-24 * time.Hour)

	r.walkIdle(time.Now())

	if _, loaded := r.workspaces[WorkspaceChat]; loaded {
		t.Error("a clean idle workspace was not unloaded")
	}
}

// Once saved, it unloads on a later tick -- the skip is not sticky.
func TestADirtyWorkspaceUnloadsOnceItIsClean(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.dirty = 1
	r := rootWith(t, ws)

	r.active = WorkspaceEditor
	r.lastActive[WorkspaceChat] = time.Now().Add(-24 * time.Hour)
	r.walkIdle(time.Now())
	if _, loaded := r.workspaces[WorkspaceChat]; !loaded {
		t.Fatal("precondition: it was unloaded while dirty")
	}

	ws.dirty = 0
	r.walkIdle(time.Now())
	if _, loaded := r.workspaces[WorkspaceChat]; loaded {
		t.Error("it did not unload after becoming clean")
	}
}

// The Editor's own registered minimum is refused with the ACTUAL numbers,
// which is TEMPLATES.md's capability contract.
func TestAWorkspaceBelowItsOwnMinimumIsRefusedWithNumbers(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{{
			ID: WorkspaceChat, Name: "Editor-sized",
			New: func() Workspace { return ws }, MinSize: Size{W: 70, H: 16},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if cmd := r.switchWorkspace(WorkspaceChat); cmd != nil {
		cmd()
	}

	plain := stripANSI(size(t, r, 60, 20))
	if !strings.Contains(plain, "70×16") || !strings.Contains(plain, "60×20") {
		t.Errorf("refusal does not name both sizes: %q", plain)
	}
}

// Compute gives the Editor workspace a Secondary rect for F14's terminal
// whatever else is going on -- the three rows the Editor draws a named stub
// into (the editor package's own terminalBox).
func TestComputeReservesTheEditorsSecondaryRect(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{70, 16}, {120, 30}, {200, 50}} {
		l := layout.Compute(sz.w, sz.h, layout.State{
			Active:        layout.WorkspaceEditor,
			RightPaneOpen: true,
			HeaderAllowed: true,
			TerminalMode:  layout.TermSmall,
			RightCards:    []layout.CardSlot{{MinHeight: 8}, {MinHeight: 7, Flex: true}},
		})
		if l.Secondary.IsZero() {
			t.Errorf("%dx%d: no Secondary rect", sz.w, sz.h)
		}
		if l.Secondary.H < layout.MinBoxHeight {
			t.Errorf("%dx%d: Secondary is %d rows", sz.w, sz.h, l.Secondary.H)
		}
	}
}

// main.go's real registry must validate: a duplicate leader sequence is a
// boot failure (registerWorkspaces runs before actions.Validate), and
// "Point refuses to start" is the worst way to find out that `e` was
// already taken. The ids, names and leader keys here are main.go's,
// verbatim; the constructors are stubs because this package cannot import
// a workspace.
func TestTheShippedWorkspaceRegistryValidates(t *testing.T) {
	stub := func() Workspace { return NewPlaceholder(WorkspaceChat, theme.New("", "")) }
	_, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{
			{ID: WorkspaceChat, Name: "Chat", New: stub, MinSize: Size{W: 50, H: 12}},
			{ID: WorkspaceEditor, Name: "Editor", LeaderKey: "e", New: stub,
				MinSize: Size{W: 70, H: 16}},
		},
	})
	if err != nil {
		t.Fatalf("the shipped registry does not validate: %v", err)
	}
}

// And `e` is genuinely free -- asserted against the whole shipped action
// set rather than against a list somebody has to keep in step with it.
func TestLeaderEIsNotTakenByAShippedAction(t *testing.T) {
	r := newRoot(t)
	for _, a := range r.actions.All() {
		if a.Leader == "e" {
			t.Fatalf("leader e is already %s", a.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// WorkspaceStatusMsg
// ---------------------------------------------------------------------------

// A workspace's transient line reaches the status bar and then expires.
// Before this existed, the Editor set its own field and nothing rendered
// it with a file open -- four separate "no message at all" reports.
func TestAWorkspaceStatusMessageReachesTheStatusBarAndExpires(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.segments = []string{"a.py", "1:1"}
	r := rootWith(t, ws)

	_, cmd := r.Update(WorkspaceStatusMsg{Text: "save is not wired yet"})
	plain := stripANSI(size(t, r, 160, 40))
	if !strings.Contains(plain, "save is not wired yet") {
		t.Fatalf("the message is not on screen:\n%s", plain)
	}
	// It outranks the workspace's own segments while it is up.
	if strings.Contains(plain, "1:1") {
		t.Error("the segments were shown alongside the message")
	}

	if cmd == nil {
		t.Fatal("no expiry was scheduled")
	}
	r.Update(statusExpireMsg{text: "save is not wired yet"})
	plain = stripANSI(size(t, r, 160, 40))
	if strings.Contains(plain, "save is not wired yet") {
		t.Error("the message did not expire")
	}
	if !strings.Contains(plain, "1:1") {
		t.Error("the segments did not come back")
	}
}

func TestAnEmptyWorkspaceStatusMessageIsIgnored(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	r.status = "something else"
	r.Update(WorkspaceStatusMsg{Text: ""})
	if r.status != "something else" {
		t.Errorf("status = %q; an empty message overwrote it", r.status)
	}
}

// ---------------------------------------------------------------------------
// The dirty notice, and the key that is supposed to rescue a warning
// ---------------------------------------------------------------------------

// Reported as: the "stays loaded — N file(s) have unsaved changes" line
// reappearing on every keystroke typed in another workspace.
//
// The notice used idle.warned as its say-once latch, and keepWarnedAlive
// DELETES every entry in that map on every keypress -- that is the "any
// key to keep" rescue. So the latch was re-armed a thousand times a minute
// and the notice re-fired on the next tick, forever.
func TestTheDirtyNoticeIsSaidOnceAcrossManyKeystrokes(t *testing.T) {
	dirty := newCapturingWorkspace(WorkspaceEditor)
	dirty.dirty = 2
	typing := newCapturingWorkspace(WorkspaceChat)

	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{
			{ID: WorkspaceChat, Name: "Chat", New: func() Workspace { return typing }},
			{ID: WorkspaceEditor, Name: "Editor", New: func() Workspace { return dirty }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if _, ok := r.overlay.(*Chooser); ok {
		r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	// Load both, leave the Editor, and put its clock past any timeout.
	if cmd := r.switchWorkspace(WorkspaceEditor); cmd != nil {
		cmd()
	}
	if cmd := r.switchWorkspace(WorkspaceChat); cmd != nil {
		cmd()
	}
	r.lastActive[WorkspaceEditor] = time.Now().Add(-24 * time.Hour)

	said := 0
	for i := 0; i < 25; i++ {
		r.status = ""
		r.walkIdle(time.Now())
		if strings.Contains(r.status, "unsaved changes") {
			said++
		}
		// A keystroke in Chat, exactly as the report describes.
		r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	}
	if said != 1 {
		t.Errorf("the dirty notice was said %d times across 25 ticks and keystrokes, want 1", said)
	}
	if _, loaded := r.workspaces[WorkspaceEditor]; !loaded {
		t.Error("the dirty workspace was unloaded")
	}
}

// Once it stops being dirty the latch clears, so the notice is available
// again next time -- it is a say-once-per-condition latch, not a
// say-once-ever one.
func TestTheDirtyNoticeIsSaidAgainAfterTheWorkspaceGoesCleanAndDirtyAgain(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	ws.dirty = 1
	r := rootWith(t, ws)
	r.active = WorkspaceEditor
	r.lastActive[WorkspaceChat] = time.Now().Add(-24 * time.Hour)

	r.status = ""
	r.walkIdle(time.Now())
	if !strings.Contains(r.status, "unsaved") {
		t.Fatalf("first notice missing: %q", r.status)
	}

	// Clean: the sweep unloads it and the latch is cleared on the way.
	ws.dirty = 0
	r.walkIdle(time.Now())
	if _, loaded := r.workspaces[WorkspaceChat]; loaded {
		t.Fatal("a clean idle workspace was not unloaded")
	}
	if r.idle.dirtyNoticed[WorkspaceChat] {
		t.Error("the dirty latch survived the workspace going clean")
	}
}

// The grace warning names the switcher and NOT a key, because a key in
// another workspace no longer rescues one -- see lifecycle.go where
// keepWarnedAlive was, and lifecycle_test.go's
// TestAKeyInOneWorkspaceDoesNotRescueAnother for the behaviour.
func TestTheGraceWarningNamesTheSwitcherNotAKey(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	r.cfg = config.Config{Workspace: map[string]any{"idle_unload_minutes": int64(1)}}
	r.active = WorkspaceEditor

	// 45s idle against a 60s timeout: inside the 30s grace window.
	r.lastActive[WorkspaceChat] = time.Now().Add(-45 * time.Second)
	r.status = ""
	r.walkIdle(time.Now())

	if !strings.Contains(r.status, "unloads in") {
		t.Fatalf("no grace warning: %q", r.status)
	}
	if strings.Contains(r.status, "any key") {
		t.Errorf("the warning promises a key that does nothing: %q", r.status)
	}
	// The switcher's real binding, resolved from the registry so the
	// sentence cannot drift from it.
	if !strings.Contains(r.status, r.keepHint()) {
		t.Errorf("status %q does not name %q", r.status, r.keepHint())
	}

	// And typing in the active workspace changes nothing about it.
	before := r.lastActive[WorkspaceChat]
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !r.idle.warned[WorkspaceChat] {
		t.Error("a keypress cleared another workspace's warning latch")
	}
	if r.lastActive[WorkspaceChat] != before {
		t.Error("a keypress reset another workspace's idle clock")
	}
}

// ---------------------------------------------------------------------------
// The bare-modifier KeyMsg
// ---------------------------------------------------------------------------

// bubbletea's Windows console reader forwards a BARE modifier key-down as
// a KeyRunes message carrying a single NUL: readConInputs skips key-UP
// records and VK_SHIFT and nothing else, keyType has no case for
// VK_CONTROL/VK_MENU/VK_LWIN/VK_CAPITAL so it ends in `return KeyRunes`,
// and a modifier record's Char is 0.
//
// handleKey drops it before anything can read it as typed text. Three
// separate editor bugs were that one rune -- see handleKey's own doc.
func TestABareModifierKeyNeverReachesAWorkspace(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)

	for _, runes := range [][]rune{{0}, {0, 0}} {
		ws.gotKeys = nil
		r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: runes})
		if len(ws.gotKeys) != 0 {
			t.Errorf("runes %v reached the workspace as %v", runes, ws.gotKeys)
		}
	}
}

// A real keystroke still gets through -- the filter must not have made the
// app deaf. Checked past the paste-burst detector, which holds a lone
// pasteable key briefly before releasing it.
func TestARealRuneStillReachesTheWorkspace(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)

	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	// The detector holds a single pasteable key; releasing it is what
	// delivers it, exactly as it does in the real app.
	for i := 0; i < 4 && len(ws.gotKeys) == 0 && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			break
		}
		_, cmd = r.Update(msg)
	}
	found := false
	for _, k := range ws.gotKeys {
		if k == "x" {
			found = true
		}
	}
	if !found {
		t.Errorf("the workspace received %v, want x among them", ws.gotKeys)
	}
}

// A non-rune key is unaffected by the filter.
func TestANonRuneKeyIsUnaffectedByTheBareModifierFilter(t *testing.T) {
	ws := newCapturingWorkspace(WorkspaceChat)
	r := rootWith(t, ws)
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	found := false
	for _, k := range ws.gotKeys {
		if k == "ctrl+z" {
			found = true
		}
	}
	if !found {
		t.Errorf("the workspace received %v, want ctrl+z", ws.gotKeys)
	}
}

func TestIsBareModifier(t *testing.T) {
	cases := []struct {
		k    tea.KeyMsg
		want bool
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}, true},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0, 0}}, true},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, false},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0, 'x'}}, false},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: nil}, false},
		{tea.KeyMsg{Type: tea.KeyCtrlZ}, false},
		{tea.KeyMsg{Type: tea.KeyEnter}, false},
		{tea.KeyMsg{Type: tea.KeyTab}, false},
	}
	for _, c := range cases {
		if got := isBareModifier(c.k); got != c.want {
			t.Errorf("isBareModifier(%v %q) = %v, want %v", c.k.Type, c.k.Runes, got, c.want)
		}
	}
}

// The NUL guard pasteable() has carried since F1/F4 is the same guard on
// the paste path alone. Kept asserted so the two cannot drift apart.
func TestPasteableStillRejectsANulRune(t *testing.T) {
	if pasteable(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}) {
		t.Error("a NUL rune is pasteable")
	}
	if !pasteable(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) {
		t.Error("an ordinary rune is not pasteable")
	}
}
