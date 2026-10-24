r"""MessageCheckpointRepository: the partial reply, on disk, before the
process dies (B15).

Raw SQL only, same shape as every other repository here.

## Why this exists at all

`StreamAccumulator` holds in-flight assistant text in memory and nothing
else does. It reaches `messages` from exactly one place -- the terminal
branch of `Runtime._make_stream_sink`, through `finalize()` or
`finalize_interrupted()`. That covers every way a task can *end*: done,
failed, cancelled, bounds-exceeded, even `Ctrl+C` (SIGINT raises
KeyboardInterrupt, `asyncio.run` unwinds, `Runtime.close()` cancels and
awaits, and the sink's cancelled branch persists the partial).

It does not cover the process being killed. `Stop-Process -Force` is a
SIGKILL: no handler runs, no terminal event fires, the accumulator dies
with the process, and `recover_orphaned_tasks` buries the `tasks` row
while never touching `messages`. B15's Done-when #3 wants the partial to
survive that, so something has to be on disk before it happens.

## What a checkpoint is, and is not

It is a row holding whatever text had arrived by the last tick, keyed by
the `message_id` the real `messages` row will eventually use. It is NOT a
transcript entry: nothing that reads history can see it (see migration
v7's note on why it is a separate table), and it is deleted the moment
the real row is written.

Three operations, and the order between them is the whole contract:

    write()    every ~500ms while text is arriving (not per delta)
    clear()    at the terminal event, AFTER the real row is persisted
    promote()  at startup, for whatever clear() never got to run on

`clear()` after the real insert, never before: a crash in the gap
between the two leaves a checkpoint that promotion turns into an
`interrupted` row, which is wrong-but-honest. The other order leaves
nothing at all, which is the failure this file exists to remove.
"""

from __future__ import annotations

import json
import sqlite3
import time
from pathlib import Path
from typing import Any, Optional, Union

import aiosqlite

# What promotion stamps a surviving checkpoint with. The same slug
# `recover_orphaned_tasks` writes into `tasks.error_code`, because it is
# the same event seen from the other table -- a reader must not have to
# learn two names for one crash.
ERROR_BACKEND_DIED = "backend_died"

_UPSERT = """
INSERT INTO message_checkpoints
    (message_id, session_id, task_id, content, invocation,
     input_tokens, output_tokens, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    content       = excluded.content,
    invocation    = excluded.invocation,
    input_tokens  = excluded.input_tokens,
    output_tokens = excluded.output_tokens,
    updated_at    = excluded.updated_at
"""


class MessageCheckpointRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    async def write(
        self,
        message_id: str,
        session_id: str,
        task_id: str,
        content: str,
        *,
        invocation: Optional[dict[str, Any]] = None,
        input_tokens: int = 0,
        output_tokens: int = 0,
    ) -> None:
        """One upsert per tick. Goes through the shared
        `SerializedConnection` like every other write in the runtime
        (B22) -- a checkpoint lands between whole statements, never
        inside another coroutine's half-finished cursor."""
        now = time.time()
        await self._conn.execute(
            _UPSERT,
            (
                message_id, session_id, task_id, content,
                json.dumps(invocation) if invocation else None,
                input_tokens, output_tokens, now, now,
            ),
        )
        await self._conn.commit()

    async def clear(self, message_id: str) -> None:
        await self._conn.execute(
            "DELETE FROM message_checkpoints WHERE message_id = ?", (message_id,)
        )
        await self._conn.commit()

    async def clear_for_task(self, task_id: str) -> None:
        """Belt and braces for a task whose message_id the caller no
        longer has to hand -- the echo path and the
        registered-but-never-streamed case."""
        await self._conn.execute(
            "DELETE FROM message_checkpoints WHERE task_id = ?", (task_id,)
        )
        await self._conn.commit()

    async def count(self) -> int:
        cursor = await self._conn.execute(
            "SELECT COUNT(*) AS n FROM message_checkpoints"
        )
        row = await cursor.fetchone()
        return int(row["n"]) if row else 0


def promote_orphaned_checkpoints(db_path: Union[str, Path]) -> int:
    """Turn every surviving checkpoint into a real `messages` row marked
    `interrupted`, and delete it. Returns how many were promoted.

    Synchronous and standalone, for the same reason
    `recover_orphaned_tasks` is: this runs at startup before a Runtime
    exists and before one event is accepted, so there is nothing to be
    asynchronous with yet. It runs in the same place, immediately after
    that function, and the pairing is deliberate -- `tasks` says the run
    died, `messages` says what it had produced, and a reader that sees
    one without the other has half the story.

    A checkpoint only survives a process that did not get to run
    `clear()`, which is a process that was killed. So every row this
    finds is by definition a crash casualty; there is no live-process
    case to exclude, and no need to join `tasks` to decide.

    `seq` is assigned here by `insert_message`'s own `MAX(seq) + 1`
    expression, not carried from the checkpoint, so a promoted row sorts
    after anything else that reached the session first.

    The promoted row is deliberately NOT given a new id: it uses the
    checkpoint's `message_id`, which is the id the sink would have used.
    `INSERT OR IGNORE` therefore makes this idempotent against the one
    ordering that could collide -- a crash between `insert_message` and
    `clear()`, where the real row is already there. The checkpoint is
    dropped either way.
    """
    path = Path(db_path)
    if not path.exists():
        return 0

    conn = sqlite3.connect(str(path), timeout=10.0)
    try:
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA busy_timeout = 5000")
        conn.execute("PRAGMA foreign_keys = ON")

        # The table may not exist yet on a database that has not been
        # migrated to v7 -- doctor and migrate both reach here on paths
        # this function must not raise from.
        exists = conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'message_checkpoints'"
        ).fetchone()
        if not exists:
            return 0

        rows = conn.execute(
            "SELECT * FROM message_checkpoints ORDER BY created_at ASC"
        ).fetchall()
        if not rows:
            return 0

        promoted = 0
        for row in rows:
            content = row["content"] or ""
            if not content:
                # Nothing to preserve. Same policy as
                # `finalize_interrupted`, which returns None rather than
                # persisting an empty assistant message.
                conn.execute(
                    "DELETE FROM message_checkpoints WHERE message_id = ?",
                    (row["message_id"],),
                )
                continue

            # The `content` column stores a Message content dict as
            # JSON, and `AssistantMessage` wraps plain text as
            # {"type": "text", "text": ...} -- built here rather than
            # imported, because this runs before the runtime exists and
            # must not depend on the managers layer booting.
            content_json = json.dumps({"type": "text", "text": content})
            storage_metadata = json.dumps({
                "metadata": {"reason": "backend_died"},
                "invocation": json.loads(row["invocation"]) if row["invocation"] else None,
            })
            total = int(row["input_tokens"]) + int(row["output_tokens"])
            cursor = conn.execute(
                """
                INSERT OR IGNORE INTO messages
                    (id, session_id, task_id, role, content, created_at, metadata,
                     input_tokens, output_tokens, total_tokens,
                     interrupted, outcome, error_code, seq)
                VALUES (?, ?, ?, 'assistant', ?, ?, ?, ?, ?, ?, 1, 'failed', ?,
                        (SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?))
                """,
                (
                    row["message_id"], row["session_id"], row["task_id"],
                    content_json, row["created_at"], storage_metadata,
                    int(row["input_tokens"]), int(row["output_tokens"]), total,
                    ERROR_BACKEND_DIED, row["session_id"],
                ),
            )
            if cursor.rowcount:
                promoted += 1
            conn.execute(
                "DELETE FROM message_checkpoints WHERE message_id = ?",
                (row["message_id"],),
            )
        conn.commit()
        return promoted
    finally:
        conn.close()

