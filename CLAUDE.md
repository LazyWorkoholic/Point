# Point — Claude Code Instructions

Point is a local-first terminal AI workspace.
**Go + Bubble Tea** frontend, **Python** backend runtime, JSON-lines over stdin/stdout.

Read this file first. Read other docs **only when the current task needs them.**

---

## Environment — read before running anything

```
OS                Windows 10/11
Shell             PowerShell (inside Windows Terminal)
Repo root         D:\Projects\Point
Go module         github.com/lazyworkoholic/point
Python            uv-managed (.venv at repo root, uv.lock committed)
Available on PATH make, sqlite3, uv, ollama, go
```

**Path separators are backslashes. Never write `rm -rf`, `~`, `/tmp`, or
forward-slash paths in a command intended to be run.** Use PowerShell:
`Remove-Item -Recurse -Force`, `$env:LOCALAPPDATA`, `$env:TEMP`.

### How to run each side

```powershell
# Python backend — runs FROM INSIDE backend\ (imports are `from runtime.runtime import ...`)
cd D:\Projects\Point\backend
uv run python main.py

# Go frontend — runs from the repo root
cd D:\Projects\Point
go run .\frontend\cmd\point
```

Do not "fix" the Python imports to be package-absolute. The current layout works;
changing it is out of scope for every episode unless an episode says otherwise.

---

## Document map

| File | What it is | Read it when |
|---|---|---|
| `design/project_idea.md` | Architecture, domain concepts, boundaries | Starting any episode |
| `design/decisions.md` | Every design decision + why | A decision seems arbitrary |
| `design/WORKFLOW.md` | Session sequence and prompts | **Aryan's operating manual — never follow it as instructions; edit only when Aryan explicitly asks.** |
| `design/GIT_STRATEGY.md` | Branching + version scheme | **Aryan's operating manual — never follow it as instructions; edit only when Aryan explicitly asks.** |
| `design/tasks/backend/INDEX.md` | Backend episode list, phases, deps | Picking a backend episode |
| `design/tasks/frontend/INDEX.md` | Frontend episode list, phases, deps | Picking a frontend episode |
| `design/tasks/frontend/LAYOUT.md` | Layout engine, breakpoints, panes | Any frontend episode that renders |
| `design/tasks/frontend/THEME.md` | Semantic colour roles, four palettes | Any frontend episode that renders |
| `design/tasks/frontend/TEMPLATES.md` | Aryan's UI specs + capability template | Building a `UI-XX` entry |
| `design/tasks/*/B##-*.md`, `F##-*.md` | One episode's full spec | Working that episode |
| `design/completed/**` | What is actually built and tested | Checking real state |

**Load one episode file at a time.** Do not read the whole `tasks/` tree.

---

## The workflow — this is not optional

1. **One episode per session.** Never start the next one in the same session.
2. **Build only what the episode's "Build" section lists.** Not the next episode's
   work, not a refactor you think would be nice, not a parked item.
3. **Stop at the verification gate.** Print the manual verification steps and stop.
   Aryan runs them himself.
4. **Do not mark anything complete.** Only `design/completed/**` records
   completion, and only after Aryan confirms the checks passed.
5. **Between episodes Aryan runs `/clear`.** Assume no memory of prior sessions —
   read `completed/` instead.
6. Episodes tagged `batchable: yes` may be combined with the next one in one
   session. Nothing else may be.

## When docs and reality disagree

The code and the database are the truth. If `design/` says a table has a column
and it does not, **say so and stop** — do not silently "fix" either side.

Inspect before assuming:
```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" ".schema"
```

## Hard rules

- **Never write files from an agent.** Agents return a rewritten region; Go
  computes the diff; the user applies it. This holds in every phase.
- **Never put secrets in `config.toml`.** Keys come from env vars, then
  `credentials.toml` (gitignored). Keys are masked in logs, errors, and UI.
- **Never log to stdout in Python.** Stdout is the protocol channel; logs go to
  stderr.
- **Never claim a performance benefit that has not been measured.** Say "for
  control" or "for correctness" instead.
- **Parking-lot items are not work.** Do not implement one because it seems
  adjacent. If an entry says "ask Aryan first", ask — do not begin.
- **No new dependencies** without saying why in the episode's handoff notes.
- **Write PowerShell in every command you print.** A Unix command Aryan cannot run
  is a failed instruction.
- **Manual verification must never let Point's process see the real
  `%APPDATA%\Point` or `%LOCALAPPDATA%\Point` path.** Point never needs to be
  told where "real" is — it only ever reads `APPDATA`/`LOCALAPPDATA` from its
  own environment (`backend\appconfig.py` `config_dir()`/`state_dir()`,
  `frontend\internal\config\config.go` `ConfigDir()`/`StateDir()` — the same
  two env vars on both sides, by design, so one override moves both). Set
  both to a scratch directory for the one script's duration and nothing under
  the real `Point` folder is ever a target of anything, including a failure:

  ```powershell
  $ErrorActionPreference = "Stop"
  $scratch = Join-Path $env:TEMP "point-scratch-<episode>"
  New-Item -ItemType Directory -Force $scratch | Out-Null
  try {
      $env:APPDATA      = $scratch   # config.toml, credentials.toml
      $env:LOCALAPPDATA = $scratch   # runtime.db, state.json, logs\
      cd D:\Projects\Point\backend
      uv run python cli.py migrate
      # ... the rest of the episode's checks ...
  }
  finally {
      Remove-Item -LiteralPath $scratch -Recurse -Force
  }
  ```

  This **replaces the old rename-aside-and-restore pattern** (rename the real
  folder out of the way, run the check, rename it back), which is retired —
  do not write a new verification script that renames or deletes anything
  under `%APPDATA%\Point` or `%LOCALAPPDATA%\Point` by name, for any reason,
  even guarded by `Test-Path`. Rename-aside had two independent failure modes
  that both actually happened: (B7) a separate `cat >` in a later tool call
  clobbered the real `config.toml`, because shell state — including an env
  var like `$env:POINT_DB` — does not survive between tool calls, so every
  step must already be one script; and (F10) `Rename-Item ... -ErrorAction
  SilentlyContinue` silently failed to move the real profile out of the way
  (a lock, a running process, anything), and the script's own "restore"
  step then ran `Remove-Item "$env:APPDATA\Point" -Recurse -Force` unconditionally,
  permanently deleting the live profile because nothing had verified the
  rename actually happened first. A pattern that depends on a rename
  succeeding, with nothing checking that it did, before a step that deletes
  by the real name, is not made safe by adding a `Test-Path` guard — it is
  made safe by never naming the real path in a destructive call at all. If a
  check genuinely cannot run against an env-var override (none identified so
  far), stop and say so rather than reintroducing rename-aside.
  `db_path`/`config_path` overrides like `$env:POINT_DB` alone are not
  sufficient by themselves either: `POINT_DB` moves the database but
  `config.toml`, `credentials.toml`, `state.json`, and `logs\` still resolve
  under the real `APPDATA`/`LOCALAPPDATA` unless those are overridden too —
  set both env vars, not just `POINT_DB`. **If any doc — completed, task, or
  otherwise — still shows the old rename-aside script, it is superseded
  regardless of where it appears or how old or new it is; do not copy it,
  even if it is the first example a search turns up.** This rule is what
  governs, not precedent found by grepping a doc mid-task instead of reading
  this file first.
- **If this episode's verification needs to exercise a command or feature
  owned by a LATER episode, do not build a stub or partial version of it.**
  Write the required state directly (SQL, a one-off script) inside that
  verification step, scoped to the single check — never as new product
  surface the later episode then has to reconcile with or discard. (B8:
  needed a `context_attachments` row to test change-detection; `attach` is
  B12's command. Inserted the row directly via `sqlite3` in the verification
  script instead of building a stub `attach`.)
- **If this episode needs a config or data shape that a LATER episode will
  formally own, do not reuse that later episode's name for it.** Define a
  narrower local type under a different name, and note in this episode's
  Handoff that the later episode's real type must be adaptable to it, not
  identical to it. (B8 needed a lightweight per-request config before B9's
  real `AgentConfig` existed — named it `ContextPolicy` instead, so the two
  never collide.)

## Phase tags

- `[P1]` — v1 scope. Build these.
- `[P2]` — stretch. Only after all `[P1]` items are done and verified.
- `[PARK]` — deferred by design. Do not build. The hook exists; the feature does not.

## Actual repo layout

Verified against the tree on 2026-10-19. The 2026-10-02 version of this
section described the frontend as 8 internal packages and "~110 files",
which had been true through F8 and missed two: `internal\editor\`, the whole
Editor workspace F13a/F13b built, and `internal\diff\`, which F9b added. The
version before that described the frontend as `cmd\point\` plus
`internal\array\` — true through F1 and eight episodes out of date — and
claimed `managers\memory.py` was empty after B11 had filled it. **That is
three times this one section has drifted**, which is why the rule below is
stated rather than assumed: if this section and the tree disagree again,
**the tree is right**; fix this file.

```
D:\Projects\Point\
├── CLAUDE.md  VERSION  README.md  makefile  go.mod  go.sum
├── pyproject.toml  uv.lock  .python-version  .gitignore  .venv\
├── .point\                     this repo is itself a Point project (gitignored)
│
├── backend\                    Python — run from inside this folder
│   ├── main.py                 transport only: stdin reader thread,
│   │                           outbox writer, teardown              [B6 ✅]
│   │                           + the third exit door (a `Shutdown`
│   │                             COMMAND), signal handlers for
│   │                             SIGINT/SIGBREAK/SIGTERM with
│   │                             "second signal exits now", and
│   │                             `exit_now()` — which skips
│   │                             interpreter finalization because the
│   │                             DAEMON stdin thread holds the
│   │                             `sys.stdin.buffer` lock on every exit
│   │                             path except EOF, and finalization
│   │                             aborts the process trying to take it [B15 ✅]
│   ├── bootstrap.py            resolve_db_path, run_migrations,
│   │                           build_runtime, shutdown_runtime      [B6 ✅]
│   ├── cli.py                  doctor · migrate · session · send ·
│   │                           tasks · project · attach · detach ·
│   │                           set-mode · attachments · context ·
│   │                           agents · workspace · bench           [B6, grown since]
│   ├── appconfig.py            paths, config.toml, key resolution   [B6 ✅]
│   ├── logging_setup.py        rotating file + stderr, redaction    [B6 ✅]
│   ├── lockfile.py             single-instance lock                 [B6 ✅]
│   │
│   │                           (no database here — B6 moved it to
│   │                           %LOCALAPPDATA%\Point\runtime.db. Override with
│   │                           POINT_DB, then config `database.path`. The
│   │                           pre-B6 backend\runtime.db this used to be
│   │                           copied from is GONE — B29 removed it, and the
│   │                           silent copy-into-any-fresh-profile mechanism
│   │                           with it, once it was confirmed every scratch-
│   │                           profile script in this repo had been getting
│   │                           a non-empty "fresh" database with no signal
│   │                           that it happened. Nothing resolves a database
│   │                           path from anywhere but these two env vars now.)
│   │
│   ├── runtime\                runtime, dispatcher, task, task_manager,
│   │                           state, errors                        [B1 ✅]
│   │                           events.py is envelope v1             [B6 ✅]
│   │                             + `event_seq`, the durable per-session
│   │                               event cursor, as a NEW field beside
│   │                               `seq` rather than redefining it
│   │                               (D100). `EventsSince`/`SessionEvents`
│   │                               replace B15's SessionReplaySince,
│   │                               which is DELETED                     [B40 ✅]
│   │                           scheduler.py — QUEUED + lane caps    [B12 ✅]
│   │                           lifecycle.py — ReleaseReport, the
│   │                             per-resource-class refusal rule,
│   │                             WorkspaceContext/ProjectContext.
│   │                             Holds no DB handle and knows nothing
│   │                             about workspaces as a UI concept    [B15 ✅]
│   ├── managers\               session, session_manager             [B2 ✅]
│   │                           message, message_history,
│   │                           message_translator                   [B3 ✅]
│   │                           project, pathguard, gitignore,
│   │                           project_manager                      [B7 ✅]
│   │                           context, attachment                  [B8 ✅]
│   │                           agent.py (AgentConfig only)          [B9 ✅]
│   │                           editor_context.py — EditorContext +
│   │                             resolve_scope (no parser)          [B11 ✅]
│   │                           memory.py — `Proposal` ONLY, B11's
│   │                             narrow stopgap over `agent_proposals`.
│   │                             NOT B13's memory system; B13 owns
│   │                             memory_records/memory_scopes, which
│   │                             exist in the schema and are unused. [B11 ✅]
│   │                           invocation.py — parse_invocation,
│   │                             one ambiguity rule for every caller [B12 ✅]
│   │                           attachment_manager.py — the guards,
│   │                             the estimate, the preview offers    [B12 ✅]
│   │                           tool.py is DELETED, not filled — see below
│   ├── agents\                 builtins, registry, executor         [B9 ✅]
│   ├── tools\                  models, schema, registry, executor,
│   │                           faults                               [B10 ✅]
│   │   └── builtins\           filesystem.py (read_file, list_files),
│   │                           web.py (web_search),
│   │                           task_results.py (load_task_result)
│   │                           — grouped by subject, NOT one file
│   │                             per tool                            [B10 ✅]
│   ├── database\               connection, session_repository       [B2 ✅]
│   │                           migrations.py, task_repository.py    [B6 ✅]
│   │                           project_repository                   [B7 ✅]
│   │                           attachment_repository                [B8 ✅]
│   │                           agent_repository                     [B9 ✅]
│   │                           tool_call_repository                 [B10 ✅]
│   │                           proposal_repository — agent_proposals,
│   │                             insert/count/fetch only            [B11 ✅]
│   │                           event_repository.py — migration v8's
│   │                             `events`, the durable event log every
│   │                             session-scoped envelope leaving
│   │                             `Runtime._emit` is written to, plus
│   │                             `events_since` and retention. Holds
│   │                             the policy for what is NOT logged:
│   │                             the token stream (D99, measured, not
│   │                             assumed), session-less envelopes
│   │                             (D101) and the replay reply itself —
│   │                             an envelope that CARRIES log contents
│   │                             is not log content                    [B40 ✅]
│   │                           message_checkpoint_repository.py —
│   │                             migration v7's `message_checkpoints`,
│   │                             the in-flight reply on disk (500ms
│   │                             cadence) plus the startup promotion
│   │                             that turns a survivor into an
│   │                             `interrupted` message row. A SEPARATE
│   │                             table, not a flag on `messages`:
│   │                             a live partial inside `messages`
│   │                             would enter the next prompt         [B15 ✅]
│   ├── providers\              base, bootstrap, errors, models, registry,
│   │                           __init__.py                          [B6 ✅]
│   │                           retry.py — backoff + jitter +
│   │                             Retry-After                        [B10 ✅]
│   │                           + anthropic\ gemini\ ollama\ openai\ openrouter\  [B4 ✅]
│   │                           + mock\ (POINT_MOCK=1 only, never in the UI) [B6 ✅]
│   ├── streaming\              accumulator, adapter, events, manager [B5 ✅]
│   └── tests\                  manual_*.py and manual_*.ps1 — one or more
│                               per episode that needed a deterministic
│                               check (B11, B12, B15, B19, B22, B40, F8 …), plus
│                               manual_provider_test.py [B4] and
│                               manual_context_test.py [B8]
│                               fixtures\ (mock_stream.json, mock_slow.json) [B6 ✅]
│                               + mock_dense.json — 2000 deltas at ZERO
│                                 delay. mock_slow is 6s of asyncio.sleep
│                                 and cannot measure anything on the emit
│                                 path; `bench event-log` defaults here   [B40 ✅]
│                               + the seven B10 tool-loop scripts,
│                                 replayed by `send --script <name>`  [B10 ✅]
│
├── frontend\                   Go — ~187 files across 10 internal packages
│   ├── cmd\point\              main.go, diagnostics.go              [F1 ✅]
│   └── internal\
│       ├── app\                the root model: root.go, render.go, focus.go,
│       │                       overlay.go, popup.go, confirm.go, picker.go,
│       │                       draft.go, stream.go, tasks.go, actions.go,
│       │                       box.go, context_expand.go, placeholder.go,
│       │                       paste.go, workspace.go                [F1→F8]
│       ├── chat\               the Chat workspace — the largest package:
│       │                       model, render, entries, cards, input*,
│       │                       stream*, markdown, scroll, reload*,
│       │                       attachments, context_overlay,
│       │                       context_report, picker, providers,
│       │                       protocol, connection, copy, cache     [F3→F8]
│       ├── editor\             the Editor workspace — buffer, doc, edit,
│       │                       motion, undo, render, keys, mouse, tabs,
│       │                       tree, highlight, status, box, textutil,
│       │                       protocol                              [F13a ✅]
│       │                       save, lock, reload, stale, autosave,
│       │                       diff (B10's coarse buffer-vs-disk
│       │                         unifiedDiff, NOT F9b's)             [F13b ✅]
│       │                       diffcard.go — `DiffCard` (UI-07) and
│       │                         Workspace.onDiffApply. Nothing
│       │                         constructs one yet: F15 is the first
│       │                         caller, by design                   [F9b 🟡]
│       ├── diff\               Region() — the real LCS line diff, computed
│       │                       from B24's validated proposal. Depends on
│       │                       NOTHING (not editor, app, chat, tea or
│       │                       theme) so F9b's packaging decision stays
│       │                       open and F15 can call it             [F9b ✅]
│       ├── protocol\           client, transport, envelope, spawn*, types,
│       │                       id — the backend client                [F2 ✅]
│       │                       B15 filled F2's replay seam: a
│       │                         per-session `lastSeq` high-water mark
│       │                         (fed by `MarkSeen`, since `messages.seq`
│       │                         lives in a payload and this package
│       │                         reads only types and routing fields),
│       │                         `RouteReplay` so a replayed message can
│       │                         never be taken for a new one, and
│       │                         `busyGrace` DELETED — the backend's ping
│       │                         is unconditional now (D98)             [B15 ✅]
│       ├── layout\             Compute, breakpoints, rects, CardSlot  [F1 ✅]
│       ├── theme\              theme.go + palettes.go (four palettes) [F1 ✅]
│       ├── actions\            the action registry                   [F1 ✅]
│       ├── config\             config.go (ConfigDir/StateDir), state.go [F1 ✅]
│       └── array\              item.go, menu.go, fuzzy.go — the shared
│                               scrollable/filterable list, folded into
│                               Popup by F7                           [F7 ✅]
│
└── design\                     these docs
```

`schemas\` **does not exist**; nothing has needed it yet. `memory\` does not
exist either — B13 creates what it needs, and B11's stopgap lives in
`managers\memory.py` and one table, deliberately not a package.

`managers\provider.py` **is deleted** — superseded by `providers\` (B4). Keeping
a second provider abstraction alive was the whole reason to remove it; do not
recreate it.

`managers\tool.py` **is deleted too** — B10 did not fill it. Same call B9 made
for `managers\agent.py`'s business rules: a registry, an executor, a schema
checker, and four tools are not one manager, and `agents\` had already
established the split. `managers\agent.py` kept only the `AgentConfig` data
model; there is no `managers\tool.py` counterpart because `Tool` and
`ToolResult` are data the tool package itself owns. Do not recreate it.

`providers\__init__.py` was missing while every other package had one; **B6
added it.** The tree worked as a namespace package regardless, so this was
cleanup, not a bug fix.

There is **no file-write TOOL anywhere in the backend**, and there must not
be one. That is the first hard rule above, enforced structurally rather than
by a check inside something: an agent-proposed change reaches disk only
through a buffer the user saves.

This paragraph used to say there was no file-write *event* either — "no
`WriteProjectFile`, no counterpart to `ReadProjectFile`" — and **B32 built
exactly that** (`WRITE_PROJECT_FILE` in `backend\runtime\events.py`,
`_handle_write_project_file`, `ProjectManager.write_file`), as the editor's
save path. Noticed during B15 and corrected here, which is the fourth time
this section has drifted and the reason its own note says the tree wins. The
distinction that actually matters, and which `events.py`'s own comment
states: a runtime event handler the USER's save goes through is not a tool an
AGENT can call, and the rule is about the second.
