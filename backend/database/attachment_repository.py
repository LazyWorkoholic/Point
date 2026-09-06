r"""AttachmentRepository: raw SQL over `context_attachments`. Read and
update-the-recorded-pair only.

B8 shipped this deliberately incomplete -- read, plus "remember what
I just sent" -- and left `attach` / `detach` / `set_mode` to B12 rather
than guessing at their shape an episode early. **B12 has now added
them**, below the B8 section: `insert`, `delete`, and `set_mode`.

`delete` really deletes the row, and that is not a contradiction of
"detach never deletes": what detach must never delete is the FILE and
any task result derived from it. `managers\attachment_manager.py` is
where that distinction lives -- it is a business rule, and this file
holds none.

The one write here is not a business decision, which is why it lives at
this layer without a manager above it: resolving an attachment IS
recording that it was resolved. Splitting those apart gives you a
builder that can send a file and forget it did.
"""

from __future__ import annotations

import time
import uuid
from typing import Optional

import aiosqlite

from managers.attachment import Attachment, SOURCE_TYPE_FILE


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

    # -- B12: the write surface B8 deliberately left unbuilt -------------

    async def insert(
        self,
        session_id: str,
        source_id: str,
        mode: str,
        source_type: str = SOURCE_TYPE_FILE,
        attachment_id: Optional[str] = None,
    ) -> Attachment:
        r"""Insert one attachment row and return it.

        `recorded_mtime`/`recorded_size` are left NULL on purpose, even
        though the caller has just stat'ed the file to guard it. A NULL
        pair reads as "never sent" (see `managers\attachment.py`), and
        that is exactly true of a file that has been attached but not
        yet included in any request. Recording the stat here would make
        the first turn believe the body is already in the history and
        send nothing at all.
        """
        attachment = Attachment(
            id=attachment_id or str(uuid.uuid4()),
            session_id=session_id,
            source_type=source_type,
            source_id=source_id,
            mode=mode,
            created_at=time.time(),
        )
        await self._conn.execute(
            """
            INSERT INTO context_attachments
                (id, session_id, source_type, source_id, mode,
                 recorded_mtime, recorded_size, created_at)
            VALUES (?, ?, ?, ?, ?, NULL, NULL, ?)
            """,
            (
                attachment.id, attachment.session_id, attachment.source_type,
                attachment.source_id, attachment.mode, attachment.created_at,
            ),
        )
        await self._conn.commit()
        return attachment

    async def delete(self, attachment_id: str) -> None:
        await self._conn.execute(
            "DELETE FROM context_attachments WHERE id = ?", (attachment_id,)
        )
        await self._conn.commit()

    async def set_mode(self, attachment_id: str, mode: str) -> None:
        """Changing the mode CLEARS the recorded pair.

        Not an optimization -- a correctness requirement, and the same
        one `_render_attachment` documents from the other side. A row
        switched from `full` to `reference` and back would otherwise
        still carry the stat from its last full send, so the builder
        would decide the body is already in the history and send
        nothing, forever. Clearing the pair means the next turn sends
        the file, which is what the user asked for by changing the mode.
        """
        await self._conn.execute(
            "UPDATE context_attachments "
            "SET mode = ?, recorded_mtime = NULL, recorded_size = NULL "
            "WHERE id = ?",
            (mode, attachment_id),
        )
        await self._conn.commit()

    async def fetch_by_source(self, session_id: str, source_id: str) -> Optional[Attachment]:
        """Attaching the same path twice is not two attachments. Used to
        make `attach` idempotent instead of growing a duplicate row (and
        a duplicate copy of the file in every future request) each time
        the user re-runs the same command."""
        cursor = await self._conn.execute(
            "SELECT * FROM context_attachments "
            "WHERE session_id = ? AND source_id = ? "
            "ORDER BY created_at ASC, rowid ASC LIMIT 1",
            (session_id, source_id),
        )
        row = await cursor.fetchone()
        return self._row_to_attachment(row) if row else None

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
