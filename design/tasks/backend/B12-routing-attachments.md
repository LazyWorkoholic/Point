# B12 — Routing, Attachments, Concurrency Caps

**Phase:** P1 · **Batchable:** no · **Depends on:** B11

**This is the end of the minimum viable backend.** Everything after is stretch.

## Purpose

Turn typed input into the right execution, resolve what the user attached, and
make concurrency honest — the UI must never claim three things are running while
the provider processes one.

## Build

### Input parsing
```
plain text          → turn, inline, the session's current agent
@agent rest         → task, agent resolved by name
@path\to\file       → attachment (path-like, or matched in the B7 index)
@C:\... @~\... @..\ → attachment, filesystem mode
@agent --advanced   → parsed and REFUSED in v1:
                      "advanced capabilities are not available yet"
```
Ambiguity: an **exact agent name wins**; otherwise treat it as a path. Parsing
returns a structured `Invocation`, never a half-parsed string.

Accept both `\` and `/` in typed paths — users type both on Windows, and the
guard normalizes anyway.

### Attachment resolution
- Resolve through B7's `resolve()`; refuse outside the project unless configured.
- Guards, each returning a **reason**, not a traceback:
  - over `attachments.max_kb` (default 100) → refuse, offer first-200-lines
  - binary (null bytes in the first 8KB) → refuse
  - directory → refuse, offer a file listing
- Return a **token estimate** with the resolution, so the UI can show the cost
  before the user sends.
- On send, write a `context_attachments` row with `mode` from the agent's
  `default_attach_mode`, overridable per invocation, recording `(mtime, size)`.
- `attach` / `detach` / `set_mode`. **Detach never deletes** — it removes the
  binding only.

### Turn queue
- Server-side FIFO, **one conversational turn in flight per session**.
- `chat.queue_depth`, default 1. At capacity, reject with `queue_full` so the UI
  can lock the input *with a reason*.
- Queued turns are cancellable before they start.
- **Agent tasks never enter this queue** — separate lane.

### Concurrency caps
```toml
[limits]
max_concurrent_tasks = 3

[limits.ollama]
max_concurrent_tasks = 1     # local inference is serialized anyway
```
Per-provider, falling back to global. Beyond the cap, tasks enter `QUEUED` (the
existing B1 state) and start as slots free. **Nothing is rejected and nothing
lies.**

### Task list query
`tasks.list(scope)` returning running + queued + recent tasks **across all
sessions**, because the frontend root owns this list globally.

### Cancellation
`task.cancel(task_id)` → partial output persisted with `interrupted=True` (B5
behaviour), exactly one terminal `cancelled` event.

Add `cli.py`: `attach`, `detach`, `tasks list`, `task cancel`.

### Persisted invocation (provenance)

Parsing already produces a structured `Invocation`. **Persist it on the
assistant message that results.** `managers/message.py:178` already defines
`InvocationMetadata`, `message.to_dict()` already serialises it (`:290`), and
the Go client already parses it (`model.go`'s `wireInvocation`) — the type
exists on both sides and **nothing writes it**. This finishes that path.

- `streaming/accumulator.py`'s `finalize()` builds the `AssistantMessage` with
  no invocation; `agents/executor.py` never sets one. One of them must.
- Record at minimum `agent`; also `provider` and `model` if cheaply available.
- A plain turn (no `@agent`) records the session's current agent, not null —
  "which agent produced this" must always have an answer, since B12's own rule
  is that every message routes through an agent config, including plain chat.

Without this, a reload cannot tell that a message came from `@research`, so the
frontend renders it as an ordinary assistant message. Provenance is lost the
moment the app restarts.

### Persisted terminal outcome (completeness)

Done-when #8 already requires a cancelled turn to persist `interrupted=True`.
**Extend that to every non-clean ending**, so the transcript never asserts an
answer finished when it did not.

- Today `runtime.py:853` sends `error`/`code` over the wire but writes nothing to
  the session. A failed or timed-out task leaves **no row at all**; a
  bounds-stopped run keeps its partial text with **no indication it was
  truncated**.
- Record `outcome` (one of: `done` | `cancelled` | `failed` | `timed_out` |
  `bounds_exceeded`) and the existing error `code` against the message or its
  task row, so reload can re-attach the correct footer.
- Partial text that was already streamed stays persisted, as it does for
  cancellation today — the marker describes *how it ended*, it does not discard
  content.

This is a truthfulness requirement, not decoration: after a reload, a partial,
timed-out, or bounds-stopped answer currently renders as a complete assistant
message.

### Link a question to its task

executor.py:418 builds UserMessage(session.id, text) before task_id
exists (generated at :421), so a user message never carries a
task_id -- only the assistant message does. A question therefore
cannot be paired to its own answer on reload, and a retry cannot be
distinguished from a genuine repeat.

Generate task_id before the user message and record it on both. This
makes answer-to-question pairing possible on reload, and is the
prerequisite for ever distinguishing a retry from a repeat.

## Do NOT

- Build the `@` picker or any UI (F8).
- Implement `--advanced` behaviour — parse it and refuse.
- Implement memory search for the picker's memory section (B13).

## Done when

1. `@research x` creates a background task while a plain message in the same
   session still streams inline and independently.
2. With Ollama capped at 1, three simultaneous `@research` run strictly one at a
   time, two reported `QUEUED`, and `tasks list` says so truthfully.
3. A second plain message while one streams is queued; a third is rejected with
   `queue_full` at depth 1.
4. A 40MB file is refused with a message naming the limit.
5. A binary is refused; a directory is refused with a listing offer.
6. The attachment's token estimate is within ~20% of what B8's log shows it cost.
7. `detach` removes the binding; the file and any task result are untouched.
8. Cancel mid-stream → partial message persisted, `interrupted=True`, exactly one
   terminal event.
9. Every persisted assistant message carries an `invocation` naming the agent
   that produced it — including plain turns, which record the session's current
   agent rather than null. Verified by reading the row back, not by inspecting
   the wire.
10. A task that fails, times out, or stops at its bounds persists a terminal
    `outcome` and error `code`, and any partial text it produced is preserved.
    A clean finish records `done`. No non-clean ending leaves the session
    indistinguishable from a completed answer.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

# 2. concurrency honesty (set limits.ollama.max_concurrent_tasks = 1 first)
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "@research a" }
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "@research b" }
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "@research c" }
Start-Sleep -Seconds 3
uv run python cli.py tasks list
```
**Expect:** one RUNNING, two QUEUED.
**Fails if:** three RUNNING — the UI would then be lying to the user.

```powershell
# 3. queue
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "write a long essay" }
Start-Sleep -Seconds 1
uv run python cli.py send "second"     # queues
uv run python cli.py send "third"      # rejected
```
**Expect:** the third returns `queue_full`, not a silent drop.

```powershell
# 4-5. attachment guards
$f = "$env:TEMP\big.log"
$fs = [System.IO.File]::Create($f); $fs.SetLength(40MB); $fs.Close()
uv run python cli.py attach $f
uv run python cli.py attach "C:\Windows\System32\notepad.exe"
uv run python cli.py attach "$env:TEMP"
Remove-Item $f
```
**Expect:** three refusals — size (with the first-200-lines offer), binary,
directory (with a listing offer).

```powershell
# 6. estimate accuracy
uv run python cli.py attach "$env:TEMP\proj\main.py"      # note the estimate
uv run python cli.py send "explain"
uv run python cli.py context report --last                # compare
```
**Expect:** within ~20%.

```powershell
# 8. cancellation
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "write a very long essay" }
Start-Sleep -Seconds 3
uv run python cli.py task cancel --last
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select interrupted, length(content) from messages order by rowid desc limit 1;"
```
**Expect:** `1|<non-zero>`.

```powershell
# 9. provenance survives persistence
uv run python cli.py send "@research something short"
uv run python cli.py send "a plain question"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select role, json_extract(content,'$.invocation.agent') from messages order by rowid desc limit 4;"
```
**Expect:** the research reply's row names research; the plain reply's row names the session's current agent (e.g. general) — not null. Fails if: either is null — the frontend cannot rebuild a slot from a null.

```powershell
# 10. terminal outcome is recorded for a non-clean ending
# (set a low bound so this ends deterministically, e.g. --max-steps 1 on an
#  agent that needs more, or cancel a long run)
uv run python cli.py send "@research a question needing several steps" --max-steps 1
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select outcome, error_code, length(content) from messages order by rowid desc limit 1;"
```
**Expect:** `bounds_exceeded|<code>|<non-zero>` — the marker is recorded and the partial text survives. Fails if: outcome is null or `done`, or the row is missing entirely.

## Handoff

State after this episode: **the v1 backend is complete.** Input routes
correctly, attachments resolve with guards and visible costs, concurrency is
honest, cancellation is clean, and **every persisted message carries its
provenance and its terminal outcome** — so a session reloaded from disk is as
truthful as a live one.

### What F5 depends on from this episode

F5 (built before this, against B11) renders collapsed task slots for agent
runs and footers for non-clean endings. Both are **live-session-only** until
this episode lands, because the data to rebuild them was never persisted. After
B12, F5 should be re-verified:

- A `@research` run survives `Ctrl+Q` + restart as a **collapsed slot**, not as
  a plain assistant message. `o` expands it.
- A failed / timed-out / bounds-stopped answer reloads **with its footer
  intact**, not as an ordinary complete reply.

Run that as a short verification pass **after** this episode's own gate passes —
not as part of it. If a slot is still wrong once the rows are correct, the
defect is in F5's rendering, and this episode's gate has already proved the data
side.

### ⚠ Checkpoint — decide here
```
~3 weeks left → build B15, then the frontend, then B13. DROP B16 (SSH).
~6 weeks left → do everything.
```

Do not proceed until every check above passes.
B12 now carries a migration. B12 takes the next version number; B18 takes the one after. If B12 lands as v5, update design/tasks/backend/B18-message-pagination.md to say SCHEMA_VERSION → 6 everywhere it currently says 5 (three places: Build step 1, Done-when #7, and the migrate line in the verification gate).

Do this edit when B12 closes, not before — the number depends on what actually shipped.
