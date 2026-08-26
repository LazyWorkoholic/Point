# F15 — Filler-Explainer Integration

**Phase:** P1 · **Batchable:** no · **Depends on:** F13, F9 · **Backend:** B11

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
8. The same agent invoked from Chat produces a result card instead of an inline
   diff.

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

## Handoff

State after this episode: the editor is AI-assisted in the two ways that matter,
with every write reviewed, editable and reversible. **v1 is feature-complete.**

Record: the enclosing-scope heuristic's failure cases as you hit them in real use.

Do not proceed until every check above passes.
