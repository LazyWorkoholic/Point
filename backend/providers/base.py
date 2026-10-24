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

    # Provider-level tool advertisement (B10). Distinct from
    # `ModelCapabilities.supports_tools`, which is per *model*: this one
    # answers "can this adapter translate a tool schema and normalize a
    # tool call back at all?", and it is what an agent requiring tools is
    # checked against at INVOCATION, before a single token is streamed.
    # Defaulting to False means a new adapter is tool-less until someone
    # writes the translation, rather than silently dropping the tools
    # mid-stream.
    supports_tools: bool = False

    def __init__(self, config: ProviderConfig) -> None:
        self.config = config


    async def close(self) -> int:
        """Release whatever this provider is holding open, and report how
        many things were dropped (B15).

        **A no-op by default, and that is the honest default.** Three of
        the five adapters have nothing to close: `ollama` and
        `openrouter` build a fresh `httpx.AsyncClient` per call inside
        `async with`, so the pool is gone before the call returns, and
        `gemini` configures a module-level key and builds a
        `GenerativeModel` per call. Only `anthropic` and `openai` hold a
        long-lived SDK client, and only they override this.

        The contract that makes closing safe: a provider must stay
        usable afterwards. An adapter that closes a pooled client
        re-creates it on the next call, because `ProviderRegistry` keeps
        handing out the same instance and a closed-and-not-reopened one
        would turn an unload into a broken next send.

        Returns a count, not a bool, so `ReleaseReport` can say how many
        pools were actually dropped -- and says nothing about bytes of
        memory, which CPython does not return to the OS reliably anyway.
        """
        return 0

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