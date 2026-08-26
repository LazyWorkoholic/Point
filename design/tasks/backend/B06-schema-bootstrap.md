# B6 — Schema v2, Envelope v1, Bootstrap, Secrets

**Phase:** P1 · **Batchable:** no · **Depends on:** B5

> **Unpaired from B7 on 2026-08-19.** This episode grew a new `tasks` table, a
> `sessions` rebuild, the §4 envelope, and six B1–B5 correctness fixes. Sharing a
> session with B7 would push both past the point where one verification pass is
> trustworthy.

> Rewritten 2026-08-19 against the code as it actually is. The previous version
> assumed a `tasks` table that does not exist, told you to add columns that were
> already there, and told you to build two commands that were already built.
> Anything below that reads as "already done" has been verified on disk.

## Purpose

Three things, all expensive to retrofit and cheap to do now.

**Schema.** Get every column later episodes need into the ground, including a
durable `tasks` table that has never existed. Several columns here stay unused
for weeks — that is intentional. Schema changes are the expensive retrofit;
forms and logic are not.

**Envelope.** `project_idea.md` §4 specifies a wire envelope the backend has
never emitted. Adopt it now, while **no Go client exists** — F2 has not been
written, so there is exactly one side to change and zero compatibility burden.
This window closes the moment F2 lands.

**Bootstrap.** Make the app able to start from nothing, put secrets somewhere
safe, and give the backend a CLI so every later episode can be verified without
Go.

---

## Build

### 1. Relocate the database

Right now the DB lives at `backend\runtime.db` — inside the source tree, where
it will be committed by accident and will break the moment the app is packaged.

```
new default:  $env:LOCALAPPDATA\Point\runtime.db
override:     config `database.path`, then the env var POINT_DB
```

Add a **one-time migration on startup**: if the new location has no DB and
`backend\runtime.db` exists, copy it across and log the move. Do not delete the
old file — leave it and let Aryan remove it once he is satisfied.

The existing `backend\runtime.db` holds 12 sessions and 42 messages. They must
survive.

### 2. Migration runner

Versioned, forward-only, recorded in `schema_migrations(version, applied_at)`.
Runs automatically on startup. **Copy the database file before every migration**
to `runtime.db.bak-<version>` beside it. A fresh DB and the existing one must
both arrive at v2.

### 3. The `tasks` table — new, and the reason this episode is not just columns

Tasks have always been in-memory only: `TaskManager._tasks`, a dict that dies
with the process. That is why `git_checkpoint_sha` has nowhere to live, why a
crash leaves no trace, and why `ListTasks` cannot answer anything about
yesterday.

```sql
tasks(id TEXT PRIMARY KEY,
      session_id          TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      agent_id            TEXT NULL,
      project_id          TEXT NULL,
      connection_id       TEXT NULL,
      kind                TEXT NOT NULL,
      status              TEXT NOT NULL,
      error_code          TEXT NULL,
      git_checkpoint_sha  TEXT NULL,
      created_at          REAL NOT NULL,
      started_at          REAL NULL,
      finished_at         REAL NULL)
```

**`TaskManager._tasks` stays** as the in-memory index. The table is a durable
**mirror**, written through a new `database\task_repository.py` in the same shape
as `session_repository.py` — raw SQL, no business rules.

- Row written on `create_task()`, before `task.start()`.
- Row updated on **every** state transition, from the one place transitions
  already happen: `Task._run()`'s state assignments and `Task.cancel()`.
- `status` uses the **existing `TaskState` values** — `pending`, `running`,
  `completed`, `failed`, `cancelled`. There is no `queued` state in the enum;
  `pending` is that state. Do not add one.
  **`QUEUED` arrives in B12**, where it means something `PENDING` does not:
  blocked behind a per-provider concurrency cap. Do not add it early, and do not
  re-litigate this — the distinction is deliberate.
- `error_code` is a short stable slug, not the free-text message. `Task.result`
  already carries the human string; this column is for what Go can branch on.
  First and only member in v1: `backend_died`. Add more only when something
  actually needs to branch on them.
- `agent_id`, `project_id`, `connection_id`, `git_checkpoint_sha` are all NULL
  in v1. They are the hooks for B9, B7, SSH, and the parked Coding Assistant.

**Crash recovery on startup.** Before accepting any event, mark every row still
`pending` or `running` as `failed`, with `error_code = 'backend_died'` and
`finished_at = now()`. Log the count. A task cannot survive the process that ran
it, so a row in a live state at startup is by definition a corpse.

### 4. Other new tables

```sql
projects(id TEXT PRIMARY KEY, name, root_path, created_at, last_opened_at)

agents(id TEXT PRIMARY KEY, name, description, builtin INTEGER,
       provider, model, effort, system_prompt, tools_json,
       reads_session INTEGER, reads_project INTEGER, reads_global INTEGER,
       writes_proposals INTEGER, stm_window INTEGER,
       max_steps INTEGER, max_tool_calls INTEGER, timeout_s INTEGER,
       default_attach_mode TEXT, created_at, updated_at)

memory_records(id TEXT PRIMARY KEY, content TEXT, tags_json TEXT,
       status TEXT, source TEXT, agent_id TEXT,
       origin_session_id TEXT, origin_task_id TEXT,
       embedding BLOB NULL, created_at, updated_at)

memory_scopes(memory_id TEXT, scope TEXT, scope_id TEXT,
       PRIMARY KEY(memory_id, scope, scope_id))

context_attachments(id TEXT PRIMARY KEY, session_id TEXT,
       source_type TEXT, source_id TEXT, mode TEXT,
       recorded_mtime INTEGER, recorded_size INTEGER, created_at)

file_locks(project_id TEXT, rel_path TEXT, holder TEXT,
       acquired_at, heartbeat_at, PRIMARY KEY(project_id, rel_path))

usage_records(id TEXT PRIMARY KEY, session_id, task_id, agent_id,
       provider, model, input_tokens INTEGER, output_tokens INTEGER,
       cost_micros INTEGER NULL, estimated INTEGER, created_at)

presence(connection_id TEXT PRIMARY KEY, user_name, connected_at, heartbeat_at)
```

`status` ∈ `pending|accepted|rejected` · `source` ∈
`auto_extractor|user_card|user_manual` · `scope` ∈ `session|project|global` ·
`mode` ∈ `full|reference|detached`.

### 5. Changes to the `sessions` table

`project_id` **already exists**. Do not add it again.

Genuinely new columns, all nullable:

```sql
sessions + effort TEXT NULL, created_by TEXT NULL, archived_at REAL NULL
```

**And one rebuild.** `provider` and `model` are currently `NOT NULL`. They must
become nullable, because NULL is how a session says *inherit from global config*
— which is the whole point of the resolution order in `project_idea.md` §10.
SQLite has no `ALTER COLUMN`, so this is the standard rebuild: create
`sessions_new` with the target shape, `INSERT INTO sessions_new SELECT ...`,
drop, rename. Do it inside the v2 migration, with §2's pre-migration backup as
the safety net.

Matching change in `managers\session.py`: `provider` and `model` become
`Optional[str]`. They are currently required fields on the `Session` dataclass
and required positional arguments to `Session.new()`, so this is a signature
change. `SessionManager.create` and `Runtime._handle_create_session` both pass
them and both keep working, but the literal defaults in `_handle_create_session`
(`"claude"` / `"claude-sonnet-5"`) become `None`. See §14 for why those defaults
are actively harmful.

Nothing resolves NULL yet. **B8 owns resolution.** This episode only makes NULL
storable.

### 6. Envelope v1

`project_idea.md` §4 specifies this envelope. The backend has never emitted it.
Adopt it now.

```json
{ "v": 1, "id": "...", "type": "task.token", "corr_id": "...",
  "session_id": "...", "task_id": "...", "connection_id": "...",
  "trace_id": "...", "seq": 1, "ts": "...", "payload": {} }
```

**This is a serialization-boundary change only.** `IncomingEvent` and
`OutgoingEvent` keep their current fields and their current names. What changes
is `OutgoingEvent.to_dict()` and `IncomingEvent.from_dict()`. Nothing between
them moves, and no handler signature changes.

Concretely, today's outgoing shape is `{event, request_id, timestamp, task_id?}`
with payload keys **flattened onto the top level**. After this episode:

| today | envelope v1 |
|---|---|
| `event` | `type` |
| `request_id` | `corr_id` |
| `timestamp` | `ts` |
| `task_id`, present only sometimes | `task_id`, always present, may be null |
| payload keys flattened to top level | nested under `payload` |
| — | `v`, `id`, `session_id`, `connection_id`, `trace_id`, `seq` |

- `v` is `1`. `id` is a fresh UUID per envelope. `corr_id` carries the causing
  request's `id` — that is how Go routes events.
- `connection_id` and `trace_id` are **accepted on input, echoed on output,
  otherwise unused.** They are the SSH and tracing hooks.
- `seq` is the per-task sequence number, so a gap is detectable rather than
  silently corrupting. `StreamManager.next_sequence()` already produces exactly
  this number per task — it is currently buried inside the `stream_event`
  payload. Lift it to the envelope; leave it in the payload too rather than
  breaking `StreamAccumulator`.

**`hello` handshake.** On startup the backend emits `hello` carrying `v` and
`capabilities`, before anything else. With nothing configured it must still
emit, reporting `capabilities: {providers: []}` so the UI can explain itself
rather than hang. A version mismatch refuses to proceed and shows **both**
versions.

**`ping` heartbeat.** Every 5s while idle. This is also the hook for the
crash-recovery path on the Go side — two missed pings catches a *hung* backend,
which process-exit detection misses entirely.

Record the final envelope in `design\decisions.md` as implemented, not
aspirational.

### 7. The cancel ack stops being terminal

`project_idea.md` §4 rule 3 promises exactly one terminal event per task. Today a
cancel produces **two** `TaskCancelled` — one as the ack from
`_handle_cancel_task`, one from the sink as the task's real terminal event.
B05's manual test log records this and calls it expected. It is not.

- `_handle_cancel_task` now emits **`task.cancel.accepted`**, a non-terminal ack
  carrying `{"accepted": bool}`. `accepted: false` when the task was already
  finished or unknown — which is also the case that currently emits a
  terminal-shaped event for a task that already sent `TaskDone`.
- Only the sink emits the terminal `TaskCancelled`.
- New `OutgoingEventType` member. `TASK_CANCELLED` keeps its meaning and drops to
  a single producer.

After this, "exactly one terminal event per task" is true and F2 may rely on it.

### 8. Additive changes to frozen episodes

Do these here, inside this episode, not as a separate pass.

| From | Change | Required by |
|---|---|---|
| B3 | a `provider` tag on reasoning/thinking blocks, so they can be filtered per provider on send. Never deleted, only filtered. | D59 |
| B4 | a normalized `effort` field (`off`/`low`/`medium`/`high`) on the provider request, translated or ignored by each adapter, plus `supports_effort` on `ModelCapabilities`. Add `supports_caching` while you are in `providers\models.py`. `supports_tools` **already exists** — leave it alone. | D58 |
| B4 | `MockProvider` — replays a scripted event sequence from a fixture file. Registered only when `POINT_MOCK=1`. **Never selectable from the UI.** Without it, every later test needs a live model. | D54 |

**Removed from this episode:** `session.archive` and `session.delete`. Both
already exist end to end — `ArchiveSession` / `DeleteSession` event types,
handlers registered in `Runtime._register_handlers`, `SessionManager.archive()`
and `.delete()`, and `SessionRepository.delete()` backed by the `ON DELETE
CASCADE` on `messages`. Verified on disk. Nothing to build.

What is actually missing there is **`unarchive`** — see §9.1.

### 9. Correctness fixes, folded in

Each is small, each is in a file this episode already opens, and each is
something a later episode would otherwise inherit as a surprise.

1. **`unarchive` has no event type and no handler.** `SessionManager.unarchive()`
   exists and is unreachable from Go. Add `UNARCHIVE_SESSION` to
   `IncomingEventType`, `SESSION_UNARCHIVED` to `OutgoingEventType`, and
   `_handle_unarchive_session` alongside `_handle_archive_session`. Clear
   `archived_at` (§5) on the way through.

2. **Gemini leaks its stream on cancel.** B05 added `async with stream:` to
   OpenAI and OpenRouter, and noted that Ollama and Anthropic already had
   equivalent teardown through their own client context managers. Gemini was
   never mentioned and never fixed — `providers\gemini\provider.py` iterates
   `generate_content_async(..., stream=True)` bare, with no context manager and
   no explicit close. Give it the same teardown guarantee the other four have.

3. **`Runtime.__init__` assigns `self.outbox` twice** — once unbounded, then
   again with `maxsize=1000`. The bounded one wins, so behaviour is already
   correct; delete the dead first assignment.

4. **`StreamEventType` has three dead members** — `COMPLETED`, `FAILED`,
   `CANCELLED`. Nothing produces them (`StreamAdapter._CHUNK_TYPE_MAP` omits
   them) and nothing consumes them (`_make_stream_sink._EVENT_TYPE_MAP` omits
   them). Terminal semantics live on the task, not on the stream. Delete all
   three. Leave `THINKING_DELTA`, `STATUS_CHANGED`, `PROGRESS_UPDATED`,
   `TOOL_STARTED`, `TOOL_COMPLETED` — those are genuinely reserved for B9/B10.

5. **The backend never exits when Go closes the pipe.** `read_stdin_loop`
   returns on EOF; `write_stdout_loop` is `while True: await outbox.get()` and is
   never cancelled; `asyncio.gather` therefore waits forever. The DB connection
   is never closed either. Fix as part of §13: on stdin EOF, drain what is left
   in the outbox, cancel the writer, then tear down. **This blocks §15** — a lock
   file held by a hung process that nothing kills is worse than no lock at all.

6. **`TaskManager.sweep_finished`'s docstring claims "Called periodically by
   Runtime."** Nothing calls it. Either wire it into a periodic sweep or correct
   the docstring — do not leave the comment lying. Now that §3 gives tasks a
   durable row, sweeping the in-memory dict loses nothing.

### 10. Reserved, not implemented

Task states `PAUSED` and `AWAITING_APPROVAL` added to the `TaskState` enum and
accepted by `tasks.status`; the `approval.*` event namespace documented as
reserved in `decisions.md`. Nothing produces either.

`trace_id` is no longer merely reserved — §6 puts it on the envelope.

### 11. SQLite configuration

`journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, applied on **every**
connection in `database\connection.py`.

Today only `foreign_keys` is set, and the live DB reports `journal_mode=delete`.
WAL is not a nice-to-have here: `project_idea.md` §12 makes shared-WAL the entire
basis of SSH topology B, and "Done when #5" cannot pass without it.

### 12. Config, credentials, secrets

```
config          $env:APPDATA\Point\config.toml      user-owned, NEVER rewritten
credentials     $env:APPDATA\Point\credentials.toml keys only, gitignored
state           $env:LOCALAPPDATA\Point\state.json  machine-written
database        $env:LOCALAPPDATA\Point\runtime.db
logs            $env:LOCALAPPDATA\Point\logs\point.log
```

Key resolution: **env var → `credentials.toml` → not found**. Written
temp-file-then-rename. On Windows there is no `chmod`, so either restrict the ACL
to the current user via `icacls`, or — acceptable for v1 — document that
permissions are inherited from the user profile directory.

**Masking helper**: renders `sk-...4f2a`. Applied in logs, error messages, and
any payload sent to Go. No code path may emit a raw key.

### 13. Shared bootstrap — extract before you add

`main.py` today fuses four responsibilities. Only the first is shareable with a
CLI:

1. Runtime construction — `await Runtime.create(db_path="runtime.db")`
2. stdio transport in — `_stdin_reader_thread` + `read_stdin_loop`
3. stdio transport out — `write_stdout_loop`
4. process entry — `asyncio.run(main())` + the `KeyboardInterrupt` guard

A CLI must **not** reuse (2) or (3): its input is `argv` and its output is
human-readable text on stdout, which would collide head-on with the JSON-lines
protocol channel.

Extract (1) into a new `backend\bootstrap.py`:

```
resolve_db_path()                    POINT_DB -> config database.path -> LOCALAPPDATA
                                     plus the one-time copy from backend\runtime.db
run_migrations(db_path)              standalone; callable without a Runtime
async build_runtime() -> Runtime     dirs, writability, migrations, crash recovery,
                                     single-instance lock, logging, then Runtime.create()
async shutdown_runtime(runtime)      cancel active tasks, close the DB, release the lock
```

`main.py` afterwards is `build_runtime()` + the two stdio loops +
`shutdown_runtime()`. Its diff should be small: three lines of construction
collapse into one call.

`resolve_db_path()` and `run_migrations()` must work **without** a Runtime —
`doctor` has to report on a missing or broken DB, and `migrate` runs before a
Runtime can safely open one.

**The reuse test:** if `cli.py` contains the string `Runtime.create`,
`open_database`, or any db-path literal, it duplicated instead of reusing. Start
over.

### 14. CLI harness

`backend\cli.py`, run as `uv run python cli.py <command>` from inside `backend\`.
It builds its Runtime through §13's `build_runtime()`.

**How a CLI talks to `Runtime`.** There is no request/response method.
`receive(raw)` returns `None`; every result leaves via `runtime.outbox` as an
`OutgoingEvent` carrying `corr_id`. So one helper does all the work:

```
async def call(runtime, type, timeout, **payload) -> list[dict]
```

It assigns an envelope `id`, awaits `runtime.receive(...)`, then drains
`runtime.outbox` collecting envelopes whose `corr_id` matches, stopping on the
expected terminal event or on timeout. Three constraints, all load-bearing:

- **Draining is mandatory, not optional.** `outbox` is `maxsize=1000` and
  `_emit` **awaits** `.put()`. A CLI that builds a Runtime and never drains will
  block the producer once the queue fills. Non-matching envelopes are discarded,
  or printed under `--verbose`.
- **`receive()` never raises to the caller.** Errors come back as `Error`
  envelopes on the outbox. Exit codes derive from the event type, never from a
  caught exception.
- **`send` is asynchronous.** `_handle_send_message` returns after emitting
  `TaskStarted`; the reply arrives later as `TextDelta`* then `TaskDone`. So
  `cli.py send` must keep draining past its own ack until it sees the terminal
  event for that `task_id`. This is §7's guarantee being cashed in.

`cli.py` never reads stdin and never writes JSON lines to stdout.

**`send` provider default.** `CreateSession` currently defaults to
`provider="claude"`, which `_PROVIDER_ALIASES` resolves to `anthropic` — a
provider that **does not register on a keyless profile**, which is exactly the
profile check #1 below sets up. So:

```
cli.py send  ->  ollama if registered, else echo.  Never claude.
```

Same for `cli.py session new`'s default. `echo` is the existing no-network
`_demo_echo_body` path and is deliberately not a registered provider, so it
always works.

Subcommands needed by later episodes' verification:

```
doctor · migrate · session new|list · send <text> · tasks list
task cancel <id> · context report · attach <path> · memory list
project init|resolve|list-files|read-file
```

Implement `doctor`, `migrate`, `session`, `send`, and `tasks list` now — the last
because §3 finally gives it something durable to read. Add the rest as their
episodes land.

### 15. Single-instance lock

A lock file in the state dir holding the pid. A second instance refuses with
`Point is already running (pid 4821)` and exits non-zero. Depends on §9.5 — a
process that never exits never releases its lock.

### 16. Logging

Rotating file at `$env:LOCALAPPDATA\Point\logs\point.log`. **stderr only** —
stdout is the protocol channel. A `--verbose` flag. Key redaction (§12) applied
to every record.

There is currently **no logging anywhere in `backend\`** — no `logging` import,
no stderr writes. This is the first. The silent-drop paths that already exist are
the first candidates: malformed JSON in `read_stdin_loop`, and §3's
crash-recovery count.

### 17. `point doctor`

Prints: `VERSION` contents and whether Go and Python agree · Python version ·
dependency check · DB path and schema version · config and credentials paths and
whether they exist · providers with credentials present · Ollama reachability and
models found · the log path.

Must run and exit 0 on a machine with **no config at all**.

---

## Do NOT

- Implement memory *logic*. Tables only.
- Implement agent execution, retrieval, or usage aggregation.
- Seed built-in agents (B9).
- Resolve a NULL `provider`/`model` against config. §5 makes NULL storable; B8
  makes it mean something.
- Change how `backend\` imports work. Everything still runs from inside
  `backend\` with `from runtime.runtime import ...`.
- Touch B1–B5 behaviour beyond §7, §8, and §9.
- Refactor `Task`, `TaskManager`, or `Dispatcher`. §3 mirrors their transitions;
  it does not restructure them.

---

## Done when

1. A fresh DB migrates to v2 with every table and column above present.
2. The existing `backend\runtime.db` is copied to the new location on first run
   with its 12 sessions and 42 messages intact; the old file is left in place.
3. A `.bak-<version>` file exists beside the DB after migrating. Re-running
   `migrate` says "already at version 2" and does not churn a second backup.
4. `sessions.provider` and `sessions.model` accept NULL, and existing rows kept
   their values through the rebuild.
5. Two processes write concurrently without `database is locked`.
6. A task's row appears in `tasks` on creation and reaches a terminal `status`
   with `finished_at` set.
7. Killing the backend mid-stream and restarting marks the orphaned row `failed`
   / `backend_died`, and logs the count.
8. Every envelope on the wire carries `v`, `id`, `type`, `corr_id`, `ts`, and a
   nested `payload`. `hello` arrives before anything else. `ping` arrives while
   idle.
9. Cancelling a task produces **exactly one** terminal event, preceded by a
   non-terminal `task.cancel.accepted`.
10. `doctor` runs on a machine with no config, prints a complete report, exits 0.
11. A second instance prints the pid message and exits non-zero.
12. Closing stdin makes the backend exit cleanly instead of hanging.
13. A key in env, a key in `credentials.toml`, and no key at all all resolve
    correctly, and the raw key appears nowhere in the log.
14. `POINT_MOCK=1` makes `MockProvider` available; without it, it is absent.
15. `cli.py send` works on a profile with no API keys.

---

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend
```

**1. doctor on a clean profile**

```powershell
Rename-Item "$env:APPDATA\Point" "Point.bak" -ErrorAction SilentlyContinue
Rename-Item "$env:LOCALAPPDATA\Point" "Point.bak" -ErrorAction SilentlyContinue
uv run python cli.py doctor
```
**Expect:** a full report, no traceback, exit 0, and a line saying no provider is
configured.

**2. migration, backup, tables**

```powershell
uv run python cli.py migrate
Get-ChildItem "$env:LOCALAPPDATA\Point"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" ".tables"
```
**Expect:** `runtime.db` plus a `.bak-` file. The table list contains `tasks`,
`projects`, `agents`, `memory_records`, `memory_scopes`, `context_attachments`,
`file_locks`, `usage_records`, `presence`, `schema_migrations`, `sessions`, and
`messages`.

**3. old data survived the move**

```powershell
uv run python cli.py session list
```
**Expect:** the 12 sessions that were in `backend\runtime.db`.

**4. nullable provider/model survived the rebuild**

```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select count(*) from sessions where provider is not null;"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "pragma table_info(sessions);"
```
**Expect:** the first returns 12 — nothing was blanked. In the second, the
`provider` and `model` rows show `notnull = 0`.

**5. WAL**

```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "pragma journal_mode;"
```
**Expect:** `wal`.

**6. idempotent migrate**

```powershell
uv run python cli.py migrate
```
**Expect:** "already at version 2", no error, no second backup.

**7. a task leaves a durable row**

```powershell
uv run python cli.py send "count to five"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select id, kind, status, error_code, finished_at from tasks order by created_at desc limit 1;"
```
**Expect:** the reply streams to the terminal, and the row shows
`status = completed`, `error_code` empty, `finished_at` set. On a keyless profile
this runs against ollama or echo — never claude.

**8. crash recovery**

Start the backend and send a long generation:

```powershell
uv run python main.py
```
In a second terminal, while it is still streaming:
```powershell
Stop-Process -Name python -Force
uv run python cli.py tasks list
```
**Expect:** the interrupted task shows `failed` / `backend_died`, and the startup
log records how many rows it recovered.

**9. the envelope**

```powershell
'{"v":1,"id":"t1","type":"ListSessions","payload":{}}' | uv run python main.py
```
**Expect:** a `hello` envelope first. Every line is a complete envelope with `v`,
`id`, `type`, `corr_id`, `ts`, and a nested `payload` — no flattened top-level
payload keys. The `SessionList` reply carries `corr_id` of `t1`.

**10. one terminal event per cancel**

```powershell
uv run python cli.py send "write a long essay" --cancel-after 2
```
**Expect:** one `task.cancel.accepted` with `accepted: true`, then exactly one
`TaskCancelled`. Never two. A handful of already-in-flight `TextDelta` arriving
after the ack is expected network buffering, not a bug.

**11. clean exit on stdin EOF**

```powershell
'{"v":1,"id":"t2","type":"ListSessions","payload":{}}' | uv run python main.py
```
**Expect:** it prints its replies and **exits**. Before this episode it hangs
forever. Confirm the prompt returns without Ctrl+C.

**12. single instance**

Two terminals, both running:
```powershell
uv run python main.py
```
**Expect:** the second prints `Point is already running (pid …)` and exits
non-zero. Close the first, run the second again — it starts, proving the lock
was released.

**13. no leaked keys**

```powershell
Select-String -Path "$env:LOCALAPPDATA\Point\logs\point.log" -Pattern "sk-[A-Za-z0-9]{20,}"
```
**Expect:** no matches.

**Fails if:** migration errors on an already-migrated DB · doctor tracebacks on a
missing config · the old sessions are gone after relocation · `provider` is still
`NOT NULL` · a cancel produces two terminal events · the backend hangs on stdin
EOF · any raw key appears in the log.

**Restore your profile afterwards:**
```powershell
Remove-Item "$env:APPDATA\Point" -Recurse -Force -ErrorAction SilentlyContinue
Rename-Item "$env:APPDATA\Point.bak" "Point" -ErrorAction SilentlyContinue
Remove-Item "$env:LOCALAPPDATA\Point" -Recurse -Force -ErrorAction SilentlyContinue
Rename-Item "$env:LOCALAPPDATA\Point.bak" "Point" -ErrorAction SilentlyContinue
```

---

## Handoff

State after this episode: the database has every column v1 needs and lives
outside the source tree; tasks are durable and survive a crash honestly; the wire
protocol is the one `project_idea.md` §4 actually documents, so F2 can be written
against it; the app boots from nothing; secrets have a safe home; and `cli.py`
makes the backend testable without Go. No user-visible behaviour yet.

Record in `completed/`:

- Which empty `managers\*.py` placeholders you filled or deleted. As of the start
  of this episode `agent.py`, `memory.py`, and `tool.py` remain empty and
  `provider.py` is already gone.
- Whether the old `backend\runtime.db` was left in place.
- The before/after envelope shape, so F2 has one authoritative reference.
- Which of §9's six fixes actually landed, and which were deferred and why.
- Whether `providers\__init__.py` was added. It is currently missing while every
  other package has one; the tree works as a namespace package, so this is
  cleanup, not a bug.

Do not proceed until every check above passes.
