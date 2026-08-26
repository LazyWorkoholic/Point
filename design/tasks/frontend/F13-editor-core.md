# F13 — Editor Core

**Phase:** P1 · **Batchable:** no · **Depends on:** F12 · **Backend:** B7

**The largest frontend episode.** It may take two sessions — if so, stop at a
working point, record what is done, `/clear`, and resume with a note.

## Purpose

A small editor that is a real editor. Not Neovim, not VS Code — but open, edit,
save, undo and select must all be correct, because everything else in the Editor
workspace stands on them.

## Build

### Buffer
- A line-slice buffer is sufficient at this scale. Keep it behind a `Buffer`
  interface so a rope or gap buffer can replace it later — **this is the first
  natural C++ component in the Track-2 book**.
- **Display width everywhere** (`go-runewidth`), same helper as F3/F4.
- **Line endings detected on open and preserved on save.** Without this, every
  saved file shows as fully modified in `git status` on Windows. Show the
  detected style (CRLF/LF) in the status bar.
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
margin**, not the whole file — this is what keeps a 5k-line file smooth. Unknown
language renders plain.

### Save, locks, and staleness
```
open   acquire a soft lock (B7); heartbeat while open; record mtime
save   if the file changed on disk since open → REFUSE:
       "changed on disk since you opened it — [reload] [overwrite] [diff]"
       release the lock on close / unload / quit
held   another holder → open read-only; status bar shows
       "held by aryan (12s ago) — [take over]"
```
Stale locks (heartbeat > 60s) can be taken over. This protects you from your own
second terminal as much as from an SSH guest.

### Autosave (D90)
```toml
[editor]
autosave = "off" | "after_delay" | "on_focus_change"   # default: after_delay
autosave_delay_ms = 1000
```

`after_delay` saves 1000ms after typing stops. That number is VS Code's, and it
is chosen so you rarely save mid-token — which matters because an autosave with
the preview running (F17) reloads the browser on a possibly-broken file.

**Five rules, each preventing a specific bug:**

1. **Never autosave a file that is read-only or held by another lock holder.**
   Otherwise autosave hammers B7's refusal path once per second.
2. **Record Point's own write mtimes.** Otherwise Point's autosave trips its own
   "changed on disk since you opened it" check on the next save.
3. **Never autosave a buffer with no path** (a scratch or unnamed buffer).
4. **An agent-applied diff is followed by an autosave** like any other edit —
   apply *was* the user's keypress. But `Ctrl+Z` after it must **also** trigger a
   save, or the buffer and disk disagree silently.
5. **Autosave does not clear undo history.** Undo remains buffer-level and
   unaffected by saving.

The dirty marker still appears between the edit and the save, so the status bar
never lies about the current state.

**Known interaction, accepted:** with autosave on, the working tree is
near-permanently dirty, so the Coding Assistant's clean-tree precondition (D80)
will prompt often. Its existing `[commit] [stash] [run anyway]` choice handles it.

### Status bar
`file · line:col · language · CRLF|LF · lock state · dirty marker`

## Do NOT

- Build the terminal (F14) or Explainer integration (F15).
- Add splits, minimap, LSP, or symbol search.
- Add a filesystem watcher — the save-time check covers v1.
- Write to disk from any agent path.

## Done when

1. Open, edit, and save a 5,000-line file with no perceptible lag while typing or
   scrolling.
2. A CRLF file round-trips unchanged — `git status` clean after open + save.
3. Undo: type a sentence, one `ctrl+z` removes a word-group, not one character.
4. Selection renders correctly with mouse drag and with shift+arrows, including
   on a line containing CJK/emoji.
5. Tab / shift+tab indents and dedents a multi-line selection.
6. Enter after an indented line preserves that indentation.
7. A second holder opens the same file read-only with the holder named;
   take-over works after the lock goes stale.
8. Modify the file externally after opening, then save → refused with the three
   options.
9. Closing a dirty file routes through `Confirm`.
10. With `autosave = "after_delay"`: typing stops → the file saves ~1s later →
    the dirty marker clears. Typing continuously for 30s produces **one** save at
    the end, not thirty.
11. Autosave on a file held by another lock holder does **not** fire and does not
    spam refusals.
12. Autosave does not trigger Point's own "changed on disk" refusal on the next
    manual save.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```
Leader → `e`, open `backend\runtime\runtime.py` (21KB) from the tree.
**Expect:** instant open, smooth scroll, correct highlighting.

```powershell
# 2. CRLF round-trip — the check that matters most on Windows
git -C D:\Projects\Point status --short
```
Open a file, save it **without editing**, then run that again.
**Expect:** no change. **Fails if:** the whole file shows as modified.

```powershell
# 8. external change
Add-Content D:\Projects\Point\backend\runtime\state.py "# external"
```
With that file already open in the editor, press save.
**Expect:** refused, with reload / overwrite / diff.
**Fails if:** it overwrites silently.
Remove the added line afterwards.

```powershell
# 7. lock — open the same file from a second Point instance
```
**Expect:** read-only, with the holder named and the age shown.

In the app, on a line containing `日本語 🎉`, drag-select across it.
**Expect:** the selection highlight covers exactly the right cells.

## Handoff

State after this episode: the editor is a correct small editor with undo,
selection, locks, and safe saves. The terminal and the Explainer plug in next.

Record: whether this took one session or two, the CRLF/BOM handling details, and
any Windows Terminal key combinations that did not arrive.

Do not proceed until every check above passes.
