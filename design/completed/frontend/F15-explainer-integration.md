# F15 — Filler-Explainer Integration ✅

**The Filler and the Explainer are wired into the Editor. Selecting code and
invoking explain streams an explanation into a panel beside the code; invoking
fill at the caret streams the proposed region back as GHOST TEXT at the cursor
and then opens F9b's diff card, whose apply lands in the UNSAVED buffer and is
removed by one `ctrl+z`. Nothing is written to disk by any path in this
episode. F9's Done-when #7 — refuse to apply when the file changed on disk
since the region was generated — is CLOSED here as Done-when 7b, populated
from B36's real generation-time `(mtime, size)` pair.**

> ✅ **v1 is now feature-complete on the frontend.** Every `[P1]` frontend
> episode that gates feature completeness has shipped. What remains for v1 is
> `[P2]`: F16 (polish), F17 (web preview, optional), F14 (editor terminal),
> F20 (live stale detection), plus the backend's own remaining sessions.
>
> **Four defects found during the gate were not in this episode's Build
> section and are recorded here as root causes rather than as symptoms**, two
> of them in code F15 only *exposed*: bubbletea's Windows chord leak (upstream
> of Point), the inline prompt's undo coalescing, `ctrl+c`'s double meaning,
> and the ghost's scroll architecture. Each has its own section below.

---

## Done when — all closed

| # | Criterion | State |
|---|---|---|
| 1 | One line selected → the sent context carries the whole enclosing function | ✅ `editorContextFor` + `resolveScope` |
| 2 | Explanation streams into the panel; `esc` dismisses; `send to chat` moves it | ✅ — `send to chat` is a **prefill**, see Deliberate limitations |
| 3 | Fill streams ghost text at the cursor, then a diff card whose lines map onto the file | ✅ |
| 3b | Switch to Chat mid-stream and back → the ghost shows everything that arrived | ✅ — `BackgroundReplies` + `BackgroundCmdFilter` |
| 3c | `esc` mid-stream discards the ghost and cancels; the buffer is untouched and not dirty | ✅ — structural, see **Ghost text is not in the buffer** |
| 4 | `e` edits the proposal, `⏎` applies the EDITED text, one `ctrl+z` removes it | ✅ |
| 5 | The file's mtime is unchanged until the user saves | ✅ — and this is where the disk-write bug was found |
| 6 | A two-file request is refused with a readable message | ✅ |
| 7 | Edit the buffer after generating, then apply → refused, re-generate offered | ✅ — F9b's `baseVersion` guard |
| **7b** | **Modify the file ON DISK after generating, then apply → refused with a reason** | ✅ **F9's Done-when #7 is now CLOSED** |
| 8 | The same agent from Chat produces no inline diff card | ✅ — wording corrected in the task doc first; see below |

### 7b closes F9's #7, and the pair comes from B36 — never from `doc`

F9b shipped `DiffCardOptions.RecordedMtime` / `RecordedSize` **zero by
design** and wrote down why every cheap source for them is wrong.
`doc.mtime` / `doc.size` describe the moment the file was OPENED or last
SAVED, which is not the moment the region was generated, and substituting it
fails in **both** directions: a file edited externally between open and
generation refuses an apply that was safe, and a file Point itself saved
between generation and apply accepts one that was not.

F15 populates them from `FillProposal`'s own `mtime` and `size` — B36's pair,
`os.fstat`'d from the same descriptor `_render_editor_context` read the region
from. `HasRecordedPair bool` replaced the zero-value sentinel, because a real
mtime of 0 and "no pair supplied" are different states and a zero check
cannot tell them apart.

**F9b's tripwire was rewritten, not deleted.**
`TestTheStalenessPairIsCarriedAndUnpopulated` existed to fail the moment
anyone populated the pair without closing #7. It now asserts the opposite —
that the pair IS populated, from the proposal, and that an apply against a
changed file is refused on the card.

### Done-when 8's wording was corrected before it was answered

It read "produces a **result card** instead of an inline diff". The first gate
run answered "pass" against a criterion that **cannot be met**: the task tray
is background work only (`builtinAgentBackground` records `filler-explainer`
as `false`, because `max_steps=1` makes `is_bounded_loop` false and the
backend runs it as an inline turn), `trayRows` filters on exactly that, and
the tray's own empty state says so in words. An inline turn has no tray row
and therefore no result card to open.

**The doc was corrected; the tray was not changed.** Making inline turns
appear in a tray labelled "background work" would make the tray lie about
every other row in it. This also reversed an earlier agreement made in this
session before the code was read — recorded because the wrong version is the
intuitive one.

---

## Root causes and fixes

Four of these were found at the gate, not planned. Each is stated as the
mechanism, because in three of the four the first hypothesis was wrong and the
wrong one is the plausible-sounding one.

### 1. The Windows chord leak — upstream of Point, mitigated by F2 / F3

**Symptom:** a literal `w`, `W` or `q` typed into the open file when the
leader chord or quit was pressed, and once a doubled `ww`. With autosave
armed, those characters reached disk — which is how this was first reported,
as "app.py written without `ctrl+s`".

**Root cause, read in the dependency rather than inferred:** bubbletea v1's
Windows console reader (`key_windows.go`) ends `keyType` with a bare
`return KeyRunes` when a CTRL bit is set but neither the control character nor
the virtual-key code matches its tables — and `tea.Key` has **no Ctrl field**
to carry the modifier. `Ctrl+Shift+W` reports Char `'W'`, so it arrives
indistinguishable from typing. The doubled `ww` is the same fault twice: the
mangled first key leaves nothing pending, so the second lands as text too.

**It is not fixable inside Point.** A heuristic ("a bare letter immediately
after a near-miss is probably a chord") was proposed and rejected: it guesses
about the user's typing, and the failure it produces when it guesses wrong is
a swallowed character in a file.

**Fix — add a key that cannot degrade, keep the old one wired:**

| action | primary | legacy |
|---|---|---|
| leader | **F2** (`actions.Leader`) | `ctrl+w` (`actions.LeaderAlt`) |
| quit | **F3** (`Action.Chord`) | `ctrl+q` (`Action.AltChord`) |

`VK_F2` resolves in the virtual-key table **before** the ctrl/rune branch is
reached and carries no rune to insert.

**Both stay, and both are shown.** Removing `ctrl+w` would make every
muscle-memory press insert a literal letter 100% of the time instead of
occasionally — strictly worse. `actions.LeaderLabel` and
`Action.ChordLabel()` print both everywhere, so neither key is a secret.

**Alt+\<letter\> was read and rejected, not assumed.** It is a cleaner path
than ctrl — `Alt` is set unconditionally from `ControlKeyState` outside
`keyType`'s lookups, so it cannot be lost the same way — but **AltGr is
`LEFT_CTRL|RIGHT_ALT`, which sets that same Alt bit**, so on a non-US layout
an alt-letter leader would swallow AltGr-composed characters. Trading one
layout-dependent ambiguity for another is not a fix. *(This finding is reused
in `F16-polish.md`, where the Editor's `f7` close-tab binding needs a
replacement and Alt+w is ruled out for the same reason.)*

**A second, independent leak was found while instrumenting this one:**
`deliverKey` in `app\root.go` had a path that bypassed the chord registry.
That one *was* Point's and is fixed. It was found by logging the cause of
every dirty-making edit rather than by theorising — the approach that also
settled "what wrote app.py" after three rounds of wrong hypotheses.

### 2. The inline prompt's undo removed several words at once

**Symptom:** `ctrl+z` in the fill prompt deleted a whole run of fast typing.

**Root cause:** the prompt grew its own undo stack, and its coalescing rule
was simpler than the buffer's. `editor\buffer.go` breaks a run on **three**
conditions and the prompt checked none of them: a time gap, a
non-contiguous caret, and a word boundary.

**Fix:** `recordText` now applies all three (`undoCoalesceGap`,
`caretContiguous()`, `!startsWord(...)`). One further bug was introduced and
caught while fixing it: `lastCol` was being set inside `record`, which runs
**before** the edit moves the caret, so every keystroke compared the caret
with itself-before-the-insert and nothing ever coalesced — the exact inverse
of the reported bug. `key()` now updates it in a `defer`.

### 3. `ctrl+c` — copy and cancel separated

**Symptom:** "trying to copy selected text shows *Cancel Stream is not
available right now* instead of copying."

**Two things were true, and the second is the larger one:**

1. `ctrl+c` is `chat.cancel`'s chord with a Chat-only `When`, and
   `dispatchKey` **swallows** an unavailable action with a status line rather
   than falling through — so the Editor never saw the key.
2. **The Editor had no copy implementation at all.** F13a built selection —
   anchor, cursor, the painted highlight, every motion that extends it — and
   nothing that could copy it. There was no clipboard call anywhere in the
   package. So this was never a regression.

**Fix, and it is a design decision rather than a bug fix.** `ctrl+c` is
**copy, with no exceptions**, and `esc` is the only cancel. The faithful
alternative — scope `ctrl+c`: cancel while streaming, copy otherwise — keeps
the spec's letter and makes a key's meaning depend on state the user cannot
see. That is the failure class this episode already paid for four times, and
`ctrl+c` is the worst candidate for a fifth because **its failure is silent
and in the dangerous direction**: a `ctrl+c` that cancelled instead of
copying leaves the user believing they have the text, and the next paste puts
something stale somewhere else. `esc` already cancels in both modes and is
F15's own primary wording; `ctrl+c` appears only in a parenthetical.

`copy.go` is OSC 52 (the mechanism `chat\copy.go` uses, so a copy works over
SSH where the terminal owns no clipboard a mouse selection could reach),
**selection-only** — no whole-line fallback, so an accidental press cannot
replace the clipboard with a line the user never chose.

`CapturesKey` claims `ctrl+c` unconditionally so it gets past `dispatchKey`;
`capturesModalKey` is esc-only.

**A fifth bug was found by writing the test.** Three comments — in `copy.go`,
`prompt.go` and `panelKey` — all claimed `handleKey` took `ctrl+c` before the
modal surfaces. It did not: the check sat *below* them, so `ctrl+c` with a
prompt or panel open fell into the prompt's own switch, matched nothing, and
**did nothing at all** — silent, which is the failure mode the design exists
to avoid. The code was moved to match.

### 4. The ghost's scroll architecture — a rewrite, not a patch

**Symptoms, all four observed directly with the stream slowed down:** the
insertion line stayed visually fixed while the ghost scrolled *within* it;
scrolling down could not reach the real code **after** the insertion point;
arrow keys glitched once the region exceeded the pane; everything was normal
the instant the edit was applied.

**Root cause: the view had TWO scroll states.** An earlier fix in this same
episode gave the ghost its own offset (`ghostText.top`) nested at the
insertion line, to solve a real bug — the renderer only emits ghost rows while
it is drawing the anchor line, so the tail of a long region was unreachable at
every file position. It solved that by making the region a **sub-viewport**,
and all four symptoms are consequences:

- `followGhost` set `d.top = anchorLine` and then moved `g.top` → the anchor
  was pinned and the text scrolled inside it.
- `d.top` could not pass the anchor without the renderer dropping the ghost,
  **and** a scroll down that reached the ghost's last row *re-armed the
  follow*, which pulled the viewport back on the next frame. The rest of the
  file was behind a wall.
- `ensureVisible` was handed `textRows - injectionsAt(caret)`, where the
  injected count was itself `total - g.top` — a number that moved with the
  ghost's own offset — so the reveal's row budget wobbled frame to frame.

**Fix — one row sequence, one position** (`editor\viewrows.go`):

```
line 0 … line anchor │ ghost row 0 … ghost row n-1 │ line anchor+1 …
```

`doc.top` indexes **that** sequence. Two pure functions own it — `rowOf(line)`
and `at(row)` — and the renderer, the hit-test, the reveal and the wheel all
call them, so they cannot disagree. With nothing injected a view row **is** a
buffer line, which is why every caller written before the ghost existed reads
unchanged. `ghostText.top`, `ghostDisplayLines`, `ghostRowCount`,
`injectionsAt`, `rowToLine`, `bufferRowAt`, `ghostFollowTop`, `scrollGhost`
and `ghostAtItsEnd` are all **deleted**.

The follow now re-arms only when a scroll down **crosses** the live edge from
above, not merely when it lands at or past it. Crossing is a one-time event:
once pinned, the next notch starts *at* the edge, does not cross, and carries
on down into the rest of the file.

**The click mapping was re-derived against the new model, not carried over.**
The earlier fix's arithmetic was correct only while the anchor could never be
above the top of the pane. It can be now. Reverting `posAt` to the old
arithmetic fails the new tests by a **varying** 13, 12, 11, 10, 9 — the spread
is the case the old version could never have handled.

---

## Where the code went

**New files**

| File | What |
|---|---|
| `editor\context.go` | `EditorContext` mirroring B11 field-for-field, `resolveScope`, `scopeName` |
| `editor\invoke.go` | the whole invocation machinery: lazy `CreateSession`, `BeginExplain`/`BeginFill`, the decoder, `onFillProposal`, the background-delivery allow-list |
| `editor\ghost.go` | `ghostText` — a `[]string` plus an anchor line, and `partialReplacement` |
| `editor\prompt.go` | the inline fill prompt, with the buffer's own undo-coalescing rule |
| `editor\explainpanel.go` | the panel that splits the Code File pane |
| `editor\copy.go` | OSC 52 copy — the Editor's first clipboard path |
| `editor\viewrows.go` | the unified row index |
| `app\editoragent.go` | the `EditorAgent` seam |
| `chat\prefill.go` | `PrefillDraft` — appends, never replaces |
| `backend\demo\fixtures\filler_fill_long.json` | a deterministic 46-line proposal in 604 token-sized pieces |

**Changed, structurally**

- `actions\registry.go` — `Leader`/`LeaderAlt`, `Action.AltChord`, `IsLeader`,
  `KeyLabel`, a derived `LeaderLabel`.
- `app\contribute.go`, `app\root.go` — `BackgroundDelivery` (default-DENY),
  `BackgroundCmdFilter`, `WorkspaceActivatedMsg`/`WorkspaceDeactivatedMsg`,
  `CloseWorkspaceOverlayMsg`, and `deliverKey` routed through `dispatchKey`.
- `editor\render.go`, `editor\mouse.go`, `editor\motion.go` — the view-row
  rewrite.
- `editor\diffcard.go` — the two-phase apply, `HasRecordedPair`, and the card
  closing itself without arming autosave.
- `editor\doc.go`, `editor\save.go`, `editor\autosave.go` — `diskHash` /
  `matchesDisk`, and a log line on every `WriteProjectFile`.
- `protocol\types.go` — `EvFillProposal`.
- `backend\providers\demo\provider.py` — `POINT_DEMO_LONG_FILL`,
  `POINT_DEMO_STREAM_DELAY_MS`, `POINT_DEMO_CARD_DELAY_MS`.

**Test state at close:** `editor` 404, `app` 380, `chat` 281, `diff` 9 —
all ten frontend packages green, `go vet ./...` clean, `gofmt -l` empty.

---

## Ghost text is not in the buffer model — the pattern to reuse

This is the thing to carry forward, and it is why Done-when 3c cannot fail by
omission.

**Ghost text is not in the buffer model at all.** It is a `[]string` on the
workspace plus an anchor line, consumed by the renderer as extra rows.
`Buffer` never sees it, `Splice` is never called, `Version()` never moves, no
`editRecord` is written, the document is not marked dirty, and the undo
history does not know it happened. `esc` mid-stream drops a slice of strings;
there is nothing to roll back.

The alternative — splice tokens in as they arrive and splice them back out on
cancel — was not built, and would have been wrong in ways this codebase has
already paid for: every arriving token would be an `editRecord`, so `ctrl+z`
would walk the stream backwards one token at a time (F13's "one undo
operation", inverted); autosave arms from `Version()`, which would move; and
a crash mid-stream would leave model output in a file nobody accepted.

**Reuse this for anything transient the editor must show** — F17's preview, an
inline annotation: state beside the buffer, rows injected at render time, keyed
to a line the renderer already has, and registered as a block in
`viewrows.go`'s index so it scrolls and hit-tests like everything else.

---

## Bugs found during the build

Beyond the four root causes above.

1. **A real disk write on apply-then-undo.** `ctrl+z` after an apply armed
   autosave, which wrote the file with identical bytes and a new mtime —
   Done-when 5, failed. Fixed with a sha256 `diskHash` baseline and
   `matchesDisk()`, plus an autosave early-return. The first fix did not hold
   because the write was coming from somewhere else as well (the chord leak,
   above). **Two causes, one symptom.**
2. **A Chat session leak.** My first hypothesis — a rendering filter — was
   wrong; `entryFor` already matches on corr_id/task_id. The real causes were
   two: Chat **adopted** the Editor's lazily-created session, and
   `restoreDraft` clobbered the prefill. **Two bugs where one was assumed.**
3. **The backgrounded diff card never opened.** Two faults:
   `WantsBackgroundReplies` was false at `TaskDone`, and
   `deliverBackgroundReply` dropped the returned cmd.
4. **The card then opened INSTANTLY in Chat.** My own comment described a
   mechanism that did not exist — Root tells only the *incoming* workspace it
   is on screen. Fixed by adding `WorkspaceDeactivatedMsg`, and verified by
   reverting each fix and watching the tests go red.
5. **`LogHas "WriteProjectFile"` could never match** — a successful write
   logged nothing, so the gate's own write-detection was an assertion that
   could not fail. A `save.go` Info line was added.
6. **Palette binding truncation.** Widening the popup did not help; the cap is
   on the column. Fixed with `NoteW: longestBinding(items)`.
7. **`leader → x` and `leader → e` were both already taken** — `e` by
   `workspace.switch.editor`, which `registerWorkspaces` derives from
   `WorkspaceDef.LeaderKey` and is therefore invisible to a grep for
   `Leader: "`. Settled on `a` (explain) and `f` (fill).
8. **Function-key labels disagreed with each other.** `LeaderLabel` was
   hand-typed with "F2" while `ChordLabel()` passed bubbletea's own
   lower-case key names straight through, so one help-screen row read
   `f3 / ctrl+q` beside `F2 / ctrl+w q`, and the Editor's empty state printed
   `f5 f6 f7`. `actions.KeyLabel` is now the only place that decides;
   `LeaderLabel` is derived from the two keys so it cannot drift from them.
   Modifier chords are deliberately untouched — `ctrl+p` is lower-case
   everywhere in this app by choice.
9. **The Editor's status bar advertised the broken half of a dual binding** —
   `ctrl+w w workspace`. It is `F2 w workspace` now, built from
   `actions.Leader`. Primary key only, not `LeaderLabel`'s both: `fitHints`
   drops WHOLE segments when the row is narrow, and what would disappear is
   the only on-screen pointer to the workspace switcher. The help screen and
   the palette still print both. `chat\stream.go`'s first-run
   `providerMissingReason` had the same bug and is fixed the same way.

### Tooling hazards worth knowing about

Heredocs and scripted patches repeatedly ate one level of backslash escaping,
corrupting Go rune and string literals (`'\n'` becoming a real newline) and
once truncating `prompt.go` to 67 lines. Mitigations that worked: numeric
literals (`0x0a`), `chr(92)` instead of a literal backslash, and the Write
tool for whole files.

---

## Deliberate limitations

- **`send to chat` is a PREFILL, not a send.** The panel's text is appended
  to Chat's draft and the user presses enter. Tracked, not hidden.
  `PrefillDraft` appends rather than replaces because `setText` clears undo.
- **A ghost mid-stream is anchored by line only, not by document.** Switching
  tabs mid-stream would draw it at the same line number in the other file.
  Noted here rather than fixed; nothing in the gate reaches it, because the
  invocation keeps the keyboard.
- **The reveal does not show a tall ghost.** Arrowing onto the line below the
  anchor scrolls past the whole region in one jump. That is correct under the
  unified model — the region occupies real screen space — and it is what the
  pane does once the edit is applied.
- **The explain panel has no selection of its own**, so `ctrl+c` there copies
  the code selection, not the explanation.
- **The backend ignores `EditorContext.imports`.** The field is sent because
  B11's shape has it; nothing reads it yet. Recorded in `context.go`.

---

## Backlog this episode leaves behind

**Open polish items go to `../../tasks/frontend/F16-polish.md`**, which is
where small wanted-but-not-scheduled work lives, and which this episode
appended two entries to:

1. **Ollama fill-mode JSON reliability** — the model echoes the literal
   `<path>` placeholder out of `_render_editor_context`'s own schema line,
   which B24 then correctly rejects as `file_mismatch`. Measured 2/5 and 0/5
   across two arms. Backend-only, one-line prompt change, **not done**.
2. **The Editor's `f7` close-tab binding wants a better key**, and Alt+w is
   ruled out by this episode's own AltGr finding.

### Test debt — F15's own, and it is not F15's to fix

**`backend\tests\manual_b28_demo_mode.py` step #8 asserts that a
`FillProposal` event does NOT exist on the wire** — verbatim, *"no
FillProposal event exists on the wire — none was emitted, by construction"*.
It does exist, so that step fails.

**This predates F15 entirely.** The test file is at its committed state from
B28 (`fa6e88c`; `git status --porcelain` on it is empty), and `FillProposal`
was added later by **B24** (`ab1743d`), which never came back to update B28's
assertion. It has been failing since B24. **The event is correct — it is what
F15 consumes — and the assertion is what is stale.**

It was deliberately **not** fixed here: B28's verification script is not F15's
scope, and the choice between the two repairs should be made on purpose rather
than in passing. It is tracked as its own parking-lot entry in
`../../tasks/backend/INDEX.md`. The more useful repair is to rewrite the
assertion as a **positive** that checks `FillProposal` arrives *after*
`TaskDone` — B24's actual invariant, and the one thing the current wording has
backwards.

---

## Handoff

### Session 19b — F9d (apply-time guard order)

F15 adds the **first caller of `WriteProjectFile` from an agent-applied
diff**, which is what makes F9a's hazard-1 guard order reachable at all. Read
F15's real save path (`editor\save.go`, `editor\diffcard.go`'s two-phase
apply) rather than the task doc's plan: the apply deliberately **does not**
arm autosave, and the card closes itself on success, so the write happens only
when the user presses `ctrl+s` afterwards.

B36 fixed the **primitive** — `CheckFileStale` is session-scoped via
`_attachment_scope` — not the call site.

### Anyone touching the Editor's viewport

**`viewrows.go` is the contract.** Add a new injected surface by adding a
block to `viewIndexAt`, not by emitting rows from the renderer and teaching
the hit-test about them separately. The test that matters is
`TestTheRowIndexAndItsInverseAgree`: `rowOf` and `at` are exact inverses and
`at` partitions the sequence with no gaps.

`unifiedscroll_test.go` records which mechanism each test actually guards,
including one that does **not** reproduce the original bug and cannot — noted
there rather than implied, because a test whose failure mode has never been
observed is a test nobody should trust.

### Anyone adding a key binding

Ask `actions.IsLeader`, never compare against `Leader`. Name keys through
`actions.KeyLabel` / `ChordLabel()` / `LeaderLabel`, never by typing them
into a string. And read `actions.Leader`'s own doc comment first: it carries
the bubbletea Windows mechanism, why there are two leader keys, and why
Alt+\<letter\> is not available — all three of which have now been
rediscovered more than once.

### Demo-mode pacing, for anyone verifying streaming behaviour by eye

`POINT_DEMO_LONG_FILL=1` swaps in the 46-line fixture.
`POINT_DEMO_STREAM_DELAY_MS` is **per chunk** and **replaces** the fixture's
own delay; the long fixture is 604 token-sized pieces with no baked-in delay,
so `10` gives a smooth ~9s typewriter and `40` gives ~28s (both measured,
including the platform timer's overshoot). `POINT_DEMO_CARD_DELAY_MS` holds
the DONE chunk back, which holds back `TaskDone` and therefore the proposal —
so it is the gap between *the ghost stopped growing* and *the card opened*,
which is the only window in which a finished ghost can be inspected with no
card over it. Both are documented beside `_LONG_FILL_ENV` and commented out
in `gate_f15.ps1`.
