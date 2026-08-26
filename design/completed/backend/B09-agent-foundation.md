# Episode 9 — Agent Foundation ✅

**One execution path every message goes through: `AgentConfig` is a frozen
dataclass mirroring the `agents` table row for row, three built-ins are seeded
from code, and `AgentExecutor` supersedes the placeholder wiring B8 left in
`Runtime._handle_send_message` — resolve config → resolve provider/model/effort
→ build context (B8) → create a Task → stream → emit → persist, with an
empty-but-real middleware chain either side of it.**

> Verified by Aryan on 2026-08-26 against `tasks/backend/B09-agent-foundation.md`'s
> five "Done when" criteria, using the corrected manual verification script
> below (the original's last SQL query assumed a column that was never part of
> any schema — see "Bugs caught" #2). Self-tested first end to end, including
> the two Done-when criteria the PowerShell script doesn't reach directly
> (stm_window changing the built context, middleware actually firing), against
> throwaway scratch databases that never touched the live profile.

---

## Where the code went

`managers\agent.py` holds only the data model — `AgentConfig`, same role as
`Session` in `managers\session.py` and `Project` in `managers\project.py`.
Everything with business rules on top of it went into a new `agents\` package,
matching the `ProjectManager` / `ProjectRepository` split B7 already
established:

```
agents\                       NEW package
├── __init__.py
├── builtins.py                the three seed AgentConfigs, as code
├── registry.py                AgentRegistry: seed, get, list, resolve,
│                               resolve_for_session
└── executor.py                AgentExecutor, AgentContext, ProviderOverride,
                                resolve_provider_model_effort(), the
                                request/response middleware chain
```

## `managers\agent.py` (filled)

`AgentConfig` — frozen dataclass, field-for-field the `agents` table B6 put in
the ground. **Recorded exactly as it ended up, not as speced — B10's tool
grants and B11's three agents both key off these names:**

```python
@dataclass(frozen=True)
class AgentConfig:
    id: str
    name: str
    description: str
    builtin: bool

    provider: Optional[str]      # None = inherit
    model: Optional[str]         # None = inherit
    effort: Optional[str]        # None = inherit

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

    def to_context_policy(self) -> ContextPolicy: ...
```

No drift from the episode's spec — this is the literal field list and order,
verified against the file on disk while writing this doc. **No `writes_global`
field**, confirmed absent: the permission ceiling on what an agent can write is
enforced by the field not existing, not by a flag that says no.

`to_context_policy()` adapts *to* B8's `ContextPolicy` (`system_prompt`,
`stm_window`, `default_attach_mode`, `effort`) — the four fields the context
builder actually reads. `managers\context.py` was not touched; the dependency
runs one way only, exactly as B8's handoff required.

## `database\agent_repository.py` (NEW)

Raw SQL, same shape as `ProjectRepository`/`SessionRepository`. `tools` is
stored as `tools_json` (a JSON array of strings) and rehydrated into a tuple on
read.

**The one rule that has to live at this exact layer:** `update_fields()` and
`delete()` both fetch the existing row first and raise
`BuiltinAgentImmutableError` if `builtin` is true, *before* any UPDATE/DELETE
statement runs. `upsert()` is a separate, unchecked entry point — it is the
seeding operation itself, called only by `AgentRegistry.seed_builtins()`, and
checking the builtin flag there would make seeding a built-in impossible.
"Rejected at the repository layer, not just in the UI" only means something if
there is no other way to reach the table; there is exactly one write path for
each operation, not two.

## `agents\builtins.py` (NEW) — the exact seed values

Three built-ins, matching `project_idea.md` §7's structural table:

| | `general` | `filler-explainer` | `research` |
|---|---|---|---|
| tools | `()` | `()` | `("web", "fs", "load_task_result")` |
| reads_session | True | True | True |
| reads_project | False | True | True |
| reads_global | False | False | False |
| writes_proposals | False | False | True |
| stm_window | 20 | 20 | 20 |
| max_steps | 1 | 1 | 8 |
| max_tool_calls | 0 | 0 | 20 |
| timeout_s | 60 | 60 | 180 |
| default_attach_mode | full | full | reference |
| provider/model/effort | None/None/None | None/None/None | None/None/None |

All three inherit provider/model/effort (nothing pins a built-in to a vendor —
that stays a session/global decision). System prompts are agent-specific
(Filler-Explainer's spells out its own refusal rule: never create a file, never
propose a command, never touch two files). Full text is in the file; not
duplicated here because it is prose, not a contract another episode reads
field-by-field.

## `agents\registry.py` (NEW)

- `seed_builtins()` — upserts all three by id, called from `Runtime.create()`
  (async, so it cannot happen in `Runtime.__init__`). Every startup re-applies
  code truth to the three rows; a stale field from an older build is
  overwritten, not accumulated.
- `get(id)` / `list()` / `resolve(name_or_id)` — id match first, then a
  case-insensitive name match (the only "alias" a user agent could have in v1
  — there is no separate alias table).
- `resolve_for_session(session)` — reads `session.settings["agent_id"]`,
  falling back to `general` when unset or when the id no longer resolves.
  **This is the session→agent binding, and it lives in the existing
  `settings` JSON column, not a new one.** Same pattern B8 used for
  `stm_window`/`system_prompt` before an agent system existed. No migration.

## `agents\executor.py` (NEW) — the episode

`AgentExecutor.run(session, text, agent, make_sink, inline, override=None)`
does what `Runtime._stream_chat_body`/`_demo_echo_body`/`_handle_send_message`
did before this episode, parameterized by an already-resolved `AgentConfig`
instead of hardcoded session settings:

```
resolve_provider_model_effort()   task override > session > agent > global config
context.build()                   agent.to_context_policy(), B8's builder, unchanged
_run_request_middleware()         no-op in v1
persist the user message
create the Task                   TaskManager.create_task(..., agent_id=agent.id)
```

The task body (`_stream_body` / `_echo_body`) is a straight carry-over of the
old `_stream_chat_body`/`_demo_echo_body` — same cancellation behaviour
(`CancelledError` propagates through the provider's `async with`, closing the
connection genuinely, not just the UI), same echo dev-path. `effort` now
actually reaches `GenerationRequest.effort` as an `Effort` enum, which nothing
did before this episode (B6 defined the enum and the field; nothing set it).

**`resolve_provider_model_effort()`** is the chain, as a standalone function
(not a method, so it is testable without a `Runtime`):

```
provider = override.provider or session.provider or agent.provider or config_value("provider.default")
model    = override.model    or session.model    or agent.model    or config_value("model.default")
effort   = override.effort   or session.effort   or agent.effort   or config_value("effort.default")
```

Raises `InvalidEventError` only when nothing in the whole chain names a
provider/model. **This replaces the old unconditional refusal** in
`_handle_send_message` that rejected a NULL-provider session outright — a
session can now have NULL provider/model and still resolve through its
agent's default or global config; the refusal only fires when every rung is
empty.

`ProviderOverride` is the top rung. Nothing on the wire sets one in v1 —
`SendMessage`'s payload carries no override fields — but the type exists so a
future call site (a per-message model picker, B11's Research choosing its own
model) is a new caller, not a rewiring of this one.

**`AgentContext`** is defined exactly as speced (config, session_id,
project_id, the built context, an empty `tools` dict for B10, an optional
`emit` callable) but **nothing constructs one yet** — there is no tool loop or
multi-step behaviour to hand it to. It is the shape B10/B11 will fill, the same
way B8's `MemoryService` Protocol defined four methods before anything called
them.

**Middleware chain** — `request_middleware`/`response_middleware`, both empty
lists on `AgentExecutor`. `apply_response_middleware()` is called from
`Runtime`'s sink (see below), not from inside `AgentExecutor.run()` — the sink
is where a finalized `Message` exists, and moving persistence into this file
would give it the database handle `AgentContext`'s docstring is explicit it
must never carry. Verified by hand (see Manual Test Log): appended a logging
middleware to both lists, sent a message, watched both fire with the right
session/agent/text, removed them again. Ships empty.

## `runtime\task.py` / `runtime\task_manager.py` (changed)

`Task` gained `self.agent_id: Optional[str]`, set at construction and never
written elsewhere. `TaskManager.create_task()` gained an `agent_id` parameter
threaded straight through to `Task()`. `database\task_repository.py` already
had an `agent_id` parameter on `record()` since B6 — it was simply never
supplied by anything until this episode's `Runtime._record_task()` started
passing `task.agent_id`.

## `runtime\runtime.py` (changed) — the placeholder B8 flagged is gone

- `_context_policy()`, `_demo_echo_body()`, `_stream_chat_body()` — **deleted**.
  B8's handoff named this exact call site as what B9 would supersede.
- `_handle_send_message()` — now: fetch the session, resolve its agent via
  `self.agents.resolve_for_session(session)`, call
  `self.agent_executor.run(...)`, emit `TaskStarted` from the returned
  `AgentRun`. No more inline context-building, no more unconditional
  NULL-provider refusal (see `resolve_provider_model_effort()` above).
- The sink (`_make_stream_sink`) — its three finalize branches and the echo
  terminal-persist branch now route the finalized `Message` through
  `self.agent_executor.apply_response_middleware(msg)` before
  `self.sessions.append_message(msg)`. This is the one place every persisted
  assistant reply passes through, streamed or echoed alike.
- `_record_task()` — now passes `agent_id=task.agent_id` to
  `task_repo.record()`.
- `Runtime.__init__` — constructs `self.agents = AgentRegistry(db_conn)` and
  `self.agent_executor = AgentExecutor(...)`, wired with `self.sessions`,
  `self.memory`, `self.tasks`, `self.providers`, `self.streams`,
  `self.stream_adapter`, `self._resolve_provider_name`.
- `Runtime.create()` — now `await`s `runtime.agents.seed_builtins()` after
  construction, before returning. This is the only place seeding happens;
  `Runtime.__init__` stays synchronous.

## `cli.py` (changed)

- `agents list` / `agents show <id>` / `agents set-model <id> <model>` — direct
  calls into `runtime.agents`, no dispatcher event (same reasoning as B8's
  `context report`: read-mostly, no task, no stream). `set-model` is how the
  read-only-builtin rule gets exercised end to end; every agent is builtin in
  v1, so it always refuses.
- `session new --agent <id>` — validates the id against the registry (a
  friendlier error than waiting for `resolve_for_session`'s silent fallback),
  then passes `settings={"agent_id": ...}` through `CreateSession`'s existing
  `settings` payload field. No event schema change.
- `context report` — now resolves the session first, then its agent via
  `resolve_for_session()`, and passes `agent.to_context_policy()` into
  `context.report()` instead of relying on the hardcoded `DEFAULT_POLICY`.
  Without this fix the command would have kept reporting a fixed stm_window
  forever, agent or no agent — caught during self-testing, see below.

---

## Manual Test Log (Episode 9)

Self-tested first against throwaway scratch databases (never the live
profile), then re-run by Aryan against a renamed-aside profile and confirmed
passing.

1. **`agents list`** — three built-ins, each marked builtin, correct tool
   lists (`research` shows `web,fs,load_task_result`, the other two show `-`)
   and scope flags. ✅
2. **`agents show research`** — `reads_project=True`, `writes_proposals=True`,
   `default_attach_mode=reference`, `max_steps=8` (non-zero). ✅
3. **`agents set-model general gpt-x`** — refused at the repository layer:
   `error: Agent 'general' is builtin and read-only -- edits are rejected at
   the repository layer`, exit 1, row unchanged. ✅
4. **Task carries agent_id.** `session new --agent general --model
   llama3.2:3b` then `send "hi"` left a `tasks` row with `agent_id='general',
   kind='chat', status='completed'`. ✅
5. **Provider resolution chain**, tested directly against
   `resolve_provider_model_effort()`: agent default alone →
   `('ollama','agent-model',None)`; session set → session wins over agent;
   task override set → task wins over both. ✅
6. **stm_window changes the built context** (Done-when #1, not reachable
   through the CLI's fixed `context report` before the fix in #7 below). Two
   `AgentConfig`s differing only in `stm_window` (1 vs 5) against six messages
   of identical history: 2 messages/36 tokens vs 6 messages/52 tokens. ✅
7. **`context report` reflects the session's agent**, after the cli.py fix —
   confirmed the token/message counts move with `stm_window` instead of
   staying fixed at `DEFAULT_STM_WINDOW`. ✅
8. **Middleware lists actually run** (Done-when #5). Appended a logging
   `request_middleware` and `response_middleware` to a live `AgentExecutor`,
   sent a message, both fired (`request` saw `session_id`/`agent_id`/`text`;
   `response` saw the finalized `Message`), then removed — confirmed both
   lists ship empty in the code that was committed. ✅

---

## Bugs caught during manual testing

**1. `AssistantMessage` import dropped during the `_handle_send_message`
rewrite — broke every echo send.** The refactor moved `UserMessage`'s only use
into `agents\executor.py` and, in tidying the top of `runtime.py`, the
`AssistantMessage` import went with it — except the sink's echo
terminal-persist branch (`AssistantMessage(session_id, full_reply, ...)`) still
lives in `runtime.py`, not in the executor. Every `send` against the `echo`
provider (the only provider that works with no config at all, and therefore
the CLI's own default) failed with `name 'AssistantMessage' is not defined` →
`TaskFailed`. Caught immediately on first end-to-end self-test.
**Fix:** re-added `from managers.message import AssistantMessage` to
`runtime.py`; `UserMessage` stayed removed from there since only
`agents\executor.py` constructs one now.

**2. The episode's own verification script assumed a `tasks.model` column
that was never part of any schema.** B6's `database\migrations.py` v2 gives
`tasks` only `agent_id, project_id, connection_id, kind, status, error_code,
git_checkpoint_sha` plus timestamps — no `model`, no `provider`. The literal
`select agent_id, model from tasks ...` in the task doc fails with `no such
column: model`; `agent_id` alone is correct and present. **This is a doc/
schema mismatch, not a code bug** — flagged to Aryan rather than silently
patched with a new migration, per `CLAUDE.md`'s rule on schema changes.
**Resolved by Aryan's call:** the verification query was corrected to join
through `sessions` instead —
`select t.agent_id, s.model from tasks t join sessions s on s.id = t.session_id
order by t.rowid desc limit 1;` — which proves the same thing (`agent_id` plus
the session's actually-resolved model, not a global default) against the real
schema. **No migration was added.** Model is resolved at request time by the
chain in `resolve_provider_model_effort()`, not stored per task row; if B14
(usage/billing) ever needs a durable per-task model record for cost
attribution, that is B14's own schema decision, not a retrofit onto this
episode.

**3. `cli.py context report` ignored the agent entirely until fixed inline.**
Caught during self-testing, before Aryan's run: the command called
`context.report(session_id, runtime.memory)` with no `policy` argument, so it
silently used `DEFAULT_POLICY` (`stm_window=20` always) regardless of which
agent a session named. The manual verification's last check ("a context shaped
by general's stm_window, not a fixed number") would have passed by
coincidence — `general`'s `stm_window` is also 20 — and stayed broken for any
agent whose `stm_window` differs. **Fixed** by resolving the session and its
agent first, then passing `agent.to_context_policy()` into `context.report()`.

---

## What the next episode inherits

**B10 (Tool System)** inherits `AgentConfig.tools: tuple[str, ...]` already
populated per built-in (`research` names `"web"`, `"fs"`,
`"load_task_result"` — string identifiers, not implementations) and
`max_tool_calls`/`timeout_s` already present as the bounds a tool loop must
honour. `AgentContext.tools: dict[str, Any]` is the empty placeholder B10
fills; nothing constructs an `AgentContext` yet, so B10 is expected to be the
first real caller. No tool actually runs anything yet — the field is
declarative only.

**B11 (Agent behaviour)** inherits three structurally distinct
`AgentConfig`s and one execution path that treats them identically: every
agent today produces a single provider round-trip through `AgentExecutor`'s
`body_factory`, exactly the same shape regardless of `reads_project`,
`writes_proposals`, or `max_steps`. Filler-Explainer's two-mode split
(explainer vs filler, decided by invocation site) and Research's bounded
step loop are both unbuilt — the config differences exist and are visible in
`agents show`, but nothing branches on them. `inline: bool` is accepted by
`AgentExecutor.run()` and threaded nowhere — v1 has exactly one call site
(`Runtime._handle_send_message`) and it always passes `inline=True`. B11's
Research agent (background always, per `project_idea.md` §7) will be the
first caller to pass `False`, and should not assume anything downstream
already branches on it — nothing does yet.

**Every agent inherits provider/model/effort = None** (inherit). The global
config fallback in `resolve_provider_model_effort()` reads
`appconfig.config_value("provider.default"/"model.default"/"effort.default")`
— **nothing sets these config keys in v1**, so in practice only the
session/agent rungs of the chain are ever exercised by a real session today.
Whoever builds a settings UI or a first-run default should know these three
dotted keys are already the read side of that contract.

**Session→agent binding lives in `session.settings["agent_id"]`, not a
column.** This is the same category of stand-in B8's `_context_policy`
was (system_prompt/stm_window in `settings`) before an agent system existed
— except this one has no B9-successor obligation to remove it, because
`resolve_for_session()` *is* the intended permanent mechanism. If a future
episode gives sessions a first-class `agent_id` column, `settings["agent_id"]`
is what it replaces, and `AgentRegistry.resolve_for_session()` is the one
place that read has to change.

**`tasks` still has no `model`/`provider` column** (see "Bugs caught" #2).
`agent_id` is the only new durable column this episode added meaning to —
it was already in the schema since B6, unpopulated until now.

**The middleware chain ships empty and is proven to fire.** Any episode
wanting request/response guardrails (rate limiting, content filtering, a
future audit log) appends to `AgentExecutor.request_middleware` /
`.response_middleware` — no rewiring needed, per the original episode's
stated intent ("wiring it now costs half an hour and avoids rewiring the
executor later").

**No new dependencies.**
