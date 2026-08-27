"""MessageTranslator: the seam where our internal Message format meets
each provider's wire format.

This is intentionally just an interface right now. Episode 5
(Providers) supplies one concrete implementation per provider family
(ClaudeMessageTranslator, OpenAIMessageTranslator, GeminiMessageTranslator,
OllamaMessageTranslator, OpenRouterMessageTranslator). Defining the
boundary now means Episode 5 plugs in without touching Message,
MessageHistory, or SessionManager at all -- and it's also what keeps
our internal Message shape from ever being designed around any one
vendor's schema.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any

from managers.message import Message


class MessageTranslator(ABC):
    """One subclass per provider, implemented in Episode 5."""

    @abstractmethod
    def to_provider_format(self, message: Message) -> dict[str, Any]:
        """Internal Message -> this provider's wire format."""
        raise NotImplementedError

    @abstractmethod
    def from_provider_format(self, raw: dict[str, Any], session_id: str) -> Message:
        """This provider's wire format -> internal Message."""
        raise NotImplementedError

    # -- tools (B10) ------------------------------------------------------
    #
    # Deliberately NOT abstract. A translator that has not been taught a
    # vendor's tool shape returns None, which reads downstream as "this
    # provider takes no tools" -- the same answer as BaseProvider's
    # supports_tools = False, arrived at from the other side. Making it
    # abstract would force every existing translator to grow a stub that
    # lies.

    def tools_to_provider_format(self, tools: list[dict[str, Any]]) -> Any:
        """Common tool schema -> this provider's own tool format.

        The common schema is exactly what `tools.models.Tool.to_schema()`
        produces: {"name", "description", "parameters"} with `parameters`
        a JSON Schema object. Returns None when this provider takes no
        tools."""
        return None