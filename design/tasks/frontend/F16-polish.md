# F16 — Polish

**Phase:** P2 · **Batchable:** no · **Depends on:** all

## Purpose

The difference between "it works" and "it feels finished". Everything here is
small; together they are what a viewer actually notices.

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

State after this episode: v1 is finished — not feature-complete, **finished**.
Everything beyond this point is in the parking lots.

Record: the final breakpoint numbers, the measured idle CPU, and the full states
checklist.
