# Episode B21 — Orphaned Messages Must Not Enter the Next Prompt ✅

**A task that fails before its first token persists a user message with no
assistant row to answer it (B12's deliberate choice for the transcript).
`context.py`'s history window used to hand that unanswered question to the
NEXT unrelated send, and the model answered it instead of — or as well as —
whatever was actually asked. `build()` now excludes a user message when its
`task_id` maps to a task that ended `failed`/`cancelled` with no assistant
reply anywhere in the fetched window, on the exact same `task_id` join S11b
already built for transcript footers. Every exclusion is named in
`ContextLog.evicted`, never dropped silently.**

> Verified by Aryan on 2026-09-24 against
> `tasks/backend/B21-orphaned-message-context.md`'s six "Done when" criteria,
> on a scratch profile (`$TEMP\point-b21\runtime.db`). **No ambiguity in the
> episode doc required stopping before writing code** — S11b's join and
> B12's `LIVE_STATUSES` complement were both exactly as the task doc
> described them.
>
> **The gate did not pass on the first report, and every follow-up finding
> was chased to ground truth rather than accepted on inference — this is
> the part worth reading in full.** Four rounds:
>
> 1. **First pass looked clean but proved nothing.** Every `send` in the
>    initial verification run landed in a *different* session — a stray
>    `--title` flag (doesn't exist; `session new`'s title is positional)
>    silently fell through to auto-creating a new session for the very
>    first repro. Caught by Aryan re-deriving session membership from a
>    direct, session-scoped SQL query rather than trusting the transcript's
>    printed lines. Re-run correctly, confirmed by the same technique: all
>    messages for the sleep/oceans repro, the France/population follow-up,
>    and the Zorbium-9 check below sit under one `session_id`, in one
>    ascending `seq`, with no assistant row for the failed task anywhere in
>    it.
> 2. **The "partial output kept" check (Done-when #3) needed a technique
>    that didn't exist yet**, because no reliable mid-stream cutoff could be
>    forced against a local Ollama model in this environment — it is either
>    too fast (completes before any timeout fires) or hasn't emitted a
>    token yet (pure cold start); every `--cancel-after`/`--max-seconds`/
>    `--max-steps` combination tried landed on one of the two extremes.
>    Solved with the **Zorbium-9 injected-fact technique** (below) — proves
>    context assembly moved real data, not just that a code path was taken.
> 3. **The in-flight check (Done-when #4) surfaced a real, separate finding**:
>    `tasks list` immediately after a backgrounded `send` showed
>    `created == finished` inside the same second, every time, in two
>    different fresh terminals. Traced to ground truth in the source, not
>    argued from timing — see "CLI UX gap found along the way" below. Closed
>    with the **live in-flight capture technique** (below), which catches
>    the task genuinely `running` inside the same process, before its own
>    teardown cancels it.
> 4. **`manual_context_test.py`'s `stm_window` check briefly looked like a
>    regression** — the two policies (`stm_window=2` and `stm_window=50`)
>    produced identical output, a literal FAIL by the script's own stated
>    criterion. Traced to the session used for the check having only one
>    message, not to anything B21 touched — see "stm_window's undocumented
>    precondition" below. Re-run against a session with real history (23
>    messages): `stm_window=2 -> messages=3`, `stm_window=50 -> messages=18,
>    evicted=6`. Clean PASS.
>
> Every one of these was closed by direct evidence (a filtered SQL query, a
> live process inspection, or a second run with the variable controlled) —
> nothing here rests on "it should work."

---

## Where the code went

Backend only, and small: one filter added to an existing function, one
narrow read added to back it, one constant next to the one it's not a
duplicate of, and one docstring fix to an existing test's precondition.

```
backend\
├── database\
│   └── task_repository.py     + TERMINAL_FAILURE_STATUSES = ("failed",
│                                 "cancelled") — NOT the complement of
│                                 LIVE_STATUSES ("completed" is terminal
│                                 too, and doesn't belong here)
├── managers\
│   └── context.py             + SessionMemoryService.task_status(task_id)
│                                 — the `tasks` row's status, reusing S11b's
│                                 join; + MemoryService.task_status stub
│                                 (Protocol, honestly typed);
│                                 build()'s history-window step (5): the
│                                 new orphan filter, right after `window`
│                                 is fetched and before MessageHistory is
│                                 built
└── tests\
    └── manual_context_test.py  docstring only — stm_window's undocumented
                                 precondition (a session with >2 messages),
                                 found chasing the Round 4 false alarm above
```

`managers\tool.py` and `providers\provider.py` remain deleted; nothing here
touches them.

---

## The mechanism

`build()`'s step 5 fetches `window = await memory.recent_messages(...)`
exactly as before. Right after that fetch, and before `MessageHistory` is
built from it:

```python
answered_task_ids = {
    message.task_id
    for message in window
    if message.task_id and message.role == MessageRole.ASSISTANT
}
orphaned_ids: set[str] = set()
for message in window:
    if (
        message.role != MessageRole.USER
        or not message.task_id
        or message.task_id in answered_task_ids
    ):
        continue
    status = await memory.task_status(message.task_id)
    if status not in TERMINAL_FAILURE_STATUSES:
        continue
    orphaned_ids.add(message.id)
    log.evicted.append(EvictedItem(
        SECTION_HISTORY, _short(message.id), message.role.value,
        estimate_tokens(_message_text(message), request.model),
        f"orphaned: task {_short(message.task_id)} ended {status!r} "
        "with no reply",
    ))
if orphaned_ids:
    window = [m for m in window if m.id not in orphaned_ids]
```

Three things make this narrow, each one a Done-when criterion:

- **The assistant-reply check reads `window` itself, never a second
  query.** `agents\executor.py` generates `task_id`, appends the user
  message, and only then runs — so an answer, if one exists, is always
  chronologically *after* its question. If the question is in this
  suffix, its reply (if any) is too. This is the same ordering guarantee
  S11b's `pairReloaded` already leans on for the exact same join.
- **A task that failed AFTER producing output is untouched.**
  `StreamAccumulator.finalize_interrupted` (`streaming\accumulator.py`)
  always persists a real `AssistantMessage` when any text arrived,
  regardless of outcome — so that task's id lands in `answered_task_ids`
  and never reaches the `task_status` lookup at all. The exchange stays
  exactly as it is, partial text and all.
- **A genuinely in-flight task is untouched.** Its status is
  `pending`/`queued`/`running` — none of which is in
  `TERMINAL_FAILURE_STATUSES` — so the loop's `continue` fires and the
  question stays in `window`. `TERMINAL_FAILURE_STATUSES` is deliberately
  **not** `not in LIVE_STATUSES`: that complement also contains
  `completed` (a normal success, never orphaned by definition) and the
  reserved, unused `paused`/`awaiting_approval`.

`SessionMemoryService.task_status()` is one `TaskRepository.fetch_one()`
call — no new SQL in `context.py`, no second join. It sits beside
`session()`/`latest_session()` as a narrow, non-Protocol-required read, the
same way `task_messages` backs `load_task_result` alone.

---

## Two verification techniques worth keeping

Neither of these is committed as a permanent script — same call B18 made
for its own one-off (`b18_check.py`, kept out of the repo): this is
verification tooling for a single check, not product surface the episode's
Build section asked for. Recorded here so the *method* survives even though
the script doesn't.

### The Zorbium-9 injected-fact technique

**Problem it solves:** proving a real live `send` actually reads a specific
prior exchange out of context assembly — not just that a code path ran, and
not by trusting a log line — when the state that exchange came from is hard
or impossible to reproduce live (here: a task that streamed partial text
before ending in a non-`completed` terminal state, which this environment's
model speed made unreproducible on demand).

**Method:** insert a fictional fact that cannot exist anywhere else — model
weights, training data, or elsewhere in the session — directly into the
`messages`/`tasks` rows that represent the hard-to-reproduce state, then
issue a genuine `cli.py send` in the *same session* asking a question only
answerable from that fact. If the live answer contains the fact, context
assembly actually carried it forward; nothing else could have supplied it.

```sql
-- a "cancelled after partial output" exchange: a real AssistantMessage
-- row exists (interrupted=1, outcome='cancelled'), so it must NOT be
-- treated as orphaned
INSERT INTO tasks (id, session_id, agent_id, kind, status, created_at,
                    started_at, finished_at)
VALUES ('<task>', '<session>', 'general', 'chat', 'cancelled', ...);

INSERT INTO messages (id, session_id, task_id, role, content, created_at,
                       seq, interrupted, outcome)
VALUES ('<qid>', '<session>', '<task>', 'user',
        '{"type":"text","text":"Tell me about the fictional bridge
          material Zorbium-9"}', ..., 0, NULL);
INSERT INTO messages (..., role, content, ..., interrupted, outcome)
VALUES (..., 'assistant',
        '{"type":"text","text":"Zorbium-9 was invented in the year 1888
          by the fictional engineer Alaric Venn."}', ..., 1, 'cancelled');
```

Then, live: `send "What year was Zorbium-9 invented, and who invented it?"
--session <session>`. Result: **"Zorbium-9 was invented in the year 1888 by
the fictional engineer Alaric Venn."** — the fact only exists in the
directly-inserted row, so the model could only have gotten it from context
assembly actually including that exchange.

**Reuse this whenever** a data-flow claim ("X ends up in the model's
context") needs proving end-to-end and the state that feeds it is
timing-sensitive, rare, or otherwise hard to force live. A fictional,
specific, checkable fact is cheap insurance against "it happened to already
know that."

### Catching a task mid-flight, inside its own process

**Problem it solves:** verifying behavior that only exists while a task is
in a non-terminal state (`pending`/`queued`/`running`), when every `cli.py`
invocation tears down its entire `Runtime` — including cancelling every
active task — the moment its handler returns (see the CLI UX finding
below). Two separate CLI invocations can *never* observe this state: by the
time a second process's `build_runtime()` even opens a connection, the
first process's own shutdown has already cancelled whatever it started.

**Method:** don't use two CLI invocations. Drive the request through
`bootstrap.build_runtime()` and `cli.py`'s own `call()` helper directly, in
one script, and stop draining events at the same point `cmd_send` does for
a backgrounded, non-`--wait` send (`TaskStarted` with `background: true`).
In that same breath — before the script's own `finally: shutdown_runtime()`
gets a chance to cancel anything — read the *live in-memory* `Task` object
(`runtime.tasks.get(task_id)`, the actual source of truth per
`TaskManager`'s own docstring, not the `tasks` table mirror) and call
`context.build()` against the real `runtime.memory`:

```python
runtime = await bootstrap.build_runtime(single_instance=False, ...)
try:
    state = {"task_id": None}
    def on_event(e):
        if e["type"] == "TaskStarted": state["task_id"] = e["task_id"]
    def until(e):
        return e["type"] == "TaskStarted" and bool(e["payload"].get("background"))

    await call(runtime, "SendMessage", until=until, match=lambda e: True,
               on_event=on_event, session=SESSION_ID, text="@research ...")

    live_task = runtime.tasks.get(state["task_id"])
    assert live_task.state.value in ("pending", "queued", "running")
    built = await context.build(..., runtime.memory, record=False)
    # assert the question is in built.log.included, not .evicted
finally:
    await bootstrap.shutdown_runtime(runtime)  # the real cancellation, on purpose
```

Confirmed result: `live in-memory task.state = 'running'`, `tasks table
status = 'running'`, question `included=True evicted=False`. The `finally`
then runs the same cancellation a real CLI `send` would, closing the loop
honestly instead of leaving the task dangling.

**Reuse this whenever** a future episode needs to test behavior gated on a
task's non-terminal state through this CLI — there is no other way to
observe it, and this is the shape that does.

---

## Manual Test Log (Episode B21)

| Done-when | Result |
|---|---|
| #1 exact reproduction: `@research importance of sleep` fails with zero output, then an unrelated "oceans" question gets a clean, oceans-only answer | ✅ confirmed against a genuinely shared session (see Round 1 above); no sleep content in the oceans answer |
| #2 normal multi-turn history unaffected | ✅ "What is the capital of France?" -> "Paris" -> "What is its population?" -> "Paris, the capital of France, has a population of approximately 2.1 million inhabitants." — correctly resolved "its", confirmed via direct session-scoped SQL, not just transcript output |
| #3 a task that failed AFTER producing output keeps its exchange | ✅ the Zorbium-9 technique (above) — a live follow-up answered a fact that existed only in a directly-inserted `interrupted=1, outcome='cancelled'` assistant row, proving the exchange survived in context |
| #4 a genuinely in-flight turn's user message stays included | ✅ the live in-flight capture technique (above) — real `TaskManager` object read as `state='running'` inside the same process, `context.build()` confirmed the question in `included`, not `evicted` |
| #5 `context report` names every excluded message and why | ✅ `evicted: history 8 user 4c72b1ae orphaned: task e25a4577 ended 'failed' with no reply` (and equivalent lines for every other orphan produced during verification) |
| #6 existing context tests still pass | ✅ `manual_context_test.py` — reasoning-block check passed both rounds; `stm_window` check traced to a test-setup gap (Round 4) and confirmed clean once run against a session with real history (`stm_window=2 -> 3 messages`, `stm_window=50 -> 18 messages, 6 evicted`) |

---

## CLI UX gap found along the way — not fixed here

**`cli.py send`'s own printed hint for a backgrounded task —
`"'tasks list' to check progress, or re-run with --wait to block"` — is
misleading, because the task it refers to is almost always already dead by
the time anyone could act on that hint.**

Traced to source, not inferred from timing:

- `cmd_send`'s `until()` (`cli.py`) stops draining the event stream the
  instant `TaskStarted` arrives with `background: true`, when `--wait`
  wasn't given — that's what makes a backgrounded send return immediately.
- `_with_runtime()` (`cli.py`) wraps every command in
  `try: return await handler(...) finally: await shutdown_runtime(runtime)`
  — so the moment `cmd_send` returns, teardown runs.
- `shutdown_runtime()` -> `Runtime.close()` (`runtime\runtime.py`)
  explicitly does `for task in self.tasks.list_active(): task.cancel()`.

So a backgrounded `send`'s own process cancels that same task, typically
within the same second, before control ever returns to the shell. A
follow-up `tasks list` — necessarily a *second*, separate `cli.py`
invocation — can only ever see it already `cancelled`. This is not a bug in
task lifecycle management (a CLI process legitimately cannot leave an
orphaned async task running after it exits), but the advertised workflow
("start it, go check on it later") does not exist through this CLI; it only
exists through a persistent host — the real Go frontend's long-lived
backend connection, which the CLI does not provide.

**Flagged for whoever next touches `cli.py send`'s background messaging,
not built here** — out of B21's Build section, and reasonable candidates
(reword the hint, or give the CLI an actual persistent-daemon mode) are
each a real, separate decision.

## stm_window's undocumented precondition — docstring fixed, not the test's logic

`check_stm_window` (`tests\manual_context_test.py`) calls
`memory.recent_messages(session_id, n)`, which is `messages[-n:]`. Against
a session with **2 or fewer** total messages, `[-2:]` and `[-50:]` are the
same slice — the two policies are then mathematically guaranteed to produce
identical output, regardless of whether `stm_window` is wired correctly at
all. The check's own "PASS if the two rows differ" was never wrong; running
it against a freshly created session (1 message) gives it nothing to
possibly diverge on. Confirmed by re-running against a session with 23 real
messages: clean divergence, `stm_window=2 -> 3, stm_window=50 -> 18`.

Fixed with a docstring note on the precondition (a session already needs
more than 2 messages), not a code change — the check's logic was never
wrong, and B21 does not touch `stm_window` handling in `context.py` at all.

---

## What the next episode inherits

- **The orphan filter is generic across every failure path that produces
  zero output**, not specific to `@research`: any agent whose task ends
  `failed`/`cancelled` with no assistant row loses its question from the
  next prompt, named in `context report`. Nothing here is keyed on agent
  id or kind.
- **`TERMINAL_FAILURE_STATUSES` lives in `task_repository.py`, next to
  `LIVE_STATUSES`, and is now the second thing any future status-based
  filter should reuse rather than re-deriving.** It is deliberately not
  `completed`/`paused`/`awaiting_approval` — see the comment on the
  constant for why each is excluded.
- **The two verification techniques above are reusable, not one-off.** The
  Zorbium-9 method generalizes to any "does X really flow through context
  assembly" question; the mid-flight capture method is the only way to
  test *any* future non-terminal-task behavior through this CLI, since two
  separate CLI invocations structurally cannot observe that state.
- **Two small, deliberately unbuilt follow-ups, both flagged above rather
  than fixed:** the CLI's misleading background-task hint, and (already
  applied) `manual_context_test.py`'s precondition docstring.
- **`^R`, S11b's pairing, and F6's task panel are all unaffected.** This
  episode changed what enters the NEXT prompt's history; it does not touch
  what gets persisted, what the transcript renders, or the `task_id`
  pairing contract itself.
