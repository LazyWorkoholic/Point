"""OpenAIProvider: GPT models via the official `openai` SDK.
Requires OPENAI_API_KEY in the environment.
"""

from __future__ import annotations

from typing import AsyncIterator

import openai

from managers.message import TokenUsage
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
    Effort,
    GenerationRequest,
    GenerationResponse,
    Model,
    ModelCapabilities,
    ProviderChunkType,
    ProviderHealth,
    ProviderHealthStatus,
    ProviderStreamChunk,
)
from providers.openai.translator import (
    OpenAIMessageTranslator,
    OpenAIToolCallAccumulator,
)


def _retry_after(exc) -> float | None:
    """Retry-After off an openai SDK error, for providers.retry's
    backoff. Shared with OpenRouter, which raises the same SDK's
    exceptions."""
    try:
        raw = exc.response.headers.get("retry-after")
        return float(raw) if raw is not None else None
    except (AttributeError, TypeError, ValueError):
        return None


def reasoning_effort_kwargs(request: GenerationRequest) -> dict:
    """OpenAI-shaped effort translation, shared with OpenRouter (whose
    endpoint is OpenAI-compatible).

    Omitted entirely when there is no effort, and when effort is `off`:
    `reasoning_effort` on a model that does not reason is a 400, and B8
    is what will decide -- from ModelCapabilities.supports_effort --
    whether a given model should be sent one at all. Until then this path
    is unreachable, because nothing sets GenerationRequest.effort.
    """
    if request.effort is None or request.effort == Effort.OFF:
        return {}
    return {"reasoning_effort": request.effort.value}


def tool_kwargs(request: GenerationRequest, translator) -> dict:
    """Shared with OpenRouter. Omitted entirely when the request carries
    no tools -- sending `tools: []` is a 400 on some deployments and a
    silent behaviour change on others."""
    if not request.tools:
        return {}
    return {"tools": translator.tools_to_provider_format(request.tools)}


class OpenAIProvider(BaseProvider):
    name = "openai"
    supports_tools = True

    def __init__(self, config) -> None:
        super().__init__(config)
        self._client = openai.AsyncOpenAI(
            api_key=config.api_key, base_url=config.base_url, timeout=config.timeout
        )
        self._translator = OpenAIMessageTranslator()

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        try:
            response = await self._client.chat.completions.create(
                model=request.model,
                messages=[self._translator.to_provider_format(m) for m in request.messages],
                temperature=request.temperature,
                max_tokens=request.max_tokens,
                **reasoning_effort_kwargs(request),
                **tool_kwargs(request, self._translator),
            )
        except openai.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        except openai.RateLimitError as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except openai.NotFoundError as exc:
            raise ModelNotFoundError(str(exc)) from exc
        except openai.APIConnectionError as exc:
            raise ProviderConnectionError(str(exc)) from exc
        except openai.APITimeoutError as exc:
            raise ProviderTimeoutError(str(exc)) from exc
        except openai.BadRequestError as exc:
            raise InvalidRequestError(str(exc)) from exc

        choice = response.choices[0]
        text = choice.message.content or ""
        usage = TokenUsage(
            input_tokens=response.usage.prompt_tokens if response.usage else 0,
            output_tokens=response.usage.completion_tokens if response.usage else 0,
        )
        return GenerationResponse(text=text, usage=usage, model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        try:
            stream = await self._client.chat.completions.create(
                model=request.model,
                messages=[self._translator.to_provider_format(m) for m in request.messages],
                temperature=request.temperature,
                max_tokens=request.max_tokens,
                stream=True,
                stream_options={"include_usage": True},
                **reasoning_effort_kwargs(request),
                **tool_kwargs(request, self._translator),
            )
            tool_calls = OpenAIToolCallAccumulator()
            async with stream:
                async for chunk in stream:
                    delta = chunk.choices[0].delta if chunk.choices else None
                    if delta is not None:
                        if delta.content:
                            yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=delta.content)
                        tool_calls.feed(delta)
                    if chunk.usage:
                        yield ProviderStreamChunk(
                            type=ProviderChunkType.USAGE,
                            usage=TokenUsage(
                                input_tokens=chunk.usage.prompt_tokens,
                                output_tokens=chunk.usage.completion_tokens,
                            ),
                        )
            # Drained after the stream closes, not on finish_reason:
            # usage arrives in a chunk *after* the one carrying
            # finish_reason, and emitting early would cut the usage
            # chunk off from the turn it belongs to.
            for call in tool_calls.drain():
                yield ProviderStreamChunk(type=ProviderChunkType.TOOL_CALL, tool_call=call)
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        except openai.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        except openai.RateLimitError as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except openai.APIConnectionError as exc:
            raise ProviderConnectionError(str(exc)) from exc

    async def list_models(self) -> list[Model]:
        try:
            response = await self._client.models.list()
        except openai.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        # supports_effort is per-model and models.list() does not report
        # it. Guessing from an id prefix is how you end up greying out
        # the wrong control, so it stays False until something can answer
        # truthfully.
        return [
            Model(
                id=m.id, provider=self.name, display_name=m.id, context_window=0,
                capabilities=ModelCapabilities(supports_streaming=True, supports_tools=True),
            )
            for m in response.data
        ]

    async def health(self) -> ProviderHealth:
        if not self.config.api_key:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED, detail="No OPENAI_API_KEY set")
        try:
            await self._client.models.list()
            return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
        except openai.AuthenticationError:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED)
        except openai.APIConnectionError:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE)
        except Exception as exc:  # noqa: BLE001
            return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=str(exc))