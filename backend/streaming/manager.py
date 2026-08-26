"""StreamManager: tracks active streams and their accumulators.

Deliberately does NOT run its own consumer loop -- Task's existing
async-generator loop (Episode 1) already IS the consumer for a
stream's events. Adding a second queue/loop here would just duplicate
that. StreamManager's job is bookkeeping: sequence numbers, the
accumulator per task, and which streams are currently active.
"""

from __future__ import annotations

from streaming.accumulator import StreamAccumulator


class StreamManager:
    def __init__(self) -> None:
        self._accumulators: dict[str, StreamAccumulator] = {}
        self._sequences: dict[str, int] = {}

    def register_stream(self, task_id: str, session_id: str, message_id: str) -> None:
        self._accumulators[task_id] = StreamAccumulator(task_id, session_id, message_id)
        self._sequences[task_id] = 0

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

    def active_task_ids(self) -> list[str]:
        return list(self._accumulators.keys())