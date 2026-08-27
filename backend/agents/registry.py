r"""AgentRegistry: seeds built-ins from code, loads user agents from the
`agents` table (none exist in v1 -- the table is the hook for the
parked dynamic-agents feature), and resolves an id or a name to a
frozen AgentConfig.
"""

from __future__ import annotations

import aiosqlite

from typing import Optional

from agents.builtins import BUILTIN_AGENTS, GENERAL_AGENT_ID
from database.agent_repository import AgentRepository
from logging_setup import get_logger
from managers.agent import AgentConfig
from managers.session import Session
from runtime.errors import AgentNotFoundError
from tools.registry import ToolRegistry

_log = get_logger("agents.registry")


class AgentRegistry:
    def __init__(
        self, conn: aiosqlite.Connection, tools: Optional[ToolRegistry] = None
    ) -> None:
        self._repo = AgentRepository(conn)
        self._tools = tools

    async def seed_builtins(self) -> None:
        """Upserted by id, every startup. Code is the source of truth for
        a built-in's fields -- a stale row from an older build is
        overwritten, not accumulated alongside it.

        B10: grants are validated **before** the first upsert, not after
        the last. A built-in naming a tool that does not exist must not
        reach the table at all -- half-seeded is worse than not seeded,
        because the next startup would find a row and believe it."""
        self._validate_grants(BUILTIN_AGENTS)
        for config in BUILTIN_AGENTS:
            await self._repo.upsert(config)
        _log.info("Seeded %d built-in agent(s)", len(BUILTIN_AGENTS))

    async def validate_all(self) -> None:
        """Every agent in the table, built-in or not. Called once at
        startup, after seeding -- so a user agent (the parked dynamic
        -agents feature) naming a tool that no longer exists is a startup
        error naming the tool, not a refusal in the middle of someone's
        conversation."""
        self._validate_grants(await self.list())

    def _validate_grants(self, configs) -> None:
        if self._tools is None:
            return
        self._tools.validate_grants(configs)

    async def get(self, agent_id: str) -> AgentConfig:
        config = await self._repo.fetch_by_id(agent_id)
        if config is None:
            raise AgentNotFoundError(f"No agent with id {agent_id!r}")
        return config

    async def list(self) -> list[AgentConfig]:
        return await self._repo.fetch_all()

    async def resolve(self, name_or_id: str) -> AgentConfig:
        """Direct id match first (the common case, and the only one
        `builtins.py`'s ids need). Falling back to a case-insensitive
        name match costs nothing and is the only "alias" a user agent
        could have in v1 -- there is no separate alias table."""
        config = await self._repo.fetch_by_id(name_or_id)
        if config is not None:
            return config
        for candidate in await self.list():
            if candidate.name.lower() == name_or_id.lower():
                return candidate
        raise AgentNotFoundError(f"No agent matches {name_or_id!r}")

    async def resolve_for_session(self, session: Session) -> AgentConfig:
        """Which agent a session uses: `session.settings["agent_id"]`,
        falling back to the general agent when unset, or when the id no
        longer resolves to anything (there is no way to remove a
        built-in today, but a session can still name one that a future
        build dropped from `BUILTIN_AGENTS`)."""
        agent_id = (session.settings or {}).get("agent_id") or GENERAL_AGENT_ID
        try:
            return await self.resolve(agent_id)
        except AgentNotFoundError:
            _log.warning(
                "Session %s names unknown agent %r; falling back to %r",
                session.id, agent_id, GENERAL_AGENT_ID,
            )
            return await self.get(GENERAL_AGENT_ID)

    async def set_model(self, agent_id: str, model: str) -> None:
        """Repository-layer enforcement does the actual refusing; this is
        just the one field `cli.py agents set-model` needs to reach."""
        await self._repo.update_fields(agent_id, {"model": model})

    async def delete(self, agent_id: str) -> None:
        await self._repo.delete(agent_id)
