"""StreamAccumulator: builds a final Message from a stream of events.

Deliberately NOT "mutate AssistantMessage.content repeatedly" -- text
accumulates into a plain buffer, and exactly one Message gets built
when the stream actually completes (or is interrupted).
"""

from __future__ import annotations

from typing import Optional

from managers.message import AssistantMessage, Message, TokenUsage
from streaming.events import StreamEvent, StreamEventType


class StreamAccumulator:
    def __init__(self, task_id: str, session_id: str, message_id: str) -> None:
        self.task_id = task_id
        self.session_id = session_id
        self.message_id = message_id
        self._text_parts: list[str] = []
        self._usage = TokenUsage()

    def apply(self, event: StreamEvent) -> None:
        if event.type == StreamEventType.TEXT_DELTA:
            self._text_parts.append(event.data.get("text", ""))
        elif event.type == StreamEventType.USAGE_UPDATED:
            usage = TokenUsage.from_dict(event.data.get("usage"))
            # Summed, not replaced (B10). A tool loop streams several
            # provider turns into ONE accumulator, and each turn reports
            # its own usage; keeping only the last one would bill a
            # six-step research run as if it were a single question.
            self._usage = TokenUsage(
                input_tokens=self._usage.input_tokens + usage.input_tokens,
                output_tokens=self._usage.output_tokens + usage.output_tokens,
            )

    @property
    def text(self) -> str:
        return "".join(self._text_parts)

    def finalize(self) -> Optional[Message]:
        """Normal completion. Returns None if the provider completed
        with zero text (content filtering, model quirk, etc.) --
        consistent with finalize_interrupted()'s policy. An empty
        AssistantMessage would fail validation anyway."""
        if not self.text:
            return None
        return AssistantMessage(
            self.session_id, self.text, task_id=self.task_id,
            message_id=self.message_id, usage=self._usage,
        )

    def finalize_interrupted(self, reason: str, error: Optional[str] = None) -> Optional[Message]:
        """Failure/cancellation. Preserves whatever partial text
        arrived rather than discarding it -- per spec. Returns None
        if nothing was received (e.g. cancelled before any text
        arrived), since an empty-content Message would fail
        validation anyway."""
        if not self.text:
            return None
        metadata = {"interrupted": True, "reason": reason}
        if error:
            metadata["error"] = error
        return AssistantMessage(
            self.session_id, self.text, task_id=self.task_id,
            message_id=self.message_id, usage=self._usage, metadata=metadata,
        )



