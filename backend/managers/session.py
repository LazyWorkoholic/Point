"""Session: everything needed to continue an interaction later.

A session is NOT "a conversation" -- it's the full context an agent
would need to pick up exactly where it left off, even after the
process restarts. That's why it carries more than just messages.

Field-by-field:

id              -> UUID. Go/Python both refer to sessions by this,
                   never by row number, so IDs stay stable across
                   any future storage backend change.
title           -> Human label for the Go TUI's session list.
provider        -> Which LLM provider this session is pinned to
                   (e.g. "claude"). Lives here, not on Agent, so
                   reconnecting after a restart doesn't require Go
                   to remember or re-send it.
model           -> Specific model string (e.g. "claude-sonnet-5").
                   Separate from provider because a session might
                   switch models within the same provider without
                   losing its identity.
workspace       -> Optional grouping key (e.g. a project folder or
                   "chat" vs "coding" workspace in the Go TUI). Lets
                   Go filter its session list without SessionManager
                   needing to know what a "workspace" means to the UI.
state           -> active | archived. Hides old sessions from the
                   default list without deleting history.
created_at/updated_at
                -> Standard audit fields; updated_at drives "most
                   recent" sorting.
summary         -> Optional. Filled in later (Episode 5-ish) once a
                   session gets long enough to need compression.
                   Column exists now so no migration is needed then.
active_task_id  -> The task_id currently running against this
                   session, if any. This is what lets Go ask "is
                   something in flight for this session?" without
                   querying TaskManager directly -- TaskManager and
                   SessionManager stay fully decoupled from each other.
metadata        -> Free-form dict for anything we haven't
                   anticipated (e.g. tags, linked files).
settings        -> Per-session overrides (e.g. temperature, system
                   prompt). Kept separate from metadata because
                   metadata is descriptive, settings are behavioral --
                   an agent will read settings, but never metadata.
messages        -> Populated on demand (see SessionManager.get). Left
                   empty by default so listing 50 sessions doesn't
                   pull their entire histories into memory.
total_message_count
                -> B18. Populated only alongside a bounded `messages`
                   load (SessionManager.get(message_limit=...)) -- None
                   otherwise, including the unbounded `load_messages`
                   path F3 uses, which must stay byte-for-byte what it
                   was before this episode. F18 needs a real count for
                   an honest scrollbar; len(messages) would only be the
                   size of the page it happens to hold.

project_id      -> Which Project this session belongs to. Nullable
                   because the Project system doesn't exist yet —
                   every session is currently unowned. Once Projects
                   ship, this becomes the join key; no migration
                   needed because the column already exists.

effort          -> Normalized reasoning effort (off|low|medium|high) for
                   this session, or None. Part of the same resolution
                   order as provider/model.
created_by      -> Who or what created this session (a user name over
                   SSH, an agent id). Nullable; nothing populates it yet.
archived_at     -> When the session was archived. `state` says whether,
                   this says when, and unarchiving clears it.

As of B6: `provider` and `model` are Optional. NULL is not "missing" --
it is how a session says *inherit from global config*, which is the
resolution order in project_idea.md §10. B6 only makes NULL storable;
**B8 owns resolution**, and until it lands a session with a NULL provider
cannot start a task.

As of Episode 3: `messages` now holds the richer Message type from
managers/message.py, not a local dataclass. The simple Message class
that used to live in this file has been removed -- Message is now a
system-wide concept, not something private to sessions.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

from managers.message import Message

class SessionState(str, Enum):
    ACTIVE = "active"
    ARCHIVED = "archived"


@dataclass
class Session:
    id: str
    title: str
    provider: Optional[str] = None
    model: Optional[str] = None
    workspace: Optional[str] = None
    project_id: Optional[str] = None
    state: SessionState = SessionState.ACTIVE
    created_at: float = field(default_factory=time.time)
    updated_at: float = field(default_factory=time.time)
    summary: Optional[str] = None
    active_task_id: Optional[str] = None
    metadata: dict[str, Any] = field(default_factory=dict)
    settings: dict[str, Any] = field(default_factory=dict)
    messages: list[Message] = field(default_factory=list)
    effort: Optional[str] = None
    created_by: Optional[str] = None
    archived_at: Optional[float] = None
    total_message_count: Optional[int] = None

    @staticmethod
    def new(
        title: str,
        provider: Optional[str] = None,
        model: Optional[str] = None,
        workspace: Optional[str] = None,
        project_id: Optional[str] = None,
        settings: Optional[dict[str, Any]] = None,
        effort: Optional[str] = None,
        created_by: Optional[str] = None,
    ) -> "Session":
        """provider/model default to None -- "inherit from global config"
        -- rather than to a hardcoded vendor. A literal default here is
        what made every session claim to be a Claude session whether or
        not a key existed."""
        now = time.time()
        return Session(
            id=str(uuid.uuid4()),
            title=title,
            provider=provider,
            model=model,
            workspace=workspace,
            project_id=project_id,
            created_at=now,
            updated_at=now,
            settings=settings or {},
            effort=effort,
            created_by=created_by,
        )