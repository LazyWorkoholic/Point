"""GeminiProvider: Google's Gemini models via `google-generativeai`.
Requires GEMINI_API_KEY in the environment.

Known limitation: genai.configure() is process-global, not per-client
-- fine for this project today, worth revisiting only if another
Google-SDK consumer with a different key ever runs in the same process.
"""

from __future__ import annotations

import uuid
from typing import AsyncIterator

import google.generativeai as genai
from google.api_core import exceptions as google_exceptions

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
from providers.gemini.translator import GeminiMessageTranslator
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


async def _close_stream(response) -> None:
    """Close the async iterator behind a streaming response, if it is
    still open. Tolerates every shape the SDK might hand back -- this
    runs during cancellation, where raising would replace a clean stop
    with a second failure."""
    if response is None:
        return
    iterator = getattr(response, "_iterator", None)
    aclose = getattr(iterator, "aclose", None)
    if aclose is None:
        return
    try:
        await aclose()
    except Exception:  # noqa: BLE001
        pass


def _retry_after(exc) -> float | None:
    """google-api-core surfaces a retry delay on the RPC's error details
    rather than an HTTP header; when it is absent providers.retry falls
    back to the computed backoff."""
    delay = getattr(getattr(exc, "retry", None), "_deadline", None)
    try:
        return float(delay) if delay is not None else None
    except (TypeError, ValueError):
        return None


def _tool_calls(chunk) -> list[ToolCall]:
    """Gemini puts a call in a `function_call` part on the candidate's
    content, with args as a proto Map rather than a dict, and issues no
    call id -- so one is synthesized (B10). Wrapped in getattr chains
    because a chunk carrying only text has none of these attributes."""
    calls = []
    for candidate in getattr(chunk, "candidates", None) or []:
        content = getattr(candidate, "content", None)
        for part in getattr(content, "parts", None) or []:
            function_call = getattr(part, "function_call", None)
            if not function_call or not getattr(function_call, "name", None):
                continue
            calls.append(
                ToolCall(
                    id=f"call-{uuid.uuid4().hex[:12]}",
                    name=function_call.name,
                    arguments=dict(getattr(function_call, "args", None) or {}),
                )
            )
    return calls


class GeminiProvider(BaseProvider):
    name = "gemini"
    supports_tools = True

    def __init__(self, config) -> None:
        super().__init__(config)
        genai.configure(api_key=config.api_key)
        self._translator = GeminiMessageTranslator()

    def _to_contents(self, request: GenerationRequest) -> list[dict]:
        return [self._translator.to_provider_format(m) for m in request.messages]

    def _tool_kwargs(self, request: GenerationRequest) -> dict:
        if not request.tools:
            return {}
        return {"tools": self._translator.tools_to_provider_format(request.tools)}

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        model = genai.GenerativeModel(request.model)
        try:
            response = await model.generate_content_async(
                self._to_contents(request), **self._tool_kwargs(request)
            )
        except google_exceptions.Unauthenticated as exc:
            raise AuthenticationError(str(exc)) from exc
        except google_exceptions.PermissionDenied as exc:
            raise AuthenticationError(str(exc)) from exc
        except google_exceptions.ResourceExhausted as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except google_exceptions.NotFound as exc:
            raise ModelNotFoundError(str(exc)) from exc
        except google_exceptions.DeadlineExceeded as exc:
            raise ProviderTimeoutError(str(exc)) from exc
        except google_exceptions.ServiceUnavailable as exc:
            raise ProviderConnectionError(str(exc)) from exc
        except google_exceptions.InvalidArgument as exc:
            raise InvalidRequestError(str(exc)) from exc

        usage_meta = getattr(response, "usage_metadata", None)
        usage = TokenUsage(
            input_tokens=getattr(usage_meta, "prompt_token_count", 0) or 0,
            output_tokens=getattr(usage_meta, "candidates_token_count", 0) or 0,
        )
        return GenerationResponse(text=response.text, usage=usage, model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        """Effort (B6 §8) is accepted and ignored here: the Gemini SDK
        expresses thinking budget per model in a shape this adapter does
        not yet build, and translating it wrong is worse than not
        translating it. ModelCapabilities.supports_effort stays False, so
        nothing offers a control that does nothing.

        Teardown: B05 gave OpenAI and OpenRouter `async with stream:` and
        noted Ollama and Anthropic already close through their own client
        context managers. Gemini was never mentioned and never fixed --
        it iterated the streaming response bare, so cancelling a task
        left the underlying HTTP stream open. The SDK's
        AsyncGenerateContentResponse has no close()/aclose() of its own,
        so the guarantee has to come from closing the async iterator it
        wraps, in a finally that runs on cancellation like everyone
        else's __aexit__ does.
        """
        model = genai.GenerativeModel(request.model)
        response = None
        try:
            response = await model.generate_content_async(
                self._to_contents(request), stream=True, **self._tool_kwargs(request)
            )
            async for chunk in response:
                calls = _tool_calls(chunk)
                # `chunk.text` raises rather than returning "" when the
                # candidate holds a function_call and no text part, so the
                # tool-call check has to come first and short-circuit it.
                if calls:
                    for call in calls:
                        yield ProviderStreamChunk(
                            type=ProviderChunkType.TOOL_CALL, tool_call=call
                        )
                elif chunk.text:
                    yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=chunk.text)
            final_usage = getattr(response, "usage_metadata", None)
            if final_usage:
                yield ProviderStreamChunk(
                    type=ProviderChunkType.USAGE,
                    usage=TokenUsage(
                        input_tokens=getattr(final_usage, "prompt_token_count", 0) or 0,
                        output_tokens=getattr(final_usage, "candidates_token_count", 0) or 0,
                    ),
                )
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        except google_exceptions.Unauthenticated as exc:
            raise AuthenticationError(str(exc)) from exc
        except google_exceptions.ResourceExhausted as exc:
            raise RateLimitError(str(exc), retry_after=_retry_after(exc)) from exc
        except google_exceptions.ServiceUnavailable as exc:
            raise ProviderConnectionError(str(exc)) from exc
        finally:
            await _close_stream(response)

    async def list_models(self) -> list[Model]:
        models = []
        for m in genai.list_models():
            if "generateContent" not in getattr(m, "supported_generation_methods", []):
                continue
            models.append(Model(
                id=m.name.replace("models/", ""), provider=self.name,
                display_name=getattr(m, "display_name", m.name),
                context_window=getattr(m, "input_token_limit", 0) or 0,
                capabilities=ModelCapabilities(supports_streaming=True),
            ))
        return models

    async def health(self) -> ProviderHealth:
        if not self.config.api_key:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED, detail="No GEMINI_API_KEY set")
        try:
            list(genai.list_models())
            return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
        except google_exceptions.Unauthenticated:
            return ProviderHealth(status=ProviderHealthStatus.UNAUTHENTICATED)
        except google_exceptions.ServiceUnavailable:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE)
        except Exception as exc:  # noqa: BLE001
            return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=str(exc))