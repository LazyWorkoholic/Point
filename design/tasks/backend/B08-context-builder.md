# B8 — Context Builder v0 (deliberately dumb)

**Phase:** P1 · **Batchable:** no · **Depends on:** B6, B7

## Purpose

The function that decides what enters a finite context window on this turn. It is
the highest-leverage component in the backend and the thing no comparable tool
exposes. **v0 is intentionally simple** — the real memory system (B13) is built
later, against failures observed while actually using B11–B12.

## Build

Put it in `backend\managers\context.py` (new file), using the existing
`message_history.py` and `message_translator.py` rather than duplicating them.

Reading `context_attachments` needs a repository that does not exist yet, so this
episode also adds **`backend\database\attachment_repository.py`** — read rows for
a session, and update a row's recorded `(mtime, size)`. Read + update only. It
does not insert, delete, or change `mode`; B12 owns all of that.

It also adds **`backend\managers\attachment.py`**, holding the `Attachment`
domain type. Its own module for the reason `Session` and `Message` have theirs:
the repository imports the type, the type imports nothing from `database\`, and
`context.py` never reaches through a repository module to name what it works
with.

### The function
```python
async def build(request: ContextRequest,
                policy: ContextPolicy,
                budget_tokens: int,
                memory: MemoryService,
                *, record: bool = True) -> BuiltContext
```

**Async**, because everything it reads is behind an aiosqlite repository. It
still never touches SQLite: `memory` is the only way in, which is what keeps the
builder testable with a fake and the storage layer swappable.

`record=False` suppresses the function's one side effect -- updating an
attachment's recorded `(mtime, size)`. `report()` passes it; see below.

`BuiltContext` carries the provider-ready message list **and a log**: what was
included, its token cost, and what was evicted and why.

### ContextRequest — not `GenerationRequest`
`providers.models.GenerationRequest` cannot be the input here: it already carries
`messages`, which is this function's *output*, and it has no field for the
session or the provider. Define a separate input type in `context.py`:

```python
@dataclass(frozen=True)
class ContextRequest:
    session_id: str
    provider: str                 # resolved name -- drives reasoning-block filtering
    model: str
    user_text: str | None = None  # this turn's new user message, not yet persisted
    task_id: str | None = None    # set only while a task is running
```

The caller composes the two: `GenerationRequest(model=…, messages=built.messages)`.

### ContextPolicy — deliberately NOT named `AgentConfig`
B9 defines the real `AgentConfig`, mirroring the `agents` table row for row. B8
must not squat that name with a smaller, differently-shaped type. The builder
takes only the fields it actually reads:

```python
@dataclass(frozen=True)
class ContextPolicy:
    system_prompt: str
    stm_window: int = 20
    default_attach_mode: str = "full"   # full | reference | detached
    effort: str | None = None
```

There is no agents repository yet (`managers\agent.py` is empty; B9 owns it), so
nothing hydrates this from the `agents` table in this episode — callers pass a
literal. **B9's `AgentConfig` must be adaptable to `ContextPolicy`** (a
`to_context_policy()` or equivalent), not replace it: the builder's input stays
the narrow set of fields it reads, so it never grows a dependency on the agent
system.

### Assembly order (fixed)
```
1. system prompt                    always
2. attachments                      resolved from context_attachments
3. memory                           v0: nothing (interface exists)
4. session summary                  v0: nothing (interface exists)
5. recent messages                  newest-first until the budget is spent
6. task working state               only while a task is running
```

### Eviction
Drop from the **oldest message backwards**, never partially truncating a message.
If a *single* message or attachment exceeds the budget alone, raise
`ContextOverflowError` with both numbers — never send something the provider will
reject with an opaque 400.

`ContextOverflowError(RuntimeException)` goes in `runtime\errors.py` and carries
`needed_tokens` and `budget_tokens` as attributes, not just inside the message
string. **It is not `PointError`.** `PointError(code=…)` is B17's consolidation of
every `RuntimeException` subclass in that file, and B7's handoff is explicit that
it must not be built piecemeal ahead of schedule. `ContextOverflowError` is the
same stopgap shape as `PathDeniedError`, and B17 folds both in together.

### Token estimation
`len(text) // 4` is acceptable in v0. Isolate it behind
`estimate_tokens(text, model)` so a real tokenizer can replace it without
touching callers.

### Budget resolution
No config key for this exists yet. Add `context.budget_tokens`, read through
`appconfig.config_value`, **default 8000**. A model's real `context_window` is not
usable as the default: `echo` is not a registered provider and unregistered models
have no `Model` row, so the common CLI path has no window to read. `build()` still
takes `budget_tokens` explicitly; config only supplies what callers pass when they
have nothing better.

### Attachment resolution
Attachment paths go through **B7's `pathguard.resolve_path()`**, never a bare
`Path()`. B7's contract is that it is the only sanctioned way to turn a string
into a filesystem path anywhere in this backend, and an attachment path is
exactly the case that matters: a string from a database row that becomes a file
read. The session's project is the containment root; a session with no project
has no scope, so an absolute path is refused unless
`project.allow_outside_project` is on. **This is why B7 is a dependency of this
episode.**

A refused, deleted, or unreadable attachment is logged with 0 tokens and a
reason. It never fails the turn -- one bad attachment must not cost the user
their message.

- `context_attachments` rows are resolved at build time — **never copies**.
- `source_type=file`: compare current `(mtime, size)` against the recorded pair.
  Unchanged → contribute **nothing** (it is already in history). Changed →
  include a fresh copy marked `— updated since earlier`, and update the recorded
  pair through the repository.
- `mode=reference`: include a **mechanical** descriptor only — basename as the
  title, byte size, line count, the first ~200 characters, and the row id. It is
  not a generated summary, and nothing in this episode generates one; the table
  has no title or summary column to read one from either.
- `mode=detached`: contribute nothing.

### Reasoning-block filtering
Include a reasoning/thinking block only when its recorded `provider` matches the
provider for this request (the B3 tag added in B6). **Never delete** — filter on
the way out. `MessageHistory.for_provider()` already implements exactly this;
call it, do not write a second filter.

### MemoryService interface
```python
class MemoryService(Protocol):
    async def recent_messages(session_id, n) -> list[Message]:      ...  # implemented
    async def attachments(session_id) -> list[Attachment]:          ...  # implemented
    async def project(session_id) -> Project | None:                ...  # implemented
    async def record_attachment_sent(id, mtime, size) -> None:      ...  # implemented
    async def search(query, scopes, limit) -> list[Record]:         ...  # NotImplementedError
    async def summary(session_id) -> str | None:                    ...  # NotImplementedError
    async def propose(record) -> None:                              ...  # NotImplementedError
    async def forget(id) -> None:                                   ...  # NotImplementedError
```

`project` and `record_attachment_sent` are additions to the episode's original
list, and both are forced: the first supplies `resolve_path()`'s containment
root, the second is how "resolved" becomes "recorded" without the builder
reaching past the repository layer.

The four unimplemented methods **raise rather than returning empty**. An empty
list is indistinguishable from "searched and found nothing", which is how a
caller ends up believing retrieval ran when no retrieval exists. `build()`
catches `NotImplementedError` at the two points where v0 is expected to have
nothing, and logs those sections as unimplemented — so the absence is visible in
every report rather than inferred.

### Context report
`context.report(session_id)` returns the breakdown the status card renders:
per-section token counts, total, free, and what the next turn would evict. It is
a **projection of the same log**, not a second implementation.

Nothing persists the log, so `report` re-runs `build()` for the session's *next*
turn and projects that run's log. This is what makes "same log, not a second
implementation" true, and what makes the totals match the built context.

`report` builds with **`record=False`**. A projection of a turn that has not
happened must not mark a changed attachment as already sent -- otherwise asking
what the next turn would cost is what stops the next turn from re-reading the
file. The practical consequence for verification: **`context report` shows what
the next `send` will pay**, so the attachment's cost appears in the report taken
*before* the send, and reads 0 in the report taken after.

Add `cli.py context report`, taking `--session <id>` or `--last`. `--last` means
**the most recently updated session** (`sessions.updated_at DESC`), so the common
case needs no id pasted.

### Wiring into the send path
The builder gates context from day one — it is not standalone infrastructure
waiting for a caller. In `runtime\runtime.py`:

- `_handle_send_message` builds the context and checks the budget **before**
  creating the task, so an oversized message fails as `ContextOverflowError` →
  clean `Error` event, on the `echo` path too, not only where a provider exists.
- `_stream_chat_body` sends `BuiltContext.messages` instead of
  `session.messages + [user_message]`.

This is an additive change to B1/B5 code, recorded in the backend `INDEX.md`
additive-changes table. B9's `AgentExecutor` is expected to supersede this exact
wiring point — it owns "resolve config → build context → create task → stream"
end to end. Do not build anything here that assumes the wiring is permanent.

## Do NOT

- Implement search, summarization, promotion, or extraction.
- Add embeddings.
- Let the builder touch SQLite directly — it goes through repositories.
- **Build `cli.py attach` in any form**, not even a minimal one. B12 owns
  `attach` / `detach` / `set_mode`, and nothing in this backend writes a
  `context_attachments` row yet. The verification below inserts the row directly
  with `sqlite3` — that is test scaffolding, not a CLI surface, and it keeps
  B12's scope untouched.
- Build `PointError`, or give `ContextOverflowError` a `code` / `retryable` /
  `detail` shape ahead of B17.
- Name the local policy type `AgentConfig`. That name is B9's.

## Done when

1. For any turn, the log states exactly what entered, each section's cost, and
   what was dropped.
2. Two `ContextPolicy` values with different `stm_window` produce measurably
   different contexts for the same input.
3. An attached file unchanged since the previous turn contributes 0 tokens; after
   being modified, it contributes a fresh copy marked as updated.
4. A message larger than the budget raises `ContextOverflowError` with both
   numbers, surfaced as a clean `Error` event — not a provider error, and not a
   traceback.
5. `context.report` totals equal the sum of its sections and match the built
   context.
6. A reasoning block tagged `anthropic` is absent when the request targets
   `openai`, and present when it targets `anthropic` — and is still in the
   `messages` table either way. Filtered on the way out, never deleted.
7. `context report` does not change what the next turn would build. Running it
   twice in a row produces identical output, and running it never causes a
   changed attachment to be skipped by the next `send`.

## Manual verification (PowerShell)

**One script, scratch profile.** `%APPDATA%\Point` and `%LOCALAPPDATA%\Point` are
renamed aside up front and restored in `finally`. Nothing below runs against the
live profile, and no step depends on an env var set by a previous step.

Three things this script does that are not obvious:

1. **It writes a `config.toml` into the scratch profile.** Attachment resolution
   goes through B7's path guard, and the demo file lives in `$env:TEMP`, outside
   any project — so with the default `project.allow_outside_project = false` the
   attachment is *correctly* refused and every attachment check reads 0. Setting
   it is part of the setup, not a workaround.
2. **The overflow check lowers the budget instead of sending a huge message.**
   Windows caps a command line at ~32K characters, so `cli.py send ("x" * 400000)`
   cannot launch at all — the process fails before Python starts. Dropping
   `context.budget_tokens` to 60 and sending a 400-character message exercises
   exactly the same code path with numbers that fit on a command line.
3. **Each attachment check reads the report taken *before* the send.**
   `context report` is `record=False`, so it shows what the next `send` will pay.
   After the send has paid it, the file is recorded as sent and the next report
   correctly reads 0.

Note: `bootstrap.resolve_db_path` copies `backend\runtime.db` into a fresh profile
that has no database, so the scratch profile starts with the old dev sessions in
it. That is fine — every step below passes an explicit session id.

```powershell
$ErrorActionPreference = "Stop"
Set-Location D:\Projects\Point\backend

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$cfg   = Join-Path $env:APPDATA      "Point"
$state = Join-Path $env:LOCALAPPDATA "Point"
$aside = "Point.aside-$stamp"

if (Test-Path $cfg)   { Rename-Item -LiteralPath $cfg   -NewName $aside }
if (Test-Path $state) { Rename-Item -LiteralPath $state -NewName $aside }

try {
    New-Item -ItemType Directory -Force $cfg | Out-Null
    "[project]`nallow_outside_project = true" |
        Set-Content -Encoding utf8 (Join-Path $cfg "config.toml")

    $db   = Join-Path $state "runtime.db"
    $demo = Join-Path $env:TEMP "b8-demo.py"

    # -- 1. sections and arithmetic -------------------------------------
    $sid = (uv run python cli.py session new "b8").Split(" ")[0]
    uv run python cli.py send "hello" --session $sid
    uv run python cli.py context report --session $sid

    # -- 2. attachment change detection ---------------------------------
    # The row is written directly. B12 owns `cli.py attach`; this episode
    # deliberately does not build it. A NULL recorded pair means "never sent".
    @"
def greet(name):
    print(f"hello {name}")

def main():
    greet("world")
    greet("point")

if __name__ == "__main__":
    main()
"@ | Set-Content $demo

    $aid = [guid]::NewGuid().ToString()
    $esc = $demo -replace "'", "''"
    sqlite3 "$db" "insert into context_attachments (id, session_id, source_type, source_id, mode, recorded_mtime, recorded_size, created_at) values ('$aid', '$sid', 'file', '$esc', 'full', NULL, NULL, strftime('%s','now'));"

    Write-Host "`n--- 2a. before the first send: attachment costs its full body ---"
    uv run python cli.py context report --session $sid | Select-String "attachments "

    uv run python cli.py send "what does this do" --session $sid | Out-Null

    Write-Host "`n--- 2b. after the send: unchanged, costs 0 ---"
    uv run python cli.py context report --session $sid | Select-String "attachments "

    "print('v2 -- a different length entirely')" | Set-Content $demo
    Write-Host "`n--- 2c. after the file changes: re-included, marked ---"
    uv run python cli.py context report --session $sid | Select-String "attachments "

    # -- 3. reference mode is mechanical only ---------------------------
    Write-Host "`n--- 3. reference mode ---"
    sqlite3 "$db" "update context_attachments set mode = 'reference', recorded_mtime = NULL, recorded_size = NULL where id = '$aid';"
    uv run python cli.py context report --session $sid

    # -- 4. oversized single message ------------------------------------
    # Budget lowered rather than message enlarged: see note 2 above.
    Write-Host "`n--- 4. context overflow ---"
    "[project]`nallow_outside_project = true`n`n[context]`nbudget_tokens = 60" |
        Set-Content -Encoding utf8 (Join-Path $cfg "config.toml")
    $big = "x" * 400
    uv run python cli.py send $big --session $sid
    Write-Host "exit code: $LASTEXITCODE  (expect 1)"
    Write-Host "messages in session (must not have grown):"
    sqlite3 "$db" "select count(*) from messages where session_id = '$sid';"

    # -- 5. stm_window and reasoning-block filtering --------------------
    Write-Host "`n--- 5. stm_window + reasoning filter ---"
    "[project]`nallow_outside_project = true" |
        Set-Content -Encoding utf8 (Join-Path $cfg "config.toml")
    uv run python tests\manual_context_test.py $sid
}
finally {
    Remove-Item -LiteralPath $cfg   -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $state -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath (Join-Path $env:TEMP "b8-demo.py") -Force -ErrorAction SilentlyContinue
    if (Test-Path (Join-Path $env:APPDATA      $aside)) { Rename-Item -LiteralPath (Join-Path $env:APPDATA      $aside) -NewName "Point" }
    if (Test-Path (Join-Path $env:LOCALAPPDATA $aside)) { Rename-Item -LiteralPath (Join-Path $env:LOCALAPPDATA $aside) -NewName "Point" }
}
```

`tests\manual_context_test.py` is part of this episode's build. Given a session
id, it covers the two checks the CLI cannot reach — `session new` has no
`--stm-window` (that knob belongs to B9's agent config), and on a keyless profile
the only usable provider is `echo`, which never reaches the provider layer at
all, so no reasoning block can arrive through it:

- builds the same request under `stm_window=2` and `stm_window=50`, printing
  message count, history tokens, and total used for each;
- appends a `ThinkingMessage(..., provider="anthropic")`, then builds once
  targeting `anthropic` and once targeting `openai`, printing whether the block
  survived each time.

**Expect, in order:**

1. Sections (`system`, `attachments`, `memory`, `summary`, `history`,
   `task_state`), per-section token counts, a total, and free space. **The
   numbers must add up** — sections sum to total, total + free = budget. `memory`
   and `summary` read 0 with `v0: ... arrives in B13`; that is the interface
   being present and honest, not a bug.
2. **2a** a non-zero attachment cost, detail `first send`; **2b** `0`, detail
   `unchanged since last sent`; **2c** non-zero again, detail
   `changed, re-sent — updated since earlier`.
   **Fails if:** 2b is non-zero (re-sent every turn — waste), or 2c is 0 (never
   re-sent after the change — the model answers about code that no longer
   exists).
3. In `reference` mode: basename, byte size, line count, a ~200-character head,
   and the row id, at a smaller cost than the full body.
   **Fails if:** anything prose-shaped appears that the file does not contain.
4. `context_overflow: this message needs 100 tokens, but the entire context
   budget is 60 tokens` — **both** numbers, as an `Error` event, exit code 1.
   Not a provider error, not a traceback. The message count must be **unchanged**:
   a message that cannot be sent must never enter the history.
5. `stm_window=2` and `stm_window=50` print different message counts and
   different `used` totals; `target=anthropic` reports the thinking block present,
   `target=openai` reports it absent with `filtered_out=1`.
6. Afterwards, `$env:APPDATA\Point` and `$env:LOCALAPPDATA\Point` are the real
   profiles again — confirm with `Get-ChildItem $env:LOCALAPPDATA\Point`, and check
   that no `Point.aside-*` directory was left behind.

## Handoff

State after this episode: every request's context is assembled by one function
with an explicit budget and an auditable log. Memory is a stub with a real
interface. `context.report` exists for the status card (F6).

Record in `completed/`:

- **`ContextPolicy`, not `AgentConfig`.** B9 owns the `AgentConfig` name and
  shape (the `agents` table, row for row). B9's `AgentConfig` must **adapt to**
  `ContextPolicy` — not replace it, and not widen the builder's input to the full
  agent row.
- **`_handle_send_message` / `_stream_chat_body` wiring is a placeholder.** B9's
  `AgentExecutor` owns "resolve config → build context → create task → stream"
  and is expected to supersede this exact call site. Anything built on top of the
  current wiring will move.
- **`ContextOverflowError` is a stopgap**, like `PathDeniedError`. B17 folds both
  into `PointError(code=…)`. Do not grow it a code/retryable/detail shape first.
- **`cli.py attach` still does not exist.** B12 owns it. The attachment path is
  implemented and verified, but the only way to create a row today is direct SQL.
- **New config key**: `context.budget_tokens` (default 8000).
- **Attachment resolution is behind B7's path guard**, so a session with no
  project cannot resolve an attachment outside a project root unless
  `project.allow_outside_project` is on. Every session today has
  `project_id = NULL`, which makes this the *common* path, not the edge case.
  B12 should decide whether attaching a file implies binding the session to a
  project — this episode only reports the refusal.
- **`report()` is `record=False`.** Anything that calls `build()` to ask a
  question rather than to send a turn must pass it, or asking the question
  changes the answer.
- **`send $big` cannot test overflow on Windows.** A ~32K command-line cap means
  a 400,000-character argument fails before Python starts. The check lowers
  `context.budget_tokens` instead. Any future episode testing a size limit
  through `cli.py` hits the same wall.
- **New file not in the original spec**: `managers\attachment.py`, holding the
  `Attachment` domain type, so the repository has something to return that is not
  defined inside a repository.
- **No new dependencies.**

Do not proceed until every check above passes.
