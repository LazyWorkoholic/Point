# F11 — Memory Popup

**Phase:** P2 · **Batchable:** no · **Depends on:** F7 · **Backend:** B13

**Ship a stub before B13 exists.** The popup opens, shows accepted records saved
from cards (which work from B12/F9 onward), and shows an empty Proposals tab. The
review machinery lands when B13 does.

## Purpose

The one place memory is reviewed, so no fact can enter the system by a path the
user never sees.

## Build

### The popup (UI-02)
The F7 `Popup` again — its **fifth** consumer.

```
╭─ Memory ───────────────────────────────────────────╮
│ =PENDING (3)=   SESSION   PROJECT   GLOBAL         │
│ filter: ___                                        │
│                                                    │
│ ⌸ Runtime uses stdin/stdout, not HTTP              │
│   research · Task #12 · 3d ago · ipc, architecture │
│                                                    │
│ ⌸ Ollama is capped at one concurrent task          │
│   user_card · "runtime bug" · 1d ago · limits      │
╰────────────────────────────────────────────────────╯
  ⏎ attach   a accept   r reject   e edit   x forget
```

- Tabs are **scopes plus a Pending tab**; the Pending count is the badge shown in
  the right pane.
- Every row shows **provenance**: source, origin session/task, age, tags. This is
  what makes "why does it think that?" answerable, and the reason `source` and
  `origin_*` exist in the schema.
- Filter is live and searches content and tags.

### Actions
```
a accept    pending → accepted        (only meaningful on the Pending tab)
r reject    pending → rejected        (kept for audit, never retrieved)
e edit      opens the content in a scratch buffer; save re-writes it
x forget    deletes the record AND all its scope bindings
⏎ attach    writes a context_attachments row → the fact enters this conversation
b bind      add another scope binding (session / project / global)
u unbind    remove one binding — NEVER deletes the record
```
`x forget` and `r reject` route through `Confirm` (F7).

### Manual retrieval is the default path
There is no automatic per-turn injection in v1. Memory helps when you attach it.
The popup is therefore a **working surface you open mid-conversation**, not a
settings screen — so `⏎ attach` is the primary action and the popup **closes
after attaching**.

### Proposal badge
`memory.proposed` events increment a badge in the right pane
(`3 pending memories`). Async and non-blocking — a proposal must never interrupt
a stream or steal focus.

### Global scope is deliberately awkward
Global is a tab, but `b bind → global` sits one level deeper in the submenu than
session and project. Global should hold about twenty facts, not two hundred, and
friction is the mechanism.

### Empty and loading states
No pending → `nothing to review`. Empty scope → `no memories in this scope yet`,
with a one-line hint that results can be saved from a task card. Backend without
B13 → the Pending tab shows `review lands with the memory system`, not an error.

## Do NOT

- Implement search ranking, extraction, or summarization (B13).
- Auto-accept anything.
- Write `accepted` records for anything except an explicit accept.
- Show global memory to an SSH guest, or offer them accept (B16 permissions).

## Done when

1. The popup is the same component as the palette — one implementation.
2. A card-saved record appears immediately under the correct scope with
   `source=user_card`.
3. With B13 present: a pending proposal can be accepted, rejected, and edited,
   and the badge count follows.
4. `⏎ attach` adds it to context — a memory section with a real token cost appears
   in the next turn's context report.
5. `u unbind` removes one binding and leaves the record; `x forget` removes the
   record and every binding.
6. A record bound to two scopes appears in both tabs but attaches **once**.
7. As a guest (B16), accept is unavailable and the Global tab is hidden.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

Save a research result to project from its card, then open the memory popup.
**Expect:** the record under PROJECT, showing source, origin and age.

Attach it, send a message, then open the context card (F6).
**Expect:** a `memory` section with a non-zero cost.

```powershell
# 5. forget removes bindings too
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select count(*) from memory_scopes where memory_id='<id>';"
```
Run before and after `x forget`. **Expect:** non-zero, then `0`.

```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select status, source from memory_records order by rowid desc limit 3;"
```
**Expect:** card saves are `accepted|user_card`; extractor output is
`pending|auto_extractor`.

## Handoff

State after this episode: memory has exactly one review surface, every record
carries its provenance, and nothing enters context without an explicit act.

Do not proceed until every check above passes.
