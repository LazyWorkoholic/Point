# B15 — Lifecycle & Resource Management

**Phase:** P2 · **Batchable:** no · **Depends on:** B12

**Demo-critical.** The crash-recovery beat is the strongest moment in the v1
demo. Build this before the other P2 work.

## Purpose

Release what is no longer used, refuse to release what is in use, and recover
cleanly when the backend dies.

## Build

### WorkspaceContext (backend-side unload)
```python
class WorkspaceContext:
    def close(self) -> ReleaseReport: ...
```
- Keyed by workspace id. `workspace.unload(id)` command from Go.
- Drops: file tree, open buffers, symbol/parse caches.
- `ProjectContext.close()` drops cached project data and the B7 file index.
- Provider clients: close idle httpx clients / connection pools.
- Agents are cheap; their **caches** are not — drop those.

### The refusal rule
A registry of live references: anything held by a `RUNNING` or `QUEUED` task is
**not eligible** for release. `close()` on such a context is a no-op that
**reports what it is holding**. This one rule is what makes unload safe.

### Heartbeat and liveness
`ping` every 5s while idle. Two missed pings marks the connection dead. This is
what catches a **hung** backend — process-exit detection never will.

**Change the suppression rule: emit `ping` unconditionally.** (D-heartbeat in
`decisions.md`.) `Runtime._heartbeat_loop` currently skips the ping whenever
`self.tasks.active_count() > 0`, on the reasoning that tokens already prove
liveness. That holds for a streaming reply and **fails for a slow tool call**,
which is silent by nature — `web_search` against a slow endpoint emits nothing
between `ToolStarted` and `ToolCompleted`. During exactly that window the
backend sends no tokens and no pings, so from Go a working tool call and a
deadlocked process look identical.

F2's client compensates by stretching its watchdog by 60s whenever it believes a
task is in flight (`busyGrace` in `frontend\internal\protocol\client.go`), which
means a genuine hang during a task takes up to ~72s to detect instead of ~12s.
That is a workaround for a backend decision, and it belongs here rather than
there. When the ping becomes unconditional, **delete `busyGrace`** and say so in
this episode's handoff so the frontend side gets simplified with it.

### Crash recovery (backend half)
- On startup, find tasks left `RUNNING` in the DB from a previous process. They
  are not running. Mark them `FAILED` with `code=backend_died`, preserving any
  partial assistant message with `interrupted=True`.
- `session.events_since(session_id, seq)` → the events needed to rebuild UI state
  after a reconnect.

  **This episode owns the command, and F2 is already waiting on it.** There is no
  member of `IncomingEventType` for it in `backend\runtime\events.py` and no
  handler, so F2 could not call it. F2's recovery ladder was therefore built
  without the replay rung: `Client.afterReconnect` in
  `frontend\internal\protocol\client.go` is a **logged no-op seam**, and every
  task that was in flight when the backend died is marked FAILED/interrupted
  rather than resumed. Partial text is kept; nothing is silently lost, but
  nothing is recovered either.

  Add the incoming event, the handler, and the outgoing reply here, then delete
  the no-op and fill the seam. The frontend change is one function; the reason
  it was left open is that inventing backend surface from a frontend episode is
  exactly what CLAUDE.md forbids.
- `tasks.list()` must be correct immediately after a restart.

### Graceful shutdown
On SIGTERM / `Ctrl+C` / quit: stop accepting new work, cancel running tasks with
partial persistence, release contexts, close the DB cleanly, exit.
A second signal exits immediately.

Windows note: `main.py` already reads stdin on a background thread. Make sure
that thread is a **daemon** or explicitly signalled, or the process will hang on
exit waiting for a blocking `readline`.

### The `Shutdown` event — this episode owns it

`Runtime._handle_shutdown` exists since B1 and is a stub in the worst way. Today
it sets `RuntimeStatus.STOPPING`, cancels active tasks, sets `STOPPED`, and then:

- **emits no ack** — which breaks `project_idea.md` §4's "every COMMAND gets
  exactly one ack";
- **does not stop the process** — the loops keep running;
- **rejects everything afterwards**, because `receive()` gates on
  `is_accepting_events()`. The runtime becomes a brick that never exits.

`Shutdown` **quiesces, it does not terminate.** The handler is the same code path
as the signal path above, entered from the protocol side instead of the OS:

1. Set `STOPPING` — stop accepting new work. `Shutdown` itself stays accepted, so
   a second one is a no-op ack rather than an error.
2. Cancel running tasks and let their sinks persist partials, exactly as a
   `CancelTask` does. Wait for terminal events, bounded by a timeout.
3. Release contexts, close the DB cleanly.
4. **Ack** — one terminal `shutdown.complete` envelope, emitted and flushed
   *before* the outbox writer stops. This is the last thing Go ever reads.
5. Set `STOPPED` and let the process exit through the normal teardown.

B6 deliberately fixed only the **stdin-EOF hang** — the `asyncio.gather` that
never returns once Go closes the pipe — and left the `Shutdown` semantics alone,
because quiescing correctly needs the context registry and the refusal rule that
this episode builds. Do not split them again.

### Bounded-growth harness
`cli.py bench unload-cycles --n 20`: load and unload a workspace context N times,
reporting object counts and cache sizes via `tracemalloc`.

**Report bounded growth, not freed RSS.** CPython does not reliably return arenas
to the OS, so a "watch memory drop" demo would embarrass you. Stable object
counts across cycles is the measurable, truthful claim.

## Do NOT

- Kill tasks when a view unloads. Unloading a view is a UI event; the task is in
  another process.
- Buffer events in memory for unloaded views — rehydration is from the DB.
- Claim freed memory anywhere in logs, UI, or docs.

## Done when

1. Unload a workspace whose task is running → the task continues and completes;
   its result is retrievable afterwards.
2. `close()` on a context holding a running task's resources is a no-op and
   reports what it holds.
3. Kill the Python process mid-stream → on restart, the previously-RUNNING task
   is `FAILED` with `backend_died` and its partial message survives with
   `interrupted=True`.
4. `events_since(session, seq)` returns exactly the missing events, in order,
   with no gaps.
5. 20 load/unload cycles: object counts and cache sizes return to baseline. RSS
   may not drop — that is expected and documented.
6. `Ctrl+C` during a stream leaves a consistent DB and a persisted partial
   message, and the process actually exits (no hanging stdin thread).
7. A `Shutdown` event during a stream returns exactly one `shutdown.complete`
   ack, persists the partial message, closes the DB, and exits — reaching the
   same end state as `Ctrl+C`. A second `Shutdown` acks again and does not error.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

# 1. unload does not cancel
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "@research <topic>" }
Start-Sleep -Seconds 3
uv run python cli.py workspace unload chat
Start-Sleep -Seconds 30
uv run python cli.py tasks list
```
**Expect:** the task RUNNING or COMPLETED — **never** CANCELLED.

```powershell
# 3. crash recovery
Start-Job { cd D:\Projects\Point\backend; uv run python cli.py send "write a long essay" }
Start-Sleep -Seconds 3
Get-Process python | Where-Object { $_.Path -like "*Point*" } | Stop-Process -Force
# then restart and inspect
uv run python cli.py tasks list
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select interrupted, length(content) from messages order by rowid desc limit 1;"
```
**Expect:** the task shown FAILED with `backend_died`; a partial message with
`interrupted = 1` and a non-zero length.

If that `Get-Process` filter is too broad on your machine, use:
```powershell
Get-CimInstance Win32_Process -Filter "Name='python.exe'" |
  Where-Object { $_.CommandLine -like "*main.py*" } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
```

```powershell
# 5. bounded growth
uv run python cli.py bench unload-cycles --n 20
```
**Expect:** flat object counts across cycles.
**Fails if:** counts climb monotonically — that is a real leak, and it is
distinct from RSS not shrinking.

```powershell
# 6. clean shutdown
uv run python main.py
# Ctrl+C during a stream
```
**Expect:** the process exits within a second or two.
**Fails if:** it hangs — the stdin reader thread is not a daemon.

```powershell
# 7. Shutdown quiesces rather than bricking
uv run python cli.py send "write a long essay" --shutdown-after 2
```
**Expect:** one `shutdown.complete` ack, a persisted partial message with
`interrupted = 1`, and the process exits. Before B15 the runtime set itself to
STOPPED, acked nothing, and stayed alive rejecting every subsequent event.
**Fails if:** no ack arrives, or the process is still running afterwards.

## Handoff

State after this episode: the backend releases what it can, refuses to release
what it must not, and survives being killed mid-stream with the conversation
intact. Combined with the frontend's auto-restart (F2), Point is crash-tolerant
rather than crash-free.

Record the exact PowerShell command that reliably kills only Point's Python
process on this machine — you will use it in the demo.
