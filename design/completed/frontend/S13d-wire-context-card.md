# Session 13d — Wire the Context Card ✅

**The Status card's row 1 reads B20's real `ContextReport` instead of a stub,
and UI-09 (the context composition card) exists: per-section token counts that
sum to the total, a named eviction prediction, reachable from the real pane and
from the narrow-terminal fallback. F6's Done-when #2 is now satisfied.
Verification also found and fixed a critical BACKEND defect: concurrent sends
built prompts containing each other's unanswered questions.**

> Verified by Aryan on 2026-09-25 against
> `tasks/frontend/S13d-wire-context-card.md`, on scratch profiles
> (CLAUDE.md's APPDATA/LOCALAPPDATA override), with a real Ollama model where
> the check needed one. Full Go and Python regression sweep clean.
>
> **Model:** started on Sonnet 5, switched to Opus 5.5 partway, for the
> backend prompt-contamination round onward.
>
> Same shape as S11b/S12b: a wiring-and-repair session, no F number.
> **It touched the backend once** (`managers\context.py`), for a defect found
> at this gate that could not wait for its own episode — recorded below, and
> the first thing a backend session should read here.

---

## Where the code went

```
frontend\internal\
├── protocol\types.go       + CmdGetContextReport, EvContextReport
├── app\
│   ├── context_expand.go   NEW — ContextExpandable, MouseOverlayTarget,
│   │                        ContextView, ContextRefresher (narrow interfaces,
│   │                        same pattern as OverlayProvider / FocusAware)
│   ├── actions.go          + context.expand (leader x)
│   └── root.go             + setOverlay (the ONE place r.overlay is
│                            assigned); OpenOverlay delegates to it;
│                            dispatchMouse asks MouseOverlayTarget;
│                            refreshContextIfShown (post-update hook)
└── chat\
    ├── model.go            + wireContextReport / wireContextItem, the
    │                        SECTION_ORDER mirror and its display labels
    ├── context_report.go   NEW — pull, one decoder for pull + push,
    │                        the guard, click hit-test, RefreshContextReport
    ├── context_overlay.go  NEW — UI-09 (bespoke Overlay, not a Popup)
    ├── connection.go       NEW — connectionDown(), the one connection check
    ├── cards.go            Status row 1 real; tokenBar; rightPaneOverlay
    │                        enter/click swap to UI-09
    ├── protocol.go         push branch in handleEnvelope; queryGetContextReport;
    │                        onConnected; decorative queries never set loadErr
    ├── render.go           statusCardRect; banner row above the transcript
    └── stream.go           writeSend no longer sets loadErr

backend\managers\context.py   history filter: every unanswered question
                              from another task is excluded (see Bug 1)
backend\tests\manual_prompt_isolation.py   NEW — captures real prompts
backend\tests\manual_eviction_wording.py   NEW — the running-task wording
```

Tests: `app\overlay_swap_test.go`, `app\context_refresh_test.go`,
`chat\context_report_test.go`, `chat\context_overlay_test.go`,
`chat\connection_banner_test.go`, `chat\reconnect_test.go`.
`TestHelpListsEveryAction`'s viewport was raised from 80×20 to 80×60: the 12th
action pushed "Retry Backend" out of an unscrolled snapshot. The test's job is
exactly to catch a registry addition, and it did.

---

## How UI-09 is reached — decided, not defaulted

The right pane is not in `FocusAware`'s rotation, and this session did not put
it there. Literal `⏎` inside `chat.Workspace` is already claimed twice (send when
the input is focused; expand a message slot when browsing), and a third focus
state for card rows is the general mechanism F6 deferred.

| Where | Gesture | How |
|---|---|---|
| Real pane (≥ 90 cols) | leader `x` | registered action `context.expand` |
| Real pane | click the Status card | root's `dispatchMouse` → `MouseOverlayTarget` |
| Fallback overlay (70–89 cols) | `enter`, or click its Status section | `rightPaneOverlay.Update` **returns UI-09 in its own place** |

**The fallback swap is a same-slot swap, not stacking.** `r.overlay` stays one
field holding one overlay. It needed one real fix to be safe: before
`setOverlay`, `dispatchKey`/`dispatchMouse` assigned whatever an overlay's
`Update` returned **unconditionally**. That had been harmless only because no
overlay had ever returned anything but itself or nil. `setOverlay` fit-checks a
changed, non-nil return exactly as `OpenOverlay` does, and `OpenOverlay` now
calls it (one fit check, not two that drift). Accepted consequence: `esc` from
UI-09 closes it entirely and does not step back to the fallback.

**One guard, every entry point:** `Workspace.ContextExpandOverlay()` refuses
with no report yet or with the backend down. The leader action, the click and
the swap all call it, and UI-09 is never constructed around a nil report.

`context.expand` shows in the palette and Help by construction (both render
`root.actions.All()`), greyed out whenever the guard would refuse.

---

## Layout decisions made here

- **UI-09 `MinSize` 42×16.** Started at 42×14 and was found short at the gate
  (Bug 3). Title carries `Context · used/budget` on the box edge, matching
  F6's mockup.
- **The connection banner is one row ABOVE the transcript, never in place of
  it** (Bug 5). A behaviour change from F3's `loadErr` screen, which still
  exists for genuine load failures.
- **The task doc's worry about UI-09 not fitting was unfounded.** Any overlay
  needs ≥ 70 columns (`layout.OverlayFloor`), so the fallback only ever opens
  at 70–89 columns, where a 42-wide UI-09 always fits. `setOverlay`'s refusal
  path is real and tested with synthetic sizes (`overlay_swap_test.go`) but
  not reachable by hand with today's numbers.

---

## Manual Test Log (S13d)

1. Row 1 goes from stub to a real bar the moment a reply finishes; correct
   immediately after reloading a session (the pull, not only the push).
2. Leader `x`, a click on the Status card, and `enter` in the fallback
   overlay all open UI-09. All three cleanly do nothing before the first
   report, and with the backend killed.
3. Section rows in `SECTION_ORDER` sum to `used_tokens`; the evictions list
   matched `cli.py context report --json` for the same session.
4. A live 6-orphan session showed all 6 or an exact `+N more` (after Bug 3's
   fix).
5. Killing the backend with UI-09 open degrades it in place, and it recovers
   without reopening (Bug 4).
6. With one `@research` running, UI-09 names it as
   `unanswered: task … (running) has no reply`, matching a fresh backend
   report (Bug 6).
7. Kill-backend-then-send, repeated: the banner row shows every time, the
   failed send keeps its footer, and the banner clears on reconnect (Bug 5).
8. Three sends back-to-back: each answer is to its own question (Bug 1).
9. The B20 push is confirmed in ONE long-lived `main.py --verbose` process
   fed by hand: `TaskStarted`, `TaskToken`, `TaskDone`, the DEBUG
   `B20: pushing context report` line, then the push (`corr_id: null`).

---

## Bugs caught during verification

### Bug 1 — concurrent sends answered each other's questions (backend, critical)

**Symptom, from raw persisted rows:** three sends fired back-to-back into one
session ("perform bubble sorting with code", "tell me importance of bees",
"hi"). The "hi" task came back with a bubble-sort answer, under a **correct**
`task_id`.

**Root cause:** `context.build()` runs at task **creation**
(`agents\executor.py`, before the user message is persisted, by B8's design).
B21's orphan filter deliberately kept unanswered questions from **in-flight**
tasks in the history window. So the three prompts were `[Q1]`, `[Q1, Q2]`,
`[Q1, Q2, Q3]`: earlier questions, persisted and unanswered, with nothing
between them. The model answered the first unanswered question it saw.

**Audited, not assumed:** `build()` is the only history source for every
prompt (executor, `context.report`, CLI). The agent loop copies the built
prompt once and never re-reads the session. Title generation and memory
proposals read only their own text or `task_id`. The question itself is
captured at creation and never re-read. B21 did not cover this; its in-flight
carve-out *was* the defect.

**Fix:** an unanswered question from another task is always excluded,
whatever its task's status; the status only picks the report's wording
(`orphaned: … ended 'failed'` vs `unanswered: task X (running) has no reply`).
A task's own question is never in the window at build time, so it cannot be
dropped.

**Proof by the real outgoing prompt, not by `task_id`** (a correct `task_id`
is exactly what the broken rows had). `manual_prompt_isolation.py` wraps
`MockProvider.stream` and records every prompt:
```
BEFORE  prompt 3: user turns = ['perform bubble sorting with code', 'tell me importance of bees', 'hi']
AFTER   prompt 3: user turns = ['hi']            (4 plain back-to-back: each prompt = its own question)
        [PASS] the answered first question is still in the second prompt
```

**Behaviour change:** while sends are in flight, the Status card and UI-09
list those sibling questions under next-turn evictions. That is accurate:
they really are left out of the next prompt.

### Bug 2 — the evictions list was silently truncated

A fixed cap (3 rows) was checked independently of the real row budget, so
`View`'s outer `clip` did the actual cutting, with no count. It showed 2 of 6
live. `evictionSection()` now takes the true remaining budget and always
reports `+N more`, the `taskCardLines` convention.

### Bug 3 — UI-09 `MinSize` was sized short

14 rows left the eviction section a 3-row budget after the 9 fixed rows. Now
16.

### Bug 4 — UI-09 froze stale numbers when the backend died

`View`/`Title` never checked `w.backendDown`. They now degrade to the single
dim `—` the other three cards use, without closing, and recover on the next
frame.

### Bug 5 — the connection banner depended on a timing window

**Symptom:** kill-backend-then-send showed `! backend is not connected` once
and nothing the next time; the failed send's footer appeared both times.

**Root cause, two windows** (`connection_banner_test.go` reproduces both,
deterministically):
- **A:** the client had already noticed the exit, so `Client.Send` failed and
  `writeSend` stored that error as `w.loadErr`: a banner drawn **in place of
  the whole transcript**, plus the footer.
- **B:** the write landed in the dead process's pipe before the exit was
  noticed, so `Send` succeeded; `BackendLostMsg` then ended the entry: footer
  only, and no banner ever.

A third path set the same banner: `doSend` stored **any** query's transport
error in `loadErr`, including the decorative `ListTasks`/`GetContextReport`.

**Fix:** `connectionDown()` (`chat\connection.go`) reads only the connection
state Root already pushes, Seq-guarded, on every transition. It alone decides
both whether the banner shows and whether a send is refused. No transport
error writes `loadErr` any more. Decorative queries fail silently, and only a
load query (nothing on screen yet) still reports.

**Related:** after `ctrl+w r` the old banner also never cleared, because the
reconnect path's `beginBootstrap` returns at once once bootstrapped.
`onConnected` clears it, and re-bootstraps only a session that never finished
loading.

### Bug 6 — UI-09 showed stale data while open

Opening UI-09 fetched nothing; it drew only the load-time pull or the
backend's push after a turn **persisted**. One `@research` running, UI-09
open: "next turn evicts: nothing", while a fresh report for the same moment
named that task's question. That "nothing" looked plausible ("one task has no
sibling"), but the report describes the *next* turn, and a running task's
question is always excluded from it.

**Fix:** `refreshContextIfShown` in `Root.Update`'s post-update hook, the same
"ask once, afterwards" spot as `refreshTasksIfVisible` and for the same
reason. UI-09 opens from three places and closes from more. It refreshes on
the transition to shown and on each task lifecycle event while shown, and
never while UI-09 is closed. Its transition flag is written in exactly one
place. Both tests go red with the hook disabled.

---

## Verification methodology — two lessons, both cost real time

### A bare CLI process runs crash recovery on the shared database

Every process that builds a Runtime runs crash recovery (`bootstrap.py`),
which marks every `running` task failed/`backend_died`, **unless a real backend
holds the single-instance lock**. `cli.py` never takes that lock.

- **Next to a running app, the CLI is safe:** the app holds the lock, so a CLI
  command skips recovery and does not touch the app's live tasks.
- **With no app running, CLI processes sweep each other.** Two `cli.py send`s
  plus a `cli.py context report` marked each other's running tasks failed, and
  the report then read a state the test itself had just corrupted (a task
  `running` in one query, "ended 'failed'" in the report run seconds later).

**Rule:** a multi-send concurrency check runs in ONE process. That is why
`manual_prompt_isolation.py`, `manual_eviction_wording.py` and
`manual_b12_concurrency.py` all drive a single Runtime directly.

### `cli.py` never attaches to a running `main.py`

Every `cli.py` command builds its own in-process Runtime (`_with_runtime`) and
tears it down. It never talks to a running `main.py` or app, **but it writes
to the same `point.log`**. So a `point.log` line may come from a CLI process,
not from the app you are watching. One round of the log check ran
`main.py --verbose` in one terminal and `cli.py send` in another, and the
verbose process never received the send.

**Rule:** any check comparing "the running app" with "a CLI command" must say
which process produced each line it relies on. To exercise one specific
`main.py`, feed it over its own stdin (the exact lines are in this session's
record: `CreateSession`, then `SendMessage` with the returned id, then
Ctrl+Z Enter).

### The push confirmation is DEBUG, and the app can't log DEBUG yet

The app spawns `uv run python main.py` with no arguments, so the push's
DEBUG line is never logged; `go run … --verbose` is silently ignored.
**Addressed by S13e** (`tasks/frontend/S13e-backend-verbose-passthrough.md`,
the next session): `POINT_BACKEND_VERBOSE=1` makes the app launch the backend
with `--verbose`.

---

## What the next episode inherits

- **F6's Done-when #2 is satisfied.** F6's own handoff note and the frontend
  INDEX's "F6 carries two open items" paragraph still say B20 and UI-09 are
  owed. Both are now stale.
- **`ContextReport` has one consumer, `chat.Workspace`.** Read
  `w.contextReport`; never re-derive the arithmetic.
- **`app.setOverlay` is general machinery.** Any overlay whose `Update`
  returns a *different* overlay is now fit-checked like a fresh open.
  Stacking (a real back-stack) is still not built.
- **The frontend's rule is now "connection state is the one source".**
  Nothing may put a transport error into `loadErr`.
- **Backend: B21's in-flight carve-out is gone.** Anyone touching
  `context.build()`'s history filter should run
  `backend\tests\manual_prompt_isolation.py` first and after.
- **Open, independent of this session — the SQLite commit race.** Under a
  burst of concurrent sends, one task's successful completion fails to commit
  its assistant row (`cannot commit transaction - SQL statements in
  progress`), and the failure fallback then crashes on `UNIQUE constraint
  failed: messages.id`. It appeared in `manual_prompt_isolation.py`'s runs
  both before and after Bug 1's fix, so no fix here caused or changed it;
  every other fix here is frontend-only. Aryan's regression sweeps reproduced
  it unchanged across three rounds. It is queued as its own task. Its effect
  on terminal events (possibly none sent for the affected task) is
  unverified.
- **Session 13d has a `model_usage.md` row** (Sonnet 5, switched to Opus 5.5).
