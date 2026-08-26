"""Dispatcher: routes a parsed IncomingEvent to the right handler.

This is intentionally dumb. It doesn't contain business logic — it
just maps event type -> handler coroutine. New event types get a new
entry here, not a new if/elif chain buried in Runtime.
"""

from __future__ import annotations

from typing import Awaitable, Callable

from runtime.errors import UnknownEventTypeError
from runtime.events import IncomingEvent, IncomingEventType

Handler = Callable[[IncomingEvent], Awaitable[None]]


class Dispatcher:
    def __init__(self) -> None:
        self._handlers: dict[IncomingEventType, Handler] = {}

    def register(self, event_type: IncomingEventType, handler: Handler) -> None:
        self._handlers[event_type] = handler

    async def dispatch(self, event: IncomingEvent) -> None:
        handler = self._handlers.get(event.type)
        if handler is None:
            raise UnknownEventTypeError(f"No handler registered for {event.type}")
        await handler(event)