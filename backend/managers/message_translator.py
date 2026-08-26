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