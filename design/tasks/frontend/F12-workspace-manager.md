# F12 — Workspace Manager and Lifecycle

**Phase:** P1 · **Batchable:** no · **Depends on:** F6 · **Backend:** B15

## Purpose

The differentiator. Workspaces load on demand, unload when idle, and **never take
a running task down with them**.

## Build

### Workspace registry
```go
Register(WorkspaceDef{
    ID: "chat", Name: "Chat", LeaderKey: "c",
    New: NewChatWorkspace, MinSize: Size{50, 12},
})
```
Palette entries, leader bindings, the startup chooser and help text all derive
from this — the same pattern as the action registry, and the hook the parked
capability system will use (`TEMPLATES.md`).

### Lifecycle, from the data structure
```
NOT_LOADED    id absent from root.workspaces
LOAD          construct + Init()        on first switch to it
ACTIVE        the visible one
INACTIVE      loaded, not visible, lastActive tracked
UNLOAD        Suspend() → store bytes → delete key → GC frees the rest
```

### Suspend / Resume
`Suspend()` returns ~2KB: session id, scroll position, draft text and cursor,
open files, cursor positions, terminal mode. **Not messages, not file contents.**

`Resume(bytes)` restores position state, then `Init()` issues one query to
rehydrate content from the database. This is the **same code path** as opening a
week-old session and as recovering from a crash — one path, constantly exercised,
so it cannot silently rot.

Suspended state is written into `%LOCALAPPDATA%\Point\state.json` (debounced), so
it survives not just unload but a kill.

### Idle auto-unload — one timer PER workspace (D87)
```toml
[workspace]
idle_unload_minutes = 5      # 0 disables

[workspace.editor]
idle_unload_minutes = 60     # per-workspace override; the Editor is expensive
                             # to reload, so it should live longer
```

**Each loaded workspace has its own `lastActive`.** A `tea.Tick` walks whatever
is currently in the map — so the count is dynamic. Four workspaces or twelve, no
constant anywhere; a capability workspace added later inherits this for free
because it comes through the same registry.

**The active workspace never unloads.** Its timer only starts when you leave it.

Worked example, with a 5-minute timeout:
```
t=0:00  chat loaded, active
t=3:00  coding workspace opened → active. chat's timer STARTS now
t=3:30  data workspace opened   → active. coding's timer starts now
t=4:00  back to chat            → chat active, timer resets.
                                  data's timer starts now
t=8:30  coding unloads   (idle since 3:30)
t=9:00  data unloads     (idle since 4:00)
        chat never unloads while you are in it
```
Nothing unloads all at once. A single global timer would have unloaded three
workspaces simultaneously for no reason.

**Two affordances this requires**, or unloading feels like the app losing your
work at random:

1. **Idle state visible per workspace** — in the workspace switcher and the
   palette: `Chat · active`, `Editor · idle 3m of 5m`, `Data · idle 4m of 5m`.
2. **A grace warning at ~30s** — a toast: `Editor unloads in 30s — any key to
   keep`. Any keypress resets that workspace's timer.

**Unload proceeds even when the workspace has a running task** — the task is in
another process. On unload with a running task, show:

`unloading view — Research task continues in background`

That message is the demo beat. Do not replace it with a block.

### Manual unload and quit
- Unload current workspace: leader → `x`, routed through `Confirm` (UI-13) when
  buffers are dirty; skipped entirely when nothing is dirty and nothing is
  running.
- Quit: `ctrl+q`, routed through `Confirm` (UI-14) **listing the running tasks it
  will cancel** — quitting kills the backend, so tasks genuinely die here, unlike
  unload. This asymmetry must be visible in the wording.
- Backend-side: send `workspace.unload(id)` so Python releases its contexts (B15).

### Startup chooser (UI-12)
**Do not auto-load the last workspace.** Loading the Editor — file tree, project
context, open files — when the user only wants to ask a question wastes time and
memory.

```
╭─ Point ────────────────────────────────╮
│  Resume previous session?              │
│                                        │
│    [ Chat ]        [ Editor ]          │
│                                        │
│  last: Chat · "runtime bug" · 2h ago   │
╰────────────────────────────────────────╯
```
`state.json` holds `last_workspace` and per-workspace suspended state — but that
data existing does **not** mean the workspace is loaded. Loading happens only on
selection.

### Switching
Leader → `c` / `e`, and the palette. Switching to an unloaded workspace loads it;
switching away leaves it INACTIVE. All loaded workspaces coexist; only the active
one renders.

## Do NOT

- Cancel tasks on unload.
- Buffer backend events for unloaded workspaces — rehydration is from the DB.
- Auto-load anything at startup.
- Let the root read a workspace's internal fields.

## Done when

1. Startup shows the chooser; picking Chat leaves the Editor NOT_LOADED — verify
   the map has one key.
2. Unload a workspace with a running task → the message appears, the task
   continues and completes, and its result opens from the tray.
3. Resume rehydrates: transcript from the DB, scroll position and draft from the
   suspended bytes.
4. Idle timeout set to 1 minute → an inactive workspace unloads on its own and
   resumes correctly.
4b. **Per-workspace timers are independent**: with three workspaces loaded and a
   1-minute timeout, leaving each at a different moment unloads them at three
   different moments, not together. The idle readout counts down per workspace,
   and the 30s grace warning fires for each; any key resets only that one.
5. `ctrl+q` with a task running lists it and warns it will be cancelled; confirming
   persists partial output.
6. Kill the app without unloading, restart → drafts and open files return.
7. Go heap returns near its pre-load level over 20 load/unload cycles.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```
**Expect:** the chooser, not a workspace.

In the app: send `@research <topic>`, then leader → `x` to unload Chat.
**Expect:** `unloading view — Research task continues in background`, and the task
still listed in the tray.

```powershell
cd D:\Projects\Point\backend
uv run python cli.py tasks list
```
**Expect:** RUNNING or COMPLETED — never CANCELLED.

Set `idle_unload_minutes = 1`, switch to the Editor, wait.
**Expect:** Chat unloads silently; switching back restores transcript, scroll and
draft.

```powershell
# 6. survive a kill
Get-Process point -ErrorAction SilentlyContinue | Stop-Process -Force
go run .\frontend\cmd\point
```
**Expect:** the chooser, then the same open files and drafts.
**Fails if:** a draft or the open-file list is lost.

```powershell
# 7. heap — with pprof enabled behind a flag
go tool pprof http://localhost:6060/debug/pprof/heap
```
**Expect:** heap returns near baseline after 20 cycles. (Go **does** release,
unlike CPython — so unlike B15, a real reduction is expected here.)

## Handoff

State after this episode: workspaces are lazy, unloadable, and independent of
task execution. This is the property most comparable tools do not have, and it is
now structural rather than aspirational.

Do not proceed until every check above passes.
