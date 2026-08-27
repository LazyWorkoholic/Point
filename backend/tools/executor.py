r"""ToolExecutor: validate -> run -> time out -> emit -> record (B10).

There are exactly two outcomes, and the line between them is whether the
model can do anything about it:

  * **Something the model got wrong, or that the world refused** -- bad
    arguments, a path outside the project, a dead network, a missing file
    -- comes back as `ToolResult(ok=False)` and is fed to the model as
    tool output. It can then try something else. This is B10's Build
    rule for schema failures and its Done-when #6 for the path guard,
    generalized to every expected refusal.
  * **Something broken** -- a tool body raising, or hanging past its
    timeout -- raises `ToolExecutionError` / `ToolTimeoutError`, each
    carrying a `code`. The loop turns that into `task.failed` with the
    code attached and keeps whatever partial output exists (Done-when #2
    and #3). Feeding a traceback back to the model would ask it to debug
    Point, which it cannot do and should not be asked to.

Neither kind ever escapes as a bare traceback, and **both are recorded**:
the `tool_calls` row and the `tool_completed` event are written before
the raise, not after the happy path, so a failing call is exactly as
answerable later as a successful one.
"""

from __future__ import annotations

import asyncio
import time
from typing import Any, Awaitable, Callable, Optional

from logging_setup import get_logger
from runtime.errors import ToolExecutionError, ToolTimeoutError
from tools import faults, schema
from tools.models import Tool, ToolCallRecord, ToolResult

_log = get_logger("tools.executor")

# Called with one ToolCallRecord after every call. Optional -- an
# executor with no recorder behaves identically, minus the durable row,
# the same way Task behaves without a TaskRecorder (B1/B6).
ToolCallRecorder = Callable[[ToolCallRecord], Awaitable[None]]

# Called with (event_name, payload) so the caller can turn a tool event
# into whatever it emits. The executor does not know about StreamEvents,
# envelopes, or sequence numbers -- same boundary Task keeps to its sink.
ToolEmit = Callable[[str, dict[str, Any]], Awaitable[None]]

EVENT_STARTED = "tool_started"
EVENT_COMPLETED = "tool_completed"


class ToolExecutor:
    def __init__(
        self,
        *,
        emit: Optional[ToolEmit] = None,
        recorder: Optional[ToolCallRecorder] = None,
    ) -> None:
        self._emit = emit
        self._recorder = recorder

    async def execute(
        self,
        tool: Tool,
        arguments: dict[str, Any],
        agent_context: Any,
        *,
        task_id: str,
        session_id: str,
    ) -> ToolResult:
        arguments = dict(arguments or {})
        arguments, fault = faults.take(arguments)

        await self._emit_event(EVENT_STARTED, {
            "tool": tool.name, "arguments": arguments, "timeout_s": tool.timeout_s,
        })
        started = time.monotonic()

        # `broken` is the exception to re-raise *after* the call has been
        # emitted and recorded. Raising straight out of _run_guarded()
        # would lose both, and "records every call" has to mean the ones
        # that went wrong most of all.
        result, broken = await self._run_guarded(tool, arguments, agent_context, fault)

        duration_ms = int((time.monotonic() - started) * 1000)
        await self._emit_event(EVENT_COMPLETED, {
            "tool": tool.name,
            "ok": result.ok,
            "error": result.error,
            "duration_ms": duration_ms,
            "tokens_estimate": result.tokens_estimate,
        })
        await self._record(ToolCallRecord(
            task_id=task_id, session_id=session_id, tool=tool.name, args=arguments,
            ok=result.ok, error=result.error, duration_ms=duration_ms,
            tokens_estimate=result.tokens_estimate,
        ))
        if broken is not None:
            raise broken
        return result

    # -- the guarded run ---------------------------------------------------

    async def _run_guarded(
        self, tool: Tool, arguments: dict[str, Any], agent_context: Any, fault: Any
    ) -> tuple[ToolResult, Optional[BaseException]]:
        """Returns (what to record/emit, what to re-raise or None)."""
        problems = schema.validate(arguments, tool.schema)
        if problems:
            # NOT an exception: this is the model getting its own
            # arguments wrong, and the model is the one that can fix it.
            return ToolResult.failure(
                f"invalid arguments for {tool.name}: " + "; ".join(problems)
            ), None
        filled = schema.apply_defaults(arguments, tool.schema)

        async def body() -> ToolResult:
            await faults.apply(fault, tool.name)
            return await tool.run(filled, agent_context)

        try:
            return await asyncio.wait_for(body(), timeout=tool.timeout_s), None
        except asyncio.TimeoutError:
            _log.warning("Tool %r timed out after %ss", tool.name, tool.timeout_s)
            message = f"{tool.name} timed out after {tool.timeout_s}s and was stopped"
            return ToolResult.failure(message, timed_out=True), ToolTimeoutError(message)
        except asyncio.CancelledError:
            # The task itself is being cancelled. Never converted --
            # a cancelled task must actually stop, not report a failed
            # tool and carry on to the next step.
            raise
        except Exception as exc:  # noqa: BLE001 - deliberately converted
            _log.exception("Tool %r raised", tool.name)
            message = f"{tool.name} failed: {type(exc).__name__}: {exc}"
            return ToolResult.failure(message, raised=True), ToolExecutionError(message)

    # -- plumbing ----------------------------------------------------------

    async def _emit_event(self, name: str, payload: dict[str, Any]) -> None:
        if self._emit is None:
            return
        await self._emit(name, payload)

    async def _record(self, record: ToolCallRecord) -> None:
        """Failing to write the row must never fail the call -- same rule
        Task._record() follows for the `tasks` table. The row is a record
        of the work, not the work."""
        if self._recorder is None:
            return
        try:
            await self._recorder(record)
        except Exception:  # noqa: BLE001
            _log.warning("Could not record tool call for %s", record.tool, exc_info=True)
