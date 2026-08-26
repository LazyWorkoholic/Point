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

## Handoff

State after this episode: **the v1 backend is complete.** Input routes correctly,
attachments resolve with guards and visible costs, concurrency is honest, and
cancellation is clean.

### ⚠ Checkpoint — decide here
```
~3 weeks left → build B15, then the frontend, then B13. DROP B16 (SSH).
~6 weeks left → do everything.
```

Do not proceed until every check above passes.
