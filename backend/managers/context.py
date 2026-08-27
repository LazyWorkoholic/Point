r"""The Context Builder: the one function that decides what enters a
finite context window on this turn.

v0 is deliberately dumb, and the dumbness is the point. The real memory
system (B13) is built later, against failures observed while actually
using B11-B12 -- not against a guess made here. So: no search, no
summarization, no promotion, no embeddings. What exists instead is the
*shape* those things will slot into, plus a log that makes every future
failure diagnosable rather than mysterious.

Two things make this file worth more than the sum of its rules:

  1. **Every turn produces an auditable log.** Not "roughly 4k tokens of
     history" -- a per-item record of what entered, what it cost, and
     what was dropped for what reason. When a model answers using stale
     code or forgets something the user said four turns ago, the log
     says which. `report()` is a projection of that same log, never a
     second implementation of the same arithmetic.

  2. **Attachments are references, never copies.** A file attached ten
     turns ago and untouched since is already in the history verbatim;
     re-sending it every turn is pure waste. Recording `(mtime, size)`
     at send time and comparing on the next build costs one `stat` and
     saves the whole body. The failure this avoids in the other
     direction is worse: a file that changed and was never re-sent means
     the model answers confidently about code that no longer exists.

What this file must never do:

  - Touch SQLite. It goes through repositories, via MemoryService.
  - Grow a dependency on the agent system. It takes ContextPolicy --
    the narrow set of fields it actually reads -- not B9's AgentConfig.
  - Partially truncate a message. Half a message is worse than no
    message: the model cannot tell it was cut.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Optional, Protocol

import appconfig
from database.attachment_repository import AttachmentRepository
from database.project_repository import ProjectRepository
from database.session_repository import SessionRepository
from logging_setup import get_logger
from managers.attachment import (
    MODE_DETACHED,
    MODE_FULL,
    MODE_REFERENCE,
    SOURCE_TYPE_FILE,
    Attachment,
)
from managers.message import ContentType, Message, MessageRole, SystemMessage, UserMessage
from managers.message_history import MessageHistory
from managers.pathguard import resolve_path
from managers.project import Project
from managers.session import Session
from runtime.errors import ContextOverflowError, PathDeniedError

_log = get_logger("context")


# ---------------------------------------------------------------------------
# Sections
#
# Fixed order, and the order is the assembly order from the episode spec.
# Three of the six contribute nothing in v0. They are still listed, still
# logged, and still cost a line in every report -- because "memory: 0" is
# information, and a section that silently does not appear is
# indistinguishable from one that had nothing to say.
# ---------------------------------------------------------------------------

SECTION_SYSTEM = "system"
SECTION_ATTACHMENTS = "attachments"
SECTION_MEMORY = "memory"
SECTION_SUMMARY = "summary"
SECTION_HISTORY = "history"
SECTION_TASK_STATE = "task_state"

SECTION_ORDER: tuple[str, ...] = (
    SECTION_SYSTEM,
    SECTION_ATTACHMENTS,
    SECTION_MEMORY,
    SECTION_SUMMARY,
    SECTION_HISTORY,
    SECTION_TASK_STATE,
)

DEFAULT_BUDGET_TOKENS = 8000
DEFAULT_STM_WINDOW = 20

DEFAULT_SYSTEM_PROMPT = (
    "You are Point, a local-first terminal AI workspace. Answer precisely and "
    "concisely. If you are unsure, say so rather than guessing."
)

# How much of a `reference`-mode file's head to show. Big enough to see
# what a file is, small enough that the whole point of reference mode --
# not paying for the body -- still holds.
REFERENCE_HEAD_CHARS = 200

# The marker a re-sent attachment carries. The model needs to be told
# that the copy further up the conversation is the stale one; without
# this it sees two versions of the same file and no ordering between
# them.
UPDATED_MARKER = "— updated since earlier"


# ---------------------------------------------------------------------------
# Token estimation
# ---------------------------------------------------------------------------

def estimate_tokens(text: str, model: Optional[str] = None) -> int:
    """`len(text) // 4`, and that is genuinely all it is in v0.

    `model` is unused and deliberately still in the signature: a real
    tokenizer is per-model, and adding the parameter later would mean
    touching every call site at exactly the moment the numbers start
    mattering. Nothing above this function is allowed to know how the
    estimate is made.

    Not measured against any real tokenizer. Treat every number this
    produces as an estimate, including the ones in `report`.
    """
    if not text:
        return 0
    return len(text) // 4


def default_budget_tokens() -> int:
    """Config `context.budget_tokens`, default 8000.

    Not the model's real `context_window`: `echo` is not a registered
    provider and an unregistered model has no `Model` row to read one
    from, so the common CLI path has no window available. A configured
    number that is always present beats a real number that is usually
    missing.
    """
    value = appconfig.config_value("context.budget_tokens", DEFAULT_BUDGET_TOKENS)
    try:
        budget = int(value)
    except (TypeError, ValueError):
        _log.warning(
            "config context.budget_tokens is not an integer (%r); using %d",
            value, DEFAULT_BUDGET_TOKENS,
        )
        return DEFAULT_BUDGET_TOKENS
    if budget <= 0:
        _log.warning(
            "config context.budget_tokens must be positive (got %d); using %d",
            budget, DEFAULT_BUDGET_TOKENS,
        )
        return DEFAULT_BUDGET_TOKENS
    return budget


# ---------------------------------------------------------------------------
# Inputs
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class ContextRequest:
    """What this turn is, as far as the builder needs to know.

    Deliberately NOT providers.models.GenerationRequest. That type
    already carries `messages`, which is this function's *output*, and it
    has no field for the session or the provider. The caller composes
    the two afterwards:

        built = await build(request, policy, budget, memory)
        GenerationRequest(model=request.model, messages=built.messages)

    `provider` is the *resolved* name (anthropic, not claude): it is what
    reasoning-block filtering compares against, and comparing an alias to
    a stored tag silently drops every block.

    `user_text` is this turn's new user message, not yet persisted. It is
    passed as text rather than as a Message on purpose -- the builder is
    called *before* the row is written, so an oversized message never
    reaches the database at all.
    """

    session_id: str
    provider: str
    model: str
    user_text: Optional[str] = None
    task_id: Optional[str] = None


@dataclass(frozen=True)
class ContextPolicy:
    """The knobs the builder actually reads. Nothing else.

    This is NOT B9's `AgentConfig`, and must not be renamed to it. B9
    defines `AgentConfig` mirroring the `agents` table row for row --
    twenty-odd fields covering tools, step limits, timeouts, and scope
    permissions, none of which this file has any business knowing. B9's
    `AgentConfig` adapts *to* this type (`to_context_policy()`); it does
    not replace it. That way the context builder never grows a
    dependency on the agent system, and can still be built and tested
    with a two-line literal.

    Nothing hydrates this from the `agents` table yet: `managers\\agent.py`
    is empty and B9 owns it. Callers pass a literal.
    """

    system_prompt: str
    stm_window: int = DEFAULT_STM_WINDOW
    default_attach_mode: str = MODE_FULL
    effort: Optional[str] = None


DEFAULT_POLICY = ContextPolicy(system_prompt=DEFAULT_SYSTEM_PROMPT)


# ---------------------------------------------------------------------------
# The log
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class IncludedItem:
    """One thing that was considered, with what it cost.

    Zero-token entries are kept, not filtered out. "attachment demo.py:
    0 tokens, unchanged since last sent" is the single most useful line
    in this whole log -- it is the difference between the change
    detection working and the file having been silently dropped.
    """

    section: str
    ref: str
    kind: str
    tokens: int
    detail: str = ""


@dataclass(frozen=True)
class EvictedItem:
    section: str
    ref: str
    kind: str
    tokens: int
    reason: str


@dataclass
class ContextLog:
    session_id: str
    provider: str
    model: str
    budget_tokens: int
    included: list[IncludedItem] = field(default_factory=list)
    evicted: list[EvictedItem] = field(default_factory=list)

    def section_tokens(self) -> dict[str, int]:
        """Every section in fixed order, including the ones that cost
        nothing."""
        totals = {name: 0 for name in SECTION_ORDER}
        for item in self.included:
            totals[item.section] = totals.get(item.section, 0) + item.tokens
        return totals

    def section_counts(self) -> dict[str, int]:
        counts = {name: 0 for name in SECTION_ORDER}
        for item in self.included:
            counts[item.section] = counts.get(item.section, 0) + 1
        return counts

    @property
    def used_tokens(self) -> int:
        return sum(item.tokens for item in self.included)

    @property
    def free_tokens(self) -> int:
        return self.budget_tokens - self.used_tokens

    def to_dict(self) -> dict[str, Any]:
        return {
            "session_id": self.session_id,
            "provider": self.provider,
            "model": self.model,
            "budget_tokens": self.budget_tokens,
            "used_tokens": self.used_tokens,
            "free_tokens": self.free_tokens,
            "sections": self.section_tokens(),
            "included": [vars(i) for i in self.included],
            "evicted": [vars(e) for e in self.evicted],
        }


# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------

@dataclass
class BuiltContext:
    """The provider-ready message list, and the log that explains it.

    These travel together on purpose. A message list on its own cannot
    answer "why is the file I attached not in here", and that is the
    question this whole episode exists to make answerable.
    """

    messages: list[Message]
    log: ContextLog


@dataclass(frozen=True)
class ContextReport:
    """What the status card (F6) renders. A projection of ContextLog --
    never a second pass over the same data, because two implementations
    of the same arithmetic disagree eventually and the UI is where you
    find out."""

    session_id: str
    provider: str
    model: str
    budget_tokens: int
    sections: dict[str, int]
    counts: dict[str, int]
    used_tokens: int
    free_tokens: int
    included: list[IncludedItem]
    next_evictions: list[EvictedItem]

    def to_dict(self) -> dict[str, Any]:
        """Every field, deliberately -- one key per dataclass field, no
        subset.

        F6's status card is the only consumer of this type, so a
        serialization that drops fields `describe()` still renders is not
        a smaller payload, it is F6 being handed an incomplete contract
        and left to finish it. `counts` and `included` are what the card
        needs for "6 messages, 2 attachments" and for a per-item
        breakdown; leaving them out would have meant either extending
        this later or parsing `describe()`, and `describe()` is a human
        rendering that is allowed to change.
        """
        return {
            "session_id": self.session_id,
            "provider": self.provider,
            "model": self.model,
            "budget_tokens": self.budget_tokens,
            "sections": self.sections,
            "counts": self.counts,
            "used_tokens": self.used_tokens,
            "free_tokens": self.free_tokens,
            "included": [vars(i) for i in self.included],
            "next_evictions": [vars(e) for e in self.next_evictions],
        }

    def describe(self) -> str:
        lines = [
            f"session   {self.session_id}",
            f"provider  {self.provider}/{self.model}",
            f"budget    {self.budget_tokens} tokens",
            "",
        ]
        for name in SECTION_ORDER:
            tokens = self.sections.get(name, 0)
            count = self.counts.get(name, 0)
            lines.append(f"  {name:<14}{tokens:>8}  ({count} item{'' if count == 1 else 's'})")
        lines.append(f"  {'':<14}{'--------':>8}")
        lines.append(f"  {'total':<14}{self.used_tokens:>8}")
        lines.append(f"  {'free':<14}{self.free_tokens:>8}")
        lines.append("")

        lines.append("included:")
        for item in self.included:
            detail = f"  {item.detail}" if item.detail else ""
            lines.append(f"  {item.section:<12} {item.tokens:>6}  {item.kind} {item.ref}{detail}")

        lines.append("")
        if self.next_evictions:
            lines.append("evicted (what this turn would drop):")
            for item in self.next_evictions:
                lines.append(
                    f"  {item.section:<12} {item.tokens:>6}  {item.kind} {item.ref}  {item.reason}"
                )
        else:
            lines.append("evicted: nothing")
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# MemoryService
#
# Two methods implemented, four raising NotImplementedError -- and the
# four are in the Protocol from day one deliberately. Defining the whole
# interface now is what stops B13 from being a refactor of every call
# site: the shape retrieval and summarization will need is visible here,
# unimplemented, rather than being discovered later.
# ---------------------------------------------------------------------------

class MemoryService(Protocol):
    async def recent_messages(self, session_id: str, n: int) -> list[Message]: ...

    async def attachments(self, session_id: str) -> list[Attachment]: ...

    async def project(self, session_id: str) -> Optional[Project]: ...

    async def task_messages(self, session_id: str, task_id: str) -> list[Message]:
        """Backs B10's `load_task_result`. Not part of the Protocol.

        Scoped to `session_id` on purpose: a task id is not a capability.
        Letting any agent load any task's output by id would let an agent
        holding only `reads_session` read across every session in the
        workspace, which is exactly the ceiling the scope flags exist to
        enforce."""
        messages = await self._sessions.fetch_messages_for_task(task_id)
        return [m for m in messages if m.session_id == session_id]

    async def record_attachment_sent(self, attachment_id: str, mtime: int, size: int) -> None: ...

    async def search(self, query: str, scopes: list[str], limit: int) -> list[Any]: ...

    async def summary(self, session_id: str) -> Optional[str]: ...

    async def propose(self, record: Any) -> None: ...

    async def forget(self, record_id: str) -> None: ...


class SessionMemoryService:
    """The v0 MemoryService: two real methods over the repositories, and
    four honest refusals.

    The refusals raise rather than returning empty. An empty list is
    indistinguishable from "searched and found nothing", which is how a
    caller ends up believing retrieval ran when no retrieval exists.
    `build()` catches NotImplementedError at exactly the two points where
    v0 is expected to have nothing, and logs the section as unimplemented
    -- so the absence is visible in every report instead of inferred.
    """

    def __init__(self, conn: Any) -> None:
        self._sessions = SessionRepository(conn)
        self._attachments = AttachmentRepository(conn)
        self._projects = ProjectRepository(conn)

    async def session(self, session_id: str) -> Optional[Session]:
        """Not part of the Protocol -- `report()` needs the session's
        provider/model to project a turn, and nothing else does."""
        return await self._sessions.fetch_one(session_id)

    async def latest_session(self) -> Optional[Session]:
        """Backs `cli.py context report --last`."""
        sessions = await self._sessions.fetch_all(include_archived=False)
        return sessions[0] if sessions else None

    async def recent_messages(self, session_id: str, n: int) -> list[Message]:
        messages = await self._sessions.fetch_messages(session_id)
        if n <= 0:
            return []
        return messages[-n:]

    async def attachments(self, session_id: str) -> list[Attachment]:
        return await self._attachments.fetch_for_session(session_id)

    async def project(self, session_id: str) -> Optional[Project]:
        """The session's project, or None. This is what gives the path
        guard a root to contain attachment paths against -- a session
        with no project has no scope, and B7's guard refuses everything
        outside one unless `project.allow_outside_project` says
        otherwise."""
        session = await self._sessions.fetch_one(session_id)
        if session is None or not session.project_id:
            return None
        return await self._projects.fetch_by_id(session.project_id)

    async def task_messages(self, session_id: str, task_id: str) -> list[Message]:
        """Backs B10's `load_task_result`. Not part of the Protocol.

        Scoped to `session_id` on purpose: a task id is not a capability.
        Letting any agent load any task's output by id would let an agent
        holding only `reads_session` read across every session in the
        workspace, which is exactly the ceiling the scope flags exist to
        enforce."""
        messages = await self._sessions.fetch_messages_for_task(task_id)
        return [m for m in messages if m.session_id == session_id]

    async def record_attachment_sent(self, attachment_id: str, mtime: int, size: int) -> None:
        await self._attachments.update_recorded_stat(attachment_id, mtime, size)

    # -- B13 ------------------------------------------------------------

    async def search(self, query: str, scopes: list[str], limit: int) -> list[Any]:
        raise NotImplementedError("Memory search arrives in B13")

    async def summary(self, session_id: str) -> Optional[str]:
        raise NotImplementedError("Session summarization arrives in B13")

    async def propose(self, record: Any) -> None:
        raise NotImplementedError("Memory promotion arrives in B13")

    async def forget(self, record_id: str) -> None:
        raise NotImplementedError("Memory forget arrives in B13")


# ---------------------------------------------------------------------------
# Attachment rendering
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class _RenderedAttachment:
    text: str
    detail: str
    # The (mtime, size) to record -- but only when the body actually went
    # in. Reference mode leaves this None: it never puts the body into
    # the history, so recording the pair would make a later switch to
    # `full` believe the file is already there and send nothing.
    record_stat: Optional[tuple[int, int]] = None


def _render_attachment(
    attachment: Attachment,
    model: Optional[str],
    project: Optional[Project],
    allow_outside_project: bool,
) -> _RenderedAttachment:
    """Resolve one attachment against the filesystem, right now.

    The path goes through B7's `resolve_path()`, not `Path()`. B7's
    contract is that `resolve_path()` is the *only* sanctioned way to
    turn a string into a filesystem path anywhere in this backend --
    two implementations drift, and the second one to drift is the one
    that lets something through. An attachment path is attacker-adjacent
    in exactly the way that matters: it is a string from a database row
    that becomes a file read.

    Never raises. A refused, deleted, or unreadable attachment is a fact
    about this turn worth logging, not a reason to fail the user's
    message -- PathDeniedError included.
    """
    if attachment.mode == MODE_DETACHED:
        return _RenderedAttachment(text="", detail="detached: contributes nothing")

    if attachment.source_type != SOURCE_TYPE_FILE:
        # B12 adds project-index ids, URLs, and prior task output. Until
        # then, say so rather than guessing at a resolution.
        return _RenderedAttachment(
            text="", detail=f"unsupported source_type {attachment.source_type!r} (B12)"
        )

    try:
        path = resolve_path(
            attachment.source_id, project, allow_outside_project=allow_outside_project
        )
    except PathDeniedError as exc:
        return _RenderedAttachment(text="", detail=f"refused by path guard: {exc}")

    try:
        stat = path.stat()
    except OSError as exc:
        return _RenderedAttachment(text="", detail=f"unreadable: {exc.strerror or exc}")

    mtime = int(stat.st_mtime)
    size = int(stat.st_size)
    name = path.name

    if attachment.mode == MODE_REFERENCE:
        return _RenderedAttachment(
            text=_reference_body(path, name, size, attachment.id),
            detail="reference mode: descriptor only, body not included",
        )

    # -- full mode ------------------------------------------------------
    recorded = attachment.recorded_stat
    if recorded == (mtime, size):
        # The body is already in the history verbatim, from the turn that
        # last sent it. Sending it again buys nothing and costs the whole
        # file, every turn, forever.
        return _RenderedAttachment(
            text="", detail=f"unchanged since last sent (mtime={mtime}, size={size})"
        )

    try:
        body = path.read_text(encoding="utf-8", errors="replace")
    except OSError as exc:
        return _RenderedAttachment(text="", detail=f"unreadable: {exc.strerror or exc}")

    first_send = recorded is None
    marker = "" if first_send else f" {UPDATED_MARKER}"
    header = f"[attachment {attachment.id} — {name} ({path}){marker}]"
    detail = (
        f"{'first send' if first_send else 'changed, re-sent ' + UPDATED_MARKER}"
        f" (mtime={mtime}, size={size})"
    )
    return _RenderedAttachment(
        text=f"{header}\n{body}",
        detail=detail,
        record_stat=(mtime, size),
    )


def _reference_body(path: Path, name: str, size: int, attachment_id: str) -> str:
    """Mechanical only. Nothing here is generated: it is the basename,
    the byte size, the line count, and the literal first few hundred
    characters of the file.

    B8's "Do NOT" is explicit that summarization is not built in this
    episode, and the `context_attachments` table has no title or summary
    column to read a real one from either. A descriptor the code can
    derive is honest; a sentence a model wrote about a file it may no
    longer match is not.
    """
    try:
        head = path.read_text(encoding="utf-8", errors="replace")
    except OSError as exc:
        return f"[attachment {attachment_id} — {name} (reference)]\nunreadable: {exc.strerror or exc}"

    lines = head.count("\n") + (1 if head and not head.endswith("\n") else 0)
    excerpt = head[:REFERENCE_HEAD_CHARS]
    ellipsis = "…" if len(head) > REFERENCE_HEAD_CHARS else ""
    return (
        f"[attachment {attachment_id} — {name} (reference)]\n"
        f"{size} bytes, {lines} lines\n"
        f"{excerpt}{ellipsis}"
    )


# ---------------------------------------------------------------------------
# Message helpers
# ---------------------------------------------------------------------------

def _message_text(message: Message) -> str:
    """What a message costs, as text. Non-textual content is priced by
    its serialized payload, which is roughly what a provider will
    receive for it -- a tool result's JSON is not free just because it
    is not prose."""
    if message.content.type in (ContentType.TEXT, ContentType.THINKING):
        return message.content.text or ""
    try:
        return json.dumps(message.content.data, default=str)
    except (TypeError, ValueError):
        return str(message.content.data)


def _short(ref: str, width: int = 8) -> str:
    return ref[:width]


# ---------------------------------------------------------------------------
# build()
# ---------------------------------------------------------------------------

async def build(
    request: ContextRequest,
    policy: ContextPolicy,
    budget_tokens: int,
    memory: MemoryService,
    *,
    record: bool = True,
) -> BuiltContext:
    """Assemble this turn's context, and the log that explains it.

    Async because everything it reads lives behind an aiosqlite
    repository. It still never touches SQLite itself -- `memory` is the
    only way in, which is what keeps this function testable with a fake
    and keeps the storage layer swappable.

    Assembly order is fixed:

        1. system prompt        always
        2. attachments          resolved, never copied
        3. memory               v0: nothing (interface exists)
        4. session summary      v0: nothing (interface exists)
        5. recent messages      newest-first until the budget is spent
        6. task working state   only while a task is running

    Two things are non-droppable and are therefore reserved up front,
    before anything competes for what is left: the system prompt, and
    this turn's own user message. Evicting either produces a request that
    is not the request the user made -- dropping the question to keep an
    attachment about it is the wrong trade in every case. Everything else
    competes, and loses from the oldest backwards.

    Raises ContextOverflowError when a single non-droppable item, or a
    single attachment, does not fit the whole budget on its own.

    `record=False` builds without the one side effect this function has
    -- updating an attachment's recorded `(mtime, size)`. `report()`
    passes it, because a projection of a turn that has not happened must
    not convince the next real turn that it already sent the file.
    """
    log = ContextLog(
        session_id=request.session_id,
        provider=request.provider,
        model=request.model,
        budget_tokens=budget_tokens,
    )
    remaining = budget_tokens

    # -- 1. system prompt (non-droppable) --------------------------------
    system_message = SystemMessage(request.session_id, policy.system_prompt)
    system_tokens = estimate_tokens(policy.system_prompt, request.model)
    if system_tokens > budget_tokens:
        raise ContextOverflowError(system_tokens, budget_tokens, subject="the system prompt")
    remaining -= system_tokens
    log.included.append(
        IncludedItem(SECTION_SYSTEM, "system_prompt", "system", system_tokens)
    )

    # -- this turn's user message (non-droppable, placed at 5) ------------
    pending_message: Optional[Message] = None
    pending_tokens = 0
    if request.user_text is not None:
        pending_tokens = estimate_tokens(request.user_text, request.model)
        if pending_tokens > budget_tokens:
            raise ContextOverflowError(
                pending_tokens, budget_tokens, subject="this message"
            )
        if pending_tokens > remaining:
            # The system prompt alone left no room. Nothing droppable has
            # been added yet, so there is nothing to evict to fix it.
            raise ContextOverflowError(
                system_tokens + pending_tokens,
                budget_tokens,
                subject="this message plus the system prompt",
            )
        pending_message = UserMessage(request.session_id, request.user_text)
        remaining -= pending_tokens

    # -- 2. attachments ---------------------------------------------------
    # The project is what the path guard contains attachment paths
    # against. A session with no project has no scope, so B7's guard
    # refuses absolute paths unless `project.allow_outside_project` is
    # on -- which is the correct default, and shows up in the log as a
    # refusal rather than as a silently missing attachment.
    project = await memory.project(request.session_id)
    allow_outside = bool(appconfig.config_value("project.allow_outside_project", False))

    attachment_messages: list[Message] = []
    for attachment in await memory.attachments(request.session_id):
        rendered = _render_attachment(attachment, request.model, project, allow_outside)
        ref = f"{_short(attachment.id)} {Path(attachment.source_id).name}"

        if not rendered.text:
            # Contributed nothing, and the log says why -- unchanged,
            # detached, or unreadable. This zero is the whole point of the
            # change-detection scheme being visible.
            log.included.append(
                IncludedItem(SECTION_ATTACHMENTS, ref, "attachment", 0, rendered.detail)
            )
            continue

        tokens = estimate_tokens(rendered.text, request.model)
        if tokens > budget_tokens:
            raise ContextOverflowError(
                tokens, budget_tokens, subject=f"attachment {Path(attachment.source_id).name}"
            )
        if tokens > remaining:
            log.evicted.append(
                EvictedItem(
                    SECTION_ATTACHMENTS, ref, "attachment", tokens,
                    f"does not fit: {tokens} tokens, {remaining} free",
                )
            )
            continue

        remaining -= tokens
        attachment_messages.append(SystemMessage(request.session_id, rendered.text))
        log.included.append(
            IncludedItem(SECTION_ATTACHMENTS, ref, "attachment", tokens, rendered.detail)
        )
        if rendered.record_stat is not None and record:
            # Recorded only now, after the body actually entered the
            # context. Recording it at resolution time would mean an
            # attachment evicted for budget is remembered as sent, and
            # never sent again.
            mtime, size = rendered.record_stat
            await memory.record_attachment_sent(attachment.id, mtime, size)

    # -- 3. memory --------------------------------------------------------
    try:
        await memory.search("", [], 0)
        detail = "unexpected: v0 memory search returned"
    except NotImplementedError as exc:
        detail = f"v0: {exc}"
    log.included.append(IncludedItem(SECTION_MEMORY, "-", "memory", 0, detail))

    # -- 4. session summary -----------------------------------------------
    try:
        await memory.summary(request.session_id)
        detail = "unexpected: v0 summary returned"
    except NotImplementedError as exc:
        detail = f"v0: {exc}"
    log.included.append(IncludedItem(SECTION_SUMMARY, "-", "summary", 0, detail))

    # -- 5. recent messages, newest-first ---------------------------------
    window = await memory.recent_messages(request.session_id, policy.stm_window)
    history = MessageHistory(request.session_id, window)

    # Reasoning blocks produced by a different provider are filtered on
    # the way out, never deleted -- B3's `provider` tag exists exactly for
    # this. MessageHistory.for_provider() already implements it; a second
    # filter here would be a second thing to keep correct.
    eligible = history.for_provider(request.provider)
    eligible_ids = {m.id for m in eligible}
    for message in window:
        if message.id not in eligible_ids:
            log.evicted.append(
                EvictedItem(
                    SECTION_HISTORY, _short(message.id), "reasoning",
                    estimate_tokens(_message_text(message), request.model),
                    f"reasoning block tagged {message.content.provider!r}, "
                    f"this request targets {request.provider!r}",
                )
            )

    kept: list[Message] = []
    # Collected separately and reversed at the end: the walk runs
    # newest-first because that is the eviction order, but the log should
    # read in the order the messages are actually sent. A report whose
    # history runs backwards is a report nobody can check against the
    # conversation.
    history_items: list[IncludedItem] = []
    spent = False
    for message in reversed(eligible):
        text = _message_text(message)
        tokens = estimate_tokens(text, request.model)
        if spent or tokens > remaining:
            # Once the budget is spent, everything older goes too. Not
            # "skip this one and try the next" -- a history with holes in
            # it reads as a conversation where the user contradicted
            # themselves.
            spent = True
            log.evicted.append(
                EvictedItem(
                    SECTION_HISTORY, _short(message.id), message.role.value, tokens,
                    "budget spent, oldest-first eviction",
                )
            )
            continue
        remaining -= tokens
        kept.append(message)
        history_items.append(
            IncludedItem(SECTION_HISTORY, _short(message.id), message.role.value, tokens)
        )
    kept.reverse()
    history_items.reverse()
    log.included.extend(history_items)

    if pending_message is not None:
        log.included.append(
            IncludedItem(
                SECTION_HISTORY, _short(pending_message.id), MessageRole.USER.value,
                pending_tokens, "this turn's message (reserved, non-droppable)",
            )
        )

    # -- 6. task working state --------------------------------------------
    # Nothing to include in v0, and inventing a marker line would be
    # fabricating content. Real working state -- tool calls, scratchpad,
    # step results -- arrives with B10/B11, at which point this section
    # already has its place in the order and its line in every report.
    log.included.append(
        IncludedItem(
            SECTION_TASK_STATE,
            request.task_id and _short(request.task_id) or "-",
            "task_state", 0,
            "v0: no working state until B10/B11"
            + ("" if request.task_id else " (no task running)"),
        )
    )

    messages = [system_message] + attachment_messages + kept
    if pending_message is not None:
        messages.append(pending_message)

    _log.debug(
        "Built context for session %s: %d message(s), %d/%d tokens, %d evicted",
        request.session_id, len(messages), log.used_tokens, budget_tokens, len(log.evicted),
    )
    return BuiltContext(messages=messages, log=log)


# ---------------------------------------------------------------------------
# report()
# ---------------------------------------------------------------------------

async def report(
    session_id: str,
    memory: SessionMemoryService,
    policy: ContextPolicy = DEFAULT_POLICY,
    budget_tokens: Optional[int] = None,
) -> ContextReport:
    """The breakdown the status card (F6) renders.

    Nothing persists the log, so this re-runs `build()` for the session's
    *next* turn and projects that run's log. That is what makes the
    totals match the built context by construction rather than by
    agreement between two pieces of arithmetic -- and it is why
    `next_evictions` is a real answer to "what would the next turn drop"
    rather than an estimate of one.

    The projected turn carries no `user_text`: the next message has not
    been typed yet. A long message will therefore push more history out
    than this report shows.

    Read-only, via `record=False`. Without it, asking what the next turn
    would cost would mark every changed attachment as already sent, and
    the next real turn would then skip the file it was about to re-read
    -- a report that silently changes the thing it reports on.
    """
    session = await memory.session(session_id)
    if session is None:
        raise ValueError(f"No session with id {session_id!r}")

    request = ContextRequest(
        session_id=session_id,
        provider=session.provider or "-",
        model=session.model or "-",
    )
    built = await build(
        request, policy, budget_tokens or default_budget_tokens(), memory, record=False
    )
    log = built.log
    return ContextReport(
        session_id=log.session_id,
        provider=log.provider,
        model=log.model,
        budget_tokens=log.budget_tokens,
        sections=log.section_tokens(),
        counts=log.section_counts(),
        used_tokens=log.used_tokens,
        free_tokens=log.free_tokens,
        included=list(log.included),
        next_evictions=list(log.evicted),
    )
