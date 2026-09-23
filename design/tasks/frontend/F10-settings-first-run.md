# F10 — Settings, Providers, First Run

**Phase:** P1 · **Batchable:** no · **Depends on:** F7 · **Backend:** B6

## Purpose

First run **is** the demo — the first thirty seconds anyone sees. It is also
where the database is created, migrations run, and credentials are written, so a
failure here looks like a broken product rather than a missing config.

## Build

### Provider chooser (UI-10) — first run
Detect silently, **select explicitly**. A local model commits the user's CPU,
GPU, memory and battery; detection is not consent.

```
╭─ Choose a provider ────────────────────────────────╮
│ ● Ollama            detected · 3 models · local    │
│     qwen3:8b        4.7 GB · runs on this machine  │
│     llama3.2:3b     2.0 GB                         │
│ ○ OpenAI            needs an API key               │
│ ○ Anthropic         needs an API key               │
│ ○ Gemini · OpenRouter   (free models available)    │
│ ○ Demo mode         pre-recorded, no network       │
│                                                    │
│ Local models use your CPU/GPU and memory.          │
│ Change anytime with the settings action.           │
╰────────────────────────────────────────────────────╯
```

This is the **Providers section of Settings**, not a separate wizard screen —
same component, three entry points (first run, settings, palette). Nothing to
keep in sync.

### Credential entry
- Masked input; never echoed; only the last 4 shown after saving.
- **Bracketed paste accepted** — keys are 100+ characters and nobody types them.
- **Trim whitespace and newlines on paste.** A trailing `\r\n` from a Windows
  copy produces a baffling 401 that looks like a wrong key.
- On enter, immediately call `list_models()` and show the result:
  `✓ valid — 14 models available`, or the provider's actual error. A mistyped key
  must fail now, not inside a task ten minutes later.
- `esc` backs out without setting a key — allowed.
- The **backend** writes `%APPDATA%\Point\credentials.toml`
  (temp-file-then-rename). The UI never touches the file.

### No-provider state
The app still runs. Input disabled, **with the reason and the fix inline**:
```
╭──────────────────────────────────────────────╮
│ ⚠ no provider configured                     │
│   ^W s  set up a provider   ·   ^P  palette  │
╰──────────────────────────────────────────────╯
```
Everything else stays explorable: palette, help, settings, editor, file tree.
**A disabled input with no explanation is the worst state a TUI can be in.**

### Settings popup (UI-03)
The F7 `Popup` with tabs: `Providers · Appearance · Behaviour · Keys · About`.

- **Providers** — add/change/remove keys, per-provider default model, effort
  (**greyed out when `supports_effort` is false** — never offer a control that
  does nothing).
- **Appearance** — theme with live preview, background mode, mouse toggle.
- **Behaviour** — queue depth, idle-unload timeout, concurrency caps, scrollback,
  `allow_outside_project`, `index.respect_gitignore`, `stale_after_days`.
- **Keys** — rebind, straight from the action registry.
- **About** — versions, paths, and a **`point doctor`** action rendering the
  backend's report in a scrollable card.

**Settings never writes `config.toml`.** Values changed in the UI are written to
`state.json` as overrides; the About tab shows which values are overridden and
how to make them permanent. Point must never destroy the user's comments.

### Demo mode
Selectable as a provider. While active a persistent `DEMO` badge sits in the
header — **it must announce itself**. Responses come from recorded fixtures, no
network, no Ollama. This is the mode for a presentation with dead wifi and no
local model.

Distinct from `MockProvider`, which is test-only (`POINT_MOCK=1`) and never
appears in this list.

**The fixture set must cover every feature the demo shows**, or demo mode
silently makes half the app look broken:

```
chat reply           a multi-paragraph markdown answer with a fenced code block
                     — long enough to stream visibly for several seconds
queued turn          a second, shorter reply, so the queue can be demonstrated
cancellation         a reply that can be interrupted mid-stream and leaves
                     a sensible partial
@research            a multi-step run: step 1/4 … 4/4 with tool.started /
                     tool.completed pairs, ending in a result with a summary,
                     a token count, and 2 pending memory proposals
@filler-explainer    an EXPLANATION for a selected function, and a FILL region
   (both modes)      that streams as ghost text and yields an applicable diff
                     whose line numbers land on a fixture file shipped with it
usage events         plausible token counts, so the context card and status
                     panel show real-looking numbers rather than zeros
error case           one fixture that fails, so the error path is demonstrable
```

Two rules:
- **Fixtures ship with a small sample project** (a few source files plus a plain
  HTML/CSS folder), so the editor, the Filler's line numbers, and the web
  preview all have something real to act on with no network.
- **Timing is replayed, not instant.** Tokens arrive at a plausible rate;
  a research run takes tens of seconds. An instantly-complete response looks
  fake and demonstrates none of the streaming, queueing, or cancellation work.

Everything that does **not** need a model — the editor, autosave, web preview,
workspace unload, the palette, memory review, settings — works normally in demo
mode, because none of it touches a provider.

## Do NOT

- Write `config.toml`.
- Show a raw key anywhere, including error messages and logs.
- Build a multi-step wizard.
- Expose `MockProvider` in the UI.

## Done when

1. On a profile with no config, launching reaches a usable state in under a
   minute with no traceback.
2. With Ollama running, it is **detected but not selected** — the user must pick.
3. Escaping the key prompt leaves the app explorable with the disabled-input
   explanation visible.
4. A wrong key is rejected at entry with the provider's real error.
5. A pasted key with a trailing newline works.
6. `config.toml` is byte-identical before and after changing settings in the UI.
7. Theme switching previews live and persists across restart.
8. Effort is greyed out on Ollama and enabled on a provider that supports it.
9. Demo mode shows the badge for its entire duration.

## Manual verification (PowerShell)

**Per CLAUDE.md: never rename or delete the real `%APPDATA%\Point` /
`%LOCALAPPDATA%\Point` by name.** Point and the Go frontend both read their
config/state directories from the `APPDATA`/`LOCALAPPDATA` env vars alone
(`backend\appconfig.py`, `frontend\internal\config\config.go`) — pointing
those at a scratch directory for one PowerShell session moves both sides of
the app at once, with nothing to rename back and nothing that can delete the
live profile even if a step fails. This whole gate is one PowerShell session
(env vars set once, `go run` launched interactively inside it, checks run
against the same scratch path throughout, cleanup at the end) — do not split
it across separate terminals or tool calls, or the override is gone before
the next step runs.

```powershell
# One session for the whole gate.
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f10"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
$env:APPDATA      = $scratch
$env:LOCALAPPDATA = $scratch

# 1-2. fresh machine simulation -- $scratch has no config.toml or state yet
cd D:\Projects\Point
go run .\frontend\cmd\point
```
**Expect:** the provider chooser, with Ollama listed as detected but **not**
pre-selected. No crash, no traceback.

Press `esc` at the key prompt. **Expect:** the disabled-input explanation, and
the palette/help/editor all still reachable.

```powershell
# 6. config.toml must be untouched (still inside the same session/$scratch)
Copy-Item "$scratch\Point\config.toml" "$env:TEMP\before.toml"
# change theme, queue depth, and mouse in the UI, then quit
Compare-Object (Get-Content "$env:TEMP\before.toml") (Get-Content "$scratch\Point\config.toml")
```
**Expect:** no output — the files are identical.
**Fails if:** anything differs. Point rewrote the user's file.

```powershell
# 4-5. key validation
```
In Settings → Providers, enter a deliberately wrong OpenAI key.
**Expect:** the provider's real error, immediately.
Then paste a valid key copied with a trailing newline.
**Expect:** `✓ valid — N models available`.

```powershell
# cleanup -- $scratch only; the real profile was never touched
Remove-Item -LiteralPath $scratch -Recurse -Force
$env:APPDATA      = $realAppData
$env:LOCALAPPDATA = $realLocalAppData
```

## Handoff

State after this episode: Point boots from nothing, asks for a key only when a
cloud provider is chosen, validates it immediately, and never rewrites the user's
config. `point doctor` is reachable from inside the app.

Record: how long the fresh-machine path actually took, and anything that made it
feel slow — this is the demo's opening.

Do not proceed until every check above passes.
