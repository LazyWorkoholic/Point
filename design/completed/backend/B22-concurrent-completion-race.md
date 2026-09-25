# Episode B22 — Concurrent Task Completion Race (`SerializedConnection`) ✅

**A burst of 3-4 concurrent sends into one session could crash a task's own
successful completion mid-persist: `sqlite3.OperationalError: cannot commit
transaction - SQL statements in progress`, on the one connection every
repository shares. `database\connection.py` now hands out a
`SerializedConnection` that fully drains every `execute()`'s cursor, under
one lock, before returning — so no cursor is ever left "in progress" across
an await boundary for another coroutine's `commit()` to trip over. A second,
independent layer (`runtime\task.py`, `runtime\runtime.py`) makes sure that
even if persisting an already-successful task's reply fails for some other
reason, its live state can never be flipped to FAILED and the client can
never end up with zero terminal events for it.**

> Confirmed by Aryan, both from this session's own verification and from a
> separate manual reproduction run of his own, on a scratch profile
> (CLAUDE.md's APPDATA/LOCALAPPDATA override — the real profile was never
> touched by any run).
>
> **Not a task-doc episode.** Found live: a pre-existing defect, reported
> with two real stack traces from separate reproductions, that predates and
> reproduces independently of B18–B21. No `tasks/backend/B22-*.md` exists;
> this file records the fix the way the bug report specified it.

---

## Where the code went

```
backend\
├── database\
│   └── connection.py     SerializedConnection + _DrainedCursor (NEW) —
│                          open_database() now returns a SerializedConnection
│                          instead of the raw aiosqlite.Connection; every
│                          repository's `self._conn.execute(...)` /
│                          `.commit()` call is unchanged
├── runtime\
│   ├── task.py            _run() restructured: COMPLETED is decided BEFORE
│   │                      the terminal record/sink, not inside the same
│   │                      try that catches the task's own failures;
│   │                      + _sink_terminal() (NEW) — the one terminal sink
│   │                      call, guaranteed not to escape _run() uncaught
│   └── runtime.py         sink()'s message-persistence calls (both the
│                          streamed and the echo branch) and set_active_task
│                          wrapped in try/except — a DB failure there is
│                          logged, never lets the terminal TaskDone/
│                          TaskFailed/TaskCancelled event go unsent
└── tests\
    └── manual_concurrent_completion.py   NEW — bursts of concurrent sends,
                                           asserts exactly-one-terminal-event
                                           per task, tasks-row status, no
                                           unretrieved loop exceptions, no
                                           OperationalError/IntegrityError on
                                           stderr
```

No schema change. No repository file touched — every one of the 7
repositories (`session_`, `task_`, `attachment_`, `proposal_`, `tool_call_`,
`agent_`, `project_repository.py`) calls `self._conn.execute(...)` /
`.fetchone()` / `.fetchall()` / `.commit()` exactly as it did before; the fix
lives entirely behind the one object `open_database()` hands out.

---

## The mechanism

### Root cause, confirmed by direct evidence before any fix was written

Every repository shares one `aiosqlite.Connection`, built once in
`Runtime.create()`. Every repository method follows the same two-step shape:

```python
cursor = await self._conn.execute(sql, params)   # await #1
row = await cursor.fetchone()                    # await #2, separate suspension point
```

Those two awaits are not atomic. Between them, the event loop is free to run
any other ready coroutine's own queued operation on the *same* connection.
With several `Task` coroutines sharing it — a burst of sends into one session
— `context.build()` (which reads messages/attachments/project at
**task-creation time**, not gated by the turn lane) is exactly the kind of
read that can leave a SELECT cursor un-fetched across that gap. If another
coroutine's `commit()` (from a different task finishing) lands in that gap,
SQLite raises `OperationalError: cannot commit transaction - SQL statements
in progress` — it refuses to commit while *any* cursor on the connection has
not been stepped to completion, and that check is connection-wide, not
per-statement.

The original bug report carried two different tracebacks for this from
separate reproductions, both inside `SessionManager.append_message()`, at
its two sequential commits:

- `session_repository.py`'s `insert_message` (the `RETURNING seq` commit)
- `session_repository.py`'s `update_fields` (the "bump `updated_at`" commit)

**Both were reproduced live in this fix's own verification, independently:**

1. This session reverted only `connection.py` and re-ran
   `manual_concurrent_completion.py` (mock provider, no network): it hit
   `update_fields`'s commit —
   `sqlite3.OperationalError: cannot commit transaction - SQL statements in
   progress` at `session_repository.py:70`, in a fresh burst.
2. Aryan's own separate manual reproduction, also with `connection.py`
   reverted, hit the *other* call site — `insert_message`'s commit — on a
   fresh task (`6993c04b`). A direct SQL query against that same run's
   database confirmed the task's row still read `status = 'completed'`,
   `error_code = NULL`, despite the persistence-layer crash underneath it —
   the defense-in-depth layer (below) holding correctly, confirmed by
   database evidence, not by the test script's own assertions.

Together, both of the original bug report's commit sites were reproduced
independently in this fix's own verification, closing the report's own
concern that this "is not pinned to one line" — it is not; it is the shared
connection's lack of isolation between concurrent coroutines, full stop.

When the crash hit the *original* code (before this fix's defense-in-depth
layer existed), `Task._run()`'s generic exception handler then mislabeled the
already-successful task FAILED and tried to sink a `"failed"` event — which
itself crashed with `IntegrityError('UNIQUE constraint failed: messages.id')`
(the earlier failed commit had still partially written the row), uncaught,
surfacing as `"Task exception was never retrieved"` with **zero** terminal
events ever reaching the client for that task.

### Fix, layer 1 — `SerializedConnection` closes the race at its root

`open_database()` now returns a `SerializedConnection` wrapping the real
`aiosqlite.Connection`. Its `execute()` acquires a connection-wide
`asyncio.Lock`, runs the statement, and fully drains whatever cursor it
produced (`await cursor.fetchall()`) *before* releasing the lock and
returning. The object handed back (`_DrainedCursor`) answers
`fetchone()`/`fetchall()` from the already-fetched rows without touching the
real connection again — so by the time any coroutine's `execute()` call
returns control to the event loop, the connection has **no** statement left
"in progress," and a `commit()` from any other coroutine, at any point
afterwards, can never find one.

**Deliberate tradeoff, stated explicitly:** this serializes *all* database
access across the whole runtime behind one lock — correctness over
throughput, which is the right trade given SQLite's own single-writer nature
and this project's scale (one local process, no concurrent-write workload
the lock could meaningfully starve).

### Fix, layer 2 — a persistence failure can never cost a terminal event

Independent of the root cause, `Task._run()` and `runtime.py`'s `sink()`
were hardened so this *class* of failure — anything going wrong while
recording or announcing an already-decided result — can no longer produce
either wrong answer:

- `Task._run()`: `self.state = TaskState.COMPLETED` (and its `TaskResult`)
  is now set, and the row recorded, **before** the terminal sink call is
  attempted — not inside the same `try` that catches the task body's own
  failures. A bookkeeping failure afterward can no longer flip a genuinely
  successful task to `FAILED` in the `tasks` table.
- `Task._sink_terminal()` (new): the one terminal sink call `_run()` makes,
  wrapped so an exception is logged and swallowed rather than escaping
  `_run()` uncaught — closing the exact "Task exception was never
  retrieved" failure mode, structurally, regardless of what raises.
- `runtime.py`'s `sink()`: the message-persistence calls
  (`agent_executor.apply_response_middleware` + `sessions.append_message`,
  in both the streamed-accumulator branch and the echo/refusal branch) and
  `sessions.set_active_task(session_id, None)` are each wrapped in their own
  try/except, logged on failure. The terminal `TaskDone`/`TaskFailed`/
  `TaskCancelled` `_emit()` call — previously reachable only if persistence
  succeeded — now always runs.

---

## Verification

### Clean run — `manual_concurrent_completion.py`, all three layers as fixed

Three bursts, zero wait between sends (every task created, and its context
built, while earlier ones are still queued or streaming): 4 plain sends, 5
plain sends, and a mixed `@research` + 2 plain burst (the exact shape the
original bug report was filed under). 12 tasks total.

```
[PASS] total terminal events == total started tasks, globally  -- 12 terminal vs 12 started
[PASS] asyncio loop exception handler was never invoked
[PASS] stderr does not contain 'Task exception was never retrieved'
[PASS] stderr does not contain 'OperationalError'
[PASS] stderr does not contain 'cannot commit transaction'
[PASS] stderr does not contain 'IntegrityError'
[PASS] stderr does not contain 'terminal sink call failed'
[PASS] stderr does not contain 'failed to persist'
[PASS] stderr does not contain 'failed to clear active_task_id'

ALL CHECKS PASSED
```

Every one of the 12 tasks individually confirmed: exactly one terminal event
(`TaskDone`), and its `tasks` row `status == 'completed'`. `stderr` captured
0 characters — the run was completely silent, no defensive-path logging
triggered.

`manual_prompt_isolation.py` (the existing, unrelated regression it shares a
fixture and driver shape with) was re-run alongside it and stayed green —
this fix touches no prompt-assembly logic.

### Deliberately-broken run — proves the test catches the real defect, not a strawman

`connection.py` alone was reverted (stashed) and the same script re-run
against a fresh scratch profile, with layer 2 (`task.py`/`runtime.py`) still
in place:

```
2026-09-25 05:04:38 ERROR   point.runtime: Task 28206858-...: failed to persist its 'done' reply -- the terminal event still goes out
Traceback (most recent call last):
  File "...\runtime\runtime.py", line 1373, in sink
    await self.sessions.append_message(msg)
  File "...\managers\session_manager.py", line 167, in append_message
    await self._repo.update_fields(message.session_id, {})  # bumps updated_at
  File "...\database\session_repository.py", line 70, in update_fields
    await self._conn.commit()
sqlite3.OperationalError: cannot commit transaction - SQL statements in progress
...
[FAIL] stderr does not contain 'OperationalError'
[FAIL] stderr does not contain 'cannot commit transaction'
[FAIL] stderr does not contain 'failed to persist'
FAILED: stderr does not contain 'OperationalError'; stderr does not contain 'cannot commit transaction'; stderr does not contain 'failed to persist'
```

The exact original error, live, on demand. **And even with the root cause
still present**, every terminal-event and tasks-row-status assertion still
passed — direct proof layer 2 is independently effective, not merely
theoretical. `connection.py` was then restored (`git stash pop`) before any
further work.

Aryan separately reproduced the same defect his own way (see "Root cause"
above) and confirmed the same result by direct SQL against the affected
task's row — two independent reproductions, hitting the two different
commit call sites the original bug report named, both resolved by the same
one fix.

---

## What the next episode inherits

- **`open_database()`'s return type changed** from `aiosqlite.Connection` to
  `SerializedConnection` — duck-typed identically for every existing caller
  (`execute`/`commit`/`close`), so no repository or manager needed a change.
  A future caller that needs `cursor.lastrowid`/`.rowcount` from a write
  still gets them (`_DrainedCursor` carries both) — only `async with
  conn.execute(...) as cursor:`-style usage would need adjusting, and grep
  confirmed nothing in this codebase does that.
- **All database access is now serialized behind one connection-wide lock,
  by design.** This is a correctness-over-throughput tradeoff, appropriate
  given SQLite's single-writer nature and this project's scale — not
  something to "optimize away" without first establishing that lock
  contention is an actual, measured problem.
- **`Task._sink_terminal()` and `sink()`'s persistence try/except are now
  the load-bearing guarantee behind "exactly one terminal event per
  task."** Any future change to `Task._run()` or `runtime.py`'s `sink()`
  should keep completion-state decision separate from the
  record/announce-the-result step that follows it — that separation is what
  this episode's fix depends on structurally, not just at this one call
  site.
- **`tests\manual_concurrent_completion.py` is a real regression test, kept
  in the repo** (unlike B21's one-off verification techniques) — it exits
  non-zero on failure and needs no live model or network, so it can run in
  any future gate that touches `Task`, `TaskScheduler`, `runtime.py`'s
  `sink()`, or the database connection layer.
