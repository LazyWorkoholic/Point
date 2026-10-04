# Episode F10c — Settings: Demo Mode ✅

**Wires B28's already-built `DemoProvider` into the provider chooser as a
selectable, no-key row; gives it the persistent `DEMO` header badge D55
requires, surviving `HeaderCompact`; fires `ProjectInit` against the
shipped sample project the instant Demo is selected, correlates its reply,
and patches the completed default back through the existing
`ProviderDefaultSetMsg` pipe; and confirms, per fixture, which of B28's
six fixtures land on existing UI surfaces with zero new rendering code.
Live verification found Decision 1's own binding mechanism (`project_id`
riding on `CreateSession`'s payload) cannot be won by a *human choosing
Demo in the first-run chooser* — that race is structurally lost on every
launch, for every provider, not a Demo-specific timing gap — spun out as
`design/tasks/backend/B30-set-session-project.md` rather than patched in
place here. **A second, later live test corrected this further: that
mechanism is not dead code.** It fires on any empty session list paired
with a `state.json` that already holds a stored Demo default — which a
stale database (e.g. `runtime.db*` deleted, `state.json` untouched) can
also produce, binding a session to a `project_id` whose `projects` row no
longer exists, silently. See "What the next episode inherits" below.
This episode resumed after B30 landed and re-verified live: a fresh
profile's first-run chooser now produces a session with a real,
non-empty `project_id`.**

> Confirmed by Aryan, live, against the real running app on a fresh
> scratch profile: Done-when #1, #2 (including the compact header), #3
> (via B30, re-verified), #5, #6, #7, #9, #10 (by its absence from any UI
> control this episode built), #11, and #12 all pass. #8 passes for what
> this episode's own UI surfaces can show — tray progress, the result
> card's summary and token count (796, matching B28's own fixture), and
> correctly **nothing** claiming a proposal count — but DemoProvider's
> reply text is scripted, so the real-tool-call-success claim underneath
> it is B28's own script's evidence, not this card's. #9's failure
> presentation was observed directly: the existing `TaskFailed` render,
> no traceback. Done-when #4 and Decision 2, as originally written, are
> superseded by B30 — see "Decisions" below for the corrected account.

---

## Where the code went

```
backend\
└── unchanged by this episode — B30 (spun out mid-session) is the only
    backend change this episode's own evidence needed, and it is its own
    completed doc, not folded into this one.

frontend\internal\
├── app\
│   ├── settings.go    ~ providerCatalog gains {ID: "demo", Label: "Demo
│   │                   mode", NeedsKey: false}; applyModelChoice gains an
│   │                   id=="demo" branch firing initDemoProject;
│   │                   initDemoProject (NEW) joins env.BackendDir with
│   │                   "demo/sample_project" and sends ProjectInit;
│   │                   onDemoProjectInit (NEW) is the reply handler —
│   │                   patches r.activeProvider.ProjectID and re-emits
│   │                   ProviderDefaultSetMsg, guarded against a stale
│   │                   reply (the user switched away before it landed);
│   │                   settingsState gains demoProjectReqID;
│   │                   handleSettingsEnvelope gains the correlation branch
│   ├── render.go      ~ header() gains demoBadge() (NEW) — theme.Warning,
│   │                   the same "must read as distinct in every palette"
│   │                   role as behaviour.go/credential.go/placeholder.go —
│   │                   folded into the right segment before width
│   │                   accounting, and prepended to the title directly in
│   │                   the HeaderCompact branch
│   └── demo_test.go   NEW — catalog row, no-credential-prompt selection,
│                       ProjectInit firing/payload/no-backend-skip, the
│                       reply handler's bind/stale-guard/refusal-handling,
│                       the correlation branch, and the badge's presence
│                       in both header layouts
├── chat\
│   ├── cards.go        ~ statusCardLines gains a 4th, lowest-priority
│   │                   line (Build §5/Decision 2): `w.sessionProvider ==
│   │                   "demo" && w.projectID == ""` → a theme.Warning
│   │                   "demo: no sample project bound" row
│   ├── cards_test.go   ~ 3 new tests for the line above (flags, clears,
│   │                   never fires for a non-demo provider)
│   ├── protocol.go     ~ onSessionList forwards `project_id` onto
│   │                   CreateSession's payload when the stored default
│   │                   carries one (Decision 1's own code — real, unit-
│   │                   tested, and, per a later live finding below, a
│   │                   genuinely live path — just not the one Decision 1
│   │                   originally described)
│   └── providers_test.go ~ 2 new tests for the onSessionList forwarding
│                       above (present when set, absent — never empty-
│                       string — when not)
└── config\state.go     ~ ProviderDefault gains ProjectID string
                        `json:"project_id,omitempty"`

design\tasks\backend\
└── B30-set-session-project.md  NEW (spun out mid-episode; landed and
    separately completed — design/completed/backend/B30-set-session-
    project.md)
```

`chat\providers.go` (`applyProviderDefault`) and the other half of
`chat\protocol.go` (`onSessionProviderUpdated`) are **B30's own edits, not
this episode's** — see that episode's completed doc. They are the reason
Decision 2's corrected account below is true.

---

## Decisions

### 1. `ProjectID` rides on `ProviderDefault`, set by a `ProjectInit` fired at selection time — built exactly as specified; a human cannot win the race it depends on, but the mechanism itself is real and live, with a real gap of its own

Built byte-for-byte as the task doc's Decision 1 describes:
`applyModelChoice` fires `ProviderDefaultSetMsg` immediately on selection
(no perceptible wait), then `initDemoProject` sends `ProjectInit`
separately; `onDemoProjectInit` patches `r.activeProvider.ProjectID` once
the reply lands and only if Demo is still the active provider, then
re-emits `ProviderDefaultSetMsg` with the completed default.
`config.ProviderDefault.ProjectID` and `onSessionList`'s forwarding of it
onto `CreateSession`'s payload are both real, both unit-tested
(`TestOnSessionListForwardsDemoProjectID`,
`TestOnSessionListOmitsProjectIDWhenUnset`), and both correct in
isolation.

**Live verification found a human cannot win the race this mechanism
depends on in its originally-described form** — selecting Demo in the
first-run chooser *before* bootstrap's own `CreateSession` fires. Traced
with real evidence, not reasoned about: `beginBootstrap` fires
`CreateSession` (via `onSessionList`) once per profile, ever, and the
first-run chooser is interactive before the backend has necessarily even
finished its own handshake — but that race is won by a structural head
start (a child-process spawn + two round trips) on **every** launch, for
**every** provider, not a Demo-specific coincidence. A real fresh-profile
repro confirmed this directly: `ProjectInit` succeeded, and the session's
`project_id` was still empty. The real fix needed telling an
**already-created** session its project after the fact — a capability
`CreateSession`'s payload cannot provide by construction. Spun out as
`design/tasks/backend/B30-set-session-project.md` rather than patched
here, the same discipline B23–B29 already established for a mid-episode
backend need found by evidence rather than assumption.

**`onSessionList`'s forwarding code was left in place, unchanged, once
B30 landed — and a second, later live test found it is NOT dead code, as
this doc previously claimed.** It does not need to win the human-vs-
bootstrap race above at all: it fires on **any** `CreateSession` (empty
session list) while `state.json` already holds a stored Demo default
carrying a `ProjectID` — a state a fresh first launch is only one way to
reach. A real repro reached it a second way: on a profile where Demo had
already been selected once (so `state.json` held a real, then-valid
`project_id`), deleting `runtime.db*` only (not `state.json`) and
relaunching reproduced an empty session list again — `onSessionList`
fired `CreateSession` with the stale `project_id` from `state.json`, and
the backend accepted it even though the fresh database's `projects`
table was now empty. The resulting session (`a7b1a727|demo|b93dbd75`)
loaded normally, with no error anywhere. **This is not F10c's or B30's
bug specifically** — neither `CreateSession` nor `SetSessionProvider` has
ever validated `project_id` against the `projects` table, a gap now
recorded as a parking-lot entry (`design/tasks/backend/INDEX.md`) rather
than fixed here, since the fix is a backend validation change outside
this episode's own "no backend changes" rule. **Not observed: what
`@research` actually does in a session bound to a `project_id` with no
matching `projects` row** — `agents\executor.py:487` resolves tool calls
against `session.project_id` directly with no fallback if it is empty,
but a *non-empty, dangling* id was never exercised against that code path
in this episode's own testing, live or otherwise.

**Is the sample project's id deterministic, so re-selecting Demo would
self-heal the dangling binding? Checked, and confirmed: yes, with one
precondition.** `ProjectManager.init()` (`backend\managers\
project_manager.py:70-96`) writes the project's id into
`.point\project.json` **once**, the first time a path is initialized
(line 86-92) — a file that lives under the sample project's own directory
tree, not in `runtime.db`, so deleting `runtime.db*` alone never touches
it. Every later call to `init()` against the same path, including every
subsequent Demo selection (`app\settings.go`'s `applyModelChoice` fires
`initDemoProject` unconditionally on every selection, not only the
first), re-reads that marker and calls `_read_marker_and_resolve`
(`project_manager.py:120-145`). Its own `existing is None` branch
(`project_manager.py:130-144`) is written for exactly this situation — "a
marker with no matching row -- a fresh copy of the database" — and
recreates the `projects` row **under the same id the marker file already
names** (`project_manager.py:136-144`, `Project(id=project_id, ...)`,
`project_id` read from the marker at line 126). So re-selecting Demo
**would** recreate the dangling row under the id the orphaned session
already references, making that binding valid again.

**The precondition this episode did NOT test:** this self-healing only
happens if `ProjectInit` is actually re-fired, which requires a human to
reopen Settings and reselect Demo — a mere relaunch does not trigger it.
`onSessionList`'s `CreateSession` path (the one that produced the
dangling session in the repro above) never calls `ProjectInit` itself; it
only forwards whatever `project_id` `state.json` already has. The exact
repro run for this doc (delete `runtime.db*`, relaunch, quit) stopped
short of reselecting Demo afterward, so the self-healing path above is
confirmed correct **by reading the code**, not by a live repro that
actually watched the dangling row become valid again.

### 2. Project binding reaches a session only if created after Demo is already active — **corrected after B30 landed: the retrofit path now binds it too, on a one-round-trip delay**

**As originally written, this decision was wrong about its own
consequence**, for the identical reason Decision 1 was: it assumed
`SetSessionProvider`'s retrofit path could only ever touch
`provider`/`model`, leaving an already-open session's `project_id`
permanently untouched. That was true before B30 and is no longer true
after it.

**The corrected sequence, confirmed by the code and by B30's own
evidence:** selecting Demo on a profile that already has an open session
fires `applyModelChoice` → `ProviderDefaultSetMsg` with
`ProjectID == ""` (ProjectInit hasn't replied yet) → `chat.
applyProviderDefault` retrofits the open session via `SetSessionProvider`
with no `project_id` in that first call, exactly as before. Once
`ProjectInit`'s reply lands, `onDemoProjectInit` patches
`r.activeProvider.ProjectID` and re-emits a **second**
`ProviderDefaultSetMsg` — and this second call reaches
`applyProviderDefault` again, this time with `d.ProjectID` set, which
B30's own edit to that function now forwards as `project_id` on a
**second** `SetSessionProvider` call. B30's backend handler binds it
unconditionally (no `NULL`-only guard — a deliberate choice, since this
path is always an explicit UI action, never an inferred one), and its
reply's re-read `project_id` flows back into `w.projectID`
(`onSessionProviderUpdated`, B30's other edit). **Retrofitting an
existing session onto Demo now does bind it to the sample project** — not
instantly, but within the one extra round trip `ProjectInit` always takes.

**The honest status line (Build §5) is consequently reachable in a
narrower window than originally described:** only while `ProjectInit`'s
reply has not yet landed (the gap between the two `ProviderDefaultSetMsg`
emissions above), or if `ProjectInit` is refused outright — in which case
`onDemoProjectInit` returns `nil` on a non-`EvProject` reply and the
second, completing `ProviderDefaultSetMsg` never fires, leaving the flag
up correctly, because the binding genuinely failed. It is not, as
originally written, a permanent fact about every retrofit.

### 3. `DemoProvider`/`MockProvider` sharing — unchanged, no action

### 4. `filler_explain.json`/`filler_fill.json` not wired to any UI surface — deferral unchanged

Confirmed by their continued absence from this episode's own Build
section — no `editor_context`-setting control was added, demo-only or
otherwise. Still blocked on F13.

### 5. "2 pending memory proposals" not rendered anywhere — deferral unchanged

Confirmed: the research fixture's result card shows the summary and a
non-zero token count (796, observed live, matching B28's own fixture
evidence) and nothing claiming a proposal count. Still blocked on
whichever episode (F11/B13) exposes `agent_proposals` over the wire.

---

## Live verification

Confirmed by Aryan, against the real running app, scratch
`APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule:

1. **#1/#2** — "Demo mode" appears as a selectable row with no key prompt;
   selecting it makes `r.activeProvider.Provider == "demo"` immediately,
   and the `DEMO` badge appears and survives the compact header.
2. **#3** — on a genuinely fresh profile, selecting Demo in the
   auto-opened first-run chooser ends with the one bootstrap session
   reading `demo|b93dbd75-...` — provider `demo`, a real, non-empty
   `project_id` — confirmed only after B30 landed; the pre-B30 repro of
   this exact step showed the same provider with an **empty**
   `project_id` despite `ProjectInit` having succeeded, which is the
   finding that produced B30 in the first place.
3. **#5/#6/#7** — a chat-reply turn streams a multi-paragraph markdown
   reply with a fenced code block over real time through the ordinary
   transcript; a second turn sent while the first streams shows the
   existing queued-send indicator and both complete; a cancelled turn
   against the cancellation fixture leaves a sensible partial.
4. **#8** — `@research` shows real tray step progress and real
   `tool.started`/`tool.completed` pairs; the result card shows a summary
   and 796 total tokens; nothing on screen claims a proposal count.
   Because `DemoProvider`'s reply text is scripted rather than generated,
   this episode's own observation proves the UI renders what the backend
   sends correctly — it does not independently reprove that the 3 real
   tool calls against the shipped sample project actually succeeded.
   That evidence is B28's own verification script's
   (`manual_b28_demo_mode.py`: "3 real tool.started/tool.completed pairs",
   "every real tool call succeeded against the shipped sample project").
5. **#9** — the error-case fixture's scripted failure surfaces as the
   existing `TaskFailed` presentation; the failure text was observed
   directly, with no traceback.
6. **#10** — `filler_explain.json`/`filler_fill.json` remain unreachable
   from any control this episode built, confirmed by absence rather than
   by attempting and failing at runtime.
7. **#11** — `config.toml` byte-identical before and after every action
   above.
8. **#12** — the existing Providers/Behaviour/About tabs and their test
   suites are unaffected.

Also run directly in this session, not by Aryan: the full
`go test ./frontend/internal/app/... ./frontend/internal/chat/... -count=1
-race` suite (F10c's own Done-when #12 and the general regression gate)
— clean:

```
ok  	github.com/lazyworkoholic/point/frontend/internal/app	...s
ok  	github.com/lazyworkoholic/point/frontend/internal/chat	...s
```

---

## Bug B — the status line's original non-appearance, traced but not fully explained live

`design/tasks/backend/B30-set-session-project.md`'s own Handoff names
this: a repro in which retrofitting an existing session away from Demo
and back did not show the "no sample project bound" flag, with no defect
found in either the writer path or the render condition by static reading
alone. **That remains true after this episode**: the flag's condition is
exactly

```go
if w.sessionProvider == "demo" && w.projectID == "" {   // cards.go:139
    lines = append(lines, th.Style(theme.Warning).Render("demo: no sample project bound"))
}
```

— an **empty-string check on `w.projectID`**, not a comparison against
the sample project's specific id. It is covered by three unit tests
(`TestStatusCardFlagsAnUnboundDemoSession`,
`TestStatusCardSilentWhenDemoIsBound`,
`TestStatusCardNeverFlagsNonDemoProviders`, `cards_test.go`), all passing,
and by direct reading it is correct: it can only ever be silent if
`w.projectID` is already non-empty for that session at render time. **It
has not, however, actually been *seen* on screen in a live run** — every
live confirmation above exercised the line being silent (a bound session)
or exercised the fresh-profile path (#3), neither of which puts the flag
on screen. The leading hypothesis carried over from B30's Handoff — the
specific repro that reported "no flag" had a session whose `project_id`
was already non-empty from something unrelated, making the absence
correct rather than a bug — is still unconfirmed by a captured query, and
Decision 2's own correction above now gives a second, equally plausible
explanation: if that repro's `ProjectInit` round trip had already
completed by the time the flag was checked, the second,
completing `SetSessionProvider` call would have bound the project before
the observer looked, which would also produce "no flag, correctly." Left
open for whoever next needs to force the narrow window Decision 2
describes (switch to Demo and inspect the status line **before** the
`ProjectInit` reply lands, not after) rather than assumed resolved by this
episode's own live rounds, which never isolated that window.

**Status: unit-tested, not seen live.** Seeing it on screen needs a way
to actually force that window open — a fault-injection path for
`ProjectInit` (delay its reply past the point of inspection, or make it
fail outright on demand) — which does not exist in this codebase today;
nothing in `initDemoProject`/`onDemoProjectInit` (`app\settings.go`)
offers a way to hold the reply back or force a refusal from the UI or CLI
side.

---

## What the next episode inherits

- **B30 is its own completed episode** — see
  `design/completed/backend/B30-set-session-project.md`. Nothing about its
  own build is restated here beyond what this doc needs to explain
  Decision 2's correction.
- **`CreateSession`/`SetSessionProvider` accepting a `project_id` that
  matches no `projects` row** — found live (see Decision 1's correction
  above), recorded as a parking-lot entry in
  `design/tasks/backend/INDEX.md`, not fixed here or in B30 (a backend
  validation change, outside this episode's own scope rule). Confirmed
  the dangling id is self-healing **by reading the code**
  (`project_manager.py:120-145`'s marker-recreation path), not by a live
  repro that watched it happen — and what `@research` does against a
  session bound to a dangling id was never observed, live or otherwise.
- **Bug B (above) is still open**, in the narrower sense that no live
  observation has ever actually shown the flag appearing and then
  clearing — only its absence, which is ambiguous between "correctly
  silent" and "the bug." Forcing the narrow pre-`ProjectInit`-reply window
  is the next concrete step, not a code change; nothing in `cards.go`'s
  condition or `onDemoProjectInit`'s sequencing looks wrong by reading.
- **`liveFooter`'s single-line truncation of long `TaskFailed`/`Error`
  text** (found live via the error-case fixture, recorded in
  `design/tasks/frontend/F16-polish.md`, not fixed) is a real, general
  rendering gap, not demo-specific — any sufficiently long error message
  hits it. See that file for the file:line account.
- **Reaching `filler_explain.json`/`filler_fill.json`** still needs F13.
- **A "2 pending memory proposals" display** still needs F11/B13's own
  wire event.
- **A "new session" action** is still absent and still not this episode's
  or B30's to add — Demo mode is what makes its absence visible, not a
  reason either episode should have built one.
