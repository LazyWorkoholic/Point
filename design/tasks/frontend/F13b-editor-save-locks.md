# F13b — Editor: Save, Locks and Autosave

**Phase:** P1 · **Batchable:** no · **Depends on:** F13a (done) ·
**Backend:** B7, B32

**The second half of F13.** F13a built the editing half and is complete
and verified — its task doc is `F13a-editor-core-editing.md` beside this
one, its completion record
`../../completed/frontend/F13a-editor-core-editing.md`.
This doc holds the save path and nothing else. Every requirement below is
F13's own, moved rather than re-derived.

F13's original doc anticipated the split: "The largest frontend episode.
It may take two sessions — if so, stop at a working point, record what is
done, `/clear`, and resume with a note." F13a is that note. See
`../../WORKFLOW.md`'s "Splitting an episode" for the convention.

## Purpose

An editor that saves correctly. F13a's buffer is byte-exact in memory and
carries everything B32 reports about a file; this episode is what puts it
back on disk without losing a byte, without clobbering someone else's
change, and without asking the user to think about either.

## Build

### Save, locks, and staleness

```
open   acquire a soft lock (B7); heartbeat while open; record mtime
save   if the file changed on disk since open → REFUSE:
       "changed on disk since you opened it — [reload] [overwrite] [diff]"
       release the lock on close / unload / quit
held   another holder → open read-only; status bar shows
       "held by aryan (12s ago) — [take over]"
```

Stale locks (heartbeat > 60s) can be taken over. This protects you from your
own second terminal as much as from an SSH guest.

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

### The status bar gains its lock segment

F13's status line is `file · line:col · language · CRLF|LF · lock state ·
dirty marker`. F13a ships every segment **except `lock state`**, and
deliberately: nothing acquired a lock, and a lock readout with no lock
behind it is the hint-that-lies this project refuses. `StatusSegments`
(`editor\status.go`) adds it here.

## Do NOT

- Build the terminal (F14) or Explainer integration (F15).
- Add splits, minimap, LSP, or symbol search.
- Add a filesystem watcher — the save-time check covers v1.
- Write to disk from any agent path.
- Re-open anything F13a settled. The buffer, the caret, undo, the tab
  strip, the tree and the highlighter are done and verified.

## Done when

Numbered as in F13's own doc, so the two halves together cover 1–12.
**F13a covers 3, 4, 5, 6 and the open half of 1.**

2. A CRLF file round-trips unchanged — `git status` clean after open + save.
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

Plus the save half of 1: a 5,000-line file **saves** with no perceptible
lag.

## Manual verification (PowerShell)

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
# 7. lock — a SECOND HOLDER, via cli.py rather than a second Point
cd D:\Projects\Point\backend
uv run python cli.py project lock acquire --path D:\Projects\Point `
    --file <relative-path> --holder someone-else@other-id
```
**Expect:** read-only, with the holder named and the age shown.

> **F13's original step said "open the same file from a second Point
> instance", and that is unachievable** — `backend\lockfile.py` refuses a
> second backend against the same profile, and two profiles have two
> `runtime.db` files and therefore two separate `file_locks` tables, so
> they cannot see each other's locks either. `cli.py` deliberately does
> not take the single-instance lock, which is why it can be the second
> holder; B32's own gate uses it this way in checks 6–8. Corrected in
> B32's handoff before F13a started.

Start from `backend\tests\gate_f13a.ps1` rather than from CLAUDE.md's
snippet: it already has the scratch-profile setup, the fixtures, the
`Assert-That` accumulator and `$ErrorActionPreference = "Continue"` (which
B32 established is required for any gate that provokes backend stderr).

## What F13a settled that this episode must honour

Decisions taken before F13a's code was written. Full reasoning in
`../../completed/frontend/F13a-editor-core-editing.md`.

- **A2 — the holder key, and it is not written yet.** The WHOLE
  `<username>@<full-id>` string, generated once and persisted in
  `state.json` as one opaque value, never regenerated on relaunch; the
  display form splits on the LAST `@` and shows the first 8 of the id;
  comparison is whole-string. The backend neither generates nor
  validates it (B32's Decision 4).

  **The trap:** `config\state.go`'s `saveStateTo` gives
  keep-what-is-on-disk treatment to exactly `LastWorkspace` and
  `WorkspaceState`. Every other field is serialised from the writer's own
  startup snapshot, and `chat.Workspace` writes the whole struct — so a
  new top-level `Holder` field is **erased by chat's next draft save on
  the launch that generated it**. Aryan's settled answer: extend the
  keep-if-zero clause to `Holder`, and generate it in `main.go` before
  `chatState := state`.
- **B4 — the save-side half of the mixed-file policy is still open.**
  F13a settled the display: a mixed file opens normally editable and the
  status bar reads `MIXED n/m`. B32 reports `crlf_count`/`lf_count` and
  deliberately stops there. **This episode picks what a save writes**
  (Aryan's round-1 answer was the majority ending), and the status bar
  must not claim otherwise.
- **B7 — heartbeat every 20s** against `LOCK_STALE_AFTER_S = 60`: three
  beats inside the window, so one dropped beat does not make Point look
  dead to itself.
- **B9 — `app.OpenWorkspaceOverlayMsg`, unbuilt.** A workspace cannot open
  an arbitrary overlay today; the three existing paths (`OpenAttachmentPickerMsg`,
  `ContextExpandable`, `MouseOverlayTarget`) each put construction in
  `app`. The three-button `stale` dialog needs it — `Confirm` is two
  buttons by construction — and so does the dirty-close confirm.
- **B10 — `[diff]`** renders a plain unified diff of buffer-vs-disk inside
  the `editor` package, with no apply mechanism, and **deliberately not
  named like F9b's diff card** (CLAUDE.md's rule about reusing a later
  episode's name).

## What is already in place

- **`doc` already carries `mtime` and `size`** from the read, precisely so
  a save does not re-stat the file and reopen the race `expected_mtime`
  exists to close.
- **`line_ending`, `bom` and `trailing_newline`** are carried verbatim and
  un-re-derived. `WriteProjectFile` takes all three back, so a CRLF+BOM
  file round-trips byte-identical using only what the read reported.
- **`protocol\types.go` already has `CmdWriteProjectFile` and
  `EvProjectFileWritten`**, and all four lock commands
  (`CmdAcquireFileLock`, `CmdHeartbeatFileLock`, `CmdReleaseFileLock`,
  `CmdTakeOverFileLock`) plus `CmdCheckFileStale`. No Go caller yet, by
  design.
- **`envelope_test.go:26` must gain `EvProjectFileWritten`** — B32's
  handoff asks for it and F13a deliberately did not touch that file.
- **`ctrl+s` currently says** "save is not wired yet — the write path
  lands with locks and autosave", via `say()`. Replace it.
- **`closeTab` currently REFUSES** to close a dirty tab and explains why.
  Done-when #9 replaces that with `Confirm`.
- **`DirtyBufferCount`** is implemented, and F13a taught the idle sweep to
  skip a dirty workspace rather than discard its edits. Once autosave
  lands, that skip fires far less often — it is the no-save-path
  safety net, not a permanent behaviour.

## Note: no CheckFileStale round trip is needed

`WriteProjectFile` returns the **new mtime** in its reply. That is exactly
autosave rule 2 ("record Point's own write mtimes"), so a save needs no
follow-up `CheckFileStale` — B32 built the reply that way for this reason.
`CmdCheckFileStale` exists and may stay unused.

## Spun out of this episode's testing — F19 and B33

Manual testing after this episode found three things that are **not on
F13's Done-when list** and are therefore not this episode's:

- **F19** (`F19-editor-keymap-scroll.md`) — the tree's h/j/k/l vim
  motions were undocumented, closing a tab had no one-key binding, and
  the scroll offset in both the code pane and the tree was bounded by
  `count-1` instead of `count-rows` (so the end of any list showed one
  item). F19 depends on F13a and F13b and reuses C3's confirm.
- **B33** (`../backend/B33-transport-utf8.md`) — `backend\main.py`
  read stdin and wrote stdout in the ANSI codepage, not UTF-8, so a
  save of any file with non-ASCII bytes was silently mojibake'd (and
  REPORTED as success) or dropped outright. This is why
  `design\GIT_STRATEGY.md` and `design\project_idea.md` would not save
  while `runtime.py` appeared to. Live since B6; nothing in this
  episode or B32 caused it, and nothing here can fix it — the defect is
  below the wire.

Neither reopens this episode. **Done-when 2's byte-exactness is only
true for a non-ASCII file once B33 has landed**, which is worth knowing
before reading this episode's own gate as proof of it.

## Handoff

State after this episode: **F13 is complete.** The editor is a correct
small editor with undo, selection, locks and safe saves. The terminal
(F14) and the Explainer (F15) plug in next; F9b's diff card gains its
apply target.

Record: the CRLF/BOM round-trip evidence (hashes before and after), which
ending a mixed file is written with and why, the holder key's persisted
shape, and any Windows Terminal key combinations that did not arrive.

Update `../../completed/frontend/INDEX.md`'s F13a row from
🟡 to ✅ for F13 as a whole once this passes.

Do not proceed until every check above passes.
