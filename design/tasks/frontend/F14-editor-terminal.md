# F14 — Editor Terminal

**Phase:** P2 · **Batchable:** no · **Depends on:** F13 · **Backend:** —

**Cuttable.** Nothing else depends on it. If time is short, skip it.

## Purpose

See test and build output beside your code, and drop to a **real** shell when
something interactive is needed — without writing a terminal emulator.

## Build

### Tier 0 command runner
```
run one command; stream stdout and stderr interleaved; show the exit code
working directory = project root, shown in the pane header
stdin: a typed line is written to the running process (covers input() prompts)
ctrl+c kills the running process, NOT the app
history with ↑/↓, persisted per project in state.json
output buffer capped ~5k lines, older dropped
one command at a time; a second Enter while running is ignored with a hint
ANSI escape codes stripped on the way in
```

`go run`, `python main.py`, `pytest`, `npm run dev` all work — they are batch
commands. What Tier 0 **cannot** do is interactive programs (REPLs, `vim`, `top`).
That is what the hatch is for. Tier 1 (a PTY plus VT100 emulator, doubled by
Windows ConPTY) is **rejected**, not deferred.

Windows specifics: run commands through `cmd /c` or PowerShell explicitly rather
than assuming a POSIX shell, and make the choice configurable
(`terminal.shell = "powershell" | "cmd"`). `pytest` and `go` resolve via PATH
either way.

### The ExecProcess hatch
```
tea.ExecProcess(cmd)  → TUI suspends, the command owns the REAL terminal
                        (full TTY, colours, interactivity)
                      → on exit, the TUI restores exactly where it was
```
- Reachable **both** from the terminal pane (a key, or a `!` prefix) **and** as a
  registered global action available from any workspace, including Chat.
- `Cmd.Dir` = project root, so no `cd` is needed; `cd ..` inside behaves normally
  because it is a real shell.
- Default command is `$env:COMSPEC` or PowerShell, per config.
- **Backend tasks keep running throughout.** Only Bubble Tea's input and render
  loop pause; the reader goroutine keeps draining events, and the Python runtime
  is a separate process. No gating, no "wait for tasks" dialog.
- On return: **full repaint** so nothing the command printed is left behind, and
  any task that completed during the suspension gets a subtle tray marker rather
  than a missed toast.

### External window option
`terminal.external = true` opens the user's own terminal instead of suspending:
```powershell
wt -d "D:\Projects\Point"          # Windows Terminal
```
Config-driven, off by default — `ExecProcess` is the better default because it
needs no per-platform command and cannot fail to find an emulator.

### Three size modes
```
small     3–4 rows, scrollable                       default
overlay   grows upward over Code File to about half
full      fills the left column entirely
```
One key cycles. **The mode is remembered per workspace** and restored on refocus,
and travels in `Suspend()`. Switching focus to the code pane collapses to
`small`; returning restores the remembered mode.

**Auto-expand to `overlay`** when a command produces output while focus is in the
code pane — with a subtle indicator, **never stealing focus**. Otherwise you run
`pytest`, keep typing, and never notice it failed.

### Permissions
The terminal pane and the hatch are **owner-only**. An SSH guest gets neither, and
the refusal is enforced in the backend, not by hiding the UI (B16).

## Do NOT

- Implement a PTY or VT100 emulator.
- Give agents command execution — agents never touch this pane.
- Run more than one command at a time.
- Block the hatch on running tasks.

## Done when

1. `pytest -q` and `python main.py` run and stream output with a visible exit
   code.
2. A script calling `input()` can be answered from the pane.
3. `ctrl+c` kills the child, not Point.
4. The hatch opens a real shell; an interactive program works inside it; exiting
   restores the TUI with no artefacts.
5. A background task started before the hatch is still running after it, with its
   events applied on return.
6. Mode cycling works, and the mode survives focus changes, unload, and restart.
7. Output while focused elsewhere auto-expands to `overlay` without stealing keys.
8. As an SSH guest, both the pane and the hatch are refused by the backend.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the editor's terminal pane:
```
pytest -q
```
**Expect:** streamed output, an exit code, and `↑` recalling it.

Send `@research <topic>` in Chat, then open the hatch, run something interactive,
and exit.
**Expect:** the research task still running or completed.
**Fails if:** it died, or the terminal is left broken after exiting.

Cycle the modes, switch to the code pane, switch back.
**Expect:** the remembered mode returns.

Start a long command, focus the code pane while it runs.
**Expect:** auto-expand to overlay, and your keystrokes still go to the code pane.

## Handoff

State after this episode: the editor can run things and hand over to a real shell,
with no emulator written and no platform-specific PTY code.

Record: which shell you configured, and whether `ExecProcess` restored Windows
Terminal cleanly every time.

Do not proceed until every check above passes.
