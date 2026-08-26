"""MessageHistory: ordered-message abstraction over a single session.

This is infrastructure only -- it does NOT implement summarization,
compression, or token-budget windowing. `get_window()` and `search()`
intentionally raise NotImplementedError so it's obvious at the call
site that Episode 7 (Memory) is what fills them in.

Episode 3 addition: plain querying (`filter_by_role`, `since`,
`by_task`) -- ordinary list filtering, not memory or search.
"""

from __future__ import annotations

from typing import Optional

from managers.message import ContentType, Message, MessageRole


class MessageHistory:
    def __init__(self, session_id: str, messages: Optional[list[Message]] = None) -> None:
        self.session_id = session_id
        self._messages: list[Message] = list(messages or [])

    def add(self, message: Message) -> None:
        if message.session_id != self.session_id:
            raise ValueError(
                f"Message belongs to session {message.session_id!r}, "
                f"not this history's session {self.session_id!r}"
            )
        message.validate()
        self._messages.append(message)

    def all(self) -> list[Message]:
        return list(self._messages)

    def last(self, n: int = 1) -> list[Message]:
        return self._messages[-n:]

    def get(self, message_id: str) -> Optional[Message]:
        for m in self._messages:
            if m.id == message_id:
                return m
        return None

    def clear(self) -> None:
        self._messages.clear()

    def __len__(self) -> int:
        return len(self._messages)

    def serialize(self) -> list[dict]:
        return [m.to_dict() for m in self._messages]

    @staticmethod
    def deserialize(session_id: str, data: list[dict]) -> "MessageHistory":
        return MessageHistory(session_id, [Message.from_dict(d) for d in data])

    # -- history querying (plain filtering, not memory/search) ------------

    def filter_by_role(self, role: MessageRole) -> list[Message]:
        return [m for m in self._messages if m.role == role]

    def since(self, timestamp: float) -> list[Message]:
        return [m for m in self._messages if m.timestamp >= timestamp]

    def by_task(self, task_id: str) -> list[Message]:
        return [m for m in self._messages if m.task_id == task_id]

    def for_provider(self, provider: str) -> list[Message]:
        """History as `provider` should see it: reasoning blocks produced
        by a *different* provider are filtered out of the send, never
        deleted from the history (B6 §8, D59). An untagged reasoning
        block is kept -- absence of a tag is not evidence of another
        provider."""
        return [
            m for m in self._messages
            if m.content.type is not ContentType.THINKING
            or m.content.provider in (None, provider)
        ]

    # -- context-window interface (NOT implemented here) ------------------

    def get_recent(self, limit: int) -> list[Message]:
        """Naive recency window -- correct as a default, but Episode 7
        (Memory) will replace this with STM/summary/LTM-aware selection."""
        return self._messages[-limit:]

    def get_window(self, max_tokens: int) -> list[Message]:
        raise NotImplementedError("Token-budget windowing arrives in Episode 7 (Memory)")

    def search(self, query: str) -> list[Message]:
        raise NotImplementedError("Message search arrives with Memory/RAG (Episodes 7/15)")