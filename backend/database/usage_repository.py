"""UsageRepository: raw SQL over `usage_records` (migration v2's table,
unused until B14).

One row per completed request -- see managers\\usage.py for what decides
`cost_micros`/`estimated` and for the daily cap that reads this table's
`today_totals()`. This file holds no pricing or cap logic; it only
stores rows and sums them, the same split context_attachments draws
between AttachmentRepository (storage) and attachment_manager.py
(policy).
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass
from typing import Optional

import aiosqlite


@dataclass
class UsageRecord:
    id: str
    session_id: Optional[str]
    task_id: Optional[str]
    agent_id: Optional[str]
    provider: Optional[str]
    model: Optional[str]
    input_tokens: int
    output_tokens: int
    cost_micros: Optional[int]
    estimated: bool
    created_at: float


class UsageRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def insert(
        self,
        *,
        session_id: Optional[str],
        task_id: Optional[str],
        agent_id: Optional[str],
        provider: Optional[str],
        model: Optional[str],
        input_tokens: int,
        output_tokens: int,
        cost_micros: Optional[int],
        estimated: bool,
    ) -> UsageRecord:
        record = UsageRecord(
            id=str(uuid.uuid4()),
            session_id=session_id,
            task_id=task_id,
            agent_id=agent_id,
            provider=provider,
            model=model,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cost_micros=cost_micros,
            estimated=estimated,
            created_at=time.time(),
        )
        await self._conn.execute(
            """
            INSERT INTO usage_records
                (id, session_id, task_id, agent_id, provider, model,
                 input_tokens, output_tokens, cost_micros, estimated, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                record.id, record.session_id, record.task_id, record.agent_id,
                record.provider, record.model, record.input_tokens,
                record.output_tokens, record.cost_micros,
                1 if record.estimated else 0, record.created_at,
            ),
        )
        await self._conn.commit()
        return record

    async def session_totals(self, session_id: str) -> dict:
        """Sum of every row for one session. `cost_micros` is NULL
        unless every contributing row has a known price -- a partial
        sum that silently drops the unpriced rows would show a dollar
        figure that looks complete and is not."""
        cursor = await self._conn.execute(
            """
            SELECT
                COALESCE(SUM(input_tokens), 0)  AS input_tokens,
                COALESCE(SUM(output_tokens), 0) AS output_tokens,
                SUM(cost_micros)                AS cost_micros,
                SUM(CASE WHEN cost_micros IS NULL THEN 1 ELSE 0 END) AS unpriced_rows,
                COUNT(*)                         AS requests
            FROM usage_records WHERE session_id = ?
            """,
            (session_id,),
        )
        row = await cursor.fetchone()
        return self._totals_row(row)

    async def task_totals(self, task_id: str) -> dict:
        cursor = await self._conn.execute(
            """
            SELECT
                COALESCE(SUM(input_tokens), 0)  AS input_tokens,
                COALESCE(SUM(output_tokens), 0) AS output_tokens,
                SUM(cost_micros)                AS cost_micros,
                SUM(CASE WHEN cost_micros IS NULL THEN 1 ELSE 0 END) AS unpriced_rows,
                COUNT(*)                         AS requests
            FROM usage_records WHERE task_id = ?
            """,
            (task_id,),
        )
        row = await cursor.fetchone()
        return self._totals_row(row)

    async def totals_since(self, since_ts: float) -> dict:
        """Everything recorded at or after `since_ts` -- the daily cap
        and `usage today` are both just this with local midnight."""
        cursor = await self._conn.execute(
            """
            SELECT
                COALESCE(SUM(input_tokens), 0)  AS input_tokens,
                COALESCE(SUM(output_tokens), 0) AS output_tokens,
                SUM(cost_micros)                AS cost_micros,
                SUM(CASE WHEN cost_micros IS NULL THEN 1 ELSE 0 END) AS unpriced_rows,
                COUNT(*)                         AS requests
            FROM usage_records WHERE created_at >= ?
            """,
            (since_ts,),
        )
        row = await cursor.fetchone()
        return self._totals_row(row)

    async def by_agent(self) -> list[dict]:
        """All-time, grouped by agent_id. NULL (no agent attributed --
        currently unreachable, every turn routes through an agent config
        per B12, but the column allows it) sorts last."""
        cursor = await self._conn.execute(
            """
            SELECT
                agent_id,
                COALESCE(SUM(input_tokens), 0)  AS input_tokens,
                COALESCE(SUM(output_tokens), 0) AS output_tokens,
                SUM(cost_micros)                AS cost_micros,
                COUNT(*)                         AS requests
            FROM usage_records
            GROUP BY agent_id
            ORDER BY agent_id IS NULL, input_tokens + output_tokens DESC
            """,
        )
        rows = await cursor.fetchall()
        return [
            {
                "agent_id": row["agent_id"],
                "input_tokens": row["input_tokens"],
                "output_tokens": row["output_tokens"],
                "cost_micros": row["cost_micros"],
                "requests": row["requests"],
            }
            for row in rows
        ]

    async def by_provider(self) -> list[dict]:
        """All-time, grouped by provider."""
        cursor = await self._conn.execute(
            """
            SELECT
                provider,
                COALESCE(SUM(input_tokens), 0)  AS input_tokens,
                COALESCE(SUM(output_tokens), 0) AS output_tokens,
                SUM(cost_micros)                AS cost_micros,
                COUNT(*)                         AS requests
            FROM usage_records
            GROUP BY provider
            ORDER BY provider IS NULL, input_tokens + output_tokens DESC
            """,
        )
        rows = await cursor.fetchall()
        return [
            {
                "provider": row["provider"],
                "input_tokens": row["input_tokens"],
                "output_tokens": row["output_tokens"],
                "cost_micros": row["cost_micros"],
                "requests": row["requests"],
            }
            for row in rows
        ]

    async def latest_for_session(self, session_id: str) -> Optional[UsageRecord]:
        cursor = await self._conn.execute(
            "SELECT * FROM usage_records WHERE session_id = ? "
            "ORDER BY created_at DESC, rowid DESC LIMIT 1",
            (session_id,),
        )
        row = await cursor.fetchone()
        return self._row_to_record(row) if row else None

    # -- helpers ------------------------------------------------------

    @staticmethod
    def _totals_row(row: aiosqlite.Row) -> dict:
        unpriced = row["unpriced_rows"] or 0
        requests = row["requests"] or 0
        return {
            "input_tokens": row["input_tokens"],
            "output_tokens": row["output_tokens"],
            # NULL if ANY contributing row is unpriced, or there were no
            # rows at all -- a cost total is only honest when every row
            # that fed it actually has one.
            "cost_micros": row["cost_micros"] if (requests and not unpriced) else None,
            "requests": requests,
        }

    @staticmethod
    def _row_to_record(row: aiosqlite.Row) -> UsageRecord:
        return UsageRecord(
            id=row["id"],
            session_id=row["session_id"],
            task_id=row["task_id"],
            agent_id=row["agent_id"],
            provider=row["provider"],
            model=row["model"],
            input_tokens=row["input_tokens"],
            output_tokens=row["output_tokens"],
            cost_micros=row["cost_micros"],
            estimated=bool(row["estimated"]),
            created_at=row["created_at"],
        )
