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
from agents.executor import AgentExecutor, BoundsOverride
from agents.registry import AgentRegistry
from database.connection import open_database
from database.migrations import SCHEMA_VERSION
from database.proposal_repository import ProposalRepository
from database.task_repository import TaskRepository
from database.tool_call_repository import ToolCallRepository
from logging_setup import get_logger
from managers.agent import AgentConfig
from managers.attachment import MODE_FULL
from managers.attachment_manager import AttachmentManager
from managers import context
from managers.context import SessionMemoryService
from managers.invocation import parse_invocation
from managers.editor_context import EditorContext
from managers.message import MessageRole, Outcome
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
from runtime.scheduler import turn_lane, turn_queue_depth
from runtime.state import RuntimeState, RuntimeStatus
from runtime.task import Task
from runtime.task_manager import TaskManager
from managers.message import AssistantMessage
from dataclasses import asdict
from providers.bootstrap import build_registry_from_env, create_provider
from providers.errors import ProviderError
from providers.models import ProviderConfig
from streaming.adapter import StreamAdapter
from streaming.events import StreamEventType
from streaming.manager import StreamManager
from tools.registry import build_default_registry


_log = get_logger("runtime")

# How many requests to remember for envelope echo. Bounded because a
# long-lived backend would otherwise grow one entry per request forever;
# 1000 is far more than the number of requests that can still have events
# in flight.
_CORRELATION_LIMIT = 1000

HEARTBEAT_INTERVAL_S = 5.0


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
        self.agent_executor = AgentExecutor(
            self.sessions, self.memory, self.tasks, self.providers,
            self.streams, self.stream_adapter, self._resolve_provider_name,
            projects=self.projects,
            tools=self.tools,
            tool_recorder=self.tool_calls.record,
            proposal_repo=self.proposals,
        )

        # request id -> the envelope metadata that came in with it, so
        # connection_id / trace_id / session_id can be echoed on every
        # event that request causes without any handler carrying them.
        self._correlation: OrderedDict[str, dict[str, Optional[str]]] = OrderedDict()
        self._heartbeat: Optional[asyncio.Task] = None

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
        database. Idempotent."""
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
                    asyncio.gather(*(task.wait() for task in active)), timeout=5.0,
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

        try:
            await self.db.close()
        except Exception as exc:  # noqa: BLE001
            _log.warning("Error closing database: %s", exc)
        self.state.status = RuntimeStatus.STOPPED

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
        self.dispatcher.register(IncomingEventType.ACQUIRE_FILE_LOCK, self._handle_acquire_file_lock)
        self.dispatcher.register(IncomingEventType.HEARTBEAT_FILE_LOCK, self._handle_heartbeat_file_lock)
        self.dispatcher.register(IncomingEventType.RELEASE_FILE_LOCK, self._handle_release_file_lock)
        self.dispatcher.register(IncomingEventType.TAKE_OVER_FILE_LOCK, self._handle_take_over_file_lock)
        self.dispatcher.register(IncomingEventType.CHECK_FILE_STALE, self._handle_check_file_stale)
        self.dispatcher.register(IncomingEventType.LIST_AGENTS, self._handle_list_agents)
        self.dispatcher.register(IncomingEventType.LIST_DIRECTORY, self._handle_list_directory)

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
        session = await self.sessions.create(
            title=p.get("title", "New Session"),
            provider=p.get("provider"),
            model=p.get("model"),
            workspace=p.get("workspace"),
            project_id=p.get("project_id"),
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
            try:
                health = await provider.health()
                results.append({"name": name, **asdict(health)})
            except ProviderError as exc:
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
        self.providers.register(name, instance)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROVIDER_API_KEY_SET,
            request_id=event.request_id,
            payload={"provider": name},
        ))

    async def _handle_set_session_provider(self, event: IncomingEvent) -> None:
        session_id = event.payload.get("session_id")
        provider = event.payload.get("provider")
        model = event.payload.get("model")
        if not session_id or not provider or not model:
            raise InvalidEventError("SetSessionProvider requires 'session_id', 'provider', and 'model'")
        await self.sessions.set_provider(session_id, provider, model)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION_PROVIDER_UPDATED,
            request_id=event.request_id,
            payload={"session_id": session_id, "provider": provider, "model": model},
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
        result = await self.projects.read_file(
            project, p["path"], allow_outside_project=bool(p.get("allow_outside_project", False))
        )
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.PROJECT_FILE,
            request_id=event.request_id,
            payload={"file": result},
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
        p = event.payload
        if "path" not in p or "mtime" not in p:
            raise InvalidEventError("CheckFileStale requires 'path' and 'mtime'")
        project = await self._resolve_optional_project(p)
        changed = await self.projects.check_stale(
            project, p["path"], float(p["mtime"]),
            allow_outside_project=bool(p.get("allow_outside_project", False)),
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

        stale_after_days = appconfig.config_value("research.stale_after_days", 7)
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
        if project is None:
            project = await self._adopt_project_for(session, path)
        mode = p.get("mode") or await self._default_attach_mode(session)
        attachment, resolution = await self.attachments.attach(
            session_id, path,
            project=project,
            mode=mode,
            allow_outside_project=allow_outside,
            model=session.model,
        )
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

    async def _adopt_project_for(self, session: Session, path: str) -> Optional[Project]:
        r"""Bind an unbound session to the project the attached file
        lives in, and return it.

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
        await self.sessions.set_project(session.id, project.id)
        _log.info(
            "Session %s adopted project %s (%s) from its first attachment",
            session.id, project.id, project.root_path,
        )
        return project

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
        allow_outside = bool(appconfig.config_value("project.allow_outside_project", False))
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

    async def _handle_shutdown(self, event: IncomingEvent) -> None:
        self.state.status = RuntimeStatus.STOPPING
        for task in self.tasks.list_active():
            task.cancel()
        self.state.status = RuntimeStatus.STOPPED

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
            if self.streams.has_stream(task.id):
                accumulator = self.streams.get_accumulator(task.id)
                if kind == "done":
                    msg = accumulator.finalize(invocation=invocation)
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
                    except Exception:  # noqa: BLE001
                        _log.exception(
                            "Task %s: failed to persist its %r reply -- "
                            "the terminal event still goes out", task.id, kind,
                        )
                self.streams.close_stream(task.id)
            else:
                if kind == "done" and echo_collected:
                    full_reply = "".join(echo_collected)
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
                    except Exception:  # noqa: BLE001
                        _log.exception(
                            "Task %s: failed to persist its echoed reply -- "
                            "the terminal event still goes out", task.id,
                        )
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

            # B20: pushed at the turn boundary, not per delta -- the
            # numbers only move once history actually gained a message.
            if persisted:
                await self._push_context_report(session_id)

        return sink

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
        await self.outbox.put(self._fill_envelope(event))

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
        """ping every 5s while idle (project_idea.md §4 rule 6). Two
        missed pings is how Go catches a *hung* backend -- the failure
        mode process-exit detection misses entirely.

        The same loop runs the in-memory task sweep, which is what makes
        TaskManager.sweep_finished's docstring true for the first time.
        """
        if self._heartbeat is None:
            self._heartbeat = asyncio.create_task(self._heartbeat_loop(interval), name="heartbeat")

    async def _heartbeat_loop(self, interval: float) -> None:
        while True:
            await asyncio.sleep(interval)
            self.tasks.sweep_finished()
            if not self.state.is_accepting_events():
                continue
            if self.tasks.active_count() > 0:
                continue  # not idle: tokens are already proving liveness
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.PING,
                request_id=None,
                payload={"active_tasks": 0, "uptime_s": time.time() - self.state.started_at},
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
