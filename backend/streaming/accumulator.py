"""StreamAccumulator: builds a final Message from a stream of events.

Deliberately NOT "mutate AssistantMessage.content repeatedly" -- text
accumulates into a plain buffer, and exactly one Message gets built
when the stream actually completes (or is interrupted).

B12 added two things to the Message this builds, and both are about a
transcript that has been reloaded from disk still telling the truth:

  * `invocation` -- which agent/provider/model produced the reply.
    Nothing wrote this before B12, so after a restart a `@research`
    answer was indistinguishable from an ordinary one.
  * `outcome` / `interrupted` / `error_code` -- HOW the reply ended.
    Before B12 a cancelled reply carried `metadata["interrupted"]` and
    nothing else, so a failure, a timeout and a loop that stopped at
    its bounds were all the same row, and a run that produced text
    before dying looked exactly like a completed answer.

`interrupted` is no longer written into `metadata` as well. It is a
column now (migration v5), and two writers for one fact is how the two
copies end up disagreeing.
"""

from __future__ import annotations

from typing import Optional

from managers.message import (
    AssistantMessage,
    InvocationMetadata,
    Message,
    Outcome,
    TokenUsage,
)
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

    def finalize(
        self, invocation: Optional[InvocationMetadata] = None
    ) -> Optional[Message]:
        """Normal completion. Returns None if the provider completed
        with zero text (content filtering, model quirk, etc.) --
        consistent with finalize_interrupted()'s policy. An empty
        AssistantMessage would fail validation anyway.

        Records `outcome=done` explicitly rather than leaving it NULL:
        NULL is reserved for rows written before migration v5, and
        "we do not know how this ended" is a different claim from
        "this ended cleanly"."""
        if not self.text:
            return None
        return AssistantMessage(
            self.session_id, self.text, task_id=self.task_id,
            message_id=self.message_id, usage=self._usage,
            invocation=invocation, outcome=Outcome.DONE,
        )

    def finalize_interrupted(
        self,
        reason: str,
        error: Optional[str] = None,
        *,
        outcome: Outcome = Outcome.FAILED,
        error_code: Optional[str] = None,
        invocation: Optional[InvocationMetadata] = None,
    ) -> Optional[Message]:
        """Failure/cancellation. Preserves whatever partial text
        arrived rather than discarding it -- per spec. Returns None
        if nothing was received (e.g. cancelled before any text
        arrived), since an empty-content Message would fail
        validation anyway.

        The partial text is kept for every outcome, not only
        cancellation: a bounds-stopped research run that produced four
        useful paragraphs before hitting its step limit has produced
        four useful paragraphs. `outcome` describes HOW it ended; it
        does not decide whether the content survives.
        """
        if not self.text:
            return None
        return AssistantMessage(
            self.session_id, self.text, task_id=self.task_id,
            message_id=self.message_id, usage=self._usage,
            metadata={"reason": reason, **({"error": error} if error else {})},
            invocation=invocation,
            outcome=outcome,
            interrupted=True,
            error_code=error_code,
        )
