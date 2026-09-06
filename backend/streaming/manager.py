"""StreamManager: tracks active streams and their accumulators.

Deliberately does NOT run its own consumer loop -- Task's existing
async-generator loop (Episode 1) already IS the consumer for a
stream's events. Adding a second queue/loop here would just duplicate
that. StreamManager's job is bookkeeping: sequence numbers, the
accumulator per task, and which streams are currently active.
"""

from __future__ import annotations

from typing import Optional

from managers.message import InvocationMetadata
from streaming.accumulator import StreamAccumulator


class StreamManager:
    def __init__(self) -> None:
        self._accumulators: dict[str, StreamAccumulator] = {}
        self._sequences: dict[str, int] = {}
        # B12: which agent/provider/model this task is running as.
        # Registered per TASK, not per stream, because the echo and
        # refusal paths persist an assistant message without ever
        # registering a stream -- and "which agent produced this" must
        # have an answer on those rows too, or provenance is lost for
        # exactly the replies that are hardest to explain after a
        # reload. Keyed by task_id and cleared by close_stream(), which
        # both terminal paths already call.
        self._invocations: dict[str, InvocationMetadata] = {}

    def register_stream(self, task_id: str, session_id: str, message_id: str) -> None:
        self._accumulators[task_id] = StreamAccumulator(task_id, session_id, message_id)
        self._sequences[task_id] = 0

    def register_invocation(self, task_id: str, invocation: InvocationMetadata) -> None:
        self._invocations[task_id] = invocation

    def invocation_for(self, task_id: str) -> Optional[InvocationMetadata]:
        return self._invocations.get(task_id)

    def next_sequence(self, task_id: str) -> int:
        self._sequences[task_id] = self._sequences.get(task_id, 0) + 1
        return self._sequences[task_id]

    def has_stream(self, task_id: str) -> bool:
        return task_id in self._accumulators

    def get_accumulator(self, task_id: str) -> StreamAccumulator:
        return self._accumulators[task_id]

    def close_stream(self, task_id: str) -> None:
        self._accumulators.pop(task_id, None)
        self._sequences.pop(task_id, None)
        self._invocations.pop(task_id, None)

    def active_task_ids(self) -> list[str]:
        return list(self._accumulators.keys())