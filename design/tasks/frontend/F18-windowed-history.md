# Episode F18 — Windowed History (bounded resident window)

**The chat transcript holds a bounded, contiguous window of messages around the
scroll position — not the whole session. Scroll past an edge, fetch the next
batch and prepend/append; grow past the bound, evict from the far edge and let
the GC reclaim it. Memory stays flat regardless of session size AND scroll
depth. Re-fetching from local SQLite is cheaper than holding, which is the whole
reason aggressive eviction is correct here.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/frontend/F18-windowed-history.md`) claims reality and
> completion. Code wins on any disagreement.

**Model:** Opus, high effort. This is a shared-contract episode touching the same
Suspend/Resume blob F4 introduced `Drafts` into, the streaming path F5 owns, and
the layout/scroll machinery F3 built. Same tier as B6, B8, F1+F2 — the
cross-cutting episodes have earned Opus every time; do not run this on Sonnet.

**Depends on:** B18 (the keyset query surface) built and verified. F5 (streaming,
queue, cancel) built — F5 must define "a message arrives" against full residency
first, so F18 makes it windowed **once** rather than F5 reasoning about
windowing while already carrying a heavy episode.

**Must land before:** F12 (Workspace Manager + Lifecycle). F12 owns Suspend/
Resume, and F18's scroll anchor lives in that blob. If F12 ships first, F18 has
to migrate a blob schema designed without knowing about the anchor — the exact
B11 `AgentConfig.background` retrofit trap. Ordering is therefore **F5 → … → F18
→ … → F12**.

---

## Why this episode exists, and why it is NOT just "call the new query"

F3's renderer already made *rendering* O(rows). B18 made *fetching a slice*
cheap. What is still true without F18: the frontend holds the **entire** session
in memory (F3 fetches it all via `GetSession` at open). On a low-end device
running a local model that is already eating 2–4 GB, Point permanently competing
for that memory is the wrong design — and the background full-fetch fires right
when a generation is likely starting, contending for a CPU that has nothing to
spare.

F18 makes message history obey the same rule the workspace-unload feature (F4
drafts, F12 lifecycle) already commits to: **memory is bounded and reclaimable,
and re-deriving state from SQLite is cheap.** History becomes the one subsystem
that currently opts out of that rule; F18 makes it comply.

The hard part is **not** the fetch. It is four consumer-side problems below.
Budget accordingly.

---

## Build

### 1. The resident window — a contiguous run, not a cache with holes

- The chat model holds a contiguous slice `resident = [oldestResidentSeq,
  newestResidentSeq]`, sized to a multiple of a screenful, governed by
  `ui.scrollback` (the config key F3 already added). One invariant:
  **`resident` is always contiguous.** Never a set of ranges with gaps — that is
  a deliberately simpler model than a hole-y cache and keeps F3's renderer's
  input shape unchanged (it still renders "a window," just of a smaller,
  managed set).
- On session open: `GetSession(message_limit = residentBound)` gives the tail +
  total count. Do **not** fetch the whole session.

### 2. Fetch on scroll, prepend/append, preserve anchor

- Scroll up past `oldestResidentSeq` → `SessionMessages(before=oldestResidentSeq,
  limit=batch)`, prepend the returned (oldest-first) batch.
- Scroll down past `newestResidentSeq` (only possible after prior eviction from
  the tail) → `SessionMessages(after=newestResidentSeq, limit=batch)`, append.
- **Scroll-anchor preservation on prepend is the bug most likely to ship
  broken.** When N older messages arrive above the viewport, the view must shift
  by their **rendered height**, not by message count, or the content jumps under
  the user. Because F3's renderer is row-windowed, the anchor must be
  `(anchorSeq, rowOffsetWithinThatMessage)` — an absolute row index is
  meaningless the instant anything is inserted above.

### 3. Eviction that never fights the user

- Once `resident` exceeds its bound, evict from the **far edge** (the end away
  from the scroll direction) and let Go's GC reclaim it. `ui.scrollback` governs
  the bound.
- **Never evict** the focused message or the anchor message. Re-fetching a
  dropped batch on scroll-back is correct and cheap (local SQLite keyset seek,
  almost certainly in the OS file cache) — this asymmetry (re-fetch cheaper than
  hold) is what makes aggressive eviction right here and would make it insane in
  a client/server app.

### 4. `G` (jump to bottom) when the tail is not resident

- Today `G` is instant because everything is in memory. With eviction the newest
  messages may not be resident, so `G` becomes a fetch (`GetSession(message_limit
  = residentBound)` or `SessionMessages` tail). This is a real behaviour change;
  F18 owns it explicitly — show the same "loading" affordance a scroll-fetch
  uses, and re-pin on arrival.

### 5. Streaming into a non-resident tail (the F5 interaction)

- If a reply is generating while the user has scrolled deep into history, its
  tokens arrive as **events** (F5's stream), not as fetches. Rule: **an actively
  streaming message is always resident**, held in a tail buffer regardless of
  scroll position, and merged into the window when the user returns to the
  bottom. Otherwise a hole opens in a supposedly-contiguous window.
- The "new content below while you're scrolled up" indicator (if F5 didn't
  already add one) belongs here: the user must know the tail grew.

### 6. Backpressure — the low-end-device rule

- While a task is actively streaming, **defer** history fetches. A local model is
  CPU-bound; stealing cycles to hydrate scrollback the user isn't looking at
  degrades tokens/sec on the thing they *are* looking at. One check against "is
  a task in flight" before dispatching a fetch. Queue the fetch, run it when the
  stream settles.

### 7. Batch size as config, not a constant

- `ui.historyBatch` (or similar) next to `ui.scrollback` in `config.toml`,
  defaulting conservatively. The right batch differs between a fast machine and a
  low-end laptop with a model loaded; it must be tunable without a rebuild.

### 8. The Suspend/Resume anchor (the F12 forward-contract)

- Suspend's blob (the F4 `Drafts`-carrying shape) gains a scroll anchor:
  `{anchorSeq, rowOffset, pinnedToBottom}`.
- **Resume must degrade, not fail.** If `anchorSeq` no longer exists (content
  changed, future archival), fall back to the bottom rather than erroring. The
  restore path can never be the thing that breaks a resume.
- **`pinnedToBottom` flag.** If the user was at the bottom when suspended, resume
  to the *current* newest message, not the old one — a session that received
  messages while unloaded (F5 background task, or an SSH peer) must resume
  pinned to real bottom, not stranded at a stale message with unread content
  below. This flag only earns its keep once F5 streams into unloaded workspaces,
  which is exactly why it is easy to get wrong later and cheap to specify now.

---

## Do NOT

- **Do not use a cache-with-holes model.** Contiguous resident window only. Holes
  make eviction, anchor math, and the renderer's input all harder for no benefit
  at this scale.
- **Do not add backend surface.** B18 gave you `SessionMessages` and
  `GetSession(message_limit)`. If you need a third backend call, stop — either
  the design is wrong or it belongs in a backend episode, not here.
- **Do not paginate on anything but `seq`.** Anchor stores `seq`, fetches key on
  `seq`. Not row index, not `created_at`.
- **Do not background-fetch the whole session** "to make scrolling instant."
  That is the exact anti-pattern this episode exists to remove; it reintroduces
  the memory ceiling on the device that can least afford it.
- **Do not evict the anchor or focused message**, and do not let a fetch fire
  while a task streams (backpressure).
- **Do not let a streaming message fall out of residency.** Tail buffer holds it
  regardless of scroll position.
- **Do not change F12's blob schema in a way F12 can't adopt** — add the anchor
  as a narrow, F18-owned field with a Handoff note, the D97/`CardSlot`/`Drafts`
  precedent, so F12's real workspace-state type adapts to it rather than being
  identical.

---

## Done when

1. Opening a session with 50,000 seeded messages is no slower than one with 10
   (measured, not asserted) — no full-session fetch occurs at open.
2. `POINT_MEMLOG` shows heap **flat** across: (a) a full scroll from bottom to
   top of a 50k-message session, and (b) repeated up/down scrolling — flat
   regardless of session size *and* scroll depth. This is the episode's
   defining proof and the instrument F3 already built for exactly this.
3. Scrolling up prepends older batches with **no content jump** — the message
   under the cursor stays put as older content loads above.
4. Eviction keeps memory bounded; scrolling back down re-fetches dropped batches
   correctly and cheaply, with no visible gap or duplicate.
5. `G` re-pins to true bottom even when the tail was evicted, fetching if needed.
6. A reply streaming while the user is scrolled deep stays resident, its tokens
   keep arriving, and it merges correctly when the user returns to bottom — no
   hole in the window.
7. History fetches defer while a task streams (verify tokens/sec is not degraded
   by a concurrent scroll-fetch).
8. Suspend then Resume restores the exact scroll position; if the anchor is gone,
   it degrades to bottom without error; if the user was pinned to bottom, Resume
   lands on the *current* newest message.
9. `ui.scrollback` and `ui.historyBatch` both take effect from `config.toml`.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$scratch = "$env:TEMP\point-f18-verify"
Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$scratch\roaming","$scratch\local" | Out-Null
$env:APPDATA = "$scratch\roaming"; $env:LOCALAPPDATA = "$scratch\local"

cd D:\Projects\Point\backend
uv run python cli.py migrate
uv run python cli.py seed-session --messages 50000

cd D:\Projects\Point
$env:POINT_MEMLOG = "2"
go run .\frontend\cmd\point
# in a second tab:
# Get-Content "$env:LOCALAPPDATA\Point\logs\frontend.log" -Wait -Tail 5 | Select-String memlog
```

Eyes-on, against the 50k session:
- **Instant open**, pinned to bottom. No multi-second load.
- **Scroll bottom → top continuously.** Memlog `heap_alloc` trends flat (noise,
  no climb) across the whole traverse and back — the same measurement F3's memory
  investigation used, so it is directly comparable.
- **Prepend has no jump** — hold a message under your eye at the top edge, scroll
  up one batch, confirm it doesn't leap.
- **`G` from deep in history** re-pins to the true newest message.
- **Send a message, scroll deep while it streams** (needs F5): tokens keep
  landing, the streaming message is never lost, merges on return to bottom.
- **Suspend/Resume** (needs F12 for the real path; until then exercise the blob
  round-trip directly): position restored; anchor-missing degrades to bottom;
  pinned-to-bottom resumes at current newest.

If memory climbs with scroll depth, the window is not evicting — that is the
failure this episode exists to prevent, so treat a non-flat memlog as a hard
stop, not a nit.

---

## Handoff — what later episodes inherit

- **F12:** the Suspend blob now carries `{anchorSeq, rowOffset, pinnedToBottom}`
  alongside F4's draft fields. F12's real workspace-state type must adopt this
  shape (adapt, don't redefine). Resume-degrades-to-bottom and the
  pinned-to-bottom rule are behaviours F12 must preserve, not re-derive.
- **F16 (polish):** the loading affordance for a scroll-fetch and for `G`-fetch
  is functional here, not final; F16 may restyle it.
- **B18's parking-lot pointer is now resolved** — update
  `design/tasks/backend/INDEX.md` to note pagination is implemented (B18) and
  windowed on the frontend (F18).
- **Not built here:** archival/summarization of old messages (B13), semantic
  retrieval (B13). F18 only makes existing history cheap to hold and read in
  slices.
