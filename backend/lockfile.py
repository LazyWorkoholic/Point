r"""Single-instance lock (B6 15).

A second backend against the same database is not a second copy of Point
-- it is two runtimes writing the same rows with two in-memory task
tables that do not know about each other. Refuse, and say which pid is
holding it.

The lock is only meaningful because the backend now exits when stdin
closes (9.5). A lock held by a hung process that nothing kills is worse
than no lock at all.

`cli.py` deliberately does not take this lock: the CLI is a second
*process*, not a second instance of the app, and WAL exists precisely so
it can read and write alongside a running backend.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Optional

import appconfig


class AlreadyRunningError(RuntimeError):
    def __init__(self, pid: int) -> None:
        super().__init__(f"Point is already running (pid {pid})")
        self.pid = pid


class SingleInstanceLock:
    def __init__(self, path: Optional[Path] = None) -> None:
        self.path = path or appconfig.lock_path()
        self._held = False

    def acquire(self) -> None:
        """Raises AlreadyRunningError if a live process holds the lock.

        A lock file whose pid is dead is stale -- taken over rather than
        respected, or one crash would need a manual file deletion before
        the app could ever start again.
        """
        self.path.parent.mkdir(parents=True, exist_ok=True)
        holder = self.holder_pid()
        if holder is not None and holder != os.getpid():
            raise AlreadyRunningError(holder)
        self.path.write_text(str(os.getpid()), encoding="utf-8")
        self._held = True

    def holder_pid(self) -> Optional[int]:
        """The pid of the live process holding the lock, or None."""
        try:
            raw = self.path.read_text(encoding="utf-8").strip()
        except OSError:
            return None
        if not raw.isdigit():
            return None
        pid = int(raw)
        return pid if pid_alive(pid) else None

    def release(self) -> None:
        if not self._held:
            return
        try:
            if self.path.read_text(encoding="utf-8").strip() == str(os.getpid()):
                self.path.unlink()
        except OSError:
            pass
        self._held = False

    def __enter__(self) -> "SingleInstanceLock":
        self.acquire()
        return self

    def __exit__(self, *exc_info: object) -> None:
        self.release()


def pid_alive(pid: int) -> bool:
    """Windows note: os.kill(pid, 0) is NOT a liveness probe here -- on
    Windows CPython routes it to TerminateProcess, which would kill the
    very process we are asking about. OpenProcess + GetExitCodeProcess is
    the read-only way to ask."""
    if pid <= 0:
        return False
    if sys.platform != "win32":
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return False
        except PermissionError:
            return True
        return True

    import ctypes
    from ctypes import wintypes

    PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
    STILL_ACTIVE = 259

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    handle = kernel32.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
    if not handle:
        return False
    try:
        exit_code = wintypes.DWORD()
        if not kernel32.GetExitCodeProcess(handle, ctypes.byref(exit_code)):
            return False
        return exit_code.value == STILL_ACTIVE
    finally:
        kernel32.CloseHandle(handle)
