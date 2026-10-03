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

# SESSION 11b — B12 Verify + F5 Reload Pairing

**Goal:** spend B12's new data — make a reloaded transcript render exactly like a live one, in every send pattern.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/S11b-b12-verify-f5-pairing.md.

B12 is complete and its gate passed. Pair on task_id only -- never
adjacency, ordering, or timing. Frontend only; every backend field this
needs is B12's and already exists.

Note one accepted B12 limitation the spec predates: a task cancelled or
failed BEFORE its first token persists no assistant message row at all
(finalize_interrupted returns None for empty text -- an empty message
fails B3's validation). Its outcome lives on the tasks row only. Rebuild
that footer by joining the user message's task_id to the tasks row
(status, error_code, agent_id). Read B12's completed doc for the full
account before you start.

More generally: this spec was written before B12 was built, so that
empty-row case is one known place where it and reality diverge. The
other fields it assumes -- task_id on user messages, invocation.agent,
terminal outcome -- B12 delivered and I verified by hand. If you find
anything else the spec assumes that is not true of this repo, stop and
tell me before writing code rather than working around it. B12's own
session caught three such assumptions that way (QUEUED not being an
existing state, the concurrency gate being unrunnable as three separate
processes, and the gate's SQL not matching the schema), and each one
changed what got built.

Stop at the verification gate with PowerShell checks.
```

**You do:** run all four send patterns and restart after each — sequential, queued,
two `@research` fired together, and plain + `@research` fired together. All four must
reload `Q,A,Q,A`. The last two are the ones that split today; if either still groups,
the pairing isn't using `task_id`.

**Record:**
```
Write design/completed/frontend/S10b-b12-verify-f5-pairing.md following
the format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Record the exact pairing key and rule as implemented -- that reload
groups a user message with the assistant message sharing its task_id,
and that adjacency, ordering, and elapsed time are never used. F6's task
panel and F9's result cards both group messages and must use this same
key, exactly.

Also record what a reloaded exchange looks like in each state: a
completed slot, a failed/timed-out one with its footer, a plain answer
(which must NOT become a slot despite carrying an invocation), and a
question whose task produced nothing.

If B12 did not persist a retry link, record that retried attempts still
render as separate questions and name the episode that would add it.
```

**Then:** commit · `/clear`

---

# SESSION 11c — B18 Message Pagination

**Goal:** make a bounded slice of history cheap to fetch, so F18 can window it.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B18-message-pagination.md.

seq is migration v6 (B12 took v5), keyset only, and GetSession stays
unbounded when message_limit is omitted -- F3 relies on that path and
must keep working until F18 replaces it. Backend only; do not touch
the frontend.

The (session_id, seq) index is the point of the episode -- verify it
is actually used with EXPLAIN QUERY PLAN rather than by eye.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code. B12's session caught three such assumptions that way and
S11b caught two, and every one of them changed what got built.
```

**Record:**
```
Write design/completed/backend/B18-message-pagination.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/backend/INDEX.md's status table.

Record the exact SessionMessages request/response payload shape and the
has_more_before/has_more_after semantics. F18 pages against these names
exactly.
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

**This session spawned two backend episodes — run 13b before 13c.** Its gate
found a live correctness defect that has nothing to do with the right pane
(B21), and it closed on seven of its eight "Done when" criteria because the
eighth needs a wire event that does not exist yet (B20). Both are specced.

---

# SESSION 13b — B21 Orphaned Messages Must Not Enter the Next Prompt

**Goal:** a task that failed without producing a token stops poisoning the
next unrelated send in that session.

**Do this one before B20.** It is a live correctness defect — the backend can
answer a question you did not ask and persist that as the reply to one you
did — where B20 only unblocks a stubbed display row.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B21-orphaned-message-context.md,
plus S11b's completed doc for the task_id pairing join this reuses, and
F06-right-pane.md's "Bug C" for the full diagnostic trace.

Join on outcome: exclude a user message whose task_id maps to a task
that failed with NO assistant row. Do NOT skip trailing unanswered
messages generically -- a genuinely in-flight turn has no assistant row
either. Do NOT invent a "was this a follow-up" heuristic; this project
already deleted one adjacency heuristic for corrupting correct
transcripts. Do NOT fabricate placeholder assistant text -- B12 decided
that deliberately and this does not reopen it.

Every exclusion must appear in ContextLog.evicted with its reason: a
silent exclusion is the same class of defect as the silent inclusion
being fixed.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code.
```

**You do:** the exact reproduction — `@research <x> --max-seconds 1 --wait`
(fails, zero output), then an unrelated send. The answer must be about the
NEW question. Then confirm a normal multi-turn follow-up still sees its own
history.

**Record:**
```
Write design/completed/backend/B21-orphaned-message-context.md following
the format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row to
design/completed/backend/INDEX.md's status table.

Record explicitly what happens to a task that failed AFTER producing
partial output -- that exchange must stay in history, and the test that
proves it.
```

**Then:** commit · `/clear`

---

# SESSION 13c — B20 `context.report` on the Wire

**Goal:** F6's Status card stub can become a real fill bar, and the expanded
context card (UI-09) becomes buildable at all.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B20-context-report.md, plus
B08-context-builder.md for the calculation this must reuse.

Transport only. Call B8's existing context.report() -- never a second
implementation of the token arithmetic, for any reason. Ship
ContextReport.to_dict() verbatim, every field, no subset: it was written
in B8 for exactly this consumer.

Push at TURN boundaries, not per delta. The numbers only move when a
turn completes.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code.
```

**You do:** confirm the wire answer matches `cli.py context report --json`
field for field for the same session, and that the numbers actually move
between two turns.

**Record:**
```
Write design/completed/backend/B20-context-report.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how they
were fixed, and what the next episode inherits. Add a row to
design/completed/backend/INDEX.md's status table.

Record whether ContextReport needed any new field. If it did, name which
and why -- it means B8 guessed wrong about what the card needs.
```

**Then:** commit · `/clear`

**Note:** F6's Status card stays a stub until a FRONTEND follow-up reads this
event. B20 gives the data a way to travel; something still has to read it.
That follow-up is SESSION 13d below.

---

# SESSION 13d — S13d Wire the Context Card

**Goal:** F6's Status card row 1 stops saying "context: not wired yet" and
starts showing real numbers, and the expanded context composition card
(UI-09) exists at all.

**This is a repair/wiring session, the same shape as S11b and S12b — not a
full F-episode.** It does not consume an F number.

**⚠ The task doc does not exist yet, and writing it is step one of this
session — not something to do in advance.** Its entire content is "read
B20's event and render it", so it cannot be specced accurately until B20's
completed doc says what that event actually looks like. B20's own Record
step asks whether `ContextReport` needed any new field; if it did, a doc
written beforehand would be specced against a shape that no longer exists.
F6 shipped a stub for exactly this reason rather than guessing at B8's
numbers, and its handoff warns that a near-match which compiles but differs
on one field name is worse than an obvious break.

**Paste:**
```
Read CLAUDE.md, design/completed/backend/B20-context-report.md, and
design/completed/frontend/F06-right-pane.md.

FIRST, before any code: write design/tasks/frontend/S13d-wire-context-card.md,
in the same shape as S11b/S12b's task docs. Scope, and nothing beyond it:

  1. Replace the Status card's row-1 stub ("context: not wired yet (B20)")
     with a real read of B20's context.report event.
  2. Build the "expand via enter/click" gesture on the Status card that F6
     deferred because there was nothing real to expand into -- UI-09, the
     context composition card: per-section token counts that sum to the
     total, and a named eviction prediction.

Reuse F6's existing card-rendering conventions -- statusCardLines, the
paneBox/overlay machinery, the theme roles. Do NOT redesign the card's
layout, and do NOT reimplement B8's token arithmetic: the numbers arrive
computed, and a second implementation of the same arithmetic disagrees
eventually.

Note that the right pane is NOT in FocusAware's rotation, so a card row
cannot currently be focused -- decide deliberately how the expand gesture
is reached, and record that decision.

Then show me the task doc and stop before writing code.
```

**You do:** confirm the section counts sum to the total and that the eviction
prediction names real messages — F6's Done-when #2, which it could not
satisfy.

**Record:**
```
Write design/completed/frontend/S13d-wire-context-card.md following the
format in design/completed/frontend/INDEX.md. Add a row to that INDEX's
status table.

Record explicitly that F6's Done-when #2 is now satisfied, and update
F06-right-pane.md's handoff note that said it was owed.
```

**Then:** commit · `/clear`

---

# SESSION 13e — S13e Backend Verbose Passthrough

**Goal:** a Go-launched app can run its backend with `--verbose`, so the
backend's DEBUG lines (B20's push confirmation among them) reach
`point.log` without launching the backend by hand.

**This is a repair/wiring session, the same shape as S11b, S12b and S13d —
not a full F-episode.** It does not consume an F number.

**Why it exists:** S13d's gate found that the app spawns the backend as a
fixed `uv run python main.py`, so no DEBUG line is ever logged, and there
was no working way to change that. `go run .\frontend\cmd\point --verbose`
is silently ignored, and `cli.py` always runs its own separate Runtime.
The task doc already exists — it was written at the end of S13d, from the
direct evidence.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/S13e-backend-verbose-passthrough.md.

Scope is exactly its Build section: spawn.go appends --verbose to the
backend launch command when POINT_BACKEND_VERBOSE is exactly "1", plus the
argv test. No CLI flags on the Go app, no backend change, no log-level
change, no log rotation.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code.
```

**You do:** run the gate with and without the variable — one
`B20: pushing context report` line per completed turn with it, none
without. Note `backend-stderr.log`'s size.

**Record:**
```
Write design/completed/frontend/S13e-backend-verbose-passthrough.md
following the format in design/completed/frontend/INDEX.md. Add a row to
that INDEX's status table.

Record the measured backend-stderr.log growth under verbose, and whether
stderrTail still ended on the real error after a deliberate exit.
```

**Then:** commit · `/clear`

---

# SESSION 13f — B23 Agent + Directory Listing on the Wire

**Goal:** give Go the two live queries F8's `@` picker needs and cannot get
today — which agents exist, and what is in an arbitrary directory — neither
of which any existing wire event answers.

**Why this episode exists.** F8 (Session 14) was started and stopped before
writing any code, per its own "stop and tell me if ambiguous" instruction:
`ListProjectFiles` requires a resolved project and cannot browse an
arbitrary path, and there is no wire event at all for listing registered
agents (`cli.py agents list` calls `AgentRegistry.list()` in-process, but
nothing exposes it to Go). Full trace in
`design/tasks/backend/B23-agent-and-directory-listing.md`'s "Why this
episode exists".

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B23-agent-and-directory-listing.md.

Two read-only wire additions, nothing else: ListAgents/AgentList over
AgentRegistry.list() (already correct, already tested -- this is
plumbing), and a directory-listing event that works with or without a
resolved project. Do NOT build any picker UI, fuzzy matching, or F8 code
-- that is Session 14's job once this lands.

The doc leaves one real decision open on purpose: whether a directory
LISTING should share the allow_outside_project gate a file READ uses, or
be exempt from it since it exposes names, not content. Decide it
deliberately and write down why, in the Build section's own words.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** confirm both new commands work with **no project open at all** —
that is the exact case F8 needs and the exact case `ListProjectFiles` cannot
serve.

**Record:**
```
Write design/completed/backend/B23-agent-and-directory-listing.md
following the format in design/completed/backend/INDEX.md. Include the
bugs we hit and how they were fixed, and what the next episode inherits.
Add a row to that INDEX.md status table.

Record the exact payload shapes for both new events -- field names and
all -- and which way you decided the allow_outside_project-for-listing
question. F8 (Session 14) is written against these exact names next.
```

**Then:** commit · `/clear`

---

# SESSION 14 — F8 @ Picker and Attachments

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F08-at-picker.md, and
design/completed/backend/B23-agent-and-directory-listing.md for the
ListAgents and directory-browse wire events this episode reads from --
they did not exist when F08's own spec was written.

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

# SESSION 15 — F9a Task Tray and Result Cards (F9, narrowed)

**F9 is SPLIT, not closed by this session.** It was started with its full
spec — tray, result cards, and the diff card — and stopped before writing any
code, per its own "stop and tell me if ambiguous" instruction. Four of its
eight "Done when" criteria assume things that are not true of this repo. The
evidence is in `design/completed/frontend/F09a-task-tray-cards.md`; the short
version:

1. **No Editor workspace exists.** `WorkspaceEditor` is a bare `layout`
   constant; there is no second `Workspace` implementation. F13 (Session 18)
   builds it, and F15 (Session 19) depends on both F13 *and* F9. So the diff
   card's only specified apply target and its only specified trigger both
   belong to later sessions. There is also no `WriteProjectFile` event, which
   is correct — so with no buffer, the card has nowhere to apply to at all.
2. **A task result cannot be attached.** `context_attachments.source_type` is
   `file`-only and `managers\context.py:564` refuses anything else outright.
   `a attach full` / `s attach summary` have no wire path. (The spec also
   names a `summary` mode that does not exist — the three modes are
   `full | reference | detached` — and its own gate expects `reference`.)
3. **Nothing writes `memory_records`.** The table exists since migration v2
   and has exactly the `status`/`source` columns the spec wants, but there is
   no `MemoryService`, no repository and no `SaveMemory` event. B11's
   `agent_proposals` stopgap is insert-only with one status, and both its
   files say `accept`/`reject` is B13's. `m save to memory` has no backend.
4. **The tray's labels have no wire source.** `ListTasks` returns
   `id, kind, state, session, agent, created_at, started_at, finished_at,
   error_code, live` — no topic, no token count. `tasks` has no result
   column; `usage_records` has tokens but no repository and no wire event.
   Durations are computable from the timestamps; "Research · tokio vs
   asyncio · 12.4k tok" is not.

**This session builds Done-when #1 and #8 only** — the global tray, and
discard-leaves-the-result-retrievable. Both are fully supported today:
`messages.task_id` exists and is indexed, so a finished task's result text is
retrievable exactly as B10's `load_task_result` gets it, including for a task
whose originating view was unloaded.

**Diff computation was cut too, deliberately, and not only for the Editor.**
There is nothing valid to compute a diff *from*. The Filler's reply is raw
model text that the prompt *hopes* is JSON, and nothing parses it. Having Go
parse unvalidated LLM output would be the first break in the
backend-resolves / frontend-renders split this project has kept through
`parse_invocation`, `ContextReport` and `ListDirectory` — in the worst place
for it, since the input is adversarial by nature and the consumer is a
destructive write path. **Session 17b (B24) fixes that**, and F9's diff-card
work resumes at Session 18b.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F09-task-tray-cards.md.

Build ONLY Done-when #1 (the global task tray, opening results whose
originating view has been unloaded) and #8 (discard removes the card
from view and leaves the task result retrievable from the tray).

Do NOT build: the diff card, any diff computation, any apply path,
attach-from-a-card, or save-to-memory. Each is blocked on something
that does not exist -- the Editor workspace (F13), a parsed Filler
proposal on the wire (B24), a non-file attachment source type (a B12
extension), and a memory write path (B13). Say explicitly at the end
which original Done-when items remain deferred and why.

The tray is global because the root owns the task list -- app/tasks.go
and refreshTasks already exist from F6. Reuse the F7 Popup; do not
write a second list component.

Stop at the verification gate with PowerShell checks.

If anything in the narrowed scope is still ambiguous, stop and tell me
before writing code.
```

**You do:** send `@research <topic>`, let it finish, and open its result from
the tray. Press `d` -- it must return to the tray's own list (not close
everything), with your typed filter and your place in the list intact, and the
same task still listed. Also confirm the tray opens and lists correctly with
**zero** tasks and while one is still running, and that a running row's m:ss
clock **advances while you watch it**, without closing and reopening.

**Two scope notes, both decided in-session and both recorded in F9a's
completed doc rather than changed in F09's spec:**

- The tray lists **background work only** -- research today. F09's "Global"
  bullet and Done-when #1 both say "tasks from every *workspace*", which is a
  statement about scope across workspaces and sessions, not a claim that every
  task record belongs here; and the episode's own Purpose is "where
  *background* work becomes visible". An inline turn's reply is already in the
  transcript, where F5's `o` expands it in place. project_idea.md:259 says
  filler-explainer-from-chat is also a background task, but the CODE gives it
  `max_steps=1` and the backend's rule is `max_steps > 1`, so it is excluded;
  per CLAUDE.md the code wins, and changing that is a backend decision.
- `d`/`esc` on a card return to the tray rather than closing outright. F09's
  Done-when #8 ("discarding a card leaves the task result retrievable from the
  tray") is unchanged and is now directly observable instead of needing an F1
  round trip, so the SPEC needed no edit -- only this gate's wording did.

**Record:**
```
Write design/completed/frontend/F09a-task-tray-cards.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row
to design/completed/frontend/INDEX.md's status table, marked SPLIT --
not complete -- and naming F9b/F9c as the remainder.

Record the four blockers with their file:line evidence, so Session 18b
and Session 23b do not have to re-derive them.

Record exactly how a finished task's result text is retrieved -- the
event, the payload field, and how task_id maps to messages -- because
F9b and F15 both read the same path.

Record the result card's plain-text rendering as an explicit FORWARD
POINTER TO F16, not an open gap: syntax colouring and a box frame for
code blocks are deferred there by decision (F16-polish.md now has the
item and the two packaging options). Indentation is preserved; colour
and framing are not.
```

**Then:** commit · `/clear`

---

# SESSION 15b — B19 Silence-Based Agent Timeout

**Goal:** the backend's timeout stops killing healthy generations, without reopening the runaway-spiral risk B11 built the check to catch.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B19-silence-based-timeout.md,
plus F5's and F7's completed docs for the exact trace and line numbers
that found this bug.

Silence-based, resetting on every activity signal, with a first-token
grace. Do NOT remove the per-chunk check outright, and do NOT touch the
frontend -- chat/stream.go's watchdog is already correct.

Explicitly verify B11's original runaway-spiral test (--max-seconds 8)
still terminates after your change. State in the completed doc which
mechanism still catches a runaway spiral if the pure silence reset
alone would not.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code.
```

**Record:**
```
Write design/completed/backend/B19-silence-based-timeout.md following
the format in design/completed/backend/INDEX.md. Include the bugs we
hit and how they were fixed, and what the next episode inherits. Add a
row to design/completed/backend/INDEX.md's status table.

Explicitly confirm and record whether B11's runaway-spiral defense
still holds, and through which mechanism.
```

**Then:** commit · `/clear`

---

# SESSION 15c — B25 Persist a Validated Provider Key

**Goal:** a key the user enters, sees validated, and moves on from is still
there the next time the backend starts.

**Why this episode exists.** Session 16 (F10) was started against its full
spec and stopped before writing any frontend code, per CLAUDE.md's "stop and
tell me" rule. F10's Build section says "the backend writes
`%APPDATA%\Point\credentials.toml`... the UI never touches the file" as
settled fact, but `appconfig.save_api_key()` — the exact writer described —
has no caller anywhere in the backend; its own docstring says so. Full trace
in `design/tasks/backend/B25-provider-key-persistence.md`'s "Why this episode
exists".

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B25-provider-key-persistence.md.

One call site: wire _handle_set_provider_api_key to validate first (the
same list_models()/health() shape it already partly does), then call
appconfig.save_api_key(), then register the instance as today. Do NOT
change SetProviderApiKey's or ProviderApiKeySet's payload shape, and do
NOT add a rollback path for a key that failed validation -- validating
before writing makes one unnecessary.

Stop at the verification gate with PowerShell checks. The one that
matters is #4: restart the backend process (not just re-ask it) and
confirm the key resolves from credentials.toml alone, with no
SetProviderApiKey call in the new process.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the masking check — grep the real key string against the log
file after the call and confirm it never appears unmasked, and the restart
check above. Both are the actual point of this episode; a diff that "looks
right" is not evidence either one holds.

**Record:**
```
Write design/completed/backend/B25-provider-key-persistence.md following
the format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row to
that INDEX.md's status table.

Confirm explicitly, with the actual command output: a wrong key never
touches credentials.toml, a right key survives a real process restart,
and the raw key never appears in logs. F10 resumes immediately after
this, and its credential-entry Done-when items are only honest once
these three are true.
```

**Then:** commit · `/clear`

---

# SESSION 16 — F10 Provider Chooser, Credentials, Providers+Appearance (F10, narrowed)

**F10 is SPLIT, not closed by this session.** It was started against its
full spec — provider chooser, credential entry, the no-provider state, a
5-tab Settings popup, and demo mode with a fixture library — and stopped
before writing any code, per its own "stop and tell me if ambiguous"
instruction. Three of its assumptions do not hold against this repo today;
the evidence is in `design/tasks/frontend/INDEX.md`'s F10 split note, the
short version:

1. **Credential persistence was a stated fact, not a built path.**
   Session 15c (B25) closes this immediately before this session runs.
2. **`point doctor` has no wire event** — it is a separate CLI subcommand
   that prints straight to stdout, not reachable from a running app
   instance. Deferred to **F10b**.
3. **The Behaviour tab's settings have no live-override path on the
   backend** — they are real `config.toml`-sourced values with no runtime
   override layer and no wire event to push one. A Behaviour tab as specced
   would be cosmetic only. Deferred to **F10b**.
4. **Demo mode assumes an apply path (F13/F9b) and a provider type that do
   not exist**, plus a shipped sample project and fixture library that is
   backend-and-asset work, not a Go frontend change. Deferred to **F10c**,
   behind a backend episode that is not yet scoped.

**This session builds the provider chooser, credential entry and
validation, the no-provider state, and the Providers + Appearance tabs of
Settings only** — everything the wire protocol already supports end to end
now that B25 has landed.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F10-settings-first-run.md, plus
design/completed/backend/B25-provider-key-persistence.md for what just
landed under credential entry.

Build ONLY: the provider chooser (first run), credential entry and
validation (now genuinely durable -- B25 landed), the no-provider state,
and the Providers + Appearance tabs of the Settings popup.

Do NOT build: the point-doctor action in About, any live effect for the
Behaviour tab's settings, or demo mode. If you render a Behaviour tab at
all, it must read and display config.toml's current values PLAINLY AS
READ-ONLY -- say explicitly that changing them means editing config.toml
and restarting, the same honest way F6 used dim "--" placeholders for a
stub rather than a live-looking control that silently does nothing. Do
NOT build an About tab control that calls `point doctor` -- there is no
wire event for it yet.

Ollama is detected silently but selected explicitly. Point must never
rewrite config.toml.

THE FRESH-MACHINE SIMULATION IS THE DANGEROUS CHECK IN THIS EPISODE.
CLAUDE.md's rename-aside rule names an F10 attempt by number: a
`Rename-Item ... -ErrorAction SilentlyContinue` silently failed to move
the real profile aside, and the script's unconditional "restore" step
then deleted the live profile by name. F10's own task doc has since
been corrected to the env-var scratch pattern -- use it exactly as
written there, and do not reconstruct a rename-aside from any other
doc. B06's and B08's task docs and B20's completed doc still SHOW the
old pattern as a historical record; they carry superseded banners, and
copying from them is the specific mistake the banner exists to stop.

F7's shared Popup gained two things in F9a that a settings UI will
want: PopupConfig.Decor (per-row icon/note computed AT RENDER TIME,
never baked into the item -- F9a shipped a frozen clock by baking it)
and Popup.RefreshItems (replace items while preserving the typed
filter and the selection). Read RowDecor's doc before writing a row
renderer.

Stop at the verification gate with PowerShell checks, including the
fresh-machine simulation.

Say explicitly at the end which original Done-when items remain
deferred and why -- the doctor action, Behaviour's real effect, and
demo mode -- each blocked on a backend episode that does not exist yet.

If anything in the narrowed scope is still ambiguous, stop and tell me
before writing code.
```

**You do:** the config.toml byte-identical check. If Point rewrote it, stop.

**Record:**
```
Write design/completed/frontend/F10-settings-first-run.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table, marked SPLIT -- not
complete -- and naming F10b/F10c as the remainder.

Also record the Settings popup's tab structure and how a new tab gets
registered. F11 adds a Memory tab to this same popup later, and F10b
adds real behaviour to the Behaviour tab without touching this session's
code -- write down what makes both true.

Record the three deferred items with their file:line evidence, so
whichever session scopes the backend work behind F10b/F10c does not have
to re-derive any of it.
```

**Then:** commit · `/clear`

---

# SESSION 16a — B26 Behaviour Overrides + `doctor` on the Wire

**Goal:** the Behaviour tab's settings get an in-memory override layer the
scheduler and two managers can consult ahead of `config.toml`, and `point
doctor`'s report becomes reachable from a running app instance instead of
only a separate process's stdout.

**Why this episode exists.** Session 16 (F10) was narrowed before this
session ran, on the grounds that the Behaviour tab's live-override path and
the About tab's `doctor` action both assumed backend surface this repo does
not have — `chat.queue_depth`, `limits.*.max_concurrent_tasks`,
`project.allow_outside_project`, `index.respect_gitignore`, and
`research.stale_after_days` are all read with `appconfig.config_value()`,
which only ever looks at `config.toml`; `point doctor` is a synchronous CLI
subcommand (`cli.py:1197`) that prints straight to stdout, with no function
anywhere that returns its report as data. Full trace in
`design/tasks/backend/B26-behaviour-overrides-doctor-wire.md`'s "Why this
episode exists".

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B26-behaviour-overrides-doctor-wire.md.

Two things, nothing beyond them: (1) appconfig_overrides.py's in-memory
layer plus SetBehaviourOverride/BehaviourOverrideSet, wired into the seven
named config_value() call sites via config_value_live(); (2)
build_doctor_report() extracted from cmd_doctor, plus
GetDoctorReport/DoctorReport reusing it verbatim.

Read the "Correction before you start" section before touching cli.py --
_ollama_report()'s asyncio.run() will raise if called from inside the
wire handler's already-running event loop. Extract the coroutine as the
doc describes; do not rewrite the probe logic.

Do NOT add a scrollback or idle-unload-timeout key -- neither has a
backend concept to override yet (idle-unload belongs to the still-unbuilt
B15). Do NOT widen the override layer beyond the named ALLOWED_KEYS.

Stop at the verification gate with PowerShell checks. Done-when #4 (doctor's
printed output byte-identical to before this episode) must be diffed, not
eyeballed.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** diff `cli.py doctor`'s output against a pre-episode capture
(Done-when #4), and confirm `GetDoctorReport` does not raise the
running-loop `RuntimeError` (Done-when #6) — both are the actual point of
this episode; a diff that "looks right" is not evidence either one holds.

**Record:**
```
Write design/completed/backend/B26-behaviour-overrides-doctor-wire.md
following the format in design/completed/backend/INDEX.md. Include the
bugs we hit and how they were fixed, and what the next episode inherits.
Add a row to that INDEX.md's status table.

Confirm explicitly, with actual command output: an override changes real
scheduler/guard behaviour, an unlisted key is rejected, an override does
not survive a process restart, and cli.py doctor's non-wire output is
unchanged. F10b resumes immediately after this, scoped to wiring its
Behaviour tab and About card to these two events.
```

**Then:** commit · `/clear`

---

# SESSION 16c — F10b Settings: Behaviour Overrides + `doctor` in About

**Goal:** the Behaviour tab stops being a read-only mirror of `config.toml`
and the About tab's `point doctor` action has something to call.

**Prerequisites:** Session 16a (B26) — `SetBehaviourOverride`/
`BehaviourOverrideSet` and `GetDoctorReport`/`DoctorReport` — must have
landed. See `design/completed/backend/B26-behaviour-overrides-doctor-wire.md`
for the events' exact shapes once that session records it.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F10-settings-first-run.md's
"Behaviour" and "About" bullets under Settings popup (UI-03), and
design/completed/backend/B26-behaviour-overrides-doctor-wire.md.

FIRST, before any code: write design/tasks/frontend/F10b-settings-
behaviour-doctor.md, in the same shape as F9a/B23's task docs. Scope,
and nothing beyond it: wiring the Behaviour tab's controls to the
backend's runtime-override event, and a doctor action in About that
renders the backend's report in a scrollable card.

Do NOT redesign the Settings popup's tab structure or touch the
Providers/Appearance tabs F10 already shipped.

Then show me the task doc and stop before writing code.
```

**Then:** commit · `/clear` once built and verified, following that doc's
own Record step.

---

# SESSION 16f — B28 Demo Mode: Provider, Fixture Library, Sample Project

**Goal:** a new, always-selectable `demo` provider, a shipped fixture
library covering every row of F10's fixture-coverage table, and a shipped
sample project for the fixtures to act on — the backend-and-asset half of
Demo mode, so Session 16d (F10c) has a real provider and real fixtures to
wire into the UI instead of a placeholder.

**Why this episode exists, and why it's scoped the way it is.** Session
16d, as originally written, found Demo mode had no backend to anchor
to at all — not B15, not `MockProvider` (test-only, must stay invisible in
the UI). Checked live before writing the task doc: F9b and F13 have **not**
landed (no completed docs past F10b), and neither has B24 — grepping
`OutgoingEventType` turns up no `FillProposal` member and `cli.py` has no
`--json-proposal` flag. That last fact is the one the original framing
didn't anticipate: an applicable diff isn't just blocked by F9b/F13's
missing Editor, it's blocked by a backend event (B24) that doesn't exist
either. The task doc narrows `@filler-explainer`'s FILL-mode fixture to
what today's real backend actually does on a fill turn — a plausible
JSON-shaped text reply, no `FillProposal` event, no apply path — because
replaying a capability Point doesn't have yet would be exactly the
theater D55 exists to rule out. Full account in
`design/tasks/backend/B28-demo-mode-provider-fixtures.md`'s "Why this
episode exists".

**Paste:**
```
Read CLAUDE.md and
design/tasks/backend/B28-demo-mode-provider-fixtures.md in full.

Build exactly as specced, with two additions:

1. Done-when #5's "exactly 2 agent_proposals rows" must come from
   actually running _maybe_propose's real code path -- read it first
   (executor.py:913-972) to learn whether it issues its own provider
   call or parses the final reply directly, and give research_tool_
   loop.json whatever that requires. Do NOT insert rows directly into
   the database to force the count. If the real mechanism produces a
   different number than 2, say so and adjust Done-when #5 to match
   reality rather than engineering around it.

2. Confirm cancellation.json's chunk pacing produces a sensible
   partial sentence at ANY reasonable cancellation timing, not only
   at one precisely-tuned moment -- a real demo presenter cancels
   whenever they choose, not at a scripted instant.

Otherwise build exactly as specced -- the new DemoProvider (selectable,
unconditionally registered, structurally separate from MockProvider),
the fixture library under backend\demo\fixtures\, the shipped sample
project under backend\demo\sample_project\, and the web_search guard
at fixture-load time.

Do NOT touch providers\mock\ or backend\tests\fixtures\. Do NOT add a
FillProposal event, any apply/diff wire surface, or a task.queued
event -- none of those are this episode's to build (see the task
doc's Do NOT section).

Stop at the verification gate, with real evidence for all ten
Done-when items -- especially #5 (state the _maybe_propose answer
plainly, not just encoded in the fixture), #6 (prove the web_search
guard by deliberately violating it once, then removing the
violation), and #10 (git diff confirming providers\mock\ and
backend\tests\fixtures\ are genuinely untouched). Record the exact
commands used for checks 3 and 4 in the completed doc, as the task
doc's own verification gate asks.

If anything in the Build section is ambiguous or seems to assume
something not true of this repo, stop and tell me before writing
more code.
```

**You do:** read the `_maybe_propose` finding yourself before accepting
Done-when #5 — confirm the stated mechanism against `executor.py:913-972`
directly, don't take the session's summary on faith. Also eyeball the git
diff for Done-when #10 and the web-search-guard proof for #6; both are
one-line-of-evidence checks that are cheap to verify and expensive to get
wrong (a quiet `web_search` fixture call, or a Mock-provider edit, both
only surface later).

**Record:**
```
Write design/completed/backend/B28-demo-mode-provider-fixtures.md
following the format in design/completed/backend/INDEX.md. Include the
bugs we hit and how they were fixed, and what the next episode (Session
16d, F10c) inherits. Add a row to that INDEX.md's status table.

State plainly, in its own paragraph: what _maybe_propose actually does
(second provider call, or direct parse of the final reply), the exact
resulting agent_proposals count if it differs from 2, and the exact
commands used for the queued-turn (Done-when #3) and cancellation
(Done-when #4) checks.
```

**Then:** commit · `/clear`

---

# SESSION 16d — F10c Settings: Demo Mode

**Goal:** a presentation with dead wifi and no local model still has
something real to show, end to end — chat, queueing, cancellation, a
research run, the filler-explainer, and an error case, all on recorded
fixtures with plausible timing.

**Prerequisites:** Session 16f (B28) must have landed —
`design/completed/backend/B28-demo-mode-provider-fixtures.md` is this
session's source for the provider id, the fixture set, and the shipped
sample project's path.

**The filler-explainer fixture ships incomplete by design, not by
accident.** B28 narrowed it to a plain JSON-shaped text reply with no
`FillProposal` event, because B24/F9b/F13 all remain unbuilt — read B28's
"Why this episode exists" before assuming this session can render a diff
card for it. This session renders whatever B28's fixture actually produces
(a plain reply), nothing more.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F10-settings-first-run.md's "Demo
mode" section in full, and
design/completed/backend/B28-demo-mode-provider-fixtures.md.

FIRST, before any code: write design/tasks/frontend/F10c-settings-demo-
mode.md, in the same shape as F9a/B23's task docs. Scope, and nothing
beyond it: selecting Demo mode from the provider chooser/Settings, the
persistent DEMO badge, and wiring each fixture B28 shipped to its correct
UI surface.

B24/F9b/F13 are still unbuilt. The filler-explainer fixture is a plain
text reply, not a parsed proposal -- do not build a diff-card rendering
for it; render it exactly like the chat-reply and explain-mode fixtures.

Then show me the task doc and stop before writing code.
```

**Then:** commit · `/clear` once built and verified, following that doc's
own Record step.

---

# SESSION 16e — B27 Doctor Resolved Behaviour Values

**Goal:** `GetDoctorReport` gains one new field — `behaviour_resolved` —
so a human or a future frontend pass can see what each Behaviour key's
`config.toml`/fallback value actually is, independent of any override.

**Why this episode exists.** Found live during F10b's own verification
(session 16c): the Behaviour tab's rows read `default`/`overridden → X`
and can never show what `default` numerically means, because Go's
`config.Load()` parses only the `[ui]` table and B26 deliberately did not
add a `GetBehaviourOverrides` pull event (two ways to learn the same fact
disagree eventually). The task doc already exists — this session reads
it rather than writing it.

**Paste:**
```
Read CLAUDE.md and
design/tasks/backend/B27-doctor-resolved-behaviour-values.md.

One new field only: RESOLVED_DEFAULTS + resolved_behaviour_settings() in
appconfig_overrides.py, and one new line inside build_doctor_report()'s
returned dict in cli.py. No new wire event, no CLI-output change
(_print_doctor_report stays untouched), no per-provider cap family, no
frontend code.

The one check that actually proves this is not config_value_live()
wearing a new name: with an override active for a key, `resolved` must
still show config.toml's/fallback's value, never the override's own
value.

Stop at the verification gate with PowerShell checks. Done-when #5
(cli.py doctor's printed output byte-identical to before this episode)
must be diffed, not eyeballed.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B27-doctor-resolved-behaviour-values.md
following the format in design/completed/backend/INDEX.md. Include the
bugs we hit and how they were fixed, and what the next episode inherits.
Add a row to that INDEX.md's status table.

Confirm explicitly, with actual command output: an override active for a
key does NOT change behaviour_resolved's own `resolved` value for it, a
key with no config.toml value and no override reports its hardcoded
fallback, and cli.py doctor's non-wire output is unchanged.
```

**Then:** commit · `/clear`

---

# SESSION 16b — F18 Windowed History

**Goal:** history obeys the same bounded-memory rule as workspace unload —
flat memory regardless of session size and scroll depth, so a low-end device
running a local model never competes with held scrollback.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F18-windowed-history.md.

Bounded contiguous resident window, keyset on seq, memory flat regardless
of session size AND scroll depth. Never background-fetch the whole session.
Defer history fetches while a task streams. The Suspend anchor is
{anchorSeq, rowOffset, pinnedToBottom} and Resume degrades to bottom if the
anchor is gone.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the memory check is the whole point — run with POINT_MEMLOG, seed
50k messages, scroll bottom to top and back, and confirm heap_alloc trends flat
with no climb. A climb means eviction isn't working; treat it as a hard stop.

**Record:**
```
Write design/completed/frontend/F18-windowed-history.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Record the Suspend-blob anchor shape exactly as implemented
({anchorSeq, rowOffset, pinnedToBottom} or whatever you land on), the
resume-degrades-to-bottom rule, and the pinned-to-bottom rule. F12's real
workspace-state type must adopt this shape method-for-method, not
approximate it, and must preserve both behaviours rather than re-deriving
them.
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

F9a's task tray is now the surface that PROVES the unload rule, and it
is the easiest way to run this episode's own demo beat: unload a
workspace with @research running, press F1, and the task is still
there and still opens its result. Use it.

But the tray is ROOT's, not a workspace's -- deliberately, because the
task list is global and a task whose originating view is unloaded must
still appear. So nothing about it belongs in Workspace.Suspend():
not the open/closed state, not the filter text, and NOT Root's
backgroundSeen map, which records TaskStarted.background flags seen in
THIS process and is meaningless once reloaded. Persisting any of it
would be persisting a view of state the DB already holds, which is the
thing Suspend's ~2KB budget exists to avoid.

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

# SESSION 17b — B24 The Filler's Proposal on the Wire

**Goal:** make "the backend returns a rewritten region" true. B11 asks the
Filler-Explainer for `{"file","start_line","end_line","replacement","note"}`
and then throws the structure away. Parse it, validate it against the region
the model was actually shown, and put it on the wire as its own event.

**Why this episode exists.** F9 (Session 15) was narrowed and stopped before
writing any diff code. One of its four blockers is that there is nothing valid
to compute a diff from: the shape is stated only inside a prompt string
(`managers\context.py:686`), nothing parses it, and `TaskDone` carries an
empty payload. Letting Go parse raw model output instead would be the first
exception to the backend-resolves / frontend-renders rule that
`parse_invocation`, B20 and B23 all established — and the validation that
matters most (is this line range inside what the model was shown?) is not even
answerable in Go, because the frontend never learns what the shown region was.
Full trace in `design/tasks/backend/B24-filler-proposal-event.md`'s "Why this
episode exists".

**Placed here, before F13**, so that F9's diff-card session (18b) has both its
dependencies — this event and the Editor — the moment it starts. B24 itself
has no frontend dependency and could run earlier; it is next to its consumer
on purpose.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B24-filler-proposal-event.md.

One parse, one validation set, one envelope type, one push site. The
proposal is pushed AFTER the terminal TaskDone, never instead of it --
B6's one-terminal-event-per-task rule holds.

A reply that does not parse is a turn with no proposal: no event, no
Error, a normal reply, one WARNING log line. The Filler is explicitly
allowed to refuse in plain text, and B11's one-file refusal takes the
echo path -- so a refusal surfacing as an error would make correct
behaviour look broken.

Do NOT write diff logic, an apply path, any UI, or a disk-write event.
Do NOT persist the proposal. Do NOT change the fill-mode prompt.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** confirm the `FillProposal` envelope's `seq` is **higher** than the
`TaskDone`'s for the same task — read the numbers, do not take the ordering on
trust. Then run the same fill request five times and count how many replies
parsed.

**Record:**
```
Write design/completed/backend/B24-filler-proposal-event.md following
the format in design/completed/backend/INDEX.md. Include the bugs we
hit and how they were fixed, and what the next episode inherits. Add a
row to that INDEX.md's status table.

Record the exact FillProposal payload shape -- every field name,
including shown_start/shown_end -- and the full list of rejection
reason slugs. Session 18b is written against these exact names.

Record the MEASURED compliance rate out of five runs, and the model it
was measured against. That number is unrecoverable after this session
and it is what tells a later episode whether the prompt needs work.
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

SETTLE THIS BEFORE WRITING CODE: there is no way to save a file today,
on either side. The backend has no write event at all -- no
`WriteProjectFile`, no counterpart to `ReadProjectFile` (confirmed
during F9a). The frontend writes exactly one file in the whole repo,
`state.json`, via config\state.go. The only other writes anywhere are
`project init`'s own `.point\project.toml` and `.gitignore`.

So "save" is an unbuilt path and WHICH SIDE OWNS IT is a real
decision, not plumbing. It interacts with three things that are
backend-side today: B7's path guard, the file locks
(AcquireFileLock/HeartbeatFileLock/ReleaseFileLock/TakeOverFileLock),
and CheckFileStale -- and F13's own spec leans on all three ("never
autosave a file held by another lock holder", "record Point's own
write mtimes", "changed on disk since you opened it"). A Go-side write
would have to re-ask questions the backend already answers, which is
the drift B12's bug #2 and B23's ListDirectory decision were both
about. Note also that CLAUDE.md's first hard rule bans writes from an
AGENT path specifically; a user saving their own buffer is not that,
so either answer is permitted -- but pick one deliberately and say
why.

Raise it and stop if the episode doc assumes a save path exists.

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

# SESSION 18b — F9b Diff Card (F9 resumed)

**Goal:** the second third of F9. Every agent-proposed change is reviewed,
editable, and reversible before it touches a buffer.

**Prerequisites, all three mandatory:** F13's editor buffer (Session 18) to
apply into, B24's `FillProposal` event (Session 17b) to compute a diff from,
and `CheckFileStale` for the refusal. If any is not landed, this session does
not start.

**Runs before Session 19 (F15)**, which reuses this card's diff mechanism for
the Filler's inline diff and needs a real function to call.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F09-task-tray-cards.md's "Diff
card (UI-07)" section, design/completed/backend/B24-filler-proposal-event.md
for the event shape, and design/completed/frontend/F09a-task-tray-cards.md
for what the narrowed F9 already built.

Build the diff card only: Done-when #5, #6, #7 of F09's original spec.
Attach-from-a-card and save-to-memory stay deferred to Session 23b.

Go computes the diff from B24's validated proposal -- never from raw
model text, and never by asking for a unified diff. `e` opens the
proposed RESULT in a scratch buffer, not the patch; the diff re-renders
live against the original as you edit.

Three things the spec does not say and that this episode must get right
-- all three are lessons B12 and F8 already paid for:

1. APPLY-TIME GUARD ORDER. Resolve the SESSION's scope first, guard
   against that, and commit nothing until the guard passes. Do not use
   a path-resolved project: `_resolve_optional_project` walks up from
   the path to auto-discover one (runtime.py:752), which answers a
   different question from `_attachment_scope`'s "what will the builder
   actually use". That drift is B12's bug #2 -- a file priced at ~718
   tokens under one scope and refused under the other -- and B23
   explicitly declined to inherit it for ListDirectory. For an estimate
   the drift costs a wrong number; for a destructive apply it means the
   write is guarded against whatever `.point\` sits above the file
   instead of the session's real root. 374baa2 established the
   corrected order for AttachContext; follow that one, not the
   original. You may need CheckFileStale to accept a session_id.

2. STALENESS IS A FACT ABOUT A MOMENT. Record the file's (mtime, size)
   as of region generation, re-stat at apply time, compare. Do not
   cache a stat and trust it: CheckFileStale takes a caller-supplied
   mtime and will confirm whatever it is handed. B12's set_mode CLEARS
   its recorded (mtime, size) pair for exactly this reason, and F8's
   chip staleness was the same mistake one layer up.

3. THE CARD'S STATE TRANSITION FOLLOWS THE BUFFER EDIT. Mark the card
   applied only AFTER the buffer edit returns. Marking it optimistically
   reproduces F8's chip bug precisely: a card that looks applied when it
   was not. Since every apply goes into an in-memory buffer and never to
   disk, a failed apply is atomic by construction -- but only if the
   ordering is this way round.

Never a direct disk write from an agent path, in any phase. There is no
WriteProjectFile event and this episode does not add one.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** three things, in this order.
1. `e`, change a line, `⏎` — confirm the **edited** text landed, not the
   original. Then `ctrl+z` once and confirm it fully undid.
2. With a card open, modify the file on disk from a second tab, then `⏎` —
   confirm it is **refused with a reason** and offers re-generate.
3. Apply, then check the card's own label. It must not read "applied" if the
   buffer edit did not happen. Force this by making the apply fail (open the
   file read-only, or point the card at a path the guard refuses) and watch
   the card, not the log.

**Record:**
```
Write design/completed/frontend/F09b-diff-card.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed. Add a row to design/completed/frontend/INDEX.md's
status table and update F9a's row to say F9c is the only part still
open.

Record exactly how the diff is computed and applied -- the function
signature and what it takes as input, not just the UI behaviour. F15
(Session 19) reuses this precise mechanism for the Filler's inline
diff, so it needs a real function to call, not a description to
re-implement.

Record what state the card tracks between being shown and being
applied, which of it is read fresh at apply time, and what the card
shows after a failed apply. That is the part most likely to rot.
```

**Then:** commit · `/clear`

---

# SESSION 19 — F15 Filler-Explainer Integration

**Goal:** v1 feature-complete.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F15-explainer-integration.md,
design/completed/backend/B24-filler-proposal-event.md for the parsed
proposal's exact payload shape, and
design/completed/frontend/F09b-diff-card.md for the diff function this
episode CALLS rather than re-implements.

Explanations render in an overlay panel inside the Editor. Fill produces a
diff card; applying goes into the UNSAVED buffer so Ctrl+Z undoes it.
Nothing writes to disk.

F9 was SPLIT during its own session; this episode's dependency is F9b
(the diff card, Session 18b), not "F9". The fill reply is no longer raw
model text either: B24 (Session 17b) parses and VALIDATES it in the
backend into a structured proposal with the shown region attached, and
F9b computes the diff from that. Do not parse the model's JSON here,
and do not write a second diff implementation -- F9b's completed doc
records the function signature precisely so this episode has something
to call.

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

Two states exist now that did not when this was written. B12 added
TaskState.QUEUED and runtime\scheduler.py: a QUEUED task holds no
provider connection and no stream, is visible as queued, and is
cancellable there -- so "refuses to free anything a running task
holds" has to distinguish QUEUED from RUNNING rather than treat
is_active() as one thing. B22 added SerializedConnection for the
concurrent-completion race; anything this episode does to teardown has
to go through it rather than around it.

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

`usage_records` already EXISTS (schema v2) and is already written per
turn. What it does not have is a repository or any wire event, which
is this episode's actual gap -- not the table. Check what is in it
before designing anything around it.

There is also already a CONSUMER of per-turn token counts in the UI:
F9a's result card sums `messages.input_tokens/output_tokens/
total_tokens` to show "12.4k tok" on a finished task. If this episode
introduces a second arithmetic for the same number, the two will
disagree and the UI is where that gets discovered -- B20's rule, for
B20's reason. Either the card reads this episode's number, or this
episode's number is derived from the same columns; decide which and
say so.

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

Two tea.Tick loops are live by the time this episode runs: the header
animation (F1) and F9a's tray clock. Both are generation-counted and
self-terminating -- they re-arm only while there is a reason to -- so
check what they do across an ExecProcess suspend and resume rather
than assuming. F9a's tray is also the easiest proof of the
"backend tasks keep running while the TUI is suspended" claim: leave
@research running, suspend, come back, press F1.

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
Read CLAUDE.md, design/tasks/backend/B13-memory-system.md, and
design/completed/backend/B11-three-agents.md's section on the
`agent_proposals` stopgap.

THREE THINGS SETTLED SINCE THIS WAS WRITTEN.

1. `agent_proposals` (migration v4) exists and is POPULATED -- B11's
   Research agent writes a row per candidate fact, status `pending`,
   capped per session by a count. B11's own files say this episode
   "reads B11's completed doc and decides how its real MemoryRecord
   type adapts to what already exists here -- it does not get to assume
   this table was never populated." Decide migrate / ignore / drop
   deliberately and write down which. `memory_records` and
   `memory_scopes` have existed unused since schema v2 and are this
   episode's to activate.

2. F9's result card needs TWO things from here, and only one of them is
   this episode: a memory write path producing status=accepted,
   source=user_card (B13's), AND the ability to ATTACH a memory or a
   task result, which `context_attachments` cannot express --
   source_type is `file`-only and managers\context.py refuses anything
   else outright. That attachment-source extension IS NOT YET AN
   EPISODE, deliberately, because B13's own shape decides what a memory
   attachment even is. Spec it at the end of this session; F9c
   (Session 23b) is blocked on both.

3. F11 is the next Popup consumer whose rows CHANGE while it is open.
   Use PopupConfig.Decor for anything state-derived (computed at render
   time) and Popup.RefreshItems to replace items without wiping the
   typed filter or the selection. F9a shipped a clock frozen at 0:00 by
   baking a render-time value into an array.Item; RowDecor's doc
   records the whole trace.

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

# SESSION 23b — F9c Result Card Attach and Save (F9 closed)

**Goal:** the last third of F9. Attach and save become independently
exercisable on a result card, which is what finally closes the episode.

**Prerequisites.** Two, and one of them is **not yet an episode**:

1. **B13's memory write path** (Session 23). `memory_records` has had the
   right `status`/`source` columns since migration v2 and nothing has ever
   written them. Saving from a card must produce `status=accepted`,
   `source=user_card` — the click *is* the approval, not a second pending
   proposal — and that needs `MemoryService` plus a `SaveMemory` event.
2. **A B12 attachment-source extension that does not exist.** Today
   `context_attachments.source_type` only ever holds `file`, and
   `managers\context.py:564` refuses anything else with
   `"unsupported source_type (B12)"`. Attaching a task result or a memory
   needs a new source type, a renderer for it, and a decision about what the
   guards mean for a source that is not a path. **Spec it when Session 23
   lands**, not before — B13's own shape determines what a memory attachment
   even is, and writing it earlier would spec it against a guess. Do not fold
   it into B13: one is "memory exists", the other is "the context builder can
   render something that is not a file".

**Also settle the mode-name discrepancy before writing code.** F09's spec
offers `a attach full` / `s attach summary`, but the three modes are
`full | reference | detached` and its own gate expects `reference`. Either
`summary` is a third real mode the builder must implement, or the key is
mislabelled and means `reference`. Decide it deliberately and write down
which, in the episode's own words.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F09-task-tray-cards.md's "Result
card (UI-06)" section, design/completed/frontend/F09a-task-tray-cards.md,
design/completed/backend/B13-memory-system.md, and the completed doc for
the attachment-source extension this depends on.

Build the result card's attach and save axes only: Done-when #2, #3, #4
of F09's original spec. The tray, the card's rendering and discard are
already built (F9a); the diff card is already built (F9b).

Attach and save are INDEPENDENT axes. Attach controls this
conversation's context; save controls durable memory. Both, either, or
neither. Attaching writes a reference, never a copy -- so editing the
memory later must change what the attached reference renders, and that
is the check that proves it.

Saving from a card creates an ACCEPTED record (source=user_card). The
click is the approval. It is not a second pending proposal.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** on one card, attach-summary **and** save-to-project, then verify
both in the DB:
```powershell
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select mode from context_attachments order by rowid desc limit 1;"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select status, source from memory_records order by rowid desc limit 1;"
```
Then edit that memory and confirm what the attached reference renders
**changes** — that is Done-when #3 and the only check that distinguishes a
reference from a copy.

**Record:**
```
Write design/completed/frontend/F09c-result-card-axes.md following the
format in design/completed/backend/INDEX.md. Add a row to
design/completed/frontend/INDEX.md's status table, and update F9a's and
F9b's rows -- F9 is CLOSED only once this lands, across all three docs.

Record which way the summary-vs-reference mode question was decided and
why, and the exact source_type the attachment extension uses for a task
result.
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

"Shared database" across concurrent runtimes is no longer unexplored
ground: B22 found and fixed a real SQLite commit race under concurrent
task completion and introduced SerializedConnection for it. Read
design/completed/backend/B22-concurrent-completion-race.md before
designing the per-connection runtime -- it is the episode that already
paid for this, and multiplying runtimes is exactly the condition that
made it visible.

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

F16-polish.md now carries a CONCRETE carried-forward item from F9a:
the result card's code blocks have no syntax colour and no box frame,
because `chat\markdown.go` is unreachable from `app` (chat imports
app). Indentation IS preserved. The section names the two packaging
options and recommends extracting the renderer into
`internal\markdown\`. This was deferred here by decision, not left
open.

Two things the responsive audit has to cover that did not exist
before. F9a's tray and result card size themselves to the TRANSCRIPT
PANE's rect (layout.Main) less a cell per side, centred in the
terminal -- so they change shape with the right pane's visibility as
well as with the terminal, and both need checking at every size. And
the task-row glyph vocabulary (glyph, colour role, grouping, label,
durations) now has exactly ONE copy, in app\tasks.go, read by both
the tray and the right-pane Task card: a glyph that degrades in
Windows Terminal is now one fix, and the theme QA should confirm the
two surfaces still agree.

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

## Seven rules that keep this from degrading

1. Never let Claude Code mark something complete. Only you, after running checks.
2. Never skip the verification gate, even when the change looks trivial.
3. Never let it build a parked item because it seems adjacent.
4. When code and docs disagree, stop. Neither side gets silently "fixed".
5. `/clear` between episodes. Every time.
6. Commit after each episode, with the episode id in the message.
7. **When two reproductions of a bug both fail, stop theorising and add a log
   line.** F6 spent four code-level reproduction attempts and three rounds of
   instrumentation on one field report. Every hypothesis was traced carefully
   and every one was wrong — the cause was in a third component neither of us
   had looked at. What settled it was one debug line carrying the ids and the
   head and tail of what a task actually accumulated; it eliminated every
   remaining theory at once. A test that passes tells you where the bug is
   NOT, which is progress, but it is slow progress. Prefer an instrument that
   can see the real run.

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
