"""OpenAIMessageTranslator: internal Message <-> OpenAI's chat format.
Shared by OpenAIProvider and OpenRouterProvider since OpenRouter's API
is OpenAI-compatible.
"""

from __future__ import annotations

from typing import Any

from managers.message import AssistantMessage, Message, MessageRole
from managers.message_translator import MessageTranslator


class OpenAIMessageTranslator(MessageTranslator):
    _ROLE_MAP = {
        MessageRole.SYSTEM: "system",
        MessageRole.USER: "user",
        MessageRole.ASSISTANT: "assistant",
        MessageRole.TOOL: "tool",
        MessageRole.ERROR: "assistant",
    }

    def to_provider_format(self, message: Message) -> dict[str, Any]:
        return {"role": self._ROLE_MAP.get(message.role, "user"), "content": message.text}

    def from_provider_format(self, raw: dict[str, Any], session_id: str) -> Message:
        return AssistantMessage(session_id, raw.get("content", ""))