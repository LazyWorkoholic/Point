"""StreamAdapter: ProviderStreamChunk -> unified StreamEvent.

Only ONE adapter exists (see the episode-opening note on this) --
Episode 4 already normalized every provider's wire format, so there's
no provider-specific translation left to do here.
"""

from __future__ import annotations

from typing import Optional

from providers.models import ProviderChunkType, ProviderStreamChunk
from streaming.events import StreamEvent, StreamEventType

_CHUNK_TYPE_MAP = {
    ProviderChunkType.TEXT: StreamEventType.TEXT_DELTA,
    ProviderChunkType.THINKING: StreamEventType.THINKING_DELTA,
    ProviderChunkType.USAGE: StreamEventType.USAGE_UPDATED,
    # ProviderChunkType.TOOL_CALL is deliberately absent (B10). It is the
    # model *asking* for a tool, which is not an event anything outside
    # the loop should see: the tool may still be refused as ungranted, or
    # rejected by its schema, or never reached because a bound ran out.
    # `tool_started` is emitted by ToolExecutor when a call actually
    # begins, and it carries the name and arguments that a request chunk
    # does not put anywhere the sink can find them. Two events with one
    # type, one of them half-empty, is worse than one honest event.
}


class StreamAdapter:
    def adapt(
        self, chunk: ProviderStreamChunk, task_id: str, session_id: str,
        message_id: str, sequence: int,
    ) -> Optional[StreamEvent]:
        """Returns None for ProviderChunkType.DONE -- that's a signal
        to stop iterating, not a StreamEvent of its own. Completed is
        emitted by the caller once the stream genuinely finishes.

        Also None for TOOL_CALL (B10): the agent loop reads
        `chunk.tool_call` off the raw chunk, and the tool's own
        started/completed pair is emitted by ToolExecutor. See
        _CHUNK_TYPE_MAP above."""
        if chunk.type in (ProviderChunkType.DONE, ProviderChunkType.TOOL_CALL):
            return None
        data: dict = {}
        if chunk.text is not None:
            data["text"] = chunk.text
        if chunk.usage is not None:
            data["usage"] = chunk.usage.to_dict()
        if chunk.tool_call is not None:
            data["tool_call"] = {
                "id": chunk.tool_call.id,
                "name": chunk.tool_call.name,
                "arguments": chunk.tool_call.arguments,
            }
        return StreamEvent(
            type=_CHUNK_TYPE_MAP[chunk.type], task_id=task_id, session_id=session_id,
            message_id=message_id, sequence=sequence, data=data,
        )