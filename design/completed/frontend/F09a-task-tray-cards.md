# F9a — Task Tray and Result Cards (F9, SPLIT) 🟡

**The global task tray (UI-05) and the result card's render/discard half
(UI-06) ship. Background work is visible and openable from anywhere, including
for a task whose originating view has been unloaded, and discarding a card
never touches the result. F9's diff card and the result card's attach/save
axes are DEFERRED, each on a dependency that does not exist in this repo —
four of the episode's eight "Done when" criteria assume things that are not
true, and this doc records them with evidence so the sessions that resume them
do not re-derive any of it.**

> 🟡 **SPLIT, not complete.** F9 closes only when F9b (Session 18b) and F9c
> (Session 23b) land. Two of eight Done-when criteria are met; six are
> deferred, itemised below with what each is blocked on.
>
> Built and self-verified against the narrowed scope Aryan authorised after
> the blocker report. Eight rounds of manual verification, which found six
> further defects — four of them one cross-cutting pattern (see **The
> build-time/render-time pattern**), one in F7's shared `Confirm`, and one in
> `array.Menu.Top()` that predates this episode and affects Help.
>
> `go test ./frontend/... -count=1 -race` clean from a cold test cache.

---

## Where the code went

```
frontend\internal\app\
├── tray.go           NEW — the tray (UI-05): trayState, openTaskTray,
│                     taskTray (Popup + the TaskTrayView marker),
│                     trayRows (the background filter), trayDecor,
│                     trayNote, the m:ss clock, cancel, and the
│                     GetSession round trip that fetches a result
├── resultcard.go     NEW — the result card (UI-06): render, scroll,
│                     discard, wrapParagraphs
├── tasks.go          + the task-row vocabulary, now the ONE copy:
│                     TaskPriority/TerminalPriority/TaskFinished,
│                     TaskLabel, SortTaskRows, TaskGlyph, TaskRole,
│                     TaskElapsed, TaskDuration, and the built-in
│                     agent table (IsKnownAgent/IsBackgroundAgent/
│                     KnownAgentIDs)
├── popup.go          + RowDecor, PopupConfig.Decor, StickySections,
│                     RefreshItems, stickyHeader, popupGeom
├── overlay.go        + MaximisedOverlay, matchTranscriptPane
├── confirm.go        ~ MinSize computed from its own buttons;
│                     renderButtons hugs each label
├── root.go           + trayState/trayClock/backgroundSeen fields,
│                     tasksOnScreen, trayTickMsg, handleTrayEnvelope,
│                     noteTaskBackground, pruneBackgroundSeen;
│                     overlayRect honours MaximisedOverlay
└── actions.go        + tasks.tray (chord f1, leader t)

frontend\internal\array\
└── menu.go           + SelectByID; Top() scrolls to the top;
                      ClickRow bounded by the visible window

frontend\internal\chat\
├── cards.go          ~ reads app's task-row vocabulary; its own
│                     copies deleted (46 lines shorter, one import fewer)
└── input_render.go   ~ reads app's built-in agent table; knownAgents deleted
```

**No backend change.** No new dependency. No file-write path added anywhere —
there still is none, which is CLAUDE.md's first hard rule enforced
structurally rather than by care.

---

## Done when — two closed, six deferred

| # | Criterion | State |
|---|---|---|
| 1 | Tray lists tasks from every workspace; opens results whose originating view has been unloaded | ✅ |
| 8 | Discarding a card leaves the task result retrievable from the tray | ✅ |
| 2 | Attach and save independently exercisable | ⛔ Session 23b |
| 3 | Editing a memory changes what an attached reference renders | ⛔ Session 23b |
| 4 | Saving produces `status=accepted`, `source=user_card` | ⛔ Session 23b |
| 5 | Diff apply lands the **edited** text | ⛔ Session 18b |
| 6 | Editor apply undone by a single `ctrl+z` | ⛔ Session 18b |
| 7 | File changed on disk → apply refused with a reason | ⛔ Session 18b |

---

## The four blockers, with evidence

Recorded so **Session 18b and Session 23b do not have to re-derive them.**
Line numbers are as of this episode; the named symbol is the stable reference.

### 1. There is no Editor workspace to apply a diff into

- `WorkspaceEditor` is a bare layout constant —
  `frontend\internal\layout\layout.go:18` (`WorkspaceEditor WorkspaceID = "editor"`),
  aliased at `frontend\internal\app\workspace.go:17`.
- `frontend\internal\` contains eight packages and **no `editor`** one. The
  only `Workspace` implementation is `chat`.
- F13 (Editor Core) is **WORKFLOW.md Session 18**; F15 depends on F13 *and*
  F9. So the diff card's only specified apply target and its only specified
  trigger both belong to later sessions.
- There is also no fallback, and that is correct: no `WriteProjectFile` event
  exists, so "apply" has nowhere to go but a buffer.

**Blocks:** Done-when #5, #6, #7. **Resumes:** Session 18b (F9b).

### 2. A task result cannot be attached

- `context_attachments.source_type` only ever holds `file` —
  `backend\managers\attachment.py:53` (`SOURCE_TYPE_FILE = "file"`).
- The context builder **refuses anything else outright** —
  `backend\managers\context.py:564`:
  ```python
  if attachment.source_type != SOURCE_TYPE_FILE:
      ... detail=f"unsupported source_type {attachment.source_type!r} (B12)"
  ```
- `AttachContext` requires a `path` and resolves it through the file guards. A
  task result is not a file.
- **Also a spec/reality mismatch worth settling in 23b:** F09 offers
  `a attach full` / `s attach summary`, but the three modes are
  `full | reference | detached` and F09's own gate expects `reference`. Either
  `summary` is a third real mode the builder must implement, or the key is
  mislabelled. Not guessed at here.

**Blocks:** Done-when #2, #3. **Resumes:** Session 23b (F9c), which needs an
attachment-source extension that **is not yet an episode** — deliberately, because
B13's own shape decides what a memory attachment is.

### 3. Nothing writes `memory_records`

- The table has had exactly the right columns since schema v2 —
  `backend\database\migrations.py:193`, with
  `status TEXT NOT NULL -- pending | accepted | rejected` and
  `source TEXT NOT NULL -- auto_extractor | user_card | user_manual`.
- There is **no memory repository** in `backend\database\`, no
  `MemoryService`, and no `SaveMemory` incoming event.
- What exists is B11's deliberate stopgap: `agent_proposals` (migration v4),
  `ProposalRepository` with insert/count/fetch only, one status (`pending`).
  Both `backend\managers\memory.py` and
  `backend\database\proposal_repository.py` say in so many words that
  `accept`/`reject` against `memory_records` is **B13's** — "do not add them
  here."

**Blocks:** Done-when #4 (and #3 jointly). **Resumes:** Session 23b, after
Session 23 (B13).

### 4. The tray's mockup labels have no wire source

- `ListTasks` returns exactly `id, kind, state, session, agent, created_at,
  started_at, finished_at, error_code, live` —
  `backend\runtime\runtime.py:1014-1047` (`_handle_list_tasks`).
- The `tasks` table has **no result column and no title** —
  `migrations.py:143`.
- `usage_records` has tokens and a `task_id` but **no repository and no wire
  event**; `UsageUpdated` is live-stream-only, so a finished-and-swept task's
  cost is unreachable from the tray.
- `kind` is useless as a discriminator: `backend\agents\executor.py:534`
  hardcodes `kind="chat"` for every task.

**Consequence, shipped rather than faked:** tray rows show the agent, the
state glyph, and a duration computed from the two timestamps. They do **not**
show the topic or a token count. The *result card* shows both, because by then
it has fetched the session's messages. Showing them for whichever session
happens to be loaded and not for the rest would make the list look broken
rather than partial.

---

## How a finished task's result text is retrieved

**F9b and F15 read this same path. It is written out in full here so neither
re-invents it.**

There is no `GetTaskResult` on the wire and `tasks` has no result column. What
a task produced is its assistant message(s), joined on `messages.task_id`.

**The request** — `frontend\internal\app\tray.go:598` `fetchTaskResult`:

| | |
|---|---|
| Event | `GetSession` (`protocol.CmdGetSession`, `frontend\internal\protocol\types.go:16`) |
| Payload | `{"session_id": <TaskRow.Session>}` |
| Correlation | `rootRequestID()` — Root's own id, **not** chat's `localID()` |

**The reply** — `tray.go:637` `handleTrayEnvelope`, claimed by `corr_id`:

| | |
|---|---|
| Event | `Session` (`protocol.EvSession`) |
| Payload field | `session.messages` → `[]trayMessage` (`tray.go:714`) |
| Fields read | `task_id`, `role`, `content.text`, `usage.{input,output,total}_tokens` |

**The join** — `tray.go:748` `splitTaskMessages(taskID, msgs)` returns
`(question, answer, tokens)`:

- Keyed on **`task_id` and nothing else.** This is `reload.go`'s permanent
  rule for this codebase, for the same reason: adjacency, arrival order and
  elapsed time all break the moment two sends overlap, and B12 added the
  column so they do not have to be guessed at.
- The **user** message with that `task_id` is the question → the card's title.
  This is the only place F09's "Research · tokio vs asyncio" becomes true.
- **Every** assistant message with that `task_id` is joined with `\n\n` — a
  tool loop persists more than one, the same way
  `backend\tools\builtins\task_results.py:41` joins them for the
  `load_task_result` tool.
- Tokens are **summed across the turn**, falling back to
  `input_tokens + output_tokens` when `total_tokens` is 0 (rows written before
  it was populated). An unknown cost renders as no claim, never as `0 tok`.

**Three things that matter and are not obvious:**

1. **Chat never sees this reply.** `chat\protocol.go:212` drops any envelope
   whose `corr_id` is not in its own pending map ("not one of ours"), so a
   tray fetch for an arbitrary session cannot make the Chat workspace switch
   sessions. Verified directly.
2. **The whole session comes back**, which is more than this needs. That is
   the honest cost of reusing an existing event rather than inventing one; a
   per-task result event is a backend episode and this one did not get to grow
   it.
3. **The fetch is keyed to the TASK, not the session** (`trayState.resultTask`),
   so a reply for a task the user has since navigated away from is dropped
   rather than opening a card they did not ask for.

---

## The build-time / render-time pattern

**Four defects this session were the same bug.** Future `Popup` consumers —
**F10's settings and F11's memory popup especially** — should read this before
building rows whose content changes.

> **A value computed when the list was BUILT, and never reconsidered when the
> list was DRAWN.** Scheduling a redraw cannot refresh a value computed before
> the redraw existed.

| # | Symptom | What was baked | Fix |
|---|---|---|---|
| 1 | The running task's `m:ss` clock sat frozen at `0:00`, moving only when the tray was reopened or the task finished | `trayNote()` ran in `trayItems()` and was stored in the item's **binding** | `PopupConfig.Decor` — `popup.go:159` |
| 2 | `✓`/`✕` rendered **uncoloured** in the tray but coloured on the right-pane Task card, despite one shared `TaskGlyph` | the glyph was concatenated into the item's **title**, which Popup renders in the title's own `TextPrimary` role | `RowDecor.Icon` + `IconRole` — `popup.go:119` |
| 3 | Section headings **vanished** while the rows they label stayed on screen and selectable | headings are ordinary rows injected at build time; `ensureVisible` scrolls the minimum to show the selected ITEM, so a heading one line above the window is simply gone | `PopupConfig.StickySections` + `stickyHeader()` — `popup.go:518` |
| 4 | Clicking the **footer** or a blank line opened a result for a task **not on screen** | `handleMouse`'s `m.Y >= listTop` was an unbounded half-plane, and `ClickRow` bounded the offset against the whole list, not the window | `Popup.geom` — `popup.go:52` |

**#1–#3 are build-time vs render-time. #4 is render-time vs CLICK-time**,
which is why #3's fix did not catch it — and it is the one that closes the
category.

### Why `Popup.geom` is the closing fix

`stickyHeader` solved #3 with "one helper, two callers" — `View` and
`handleMouse` consult one function so they cannot disagree. That does not
work for #4, and the reason is structural:

**How many row lines were drawn depends on the box height, and `handleMouse`
never receives one. Only `View` does.** `rowOffsets` shares the list's *top*
and nothing else; there was no shared description of where the list *ended*.
So the choice was a recorded value or a second, independent, drifting
derivation.

`Popup.geom` is written by `View` every frame (`rowsTop`, `rowCount`,
`dropped`) and read by `handleMouse`. One writer, one reader. `View` always
runs before any mouse event can target what it drew, so the recorded geometry
is exactly what is on screen — there is now **one description of the rendered
list, and both the renderer and the hit-test use it.**

### The rule for F10 and F11

- Anything **state-derived** goes in `Decor`, computed per render. If you find
  yourself calling a function at `SetItems` time and storing its result on an
  `array.Item`, that value is frozen.
- An `array.Item` is a **snapshot**: id, title, section, disabled. A binding
  is fine (a key binding really is fixed for the life of the item); a clock,
  a colour, a count or a status is not.
- Refresh a live list with `RefreshItems` (`popup.go:977`), not `SetItems` —
  it preserves the typed filter and re-finds the selection **by id** across a
  re-sort, which `SelectByID` exists for. `SetItems` deliberately discards
  both and is right for showing a *different* list (F8's picker descending
  into a directory).
- A long sectioned list wants `StickySections: true`.
- If you add a clickable region to `Popup`, bound it against `p.geom` and
  nothing else.

---

## Decisions

### The tray lists background work only

F09's "Global" bullet says "tasks from every **workspace**, since the root
owns the list", and Done-when #1 says "lists tasks from every **workspace**".
Read literally that is a statement about **scope across workspaces and
sessions** — a task whose originating view is unloaded still appears — not a
claim that every task record belongs here regardless of how it ran. The
episode's own Purpose is "where **background work** becomes visible and
actionable", and an inline turn's reply is already in the transcript, where
F5's `o` expands it in place (`chat\scroll.go:269`, F5's, pre-existing).

Corroborated from two directions: `_RESEARCH`'s own config says "Always a
background task -- it is the reason background tasks, result cards, and
cancellation have to genuinely work", and `project_idea.md:265` says research
"must be a background task, which forces the task panel, result cards, attach
modes, and cancellation to all genuinely work."

**Implemented as a rule, not a hardcode.** The backend's real rule is
`agent.max_steps > 1` (`runtime.py:822`'s `is_bounded_loop`), ridden out as
`TaskStarted.background` (`runtime.py:1383`). `max_steps` is not on the wire
and `ListAgents` (B23) returns only `{id, name, description}`, so
`app.IsBackgroundAgent` stands in — **and a `TaskStarted.background` this
client actually saw PROMOTES a task in regardless** (`noteTaskBackground`,
`tray.go:213`). Promote-only: hiding a real background result is the costly
error, showing one extra row is not. `backgroundSeen` is pruned to the current
snapshot each refresh, so it cannot grow for the life of the process.

**One spec disagreement, recorded rather than resolved.**
`project_idea.md:259` says the Filler-Explainer invoked "from chat" is "a
background task and a result card", which would put it in the tray. The
**code** makes it inline (`max_steps=1`). Per CLAUDE.md the code wins, so it
is excluded. If that is wrong it is a backend change — its `max_steps`, or a
real `background` field — not a frontend filter.

### `d` and `esc` both return to the tray

F09 distinguishes them because a card in the full spec can be attached or
saved; with neither axis built there is nothing for them to differ about. Both
return the **live tray instance** (`ResultCard.back`), so the filter and the
selection are exactly where they were — `d` additionally reports which card
went, which is the seam F9c extends.

**F09's spec needed no edit for this.** Done-when #8 is "discarding a card
leaves the task result retrievable from the tray", which is unchanged and now
directly observable instead of needing an F1 round trip. Only WORKFLOW.md's
gate wording changed.

### Done-when #8 holds structurally, not carefully

This card has **no delete path to call** — there is no event for removing a
task or a message, and the result lives in `messages` rows the backend owns.
Discarding stops showing a card. The test asserts the observable consequence
(the task is still listed and re-fetches the same text) so that a later
episode which *does* gain such a path cannot wire it here by accident.

### The overlay is sized to the transcript pane, not the terminal

`MaximisedOverlay.PreferredSize` is offered `r.layout.Main` — the transcript
pane's own computed rect — **less one cell per side**, and the panel is then
**centred in the whole terminal** so the right pane and status bar stay
visible around it under the scrim. Size from the pane, position from the
screen.

`Main` already has the right pane's columns and the header/status/query rows
subtracted by `Compute`, so the overlay is the size of the thing the reader is
already reading, and it tracks pane proportions for free — hide the right pane
and it widens. A fraction-of-the-terminal constant would have drifted the
moment pane proportions changed. LAYOUT.md's `50×12` / `60×18` survive as the
floors `overlayFits` refuses below.

Measured: terminal 160×48 with the pane shown → `Main` 125×41 → overlay
123×39 at (18,4); pane hidden → `Main` 158×41 → overlay 156×39 at (2,4).

### One copy of the task-row vocabulary

The glyphs, the running/queued/finished grouping, the label rule and the
duration formatting now live once, in `app\tasks.go`, read by both the tray
and F6's right-pane Task card.

This was **reversed within the session.** The first pass added them to `app`
and left `chat\cards.go`'s originals, on the grounds that editing F6's shipped
rendering was out of scope. Aryan pushed back, correctly: this codebase has
paid for two independently-maintained copies of one fact three separate times
(F5's `Seq`, F6's `taskCardWasVisible`, F8's Bug A), and the risk here was
concrete — `cards.go`'s own comment records a debugging round spent on a glyph
that degraded in Windows Terminal (`⋯` rendering as three literal dots,
replaced by `◌`). A fix like that landing in one copy and not the other is
exactly the failure.

Collapsing it was mechanical: four facts, `cards.go` 46 lines shorter and one
import fewer. **Byte-identical rendering verified**, not assumed — a golden
dump of `taskCardLines` across three widths × three heights × seven states
(including an unrecognised state and a zero `FinishedAt`), diffed before and
after, escape sequences included.

**What did NOT collapse, and must not:** the order *policy* and the note
*text*. `chat`'s `sortStableTaskRows` leaves active rows in the backend's
arrival order while `app.SortTaskRows` sorts them (the tray needs a total
order it computes itself, because it diffs consecutive snapshots to decide
whether to re-render); and the card's 26-column note shows a bare state word
where the tray shows an error code and a duration. Two policies over one
shared fact. `TestTaskCardNoteIsDeliberatelyNarrowerThanTheTrays` pins the
second so a later "finish the de-duplication" pass has to read why it stopped.

### A timer, where F6 was explicit about not having one

F6's rule is about **requests** — "no subscriptions while hidden" — and
`refreshTasks` still sends nothing except on a real lifecycle event. The tray
clock sends nothing at all: it is a local redraw of state Root already holds,
and it exists because `TaskElapsed` reads the wall clock, which no backend
event will ever report. Generation-counted and self-terminating, the same
shape as F1's header animation. Armed only while the tray is open **and** holds
a running row — a tray of finished tasks would otherwise redraw an identical
frame once a second forever.

---

## Bugs found during verification

Eight rounds. Four are the pattern above; these are the rest.

**1. `Confirm` truncated its own buttons, and the brackets appeared to jump.**
`MinSize()` returned a flat `40×7`. "Cancel it" / "Leave it running" compose
to a 44-cell button row inside a 38-cell budget, so `centre` fell through to
`truncateCells` on a **styled** string — which lost the tail *and*, because the
focused and unfocused forms carry their escape sequences at different offsets,
cut at a different visible column depending on which side had focus. One
truncation, two symptoms.

Fixed in the widget, not by shortening the labels: a widget that truncates its
only two controls is broken for every future caller, and F11's delete and
F9b's apply-anyway will both want verbs longer than "Quit". `MinSize` now
computes the width its buttons need with 40 as the floor; the quit confirm
still returns exactly `40×7` and there is a test pinning that.

**2. The two buttons were not visually balanced.** Padding both labels to a
shared width left the shorter one floating in the middle of a 16-cell field —
five cells of slack one side, six the other — beside a box that hugged its own
text. Each button now hugs its own label with two cells inside its own box.
The row's width is still invariant under focus (`"[ l ]"` and `"  l  "` are
both `l+4`), which is what `TestConfirmButtonsAreEvenlyPadded` actually
measures, so that test still passes.

**3. `array.Menu.Top()` did not scroll to the top.** It set
`selected = firstItemRow()` and relied on `ensureVisible`, which leaves
`cursor` at 1 whenever the list opens with a header — so **Home hid the very
first heading**, the one row someone pressing it is most likely looking for.
`Top()` now sets `cursor = 0`.

**This predates F9a and affects Help too**, which is sectioned by category.
Found while testing the pinned headings. Pinned by
`array.TestTopShowsTheFirstHeaderNotJustTheFirstItem`.

**4. A blank gap appeared and vanished as you scrolled.** My own sticky-header
fix cost a row: when the pinned heading stood in for the first visible row the
list drew one row fewer than it had room for, visible as a blank line above
the hint at the top of the list, filled by another task once you scrolled
past. `View` now asks the menu for one extra row when the heading stands in
for one, so the list area is always fully used.

Worth recording: the first regression test for this asserted *the number of
tasks on screen is constant* and **failed, correctly** — an inline section
boundary is a row and legitimately takes a line. The test now asserts the real
invariant (the list area is fully used and no drawn line is blank) with the
distinction written down so nobody "fixes" the boundary row later.

### And one process note worth keeping

**The clock was reported fixed once while still broken.** The first fix
scheduled the tick correctly, and the test written for it asserted *that the
tick fired* — which it did. The frame never changed, because the value was
baked into the item. The test was asserting the wrong thing, and it passed.

The replacement, `TestTrayClockAdvancesInTheRenderedFrame`, spends ~3 real
seconds and reads the clock **out of the rendered frame** across three real
ticks. Watched ticking five times in a row with nothing mutated —
`0:01 0:02 0:03 0:04 0:05` — before being reported fixed the second time.

**The lesson is about the assertion, not the bug:** a test that asserts the
mechanism fired rather than the outcome changed will pass over exactly this
class of defect. Every regression test added after that point asserts rendered
output or observable state.

---

## Deliberate limitations

### The result card renders plain text — deferred to F16 by decision

**This is a forward pointer, not an open gap.**
`design\tasks\frontend\F16-polish.md` now carries the item under
"Result-card code blocks — carried forward from F9a", with the diagnosis and
two packaging options.

- **Indentation IS preserved.** That half was fixed here, because losing it
  makes code read as *different code* — `await` rendering at the same level as
  `async def` is not merely plainer, it is wrong. `strings.Fields` was dropping
  leading whitespace; `wrapParagraphs` now captures it per source line,
  re-applies it to wrapped continuations, expands tabs to four spaces (a
  literal tab advances to a tab stop no width calculation here can predict,
  which is what tears a border), and drops it only when the indent exceeds the
  box width.
- **Syntax colour and a box frame are NOT provided**, and will not be before
  F16.

**Why it is a packaging problem, not a rendering one:** the markdown renderer
is `chat\markdown.go`, and `chat` imports `app`. The card cannot reach it. The
only two ways out were both out of scope for a frontend-F9 session — move the
renderer into a third package (a refactor of F3's shipped code), or move the
card into `chat` (which contradicts why the tray is Root's at all, since
Done-when #1 requires a task whose originating view is unloaded to still open
its result). F16's section recommends extracting into
`internal\markdown\`, since F11's memory popup will want it too. The frame is
free once a renderer is reachable: `box.go`'s `CodeBorder`/`CodeText` roles
already exist in every palette and the transcript already uses them.

### Other knowingly-accepted gaps

- **An empty tray's footer reads `1/1`.** The "no background tasks" row is a
  real disabled item and `Menu.Len()` counts it. Left consistent with F8's
  picker, whose "Loading…" row does the same, rather than diverging from an
  established surface for a cosmetic detail.
- **No `summary` attach mode.** See blocker 2.
- **The tray cannot show a task's topic or token count.** See blocker 4.
- **Concurrent *different* background agent types are not testable** until
  either filler's `max_steps` changes or a user agent exists, since research
  is the only agent that qualifies today. Two concurrent `@research` sends is
  the available concurrency test.

---

## Handoff

### Session 18b — F9b, the diff card

Needs **F13's editor buffer** and **B24's `FillProposal` event** both landed.
Three hazards, each one another episode already paid for:

1. **Apply-time guard ORDER.** Resolve the *session's* scope first, guard
   against that, commit nothing until it passes. Do **not** use a
   path-resolved project: `_resolve_optional_project` walks up from the path
   to auto-discover one (`runtime.py:752`), which answers a different question
   from `_attachment_scope`'s "what will the builder actually use". That drift
   is B12's bug #2 — a file priced at ~718 tokens under one scope and refused
   under the other — and B23 explicitly declined to inherit it for
   `ListDirectory`. For an estimate the drift costs a wrong number; for a
   **destructive apply** it means the write is guarded against whatever
   `.point\` sits above the file instead of the session's real root. Commit
   `374baa2` established the corrected order for `AttachContext`; follow that
   one. F9b may need `CheckFileStale` to accept a `session_id`.
2. **Staleness is a fact about a MOMENT.** Record the file's `(mtime, size)`
   as of region generation, re-`stat` at apply time, compare. Do not cache a
   stat and trust it: `CheckFileStale` takes a caller-supplied `mtime` and
   confirms whatever it is handed. B12's `set_mode` *clears* its recorded pair
   for exactly this reason, and F8's chip staleness was the same mistake one
   layer up. B24 already applies the rule once — `shown_start`/`shown_end` are
   carried from the context build rather than recomputed.
3. **The card's state transition FOLLOWS the buffer edit.** Mark it applied
   only after the edit returns. Optimistic marking reproduces F8's chip bug
   precisely: a card that looks applied when it was not. Since every apply
   goes into an in-memory buffer and never to disk, a failed apply is atomic
   by construction — but only if the ordering is this way round.

Read **How a finished task's result text is retrieved** above; F9b needs the
same path.

### Session 23b — F9c, attach and save

Needs **B13** and **an attachment-source extension that is not yet an
episode** — spec it at the end of Session 23, once B13's shape decides what a
memory attachment is. Settle the `summary`-vs-`reference` mode-name question
there, deliberately, and write down which way.

### Session 19 — F15

Reads **B24's payload** and **calls F9b's diff function** rather than
re-implementing it. Its dependency is F9b, not "F9".

### Anyone building a Popup

Read **The build-time / render-time pattern**. F10 and F11 are the next two
consumers and both have rows whose content changes.

### Anyone touching `app\tasks.go`

Its vocabulary is read by `chat\cards.go`. A glyph changed there changes both
surfaces, which is the point — and
`chat.TestTaskCardReadsTheSharedVocabulary` plus
`app.TestTrayRowGlyphIsStyledLikeTheTaskCards` anchor on the same expression
(`th.Style(app.TaskRole(s)).Render(app.TaskGlyph(s) + " ")`) so neither
package can re-fork it silently.

### Not done, and not this episode's to do

**There is no way to save a file in this codebase**, on either side — no
`WriteProjectFile`, and the frontend writes exactly one file (`state.json`).
F13's spec leans on B7's path guard, the file locks and `CheckFileStale`, all
backend-side. Which side owns "save" is a real decision, flagged in
WORKFLOW.md Session 18's prompt to be settled before F13 writes code.
