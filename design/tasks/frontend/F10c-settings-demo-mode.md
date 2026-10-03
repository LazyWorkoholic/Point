# F10c — Settings: Demo Mode

**Phase:** P1 · **Batchable:** no · **Depends on:** F10 (Providers tab,
shipped), F10b (Behaviour/About tabs, shipped — unrelated, but Settings'
tab-dispatch structure is now four tabs and this adds no fifth) ·
**Backend:** B28 (shipped — `DemoProvider`, `backend\demo\fixtures\`,
`backend\demo\sample_project\`, all verified)

**Wires B28's already-built, already-verified `DemoProvider` into the
provider chooser/Settings as a selectable row, gives it the persistent
`DEMO` header badge D55 requires, binds a demo session to the shipped
sample project the way the chooser's own "Detect silently, select
explicitly" side effects already work for every other provider, and
confirms — per fixture — which of B28's six fixtures land on an existing UI
surface with zero new rendering code, and which do not and why. No backend
change. No editor, no diff card, no memory-popup UI — none of those exist
yet and this episode does not build stand-ins for any of them.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/frontend/F10c-settings-demo-mode.md`, once it exists)
> claims reality and completion. Code wins on any disagreement.

---

## Why this episode exists, and why it can be scoped now

F10's own "Demo mode" section describes a selectable provider with a
persistent badge, replaying fixtures that cover every feature the demo
shows. F10 itself deferred all of it — INDEX.md's F10-split note #4 calls
it "a provider type that does not exist, plus a shipped sample project and
fixture library… not a Go frontend change." B28 is exactly that backend
work, and it is done: `DemoProvider` is unconditionally registered,
`selectable=True`, six fixtures plus usage-everywhere, a real shipped
sample project, all backend-verified
(`design/completed/backend/B28-demo-mode-provider-fixtures.md`).

B28's own Handoff names three questions it deliberately left open. The
instruction that started this episode asks all three to be resolved
explicitly, not guessed at. They are, below — plus two more that turned up
while checking "wiring each fixture to its correct UI surface" against
what actually exists in this repo today, which turned out not to be a
formality.

### Question 1 — when/how a demo session's `.point\` gets created

**Resolved: automatically, the instant Demo mode is selected — not a
separate explicit step.** This is not a new mechanism; it is the existing
one. F10's chooser already has a side-effect-on-selection shape:
`applyProviderDefault` (`frontend\internal\chat\providers.go:135-148`)
fires the moment any provider is chosen, persists the choice to
`state.json` immediately, and retrofits the *currently open* session via
`SetSessionProvider` if there is one. Demo mode gets the identical
treatment, with one extra call: selecting it also fires `ProjectInit`
against the shipped sample project's path (Question 2), and the returned
project id rides along on the same `config.ProviderDefault` struct
`applyProviderDefault` already persists — see **Decision 1**.

This is **not** the same as guaranteeing every demo session is bound. See
**Decision 2** for the real, evidenced limit on that: binding only ever
reaches a session created *after* the choice exists, because the backend
has no "rebind this session's project" event — only
`SetSessionProvider`, which touches `provider`/`model` and nothing else
(confirmed at `backend\runtime\runtime.py:631-642`).

**Traced before writing any code, as asked: the one-time `CreateSession`
window this depends on is reachable through the first-run chooser
specifically — not through "open Settings on an empty-session profile" as
a general, repeatable action.** Two facts, both read directly rather than
assumed:

1. `beginBootstrap` (`frontend\internal\chat\protocol.go:136-154`) —
   the function that eventually calls `CreateSession` via
   `onSessionList` — **fires once per profile, ever**, by its own doc
   comment: "fires once — the first time the backend is known to be
   connected. A reconnect does not repeat it... re-issuing CreateSession
   on every reconnect would litter the database with empty sessions."
   `onConnected` (`protocol.go:175-185`) only re-enters it on a later
   reconnect if `w.sessionID == ""` — a session that never finished
   *loading*, not an empty session *list*. There is also no
   `DeleteSession` caller anywhere in the Go frontend (`CmdDeleteSession`
   exists in `protocol\types.go` but nothing sends it) and no "new
   session" action (confirmed earlier, Decision 2). So once this fires
   and succeeds — which it does on every normal launch, since the
   backend always eventually connects — **the profile has at least one
   session for the rest of its life.** "A profile with no sessions at
   all" is not a state Settings can be opened against more than once;
   it exists only in the gap between process start and that one
   `CreateSession` call completing.
2. The first-run chooser does not wait for that gap to close — it opens
   on the **very first** `tea.WindowSizeMsg` (`root.go:386,399-401`),
   unconditionally, and `openSettingsPopup` (`settings.go:166-176`)
   renders immediately with whatever state exists, fetching provider
   health *afterward* over its own independent channel
   (`settingsFetchProviders`) rather than waiting for it first. Root's
   `Init()` (`root.go:291-298`) calls `r.client.Start()` — which spawns
   the backend as a **child process** (`uv run python main.py`) — in the
   same call that returns the commands Bubble Tea then races against
   that first `WindowSizeMsg`. `CreateSession` cannot fire before:
   process creation, Python/`uv` startup, the `hello` handshake
   (`protocol.ConnectedMsg`), then `ListProviders`, then `ListSessions` —
   four real steps through a freshly spawned OS process, none of them
   sub-millisecond. A terminal's initial `WindowSizeMsg` has none of
   that latency. So the chooser is open and interactive before the
   backend has necessarily even been spawned, let alone handshaken and
   round-tripped twice.

**Plainly: this is a race the UI wins by construction, not a window
`CreateSession` is blocked from entering.** Nothing in this codebase
pauses bootstrap pending a chooser choice — `beginBootstrap` has no
dependency on `r.settings`/the overlay at all. But it is a race with a
large, structural head start in the UI's favour (an entire child-process
lifecycle vs. one terminal event), and it is the **only** avenue: once
`CreateSession` has fired (normally within at most a few seconds of
launch), there is no later action — through Settings or anywhere else —
that reopens an empty session list to race against again. Done-when #3
and the verification gate below test the first-run chooser specifically,
not "open Settings" as a general action, because the latter cannot
reliably — or even repeatably — exercise this path at all.

### Question 2 — how the frontend discovers the sample project's path

**Resolved: reuse `protocol.FindBackendDir()`, already resolved and
already held by Root — no new field, no new wire event.**
`frontend\internal\protocol\spawn.go:21-65` is the real, shipped mechanism
that finds `backend\` at runtime (env var override, then upward from the
executable, then upward from the working directory — the dev-vs-packaged
split the doc comment there explains). `main.go:109-116` resolves it once
at startup and stores it on `app.Environment.BackendDir`, which Root
already holds as `r.env.BackendDir` (`frontend\internal\app\root.go:217ff`,
read elsewhere e.g. `placeholder.go:192`).

B28's sample project lives at `backend\demo\sample_project\` — a sibling of
`backend\main.py` itself, the same directory `FindBackendDir` already
proves exists and is reachable. The path is therefore
`filepath.Join(r.env.BackendDir, "demo", "sample_project")` — a join onto
machinery this codebase already trusts to locate `backend\`, not a second,
independently-guessed relative path from somewhere else (cwd, the
executable's own directory, etc.) that could silently disagree with where
the running backend actually is.

### Question 3 — `DemoProvider`/`MockProvider` sharing

Confirmed already, no action: B28 kept them independently duplicated
(`DemoProvider._play()`, ~20 lines, not imported from
`providers\mock\provider.py`). Nothing on the frontend side depends on or
needs to know this either way.

### Two more questions, found while checking fixture-to-UI wiring

**Not in B28's Handoff, because they are frontend facts B28 had no reason
to check. Found by reading this repo's actual code, not assumed.**

**4. `filler_explain.json` and `filler_fill.json` cannot be demonstrated by
anything this episode is allowed to build.** `_render_editor_context`
(`backend\managers\context.py:641-705`) — the function that writes the
literal `mode=fill`/`mode=explain` text `DemoProvider._classify()` reads to
pick either fixture — runs **only** when `SendMessage`'s payload carries an
`editor_context` object. That field is optional
(`agents\executor.py:357`, `Optional[EditorContext] = None`) and its own
doc comment says it is "set by the invocation site (F11/F15), never typed
by the model." Checked directly: there is no `editor_context`,
`cursor`, or `selection` anywhere in
`frontend\internal\chat\protocol.go` or any other Go file — confirmed by
grep, not inferred. The only place in this repo that ever builds one is
`cli.py`'s own `--file`/`--select`/`--cursor`/`--mode` flags
(`backend\cli.py:258-336`), which is how B28's *own* verification script
exercised these two fixtures — a CLI-only path, not a UI one.

Building any frontend control that sets `editor_context` — even a
minimal, demo-only "pretend the cursor is at line 7" trigger — would be
building a stand-in for F13 (Editor Core)'s real mechanism before F13
exists, which is exactly what CLAUDE.md's hard rules forbid ("do not build
a stub or partial version of" a later episode's owned capability). So:
**these two fixtures are not wired to any UI surface by this episode, on
purpose.** The instruction that started this episode already anticipated
*how* they should render once reachable — "render it exactly like the
chat-reply and explain-mode fixtures," i.e. plain text, no diff card — and
that guidance is recorded in **Decision 4** for whichever episode (F13,
then this episode's own eventual follow-up) makes them reachable. It costs
nothing to state now and saves that episode from re-deriving it.

**5. "2 pending memory proposals" has no wire event to read it from, and
so no UI surface to show it on.** F10's fixture-coverage table asks for
the `@research` demo to end in "a result with a summary, a token count,
and 2 pending memory proposals." Checked: `backend\runtime\events.py` has
no event mentioning `proposal` or `agent_proposals` anywhere — B28's own
verification script (`manual_b28_demo_mode.py`) only ever observed the
count via a direct `sqlite3` query against `agent_proposals`, never over
the wire. This is not a demo-specific gap: F9a's own result card
(`frontend\internal\app\resultcard.go:42-60`) already documents "save to
memory" as unreachable for the identical reason, and F11 (the memory
popup) is what eventually owns this. **The research fixture's result card
shows the summary and the token count — both already rendered by the
existing tray/result-card machinery — and nothing claiming a proposal
count, because nothing can confirm one exists.** See **Decision 5**.

---

## Decisions

> **Decision 1 below was found wrong during live verification, by
> tracing real evidence, not by review.** The "ride along on
> `CreateSession`'s payload" mechanism it describes is real code, passes
> its own unit tests, and never actually binds a session in the live app
> — `chat\providers.go`'s own prior F10-era comment already proves why:
> "the session already open was created **before this choice existed**,"
> true for every provider, which means the one case Decision 1 needed
> (a human choosing Demo before bootstrap's own `CreateSession` fires)
> does not realistically occur. Left in place below, struck through in
> spirit but not in text, because the code it describes is real and the
> reasoning error is itself worth keeping visible rather than quietly
> rewritten. The actual fix is
> `design/tasks/backend/B30-set-session-project.md` — a small backend
> episode, found and spun out the same way B29 was, extending
> `SetSessionProvider`'s existing retrofit call (which already fires
> reliably, with no race, every time a corrected default arrives) to also
> carry `project_id`. Decision 2, below, turns out to already have been
> pointing at the real mechanism — it just under-estimated how
> universally necessary closing its own gap would turn out to be.

### 1. `ProjectID` rides on the existing `ProviderDefault`, set by a `ProjectInit` fired at selection time — **does not work; see the note above**

`config.ProviderDefault` (`frontend\internal\config\state.go:97-103`)
gains one field:

```go
type ProviderDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	// ProjectID is Demo mode's: the sample project's id, set once
	// ProjectInit's reply lands. Empty for every other provider — nothing
	// else ever writes it.
	ProjectID string `json:"project_id,omitempty"`
}
```

Picked over a separate `DemoProjectID` field on `State` because it needs
to flow through exactly one existing pipe: `applyProviderDefault`'s
persist-to-`state.json` call, and `onSessionList`'s own
`CreateSession` payload builder
(`frontend\internal\chat\protocol.go:384-392`), which already does
`if d.Effort != "" { payload["effort"] = d.Effort }` — `ProjectID` gets
the identical one-line treatment,
`if d.ProjectID != "" { payload["project_id"] = d.ProjectID }`. A second
field on `State` would need its own plumbing through both of those call
sites for no additional clarity.

**Sequencing, since `ProjectInit` is a round trip and the provider choice
itself should not visibly wait on it:** selecting Demo (`applyModelChoice`
in `settings.go`, `id == "demo"`) emits `ProviderDefaultSetMsg`
immediately, exactly as every other provider does today — the badge and
`r.activeProvider` update with no perceptible delay. Immediately
afterward, in the same call, it also fires `ProjectInit` via
`settingsSend` (mirroring `settingsSubmitKey`'s own fire-and-correlate
shape, `settings.go:724-736`), recording the request id in a new
`r.settings.demoProjectReqID`. `handleSettingsEnvelope`
(`settings.go:827-856`) gains one more branch matching it to the `Project`
reply; the handler reads `payload.project.id`, and — **only if
`r.activeProvider` is still non-nil and still `Provider == "demo"`** (the
user may have switched away before the reply landed) — patches
`r.activeProvider.ProjectID` and re-emits `ProviderDefaultSetMsg` with the
now-complete default, which `applyProviderDefault` persists exactly as it
would any other change to the default.

`ProjectManager.init()` (`backend\managers\project_manager.py:70-96`) is
documented as idempotent — "If the folder is already initialized, this is
just resolve() plus a touch — init is not destructive" — so firing it on
every Demo selection, including re-selecting Demo after already having
done so, is deliberately cheap and side-effect-free beyond the touch.

### 2. Project binding reaches a session only if it is created *after* Demo is already the active default — this is a real, inherited limit, not solved here (**now confirmed: this is the ONLY case that ever occurs — see B30**)

**Stated plainly because it is easy to assume otherwise and be wrong.**
`CreateSession` is reachable from exactly one place in the shipped
frontend: `onSessionList`, and only when the session list comes back
**empty** (`protocol.go:345-392`). There is no "new session" action
anywhere in this codebase — checked by grep across
`frontend\internal`, zero matches for anything resembling one. So the
`project_id` payload field Decision 1 adds only ever has an effect in one
real scenario: a genuinely fresh profile (no sessions, ever) on which
Demo is chosen **before** that one `CreateSession` fires — see the
bootstrap-ordering trace above for why this is realistically the
first-run chooser and nothing else. (Not "every session since
deleted/archived off the list" either: `CmdDeleteSession` and
`CmdArchiveSession` both exist on the wire but neither has a caller
anywhere in the Go frontend — checked by grep, zero matches. There is no
UI path back to an empty session list once one exists.)

For every other case — Settings opened on a profile that already has at
least one session, Demo selected there — `applyProviderDefault`'s existing
retrofit path runs instead: `SetSessionProvider` updates the **currently
open** session's `provider`/`model` columns immediately (unchanged,
already shipped, already true for every provider today), and the `DEMO`
badge comes on. But that session's `project_id` is whatever it already
was — `_handle_set_session_provider`
(`backend\runtime\runtime.py:631-642`) touches only two columns, and no
other wire event exists to change a session's project after creation.
**The badge is honest about the provider. It says nothing about whether
this particular session can see the sample project**, and today, for an
already-open session, it cannot.

This is not new to Demo — it is the same fact every provider's retrofit
already lives with (switching to Ollama via Settings never moves the open
session into some other project either); Demo is simply the first provider
for which project binding is part of what "working" means. **Not fixed
here, and — unlike this section's first draft assumed — not an edge
case either: live tracing (bootstrap-ordering section above, confirmed
again under Bug A's live repro) shows the retrofit path is the ONLY path
that ever actually runs, for every session, always.**
`design/tasks/backend/B30-set-session-project.md` closes this —
extending `SetSessionProvider`'s existing retrofit call to also carry
`project_id`, reusing `session_manager.set_project()` (already built for
B12, never exposed until B30). Not this episode's job to build (backend
change, out of F10c's own declared scope), but it is the thing F10c's
own Done-when #3/#4 are blocked on.

**What this episode does instead, honestly:** when `applyProviderDefault`
retrofits the currently open session to Demo (i.e., the `w.sessionID != ""`
branch runs, not the fresh-`CreateSession` one), set a status line saying
so — reusing the existing `w.status`-equivalent surface Root already uses
for "provider is not reachable right now" (`settings.go:554`) — rather
than letting the badge imply a capability this specific session does not
have.

### 3. `DemoProvider`/`MockProvider` sharing — confirmed, no action (Question 3)

Nothing for the frontend to do either way; recorded only so a later reader
does not wonder why this doc is silent on it.

### 4. `filler_explain.json`/`filler_fill.json` are not wired to any UI surface this episode — deferred to whichever episode gives the frontend a way to set `editor_context`

Per **Question 4** above. Recorded for that future episode (most likely
F13, then a follow-up here): **when** `editor_context` becomes settable
from the Go frontend, both fixtures render through the **plain assistant-
message path already shipped** — `@filler-explainer` has no mode-specific
rendering today (`frontend\internal\chat\input_render.go` treats it like
`@general`; grep confirms no `explain`/`fill`-specific UI anywhere in
`chat\`) — so `filler_fill.json`'s JSON-shaped text reply needs **no diff
card, no new rendering code, nothing beyond what chat_reply.json already
exercises**. This matches the instruction that started this episode
exactly ("render it exactly like the chat-reply and explain-mode
fixtures") and B28's own framing (a plain text reply today, a true,
zero-rework `FillProposal` the day B24 ships) — there is simply no way to
*reach* either fixture from this repo's UI yet, which is a different
problem from how to render them once reached.

### 5. "2 pending memory proposals" is not rendered anywhere — confirmed absent, not approximated

Per **Question 5**. The research fixture's demo turn is wired exactly as
far as the existing tray/result-card machinery already reaches: step
progress (`ProgressUpdated` → F9a's tray), real `tool.started`/
`tool.completed` pairs against the shipped sample project, a final reply
and token count in the result card. No proposal count is added anywhere —
not a guessed number, not a hardcoded "2", nothing — because nothing on
the wire confirms one. `agent_proposals` rows still exist in the database
exactly as B28 wrote them; there is simply no UI surface reading them yet.

---

## Build

### 1. The provider chooser gains a `demo` row

`frontend\internal\app\settings.go:46-58`'s `providerCatalog` gains:

```go
{ID: "demo", Label: "Demo mode", NeedsKey: false}
```

The comment immediately above the table ("Demo mode is deliberately absent
— out of this episode's scope") is removed; everything below it about why
the catalog has to be a frontend constant at all is unchanged.

Nothing else about `selectProvider`/`finalizeProviderSelection`/
`applyProviderChoice`/`applyModelChoice`
(`settings.go:538-662`) needs branching on provider identity to make the
row *selectable* — `DemoProvider.health()` always reports healthy (B28
§1), so `usable := known && h.status == "healthy"` is true for `demo` the
moment `ListProviders`' reply includes it (which it already does — B28
registers `demo` unconditionally, confirmed `cli.py provider list` output
in B28's own manual test log: `registered: ['ollama', 'demo']`), and
`list_models()` returning its one fixed `point-demo-v1` pseudo-model means
`applyProviderChoice`'s "pick the lexicographically-first model" quick
default already picks the only model there is. The **one** place that
does need an `id == "demo"` branch is `applyModelChoice` — see Build §2.

### 2. `ProjectInit` fired from `applyModelChoice`, correlated in `handleSettingsEnvelope`

```go
func (r *Root) applyModelChoice(id string, model providerModelInfo) tea.Cmd {
	def := config.ProviderDefault{Provider: id, Model: model.id}
	r.activeProvider = &def
	r.settings.activeModelSupportsEffort = model.supportsEffort
	r.rebuildSettingsProvidersTab()
	r.status = fmt.Sprintf("%s (%s) is now the active provider", providerLabel(id), model.id)
	cmds := []tea.Cmd{func() tea.Msg { return ProviderDefaultSetMsg{Default: def} }}
	if id == "demo" {
		cmds = append(cmds, r.initDemoProject())
	}
	return tea.Batch(cmds...)
}

func (r *Root) initDemoProject() tea.Cmd {
	path := filepath.Join(r.env.BackendDir, "demo", "sample_project")
	reqID, ok := r.SettingsSendFn(protocol.CmdProjectInit, map[string]any{"path": path})
	if !ok {
		return nil
	}
	r.settings.demoProjectReqID = reqID
	return nil
}
```

`settingsState` (`settings.go:95ff`) gains `demoProjectReqID string`,
following the exact naming convention every other in-flight correlation id
on that struct already uses (`providersReqID`, `setKeyReqID`,
`doctorReqID`).

`handleSettingsEnvelope` (`settings.go:827-856`) gains:

```go
if env.CorrID == r.settings.demoProjectReqID {
	r.settings.demoProjectReqID = ""
	return r.onDemoProjectInit(env)
}
```

```go
func (r *Root) onDemoProjectInit(env protocol.Envelope) tea.Cmd {
	if env.Type != protocol.EvProject {
		return nil // refused — Demo stays selected, just without binding
	}
	var p struct {
		Project struct{ ID string `json:"id"` } `json:"project"`
	}
	if err := env.UnmarshalPayload(&p); err != nil || p.Project.ID == "" {
		return nil
	}
	if r.activeProvider == nil || r.activeProvider.Provider != "demo" {
		return nil // switched away before this landed — Decision 1
	}
	def := *r.activeProvider
	def.ProjectID = p.Project.ID
	r.activeProvider = &def
	return func() tea.Msg { return ProviderDefaultSetMsg{Default: def} }
}
```

### 3. `config.ProviderDefault` gains `ProjectID` (Decision 1); `onSessionList` forwards it

`frontend\internal\config\state.go:97-103` — add the field shown in
Decision 1.

`frontend\internal\chat\protocol.go:384-392` — add, parallel to the
existing `Effort` line:

```go
if d.ProjectID != "" {
	payload["project_id"] = d.ProjectID
}
```

### 4. The `DEMO` badge

`frontend\internal\app\render.go`'s `header()` (lines 122-149) gains a
badge, visible whenever `r.activeProvider != nil && r.activeProvider.Provider
== "demo"`, for as long as that stays true — D55's "it must announce
itself," matching this codebase's existing pattern of using
`theme.Warning` for anything that must read as distinct in every palette
regardless of theme (`behaviour.go:134`, `credential.go:200`,
`placeholder.go:204` are all the same role for the same reason). Exact
placement (folded into `left`/`right`, or a fourth segment with its own
width accounting) and the compact-header case (`l.HeaderCompact`, line
128 — currently just centres the title) are this episode's call to make;
record which in the completed doc. Whatever the placement, it must survive
`l.HeaderCompact` — the badge announcing itself is the one requirement
F10 states explicitly ("for its entire duration"), compact layout or not.

### 5. Retrofit-without-binding gets an honest status line (Decision 2)

Not hooked into `applyProviderDefault`'s retrofit branch specifically —
the fact worth surfacing ("this session is on Demo but has no project
bound") is already fully determined by state `chat.Workspace` tracks
regardless of HOW the session got there: `w.sessionProvider == "demo" &&
w.projectID == ""` (`w.projectID` is F8's own field, set from the
session's `project_id` — `protocol.go:433`). Deriving it in
`statusCardLines` (`cards.go`, the Status card's existing "provider ·
model" row) as a fourth, lowest-priority line is simpler than threading a
one-shot flag through the retrofit call site, and it stays correct even
in an edge case the retrofit-specific version would miss: if some future
path ever created a demo session with no project bound some other way,
this still catches it, because it is a fact about the session's current
state, not about the call that most recently changed it — the same
build-time-vs-render-time discipline F9a's own doc already states as a
rule for this codebase.

### 6. Fixture-to-UI-surface confirmation — verification, not new code, for five of the six fixtures

`chat_reply.json`, `queued_turn.json`, `cancellation.json`,
`research_tool_loop.json`, `error_case.json` all render through
machinery that already exists and already ships: the transcript (F3),
the queued-send indicator and cancel path (F5), the tray and result card
including tool-call progress (F9a), and the generic `TaskFailed` error
render. **This episode's job for these five is to confirm each one
actually demonstrates what F10's fixture table asks for through the real
app, not to write new rendering for any of them** — see Done-when and the
verification gate below. `filler_explain.json`/`filler_fill.json` are
explicitly not in this list — Decision 4.

---

## Do NOT

- **Do not touch `backend\` at all.** Every event this episode drives
  (`ListProviders`, `ListProviderModels`, `ProjectInit`, `CreateSession`,
  `SetSessionProvider`) already exists and is already verified by F10 or
  B28. If something here turns out to need a backend change, stop and say
  so rather than extending either episode's scope informally.
- **Do not build any editor_context-setting UI**, demo-only or otherwise —
  Decision 4/Question 4. That is F13's capability; a demo-only stand-in is
  exactly the stub CLAUDE.md's hard rules forbid.
- **Do not invent a proposal-count display, guessed, hardcoded, or
  otherwise** — Decision 5/Question 5. If a number cannot be confirmed
  from the wire, show nothing rather than something that might be wrong
  the day B13 lands a real one.
- **Do not add a `SetSessionProject`-style wire event or a "new session"
  action.** Both would close Decision 2's real gap, but both are new
  backend/frontend surface well beyond "wire Demo mode into the chooser,"
  and neither is this episode's to invent on the spot.
- **Do not build a diff-card rendering for `filler_fill.json`.** Stated
  directly by the instruction that started this episode: it is a plain
  text reply, rendered exactly like `chat_reply.json` and
  `filler_explain.json`, whenever it becomes reachable at all.
- **Do not touch `MockProvider`, `POINT_MOCK`, or anything under
  `backend\tests\fixtures\`.** Unrelated to this episode, same as it was
  to B28.
- **Do not redesign the Settings popup's tab structure.** `Tabs` stays at
  four (`Providers · Appearance · Behaviour · About`); Demo is a new
  **row** inside the existing Providers tab, not a new tab.

---

## Done when

1. Opening the provider chooser (first run or Settings) shows "Demo mode"
   as a selectable row, with no key prompt — selecting it never opens
   `CredentialPrompt`.
2. Selecting Demo mode makes `r.activeProvider.Provider == "demo"`
   immediately (no visible wait), and the `DEMO` badge appears in the
   header and stays visible for as long as Demo remains the active
   provider, including across a popup close/reopen and a window resize
   into/out of `HeaderCompact`.
3. **On a genuinely fresh profile's first launch ever**, selecting Demo
   mode in the auto-opened first-run chooser — before the session list
   has resolved — results in that profile's one bootstrap session having
   `project_id` set to the shipped sample project's. Confirmed via
   `GetSession`'s reply (or direct inspection of `sessions`), not assumed
   from the UI alone. Tested on the **first-run chooser specifically**,
   not "open Settings on an empty-session profile" — the bootstrap-
   ordering trace above establishes that the latter is not a reliably (or
   even repeatably) reachable state: `CreateSession` fires at most once
   per profile, ever, and nothing in this codebase can get a profile back
   to zero sessions afterward.
4. **On a profile with an existing session**, selecting Demo mode via
   Settings retrofits that session's provider (unchanged existing
   behavior) and shows the honest "not bound to the sample project" status
   line from Build §5 — it does **not** silently claim the session can use
   the sample project.
5. A chat-reply turn against Demo streams a multi-paragraph markdown
   reply with a fenced code block over several real seconds, through the
   ordinary transcript — no new rendering code involved.
6. Sending a second turn while the first streams shows the existing
   queued-send indicator; both complete.
7. A turn against the cancellation fixture, cancelled mid-stream through
   the existing cancel key, leaves a sensible partial in the transcript.
8. `@research` against Demo shows real step progress and real
   `tool.started`/`tool.completed` pairs in the tray, and the result card
   shows the final summary and a non-zero token count — and shows
   **nothing** claiming a proposal count (Decision 5).
9. A turn against the error-case fixture surfaces as the existing
   `TaskFailed` error presentation, not a crash or a raw traceback.
10. `filler_explain.json` and `filler_fill.json` are not reachable from
    any UI control this episode adds — confirmed by their absence from
    this doc's own Build section, not by trying and failing at runtime.
11. `config.toml` is byte-identical before and after every action above —
    F10's own Done-when #6, unregressed by a fifth provider row.
12. The Providers tab's existing rows (ollama, openai, anthropic, gemini,
    openrouter) and F10b's Behaviour/About tabs behave exactly as before —
    confirmed by running their existing test suites unmodified.

---

## Manual verification (PowerShell)

**Per CLAUDE.md's hard rule: never rename or delete the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` by name.** One PowerShell
session, env vars set once, both `go run` and `uv run python main.py`
launched inside it, cleanup at the end.

Eleven steps below, numbered to match Done-when, run **in this exact
order** — the chooser must be the very first thing touched after launch
(Done-when #3 depends on nothing having created a session yet), and the
`config.toml` snapshot must be taken before that, since it may not exist
at all until something creates it.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f10c"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData      = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch

    # --- 0 (setup for #11): snapshot config.toml BEFORE anything runs.
    # On a genuinely fresh profile this most likely does not exist yet —
    # that absence is itself the "before" state the final compare needs.
    $beforeToml = Join-Path $env:TEMP "before-f10c.toml"
    Copy-Item "$scratch\Point\config.toml" $beforeToml -ErrorAction SilentlyContinue

    cd D:\Projects\Point\backend
    # (terminal A, inside this same $env:APPDATA/$env:LOCALAPPDATA session)
    #     uv run python main.py
    cd D:\Projects\Point
    # (terminal B, inside this same session)
    #     go run .\frontend\cmd\point

    # --- 1-3: the FIRST thing done after launch, on this genuinely fresh
    # profile, is answering the auto-opened first-run chooser — nothing
    # else is clicked first. Select "Demo mode".
    # EXPECT (#1): no key prompt opens.
    # EXPECT (#2): the DEMO badge appears in the header immediately, with
    # no visible wait, and stays up across a resize and a Settings
    # close/reopen.
    #
    # Once the app has settled (a few seconds), confirm #3 from a SECOND,
    # ordinary PowerShell window that has NOT touched $env:APPDATA/
    # $env:LOCALAPPDATA (read-only inspection of the scratch DB; it does
    # not need the override, only the path):
    sqlite3 "$scratch\Point\runtime.db" `
        "select s.id, s.provider, s.project_id, p.root_path from sessions s " + `
        "left join projects p on p.id = s.project_id order by s.created_at desc limit 1;"
    # EXPECT: provider = demo, project_id NOT NULL, root_path ending in
    # backend\demo\sample_project.

    # --- 5-7: in the running app, send a plain chat message (chat_reply),
    # send a second one immediately while the first still streams
    # (queued_turn), then send a third and cancel it mid-stream
    # (cancellation).
    # EXPECT (#5): the first streams a multi-paragraph markdown reply with
    # a fenced code block over several real seconds.
    # EXPECT (#6): the existing queued-send indicator shows while the
    # first is still streaming; both eventually complete.
    # EXPECT (#7): cancelling the third leaves a partial reply ending at a
    # complete sentence, not a mid-word cut.

    # --- 8: send "@research summarize the sample project".
    # EXPECT: the tray shows 4 real PROGRESS_UPDATED steps and real
    # tool.started/tool.completed pairs; the result card (opened from the
    # tray) shows a summary and a non-zero token count; nothing on screen
    # claims a proposal count of any kind.

    # --- 9: send the error-case fixture's scripted trigger phrase
    # ("trigger the scripted failure" — confirm the exact phrase against
    # backend\demo\fixtures\error_case.json / DemoProvider._classify at
    # implementation time).
    # EXPECT: the existing TaskFailed error presentation, no traceback.

    # --- 10: confirm filler_explain.json/filler_fill.json are NOT
    # reachable from anything built this episode. Try sending
    # "@filler-explainer explain this" with no file attached and no
    # editor open.
    # EXPECT: this does not hit either Filler fixture (no editor_context
    # exists to route on) — it is not expected to produce a sensible
    # result at all under Demo; the point of this check is that nothing
    # built THIS episode offers a way to make it do so, not that the
    # agent's unconditioned behavior under Demo is itself meaningful.

    # --- 4: the EXISTING-session case. Quit the app (the one session
    # from steps 1-9 above is still there). Relaunch
    # `go run .\frontend\cmd\point` against the SAME $scratch. The
    # session list is no longer empty, so onSessionList opens the
    # existing session instead of creating one; in Settings, switch the
    # active provider away from Demo and back to it (or to a different
    # provider first, if Demo was already active) to exercise the
    # RETROFIT path deliberately.
    # EXPECT: the session's provider retrofits immediately
    # (SetSessionProvider, unchanged existing behavior), the honest
    # "not bound to the sample project" status line from Build §5
    # appears, and the session's project_id is UNCHANGED from step 3's
    # reading:
    sqlite3 "$scratch\Point\runtime.db" `
        "select id, provider, project_id from sessions order by created_at desc limit 1;"
    # EXPECT: provider = demo (or whatever was last selected), project_id
    # IDENTICAL to the value read after step 3 — not null-ed, not
    # re-pointed, not re-run through ProjectInit.

    # Quit the app cleanly before the file compare below.

    # --- 11: config.toml must be exactly what it was before step 1.
    Compare-Object (Get-Content "$scratch\Point\config.toml" -ErrorAction SilentlyContinue) `
                   (Get-Content $beforeToml -ErrorAction SilentlyContinue)
    # EXPECT: no output, AND Test-Path on both sides agrees (both absent,
    # or both present and identical) — neither "did not exist, now does"
    # nor the reverse counts as a pass.

    # --- 12: no regression to F10/F10b's existing Settings tabs, plus
    # whatever new tests this episode adds for Build §1-3/§4.
    go test .\frontend\internal\app\... .\frontend\internal\chat\... -count=1 -race
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

**Status: blocked on `design/tasks/backend/B30-set-session-project.md`.**
Live verification found Decision 1's binding mechanism never actually
runs (see the note at the top of Decisions) — `GetSession`/direct-DB
evidence from a real fresh-profile repro showed `project_id` empty
despite `ProjectInit` succeeding. Done-when #3 and #4 do not pass today.
Everything else built in this episode (the catalog row, the badge, the
`ProjectInit` firing/correlation machinery, the status line's own render
condition) is correct and does not need to change once B30 lands — B30's
own Build section only touches `chat\providers.go`/`chat\protocol.go`'s
two call sites, both already pointed at from this doc.

Once B30 lands: re-verify Bug A's exact repro (fresh profile, first-run
chooser, Demo mode, direct query), re-verify Bug B's repro WITH the query
this time (switch an existing session away from Demo and back via
Settings, query immediately after quitting — no defect was found by
static tracing in the writer/render path, so the leading hypothesis is
that `project_id` was already non-empty for an unrelated reason in that
specific repro, not a second bug, but this needs the query to confirm,
not an assumption), and only then is Step 10 (`@research`) safe to test,
since it resolves tool calls against `session.project_id` directly with
no fallback if empty (`agents\executor.py:487`).

Five of B28's six fixtures were otherwise confirmed working end to end
through existing UI with no new rendering code, independent of this
blocker; the sixth and seventh (`filler_explain.json`/`filler_fill.json`)
are confirmed *not* reachable, with the reason on record (Decision 4).

Not done, and not this episode's to do:

- **Reaching `filler_explain.json`/`filler_fill.json` at all** — needs
  F13 (Editor Core) to give the frontend any way to set `editor_context`.
  Once it does, re-read Decision 4: the rendering side needs nothing new.
- **A "2 pending memory proposals" display** — needs a wire event
  exposing `agent_proposals` (or whatever F11/B13 end up calling it).
  Decision 5's absence is deliberate, not a placeholder to fill in later
  without re-checking what the real event ends up shaped like.
- **A "new session" action**, or anything beyond B30's own narrow
  extension — B30 closes Decision 2's gap exactly as far as
  `SetSessionProvider`'s existing retrofit already reaches (the
  currently open session); it does not add a way to create a second
  session in a profile that already has one. Flagged here because Demo
  mode is what makes that absence visible, not because either this
  episode or B30 was meant to close it.
- **A provider-cap-style per-provider row for anything Demo-related** —
  there is nothing analogous to add; Demo has exactly one fixed
  pseudo-model and no key, which is why Build §1 needed almost no new
  branching.
