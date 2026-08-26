"""MockProvider: replays a scripted stream from a fixture file.

Registered only when POINT_MOCK=1, and never selectable from the UI
(`selectable = False`). Without it every later test needs a live model,
a key, and a network -- which is how a test suite quietly becomes
something nobody runs.

Fixture format, either shape:

    {"model": "mock-1", "chunks": [ ... ]}
    [ ... ]                                  # just the chunks

    {"type": "text",  "text": "Hel", "delay": 0.02}
    {"type": "thinking", "text": "...", "delay": 0}
    {"type": "usage", "input_tokens": 12, "output_tokens": 7}
    {"type": "done"}
    {"type": "error", "error": "rate limited"}   -> raises, to script failures

`delay` is honoured because a stream that arrives instantly does not
exercise cancellation, and cancellation is the thing worth testing.
"""

from __future__ import annotations

import asyncio
import json
import os
from pathlib import Path
from typing import Any, AsyncIterator

from managers.message import TokenUsage
from providers.base import BaseProvider
from providers.errors import InvalidRequestError, ProviderError
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

ENV_ENABLED = "POINT_MOCK"
ENV_FIXTURE = "POINT_MOCK_FIXTURE"

_DEFAULT_FIXTURE = Path(__file__).resolve().parents[2] / "tests" / "fixtures" / "mock_stream.json"


def is_enabled() -> bool:
    return os.environ.get(ENV_ENABLED) == "1"


def fixture_path() -> Path:
    override = os.environ.get(ENV_FIXTURE)
    return Path(override) if override else _DEFAULT_FIXTURE


class MockProvider(BaseProvider):
    name = "mock"
    selectable = False

    def __init__(self, config) -> None:
        super().__init__(config)
        self._fixture = fixture_path()

    # -- fixture loading ---------------------------------------------------

    def _load(self) -> tuple[str, list[dict[str, Any]]]:
        try:
            raw = json.loads(self._fixture.read_text(encoding="utf-8"))
        except FileNotFoundError as exc:
            raise InvalidRequestError(f"Mock fixture not found: {self._fixture}") from exc
        except json.JSONDecodeError as exc:
            raise InvalidRequestError(f"Mock fixture is not valid JSON: {exc}") from exc

        if isinstance(raw, list):
            return "mock-1", raw
        if isinstance(raw, dict):
            return raw.get("model", "mock-1"), list(raw.get("chunks", []))
        raise InvalidRequestError("Mock fixture must be a list or an object")

    # -- BaseProvider ------------------------------------------------------

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        _, chunks = self._load()
        text = "".join(c.get("text", "") for c in chunks if c.get("type") == "text")
        usage = TokenUsage()
        for chunk in chunks:
            if chunk.get("type") == "usage":
                usage = TokenUsage(
                    input_tokens=chunk.get("input_tokens", 0),
                    output_tokens=chunk.get("output_tokens", 0),
                )
        return GenerationResponse(text=text, usage=usage, model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        _, chunks = self._load()
        saw_done = False
        for chunk in chunks:
            delay = float(chunk.get("delay", 0.0))
            if delay:
                await asyncio.sleep(delay)

            kind = chunk.get("type")
            if kind == "text":
                yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=chunk.get("text", ""))
            elif kind == "thinking":
                yield ProviderStreamChunk(type=ProviderChunkType.THINKING, text=chunk.get("text", ""))
            elif kind == "usage":
                yield ProviderStreamChunk(
                    type=ProviderChunkType.USAGE,
                    usage=TokenUsage(
                        input_tokens=chunk.get("input_tokens", 0),
                        output_tokens=chunk.get("output_tokens", 0),
                    ),
                )
            elif kind == "error":
                raise ProviderError(chunk.get("error", "scripted mock failure"))
            elif kind == "done":
                saw_done = True
                yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        if not saw_done:
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)

    async def list_models(self) -> list[Model]:
        model_id, _ = self._load()
        return [
            Model(
                id=model_id, provider=self.name, display_name=f"Mock ({self._fixture.name})",
                context_window=0,
                capabilities=ModelCapabilities(supports_streaming=True),
            )
        ]

    async def health(self) -> ProviderHealth:
        if not self._fixture.exists():
            return ProviderHealth(
                status=ProviderHealthStatus.UNAVAILABLE,
                detail=f"fixture not found: {self._fixture}",
            )
        return ProviderHealth(status=ProviderHealthStatus.HEALTHY, detail=str(self._fixture))
