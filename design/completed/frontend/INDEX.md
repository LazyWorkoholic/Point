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
| F6, F8–F18 | — | not started | — |

**F1 and F2 were built and verified in one session** (`batchable: yes`).

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
- **`app.StreamAware` / `app.QueueAware`** (`app\stream.go`) — the same
  narrow-interface pattern as `FocusAware` and `DraftFlusher`, for "is
  anything running" (the header animation, which must freeze when idle or
  it becomes a permanent idle-CPU drain) and "is a message queued" (which
  esc means). Full account in `F05-streaming-queue.md`'s "Bug 3".

**F6 can start now.** It needs F5 (done) and F7. **F4 carries one open watch
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
- **A timing-dependent heuristic that misfires once and then stops is not
  proven fixed by it not misfiring again.** F2's paste-burst detector and
  F4's Bug 17b both hit this: a bug traced exhaustively with no cause found,
  then a clean re-test with no code change in between. Record it as
  *observed, then not reproducible*, watched going forward — not as fixed —
  and name the exact repro so a later episode can re-run it deliberately
  rather than rediscover it from scratch.
