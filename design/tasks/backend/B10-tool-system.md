# B10 — Tool System

**Phase:** P1 · **Batchable:** no · **Depends on:** B9

## Purpose

Give agents bounded external capability, with a loop that terminates and errors
that arrive as events rather than crashes.

`backend\managers\tool.py` exists but is **empty**. Fill it, or create
`backend\tools\` for the individual tools — say which in the handoff.

## Build

### Tool definition
```python
@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    schema: dict          # JSON Schema for parameters
    timeout_s: int
    run: Callable[[dict, AgentContext], ToolResult]
```
`ToolResult`: `{ok, content, error, tokens_estimate, meta}`.

### ToolRegistry
`register`, `get`, `list_for(agent_config)` — an agent sees **only** the tools its
config grants. Tools are granted, never invented: a config naming an unregistered
tool fails validation **at load time**, not at call time.

### ToolExecutor
- Validates arguments against the schema before running. A schema failure is a
  `ToolResult(ok=False)` fed back to the model, **not** an exception.
- Enforces the per-tool timeout.
- Emits `tool.started` / `tool.completed` with name and duration.
- Records every call against the task, so "which document contaminated this
  answer" is answerable later.

### The tool-call loop
```
model → tool_calls? → execute (respecting max_tool_calls)
      → append results → model → … → final answer

bounded by max_steps, max_tool_calls, timeout_s from AgentConfig
on exceeding a bound: stop, emit task.failed with code=bounds_exceeded,
                      and KEEP whatever partial output exists
```
Step count emitted as `task.progress` so the UI can show `step 3/6`.

### v1 tools
```
read_file(path)             guarded by B7's resolve(); size-capped;
                            returns content + mtime
list_files(prefix, limit)   from B7's index
web_search(query, limit)    provider-agnostic; returns title/url/snippet
load_task_result(task_id)   full text of a past result — the "load" half of
                            reference mode
```

For `web_search`, pick the simplest thing that works and **note the choice and
its cost in the handoff**. If it needs a key, it resolves through B6's credential
chain like any other.

### Provider tool-calling
Each adapter in `providers\` translates the common tool schema into its own
format and normalizes tool-call responses back. Providers without tool support
advertise `supports_tools=False`, and an agent requiring tools on such a provider
fails **at invocation with a clear message**, not mid-stream.

### 429 handling (the B4 additive change)
Exponential backoff with jitter, respecting `Retry-After`. A rate limit is a
**retry, not a failure** — surface it as `task.progress` with
`rate limited — retrying in 12s`. Without this, a burst of concurrent tasks looks
like random crashes.

## Do NOT

- Add a terminal or command-execution tool. **No agent gets command execution in
  v1.** This prohibition is lifted for the **Coding Assistant only** when it is
  unparked in v2 (D84) — it needs to run tests, read the failure, and fix it, and
  a checkpoint commit covers the risk. Do not build it here, and do not build it
  for the Filler or Research ever.
- Add a write or edit tool. Agents return regions; Go computes diffs; the user
  applies. This holds in every phase.
- Add MCP (parked — it is a `ToolProvider` behind this registry).
- Add tool approval prompts (parked; the diff card covers the v1 case).

## Done when

1. An agent completes a task requiring **two sequential** tool calls.
2. A tool that raises produces `task.failed` with a code — the runtime survives
   and the next request works immediately.
3. A tool that hangs is killed at its timeout and reported.
4. `max_tool_calls = 2` with a task wanting three stops cleanly with
   `bounds_exceeded` and keeps partial output.
5. `list_for()` on `general` returns empty; on `research`, exactly its grants.
6. `read_file` outside the project root is refused by B7's guard, and the refusal
   reaches the model **as a tool result**, not as an exception.
7. A simulated 429 retries with backoff and eventually succeeds, with the retry
   visible in the event log.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend
$env:POINT_MOCK = "1"

uv run python cli.py send "@research test" --script two-tool-calls
```
**Expect:** `tool.started` / `tool.completed` pairs, then a final answer.

```powershell
uv run python cli.py send "@research test" --script tool-raises
uv run python cli.py send "still alive?"
```
**Expect:** `task.failed` with a code; the second command works normally.

```powershell
uv run python cli.py send "@research test" --script tool-hangs
```
**Expect:** killed at the timeout, reported, not a hang.

```powershell
uv run python cli.py send "@research test" --script three-tool-calls --max-tool-calls 2
```
**Expect:** `bounds_exceeded`, partial output preserved.

```powershell
uv run python cli.py send "@research test" --script rate-limited-then-ok
```
**Expect:** a retry with a visible delay, then success.

```powershell
uv run python cli.py agents show general
uv run python cli.py agents show research
Remove-Item Env:\POINT_MOCK
```
**Expect:** `general` has no tools; `research`'s list matches what it can call.

**Fails if:** a tool exception escapes as a traceback · a bounded loop runs past
its cap · an agent calls a tool its config does not grant · a 429 surfaces as a
failure.

## Handoff

State after this episode: agents can act within bounds, every call is recorded,
every failure is an event, and rate limits are handled. No agent can write a file
or run a command — by design, permanently.

Record in `completed/`: the `web_search` implementation chosen and why, and the
exact backoff parameters.

Do not proceed until every check above passes.
