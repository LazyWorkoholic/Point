r"""The three built-in agents (project_idea.md §7), as code -- the
source of truth `AgentRegistry.seed_builtins()` upserts into the
`agents` table on every startup.

They differ **structurally**, not by prompt:

                General      Filler-Explainer     Research
    tools       --           --                   read_file, list_files,
                                                  web_search,
                                                  load_task_result
    reads proj  no           yes (explainer mode)  yes
    writes mem  no           no                    proposals only
    multi-step  no           no                    yes, bounded
    attach      full         full                  reference

Behaviour (what the Filler-Explainer mode split actually does, what the
Research loop actually does) is B11. This file only owns the config
that makes those differences visible before any of it is built.

**Research's tool grants were renamed in B10.** They read
`("web", "fs", "load_task_result")` when this file shipped in B9 --
placeholder category names written before any tool existed. B10
registered the real four (`read_file`, `list_files`, `web_search`,
`load_task_result`) and made an unregistered grant a *load-time* failure,
which those three placeholder names would have tripped on every startup.
Built-ins are re-upserted from code on every startup, so the rename
needed no migration: the stale row is overwritten, not accumulated
beside the new one.
"""

from __future__ import annotations

from managers.agent import AgentConfig
from managers.attachment import MODE_FULL, MODE_REFERENCE

GENERAL_AGENT_ID = "general"
FILLER_EXPLAINER_AGENT_ID = "filler-explainer"
RESEARCH_AGENT_ID = "research"

_GENERAL = AgentConfig(
    id=GENERAL_AGENT_ID,
    name="General",
    description=(
        "General chat, with everything switched off. Not a special case -- "
        "every message goes through an agent, and this is the agent with no "
        "extra scope, no tools, no multi-step loop."
    ),
    builtin=True,
    provider=None,
    model=None,
    effort=None,
    system_prompt=(
        "You are Point, a local-first terminal AI workspace. Answer precisely "
        "and concisely. If you are unsure, say so rather than guessing."
    ),
    tools=(),
    reads_session=True,
    reads_project=False,
    reads_global=False,
    writes_proposals=False,
    stm_window=20,
    max_steps=1,
    max_tool_calls=0,
    timeout_s=60,
    default_attach_mode=MODE_FULL,
)

_FILLER_EXPLAINER = AgentConfig(
    id=FILLER_EXPLAINER_AGENT_ID,
    name="Filler-Explainer",
    description=(
        "One agent, two modes, decided by the invocation site. Explainer: "
        "explains selection -> enclosing function -> block -> file, project "
        "scope on explicit request only, as a file list, never contents. "
        "Filler: writes code at one location, one file, one region -- never "
        "creates a file, never runs a command, never touches two files; "
        "refuses and says so rather than guessing at scope."
    ),
    builtin=True,
    provider=None,
    model=None,
    effort=None,
    system_prompt=(
        "You are Point's Filler-Explainer agent. In explainer mode, explain "
        "the given selection, then its enclosing function, block, or file, "
        "as needed for the question -- concisely, and without inventing "
        "behaviour you cannot see in the given code. In filler mode, write "
        "the rewritten region for exactly the one location given. Never "
        "create a file, never propose running a command, and never touch "
        "more than one file -- if the request needs more than that, refuse "
        "and say why instead of guessing."
    ),
    tools=(),
    reads_session=True,
    reads_project=True,
    reads_global=False,
    writes_proposals=False,
    stm_window=20,
    max_steps=1,
    max_tool_calls=0,
    timeout_s=60,
    default_attach_mode=MODE_FULL,
)

_RESEARCH = AgentConfig(
    id=RESEARCH_AGENT_ID,
    name="Research",
    description=(
        "The only structurally complex agent: memory-first web research, "
        "bounded by max_steps/max_tool_calls/timeout_s with the step count "
        "meant to stay visible on the task card. Always a background task -- "
        "it is the reason background tasks, result cards, and cancellation "
        "have to genuinely work."
    ),
    builtin=True,
    provider=None,
    model=None,
    effort=None,
    system_prompt=(
        "You are Point's Research agent. Search stored memory before the "
        "web; look at the age of what you find and decide whether it is "
        "worth re-researching rather than trusting it blindly. Work in "
        "bounded steps -- you have a maximum step count and a maximum "
        "number of tool calls, and running out of either means stopping and "
        "reporting what you have, not guessing past the boundary. Findings "
        "worth keeping are proposals, not writes -- you never save memory "
        "directly."
    ),
    tools=("read_file", "list_files", "web_search", "load_task_result"),
    reads_session=True,
    reads_project=True,
    reads_global=False,
    writes_proposals=True,
    stm_window=20,
    max_steps=8,
    max_tool_calls=20,
    timeout_s=180,
    default_attach_mode=MODE_REFERENCE,
)

# Order is the seeding order and the default `agents list` order for
# ties (AgentRepository.fetch_all() then sorts builtin-first, by name).
BUILTIN_AGENTS: tuple[AgentConfig, ...] = (_GENERAL, _FILLER_EXPLAINER, _RESEARCH)
