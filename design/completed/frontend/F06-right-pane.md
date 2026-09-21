# Episode F6 — Right Pane ✅

**Three independently bordered cards — Status, Task, Sessions — plus a
fallback overlay for terminals too narrow to hold them. The Task card is a
global, root-owned list; the Sessions card is the workspace's own. The
context fill bar is deliberately a marked stub, because B8's `context.report`
has no wire path yet.**

> Verified by Aryan across seven rounds against
> `tasks/frontend/F06-right-pane.md`'s eight "Done when" criteria, on a real
> terminal, against a live backend.
>
> **Two ambiguities stopped the session before code, as the episode's own
> instructions require**, and both were load-bearing:
>
> 1. **`context.report` has no wire path at all.** The Build section says the
>    Status card and the expanded context card are "driven by `usage.updated`
>    and `context.report` events — push, never polling". `usage.updated`
>    exists; `context.report` does not — there is no `GetContextReport`
>    request in `IncomingEventType` and no `ContextReport` event in
>    `OutgoingEventType`. B8's `context.report()` is only ever called from
>    `cli.py`. Aryan's call: ship the card against a marked stub, do not
>    build the wire plumbing here, and spec it as **B20**.
> 2. **The Ollama `⌂ local · 4.7GB` marker has no backing data.**
>    `providers/ollama/provider.py` never calls `/api/ps` and nothing carries
>    model size to the frontend. Aryan's call: **cut it, do not stub it** —
>    a stub for it would be decoration, not a placeholder. Same treatment as
>    F4's document-extraction parking-lot item.
>
> The longest part of this episode was not building it. It was one field
> report — "the overlay shows nothing until the first task completes" —
> that took **four code-level reproduction attempts and three rounds of log
> instrumentation** to find, and whose real cause was none of the four
> things it looked like. See "The pattern this episode is really about".

---

## Where the code went

```
frontend\internal\
├── app\
│   ├── tasks.go        NEW — TaskRow, the five TaskState values
│   ├── root.go         rightPaneOpen; tasks/tasksReqID/tasksDirty/
│   │                   taskCardWasVisible/diagnosticsSeq; rightPaneVisible,
│   │                   refreshTasks, refreshTasksIfVisible, sendListTasks,
│   │                   handleTaskListEnvelope, closeRightPaneFallbackIfObsolete
│   ├── workspace.go    OverlayProvider, RightPaneFallback
│   ├── placeholder.go  Diagnostics gains Seq + Tasks; diagnosticsMsg is now
│   │                   EXPORTED as DiagnosticsMsg (chat reads it too)
│   └── actions.go      pane.right.toggle (leader `c`)
└── chat\
    ├── cards.go        NEW — the three card renderers, taskLine,
    │                   singleLineTitle, sideBySide, rightPaneOverlay
    ├── render.go       View composes the right column; rightPane()
    ├── chat.go         Cards() is real; tasks/backendDown/diagnosticsSeq/
    │                   sessions/usage/sessionProvider/sessionModel
    ├── protocol.go     DiagnosticsMsg case; sessions retained; usage seeded
    │                   from the reloaded transcript
    ├── stream.go       liveEntry.usage; EvUsageUpdated split out of the
    │                   "evidence of life" bucket and actually read
    └── model.go        wireSession gains Model

backend\managers\session_manager.py   _sanitize_title, applied in create()
                                      and rename()
```

Tests: `cards_test.go`, `root_toggle_test.go`, `diagnostics_race_test.go`,
`overlay_concurrency_test.go` (chat); `tasks_test.go` (app).

**One backend file changed, outside F6's nominal scope, at Aryan's explicit
direction** — see "The session-title newline" below. It is recorded here
rather than in a backend episode doc because this episode is where it was
found and fixed.

---

## The task-card entry shape — F9 MUST use these names

**F9 appends result cards into this same list.** A near-match that compiles
but differs on one field name gives a tray silently showing a mix of two
shapes, so this is copied from the code, not described.

```go
// app\tasks.go — mirrors ONE entry of ListTasks' payload
// (backend\runtime\runtime.py's _handle_list_tasks).
type TaskRow struct {
    ID         string  `json:"id"`
    Kind       string  `json:"kind"`
    State      string  `json:"state"`
    Session    string  `json:"session"`
    Agent      string  `json:"agent"`
    CreatedAt  float64 `json:"created_at"`
    StartedAt  float64 `json:"started_at"`
    FinishedAt float64 `json:"finished_at"`
    ErrorCode  string  `json:"error_code"`
}

const (
    TaskStateQueued    = "queued"
    TaskStateRunning   = "running"
    TaskStateCompleted = "completed"
    TaskStateFailed    = "failed"
    TaskStateCancelled = "cancelled"
)
```

Four things F9 has to know about this shape:

1. **`Kind` is ALWAYS `"chat"`. Use `Agent`.** Every task the agent executor
   creates — a plain conversational turn included — is submitted with
   `kind="chat"` hardcoded (`backend\agents\executor.py`'s `create_task`
   call). The capability that actually ran (`research`, `general`, …) lives
   in `agent_id`. `runtime\task.py`'s own doc calls `kind` opaque. F6
   originally preferred `Kind` and displayed "chat" for an `@research` task;
   `taskLine` now reads `Agent` first and falls back to `Kind`.
2. **The timestamps are epoch float seconds**, Python's `time.time()`, the
   same shape as `wireMessage.Timestamp`. `StartedAt` is `0` for a task that
   has not started; `elapsedSince` renders `""` rather than a bogus clock.
3. **`PAUSED` and `AWAITING_APPROVAL` exist in `runtime\task.py` and are
   deliberately absent here.** Nothing produces them yet. `taskLine`'s
   switch has a `default` that renders any unrecognised state as done rather
   than dropping the row.
4. **The list is global — every workspace's tasks, not the active
   session's.** F6's spec is explicit that the root owns it, which is why
   `TaskRow` lives in `app` and not in `chat`.

`protocol.IsTerminal` and the `TaskStarted` / `background` pair from
`F05-streaming-queue.md` are still the lifecycle vocabulary; this struct is
the *list* shape, not a replacement for them.

---

## The pattern this episode is really about

**An invariant maintained by N call sites remembering to update it will
eventually have one that forgets. Decide it once, centrally, after the
fact — the way `ensureAnimation()` already does.**

This codebase already had the answer written down. `Root.Update`'s own doc
comment says:

> Update wraps the real update so ONE place decides whether the header
> animation should be running. Streaming starts and stops in a dozen
> different messages (an envelope, a key, a tick of chat's own); asking
> afterwards, once, is what keeps "the animation runs only while something
> streams" from being a rule every new case has to remember.

F6 then produced **two separate real bugs by not following it**, in two
different forms. Both cost multiple verification rounds. Neither was
findable by reading the function that looked wrong, because in both cases
that function was correct — what was wrong was an assumption it depended on
being maintained elsewhere.

**Recognise the smell:** a boolean, sequence number, or cached snapshot
that several code paths are each individually responsible for keeping
accurate. Ask whether it can be *derived* once, at a single choke point,
instead.

### Instance 1 — ordering spread across goroutines (`Diagnostics.Seq`)

`pushDiagnostics` returns a `tea.Cmd`. **Bubble Tea runs every returned Cmd
in its own goroutine, and gives no ordering guarantee between them.** Two
pushes close together — a `TaskDone` envelope, immediately followed by the
`ListTasks` reply it triggered — can therefore be delivered to `Update` in
either order. When the older one landed second, it silently overwrote the
newer snapshot, and nothing corrected it until some later event pushed
again.

The fix is not to make delivery ordered — it cannot be. It is to stop
trusting arrival order at all:

```go
// app\root.go — pushDiagnostics
r.diagnosticsSeq++
d := Diagnostics{Seq: r.diagnosticsSeq, /* ... */}

// chat\protocol.go — the receiver
if m.D.Seq > w.diagnosticsSeq {
    w.diagnosticsSeq = m.D.Seq
    w.tasks = m.D.Tasks
    w.backendDown = m.D.ConnState == protocol.StateFailed
}
```

`Seq` is assigned **synchronously**, inside the `Update` call that builds
the snapshot, so a higher `Seq` can never carry older data. The receiver
gates the **whole message**, never per-field — a per-field comparison would
allow a frame mixing one push's tasks with another's connection state.
`app.Placeholder` applies the same guard; it is the other consumer of the
same message.

**Anything else that later rides `Diagnostics` inherits this for free, and
must not bypass it.**

### Instance 2 — a visibility flag spread across five call sites (`taskCardWasVisible`)

This is the one that cost four reproduction attempts.

`refreshTasks` deliberately sends **nothing** while the pane is hidden —
that is Done-when #6, and it is correct. The consequence is that the
moment the pane *becomes* visible is the **only** chance to learn about
work that started while it was not. `refreshTasksIfVisible` detects that
transition by comparing against `taskCardWasVisible`.

That flag was updated only inside `recompute()`. **Five paths change
whether the right pane is visible, and three of them never call
`recompute()`:**

| path | calls `recompute()`? |
|---|---|
| resize crossing the 90-column breakpoint | ✅ |
| `pane.right.toggle` action | ✅ |
| `OpenOverlay` | ✅ |
| `esc` closing the overlay (`dispatchKey`) | ❌ |
| click-outside closing it (`dispatchMouse`) | ❌ |
| `closeOverlayIfTooSmall` | ❌ |

So the flag stayed `true` across a close. **Reopening then registered as
"no transition at all" and refreshed nothing.**

Why that produced exactly the reported symptom: sending a message requires
the overlay closed (esc), so on a narrow terminal every task necessarily
starts while the pane is hidden — every `TaskStarted` correctly no-ops.
Reopening was the only remaining chance, and it was skipped. The next
refresh therefore came from whatever lifecycle event fired next while the
overlay happened to be open — **the first `TaskDone`**.

The fix is the same shape as `ensureAnimation`:

```go
func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    model, cmd := r.update(msg)
    r.refreshTasksIfVisible()   // ask ONCE, after every message
    if anim := r.ensureAnimation(); anim != nil { /* ... */ }
}
```

and the call was **removed** from `recompute()`, so there is exactly one
place that decides.

**Why four tests missed it:** every test opened the overlay for the *first*
time, where `taskCardWasVisible` starts `false` and the transition fires
correctly. None closed and reopened. The user's workflow does that on every
single cycle. `TestReopeningTheOverlayRefreshesWorkStartedWhileItWasClosed`
now covers it, and also asserts the hidden pane still sends nothing, so the
fix cannot regress Done-when #6.

---

## What each card actually shows

### Status
```
context: not wired yet (B20)     ← STUB, see below
2.4k in · 10 out                 ← real, last turn's usage
anthropic · claude               ← real, from the session
```

1. **Context fill — a marked stub.** There is no `context.report` on the
   wire. The row says so in words rather than drawing a bar B8 never sent,
   because a fabricated bar is indistinguishable from a real one that is
   lying. B20 wires it; a frontend follow-up then replaces this line.
2. **Tokens — real, and per-turn, not a session total.** `w.usage` is
   *overwritten* by each `UsageUpdated` and seeded on reload from the newest
   message carrying non-zero usage. **Cost is omitted, never zeroed**, per
   spec — and it is omitted permanently, because the backend has no pricing
   table anywhere (`managers\message.py`'s `TokenUsage` defers billing to a
   later episode).
3. **Provider · model** — from the session row. `wireSession` gained
   `Model`; the wire always sent it and nothing decoded it until now.

**The token counter updates only at end of stream, and that is correct by
design, not a defect.** All three providers yield exactly ONE `USAGE` chunk,
positioned immediately before `DONE` — Anthropic `provider.py:110`, OpenAI
`provider.py:136`, Ollama `provider.py:131`. F5's per-delta guarantee is
about *text*; there is no incremental usage data to render sooner. Checked
twice, at Aryan's request, and closed both times.

### Task
Real, root-owned, and driven by real backend queueing (B12). Rows sort
running → queued → terminal, stably, so recency order survives inside each
group. `+N more` closes the tail when rows run out — see the bug table.

### Sessions
Recent sessions, active one marked, titles truncated by **display width**
with an ellipsis. `singleLineTitle` strips newlines and control characters
before anything measures the title — see the bug table.

---

## Bugs caught during verification

Ten, across seven rounds. The two that matter most are the pattern above;
these are the rest.

| # | Symptom | Cause | Fix |
|---|---|---|---|
| 1 | Task card showed one task at a time, swapping identity | `refreshTasks` DROPPED a trigger that arrived while a request was in flight, with nothing remembering the answer would be stale | `tasksDirty` coalescing: fire exactly one follow-up when the in-flight request resolves |
| 2 | `@research` task labelled "chat" | `Kind` preferred over `Agent`; `Kind` is always `"chat"` | prefer `Agent`, fall back to `Kind` |
| 3 | Queued icon rendered as literal `...` | `⋯` (U+22EF) was a glyph used nowhere else in this codebase | use `◌`, which `stream_render.go` already uses for "◌ queued" |
| 4 | State words truncated in the overlay (`canc`, `do`), session titles cut with no ellipsis | **F7's own documented gotcha #3**: `box.go`'s `fitCells` pads one more cell each side *after* `View` returns, so the real budget is `w-2`. F6 forwarded `w` straight through, and `fitCells`' blind `truncateCells` then cut 2 cells off every line — after the renderer's own ellipsis logic had already run | budget `w-2` in `rightPaneOverlay.View` |
| 5 | Stale "right pane shown" text under a later overlay | the toggle set a status string with no expiry; a later *resize* auto-hid the pane through a path that never touches `r.status` | removed the status text — the pane's own presence is the feedback, and it is always live |
| 6 | Both the real pane AND the overlay visible after widening | `closeOverlayIfTooSmall` closes an overlay that no longer FITS; nothing closed one that had become UNNECESSARY | `RightPaneFallback` marker + `closeRightPaneFallbackIfObsolete` on resize |
| 7 | Overlay's Task section never updated live | `refreshTasks`' gate checked only `layout.ShowRightPane`, which is false *by definition* while the overlay is the visible surface | `rightPaneVisible()` — true for either surface |
| 8 | Sessions card border broken | a real DB row whose **title contained a raw `\n`**; the string measured correctly as one line and the terminal drew it as two | `singleLineTitle` at render, `_sanitize_title` at write |
| 9 | Task card showed 5 of 50 rows with no sign of the other 45 | silent truncation | `+N more` |
| 10 | Three tests failing intermittently under `-race` | all three asserted **wall-clock timing**, not behaviour | elapsed *bands*, idle-based cmd draining, generous ceilings |

### The session-title newline (#8) — fixed at both layers

Two real rows carried `"# heading\n**bold** and \`inline\` and 日本語"`. It
looked like a markdown bug and then like a CJK width bug. It was neither:
`sessionsCardLines` measured and truncated it as one line **by assumption**,
the display width was correct, and the terminal drew the second half on the
row below — outside a box border that had already closed.

- **Render side:** `singleLineTitle` — a title is single-line *by
  construction* now, not by assumption. CJK in the same title measures and
  truncates correctly through `runewidth.StringWidth` once the newline is
  gone.
- **Write side (backend, at Aryan's direction):** `_sanitize_title` in
  `SessionManager.create()` and `.rename()` — the only two places a title is
  ever written, so `CreateSession`, `RenameSession` and any future caller
  are covered at one choke point. `_generate_title`'s own
  `splitlines()[0][:60]` guard is why the *auto-generated* path was already
  safe; the malformed rows came through the request handlers, which had no
  equivalent. Verified from the database: stored titles now contain no `\n`.

### Bug 10 is worth reading even though it is test-only

Three of this episode's own tests asserted on wall time: a 3s settle budget
(measured at 2.07s under `-race`), a 50ms cmd-drain that dropped the very
message it was waiting for, and an exact `"1:30"` clock assertion that
legitimately rendered `"1:31"`. **A suite that fails intermittently trains
you to ignore failures**, which is strictly worse than not having the test.
The rule that came out of it: a timeout in a test is a *ceiling on how long
a broken run may hang*, never an assertion about how fast a correct one is.
Verified with 8× and 5× repeated `-race` sweeps, clean.

---

## Layout: what Compute actually chose

`Cards()` returns `{MinHeight: 5}, {MinHeight: 8}, {MinHeight: 6, Flex: true}`.

| terminal | Status | Task | Sessions | sum = body height |
|---|---|---|---|---|
| 120×30 | 5 | 8 | 13 | 26 |
| 110×35 | 5 | 8 | 18 | 31 |
| 95×30 | 5 | 8 | 13 | 26 |

**No breakpoint needed raising.** `LAYOUT.md` marks the numbers provisional
and F1 expected to revise them once real borders rendered; they held.
Verified across ten width/height combinations straddling every breakpoint —
120×30, 110×35 (empty and populated), 95×30, 89×24, 91×29, 119×31, 121×37,
73×25, 105×41 — with the left and right columns summing to exactly the same
height in every one (`TestRightColumnHeightMatchesLeftColumn`).

---

## Known limitation — for an F7 follow-up or F16

**`Overlay.MinSize` does double duty: it is both the refusal threshold and
the actual rendered size.** `overlayRect` only ever *shrinks* an overlay
(when `MinSize` exceeds the terminal), never grows it. So the fallback right
pane is permanently 36×20 — giving its Task section five rows — even on a
terminal with fifty rows to spare.

That is not cosmetic: it is why bug #9's silent truncation bit as hard as it
did, and why the overlay shows meaningfully less than the real pane on the
same screen.

Raising this overlay's `MinSize` is **not** the fix — it would raise the
refusal threshold with it, and terminals that can currently open the overlay
would stop being able to. The fix is a **preferred-vs-minimum distinction in
the `Overlay` contract**: `MinSize` keeps deciding refusal, a new preferred
or growable size decides layout. That is F7's machinery, shared by Popup and
Confirm, so it was flagged rather than changed from inside F6.

---

## The task-list scope — an activity view, not a history browser

`ListTasks` is still asked with `scope=all`, which answers with up to 50 rows
— every recent task in the database. Rendered into a five-row card that made
it a history browser, when F06-right-pane.md's own mockup is plainly an
activity view: one running, one queued, one recently done. History belongs to
F9's result cards and task tray.

`capTerminalTail` (`cards.go`) keeps **all** active work and only the
`recentTerminalLimit` (3) most recent finished rows. Capping frontend-side
rather than switching the request to `scope=active`: that scope returns NO
terminal rows at all (`runtime.py`'s `_handle_list_tasks`), so the "one
recently done" the mockup shows would need a second request. One request plus
a cap is simpler and needs no new backend capability.

**The ordering key for finished rows is `FinishedAt`, not `created_at`**, and
the distinction is not academic. `sortStableTaskRows` groups by state and
orders the terminal group by `FinishedAt` DESC, while active rows keep their
incoming `created_at` DESC order — "when it started" is the right key for work
still in flight, since that is what the elapsed clock counts from. Keyed on
`created_at`, a long `@research` created before three quick turns and
finishing after all of them was evicted in favour of work that completed
earlier. That is this app's normal workload, not a corner case. A
`FinishedAt` of 0 — a row whose finish was never recorded, which this
database has real examples of from killed backends — sorts last among
terminal rows, which a DESC comparison gives for free.

**`+N more` counts BOTH kinds of omission**: rows the scope cap dropped and
rows the height budget could not fit. It did not, at first, on the reasoning
that history is "out of scope rather than hidden". That reasoning was wrong
in the only way that matters — firing ten tasks and seeing three, with
nothing saying so, is indistinguishable from a card that is broken. Whatever
the card's scope is, the number it is not showing is information the reader
needs, and silence is the one answer that cannot be checked.

## Three bugs found at the gate that were NOT the right pane's

The last verification round surfaced three things that looked like F6
defects. One was, one was environmental, and one was a real backend
correctness defect this episode found but deliberately did not fix.

### Bug A — the Task card hid rows silently ✅ fixed here

Ten tasks fired, three shown, no indicator. Fixed: `+N more` now counts both
the scope cap and the height budget. See "The task-list scope" above for the
reasoning and why the original call was wrong.

### Bug B — a background task and a plain turn did not stream together — environmental

Diagnosed twice and never F6's code. `agents/executor.py:525` gives EVERY
task the provider lane; only inline turns additionally take the turn lane,
and `scheduler.submit` waits for a slot in **every** lane a task names. With
`[limits.ollama] max_concurrent_tasks = 1` — the setting F6's own Done-when
#3 requires — an `@research` holds the only provider slot and a plain turn
queues behind it. With the cap raised to 3 the remaining serialization is
**Ollama's own**: a local instance runs one generation at a time unless
`OLLAMA_NUM_PARALLEL` is raised. Environment setting, not code.

Worth recording separately: **B12's scheduler docstring oversells this.** It
says three background runs can saturate the provider lane "without the user
losing the ability to ask a question". That holds for the *turn* lane only —
the provider lane is shared, so at cap 1 a question genuinely does block.
The mechanism is right; the prose is not.

### Bug C — a failed task poisons the NEXT prompt → deferred to B21

The serious one, and the reason this episode's gate ran long.

**Symptom:** send `@research <x>`, it fails with zero output, then send
something unrelated — and the model answers the *research* question instead.
Reproduced deterministically with nothing in flight and no concurrency:
`@research importance of sleep` with `--max-seconds 1` (fails, zero output),
then "write a short paragraph about oceans" → a paragraph entirely about
**sleep**, no mention of oceans.

**Cause:** `executor.py:464` persists the user's message before the task
runs; a task that fails before its first token persists no assistant row
(B12's deliberate decision); `context.py:886` then builds the next prompt
from `recent_messages`, and nothing filters an unanswered question. The
orphan is carried forward as ordinary conversation.

**Not F6's to fix**, and not fixed here. Specced as
`design/tasks/backend/B21-orphaned-message-context.md` — exclude a user
message whose `task_id` maps to a failed, empty-output task, using the same
two-table join S11b already built for transcript footers.

**The eliminations are worth as much as the conclusion**, because three of
them looked far more likely than the cause did. Ruled out, in order, each by
trace rather than argument: prompt concatenation in the context builder
(context is built once, before the task exists, and `_loop_body` takes a
copy — it never re-reads the session); `entryFor` mis-routing one task's
deltas onto another's entry (disproven by
`TestConcurrentTasksNeverMixTheirStreamedContent`, which passes); two tasks
sharing one accumulator (the database holds exactly one correct row per
task_id); and a display/repaint fault (the persisted row carried the same
merged content, so it was never a rendering question).

**What actually resolved it was a log line, not a theory** — one line per
finished reply in `chat\stream.go`'s `finish()`, carrying the entry's
corr_id, task_id, byte count and the head and tail of what it accumulated.
That single line showed the research task at `bytes=0, status=failed` and
the plain message at 7261 bytes spanning both topics, which eliminated every
frontend hypothesis at once and pointed at assembly rather than delivery.
**It is still there, and it is the first thing to reach for if anything like
this recurs.**

## Forward notes for later episodes, not yet specced

Two ideas raised during this episode, recorded so they are not lost.
**Neither has code to write now, and neither should be built
speculatively** — both are blocked on something that does not exist yet,
and building either against a guessed data shape would mean reconciling it
later with the real one.

### Task-card dismissibility

Removing a finished task from the card once it has been reviewed.
**Blocked on F9.** F6's own "Do NOT" already defers the "open card" action
to F9's result/diff cards, and dismissal has the same dependency for the
same reason: there is nowhere for "I have seen this, clear it" to lead
until a result card exists to have been seen. F6's spec is silent on
dismissal entirely — it is neither required nor forbidden here.

Revisit once F9 lands, and decide it there rather than bolting a clear
action onto a card whose rows cannot currently even be focused (the right
pane is not in `FocusAware`'s rotation — see "What the next episode
inherits").

### A richer usage / token model

Raised this episode: rolling time windows, a purchased-token balance,
per-session running totals — as against the single per-turn figure the
Status card shows today.

**Blocked on B20.** Real session-level token data arrives with
`context.report`; until that event exists, the only usage on the wire is
one `TokenUsage` per turn, reported once at end of stream (see the Status
card section). Anything built now would be inventing a data model the
backend cannot yet feed, and cost in particular has no source at all —
there is no pricing table anywhere in the backend.

This is **closer to a small feature spec than a bug fix**, and it should
get a real task doc once B20 exists and the actual shape of
`ContextReport` is known — not a patch onto the Status card.

## What the next episode inherits

**B21 is owed, and it is the more urgent of the two.** A failed, empty-output
task poisons the next prompt in the same session — see "Bug C" above and
`design/tasks/backend/B21-orphaned-message-context.md`. It is a backend
correctness defect found at F6's gate, not a right-pane issue, and nothing in
this episode works around it.

**B20 is owed, and F6 is blocked on it for one row.** Spec: a
`GetContextReport` request and a `ContextReport` push event, driven off
`usage.updated` / turn boundaries, **calling B8's existing `context.py`
calculation — never a second implementation of the arithmetic.** A frontend
follow-up then replaces the Status card's stub line and builds the expanded
context composition card (UI-09), which is **not built at all** here: with
no section data, an expand gesture would open an overlay that repeats
"not wired yet", which is the same decoration the Ollama line was cut for.
**Done-when #2 (sections sum to the total, eviction named) is therefore not
satisfied by this episode**, by direct consequence of the decision above.

**F9 — your result cards append into the Task card's list.** Use `TaskRow`'s
field names exactly (section above), prefer `Agent` over `Kind`, and note
that `⏎`-to-open is not wired: the right pane is not in `FocusAware`'s
rotation, so nothing can currently focus a card row. F6 deliberately did not
invent a focus path for a destination that does not exist yet — decide
whether the tray owns that, and wire it there.

**Task dismissal is deferred**, by Aryan's decision, to F16 or its own doc,
once F9's result cards give a "clear" action somewhere to lead to. F6's spec
is silent on it; nothing was built toward it.

**New narrow interfaces, same pattern as `FocusAware` / `DraftFlusher`:**

- **`app.OverlayProvider`** — a workspace that can hand the root an overlay
  standing in for its right pane.
- **`app.RightPaneFallback`** — marks that overlay so the root can retire it
  on a resize, and count it as "the pane is visible" for refresh purposes.

**`app.DiagnosticsMsg` is now exported** (was `diagnosticsMsg`), because
`chat` reads `Tasks` and `ConnState` off it. Any new consumer must respect
`Seq` — see instance 1 above.

**`Root.ListTasksFn` is an exported seam**, the same shape as
`chat.Workspace`'s `sendFn`: a test substitutes a recorder so the coalescing
and visibility logic can be exercised without a live `protocol.Client`. It is
exported rather than package-private only because the tests that need it live
in `chat`, which cannot be imported from `app`.

**A real concurrent test harness now exists** in
`chat\overlay_concurrency_test.go`: a `fakeBackend` plus a `concurrentDriver`
that spawns every `tea.Cmd` in its own goroutine, the way Bubble Tea actually
does. Three earlier reproduction attempts passed only because they drove
messages synchronously and imposed an ordering Bubble Tea never promises.
**Any future episode chasing a "works in tests, fails live" bug should reach
for this harness first.**

**Still owed, now from five episodes.** `chat\textutil.go` duplicates
`app\box.go`'s width primitives; F6 added a fourth copy of `sideBySide` to
it. F1 flagged lifting them into a shared `ui` package as F3-or-F7 work; F3,
F5 and F7 each deferred, and so did this one.

**Do not proceed on the assumption that the overlay and the real pane share
a render trigger by accident.** They share it by construction: there is one
`View()`, it recomposes the entire frame every call, and nothing subscribes
to anything. Several rounds of this episode were spent testing a
subscription model that does not exist.
