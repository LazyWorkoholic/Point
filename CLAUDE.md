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
- **Manual verification that touches `%APPDATA%\Point` or `%LOCALAPPDATA%\Point`
  must run as one script against a renamed-aside scratch profile** — never
  against the live one, and never by patching individual env vars per command.
  Separate tool calls do not share shell state, so `$env:POINT_DB` (or any
  other env var) set in one call is gone by the next; the fix is one combined
  script, not a var per call. (B7: this is exactly how a test project landed
  two rows in the real `runtime.db`, and a separate `cat >` clobbered the real
  `config.toml`.)
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

```
D:\Projects\Point\
├── CLAUDE.md  VERSION  README.md  makefile  go.mod  go.sum
├── pyproject.toml  uv.lock  .python-version  .gitignore  .venv\
│
├── backend\                    Python — run from inside this folder
│   ├── main.py                 transport only: stdin reader thread,
│   │                           outbox writer, teardown              [B6 ✅]
│   ├── bootstrap.py            resolve_db_path, run_migrations,
│   │                           build_runtime, shutdown_runtime      [B6 ✅]
│   ├── cli.py                  doctor · migrate · session new|list ·
│   │                           send · tasks list                    [B6 ✅]
│   ├── appconfig.py            paths, config.toml, key resolution   [B6 ✅]
│   ├── logging_setup.py        rotating file + stderr, redaction    [B6 ✅]
│   ├── lockfile.py             single-instance lock                 [B6 ✅]
│   │
│   │                           (no database here — B6 moved it to
│   │                           %LOCALAPPDATA%\Point\runtime.db. Override with
│   │                           POINT_DB, then config `database.path`. The old
│   │                           backend\runtime.db was COPIED, not moved, and
│   │                           is deliberately still on disk.)
│   │
│   ├── runtime\                runtime, dispatcher, task, task_manager,
│   │                           state, errors                        [B1 ✅]
│   │                           events.py is envelope v1              [B6 ✅]
│   ├── managers\               session, session_manager             [B2 ✅]
│   │                           message, message_history,
│   │                           message_translator                   [B3 ✅]
│   │                           agent.py, memory.py, tool.py         ← EMPTY
│   ├── database\               connection, session_repository       [B2 ✅]
│   │                           migrations.py, task_repository.py    [B6 ✅]
│   ├── providers\              base, bootstrap, errors, models, registry,
│   │                           __init__.py                          [B6 ✅]
│   │                           + anthropic\ gemini\ ollama\ openai\ openrouter\  [B4 ✅]
│   │                           + mock\ (POINT_MOCK=1 only, never in the UI) [B6 ✅]
│   ├── streaming\              accumulator, adapter, events, manager [B5 ✅]
│   └── tests\                  manual_provider_test.py
│                               fixtures\ (mock_stream.json, mock_slow.json) [B6 ✅]
│
├── frontend\                   Go
│   ├── cmd\point\              entry point                          [F1 creates]
│   └── internal\
│       └── array\              item.go, menu.go — seed for the shared list widget
│
└── design\                     these docs
```

`agents\`, `memory\`, and `schemas\` **do not exist**. B9 and B10 create them.

`managers\provider.py` **is deleted** — superseded by `providers\` (B4). Keeping
a second provider abstraction alive was the whole reason to remove it; do not
recreate it.

`providers\__init__.py` was missing while every other package had one; **B6
added it.** The tree worked as a namespace package regardless, so this was
cleanup, not a bug fix.
