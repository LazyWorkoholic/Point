# Episode B40 — The Durable Event Log, and Real `events_since`

**Persist every outgoing envelope to an `events` table, give `seq` a
durable per-session definition, and replace B15's `SessionReplaySince`
with the event replay F2's recovery ladder was specified against.**

> Task doc — describes **intent**. A completed doc
> (`design/completed/backend/B40-durable-event-log.md`) will claim
> reality and completion. Code wins on any disagreement.

> **Written during B15 (Session 20), before any of this is built.** B15's
> Build section asked for `session.events_since(session_id, seq)` and
> this repo has no event log to serve it from. Rather than invent a
> schema mid-episode or leave a second seam open, B15 built the honest
> interim — **`SessionReplaySince`**, message-level replay keyed on
> `messages.seq` — and this doc records the real target. Nothing here is
> derived from working code; it is the deferred half of B15, stated while
> the reasons were in front of someone.

**Model:** Opus medium. Not for the table, which is four columns and an
index. For the two decisions underneath it: what a write per envelope
costs on the token path through `SerializedConnection`, and what `seq`
means once it has to be durable and per-session rather than in-memory and
per-task. The second one is a wire-contract change to a field F2, F5, F18
and B24 all already read, which is the shape of work B6 and B32 both
rated Opus-tier.

**Depends on:** **B15** (Session 20) — this replaces a command B15 built,
and inherits its `message_checkpoints` table and crash-recovery ordering.
**B18** (done) for `messages.seq`, the cursor this episode must not break
while adding a second, different `seq`. **B22** (done) for
`SerializedConnection`, which every write here goes through.

**Consumed by:** F2's recovery ladder (`Client.afterReconnect`), which
B15 filled against `SessionReplaySince`. This episode changes what that
function asks for, so the Go side moves with it.

---

## Why this episode exists

B15's Build section says:

> `session.events_since(session_id, seq)` → the events needed to rebuild
> UI state after a reconnect.

Four things in this repo make that unservable as written, all verified on
disk during B15 rather than assumed:

1. **There is no `events` table.** Schema v7 holds `sessions`,
   `messages`, `tasks`, `projects`, `agents`, `memory_records`,
   `memory_scopes`, `context_attachments`, `file_locks`, `usage_records`,
   `presence`, `tool_calls`, `agent_proposals` and
   `message_checkpoints`. No event log, and nothing that resembles one.
2. **Nothing persists an `OutgoingEvent`.** `Runtime._emit` puts the
   envelope on `self.outbox` and that is the end of its life; `main.py`'s
   writer loop serializes it to stdout and drops it.
3. **The envelope's `seq` cannot be the cursor.** It is a per-TASK stream
   counter living in `StreamManager._sequences` — an in-memory dict,
   incremented by `next_sequence(task_id)`, deleted by `close_stream()`
   at the terminal event. It does not survive the task, let alone the
   process, and it is `null` on every envelope that is not part of a
   task's token stream (`events.py`'s own module docstring: *"a
   lifecycle event is identified by its type, not by counting"*).
4. **`messages.seq` is a different quantity.** Migration v6 (B18) added
   it as per-session message insertion order, assigned by
   `insert_message`'s own `MAX(seq) + 1` subquery. It is durable and
   ordered, which is exactly why B15 keyed its interim replay on it —
   and it counts messages, not events, so it cannot express "the
   `ToolStarted` you missed".

So B15 shipped **`SessionReplaySince`**: `{session, since_seq, limit}` in,
`{session, since_seq, mechanism: "messages.seq", replay: true, messages,
tasks, has_more}` out. It answers the question F2 actually needs answered
after a reconnect — *what does this transcript look like now* — and it
says `mechanism` on the wire so no consumer has to guess which `seq` it
is holding. What it cannot answer is *what happened while I was gone*: a
tool call that started and completed, a progress step, a usage update, a
`FillProposal` pushed after a terminal event. Those are events, and
events are not stored.

**This episode is what makes them stored.**

---

## Build

### 1. The `events` table (migration v8)

```sql
CREATE TABLE IF NOT EXISTS events (
    id            TEXT PRIMARY KEY,   -- the envelope's own `id`
    session_id    TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    task_id       TEXT,
    corr_id       TEXT,
    type          TEXT NOT NULL,
    session_seq   INTEGER NOT NULL,   -- the new cursor; see 3 below
    stream_seq    INTEGER,            -- the EXISTING per-task counter, kept
    ts            REAL NOT NULL,
    payload       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, session_seq);
```

`session_id` is nullable because `hello` and `ping` have none, and an
event log that cannot hold them is not a log of what went over the wire.
Decide explicitly whether connection-level envelopes are written at all —
they are the highest-volume, least-replayable rows in the design (one
`ping` every 5s, forever, as of B15's unconditional heartbeat, D98).

**What gets written, and where.** Every envelope that leaves
`Runtime._emit`, written from inside `_emit` itself — one place, the same
way `_fill_envelope` is already the single serialization boundary. Not
from the writer loop in `main.py`: that is transport, it has no database
handle, and a log that only records what was successfully written to
stdout cannot record the envelopes that mattered when stdout died.

### 2. The cost, stated honestly before it is paid

**This is a database write on the streaming hot path, through
`SerializedConnection`.** That is not a detail to discover in profiling;
it is the central risk of the episode and the reason it was deferred out
of B15 rather than folded in.

B22's `SerializedConnection` takes a connection-wide `asyncio.Lock`
around every `execute()` and drains the cursor before releasing it. Every
`TextDelta` would therefore queue behind every other database operation
in the runtime — the `tasks` row upsert, the context builder's reads,
B15's checkpoint writes — and a local model emitting tens of deltas a
second would turn each one into a lock acquisition plus a commit.

Three options, and the episode must pick one and say why:

- **(a) Write every envelope synchronously in `_emit`.** Simplest,
  slowest, and the only one where the log is complete at the instant the
  process dies — which is the property a crash-recovery feature exists
  for.
- **(b) Buffer and flush.** A bounded in-memory list, flushed on a timer
  or at N envelopes, in one multi-row INSERT. B15's checkpoint cadence
  (500ms, `CHECKPOINT_INTERVAL_S`) is the precedent and the measured
  argument for it. The cost is that the last window of events is lost in
  exactly the crash the log is for — which may be acceptable, because
  B15's `message_checkpoints` already preserves the reply TEXT across
  that same window, and the events are the finer-grained story on top.
- **(c) Write lifecycle events synchronously and token deltas not at
  all.** `TextDelta`/`ThinkingDelta`/`TaskToken` are the volume, and they
  are also the three types `IsDroppable` in
  `frontend\internal\protocol\types.go` already says may be discarded
  under backpressure, because *"the accumulated string re-renders from
  the next token anyway"*. If a dropped token costs a frame, a
  non-persisted token costs nothing a replay can observe — the text is
  rebuilt from `messages` (or from B15's checkpoint).

**(c) is the strong candidate and (b) is its fallback**, but the episode
owes a measurement, not a preference. Measure it: a fixed fixture
through `POINT_MOCK=1`, with and without the write, reporting wall-clock
and the delta count. **Never claim a performance benefit that has not
been measured** (CLAUDE.md).

### 3. What `seq` means afterwards

Today `seq` on the envelope is the per-task stream counter, and
`StreamAccumulator` reads the same number out of the payload
(`events.py`: *"one counter, printed twice"*). B18's `messages.seq` is a
per-session counter with no relation to it.

This episode needs a **durable, per-session, monotonic event cursor** —
the thing `events_since(session_id, seq)` is keyed on. It must not be
either of the existing two, and it must not quietly replace the envelope
field four consumers already decode.

The decision to make: does `session_seq` ride on the envelope as a new
field, or does the envelope's `seq` change meaning?

- **A new field** (`event_seq`, say) is additive, which is the rule B1–B5
  are frozen under and the pattern B18, B24 and B36 all followed. Every
  existing consumer keeps working, and a replay client reads the new one.
- **Changing `seq`'s meaning** is one field instead of two, and it
  silently alters what `StreamAccumulator`, F5's stream ordering, F18's
  pagination and B24's `FillProposal` note (*"`seq` is null on BOTH
  envelopes and cannot order them"*) are reading. That note becomes false
  the moment every envelope has a real cursor, which is an improvement
  **and** a contract change — and a contract change that arrives
  disguised as the same field is the kind that gets found by a frontend
  episode six weeks later.

Assignment must be atomic against concurrent coroutines on one
connection. `insert_message`'s `(SELECT COALESCE(MAX(seq), 0) + 1 FROM
messages WHERE session_id = ?)` inline in the `VALUES` clause is the
pattern that already solves this here, and B18's own note says why: *two
coroutines on this connection can only interleave between whole
statements, never inside one.* Do not read-then-write.

### 4. `events_since(session_id, seq)` — the real command

A new `IncomingEventType` and handler, returning the envelopes after the
cursor in order, bounded by a limit, with `has_more`.

**`SessionReplaySince` is then deleted, not kept beside it.** Two replay
commands with two different cursors is exactly the "second copy that can
disagree" this repo removes on sight (`managers\provider.py`,
`managers\tool.py`). The frontend's `CmdSessionReplaySince` and
`EvSessionReplay` constants go with it, and `Client.afterReconnect`
switches to the new command — including `MarkSeen`, whose `seq` argument
is currently documented as `messages.seq` and would become the event
cursor.

### 5. Retention

An append-only log of every envelope on a long-lived local backend grows
without bound. `TaskManager.sweep_finished(keep_last=200)` is the
precedent for the shape of the answer (a bounded in-memory index swept
from the heartbeat); the DB version needs a row cap or an age cap, run
from somewhere that is not the hot path. Decide it in this episode rather
than leaving it for B17 — a table with no retention policy is a disk-full
bug with a long fuse.

---

## Do NOT

- Do not keep `SessionReplaySince` as a second, parallel replay path.
- Do not reuse `messages.seq` as the event cursor, or write the event
  cursor into `messages.seq`. They count different things.
- Do not persist events from `main.py`'s writer loop. See 1.
- Do not claim a cost or a benefit that has not been measured.
- Do not touch `message_checkpoints` (B15). It preserves the in-flight
  reply TEXT across a crash and is a different mechanism from the event
  log; an episode that replaces one with the other has to say so
  explicitly and re-run B15's Done-when #3.

---

## Done when

1. Every envelope the episode decides to persist appears in `events` with
   its own id, its type, its correlation fields and a monotonic
   per-session cursor.
2. `events_since(session, seq)` returns exactly the events after the
   cursor, in order, with no gaps — **and B15's own Done-when #4 is
   re-run against this mechanism** (see below).
3. The measured cost of the write is recorded in the completed doc, with
   the fixture and the numbers.
4. `SessionReplaySince` is gone from `events.py`, from `cli.py`, from
   `types.go` and from `client.go`, and `afterReconnect` calls the new
   command.
5. Retention is implemented and its policy is stated.
6. A reconnect mid-stream rebuilds the UI from the log: the tool call
   that started and completed while the client was away is visible
   afterwards, which is the thing message-level replay could not do.

### This episode must RE-RUN B15's Done-when #4, not edit a doc

B15's Done-when #4 reads:

> `events_since(session, seq)` returns exactly the missing events, in
> order, with no gaps.

B15 verified that against `SessionReplaySince` and `messages.seq`, and
recorded in its completed doc that it did (and that it was an interim).
When this episode lands, **the underlying mechanism changes**, so B15's
evidence no longer describes the code. That is a re-test, not a
documentation update:

- re-run `backend\tests\manual_b15_lifecycle.py`'s `[4]` block against
  the event log — the `mechanism` assertion in it will fail by design,
  which is the signal that it is measuring the right thing;
- update it to assert the event cursor, keeping the no-gaps and ordering
  checks intact;
- state in B40's completed doc that B15's #4 was re-verified, with the
  new output, and amend B15's completed doc to point here.

A doc edit alone would leave the repo claiming a verification that was
performed against a mechanism that no longer exists — the exact drift
`design/completed/` exists to prevent.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b40"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    $env:POINT_MOCK   = "1"
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # 1 + 2: the log fills, and replay reads it back with no gaps
    $sid = (uv run python cli.py session new "b40" --provider mock --model mock-slow) -replace '\s.*',''
    uv run python cli.py send "count" --session $sid --script mock_slow
    sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select type, session_seq from events order by session_seq;"
    uv run python cli.py session events-since --session $sid --seq 0

    # 3: the measurement, both arms, same fixture
    uv run python cli.py bench event-log --script mock_slow

    # B15 #4, re-run against the new mechanism
    uv run python tests\manual_b15_lifecycle.py
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

---

## Handoff

State after this episode: the backend remembers what it said, not just
what it stored, and a reconnect can rebuild a view rather than
approximate it from the transcript. B15's interim command is gone and
F2's ladder calls the thing F2 was written against.

---

## Appendix — things B15 built or found that are not in its own Build section

Recorded here rather than dropped, per B15's own instruction to make
out-of-scope findings visible and decided later instead of invisible.
None of these is a B40 dependency; they are listed because B40 is the
first doc written after them and the alternative was a parking-lot entry
nobody reads.

### A1. `message_checkpoints` is a schema migration B15's Build section never asked for

B15's Done-when #3 wants a partial reply to survive a `Stop-Process
-Force`, and its Build section describes only the `tasks`-row half
(*"Mark them FAILED with code=backend_died, preserving any partial
assistant message with interrupted=True"*) — which presupposes a partial
that was never on disk. B15 therefore added **migration v7**, a
`message_checkpoints` table, a repository, a 500ms write on the token
path and a startup promotion step.

A separate table rather than a flag on `messages`, because a partial row
inside `messages` is immediately visible to `fetch_messages`,
`fetch_messages_keyset`, the context builder's history window and B21's
orphan filter — and the context builder is the one that actually breaks:
a second send while the first still streams would build its prompt from a
live partial as if it were a finished reply.

**Decided and built in B15.** Listed here because a migration is the
expensive retrofit this project deliberately front-loads, and a future
reader deserves to find out that v7 exists from a document rather than
from `.schema`.

### A2. `Shutdown` needed a third exit door in `main.py`, and signal handling needed building

`main.py` exited on exactly two conditions before B15: EOF on stdin, or a
dead stdout. Neither happens when Go asks politely, so `Shutdown` needed
`Runtime.shutdown_requested` (an `asyncio.Event`) and a third task in
`main()`'s `asyncio.wait`.

Signal handling **is** in B15's Build section (*"On SIGTERM / Ctrl+C /
quit… A second signal exits immediately"*) and did not exist: `Ctrl+C`
worked by accident of control flow (KeyboardInterrupt unwound
`asyncio.run` and `main()`'s `finally` did the teardown), and `Ctrl+Break`
— SIGBREAK, whose default action on Windows terminates the process
outright — had no path at all. **Measured during B15's gate: a slow
stream interrupted with `CTRL_BREAK_EVENT` died in 0.02s with the partial
reply still only in memory and nothing but the user's row on disk.** Now
every signal takes the same door the `Shutdown` command takes, and a
second one is `os._exit(130)`.

### A3. The daemon stdin thread makes interpreter finalization unsafe on every exit path except EOF

Also found by B15's gate, also measured:

```
Fatal Python error: _enter_buffered_busy: could not acquire lock for
<_io.BufferedReader name='<stdin>'> at interpreter shutdown, possibly
due to daemon threads
```

Exit code `0xC0000005`, on the `Shutdown` path. The reader thread is a
daemon (which is what stops a blocking `readline` from hanging the exit —
B15's own Windows note) and on every exit path that does not begin with
stdin closing it is still blocked inside that read, holding the lock on
`sys.stdin.buffer`. Finalization then cannot close that buffer and aborts
the process.

B15's fix is `main.exit_now()`: flush both streams, `logging.shutdown()`,
`os._exit(code)`. Everything durable has already happened by then
(`shutdown_runtime` closed the database and released the single-instance
lock; the outbox was drained to stdout).

**This is the open item of the three.** `os._exit` is correct but blunt,
and the alternative — a reader thread that does not block indefinitely,
so finalization is safe — is a real design change to
`_stdin_reader_thread` and the one part of the transport B33 hardened by
hand. Worth doing properly in **B17 (Hardening)** rather than in an
episode that needed the process to exit today. If a later episode adds
anything that must run at interpreter shutdown, it will find `os._exit`
in the way, and this is the note that explains why it is there.

### A4. `provider_pool` releases are process-wide, not workspace-scoped

B15's `WorkspaceContext` releases provider connection pools, and a pool
belongs to the process, not to a workspace. B15's rule: pools are only
released when the unload names a workspace the backend actually holds
sessions for, so unloading an unrecognised view cannot close a pool
another workspace is about to use. Teardown and the bench harness pass
`include_provider_pools=True` explicitly.

If B16's per-connection runtimes give each SSH connection its own
provider registry, this stops being a shared resource and the rule can
simplify. Until then it is a scoping decision worth knowing was made
deliberately.

### A5. Three PowerShell traps that crash a gate instead of failing it, and one other gate still exposed

Found while B15's own gate failed. **No product code was involved in
any of them** — all three are harness bugs, and the one that mattered
masked a real reading of the thing under test.

1. **A native command that writes nothing to stdout does not give you
   `$null`.** It gives AutomationNull, which `-eq $null` reports as true
   but which the comparison operators treat as an empty *collection*:
   `$empty -like "*x*"` and `$empty -match "x"` both return `Object[]`,
   not a Boolean. Handed to a `[bool]`-typed parameter that throws
   `Cannot convert value "System.Object[]" to type "System.Boolean"` —
   so the gate crashes at exactly the moment a check was about to fail,
   and reports a PowerShell type error instead of the product fact.
2. **`sqlite3.exe` has no busy timeout by default.** Every connection
   Point opens sets `busy_timeout = 5000` (`PRAGMAS` in
   `database\migrations.py`); a bare `sqlite3 $db "..."` in a script
   sets nothing, so a query landing while the backend is mid-write fails
   instantly with `Error: in prepare, database is locked (5)`. Measured
   while polling a live backend.
3. **Redirected native stderr is a *terminating* error under
   `$ErrorActionPreference = "Stop"`.** PS 5.1 wraps each stderr line in
   a `NativeCommandError`, so `sqlite3 "select * from nope"` killed the
   script rather than returning a failure the caller could report.

B15's fix is `backend\tests\gatelib.ps1`: `Invoke-Sql` (busy timeout,
stderr separated by ErrorRecord type, `$LASTEXITCODE` preserved, always
a scalar string), `Test-Truthy` (normalizes anything a check expression
can produce, so a check can fail but never crash) and `Wait-ForSql`
(wait for a precondition instead of guessing at it with `Start-Sleep`).

**`gate_b32.ps1` is still exposed and was deliberately NOT changed.**
Its `Assert-That` takes `[bool]$Ok`, and line 173's `$hbBefore = sqlite3
$db "SELECT heartbeat_at FROM file_locks;"` feeds an assertion — so an
empty or locked read crashes it the same way. It was left alone because
editing another episode's verification script in passing is what the
parking-lot entry for `manual_b28_demo_mode.py` exists to warn against
("an episode's diff touching another episode's verification script").
**Fix it where it is owned**, by dot-sourcing `gatelib.ps1` and
loosening the `[bool]`.

Audited and NOT exposed to a crash (all take an untyped `$ok`/`$what`,
so a bad shape mis-reports at worst): `gate_f13b.ps1`, `gate_f13a.ps1`,
`gate_f15.ps1`, `gate_f9d.ps1`, `manual_f18_verify.ps1`. Of those,
`manual_f18_verify.ps1` is the one worth converting next — nine
`sqlite3` call sites, including a `-split '|'` over a possibly-empty
capture whose downstream index reads would be silently wrong rather
than loud.
