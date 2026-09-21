"""SessionManager: business rules for sessions. Delegates all SQL to
SessionRepository. Does NOT know Claude, OpenAI, or any provider."""

from __future__ import annotations

import re
import time
from typing import Any, Optional

import aiosqlite

from database.session_repository import SessionRepository
from managers.message import Message
from managers.message_history import MessageHistory
from managers.session import Session, SessionState
from runtime.errors import RuntimeException


class SessionNotFoundError(RuntimeException):
    pass


_CONTROL_CHARS = re.compile(r"[\x00-\x1f\x7f]")


def _sanitize_title(title: str) -> str:
    """Collapse a title to one line and strip control characters.

    Confirmed from two real rows: a title containing a raw embedded "\n"
    ("# heading\n**bold** and `inline` and ...") measures and stores fine
    -- nothing here validates line count -- but broke the Go frontend's
    Sessions card border, because that renderer measures a title's DISPLAY
    WIDTH assuming one line and the terminal drew the second half on the
    row below, outside the box it had already closed. Applied here rather
    than at each caller: create() and rename() are the only two places a
    title is ever written, so this is the one choke point that protects
    CreateSession, RenameSession, a manually-typed CLI title, and whatever
    calls either method next -- none of which should have to remember this
    on their own. _generate_title's own splitlines()[0][:60] guard stays;
    this is defense in depth, not a replacement for it.
    """
    return " ".join(_CONTROL_CHARS.sub(" ", title).split())


class SessionManager:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._repo = SessionRepository(conn)

    # -- lifecycle (unchanged from Episode 2) ----------------------------

    async def create(
        self,
        title: str,
        provider: Optional[str] = None,
        model: Optional[str] = None,
        workspace: Optional[str] = None,
        project_id: Optional[str] = None,
        settings: Optional[dict[str, Any]] = None,
        effort: Optional[str] = None,
        created_by: Optional[str] = None,
    ) -> Session:
        """provider/model are Optional as of B6: NULL means "inherit from
        global config". This manager stores that; it does not resolve it."""
        session = Session.new(title=_sanitize_title(title), provider=provider, model=model,
                               workspace=workspace, project_id=project_id,
                               settings=settings, effort=effort, created_by=created_by)
        await self._repo.insert(session)
        return session

    async def get(
        self,
        session_id: str,
        load_messages: bool = False,
        message_limit: Optional[int] = None,
    ) -> Session:
        """B18: `message_limit` is additive. Omitted or None -> today's
        unbounded `fetch_messages` path, byte-for-byte, because F3 relies
        on it until F18 replaces it. Given -> the newest `message_limit`
        messages plus a real total count, both off the new keyset query."""
        session = await self._repo.fetch_one(session_id)
        if session is None:
            raise SessionNotFoundError(f"No session with id {session_id!r}")
        if load_messages:
            if message_limit is not None:
                messages, _, _ = await self._repo.fetch_messages_keyset(
                    session_id, before=None, after=None, limit=message_limit,
                )
                session.messages = messages
                session.total_message_count = await self._repo.fetch_message_count(session_id)
            else:
                session.messages = await self._repo.fetch_messages(session_id)
        return session

    async def list_all(self, include_archived: bool = False) -> list[Session]:
        return await self._repo.fetch_all(include_archived=include_archived)

    async def rename(self, session_id: str, new_title: str) -> None:
        await self._ensure_exists(session_id)
        await self._repo.update_fields(session_id, {"title": _sanitize_title(new_title)})

    async def archive(self, session_id: str) -> None:
        await self._ensure_exists(session_id)
        await self._repo.update_fields(
            session_id,
            {"state": SessionState.ARCHIVED.value, "archived_at": time.time()},
        )

    async def set_provider(self, session_id: str, provider: str, model: str) -> None:
        await self._ensure_exists(session_id)
        await self._repo.update_fields(session_id, {"provider": provider, "model": model})

    async def set_project(self, session_id: str, project_id: str) -> None:
        """Bind a session to a project (B12).

        `project_id` was write-once-at-CreateSession until now, which
        left a session created outside a project permanently unable to
        attach anything: every attachment is guarded against
        `session.project_id`, and there was no way to give it one.

        The counterpart of `set_provider`, and deliberately the same
        shape -- a column the session owns, changed through the manager
        that owns the session.
        """
        await self._ensure_exists(session_id)
        await self._repo.update_fields(session_id, {"project_id": project_id})

    async def unarchive(self, session_id: str) -> None:
        await self._ensure_exists(session_id)
        await self._repo.update_fields(
            session_id,
            {"state": SessionState.ACTIVE.value, "archived_at": None},
        )

    async def delete(self, session_id: str) -> None:
        await self._ensure_exists(session_id)
        await self._repo.delete(session_id)

    async def update_metadata(self, session_id: str, patch: dict[str, Any]) -> Session:
        session = await self.get(session_id)
        merged = {**session.metadata, **patch}
        await self._repo.update_fields(session_id, {"metadata": _to_json(merged)})
        session.metadata = merged
        return session

    async def update_settings(self, session_id: str, patch: dict[str, Any]) -> Session:
        session = await self.get(session_id)
        merged = {**session.settings, **patch}
        await self._repo.update_fields(session_id, {"settings": _to_json(merged)})
        session.settings = merged
        return session

    async def set_active_task(self, session_id: str, task_id: Optional[str]) -> None:
        await self._ensure_exists(session_id)
        await self._repo.update_fields(session_id, {"active_task_id": task_id})

    # -- messages ---------------------------------------------------------
    # Updated in Episode 3: append_message now takes a fully-constructed,
    # already-validated Message object instead of loose (role, content)
    # strings. The caller (Runtime) builds the Message using the
    # SystemMessage/UserMessage/AssistantMessage/etc. factories from
    # managers/message.py.

    async def append_message(self, message: Message) -> Message:
        await self._ensure_exists(message.session_id)
        message.validate()
        await self._repo.insert_message(message)
        await self._repo.update_fields(message.session_id, {})  # bumps updated_at
        return message

    async def get_messages(self, session_id: str) -> list[Message]:
        await self._ensure_exists(session_id)
        return await self._repo.fetch_messages(session_id)

    async def get_messages_page(
        self, session_id: str, *, before: Optional[int], after: Optional[int], limit: int,
    ) -> tuple[list[Message], bool, bool]:
        """B18: the SessionMessages keyset query. See
        SessionRepository.fetch_messages_keyset for the contract."""
        await self._ensure_exists(session_id)
        return await self._repo.fetch_messages_keyset(
            session_id, before=before, after=after, limit=limit,
        )

    async def get_history(self, session_id: str) -> MessageHistory:
        """Returns a MessageHistory wrapper for the session. Not yet
        wired into any Runtime handler -- exists as ready-to-use
        infrastructure for Episode 7 (Memory)."""
        messages = await self.get_messages(session_id)
        return MessageHistory(session_id, messages)

    # -- helpers ------------------------------------------------------

    async def _ensure_exists(self, session_id: str) -> None:
        if await self._repo.fetch_one(session_id) is None:
            raise SessionNotFoundError(f"No session with id {session_id!r}")


def _to_json(d: dict[str, Any]) -> str:
    import json
    return json.dumps(d)