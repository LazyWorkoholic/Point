"""OllamaMessageTranslator: internal Message <-> Ollama's chat format.

Ollama's /api/chat expects {"role": ..., "content": ...} per message,
almost identical to our internal shape -- MessageRole.TOOL/ERROR just
don't have a direct Ollama equivalent, so they map down to "assistant".
"""

from __future__ import annotations

from typing import Any

from managers.message import AssistantMessage, Message, MessageRole
from managers.message_translator import MessageTranslator


class OllamaMessageTranslator(MessageTranslator):
    def to_provider_format(self, message: Message) -> dict[str, Any]:
        role = message.role.value
        if message.role in (MessageRole.TOOL, MessageRole.ERROR):
            role = "assistant"
        return {"role": role, "content": message.text}

    def from_provider_format(self, raw: dict[str, Any], session_id: str) -> Message:
        return AssistantMessage(session_id, raw.get("content", ""))