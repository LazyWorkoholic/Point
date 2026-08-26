# F8 — @ Picker and Attachments

**Phase:** P1 · **Batchable:** no · **Depends on:** F7 · **Backend:** B7, B12

## Purpose

One key opens agents, files and memory. No second sigil to remember, and the cost
of an attachment is visible before it is paid.

## Build

### Mode from the first character after `@` — no new syntax
```
@                → fuzzy over the project tree (when a project is open)
                   with no project: filesystem browse rooted at cwd
@C:\  @D:\       → filesystem browse, absolute (Windows drive letters)
@\  @.\  @..\    → filesystem browse, relative
@/   @./  @../   → same; accept forward slashes, users type both
@~\  @~/         → user profile
@<name>          → agent, if it exactly matches a registered agent name
@?               → explicit explorer popup, arrow-key navigation
```
Ambiguity rule: an **exact agent name wins**; otherwise treat it as a path.
Typing `@D:\Projects\` naturally becomes path completion because it starts with a
drive letter.

### Sections in the picker
```
╭─ @ ────────────────────────────────────╮
│ filter: run_                           │
│ ─ Agents ───────────────────────────── │
│   @research                            │
│ ─ Files ────────────────────────────── │
│   backend\runtime\task.py              │
│   backend\runtime\runtime.py           │
│ ─ Memory ───────────────────────────── │
│   ⌸ Runtime uses stdin/stdout          │
╰────────────────────────────────────────╯
```
The same `Popup` from F7 — its **fourth** consumer. Memory section hidden while
empty (it stays empty until B13).

### File list source
`project.list_files` (B7): honours `.gitignore` with a toggle, skips binaries and
oversized files, and always skips `.venv`, `__pycache__`, `node_modules`, `.git`.

**Go never walks the filesystem.** One resolution point, on the backend, so local
and SSH behave identically.

### Guards — messages, not errors
```
> 100 KB          refuse · offer "attach first 200 lines"
binary            refuse
directory         refuse · offer a file listing
outside project   allowed only with allow_outside_project; the chip shows ↗
```

### Chips and cost
On selection a chip is added to the input's top row (F4 renders it):
```
@task.py (~1.2k tok) ✓ current
@notes.md (~800 tok) ⚠ changed — will resend
```
The estimate comes from B12's resolution response. **Showing the cost before
paying it is the point of this feature.**

### Change detection
State comes from the backend's `(mtime, size)` comparison. `✓ current`
contributes nothing on the next turn; `⚠ changed` will re-send. **Never show
`current` for a file that changed** — the model would answer about code that no
longer exists.

### Detach
`esc` or backspace on a chip, or `/detach`. **Detaching never deletes** — it
removes the binding only.

### Editor context
In the Editor workspace `@` remains available for pulling in a *different* file,
but the Explainer's normal path needs no typing — it takes cursor, selection and
enclosing scope implicitly (F15).

## Do NOT

- Read files in Go.
- Implement memory search (B13) — render the section only when the backend
  returns results.
- Implement result or diff cards (F9).

## Done when

1. `@` with a project open lists project files fuzzily; without one it opens
   browse mode rooted at cwd — **never a dead key**.
2. `@res` offers the `research` agent above any file named `res*`.
3. `@D:\` switches to path completion with no extra keystroke; so does `@..\`.
4. `@?` opens the explorer popup and navigates with arrows.
5. A 40MB file is refused naming the limit; a binary is refused; a directory
   offers a listing.
6. The chip's estimate is within ~20% of what the context report shows it cost.
7. Touch an attached file → the chip flips to `⚠ changed` → the next send re-sends
   it → the chip returns to `✓ current`.
8. `.gitignore`d paths are absent; with the toggle off they appear, but `.venv`
   still does not.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the app, from a project directory:
- Type `@` and browse. **Expect:** project files, no `.venv`.
- Type `@D:\` . **Expect:** path completion.
- Type `@res`. **Expect:** the agent listed above any similarly-named file.
- Type `@?`. **Expect:** the explorer popup.

```powershell
# 5. guards
$f = "$env:TEMP\big.log"
$fs = [System.IO.File]::Create($f); $fs.SetLength(40MB); $fs.Close()
```
Attach `$env:TEMP\big.log`, then `C:\Windows\System32\notepad.exe`, then
`$env:TEMP`.
**Expect:** three refusals with reasons and offers.
```powershell
Remove-Item $f
```

```powershell
# 7. change detection — with backend\runtime\task.py attached:
Add-Content D:\Projects\Point\backend\runtime\task.py "# touch"
```
**Expect:** the chip flips to `⚠ changed`. Send, then check the context report:
the file was re-sent **once**, not every turn.
**Fails if:** re-sent every turn (waste), or never (stale).
Remove the added line afterwards.

## Handoff

State after this episode: attaching context is one key, costs are visible before
sending, and the guards prevent the obvious foot-guns. The picker is the fourth
consumer of one widget.

Record: which Windows path forms you tested, and any that the guard mishandled.

Do not proceed until every check above passes.
