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
    FILE_LOCK_RESULT = "FileLockResult"
    FILE_LOCK_RELEASED = "FileLockReleased"
    FILE_STALE_RESULT = "FileStaleResult"
    # Attachments (B12).
    ATTACHMENT = "Attachment"
    ATTACHMENT_LIST = "AttachmentList"
    ATTACHMENT_DETACHED = "AttachmentDetached"
    ATTACHMENT_RESOLUTION = "AttachmentResolution"
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
            "ts": self.timestamp,
            "payload": self.payload,
        }
