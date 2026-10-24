"""Runtime: the coordinator. Owns managers, never does their job."""

from __future__ import annotations

import asyncio
import time
from collections import OrderedDict
from dataclasses import replace
from pathlib import Path
from typing import Any, Optional, Union

import aiosqlite

import appconfig
import appconfig_overrides
from agents.executor import AgentExecutor, BoundsOverride
from agents.registry import AgentRegistry
from database.connection import open_database
from database.event_repository import EventRepository
from database.message_checkpoint_repository import MessageCheckpointRepository
from database.migrations import SCHEMA_VERSION
from database.proposal_repository import ProposalRepository
from database.task_repository import TaskRepository
from database.tool_call_repository import ToolCallRepository
from database.usage_repository import UsageRepository
from logging_setup import get_logger
from managers.agent import AgentConfig
from managers.attachment import MODE_FULL
from managers.attachment_manager import AttachmentManager
from managers import context
from managers.context import SessionMemoryService, estimate_tokens
from managers.invocation import parse_invocation
from managers.usage import UsageManager
from managers.editor_context import EditorContext
from managers.fill_proposal import FillParseFailure, parse_fill_proposal
from managers.message import Message, MessageRole, Outcome
from managers.project import Project
from managers.project_manager import ProjectManager
from managers.session import Session
from managers.session_manager import SessionManager, SessionNotFoundError
from runtime.dispatcher import Dispatcher
from runtime.errors import (
    AdvancedUnavailableError,
    InvalidEventError,
    ProjectNotFoundError,
    QueueFullError,
    RuntimeException,
    TaskNotFoundError,
)
from runtime.events import (
    ENVELOPE_VERSION,
    IncomingEvent,
    IncomingEventType,
    OutgoingEvent,
    OutgoingEventType,
)
from runtime.lifecycle import (
    LiveReferenceRegistry,
    ProjectContext,
    ReleaseReport,
    WorkspaceContext,
)
from runtime.scheduler import turn_lane, turn_queue_depth
from runtime.state import RuntimeState, RuntimeStatus
from runtime.task import Task
from runtime.task_manager import TaskManager
from managers.message import AssistantMessage
from dataclasses import asdict
from providers.bootstrap import build_registry_from_env, create_provider
from providers.errors import AuthenticationError, ProviderError, ProviderUnavailableError
from providers.models import ProviderConfig, ProviderHealthStatus
from streaming.adapter import StreamAdapter
from streaming.events import StreamEventType
from streaming.manager import RegisteredEditorContext, StreamManager
from tools.registry import build_default_registry


_log = get_logger("runtime")

# How many requests to remember for envelope echo. Bounded because a
# long-lived backend would otherwise grow one entry per request forever;
# 1000 is far more than the number of requests that can still have events
# in flight.
_CORRELATION_LIMIT = 1000

HEARTBEAT_INTERVAL_S = 5.0

# B15: how often an in-flight assistant reply is written to
# `message_checkpoints` while text is arriving.
#
# Time-based, not per-delta, and that is the whole decision. A local
# model streams tens of deltas a second; a write per delta would put
# tens of serialized SQLite round-trips a second on the token path for a
# row that is overwritten immediately and read only after a crash. 500ms
# bounds the worst-case loss to half a second of text, which is a
# sentence fragment, while costing two writes a second during a stream
# that is already producing far more events than that on the wire.
#
# The first delta checkpoints immediately rather than waiting out the
# first interval: a task killed 200ms in would otherwise leave nothing
# at all, which is exactly the hole this closes.
CHECKPOINT_INTERVAL_S = 0.5

# B15: how long `Shutdown` and `close()` wait for cancelled tasks to
# finish unwinding before giving up on them. A cancelled task mid network
# I/O needs several loop turns to propagate CancelledError up through
# httpx -> httpcore -> anyio and then run the sink's own
# persist-the-partial branch; bounded so a task whose cleanup is stuck
# cannot hang the process forever (the lock outlives a hung process).
TEARDOWN_TIMEOUT_S = 5.0

# B25: a non-healthy health() result, turned into the ProviderError
# subclass that best describes why -- so a bad SetProviderApiKey key
# surfaces the same kind of error a provider would have raised itself
# had the handler called a method that always raises on failure (every
# provider's health() returns a ProviderHealth instead; only some of
# them raise). ProviderError is the fallback for UNHEALTHY, which has
# no single better-fitting subclass.
_HEALTH_FAILURE_ERRORS = {
    ProviderHealthStatus.UNAUTHENTICATED: AuthenticationError,
    ProviderHealthStatus.UNAVAILABLE: ProviderUnavailableError,
    ProviderHealthStatus.UNHEALTHY: ProviderError,
}


def _parse_optional_int(value: Any, event_name: str, field_name: str) -> Optional[int]:
    """None passes through untouched -- that is the "not given" case
    every B18 payload field uses. Anything else must be a real int, or
    the request is malformed rather than silently coerced."""
    if value is None:
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        raise InvalidEventError(f"{event_name} '{field_name}' must be an integer")


def _parse_optional_positive_int(value: Any, event_name: str, field_name: str) -> Optional[int]:
    parsed = _parse_optional_int(value, event_name, field_name)
    if parsed is not None and parsed <= 0:
        raise InvalidEventError(f"{event_name} '{field_name}' must be a positive integer")
    return parsed


def _outcome_for(kind: str, code: Optional[str], bound: Optional[str]) -> Outcome:
    """Map a terminal chunk to the outcome that gets persisted (B12).

    `bound` is what separates `timed_out` from `bounds_exceeded`. Both
    arrive as BoundsExceededError with `code="bounds_exceeded"`, but they
    mean different things to whoever reads the transcript later:
    `timed_out` says "this took too long", which invites re-running it;
    `bounds_exceeded` says "this agent was allowed N steps and used
    them", which invites raising the bound. A single code cannot carry
    that distinction, which is why `Task._run()` copies the exception's
    `bound` attribute out beside the code.

    Anything unrecognised falls through to `failed`. That is the honest
    default: a new error slug nobody has mapped yet is a failure whose
    kind we do not know, and guessing `done` would be the one answer
    that is definitely wrong.
    """
    if kind == "done":
        return Outcome.DONE
    if kind == "cancelled":
        return Outcome.CANCELLED
    if code == "bounds_exceeded":
        return Outcome.TIMED_OUT if bound == "timeout_s" else Outcome.BOUNDS_EXCEEDED
    if code == "tool_timeout":
        return Outcome.TIMED_OUT
    return Outcome.FAILED


class Runtime:
    def __init__(self, db_conn: aiosqlite.Connection) -> None:
        self.state = RuntimeState()
        self.db = db_conn
        self.task_repo = TaskRepository(db_conn)
        # B15: the partial reply on disk, written every
        # CHECKPOINT_INTERVAL_S while text arrives and deleted the moment
        # the real `messages` row exists. Nothing that reads history can
        # see it -- see migration v7 for why it is a separate table.
        self.checkpoints = MessageCheckpointRepository(db_conn)
        # B40: the durable event log. Every session-scoped envelope that
        # leaves `_emit` is written here, and `EventsSince` reads it back
        # after a reconnect. Constructed before `_register_handlers()`
        # because the handler closes over it.
        #
        # It does NOT hold the token stream -- see
        # `database\event_repository.py` for the policy and the measured
        # reason for it.
        self.events = EventRepository(db_conn)
        # The recorder is how a Task's state transitions reach the `tasks`
        # table. TaskManager still owns the live objects.
        self.tasks = TaskManager(recorder=self._record_task)
        self.sessions = SessionManager(db_conn)
        self.projects = ProjectManager(db_conn)
        # B8: the only way into the context builder's data. The builder
        # never touches SQLite itself.
        self.memory = SessionMemoryService(db_conn)
        # B12: the write half of attachments (attach/detach/set_mode)
        # plus the guards and the pre-send cost estimate. B8's
        # AttachmentRepository stays where it is -- this sits above it.
        self.attachments = AttachmentManager(db_conn)
        self.dispatcher = Dispatcher()
        self.outbox: asyncio.Queue[OutgoingEvent] = asyncio.Queue(maxsize=1000)

        self._register_handlers()
        self.state.status = RuntimeStatus.READY

        self.providers = build_registry_from_env()

        self.streams = StreamManager()
        self.stream_adapter = StreamAdapter()

        # B9: the agent registry (built-ins seeded in Runtime.create(),
        # below -- seeding is async and __init__ cannot be) and the one
        # execution path every SendMessage goes through. AgentExecutor
        # gets managers, never a database handle or its own provider
        # client -- see agents\executor.py's module docstring.
        # B10: the tool registry is built before the agent registry,
        # because seeding an agent now validates its grants against it --
        # an agent naming a tool that does not exist must fail at startup,
        # not at the call that would have used it.
        self.tools = build_default_registry()
        self.tool_calls = ToolCallRepository(db_conn)
        self.agents = AgentRegistry(db_conn, tools=self.tools)
        # B11's narrow proposal stopgap (managers\memory.py's Proposal,
        # migration v4's agent_proposals table) -- NOT B13's
        # memory_records/memory_scopes. See decisions.md D97.
        self.proposals = ProposalRepository(db_conn)
        # B14: usage_records existed since schema v2 but nothing wrote
        # it (see INDEX.md). UsageManager is the one place pricing
        # (config `[pricing]`) and the daily cap (config `[limits]`)
        # are decided; UsageRepository is just storage, same split as
        # AttachmentManager/AttachmentRepository above.
        self.usage_repo = UsageRepository(db_conn)
        self.usage = UsageManager(self.usage_repo)
        self.agent_executor = AgentExecutor(
            self.sessions, self.memory, self.tasks, self.providers,
            self.streams, self.stream_adapter, self._resolve_provider_name,
            projects=self.projects,
            tools=self.tools,
            tool_recorder=self.tool_calls.record,
            proposal_repo=self.proposals,
            usage=self.usage,
        )

        # request id -> the envelope metadata that came in with it, so
        # connection_id / trace_id / session_id can be echoed on every
        # event that request causes without any handler carrying them.
        self._correlation: OrderedDict[str, dict[str, Optional[str]]] = OrderedDict()
        self._heartbeat: Optional[asyncio.Task] = None

        # B15. The registry is DERIVED from TaskManager, not maintained
        # beside it: `_tasks` already is the live set, and a second copy
        # updated by hand on every transition is a second copy that can
        # disagree. See runtime\lifecycle.py for the per-resource-class
        # refusal rule and why `is_active()` is the wrong input for it.
        self.live_refs = LiveReferenceRegistry(self.tasks.list_all)
        # monotonic deadline per task: when its next checkpoint is due.
        self._checkpoint_at: dict[str, float] = {}
        # Set by `Shutdown` once its ack is on the outbox. `main.py`
        # waits on it alongside the pipes, because `Shutdown` quiesces
        # from the PROTOCOL side and stdin is still open -- nothing else
        # would ever end the process.
        self.shutdown_requested: asyncio.Event = asyncio.Event()
        self._torn_down = False

    _PROVIDER_ALIASES = {"claude": "anthropic", "gpt": "openai", "chatgpt": "openai"}

    def _resolve_provider_name(self, name: str) -> str:
        return self._PROVIDER_ALIASES.get(name, name)

    @classmethod
    async def create(cls, db_path: Union[str, Path]) -> "Runtime":
        """db_path has no default any more: the default used to be the
        literal "runtime.db", which resolved to wherever the process
        happened to be standing. bootstrap.resolve_db_path() is the only
        thing that decides where the database lives."""
        conn = await open_database(db_path)
        runtime = cls(conn)
        # Built-ins are seeded from code on every startup (B9) -- async,
        # so it cannot happen in __init__. Upserted by id: a stale row
        # from an older build is overwritten, not accumulated.
        await runtime.agents.seed_builtins()
        await runtime.agents.validate_all()
        return runtime

    async def close(self) -> None:
        """Cancel what is running, stop the heartbeat, close the
        database. Idempotent.

        B15 made this and the `Shutdown` handler the same code path
        rather than two that drift: `Shutdown` is this sequence entered
        from the protocol side instead of from the OS, plus one ack.
        Both go through `_teardown()`, and the `_torn_down` guard is why
        `shutdown_runtime()` running after a `Shutdown` is a no-op
        instead of a second attempt to close a closed database.
        """
        await self._teardown()

    async def _teardown(self) -> None:
        """Quiesce, release, close -- once. See close() and
        _handle_shutdown()."""
        if self._torn_down:
            return
        self._torn_down = True
        self.state.status = RuntimeStatus.STOPPING
        if self._heartbeat is not None:
            self._heartbeat.cancel()
            try:
                await self._heartbeat
            except asyncio.CancelledError:
                pass
            self._heartbeat = None

        # B11: fire-and-forget work that is not a TaskManager Task at
        # all (session title generation) gets the same courtesy, before
        # the cancel-and-wait below -- it is not something we asked to
        # stop, only something a short CLI process would otherwise cut
        # off mid-flight. See AgentExecutor.drain_background()'s
        # docstring for the failure this closes.
        await self.agent_executor.drain_background()

        active = self.tasks.list_active()
        for task in active:
            task.cancel()
        # B11 exposed the bug a bare `asyncio.sleep(0)` here was hiding:
        # a task cancelled mid network I/O does not finish unwinding in
        # one loop turn. CancelledError has to propagate up through
        # however many awaited layers it was suspended in (httpx ->
        # httpcore -> anyio for every real provider), each needing its
        # own turn -- and the sink's own cancelled-branch handling
        # (persisting the partial message, `set_active_task`) is more
        # awaits on top of that. One turn was enough for nothing to have
        # run yet before this method reached `self.db.close()` two lines
        # down, which raised `ValueError: no active connection` from
        # inside the task's own cleanup instead of letting it finish
        # cleanly. `Task.wait()` awaits the real asyncio.Task and
        # swallows the CancelledError it raises back at its own
        # awaiter -- close() already knows it cancelled these; it only
        # needs the guarantee that cleanup actually completed. Bounded,
        # so a task whose cleanup is genuinely stuck cannot hang
        # shutdown forever.
        if active:
            try:
                await asyncio.wait_for(
                    asyncio.gather(*(task.wait() for task in active)),
                    timeout=TEARDOWN_TIMEOUT_S,
                )
            except asyncio.TimeoutError:
                _log.warning(
                    "Timed out waiting for %d cancelled task(s) to finish during shutdown",
                    len(active),
                )

        # B12: the scheduler's lane-release watchers, drained for the
        # same reason as everything above -- a watcher killed by
        # `asyncio.run()` returning is one whose `finally` never ran, so
        # its `_record()`-adjacent work is cut off mid-flight against a
        # database that is about to close. Nothing is waiting on a slot
        # by this point (every task was just cancelled), so this is a
        # short wait in practice.
        await self.tasks.scheduler.drain()

        # B15: release contexts before the database goes. Nothing is
        # running by this point, so every refusal rule in
        # runtime\lifecycle.py passes trivially -- which is the honest
        # order: release what is free, having first made it free.
        report = await self.release_all()
        _log.info("Teardown release: %s", report.describe())

        try:
            await self.db.close()
        except Exception as exc:  # noqa: BLE001
            _log.warning("Error closing database: %s", exc)
        self.state.status = RuntimeStatus.STOPPED

    # -- release (B15) ----------------------------------------------------

    async def _close_provider_pool(self, name: str) -> int:
        """One provider's pool, closed through the provider's own
        `close()` so an adapter that holds nothing reports 0 rather than
        pretending. Never raises into the report: a vendor SDK that
        throws on close must not take the rest of an unload with it."""
        try:
            provider = self.providers.get(name)
        except RuntimeException:
            return 0
        return await provider.close()

    async def _sessions_for_workspace(self, workspace_id: str) -> list[Session]:
        """Which sessions a workspace label covers.

        `sessions.workspace` is the only place a workspace id appears
        anywhere in the backend, and it is a nullable grouping label --
        nothing is keyed by it. Resolving it here is what lets Go keep
        asking in the vocabulary it has ("unload chat") without the
        backend inventing a workspace registry it has no other use for.

        Archived sessions are included deliberately: an archived
        session's project index is exactly the kind of thing an unload
        should be free to drop.
        """
        sessions = await self.sessions.list_all(include_archived=True)
        return [s for s in sessions if (s.workspace or "") == workspace_id]

    def _workspace_context(
        self, workspace_id: str, sessions: list[Session],
    ) -> WorkspaceContext:
        return WorkspaceContext(
            workspace_id,
            projects=self.projects,
            registry=self.live_refs,
            session_ids=[s.id for s in sessions],
            project_ids=[s.project_id for s in sessions if s.project_id],
            provider_names=self.providers.list(),
            close_provider=self._close_provider_pool,
        )

    def project_context(self, project_id: str, session_ids: list[str]) -> ProjectContext:
        """B15's `ProjectContext`, reachable on its own as well as
        through a workspace: B16 gives each SSH connection its own
        runtime with this same lifecycle, and a per-project release it
        can call directly is what that needs."""
        return ProjectContext(project_id, self.projects, self.live_refs, session_ids)

    async def release_all(self) -> ReleaseReport:
        """Every project index and every provider pool, subject to the
        same refusal rule as a workspace unload. Used by teardown, and
        by `bench unload-cycles` as the "unload everything" half of a
        cycle."""
        sessions = await self.sessions.list_all(include_archived=True)
        context = WorkspaceContext(
            "*",
            projects=self.projects,
            registry=self.live_refs,
            session_ids=[s.id for s in sessions],
            project_ids=[s.project_id for s in sessions if s.project_id],
            provider_names=self.providers.list(),
            close_provider=self._close_provider_pool,
            # Explicit: "release everything" means the pools too, even
            # on a profile with no sessions at all, which is the one
            # case the per-workspace default declines.
            include_provider_pools=True,
        )
        return await context.close()

    def _register_handlers(self) -> None:
        self.dispatcher.register(IncomingEventType.SEND_MESSAGE, self._handle_send_message)
        self.dispatcher.register(IncomingEventType.CANCEL_TASK, self._handle_cancel_task)
        self.dispatcher.register(IncomingEventType.LIST_TASKS, self._handle_list_tasks)
        self.dispatcher.register(IncomingEventType.SHUTDOWN, self._handle_shutdown)
        self.dispatcher.register(IncomingEventType.CREATE_SESSION, self._handle_create_session)
        self.dispatcher.register(IncomingEventType.GET_SESSION, self._handle_get_session)
        self.dispatcher.register(IncomingEventType.SESSION_MESSAGES, self._handle_session_messages)
        self.dispatcher.register(IncomingEventType.LIST_SESSIONS, self._handle_list_sessions)
        self.dispatcher.register(IncomingEventType.RENAME_SESSION, self._handle_rename_session)
        self.dispatcher.register(IncomingEventType.ARCHIVE_SESSION, self._handle_archive_session)
        self.dispatcher.register(IncomingEventType.UNARCHIVE_SESSION, self._handle_unarchive_session)
        self.dispatcher.register(IncomingEventType.DELETE_SESSION, self._handle_delete_session)
        self.dispatcher.register(IncomingEventType.UPDATE_SESSION_METADATA, self._handle_update_metadata)
        self.dispatcher.register(IncomingEventType.UPDATE_SESSION_SETTINGS, self._handle_update_settings)
        self.dispatcher.register(IncomingEventType.LIST_PROVIDERS, self._handle_list_providers)
        self.dispatcher.register(IncomingEventType.LIST_PROVIDER_MODELS, self._handle_list_provider_models)
        self.dispatcher.register(IncomingEventType.SET_PROVIDER_API_KEY, self._handle_set_provider_api_key)
        self.dispatcher.register(IncomingEventType.SET_SESSION_PROVIDER, self._handle_set_session_provider)
        self.dispatcher.register(IncomingEventType.ATTACH_CONTEXT, self._handle_attach_context)
        self.dispatcher.register(IncomingEventType.DETACH_CONTEXT, self._handle_detach_context)
        self.dispatcher.register(IncomingEventType.SET_ATTACHMENT_MODE, self._handle_set_attachment_mode)
        self.dispatcher.register(IncomingEventType.LIST_ATTACHMENTS, self._handle_list_attachments)
        self.dispatcher.register(IncomingEventType.RESOLVE_ATTACHMENT, self._handle_resolve_attachment)
        self.dispatcher.register(IncomingEventType.GET_CONTEXT_REPORT, self._handle_get_context_report)
        self.dispatcher.register(IncomingEventType.PROJECT_INIT, self._handle_project_init)
        self.dispatcher.register(IncomingEventType.PROJECT_RESOLVE, self._handle_project_resolve)
        self.dispatcher.register(IncomingEventType.LIST_PROJECT_FILES, self._handle_list_project_files)
        self.dispatcher.register(IncomingEventType.READ_PROJECT_FILE, self._handle_read_project_file)
        self.dispatcher.register(IncomingEventType.WRITE_PROJECT_FILE, self._handle_write_project_file)
        self.dispatcher.register(IncomingEventType.ACQUIRE_FILE_LOCK, self._handle_acquire_file_lock)
        self.dispatcher.register(IncomingEventType.HEARTBEAT_FILE_LOCK, self._handle_heartbeat_file_lock)
        self.dispatcher.register(IncomingEventType.RELEASE_FILE_LOCK, self._handle_release_file_lock)
        self.dispatcher.register(IncomingEventType.TAKE_OVER_FILE_LOCK, self._handle_take_over_file_lock)
        self.dispatcher.register(IncomingEventType.CHECK_FILE_STALE, self._handle_check_file_stale)
        self.dispatcher.register(IncomingEventType.LIST_AGENTS, self._handle_list_agents)
        self.dispatcher.register(IncomingEventType.LIST_DIRECTORY, self._handle_list_directory)
        self.dispatcher.register(IncomingEventType.SET_BEHAVIOUR_OVERRIDE, self._handle_set_behaviour_override)
        self.dispatcher.register(IncomingEventType.GET_DOCTOR_REPORT, self._handle_get_doctor_report)
        self.dispatcher.register(IncomingEventType.WORKSPACE_UNLOAD, self._handle_workspace_unload)
        self.dispatcher.register(IncomingEventType.EVENTS_SINCE, self._handle_events_since)

    async def receive(self, raw: dict[str, Any]) -> None:
        try:
            event = IncomingEvent.from_dict(raw)
        except InvalidEventError as exc:
            # corr_id comes from the envelope's `id` now. A malformed
            # envelope still gets an answer it can be routed by, if it
            # had an id at all.
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.ERROR,
                request_id=raw.get("id", "unknown"),
                connection_id=raw.get("connection_id"),
                trace_id=raw.get("trace_id"),
                payload={"error": str(exc)},
            ))
            return

        self._remember_envelope(event)

        if not self.state.is_accepting_events() and event.type != IncomingEventType.SHUTDOWN:
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.ERROR,
                request_id=event.request_id,
                payload={"error": f"Runtime not ready (status={self.state.status})"},
            ))
            return

        try:
            await self.dispatcher.dispatch(event)
        except RuntimeException as exc:
            # B12: `code` rides beside `error`. `queue_full` in
            # particular is not a message to show and forget -- the UI
            # locks its input box on it and has to say why, and a client
            # that has to regex an English sentence to decide that is a
            # client coupled to the sentence. Every RuntimeException
            # subclass that carries a slug exposes it as `code`; the
            # ones that do not send null, exactly as before.
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.ERROR,
                request_id=event.request_id,
                payload={
                    "error": str(exc),
                    "code": getattr(exc, "code", None),
                    # B12: a refusal that offers a fallback has to carry
                    # the offer, or the client is left inventing its own
                    # advice -- which is how a CLI ends up printing a
                    # flag that no longer exists.
                    "offer": getattr(exc, "offer", None) or None,
                },
            ))

    # -- session handlers -----------------------------------------------

    async def _handle_create_session(self, event: IncomingEvent) -> None:
        """No provider/model default. The old literals ("claude" /
        "claude-sonnet-5") made every session claim a vendor whose key
        might not exist -- and "claude" resolves to anthropic, which does
        not even register on a keyless profile. NULL means "inherit from
        global config"; B8 is what resolves it."""
        p = event.payload
        project_id = p.get("project_id")
        if project_id and not await self.projects.exists(project_id):
            _log.warning(
                "CreateSession: project_id %r matches no project; creating without one",
                project_id,
            )
            project_id = None
        session = await self.sessions.create(
            title=p.get("title", "New Session"),
            provider=p.get("provider"),
            model=p.get("model"),
            workspace=p.get("workspace"),
            project_id=project_id,
            settings=p.get("settings"),
            effort=p.get("effort"),
            created_by=p.get("created_by"),
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_CREATED,
            request_id=event.request_id,
            payload={"session": self._session_to_dict(session)},
        ))

    async def _handle_get_session(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        if not session_id:
            raise InvalidEventError("GetSession requires 'session_id'")
        # B18: optional, additive. Omitted/null keeps today's unbounded
        # fetch -- the exact path F3 depends on -- untouched.
        message_limit = _parse_optional_positive_int(
            event.payload.get("message_limit"), "GetSession", "message_limit",
        )
        session = await self.sessions.get(
            session_id, load_messages=True, message_limit=message_limit,
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION,
            request_id=event.request_id,
            payload={"session": self._session_to_dict(session, include_messages=True)},
        ))

    async def _handle_session_messages(self, event: IncomingEvent) -> None:
        """B18: the keyset scrollback query. `before`/`after` are seq
        cursors, not row indices or timestamps -- see managers/message.py
        and database/session_repository.py for why."""
        p = event.payload
        session_id = p.get("session_id")
        if not session_id:
            raise InvalidEventError("SessionMessages requires 'session_id'")

        before = _parse_optional_int(p.get("before"), "SessionMessages", "before")
        after = _parse_optional_int(p.get("after"), "SessionMessages", "after")
        if before is not None and after is not None:
            raise InvalidEventError(
                "SessionMessages: pass at most one of 'before'/'after' "
                "(both null means 'the newest limit messages')"
            )

        limit = _parse_optional_positive_int(p.get("limit"), "SessionMessages", "limit")
        if limit is None:
            raise InvalidEventError("SessionMessages requires 'limit'")

        messages, has_more_before, has_more_after = await self.sessions.get_messages_page(
            session_id, before=before, after=after, limit=limit,
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_MESSAGE_LIST,
            request_id=event.request_id,
            payload={
                "session_id": session_id,
                "messages": [m.to_dict() for m in messages],
                "has_more_before": has_more_before,
                "has_more_after": has_more_after,
            },
        ))

    async def _handle_list_sessions(self, event: IncomingEvent) -> None:
        include_archived = bool(event.payload.get("include_archived", False))
        sessions = await self.sessions.list_all(include_archived=include_archived)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_LIST,
            request_id=event.request_id,
            payload={"sessions": [self._session_to_dict(s) for s in sessions]},
        ))

    async def _handle_rename_session(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        new_title = event.payload.get("title")
        if not session_id or not new_title:
            raise InvalidEventError("RenameSession requires 'session_id' and 'title'")
        await self.sessions.rename(session_id, new_title)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_RENAMED,
            request_id=event.request_id,
            payload={"session_id": session_id, "title": new_title},
        ))

    async def _handle_archive_session(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        if not session_id:
            raise InvalidEventError("ArchiveSession requires 'session_id'")
        await self.sessions.archive(session_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_ARCHIVED,
            request_id=event.request_id,
            payload={"session_id": session_id},
        ))

    async def _handle_unarchive_session(self, event: IncomingEvent) -> None:
        """SessionManager.unarchive() has existed since B2 with no event
        type and no handler, which made it unreachable from Go."""
        session_id = event.payload.get("session_id")
        if not session_id:
            raise InvalidEventError("UnarchiveSession requires 'session_id'")
        await self.sessions.unarchive(session_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_UNARCHIVED,
            request_id=event.request_id,
            session_id=session_id,
            payload={"session_id": session_id},
        ))

    async def _handle_delete_session(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        if not session_id:
            raise InvalidEventError("DeleteSession requires 'session_id'")
        await self.sessions.delete(session_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_DELETED,
            request_id=event.request_id,
            payload={"session_id": session_id},
        ))

    async def _handle_update_metadata(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        patch = event.payload.get("metadata", {})
        if not session_id:
            raise InvalidEventError("UpdateSessionMetadata requires 'session_id'")
        session = await self.sessions.update_metadata(session_id, patch)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_METADATA_UPDATED,
            request_id=event.request_id,
            payload={"session_id": session_id, "metadata": session.metadata},
        ))

    async def _handle_update_settings(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        patch = event.payload.get("settings", {})
        if not session_id:
            raise InvalidEventError("UpdateSessionSettings requires 'session_id'")
        session = await self.sessions.update_settings(session_id, patch)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_SETTINGS_UPDATED,
            request_id=event.request_id,
            payload={"session_id": session_id, "settings": session.settings},
        ))

    async def _handle_list_providers(self, event: IncomingEvent) -> None:
        results = []
        for name in self.providers.list():
            provider = self.providers.get(name)
            if not getattr(provider, "selectable", True):
                continue  # MockProvider: registered for tests, never offered
            # F10 found ListProviders appearing to hang for minutes on a
            # fresh profile's first launch, with every later launch of the
            # SAME profile resolving instantly -- a pattern that pointed at
            # something environmental (Windows' IPv6/"localhost" happy-
            # eyeballs delay is the leading suspect; OllamaProvider's
            # default base_url is now the literal 127.0.0.1 rather than
            # "localhost" for exactly this reason) rather than Ollama
            # itself being slow. Logged unconditionally, at warning once it
            # crosses a second, so the NEXT reproduction -- if there is one
            # -- pins the exact culprit from point.log instead of being
            # re-diagnosed from a bug report alone.
            started = time.monotonic()
            try:
                health = await provider.health()
                elapsed = time.monotonic() - started
                log_at = _log.warning if elapsed > 1.0 else _log.debug
                log_at("Provider %r health check took %.3fs (status=%s)", name, elapsed, health.status.value)
                results.append({"name": name, **asdict(health)})
            except ProviderError as exc:
                elapsed = time.monotonic() - started
                _log.warning("Provider %r health check raised after %.3fs: %s", name, elapsed, exc)
                results.append({"name": name, "status": "unhealthy", "detail": str(exc)})
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROVIDER_LIST,
            request_id=event.request_id,
            payload={"providers": results},
        ))

    async def _handle_list_provider_models(self, event: IncomingEvent) -> None:
        name = event.payload.get("provider")
        if not name:
            raise InvalidEventError("ListProviderModels requires 'provider'")
        provider = self.providers.get(name)  # raises ProviderNotRegisteredError if missing
        models = await provider.list_models()
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROVIDER_MODEL_LIST,
            request_id=event.request_id,
            payload={"provider": name, "models": [asdict(m) for m in models]},
        ))

    async def _handle_set_provider_api_key(self, event: IncomingEvent) -> None:
        name = event.payload.get("provider")
        api_key = event.payload.get("api_key")
        base_url = event.payload.get("base_url")
        if not name or not api_key:
            raise InvalidEventError("SetProviderApiKey requires 'provider' and 'api_key'")
        config = ProviderConfig(provider=name, api_key=api_key, base_url=base_url)
        instance = create_provider(name, config)
        health = await instance.health()
        if health.status != ProviderHealthStatus.HEALTHY:
            error_cls = _HEALTH_FAILURE_ERRORS.get(health.status, ProviderError)
            raise error_cls(health.detail or f"{name} key failed validation ({health.status.value})")
        appconfig.save_api_key(name, api_key)
        self.providers.register(name, instance)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROVIDER_API_KEY_SET,
            request_id=event.request_id,
            payload={"provider": name},
        ))

    async def _handle_set_behaviour_override(self, event: IncomingEvent) -> None:
        """B26: the Behaviour tab's write path. Same key, presence of
        'value' changes the verb -- DetachContext's shape, not a second
        event type for clearing one override."""
        key = event.payload.get("key")
        if not key:
            raise InvalidEventError("SetBehaviourOverride requires 'key'")
        if "value" in event.payload:
            try:
                appconfig_overrides.set_override(key, event.payload["value"])
            except ValueError as exc:
                raise InvalidEventError(str(exc)) from exc
        else:
            appconfig_overrides.clear_override(key)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.BEHAVIOUR_OVERRIDE_SET,
            request_id=event.request_id,
            payload={"key": key, "overrides": appconfig_overrides.all_overrides()},
        ))

    async def _handle_get_doctor_report(self, event: IncomingEvent) -> None:
        """B26: `point doctor`'s own report, as data. Deferred import --
        `cli.py` imports `Runtime` from this module, so importing `cli`
        at module level here would be circular; by the time this handler
        runs, both modules are already fully loaded."""
        import cli

        report = await cli.build_doctor_report()
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.DOCTOR_REPORT,
            request_id=event.request_id,
            payload=report,
        ))

    async def _handle_set_session_provider(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        provider = event.payload.get("provider")
        model = event.payload.get("model")
        if not session_id or not provider or not model:
            raise InvalidEventError("SetSessionProvider requires 'session_id', 'provider', and 'model'")
        await self.sessions.set_provider(session_id, provider, model)
        project_id = event.payload.get("project_id")
        if project_id:
            if await self.projects.exists(project_id):
                await self.sessions.set_project(session_id, project_id)
            else:
                _log.warning(
                    "SetSessionProvider: project_id %r matches no project; binding left unchanged",
                    project_id,
                )
        session = await self.sessions.get(session_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_PROVIDER_UPDATED,
            request_id=event.request_id,
            payload={
                "session_id": session_id, "provider": provider, "model": model,
                "project_id": session.project_id,
            },
        ))


    @staticmethod
    def _session_to_dict(session: Session, include_messages: bool = False) -> dict[str, Any]:
        d = {
            "id": session.id,
            "project_id": session.project_id,
            "title": session.title,
            "provider": session.provider,
            "model": session.model,
            "workspace": session.workspace,
            "state": session.state.value,
            "created_at": session.created_at,
            "updated_at": session.updated_at,
            "summary": session.summary,
            "active_task_id": session.active_task_id,
            "metadata": session.metadata,
            "settings": session.settings,
            "effort": session.effort,
            "created_by": session.created_by,
            "archived_at": session.archived_at,
        }
        if include_messages:
            d["messages"] = [m.to_dict() for m in session.messages]
            # B18: only present alongside a bounded (message_limit) load.
            # The unbounded path leaves session.total_message_count None,
            # so this key is simply absent -- "exactly as before" for F3.
            if session.total_message_count is not None:
                d["total_message_count"] = session.total_message_count
        return d

    # -- project handlers ---------------------------------------------------
    # project=None is a legitimate answer everywhere here -- "no .point\"
    # means "no project scope", not an error. _resolve_optional_project()
    # only raises when a project was actually required.

    async def _resolve_optional_project(self, payload: dict[str, Any]) -> Optional[Project]:
        project_id = payload.get("project_id")
        path = payload.get("path")
        if not project_id and not path:
            return None
        return await self.projects.resolve(path=path, explicit_project_id=project_id)

    async def _require_project(self, payload: dict[str, Any]) -> Project:
        project = await self._resolve_optional_project(payload)
        if project is None:
            raise ProjectNotFoundError(
                "No project resolved: pass 'project_id', or a 'path' containing .point\\"
            )
        return project

    async def _handle_project_init(self, event: IncomingEvent) -> None:
        path = event.payload.get("path")
        if not path:
            raise InvalidEventError("ProjectInit requires 'path'")
        project = await self.projects.init(path)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT,
            request_id=event.request_id,
            payload={"project": project.to_dict()},
        ))

    async def _handle_project_resolve(self, event: IncomingEvent) -> None:
        p = event.payload
        project = await self.projects.resolve(path=p.get("path"), explicit_project_id=p.get("project_id"))
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT,
            request_id=event.request_id,
            payload={"project": project.to_dict() if project else None},
        ))

    async def _handle_list_project_files(self, event: IncomingEvent) -> None:
        p = event.payload
        project = await self._require_project(p)
        files = await self.projects.list_files(
            project, prefix=p.get("prefix", ""), limit=p.get("limit", 200), refresh=bool(p.get("refresh", False))
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT_FILE_LIST,
            request_id=event.request_id,
            payload={"files": files},
        ))

    async def _handle_read_project_file(self, event: IncomingEvent) -> None:
        p = event.payload
        if "path" not in p:
            raise InvalidEventError("ReadProjectFile requires 'path'")
        project = await self._resolve_optional_project(p)
        # B32: the EDITOR's cap (512KB), not the index/tool one (100KB).
        # read_file defaults to index.max_file_kb so the `read_file` tool
        # keeps its own, narrower limit -- a 400KB file is reasonable to
        # edit and unreasonable to pull into a prompt.
        result = await self.projects.read_file(
            project,
            p["path"],
            allow_outside_project=bool(p.get("allow_outside_project", False)),
            max_kb=appconfig.editor_max_file_kb(),
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT_FILE,
            request_id=event.request_id,
            payload={"file": result},
        ))

    async def _handle_write_project_file(self, event: IncomingEvent) -> None:
        r"""B32: the editor's save.

        `_require_project`, NOT `_resolve_optional_project` -- the same
        choice AcquireFileLock and its three siblings already make, and
        for the same reason: a file with no `.point\` above it cannot
        take a lock, so it is read-only by construction rather than
        writable-but-unlockable. That is F13's "no project means
        read-only" decision enforced where it is enforceable.

        `expected_mtime` ABSENT and `expected_mtime: null` both mean the
        same thing here -- "I expect no file at this path" -- because
        that is the creation signal, not a missing value. The
        precondition is never defaulted to "write anyway"; `overwrite`
        has to be asked for.

        F9d adds `session_id`, additively and by exactly the rule B36
        established one event over on CheckFileStale:

        - No `session_id` -> EXACTLY the pre-F9d behaviour
          (`_require_project`: explicit `project_id`, else a walk-up
          from `path`, else refused). Every caller that has ever sent
          this event keeps its own answer, byte for byte.
        - `session_id` given -> the project is the SESSION's own, via
          `_attachment_scope`, and `project_id` in the payload is
          IGNORED OUTRIGHT -- not merged, not a fallback. This is F9a's
          hazard 1 on the one destructive path in this backend: a buffer
          that received an agent-applied region from a session scoped to
          project A must not be written under whatever `.point\` happens
          to sit above the file. Resolving the scope here, before
          `write_file`, is what makes "commit nothing until the guard
          passes" structural -- the guard is not a separate call that
          could be skipped, it is which project the write is performed
          against.
        - A `session_id` whose session has NO project is refused, not
          downgraded to the path walk-up. Falling back would hand the
          caller precisely the resolution it asked to avoid, which is
          the drift, not a lenient version of it. It is also already
          this event's own rule for a bare path ("no project means
          read-only", above), now stated for a session too.

        `_attachment_scope`'s second return value (`allow_outside_
        project`) is deliberately DISCARDED: `write_file` has no such
        parameter and must not gain one -- "an out-of-project path
        cannot take a lock at all... accepting one here would
        manufacture exactly the writable-but-unlockable case F13's `no
        .point\ means read-only` decision rules out" (its own
        docstring). An outside path raises PathDeniedError, which is the
        refusal this episode wants, not a case to permit.
        """
        p = event.payload
        if "path" not in p:
            raise InvalidEventError("WriteProjectFile requires 'path'")
        if "content" not in p:
            raise InvalidEventError("WriteProjectFile requires 'content'")
        if not p.get("holder"):
            raise InvalidEventError("WriteProjectFile requires 'holder'")
        session_id = p.get("session_id")
        if session_id:
            project, _allow_outside = await self._attachment_scope(session_id)
            if project is None:
                raise ProjectNotFoundError(
                    f"Session {session_id} has no project: a write scoped to a "
                    "session cannot fall back to resolving a project from the "
                    "file's own path"
                )
        else:
            project = await self._require_project(p)

        raw_mtime = p.get("expected_mtime")
        result = await self.projects.write_file(
            project,
            p["path"],
            p["content"],
            holder=p["holder"],
            expected_mtime=None if raw_mtime is None else float(raw_mtime),
            overwrite=bool(p.get("overwrite", False)),
            line_ending=p.get("line_ending", "lf"),
            bom=bool(p.get("bom", False)),
            trailing_newline=bool(p.get("trailing_newline", True)),
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT_FILE_WRITTEN,
            request_id=event.request_id,
            payload=result.to_dict(),
        ))

    async def _handle_acquire_file_lock(self, event: IncomingEvent) -> None:
        p = event.payload
        if "path" not in p or "holder" not in p:
            raise InvalidEventError("AcquireFileLock requires 'path' and 'holder'")
        project = await self._require_project(p)
        result = await self.projects.acquire_lock(project, p["path"], p["holder"])
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILE_LOCK_RESULT,
            request_id=event.request_id,
            payload=result.to_dict(),
        ))

    async def _handle_heartbeat_file_lock(self, event: IncomingEvent) -> None:
        p = event.payload
        if "path" not in p or "holder" not in p:
            raise InvalidEventError("HeartbeatFileLock requires 'path' and 'holder'")
        project = await self._require_project(p)
        result = await self.projects.heartbeat_lock(project, p["path"], p["holder"])
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILE_LOCK_RESULT,
            request_id=event.request_id,
            payload=result.to_dict(),
        ))

    async def _handle_release_file_lock(self, event: IncomingEvent) -> None:
        p = event.payload
        if "path" not in p or "holder" not in p:
            raise InvalidEventError("ReleaseFileLock requires 'path' and 'holder'")
        project = await self._require_project(p)
        released = await self.projects.release_lock(project, p["path"], p["holder"])
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILE_LOCK_RELEASED,
            request_id=event.request_id,
            payload={"released": released},
        ))

    async def _handle_take_over_file_lock(self, event: IncomingEvent) -> None:
        p = event.payload
        if "path" not in p or "holder" not in p:
            raise InvalidEventError("TakeOverFileLock requires 'path' and 'holder'")
        project = await self._require_project(p)
        result = await self.projects.take_over_lock(project, p["path"], p["holder"])
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILE_LOCK_RESULT,
            request_id=event.request_id,
            payload=result.to_dict(),
        ))

    async def _handle_check_file_stale(self, event: IncomingEvent) -> None:
        """B36 added `session_id` and `size` as real comparison fields,
        additively:

        - No `session_id` -> EXACTLY the pre-B36 behaviour below
          (`_resolve_optional_project` + the payload's own
          `allow_outside_project`). This is `cli.py project check-stale`
          today, and it must not silently start answering a different
          question.
        - `session_id` given -> the SESSION's own scope, via
          `_attachment_scope` -- never `_resolve_optional_project`, which
          walks up from `path` to auto-discover a project and answers a
          different question (B12's bug #2; 374baa2 fixed the same drift
          for AttachContext/ResolveAttachment). `allow_outside_project`
          is then config-driven through `_attachment_scope`, same as
          those two callers, not the payload flag -- a caller that knows
          which session produced the recorded stat should be checked
          against that session's actual scope, not a per-request
          override of it.
        - `size` is optional throughout: omitted, `check_stale` compares
          mtime alone, unchanged from before B36.
        """
        p = event.payload
        if "path" not in p or "mtime" not in p:
            raise InvalidEventError("CheckFileStale requires 'path' and 'mtime'")
        session_id = p.get("session_id")
        if session_id:
            project, allow_outside = await self._attachment_scope(session_id)
        else:
            project = await self._resolve_optional_project(p)
            allow_outside = bool(p.get("allow_outside_project", False))
        raw_size = p.get("size")
        changed = await self.projects.check_stale(
            project, p["path"], float(p["mtime"]),
            recorded_size=int(raw_size) if raw_size is not None else None,
            allow_outside_project=allow_outside,
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILE_STALE_RESULT,
            request_id=event.request_id,
            payload={"changed": changed},
        ))

    # -- agent / directory listing (B23) -------------------------------
    # F8's `@` picker needs both, live, per keystroke: which agents
    # exist, and what is in an arbitrary directory.

    async def _handle_list_agents(self, event: IncomingEvent) -> None:
        """No session, no project -- AgentRegistry.list() already does
        the whole job (B9); this is plumbing, not new logic."""
        configs = await self.agents.list()
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.AGENT_LIST,
            request_id=event.request_id,
            payload={
                "agents": [
                    {"id": c.id, "name": c.name, "description": c.description}
                    for c in configs
                ],
            },
        ))

    async def _handle_list_directory(self, event: IncomingEvent) -> None:
        """Project scope here is explicit-only (`project_id`), unlike
        ReadProjectFile/CheckFileStale's `_resolve_optional_project`,
        which also walks up from the same `path` it operates on to
        auto-discover a project. A browse target is meant to sweep
        across arbitrary, often unrelated locations (`@C:\\`, `@D:\\`) --
        letting it silently pick up whatever `.point\\` happens to sit
        above whichever folder the user is currently looking at would
        make browsing behave differently depending on ambient project
        markers no one asked about. A session that already has a
        resolved project passes its id explicitly, exactly as
        AcquireFileLock and friends require one; browsing with no
        project is a first-class, equally valid case, not a fallback.
        """
        p = event.payload
        if "path" not in p:
            raise InvalidEventError("ListDirectory requires 'path'")
        project = None
        if p.get("project_id"):
            project = await self.projects.resolve(explicit_project_id=p["project_id"])
        result = await self.projects.list_directory(project, p["path"], cwd=p.get("cwd"))
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.DIRECTORY_LIST,
            request_id=event.request_id,
            payload=result,
        ))

    # -- task handlers ----------------------------------------------------

    async def _handle_send_message(self, event: IncomingEvent) -> None:
        text = event.payload.get("text", "")
        session_id = event.payload.get("session")
        if not session_id:
            raise InvalidEventError("SendMessage requires 'session'")

        # No load_messages: the context builder reads history itself,
        # through the memory service and its own stm_window. Loading the
        # full history here as well would fetch every message in the
        # session twice, and only one of the two copies would be the one
        # actually sent.
        session = await self.sessions.get(session_id)

        # B12: routing is decided HERE, not in the client. `cli.py` used
        # to strip its own `@agent` prefix and hand the remainder over as
        # text, which meant the CLI and a future picker each owned a copy
        # of the ambiguity rule. One parser, one rule, both callers.
        invocation = await self._parse_invocation(session, text)
        if invocation.advanced:
            # Parsed and refused, which is the whole requirement. The
            # Invocation already records `advanced=True`, so the day the
            # capability exists nothing about the parser changes.
            raise AdvancedUnavailableError()
        text = invocation.text
        if invocation.agent and not event.payload.get("agent"):
            # An explicit `payload["agent"]` still wins: that is a picker
            # that already resolved a choice, and re-deriving it from
            # text it may have rewritten would be second-guessing it.
            event.payload["agent"] = invocation.agent

        agent = await self._resolve_agent_for_send(session, event.payload)
        editor_context = _editor_context_from_payload(event.payload.get("editor_context"))

        # B11: no `background` field exists on AgentConfig (B9's field
        # list is frozen without one, and adding a real column is a
        # migration). `max_steps > 1` is the config already on hand that
        # answers the same question -- an agent with a real bounded loop
        # is the one that both (a) belongs in the background and (b) can
        # meaningfully ask "have I already done this work in this
        # session before". One computed flag drives both, here, not
        # inside agents\executor.py, which must stay free of
        # `if agent.id == ...`. See this episode's Handoff for where this
        # heuristic breaks and what replaces it.
        is_bounded_loop = agent.max_steps > 1
        reference_index = (
            await self._build_reference_index(session, agent) if is_bounded_loop else None
        )
        if reference_index:
            # The visible half of Done-when #6 ("the log shows it
            # consulted memory/past results before searching") -- a
            # real model may or may not act on the index, but that it
            # was BUILT and OFFERED is deterministic and belongs in the
            # log regardless of what the model does with it.
            _log.info(
                "Session %s: agent %s offered %d past result(s) before this turn",
                session.id, agent.id, reference_index.count("\n- task "),
            )

        # B9: AgentExecutor owns "resolve config -> build context ->
        # create task -> stream -> emit -> persist" end to end; this
        # handler's job is reduced to "who, and with which agent". A
        # session with NULL provider/model is no longer refused here
        # unconditionally -- task override > session > agent default >
        # global config means it may still resolve. The executor raises
        # InvalidEventError (-> a clean Error event) only when nothing in
        # that whole chain names a provider/model.
        # B12's turn queue, checked before a task exists so a refusal is
        # still a clean answer to the request rather than a task that
        # dies immediately. Inline turns only: an agent task runs in its
        # own lane and is explicitly never queued behind conversation.
        if not is_bounded_loop:
            self._check_turn_queue(session.id)

        run = await self.agent_executor.run(
            session=session,
            text=text,
            agent=agent,
            make_sink=lambda message_id: self._make_stream_sink(
                event.request_id, session_id, message_id, background=is_bounded_loop,
            ),
            inline=not is_bounded_loop,
            bounds=_bounds_override(event.payload.get("bounds")),
            editor_context=editor_context,
            research_reference_index=reference_index,
        )
        # TaskStarted is emitted by the sink, from Task._run()'s own
        # "started" chunk -- NOT here (B10 fix). Emitting it here raced
        # the task body: create_task() schedules the body immediately,
        # and the first `await` inside run() hands the loop to it, so any
        # event the body produced before this line reached the client
        # ahead of the TaskStarted that announces the task. B9's single
        # round-trip hid it (the first provider chunk always arrived
        # later than this emit); B10's loop emits `step 1/N` with no
        # delay at all and lost it every time.
        self.state.active_task_count = self.tasks.active_count()

    async def _resolve_agent_for_send(self, session: Session, payload: dict[str, Any]) -> AgentConfig:
        """`payload["agent"]` is a per-message override (B11) -- the
        `@agent-id` prefix `cli.py send` strips, and the mechanism a
        future `@` picker (F8) reaches the same way. Never written back
        to `session.settings`: an override is for this one send, exactly
        like BoundsOverride (B10) never mutates the stored AgentConfig.
        Falls through to the session's own binding when absent."""
        override_id = payload.get("agent")
        if override_id:
            return await self.agents.resolve(override_id)  # AgentNotFoundError -> a clean Error event
        return await self.agents.resolve_for_session(session)

    async def _parse_invocation(self, session: Session, text: str):
        """Parse typed input against the agents that actually exist.

        The agent names are fetched once and handed to the parser as a
        set, rather than the parser being given the registry: the
        ambiguity rule ("an exact agent name wins, otherwise it is a
        path") has to be decidable without I/O, or it becomes a rule
        that behaves differently under load.

        Matching mirrors `AgentRegistry.resolve()` -- id first, then a
        case-insensitive name -- so `@research` routes identically
        whether it arrives here or through the picker.
        """
        configs = await self.agents.list()
        ids = {c.id for c in configs}
        names = {c.name.lower() for c in configs}

        def is_agent(token: str) -> bool:
            return token in ids or token.lower() in names

        return parse_invocation(text, is_agent=is_agent)

    def _check_turn_queue(self, session_id: str) -> None:
        """One conversational turn in flight per session, plus
        `chat.queue_depth` waiting. Beyond that, `queue_full`.

        A rejection rather than a silent drop, and rather than a third
        reply streaming into the same transcript: a rejected send leaves
        the user holding text they can resend, while a dropped one
        leaves them believing they asked something they did not. The UI
        locks its input on the `queue_full` code and shows the reason.
        """
        lane = turn_lane(session_id)
        depth = turn_queue_depth()
        occupancy = self.tasks.scheduler.occupancy(lane)
        ceiling = 1 + depth
        if occupancy >= ceiling:
            raise QueueFullError(
                f"This session already has {occupancy} turn(s) in flight or "
                f"queued (limit {ceiling}: one streaming plus chat.queue_depth "
                f"= {depth} waiting). Wait for one to finish, or cancel it.",
                depth=depth, in_flight=occupancy,
            )

    async def _build_reference_index(self, session: Session, agent: AgentConfig) -> Optional[str]:
        """Research's memory-first behaviour (B11), the half that needs
        no B13 schema: a reference index of this same agent's own past
        completed tasks *in this session*, each with its age -- scoped
        the same way B10's load_task_result already is, for the same
        reason (a task id is not a capability). The "accepted memory
        records" half of memory-first stays genuinely empty in v1;
        context.build() already logs that as unimplemented, and this
        function does not touch it.
        """
        rows = await self.task_repo.list_recent(limit=50, session_id=session.id)
        matches = [r for r in rows if r["agent_id"] == agent.id and r["status"] == "completed"]
        if not matches:
            return None

        stale_after_days = appconfig_overrides.config_value_live("research.stale_after_days", 7)
        now = time.time()
        lines = [f"Past {agent.name} results in this session (most recent first):"]
        for row in matches[:5]:
            finished = row["finished_at"] or row["created_at"]
            age_days = (now - finished) / 86400.0
            messages = await self.memory.task_messages(session.id, row["id"])
            preview = " ".join(
                m.text for m in messages if m.role == MessageRole.ASSISTANT and m.text
            )[:200]
            stale = f" [older than {stale_after_days}d -- consider re-researching]" if age_days > stale_after_days else ""
            lines.append(f"- task {row['id'][:8]} ({age_days:.1f}d ago{stale}): {preview or '(no stored text)'}")
        lines.append(
            "Before searching, check whether one of these already answers the "
            "question. Call load_task_result with its full task id to read one "
            "in full before deciding to search."
        )
        return "\n".join(lines)

    async def _handle_cancel_task(self, event: IncomingEvent) -> None:
        """The ack is NOT terminal (B6 §7). It used to be a second
        TaskCancelled, so a cancel produced two terminal events for one
        task and project_idea.md §4's "exactly one terminal event" was
        false. The sink is now the only producer of TaskCancelled.

        `accepted: false` covers both "already finished" and "never
        existed" -- from the caller's side those are the same answer:
        nothing was stopped, and no terminal event is coming.
        """
        task_id = event.payload.get("task_id")
        if not task_id:
            raise InvalidEventError("CancelTask requires 'task_id'")
        try:
            accepted = self.tasks.cancel(task_id)
        except TaskNotFoundError:
            accepted = False
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.TASK_CANCEL_ACCEPTED,
            request_id=event.request_id,
            task_id=task_id,
            payload={"accepted": accepted},
        ))

    async def _handle_list_tasks(self, event: IncomingEvent) -> None:
        """`tasks.list(scope)` -- running + queued + recent, **across all
        sessions** (B12).

        Across all sessions is not a convenience: the frontend root owns
        this list globally, so a per-session answer would force it to fan
        out one request per session and stitch the results, and the
        stitched view would be inconsistent by construction.

        Live objects first, durable rows second. `TaskManager._tasks` is
        the source of truth for anything running or queued -- the `tasks`
        table is a mirror written a moment behind it -- so a live task
        wins over its own row wherever both exist. `recent` is the only
        thing the table answers alone, because a finished task's object
        is eventually swept while its row is not.

        scope: `all` (default) | `active` (running + queued only).
        """
        scope = event.payload.get("scope", "all")
        if scope not in ("all", "active"):
            raise InvalidEventError(
                f"ListTasks scope must be 'all' or 'active', got {scope!r}"
            )
        limit = int(event.payload.get("limit", 50))

        live = self.tasks.list_all()
        entries = [
            {
                "id": t.id,
                "kind": t.kind,
                "state": t.state.value,
                "session": t.session_id,
                "agent": t.agent_id,
                "created_at": t.created_at,
                "started_at": t.started_at,
                "finished_at": t.finished_at,
                "error_code": t.result.code if t.result else None,
                "live": True,
            }
            for t in live
            if scope == "all" or t.is_active()
        ]

        if scope == "all":
            seen = {entry["id"] for entry in entries}
            for row in await self.task_repo.list_recent(limit=limit):
                if row["id"] in seen:
                    continue
                entries.append({
                    "id": row["id"],
                    "kind": row["kind"],
                    "state": row["status"],
                    "session": row["session_id"],
                    "agent": row["agent_id"],
                    "created_at": row["created_at"],
                    "started_at": row["started_at"],
                    "finished_at": row["finished_at"],
                    "error_code": row["error_code"],
                    "live": False,
                })

        entries.sort(key=lambda e: e["created_at"] or 0.0, reverse=True)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.TASK_LIST,
            request_id=event.request_id,
            payload={"scope": scope, "tasks": entries[:limit]},
        ))

    # -- attachment handlers (B12) -----------------------------------------

    async def _handle_attach_context(self, event: IncomingEvent) -> None:
        p = event.payload
        session_id, session = await self._require_session(p)
        path = p.get("path")
        if not path:
            raise InvalidEventError("AttachContext requires 'path'")
        project, allow_outside = await self._attachment_scope(session_id)
        # The project an unbound session would adopt is RESOLVED here (the
        # guard below needs it to resolve the path against -- B12's bug #1)
        # but only WRITTEN after the attach succeeds. Adopting before the
        # guard meant a refused attach still permanently bound the
        # session's project: reproduced with a genuinely binary file
        # inside a `.point\` tree, refused with "looks binary", project_id
        # set regardless. See _adoptable_project_for / _commit_adoption.
        pending_adopt: Optional[Project] = None
        if project is None:
            project = await self._adoptable_project_for(session, path)
            pending_adopt = project
        mode = p.get("mode") or await self._default_attach_mode(session)
        attachment, resolution = await self.attachments.attach(
            session_id, path,
            project=project,
            mode=mode,
            allow_outside_project=allow_outside,
            model=session.model,
        )
        # Past the guards: the attach is real, so the binding is earned.
        if pending_adopt is not None:
            await self._commit_adoption(session, pending_adopt)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.ATTACHMENT,
            request_id=event.request_id,
            session_id=session_id,
            payload={
                "attachment": attachment.to_dict(),
                "resolution": resolution.to_dict(),
            },
        ))

    async def _handle_get_context_report(self, event: IncomingEvent) -> None:
        """B20: the on-demand pull. Session resolution mirrors
        `cli.py context report` exactly -- `memory.session()` /
        `memory.latest_session()`, not `self.sessions.get()` -- so the
        wire answer and the CLI's own numbers are the same lookup, not
        two that happen to usually agree.
        """
        session_id = event.payload.get("session_id")
        if session_id:
            session = await self.memory.session(session_id)
            if session is None:
                raise SessionNotFoundError(f"No session with id {session_id!r}")
        else:
            session = await self.memory.latest_session()
            if session is None:
                raise InvalidEventError("GetContextReport: no sessions exist")
            session_id = session.id

        # B9: the report is shaped by the session's own agent, exactly
        # like cmd_context_report -- not a fixed policy.
        agent = await self.agents.resolve_for_session(session)
        report = await context.report(session_id, self.memory, policy=agent.to_context_policy())
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.CONTEXT_REPORT,
            request_id=event.request_id,
            session_id=session_id,
            payload=report.to_dict(),
        ))

    async def _push_context_report(self, session_id: str) -> None:
        """B20: pushed once a turn's assistant message is persisted --
        the only point the report's numbers can have moved. Calls the
        same `context.report()` `GetContextReport` calls; never a second
        implementation of the arithmetic.

        Best-effort and swallows `RuntimeException`: this runs from
        inside the stream sink, on the same `await` chain `task.py`
        wraps to convert an exception into a `failed` terminal event
        (`runtime/task.py` `_run()`). A report that fails to build here
        must not retroactively turn an already-persisted, already
        `TaskDone`-announced turn into a second, contradictory terminal
        event.
        """
        try:
            session = await self.memory.session(session_id)
            if session is None:
                return
            agent = await self.agents.resolve_for_session(session)
            report = await context.report(session_id, self.memory, policy=agent.to_context_policy())
        except RuntimeException as exc:
            _log.warning("B20: could not build context report for session %s: %s", session_id, exc)
            return
        _log.debug(
            "B20: pushing context report for session %s (used=%d free=%d)",
            session_id, report.used_tokens, report.free_tokens,
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.CONTEXT_REPORT,
            request_id=None,
            session_id=session_id,
            payload=report.to_dict(),
        ))

    # -- usage capture and the daily cap (B14) -----------------------------

    async def _record_usage(self, task: Task, msg: Message) -> None:
        """One `usage_records` row per persisted reply, deriving its
        numbers from the SAME `msg.usage` that `messages.input_tokens`/
        `output_tokens`/`total_tokens` are built from a few lines above
        this call -- so this row and F9a's result card (which sums
        those message columns) can never disagree about one turn's
        tokens.

        Best-effort, same reasoning as `_maybe_checkpoint`'s docstring:
        this runs on the same await chain that is about to announce a
        terminal event for a turn that has ALREADY been persisted and
        ALREADY ran its real cost -- a usage-recording failure must not
        retroactively turn that into anything else, just a missing row.
        """
        try:
            invocation = msg.invocation
            provider = invocation.provider if invocation else None
            model = invocation.model if invocation else None
            agent_id = invocation.agent if invocation else None
            input_tokens = msg.usage.input_tokens
            output_tokens = msg.usage.output_tokens
            reply_text = msg.content.text or ""
            estimated = False
            # A completed reply with text in it spent at least one real
            # output token, so `output_tokens == 0` here can only mean
            # the provider never reported usage at all -- not that
            # nothing was spent. Falls back to the same len(text)//4
            # estimator managers\context.py already uses for the same
            # reason. (Every real provider in this repo reports usage --
            # see providers\*\provider.py -- so this path is for the
            # Ollama case the episode's own verification names: some
            # responses omit `eval_count`.)
            #
            # Exempt `echo`: there, `output_tokens == 0` is not an
            # omission, it is the true answer -- no provider was ever
            # called, so 0 is real and `messages.output_tokens` (which
            # F9a's card sums) is also genuinely 0. Estimating here
            # would make this row disagree with that column for the
            # one case where disagreeing is easiest to spot.
            if output_tokens == 0 and reply_text and provider != "echo":
                output_tokens = estimate_tokens(reply_text)
                estimated = True
            await self.usage.record(
                session_id=msg.session_id, task_id=task.id, agent_id=agent_id,
                provider=provider, model=model,
                input_tokens=input_tokens, output_tokens=output_tokens,
                estimated=estimated,
            )
        except Exception:  # noqa: BLE001
            _log.exception("Task %s: usage record failed to write", task.id)

    async def _push_usage_totals(self, session_id: str) -> None:
        """Pushed at the same turn boundary as `_push_context_report`
        (right after it, both gated on `persisted`), so the status
        panel is push-driven rather than polling for its totals.
        Best-effort for the same reason that method is."""
        try:
            session_totals = await self.usage.session_totals(session_id)
            cap = await self.usage.remaining_cap()
        except Exception:  # noqa: BLE001
            _log.exception("B14: could not build usage totals for session %s", session_id)
            return
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.USAGE_TOTALS_UPDATED,
            request_id=None,
            session_id=session_id,
            payload={"session": session_totals, "cap": cap},
        ))

    async def _handle_resolve_attachment(self, event: IncomingEvent) -> None:
        """Guard and price a path WITHOUT attaching it.

        This is what lets a picker show the cost before the user
        commits -- "attach it and see" is not an acceptable way to find
        out that a file is 40MB.
        """
        p = event.payload
        session_id, session = await self._require_session(p)
        path = p.get("path")
        if not path:
            raise InvalidEventError("ResolveAttachment requires 'path'")
        project, allow_outside = await self._attachment_scope(session_id)
        resolution = self.attachments.resolve(
            path,
            project=project,
            allow_outside_project=allow_outside,
            mode=p.get("mode") or await self._default_attach_mode(session),
            model=session.model,
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.ATTACHMENT_RESOLUTION,
            request_id=event.request_id,
            session_id=session_id,
            payload=resolution.to_dict(),
        ))

    async def _handle_detach_context(self, event: IncomingEvent) -> None:
        p = event.payload
        session_id, _session = await self._require_session(p)
        attachment_id = p.get("attachment")
        if not attachment_id:
            raise InvalidEventError("DetachContext requires 'attachment'")
        attachment = await self.attachments.detach(session_id, attachment_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.ATTACHMENT_DETACHED,
            request_id=event.request_id,
            session_id=session_id,
            # `file_deleted: false` is stated rather than implied.
            # Detach removing a binding and never a file is the
            # guarantee this command lives or dies on, and a client
            # should not have to take it on trust from a docstring it
            # cannot read.
            payload={"attachment": attachment.to_dict(), "file_deleted": False},
        ))

    async def _handle_set_attachment_mode(self, event: IncomingEvent) -> None:
        p = event.payload
        session_id, _session = await self._require_session(p)
        attachment_id = p.get("attachment")
        mode = p.get("mode")
        if not attachment_id or not mode:
            raise InvalidEventError("SetAttachmentMode requires 'attachment' and 'mode'")
        attachment = await self.attachments.set_mode(session_id, attachment_id, mode)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.ATTACHMENT,
            request_id=event.request_id,
            session_id=session_id,
            payload={"attachment": attachment.to_dict()},
        ))

    async def _handle_list_attachments(self, event: IncomingEvent) -> None:
        session_id, _session = await self._require_session(event.payload)
        attachments = await self.attachments.list(session_id)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.ATTACHMENT_LIST,
            request_id=event.request_id,
            session_id=session_id,
            payload={"attachments": [a.to_dict() for a in attachments]},
        ))

    async def _adoptable_project_for(self, session: Session, path: str) -> Optional[Project]:
        r"""The project an unbound session WOULD adopt from this path --
        resolved only, never written. `_commit_adoption` is what writes.

        Split in two (F8 verification): this used to resolve AND write in
        one step, and `_handle_attach_context` called it BEFORE
        `attachments.attach()` -- which is what raises
        AttachmentRefusedError. So a refused attach still permanently
        bound the session's project: reproduced with a real binary file
        (a copy of notepad.exe) inside a `.point\` tree, refused with
        "looks binary", `project_id` set anyway. The guard has to run
        first and the write has to be conditional on it passing, but the
        guard ALSO needs the project to resolve the path against (B12's
        own bug #1 -- "no project is open" while standing in one), so the
        resolve cannot simply move after it. Hence: resolve first, guard
        with the result, write only on success.

        Without this, `attach` was unusable in the only workflow anyone
        actually has: `project init`, then `session new`, then `attach` --
        which refused with "no project is open" even though the project
        existed and the file was inside it. The project was real; the
        SESSION had never been told about it, and nothing but
        `session new --project <uuid>` could tell it.

        Resolving from the FILE rather than from the process's cwd is
        the important part. `_attachment_scope` exists so the attach-time
        guard and the send-time guard ask the same question, and the
        builder asks `session.project_id`. A cwd-resolved project would
        guard against something the builder will never look at -- which
        is exactly the bug that made `attach` quote ~718 tokens for a
        file that then contributed zero.

        Only ever fills a NULL. A session already bound to a project is
        never re-pointed at another one by attaching a file: that would
        silently change the root every later tool call is guarded
        against, which is a security boundary, not a convenience.
        """
        if session.project_id:
            return await self.memory.project(session.id)
        try:
            project = await self.projects.resolve(path)
        except (ProjectNotFoundError, OSError):
            return None
        if project is None:
            # The file is not inside any project. Left unbound on
            # purpose -- the path guard refuses it a moment later, with
            # a message naming the real reason.
            return None
        return project

    async def _commit_adoption(self, session: Session, project: Project) -> None:
        """Write the binding `_adoptable_project_for` resolved. Called
        ONLY after `attachments.attach()` has returned successfully --
        a refused attach, for any reason the guards can give, must leave
        `project_id` exactly as it was."""
        await self.sessions.set_project(session.id, project.id)
        _log.info(
            "Session %s adopted project %s (%s) from its first successful attachment",
            session.id, project.id, project.root_path,
        )

    async def _attachment_scope(self, session_id: str) -> tuple[Optional[Project], bool]:
        """The project and the outside-project permission an attachment
        will be resolved under **when the context builder resolves it**.

        Not the caller's choice, and deliberately not a per-request flag.
        `managers\\context.py` resolves every attachment against the
        SESSION's project (`SessionMemoryService.project()`) and the
        config key `project.allow_outside_project` -- so a guard that
        used anything else here would be answering a different question
        from the one the builder later asks.

        That mattered in practice, and this is what it looked like:
        attaching an absolute path with a caller-supplied
        `allow_outside_project` reported a confident "~718 tokens" and
        then contributed **zero**, because at send time the same path was
        refused by the guard with the session's real (empty) scope. The
        user was shown a cost for something that never entered the
        request. Now the refusal happens at attach time, where it names
        the reason and the user can still do something about it.
        """
        project = await self.memory.project(session_id)
        allow_outside = bool(appconfig_overrides.config_value_live("project.allow_outside_project", False))
        return project, allow_outside

    async def _require_session(self, payload: dict[str, Any]) -> tuple[str, Session]:
        session_id = payload.get("session")
        if not session_id:
            raise InvalidEventError("This event requires 'session'")
        return session_id, await self.sessions.get(session_id)

    async def _default_attach_mode(self, session: Session) -> str:
        """The agent's `default_attach_mode`, per B12 -- overridable per
        invocation by passing `mode` explicitly.

        B9 put the field on AgentConfig and nothing has ever read it;
        this is the reader. Falls back to `full` when the session's
        agent cannot be resolved, which is the same answer B8's
        ContextPolicy already defaults to.
        """
        try:
            agent = await self.agents.resolve_for_session(session)
        except RuntimeException:
            return MODE_FULL
        return agent.default_attach_mode or MODE_FULL

    # -- lifecycle handlers (B15) ------------------------------------------

    async def _handle_workspace_unload(self, event: IncomingEvent) -> None:
        """`workspace.unload(id)` from Go. **Never cancels a task.**

        Unloading a view is a UI event and the task is in this process's
        lifetime, not the view's -- so this releases what is free and
        reports what it is not allowed to touch. B15's Do-NOT list and
        Done-when #1 are the same statement from two directions: the
        task keeps streaming, its result is retrievable afterwards, and
        the reply says which resources its liveness protected.

        A workspace id matching no session is not an error. It is a view
        Go unloaded that this backend never held anything for, and an
        empty `released` with an empty `refused` says exactly that --
        distinguishable from a refusal, which is the whole reason this
        reply is three lists rather than a boolean.
        """
        workspace_id = event.payload.get("workspace") or event.payload.get("workspace_id")
        if not workspace_id:
            raise InvalidEventError("WorkspaceUnload requires 'workspace'")

        sessions = await self._sessions_for_workspace(workspace_id)
        report = await self._workspace_context(workspace_id, sessions).close()
        _log.info("WorkspaceUnload %s: %s", workspace_id, report.describe())
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.WORKSPACE_UNLOADED,
            request_id=event.request_id,
            payload={
                "workspace": workspace_id,
                "sessions": [s.id for s in sessions],
                **report.to_dict(),
            },
        ))

    async def _handle_events_since(self, event: IncomingEvent) -> None:
        r"""The replay rung of F2's recovery ladder (B40), served from
        the durable event log.

        **This replaces B15's `SessionReplaySince`, which is deleted.**
        B15 was asked for `events_since(session_id, seq)` and this repo
        had no event log to serve it from, so it shipped message-level
        replay keyed on `messages.seq` as a named interim and deferred
        the real thing. Migration v8's `events` table is that thing.

        The difference is what a client gets back. Message-level replay
        answered *what does this transcript look like now*. This answers
        *what happened while I was gone* -- the tool call that started
        AND completed in the gap, the progress steps, the usage update,
        the `FillProposal` pushed after a terminal event. None of those
        are messages, so none of them were expressible before.

        What it deliberately does not replay is the token stream.
        `TextDelta`/`ThinkingDelta`/`TaskToken` are not in the log at
        all (see `database\event_repository.py` for the policy and the
        measurement behind it), so a replayed turn arrives as its
        structure with the prose coming from `messages` -- or, for a
        reply the process died mid-way through, from B15's
        `message_checkpoints`, which this episode does not touch.

        `since_seq` is the `event_seq` cursor and nothing else. 0 or
        absent means everything the log still holds, which retention
        bounds -- so a client whose mark has been swept past gets the
        oldest surviving events rather than a lie about completeness,
        and `has_more` is how it learns to come back.

        `tasks` is carried over from B15's reply unchanged, for the
        reason B15 gave: replaying events rebuilds the view, and the
        task rows are what resolve a task the client marked UNKNOWN --
        including a `backend_died` row written by crash recovery before
        this process accepted a single event. Live objects first,
        durable rows second, the same rule `_handle_list_tasks` states.
        """
        p = event.payload
        session_id = p.get("session") or p.get("session_id")
        if not session_id:
            raise InvalidEventError("EventsSince requires 'session'")

        since_seq = _parse_optional_int(p.get("since_seq"), "EventsSince", "since_seq") or 0
        if since_seq < 0:
            raise InvalidEventError("EventsSince 'since_seq' must not be negative")
        limit = _parse_optional_positive_int(p.get("limit"), "EventsSince", "limit") or 200

        events, has_more = await self.events.events_since(session_id, since_seq, limit)

        live = {t.id: t for t in self.tasks.list_all() if t.session_id == session_id}
        tasks = [
            {
                "id": t.id,
                "state": t.state.value,
                "kind": t.kind,
                "agent": t.agent_id,
                "error_code": t.result.code if t.result else None,
                "live": True,
            }
            for t in live.values()
        ]
        for row in await self.task_repo.list_recent(limit=limit, session_id=session_id):
            if row["id"] in live:
                continue
            tasks.append({
                "id": row["id"],
                "state": row["status"],
                "kind": row["kind"],
                "agent": row["agent_id"],
                "error_code": row["error_code"],
                "live": False,
            })

        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_EVENTS,
            request_id=event.request_id,
            session_id=session_id,
            payload={
                "session": session_id,
                "since_seq": since_seq,
                "mechanism": "events.session_seq",
                "replay": True,
                "events": events,
                "tasks": tasks,
                "has_more": has_more,
            },
        ))

    async def _handle_shutdown(self, event: IncomingEvent) -> None:
        """`Shutdown` QUIESCES; it does not just flip a flag (B15).

        What it did before this episode: set STOPPING, cancel active
        tasks, set STOPPED. No ack -- which broke project_idea.md §4's
        "every COMMAND gets exactly one ack". No process exit -- the
        loops kept running. And `receive()` gates on
        `is_accepting_events()`, so every later event was rejected: the
        runtime became a brick that never exited.

        What it does now, in this order:

          1. STOPPING -- stop accepting new work. `Shutdown` itself
             stays accepted, so a second one is a no-op ack rather than
             an error.
          2. Cancel running tasks and let their sinks persist partials,
             exactly as a `CancelTask` does, bounded by a timeout. This
             is `_teardown()`, the same code path `Ctrl+C` takes, which
             is why Done-when #7 can say "the same end state as
             Ctrl+C" and mean it.
          3. Release contexts, close the database cleanly (also
             `_teardown()`).
          4. Ack -- one terminal `shutdown.complete`, put on the outbox
             before anything stops the writer. `main.py` drains the
             outbox before it cancels the writer, which is what makes
             this the last thing Go ever reads.
          5. STOPPED, and `shutdown_requested` lets `main.py` exit
             through its normal teardown.

        A second `Shutdown` arrives after `_torn_down` is set, so step 2
        and 3 are no-ops and it gets its own ack with
        `already_stopping: true`. Two acks for two commands; still
        exactly one per command.
        """
        already = self._torn_down
        await self._teardown()
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SHUTDOWN_COMPLETE,
            request_id=event.request_id,
            payload={
                "already_stopping": already,
                "status": self.state.status.value,
                "uptime_s": time.time() - self.state.started_at,
            },
        ))
        self.shutdown_requested.set()

    # -- event plumbing -----------------------------------------------

    def _make_stream_sink(
        self, request_id: str, session_id: str, message_id: str, *, background: bool = False,
    ):
        _EVENT_TYPE_MAP = {
            StreamEventType.TEXT_DELTA: OutgoingEventType.TEXT_DELTA,
            StreamEventType.THINKING_DELTA: OutgoingEventType.THINKING_DELTA,
            StreamEventType.STATUS_CHANGED: OutgoingEventType.STATUS_CHANGED,
            StreamEventType.PROGRESS_UPDATED: OutgoingEventType.PROGRESS_UPDATED,
            StreamEventType.USAGE_UPDATED: OutgoingEventType.USAGE_UPDATED,
            # B10. The step counter and the rate-limit notice both ride
            # PROGRESS_UPDATED above; these two are the tool pair.
            StreamEventType.TOOL_STARTED: OutgoingEventType.TOOL_STARTED,
            StreamEventType.TOOL_COMPLETED: OutgoingEventType.TOOL_COMPLETED,
        }
        echo_collected: list[str] = []  # only used for session.provider == "echo"

        async def sink(task: Task, chunk: dict[str, Any]) -> None:
            kind = chunk.get("type")

            if kind == "started":
                # The one ordering guarantee that matters: Task._run()
                # emits this before it pulls the first chunk out of the
                # body, so TaskStarted always precedes everything the
                # task causes.
                #
                # `background` (B11) rides this payload so a client can
                # tell, from TaskStarted alone, whether to keep draining
                # to a terminal event or treat the send as fired-and-
                # forgotten -- `cli.py send --wait` overrides the default
                # either way. Absent/false preserves every prior client's
                # behaviour exactly: an inline agent's TaskStarted looks
                # identical to what it always was.
                await self._emit(OutgoingEvent(
                    type=OutgoingEventType.TASK_STARTED,
                    request_id=request_id,
                    task_id=task.id,
                    session_id=session_id,
                    payload={"session": session_id, "message_id": message_id, "background": background},
                ))
                return

            if kind == "chunk":
                if "stream_event" in chunk:
                    raw = chunk["stream_event"]
                    out_type = _EVENT_TYPE_MAP.get(StreamEventType(raw["type"]))
                    if out_type is not None:
                        # seq lifted out of the payload onto the envelope.
                        # It stays in the payload too, so StreamAccumulator
                        # is untouched -- one counter, printed twice.
                        await self._emit(OutgoingEvent(
                            type=out_type, request_id=request_id, task_id=task.id,
                            session_id=session_id, seq=raw.get("sequence"), payload=raw,
                        ))
                    # B15: the partial reply reaches disk here, and
                    # nowhere else, while the task is alive. Only on a
                    # text delta: `StreamAccumulator.apply` appends TEXT
                    # deltas to `text` and nothing else does, so a
                    # progress or tool event has no new content to
                    # checkpoint. The accumulator is fed by
                    # `AgentExecutor` BEFORE it yields the chunk, so
                    # `accumulator.text` here already includes this
                    # delta.
                    if raw.get("type") == StreamEventType.TEXT_DELTA.value:
                        await self._maybe_checkpoint(task, session_id, message_id)
                else:
                    echo_collected.append(chunk.get("text", ""))
                    await self._emit(OutgoingEvent(
                        type=OutgoingEventType.TASK_TOKEN, request_id=request_id,
                        task_id=task.id, session_id=session_id,
                        seq=self.streams.next_sequence(task.id),
                        payload={"text": chunk.get("text", "")},
                    ))
                return

            # Terminal states: done / failed / cancelled
            outcome = _outcome_for(kind, chunk.get("code"), chunk.get("bound"))
            invocation = self.streams.invocation_for(task.id)
            # B20: whether this chunk persisted a message -- and so
            # whether the context actually changed and a report push is
            # warranted. Set at every `append_message` below, never
            # inferred from `kind` alone: a `failed`/`cancelled` turn
            # still enters history when the accumulator salvaged partial
            # text, and the report must reflect that turn too.
            persisted = False
            # B24: read BEFORE close_stream() below pops it -- both
            # branches call close_stream() well before the terminal
            # event is emitted, and the FillProposal push (after that
            # emit, never before/instead of it) needs this and the
            # reply text regardless of which branch persisted the
            # message.
            ec_record = self.streams.editor_context_for(task.id)
            fill_reply_text: Optional[str] = None
            if self.streams.has_stream(task.id):
                accumulator = self.streams.get_accumulator(task.id)
                if kind == "done":
                    msg = accumulator.finalize(invocation=invocation)
                    fill_reply_text = accumulator.text
                elif kind == "failed":
                    msg = accumulator.finalize_interrupted(
                        "failed", error=chunk.get("error"),
                        outcome=outcome, error_code=chunk.get("code"),
                        invocation=invocation,
                    )
                elif kind == "cancelled":
                    msg = accumulator.finalize_interrupted(
                        "cancelled", outcome=outcome, invocation=invocation,
                    )
                else:
                    msg = None
                if msg is not None:
                    # The guardrails hook (B9, no-op in v1): every
                    # persisted assistant reply, streamed or echoed,
                    # passes through here before it reaches the session.
                    #
                    # Wrapped: this is the one place a DB hiccup used to
                    # be fatal to the whole terminal event. A task whose
                    # body already finished (successfully or not) must
                    # still reach the client with exactly one terminal
                    # event even if writing its transcript row fails --
                    # the alternative is the task's own exception handler
                    # catching this, marking a *successful* task FAILED,
                    # and then the fallback sink call below crashing too
                    # with nothing left to catch it (the "Task exception
                    # was never retrieved" failure mode).
                    try:
                        msg = await self.agent_executor.apply_response_middleware(msg)
                        await self.sessions.append_message(msg)
                        persisted = True
                        await self._record_usage(task, msg)
                    except Exception:  # noqa: BLE001
                        _log.exception(
                            "Task %s: failed to persist its %r reply -- "
                            "the terminal event still goes out", task.id, kind,
                        )
                # B15: the checkpoint has done its job once the real row
                # is on disk. Cleared AFTER the insert, never before --
                # a crash in the gap between the two leaves a checkpoint
                # that startup promotion turns into an `interrupted`
                # row, which is wrong-but-honest; the other order leaves
                # nothing at all, which is the hole this whole mechanism
                # exists to close. A persist that FAILED above
                # deliberately keeps its checkpoint: it is then the only
                # copy of that text, and the next startup recovers it.
                if persisted or msg is None:
                    await self._clear_checkpoint(task.id)
                self.streams.close_stream(task.id)
            else:
                if kind == "done" and echo_collected:
                    full_reply = "".join(echo_collected)
                    fill_reply_text = full_reply
                    # The echo/refusal path persists a reply without ever
                    # registering a stream, so it needs its provenance
                    # and its outcome attached here rather than in the
                    # accumulator. Same row shape either way -- a
                    # reloaded transcript must not be able to tell which
                    # code path wrote a message.
                    msg = AssistantMessage(
                        session_id, full_reply, task_id=task.id, message_id=message_id,
                        invocation=invocation, outcome=Outcome.DONE,
                    )
                    try:
                        msg = await self.agent_executor.apply_response_middleware(msg)
                        await self.sessions.append_message(msg)
                        persisted = True
                        await self._record_usage(task, msg)
                    except Exception:  # noqa: BLE001
                        _log.exception(
                            "Task %s: failed to persist its echoed reply -- "
                            "the terminal event still goes out", task.id,
                        )
                # The echo path never registers a stream, so it never
                # checkpointed either (no stream, no deltas, nothing to
                # checkpoint). Cleared anyway: this is one DELETE by
                # task id, and "the path that cannot have left a row
                # does not clean up after itself" is an invariant that
                # only has to be wrong once.
                await self._clear_checkpoint(task.id)
                # The echo path never registers a stream, but it does draw
                # sequence numbers for its envelopes. Drop the counter or
                # it outlives the task it belonged to.
                self.streams.close_stream(task.id)

            try:
                await self.sessions.set_active_task(session_id, None)
            except Exception:  # noqa: BLE001
                _log.exception(
                    "Task %s: failed to clear active_task_id for session %s",
                    task.id, session_id,
                )
            self.state.active_task_count = self.tasks.active_count()

            out_type = {
                "done": OutgoingEventType.TASK_DONE,
                "failed": OutgoingEventType.TASK_FAILED,
                "cancelled": OutgoingEventType.TASK_CANCELLED,
            }[kind]
            # B10: `code` rides beside `error` so Go can tell
            # `bounds_exceeded` -- a loop that stopped at its boundary and
            # kept its partial answer -- from a genuine failure, without
            # parsing the message.
            payload = (
                {
                    "error": chunk.get("error", "unknown error"),
                    "code": chunk.get("code"),
                    # B12: the same value that was just persisted as the
                    # message's `outcome`, so a live client and a
                    # reloaded one render the identical footer without
                    # either of them deriving it independently.
                    "outcome": outcome.value,
                }
                if kind == "failed" else {}
            )
            await self._emit(OutgoingEvent(
                type=out_type, request_id=request_id, task_id=task.id,
                session_id=session_id, payload=payload,
            ))

            # B24: AFTER the terminal event above, never instead of it
            # and never before it (B6's one-terminal-event-per-task rule
            # holds; a client that stops listening at the terminal event
            # is not broken, it just has no diff card). Only for a
            # clean `done` -- a failed/cancelled turn's salvaged partial
            # text is the one case where syntactically valid JSON is
            # semantically worthless (the model was cut off mid-
            # `replacement`).
            if kind == "done" and ec_record is not None and fill_reply_text is not None:
                await self._push_fill_proposal(
                    request_id, task.id, session_id, ec_record, fill_reply_text,
                )

            # B20: pushed at the turn boundary, not per delta -- the
            # numbers only move once history actually gained a message.
            if persisted:
                await self._push_context_report(session_id)
                await self._push_usage_totals(session_id)

        return sink

    # -- partial-reply checkpoints (B15) ----------------------------------

    async def _maybe_checkpoint(
        self, task: Task, session_id: str, message_id: str,
    ) -> None:
        """Write the in-flight reply to `message_checkpoints`, at most
        once per CHECKPOINT_INTERVAL_S.

        The first delta writes immediately and then arms the timer: a
        task killed 200ms into its stream would otherwise leave nothing
        at all, which is the exact failure Done-when #3 tests.

        A checkpoint failure is logged and swallowed. This runs on the
        token path, and a database hiccup must not be able to kill a
        reply the user is watching arrive -- the checkpoint is insurance
        against a crash, not part of the delivery.
        """
        now = time.monotonic()
        due = self._checkpoint_at.get(task.id)
        if due is not None and now < due:
            return
        self._checkpoint_at[task.id] = now + CHECKPOINT_INTERVAL_S

        if not self.streams.has_stream(task.id):
            return
        accumulator = self.streams.get_accumulator(task.id)
        text = accumulator.text
        if not text:
            return
        invocation = self.streams.invocation_for(task.id)
        try:
            await self.checkpoints.write(
                message_id=message_id,
                session_id=session_id,
                task_id=task.id,
                content=text,
                invocation=invocation.to_dict() if invocation else None,
                input_tokens=accumulator.usage.input_tokens,
                output_tokens=accumulator.usage.output_tokens,
            )
        except Exception:  # noqa: BLE001
            _log.exception(
                "Task %s: checkpoint write failed -- the stream continues, "
                "but a crash from here would lose the partial reply", task.id,
            )

    async def _clear_checkpoint(self, task_id: str) -> None:
        self._checkpoint_at.pop(task_id, None)
        try:
            await self.checkpoints.clear_for_task(task_id)
        except Exception:  # noqa: BLE001
            # A checkpoint left behind is promoted at the next startup
            # into an `interrupted` row for a task that actually
            # finished -- wrong, but visible and bounded, where a raise
            # here would come out of the terminal-event path.
            _log.exception("Task %s: could not clear its checkpoint", task_id)

    async def _push_fill_proposal(
        self,
        request_id: Optional[str],
        task_id: str,
        session_id: str,
        ec_record: RegisteredEditorContext,
        reply_text: str,
    ) -> None:
        """B24: parse the Filler's reply against the region it was
        actually shown, and push it as its own event -- or don't. A
        reply that does not parse (a plain-text refusal, included) is
        logged once at WARNING and otherwise produces nothing: no
        `FillProposal`, no `Error`. A nullable payload would make every
        consumer write the same `if proposal == nil` branch to express
        what "no event arrived" already says for free.
        """
        result = parse_fill_proposal(
            reply_text,
            file=ec_record.context.file,
            shown_start=ec_record.shown_start,
            shown_end=ec_record.shown_end,
            mtime=ec_record.mtime,
            size=ec_record.size,
        )
        if isinstance(result, FillParseFailure):
            _log.warning(
                "Task %s: fill proposal (%s): %s", task_id, result.reason, result.detail,
            )
            return
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.FILL_PROPOSAL,
            request_id=request_id, task_id=task_id, session_id=session_id,
            payload={"proposal": result.to_dict()},
        ))

    # -- envelope ---------------------------------------------------------

    def _remember_envelope(self, event: IncomingEvent) -> None:
        """Keep an incoming request's envelope metadata so every event it
        causes can echo it. Handlers never see this: connection_id and
        trace_id are transport concerns, and threading them through
        seventeen handler signatures to reach the same serialization
        boundary would be the wrong shape."""
        self._correlation[event.request_id] = {
            "connection_id": event.connection_id,
            "trace_id": event.trace_id,
            "session_id": event.session_id
            or event.payload.get("session")
            or event.payload.get("session_id"),
        }
        self._correlation.move_to_end(event.request_id)
        while len(self._correlation) > _CORRELATION_LIMIT:
            self._correlation.popitem(last=False)

    def _fill_envelope(self, event: OutgoingEvent) -> OutgoingEvent:
        context = self._correlation.get(event.request_id) if event.request_id else None
        if context is None:
            return event
        return replace(
            event,
            session_id=event.session_id or context.get("session_id"),
            connection_id=event.connection_id or context.get("connection_id"),
            trace_id=event.trace_id or context.get("trace_id"),
        )

    async def _emit(self, event: OutgoingEvent) -> None:
        r"""The one serialization boundary, and now the one persistence
        boundary too (B40).

        The log is written from HERE, not from `main.py`'s writer loop,
        and that was B40's call rather than an accident of convenience:
        the writer loop is transport, it holds no database handle, and a
        log that only records what was successfully written to stdout
        cannot record the envelopes that mattered when stdout died.

        Order matters. The envelope is filled first (so the log stores
        the same `session_id` the client will see), then persisted (so
        the assigned cursor exists), then put on the outbox carrying
        that cursor as `event_seq`. Putting it on the outbox first would
        be faster and would emit envelopes whose `event_seq` is null
        even though the row exists -- a client marking its high-water
        mark would skip them and replay them forever.

        The event log is a recovery aid and the envelope is the product,
        so **nothing the log does may stop an envelope going out.**
        `EventRepository.append` already catches operational failures (a
        locked database, a full disk) and returns None, which is the
        first line of that. The `try` here is the second, and it is not
        redundant: the guarantee belongs to the boundary that depends on
        it. Without it, any defect in the log path -- not just the
        failures `append` anticipated -- turns into a reply the user
        waiting for it never receives, which is a far worse outcome than
        a degraded replay. B40's own check caught this by stubbing a
        raising `append` and watching the request die.
        """
        filled = self._fill_envelope(event)
        try:
            event_seq = await self.events.append(
                event_id=filled.event_id,
                session_id=filled.session_id,
                task_id=filled.task_id,
                corr_id=filled.request_id,
                event_type=filled.type.value,
                stream_seq=filled.seq,
                ts=filled.timestamp,
                payload=filled.payload,
            )
        except Exception:
            _log.warning(
                "event log raised for %s; emitting with no event_seq",
                filled.type.value, exc_info=True,
            )
            event_seq = None
        if event_seq is not None:
            filled = replace(filled, event_seq=event_seq)
        await self.outbox.put(filled)

    async def emit_hello(self) -> None:
        """First envelope on the wire, before anything else, caused by no
        request -- so corr_id is null. It must be emitted even when
        nothing at all is configured: a UI that knows the backend is
        alive and unconfigured can explain itself, where a UI waiting for
        a handshake that never comes just hangs.

        `providers` lists what is *registered*, not what is reachable.
        Ollama registers without a key, so a keyless profile reports
        ["ollama"]; whether the daemon is actually up is a health
        question, and ListProviders is what answers it.
        """
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.HELLO,
            request_id=None,
            payload={
                "v": ENVELOPE_VERSION,
                "capabilities": {
                    "providers": [
                        name for name in self.providers.list()
                        if getattr(self.providers.get(name), "selectable", True)
                    ],
                    "schema_version": SCHEMA_VERSION,
                    "streaming": True,
                    "cancel": True,
                },
            },
        ))

    def start_heartbeat(self, interval: float = HEARTBEAT_INTERVAL_S) -> None:
        """ping every 5s (project_idea.md §4 rule 6). Two missed pings is
        how Go catches a *hung* backend -- the failure mode process-exit
        detection misses entirely.

        "while idle" is gone as of B15/D98: a silent task is not proof of
        life, and the ping is now emitted whatever the backend is doing.
        See `_heartbeat_loop`.

        The same loop runs the in-memory task sweep, which is what makes
        TaskManager.sweep_finished's docstring true for the first time.
        """
        if self._heartbeat is None:
            self._heartbeat = asyncio.create_task(self._heartbeat_loop(interval), name="heartbeat")

    async def _heartbeat_loop(self, interval: float) -> None:
        """B15 (decisions.md **D98**): the ping is UNCONDITIONAL.

        It used to be skipped whenever `self.tasks.active_count() > 0`,
        on the reasoning in `start_heartbeat`'s own docstring -- "tokens
        are already proving liveness". That is true of a streaming reply
        and false of a tool call. B10's `web_search` against a slow
        endpoint emits `ToolStarted` and then nothing at all until
        `ToolCompleted`; a large `read_file` behaves the same. Inside
        that window the backend produced no tokens AND no pings, so the
        wire was indistinguishable from a deadlocked process -- during
        precisely the operation most likely to deadlock, the one waiting
        on something external.

        Go carried the workaround for this backend decision: `busyGrace`
        in `frontend\\internal\\protocol\\client.go` stretched the
        watchdog by 60s whenever it believed a task was in flight, so a
        genuine hang mid-task took ~72s to detect instead of ~12s. B15
        deletes `busyGrace`, which it can only do because of this
        change.

        `active_tasks` stays in the payload and now carries the real
        count rather than a hardcoded 0 -- a ping during a task is the
        new case, and reporting 0 through it would be a fresh lie in
        place of the one just removed.
        """
        while True:
            await asyncio.sleep(interval)
            self.tasks.sweep_finished()
            # B40 retention. Same loop, same reason the task sweep is
            # here: a bounded store needs something that bounds it, and
            # the heartbeat is the only thing in this process that runs
            # forever and is not on a request path. `maybe_sweep` is
            # throttled internally (every ~5 min, not every 5s) -- the
            # cadence belongs with the caps, in the repository, not
            # spread across this loop.
            await self.events.maybe_sweep()
            if not self.state.is_accepting_events():
                continue
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.PING,
                request_id=None,
                payload={
                    "active_tasks": self.tasks.active_count(),
                    "uptime_s": time.time() - self.state.started_at,
                },
            ))

    # -- durable task mirror ----------------------------------------------

    async def _record_task(self, task: Task) -> None:
        """Every Task state transition lands here, from the one place
        transitions happen. agent_id is set by B9's AgentExecutor at
        task-creation time and simply carried through; project_id /
        connection_id / git_checkpoint_sha stay NULL in v1 -- they are
        the hooks for B7, SSH, and the parked Coding Assistant.

        B10: `error_code` is now written for live failures too. Until
        this episode the column only ever held `backend_died`, written by
        the startup sweeper -- a task that failed while the process was
        alive left the column NULL, so "why did this task fail" was
        answerable for crashes and nothing else."""
        await self.task_repo.record(
            task_id=task.id,
            session_id=task.session_id,
            kind=task.kind,
            status=task.state.value,
            created_at=task.created_at,
            started_at=task.started_at,
            finished_at=task.finished_at,
            error_code=task.result.code if task.result else None,
            agent_id=task.agent_id,
        )

def _editor_context_from_payload(payload: Optional[dict[str, Any]]) -> Optional[EditorContext]:
    """SendMessage's optional `editor_context` object -> an EditorContext
    (B11). None on the wire means no EditorContext at all -- `cli.py`
    only sends one when `--file` is given, and a real editor (F11) only
    sends one when it is the invocation site."""
    if not payload:
        return None
    return EditorContext.from_dict(payload)


def _bounds_override(payload: Optional[dict[str, Any]]) -> Optional[BoundsOverride]:
    """SendMessage's optional `bounds` object -> a BoundsOverride (B10).

    Returns None when the key is absent, which is the normal case: no
    bounds on the wire means the agent's own config is the only thing
    that decides. An empty object is treated the same way -- an override
    that overrides nothing is not an override.
    """
    if not payload:
        return None
    return BoundsOverride(
        max_steps=payload.get("max_steps"),
        max_tool_calls=payload.get("max_tool_calls"),
        timeout_s=payload.get("timeout_s"),
    )
