"""SessionRepository: raw SQL only. No business rules live here.

The distinction from SessionManager: this file would look almost
identical if you swapped SQLite for Postgres. It doesn't decide
things like "what happens when you rename an archived session" --
it just reads and writes rows. SessionManager owns the decisions.
"""

from __future__ import annotations

import json
import time
from typing import Any, Optional

import aiosqlite

from managers.message import (
    ContentType, InvocationMetadata, Message, MessageContent, MessageRole, Outcome, TokenUsage,
)
from managers.session import Session, SessionState

class SessionRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    # -- sessions -------------------------------------------------------

    async def insert(self, session: Session) -> None:
        await self._conn.execute(
            """
            INSERT INTO sessions
                (id, project_id, title, provider, model, workspace, state, created_at,
                 updated_at, summary, active_task_id, metadata, settings,
                 effort, created_by, archived_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                session.id, session.project_id, session.title, session.provider, session.model,
                session.workspace, session.state.value, session.created_at,
                session.updated_at, session.summary, session.active_task_id,
                json.dumps(session.metadata), json.dumps(session.settings),
                session.effort, session.created_by, session.archived_at,
            ),
        )
        await self._conn.commit()

    async def fetch_one(self, session_id: str) -> Optional[Session]:
        cursor = await self._conn.execute("SELECT * FROM sessions WHERE id = ?", (session_id,))
        row = await cursor.fetchone()
        return self._row_to_session(row) if row else None

    async def fetch_all(self, include_archived: bool) -> list[Session]:
        query = "SELECT * FROM sessions"
        if not include_archived:
            query += " WHERE state = 'active'"
        query += " ORDER BY updated_at DESC"
        cursor = await self._conn.execute(query)
        rows = await cursor.fetchall()
        return [self._row_to_session(r) for r in rows]

    async def update_fields(self, session_id: str, fields: dict[str, Any]) -> None:
        if not fields:
            fields = {}
        fields = {**fields, "updated_at": time.time()}
        set_clause = ", ".join(f"{k} = ?" for k in fields)
        await self._conn.execute(
            f"UPDATE sessions SET {set_clause} WHERE id = ?",
            (*fields.values(), session_id),
        )
        await self._conn.commit()

    async def delete(self, session_id: str) -> None:
        await self._conn.execute("DELETE FROM sessions WHERE id = ?", (session_id,))
        await self._conn.commit()

    # -- messages ---------------------------------------------------------

    async def insert_message(self, message: Message) -> None:
        """B12: `outcome`, `interrupted` and `error_code` go into real
        columns (migration v5), not into the `metadata` blob beside
        `invocation`. They are the three things a reloaded transcript
        has to know about how a reply ended, and a footer that can only
        be rebuilt by JSON-decoding every row is a footer that gets
        rebuilt wrong. `interrupted` is deliberately NOT also written to
        `metadata` -- one writer, one reader, no drift."""
        storage_metadata = {
            "metadata": message.metadata,
            "invocation": message.invocation.to_dict() if message.invocation else None,
        }
        await self._conn.execute(
            """
            INSERT INTO messages
                (id, session_id, task_id, role, content, created_at, metadata,
                 input_tokens, output_tokens, total_tokens,
                 interrupted, outcome, error_code)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                message.id, message.session_id, message.task_id, message.role.value,
                json.dumps(message.content.to_dict()), message.timestamp,
                json.dumps(storage_metadata),
                message.usage.input_tokens, message.usage.output_tokens, message.usage.total_tokens,
                1 if message.interrupted else 0,
                message.outcome.value if message.outcome else None,
                message.error_code,
            ),
        )
        await self._conn.commit()

    async def fetch_messages_for_task(self, task_id: str) -> list[Message]:
        """Every message one task produced, oldest first. Uses B6's
        existing idx_messages_task -- B10's load_task_result is the first
        thing that ever needed the index that has been there since the
        table was created."""
        cursor = await self._conn.execute(
            "SELECT * FROM messages WHERE task_id = ? ORDER BY created_at ASC, rowid ASC",
            (task_id,),
        )
        rows = await cursor.fetchall()
        return [self._row_to_message(r) for r in rows]

    async def fetch_messages(self, session_id: str) -> list[Message]:
        cursor = await self._conn.execute(
            "SELECT * FROM messages WHERE session_id = ? ORDER BY created_at ASC, rowid ASC",
            (session_id,),
        )
        rows = await cursor.fetchall()
        return [self._row_to_message(r) for r in rows]

    # -- helpers ------------------------------------------------------

    @staticmethod
    def _row_to_session(row: aiosqlite.Row) -> Session:
        return Session(
            id=row["id"],
            project_id=row["project_id"],
            title=row["title"],
            provider=row["provider"],
            model=row["model"],
            workspace=row["workspace"],
            state=SessionState(row["state"]),
            created_at=row["created_at"],
            updated_at=row["updated_at"],
            summary=row["summary"],
            active_task_id=row["active_task_id"],
            metadata=json.loads(row["metadata"]),
            settings=json.loads(row["settings"]),
            effort=row["effort"],
            created_by=row["created_by"],
            archived_at=row["archived_at"],
        )

    @staticmethod
    def _row_to_message(row: aiosqlite.Row) -> Message:
        stored = json.loads(row["metadata"])
        return Message(
            id=row["id"],
            session_id=row["session_id"],
            task_id=row["task_id"],
            role=MessageRole(row["role"]),
            content=MessageContent.from_dict(json.loads(row["content"])),
            timestamp=row["created_at"],
            metadata=stored.get("metadata", {}),
            invocation=InvocationMetadata.from_dict(stored.get("invocation")),
            usage=TokenUsage(
                input_tokens=row["input_tokens"],
                output_tokens=row["output_tokens"],
                total_tokens=row["total_tokens"],
            ),
            # NULL outcome means the row predates migration v5. Left as
            # None rather than defaulted to DONE: "we do not know how
            # this ended" and "this ended cleanly" are different claims,
            # and only one of them is true of an old row.
            outcome=Outcome(row["outcome"]) if row["outcome"] else None,
            interrupted=bool(row["interrupted"]),
            error_code=row["error_code"],
        )