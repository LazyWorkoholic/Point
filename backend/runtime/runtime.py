"""Runtime: the coordinator. Owns managers, never does their job."""

from __future__ import annotations

import asyncio
import time
from collections import OrderedDict
from dataclasses import replace
from pathlib import Path
from typing import Any, Optional, Union

import aiosqlite

from agents.executor import AgentExecutor
from agents.registry import AgentRegistry
from database.connection import open_database
from database.migrations import SCHEMA_VERSION
from database.task_repository import TaskRepository
from logging_setup import get_logger
from managers.context import SessionMemoryService
from managers.project import Project
from managers.project_manager import ProjectManager
from managers.session import Session
from managers.session_manager import SessionManager
from runtime.dispatcher import Dispatcher
from runtime.errors import InvalidEventError, ProjectNotFoundError, RuntimeException, TaskNotFoundError
from runtime.events import (
    ENVELOPE_VERSION,
    IncomingEvent,
    IncomingEventType,
    OutgoingEvent,
    OutgoingEventType,
)
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


_log = get_logger("runtime")

# How many requests to remember for envelope echo. Bounded because a
# long-lived backend would otherwise grow one entry per request forever;
# 1000 is far more than the number of requests that can still have events
# in flight.
_CORRELATION_LIMIT = 1000

HEARTBEAT_INTERVAL_S = 5.0


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
        self.agents = AgentRegistry(db_conn)
        self.agent_executor = AgentExecutor(
            self.sessions, self.memory, self.tasks, self.providers,
            self.streams, self.stream_adapter, self._resolve_provider_name,
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

        for task in self.tasks.list_active():
            task.cancel()
        # One loop turn, so each cancelled task reaches its except branch
        # and writes its terminal row instead of being buried by the next
        # startup's crash recovery.
        await asyncio.sleep(0)

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
        self.dispatcher.register(IncomingEventType.PROJECT_INIT, self._handle_project_init)
        self.dispatcher.register(IncomingEventType.PROJECT_RESOLVE, self._handle_project_resolve)
        self.dispatcher.register(IncomingEventType.LIST_PROJECT_FILES, self._handle_list_project_files)
        self.dispatcher.register(IncomingEventType.READ_PROJECT_FILE, self._handle_read_project_file)
        self.dispatcher.register(IncomingEventType.ACQUIRE_FILE_LOCK, self._handle_acquire_file_lock)
        self.dispatcher.register(IncomingEventType.HEARTBEAT_FILE_LOCK, self._handle_heartbeat_file_lock)
        self.dispatcher.register(IncomingEventType.RELEASE_FILE_LOCK, self._handle_release_file_lock)
        self.dispatcher.register(IncomingEventType.TAKE_OVER_FILE_LOCK, self._handle_take_over_file_lock)
        self.dispatcher.register(IncomingEventType.CHECK_FILE_STALE, self._handle_check_file_stale)

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
            await self._emit(OutgoingEvent(
                type=OutgoingEventType.ERROR,
                request_id=event.request_id,
                payload={"error": str(exc)},
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
        session = await self.sessions.get(session_id, load_messages=True)
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.SESSION,
            request_id=event.request_id,
            payload={"session": self._session_to_dict(session, include_messages=True)},
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
        agent = await self.agents.resolve_for_session(session)

        # B9: AgentExecutor owns "resolve config -> build context ->
        # create task -> stream -> emit -> persist" end to end; this
        # handler's job is reduced to "who, and with which agent". A
        # session with NULL provider/model is no longer refused here
        # unconditionally -- task override > session > agent default >
        # global config means it may still resolve. The executor raises
        # InvalidEventError (-> a clean Error event) only when nothing in
        # that whole chain names a provider/model.
        run = await self.agent_executor.run(
            session=session,
            text=text,
            agent=agent,
            make_sink=lambda message_id: self._make_stream_sink(event.request_id, session_id, message_id),
            inline=True,
        )
        self.state.active_task_count = self.tasks.active_count()

        await self._emit(OutgoingEvent(
            type=OutgoingEventType.TASK_STARTED,
            request_id=event.request_id,
            task_id=run.task.id,
            session_id=session_id,
            payload={"session": session_id, "message_id": run.message_id},
        ))

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
        tasks = self.tasks.list_all()
        await self._emit(OutgoingEvent(
            type=OutgoingEventType.TASK_LIST,
            request_id=event.request_id,
            payload={
                "tasks": [
                    {"id": t.id, "kind": t.kind, "state": t.state.value, "session": t.session_id}
                    for t in tasks
                ]
            },
        ))

    async def _handle_shutdown(self, event: IncomingEvent) -> None:
        self.state.status = RuntimeStatus.STOPPING
        for task in self.tasks.list_active():
            task.cancel()
        self.state.status = RuntimeStatus.STOPPED

    # -- event plumbing -----------------------------------------------

    def _make_stream_sink(self, request_id: str, session_id: str, message_id: str):
        _EVENT_TYPE_MAP = {
            StreamEventType.TEXT_DELTA: OutgoingEventType.TEXT_DELTA,
            StreamEventType.THINKING_DELTA: OutgoingEventType.THINKING_DELTA,
            StreamEventType.STATUS_CHANGED: OutgoingEventType.STATUS_CHANGED,
            StreamEventType.PROGRESS_UPDATED: OutgoingEventType.PROGRESS_UPDATED,
            StreamEventType.USAGE_UPDATED: OutgoingEventType.USAGE_UPDATED,
        }
        echo_collected: list[str] = []  # only used for session.provider == "echo"

        async def sink(task: Task, chunk: dict[str, Any]) -> None:
            kind = chunk.get("type")

            if kind == "started":
                return  # Runtime already emitted TaskStarted before the body ran

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
            if self.streams.has_stream(task.id):
                accumulator = self.streams.get_accumulator(task.id)
                if kind == "done":
                    msg = accumulator.finalize()
                elif kind == "failed":
                    msg = accumulator.finalize_interrupted("failed", error=chunk.get("error"))
                elif kind == "cancelled":
                    msg = accumulator.finalize_interrupted("cancelled")
                else:
                    msg = None
                if msg is not None:
                    # The guardrails hook (B9, no-op in v1): every
                    # persisted assistant reply, streamed or echoed,
                    # passes through here before it reaches the session.
                    msg = await self.agent_executor.apply_response_middleware(msg)
                    await self.sessions.append_message(msg)
                self.streams.close_stream(task.id)
            else:
                if kind == "done" and echo_collected:
                    full_reply = "".join(echo_collected)
                    msg = AssistantMessage(session_id, full_reply, task_id=task.id, message_id=message_id)
                    msg = await self.agent_executor.apply_response_middleware(msg)
                    await self.sessions.append_message(msg)
                # The echo path never registers a stream, but it does draw
                # sequence numbers for its envelopes. Drop the counter or
                # it outlives the task it belonged to.
                self.streams.close_stream(task.id)

            await self.sessions.set_active_task(session_id, None)
            self.state.active_task_count = self.tasks.active_count()

            out_type = {
                "done": OutgoingEventType.TASK_DONE,
                "failed": OutgoingEventType.TASK_FAILED,
                "cancelled": OutgoingEventType.TASK_CANCELLED,
            }[kind]
            payload = {"error": chunk.get("error", "unknown error")} if kind == "failed" else {}
            await self._emit(OutgoingEvent(
                type=out_type, request_id=request_id, task_id=task.id,
                session_id=session_id, payload=payload,
            ))

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
        the hooks for B7, SSH, and the parked Coding Assistant."""
        await self.task_repo.record(
            task_id=task.id,
            session_id=task.session_id,
            kind=task.kind,
            status=task.state.value,
            created_at=task.created_at,
            started_at=task.started_at,
            finished_at=task.finished_at,
            agent_id=task.agent_id,
        )