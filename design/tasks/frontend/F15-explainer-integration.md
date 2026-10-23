# F15 — Filler-Explainer Integration

**Phase:** P1 · **Batchable:** no · **Depends on:** F13a, F9b · **Backend:** B11, B24, B36

> **Depends-on updated 2026-10-19.** It read `F13, F9 · Backend: B11`, which
> predates three splits and one new episode. F9 is split and the dependency
> is **F9b** specifically (the diff card and its diff function), not "F9" —
> F9c is unrelated to this episode. **B24** parses and validates the fill
> reply, which this episode must not re-do. **B36** carries the
> `(mtime, size)` pair captured at region generation, which Done-when 7b
> below cannot be verified without. See WORKFLOW.md Session 19.

**The last P1 episode.** After it, v1 is feature-complete.

## Purpose

The editor's payoff: explain what is under the cursor, and insert code at it —
with the change reviewable and undoable before it reaches a buffer.

## Build

### EditorContext extraction
```go
type EditorContext struct {
    File, Language string
    Cursor         Pos
    Selection      *Range
    Enclosing      *Range   // nearest def/class/func/brace block
    Imports        []string // the file's import block, cheap to grab
}
```
Sent with any AI invocation from the Editor. **This is why the Explainer needs no
typing** — the common path is select-then-invoke, not `@file`.

**Enclosing scope, Tier 1:** nearest enclosing `def` / `class` / `func` by
indentation or brace matching. No parser. Capped at ~2k tokens; if the enclosing
scope is larger, fall back to selection ± 20 lines. Sending one highlighted line
without its function makes the question unanswerable.

The backend has the same helper (B11); **the frontend's version wins when
supplied**, because it knows the real cursor.

### Explain (read path)
```
select code (or just place the cursor) → leader → x  (or the palette)
   → task created with EditorContext
   → explanation streams
```
Rendered in a **right-side overlay panel inside the Editor** — a rounded box with
its title on the edge, scrollable, dismissed with `esc`, with a `send to chat`
action that copies the exchange into the current chat session.

Rationale: switching workspaces to read an explanation about code you are looking
at defeats the purpose.

Scope ladder, selectable before invoking: `selection → enclosing → block → file`.
**Project-wide only on explicit request**, and then as a file list, never
contents.

### Fill (write path)
```
place cursor → leader → f → type an instruction inline
   → the region STREAMS BACK as GHOST TEXT at the cursor (D85):
       dim, in a distinct theme role, growing as tokens arrive
       nothing on disk · nothing in the real buffer · not undoable yet
   → when the stream completes, Go computes the diff → diff card (F9)
   → ⏎ apply  ·  e edit  ·  d discard
```

**Ghost text is the "watch it write" feeling without giving up the guarantee that
nothing lands without a keypress.** It is display-only: the buffer's real content
is untouched until apply.

If you switch workspace mid-stream and come back, the ghost **rebuilds from the
task's accumulated events** — not from a snapshot taken when you left, so it
shows everything that arrived while you were away. This reuses the same
rehydrate-from-the-backend property F12 already needs; do not build a second
mechanism.

Cancelling (`esc` or `ctrl+c`) discards the ghost entirely and cancels the task.
- Apply inserts into the **unsaved buffer**, so `ctrl+z` undoes it — and it must
  be **one** undo operation (F13's grouping requirement).
- If the buffer changed since the region was generated, refuse and offer
  re-generate — the same staleness rule as save.
- **And separately, if the FILE changed ON DISK since the region was
  generated, refuse too.** These are two different checks over two different
  things, and reading them as one is what left the second unbuilt: F9b
  shipped the buffer check (`baseVersion`, an in-memory version compare that
  needs no stat) and **deferred the disk check as its Hazard 2**, because no
  `(mtime, size)` pair captured at region-generation time existed to compare
  against. B36 adds it. Populate `DiffCardOptions.RecordedMtime` /
  `RecordedSize` from B36's real fields — never from `doc.mtime` /
  `doc.size`, which describe the open/last-save moment and fail in both
  directions. Done-when 7 and 7b below are these two checks respectively.
- A request needing a second file, a new file, or a command is **refused by the
  agent** with a message. The UI must show that as a **normal response, not an
  error state** — the refusal is a feature, and it is the boundary against the
  parked Coding Assistant.

### Inline invocation UI
A one-line prompt at the cursor row, in the accent border, with the scope shown:
```
▸ fill · after line 12 · python  │ add error handling for a missing file
```
`esc` cancels. Nothing is sent until Enter.

### From Chat
`@filler-explainer` plus an `@file` attachment works from Chat too, and there it
uses the normal background-task plus result-card presentation (F9). Same agent,
same config — the **invocation site decides presentation**.

## Do NOT

- Write to disk from this path, in any phase.
- Ask the model for a patch or for line numbers to edit.
- Send the whole file when a selection or enclosing scope will do.
- Send project-wide context implicitly.

## Done when

1. Select one line inside a function, invoke explain → the sent context contains
   the **whole enclosing function** (verify in the context report).
2. The explanation streams into the overlay panel; `esc` dismisses;
   `send to chat` moves it into the current session.
3. Fill at a cursor streams **ghost text** at the cursor as tokens arrive, then
   produces a diff card whose line numbers map onto the real file.
3b. Switch to Chat mid-stream and back → the ghost shows everything that arrived
   while away, not a frozen snapshot.
3c. `esc` mid-stream discards the ghost and cancels the task; the buffer is
   untouched and not marked dirty.
4. `e` edits the proposal, `⏎` applies the **edited** text, and one `ctrl+z`
   removes the entire insertion.
5. The file's mtime is unchanged until the user saves.
6. A two-file request is refused with a readable message, not an error state.
7. Edit the buffer after generating, then apply → refused, with re-generate
   offered.
7b. **Modify the file ON DISK after generating, then apply → refused, with a
   reason.** This is **F9's own Done-when #7**, which **F9b deferred as its
   Hazard 2** — see `design/completed/frontend/F09b-diff-card.md`. It is NOT
   a duplicate of 7: 7 is the buffer changing (F9b's `baseVersion` guard,
   built and tested), 7b is the file on disk changing, which needs B36's
   `(mtime, size)` pair captured at the region-generation read. **F9's #7
   closes here or not at all** — no later episode is positioned to do it, so
   a green F15 that skipped this leaves F9 permanently short of one
   criterion. Verify the refusal is shown ON THE CARD, not only in a log,
   and that the card does not read "applied".
8. The same agent invoked from Chat produces **no inline diff card** -- the
   reply streams into the transcript as an ordinary assistant message, and the
   diff card is the Editor's presentation only.

   > **Wording corrected 2026-10-20, after F15's own gate run.** It read "produces
   > a **result card** instead of an inline diff", and that half of it asked for
   > something this build deliberately does not do. **The task tray is background
   > work only:** `app	asks.go`'s `builtinAgentBackground` records
   > `filler-explainer` as `false` (it is `max_steps=1`, so `is_bounded_loop` is
   > false and the backend runs it as an inline turn), `trayRows` filters on
   > exactly that, and the tray's own empty state says so verbatim -- "no
   > background tasks ... an inline turn's reply is in the transcript, not here".
   > F9a's own framing is "the global task tray -- every workspace's BACKGROUND
   > work".
   >
   > So an inline turn correctly has no tray row and therefore no result card to
   > open, and the first gate run was answered "pass" against a criterion that
   > could not be met. **The tray was not changed**: opening a result card for a
   > reply already sitting in the transcript would duplicate it, which is the
   > reason the filter exists. What survives of #8 is the half that is actually
   > about this episode -- the invocation site decides presentation -- and it is
   > verifiable: the Editor builds a `DiffCard` only for a proposal matching its
   > OWN corr_id (`TestAForeignProposalBuildsNoCard`).
   >
   > A single-step agent whose result IS worth keeping (there is none today) would
   > need the tray to register inline tasks too, which is a change to F9a's filter
   > and its own decision to make, not this one's.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the editor, open `backend\runtime\task.py`, select **one line** inside a
function, leader → `x`.
**Expect:** an explanation about the function, not the line alone. Confirm in the
context report that the whole function was sent.

```powershell
# 5. nothing touches disk
(Get-Item D:\Projects\Point\backend\runtime\task.py).LastWriteTime
```
Then: leader → `f` → "add a docstring" → `e` → tweak → `⏎` apply → `ctrl+z`.
```powershell
(Get-Item D:\Projects\Point\backend\runtime\task.py).LastWriteTime
```
**Expect:** identical timestamps; the buffer dirty; one `ctrl+z` reverting fully.
**Fails if:** the timestamp changed, or undo takes several presses.

Then: leader → `f` → "refactor this across task.py and state.py".
**Expect:** a plain refusal naming the one-file limit, rendered as a normal
response.

```powershell
# 7b. staleness on DISK (F9's Done-when #7, deferred by F9b as Hazard 2).
# Generate a fill, leave the card OPEN, then change the file from outside
# Point -- a second Windows Terminal tab, not from inside the editor:
Add-Content D:\Projects\Point\backend\runtime\task.py "# external"
```
Then press `⏎` on the still-open card.
**Expect:** refused, with a reason **on the card**, a re-generate offer, and
the card still reading "Proposed change" rather than "Applied".
**Fails if:** it applies, or the refusal appears only in the log, or the card
flips to applied — and also fails if it refuses for the wrong reason: check
the message names the file changing on disk, not the buffer (that is
Done-when 7, and F9b's `baseVersion` guard would catch it either way, which
is exactly how a disk check that never ran could look like it passed).

## Handoff

State after this episode: the editor is AI-assisted in the two ways that matter,
with every write reviewed, editable and reversible. **v1 is feature-complete.**

Record: the enclosing-scope heuristic's failure cases as you hit them in real use.

Do not proceed until every check above passes.
