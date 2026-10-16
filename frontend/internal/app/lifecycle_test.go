package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// fakeWorkspace is a Workspace that records what the lifecycle did to it.
//
// The real workspaces this episode has to drive are chat (one) and the
// Editor (F13's, which does not exist). Done-when #1, #4 and #4b are all
// about the behaviour of SEVERAL workspaces, so they are proven here,
// through the real registry, with test-only definitions — Aryan's decision
// 2, and the same call F18 made for Suspend/Resume: proven by test because
// the thing that would exercise it manually is a later episode's.
type fakeWorkspace struct {
	id WorkspaceID

	// blob is what Suspend returns and what Resume was handed, so the
	// round trip is observable without parsing anything.
	blob     []byte
	resumed  []byte
	suspends int

	initCalls   int
	flushes     int
	dirty       int
	suspendErr  error
	gotSize     bool
	gotConnect  bool
	msgs        []tea.Msg
	lastDiagSeq int
}

func (f *fakeWorkspace) ID() WorkspaceID { return f.id }
func (f *fakeWorkspace) Init() tea.Cmd   { f.initCalls++; return nil }

func (f *fakeWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	f.msgs = append(f.msgs, msg)
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		f.gotSize = true
	case DiagnosticsMsg:
		f.lastDiagSeq = m.D.Seq
	case protocol.ConnectedMsg:
		f.gotConnect = true
	}
	return f, nil
}

func (f *fakeWorkspace) View(l layout.Layout) string { return "fake:" + string(f.id) }

func (f *fakeWorkspace) Suspend() ([]byte, error) {
	f.suspends++
	if f.suspendErr != nil {
		return nil, f.suspendErr
	}
	return f.blob, nil
}

func (f *fakeWorkspace) Resume(b []byte) error {
	f.resumed = append([]byte(nil), b...)
	return nil
}

func (f *fakeWorkspace) Cards() []layout.CardSlot { return nil }

func (f *fakeWorkspace) FlushDraft() { f.flushes++ }

// DirtyBufferCount is only consulted when dirty > 0 matters — UI-13's
// branch, which nothing in the real app can reach yet (there is no editor
// buffer; see DirtyBuffers' own doc).
func (f *fakeWorkspace) DirtyBufferCount() int { return f.dirty }

// lifecycleRoot builds a Root with n test workspaces registered through the
// REAL registry, so what these tests exercise is the shipped code path and
// not a parallel one.
//
// A provider default is seeded for newRoot's own reason (F10's chooser must
// not open underfoot), and SaveWorkspaceStateFn is replaced with a recorder
// for chat's saveStateFn reason: the real one writes
// %LOCALAPPDATA%\Point\state.json, and CLAUDE.md is explicit that nothing
// automated may touch that file outside a scratch profile.
type lifecycleFixture struct {
	root  *Root
	ws    map[WorkspaceID]*fakeWorkspace
	saved []savedState
	clock time.Time
}

type savedState struct {
	last  string
	blobs map[string]json.RawMessage
}

func lifecycleRoot(t *testing.T, ids ...WorkspaceID) *lifecycleFixture {
	t.Helper()
	return lifecycleRootWithState(t, config.State{
		Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"},
	}, config.Config{}, ids...)
}

func lifecycleRootWithState(t *testing.T, st config.State, cfg config.Config, ids ...WorkspaceID) *lifecycleFixture {
	t.Helper()
	f := &lifecycleFixture{
		ws:    make(map[WorkspaceID]*fakeWorkspace),
		clock: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	}

	defs := make([]WorkspaceDef, 0, len(ids))
	for _, id := range ids {
		id := id
		w := &fakeWorkspace{id: id, blob: []byte(`{"v":1,"` + string(id) + `":true}`)}
		f.ws[id] = w
		defs = append(defs, WorkspaceDef{
			ID:   id,
			Name: strings.ToUpper(string(id[:1])) + string(id[1:]),
			New:  func() Workspace { return w },
		})
	}

	r, err := New(Options{
		Theme:      theme.New("turquoise", theme.BackgroundTransparent),
		State:      st,
		Config:     cfg,
		Workspaces: defs,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Now = func() time.Time { return f.clock }
	r.StatusLinger = time.Millisecond // see newRoot
	r.SaveWorkspaceStateFn = func(last string, blobs map[string]json.RawMessage) error {
		copied := make(map[string]json.RawMessage, len(blobs))
		for k, v := range blobs {
			copied[k] = v
		}
		f.saved = append(f.saved, savedState{last: last, blobs: copied})
		return nil
	}
	f.root = r
	return f
}

// pumpDiagnostics delivers the diagnostics snapshot the real app delivers
// through a tea.Cmd. These tests call Update directly and drop the commands
// it returns -- which is what every test in this package does, and fine for
// everything except the one assertion that reads what the VACANT SCREEN
// renders, since that screen's whole content comes from this message.
//
// Not a drain loop: most of what Update returns here is a tea.Tick, and
// running one blocks for its full duration.
func (f *lifecycleFixture) pumpDiagnostics() {
	cmd := f.root.pushDiagnostics()
	if cmd == nil {
		return
	}
	f.root.Update(cmd())
}

// start sizes the terminal and dismisses the startup chooser, which is what
// most of these tests want: the chooser is the subject of three of them and
// in the way of the rest.
func (f *lifecycleFixture) start(t *testing.T) {
	t.Helper()
	f.root.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if _, ok := f.root.overlay.(*Chooser); ok {
		f.root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
}

// advance moves the fixture's clock. The idle timers read Root.Now, so a
// five-minute timeout is exercised without waiting five minutes — which is
// also the only way Done-when #4b (three workspaces unloading at three
// different moments) is assertable at all.
func (f *lifecycleFixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

// loaded is the set of currently loaded workspace ids, sorted by the
// registry's order so a failure message is stable.
func (f *lifecycleFixture) loaded() []string {
	var out []string
	for _, def := range f.root.Defs() {
		if _, ok := f.root.workspaces[def.ID]; ok {
			out = append(out, string(def.ID))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Done-when #1 — startup loads nothing
// ---------------------------------------------------------------------------

// The headline rule, and the one most easily regressed by a convenience
// added later: "Do not auto-load anything at startup."
func TestStartupLoadsNoWorkspaceEvenWithALastWorkspaceRecorded(t *testing.T) {
	f := lifecycleRootWithState(t, config.State{
		LastWorkspace: "chat",
		Provider:      &config.ProviderDefault{Provider: "mock", Model: "mock"},
	}, config.Config{}, "chat", "editor")

	if got := f.loaded(); len(got) != 0 {
		t.Fatalf("startup loaded %v, want nothing", got)
	}
	// last_workspace still governs what the frame is FOR, so the chat.*
	// actions' When predicates and UI-12's pre-selection both read it.
	if f.root.active != WorkspaceChat {
		t.Errorf("active = %q, want the recorded last workspace", f.root.active)
	}
	if f.ws["chat"].initCalls != 0 {
		t.Errorf("chat was constructed and Init'd at startup (%d calls)", f.ws["chat"].initCalls)
	}
}

// Picking one workspace leaves the other NOT_LOADED: "verify the map has one
// key". This is the assertion the old current() made impossible — it
// resurrected a Placeholder under r.active on every read, so the map was
// never observably missing a key.
func TestChoosingChatLeavesTheOtherWorkspaceNotLoaded(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)

	f.root.switchWorkspace("chat")

	if got := f.loaded(); len(got) != 1 || got[0] != "chat" {
		t.Fatalf("loaded %v, want exactly [chat]", got)
	}
	if _, ok := f.root.workspaces["editor"]; ok {
		t.Error("editor is in the map; NOT_LOADED is supposed to be an absent key")
	}
	if f.ws["editor"].initCalls != 0 {
		t.Error("editor was constructed without being chosen")
	}
}

// The startup chooser appears, and does not appear when there is nothing to
// choose — which is how every other test in this package avoids it.
func TestStartupChooserOpensOnlyWhenWorkspacesAreRegistered(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.root.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if _, ok := f.root.overlay.(*Chooser); !ok {
		t.Fatalf("no chooser opened; overlay is %T", f.root.overlay)
	}

	bare := newRoot(t) // no Options.Workspaces
	bare.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if _, ok := bare.overlay.(*Chooser); ok {
		t.Error("a Root with no registered workspaces opened a chooser over nothing")
	}
}

// Dismissing the chooser must not reopen it forever, and the screen behind
// it has to say how to get a workspace back — otherwise esc is a dead end
// whose only exit is a key nothing on screen mentions.
func TestDismissedChooserStaysClosedAndTheVacantScreenSaysWhatToPress(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.root.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	f.root.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if f.root.overlay != nil {
		t.Fatalf("esc did not dismiss the chooser; overlay is %T", f.root.overlay)
	}
	m, _ := f.root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if o := m.(*Root).overlay; o != nil {
		t.Fatalf("the chooser reopened after being dismissed: %T", o)
	}
	f.pumpDiagnostics()
	view := stripANSI(f.root.View())
	if !strings.Contains(view, "No workspace loaded") {
		t.Errorf("the vacant screen does not say nothing is loaded:\n%s", view)
	}
	if !strings.Contains(view, "choose a workspace") {
		t.Errorf("the vacant screen does not say how to get one:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Done-when #2 — unload never cancels a task
// ---------------------------------------------------------------------------

// The property this whole episode exists for, asserted as narrowly as it can
// be: unloading sends nothing, cancels nothing, and leaves the global task
// list exactly as it was. A task whose originating view is gone is still in
// the list, because the list is Root's and the task is in another process.
func TestUnloadSendsNothingAndLeavesRunningTasksAlone(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.root.tasks = []TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1},
	}
	var sent []string
	f.root.ListTasksFn = func(env protocol.Envelope) bool {
		sent = append(sent, env.Type)
		return true
	}

	f.root.unloadAndReport("chat", "Chat")

	if got := f.loaded(); len(got) != 0 {
		t.Fatalf("chat is still loaded: %v", got)
	}
	if len(f.root.tasks) != 1 || f.root.tasks[0].State != TaskStateRunning {
		t.Fatalf("the task list changed on unload: %+v", f.root.tasks)
	}
	for _, typ := range sent {
		if strings.Contains(typ, "ancel") {
			t.Fatalf("unload sent %q", typ)
		}
	}
}

// F12: "On unload with a running task, show: `unloading view — Research task
// continues in background`. That message is the demo beat. Do not replace it
// with a block." Asserted verbatim, because it is the one string in this
// episode whose exact wording is specified.
func TestUnloadWithARunningTaskShowsTheDemoBeatVerbatim(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.tasks = []TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1},
	}

	f.root.unloadAndReport("chat", "Chat")

	const want = "unloading view — Research task continues in background"
	if f.root.status != want {
		t.Fatalf("status is %q, want %q", f.root.status, want)
	}
}

// With the right pane hidden and the tray never opened, refreshTasks has
// deliberately sent nothing (F6's Done-when #6), so the snapshot cannot name
// what is running. conn.inFlight can still prove that SOMETHING is, and the
// notice has to degrade to that rather than claim nothing is running.
func TestUnloadDegradesTheNoticeRatherThanClaimingNothingRuns(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.tasks = nil // never refreshed: nothing that shows it was on screen
	f.root.conn.inFlight = []string{"corr-1"}

	f.root.unloadAndReport("chat", "Chat")

	if f.root.status != "unloading view — background work continues" {
		t.Fatalf("status is %q, want the unnamed degradation", f.root.status)
	}
}

// Nothing running at all: no reassurance to give, so it says what happened
// and nothing more.
func TestUnloadWithNothingRunningJustSaysSo(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.root.unloadAndReport("chat", "Chat")

	if f.root.status != "unloaded Chat" {
		t.Fatalf("status is %q", f.root.status)
	}
}

// UI-13's own empty state: "if nothing is dirty and no task runs, unload
// without asking" — and F12 is explicit that a RUNNING TASK is not a reason
// to ask either, because the notice is the point and a block would be the
// app asking permission for the thing this episode proves is safe.
func TestUnloadConfirmsOnlyForUnsavedWork(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.tasks = []TaskRow{{ID: "t1", Agent: "research", State: TaskStateRunning}}

	f.root.requestUnload()
	if f.root.overlay != nil {
		t.Fatalf("a running task opened a confirmation: %T", f.root.overlay)
	}
	if got := f.loaded(); len(got) != 0 {
		t.Fatalf("unload did not happen: %v", got)
	}

	// Now with unsaved work, which is the one case that asks.
	f.root.switchWorkspace("chat")
	f.ws["chat"].dirty = 2
	f.root.requestUnload()
	c, ok := f.root.overlay.(*Confirm)
	if !ok {
		t.Fatalf("unsaved work did not open UI-13; overlay is %T", f.root.overlay)
	}
	if !strings.Contains(c.body, "2 file(s) have unsaved changes") {
		t.Errorf("UI-13 does not name the dirty count: %q", c.body)
	}
	if !strings.Contains(c.body, "will keep running") {
		t.Errorf("UI-13 does not say tasks survive: %q", c.body)
	}
	if got := f.loaded(); len(got) != 1 {
		t.Fatalf("the workspace unloaded before the confirmation was answered: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Done-when #3 — Resume gets back exactly what Suspend stored
// ---------------------------------------------------------------------------

// The blob round trip, and the fact that Root never looks inside it.
func TestSuspendedBytesComeBackToResumeUnread(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.ws["chat"].blob = []byte(`{"version":1,"anchor":{"anchor_seq":4211,"row_offset":7,"pinned_to_bottom":false}}`)
	f.root.unloadAndReport("chat", "Chat")
	f.root.switchWorkspace("chat")

	got := string(f.ws["chat"].resumed)
	if got != string(f.ws["chat"].blob) {
		t.Fatalf("Resume was handed %q, want the exact bytes Suspend returned", got)
	}
	// Init runs after Resume: F18's applyResumeAnchor consumes the stored
	// anchor at the end of the load, so a load that resumed afterwards
	// would hand the anchor over after the thing that reads it had run.
	if f.ws["chat"].initCalls != 2 {
		t.Errorf("Init ran %d times across two loads", f.ws["chat"].initCalls)
	}
}

// A Suspend that fails must not make a workspace unloadable — that would
// turn a broken Suspend into a memory leak. The position is what is lost,
// and losing it is F18's documented degradation.
func TestAFailingSuspendStillUnloads(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.ws["chat"].suspendErr = fmt.Errorf("no position to store")

	f.root.unloadAndReport("chat", "Chat")

	if got := f.loaded(); len(got) != 0 {
		t.Fatalf("a failing Suspend blocked the unload: %v", got)
	}
	if _, ok := f.root.suspended["chat"]; ok {
		t.Error("a failed Suspend left bytes behind")
	}
}

// The draft is flushed synchronously before the workspace stops receiving
// messages — app.quit's Bug 17, one lifecycle event over.
func TestUnloadFlushesTheDraftBeforeDroppingTheWorkspace(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.root.unloadAndReport("chat", "Chat")

	if f.ws["chat"].flushes != 1 {
		t.Fatalf("FlushDraft ran %d times on unload, want 1", f.ws["chat"].flushes)
	}
}

// ---------------------------------------------------------------------------
// Done-when #4 and #4b — per-workspace timers
// ---------------------------------------------------------------------------

func idleCfg(global int, per map[string]int) config.Config {
	table := map[string]any{"idle_unload_minutes": int64(global)}
	for id, mins := range per {
		table[id] = map[string]any{"idle_unload_minutes": int64(mins)}
	}
	return config.Config{Workspace: table}
}

// An inactive workspace unloads on its own; the active one never does,
// however long it sits.
func TestAnIdleWorkspaceUnloadsAndTheActiveOneNeverDoes(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)

	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor") // chat's timer starts now

	f.advance(90 * time.Second)
	f.root.walkIdle(f.clock)

	if got := f.loaded(); len(got) != 1 || got[0] != "editor" {
		t.Fatalf("loaded %v, want only the active editor", got)
	}

	// And the active one, left for twenty times its timeout.
	f.advance(20 * time.Minute)
	f.root.walkIdle(f.clock)
	if got := f.loaded(); len(got) != 1 || got[0] != "editor" {
		t.Fatalf("the ACTIVE workspace unloaded: loaded %v", got)
	}
}

// Done-when #4b, the one that cannot be checked by hand until F13 lands:
// three workspaces left at three different moments unload at three
// different moments, not together. A single global timer passes every other
// test in this file and fails this one.
func TestPerWorkspaceTimersAreIndependent(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor", "data")
	f.start(t)

	// F12's own worked example, at a one-minute timeout.
	f.root.switchWorkspace("chat")
	f.advance(30 * time.Second)
	f.root.switchWorkspace("editor") // chat's timer starts at t=0:30
	f.advance(20 * time.Second)
	f.root.switchWorkspace("data") // editor's timer starts at t=0:50

	// t=1:20 — chat has been idle 50s, editor 30s. Nothing is due.
	f.advance(30 * time.Second)
	f.root.walkIdle(f.clock)
	if got := f.loaded(); len(got) != 3 {
		t.Fatalf("something unloaded early: loaded %v", got)
	}

	// t=1:35 — chat has been idle 65s. Chat alone is due.
	f.advance(15 * time.Second)
	f.root.walkIdle(f.clock)
	if got := f.loaded(); len(got) != 2 || got[0] != "editor" {
		t.Fatalf("after chat's timeout, loaded %v, want [editor data]", got)
	}

	// t=1:55 — editor has been idle 65s. Editor is due; data is active.
	f.advance(20 * time.Second)
	f.root.walkIdle(f.clock)
	if got := f.loaded(); len(got) != 1 || got[0] != "data" {
		t.Fatalf("after editor's timeout, loaded %v, want only the active data", got)
	}
}

// A per-workspace override beats the table default, which is the whole
// reason for `[workspace.editor]` — "the Editor is expensive to reload, so
// it should live longer".
func TestAPerWorkspaceOverrideBeatsTheDefault(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, map[string]int{"editor": 60}), "chat", "editor", "data")
	f.start(t)

	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")
	f.root.switchWorkspace("data") // both chat and editor are now inactive

	f.advance(5 * time.Minute)
	f.root.walkIdle(f.clock)

	if got := f.loaded(); len(got) != 2 || got[0] != "editor" || got[1] != "data" {
		t.Fatalf("loaded %v, want the long-lived editor and the active data", got)
	}
}

// Zero disables unloading entirely, and the walk must then not run at all —
// the idle-CPU guarantee animState already holds.
func TestZeroTimeoutDisablesUnloadingAndTheWalk(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(0, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")

	if n := f.root.unloadableLoaded(); n != 0 {
		t.Fatalf("%d workspaces are considered unloadable at a zero timeout", n)
	}
	if cmd := f.root.ensureIdleWalk(); cmd != nil {
		t.Error("the idle walk armed itself with nothing to unload")
	}
	f.advance(time.Hour)
	f.root.walkIdle(f.clock)
	if got := f.loaded(); len(got) != 2 {
		t.Fatalf("a zero timeout unloaded something: loaded %v", got)
	}
}

// With only the active workspace loaded there is nothing to watch, so the
// tick must not be running.
func TestTheWalkDoesNotRunWithOnlyTheActiveWorkspaceLoaded(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(5, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	if cmd := f.root.ensureIdleWalk(); cmd != nil {
		t.Fatal("the walk armed itself with only the active workspace loaded")
	}
	f.root.switchWorkspace("editor")
	if cmd := f.root.ensureIdleWalk(); cmd == nil {
		t.Fatal("the walk did not arm once a workspace became inactive")
	}
}

// The grace warning fires once per idle period, names the workspace and the
// time left, and expires rather than sitting in the status bar forever —
// which is what "Editor unloads in 30s" left behind after the Editor
// unloaded would be.
func TestTheGraceWarningFiresOnceAndExpires(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")

	f.advance(25 * time.Second) // 35s left: outside the 30s grace window
	f.root.walkIdle(f.clock)
	if strings.Contains(f.root.status, "unloads in") {
		t.Fatalf("warned too early: %q", f.root.status)
	}

	f.advance(10 * time.Second) // 25s left
	cmd := f.root.walkIdle(f.clock)
	if !strings.Contains(f.root.status, "Chat unloads in") {
		t.Fatalf("status is %q, want a grace warning naming Chat", f.root.status)
	}
	// It names the SWITCHER, not a key. The rescue a key used to perform
	// is gone -- see lifecycle.go where keepWarnedAlive was -- and a
	// toast promising a key that does nothing is the hint-that-lies this
	// codebase refuses.
	if !strings.Contains(f.root.status, "to keep it") {
		t.Fatalf("the warning does not say what to do: %q", f.root.status)
	}
	if strings.Contains(f.root.status, "any key") {
		t.Fatalf("the warning still promises any key: %q", f.root.status)
	}
	warning := f.root.status

	// Once, not once per tick.
	f.root.status = ""
	f.advance(time.Second)
	f.root.walkIdle(f.clock)
	if f.root.status != "" {
		t.Errorf("warned twice in one idle period: %q", f.root.status)
	}

	// And it clears itself.
	if cmd == nil {
		t.Fatal("the warning scheduled no expiry")
	}
	f.root.status = warning
	f.root.Update(statusExpireMsg{text: warning})
	if f.root.status != "" {
		t.Errorf("the warning did not expire: %q", f.root.status)
	}
}

// A keystroke in the ACTIVE workspace has ZERO effect on a backgrounded
// workspace's idle countdown.
//
// This is the inverse of what F12 shipped. keepWarnedAlive reset the clock
// of every workspace inside its grace window from any key in any
// workspace, which meant a backgrounded workspace could never unload while
// anybody was typing: it cycled idle -> warn -> rescued -> idle, forever.
// Reproduced by hand as "stay in the Editor and type; Chat's countdown
// returns to full".
//
// Switching to a workspace is still a rescue, and is the only one -- it is
// the one act that evidences interest in it, and it is what the warning
// names.
func TestAKeyInOneWorkspaceDoesNotRescueAnother(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, map[string]int{"data": 5}), "chat", "editor", "data")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("data")
	f.root.switchWorkspace("editor") // chat and data both inactive

	f.advance(40 * time.Second) // chat is inside its grace window; data is not
	f.root.walkIdle(f.clock)
	if !f.root.idle.warned["chat"] {
		t.Fatal("chat was not warned")
	}
	if f.root.idle.warned["data"] {
		t.Fatal("data was warned 4+ minutes from its own timeout")
	}
	chatIdleBefore := f.clock.Sub(f.root.lastActive["chat"])

	// Type in the Editor, which is where the user actually is.
	for i := 0; i < 10; i++ {
		f.root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}

	if !f.root.idle.warned["chat"] {
		t.Error("typing elsewhere cleared chat's warning latch")
	}
	if got := f.clock.Sub(f.root.lastActive["chat"]); got < chatIdleBefore {
		t.Errorf("typing elsewhere reset chat's clock: %s -> %s", chatIdleBefore, got)
	}

	// ...and it goes on to unload, which is the whole point.
	f.advance(25 * time.Second)
	f.root.walkIdle(f.clock)
	if _, loaded := f.root.workspaces["chat"]; loaded {
		t.Error("chat never unloaded despite being idle past its timeout")
	}

	// The ACTIVE workspace is still kept alive by its own keys -- a
	// different rule, and the one that has to stay true.
	if _, ok := f.root.lastActive["editor"]; ok {
		t.Error("the active workspace has an idle clock running")
	}
}

// Switching to a warned workspace is the rescue the warning names.
func TestSwitchingToAWarnedWorkspaceKeepsIt(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")

	f.advance(40 * time.Second)
	f.root.walkIdle(f.clock)
	if !f.root.idle.warned["chat"] {
		t.Fatal("chat was not warned")
	}

	f.root.switchWorkspace("chat")
	if f.root.idle.warned["chat"] {
		t.Error("switching to chat left its warning latched")
	}
	if _, ok := f.root.lastActive["chat"]; ok {
		t.Error("switching to chat left an idle clock running on it")
	}

	// And leaving again starts a fresh full countdown rather than
	// resuming the old one.
	f.root.switchWorkspace("editor")
	f.advance(10 * time.Second)
	f.root.walkIdle(f.clock)
	if f.root.idle.warned["chat"] {
		t.Error("chat warned 50s before its own timeout")
	}
	if _, loaded := f.root.workspaces["chat"]; !loaded {
		t.Error("chat unloaded 50s early")
	}
}

// The idle readout, which is the other affordance F12 requires alongside
// the timer "or unloading feels like the app losing your work at random".
//
// A countdown, CLAMPED so elapsed can never print larger than the budget
// it counts against -- see WorkspaceStatus.Label for why the clamp and the
// pinned label come as a pair.
func TestTheIdleReadoutCountsDownAndNamesTheOtherStates(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(5, nil), "chat", "editor", "data")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.advance(3 * time.Minute)
	f.root.switchWorkspace("editor")
	f.advance(3 * time.Minute)

	want := map[WorkspaceID]string{
		"chat":   "idle 3m00s of 5m00s",
		"editor": "active",
		"data":   "not loaded",
	}
	for _, s := range f.root.WorkspaceStatuses(f.clock) {
		if got := s.Label(); got != want[s.Def.ID] {
			t.Errorf("%s reads %q, want %q", s.Def.ID, got, want[s.Def.ID])
		}
	}
}

// A workspace configured never to unload has nothing to count down to.
func TestTheIdleReadoutSaysSoWhenAWorkspaceNeverUnloads(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(0, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")
	f.advance(10 * time.Minute)

	for _, st := range f.root.WorkspaceStatuses(f.clock) {
		if st.Def.ID != "chat" {
			continue
		}
		if got := st.Label(); got != "loaded · stays loaded" {
			t.Errorf("chat reads %q, want %q", got, "loaded · stays loaded")
		}
	}
}

// THE CLAMP. Elapsed is never displayed larger than the budget, however
// far past it the raw figure has run -- and the pinned case says why it is
// sitting at its limit rather than implying an unload that is not coming.
func TestTheIdleReadoutClampsElapsedToTheBudget(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")

	// Chat holds unsaved work, so the sweep pins it rather than unloading
	// it -- which is the only way elapsed genuinely outruns the budget.
	f.ws["chat"].dirty = 2

	for _, elapsed := range []time.Duration{
		90 * time.Second, 2*time.Minute + 35*time.Second, time.Hour,
	} {
		f.clock = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
		f.root.lastActive["chat"] = f.clock.Add(-elapsed)
		f.root.walkIdle(f.clock)

		var chat WorkspaceStatus
		for _, st := range f.root.WorkspaceStatuses(f.clock) {
			if st.Def.ID == "chat" {
				chat = st
			}
		}

		if _, loaded := f.root.workspaces["chat"]; !loaded {
			t.Fatalf("elapsed %s: a dirty workspace was unloaded", elapsed)
		}
		if !chat.Pinned {
			t.Errorf("elapsed %s: chat is not reported pinned", elapsed)
		}
		if chat.Idle < elapsed {
			t.Errorf("elapsed %s: the raw Idle field reads %s, want it unclamped",
				elapsed, chat.Idle)
		}

		want := "idle 1m00s of 1m00s · pinned"
		if got := chat.Label(); got != want {
			t.Errorf("elapsed %s: reads %q, want %q", elapsed, got, want)
		}
	}
}

// The clamp is unconditional, not pinned-only: a workspace is also over
// its budget in the window between the timeout elapsing and the next
// one-second tick unloading it, and a readout that printed `of` backwards
// for one frame is not worth a special case.
func TestTheIdleReadoutClampsEvenWhenNotPinned(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(1, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.switchWorkspace("editor")
	f.advance(5 * time.Minute) // over budget, and NOT dirty

	for _, st := range f.root.WorkspaceStatuses(f.clock) {
		if st.Def.ID != "chat" {
			continue
		}
		if st.Pinned {
			t.Error("a clean workspace is reported pinned")
		}
		if got := st.Label(); got != "idle 1m00s of 1m00s" {
			t.Errorf("reads %q, want the clamped countdown with no pinned label", got)
		}
		// The raw field is untouched -- only the rendering clamps.
		if st.Idle < 5*time.Minute {
			t.Errorf("Idle reads %s, want the raw elapsed time", st.Idle)
		}
	}
}

// Whatever the inputs, the rendered elapsed never exceeds the rendered
// budget. Stated as the invariant rather than as the three cases above.
func TestTheIdleReadoutNeverRendersElapsedPastItsBudget(t *testing.T) {
	budgets := []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute}
	elapsed := []time.Duration{
		0, time.Second, 29 * time.Second, time.Minute,
		3 * time.Minute, time.Hour, 24 * time.Hour,
	}
	for _, budget := range budgets {
		for _, el := range elapsed {
			for _, pinned := range []bool{false, true} {
				st := WorkspaceStatus{
					Def:     WorkspaceDef{ID: "x", Name: "X"},
					Loaded:  true,
					Idle:    el,
					Timeout: budget,
					Pinned:  pinned,
				}
				label := st.Label()
				shown := el
				if shown > budget {
					shown = budget
				}
				want := "idle " + shortDuration(shown) + " of " + shortDuration(budget)
				if pinned {
					want += " · pinned"
				}
				if label != want {
					t.Errorf("budget %s elapsed %s pinned %v: %q, want %q",
						budget, el, pinned, label, want)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Done-when #5 — quit warns, and says it is DIFFERENT from unload
// ---------------------------------------------------------------------------

// UI-14 lists what quitting cancels, and says so — the asymmetry with
// unload is the reason the dialog exists at all.
func TestQuitConfirmListsRunningTasksAndNamesTheAsymmetry(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.conn.inFlight = []string{"c1"}
	f.root.tasks = []TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateRunning, StartedAt: 1},
	}

	body := quitBody(f.root, 1)
	for _, want := range []string{"1 task is still running", "Research", "Quitting will cancel them", "unloading a view would not"} {
		if !strings.Contains(body, want) {
			t.Errorf("UI-14's body does not contain %q:\n%s", want, body)
		}
	}
}

// A snapshot with no rows and a non-zero in-flight count still warns, in the
// honest wording for "something is running and I cannot name it".
func TestQuitConfirmWarnsEvenWithoutNamedRows(t *testing.T) {
	f := lifecycleRoot(t, "chat")
	body := quitBody(f.root, 2)
	if !strings.Contains(body, "2 request(s) still in flight") {
		t.Errorf("body does not warn from the in-flight count:\n%s", body)
	}
	if !strings.Contains(body, "cancels them") {
		t.Errorf("body does not say they are cancelled:\n%s", body)
	}
}

// The dialog waits for a fresh snapshot rather than drawing a stale one, and
// opens anyway if the answer never comes — a ctrl+q that visibly does
// nothing would be worse than a list one task short.
func TestQuitWaitsForAFreshSnapshotAndOpensAnywayOnTimeout(t *testing.T) {
	f := lifecycleRoot(t, "chat")
	f.start(t)
	// A client to ask with. It is never Start()ed -- ListTasksFn below is
	// what actually records the request, the same seam refreshTasks' own
	// tests use -- but requestTasksOffScreen refuses to ask at all with no
	// backend, which is itself correct and is covered by the dialog
	// opening immediately in that case.
	f.root.client = protocol.NewClient(protocol.Options{})
	f.root.conn.inFlight = []string{"c1"}

	var asked int
	f.root.ListTasksFn = func(protocol.Envelope) bool { asked++; return true }

	a, ok := f.root.actions.ByID("app.quit")
	if !ok {
		t.Fatal("app.quit is not registered")
	}
	a.Run(f.root)

	if asked != 1 {
		t.Fatalf("quit asked for %d snapshots, want 1", asked)
	}
	if f.root.overlay != nil {
		t.Fatalf("the dialog opened before the snapshot landed: %T", f.root.overlay)
	}
	f.root.Update(quitConfirmTimeoutMsg{})
	if _, ok := f.root.overlay.(*Confirm); !ok {
		t.Fatalf("the dialog never opened; overlay is %T", f.root.overlay)
	}
}

// ---------------------------------------------------------------------------
// Done-when #6 — survive a kill
// ---------------------------------------------------------------------------

// A kill never calls Suspend, so the debounced save has to have asked the
// ACTIVE workspace for a blob in advance. This is the only path that writes
// one for a workspace that is still loaded.
func TestTheDebouncedSaveSuspendsTheActiveWorkspace(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.root.flushWorkspaceState()

	if len(f.saved) == 0 {
		t.Fatal("nothing was saved")
	}
	last := f.saved[len(f.saved)-1]
	if last.last != "chat" {
		t.Errorf("last_workspace saved as %q", last.last)
	}
	if string(last.blobs["chat"]) != string(f.ws["chat"].blob) {
		t.Fatalf("saved blob is %q, want the active workspace's own bytes",
			last.blobs["chat"])
	}
	if f.ws["chat"].suspends == 0 {
		t.Error("the active workspace was never asked to suspend")
	}
}

// Blobs read out of state.json at startup are held, unparsed, and handed
// back on load — WITHOUT loading anything, which is the distinction F12's
// "a stored blob existing does not mean the workspace is loaded" turns on.
func TestStoredBlobsAreRestoredWithoutLoadingAnything(t *testing.T) {
	stored := json.RawMessage(`{"version":1,"anchor":{"anchor_seq":99}}`)
	f := lifecycleRootWithState(t, config.State{
		LastWorkspace:  "chat",
		Provider:       &config.ProviderDefault{Provider: "mock", Model: "mock"},
		WorkspaceState: map[string]json.RawMessage{"chat": stored},
	}, config.Config{}, "chat", "editor")

	if got := f.loaded(); len(got) != 0 {
		t.Fatalf("a stored blob loaded a workspace: %v", got)
	}
	if string(f.root.suspended["chat"]) != string(stored) {
		t.Fatalf("stored blob is %q", f.root.suspended["chat"])
	}

	f.start(t)
	f.root.switchWorkspace("chat")
	if string(f.ws["chat"].resumed) != string(stored) {
		t.Fatalf("Resume was handed %q, want the stored bytes", f.ws["chat"].resumed)
	}
}

// ---------------------------------------------------------------------------
// The registry itself
// ---------------------------------------------------------------------------

// A workspace's leader key goes through the SAME validation as every
// hand-registered action, so a collision is a boot failure naming both
// offenders rather than a key that quietly does the other thing. This is
// also the proof that LeaderKey is wired at all, which chat's own
// registration cannot give (its key is empty — `c` is F6's).
func TestAWorkspaceLeaderKeyCollidingWithAnActionFailsAtBoot(t *testing.T) {
	_, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{{
			ID: "editor", Name: "Editor",
			LeaderKey: "c", // pane.right.toggle's, since F6
			New:       func() Workspace { return &fakeWorkspace{id: "editor"} },
		}},
	})
	if err == nil {
		t.Fatal("a workspace shadowed an existing leader binding without complaint")
	}
	for _, want := range []string{"c", "Toggle Right Pane", "Editor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

// A free leader key works, which is what makes the field real rather than
// decorative.
func TestAWorkspaceLeaderKeySwitchesToIt(t *testing.T) {
	f := lifecycleRoot(t, "chat")
	// Rebuild with a leader key; lifecycleRoot leaves them empty.
	w := &fakeWorkspace{id: "editor"}
	r, err := New(Options{
		Theme: theme.New("turquoise", theme.BackgroundTransparent),
		State: config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		Workspaces: []WorkspaceDef{{
			ID: "editor", Name: "Editor", LeaderKey: "e",
			New: func() Workspace { return w },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.SaveWorkspaceStateFn = func(string, map[string]json.RawMessage) error { return nil }
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.Update(tea.KeyMsg{Type: tea.KeyEsc}) // dismiss the chooser

	a, ok := r.actions.ByLeader("e")
	if !ok {
		t.Fatal("no action bound to the workspace's leader key")
	}
	a.Run(r)
	if _, loaded := r.workspaces["editor"]; !loaded {
		t.Fatal("the leader key did not load the workspace")
	}
	_ = f
}

// A definition with no ID or no constructor is a boot failure, not a
// workspace that cannot be opened.
func TestAMalformedWorkspaceDefinitionFailsAtBoot(t *testing.T) {
	cases := []struct {
		name string
		def  WorkspaceDef
		want string
	}{
		{"no ID", WorkspaceDef{Name: "Nameless", New: func() Workspace { return nil }}, "no ID"},
		{"no constructor", WorkspaceDef{ID: "ghost", Name: "Ghost"}, "no New constructor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{
				Theme:      theme.New("turquoise", theme.BackgroundTransparent),
				State:      config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
				Workspaces: []WorkspaceDef{tc.def},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error is %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// The palette and the help screen derive their workspace entries from the
// registry, which is the property that makes adding a workspace one entry
// rather than edits in five files.
func TestTheRegistryDerivesPaletteEntries(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	var found []string
	for _, a := range f.root.actions.All() {
		if strings.HasPrefix(a.ID, "workspace.switch.") {
			found = append(found, a.ID)
		}
	}
	if len(found) != 2 {
		t.Fatalf("the registry derived %v, want one switch action per workspace", found)
	}
}

// A workspace loaded on demand has missed the handshake and the window
// size, and chat.Workspace does not bootstrap without the former: its own
// doc says Update watches for ConnectedMsg because a query fails while the
// client is not connected. Before lazy loading this could not happen.
func TestALazilyLoadedWorkspaceIsGivenTheHandshakeAndTheSize(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.conn.handshaked = true
	f.root.conn.protocolVersion = 1

	f.root.switchWorkspace("chat")

	if !f.ws["chat"].gotSize {
		t.Error("a lazily loaded workspace never received a WindowSizeMsg")
	}
	if !f.ws["chat"].gotConnect {
		t.Error("a lazily loaded workspace never received a ConnectedMsg")
	}
}

// Unloading the only loaded workspace leaves the map empty and r.active
// still naming it — "which workspace the frame is for" and "is it in
// memory" are deliberately separate facts.
func TestUnloadingTheLastWorkspaceLeavesNothingLoaded(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	f.root.unloadAndReport("chat", "Chat")

	if len(f.root.workspaces) != 0 {
		t.Fatalf("the map holds %v", f.loaded())
	}
	if f.root.active != "chat" {
		t.Errorf("active = %q, want it still naming the unloaded workspace", f.root.active)
	}
	if _, ok := f.root.current().(*Placeholder); !ok {
		t.Errorf("current() is %T, want the vacant diagnostics screen", f.root.current())
	}
}

// Unloading the active one when another is loaded hands the screen over
// rather than leaving the user on the vacant screen with a loaded workspace
// sitting unreachable behind it.
func TestUnloadingTheActiveWorkspaceFallsBackToAnotherLoadedOne(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("editor")
	f.root.switchWorkspace("chat")

	f.root.unloadAndReport("chat", "Chat")

	if f.root.active != "editor" {
		t.Fatalf("active = %q, want the other loaded workspace", f.root.active)
	}
	if got := f.loaded(); len(got) != 1 || got[0] != "editor" {
		t.Fatalf("loaded %v", got)
	}
}

// current() must not resurrect an unloaded workspace. This is the F1
// invariant edit, asserted directly: the old implementation stored a
// Placeholder under r.active on every read, so one View() after an unload
// put the key back and NOT_LOADED stopped being observable.
func TestReadingTheCurrentWorkspaceDoesNotResurrectTheKey(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.unloadAndReport("chat", "Chat")

	for i := 0; i < 5; i++ {
		f.root.View()
		f.root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
		f.root.current()
	}

	if len(f.root.workspaces) != 0 {
		t.Fatalf("reading current() put %v back in the map", f.loaded())
	}
}

// The vacant screen stays LIVE — it receives diagnostics like any
// workspace, which matters because it is the screen a user with a broken
// backend spends their whole session on.
func TestTheVacantScreenKeepsReceivingDiagnostics(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.root.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	f.root.Update(tea.KeyMsg{Type: tea.KeyEsc}) // dismiss the chooser

	f.pumpDiagnostics()
	before := stripANSI(f.root.View())
	f.root.Update(statusMsg("x"))
	f.pumpDiagnostics()
	after := stripANSI(f.root.View())

	if !strings.Contains(before, "No workspace loaded") || !strings.Contains(after, "No workspace loaded") {
		t.Fatalf("the vacant screen stopped rendering:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if f.root.vacant == nil {
		t.Error("the vacant screen is not held anywhere")
	}
	if _, ok := f.root.workspaces[f.root.active]; ok {
		t.Error("the vacant screen leaked into the workspace map")
	}
}

// windowSize is tea.WindowSizeMsg, spelled once.
func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// ---------------------------------------------------------------------------
// Defects found on the first run against the real app
// ---------------------------------------------------------------------------

// The switcher's idle readout has to reach the column that is actually
// RENDERED.
//
// It did not: renderRow draws the title and the NOTE, the description column
// was dropped from Popup outright, and the row therefore showed its binding
// -- "—" for the one workspace that exists -- while "active" went nowhere.
func TestTheSwitcherRendersTheIdleReadoutNotJustStoresIt(t *testing.T) {
	f := lifecycleRootWithState(t,
		config.State{Provider: &config.ProviderDefault{Provider: "mock", Model: "mock"}},
		idleCfg(5, nil), "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.advance(2 * time.Minute)
	f.root.switchWorkspace("editor")
	f.advance(90 * time.Second)

	// Rendered at the size the app actually gives it. MinSize is the
	// refusal floor (50 cells), where rowWidths caps the note column at
	// a third of the row and a long readout would elide -- the one value
	// this popup exists to show. The real width comes from PreferredSize,
	// exactly as overlayRect applies it.
	p := newSwitcherOverlay(f.root)
	if minW, _ := p.MinSize(); minW != 50 {
		t.Errorf("MinSize width is %d, want the unchanged 50-cell floor", minW)
	}
	w, h := p.PreferredSize(f.root.layout.Main.W-2, f.root.layout.Main.H-2)
	view := stripANSI(p.View(theme.New("turquoise", theme.BackgroundTransparent), w-2, h-2))

	for _, want := range []string{"active", "idle 1m30s of 5m00s"} {
		if !strings.Contains(view, want) {
			t.Errorf("the switcher does not show %q at %dx%d:\n%s", want, w, h, view)
		}
	}

	// And through the real frame, so the growth has to survive
	// overlayRect rather than only PreferredSize -- the same end-to-end
	// check the quit dialog gets below.
	f.root.OpenOverlay(newSwitcherOverlay(f.root))
	frame := stripANSI(f.root.View())
	if !strings.Contains(frame, "idle 1m30s of 5m00s") {
		t.Errorf("the rendered frame elides the readout:\n%s", frame)
	}
}

// Every line the chooser draws has to fit the box the chooser asked for.
//
// Found against the real app as "last: Chat - position restored on op":
// MinSize measured only the button row, and the `last:` line is the widest
// thing in the box. padCells then cut it, silently and with no ellipsis,
// which is why it read as a typo rather than as an overflow.
func TestTheChooserIsWideEnoughForEveryLineItDraws(t *testing.T) {
	f := lifecycleRootWithState(t, config.State{
		LastWorkspace:  "chat",
		Provider:       &config.ProviderDefault{Provider: "mock", Model: "mock"},
		WorkspaceState: map[string]json.RawMessage{"chat": json.RawMessage(`{"version":1}`)},
	}, config.Config{}, "chat", "editor")

	c := newChooser(f.root)
	w, h := c.MinSize()
	view := stripANSI(c.View(theme.New("turquoise", theme.BackgroundTransparent), w-2, h-2))

	for _, want := range []string{c.question, c.lastLine(), c.hint} {
		if !strings.Contains(view, want) {
			t.Errorf("%q was cut; the box is %d wide:\n%s", want, w, view)
		}
	}
	if rows := len(strings.Split(view, "\n")); rows > h-2 {
		t.Errorf("the chooser drew %d rows into %d available", rows, h-2)
	}
}

// A hint bar that does not fit drops whole hints and says how many, rather
// than cutting a binding name in half.
//
// Found at 74 columns as "… w Workspac" -- a claim about what a key does,
// with the wrong word in it. F12's two new leader actions are what pushed
// the list past the edge; it fitted at 100 columns before them.
func TestTheHintBarDropsWholeHintsRatherThanCuttingOne(t *testing.T) {
	parts := []string{"? Help", "q Quit", "s Settings", "u Unload Workspace", "w Workspaces"}

	for _, width := range []int{20, 30, 40, 50, 60, 74, 90, 200} {
		got := fitHints(parts, width)
		if n := runewidth.StringWidth(got); n > width {
			t.Errorf("width %d: fitHints returned %d cells: %q", width, n, got)
		}
		for _, shown := range strings.Split(got, hintGap) {
			if shown == "" || strings.HasPrefix(shown, "+") {
				continue
			}
			whole := false
			for _, p := range parts {
				if p == shown {
					whole = true
				}
			}
			if !whole {
				t.Errorf("width %d: %q is not a whole hint (from %q)", width, shown, got)
			}
		}
	}

	// At the width that produced the bug, the last hint is dropped whole
	// and counted. "Unload Workspace" legitimately contains "Workspac", so
	// the whole names are removed before looking for a fragment.
	got := fitHints(parts, 60)
	probe := got
	for _, frag := range []string{"Workspaces", "Unload Workspace"} {
		probe = strings.ReplaceAll(probe, frag, "")
	}
	if strings.Contains(probe, "Workspac") {
		t.Fatalf("a hint was cut mid-word: %q", got)
	}
	if !strings.Contains(got, "+") {
		t.Errorf("hints were dropped without saying so: %q", got)
	}
}

// The same thing through the real status bar, at the width it was reported
// at. The leader hint bar is what produced the symptom, so it is what this
// renders.
func TestTheStatusBarNeverShowsAPartialBindingName(t *testing.T) {
	f := lifecycleRoot(t, "chat", "editor")
	f.start(t)
	f.root.switchWorkspace("chat")

	for _, width := range []int{70, 74, 80, 100, 120} {
		f.root.Update(windowSize(width, 30))
		f.root.leader.pending = true
		f.root.leader.showHint = true
		bar := stripANSI(f.root.statusBar(f.root.layout))

		if n := runewidth.StringWidth(bar); n > f.root.layout.StatusBar.W {
			t.Errorf("width %d: the status bar is %d cells in a %d-cell slot",
				width, n, f.root.layout.StatusBar.W)
		}
		// "Workspac" with no "es" after it is the exact reported symptom,
		// and "Unload Workspace" legitimately contains "Workspac" too --
		// so the check is that every occurrence belongs to a whole name.
		for _, frag := range []string{"Workspaces", "Unload Workspace"} {
			bar = strings.ReplaceAll(bar, frag, "")
		}
		if strings.Contains(bar, "Workspac") {
			t.Errorf("width %d: a binding name was cut: %q",
				width, stripANSI(f.root.statusBar(f.root.layout)))
		}
	}
}

// UI-14's buttons and focus marker have to be on screen.
//
// Confirm.MinSize returned a flat height of 7 -- five content rows --
// however long its body was, and overlayRect treats a plain Overlay's
// MinSize as the ACTUAL size. F12's quit body wraps to four lines, so the
// button row and the key hint were rows six and seven and were simply cut
// off: no labels, no focus marker, while left/right and Enter still worked.
// The WIDTH axis of this same bug was already fixed once (F9a's long button
// labels); the height was left a constant.
func TestTheQuitConfirmShowsItsButtonsAndFocus(t *testing.T) {
	f := lifecycleRoot(t, "chat")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.conn.inFlight = []string{"c1"}
	f.root.tasks = []TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateRunning,
			StartedAt: float64(time.Now().Unix() - 12)},
	}

	c := NewConfirm("Quit Point?", quitBody(f.root, 1), "Quit", "Cancel", nil)

	// MinSize stays the refusal FLOOR -- raising it would stop short
	// terminals opening the dialog at all, which for a warning about
	// losing work is the worst possible trade (F06's own note).
	if _, minH := c.MinSize(); minH != confirmFloorH {
		t.Errorf("MinSize height is %d, want the unchanged floor %d", minH, confirmFloorH)
	}

	mo, ok := any(c).(MaximisedOverlay)
	if !ok {
		t.Fatal("Confirm cannot grow: it is not a MaximisedOverlay")
	}
	w, h := mo.PreferredSize(100, 30)
	view := stripANSI(c.View(theme.New("turquoise", theme.BackgroundTransparent), w-2, h-2))

	for _, want := range []string{"Research", "Quit", "Cancel", "esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the quit dialog does not show %q at %dx%d:\n%s", want, w, h, view)
		}
	}
	// "No is focused by default" (F07), and it has to be VISIBLE.
	if !strings.Contains(view, "[ Cancel ]") {
		t.Errorf("the focused button is not marked:\n%s", view)
	}
	if strings.Contains(view, "[ Quit ]") {
		t.Errorf("the destructive button is focused by default:\n%s", view)
	}
	if rows := len(strings.Split(view, "\n")); rows > h-2 {
		t.Errorf("the dialog drew %d rows into %d available:\n%s", rows, h-2, view)
	}
}

// And through the real action at a real terminal size, so the growth has to
// survive overlayRect rather than only PreferredSize.
func TestTheQuitConfirmFitsOnScreenThroughTheRealPath(t *testing.T) {
	f := lifecycleRoot(t, "chat")
	f.start(t)
	f.root.switchWorkspace("chat")
	f.root.client = protocol.NewClient(protocol.Options{})
	f.root.conn.inFlight = []string{"c1"}
	f.root.tasks = []TaskRow{
		{ID: "t1", Agent: "research", State: TaskStateRunning,
			StartedAt: float64(time.Now().Unix() - 12)},
	}
	f.root.ListTasksFn = func(protocol.Envelope) bool { return true }

	a, _ := f.root.actions.ByID("app.quit")
	a.Run(f.root)
	f.root.Update(quitConfirmTimeoutMsg{})
	if _, ok := f.root.overlay.(*Confirm); !ok {
		t.Fatalf("no dialog opened: %T", f.root.overlay)
	}

	frame := stripANSI(f.root.View())
	for _, want := range []string{"Quit Point?", "[ Cancel ]", "Research"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the rendered frame does not show %q:\n%s", want, frame)
		}
	}
}

// The provider chosen before any workspace existed has to reach the one
// that is loaded afterwards.
//
// On a fresh profile the first-run chooser opens before anything is loaded,
// so ProviderDefaultSetMsg reached the vacant diagnostics screen and was
// gone; chat was then built from the State main.go captured at startup,
// where Provider is nil. The session was created with no provider and the
// Status card read "— · —" until the user re-picked the same provider from
// inside Chat. It also meant the choice never reached state.json, since
// chat is the only writer of that field.
func TestTheChosenProviderReachesALazilyLoadedWorkspace(t *testing.T) {
	f := lifecycleRootWithState(t, config.State{}, config.Config{}, "chat") // Provider nil: a first run
	f.root.Update(windowSize(100, 30))

	// What Settings does once a provider is picked.
	def := config.ProviderDefault{Provider: "demo", Model: "demo", ProjectID: "p1"}
	f.root.activeProvider = &def
	f.root.Update(ProviderDefaultSetMsg{Default: def})
	f.root.setOverlay(nil)

	f.root.switchWorkspace("chat")

	var got *config.ProviderDefault
	for _, m := range f.ws["chat"].msgs {
		if pm, ok := m.(ProviderDefaultSetMsg); ok {
			d := pm.Default
			got = &d
		}
	}
	if got == nil {
		t.Fatalf("the workspace was never told which provider was chosen; it got %d messages",
			len(f.ws["chat"].msgs))
	}
	if got.Provider != "demo" || got.Model != "demo" || got.ProjectID != "p1" {
		t.Fatalf("the workspace was told %+v, want the full choice including ProjectID", *got)
	}
}

// And it must arrive BEFORE the handshake, because the handshake is what
// starts the CreateSession chain that reads it. The other order creates the
// session first and then retrofits it over the wire.
func TestTheProviderArrivesBeforeTheHandshakeReplay(t *testing.T) {
	f := lifecycleRootWithState(t, config.State{}, config.Config{}, "chat")
	f.root.Update(windowSize(100, 30))
	def := config.ProviderDefault{Provider: "demo", Model: "demo"}
	f.root.activeProvider = &def
	f.root.conn.handshaked = true
	f.root.setOverlay(nil)

	f.root.switchWorkspace("chat")

	provIdx, connIdx := -1, -1
	for i, m := range f.ws["chat"].msgs {
		switch m.(type) {
		case ProviderDefaultSetMsg:
			if provIdx < 0 {
				provIdx = i
			}
		case protocol.ConnectedMsg:
			if connIdx < 0 {
				connIdx = i
			}
		}
	}
	if provIdx < 0 || connIdx < 0 {
		t.Fatalf("missing a replay: provider at %d, handshake at %d", provIdx, connIdx)
	}
	if provIdx > connIdx {
		t.Fatalf("the handshake (%d) arrived before the provider (%d)", connIdx, provIdx)
	}
}
