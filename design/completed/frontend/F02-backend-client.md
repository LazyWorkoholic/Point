# Episode F2 — Backend Client ✅

**Go and Python are talking, the envelope version is verified at the handshake,
and killing the backend mid-stream is survivable.**

Built and verified in one session together with F1 (`batchable: yes`).

---

## `frontend\internal\protocol\` — the whole episode

| File | Lines | What |
|---|---|---|
| `envelope.go` | 93 | the wire format |
| `types.go` | 150 | event-type constants + classification |
| `transport.go` | 398 | `StdioTransport`, the three transport rules |
| `client.go` | 807 | handshake, correlation, watchdog, recovery ladder |
| `spawn.go` | 109 | backend-directory resolution, `exec.Cmd` construction |
| `spawn_windows.go` | 21 | `CREATE_NO_WINDOW` |
| `spawn_other.go` | 11 | no-op, so `go vet` passes off Windows |
| `id.go` | 21 | envelope IDs without a UUID dependency |

Tests: `envelope_test.go`, `transport_test.go`, `client_test.go`,
`live_test.go` — **49 test functions**, of which 4 are live and skipped unless
`POINT_LIVE_BACKEND=1`.

## The envelope — exact Go names

```go
const Version = 1   // the ENVELOPE version, from backend\runtime\events.py

type Envelope struct {
    V            int             `json:"v"`
    ID           string          `json:"id"`
    Type         string          `json:"type"`
    CorrID       string          `json:"corr_id"`
    SessionID    string          `json:"session_id"`
    TaskID       string          `json:"task_id"`
    ConnectionID string          `json:"connection_id"`
    TraceID      string          `json:"trace_id"`
    Seq          *int            `json:"seq"`
    TS           float64         `json:"ts"`
    Payload      json.RawMessage `json:"payload"`
}

func Decode(line []byte) (Envelope, error)
func Encode(e Envelope) ([]byte, error)        // newline included
func (e Envelope) UnmarshalPayload(v any) error
func (e Envelope) SeqOr(fallback int) int
```

Two shape decisions worth knowing before writing a consumer:

- **The nullable metadata fields are plain `string`, not `*string`.** Python
  writes JSON `null` for every one it does not set, and `encoding/json` decodes
  `null` into a string as a no-op, so `""` and `null` are the same absence.
- **`Seq` IS a pointer**, because `0` is a value a sequence number can take and
  "absent" has to stay distinguishable from "the first one". Use `SeqOr`.

`Payload` is `json.RawMessage` — **F2 does not interpret payloads.** Each
consumer unmarshals its own.

### Classification helpers

```go
func IsTerminal(eventType string) bool         // TaskDone | TaskFailed | TaskCancelled
func IsDroppable(eventType string) bool        // TaskToken | TextDelta | ThinkingDelta
func IsConnectionLevel(eventType string) bool  // hello | ping
```

`task.cancel.accepted` is **not** terminal — it is the ack for `CancelTask`, and
the sink still emits `TaskCancelled` afterwards. Before B6 the ack *was* a
`TaskCancelled`, which made "exactly one terminal event per task" false.

Event names are plain string constants (`EvTaskDone`, `CmdSendMessage`, …), not
an enum: an unknown type must be logged and ignored, never crash, so a closed
set buys nothing. `TestUnknownTypeDecodes` asserts it.

## The `Transport` interface — exact

```go
type Transport interface {
    Send(Envelope) error
    Recv() <-chan Envelope
    Close() error
}
```

The seam exists so a socket implementation is a drop-in. **`cmd.Stdin` appears
in exactly one file.** `StdioTransport` implements it, plus:

```go
func (t *StdioTransport) Exited() <-chan struct{}
func (t *StdioTransport) ExitError() error
func (t *StdioTransport) StderrTail() string
func (t *StdioTransport) Dropped() int64
```

The client drives a slightly wider interface, so tests can supply a backend with
no process behind it:

```go
type Backend interface {
    Transport
    Exited() <-chan struct{}
    ExitError() error
    StderrTail() string
}

type Dialer func() (Backend, error)
func StdioDialer(backendDir string, sink io.Writer, log Logger) Dialer
```

This is what makes the entire recovery ladder testable in milliseconds without
spawning Python.

## The three transport rules, as built

1. **Dedicated reader goroutine** (`readLoop`). Reading on the render goroutine
   would let a slow render fill the OS pipe buffer, block Python's write, and
   freeze the backend.
2. **Bounded channel (256) with a drop policy** (`emit`). Token events are
   dropped under backpressure and counted; **everything else blocks until the
   consumer catches up.** A dropped lifecycle event is a permanent spinner,
   which is strictly worse than a frame of lag.
   `TestFloodDropsTokensButNeverLifecycleEvents` sends 5,000 tokens through a
   4-deep channel with a dawdling consumer and asserts
   `received + dropped == sent` and that both lifecycle events survive.
3. **stderr drained to its own file**, never mixed with stdout.

`readLoop` uses `bufio.Reader` + `ReadSlice` with a **16 MiB** cap, not
`bufio.Scanner`. Scanner's 64 KiB default is far too small — one tool result or
a pasted file exceeds it, and the failure mode is a truncated line reported as
corruption.

## `Client` — messages the model receives

Every one of these arrives in `Update` like any other `tea.Msg`. There is no
shared mutable state between the reader goroutine and the model.

```go
func (c *Client) Next() tea.Cmd   // one message per call
```

**Every handler of a client message must return `Next()` again**, or the stream
stops after one event. `Root.afterBackend()` is the single place that does it.

| Message | Meaning |
|---|---|
| `ConnectingMsg{Attempt}` | before each attempt, 1-based |
| `ConnectedMsg{ProtocolVersion, Capabilities, Restarted}` | handshake done |
| `VersionMismatchMsg{Client, Backend}` | refuse; both versions named |
| `BackendLostMsg{Reason, UnknownTasks}` | tasks are UNKNOWN, not failed |
| `TasksInterruptedMsg{TaskIDs}` | these did not survive |
| `BackendFailedMsg{Reason, Detail}` | banner; no more auto-restarts |
| `EnvelopeMsg{Envelope, Route}` | a routed event |
| `ProtocolErrorMsg{Line, Text, Message}` | a non-envelope line on stdout |
| `ClosedMsg{}` | the client has stopped for good |

Query surface:

```go
func (c *Client) State() ConnState        // starting | connected | lost | failed
func (c *Client) Capabilities() Capabilities
func (c *Client) Restarts() int
func (c *Client) InFlight() []string
func (c *Client) Retry()
func (c *Client) Send(Envelope) error
func (c *Client) Request(e Envelope, timeout time.Duration) (<-chan Envelope, error)
func (c *Client) Release(id string)
func (c *Client) Close() error
```

**`Request`'s timeout covers the ACK only.** A command gets a fast ack and then
a stream, so once anything has arrived the timer stops and the channel stays
open until `Release`. A query is `Release`d straight after its one result. The
channel is closed on timeout, on `Release`, and on connection loss, so a caller
ranging over it is never left hanging.

Routing is `corr_id` → `task_id` → `session_id`, then connection-level, then
unroutable — and **routing does not consume**. A correlated reply reaches both
its waiter and the model, because the model still has to render what a request
produced. An unroutable event is delivered *and* logged: it means a contract
bug, and the point of logging is to see it.

## Backend directory resolution — the exact logic

```
1. POINT_BACKEND_DIR, if set        (validated; a wrong value is an error, not a fallback)
2. upward from os.Executable()      the packaged case
3. upward from os.Getwd()           the DEVELOPMENT case
```

A directory qualifies when it contains `backend\main.py`. Step 3 is **not**
redundant with step 2: `go run .\frontend\cmd\point` builds the binary into a
temp directory under `%LOCALAPPDATA%\go-build`, so walking up from the
executable finds nothing during normal work.

Working directory is `backend\` because `main.py` does
`from runtime.runtime import Runtime`, which resolves only from inside it.

## Windows specifics

- `uv` resolves through PATH; `exec.LookPath` applies `PATHEXT`, so no `.exe`
  handling.
- `spawn_windows.go` sets `SysProcAttr{HideWindow: true, CreationFlags:
  CREATE_NO_WINDOW}`. Pipes are inherited regardless — handle inheritance and
  window creation are unrelated.
- **`Close()` closes the child's stdin rather than killing it.** That is the
  shutdown `main.py` was designed around: EOF on stdin means "Go is gone", so it
  drains in-flight handlers, closes the database and releases the
  single-instance lock. Killing instead leaves that lock held by a dead pid and
  every later start refuses. A kill is the 5-second fallback.

## The recovery ladder, as built

```
no traffic for (MissedPings+1) × PingInterval   OR   process exit
  → in-flight tasks marked UNKNOWN  (BackendLostMsg)
  → one restart, 500 ms backoff
  → replay hook: NO-OP, see below
  → still-missing tasks → interrupted  (TasksInterruptedMsg)
  → a second failure within 60 s → BackendFailedMsg, no more auto-restarts
```

The banner is a status-bar line with a retry action, **not a modal** — asserted
by `TestFailureIsABannerNotAModal`, which checks the panes are still drawn.

A backend that exits **before** the handshake is not auto-restarted: a restart
would fail identically. Its own stderr tail explains it, which is exactly how
`AlreadyRunningError` ("Point backend is already running (pid …)") surfaces.

## Two places where F2's spec did not match this repo

Both were raised with Aryan before any code was written; the decisions are his.

### 1. The version-mismatch check compared the wrong thing

F2's manual step wrote `9.9.9` into the repo's `VERSION` file. Nothing in the
backend reads `VERSION` — only `cli.py doctor` prints it — and the `hello`
payload's `v` is `ENVELOPE_VERSION = 1` from `backend\runtime\events.py`. The
step could never fire, and its restore line wrote `0.1.0` over a file that says
`0.2.0`.

Built against the **envelope** version, per `project_idea.md` §4 rule 5.
`../../tasks/frontend/F02-backend-client.md` was corrected with Aryan's approval:
the check now bumps `ENVELOPE_VERSION` temporarily and reverts that one line,
and the Handshake section states which version `v` is.

### 2. `session.events_since` does not exist

F2's ladder calls it on reconnect. There is no such member of
`IncomingEventType` and no handler, so there was nothing for Go to call.
Building one from a frontend episode would invent backend surface a later
episode then has to reconcile with — which `CLAUDE.md` forbids twice over.

**`Client.afterReconnect` is a logged no-op seam.** Without replay, a task that
was in flight cannot be recovered, only reported: it goes UNKNOWN, then
interrupted. Partial text is kept; nothing is silently lost, and nothing is
resumed. The requirement is written into
`../../tasks/backend/B15-lifecycle-resources.md` itself — the command, the
handler, and an instruction to delete the no-op — because B15's session will not
read this file.

## Manual Test Log (F2)

1. **Handshake against the real backend.** `envelope v1, providers [ollama],
   schema v4, streaming true, cancel true`. Header and View Port both show
   `connected`.
2. **Query round trip.** `ListSessions` out, `SessionList` back, matched by
   `corr_id`, payload intact.
3. **Ping.** Arrives on its own within 5 s while idle, routed as
   `RouteConnection` (`corr_id` is null by design, so it is not unroutable).
4. **Crash recovery.** Killed `python.exe` mid-run with `Stop-Process`: banner,
   task shown `unknown`, automatic restart within ~1 s, task then `interrupted`,
   `restarts 1`. No loop.
5. **Two failures inside 60 s.** Banner `the backend has failed twice in a
   minute`, restart count froze, `ctrl+w r` offered and working, panes still
   drawn throughout.
6. **Version mismatch.** `ENVELOPE_VERSION = 2` in `events.py` → refusal naming
   both versions, no auto-restart. Reverted; `git diff --stat` clean.
7. **Protocol corruption.** `print("hi")` added to `main.py` → `line N: hi
   (invalid character 'h'…)` under Protocol errors, connection still working.
   Removed afterwards.
8. **stderr log growth**, verified against the real process by
   `TestLiveStderrLogGrowsOnEveryLaunch`: 161 bytes after launch 1, 321 after
   launch 2, with clean `started` / `exited cleanly` stamps and advancing mtimes.
9. `go test -race` clean on `protocol` and `app`.

Every live run used a **scratch profile** (`APPDATA` and `LOCALAPPDATA`
redirected in one script). The real `%LOCALAPPDATA%\Point` was confirmed
untouched by timestamp afterwards.

## Bugs caught during manual testing

### 1. `backend-stderr.log`'s timestamp never moved across launches

Reproduced twice, 33 minutes apart, with zero orphan `python.exe` processes and
a working connection both times.

**Not a Go bug and not a Python bug.** `backend\logging_setup.py` sets its stderr
handler to `WARNING` unless `--verbose`, so a healthy backend writes **nothing**
to stderr. The file was created once and never appended to again. An empty file
is the correct outcome for a clean run — and indistinguishable from a drain that
was never wired up, which is precisely what F2's verification asks you to
confirm by looking at it.

Fix: `StdioTransport.stamp` writes a framed line on spawn and on exit —
`---- point backend started · pid 22508 · D:\Projects\Point\backend · <RFC3339>`.
Python's warnings still land in the same file, interleaved, when there are any.
Python's log levels were left alone: lowering them would spam.

### 2. Pasting into the TUI did nothing at all

No counter change, no content, no error — while the same clipboard pasted fine
into a shell.

**My F1 handoff claimed bracketed paste delivers one `KeyMsg` with `Paste` set.
That is false on Windows.** In Bubble Tea v1.3.10:

- `initInput()` sets `ENABLE_VIRTUAL_TERMINAL_INPUT` on stdin, then
- `newInputReader()` → `prepareConsole(conin, ENABLE_WINDOW_INPUT |
  ENABLE_EXTENDED_FLAGS [| ENABLE_MOUSE_INPUT])` rebuilds the console mode
  **from zero**, clearing the VT-input bit that was just set, and
- `readConInputs()` emits one `KeyMsg` per console record and **never writes the
  `Paste` field**.

So `KeyMsg.Paste` is structurally always false here, the `\x1b[200~ … \x1b[201~`
markers never arrive, and `detectBracketedPaste` is only reachable on the ANSI
path. There is no `tea.WithPaste()` in v1.

`tea.WithInputTTY()` **would** reach the working ANSI path — it opens `CONIN$`
on a different file descriptor, and `newInputReader` falls back to the ANSI
cancelreader when the fd is not `os.Stdin`'s. It was rejected: on Windows
`listenForResize` is a no-op and `WindowSizeMsg` is produced **only** by the
console-record reader. That trades paste for a layout engine that never learns
the terminal resized — strictly worse, and the thing F1 exists to get right.

Fix: `frontend\internal\app\paste.go` reconstructs the burst. A paste is
delivered by `peekAndReadConsInput` as one batch, so its keys reach `Update`
microseconds apart against tens of milliseconds for typing. A one-key lookback
(max 6 ms added latency, well under one frame) catches the first character; the
burst flushes after 25 ms of quiet. Both routes emit the same `PasteMsg`, so F4
never sees the platform difference.

**Status: observed once, then not reproducible.** After the fix Aryan ran three
separate clean tests — 30 lines, 3 lines, 7 lines — and all three showed correct
event and line math. An earlier run had shown wrong counts once. It is recorded
here as *seen once, not reproduced afterwards*, not as fixed-and-confirmed-gone:
the burst detector is a timing heuristic, and a heuristic that has misfired once
should be watched. **F4 owns the real input and should re-test this
deliberately** — in particular a very large paste, and a paste arriving while
the app is busy rendering.

### 3. The restart counter ignored manual retries

`Restarts()` was incremented only by the automatic restart path, so `ctrl+w r`
started a real child process and the visible number never moved. Confirmed
across three tests.

Fix: derive it from `connects` (successful handshakes − 1), so any reconnect
counts however it was triggered. The 60-second auto-retry budget stays a
separate `failures` slice; `TestRestartCounterIsNotTheFailureBudget` asserts
neither can leak into the other.

### 4. `drainStderr` never checked `scanner.Err()`

Found by the linter, not by a test. `bufio.Scanner.Scan()` returns false for EOF
and for failure alike, so a broken stderr pipe would have ended the drain
**silently** — the exact condition transport rule 3 exists to prevent. From that
point Python's stderr has nobody reading it, and the next time the pipe buffer
fills, the logging call that fills it blocks. Now logged and stamped into the
stderr log.

### 5. `termenv` was a direct import listed as indirect

`app`'s `TestMain` imports it to pin the colour profile. `go mod tidy` promoted
it. Direct dependencies added by F1+F2: `BurntSushi/toml` (config.toml, matching
Python's tomllib including BOM handling), `charmbracelet/lipgloss` (was
indirect), `muesli/termenv` (test-only, to make colour assertable).

## What the next episode inherits

- **A live, verified link to Python.** `Client` is constructed in `main.go` and
  handed to `app.New`; consumers subscribe by handling `protocol.EnvelopeMsg` in
  `Update` and returning `Next()`.
- **`Envelope` and `Transport` are the contract.** Every episode from F3 sends
  and receives through them. F2 interprets type and correlation only; payloads
  are the consumer's.
- **`Capabilities` is stored on the root** (`Root.conn.capabilities`), so F10
  and later can grey out what the backend cannot do rather than offering it and
  failing.
- **Task lifecycle is tracked** in `Client.inflight`, keyed on `TaskStarted` and
  cleared by any terminal event. This is what makes "every spinner stops"
  checkable before there is a spinner.
- **Deliberately unfinished:** the replay rung
  (`Client.afterReconnect` → B15), and `busyGrace`.
- **`busyGrace` (60 s) is a workaround living on the wrong side.** The backend
  suppresses `ping` while `active_count() > 0`, so a slow, silent tool call is
  indistinguishable from a hang; the client stretches its watchdog to avoid
  killing healthy work, which pushes genuine hang detection from ~12 s to ~72 s.
  Recorded as **D98** in `design\decisions.md`, with the fix (emit `ping`
  unconditionally) and the instruction to delete `busyGrace` written into
  `../../tasks/backend/B15-lifecycle-resources.md`.
- **`live_test.go` is a permanent, opt-in integration suite.** It spawns the
  real backend and is skipped unless `POINT_LIVE_BACKEND=1`. It **must** be run
  against a scratch profile — the backend takes a single-instance lock, opens
  `runtime.db` and runs migrations. The header comment carries the PowerShell.
- One live-run surprise worth knowing: `resolve_db_path` copies
  `backend\runtime.db` into any empty profile, so a "clean" scratch profile
  arrives pre-seeded with real sessions. Harmless, surprising, and it made the
  first scratch run look like it had hit the real database.
