r"""DemoProvider: replays Point's shipped Demo-mode fixtures (B28).

Selectable and always registered (`providers\bootstrap.py`) -- the
opposite of `providers\mock\provider.py`'s `MockProvider`, which is
test-only (`POINT_MOCK=1`) and must never reach the UI (F10's own Do-NOT
list). The two providers replay the same kind of scripted chunk data for
opposite reasons, so this module does not import from `providers.mock`:
Mock may grow test-only chunk kinds (`rate_limit`, `hang`) this provider
must never need to understand, and the two are kept structurally
separate on purpose (same discipline as `ContextPolicy` vs. the real
`AgentConfig`, decisions.md D97). The ~15-line turn/chunk walk is
duplicated rather than shared for that reason -- recorded here, and in
`design/completed/backend/B28-demo-mode-provider-fixtures.md`, as the
call this episode made.

Demo mode promises "no network" (D55, F10's own Demo-mode section). That
promise is enforced structurally, not just by fixture discipline: every
fixture file is scanned for the literal string `web_search` the moment
it is loaded, and loading refuses loudly if it is ever found (`_fixture`
below).

**What picks which fixture plays** -- there is no CLI flag and no new
wire field, because the information already exists where a real model
would read it too:

  - `request.tools` is only ever non-empty for the `research` agent
    (`agents\builtins.py`) in this fixture set -- its presence IS the
    signal, the same way a real model infers "I may call tools" from
    being handed the schema.
  - The editor-context header `managers\context.py:_render_editor_context`
    renders into the prompt literally says `mode=fill` or `mode=explain`
    -- substrings even a real model reads to know which mode it is in,
    so scanning for them here is not a shortcut around anything; it is
    the same information channel.
  - A plain chat turn has neither signal, and is routed by a literal
    trigger phrase in the user's own latest message ("scripted failure",
    "cancel") so a demo presenter can deliberately reach the error and
    cancellation beats on command, or an ordinary cycling chat/queued
    reply otherwise.

**Research's "4-step tool loop" needs no turn counter at all.** Each
step's `GenerationRequest.messages` is the whole accumulated history for
that run (`agents\executor.py:_loop_body` appends one `ToolMessage` per
completed call), so counting how many TOOL-role messages are already in
`request.messages` tells this provider exactly which step it is on --
self-resetting per run, and safe if two research runs ever overlap in
one process. See `_research_step`.
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any, AsyncIterator, Optional

from managers.message import MessageRole, TokenUsage
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
    ToolCall,
)

_FIXTURE_DIR = Path(__file__).resolve().parents[2] / "demo" / "fixtures"
_MODEL_ID = "point-demo-v1"

# The two helper calls `agents\executor.py` makes through `generate()`
# (never `stream()`), identified by a substring of their own prompt text
# -- `_generate_title`'s and `_maybe_propose`'s, respectively. Neither is
# a named row in F10's fixture-coverage table; both fire on every demo
# turn regardless of which fixture is streaming, so they live here as
# plumbing rather than as a seventh/eighth fixture file.
_TITLE_PROMPT_MARKER = "short 3-6 word title"
_PROPOSAL_PROMPT_MARKER = "standalone fact(s) worth remembering"
_DEMO_SESSION_TITLE = "Point Demo Walkthrough"

# The editor-context headers `_render_editor_context` renders into the
# prompt (`managers\context.py`) -- the literal substrings this provider
# keys fixture selection off, same information a real model reads.
_FILL_MARKER = "mode=fill"
_EXPLAIN_MARKER = "mode=explain"


def _forbid_web_search(data: Any, source: Path) -> None:
    """D55/F10: Demo mode is "no network", enforced here rather than left
    as a fixture-authoring convention. Scans the whole loaded fixture,
    not just its tool_call entries -- a blunt check, deliberately: the
    one thing worse than a fixture that calls `web_search` is a second,
    narrower check that missed a new way of asking for it."""
    if "web_search" in json.dumps(data):
        raise InvalidRequestError(
            f"Demo fixture {source} requests or mentions the network-touching "
            "tool 'web_search', which Demo mode must never reach (D55: no "
            "network, no exceptions). Remove it before shipping this fixture."
        )


class DemoProvider(BaseProvider):
    name = "demo"
    # The opposite of MockProvider's `selectable = False` -- Demo mode is
    # a real, user-facing choice (F10's provider chooser), not a test
    # fixture wearing a provider's interface.
    selectable = True
    # research (`agents\builtins.py`) grants tools; without this, `run()`
    # raises ToolsUnsupportedError at invocation before a token streams.
    supports_tools = True

    def __init__(self, config: Any) -> None:
        super().__init__(config)
        self._fixture_cache: dict[str, dict[str, Any]] = {}
        # Only the plain-chat fixture needs turn state (chat reply, then
        # the queued reply, repeating the last once exhausted) -- every
        # other fixture is either single-shot or self-resetting from the
        # request's own content (see _research_step).
        self._chat_turn = 0

    # -- fixture loading ----------------------------------------------------

    def _fixture(self, name: str) -> dict[str, Any]:
        cached = self._fixture_cache.get(name)
        if cached is not None:
            return cached
        path = _FIXTURE_DIR / name
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError as exc:
            raise InvalidRequestError(f"Demo fixture not found: {path}") from exc
        except json.JSONDecodeError as exc:
            raise InvalidRequestError(f"Demo fixture is not valid JSON: {exc}") from exc
        _forbid_web_search(data, path)
        self._fixture_cache[name] = data
        return data

    # -- fixture selection ---------------------------------------------------

    def _classify(self, request: GenerationRequest) -> str:
        combined = "\n".join(m.text for m in request.messages)
        if _FILL_MARKER in combined:
            return "fill"
        if _EXPLAIN_MARKER in combined:
            return "explain"
        if request.tools:
            return "research"
        latest_user = ""
        for message in reversed(request.messages):
            if message.role == MessageRole.USER:
                latest_user = message.text
                break
        low = latest_user.lower()
        if "scripted failure" in low:
            return "error"
        if "cancel" in low:
            return "cancellation"
        return "chat"

    # -- BaseProvider --------------------------------------------------------

    async def generate(self, request: GenerationRequest) -> GenerationResponse:
        """Only ever reached by `_generate_title` or `_maybe_propose`
        (`agents\\executor.py`) -- a real turn always streams. Both are
        best-effort and swallow their own exceptions, so an unrecognized
        prompt here safely degrades to an empty reply rather than ever
        needing to raise."""
        combined = "\n".join(m.text for m in request.messages)
        if _PROPOSAL_PROMPT_MARKER in combined:
            facts = self._fixture("research_tool_loop.json").get("proposals", [])
            return GenerationResponse(
                text=json.dumps(facts),
                usage=TokenUsage(input_tokens=180, output_tokens=24),
                model=request.model,
            )
        if _TITLE_PROMPT_MARKER in combined:
            return GenerationResponse(
                text=_DEMO_SESSION_TITLE,
                usage=TokenUsage(input_tokens=60, output_tokens=6),
                model=request.model,
            )
        return GenerationResponse(text="", usage=TokenUsage(), model=request.model)

    async def stream(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        kind = self._classify(request)
        if kind == "fill":
            async for chunk in self._play(self._fixture("filler_fill.json")["chunks"]):
                yield chunk
        elif kind == "explain":
            async for chunk in self._play(self._fixture("filler_explain.json")["chunks"]):
                yield chunk
        elif kind == "research":
            async for chunk in self._research_step(request):
                yield chunk
        elif kind == "error":
            async for chunk in self._play(self._fixture("error_case.json")["chunks"]):
                yield chunk
        elif kind == "cancellation":
            async for chunk in self._play(self._fixture("cancellation.json")["chunks"]):
                yield chunk
        else:
            async for chunk in self._play(self._next_chat_turn()):
                yield chunk

    async def list_models(self) -> list[Model]:
        return [
            Model(
                id=_MODEL_ID, provider=self.name, display_name="Point Demo (pre-recorded)",
                context_window=0,
                capabilities=ModelCapabilities(supports_streaming=True, supports_tools=True),
            )
        ]

    async def health(self) -> ProviderHealth:
        if not _FIXTURE_DIR.is_dir():
            return ProviderHealth(
                status=ProviderHealthStatus.UNAVAILABLE,
                detail=f"fixture directory not found: {_FIXTURE_DIR}",
            )
        return ProviderHealth(status=ProviderHealthStatus.HEALTHY, detail=str(_FIXTURE_DIR))

    # -- chat turn cycling ----------------------------------------------------

    def _next_chat_turn(self) -> list[dict[str, Any]]:
        turns = self._fixture("chat_turns.json")["turns"]
        index = min(self._chat_turn, len(turns) - 1)
        self._chat_turn += 1
        return turns[index]

    # -- research's stateless step selection ---------------------------------

    def _research_step(self, request: GenerationRequest) -> AsyncIterator[ProviderStreamChunk]:
        steps = self._fixture("research_tool_loop.json")["steps"]
        completed_tool_calls = sum(1 for m in request.messages if m.role == MessageRole.TOOL)
        step = steps[min(completed_tool_calls, len(steps) - 1)]
        return self._play_research_step(step)

    async def _play_research_step(self, step: dict[str, Any]) -> AsyncIterator[ProviderStreamChunk]:
        delay = float(step.get("delay", 0.0))
        if delay:
            await asyncio.sleep(delay)
        if "tool_call" in step:
            call = step["tool_call"]
            yield ProviderStreamChunk(
                type=ProviderChunkType.TOOL_CALL,
                tool_call=ToolCall(
                    id=f"demo-call-{call['name']}",
                    name=call["name"],
                    arguments=call.get("arguments", {}) or {},
                ),
            )
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
            return
        for text_chunk in step.get("text_chunks", []):
            chunk_delay = float(text_chunk.get("delay", 0.0))
            if chunk_delay:
                await asyncio.sleep(chunk_delay)
            yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=text_chunk.get("text", ""))
        usage = step.get("usage")
        if usage:
            yield ProviderStreamChunk(
                type=ProviderChunkType.USAGE,
                usage=TokenUsage(
                    input_tokens=usage.get("input_tokens", 0),
                    output_tokens=usage.get("output_tokens", 0),
                ),
            )
        yield ProviderStreamChunk(type=ProviderChunkType.DONE)

    # -- the shared flat-chunk player ----------------------------------------

    async def _play(self, chunks: list[dict[str, Any]]) -> AsyncIterator[ProviderStreamChunk]:
        """Plays a flat `{"type": ..., "delay": ...}` chunk list -- the
        format every fixture except `research_tool_loop.json` uses
        (that one needs per-step tool-call branching, see
        `_research_step`). Deliberately narrower than MockProvider's own
        player: no `rate_limit`, no `hang` -- Demo mode never scripts a
        retry or a timeout, so there is nothing here to keep in sync with
        if Mock ever grows another test-only kind."""
        saw_done = False
        for chunk in chunks:
            delay = float(chunk.get("delay", 0.0))
            if delay:
                await asyncio.sleep(delay)
            kind = chunk.get("type")
            if kind == "text":
                yield ProviderStreamChunk(type=ProviderChunkType.TEXT, text=chunk.get("text", ""))
            elif kind == "usage":
                yield ProviderStreamChunk(
                    type=ProviderChunkType.USAGE,
                    usage=TokenUsage(
                        input_tokens=chunk.get("input_tokens", 0),
                        output_tokens=chunk.get("output_tokens", 0),
                    ),
                )
            elif kind == "error":
                raise ProviderError(chunk.get("error", "demo: scripted failure"))
            elif kind == "done":
                saw_done = True
                yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        if not saw_done:
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
