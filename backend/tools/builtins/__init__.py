r"""The four v1 tools (B10).

    read_file(path)              guarded by B7's resolve(), size-capped,
                                 returns content + mtime
    list_files(prefix, limit)    from B7's index
    web_search(query, limit)     provider-agnostic; title/url/snippet
    load_task_result(task_id)    the full text of a past result -- the
                                 "load" half of reference mode

There is **no terminal tool and no write tool**, and there never will be
for these agents. Agents return regions; Go computes the diff; the user
applies it. B10's Do NOT section lifts the command-execution prohibition
for exactly one future agent (the Coding Assistant, D84, v2) and for
nothing else.

Every tool follows the same two rules:

  * Expected refusals are `ToolResult(ok=False)`, never exceptions -- so
    the model reads "that path is outside the project" and tries
    something else instead of the task dying.
  * Nothing here holds a database handle or a provider client. What a
    tool may touch arrives on `AgentContext.services`, which carries
    managers only.
"""

from __future__ import annotations

from tools.builtins.filesystem import LIST_FILES, READ_FILE
from tools.builtins.task_results import LOAD_TASK_RESULT
from tools.builtins.web import WEB_SEARCH
from tools.models import Tool

BUILTIN_TOOLS: tuple[Tool, ...] = (READ_FILE, LIST_FILES, WEB_SEARCH, LOAD_TASK_RESULT)

__all__ = ["BUILTIN_TOOLS", "READ_FILE", "LIST_FILES", "WEB_SEARCH", "LOAD_TASK_RESULT"]
