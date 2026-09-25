r"""Concurrency regression: a burst of sends into one session must never
lose a terminal event, misreport a completed task as failed, or crash the
shared database connection.

The defect this guards (found live, confirmed from raw stderr and DB rows):
3-4 concurrent sends into one session raced a task's successful completion
against the SQLite commit that persists it. Two different tracebacks were
observed for the SAME root cause, at the two sequential commits inside
SessionManager.append_message() (session_repository.py's insert_message,
then its update_fields):

    sqlite3.OperationalError: cannot commit transaction - SQL statements
    in progress

All repositories share one aiosqlite connection with no serialization: a
coroutine's `cursor = await conn.execute(...)` and its later
`await cursor.fetchone()/fetchall()` are two separate awaits with a
suspension point between them, and any other coroutine's queued
`commit()` could land in that gap while the first coroutine's SELECT
cursor was still open -- SQLite refuses to commit while any cursor on the
connection has not been stepped to completion. `context.build()` (which
runs at task-creation time, not gated by the turn lane) is exactly the
kind of read that leaves a cursor open across an await boundary while
another task's completion tries to commit.

When the primary commit failed, Task._run()'s exception handler then
mislabeled the (actually successful) task FAILED and tried to sink a
"failed" event -- which itself crashed with
`IntegrityError('UNIQUE constraint failed: messages.id')`, uncaught,
surfacing as "Task exception was never retrieved" with zero terminal
events ever reaching the client for that task.

Two independent fixes, both exercised here:

  1. database/connection.py: SerializedConnection makes every `execute()`
     acquire a connection-wide lock and fully drain its cursor before
     returning, so no cursor is ever left "in progress" across an await
     boundary for another coroutine's commit() to trip over.

  2. runtime/task.py + runtime/runtime.py: even if persisting a *already
     successful* task's reply fails for some other reason, the task's
     live state must stay COMPLETED (never flipped to FAILED by a
     bookkeeping failure) and exactly one terminal event must still reach
     the client -- Task._sink_terminal() cannot let an exception escape
     _run() uncaught, and the sink's persistence step is wrapped so a DB
     hiccup can't swallow the terminal TaskDone/TaskFailed/TaskCancelled
     emission.

This script asserts, across several bursts:
  - every task gets exactly one terminal event (TaskDone/TaskFailed/
    TaskCancelled), never zero, never more than one;
  - every task whose terminal event is TaskDone has a `tasks` row whose
    `status` is 'completed' (not 'failed');
  - the asyncio loop's exception handler is never invoked (the direct
    signal for "Task exception was never retrieved"); and
  - stderr contains none of the failure signatures above, nor either of
    this fix's own defensive log lines (their presence would mean the
    persistence-failure path still triggered for some other reason).

Run from inside backend\, against a SCRATCH profile (CLAUDE.md's
APPDATA/LOCALAPPDATA override -- never the real one):

    uv run python tests\manual_concurrent_completion.py

Exits non-zero if any check fails.
"""

from __future__ import annotations

import asyncio
import io
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Optional

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

# Must be set BEFORE the provider registry is built: the mock only
# registers under POINT_MOCK=1 (providers/mock/provider.py).
os.environ["POINT_MOCK"] = "1"
os.environ.setdefault(
    "POINT_MOCK_FIXTURE",
    str(Path(__file__).resolve().parent / "fixtures" / "mock_slow.json"),
)

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
import runtime.runtime as runtime_module  # noqa: E402
from runtime.events import ENVELOPE_VERSION, OutgoingEvent, OutgoingEventType  # noqa: E402
from runtime.runtime import Runtime  # noqa: E402

FAILURES: list[str] = []

TERMINAL_TYPES = {
    OutgoingEventType.TASK_DONE: "completed",
    OutgoingEventType.TASK_FAILED: "failed",
    OutgoingEventType.TASK_CANCELLED: "cancelled",
}

# Every OutgoingEvent this run emits, as (type, task_id), in emission order.
EMITTED: list[tuple[OutgoingEventType, Optional[str]]] = []

_original_emit = Runtime._emit


async def _recording_emit(self: Runtime, event: OutgoingEvent) -> None:
    EMITTED.append((event.type, event.task_id))
    await _original_emit(self, event)


Runtime._emit = _recording_emit  # type: ignore[method-assign]

# chat.queue_depth defaults to 1 (one streaming + one waiting, a third
# refused with queue_full). Raised in-process for this driver only, so an
# all-plain burst of several can be accepted without writing a config file
# into anyone's profile.
runtime_module.turn_queue_depth = lambda: 8

# Unhandled-exception detector: the direct signal behind "Task exception
# was never retrieved". A clean run must never call this.
LOOP_EXCEPTIONS: list[dict[str, Any]] = []


class _StderrTee(io.TextIOBase):
    """Forwards to the real stderr (so the run is still visible) while
    keeping everything written for the post-run text assertions below."""

    def __init__(self, real: Any) -> None:
        self._real = real
        self.captured = io.StringIO()

    def write(self, s: str) -> int:
        self.captured.write(s)
        return self._real.write(s)

    def flush(self) -> None:
        self._real.flush()


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


async def send(runtime: Runtime, session_id: str, text: str) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "SendMessage",
        "payload": {"session": session_id, "text": text},
    })


async def drain_until_idle(runtime: Runtime, timeout: float = 90.0) -> None:
    """Drain the outbox (an undrained bounded queue blocks the runtime)
    until no task is active, bounded so a hang fails instead of spinning."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            await asyncio.wait_for(runtime.outbox.get(), timeout=0.1)
            continue
        except asyncio.TimeoutError:
            pass
        if not runtime.tasks.list_active():
            for _ in range(200):
                try:
                    await asyncio.wait_for(runtime.outbox.get(), timeout=0.05)
                except asyncio.TimeoutError:
                    break
            return
    FAILURES.append(f"tasks still active after {timeout}s")


async def burst(runtime: Runtime, title: str, sends: list[str]) -> list[str]:
    """Fire every send with zero wait between them (every task created,
    and its context built, while earlier ones are still queued or
    streaming), drain, and return the task_ids TaskStarted assigned, in
    the order they started."""
    print(f"\n=== {title}: {len(sends)} send(s), zero wait between them")
    session = await runtime.sessions.create(title=title, provider="mock", model="mock-slow")
    before = len(EMITTED)

    for text in sends:
        await send(runtime, session.id, text)

    await drain_until_idle(runtime)
    round_events = EMITTED[before:]

    started_ids = [tid for etype, tid in round_events if etype == OutgoingEventType.TASK_STARTED]
    check(
        f"{len(sends)} send(s) -> {len(sends)} TaskStarted",
        len(started_ids) == len(sends), f"got {len(started_ids)}",
    )

    for task_id in started_ids:
        terminal = [etype for etype, tid in round_events if tid == task_id and etype in TERMINAL_TYPES]
        check(
            f"task {task_id[:8]} gets exactly one terminal event",
            len(terminal) == 1, f"got {[t.value for t in terminal]}",
        )
        if len(terminal) == 1:
            expected_status = TERMINAL_TYPES[terminal[0]]
            row = await runtime.task_repo.fetch_one(task_id)
            check(
                f"task {task_id[:8]} ({terminal[0].value}) tasks-row status == {expected_status!r}",
                row is not None and row["status"] == expected_status,
                f"row={row}",
            )

    return started_ids


def loop_exception_handler(loop: asyncio.AbstractEventLoop, context: dict[str, Any]) -> None:
    LOOP_EXCEPTIONS.append(context)
    loop.default_exception_handler(context)


async def main() -> int:
    asyncio.get_running_loop().set_exception_handler(loop_exception_handler)

    real_stderr = sys.stderr
    tee = _StderrTee(real_stderr)
    sys.stderr = tee
    try:
        # bootstrap.build_runtime() -> logging_setup.configure_logging()
        # creates its stderr handler against sys.stderr as it is AT THAT
        # CALL, which is why the tee has to be installed before this.
        runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
        try:
            # The live repro's exact shape: all-plain sends, one session,
            # nothing waited on, none failing -- reported to reproduce
            # with no background/@research task involved at all.
            await burst(runtime, "burst of 4, round 1", [
                "perform bubble sorting with code",
                "tell me importance of bees",
                "hi",
                "what is 2+2",
            ])
            # A second, larger round: same shape, more tasks, to make a
            # surviving race more likely to show up if the fix is
            # incomplete rather than merely lucky on round 1.
            await burst(runtime, "burst of 5, round 2", [
                "one", "two", "three", "four", "five",
            ])
            # Mixed shape: a background @research task's provider-lane
            # execution overlapping plain turn-lane sends -- the OTHER
            # shape the original defect was reported under.
            await burst(runtime, "mixed background + plain, round 3", [
                "@research perform bubble sorting with code",
                "tell me importance of bees",
                "hi",
            ])
        finally:
            await shutdown_runtime(runtime)
    finally:
        sys.stderr = real_stderr

    total_terminal = sum(1 for etype, _ in EMITTED if etype in TERMINAL_TYPES)
    total_started = sum(1 for etype, _ in EMITTED if etype == OutgoingEventType.TASK_STARTED)
    check("total terminal events == total started tasks, globally",
          total_terminal == total_started, f"{total_terminal} terminal vs {total_started} started")

    check("asyncio loop exception handler was never invoked",
          not LOOP_EXCEPTIONS,
          "; ".join(c.get("message", str(c)) for c in LOOP_EXCEPTIONS))

    stderr_text = tee.captured.getvalue()
    print(f"\n  (captured {len(stderr_text)} char(s) of stderr during the run)")
    for needle in (
        "Task exception was never retrieved",
        "OperationalError",
        "cannot commit transaction",
        "IntegrityError",
        "terminal sink call failed",
        "failed to persist",
        "failed to clear active_task_id",
    ):
        check(f"stderr does not contain {needle!r}", needle not in stderr_text)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
