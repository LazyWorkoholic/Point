"""AnthropicMessageTranslator: internal Message <-> Anthropic's format.

Known simplification: Anthropic passes system prompts as a separate
top-level `system` parameter, not a message with role="system". This
translator maps SYSTEM messages to role="user" as a placeholder --
proper system-prompt extraction belongs to whoever builds the full
GenerationRequest (likely Episode 8's Agent layer), which should pull
SystemMessages out and pass them via GenerationRequest.extra["system"]
instead of including them in `messages` at all.
"""

from __future__ import annotations

from typing import Any

from managers.message import AssistantMessage, Message, MessageRole
from managers.message_translator import MessageTranslator


class AnthropicMessageTranslator(MessageTranslator):
    def to_provider_format(self, message: Message) -> dict[str, Any]:
        role = "assistant" if message.role == MessageRole.ASSISTANT else "user"
        return {"role": role, "content": message.text}

    def from_provider_format(self, raw: dict[str, Any], session_id: str) -> Message:
        return AssistantMessage(session_id, raw.get("content", ""))

    def tools_to_provider_format(self, tools: list[dict[str, Any]]) -> Any:
        """Anthropic takes tools flat, and calls the JSON Schema
        `input_schema` rather than `parameters` (B10)."""
        return [
            {
                "name": t["name"],
                "description": t["description"],
                "input_schema": t["parameters"],
            }
            for t in tools
        ]