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
    {"type": "error", "error": "boom"}           -> raises, to script failures

B10 added three more, so a bounded tool loop can be exercised without a
live model:

    {"type": "tool_call", "name": "read_file",
     "arguments": {"path": "README.md"}, "id": "call-1"}
    {"type": "rate_limit", "retry_after": 1}     -> raises RateLimitError
    {"type": "hang", "seconds": 30}              -> sleeps, to be killed

A fixture may also be a **list of turns** rather than a list of chunks:

    {"model": "mock-1", "turns": [[chunk, ...], [chunk, ...]]}

Each `stream()` call consumes the next turn, so one fixture can script a
whole tool loop -- turn 1 asks for a tool, turn 2 asks for another, turn
3 answers. Turns are per *provider instance*, which is per process here,
and the last turn repeats once they run out rather than raising: a loop
that runs one step further than the script expected should fail its
bound, not die on a fixture.

A `rate_limit` chunk is consumed the first time it is seen and skipped on
every retry of that same turn -- otherwise "retries and eventually
succeeds" would be unscriptable, since a replayed turn would throttle
forever.

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
from providers.errors import InvalidRequestError, ProviderError, RateLimitError
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
    # The mock speaks the whole tool contract (schema in, tool_call
    # chunks out), because the alternative is that every tool-loop test
    # needs a key and a network -- which is how a test suite quietly
    # becomes something nobody runs.
    supports_tools = True

    def __init__(self, config) -> None:
        super().__init__(config)
        self._fixture = fixture_path()
        self._turn = 0
        self._rate_limits_fired: set[int] = set()

    # -- fixture loading ---------------------------------------------------

    def _load_turns(self) -> tuple[str, list[list[dict[str, Any]]]]:
        try:
            raw = json.loads(self._fixture.read_text(encoding="utf-8"))
        except FileNotFoundError as exc:
            raise InvalidRequestError(f"Mock fixture not found: {self._fixture}") from exc
        except json.JSONDecodeError as exc:
            raise InvalidRequestError(f"Mock fixture is not valid JSON: {exc}") from exc

        if isinstance(raw, list):
            return "mock-1", [raw]
        if isinstance(raw, dict):
            model = raw.get("model", "mock-1")
            if "turns" in raw:
                return model, [list(t) for t in raw["turns"]]
            return model, [list(raw.get("chunks", []))]
        raise InvalidRequestError("Mock fixture must be a list or an object")

    def _load(self) -> tuple[str, list[dict[str, Any]]]:
        """The single-turn view, kept for generate() and list_models()."""
        model, turns = self._load_turns()
        return model, turns[0] if turns else []

    def _next_turn(self) -> tuple[int, list[dict[str, Any]]]:
        """The turn this stream() call should replay, and its index.

        Clamped rather than exhausted: a loop that takes one step more
        than the fixture scripts should be stopped by its own bound and
        report that, not by the mock raising IndexError from underneath
        it."""
        _, turns = self._load_turns()
        if not turns:
            return 0, []
        index = min(self._turn, len(turns) - 1)
        self._turn += 1
        return index, turns[index]

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
        turn_index, chunks = self._next_turn()
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
            elif kind == "tool_call":
                yield ProviderStreamChunk(
                    type=ProviderChunkType.TOOL_CALL,
                    tool_call=ToolCall(
                        id=chunk.get("id") or f"mock-call-{turn_index}",
                        name=chunk.get("name", ""),
                        arguments=chunk.get("arguments", {}) or {},
                    ),
                )
            elif kind == "hang":
                # Never yields. The tool/step timeout is what ends this,
                # which is the whole point of scripting it.
                await asyncio.sleep(float(chunk.get("seconds", 3600)))
            elif kind == "rate_limit":
                # Fires once per turn. providers.retry reopens the stream,
                # the replay skips this chunk, and the turn succeeds --
                # so "retries and eventually succeeds" is one fixture
                # rather than two providers.
                if turn_index not in self._rate_limits_fired:
                    self._rate_limits_fired.add(turn_index)
                    raise RateLimitError(
                        chunk.get("error", "scripted mock rate limit"),
                        retry_after=chunk.get("retry_after"),
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
                capabilities=ModelCapabilities(supports_streaming=True, supports_tools=True),
            )
        ]

    async def health(self) -> ProviderHealth:
        if not self._fixture.exists():
            return ProviderHealth(
                status=ProviderHealthStatus.UNAVAILABLE,
                detail=f"fixture not found: {self._fixture}",
            )
        return ProviderHealth(status=ProviderHealthStatus.HEALTHY, detail=str(self._fixture))
