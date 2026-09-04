# Episode F4 — Query Input ✅

**A real text-editing surface for the Chat workspace: full cursor/selection/
undo, growth — overlay + auto-scroll, drafts that survive a restart, a
focus model shared with F3's transcript hotkeys, and a mode chip that
resolves `@` mentions against the agent registry.** Six rounds of manual
verification found fifteen numbered bugs plus a render/paste circular
dependency and a fixed-point cursor-affinity defect that took three rounds
to fully centralize. **Three further rounds after this document was first
written** found a caret-rendering gap (Bug 16), a real data-loss bug in
quitting (Bug 17), and one bug (17b) that was traced exhaustively, could not
be reproduced, and then stopped reproducing on its own with no code change
identified as the cause — recorded honestly as *observed, then not
reproducible*, not as fixed, per the same standard as F2's paste note. This
document is the full account of all of it, not just the feature.

---

## `frontend\internal\chat\` — new files

| File | Lines (approx) | What |
|---|---|---|
| `input.go` | 460 | `inputBuffer` — the width-independent half: rune buffer, cursor, selection, undo/redo, `InsertText`/`Backspace`/`Delete`/`DeleteWordLeft`, `filterInsertable`, word-wise boundaries, `beginMove`/`setCursor` |
| `input_wrap.go` | 500 | The width-dependent half — `wrapInputRunes`/`wrapParagraph` (position-preserving wrap, gap-aware since Bug 14/15), `lineAt`/`lineAtTrailing`/`lineForCursor`/`atSoftBoundary`/`lineContaining`, `MoveUp`/`MoveDown`/`MoveHome`/`MoveEnd`/`MoveRight`/`MoveLeft` |
| `input_render.go` | 320 | `AttachmentChip`, the `editorMode` state machine (`agentTokenState`/`matchedAgentID`/`currentMode`), `renderInputLines`/`renderTextLineWithCursor`, `resolveCaretPosition` (Bug 16), `modeChipPrefix`/`browseModePrefix`/`activePrefix`, `queryBorderRole` |
| `input_keys.go` | 220 | `handleInputKey` (the whole editing keymap), `handleInputPaste`, `onSend`, `OpenAttachmentPickerMsg`, `enterBrowseMode`/`enterInputMode`, `InputFocused` |
| `draft.go` | 145 | `scheduleDraftSave`/`saveDraftIfDue`/`persistDraft`/`clearDraft`/`restoreDraft`/`FlushDraft` (Bug 17) — the debounced, per-session, self-healing-on-load draft |

Plus edits to `chat.go` (`inputState`, `Options.State`, `Workspace.state`/`saveStateFn`), `protocol.go` (`Update` routes focus/paste/draft messages; `setSession` calls `restoreDraft`), `scroll.go` (unchanged logic, now reached only when `!w.input.focused`), `render.go` (`splitGrowth`, `ensureCursorVisible`, `queryLines`, `mainBorderRole`), and two new test files, `input_test.go` and `input_wrap_test.go`, plus `input_workspace_test.go` and `input_bench_test.go`.

`input_render.go` also gained `resolveCaretPosition` (Bug 16) and `draft.go`
gained `FlushDraft` (Bug 17) after this document was first written — both
covered in their own section below.

Current counts in the episode's own files: `input_test.go` 11,
`input_wrap_test.go` 27, `input_workspace_test.go` 34,
`input_bench_test.go` 2 benchmarks, plus `app`'s `regression_test.go` (20),
`focus_test.go` (3) and `draft_test.go` (2, new). `go build`, `go vet`,
`go test ./...` green throughout, gofmt clean, no NUL bytes anywhere in the
tree (see "A tooling hazard worth recording," below).

## `frontend\internal\app\` — changes

| File | What |
|---|---|
| `focus.go` (new) | `EnterBrowseMsg`, `FocusInputMsg`, `FocusAware` interface — the narrow-interface pattern `Cards()` already established, so Root never reads a workspace's internal focus state directly |
| `draft.go` (new, Bug 17) | `DraftFlusher` interface — the same narrow-interface pattern as `FocusAware`, read through a type assertion so Root never needs to know chat's own draft-save details |
| `actions.go` | `pane.focus.next` (Tab) repurposed to toggle focus via `FocusAware`; new `chat.browse` (Esc) action, always available in Chat, idempotent. **Bug 17:** `app.quit`'s `Run` now calls `DraftFlusher.FlushDraft()` on the active workspace before returning `tea.Quit` |
| `paste.go` | `pasteable()` rejects a zero-rune `KeyRunes` event; `pasteState.flushPending` replaces one-timer-per-character; `onFlushTick` re-arms from `lastAt` instead of trusting which tick fired; `looksLikePaste`/`isRepeatedKey` classify a burst by shape, not timing; `pasteInFlight()` |
| `render.go` | `View()` re-serves `r.lastFrame` while `pasteInFlight()` — the render-suppression fix, see its own section below |
| `root.go` | `lastFrame string` field |

## `frontend\internal\config\` — changes

`state.go`: `State.Drafts map[string]Draft` and the `Draft{Text, Cursor}`
type. A narrower, F4-owned shape — not F12's eventual `WorkspaceState`
(scroll position, open files, cursor in general) — per the project's own
rule for a shape a later episode will formally own (CLAUDE.md's B8
precedent). **F12's real type must be adaptable TO `Draft`, not identical
to it.**

---

## The cursor affinity model — read this before adding a Move* method

This took three rounds to get right (Bug 6 → Bug 11 → Bug 13, plus Bug
14/15 as a related-but-distinct wrap-layer defect). A future episode
adding any new cursor-movement operation must use this model from the
start, not rediscover it.

### The problem affinity solves

`inputLine.End` (a rune index) is ambiguous by construction at a **soft
wrap boundary**: it is simultaneously "one past the last rune of this
line" and "the first rune of the next line" — the *same buffer index*, two
different screen positions. `inputBuffer.trailing` is that missing bit:

- `false` (the default) — the position reads as the **start of the next
  line**. This is where a caret continues after wrapping, and where
  Home/Left/Right/Up/Down all leave the cursor.
- `true` — the position reads as the **end of the previous line**. Only
  `MoveEnd` sets this, and only when the boundary is a genuine soft wrap
  (`atSoftBoundary` — see below); at the buffer's own last line, or at a
  hard newline, there is nothing for "trailing" to mean relative to, and
  `MoveEnd` correctly leaves it `false` there.

A **hard** boundary (a real `\n`, or — since Bug 14/15 — a consumed
whitespace run) has no such ambiguity: `lineAt` and `lineAtTrailing`
already agree on which line it belongs to, so `trailing` is inert there.

### `setCursor` is the only assignment point

```go
// setCursor is the ONLY place b.cursor and b.trailing are ever assigned
// together after construction.
func (b *inputBuffer) setCursor(pos int, trailing bool)
```

`beginMove` (the shared shift-selection/undo-coalescing setup every
movement function calls first) deliberately does **not** touch either
field. Every `Move*` function computes its destination and calls
`setCursor` exactly once, last, with **both** arguments given explicitly.
This is not a style preference — it is the actual fix. Bug 6 (Home/End),
Bug 11 (Up/Down), and Bug 13 (Left/Right) were the *same* omission at
three separate call sites, each one a movement function that computed a
new index and left affinity to whatever it happened to already be.
Routing every write through one function with a mandatory second argument
turns "forgot to set affinity" into a compile error, not a manual-test
regression eleven rounds later.

`splice`, `setText`, `Undo`, `Redo`, and `SelectAll` — not "movement" in
the arrow-key sense, but every other site that ever changes `cursor` —
route through `setCursor` too, for the same audit-ability: grep for
`b.cursor =` outside `input.go`/`input_wrap.go` and it should not exist.

### The completeness guard

`input_wrap_test.go`'s `TestEveryMoveMethodIsRegistered` walks
`inputBuffer`'s method set via reflection and fails **by name** if a
`Move*` method exists that is not in the `allMovementOps` table (and the
reverse: a registered name that no longer exists). A future movement
function that skips this scrutiny does not silently ship — it fails this
one test on its own, naming itself. `allMovementOps` currently:

```
MoveLeft, MoveRight, MoveWordLeft, MoveWordRight,
MoveUp, MoveDown, MoveHome, MoveEnd
```

`TestEveryMovementFromAnEndPositionStaysConsistent` then runs **every**
registered op, from an End position on **every line of every
`wrapKindCases` entry** (see below), and asserts the general invariant: a
resulting `cursor` is always in `[0, len(runes)]`, and if `trailing` is
left `true`, `atSoftBoundary` must actually be true there. This is what
caught `MoveEnd` setting `trailing=true` unconditionally even on the
buffer's last line (a harmless-in-practice but semantically sloppy case,
tightened once the invariant flagged it).

### `wrapKindCases` — the boundary-sweep fixture, extended twice

```go
var wrapKindCases = []struct{ name, text string; width int }{
    {"mid-word wrap",    "abcdefghijklmnopqrstuvwxyz",                6},
    {"space wrap",       "aaaaa bbbbb ccccc ddddd eeeee",              6},
    {"multi-space wrap", "aaaaa        bbbbb        ccccc ...",       6},
}
```

Every boundary-sweep test (`TestRightFromEndOfEverySoftWrappedLine...`,
`TestLeftFromEndOfEverySoftWrappedLine...`,
`TestEveryMovementFromAnEndPositionStaysConsistent`) runs as a `t.Run`
subtest against **all three**. The first round's sweep only ever used the
mid-word case — exactly why Bug 14 (a single wrap-consumed space) and Bug
15 (a whole space *run* wider than the box) both survived it. A future
change to the wrap or movement code that only gets tested against one
wrap kind is repeating that exact mistake.

### The trailing-space defect this model just absorbed (Bug 14 / Bug 15)

Not a *separate* concern from affinity — the same underlying model, at the
wrap layer instead of the movement layer. `wrapParagraph` (`input_wrap.go`)
now treats a whitespace run that **separates two words** and falls at a
wrap point as a **gap**, exactly like the `\n` between paragraphs: real
content at its own buffer index (undo, selection, the eventual sent
message all still see it, verbatim, never collapsed), but rendered by
**no** line, so not a position a caret can visit mid-line either.

- **Bug 14**: a *single* space that merely fit on the current line before
  the next word overflowed was, before the fix, kept as the trailing rune
  of that line's own range — real content, rendered as an invisible blank
  column. Fixed by a `pendingSpace` mechanism in `wrapParagraph`: a
  fitting space is tentatively counted into `used`, and retroactively
  excluded if the line flushes right after it.
- **Bug 15**: a whitespace *run* wider than the box itself hit the
  `tokWidth > width` hard-break case (meant for an overlong *word*, which
  has no wrap point to hide behind) instead of the gap case — rendering an
  entire intermediate line of nothing but blank, individually-landable
  space columns, with leftover leading spaces carried onto the line after.
  Fixed by extending the gap rule to apply regardless of the run's width,
  as long as it separates two words (`space && j < hi`).
- Trailing whitespace with **nothing after it** in the paragraph (the
  paragraph's own tail, not a wrap artifact) is deliberately **not**
  gapped either way — it is real, standalone content and still hard-breaks
  normally if it does not fit, same as an overlong word. Confirmed
  correct, not collapsed, by explicit test
  (`TestOverwideTrailingSpaceAtParagraphEndIsNotGapped`).

Because `lineAt`/`lineAtTrailing`/`atSoftBoundary` only ever compare
**indices** — never inspect what rune sits in a gap, or how wide it is —
gapping a wrap-consumed run the same way `\n` already was made every
movement function correct **for free**, with zero changes to `MoveHome`,
`MoveEnd`, `MoveUp`, or `MoveDown`. `MoveRight` and `MoveLeft` were the
exception, and needed their own extension:

- `MoveRight`'s Bug 13 fix (flip affinity, don't increment) only covered a
  **one-rune** soft boundary. A gap wider than one rune needed the *whole
  gap* skipped, or a plain `cursor+1` step lands on a real rune index that
  no line renders — `renderInputLines`' `i == cursorLine` check then
  matches **no** row, and the caret genuinely disappears (the exact
  symptom reported: index real, no visible column, several more presses
  needed to reach the far side).
- `MoveLeft` never had gap-awareness at all — Bug 13 established Left
  needed none, correctly, for a one-rune gap (decrementing from a boundary
  always lands unambiguously inside the previous line). That reasoning
  does not extend to a wider gap, and Left needed the same treatment,
  which is why it moved from `input.go` (width-independent) into
  `input_wrap.go` (width-dependent) — it now needs `lines(width)` to
  detect a gap at all.

Both route through a new helper:

```go
// lineContaining returns the index of the line whose [Start,End] range
// (End inclusive) actually contains pos, or -1 if pos falls strictly
// INSIDE a gap between two lines.
func lineContaining(lines []inputLine, pos int) int
```

**A genuine crash this surfaced, caught before shipping**: `lineContaining`
returning `-1` is the same answer both for a real inter-line gap *and* for
stepping past the very start or end of the buffer (nothing exists there
either). Confusing the two made `MoveLeft` at buffer position 0 panic
(`lines[-1]`, since `lineAt(lines, -1)` resolves to line 0 and the
gap-skip logic then indexed `lines[0-1]`) and made `MoveRight` at the
buffer's true end land one rune short of it. Both guarded now (`next <=
len(b.runes)` / `prev >= 0` before the gap-skip fires), with a dedicated
test, `TestMoveRightLeftAtBufferEdgesDoNotMistakeOverflowForAGap`, run
against all three `wrapKindCases`.

---

## Editing across a wrap gap — Bug 16, Bug 17, and Bug 17b

Three more rounds, all found **after** this document was first written and
the episode was marked complete. Each is a different failure mode of the
same underlying fact: a wrap **gap** (the section above) is a concept the
*movement* functions know about, but nothing else that can set `b.cursor`
was ever taught it exists. Recorded here in full, per Aryan's explicit
instruction that these be registered once fixed (or, for 17b, once
honestly characterized as not-fixed).

### Bug 16 — inserting a character at a gap boundary made the caret disappear

**Repro:** press End on a line that wraps at consumed whitespace (a real
gap, not a true soft boundary — see above), landing `trailing=false` at the
gap's near edge. Type a SPACE. The caret vanishes — but `state.json`'s
cursor index is correct, confirmed by direct trace, and typing/arrow keys
still work from the "invisible" state.

**Root cause:** `splice` (`input.go`) sets the cursor by plain arithmetic —
`lo+len(repl)` — with no idea a gap exists there, unlike `MoveRight`/
`MoveLeft`, which deliberately skip over one. Typing a space at the gap's
edge widens the gap by exactly one rune, and `lo+1` lands one past the OLD
edge — still inside the NEW, wider gap. `renderTextLineWithCursor`'s
per-rune `i == cursorPos` check only ever matches a rune inside some line's
own `[Start,End)`, so a gap-interior cursor matches **no** row, on any
line, and the caret does not render — same visible symptom as Bug 15,
reached through editing instead of moving.

**Fix, deliberately in the renderer, not in `splice`:** a new
`resolveCaretPosition(lines, cursor, trailing) (cursorLine, displayCursor
int)` in `input_render.go`, called once from `renderInputLines`. It
resolves `cursorLine` via the existing `lineForCursor` (unchanged), then
clamps only the **displayed** index to that line's own `[Start,End]` — the
real `b.cursor` is never touched, so what the next keystroke does is
unaffected. This was a deliberate choice over normalizing inside `splice`:
rendering is the one place every cursor-setting path (move, splice, undo,
redo, `setText`) converges, so fixing it there once is what stops a
*future* edit path from reintroducing the bug by forgetting to normalize —
the same reasoning `setCursor` already established for affinity.

**Tests:** `TestInsertingASpaceAtAWrapBoundaryStillRendersACaret`
(`input_wrap_test.go`) reproduces the exact repro and asserts the resolved
line's range covers the displayed cursor. `TestUndoLandingInAWrapGapStillRendersACaret`
covers the same failure reached via `Undo` (`inputSnapshot.cursor` is a
plain saved int, exactly as arithmetic as `splice`'s `lo+len(repl)`) —
installs a snapshot with a gap-interior cursor directly and confirms the
same fix covers it. Both verified to actually fail (the exact "gap
swallowed the caret" message) when the clamp is removed, then re-confirmed
passing with it restored.

### Bug 17 — the persisted draft's cursor went stale on quit

**Repro, reported next:** typing several characters right at a wrap-gap
boundary and then quitting immediately (no arrow key first) left the
persisted cursor "off by roughly the number of characters just typed."
Pressing one arrow key before quitting reliably made it correct.

**Confirmed by direct trace before touching anything** (per Aryan's
explicit "reproduce, then inspect the in-memory cursor and gap boundaries —
not state.json" instruction): the LIVE `b.cursor` was never wrong. `splice`'s
`lo+len(repl)` matched manual recount at every single step of a run of
inserts starting from a gap-adjacent cursor, every time — even while
genuinely landing inside a progressively wider gap. Bug 16's own theory
("splice computes the near edge instead of the far edge") does not hold up
under trace: `splice` never reads `trailing`, a gap, or calls `.lines()` at
all.

**The real cause was in `app.quit`**, not `splice`: `scheduleDraftSave`
(`draft.go`) only ever returns a `tea.Tick` `draftDebounce` (500ms) out, and
`app.quit`'s `Run` (`actions.go`) returned `tea.Quit` with **no synchronous
flush of its own**. Once bubbletea has `tea.Quit`, a still-sleeping Tick's
eventual `draftSaveMsg` is never delivered — `Update` simply never runs
again — so whatever the *previous* successful debounced write held stayed
on disk, stale by however much was typed since. This fully explains every
reported symptom with no gap-specific mechanism at all: "off by roughly N
characters" is the gap between the last successful flush and quit; "an
arrow key fixes it" is just the natural extra human time between one more
keypress and actually quitting being enough for the 500ms window to close
— not the arrow key doing anything structurally different (it reschedules
the same debounce typing does).

**Fix:** `Workspace.FlushDraft()` (`chat\draft.go`) bumps `draftGen` and
persists synchronously, bypassing the debounce entirely. Exposed to the app
layer through a narrow `DraftFlusher` interface (`app\draft.go`) — the same
type-assertion pattern `FocusAware` already established — so `app.quit`
calls it before returning `tea.Quit`, with zero coupling from `app` back
into `chat`.

**Tests:** `TestFlushDraftPersistsWithoutWaitingForTheDebounce` and
`TestFlushDraftCapturesATypedBurstFromAGapAdjacentCursor`
(`input_workspace_test.go`) prove `FlushDraft` captures the live buffer
with the debounce tick still outstanding. `TestQuitFlushesADraftFlusherWorkspace`
and `TestQuitWithoutADraftFlusherWorkspaceStillQuits` (`app\draft_test.go`,
following `focus_test.go`'s `fakeFocusWorkspace` pattern — `app` cannot
import `chat`, so a local fake stands in) prove `app.quit` actually calls
it, and that a workspace without one still quits cleanly. Verified to
actually fail (`FlushDraft called 0 times, want 1`) with the fix reverted,
then re-confirmed passing restored. `TestTypingASequenceFromAGapAdjacentCursorMatchesManualRecount`
(`input_wrap_test.go`) records the trace finding itself as a permanent
test: the raw cursor index is never ambiguous, whether or not it lands in a
gap.

### Bug 17b — a backspace round-trip at the gap, observed then not reproducible

**Reported:** End on a wrapped line (a gap of 2), five spaces (cursor
correctly `+5`, confirmed against `state.json`), then five backspaces
immediately after — expected the cursor back at the starting position,
observed it several short, repeatably.

**Traced exhaustively before writing any fix**, at every layer that could
plausibly be responsible:

- **`Backspace()` in isolation** — `splice(cursor-1, cursor, nil)` — is
  exactly as unconditional as `splice`'s insert arithmetic (Bug 17's own
  finding): 5 inserts then 5 backspaces round-tripped to the exact starting
  cursor and text, direct buffer calls, no app layer involved, at the
  reported gap width.
- **The real `app.Root` paste/burst-detection layer**, deliberately forced
  into its most aggressive shape: 5 spaces fired back-to-back (microseconds
  apart in a test loop, confirmed by the existing `TestBurstOfKeysIsOnePaste`
  to genuinely activate `r.paste.active`), immediately followed by 5
  backspaces with zero pause. A counting fake workspace showed **all 5**
  backspace events reaching the workspace, every time — the burst-flush
  (delivered as individual keystrokes) and the backspaces never dropped or
  merged anything.
- **The exact combined real-world delivery order**, replayed against a real
  `chat.Workspace` — round-tripped to the exact starting cursor and text
  again.

No construction reproduced a deficit. Reported back to Aryan as such,
honestly, with a request for more specific repro detail rather than a
guessed fix.

**Aryan re-tested the identical sequence afterward and it round-tripped
correctly, every variation tried, with no code change knowingly made in
between.** Before writing this section, that claim was checked rather than
assumed: file modification times on every file in the delete/key-delivery
path (`input.go`, `input_wrap.go`, `input_render.go`, `input_keys.go`,
`draft.go`, `app\paste.go`, `app\root.go`, `app\actions.go`) all predate the
investigation round entirely — the **only** file touched in that round was
`input_wrap_test.go` (new tests added, nothing else), which cannot change
runtime behaviour. `Backspace()`'s body and `paste.go`'s
`pasteBurstGap`/`pasteQuiet`/`minPasteRunes` constants were re-read and
confirmed byte-identical to what was traced when the bug was still
reproducing.

**Status: observed repeatedly, then not reproducible, with no code change
identified as the cause.** Recorded as such, not as fixed — the same
standard `F02-backend-client.md` used for a paste-burst bug that showed a
wrong count once and then three clean runs in a row: a timing-dependent
heuristic (the paste/burst detector, key delivery timing, or something in
Windows Terminal/console input this codebase cannot see past) that has
misfired once should stay **watched**, not be declared solved on the
strength of it not misfiring again. If it returns, the exact repro is
above: End at a wrap gap, N spaces with no pause, N backspaces with no
pause, check the live cursor (not just `state.json`) at each step.

**What guards it going forward:** `TestInsertThenBackspaceRoundTripsToTheStartingState`
(`input_wrap_test.go`, table-driven — mid-word/no-gap, 2-space gap, 8-space
gap) and `TestInsertThenBackspaceRoundTripsThroughRealKeyDeliveryOrder`
(the same round trip through the real burst-flush delivery order). Both
pass today. This is real coverage that was genuinely missing — insertion
and deletion had only ever been tested separately — even though it is not
currently catching anything.

---

## The paste ↔ render circular dependency

This is the full account Aryan asked for — the render-suppression bug is
not a one-line entry, it is the mechanism behind Bug 1 (data loss), Bug 4
(phantom paste counts), and the reported lag, all three, and understanding
it is what let all three get fixed with two small, targeted changes rather
than three separate patches.

### The mechanism

Bubble Tea's `eventLoop` (`tea.go`) calls `model.Update(msg)` then
`p.renderer.write(model.View())` for **every** message it receives, one at
a time, off an **unbuffered** `msgs` channel. On Windows, a paste is not a
single bracketed event — it arrives as one `KeyMsg` per pasted character,
so a large paste means Bubble Tea processes (and, before this fix,
**renders**) thousands of messages in rapid succession.

`app\paste.go`'s burst detector decides whether two keys were typed or
pasted by comparing their **arrival time at Update**, not their arrival
time at the OS: a burst only *activates* — treats keys as belonging to the
same paste — when two pasteable keys are processed within `pasteBurstGap`
(6ms) of each other. That comparison happens **after** whatever rendering
the *previous* key triggered has already run.

`chat\input.go`'s `inputBuffer.lines(width)` is called from three places
in a single frame (`splitGrowth`'s `visualLineCount`,
`ensureCursorVisible`, `renderInputLines`), and — before the incremental
re-wrap fix — recomputed the **whole** wrap every time, because every edit
bumps `version` and the cache had no way to reuse anything. Cost is
O(buffer), and it ran once per keystroke.

**The circle**: as the buffer accumulated pasted content, render cost grew
with it. Once **one keystroke's** `Update`+`View` cost exceeded
`pasteBurstGap`, the **next** pasted character always arrived — measured
at Update time — too far apart from the previous one to look like a
paste. The burst never activated. Render suppression (see below) is keyed
off `r.paste.active`; if a burst never activates, suppression never
engages either, so the *next* paste renders even more slowly than the
first, at the same time as pushing more content into an unbuffered
console-input pipe the render loop is no longer draining fast enough. The
OS console input buffer fills and silently drops records it cannot store.

### The measured cost curve that pinned it

Benchmarked directly against the real code paths, 200-message session,
120×30 layout, before any of this round's fixes:

```
buffer=    20 runes  per-keystroke Update+View =   0.34 ms
buffer= 16737 runes  per-keystroke Update+View =   5.27 ms
buffer= 33454 runes  per-keystroke Update+View =   6.95 ms   (over pasteBurstGap = 6ms)
```

Linear in buffer size, and it crosses the 6ms line at almost exactly the
buffer size a ~229-line paste produces — matching the verification
round's own reported ceiling number precisely. This is why simply
**raising** the render-suppression ceiling in an earlier round *rescaled*
the failure instead of removing it: the cost that mattered was
per-keystroke render cost during a still-assembling burst, which grows
with total accumulated buffer, not with any one paste's size — bigger
total content hits the ceiling in fewer pastes, and one paste large enough
hits it alone (the reported case: one 400-line paste arriving as 14
lines, 386 gone, undo unable to recover them because they were never
typed into the buffer at all — the console buffer dropped the records
before this code ever saw them).

### The two-part fix

1. **Cost** (`input.go`'s `inputBuffer.lines`): memoized on
   `(version, width)`, and re-wrapped **incrementally** on a cache hit
   miss — `dirtyFrom`/`markDirty` track the lowest rune index any edit
   touched, and `wrapFrom`/`paragraphStart` re-wrap only the paragraph
   that changed and everything after it, reusing the untouched prefix
   verbatim. Also removed a `StringWidth(string(runes[i:j]))` allocation
   per wrap token (thousands per wrap of a pasted message) in favor of
   summing `RuneWidth` rune by rune. Measured: 4.36ms → 1.55ms → (after the
   incremental rewrap) a **flat** ~3.4ms whether the buffer holds 16k or
   33k runes — no longer O(buffer).
2. **The loop itself** (`app\render.go`'s `View`, `app\paste.go`'s
   `pasteInFlight`): `View` now re-serves the cached `r.lastFrame` while
   `r.pasteInFlight()` — active burst **or** a pasteable key merely
   *held*, not yet resolved into a burst or released as an ordinary
   keystroke. Covering the held case (not just the active-burst case) is
   what actually breaks the circularity: it makes burst *activation*
   independent of how expensive the previous frame was, since no frame
   renders at all for the ~6ms a key is held, regardless of buffer size.

Also fixed as part of the same investigation, one level down in
`app\paste.go`'s own timer bookkeeping: `scheduleFlush` used to return a
`tea.Tick` **per pasted character**. Every `tea.Cmd` runs in its own
goroutine that blocks sending its result on the same unbuffered `msgs`
channel the console reader needs — a 400-line paste queued roughly 14,000
goroutines against that one channel, which is the more direct explanation
for records actually being dropped (as opposed to merely mis-timed).
`pasteState.flushPending` now allows **at most one** outstanding timer,
ever, re-armed from `lastAt` (not from which tick fired) if it wakes
before the burst has genuinely gone quiet — `onFlushTick`'s whole job.

### Why timing alone cannot classify a burst (Bug 4)

A separate, related defect surfaced during the same investigation:
pressing two **different** keys close together (ordinary fast-typing
rollover) delivers two console records microseconds apart — indistinguishable
from a paste by arrival time alone. The event source does carry a real
distinction (a paste injected via `WriteConsoleInput` has `VirtualKeyCode
0`, a physical key never does), but Bubble Tea's `KeyMsg` keeps only
`Type`/`Runes`/`Alt`/`Paste` — reaching the raw record means replacing its
input reader, which `paste.go`'s own header already establishes cannot be
done on Windows without losing `WindowSizeMsg` entirely.

`looksLikePaste(text string) bool` classifies on **shape** instead, once a
burst is fully assembled: a held key's own auto-repeat (`isRepeatedKey` —
`bubbletea`'s Windows reader expands one console record's `RepeatCount`
into that many identical `KeyMsg`s with **zero** delay between them, so
holding a key is *also* indistinguishable from pasting by timing) is never
a paste; any line break is always a paste; otherwise the burst needs at
least `minPasteRunes` (8) runes. A burst that fails this check is
delivered back to the workspace as the individual keystrokes it actually
was (`deliverAsKeystrokes`) — the text lands identically either way; only
classification, the paste counter, and undo granularity differ.

---

## Attachment chips and the `@` agent state machine

### `AttachmentChip` — the exact shape (`input_render.go`)

```go
type AttachmentChip struct {
    Name    string
    Current bool // true: "✓ current". false: "⚠ changed" — will be re-sent.
    Tokens  int  // a token estimate, shown alongside the chip
}
```

`w.SetChips([]AttachmentChip)` is the whole write surface — F4 never
populates a non-empty slice itself (F04-query-input.md's own "Do NOT
implement the @ picker — render chips you are handed"). `Tokens` is
declared but not yet rendered anywhere (`renderChipRow` only draws
`Name`/`Current`); F8 gets to decide the exact layout once it has a real
token estimate to show. Backspace at buffer position 0 with a non-empty
chip slice and no selection removes the last chip
(`input_keys.go`'s `KeyBackspace` case) rather than doing nothing, per
spec.

### The three-state mode chip (`editorMode`)

```go
const (
    modeChat editorMode = iota
    modePending  // added mid-episode, see below
    modeResearch
)
```

`currentMode()` derives the chip purely from `(in.buf.runes, in.buf.cursor)`
— no sticky flag to fall out of sync with the buffer. It calls
`agentTokenState`, which resolves the leading `@token` **two different
ways**, and this asymmetry is deliberate, not an oversight:

- **Exact match is whitespace-bounded.** The token is `runes[1:end]` where
  `end` is the first whitespace after the `@`, computed **regardless of
  where the cursor currently is**. This mirrors exactly what
  `backend\cli.py`'s `_strip_agent_prefix` resolves (`text.partition("
  ")`), so a *finished* message like `"@research explain the runtime"`
  keeps its chip no matter where the cursor has since moved to — moving to
  the very end of that message, or back before the `@`, must not make the
  chip disappear.
- **Pending is cursor-bounded.** The token is `runes[1:cursor]` — only the
  part the user has *actually typed so far*. This was a real bug (fixed
  mid-episode): with content already in the buffer, moving the cursor to
  position 0 and typing `@` produced a **whitespace-bounded** token that
  swallowed whatever text already followed it (e.g. `@` before `"hello
  world"` made the token `"hello"`, which names no agent and is no
  viable prefix of one), so the chip stayed on `modeChat` and the user got
  no feedback that `@` had registered at all. Bounded by the cursor
  instead, the typed token right after a bare `@` is `""` — a viable
  prefix of every agent — and `modePending` shows correctly regardless of
  what already sits after the cursor.

A token already followed by whitespace is "finished," not pending — typing
`@r hello` (space after `r`) reads as `modeChat`, not
`modePending`, since there is nothing left to type into that token.

`modeChipPrefix` renders three fixed strings — `▸ chat │ `, `▸ @agent… │ `,
`▸ @research │ ` — deliberately **not** interpolating the half-typed token
into the pending chip, since that would change the prefix's own width on
every keystroke and re-wrap the text beside it as you type.

### The hardcoded agent list — a real stopgap, and its case-sensitivity trap

```go
var knownAgentIDs = map[string]bool{
    "general": true, "filler-explainer": true, "research": true,
}
```

There is no `ListAgents` protocol command (only `ListProviders`/
`ListProviderModels` exist), so these are the three
`backend\agents\builtins.py` ids, copied by hand. **F8 must replace this
map with a live fetch before shipping a real picker.**

A genuinely latent trap, verified against the real backend rather than
assumed: `AgentRegistry.resolve` (`backend\agents\registry.py`) tries an
**exact id match first** (`agents` table, `id TEXT PRIMARY KEY`, no
`COLLATE NOCASE` — a mixed-case id genuinely misses), then falls back to a
**case-insensitive NAME match**. Verified directly against a scratch
database:

```
resolve('research')  -> 'research'      resolve('RESEARCH') -> 'research'
resolve('Research')  -> 'research'      resolve('researcH') -> 'research'
resolve('general')   -> 'general'       resolve('GENERAL')  -> 'general'
resolve('report.pdf')-> AgentNotFoundError
```

Mixed case resolves **only because every built-in's `name` happens to
lowercase to its `id`** (`"Research"` → `research`). `matchedAgentID`
lowercases the whole typed token and checks it against `knownAgentIDs`
(ids only) — which happens to agree with the real backend for exactly
these three built-ins, but is not actually mirroring the backend's real
rule. **A user-defined agent with id `code-review` and name `Reviewer`
would resolve `@Reviewer` (case-insensitive name match) but NOT
`@CODE-REVIEW`** (no case-insensitive id match exists in the backend
either) — and F4's current map has no way to represent that split at all,
since it only ever stores lowercased ids.

**F8, when it replaces `knownAgentIDs` with a live fetch, must pull both
ids and names and match them the way `AgentRegistry.resolve` actually
does** — id exact match, then name case-insensitive — not just a
lowercased id set. Noted here as the specific defect to avoid re-shipping,
not merely "the map is a stopgap."

---

## Growth — overlay + auto-scroll (`render.go`'s `splitGrowth`)

Entirely local to the `chat` package — `layout.Compute`, `layout.State`,
and the `Workspace` interface are all untouched. `l.Main.H + l.Secondary.H`
is a fixed total Root's `View` pads/clips to every frame; `splitGrowth`
only ever **trades** rows between the two boxes at that same total, using
`w.input.buf.visualLineCount(textWidth)` to decide how many rows the input
currently wants, capped so the viewport never gives up more than half its
own **nominal** height (`(l.Main.H+1)/2`, not a moving target as the
viewport itself shrinks). Past the cap, `ensureCursorVisible` keeps the
cursor's own wrapped line inside the input's own internal scroll window —
the input never shrinks below what `layout.Compute` originally gave it.
The transcript's existing pinned/bottom-anchored scroll (F3) already keeps
the newest message visible as its row budget shrinks — no new state needed
for the auto-scroll half of the requirement.

## Focus model

The input holds keyboard focus by default (`w.input.focused = true` in
`New`). `Esc` (`chat.browse`, always available in Chat, idempotent) and
`Tab` (`pane.focus.next`, now doing its first real job — toggles via
`FocusAware.InputFocused()`, never reading a workspace's internal fields
directly) both route through `app.EnterBrowseMsg`/`app.FocusInputMsg` —
Root's `Update` has no case for either, so its fallthrough
(`ws, cmd := r.current().Update(msg)`) forwards them to the workspace, the
same mechanism `PasteMsg`/`EnvelopeMsg` already used. In browse mode, F3's
`j`/`k`/`y`/`G` hotkeys work exactly as they did before this episode;
outside it, every keystroke routes to the input.

Browse mode replaces the mode chip with a literal, always-accurate
indicator — `◌ BROWSE — j/k/y/G · Tab to type │ ` — chosen over silently
discarding keys with no feedback at all (the original design gap a
verification round flagged). Deliberately names only bindings that
actually work: an earlier draft advertised `Esc` too before Bug 12
confirmed Esc only ever moves *into* browse mode, never back out of it.

## Draft persistence

Debounced 500ms after typing stops (`draftDebounce`), keyed per session
(`w.sessionID`), generation-counter-guarded the same way `app\paste.go`'s
flush timer is (a later keystroke supersedes an in-flight save rather than
racing it). `onSend`'s `clearDraft` saves synchronously and immediately,
bypassing the debounce, so a crash between "sent" and the next tick cannot
resurrect cleared text. `restoreDraft` (called from `setSession`) **always**
calls `setText`, even when no draft exists for the session — a session
with nothing on file must start empty, not carry over whatever a
previously open session left in the buffer (a real bug, caught by its own
test). It also runs every restored draft through `filterInsertable` before
loading it — a draft written before that filter existed can hold control
characters that are invisible on screen but real in the buffer, and
without repairing on load, a corrupted `state.json` restored the exact bug
it came from on every subsequent launch. `Cursor` is clamped to the
repaired text's length, since dropped runes make the saved cursor point
past content that no longer exists.

---

## Windows key-decoding findings

Recorded because a later episode extending the keymap needs these facts,
not to rediscover them by manual testing again.

- **Ctrl+←/→/Home/End decode correctly and distinctly** — confirmed by
  reading `key_windows.go`'s `keyType()`: every one of these explicitly
  checks `ctrlPressed` in its own `VK_*` case.
- **Ctrl+Backspace is indistinguishable from plain Backspace, on any
  platform this bubbletea version runs on, not just Windows.** `VK_BACK`
  maps unconditionally to `KeyBackspace` — its case is the *only* named key
  in the whole switch that does not check `ctrlPressed`, and there is no
  `KeyCtrlBackspace` constant anywhere in the library to decode into even
  hypothetically. **Alt+Backspace is the actual working binding** for
  word-delete instead (`Alt` is read into `KeyMsg.Alt` independently of key
  type, for every key) — `DeleteWordLeft` is reachable through it.
  Ctrl+Backspace quietly falls through to an ordinary single-character
  delete rather than failing outright.
- **Alt+Enter never reaches the process at all** — it is Windows Console's
  own fullscreen-toggle shortcut, intercepted before the child ever sees
  it. **Ctrl+J is newline's actual working binding** (`'\n'` decodes to
  `KeyCtrlJ` unconditionally in the control-character table, no OS binding
  competing for it). Alt+Enter is kept registered alongside Ctrl+J anyway
  — harmless here, and it may work on a terminal/OS that does not
  intercept it.
- **A bare modifier keydown/up (Ctrl, Alt, the Windows key, Caps Lock,
  pressed or released alone, with no following character) decodes as
  `KeyRunes` with a rune value of `0`.** `keyType()`'s final fallback for
  an unrecognised key is `return KeyRunes`, with `Runes` set to `e.Char`
  verbatim — for one of these events `e.Char` is the zero rune. Without a
  guard, this reaches the burst detector (`pasteable()`, now rejects
  `k.Runes[0] == 0`) and the buffer's own insertion path
  (`filterInsertable`, now drops anything that fails `unicode.IsPrint`
  except `\n`/`\t`) identically to a real typed character.
- **A held key's OS auto-repeat has zero delay between records**
  (`inputreader_windows.go` expands one `KeyEventRecord`'s `RepeatCount`
  into that many identical `KeyMsg`s in a tight loop) — indistinguishable
  from a paste by timing alone, which is why the paste detector needed a
  shape-based classifier (`looksLikePaste`/`isRepeatedKey`, see the paste
  section above) rather than a timing-only one.
- **Alt+Backspace's own "needs a second press" quirk remains unexplained
  and is flagged, not fixed.** The first press appeared to delete only one
  character, as if Alt were not yet seen as held, while every other
  Alt-modified key reads `k.Alt` correctly on the first press — looks like
  the console reporting Alt's own key-down slightly behind Backspace's,
  the same general family as the well-known "first keystroke after
  refocus is swallowed" quirk. A later verification round reported it
  "appears to work reliably now" with **no code change on this side**
  between the two reports — genuinely not understood, and left flagged in
  code (`input_keys.go`'s `handleInputKey` doc) for re-verification rather
  than claimed fixed.
- **Windows' native emoji picker (Win+.) is unrouted.** Selecting an emoji
  landed in the transcript, not the input — an IME/`WM_CHAR` composition
  path this console app has no special handling for at all. Reported,
  investigated, **not fixed** — could not be reproduced or safely
  addressed without live testing against the actual picker. A known
  limitation for the next episode to pick up, not resolved here.

---

## Layout / theme decisions made here

- **`theme.SelectText`/`theme.SelectBg`** (`th.Fill`, already named in
  `theme.go`'s own doc for exactly this: "a selected list row, a focused
  input") render both the selection range and the single-rune caret — no
  new role added.
- **The chat/research accent is the one deliberately "moving colour"**
  (F04-query-input.md: "make that the only moving colour on screen at that
  moment") — reserved for `queryBorderRole`'s `modeResearch` case, and only
  while the input is actually focused. Browse-mode unfocus uses
  `theme.Warning` for its own indicator instead, deliberately a different
  role so the two signals never compete for the same "moving colour" at
  once. `modePending`'s chip text uses `theme.TextSecondary` — visible, but
  explicitly not the accent, since nothing has resolved to an agent yet.
- **Exactly one pane's border is ever `BorderFocus`** — `mainBorderRole`
  (View Port) and `queryBorderRole` (Query Area) are mirror images of the
  same `w.input.focused` bool, so focus is always visually unambiguous
  between the two panes.

## Manual Test Log (F4)

Six rounds of interactive verification against Windows Terminal, real
backend, scratch profiles. Selected, specific confirmations (the full
back-and-forth is in the session transcript this doc was written from):

1. Every editing behaviour in F04-query-input.md's Build section —
   cursor/selection/undo/word-wise/paste-as-one-draft — confirmed working,
   including select-all-then-type replacing the selection.
2. Growth: 20 lines with Alt+Enter grows and auto-scrolls correctly;
   scrolling up mid-typing does not yank the view; the transcript does not
   re-wrap while the input grows.
3. Draft survival across `Ctrl+C` **and** a full terminal close — both
   confirmed to preserve text and cursor position, on a clean
   (post-Bug-2-fix) `state.json`.
4. Pasting the ~229-line real-world content **4+ times consecutively**
   after the final fix: every paste landed complete, full line counts
   reported, nothing vanished. Pushed past 1,000 accumulated lines with no
   threshold appearing at all — typing and cursor movement at that size
   feel identical to an empty buffer.
5. Home/End on soft-wrapped lines, Up/Down across wrap boundaries,
   Left/Right from an End position, mid-buffer edits reflowing correctly,
   CJK width — all re-verified correct after the wrap rewrite.
6. The `@` pending state verified against the full intended table: bare
   `@`, `@r`, `@rese` all show the pending chip; `@research` shows the
   research chip; `@research.pdf`/`@report.pdf` correctly show no chip;
   the pending state survives a restart (persisted via the same draft
   path, repaired-on-load like any other draft).
7. Mixed-case agent resolution (`@RESEARCH`, `@Research`, `@researcH`,
   `@REsearcH`) all correctly activate the research chip — confirmed
   correct against the real backend, not a bug.
8. Simultaneous/rollover keypresses no longer register as a paste;
   End-then-Up carries affinity correctly across multiple lines; the
   browse indicator reads correctly with no dead `Esc` promise.
9. Lag: a single Ctrl+V is near-instant; ten in a row (40-line content)
   survives 20+ repetitions with no degradation.

**Three further rounds, after this document was first written**, each with
its own precise repro and (for Bugs 16 and 17) confirmation the fix
actually resolved it — Bug 16 confirmed by re-testing space-insertion at
several wrap-gap widths after the render fix; Bug 17 confirmed with the
exact numbers the debounce/quit fix predicts (`+5` per burst, faithfully,
quitting immediately with no arrow key); Bug 17b's round is its own case —
see "Editing across a wrap gap," above, for the full account of why it is
recorded as observed-then-not-reproducible rather than confirmed fixed.

## Bugs caught during manual testing

Fifteen numbered bugs across the original six verification rounds, plus
three more (16, 17, 17b) found across three further rounds after this
document was first written. **Bug 1/4/lag** (the paste ↔ render circular
dependency) and **Bug 6/11/13/14/15** (the cursor affinity model) and
**Bug 16/17/17b** (editing across a wrap gap) each have their own full
section above — the entries below are deliberately short and point there
rather than duplicating the account.

1. **Data loss on repeated/large pastes.** Root cause: the paste ↔ render
   circular dependency — see its own section above. Fixed by the
   incremental re-wrap, the render-suppression-while-held fix, and the
   single-flush-timer fix, together.
2. **NUL runes surviving into typed/pasted text and the persisted draft.**
   A bare modifier keydown/up decodes as `KeyRunes` with a zero rune (see
   Windows key-decoding findings). Fixed by `filterInsertable` (the single
   insertion chokepoint) and `pasteable()`'s zero-rune rejection.
3. **`@` mode detection stopped working after a large paste + edits.**
   Root cause, once fully traced: the exact-match rule is
   whitespace-bounded on purpose (see the `@` state machine section), and
   the pending rule needed to be cursor-bounded to give feedback on a bare
   `@` typed before existing content — both are now correctly
   distinguished, not a corruption bug as first suspected.
4. **Phantom paste counts during ordinary typing.** Root cause: a held
   key's OS auto-repeat has zero delay between records — see Windows
   key-decoding findings and the paste section's `looksLikePaste`
   discussion. Fixed by `isRepeatedKey` and shape-based (not
   timing-based) burst classification.
5. **Alt+Enter does not insert a newline.** Root cause: Windows Console's
   own fullscreen-toggle shortcut, never reaches the process. Fixed by
   adding Ctrl+J as the actual working binding (kept Alt+Enter registered
   too, for other platforms).
6. **Home/End misbehave on wrapped lines.** The first cursor-affinity
   round — see the dedicated section above.
7. **Alt+Backspace needs a second press.** Investigated, not fixed — see
   Windows key-decoding findings. Still flagged for re-verification; a
   later round reported it "appears to work now" with no code change,
   which is itself unexplained.
8. **Windows emoji picker (Win+.) inserts into the transcript, not the
   input.** Investigated, not fixed — see Windows key-decoding findings.
9. **`backend.retry` reports unavailable while the backend is healthy.**
   Traced and confirmed **correct by design**, not a bug: `banner != ""`
   is required by `When`, and `ConnectedMsg` clears the banner on every
   successful (re)connect — pre-existing F2 code, untouched by F4.
10. **The `@` rule was position-based, not content-based, against the real
    agent registry.** `@report.pdf` false-positived into research mode.
    Fixed by resolving against `knownAgentIDs` — see the `@` state machine
    section above for the full model, including the hardcoded list's
    known case-sensitivity gap.
11. **Vertical movement (Up/Down) did not carry cursor affinity.** The
    second affinity round — see the dedicated section above.
12. **The browse indicator advertised `Esc` returning focus to the input,
    which does not happen** — only `Tab` does. Fixed by correcting the
    label rather than adding a binding, since the modal design (explicit
    Tab/Esc, not "any key returns focus") was the deliberate choice from
    round one.
13. **Left/Right (horizontal movement) did not carry cursor affinity.**
    The third affinity round, and the one that finally motivated
    centralizing the whole model through `setCursor` — see the dedicated
    section above.
14. **A line that wraps exactly at a single space keeps that space as
    invisible, landable trailing content.** The wrap-layer half of the
    affinity story — see "The trailing-space defect this model just
    absorbed," above.
15. **A whitespace *run* wider than the box was hard-broken as visible
    content instead of gapped**, plus two edge cases the fix's own
    generalization exposed (a `MoveLeft` crash at buffer position 0, an
    off-by-one in `MoveRight` at the buffer's true end) — see the same
    section above.
16. **Inserting a character at a gap boundary made the caret disappear.**
    Found after this document was first written. Root cause and fix — see
    "Editing across a wrap gap," above. Fixed in the renderer
    (`resolveCaretPosition`), deliberately not in `splice`.
17. **The persisted draft's cursor went stale on quit.** Same section
    above. Root cause was never in `splice` (traced and ruled out
    explicitly) — `app.quit` had no synchronous flush of the debounced
    draft save. Fixed by `FlushDraft`/`DraftFlusher`.
18. **(Reported as "17b") A backspace round-trip at a wrap gap boundary
    undercounted, then stopped reproducing.** Same section above. Traced
    exhaustively at every layer with nothing found; a re-test afterward,
    with no code change made in between, round-tripped cleanly. Recorded
    as *observed, then not reproducible* — not as fixed. Guarded by two
    new round-trip tests regardless.

### A tooling hazard worth recording

Several rounds of this episode's own patches (applied via scripted string
replacement rather than the ordinary edit tool) accidentally embedded
**literal NUL bytes** into source files — the exact defect Bug 2 was about
fixing in the *product*, reproduced by accident in the *tooling* used to
fix it. Caught each time by scanning every touched file
(`\x00` byte count) before every build, not by the compiler (a literal NUL
inside a Go string produced a plain syntax error and was obvious; a NUL
inside a *comment*, once, built silently and was only caught by the
explicit scan). Any future large-scale scripted edit to this codebase
should scan for this the same way.

## What the next episode inherits

- **The cursor affinity model is load-bearing and must be extended, not
  bypassed.** Any new `Move*` method: compute the destination, decide its
  affinity explicitly, call `setCursor` once as the last step. The
  reflection test (`TestEveryMoveMethodIsRegistered`) enforces registration
  in `allMovementOps`; the consistency sweep
  (`TestEveryMovementFromAnEndPositionStaysConsistent`) checks the actual
  invariant across all three `wrapKindCases`. Extend `wrapKindCases` again
  if a new wrap SHAPE is ever introduced (e.g. tabs, zero-width joiners) —
  the mid-word-only sweep is exactly how Bug 14/15 survived a whole round.
- **`onSend` is F5's actual seam.** It currently clears the buffer via
  `ClearKeepingUndo` (undoable — a cheap safety net, not a real send) and
  calls `w.clearDraft()`. F5 replaces this function's body with the real
  `protocol.CmdSendMessage` call and a task slot
  (`render.go`'s `renderTaskSlot`, unwired since F3), keeping the clear as
  its last step.
- **F8's @ picker inherits `knownAgentIDs`, `AttachmentChip`, and
  `OpenAttachmentPickerMsg`.** It must replace the hardcoded id map with a
  live fetch that matches `AgentRegistry.resolve`'s real rule (id exact,
  then name case-insensitive — see the case-sensitivity trap above, not
  just "make it live"), and needs **agents and files in one list**, per
  the original episode note (an `@token` can name either). It also needs a
  protocol command that does not exist yet — there is no `ListAgents`.
- **F12's real `WorkspaceState` must be adaptable TO `config.Draft`, not
  identical to it** — `Drafts` is F4's own narrow addition to
  `config.State`, not F12's eventual general shape (scroll position, open
  files, cursor in general).
- **The render-suppression mechanism (`r.lastFrame`, `pasteInFlight`) is
  general infrastructure, not paste-specific in principle** — any future
  source of many-messages-in-a-tight-burst could reuse the same pattern:
  skip rendering while something is knowably still assembling, key it off
  a narrow "in flight" predicate rather than the specific feature.
- **`resolveCaretPosition` (`input_render.go`) is the one place a wrap
  gap's DISPLAY position is resolved — Bug 16.** Any future rendering
  change that touches how the caret is drawn must go through it, not
  re-derive `cursorLine`/`displayCursor` independently, or it can
  reintroduce the exact bug it fixed.
- **`FlushDraft`/`DraftFlusher` (`chat\draft.go`, `app\draft.go`) is
  general infrastructure for "synchronously escape a debounce before the
  program can no longer deliver its result," not draft-specific in
  principle — Bug 17.** Any future workspace with its own debounced saved
  state should implement `DraftFlusher` and be wired into `app.quit` the
  same way, rather than each workspace inventing its own quit-time flush.
- **Still unresolved, flagged rather than fixed:** Alt+Backspace's
  intermittent first-press failure (Windows key-decoding findings), and
  the Windows native emoji picker not routing into the focused input.
  Both are platform/library-boundary issues this package cannot see past.
- **Bug 17b is a WATCH item, not a closed one.** A backspace round-trip at
  a wrap gap boundary was observed undercounting, traced exhaustively with
  no cause found, then stopped reproducing with no code change identified
  as why — see "Editing across a wrap gap" above for the full account and
  the exact repro shape. `TestInsertThenBackspaceRoundTripsToTheStartingState`
  and `TestInsertThenBackspaceRoundTripsThroughRealKeyDeliveryOrder` guard
  it going forward, but neither is currently catching anything, so they
  are not proof it cannot recur. Any future episode touching the paste/
  burst-detection timing (`app\paste.go`) or the delete path (`Backspace`/
  `DeleteWordLeft`/`splice`) should re-run the exact repro deliberately,
  the same way F2's own "observed once" paste note asked F4 to.
- **Deliberately unfinished, per F04-query-input.md's own Do-NOT:** the
  `@` picker itself (F8), real send/queueing (F5).
