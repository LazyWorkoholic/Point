# F2 — Backend Client

**Phase:** P1 · **Batchable:** yes (with F1) · **Depends on:** F1 · **Backend:** B1–B5 ✅

## Purpose

The link to Python, built so that the backend dying is a normal, recoverable
event rather than a broken UI.

## Build

### Spawning the backend
```go
cmd := exec.Command("uv", "run", "python", "main.py")
cmd.Dir = filepath.Join(repoRoot, "backend")   // imports are relative to backend\
```
The working directory **must** be `backend\` — `main.py` does
`from runtime.runtime import Runtime`, which only resolves from inside that
folder. Do not "fix" the Python imports.

Locate `backend\` relative to the executable, with a `POINT_BACKEND_DIR` override
so a packaged build can point elsewhere.

Windows notes: `uv` resolves via PATH, so no `.exe` suffix handling is needed;
set `cmd.SysProcAttr` to avoid a console window flash if one appears; and
**always** drain stderr, or a full pipe buffer will block Python's logging.

### Transport
```go
type Transport interface {
    Send(Envelope) error
    Recv() <-chan Envelope
    Close() error
}
```
`StdioTransport` wraps the child process. The interface exists so a socket
implementation is a drop-in later — do not scatter `cmd.Stdin` across the codebase.

### Envelope
Exactly the schema in `project_idea.md` §4: `v, id, type, corr_id, session_id,
task_id, connection_id, trace_id, ts, payload`. Newline-delimited JSON, one
message per line.

### The three transport rules — each is a real bug
1. **Dedicated reader goroutine.** Reading on the render goroutine lets a slow
   render fill the OS pipe buffer, blocking Python's write and freezing the
   backend.
2. **Bounded channel with a drop policy.** Under backpressure, drop intermediate
   `task.token` events — the accumulated string re-renders anyway. **Never** drop
   a lifecycle event (`completed` / `failed` / `cancelled`); a dropped one is a
   permanent spinner.
3. **Python's stderr drained to a log file**, never mixed with stdout.

### Routing
By `corr_id`, then `task_id`, then `session_id`. An event matching nothing is
**logged, not silently dropped** — an unroutable event means a contract bug you
want to see.

### Request correlation
`Send` returns a channel for the ack/result of a command or query, with a
timeout. Commands get a fast ack then a stream; queries get one result.

### Handshake
Wait for `hello {v, capabilities}`. On version mismatch, **refuse** and show both
versions plus the fix. Store `capabilities` on the root so later episodes can grey
out unsupported features rather than lying.

`v` is the **envelope** version — `ENVELOPE_VERSION` in `backend\runtime\events.py`,
currently `1` — not the product version in the repo's `VERSION` file. Nothing on
the wire reads `VERSION`; only `cli.py doctor` prints it.

### Heartbeat and recovery
```
ping every 5s while idle
two missed pings OR process exit
  → mark in-flight tasks UNKNOWN (not FAILED — you do not know yet)
  → restart the child once, with backoff
  → on reconnect: session.events_since(session, last_seq) to replay
  → still-missing tasks → FAILED, partial text kept, marked interrupted
  → a second failure within 60s → banner, no more auto-restarts, manual retry
```
The banner is a status-bar line with a retry action, **not a modal** — the rest of
the app stays usable.

### Bubble Tea integration
The reader channel is surfaced as a `tea.Cmd` yielding the next envelope, so
backend events arrive in `Update` like any other message. No shared mutable state
between goroutines and the model.

## Do NOT

- Render anything (F3).
- Interpret payloads beyond routing — that is each consumer's job.
- Retry more than once automatically.
- Change the Python import structure.

## Done when

1. Starting the app spawns the backend, the handshake completes, and
   `capabilities` is populated.
2. A deliberately mismatched `VERSION` refuses to proceed, naming both versions.
3. Killing the Python process mid-stream produces a banner, an automatic restart,
   and intact messages after replay.
4. Two failures inside 60s → banner with manual retry, **no restart loop**.
5. Flooding the channel drops token events but never a terminal event — every
   task still reaches a terminal state.
6. A stray `print()` on the Python side produces a **clear** protocol error naming
   the offending line, not silent corruption.
7. Envelope round-trip tests cover every event type, including unknown-type
   tolerance (log and ignore, never crash).

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```
**Expect:** the app starts, the header shows connected, and
`%LOCALAPPDATA%\Point\logs\` has backend stderr in it.

```powershell
# 3. crash recovery — with a reply streaming, in a second tab:
Get-CimInstance Win32_Process -Filter "Name='python.exe'" |
  Where-Object { $_.CommandLine -like "*main.py*" } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
```
**Expect:** a banner, an automatic restart within a second or two, and the partial
reply still on screen marked interrupted.
**Fails if:** the UI hangs, the partial text vanishes, or it restarts in a loop.

```powershell
# 2. version mismatch — the ENVELOPE version, not the repo's VERSION file.
#    `hello` carries ENVELOPE_VERSION from backend\runtime\events.py, and
#    nothing on the wire reads VERSION at all (only `cli.py doctor` prints it).
#    So bump the one line the handshake actually compares, then revert it.
#    NEVER edit VERSION for this check.
$events = ".\backend\runtime\events.py"
(Get-Content $events -Raw) -replace 'ENVELOPE_VERSION = 1', 'ENVELOPE_VERSION = 2' |
  Set-Content $events -NoNewline
go run .\frontend\cmd\point
# ... observe the refusal, quit, then put it back:
(Get-Content $events -Raw) -replace 'ENVELOPE_VERSION = 2', 'ENVELOPE_VERSION = 1' |
  Set-Content $events -NoNewline
git diff --stat backend\runtime\events.py    # expect: no output
```
**Expect:** a refusal naming both versions — "Point speaks v1, the backend
speaks v2" — and no automatic restart, because a restart cannot fix it.
**Fails if:** it connects anyway, or it retries in a loop.

```powershell
# 6. protocol corruption — temporarily add print("hi") to backend\main.py
go run .\frontend\cmd\point
```
**Expect:** a clear protocol error. **Fails if:** it hangs or shows garbage.
Remove the print afterwards.

```powershell
go test ./frontend/internal/protocol/...
```
**Expect:** green.

## Handoff

State after this episode: Go and Python are talking, versions are verified, and
killing the backend mid-stream is survivable. This is half of the strongest beat
in the demo — the other half is B15.

Record: the exact backend-directory resolution logic, and anything Windows-
specific about spawning `uv`.

Do not proceed until every check above passes.
