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
import json
import re
import time
import uuid
from collections import deque
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, AsyncIterator, Awaitable, Callable, Optional

import appconfig
from database.proposal_repository import ProposalRepository
from logging_setup import get_logger
from managers import context
from managers.agent import AgentConfig
from managers.context import SessionMemoryService
from managers.editor_context import MODE_FILL, EditorContext
from managers.memory import Proposal
from managers.message import InvocationMetadata, Message, ToolMessage, UserMessage
from managers.project import Project
from managers.project_manager import ProjectManager
from managers.session import Session
from managers.session_manager import SessionManager
from managers.usage import UsageManager
from providers import retry
from providers.models import Effort, GenerationRequest, ToolCall
from providers.registry import ProviderRegistry
from runtime.errors import BoundsExceededError, InvalidEventError, ToolsUnsupportedError
from runtime.scheduler import LaneKey, provider_lane, turn_lane
from runtime.task import EventSink, Task
from runtime.task_manager import TaskManager
from streaming.adapter import StreamAdapter
from streaming.events import StreamEvent, StreamEventType
from streaming.manager import StreamManager
from tools.executor import EVENT_COMPLETED, EVENT_STARTED, ToolExecutor
from tools.models import Tool
from tools.registry import ToolRegistry

_log = get_logger("agents.executor")

# "at most 2-3 per session, enforced by a counter, never by prompt
# instruction" (B11). A number here, checked against a real COUNT(*),
# not a line in a system prompt asking the model to behave.
PROPOSAL_CAP_PER_SESSION = 3

# The Filler's one-file rule (project_idea.md §7's "Diffs, always" --
# "never touches more than one file"), enforced structurally, not only
# by the system prompt. Self-testing against a small local model
# (qwen2.5:1.5b) showed the prompt alone is not enough: asked to
# "refactor this across main.py and utils.py", it happily produced a
# JSON region that silently moved the function into utils.py instead of
# refusing -- see B11's completed doc. A dotted token at least two
# characters long, to keep "e.g." from matching.
_FILE_TOKEN_RE = re.compile(r"\b\w{2,}[\w.\\/-]*\.[A-Za-z0-9]{1,6}\b")

# B24: the only external trace of the region `_render_editor_context`
# (`managers\context.py`, on the B24 Do-NOT list -- read what it
# decided, never touch it) showed the Filler. Its `task_state`
# IncludedItem's `detail` is always `"editor context: {name}
# mode={mode} lines {start}-{end} mtime={mtime} size={size}"` (B36
# added the trailing `mtime=`/`size=` to the same unconditional
# f-string), so these two patterns cover every case that detail string
# can take.
_SHOWN_RANGE_RE = re.compile(r"lines (\d+)-(\d+)")
_REGION_STAT_RE = re.compile(r"mtime=([0-9]+(?:\.[0-9]+)?) size=(\d+)")


def _region_stat_from_built(
    built: context.BuiltContext,
) -> Optional[tuple[int, int, float, int]]:
    """`(shown_start, shown_end, mtime, size)` `_render_editor_context`
    computed for THIS build, recovered from `built.log.included` rather
    than recomputed -- `resolve_scope()` is deterministic given the same
    lines, but the file may change on disk before the terminal event
    this feeds, so the region shown (and the stat taken alongside it,
    B36) must be captured here, not re-asked for later.

    `build()`'s assembly runs its six sections exactly once with no
    loop that could append `SECTION_TASK_STATE` to `log.included` more
    than once, so there is at most one match -- never a "which one"
    question. None (not a raise) when there isn't one at all: a
    path-guard refusal or an unreadable file leaves `task_state` with
    neither "lines" nor "mtime=" text, same as task_state being evicted
    for budget (`EvictedItem.reason` never contains either either). All
    four values or none -- a `detail` that matched one pattern but not
    the other would mean the two were edited out of sync, and that is a
    bug to surface as "nothing to find", not a proposal built from half
    a fact."""
    for item in built.log.included:
        if item.section == context.SECTION_TASK_STATE:
            range_match = _SHOWN_RANGE_RE.search(item.detail)
            stat_match = _REGION_STAT_RE.search(item.detail)
            if range_match is None or stat_match is None:
                return None
            return (
                int(range_match.group(1)), int(range_match.group(2)),
                float(stat_match.group(1)), int(stat_match.group(2)),
            )
    return None


def _mentions_other_file(text: str, current_file: str) -> Optional[str]:
    """The first filename-looking token in `text` that is not
    `current_file`'s own basename, or None. A false positive (an
    unrelated dotted token) costs one unnecessary refusal; a false
    negative is the exact boundary this check exists to hold, so this
    errs toward refusing."""
    current_name = Path(current_file).name.lower()
    for match in _FILE_TOKEN_RE.finditer(text):
        token = match.group(0)
        name = Path(token.replace("\\", "/")).name.lower()
        if name and name != current_name:
            return token
    return None

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

# B19: `timeout_s` is a SILENCE budget, not a wall-clock one -- mirrors
# chat/stream.go's own firstTokenGraceFactor concept on the frontend, which
# this constant is named to match. Waiting on a cold local model being
# loaded from disk is the one silence that is expected, so the budget is
# doubled until the first real provider chunk arrives; after that, ordinary
# `timeout_s` silence applies.
_FIRST_TOKEN_GRACE_FACTOR = 2

# B19's backstop: a PURE silence reset does not, by itself, bound a
# degenerate model that keeps emitting tokens in a tight, meaningless loop
# with no real gap between them -- the exact failure B11 built the original
# per-chunk check to catch (self-tested against qwen2.5:1.5b spiralling into
# an unrelated multi-hundred-line ramble). Neither `max_steps` (bounds the
# number of separate provider.stream() calls, not the length of any one of
# them) nor `max_tool_calls` (unrelated to raw text volume) catches this.
# This ceiling is a TOTAL-duration cap from task start, independent of
# activity -- generous enough that no healthy generation should ever reach
# it (a real research run was observed completing at 3:24 against a 180s
# `timeout_s`, comfortably under research's own 1080s/18min ceiling here),
# but present so a stream that never goes silent also never runs forever.
#
# 6, not something larger: `tests\manual_b11_deadline_check.ps1` exercises
# this exact backstop with `--max-seconds 8` and its own CLI `--timeout 60`
# wait budget. 6x keeps that scenario resolving at 48s — still comfortably
# under 60s, so that existing script keeps working unmodified — while
# staying generous for the real 60s/180s agent configs.
_HARD_CEILING_FACTOR = 6


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
        proposal_repo: Optional[ProposalRepository] = None,
        usage: Optional[UsageManager] = None,
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
        # B14's one spending gate. None-tolerant like proposal_repo above
        # (an executor built without one just never caps anything, the
        # same way an older test constructs this class today).
        self._usage = usage
        # B11's proposal stopgap (managers\memory.py's Proposal, NOT
        # B13's memory_records). None-tolerant: an executor built without
        # one (e.g. an older test) just never writes proposals, the same
        # way a Task with no recorder behaves identically minus the row.
        self._proposal_repo = proposal_repo

        # Fire-and-forget work this file starts that is NOT a TaskManager
        # Task (session title generation, today) -- tracked so
        # Runtime.close() can wait for it instead of `asyncio.run()`
        # cancelling it out from under a short-lived CLI process. Found
        # during B11 self-testing: a background (max_steps > 1) agent's
        # FIRST send in cli.py without --wait let the whole process exit
        # ~1-2s later, killing the title call before its network request
        # returned -- and because the flag guarding "only try once" is
        # set BEFORE the call (so two fast sends cannot both fire it),
        # the session was left permanently untitled instead of retried.
        self._background_tasks: set[asyncio.Task] = set()

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
        editor_context: Optional[EditorContext] = None,
        research_reference_index: Optional[str] = None,
    ) -> AgentRun:
        provider_name, model, effort = resolve_provider_model_effort(
            override=override, session=session, agent=agent,
        )
        resolved_provider = self._resolve_provider_name(provider_name)
        limits = resolve_bounds(bounds, agent)
        granted = self._tools.list_for(agent)

        # B14: the spending cap, checked before ANYTHING else in this
        # method -- before context is built, before the user message is
        # persisted, before a task exists. `echo` is exempt the same way
        # it is exempt from the provider lane below: a dev/test path
        # with no real provider call to protect anyone's wallet from.
        # See managers\usage.py's module docstring for exactly what
        # guarantee this check does and does not make.
        if self._usage is not None and provider_name != "echo":
            await self._usage.check_cap()

        # Session title generation (B11): one cheap, best-effort call on
        # the FIRST exchange only. Fired here, not from the sink, because
        # it needs nothing from the reply -- only the user's own text,
        # already in hand. Fire-and-forget on purpose: "a killed title
        # call leaves the session usable" means this must never be
        # awaited on the reply path, and a failure inside it must never
        # reach the caller.
        if provider_name != "echo" and not (session.settings or {}).get("title_generated"):
            existing = await self._sessions.get_messages(session.id)
            if not existing:
                title_task = asyncio.ensure_future(
                    self._generate_title(session, text, resolved_provider, model)
                )
                self._background_tasks.add(title_task)
                title_task.add_done_callback(self._background_tasks.discard)

        # The Filler's one-file rule, enforced structurally (B11) -- see
        # `_mentions_other_file`'s docstring for why prompting alone was
        # not enough. `refusal_text` is not None only in fill mode with
        # a second filename mentioned; `body_factory` below reuses
        # `_echo_body` to deliver it as a NORMAL reply (TaskDone, no
        # `code`, nothing that renders as an error), never a model call.
        refusal_text: Optional[str] = None
        if editor_context is not None and editor_context.mode == MODE_FILL:
            other = _mentions_other_file(text, editor_context.file)
            if other is not None:
                current_name = Path(editor_context.file).name
                refusal_text = (
                    f"I can only write one region in one file at a time. This "
                    f"request also names {other!r}, but I'm scoped to "
                    f"{current_name!r} here -- ask about one file, or handle "
                    f"{other!r} as a separate request."
                )

        # Refused at INVOCATION, before a task exists and before a token
        # is streamed (B10). A model that silently ignores tools it was
        # never sent produces a confidently wrong answer, which is worse
        # than an error.
        if refusal_text is None and granted and provider_name != "echo":
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
                editor_context=editor_context,
                research_reference_index=research_reference_index,
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

        # B12: the task id is generated BEFORE the user message, and
        # recorded on it.
        #
        # Until now it was generated after (the two lines were the other
        # way round), so a user message never carried a task_id and only
        # the assistant reply did. That made a question impossible to
        # pair with its own answer on reload -- a session with three
        # questions and three answers has no stored link saying which
        # answered which, and ordering is not a link the moment anything
        # runs concurrently, which is the entire premise of this
        # runtime. It is also the prerequisite for ever telling a retry
        # apart from a genuine repeat: same text, same session, and only
        # the task id says whether the user asked twice or the app ran
        # it twice.
        task_id = str(uuid.uuid4())
        message_id = str(uuid.uuid4())

        user_message = UserMessage(session.id, text, task_id=task_id)
        await self._sessions.append_message(user_message)

        # Provenance for whatever this task persists (B12). Registered
        # here, per task, so it is in place before the body can produce
        # anything -- and so the echo and refusal paths, which never
        # register a stream, still have an answer to "which agent
        # produced this".
        #
        # A plain turn records the session's current agent rather than
        # null: B12's own rule is that EVERY message routes through an
        # agent config, plain chat included, so null would not mean
        # "no agent" -- it would mean "we did not write it down".
        self._streams.register_invocation(task_id, InvocationMetadata(
            workspace=session.workspace,
            agent=agent.id,
            provider=resolved_provider,
            model=model,
        ))

        # B24: registered here too, for the same reason as the
        # invocation above -- the echo/refusal path never registers a
        # stream, and a fill-mode refusal (this file's own
        # `refusal_text`) must still be reachable by the terminal
        # event's parse gate. Gated on MODE_FILL only: an explain-mode
        # or plain chat turn must never be parsed at all, whatever its
        # reply looks like.
        if editor_context is not None and editor_context.mode == MODE_FILL:
            region_stat = _region_stat_from_built(built)
            if region_stat is not None:
                shown_start, shown_end, mtime, size = region_stat
                self._streams.register_editor_context(
                    task_id, editor_context, shown_start, shown_end, mtime, size,
                )
            else:
                _log.warning(
                    "Task %s: fill turn's shown line range (or its stat) "
                    "could not be recovered from the context build; no "
                    "FillProposal will be parsed for this turn",
                    task_id,
                )

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
            if refusal_text is not None:
                # Same shape as the echo dev-path (word-chunked, no
                # `stream_event`), which the sink already treats as a
                # normal completed reply -- no model call, no `code`,
                # nothing that reaches the client as an error.
                return self._echo_body(refusal_text)
            if provider_name == "echo":
                # Dev/test convenience: no provider/network required.
                # Not a real registered provider on purpose.
                return self._echo_body(text)
            return self._loop_body(
                resolved_provider, model, effort, agent_context, limits,
                task_id, session.id, message_id,
            )

        sink = make_sink(message_id)
        # B12's two lanes. Every task takes a slot in its provider's
        # lane; only an INLINE conversational turn also takes the
        # session's turn lane. That asymmetry is "agent tasks never
        # enter this queue", expressed as the one line that decides it:
        # three background research runs can saturate Ollama without
        # costing the user the ability to ask a question, and a slow
        # question cannot jump ahead of research already waiting.
        #
        # The echo provider is exempt from the provider lane entirely --
        # it is a local dev path with no resource to contend for, and
        # capping it would make the mock slower than the real thing.
        lanes: list[LaneKey] = []
        if resolved_provider != "echo":
            lanes.append(provider_lane(resolved_provider))
        if inline:
            lanes.append(turn_lane(session.id))

        task = self._tasks.create_task(
            body=body_factory,
            session_id=session.id,
            kind="chat",
            sink=sink,
            task_id=task_id,
            agent_id=agent.id,
            lanes=lanes,
        )
        await self._sessions.set_active_task(session.id, task.id)
        return AgentRun(task=task, message_id=message_id)

    async def drain_background(self, timeout: float = 5.0) -> None:
        """Wait (bounded) for fire-and-forget work started by `run()` --
        session title generation today -- to finish. `Runtime.close()`
        calls this before it closes the database, for the same reason it
        now waits on cancelled Tasks rather than a bare `sleep(0)`: a
        short-lived CLI process's `asyncio.run()` cancels whatever is
        still pending the moment its own coroutine returns, and this is
        the difference between a title call finishing and one silently
        losing its one attempt."""
        pending = list(self._background_tasks)
        if not pending:
            return
        try:
            await asyncio.wait_for(
                asyncio.gather(*pending, return_exceptions=True), timeout=timeout,
            )
        except asyncio.TimeoutError:
            _log.warning("Timed out waiting for %d background task(s) to finish", len(pending))

    # -- session title generation (B11) ------------------------------------

    async def _generate_title(
        self, session: Session, text: str, provider_name: str, model: str,
    ) -> None:
        """One cheap, non-streaming call producing a short title.

        The flag is set BEFORE the call, not after: two sends arriving
        close together can both observe "no messages yet" (the user
        message for the first one is not persisted until later in
        `run()`), and claiming the "first exchange" slot immediately is
        what stops both from firing. Any failure below is logged and
        swallowed -- "a killed title call leaves the session usable"
        means an untitled session is a fine outcome, never a retried one.

        No "small model" tier exists anywhere in this codebase yet
        (`providers\\models.py` has no such flag), so this reuses the
        session's own resolved provider/model rather than inventing a
        selection mechanism nothing else needs. `echo` never reaches
        here (see the `provider_name != "echo"` guard in `run()`) --
        it is not a registered provider and has no `.generate()`.
        """
        try:
            await self._sessions.update_settings(session.id, {"title_generated": True})
            provider = self._providers.get(provider_name)
            request = GenerationRequest(
                model=model,
                messages=[UserMessage(
                    session.id,
                    "Reply with only a short 3-6 word title (no quotes, no "
                    "trailing punctuation) for a chat session that starts "
                    "with this message:\n\n" + text[:500],
                )],
            )
            response = await asyncio.wait_for(provider.generate(request), timeout=30.0)
            title = response.text.strip().strip('"').strip("'")
            title = title.splitlines()[0][:60] if title else ""
            if title:
                await self._sessions.rename(session.id, title)
        except Exception:  # noqa: BLE001 - best-effort, never fatal
            _log.debug("Session title generation failed for %s", session.id, exc_info=True)

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
        """Placeholder task body for the no-network `echo` provider.

        Chunked on whitespace RUNS via re.finditer, not `str.split()`'s
        words. `split()` with no argument discards every separator it
        splits on, so a blank line ("\\n\\n"), a fenced code block's
        indentation, and a single space were all indistinguishable by the
        time a chunk left here -- every run of whitespace collapsed to the
        one literal " " each yielded word was reassembled with. That
        corruption was in the stored message itself, before F3 or
        anything else downstream ever touched it (see F03-chat-viewport.md's
        completion notes for how it was found).

        `\\S+|\\s+` alternates non-whitespace and whitespace runs, each
        yielded verbatim, so concatenating every chunk reconstructs `text`
        exactly. Only a non-whitespace chunk gets the pacing sleep — a run
        of blank lines streaming instantly does not need to feel typed.
        """
        for m in re.finditer(r"\S+|\s+", text):
            piece = m.group()
            if piece.strip():
                await asyncio.sleep(0.05)
            yield {"text": piece}

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

        # B19: `last_activity` replaces the old fixed `deadline`. It is
        # RESET by `touch()` below at every point this loop already treats
        # as "the task is alive" -- a provider chunk, a tool event, a
        # rate-limit retry notice -- so `_check_deadline` only ever fires on
        # genuine silence. `hard_deadline` is the one thing that is NOT
        # reset; see `_HARD_CEILING_FACTOR`'s own doc for why it still has
        # to exist.
        last_activity = time.monotonic()
        first_token_seen = False
        hard_deadline = last_activity + limits.timeout_s * _HARD_CEILING_FACTOR

        def touch(first_token: bool = False) -> None:
            nonlocal last_activity, first_token_seen
            last_activity = time.monotonic()
            if first_token:
                first_token_seen = True

        def emit_stream(event_type: StreamEventType, data: dict[str, Any]) -> dict[str, Any]:
            event = StreamEvent(
                type=event_type, task_id=task_id, session_id=session_id,
                message_id=message_id, sequence=self._streams.next_sequence(task_id),
                data=data,
            )
            self._streams.get_accumulator(task_id).apply(event)
            return {"stream_event": event.to_dict()}

        for step in range(1, limits.max_steps + 1):
            _check_deadline(last_activity, first_token_seen, hard_deadline, limits)

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
                    # A rate-limited retry is time passing without a
                    # provider chunk, but the task is actively managing a
                    # known, bounded wait -- not dead air. Same reasoning as
                    # a tool call in flight (below): evidence of life, even
                    # though no content arrived.
                    touch()
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
            # B19: iterated by hand rather than with `async for`, because
            # the silence budget has to be able to fire WHILE WAITING for
            # the next chunk -- which `async for` cannot do. It blocks in
            # `__anext__` until the provider speaks, so the only moment a
            # per-chunk check can run is the moment the silence ENDS, and a
            # check that fires then kills a task at the instant it proved
            # itself alive. That is not a hypothetical: it is exactly how a
            # cold Ollama load died at its first token, with nothing yet in
            # the accumulator -- so `finalize_interrupted` returned None
            # (its documented empty-text policy), no assistant row was
            # written at all, and the outcome/error_code columns the whole
            # three-way split depends on were never reached. Timing out
            # from the WAIT instead means whatever arrived BEFORE the gap
            # is already accumulated and persists with its outcome intact.
            chunks = stream.__aiter__()
            while True:
                try:
                    provider_chunk = await asyncio.wait_for(
                        chunks.__anext__(), timeout=_silence_budget(first_token_seen, limits),
                    )
                except StopAsyncIteration:
                    break
                except asyncio.TimeoutError:
                    raise _silence_exceeded(first_token_seen, limits) from None

                # A chunk in hand IS the sign of life, so it touches before
                # anything else. The check that remains here is the hard
                # ceiling (B11's runaway-spiral defense, which a silence
                # reset alone cannot provide) -- see `_check_deadline`.
                touch(first_token=True)
                _check_deadline(last_activity, first_token_seen, hard_deadline, limits)
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
                # Config-driven, not an `if agent.id == ...`: any agent
                # whose config grants `writes_proposals` gets this, and
                # today that is only `research` (B11's builtins.py).
                if agent_context.config.writes_proposals:
                    await self._maybe_propose(agent_context, provider, model, task_id, session_id)
                return  # the model answered instead of asking for a tool

            for call in requested:
                _check_deadline(last_activity, first_token_seen, hard_deadline, limits)
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
                            touch()  # a tool.started/tool.completed IS the task, working
                            yield emit_stream(_TOOL_EVENT_TYPES[name], payload)
                        if running.done():
                            break
                        await asyncio.wait({running}, timeout=_TOOL_EVENT_POLL_S)
                        # B19: a tool call in flight is known, bounded work
                        # (ToolExecutor has its own internal timeout — B10's
                        # "validate, run, time out, emit") even when it
                        # emits no intermediate event of its own. Touching
                        # every poll tick is what keeps a single slow tool
                        # call (a web search, say) from tripping the OUTER
                        # silence budget while the tool's own timeout is the
                        # thing actually governing it.
                        touch()
                finally:
                    # A cancelled task must not leave a tool call running
                    # against the filesystem or the network.
                    if not running.done():
                        running.cancel()
                while pending_events:
                    name, payload = pending_events.popleft()
                    touch()
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

    # -- proposals (B11) ----------------------------------------------------

    async def _maybe_propose(
        self,
        agent_context: AgentContext,
        provider: Any,
        model: str,
        task_id: str,
        session_id: str,
    ) -> None:
        """Extraction plus the ≤3-per-session cap. Writes through the
        narrow `agent_proposals` table (`managers\\memory.py`'s
        `Proposal`, `database\\proposal_repository.py`) -- NOT
        `SessionMemoryService.propose()`, which stays `NotImplementedError`
        for B13's real `memory_records`.

        Best-effort and never fatal: a completed Research task that
        recorded no proposals is a normal outcome, not a reason to fail
        a turn that already succeeded. Only reached on a clean finish
        (the caller does not call this on `bounds_exceeded` or an
        exception) -- a run that stopped short of an answer is not
        "complete work" to extract durable facts from.
        """
        if self._proposal_repo is None:
            return
        answer = self._streams.get_accumulator(task_id).text
        if not answer.strip():
            return
        try:
            remaining = PROPOSAL_CAP_PER_SESSION - await self._proposal_repo.count_for_session(session_id)
            if remaining <= 0:
                return
            request = GenerationRequest(
                model=model,
                messages=[UserMessage(
                    session_id,
                    f"From the answer below, list at most {remaining} short, "
                    "standalone fact(s) worth remembering for future "
                    "sessions -- durable facts only, not the specifics of "
                    "this one conversation. Reply with a JSON array of "
                    "strings, or [] if nothing is worth keeping. No other "
                    "text.\n\n" + answer[:4000],
                )],
            )
            # Bounded like every other network call this file makes --
            # this runs before the task's own "done" is emitted, so a
            # hang here must not become a hang in the reply the user is
            # already waiting on.
            response = await asyncio.wait_for(provider.generate(request), timeout=30.0)
            facts = _parse_fact_list(response.text)[:remaining]
            for fact in facts:
                await self._proposal_repo.insert(Proposal.new(
                    session_id=session_id, content=fact, task_id=task_id,
                    agent_id=agent_context.config.id,
                ))
            if facts:
                _log.info(
                    "Recorded %d proposal(s) for session %s (agent %s)",
                    len(facts), session_id, agent_context.config.id,
                )
        except Exception:  # noqa: BLE001 - proposals are best-effort, never fatal
            _log.debug("Proposal extraction failed for task %s", task_id, exc_info=True)


_TOOL_EVENT_TYPES = {
    EVENT_STARTED: StreamEventType.TOOL_STARTED,
    EVENT_COMPLETED: StreamEventType.TOOL_COMPLETED,
}


def _parse_fact_list(text: str) -> list[str]:
    """Parse the extraction call's reply as a JSON array of strings.
    One salvage attempt for a model that wraps the array in prose or a
    code fence despite being told not to; anything else is treated as
    "nothing worth keeping" rather than raised -- this is the model's
    output, not user input, and failing to parse it must never fail the
    task that already completed."""
    text = text.strip()
    if not text:
        return []
    data = _try_json(text)
    if data is None:
        start, end = text.find("["), text.rfind("]")
        if start != -1 and end != -1 and end > start:
            data = _try_json(text[start:end + 1])
    if not isinstance(data, list):
        return []
    return [item.strip() for item in data if isinstance(item, str) and item.strip()]


def _try_json(text: str) -> Any:
    try:
        return json.loads(text)
    except (ValueError, TypeError):
        return None


def _check_deadline(
    last_activity: float, first_token_seen: bool, hard_deadline: float, limits: Bounds,
) -> None:
    """B19: silence-based, not wall-clock. Two independent checks:

    - The hard ceiling (`_HARD_CEILING_FACTOR × timeout_s` from task start,
      never reset) is the runaway-spiral backstop -- see that constant's own
      doc for why it is still needed even though the ordinary check below no
      longer fires on elapsed time alone.
    - The silence budget (`timeout_s`, doubled until the first provider
      chunk arrives) is the real fix: it only fires on genuine dead air, so
      a task that keeps producing output -- however long that takes -- is
      never interrupted by this check.

    Both raise the SAME `bound="timeout_s"` `BoundsExceededError` runtime.py's
    `_outcome_for` already maps to `Outcome.TIMED_OUT` -- this episode changes
    WHEN the check fires, not what happens once it does (F7's own scope note).
    """
    now = time.monotonic()
    if now >= hard_deadline:
        raise BoundsExceededError(
            f"stopped after {limits.timeout_s * _HARD_CEILING_FACTOR}s total: "
            "this agent never finished, and never went quiet enough to time "
            "out on its own",
            bound="timeout_s", limit=limits.timeout_s,
        )
    if now - last_activity >= _silence_budget(first_token_seen, limits):
        raise _silence_exceeded(first_token_seen, limits)


def _silence_budget(first_token_seen: bool, limits: Bounds) -> int:
    """How long dead air may last before the task is ended. Doubled until
    the first provider chunk arrives -- a cold local model being loaded
    from disk is the one silence that is expected."""
    return limits.timeout_s * (1 if first_token_seen else _FIRST_TOKEN_GRACE_FACTOR)


def _silence_exceeded(first_token_seen: bool, limits: Bounds) -> BoundsExceededError:
    """ONE wording and ONE `bound` for a silence timeout, whether it was
    noticed by a check between steps or by the stream wait itself timing
    out. Two constructions would be two chances for the `bound="timeout_s"`
    that runtime.py's `_outcome_for` keys `Outcome.TIMED_OUT` on to drift
    apart -- and the outcome columns going missing is precisely the failure
    this episode's own verification caught."""
    return BoundsExceededError(
        f"stopped after {_silence_budget(first_token_seen, limits)}s of silence: "
        "this agent's time limit",
        bound="timeout_s", limit=limits.timeout_s,
    )
