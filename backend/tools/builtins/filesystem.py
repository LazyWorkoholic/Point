r"""read_file and list_files -- both straight through B7 (B10).

Neither tool resolves a path itself. `ProjectManager.read_file()` already
calls `pathguard.resolve_path()`, applies the `index.max_file_kb` cap,
and reports `outside_project`; `ProjectManager.list_files()` already
serves the gitignore-aware index. Re-implementing either here would be
the second path-guard implementation B7's docstring is explicit about
never wanting -- and the second one to drift is the one that lets
something through.

`allow_outside_project` is **not** exposed as an argument. A model must
not be able to opt itself out of the project scope; the escape hatch
exists for a user action (an explicit `@file` outside the root), not for
an agent.
"""

from __future__ import annotations

from typing import Any

from runtime.errors import InvalidEventError, PathDeniedError, ProjectNotFoundError
from tools.models import Tool, ToolResult

READ_FILE_SCHEMA = {
    "type": "object",
    "properties": {
        "path": {
            "type": "string",
            "description": "Path to read, relative to the project root or absolute.",
        },
    },
    "required": ["path"],
}

LIST_FILES_SCHEMA = {
    "type": "object",
    "properties": {
        "prefix": {
            "type": "string",
            "description": "Only list paths starting with this prefix. Empty lists everything.",
            "default": "",
        },
        "limit": {
            "type": "integer",
            "description": "Maximum number of paths to return.",
            "default": 200,
            "minimum": 1,
            "maximum": 2000,
        },
    },
    "required": [],
}


async def _read_file(arguments: dict[str, Any], ctx: Any) -> ToolResult:
    services = ctx.services
    if services.project is None:
        return ToolResult.failure(
            "no project is open, so there is no root to resolve a path against"
        )
    try:
        read = await services.projects.read_file(services.project, arguments["path"])
    except PathDeniedError as exc:
        # The refusal reaches the model as a tool result, not as an
        # exception (Done-when #6). This is the single most important
        # line in the file: a guard that crashes the task teaches the
        # model nothing, and a guard that returns teaches it the scope.
        return ToolResult.failure(str(exc), denied=True)
    except (InvalidEventError, ProjectNotFoundError) as exc:
        return ToolResult.failure(str(exc))
    except OSError as exc:
        return ToolResult.failure(f"could not read {arguments['path']!r}: {exc}")

    return ToolResult.success(
        read["content"],
        path=read["path"],
        mtime=read["mtime"],
        size=read["size"],
        outside_project=read["outside_project"],
    )


async def _list_files(arguments: dict[str, Any], ctx: Any) -> ToolResult:
    services = ctx.services
    if services.project is None:
        return ToolResult.failure("no project is open, so there is no file index to list")
    paths = await services.projects.list_files(
        services.project, prefix=arguments["prefix"], limit=arguments["limit"]
    )
    if not paths:
        return ToolResult.success("", count=0, prefix=arguments["prefix"])
    return ToolResult.success("\n".join(paths), count=len(paths), prefix=arguments["prefix"])


READ_FILE = Tool(
    name="read_file",
    description=(
        "Read one text file from the open project. Returns its contents. "
        "Paths outside the project root are refused. Large and binary files "
        "are refused rather than truncated."
    ),
    schema=READ_FILE_SCHEMA,
    timeout_s=15,
    run=_read_file,
)

LIST_FILES = Tool(
    name="list_files",
    description=(
        "List file paths in the open project, optionally filtered by a path "
        "prefix. Respects .gitignore. Returns one path per line, relative to "
        "the project root."
    ),
    schema=LIST_FILES_SCHEMA,
    timeout_s=30,
    run=_list_files,
)
