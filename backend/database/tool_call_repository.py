r"""ToolCallRepository: raw SQL only, same shape as TaskRepository (B10).

The `tool_calls` table (migration v3) is the durable answer to "which
document contaminated this answer". Every executed call lands here,
successful or not -- a failed `read_file` on a path outside the project
is exactly as interesting after the fact as a successful one.

Insert-only. There is no update path and no delete path: a record of what
an agent did is not something the agent, or anything else at runtime,
gets to revise.
"""

from __future__ import annotations

import json
import time
from typing import Any, Optional

import aiosqlite

from tools.models import ToolCallRecord

_INSERT = """
INSERT INTO tool_calls
    (task_id, session_id, tool, args_json, ok, error, duration_ms,
     tokens_estimate, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
"""


class ToolCallRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def record(self, call: ToolCallRecord) -> None:
        await self._conn.execute(
            _INSERT,
            (
                call.task_id,
                call.session_id,
                call.tool,
                json.dumps(call.args, default=str),
                1 if call.ok else 0,
                call.error,
                call.duration_ms,
                call.tokens_estimate,
                time.time(),
            ),
        )
        await self._conn.commit()

    async def fetch_for_task(self, task_id: str) -> list[dict[str, Any]]:
        cursor = await self._conn.execute(
            "SELECT * FROM tool_calls WHERE task_id = ? ORDER BY id ASC", (task_id,)
        )
        return [dict(r) for r in await cursor.fetchall()]

    async def list_recent(
        self, limit: int = 50, session_id: Optional[str] = None
    ) -> list[dict[str, Any]]:
        query = "SELECT * FROM tool_calls"
        params: tuple[Any, ...] = ()
        if session_id:
            query += " WHERE session_id = ?"
            params = (session_id,)
        query += " ORDER BY id DESC LIMIT ?"
        cursor = await self._conn.execute(query, (*params, limit))
        return [dict(r) for r in await cursor.fetchall()]
