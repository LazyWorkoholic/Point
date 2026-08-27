r"""ToolRegistry: what exists, and what a given agent may call (B10).

Two rules, and the second is the whole point of the file:

  * **Tools are granted, never invented.** `list_for(agent_config)`
    returns only the tools the config names -- an agent cannot reach a
    registered tool it was not granted, and there is no "all tools"
    escape hatch to forget to close.
  * **A config naming an unregistered tool fails at LOAD time.**
    `validate_grants()` runs during seeding, not on the call that would
    have used the tool. A typo in a grant list is a startup error with
    the name in it, not a mid-conversation refusal the user reads as the
    model being unhelpful.
"""

from __future__ import annotations

from typing import Iterable, Optional

from logging_setup import get_logger
from managers.agent import AgentConfig
from runtime.errors import ToolNotFoundError, ToolNotGrantedError
from tools.models import Tool

_log = get_logger("tools.registry")


class ToolRegistry:
    def __init__(self) -> None:
        self._tools: dict[str, Tool] = {}

    def register(self, tool: Tool) -> None:
        if tool.name in self._tools:
            # Last registration wins, but say so. Two tools under one
            # name is a packaging mistake, and a silent overwrite is how
            # an agent ends up calling the other one.
            _log.warning("Tool %r re-registered; replacing the previous one", tool.name)
        self._tools[tool.name] = tool

    def get(self, name: str) -> Tool:
        tool = self._tools.get(name)
        if tool is None:
            raise ToolNotFoundError(f"No tool registered as {name!r}")
        return tool

    def names(self) -> list[str]:
        return sorted(self._tools)

    def list_for(self, agent: AgentConfig) -> list[Tool]:
        """The tools this agent may call, in the order its config grants
        them. An unregistered grant is skipped with a warning rather than
        raised: `validate_grants()` has already refused that config at
        load time, so reaching here means something bypassed seeding, and
        a running agent losing one tool beats a running agent dying."""
        granted: list[Tool] = []
        for name in agent.tools:
            tool = self._tools.get(name)
            if tool is None:
                _log.warning(
                    "Agent %r grants unregistered tool %r; skipping", agent.id, name
                )
                continue
            granted.append(tool)
        return granted

    def resolve_for(self, agent: AgentConfig, name: str) -> Tool:
        """The one lookup a tool call goes through. Refuses an
        ungranted tool by name, so "an agent called a tool its config
        does not grant" is impossible rather than merely unlikely."""
        if name not in agent.tools:
            raise ToolNotGrantedError(
                f"Agent {agent.id!r} is not granted the tool {name!r} "
                f"(granted: {', '.join(agent.tools) or 'none'})"
            )
        return self.get(name)

    def validate_grants(self, agents: Iterable[AgentConfig]) -> None:
        """Load-time validation. Raises on the first config that names a
        tool this registry does not have."""
        for agent in agents:
            unknown = [name for name in agent.tools if name not in self._tools]
            if unknown:
                raise ToolNotFoundError(
                    f"Agent {agent.id!r} grants unregistered tool(s): "
                    f"{', '.join(sorted(unknown))}. Registered: "
                    f"{', '.join(self.names()) or 'none'}"
                )


def build_default_registry(registry: Optional[ToolRegistry] = None) -> ToolRegistry:
    """The v1 tool set. Imported here rather than in `__init__` so the
    package can be imported without pulling in httpx and the project
    manager."""
    from tools.builtins import BUILTIN_TOOLS

    registry = registry or ToolRegistry()
    for tool in BUILTIN_TOOLS:
        registry.register(tool)
    return registry
