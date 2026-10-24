"""The wire format. One envelope, both directions (project_idea.md §4).

Adopted in B6, while no Go client exists -- there is exactly one side to
change and zero compatibility burden, and that window closes the moment
F2 lands.

    { "v": 1, "id": "...", "type": "task.token", "corr_id": "...",
      "session_id": "...", "task_id": "...", "connection_id": "...",
      "trace_id": "...", "seq": 1, "ts": ..., "payload": {} }

This is a **serialization-boundary change only**. IncomingEvent and
OutgoingEvent keep their fields and their names; what changed is
`from_dict()` and `to_dict()`. No handler signature moved.

    before                              after
    event                               type
    request_id                          corr_id
    timestamp                           ts
    task_id, sometimes present          task_id, always present, may be null
    payload keys flattened to top       payload nested under "payload"
    --                                  v, id, session_id, connection_id,
                                        trace_id, seq

`ts` stays a float epoch rather than becoming an ISO string: the field
did not change, only the key it serializes under.

`connection_id` and `trace_id` are accepted on input and echoed on
output, otherwise unused -- the SSH and tracing hooks.

`seq` is the per-task stream sequence, lifted out of the stream_event
payload (where it still appears, so StreamAccumulator is untouched). It
is null on envelopes that are not part of a task's token stream; a
lifecycle event is identified by its type, not by counting.

`event_seq` (B40) is a SECOND, unrelated cursor, and the distinction is
load-bearing: it is the durable, per-session, monotonic position of this
envelope in `events` (migration v8). It was added as a new field rather
than by redefining `seq`, which is the additive rule B1-B5 are frozen
under and the pattern B18, B24 and B36 all followed -- `seq` still means
exactly what it meant, so StreamAccumulator, F5's stream ordering and
F18's pagination all keep working untouched. Redefining `seq` would have
been one field instead of two and a contract change arriving disguised
as the same field, which is the kind a frontend episode finds six weeks
later.

`event_seq` is null on any envelope the log does not hold: one with no
session, and the three token-stream types `database\event_repository.py`
excludes. A client marks its high-water mark from the non-null ones and
asks `EventsSince` for the gap.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

from runtime.errors import InvalidEventError, UnknownEventTypeError

ENVELOPE_VERSION = 1


# ---------------------------------------------------------------------------
# Incoming event types (Go -> Python)
# ---------------------------------------------------------------------------

class IncomingEventType(str, Enum):
    SEND_MESSAGE = "SendMessage"
    CANCEL_TASK = "CancelTask"
    LIST_TASKS = "ListTasks"
    SHUTDOWN = "Shutdown"
    CREATE_SESSION = "CreateSession"
    GET_SESSION = "GetSession"
    # B18: keyset scrollback. Consumed by F18; independently verifiable
    # through cli.py's `session messages` without it.
    SESSION_MESSAGES = "SessionMessages"
    LIST_SESSIONS = "ListSessions"
    RENAME_SESSION = "RenameSession"
    ARCHIVE_SESSION = "ArchiveSession"
    UNARCHIVE_SESSION = "UnarchiveSession"
    DELETE_SESSION = "DeleteSession"
    UPDATE_SESSION_METADATA = "UpdateSessionMetadata"
    UPDATE_SESSION_SETTINGS = "UpdateSessionSettings"
    LIST_PROVIDERS = "ListProviders"
    LIST_PROVIDER_MODELS = "ListProviderModels"
    SET_PROVIDER_API_KEY = "SetProviderApiKey"
    SET_SESSION_PROVIDER = "SetSessionProvider"
    # Project system (B7).
    PROJECT_INIT = "ProjectInit"
    PROJECT_RESOLVE = "ProjectResolve"
    LIST_PROJECT_FILES = "ListProjectFiles"
    READ_PROJECT_FILE = "ReadProjectFile"
    # B32: the editor's save, and the FIRST write path in the backend.
    # A runtime event handler only -- there is no `write_file` tool and
    # there must not be one. CLAUDE.md's first hard rule ("never write
    # files from an agent") is enforced structurally, by the absence of
    # a tool, not by a check inside one.
    WRITE_PROJECT_FILE = "WriteProjectFile"
    ACQUIRE_FILE_LOCK = "AcquireFileLock"
    HEARTBEAT_FILE_LOCK = "HeartbeatFileLock"
    RELEASE_FILE_LOCK = "ReleaseFileLock"
    TAKE_OVER_FILE_LOCK = "TakeOverFileLock"
    CHECK_FILE_STALE = "CheckFileStale"
    # Attachments (B12). B8 built the read half; these are the writes.
    ATTACH_CONTEXT = "AttachContext"
    DETACH_CONTEXT = "DetachContext"
    SET_ATTACHMENT_MODE = "SetAttachmentMode"
    LIST_ATTACHMENTS = "ListAttachments"
    RESOLVE_ATTACHMENT = "ResolveAttachment"
    # B20: the on-demand pull for what B8's context.report() already
    # computes. Payload mirrors `cli.py context report`: an optional
    # `session_id`, absent meaning the most recently updated session.
    GET_CONTEXT_REPORT = "GetContextReport"
    # B23: F8's `@` picker. ListAgents needs no session and no project --
    # AgentRegistry.list() already returns every registered agent.
    LIST_AGENTS = "ListAgents"
    # B23: a plain, uncached directory listing for the picker's
    # filesystem-browse mode. Distinct from LIST_PROJECT_FILES, which
    # requires a resolved project and serves the indexed/gitignore-aware/
    # cached file tree -- this lists one folder's immediate children,
    # with or without a project, live.
    LIST_DIRECTORY = "ListDirectory"
    # B26: Settings-tab writes for values with no in-memory layer before
    # this episode. Payload mirrors DetachContext's shape -- same key,
    # presence of 'value' changes the verb (set vs. clear) -- rather than
    # a second event type for clearing one override.
    SET_BEHAVIOUR_OVERRIDE = "SetBehaviourOverride"
    # B26: the About tab's `point doctor` action, as data instead of stdout.
    GET_DOCTOR_REPORT = "GetDoctorReport"
    # B15: the backend-side unload of one workspace. Keyed by the
    # WORKSPACE id Go names ("chat", "editor"), which the backend
    # resolves to sessions and those to project ids -- nothing in the
    # backend is keyed by a workspace label, and this episode did not
    # make anything so. Never cancels a task: unloading a view is a UI
    # event, and the refusal rule in `runtime\lifecycle.py` is what
    # makes that safe rather than a hope.
    WORKSPACE_UNLOAD = "WorkspaceUnload"
    # B40: the replay rung of F2's recovery ladder, served from the
    # durable event log (migration v8) -- the command F2 was written
    # against and B15 could not serve.
    #
    # B15 shipped `SessionReplaySince` here instead: message-level
    # replay keyed on `messages.seq`, named for what it did, with
    # `mechanism` on the wire so no consumer had to guess which `seq` it
    # held. **B40 DELETED it rather than keeping it beside this.** Two
    # replay commands with two different cursors is the "second copy
    # that can disagree" this repo removes on sight (`managers\provider.py`,
    # `managers\tool.py`).
    #
    # Payload: {session, since_seq, limit}. `since_seq` is the
    # `event_seq` cursor (`events.session_seq`), NOT `messages.seq` and
    # NOT the per-task stream `seq`. 0 or absent means everything the log
    # still holds -- which retention bounds, so "everything" is honest
    # rather than complete, and `has_more` is how a caller finds out.
    EVENTS_SINCE = "EventsSince"


@dataclass(frozen=True)
class IncomingEvent:

    type: IncomingEventType
    request_id: str
    payload: dict[str, Any] = field(default_factory=dict)
    # Envelope metadata. Accepted here so it can be echoed back on every
    # event this request causes; no handler reads them.
    session_id: Optional[str] = None
    task_id: Optional[str] = None
    connection_id: Optional[str] = None
    trace_id: Optional[str] = None

    @staticmethod
    def from_dict(raw: dict[str, Any]) -> "IncomingEvent":
        version = raw.get("v", ENVELOPE_VERSION)
        if version != ENVELOPE_VERSION:
            raise InvalidEventError(
                f"Envelope version mismatch: backend speaks v{ENVELOPE_VERSION}, "
                f"client sent v{version}"
            )

        if "type" not in raw:
            raise InvalidEventError("Missing required field: 'type'")

        raw_type = raw["type"]
        try:
            event_type = IncomingEventType(raw_type)
        except ValueError as exc:
            raise UnknownEventTypeError(f"Unknown event type: {raw_type!r}") from exc

        payload = raw.get("payload") or {}
        if not isinstance(payload, dict):
            raise InvalidEventError("'payload' must be an object")

        return IncomingEvent(
            type=event_type,
            request_id=raw.get("id") or str(uuid.uuid4()),
            payload=payload,
            session_id=raw.get("session_id"),
            task_id=raw.get("task_id"),
            connection_id=raw.get("connection_id"),
            trace_id=raw.get("trace_id"),
        )


# ---------------------------------------------------------------------------
# Outgoing event types (Python -> Go)
# ---------------------------------------------------------------------------

class OutgoingEventType(str, Enum):
    TASK_STARTED = "TaskStarted"
    TASK_TOKEN = "TaskToken"
    TASK_DONE = "TaskDone"
    TASK_FAILED = "TaskFailed"
    TASK_CANCELLED = "TaskCancelled"
    # Non-terminal ack for CancelTask. Before B6 the ack was itself a
    # TaskCancelled, so a cancel produced two terminal events and
    # "exactly one terminal event per task" was false. The sink is now
    # the only producer of TASK_CANCELLED.
    TASK_CANCEL_ACCEPTED = "task.cancel.accepted"
    TASK_LIST = "TaskList"
    ERROR = "Error"
    SESSION_CREATED = "SessionCreated"
    SESSION = "Session"
    # B18: reply to SessionMessages.
    SESSION_MESSAGE_LIST = "SessionMessageList"
    SESSION_LIST = "SessionList"
    SESSION_RENAMED = "SessionRenamed"
    SESSION_ARCHIVED = "SessionArchived"
    SESSION_UNARCHIVED = "SessionUnarchived"
    SESSION_DELETED = "SessionDeleted"
    SESSION_METADATA_UPDATED = "SessionMetadataUpdated"
    SESSION_SETTINGS_UPDATED = "SessionSettingsUpdated"
    PROVIDER_LIST = "ProviderList"
    PROVIDER_MODEL_LIST = "ProviderModelList"
    PROVIDER_API_KEY_SET = "ProviderApiKeySet"
    SESSION_PROVIDER_UPDATED = "SessionProviderUpdated"
    TEXT_DELTA = "TextDelta"
    THINKING_DELTA = "ThinkingDelta"
    STATUS_CHANGED = "StatusChanged"
    PROGRESS_UPDATED = "ProgressUpdated"
    USAGE_UPDATED = "UsageUpdated"
    # Tool system (B10). `task.progress` in the episode's prose is this
    # enum's existing PROGRESS_UPDATED -- the step counter and the
    # rate-limit notice both ride it, rather than a third progress
    # vocabulary being invented next to the two that already exist.
    TOOL_STARTED = "ToolStarted"
    TOOL_COMPLETED = "ToolCompleted"
    # Project system (B7).
    PROJECT = "Project"
    PROJECT_FILE_LIST = "ProjectFileList"
    PROJECT_FILE = "ProjectFile"
    # B32: reply to WriteProjectFile. A NEW type rather than a reuse of
    # PROJECT_FILE -- the two payloads have almost nothing in common, and
    # overloading one would make both decoders branch on which keys
    # arrived (the same reason CONTEXT_REPORT's own comment gives for one
    # shape, one decoder).
    #
    # `{ok: true, ...}` carries what was ACTUALLY written, including the
    # new mtime -- which is the whole of F13's autosave rule 2, and why
    # a save needs no follow-up CheckFileStale. `{ok: false, reason}`
    # carries one of seven slugs; a path denial or an unresolvable
    # project is NOT among them and arrives as an Error, because those
    # mean the editor should never have offered the save.
    PROJECT_FILE_WRITTEN = "ProjectFileWritten"
    FILE_LOCK_RESULT = "FileLockResult"
    FILE_LOCK_RELEASED = "FileLockReleased"
    FILE_STALE_RESULT = "FileStaleResult"
    # Attachments (B12).
    ATTACHMENT = "Attachment"
    ATTACHMENT_LIST = "AttachmentList"
    ATTACHMENT_DETACHED = "AttachmentDetached"
    ATTACHMENT_RESOLUTION = "AttachmentResolution"
    # B20: ContextReport.to_dict() verbatim, both as the reply to
    # GetContextReport (corr_id set) and as the push after a turn's
    # assistant message is persisted (corr_id null) -- one shape, one
    # decoder, not two.
    CONTEXT_REPORT = "ContextReport"
    # B24: pushed after the terminal TaskDone/TaskFailed/TaskCancelled
    # event for a fill-mode turn whose reply parsed as a valid
    # `FillProposal` -- never instead of the terminal event, never
    # before it, and never for a reply that failed to parse (that is a
    # log line, not an event). `corr_id` is the originating
    # SendMessage's, like the terminal event -- this is caused by a
    # request, unlike CONTEXT_REPORT's push.
    FILL_PROPOSAL = "FillProposal"
    # B23: reply to ListAgents / ListDirectory.
    AGENT_LIST = "AgentList"
    DIRECTORY_LIST = "DirectoryList"
    # B26: reply to SetBehaviourOverride -- carries the full current
    # override map, not just the key just changed, matching
    # ProviderApiKeySet's precedent of echoing back resulting state.
    BEHAVIOUR_OVERRIDE_SET = "BehaviourOverrideSet"
    # B26: reply to GetDoctorReport. build_doctor_report()'s dict verbatim.
    DOCTOR_REPORT = "DoctorReport"
    # B15: the reply to WorkspaceUnload -- `ReleaseReport.to_dict()`
    # verbatim. Three lists, not a bool: `released` (dropped, with a
    # count), `refused` (a running or queued task holds it, with which
    # tasks and their states) and `absent` (a resource B15 names that
    # this backend does not have). Nothing in it claims freed memory.
    WORKSPACE_UNLOADED = "WorkspaceUnloaded"
    # B40: the reply to EventsSince. `{session, since_seq, events,
    # tasks, has_more, mechanism: "events.session_seq"}`.
    #
    # Each entry in `events` is the envelope as it went out, with
    # `replay: true` on it -- which is what lets Go route these through
    # the same `deliver()` path as live events without mistaking a
    # replayed `ToolCompleted` for a tool completing now. `mechanism` is
    # kept from B15's reply for the same reason B15 put it there: a
    # client should read which cursor it is holding, not infer it.
    #
    # `tasks` rides along unchanged from B15's reply. Replaying the
    # events is what rebuilds the view; the task states are what resolve
    # a task the client marked UNKNOWN, including a `backend_died` row
    # crash recovery wrote before this process accepted an event.
    SESSION_EVENTS = "SessionEvents"
    # B15: the ONE terminal ack for a `Shutdown` command, emitted and
    # flushed before the outbox writer stops. This is the last envelope
    # Go ever reads from this process. Before B15 the handler acked
    # nothing, did not stop the process, and left the runtime rejecting
    # every later event -- see `_handle_shutdown`.
    SHUTDOWN_COMPLETE = "shutdown.complete"
    # Connection-level, caused by no request: corr_id is null on both.
    HELLO = "hello"
    PING = "ping"


@dataclass(frozen=True)
class OutgoingEvent:

    type: OutgoingEventType
    request_id: Optional[str]
    task_id: Optional[str] = None
    payload: dict[str, Any] = field(default_factory=dict)
    timestamp: float = field(default_factory=time.time)
    # Envelope metadata. `event_id` is this envelope's own id; the id of
    # the request that caused it goes out as corr_id, which is how Go
    # routes events back to whoever asked.
    event_id: str = field(default_factory=lambda: str(uuid.uuid4()))
    session_id: Optional[str] = None
    connection_id: Optional[str] = None
    trace_id: Optional[str] = None
    seq: Optional[int] = None
    # B40's durable per-session event cursor. Set by `Runtime._emit`
    # from what the log assigned, so it is null on every envelope the
    # log does not hold -- see this module's docstring.
    event_seq: Optional[int] = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "v": ENVELOPE_VERSION,
            "id": self.event_id,
            "type": self.type.value,
            "corr_id": self.request_id,
            "session_id": self.session_id,
            "task_id": self.task_id,
            "connection_id": self.connection_id,
            "trace_id": self.trace_id,
            "seq": self.seq,
            "event_seq": self.event_seq,
            "ts": self.timestamp,
            "payload": self.payload,
        }
