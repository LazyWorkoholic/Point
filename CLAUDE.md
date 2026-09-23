# Point â€” Claude Code Instructions

Point is a local-first terminal AI workspace.
**Go + Bubble Tea** frontend, **Python** backend runtime, JSON-lines over stdin/stdout.

Read this file first. Read other docs **only when the current task needs them.**

---

## Environment â€” read before running anything

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
# Python backend â€” runs FROM INSIDE backend\ (imports are `from runtime.runtime import ...`)
cd D:\Projects\Point\backend
uv run python main.py

# Go frontend â€” runs from the repo root
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
| `design/WORKFLOW.md` | Session sequence and prompts | **Aryan's operating manual â€” never follow it as instructions; edit only when Aryan explicitly asks.** |
| `design/GIT_STRATEGY.md` | Branching + version scheme | **Aryan's operating manual â€” never follow it as instructions; edit only when Aryan explicitly asks.** |
| `design/tasks/backend/INDEX.md` | Backend episode list, phases, deps | Picking a backend episode |
| `design/tasks/frontend/INDEX.md` | Frontend episode list, phases, deps | Picking a frontend episode |
| `design/tasks/frontend/LAYOUT.md` | Layout engine, breakpoints, panes | Any frontend episode that renders |
| `design/tasks/frontend/THEME.md` | Semantic colour roles, four palettes | Any frontend episode that renders |
| `design/tasks/frontend/TEMPLATES.md` | Aryan's UI specs + capability template | Building a `UI-XX` entry |
| `design/tasks/*/B##-*.md`, `F##-*.md` | One episode's full spec | Working that episode |
| `design/completed/**` | What is actually built and tested | Checking real state |

**Load one episode file at a time.** Do not read the whole `tasks/` tree.

---

## The workflow â€” this is not optional

1. **One episode per session.** Never start the next one in the same session.
2. **Build only what the episode's "Build" section lists.** Not the next episode's
   work, not a refactor you think would be nice, not a parked item.
3. **Stop at the verification gate.** Print the manual verification steps and stop.
   Aryan runs them himself.
4. **Do not mark anything complete.** Only `design/completed/**` records
   completion, and only after Aryan confirms the checks passed.
5. **Between episodes Aryan runs `/clear`.** Assume no memory of prior sessions â€”
   read `completed/` instead.
6. Episodes tagged `batchable: yes` may be combined with the next one in one
   session. Nothing else may be.

## When docs and reality disagree

The code and the database are the truth. If `design/` says a table has a column
and it does not, **say so and stop** â€” do not silently "fix" either side.

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
  adjacent. If an entry says "ask Aryan first", ask â€” do not begin.
- **No new dependencies** without saying why in the episode's handoff notes.
- **Write PowerShell in every command you print.** A Unix command Aryan cannot run
  is a failed instruction.
- **Manual verification must never let Point's process see the real
  `%APPDATA%\Point` or `%LOCALAPPDATA%\Point` path.** Point never needs to be
  told where "real" is â€” it only ever reads `APPDATA`/`LOCALAPPDATA` from its
  own environment (`backend\appconfig.py` `config_dir()`/`state_dir()`,
  `frontend\internal\config\config.go` `ConfigDir()`/`StateDir()` â€” the same
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
  folder out of the way, run the check, rename it back), which is retired â€”
  do not write a new verification script that renames or deletes anything
  under `%APPDATA%\Point` or `%LOCALAPPDATA%\Point` by name, for any reason,
  even guarded by `Test-Path`. Rename-aside had two independent failure modes
  that both actually happened: (B7) a separate `cat >` in a later tool call
  clobbered the real `config.toml`, because shell state â€” including an env
  var like `$env:POINT_DB` â€” does not survive between tool calls, so every
  step must already be one script; and (F10) `Rename-Item ... -ErrorAction
  SilentlyContinue` silently failed to move the real profile out of the way
  (a lock, a running process, anything), and the script's own "restore"
  step then ran `Remove-Item "$env:APPDATA\Point" -Recurse -Force` unconditionally,
  permanently deleting the live profile because nothing had verified the
  rename actually happened first. A pattern that depends on a rename
  succeeding, with nothing checking that it did, before a step that deletes
  by the real name, is not made safe by adding a `Test-Path` guard â€” it is
  made safe by never naming the real path in a destructive call at all. If a
  check genuinely cannot run against an env-var override (none identified so
  far), stop and say so rather than reintroducing rename-aside.
  `db_path`/`config_path` overrides like `$env:POINT_DB` alone are not
  sufficient by themselves either: `POINT_DB` moves the database but
  `config.toml`, `credentials.toml`, `state.json`, and `logs\` still resolve
  under the real `APPDATA`/`LOCALAPPDATA` unless those are overridden too â€”
  set both env vars, not just `POINT_DB`. **If any doc â€” completed, task, or
  otherwise â€” still shows the old rename-aside script, it is superseded
  regardless of where it appears or how old or new it is; do not copy it,
  even if it is the first example a search turns up.** This rule is what
  governs, not precedent found by grepping a doc mid-task instead of reading
  this file first.
- **If this episode's verification needs to exercise a command or feature
  owned by a LATER episode, do not build a stub or partial version of it.**
  Write the required state directly (SQL, a one-off script) inside that
  verification step, scoped to the single check â€” never as new product
  surface the later episode then has to reconcile with or discard. (B8:
  needed a `context_attachments` row to test change-detection; `attach` is
  B12's command. Inserted the row directly via `sqlite3` in the verification
  script instead of building a stub `attach`.)
- **If this episode needs a config or data shape that a LATER episode will
  formally own, do not reuse that later episode's name for it.** Define a
  narrower local type under a different name, and note in this episode's
  Handoff that the later episode's real type must be adaptable to it, not
  identical to it. (B8 needed a lightweight per-request config before B9's
  real `AgentConfig` existed â€” named it `ContextPolicy` instead, so the two
  never collide.)

## Phase tags

- `[P1]` â€” v1 scope. Build these.
- `[P2]` â€” stretch. Only after all `[P1]` items are done and verified.
- `[PARK]` â€” deferred by design. Do not build. The hook exists; the feature does not.

## Actual repo layout

```
D:\Projects\Point\
â”œâ”€â”€ CLAUDE.md  VERSION  README.md  makefile  go.mod  go.sum
â”œâ”€â”€ pyproject.toml  uv.lock  .python-version  .gitignore  .venv\
â”‚
â”œâ”€â”€ backend\                    Python â€” run from inside this folder
â”‚   â”œâ”€â”€ main.py                 transport only: stdin reader thread,
â”‚   â”‚                           outbox writer, teardown              [B6 âœ…]
â”‚   â”œâ”€â”€ bootstrap.py            resolve_db_path, run_migrations,
â”‚   â”‚                           build_runtime, shutdown_runtime      [B6 âœ…]
â”‚   â”œâ”€â”€ cli.py                  doctor Â· migrate Â· session new|list Â·
â”‚   â”‚                           send Â· tasks list                    [B6 âœ…]
â”‚   â”œâ”€â”€ appconfig.py            paths, config.toml, key resolution   [B6 âœ…]
â”‚   â”œâ”€â”€ logging_setup.py        rotating file + stderr, redaction    [B6 âœ…]
â”‚   â”œâ”€â”€ lockfile.py             single-instance lock                 [B6 âœ…]
â”‚   â”‚
â”‚   â”‚                           (no database here â€” B6 moved it to
â”‚   â”‚                           %LOCALAPPDATA%\Point\runtime.db. Override with
â”‚   â”‚                           POINT_DB, then config `database.path`. The old
â”‚   â”‚                           backend\runtime.db was COPIED, not moved, and
â”‚   â”‚                           is deliberately still on disk.)
â”‚   â”‚
â”‚   â”œâ”€â”€ runtime\                runtime, dispatcher, task, task_manager,
â”‚   â”‚                           state, errors                        [B1 âœ…]
â”‚   â”‚                           events.py is envelope v1              [B6 âœ…]
â”‚   â”œâ”€â”€ managers\               session, session_manager             [B2 âœ…]
â”‚   â”‚                           message, message_history,
â”‚   â”‚                           message_translator                   [B3 âœ…]
â”‚   â”‚                           project, pathguard, gitignore,
â”‚   â”‚                           project_manager                      [B7 âœ…]
â”‚   â”‚                           context, attachment                  [B8 âœ…]
â”‚   â”‚                           agent.py (AgentConfig only)          [B9 âœ…]
â”‚   â”‚                           memory.py                            â† EMPTY
â”‚   â”‚                           tool.py is DELETED, not filled â€” see below
â”‚   â”œâ”€â”€ agents\                 builtins, registry, executor         [B9 âœ…]
â”‚   â”œâ”€â”€ tools\                  models, schema, registry, executor,
â”‚   â”‚                           faults, builtins\ (read_file,
â”‚   â”‚                           list_files, web_search,
â”‚   â”‚                           load_task_result)                    [B10 âœ…]
â”‚   â”œâ”€â”€ database\               connection, session_repository       [B2 âœ…]
â”‚   â”‚                           migrations.py, task_repository.py    [B6 âœ…]
â”‚   â”‚                           project_repository                   [B7 âœ…]
â”‚   â”‚                           attachment_repository                [B8 âœ…]
â”‚   â”‚                           agent_repository                     [B9 âœ…]
â”‚   â”‚                           tool_call_repository                 [B10 âœ…]
â”‚   â”œâ”€â”€ providers\              base, bootstrap, errors, models, registry,
â”‚   â”‚                           __init__.py                          [B6 âœ…]
â”‚   â”‚                           + anthropic\ gemini\ ollama\ openai\ openrouter\  [B4 âœ…]
â”‚   â”‚                           + mock\ (POINT_MOCK=1 only, never in the UI) [B6 âœ…]
â”‚   â”œâ”€â”€ streaming\              accumulator, adapter, events, manager [B5 âœ…]
â”‚   â””â”€â”€ tests\                  manual_provider_test.py,
â”‚                               manual_context_test.py               [B8 âœ…]
â”‚                               fixtures\ (mock_stream.json, mock_slow.json) [B6 âœ…]
â”‚                               + the seven B10 tool-loop scripts,
â”‚                                 replayed by `send --script <name>`  [B10 âœ…]
â”‚
â”œâ”€â”€ frontend\                   Go
â”‚   â”œâ”€â”€ cmd\point\              entry point                          [F1 creates]
â”‚   â””â”€â”€ internal\
â”‚       â””â”€â”€ array\              item.go, menu.go â€” seed for the shared list widget
â”‚
â””â”€â”€ design\                     these docs
```

`memory\` and `schemas\` **do not exist**. B13 and a later episode create what
they need. `agents\` was created by B9 and `tools\` by B10 â€” both are real.

`managers\provider.py` **is deleted** â€” superseded by `providers\` (B4). Keeping
a second provider abstraction alive was the whole reason to remove it; do not
recreate it.

`managers\tool.py` **is deleted too** â€” B10 did not fill it. Same call B9 made
for `managers\agent.py`'s business rules: a registry, an executor, a schema
checker, and four tools are not one manager, and `agents\` had already
established the split. `managers\agent.py` kept only the `AgentConfig` data
model; there is no `managers\tool.py` counterpart because `Tool` and
`ToolResult` are data the tool package itself owns. Do not recreate it.

`providers\__init__.py` was missing while every other package had one; **B6
added it.** The tree worked as a namespace package regardless, so this was
cleanup, not a bug fix.

