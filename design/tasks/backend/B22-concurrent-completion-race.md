# Episode B22 — Concurrent Task Completion Race (`SerializedConnection`)

**A burst of 3-4 concurrent sends into one session can crash a task's own
successful completion mid-persist — `sqlite3.OperationalError: cannot commit
transaction - SQL statements in progress` — on the one `aiosqlite` connection
every repository shares. Close the race at its root: no cursor may ever be
left "in progress" across an `await` boundary for another coroutine's
`commit()` to trip over. Then make sure that even if persisting an
already-successful task's reply fails for some other reason, its live state
can never be flipped to `FAILED` and the client can never end up with zero
terminal events for it.**

> Task doc — describes **intent**. **Written retroactively**, after the fix
> already shipped: `design/completed/backend/B22-concurrent-completion-race.md`
> itself says "Not a task-doc episode... No `tasks/backend/B22-*.md` exists;
> this file records the fix the way the bug report specified it." This file
> exists only to bring B22 into the same documented shape as every other
> episode in the series, reconstructed from the completed doc's own detailed
> account (which remains the authoritative record of what actually happened
> and how it was verified) and from `tests/manual_concurrent_completion.py`,
> the real regression test the fix left behind. **Code and the completed doc
> win on any disagreement with this file** — same rule as every other task
> doc, doubly true here since this one was written last, not first.

**Model:** Opus. Found live, not spec'd in advance — a shared-connection race
condition under concurrency, two independent crash signatures from the same
root cause, and a defense-in-depth layer on top. Debugging-grade reasoning,
not a mechanical build from a clear spec.

**Depends on:** B1 (Runtime — `Task`, its lifecycle and terminal states), B2
(Session System — `database/connection.py`'s single shared
`aiosqlite.Connection`, and `SessionRepository`'s two sequential commits
where both reproductions landed), B12 (`TaskScheduler` — task *creation*,
including `context.build()`'s reads, happens before a task is admitted into
its turn lane, which is exactly what lets a burst of sends overlap on the
shared connection despite the turn lane's own cap of 1).

**Consumed by:** Every future episode that touches `Task`, `TaskScheduler`,
`runtime.py`'s `sink()`, or `database/connection.py` — the completed doc's
own handoff says the completion-state-decided-before-record/announce
separation this fix depends on must be preserved structurally, not just at
this one call site.

---

## Why this episode exists

A bug report, not a planned episode: a burst of 3-4 concurrent sends into one
session intermittently crashed with
`sqlite3.OperationalError: cannot commit transaction - SQL statements in
progress`, carrying two different tracebacks from separate reproductions —
both inside `SessionManager.append_message()`, at its two sequential
commits (`session_repository.py`'s `insert_message`, the `RETURNING seq`
commit, and its `update_fields`, the "bump `updated_at`" commit). The report
explicitly noted the crash was "not pinned to one line."

Root cause, confirmed by direct evidence before any fix was written: every
repository shares one `aiosqlite.Connection`, and every repository method
does `cursor = await self._conn.execute(...)` then `await
cursor.fetchone()/fetchall()` as two separate awaits with a suspension point
between them. With several `Task` coroutines sharing the connection,
`context.build()` — which reads messages/attachments/project at
**task-creation time**, not gated by the turn lane — is exactly the kind of
read that can leave a cursor un-fetched across that gap. If another
coroutine's `commit()` lands in that gap, SQLite refuses it: that check is
connection-wide, not per-statement.

When the crash hit the original code, it compounded: `Task._run()`'s generic
exception handler mislabeled the already-successful task `FAILED` and tried
to sink a `"failed"` event, which itself crashed with
`IntegrityError('UNIQUE constraint failed: messages.id')`, uncaught,
surfacing as `"Task exception was never retrieved"` with **zero** terminal
events ever reaching the client for that task.

---

## Build

### 1. `SerializedConnection` — close the race at its root (`database/connection.py`)

`open_database()` returns a `SerializedConnection` wrapping the real
`aiosqlite.Connection`, duck-typed identically (`execute`/`commit`/`close`)
so no repository or manager needs to change. Its `execute()` acquires a
connection-wide `asyncio.Lock`, runs the statement, and fully drains
whatever cursor it produced (`await cursor.fetchall()`) *before* releasing
the lock and returning; the object handed back answers
`fetchone()`/`fetchall()` from the already-fetched rows without touching the
real connection again. By the time any `execute()` call returns control to
the event loop, the connection has no statement left "in progress."

**Deliberate tradeoff, to be stated explicitly in the build, not discovered
later:** this serializes *all* database access behind one lock —
correctness over throughput, appropriate given SQLite's single-writer
nature and this project's scale. Do not treat lock contention as a problem
to optimize away without first measuring that it actually is one.

### 2. A persistence failure can never cost a terminal event (`runtime/task.py`, `runtime/runtime.py`)

Independent of the root cause, harden `Task._run()` and `runtime.py`'s
`sink()` so this *class* of failure can no longer produce a wrong answer:

- `Task._run()`: decide `TaskState.COMPLETED` (and record the `TaskResult`
  row) **before** attempting the terminal sink call — not inside the same
  `try` that catches the task body's own failures. A bookkeeping failure
  afterward must never flip a genuinely successful task to `FAILED` in the
  `tasks` table.
- A single terminal-sink call (e.g. `Task._sink_terminal()`) that cannot let
  an exception escape `_run()` uncaught — closing "Task exception was never
  retrieved" structurally, regardless of what raises.
- `runtime.py`'s `sink()`: wrap the message-persistence calls (both the
  streamed-accumulator branch and the echo/refusal branch) and
  `set_active_task(session_id, None)` each in their own try/except, logged
  on failure — never silently swallowed, never allowed to block the
  terminal `TaskDone`/`TaskFailed`/`TaskCancelled` emission that follows.

---

## Do NOT

- Touch any repository file. All seven (`session_`, `task_`, `attachment_`,
  `proposal_`, `tool_call_`, `agent_`, `project_repository.py`) must keep
  calling `self._conn.execute(...)` / `.fetchone()` / `.fetchall()` /
  `.commit()` exactly as before — the fix lives entirely behind the one
  object `open_database()` hands out.
- Change *when* the user message is persisted, or any turn-queue/scheduler
  admission logic from B12 — this is a connection-layer and
  completion-bookkeeping fix, not a concurrency-policy change.
- Fabricate or skip a terminal event to work around a persistence failure —
  the fix is to make persistence failures non-fatal to the terminal event,
  never to paper over a missing one.
- "Optimize" the connection-wide lock into per-table or per-statement
  locking without first measuring real contention. That is a different,
  larger change with its own correctness risk, and nothing here calls for
  it.

---

## Done when

1. A burst of concurrent sends into one session (the reproduction shape:
   several plain sends, and a mixed `@research` + plain-send burst)
   produces **zero** `OperationalError`, `IntegrityError`, or "Task
   exception was never retrieved" on stderr.
2. Every task in the burst gets **exactly one** terminal event
   (`TaskDone`/`TaskFailed`/`TaskCancelled`) — never zero, never more than
   one.
3. Every task whose terminal event is `TaskDone` has a `tasks` row whose
   `status` is `'completed'`, never `'failed'`, even under a deliberately
   reintroduced connection-layer failure (layer 2 must hold independently
   of layer 1).
4. The asyncio loop's exception handler is never invoked during the run.
5. Reverting `connection.py` alone (with layer 2 still in place) reproduces
   the *original* error text on stderr, proving the regression test
   exercises the real defect and not a strawman.

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md: `APPDATA`/`LOCALAPPDATA` redirected to a scratch directory
for the whole script, never the live profile. The regression test this
episode left behind (`backend\tests\manual_concurrent_completion.py`) is
kept in the repo and exits non-zero on failure — this gate is just running
it.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b22"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # 1-4: the clean run -- all checks above, asserted by the script itself
    uv run python tests\manual_concurrent_completion.py
    # EXPECT: "ALL CHECKS PASSED", 0 characters on stderr

    # 5: proves the test isn't a strawman -- stash connection.py's fix,
    # re-run with layer 2 still in place
    git stash push -- database\connection.py
    uv run python tests\manual_concurrent_completion.py
    # EXPECT: the original OperationalError text appears on stderr, AND
    # every terminal-event / tasks-row-status assertion still passes --
    # layer 2 holding independently of layer 1.
    git stash pop
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

---

## Handoff

- This doc is a reconstruction, not a plan that was followed. The
  authoritative account — the two independent live reproductions (one from
  this session, one Aryan ran separately and confirmed by direct SQL
  against the affected task's row), the exact stack traces, and the full
  "deliberately-broken run" proof — lives in
  `design/completed/backend/B22-concurrent-completion-race.md` and is not
  repeated here in full.
- **`open_database()`'s return type changed** from `aiosqlite.Connection` to
  `SerializedConnection`. Any future caller using
  `async with conn.execute(...) as cursor:` style usage (none does today —
  confirmed by grep at the time of the fix) would need adjusting.
- **All database access is serialized behind one connection-wide lock, by
  design** — a correctness-over-throughput tradeoff appropriate at this
  project's scale, not something to remove without first measuring lock
  contention as an actual, observed problem.
- The completion-state-decided-before-record/announce separation in
  `Task._run()` is now load-bearing. Any future change to `Task._run()` or
  `runtime.py`'s `sink()` must preserve it.
