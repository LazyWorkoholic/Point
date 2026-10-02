# Completed — Frontend

**This directory is the only place completion is claimed.** Task docs in
`../../tasks/frontend/` describe intent; these files describe reality. If the
code and these files disagree, **the code wins** — report the mismatch, do not
silently edit either side.

Written only after Aryan has run an episode's manual verification and confirmed
it passed.

## Status

| # | Episode | State | File |
|---|---|---|---|
| F1 | Bootstrap, Layout, Registry, Theme | ✅ built + manually verified | `F01-bootstrap.md` |
| F2 | Backend Client | ✅ built + manually verified | `F02-backend-client.md` |
| F3 | Chat Viewport | ✅ built + manually verified | `F03-chat-viewport.md` |
| F4 | Query Input | ✅ built + manually verified | `F04-query-input.md` |
| F5 | Streaming, Queue, Cancel | ✅ built + manually verified | `F05-streaming-queue.md` |
| S11b | B12 Verification + F5 Reload Pairing | ✅ built + manually verified | `S11b-b12-verify-f5-pairing.md` |
| F7 | Popup Framework (Popup + Confirm) | ✅ built + manually verified | `F07-popup-framework.md` |
| S12b | F7 Verification Repairs (chat/ render + startup) | ✅ built + manually verified | `S12b-f7-verification-repairs.md` |
| F6 | Right Pane (Status · Task · Sessions) | ✅ built + manually verified | `F06-right-pane.md` |
| S13d | Wire the Context Card (Status row 1 + UI-09) | ✅ built + manually verified | `S13d-wire-context-card.md` |
| S13e | Backend Verbose Passthrough (`POINT_BACKEND_VERBOSE`) | ✅ built + manually verified | `S13e-backend-verbose-passthrough.md` |
| F8 | @ Picker + Attachments | ✅ built + manually verified | `F08-at-picker.md` |
| **F9a** | **Task Tray + Result Cards (F9, SPLIT)** | 🟡 **built + manually verified — F9 NOT complete; F9b + F9c are the remainder** | `F09a-task-tray-cards.md` |
| F9b | Diff Card | not started — blocked on F13 + B24 | — |
| F9c | Result Card Attach + Save | not started — blocked on B13 + an unspecced B12 attachment-source extension | — |
| F10–F18 | — | not started | — |

**F1 and F2 were built and verified in one session** (`batchable: yes`).

**F9 is SPLIT across three sessions and is NOT complete.** F9a shipped the
global task tray and the result card's render/discard half — two of the
episode's eight "Done when" criteria. The other six were deferred before any
code was written, each on a dependency that does not exist in this repo: there
is no Editor workspace to apply a diff into (F13), nothing parses the Filler's
JSON reply into a structured proposal (B24), `context_attachments.source_type`
is `file`-only so a task result cannot be attached, and nothing writes
`memory_records` (B13). **All four are recorded with file:line evidence in
`F09a-task-tray-cards.md`** so Sessions 18b and 23b do not re-derive them.

F9 closes only when **F9b** (Session 18b — the diff card) and **F9c**
(Session 23b — attach and save) land. Until then this row stays 🟡.

**`F09a-task-tray-cards.md` is worth reading before building any Popup
consumer**, F10's settings and F11's memory popup especially. Four of the six
defects found verifying it were one cross-cutting pattern — a value computed
when the list was BUILT and never reconsidered when it was DRAWN (a frozen
`m:ss` clock, a glyph that lost its colour, section headings that vanished
while their rows stayed, and a footer that opened a task which was not on
screen). The doc names the pattern, the four instances, and `Popup.geom` as
its closing fix.

**S12b is the same shape as S11b, and it is where three `chat/` defects
live.** They were found while verifying F7 but none of them is F7's code —
F7 is `app/` and `array/`. Rather than bury them in an episode doc about
popups, they are recorded on their own: the frame-scoped render memo (a
height-instability race across the two render passes in every frame), the
`renderList` bug that silently dropped fenced and indented code inside list
items (**live since F3**), and provider-aware session selection at startup
(a stale session row pinned to a missing provider could strand the app with
no way out from inside it). Commits `4d8aef6` and `d3133ba`. **Anyone
working in `chat/` should read it** — particularly before touching
`renderLive`, `renderList`, or `onSessionList`.

**S11b is a verification-and-repair session, not a numbered episode.** It ran
immediately after B12 closed and spent the data B12 added, closing the three
gaps F5 deferred for lack of it. It is listed here because it changed
behaviour and carries contracts of its own — it does not consume an F number
and F6 is still the next episode.

The two contracts the rest of the frontend is built on:

- **`layout.Compute(w, h int, s State) Layout`** — every episode from F3 renders
  into the rects it returns. Full field list in `F01-bootstrap.md`.
- **`protocol.Envelope` and `protocol.Transport`** — every episode from F3 sends
  and receives through them. Full definitions in `F02-backend-client.md`.
- **The message → block rendering pipeline** (`renderMessage`, the render
  cache, the windowed scroll) — F5 (streaming) and F9 (task slots) both
  build on it rather than inventing their own. Full account in
  `F03-chat-viewport.md`'s "The message → block pipeline".
- **The cursor affinity model** (`inputBuffer.trailing`, `setCursor` as the
  sole assignment point, the `TestEveryMoveMethodIsRegistered` reflection
  guard) — any later episode adding a new cursor-movement operation must
  follow this model from the start; it took three rounds to get right the
  first time. Full account in `F04-query-input.md`'s "The cursor affinity
  model".
- **`app.DraftFlusher`** (`app\draft.go`) — a narrow interface, same pattern
  as `FocusAware`, for a workspace to expose a synchronous escape hatch
  from its own debounced saved state; `app.quit` calls it on the active
  workspace before returning `tea.Quit`. Any future workspace with
  debounced state to lose on quit should implement it rather than invent
  its own quit-time flush. Full account in `F04-query-input.md`'s "Bug 17"
  section under "Editing across a wrap gap".
- **The ordered entry model** (`chat\entries.go`) — the transcript is ONE
  sequence assembled from persisted messages, live entries and the pending
  bubble, and **display position is creation order, never role and never
  which list a thing lives in**. Getting this wrong produced four separate
  visible bugs at once. Anything adding a fourth source adds it here, not
  beside it. Full account in `F05-streaming-queue.md`'s "Bug 1".
- **The row-based scroll position** (`(topAbs, topRow)`, `entryBlock`) — an
  entry index plus a row offset inside it, deliberately NOT an absolute row
  number, which would cost O(all entries) per render. Full account in
  `F05-streaming-queue.md`'s "Bug 2".
- **The task lifecycle identifiers** — `TaskStarted` + its `background`
  field for the lane, `protocol.IsTerminal` for the three terminal events,
  and the `TextDelta`/`TaskToken` pair for tokens. **F6's task card and
  F9's result card must use these names exactly.** Full table in
  `F05-streaming-queue.md`'s "The two contracts F6 and F9 must use EXACTLY".
- **The question→answer pairing key** (`chat\reload.go`) — a reloaded
  transcript groups a user message with the assistant message carrying the
  **same `task_id`**, and adjacency, arrival order and elapsed time are
  never used for anything. **F6's task panel and F9's result cards both
  group messages and must use this same key** — anything keyed on position
  is wrong the moment two things run at once, which is the premise of this
  runtime. F5 already tried an adjacency heuristic and deleted it for
  corrupting correct transcripts. Full account in
  `S11b-b12-verify-f5-pairing.md`'s "The pairing contract".
- **The reload-renders-through-live-renderers rule** (`chat\entries.go`'s
  `histAt`/`liveAt`) — a persisted row that needs a task slot or a terminal
  footer is decorated with a `liveEntry` and drawn by F5's own
  `renderTaskEntry`/`liveFooter`, never by a reload-specific renderer. That
  is what makes "reload looks identical to live" true by construction
  rather than by discipline. Full account in
  `S11b-b12-verify-f5-pairing.md`.
- **`app.TaskRow` and the five `TaskState` values** (`app\tasks.go`) — the
  shape of one entry in the right pane's Task card list. **F9's result
  cards append into this same list and must use these field names
  exactly**, or the tray silently shows a mix of two shapes. Note that
  `Kind` is always `"chat"` and the capability name lives in `Agent`. Full
  struct and the four things a consumer must know in
  `F06-right-pane.md`'s "The task-card entry shape".
- **`app.OverlayProvider` / `app.RightPaneFallback`** (`app\workspace.go`)
  — the same narrow-interface pattern as `FocusAware` and `DraftFlusher`,
  for a workspace that can stand its right pane up as an overlay when the
  terminal is too narrow, and for the root to recognise that overlay so it
  can retire it on a resize and count it as "visible" for refresh
  purposes. Full account in `F06-right-pane.md`.
- **`app.DiagnosticsMsg.Seq`** (`app\placeholder.go`, `app\root.go`) — a
  monotonic counter every consumer of `Diagnostics` must gate the WHOLE
  message on. Bubble Tea runs each `tea.Cmd` in its own goroutine with no
  ordering guarantee, so arrival order cannot be trusted. Full account in
  `F06-right-pane.md`'s "The pattern this episode is really about".
- **`app.StreamAware` / `app.QueueAware`** (`app\stream.go`) — the same
  narrow-interface pattern as `FocusAware` and `DraftFlusher`, for "is
  anything running" (the header animation, which must freeze when idle or
  it becomes a permanent idle-CPU drain) and "is a message queued" (which
  esc means). Full account in `F05-streaming-queue.md`'s "Bug 3".

**F8 can start now.** F6 closed; F7's `Popup` is built and F8's `@` picker is
its next consumer.

**F6's two open items are both closed.**

1. **✅ B20 + UI-09.** B20 put `ContextReport` on the wire
   (`../backend/B20-context-report.md`); S13d replaced the Status card's
   stub row and built UI-09, so **F6's Done-when #2 is now satisfied**
   (`S13d-wire-context-card.md`).
2. **✅ B21** (`../backend/B21-orphaned-message-context.md`) — a failed,
   empty-output task no longer poisons the next prompt. The original trace
   is in `F06-right-pane.md`'s "Bug C". S13d then found that B21's filter
   deliberately kept in-flight unanswered questions, which let concurrent
   sends answer each other's questions; that carve-out is removed
   (`S13d-wire-context-card.md`'s Bug 1).

**S13d is a repair/wiring session like S11b/S12b, and it changed the
backend once** (`managers\context.py`). Anyone working on
`context.build()`'s history filter should read its Bug 1 and run
`backend\tests\manual_prompt_isolation.py` before and after.

**Read `F06-right-pane.md`'s "The pattern this episode is really about"
before adding any flag, sequence number or cached snapshot that more than
one call site is responsible for keeping accurate.** It cost F6 two real
bugs and four failed reproduction attempts, and it is the second time this
exact pattern has bitten this codebase — `ensureAnimation()` is the
original, correct answer to it. An invariant maintained by N call sites
will eventually have one that forgets; decide it once, centrally, after the
fact.

**Known limitation, for an F7 follow-up or F16:** `Overlay.MinSize` doubles
as both the refusal threshold and the actual rendered size —
`overlayRect` only ever shrinks an overlay, never grows it — so the
fallback right pane is permanently capped at its floor dimensions no matter
how much terminal is available. Raising `MinSize` is not the fix; it would
raise the refusal threshold with it. Needs a preferred-vs-minimum
distinction in the `Overlay` contract, which is shared F7 machinery.

**F4 carries one open watch
item** — `F04-query-input.md`'s Bug 17b: a backspace round-trip at a wrap
gap boundary was observed miscounting, traced with no cause found, then
stopped reproducing on its own. Not blocking, but any episode touching
`app\paste.go`'s burst timing or the delete path should re-run its exact
repro.

**F4 carries a second open item, from S11b:** the mode chip recognises
`@research` only. `agentTokenState` resolves all three built-ins, but
`currentMode` (`chat\input_render.go:148`) tests `id == "research"`, because
F4 defined exactly two chips. So `@filler-explainer` routes correctly and
records its agent while showing no chip at all. Investigated and
deliberately not fixed in S11b — the fix is a fourth `editorMode`, a
`modeChipPrefix` entry and a `queryBorderRole` case, all inside F4's query
input, and it wants F4's own gate rather than a reload session's. Full
account in `S11b-b12-verify-f5-pairing.md`'s Bug 6.

## How to add an entry

When an episode passes its verification gate, create `F##-<slug>.md`:

```
# Episode F# — <Title> ✅
**<one-line summary>**

## <package / file path>       ← one section per package or file created
<what it does, why it exists, what depends on it>

## Layout / theme decisions made here
<anything that changed LAYOUT.md or THEME.md, and why>

## Manual Test Log (F#)
<numbered, specific behaviours confirmed AT SPECIFIC TERMINAL SIZES —
 not "it rendered">

## Bugs caught during manual testing
<what broke, why, and the fix — the most valuable section>

## What the next episode inherits
<state, interfaces, and anything deliberately left unfinished>
```

Then add a row to the table above.

## Frontend-specific things worth recording

These recur in TUI work, and writing them down the first time saves the second:

- **Terminal sizes tested** — the actual dimensions, not "it worked".
- **Wide-character bugs** (CJK, emoji) — where display width was used incorrectly.
  The signature is a right border sitting one or two cells off on one line.
- **Keys Windows Terminal swallowed or encoded oddly**, and which ones arrived
  fine. This list is useful for every later episode.
- **CRLF / BOM handling** — anything that made `git status` show a file as fully
  modified after a no-op open-and-save.
- **Render performance** — what was cached, what was measured, at what size.
- **Anything that behaved differently over SSH** versus locally.
- **Breakpoint adjustments.** `LAYOUT.md` marks the numbers provisional; separate
  bordered boxes plus outer margins cost more rows and columns than the original
  estimate. **F1 and F16 should both record the values actually settled on.**
- **A test timeout is a ceiling on how long a BROKEN run may hang, never an
  assertion about how fast a correct one is.** F6 shipped three tests that
  asserted wall-clock timing — a settle budget, a cmd-drain window, and an
  exact `"1:30"` clock reading that legitimately rendered `"1:31"` — and all
  three failed intermittently under `-race`. A suite that fails
  intermittently trains you to ignore failures, which is worse than not
  having the test. Assert bands and behaviour; make ceilings generous;
  prove it with repeated `-count` sweeps under `-race`.
- **A test harness that drives `tea.Cmd`s synchronously imposes an ordering
  Bubble Tea never promises.** Three of F6's reproduction attempts passed
  for this reason alone while the live bug was real.
  `chat\overlay_concurrency_test.go` has a `fakeBackend` plus a
  `concurrentDriver` that spawns every Cmd in its own goroutine; reach for
  it before concluding a "works in tests, fails live" bug is unreproducible.
- **A timing-dependent heuristic that misfires once and then stops is not
  proven fixed by it not misfiring again.** F2's paste-burst detector and
  F4's Bug 17b both hit this: a bug traced exhaustively with no cause found,
  then a clean re-test with no code change in between. Record it as
  *observed, then not reproducible*, watched going forward — not as fixed —
  and name the exact repro so a later episode can re-run it deliberately
  rather than rediscover it from scratch.
