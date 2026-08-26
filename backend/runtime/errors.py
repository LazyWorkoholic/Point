"""Runtime-specific exceptions.

Every error the runtime can raise lives here so the dispatcher
can catch them by type and turn them into clean error events
instead of leaking stack traces to the Go TUI.
"""

from __future__ import annotations


class RuntimeException(Exception):
    """Base class for all runtime errors."""


class InvalidEventError(RuntimeException):
    """Raised when an incoming event is malformed (bad JSON shape)."""


class UnknownEventTypeError(InvalidEventError):
    """Raised when the dispatcher has no handler for an event type."""


class TaskNotFoundError(RuntimeException):
    """Raised when an operation references a task_id that doesn't exist."""


class TaskAlreadyFinishedError(RuntimeException):
    """Raised when trying to cancel a task that already completed."""


class PathDeniedError(RuntimeException):
    """Raised by managers.pathguard.resolve_path() when a path escapes
    project scope, or when no project is open and the path is either
    relative or allow_outside_project is off (B7).

    Stopgap: B17 folds this into PointError(code="path_denied") along
    with every other RuntimeException subclass in this file. Do not
    build PointError piecemeal before then -- B17 is Phase P2 and
    depends on every episode, this one included.
    """


class ProjectNotFoundError(RuntimeException):
    """Raised when a project_id has no row, or an operation required a
    resolved project and none was found (no 'project_id', and no
    '.point\\' above the given path)."""


class LockNotHeldError(RuntimeException):
    """Raised by heartbeat when there is no lock at all to heartbeat.
    Distinct from "held by someone else", which is a normal LockResult,
    not an error."""


class AgentNotFoundError(RuntimeException):
    """Raised when an agent id/name resolves to no row (B9)."""


class BuiltinAgentImmutableError(RuntimeException):
    """Raised by database.agent_repository when an edit or delete targets
    a builtin=True agent (B9). Enforced at the repository layer, not
    just in the UI -- so there is exactly one place this rule can be
    bypassed, and it is nowhere."""


class ContextOverflowError(RuntimeException):
    """Raised by managers.context.build() when a single message or
    attachment does not fit the whole budget on its own (B8).

    Both numbers ride as attributes, not just inside the string: the
    frontend's status card wants to render "needs 100000, budget 8000",
    and re-parsing that back out of a sentence is how a UI ends up
    coupled to an error message's wording.

    The alternative to raising is sending anyway and letting the
    provider answer with an opaque 400 -- a failure the user cannot act
    on, arriving after the request was paid for.

    Stopgap, exactly like PathDeniedError above: B17 folds this into
    PointError(code="context_overflow"). Do not give it a
    code/retryable/detail shape before then.
    """

    def __init__(self, needed_tokens: int, budget_tokens: int, subject: str = "") -> None:
        self.needed_tokens = needed_tokens
        self.budget_tokens = budget_tokens
        self.subject = subject
        super().__init__(
            f"context_overflow: {subject or 'this item'} needs {needed_tokens} tokens, "
            f"but the entire context budget is {budget_tokens} tokens"
        )
