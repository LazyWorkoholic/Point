r"""load_task_result -- the "load" half of reference mode (B10).

Reference mode (B8) attaches a pointer rather than a body: the model sees
that a past task produced a result, and pulls the full text only if it
decides the result matters. This tool is that pull.

**Scoped to the current session, deliberately.** A task id is not a
capability -- if any agent could load any task's output by id, an agent
with `reads_session` only would be able to read across every session in
the workspace, which is exactly the ceiling `reads_project`/
`reads_global` exist to enforce. Cross-session loading, if it is ever
wanted, is a memory-scope decision (B13), not a tool argument.
"""

from __future__ import annotations

from typing import Any

from managers.message import MessageRole
from tools.models import Tool, ToolResult

LOAD_TASK_RESULT_SCHEMA = {
    "type": "object",
    "properties": {
        "task_id": {
            "type": "string",
            "description": "The id of a past task in this session.",
        },
    },
    "required": ["task_id"],
}


async def _load_task_result(arguments: dict[str, Any], ctx: Any) -> ToolResult:
    task_id = arguments["task_id"]
    messages = await ctx.services.memory.task_messages(ctx.session_id, task_id)
    if not messages:
        return ToolResult.failure(
            f"no result found for task {task_id!r} in this session"
        )
    text = "\n\n".join(
        m.text for m in messages if m.role == MessageRole.ASSISTANT and m.text
    )
    if not text:
        return ToolResult.failure(
            f"task {task_id!r} exists but produced no assistant text"
        )
    return ToolResult.success(text, task_id=task_id, messages=len(messages))


LOAD_TASK_RESULT = Tool(
    name="load_task_result",
    description=(
        "Load the full text a past task in this session produced. Use this "
        "when a result is referenced but not included, to decide whether it "
        "is worth reading in full."
    ),
    schema=LOAD_TASK_RESULT_SCHEMA,
    timeout_s=15,
    run=_load_task_result,
)
