r"""AgentConfig: the *capability* as configuration, not code (B9).

project_idea.md §2: "Agent = capability... Stateless configuration."
Three agents ship in v1 (`agents\builtins.py`) and they differ
**structurally** -- which tools, which scopes, how many steps -- never
by a special-cased class. This file is the proof that works: one frozen
dataclass, zero subclasses, mirrored field-for-field from the `agents`
table B6 put in the ground.

`provider`, `model`, `effort` are `None` = inherit. Resolving what they
inherit *from* -- task override, then session, then this config, then
global config -- is `agents\executor.py`'s job, not this file's.

**There is no `writes_global` field.** The permission ceiling on what an
agent can write is enforced by the field's ABSENCE, not by a runtime
check reading a flag that says no. An agent config that wanted to write
global memory would have nowhere to put that intent.

This is a plain data model, same shape as `managers\session.py`'s
`Session` and `managers\project.py`'s `Project` -- business rules
(seeding, resolution, read-only enforcement) live one layer up, in
`agents\registry.py` and `database\agent_repository.py`.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from managers.context import ContextPolicy


@dataclass(frozen=True)
class AgentConfig:
    id: str
    name: str
    description: str
    builtin: bool

    # None = inherit. agents\executor.py resolves the chain:
    # task override > session > this config > global config.
    provider: Optional[str]
    model: Optional[str]
    effort: Optional[str]

    system_prompt: str
    tools: tuple[str, ...]

    reads_session: bool
    reads_project: bool
    reads_global: bool
    writes_proposals: bool

    stm_window: int
    max_steps: int
    max_tool_calls: int
    timeout_s: int
    default_attach_mode: str

    def to_context_policy(self) -> ContextPolicy:
        """Adapts *to* B8's `ContextPolicy` -- never replaces it, and
        `managers\\context.py` never grows a dependency on this type in
        the other direction. Only the four fields the builder actually
        reads cross this boundary; tools, step limits, timeouts, and
        scope permissions are none of the builder's business.
        """
        return ContextPolicy(
            system_prompt=self.system_prompt,
            stm_window=self.stm_window,
            default_attach_mode=self.default_attach_mode,
            effort=self.effort,
        )
