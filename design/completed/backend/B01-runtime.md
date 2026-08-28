# Episode 1 — Runtime ✅

**Task lifecycle, concurrency, cancellation, event dispatch**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Original source: `completed_backend_task.md`, split per episode. Content is
> unchanged apart from heading levels.

---

## `runtime/errors.py`
Central exception hierarchy so the dispatcher can catch errors by type
and convert them into clean `Error` events instead of leaking stack
traces to Go.

- `RuntimeException` — base class for everything below.
- `InvalidEventError` — malformed incoming event (bad JSON shape).
- `UnknownEventTypeError(InvalidEventError)` — no handler registered
  for the given event type.
- `TaskNotFoundError` — operation referenced a `task_id` that doesn't
  exist.
- `TaskAlreadyFinishedError` — defined for future use; not currently
  raised anywhere (cancelling a finished task currently just returns
  `False` rather than raising — see `Task.cancel()` below).

**Used by:** every other `runtime/` file, and `session_manager.py`
(via `SessionNotFoundError(RuntimeException)`).

## `runtime/state.py`
`RuntimeState` — tracks the Runtime's own health, separate from
session/task data on purpose (so a lightweight status check never has
to touch `SessionManager` or `TaskManager` internals).

- `RuntimeStatus` enum: `STARTING`, `READY`, `STOPPING`, `STOPPED`.
- `RuntimeState.is_accepting_events()` — used by `Runtime.receive()` to
  reject events (except `Shutdown`) once the runtime is no longer
  `READY`.
- `active_task_count` — updated by `Runtime` after every task
  creation/completion; not yet exposed to Go but ready for a future
  status-bar event.

## `runtime/events.py`
Defines the entire typed event vocabulary — no raw dicts trusted past
the parsing boundary.

- `IncomingEventType` enum — currently: `SEND_MESSAGE`, `CANCEL_TASK`,
  `LIST_TASKS`, `SHUTDOWN`, plus the Episode 2 additions:
  `CREATE_SESSION`, `GET_SESSION`, `LIST_SESSIONS`, `RENAME_SESSION`,
  `ARCHIVE_SESSION`, `DELETE_SESSION`, `UPDATE_SESSION_METADATA`,
  `UPDATE_SESSION_SETTINGS`.
- `IncomingEvent` (frozen dataclass) — `type`, `request_id` (generated
  if not supplied by Go), `payload` (everything else from the raw
  dict). `IncomingEvent.from_dict(raw)` is the **single** place raw
  JSON is trusted; every other function downstream works with a typed
  object.
- `OutgoingEventType` enum — `TASK_STARTED`, `TASK_TOKEN`,
  `TASK_DONE`, `TASK_FAILED`, `TASK_CANCELLED`, `TASK_LIST`, `ERROR`,
  plus Episode 2's `SESSION_CREATED`, `SESSION`, `SESSION_LIST`,
  `SESSION_RENAMED`, `SESSION_ARCHIVED`, `SESSION_DELETED`,
  `SESSION_METADATA_UPDATED`, `SESSION_SETTINGS_UPDATED`.
- `OutgoingEvent` (frozen dataclass) — `type`, `request_id`, optional
  `task_id`, `payload`, `timestamp`. `.to_dict()` is what `main.py`
  serializes to a JSON line on stdout.

**Relation to future episodes:** Episode 3 will add message-related
event types here (or Episode 3's message payloads will ride inside
existing `SendMessage`/streaming events — decision pending when that
episode starts).

## `runtime/task.py`
The fundamental unit of concurrent work. Built *before* `Runtime`
itself, per the architectural decision that concurrency should be
designed from `Task` outward, not bolted onto a synchronous core.

- `TaskState` enum: `PENDING`, `RUNNING`, `COMPLETED`, `FAILED`,
  `CANCELLED`.
- `TaskResult` — final outcome (`state`, `output`, `error`,
  `finished_at`).
- `TaskBody` type alias — an async generator function that yields
  `{"text": ...}`-shaped chunks. This is the seam where Episode 5's
  real provider streaming will plug in, replacing `Runtime`'s current
  placeholder `_demo_echo_body`.
- `EventSink` type alias — callback `Task` invokes on every lifecycle
  change/chunk; `Task` itself knows nothing about JSON or stdout, it
  just calls this.
- `Task.start()` — schedules `self._run()` via `asyncio.create_task`.
  Never blocks the caller.
- `Task._run()` — sets state to `RUNNING`, emits a `started` chunk,
  iterates the task body emitting `chunk` for each yield, then emits
  `done`/`failed`/`cancelled` depending on outcome. Catches
  `asyncio.CancelledError` explicitly to set `CANCELLED` state and
  re-raise (so `asyncio` still sees the cancellation correctly).
- `Task.cancel()` — calls `asyncio.Task.cancel()`; returns `False` if
  the task doesn't exist yet or is already finished (verified in
  manual testing — see "Manual Test Log" below).
- `Task.is_active()` — `True` if `PENDING` or `RUNNING`.

## `runtime/task_manager.py`
Owns the lifetime of every `Task`. Single responsibility: create,
track, cancel, list. Does not know what a chat, agent, or session is.

- `create_task(body, session_id, kind, sink)` — constructs a `Task`,
  registers it in an internal dict keyed by `task.id`, calls
  `task.start()`, returns it.
- `get(task_id)` — raises `TaskNotFoundError` if missing.
- `cancel(task_id)` — delegates to `Task.cancel()`.
- `list_active()` / `list_all()` / `active_count()`.
- `sweep_finished(keep_last=200)` — drops old finished tasks to bound
  memory growth. **Not yet called anywhere** — needs to be wired into
  a periodic cleanup (candidate for Episode 4/6 when the runtime has a
  proper background maintenance loop).

## `runtime/dispatcher.py`
Deliberately dumb switchboard — maps `IncomingEventType` → handler
coroutine. New event types get a new `register()` call, never an
if/elif chain inside `Runtime`.

- `register(event_type, handler)`
- `dispatch(event)` — raises `UnknownEventTypeError` if nothing is
  registered for the event's type.

## `runtime/runtime.py`
The coordinator. Owns `TaskManager`, `SessionManager` (added in
Episode 2), the `Dispatcher`, and the outbound event queue
(`self.outbox`). Never imports an LLM SDK.

Key methods as of end of Episode 2:
- `Runtime.__init__(db_conn)` — wires all managers, registers all
  handlers, sets status to `READY`.
- `Runtime.create(db_path)` *(classmethod)* — async factory, since
  opening the DB connection requires `await`. This is why `main.py`
  calls `await Runtime.create(...)` instead of `Runtime()` directly.
- `receive(raw)` — the single entry point for every event from Go.
  Parses via `IncomingEvent.from_dict`; on `InvalidEventError`, emits
  an `Error` event and returns. Rejects events (except `Shutdown`) if
  the runtime isn't `READY`. Dispatches; catches any
  `RuntimeException` subclass and converts it to an `Error` event.
- `_handle_send_message` — validates a `session` was provided, calls
  `sessions.get()` (raises `SessionNotFoundError` if missing — this is
  what makes "SendMessage requires an existing session" enforced),
  persists the user message via `sessions.append_message`, creates a
  `Task` running `_demo_echo_body`, calls
  `sessions.set_active_task(session_id, task.id)`, emits `TaskStarted`.
- `_demo_echo_body(text)` — **placeholder only.** Streams the input
  text back word-by-word with a short `asyncio.sleep` between each.
  This is explicitly what Episode 5 (Providers) replaces with a real
  `ChatAgent`/provider call — nothing else in `Task`/`TaskManager`/
  `Dispatcher`/`Runtime` needs to change when that swap happens.
- `_handle_cancel_task` / `_handle_list_tasks` / `_handle_shutdown` —
  straightforward delegation to `TaskManager`.
- Session handlers (`_handle_create_session`, `_handle_get_session`,
  `_handle_list_sessions`, `_handle_rename_session`,
  `_handle_archive_session`, `_handle_delete_session`,
  `_handle_update_metadata`, `_handle_update_settings`) — each
  validates required payload fields, delegates to `SessionManager`,
  emits the corresponding `Outgoing` event. `_session_to_dict()` is
  the single serialization point used by all of them (and includes
  `messages` only when `include_messages=True`, i.e. only for
  `GetSession`, to avoid pulling full history into every `ListSessions`
  response).
- `_make_sink(request_id, session_id)` — builds the `EventSink`
  callback passed to each chat `Task`. Accumulates streamed word
  chunks in a closure list; on `done`, joins them into the full
  assistant reply and persists it via `sessions.append_message` (so
  the DB gets one row per full reply, not one row per streamed word);
  on `done`/`failed`/`cancelled`, also calls
  `sessions.set_active_task(session_id, None)` to clear the
  "something is running" flag.
- `_emit(event)` — pushes to `self.outbox`; `main.py`'s
  `write_stdout_loop` drains this queue and writes JSON lines to
  stdout.

## `main.py`
Entry point. Two concurrent loops sharing one `Runtime` instance for
the process's lifetime.

- `_stdin_reader_thread(loop, queue)` — runs in a background OS
  thread, blocking-reads `sys.stdin` line by line, hands each line to
  the event loop via `loop.call_soon_threadsafe(queue.put_nowait, ...)`.
  **Why a thread, not `loop.connect_read_pipe`:** on Windows, asyncio's
  `ProactorEventLoop` (required for subprocess support, and the
  default on Windows) does not reliably support `connect_read_pipe` on
  console stdin — this caused a crash during testing (`OSError:
  [WinError 6] The handle is invalid`). Reading in a thread sidesteps
  the issue entirely and behaves identically on Windows/Linux/macOS.
- `read_stdin_loop(runtime)` — pulls lines off the thread-fed queue,
  parses JSON (silently skips malformed lines rather than crashing),
  and calls `asyncio.create_task(runtime.receive(raw))` — fire-and-
  forget, so multiple events can be mid-processing concurrently.
- `write_stdout_loop(runtime)` — drains `runtime.outbox`, writes each
  event as one JSON line, flushes immediately (so Go sees streamed
  tokens in real time, not buffered).
- `main()` — creates the `Runtime` via `Runtime.create(db_path=
  "runtime.db")`, runs both loops concurrently via `asyncio.gather`.
- Bottom-of-file `try/except KeyboardInterrupt: pass` around
  `asyncio.run(main())` — suppresses the traceback that Ctrl+C
  otherwise prints during manual testing. **Confirmed not to affect
  the production path** — Go manages the Python subprocess lifecycle
  by closing its stdin pipe, not by sending Ctrl+C/SIGINT, so this
  code path is a dev-testing convenience only.

---

## Correction — found in F3 (2026-08-29)

**`_demo_echo_body` flattened every reply's newlines.** Documented above as
"streams the input text back word-by-word" — true, but the implementation was
`for word in text.split(): yield {"text": word + " "}`. `str.split()` with no
argument splits on *any* run of whitespace and discards the separator
entirely, so a blank line, a fenced code block's line breaks, and a single
space were all indistinguishable by the time a chunk left this function. The
echoed reply was corrupted in the **stored** message — verifiable straight
from the database, before `cli.py`'s printing or any frontend rendering ever
touched it.

Invisible until now because nothing before F3 rendered a multi-line echoed
reply in a way where flattening was obvious — B9's tool-loop and B10/B11's
real providers don't go through this path at all (`echo` is the no-network
dev path only), and every manual echo test through B11 sent single-line text.

The code moved during B9's refactor — `_demo_echo_body` here became
`AgentExecutor._echo_body` in `backend\agents\executor.py` — so the fix lives
there, not in this file. It now iterates `re.finditer(r"\S+|\s+", text)`,
yielding each whitespace run verbatim alongside each word, so concatenating
every chunk reconstructs the original text exactly. Found and fixed while
verifying `design/tasks/frontend/F03-chat-viewport.md`'s multi-line
markdown/code-fence rendering; recorded here, not in F3's notes, because the
defect predates F3 and lived entirely on the backend side of the wire.
