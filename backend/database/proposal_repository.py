r"""ProposalRepository: raw SQL over `agent_proposals` (B11, migration
v4). Same shape as AttachmentRepository -- deliberately narrow.

No update, no delete: v1 has exactly one status (`pending`) and no
review path. `accept`/`reject`/`edit`/`forget` are B13's, against
`memory_records`, not this table -- do not add them here.
"""

from __future__ import annotations

from typing import Optional

import aiosqlite

from managers.memory import PROPOSAL_STATUS_PENDING, Proposal


class ProposalRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def insert(self, proposal: Proposal) -> None:
        await self._conn.execute(
            """
            INSERT INTO agent_proposals
                (id, session_id, task_id, agent_id, content, status, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                proposal.id, proposal.session_id, proposal.task_id, proposal.agent_id,
                proposal.content, proposal.status, proposal.created_at,
            ),
        )
        await self._conn.commit()

    async def count_for_session(
        self, session_id: str, status: Optional[str] = PROPOSAL_STATUS_PENDING
    ) -> int:
        """The whole enforcement mechanism for "at most 2-3 per session"
        -- a count, checked before every insert, never a prompt asking
        the model to police itself."""
        query = "SELECT COUNT(*) AS n FROM agent_proposals WHERE session_id = ?"
        params: tuple = (session_id,)
        if status is not None:
            query += " AND status = ?"
            params = (session_id, status)
        cursor = await self._conn.execute(query, params)
        row = await cursor.fetchone()
        return int(row["n"]) if row else 0

    async def fetch_for_session(
        self, session_id: str, status: Optional[str] = None
    ) -> list[Proposal]:
        query = "SELECT * FROM agent_proposals WHERE session_id = ?"
        params: tuple = (session_id,)
        if status is not None:
            query += " AND status = ?"
            params = (session_id, status)
        query += " ORDER BY created_at ASC, rowid ASC"
        cursor = await self._conn.execute(query, params)
        rows = await cursor.fetchall()
        return [self._row_to_proposal(r) for r in rows]

    async def list_by_status(
        self, status: Optional[str] = PROPOSAL_STATUS_PENDING, limit: int = 50
    ) -> list[Proposal]:
        query = "SELECT * FROM agent_proposals"
        params: tuple = ()
        if status is not None:
            query += " WHERE status = ?"
            params = (status,)
        query += " ORDER BY created_at DESC, rowid DESC LIMIT ?"
        cursor = await self._conn.execute(query, (*params, limit))
        rows = await cursor.fetchall()
        return [self._row_to_proposal(r) for r in rows]

    @staticmethod
    def _row_to_proposal(row: aiosqlite.Row) -> Proposal:
        return Proposal(
            id=row["id"],
            session_id=row["session_id"],
            task_id=row["task_id"],
            agent_id=row["agent_id"],
            content=row["content"],
            status=row["status"],
            created_at=row["created_at"],
        )
