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

from pathlib import Path
from typing import Union

import aiosqlite

from database.migrations import PRAGMAS, run_migrations


async def open_database(path: Union[str, Path]) -> aiosqlite.Connection:
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
    return conn
