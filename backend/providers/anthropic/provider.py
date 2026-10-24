"""AnthropicProvider: Claude via the official `anthropic` SDK.

Requires ANTHROPIC_API_KEY in the environment. If missing,
providers/bootstrap.py simply never registers this provider.
"""

from __future__ import annotations

from typing import AsyncIterator, Optional

import anthropic

from managers.message import TokenUsage
from providers.anthropic.translator import AnthropicMessageTranslator
from providers.base import BaseProvider
from providers.errors import (
    AuthenticationError,
    InvalidRequestError,
    ModelNotFoundError,
    ProviderConnectionError,
    ProviderTimeoutError,
    RateLimitError,
)
from providers.models import (
    GenerationRequest,
    GenerationResponse,
    Model,
    ModelCapabilities,
    ProviderChunkType,
    ProviderHealth,
    ProviderHealthStatus,
    ProviderStreamChunk,
    ToolCall,
)


def _retry_after(exc) -> float | None:
    try:
        raw = exc.response.headers.get("retry-after")
        return float(raw) if raw is not None else None
    except (AttributeError, TypeError, ValueError):
        return None


def _tool_kwargs(request: GenerationRequest, translator) -> dict:
    if not request.tools:
        return {}
    return {"tools": translator.tools_to_provider_format(request.tools)}


class AnthropicProvider(BaseProvider):
    name = "anthropic"
    supports_tools = True

    def __init__(self, config) -> None:
        super().__init__(config)
        # Built on first use rather than in __init__, so close() (B15)
        # can drop the pool and the next call can rebuild it. Every call
        # site still reads `self._client` and is unchanged -- the
        # property below is the whole of the difference.
        self._pool: Optional[anthropic.AsyncAnthropic] = None
        self._translator = AnthropicMessageTranslator()

    @property
    def _client(self) -> "anthropic.AsyncAnthropic":
        if self._pool is None:
            self._pool = anthropic.AsyncAnthropic(
                api_key=self.config.api_key, timeout=self.config.timeout
            )
        return self._pool

    async def close(self) -> int:
        """Close the SDK client and its underlying httpx connection
        pool (B15). Re-created on the next call by `_client` above --
        `ProviderRegistry` keeps handing out this same instance, so an
        adapter that closed and stayed closed would turn a workspace
        unload into a broken next send."""
        if self._pool is None:
            return 0
        pool, self._pool = self._pool, None
        await pool.close()
        return 1

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        try:
            response = await self._client.messages.create(
                model=request.model,
                max_tokens=request.max_tokens or 4096,
                messages=[self._translator.to_provider_format(m) for m in request.messages],
                **_tool_kwargs(request, self._translator),
            )
        except anthropic.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        except anthropic.RateLimitError as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except anthropic.NotFoundError as exc:
            raise ModelNotFoundError(str(exc)) from exc
        except anthropic.APIConnectionError as exc:
            raise ProviderConnectionError(str(exc)) from exc
        except anthropic.APITimeoutError as exc:
            raise ProviderTimeoutError(str(exc)) from exc
        except anthropic.BadRequestError as exc:
            raise InvalidRequestError(str(exc)) from exc

        text = "".join(block.text for block in response.content if block.type == "text")
        usage = TokenUsage(
            input_tokens=response.usage.input_tokens,
            output_tokens=response.usage.output_tokens,
        )
        return GenerationResponse(text=text, usage=usage, model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        """Effort (B6 §8) is accepted and ignored. Anthropic expresses it
        as a thinking budget attached to a thinking block, which is the
        reasoning work, not this episode's; a guessed budget would change
        what the model costs and returns. supports_effort stays False
        until the adapter really translates it."""
        try:
            async with self._client.messages.stream(
                model=request.model,
                max_tokens=request.max_tokens or 4096,
                messages=[self._translator.to_provider_format(m) for m in request.messages],
                **_tool_kwargs(request, self._translator),
            ) as stream:
                async for event in stream:
                    if event.type == "content_block_delta" and event.delta.type == "text_delta":
                        yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=event.delta.text)
                    elif event.type == "message_stop":
                        final = await stream.get_final_message()
                        usage = TokenUsage(
                            input_tokens=final.usage.input_tokens,
                            output_tokens=final.usage.output_tokens,
                        )
                        yield ProviderStreamChunk(type=ProviderChunkType.USAGE, usage=usage)
                        # Read off the final message rather than
                        # reassembled from input_json_delta fragments:
                        # the SDK has already done that accumulation, and
                        # doing it twice is two chances to get it wrong.
                        for block in final.content:
                            if getattr(block, "type", None) != "tool_use":
                                continue
                            yield ProviderStreamChunk(
                                type=ProviderChunkType.TOOL_CALL,
                                tool_call=ToolCall(
                                    id=block.id,
                                    name=block.name,
                                    arguments=dict(block.input or {}),
                                ),
                            )
                        yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        except anthropic.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        except anthropic.RateLimitError as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except anthropic.APIConnectionError as exc:
            raise ProviderConnectionError(str(exc)) from exc

    async def list_models(self) -> list[Model]:
        # No public models.list() in this SDK version -- return the
        # known current model family. Revisit if the SDK adds one.
        known = [
            ("claude-sonnet-5", "Claude Sonnet 5", 200_000),
            ("claude-opus-4-8", "Claude Opus 4.8", 200_000),
            ("claude-haiku-4-5-20251001", "Claude Haiku 4.5", 200_000),
        ]
        return [
            Model(
                id=model_id, provider=self.name, display_name=display_name,
                context_window=ctx,
                capabilities=ModelCapabilities(
                    supports_streaming=True, supports_tools=True,
                    supports_vision=True, supports_reasoning=True,
                ),
            )
            for model_id, display_name, ctx in known
        ]

    async def health(self) -> ProviderHealth:
        if not self.config.api_key:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED, detail="No ANTHROPIC_API_KEY set")
        try:
            await self._client.messages.create(
                model="claude-haiku-4-5-20251001", max_tokens=1,
                messages=[{"role": "user", "content": "ping"}],
            )
            return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
        except anthropic.AuthenticationError:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED)
        except anthropic.APIConnectionError:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE)
        except Exception as exc:  # noqa: BLE001
            return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=str(exc))