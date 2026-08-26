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
}


class StreamAdapter:
    def adapt(
        self, chunk: ProviderStreamChunk, task_id: str, session_id: str,
        message_id: str, sequence: int,
    ) -> Optional[StreamEvent]:
        """Returns None for ProviderChunkType.DONE -- that's a signal
        to stop iterating, not a StreamEvent of its own. Completed is
        emitted by the caller once the stream genuinely finishes."""
        if chunk.type == ProviderChunkType.DONE:
            return None
        data: dict = {}
        if chunk.text is not None:
            data["text"] = chunk.text
        if chunk.usage is not None:
            data["usage"] = chunk.usage.to_dict()
        return StreamEvent(
            type=_CHUNK_TYPE_MAP[chunk.type], task_id=task_id, session_id=session_id,
            message_id=message_id, sequence=sequence, data=data,
        )