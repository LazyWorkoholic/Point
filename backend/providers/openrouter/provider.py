"""OpenRouterProvider: many models via OpenRouter's OpenAI-compatible
API. Reuses the OpenAI SDK/translator since OpenRouter's endpoint is
OpenAI-compatible. model_id here must be OpenRouter's full model
string, e.g. "anthropic/claude-sonnet-5" or "google/gemini-2.0-flash"
-- this is the concrete case of "Provider != Model" from project_idea.md.

Requires OPENROUTER_API_KEY in the environment.
"""

from __future__ import annotations

from typing import AsyncIterator

import httpx
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
    GenerationRequest,
    GenerationResponse,
    Model,
    ModelCapabilities,
    ProviderChunkType,
    ProviderHealth,
    ProviderHealthStatus,
    ProviderStreamChunk,
)
from providers.openai.provider import _retry_after, reasoning_effort_kwargs, tool_kwargs
from providers.openai.translator import (
    OpenAIMessageTranslator,
    OpenAIToolCallAccumulator,
)

_OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1"


class OpenRouterProvider(BaseProvider):
    name = "openrouter"
    supports_tools = True

    def __init__(self, config) -> None:
        super().__init__(config)
        self._base_url = (config.base_url or _OPENROUTER_BASE_URL).rstrip("/")
        self._client = openai.AsyncOpenAI(
            api_key=config.api_key,
            base_url=self._base_url,
            timeout=config.timeout,
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
        return [
            Model(
                id=m.id, provider=self.name, display_name=m.id, context_window=0,
                capabilities=ModelCapabilities(supports_streaming=True),
            )
            for m in response.data
        ]

    async def health(self) -> ProviderHealth:
        """`models.list()` (what list_models() uses, and what this used to
        check with too) hits OpenRouter's `/models` endpoint, which is
        PUBLIC -- it returns the full catalog for any key, garbage or
        real, with no auth check at all (confirmed live: a garbage key
        got back all 465 models). That made this the same shape of bug
        B25 fixed for Anthropic's list_models(), just surfacing through a
        real HTTP call instead of a hardcoded list. `/auth/key` is
        OpenRouter's documented endpoint for checking a key itself --
        it 401s on an invalid key -- so it is the one real network call
        here that actually answers "does this key work."
        """
        if not self.config.api_key:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED, detail="No OPENROUTER_API_KEY set")
        try:
            async with httpx.AsyncClient(timeout=self.config.timeout) as client:
                resp = await client.get(
                    f"{self._base_url}/auth/key",
                    headers={"Authorization": f"Bearer {self.config.api_key}"},
                )
        except httpx.ConnectError as exc:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE, detail=str(exc))
        if resp.status_code == 401:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED)
        if resp.status_code == 200:
            return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
        return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=f"status {resp.status_code}")