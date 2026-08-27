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


class TaskState(str, Enum):
    PENDING = "pending"
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
            self.state = TaskState.COMPLETED
            self.result = TaskResult(state=TaskState.COMPLETED)
            self.finished_at = self.result.finished_at
            await self._record()
            await self._sink(self, {"type": "done"})
        except asyncio.CancelledError:
            self.state = TaskState.CANCELLED
            self.result = TaskResult(state=TaskState.CANCELLED)
            self.finished_at = self.result.finished_at
            await self._record()
            await self._sink(self, {"type": "cancelled"})
            raise
        except Exception as exc:  # noqa: BLE001 - we deliberately convert to a result
            # `code` is read off the exception when it carries one --
            # every runtime.errors.ToolError subclass does (B10). Task
            # still knows nothing about tools: it copies an attribute if
            # it is there and leaves it None if it is not.
            code = getattr(exc, "code", None)
            self.state = TaskState.FAILED
            self.result = TaskResult(state=TaskState.FAILED, error=str(exc), code=code)
            self.finished_at = self.result.finished_at
            await self._record()
            await self._sink(self, {"type": "failed", "error": str(exc), "code": code})

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
        if self._asyncio_task is None or self._asyncio_task.done():
            return False
        self._asyncio_task.cancel()
        if self.state is TaskState.PENDING:
            self.state = TaskState.CANCELLED
            self.result = TaskResult(state=TaskState.CANCELLED)
            self.finished_at = self.result.finished_at
            self._record_soon()
        return True

    def _record_soon(self) -> None:
        """Fire-and-forget variant of _record() for synchronous callers."""
        if self._recorder is None:
            return
        try:
            asyncio.get_running_loop().create_task(self._record())
        except RuntimeError:
            pass  # no loop running: nothing is streaming either

    def is_active(self) -> bool:
        return self.state in (TaskState.PENDING, TaskState.RUNNING)

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