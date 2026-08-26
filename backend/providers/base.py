"""BaseProvider: the one interface the rest of the application talks
to, regardless of which LLM vendor sits behind it.

Providers know about Messages and Models. They do NOT know about
Sessions, MessageHistory, Memory, Agents, Workspaces, or Projects --
that boundary is deliberate. Something upstream converts a Session's
MessageHistory into a GenerationRequest before calling a provider.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import AsyncIterator

from providers.models import (
    GenerationRequest,
    GenerationResponse,
    Model,
    ProviderConfig,
    ProviderHealth,
    ProviderStreamChunk,
)


class BaseProvider(ABC):
    name: str = "base"

    # False keeps a provider out of anything a user picks from. Only
    # MockProvider sets it: it is a test fixture wearing a provider's
    # interface, and it must never be selectable from the UI.
    selectable: bool = True

    def __init__(self, config: ProviderConfig) -> None:
        self.config = config

    @abstractmethod
    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        """Non-streaming call: full request in, full response out."""
        raise NotImplementedError

    @abstractmethod
    def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        """Streaming call: yields raw ProviderStreamChunk as the model
        generates. This method establishes the contract only --
        Episode 5's Stream Adapter is what converts these chunks into
        unified StreamEvents; providers are still responsible for
        actually talking to their API correctly."""
        raise NotImplementedError

    @abstractmethod
    async def list_models(self) -> list[Model]:
        raise NotImplementedError

    @abstractmethod
    async def health(self) -> ProviderHealth:
        raise NotImplementedError