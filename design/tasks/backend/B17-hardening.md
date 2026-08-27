# B17 — Hardening

**Phase:** P2 · **Batchable:** no · **Depends on:** all

## Purpose

Make failure modes uniform, behaviour verifiable without a human, and the thing
installable by someone who is not you.

## Build

### Error taxonomy
One error type crossing the boundary:
```python
class PointError(Exception):
    code: str
    message: str        # a user-facing sentence, never a traceback
    retryable: bool
    detail: dict
```

Codes, stable — the UI switches on `code`, never on message text:
```
provider_auth · provider_rate_limit · provider_unavailable · provider_bad_request
context_overflow · bounds_exceeded · tool_error · tool_timeout
tool_not_granted · tool_not_found · tools_unsupported
path_denied · file_too_large · file_binary · file_changed · lock_held
cap_exceeded · queue_full · not_found · schema_mismatch · backend_died
```
Every raise site maps to one. No message may contain a credential.

**`tool_failed` was renamed to `tool_error`, and three tool codes were
added.** This list originally said `tool_failed`; B10 shipped the code as
`tool_error` and it is already on the wire and in `tasks.error_code`. The
code that exists wins over the code that was planned — corrected here rather
than left for B17 to discover a mismatch it would have to migrate. The three
additions (`tool_not_granted`, `tool_not_found`, `tools_unsupported`) had no
entry at all because the tool system did not exist when this list was
written.

### The three stopgap classes to fold in — named, not described

Three earlier episodes deliberately raised a narrow exception rather than
inventing a piece of `PointError` early, each with a docstring saying so.
**These are the exact classes, and this is the complete list** — do not go
looking for others, and do not build `PointError` piecemeal before this
episode:

| Class | Module | Episode | Code it becomes |
|---|---|---|---|
| `PathDeniedError` | `runtime\errors.py` | B7 | `path_denied` |
| `ContextOverflowError` | `runtime\errors.py` | B8 | `context_overflow` |
| `ToolError` + 5 subclasses | `runtime\errors.py` | B10 | see below |

`ToolError` is different from the other two in one way that matters here:
**it already carries its codes.** B10's Done-when #4 required
`bounds_exceeded` to be distinguishable on the wire and in the `tasks` row,
so the code could not wait for this episode to invent one. Every subclass
sets a `code` class attribute, `Task._run()` copies it onto
`TaskResult.code` via `getattr(exc, "code", None)`, and it reaches Go in the
`TaskFailed` payload and `tasks.error_code`:

```
BoundsExceededError    -> bounds_exceeded    (also carries .bound and .limit)
ToolExecutionError     -> tool_error
ToolTimeoutError       -> tool_timeout
ToolNotGrantedError    -> tool_not_granted
ToolNotFoundError      -> tool_not_found
ToolsUnsupportedError  -> tools_unsupported
```

**B17's job for the tool family is therefore narrower than for the other
two: fold them into the shared envelope, do not invent codes for them.**
Changing any of these strings breaks a frontend branch that already exists.
`ContextOverflowError` is the parallel warning in the other direction — it
carries `needed_tokens`/`budget_tokens` as attributes precisely so the UI
never re-parses them out of a sentence; `PointError.detail` is where those
belong.

### Test suite

**Python** (`backend\tests\`, alongside the existing `manual_provider_test.py`)
```
repositories        real SQLite in a temp file, migrations applied
context builder     assert exactly what enters context at a given budget,
                    including eviction order and the attachment-change rule
provider adapters   recorded fixtures; NO live calls
MockProvider        drives every agent/task/stream/cancel/recovery test
tool executor       schema failure, exception, timeout, bounds
memory              accepted-only retrieval, cross-scope dedup, forget
permissions         each guest restriction asserted at the backend entry point
path guard          the three Windows escape forms from B7
```

**Go** (`frontend\internal\...`)
```
Compute()           table-driven across every breakpoint; no terminal
protocol codec      round-trip every event type; reject an unknown version
action registry     no duplicate ids, no duplicate bindings
diff computation    region → diff → apply → original + edit round-trips
```

**Cross-boundary**
```
protocol conformance   cli.py replays a scripted session and asserts the
                       event sequence: exactly one terminal event per task,
                       monotonic seq numbers, no gaps
```

### Chaos checks (scripted, not manual)
- Kill the backend mid-stream → restart → replay → conversation intact.
- Fill the outbound channel → token events drop, lifecycle events do not.
- 429 storm → backoff, no lost tasks, no duplicate messages.

### Packaging
```powershell
# PyInstaller ONEDIR, never onefile
uv run pyinstaller --onedir --name point-backend backend\main.py
go build -o dist\point.exe .\frontend\cmd\point
```
`--onefile` re-extracts to `%TEMP%` on **every** launch — roughly a second of
pure waste per start.

- Version stamped from `VERSION` into both binaries; the handshake rejects a
  mismatch.
- Smoke-test on a machine (or a clean user profile) with no Python installed.
- Windows specifics to expect: Defender may flag a fresh PyInstaller binary on
  first run, and hidden imports for the provider SDKs often need
  `--hidden-import`. Record whatever you had to add.

### Docs regeneration
Confirm `completed\**` matches reality for every built episode, and that no task
doc claims completion.

## Do NOT

- Add telemetry, i18n, auto-update, or plugin sandboxing.
- Add live provider calls to the test suite.
- Refactor working code for elegance while "hardening".

## Done when

1. Every raise site maps to a taxonomy code — grepping the runtime package for
   bare `raise Exception` returns nothing.
2. The full suite is green **with no network access**.
3. The protocol conformance test asserts one terminal event per task and
   monotonic `seq` across a scripted session.
4. Chaos checks pass unattended.
5. The packaged build runs on a profile with no Python; cold start measured and
   recorded.
6. A deliberately mismatched Go/Python version refuses to start, printing both.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend
uv run pytest -q
```
**Expect:** green. Then disconnect from the network and run it again — still
green.

```powershell
Select-String -Path .\**\*.py -Pattern "raise Exception\(" -Recurse
```
**Expect:** no matches in runtime code.

```powershell
cd D:\Projects\Point
go test ./frontend/...
```
**Expect:** green, no terminal required.

```powershell
# packaging
uv run pyinstaller --onedir --name point-backend backend\main.py
go build -o dist\point.exe .\frontend\cmd\point
Measure-Command { .\dist\point.exe --version }
```
**Expect:** both build; record the cold-start time.

```powershell
# version mismatch
"9.9.9" | Set-Content VERSION -NoNewline
# rebuild only ONE side, then start
```
**Expect:** a refusal naming both versions.
**Fails if:** it starts and misbehaves later.
Restore `VERSION` afterwards.

## Handoff

State after this episode: failures are uniform and typed, the suite runs offline,
the app installs for someone else, and the docs match reality. This is the end of
the planned backend work — everything remaining is in the parking lot.

Record: any `--hidden-import` flags PyInstaller needed, the measured cold start,
and whether Defender interfered.
