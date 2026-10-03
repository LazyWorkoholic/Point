# Episode F10b — Settings: Behaviour Overrides + `doctor` in About ✅

**The Behaviour tab (five rows wired to B26's `SetBehaviourOverride`/
`BehaviourOverrideSet`) and the About tab (one `Run doctor` action calling
`GetDoctorReport`, rendered in a scrollable card) both ship. Four rounds
of live verification after the build found one real, non-cosmetic
correctness bug (a provider's specific model choice silently reverting
on re-expand), one finding that reclassified a reported "critical bug"
as a pre-existing, deliberately-scoped limitation rather than a defect
(`chat.queue_depth`'s local-queue ceiling mismatch), one verification-
accuracy correction on this episode's own first write-up of that finding
(the claimed "observable via `@research`" alternative was never checked
and was wrong), and three cosmetic-but-real rendering bugs in the one new
UI surface this episode added to flag that mismatch honestly. All are
detailed below, each with the actual evidence, not a description of what
should be true.**

> Confirmed by Aryan across several live rounds against the real running
> app (not only the direct-`Runtime` scripts): the Behaviour tab's five
> rows, About's doctor card, the model-reversion fix, and — on the final
> round — all three UI rendering bugs fixed and reverified at real popup
> width with real override values (25, 100), full suite including
> `-race` clean. Per CLAUDE.md's hard rule, every scratch verification in
> this episode used the `APPDATA`/`LOCALAPPDATA` env-var-override
> pattern; the real profile was never a target of anything.

---

## Where the code went

```
backend\
└── (unchanged — this episode is the Go side only; B26 shipped every
    wire event it uses)

frontend\internal\
├── app\
│   ├── behaviour.go      NEW — the Behaviour tab: behaviourKeys (the
│   │                     five rows, general cap only, no per-provider
│   │                     family), behaviourRowDecor, onBehaviourSelect
│   │                     (bool toggle / opens the numeric prompt),
│   │                     sendBehaviourOverride/sendBehaviourClear,
│   │                     onBehaviourOverrideSet (applies the backend's
│   │                     full echoed map), replayBehaviourOverrides
│   │                     (every protocol.ConnectedMsg), behaviourInertKeys
│   │                     (chat.queue_depth's own honest flag)
│   ├── doctor.go         NEW — the About tab's one action, formatDoctorReport
│   │                     (mirrors cli.py's own section order), DoctorCard
│   │                     (a small scrollable overlay reusing resultcard.go's
│   │                     wrapParagraphs, not the ResultCard type itself)
│   ├── credential.go     ~ generalized with an unmasked/behaviourKey mode
│   │                     for the three numeric rows, instead of a second
│   │                     input overlay
│   ├── settings.go       ~ behaviourTabIndex/aboutTabIndex added to Tabs;
│   │                     "behaviour:"/"about:" prefix cases in
│   │                     settingsDecor/onSettingsSelect; a ctrl+x "clear
│   │                     override" ItemAction; settingsHint extended for
│   │                     the inert row's own explanation
│   ├── root.go           ~ r.behaviourOverrides seeded from state.json at
│   │                     New(), replayed on every ConnectedMsg; the
│   │                     selectProvider fix (see Bugs, below)
│   │                     + submitBehaviourValueMsg case
│   ├── behaviour_test.go NEW
│   └── doctor_test.go    NEW
├── chat\
│   ├── providers.go      + applyBehaviourOverrides (full-replace persist,
│   │                     not a patch — BehaviourOverrideSet's own reply
│   │                     already IS the complete resulting map)
│   ├── protocol.go       + the app.BehaviourOverridesSetMsg case
│   └── providers_test.go + the replace-not-merge test
├── config\state.go       + BehaviourOverrides map[string]any
└── protocol\types.go     + CmdSetBehaviourOverride/CmdGetDoctorReport,
                          EvBehaviourOverrideSet/EvDoctorReport

backend\tests\
└── manual_b26_behaviour_overrides.py  ~ EXTENDED (sections #1b–#1e) —
    see "The queue_depth investigation" below. Backend code itself:
    unchanged; this is regression coverage added while re-verifying B26's
    own claims, in the same file B26 shipped, under its own established
    convention.

design\tasks\backend\
└── B27-doctor-resolved-behaviour-values.md  NEW (task doc only, not
    started) — see "What the next episode inherits".
```

Six existing test files gained nothing new this episode beyond what's
listed above; `settings_test.go` gained two tests for the model-reversion
fix (below).

---

## The narrowed scope, as built

- **Behaviour tab.** Five rows — `chat.queue_depth`, `limits.max_concurrent_tasks`,
  `project.allow_outside_project`, `index.respect_gitignore`,
  `research.stale_after_days` — each showing `default` or
  `overridden → X` (never a guessed `config.toml` value — Decision 2,
  below). Bool rows toggle on Enter; the three numeric rows open an
  unmasked numeric prompt (`CredentialPrompt`'s generalized mode); a
  `ctrl+x` action clears whichever row is selected and currently
  overridden.
- **About tab.** One row, "Run doctor," opens a scrollable card with the
  backend's `GetDoctorReport` payload formatted in `cli.py`'s own section
  order (not byte-identical text — that guarantee stays `cli.py`'s own —
  but the same facts, same grouping).
- **Persistence and replay (Decision 1).** Every confirmed override is
  persisted to `state.json` (the same single-writer rule `Provider`/`UI`
  already follow) and replayed via `SetBehaviourOverride` on **every**
  `protocol.ConnectedMsg` — first launch and any later reconnect,
  including one caused by the backend restarting while the frontend
  keeps running. Because the replay loop walks the whole
  `r.behaviourOverrides` map, a reconnect with multiple active overrides
  replays all of them in one pass, not just the most recently set key —
  proven directly (Done-when #9, two overrides of different types, both
  survive both a backend-only restart and a separate frontend restart).

---

## Decisions

### 1. Replay on every connect, not shown as "reverted"

See the task doc's own Decision 1 — unchanged from the original design,
confirmed working exactly as specified across every live round.

### 2. Override state only, never a guessed `config.toml` value

See the task doc's own Decision 2. Holds exactly as designed; this is
also the gap B27 (below) exists to close, on the backend side only.

### 3. Only the general concurrency cap, not a row per provider

See the task doc's own Decision 3. Unexercised by this episode's
verification — no provider-cap-specific bug surfaced.

### 4. `chat.queue_depth` — the investigation, corrected twice, now accurate

This is the episode's most expensive finding, and it went through three
distinct stages worth recording in order, because the middle stage was
wrong and the record should show the correction, not just the final
answer.

**Stage 1 — reported as a critical bug.** Live testing at `queue_depth=2`
and `queue_depth=5` showed the refusal point stuck at the same message
regardless of the configured depth — exactly the shape of "the override
has no effect."

**Stage 2 — root-caused, direction correct, one claim inside it wrong.**
Traced and reproduced: `turn_queue_depth()` (`backend\runtime\scheduler.py:269`)
does call `config_value_live()` fresh on every check, and there is only
one `_OVERRIDES` dict (`appconfig_overrides.py`), imported identically
everywhere — no dual-instance bug, the class this project has hit before
(`DiagnosticsMsg.Seq`, `taskCardWasVisible`). The real cause: the
frontend's own F5-era local compose-ahead queue (`chat\stream.go`'s
`w.queued`, one slot, "a queue that cannot hold two cannot be indexed
wrong") was written when the backend's depth was a hardcoded `1`, and
nothing connects it to the now-configurable value. Every plain-chat send
path — `onSend` (`stream.go:365`), `fireQueued` (`stream.go:716-722`),
`retryFocused` (`stream.go:814-828`) — shares the identical
`w.activeTurn() != nil` gate, so a session's turn lane never holds more
than one occupant from this client, at any configured depth. **The
write-up at this stage additionally claimed** the override's real effect
was "observable through concurrent backend-driven tasks (e.g. multiple
`@research` sends)" as the honest alternative to plain-chat testing.
**That claim was never checked and was wrong.**

**Stage 3 — challenged directly, re-verified with the same file:line
rigor as the original investigation, corrected.**

- `@research` (and every bounded-loop agent) is **exempt from
  `_check_turn_queue` entirely**: `runtime.py:894`
  (`is_bounded_loop = agent.max_steps > 1`) and `runtime.py:921-922`
  (`if not is_bounded_loop: self._check_turn_queue(session.id)`) — the
  one and only call site of `turn_queue_depth()` never runs for it.
- `runtime.py:931` (`inline=not is_bounded_loop`) and
  `agents\executor.py:525-529` confirm `@research` never even takes a
  turn-lane slot (`if inline: lanes.append(turn_lane(session.id))` —
  `inline` is `False` for it). It takes only a provider-lane slot.
- `@research` is therefore a real, observable path for
  **`limits.max_concurrent_tasks`** — a genuinely different key behind a
  genuinely different admission check — and proves nothing at all about
  `chat.queue_depth`, which it never consults.

**The corrected, final finding:** `chat.queue_depth` currently has **no
observable effect anywhere in the shipped application, at any
configured value.** Its only proof of correctness is
`backend\tests\manual_b26_behaviour_overrides.py`'s `#1b` — a direct
`Runtime.receive()` call, deliberately bypassing the frontend's own gate.
The only thing this Behaviour-tab control currently does, end to end, is
change a number `GetDoctorReport` echoes back.

**Systemic check, requested once the queue_depth finding landed** (is
the shared `config_value_live()`/override-store mechanism broken for all
five keys, or specific to this one): all three other admission-governing
keys were tested live, at two different values each, same rigor —

- `limits.max_concurrent_tasks` — cap=1 → a second session's send
  `QUEUED` behind the first; cap=3 → admitted immediately. (First
  attempt used `echo`, which `executor.py` deliberately exempts from the
  provider lane — looked exactly like the queue_depth bug, was that same
  documented exemption. Corrected to `mock`.)
- `research.stale_after_days` — a synthetic 10-day-old completed task is
  flagged stale at threshold 5, not flagged at threshold 15. Clean on the
  first attempt.
- `project.allow_outside_project` — an outside-project `AttachContext` is
  refused by default, succeeds with the override `true`, refused again
  after clearing. Clean on the first attempt, and separately reconfirmed
  live in the running app (see Manual Test Log).

**Conclusion: not a systemic bug.** `config_value_live()` and the
override store are correct for all five keys. `chat.queue_depth` is the
one key with no reachable path to exercise its own correct backend
mechanism, for a reason specific to this frontend's own, older, unrelated
queueing convenience — not a flaw in B26's override layer.

**Decided explicitly, not fixed this episode** (the user's own call,
after being offered three options): leave the local queue at capacity 1.
Two real fixes were identified and declined — resizing the local queue
to the live depth (needs new plumbing `chat.Workspace` does not have
today, plus turning `w.queued` into a bounded FIFO), or removing the
local queue entirely in favor of the backend's own already-correct
admission (architecturally cleaner, but changes `ctrl+e`'s contract from
"edit a not-yet-sent message" to "cancel an already-queued backend
task"). Both remain open for whoever picks this up; the honest-labelling
fix below (the inert-row marker) was built instead, this episode, so the
gap is visible rather than silent.

**What this means for verification going forward:** do not test
`chat.queue_depth` by typing several messages into the query input, at
any depth, and do not point at `@research` (or any agent send) as an
alternative — neither will ever show this key doing anything, because
neither reaches the code that reads it.

---

## Bugs found during verification

Four rounds, five findings total — one real correctness bug, one
investigation correction (above), and three rendering bugs in this
episode's own new UI surface.

### 1. Re-expanding an already-active provider silently reverted its specific model choice — real, not cosmetic

Reported from a live sequence: after a restart, the Status bar correctly
showed the originally-selected, non-alphabetically-first model; the
moment the provider row was re-expanded (Enter, just to look at the model
list), the alphabetically-first model became active and the Status bar
updated to match — with no further user action.

Traced to `selectProvider` (`settings.go`): Enter on a provider row was
not a neutral "expand" action — it was the identical code path as
"choose this provider fresh," which unconditionally re-ran
`applyProviderChoice`'s quick pick (always `models[0]`) and persisted it
via a real `ProviderDefaultSetMsg`. Confirmed with a direct before/after
test, not assumed:

```
BEFORE 're-expand': qwen2.5-coder:3b
AFTER (Enter on provider row): llama3.2:3b          <- reverted
ProviderDefaultSetMsg emitted: Model="llama3.2:3b"  <- would persist to state.json
```

**Fixed:** `selectProvider` now checks whether the provider being
selected is already the active default. If so, it only expands
(`settingsFetchModels` if the models genuinely aren't cached yet,
otherwise nothing) and returns `nil` — no command, no message, nothing to
persist. Choosing a *different* provider still runs the quick pick
exactly as before. Two tests pin both halves —
`TestReExpandingTheActiveProviderDoesNotRevertASpecificModelChoice`
(asserts the command is `nil` outright, not merely "if it fires, it
isn't this message type" — the stronger claim the fix actually earns) and
`TestSelectingADifferentProviderStillAppliesTheQuickPick`. Confirmed to
fail without the fix and pass with it, both ways, not assumed from the
diff alone.

### 2. Three rendering bugs in the Behaviour tab's own inert-row marker

Found live, in the UI feature built to flag finding #4 (Decision 4)
honestly rather than silently. All three were confirmed **by rendering
the real `Popup.View()` at the real floor width** — the first attempt at
proving no overflow used a mental/rune-count check against an assumed
24-cell ceiling, which was itself wrong (see 2b) and let the bug ship
past it.

**2a. Icon-to-label spacing.** The warning icon (`⚠`, U+26A0) rendered
with no visible gap before the label, unlike every other row's `●`/`○`.
Go's own render logic (`popup.go`'s `renderRow`) inserts an identical
single space after every non-empty icon regardless of which one — traced
and confirmed correct in isolation — so the mismatch is a terminal/font
rendering quirk specific to that glyph, the same class of bug this
project already paid for once (`⋯` rendering as three literal dots,
replaced with `◌`, F9a). **Fixed** by swapping to `!` — plain ASCII, no
width ambiguity, same `theme.Warning` role.

**2b. Note-column overflow at a two-digit override value.** The row's
note carried a `· inert` suffix (`"overridden → 15 · inert"`), checked
against a mentally-counted 24-cell ceiling and believed to fit. Rendering
it for real, at the popup's actual floor width (`MinW: 74`), showed it
truncated to `"...· ine…"`. The real cause: `popup.go`'s `rowWidths`
caps the note column at `min(24, avail/3)` — at this popup's real width,
`avail/3` computes to **23**, not 24, so the mental ceiling was checking
against the wrong number entirely, not merely applying it carelessly.
**Fixed** by removing the suffix: the note now reads identically to a
working row's (`default` / `overridden → X`), relying on the icon alone
for the "this is different" signal — which eliminates the whole
width-fragility class rather than hunting for a suffix short enough to
survive every possible width.

**2c. Hint-line truncation.** The explanatory hint shown when the row is
selected ("...see F10b-settings-behaviour-doctor.md's Decision 4")
truncated mid-filename at the real popup width. **Fixed** by shortening
it to `"not yet enforced for plain chat sends (see F10b's Decision 4)"`,
confirmed to render in full, untruncated, at depth values 0, 25, and 100.

**The test that was supposed to catch this, and why it didn't:** the
first version of this coverage asserted `len([]rune(note)) <= 24` —
which passed, because it counted runes against an assumed ceiling instead
of rendering the real string at the real width against the real,
width-dependent budget. Replaced with
`TestBehaviourInertRowNeverOverflowsAtRealWidth`, which builds the actual
`Popup`, calls `.View()` at `MinW: 74`, and asserts no line contains the
truncation ellipsis — confirmed this is the test that would have caught
2b, and does now. (`TestSettingsHintExplainsTheInertRowWhenSelected`,
covering the hint firing only on this specific row, already existed under
that name from the prior round — it simply doesn't match a
`-run "TestBehaviour"` filter, which is why it initially read as
missing.)

---

## Manual Test Log

Across several live rounds, confirmed by Aryan against the real running
app (backend + frontend together, scratch `APPDATA`/`LOCALAPPDATA`
profile):

1. Behaviour tab: all five rows read `default` on open; setting each
   (bool toggle, numeric prompt) updates in place on the
   `BehaviourOverrideSet` reply; clearing returns to `default`; a
   non-numeric entry is rejected before any request is sent.
2. About → Run doctor opens a card showing the live overrides.
3. Backend-restart and frontend-restart persistence, each with **two**
   simultaneous overrides of different types (`chat.queue_depth`,
   `project.allow_outside_project`) — both survive both restart paths,
   proving the replay is the full map, not just the latest key.
4. `config.toml` byte-identical before/after every Behaviour change.
5. `project.allow_outside_project`, live: an outside-project attach
   refused by default, succeeds with the override `true`, refused again
   after clearing — **confirmed working, no action needed**. (A separate
   `.docx` binary refusal seen during this same test is B12's own
   pre-existing, correct guard, unrelated to this override.)
6. The model-reversion fix: re-expanding an already-active provider's row
   leaves its specific model untouched, confirmed live, several times.
7. All three UI rendering bugs: confirmed fixed at real popup width, with
   real override values (25, 100) and the hint line visible — no
   truncation, correct icon spacing, matching a working row's shape.
8. Full `go test ./frontend/...` suite, including `-race`, clean after
   every round of fixes in this episode.

---

## What the next episode inherits

- **`design/tasks/backend/B27-doctor-resolved-behaviour-values.md`** (NEW,
  not started) — the direct consequence of Decision 2/4: nothing on the
  wire today can say what a Behaviour key's `config.toml`/fallback value
  actually is, independent of any override. B27 adds exactly one field
  (`behaviour_resolved`) to `GetDoctorReport`'s existing payload — no new
  wire event, no CLI-output change, backend only. The frontend half
  (showing the real number in the Behaviour tab) is its own later pass,
  deliberately not scoped there either.
- **The Keys tab** remains unbuilt and unscoped — no episode owns it yet.
- **Demo mode (F10c)** is unaffected.
- **`chat.queue_depth`'s local-queue mismatch** is a real, open,
  deliberately-deferred gap (Decision 4) — whoever picks it up should
  read that section's two declined fix shapes before choosing a third.
- **`CredentialPrompt`'s unmasked-mode generalization** worked cleanly
  across this episode's own numeric rows and the live verification
  rounds — a future numeric/text Settings control (or F11's memory
  popup) can reuse it with no further generalization needed.
- **`behaviourInertKeys`** (`behaviour.go`) is a one-entry map today
  (`chat.queue_depth`). If a future key ever ships with the same
  "real override, no reachable effect yet" shape, add it there rather
  than inventing a second mechanism.
