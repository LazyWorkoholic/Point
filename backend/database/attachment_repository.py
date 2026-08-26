"""AttachmentRepository: raw SQL over `context_attachments`. Read and
update-the-recorded-pair only.

Deliberately incomplete. B12 owns `attach` / `detach` / `set_mode`, so
this file has no insert, no delete, and no way to change `mode`. B8
needs exactly two operations -- "what is attached to this session" and
"remember what I just sent" -- and giving it more would leave B12
arriving to find half its surface already built in a shape nobody
designed for it.

The one write here is not a business decision, which is why it lives at
this layer without a manager above it: resolving an attachment IS
recording that it was resolved. Splitting those apart gives you a
builder that can send a file and forget it did.
"""

from __future__ import annotations

from typing import Optional

import aiosqlite

from managers.attachment import Attachment


class AttachmentRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def fetch_for_session(self, session_id: str) -> list[Attachment]:
        """Oldest first: attachment order is the order they were made,
        and a stable order is what makes two `context report` runs
        comparable to a human reading them side by side."""
        cursor = await self._conn.execute(
            "SELECT * FROM context_attachments WHERE session_id = ? "
            "ORDER BY created_at ASC, rowid ASC",
            (session_id,),
        )
        rows = await cursor.fetchall()
        return [self._row_to_attachment(r) for r in rows]

    async def fetch_one(self, attachment_id: str) -> Optional[Attachment]:
        cursor = await self._conn.execute(
            "SELECT * FROM context_attachments WHERE id = ?", (attachment_id,)
        )
        row = await cursor.fetchone()
        return self._row_to_attachment(row) if row else None

    async def update_recorded_stat(self, attachment_id: str, mtime: int, size: int) -> None:
        """Record the `(mtime, size)` that was just sent. Called only
        after the fresh copy actually entered the context -- recording it
        before would mean an attachment evicted for budget is remembered
        as sent, and never re-sent."""
        await self._conn.execute(
            "UPDATE context_attachments SET recorded_mtime = ?, recorded_size = ? WHERE id = ?",
            (int(mtime), int(size), attachment_id),
        )
        await self._conn.commit()

    # -- helpers ------------------------------------------------------

    @staticmethod
    def _row_to_attachment(row: aiosqlite.Row) -> Attachment:
        return Attachment(
            id=row["id"],
            session_id=row["session_id"],
            source_type=row["source_type"],
            source_id=row["source_id"],
            mode=row["mode"],
            recorded_mtime=row["recorded_mtime"],
            recorded_size=row["recorded_size"],
            created_at=row["created_at"],
        )
