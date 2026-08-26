# F9 — Task Tray, Result Cards, Diff Card

**Phase:** P1 · **Batchable:** no · **Depends on:** F7, F8 · **Backend:** B11, B12

## Purpose

Where background work becomes visible and actionable — and where an agent's
proposed code change is reviewed before it touches anything.

## Build

### Task tray (UI-05)
`F1` and a palette action open it. The `Popup` component again — this time with
sections rather than tabs:

```
╭─ Tasks ────────────────────────────────────╮
│ filter: ___                                │
│ ─ Running ──────────────────────────────── │
│  ⟳ Research  tokio vs asyncio  0:42  3/6   │
│ ─ Queued ───────────────────────────────── │
│  ⋯ Research  <topic>                       │
│ ─ Finished ─────────────────────────────── │
│  ✓ Research  <topic>   12.4k tok · 1m03s   │
│  ⚠ Filler    task.py   failed: tool_timeout│
╰────────────────────────────────────────────╯
```

- **Global**: tasks from every workspace, since the root owns the list. A task
  whose originating view is unloaded still appears and still opens.
- `⏎` on a finished task opens its result card; on a running task, offers cancel.
- Reachable from every workspace, including the Editor.

### Result card (UI-06)
```
╭─ Research · tokio vs asyncio ── done · 12.4k tok · 1m03s ─╮
│                                                           │
│  <rendered markdown result, scrollable>                   │
│                                                           │
│ ───────────────────────────────────────────────────────── │
│ a attach full   s attach summary   m save to memory ▸     │
│ r re-run        d discard          esc close              │
╰───────────────────────────────────────────────────────────╯
```

- **Attach and save are independent axes.** Attach controls this conversation's
  context (`full | reference | detached`); save controls durable memory
  (`project | global`). Both, either, or neither.
- Attaching writes a `context_attachments` **reference**, never a copy — so the
  rendered text always reflects the current source.
- `m` opens a small scope submenu. **Session is preselected**, project is offered
  only when a project is open, global sits one level deeper on purpose.
- Saving from a card creates an **accepted** record (`source=user_card`) — the
  click *is* the approval. It is **not** a second pending proposal.
- The header shows current attach state: `⚲ in context (summary)` or
  `⌸ reference only`.
- `d discard` removes the card from view; it does **not** delete the task result.

### Diff card (UI-07)
```
╭─ Proposed change · config.py ─────────────────────────────╮
│  12    def parse_config(path):                            │
│  13 +      if not path.exists():                          │
│  14 +          raise ConfigError(path)                    │
│  15        with open(path) as f:                          │
│ ───────────────────────────────────────────────────────── │
│ ⏎ apply    e edit    d discard                            │
╰───────────────────────────────────────────────────────────╯
```

- The backend returns a **rewritten region**, not a patch; **Go computes the diff
  locally** — models miscount line numbers and emit patches that will not apply.
- `e` opens the **proposed result** in a scratch editing buffer, not the patch.
  The diff re-renders live against the original as you edit. Editing a new
  version is typing; editing a patch is punishment.
- `⏎` applies: in the Editor, into the **unsaved buffer** so `ctrl+z` undoes it as
  **one** operation (F13/F15). From Chat, apply through the editor buffer if the
  file is open, otherwise offer to open it. **Never a direct disk write from an
  agent path, in any phase.**
- If the file changed on disk since the region was generated, **refuse to apply**
  and say so (the B7 stale check), offering re-generate.
- Same card, same keys, in both workspaces.

### Notifications
A task completing while you are elsewhere shows a brief toast
(`✓ Research done — F1 to view`), auto-dismissing after ~4s. Toasts never steal
focus and never cover the input.

## Do NOT

- Write files directly from any code path in this episode.
- Ask the model for a unified diff.
- Delete task results on discard.
- Implement the memory review queue (F11).

## Done when

1. The tray lists tasks from every workspace and opens results whose originating
   view has been unloaded.
2. Attach and save are independently exercisable: attach-summary **and**
   save-to-project on one card, both verifiable in the DB.
3. Editing a memory later changes what an attached reference renders — proving it
   is a reference, not a copy.
4. Saving from a card produces `status=accepted`, `source=user_card` — not
   pending.
5. Diff apply lands the **edited** text, not the original.
6. Applying in the Editor is undone by a **single** `ctrl+z`.
7. Modify the file on disk after generation, then apply → refused with a reason.
8. Discarding a card leaves the task result retrievable from the tray.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the app: send `@research <topic>`, switch to the Editor while it runs, press
`F1` when it finishes. **Expect:** the card opens from the Editor.

On the card press `s` (attach summary), then `m` → project. Then:
```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select mode from context_attachments order by rowid desc limit 1;"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select status, source from memory_records order by rowid desc limit 1;"
```
**Expect:** `reference` and `accepted|user_card`.

Then: cursor-fill in the editor → diff card → `e` → change a line → `⏎` apply →
`ctrl+z`.
**Expect:** the edited version applied, then fully undone in one press.
**Fails if:** the original is applied instead of your edit, or undo takes several
presses.

```powershell
# 7. staleness — with a diff card open, in a second tab:
Add-Content D:\Projects\Point\backend\runtime\task.py "# external"
```
Then press `⏎` on the card. **Expect:** refused, with a re-generate offer.

## Handoff

State after this episode: background work is visible and actionable from
anywhere, results carry honest cost labels, and every agent-proposed change is
reviewed, editable, and reversible before it touches a buffer.

Do not proceed until every check above passes.
