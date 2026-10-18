# F20 — Live Stale Detection

**Phase:** P2 · **Batchable:** no · **Depends on:** F13b (done) ·
**Backend:** B32 ✅, B7 ✅ — **no new backend work**

## Goal

**An open document notices that its file changed on disk, without
waiting for someone to try to save it.**

F13's Done-when 8 is correct and stays correct: an external change
followed by a save is refused with `[reload] [overwrite] [diff]`, and
that was re-confirmed on the real application during F13b's post-close
patch. But the check runs **only on the write path**. A file edited
externally while Point's own copy is locally clean shows **no signal at
all** — the tab looks clean, the status bar says nothing, and the buffer
on screen is quietly out of date for as long as nobody presses `ctrl+s`.

With autosave on, the window is usually short. With `autosave = "off"`,
on a file you opened to read, or on a branch switch that rewrites forty
files under you, it is unbounded.

**This episode adds a new TRIGGER into F13b's existing refusal
machinery. It is not a second staleness mechanism**, and the distinction
is the whole design: B32's `expected_mtime` precondition stays the single
source of truth about whether a write is safe, and `stale.go`'s
three-button dialog stays the single place a user resolves it.

> Task doc — describes **intent**. The completed doc
> (`../../completed/frontend/F20-live-stale-detection.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Found during:** F13b's post-close patch, while re-testing Done-when 8
independently on the real application. Recorded there as a gap rather
than investigated, because nothing about Done-when 8 was wrong — see
that doc's "Done-when 8 — confirmed intact, not re-investigated".

## Build

### 1. The check — polled, and `CmdCheckFileStale` is already built

**`CmdCheckFileStale` exists end to end and has no caller.** F13b said so
in as many words ("`CmdCheckFileStale` exists and may stay unused"),
because the write reply carries the new mtime and a save therefore needs
no follow-up check. This episode is the caller it was built for:

```
frontend\internal\protocol\types.go:49    CmdCheckFileStale  = "CheckFileStale"
frontend\internal\protocol\types.go:125   EvFileStaleResult  = "FileStaleResult"
backend\runtime\runtime.py:860            _handle_check_file_stale
                                          -> ProjectManager.check_stale
                                          -> {changed: bool}
```

It takes `path` and `mtime` and answers one boolean against the mtime
the document is already carrying. **So the backend half of this episode
is zero lines.**

**Poll, do not watch, and the reason is measured rather than
preferential.** F13b's round 3 established that `_atomic_write` ends in
`os.replace`, which on Windows fires `Deleted` + `Renamed` and **no
`Changed` at all** — a `FileSystemWatcher` configured the obvious way is
structurally incapable of seeing a save, and the gate's own watcher had
to be rebuilt around `Renamed`/`NotifyFilters.FileName` once that was
measured. A watcher per open document would have to get that right for
every editor anyone else uses, plus network drives and WSL mounts.
F13b's task doc also lists "add a filesystem watcher" under **Do NOT**;
this episode is where that call is revisited, and the answer is still no.

A poll asks the backend — which is the thing that already owns
`check_stale` — and gets the same answer `WriteProjectFile` would.

**Decide and record the interval.** A reasonable default is one check per
open document every few seconds, off a single workspace-level ticker
(the shape `heartbeatOn` already uses for locks: one ticker, every held
lock refreshed on it — explicitly not the C5 per-entity-timer case,
because it sets no per-document deadline). Do not give each document its
own ticker.

### 2. The marker, and the pause

When a document goes stale this way:

- **A visible marker on the tab and in the status bar.** Distinct from
  the dirty bullet, which means something different and can be true at
  the same time. `status.go`'s segments are where the readout goes;
  `tabs.go`'s `tabLabel` is where the tab marker goes.
- **Autosave pauses for that document, with the reason said once.**
  F13b's `doc.autosaveBlocked` is exactly this mechanism and already
  carries the "say it once rather than once a second" rule — a stale
  document is a document whose next autosave would be refused anyway, so
  blocking it is honest rather than defensive. The status bar's "autosave
  stopped" segment is already gated on that flag.

**The pause must use `autosaveBlocked`, not F13b's `autosavePaused`.**
They are different things and the post-close patch's own doc says why:
`autosavePaused` is a workspace-wide, temporary pause owned by a dialog,
cleared wholesale on resume; `autosaveBlocked` is a per-document refusal
that must survive one. A stale document is the second kind.

### 3. Do not open the dialog by yourself

**The marker is the signal. The dialog is the user's move.** F13b
settled this and the reasoning is recorded in its "Still owed":

> An autosave that hits `stale` does not open the modal — it says one
> line and stops autosaving that file until `ctrl+s`. A modal appearing
> a second after you stop typing, for someone else's change, is how
> people turn autosave off.

A poll makes that worse, not better: a modal can now appear while the
user is typing in a *different* file, or in Chat. So detection changes
what is **shown**; the existing `[reload] [overwrite] [diff]` dialog
(`stale.go`) still opens from where it opens today — a manual save, or
whatever affordance this episode gives the marker (pressing a key on the
marked tab is the obvious one, and is this episode's to decide).

`[reload]` and `[overwrite]` keep their existing `reload.go`
implementations unchanged — one splice, so one `ctrl+z`.

### 4. The two interactions that will bite

- **A poll tick does not fire while the Editor is backgrounded.** Root
  routes every message to `r.current()` only; this is the same routing
  that caused F13b's wedged-save regression and that still stops lock
  heartbeats (see `F16-polish.md`'s carried-forward entry). So a
  backgrounded Editor detects nothing until the user returns. **That is
  acceptable and should be stated rather than fixed here** — the user
  cannot act on a marker they are not looking at, and a check on the
  foreground transition covers the case completely. Do not reach for
  `app.BackgroundReplies` to make polling work in the background; read
  its doc in `app\contribute.go` first, and note that answering `true`
  whenever any document is open turns the Editor into a workspace that
  receives every envelope in the app for the whole session.
- **Point's own writes must not trip this.** Autosave rule 2 — the write
  reply carries the new mtime and `onWriteSucceeded` records it — is what
  makes that true today for the save path, and the poll reads the same
  `d.mtime`. A poll in flight **across** a write is the race to get
  right: a check sent with the old mtime can land after the write
  succeeded and report `changed: true` for Point's own save. Discard a
  poll result for a document whose `d.saving` is set or whose mtime moved
  since the check went out. This is Done-when 12 restated for a second
  caller, and it is the one thing in this episode that can produce a
  false "someone changed your file".

## Do NOT

- **Add a filesystem watcher.** Measured reason above, not taste.
- **Build a second refusal path.** B32's `expected_mtime` precondition
  stays the only thing that decides whether a write is safe. This
  episode detects and reports; it never decides.
- **Re-derive staleness on the frontend.** No `os.Stat`, no mtime
  comparison in Go beyond "is this the same number the document already
  had". `check_stale` is the backend's answer and the frontend has no
  business having a second opinion — the same rule F13a's "B32's
  metadata is carried, never re-derived" already states.
- **Open the stale dialog from a poll result.** See Build 3.
- **Auto-reload a clean document**, however tempting it looks when the
  buffer has no unsaved changes. It silently moves the caret and the
  scroll position under the user, and it discards the undo history of a
  file they may have been reading for a reason. Reload stays a button.
- **Touch `writeEnding`, C4's tiebreak, or any of B32's byte metadata.**
  Nothing here changes what a save writes.
- **Reopen Done-when 8.** It was re-confirmed working on the real
  application during F13b's post-close patch. This is a new trigger into
  the same machinery.
- **Fix the backgrounded-heartbeat issue** while you are in here. It is
  `F16-polish.md`'s, it needs a lock-lifecycle decision, and it is a
  different bug that merely shares a cause.

## Done when

1. A file changed by another program while Point's copy is **locally
   clean** shows a stale marker within the polling interval, with no save
   attempted and no keystroke in the editor.
2. The same is true for a **dirty** buffer: the stale marker and the
   dirty bullet are both shown, and they are visually distinguishable.
3. Autosave stops for that document and the status bar says why, **once**
   — not once per poll.
4. **No dialog opens by itself**, in any of the three cases: the file is
   the active document, the file is an inactive tab, or the Editor is not
   the active workspace.
5. Acting on the marker opens the **existing** `[reload] [overwrite]
   [diff]` dialog — the same overlay `stale.go` builds today, proven by
   there being exactly one construction site in the package.
6. `[reload]` restores a clean, up-to-date buffer, clears the marker, and
   is **one** `ctrl+z` — `reload.go`'s existing single splice.
7. **Point's own saves never raise the marker.** Autosave a file
   continuously for 30 seconds and it never reports itself stale, with
   the poll running throughout. This is the race in Build 4.
8. A document whose file is **deleted** externally is handled explicitly
   and says so, rather than reporting stale and then failing at reload.
9. Switching away and back re-checks on the foreground transition, so a
   change made while the Editor was backgrounded is visible immediately
   on return rather than after one further interval.
10. Idle cost is nil: with no files open, nothing is sent. With files
    open and nothing changing, the poll is the only traffic and
    `F16-polish.md`'s "idle Point uses ~0% CPU" still holds.

## Verification gate (PowerShell)

Per CLAUDE.md's env-var-override rule. Nothing below names the real
`%APPDATA%\Point` or `%LOCALAPPDATA%\Point`.

```powershell
$ErrorActionPreference = "Continue"
$scratch = Join-Path $env:TEMP "point-scratch-f20"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    # ... the steps below ...
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Done-when 1 — the headline case.** Open a fixture in the Editor, do not
type. From a second shell:

```powershell
Add-Content -LiteralPath "D:\Projects\Point\backend\tests\fixtures\f20\clean.py" "# external"
```
**Expect:** the stale marker appears within the interval, with no save
attempted. **Fails if:** nothing shows until `ctrl+s` — that is the
current behaviour and the whole reason for this episode.

**Done-when 7 — Point's own saves must not trip it.** Type continuously
in a file for 30 seconds with autosave on and the poll running.
**Expect:** saves land, the marker never appears.
**Fails if:** the marker flickers on after any autosave — the poll raced
the write and read a pre-write mtime.

**Done-when 4 — nothing opens by itself.** With a stale file open as an
INACTIVE tab, and again with the Editor backgrounded, wait several
intervals. **Expect:** no overlay, ever. **Fails if:** a modal appears
over Chat.

**Done-when 8 — deletion.**
```powershell
Remove-Item -LiteralPath "D:\Projects\Point\backend\tests\fixtures\f20\gone.py"
```
**Expect:** a distinct message naming the file as gone.
**Fails if:** it reports stale and then fails on `[reload]`.

**Done-when 10 — idle cost.**
```powershell
$p = Get-Process point
$before = $p.CPU ; Start-Sleep -Seconds 60
(Get-Process point).CPU - $before
```
**Expect:** very close to 0 with files open and nothing changing.

**The suite, unchanged and green:**
```powershell
cd D:\Projects\Point
gofmt -l . ; go build ./... ; go vet ./frontend/... ; go test -count=1 ./frontend/...
```

## Handoff

- **The one number to record** is the polling interval and why it was
  chosen, measured against Done-when 10 rather than assumed.
- **Record whether the marker got its own key** or reuses an existing
  one, and if a new function key: `f5` re-index, `f6` take over, `f7`
  close tab, `f8` is F14's terminal. The next one after that.
- **State explicitly in the completed doc that B32 and Done-when 8 were
  not changed** — "live stale detection" and "the stale refusal" are two
  different things and a later reader will conflate them.
- This episode makes `CmdCheckFileStale` used for the first time since
  B32 built it. If it stays unused after all — because the design lands
  somewhere else — say so, rather than leaving a doc claiming a caller
  that does not exist.
