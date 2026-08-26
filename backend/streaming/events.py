"""StreamEvent: the unified event vocabulary this episode introduces.

MESSAGE != EVENT. A Message (Episode 3) is conversation data; a
StreamEvent is something happening DURING execution -- the raw
material a StreamAccumulator turns into a Message once a task
completes. Nothing downstream of streaming/ ever sees a provider's
raw ProviderStreamChunk -- only StreamEvent.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any


class StreamEventType(str, Enum):
    TEXT_DELTA = "text_delta"
    THINKING_DELTA = "thinking_delta"       # reserved -- no provider emits this yet
    STATUS_CHANGED = "status_changed"       # reserved -- for Episode 8+ agents
    PROGRESS_UPDATED = "progress_updated"   # reserved -- for Episode 8+ agents
    USAGE_UPDATED = "usage_updated"
    TOOL_STARTED = "tool_started"           # reserved -- Episode 10 Tool System
    TOOL_COMPLETED = "tool_completed"       # reserved -- Episode 10 Tool System
    # B6 removed COMPLETED / FAILED / CANCELLED. Nothing produced them
    # (StreamAdapter._CHUNK_TYPE_MAP omitted them) and nothing consumed
    # them (_make_stream_sink's map omitted them too). Terminal semantics
    # live on the task, not on the stream -- a stream ending is not an
    # outcome, and pretending otherwise gave the same event two owners.


@dataclass
class StreamEvent:
    type: StreamEventType
    task_id: str
    session_id: str
    message_id: str
    sequence: int
    event_id: str = field(default_factory=lambda: str(uuid.uuid4()))
    timestamp: float = field(default_factory=time.time)
    data: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "event_id": self.event_id,
            "type": self.type.value,
            "task_id": self.task_id,
            "session_id": self.session_id,
            "message_id": self.message_id,
            "sequence": self.sequence,
            "timestamp": self.timestamp,
            "data": self.data,
        }