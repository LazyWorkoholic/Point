# Episode 12 — Routing, Attachments, Concurrency Caps ✅

**The minimum viable backend is complete. Typed input becomes a structured
`Invocation` under one ambiguity rule shared by every caller; attachments
resolve through guards that each return a reason and a token estimate produced
by the context builder's own renderer; concurrency is honest — a task that
cannot start enters `QUEUED`, is visible as queued, and is cancellable there;
and every persisted assistant message now carries both its provenance and how
it ended, so a session reloaded from disk is as truthful as a live one.**

> Verified by Aryan on 2026-09-06 against
> `tasks/backend/B12-routing-attachments.md`'s ten "Done when" criteria.
> Self-tested first end to end against throwaway scratch profiles under
> `%TEMP%` (`APPDATA`, `LOCALAPPDATA` and `POINT_DB` all redirected in **one**
> script — never the live profile, never a per-call env var), with real Ollama
> (`qwen2.5:1.5b-8k`) for the concurrency and routing checks and the B10 mock
> fixtures where determinism mattered. The live `%LOCALAPPDATA%\Point\runtime.db`
> was confirmed still at schema v2 afterwards.
>
> **Three things in the episode doc were wrong and were raised before any code
> was written**, per the standing rule that the code and the database are the
> truth:
>
> 1. `QUEUED` was described as "the existing B1 state". It did not exist —
>    B1 shipped `PENDING/RUNNING/COMPLETED/FAILED/CANCELLED` and nothing between
>    "created" and "running". Aryan confirmed it as new.
> 2. The concurrency and turn-queue gates drove three `Start-Job { cli.py send }`
>    processes. Each `cli.py` is an independent runtime with its own
>    `TaskManager`, so a per-process cap serializes nothing across them and
>    `tasks list` would have truthfully shown three RUNNING — the gate's own
>    stated failure condition. Aryan approved rewriting both steps to drive one
>    backend.
> 3. The gate's SQL did not match the schema: `messages` had no `interrupted`,
>    `outcome` or `error_code` column, and `invocation` is stored in the
>    `metadata` column, not inside `content`. Aryan chose **real columns** at
>    **v5**, after which the gate's queries are correct as written.
>
> **The gate did not pass on the first attempt.** Aryan's manual testing found a
> defect the scripted gate could not: `attach` refused every file with "no
> project is open" while standing inside a real project. See
> "Bugs caught during manual testing" — it was a real, general bug, and the
> scripted gate passed only because it happened to bind its session explicitly.

---

## Where the code went

Three new modules and one new migration. No new top-level package: routing and
attachments are business rules that belong beside the managers that already
exist, and the scheduler belongs beside the `TaskManager` whose behaviour it
gates.

```
backend\
├── managers\
│   ├── invocation.py            NEW — Invocation, AttachmentRef,
│   │                             parse_invocation(). Zero I/O: takes
│   │                             `is_agent` / `is_indexed_path` predicates
│   ├── attachment_manager.py    NEW — the guards, the estimate, and
│   │                             attach / detach / set_mode / preview
│   └── session_manager.py       + set_project()
├── runtime\
│   ├── scheduler.py             NEW — TaskScheduler, lanes, capacities,
│   │                             turn_queue_depth()
│   ├── task.py                  + TaskState.QUEUED, cancel-while-queued,
│   │                             `bound` on the failed chunk
│   ├── task_manager.py          create_task() submits instead of starting
│   ├── errors.py                + AdvancedUnavailableError, QueueFullError,
│   │                             AttachmentRefusedError, AttachmentNotFoundError
│   ├── events.py                + 5 incoming, 4 outgoing attachment types
│   └── runtime.py               parsing, admission, attachment handlers,
│                                 outcome mapping, tasks.list(scope)
├── database\
│   ├── migrations.py            SCHEMA_VERSION 4 → 5
│   ├── attachment_repository.py + insert / delete / set_mode / fetch_by_source
│   ├── session_repository.py    writes the three new columns
│   └── task_repository.py       LIVE_STATUSES gained "queued"
├── managers\message.py          + Outcome enum, outcome/interrupted/error_code
├── streaming\
│   ├── accumulator.py           finalize() takes invocation + outcome
│   └── manager.py               + register_invocation / invocation_for
└── tests\
    ├── manual_b12_verify.ps1    NEW — the whole gate, one script
    ├── manual_b12_concurrency.py NEW — cap + queue, one backend, exit code
    └── manual_b12_parser.py     NEW — the routing rules, no I/O, exit code
```

`managers\tool.py` and `managers\provider.py` remain deleted. Nothing here
recreates them.

---

## Migration v5 — real columns, not JSON

```sql
ALTER TABLE messages ADD COLUMN interrupted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN outcome     TEXT;
ALTER TABLE messages ADD COLUMN error_code  TEXT;
```

Columns rather than keys in the existing `metadata` blob, because everything
about how a reply *ended* is what a reloaded session has to re-render — a
footer, a collapsed slot, an interrupted marker — and a value buried in JSON
cannot be filtered, indexed, or read by a `sqlite3` one-liner during triage.
F5 / S11b reconstructs from these rows.

`interrupted` already existed as `metadata["interrupted"]` (B5). It was
**promoted, not duplicated**: `StreamAccumulator` no longer writes it to the
dict. Two writers for one fact is how the two copies end up disagreeing.

`outcome` is `NULL` **only** on rows written before this migration. That is the
honest value for "this predates the marker", which is deliberately not the same
claim as `done` — `SessionRepository._row_to_message()` leaves it None rather
than defaulting.

### The `Outcome` vocabulary — `managers\message.py`

```
done | cancelled | failed | timed_out | bounds_exceeded
```

All five have real producers. `_outcome_for(kind, code, bound)` in
`runtime\runtime.py` is the single mapping point, and anything unrecognised
falls through to `failed` — a new slug nobody has mapped is a failure whose kind
we do not know, and guessing `done` is the one answer that is definitely wrong.

### `timed_out` vs `bounds_exceeded` — the `bound` attribute

Both arrive as `BoundsExceededError` with `code="bounds_exceeded"`, because B10
raises the same class for `max_steps`, `max_tool_calls` **and** `timeout_s`. A
single code cannot separate them, and they mean different things to whoever
reads the transcript later: `timed_out` says "this took too long", which invites
re-running it; `bounds_exceeded` says "this agent was allowed N steps and used
them", which invites raising the bound.

`Task._run()` therefore copies the exception's `.bound` attribute out beside
`.code`, exactly as B10 established for `.code` itself — Task still knows
nothing about tools or bounds, it copies an attribute if it is there and leaves
it `None` if it is not. `bound == "timeout_s"` → `timed_out`; anything else →
`bounds_exceeded`. Both also ride out on the `TaskFailed` payload as `outcome`,
so a live client and a reloaded one render the identical footer without either
deriving it independently.

`.bound` and `.limit` were noted in B10's handoff as "not on the wire". Half of
that is now closed: `bound` reaches the persisted row and the payload's
`outcome`; `limit` still does not.

---

## Error payloads now carry `code`

`Runtime.receive()`'s `RuntimeException` handler emitted `{"error": str(exc)}`
and nothing else. **`queue_full` is unusable that way** — the UI locks its input
box on it and has to say why, and a client that regexes an English sentence to
decide that is a client coupled to the sentence. The payload is now:

```json
{"error": "...", "code": "queue_full", "offer": null}
```

`code` comes from `getattr(exc, "code", None)`, so every `RuntimeException`
subclass carrying a slug exposes it and the ones that do not send `null`,
exactly as before. `offer` is B12's addition for refusals that come with a
fallback (see Attachments).

This is a **stopgap in B17's direction, not B17**. B17 folds every
`RuntimeException` into `PointError(code=...)`; this only stops the four new
B12 errors from being unreadable in the meantime. B10's recorded asymmetry —
`tools_unsupported` arriving as an `Error` with no `code` — is now partially
closed as a side effect: it has a `code` class attribute, so it now surfaces one.

### The four new error codes

| code | raised when |
|---|---|
| `advanced_unavailable` | `--advanced` was parsed and refused |
| `queue_full` | the session's turn queue is at `1 + chat.queue_depth` |
| `attachment_refused` | any attachment guard fired; carries `reason` + `offer` |
| `attachment_not_found` | detach/set_mode named an id not in this session |

---

## Input parsing — `managers\invocation.py`

```
plain text            -> TURN, inline, the session's current agent
@agent rest           -> TASK, agent resolved by name
@path\to\file         -> AttachmentRef
@C:\... @~\... @..\   -> AttachmentRef, filesystem=True
@agent --advanced     -> parsed, `advanced=True`, REFUSED by the caller
```

**The ambiguity rule — an exact agent name wins, otherwise it is a path — is
decided in one place**, so the CLI and F8's picker cannot disagree. Before B12
the CLI owned a private copy (`cli.py:_strip_agent_prefix`, now deleted) that
returned "the rest of the string", which works for exactly one case and has
nowhere to put a second.

`parse_invocation()` does **no I/O**. It takes `is_agent(token)` and
`is_indexed_path(token)` as predicates, which is what makes the routing rule
testable against a set literal and stops it behaving differently under load.
`Runtime._parse_invocation()` fetches the agent names once and builds the
predicate, matching `AgentRegistry.resolve()`'s own order (id first, then
case-insensitive name).

Decisions worth not re-litigating:

- **Only the FIRST `@`-token can name the agent.** A later `@agent` is prose
  about an agent; there is no such thing as running one message on two agents.
- **Attachment tokens stay in the text.** Only the leading agent token is
  stripped, mirroring what the CLI already did. `@main.py what does this do`
  reads as a sentence; deleting the noun to leave "what does this do" is a worse
  message than the one the user wrote.
- **Both separators.** `\` and `/` both parse; `resolve_path()` normalizes
  anyway and refusing one would be a rule that exists nowhere else.
- **Trailing punctuation is stripped, trailing separators are not.**
  `@research,` names `research`; `@some/dir\` keeps its `\` because that is how
  a user says "directory", which the guard refuses *with a listing offer*.
- **`--advanced` must stand alone as a token.** `--advanced-mode` is not it, and
  neither is the word in prose.

`--advanced` is recorded on the `Invocation` and refused at the boundary by
`Runtime._handle_send_message`, **before a task exists**. The day the capability
lands, the parser does not change at all.

Routing now happens in the backend, not the client. An explicit
`payload["agent"]` still wins over a parsed one — that is a picker that already
resolved a choice, and re-deriving it from text it may have rewritten would be
second-guessing it.

---

## Attachments

### The guards, in order, and why that order

1. **path guard** — a path we are not allowed to touch must not be `stat`ed
2. **existence**
3. **directory**, before size — a directory's `st_size` is meaningless, and
   refusing it as "too large" is a nonsense answer to a sensible mistake
4. **binary**, before size
5. **size**

Each returns a `reason` slug (`path_denied`, `not_found`, `is_directory`,
`binary`, `too_large`, `unreadable`, `bad_mode`) and, where one exists, an
`offer`. Never a traceback.

### Decided: a file that is both binary and oversized reports `binary`

Surfaced by Aryan during manual testing. A 40MB file created with
`[System.IO.File]::SetLength()` is **sparse — all null bytes** — so it refuses
as binary, not as oversized. The consequence is general: a large binary always
reports `binary` and never `too_large`.

**That is intended.** `binary` is the reason that stays true: a binary file is
refused whatever its size, so letting size win would explain the same mistake
two different ways depending on how much of it there is. And the oversize
explanation is the useless half of the pair — its entire value is the
first-200-lines offer, which a file with no lines cannot take. `too_large`
remains fully reachable for the case it is actually about: a large **text**
file, which is what a 40MB log is. Recorded in `resolve()`'s docstring so it
does not read as emergent.

### The estimate is the builder's own computation

`resolve()` builds a throwaway `Attachment` with NULL recorded stats — which is
exactly what a not-yet-sent attachment looks like — and calls
`managers\context.py:_render_attachment()` on it, then `estimate_tokens()` on
the text that comes back. Deliberately **not** a parallel `size/4`: the number
the UI shows and the number the turn actually spends must come from the same
code, or the estimate is a second implementation that drifts, and the one that
drifts is always the one the user was shown.

Verified: attach reported 713, `context report` reported 713.

**`context report` must be read BEFORE sending.** It projects the *next* turn.
After a send the attachment correctly reports 0 — "unchanged since last sent"
is B8 refusing to pay for a file already in the history verbatim. The episode's
step 6 had the two in the wrong order.

### The offers are real

`AttachmentManager.preview()` honours both: the first 200 lines of an oversized
file, or the first 20 entries of a directory. It resolves through the same guard,
so "offer a preview" never becomes a way around the path rules. A refusal that
offers a fallback nobody can accept is a refusal with a decoration on it.

The `offer` text now travels on the `Error` payload and the CLI prints it
verbatim rather than a hint written in `cli.py` — which had already drifted once,
still advertising an `--allow-outside` flag that had been removed.

### attach / detach / set_mode

- `attach` is **idempotent per (session, resolved path)**. Re-attaching updates
  the mode instead of adding a second row; two rows would put the file into
  every future request twice.
- `set_mode` **clears the recorded `(mtime, size)` pair**. A correctness
  requirement, not an optimization: a row switched `full` → `reference` → `full`
  would otherwise still carry the stat from its last full send, so the builder
  would believe the body is already in history and send nothing, forever.
- `detach` deletes the **row**, and that is not a contradiction of "detach never
  deletes". What it must never delete is the FILE and any task result that
  quoted it — verified: attachments 1→0, `messages` and `tasks` counts
  unchanged, file intact. `detached` as a *mode* ("keep the binding, stop
  spending tokens") remains reachable through `set_mode`; the two answer
  different questions and collapsing them would leave no way to say the second.
- `mode` defaults to the agent's `default_attach_mode`, overridable per
  invocation. **B9 put that field on `AgentConfig` and nothing had ever read
  it**; `Runtime._default_attach_mode()` is the first reader.

### Wire surface

Incoming: `AttachContext`, `DetachContext`, `SetAttachmentMode`,
`ListAttachments`, `ResolveAttachment`.
Outgoing: `Attachment`, `AttachmentList`, `AttachmentDetached`,
`AttachmentResolution`.

`ResolveAttachment` guards and prices a path **without attaching it** — that is
what lets a picker show the cost before the user commits, since "attach it and
see" is not an acceptable way to discover that a file is 40MB.
`AttachmentDetached` states `file_deleted: false` explicitly rather than
implying it; a client should not have to take that guarantee on trust from a
docstring it cannot read.

CLI: `attach <path> [--mode] [--preview] [--session]`, `detach <id>`,
`set-mode <id> <mode>`, `attachments list`. `--session` defaults to the most
recently updated session.

---

## `QUEUED`, the scheduler, and why the cap is per-process

### `TaskState.QUEUED` is new — `runtime\task.py`

`PENDING` and `QUEUED` are **not merged**. `PENDING` is the sub-millisecond gap
between construction and the event loop getting a turn — nobody waits on it and
nothing shows it. `QUEUED` is a real, user-visible, possibly-minutes-long wait
the UI must display and the user must be able to cancel out of. Collapsing them
would make "is this thing actually running?" unanswerable, which is the exact
dishonesty this episode exists to remove.

Consequences, all of which had to be tracked down rather than assumed:

- **`Task.is_active()` counts it.** Otherwise `active_task_count` under-reports,
  `sweep_finished` can drop a task that never ran, and shutdown skips
  cancelling it.
- **`LIVE_STATUSES` in `task_repository.py` gained `"queued"`.** A queued task
  never started, so nothing will ever move it out of `queued` on its own —
  without this a crash leaves rows claiming to be waiting for a scheduler that
  no longer exists, and `tasks list` reports them as pending work forever.
  Verified: crash recovery leaves 0 running / 0 queued.
- **`Task.cancel()` gained a branch for it.** `_run()` never executes for a
  queued task, so nothing else would ever write its terminal state or emit its
  terminal event; both now happen in `cancel()`, via a new `_emit_soon()` that
  mirrors the existing `_record_soon()` fire-and-forget shape. Verified:
  **exactly one** `TaskCancelled`.

### `TaskScheduler` — `runtime\scheduler.py`

Two lanes. A task waits for a slot in **every** lane it names:

```
("provider", "ollama")   capacity: [limits.ollama] → [limits] → 3
("turn", <session_id>)   capacity: 1, inline conversational turns only
```

That asymmetry **is** "agent tasks never enter this queue", expressed as the one
line in `AgentExecutor.run()` that decides it: every task takes a provider lane,
only an inline turn also takes the session's turn lane. Three background research
runs can saturate Ollama without costing the user the ability to ask a question,
and a slow question cannot jump ahead of research already waiting. The `echo`
provider takes no provider lane at all — a local dev path with no resource to
contend for, and capping it would make the mock slower than the real thing.

**First-fit, not strict FIFO.** The queue is scanned in submission order and the
first task whose lanes are *all* free starts. Order is preserved *within* a lane,
which is the only place it is meaningful, while a chat turn blocked on its
session's turn lane does not stall an unrelated research task behind it. Strict
FIFO across lanes is head-of-line blocking between things that share no resource.

`_pump()` runs to a fixed point rather than starting one task per release:
freeing a provider slot can unblock a task that also needed an already-free turn
slot, and stopping after the first start would leave the second waiting on an
event that may never come.

Slots are held until `Task.wait()` returns, not until `cancel()` is called —
releasing earlier would let the next task start while the previous one is still
writing its terminal state, which is how a cap of 1 ends up with two tasks
briefly overlapping.

### Why per-process is correct, not a shortcut

`lockfile.py` makes the backend **single-instance**, so one process owns every
task in the app. A second `cli.py` is a second *process* by design — WAL exists
precisely so it can read and write alongside a live backend — and it is not
covered by this cap. Driving three sends through three CLI processes bypasses
the scheduler entirely and *will* show three RUNNING; that is a property of the
test, not a hole in the cap.

The alternative, a DB-backed lease, means inventing lease expiry and crash
recovery for a distributed lock the product has no use for. Rejected, with
Aryan's approval, and the gate rewritten to drive one backend
(`tests\manual_b12_concurrency.py`).

**Known limitation this leaves:** `tasks list --scope active` from a fresh
`cli.py` is always empty, because `active` means live objects in *this* process.
The durable rows are what survive a process, so `--scope all` is what a CLI
should read. Inside the real single backend — where F8 reads this list — `active`
is live and correct.

### The turn queue

One turn in flight per session (`TURN_LANE_CAPACITY = 1`, not configurable —
two replies streaming into one transcript is not a thing the transcript can
represent), plus `chat.queue_depth` waiting (default 1). Beyond that,
`QueueFullError` at the `SendMessage` boundary, **before a task exists**, so the
refusal is still a clean answer to the request rather than a task that dies
immediately.

A rejection, not a silent drop: a rejected send leaves the user holding text
they can resend, while a dropped one leaves them believing they asked something
they did not.

---

## Provenance and question-to-answer pairing

### Every persisted assistant message names its agent

`StreamManager` gained `register_invocation(task_id, ...)` / `invocation_for()`.
Registered **per task, not per stream**, because the echo and refusal paths
persist an assistant message without ever registering a stream — and "which
agent produced this" must have an answer on those rows too, or provenance is
lost for exactly the replies that are hardest to explain after a reload. Cleared
by `close_stream()`, which both terminal paths already call.

A plain turn records the **session's current agent**, never null. B12's own rule
is that every message routes through an agent config, plain chat included, so
null would not mean "no agent" — it would mean "we did not write it down", and
F5 cannot rebuild a slot from a null.

`InvocationMetadata` has existed since B3 and `model.go`'s `wireInvocation`
since F2. **Nothing wrote it until now.**

### `task_id` on user messages

`agents\executor.py` built the `UserMessage` before generating `task_id` (the
two lines were the other way round), so a user message never carried one and
only the assistant reply did. The order is reversed and both now carry it.

This makes answer-to-question pairing possible on reload — a session with three
questions and three answers had no stored link saying which answered which, and
ordering is not a link the moment anything runs concurrently, which is the
entire premise of this runtime. It is also the prerequisite for ever telling a
retry from a genuine repeat: same text, same session, and only the task id says
whether the user asked twice or the app ran it twice.

Verified: `user_without_task = 0`, `paired = 2`.

---

## Manual Test Log (Episode 12)

`tests\manual_b12_verify.ps1` runs the whole gate in one script against a
scratch profile. Two helpers exit non-zero on failure so the gate branches on
exit codes rather than on a human reading output.

| Done-when | Result |
|---|---|
| #1 `@research` background + plain turn inline, independently | ✅ background task returns immediately; plain turn streams and completes alongside it |
| #2 Ollama capped at 1: three `@research` | ✅ 1 running / 2 queued, and `ListTasks` reports exactly that |
| #3 turn queue at depth 1 | ✅ second queues, third rejected with `queue_full` |
| #4 40MB file refused naming the limit | ✅ `exceeds the 100KB attachment limit (attachments.max_kb)` |
| #5 binary + directory refused | ✅ 512-byte binary → `binary` (proves binary-before-size); directory → `is_directory` with a takeable listing |
| #6 estimate within ~20% | ✅ exact: 713 estimate, 713 spent |
| #7 detach removes only the binding | ✅ attachments 1→0, messages/tasks unchanged, file intact |
| #8 cancel mid-stream | ✅ partial text persisted, `interrupted=1`, exactly one terminal event |
| #9 provenance on every assistant row | ✅ research reply → `research`, plain reply → `general` |
| #10 terminal outcome recorded | ✅ `bounds_exceeded`, `timed_out`, `cancelled` all with `interrupted=1` and non-zero content; clean finishes record `done` |

Plus: `--advanced` refused with `advanced_unavailable`; queued turns cancellable
before they start with exactly one terminal event; parser rules all green;
crash recovery leaves 0 running / 0 queued.

### Deterministic repros for #10

`--max-steps 1` on a live model is **not** a reliable `bounds_exceeded` repro
and was briefly reported as a bug. It is not one — see below.

```powershell
# bounds_exceeded, deterministic (the fixture's step 1 IS a tool call)
$env:POINT_MOCK = "1"
uv run python cli.py send "@research x" --script two-tool-calls --max-steps 1 --wait

# timed_out, deterministic with a real model
uv run python cli.py send "@research anything" --max-seconds 1 --wait
```

---

## Bugs caught during manual testing

**1. `attach` refused every file with "no project is open", while standing
inside a real project.** Aryan's repro: `project init`, then `session new`, then
`attach <file inside that project>` → refused. This blocked Done-when #4, #5, #6
and #7 outright, because path resolution failed before the size / binary /
directory guards or the estimate could ever run.

**Root cause: `session.project_id` was write-once at `CreateSession`, and
nothing was setting it.** Every attachment is guarded against the *session's*
project, so `attach` was asking the right question of a session that had no
answer. `session new --project <uuid>` existed (B10 added it for `read_file`)
but was opt-in and undiscoverable, and B7's wording — "no project is open" —
reads as a fact about the *directory*, which made a session problem look like a
filesystem one.

**Fix, in three parts:**
- `session new` resolves the project from `--path` (default cwd), the same walk
  `project resolve` does, and binds it. `--project` still wins; `--no-project`
  opts out.
- `attach` **adopts** a project for an unbound session from the attached file's
  own location, once, and logs it. It only ever fills a NULL — a session already
  bound is never re-pointed by attaching a file, because that would silently
  move the root every later tool call is guarded against, which is a security
  boundary, not a convenience. Verified: attaching from a second project to a
  bound session is refused, `project_id` unchanged.
- `SessionManager.set_project()`, the counterpart to `set_provider()`.

**Why the guard still reads `session.project_id` rather than resolving from
cwd.** This is the important half. `managers\context.py` resolves every
attachment against `SessionMemoryService.project()` — i.e. `session.project_id`
— and the config key `project.allow_outside_project`, at **send** time. A
cwd-resolved guard would answer a different question from the one the builder
later asks, and that drift is exactly bug #2 below: it is what let `attach`
report a confident **~718 tokens** for a file that then contributed **zero**.
So the fix gives the *session* a project; it does not give `attach` a different
question. `Runtime._attachment_scope()` is the single place that answers "what
will the builder use", and both `attach` and `resolve` go through it.

The refusal now names which of the two problems it actually is — "this session
is not bound to a project, and the file is not inside one either…" vs "the file
is outside this session's project (`<root>`)…" — and carries that text over the
wire as `offer`.

**2. The token estimate was a promise the builder did not keep** (found during
self-testing, before the gate). The first implementation guarded with a
caller-supplied project and an `--allow-outside` flag. The builder uses neither.
Attaching an absolute path reported ~718 tokens and spent 0, because at send
time the same path was refused by the guard under the session's real scope.
**Fix:** `_attachment_scope()`, as above, and `--allow-outside` removed from
`attach` entirely — a per-command override is precisely what recreates the lie.
An out-of-scope path is now refused at attach time, where the user can still do
something about it.

**3. Guard ordering put `binary` after `size`, producing an unusable offer.**
`notepad.exe` is 200KB, so the size guard fired first and offered "the first 200
lines" — an offer that cannot be taken for a file with no lines. **Fix:** binary
before size, at the cost of a bounded 8KB read even on a 40MB file. See
"Decided: a file that is both binary and oversized reports `binary`" for the
consequence Aryan surfaced and the ruling on it.

**4. `--max-steps 1` reported as not bounding the run — NOT a bug.**
Investigated and closed with evidence: `--max-steps 1` prints `[progress: step
1/1]` and the same send without the flag prints `[progress: step 1/8]`
(research's configured `max_steps`), so the bound reaches the executor on the
`--wait` path. Both runs returned `done` because qwen2.5:1.5b answered on step
one **without calling a tool** — the loop returned normally, which is correct.
`max_steps` bounds model turns; it does not force the model to use them.
`BoundsExceededError` is raised only when the loop exhausts its steps still
holding a tool call. There is no deterministic `bounds_exceeded` with a live
model, because it requires the model to choose a tool — that is a property of
models, not of the bound.

**5. Reported and retracted: background `@research` persisting an empty
invocation.** Caused by querying before the write landed. Confirmed correct with
`--wait`. The follow-up question — whether a *cancelled* background task also
persists its invocation — was tested directly and does, when it streamed any
text: `outcome=cancelled, interrupted=True, agent=research`. The zero-text case
is the known limitation below.

---

## Known limitation — accepted, not closed

**A task cancelled or failed before its first token persists no assistant row at
all, so its outcome lives on the `tasks` row only.**

`StreamAccumulator.finalize_interrupted()` returns `None` for empty text,
because an empty `AssistantMessage` fails B3's `Message.validate()`. Reproduced
deterministically with `--max-seconds 1` against a live model: the `tasks` row
records `failed / bounds_exceeded / research` and there is no message row.

**Aryan's decision: accept it.** Relaxing B3's validation to allow an
empty-content message, or fabricating placeholder text, both cost more than they
buy.

**The data is recoverable, just not from a single row.** The user message now
carries `task_id` (this episode's own change), and the `tasks` row carries
`status`, `error_code` and `agent_id`. **F5 / S11b (`tasks/frontend/S11b-b12-verify-f5-pairing.md`) reconstructs the
footer by joining the user message's `task_id` to the tasks row.** Everything
needed is present; it is a two-table join rather than the single-row guarantee
the rest of Done-when #10 provides.

---

## What the next episode inherits

**The v1 backend is complete.** Input routes correctly, attachments resolve with
guards and visible costs, concurrency is honest, cancellation is clean, and every
persisted message carries its provenance and its terminal outcome.

- **Branch on `payload["code"]`, never on `payload["error"]`.** Now true for
  `Error` envelopes as well as `TaskFailed` — the four new codes are
  `advanced_unavailable`, `queue_full`, `attachment_refused`,
  `attachment_not_found`. `attachment_refused` also carries `reason` (a slug)
  and `offer` (text to show verbatim).
- **`TaskFailed` payloads gained `outcome`**, the same value persisted on the
  message. A live client and a reloaded one must render the identical footer;
  neither should derive it independently.
- **`tasks list --scope active` is in-process only.** From a fresh `cli.py` it is
  always empty. Use `--scope all` from a CLI; `active` is correct inside the
  single backend, which is where F8 reads it.
- **The concurrency cap does not span processes, by design.** Any future test of
  it must drive one backend. `tests\manual_b12_concurrency.py` is the pattern.
- **`AttachmentRef`s parsed out of typed input are not auto-attached.**
  `parse_invocation()` records them on the `Invocation`; nothing consumes that
  list yet. F8's picker is what turns `@file.py` in the input box into an
  `AttachContext` call. The parse side is done; the wiring is not.
- **`is_indexed_path` is never supplied.** `parse_invocation()` accepts the
  predicate so `@makefile` (no separator, no extension) can resolve through B7's
  index, and `Runtime._parse_invocation()` passes `None`. Only path-*shaped*
  tokens become attachments today — the correct conservative answer, and the
  hook F8 fills.
- **`source_type` is still only `file`.** B8 reserved the column for project
  index ids, URLs and prior task output; B12 added none of them.
  `_render_attachment` still returns "unsupported source_type (B12)" for
  anything else, and that string is now slightly stale.
- **Memory search for the picker's memory section is untouched** — B13's, as the
  episode's Do NOT says. `SessionMemoryService.search/propose/forget` still
  raise `NotImplementedError`.
- **`--advanced` is parsed and refused.** The `Invocation` already records it;
  implementing the capability changes nothing in the parser.
- **`BoundsExceededError.limit` is still not on the wire.** `bound` now is, via
  the persisted `outcome`.

### Schema numbering

**B12 landed as v5.** `SCHEMA_VERSION = 5`, migration
`"message terminal outcome: interrupted, outcome, error_code"`.

**B18 therefore becomes v6.** `tasks/backend/B18-message-pagination.md` says
`SCHEMA_VERSION → 5` in three places — Build step 1, Done-when #7, and the
migrate line in its verification gate. Aryan is updating that doc at close.

### ⚠ Checkpoint decision — **6 weeks: do everything**

Recorded here because every session from this point is written assuming a choice
was made, and future-you should not have to reconstruct which.

**Aryan chose the ~6-week path: build everything. B16 (SSH) is NOT dropped.**
The ~3-week alternative — B15, then the frontend, then B13, dropping B16 — was
not taken.

### F5 re-verification, after this episode's own gate

B12's handoff calls for a short pass on F5, which was built against B11 and
whose task slots and footers were live-session-only until these rows existed:

- A `@research` run must survive `Ctrl+Q` + restart as a **collapsed slot**, not
  a plain assistant message. `o` expands it.
- A failed / timed-out / bounds-stopped answer must reload **with its footer
  intact**.

Run that separately, not as part of B12's gate. If a slot is still wrong once
the rows are correct, the defect is in F5's rendering — this episode's gate has
already proved the data side.

**No new dependencies.**
