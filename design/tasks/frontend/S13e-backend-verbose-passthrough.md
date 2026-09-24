# Session 13e — Backend Verbose Passthrough (`POINT_BACKEND_VERBOSE`)

**Let a Go-launched app run its backend with `--verbose`, so the backend's
DEBUG lines — B20's context-report push confirmation among them — land in
`point.log` without launching the backend by hand.**

> **Depends on:** nothing unfinished. Found during S13d's verification gate.
>
> **Model:** Sonnet high. One environment variable, one argv append, one test.
>
> A **repair/wiring session**, the same shape as S11b/S12b/S13d: it does not
> consume an F number.

---

## Why this session exists

S13d's gate searched `point.log` for B20's push confirmation after a
completed turn and found nothing. Resolved with direct evidence, not
guesswork:

- The line is `_log.debug(...)` (`backend\runtime\runtime.py`,
  `_push_context_report`). `logging_setup.configure_logging()` sets the
  `point` logger to INFO unless `--verbose` is passed, so a DEBUG record is
  dropped at the logger — the file handler's own DEBUG threshold never gets
  to see it. The same gap B20 hit.
- The Go app spawns the backend as a fixed `uv run python main.py`
  (`frontend\internal\protocol\spawn.go`, `buildCommand`). No argument is
  ever added. `frontend\cmd\point` parses no flags at all, so
  `go run .\frontend\cmd\point --verbose` is silently ignored. `main.py`
  reads only `--verbose` from its own argv, and no environment variable
  sets it.
- Driving `main.py` over stdin exactly as Go spawns it: the push arrives on
  the wire (`ContextReport`, `corr_id: null`) and `point.log` contains
  **0 DEBUG lines** and no match. The identical run with `main.py --verbose`:
  the line is there
  (`DEBUG point.runtime: B20: pushing context report for session …`).

So the data path works and the log is the right file. Only the app has no
way to ask for the verbosity the backend already supports.

**`cli.py` is not a way around this.** Every `cli.py` command builds and
tears down its OWN in-process `Runtime` (`cli.py` `_with_runtime`); it never
attaches to a running `main.py` or app. `cli.py send --verbose` logs its own
process's push, not the app's.

---

## Build

1. **`frontend\internal\protocol\spawn.go`:** a new exported constant next to
   `BackendDirEnv`:
   ```go
   const BackendVerboseEnv = "POINT_BACKEND_VERBOSE"
   ```
   `buildCommand` appends `--verbose` to the argv when, and only when,
   `os.Getenv(BackendVerboseEnv) == "1"`.

   **Exactly `"1"`, decided here:** the same convention as the backend's
   `POINT_MOCK == "1"` and `POINT_LIVE_BACKEND=1`. Any other value (`true`,
   `yes`, `0`, empty) is off. A flag that half-parses is how a verification
   run silently doesn't do what it claims.

2. **Test**, beside the existing spawn tests in
   `frontend\internal\protocol\transport_test.go` (or a new
   `spawn_test.go`, your call). Using `t.Setenv`, assert `buildCommand(dir).Args`:
   - unset → exactly `uv run python main.py` (unchanged from today)
   - `"1"` → `uv run python main.py --verbose`
   - `"true"` and `"0"` → no `--verbose` (the exact-match decision above)

3. **Record the knob where someone will look for it:** a one-line comment on
   `buildCommand` naming the variable, plus a line in the completed doc's
   handoff.

---

## Two consequences to check, not change

- **`backend-stderr.log` grows with every DEBUG line.** `--verbose` also
  drops the backend's *stderr* handler to DEBUG, and the Go app appends that
  stream to `%LOCALAPPDATA%\Point\logs\backend-stderr.log`
  (`frontend\cmd\point\main.go`, `O_APPEND`, never rotated). Every verbose
  line is therefore written twice: to `point.log` (rotated) and to
  `backend-stderr.log` (not rotated). Acceptable for a debugging switch.
  Record the observed size after one verbose session in the handoff. Do
  **not** add rotation here.
- **`stderrTail` still ends on the crash.** The transport keeps the last N
  stderr lines as "the child's last words" for a failed handshake. Under
  verbose, DEBUG noise fills that buffer, but a traceback is written *last*,
  so the tail should still end on it. Confirm with one deliberate early
  exit. If DEBUG crowds out the real error, stop and report. Don't widen
  the buffer.

---

## Do NOT

- **Do not add command-line flag parsing to `frontend\cmd\point`.** An
  environment variable reaches the child without inventing the app's first
  CLI surface.
- **Do not change the backend.** `main.py --verbose` already works. This
  session only lets Go ask for it.
- **Do not change any log level** (`_push_context_report`'s DEBUG stays DEBUG,
  `frontend.log` stays as it is). The point is to make existing DEBUG
  observable on demand, not to promote lines to INFO.
- **Do not rotate or cap `backend-stderr.log`.** Record the size; a later
  episode decides.

---

## Done when

1. With `POINT_BACKEND_VERBOSE=1`, a Go-launched app's `point.log` contains
   `B20: pushing context report` once per completed turn.
2. Without it (and with any value other than `1`), `point.log` has no DEBUG
   lines, same as today.
3. The argv test passes for all four cases.
4. `backend-stderr.log`'s growth under verbose is measured and recorded.
5. A deliberate backend exit under verbose still reports its real error.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-s13e"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA               = $scratch
    $env:LOCALAPPDATA          = $scratch
    $env:POINT_BACKEND_VERBOSE = "1"
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    cd D:\Projects\Point
    go test ./frontend/internal/protocol/ -run "BuildCommand|Verbose" -v
    go run .\frontend\cmd\point
    # in the app: send one message, let it finish, then ctrl+q
    Select-String -Path (Join-Path $scratch "Point\logs\point.log") -Pattern "context report" -CaseSensitive:$false
    Get-Item (Join-Path $scratch "Point\logs\backend-stderr.log") | Select-Object Name, Length
}
finally {
    Remove-Item Env:POINT_BACKEND_VERBOSE -ErrorAction SilentlyContinue
    cd D:\Projects\Point
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Expect:** the `go test` cases pass; one `B20: pushing context report` line
per completed turn; a non-empty `backend-stderr.log` with its size noted.
Then repeat with the `POINT_BACKEND_VERBOSE` line removed. **Expect:** no
match, as today.

---

## Handoff

- `POINT_BACKEND_VERBOSE=1` is the one supported way to see backend DEBUG
  from the real app. Name it in any future gate that greps `point.log` for a
  DEBUG line, so no one repeats S13d's dead end.
- Record `backend-stderr.log`'s measured growth, and whether `stderrTail`
  still ended on the real error.
