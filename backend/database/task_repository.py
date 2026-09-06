"""TaskRepository: raw SQL only, same shape as SessionRepository.

The `tasks` table is a durable *mirror* of TaskManager._tasks, not a
replacement for it. TaskManager still owns the live objects; this file
only writes down what happened, so a crash leaves a trace, ListTasks can
answer something about yesterday, and git_checkpoint_sha has a home when
the Coding Assistant eventually needs one.

Every write is an upsert. TaskManager.create_task() is synchronous and
schedules its row without awaiting it, so an insert and the first state
update can reach the connection in either order -- an upsert makes that
harmless instead of a lost row.
"""

from __future__ import annotations

import sqlite3
import time
from pathlib import Path
from typing import Any, Optional, Union

import aiosqlite

# The only error_code v1 produces. It is a slug, not a message: Go
# branches on this, and Task.result already carries the human string.
# Add more only when something actually needs to branch on them.
ERROR_BACKEND_DIED = "backend_died"

# A row in one of these at startup is a corpse: a task cannot survive the
# process that ran it.
#
# B12 added `queued`, and it belongs here for the same reason the other
# two do -- more so, in fact. A queued task never started, so nothing
# will ever move it out of `queued` on its own; without this entry a
# backend crash would leave rows claiming to be waiting for a slot in a
# scheduler that no longer exists, and `tasks list` would report them as
# pending work forever.
LIVE_STATUSES = ("pending", "queued", "running")

_UPSERT = """
INSERT INTO tasks
    (id, session_id, agent_id, project_id, connection_id, kind, status,
     error_code, git_checkpoint_sha, created_at, started_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    status      = excluded.status,
    error_code  = COALESCE(excluded.error_code, tasks.error_code),
    started_at  = COALESCE(excluded.started_at, tasks.started_at),
    finished_at = COALESCE(excluded.finished_at, tasks.finished_at)
"""


class TaskRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def record(
        self,
        task_id: str,
        session_id: str,
        kind: str,
        status: str,
        created_at: float,
        started_at: Optional[float] = None,
        finished_at: Optional[float] = None,
        error_code: Optional[str] = None,
        agent_id: Optional[str] = None,
        project_id: Optional[str] = None,
        connection_id: Optional[str] = None,
        git_checkpoint_sha: Optional[str] = None,
    ) -> None:
        await self._conn.execute(
            _UPSERT,
            (
                task_id, session_id, agent_id, project_id, connection_id, kind,
                status, error_code, git_checkpoint_sha, created_at, started_at,
                finished_at,
            ),
        )
        await self._conn.commit()

    async def fetch_one(self, task_id: str) -> Optional[dict[str, Any]]:
        cursor = await self._conn.execute("SELECT * FROM tasks WHERE id = ?", (task_id,))
        row = await cursor.fetchone()
        return dict(row) if row else None

    async def list_recent(
        self, limit: int = 50, session_id: Optional[str] = None
    ) -> list[dict[str, Any]]:
        query = "SELECT * FROM tasks"
        params: tuple[Any, ...] = ()
        if session_id:
            query += " WHERE session_id = ?"
            params = (session_id,)
        query += " ORDER BY created_at DESC LIMIT ?"
        cursor = await self._conn.execute(query, (*params, limit))
        rows = await cursor.fetchall()
        return [dict(r) for r in rows]


def recover_orphaned_tasks(db_path: Union[str, Path]) -> int:
    """Mark every task still in a live state as failed/backend_died.

    Synchronous and standalone on purpose: this runs at startup before
    the Runtime exists and before a single event is accepted, so there is
    nothing to be asynchronous with yet. Returns how many rows it buried.
    """
    path = Path(db_path)
    if not path.exists():
        return 0

    conn = sqlite3.connect(str(path), timeout=10.0)
    try:
        conn.execute("PRAGMA busy_timeout = 5000")
        conn.execute("PRAGMA foreign_keys = ON")
        placeholders = ", ".join("?" for _ in LIVE_STATUSES)
        cursor = conn.execute(
            f"""
            UPDATE tasks
               SET status = 'failed',
                   error_code = ?,
                   finished_at = ?
             WHERE status IN ({placeholders})
            """,
            (ERROR_BACKEND_DIED, time.time(), *LIVE_STATUSES),
        )
        conn.commit()
        return cursor.rowcount or 0
    finally:
        conn.close()
