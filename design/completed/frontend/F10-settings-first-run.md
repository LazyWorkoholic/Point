# F10 — Settings, Providers, First Run (narrowed) 🟡

**The provider chooser (first run), credential entry and validation, the
no-provider state, and the Providers + Appearance tabs of Settings all
ship. `point doctor`, the Behaviour tab's live effect, and demo mode are
NOT built — each blocked on a backend episode that does not exist yet
(`design/tasks/frontend/INDEX.md`'s F10 split note, items 2–4). This row
stays 🟡 until F10b and F10c land.**

> Confirmed by Aryan, scratch `APPDATA`/`LOCALAPPDATA` profile per
> CLAUDE.md's hard rule: the fresh-machine simulation (provider chooser
> auto-opens, Ollama detected but not pre-selected, no traceback), the
> disabled-input state after Esc, wrong/right key validation (including a
> trailing-newline paste), `config.toml` byte-identical before/after
> changing Settings (confirmed by file size — `Compare-Object` against an
> empty file returns a spurious null-reference error that looks like a
> diff and is not one), theme persistence across restart, and the
> first-launch hang **confirmed fixed 5/5 clean across independent
> fresh-profile launches**, with its regression test
> (`TestSettingsRetriesTheProvidersFetchOnceConnected`) confirmed passing
> under its real name.
>
> Four verification rounds, each finding real defects — this file records
> them in the order they were actually found and fixed, including two
> fixes that turned out NOT to be the cause of the headline bug but are
> kept anyway as real, independent improvements.

---

## Where the code went

```
backend\
├── providers\ollama\provider.py   list_models(): metadata.size_bytes /
│                                  parameter_size, forwarded from Ollama's
│                                  own /api/tags response (it already sends
│                                  these; the provider just was not reading
│                                  them) — the settings popup's per-model
│                                  rows use this for real on-disk sizes.
│                                  health(): + except httpx.TimeoutException
│                                  (kept; not the fix for the hang — see
│                                  "The first-launch hang" below).
│                                  __init__: base_url fallback literal
│                                  127.0.0.1, not "localhost" (kept, same
│                                  reason).
├── providers\models.py            ProviderConfig.from_env: ollama's default
│                                  base_url is the literal 127.0.0.1 (kept).
└── runtime\runtime.py             _handle_list_providers: per-provider
                                   timing (warning if a health check takes
                                   over a second) — diagnostic, written
                                   while chasing the hang, kept regardless.

frontend\internal\
├── app\
│   ├── settings.go       NEW — the Settings popup: the provider catalog,
│   │                     ListProviders/ListProviderModels/
│   │                     SetProviderApiKey round trips, Providers tab
│   │                     items/decor/selection, the first-run auto-open
│   │                     entry point (openSettingsPopup)
│   ├── credential.go     NEW — CredentialPrompt, the masked key-entry
│   │                     overlay
│   ├── appearance.go     NEW — the Appearance tab: theme/background/mouse
│   ├── workspace.go      + ThemeAware — a workspace holding its own theme
│   │                     copy needed telling when Settings changes it
│   ├── placeholder.go    + Diagnostics.NoProviderConfigured
│   ├── root.go           + activeProvider, needsProviderSetup, settings,
│   │                     mouseEnabled, SettingsSendFn; the first-run
│   │                     trigger (WindowSizeMsg case); ConnectedMsg now
│   │                     also calls retrySettingsIfAwaiting; EnvelopeMsg
│   │                     dispatches to handleSettingsEnvelope;
│   │                     submitCredentialMsg case
│   ├── actions.go        + settings.open (leader "s")
│   └── settings_test.go  NEW
├── chat\
│   ├── chat.go            + SetTheme (invalidates the render cache,
│   │                      bumps themeGen); + themeGen field
│   ├── providers.go       providerMissing reads a DiagnosticsMsg push,
│   │                      not w.state.Provider directly; applyProviderDefault
│   │                      also retrofits the OPEN session via
│   │                      SetSessionProvider; applyUIOverride MERGES a
│   │                      patch instead of replacing
│   ├── protocol.go        CreateSession's payload carries the stored
│   │                      default when one exists; + querySetSessionProvider
│   │                      / onSessionProviderUpdated; + ProviderDefaultSetMsg
│   │                      / UIOverrideSetMsg cases
│   ├── render.go          queryLines: the no-provider banner
│   ├── stream.go          inputLocked also locks on providerMissing;
│   │                      + providerMissingReason
│   ├── stream_render.go   renderLive's three freshness checks also compare
│   │                      renderedThemeGen
│   ├── theme_live_test.go NEW
│   └── providers_test.go  + the SetSessionProvider retrofit tests
├── config\state.go        + ProviderDefault, UIOverride
└── cmd\point\main.go       applies state.json's UI override on top of
                            config.toml before building the theme; the
                            mouse program option is now conditional
```

Six existing test files (`app\app_test.go`, `app\picker_test.go`,
`app\tray_test.go`, `chat\main_test.go`, `chat\diagnostics_race_test.go`,
`chat\overlay_concurrency_test.go`, `chat\root_toggle_test.go`) gained a
`testProviderDefault`/`State: testProviderDefault` fixture — see "A
regression this episode caused in its own tests" below.

---

## The narrowed scope, as built

Everything the wire protocol supports end to end now that B25 has landed:

- **Provider chooser (first run).** Auto-opens exactly when
  `opts.State.Provider == nil` — nothing has been chosen through Point's
  own UI yet. Ollama is detected (via `ListProviders`) but never
  pre-selected; the user picks explicitly, every time.
- **Credential entry and validation.** Masked, paste-tolerant (trims
  whitespace/newlines, absorbs a Windows paste's trailing Enter the same
  way `chat`'s own `pasteSettle` does), validated via `SetProviderApiKey`
  — the real backend error on a bad key, `"valid"` on a good one, now
  durable across a restart (B25).
- **The no-provider state.** Input genuinely disabled (not just a refused
  send) with the reason and the fix inline — `"⚠ no provider
  configured — ctrl+w s to set one up"` — in the Query Area's own place,
  the same pattern F5's queue-full lock established.
- **Providers tab.** One row per known provider (hardcoded catalog —
  `ListProviders` only reports what is already *registered*, never
  "known but not yet configured"; see `settings.go`'s own doc), each
  provider's installed/available models as their own indented rows once
  fetched, an Effort row once there is an active default.
- **Appearance tab.** Theme (live preview, persists via a `state.json`
  override — `config.toml` is never written), background mode, mouse
  on/off (`tea.EnableMouseCellMotion`/`tea.DisableMouse`, live).

**Deferred, unchanged from the original split** (`design/tasks/frontend/
INDEX.md`'s F10 split note, items 2–4 — item 1, the credential-persistence
gap, was B25's and is now closed):

| # | Item | Blocked on | Evidence |
|---|---|---|---|
| 2 | `point doctor` / the About tab | a `doctor` wire event — it is a separate CLI subcommand today | `backend\cli.py:1194` prints straight to stdout, a different process invocation |
| 3 | Behaviour tab's live effect | a runtime-override/reporting layer | `backend\runtime\scheduler.py:269` reads `chat.queue_depth` live via `appconfig.config_value()`; no in-memory override layer, no wire event to push one, and `state.json` is Go-only — the backend process never reads it |
| 4 | Demo mode | a new provider type + fixture library + sample project, none of which exist | needs an apply target F09a already found missing (no Editor workspace, F13/F9b's job) plus a provider distinct from the test-only `MockProvider` (`backend\providers\mock\provider.py:87`) |

No Behaviour tab was built at all this session — not even a read-only
stub — since the narrowing instructions this session actually ran under
scoped the Build section to exactly Providers + Appearance.

---

## The Settings popup's tab structure, and how a new tab gets registered

**One Popup, built in one place (`newSettingsPopup`, `settings.go`), with
`Decor`/`OnSelect`/`Hint` each dispatching by the selected item's ID
PREFIX — never by which tab is active.** This is what makes adding a tab
purely additive, for both F11 (a Memory tab) and F10b (giving the
Behaviour tab a real effect):

```go
Tabs: []PopupTab{
    {Label: "Providers",  Items: newProvidersTabItems(r)},  // providersTabIndex  = 0
    {Label: "Appearance", Items: newAppearanceTabItems(r)}, // appearanceTabIndex = 1
},
```

Four things an F11/F10b session needs to know before adding a third tab:

1. **Give the new tab's items their own ID prefix** (`"memory:"`,
   `"behaviour:"`, …) and add one `case strings.HasPrefix(id, "...")`
   each to `settingsDecor` and `onSettingsSelect`. Every existing case
   stays untouched — this is how F10b adds real Behaviour-tab controls
   without touching a line of this session's Providers/Appearance code,
   and how F11 adds Memory the same way.
2. **If the new tab's rows need a live background refresh** (an async
   fetch landing while a DIFFERENT tab is on screen — Memory's own
   listing plausibly will), add a new tab-index constant and a new
   `rebuildXTab` helper shaped exactly like `rebuildSettingsProvidersTab`:
   `Popup.RefreshItems` only ever targets `p.cfg.Tabs[p.active]` — there
   is no "refresh tab N regardless of which is showing" method — so a
   background update to an INACTIVE tab has to write
   `p.cfg.Tabs[N].Items` directly (same package, unexported field, safe
   for a tab that is not the one `array.Menu` currently reflects).
   `rebuildSettingsProvidersTab`'s own doc comment explains why this
   guard exists at all — Settings is the first Popup consumer with two
   tabs where either can be open while the OTHER one's data changes in
   the background.
3. **A row whose content is live state, not a fixed fact, belongs in
   `Decor`, not baked into the item at build time.** F9a's render-time
   lesson, reapplied here for `providerRowDecor`/`modelRowDecor`
   /`themeRowDecor` — none of them need a rebuild to stay current,
   because `Decor` is re-evaluated fresh on every render regardless (see
   "The loading-indicator question" below, which is exactly this
   property working as intended).
4. **The construction path (`openSettingsPopup`), the first-run trigger
   (`root.go`'s `WindowSizeMsg` case), and the `settings.open` action are
   already shared.** Neither F11 nor F10b touches any of them — a new
   tab only ever changes what `newSettingsPopup` puts in `Tabs` and what
   the two dispatcher switches handle.

---

## The first-launch hang — three rounds, the actual cause on the fourth

Reported as "Ollama's row sat on 'checking…' for 5+ minutes once, then
resolved instantly on reopening Settings" — reproduced **3× independently
across separate fresh scratch profiles**, every later launch of the SAME
profile instant. That specific pattern (first-contact-only, not
reproducible on a warm profile) is what made each of the first two
theories plausible and each one wrong.

### Round 1 theory: an uncaught exception in `health()` — real, kept, not the cause

`OllamaProvider.health()` (`backend\providers\ollama\provider.py`) only
ever caught `httpx.ConnectError`. A `httpx.TimeoutException` would
propagate uncaught past `_handle_list_providers`'s own
`except ProviderError` (a raw httpx exception is not one) and past
`Runtime.receive`'s `except RuntimeException` too — meaning no
`ProviderList` and no `Error` envelope would reach the frontend AT ALL for
that request. Fixed by adding `except httpx.TimeoutException`, mirroring
what `generate()`/`stream()` in the same file already do. **Real bug,
real fix, kept** — just not sufficient, since it still reproduced
afterward.

### Round 2 theory: resolving "localhost" instead of the literal loopback IP — real, kept, not the cause

`ProviderConfig.from_env` (`backend\providers\models.py`) and
`OllamaProvider.__init__` both defaulted Ollama's base URL to
`http://localhost:11434`. Resolving "localhost" on Windows can try IPv6
(`::1`) first; Ollama listens on IPv4 only, so that attempt has to
exhaust its own timeout before falling back — a well-known source of
exactly this "slow once, instant after" shape, if the OS's own resolver
cache is what later launches benefit from. Fixed by defaulting to the
literal `127.0.0.1` in both places, which needs no resolution at all.
Also added per-provider timing to `_handle_list_providers` (warning above
1 second) so a future reproduction would be diagnosable from `point.log`
directly. **Real improvement, kept** — and still reproduced afterward,
disproving this theory too.

### Round 3: the actual cause, found by tracing `protocol\client.go` rather than guessing a fourth time

```go
func (c *Client) Request(e Envelope, timeout time.Duration) (<-chan Envelope, error) {
	...
	if b == nil {
		return nil, errors.New("backend is not connected")
	}
```

`c.backend` is `nil` until the handshake completes — which, on a
brand-new profile, waits out `bootstrap.build_runtime()`'s full migration
chain first. `root.go`'s first-run trigger fires from the terminal's own
first `tea.WindowSizeMsg`, which arrives from the OS essentially
immediately — almost certainly before that handshake on a cold profile,
rarely on a warm one (migrations are then a no-op). **`settingsFetchProviders`
called `Request`, got this error back, and did nothing with it — no
queue, no retry.** The `ListProviders` request was never sent at all;
how long Ollama itself takes to answer was never the right question,
on either of the first two rounds.

This exact shape had already happened once in this codebase:
`app\picker.go`'s own "Bug B" — an action attempted before the handshake
completes, remembered as `awaitingConnection` so `ConnectedMsg` can retry
it once the connection is actually usable. `settings.go` now has the
identical mechanism: `settingsSend` sets `r.settings.awaitingConnection`
on exactly this failure, and `retrySettingsIfAwaiting` (called from
Root's `ConnectedMsg` case, alongside the picker's own
`retryPickerIfAwaiting`) re-issues the fetch once the connection is live.

**Confirmed fixed 5/5 clean** across independent fresh-profile launches.
`TestSettingsRetriesTheProvidersFetchOnceConnected` pins it — confirmed to
fail without the fix (`ConnectedMsg` wiring temporarily reverted, test
failed with "did not retry the Providers fetch") and pass with it,
before being left in its final, correctly-wired state.

**The lesson, stated plainly since it cost three rounds:** a symptom that
reproduces only on first contact with a fresh resource and resolves
instantly on every later attempt is a *missing retry after a connection
that was not ready yet*, far more often than it is the resource itself
being slow — the same shape this project had already paid for once in
`picker.go`. Check for an unhandled "not connected yet" failure before a
timing theory.

---

## Other bugs found during verification, in the order found

### 1. Theme changes never reached the transcript — two stale render caches

Changing the theme repainted every OTHER surface immediately; the
transcript specifically (user message boxes, "Point:" labels, the
streaming cursor bar) kept the OLD colours until an unrelated keypress
happened to trigger a redraw that picked it up. Two independent render
memos, neither theme-aware:

- `chat\cache.go`'s by-message-id cache bakes `w.theme`'s colours in once,
  invalidated only by a width change.
- `chat\stream_render.go`'s per-live-entry memo (`renderLive`) has three
  freshness conditions — version/width/focus — none of them theme-aware.

Fixed: `SetTheme` (`chat.go`, `app.ThemeAware`'s implementation) now
clears the message cache and bumps a new `themeGen` counter;
`renderLive`'s three freshness checks (`sameFrame`/`fresh`/`throttled`)
each also compare `e.renderedThemeGen == w.themeGen`. Two regression
tests (`chat\theme_live_test.go`) confirmed to fail without the fix
(reverted each mechanism in turn, reconfirmed, restored).

`app.ThemeAware` did not exist before this episode — `Placeholder` had a
vestigial, never-called `SetTheme` method; `chat.Workspace` had none at
all. Added the interface (`workspace.go`) and the real implementation,
wired from `appearance.go`'s `propagateTheme`.

### 2. No way to choose a SPECIFIC model — read as "the status bar won't update"

Reported first as "selecting a specific Ollama model still shows the old
default" and "validating a new provider doesn't update the status bar."
Tracing it: there was no way to choose a *specific* model at all — only
"pick a provider, get whichever model sorts first" (`applyProviderChoice`).
Re-selecting the same provider row always re-resolved to the same model,
indistinguishable from "selecting a model does nothing."

Fixed by adding one row per installed/available model, indented under its
provider (`newProvidersTabItems`), selectable via `selectSpecificModel` —
looked up from the cached list, never trusted from the item id alone, so
a model that has since disappeared from a refreshed list cannot be set as
a default this process never confirmed exists.

### 3. The provider row's note truncated, with no ellipsis visible

`"detected · 3 models · local"` (28 cells) exceeded `popup.go`'s
`rowWidths` note-column cap of 24 cells **at any overlay width** — the
cap is `min(24, avail/3)`, never purely a function of how wide the
popup is, so **widening the overlay alone was never going to fix this**
(confirmed, not assumed, before the sizing request below was applied).

Fixed by shortening every note (`providerRowDecor`) — the model count is
redundant now that each model has its own row — and by writing a test
(`TestProviderRowNeverExceedsTheBindingColumn`) that asserts the ceiling
for every state this function can produce, not just the one state found
by hand. That test immediately caught a SECOND instance of the same risk:
the `"configured · <unhealthy detail>"` branch had no bound at all on
`ProviderHealth.detail`, which is arbitrary backend-supplied text. Fixed
with a dedicated `truncateToRunes` helper, and the "active" marker
changed from a 9-cell `" · active"` suffix to a 2-cell `" ★"` — the
icon's own colour (`AccentPrimary`) already carries most of that signal.

### 4. Sizing

Widened 2 cells each side (`MinW: 70 → 74`) per direct request — real,
but (per #3 above) never the fix for the truncation.

### 5. Model lists never collapsed

Once a provider's models had been fetched, they stayed visible under it
for the rest of the popup's lifetime — switching to a different provider
left the first one's whole model list stacked on screen underneath it
too. Fixed with `settingsState.expandedProvider`, single-valued on
purpose: selecting a different provider's row (or starting a credential
flow for one) overwrites it, which is what collapses whichever was
expanded before without a separate "collapse" action needing to exist.

### 6. The Status card never reflected the chosen provider/model

Root cause: picking a provider/model in Settings only ever changes what
a FUTURE session is created with (`onSessionList`'s `CreateSession`
payload) — the session already open was created with a NULL provider
(before any choice existed) and nothing ever told it otherwise.
`cards.go`'s Status card reads `w.sessionProvider`/`w.sessionModel`
verbatim, so it stayed on `"—"` or a stale value regardless of what was
picked afterward.

Fixed: `applyProviderDefault` (`chat\providers.go`) now also calls the
already-existing, already-backend-wired `SetSessionProvider` for the
currently open session — this is the FIRST Go caller of that event.
`onSessionProviderUpdated` applies the reply, dropping it if it is for a
session this workspace has since moved away from (decoded from the
reply's own echo, never trusted from what was sent). Two regression
tests in `chat\providers_test.go` cover the retrofit firing with an open
session, staying silent with none, and the stale-session-id guard.

### 7. The loading-indicator question — resolved empirically, not by a code fix

Asked directly: does switching to a provider whose models were never
fetched this session jump straight to the full list because the loading
state isn't being set, or because the fetch is genuinely too fast to
perceive? Traced rather than patched further: `providerRowDecor`'s
`"loading models…"` override reads `r.settings.modelsReqID` fresh on
EVERY render, with no dependency on when (or whether) the item list was
last rebuilt — and that field is set synchronously, before the fetch
call returns, inline. The mechanism was already correct by construction.
A minor defensive rebuild was added inside `finalizeProviderSelection`
(self-contained now, rather than relying on a caller to sequence two
calls correctly) — harmless, but confirmed NOT to change what renders,
since `Decor` never needed it.

The remaining variable was real network latency: Ollama's own
`/api/tags` is a localhost round trip, plausibly fast enough that even a
genuine first-ever fetch resolves in under one frame. **Confirmed by
Aryan against a real cloud-provider fetch (a genuine network round
trip)** — the loading indicator IS visible there. Not a bug; Ollama is
just that fast.

### Not a bug — re-run artifacts

Two items from an earlier verification round turned out to be the
reviewer's own script, not the code: `Compare-Object` against a
genuinely empty `config.toml` returns a null-reference error that reads
like a diff and is not one (confirmed by direct file-size check, 0 bytes
before and after — `config.toml` was never written to); a second
"Step 4" finding needed only a clean re-run with a fresh scratch
variable. Recorded here so neither is mistaken for a regression later.

---

## A regression this episode caused in its own tests

Wiring `providerMissing()` directly off `w.state.Provider == nil` broke
roughly 40 pre-existing `chat` tests — every bare `Workspace{}` a test
builds has a nil `Provider` by simple zero-value default, which is
indistinguishable from "a real profile that genuinely has nothing
configured" if checked locally. Fixed by routing it through
`app.Diagnostics.NoProviderConfigured` instead — the SAME push-based
pattern `backendDown`/`connState` already use — so the lock can only ever
turn on because ROOT decided it should (from `opts.State.Provider` at
startup), never as an accidental side effect of an unrelated struct
defaulting to its zero value. The same shape broke several `app` package
tests once the first-run chooser started auto-opening on any bare
`Options{}`; fixed with a shared `testProviderDefault` fixture
(`app\app_test.go`, `chat\main_test.go`) that every pre-existing test now
opts into except the two tests that are specifically ABOUT the first-run
trigger.

---

## What F10b and F10c inherit

Unchanged from the original split (`design/tasks/frontend/INDEX.md`):

- **F10b** (Behaviour overrides + `doctor` in About) needs a runtime
  override/reporting layer that does not exist yet — `config.toml`
  values the backend reads live (`chat.queue_depth` and friends) have no
  in-memory override path and no wire event to push one, and a `doctor`
  wire event to call from the About tab. Add a `"behaviour:"`/`"about:"`
  prefix each to `settingsDecor`/`onSettingsSelect`, extend `Tabs`, done
  — see "The Settings popup's tab structure" above for exactly what that
  touches and does not touch.
- **F10c** (Demo mode) needs a new provider type (distinct from the
  test-only `MockProvider`), a fixture library, a shipped sample project,
  and — per F09a's own finding, unchanged — an apply target (no Editor
  workspace exists yet; F13/F9b's job).
- **F11** (Memory popup) is the next real exercise of `PopupConfig.Tabs`
  beyond two, and of `Decor`/`OnSelect` dispatching by prefix rather than
  by tab.
- **Whichever session actually touches Ollama's `health()`/base-URL
  logic again** should know two things are now true there that were not
  before this episode: it catches `httpx.TimeoutException`, not just
  `httpx.ConnectError`; and it defaults to `127.0.0.1`, not `"localhost"`.
  Neither was the fix for the hang this episode chased, but both are real
  and should not be reverted.
- **`app.ThemeAware`** is now a real, implemented narrow interface
  (`chat.Workspace.SetTheme`). Any future workspace that holds its own
  copy of anything Root can change live (not just the theme) should read
  `SetTheme`'s own doc before inventing a second way to propagate it.
