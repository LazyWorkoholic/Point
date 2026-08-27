# Episode 10 — Tool System ✅

**Agents can now act within bounds: four granted tools behind a registry that
refuses anything a config did not name, an executor that turns an expected
refusal into a `ToolResult` the model reads and a broken tool into a coded
`task.failed`, a loop bounded by `max_steps`/`max_tool_calls`/`timeout_s` that
keeps its partial output when it stops, one `tool_calls` row per executed call,
and 429s that retry instead of failing.**

> Verified by Aryan on 2026-08-27 against `tasks/backend/B10-tool-system.md`'s
> seven "Done when" criteria, all 12 steps confirmed independently. Self-tested
> first end to end against throwaway scratch profiles under `%TEMP%` (APPDATA,
> LOCALAPPDATA and POINT_DB all redirected in **one** script — never the live
> profile, never a per-call env var).
>
> **`@research` was dropped from the verification, not implemented.** The
> episode's manual steps all read `send "@research test"`, but nothing in the
> backend parses an `@agent` prefix and B10 does not build one — that is B11/F8.
> The gate uses `session new --agent research --project <id>` plus
> `send --session <id>` instead, which routes through
> `AgentRegistry.resolve_for_session()`, the mechanism that actually exists.

---

## Where the code went

A new `tools\` package, mirroring the `agents\` split B9 established. The
episode offered "fill `managers\tool.py`, or create `backend\tools\` — say which
in the handoff": **`tools\`, and `managers\tool.py` is deleted, not filled.**

```
tools\                        NEW package
├── __init__.py
├── models.py                 Tool, ToolResult, ToolCallRecord — data only
├── schema.py                 the JSON Schema subset arguments are checked against
├── registry.py               ToolRegistry: register / get / list_for /
│                              resolve_for / validate_grants
├── executor.py               ToolExecutor: validate → run → time out → emit → record
├── faults.py                 POINT_MOCK-only fault injection (raise / hang)
└── builtins\
    ├── __init__.py           BUILTIN_TOOLS
    ├── filesystem.py         read_file, list_files
    ├── task_results.py       load_task_result
    └── web.py                web_search
```

The reasoning is B9's, applied again: a registry, an executor, a schema checker
and four tools are not one manager. `managers\agent.py` survived B9 holding only
the `AgentConfig` data model; there is no `managers\tool.py` counterpart because
`Tool` and `ToolResult` are data the tool package itself owns, with no other
consumer. **Do not recreate `managers\tool.py`** — same standing instruction as
`managers\provider.py`.

---

## The exact shapes B11 branches on

### `Tool` — `tools\models.py`

```python
@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    schema: dict[str, Any]      # JSON Schema for parameters
    timeout_s: int
    run: ToolRun                # async (arguments: dict, ctx: AgentContext) -> ToolResult

    def to_schema(self) -> dict[str, Any]:
        return {"name": ..., "description": ..., "parameters": self.schema}
```

`to_schema()` is the **common tool schema** — the only shape that crosses into
`providers\`. A `Tool` never does: providers know nothing about timeouts or
callables.

### `ToolResult` — `tools\models.py`

```python
@dataclass
class ToolResult:
    ok: bool
    content: str = ""
    error: Optional[str] = None
    tokens_estimate: int = 0
    meta: dict[str, Any] = field(default_factory=dict)

    @staticmethod
    def success(content: str, **meta) -> ToolResult    # fills tokens_estimate
    @staticmethod
    def failure(error: str, **meta) -> ToolResult      # ok=False, content=""
    def to_dict(self) -> dict[str, Any]                # all five fields
```

`tokens_estimate` uses `estimate_tokens()` — the same 4-characters-per-token
approximation B8's context builder uses, deliberately not a real tokenizer. B8
already established that one approximation everything shares beats five exact
numbers that disagree.

`meta` carries per-tool detail the model does not need in its content but a
later reader does: `read_file` puts `path`/`mtime`/`size`/`outside_project`
there, `list_files` puts `count`/`prefix`, `web_search` puts `count`/`query`,
and the executor adds `timed_out=True` or `raised=True` on the two broken paths.

### The four v1 tools

| name | arguments | timeout_s | goes through |
|---|---|---|---|
| `read_file` | `path` (required) | 15 | `ProjectManager.read_file()` → B7's `resolve_path()` |
| `list_files` | `prefix` (default `""`), `limit` (default 200, 1–2000) | 30 | `ProjectManager.list_files()` → B7's index |
| `web_search` | `query` (required), `limit` (default 5, 1–20) | 30 | DuckDuckGo HTML endpoint |
| `load_task_result` | `task_id` (required) | 15 | `SessionMemoryService.task_messages()` |

**`allow_outside_project` is deliberately not exposed as an argument.** A model
must not be able to opt itself out of project scope. The escape hatch exists for
a user action (an explicit `@file` outside the root), not for an agent.

**`load_task_result` is scoped to the current session.** A task id is not a
capability: letting any agent load any task's output by id would let an agent
holding only `reads_session` read across every session in the workspace, which
is exactly the ceiling the scope flags exist to enforce. Cross-session loading,
if ever wanted, is a memory-scope decision (B13), not a tool argument.

### `ToolRegistry` — `tools\registry.py`

- `register(tool)` / `get(name)` / `names()`
- `list_for(agent_config)` — only what the config grants, in grant order.
- `resolve_for(agent_config, name)` — **the one lookup a call goes through.**
  Raises `ToolNotGrantedError` before `get()`, so an ungranted tool cannot run
  whatever the model asks for.
- `validate_grants(configs)` — **load time.** Raises `ToolNotFoundError` naming
  the tool and listing what is registered.
- `build_default_registry()` — the v1 set.

`AgentRegistry.seed_builtins()` validates **before the first upsert**, not after
the last: a built-in naming a tool that does not exist must not reach the table
at all, because the next startup would find a row and believe it.
`AgentRegistry.validate_all()` then covers every row including user agents, and
`Runtime.create()` calls both.

### The `tool_calls` table — migration v3

`SCHEMA_VERSION` is now **3**. Verified on disk:

```sql
CREATE TABLE IF NOT EXISTS tool_calls (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id          TEXT NOT NULL,
    session_id       TEXT NOT NULL,
    tool             TEXT NOT NULL,
    args_json        TEXT,
    ok               INTEGER NOT NULL,
    error            TEXT,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    tokens_estimate  INTEGER NOT NULL DEFAULT 0,
    created_at       REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_task    ON tool_calls(task_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool    ON tool_calls(tool);
```

Three decisions worth not re-litigating:

- **`args_json` is the call as it RAN**, after `schema.apply_defaults()` — not
  as the model phrased it. Recording the phrasing would make a later reader
  guess at which defaults were in force that day.
- **No foreign key to `tasks`.** A tool call is evidence, and evidence should
  survive its subject. `tasks` rows are already swept; `ON DELETE CASCADE` would
  quietly take the ledger with them.
- **Insert-only.** `ToolCallRepository` has `record`, `fetch_for_task`,
  `list_recent` and no update or delete path. A record of what an agent did is
  not something anything at runtime gets to revise.

---

## Every error code on the wire

`runtime\errors.py` gained a `ToolError` family. Each subclass sets a `code`
class attribute; `Task._run()` copies it with `getattr(exc, "code", None)` onto
`TaskResult.code`; it reaches Go in the `TaskFailed` payload **and**
`tasks.error_code`. Task itself still knows nothing about tools — it copies an
attribute if it is there.

| Class | `code` | Raised by |
|---|---|---|
| `BoundsExceededError` | `bounds_exceeded` | the loop, on max_steps / max_tool_calls / timeout_s |
| `ToolExecutionError` | `tool_error` | `ToolExecutor`, when a tool body raises |
| `ToolTimeoutError` | `tool_timeout` | `ToolExecutor`, on the per-tool timeout |
| `ToolNotGrantedError` | `tool_not_granted` | `ToolRegistry.resolve_for()` |
| `ToolNotFoundError` | `tool_not_found` | `ToolRegistry.validate_grants()` — **startup only** |
| `ToolsUnsupportedError` | `tools_unsupported` | `AgentExecutor.run()` — **before the task exists** |
| `ToolError` (base) | `tool_error` | never raised directly |

**Only the first four ever appear as a `TaskFailed`.** The last two are the
important precision point for B11:

- `ToolNotFoundError` fires during `Runtime.create()`, before a single event is
  accepted. It is a **startup crash**, not an event.
- `ToolsUnsupportedError` is raised inside `agent_executor.run()`, **before**
  `create_task()`. It is a `RuntimeException`, so `Runtime.receive()` catches it
  and emits an **`Error`** envelope — whose payload is `{"error": "..."}` with
  **no `code` field**. A caller branching on `payload["code"]` will read `None`
  there. That asymmetry is a real gap and is B17's to close, not something to
  work around with a string match.

### Verified `TaskFailed` payloads

Captured from live runs, not written from memory. The envelope is B6's v1 in
every case; only `payload` differs.

**`bounds_exceeded`** — `max_tool_calls` reached (`--max-tool-calls 2` against a
task wanting three):

```json
{
  "v": 1, "id": "06929f29-…", "type": "TaskFailed",
  "corr_id": "req-three-tool-calls",
  "session_id": "b363dd83-…", "task_id": "362e9627-…",
  "connection_id": null, "trace_id": null, "seq": null,
  "ts": 1787821819.564179,
  "payload": {
    "error": "stopped after 2 tool call(s): this agent's limit is 2",
    "code": "bounds_exceeded"
  }
}
```

The other two `bounds_exceeded` messages, same code:

```
max_steps   "stopped after 8 step(s) without a final answer"
timeout_s   "stopped after 180s: this agent's time limit"
```

`BoundsExceededError` also carries `.bound` (`"max_steps"` / `"max_tool_calls"`
/ `"timeout_s"`) and `.limit` as attributes. **Neither is on the wire yet** —
they exist for B17's `PointError.detail`. Do not parse them out of `error`.

**`tool_error`** — a tool body raised:

```json
"payload": {
  "error": "web_search failed: InjectedToolError: injected failure inside tool 'web_search'",
  "code": "tool_error"
}
```

**`tool_timeout`** — a tool ran past its own `timeout_s`:

```json
"payload": {
  "error": "read_file timed out after 15s and was stopped",
  "code": "tool_timeout"
}
```

**`tool_not_granted`** — the model asked for a tool its config does not name:

```json
"payload": {
  "error": "Agent 'research' is not granted the tool 'terminal' (granted: read_file, list_files, web_search, load_task_result)",
  "code": "tool_not_granted"
}
```

**A successful turn's `TaskDone` payload is `{}`** — unchanged from B6. The code
key exists only on `TaskFailed`.

### `bounds_exceeded` keeps partial output

The accumulator is fed every delta as it arrives, so when the loop raises, the
sink's `finalize_interrupted("failed", …)` path still builds and persists an
assistant message carrying everything produced up to the boundary, with
`metadata = {"interrupted": True, "reason": "failed", "error": …}`. Confirmed in
verification: after the three-call run stopped at two, the last message still
read `First look. Second look. Third look — this is the one a max_tool_calls of
2 refuses.` **Stopping at a boundary loses the remaining work, never the work
already done.**

---

## The two new envelope types

`OutgoingEventType` gained `TOOL_STARTED = "ToolStarted"` and
`TOOL_COMPLETED = "ToolCompleted"`. `StreamEventType.TOOL_STARTED` /
`TOOL_COMPLETED` have been reserved since B5 and finally have producers.

**`task.progress` in the episode's prose is the existing `PROGRESS_UPDATED` /
`ProgressUpdated`** — no third progress vocabulary was invented. Both the step
counter and the rate-limit notice ride it, distinguished by their `data` keys.

Verified payloads (the envelope's `payload` is the `StreamEvent.to_dict()`, and
the tool detail is under `payload.data`):

```json
"type": "ProgressUpdated",
"payload": { "…": "…", "data": {
  "step": 1, "max_steps": 8,
  "tool_calls_used": 0, "max_tool_calls": 20,
  "message": "step 1/8"
}}
```

```json
"type": "ProgressUpdated",
"payload": { "…": "…", "data": {
  "step": 1, "max_steps": 8,
  "rate_limited": true, "attempt": 1, "retry_in_s": 2.0,
  "message": "rate limited -- retrying in 2s"
}}
```

```json
"type": "ToolStarted",
"payload": { "…": "…", "data": {
  "tool": "list_files",
  "arguments": {"prefix": "backend/tools/", "limit": 20},
  "timeout_s": 30
}}
```

```json
"type": "ToolCompleted",
"payload": { "…": "…", "data": {
  "tool": "list_files", "ok": true, "error": null,
  "duration_ms": 16, "tokens_estimate": 73
}}
```

`ToolStarted`/`ToolCompleted` are **always paired**, including on failure —
`ToolExecutor` emits and records before it raises, so a `tool_error` turn shows
`ToolCompleted` with `ok: false` and then `TaskFailed`.

---

## `agents\executor.py` — the loop replaced the round-trip

`_stream_body` is gone; `_loop_body` replaces it. **There is still exactly one
task body.** An agent with no tools and `max_steps=1` runs the loop once, sees no
tool calls, and returns — the same shape `general` had before, arrived at by
configuration rather than by a branch on which agent it is. This file still has
no `if agent.id == …`.

```
model → tool_calls? → execute (respecting max_tool_calls)
      → append results → model → … → final answer
```

`BoundsOverride` and `resolve_bounds()` are the per-send ceiling:

```python
@dataclass(frozen=True)
class BoundsOverride:
    max_steps: Optional[int] = None
    max_tool_calls: Optional[int] = None
    timeout_s: Optional[int] = None
```

Deliberately **not** an extension of `ProviderOverride` and **not** a mutation of
`AgentConfig`: the config is frozen and every v1 agent is builtin, which the
repository layer refuses to edit (B9). Keeping bounds separate is also what stops
a test flag from permanently loosening an agent's ceiling. `resolve_bounds()`
uses `_first_set()`, not `or` — **`0` is a meaningful bound** (`general` grants
zero tool calls) and `or` would silently replace it with the agent's value.

`AgentContext` is now actually constructed — B9 defined it and left it
unbuilt. It gained `services: ToolServices`:

```python
@dataclass(frozen=True)
class ToolServices:
    projects: ProjectManager
    memory: SessionMemoryService
    project: Optional[Project]
```

Managers only, never a connection and never a provider client — the rule
`AgentContext`'s B9 docstring set. `project` is resolved **once per run**, not
per call: it is the root every path in that run is guarded against, and
re-resolving mid-loop would let the third tool call be scoped differently from
the first. `tools` is now `{name: Tool}` for the granted set.

`SendMessage` accepts an optional `bounds` object (`max_steps`,
`max_tool_calls`, `timeout_s`); `_bounds_override()` in `runtime.py` converts
it. Absent or empty means the agent's own config decides — an override that
overrides nothing is not an override.

---

## Provider tool-calling — the B4 additive work

`ProviderChunkType.TOOL_CALL` and a frozen `ToolCall(id, name, arguments)` were
added to `providers\models.py`; `ProviderStreamChunk` gained `tool_call`.
**`arguments` is always already-parsed JSON, never a raw string** — every
adapter parses in its own layer, so nothing downstream has to know which vendors
stream arguments as fragments.

`MessageTranslator` gained a non-abstract `tools_to_provider_format(tools)`
returning `None` by default. Not abstract on purpose: a translator that has not
been taught a vendor's tool shape returns `None`, which reads downstream as
"this provider takes no tools" — the same answer `BaseProvider.supports_tools =
False` gives from the other side. Making it abstract would force every existing
translator to grow a stub that lies.

| Provider | schema shape | tool-call normalization | `supports_tools` |
|---|---|---|---|
| OpenAI | `[{type: function, function: {name, description, parameters}}]` | `OpenAIToolCallAccumulator` reassembles fragmented deltas by `index`, drained after the stream closes | True |
| OpenRouter | same (shares the translator and the accumulator) | same | True |
| Anthropic | `[{name, description, input_schema}]` | read off `get_final_message()`'s `tool_use` blocks — the SDK already did the accumulation, doing it twice is two chances to get it wrong | True |
| Ollama | OpenAI's shape (duplicated, not imported — the day Ollama diverges is the day a shared helper becomes a bug in two providers) | whole calls on one message; `arguments` accepted as dict or JSON string; id synthesized | True |
| Gemini | `[{function_declarations: [...]}]` — one tool object, not one per tool | `function_call` parts off the candidate; args are a proto Map; id synthesized | True |
| Mock | passthrough | scripted `tool_call` chunks | True |

**`BaseProvider.supports_tools` is new and distinct from
`ModelCapabilities.supports_tools`.** The model-level flag is per model; the
provider-level one answers "can this adapter translate a schema and normalize a
call back at all?", and it is what an agent requiring tools is checked against at
**invocation**, before a task exists. It defaults to `False`, so a new adapter is
tool-less until someone writes the translation rather than silently dropping the
tools mid-stream. Nothing in v1 sets it to `False` — every shipped adapter can
take tools — so `tools_unsupported` is reachable but not exercised by a shipped
provider.

The OpenAI accumulator drains **after the stream closes, not on
`finish_reason`**: usage arrives in a chunk *after* the one carrying
`finish_reason`, and emitting early would cut the usage chunk off from the turn
it belongs to.

---

## 429 handling — `providers\retry.py`

The exact parameters, as B10's handoff asks:

```
MAX_ATTEMPTS  = 5          (1 initial + 4 retries)
BASE_DELAY_S  = 1.0
FACTOR        = 2.0        → ~1s, ~2s, ~4s, ~8s
JITTER        = 0.25       ±25%, applied multiplicatively
MAX_DELAY_S   = 60.0       caps both the formula AND Retry-After
```

Measured in verification: `[1.17, 2.33, 3.17, 8.91]`; `Retry-After: 12` → `12.0`;
`Retry-After: 86400` → `60.0`.

Two rules shape the module:

- **Only a stream that has produced nothing yet may be retried.** Once a token
  has reached the accumulator, restarting would replay text the user already
  saw. A 429 mid-stream is a genuine failure and is re-raised. Providers reject
  at connect time in practice, so this costs nothing real and removes the
  duplication bug entirely.
- **`Retry-After` wins over the computed backoff, always** (still capped). The
  server knows when it will serve you; the formula is only for when it does not
  say.

`stream_with_retry()` takes a zero-arg **factory**, not an iterator — a retry
needs a fresh stream, and an exhausted async generator cannot be restarted.
`CancelledError` is never caught: cancelling a task mid-backoff must stop it
immediately, not after the sleep.

`RateLimitError` gained a `retry_after` attribute, populated by every adapter
that can find one (`response.headers["retry-after"]` for openai/anthropic, the
RPC retry deadline for google-api-core). It rides as an attribute because
`providers.retry` does arithmetic with it, and re-parsing a number out of a
sentence is how a backoff ends up coupled to an error string's wording.

---

## `web_search` — the implementation chosen, and its cost

**DuckDuckGo's keyless HTML endpoint** (`https://html.duckduckgo.com/html/`),
parsed with stdlib `html.parser`, fetched with `httpx` (already a dependency
since B4's Ollama adapter). Recorded because the episode asks for the choice and
its cost:

- **No key.** Point is local-first; a v1 tool that needs an account before it
  works is a tool most users never see working. Brave Search and Tavily are both
  better search, and both start with "get an API key".
- **No new dependency.** B10's rule is no new dependency without a reason in the
  handoff; this needed none. **The episode added zero dependencies.**
- **Cost: zero per query, and no rate-limit contract.** That is the real trade.
  DuckDuckGo publishes no quota for this endpoint, does not promise the markup
  will not change, and will throttle a burst.

Every one of those failure modes is a `ToolResult(ok=False)` the model can read,
never an exception — so the worst case is an agent told search is unavailable
that says so, which is the correct behaviour for a local-first tool anyway.
**Observed during self-test:** one probe returned zero results and the next
returned three for the identical query. That is the documented flakiness, and it
degraded to `count=0` rather than raising.

`SearchBackend` is the seam — the HTTP call and the parsing sit behind one
callable, so a keyed backend later is a new function plus a config key, not a
rewrite of the tool. A keyed backend resolves through B6's credential chain
(`appconfig.resolve_api_key`) like every other key; there is no second key path.

`_clean_url()` unwraps DuckDuckGo's `l/?uddg=` redirector — a model handed the
redirector cannot tell one domain from another, which defeats the point of
returning urls.

---

## Testing surface added

**`faults.py` — fault injection, not mock tools.** Done-when #2 and #3 need a
tool that raises and a tool that hangs. Neither is something a correct tool does:
`read_file` turns a denied path into a `ToolResult`, `web_search` turns a dead
network into one. Something has to misbehave on purpose.

A mock *tool* was rejected: it would have to be granted to an agent to be
callable, and B10's rule is that tools are granted, never invented. Granting
`mock_hangs` to `research` under an env var would make the grant list — the thing
Done-when #5 checks — lie. Injecting instead means the scripted call is a **real
call to a real granted tool**, which is what the loop, the bounds, the events and
the `tool_calls` row all see. The hook is a `__mock_fault` argument stripped
before the tool body runs, honoured only when `POINT_MOCK=1`, and inert
otherwise — so a fixture leaking into a real run cannot change behaviour.

**MockProvider gained a `turns` fixture format**, three chunk types
(`tool_call`, `rate_limit`, `hang`), and `supports_tools = True`. Each
`stream()` call consumes the next turn, so one fixture scripts a whole loop. The
last turn repeats once turns run out rather than raising: a loop that runs one
step further than the script expected should fail its **bound**, not die on a
fixture. A `rate_limit` chunk fires once per turn and is skipped on replay —
otherwise "retries and eventually succeeds" would be unscriptable.

**Seven scripts** in `tests\fixtures\`, replayed by `send --script <name>`
(a name→fixture mapping over the `POINT_MOCK_FIXTURE` env var MockProvider
already reads — not a new provider switch):

```
two-tool-calls          three-tool-calls        tool-raises
tool-hangs              rate-limited-then-ok    read-outside-project
ungranted-tool
```

**`cli.py` also gained** `send --max-steps/--max-tool-calls/--max-seconds` (the
`BoundsOverride` wire form), rendering for `ProgressUpdated`/`ToolStarted`/
`ToolCompleted`/`TaskFailed code=…`, and `session new --project <id>` —
`CreateSession` has carried `project_id` since B7, but nothing on the CLI could
set it until a tool needed a root to be guarded against.

---

## Changes to earlier episodes' code

### 1. Research's tool grants were renamed — `web`/`fs` → the real four

**This is the one change to a previous episode's shipped values, and it is
recorded here rather than in passing.**

B9 seeded `research` with `tools=("web", "fs", "load_task_result")` — placeholder
category names, written before any tool existed. B10 registers `read_file`,
`list_files`, `web_search`, `load_task_result`, and makes an unregistered grant a
**load-time** failure. Those three placeholder names would therefore have tripped
`validate_grants()` on **every startup**, making the backend unstartable.

`agents\builtins.py` now reads:

```python
tools=("read_file", "list_files", "web_search", "load_task_result"),
```

- **`web` → `web_search`** and **`fs` → `read_file` + `list_files`** (one
  category became two tools). `load_task_result` was already a real tool name and
  is unchanged.
- **No migration was needed.** Built-ins are re-upserted from code on every
  startup (`AgentRegistry.seed_builtins()`), so the stale row is overwritten, not
  accumulated beside the new one. This is exactly the property B9's seeding
  design was chosen for.
- The alternative — treating `web`/`fs` as capability *groups* that expand to
  tools — was rejected: `load_task_result` in the same tuple is a bare tool name,
  so the field would have meant two things at once.
- **`B09-agent-foundation.md` still records the old names** and is left unedited,
  per the rule that per-episode files are history. This entry is the correction.

### 2. `TaskResult.code` / `tasks.error_code` (authorized cross-episode edit)

`TaskResult` gained `code: Optional[str]`; `Task._run()` reads it off the
exception; the sink puts it in the `TaskFailed` payload; `Runtime._record_task()`
writes it to `tasks.error_code`. **Until this episode that column only ever held
`backend_died`**, written by the startup sweeper — a task that failed while the
process was alive left it NULL, so "why did this task fail" was answerable for
crashes and nothing else.

### 3. `ToolMessage` gained a `text` parameter (`managers\message.py`)

Every `MessageTranslator` renders through `Message.text`, which reads
`content.text`. A `TOOL_RESULT` content with only `data` reaches the provider as
an **empty string** — the model asks for a file and is handed a turn where it
received nothing. `data` stays authoritative for a later reader; `text` is what
goes back to the model. When omitted it falls back to a JSON dump of `result`,
which is at least truthful. The loop passes
`result.content if result.ok else f"error: {result.error}"` — a refusal is
phrased as one, not hidden, which is what lets the model try a different path
instead of repeating the denied call.

### 4. `StreamAccumulator` sums usage instead of replacing it

A tool loop streams several provider turns into **one** accumulator, and each
turn reports its own usage. Keeping only the last would bill a six-step research
run as if it were a single question.

### 5. `SessionRepository.fetch_messages_for_task()` / `SessionMemoryService.task_messages()`

Backs `load_task_result`. Uses `idx_messages_task`, which has existed since B6
and had no reader until now.

---

## Manual Test Log (Episode 10)

Self-tested first against throwaway scratch profiles, then re-run by Aryan; all
12 gate steps confirmed independently on 2026-08-27.

1. **Migration v3.** `schema_migrations` → `1 2 3`; `tool_calls` and its three
   indexes present; `doctor` reports `schema v3 (expected v3)`. ✅
2. **Done-when 5 — `list_for()`.** `general` → `[]`; `filler-explainer` → `[]`;
   `research` → `['read_file', 'list_files', 'web_search', 'load_task_result']`,
   exactly its grants in grant order. ✅
3. **Done-when 1 — two sequential tool calls.** `step 1/8` → `list_files`
   started/completed → `step 2/8` → `read_file` started/completed → `step 3/8` →
   final answer → `TaskDone`. Two `tool_calls` rows, both `ok=1`. ✅
4. **Done-when 6 — the path guard reaches the model as a tool result.**
   `read_file` on `C:/Windows/win.ini` → `ToolCompleted ok=false` carrying
   *"resolves to C:\Windows\win.ini, outside project root D:\Projects\Point"*,
   the loop **continued**, the model answered, `TaskDone`. Not an exception, not
   a `TaskFailed`. ✅
5. **Done-when 4 — `max_tool_calls=2` against a task wanting three.** Two pairs,
   then `TaskFailed code=bounds_exceeded`; `tasks` row `failed|bounds_exceeded`;
   the partial assistant message survived with all three narration fragments. ✅
6. **Done-when 2 — a tool that raises.** `TaskFailed code=tool_error`; the very
   next `send` on the same session completed normally. ✅
7. **Done-when 7 — a simulated 429.** `[progress: rate limited -- retrying in
   2s]`, a real ~2s pause, then the answer and `TaskDone`. The retry is visible
   in the event log, not just the file log. ✅
8. **Done-when 3 — a tool that hangs.** `ToolCompleted read_file 15000ms failed:
   … timed out after 15s and was stopped`, then `TaskFailed code=tool_timeout`.
   Killed at the tool's own timeout, reported, no hang. ✅
9. **Tools are granted, never invented.** A scripted call to `terminal` →
   `TaskFailed code=tool_not_granted`, listing the four granted names. No tool
   ran. ✅
10. **The ledger.** Eight `tasks` rows with the right `agent_id`/`status`/
    `error_code`; seven `tool_calls` rows including every failure, with
    `duration_ms` and the error text. ✅
11. **No regression on the non-mock paths.** The `echo` provider still echoes;
    a real `ollama` send with `general` printed `step 1/1` and answered. ✅
12. **`web_search` live.** 3/3 real results with titles, unwrapped urls, and
    snippets. ✅

---

## Bugs caught during manual testing

**1. `TaskStarted` raced the task body — every event before it was silently
dropped.** `_handle_send_message` emitted `TaskStarted` *after*
`agent_executor.run()` returned. But `TaskManager.create_task()` schedules the
body immediately, and the first `await` inside `run()` (`set_active_task`) hands
the loop to it — so any event the body produced before that emit reached the
client **ahead of the `TaskStarted` announcing the task**, and `cli.py`'s
`_first_ack_then()` filter (which drops everything until it sees `TaskStarted`)
threw it away. B9's single round-trip hid this: the first provider chunk always
happened to arrive later. B10's loop emits `step 1/N` with **no delay at all**
and lost it every time — the first observed run showed the tool pair but no
`step 1/8` and no opening text.
**Fix:** `TaskStarted` now comes from the sink's `"started"` chunk.
`Task._run()` emits that as its very first action, before it pulls a single
chunk out of the body, so the ordering is guaranteed rather than lucky. The
sink's old comment — *"Runtime already emitted TaskStarted before the body ran"*
— was the false assumption, stated out loud, since B6.
**This is a B1/B6-era defect, not a B10 one.** It was only reproducible once
something emitted at zero latency.

**2. A provider's `TOOL_CALL` chunk surfaced as a second, half-empty
`ToolStarted`.** The adapter initially mapped `ProviderChunkType.TOOL_CALL` to
`StreamEventType.TOOL_STARTED`, so every tool call produced **two**
`ToolStarted` envelopes: the real one from `ToolExecutor` with name, arguments
and timeout, and a phantom one from the provider chunk whose `data` held only
`tool_call` — rendering as `[tool.started None {}]`.
**Fix:** the adapter returns `None` for `TOOL_CALL`, exactly as it does for
`DONE`. A model *asking* for a tool is not a tool starting — the call may still
be refused as ungranted, rejected by its schema, or never reached because a
bound ran out. `tool_started` is emitted when a call actually begins. Two events
with one type, one of them half-empty, is worse than one honest event.

**3. The fix for #2 immediately broke the loop — a self-inflicted regression,
caught within one run.** The loop read `provider_chunk.tool_call` *after* the
`if stream_event is None: continue` guard. Once the adapter started returning
`None` for tool-call chunks, that `continue` skipped the collection and
`requested` stayed empty — so every run answered on step 1 and no tool ever ran.
**Fix:** collect off the **raw** chunk before the adapter is consulted, with a
comment saying why. Worth recording because the two changes look independent and
are not: the adapter's return value and the loop's read order are coupled.

---

## What the next episode inherits

**B11 (The Three Agents)** inherits a loop that is fully bounded and fully
config-driven, and **calls it on every agent turn**:

- **Branch on `payload["code"]`, never on `payload["error"]`.** The four codes
  that appear as `TaskFailed` are `bounds_exceeded`, `tool_error`,
  `tool_timeout`, `tool_not_granted`, with the exact payloads recorded above.
  `bounds_exceeded` is **not** a failure of the work — the partial answer is
  persisted, and a result card should render it.
- **`tools_unsupported` arrives as an `Error` envelope with no `code` key.**
  So does anything else `AgentExecutor.run()` raises before the task exists
  (`InvalidEventError` from the provider/model chain, `ContextOverflowError`
  from B8). Closing that asymmetry is B17's, and B17's doc now names the three
  stopgap classes explicitly.
- **`.bound` and `.limit` on `BoundsExceededError` are not on the wire.** If
  B11's task card wants to say *which* bound stopped a run, that is a payload
  addition, not a string parse.
- **Research's step loop is now real, but nothing decides when to stop early.**
  `max_steps=8` / `max_tool_calls=20` / `timeout_s=180` are enforced; what is
  absent is the agent *choosing* to stop. The loop ends when the model answers
  without asking for a tool, or when a bound fires. Memory-first search (B13),
  and the judgement about whether stored findings are stale enough to
  re-research, are both still unbuilt — the system prompt describes behaviour
  nothing implements.
- **`writes_proposals=True` still does nothing.** No proposal path exists;
  `SessionMemoryService.propose()` still raises `NotImplementedError` for B13.
- **`inline: bool` is still threaded nowhere.** Unchanged from B9: one call site,
  always `inline=True`. Research being a background task is B11's first
  `inline=False` caller, and nothing downstream branches on it yet.
- **Filler-Explainer's two-mode split is still unbuilt.** Both modes have
  `tools=()` and `max_steps=1`, so both run the loop exactly once with no tools —
  structurally identical to `general` today.

**Adding a tool is: write it in `tools\builtins\`, register it in
`BUILTIN_TOOLS`, and add its name to an agent's grant tuple.** Forgetting the
third step means the model cannot call it; a typo in the third step is a startup
error naming the tool. There is no path from a model naming a tool to that tool
running.

**No agent can write a file or run a command — by design, permanently.** B10's
Do NOT section lifts the command-execution prohibition for exactly one future
agent (the Coding Assistant, D84, v2, where a checkpoint commit covers the risk)
and for nothing else. The Filler and Research never get it. MCP stays parked as a
`ToolProvider` behind this registry; tool approval prompts stay parked (the diff
card covers the v1 case).

**No new dependencies.**

---

## Side effect of running the gate

The verification runs `project init` against `D:\Projects\Point`, which is B7
behaving correctly: it creates `.point\project.json` and appends `.point/` to the
repo's `.gitignore`. Both are expected. A `.point` marker whose `project_id` has
no row in a *different* database is handled —
`ProjectManager._read_marker_and_resolve()` recreates the row from the marker,
since the id in the file is the identity.
