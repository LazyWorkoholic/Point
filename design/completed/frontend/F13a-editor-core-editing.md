# Episode F13a — Editor Core: the Editing Half ✅

**Task doc:** `../../tasks/frontend/F13a-editor-core-editing.md` ·
**Session:** 18a · **Remainder:** F13b (Session 18b)


**Point has a second workspace, and it is a real editor. A line-slice
`Buffer` behind an interface, a caret that moves by rune and renders by
display cell, selection the editor owns because mouse capture took the
terminal's away, Tier 1 auto-indent, undo that records splices rather
than snapshots (so an agent-applied diff is one undo by construction), a
tab strip inside the Code File box, a file tree from B7's index, and
chroma highlighting over the visible window. Everything B32 reports about
a file's bytes — line ending, BOM, trailing newline, decode replacements,
writability — is carried through unchanged and un-re-derived, so F13b's
save has the values it needs without going back to the wire. There is NO
save path: no `WriteProjectFile` call, no lock, no autosave, and `ctrl+s`
says so in one line rather than pretending.**

> Verified by Aryan across **seven rounds** against
> `backend\tests\gate_f13a.ps1` (1,062 lines), in a scratch
> `%APPDATA%`/`%LOCALAPPDATA%` profile per CLAUDE.md's env-var-override
> pattern — the real profile was never named in any call, destructive or
> otherwise. Final state: `go build`, `gofmt -l`, `go vet` clean;
> **212 tests in `internal/editor`, 379 in `internal/app`**.
>
> **This episode was SPLIT, and F13's own task doc sanctioned it**: "The
> largest frontend episode. It may take two sessions — if so, stop at a
> working point, record what is done, `/clear`, and resume with a note."
> This is that note. See "Scope" immediately below for exactly which
> Done-whens are and are not covered, and
> `../../tasks/frontend/F13b-editor-save-locks.md` for the remainder.
>
> **Four of F13's Build-section assumptions did not hold against this
> repo and were resolved with Aryan before any code was written** — see
> "Five assumptions that did not hold". Two of them (`tab` as a chord,
> and a status bar Root owns outright) would have made two Done-whens
> unreachable.
>
> **Six of the seven rounds were verification repairs, and that is the
> record rather than an embarrassment.** Round 1 built it; rounds 2–7
> fixed what running it found. Three of those rounds traced several
> separately-reported symptoms to ONE cause each — the NUL rune (round
> 3), the never-rendered note (round 4), the status expiry (round 5) —
> and two found bugs in this episode's own earlier fixes (rounds 5 and
> 6). Every one is named below with what it actually was.

---

## Scope — what this episode covers, and what it does not

**Covered: the editing half.** F13's Done-when **3, 4, 5, 6** and the
**open half of 1**:

| # | Done-when | How it is gated |
|---|---|---|
| 1 (open half) | a 5,000-line file opens and scrolls with no perceptible lag | gate step 2 + measured benchmarks |
| 3 | undo removes a word-group, not one character | `TestUndoRemovesAWordNotACharacter` + gate step 3 |
| 4 | selection correct with mouse drag and shift+arrows, on CJK/emoji | `TestSelectionCoversExactlyTheRightCells` + gate step 4 |
| 5 | tab / shift+tab indents and dedents a multi-line selection | `TestIndentAndDedentAMultiLineSelection` + gate steps 5, 5a |
| 6 | Enter after an indented line preserves that indentation | `TestEnterCopiesThePreviousIndentation` + gate step 6 |

**NOT covered — F13b's, every one of them:**

```
2    a CRLF file round-trips unchanged; git status clean after open + save
7    a second holder opens read-only with the holder named; take-over
     works after the lock goes stale
8    external change then save -> refused with [reload] [overwrite] [diff]
9    closing a dirty file routes through Confirm
10   autosave = "after_delay": one save at the end, not thirty
11   autosave does not fire on a file held by another holder
12   autosave does not trip Point's own "changed on disk" check
```

All seven need the save path, the lock lifecycle or both. The gate's own
footer lists them as explicitly out of scope so a green run here cannot
be mistaken for a green F13.

**The seam is clean, and that is why the split fell here.** The save
path is exactly the part that needs the holder key (A2), the lock
lifecycle, and three overlays that do not exist; the editing core is
fully testable without any of it. Nothing in the editing half is
provisional pending the save half.

---

## Where the code went

```
frontend\internal\editor\         NEW PACKAGE — 4,515 lines, 4,569 of tests
├── buffer.go      271  Pos, the Buffer interface, lineBuffer. Splice is the
│                       ONLY mutation, which is what makes undo a record of
│                       one call. Single-line fast path (round 6).
├── undo.go        351  editRecord + history. Splices, not snapshots.
│                       editInsert/editDelete/editOther/editPaste, keystroke
│                       coalescing, ApplyRegion (F9b/F15's entry point).
├── doc.go         353  one open file: B32's metadata carried verbatim,
│                       readOnlyReason, indentStyle + detectIndent, reveal.
├── motion.go      283  every cursor move through one moveTo; word classes;
│                       ensureVisible behind the reveal gate.
├── edit.go        397  insert/backspace/delete, Tier 1 auto-indent,
│                       indent/dedent a block in ONE splice, bracket pairing.
├── render.go      479  the Code File box, gutter, cellPaint, caretStyle
│                       (reverse video), run-coalesced sliceCells, the F14
│                       Terminal stub, the two right-pane cards.
├── highlight.go   284  chroma over window+margin; styledRune holds a ROLE.
├── tree.go        368  the flat B7 index grouped into a tree.
├── tabs.go        215  the strip as the first content row, tabSpan hit-tests.
├── protocol.go    328  ProjectResolve -> ListProjectFiles -> ReadProjectFile.
├── keys.go        234  the key map, and what it deliberately does not claim.
├── mouse.go       192  drag selection, wheel, tab-strip and tree clicks.
├── editor.go      353  the app.Workspace itself: Update, Cards, say(),
│                       Suspend/Resume, CapturesKey.
├── status.go      126  StatusSegments, lineEndingLabel, DirtyBufferCount.
├── textutil.go    167  the THIRD copy of app\box.go's width primitives.
└── box.go         114  ...and of its paneBox. See "Still owed" below.

frontend\internal\app\
├── contribute.go   99  NEW — KeyCapturer, StatusContributor,
│                       WorkspaceStatusMsg. F13's three additions to what
│                       Root asks of the active workspace.
├── root.go       +212  KeyCapturer in dispatchKey (before the chord table),
│                       isBareModifier drop, WorkspaceStatusMsg case,
│                       expireStatusIfChanged, StatusLinger, forwardKey.
├── lifecycle.go  +209  resetIdle, the dirty-pinned skip + its own latch,
│                       graceSeconds, keepWarnedAlive DELETED.
├── registry.go   +110  WorkspaceStatus.Pinned, Label's clamped countdown,
│                       shortDuration unconditional.
├── render.go      +94  statusLeft (who yields to whom), fitParts.
├── popup.go       +49  PopupConfig.NoteW, rowWidths' declared-width path.
├── appearance.go   +9  propagateTheme over EVERY loaded workspace.
├── chooser.go     +18  switcherNoteW.
├── paste.go       +20  isBareModifier.
└── cmd\point\main.go +29  the Editor registered, leader -> e, MinSize 70x16.

backend\tests\gate_f13a.ps1   NEW — 1,062 lines, 14 steps
design\tasks\frontend\F16-polish.md   +3 deferred items
design\completed\frontend\F12-workspace-manager.md   + "Superseded by F13"
```

No new dependencies: `chroma/v2` and `go-runewidth` were already in
`go.mod`. `envelope_test.go` was NOT touched — `EvProjectFileWritten` is
F13b's to add, per B32's handoff.

---

## The architecture

### The buffer is a line slice behind an interface, and Splice is the only mutation

F13 asks for the interface so "a rope or gap buffer can replace it later
— the first natural C++ component in the Track-2 book". The interface is
there, and one decision gives it more than future-proofing: **everything
that mutates goes through `Splice`**. Not minimalism — it is what makes
undo a record of one call rather than a separate inverse per operation,
and it is why "an agent-applied diff is ONE undo" is structural instead
of a flag somebody has to set.

`Pos` is a line index and a **rune** index. Never bytes (wrong at the
first non-ASCII rune), never display cells (wrong at the first tab or CJK
rune); both are reachable in the first file anybody opens in this repo.
Display cells are the right unit only for rendering and horizontal
movement, where `displayCol`/`colAtDisplay` convert on demand.

### Undo records splices, not snapshots — unlike chat's

`chat\input.go` stores a full copy of the text per undo step, which is
right there: a draft is a few hundred runes. The same shape here would
copy 95KB per character typed in Done-when #1's own file, so undo's cost
would scale with the file opened rather than the edit made — the exact
thing "no perceptible lag while typing" rules out.

So a record is the splice: where, what came out, what went in. Three
things fall out and none needed a special case: memory proportional to
the edit; redo free, since a record holds both directions; and
`ApplyRegion` — one splice, one undo step — which is F13's "without this
the diff card is unusable in practice", expressed structurally.

### B32's metadata is carried, never re-derived

`wireFile` mirrors `ProjectManager.read_file`'s dict field for field.
`line_ending`, `crlf_count`, `lf_count`, `bom`, `trailing_newline`,
`decode_replacements` and `writable` are stored and displayed; nothing
here looks for a BOM in `content` (B32 strips it and reports it), and
nothing infers a line ending from the bytes. `TestMetadataIsCarriedNotDerived`
feeds LF-only content with CRLF+BOM metadata — the exact shape B32 puts
on the wire — so any "detect it from the content" shortcut fails.

A single trailing newline IS stripped from the buffer at load, and that
is a decision rather than a convenience: `"a\nb\n"` is a two-line file in
every tool that counts lines, and leaving the split's empty third element
in would make the gutter, `line:col` and F13b's save all special-case it.
The fact lives in `doc.trailingNewline`, from B32's field.

### Revealing is something an action requests

`doc.reveal` separates "the caret asked to be shown" from "the user
scrolled". `ensureVisible` used to run on every frame, so any scroll the
caret did not ask for was undone on the frame it happened. Every caret
move and every edit sets `reveal`; the wheel deliberately does not. The
tree has the same flag for the same reason. **F16's horizontal-scroll
item depends on this distinction** and its entry says so.

### Three narrow interfaces, not one wide one

`KeyCapturer`, `StatusContributor` and `WorkspaceStatusMsg` follow the
pattern `FocusAware`/`OverlayProvider`/`DirtyBuffers` already set: Root
asks the active workspace a question and never reads its fields. A
workspace that does not implement one is unaffected — the type assertion
fails harmlessly and the previous behaviour stands.

---

## Five assumptions in the Build section that did not hold

### 1. `tab` has been a chord since F4, and an unavailable chord is SWALLOWED

`pane.focus.next` holds `Chord: "tab"` with no `When`. `dispatchKey`
consults the chord registry BEFORE the workspace, and an action whose
`When` is false sets a status line and **returns** rather than falling
through. So Tab could never indent (Done-when #5), and giving the action
a `When` would only change the failure from "focuses the wrong thing" to
"prints *Focus Next Pane is not available right now*".

Aryan's call: `KeyCapturer`, asked after the leader and before the chord
lookup. No shipped binding moved. **The Editor claims exactly two keys,
`"tab"` and `"shift+tab"`**, and `TestCapturesKeyClaimsExactlyTabAndShiftTab`
asserts both the two claimed and 22 explicitly not — because every
captured key is a key whose global binding silently stops working here.

### 2. The status bar was Root's outright

`statusBar` is built entirely inside Root and no workspace could
contribute. F13's status line and LAYOUT.md's Editor sketch both need the
file's own state, which only the workspace has. Hence
`StatusContributor`, slotted where `bindingsHint` sat.

### 3. `ctrl+tab` does not exist on Windows

`tea.Key` carries `{Type, Runes, Alt, Paste}` — no Ctrl field — and
`key_windows.go` maps `VK_TAB` to `KeyTab` whether or not Ctrl is held.
`ctrl+tab` and `tab` are the same `KeyMsg` and `String()` returns `"tab"`
for both. Tabs cycle on **`ctrl+n` / `ctrl+b`**. This is exactly the class
of finding F13's own Handoff asks to record.

### 4. LAYOUT.md's `^E tree` is unreachable, and so is `ctrl+r`

Both belong to Chat-only actions (`chat.queue.edit`, `chat.retry`) whose
`When` is false in the Editor — so both are swallowed with a status line.
The tree key is **`ctrl+o`** and re-index is **`f5`**, both free in the
registry. `esc` and `ctrl+c` are swallowed the same way; neither is
load-bearing here, and gate step 13 makes the reader look at all four.

### 5. The Editor has no project, and `runtime.py` is not 21KB

F13's verification opens `backend\runtime\runtime.py` as "21KB"; it is
**90,797 bytes**. No 5,000-line file exists in this repo at all (the
largest is `WORKFLOW.md` at 2,996 lines), so the gate generates one — and
it must be **under `index.max_file_kb` (100)** or the tree will not show
it and nothing in 18a's UI opens a file the tree does not show. The
fixture is 5,000 lines / 84,556 bytes, asserted into that band.

The Editor also has no session and so no `project_id`: it resolves one
itself with `ProjectResolve{path: cwd}`. **Per-file resolution needs no
second call** — `_handle_read_project_file` passes the payload to
`_resolve_optional_project`, which reads the same `path` key the file is
named by, so a read with no `project_id` walks up from the file's own
path. That is the task doc's note 4, for free. And `pathguard.py` is
stricter than "read-only": with no project resolved it **refuses every
read**, so a file with no `.point\` above it cannot be opened at all.

---

## Decisions settled before any code was written

Aryan settled all thirteen in response to a pre-code report. They are
recorded as given, with what each prevented.

| | Decision | What it prevented |
|---|---|---|
| **A1** | `KeyCapturer{CapturesKey(string) bool}`, checked before the chord registry; Editor claims `tab`/`shift+tab` only | Done-when #5 being unreachable; moving a shipped F4 binding |
| **A2** | The holder key is the WHOLE `<username>@<full-id>` string, generated once, persisted in `state.json` | A renamed Windows account silently becoming a different holder (B32's Decision 4). **F13b's — not written this session** |
| **A3** | `StatusContributor`, slotted where `bindingsHint` sat; a transient message still outranks it | F13's status line having nowhere to render |
| **B1** | `ProjectResolve{path: cwd}` at load; no `.point\` → read-only | The file tree having no root before anything is open |
| **B2** | The Done-when #1 fixture is ~19 bytes/line so it lands under 100KB | A fixture invisible to the tree and therefore unopenable |
| **B3** | chroma over window + margin, cached per (file, generation) | A 302ms frame per keystroke |
| **B4** | A mixed-ending file opens **normally editable**, status bar reads `MIXED n/m`; **the save-side majority-ending write is F13b's** | Claiming CRLF or LF for a file that is neither |
| **B5** | `decode_replacements > 0` → strictly read-only, reason in the status bar | Writing U+FFFD over the original (B32's handoff) |
| **B6** | Tab display width is **8**, matching `chat\textutil.go`. One constant, not two | Two numbers for one concept |
| **B7** | Heartbeat every 20s against B7's 60s staleness — **F13b's** | One dropped beat making Point look dead to itself |
| **B8** | The Terminal rect gets a titled box naming F14 | An empty bordered box reading as a bug |
| **B9** | One generic `app.OpenWorkspaceOverlayMsg` for the editor's dialogs — **F13b's**, unbuilt | Three bespoke overlay paths in `app` |
| **B10** | `[diff]` renders a plain unified diff inside `editor`, not named like F9b's card — **F13b's** | Reusing a later episode's name (CLAUDE.md's rule) |

**B6 and B4 are the two worth re-reading before F13b.** B6 settled the
tab *display* width at 8; what Tab *inserts* is the file's own detected
style, and the two are independent (`runeCells` vs `indentStyle.unit`).
B4 settled only the display half: a mixed file is editable and labelled,
and **which ending a save writes is still open** — B32 reports
`crlf_count`/`lf_count` and deliberately stops, and F13b has to choose.

---

## The rounds

Round 1 built it. Rounds 2–7 are what running it found. Named, because
"all bugs fixed" is not a record.

### Round 1 — the build, and what verification immediately found

Shipped the whole editing half: the package above, the three `app`
interfaces, the Editor registered with `leader → e` and `MinSize 70×16`,
and a 14-step gate. Measured, not asserted: a 30-line highlight window
3.2ms against 302ms for the whole 5,000-line file — the 108× that
justifies the window rule.

Verification found three groups, reported as eighteen numbered symptoms.
Two were not bugs and are recorded as such:

- **tab in `tabs.go` inserts 4 spaces** — it does not. A real `\t` is
  inserted at every caret position through both paths, and the fixture
  contains four real `0x09` bytes. `tabStop` is 8, so a tab *draws* 4
  cells wide at display column 4, 12, 20…, which is what was seen.
  `TestTabInTheGateFixtureInsertsARealTabAtEveryPosition` pins it.
- **the caret-move undo group** — verified correct: `abc`, two arrows,
  `xyz`, one `ctrl+z` leaves `abc`. Aryan had flagged his own uncertainty.

### Round 2 — the caret was invisible, the wheel could not scroll, messages never showed

- **The caret was drawn at the right cell every time and was the wrong
  COLOUR.** Measured from the emitted bytes: the caret's background was
  `AccentPrimary` `#2EC4C6` and the neighbouring keyword's foreground is
  the same `#2EC4C6` — THEME.md rule 3 makes accent.primary keywords,
  the focused border and the streaming indicator all at once. On a space
  it read as a solid block; on a glyph it vanished. **Fix:** reverse
  video over the cell's own role, which needs no new role and survives
  `NO_COLOR`. The caret is now also drawn at a selection's active end,
  which is what made a short shift+arrow selection stop reading as an
  artifact.
- **`ensureVisible` ran on every frame**, so the wheel moved `top` and
  the frame snapped it straight back: `top after wheel=3, top after
  View=0`, five times running. **Fix:** `doc.reveal`.
- **`w.note` was rendered only by the empty state** — i.e. never, with a
  file open. Four separate "no message at all" reports, one cause.
  **Fix:** `app.WorkspaceStatusMsg` to Root's status bar.
- **Paste performance.** The per-keystroke frame was measured at p50
  6.0ms / p90 9.0ms / max 18.3ms, with **half of all frames over
  `paste.go`'s 6ms burst gate** — so pastes frequently degraded into N
  keystrokes. Three fixes: `styledRune` holds a `theme.Role` instead of a
  `lipgloss.Style`; `sliceCells` emits one escape pair per *run* (1,500 →
  44 per frame); and `Splice` got a single-line fast path, removing an
  O(file) allocation per keystroke. Result **p50 3.5ms / p90 5.9ms / max
  8.1ms, 6.75% over the gate**. Also `editPaste`, so a split paste is one
  undo step by construction.
- Plus: single-click tree open (F13's spec says "click", singular), the
  caret/selection record fix in `shiftLines`/`maybePair`, and the File
  Tree/Task card heights swapped on Aryan's call.

### Round 3 — one NUL rune explained three symptoms

Aryan proposed a unifying hypothesis and it was **confirmed from
bubbletea's source**: `readConInputs` skips key-UP records and `VK_SHIFT`
and nothing else, `keyType` has no case for `VK_CONTROL`, so it falls
through to `return KeyRunes` — and a modifier record's `Char` is 0. A
bare Ctrl key-down therefore arrives as `KeyMsg{KeyRunes, []rune{0}}`.

| reported | measured |
|---|---|
| selection collapses when Ctrl goes down | record `removed="hello world" inserted="\x00"` |
| `ctrl+z` needs two presses | `typed+paused: recs=1` → `bare ctrl: recs=2` |
| arrows skip / caret vanishes | line `[97 0 98 99]`: cols 1 and 2 both map to cell 1 |

**Fix:** `isBareModifier` dropped in `Root.handleKey`. The right layer —
`pasteable()` had carried `k.Runes[0] != 0` since F1/F4, i.e. someone had
already hit this record and filtered it on the paste path alone, and
chat's input was one Ctrl press from putting a NUL in a draft. Plus
defence in depth: `insertRune` refuses C0 control runes.

Also round 3: **the tab strip and the File Tree's first row are the same
screen row** (Compute starts both panes at `bodyTop`), and the strip was
hit-tested on **Y alone** — so every click on that row across the whole
terminal went to `clickTabs` and was dropped. `tabRow int` →
`tabStrip layout.Rect`. Round 2's test for this had passed for the wrong
reason: it had no file open, so no tab row was recorded.

And the **idle rescue** was rescoped — see "Two changes to F12" below.

### Round 4 — the theme never reached a backgrounded workspace

- **`propagateTheme` pushed to `r.current()` alone**, and the only other
  caller of `SetTheme` is `loadWorkspace` (construction). So a loaded,
  inactive workspace never heard, and switching to it does not re-theme
  something already loaded. Root's header read the theme live and was
  always right, which made it look like the Editor cached styles — it
  does not. Affected **every palette**; the repro just named two.
  `TestEveryPaletteReachesEveryLoadedWorkspace` covers all 12 ordered
  pairs.
- **The status bar's budget.** `fitHints` was measured exact at every
  digit count — the reported digit boundary was not the cause. The left
  half was built against `width - right - 1`, so a segment dropped while
  30 cells went to `connected · N task(s) · W×H`: at 100 columns, budget
  67 against 77 needed, **21 cells spare in the line**. `statusLeft` now
  lets a contributor's segments take the line, but only when a segment
  would otherwise be lost. A second bug fell out: `fitHints` reserved
  room for its own `+N` while deciding each part, dropping parts at a
  width the full set fits exactly.
- The switcher readout was simplified to plain labels — **superseded in
  round 5**.

### Round 5 — two bugs in this episode's own earlier fixes

- **The dirty-notice latch was permanent.** Round 2 added
  `idle.dirtyNoticed`, cleared in exactly one place — `walkIdle`'s
  not-dirty branch — while three places reset the idle clock. So for a
  workspace that stayed dirty the notice fired once per *loaded
  lifetime*, not once per idle period. **Fix:** one `resetIdle(id)`,
  called from all three, which is what "this workspace's idle accounting
  starts over" means in one place.
- **`recordPaste` set `r.status` by bare assignment** and scheduled
  nothing, so the paste counter never faded and masked the file
  segments. Not unique to paste: **~40 sites assign `r.status`
  directly**, several from paths with no `tea.Cmd` to hang a tick on.
  **Fix:** `expireStatusIfChanged` derived in `Update`, the one funnel
  all forty pass through. `Root.StatusLinger` keeps the derived tick from
  parking tests for the real linger.
- The countdown was reinstated, **clamped** — Aryan's call, replacing
  round 4's labels.

### Round 6 — the clamped countdown dropped its minutes field

`shortDuration` had one special case — `if total < 60 { return "%ds" }` —
contradicting its own doc comment. It is the **shared** formatter: both
sides of `idle X of Y` always called it, so its *output* changed shape at
sixty seconds and produced `idle 58s of 1m00s`, one line in two formats.
The asymmetry was never two code paths. **Fix:** remove the branch.

And **`· pinned` was genuinely truncated**, confirmed by rendering the
row: `idle 1m00s of 1m00s …`. 28 cells needed against `rowWidths`'
24-cell ceiling — and `rowWidths(86)` still answered 24, so no amount of
popup width could have fixed it. **Fix:** `PopupConfig.NoteW`, clamped
against the name column rather than a third of the row.

### Round 7 — the grace warning reverted to bare seconds

Round 6's unconditional `shortDuration` moved the grace warning to
`unloads in 0m29s` as a side effect, contradicting the gate's own
already-verified Step 10b text. Aryan's call: its own `graceSeconds` at
its one call site — **not** the under-a-minute branch put back inside
`shortDuration`, which is the special case that caused round 6's bug.
Two formatters for two purposes: the paired one is unconditional because
its output is read as a pair; the grace countdown has no pair.

---

## Two deliberate changes to what F12 shipped

Both on Aryan's call, both with one of F12's own tests replaced. They are
recorded in full in
**`F12-workspace-manager.md`'s "Superseded by F13 (Session 18a)"
section**, with pointers from the two Decisions they contradict — read it
there rather than here:

1. **The global "any key to keep" idle rescue is gone** (round 3). A
   keystroke now affects only the active workspace's own clock;
   switching to a workspace is the only rescue, and the warning names
   the switcher instead of promising a key.
2. **The switcher readout is a clamped countdown** (rounds 4–7), after
   passing through plain labels in round 4. Not F12's original unclamped
   form and not round 4's labels: `idle 0m23s of 1m00s` normally, `idle
   1m00s of 1m00s · pinned` when unsaved work is holding a workspace
   loaded past its budget.

F12's `DirtyBuffers`, declared there with no implementors, is implemented
here — and the pinned state it produces is why the clamp exists at all.

---

## Deferred to F16, with the reasoning

Three items are logged in **`../../tasks/frontend/F16-polish.md`** as
`carried forward from F13 (18a)`, each with the reasoning it was raised
with rather than a one-line restatement:

- **horizontal scrolling in the Code File box** — the mechanism exists
  (`doc.left`, `sliceCells`); what is missing is scrolling as its own
  action. The entry names the `doc.reveal` trap: a horizontal scroll must
  set `left` WITHOUT setting `reveal`.
- **creating a new file, and opening a different directory as the project
  root** — the backend half is settled (`WriteProjectFile` with
  `expected_mtime: null`); it belongs after F13b, reusing its refusal UIs.
- **tab strip shrink-to-fit** instead of overflow arrows — confirmed
  current behaviour, not a regression.

F16 is `[P2] · Depends on: all`, which is already the "only if time
remains" bucket. `design/tasks/polishing.md`, created across rounds 2 and
4 to hold these, was merged into F16 and **deleted** — one document per
scope.

---

## Still owed

- **The width primitives are now in three packages.** `editor\textutil.go`
  and `editor\box.go` are the third copy of `app\box.go`'s
  `visibleWidth`/`padCells`/`truncateCells`/`stripANSI` and `paneBox`.
  F1's handoff flagged a shared `ui` package as F3-or-F7 work; chat's own
  copy says the same. Three callers now, and each copy's header says so.
- **`envelope_test.go:26` must gain `EvProjectFileWritten`** when F13b
  wires the save — B32's handoff asks for it.
- **The file tree cannot see files between `index.max_file_kb` (100) and
  `editor.max_file_kb` (512).** B32's recorded accepted limitation,
  inherited rather than introduced. Nothing in 18a's UI opens a file the
  tree does not show, so the band has no route in from the Editor at all.
- **`app\chooser.go`'s switcher rows are a snapshot** taken when the
  popup opens and never advance. F12's, pre-existing, out of scope here
  and recorded in F12's own doc.
- **6.75% of frames still exceed `paste.go`'s 6ms gate**, so a paste can
  occasionally still arrive as keystrokes. The undo count is right either
  way (`editPaste`). Cutting the remaining ~3ms means lexing a line
  without the lines above it — a visible colour regression. Not taken.

---

## Handoff

### F13b (Session 18b) — the save and lock half

`../../tasks/frontend/F13b-editor-save-locks.md` holds Done-when 2, 7, 8,
9, 10, 11 and 12 verbatim from F13's own task doc. It inherits:

- **A2's holder key, unwritten.** The whole `<username>@<full-id>` string,
  generated once and persisted in `state.json`. `config\state.go`'s
  `saveStateTo` gives keep-what-is-on-disk treatment to exactly
  `LastWorkspace` and `WorkspaceState`; every other field is serialised
  from the writer's own startup snapshot, and `chat.Workspace` writes the
  whole struct. **A new top-level `Holder` field would be erased by
  chat's next draft save on the launch that generated it.** Aryan's
  settled answer: extend the keep-if-zero clause to `Holder` and generate
  it in `main.go` before `chatState := state`.
- **B9's `app.OpenWorkspaceOverlayMsg`, unbuilt.** A workspace cannot open
  an arbitrary overlay today; the three existing paths each put
  construction in `app`. Needed for the three-button `stale` dialog
  (`Confirm` is two buttons by construction) and the dirty-close confirm.
- **B10's `[diff]`** — a plain unified diff inside `editor`, deliberately
  not named like F9b's card.
- **B4's save-side half**: which ending a mixed file is written with.
- **Everything B32 reports is already in `doc`** — `mtime` and `size` are
  stored for `expected_mtime` precisely so a save does not re-stat and
  reopen the race the precondition closes.
- `closeTab` currently REFUSES to close a dirty tab and says why; F13b
  routes it through `Confirm` (Done-when #9).

### F14 (Editor Terminal)

`Compute` already hands the Editor a 3-row `Secondary` rect
unconditionally (`recompute` hardcodes `TermSmall`). It currently holds a
titled box naming F14 — B8. Reclaiming or resizing those rows is a layout
change, not this episode's.

### F9b (Diff Card) and F15 (Explainer)

`doc.ApplyRegion(from, to, repl)` is the entry point: one splice, one undo
step, caret left at the end of what it inserted. Built and tested here
with **no caller**, deliberately — the undo model either supports it by
construction or has to be rebuilt to, and this one does. It is not a write
path: it changes a buffer the user then saves, so CLAUDE.md's first hard
rule is intact.

### Anyone adding a workspace

Three narrow interfaces are available and all three are optional:
`KeyCapturer` (claim a key the chord table owns — and say which, in the
implementation's own doc), `StatusContributor` (own the status bar's left
half), and `WorkspaceStatusMsg` (one transient line, which Root expires).
`DirtyBuffers` now has a real implementor, so the idle sweep will skip
your workspace if you report unsaved work.

### Anyone touching the key path

`dispatchKey`'s order is: open overlay → leader → **KeyCapturer** → chord
registry → workspace. An unavailable chord is swallowed with a status
line and does not fall through — that is pre-existing and is why four
Chat-only chords misreport in the Editor (gate step 13). A bare modifier
is dropped before any of it.
