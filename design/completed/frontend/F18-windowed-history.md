# Episode F18 — Windowed History (bounded resident window) ✅

**The chat transcript holds a bounded, contiguous run of messages around the
scroll position instead of the whole session. Scroll past an edge, fetch the
adjoining batch and prepend or append; grow past the bound, evict from the far
edge. Heap stayed in a ~2.0–2.4 MB band across minutes of scrolling a
50,000-message session, sending into it, and scrolling away mid-stream — flat
with respect to session size AND scroll depth, which is the episode's defining
proof. Six defects were found, every one of them by this episode's own tests,
and every one proven by reverting the fix to watch its regression fail.**

> Verified by Aryan on 2026-10-09 against
> `tasks/frontend/F18-windowed-history.md`'s nine "Done when" criteria, in a
> scratch `%APPDATA%`/`%LOCALAPPDATA%` profile per CLAUDE.md's env-var-override
> pattern — the real profile was never named in any call, destructive or
> otherwise.
>
> **Four of this episode's Build-section assumptions did not hold against this
> repo and were resolved before any code was written** — see "Four assumptions
> that were wrong" below. One of them would have made the episode's own
> verification gate measure nothing.
>
> Done-when #8 (Suspend/Resume) is proven **by test, not eyes-on**, and that is
> the correct gate rather than a shortfall: nothing calls either method until
> F12 lands, which the task doc anticipated ("until then exercise the blob
> round-trip directly").
>
> Done-when #9's second half was verified by test rather than by the manual
> flash-counting the gate originally asked for. Eyeballing fetch frequency is
> not a reliable human check; `TestConfigGovernsTheBoundAndTheBatch` proves the
> same four claims deterministically.
>
> One bug found during verification turned out **not to be F18's and not to be
> the component it was reported against.** Diagnosed, not fixed, and parked —
> see "The seventh bug, which is F3's" below.

---

## Where the code went

```
frontend\internal\chat\
├── history.go        NEW — the whole subsystem: historyWindow, the fetch
│                     state machine (histBefore/histAfter/histTail/
│                     histAnchored), seedWindow/rebuildWindow,
│                     prepend/append/replaceRows, evictWindow +
│                     protectedSeqs + forgetRendered, anchorID with
│                     capture/resolve/restore, slotUI capture/restore,
│                     maybeFetchAtEdge, jumpToBottom, historyNotice,
│                     suspendBlob/blobAnchor, applyResumeAnchor
├── history_test.go   NEW — 25 tests: one per Done-when, one per decision,
│                     one per numbered bug
├── chat.go           + raw / window / taskRows / resumeAnchor /
│                     historyBatchSize / sendHistoryFn;
│                     Options.HistoryBatch; **Suspend/Resume made real**
├── protocol.go       + querySessionMessages (decorative);
│                     newQueryEnvelope extracted from doSend;
│                     getSession() now bounded; setSession calls
│                     seedWindow + applyResumeAnchor
├── reload.go         onTaskList KEEPS its rows and rebuilds;
│                     applyTaskRows gated on reachesTail();
│                     pairReloaded's doc records the window-seam property
├── scroll.go         re-pin gated on historyAtTail(); maybeFetchAtEdge
│                     from scrollRows and moveFocus; G calls jumpToBottom
├── render.go         + the loading / paused-by-backpressure affordance
└── model.go          + wireMessage.Seq, wireSession.TotalMessageCount

frontend\internal\protocol\types.go
    + CmdSessionMessages / EvSessionMessageList

frontend\internal\config\config.go
    + ui.history_batch; Scrollback's stale comment corrected

frontend\cmd\point\main.go
    + HistoryBatch wiring

backend\tests\
├── manual_f18_fixture.py   NEW — the three exchanges seed-session cannot make
└── manual_f18_verify.ps1   NEW — gate part 1, self-checking
```

**No new dependencies. No backend product code touched.** `SessionMessages` and
`GetSession(message_limit)` are B18's, exactly as specced — the episode's own
"Do not add backend surface" held.

---

## The architecture, and the one property that carries it

### `raw` is the window; `all` and `hist` are derived

`w.raw` is a contiguous run of persisted rows, oldest-first, exactly as B18's
keyset query returned them: `[oldestSeq, newestSeq]`, **never a set of ranges
with gaps**. `w.all` and `w.hist` are rebuilt from it on every mutation, so
F3's renderer's input shape is unchanged — it still renders "a window", just of
a smaller, managed set.

Contiguity rather than a cache-with-holes was not a reluctant simplification.
A hole-y cache makes eviction, anchor arithmetic and the renderer's input all
harder and buys nothing at this scale: re-fetching a dropped batch is a local
SQLite keyset seek against the index B18 built for exactly this, almost
certainly already in the OS file cache. **Re-fetching is cheaper than holding**,
which is what makes aggressive eviction correct here and would make it insane in
a client/server app.

### The scroll anchor needs no height arithmetic, and that is structural

The task doc calls anchor preservation on prepend "the bug most likely to ship
broken": when N older messages arrive above the viewport, the view must shift by
their **rendered height**, not by message count.

It shipped correct for a structural reason rather than a careful one. F3 already
stores the position as the pair `(topAbs, topRow)` — an entry plus a row offset
inside that entry's block — precisely because an absolute row number would have
to be recomputed against every entry's height on every render (`scroll.go`'s own
doc comment says so). **Inserting rows above an (entry, offset) address changes
the entry's INDEX and nothing else.** So the whole of anchor preservation is:
remember *which* entry the index pointed at, mutate, look it up again. No height
is ever measured, so there is no height for a measurement bug to be wrong about.

This is F3's design paying a dividend six episodes later, and it is worth naming
because the obvious implementation — measure the prepended batch, add its rows
to an absolute scroll offset — is the one the task doc warned about and is the
one an absolute-row position would have forced.

### `pinned` had to keep meaning "at the live edge"

`scrollRows` used to re-pin on reaching the bottom, and `moveFocus` on focusing
the last entry. Once the tail can be absent, the bottom of the resident window
is not the live edge, and pinning there would be a lie with two visible
consequences: the pinned render would bottom-anchor to a stale newest message,
and the "↓ jump to latest (G)" affordance would **disappear at exactly the
moment it is the only way back**. Both re-pins are now gated on
`historyAtTail()`; reaching a windowed bottom fires an append fetch instead and
lets the user keep scrolling.

### §5 (streaming into a non-resident tail) needed no tail buffer

The task doc asks for an "actively streaming message held in a tail buffer
regardless of scroll position". That buffer already exists and is called
`w.live`: it is append-only, never evicted, and always rendered after `w.all`.
What was actually needed was the guarantee that its *persisted twin* can never
enter the window and open a hole — which is the ceiling below. The "merge when
the user returns to the bottom" is therefore structural, not an operation:
`w.all`'s newest resident seq is always ≤ the ceiling, and every live send's
persisted seq is above it.

---

## Four assumptions in the Build section that did not hold

All four were checked against the repo and resolved with Aryan before any code
was written. Recording them because three of them are the kind that would have
been "fixed" silently and wrongly.

### 1. The verification gate could not open the seeded session

`seed-session` resolves its provider through `cli.py`'s `default_provider()`,
which answers "ollama if it is registered **and answering**, else `echo`" — and
`echo` is deliberately not a registered provider. `chat\protocol.go`'s
`onSessionList` skips any session pinned to a provider the backend did not
register, and `ollama` + `demo` always register
(`backend\providers\bootstrap.py`), so `w.providers` is never nil and an
echo-pinned session is **skipped**: the app would bootstrap a fresh empty
session and the entire gate would measure nothing — B18's own "green while
verifying almost nothing", one layer up.

Whether it bites depends on whether the Ollama daemon happens to be up when the
seed runs. On the verification machine it was, and the seed landed on
`ollama/qwen2.5:1.5b-8k`, which *is* registered — so the gate would have passed
by luck. That is exactly the kind of thing that works until it does not.

**Resolved, script-scoped only:** the gate runs
`UPDATE sessions SET provider='demo', model='demo';` after seeding.
`seed-session` itself is untouched — CLAUDE.md's rule against building product
surface for one verification step applies to a `--provider` flag too.

### 2. "Suspend's blob (the F4 `Drafts`-carrying shape)" did not exist

`chat.Workspace.Suspend()` was `return nil, nil` and **nothing called it**. F4's
`Drafts` live somewhere else entirely — in `config.State` → `state.json`. There
was no blob carrying drafts for the anchor to join.

**Resolved:** `Suspend`/`Resume` are implemented for real here, and the anchor
lives only there. It deliberately does **not** also go into `state.json` beside
`Drafts`: two homes would hand F12 two anchors to reconcile. Full-restart
survival of the anchor is F12's call, not this episode's to pre-empt; Done-when
#8 only requires the round trip.

### 3. Prepending can move rows, and the tasks join can fabricate one

S11b's three reload transforms (`pairReloaded` → `decorateReloaded` →
`applyTaskRows`) ran **once over the whole session**. Over a slice, two things
break at the edges, and only the first is the one §2 names:

- `pairReloaded` moves an answer up under its question. A question near the
  newest resident edge whose answer is one batch below stays unpaired; when that
  batch appends, re-pairing *moves rows*.
- `applyTaskRows` builds `answered` from `w.all` only. **A task that failed
  after streaming tokens has a real assistant row.** With that row outside the
  window the question looks unanswered, the tasks row says `failed`, and
  `orphanReply` inserts a placeholder "✗ failed" reply — a **fabricated row in a
  window whose whole contract is that it is a faithful slice.** Strictly worse
  than a missing footer.

**Resolved (Aryan's decision 3): undecided, not reordered, not
fetched-with-overlap.** The transforms run over the resident window only. The
no-reorder-across-an-edge half turned out to be already structural —
`answerAt` is built from the slice it was handed, so an answer that is not
resident is not a candidate. The orphan join is gated on
`window.reachesTail()`, where an absent answer really is absent. A question at
or near a window edge renders plain and unpaired and re-resolves normally when
the adjoining batch loads; the visible cost is a slot or footer appearing one
batch late, recorded in the gate's checklist so it is not mistaken for a content
jump.

Why not a one-message overlap fetch: an answer can sit arbitrarily far below its
question (overlapping sends persist both questions before either answer —
`pairReloaded`'s own doc records this as the defect S11b existed to fix), so no
fixed overlap makes the question decidable. The only honest answers were "defer"
or "guess", and a guess here invents rows.

### 4. A re-fetched tail would duplicate this session's own live sends

`w.live` holds every send made this session — question and reply, as entries.
The backend **also** persists both, with real seqs. They never collided before
F18 because `w.all` was a snapshot taken at open and nothing re-fetched. The
moment a re-fetch exists, an `after=` or tail fetch hands back rows `w.live` is
already rendering and every live send appears twice.

**Resolved (decision 4):** `historyWindow.ceilingSeq` is the newest seq that
existed at open. Nothing above it is ever ingested into `w.raw`; `w.live` owns
that range for the session's lifetime. The tail fetch asks
`before = ceiling + 1` rather than a bare cursorless tail, and
`onSessionMessages` filters on arrival as well — two independent guards, because
this is the one invariant whose violation is visible as duplicated content.

A deliberate second benefit: `reachesTail()` compares against a **fixed** number
instead of the wire's `has_more_after`. The first send of the session persists
rows past the ceiling, so the wire flag would say "more exists" forever
afterwards and every honest-bottom test in the file would start lying.

The stated cost: rows another client appends while the session is open are not
picked up until the next load. That was already true before F18 (nothing
re-fetched after a send at all), so it is not a regression this introduced.

---

## `ui.scrollback` finally means what config.go always said it did

From F1 until this episode, `ui.scrollback` governed only the render cache
(`chat\cache.go`). `config.go`'s own comment claimed it "caps how many messages
F3's Chat workspace renders per session" — **behaviour no code had.** F18 makes
the claim true and the comment now says so, including that it used to be wrong.

`ui.history_batch` is new, named in the file's existing snake_case convention
(`scrollback`, `stream_timeout`) rather than the task doc's `ui.historyBatch`,
so it reads like its neighbours. Default 100, deliberately conservative: the
batch is the unit of work one scroll step can cost, on the machine this episode
was written for.

`historyBound()` floors the bound twice, both for correctness:

- **three screenfuls** — a message occupies at least one row, so a terminal
  taller than the window could never fill its own viewport and every render
  would sit at both edges at once;
- **twice the batch** — below that, a prepend immediately evicts most of what it
  just fetched and the next step re-fetches it: a thrash loop that reads as
  "scrolling is slow" and is really "the config is impossible".

---

## The six bugs, all found by this episode's own tests

Every one was proven by **reverting the fix to watch its regression test fail,
then restoring it to watch it pass.** The exact failure output is quoted because
that is the evidence, not the claim.

### 1. Eviction was completely defeated by the focus fallback

`protectedSeqs` honoured `focusedIndex()`, which falls back to the **newest**
entry when nothing is explicitly focused (F3's convention, so `y` works without
pressing a focus key first). That is not a focus — it is a default. Protecting
it pinned the far edge permanently, so the cut could never reach past it and the
window grew by one batch per fetch forever.

Revert reproduces, in two independent tests:

```
round 0: window holds 50 rows, bound is 40 — eviction is not keeping up
resident rows 80 exceed the bound 60 at oldest=1921
```

**A heap climbing with scroll depth on a session of any size — the exact failure
Done-when #2 calls a hard stop.** Fixed by reading `w.focus` directly.

### 2. "Never evict the anchor or focus" and "memory flat" cannot both hold unconditionally

With the protection honoured unconditionally, holding a focus on the newest row
and fetching older batches grew the window one batch per fetch: 50 rows, 60, 70,
against a bound of 40. The task doc asks for both properties; only one of them is
a hard stop.

`evictWindow` caps the protection at **one batch of slack**
(`bound + historyBatch()`), past which the row is evicted and the position
degrades honestly — `restoreAnchor` pins to the bottom, `restoreFocus` clears to
-1. Revert reproduces:

```
round 1: window holds 60 rows, bound+batch is 50 — protecting a pinned row
must cost bounded slack, not unbounded growth
```

**Unreachable by scrolling**, which is why the trade costs nothing in practice: a
prepend fires only from the top of the window and an append only from its bottom
(`maybeFetchAtEdge`), so the anchor is always at the **near** edge when a batch
lands and can never be the far-edge blocker. Only a focus left behind by `j`/`k`
can be, and losing a focus you have scrolled a full window and a batch away from
is the right answer anyway. The test drives the sequence directly precisely
because no key can.

### 3. A background fetch invented a focus

`restoreFocus` was handed an identity captured through `focusedIndex()`, so
"nothing is focused" became "the newest entry is focused" on the first
scroll-fetch — a focus ring appearing out of a fetch the user did not make, plus
a row protected from eviction for the rest of the session. Revert reproduces:

```
a scroll-fetch set the focus to 29; nothing had been focused
```

Only an explicit focus (`w.focus >= 0`) is captured now.

### 4. A rebuild threw away the user's own slot state

**Found during Aryan's verification**, reported as "`o` stops working on a task
entry after moving the cursor". `decorateReloaded` allocates fresh `liveEntry`
values from the message columns, and F18 made `rebuildWindow` run on every
prepend, append and eviction rather than once per session load — so `slotOpen`
and `expanded`, which exist nowhere but on that pointer, were discarded by every
fetch. An open slot snapped shut the moment a batch landed.

`rebuildWindow` now captures and re-applies a `slotUI{slotOpen, expanded}` map
across the rebuild, keyed by message id and falling back to `(task_id, role)` —
because `applyTaskRows`' placeholder reply has no id and no seq at all.
**Only those two fields** ride along; status, text, the clock and the error are
derived from the row and must be re-derived, or a rebuild would preserve a stale
rendering of a row that has since changed. Revert reproduces:

```
the slot closed itself when the batch landed; slotOpen is the user's state,
not the row's, and a rebuild may not discard it
```

### 5. A cancelled task's slot could not be addressed, so the focus walked off it

`captureAnchor` walks **up** from a row with no id and no seq to the nearest
addressable one. That is right for a scroll position — any nearby row is a fine
place to resume composing a frame from — and **wrong for a focus**, which names
the entry every transcript action acts on. `applyTaskRows`' placeholder reply
(a cancelled or failed-with-no-output research task) is exactly such a row, so
focusing its slot and taking any fetch moved the focus to the **question above
it**, where `liveAt` returns nil and `o` is a silent no-op.

`anchorID` now carries `taskID` and `role`, and `resolveAnchor` matches most
specific first: id, then `(task_id, role)`, then seq. The pair is unique within
a window — `pairReloaded` guarantees at most one assistant row per task. Revert
reproduces:

```
after the batch landed the focus (10) resolves to NO live entry,
so o is a silent no-op
```

### 6. A pinned fetch skipped restoring the focus entirely

The third spelling of the same defect, caught by the test written for bug 4.
`onSessionMessages` returned early when `wasPinned`, skipping **both**
`restoreAnchor` and `restoreFocus`. Skipping the anchor is right — a prepend does
not move the live edge, and restoring a position would unpin. Skipping the focus
is wrong: `pinned` is a statement about the scroll position, not the focus, and
`moveFocus` can set it while an explicit focus exists (focusing the newest entry
pins). A stale absolute index then points the focus at whichever row inherited
the number. Revert reproduces:

```
the thinking block collapsed itself when a batch landed (entry=<nil>)
```

The focus is now re-resolved on both paths; only the anchor is conditional.

**Bugs 4, 5 and 6 are one lesson in three places:** F18 turned a once-per-load
derivation into a per-mutation one, and every piece of state that lived only on
the derived objects had to be made to survive the derivation. Anything a later
episode adds to `liveEntry` that the USER sets — not the row — has to join
`slotUI`.

---

## The seventh bug, which is F3's — diagnosed, not fixed, parked

Reported during verification as a **task tray** bug: `n` onto a research task
entry, `o` toggles it, then `j`/`k`/`Shift+G` without pressing `o`, and `o`
stops working; only `p` then `n` restores it.

**It is not the tray's, and not F9a's.** In `app\popup.go`'s `handleKey`, every
plain rune goes into the popup's **filter** — F07's own Done-when #5, so a typed
`j` cannot read as "scroll" — so `n`, `o`, `j`, `k` and `G` never reach the tray
at all; pressing `n` there filters the list. The keys in the repro are the chat
transcript's browse-mode keys (`chat\scroll.go`'s `handleKeyString`), acting on a
reloaded `@research` slot in the transcript, which looks enough like a tray row
to be mistaken for one.

Three mechanisms were separated by test:

| Spelling | Cause | Whose |
|---|---|---|
| `j` / `k` on an answered slot | the slot's open state lost in the rebuild | **F18's — bug 4, fixed** |
| `j` / `k` on a cancelled slot | the focus walks up to the question | **F18's — bug 5, fixed** |
| `Shift+G` | `case "G"` sets `w.focus = -1` | **F3's — parked** |

The `G` half was verified pre-existing rather than assumed: `w.focus = -1` on `G`
was introduced in commit `68a1cbc` ("F3: chat viewport") and is byte-identical at
`96feddc`, the commit F9a closed on — confirmed with
`git show 96feddc:frontend/internal/chat/scroll.go`. F9a-era `scrollRows` never
touched `w.focus` at all, so `j`/`k` could not desync anything there; those two
spellings are genuinely new with F18's fetch-on-scroll and are fixed above. F18
only appended `w.jumpToBottom()` after `G`'s two existing lines.

Not fixed here, deliberately: it is outside this episode's Build section, and the
fix is a UX decision rather than a repair — should `G` keep the focus where it is
(leaving the highlight off screen), move it explicitly to the newest entry, or
keep clearing it and have `o` say why it did nothing? `y`, `t` and `^R` all read
the same `focusedIndex()` fallback, so whichever answer is chosen governs four
keys. Parked in `tasks/frontend/INDEX.md` with the repro, the owner
(`chat\scroll.go`'s `handleKeyString`/`focusedIndex`) and an F16 anchor.

---

## Manual Test Log (F18)

| Done-when | Result |
|---|---|
| #1 50,000-message session opens no slower than a 10-message one; no full-session fetch at open | ✅ instant open, pinned to bottom, no multi-second load. `GetSession` always carries `message_limit` — the unbounded path now has no reader anywhere in the frontend |
| #2 `POINT_MEMLOG` heap flat across a full bottom→top traverse AND repeated up/down scrolling | ✅ **`heap_alloc` stayed in a tight ~2.0–2.4 MB band across minutes of scrolling, sending, and scrolling away mid-stream — no upward trend.** The episode's defining proof |
| #3 scrolling up prepends with no content jump | ✅ the message under the cursor stays put across a batch boundary |
| #4 eviction keeps memory bounded; scrolling back down re-fetches correctly, no gap or duplicate | ✅ confirmed eyes-on and by `TestScrollBackDownRefetchesWithoutGapOrDuplicate`, which asserts the exact seq run after every step rather than a length |
| #5 `G` re-pins to true bottom even when the tail was evicted | ✅ shows `◐ loading latest…`, re-pins on arrival; instant when the tail is already resident |
| #6 a reply streaming while scrolled deep stays resident and merges correctly | ✅ confirmed — tokens kept arriving, the streaming reply was never lost |
| #7 history fetches defer while a task streams | ✅ confirmed — `⏸ history paused while generating`, parked fetch fires when the stream settles |
| #8 Suspend/Resume restores position; degrades to bottom if the anchor is gone; pinned resumes at current newest | ✅ **by test** (`go test -run 'TestSuspendResume|TestResume' -v`, all cases pass). Nothing calls either method until F12 — the task doc's own instruction |
| #9a `config.toml` is read, not ignored or fatal | ✅ confirmed |
| #9b `ui.scrollback` and `ui.history_batch` actually govern the bound and the batch | ✅ **by test** — `TestConfigGovernsTheBoundAndTheBatch`, all four subtests (scrollback is the bound; floored at three screenfuls; floored at twice the batch; `historyBatch` is the batch and defaults). Replaces the gate's original manual flash-counting, which is not a reliable human check |

### Pairing at a window seam, against the hand-built session

| Item | Result |
|---|---|
| Exchange C — clean research exchange | ✅ reloads as a collapsed slot; `o` expands and collapses correctly |
| Exchange B — cancelled, no assistant row at all | ✅ the orphan footer appears **only once its batch is resident**, not before |
| Exchange A — failed AFTER partial output | ✅ **no fabricated "✗ failed" marker appeared while its answer's batch was outside the window**; the real footer appeared only once that batch loaded. This is assumption 3's whole point and the one that would have invented a row |
| Batch-seam behaviour, scrolling back and forth across boundaries | ✅ rows went unpaired-then-resolved as expected; never duplicated, never mispaired |
| The oldest exchange is a REAL `@research` turn driven through the wire | ✅ real `task_id` on both rows, real `invocation`, real `outcome` — at least one collapsed slot on screen came from the live path, not from hand-written rows |

### Gate tooling

| Item | Result |
|---|---|
| `manual_f18_verify.ps1` — all 9 fixture self-checks | ✅ pass |
| `manual_f18_fixture.py` — all 9 of its own self-checks | ✅ pass |

Gate: `gofmt -l` empty, `go vet ./...` clean, `go test ./frontend/...` green.

### Why the fixtures are two sessions and not one

`seed-session` writes plain alternating rows with **no `task_id` on any of
them**, so `pairReloaded` is a no-op over it and `hist` is all nil. That session
is exactly right for the memory proof and exercises **none** of the
slot-reconstruction logic decision 3 is about. `manual_f18_fixture.py` builds the
second session: a failed-after-partial-output exchange (which no real send can
produce on demand — nothing makes the demo provider drop a connection halfway), a
cancelled task with no assistant row, and a clean one, each separated by 60 filler
rows so no resident window can hold two at once. Everything goes through
`SessionManager.append_message` and `TaskRepository.record`, so `seq` is assigned
by the database (B18: never by a caller) and the stored content/metadata encoding
is the real one rather than hand-written JSON.

Both scripts self-check their own output before anyone scrolls to it. A gate that
trusts a fixture it never looked at is B18's "green while verifying almost
nothing", one layer down.

---

## Handoff — what later episodes inherit

### F12 — the Suspend/Resume contract, exactly as implemented

F18 is the **first real implementation** of `chat.Workspace.Suspend`/`Resume`;
both were `return nil, nil` from F3 until here. **F12's real workspace-state type
must adopt this shape method-for-method, not approximate it.**

```go
type suspendBlob struct {
    Version int         `json:"version"`
    Anchor  *blobAnchor `json:"anchor,omitempty"`
}

const suspendVersion = 1

type blobAnchor struct {
    AnchorSeq      int  `json:"anchor_seq"`
    RowOffset      int  `json:"row_offset"`
    PinnedToBottom bool `json:"pinned_to_bottom"`
}
```

- **`AnchorSeq` is B18's `seq` and nothing else.** Not a row index (meaningless
  the instant anything is inserted above), not `created_at` (B18's migration
  broke `created_at` ties with `rowid` during a one-time backfill and never
  again). **Zero means "no addressable anchor"** — the position was on one of
  this session's own live sends, whose persisted seq the frontend never learns
  (the backend persists it and never echoes it back), or on `applyTaskRows`'
  placeholder, which has no row to have a seq.
- **`RowOffset` is how far INTO that message's block the top row sits.** Without
  it a resume lands at the head of a reply the user was reading the middle of,
  which on a long reply is a different screen entirely.
- **`PinnedToBottom` is not redundant with `AnchorSeq`, and it is the field most
  easily got wrong later.** Restoring a pinned user to their old anchor seq
  strands them at a stale message with unread content below, because a session
  can **receive messages while unloaded** — an F5 background task, or an SSH
  peer. Pinned means "the newest message", **resolved at Resume time**, not the
  newest message as of Suspend. It earns its keep only once F5 streams into
  unloaded workspaces, which is exactly why it is cheap to specify now and easy
  to forget later.

**Two behaviours F12 must preserve, not re-derive:**

1. **Resume degrades, never fails.** Five ways a blob can be unusable — empty
   (which is what F3's own `Suspend` wrote, so a pre-F18 state file will hold
   one), not JSON, a version this build does not know, an anchor newer than the
   session has, an anchor that was never addressable — all resume **at the
   bottom**, with the session loaded and `nil` returned. The restore path can
   never be the thing that breaks a resume. All five are covered by
   `TestResumeDegradesToBottomAndNeverErrors`.
2. **Resume cannot position anything itself.** At `Resume` time there is no
   session, no window and no transcript to address. It stores the anchor;
   `applyResumeAnchor` consumes it **once**, at the end of `setSession`. An
   anchor older than the opened tail is fetched with `after = anchorSeq - 1`, so
   the anchor sits at the **head** of the window with content below it to fill
   the viewport rather than at its tail with blank space under it.

**The anchor deliberately does NOT also live in `state.json` beside F4's
`Drafts`.** Two homes would give F12 two anchors to reconcile. Full-restart
survival is F12's decision; the blob round trip is all F18 claims.

### F16 (polish)

- The loading affordance for a scroll-fetch, a `G`-fetch and a
  backpressure-parked fetch is functional here, not final. `historyNotice`
  returns the text and which edge it belongs on; `viewportBody` overlays it on
  a transcript row rather than taking one — the same reason the connection
  banner takes a row and this does not: the banner is about the whole session,
  this is about one edge of it, and stealing a row would reflow the transcript
  every time a batch loaded, which is the content jump this episode exists to
  prevent arriving by the back door.
- The parked case says `⏸ history paused while generating`, not "loading". A
  fetch held back by backpressure is not slow, it is deliberately waiting, and
  F04's Bug 12 (a browse indicator advertising an Esc binding that did not
  exist) is this codebase's standing rule against a hint that lies.
- **The `G`-clears-the-focus bug is parked for F16** — see
  `tasks/frontend/INDEX.md`'s parking lot.

### Anyone touching `liveEntry` or the reload transforms

- **`w.all` has exactly one writer path now**: `rebuildWindow` →
  `pairReloaded` → `decorateReloaded` → `applyTaskRows`. Do not add a second.
  Tests that assign `w.all` directly still get F3's plain rendering with no
  window behind it, which is the latitude `histAt` was already bounds-safe for.
- **Anything added to `liveEntry` that the USER sets rather than the row must
  join `slotUI`** (bug 4). The three-bug cluster 4/5/6 is one lesson: F18 turned
  a once-per-load derivation into a per-mutation one, and every piece of state
  living only on the derived objects had to be made to survive the derivation.
- **`applyTaskRows` is gated on `window.reachesTail()`.** Anything that makes it
  fire away from the tail reintroduces the fabricated-row bug in assumption 3.
- **`historyWindow.ceilingSeq` is load-bearing for correctness, not an
  optimisation.** Removing it duplicates every live send.
- **`onSessionMessages` must keep its `env.CorrID == ""` guard.** F8's Bug A was
  exactly this: a cleared correlation-id field is a valid switch case, and the
  backend's 5-second idle heartbeat carries a null corr_id forever.

### B18's parking-lot pointer

`tasks/backend/INDEX.md` can now record that pagination is implemented (B18) and
windowed on the frontend (F18). **Not edited by this session** — CLAUDE.md's rule
is that only `design/completed/**` claims completion, and only after Aryan
confirms. Left as a one-line doc step for whoever next touches that file.

### Not built here, on purpose

Archival or summarisation of old messages, and semantic retrieval — both B13's.
F18 only makes existing history cheap to hold and to read in slices.
