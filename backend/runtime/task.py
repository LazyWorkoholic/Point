"""Task: the fundamental unit of work in the runtime.

Everything the user does — chatting, running a coding agent, indexing
a PDF for RAG — becomes a Task. The Runtime never knows what a task
*does*; it only knows a task's state (pending/running/done/failed/
cancelled) and how to cancel it.

Think of this the way VS Code thinks about a build: VS Code doesn't
understand C++, it understands "a task is running" and "a task can
be killed."
"""

from __future__ import annotations

import asyncio
import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, AsyncIterator, Awaitable, Callable, Optional

from logging_setup import get_logger

_log = get_logger("task")


class TaskState(str, Enum):
    PENDING = "pending"
    # Admitted, not yet started: every lane this task needs is full
    # (B12). New in B12 -- B12's own spec called this "the existing B1
    # state", which was wrong; B1 shipped PENDING/RUNNING/COMPLETED/
    # FAILED/CANCELLED and nothing between "created" and "running".
    #
    # PENDING and QUEUED are not the same thing and are not merged.
    # PENDING is the sub-millisecond gap between construction and the
    # event loop getting a turn -- nobody waits on it and nothing shows
    # it. QUEUED is a real, user-visible, possibly-minutes-long wait
    # that the UI must be able to display and the user must be able to
    # cancel out of. Collapsing them would make "is this thing actually
    # running?" unanswerable, which is the exact dishonesty this
    # episode exists to remove.
    QUEUED = "queued"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"
    # Reserved (B6 §10). Nothing produces either yet, and the approval.*
    # event namespace they belong to is documented as reserved in
    # decisions.md. They are here so `tasks.status` can accept them
    # without a migration when the agent loop needs them.
    PAUSED = "paused"
    AWAITING_APPROVAL = "awaiting_approval"


@dataclass
class TaskResult:
    """Final outcome of a task, regardless of what kind of work it did.

    B10 added `code`: a machine-readable slug beside the human `error`
    string, in the same spirit as `tasks.error_code` (which B6 created
    and only ever wrote `backend_died` into). Go has to be able to tell
    `bounds_exceeded` -- a loop that stopped at its boundary and kept its
    partial answer -- from `tool_error`, a genuine failure, and branching
    on the wording of an error message is how a UI ends up coupled to a
    sentence."""

    state: TaskState
    output: Any = None
    error: Optional[str] = None
    code: Optional[str] = None
    finished_at: float = field(default_factory=time.time)


# A task body is an async generator that yields incremental "chunk" dicts
# (e.g. {"text": "Hel"}) and returns nothing — Task wraps it and turns
# each yield into an emitted event. This is what lets ChatAgent (later)
# stream tokens without Task or Runtime knowing anything about LLMs.
TaskBody = Callable[[], AsyncIterator[dict[str, Any]]]

# Called by Task on every state change / chunk, so Runtime can forward
# it to Go. Task doesn't know about JSON or stdout — it just calls this.
EventSink = Callable[["Task", dict[str, Any]], Awaitable[None]]

# Called by Task after every state transition so the transition can be
# mirrored to a durable row (B6 §3). Task doesn't know about SQL either;
# it just calls this. Optional -- a Task with no recorder behaves exactly
# as it did before B6.
TaskRecorder = Callable[["Task"], Awaitable[None]]


class Task:
    """Wraps a single unit of async work with lifecycle + cancellation.

    Why this exists instead of just calling `asyncio.create_task` inline
    everywhere: every call site would have to reimplement state tracking,
    cancellation, and error handling. Task centralizes that once.

    Why it owns the coroutine instead of TaskManager owning it directly:
    cancellation and state need to live right next to the thing being
    cancelled, or you end up with race conditions between "task finished"
    and "task cancelled" arriving at the same time.
    """

    def __init__(
        self,
        body: TaskBody,
        session_id: str,
        kind: str,
        sink: EventSink,
        task_id: Optional[str] = None,
        recorder: Optional[TaskRecorder] = None,
        agent_id: Optional[str] = None,
    ) -> None:
        self.id: str = task_id or str(uuid.uuid4())
        self.session_id = session_id
        self.kind = kind  # e.g. "chat", "research", "coding" — opaque to Runtime
        # Which AgentConfig produced this task (B9). None for anything
        # created before B9's AgentExecutor existed as a call site --
        # Task itself has no opinion on what a missing agent_id means.
        self.agent_id = agent_id
        self.state: TaskState = TaskState.PENDING
        self.created_at: float = time.time()
        self.started_at: Optional[float] = None
        self.finished_at: Optional[float] = None
        self.result: Optional[TaskResult] = None

        self._body = body
        self._sink = sink
        self._recorder = recorder
        self._asyncio_task: Optional[asyncio.Task] = None

    def start(self) -> None:
        """Schedule the task body to run on the event loop. Never blocks."""
        if self._asyncio_task is not None:
            raise RuntimeError(f"Task {self.id} already started")
        self._asyncio_task = asyncio.create_task(self._run(), name=f"task-{self.id}")

    async def _run(self) -> None:
        self.state = TaskState.RUNNING
        self.started_at = time.time()
        await self._record()
        await self._sink(self, {"type": "started"})
        try:
            async for chunk in self._body():
                await self._sink(self, {"type": "chunk", **chunk})
        except asyncio.CancelledError:
            self.state = TaskState.CANCELLED
            self.result = TaskResult(state=TaskState.CANCELLED)
            self.finished_at = self.result.finished_at
            await self._record()
            await self._sink_terminal({"type": "cancelled"})
            raise
        except Exception as exc:  # noqa: BLE001 - we deliberately convert to a result
            # `code` is read off the exception when it carries one --
            # every runtime.errors.ToolError subclass does (B10). Task
            # still knows nothing about tools: it copies an attribute if
            # it is there and leaves it None if it is not.
            code = getattr(exc, "code", None)
            # B12: `bound` rides beside `code` by the same rule -- copied
            # off the exception if it is there, ignored if it is not.
            # BoundsExceededError is raised for max_steps, max_tool_calls
            # AND timeout_s, but a reader has to tell "this took too
            # long" (retry it) from "this agent was allowed N steps and
            # used them" (raise the bound). `code` alone cannot say
            # which; `bound` can.
            bound = getattr(exc, "bound", None)
            self.state = TaskState.FAILED
            self.result = TaskResult(state=TaskState.FAILED, error=str(exc), code=code)
            self.finished_at = self.result.finished_at
            await self._record()
            await self._sink_terminal(
                {"type": "failed", "error": str(exc), "code": code, "bound": bound}
            )
            return

        # The body itself is done deciding pass/fail -- this line only
        # runs if the `async for` above finished cleanly. Everything past
        # this point is bookkeeping (recording the row, telling the
        # client), and a failure in *that* must never be mistaken for the
        # task having failed: it stays COMPLETED, and `_sink_terminal`
        # guarantees the client still gets a terminal event either way.
        self.state = TaskState.COMPLETED
        self.result = TaskResult(state=TaskState.COMPLETED)
        self.finished_at = self.result.finished_at
        await self._record()
        await self._sink_terminal({"type": "done"})

    async def _sink_terminal(self, chunk: dict[str, Any]) -> None:
        """Deliver the one terminal sink call `_run()` makes, and never
        let a failure escape it uncaught.

        `_run()` is scheduled with a bare `asyncio.create_task()`
        (`start()`) and nothing awaits it directly, so an exception
        raised here would become "Task exception was never retrieved" on
        the event loop -- silently, with no terminal event ever reaching
        the client and this task's live state stuck wherever it was.
        Caught and logged instead: `_record()` already wrote the durable
        `tasks` row before this is called, so a client that never sees
        the live event still has a correct row to fall back on.
        """
        try:
            await self._sink(self, chunk)
        except Exception:  # noqa: BLE001
            _log.exception(
                "Task %s: terminal sink call failed for %r -- state stays %s",
                self.id, chunk.get("type"), self.state.value,
            )

    async def _record(self) -> None:
        """Mirror the current state to durable storage, if anyone asked
        to be told. Failing to write a row must never fail the task --
        the row is a record of the work, not the work."""
        if self._recorder is None:
            return
        try:
            await self._recorder(self)
        except Exception:  # noqa: BLE001
            pass

    def cancel(self) -> bool:
        """Request cancellation. Returns False if already finished.

        Cancelling does not itself transition state: CancelledError is
        raised inside _run(), and _run()'s handler is where the state
        changes and the row is written. The one exception is a task
        cancelled before its coroutine ever got a turn on the loop --
        _run() then never executes at all, so the row would sit at
        `pending` until the next startup buried it. That case is recorded
        here.
        """
        if self._asyncio_task is None and self.state is TaskState.QUEUED:
            # B12: "queued turns are cancellable before they start."
            # `_run()` will never execute for this task, so nothing else
            # will ever write its terminal state or emit its terminal
            # event -- both happen here instead. The scheduler skips it
            # on the next pump because it is no longer QUEUED.
            self.state = TaskState.CANCELLED
            self.result = TaskResult(state=TaskState.CANCELLED)
            self.finished_at = self.result.finished_at
            self._record_soon()
            self._emit_soon({"type": "cancelled"})
            return True
        if self._asyncio_task is None or self._asyncio_task.done():
            return False
        self._asyncio_task.cancel()
        if self.state is TaskState.PENDING:
            self.state = TaskState.CANCELLED
            self.result = TaskResult(state=TaskState.CANCELLED)
            self.finished_at = self.result.finished_at
            self._record_soon()
        return True

    def _emit_soon(self, chunk: dict[str, Any]) -> None:
        """Fire-and-forget sink call for a task whose `_run()` never
        executes. Only the cancelled-while-queued path uses it: every
        other terminal event is emitted from inside `_run()`, and there
        is exactly one of them per task either way.
        """
        try:
            asyncio.get_running_loop().create_task(self._sink(self, chunk))
        except RuntimeError:
            pass  # no loop running: nobody is listening either

    def _record_soon(self) -> None:
        """Fire-and-forget variant of _record() for synchronous callers."""
        if self._recorder is None:
            return
        try:
            asyncio.get_running_loop().create_task(self._record())
        except RuntimeError:
            pass  # no loop running: nothing is streaming either

    def is_active(self) -> bool:
        """QUEUED counts as active (B12). A queued task is work the user
        has asked for and has not got yet -- excluding it would make
        `active_task_count` under-report, let `sweep_finished` drop a
        task that has not run, and let shutdown skip cancelling it."""
        return self.state in (TaskState.PENDING, TaskState.QUEUED, TaskState.RUNNING)

    async def wait(self) -> None:
        """Wait for `_run()` to actually finish -- not just for
        `cancel()` to have been called (B11). Swallows the
        CancelledError `_asyncio_task` raises back at its own awaiter:
        a caller using this to wait out a cancellation it just
        requested already knows that; it only wants the guarantee that
        `_run()`'s cleanup (the sink call, the recorder write) has
        actually completed before it does something that assumes this
        task is done, like closing the database out from under it."""
        if self._asyncio_task is None:
            return
        try:
            await self._asyncio_task
        except asyncio.CancelledError:
            pass