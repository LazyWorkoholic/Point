# F13a — Editor Core: the Editing Half

**Phase:** P1 · **Batchable:** no · **Depends on:** F12 ·
**Backend:** B7, B32

**The first half of F13.** F13's own doc anticipated the split: "The
largest frontend episode. It may take two sessions — if so, stop at a
working point, record what is done, `/clear`, and resume with a note."
It did take two. This doc is the half that was built first — the editing
half, with **no save path anywhere in it**. The save half is
`F13b-editor-save-locks.md`.

Every requirement below is F13's own, moved rather than re-derived.
Carved out of `F13-editor-core.md` when the episode split mid-build; that
combined doc was then retired. See `../../WORKFLOW.md`'s "Splitting an
episode" for the convention, and
`../../completed/frontend/F13a-editor-core-editing.md` for what the
session actually did.

## Purpose

A small editor that is a real editor. Not Neovim, not VS Code — but open,
edit, undo and select must all be correct, because everything else in the
Editor workspace stands on them. **Save** stands on them too, and is
F13b's.

## Build

### Buffer

- A line-slice buffer is sufficient at this scale. Keep it behind a `Buffer`
  interface so a rope or gap buffer can replace it later — **this is the first
  natural C++ component in the Track-2 book**.
- **Display width everywhere** (`go-runewidth`), same helper as F3/F4.
- **Line endings detected on open and preserved on save.** Without this, every
  saved file shows as fully modified in `git status` on Windows. Show the
  detected style (CRLF/LF) in the status bar.

  *This episode's share:* detect, carry and **display**. The preserve-on-save
  half is F13b's, and B32 reports `line_ending`, `crlf_count` and `lf_count`
  on the read so nothing has to be re-derived there.
- Trailing-newline behaviour preserved.
- BOM detected and preserved — some Windows-authored files have one, and dropping
  it is a silent one-byte diff.

### Cursor and selection

```
movement   ←/→/↑/↓, home/end, word-wise (ctrl+←/→), page up/down,
           ctrl+home / ctrl+end
selection  shift+movement, ctrl+a, mouse drag
edit       insert, delete, backspace, delete selection, replace selection
indent     tab / shift+tab on a selection indents / dedents
```

Mouse capture disables Windows Terminal's native selection, so **the editor owns
selection rendering**. That is real work here, not something mouse support gives
free — and it is needed anyway for the Explainer.

### Auto-indent — Tier 1 only

On Enter, **copy the previous line's indentation**. Nothing else. Language-aware
rules (increase after `:` or `{`, dedent on `}`) are Tier 2, parked to F16 if time
allows. Tree-sitter is post-v1.

Also here, roughly 20–40 lines each: tab-vs-space detection from the file,
`backspace` deleting a full indent level, and bracket auto-pairing.

### Undo / redo

- Per-buffer undo stack with **keystroke grouping** — a word of typing is one
  undo, not eight. Group by pause and by boundary characters.
- **An agent-applied diff is a single undoable operation** (F9/F15). Without this
  the diff card is unusable in practice.
- Redo cleared on new input. History discarded on file close.

### Tabs

The first content row **inside** the Code File box — not a nested box
(`LAYOUT.md`). Active tab in the accent role, inactive dim, separated by `│`,
horizontal overflow with `‹ ›`. `ctrl+tab` cycles, palette quick-switch, `×`
closes, dirty files marked `•`. Below 70 columns it collapses to the active
filename.

### File tree

Right pane, from `project.list_files` (B7). Expand/collapse, open on `⏎` or click,
honours `.gitignore` with the toggle, always skips `.venv` and `__pycache__`.
Hides below 90 columns.

### Syntax highlighting

Chroma, themed from semantic roles. Highlight the **visible window plus a
margin**, not the whole file — this is what keeps a large file smooth. Unknown
language renders plain.

### Status bar

`file · line:col · language · CRLF|LF · lock state · dirty marker`

**Every segment except `lock state`.** Nothing in this episode acquires a
lock, and a lock readout with no lock behind it is the hint-that-lies this
project refuses. F13b adds that one segment.

## Do NOT

- **Build any part of the save path.** No `WriteProjectFile` call, no lock
  acquire/heartbeat/release, no autosave, no staleness check, no
  reload/overwrite/diff refusal UI, no holder key in `state.json`.
  `ctrl+s` refuses with a message saying the write path is not wired yet.
  All of it is F13b's, and B32 already built the backend half.
- Build the terminal (F14) or Explainer integration (F15).
- Add splits, minimap, LSP, or symbol search.
- Add a filesystem watcher — the save-time check covers v1.
- Write to disk from any agent path.
- Write a project file from Go at all. `config\state.go` stays the only
  file the frontend writes.

## Done when

Numbered as in F13's own doc, so the two halves together cover 1–12.
**F13b covers 2, 7, 8, 9, 10, 11 and 12.**

1. *(open half)* Open a large file and edit and scroll it with no perceptible
   lag. The save half of 1 is F13b's.
3. Undo: type a sentence, one `ctrl+z` removes a word-group, not one character.
4. Selection renders correctly with mouse drag and with shift+arrows, including
   on a line containing CJK/emoji.
5. Tab / shift+tab indents and dedents a multi-line selection.
6. Enter after an indented line preserves that indentation.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

Leader → `e`, open `backend\runtime\runtime.py` from the tree.
**Expect:** instant open, smooth scroll, correct highlighting.

> **Two corrections to F13's original wording, settled before this episode
> started.** It said `runtime.py` is 21KB — it is **88,425 bytes**. And
> Done-when 1 named a 5,000-line file: **there is no 5,000-line file in
> this repo**, so the check is against the largest real one rather than a
> fabricated fixture. Do not create one to satisfy the number.

In the app, on a line containing `日本語 🎉`, drag-select across it.
**Expect:** the selection highlight covers exactly the right cells.

Press `ctrl+s`.
**Expect:** a refusal saying the write path is not wired yet.
**Fails if:** anything is written to disk.

## Handoff

State after this episode: the editor is a correct small editor with undo,
selection, indent, tabs, a tree and highlighting — and **no way to save**.
F13b adds locks and safe saves and closes F13; the terminal (F14) and the
Explainer (F15) plug in after that. F9b's diff card gains its apply
target here: `doc.ApplyRegion`, one splice and one undo step, shipped
deliberately with no caller.

Record: whether this took one session or two, the CRLF/BOM detection
details, and any Windows Terminal key combinations that did not arrive.

Do not proceed until every check above passes.
