# Episode 2 — Session System ✅

**Session identity, CRUD, SQLite persistence**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Original source: `completed_backend_task.md`, split per episode. Content is
> unchanged apart from heading levels.

---

## `database/connection.py`
Owns the single `aiosqlite` connection for the whole runtime — the
only file (along with `session_repository.py`) that would need to
change if SQLite were ever swapped for Postgres.

Current schema:
```sql
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    project_id      TEXT,                 -- nullable, added ahead of the Project layer
    title           TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT NOT NULL,
    workspace       TEXT,
    state           TEXT NOT NULL,
    created_at      REAL NOT NULL,
    updated_at      REAL NOT NULL,
    summary         TEXT,
    active_task_id  TEXT,
    metadata        TEXT NOT NULL DEFAULT '{}',
    settings        TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
```

`open_database(path)` — opens the connection, sets `row_factory =
aiosqlite.Row` (so query results are dict-like), enables
`PRAGMA foreign_keys = ON` (needed for the `ON DELETE CASCADE` on
`messages`), applies the schema, commits.

**Why `project_id` was added now:** the Project layer (see
`project_idea.md` §3) isn't built yet, but adding this nullable column
while the database has zero real rows costs nothing and avoids a
migration later. Same reasoning was applied earlier to `model` and
`workspace`.

## `managers/session.py`
Dataclasses only — no I/O, no SQL.

- `SessionState` enum: `ACTIVE`, `ARCHIVED`.
- `Message` — `role`, `content`, `created_at`.
- `Session` — full field set: `id`, `title`, `provider`, `model`,
  `workspace`, `project_id`, `state`, `created_at`, `updated_at`,
  `summary`, `active_task_id`, `metadata`, `settings`, `messages`
  (populated on demand, not by default — see `SessionManager.get`
  below).
- `Session.new(title, provider, model, workspace=None,
  project_id=None, settings=None)` *(staticmethod)* — generates a UUID
  id and timestamps, used by `SessionManager.create`.

**Field-by-field rationale is documented directly in this file's
module docstring** — kept there rather than duplicated here since it's
the canonical source and should stay next to the code it describes.

## `database/session_repository.py`
Pure SQL layer — no business rules. This file is what makes the
"only one layer touches the database" rule concrete and enforceable,
per the original architecture discussion.

- `insert(session)` — full-row `INSERT`.
- `fetch_one(session_id)` — returns `Optional[Session]` (`None` if not
  found; `SessionManager` is responsible for turning that into
  `SessionNotFoundError`, not this file).
- `fetch_all(include_archived)` — filters `WHERE state = 'active'`
  unless `include_archived=True`; always `ORDER BY updated_at DESC`.
- `update_fields(session_id, fields)` — generic column-level update;
  automatically stamps `updated_at`. Used both for real updates (e.g.
  renaming) and as a cheap "touch" (`update_fields(session_id, {})`)
  to bump `updated_at` when a message is appended.
- `delete(session_id)` — relies on the `ON DELETE CASCADE` foreign key
  to also remove the session's messages.
- `insert_message(session_id, message)` / `fetch_messages(session_id)`
  — message CRUD, ordered by `id ASC` (insertion order).
- `_row_to_session(row)` *(staticmethod)* — the single place a raw SQL
  row becomes a `Session` object, including `json.loads()` on the
  `metadata`/`settings` columns.

## `managers/session_manager.py`
Business logic layer. Knows session *rules*, delegates all storage to
`SessionRepository`. Explicitly does not know about Claude, OpenAI, or
any provider.

- `SessionNotFoundError(RuntimeException)` — raised by `get()` and by
  `_ensure_exists()` (used before any mutation, so e.g. renaming a
  nonexistent session fails clearly instead of silently no-op-ing).
- `create(title, provider, model, workspace=None, project_id=None,
  settings=None)` — builds a `Session.new(...)`, persists it.
- `get(session_id, load_messages=False)` — raises
  `SessionNotFoundError` if missing; only fetches the message history
  when explicitly asked, so `ListSessions` stays cheap.
- `list_all(include_archived=False)`.
- `rename(session_id, new_title)`.
- `archive(session_id)` / `unarchive(session_id)`.
- `delete(session_id)`.
- `update_metadata(session_id, patch)` — **shallow-merges** into
  existing metadata rather than replacing it wholesale; returns the
  updated `Session`.
- `update_settings(session_id, patch)` — same merge behavior as
  metadata, kept as a separate method/column because metadata is
  descriptive while settings are meant to be read by an agent's
  behavior (per `project_idea.md`'s field rationale).
- `set_active_task(session_id, task_id)` — called by `Runtime` when a
  task starts (`task.id`) and again when it ends (`None`). This is
  what lets `GetSession` answer "is anything running here?" without a
  separate `TaskManager` query.
- `append_message(session_id, role, content)` / `get_messages
  (session_id)`.

## `runtime/runtime.py` — Episode 2 additions
(See the Episode 1 entry above for the full method list; this is just
what Episode 2 specifically changed.)

- Constructor now takes `db_conn` and builds `self.sessions =
  SessionManager(db_conn)`.
- `_handle_send_message` now **requires** a valid, existing
  `session_id` (previously accepted any string) — this is the change
  that made conversations persist across restarts instead of living in
  an in-memory dict.
- Eight new event handlers for full session CRUD (listed above).
- `_session_to_dict()` centralizes serialization so every session-
  related outgoing event has an identical, complete shape.

---

# Manual Test Log

Recorded here because these confirmed specific behaviors, not just
"it ran without crashing":

1. **Concurrency (Episode 1):** two `SendMessage` events fired back to
   back produced genuinely interleaved `TaskToken` events from two
   different `task_id`s — confirmed the Runtime does not block on one
   task while another runs.
2. **Cancellation (Episode 1):** `CancelTask` sent after a task had
   already completed correctly returned `{"cancelled": false}` with no
   crash — confirmed `Task.cancel()`'s finished-task guard works under
   a real race condition, not just in theory.
3. **Persistence (Episode 2):** created a session, sent a message,
   restarted the process (`Ctrl+C` then `uv run main.py` again),
   called `ListSessions`/`GetSession` — session, updated `updated_at`,
   and full message history (both user and assistant messages) were
   all still present. Confirmed SQLite persistence works end-to-end,
   not just within a single process lifetime.
4. **Full field set (Episode 2 + `project_id`):** `GetSession` after a
   `CreateSession` → `RenameSession` → `UpdateSessionMetadata` →
   `SendMessage` sequence returned every field populated correctly
   (`model`, `workspace`, `metadata`, `settings`, `active_task_id`
   correctly `null` again post-completion, full `messages` array).

---
