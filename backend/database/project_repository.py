r"""ProjectRepository: raw SQL only, same shape as SessionRepository and
TaskRepository. No business rules here -- ProjectManager decides what a
stale lock or a moved project means; this file just reads and writes
rows in `projects` and `file_locks` (B7)."""

from __future__ import annotations

from typing import Optional

import aiosqlite

from managers.project import Project


class ProjectRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    # -- projects ---------------------------------------------------------

    async def upsert(self, project: Project) -> None:
        """Insert, or update in place if `project.id` already has a row.
        Used both by `project init` (first row) and by `resolve()` after
        a rename/move (root_path changed) or a plain re-open
        (last_opened_at bumped) -- match on id, never on root_path."""
        await self._conn.execute(
            """
            INSERT INTO projects (id, name, root_path, created_at, last_opened_at)
            VALUES (?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                name = excluded.name,
                root_path = excluded.root_path,
                last_opened_at = excluded.last_opened_at
            """,
            (project.id, project.name, project.root_path, project.created_at, project.last_opened_at),
        )
        await self._conn.commit()

    async def fetch_by_id(self, project_id: str) -> Optional[Project]:
        cursor = await self._conn.execute("SELECT * FROM projects WHERE id = ?", (project_id,))
        row = await cursor.fetchone()
        return self._row_to_project(row) if row else None

    @staticmethod
    def _row_to_project(row: aiosqlite.Row) -> Project:
        return Project(
            id=row["id"],
            name=row["name"],
            root_path=row["root_path"],
            created_at=row["created_at"],
            last_opened_at=row["last_opened_at"],
        )

    # -- file locks ---------------------------------------------------------
    # rel_path is always project-root-relative, forward-slash-separated --
    # never an absolute path -- so a lock survives the project moving to
    # a different drive letter.

    async def get_lock(self, project_id: str, rel_path: str) -> Optional[aiosqlite.Row]:
        cursor = await self._conn.execute(
            "SELECT * FROM file_locks WHERE project_id = ? AND rel_path = ?",
            (project_id, rel_path),
        )
        return await cursor.fetchone()

    async def put_lock(
        self, project_id: str, rel_path: str, holder: str, acquired_at: float, heartbeat_at: float
    ) -> None:
        """Unconditional write -- used for a fresh acquire, a stale
        takeover, and an explicit take_over alike. The caller has
        already decided this write is allowed."""
        await self._conn.execute(
            """
            INSERT INTO file_locks (project_id, rel_path, holder, acquired_at, heartbeat_at)
            VALUES (?, ?, ?, ?, ?)
            ON CONFLICT(project_id, rel_path) DO UPDATE SET
                holder = excluded.holder,
                acquired_at = excluded.acquired_at,
                heartbeat_at = excluded.heartbeat_at
            """,
            (project_id, rel_path, holder, acquired_at, heartbeat_at),
        )
        await self._conn.commit()

    async def touch_heartbeat(self, project_id: str, rel_path: str, heartbeat_at: float) -> None:
        await self._conn.execute(
            "UPDATE file_locks SET heartbeat_at = ? WHERE project_id = ? AND rel_path = ?",
            (heartbeat_at, project_id, rel_path),
        )
        await self._conn.commit()

    async def delete_lock(self, project_id: str, rel_path: str) -> None:
        await self._conn.execute(
            "DELETE FROM file_locks WHERE project_id = ? AND rel_path = ?",
            (project_id, rel_path),
        )
        await self._conn.commit()
