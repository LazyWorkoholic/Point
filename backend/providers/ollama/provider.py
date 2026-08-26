"""OllamaProvider: talks to a locally-running Ollama HTTP server.

Chosen as the first concrete provider because it's local -- no API
key, no billing -- making it the fastest thing to actually test.
"""

from __future__ import annotations

import json
from typing import AsyncIterator

import httpx

from managers.message import TokenUsage
from providers.base import BaseProvider
from providers.errors import (
    ModelNotFoundError,
    ProviderConnectionError,
    ProviderTimeoutError,
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
from providers.ollama.translator import OllamaMessageTranslator


class OllamaProvider(BaseProvider):
    name = "ollama"

    def __init__(self, config) -> None:
        super().__init__(config)
        self._base_url = (config.base_url or "http://localhost:11434").rstrip("/")
        self._translator = OllamaMessageTranslator()

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        payload = {
            "model": request.model,
            "messages": [self._translator.to_provider_format(m) for m in request.messages],
            "stream": False,
        }
        try:
            async with httpx.AsyncClient(timeout=self.config.timeout) as client:
                resp = await client.post(f"{self._base_url}/api/chat", json=payload)
        except httpx.ConnectError as exc:
            raise ProviderConnectionError(f"Could not reach Ollama at {self._base_url}") from exc
        except httpx.TimeoutException as exc:
            raise ProviderTimeoutError("Ollama request timed out") from exc

        if resp.status_code == 404:
            raise ModelNotFoundError(f"Ollama model not found: {request.model}")
        resp.raise_for_status()
        data = resp.json()

        text = data.get("message", {}).get("content", "")
        usage = TokenUsage(
            input_tokens=data.get("prompt_eval_count", 0),
            output_tokens=data.get("eval_count", 0),
        )
        return GenerationResponse(text=text, usage=usage, model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        """Effort (B6 §8) is accepted and ignored: /api/chat has no
        equivalent knob, which is exactly the case supports_effort=False
        exists to describe."""
        payload = {
            "model": request.model,
            "messages": [self._translator.to_provider_format(m) for m in request.messages],
            "stream": True,
        }
        try:
            async with httpx.AsyncClient(timeout=self.config.timeout) as client:
                async with client.stream("POST", f"{self._base_url}/api/chat", json=payload) as resp:
                    resp.raise_for_status()
                    async for line in resp.aiter_lines():
                        if not line:
                            continue
                        chunk = json.loads(line)
                        content = chunk.get("message", {}).get("content", "")
                        if content:
                            yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=content)
                        if chunk.get("done"):
                            usage = TokenUsage(
                                input_tokens=chunk.get("prompt_eval_count", 0),
                                output_tokens=chunk.get("eval_count", 0),
                            )
                            yield ProviderStreamChunk(type=ProviderChunkType.USAGE, usage=usage)
                            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        except httpx.ConnectError as exc:
            raise ProviderConnectionError(f"Could not reach Ollama at {self._base_url}") from exc

    async def list_models(self) -> list[Model]:
        try:
            async with httpx.AsyncClient(timeout=self.config.timeout) as client:
                resp = await client.get(f"{self._base_url}/api/tags")
        except httpx.ConnectError as exc:
            raise ProviderConnectionError(f"Could not reach Ollama at {self._base_url}") from exc
        resp.raise_for_status()
        data = resp.json()
        return [
            Model(
                id=m["name"], provider=self.name, display_name=m["name"],
                context_window=0,  # not exposed via /api/tags
                capabilities=ModelCapabilities(supports_streaming=True),
            )
            for m in data.get("models", [])
        ]

    async def health(self) -> ProviderHealth:
        try:
            async with httpx.AsyncClient(timeout=5.0) as client:
                resp = await client.get(f"{self._base_url}/api/tags")
            if resp.status_code == 200:
                return ProviderHealth(status=ProviderHealthStatus.HEALTHY)
            return ProviderHealth(status=ProviderHealthStatus.UNHEALTHY, detail=f"status {resp.status_code}")
        except httpx.ConnectError:
            return ProviderHealth(status=ProviderHealthStatus.UNAVAILABLE, detail="Ollama not reachable")