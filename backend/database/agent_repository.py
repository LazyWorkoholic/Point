r"""AgentRepository: raw SQL only, same shape as ProjectRepository and
SessionRepository. No business rules here except the one that must
live at this exact layer: a builtin=True row rejects edits and deletes
before a single UPDATE/DELETE statement runs (B9's Done-when #2) --
"rejected at the repository layer, not just in the UI" only means
something if there is no other way to reach the table.
"""

from __future__ import annotations

import json
import time
from typing import Any, Optional

import aiosqlite

from managers.agent import AgentConfig
from runtime.errors import AgentNotFoundError, BuiltinAgentImmutableError

# Columns a caller may patch through update_fields(). id/builtin/created_at
# are identity and provenance -- never patched in place.
_PATCHABLE_FIELDS = (
    "name", "description", "provider", "model", "effort", "system_prompt",
    "tools_json", "reads_session", "reads_project", "reads_global",
    "writes_proposals", "stm_window", "max_steps", "max_tool_calls",
    "timeout_s", "default_attach_mode",
)


class AgentRepository:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._conn = conn

    # -- seeding ----------------------------------------------------------

    async def upsert(self, config: AgentConfig) -> None:
        """Insert, or overwrite in place if `config.id` already has a row.

        This is the seeding entry point (`AgentRegistry.seed_builtins()`
        calls it every startup so code stays the source of truth for a
        built-in's fields) -- it deliberately does NOT go through the
        builtin check in `update_fields()`/`delete()` below, because
        seeding a builtin is exactly the operation those two exist to
        prevent everyone else from doing.
        """
        now = time.time()
        row = await self._conn.execute(
            "SELECT created_at FROM agents WHERE id = ?", (config.id,)
        )
        existing = await row.fetchone()
        created_at = existing["created_at"] if existing else now

        await self._conn.execute(
            """
            INSERT INTO agents (
                id, name, description, builtin, provider, model, effort,
                system_prompt, tools_json, reads_session, reads_project,
                reads_global, writes_proposals, stm_window, max_steps,
                max_tool_calls, timeout_s, default_attach_mode,
                created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                name                = excluded.name,
                description         = excluded.description,
                builtin              = excluded.builtin,
                provider             = excluded.provider,
                model                = excluded.model,
                effort               = excluded.effort,
                system_prompt        = excluded.system_prompt,
                tools_json           = excluded.tools_json,
                reads_session        = excluded.reads_session,
                reads_project        = excluded.reads_project,
                reads_global         = excluded.reads_global,
                writes_proposals     = excluded.writes_proposals,
                stm_window           = excluded.stm_window,
                max_steps            = excluded.max_steps,
                max_tool_calls       = excluded.max_tool_calls,
                timeout_s            = excluded.timeout_s,
                default_attach_mode  = excluded.default_attach_mode,
                updated_at           = excluded.updated_at
            """,
            (
                config.id, config.name, config.description, int(config.builtin),
                config.provider, config.model, config.effort, config.system_prompt,
                json.dumps(list(config.tools)), int(config.reads_session),
                int(config.reads_project), int(config.reads_global),
                int(config.writes_proposals), config.stm_window, config.max_steps,
                config.max_tool_calls, config.timeout_s, config.default_attach_mode,
                created_at, now,
            ),
        )
        await self._conn.commit()

    # -- reads --------------------------------------------------------------

    async def fetch_by_id(self, agent_id: str) -> Optional[AgentConfig]:
        cursor = await self._conn.execute("SELECT * FROM agents WHERE id = ?", (agent_id,))
        row = await cursor.fetchone()
        return self._row_to_config(row) if row else None

    async def fetch_all(self) -> list[AgentConfig]:
        cursor = await self._conn.execute("SELECT * FROM agents ORDER BY builtin DESC, name")
        rows = await cursor.fetchall()
        return [self._row_to_config(row) for row in rows]

    @staticmethod
    def _row_to_config(row: aiosqlite.Row) -> AgentConfig:
        tools_json = row["tools_json"]
        return AgentConfig(
            id=row["id"],
            name=row["name"],
            description=row["description"] or "",
            builtin=bool(row["builtin"]),
            provider=row["provider"],
            model=row["model"],
            effort=row["effort"],
            system_prompt=row["system_prompt"] or "",
            tools=tuple(json.loads(tools_json)) if tools_json else (),
            reads_session=bool(row["reads_session"]),
            reads_project=bool(row["reads_project"]),
            reads_global=bool(row["reads_global"]),
            writes_proposals=bool(row["writes_proposals"]),
            stm_window=row["stm_window"],
            max_steps=row["max_steps"],
            max_tool_calls=row["max_tool_calls"],
            timeout_s=row["timeout_s"],
            default_attach_mode=row["default_attach_mode"],
        )

    # -- writes (user agents only -- builtins refuse here, not in the UI) --

    async def update_fields(self, agent_id: str, patch: dict[str, Any]) -> None:
        existing = await self.fetch_by_id(agent_id)
        if existing is None:
            raise AgentNotFoundError(f"No agent with id {agent_id!r}")
        if existing.builtin:
            raise BuiltinAgentImmutableError(
                f"Agent {agent_id!r} is builtin and read-only -- edits are "
                "rejected at the repository layer"
            )
        unknown = set(patch) - set(_PATCHABLE_FIELDS)
        if unknown:
            raise ValueError(f"Cannot patch field(s): {sorted(unknown)}")
        if not patch:
            return
        columns = ", ".join(f"{key} = ?" for key in patch)
        values = list(patch.values())
        await self._conn.execute(
            f"UPDATE agents SET {columns}, updated_at = ? WHERE id = ?",
            (*values, time.time(), agent_id),
        )
        await self._conn.commit()

    async def delete(self, agent_id: str) -> None:
        existing = await self.fetch_by_id(agent_id)
        if existing is None:
            raise AgentNotFoundError(f"No agent with id {agent_id!r}")
        if existing.builtin:
            raise BuiltinAgentImmutableError(
                f"Agent {agent_id!r} is builtin and cannot be deleted -- "
                "rejected at the repository layer"
            )
        await self._conn.execute("DELETE FROM agents WHERE id = ?", (agent_id,))
        await self._conn.commit()
