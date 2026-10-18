# F16 — Polish

**Phase:** P2 · **Batchable:** no · **Depends on:** all

## Purpose

The difference between "it works" and "it feels finished". Everything here is
small; together they are what a viewer actually notices.

**This episode is `[P2]`, and depends on `all`** — which is already the
"only if time remains" bucket: nothing in `design/tasks/` may grow a
dependency on anything here, and CLAUDE.md's phase rule is that P2 is
touched only after every P1 item is done and verified. Items carried
forward into it from an earlier episode's verification are recorded with
the reasoning they were logged with, and with the episode that deferred
them named, so picking one up does not mean rediscovering why it was not
done at the time.

This is NOT the parking lot. `project_idea.md` and the `INDEX.md` files
carry `[PARK]` entries, which are deferred *by design* — the hook exists
and the feature deliberately does not, and CLAUDE.md is explicit that a
parking-lot item is not work and must not be built because it seems
adjacent. Everything here is wanted; it is a question of time, not of a
decision still to be made.

## Build

### Header — carried forward from F1

F1 built the header line and deliberately left two things unfinished. They are
cosmetic, which is why they were not fixed there; they are recorded here because
this is the episode that owns cosmetics.

- **The animation slot is a static glyph.** `render.go`'s header draws a fixed
  `◇ ◈ ◇` on the left where LAYOUT.md calls for an animation. Nothing animates
  yet, and nothing schedules a frame tick. Whatever this becomes, it must be
  cheap: a `tea.Tick` that repaints the whole frame several times a second is
  the easiest way to make an idle TUI burn CPU, which is the thing
  **"4. idle CPU"** in this episode's own verification is measuring.
- **Header padding degrades rather than reflows at narrow widths.** The three
  header segments are laid out by computing a left and a right gap, each clamped
  to a minimum of one cell. Above roughly 90 columns this centres `P O I N T`
  correctly; below it the clamp wins and the title drifts off-centre rather than
  the right-hand segment dropping out. Decide the narrow-width behaviour here —
  most likely: drop the right segment first, then the left, before letting the
  title move.

### States audit
Walk **every** list, pane and popup and confirm three states exist:
```
empty     a sentence naming what would fill it, and how
loading   a dim indicator, never a frozen blank
error     the reason in words, plus the action that fixes it
```
Cheap now, ugly to retrofit. Track it as a literal checklist, one line per
component, in the completed doc.

### Toasts
One component: `✓ Research done — F1 to view`. Auto-dismiss ~4s, stack at most 3,
never steal focus, never cover the input. Sources: task completion while
elsewhere, memory proposals, save failures, lock take-overs.

### Help and discoverability
- Help screen generated from the action registry (F7), grouped by category.
- Leader-pending hint bar showing available keys, on a short delay so fast users
  never see it.
- First-launch hint line in an empty chat naming exactly two things:
  `type a message` and `^P palette`. **Not a tour.**

### Editor keymap documentation and a one-key close -- ALREADY SHIPPED in 6e28f6b

**These two are DONE. They are recorded here, in the polish bucket they
belong to, rather than as an episode of their own.**

Both were found during F13b's manual verification and are not on F13's
Done-when list. They were briefly written up as a separate episode (F19)
and that episode was retired: two documentation-and-binding items are
exactly what this file is for, and a third episode number for them was
more bookkeeping than they are worth. The code is committed and correct;
only the grouping changed. Nothing here is outstanding.

#### The file tree's vim motions, documented

Reported as "`l` behaves like Enter -- intentional vim-motion binding
that isn't documented, or a stray keymap entry?". **Intentional, and a
complete set** that `treeKey` has had since F13a, mirroring chat's own
browse keys (`keys_test.go`'s `TestTreeNavigationKeys` says so):

```
up    k        down  j
right l        expand a folder, or open a file
left  h        collapse
enter          open a file, or expand a folder
pgup/pgdown    home/end
```

`l` and Enter are genuinely equivalent -- on a file `toggle()` reports
false so both fall through to `openSelected`, and on a folder both
expand it. Two spellings for the hand, not two behaviours.

The keymap was right and the documentation was absent, so removing the
motions would have been a regression for anyone using them since F13a.
`treeKey`'s own doc comment now carries the table, and `render.go`'s
empty state shows `in the tree: j/k or arrows move, l expand/open, h
collapse` -- the screen a new user actually reads.

#### `f7` closes the active tab

Routed through the **same `closeTab`** the tab strip's `x` uses, so a
dirty file still gets F13b's C3 confirm (`[ Close ] [ Cancel ]`, Cancel
focused, Close discards). No second close path and no second confirm.

Neither obvious binding was available, and both reasons are worth
keeping written down:

- **`ctrl+w` is the leader** (`actions.Leader`), intercepted before the
  workspace sees a key. That is what makes `ctrl+w e` and `ctrl+w w`
  work, so it cannot also mean close.
- **`ctrl+shift+w` is not a key Windows delivers.** bubbletea's
  console-record reader produces the same control character for `Ctrl+W`
  and `Ctrl+Shift+W`, so it arrives as `ctrl+w` -- the leader again, and
  close-tab would open the leader hint. Measured for `Ctrl+Shift+L`
  during F13b, which is why the lock take-over is on `f6`.

So `f7`, which keeps the editor's function keys contiguous and in a
memorable order: **f5 re-index, f6 take over a stale lock, f7 close the
tab.** Nothing in the chord registry claims it, so it needs no
`KeyCapturer` claim and F13a's claim list stays at two keys. **`f8` is
the next free one** -- F14's terminal should continue the block rather
than reach for a chord.

It is advertised in the empty-state list and appended **last** to the
status line, so `fitHints` -- which drops whole parts from the end --
sheds it first on a narrow terminal rather than a segment describing the
file.

### The scroll offset is bounded by count-1, not count-rows -- carried forward from F13b, DEFERRED

**This one is NOT done. It was fixed and the fix was deliberately backed
out, so the bug is live and this entry is the record of exactly what it
is and what the fix was.**

Both the code pane and the file tree bound their scroll offset by
`count-1`, so scrolling to the end of any list leaves ONE item visible
however tall the viewport is. Measured:

```
10-line file in a 20-row viewport (which should not scroll at all):
  scrolled to the end  -> top=9, 1 line visible
  one wheel notch back -> top=6, 4 lines visible     <- the reported "flash"
  another notch back   -> top=3, 7 lines visible
tree, 6 rows in a 4-row viewport -> top=5, 1 row visible
```

The wheel moves three lines at a time, which is where "a flash of the
last four items" comes from: the first scroll attempts do nothing
because `top` is already at its ceiling, and the next one jumps three.
These numbers were re-measured after the fix was reverted and are
identical, so the bug is in its original shape rather than a new one.

**It is the same expression in three places, not a shared viewport
component with one bug** -- the tree and the doc each own their `top`,
for good reasons (different content, different reveal semantics):

- `motion.go` `scrollBy`: `if max := d.buf.Lines() - 1`
- `motion.go` `ensureVisible`: `if d.top > d.buf.Lines()-1`
- `tree.go` `ensureVisible`: `if max := len(t.rows) - 1`

**The fix, which is known to work and is not in the tree:** extract one
`clampTop(top, count, rows int) int` into `textutil.go`, bounding at
`count-rows` floored at 0, and call it from all three. `scrollBy` has to
take `rows` as a parameter rather than remembering it -- the viewport
height is the layout's, recorded during `View`, and a scroll clamped
against a stale copy is the drifting-state bug F13b hit twice. The
caller has `w.textArea.H` at the moment of the scroll.

`ensureVisible` needs the clamp **before** its reveal gate as well as
after, which is load-bearing on two paths `scrollBy` never touches: a
**resize** (the viewport grows and nothing calls `scrollBy`) and
**Resume** (`applyResumeTo` assigns `d.top` straight from the suspend
blob, written against whatever height that session had). A test for that
must set `reveal = false`, or the post-reveal clamp covers it and the
test passes against the unfixed code -- which is what happened the first
time.

Three existing tests assert the OLD bound and will need retargeting:
`tree_test.go`'s `TestTreeEnsureVisibleLeavesAnUnrequestedScrollAlone`
(`want it clamped to len(t.rows)-1`), `motion_test.go`'s wheel test, and
`highlight_test.go`'s benchmark, which only needs the new parameter.

**Interacts with "Horizontal scrolling in the Code File box" below** --
that item moves `doc.left` rather than `doc.top`, so the two are
independent, but whoever does one should do the other and share the
helper.

### Lock heartbeats stop while the Editor is backgrounded — carried forward from F13b's post-close patch

**NOT done. Found while fixing the two save/autosave regressions after
F13b closed, and deliberately left out of that patch.**

`heartbeatTickMsg` reaches the Editor through `Update`, and Root routes
every message to `r.current()` only — the same active-workspace-only
routing that caused the wedged-save regression. So the 20s heartbeat
simply stops while the user is in Chat, and after B7's
`LOCK_STALE_AFTER_S = 60` this process's own locks are stale and
take-over-able by anything, including Point's own next acquire.

The write-reply case was fixed in that patch (`app.BackgroundReplies`,
`Root.deliverBackgroundReply`) and the heartbeat was not, for a reason
worth keeping: a dropped write reply **wedges** a document permanently,
while an expired heartbeat merely expires — the lock is re-acquired on
the next save attempt and the backend is the only judge of staleness
either way. Nothing is lost, and nothing is silently wrong.

**What makes this more than extending the existing interface.**
`BackgroundReplies` is about receiving a reply already in flight.
Heartbeating is about continuing to *assert* a claim, and that raises a
question the patch had no business answering alone: **should a
backgrounded workspace hold locks at all?** A reasonable answer is no —
release on background, re-acquire on foreground, which is roughly what
`ReleaseLocks` already does on unload. Another is yes, keep beating,
which makes the Editor hold locks for as long as Point runs. That is a
lock-lifecycle decision, not a bug fix, which is why it is here.

Interacts with the `BackgroundReplies` interface added by that patch —
whoever does this should read `app\contribute.go`'s doc on it first,
because the cheap-looking fix (answer `true` whenever any lock is held)
quietly turns the Editor into a workspace that receives every envelope
in the app for the entire session.

### `tabs.go:264`'s `autosaveBlocked` check is always true — carried forward from F13b's post-close patch

One line, cosmetic, pre-dates the patch that found it.

`confirmCloseDirty` sets `d.autosaveBlocked = true` at line 258 and then
tests `if d.autosaveBlocked` at line 264 to decide whether to append
"Autosave is stopped for this file — see the status bar" to the dialog
body. It is the same function, six lines apart, so the test can never be
false and the sentence is always shown — including on a perfectly
healthy file whose autosave was stopped one instant earlier *by this
dialog itself*.

The intent was clearly to report a PRE-EXISTING block (read-only, held by
another holder), so the fix is to read the flag **before** the pause sets
it. That is the whole change.

Not folded into the hotfix that found it: an unrelated cosmetic line in a
patch about two races is how a focused fix stops being reviewable.

### Responsive audit
Test every screen at:
```
200×50 · 120×40 · 100×30 · 90×26 · 80×24 · 70×20 · 60×16 · 50×12 · 45×10
```
Confirm: no overflow, no truncated borders, overlays refuse below their MinSize
with real numbers, header degrades to the watermark, the right pane hides and the
left column reflows with no gap.

**Expect to adjust the breakpoints here.** `LAYOUT.md` marks them provisional —
separate bordered boxes plus outer margins cost more rows and columns than the
original numbers assumed. Record the final values.

### Theme QA
Each of the four palettes, on a **dark** and a **light** Windows Terminal profile,
in transparent mode: readable text, distinguishable running/queued/done, error and
warning obviously different from the palette, focused vs idle borders
distinguishable, code blocks legible. **Gold is the one to scrutinise.**

### Performance pass
```
transcript render cache holds during streaming (only the live message re-renders)
header animation frozen when idle, capped at 10fps
right-pane subscriptions dropped when hidden
syntax highlighting limited to the visible window + margin
Go heap returns near baseline over 20 workspace load/unload cycles
```
Measure, then state numbers **only where measured**.

### Tier 2 auto-indent — only if time
Increase after `:` / `{`, dedent on `}` and dedent keywords, from a small
per-language table. Parked otherwise.

### Result-card code blocks — carried forward from F9a

The result card (UI-06) renders its body as **wrapped plain text**: no syntax
colouring, and no box frame around a code block. Indentation IS preserved —
that was fixed in F9a, because losing it makes code read as *different code*
rather than merely plainer — but a code block in a research result is visually
indistinguishable from prose.

**Why it was not fixed in F9a, and what the fix actually needs.** The markdown
renderer is `chat\markdown.go`, and the card lives in `app`. `chat` imports
`app`, so the card cannot reach the renderer, and the only two ways out were
both out of scope for a frontend-F9 session: move the renderer into a third
package (a refactor of F3's shipped code), or move the card into `chat` (which
contradicts why the tray is Root's at all — F09's Done-when #1 requires a task
whose originating view has been unloaded to still open its result).

So this is a **packaging** problem, not a rendering one. Pick one:

- extract `chat\markdown.go`'s renderer into `internal\markdown\`, imported
  by both `chat` and `app`. Cleanest, and F11's memory popup will want it too;
- or give `app` a minimal code-span/fence renderer of its own. Cheaper, and a
  second renderer that drifts from the transcript's is exactly the duplication
  this project has paid for four times (F5's Seq, F6's `taskCardWasVisible`,
  F8's Bug A, F9a's task-row vocabulary). Prefer the extraction.

Either way the box frame is `box.go`'s existing `CodeBorder`/`CodeText` roles,
already in every palette and already used by the transcript — so once the card
can call a renderer, the theming is free.

Confirmed with Aryan during F9a's verification as a deliberate deferral to
this episode, not an open gap. See `design/completed/frontend/F09a-task-tray-cards.md`.

### The @ picker's refusal notice is treated as a filterable row — found during F10b

`pickerShowRefusal` (`app\picker.go`) renders an attach refusal (e.g. a
binary file, or — once F10b's Behaviour tab exists — an outside-project
path with the override off) by prepending synthetic
`array.NewItem(fmt.Sprintf("__refused__%d", i), line).SetDisabled(true)`
rows ahead of the real agent/directory items, then calling
`r.picker.popup.SetItems(combined)`.

`.SetDisabled(true)` correctly makes Enter on that row a no-op (the same
behaviour F8's "no background tasks" placeholder row already has) — that
part is not the bug. The bug is that `array.Menu`/`Popup` treat this
notice row as ordinary DATA for two purposes it should never participate
in:

1. **It counts.** The footer's `n/total` indicator (`popup.go`) counts
   every item `Menu.Len()` sees, notice rows included — so a directory
   with 7 real entries and one refusal notice reads `8/8`, which is a
   count of something that is not an entry.
2. **It filters.** The notice is an ordinary row in the same filterable
   list, so typing a path segment (e.g. `..` to navigate up, in browse
   mode) runs the SAME fuzzy/prefix match against the notice's own text
   as it does against real entries — it can be filtered in or out
   alongside directories and files it has nothing to do with, and
   because it is also disabled, a user who manages to filter it
   in and presses Enter on it gets the silent no-op described above
   with no indication why.

**Exact repro:** attach a file the backend refuses (a binary file is the
easiest — B12's own existing, correct guard), then in the SAME picker
session type `..` into the filter to browse up a directory. The refusal
notice line is filtered in or out exactly like a real directory entry
would be, and if it survives the filter and is selected, Enter does
nothing.

**Not this episode's fix.** The real fix is a way to mark a row as
*chrome* — excluded from both `Menu.Len()`'s count and `Filter()`'s
matching, shown unconditionally at a fixed position — which
`array.Item`/`Popup`/`array.Menu` have no concept of today. F8's
disabled placeholder rows (`"no background tasks"`, `"Loading…"`) have
the same shape and would likely want the same mechanism once it exists,
so this is a small, narrow addition to `array`'s own API, not a
picker-local patch.

### The generic failure footer truncates long error text — found during F10c

`liveFooter` (`frontend\internal\chat\stream_render.go:428`) builds the
`"✗ failed — " + e.errText` line (line 442) for every `TaskFailed`/`Error`
terminal state, then renders it through `padCells(line, width)` (line 451)
— `width` is the transcript's own column width, passed in unchanged from
`liveFooter`'s two call sites (`stream_render.go:199`, `:273`). `padCells`
(`chat\textutil.go:44`) cuts anything wider than `width` via
`truncateCells` (`chat\textutil.go:61`), which trims to the cell budget
with no ellipsis, no wrap, and no "show more" — it is built for a
single-line layout element, not a message body.

**Exact repro, from F10c's error-case fixture**
(`backend\demo\fixtures\error_case.json`): the scripted message is

> `demo: scripted failure -- Point's error path, exercised on purpose, no real provider was involved`

At a transcript width narrower than that string plus the `"✗ failed — "`
prefix and the trailing `"  [Retry ^R]"` suffix, the rendered line cuts
mid-word — observed ending `"...exercised on purpo"`. Any `TaskFailed`/
`Error` whose message is longer than one line's worth of transcript width
hits the same cut, demo or otherwise; this fixture is simply what surfaced
it live.

**Not fixed here or in F10c** — per that episode's own scope (wire Demo
mode into existing surfaces, not change them) and this file's own
instruction not to fix it in this entry either. A real fix needs
`liveFooter` to wrap the error text across multiple lines (or truncate
with a visible ellipsis) rather than pass a long single-purpose line
through a single-line padder — this episode's job when it gets here.

### Horizontal scrolling in the Code File box — carried forward from F13 (18a)

Lines wider than the text area cannot be read past the right edge by any
deliberate gesture.

The mechanism exists and is exercised: `doc.left` is a horizontal scroll
offset in display cells, `ensureVisible` moves it to keep the caret in
view, and `sliceCells` renders from it — including the case of a
double-width rune straddling the left edge, which is padded rather than
half-drawn. So a long line IS reachable today by walking the caret to it
with `end` or the arrow keys.

What is missing is scrolling as its own action, independent of the caret:

- a horizontal wheel / trackpad gesture (`tea.MouseButtonWheelLeft` and
  `WheelRight`, which bubbletea decodes on Windows via `MOUSE_HWHEELED`
  and `frontend\internal\editor\mouse.go` currently ignores);
- a key pair for it;
- some indication that there is content past the edge, so a truncated
  line does not read as the file's real content.

**The one trap worth knowing before starting.** `doc.reveal` separates
"the caret asked to be shown" from "the user scrolled" — it is what made
the vertical wheel work after `ensureVisible` was undoing every scroll on
the frame it happened (18a's second verification round: `top after
wheel=3, top after View=0`, five times running). A horizontal scroll must
set `left` WITHOUT setting `reveal`, for exactly the same reason.

Not urgent: code in this repo is mostly inside 100 columns, and the Code
File box is 85 columns wide at 120×30 with the right pane open.

### Creating files, and opening another directory as the project root — carried forward from F13 (18a)

Two VS-Code-shaped affordances the Editor has no route to:

- **create a new file** from inside the Editor (and the directory to put
  it in);
- **open a different directory as the project root**, rather than the one
  Point was launched from.

Both are deliberately absent rather than overlooked, and the reasons are
worth keeping with the request:

- The Editor resolves exactly one project, once, from the working
  directory (`ProjectResolve{path: cwd}`), because it has no session and
  therefore no `project_id` of its own. Changing roots means deciding what
  happens to open buffers, the file index, and any lock held under the old
  root.
- Creating a file is a **write**, and the backend already supports it
  precisely: `WriteProjectFile` with `expected_mtime: null` means "I
  expect no file here", which B32 chose specifically so that creating a
  file and clobbering a concurrent change could never be the same
  request. It refuses `no_parent_dir` rather than creating directories.
  So the backend half is built and settled; what is missing is the
  frontend's — a name prompt, a target directory, and the refusal UI.
- F13b (18b) owns the save path, the lock lifecycle and the refusal UIs.
  Creating a file sits naturally **after** that, reusing them, and would
  be disproportionate before it.

So: not before 18b, and not part of it either.

### Tab strip: shrink tabs to fit rather than hiding all but the active one — carried forward from F13 (18a)

Confirmed current behaviour, and not a regression: once the open tabs no
longer fit the Code File box's content width, the strip scrolls
horizontally with `‹` and `›` markers and keeps the ACTIVE tab visible —
so with enough files open you see one filename and two arrows. Below
`tabsCollapseWidth` (44 cells) it collapses to the active filename alone,
which is LAYOUT.md's own rule for narrow terminals.

Wanted instead, VS-Code-style: keep **every** tab visible and make each
one narrower, truncating the filenames rather than the strip.

What makes it more than a width tweak:

- `renderTabs` measures each tab at its natural width and then windows
  the list. Shrinking means a second pass that divides the available
  cells among N tabs and truncates each label to its share — with a
  floor, because a two-cell tab is not a tab.
- That floor decides where the two behaviours meet: past some number of
  open files, shrinking has to give way to scrolling anyway, so this adds
  a regime rather than replacing one.
- `tabSpan` already records where each tab was drawn and `mouse.go`
  hit-tests against it, so clicking and closing follow for free — but the
  `×` needs a share of a narrower tab too, and at some width it is the
  first thing that should go.
- Truncation must be display-width aware (`runewidth.Truncate`, as
  `paneBox` already does for titles), since a filename can contain CJK.

Not urgent: the strip is correct and legible today, and the dirty marker
and the active accent both survive the current behaviour. This is about it
reading like a familiar editor rather than about information being lost.

### Demo recording
Record the v1 demo script — **the one in `design/WORKFLOW.md` under "What done
looks like"**, which covers ghost text, autosave + web preview, per-workspace
idle timers, and crash recovery — as a 60–90s capture for the README. Live demos fail; a
recording is the highest-return artefact per minute of effort. It also forces you
to run the script end to end, which reliably finds bugs.

On Windows: Xbox Game Bar (`Win+G`) records a window to MP4, or use
[asciinema](https://asciinema.org) via WSL if you want a text capture. MP4 is
fine — the README can embed a GIF converted from it.

### README
What Point is, the recording, install steps, a screenshot of the context card,
and an honest **"what it does not do"** section. That last section is a
credibility signal, not a weakness.

## Do NOT

- Add features.
- Add telemetry, i18n, or auto-update.
- Claim performance numbers that were not measured.

## Done when

1. The states checklist is complete, every component ticked.
2. Every screen renders correctly at all nine sizes.
3. All four themes pass QA on dark and light terminal profiles.
4. Idle Point uses ~0% CPU over 60 untouched seconds.
5. The demo recording exists and runs the full script without a retake needed for
   a bug.
6. The README's "does not do" section is written and accurate.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```
Resize through all nine sizes, in and out.
**Expect:** no artefacts, no panics, correct degradation.

```powershell
# 4. idle CPU
$p = Get-Process point
$before = $p.CPU ; Start-Sleep -Seconds 60
(Get-Process point).CPU - $before
```
**Expect:** a value very close to 0.
**Fails if:** it climbs — the animation or a tick is still running while idle.

Switch each of the four themes in Settings, on both a dark and a light Windows
Terminal profile.
**Expect:** all readable; error and warning obviously distinct from the palette.

## Handoff

**Three entries here came from F13b's verification rather than from this
episode's own planning**: the two shipped keymap/close items above
(done, in `6e28f6b`), and the deferred scroll bound (live, with its
known fix written out). They are marked as such so picking one up does
not mean rediscovering why it was or was not done at the time -- the
rule this file's Purpose section already states.

State after this episode: v1 is finished — not feature-complete, **finished**.
Everything beyond this point is in the parking lots.

Record: the final breakpoint numbers, the measured idle CPU, and the full states
checklist.
