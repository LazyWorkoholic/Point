"""TaskManager: owns the lifetime of every Task in the runtime.

One job only: create tasks, hold them, look them up, cancel them,
clean up finished ones. It does not know what a chat, an agent, or
a session is — that knowledge lives one layer up, in whoever calls
create_task().
"""

from __future__ import annotations

from typing import Iterable, Optional

from runtime.errors import TaskNotFoundError
from runtime.scheduler import LaneKey, TaskScheduler
from runtime.task import EventSink, Task, TaskBody, TaskRecorder


class TaskManager:
    """B6 note: `_tasks` remains the in-memory index and the source of
    truth for anything live. The `tasks` table is a durable mirror of it,
    written through the optional `recorder` -- not a replacement for it,
    and not a second lifecycle."""

    def __init__(
        self,
        recorder: Optional[TaskRecorder] = None,
        scheduler: Optional[TaskScheduler] = None,
    ) -> None:
        self._tasks: dict[str, Task] = {}
        self._recorder = recorder
        # B12. TaskManager still owns lifetime; the scheduler owns only
        # *when* a task may begin. Optional so a TaskManager built
        # without one behaves exactly as it did before B12 -- every task
        # starts immediately, which is also what happens with a
        # scheduler and no lanes.
        self.scheduler = scheduler or TaskScheduler()

    def create_task(
        self,
        body: TaskBody,
        session_id: str,
        kind: str,
        sink: EventSink,
        task_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        lanes: Iterable[LaneKey] = (),
    ) -> Task:
        """Create, register, and submit a new task. Never blocks the caller.

        B12: "start" became "submit". A task with no `lanes` still
        starts immediately -- a task that names lanes starts when they
        all have room, and sits in QUEUED until then. The caller is what
        decides which lanes a task belongs to, because TaskManager
        deliberately does not know what a provider or a session is.
        """
        task = Task(body=body, session_id=session_id, kind=kind, sink=sink,
                    task_id=task_id, recorder=self._recorder, agent_id=agent_id)
        self._tasks[task.id] = task
        # The row is scheduled before the task can start, so the insert
        # reaches the connection ahead of the first state update. Every
        # write is an upsert anyway, so ordering is a nicety, not a
        # dependency.
        task._record_soon()
        self.scheduler.submit(task, lanes)
        return task

    def get(self, task_id: str) -> Task:
        task = self._tasks.get(task_id)
        if task is None:
            raise TaskNotFoundError(f"No task with id {task_id!r}")
        return task

    def cancel(self, task_id: str) -> bool:
        return self.get(task_id).cancel()

    def list_active(self) -> list[Task]:
        return [t for t in self._tasks.values() if t.is_active()]

    def list_all(self) -> list[Task]:
        return list(self._tasks.values())

    def active_count(self) -> int:
        return len(self.list_active())

    def sweep_finished(self, keep_last: int = 200) -> None:
        """Drop old finished tasks so memory doesn't grow forever.

        Called from Runtime's heartbeat, not on every event. Before B6
        this docstring claimed a caller that did not exist; the sweep is
        now genuinely wired up, and it loses nothing, because a finished
        task's row survives in `tasks` after the object is dropped.
        """
        finished = [t for t in self._tasks.values() if not t.is_active()]
        if len(finished) <= keep_last:
            return
        finished.sort(key=lambda t: t.created_at)
        for stale in finished[: len(finished) - keep_last]:
            self._tasks.pop(stale.id, None)