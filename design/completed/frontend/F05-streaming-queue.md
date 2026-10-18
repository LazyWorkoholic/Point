# Episode F5 — Streaming, Queue, Cancel ✅

**The episode where Point became an application: tokens appear, a second
message can be composed while the first replies, two lanes run at once, and
cancelling leaves something honest behind.** Four rounds of manual
verification found nine defects, and the two largest were not in the feature
work at all — they were in the *model underneath it*. One list-splitting
mistake caused four separate visible bugs, and one fix added in round three
had to be **removed entirely** in round four because it corrupted correct
transcripts. Both are recorded in full below, mechanism first, because a
summary of either would lose the thing a future reader needs.

Verified against the running app on 2026-09-06.

---

## The two contracts F6 and F9 must use EXACTLY

F6's task card and F9's result card both listen for "a task reserved a slot"
and "a task ended". **These are the names. Not approximately these names.**

### 1 — A task reserves a transcript slot

There is no wire event for this, and there must not be one: F5 requires the
slot exist at **send** time, before any reply has arrived. The reservation is
a purely local act, and the wire only ever *confirms* it afterwards.

| Stage | Identifier | Where |
|---|---|---|
| Reserve (local, at send) | `liveEntry{kind: liveReply, lane: laneTask}` appended to `Workspace.live` | `chat\stream.go` `startSend` |
| Confirm (wire) | `protocol.EvTaskStarted` — wire string **`"TaskStarted"`** | `protocol\types.go:44` |
| Lane decision | `TaskStarted` payload field **`background`** (bool) | `runtime.py`'s `_make_stream_sink` |
| Task identity | envelope `task_id`; before it arrives, envelope `corr_id` | `protocol.Envelope` |

`background` is the **only** authority on which lane a task belongs to. The
backend computes it as `agent.max_steps > 1` (B11) and rides it on
`TaskStarted`. The frontend reserves optimistically from F4's static agent
map and then **reconciles**: `background: true` stays `laneTask`,
`background: false` collapses to `laneTurn` in place, without changing the
entry's index. F6 and F9 must read the same field rather than re-deriving the
lane from the agent id.

**Correlation rule, which cost a round to get right:** a send's replies are
matched by `corr_id` **first**, `task_id` second. `corr_id` is the id the
frontend minted for `CmdSendMessage` and is the only unambiguous handle
*before* `TaskStarted` names a task. Matching on `task_id` alone loses every
event of a task that is cancelled before it starts.

### 2 — A terminal event was received

```go
protocol.IsTerminal(eventType) // protocol\types.go
```

| Constant | Wire string | Meaning |
|---|---|---|
| `protocol.EvTaskDone` | `"TaskDone"` | completed |
| `protocol.EvTaskFailed` | `"TaskFailed"` | failed; payload carries `error` and `code` |
| `protocol.EvTaskCancelled` | `"TaskCancelled"` | cancelled; partial text is kept |

**Exactly one of these arrives per task.** Three things are commonly mistaken
for terminal events and are not:

- **`protocol.EvTaskCancelAccepted` — `"task.cancel.accepted"`** is the ACK
  for a `CancelTask` request. The sink still emits `TaskCancelled`
  afterwards. Treating the ack as terminal leaves the real terminal event
  with nothing to land on.
- **`statusTimedOut`** is the *UI's own* provisional state, never a wire
  event. See "terminal vs wireTerminal" below.
- **`TaskFailed` with `code == "bounds_exceeded"`** is an outcome, not a
  fault — a bounded loop that stopped at its own configured limit and **kept
  its partial answer**. F9's result card must render it as a completed-but-
  truncated result, not as a failure.

### 3 — Streaming payload shapes

Two token events exist, on different backend paths, and **both are real**:

| Constant | Wire string | Text location | Path |
|---|---|---|---|
| `protocol.EvTextDelta` | `"TextDelta"` | `payload.data.text` | every streaming provider |
| `protocol.EvTaskToken` | `"TaskToken"` | `payload.text` | the `echo` provider only |
| `protocol.EvThinkingDelta` | `"ThinkingDelta"` | `payload.data.text` | reserved; no provider emits yet |
| `protocol.EvProgressUpdated` | `"ProgressUpdated"` | `payload.data.step`, `.max_steps`, `.message` | B10/B11 agent loops |

Handling only `TextDelta` leaves the echo provider rendering nothing;
handling only `TaskToken` leaves every real provider rendering nothing. F5
reads both through one `deltaText(env)` helper (`chat\stream.go`).

### 4 — App-level messages (`frontend\internal\app\stream.go`)

| Type | Raised by | Consumed by |
|---|---|---|
| `app.CancelStreamMsg` | `chat.cancel` action, `ctrl+c` | the active workspace |
| `app.DiscardPendingMsg` | `chat.browse` (`esc`) when at queue capacity | the active workspace |
| `app.EditPendingMsg` | `chat.queue.edit`, `ctrl+e` | the active workspace |
| `app.RetryMsg` | `chat.retry`, `ctrl+r` | the active workspace |
| `app.StreamAware` | interface: `Streaming() bool` | Root, for the header animation |
| `app.QueueAware` | interface: `HasPending() bool` | Root, to choose esc's meaning |

---

## Files

### `frontend\internal\chat\` — new

| File | Lines | What |
|---|---|---|
| `stream.go` | 855 | The send path, both lanes, the depth-1 queue, cancel/retry, the silence watchdog, `handleStreamEvent` |
| `stream_render.go` | 448 | `renderLive` (memo + throttle), the task slot, the inline turn, thinking block, the unclosed-fence rule, footers, the pending bubble |
| `entries.go` | 138 | The three sources read as one ordered sequence — `entryCount`/`entryAt`/`entryRole`/`entryLines`/`entryText`/`liveAt` |

`scroll.go` (340) was **rewritten**, not edited. `chat.go`, `protocol.go`,
`render.go`, `copy.go`, `input_keys.go`, `codeblock.go` and `textutil.go`
were edited. `reload.go` was created in round three and **deleted** in round
four — see "The retry-fold".

### `frontend\internal\app\` — new

`stream.go` (70) — `StreamAware`, `QueueAware`, the four F5 messages, and
`Root.streaming()`/`Root.hasPending()`. Same narrow-interface pattern
`FocusAware` (F4) and `DraftFlusher` (F4 Bug 17) already established: the
root never reads a workspace's fields, it asks a one-method interface.

`actions.go`, `root.go`, `render.go` edited. `config.go` gained
`ui.stream_timeout`; `main.go` passes it through.

### Tests

`chat\stream_test.go` 39, `chat\reload_test.go` 2 + 2 benchmarks,
`app\stream_test.go` 6, plus updates to `workspace_test.go` (20) and
`input_workspace_test.go`. **190 tests pass** across `chat` and `app`;
`go build`, `go vet`, `go test ./...` green with `-count=1` and `-race`;
`gofmt` clean.

---

## Config named here

`[ui] stream_timeout` — integer **seconds**, default **120**. Named in F5
because `F05-streaming-queue.md` asked for it as "(config, default 120s)" and
named no key — the same gap `ui.scrollback` had, resolved the same way F1
resolved that one. `[ui]` is the table the frontend reads and this is a
**client-side** watchdog. The backend has its own unrelated per-agent bounds
and does **not** read this key.

---

## Bug 1 — the two-list entry bug (four visible symptoms, one cause)

**The single most valuable thing in this document.** Four separate bug
reports across two verification rounds turned out to be one mistake.

### The mistake

`startSend` appended the user's message to `w.all` (the persisted-message
list) and the assistant reply to `w.live` (the in-flight list). `entries.go`
rendered *all of `w.all`, then all of `w.live`*. Two append-only lists cannot
express one interleaved order.

```
send Q1 → all=[Q1]        live=[A1]     renders Q1 A1        ✓
send Q2 → all=[Q1,Q2]     live=[A1,A2]  renders Q1 Q2 A1 A2  ✗
```

**Every question clustered above every answer.** This fired on *any* second
send in a session; the queue and retry paths merely made it obvious enough to
report.

### What else it was causing

The same list split was behind three other reports that looked unrelated:

- **The task slot could not be focused, expanded or retried.** `focusedIndex`
  and `liveAt` index across the combined sequence; with the two lists
  interleaved wrongly, `o`/`t`/`ctrl+r` resolved to a different entry than
  the one on screen.
- **`ctrl+R` retried the wrong task.** `retryLast` scanned for "the most
  recent retryable entry" rather than the focused one. With several dead
  entries on screen, "newest" and "the one you are looking at" are rarely the
  same, and only one of them is a target the user chose.
- **A `[Retry]` label with nothing behind it.** The footer promised an action
  whose target was resolved by list position.

### The fix

A send now appends **both** entries to `w.live`, adjacent, in creation order:

```go
type liveKind int
const (
    liveUserMsg liveKind = iota // what the user sent
    liveReply                   // what came back (either lane)
)
```

Creation order **is** display order; there is nothing left to interleave.
`w.all` stays first because it is history — everything in it happened before
this session's first send, by construction.

Retry targeting was fixed at the same time and by the same principle:
`retryTarget()` prefers the **focused** entry, falls back to the most recent
retryable, and carries the entry **by pointer** from then on. Resolving it
again by index would reintroduce the bug the moment anything appended to
`w.live` — which retrying does, immediately.

### The one it hid

Fixing the ordering exposed a defect the tests had missed: a `liveUserMsg`
carries a `lane` field like every other entry (the zero value, `laneTurn`),
and `renderLiveEntry` dispatched on **lane** alone. A message the user typed
rendered under a **"Point:" label**. `renderLiveEntry` now dispatches on
**kind first, then lane**.

**The rule for a future episode:** display position is creation order —
never role, never which list a thing lives in. Anything that adds a fourth
source to the transcript adds it to `entries.go`'s ordering, not beside it.

---

## Bug 2 — the scroll rewrite

### What was wrong

F5's first cut scrolled by **entry**: `j`/`k` moved `topAbs` by one whole
message. A reply taller than the viewport could not be scrolled *through* —
everything below its first screenful was permanently unreachable. F3 had
scrolled row-based over a flat list and worked; introducing the three-source
entry model silently moved scrolling to entry granularity.

### Why an absolute row index was the wrong fix

The obvious repair is a single absolute row number. It is wrong here:
resolving an absolute row to a position requires summing every entry's
rendered height, which is **O(all entries) per render**, and throws away F3's
explicit "never more than one screenful is ever measured" property — the
thing that keeps a 10,000-message session smooth.

### What it is instead

The position is a **pair**: `(topAbs, topRow)` — an entry index plus a row
offset inside that entry's block.

```go
// entryBlock is one entry's rows INCLUDING the blank gap that precedes it.
// The transcript is exactly block(0) … block(n-1) concatenated, so every row
// belongs to exactly one block — which is what makes (entry, offset) a
// complete address for any row.
func (w *Workspace) entryBlock(i, width int) []string
```

Each scroll step is **O(1)** (advance the offset; roll to the next block at
its end) and each render is **O(rows)**. `bottomAnchored` walks backward from
the last entry and stops once `rows` is filled, so pinned mode is O(rows) too.

### Two bugs found while writing the test for it

Both were in the new scroll code and neither would have been caught by
inspection:

1. **Holding `j` at the bottom drifted the position arbitrarily past the end
   of the transcript.** The state moved on every press while the pinned
   render snapped the screen back, so the position and the display diverged
   without limit. Fixed by clamping to the bottom-anchored position, not just
   setting the `pinned` flag.
2. **Scrolling up from pinned never unpinned.** `pinned` was only ever
   assigned `true`; a scroll that started pinned stayed pinned, the pinned
   render snapped straight back, and scrolling up did nothing at all. Fixed
   by assigning `false` on the other branch — the assignment has to happen on
   **both** paths.

### The keymap change

F3 defined `j`/`k` as *message focus* movement. F5 changes that:

```
j/k · ↓/↑    scroll ONE ROW
n/p          move entry focus (what y, o, t and ^R act on)
ctrl+d/u · PgDn/PgUp   page (one screenful less a row of overlap)
G            pin to the live bottom
y            copy the focused entry's raw text
t            toggle the focused reply's thinking block
o / enter    expand the focused task slot
```

Focus moved to `n`/`p` rather than being dropped: `y`, `o`, `t` and Retry all
need a target, and "whichever entry happens to be at the bottom" is not one a
user can aim. F3's and F4's tests were **updated** to the new semantics, not
deleted.

**Auto-follow**, confirmed and tested: pinned follows tokens down; scrolling
up holds position while the stream continues underneath; scrolling back down
to the bottom re-pins on its own; `G` returns to the live edge.

**Focus indication on a one-line slot** needed its own answer. Recolouring
the head line was invisible on a failed slot, which already had a colour of
its own. Focus is now **structural** — a leading `▌` bar occupying a column
the unfocused state leaves blank, legible whatever colour the state is.

---

## Bug 3 — the timer that outlived its stream (idle CPU)

Measured at **~1.6% of a core, continuously, on an untouched app**, and it
never stopped on its own.

### The mechanism

`writeSend` reported a failure by setting `w.loadErr` and returning. The live
entry stayed in `statusSending` — **forever**. Nothing else could end it:

```
entry stuck non-terminal
  → Streaming() never returns false
    → Root.ensureAnimation() re-arms the 120ms header tick forever
    → Workspace.armTick() re-arms the 500ms watchdog tick forever
    → renderLive re-renders the entry every second (non-terminal entries are
      stale once their drawn second passes, so the slot clock can tick)
```

Three independent periodic costs, all keyed off one boolean that could never
go false. Any send that could not reach the wire — no client, a marshal
error, a closed pipe — armed it permanently.

### The fix

**Every failure path in `writeSend` now ends the entry** (`statusFailed`
with the reason), not just records the error. And `liveUserMsg` entries are
born `statusDone`: a user's message is not a task, and it must never be able
to hold the ticks open. `Streaming()` counts only `liveReply` entries.

The tick design itself was already correct and is worth stating, because it
is the thing the leak defeated: **nothing is scheduled when nothing is
running.** `armTick` returns `nil` unless `Streaming()`; `onTick` re-arms only
while `Streaming()`; the animation tick freezes rather than resetting, so a
frozen frame reads as "idle" instead of "broken". A tick already in flight
when a stream ends simply does not re-arm — that is the only way to end a
`tea.Tick` that has already been scheduled.

**Confirmed fixed in verification:** Point idles under 1% (averaging ~0.4%),
2–3% while actually streaming, with `llama-server` carrying 55–70%.

---

## Bug 4 — `runewidth.StringWidth("\t") == 0`

Reported as "the expanded slot body doesn't wrap; long lines overflow right
and wrap to the far left". **The wrapper was not at fault.** A sweep of every
renderer at widths 20–120, focused and unfocused, with prose, code blocks,
long URLs, tables and open fences found **zero** overflow.

### The actual cause

`runewidth.StringWidth("\t")` returns **0**. Every width measurement in the
package counts a tab as zero cells; every terminal draws it as up to eight.
Preformatted text carrying tabs therefore **measured as fitting and then
overflowed on screen**, and the terminal wrapped it to the far left. The
symptom reads as broken wrapping; the wrapping was correct and the
measurement was wrong.

Prose escapes this entirely because `splitWords` goes through
`strings.Fields`, which drops tabs. The paths that keep text verbatim are the
affected ones, and they are exactly three:

- fenced code blocks — **`codeblock.go`, which is F3's file**: this was never
  an F5-only bug. Any chat reply containing tab-indented code overflowed the
  same way, and had since F3.
- an unclosed fence's raw tail (`renderStreamBody`)
- thinking blocks (`renderThinking`)

### The fix

`expandTabs` in `textutil.go`, column-aware, applied at all three entry
points. `tabStop = 4`, and **the value is cosmetic while the expansion is
not**: once the tabs are gone, what the package measures and what the
terminal draws agree whatever stop is used. That is the entire point.

---

## Bug 5 — the retry-fold, added and then REMOVED

**This one is recorded because it is a mistake worth not repeating: a
heuristic that "usually works" corrupted correct data, and deleting it beat
refining it.**

### Why it was added

A retry re-sends the question, and the backend persists a user message per
send (`agents\executor.py`'s `run` appends a `UserMessage` before doing
anything else) — it has no way to know a send repeats one whose answer was
discarded. So a reloaded retry read as two identical questions with one
answer. Round three added `reload.go`: fold runs of **adjacent identical user
messages** into the first, labelled `· asked twice (retried)`.

### Why it was wrong

**Adjacency does not identify a retry.** Two *successful* task-lane
(`@research`) sends of the same question persist their questions adjacently,
because both questions land before either answer arrives:

```
DB rows:   Q1  Q1  A1  A1
folded:    Q1(labelled "retried")  A1  A1
```

One question, **two answers orphaned under it**, and a "retried" label on
sends that were never retried. Reproduced in 3 of 4 batches. The fold was
**corrupting a correct transcript** — strictly worse than the cosmetic
duplicate it was hiding.

### Why removal, not refinement

There is nothing real to fold on. `agents\executor.py:418` builds
`UserMessage(session.id, text)`; `task_id` is not generated until line
**421**. **The question is created before its own task exists and never gets
one.** Only the assistant side carries `task_id`. So there is no retry link,
and no question→answer link at all — pairing is not merely hard, it is
unrepresentable in the stored data.

Any timing-based substitute is also ruled out by the verification evidence:
across four patterns, sequential and queued sends reload correctly, while
*two research tasks fired together* and *a plain question plus a research
task fired together* both split. In the mixed case **the plain question also
separated from its answer** — so this is not "background tasks misorder",
and no heuristic keyed on lane or timing can be correct.

`reload.go` was deleted; `setSession` is back to `w.all = s.Messages`
verbatim. `TestReloadRendersEveryRowVerbatim` locks in the exact repro: four
rows in, four entries out, both answers kept, no invented label.

**Consequence, accepted deliberately:** a reloaded retry shows the question
twice. That is the honest rendering of what the database holds, and it stays
until B12 supplies the link.

---

## `terminal` vs `wireTerminal` — read this before touching the watchdog

Two predicates on `liveStatus`, and conflating them causes the failure the
watchdog exists to prevent:

```go
func (s liveStatus) terminal() bool      // Done, Failed, Cancelled, TimedOut
func (s liveStatus) wireTerminal() bool  // Done, Failed, Cancelled
```

- **`terminal()`** — "finished as far as the UI is concerned". Stops the
  spinner, frees the lane, lets the queue fire.
- **`wireTerminal()`** — an outcome the **backend actually reported**. This,
  not `terminal()`, is what enforces "exactly one terminal state".

`statusTimedOut` is the UI's own **guess** that nothing more is coming, and a
guess must be revisable. If the task speaks again it was alive after all and
returns to running; if its real `TaskDone` arrives late, that outcome wins.
The first cut made `finish()` refuse any change once `terminal()` was true —
so a task that resumed kept a "no response" footer forever and its real
completion was ignored. **The mechanism built to prevent one wrong ending was
causing a different one.**

---

## The watchdog is a SILENCE budget

`lastEventAt` is reset by `touch()` on every text delta, thinking delta,
progress update and tool event; `onTick` measures against that, never against
`startedAt`. A task that keeps talking is never cut off, however long it
runs.

Two refinements landed in round three:

- **First-token grace** (`firstTokenGraceFactor = 2`). Waiting on a cold
  local model being loaded from disk is the one silence that is *expected*.
  Proportional rather than a constant, so a small `stream_timeout` set for
  testing still behaves predictably.
- **Three outcomes now read as three different things**, because they are:
  `⚠ stopped at its limit — …` (bounds), `✗ failed — …` (fault),
  `⚠ no response — nothing arrived` (genuine silence).

---

## Known limitations — deliberately NOT fixed here

### 1. `timeout_s` is a wall-clock duration cap, and it kills healthy streams

**Traced, not inferred.** A plain turn died at exactly 60s and a `@research`
task at exactly 180s, both while actively producing output.

- The message `"stopped after {timeout_s}s: this agent's time limit"` exists
  at exactly one place: `backend\agents\executor.py:884`, in `_check_deadline`.
- 60 and 180 are `general.timeout_s` and `research.timeout_s` in
  `backend\agents\builtins.py`. A single frontend `stream_timeout` could not
  produce two different numbers.
- `_check_deadline` is called at `executor.py:699`, inside
  `async for provider_chunk in stream:` — **once per chunk**. B11 added it
  deliberately (to cap a degenerate local model that spiralled), and the
  consequence is that `timeout_s` is enforced *while output is flowing*.

**The frontend watchdog is innocent** — it produces `"no response — nothing
arrived"` and only after real silence, and its budget never came close to
elapsing. **The frontend message is accurate**: the task genuinely was
stopped at its configured limit. **The POLICY is wrong**, in two ways: 60s is
far too low for local-model generation, and a bound should never terminate a
task that is actively producing output. The degenerate case B11 defended
against is unbounded *output*, not elapsed *time* — an output-size or
stall-based bound targets it without killing healthy long generations.
Backend decision, tracked separately.

### 2. No `task_id` on user messages

`executor.py:418` builds the `UserMessage` before `task_id` exists at `:421`.
A question cannot be paired to its answer, and a retry cannot be told from a
repeat. **B12 fixes.**

### 3. Reload pairing

Anything in flight **at the same time** reloads grouped (`Q,Q,A,A`) rather
than paired. Confirmed across four patterns: sequential and queued sends are
correct; two research tasks fired together, and plain + research fired
together, both split. This is **not** "background tasks misorder" — the plain
question in the mixed case also separated from its answer. Any timing-based
heuristic is therefore wrong; pairing needs B12's `task_id`. Fixed in session
10b.

### 4. Slots and failure footers are live-session-only

`invocation` is defined (`managers\message.py:178`), serialised
(`:290`) and already parsed by the frontend (`model.go`'s `wireInvocation`) —
but **nothing writes it**. Neither `executor.py` nor
`streaming\accumulator.py` sets one. After a reload the frontend cannot tell
that a message came from `@research`, so a slot cannot be reconstructed and
`o` has nothing to toggle. Failed and timed-out tasks persist nothing at all,
so their footers are lost outright.

**This is a correctness gap, not missing decoration.** After a reload, a
research answer that was partial, timed out, or stopped at its bounds renders
as an ordinary, complete assistant message. The transcript asserts the answer
is finished when it is not; provenance and completeness are both lost.

**It is not, however, an approval bypass.** The propose-then-approve rule
governs **memory writes and file writes**, not transcript membership:
`status: pending | accepted | rejected` (`project_idea.md:217`) is a column on
the **`memories`** table, Research's row reads `writes memory: proposals only`
(`:241`), and B11's `_maybe_propose` (`executor.py:786`) writes to its own
`agent_proposals` table, only on a clean finish. Those records survive a
restart in their own state, and **F11** is their approval surface. Restarting
accepts nothing.

**B12 needs two additive changes:** populate `invocation` (at minimum
`agent`) on persisted assistant messages, and record a terminal outcome +
`code` for tasks that did not finish cleanly.

---

## Layout / theme decisions made here

Neither `LAYOUT.md` nor `THEME.md` changed. Everything below uses roles and
shapes they already define.

- **The header animation** (`app\render.go`'s `animFrames`) — LAYOUT.md
  specifies "animation left, POINT centre, trademark right" and draws it as
  `(animation)` without naming a form. F5 makes it one bright diamond
  travelling across three dim ones, every frame exactly five display cells
  wide so the centred title never shifts. `animFrames[0]` **is** the idle
  frame and is byte-identical to what the header drew before F5 — an
  untouched app looks exactly as it did.
- **Task slot states** use THEME.md's existing `running` / `queued` / `done`
  / `warning` / `error` roles. No new role was added, and none was needed.
- **Focus on a one-line slot** is structural, not chromatic — a leading `▌`
  in `border.focus`, occupying a column the unfocused state leaves blank.
  Recolouring the head line was invisible on a slot that already carried
  `error` red.
- **The queue-full lock** renders in `warning`, in the Query Area's own box,
  in place of the input. The reason travels with the lock rather than being
  put in the status bar, because "the input is dead with no explanation" is
  the stated failure.
- **`tabStop = 4`** (`chat	extutil.go`) is the one new constant with a
  visual consequence. It is cosmetic by construction — see Bug 4.

## Manual Test Log (F5)

Confirmed by Aryan across four verification rounds, against a real Ollama
backend. **Terminal size was not recorded per check** — a gap worth closing
in future rounds, since LAYOUT.md's breakpoints are the thing size-specific
testing would exercise.

1. Tokens stream visibly; a fenced code block does not flicker while the
   fence is still open.
2. Sending during a stream produces a dimmed pending bubble that fires
   automatically when the stream ends. `ctrl+e` pulls it back into the input;
   `esc` discards it.
3. At depth 1 the input locks **with the reason visible**
   (`⊘ queue full — esc to discard pending`).
4. `ctrl+c` mid-stream keeps the partial text, marks it `⚠ interrupted`,
   offers `[Retry ^R]`, and the app stays fully usable.
5. `ctrl+c` with nothing streaming does nothing at all — no quit, no status
   line. `ctrl+q` quits.
6. `@research <topic>` reserves a slot immediately; chatting appends below
   it; the slot fills in place with no reordering.
7. A tall reply scrolls **row by row** through its whole height; `G` lands on
   the transcript's true last row.
8. Auto-follow: pinned follows tokens down, scrolling up holds position while
   the stream continues underneath, scrolling back down re-pins on its own.
9. Slot focus (`n`/`p`), expand (`o`) and retry (`ctrl+r`) all act on the
   entry the `▌` bar marks — including on a **failed** slot, which was the
   case that had no visible focus at all before.
10. Tabs: nothing overflows at any width, in chat replies or expanded slots.
11. **Idle CPU:** Point sits under 1% idle (averaging ~0.4%), 2–3% while
    streaming (real render work), with `llama-server` carrying 55–70%.
12. **Scroll cost, reloaded vs live**, four runs: 1845/1642, 1603/1704,
    1508/1305, 2502/1653 ns per step. The ordering flips between runs, so the
    difference is noise; all ~1.3–2.5µs against a 16ms frame (~0.01%). No
    reload-path regression.

## Bugs caught during manual testing

Nine defects across four rounds. The five with their own sections above are
the ones whose *mechanism* matters; the rest are listed here in full.

| # | Symptom | Cause | Fix |
|---|---|---|---|
| 1 | Q1·Q2·A1·A2 — every question above every answer | User message and reply went into two different lists | One ordered list — see "Bug 1" |
| 2 | Slot could not be focused / expanded / retried | Same list split; indices resolved to the wrong entry | Same fix |
| 3 | `ctrl+R` retried the wrong task | `retryLast` took "most recent", not "focused" | `retryTarget()`, focus first, carried by pointer |
| 4 | A tall reply's lower half was unreachable | Scroll stepped by entry, not row | `(topAbs, topRow)` — see "Bug 2" |
| 5 | ~1.6% of a core, continuously, on an idle app | `writeSend` left entries in `statusSending` forever | Every failure path ends the entry — see "Bug 3" |
| 6 | Tab-indented code overflowed and wrapped left | `StringWidth("	") == 0` | `expandTabs` — see "Bug 4" |
| 7 | Reload invented "retried" labels, orphaned answers | Adjacency cannot identify a retry | The fold was **removed** — see "Bug 5" |
| 8 | A late `TaskStarted` reopened a cancelled entry, and leaked a backend task nobody cancelled | `TaskStarted` set `statusRunning` unconditionally | Guard on `wireTerminal()`; send a real `CancelTask` with the id it finally learned |
| 9 | A sent message rendered under a "Point:" label | `renderLiveEntry` dispatched on lane, and a user entry has a lane field | Dispatch on **kind** first, then lane |

Three further defects were found *by writing the tests* rather than in
manual verification, and never reached Aryan: holding `j` at the bottom drifted
the scroll position past the end of the transcript; scrolling up from pinned
never unpinned; and a timed-out task that resumed kept its "no response"
footer while its real completion was ignored (the `terminal` vs
`wireTerminal` split).

## What the next episode inherits

**State:** Point is usable. Chat streams, a second message can be composed
while the first replies, both lanes are visible, cancellation is clean, and
interruption renders identically whether the user or a backend crash caused
it.

### F6 (Right Pane) and F9 (Task Tray, Cards)

Use the identifiers in **"The two contracts"** above, exactly. In particular:
read the lane from `TaskStarted`'s `background` field rather than re-deriving
it; treat `task.cancel.accepted` as an ack, not an outcome; and render
`bounds_exceeded` as truncated-but-complete rather than as a failure.

### Accept / reject / edit is F9's, not F5's

`F05-streaming-queue.md`'s Do-NOT says "Build result cards (F9)", and
`F09-task-tray-cards.md` owns both surfaces — the **Result card (UI-06)**
(`a attach full · s attach summary · m save to memory ▸ · r re-run · d
discard`) and the **Diff card (UI-07)** (`⏎ apply · e edit · d discard`).

**Dependency chain: F5 → F7 → F8 → F9.** Until F9 exists, an agent result
simply appears in the transcript, which is the expected interim behaviour.

### F4's static agent map is now a shared stopgap

`input_render.go`'s `knownAgentIDs` has a **second** call site: `splitAgent`
in `chat\stream.go`. There is no `ListAgents` command in the protocol.
Whichever episode adds one (F8 or F10) must replace **both** call sites at
once, not just F4's.

### Owed from earlier episodes, still owed

`chat\textutil.go` still duplicates `app\box.go`'s width primitives. F1's
handoff flagged lifting them into a shared `ui` package as F3-or-F7 work; F3
deferred it, F5 deferred it again and added `expandTabs` to the duplicate.
Still owed.
