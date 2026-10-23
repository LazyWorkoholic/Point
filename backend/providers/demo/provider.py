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
import logging
import os
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

# POINT_DEMO_LONG_FILL=1 swaps the fill fixture for a 46-line one.
#
# Why a second fixture rather than a longer first one: `filler_fill.json`
# is what B28 shipped and what `manual_b28_demo_mode.py` asserts against,
# and its five-line proposal is the right size for demonstrating the
# feature. It is the wrong size for TESTING the Editor's ghost rendering
# -- scrolling, click-to-line mapping and arrow movement through ghost
# rows only misbehave visibly once the region is taller than the pane,
# which five lines never is. F15's gate could only see a caret offset by
# reaching for a real model, which brings its own unrelated output-format
# problems (see this episode's notes on qwen2.5:3b).
#
# So: same provider, same deterministic replay, one env var. Both
# fixtures propose against app.py lines 4-8, so B24 validates either one
# identically and the only difference is how much text comes back.
_LONG_FILL_ENV = "POINT_DEMO_LONG_FILL"

# Two debug-only pacing overrides, same shape and same reasoning as
# _LONG_FILL_ENV above: off unless set, read once per stream so a
# long-lived process can be re-paced between runs, and confined to this
# module -- which only loads for the Demo provider, so they are demo-only
# by construction rather than by a check someone has to remember.
#
# filler_fill_long.json carries NO baked-in per-chunk delay, deliberately:
# its 604 pieces are there to be paced by the first variable below, and
# baking a cadence in would mean ~600 platform timer waits on every gate
# and test run for a cadence nobody is watching. Unset, it streams in
# about 0.06s. The one 50ms lead-in chunk stays, so the ghost's first
# paint is a frame rather than an already-finished region.
#
# They exist because the Editor's ghost has two behaviours that are only
# observable WHILE text is arriving, and the shipped fixtures arrive
# faster than a human can look:
#
#   POINT_DEMO_STREAM_DELAY_MS  per chunk, REPLACING the fixture's own
#                               `delay` rather than adding to it -- one
#                               number gives an even cadence to watch
#                               the ghost scroll, follow the caret and
#                               click a line mid-stream. Additive would
#                               mean two fixtures paced differently at
#                               the same setting; an even cadence is also
#                               what makes a scroll hitch readable as the
#                               VIEWPORT's rather than the fixture's.
#
#                               PER CHUNK, and the fixtures have very
#                               different chunk counts, so the useful
#                               value depends on which one is playing:
#
#                                 filler_fill_long   604 chunks, no
#                                   baked-in delay. 10 gives a smooth
#                                   ~9s typewriter over its 46 lines;
#                                   40 gives ~28s. Measured, including
#                                   the timer overshoot below.
#                                 filler_fill          2 chunks, 500-600ms
#                                 filler_explain       4 chunks, 500-700ms
#                                   -- for these a value under ~500 is
#                                   FASTER than shipped, not slower.
#
#                               The overshoot is real and not worth
#                               correcting for: 604 waits of 10ms took
#                               9.45s rather than 6.04s, because each
#                               asyncio.sleep rounds up to the platform
#                               timer. Pick by feel, not by arithmetic.
#
#   POINT_DEMO_CARD_DELAY_MS    one pause before the DONE chunk, which
#                               is the gap between "the ghost stopped
#                               growing" and "the diff card opens": the
#                               backend pushes B24's FillProposal AFTER
#                               TaskDone, and TaskDone follows DONE, so
#                               holding DONE back holds both. Separate
#                               from the per-chunk delay because the
#                               thing being inspected is the fully
#                               streamed ghost with no card over it,
#                               which no per-chunk value can produce.
#
# Neither is a product feature and neither belongs in config.toml: they
# pace a fixture replay, and a real provider's pacing is the network's.
_STREAM_DELAY_ENV = "POINT_DEMO_STREAM_DELAY_MS"
_CARD_DELAY_ENV = "POINT_DEMO_CARD_DELAY_MS"

_log = logging.getLogger(__name__)


def _delay_override(name: str) -> Optional[float]:
    """Milliseconds from `name`, in seconds, or None if unset.

    A value that is not a non-negative number is IGNORED rather than
    raised on -- a typo in a debugging variable must not fail a session
    -- but it is logged, because a silently ignored override reads
    exactly like a feature that does not work. Stderr, never stdout
    (that is the protocol channel).
    """
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return None
    try:
        ms = float(raw)
    except ValueError:
        _log.warning("demo: ignoring %s=%r -- not a number", name, raw)
        return None
    if ms < 0:
        _log.warning("demo: ignoring %s=%r -- must not be negative", name, raw)
        return None
    return ms / 1000.0


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

    @staticmethod
    def _fill_fixture() -> str:
        """Which fill fixture to replay -- see _LONG_FILL_ENV.

        Read per call rather than cached at construction so the variable
        can be flipped between runs of a long-lived process (the gate
        launches Point more than once against one scratch profile), and
        read as "exactly 1" for the same reason POINT_MOCK and
        POINT_BACKEND_VERBOSE are: a truthy-string test would make
        POINT_DEMO_LONG_FILL=0 turn it ON.
        """
        if os.environ.get(_LONG_FILL_ENV) == "1":
            return "filler_fill_long.json"
        return "filler_fill.json"

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
            async for chunk in self._play(self._fixture(self._fill_fixture())["chunks"]):
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
        # The per-chunk override applies here too: the variable is named
        # for streaming, not for fill, and a research step that ignored
        # it would make the name a lie. _CARD_DELAY_ENV deliberately does
        # NOT -- a research step has no diff card, and its DONE ends a
        # tool-loop iteration rather than a reply.
        per_chunk = _delay_override(_STREAM_DELAY_ENV)
        for text_chunk in step.get("text_chunks", []):
            chunk_delay = float(text_chunk.get("delay", 0.0)) if per_chunk is None else per_chunk
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
        # Read once per stream, not per chunk: same granularity as
        # _fill_fixture (which stream() calls once), and it keeps a bad
        # value's warning to one line instead of one per chunk.
        per_chunk = _delay_override(_STREAM_DELAY_ENV)
        card_gap = _delay_override(_CARD_DELAY_ENV)

        saw_done = False
        for chunk in chunks:
            delay = float(chunk.get("delay", 0.0)) if per_chunk is None else per_chunk
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
                if card_gap:
                    await asyncio.sleep(card_gap)
                yield ProviderStreamChunk(type=ProviderChunkType.DONE)
        if not saw_done:
            # Both exits get the gap. A fixture that ends without an
            # explicit `done` chunk takes this branch, and
            # filler_fill.json is one of them -- an override that worked
            # for the long fixture and not the shipped one would be worse
            # than no override.
            if card_gap:
                await asyncio.sleep(card_gap)
            yield ProviderStreamChunk(type=ProviderChunkType.DONE)
