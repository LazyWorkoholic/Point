# F10b — Settings: Behaviour Overrides + `doctor` in About

**Phase:** P1 · **Batchable:** no · **Depends on:** F10 (Providers + Appearance
tabs, shipped) · **Backend:** B26 (shipped — `SetBehaviourOverride`/
`BehaviourOverrideSet`, `GetDoctorReport`/`DoctorReport`, both verified)

**Wires the Behaviour tab's five controls to `SetBehaviourOverride`/
`BehaviourOverrideSet`, and adds one action in About — `point doctor` — that
calls `GetDoctorReport` and renders the payload in a scrollable card. Nothing
else. The Providers and Appearance tabs F10 already shipped are not touched;
the Settings popup's tab-dispatch-by-prefix structure is not redesigned, only
extended the way F10's own handoff already described for exactly this case.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/frontend/F10b-settings-behaviour-doctor.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

---

## Why this episode exists, and why it can be scoped now

F10 split itself in three (`design/tasks/frontend/INDEX.md` rows 39–41)
because its Behaviour tab and About tab's `doctor` action both needed backend
surface that did not exist yet. That surface is now real: B26 shipped
`appconfig_overrides.py`'s in-memory layer, `SetBehaviourOverride`/
`BehaviourOverrideSet` (echoes the full override map back on every change),
and `GetDoctorReport`/`DoctorReport` (the same facts `cli.py doctor` prints,
as a dict, including `behaviour_overrides`). Both are backend-verified
(`design/completed/backend/B26-behaviour-overrides-doctor-wire.md`) — this
episode is the Go side only: no backend file changes.

Two things B26's own handoff explicitly left for this episode to decide,
because they are frontend calls, not backend facts:

1. **What happens to an override across a backend restart**, since overrides
   are deliberately process-lifetime-only in the backend (B26 Do-NOT #3) and
   `state.json` is Go-only — the backend never reads it.
2. **How the Behaviour tab represents "current value"** when it has no way
   to read `config.toml`'s actual value for any of these five keys. Go's own
   `config.Load()` deliberately parses only the `[ui]` table
   (`frontend\internal\config\config.go`'s own doc comment) — it was never
   taught to parse `chat.queue_depth` or the other four. And B26 deliberately
   added **no** `GetBehaviourOverrides` pull event (its own §1b) — the only
   facts the wire will ever hand this process are "an override is active,
   and here is its value," never "here is what `config.toml` has it set to."

Both are decided below, not left open — see **Decisions**.

---

## Decisions

### 1. Overrides are re-sent from `state.json` on every successful connection, not shown as "reverted"

Persist the override map Settings has set this session into `state.json` —
a new field alongside `Provider`/`UI`
(`frontend\internal\config\state.go`), written the same atomic
temp-file-then-rename way `SaveState` already writes every other field. On
every point the frontend has a live backend connection — first launch **and**
any later reconnect, including one caused by the backend process itself
restarting while Go keeps running — replay every persisted override via
`SetBehaviourOverride`, one call per key, using the exact
`awaitingConnection`/`retrySettingsIfAwaiting` mechanism `settings.go` already
uses for the Providers fetch (F10's own first-launch-hang fix). This is a
**connection-lifecycle hook**, not a Settings-popup-open hook — the backend
can restart while Settings is closed, and the override still needs to be
back in effect before the next turn that depends on it runs.

Rejected alternative: showing the user "this override reverted, re-apply it."
`state.json` already exists, already persists exactly this class of
UI-chosen-value-the-backend-doesn't-own (`Provider`, `UI`), and the connection
retry plumbing already exists for the identical "request attempted before
the backend was ready" shape. Silently restoring costs nothing additional and
matches what both existing overrides already do; asking the user to notice
and re-click something they set once is the worse default.

### 2. The Behaviour tab shows override state, never a fabricated "current value"

Each row shows exactly one of two states:

- **`default`** — no override is active. The tab does **not** claim to know
  or display what `config.toml` actually has the key set to, because this
  process cannot read it and B26 deliberately did not add a way to ask.
- **`overridden → <value>`** — an override is active, showing the value this
  process itself set (and which `BehaviourOverrideSet`'s reply already
  confirmed), never a value read back from anywhere else.

This is the same discipline CLAUDE.md's hard rules already apply to
performance claims, turned to configuration: never state a fact this process
has not actually observed. It also means the row's initial state on a freshly
opened tab (before any override has been set *this session*) is always
`default` — correct, since `state.json`'s replay (Decision 1) either already
ran and would have flipped it to `overridden`, or there was nothing to
replay.

### 3. Only the general concurrency cap is exposed, not a row per provider

B26's `ALLOWED_KEYS` recognizes `limits.max_concurrent_tasks` plus the whole
`limits.<provider>.max_concurrent_tasks` family by prefix/suffix, but F10's
own Behaviour bullet says only "concurrency caps," naming no providers. This
episode wires the one general key. A row per registered provider is a real
feature (and the backend already supports it) but it multiplies this tab's
row count by however many providers are configured, which is new UI surface
this episode's scope — "wiring the controls," not "designing the provider-cap
sub-list" — does not ask for. Flagged here rather than silently dropped, in
case a later episode wants it.

### 4. `chat.queue_depth` has NO observable effect anywhere in the shipped application, at any configured value, today

Found during live verification, and the first write-up of this finding
was wrong in a way worth recording precisely rather than quietly
correcting: setting `chat.queue_depth` to 2 or 5 and then typing a third
message into the query input while a second sits composed produces a
`queue_full`-looking refusal on every other send, regardless of the
configured depth. Traced to `chat\stream.go`'s F5-era local queue
(`w.queued *queuedSend`, one slot, "a queue that cannot hold two cannot
be indexed wrong") — written when the backend's own depth was a
hardcoded `1`, so one local slot matched reality at the time. B26 made
the backend value configurable; nothing connects it to this local
queue's own capacity, which has stayed exactly 1 ever since. Checked
every plain-chat send site in the Go frontend — `onSend`
(`stream.go:365`), `fireQueued` (`stream.go:716-722`),
`retryFocused` (`stream.go:814-828`) — all three share the same
`w.activeTurn() != nil` gate, and `w.queued` is a pointer, never a
collection. The structural result: for any one session, the shipped
frontend never has more than one turn-lane occupant in flight at a time,
for any plain-chat send, at any configured depth. `_check_turn_queue`'s
ceiling (`1 + depth`, `runtime.py:994`) is being checked against an
occupancy that can never exceed 1 from this client, so the check never
has a reason to refuse — at depth 0 or depth 50.

**The first version of this section claimed the override's "real effect
is only observable through concurrent backend tasks (e.g. multiple
`@research` sends)" — that claim was never verified and is wrong.**
Challenged directly and checked with the same file:line rigor as the
original investigation:

- `@research` (and every bounded-loop agent) is **exempt from
  `_check_turn_queue` entirely**: `runtime.py:894`
  (`is_bounded_loop = agent.max_steps > 1`) and `runtime.py:921-922`
  (`if not is_bounded_loop: self._check_turn_queue(session.id)`) — the
  one and only call site of `turn_queue_depth()` (which reads
  `chat.queue_depth`) never runs for it.
- `runtime.py:931` (`inline=not is_bounded_loop`) and
  `agents\executor.py:525-529` confirm `@research` never even takes a
  turn-lane slot (`if inline: lanes.append(turn_lane(session.id))` —
  `inline` is `False`). It takes only a provider-lane slot.
- So `@research` is a real, shipped-app-reachable observable path for
  **`limits.max_concurrent_tasks`** (a genuinely different key, governed
  by a genuinely different admission check) — and proves nothing at all
  about `chat.queue_depth`, which it never consults.

**Stated plainly, as asked: this Behaviour-tab control currently has NO
observable effect in the shipped application, at any value, until
Option 1 or Option 2 below is implemented.** Its only proof of
correctness today is `backend\tests\manual_b26_behaviour_overrides.py`'s
`#1b` — a direct `Runtime.receive()` call, deliberately bypassing the Go
frontend's own gate, which is the only reason that script can observe
more than one waiting item in a turn lane at all.

**Decided explicitly, not fixed this episode.** `chat\stream_test.go`'s
`TestInputFullyRecoversAfterDiscard` pins the local queue's actual
behavior (capacity 1, fully recoverable via esc, functional afterward —
the lock/unlock cycle itself was never broken, which is a separate fact
from this key having no reachable effect) so neither is mistaken for a
regression later. Two real fixes were considered and declined for this
episode — both would make `chat.queue_depth` reachable, not just
differently explained:

- **Resize the local queue to match the live depth.** Keeps F5's
  "compose ahead, editable via ctrl+e, discardable via esc" contract,
  scaled to N messages. Needs new plumbing `chat.Workspace` does not have
  today — the live override value currently reaches only `Root`
  (`r.behaviourOverrides`), never the workspace — plus turning `w.queued`
  from a pointer into a bounded FIFO, touching `fireQueued`/
  `editPending`/`discardPending` and the queued-message render.
- **Remove the local queue; let every send hit the backend's own
  (already depth-aware) admission directly.** Smaller diff, architecturally
  cleaner (the backend's queued turn is a real, visible, cancellable task;
  the local one is invisible until it fires), but changes F5's contract:
  ctrl+e currently pulls a *not-yet-sent* message back to edit; with
  nothing held back locally, every message is already committed to the
  backend the instant Enter is pressed, so ctrl+e/esc would need
  repurposing (cancel the queued backend task) rather than editing.

**What this means for verification going forward:** do not test
`chat.queue_depth` by typing several messages into the query input, at
any depth, and do not point at `@research` (or any agent send) as an
alternative — neither will ever show this key doing anything, because
neither reaches the code that reads it. The only thing this control
currently does is change a number `GetDoctorReport` echoes back.

---

## Build

### 1. The Behaviour tab

New tab in `newSettingsPopup`'s `Tabs` slice
(`frontend\internal\app\settings.go`), `behaviourTabIndex` alongside the
existing `providersTabIndex`/`appearanceTabIndex` constants — purely
additive, per F10's own handoff ("Give the new tab's items their own ID
prefix... every existing case stays untouched"). New file
`frontend\internal\app\behaviour.go`, built the same shape as
`appearance.go`: a static item list from `newBehaviourTabItems`, `Decor`
computed fresh every render from Root's own live state (never baked into an
item at build time — F9a's render-time rule, which F10's own handoff flagged
Settings as the next consumer of).

Five rows, one `"behaviour:"`-prefixed item id per key:

| Row | Key | Value shape | Control |
|---|---|---|---|
| Queue depth | `chat.queue_depth` | int ≥ 0 | numeric entry (see below) |
| Concurrency cap | `limits.max_concurrent_tasks` | int ≥ 1 | numeric entry |
| Allow outside project | `project.allow_outside_project` | bool | toggle on select |
| Respect .gitignore | `index.respect_gitignore` | bool | toggle on select |
| Stale after (days) | `research.stale_after_days` | int ≥ 1 | numeric entry |

**Bool rows** toggle immediately on selection and send
`SetBehaviourOverride{key, value: !current}` — the identical shape
`appearance.go`'s `toggleMouse` already uses for a live, backend-free
toggle; here the send is the only new part.

**Int rows** need a value typed in, and this codebase has exactly one
text-entry mechanism today: `CredentialPrompt`
(`frontend\internal\app\credential.go`), currently hardcoded to mask input.
Generalize it with an `unmasked`/plain-text mode (a one-field addition, not a
new widget) rather than building a second input overlay — reused for all
three numeric rows, with its validation rejecting non-numeric input locally
before a request is ever sent (the backend's own `set_override` still
validates shape server-side regardless; client-side rejection is only to
avoid a round trip for an obviously-bad value, e.g. the letter "a").

**A fourth per-row action: clear.** Each row's hint includes a clear binding
(mirroring `d`/`esc` returning a card to the tray in F9a — a short, discrete
key, not a submenu) that sends `SetBehaviourOverride{key}` with no `value`
field, per B26 §1b's clear shape. Clearing an override that was never set is
a no-op the backend already handles (clearing something absent).

### 2. Wire plumbing

`frontend\internal\protocol\types.go`:

```go
CmdSetBehaviourOverride = "SetBehaviourOverride"
CmdGetDoctorReport      = "GetDoctorReport"
```
```go
EvBehaviourOverrideSet = "BehaviourOverrideSet"
EvDoctorReport         = "DoctorReport"
```

Payload types mirroring the backend's own shapes exactly (B26 §1b, §2b):
`SetBehaviourOverride{Key string, Value any, HasValue bool}` (or the
equivalent "omit the field entirely to clear" encoding
`CmdSetProviderAPIKey`'s own payload marshaling already has to solve
elsewhere — reuse whichever approach that call site settled on rather than
inventing a second one), `BehaviourOverrideSet{Key string, Overrides
map[string]any}`, `DoctorReport` as a loosely-typed `map[string]any` (the
backend's own dict has no fixed Go-side schema and isn't owed one — the card
renders it as text, it does not bind individual fields).

`settings.go` gains `settingsSendBehaviourOverride(key, value)` /
`settingsSendBehaviourClear(key)`, built on the same `settingsSend` helper
`settingsFetchProviders` already uses — including its
`awaitingConnection`/retry behavior, since a Behaviour change attempted in
the same narrow startup window F10's handoff already documented (before the
handshake completes) must not be silently dropped either.

`root.go`'s `handleSettingsEnvelope` gains the `BehaviourOverrideSet` case
(updates `settingsState`'s own override map, which `behaviourRowDecor`
reads) and the `DoctorReport` case (opens the doctor card — see §3).

### 3. The About tab and the doctor card

New tab, `aboutTabIndex`, one row: `"about:doctor"`, labeled "Run doctor".
Selecting it calls `settingsSendGetDoctorReport()`; the `DoctorReport` reply
opens a scrollable card over the Settings popup, reusing `resultcard.go`'s
rendering primitives (`wrapParagraphs`, the same scroll/discard keys F9a
already built) rather than a third text-rendering implementation — the card
back-returns to the live Settings popup instance, the same pattern
`ResultCard.back` already uses to return to the tray.

The card's text is produced by formatting `DoctorReport`'s dict in the same
section order `cli.py`'s `_print_doctor_report` prints it in (version info,
go frontend status, python version, dependency report, database path/schema
state, config/credentials/state/log paths and existence, per-provider key
source masked, ollama report, mock provider status, `behaviour_overrides`) —
not byte-identical to the CLI's own formatting (that guarantee is `cli.py`'s
alone, B26 Done-when #4), but the same facts in the same grouping, so a
report that looks right on one side looks right on the other.

No other About content (versions/paths as a separate always-visible list,
etc.) is in this episode's scope — the doctor card already surfaces those
facts, and F10's original spec's "About — versions, paths, and a `point
doctor` action" is satisfied by the one action existing and showing them,
not by a second, separate static display of the same facts.

---

## Do NOT

- **Do not touch the Providers or Appearance tabs.** Not their items, not
  `settingsDecor`'s or `onSettingsSelect`'s existing cases, not
  `newProvidersTabItems`/`newAppearanceTabItems`. New prefixes only.
- **Do not redesign the Settings popup's tab structure.** `Tabs` grows by two
  entries (`Behaviour`, `About`); the dispatch-by-prefix mechanism itself
  (`settingsDecor`/`onSettingsSelect` switching on `strings.HasPrefix`) is
  unchanged, exactly as F10's own handoff described this exact extension.
- **Do not add `scrollback` or an idle-unload-timeout row.** Neither is a
  real `config_value()`-backed key — B26 deliberately did not create either
  (Do-NOT, that episode), and inventing a disabled placeholder row here would
  promise a feature no backend key exists for. Omit both entirely; do not
  show them greyed out.
- **Do not add a row per provider's concurrency cap.** See Decision 3 — one
  general-cap row only.
- **Do not display a value for `config.toml`'s actual setting.** See
  Decision 2 — `default`/`overridden → X` only, never a guessed or
  cached-from-nowhere number.
- **Do not add a `GetBehaviourOverrides` request.** B26 deliberately did not
  build one (§1b) — this process's own override map plus `state.json`'s
  persisted copy is the only source of truth it needs; `DoctorReport`'s
  `behaviour_overrides` field is read-only confirmation, not a sync point to
  poll.
- **Do not build the Keys tab.** F10's original spec lists it; no episode
  (this one included) has scoped it yet. Leave `Tabs` at exactly `Providers,
  Appearance, Behaviour, About` — four, not five.
- **Do not build demo mode or touch anything F10c owns.** Unrelated surface,
  untouched by B26 too.
- **Do not change `backend\` at all.** Every event this episode drives
  already exists, already verified. If something here turns out to need a
  backend change, stop and say so rather than extending B26's scope informally.

---

## Done when

**Scope note, since several items below use `chat.queue_depth` as their
walkthrough example:** every item in this list tests the WIRE round trip
and persistence/replay mechanics — does the UI send
`SetBehaviourOverride`, does the row update on `BehaviourOverrideSet`,
does `state.json` persist it, does a reconnect replay it. None of them
test, or are capable of testing, whether a configured depth changes real
send-admission behavior through the shipped app — Decision 4 above
establishes, with file:line evidence, that `chat.queue_depth` specifically
has **no reachable path** to do so today, regardless of these items
passing. That admission-logic correctness is proven separately, backend-
only, by `backend\tests\manual_b26_behaviour_overrides.py`'s `#1b`
(direct `Runtime.receive()` calls, B26's own verification). Passing
#2/#7/#8/#9 below confirms the plumbing works; it does not and cannot
confirm `chat.queue_depth` does anything observable end to end.

1. Opening Settings → Behaviour shows all five rows, each reading `default`
   (no override has been set this session, none was persisted to replay).
2. Setting Queue depth to `0` sends `SetBehaviourOverride` and, on the
   `BehaviourOverrideSet` reply, the row updates in place to `overridden → 0`
   with no popup close/reopen needed.
3. Clearing that same row sends `SetBehaviourOverride` with no `value`, and
   the row returns to `default` on the reply.
4. Toggling `Allow outside project` flips immediately and is reflected in the
   next `GetDoctorReport`'s `behaviour_overrides` map.
5. Typing a non-numeric value into a numeric row is rejected locally, before
   any wire request is sent.
6. About → Run doctor opens a scrollable card showing the backend's report,
   including the active overrides from steps 2 and 4 under
   `behaviour_overrides`.
7. **Backend-restart persistence (Decision 1):** with an override set, kill
   and relaunch the backend process while the frontend stays running;
   once reconnected, the override is automatically re-sent (observable via a
   fresh `GetDoctorReport` through the doctor card showing it still active)
   with no user action beyond what reconnection already does on its own.
8. **Frontend-restart persistence:** quit and relaunch the frontend entirely
   (same backend profile); the previously-set override is replayed once
   connected, same observable as #7.
9. **Multi-override replay is the full map, not just the latest key.**
   #7/#8 each exercise exactly one active override, which cannot distinguish
   "replay every persisted override" from "replay only the most recently set
   one" — both would pass a single-override check identically. With **two**
   overrides active at once, on keys of **different types**
   (`chat.queue_depth` = an int, `project.allow_outside_project` = a bool),
   both a backend restart and a separate frontend restart must bring **both**
   back — `GetDoctorReport`'s `behaviour_overrides` after reconnecting
   contains both keys, not whichever was set second, and not whichever
   sorts first.
10. `config.toml` remains byte-identical before and after every Behaviour
    change in this session (including the multi-override case in #9) —
    F10's own Done-when #6, which this episode must not regress since it is
    the same "Settings never writes `config.toml`" rule applied to a fourth
    tab.
11. The Providers and Appearance tabs behave exactly as F10 left them —
    confirmed by running F10's own existing test suite unmodified, not by
    inspection.

---

## Manual verification (PowerShell)

**Per CLAUDE.md's hard rule: never rename or delete the real `%APPDATA%\Point`
/ `%LOCALAPPDATA%\Point` by name.** One PowerShell session for the whole
gate, env vars set once, both `go run` and `uv run python main.py` launched
inside it, cleanup at the end. Eleven steps, numbered to match Done-when,
run in sequence as one script — the config.toml snapshot is taken before any
Behaviour change, and the regression suite runs as the last step inside the
same `try`, not as a separate paragraph below it.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f10b"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch

    # Launch backend + frontend against $scratch, in two terminals of this
    # same session (both processes read APPDATA/LOCALAPPDATA from this
    # session's env, so either terminal works as long as neither is started
    # outside it).
    cd D:\Projects\Point\backend
    # (terminal A) uv run python main.py
    cd D:\Projects\Point
    # (terminal B) go run .\frontend\cmd\point

    # --- 0 (setup for #10): snapshot config.toml BEFORE any Behaviour
    #     change is made, so the before/after compare at the end is honest.
    Copy-Item "$scratch\Point\config.toml" "$env:TEMP\before-f10b.toml" -ErrorAction SilentlyContinue

    # --- 1: Settings -> Behaviour. All five rows read `default`.
    # EXPECT: no row claims an overridden value before anything has been set.

    # --- 2: set chat.queue_depth = 0.
    # EXPECT: the row updates in place to `overridden -> 0` on the
    # BehaviourOverrideSet reply, popup stays open.

    # --- 3: clear that same row.
    # EXPECT: the row returns to `default` on the reply.

    # --- 4: toggle Allow outside project on.
    # EXPECT: flips immediately; About -> Run doctor's next report shows it
    # under behaviour_overrides.

    # --- 5: type a non-numeric value ("a") into a numeric row.
    # EXPECT: rejected locally -- no SetBehaviourOverride request leaves the
    # process (confirm via point.log: no new request logged for that key).

    # --- 6: About -> Run doctor.
    # EXPECT: a scrollable card opens over Settings showing the backend's
    # report, including the override set in step 4 under behaviour_overrides.

    # Clear step 4's override before continuing, so #7 starts from a known
    # single-override state.
    # (Behaviour tab -> clear "Allow outside project")

    # --- 7: backend-restart persistence, ONE override active.
    # Set chat.queue_depth = 0. Stop the backend process (Ctrl+C in terminal
    # A) and restart it (uv run python main.py again, same $scratch).
    # Reconnect happens automatically. Reopen About -> Run doctor.
    # EXPECT: chat.queue_depth=0 is back in behaviour_overrides, without
    # having re-set it by hand.

    # --- 8: frontend-restart persistence, ONE override active (still
    #     chat.queue_depth=0 from #7). Quit the frontend entirely and
    #     relaunch `go run .\frontend\cmd\point` against the same $scratch
    #     (backend may stay running or also be restarted -- either path
    #     must work per Decision 1).
    # EXPECT: chat.queue_depth=0 reappears in the doctor card after
    # reconnecting, with no Behaviour-tab action taken this launch.

    # --- 9: MULTI-override replay -- #7/#8 above each prove only a SINGLE
    #     override survives a restart, which cannot distinguish "replay the
    #     whole map" from "replay only the most recent key." With
    #     chat.queue_depth=0 still active from #8, ALSO set
    #     project.allow_outside_project = true (a second key, a different
    #     type). Confirm via About -> Run doctor that BOTH appear in
    #     behaviour_overrides BEFORE restarting anything.
    #
    #   9a. Restart the BACKEND only (terminal A). Reopen About -> Run
    #       doctor once reconnected.
    #       EXPECT: behaviour_overrides contains BOTH chat.queue_depth=0
    #       AND project.allow_outside_project=true.
    #       FAILS IF: only one of the two survives.
    #
    #   9b. Now restart the FRONTEND (quit terminal B, relaunch
    #       `go run .\frontend\cmd\point` against the same $scratch).
    #       Reopen About -> Run doctor once reconnected.
    #       EXPECT: behaviour_overrides still contains BOTH keys.
    #       FAILS IF: only one of the two survives, on EITHER restart path.

    # Quit the frontend (and backend) cleanly before the file compare below.

    # --- 10: config.toml untouched across every change made in steps 1-9.
    Compare-Object (Get-Content "$scratch\Point\config.toml" -ErrorAction SilentlyContinue) `
                   (Get-Content "$env:TEMP\before-f10b.toml" -ErrorAction SilentlyContinue)
    # EXPECT: no output.
    # FAILS IF: anything differs, or config.toml did not exist before and
    # exists after.

    # --- 11: no regression to F10's existing Providers/Appearance tabs.
    go test .\frontend\internal\app\... .\frontend\internal\chat\... -count=1 -race
    # EXPECT: clean, including every F10-era test named in
    # design/completed/frontend/F10-settings-first-run.md's "Where the code
    # went" (settings_test.go, theme_live_test.go, providers_test.go's
    # retrofit tests) still passing unmodified.
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

Do not proceed until every check above passes.

---

## Handoff

State after this episode: Settings has four tabs (`Providers · Appearance ·
Behaviour · About`), the Behaviour tab has a real backend-connected effect
for the five keys B26 supports, and About's `doctor` action gives a human
the same report `point doctor` prints, from inside the app, without a second
terminal.

Record for the next session: whether `CredentialPrompt`'s unmasked-mode
generalization turned out clean or awkward — F11's memory popup and any
future numeric/text Settings control will want the answer before deciding
whether to reuse it or build a second input overlay.

Not done, and not this episode's to do:

- **The Keys tab** (rebind, from the action registry) — F10's spec lists it;
  no episode has scoped it.
- **Per-provider concurrency-cap rows** (Decision 3) — the backend already
  supports the family; only the general cap is wired here.
- **Demo mode (F10c)** — unaffected, still needs its own provider type,
  fixture library, and sample project.
- **F11 (Memory popup)** — the next tab added to this same `Tabs` slice,
  same prefix-dispatch mechanism, per F10's own handoff.
