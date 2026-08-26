"""Global runtime lifecycle state.

This is NOT chat history, NOT session state, NOT task state.
It only answers one question: is the runtime accepting new work?
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


class RuntimeStatus(str, Enum):
    STARTING = "starting"
    READY = "ready"
    STOPPING = "stopping"
    STOPPED = "stopped"


@dataclass
class RuntimeState:
    """Mutable snapshot of the runtime's own health.

    Kept separate from business data (sessions/tasks) on purpose:
    Go's status bar can poll *this* cheaply without touching
    SessionManager or TaskManager internals.
    """

    status: RuntimeStatus = RuntimeStatus.STARTING
    active_task_count: int = 0
    started_at: float = field(default_factory=lambda: __import__("time").time())

    def is_accepting_events(self) -> bool:
        return self.status == RuntimeStatus.READY