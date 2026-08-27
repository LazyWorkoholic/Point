r"""POINT_MOCK-only fault injection for the tool loop (B10).

Done-when #2 and #3 require a tool that *raises* and a tool that *hangs*.
Neither is something a correct tool does, and neither can be produced by
one: `read_file` turns a denied path into a ToolResult, `web_search`
turns a dead network into one. Something has to misbehave on purpose.

The precedent is MockProvider: a test fixture wearing the real
interface, registered only when `POINT_MOCK=1`, never reachable from the
UI. This is the same idea one layer down, and it is deliberately NOT a
mock *tool*:

  * A mock tool would have to be granted to an agent to be callable, and
    B10's rule is that tools are granted, never invented. Granting
    `mock_hangs` to `research` under an env var would make the grant list
    -- the thing Done-when #5 checks -- lie.
  * Injecting instead means the scripted call is a real call to a real
    granted tool (`web_search`), which is what the loop, the bounds, the
    events, and the `tool_calls` row all see.

The hook is a `__mock_fault` argument on the call, stripped before the
tool body ever sees it. With POINT_MOCK unset this module is inert: the
key is stripped and ignored, so a fixture leaking into a real run cannot
change behaviour.
"""

from __future__ import annotations

import asyncio
import os
from typing import Any

FAULT_KEY = "__mock_fault"

RAISE = "raise"
HANG = "hang"


class InjectedToolError(RuntimeError):
    """What `__mock_fault: "raise"` raises. A plain RuntimeError subclass
    on purpose -- the executor must treat it exactly as it would treat a
    genuine bug inside a tool, with no special case anywhere."""


def _enabled() -> bool:
    return os.environ.get("POINT_MOCK") == "1"


def take(arguments: dict[str, Any]) -> tuple[dict[str, Any], Any]:
    """Split a scripted fault off the arguments. Always strips the key,
    whether or not mocking is on."""
    if FAULT_KEY not in arguments:
        return arguments, None
    cleaned = dict(arguments)
    fault = cleaned.pop(FAULT_KEY)
    return cleaned, fault if _enabled() else None


async def apply(fault: Any, tool_name: str) -> None:
    """Perform the fault. `hang` sleeps far past any per-tool timeout, so
    the timeout is what ends it -- which is the behaviour under test."""
    if fault == RAISE:
        raise InjectedToolError(f"injected failure inside tool {tool_name!r}")
    if fault == HANG:
        await asyncio.sleep(3600)
