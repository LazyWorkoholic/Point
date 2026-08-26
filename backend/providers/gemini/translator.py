"""GeminiMessageTranslator: internal Message <-> Gemini's `contents`
format. Gemini uses "model" instead of "assistant" as the role name.
"""

from __future__ import annotations

from typing import Any

from managers.message import AssistantMessage, Message, MessageRole
from managers.message_translator import MessageTranslator


class GeminiMessageTranslator(MessageTranslator):
    def to_provider_format(self, message: Message) -> dict[str, Any]:
        role = "model" if message.role == MessageRole.ASSISTANT else "user"
        return {"role": role, "parts": [message.text]}

    def from_provider_format(self, raw: dict[str, Any], session_id: str) -> Message:
        text = "".join(raw.get("parts", []))
        return AssistantMessage(session_id, text)