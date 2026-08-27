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


# ---------------------------------------------------------------------------
# Tool system (B10)
# ---------------------------------------------------------------------------

class ToolError(RuntimeException):
    """Base class for everything the tool system raises, and the one B17
    folds into `PointError(code=...)` alongside `PathDeniedError` (B7)
    and `ContextOverflowError` (B8).

    Unlike those two, this family carries its `code` from the start --
    B10's Done-when #4 requires `bounds_exceeded` to be distinguishable
    on the wire and in the `tasks` row, so the code could not wait for
    B17 to invent one. B17's job here is therefore narrower: fold these
    into the shared envelope, do not invent codes for them.

    Only *broken* things raise. An expected refusal -- bad arguments, a
    denied path, a dead network -- is a `ToolResult(ok=False)` handed
    back to the model, never one of these.
    """

    code = "tool_error"


class ToolNotFoundError(ToolError):
    """No tool is registered under that name. Raised at LOAD time by
    `ToolRegistry.validate_grants()` when an AgentConfig names a tool
    that does not exist -- a typo in a grant list is a startup error, not
    a mid-conversation refusal."""

    code = "tool_not_found"


class ToolNotGrantedError(ToolError):
    """The tool exists, but this agent's config does not grant it. Tools
    are granted, never invented; there is no path from a model naming a
    tool to that tool running."""

    code = "tool_not_granted"


class ToolExecutionError(ToolError):
    """A tool body raised. The runtime survives -- this ends one task
    with a code, and the next request works immediately."""

    code = "tool_error"


class ToolTimeoutError(ToolError):
    """A tool ran past its per-tool timeout and was stopped."""

    code = "tool_timeout"


class ToolsUnsupportedError(ToolError):
    """An agent requiring tools was pointed at a provider that cannot
    take them. Raised at INVOCATION, before a token is streamed, so the
    user gets a clear message instead of a model that silently ignores
    every tool it was never sent."""

    code = "tools_unsupported"


class BoundsExceededError(ToolError):
    """A bounded agent loop hit `max_steps`, `max_tool_calls`, or
    `timeout_s` (B10). **Not a failure of the work** -- whatever partial
    output exists is kept and persisted; this only says the loop stopped
    at its boundary rather than at an answer."""

    code = "bounds_exceeded"

    def __init__(self, message: str, *, bound: str = "", limit: int = 0) -> None:
        self.bound = bound
        self.limit = limit
        super().__init__(message)
