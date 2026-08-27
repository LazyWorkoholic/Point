r"""The tool system (B10).

Layout mirrors `agents\` (B9): a plain data model, a registry with the
rules on top of it, an executor, and the built-in set as code.

    tools\
    ├── models.py       Tool, ToolResult, ToolCallRecord -- data only
    ├── schema.py       the JSON Schema subset arguments are checked against
    ├── registry.py     ToolRegistry: register / get / list_for
    ├── executor.py     ToolExecutor: validate -> run -> time out -> emit -> record
    ├── faults.py       POINT_MOCK-only fault injection (raise / hang)
    └── builtins\       the four v1 tools

`managers\tool.py` is **deleted**, not filled -- same call B9 made for
`managers\agent.py`'s business rules. A registry, an executor, a schema
checker, and four tools are not one manager.

Nothing in this package writes a file or runs a command, and nothing in
it ever will: agents return regions, Go computes the diff, the user
applies it. That is a permanent property of the tool set, not a v1
limitation (B10's Do NOT section).
"""
