r"""AgentExecutor: the one execution path every message goes through
(project_idea.md §2). Config-driven -- this file must never grow an
`if agent.id == ...`.

    resolve config
      -> resolve provider/model/effort  (task > session > agent > global)
      -> resolve bounds                 (task override > agent config)
      -> build context (B8) with the agent's budget
      -> create a Task (B1 TaskManager)
      -> run the bounded tool loop (B10) through the provider (B4)
         via the streaming manager (B5)
      -> emit events
      -> persist the assistant message

`inline` vs `background` is a property of the INVOCATION, not the
agent -- callers pass it. Nothing here decides it from `AgentConfig`,
and nothing here reads it yet either: v1 has exactly one call site
(`Runtime._handle_send_message`) and it is always inline. The parameter
exists now so a background caller (Research, B11) is a new call site,
not a rewiring of this one.

B10 replaced B9's single provider round-trip with a bounded loop. There
is still exactly ONE task body: an agent with no tools and `max_steps=1`
runs the loop once, sees no tool calls, and returns -- the same shape
`general` had before, arrived at by configuration rather than by a
branch on which agent it is.

What this file must never do, same as B8's context builder:

  - Hold a database handle of its own. It takes `SessionManager`,
    `SessionMemoryService`, and `ProjectManager` -- all three already
    keep SQL behind them.
  - Implement a tool. Tools live in `tools\builtins\`; this file only
    knows how to ask the registry which ones an agent may call, and how
    to feed a result back to the model.
  - Decide *which* agent a session uses. That is
    `AgentRegistry.resolve_for_session()`'s job; this file is hand
    -ed an already-resolved `AgentConfig`.
"""

from __future__ import annotations

import asyncio
import time
import uuid
from collections import deque
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Awaitable, Callable, Optional

import appconfig
from logging_setup import get_logger
from managers import context
from managers.agent import AgentConfig
from managers.context import SessionMemoryService
from managers.message import Message, ToolMessage, UserMessage
from managers.project import Project
from managers.project_manager import ProjectManager
from managers.session import Session
from managers.session_manager import SessionManager
from providers import retry
from providers.models import Effort, GenerationRequest, ToolCall
from providers.registry import ProviderRegistry
from runtime.errors import BoundsExceededError, InvalidEventError, ToolsUnsupportedError
from runtime.task import EventSink, Task
from runtime.task_manager import TaskManager
from streaming.adapter import StreamAdapter
from streaming.events import StreamEvent, StreamEventType
from streaming.manager import StreamManager
from tools.executor import EVENT_COMPLETED, EVENT_STARTED, ToolExecutor
from tools.models import Tool
from tools.registry import ToolRegistry

_log = get_logger("agents.executor")

# The no-op guardrails hook (B9's "Middleware chain" build item). Typed
# against the plain dicts/Messages already flowing through this file
# rather than a new envelope.
RequestMiddleware = Callable[[dict[str, Any]], Awaitable[Optional[dict[str, Any]]]]
ResponseMiddleware = Callable[[Message], Awaitable[Optional[Message]]]

# `make_sink(message_id) -> EventSink`. The sink itself stays Runtime's:
# it is `_emit`/envelope-filling machinery, which is transport, not
# agent execution. This file only needs to hand it the message_id it
# generated, so the stream's events and the eventual persisted message
# agree on one id.
SinkFactory = Callable[[str], EventSink]

# How often the loop looks for tool events while a tool call is in
# flight. Small enough that `tool.started` reaches the UI as the call
# begins rather than as it ends, large enough not to spin.
_TOOL_EVENT_POLL_S = 0.05


@dataclass(frozen=True)
class ProviderOverride:
    """The top rung of the provider resolution chain -- a per-task
    override. Nothing on the wire sets one yet (`SendMessage` carries no
    override fields in v1), but the chain has all four rungs from day one
    rather than growing a fourth parameter into `run()` the day something
    needs it."""

    provider: Optional[str] = None
    model: Optional[str] = None
    effort: Optional[str] = None


@dataclass(frozen=True)
class BoundsOverride:
    """A per-send override of the loop's bounds (B10).

    Deliberately **not** an extension of `ProviderOverride`, and
    deliberately **not** a mutation of `AgentConfig`. `AgentConfig` is
    frozen and every v1 agent is builtin, which the repository layer
    refuses to edit (B9) -- so "run this one send with max_tool_calls=2"
    has to be a separate value travelling beside the config, not a
    changed config. Keeping it separate is also what stops a test flag
    from being able to permanently loosen an agent's ceiling.

    None on a field means "use the agent's own value".
    """

    max_steps: Optional[int] = None
    max_tool_calls: Optional[int] = None
    timeout_s: Optional[int] = None


@dataclass(frozen=True)
class Bounds:
    """The resolved ceiling one run must stay inside."""

    max_steps: int
    max_tool_calls: int
    timeout_s: int


def resolve_bounds(override: Optional[BoundsOverride], agent: AgentConfig) -> Bounds:
    override = override or BoundsOverride()
    return Bounds(
        max_steps=_first_set(override.max_steps, agent.max_steps),
        max_tool_calls=_first_set(override.max_tool_calls, agent.max_tool_calls),
        timeout_s=_first_set(override.timeout_s, agent.timeout_s),
    )


def _first_set(override: Optional[int], fallback: int) -> int:
    """`0` is a meaningful bound (`general` grants zero tool calls), so
    this cannot be `override or fallback`."""
    return fallback if override is None else override


@dataclass(frozen=True)
class ToolServices:
    """What a tool may reach through `AgentContext` (B10).

    Managers only -- never a connection, never a provider client. Same
    rule this file follows for itself: `ProjectManager` and
    `SessionMemoryService` both already keep SQL behind them, so a tool
    holding one cannot issue a query of its own devising.

    `project` is resolved once per run rather than per call: it is the
    root every path in that run is guarded against, and re-resolving it
    mid-loop would mean the third tool call could be scoped differently
    from the first.
    """

    projects: ProjectManager
    memory: SessionMemoryService
    project: Optional[Project]


@dataclass
class AgentContext:
    """What a running agent may touch (B9's Build section). No database
    handle, no provider client of its own -- both stay behind the
    executor. B10 filled in `tools` (the granted set, by name) and added
    `services`."""

    config: AgentConfig
    session_id: str
    project_id: Optional[str]
    built: context.BuiltContext
    services: ToolServices
    tools: dict[str, Tool] = field(default_factory=dict)
    emit: Optional[Callable[[dict[str, Any]], Awaitable[None]]] = None


@dataclass(frozen=True)
class AgentRun:
    task: Task
    message_id: str


def resolve_provider_model_effort(
    *, override: Optional[ProviderOverride], session: Session, agent: AgentConfig,
) -> tuple[str, str, Optional[str]]:
    """task override > session > agent default > global config
    (project_idea.md §10).

    Raises InvalidEventError when nothing in the chain names a
    provider/model -- guessing here is the exact habit B6's
    CreateSession removed; SendMessage already refuses the same way for
    a NULL session, this just widens the check to the whole chain.
    """
    override = override or ProviderOverride()
    provider = (
        override.provider
        or session.provider
        or agent.provider
        or appconfig.config_value("provider.default")
    )
    model = (
        override.model
        or session.model
        or agent.model
        or appconfig.config_value("model.default")
    )
    effort = (
        override.effort
        or session.effort
        or agent.effort
        or appconfig.config_value("effort.default")
    )
    if not provider or not model:
        raise InvalidEventError(
            f"No provider/model resolved for session {session.id!r} via "
            f"agent {agent.id!r} (checked task override, session, agent "
            "default, and global config). Set one explicitly with "
            "SetSessionProvider."
        )
    return provider, model, effort


class AgentExecutor:
    def __init__(
        self,
        sessions: SessionManager,
        memory: SessionMemoryService,
        tasks: TaskManager,
        providers: ProviderRegistry,
        streams: StreamManager,
        stream_adapter: StreamAdapter,
        resolve_provider_name: Callable[[str], str],
        projects: ProjectManager,
        tools: ToolRegistry,
        tool_recorder: Optional[Callable[[Any], Awaitable[None]]] = None,
    ) -> None:
        self._sessions = sessions
        self._memory = memory
        self._tasks = tasks
        self._providers = providers
        self._streams = streams
        self._stream_adapter = stream_adapter
        self._resolve_provider_name = resolve_provider_name
        self._projects = projects
        self._tools = tools
        self._tool_recorder = tool_recorder

        # The guardrails hook. Empty lists -- nothing appends to these in
        # v1. Verified by hand during B9: append a logging middleware,
        # run a send, see it log, remove it again.
        self.request_middleware: list[RequestMiddleware] = []
        self.response_middleware: list[ResponseMiddleware] = []

    async def run(
        self,
        *,
        session: Session,
        text: str,
        agent: AgentConfig,
        make_sink: SinkFactory,
        inline: bool,
        override: Optional[ProviderOverride] = None,
        bounds: Optional[BoundsOverride] = None,
    ) -> AgentRun:
        provider_name, model, effort = resolve_provider_model_effort(
            override=override, session=session, agent=agent,
        )
        resolved_provider = self._resolve_provider_name(provider_name)
        limits = resolve_bounds(bounds, agent)
        granted = self._tools.list_for(agent)

        # Refused at INVOCATION, before a task exists and before a token
        # is streamed (B10). A model that silently ignores tools it was
        # never sent produces a confidently wrong answer, which is worse
        # than an error.
        if granted and provider_name != "echo":
            provider = self._providers.get(resolved_provider)
            if not provider.supports_tools:
                raise ToolsUnsupportedError(
                    f"Agent {agent.id!r} requires tools "
                    f"({', '.join(t.name for t in granted)}), but provider "
                    f"{resolved_provider!r} cannot take them."
                )

        # Context is built BEFORE the user message is persisted and
        # before the task exists (B8, carried over verbatim): an
        # oversized message fails as a clean ContextOverflowError
        # instead of an opaque provider 400, and failing before the
        # INSERT means a message that can never be sent never enters
        # history to poison every later build.
        built = await context.build(
            context.ContextRequest(
                session_id=session.id,
                provider=resolved_provider,
                model=model,
                user_text=text,
            ),
            agent.to_context_policy(),
            context.default_budget_tokens(),
            self._memory,
        )
        _log.debug(
            "Context for %s via agent %s: %d/%d tokens, %d evicted",
            session.id, agent.id, built.log.used_tokens, built.log.budget_tokens,
            len(built.log.evicted),
        )

        await self._run_request_middleware({
            "session_id": session.id,
            "agent_id": agent.id,
            "provider": resolved_provider,
            "model": model,
            "text": text,
        })

        user_message = UserMessage(session.id, text)
        await self._sessions.append_message(user_message)

        task_id = str(uuid.uuid4())
        message_id = str(uuid.uuid4())

        agent_context = AgentContext(
            config=agent,
            session_id=session.id,
            project_id=session.project_id,
            built=built,
            services=ToolServices(
                projects=self._projects,
                memory=self._memory,
                project=await self._memory.project(session.id),
            ),
            tools={tool.name: tool for tool in granted},
        )

        def body_factory() -> AsyncIterator[dict[str, Any]]:
            if provider_name == "echo":
                # Dev/test convenience: no provider/network required.
                # Not a real registered provider on purpose.
                return self._echo_body(text)
            return self._loop_body(
                resolved_provider, model, effort, agent_context, limits,
                task_id, session.id, message_id,
            )

        sink = make_sink(message_id)
        task = self._tasks.create_task(
            body=body_factory,
            session_id=session.id,
            kind="chat",
            sink=sink,
            task_id=task_id,
            agent_id=agent.id,
        )
        await self._sessions.set_active_task(session.id, task.id)
        return AgentRun(task=task, message_id=message_id)

    # -- middleware -------------------------------------------------------

    async def _run_request_middleware(self, request: dict[str, Any]) -> dict[str, Any]:
        for middleware in self.request_middleware:
            request = await middleware(request) or request
        return request

    async def apply_response_middleware(self, message: Message) -> Message:
        """Called by Runtime's sink, right before a finalized assistant
        message is persisted -- the one point every reply, streamed or
        echoed, passes through. Public because the sink stays in
        Runtime (it is `_emit`/envelope machinery, transport rather than
        agent execution) and cannot be folded in here without this file
        acquiring the database handle and provider client `AgentContext`
        is explicit that it must never carry."""
        for middleware in self.response_middleware:
            message = await middleware(message) or message
        return message

    # -- task bodies --------------------------------------------------------

    async def _echo_body(self, text: str) -> AsyncIterator[dict[str, Any]]:
        """Placeholder task body for the no-network `echo` provider."""
        for word in text.split():
            await asyncio.sleep(0.05)
            yield {"text": word + " "}

    async def _loop_body(
        self,
        provider_name: str,
        model: str,
        effort: Optional[str],
        agent_context: AgentContext,
        limits: Bounds,
        task_id: str,
        session_id: str,
        message_id: str,
    ) -> AsyncIterator[dict[str, Any]]:
        r"""The bounded tool-call loop (B10):

            model -> tool_calls? -> execute (respecting max_tool_calls)
                  -> append results -> model -> ... -> final answer

        bounded by max_steps, max_tool_calls and timeout_s. Exceeding any
        of them raises `BoundsExceededError`, which Task turns into
        `task.failed` with `code="bounds_exceeded"` -- and because the
        accumulator has been fed every delta as it arrived, whatever
        partial answer exists is still finalized and persisted by the
        sink's `finalize_interrupted()` path. Stopping at a boundary
        loses the *remaining* work, never the work already done.

        Cancellation note carried over from B9's `_stream_body`: when
        Task.cancel() fires, CancelledError is raised at whichever await
        this generator is suspended on -- almost always inside
        provider.stream()'s own `async for`. Every provider wraps its SDK
        client in `async with`, so that context manager's __aexit__ runs
        during teardown and closes the underlying connection. B10 adds
        one more place to be suspended -- an in-flight tool call -- and
        that one is cancelled explicitly in the `finally` below.
        """
        self._streams.register_stream(task_id=task_id, session_id=session_id, message_id=message_id)
        provider = self._providers.get(provider_name)  # ProviderNotRegisteredError -> clean Error event

        effort_enum: Optional[Effort] = None
        if effort:
            try:
                effort_enum = Effort(effort)
            except ValueError:
                _log.warning("Unknown effort %r for provider %s; ignoring", effort, provider_name)

        granted = list(agent_context.tools.values())
        tool_schemas = [tool.to_schema() for tool in granted] or None

        # Tool events are produced inside `await executor.execute(...)`,
        # which cannot yield. The executor appends them here and the loop
        # drains them -- so `tool.started` reaches the UI while the call
        # is still running, not once it has finished.
        pending_events: deque[tuple[str, dict[str, Any]]] = deque()

        async def collect(name: str, payload: dict[str, Any]) -> None:
            pending_events.append((name, payload))

        tool_executor = ToolExecutor(emit=collect, recorder=self._tool_recorder)

        messages: list[Message] = list(agent_context.built.messages)
        tool_calls_used = 0
        deadline = time.monotonic() + limits.timeout_s

        def emit_stream(event_type: StreamEventType, data: dict[str, Any]) -> dict[str, Any]:
            event = StreamEvent(
                type=event_type, task_id=task_id, session_id=session_id,
                message_id=message_id, sequence=self._streams.next_sequence(task_id),
                data=data,
            )
            self._streams.get_accumulator(task_id).apply(event)
            return {"stream_event": event.to_dict()}

        for step in range(1, limits.max_steps + 1):
            _check_deadline(deadline, limits)

            # `task.progress` in B10's prose -- the existing
            # PROGRESS_UPDATED stream event, so the UI can show
            # "step 3/6" without a new vocabulary.
            yield emit_stream(StreamEventType.PROGRESS_UPDATED, {
                "step": step,
                "max_steps": limits.max_steps,
                "tool_calls_used": tool_calls_used,
                "max_tool_calls": limits.max_tool_calls,
                "message": f"step {step}/{limits.max_steps}",
            })

            request = GenerationRequest(
                model=model, messages=list(messages), effort=effort_enum, tools=tool_schemas,
            )

            requested: list[ToolCall] = []
            # Retry notices are produced inside providers.retry, which
            # cannot yield either. Same deque-and-drain shape as tool
            # events, for the same reason: a rate limit has to be
            # *visible* as progress, or a task that is patiently waiting
            # out a backoff is indistinguishable from a hung one.
            rate_limit_notices: deque[tuple[int, float, str]] = deque()

            async def notice(attempt: int, delay: float, error: str) -> None:
                rate_limit_notices.append((attempt, delay, error))

            def drain_notices():
                while rate_limit_notices:
                    attempt, delay, _error = rate_limit_notices.popleft()
                    yield emit_stream(StreamEventType.PROGRESS_UPDATED, {
                        "step": step,
                        "max_steps": limits.max_steps,
                        "rate_limited": True,
                        "attempt": attempt,
                        "retry_in_s": round(delay, 1),
                        "message": f"rate limited -- retrying in {delay:.0f}s",
                    })

            stream = retry.stream_with_retry(
                lambda: provider.stream(request), on_retry=notice,
            )
            async for provider_chunk in stream:
                for event in drain_notices():
                    yield event

                # Collected off the RAW chunk, before the adapter is
                # consulted: the adapter deliberately returns None for a
                # tool-call chunk (it is a request, not an event), so
                # reading it after the None check would silently drop
                # every tool call the model asked for.
                if provider_chunk.tool_call is not None:
                    requested.append(provider_chunk.tool_call)

                stream_event = self._stream_adapter.adapt(
                    provider_chunk, task_id=task_id, session_id=session_id,
                    message_id=message_id, sequence=self._streams.next_sequence(task_id),
                )
                if stream_event is None:
                    continue
                self._streams.get_accumulator(task_id).apply(stream_event)
                yield {"stream_event": stream_event.to_dict()}

            for event in drain_notices():
                yield event

            if not requested:
                return  # the model answered instead of asking for a tool

            for call in requested:
                _check_deadline(deadline, limits)
                if tool_calls_used >= limits.max_tool_calls:
                    raise BoundsExceededError(
                        f"stopped after {tool_calls_used} tool call(s): this agent's "
                        f"limit is {limits.max_tool_calls}",
                        bound="max_tool_calls", limit=limits.max_tool_calls,
                    )
                tool_calls_used += 1

                # Refuses an ungranted tool by name. The model cannot
                # reach a tool its config does not name, whatever it asks
                # for.
                tool = self._tools.resolve_for(agent_context.config, call.name)

                running = asyncio.ensure_future(tool_executor.execute(
                    tool, call.arguments, agent_context,
                    task_id=task_id, session_id=session_id,
                ))
                try:
                    while True:
                        while pending_events:
                            name, payload = pending_events.popleft()
                            yield emit_stream(_TOOL_EVENT_TYPES[name], payload)
                        if running.done():
                            break
                        await asyncio.wait({running}, timeout=_TOOL_EVENT_POLL_S)
                finally:
                    # A cancelled task must not leave a tool call running
                    # against the filesystem or the network.
                    if not running.done():
                        running.cancel()
                while pending_events:
                    name, payload = pending_events.popleft()
                    yield emit_stream(_TOOL_EVENT_TYPES[name], payload)

                result = running.result()   # re-raises ToolExecutionError / ToolTimeoutError
                # `text` is what the model reads next turn; `data` (the
                # full ToolResult) is what a later reader pulls apart. A
                # refusal is phrased as one, not hidden -- that is what
                # lets the model try a different path instead of
                # repeating the call that was denied.
                messages.append(ToolMessage(
                    session_id, tool.name, result.to_dict(),
                    tool_call_id=call.id, task_id=task_id,
                    text=result.content if result.ok else f"error: {result.error}",
                ))

        raise BoundsExceededError(
            f"stopped after {limits.max_steps} step(s) without a final answer",
            bound="max_steps", limit=limits.max_steps,
        )


_TOOL_EVENT_TYPES = {
    EVENT_STARTED: StreamEventType.TOOL_STARTED,
    EVENT_COMPLETED: StreamEventType.TOOL_COMPLETED,
}


def _check_deadline(deadline: float, limits: Bounds) -> None:
    if time.monotonic() >= deadline:
        raise BoundsExceededError(
            f"stopped after {limits.timeout_s}s: this agent's time limit",
            bound="timeout_s", limit=limits.timeout_s,
        )
