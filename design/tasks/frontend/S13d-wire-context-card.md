# Session 13d — Wiring the Context Card

**Replace the Status card's row-1 stub with a real read of B20's
`ContextReport` event, and build the "expand via ⏎/click" gesture into
UI-09 — the context composition card — using the same numbers, never a
second pass over them.**

> **Depends on:** B20 (done — `GetContextReport`/`ContextReport` are real
> wire types), F6 (done — the Status card exists as a marked stub), F7
> (done — the overlay contract and `Popup`/`Confirm` precedent this session
> follows for UI-09).
>
> **Model:** Sonnet. The arithmetic is B8's and stays B8's; this session
> only decodes a payload, stores it, and draws it. The two open design
> questions — how the expand gesture is reached given the right pane has no
> focus model, and whether it's reachable at all below 90 columns — are
> decided below, not left for implementation time.
>
> This is a **verification-and-repair-shaped session**, same lineage as
> S11b/S12b: it consumes no F number, spends data a backend episode already
> shipped, and is scoped to files F6 already touched plus the wire-layer
> types B20 added, **plus one small, explicitly-flagged generalization in
> `app\root.go`** (§6) that the reachability decision below depends on.

---

## Why this session exists

B20 put `context.report()` on the wire: `GetContextReport` (on-demand pull,
optional `session_id`) and `ContextReport` (the reply, `corr_id` set — and
the push after a turn's assistant message persists, `corr_id: null`, always
carrying `session_id`). F6 built the Status card against nothing — the row
says `"context: not wired yet (B20)"` in words, deliberately, rather than
draw a bar B8 never sent — and F6's own spec (`F06-right-pane.md`, Done-when
#2) requires the expanded composition card, but F6 could not build it with
no section data to show. Both gaps close together, from the same event.

---

## What actually arrives — read from the source, not assumed

`ContextReport.to_dict()` (`backend\managers\context.py:338`), one key per
dataclass field, no subset:

```
session_id, provider, model, budget_tokens,
sections: {system, attachments, memory, summary, history, task_state} -> int,
counts:   same six keys -> int,
used_tokens, free_tokens,
included:       [{section, ref, kind, tokens, detail}],
next_evictions: [{section, ref, kind, tokens, reason}]
```

`SECTION_ORDER` (`context.py:85`) is `(system, attachments, memory, summary,
history, task_state)` — always all six keys, zero-valued sections included,
which is what lets the card sum sections and equal `used_tokens` without a
special case for "nothing there yet".

On the wire (`backend\runtime\runtime.py:1038-1099`): the push fires from
inside the stream sink's terminal-state branch, only when a message was
actually persisted, envelope-level `session_id` always set, `corr_id: null`.
The pull reply carries the same payload shape with `corr_id` set to the
request's id. **Both use the same `OutgoingEventType.CONTEXT_REPORT =
"ContextReport"` wire type** — one decoder on the Go side, exactly as B20's
doc says.

**Routing consequence, checked against `frontend\internal\protocol\client.go`
`deliver()` (lines 699-733):** routing order is corr_id, then task_id, then
session_id. The push has no corr_id and no task_id but DOES carry
`session_id`, so it routes `RouteSession`, not `RouteUnroutable` — it will
NOT be silently logged as a contract bug. But `chat\protocol.go`'s
`handleEnvelope` (the only place `chat.Workspace` currently reads an
`EnvelopeMsg`) tries `handleStreamEvent` (matches by corr_id/task_id — both
empty, no match) and then looks up `w.pending[env.CorrID]` — `w.pending[""]`
is never populated, so today this event would hit `"not one of ours"` and be
dropped. **A push-shaped envelope needs its own branch in `handleEnvelope`,
checked before the pending-map lookup** — nothing upstream of `chat` handles
it for us.

---

## Build

### 1. Wire types (`frontend\internal\protocol\types.go`)

Add, matching the backend's literal strings exactly (the dispatcher and this
decoder both key on the JSON string, not a shared enum):

```go
CmdGetContextReport = "GetContextReport"   // Cmd block
EvContextReport     = "ContextReport"      // Ev block
```

Add both to whatever exhaustiveness list `envelope_test.go` keeps (it already
enumerates every `Ev*`/`Cmd*` — see `types.go:10-83` and the corresponding
test) so a future audit doesn't flag a wire type nothing decodes.

### 2. Decode shape (`frontend\internal\chat`, new or existing file — your call, but keep it next to `wireUsage`/`wireSession` in `model.go`)

A `wireContextReport` struct mirroring `ContextReport.to_dict()` field for
field — `Sections`/`Counts` as `map[string]int`, `Included`/`NextEvictions`
as slices of a small `wireContextItem{Section, Ref, Kind, Tokens, Detail,
Reason string}` (one shape serves both — `to_dict()`'s `included` entries
never populate `reason` and `next_evictions` entries never populate
`detail`; decoding both through the same struct costs nothing and avoids a
second near-identical type). **No field renamed, none dropped, none
computed client-side** — `used_tokens`/`free_tokens`/`sections` arrive
already summed; do not re-derive them from `included` in Go, which is
exactly the "second implementation of the arithmetic" B8's own docstring and
this session's own charter both forbid.

### 3. Fetch + push, in `chat.Workspace`

- New field next to `usage wireUsage` (`chat.go:257`): `contextReport
  *wireContextReport` (pointer — nil is the real "nothing yet" state,
  distinct from a zero-valued report, the same distinction F6 already makes
  for `w.usage`).
- New `queryKind`: `queryGetContextReport`, alongside `queryListTasks` /
  `queryListProviders` (`protocol.go:23-37`).
- **Pull once per session load.** `setSession` (`protocol.go:249-298`) ends
  by calling `w.requestTaskRows()` — add a parallel
  `w.requestContextReport()` there (send `GetContextReport` with
  `session_id: w.sessionID`). Reload has no per-message history to seed this
  from the way `w.usage` seeds off the newest message's `TokenUsage` — a
  composition snapshot isn't stored per-message — so an explicit pull is the
  only way the card shows anything before the next turn completes.
- **Push, every turn.** In `handleEnvelope` (`protocol.go:126-187`), add a
  branch for the unsolicited case **before** the `w.pending[env.CorrID]`
  lookup:
  ```go
  if env.Type == protocol.EvContextReport && env.CorrID == "" {
      w.onContextReport(env)
      return nil
  }
  ```
  The pull's reply (corr_id set) goes through the ordinary `kind` switch —
  add `case queryGetContextReport: w.onContextReport(env)` there. **Both
  paths call the same `onContextReport` — confirmed correct as designed,
  no change from the first draft.** This is the same "route both paths
  through one function" fix this project has needed twice before at real
  cost (`Diagnostics.Seq` and `taskCardWasVisible`, both in F6's own "The
  pattern this episode is really about") — applied here from the start
  instead of discovered after a field report.
- **`onContextReport` filters on `env.SessionID == w.sessionID`.** Nothing
  in this codebase currently filters an envelope by session — F6 never
  needed to, because `chat.Workspace` only ever has one session open and
  everything it reads so far is either scoped by corr_id/task_id or is
  root-owned (`Diagnostics.Tasks` is deliberately global). A `ContextReport`
  push is the first per-session-scoped broadcast this workspace receives
  that carries no correlating id at all — a background task belonging to a
  session the user has since navigated away from (once session-switching
  exists) would otherwise overwrite the open session's numbers with a
  stranger's. Decode, compare `payload.session_id` to `w.sessionID`, ignore
  on mismatch. Cheap, and it is the same defensive shape `knownProvider`
  already applies to a different stale-data hazard (`S12b`, "a stale
  session row could strand the app").
- **Reset `w.contextReport = nil` wherever `w.usage = wireUsage{}` already
  resets on session change** (`setSession`, `protocol.go:272`) — same
  reasoning, same place.

### 4. Status card row 1 — real, `chat\cards.go` `statusCardLines` (line 85)

Replace the stub line with:
- `w.backendDown` or `w.contextReport == nil` → keep the existing dim
  placeholder convention (`"—"`/`"no turns yet"` siblings already use this
  card's own idiom) — never a fabricated bar before the first report
  arrives. **This is one of the two places §6's open gesture must be gated
  on the same condition — see §6's guard subsection; do not let the two
  drift into checking it two different ways.**
- Otherwise: a fill bar plus `"12.4k / 32k"` computed from
  `contextReport.UsedTokens` / `BudgetTokens` (NOT `FreeTokens` — the
  mockup's ratio is used-over-budget, and `formatTokens` already exists
  for the `12.4k` rendering, reuse it verbatim).
- **New small helper**, not present anywhere in this codebase yet (checked —
  no `█`/`▓`/bar-drawing helper exists in `frontend/`): something like
  `tokenBar(used, budget, width int) string` returning a fixed-width run of
  a filled-cell rune over a track rune, `AccentPrimary` for the filled
  portion and `TextDim` for the track (matching `statusCardLines`' existing
  `dim`/`th.Style(theme.TextPrimary)` split). Keep it in `cards.go`, next to
  `formatTokens` — it is a rendering primitive for this card, not a shared
  `ui` package concern (this project already carries four un-shared copies
  of `sideBySide`; do not make this a fifth thing flagged for a future
  `ui` package — it's ~10 lines and has exactly one caller family: this
  card and UI-09 below).

Do not touch the card's row 2/row 3 (tokens, provider·model) or its
priority-drop-from-the-bottom behavior — those are already real and
untouched by this session.

### 5. UI-09 — the context composition card

**A bespoke `app.Overlay`, not a `Popup`.** `rightPaneOverlay`
(`chat\cards.go:400-410`) already made this exact call for the fallback
right-pane overlay, for the same reason: the content is ambient data, not a
selectable item list, so `Popup`'s list/selection machinery (`app\popup.go`)
buys nothing and its `OnSelect`/tab/section model would be dead weight here.
Follow `rightPaneOverlay`'s own shape — a small struct holding `*Workspace`,
implementing `MinSize()/Title()/View()/Update()`; `Update` closes on `esc`,
same one-liner `rightPaneOverlay.Update` already uses.

Content, straight off `w.contextReport` — **no arithmetic, only formatting**:
- Header: `"Context · {used} / {budget}"` (reuse `formatTokens`).
- One row per `SECTION_ORDER` name, in that fixed order, sections with a
  `counts[name] > 1` showing the count (`"attachments (2)"`, `"history (18
  msgs)"` — the mockup's own wording; a count of 0 or 1 shows no
  parenthetical, matching `describe()`'s own singular/plural handling at
  `context.py:374`), each with `tokenBar` (§4) scaled to that section's
  share of `used_tokens`, not of `budget_tokens` — the mockup's own bars are
  visibly proportional to each other, not to the free space. A local
  `map[string]string` from the six `SECTION_ORDER` keys to display labels
  (`"system"` → `"system prompt"`, `"task_state"` → `"task state"`, the rest
  unchanged) — six entries, not worth a shared type.
- A rule, then `free` and the eviction line: if `NextEvictions` is empty,
  `"next turn evicts: nothing"` (mirrors `describe()`'s own `"evicted:
  nothing"` at `context.py:392-393`); otherwise summarize what B8 actually
  named — do not invent a generic label FROM the count. `next_evictions`
  entries carry `kind`/`ref`/`reason` per item; render as many as the
  overlay's height affords and cap the rest with the exact `"+N more"`
  convention `taskCardLines`/`capTerminalTail` already established in F6
  (`F06-right-pane.md`'s Bug #9 finding: "the number it is not showing is
  information the reader needs, and silence is the one answer that cannot
  be checked" — the same rule applies here, not just to the Task card).
- **`MinSize()`: pick a real size for the content, not a size chosen to
  dodge the reachability problem.** §6's fix means the fallback-overlay
  swap path re-runs the same fit check `OpenOverlay` runs, so UI-09 does
  NOT need to be squeezed to fit inside the fallback's fixed 36×20 box —
  size it for what the mockup actually needs (something in the neighborhood
  of 45×14; check your own rendered content against it the way F7 did
  before settling on 70×16, and record the actual numbers chosen in this
  session's Handoff). If the terminal is too small for that size when the
  swap is attempted, §6's fix means it **refuses with the real numbers and
  keeps the fallback overlay open** — it does not clip, and it does not
  crash.

Open it with `root.OpenOverlay(...)` from the real (non-overlay) pane's
entry points, or via the self-swap return value from `rightPaneOverlay.Update`
— see §6 for both paths.

### 6. The expand gesture — decided here, not left open

**The right pane is not in `FocusAware`'s rotation** (F6's own inheritance
note), and this session does not put it there.

#### 6a. Literal `⏎` is unavailable inside `chat.Workspace` — checked, not assumed

`chat.Workspace.Update` branches purely on `w.input.focused`
(`protocol.go:376-380`): focused, Enter sends the message
(`input_keys.go:67`); unfocused ("browsing" — `EnterBrowseMsg`), Enter is
already `handleKeyString`'s `case "o", "enter":` for expanding a focused
**message** slot (`scroll.go:269`). There is no third focus state for the
right pane to occupy, and inventing one — a rotation `Tab` cycles through
that includes card rows — is exactly the kind of new focus-model
infrastructure F6 flagged and explicitly deferred. This session has exactly
one destination (the Status card), not the general case F9's task-tray
click-to-open will eventually need, so building the general mechanism here
would be over-scoped for what's actually needed.

**Decision for the real (non-overlay) right pane:** a registered leader
action, independent of any focus state — the same shape `pane.right.toggle`
already uses. Register `context.expand` in `app\actions.go`, next to
`pane.focus.next`:

- **Chord: leader `x`.** Checked against every existing registration
  (`app\actions.go`) before picking it — the four leader bindings in this
  codebase today are `q` (quit), `?` (help), `c` (`pane.right.toggle`), `r`
  (retry). `x` is free; registering a duplicate fails `registry.Validate`
  at boot, so this is confirmed, not assumed.
- `When`/`Run` both go through the ONE guard method described in §6c below
  — not two independent nil-checks that can drift apart.
- Because it is a normal registered action, it is **automatically listed in
  both the command palette and the Help screen** (`app\overlay.go`'s
  `newPaletteOverlay`/`newHelpOverlay` both build their item list from
  `root.actions.All()` — nothing hand-maintained, nothing to add
  separately). This closes the discoverability ask directly: no new help
  entry to write, because the help screen already renders the registry, not
  a curated list. If `context.expand` isn't currently available (no report
  yet, or the backend is down), it shows in Help the same way every other
  conditionally-available action does — `SetDisabled(!a.Available(root))` —
  greyed out, not hidden, not omitted.

**Click, for the same real pane:** mouse events already reach
`chat.Workspace.Update` as a raw `tea.MouseMsg` whenever no overlay is open
(`root.go` `dispatchMouse`, lines 876-881: `r.overlay == nil` routes
straight to `r.current().Update(m)`), but `chat.Workspace` has **no
`tea.MouseMsg` case anywhere today** — checked directly, none exists. Add
one: `rightPane()` (`render.go:44-61`) already computes, on every call, the
exact on-screen `layout.Rect` for the Status card (`l.RightPane[0]`) — store
it on the workspace as it's computed (e.g. `w.statusCardRect layout.Rect`)
and hit-test a left-click-press against it in the new `case tea.MouseMsg:`.
A hit calls the same guarded constructor the leader action's `Run` calls
(§6c) — one function, two triggers into it, not two implementations of
"open the overlay."

#### 6b. The narrow-terminal fallback — reachable via a same-slot swap, not stacking

Re-examined, distinguishing the two shapes precisely, because they are not
the same risk:

- **STACKING — rejected, correctly, still out of scope.** Making
  `r.overlay` able to hold two overlays at once (a stack, or a "previous"
  pointer) is a real type change to `Root`'s own state and a new class of
  bug (which one gets `esc`, which one gets resize checks, what "the open
  overlay" even means for `rightPaneVisible()`). Nothing in this session
  needs two overlays showing at once, and nothing here builds that.
- **SAME-SLOT SWAP — genuinely workable, and it is what §6 now specifies.**
  `Overlay.Update` already returns `(Overlay, tea.Cmd)`, and root already
  documents that an overlay may hand back something other than itself:
  `root.go:697-698`'s own comment on the esc-closes-on-second-press case —
  *"An overlay asks to close by returning a nil Overlay from Update, exactly
  like any other state change."* Both `dispatchKey` (line 700-701) and
  `dispatchMouse` (line 895-896) already just assign whatever `Update`
  returns straight to `r.overlay` — nothing about the mechanism is specific
  to `nil`. So `rightPaneOverlay.Update`, while it is the open overlay and
  therefore traps all input (`dispatchKey`'s own comment, lines 693-698),
  can recognize `enter` on a `tea.KeyMsg` — **not the leader chord**, which
  cannot reach it (see the precise blocker below) — and a click on its own
  embedded Status section, and return the guarded UI-09 overlay in place of
  itself. `r.overlay` stays a single field, holding exactly one overlay at
  all times, before and after. No stacking type, no two-open-at-once state.

**The one precise blocker this surfaced, and how it's closed — not
sidestepped:** a self-returned overlay swap is **not** fit-checked today.
`OpenOverlay` (`root.go:812-820`) is the only call site that runs
`overlayFits` before assigning `r.overlay`; the two call sites that apply
whatever `Update` returns (`dispatchKey:700-701`, `dispatchMouse:895-896`)
assign unconditionally. `rightPaneOverlay.Update` also has no access to the
real terminal size to check this itself — its own `View(th, w, h)` only
ever receives its OWN rendered box size (`overlayRect()` never grows an
overlay past its `MinSize`, so that `w,h` is a constant ≈36×20, not the
terminal's actual dimensions). Swapping to a larger UI-09 without a real
fit check could render it clipped on a terminal that only barely fit the
36×20 fallback.

**The fix — a small generalization at the one place `r.overlay` is
assigned, applying this project's own recurring lesson (F6: derive an
invariant once, at a single choke point, rather than trust every call site
to maintain it) to a THIRD case, after `Diagnostics.Seq` and
`taskCardWasVisible`:**

```go
// app/root.go
// setOverlay applies whatever an Overlay's own Update returned. nil always
// closes. Anything else is choosing to open a (possibly different) overlay
// -- rightPaneOverlay swapping itself for UI-09's composition card (S13d)
// is exactly this -- and must clear the same MinSize floor OpenOverlay
// already enforces for a fresh open. Without this, a self-returned swap
// could render an overlay larger than the terminal actually has room for,
// because neither dispatchKey nor dispatchMouse re-checked before this,
// and an Overlay's own View() never sees the real terminal size to check
// it another way (only its own, non-growing, rendered box size).
func (r *Root) setOverlay(next Overlay) {
    if next == nil {
        r.overlay = nil
        return
    }
    if next == r.overlay {
        return // unchanged -- most Update calls hit this path
    }
    if ok, msg := r.overlayFits(next); !ok {
        r.status = msg
        return // keep whatever is already open; do not clip, do not close
    }
    r.overlay = next
}
```

`dispatchKey:700-701` and `dispatchMouse:895-896` both change from
`r.overlay = o` to `r.setOverlay(o)`. This is the one piece of this session
that touches `app\root.go` rather than `chat`'s own files or the new wire
constants — flagged explicitly here, the same way F6 flagged the
session-title-newline fix to `session_manager.py` as outside its own
nominal scope but necessary and recorded.

With `setOverlay` in place: `rightPaneOverlay.Update` can safely return
`o.w`'s guarded UI-09 overlay (§6c) on `enter` or a Status-section click.
If it doesn't fit the real terminal, the swap is refused with the actual
numbers and the fallback overlay stays exactly as it was — the same
refusal behavior every other overlay in this app already gives, now applied
uniformly regardless of which of the two paths chose to open one.

**Accepted consequence, stated plainly:** `esc` from UI-09 closes it
entirely (`return nil, nil`, same one-liner every bespoke overlay in this
codebase already uses) — it does not step back to the fallback overlay.
Reaching Task/Sessions again from a narrow terminal means reopening the
pane via `pane.right.toggle`. This is a real, deliberate cost of "one field,
no stacking" — recorded here rather than silently accepted, and it is not
what any Done-when below claims otherwise.

**Checked for regressions, not just assumed clean:** after the swap,
`r.overlay.(RightPaneFallback)` no longer holds (UI-09 doesn't implement
`IsRightPaneFallback()`), so `rightPaneVisible()` (`root.go:481-487`) goes
false while UI-09 is open — which correctly **stops task-list polling**
while looking at the composition card, the same "no subscribers while
hidden" rule F6's Done-when #6 already established for the pane itself
(the Task/Sessions cards genuinely aren't on screen while UI-09 is). It also
means `closeRightPaneFallbackIfObsolete` (`root.go:556-569`) no longer
auto-retires UI-09 on a widen-resize — it just behaves like any other
floating overlay (Popup included), which is correct: UI-09 is not standing
in for the pane anymore once it's the thing on screen, it's its own
destination.

#### 6c. The guard — one function, both entry points, confirmed

**A press or click before the first report arrives, or while the backend is
down, must not open UI-09 with no data.** This is enforced by construction,
at a single choke point, not left to each of the three call sites (leader
action, click on the real pane, `enter`/click inside the fallback overlay)
to remember separately — the same shape this session already uses for
`onContextReport` (§3) and is deliberately reusing here rather than
inventing a second pattern:

```go
// chat package — a narrow interface app.go can type-assert against,
// same pattern as app.OverlayProvider / app.FocusAware.
func (w *Workspace) ContextExpandOverlay() (app.Overlay, bool) {
    if w.backendDown || w.contextReport == nil {
        return nil, false
    }
    return newContextOverlay(w), true
}
```

- `context.expand`'s `When` calls it and reads only the `bool` (mirrors how
  `pane.focus.next`'s `Run` already reads `FocusAware` through a type
  assertion on `root.current()`).
- `context.expand`'s `Run`, the click handler in `chat.Workspace`, and
  `rightPaneOverlay.Update`'s `enter`/click branch **all call this same
  method** and only proceed to `OpenOverlay`/`setOverlay` on `true`. A
  failed guard inside `chat.Workspace`'s own click handler or the leader
  action is a silent no-op (the action's own `When: false` already prevents
  the leader chord from doing anything, and a click has no natural place to
  toast a status from inside a workspace's `Update`); a failed guard inside
  `rightPaneOverlay.Update` returns itself unchanged (`o, nil`) — same "stay
  open, do nothing" shape it already uses for any key that isn't `esc`.
- **Confirmed: this is enforced before opening, not left to UI-09 to
  render gracefully around a nil report.** UI-09 is never constructed with
  a nil `contextReport` in the first place — `newContextOverlay` is only
  ever called from inside the `true` branch above, so it never needs a nil
  guard of its own.

---

## Do NOT

- **Do not reimplement any of B8's arithmetic.** Every number this session
  shows — the fill ratio, the per-section bars, the free total, which items
  get evicted — comes verbatim off `ContextReport.to_dict()`. If a number
  looks wrong, the bug is in how it's read or rendered, never in a
  recomputation.
- **Do not redesign the Status card's existing layout or priority-drop
  order.** Rows 2 and 3 are real and unrelated to this session; touch only
  row 1.
- **Do not build a general right-pane focus/rotation model.** §6 above is
  the full, bounded answer for this one destination. Leave Task-card
  `⏎`-to-open (F9's) and Sessions-card behavior exactly as F6 left them.
- **Do not make `Popup` do this.** UI-09 is a bespoke `Overlay`, matching
  `rightPaneOverlay`'s own precedent, not a list to select from.
- **Do not touch the backend.** Every field this session needs is B20's and
  already shipped, byte-identical between the pull and the push (B20's own
  gate proved this). If a field this doc describes is actually missing or
  shaped differently once you go read the code, stop and report it — do not
  patch the backend from here.
- **Do not build overlay stacking.** §6b's same-slot swap is not stacking —
  `r.overlay` remains exactly one field holding exactly one `Overlay` at any
  moment, before and after. Do not generalize `setOverlay` into anything
  that remembers a "previous" overlay or lets two be open at once; that is
  a different, unrequested feature.
- **Do not skip the `setOverlay` fit-check "just this once."** It is the
  only thing making the fallback-overlay swap safe on a terminal sitting
  exactly at the 36×20 floor; a swap that bypasses it is the clipping bug
  §6b's blocker describes, reintroduced.

---

## Done when

1. The Status card's row 1 is a real fill bar with real `used/budget`
   numbers, sourced from a decoded `ContextReport`, updating after every
   turn (push) and correct immediately after a session reload (pull).
2. Leader `x`, a left-click on the Status card in the real (non-overlay)
   right pane, AND `enter` (or a click on the embedded Status section)
   inside the narrow-terminal fallback overlay all open the UI-09
   composition overlay — three entry points, one guarded constructor.
3. The composition overlay's six section rows, in `SECTION_ORDER`, sum
   exactly to `used_tokens`, and each section's bar is proportional to the
   others.
4. A named eviction shows real `next_evictions` content (kind/ref/reason),
   not a generic placeholder, and `next turn evicts: nothing` when the list
   is empty.
5. A `ContextReport` push for a session other than the one currently open
   does not alter the open session's card (session-id filter, §3).
6. Backend down still shows the card's existing `—` placeholder — no stale
   numbers frozen from before the disconnect — and none of the three entry
   points in #2 opens UI-09 while it's down (the §6c guard).
7. On a terminal too small for UI-09's real `MinSize`, every entry point
   refuses with the actual numbers (`OpenOverlay`'s own message for the two
   real-pane entry points; `setOverlay`'s identical message, via
   `r.status`, for the fallback-overlay swap) — never a clipped render,
   never a crash, and the fallback overlay (where applicable) stays open
   rather than closing to nothing.
8. `context.expand` appears in both the command palette and the Help
   screen, greyed out via `Available(root)` exactly when guard #6 above
   would refuse it — confirmed by inspection, not just "should follow from
   the registry."
9. `pane.right.toggle`, the Task and Sessions cards, and F5's live-session
   behavior are all unchanged. Task-list polling correctly stops while
   UI-09 is the open overlay (§6b) and resumes correctly once it closes.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-13d"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    cd D:\Projects\Point
    go run .\frontend\cmd\point
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

Eyes-on, inside the running app:

1. Send a message. **Expect:** row 1 goes from stub text to a real bar the
   moment the reply finishes (push, not mid-stream — B20's own gate already
   established the report only moves at turn end).
2. Before sending anything (fresh session, no report yet) and again with the
   backend killed, try leader `x` and a click on the Status card. **Expect:**
   both cleanly do nothing — no overlay, no crash, no half-drawn UI-09
   (confirms §6c's guard).
3. Trigger leader `x`. **Expect:** the composition overlay opens, section
   rows sum to the row-1 total, an eviction line names something real once
   the window is over budget (send enough turns, or check against
   `cli.py context report --json` on the same session for a byte-for-byte
   sections/next_evictions comparison — B20's own `--via-wire` flag is
   exactly this cross-check, reuse it):
   ```powershell
   cd D:\Projects\Point\backend
   uv run python cli.py context report --session-id <id> --json
   ```
4. Click directly on the Status card (terminal wide enough for the real
   pane, not the fallback). **Expect:** same overlay, same content.
5. Open the command palette and the Help screen. **Expect:** `context.expand`
   (or whatever `Name` is chosen) listed in both, bound to leader `x`,
   greyed out exactly when #2's conditions hold.
6. Resize below 90 columns, open the fallback overlay, press `enter`.
   **Expect:** the fallback swaps to UI-09 in place — same content as #3.
   Press `esc`. **Expect:** UI-09 closes entirely (not back to the
   fallback) — confirms the accepted consequence in §6b, not a bug.
7. Shrink the terminal to something that fits the fallback (≥36×20) but not
   UI-09's real `MinSize`, then repeat step 6's `enter` press. **Expect:** a
   refusal message with the actual numbers, fallback overlay still open,
   nothing clipped — confirms `setOverlay`'s fit-check actually runs.
8. Kill the backend (`Stop-Process` on `main.py`, same pattern F06's own
   gate uses). **Expect:** row 1 reverts to `—`, no frozen stale bar.
9. Restart, reload an existing session with prior turns. **Expect:** row 1
   shows real numbers immediately, before sending anything new (the pull,
   not just the push).

---

## Handoff

- **`ContextReport` is now read by exactly one consumer, `chat.Workspace`.**
  Any later feature reading context data (a memory popup, a settings screen
  showing budget) should read `w.contextReport`, never re-request or
  re-derive it.
- **Session-id filtering on an unsolicited push is new to this codebase** —
  record here whether it ever actually triggered a mismatch during
  verification, since today only one session is open at a time and the
  filter is defensive against a future multi-session/background-task
  interleaving, not a bug reproduced live.
- **Record the actual `MinSize()` chosen for UI-09**, and whether it needed
  a second round the way F7's 60×14 → 70×16 did once real content rendered.
- **`app.setOverlay` is new, general machinery, not a one-off for UI-09.**
  Any future overlay whose `Update` returns a *different* overlay (not just
  nil-to-close or itself-to-stay) now gets the same fit-check `OpenOverlay`
  already gave a fresh open. Record here whether anything else in this
  codebase was silently relying on the old unconditional-assignment
  behavior — checked during this session's own implementation, nothing
  currently does (only `rightPaneOverlay`'s esc-to-nil and now
  enter-to-UI-09 ever return anything other than themselves), but a future
  session extending `Overlay.Update` usage should re-check this.
- **The `esc`-from-UI-09-doesn't-return-to-the-fallback consequence (§6b)**
  is accepted, not a bug — if a future episode wants a real back-stack
  between overlays, that is the actual "overlay stacking" work this and F6
  both deliberately avoided building, and it would replace `setOverlay`'s
  simple swap with something that remembers what was open before.
- **Leader `x` is now taken**, registered as `context.expand`. The three
  other bindings in this codebase are `q`, `?`, `c`, `r` — anything picking
  a fifth should check `app\actions.go` fresh rather than trust this list,
  since it will grow.
