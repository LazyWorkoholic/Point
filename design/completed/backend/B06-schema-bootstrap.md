# Episode 6 — Schema v2, Envelope v1, Bootstrap, Secrets ✅

**Every column v1 needs is in the ground, the database lives outside the source
tree, tasks survive a crash honestly, the wire is the envelope `project_idea.md`
§4 actually documents, the app boots from nothing, and `cli.py` makes the backend
testable without Go.**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Verified by Aryan on 2026-08-26 against `tasks/backend/B06-schema-bootstrap.md`'s
> thirteen manual checks. No user-visible behaviour yet.

---

## Envelope v1 — the authoritative reference for F2

This is the section F2 is written against. Everything else in this file is
supporting detail.

### Before (B1–B5, never matched `project_idea.md` §4)

```json
{"event": "TextDelta", "request_id": "t1", "timestamp": 1787681546.33,
 "task_id": "8077b925", "event_id": "…", "type": "text_delta",
 "session_id": "…", "message_id": "…", "sequence": 4, "data": {"text": "Riv"}}
```

Payload keys were **flattened onto the top level**, which is why `type` above
means two different things at once — the outgoing event type lived under `event`,
and the *stream event's* type occupied `type`. `task_id` appeared only sometimes.
There was no `v`, no `corr_id`, no `hello`, no `ping`, no envelope-level `seq`.

### After (B6, what the backend emits today)

```json
{"v": 1,
 "id": "0f0e2b4c-9a3d-4c71-8f0e-2b4c9a3d4c71",
 "type": "TextDelta",
 "corr_id": "t1",
 "session_id": "7c3d4a7b-942f-4ea5-9b34-8cb672407b95",
 "task_id": "8077b925-d240-4772-8bba-17585fc290d8",
 "connection_id": null,
 "trace_id": null,
 "seq": 4,
 "ts": 1787681546.33,
 "payload": {"event_id": "…", "type": "text_delta", "task_id": "…",
             "session_id": "…", "message_id": "…", "sequence": 4,
             "timestamp": 1787681546.33, "data": {"text": "Riv"}}}
```

### Field-by-field mapping

| before | after | notes |
|---|---|---|
| `event` | `type` | values unchanged (`TaskStarted`, `TextDelta`, …) |
| `request_id` | `corr_id` | the causing request's `id`; **null** on `hello`/`ping` |
| `timestamp` | `ts` | still a **float epoch**, not an ISO string — only the key was renamed |
| `task_id`, sometimes present | `task_id` | **always present**, may be null |
| payload keys flattened to top level | nested under `payload` | never merged upward again |
| — | `v` | always `1` |
| — | `id` | fresh UUID **per envelope** |
| — | `session_id` | may be null |
| — | `connection_id`, `trace_id` | accepted on input, echoed on output, otherwise unused |
| — | `seq` | per-task stream sequence; **null** off the token stream |

### Rules F2 may rely on

1. **`corr_id` routes everything.** Every event caused by a request carries that
   request's `id`. `hello` and `ping` are caused by no request and carry `null`.
2. **`hello` is the first line on the wire**, before anything else, even with
   nothing configured. Payload:
   `{"v": 1, "capabilities": {"providers": [...], "schema_version": 2, "streaming": true, "cancel": true}}`.
   `providers` lists what is **registered**, not what is reachable — Ollama
   registers without a key, so a keyless profile reports `["ollama"]`.
   Reachability is a health question `ListProviders` answers.
3. **`ping` every 5s while idle**, payload `{"active_tasks": 0, "uptime_s": …}`.
   Two missed pings is the crash-recovery signal for a *hung* backend.
4. **Exactly one terminal event per task.** `TaskDone` | `TaskFailed` |
   `TaskCancelled`, never zero, never two. This is true as of B6 and was not
   before.
5. **`task.cancel.accepted` is not terminal.** It is the ack for `CancelTask`,
   payload `{"accepted": bool}`. `false` means already finished or unknown — in
   both cases nothing was stopped and no terminal event is coming.
6. **`seq` gaps are detectable** within a task's token stream. The same number
   also stays in `payload.sequence`, so `StreamAccumulator` was untouched.
7. **Version mismatch refuses and shows both versions:** an incoming envelope
   whose `v` is not 1 gets
   `Error: "Envelope version mismatch: backend speaks v1, client sent v2"`.

### Incoming shape

```json
{"v": 1, "id": "t1", "type": "ListSessions", "payload": {},
 "session_id": null, "task_id": null, "connection_id": null, "trace_id": null}
```

`type` is required. `payload` defaults to `{}` and must be an object. `id`
defaults to a generated UUID. **The old `{"event": …}` form is not accepted** —
this was the deliberate one-way switch, made while no Go client existed.

---

## `runtime/events.py` — rewritten at the serialization boundary

The change is **serialization only**. `IncomingEvent` and `OutgoingEvent` kept
their fields and their names; `from_dict()` and `to_dict()` are what moved, and
**no handler signature changed** — which is why an envelope swap cost one file.

- `ENVELOPE_VERSION = 1`.
- `IncomingEvent` gained `session_id`, `task_id`, `connection_id`, `trace_id`,
  all optional, all envelope metadata no handler reads.
- `OutgoingEvent` gained `event_id` (serialized as `id`, a real field with a
  `default_factory` so serializing twice cannot mint two different ids),
  `session_id`, `connection_id`, `trace_id`, `seq`. `request_id` became
  `Optional[str]` so `hello`/`ping` can carry a null `corr_id`.
- New `IncomingEventType`: `UNARCHIVE_SESSION`.
- New `OutgoingEventType`: `SESSION_UNARCHIVED`, `TASK_CANCEL_ACCEPTED`
  (`"task.cancel.accepted"`), `HELLO` (`"hello"`), `PING` (`"ping"`). Existing
  member *values* were not renamed — only the key they serialize under changed.

## `database/migrations.py` (NEW)

Versioned, forward-only, recorded in `schema_migrations(version, applied_at)`,
run automatically on startup and callable without a Runtime, an event loop, or
any of the rest of the backend.

- **`PRAGMAS`** — `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`,
  applied on every connection here and in `connection.py`.
- **v1** reproduces the B2/B3 schema verbatim, so a fresh database walks the same
  path an existing one did instead of being born at v2 with no history.
- **v2** rebuilds `sessions` (nullable `provider`/`model`, plus `effort`,
  `created_by`, `archived_at`) and adds `tasks`, `projects`, `agents`,
  `memory_records`, `memory_scopes`, `context_attachments`, `file_locks`,
  `usage_records`, `presence`.
- **Baselining**: a database that predates the runner already has v1's tables, so
  v1 is *recorded* as applied rather than re-run. The existing `runtime.db`
  therefore reported `1 -> 2 (v2)` and produced exactly one backup.
- **Backup before every migration**: `runtime.db.bak-<version>`, preceded by
  `PRAGMA wal_checkpoint(TRUNCATE)` — under WAL a plain file copy can miss
  everything still sitting in the `-wal` file, which is the difference between a
  backup and a file that looks like one. Skipped when the database has no user
  tables yet, so creating a database does not leave a backup of nothing.
- **The rebuild runs with `foreign_keys=OFF` and `legacy_alter_table=ON`.** This
  is not a style choice. With foreign keys on, `DROP TABLE sessions` fires the
  `ON DELETE CASCADE` on `messages` and destroys all 42 rows; and with
  `legacy_alter_table` off, `ALTER TABLE sessions_new RENAME TO sessions` tries
  to reparse `messages`' foreign key against a table that does not exist for the
  length of one statement. `PRAGMA foreign_key_check` runs inside the same
  transaction afterwards, and both pragmas are restored in a `finally`.
- The sessions rebuild is **first** in v2, so the new tables that reference
  `sessions(id)` are created against the final table.

## `database/task_repository.py` (NEW)

Raw SQL only, same shape as `session_repository.py`.

- **Every write is an upsert** (`ON CONFLICT(id) DO UPDATE`, with `COALESCE` so a
  later write cannot blank an earlier `started_at`). `TaskManager.create_task()`
  is synchronous and schedules its row without awaiting it, so the insert and the
  first state update can reach the connection in either order — an upsert makes
  that harmless instead of a lost row.
- `list_recent(limit, session_id)` is what `cli.py tasks list` reads.
- **`recover_orphaned_tasks(db_path)`** — synchronous and standalone, because it
  runs at startup before a Runtime exists and before a single event is accepted.
  Marks every row still `pending` or `running` as `failed` /
  `error_code='backend_died'` with a `finished_at`, and returns the count.
- `ERROR_BACKEND_DIED` is the **only** `error_code` v1 produces. It is a slug for
  Go to branch on, not a message — an ordinary task failure (provider not
  registered, network down) leaves `error_code` NULL and carries its human string
  in the `TaskFailed` payload, where `Task.result` already put it.

## `database/connection.py` — schema moved out

`_SCHEMA` is gone. `open_database()` now calls `run_migrations(path)` first, on
its own synchronous connection (the pre-migration backup is a file copy, and
copying a database out from under an open async connection is how you get a
backup missing the last write), then opens aiosqlite and applies `PRAGMAS`.
Any entry point that reaches `open_database()` gets a migrated schema whether or
not it went through `bootstrap.build_runtime()`.

## `appconfig.py` (NEW)

Where Point keeps its files, and how a key is found.

```
config      %APPDATA%\Point\config.toml       user-owned, NEVER rewritten
credentials %APPDATA%\Point\credentials.toml  keys only, gitignored
state       %LOCALAPPDATA%\Point\state.json   machine-written (path only; nothing writes it yet)
database    %LOCALAPPDATA%\Point\runtime.db
logs        %LOCALAPPDATA%\Point\logs\point.log
lock        %LOCALAPPDATA%\Point\point.lock
```

- `load_config()` / `config_value("database.path")` — `tomllib`, stdlib, **no new
  dependency**. Returns `{}` when there is no config at all, because that is a
  supported state and not an error.
- **`resolve_api_key(provider)` — env var → `credentials.toml` → `None`.** No
  prompting; that is the frontend's job. `ProviderConfig.from_env()` now goes
  through this, so a key in `credentials.toml` registers a provider exactly as an
  env var does.
- `key_source(provider)` — for `doctor`. Returns where the key came from, never
  the key.
- `save_api_key()` — temp file then rename, `icacls /inheritance:r /grant:r` on
  Windows, `chmod 0600` elsewhere. **Nothing calls it yet** (see "deliberately
  unfinished").
- `mask_secret()` → `sk-...4f2a`. `register_secret()` remembers every resolved
  secret so the log redactor can catch it inside someone else's error string.

## `logging_setup.py` (NEW)

The first logging in this backend. **stderr plus a rotating file, never stdout** —
stdout is the protocol channel.

- `RotatingFileHandler` at `%LOCALAPPDATA%\Point\logs\point.log`, 2 MB × 3.
- stderr handler at WARNING (DEBUG with `--verbose`), file handler at DEBUG.
- `RedactingFilter` rewrites the **formatted** message, because the interesting
  case is a key that arrived already interpolated into a third-party exception.
  It masks every registered secret plus four key shapes (`sk-ant-`, `sk-or-v1-`,
  `sk-`, `AIza`) — so a key Point never resolved itself is still caught.
- An unwritable log directory downgrades to stderr rather than refusing to boot.

## `lockfile.py` (NEW)

Single-instance lock holding the pid, in the state dir. A second instance prints
`Point is already running (pid 4821)` and exits non-zero. A lock whose pid is
**dead is stale** and taken over, or one crash would require deleting a file by
hand before the app could ever start again.

**`os.kill(pid, 0)` is not a liveness probe on Windows** — CPython routes it to
`TerminateProcess`, which would kill the very process being asked about.
`pid_alive()` uses `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` +
`GetExitCodeProcess` and checks for `STILL_ACTIVE`.

`cli.py` deliberately does **not** take this lock: the CLI is a second *process*,
not a second instance of the app, and WAL exists precisely so it can read and
write alongside a running backend.

## `bootstrap.py` (NEW) — construction extracted from `main.py`

`main.py` fused four responsibilities; only the first is shareable with a CLI.

- **`resolve_db_path()`** — `POINT_DB` → config `database.path` →
  `%LOCALAPPDATA%\Point\runtime.db`, plus the **one-time copy** out of the source
  tree: if the target has no database and `backend\runtime.db` does, it is copied
  (with any `-wal`/`-shm` sidecars) and logged. **The old file is left in place.**
- **`run_migrations()`, `current_version()`, `SCHEMA_VERSION`** are re-exported
  here so `cli.py` never needs a database path literal of its own.
- **`build_runtime(single_instance, verbose, say_hello, heartbeat)`** — dirs,
  writability, lock, db path, migrations, crash recovery, `Runtime.create()`,
  `hello`, heartbeat.
- **Crash recovery is conditional.** It runs when we hold the lock, or when
  nothing live holds it. Running it from the CLI while a backend is streaming
  would bury tasks that are very much alive. It logs at WARNING when the count is
  non-zero — that is news — and INFO when it is zero.
- **`shutdown_runtime()`** — closes the runtime in a `try`, releases the lock in
  the `finally`. A lock left behind by a process that is exiting anyway would
  refuse every future start for a pid that no longer exists.

The reuse test from the task doc holds: `cli.py` contains no `Runtime.create`, no
`open_database`, and no db-path literal.

## `cli.py` (NEW)

`uv run python cli.py <command>`, run from inside `backend\`. Never reads stdin,
never writes JSON lines to stdout.

**One helper does all the work.** `call(runtime, type, timeout, **payload)`
assigns an envelope `id`, awaits `receive()`, then drains `runtime.outbox`
collecting envelopes whose `corr_id` matches, stopping on the expected terminal
event or on timeout. Three constraints shaped it:

- **Draining is mandatory.** `outbox` is `maxsize=1000` and `_emit` *awaits*
  `.put()`, so anything the CLI refuses to take it eventually blocks on.
  Non-matching envelopes are discarded, or printed with `--verbose`.
- **`receive()` never raises to the caller.** Errors arrive as `Error` envelopes;
  exit codes derive from the event type, never from a caught exception.
- **`send` keeps draining past its own ack** until the terminal event for that
  `task_id` — which is where the "exactly one terminal event" guarantee gets
  cashed in. Its match predicate deliberately accepts events for its task
  regardless of `corr_id`, so the cancel ack (caused by a *different* request) is
  visible; without that you cannot tell one terminal event from two.

Implemented: `doctor`, `migrate`, `session new|list`, `send`, `tasks list`.
`--verbose` is accepted before *and* after the subcommand.

- **`send`/`session new` default to ollama if it is registered *and* answering,
  else `echo`. Never claude.** `CreateSession`'s old default resolved through
  `_PROVIDER_ALIASES` to anthropic, which does not register at all on a keyless
  profile — exactly the profile the checks run on. `echo` is the existing
  no-network `_demo_echo_body` path and is deliberately not a registered provider,
  so it always works.
- **`tasks list` reads the durable rows**, not `TaskManager`'s in-memory dict — a
  fresh CLI process has none, and the point of the table is that the answer
  outlives the process that produced it. `_handle_list_tasks` (the event) still
  reads memory; nothing asked for that to change.
- **`doctor`** prints VERSION and whether Go and Python agree, Python version and
  path, dependency versions, database path + schema version, config/credentials/
  state/log paths and whether they exist, per-provider key source (masked),
  Ollama reachability and model list, and mock status. It builds **no Runtime**,
  runs on a machine with no config at all, and exits 0. The Go line is honest
  about what it cannot know: with no built binary there is nothing to compare.

## `main.py` — transport and nothing else

Construction collapsed into one `build_runtime()` call. The loops are unchanged
in shape; the teardown is entirely new.

- `read_stdin_loop()` returns the still-running receive tasks so they can finish
  before teardown, and **logs malformed JSON** instead of dropping it silently.
- `write_stdout_loop()` **returns rather than raises** when stdout stops
  accepting writes (see bug 2 below).
- `main()` waits on reader-or-writer, whichever completes first — either end of
  the pipe closing is the same news — drains, then always reaches
  `shutdown_runtime()` in a `finally`.
- `--verbose`, checked with `in sys.argv` rather than argparse, so the one flag
  this process has can never consume a protocol line.

## `runtime/runtime.py`

- **Dead duplicate `self.outbox` assignment deleted** (§9.3). The bounded one
  always won; the unbounded line was noise that read like a bug.
- `Runtime.create(db_path)` — **the `"runtime.db"` default is gone.** It resolved
  to wherever the process happened to be standing; `resolve_db_path()` is now the
  only thing that decides where the database lives.
- **`Runtime.close()`** — cancels the heartbeat, cancels active tasks, yields one
  loop turn so each cancelled task reaches its `except` branch and writes its own
  terminal row instead of being buried by the next startup, then closes the
  database.
- **`_correlation`** — a bounded `OrderedDict` (1000) mapping request id →
  `{connection_id, trace_id, session_id}`. `_fill_envelope()` applies it at
  `_emit`. This is why echoing two transport fields did not require touching
  seventeen handler signatures.
- **`emit_hello()`** and **`start_heartbeat()`** — the heartbeat loop also calls
  `TaskManager.sweep_finished()`, which is what finally makes that method's
  docstring true (§9.6).
- **`_handle_cancel_task`** emits `task.cancel.accepted` (§7). `TaskNotFoundError`
  becomes `accepted: false` rather than an `Error`.
- **`_handle_unarchive_session`** (§9.1) — `SessionManager.unarchive()` had
  existed since B2 with no event type and no handler.
- **`_handle_create_session` no longer defaults provider/model.** They are `None`
  unless supplied.
- **`_handle_send_message` refuses a NULL provider/model with a clean `Error`**
  naming B8. Refusing is not resolving; guessing would have been.
- **`_record_task()`** — the recorder handed to `TaskManager`.
- `_handle_list_providers` skips providers with `selectable = False`.
- Sink: stream envelopes carry `seq=payload["sequence"]`; echo tokens draw from
  the same per-task counter and close it on terminal, so it cannot outlive its
  task.

## `runtime/task.py` / `runtime/task_manager.py`

- `TaskRecorder` type + optional `recorder` argument. A `Task` with no recorder
  behaves exactly as it did before B6.
- `_run()` awaits `_record()` after **every** state assignment; `started_at` and
  `finished_at` are new fields. A failing row write can never fail the task — the
  row is a record of the work, not the work.
- `cancel()` records only in the one case where it genuinely transitions state: a
  task cancelled before its coroutine ever got a turn on the loop, where `_run()`
  never executes and the row would otherwise sit at `pending` until the next
  startup buried it.
- `TaskState` gained `PAUSED` and `AWAITING_APPROVAL`, reserved, unproduced.
  **There is no `QUEUED`** — `PENDING` is that state, and B12 is where `QUEUED`
  will mean something different (blocked behind a per-provider cap).
- `TaskManager(recorder=…)`; `create_task()` schedules the row before `start()`.

## `managers/session.py`, `session_manager.py`, `database/session_repository.py`

`provider` and `model` are `Optional[str]`, defaulting to `None`. `effort`,
`created_by`, `archived_at` added and carried through insert and row mapping.
`archive()` sets `archived_at`; `unarchive()` clears it. `SessionManager.create()`
gained `effort` and `created_by`.

## `managers/message.py`, `message_history.py` — reasoning blocks are taggable

`ContentType.THINKING`, `MessageContent.provider`, `MessageContent.from_thinking()`,
and a `ThinkingMessage()` constructor. `validate()` treats THINKING as textual.
`MessageHistory.for_provider(provider)` filters reasoning blocks produced by a
*different* provider out of a send — **never deletes them**, and keeps untagged
ones, because absence of a tag is not evidence of another provider. Nothing emits
reasoning content yet; the shape exists so the tag arrives as a field rather than
as a migration.

## `streaming/events.py`

`COMPLETED`, `FAILED`, `CANCELLED` deleted (§9.4). Nothing produced them, nothing
consumed them, and terminal semantics live on the task — a stream ending is not
an outcome. `THINKING_DELTA`, `STATUS_CHANGED`, `PROGRESS_UPDATED`,
`TOOL_STARTED`, `TOOL_COMPLETED` remain reserved for B9/B10.

## `providers/` — effort, capabilities, mock, and the Gemini leak

- **`models.py`**: `Effort` enum (`off|low|medium|high`), `GenerationRequest.effort`,
  `ModelCapabilities.supports_effort` and `supports_caching`. `from_env()` now
  resolves keys through `appconfig`.
- **`openai/provider.py`**: `reasoning_effort_kwargs()`, shared with OpenRouter.
  Omitted entirely when effort is `None` or `off`, because `reasoning_effort` on
  a model that does not reason is a 400. **Unreachable today** — nothing sets
  `effort`.
- **`anthropic`, `gemini`, `ollama`** accept and ignore effort, each with a
  docstring saying why. **`supports_effort` is `False` everywhere**, including
  OpenAI: it is per-model, `models.list()` does not report it, and guessing from
  an id prefix is how you grey out the wrong control.
- **`gemini/provider.py` (§9.2)**: the stream leak B05 claimed to have closed
  everywhere. `AsyncGenerateContentResponse` has no `close()`/`aclose()` of its
  own, so the guarantee comes from `_close_stream()` closing the async iterator it
  wraps, in a `finally` that runs on cancellation exactly as everyone else's
  `__aexit__` does.
- **`mock/provider.py` (NEW)**: replays a scripted stream from a fixture.
  Registered **only** when `POINT_MOCK=1`, and `selectable = False` keeps it out
  of `ListProviders` — a scripted provider that can be reached without opting in
  will eventually answer a real question with a fixture. `BaseProvider.selectable`
  is the new class attribute; only Mock sets it False.
- **`providers/__init__.py` added.** Every other package had one; the tree worked
  as a namespace package, so this was cleanup, not a bug.
- **Fixtures**: `tests/fixtures/mock_stream.json` (five fast chunks) and
  `mock_slow.json` (twenty chunks at 0.3s ≈ 6s, for cancellation tests without a
  live model).

## `design/decisions.md` — D91–D96 added

The envelope is recorded there **as implemented, not aspirational**, along with
the non-terminal cancel ack, NULL-means-inherit, the reserved `approval.*`
namespace, durable tasks, and the database relocation.

---

## Manual Test Log (Episode 6)

Run by Aryan on 2026-08-26, on a profile renamed aside so `%APPDATA%\Point` and
`%LOCALAPPDATA%\Point` did not exist.

1. **`doctor` on a clean profile** — full report, no traceback, exit 0, and
   `No cloud provider is configured.` Ran with no config, no database, and no
   credentials file.
2. **Migration** — the existing `backend\runtime.db` was copied to
   `%LOCALAPPDATA%\Point\runtime.db` and reported `migrated 1 -> 2 (v2)` with
   exactly one backup, `runtime.db.bak-2`. `.tables` listed all twelve:
   `agents`, `context_attachments`, `file_locks`, `memory_records`,
   `memory_scopes`, `messages`, `presence`, `projects`, `schema_migrations`,
   `sessions`, `tasks`, `usage_records`.
3. **Old data survived the move** — all 12 original sessions listed, oldest
   `Bubble Tea Chat` (2026-08-09) through `Cancel Test`. A 13th appeared later
   only because verification itself created one.
4. **Nullable provider/model** — `select count(*) from sessions where provider is
   not null` returned every existing row (nothing was blanked), and
   `pragma table_info(sessions)` showed `notnull = 0` on both `provider` (3) and
   `model` (4), with `effort` (13), `created_by` (14), `archived_at` (15)
   present.
5. **WAL** — `pragma journal_mode` returns `wal`, where the pre-B6 database
   reported `delete`.
6. **Idempotent migrate** — a second run printed `already at version 2` and did
   **not** churn a second backup.
7. **A task leaves a durable row** — `cli.py send "count to five"` streamed a
   reply and left a row with `status = completed`, empty `error_code`, and
   `finished_at` set.
8. **Crash recovery** — a 500-word ollama generation started inside `main.py`,
   killed mid-stream with
   `Stop-Process -Id (Get-Content "$env:LOCALAPPDATA\Point\point.lock")`. The row
   sat at `running` with a null `finished_at`; the next startup logged
   `WARNING point.bootstrap: Crash recovery: 1 orphaned task(s) marked
   failed/backend_died` and the row became `failed` / `backend_died` with a
   `finished_at`.
9. **The envelope** — `'{"v":1,"id":"t1","type":"ListSessions","payload":{}}' |
   uv run python main.py` produced `hello` first, then a `SessionList` whose
   `corr_id` was `t1`, every line carrying `v`, `id`, `type`, `corr_id`, `ts`,
   and a nested `payload` with nothing flattened.
10. **One terminal event per cancel** — `cli.py send "write a long essay"
    --cancel-after 2` on ollama produced one `[cancel accepted: True]` then
    exactly one `[TaskCancelled]`, and the row read `cancelled`. Repeated against
    `mock-slow` with the same result.
11. **Clean exit on stdin EOF** — the prompt returns without Ctrl+C, where before
    B6 it hung forever.
12. **Single instance** — with terminal 1 alive, terminal 2 printed
    `Point is already running (pid 7148)` and exited non-zero; the lock file held
    that exact pid. After closing terminal 1, `Test-Path` on the lock returned
    `False` and terminal 2 started normally — proving the lock was released, not
    merely abandoned.
13. **No leaked keys** — `Select-String -Pattern "sk-[A-Za-z0-9]{20,}"` over
    `point.log` found nothing. Separately confirmed across all three key states
    (env var, `credentials.toml`, none), including a key shape Point never
    resolved itself, which the pattern redactor still masked to `sk-...3456`.

Additionally confirmed outside the numbered gate: two processes writing 40
sessions each, concurrently, with **zero** `database is locked` errors (80 rows,
both processes reporting success); `POINT_MOCK` absent → registry is
`['ollama']`, `POINT_MOCK=1` → `['ollama', 'mock']` while `ListProviders` still
reports only `['ollama']`; archive → `archived_at` set → unarchive → `archived_at`
cleared; a NULL-provider session refusing `SendMessage` with a clean `Error`; and
`{"v": 2, …}` refused with both versions named.

**Two false readings during verification, both caused by the test instructions
rather than the code** — recorded because the next reader will otherwise think
check 8 is flaky:

- Check 8 first reported a pass with no orphan. The `SendMessage` had been sent to
  a session id copied from a *previous* profile, so the backend answered
  `Error: No session with id …`, no task ever started, and there was nothing live
  to orphan. The `Error` envelope was correct — it even echoed `session_id`
  properly, which was the correlation path working. **The generation must run
  inside the process being killed**; a `cli.py send` is a different process and
  survives the backend's death.
- Two `failed` rows with an empty `error_code` were mock-session sends from a
  terminal where `$env:POINT_MOCK` was not set: `No provider registered as
  'mock'`. That is correct — `error_code` is reserved for slugs Go branches on,
  and an ordinary provider failure carries its text in the `TaskFailed` payload.

---

## Bugs caught during manual testing

### 1. The write probe raced itself, breaking the two-process case it existed to protect

`appconfig.is_writable()` wrote a probe file with a **fixed name**,
`.point-write-probe`. Two Point processes starting at the same moment each
created it and each deleted the other's, so one of them caught the `OSError` and
concluded the state directory was read-only:

```
bootstrap.BootstrapError: State directory is not writable: …\Local\Point
```

Found on the very first concurrent-write run — one process wrote its 40 sessions,
the other never started. Two processes starting at once is not an edge case here;
it is the CLI beside a running backend, which is the entire reason WAL is on.
**Fixed** by putting the pid in the probe name and unlinking in a `finally`.
"Done when #5" is unreachable without this.

### 2. A closed stdout skipped shutdown, and skipping shutdown hangs forever

Piping `main.py` into `head` closes stdout early. `write_stdout_loop` raised
`OSError: [Errno 22] Invalid argument` (Windows raises `EINVAL`, not
`BrokenPipeError`), the exception escaped through `await writer` **inside the
`finally`**, and `shutdown_runtime()` never ran. The consequence is worse than a
traceback: **aiosqlite's worker thread is not a daemon**, so a connection that is
never closed hangs the process at interpreter exit — permanently. A hung process
holds the single-instance lock with a pid that never dies, so **every subsequent
start would refuse forever.** That is precisely the failure §15 warns about,
arriving through a door the episode did not name. Observed as a 2-minute timeout
during the single-instance test.

**Fixed** in three places: `write_stdout_loop` logs and returns instead of
raising; `main()` waits on reader-or-writer whichever finishes first and always
reaches teardown; `shutdown_runtime()` releases the lock in a `finally` so a
failed close cannot strand it. Verified: the same pipeline now exits in 2s with
no lock left behind, and a unit check confirms the writer returns cleanly on a
dead stdout rather than raising or spinning.

### 3. `--verbose` was unusable where anyone would actually type it

`--verbose` existed only on the top-level parser, so
`cli.py send "…" --verbose` — the natural form — died with a usage error.
**Fixed** with a shared parent parser using `default=argparse.SUPPRESS`, so the
flag works before *and* after the subcommand and a subparser default cannot
overwrite a value given earlier.

### 4. Near-miss, designed around rather than hit: the sessions rebuild would have deleted every message

`messages.session_id` has `ON DELETE CASCADE`. With `foreign_keys=ON` — which
§11 requires on every connection — `DROP TABLE sessions` inside the v2 rebuild
fires that cascade and takes all 42 messages with it. Caught before running
anything against real data by testing the migration on a copy first; the runner
disables foreign keys and enables `legacy_alter_table` for the duration of each
migration and runs `PRAGMA foreign_key_check` before committing. The copy test
confirmed 12 sessions and 42 messages intact, `notnull=0` on both columns, and no
foreign key violations.

---

## What the next episode inherits

**Ready and empty, by design.** `projects`, `agents`, `memory_records`,
`memory_scopes`, `context_attachments`, `file_locks`, `usage_records`, `presence`
all exist with no rows and no logic. `tasks.agent_id`, `project_id`,
`connection_id`, and `git_checkpoint_sha` are NULL in every row — hooks for B9,
B7, SSH, and the parked Coding Assistant.

**B7 (Project System)** — `projects` and `sessions.project_id` are in place.
`cli.py project init|resolve|list-files|read-file` is **not** implemented; add it
when B7 lands, alongside `task cancel`, `context report`, `attach`, and
`memory list`.

**B8 (Context Builder) owns NULL resolution.** B6 only made NULL storable. Today
a session with a NULL provider/model gets a clean `Error` from `SendMessage`
naming B8. The `Effort` vocabulary and `supports_effort` exist and **nothing sets
effort** — the resolution order (task > session > agent > global) is B8's.

**B9 (Agents)** — the `agents` table is unseeded and `managers\agent.py`,
`memory.py`, and `tool.py` are still empty files.

**B12** — `QUEUED` was deliberately not added. Do not re-litigate.

**B15** — the `Shutdown` *event* is untouched. B6 fixed only the stdin-EOF hang.
`_handle_shutdown` still flips status away from `READY` without acking or
exiting, and that stays B15's to fix.

**F2** — the envelope section at the top of this file is the reference. "Exactly
one terminal event per task" is now true and may be relied on.

**Answering B5's carried-forward question** ("does stream/task history need its
own repository?"): **tasks yes, streams no.** `tasks` is a durable mirror written
through `task_repository.py`; `StreamManager` and `StreamAccumulator` remain
in-memory only, and `messages` — with its `task_id` column from B3 — is still the
durable record of what a stream produced. No schema change was needed for
streams, and none is expected.

**Deliberately unfinished, so nobody "fixes" it by surprise:**

- `appconfig.save_api_key()` is implemented and **called by nothing**. §12 asked
  for the writer; persisting keys from `SetProviderApiKey` is a behaviour change
  no episode has requested.
- `state.json` has a path and no writer.
- `approval.*`, `TaskState.PAUSED`, `AWAITING_APPROVAL` — reserved, unproduced.
- `_handle_list_tasks` (the event) still reads `TaskManager`'s in-memory dict;
  only `cli.py tasks list` reads the durable table.
- The masking helper is applied in logs and errors. No code path sends a key to
  Go today, so there is nothing yet to mask on that boundary.
- **`backend\runtime.db` is still there.** It was copied, not moved. Deleting it
  is Aryan's call once he is satisfied the move held.
