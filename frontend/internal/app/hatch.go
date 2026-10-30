package app

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/config"
)

// F14's ExecProcess hatch. Reachable two ways: the editor.hatch action
// registered below (any workspace, including Chat -- see actions.go), and
// the terminal pane's own `!` prefix (editor\terminal.go calls back into
// this package's exported Root method the same way).
//
// Both routes end up here because both need the same three things: where
// to run (hatchDir), what shell to run it in (config, not a constant --
// Windows has no POSIX shell to assume), and bubbletea's own ExecProcess
// plumbing, which only Root can return a tea.Cmd for -- a workspace's
// Update return value is wrapped by storeCurrent before Root ever sees it,
// so a workspace cannot hand back a raw ExecProcess Cmd and expect Root's
// own dispatchKey path to carry it unchanged. editor.Workspace instead
// returns an openHatchMsg (terminal.go) that Root's Update turns into the
// real tea.ExecProcess Cmd via this file.

// ProjectRootProvider is implemented by a workspace that has resolved a
// project root -- today only the Editor (F13). hatchDir asks for it the
// same narrow-interface way ThemeAware and OverlayProvider are asked,
// rather than reaching into editor.Workspace's fields directly, so Root
// stays ignorant of what a workspace holds.
type ProjectRootProvider interface {
	ProjectRoot() string
}

// hatchDir is Cmd.Dir for the hatch, and (via the same narrow interface)
// for F14's Tier 0 runner too: the LOADED Editor workspace's resolved
// project root if there is one, whether or not the Editor is the ACTIVE
// workspace -- the hatch is reachable from Chat, which never resolves a
// project of its own -- or the process's own OS working directory
// otherwise.
//
// An empty ProjectRoot() is not an error to route around: editor\
// protocol.go's onProject already treats "no .point\ above the working
// directory" as an ordinary, common answer, not a failure, and this
// fallback is the same call made for the same reason one layer up.
func (r *Root) hatchDir() string {
	if ws, ok := r.workspaces[WorkspaceEditor]; ok {
		if pr, ok := ws.(ProjectRootProvider); ok {
			if root := pr.ProjectRoot(); root != "" {
				return root
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// hatchShellCmd builds the shell command for ExecProcess (or the external
// window) -- F14's "default command is $env:COMSPEC or PowerShell, per
// config", with an optional one-shot command appended. A non-empty command
// is run with the shell left OPEN afterwards (`-NoExit` / `cmd /k`)
// rather than exiting the instant it finishes: the whole reason to reach
// for the hatch over Tier 0 is a program that needs a real console for the
// REST of its life (an interactive installer step, a REPL), and a shell
// that vanished the moment that program's first line ran would be no hatch
// at all.
// # The nested shell announces itself, and that is the actual bug fix
//
// The first version ran a BARE `powershell -NoLogo`, and that combination
// is indistinguishable from having crashed:
//
//   - releasing the terminal exits the alt screen, so the user's own shell
//     prompt and scrollback come straight back;
//   - `-NoLogo` suppresses PowerShell's banner, by definition;
//   - the child's prompt is `PS D:\Projects\Point>` -- character for
//     character what the PARENT shell was showing before Point started.
//
// So the screen after a perfectly successful hatch is: no TUI, and a
// prompt that looks exactly like the one you launched Point from. It was
// reported as "pressing the hatch key exits the entire application", and
// the evidence offered for that was Point still being alive and needing a
// force-kill -- which is precisely what a correctly SUSPENDED Point looks
// like while it waits in c.Run() for this shell to exit. Nothing was
// crashing; the hatch gave no way to tell it had worked, and no hint that
// `exit` was what came next.
//
// Both shells therefore print a notice and take a DISTINCT prompt, so the
// nested shell cannot be mistaken for the parent one. That is also what
// stops the follow-on failure: a user who does not know Point is still
// running launches it again, and the second instance's backend refuses on
// the single-instance lock with "Point is already running (pid N)" --
// reproduced exactly, and the reason that error appeared on "every
// subsequent launch".
const (
	hatchNotice = "Point is suspended. Type exit to return to Point."

	// PowerShell: the notice, then a prompt function, so every line the
	// user sees in here says `point>` instead of `PS <dir>>`.
	hatchPwshPrelude = "Write-Host ''; " +
		"Write-Host '  " + hatchNotice + "' -ForegroundColor Cyan; " +
		"Write-Host ''; " +
		"function prompt { 'point> ' }"

	// cmd.exe: the same two things in its own dialect. $G is `>`.
	hatchCmdPrelude = "prompt point$G & echo. & echo   " + hatchNotice + " & echo."
)

func hatchShellCmd(dir, shell, command string) *exec.Cmd {
	var c *exec.Cmd
	switch shell {
	case config.ShellCmd:
		line := hatchCmdPrelude
		if command != "" {
			line += " & " + command
		}
		c = exec.Command("cmd", "/k", line)
	default: // PowerShell, and config.TerminalShell has already normalised
		// anything unrecognised to this branch.
		line := hatchPwshPrelude
		if command != "" {
			line += "; " + command
		}
		// -NoExit on BOTH paths now: the prelude is itself a -Command, so
		// without it the shell would run the notice and exit immediately.
		c = exec.Command("powershell", "-NoLogo", "-NoExit", "-Command", line)
	}
	c.Dir = dir
	return c
}

// hatchDoneMsg is ExecProcess's own completion callback, wrapped so
// root.go's Update can see it arrive without importing os/exec itself.
type hatchDoneMsg struct{ err error }

// openHatch suspends Point (unless `terminal.external` says otherwise) and
// hands the real terminal to a shell. Backend tasks keep running
// throughout: tea.ExecProcess's own exec() (bubbletea's exec.go) blocks
// only the Program's event-processing goroutine -- the one that also
// drains tea.Tick and protocol.Client's Next() pull-loop -- never the
// Python process or the dedicated reader goroutine reading its stdout,
// which are a separate OS process and a separate goroutine neither of
// which that blocked goroutine touches. Any event already in flight when
// the hatch opens queues (blocked on an unbuffered channel send) and
// delivers the moment the shell exits and the loop resumes; nothing is
// dropped and nothing floods, because each tick/pull loop only ever has
// one such send outstanding at a time.
func (r *Root) openHatch(command string) tea.Cmd {
	if r.cfg.Terminal.External {
		return r.openExternalWindow(command)
	}
	c := hatchShellCmd(r.hatchDir(), r.cfg.TerminalShell(), command)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return hatchDoneMsg{err: err}
	})
}

// openExternalWindow is `terminal.external = true`: a real Windows
// Terminal window in the project root, left running on its own rather than
// suspending this process. Off by default, per F14's own reasoning:
// ExecProcess needs no per-platform emulator lookup and cannot fail to
// find one, which `wt` can if it is not installed.
// The shell line is built by hatchShellCmd, the SAME function the
// suspending path uses, rather than assembled again here.
//
// It was assembled again here, and it was wrong for one of the two
// supported shells: it appended PowerShell's own `-NoExit -Command` flags
// after whatever `terminal.shell` named, so `shell = "cmd"` produced
// `wt -d <dir> cmd -NoExit -Command <line>` -- flags cmd.exe does not
// have, where it needs `/k`. Found by review while testing this path, not
// in use; the fix is to have one place that knows how each shell takes a
// command.
func (r *Root) openExternalWindow(command string) tea.Cmd {
	dir := r.hatchDir()
	shell := hatchShellCmd(dir, r.cfg.TerminalShell(), command)
	// shell.Args[0] is the shell's own name, which is exactly what wt
	// wants as the command for the new tab.
	args := append([]string{"-d", dir}, shell.Args...)

	c := exec.Command("wt", args...)
	if err := c.Start(); err != nil {
		// `wt` is an app-execution alias under %LOCALAPPDATA%, so it is
		// present on most Windows 11 machines and absent on plenty of
		// Windows 10 ones. Naming the error rather than swallowing it is
		// the whole reason ExecProcess is the default.
		return status("could not open Windows Terminal: " + err.Error())
	}
	return status("opened in Windows Terminal")
}

// OpenHatchMsg is how a workspace (the Editor's terminal pane) ASKS for
// the hatch, rather than returning a raw tea.ExecProcess Cmd itself -- see
// this file's header comment for why a workspace cannot do that directly.
// Exported so editor.Workspace can construct one; Root's Update (root.go)
// turns it into the real openHatch call the instant it sees one.
type OpenHatchMsg struct{ Command string }
