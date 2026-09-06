r"""Versioned, forward-only migrations (B6 2).

Rules this runner enforces, because every one of them is something that
bites later:

  * Forward only. There is no `down`. A rollback is restoring the backup
    this runner takes before every migration.
  * The database file is copied to `runtime.db.bak-<version>` before the
    migration that version names is applied. `.bak-2` therefore holds the
    schema as it was at v1 -- the thing you want when v2 went wrong.
  * Standalone. `run_migrations()` takes a path and nothing else: doctor
    has to report on a database that may not exist, and `migrate` runs
    before a Runtime can safely open one.
  * Synchronous sqlite3, not aiosqlite. Migrations run before the event
    loop matters, and a schema rebuild is exactly the moment you want one
    connection and no concurrency.

An existing database that predates this runner is *baselined*, not
re-created: it already has v1's tables, so v1 is recorded as applied and
only v2 actually runs.
"""

from __future__ import annotations

import shutil
import sqlite3
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional, Union

SCHEMA_VERSION = 5

# Applied to every connection, here and in database/connection.py.
#   WAL          -- 12 makes shared-WAL the basis of SSH topology B, and
#                   two processes writing at once (the CLI beside a live
#                   backend) is a v1 requirement, not a v2 nicety.
#   busy_timeout -- without it, the second writer fails instantly with
#                   "database is locked" instead of waiting its turn.
#   foreign_keys -- SQLite defaults this OFF; messages ON DELETE CASCADE
#                   is silently inert without it.
PRAGMAS = (
    ("journal_mode", "WAL"),
    ("busy_timeout", "5000"),
    ("foreign_keys", "ON"),
)


# ---------------------------------------------------------------------------
# v1 -- the schema as B2/B3 shipped it. Reproduced verbatim so a fresh
# database walks the same path an existing one did, rather than being
# born at v2 with no history.
# ---------------------------------------------------------------------------

_V1 = """
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    project_id      TEXT,
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
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_id         TEXT,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    created_at      REAL NOT NULL,
    metadata        TEXT NOT NULL DEFAULT '{}',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tokens    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_task ON messages(task_id);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
"""


# ---------------------------------------------------------------------------
# v2 -- everything v1 of the product needs in the ground.
#
# Several columns here stay unused for weeks. That is the point: a schema
# change is the expensive retrofit, forms and logic are not.
#
# The sessions rebuild comes FIRST so the new tables that reference
# sessions(id) are created against the final table, not one that is about
# to be dropped.
# ---------------------------------------------------------------------------

_V2 = """
-- sessions: provider/model become nullable, because NULL is how a
-- session says "inherit from global config" (project_idea 10). SQLite
-- has no ALTER COLUMN, so this is the standard rebuild. B8 owns what
-- NULL *means*; this migration only makes it storable.
CREATE TABLE sessions_new (
    id              TEXT PRIMARY KEY,
    project_id      TEXT,
    title           TEXT NOT NULL,
    provider        TEXT,
    model           TEXT,
    workspace       TEXT,
    state           TEXT NOT NULL,
    created_at      REAL NOT NULL,
    updated_at      REAL NOT NULL,
    summary         TEXT,
    active_task_id  TEXT,
    metadata        TEXT NOT NULL DEFAULT '{}',
    settings        TEXT NOT NULL DEFAULT '{}',
    effort          TEXT,
    created_by      TEXT,
    archived_at     REAL
);

INSERT INTO sessions_new
    (id, project_id, title, provider, model, workspace, state, created_at,
     updated_at, summary, active_task_id, metadata, settings)
SELECT
     id, project_id, title, provider, model, workspace, state, created_at,
     updated_at, summary, active_task_id, metadata, settings
FROM sessions;

DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
CREATE INDEX IF NOT EXISTS idx_sessions_state   ON sessions(state);

-- tasks: the durable mirror of TaskManager._tasks. A task cannot outlive
-- the process that ran it, which is exactly why a row in a live state at
-- startup is a corpse and gets marked failed/backend_died.
CREATE TABLE IF NOT EXISTS tasks (
    id                  TEXT PRIMARY KEY,
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
    finished_at         REAL NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status  ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at);

CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    root_path       TEXT NOT NULL,
    created_at      REAL NOT NULL,
    last_opened_at  REAL
);

CREATE TABLE IF NOT EXISTS agents (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    description          TEXT,
    builtin              INTEGER NOT NULL DEFAULT 0,
    provider             TEXT,
    model                TEXT,
    effort               TEXT,
    system_prompt        TEXT,
    tools_json           TEXT,
    reads_session        INTEGER NOT NULL DEFAULT 0,
    reads_project        INTEGER NOT NULL DEFAULT 0,
    reads_global         INTEGER NOT NULL DEFAULT 0,
    writes_proposals     INTEGER NOT NULL DEFAULT 0,
    stm_window           INTEGER,
    max_steps            INTEGER,
    max_tool_calls       INTEGER,
    timeout_s            INTEGER,
    default_attach_mode  TEXT,
    created_at           REAL NOT NULL,
    updated_at           REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_records (
    id                 TEXT PRIMARY KEY,
    content            TEXT NOT NULL,
    tags_json          TEXT NOT NULL DEFAULT '[]',
    status             TEXT NOT NULL,          -- pending | accepted | rejected
    source             TEXT NOT NULL,          -- auto_extractor | user_card | user_manual
    agent_id           TEXT,
    origin_session_id  TEXT,
    origin_task_id     TEXT,
    embedding          BLOB NULL,
    created_at         REAL NOT NULL,
    updated_at         REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memory_status ON memory_records(status);

-- scope_id is NOT NULL DEFAULT '' rather than nullable: it is half a
-- primary key, and SQLite treats NULLs in a primary key as distinct, so
-- a nullable scope_id would allow the same global memory to be attached
-- to the global scope any number of times. '' means "this scope has no
-- id", which is the global case.
CREATE TABLE IF NOT EXISTS memory_scopes (
    memory_id  TEXT NOT NULL REFERENCES memory_records(id) ON DELETE CASCADE,
    scope      TEXT NOT NULL,                 -- session | project | global
    scope_id   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (memory_id, scope, scope_id)
);

CREATE TABLE IF NOT EXISTS context_attachments (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    source_type     TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    mode            TEXT NOT NULL,            -- full | reference | detached
    recorded_mtime  INTEGER,
    recorded_size   INTEGER,
    created_at      REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attachments_session ON context_attachments(session_id);

CREATE TABLE IF NOT EXISTS file_locks (
    project_id    TEXT NOT NULL,
    rel_path      TEXT NOT NULL,
    holder        TEXT NOT NULL,
    acquired_at   REAL NOT NULL,
    heartbeat_at  REAL NOT NULL,
    PRIMARY KEY (project_id, rel_path)
);

CREATE TABLE IF NOT EXISTS usage_records (
    id             TEXT PRIMARY KEY,
    session_id     TEXT,
    task_id        TEXT,
    agent_id       TEXT,
    provider       TEXT,
    model          TEXT,
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_micros    INTEGER NULL,
    estimated      INTEGER NOT NULL DEFAULT 1,
    created_at     REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_session ON usage_records(session_id);

CREATE TABLE IF NOT EXISTS presence (
    connection_id  TEXT PRIMARY KEY,
    user_name      TEXT,
    connected_at   REAL NOT NULL,
    heartbeat_at   REAL NOT NULL
);
"""


# ---------------------------------------------------------------------------
# v3 -- B10's tool-call ledger.
#
# The `tasks` row says a task ran and how it ended. It cannot say *what
# the answer was built from*, and "which document contaminated this
# answer" is a question that only gets asked after the task object has
# been swept out of memory. One row per executed call, written whether
# the call succeeded or failed, is the whole feature.
#
# `args_json` stores the arguments as they were validated, after the
# schema's defaults were applied -- the call as it actually ran, not as
# the model phrased it. Recording the phrasing instead would make a
# reader guess at the defaults in force that day.
#
# No FK to `tasks`: a tool call is evidence, and evidence should survive
# its subject. `tasks` rows are already swept, and ON DELETE CASCADE here
# would quietly take the ledger with them.
# ---------------------------------------------------------------------------

_V3 = """
CREATE TABLE IF NOT EXISTS tool_calls (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id          TEXT NOT NULL,
    session_id       TEXT NOT NULL,
    tool             TEXT NOT NULL,
    args_json        TEXT,
    ok               INTEGER NOT NULL,
    error            TEXT,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    tokens_estimate  INTEGER NOT NULL DEFAULT 0,
    created_at       REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_task    ON tool_calls(task_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool    ON tool_calls(tool);
"""


# ---------------------------------------------------------------------------
# v4 -- B11's narrow proposal stopgap.
#
# NOT `memory_records`/`memory_scopes` -- those tables already exist
# (v2) and are B13's to activate, with review, search, tags, and scope
# bindings. `agent_proposals` is deliberately smaller and differently
# named so the two never collide: one row per candidate fact, one
# status (`pending` -- v1 has no accept/reject path, that is B13's),
# and a per-session count is the whole enforcement mechanism the ≤3 cap
# needs. When B13 lands, it reads B11's completed doc and decides how
# (or whether) to migrate these rows into `memory_records`; this table
# is not renamed or repurposed to become that one.
# ---------------------------------------------------------------------------

_V4 = """
CREATE TABLE IF NOT EXISTS agent_proposals (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_id       TEXT,
    agent_id      TEXT,
    content       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    created_at    REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_proposals_session ON agent_proposals(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_proposals_status  ON agent_proposals(status);
"""


# ---------------------------------------------------------------------------
# v5 -- B12's terminal-outcome columns on `messages`.
#
# Real columns, not JSON inside `metadata`. Everything about how a reply
# ENDED is what a reloaded session has to re-render: a footer, a
# collapsed slot, an "interrupted" marker. F5 reconstructs those from
# these rows, and a value buried in a JSON blob is one that cannot be
# filtered, indexed, or read by a `sqlite3` one-liner during triage.
# `interrupted` in particular already existed as `metadata.interrupted`
# (B5); it is promoted here rather than duplicated, and the writer stops
# putting it in the blob.
#
#   interrupted  1 when the reply stopped before the model was finished.
#                Redundant with `outcome != 'done'` by construction, and
#                kept anyway: B5 shipped the concept, F5 already reads
#                it, and a boolean is the cheap answer to the cheap
#                question.
#   outcome      done | cancelled | failed | timed_out | bounds_exceeded.
#                NULL only on rows written before this migration -- the
#                honest value for "this predates the marker", which is
#                not the same claim as `done`.
#   error_code   The slug from TaskResult.code (`bounds_exceeded`,
#                `tool_timeout`, ...), mirrored beside the outcome so a
#                reader does not have to join `tasks` to know why.
# ---------------------------------------------------------------------------

_V5 = """
ALTER TABLE messages ADD COLUMN interrupted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN outcome     TEXT;
ALTER TABLE messages ADD COLUMN error_code  TEXT;
"""


@dataclass(frozen=True)
class Migration:
    version: int
    name: str
    script: str


MIGRATIONS: tuple[Migration, ...] = (
    Migration(1, "baseline sessions + messages", _V1),
    Migration(2, "tasks, projects, agents, memory, nullable session provider/model", _V2),
    Migration(3, "tool_calls ledger", _V3),
    Migration(4, "agent_proposals (B11 stopgap, not memory_records)", _V4),
    Migration(5, "message terminal outcome: interrupted, outcome, error_code", _V5),
)


@dataclass
class MigrationResult:
    db_path: Path
    from_version: int
    to_version: int
    applied: list[int] = field(default_factory=list)
    backups: list[Path] = field(default_factory=list)

    @property
    def changed(self) -> bool:
        return bool(self.applied)

    def describe(self) -> str:
        if not self.applied:
            return f"already at version {self.to_version}"
        versions = ", ".join(f"v{v}" for v in self.applied)
        return f"migrated {self.from_version} -> {self.to_version} ({versions})"


# ---------------------------------------------------------------------------
# Connections
# ---------------------------------------------------------------------------

def connect(db_path: Union[str, Path]) -> sqlite3.Connection:
    """A synchronous connection with Point's pragmas applied. autocommit
    (isolation_level=None) so the migration runner owns its own BEGIN /
    COMMIT rather than fighting sqlite3's implicit transactions."""
    conn = sqlite3.connect(str(db_path), timeout=10.0)
    conn.isolation_level = None
    conn.row_factory = sqlite3.Row
    for name, value in PRAGMAS:
        conn.execute(f"PRAGMA {name} = {value}")
    return conn


# ---------------------------------------------------------------------------
# Version tracking
# ---------------------------------------------------------------------------

def _ensure_migrations_table(conn: sqlite3.Connection) -> None:
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version    INTEGER PRIMARY KEY,
            applied_at REAL NOT NULL
        )
        """
    )


def _table_exists(conn: sqlite3.Connection, name: str) -> bool:
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", (name,)
    ).fetchone()
    return row is not None


def _recorded_version(conn: sqlite3.Connection) -> int:
    row = conn.execute("SELECT MAX(version) AS v FROM schema_migrations").fetchone()
    return int(row["v"]) if row and row["v"] is not None else 0


def _baseline(conn: sqlite3.Connection) -> int:
    """A database that predates this runner already has v1's tables.
    Record v1 as applied instead of running it again -- the script is
    idempotent, but a false 'applied v1' backup is not."""
    version = _recorded_version(conn)
    if version == 0 and _table_exists(conn, "sessions"):
        conn.execute(
            "INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)",
            (1, time.time()),
        )
        return 1
    return version


def current_version(db_path: Union[str, Path]) -> int:
    """0 for a database that does not exist or has no schema at all.
    Never creates the file -- doctor calls this."""
    path = Path(db_path)
    if not path.exists():
        return 0
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=5.0)
    conn.row_factory = sqlite3.Row
    try:
        if not _table_exists(conn, "schema_migrations"):
            return 1 if _table_exists(conn, "sessions") else 0
        return _recorded_version(conn)
    finally:
        conn.close()


# ---------------------------------------------------------------------------
# Running
# ---------------------------------------------------------------------------

def _backup(conn: sqlite3.Connection, path: Path, version: int) -> Optional[Path]:
    """Copy the database before applying `version`. Returns None when
    there is nothing to copy (a database being created right now).

    The WAL checkpoint matters: under WAL a plain file copy can miss
    everything still sitting in the -wal file, which is the difference
    between a backup and a file that looks like one.
    """
    if not path.exists() or path.stat().st_size == 0:
        return None
    row = conn.execute(
        "SELECT COUNT(*) AS n FROM sqlite_master "
        "WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'"
    ).fetchone()
    if row is None or row["n"] == 0:
        return None  # a database being created right now -- nothing to lose
    conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    target = path.with_name(f"{path.name}.bak-{version}")
    shutil.copy2(path, target)
    return target


def run_migrations(db_path: Union[str, Path], logger=None) -> MigrationResult:
    """Bring the database at `db_path` up to SCHEMA_VERSION.

    Callable without a Runtime, an event loop, or any of the rest of the
    backend -- `migrate` and `doctor` both need it that way.
    """
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)

    conn = connect(path)
    try:
        _ensure_migrations_table(conn)
        version = _baseline(conn)
        result = MigrationResult(db_path=path, from_version=version, to_version=version)

        for migration in MIGRATIONS:
            if migration.version <= version:
                continue

            backup = _backup(conn, path, migration.version)
            if backup is not None:
                result.backups.append(backup)
                if logger:
                    logger.info("Backed up database to %s", backup.name)

            # A rebuild drops and renames a table other tables reference.
            # foreign_keys OFF stops DROP TABLE sessions from cascading
            # every message into oblivion; legacy_alter_table stops the
            # RENAME from trying to reparse messages' FK against a table
            # that does not exist for the length of one statement. Both
            # are the documented SQLite procedure, and both are restored
            # below.
            conn.execute("PRAGMA foreign_keys = OFF")
            conn.execute("PRAGMA legacy_alter_table = ON")
            try:
                conn.executescript("BEGIN;\n" + migration.script + "\nCOMMIT;")
                conn.execute(
                    "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
                    (migration.version, time.time()),
                )
                violations = conn.execute("PRAGMA foreign_key_check").fetchall()
                if violations:
                    raise sqlite3.IntegrityError(
                        f"migration v{migration.version} left "
                        f"{len(violations)} foreign key violation(s)"
                    )
            except Exception:
                if conn.in_transaction:
                    conn.execute("ROLLBACK")
                raise
            finally:
                conn.execute("PRAGMA legacy_alter_table = OFF")
                conn.execute("PRAGMA foreign_keys = ON")

            result.applied.append(migration.version)
            result.to_version = migration.version
            if logger:
                logger.info("Applied migration v%d (%s)", migration.version, migration.name)

        return result
    finally:
        conn.close()
