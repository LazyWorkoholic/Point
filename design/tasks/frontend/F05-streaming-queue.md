# F5 — Streaming, Queue, Cancel

**Phase:** P1 · **Batchable:** no · **Depends on:** F4 · **Backend:** B5 ✅, B12

## Purpose

The moment Point becomes an application. Tokens appear, a second message can be
composed while the first replies, and cancelling leaves something honest behind.

## Build

### Streaming render
- `task.token` appends to the streaming message's buffer; **only that message
  re-renders** (F3's cache covers the rest).
- Markdown re-rendered on a throttle (~30–60ms), not per token. An unclosed fence
  mid-stream must not flicker the block — render the raw tail plainly until the
  fence closes.
- `task.thinking_delta` renders in a dim collapsible block, collapsed by default.
- `task.progress` renders as `step 3/6` on the message header.
- The header animation runs **while streaming** and freezes when idle
  (`LAYOUT.md`), making it an activity indicator rather than decoration.

### Two lanes
```
TURN   sequential, streams inline into the transcript
TASK   concurrent; reserves a collapsed slot in the transcript at SEND time
       and fills it in place when it completes — nothing ever jumps
```
The slot is rendered by F3; this episode owns its state transitions
(`⟳ running 0:42` → `✓ done` → expandable).

### Queue
```
send while a reply streams  → dimmed pending bubble below the streaming reply
                              editable (ctrl+e) and discardable (esc) until it fires
queue full (depth 1)        → input locks WITH THE REASON SHOWN:
                              "queue full — esc to discard pending"
reply completes             → the pending message fires automatically
```
Agent tasks never enter this queue.

### Cancellation
- `ctrl+c` cancels the streaming turn. If nothing is streaming it is a **no-op**,
  not quit — quit is `ctrl+q`.
- On `task.cancelled`, keep the partial text and mark it: a dim `⚠ interrupted`
  footer plus a `[Retry]` action.
- If a message is queued, cancelling the current turn fires the queued one
  immediately.

### Terminal-event discipline
Every task must reach exactly one terminal state. The UI asserts this: a task in
a running state with no events for `stream_timeout` (config, default 120s) shows
`⚠ no response — [Retry]` rather than spinning forever. **A spinner that can
never stop is the worst failure mode here.**

### Interrupted rendering
The same rendering path serves user cancellation **and** backend crash recovery
(F2) — both produce a partial message with `interrupted=True`. One path, two
causes, so it cannot rot.

## Do NOT

- Build the task panel (F6) — this episode only renders in-transcript slots.
- Build result cards (F9).
- Lock the input except at queue capacity.

## Done when

1. Tokens stream visibly; a fenced code block does not flicker while still open.
2. Typing and sending during a stream produces a pending bubble that fires when
   the stream ends; `ctrl+e` edits it first; `esc` discards it.
3. At depth 1, a third send locks the input **with the reason visible**.
4. `ctrl+c` mid-stream leaves partial text marked interrupted, with Retry, and the
   app stays fully usable.
5. `@research x` reserves a slot immediately; chatting appends below it; the slot
   fills in place with no reordering.
6. A task emitting nothing for `stream_timeout` shows the no-response state.
7. The header animation runs only while something streams.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the app:
- Ask something long. While it streams, type and send a second message.
  **Expect:** a pending bubble, then it fires.
  **Fails if:** the input is dead with no explanation, or the two replies
  interleave.
- Send a third while one is pending. **Expect:** the input locks *with the reason*.
- Press `ctrl+c` mid-stream. **Expect:** partial text kept, `⚠ interrupted`,
  Retry present, app still usable.
- Press `ctrl+c` with nothing streaming. **Expect:** nothing happens — it must not
  quit.
- Send `@research <topic>`, then immediately chat normally.
  **Expect:** the research slot holds its position while chat appends below it.

```powershell
# 6. no-response state — needs a mock that emits tokens then stops
cd D:\Projects\Point\backend
$env:POINT_MOCK = "1"
cd D:\Projects\Point
go run .\frontend\cmd\point      # send with the "stalls" script
Remove-Item Env:\POINT_MOCK
```
**Expect:** after `stream_timeout`, `⚠ no response — [Retry]`, not an eternal
spinner.

```powershell
# 7. idle CPU — leave the app open, untouched, for 60 seconds
Get-Process point | Select-Object CPU
```
Take a reading, wait 60s, take another.
**Expect:** almost no change. **Fails if:** CPU climbs — the animation is not
freezing when idle.

## Handoff

State after this episode: Point is usable. Chat streams, concurrency is visible,
cancellation is clean, and interruption renders identically whether the user or a
crash caused it.

Do not proceed until every check above passes.
