# Episode B15 — Lifecycle and Resource Management ✅

**Release what is no longer used, refuse to release what is in use, and
survive being killed mid-stream with the conversation intact. Four parts.
(1) `WorkspaceUnload` releases project-scoped caches and idle provider
pools, and REFUSES — as a reporting no-op, never a cancel — anything a
live task holds, with the refusal rule applied per RESOURCE CLASS so
QUEUED and RUNNING are not one thing. (2) A partial reply reaches disk
while it is still arriving (`message_checkpoints`, migration v7, 500ms
cadence through `SerializedConnection`) and is promoted into a real
`interrupted` message at the next startup, which is what makes the
kill-mid-stream recovery claim true rather than aspirational. (3) The
heartbeat pings unconditionally (decisions.md D98) and Go's `busyGrace`
workaround is deleted with it. (4) `Shutdown` quiesces, acks exactly
once, and the process actually exits — plus the SIGINT/SIGBREAK/SIGTERM
handling B15's Build section asked for and the repo did not have.**

> Verified in a scratch `%APPDATA%`/`%LOCALAPPDATA%` profile per
> CLAUDE.md's hard rule, through one gate: `tests\gate_b15.ps1`, nine
> automated steps, **all PASS**. Aryan confirmed the gate green end to
> end and separately ran the one step a script cannot do — the real
> `Ctrl+C` keypress — observing the process exit instantly with the
> partial persisted (`interrupted = 1`, `outcome = cancelled`). Full
> logs in "Manual Test Log" below.

> **`SessionReplaySince` is an interim.** B15's Build section asked for
> `session.events_since(session_id, seq)` and this schema has no event
> log to serve it from. What shipped is message-level replay keyed on
> `messages.seq`, named for what it does. The real thing is
> **[B40](../../tasks/backend/B40-durable-event-log.md)**, which
> replaces this command — and B40 **owes a re-run of this doc's
> Done-when #4**, not a doc edit. See "The replay rung is deliberately
> not what F2 specified" below.

---

## Where the code went

```
backend\
├── main.py                  three exit doors now, not two. `Shutdown`
│                            joins stdin-EOF and dead-stdout via
│                            `Runtime.shutdown_requested` (an
│                            asyncio.Event) and a third task in main()'s
│                            asyncio.wait. Plus
│                            `install_signal_handlers` (SIGINT /
│                            SIGBREAK / SIGTERM -> the same door, second
│                            signal -> os._exit(130)) and `exit_now()`,
│                            which skips interpreter finalization --
│                            see "Two bugs the gate found"
├── bootstrap.py             `promote_orphaned_checkpoints` called
│                            immediately after `recover_orphaned_tasks`,
│                            under the same single-instance guard. Both
│                            halves or neither
├── cli.py                   + workspace unload <id> [--json]
│                            + bench unload-cycles --n N [--path DIR]
│                            + session replay [--session] [--since-seq]
│                            + send --shutdown-after SECONDS
│                            + session new --workspace <label>
│                              (CreateSession has accepted `workspace`
│                              since B2; nothing exposed it until an
│                              unload needed one to resolve)
├── database\
│   ├── migrations.py        SCHEMA_VERSION 6 -> 7; `_V7` creates
│   │                        `message_checkpoints`
│   └── message_checkpoint_repository.py   NEW. write/clear/
│                            clear_for_task/count, plus the standalone
│                            synchronous `promote_orphaned_checkpoints`
├── managers\
│   └── project_manager.py   + release_index(project_id) -> count
│                            + index_sizes() -> {project_id: entries}
│                            Neither decides WHETHER it may be dropped;
│                            that is lifecycle.py's rule
├── providers\
│   ├── base.py              + async close() -> int, a NO-OP default
│   ├── anthropic\provider.py  self._client becomes a lazy property over
│   │                          self._pool; close() drops the pool and the
│   │                          next call rebuilds it
│   └── openai\provider.py     same shape, same reason
├── runtime\
│   ├── lifecycle.py         NEW. ReleaseReport / Released / Refused /
│   │                        Absent / Holder, ResourceClass, the
│   │                        per-class refusal table,
│   │                        LiveReferenceRegistry, ProjectContext,
│   │                        WorkspaceContext. No DB handle, no
│   │                        knowledge of workspaces as a UI concept
│   ├── events.py            + WORKSPACE_UNLOAD, SESSION_REPLAY_SINCE
│   │                        + WORKSPACE_UNLOADED, SESSION_REPLAY,
│   │                          SHUTDOWN_COMPLETE
│   └── runtime.py           close() -> _teardown() (shared with
│                            Shutdown, guarded by _torn_down);
│                            _handle_workspace_unload,
│                            _handle_session_replay_since, a real
│                            _handle_shutdown; _maybe_checkpoint /
│                            _clear_checkpoint on the sink's text-delta
│                            and terminal paths; release_all();
│                            _heartbeat_loop pings unconditionally
├── streaming\
│   └── accumulator.py       + `usage` property, so a checkpoint can
│                            write the tokens spent beside the text
└── tests\
    ├── gatelib.ps1          NEW, shared. Invoke-Sql / Test-Truthy /
    │                        Wait-ForSql — see "Two bugs the gate found"
    ├── gate_b15.ps1         NEW. The nine-step gate
    ├── manual_b15_lifecycle.py     NEW. 30 checks, in-process
    ├── manual_b15_crash.ps1        NEW. the kill, Done-when #3
    ├── manual_b15_shutdown_wire.py NEW. a real main.py over real pipes
    └── manual_b15_heartbeat.py     NEW. D98, the unconditional ping

frontend\internal\protocol\
├── types.go     + CmdWorkspaceUnload, CmdSessionReplaySince
│                + EvWorkspaceUnloaded, EvSessionReplay,
│                  EvShutdownComplete
├── client.go    busyGrace DELETED; one watchdog budget in every state.
│                + lastSeq map, MarkSeen(), ReplayMarks(),
│                  trackSession(), RouteReplay; afterReconnect filled
└── client_test.go  + 5 tests (replay route, monotonic marks, the
                    reconnect request, silence-detected-during-a-task)
```

---

## The recovery sequence — exact order

**B16 gives each SSH connection its own runtime with this same
lifecycle. This ordering has to be right before a second dimension goes
on top of it, so it is written out rather than left to be read off the
diff.**

1. **Checkpoint write**, from `Runtime._maybe_checkpoint`, called from
   the sink's `chunk` branch and only on a `TEXT_DELTA` — `StreamAccumulator.apply`
   appends text deltas and nothing else does, so a progress or tool
   event has no new content to write. **Cadence 500ms**
   (`CHECKPOINT_INTERVAL_S`), time-based and not per-delta: a local
   model streams tens of deltas a second and a write per delta would
   put tens of serialized SQLite round-trips a second on the token path
   for a row that is overwritten immediately and read only after a
   crash. **The first delta writes immediately** and then arms the
   timer — a task killed 200ms in would otherwise leave nothing at all,
   which is exactly the hole this closes. The write is an UPSERT on
   `message_checkpoints.message_id` through the shared
   **`SerializedConnection`** (B22), so it lands between whole
   statements and never inside another coroutine's half-finished
   cursor. A failure here is logged and swallowed: this is insurance
   against a crash, not part of delivering the reply.
2. **The kill.** `Stop-Process -Force` is a SIGKILL — no handler runs,
   no terminal event fires, the accumulator dies with the process. What
   is on disk is whatever step 1 last wrote. Measured: the first
   checkpoint lands at **+0.90s** on an idle machine and **+1.42–1.65s**
   under load (see "Two bugs the gate found").
3. **`recover_orphaned_tasks(db_path)`** (B6, unchanged) runs first at
   the next startup, from `bootstrap.build_runtime`, under the
   `single_instance or holder is None` guard. Every task row still in
   `LIVE_STATUSES` (`pending`, `queued`, `running`) becomes
   `status='failed'`, `error_code='backend_died'`, `finished_at=now`.
4. **`promote_orphaned_checkpoints(db_path)`** runs immediately after,
   same guard, same place. For each surviving checkpoint it
   `INSERT OR IGNORE`s a real `messages` row: `role='assistant'`,
   content wrapped as `{"type": "text", "text": ...}`, **`interrupted=1`,
   `outcome='failed'`, `error_code='backend_died'`**, and `seq` assigned
   by the same `(SELECT COALESCE(MAX(seq),0)+1 …)` expression
   `insert_message` uses, so the promoted row sorts after anything that
   reached the session first. The row reuses the checkpoint's own
   `message_id` — the id the sink would have used — which is what makes
   the promotion idempotent and unable to produce a duplicate
   transcript entry. A checkpoint with empty content is deleted, not
   promoted, matching `finalize_interrupted`'s own refusal to persist
   an empty assistant message.
   **Tasks first, then messages, in that order and not the other way:**
   a promoted message is never the only evidence of a crash, so a
   reader can never find an interrupted reply belonging to a task that
   still claims to be running.
5. **The checkpoint is cleared ONLY after the real insert succeeds —
   never before.** On the live path that is
   `Runtime._clear_checkpoint(task.id)` in the sink's terminal branch,
   placed *after* `sessions.append_message(msg)` and gated on
   `persisted or msg is None`. A crash in the gap between the insert
   and the clear therefore leaves a checkpoint that promotion turns
   into an `interrupted` row — wrong-but-honest, and bounded. The
   opposite order leaves nothing at all, which is the failure the whole
   mechanism exists to remove. **A persist that FAILED deliberately
   keeps its checkpoint**: it is then the only copy of that text, and
   the next startup recovers it.

Nothing in this sequence reads or writes `messages` while a reply is in
flight, which is the reason `message_checkpoints` is a separate table
rather than a flag on `messages` — see Key decision 1.

---

## Key decisions

### 1. A separate table, not a flag on `messages`

A partial row inside `messages` is visible to every existing reader the
moment it is written: `fetch_messages` (GetSession), `fetch_messages_keyset`
(B18), B21's orphan filter, and the context builder's history window.
**The context builder is the one that actually breaks.** A second send
while the first still streams builds its prompt from history, so a live
partial would enter the next prompt as if it were a finished reply.
Filtering it back out of four queries is four chances to forget one;
keeping it out of `messages` until it is real is none.

Cost: migration v7, a table B15's Build section never asked for. Logged
as [B40 Appendix A1](../../tasks/backend/B40-durable-event-log.md).

### 2. The refusal rule is per RESOURCE CLASS, because QUEUED is not RUNNING

B15's Build section says "anything held by a `RUNNING` or `QUEUED` task",
as one rule. `Task.is_active()` folds PENDING/QUEUED/RUNNING into one
boolean, and that boolean is the wrong input, because the two states
hold different things:

| | holds |
|---|---|
| RUNNING | a provider connection, an open stream, an accumulator, a registered EditorContext, and the project data its context was built from |
| QUEUED | a lane reservation and its own `tasks` row. No stream (the executor calls `register_stream` when it STARTS, not at submit), no provider client, no accumulator |

So:

| class | blocked by |
|---|---|
| `STREAM` | PENDING, RUNNING |
| `PROVIDER` | PENDING, RUNNING |
| `PROJECT` | PENDING, QUEUED, RUNNING |

PENDING counts as RUNNING: it is the sub-millisecond gap between
construction and the loop's next turn (`task.py`'s own note), and a task
about to hold a stream must not have it dropped in that window.
`PROJECT` includes QUEUED because a queued turn's context is built from
that project's data when it starts — dropping the index is
correct-but-wasteful, and reporting it released while something waits to
use it is the half-truth this episode exists to remove.

`Holder` carries its task's **state**, so "held by 1 running, 1 queued"
is a different answer from "held by 2 tasks".

The state combination that matters — one QUEUED task with nothing
RUNNING — cannot be produced through the scheduler (a task queues
*because* a lane is full), so it is asserted directly against
`LiveReferenceRegistry` with constructed `Task` objects in
`manual_b15_lifecycle.py`. It is a real state: a lane freed by a cancel,
with the queue not yet pumped, is exactly that for a few milliseconds.

### 3. `WorkspaceContext` resolves a label; it does not own a registry

**The backend has no workspace registry and this episode did not invent
one.** `sessions.workspace` is a nullable grouping LABEL
(`managers\session.py`: 'e.g. a project folder or "chat" vs "coding"
workspace in the Go TUI'); nothing is keyed by it. `workspace.unload(id)`
is the shape F12 and B16 want, so the backend resolves
workspace → sessions → project ids and releases what is project-scoped.

Of the three things B15's Build section lists for `close()` to drop:

- **file tree** — real, but it is `ProjectManager._index_cache`, keyed by
  PROJECT id with a 30s TTL. It is the only cache in the backend, verified
  by grepping every `_cache`/`lru_cache` in `managers\`, `agents\`,
  `tools\` and `runtime\`.
- **open buffers** — live in Go (`frontend\internal\editor\`). The backend
  holds none; its only editor-adjacent state is
  `StreamManager._editor_contexts`, keyed by task id and already cleared
  at the terminal event.
- **symbol/parse caches** — do not exist. `editor_context.py` is
  `resolve_scope` with no parser, and this episode did not add one.

The last two are reported as **`absent`**, with the reason, rather than
omitted — "unload dropped 0 of 3 things and did not say which" is the
shape of a feature nobody can tell is broken. Hence three lists
(`released` / `refused` / `absent`) and not a boolean.

### 4. Provider pools are process-wide, so an unload only touches them when it holds sessions

A pool belongs to the process, not to a workspace. Unloading a workspace
id this backend has never seen must not reach out and close a pool
another workspace is about to use, so `include_provider_pools` defaults
to `bool(session_ids)` — an unrecognised workspace gets an empty report,
which is a *different answer* from a refusal. `release_all()` (teardown,
and the bench harness) passes `True` explicitly, because "release
everything" is what it means even on a profile with no sessions.

Only `anthropic` and `openai` hold a long-lived SDK client. `ollama` and
`openrouter` build a fresh `httpx.AsyncClient` per call inside
`async with`; `gemini` configures a module-level key and builds a
`GenerativeModel` per call. `BaseProvider.close()` is therefore a no-op
default that returns 0, and the two that override it **re-create the
client lazily on the next call** — `ProviderRegistry` keeps handing out
the same instance, so an adapter that closed and stayed closed would
turn an unload into a broken next send.

### 5. `Shutdown` and `Ctrl+C` are one code path

`_handle_shutdown` was a stub in the worst way: STOPPING, cancel, STOPPED
— no ack (breaking project_idea.md §4's one-ack-per-command), no process
exit, and `receive()` gating on `is_accepting_events()` so every later
event was rejected. A brick that never exits.

Both now go through `Runtime._teardown()` (guarded by `_torn_down`, so
`shutdown_runtime()` running afterwards is a no-op rather than a second
attempt to close a closed database): stop the heartbeat, drain
background work, cancel active tasks and await their cleanup bounded by
`TEARDOWN_TIMEOUT_S`, drain the scheduler's watchers, `release_all()`,
close the DB. `Shutdown` then adds **one** `shutdown.complete` and sets
`shutdown_requested`; a signal adds neither, deliberately — a signal is
not a command and nobody is waiting on a correlation id.

A second `Shutdown` arrives after `_torn_down`, so it is a no-op ack
carrying `already_stopping: true`. Two acks for two commands; still
exactly one per command.

### 6. The heartbeat pings unconditionally (D98)

The loop skipped its ping whenever `active_count() > 0`, on the reasoning
in its own docstring — "tokens are already proving liveness". True of a
streaming reply, false of a tool call: B10's `web_search` against a slow
endpoint emits `ToolStarted` and then nothing until `ToolCompleted`, and
in that window the backend produced no tokens *and* no pings. The wire
was indistinguishable from a deadlocked process during precisely the
operation most likely to deadlock.

`active_tasks` in the payload now carries the real count rather than a
hardcoded `0` — a ping during a task reporting 0 active would be a fresh
lie in place of the one just removed.

Go's `busyGrace` (60s) is **deleted**: it stretched the watchdog whenever
a task was in flight, so a genuine hang mid-task took ~72s to detect
instead of ~12s. One budget in every state, which is only correct
*because* of this change. The anchor cited in the task doc as
"D-heartbeat" does not exist; the decision is **D98**.

---

## The replay rung is deliberately not what F2 specified

F2's ladder calls `session.events_since(session, last_seq)` on reconnect,
and `Client.afterReconnect` was a logged no-op seam because that command
did not exist. Four facts, all verified on disk during this episode,
make it unservable as written:

1. **No `events` table.** Schema v7 has none, and nothing resembling one.
2. **Nothing persists an `OutgoingEvent`.** `Runtime._emit` puts the
   envelope on `self.outbox`; `main.py`'s writer serializes it to stdout
   and drops it.
3. **The envelope's `seq` cannot be the cursor.** It is a per-TASK stream
   counter in `StreamManager._sequences`, deleted by `close_stream()`,
   and `null` on every envelope that is not part of a token stream.
4. **`messages.seq` is a different quantity** — per-session message
   insertion order (migration v6, B18), durable and indexed.

So what shipped is **`SessionReplaySince`**: `{session, since_seq, limit}`
in, and out `{session, since_seq, mechanism: "messages.seq", replay:
true, messages, tasks, has_more}`. It answers what F2 actually needs
after a reconnect — *what does this transcript look like now* — plus the
authoritative task states so a task the client marked UNKNOWN can be
resolved to what happened, including a `backend_died` row written before
this process accepted an event. `mechanism` is on the wire so no
consumer has to guess which `seq` it is holding.

What it **cannot** answer is *what happened while I was gone*: a tool
call that started and completed, a progress step, a usage update, a
`FillProposal` pushed after a terminal event. Those are events, and
events are not stored.

**[B40](../../tasks/backend/B40-durable-event-log.md) is the real event
log and replaces this command.** Its task doc was written during this
session, before any of it was built, while the reasons were in front of
someone — including the two decisions it owes (the measured cost of a
write per envelope on the token path through `SerializedConnection`, and
what `seq` means once it has to be durable and per-session).

**B40 owes a RE-RUN of this doc's Done-when #4, not a documentation
edit.** Done-when #4 was verified here against `messages.seq`; when the
event log lands, the mechanism that verification measured will no longer
exist. `manual_b15_lifecycle.py`'s `[4]` block asserts
`mechanism == "messages.seq"`, which will fail by design — that failure
is the signal it is measuring the right thing. It must be updated to
assert the event cursor with its ordering and no-gaps checks intact, and
B40's completed doc must state that B15 #4 was re-verified, with the new
output.

### The frontend half

`afterReconnect` now sends `CmdSessionReplaySince` for every session the
client has seen traffic for, from that session's high-water mark.
Fire-and-forget on purpose: it runs on the supervisor goroutine *before*
`run()` starts reading, so a `Request` there would wait for a reply
nobody is collecting yet.

The mark comes from `MarkSeen(sessionID, seq)` — monotonic, so a late or
out-of-order call cannot walk it backwards. It is the consumer's to
report and not the protocol package's to derive: `seq` here is
`messages.seq`, which lives in a message *payload*, and that package
reads types and correlation fields only. A session becomes *known* from
`env.SessionID` alone (`trackSession`), which is a routing field — so a
reconnect knows which sessions to replay without reading a payload. A
session with no mark replays from 0, which is the honest default.

Replies route as **`RouteReplay`**, never `RouteSession`: a replay reply
carries a session id and a corr_id and would otherwise be
indistinguishable from live traffic, and a consumer that appended it
would duplicate every message it already had. A waiter still receives
it, so a consumer that asked explicitly is not punished.

`markInterrupted()` still runs, and still runs *before* the replies. A
reconnect means a NEW backend process (`restarted` is `connects > 1`, and
a connect is a handshake with a freshly dialled child), so every task
from the old process is dead whatever the replay says. What replay adds
is not "maybe it survived": it is the persisted partial text and the
authoritative FAILED/backend_died state.

---

## Bounded growth — what was measured, and what the one outlier is

`cli.py bench unload-cycles --n 20` loads and unloads a project context
20 times, measuring **what each cycle RETAINS** (a tracemalloc reading
taken before the load and after the release, per cycle) rather than a
cumulative total — a cumulative figure is dominated by the harness
itself, and reading a small steady climb in that as a leak in the cache
under test is how a bounded-growth harness ends up measuring itself.

**No RSS claim anywhere**, in the output, the logs or this doc. CPython
does not reliably return arenas to the OS, so a "watch memory drop" demo
would be dishonest. The measurable claims are:

- **`objects` is flat**: 26799 at cycle 1, then **26797 for cycles 2–20,
  identical across runs**. `-2` first-to-last, not monotonic.
- **Index size identical every cycle**: `[499] (stable)`.
- **Items dropped identical every cycle**: `[499] (stable)`.
- **Retained: median +0.1 KiB, 18/20 cycles under 1 KiB.**

### The one large outlier, attributed by name

One cycle reads **+938.8 KiB**, at **cycle 11, five runs out of five, to
the tenth of a KiB**. It is not jitter and it is not an unexplained
allocator step; both of those were my earlier readings and both were
wrong. Attributed with real `tracemalloc` snapshots and a `compare_to`
at the blip cycle:

```
+938.7 KiB  +1 blocks  pathlib.py:74: sys.intern(rel)

  project_manager.py:333   full = base / name        (_build_index, via asyncio.to_thread)
    pathlib.py:767           return self._make_child((key,))
      pathlib.py:502           return cls._flavour.parse_parts(parts)
        pathlib.py:74            parsed.append(sys.intern(rel))
```

**It is CPython's global interned-string table doubling, once.**
`sys.intern()` is called by `pathlib` while parsing path parts; that
table grows by doubling, and the resize is a single ~939 KiB block that
tracemalloc attributes to whichever `intern()` call tripped it. Note
`+1 blocks` — one allocation, not 939 KiB of accumulating data.

Three things establish it is not Point retaining anything:

- `objects` is flat straight through that cycle. A real 939 KiB
  retention by the index cache would move the object count.
- `indexed`/`dropped` are 499/499 on every cycle, so the cache is fully
  rebuilt and fully released each time.
- Raising tracemalloc's traceback depth **moved it to cycle 12**. The
  position is deterministic in the *cumulative* interned-string count —
  the same 499 files intern the same strings every cycle, so the
  threshold is crossed after the same amount of work every run — and is
  not a property of "cycle 11".

It cannot recur: the next doubling would need roughly twice as many
distinct interned strings, and the per-cycle set is identical.
`tracemalloc.get_tracemalloc_memory()` was 316 KiB at the end, so it is
not tracemalloc's own bookkeeping either.

**Deliberately not warmed away.** A warm-up cycle would move the resize
outside the reported window and make all 20 cycles read ~+0.1, which is
tidier and less useful: a harness that hides a real one-time cost is
worth less than one that names it. The bench prints the explanation
itself, at the point a reader would otherwise suspect a leak.

---

## Two bugs the gate found, both in the harness, neither in the product

Full account in
**[B40 Appendix A5](../../tasks/backend/B40-durable-event-log.md)**,
which is where it lives rather than being duplicated here. In short:

1. **A blind `Start-Sleep -Seconds 2` before the kill.** It had to cover
   interpreter start, bootstrap, the migrations check, agent seeding and
   the first text delta. Measured: the first checkpoint lands at
   **+0.90s idle (4 trials) and +1.42–1.65s loaded (2 trials)** — and
   step 3 runs straight after the 20-cycle benchmark, so loaded is the
   normal case and the margin was ~0.35s. The gate failed on machine
   load, not on anything about the code: the mechanism writes at +0.9s
   every time and the row survives a SIGKILL. Replaced with
   `Wait-ForSql`, which polls until a checkpoint row is on disk **and**
   has been updated at least once (proving the 500ms cadence fired more
   than once), then kills immediately — strictly stronger than the
   sleep, because the wait expiring is itself a real assertion.
2. **An `AutomationNull` crash that masked the first.** A native command
   writing nothing to stdout yields AutomationNull, which `-eq $null`
   reports as true but which the comparison operators treat as an empty
   *collection*: `$empty -like "*x*"` returns `Object[]`, not a Boolean,
   and a `[bool]`-typed parameter then throws `Cannot convert value
   "System.Object[]" to type "System.Boolean"`. So the gate crashed at
   exactly the moment a check was about to fail. It applies to `-match`
   equally, not just `-like`. Fixed generally in a new shared
   `tests\gatelib.ps1` (`Invoke-Sql` always returns a scalar string and
   passes the busy timeout `sqlite3.exe` lacks by default; `Test-Truthy`
   normalizes anything a check expression can produce).

`gate_b32.ps1` is the one other crash-capable script and was
**deliberately not touched** — editing another episode's verification
script in passing is what the parking-lot entry for
`manual_b28_demo_mode.py` warns against. Flagged in A5 with the audit of
every other `.ps1` that touches `sqlite3`.

---

## Manual Test Log (Episode B15)

`tests\gate_b15.ps1`, one scratch profile, nine automated steps:

| Step | Covers | Result |
|---|---|---|
| 0 | migrate to v7, `message_checkpoints` present | PASS |
| 0b | the **v6 → v7 upgrade path**, wound back and re-migrated — not just a fresh create | PASS |
| 1/2/4/5/7 | `manual_b15_lifecycle.py`, 30 checks in-process | PASS |
| 5 | `bench unload-cycles --n 20` | PASS |
| 3 | `manual_b15_crash.ps1` — the kill and the recovery | PASS |
| 6/7 | `manual_b15_shutdown_wire.py` — a real `main.py` over real pipes | PASS |
| 7b | `send --shutdown-after 1.5` — the ack and the partial | PASS |
| D98 | `manual_b15_heartbeat.py` — ping during a task, real `active_tasks` | PASS |
| Go | `go test ./frontend/...` (all 10 packages) | PASS |

Done-when by Done-when:

1. **Unload a workspace whose task is running → the task continues and
   completes; its result is retrievable.** PASS. The task was confirmed
   `running` at the moment the unload arrived, the terminal event was
   `TaskDone` (not `TaskCancelled`), and the assistant row was present
   and not `interrupted` afterwards.
2. **`close()` on a context holding a running task's resources is a
   no-op that reports what it holds.** PASS. `file_index[…]` came back
   under `refused` with a holder whose `state` was `running`, and
   `fully_released` was `false`. With nothing running, the same index
   was `released` **with a count** (`items > 0`).
3. **Kill mid-stream → `FAILED`/`backend_died` with the partial
   surviving as `interrupted`.** PASS. Before the restart: a checkpoint
   on disk holding the streamed text, no assistant row yet, task row
   still `running`. After: task `failed`/`backend_died`, a promoted
   assistant row with `interrupted=1`, `outcome=failed`,
   `error_code=backend_died`, non-empty content matching what was
   actually streamed, and the checkpoint consumed. `tasks list` correct
   immediately after the restart, and the turn replayable.
4. **`events_since(session, seq)` returns exactly the missing events, in
   order, with no gaps.** PASS **against `SessionReplaySince` and
   `messages.seq`** — exactly the messages after the mark, consecutive
   seqs, `mechanism` stated on the wire, task states included.
   **B40 must re-run this.**
5. **20 load/unload cycles: object counts and cache sizes return to
   baseline; RSS may not drop.** PASS — see "Bounded growth" above.
6. **`Ctrl+C` during a stream leaves a consistent DB, a persisted
   partial, and the process actually exits.** PASS. Automated via
   `CTRL_BREAK_EVENT` to a child in its own process group (the only way
   to deliver a genuine console interrupt to one process from a script
   on Windows; it reaches the same handler): exited in 0.05s, rc 0,
   partial persisted, user row intact. **Aryan separately confirmed the
   real keypress**: process exited instantly, partial persisted with
   `interrupted = 1`, `outcome = cancelled`.
7. **A `Shutdown` during a stream returns exactly one
   `shutdown.complete`, persists the partial, closes the DB, and exits;
   a second acks again and does not error.** PASS. One ack, it was the
   **last** envelope on the wire, `already_stopping: false`, the process
   exited with rc 0, and the partial was persisted `interrupted`. The
   second `Shutdown` acked with `already_stopping: true`.

Regression checks on the paths this episode touched:
`manual_transport_encoding.py` 14/14 (it spawns `main.py`, which this
episode changed), `manual_concurrent_completion.py` ALL PASS,
`manual_b19_silence_timeout.py` ALL PASS, `manual_b12_concurrency.py`
ALL PASS with its own config (`[limits.ollama] max_concurrent_tasks = 1`
— a first run without it reported 3 RUNNING, which is the test's
documented config dependency, not a regression). `gofmt -l` and
`go vet ./frontend/...` clean.

---

## What the next episode inherits

### B16 (SSH Serving) — depends on B15 and gets a per-connection lifecycle

The release path is reached through a **type** (`WorkspaceContext` /
`ProjectContext`) rather than a method on `ProjectManager`, specifically
so B16 can instantiate one per connection. `LiveReferenceRegistry` takes
a `Callable[[], Iterable[Task]]`, so a per-connection registry is a
different callable and not a different rule.

Two things B16 must know:

- **The refusal rule is derived, not tracked.** `TaskManager._tasks`
  already *is* the live set; a second copy updated by hand on every
  transition is a second copy that can disagree. Per connection, that
  means handing the registry that connection's task view — not
  maintaining a parallel reference count.
- **Provider pools are shared today** (Key decision 4). If B16 gives
  each connection its own provider registry, pools stop being a shared
  resource and `include_provider_pools`'s default can simplify.

### B40 — the event log

Owns the replacement of `SessionReplaySince` and the re-run of Done-when
#4. Its appendix also carries four findings from this episode that B15's
own Build section never asked for: A1 (migration v7), A2 (the third exit
door and the signal handling), A3 (`os._exit` at finalization — **the
one genuinely open item**, properly B17's), A4 (provider-pool scoping)
and A5 (the PowerShell traps and the `gate_b32.ps1` exposure).

### A3 is the open one, and it is B17's

`main.exit_now()` skips interpreter finalization because the daemon
stdin reader thread holds the `sys.stdin.buffer` lock on every exit path
except EOF, and finalization aborts the process trying to take it
(`Fatal Python error: _enter_buffered_busy`, exit code `0xC0000005`,
measured on the `Shutdown` path). `os._exit` is correct but blunt. The
alternative — a reader thread that does not block indefinitely, so
finalization is safe — is a real design change to
`_stdin_reader_thread`, the one part of the transport B33 hardened by
hand. **If a later episode adds anything that must run at interpreter
shutdown, it will find `os._exit` in the way; A3 is the note that
explains why it is there.**

### The exact kill command, for the demo

```powershell
$proc = Start-Process -FilePath "D:\Projects\Point\.venv\Scripts\python.exe" `
    -ArgumentList @("cli.py", "send", "...", "--session", $sid) `
    -WorkingDirectory "D:\Projects\Point\backend" -PassThru -NoNewWindow
# ... wait for the precondition, never a blind sleep ...
Stop-Process -Id $proc.Id -Force
```

`-PassThru` means `$proc.Id` **is** the `python.exe` running Point, so
nothing has to be matched by name or command line. The venv interpreter
is invoked directly rather than through `uv run` for that reason — `uv
run` is the process PowerShell hands back, and the python it spawns
underneath is a child whose pid the script never learns. The
`Get-CimInstance Win32_Process | Where-Object CommandLine -like "*main.py*"`
filter in B15's task doc is the fallback for a process someone launched
by hand, not the one to use from a script.
