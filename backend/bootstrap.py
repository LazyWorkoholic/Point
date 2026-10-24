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
from pathlib import Path
from typing import Optional

import appconfig
from database.message_checkpoint_repository import promote_orphaned_checkpoints
from database.migrations import SCHEMA_VERSION, current_version, run_migrations  # noqa: F401
from database.task_repository import recover_orphaned_tasks
from lockfile import AlreadyRunningError, SingleInstanceLock  # noqa: F401
from logging_setup import configure_logging, get_logger
from runtime.runtime import Runtime

_log = get_logger("bootstrap")

_lock: Optional[SingleInstanceLock] = None


class BootstrapError(RuntimeError):
    """Raised when the app cannot start for a reason a user can fix."""


# ---------------------------------------------------------------------------
# Database location
# ---------------------------------------------------------------------------

def resolve_db_path() -> Path:
    """POINT_DB -> config `database.path` -> %LOCALAPPDATA%\\Point\\runtime.db."""
    override = os.environ.get("POINT_DB")
    if override:
        path = Path(override).expanduser()
    else:
        configured = appconfig.config_value("database.path")
        path = Path(configured).expanduser() if configured else appconfig.default_db_path()

    path.parent.mkdir(parents=True, exist_ok=True)
    return path


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
        # B15: the other half of the same event. `recover_orphaned_tasks`
        # buries the `tasks` row; this turns whatever text that run had
        # already produced into a real `messages` row marked
        # `interrupted` with `error_code = backend_died`. Both halves or
        # neither: a reader who sees one without the other has a task
        # that failed for no visible reason, or an interrupted reply
        # belonging to a task still claiming to run.
        #
        # Order matters only in one direction -- tasks first, so a
        # promoted message is never the only evidence of a crash.
        promoted = promote_orphaned_checkpoints(db_path)
        # A non-zero count means the last run died with work in flight.
        # That is worth stderr, not just the file.
        log_at = _log.warning if buried or promoted else _log.info
        log_at(
            "Crash recovery: %d orphaned task(s) marked failed/backend_died, "
            "%d partial repl%s promoted from checkpoints",
            buried, promoted, "y" if promoted == 1 else "ies",
        )
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
