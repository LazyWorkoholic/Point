# Workflow — Start to Finish

Every session, written out. Copy the prompt, paste it, run the checks, paste the
record prompt, `/clear`, next session. Nothing to improvise.

**This file is for you, not for Claude Code.** Never tell it to follow this file.

---

## The session rule

**One session per episode. `/clear` between them.**

Not one long session: Claude Code auto-compacts when context fills, and
compaction summarises away the exact decisions and edge cases you need three
episodes later. You get a model that half-remembers its own choices and starts
contradicting them. The docs are the memory; a fresh session reads them and knows
more than a compacted one remembers.

Your sidebar will fill with sessions named per episode. That is correct.

**Within an episode, stay in one session** — even across five rounds of fixing a
failed check. The boundary is the episode, not the message count.

Only `F1+F2` shares a session. B6 and B7 used to — B6 grew too large in the
Session 0 audit, so they are now one episode each.

---

## Phase 0 — Setup (you, ~10 minutes)

```
1. Already done: CLAUDE.md at root, VERSION, README.md, deleted Design_1/
   Design_2/ micro.md tui_components.md, WORKFLOW row in CLAUDE.md.

2. Replace CLAUDE.md with the NEW one (this batch). The old one had Unix
   commands and the wrong repo layout.

3. Put design\ at D:\Projects\Point\design\ with everything from this batch.

4. Rename:  D:\Projects\Point\frontend\internal_state  →  frontend\internal
   Delete:   D:\Projects\Point\frontend\component  (empty)

5. Replace frontend\internal\array\menu.go with the new one in this batch.

6. Add to .gitignore:
      .point/
      backend/runtime.db
      *.db-wal
      *.db-shm

   NOT credentials.toml or state.json — B6 puts those in %APPDATA%\Point
   and %LOCALAPPDATA%\Point, outside the repo. If either ever shows up at
   the repo root, something wrote it to the wrong place; fix that rather
   than ignoring it.

7. git add -A && git commit -m "design docs + windows workflow"
```

Then open Claude Code with **`D:\Projects\Point`** as the working directory. Open
a subfolder and `CLAUDE.md` will not auto-load.

---

# SESSION 0 — Verification pass (no code)

**Goal:** prove the docs match your repo before you rely on them 25 times.

**Paste:**
```
Read CLAUDE.md, then design/project_idea.md, then
design/tasks/backend/INDEX.md and design/completed/backend/INDEX.md.

Do not write any code. Do not edit any file yet.

1. Compare design/completed/backend/ against the actual code in backend\.
   For each of B1-B5, confirm the files it claims exist actually exist and
   do roughly what the doc says. List every mismatch.
2. Check the makefile at the repo root. Tell me what targets it has and
   whether they will work on Windows PowerShell.
3. Read backend\main.py and tell me exactly how the CLI harness in B6
   should be structured so it reuses this entry point instead of
   duplicating it.
4. List anything in the docs that is ambiguous, contradicts the code, or
   assumes something that is not true of this repo.

Then stop and wait.
```

**You do:** read its findings. Anything it flags in the docs, fix with:
```
Update <file> to match reality: <what you found>. Show me the diff.
Change no code.
```

**Then:** `/clear`

---

# SESSION 1 — B6 Schema v2, Envelope v1, Bootstrap

**Goal:** every column v1 needs is in the ground, tasks are durable, and the wire
protocol is the one the design actually documents.

**No longer batchable with B7.** Session 0's audit grew this episode
considerably — it now carries a new `tasks` table, a `sessions` rebuild, the §4
envelope with `hello`/`ping`, and six correctness fixes inherited from B1–B5.
Pairing it with B7 would push both past the point where one session's checks are
trustworthy.

**Before you start:** `backend\` is untracked in git. Commit it first — this
episode moves and migrates `runtime.db`, and you want a restore point.

```powershell
cd D:\Projects\Point
git add backend
git commit -m "backend: commit B1-B5 before B6 migrates the database"
```

**Paste:**
```
Read CLAUDE.md, then design/tasks/backend/B06-schema-bootstrap.md.

Implement only what its Build section lists. Follow the Do NOT section.
All commands you print must be PowerShell.

This episode adopts the project_idea.md §4 envelope. That is a
serialization-boundary change only — IncomingEvent and OutgoingEvent keep
their fields and no handler signature moves.

Do NOT resolve a NULL provider/model against config. B8 owns resolution;
this episode only makes NULL storable.

Stop at the verification gate and print the manual checks with real paths
for this repo filled in.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** run all 13 checks. The ones that matter most are #4 (the `sessions`
rebuild kept its data), #8 (crash recovery marks the orphan `backend_died`), #9
(every line is a real envelope), and #11 (the backend actually exits on stdin
EOF instead of hanging).

**Record:**
```
Write design/completed/backend/B06-schema-bootstrap.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record the before/after envelope shape explicitly — F2 will be
written against it, and it is the one reference that matters.
```

**Then:** `git commit -m "B6: schema v2, envelope v1, bootstrap"` · `/clear`

---

# SESSION 2 — B7 Project System

**Goal:** projects have identity, and moving the folder does not orphan them.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B07-project-system.md.

Implement only what its Build section lists. Follow the Do NOT section.
All commands you print must be PowerShell.

B6 created the projects table and the path columns this episode fills.
Read design/completed/backend/B06-schema-bootstrap.md first to see what
actually landed.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the check that matters is renaming the project folder and confirming
its memories and sessions still resolve through the ID registry.

**Record:**
```
Write design/completed/backend/B07-project-system.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how they
were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record the exact behavior of resolve() (the path guard) and how a
soft lock is acquired, held, and released — B10's read_file tool and F8's
@ picker both call through this path, and pinning the exact contract now
saves a re-read of this whole episode later.
```

**Then:** `git commit -m "B7: project system"` · `/clear`

---

# SESSION 3 — B8 Context Builder

**Goal:** one function decides what enters the model's context, with a log.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B08-context-builder.md.

Before writing anything: read design/completed/backend/B06-schema-bootstrap.md
and design/completed/backend/B07-project-system.md to see what actually
landed. Attachment resolution in this episode must go through B7's
resolve_path() and its path guard — do not read files directly.

Implement only what its Build section lists. This episode is deliberately
dumb — do NOT implement search, summarization, promotion, or embeddings.
The MemoryService interface must be fully defined with most methods
raising NotImplementedError.

Stop at the verification gate. Print the manual checks as PowerShell with
real paths.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B08-context-builder.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
that INDEX.md status table.

Also record exactly what context.report() returns — the section names,
their fields, and how eviction is described. F6's status card renders
this directly, and the card breaks silently if the shape drifts even
slightly from what you write here.
```

**Then:** commit · `/clear`

---

# SESSION 4 — B9 Agent Foundation

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B09-agent-foundation.md.

Note: backend\managers\agent.py exists but is empty, and backend\agents\
does not exist — create what the episode needs.

Implement only the Build section. Do not implement tools (B10) or agent
behaviour (B11). Seed the three built-in configs only.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B09-agent-foundation.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
that INDEX.md status table.

Also record the final AgentConfig field list exactly as it ended up, not
as it was speced — B10's tool grants and B11's three agents both key off
these field names, and any drift here means the next two sessions are
built against a stale shape.
```

**Then:** commit · `/clear`

---

# SESSION 5 — B10 Tool System

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B10-tool-system.md.

Implement only the Build section. No terminal tool, no write tool, no MCP —
those are permanently or explicitly out of scope per the Do NOT section.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B10-tool-system.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record the Tool and ToolResult shapes, and exactly what a
bounds-exceeded task.failed looks like on the wire. B11 calls this
machinery on every single agent turn, and needs the precise error code
to detect it.
```

**Then:** commit · `/clear`

---

# SESSION 6 — B11 The Three Agents

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B11-three-agents.md.

Build order inside this episode: Explainer, then General, then Research.
The Filler must never write to disk — it returns a rewritten region.

Stop at the verification gate with PowerShell checks. Include the
stat-before/stat-after check that proves no file was written.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** pay attention to check 3 (mtime unchanged). That is the one that
proves the no-disk-write rule actually holds.

**Record:**
```
Write design/completed/backend/B11-three-agents.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record each agent's final tool list and default_attach_mode, exactly
as built. B12's routing and F9's result cards both branch on these
per-agent values, and getting them down now saves re-reading three files
later.
```

**Then:** commit · `/clear`

---

# SESSION 7 — B12 Routing, Attachments, Caps

**Goal:** v1 backend complete.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B12-routing-attachments.md.

Implement only the Build section. Parse `--advanced` and refuse it.

Stop at the verification gate with PowerShell checks, including the
concurrency test with Ollama capped at 1.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B12-routing-attachments.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
that INDEX.md status table. Note explicitly that this closes the v1
backend.

Also record which checkpoint decision you made below (3-week vs 6-week
plan) in the Handoff section — every session from here on is written
assuming you picked one, and future-you should not have to guess which.
```

**Then:** commit · `/clear`

### ⚠ CHECKPOINT — decide now, with real information

Count your remaining weeks:

```
~3 weeks left  → do B15, then all F episodes, then B13. DROP SSH (B16).
~6 weeks left  → follow the full order below.
```

---

# SESSION 8 — F1 + F2 (batchable)

**Goal:** the Go shell and the link to Python. **No visible feature — expected.**

**Paste:**
```
Read CLAUDE.md, then design/tasks/frontend/INDEX.md,
design/tasks/frontend/LAYOUT.md, design/tasks/frontend/THEME.md,
then F01-bootstrap.md and F02-backend-client.md.

These two are batchable — do both, F1 first.

Go module is github.com/lazyworkoholic/point, so frontend packages are
github.com/lazyworkoholic/point/frontend/internal/...
Create frontend\cmd\point\main.go as the entry point.

frontend\internal\array\ already has item.go and menu.go — a hand-written
list widget. Read it. Do not rewrite it in this episode; F7 turns it into
the shared Popup. Note anything about it you would change.

The Python backend is started as a child process with working directory
D:\Projects\Point\backend running `uv run python main.py`.

Stop at F2's verification gate with PowerShell checks for both episodes.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** F1's key check is `go test ./frontend/internal/layout/...` passing
with no terminal. F2's is killing the Python process and watching it recover.

**Record:**
```
Write design/completed/frontend/F01-bootstrap.md and
design/completed/frontend/F02-backend-client.md, following the format in
design/completed/backend/INDEX.md (frontend uses the same shape). Include
the bugs we hit and how they were fixed, and what the next episode
inherits. Add both rows to design/completed/frontend/INDEX.md's status
table.

Also record Compute()'s exact signature and every field on Layout (F1),
and the Go-side struct names for the envelope plus the Transport
interface (F2). Every frontend episode from F3 onward renders into what
F1 returns and sends or receives through what F2 defines — this is the
one pair of contracts the rest of the frontend is built on.
```

**Then:** commit · `/clear`

---

# SESSION 9 — F3 Chat Viewport

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/LAYOUT.md,
design/tasks/frontend/THEME.md, and F03-chat-viewport.md.

Implement only the Build section. User messages get a rounded box; AI
replies get a left accent bar and NO box.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F03-chat-viewport.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record how a message becomes a renderable, cached block — the
caching key, and where the AI-accent-bar vs user-box decision lives. F5
streams tokens into this same structure and F9 inserts task slots into
it; both need to reuse it, not reinvent it.
```

**Then:** commit · `/clear`

---

# SESSION 10 — F4 Query Input

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/LAYOUT.md, and F04-query-input.md.

Enter sends, Alt+Enter inserts a newline. Growth is overlay + auto-scroll —
the viewport is never resized.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the draft-survival check matters — kill the app mid-typing, restart,
confirm the text and cursor return.

**Record:**
```
Write design/completed/frontend/F04-query-input.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record how an attachment chip is represented in the input's model
(the exact fields, not just the picture). F8 is what populates this list,
and needs to match the field names exactly, not approximately.
```

**Then:** commit · `/clear`

---

# SESSION 11 — F5 Streaming, Queue, Cancel

**Goal:** the first session where it feels like an application.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F05-streaming-queue.md.

Queue depth 1 by default; the input locks only at capacity, and always
with the reason visible. Ctrl+C cancels the stream, it does not quit.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F05-streaming-queue.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the exact message/event types for "a task reserves a
transcript slot" and "a terminal event was received." F6's task card and
F9's result card both need to listen for these, using the same names you
land on here.
```

**Then:** commit · `/clear`

---

# SESSION 12 — F7 Popup Framework

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F07-popup-framework.md.

frontend\internal\array\menu.go is the seed for this — read it and fold it
into the shared Popup rather than writing a second list.

The palette and the help screen must be the SAME component with different
data. One implementation.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F07-popup-framework.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the final Popup struct's fields and the Confirm component's
API, exactly as built. F8, F9, F10, and F11 are all consumers of this
one widget — each must match it exactly, and a near-match that compiles
but drifts on one field name is worse than an obvious break.
```

**Then:** commit · `/clear`

---

# SESSION 13 — F6 Right Pane

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/LAYOUT.md, and F06-right-pane.md.

No CPU/RAM/GPU. Context fill % is the first line. The expanded context
composition card comes straight from B8's context.report — do not
reimplement the calculation.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the concurrency-honesty check — the pane and `tasks list` must agree.

**Record:**
```
Write design/completed/frontend/F06-right-pane.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the exact shape of a task-card entry (its fields and its
state values). F9 appends result cards into this same list and must use
identical field names, or the tray silently shows a mix of two shapes.
```

**Then:** commit · `/clear`

---

# SESSION 14 — F8 @ Picker and Attachments

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F08-at-picker.md.

Mode is decided by the first character after @. Go never reads files —
the backend resolves every path.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F08-at-picker.md following the format in
design/completed/backend/INDEX.md. Include the bugs we hit and how they
were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the attachment resolution response shape — the token
estimate field and the current/changed indicator. F9's result cards
reuse this exact same attach/reference mechanism for task results, and
need the field names to match.
```

**Then:** commit · `/clear`

---

# SESSION 15 — F9 Task Tray, Result Cards, Diff Card

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F09-task-tray-cards.md.

The backend returns a rewritten region; Go computes the diff locally.
`e` opens the proposed RESULT in a scratch buffer, not the patch.
Attach and save are independent axes.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** check that edit-then-apply lands the *edited* text, not the original.

**Record:**
```
Write design/completed/frontend/F09-task-tray-cards.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record exactly how the diff card computes and applies a diff — the
function signature and what it takes as input, not just the UI
behaviour. F15 reuses this precise mechanism for the Filler's inline
diff, so it needs a real function to call, not a description to
re-implement.
```

**Then:** commit · `/clear`

---

# SESSION 16 — F10 Settings, Providers, First Run

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F10-settings-first-run.md.

Ollama is detected silently but selected explicitly. Point must never
rewrite config.toml. Keys are validated with list_models() at entry.

Stop at the verification gate with PowerShell checks, including the
fresh-machine simulation.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the config.toml byte-identical check. If Point rewrote it, stop.

**Record:**
```
Write design/completed/frontend/F10-settings-first-run.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the Settings popup's tab structure and how a new tab gets
registered. F11 adds a Memory tab to this same popup later and should be
able to do it without touching F10's code — write down what makes that
true.
```

**Then:** commit · `/clear`

---

# SESSION 17 — F12 Workspace Manager and Lifecycle

**Goal:** the differentiator.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F12-workspace-manager.md.

Unloading a view must NEVER cancel a task. Startup must NOT auto-load the
last workspace — show the chooser.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** unload a workspace with a research task running and confirm the task
survives. This is a demo beat.

**Record:**
```
Write design/completed/frontend/F12-workspace-manager.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the Workspace interface exactly as implemented — the real
signatures for Init/Update/View/Suspend/Resume/ID. F13's Editor
workspace and F17's preview both implement this interface and must
match it exactly, method for method.
```

**Then:** commit · `/clear`

---

# SESSION 18 — F13 Editor Core

**Goal:** the largest frontend episode. **May take two sessions** — if so, stop at
a working point, record what is done, `/clear`, and resume with a note.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/LAYOUT.md, and F13-editor-core.md.

Auto-indent is Tier 1 only (copy previous line's indentation). Line
endings detected on open and preserved on save — this matters on Windows.
Undo must group keystrokes, and an agent-applied diff must be ONE undo.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the CRLF check — open and save a file without editing, then
`git status`. If the whole file shows modified, it is wrong.

**Record:**
```
Write design/completed/frontend/F13-editor-core.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table. If this took two
sessions, say so and record what was already inherited going into the
second one.

Also record the Buffer interface and exactly how undo grouping works.
F15's ghost text and diff-apply write into this same buffer and must
respect its undo boundaries, or Ctrl+Z stops being a single, trustworthy
action.
```

**Then:** commit · `/clear`

---

# SESSION 19 — F15 Filler-Explainer Integration

**Goal:** v1 feature-complete.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F15-explainer-integration.md.

Explanations render in an overlay panel inside the Editor. Fill produces a
diff card; applying goes into the UNSAVED buffer so Ctrl+Z undoes it.
Nothing writes to disk.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F15-explainer-integration.md following
the format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row
to design/completed/frontend/INDEX.md's status table. Note explicitly
that v1 is now feature-complete on the frontend.

Also record how ghost text is represented in the buffer model, distinct
from real content — this is the pattern to reuse if F17 or anything
later ever needs to show something transient in the editor.
```

**Then:** commit · `/clear`

---

# SESSION 20 — B15 Lifecycle and Crash Recovery

**Goal:** demo-critical despite being P2. Do this before the other P2 work.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B15-lifecycle-resources.md.

Unloading a view never cancels a task. The backend refuses to free
anything a running task holds. Report bounded growth, never freed RSS.

Stop at the verification gate with PowerShell checks, including killing
the Python process mid-stream.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** this is the strongest beat in your demo. Test it properly.

**Record:**
```
Write design/completed/backend/B15-lifecycle-resources.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
that INDEX.md status table.

Also record the exact recovery sequence — what marks a task failed, what
gets persisted, in what order. B16 gives each SSH connection its own
runtime with this same lifecycle, and needs it to already be correct
before it adds a second dimension (connections) on top.
```

**Then:** commit · `/clear`

---

# SESSION 21 — B14 Usage and Limits

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B14-usage-limits.md.

Prices live in config, never hardcoded. Caps refuse BEFORE spending and
never kill a running task.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B14-usage-limits.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record the usage.updated event's exact payload shape. F6's status
card was built expecting certain field names for tokens and cost — check
they still match, and record the real ones here so nobody has to
diff the two files to find out.
```

**Then:** commit · `/clear`

---

# SESSION 22 — F14 Editor Terminal

**Cuttable.** If time is short, skip it — nothing depends on it.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F14-editor-terminal.md.

Tier 0 command runner plus a tea.ExecProcess hatch. No PTY, no VT100
emulator. Backend tasks keep running while the TUI is suspended.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F14-editor-terminal.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits (or note plainly
that this episode was skipped, and why). Add a row to
design/completed/frontend/INDEX.md's status table.

Also record how the terminal's size mode is persisted in Suspend(). F17's
web preview shares this pane in spirit and should follow the same
persistence pattern if it ever grows one.
```

**Then:** commit · `/clear`

---

# SESSION 23 — B13 + F11 Memory

**⚠ Precondition.** B13 opens with a gate: you must list **three concrete
failures** you have actually experienced using Point. If you cannot, skip this
session and come back later. That is by design.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B13-memory-system.md.

The precondition failures I observed are:
1. <your first observed failure>
2. <your second>
3. <your third>

Scope the build to those three. Do not build the rest by default.
Retrieval stays manual; automatic ships off.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

Then, same session or a new one:
```
Read CLAUDE.md and design/tasks/frontend/F11-memory-popup.md.
Implement it against the B13 backend we just built.
Stop at the verification gate with PowerShell checks.
```

**Record:**
```
Write design/completed/backend/B13-memory-system.md and
design/completed/frontend/F11-memory-popup.md, each following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to each
side's status table.

In B13's file, list the three precondition failures verbatim and state
how each was addressed — that is the evidence this episode was built for
a real reason, not a guess.

Also record the final memory_records status/source values as
implemented, and confirm they match decisions.md's D17–D20. If anything
drifted during the build, note it here rather than silently living with
the difference.
```

**Then:** commit · `/clear`

---

# SESSION 24 — B16 SSH

**Drop this if you are at ~3 weeks.**

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B16-ssh-serving.md.

Topology B: one runtime per connection, shared database. Permissions are
enforced in the BACKEND, never only in the UI. SSH is off by default.

Stop at the verification gate with PowerShell checks. I will test with a
second terminal on this machine.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the important check is calling a guest-forbidden command *directly*,
not through the UI — a UI that merely hides a button is not a permission.

**Record:**
```
Write design/completed/backend/B16-ssh-serving.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table.

Also record the exact Permissions struct fields and precisely where each
one is checked. This is the part of the whole project most likely to
come up in an interview, so the doc should let you point at the exact
enforcement line, not just describe the idea.
```

**Then:** commit · `/clear`

---

# SESSION 25 — F16 Polish and Demo Recording

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F16-polish.md.

Walk every component for empty/loading/error states, audit all nine
terminal sizes, and QA all four themes.

Stop at the verification gate with PowerShell checks. Then write the
README following the F16 spec, including the honest "what it does not do"
section.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** record the demo script as a GIF. Running it end to end will find bugs
— that is part of the point.

**Record:**
```
Write design/completed/frontend/F16-polish.md following the format in
design/completed/backend/INDEX.md. Include the bugs we hit and how they
were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the final breakpoint numbers you settled on (LAYOUT.md
called them provisional) and the measured idle CPU. Both numbers are
needed verbatim for the SRS/SDS documents later — write them down now so
they don't have to be re-measured then.
```

**Then:** commit · `/clear`

---

# SESSION 26 — B17 Hardening

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B17-hardening.md.

The test suite must run with no network access. Packaging is PyInstaller
--onedir, never --onefile.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B17-hardening.md following the format in
design/completed/backend/INDEX.md. Include the bugs we hit and how they
were fixed, and what the next episode inherits. Add a row to that
INDEX.md status table. Note explicitly that the planned backend work
ends here.

Also record the full error taxonomy as implemented (every code, not just
the categories) and the measured cold-start time for the packaged build.
Both go directly into the SRS/SDS testing documents.
```

**Then:** commit · `/clear`

---

# SESSION 27 — F17 Web Preview (optional)

**Goal:** edit a static site in Point and watch the browser update live.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F17-web-preview.md.

Tier 1 (static, no package.json): Point IS the dev server — net/http +
fsnotify + an injected websocket reload script.
Tier 2 (npm project): run the project's own dev script in the F14 terminal
pane and open the browser at its port. Do NOT reimplement HMR.
Tier 3 is parked — no framework detection, no server supervision.

The preview must never touch the Go/Python stdin-stdout protocol, and the
watcher must stop entirely when the preview closes.

Stop at the verification gate with PowerShell checks, including the
idle-CPU measurement.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the idle-CPU check matters most. A polling file watcher shows up
there and nowhere else.

**Record:**
```
Write design/completed/frontend/F17-web-preview.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Also record the measured idle CPU with the preview running — this is the
number that proves the "does not touch the stdin/stdout protocol and
costs almost nothing" claim in decisions.md's D86. If it does not match
what D86 estimated, update D86 rather than leaving two numbers in
disagreement.
```

**Then:** commit · `/clear`

---

## When something goes wrong

**A check fails** — same session:
```
Check 4 failed. Here is the exact output:
<paste>
Fix it and tell me what to re-run.
```
Five failed rounds → stop, `/clear`, restart the episode with a note about what
went wrong. A session that has failed repeatedly is worse than a fresh one.

**A command in the doc does not exist:**
```
The doc's verification command is <X> but this repo has <Y>. Either make
the code match the doc, or tell me the doc is wrong and show me the fix.
Do not just change the command silently.
```

**A check reveals a design problem, not a bug** — stop. Do not let Claude Code
redesign. Come back, decide, update the doc, then resume.

**You have a new idea mid-project:**
```
Add a parking lot entry to design/tasks/<side>/INDEX.md for <idea>.
Follow the existing format: why parked, hook, anchor episode, what
unparking takes. Do not implement it.
```
For a UI idea, write it into `design/tasks/frontend/TEMPLATES.md` yourself using
the `UI-XX` template.

---

## Reusable prompts

**Where am I:**
```
Read design/tasks/backend/INDEX.md, design/tasks/frontend/INDEX.md, and
both completed/ INDEX files. What is done, what is next, and does anything
disagree with the actual code?
```

**A doc is wrong:**
```
design/tasks/frontend/F09-task-tray-cards.md says <X>, but <Y> is true
because <reason>. Update that file. Do not change code. Show me the diff.
```

**Record a decision:**
```
Add an entry to design/decisions.md for <the choice>. Follow the existing
format: decision, alternatives, why, what it costs.
```

**Interview prep:**
```
Read design/decisions.md and design/completed/. Ask me ten hard questions
about this project, the kind an interviewer would ask, and push back when
my answer is weak.
```

---

## Six rules that keep this from degrading

1. Never let Claude Code mark something complete. Only you, after running checks.
2. Never skip the verification gate, even when the change looks trivial.
3. Never let it build a parked item because it seems adjacent.
4. When code and docs disagree, stop. Neither side gets silently "fixed".
5. `/clear` between episodes. Every time.
6. Commit after each episode, with the episode id in the message.

---

## What "done" looks like

v1 is finished when you can run this end to end without a retake. Each beat
proves one architectural claim — that is why the order is what it is.

```
0:00  LAUNCH
      point → TUI paints instantly, header animates, provider chooser appears.
      Ollama shown as detected but NOT pre-selected. Pick it.
      PROVES  fast startup · informed local-model consent · zero-config boot

0:30  CHAT + QUEUE + CANCEL
      Ask something. Tokens stream. Type the next question WHILE it streams —
      it queues visibly as an editable pending bubble. Ctrl+C mid-stream →
      partial reply stays, marked interrupted.
      PROVES  streaming · queue-don't-lock · cancellation · partial persistence

1:10  CONTEXT CARD                                    ← strongest quiet beat
      Expand the status card. Show the breakdown: system prompt, attachments,
      history, and what gets evicted next turn.
      PROVES  you understand context budgeting. Nobody else shows this.

1:40  BACKGROUND AGENT
      @research something. It reserves its slot in the transcript and appears
      in the task panel. Keep chatting — normal conversation continues while
      it runs. With Ollama capped at 1, fire a second: it shows QUEUED, and
      the CLI agrees.
      PROVES  real concurrency · honest queueing, not a fake spinner

2:20  EDITOR + GHOST TEXT + DIFF                              ← new
      Leader → editor. Open a file, syntax highlighting. Select a function,
      invoke the Explainer → explanation streams into the side panel.
      Cursor-invoke the Filler → watch GHOST TEXT stream in at the cursor →
      diff card → press e, edit the proposal → apply → Ctrl+Z undoes it in ONE
      press. Check the file's mtime: unchanged until save.
      PROVES  editor · editor context · agent-proposes-user-applies ·
              watch-it-write without giving up the keypress guarantee

3:20  AUTOSAVE + WEB PREVIEW                                  ← new
      Open a plain HTML/CSS folder. Ctrl+P → browser opens, Point IS the dev
      server. Edit the CSS, stop typing → autosave fires ~1s later → the page
      reloads. No npm anywhere.
      PROVES  the whole edit-save-see loop, with no framework and no
              process supervisor

4:00  BACK TO CHAT
      Research is done. Open its card: attach summary, save to memory.
      Ctrl+H shows the record with its provenance — source, origin task, age.
      PROVES  the memory/approval design · reference-not-copy attachments

4:30  UNLOAD WITH A TASK RUNNING                              ← updated
      Start another @research. Unload the editor: "unloading view — Research
      task continues in background". Show the switcher: each loaded workspace
      has its OWN idle countdown, and the active one never unloads. Return —
      state rehydrates from the DB, draft still in the input.
      PROVES  per-workspace lifecycle · tasks independent of UI · rehydration

5:10  KILL IT                                        ← the beat that separates
      In another terminal, kill the Python process. Banner appears.
      Auto-restart. Session recovers, partial message intact.
      PROVES  crash tolerance. Very few student projects survive this live.

5:40  SSH (optional)
      Second terminal SSHes in, sees the shared session list, and is refused
      the terminal pane — by the backend, not by a hidden button.
```

Every beat has an episode behind it. Nothing else is required for v1 to be real.

**Record it.** Live demos fail; a 90-second capture in the README is the highest
return per minute of effort in the whole project, and running the script end to
end will find bugs before an audience does.
