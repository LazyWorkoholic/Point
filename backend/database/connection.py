"""Owns the single aiosqlite connection for the whole runtime.

Only the repositories talk to this module. Swapping SQLite for Postgres
later means touching this file + the repositories -- nothing above
SessionManager needs to know or care.

The schema no longer lives here. B6 moved it into
database/migrations.py, because a CREATE TABLE IF NOT EXISTS block can
add a table but can never change one, and v2 has to rebuild `sessions`
to make provider/model nullable. Opening the database still guarantees a
migrated schema: any entry point that reaches open_database() gets one,
whether or not it went through bootstrap.build_runtime().
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any, Optional, Union

import aiosqlite

from database.migrations import PRAGMAS, run_migrations


class _DrainedCursor:
    """A cursor-shaped view over rows already pulled out of SQLite.

    Returned by `SerializedConnection.execute()` in place of the raw
    aiosqlite cursor. Every repository calls `fetchone()`/`fetchall()`
    on the object `execute()` gives back, in a separate `await` right
    after -- so this exists purely to make that second `await` a no-op
    against the real connection, rather than a second queued operation
    that could interleave with another coroutine's.
    """

    def __init__(self, rows: list[Any], lastrowid: Optional[int], rowcount: int) -> None:
        self._rows = rows
        self._pos = 0
        self.lastrowid = lastrowid
        self.rowcount = rowcount

    async def fetchone(self) -> Any:
        if self._pos >= len(self._rows):
            return None
        row = self._rows[self._pos]
        self._pos += 1
        return row

    async def fetchall(self) -> list[Any]:
        rows = self._rows[self._pos:]
        self._pos = len(self._rows)
        return rows


class SerializedConnection:
    """Wraps the one aiosqlite connection the whole runtime shares, and
    serializes every logical operation against it.

    The defect this exists to close: aiosqlite queues each awaited call
    (`execute()`, `fetchone()`, `fetchall()`, `commit()`, ...) as a
    separate item processed one at a time on its background thread, but
    the *event loop* decides which coroutine's next call gets queued
    next. A repository method's own `cursor = await conn.execute(...)`
    followed by `row = await cursor.fetchone()` looks atomic in source,
    but is two separate awaits with a suspension point between them --
    and with several Task coroutines sharing this connection (a burst of
    concurrent sends into one session), any other coroutine's queued
    call can land in that gap. If the connection still has an
    un-fetched SELECT cursor open (fetchone/fetchall not yet called) and
    another coroutine's `commit()` reaches the queue first, SQLite
    raises `OperationalError: cannot commit transaction - SQL statements
    in progress` -- the C-level check that (correctly) refuses to commit
    while any cursor on the connection has not been stepped to
    completion. This hit two different call sites (session_repository's
    `insert_message` and `update_fields`, both inside
    SessionManager.append_message) depending on timing, which is exactly
    what "the connection lacks isolation between concurrent coroutines"
    predicts -- it is not pinned to one query.

    Fixed here, once, instead of at every repository call site: `execute()`
    acquires a connection-wide lock, runs the statement, and fully drains
    whatever cursor it produced (`fetchall()`) *before* releasing the lock
    and returning. By the time any coroutine's `execute()` call returns
    control to the event loop, the connection has no statement left "in
    progress" -- so a `commit()` from any other coroutine, arriving at any
    point afterwards, can never find one. The object handed back
    (`_DrainedCursor`) answers `fetchone()`/`fetchall()` from the
    already-fetched rows without touching the real connection again, so
    every existing repository call site works unchanged.
    """

    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn
        self._lock = asyncio.Lock()

    async def execute(self, sql: str, params: Any = ()) -> _DrainedCursor:
        async with self._lock:
            cursor = await self._conn.execute(sql, params)
            try:
                rows = await cursor.fetchall()
            except Exception:
                # Not every statement produces a result set (INSERT/UPDATE/
                # DELETE without RETURNING) -- aiosqlite already treats
                # fetchall() on those as returning [], but this stays
                # defensive rather than assuming every driver does.
                rows = []
            lastrowid = cursor.lastrowid
            rowcount = cursor.rowcount
            await cursor.close()
            return _DrainedCursor(rows, lastrowid, rowcount)

    async def commit(self) -> None:
        async with self._lock:
            await self._conn.commit()

    async def close(self) -> None:
        async with self._lock:
            await self._conn.close()


async def open_database(path: Union[str, Path]) -> SerializedConnection:
    # Migrations run first, on their own synchronous connection: the
    # pre-migration backup is a file copy, and copying a database out
    # from under an open async connection is how you get a backup that
    # is missing the last write.
    run_migrations(path)

    conn = await aiosqlite.connect(str(path))
    conn.row_factory = aiosqlite.Row
    for name, value in PRAGMAS:
        await conn.execute(f"PRAGMA {name} = {value}")
    await conn.commit()
    return SerializedConnection(conn)
