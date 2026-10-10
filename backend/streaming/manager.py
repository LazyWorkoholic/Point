"""StreamManager: tracks active streams and their accumulators.

Deliberately does NOT run its own consumer loop -- Task's existing
async-generator loop (Episode 1) already IS the consumer for a
stream's events. Adding a second queue/loop here would just duplicate
that. StreamManager's job is bookkeeping: sequence numbers, the
accumulator per task, and which streams are currently active.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from managers.editor_context import EditorContext
from managers.message import InvocationMetadata
from streaming.accumulator import StreamAccumulator


@dataclass(frozen=True)
class RegisteredEditorContext:
    """What a fill-mode task needs reachable at the terminal-event sink
    (B24): the request-side `EditorContext` as the invocation built it,
    plus the shown region -- carried from the SAME context build that
    produced this turn's prompt, never recomputed later (the file may
    have changed on disk by the time the sink runs). `EditorContext`
    itself gains no fields for this: it mirrors F11/F15's Go struct by
    name and by field, and `shown_start`/`shown_end` are a backend-only
    fact about one build, not part of that wire shape."""

    context: EditorContext
    shown_start: int
    shown_end: int


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
        # B24: same reasoning as `_invocations` -- a fill-mode task that
        # gets refused takes the echo path and never registers a stream
        # either, so this is keyed by task_id, not by stream, and
        # cleared by close_stream() alongside it.
        self._editor_contexts: dict[str, RegisteredEditorContext] = {}

    def register_stream(self, task_id: str, session_id: str, message_id: str) -> None:
        self._accumulators[task_id] = StreamAccumulator(task_id, session_id, message_id)
        self._sequences[task_id] = 0

    def register_invocation(self, task_id: str, invocation: InvocationMetadata) -> None:
        self._invocations[task_id] = invocation

    def invocation_for(self, task_id: str) -> Optional[InvocationMetadata]:
        return self._invocations.get(task_id)

    def register_editor_context(
        self, task_id: str, context: EditorContext, shown_start: int, shown_end: int,
    ) -> None:
        self._editor_contexts[task_id] = RegisteredEditorContext(context, shown_start, shown_end)

    def editor_context_for(self, task_id: str) -> Optional[RegisteredEditorContext]:
        return self._editor_contexts.get(task_id)

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
        self._editor_contexts.pop(task_id, None)

    def active_task_ids(self) -> list[str]:
        return list(self._accumulators.keys())