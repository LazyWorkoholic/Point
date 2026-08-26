# B9 — Agent Foundation

**Phase:** P1 · **Batchable:** no · **Depends on:** B8

## Purpose

One agent abstraction where every difference between agents is **configuration**,
not code. The old "Agent Memory Policies" episode collapses into this one — a
policy is a set of fields, not a class.

## Where the code goes

`backend\managers\agent.py` exists but is **empty**. Fill it, and create
`backend\agents\` for the executor and registry if that reads better — say which
you chose in the handoff. Do not scatter agent code across `managers\`.

## Build

### AgentConfig
Mirrors the `agents` table from B6 exactly:

```python
@dataclass(frozen=True)
class AgentConfig:
    id: str; name: str; description: str; builtin: bool
    provider: str | None; model: str | None; effort: str | None   # None = inherit
    system_prompt: str
    tools: tuple[str, ...]
    reads_session: bool; reads_project: bool; reads_global: bool
    writes_proposals: bool
    stm_window: int
    max_steps: int; max_tool_calls: int; timeout_s: int
    default_attach_mode: str          # full | reference | detached
```

**There is no `writes_global` field.** The permission ceiling is enforced by
absence, not by a check.

**B8 introduced `ContextPolicy`** (`managers\context.py`) — the narrow set of
fields the context builder actually reads. This `AgentConfig` must **adapt to**
it (`to_context_policy()` or equivalent), never replace it and never widen
`build()`'s input to the full agent row: the builder must not grow a dependency
on the agent system.

### AgentRegistry
- Seeds **built-ins from code** on startup (`builtin=True`, upserted by id).
- Loads user agents from the `agents` table (none exist in v1 — the table is the
  hook for the parked dynamic-agents feature).
- Built-ins are read-only: reject edits and deletes **at the repository layer**,
  not just in the UI.
- `get(id)`, `list()`, `resolve(name_or_alias)`.

### AgentExecutor
```
resolve config
  → resolve provider/model/effort  (task > session > agent > global)
  → build context (B8) with the agent's budget
  → create a Task (B1 TaskManager)
  → stream through the provider (B4) via the streaming manager (B5)
  → emit events
  → persist the assistant message
```

`inline` vs `background` is a property of the **invocation**, not the agent — the
executor takes it as a parameter.

### Middleware chain (no-op)
`request_middleware` and `response_middleware`, both empty lists. This is the
guardrails hook; wiring it now costs half an hour and avoids rewiring the
executor later.

### AgentContext
What a running agent may touch: config, session id, project id (or None), the
built context, the tool registry (B10, empty here), the emit function. **No
database handle, no provider client of its own.**

### Built-in seed (config only — behaviour is B11)
`general`, `filler-explainer`, `research`, with the field values from
`project_idea.md` §7.

Add `cli.py agents list|show`.

## Do NOT

- Implement tools (B10) or agent behaviour (B11).
- Build the create/clone UI or user agents.
- Give any agent a provider client, a DB handle, or a task queue.

## Done when

1. Two configs differing only in `stm_window` produce different contexts for the
   same input, visible in the B8 log.
2. Editing or deleting a `builtin=True` agent is rejected at the repository layer.
3. A task created through the executor carries `agent_id` on the `tasks` row.
4. Provider resolution honours the chain: a model set on the session overrides
   the agent's, and a per-task override beats both.
5. Middleware lists are actually called — add a temporary logging middleware, see
   it run, remove it.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

uv run python cli.py agents list
```
**Expect:** three built-ins, each marked builtin, with their tool lists and scope
flags visible.

```powershell
uv run python cli.py agents show research
```
**Expect:** `reads_project=True`, `writes_proposals=True`,
`default_attach_mode=reference`, non-zero `max_steps`.

```powershell
uv run python cli.py agents set-model general gpt-x
```
**Expect:** a refusal — built-ins are read-only.
**Fails if:** it succeeds, or fails only with a UI-level message while the row
actually changed.

```powershell
uv run python cli.py session new --agent general --model "llama3.2:3b"
uv run python cli.py send "hi"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select agent_id, model from tasks order by rowid desc limit 1;"
```
**Expect:** `general` and `llama3.2:3b` — the session's model, not the global
default.

```powershell
uv run python cli.py context report --last
```
**Expect:** a context shaped by `general`'s `stm_window`, not a fixed number.

## Handoff

State after this episode: there is one execution path for every message. Agents
are rows plus a frozen dataclass. Tools and behaviour are missing, so responses
are plain single-shot completions.

Record in `completed/`: whether you used `managers\agent.py` alone or created
`agents\`, and the exact built-in seed values.

Do not proceed until every check above passes.
