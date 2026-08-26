"""Message: the structured unit of conversation.

This is the "common language" Episode 3 exists to create — the shape
that Runtime, Sessions, and (starting Episode 5) Providers/Agents all
speak, instead of passing bare strings around.

Field-by-field reasoning:

id          -> UUID. Needed for anything that references a specific
               message later: editing, deleting, associating a tool
               result, associating a streamed reply, tracing back to
               the task that produced it, or pulling it into memory.
session_id  -> Which session this message belongs to. Required and
               validated -- a message with no session is meaningless.
task_id     -> Which Task produced this message, if any. Required
               because of the concurrent architecture from Episode 1:
               a single session can have multiple tasks in flight at
               once (e.g. a chat reply streaming while a research task
               also runs), so "session_id" alone isn't enough to say
               which piece of work generated a given message. User
               messages (the trigger, not a task's output) leave this
               None; assistant/tool/error messages set it once their
               task exists.
role        -> Strict enum (system/user/assistant/tool/error), not a
               free string. The provider layer (Episode 5) translates
               this to whatever schema OpenAI/Claude/Gemini/Ollama/
               OpenRouter each expect -- our internal shape never
               depends on any single vendor's format.
content     -> A MessageContent object, not a raw string. Starts as
               text-only but is shaped so code/image/tool_result/
               structured content can be added later without breaking
               anything that already reads `.text`.
timestamp   -> When the message was created.
metadata    -> Free-form dict. Not fully populated yet, but must be
               able to eventually carry workspace/capability/agent/
               provider/model context (see project_idea.md) without a
               schema change.
usage       -> TokenUsage for this message. Zero/unset until Episode 5
               wires up real provider responses -- the shape exists
               now so nothing downstream (TUI display, limits,
               billing) needs to change when real numbers start
               flowing through it.

invocation  -> InvocationMetadata. Kept as its own typed field rather
               than stuffed into the generic `metadata` dict, because
               "which workspace/capability/agent/provider/model
               produced this message" is a specific, recurring
               question the frontend will ask -- it deserves a stable
               shape, not a loosely-typed dict key that might be
               spelled differently by different callers. `metadata`
               remains for genuinely free-form, per-message extras.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

from runtime.errors import RuntimeException


class MessageValidationError(RuntimeException):
    """Raised when a Message fails validation -- fails early and loud
    rather than letting a malformed message reach the database or a
    provider call."""


class MessageRole(str, Enum):
    SYSTEM = "system"
    USER = "user"
    ASSISTANT = "assistant"
    TOOL = "tool"
    ERROR = "error"


class ContentType(str, Enum):
    TEXT = "text"
    THINKING = "thinking"       # reasoning block -- see MessageContent.provider
    CODE = "code"
    IMAGE = "image"
    TOOL_RESULT = "tool_result"
    STRUCTURED = "structured"


# Content types whose payload is `text`, not `data`.
_TEXTUAL_CONTENT = (ContentType.TEXT, ContentType.THINKING)


@dataclass
class MessageContent:
    """Content is typed so we're not locked into plain text forever.

    `text` is used by TEXT and THINKING content. `data` is a free-form
    dict used by every other content type (e.g. tool name/result for
    TOOL_RESULT). We don't design a dedicated field per content type --
    that would grow forever -- `data` stays generic until a real use case
    demands stronger typing.

    `provider` tags a reasoning/thinking block with whoever produced it
    (B6 §8, for D59). Reasoning blocks are **filtered per provider on
    send, never deleted**: sending Anthropic's thinking blocks to OpenAI
    is at best noise and at worst a 400, but the blocks still belong to
    the conversation and still render. MessageHistory.for_provider() is
    the filter; this field is what makes it possible.
    """

    type: ContentType = ContentType.TEXT
    text: Optional[str] = None
    data: dict[str, Any] = field(default_factory=dict)
    provider: Optional[str] = None

    @staticmethod
    def from_text(text: str) -> "MessageContent":
        return MessageContent(type=ContentType.TEXT, text=text)

    @staticmethod
    def from_thinking(text: str, provider: str) -> "MessageContent":
        return MessageContent(type=ContentType.THINKING, text=text, provider=provider)

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {"type": self.type.value}
        if self.text is not None:
            d["text"] = self.text
        if self.data:
            d["data"] = self.data
        if self.provider is not None:
            d["provider"] = self.provider
        return d

    @staticmethod
    def from_dict(d: dict[str, Any]) -> "MessageContent":
        return MessageContent(
            type=ContentType(d.get("type", "text")),
            text=d.get("text"),
            data=d.get("data", {}),
            provider=d.get("provider"),
        )


@dataclass
class TokenUsage:
    """Data model only -- Episode 5 fills it with real numbers from
    provider responses, Episode 16 builds accounting/limits/billing
    on top of it."""

    input_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0

    def __post_init__(self) -> None:
        if self.total_tokens == 0 and (self.input_tokens or self.output_tokens):
            self.total_tokens = self.input_tokens + self.output_tokens

    def to_dict(self) -> dict[str, int]:
        return {
            "input_tokens": self.input_tokens,
            "output_tokens": self.output_tokens,
            "total_tokens": self.total_tokens,
        }

    @staticmethod
    def from_dict(d: Optional[dict[str, Any]]) -> "TokenUsage":
        if not d:
            return TokenUsage()
        return TokenUsage(
            input_tokens=d.get("input_tokens", 0),
            output_tokens=d.get("output_tokens", 0),
            total_tokens=d.get("total_tokens", 0),
        )


@dataclass
class InvocationMetadata:
    """Answers "which workspace/capability/agent/provider/model
    produced this message?" without the frontend having to hardcode
    assumptions or parse a loosely-typed metadata dict.

    Every field is optional and defaults to None -- Episode 3 defines
    the shape; Episodes 8/11/5 are what actually populate workspace/
    capability/agent and provider/model respectively. A message
    created today (e.g. the demo echo body) simply carries an empty
    InvocationMetadata, or None.
    """

    workspace: Optional[str] = None
    capability: Optional[str] = None
    agent: Optional[str] = None
    provider: Optional[str] = None
    model: Optional[str] = None

    def is_empty(self) -> bool:
        return not any((self.workspace, self.capability, self.agent, self.provider, self.model))

    def to_dict(self) -> dict[str, Any]:
        return {
            "workspace": self.workspace,
            "capability": self.capability,
            "agent": self.agent,
            "provider": self.provider,
            "model": self.model,
        }

    @staticmethod
    def from_dict(d: Optional[dict[str, Any]]) -> Optional["InvocationMetadata"]:
        if not d:
            return None
        return InvocationMetadata(
            workspace=d.get("workspace"),
            capability=d.get("capability"),
            agent=d.get("agent"),
            provider=d.get("provider"),
            model=d.get("model"),
        )


@dataclass
class Message:
    id: str
    session_id: str
    role: MessageRole
    content: MessageContent
    task_id: Optional[str] = None
    timestamp: float = field(default_factory=time.time)
    metadata: dict[str, Any] = field(default_factory=dict)
    invocation: Optional[InvocationMetadata] = None
    usage: TokenUsage = field(default_factory=TokenUsage)

    @staticmethod
    def new(
        session_id: str,
        role: MessageRole,
        text: Optional[str] = None,
        content: Optional[MessageContent] = None,
        task_id: Optional[str] = None,
        metadata: Optional[dict[str, Any]] = None,
        invocation: Optional[InvocationMetadata] = None,
        usage: Optional[TokenUsage] = None,
        message_id: Optional[str] = None,
    ) -> "Message":
        if content is None:
            content = MessageContent.from_text(text or "")
        message = Message(
            id=message_id or str(uuid.uuid4()),
            session_id=session_id,
            role=role,
            content=content,
            task_id=task_id,
            metadata=metadata or {},
            invocation=invocation,
            usage=usage or TokenUsage(),
        )
        message.validate()
        return message

    def validate(self) -> None:
        if not self.id:
            raise MessageValidationError("Message is missing an id")
        if not isinstance(self.role, MessageRole):
            raise MessageValidationError(f"Invalid message role: {self.role!r}")
        if self.content is None:
            raise MessageValidationError("Message is missing content")
        if self.content.type in _TEXTUAL_CONTENT and not self.content.text:
            raise MessageValidationError("Text message has empty content")
        if self.content.type not in _TEXTUAL_CONTENT and not self.content.data:
            raise MessageValidationError(f"{self.content.type.value} message is missing content data")
        if not self.session_id:
            raise MessageValidationError("Message is missing a session_id")
        if self.timestamp is None:
            raise MessageValidationError("Message is missing a timestamp")

    @property
    def text(self) -> str:
        """Convenience accessor for the common case of text content."""
        return self.content.text or ""

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "session_id": self.session_id,
            "task_id": self.task_id,
            "role": self.role.value,
            "content": self.content.to_dict(),
            "timestamp": self.timestamp,
            "metadata": self.metadata,
            "invocation": self.invocation.to_dict() if self.invocation else None,
            "usage": self.usage.to_dict(),
        }

    @staticmethod
    def from_dict(d: dict[str, Any]) -> "Message":
        message = Message(
            id=d["id"],
            session_id=d["session_id"],
            role=MessageRole(d["role"]),
            content=MessageContent.from_dict(d.get("content", {})),
            task_id=d.get("task_id"),
            timestamp=d.get("timestamp", time.time()),
            metadata=d.get("metadata", {}),
            invocation=InvocationMetadata.from_dict(d.get("invocation")),
            usage=TokenUsage.from_dict(d.get("usage")),
        )
        message.validate()
        return message


# ---------------------------------------------------------------------------
# Message type constructors
# ---------------------------------------------------------------------------

def SystemMessage(session_id: str, text: str, **kw: Any) -> Message:
    return Message.new(session_id, MessageRole.SYSTEM, text=text, **kw)


def UserMessage(session_id: str, text: str, **kw: Any) -> Message:
    return Message.new(session_id, MessageRole.USER, text=text, **kw)


def AssistantMessage(session_id: str, text: str, **kw: Any) -> Message:
    return Message.new(session_id, MessageRole.ASSISTANT, text=text, **kw)


def ThinkingMessage(session_id: str, text: str, provider: str, **kw: Any) -> Message:
    """A reasoning block, tagged with the provider that produced it.
    Nothing emits these yet -- no provider in B4 surfaces reasoning
    content -- but the shape has to exist before one does, or the tag
    arrives as a migration instead of a field."""
    content = MessageContent.from_thinking(text, provider)
    return Message.new(session_id, MessageRole.ASSISTANT, content=content, **kw)


def ToolMessage(
    session_id: str,
    tool_name: str,
    result: Any,
    tool_call_id: Optional[str] = None,
    task_id: Optional[str] = None,
    metadata: Optional[dict[str, Any]] = None,
    invocation: Optional[InvocationMetadata] = None,
) -> Message:
    """Structured tool-result representation, not plain text -- so a
    later reader (agent, memory, frontend) can pull `tool_name` /
    `result` back out programmatically instead of re-parsing a string.
    """
    content = MessageContent(
        type=ContentType.TOOL_RESULT,
        data={"tool_name": tool_name, "tool_call_id": tool_call_id, "result": result},
    )
    return Message.new(
        session_id, MessageRole.TOOL, content=content,
        task_id=task_id, metadata=metadata, invocation=invocation,
    )


def ErrorMessage(
    session_id: str,
    text: str,
    error_code: Optional[str] = None,
    error_type: Optional[str] = None,
    task_id: Optional[str] = None,
    metadata: Optional[dict[str, Any]] = None,
    invocation: Optional[InvocationMetadata] = None,
) -> Message:
    """Structured error representation -- error_code/error_type ride
    alongside the human-readable text instead of being buried inside
    it, so the frontend can branch on them without string parsing."""
    meta = dict(metadata or {})
    if error_code is not None:
        meta["error_code"] = error_code
    if error_type is not None:
        meta["error_type"] = error_type
    return Message.new(
        session_id, MessageRole.ERROR, text=text,
        task_id=task_id, metadata=meta, invocation=invocation,
    )