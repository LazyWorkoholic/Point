r"""Everything that has to happen before a Runtime can exist, in one
place both entry points share (B6 13).

`main.py` used to fuse four responsibilities:

    1. Runtime construction
    2. stdio transport in
    3. stdio transport out
    4. process entry

Only (1) is shareable with a CLI. A CLI must not reuse (2) or (3): its
input is argv and its output is human-readable text on stdout, which
would collide head-on with the JSON-lines protocol channel. So (1) moved
here, and `main.py` is now that call plus its two loops.

`resolve_db_path()` and `run_migrations()` work without a Runtime on
purpose -- `doctor` has to report on a missing or broken database, and
`migrate` runs before a Runtime can safely open one.
"""

from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Optional

import appconfig
from database.migrations import SCHEMA_VERSION, current_version, run_migrations  # noqa: F401
from database.task_repository import recover_orphaned_tasks
from lockfile import AlreadyRunningError, SingleInstanceLock  # noqa: F401
from logging_setup import configure_logging, get_logger
from runtime.runtime import Runtime

_log = get_logger("bootstrap")

# Where the database lived before B6: inside the source tree, where it
# would be committed by accident and would break the moment the app was
# packaged. Copied out once, never deleted -- removing it is Aryan's call
# once he is satisfied the move worked.
LEGACY_DB_PATH = Path(__file__).resolve().parent / "runtime.db"

_lock: Optional[SingleInstanceLock] = None


class BootstrapError(RuntimeError):
    """Raised when the app cannot start for a reason a user can fix."""


# ---------------------------------------------------------------------------
# Database location
# ---------------------------------------------------------------------------

def resolve_db_path(migrate_legacy: bool = True) -> Path:
    """POINT_DB -> config `database.path` -> %LOCALAPPDATA%\\Point\\runtime.db.

    Also performs the one-time copy out of the source tree: if the chosen
    location has no database and `backend\\runtime.db` does, it is copied
    across and the move is logged. The old file is left where it is.
    """
    override = os.environ.get("POINT_DB")
    if override:
        path = Path(override).expanduser()
    else:
        configured = appconfig.config_value("database.path")
        path = Path(configured).expanduser() if configured else appconfig.default_db_path()

    path.parent.mkdir(parents=True, exist_ok=True)

    if migrate_legacy and not path.exists() and LEGACY_DB_PATH.exists():
        _copy_legacy_database(path)
    return path


def _copy_legacy_database(target: Path) -> None:
    shutil.copy2(LEGACY_DB_PATH, target)
    # A -wal/-shm pair beside the old file would hold writes the main file
    # does not. The pre-B6 database ran in journal_mode=delete so there
    # should not be one, but copying a database without them is how you
    # lose the last session you had open.
    for suffix in ("-wal", "-shm"):
        sidecar = LEGACY_DB_PATH.with_name(LEGACY_DB_PATH.name + suffix)
        if sidecar.exists():
            shutil.copy2(sidecar, target.with_name(target.name + suffix))
    _log.info(
        "Moved database out of the source tree: %s -> %s (original left in place)",
        LEGACY_DB_PATH, target,
    )


# ---------------------------------------------------------------------------
# Startup / shutdown
# ---------------------------------------------------------------------------

async def build_runtime(
    single_instance: bool = True,
    verbose: bool = False,
    say_hello: bool = True,
    heartbeat: bool = True,
) -> Runtime:
    """Directories, writability, the lock, migrations, crash recovery,
    logging -- then Runtime.create().

    `single_instance=False` is for `cli.py`: the CLI is a second process,
    not a second instance of the app, and WAL exists precisely so it can
    work alongside a running backend. That is also why crash recovery is
    conditional below.
    """
    global _lock

    appconfig.ensure_dirs()
    configure_logging(verbose=verbose)

    if not appconfig.is_writable(appconfig.state_dir()):
        raise BootstrapError(f"State directory is not writable: {appconfig.state_dir()}")

    lock = SingleInstanceLock()
    holder = lock.holder_pid()
    if single_instance:
        lock.acquire()          # raises AlreadyRunningError if one is live
        _lock = lock

    db_path = resolve_db_path()
    result = run_migrations(db_path, logger=_log)
    _log.info("Database %s: %s", db_path, result.describe())

    # Crash recovery, but only when we know nothing else is running: a
    # task in a live state is a corpse *because* the process that owned
    # it is gone. Running this from the CLI while a backend is streaming
    # would bury tasks that are very much alive.
    if single_instance or holder is None:
        buried = recover_orphaned_tasks(db_path)
        # A non-zero count means the last run died with work in flight.
        # That is worth stderr, not just the file.
        log_at = _log.warning if buried else _log.info
        log_at("Crash recovery: %d orphaned task(s) marked failed/backend_died", buried)
    else:
        _log.debug("Crash recovery skipped: backend already running (pid %s)", holder)

    runtime = await Runtime.create(db_path)
    if say_hello:
        await runtime.emit_hello()
    if heartbeat:
        runtime.start_heartbeat()
    return runtime


async def shutdown_runtime(runtime: Runtime) -> None:
    """Cancel active tasks, stop the heartbeat, close the database,
    release the lock. Safe to call more than once."""
    global _lock
    try:
        await runtime.close()
    finally:
        # The lock is released even if closing the runtime went wrong. A
        # lock left behind by a process that is exiting anyway would refuse
        # every future start for a pid that no longer exists.
        if _lock is not None:
            _lock.release()
            _lock = None
    _log.info("Backend stopped")
