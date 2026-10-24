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

### Splitting an episode

**An episode that turns out to need more than one build session splits into
lettered sub-episodes — `F13a`, `F13b` — and each half gets its own doc.**

The rule the split exists to protect: `completed/` is the only place
completion is claimed, so a doc must be entirely true or entirely absent.
One shared doc for a half-built episode is half-true, which is worse than
either.

So:

1. **Pick a seam, not a stopping point.** The half that ships has to be
   complete in itself — nothing in it provisional pending the rest. F13's
   seam was the save path: the editing half is fully testable without it.
2. **Both halves get a task doc**, `tasks/frontend/Fxxa-<slug>.md` and
   `tasks/frontend/Fxxb-<slug>.md`, each carrying its requirements
   **verbatim** from the combined doc. Do not re-derive or re-word them —
   the original wording is what was reviewed. `Fxxb`'s metadata says
   **Depends on: Fxxa (done)**. Writing `Fxxa`'s after the fact feels
   redundant when the work is already done; write it anyway. **F9a is
   the counterexample this step exists for:** it split before any code
   was written, got `completed/frontend/F09a-task-tray-cards.md`, and
   never got a matching task doc — `tasks/frontend/F09-task-tray-cards.md`
   still describes all three thirds, so there is no document anywhere
   stating what F9a alone was asked to do. (B1–B5 and S12b also have
   completed docs with no task doc; those predate the `tasks/` tree and
   are not a precedent.)
3. **Write the finished half into `completed/frontend/Fxxa-<slug>.md`**,
   stating in its first section which Done-whens it covers and which it
   does not, by number.
4. **Retire the combined task doc** once both halves exist. Three
   documents describing overlapping scope is how they drift; git keeps
   the history.
5. **Both INDEXes get a row**, and the finished half is 🟡 not ✅ —
   "built and verified; the episode is NOT complete; Fxxb is the
   remainder". F9a's row is the model.
6. **The gate names what it does not cover.** `gate_f13a.ps1`'s footer
   lists the seven Done-whens that are F13b's, so a green run cannot be
   mistaken for a green episode.

Precedents: **F9 → F9a/F9b/F9c** and **F10 → F10/F10b/F10c** both split
*before* any code was written. **F13 → F13a/F13b** is the first to split
**mid-build**, which is the case this section is really for — and F13's
own task doc had sanctioned it in advance ("may take two sessions").

**Session labels and episode letters are separate namespaces, but when a
split episode can have them line up, make them line up.** `16b`–`16h`,
`17b`, `17c`, `18d` and `23b` are *interleaved sessions* — a different
episode slotted between numbered ones — not sub-episodes of 16, 17, 18
or 23. F13's split collided with one of those: F9b already held `18b`.
Rather than leave F13's halves on mismatched labels, **F9b was moved to
`18d`**, so F13a is Session 18a and F13b is Session 18b — doc letter and
session letter the same character. A label is only a position in this
file; moving one costs a few references and buys a reader never having to
remember which session letter maps to which doc letter.

**That renumbering is safe, and here is the whole dependency chain so it
does not have to be inferred:**

```
18a  F13a  editing half          needs F12, B7, B32 — all landed. DONE.
18b  F13b  save half             needs 18a only (buffer, doc, tab strip).
18d  F9b   diff card             needs 18a only — specifically
                                 doc.ApplyRegion, which 18a shipped with
                                 no caller for exactly this. Plus B24 (17b).
                                 RAN: built #5/#6, DEFERRED #7 + the
                                 packaging decision. See 18d's own note.
19a  B36   generation-time       needs B24 (17b) only. Carries (mtime,
           staleness             size) from the region-generation read and
                                 gives CheckFileStale session_id + size —
                                 what 18d found missing. RUNS BEFORE 19 so
                                 F15's EvFillProposal decoder is written
                                 once against the final payload shape.
19   F15   filler-explainer      DONE. CALLS F13a's buffer and F9b's diff
                                 function; writes nothing to disk. 18b is
                                 a SCHEDULING prerequisite only — v1 is
                                 not feature-complete while F13 is open.
                                 Also adds the FIRST caller of
                                 NewDiffCard, and owes 18d's "You do:"
                                 steps 1 and 3, which 18d could not run.
19b  F9d   apply-time guard      needs 19 (F15) — the guard F9a hazard-1
           order                 names is "not reachable" until F15 adds
                                 the first caller of WriteProjectFile from
                                 an agent-applied diff. RUNS AFTER 19, the
                                 only interleaved session in this chain
                                 that does. Uses B36's session-scoped
                                 CheckFileStale (19a), which fixed the
                                 PRIMITIVE, not the call site.
```

`18b` and `18d` are mutually independent — neither reads the other's code
— so either may run first; `18b` before `18d` is only the natural reading
order, and 19 is the first session that both must precede. Nothing that previously
depended on F9b-at-18b depends on it being *second*; it depended on it
being after the editor buffer, and 18d still is.

Note the session blocks below are in the order they were slotted, not
alphabetical — 18d sits physically between 18a and 18b, the same way
`16d`, `16g`, `16h`, `16e`, `16b` appear in that order. The label, not the
position, is what orders a session.

Refer to a split episode by its **doc name** (`F13a`, `F13b`), which is
unambiguous either way.

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

### Windows console encoding

Found during the B35 re-check (2026-10-18): a correctly-encoded em-dash in
`design/completed/frontend/F05-streaming-queue.md` rendered as `â€”` in a
PowerShell console and looked exactly like the real corruption B33/B35
fixed. It wasn't — see that doc's correction note. In order of preference:

a. **Policy, applies no matter which fix below is in place:** never trust
   `Get-Content`/`Select-String` console output to judge a file's
   encoding. Pipe to a file and inspect with `Format-Hex`, or decode the
   bytes explicitly, instead — that is what actually caught the false
   alarm above.
b. **Recommended fix:** upgrade to PowerShell 7 (`pwsh`) — it defaults to
   UTF-8 console I/O, no per-session config needed.
c. **Optional machine-wide fix:** Windows Settings → Region →
   Administrative → Change system locale → enable "Use Unicode UTF-8 for
   worldwide language support" (requires reboot).
d. **Fallback, staying on PowerShell 5.1 without the system toggle:** add
   to `$PROFILE`:
   ```powershell
   [Console]::OutputEncoding = [Text.Encoding]::UTF8
   chcp 65001
   ```

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
   constant; there is no second `Workspace` implementation. F13 (Session 18a)
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
work resumes at Session 18d.

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

Record the four blockers with their file:line evidence, so Session 18d
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

**⚠ Status: this session started, found a real binding bug live, and
now cannot CLOSE until Session 16g (B30) lands.** B28 landed and this
session's own build and most of its verification ran — Done-when #1,
#2, #5, #7-9, #11-12 of `design/tasks/frontend/F10c-settings-demo-
mode.md` are confirmed. But Done-when #3 (fresh-profile project
binding) and #4 (the retrofit-without-binding status line) do not pass
today: live testing found the task doc's own Decision 1 never actually
had a working mechanism (see that doc's own corrected note at the top
of its Decisions section, and `design/tasks/backend/B30-set-session-
project.md`'s full trace). Done-when #6 (`@research`) is deliberately
untested — it resolves tool calls against `session.project_id` with no
fallback if empty, so testing it before #3 is fixed would not prove
anything. **Resume this session only after Session 16g (B30) has landed**
— re-verify #3/#4 with the exact repro already on record (fresh
profile, first-run chooser, Demo mode, direct DB query), then #6, then
close.

Also found and already closed separately, same session, same discipline
as every other mid-episode backend need: Session 16g-adjacent
`design/tasks/backend/B29-remove-legacy-runtime-db.md`, built and
committed on its own (a stale `backend\runtime.db` was silently copied
into every fresh scratch profile, including this session's own
verification attempts, until B29 removed it).

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

# SESSION 16g — B30 Let `SetSessionProvider` Also Bind a Session's Project

**Goal:** a session can be told its own project id after it already
exists, the same way it can already be told its provider/model — one
optional field through the existing `SetSessionProvider` event, reusing
`session_manager.set_project()` (already built for B12, never exposed
until now). Unblocks Session 16d (F10c)'s own Done-when #3/#4 and its
`@research` fixture check.

**Why this episode exists.** Found live during Session 16d's (F10c)
own verification, traced with real evidence rather than reasoned about:
F10c's original design assumed a fresh session's `CreateSession` payload
could carry a human-chosen Demo project id, if `ProjectInit`'s reply
resolved it in time. It never does — `chat\providers.go`'s own comment,
written back in Session 16 (F10), already proves why: a first-run
session "was created before this choice existed," true for every
provider, because bootstrap's own `CreateSession` needs two fully
automatic round trips with zero human action, while the chooser cannot
even become clickable until the first of those same two round trips has
already landed. A human cannot win that race. The only mechanism that
can ever work is retrofitting an already-created session — exactly what
`SetSessionProvider`'s existing call already does for provider/model,
extended here to also carry `project_id`. Full trace in
`design/tasks/backend/B30-set-session-project.md`'s "Why this episode
exists" — the task doc already exists; this session reads it rather
than writing it.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B30-set-session-project.md.

One optional field through one existing event: `project_id` added to
SetSessionProvider's payload/handler (runtime.py), calling
session_manager.set_project() (already built for B12, already tested)
when present, and the reply always echoes the session's real, current
project_id -- re-read after the write, never the raw input parroted
back. No new wire event, no new reply type. The matching Go-side change
is two call sites only: applyProviderDefault (providers.go) forwards
project_id when set, onSessionProviderUpdated (protocol.go) reads it
back into w.projectID.

Do NOT add a SetSessionProject-only event, touch AttachContext/
_adoptable_project_for/_commit_adoption, build anything Demo-mode-
specific, or change any F10c frontend file beyond the two call sites
named above.

Stop at the verification gate with real evidence for all six
Done-when items -- especially #2/#3 (an existing project_id survives a
provider-only retrofit, and the reply echoes it even when the request
never sent one) and #4 (go test ./frontend/internal/chat/... passes
unmodified with the new field).

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before
writing code.
```

**You do:** confirm Done-when #2 yourself against the actual manual
script output — this is the one check that would silently pass even if
`set_project` were wrongly called unconditionally in a way that clobbers
an existing binding; read the asserted "UNCHANGED from step 1" line
directly rather than trusting the summary.

**Record:**
```
Write design/completed/backend/B30-set-session-project.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and that this unblocks Session 16d (F10c)'s own
Done-when #3/#4 and its @research fixture check. Add a row to that
INDEX.md's status table.

Confirm explicitly, with actual command output: an existing project_id
survives a provider-only retrofit, and SetSessionProvider's reply
carries the session's real project_id even on a request that never sent
one.
```

**Then:** commit · `/clear`

---

# SESSION 16h — B31 Validate `project_id` in `CreateSession`/`SetSessionProvider`

**Goal:** neither `CreateSession` nor `SetSessionProvider` (B30) has ever
checked that an incoming `project_id` matches a real `projects` row —
close that gap in both handlers, with an unknown id treated as "no
project" on `CreateSession` and "leave the existing binding alone" on
`SetSessionProvider`. Neither refuses the request outright.

**Does not block anything.** Found and queued *after* both Session 16d
(F10c) and Session 16g (B30) were already recorded complete, via a live
test run outside either episode's own verification gate. Unlike 16g, this
session does not reopen 16d or any other closed session — it is a
standalone hardening fix to a gap the earlier recording session found and
parked (`design/tasks/backend/INDEX.md`'s parking lot) rather than fixed
on the spot.

**Why this episode exists.** The repro: select Demo mode (binds a session
to the sample project), delete `runtime.db*` only — not `state.json` —
migrate, relaunch. The session list comes back empty, `onSessionList`
fires `CreateSession` with the `project_id` still stored in `state.json`,
and the backend accepted it even though the fresh database's `projects`
table was empty — no error, no validation, a session bound to nothing.
Full trace in `design/completed/frontend/F10c-settings-demo-mode.md`'s
Decision 1 correction and `design/tasks/backend/B31-validate-project-
id.md`'s own "Why this episode exists" — the task doc already exists;
this session reads it rather than writing it.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B31-validate-project-id.md.

Add ProjectManager.exists() (a pure existence check, no touch, no write
-- see the task doc's own Build §1 for why resolve() is not reused for
this), then gate _handle_create_session's and _handle_set_session_
provider's own handling of an incoming project_id on it. An unknown id
means: CreateSession creates the session with project_id NULL and logs a
warning; SetSessionProvider leaves whatever binding already existed
untouched and logs a warning. Neither refuses the request.

Do NOT touch the frontend, add a FOREIGN KEY/migration, change
SetSessionProvider's existing unconditional-overwrite behaviour for a
VALID project_id, or touch anything in either handler unrelated to
project_id.

Stop at the verification gate with real evidence for all seven
Done-when items -- especially #3/#4 (an existing SetSessionProvider
binding, bound or unbound, survives an unknown project_id arriving
alongside it) and #5 (ProjectManager.exists() causes no write on a hit
or a miss).

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B31-validate-project-id.md following the
format in design/completed/backend/INDEX.md. Add a row to that INDEX.md's
status table and to design/model_usage.md.
```

**Then:** commit · `/clear`

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

**Placed here, before F13**, so that F9's diff-card session (18d) has both its
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
reason slugs. Session 18d is written against these exact names.

Record the MEASURED compliance rate out of five runs, and the model it
was measured against. That number is unrecoverable after this session
and it is what tells a later episode whether the prompt needs work.
```

**Then:** commit · `/clear`

---

# SESSION 17c — B32 `WriteProjectFile`: the Editor's Save Path

**Goal:** make "save" exist. Point cannot write a project file — there is no
write event in the backend, and the frontend writes nothing but `state.json`
and its two log files — so every save, autosave and refusal path in F13's
spec is written against a path that does not exist.

**Why this episode exists.** Session 18a (F13) was started against its full
spec and **stopped before any code** at its own "settle this before writing
code" instruction. Save ownership was settled: **the backend owns the write,
and the read moves with it.** `pathguard.py`'s own docstring already names
"every editor open" as its consumer; `project_idea.md` §12 makes editor-write
permission backend-enforced policy, which a Go-side write would make
undeliverable for a B16 guest; and the lock key is derived backend-side from
`resolve_path` (`project_manager.py:378-381`), so Go would have to reproduce
that mapping to know which `file_locks` row its own write belongs to — the
B12-bug-#2 / B23-`ListDirectory` drift pattern, in the one place where the
consequence is a write rather than a wrong number.

Settling it surfaced **two further blockers F13 cannot route around**:
`ReadProjectFile` collapses CR LF to LF through `read_text`'s universal-newline
mode (`project_manager.py:269`), so the frontend has nowhere to learn a file's
line ending from and F13's Done-when #2 cannot pass; and `index.max_file_kb`
(default 100) makes F13's own 5,000-line Done-when both unreadable and
invisible in the file tree. Full trace in
`design/tasks/backend/B32-write-project-file.md`'s "Why this episode exists".

**Placed here, before F13**, for the same reason 17b (B24) sits before 18d:
the consumer should find its dependency already landed. Unlike B24, this one
is **mandatory** — F13 does not start without it.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B32-write-project-file.md.

One manager method, one event type, one reply type, one additive change
to ReadProjectFile's payload, one CLI subcommand. Every design call is
already made in the task doc -- creation via expected_mtime null,
heartbeat refreshed on success only and after the bytes land, refusals
as ok:false replies while path and project failures stay Error
envelopes, and no allow_outside_project flag on the write at all.

A refused write must leave the file byte-identical. Check the hash, not
the reply.

Do NOT register this as a tool -- it is a runtime event handler like
ProjectInit, and the absence of a write tool is how CLAUDE.md's first
hard rule is enforced structurally. Do NOT build any frontend. Do NOT
touch index.max_file_kb, _build_index's cache key, or context.py's
three direct read_text calls. Do NOT make overwrite the default.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the byte-exact round trip. Write a CRLF + BOM file, hash it, read
and write it back through the event without editing, hash it again. Equal, or
the episode is not done. Then confirm by eye that the `stale` refusal reports
the file's **current** mtime rather than echoing back the one it was sent — a
handler that echoed the input would pass every other check in the gate.

**Record:**
```
Write design/completed/backend/B32-write-project-file.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit
and how they were fixed, and what the next episode inherits. Add a row
to that INDEX.md's status table.

Record the exact ProjectFileWritten payload shape -- every field name --
and the full list of refusal reason slugs. Session 18a (F13a) and 18d
(F9b) are both written against these exact names.

Record the seven new ReadProjectFile metadata fields and what each one
is measured from. Record whether decode_replacements came back non-zero
on any real file in this repo: that is a finding about B7's lossy
errors="replace" read, not about this episode, and it is unrecoverable
once the session is over.
```

**Then:** commit · `/clear`

---

# SESSION 18a — F13a Editor Core: the Editing Half  ✅ DONE

**Goal:** the largest frontend episode. **It did take two sessions**, exactly as
this entry anticipated: it split mid-build at the save path. This session is
**F13a — the editing half**, and it is complete and verified (seven rounds).
The save half is **Session 18b (F13b)** below.

> Read `design/completed/frontend/F13a-editor-core-editing.md` before 18b.
> It records the thirteen pre-code decisions (A1–A3, B1–B10), the five
> Build-section assumptions that did not hold, and what each of the seven
> rounds actually fixed. See also "Splitting an episode" above.

**Prerequisites, all three** — the same three the INDEX row lists
(`F12` + `B7, B32`). Recorded for consistency with 18b/18d/19; this
session is done, so nothing here gates anything now:

1. **F12** (Session 17) — the workspace registry and lifecycle the Editor
   is registered into. `LeaderKey: "e"`, `MinSize{70,16}`, the idle
   timers, and the dirty-buffer skip are all F12's surface.
2. **B7** (Session 2) — `project.list_files` for the file tree, plus
   `pathguard.resolve_path` and `ProjectManager.read_file` behind the
   open path.
3. **B32** (Session 17c) — **for its READ path, not its write path.** The
   save path did not exist when this episode was first written, and B32
   builds it; what F13a actually consumes is the metadata B32 added to
   `ReadProjectFile` (`line_ending`, `crlf_count`/`lf_count`, `bom`,
   `trailing_newline`, `decode_replacements`, `writable`), which it
   carries through un-re-derived so 18b can write back without
   returning to the wire. Every field name in the paste below comes from
   B32's completed doc.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/LAYOUT.md, and
design/completed/frontend/F13a-editor-core-editing.md.
Then read design/completed/backend/B32-write-project-file.md -- the save
path's real shape, which this episode is written against field for
field.

Auto-indent is Tier 1 only (copy previous line's indentation). Line
endings detected on open and preserved on save -- this matters on
Windows. Undo must group keystrokes, and an agent-applied diff must be
ONE undo.

THE SAVE PATH IS SETTLED AND BUILT, AND THE BACKEND OWNS THE WRITE.
WriteProjectFile takes path, content (LF-normalised, no BOM), holder,
expected_mtime, overwrite, line_ending, bom, trailing_newline, and
returns the NEW mtime -- which is exactly what autosave rule 2 records,
so there is no follow-up CheckFileStale. ReadProjectFile now reports
line_ending, crlf_count, lf_count, bom, trailing_newline,
decode_replacements and writable. Use them. Do not re-derive any of
them, do not strip a BOM yourself, and do NOT write a project file from
Go -- config\state.go stays the only file the frontend writes.

Refusals come back as ok:false with a reason slug: stale, exists,
locked, read_only, too_large, no_parent_dir, io_error. Render the three
buttons off `stale` and the holder+age off `locked`. A path or project
failure is still an Error envelope, not a slug.

Four things in THIS episode's doc are wrong about this repo. They were
found before the episode started; do not rediscover them:

1. runtime.py is 88,425 bytes, not the "21KB" the manual verification
   claims.
2. No 5,000-line file exists in this repo. Generate the Done-when #1
   fixture.
3. Done-when #7's "second Point instance" is unachievable as written.
   backend\lockfile.py refuses a second backend against the same
   profile, and two profiles have two runtime.db files and therefore
   two separate file_locks tables -- so they cannot see each other's
   locks either. Use cli.py as the second holder: `project lock acquire
   --holder someone-else@other-id`. It deliberately does not take the
   single-instance lock.
4. The Editor has no project id, and the lock events require one.
   Resolve it from the opened file's OWN path, walking up for .point\,
   via ProjectResolve. No .point\ found -> open READ-ONLY. Not
   editable without a lock.

The holder key is the WHOLE composed string <username>@<full-id>,
generated once and persisted in state.json as one opaque value, never
regenerated on relaunch -- not the id alone with the username re-read
live at each launch, which would make a renamed account silently a
different holder. Display <username>@<first 8 of the id>, splitting on
the LAST @; compare on the whole string. The backend neither generates
nor validates it.

A file between index.max_file_kb (100) and editor.max_file_kb (512) is
openable by path but will NOT appear in the file tree. That is an
accepted limitation recorded in B32's handoff, not a bug to fix here.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**You do:** the CRLF check — open and save a file without editing, then
`git status`. If the whole file shows modified, it is wrong. Then the autosave
counter: type continuously for 30 seconds and confirm **one** write landed, not
thirty — read the file's mtime, do not watch the dirty marker.

**Record:**
```
Write design/completed/frontend/F13a-editor-core-editing.md following the format
in design/completed/backend/INDEX.md. Include the bugs we hit and how
they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table. If this took two
sessions, say so and record what was already inherited going into the
second one.

Also record the Buffer interface and exactly how undo grouping works.
F15's ghost text and diff-apply write into this same buffer and must
respect its undo boundaries, or Ctrl+Z stops being a single, trustworthy
action.

Record the CRLF and BOM handling as implemented -- which of B32's seven
metadata fields the buffer stores, and what it sends back on save -- and
any Windows Terminal key combination that did not arrive. Also correct
the four stale facts listed in the paste, inside F13's own task doc, so
the next reader of it is not misled the same way.
```

**Then:** commit · `/clear`

---

# SESSION 18d — F9b Diff Card (F9 resumed)  ✅ DONE — #7 closed by F15, F9a hazard 1 by F9d

> **This session was `18b` until F13 split.** It moved to `18d` so F13's
> two halves could be `18a`/`18b` and match their doc letters. Nothing
> about its dependencies changed — see "Splitting an episode" above.
> Older docs may still say "Session 18b" for this; they mean this block.

> 🟡 **RAN, and F9b itself split.** Read
> `design/completed/frontend/F09b-diff-card.md` before any session that
> touches the diff card, F15, or the editor's apply path.
>
> **Built: Done-when #5 and #6.** The card, the real line diff (new package
> `frontend\internal\diff`, which F15 calls rather than re-implementing),
> `e` into a scratch buffer with the diff re-rendering live, and `⏎`
> applying through `doc.ApplyRegion` as one undoable splice.
>
> **NOT built: Done-when #7** (refuse an apply when the file changed on disk
> since the region was generated). **Absent, not approximated** — the
> session stopped on its own prerequisite check and reported before writing
> code, as the paste below instructs. Nothing in the backend captures
> `(mtime, size)` at region-generation time
> (`_render_editor_context` reads the file and never stats it;
> `RegisteredEditorContext` carries only `shown_start`/`shown_end`;
> `FillProposal.to_dict()` is seven fields with no stat), and
> `CheckFileStale` accepts neither `session_id` nor `size`. The available
> pair — `doc.mtime`/`doc.size` — is the open/last-save moment, and
> substituting it fails in BOTH directions, so it was refused rather than
> approximated.
>
> **Point 1 of the paste below (apply-time guard ORDER) is blocked by the
> same gap**, and nothing fell back to `_resolve_optional_project` to make
> it work: the apply makes no backend call and no disk write at all, the
> Editor has no session to resolve a scope from (`protocol.go:20`), and the
> proposal's `session_id` never reaches Go. It becomes load-bearing when an
> applied buffer is SAVED, which F15 makes reachable.
>
> **Both deferrals are now tracked as `B36`** — see **SESSION 19a** below,
> which runs before 19.
>
> **The three "You do:" steps below were NOT performed, and could not be.**
> `NewDiffCard` has no production caller anywhere in the Go tree —
> deliberately, because F15 is *downstream* of F9b and there is no
> `EvFillProposal` decoder yet, exactly as `doc.ApplyRegion` shipped with no
> caller at 18a. So no diff card can be made to appear in the running app,
> and **the Go test suite is this episode's actual verification surface**
> (26 new tests; `internal\diff` 9/9, `internal\editor` 305/305, gofmt and
> vet clean, full frontend suite green under a scratch profile). Step 2 is
> #7 and is deferred; **steps 1 and 3 are owed by Session 19**, the first
> time a card can actually be opened.
>
> **F09's "same card, same keys, in both workspaces" was also NOT built**,
> and is left open as a decision rather than settled: `doc.ApplyRegion` is
> on the unexported `doc`, `Workspace` exposes no apply wrapper, and `app`
> imports neither `chat` nor `editor` (both import `app`), so a card beside
> F9a's `ResultCard` cannot reach the apply at all. Two options are recorded
> in the completed doc.
>
> **F9 now stands at 4 of 8 Done-whens** (#1, #8 from F9a; #5, #6 here) and
> stays 🟡. It closes when F9b's deferred half and F9c (Session 23b) land.

**Goal:** the second third of F9. Every agent-proposed change is reviewed,
editable, and reversible before it touches a buffer.

**Prerequisites, all four mandatory** — the same four the INDEX row
lists (`F9a, F13a` + `B24`):

1. **F9a** (Session 15, done) — the task tray and result cards this card
   is rendered inside. This session is the second third of F9, so F9a is
   a prerequisite even though the Goal line only implies it.
2. **F13a's editor buffer** (Session 18a, done) to apply into —
   `doc.ApplyRegion` is the entry point it shipped, one splice and one
   undo step, deliberately with no caller.
3. **B24's `FillProposal` event** (Session 17b) to compute a diff from.
4. **`CheckFileStale`** for the refusal.

If any is not landed, this session does not start. **F13b (18b) is NOT a
prerequisite:** the card applies into a buffer, and saving that buffer is
the user's next keypress, not this session's concern.

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

**You do:** three things, in this order. **⚠ NONE OF THESE WERE RUN — see the
note at the top of this block. There is no way to open a diff card in this
build; steps 1 and 3 are owed by Session 19, and step 2 is Done-when #7,
deferred to B36.** Kept as written because they are what Session 19 must
perform.
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

# SESSION 18b — F13b Editor: Save, Locks, Autosave (F13 closed)  ✅ DONE

**Goal:** close F13. Done-when 2, 7, 8, 9, 10, 11, 12 — the half F13a
deliberately did not build. **It did, across five rounds**: one build plus
four of gate-driven repairs, all in one session per the episode-boundary
rule. F13 is complete.

> Read `design/completed/frontend/F13b-editor-save-locks.md` before any
> session that touches the editor. It records the five pre-session
> decisions (C1—C5), what each of the four verification rounds
> actually fixed, and the six things still owed. Commits `65cb6dc` (the
> build) and `6e28f6b` (the repairs).
>
> **Two episodes were spun out of its testing and are NOT part of it:**
> **B33** (the protocol channel ran in the ANSI codepage, so any
> non-ASCII save was silently corrupted or dropped — Done-when 2's
> byte-exactness was only true for an ASCII file until it landed) and
> three items now in **F16** (two shipped in `6e28f6b`, one reverted and
> deferred there with its measured numbers and known fix).

**Prerequisites, mandatory** — the same three the INDEX row lists
(`F13a (done)` + `B7, B32`):

1. **F13a** (Session 18a), done. Read
   `design/completed/frontend/F13a-editor-core-editing.md` first: it
   names the three things this session inherits unbuilt (A2's holder key,
   B9's `OpenWorkspaceOverlayMsg`, B10's `[diff]` view) and the one
   decision still open (B4's save-side mixed-ending policy).
2. **B32** (Session 17c) — `WriteProjectFile` itself. There is no
   file-write event in the backend without it, and CLAUDE.md's first hard
   rule means this session cannot add one from Go.
3. **B7** (Session 2) — `pathguard.resolve_path`, `check_stale`, the four lock
   methods and the `file_locks` table. Every lock behaviour in Done-when
   7 and 11 is B7's surface, not new work here.

**Numbered 18b so the session letter matches the doc letter.** F9b held
`18b` and was moved to `18d` to free it — see "Splitting an episode"
above for the full chain. Order: **18a** (F13a) → **18b** (F13b, this
session, needs 18a only) → **18d** (F9b, needs 18a's `doc.ApplyRegion`,
independent of this session) → **19** (F15, which calls neither this
session's code nor the save path, but cannot be "v1 feature-complete"
while F13 is open). 18d's block sits physically above this one in the file;
that is slotting order, not dependency order.

**Paste:**
```
Read CLAUDE.md, design/tasks/frontend/F13b-editor-save-locks.md, and
design/completed/frontend/F13a-editor-core-editing.md -- what is already
built and what this session inherits unbuilt.
Then read design/completed/backend/B32-write-project-file.md for the
save path's real shape: WriteProjectFile's nine ordered steps, its seven
refusal slugs, and why a successful write's reply carries the new mtime
(autosave rule 2, and why no follow-up CheckFileStale is needed).

Start from backend\tests\gate_f13a.ps1 rather than CLAUDE.md's snippet:
it already has the scratch-profile setup, the fixtures, the Assert-That
accumulator and the Continue preference B32 established for any gate
that provokes backend stderr.

Stop at the verification gate with PowerShell checks.
```

**Record:**
```
Write design/completed/frontend/F13b-editor-save-locks.md. Include the
CRLF/BOM round-trip evidence (hashes before and after), which ending a
mixed file is written with and why, the holder key's persisted shape,
and any Windows Terminal key combinations that did not arrive.
Move F13a's row in design/completed/frontend/INDEX.md from the split
note to a plain F13 ✅, and add F13b's own row.
```

**Then:** commit · `/clear`

---

# SESSION 18e — B33 The Protocol Channel Is UTF-8  ✅ DONE

**Goal:** `backend\main.py` read stdin and wrote stdout in the platform's
ANSI codepage, never UTF-8, so a non-ASCII payload was either silently
mojibake'd (and the write REPORTED success) or dropped with no reply.
Pinning stdin then moved the failure into the reader thread, which died
and left the process hung with the single-instance lock held.

**Numbered 18e** because it was found during 18b's testing and belongs
beside it, not after F15 — see "Splitting an episode" on session
labels being positions in this file rather than an ordering claim. It is
a BACKEND episode in a frontend-numbered slot for that reason alone.

> Read `design/completed/backend/B33-transport-utf8.md`. It records why
> nothing in the repo caught this from B6 to F13b: `cli.py` and every
> `manual_*` test build a Runtime in-process and never cross the
> stdin/stdout boundary. `backend\tests\manual_transport_encoding.py`
> is the first test that spawns `main.py` over pipes, and a future
> transport change extends it rather than adding a second one.

**Then:** commit · `/clear`

---

# SESSION 18g — B35 Two Artefacts B20 Committed and Nobody Read  ✅ DONE

**Goal:** `CLAUDE.md` — the file every session is told to read first — has
been mojibake'd since `790e0c2` (B20, 2026-09-24). Twenty-four lines carry
`â€”` where an em-dash belongs, plus a UTF-8 BOM that is not deliberate.
The same commit also added `backend\direct.json` and `backend\wire.json`:
two byte-identical UTF-16LE dumps of a `context.report` payload that git
stores as BINARY and that nothing in the repo reads. Two unrelated root
causes, one commit.

**Numbered 18g because it was written after 18f, but it RUNS FIRST** —
the letters are labels, not an order; position in this file is the order,
which is why this block sits above 18f. B35 deletes the two UTF-16LE
files, and those are exactly the binary blobs B34's draft `.gitattributes`
comment says do not exist. Running B35 first makes that comment true
instead of making B34 fix someone else's mess. See "Splitting an episode"
on session labels being positions rather than an ordering claim.

> **This does not reopen B33.** Its fix is correct and was re-verified
> green (14/14) against HEAD `fdc6cf7` during the pass that found this.
> What B33 got wrong is the SCOPE of one claim in its completed doc:
> "verified afterwards that no mojibake remains anywhere in the tree"
> compared the WORKING TREE against HEAD, which is blind by construction
> to a file corrupted in a commit thirteen episodes earlier. The bug class
> was live from B6, so its victims were in history, not only in the
> working tree. **That is the transferable finding, and it is not about
> encodings** — any episode fixing a long-lived corruption bug has to ask
> where the victims would be and whether that is where it is looking.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B35-claude-md-corruption.md.

Two separate commits, one per root cause. Do not combine them, and do
not run `git add --renormalize` -- that is B34's operation and would mix
46 files of line-ending churn into a 24-character fix.

Repair CLAUDE.md at the BYTE level: replace the 24 occurrences of
C3 A2 E2 82 AC E2 80 9D with E2 80 94, assert the count before and
after, and expect the file to shrink by exactly 120 bytes. Do not decode
to text and write back with a default encoding -- that is the shape of
the bug being fixed. Decide the BOM explicitly and record the reasoning;
the three .ps1 gates keep theirs and nothing here touches them.

Do NOT "fix" the deliberate mojibake in backend\main.py and the B33 and
B35 docs. They are prose about the bug and a scan that flags them is
working correctly.

Confirm nothing reads direct.json / wire.json before deleting them, and
record in the completed doc that B20's verification redirected into the
repo root -- that is the reusable lesson, not the two files.

Stop at the verification gate with PowerShell checks.
```

**Then:** commit · `/clear`

---

# SESSION 18f — B34 A Declared Line-Ending Policy  ✅ DONE

**Goal:** this repo has no `.gitattributes` and `core.autocrlf` is `true`
from the SYSTEM config, so every blob is LF while 45 tracked files are
CRLF in the working tree and one (`backend\runtime\errors.py`) is MIXED
— the one case B32 says cannot round-trip byte-exact. Commits stay
correct only by accident of that setting, and `gofmt -l` cannot be used
as a gate on a fresh Windows checkout.

> **Run 18g (B35) before this one.** B34's draft `.gitattributes` carries
> the comment "No binary entries yet: `git ls-files` currently lists no
> binary file." That is wrong as written — `backend\direct.json` and
> `backend\wire.json` are UTF-16LE and git already classifies both as
> binary. They also skew B34's own headline measurement: UTF-16LE encodes
> CRLF as `0d 00 0a 00`, so the two bytes are never adjacent and a
> byte-pair scan does not count them, which is part of why B34 reads
> "zero tracked blobs contain CRLF". B35 deletes both files; if it has
> run, B34's comment becomes true and nothing else changes. If for any
> reason B34 goes first, it must add an explicit binary entry for them.

**Paste:**
```
Read CLAUDE.md and design/tasks/backend/B34-line-ending-policy.md.

Scope only. The doc already names the recommended shape (explicit
per-type `text eol=lf`, NOT `* text=auto`) and why, the files that would
normalise, and the two-commit split it requires. Do not combine the
.gitattributes commit with the renormalise commit.

Verify both PowerShell gates still parse with zero errors and still
report BOM present, ASCII-only, zero CRLF afterwards.

Stop at the verification gate with PowerShell checks.
```

**Then:** commit · `/clear`

---

# SESSION 19a — B36 Generation-Time Staleness for the Filler's Proposal  ✅ DONE (independently re-run 2026-10-23, 13/13)

> **RUNS BEFORE Session 19.** Numbered `19a` rather than taking a plain
> number because it is an interleaved backend episode in the same shape as
> `15c` (B25, split out ahead of 16) and `18g` (B35, which runs before
> `18f`) — see "Splitting an episode" above.

**Why it runs before 19 rather than after:** F15 adds the Go
`EvFillProposal` decoder, which does not exist yet. If B36 lands first, that
decoder is written **once** against the final payload shape. If it lands
after, F15 ships a decoder that B36 then has to extend, and F9b's deferred
half has to wait for a second frontend pass to carry two fields it could
have had from the start.

**Why this episode exists.** F9b (Session 18d) built the diff card's apply
and **declined to build Done-when #7** — refuse the apply when the file
changed on disk since the region was generated — because it cannot be built
honestly against this backend. The prerequisite check is recorded in
`design/completed/frontend/F09b-diff-card.md`; the short version, all three
verified in code:

1. **Nothing captures `(mtime, size)` at region-generation time.**
   `_render_editor_context` (`managers\context.py`) reads the file with
   `path.read_text(...)` and **never stats it**. `RegisteredEditorContext`
   (`streaming\manager.py`) carries `context`, `shown_start`, `shown_end`
   and nothing else. `FillProposal.to_dict()`
   (`managers\fill_proposal.py`) is seven fields with no stat among them,
   and its own docstring says all seven travel together, no subset. So the
   moment the model saw the file is unrecoverable downstream.
2. **`CheckFileStale` cannot express the comparison.**
   `_handle_check_file_stale` (`runtime\runtime.py:860`) requires `path` and
   `mtime` only — no `size`, no `session_id` — and `check_stale`
   (`managers\project_manager.py:590`) compares `st_mtime` against the
   supplied value with a 1e-6 tolerance. Size is not a parameter.
3. **The pair that IS available is the wrong moment.** `doc.mtime` /
   `doc.size` are from the read that opened the file, replaced from every
   successful write's reply (autosave rule 2). F9b refused to substitute it:
   it fails in BOTH directions — a false refusal when the region was
   generated from content newer than the open, and a missed refusal when a
   save refreshed the pair after generation. That is B12's `set_mode`
   lesson and F8's chip staleness, which is exactly why F9b's own paste
   forbade it.

**It also unblocks F9a's hazard-1 guard ORDER**, which F9b could not
complete for the same reason: `session_id` is half of it. Nothing in F9b
fell back to `_resolve_optional_project` to fake it — the apply makes no
backend call at all — but the guard becomes load-bearing the moment an
applied buffer is **saved**, because F13b's `WriteProjectFile`
`_require_project`s the EDITOR's path-resolved `project_id` rather than the
session that produced the proposal. That is B12's bug #2 on a destructive
path, and F15 is what makes it reachable.

**Prerequisites:** **B24** (Session 17b, done) — this extends its event.
**B7** (done) for `check_stale` and the path guard.

**Paste:**
```
Read CLAUDE.md, design/completed/backend/B24-filler-proposal-event.md,
design/completed/frontend/F09b-diff-card.md's "#7 is absent, not
approximated" and "The guard order (F9a hazard 1) is also incomplete"
sections, and design/completed/backend/B07-project-system.md for
check_stale.

Build two additions and nothing else.

1. FillProposal must carry (mtime, size) CAPTURED AT THE
   REGION-GENERATION DISK READ. That is the `path.read_text()` in
   `_render_editor_context` (managers\context.py) -- stat the file at
   the moment it is read, not before and not after, and carry the pair
   forward on RegisteredEditorContext beside shown_start/shown_end,
   then into FillProposal.to_dict(). Respect that dataclass's own rule:
   all fields travel together, no subset, so this is a wire-shape
   change and every consumer sees both fields or neither.

   The pair is a fact about ONE MOMENT. Do not re-stat it later in the
   turn to "refresh" it, and do not derive it from a second read --
   that is the same error B24 avoided by carrying shown_start/shown_end
   from the context build rather than recomputing them at the terminal
   event, and the error F9b refused to make on the frontend side.

2. CheckFileStale must accept session_id AND size as real comparison
   fields. Today `_handle_check_file_stale` (runtime\runtime.py) takes
   (path, mtime) and `check_stale` (managers\project_manager.py)
   compares mtime alone with a 1e-6 tolerance.

   - `size` must be compared, not merely accepted and ignored.
   - `session_id`, when supplied, must resolve the SESSION's scope --
     the `_attachment_scope` shape, from session.project_id -- and NOT
     `_resolve_optional_project`, which walks up from the path to
     auto-discover a project and answers a different question. That
     drift is B12's bug #2; 374baa2 established the corrected order for
     AttachContext. Follow that one.
   - Decide deliberately, and write down which: whether `mtime` and
     `size` are both required, and what a caller that supplies only
     mtime gets. An existing caller must not silently change meaning.

Both are ADDITIVE to a built, verified episode's event. Do not change
what B24 already emits beyond adding fields, and do not touch the
parse/validation rules -- B24's five reason slugs are what F9b's
frontend is written against.

Never a direct disk write from an agent path. No WriteProjectFile is
added or changed here.

Stop at the verification gate with PowerShell checks.

If anything here assumes something not true of this repo, stop and tell
me before writing code.
```

**You do:**
1. Run a fill-mode turn and confirm the emitted `FillProposal` carries
   `mtime` and `size`, and that they match the file **as it was when the
   context was built** — change the file on disk *after* the build but
   before the terminal event, and confirm the pair still describes the
   pre-change file.
2. `CheckFileStale` with a matching `(mtime, size)` → not stale. With the
   same mtime but a different size → **stale**. That is the check mtime
   alone cannot make, and the reason `size` is in this episode.
3. `CheckFileStale` with a `session_id` whose session has no project, over a
   path that has a `.point\` above it → confirm it is guarded against the
   SESSION's (empty) scope, not the path-resolved one. This is B12 bug #2's
   shape and the one check that proves the guard order.

**Record:**
```
Write design/completed/backend/B36-generation-time-staleness.md following
the format in design/completed/backend/INDEX.md, and add its row.

Record the exact wire shape of the extended FillProposal and the exact
CheckFileStale signature, including what a caller that supplies only
mtime now gets -- F9b's deferred half and F20 both call these and need
a contract, not a description.

Then say plainly in that doc which of F9's Done-when #7 and F9a's
hazard-1 guard order are now UNBLOCKED but still NOT BUILT: both are
frontend work. Update design/completed/frontend/INDEX.md's F9b row to
point at this episode as the unblocker, and add a session to
WORKFLOW.md for F9b's deferred half.
```

**Then:** commit · `/clear`

---

# SESSION 19 — F15 Filler-Explainer Integration  ✅ DONE

**Goal:** v1 feature-complete. — **met.** Recorded in
`design/completed/frontend/F15-explainer-integration.md`. Four defects found at
the gate were not in the Build section and are written up there as root causes:
bubbletea's Windows chord leak (mitigated with F2/F3 beside the legacy
ctrl+w/ctrl+q, both kept and both shown), the inline prompt's undo coalescing,
`ctrl+c`'s double meaning (now copy only; `esc` is the only cancel), and the
ghost's scroll architecture (rewritten to one row sequence and one scroll
position). **F9's Done-when #7 closed here** as F15's 7b.

> **F9b's packaging collision does NOT block this episode — confirmed, so
> nobody re-raises it here as a blocker.** F9b left open where a diff card
> *shared by Chat and the Editor* should live: `doc.ApplyRegion` is a method
> on the unexported `doc`, `Workspace` exposes no apply wrapper, and `app`
> imports neither `chat` nor `editor` (both import `app`), so a card in
> `app` cannot reach the apply at all. **F15 never needs that**, because
> F15's own spec gives the two workspaces *different* presentations: the
> diff card is specified only at the Editor cursor (F15's Build → "Fill
> (write path)", Done-when 3 and 4), while the Chat path is specified as a
> **result card** — F9a's, which already lives in `app` — and explicitly
> not a diff card ("`@filler-explainer` … uses the normal background-task
> plus result-card presentation"; Done-when 8: "The same agent invoked from
> Chat produces a result card instead of an inline diff"). That is the
> spec's own "the invocation site decides presentation". The collision is
> F9's to settle, on F9's own schedule.
>
> ⚠ **One correction worth carrying, because the easy version of this is
> wrong:** the reason is *not* that the Filler is Editor-only. **It is
> not** — F15 has a real Chat path (Done-when 8, and Build → "From Chat").
> The paste block's "Explanations render in an overlay panel inside the
> Editor" describes the **explain** path, not fill, and does not make the
> episode Editor-only. The reason the collision does not bite is the
> result-card/diff-card split above, which holds whether or not Chat is in
> scope — so it survives a reader noticing the Chat path, which the
> Editor-only reading would not.

**Prerequisites:** **F13a** (Session 18a, done) for the buffer, ghost
text, apply-into-buffer and undo; **F9b** (Session 18d) for the diff
function this episode calls rather than re-implements; **B11** and
**B24** (Session 17b) for the agent and its parsed, validated proposal;
and **B36** (Session 19a) for the `(mtime, size)` pair the diff card's
staleness refusal needs.

**Why B36 is a prerequisite and not an optional extra — this episode is
where F9b's deferred fields get filled in.** F9b shipped
`DiffCardOptions.RecordedMtime` / `RecordedSize` and left them **zero by
design**: `design/completed/frontend/F09b-diff-card.md` records that it
refused to populate them from anything available at the time, because
nothing described the right moment (`doc.mtime` is the open/last-save
moment, and substituting it fails in both directions). B36 is where
`FillProposal` starts carrying a **real** pair, captured at the
region-generation disk read. **F15 is the episode that fills them in**,
because F15 writes the `EvFillProposal` decoder and constructs the first
`DiffCard` — there is no other episode between the two that touches
either end. Running 19 before 19a would leave F9's Done-when #7 open with
the backend half already landed and the two unpopulated fields sitting in
a decoder that has to be revisited, which is the worst of the three
available states.

**F13b (Session 18b) is a scheduling prerequisite, not a technical one.**
F15 writes nothing to disk in any phase — its own Do-NOT says so, apply
goes into the UNSAVED buffer, and Done-when 5 is verified by comparing
`LastWriteTime` across apply-then-undo, which never saves. But F15 is the
last P1 episode and "v1 feature-complete" is not true while F13 is still
open, so **18b runs before 19** regardless. The INDEX row reads `F13a,
F9b` for the same reason: it records what this episode calls.
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
(the diff card, Session 18d), not "F9". The fill reply is no longer raw
model text either: B24 (Session 17b) parses and VALIDATES it in the
backend into a structured proposal with the shown region attached, and
F9b computes the diff from that. Do not parse the model's JSON here,
and do not write a second diff implementation -- F9b's completed doc
records the function signature precisely so this episode has something
to call.

POPULATE DiffCardOptions' RecordedMtime and RecordedSize from the REAL
fields B36 (Session 19a) adds to FillProposal -- the (mtime, size) pair
captured at the region-generation disk read, carried through the
EvFillProposal decoder you are writing. F9b shipped those two fields
UNPOPULATED on purpose and wrote down why; this episode is where they
get filled, and it is the only episode positioned to do it.

Do NOT leave them zero, and do NOT substitute doc.mtime / doc.size.
Those are the open/last-save moment, not the generation moment, and
design/completed/frontend/F09b-diff-card.md records that substituting
them fails in BOTH directions: a FALSE refusal when the region was
generated from content newer than the open, and a MISSED refusal when a
successful save refreshed the pair after generation. A staleness check
that permits the dangerous apply is worse than no check at all, because
it is believed. F9b's TestTheStalenessPairIsCarriedAndUnpopulated is the
tripwire that currently asserts the pair stays zero -- changing that
test is part of this episode's work, deliberately, not a test to route
around.

Done-when 7b is this episode's proof that F9's Done-when #7 -- deferred
by F9b as its Hazard 2 -- is finally closed. It is a DIFFERENT check
from Done-when 7, and both must pass: 7 is the BUFFER changing since
generation (F9b's baseVersion guard, already built and tested), 7b is
the FILE changing ON DISK, which is what needs B36's pair.

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

# SESSION 19b — F9d Apply-Time Guard Order (F9a Hazard 1)  ✅ DONE (gate green all four steps, 2026-10-24)

> **RUNS AFTER Session 19.** Numbered `19b` rather than `19c`/a plain
> number because, like `19a`, it is an interleaved episode slotted
> against 19 rather than a sub-episode of it — see "Splitting an episode"
> above. Unlike `19a` it runs AFTER its anchor, not before: the guard
> order this session fixes is, by F9b's own words, "not reachable" until
> something actually calls `WriteProjectFile` from an agent-applied
> diff — and F15 (Session 19) is the first and only episode that adds
> that caller. Running this before 19 would mean fixing a call site that
> does not exist yet.

**Why this episode exists.** `F09b-diff-card.md`'s "The guard order (F9a
hazard 1) is also incomplete" section, read in full before starting:
F9a's own hazard list requires "resolve the SESSION's scope first, guard
against that, and commit nothing until the guard passes" for any backend
call an applied diff makes. F9b built the apply itself (into an in-memory
buffer only, no backend call), so the hazard was dormant there. It becomes
live the moment the applied buffer is **saved** — `frontend\internal\editor
\save.go`'s write goes to `WriteProjectFile`, whose handler
(`runtime.py`'s `_handle_write_project_file`) resolves its project via
`_require_project` → `_resolve_optional_project` — the EDITOR's own
path-walked project (whatever `.point\` sits above the open file), never
the SESSION that produced the Filler's proposal. That is B12 bug #2 on a
destructive path: a file could be saved under the wrong project's guard,
silently, the exact drift `AttachContext`/`ResolveAttachment` were fixed
against in `374baa2`.

**What B36 (Session 19a) changed, and what it did NOT.** `CheckFileStale`
now accepts `session_id` and resolves the SESSION's own scope
(`_attachment_scope`) instead of the path-walk — exactly the primitive
this guard needs. **B36 did not touch `_handle_write_project_file` itself**,
and did not wire anything to call the new session-scoped `CheckFileStale`
from the apply/save path — see `completed/backend/B36-generation-time-
staleness.md`'s "What the next episode inherits" for the exact two things
left open. This session is where they get picked up.

**Paste:**
```
Read CLAUDE.md, design/completed/frontend/F09b-diff-card.md's "The guard
order (F9a hazard 1) is also incomplete" section in full, design/completed
/backend/B36-generation-time-staleness.md (the session_id contract this
episode uses), and design/completed/frontend/F15-explainer-integration.md
(read it fresh — this session runs after F15, so read what F15 actually
built, not what its task doc planned).

Fix the guard order for an agent-applied diff's save: resolve the SESSION
that produced the diff (the session F15's EvFillProposal arrived on, or
whatever the Editor's own save path now carries for this), and guard the
write against THAT session's scope -- not whatever project
`_handle_write_project_file`'s own `_require_project` would path-walk to
from the file's location. Decide, by reading F15's actual save-path code
(not by guessing ahead of it), whether the right shape is:

  (a) a pre-write guard call: resolve the session's scope, call the now
      session-scoped CheckFileStale (or an equivalent session-scoped
      existence/containment check) BEFORE WriteProjectFile is sent, and
      refuse the save entirely if the guard fails; or
  (b) a direct fix to `_handle_write_project_file`'s own project
      resolution, so EVERY write from an editor buffer that has a known
      originating session resolves against that session, not the path.

Whichever shape, the end state is the one F9a's hazard-1 already states:
commit nothing until the guard, resolved against the right scope, passes.

Do not restate or re-litigate F9's packaging decision (same card, same
keys, in both workspaces) -- that stays open on its own schedule and is
not this session's concern.

Stop at the verification gate with PowerShell checks (plus `go test
./frontend/...` for whatever changed on the Go side).

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write up what was fixed and where (backend handler, frontend save path,
or both) in whichever completed doc it belongs to -- F15's own, or a new
F9d doc if the fix is substantial enough to warrant one. Add a row to
the relevant INDEX.md. State plainly whether F9a's hazard-1 is now fully
closed, and update design/completed/frontend/INDEX.md's F9b row
accordingly -- it currently says this is scheduled here.
```

**Then:** commit · `/clear`

---

# SESSION 20 — B15 Lifecycle and Crash Recovery  ✅ DONE (gate green 9/9, 2026-10-24)

**Goal:** demo-critical despite being P2. Do this before the other P2 work.

**Outcome:** built and verified — see
`design/completed/backend/B15-lifecycle-resources.md`. Stopped before code
first, as the block asks: three Build-section items assumed things this repo
does not have (no event log for `events_since`; no partial on disk for
Done-when #3; nothing for `WorkspaceContext` to own). `SessionReplaySince`
shipped as a **stated interim** and the real event log became **B40**
(Session 20b). The gate also found two harness bugs and one product gap
the Build section had asked for and nobody had built (signal handling).

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

# SESSION 20b — B40 The Durable Event Log ✅ done

**Goal:** replace the interim B15 shipped. B15's Build section asked for
`session.events_since(session_id, seq)`; this repo has no event log, so B15
built **`SessionReplaySince`** — message-level replay keyed on `messages.seq`,
with `mechanism` stated on the wire so nothing has to guess which `seq` it
holds. This session builds the real one and deletes the interim.

**Read the task doc first.** `design/tasks/backend/B40-durable-event-log.md`
was written during Session 20, before any of this was built, while the reasons
were in front of someone. Its Build section names the two real decisions (the
cost of a write per envelope on the token path through `SerializedConnection`,
and what `seq` means once it is durable and per-session) and its appendix
records four things B15 built that B15's own Build section never asked for.

**Paste:**
```
Read CLAUDE.md, design/tasks/backend/B40-durable-event-log.md, and
design/completed/backend/B15-lifecycle-resources.md (what B15 actually
built, not what its task doc planned).

Build the event log and the real events_since, and DELETE
SessionReplaySince from both sides -- events.py, cli.py, types.go,
client.go, and Client.afterReconnect. Two replay commands with two
different cursors is the second-copy-that-can-disagree this repo
removes on sight.

Two things are measurements, not judgements, and the episode owes
numbers for both: what a write per envelope costs on the streaming
path through SerializedConnection (same fixture, both arms), and
whether token deltas are persisted at all -- IsDroppable in
frontend\internal\protocol\types.go already says those three types may
be discarded under backpressure, which is an argument for not storing
them that the doc states and does not settle.

The new cursor is NEITHER of the two `seq`s that already exist. Decide
explicitly whether it rides as a new envelope field or changes what
`seq` means, and say which -- four consumers decode that field today.

B15's Done-when #4 was verified against the mechanism you are
replacing. RE-RUN it: tests\manual_b15_lifecycle.py's [4] block, which
will fail its `mechanism` assertion by design, then update it to assert
the event cursor with the no-gaps and ordering checks intact. A doc
edit alone would leave the repo claiming a verification performed
against code that no longer exists.

Stop at the verification gate with PowerShell checks (plus `go test
./frontend/...` for the Go side).

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/backend/B40-durable-event-log.md following the
format in design/completed/backend/INDEX.md. Include the measured cost
with its fixture and numbers, the `seq` decision and why, and the
retention policy. Add a row to design/tasks/backend/INDEX.md.

Also amend design/completed/backend/B15-lifecycle-resources.md: its
SessionReplaySince section describes a command that no longer exists,
and its Done-when #4 evidence now describes this episode's mechanism.
State plainly that #4 was re-verified here, with the new output.
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

<!-- KNOWN GAP, deliberately not fixed: this block states no prerequisites,
     while F14's INDEX row lists `F13a`. Found during the F13-split
     renumbering audit (18a/18b/18d/19 were reconciled; this was left
     alone as out of scope). It is a real mismatch of the kind
     "Splitting an episode" asks blocks to avoid, NOT a new bug — do not
     re-report it as one. Fix it when F14 is next touched: F14 needs
     F13a's editor workspace, and the "nothing depends on it" line below
     is about what depends on F14, not what F14 depends on. -->

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

# SESSION 22b — F20 Live Stale Detection  (not started)

**Goal:** an open document notices its file changed on disk without
waiting for a save attempt. F13's Done-when 8 only runs the stale check
on the WRITE path, so a file edited externally while Point's copy is
locally clean shows no signal at all — clean tab, silent status bar,
quietly out-of-date buffer — until somebody presses `ctrl+s`.

**Numbered 22b, after F14**, because it is `[P2]` and CLAUDE.md's phase
rule puts P2 after every P1 item is done and verified. It depends on
**F13b only** (18b, done) and not on F14 at all, so it could run any
time after 18b on dependencies alone — the position here is about phase,
not about prerequisites. Grouped with 22 because both are Editor P2
work. An interleaved session, the same way `16b`–`16h`, `17b`, `17c`,
`18d` and `23b` are, and not a sub-episode of F14.

**The episode is F20, not F19.** F19 was written up during F13b's
verification and retired in `4a2a41b` — two of its items moved into
`F16-polish.md` and the third was reverted and deferred there. The
number is burned: it is named as retired in seven places across
`design/`, so reusing it would make every one of those ambiguous.

> **Zero backend work.** `CmdCheckFileStale` / `EvFileStaleResult` were
> built by B32 and deliberately left with no caller — F13b's own note
> says "`CmdCheckFileStale` exists and may stay unused", because the
> write reply carries the new mtime and a save needs no follow-up check.
> This episode is the caller it was built for.

**Paste:**
```
Read CLAUDE.md and design/tasks/frontend/F20-live-stale-detection.md.

This is a new TRIGGER into F13b's existing refusal machinery, not a
second staleness mechanism. B32's expected_mtime precondition stays the
only thing that decides whether a write is safe, and stale.go's
[reload][overwrite][diff] dialog stays the only place a user resolves
one. Detection changes what is SHOWN, nothing else.

Poll, do not add a filesystem watcher. The reason is measured, not
preferential: F13b round 3 established that _atomic_write ends in
os.replace, which on Windows fires Deleted + Renamed and no Changed at
all, so a watcher configured the obvious way cannot see a save.

Do NOT open the dialog from a poll result. F13b already settled that a
modal appearing for someone else's change, a second after you stop
typing, is how people turn autosave off -- and a poll makes it worse,
because it can now fire while you are in a different file or in Chat.

The one race that can produce a FALSE "someone changed your file" is a
poll in flight across one of Point's own writes. Done-when 7 is that
case; get it right before anything else.

Stop at the verification gate with PowerShell checks.

If anything in this episode's Build section is ambiguous or seems to
assume something not true of this repo, stop and tell me before writing
code.
```

**Record:**
```
Write design/completed/frontend/F20-live-stale-detection.md following the
format in design/completed/backend/INDEX.md. Include the bugs we hit and
how they were fixed, and what the next episode inherits. Add a row to
design/completed/frontend/INDEX.md's status table.

Record the polling interval and WHY it was chosen, measured against the
idle-CPU check rather than assumed; whether the marker got its own key
(f5 re-index, f6 take over, f7 close, f8 is F14's terminal -- the next
one after that); and state explicitly that B32 and Done-when 8 were not
changed, because "live stale detection" and "the stale refusal" are two
different things a later reader will conflate.
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

# SESSION 28 — F21 Multi-line Fill-Prompt Input + B37  (not started)

> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.** This and the two sessions below are **Part 2 of the
> Filler/Explainer**: wanted, specced, and deliberately not part of v1.
> Everything in v1's own list comes first — CLAUDE.md's phase rule is that
> `[P2]` is touched only after every `[P1]` item is done and verified.
>
> **Two sessions, not one, and B37 runs FIRST.** The frontend must not work
> around a missing B37 by flattening newlines before sending — that would
> ship the prompt-collision bug invisibly, which is the one failure this
> pair exists to prevent.

**Goal:** a fill request can be longer than one line.

**Paste (B37 first):**
```
Read CLAUDE.md and the B37 row in design/tasks/backend/INDEX.md.

Decide how a MULTI-LINE user request is delimited inside the prompt
_render_editor_context builds, so the model can tell the request from
Point's own instruction block. Say what happens when the request itself
contains the delimiter. Backend only; do not change the Go side.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**Paste (F21 second):**
```
Read CLAUDE.md and design/tasks/frontend/F21-multiline-fill-prompt.md.

The prompt becomes a small bounded multi-line editor. Decide the submit
key and the newline key, and VERIFY the newline key arrives distinctly on
Windows before committing to it -- shift is not carried on tea.KeyMsg.
Reuse the buffer's undo-coalescing rule; do not write a third copy of it.
The prompt is already a block in viewrows.go's row index -- give it a row
count, do not emit rows from the renderer.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**You do:** type a four-line request and watch for the caret line being
pushed off the pane. Then paste 500 lines into the prompt.

**Record:**
```
Write design/completed/backend/B37-<slug>.md and
design/completed/frontend/F21-multiline-fill-prompt.md following the
format in design/completed/backend/INDEX.md. Add a row to each INDEX.
Record the newline key chosen and what was MEASURED about the rejected
one, and whether the prompt ended up holding a Buffer or its own slice.
```

**Then:** commit · `/clear`

---

# SESSION 29 — F22 Explain as an Ongoing Conversation + B38  (not started)

> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.**
>
> **B38 is the larger half and runs FIRST.** Its three decisions are what
> make a follow-up answer build on the previous one; the panel is the easy
> part.

**Goal:** the explain panel becomes a real back-and-forth.

**Paste (B38 first):**
```
Read CLAUDE.md and the B38 row in design/tasks/backend/INDEX.md.

Three decisions, all backend: whether a follow-up replays the prior
exchange as message history or as a summary; whether editor_context is
re-sent per turn, and what the user is told when the region changed
underneath the exchange; and the token bound plus what happens at it.

The second one is B36's problem one layer up -- pairing an answer with a
region read at a different moment. Do not decide it by analogy; decide it
and write down which way and why.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**Paste (F22 second):**
```
Read CLAUDE.md and design/tasks/frontend/F22-explain-as-conversation.md.

The panel gains entries and an input. ONE scroll position over the whole
exchange -- read viewrows.go's header first; F15 built the
follow-the-live-edge behaviour twice and the first version was a nested
sub-viewport. Order esc's three meanings and write them in panelKey.
ctrl+c stays copy only.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**You do:** ask a follow-up that only makes sense with the prior turn
("why?"), and read the rendered prompt out of the backend log to confirm the
model actually saw turn 1. A plausible second answer is not evidence.

**Record:**
```
Write design/completed/backend/B38-<slug>.md and
design/completed/frontend/F22-explain-as-conversation.md following the
format in design/completed/backend/INDEX.md. Add a row to each INDEX.
Record B38's three decisions as settled, the esc precedence, and whether
the input was shared with F21's or built fresh.
```

**Then:** commit · `/clear`

---

# SESSION 30 — F23 Fill on a Blank Line + B39  (not started)

> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.**
>
> **B39 runs FIRST and is the larger half.** The risk in this pair is not
> the new feature — it is weakening the EXISTING rewrite path's validation
> on the way to the new one.

**Goal:** fill works where there is no code to rewrite — pure generation.

**Paste (B39 first):**
```
Read CLAUDE.md, the B39 row in design/tasks/backend/INDEX.md, and
design/tasks/frontend/F23-fill-on-a-blank-line.md for the shape of the
case.

A THIRD EditorContext.mode, not a flag on fill. Decide what the model is
shown and how it is marked read-only; write a generation instruction
block that does NOT copy the "<path>" placeholder line; and validate an
insertion point plus a length bound INSTEAD of a range inside a shown
region -- without loosening the existing rewrite check. Capture
(mtime, size) the way B36 does, from the same descriptor as the read.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**Paste (F23 second):**
```
Read CLAUDE.md and design/tasks/frontend/F23-fill-on-a-blank-line.md.

The FRONTEND decides the mode (it already computes `enclosing`); the
backend must not re-derive it. Additions-only diff card -- do not
fabricate an old side. Check whether doc.ApplyRegion already handles an
empty range as one undoable edit and use it if so; do not add a second
splice entry point. The staleness guard stays.

Stop at the verification gate with PowerShell checks.

If anything is ambiguous or seems to assume something not true of this
repo, stop and tell me before writing code.
```

**You do:** the regression check matters most — a blank line INSIDE an
existing function must still take the rewrite path. Then check indentation
inside a class body.

**Record:**
```
Write design/completed/backend/B39-<slug>.md and
design/completed/frontend/F23-fill-on-a-blank-line.md following the
format in design/completed/backend/INDEX.md. Add a row to each INDEX.

State EXPLICITLY whether generation mode made the rewrite path's
validation weaker. That is the one way this pair can do real damage, and
a completed doc that does not answer it leaves the question open for
whoever reads B24's checks next.
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
