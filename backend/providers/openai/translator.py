"""OpenAIMessageTranslator: internal Message <-> OpenAI's chat format.
Shared by OpenAIProvider and OpenRouterProvider since OpenRouter's API
is OpenAI-compatible.
"""

from __future__ import annotations

from typing import Any

import json
import uuid

from managers.message import AssistantMessage, Message, MessageRole
from managers.message_translator import MessageTranslator
from providers.models import ToolCall


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

    def tools_to_provider_format(self, tools: list[dict[str, Any]]) -> Any:
        """OpenAI wraps each tool in a `function` envelope and takes the
        JSON Schema verbatim as `parameters` (B10)."""
        return [
            {
                "type": "function",
                "function": {
                    "name": t["name"],
                    "description": t["description"],
                    "parameters": t["parameters"],
                },
            }
            for t in tools
        ]

class OpenAIToolCallAccumulator:
    """Reassembles OpenAI's fragmented tool-call deltas into whole
    ToolCalls (B10). Shared with OpenRouter, whose endpoint streams the
    identical shape.

    OpenAI streams a tool call across many chunks: the first carries the
    id and function name, every later one appends a slice of the
    arguments JSON string. Nothing can be validated against a schema
    until the last slice arrives, so this buffers by `index` and only
    emits on `drain()`, which the provider calls once the stream ends.

    A call whose arguments never parse as JSON is emitted with empty
    arguments rather than dropped: the tool executor's schema check is
    the one place a bad call should be turned into a
    ToolResult(ok=False) the model can read, and swallowing it here would
    hide the call from the model entirely.
    """

    def __init__(self) -> None:
        self._pending: dict[int, dict[str, str]] = {}

    def feed(self, delta) -> None:
        for fragment in getattr(delta, "tool_calls", None) or []:
            slot = self._pending.setdefault(
                getattr(fragment, "index", 0), {"id": "", "name": "", "arguments": ""}
            )
            if getattr(fragment, "id", None):
                slot["id"] = fragment.id
            function = getattr(fragment, "function", None)
            if function is not None:
                if getattr(function, "name", None):
                    slot["name"] += function.name
                if getattr(function, "arguments", None):
                    slot["arguments"] += function.arguments

    def drain(self) -> list[ToolCall]:
        calls: list[ToolCall] = []
        for _, slot in sorted(self._pending.items()):
            if not slot["name"]:
                continue
            try:
                arguments = json.loads(slot["arguments"]) if slot["arguments"] else {}
            except json.JSONDecodeError:
                arguments = {}
            if not isinstance(arguments, dict):
                arguments = {}
            calls.append(
                ToolCall(
                    id=slot["id"] or f"call-{uuid.uuid4().hex[:12]}",
                    name=slot["name"],
                    arguments=arguments,
                )
            )
        self._pending.clear()
        return calls
