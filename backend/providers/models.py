"""Provider-layer domain models.

Distinct from managers/message.py's Message -- these are what carries
a request TO a provider and what comes back, not conversation records.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

import appconfig
from managers.message import AssistantMessage, Message, TokenUsage


class Effort(str, Enum):
    """Normalized reasoning effort (project_idea.md §10). Each adapter
    translates it or ignores it; `ModelCapabilities.supports_effort` is
    how the UI knows to grey out what a provider cannot do.

    Nothing sets this yet -- B8 owns the resolution order (task override
    > session override > agent default > global config). B6 only defines
    the vocabulary and carries it to the provider boundary."""

    OFF = "off"
    LOW = "low"
    MEDIUM = "medium"
    HIGH = "high"


class ProviderHealthStatus(str, Enum):
    HEALTHY = "healthy"
    UNHEALTHY = "unhealthy"
    UNAUTHENTICATED = "unauthenticated"
    UNAVAILABLE = "unavailable"


@dataclass
class ProviderHealth:
    status: ProviderHealthStatus
    detail: Optional[str] = None


@dataclass
class ModelCapabilities:
    supports_streaming: bool = True
    supports_tools: bool = False
    supports_vision: bool = False
    supports_reasoning: bool = False
    supports_effort: bool = False    # can this model take an Effort at all?
    supports_caching: bool = False   # prompt caching, for whoever prices it later


@dataclass
class Model:
    id: str
    provider: str
    display_name: str
    context_window: int
    capabilities: ModelCapabilities = field(default_factory=ModelCapabilities)
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass
class ProviderConfig:
    """Never stash a raw API key on a Provider as loose application
    state elsewhere -- it lives here, passed once at construction.
    As of B6 the key comes from appconfig's resolution order --
    env var -> credentials.toml -> not found -- so a key stored in
    credentials.toml under %APPDATA% works exactly as an env var does."""

    provider: str
    api_key: Optional[str] = None
    base_url: Optional[str] = None
    timeout: float = 60.0
    extra: dict[str, Any] = field(default_factory=dict)

    @staticmethod
    def from_env(provider: str) -> "ProviderConfig":
        import os
        api_key = appconfig.resolve_api_key(provider)
        base_url = os.environ.get(f"{provider.upper()}_BASE_URL")
        if provider == "ollama" and not base_url:
            # 127.0.0.1, not "localhost" -- F10's verification found
            # ListProviders hanging for minutes on a fresh profile's FIRST
            # launch (every later launch of the same profile resolved
            # instantly), the exact signature of Windows resolving
            # "localhost" to both ::1 and 127.0.0.1 and trying the IPv6
            # route first: Ollama listens on IPv4 only, so that attempt has
            # to exhaust its own timeout before falling back -- and
            # whatever cache makes the SECOND launch instant (the OS
            # resolver, a route cache) is not warm yet on the very first
            # one. A literal IP needs no resolution and no such race.
            base_url = os.environ.get("OLLAMA_BASE_URL", "http://127.0.0.1:11434")
        return ProviderConfig(provider=provider, api_key=api_key, base_url=base_url)


@dataclass
class GenerationRequest:
    """Provider-independent request. Deliberately takes a plain list
    of Messages, never a Session -- see providers/base.py docstring."""

    model: str
    messages: list[Message]
    temperature: Optional[float] = None
    max_tokens: Optional[int] = None
    tools: Optional[list[dict[str, Any]]] = None
    effort: Optional[Effort] = None
    extra: dict[str, Any] = field(default_factory=dict)


@dataclass
class GenerationResponse:
    """Provider output -- deliberately NOT a full Message yet. A
    Message needs a session_id, and the provider layer doesn't know
    what a session is. Call `.to_assistant_message(session_id,
    task_id)` once the caller (Runtime, later ChatAgent) is ready to
    persist it."""

    text: str
    usage: TokenUsage
    model: str
    metadata: dict[str, Any] = field(default_factory=dict)

    def to_assistant_message(self, session_id: str, task_id: Optional[str] = None) -> Message:
        return AssistantMessage(session_id, self.text, task_id=task_id, usage=self.usage)


class ProviderChunkType(str, Enum):
    TEXT = "text"
    THINKING = "thinking"
    USAGE = "usage"
    TOOL_CALL = "tool_call"
    DONE = "done"


@dataclass(frozen=True)
class ToolCall:
    """One tool invocation a model asked for, normalized (B10).

    `id` is the provider's own correlation handle for the call --
    OpenAI's `tool_call.id`, Anthropic's `tool_use` block id. Gemini and
    Ollama do not issue one, so the adapter synthesizes it; the loop only
    needs it to pair a result back to a request, and it never leaves the
    process. `arguments` is already-parsed JSON, never the raw string:
    every adapter parses in its own layer so nothing downstream has to
    know which vendors stream arguments as fragments.
    """

    id: str
    name: str
    arguments: dict[str, Any] = field(default_factory=dict)


@dataclass
class ProviderStreamChunk:
    """Raw, minimally-normalized output from a provider's stream()
    call. This is NOT Episode 5's StreamEvent -- it's the seam Episode
    5's Stream Adapter will convert FROM. Providers translate their
    own wire format (OpenAI chunks, Anthropic events, Ollama JSON
    lines) into this small shared shape; Episode 5 turns THIS into
    TextDelta/ThinkingDelta/UsageUpdated StreamEvents with
    task_id/session_id/message_id/sequence attached.
    """

    type: ProviderChunkType
    text: Optional[str] = None
    usage: Optional[TokenUsage] = None
    # Set only on TOOL_CALL chunks (B10). A provider emits one of these
    # once it has the *whole* call -- fragmented argument deltas are
    # reassembled inside the adapter, because a half-parsed argument dict
    # is not something the tool loop can validate against a schema.
    tool_call: Optional[ToolCall] = None