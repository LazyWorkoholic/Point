"""OpenRouterProvider: many models via OpenRouter's OpenAI-compatible
API. Reuses the OpenAI SDK/translator since OpenRouter's endpoint is
OpenAI-compatible. model_id here must be OpenRouter's full model
string, e.g. "anthropic/claude-sonnet-5" or "google/gemini-2.0-flash"
-- this is the concrete case of "Provider != Model" from project_idea.md.

Requires OPENROUTER_API_KEY in the environment.
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
    GenerationRequest,
    GenerationResponse,
    Model,
    ModelCapabilities,
    ProviderChunkType,
    ProviderHealth,
    ProviderHealthStatus,
    ProviderStreamChunk,
)
from providers.openai.provider import reasoning_effort_kwargs
from providers.openai.translator import OpenAIMessageTranslator

_OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1"


class OpenRouterProvider(BaseProvider):
    name = "openrouter"

    def __init__(self, config) -> None:
        super().__init__(config)
        self._client = openai.AsyncOpenAI(
            api_key=config.api_key,
            base_url=config.base_url or _OPENROUTER_BASE_URL,
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
            raise RateLimitError(str(exc)) from exc
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
            )
            async with stream:
                async for chunk in stream:
                    if chunk.choices and chunk.choices[0].delta and chunk.choices[0].delta.content:
                        yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=chunk.choices[0].delta.content)
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        except openai.AuthenticationError as exc:
            raise AuthenticationError(str(exc)) from exc
        except openai.RateLimitError as exc:
            raise RateLimitError(str(exc)) from exc
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
        if not self.config.api_key:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED, detail="No OPENROUTER_API_KEY set")
        try:
            await self._client.models.list()
            return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
        except openai.AuthenticationError:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED)
        except openai.APIConnectionError:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE)
        except Exception as exc:  # noqa: BLE001
            return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=str(exc))