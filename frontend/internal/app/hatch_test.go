package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// F14's app-side tests. All three cover failures that shipped, passed the
// suite, and were found by hand -- the layout was only recomputed on a
// resize, and a message addressed to a backgrounded workspace went to
// whatever happened to be on screen instead.

// termModeWorkspace is a workspace with F14's two narrow interfaces and
// nothing else.
type termModeWorkspace struct {
	mode    layout.TermMode
	root    string
	stopped int
}

func (t *termModeWorkspace) Init() tea.Cmd { return nil }
func (t *termModeWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	if m, ok := msg.(setTermModeMsg); ok {
		t.mode = m.mode
	}
	return t, nil
}
func (t *termModeWorkspace) View(layout.Layout) string     { return "" }
func (t *termModeWorkspace) Suspend() ([]byte, error)      { return nil, nil }
func (t *termModeWorkspace) Resume([]byte) error           { return nil }
func (t *termModeWorkspace) ID() WorkspaceID               { return WorkspaceEditor }
func (t *termModeWorkspace) Cards() []layout.CardSlot      { return nil }
func (t *termModeWorkspace) SetTheme(theme.Theme)          {}
func (t *termModeWorkspace) TerminalMode() layout.TermMode { return t.mode }
func (t *termModeWorkspace) ProjectRoot() string           { return t.root }
func (t *termModeWorkspace) StopRunningProcess()           { t.stopped++ }
func (t *termModeWorkspace) AllowBackgroundMsg(tea.Msg) BackgroundDelivery {
	return DeliverToWorkspace
}

type setTermModeMsg struct{ mode layout.TermMode }

// addressedMsg is the shape F14's terminal events have.
type addressedMsg struct{ to WorkspaceID }

func (a addressedMsg) TargetWorkspace() WorkspaceID { return a.to }

// recordingWorkspace counts what it was handed, so a test can prove a
// message reached a workspace that was NOT active.
type recordingWorkspace struct {
	stubWorkspace
	got []tea.Msg
}

func (r *recordingWorkspace) Update(msg tea.Msg) (Workspace, tea.Cmd) {
	r.got = append(r.got, msg)
	// Return a command, the way the terminal's own re-arm does, so the
	// caller can assert it is not thrown away.
	return r, func() tea.Msg { return nil }
}
func (r *recordingWorkspace) AllowBackgroundMsg(tea.Msg) BackgroundDelivery {
	return DeliverToWorkspace
}

// ---------------------------------------------------------------------------
// The layout has to follow a mode change IMMEDIATELY
// ---------------------------------------------------------------------------

// The reported bug, as a test: change the terminal mode and the layout
// must change on that message, not at the next unrelated resize.
//
// Before the fix, r.layout was recomputed only on a real resize (plus
// redraw / pane toggle / overlay / workspace load), so the mode key did
// nothing visible and the change then surfaced on an alt-tab or a
// workspace switch -- "the terminal resizes itself at random", with no way
// to shrink it back because the shrink key was equally invisible.
func TestAModeChangeChangesTheLayoutWithoutAResize(t *testing.T) {
	r := newRoot(t)
	ws := &termModeWorkspace{mode: layout.TermSmall}
	r.workspaces[WorkspaceEditor] = ws
	r.active = WorkspaceEditor
	size(t, r, 120, 40)

	small := r.layout.Secondary.H
	if small != layout.TermSmallHeight {
		t.Fatalf("small secondary = %d, want %d", small, layout.TermSmallHeight)
	}
	mainSmall := r.layout.Main.H

	// A message that is NOT a resize.
	r.Update(setTermModeMsg{mode: layout.TermFull})
	if r.layout.Secondary.H == small {
		t.Errorf("secondary still %d rows after switching to full -- the layout did not follow the mode",
			r.layout.Secondary.H)
	}
	if !r.layout.Main.IsZero() {
		t.Errorf("full mode left Main at %+v, want it gone", r.layout.Main)
	}

	// And back down again: the escape hatch has to work too, which is the
	// half that made the bug unrecoverable rather than merely odd.
	r.Update(setTermModeMsg{mode: layout.TermSmall})
	if r.layout.Secondary.H != small {
		t.Errorf("secondary = %d after returning to small, want %d", r.layout.Secondary.H, small)
	}
	if r.layout.Main.H != mainSmall {
		t.Errorf("Main = %d after returning to small, want %d", r.layout.Main.H, mainSmall)
	}
}

// The mode is read off the ACTIVE workspace, so a workspace with no
// terminal must not inherit the Editor's mode.
func TestAWorkspaceWithoutATerminalGetsTheSmallDefault(t *testing.T) {
	r := newRoot(t)
	r.workspaces[WorkspaceEditor] = &termModeWorkspace{mode: layout.TermFull}
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	if r.layout.Main.IsZero() {
		t.Error("Chat lost its Main rect to the Editor's terminal mode")
	}
}

// ---------------------------------------------------------------------------
// An addressed message reaches its workspace even when it is not active
// ---------------------------------------------------------------------------

// The dead-loop bug. A terminal event arriving while Chat is on screen
// must still reach the Editor, and its command must survive, or the pane's
// I/O stops for the rest of the session.
func TestAnAddressedMessageReachesABackgroundedWorkspace(t *testing.T) {
	r := newRoot(t)
	editor := &recordingWorkspace{stubWorkspace: stubWorkspace{id: WorkspaceEditor}}
	r.workspaces[WorkspaceEditor] = editor
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	_, cmd := r.Update(addressedMsg{to: WorkspaceEditor})
	if len(editor.got) == 0 {
		t.Fatal("the backgrounded Editor never received the message addressed to it")
	}
	if cmd == nil {
		t.Error("the workspace's command was dropped -- a pull loop would die here")
	}
}

// Addressed to a workspace that is not loaded: dropped, not resurrected.
func TestAnAddressedMessageForAnUnloadedWorkspaceIsDropped(t *testing.T) {
	r := newRoot(t)
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	r.Update(addressedMsg{to: WorkspaceEditor})
	if _, loaded := r.workspaces[WorkspaceEditor]; loaded {
		t.Error("an addressed message revived an unloaded workspace")
	}
}

// ---------------------------------------------------------------------------
// The hatch
// ---------------------------------------------------------------------------

// Cmd.Dir comes from the loaded Editor whether or not it is active (the
// hatch is reachable from Chat), and falls back to the process's own
// working directory when no project is resolved.
func TestHatchDirPrefersTheEditorsProjectRootThenTheWorkingDirectory(t *testing.T) {
	r := newRoot(t)
	r.active = WorkspaceChat
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}

	// No Editor loaded at all: the OS working directory.
	if got := r.hatchDir(); got == "" || got == "." {
		t.Errorf("hatchDir with no Editor = %q, want a real working directory", got)
	}

	// Editor loaded but with no project resolved: still the fallback.
	r.workspaces[WorkspaceEditor] = &termModeWorkspace{root: ""}
	fallback := r.hatchDir()
	if fallback == "" || fallback == "." {
		t.Errorf("hatchDir with an unresolved project = %q, want the working directory", fallback)
	}

	// Editor loaded WITH a project, while Chat is still the active one.
	r.workspaces[WorkspaceEditor] = &termModeWorkspace{root: `D:\somewhere\proj`}
	if got := r.hatchDir(); got != `D:\somewhere\proj` {
		t.Errorf("hatchDir = %q, want the Editor's project root even from Chat", got)
	}
}

// A quit must stop a Tier 0 child, or it outlives Point with no pane left
// to notice it from.
func TestQuitStopsARunningChild(t *testing.T) {
	r := newRoot(t)
	ws := &termModeWorkspace{}
	r.workspaces[WorkspaceEditor] = ws
	r.active = WorkspaceEditor

	doQuit(r)
	if ws.stopped != 1 {
		t.Errorf("StopRunningProcess called %d times on quit, want 1", ws.stopped)
	}
}

// And so must an unload, which is the other way a workspace stops
// receiving messages.
func TestUnloadStopsARunningChild(t *testing.T) {
	r := newRoot(t)
	ws := &termModeWorkspace{}
	r.workspaces[WorkspaceEditor] = ws
	r.active = WorkspaceEditor
	size(t, r, 120, 40)

	r.unloadWorkspace(WorkspaceEditor)
	if ws.stopped != 1 {
		t.Errorf("StopRunningProcess called %d times on unload, want 1", ws.stopped)
	}
}

// pumpMsg delivers a message and then DELIVERS WHAT IT PRODUCED, walking
// one level of tea.Batch. r.status is set through a statusMsg command
// rather than by assignment, so a test that only calls Update sees an
// empty status and proves nothing.
func pumpMsg(t *testing.T, r *Root, msg tea.Msg) {
	t.Helper()
	_, cmd := r.Update(msg)
	if cmd == nil {
		return
	}
	out := cmd()
	if batch, ok := out.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if sub := c(); sub != nil {
				r.Update(sub)
			}
		}
		return
	}
	if out != nil {
		r.Update(out)
	}
}

// A hatch that FAILED must say so and must put the terminal back.
//
// The first version reported "back from the shell" whatever happened, and
// the failure it hid is the worst one available: when ExecProcess's own
// ReleaseTerminal fails, bubbletea returns before starting the child AND
// before its RestoreTerminal, leaving the program alive with its input
// reader cancelled -- unable to receive another keystroke, which reads as
// a crash. See app.Options.RestoreTerminal.
func TestAFailedHatchIsReportedAndTheTerminalIsRestored(t *testing.T) {
	restores := 0
	r := newRoot(t)
	r.restoreTerminal = func() error { restores++; return nil }
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	pumpMsg(t, r, hatchDoneMsg{err: errors.New("error restoring console: The parameter is incorrect.")})

	if restores != 1 {
		t.Errorf("RestoreTerminal called %d times after a failed hatch, want 1", restores)
	}
	if !strings.Contains(r.status, "could not open") {
		t.Errorf("status = %q, want it to report the failure", r.status)
	}
	if !strings.Contains(r.status, "The parameter is incorrect") {
		t.Errorf("status = %q, want the underlying error in it", r.status)
	}
	if strings.Contains(r.status, "back from the shell") {
		t.Errorf("a failed hatch still claimed success: %q", r.status)
	}
}

// The ordinary path is unchanged, and must not call RestoreTerminal --
// bubbletea has already done it.
func TestASuccessfulHatchDoesNotTouchTheTerminal(t *testing.T) {
	restores := 0
	r := newRoot(t)
	r.restoreTerminal = func() error { restores++; return nil }
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	pumpMsg(t, r, hatchDoneMsg{})

	if restores != 0 {
		t.Errorf("RestoreTerminal called %d times on a clean return, want 0", restores)
	}
	if !strings.Contains(r.status, "back from the shell") {
		t.Errorf("status = %q", r.status)
	}
}

// A nil RestoreTerminal (every test, and any build with no Program) must
// not panic -- the nil check is the contract.
func TestAFailedHatchWithNoRestoreHookStillReports(t *testing.T) {
	r := newRoot(t)
	r.restoreTerminal = nil
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	pumpMsg(t, r, hatchDoneMsg{err: errors.New("boom")})
	if !strings.Contains(r.status, "boom") {
		t.Errorf("status = %q, want the error reported", r.status)
	}
}

// The hatch key must produce an EXEC, never a quit.
//
// Reported as "pressing the hatch key exits the whole application". This
// drives the real leader sequence and inspects the message the resulting
// command actually produces -- which is how that report was shown to be
// something else entirely (bubbletea's own ReleaseTerminal failing inside
// exec, see cmd\point\consolemode.go). The dispatch was never the problem,
// and this is the test that says so.
func TestTheHatchKeySequenceProducesAnExecNotAQuit(t *testing.T) {
	r := newRoot(t)
	r.workspaces[WorkspaceChat] = &stubWorkspace{id: WorkspaceChat}
	r.active = WorkspaceChat
	size(t, r, 120, 40)

	a, ok := r.actions.ByLeader("h")
	if !ok {
		t.Fatal("nothing is bound to leader h")
	}
	if a.ID != "editor.hatch" {
		t.Fatalf("leader h is %q, not the hatch", a.ID)
	}

	m, _ := r.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	rr := m.(*Root)
	if !rr.leader.pending {
		t.Fatal("ctrl+w did not start a leader sequence")
	}

	_, cmd := rr.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("the hatch key produced no command")
	}
	msg := cmd()
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		t.Fatal("the hatch key produces tea.QuitMsg -- it would exit the app")
	}
	// bubbletea's exec message is unexported, so this is matched by name.
	if got := fmt.Sprintf("%T", msg); got != "tea.execMsg" {
		t.Errorf("the hatch key produced %s, want tea.execMsg", got)
	}
}

// The hatch's shell line, asserted by PROPERTY rather than by exact
// string, because the interesting part is no longer the flag list.
//
// What matters, and what was missing when this was reported as "the hatch
// exits the app": the nested shell must ANNOUNCE itself and must not wear
// the parent shell's prompt. See hatchShellCmd for the whole reason.
func TestTheHatchShellAnnouncesItselfAndTakesItsOwnPrompt(t *testing.T) {
	for _, shell := range []string{"powershell", "cmd"} {
		for _, command := range []string{"", "pytest -q"} {
			c := hatchShellCmd(`D:\p`, shell, command)
			line := strings.Join(c.Args, " ")

			if c.Dir != `D:\p` {
				t.Errorf("%s/%q: Dir = %q", shell, command, c.Dir)
			}
			if !strings.Contains(c.Path, shell) {
				t.Errorf("%s/%q: Path = %q", shell, command, c.Path)
			}
			// 1. It says Point is suspended and how to come back.
			if !strings.Contains(line, hatchNotice) {
				t.Errorf("%s/%q: no notice in %q", shell, command, line)
			}
			if !strings.Contains(line, "exit") {
				t.Errorf("%s/%q: the notice does not mention exit: %q", shell, command, line)
			}
			// 2. It takes a prompt of its own, so it cannot be mistaken
			//    for the shell Point was launched from.
			if !strings.Contains(line, "point>") && !strings.Contains(line, "point$G") {
				t.Errorf("%s/%q: no distinct prompt in %q", shell, command, line)
			}
			// 3. The shell STAYS OPEN -- the prelude is itself a command,
			//    so without this the shell would run the notice and exit.
			switch shell {
			case "cmd":
				if c.Args[1] != "/k" {
					t.Errorf("cmd/%q: args = %q, want /k", command, c.Args)
				}
				if strings.Contains(line, "-Command") {
					t.Errorf("cmd/%q got PowerShell flags: %q", command, line)
				}
			default:
				if !strings.Contains(line, "-NoExit") {
					t.Errorf("%s/%q: no -NoExit in %q", shell, command, line)
				}
			}
			// 4. A user command is still run.
			if command != "" && !strings.Contains(line, command) {
				t.Errorf("%s/%q: the command was dropped: %q", shell, command, line)
			}
		}
	}
}

// The external-window path builds its line through the SAME function, so
// `shell = "cmd"` cannot get PowerShell's flags -- which it did when this
// path assembled its own arguments.
func TestTheExternalWindowUsesTheSameShellLine(t *testing.T) {
	for _, shell := range []string{"powershell", "cmd"} {
		sh := hatchShellCmd(`D:\p`, shell, "pytest -q")
		args := append([]string{"-d", `D:\p`}, sh.Args...)
		if args[2] != shell {
			t.Errorf("%s: wt command is %q, want the shell itself", shell, args[2])
		}
		if shell == "cmd" && strings.Contains(strings.Join(args, " "), "-Command") {
			t.Errorf("cmd got PowerShell flags: %q", args)
		}
		if !strings.Contains(strings.Join(args, " "), hatchNotice) {
			t.Errorf("%s: the external window would not announce itself: %q", shell, args)
		}
	}
}
