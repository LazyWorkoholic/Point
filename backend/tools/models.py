r"""Tool and ToolResult: the data model, no behaviour (B10).

Same role `managers\agent.py`'s AgentConfig plays for agents -- the rules
(granting, validating, timing out, recording) live one layer up in
`registry.py` and `executor.py`.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Awaitable, Callable, Optional


@dataclass
class ToolResult:
    """What a tool hands back. **Never an exception for an expected
    refusal** -- a denied path, a missing file, a bad argument are all
    `ok=False` results fed back to the model as tool output, because a
    model that is told "that path is outside the project" can try
    something else, and a model that gets a traceback cannot.

    `tokens_estimate` is what the tool costs the next context build.
    `meta` is free-form per-tool detail (mtime, result count, url) that
    the model does not need in its content but a later reader does.
    """

    ok: bool
    content: str = ""
    error: Optional[str] = None
    tokens_estimate: int = 0
    meta: dict[str, Any] = field(default_factory=dict)

    @staticmethod
    def success(content: str, **meta: Any) -> "ToolResult":
        return ToolResult(
            ok=True, content=content, tokens_estimate=estimate_tokens(content), meta=meta
        )

    @staticmethod
    def failure(error: str, **meta: Any) -> "ToolResult":
        return ToolResult(
            ok=False, content="", error=error, tokens_estimate=estimate_tokens(error), meta=meta
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            "ok": self.ok,
            "content": self.content,
            "error": self.error,
            "tokens_estimate": self.tokens_estimate,
            "meta": self.meta,
        }


def estimate_tokens(text: str) -> int:
    """The same 4-characters-per-token approximation B8's context builder
    uses. Deliberately not a real tokenizer: this number budgets a tool
    result against a context, and B8 already established that an
    approximation everything shares beats five exact numbers that
    disagree."""
    return max(1, (len(text) + 3) // 4) if text else 0


# A tool body. Async because three of the four v1 tools do I/O, and the
# one that does not (`load_task_result`) still reads through a manager
# that does. AgentContext arrives as the second argument so a tool can
# reach the project root and the session without any tool holding a
# database handle of its own.
ToolRun = Callable[[dict[str, Any], Any], Awaitable[ToolResult]]


@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    schema: dict[str, Any]          # JSON Schema for parameters
    timeout_s: int
    run: ToolRun

    def to_schema(self) -> dict[str, Any]:
        """The common tool schema handed to a provider adapter, which
        translates it into its own shape (`MessageTranslator.
        tools_to_provider_format`). One dict shape crosses that boundary,
        never a Tool -- providers know nothing about timeouts or
        callables."""
        return {
            "name": self.name,
            "description": self.description,
            "parameters": self.schema,
        }


@dataclass(frozen=True)
class ToolCallRecord:
    """One executed call, as it lands in the `tool_calls` table (B10's
    migration v3). This is the answer to "which document contaminated
    this answer" -- args and outcome, per task, kept after the task
    object is swept out of memory."""

    task_id: str
    session_id: str
    tool: str
    args: dict[str, Any]
    ok: bool
    error: Optional[str]
    duration_ms: int
    tokens_estimate: int
