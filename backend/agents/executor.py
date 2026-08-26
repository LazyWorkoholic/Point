r"""AgentExecutor: the one execution path every message goes through
(project_idea.md §2). Config-driven -- this file must never grow an
`if agent.id == ...`.

    resolve config
      -> resolve provider/model/effort  (task > session > agent > global)
      -> build context (B8) with the agent's budget
      -> create a Task (B1 TaskManager)
      -> stream through the provider (B4) via the streaming manager (B5)
      -> emit events
      -> persist the assistant message

`inline` vs `background` is a property of the INVOCATION, not the
agent -- callers pass it. Nothing here decides it from `AgentConfig`,
and nothing here reads it yet either: v1 has exactly one call site
(`Runtime._handle_send_message`) and it is always inline. The parameter
exists now so a background caller (Research, B11) is a new call site,
not a rewiring of this one.

What this file must never do, same as B8's context builder:

  - Hold a database handle of its own. It takes `SessionManager` and
    `SessionMemoryService` -- both already keep SQL behind them.
  - Implement tool calls or a multi-step loop (B10/B11). `body_factory`
    below is a single provider round-trip, exactly what
    `Runtime._stream_chat_body` was before this file existed.
  - Decide *which* agent a session uses. That is
    `AgentRegistry.resolve_for_session()`'s job; this file is hand
    -ed an already-resolved `AgentConfig`.
"""

from __future__ import annotations

import asyncio
import uuid
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Awaitable, Callable, Optional

import appconfig
from logging_setup import get_logger
from managers import context
from managers.agent import AgentConfig
from managers.context import SessionMemoryService
from managers.message import Message, UserMessage
from managers.session import Session
from managers.session_manager import SessionManager
from providers.models import Effort, GenerationRequest
from providers.registry import ProviderRegistry
from runtime.errors import InvalidEventError
from runtime.task import EventSink, Task
from runtime.task_manager import TaskManager
from streaming.adapter import StreamAdapter
from streaming.manager import StreamManager

_log = get_logger("agents.executor")

# The no-op guardrails hook (B9's "Middleware chain" build item). Typed
# against the plain dicts/Messages already flowing through this file
# rather than a new envelope -- B10's tool loop is the first thing that
# will need either signature to carry more, and that is when it should
# change, not before.
RequestMiddleware = Callable[[dict[str, Any]], Awaitable[Optional[dict[str, Any]]]]
ResponseMiddleware = Callable[[Message], Awaitable[Optional[Message]]]

# `make_sink(message_id) -> EventSink`. The sink itself stays Runtime's:
# it is `_emit`/envelope-filling machinery, which is transport, not
# agent execution. This file only needs to hand it the message_id it
# generated, so the stream's events and the eventual persisted message
# agree on one id.
SinkFactory = Callable[[str], EventSink]


@dataclass(frozen=True)
class ProviderOverride:
    """The top rung of the resolution chain -- a per-task override.
    Nothing on the wire sets one yet (`SendMessage` carries no override
    fields in v1), but the chain has all four rungs from day one rather
    than growing a fourth parameter into `run()` the day something
    needs it."""

    provider: Optional[str] = None
    model: Optional[str] = None
    effort: Optional[str] = None


@dataclass
class AgentContext:
    """What a running agent may touch (B9's Build section, verbatim).
    No database handle, no provider client of its own -- both stay
    behind the executor. `tools` is B10's, empty here."""

    config: AgentConfig
    session_id: str
    project_id: Optional[str]
    built: context.BuiltContext
    tools: dict[str, Any] = field(default_factory=dict)
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
    ) -> None:
        self._sessions = sessions
        self._memory = memory
        self._tasks = tasks
        self._providers = providers
        self._streams = streams
        self._stream_adapter = stream_adapter
        self._resolve_provider_name = resolve_provider_name

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
    ) -> AgentRun:
        provider_name, model, effort = resolve_provider_model_effort(
            override=override, session=session, agent=agent,
        )
        resolved_provider = self._resolve_provider_name(provider_name)

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

        def body_factory() -> AsyncIterator[dict[str, Any]]:
            if provider_name == "echo":
                # Dev/test convenience: no provider/network required.
                # Not a real registered provider on purpose.
                return self._echo_body(text)
            return self._stream_body(
                resolved_provider, model, effort, built.messages,
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

    async def _stream_body(
        self,
        provider_name: str,
        model: str,
        effort: Optional[str],
        messages: list[Message],
        task_id: str,
        session_id: str,
        message_id: str,
    ) -> AsyncIterator[dict[str, Any]]:
        """Cancellation note carried over from `Runtime._stream_chat_body`:
        when Task.cancel() fires, CancelledError is raised at whichever
        await this generator is suspended on -- almost always inside
        provider.stream()'s own `async for`. Every provider wraps its SDK
        client in `async with`, so that context manager's __aexit__ runs
        during this generator's teardown and closes the underlying
        connection -- cancelling a task genuinely stops the network
        stream, not just the UI.
        """
        self._streams.register_stream(task_id=task_id, session_id=session_id, message_id=message_id)
        provider = self._providers.get(provider_name)  # ProviderNotRegisteredError -> clean Error event

        effort_enum: Optional[Effort] = None
        if effort:
            try:
                effort_enum = Effort(effort)
            except ValueError:
                _log.warning("Unknown effort %r for provider %s; ignoring", effort, provider_name)

        request = GenerationRequest(model=model, messages=messages, effort=effort_enum)

        async for provider_chunk in provider.stream(request):
            stream_event = self._stream_adapter.adapt(
                provider_chunk, task_id=task_id, session_id=session_id,
                message_id=message_id, sequence=self._streams.next_sequence(task_id),
            )
            if stream_event is None:
                continue
            self._streams.get_accumulator(task_id).apply(stream_event)
            yield {"stream_event": stream_event.to_dict()}
