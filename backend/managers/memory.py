r"""Proposal: B11's narrow stopgap for "Research produced a fact worth
keeping," filled into this (previously empty) file the same way B9
filled `managers\agent.py` with only `AgentConfig` before `agents\`
existed as a package.

**This is NOT B13's memory system.** B13 owns `memory_records` /
`memory_scopes` (already in the schema since v2, unused until then),
`MemoryService.propose()`, review (`accept`/`reject`/`edit`/`bind`/
`forget`), tagging, and search. Those need real usage data first --
B13's own doc will not start without three observed failures from
actually using B11-B12.

Research still needs to emit *something* durable and listable today
(B11's own Done-when #5/#6), without reaching into a table and a
Protocol method that belong to an episode that has not started. So:
one new table (`agent_proposals`, migration v4 -- deliberately not
`memory_records`), one narrow dataclass here, one status
(`pending` -- v1 has no accept/reject path), and a per-session count as
the entire enforcement for "at most 2-3 per session."

This is now a confirmed pattern in this codebase, not a one-off: B9's
`ContextPolicy` (not `AgentConfig`), B10's `BoundsOverride` (not a
mutated `AgentConfig`), and this. See decisions.md D97. When B13
starts, it reads B11's completed doc and decides how its real
`MemoryRecord` type adapts to what already exists here -- it does not
get to assume this table was never populated.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from typing import Optional

# Only status that exists in v1. B13 owns accepted/rejected and the
# transitions between them; a status column with only one value in it
# still has to be a real column, not a comment, because B13 needs it to
# already be there when it adds the second and third values.
PROPOSAL_STATUS_PENDING = "pending"


@dataclass(frozen=True)
class Proposal:
    id: str
    session_id: str
    content: str
    task_id: Optional[str] = None
    agent_id: Optional[str] = None
    status: str = PROPOSAL_STATUS_PENDING
    created_at: float = field(default_factory=time.time)

    @staticmethod
    def new(
        session_id: str,
        content: str,
        task_id: Optional[str] = None,
        agent_id: Optional[str] = None,
    ) -> "Proposal":
        return Proposal(
            id=str(uuid.uuid4()),
            session_id=session_id,
            content=content,
            task_id=task_id,
            agent_id=agent_id,
        )

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "session_id": self.session_id,
            "task_id": self.task_id,
            "agent_id": self.agent_id,
            "content": self.content,
            "status": self.status,
            "created_at": self.created_at,
        }
