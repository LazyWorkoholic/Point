# Episode F14 — Editor Terminal ✅

**The Editor's Secondary rect is a real command runner now. One child at
a time, launched through PowerShell or `cmd` per `[terminal] shell`, with
stdout and stderr merged into one capped 5,000-line scrollback; a typed
line is forwarded to the running child's stdin and echoed inline on the
prompt line it answers; `↑`/`↓` recall history persisted per PROJECT in
`state.json`; PgUp/PgDn scroll; `ctrl+c` kills the whole process TREE,
not just the shell wrapper Point tracks. Beside it, `tea.ExecProcess` is
the hatch — reachable as a global action (leader `h`) from any workspace
including Chat, or as a `!`-prefixed line in the pane — and backend tasks
demonstrably keep running across the suspension. Three size modes
(`small`/`overlay`/`full`) cycle on `f8`, are remembered per WORKSPACE in
the Suspend blob, collapse while the code pane has focus and auto-expand
on background output without stealing a keystroke. No PTY, no VT100
emulator, no file-write path, nothing an agent can call.**

> **Verified by Aryan directly in the real running application, across
> many rounds, with a real keyboard and real child processes** — not by a
> synthetic harness standing in for it. That distinction earned itself:
> this episode shipped "done" three times on the strength of a green
> suite and was sent back each time by manual use, and every one of those
> rounds found a defect the suite could not see. The automated tests that
> exist now were written *after* each failure, mostly driving real
> `powershell`/`uv run python` children through the real pipe, because a
> synthetic test written by whoever wrote the bug agrees with the bug.
>
> **Three gaps in the task doc were raised and settled before any code
> was written**, per the episode's own instruction to stop rather than
> build through them:
>
> 1. **Done-when #8 (an SSH guest is refused by the backend) cannot be
>    tested.** B16 is not started and there is no guest/owner identity
>    anywhere in the backend — grepped; the only hit is a comment in
>    `managers\usage.py` recording that B14 declined to stub one.
>    Deferred, with nothing to remove later: see "The B16 gap" below.
> 2. **Password-style prompts do not work and cannot be made to.**
>    Windows' `getpass.getpass()` bypasses `sys.stdin` entirely. Measured
>    against CPython's own source, documented as a standing limitation —
>    see "The getpass blind spot".
> 3. **`\r`/`\b` progress redraws are not ANSI sequences** and would have
>    spammed the capped buffer. Fixed in `feed`, and the fix survived the
>    CRLF correction that came later.
>
> **Scope was also split out rather than absorbed.** Mouse scrollback and
> text selection are **F14b**, scheduled after B17 — see "F14b" below.

---

## Where the code went

No new package. The pane is one file in `editor\`; the hatch is one file
in `app\`, because only Root can hand Bubble Tea an `ExecProcess`.

```
frontend\
├── cmd\point\
│   ├── main.go                 + sanitizeConsoleInput() before app.New and
│   │                             tea.NewProgram; + Options.RestoreTerminal
│   │                             closure over the Program; + Shell from
│   │                             cfg.TerminalShell(). The "no third writer
│   │                             of state.json" comment is now wrong and
│   │                             says so: F14 is a deliberate third writer
│   ├── consolemode.go          NEW -- repairConsoleInputMode, pure and
│   │                             platform-independent so the rule is
│   │                             testable without a console
│   ├── consolemode_windows.go  NEW -- GetConsoleMode/SetConsoleMode via
│   │                             syscall.NewLazyDLL (no new dependency),
│   │                             every step VERIFIED by re-reading
│   └── consolemode_other.go    NEW -- no-op away from Windows
├── internal\
│   ├── app\
│   │   ├── hatch.go            NEW -- ProjectRootProvider, hatchDir,
│   │   │                         hatchShellCmd, openHatch,
│   │   │                         openExternalWindow, OpenHatchMsg,
│   │   │                         hatchDoneMsg
│   │   ├── contribute.go       + WorkspaceAddressed (a message that names
│   │   │                         its own workspace), + ProcessStopper
│   │   ├── workspace.go        + TerminalModeAware
│   │   ├── root.go             + restoreTerminal field/Option, the
│   │   │                         addressed-message route in the
│   │   │                         fall-through, OpenHatchMsg and
│   │   │                         hatchDoneMsg cases, terminalModeOf, and
│   │   │                         r.recompute() on EVERY message
│   │   ├── actions.go          + editor.hatch (leader h, unconditional),
│   │   │                         + ProcessStopper in doQuit
│   │   └── lifecycle.go        + ProcessStopper in unloadWorkspace
│   ├── chat\
│   │   └── stream.go           + WantsBackgroundReplies -- Chat had never
│   │                             implemented app.BackgroundReplies
│   ├── config\
│   │   ├── config.go           + [terminal] table (shell, external),
│   │   │                         TerminalShell(), and the cfg.Terminal
│   │   │                         assignment loadFrom was missing for its
│   │   │                         neighbour (see "The [editor] table" below)
│   │   └── state.go            + TerminalHistory map, SaveTerminalHistory,
│   │                             and TerminalHistory added to saveStateTo's
│   │                             keep-if-zero clause
│   ├── editor\
│   │   ├── terminal.go         NEW, ~1,600 lines with its reasoning --
│   │   │                         terminalPane, the runner, feed, the pull
│   │   │                         loop, history, modes, the renderer
│   │   ├── editor.go           + focusTerminal (the enum's third value,
│   │   │                         which F13a's own comment predicted),
│   │   │                         + term/termRect/shell fields, TermMode in
│   │   │                         the suspend blob, TerminalMode(),
│   │   │                         ProjectRoot(), the paste branch, and
│   │   │                         effectiveFocus narrowed to focusBuffer
│   │   ├── keys.go             + f8 (cycle), f9 (focus), the ctrl+c
│   │   │                         kill-vs-copy branch, terminal routing
│   │   ├── mouse.go            + click-to-focus on termRect
│   │   ├── protocol.go         + reloadTerminalHistory() in onProject
│   │   ├── render.go           - the F13 placeholder box
│   │   └── invoke.go           + terminalEventMsg on the background
│   │                             allow-list (DeliverToWorkspace)
│   └── layout\
│       └── layout.go           + TermSmallHeight = 6, replacing
│                                 MinBoxHeight in the editor's small branch
```

61 tests across ten files. The ones that matter most start real children:
`terminal_test.go` (18), `terminal_report2_test.go` (13, the clear-screen
family), `hatch_test.go` (13), `terminal_report3_test.go` (4),
`terminal_escape_test.go` (3), `consolemode_test.go` (3),
`terminal_cmdshell_test.go` (2), `background_finish_test.go` (2),
`terminal_config_test.go` (2), and `terminal_walkthrough_test.go` (1 — a
printing walkthrough whose output is the artefact).

---

## Tier 0: the design decisions that are not obvious from the spec

### `small` is 6 rows, not `MinBoxHeight`, and that diverges from LAYOUT.md's sketch

F1 left `secondaryHeight` returning `MinBoxHeight` (3) for the editor's
default. Three rows is two borders and **one** content row, and the pane
pins its input prompt to the bottom row — so `small` had the prompt and
nowhere to show a single line of what the command printed. LAYOUT.md and
F14 both say "small (3–4 rows), scrollable", and `TermSmallHeight = 6`
reads that as 3–4 rows **of terminal** (four content rows: three of
output plus the prompt) rather than a 3-row box with one usable row —
"scrollable" is meaningless for one row.

This makes the box taller than LAYOUT.md's own Editor sketch draws it.
Recorded as a deliberate divergence from the sketch, the same way F13's
`Cards()` diverged from the right-pane sketch and for the same kind of
reason: the proportions in the drawing do not survive the pane having a
job.

### History is per PROJECT; the size mode is per WORKSPACE

The task doc states both separately — "history … persisted per project"
and "the mode is remembered per workspace … travels in `Suspend()`" —
and they are genuinely different axes, so they are keyed differently.

The mode rides in editor's own `suspendBlob` (inside F12's
`WorkspaceState`, keyed by workspace id). History goes in a new
`state.json` map keyed by `terminalHistoryKey()`: `project:<id>` when a
project resolved, `dir:<path>` when none did.

**It has to be the project, not the workspace, because one editor
workspace's project can CHANGE during its own lifetime.** `onProject`
(`editor\protocol.go`) reassigns `projectID`/`projectRoot` from whatever
the active session resolves; F12 does not give one project one fixed
workspace id. Keyed by workspace, the pane would carry one project's
commands into another's the moment that binding moved. `onProject` now
calls `reloadTerminalHistory()`, which swaps the whole bucket.

`SaveTerminalHistory` is a **third writer** of `state.json`, alongside
chat's whole-struct snapshot and Root's `LastWorkspace`/`WorkspaceState`.
`saveStateTo`'s keep-if-zero clause was extended for it, exactly as it
was for `Holder` in F13b — without that, chat's next keystroke would
serialise a nil map over the history. `main.go`'s "there is still no
third writer" comment was true and is now wrong; it says so and explains
why routing history through main.go (as `Holder` is) would mean main.go
learning the project binding just to hand it back.

### `Cmd.Dir` falls back to the process's working directory

`onProject` sets `projectRoot` to `""` when no `.point\` is found above
the working directory — a legitimate, common state the tree already
reports as a note. The task doc's "working directory = project root"
assumes one always exists. Both Tier 0 and the hatch therefore fall back
to the process's own OS working directory, cached once (`term.cwd`)
because `terminalDir()` is called on every frame for the header, and
shown in the header either way so the user is never told a directory the
pane is not running in.

### Keys: `ctrl+o` was left alone

Extending `ctrl+o` into a three-way cycle was tried and **reverted**: it
broke four tests across three files that pin "ctrl+o from the tree
returns to the buffer" as the contract, and nothing in the task doc asks
for `ctrl+o`. `f9` is a dedicated focus toggle instead and `f8` cycles
the mode, keeping the editor's function-key block contiguous (f5
re-index, f6 take over the lock, f7 close the tab, f8 mode, f9 focus).
Neither is claimed by the chord registry, so neither needs a
`KeyCapturer` claim.

`ctrl+c` is the one existing binding this episode had to touch: it means
copy in this workspace **always** (`copy.go`'s deliberate divergence from
F15's spec). It now means kill *only* when the terminal has focus **and**
a command is actually running — checked narrowly so ctrl+c with nothing
running still copies, unchanged from every other state.

### `effectiveFocus` was narrowed, and had to be

It coerced any focus to `focusTree` whenever no document was open —
correct while buffer and tree were the only targets, and wrong for a
third pane that needs no open file to run a command. Cycling to the
terminal with nothing open bounced straight back to the tree on every
read. Now the coercion applies only to `focusBuffer`.

---

## The hatch, and the false lead that cost two rounds

### What was actually wrong: a successful suspend is indistinguishable from a crash

Reported as "pressing the hatch key exits the entire application
immediately", with real evidence: the `point` process still alive and
needing a force-kill, two `python` processes still running, and every
subsequent launch failing its handshake.

**Nothing was crashing.** `powershell -NoLogo` suppresses the banner by
definition, and its prompt is `PS D:\Projects\Point>` — character for
character what the parent shell was showing before Point started.
Releasing the terminal exits the alt screen, so the user's own prompt and
scrollback come back. Reproduced in a real console:

```
################ BEFORE: the bare shell the hatch used to run ################
Active code page: 65001
PS D:\Projects\Point>
```

Every piece of the reported evidence then fits: the live `point` pid is a
correctly **suspended** Point blocked in `c.Run()` waiting for that
nested shell to exit; the two `python` processes are the backend still
running, which is Done-when #5 working; and the handshake failures are
the suspended instance's backend still holding the single-instance lock.
Typing `exit` would have returned the TUI.

Fixed by making the nested shell unmistakable — it prints a notice and
takes its own prompt, in both shells:

```
  Point is suspended. Type exit to return to Point.

point>
```

### The single-instance lock message

Reproduced verbatim by running two Point instances against one profile:

```
Point is already running (pid 12264)
---- point backend exited: exit status 1
level=ERROR msg="backend unavailable" reason="the backend exited before the handshake"
  detail="Point is already running (pid 12264)"
```

**No code changed for this, and that is the honest record.** The reason
was already being carried and already rendered — `BannerDetail` reaches
the diagnostics screen (`app\placeholder.go:220`). What made it look
unexplained was the hatch, not the message: a user who cannot tell a
suspended Point from a crashed one relaunches, and the lock correctly
refuses. The fix is upstream, above. Ruled out along the way, each with
evidence: a stale lock (dead pids are taken over by design), an orphaned
backend (killing the frontend let the backend exit cleanly and remove its
own lock file), the scratch profile (the backend handshakes fine with
`APPDATA`/`LOCALAPPDATA` redirected), and a corrupt database (migrations
replayed `0 -> 8` cleanly).

### The console-mode fix: real, correctly built, and probably never the bug

Before the above, the hatch failure was root-caused to something else
entirely, a fix was built, and it was **claimed verified**. Then it was
disproven by Aryan's own evidence and retracted. Both halves belong on
the record.

The mechanism found was genuine and is reproducible: a Windows console
whose input mode has `ENABLE_ECHO_INPUT` set **without**
`ENABLE_LINE_INPUT` is a mode `SetConsoleMode` refuses with
ERROR_INVALID_PARAMETER. Bubble Tea saves the mode at startup and
restores it inside `ReleaseTerminal`, which is the **first** thing
`tea.ExecProcess` does; when that restore fails, bubbletea's `exec`
returns early — before launching the child and before `RestoreTerminal`
— leaving the input reader cancelled, the renderer stopped and the alt
screen already exited. Measured, with Point's own Bubble Tea options:

```
console input mode = 0x01F5
!! ECHO_INPUT without LINE_INPUT -- SetConsoleMode refuses this combination
!! SetConsoleMode(0x01F5) FAILED: The parameter is incorrect.

about to return tea.ExecProcess for: [powershell -NoLogo]
ExecProcess callback fired: err=error restoring console: The parameter is incorrect.
```

86ms, no child. The same binary in a fresh console launched the shell and
came back normally. A failing `SetConsoleMode` can also still CHANGE the
mode (`poisoning console: 0x01F7 -> 0x01F5 (err=The parameter is
incorrect.)`), which is how a console reaches an unrestorable state in
the first place — and Point's own Tier 0 `ctrl+c` (`taskkill /T /F` on a
console-owning child) is one way to get there.

**What disproved it as the user's bug** was the tab-vs-window question,
measured both directions: poisoning one Windows Terminal tab left a
second tab in the *same window* reading `0x01F7`, clean and restorable.
Consoles are per-tab. So a fresh tab really was fresh, the startup repair
had nothing to do (confirmed: no repair line in `frontend.log` on the
real binary's run), and the console-mode story cannot explain the
reported failure.

It is kept because it stands on its own merit: the failure mode is real,
catastrophic when hit (an alive, invisible, deaf application), and
sticky per console. `cmd\point\consolemode.go` repairs the mode before
bubbletea ever reads it, verifying by re-reading rather than trusting a
return value, and the fix was demonstrated end to end in a deliberately
poisoned console. **Any future episode touching raw console mode on
Windows should read that file before assuming `term.Restore` cannot
fail.** Two supporting pieces came from the same round and are also
worth keeping: a failed hatch now reports the real error instead of
claiming "back from the shell", and `Options.RestoreTerminal` gives Root
the only possible way back from a failed release.

A regression was also caught in that work before it shipped:
`Signal(syscall.Signal(0))` as a liveness probe is **wrong on Windows** —
measured `alive while Start-Sleep 20 runs: false` for a live process,
because Go rejects anything but Kill/Interrupt there. Shipped, ctrl+c
would never have killed anything and would have orphaned every child.
Replaced by `runMayStillReport()`, which reads the run's own `exitOnce`.

---

## Bugs found by manual use, with their causes

### Output was blank on every line — CRLF

The worst defect in the episode. `feed` treated **every** `\r` as a
progress redraw and cleared the pending line, so on Windows the CR of
every CRLF wiped the line and the LF committed an empty string. Every
line of every command's output became a blank row. Measured:

```
raw chunks: 4
  alpha
  <CR><LF>
  beta
  <CR><LF>
feed() as shipped produces these committed lines:
  [0] ""
  [1] ""
```

The reported "`pytest -q` shows blank lines then `[exit 1]`" was pytest's
own output, all of it, blanked one line at a time. Fixed with a
`pendingCR` flag that survives a **chunk boundary** — the CR and the LF
genuinely arrive in separate reads, so `strings.ReplaceAll("\r\n","\n")`
per chunk looks equivalent and silently fails.

### The `input()` answer rendered in the wrong order, then on the wrong line

An `input()` prompt is a PARTIAL line — `your name: ` with no newline,
held in `pendingRunes`. The first version appended the typed echo as its
own **committed** line, which landed above the still-open prompt, and the
child's reply then continued that prompt line: `» Srijan` followed by
`your name: hi Srijan`, with the typed name missing from the line it was
typed on. Appending to the pending line and committing it gives a real
terminal's order.

A second round found the live case still wrong: while typing, the prompt
and the in-progress text were on two rows, collapsing only on Enter. The
pending line is now excluded from the output rows and prefixes the
**input row**, so it reads `scanf`-style the whole time:

```
│ $ uv run python -c "name = input('your name: '); print('hi', name)"  │
│ your name: »srijan                                                   │
```

A prompt wider than the box drops its **head**, not its tail, so the
caret stays visible.

### The permanent "stopping" hang — two causes, one dead end

Reliably reproduced by `↑` then Enter with no pause. `running` is only as
current as the last **delivered** event, and delivery goes through Bubble
Tea; two keystrokes with nothing between them are shorter than that
window, so Enter read a stale `true`:

```
second finished, events UNDRAINED      running=true  queued=3
after up-arrow                         input="echo second"
after immediate Enter  <-- THE BUG     input=""  (no new line, gen unchanged)
```

The recalled command went down the stdin branch to a dead pipe, the write
failed, and the command was **lost** to a transient status line. The
plain-`hi` report was the same race reached by typing — `hi` in isolation
errors correctly and exits 1.

The second cause made it permanent. A run reports its exit exactly once
(`exitOnce`, which is right — the real `Wait` and the kill watchdog both
report and only the first may be applied). Once spent, nothing could move
the pane out of `running`:

```
forced stale running=true      running=true  killing=false run=set
after first ctrl+c             running=true  killing=true
after second ctrl+c            running=true  killing=true
after killGrace + drain        running=true  killing=true   <-- stuck
```

Three fixes: `catchUpTerminalState()` drains already-queued events before
anything branches on "is it running"; a failed stdin write is treated as
proof the child is gone and the line is run as the command it was; and
`killRunning` resolves locally (`resolveDeadRun`) when nothing can report
any more.

### `ctrl+c` killed only the shell wrapper

Tier 0 runs through a shell, so the tracked handle is the **wrapper** and
`pytest` is its grandchild. Killing the wrapper left the grandchild
holding the pipe write handles, so the pipes never reached EOF,
`cmd.Wait()` was never called, and no exit event was ever emitted —
measured: still no EOF six seconds after the kill. The second press then
hit an already-dead handle and produced `TerminateProcess: Access is
denied`, reported verbatim. Fixed with `taskkill /T /F` (base Windows, no
new dependency), a `sync.Once` so exactly one exit is applied, and a
`killGrace` watchdog so the pane can never sit on "running" indefinitely.
A ctrl+c now reports `[stopped]`, not the kill's incidental exit code.

### The stale elapsed counter in Chat — the same routing class

A `@research` task the tray correctly showed finishing in ~15s showed an
incrementing counter to `1:02` in the Chat transcript. **Chat had never
implemented `app.BackgroundReplies`**, so while it was not the active
workspace it received no envelopes at all; the task's completion never
reached `finish()`, the slot kept `statusRunning`, and
`renderTaskEntry` kept rendering `time.Since(startedAt)` from a start
time whose task was over. The later correction was a REHYDRATE from the
database on workspace switch, not the event arriving late.

Same class as the terminal pane's own dead pull loop — a completion
delivered only to whatever is on screen is a completion the owner can
miss — and it surfaced now because F14 makes leaving Chat while a task
runs the normal thing to do. Chat now answers `WantsBackgroundReplies()`
while anything is in flight; its commands stay default-denied, so only
the state change crosses.

### Long lines were cut, not wrapped

`uv run python cli.py --help` showed `…\python.exe:ca` and stopped. The
buffer held every character (verified against the raw chunks); the
renderer cut the row to the box width with no marker. **The error itself
was genuine** — `cli.py` lives in `backend\` and Tier 0 starts at the
project root — so the honest fix was to show it. The pane now wraps, by
display **cells**, deliberately NOT reusing `explainpanel.go`'s
word-wrapping `wrapCells`: that one collapses runs of spaces via
`strings.Fields`, which would shred a `--help` table.

### `cd` is stateless by design, and now says so

Measured: `cd ..` emits zero chunks, exits 0, changes nothing visible.
Every command is its own shell process with `Cmd.Dir` at the project
root, so `cd` changes that child's directory and the child exits. The
task doc specifies exactly this and asks for nothing persistent, so **no
persistent working directory was built**. The defect was the silence. A
bare `cd`/`chdir`/`Set-Location`/`pushd`/`popd` now answers in the pane
with the workaround, which genuinely works:

```
$ cd backend; uv run python cli.py --help
usage: cli.py [-h] [--verbose] …
```

A *chained* `cd` is not nagged about — that noise was a bug in the first
cut of this very fix.

### Status-bar overflow — mitigated, NOT diagnosed

Reported overflowing past the edge. **Not reproduced.** `statusBar`
already truncates at `gap < 1`, and `runewidth` reports 1 cell for
`—`/`·`/`…`/`»`, so the width arithmetic is self-consistent. Mitigated by
splitting the note: the pane (which wraps) gets the long form, the bar
gets `cd applies to this command only` — short and ASCII-only, since the
bar's fit is computed by `runewidth` and drawn by the terminal and the
two need not agree about a glyph. **Flagged as mitigation, not a fix.**
If it recurs the cause is elsewhere.

---

## The clear-screen saga, in three rounds

Worth recording in full, because the lesson is about the approach rather
than the bug.

**The fault.** `cls` sends **nothing** down the pipe — measured, zero
chunks — because PowerShell's `Clear-Host` writes through the console
API. It clears the console Point is drawing on, and Bubble Tea's renderer
only rewrites rows it believes changed, so the wiped rows stayed blank:
"borders and spacing disappear, recoverable only by cycling to full and
back" (a mode change forces a relayout, which is why that looked like a
cure).

**Round 1 — repaint after every command.** Correct and far too wide. A
full-screen blink after every `echo`, reported as worse than the fault.

**Round 2 — narrow by output volume, then by text.** First to "produced
no output at all", which still caught `$x = 5` and every other quiet
success. Then to a text classifier requiring a segment to be a single
token equal to `cls`/`clear`/`clear-host`. That stopped the blinking and
introduced a **false negative**.

**Round 3 — `clear all`.** Reported as the original corruption coming
back: borders gone, only a mode cycle, `cls` or `ctrl+l` recovering it.
The hypothesis offered was a first-word false positive causing a spurious
wipe. It was the opposite. `Clear-Host` is a FUNCTION with no declared
parameters —

```
(Get-Command Clear-Host).CommandType  -> Function
(Get-Command Clear-Host).Parameters   -> (none)
```

— so PowerShell drops extra positional arguments into `$args` and runs
the body anyway. In a real console:

```
MARKER-BEFORE-clearall          <- gone from the screen afterwards
DONE-clearall exit=0
```

`clear all` really clears and really exits 0; cmd.exe's builtin behaves
the same (`cmd /c "cls all"` → exit 0, cleared). So the `[exit 0]` was
honest and there was no error to surface. The single-token requirement
rejected it, so Point declined to repair the console it had just had
wiped, and declined to clear its own buffer to match. **Requiring an
exact match on the whole segment — the obvious reading — would have left
that exactly as broken**, which is why the final rule matches the command
**NAME**, whole and case-insensitively, with one evidence-backed
exclusion: a help request prints help and clears nothing —

```
MARKER-BEFORE-clearq            <- still on screen afterwards
NAME ...                        <- help text on stdout
```

so `-?`, `/?`, `-h`, `-help`, `--help` are excluded.

**And clearing now actually clears.** Round 1 and 2 only repainted; the
old lines stayed, so `cls` looked like a blink that did nothing. The wipe
happens at **start**, where a real terminal does it, so a chained
`cls; echo hi` leaves `hi` on a cleared pane. A line whose only job was
clearing leaves nothing at all — not its own echo, not an exit marker on
success — which is what makes the buffer genuinely empty:

```
before cls, 7 lines: ["$ 1..5 | ForEach-Object { … }" "line 1" … "[exit 0]"]
the instant cls starts, 0 lines: []
after cls exited, 0 lines: []  exitLine=""
```

A clear that **fails** still reports normally, because a silent failure
is a fault this episode was bitten by twice.

### The audit, both directions

**False positives: none.** Matching is on the whole first token against
`{cls, clear, clear-host}`, so `clear-variable` ≠ `clear`. The whole
family is table-asserted and one was run for real:

```
after Clear-Variable:
keep me
$ Clear-Variable nosuchvar -ErrorAction SilentlyContinue
[exit 1]
```

Buffer intact, no repaint, and an honest `[exit 1]` — PowerShell really
does fail that one. `Clear-Item`, `Clear-Content`, `Clear-RecycleBin`
are asserted alongside it.

**False negatives: an accepted, structural blind spot.**
`[Console]::Clear()`, or any child clearing through the console API, is
invisible to text matching. This is inherent to the no-PTY design this
episode explicitly chose — the only general fix is a PTY, which F14
rejects outright — and it is the same root cause as the `cls`-mid-run
caveat below. Not a gap this round could or should close. The failure
direction is at least the safe one: no wipe and no repaint, i.e. exactly
the pre-episode behaviour.

---

## Not fixed, and accepted as standing limitations

### `cls` mid-run

The wipe and the repaint happen at the start and exit of the command that
owns them. A long-running program that clears partway through leaves the
frame corrupted until it finishes, then self-repairs on the exit
repaint. Same family as the blind spot above.

### The getpass blind spot

On Windows, `getpass.getpass()` dispatches to `win_getpass`, which does
**not** read `sys.stdin`: it calls `msvcrt.getwch()`, reading the shared
console input buffer directly and bypassing the pipe Tier 0 writes typed
lines to. Point already owns that console in raw mode for its own keys,
so the child's read races a reader that is already blocked and loses. The
result is a **silent hang** — not an error, not a visible echo warning,
not a leak. Fixing it means emulating a console input source for the
child, which is PTY territory. Documented; `ctrl+c` is the way out of
one and the hatch (a real inherited console) is the way to avoid it.
Ordinary `input()` prompts are unaffected — those genuinely read the pipe.

### The B16 gap (Done-when #8)

The pane and the hatch are specified owner-only, enforced in the backend.
There is no guest/owner identity in the backend to test against, and
**nothing to remove later**: no code in `terminal.go` or `hatch.go`
branches on identity, so there is no client-side bypass for B16 to tear
out. The same pattern `editor\save.go` already follows for writes.
Done-when #8 is deferred, not passed.

### `[editor]` in config.toml has never been read — found, not fixed

While adding `[terminal]`, `loadFrom` turned out to decode the `[editor]`
table into `parsed` and then discard it — there is no `cfg.Editor`
assignment, so a hand-edited `autosave` key has never taken effect,
despite that struct's own doc claiming it is read. Untested (no
`config_test.go` case exercises it) and out of scope here. **F14's own
table does not repeat the mistake** — `cfg.Terminal = parsed.Terminal` is
asserted by a test precisely because its neighbour lacks one.

### `pytest -q` cannot pass as the task doc writes it

`pytest` is not installed in this repo's uv environment (`error: Failed
to spawn: pytest / program not found`), and the backend uses
`manual_*.py` scripts. The doc's own suggested check is unrunnable; use
`uv run python -c "…"` or `go build ./...` as the real command.

---

## F14b — split out, not absorbed

Mouse scrollback and text selection/copy in the pane is **new scope this
episode's Build section never asked for** (it specified `↑`/`↓` history
and PgUp/PgDn only). Spun out rather than smuggled in, after F14 had
already run long on defects in what it did specify. Scheduled **after
B17**, Aryan's call. See `design/tasks/frontend/F14b-terminal-mouse-selection.md`
and its rows in `INDEX.md` and `model_usage.md` — not re-described here.

---

## Manual Test Log

**Confirmed by Aryan in the real running application**, real keyboard,
real child processes, across the verification rounds:

| Behaviour | Result |
|---|---|
| `f8` and `f9` arrive on Windows | CONFIRMED — the two new function keys reach the workspace |
| The hatch from the Editor **and** from Chat | CONFIRMED — leader `h` works from both |
| Backend tasks survive the suspension | CONFIRMED — `@research` still running/completed after the hatch (Done-when #5) |
| The single-instance lock message | CONFIRMED — reproduced, and its reason was already on the diagnostics screen |
| Mode cycling, and persistence across focus change, unload and restart | CONFIRMED |
| The no-project fallback | CONFIRMED |
| Scrollback (PgUp/PgDn) and paste into the input line | CONFIRMED |
| The whole `clear` family | CONFIRMED — bare `cls`/`clear`/`Clear-Host` wipe; `clear all` wipes and repairs; `clear -?` does neither and shows its help; `Clear-Variable` left alone |

Alongside it, harness evidence against real children (`go test`, all
green under `-race`): output with CRLF intact, stderr interleaved, exit
codes including non-zero, `uv run python` through the real venv, an
`input()` prompt answered inline, 40 `\r` redraws collapsing to one line,
a tree-kill with `tasklist` confirming the grandchild is gone, the
`↑`+Enter race, the spent-run stick, the `cmd` shell path, per-project
history round-tripping through `state.json`, the 5,000-line cap, and
frame-width invariance with command echoes, CJK and emoji in the buffer.

Done-when, against the task doc's list:

1. **`pytest -q` / `python main.py` stream with a visible exit code.**
   PASS, re-scoped — `pytest` is not installed here (above); verified
   with real `uv run python` and PowerShell commands, exit codes shown
   in the header and the buffer.
2. **A script calling `input()` can be answered from the pane.** PASS —
   and it now renders inline, which took two rounds.
3. **`ctrl+c` kills the child, not Point.** PASS — the whole tree.
4. **The hatch opens a real shell; exiting restores the TUI with no
   artefacts.** PASS — and the shell now announces itself.
5. **A background task started before the hatch is still running after
   it.** PASS — confirmed in the real app.
6. **Mode cycling survives focus changes, unload and restart.** PASS.
7. **Output while focused elsewhere auto-expands without stealing keys.**
   PASS — keystrokes verified still reaching the buffer.
8. **As an SSH guest, both are refused by the backend.** DEFERRED to
   B16 — no identity exists to test against; see above.

---

## What the next episode inherits

### F17 (Web Preview) depends on F14

It now has a pane that runs commands and a hatch that hands over the real
terminal. Note `terminal.external = true` (`wt -d`) already exists as a
config-driven alternative, and `wt.exe` is present on this machine as an
app-execution alias.

### Anyone touching Root's message routing

`WorkspaceAddressed` is new and load-bearing: a message that names its
own workspace is delivered there whether or not it is active, and the
return trip reuses F15's `backgroundCmd` filter. The terminal's pull loop
depends on it — dropping one of its events does not lose a chunk, it ends
the pane's I/O for the session. `r.recompute()` now runs on **every**
message, because the layout is derived state and a workspace feeds it
(`TerminalMode`); recomputing only on resize is what made the mode key
look broken and the pane resize itself at random.

### Anyone adding a workspace that runs a local process

`ProcessStopper` is called from `unloadWorkspace` and `doQuit`. Nothing
on the backend cleans up a local child — the OS does not kill a child
because its parent stopped listening — so without it, quitting Point
leaves `npm run dev` running with no pane left to notice it from.

### The console-mode file

`cmd\point\consolemode.go` is the one piece of this episode whose bug was
real and whose relevance here was not. Read it before assuming
`term.Restore` cannot fail on Windows.
