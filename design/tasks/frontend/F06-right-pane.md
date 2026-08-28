# F6 — Right Pane

**Phase:** P1 · **Batchable:** no · **Depends on:** F5, **F7** · **Backend:** B8, B12

F7 comes first — the expanded context card is a popup, and building a throwaway
overlay here would be wasted work.

## Purpose

Ambient awareness: what it costs, what is running, where you have been. Contains
the single most differentiating screen in Point — the context composition card.

## Build

### Composition
Three **independently bordered** rounded boxes, stacked with no gap between them,
fixed 32 columns (26 at 90–119 width). `Compute` returns **one rect per card with
different heights** — Status short, Task medium, Sessions taking the remainder.

> **Inherited from F1 — `layout.CardSlot`.**
> `Compute` cannot return one rect per card without being told how many cards
> there are and what each wants, and it must not know what a "Status card" is.
> F1 therefore added a deliberately narrow type:
>
> ```go
> type CardSlot struct {
>     MinHeight int   // rows this card wants, borders included
>     Flex      bool  // may absorb leftover rows; the last Flex card gets them
> }
> ```
>
> `layout.State.RightCards []CardSlot` is what the workspace hands `Compute`,
> and the distribution rule is already implemented and tested: every card gets
> its `MinHeight`, the leftover goes to the last `Flex` card, and when the pane
> is too short **trailing cards are dropped to a zero rect rather than every
> card being squeezed below the three rows a bordered box needs**.
>
> This follows the B8 `ContextPolicy` precedent: F1 needed a shape that F6
> formally owns, so it defined a narrower type under a different name instead of
> claiming F6's. **F6's real card type must be adaptable to `CardSlot`, not
> identical to it** — the card owns its title, its content and its state; the
> slot owns only its height appetite. Do not widen `CardSlot` to carry rendering
> concerns; `Compute` is pure and has no business knowing them.
>
> An open right pane with an empty `RightCards` renders no pane at all, and
> `Main` reclaims the width. That is intentional — an open pane with nothing in
> it is not a layout the app can produce.

```
╭─ Status ─────────╮
│ ████████░░░░     │
│ 12.4k / 32k      │
│ ollama qwen3:8b  │
│ ⌂ local · 4.7GB  │
╰──────────────────╯
╭─ Task ───────────╮
│ ⟳ Research  0:42 │
│   step 3/6       │
│ ⋯ Blog    queued │
│ ✓ Data      done │
╰──────────────────╯
╭─ Sessions ───────╮
│ • runtime bug    │
│ • bubbletea la…  │
│ • provider erro… │
╰──────────────────╯
```

**No CPU/RAM/GPU** — cross-platform pain, misleading numbers, low value. The one
system-ish line worth showing is free: when a local model is loaded,
`ollama · qwen3:8b · 4.7GB` from Ollama's own API, with a `⌂` marker in a distinct
dim role so you always know computation is happening on your machine.

### Status card
Order of priority, top to bottom, dropping from the bottom as height shrinks:
```
1. context fill bar + numbers      always
2. tokens / cost                   if a row is available
3. provider · model · local marker if a row is available
```
Driven by `usage.updated` and `context.report` events — **push, never polling**.
Cost is **omitted, not zeroed**, when pricing for the model is unknown.

### Context composition card (UI-09)
Expanded from Status via `⏎` or click. A rounded overlay, title on the edge:

```
╭─ Context · 12.4k / 32k ──────────────────╮
│ system prompt        420  ▓               │
│ attachments (2)    3,100  ▓▓▓▓▓           │
│ history (18 msgs)  8,600  ▓▓▓▓▓▓▓▓▓▓▓▓    │
│ memory (0)             0                  │
│ ──────────────────────────────────────    │
│ free              19,600                  │
│ next turn evicts: oldest 4 messages       │
╰───────────────────────────────────────────╯
```

Straight from B8's `context.report` — **no second implementation of the maths**.
This costs almost nothing to build and no comparable tool shows it. Treat it as
demo-critical.

### Tasks card
- States use the `running` / `queued` / `done` / `error` roles.
- Queued entries must reflect **real backend queueing** (B12). The UI may never
  show three running while the provider processes one.
- `⏎` on a finished task emits an "open card" message (F9 implements the card).
- **Global list**: tasks from every workspace, because the root owns it.

### Sessions card
Recent sessions, titles truncated with `…` by **display width**, active one
marked. `⏎` switches. No search here — that is the palette's job.

### Toggle and reflow
- A registered action plus a leader binding; the left column reflows to full
  width when hidden, **no leftover gap**.
- Auto-hides below 90 columns; still openable as an overlay.
- **Stop subscribing to updates when hidden** — no subscribers, no ticks.

### Empty and loading states
No tasks → dim `no background tasks`. No sessions → dim `no sessions yet`.
Backend down → dim `—`, never a zero that looks like real data.

## Do NOT

- Poll for status; everything is event-driven.
- Add system metrics.
- Implement result/diff cards (F9) or the memory popup (F11).
- Give the three cards equal heights.

## Done when

1. Context fill updates live as a reply streams and matches `context.report`.
2. The expanded card's sections sum to the total and name what will be evicted.
3. Three concurrent `@research` with Ollama capped at 1 shows **1 running, 2
   queued** — matching `cli.py tasks list` exactly.
4. Toggling the pane reflows the left column with no leftover gap at any width.
5. Below 90 columns the pane hides automatically and the overlay still opens.
6. With the pane hidden, no status/usage subscriptions remain active.
7. Killing the backend shows `—` placeholders, not stale or fake numbers.
8. The three cards have visibly different heights and each has its own border.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```
Send a message and watch the fill bar move. Expand the Status card mid-conversation.
**Expect:** section token counts that add up, and an eviction prediction.

```powershell
# 3. concurrency honesty — with limits.ollama.max_concurrent_tasks = 1,
# fire three @research invocations in the app, then in a second tab:
cd D:\Projects\Point\backend
uv run python cli.py tasks list
```
**Expect:** the pane and the CLI agree exactly.
**Fails if:** the UI shows three running.

```powershell
# 7. backend down
Get-CimInstance Win32_Process -Filter "Name='python.exe'" |
  Where-Object { $_.CommandLine -like "*main.py*" } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
```
**Expect:** `—` placeholders, not the last known numbers frozen in place.

Resize to 85 columns. **Expect:** the pane hides; the overlay still opens.

## Handoff

State after this episode: the right pane gives honest ambient state, and the
context card exposes the one thing that is genuinely hard to see in any AI tool.

Record: the actual per-card heights `Compute` chose, and whether the layout
breakpoints needed raising once real borders and gaps were rendered.

Do not proceed until every check above passes.
