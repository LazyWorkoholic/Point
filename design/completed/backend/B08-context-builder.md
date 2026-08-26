# Episode 8 — Context Builder v0 ✅

**Every request's context is assembled by one function with an explicit budget
and a per-item auditable log; attachments are resolved as references and cost 0
tokens when unchanged; memory is a stub behind a real interface.**

> Verified by Aryan on 2026-08-26 against `tasks/backend/B08-context-builder.md`'s
> seven "Done when" criteria. Built and self-tested first, then re-run end to end
> by Aryan against a renamed-aside scratch profile and confirmed passing.
>
> **One change landed after that run**, before this file was finalized:
> `ContextReport.to_dict()` was serializing 8 of the dataclass's 10 fields,
> dropping `counts` and `included`. Completed on Aryan's call — F6 is the only
> consumer of `report()`, so a partial serialization is not a smaller payload, it
> is F6 quietly finishing B8's work. Purely additive to a payload nothing consumes
> yet. **The full suite was re-run afterwards** and all checks still pass; the
> contract section below is captured from the corrected shape.

---

## Resolved before building — four things the task doc assumed

All four were raised and settled with Aryan **before any code was written**. The
task doc was corrected first, then implemented against; `tasks/backend/B08-*.md`
now reads as what was actually built.

| Assumed | Reality | Decision |
|---|---|---|
| `raise PointError(context_overflow)` | `PointError` does not exist, and `runtime/errors.py` + B7's handoff both forbid building it before B17 | `ContextOverflowError(RuntimeException)`, same stopgap shape as `PathDeniedError`. B17 folds both in. |
| `cli.py attach` exists | It does not, and `tasks/backend/B12-*.md:70` owns it. Nothing in the backend writes a `context_attachments` row. | **No stub built.** The verification inserts the row with `sqlite3`. B12's surface stays untouched. |
| `build(request, agent_config, …)` | `agent_config` has no source — `managers/agent.py` is empty and B9 owns the `agents` table | Local `ContextPolicy` with only the fields the builder reads. **B9's `AgentConfig` adapts to it**, never replaces it. |
| `mode=reference` includes "a short summary" | The same doc's "Do NOT" forbids summarization, and `context_attachments` has no title/summary column | Mechanical descriptor only: basename, byte size, line count, ~200-char head, row id. Nothing generated. |

Both decisions in rows 2 and 3 were generalized into `CLAUDE.md` hard rules by
Aryan during this episode.

**`Depends on` changed from `B6` to `B6, B7` mid-episode.** Attachment resolution
goes through B7's `pathguard.resolve_path()` — see the consequence below, which
is load-bearing for B12.

---

## `runtime/errors.py` (changed)

`ContextOverflowError(RuntimeException)` — carries `needed_tokens`,
`budget_tokens`, and `subject` **as attributes**, not only inside the message
string. The frontend's status card renders "needs 100000, budget 8000"; re-parsing
that back out of a sentence is how a UI ends up coupled to error wording.

Message format, relied on by the verification:
`context_overflow: <subject> needs <n> tokens, but the entire context budget is <m> tokens`

**Stopgap.** B17 folds this and `PathDeniedError` into `PointError(code=…)`. Do
not give it a `code`/`retryable`/`detail` shape first.

## `managers/attachment.py` (NEW — not in the original spec)

`Attachment` — one `context_attachments` row as a frozen dataclass. Its own module
for the reason `Session` and `Message` have theirs: the repository imports the
type, the type imports nothing from `database\`, and `context.py` never reaches
through a repository module to name what it works with.

`recorded_stat` returns `(mtime, size)` **or None when either half is missing** —
half a pair is not a usable comparison, and treating it as one re-sends an
unchanged file forever. Constants: `MODE_FULL` / `MODE_REFERENCE` /
`MODE_DETACHED`, `SOURCE_TYPE_FILE`.

## `database/attachment_repository.py` (NEW)

Raw SQL, **read + update-the-recorded-pair only**. `fetch_for_session` (oldest
first), `fetch_one`, `update_recorded_stat`.

Deliberately incomplete: **no insert, no delete, no way to change `mode`** — B12
owns all of that. The one write lives at this layer with no manager above it
because resolving an attachment *is* recording that it was resolved; splitting
them gives you a builder that can send a file and forget it did.

## `managers/context.py` (NEW) — the episode

`async def build(request, policy, budget_tokens, memory, *, record=True) -> BuiltContext`

Async because everything it reads is behind an aiosqlite repository. It still
never touches SQLite — `memory` is the only way in, which keeps it testable with
a fake and the storage layer swappable.

**Assembly order** (fixed, and it is also the priority order):
`system → attachments → memory → summary → recent messages → task working state`.

**Two items are non-droppable and are reserved up front**, before anything else
competes: the system prompt, and this turn's own user message. Evicting either
produces a request that is not the request the user made — dropping the question
to keep an attachment about it is the wrong trade in every case. Everything else
competes and loses from the oldest backwards, **never partially truncated**: half
a message is worse than none, because the model cannot tell it was cut.

**Attachment change detection** — the highest-value behaviour here. `stat` the
file, compare `(mtime, size)` to the recorded pair:

- equal → contribute **nothing**, logged as `unchanged since last sent`. The body
  is already in history verbatim; re-sending costs the whole file every turn.
- differs, pair was NULL → full body, logged `first send`.
- differs, pair existed → full body headed `— updated since earlier`, logged
  `changed, re-sent`. Without the marker the model sees two versions of one file
  and no ordering between them.
- `reference` → mechanical descriptor, **and the pair is deliberately not
  recorded** (the body never entered history; recording it would make a later
  switch to `full` believe the file is already there and send nothing).
- `detached` → nothing.

**Reasoning-block filtering** delegates to B3/B6's existing
`MessageHistory.for_provider()`. Filtered blocks are logged as evicted with the
tag mismatch spelled out. **Never deleted** — confirmed still in `messages` after
the check.

`estimate_tokens(text, model)` is `len(text) // 4`. `model` is unused and
deliberately still in the signature so a real tokenizer does not force a
call-site sweep later. **Not measured against any real tokenizer — every number
this episode produces is an estimate, including the ones in `report`.**

`default_budget_tokens()` reads config `context.budget_tokens`, default **8000**,
falling back with a warning on a non-integer or non-positive value. Not the
model's `context_window`: `echo` is not a registered provider and unregistered
models have no `Model` row, so the common CLI path has no window to read.

**`MemoryService` Protocol** — `recent_messages`, `attachments`, `project`,
`record_attachment_sent` implemented; `search`, `summary`, `propose`, `forget`
raise `NotImplementedError`. `project` and `record_attachment_sent` are additions
to the spec's list and both are forced: the first supplies `resolve_path()`'s
containment root, the second is how "resolved" becomes "recorded" without
reaching past the repository layer.

The four refusals **raise rather than returning empty**. An empty list is
indistinguishable from "searched and found nothing", which is how a caller ends
up believing retrieval ran when none exists. `build()` catches
`NotImplementedError` at the two points where v0 is expected to have nothing and
logs those sections as unimplemented — so the absence appears in every report
instead of being inferred.

## `runtime/runtime.py` (changed) — additive change to B1/B5

Recorded in `tasks/backend/INDEX.md`'s additive-changes table.

- `self.memory = SessionMemoryService(db_conn)` on the Runtime.
- `_handle_send_message` builds the context **before persisting the user message
  and before the task exists**. Two reasons, both about failure: an oversized
  message fails as a clean `Error` rather than an opaque provider 400, and
  failing before the INSERT means a message that can never be sent never enters
  the history to poison every later build.
- `_stream_chat_body` sends `built.messages`, not `session.messages + [user_message]`.
- `_context_policy(session)` — v0 resolution from `session.settings`
  (`system_prompt`, `stm_window`), falling back to module defaults. A malformed
  `stm_window` warns and falls back rather than making a session unusable.
- `sessions.get()` no longer passes `load_messages=True` here: the builder reads
  history itself through its own `stm_window`, and loading it twice meant only
  one of the two copies was the one actually sent.

**This wiring is a placeholder — see "what the next episode inherits".**

## `cli.py` (changed)

`context report [--session ID | --last] [--json]`. Goes straight to the builder
rather than through an event: a read-only projection with no task, no stream, and
no terminal event to wait for gains nothing from the request/response machinery.
`--last` = most recently updated non-archived session.

## `tests/manual_context_test.py` (NEW)

The two checks the CLI cannot reach. `session new` has no `--stm-window` (that
knob is B9's), and on a keyless profile the only usable provider is `echo`, which
never reaches the provider layer, so no reasoning block can arrive through it.
Takes a session id; prints its own PASS/FAIL conditions.

---

## `context.report()` — the exact contract F6 renders

**F6's status card reads this directly. It breaks silently if the shape drifts.**
Everything below was captured from a live run on 2026-08-26, not written from
memory.

### Section names — fixed, always all six, always this order

```
system  ·  attachments  ·  memory  ·  summary  ·  history  ·  task_state
```

`SECTION_ORDER` in `managers/context.py` is the single source. **A section is
never omitted**, even at 0 — `memory: 0` is information, and a section that
silently disappears is indistinguishable from one that had nothing to say. In v0,
`memory`, `summary`, and `task_state` are **always 0**.

### `ContextReport` — the dataclass (10 fields)

| Field | Type | Notes |
|---|---|---|
| `session_id` | `str` | |
| `provider` | `str` | resolved name; `"-"` if the session's is NULL |
| `model` | `str` | `"-"` if NULL |
| `budget_tokens` | `int` | config `context.budget_tokens`, default 8000 |
| `sections` | `dict[str, int]` | all six keys, in `SECTION_ORDER`, token counts |
| `counts` | `dict[str, int]` | all six keys, **item** counts |
| `used_tokens` | `int` | == `sum(sections.values())` |
| `free_tokens` | `int` | == `budget_tokens - used_tokens` |
| `included` | `list[IncludedItem]` | per-item detail, in send order |
| `next_evictions` | `list[EvictedItem]` | what the next turn would drop |

`IncludedItem`: `section` · `ref` · `kind` · `tokens` · `detail` (may be `""`).
`EvictedItem`: `section` · `ref` · `kind` · `tokens` · `reason` (never empty).

`ref` is **not** a bare id — it is an 8-char id prefix for messages
(`"81ff8db9"`), and `"<8-char-id> <basename>"` for attachments
(`"33b76681 d.py"`). Do not parse it; render it.

`kind` values seen in v0: `system`, `attachment`, `memory`, `summary`,
`task_state`, `reasoning`, and any `MessageRole` value (`user`, `assistant`,
`tool`, `error`).

### `to_dict()` returns **all 10 fields** — one key per dataclass field

Key order, verbatim:

```
session_id · provider · model · budget_tokens · sections · counts
used_tokens · free_tokens · included · next_evictions
```

`--json` output on a session with one attachment too large to fit, abridged in
the middle only:

```json
{
  "session_id": "ca65f9d1-…", "provider": "echo", "model": "echo",
  "budget_tokens": 45,
  "sections": {"system": 32, "attachments": 0, "memory": 0,
               "summary": 0, "history": 5, "task_state": 0},
  "counts":   {"system": 1,  "attachments": 0, "memory": 1,
               "summary": 1,  "history": 2, "task_state": 1},
  "used_tokens": 37,
  "free_tokens": 8,
  "included": [
    {"section": "system", "ref": "system_prompt", "kind": "system",
     "tokens": 32, "detail": ""},
    {"section": "memory", "ref": "-", "kind": "memory",
     "tokens": 0, "detail": "v0: Memory search arrives in B13"},
    {"section": "history", "ref": "f566d761", "kind": "user",
     "tokens": 2, "detail": ""},
    …
  ],
  "next_evictions": [
    {"section": "attachments", "ref": "8c4f3c34 d.py", "kind": "attachment",
     "tokens": 30, "reason": "does not fit: 30 tokens, 13 free"}
  ]
}
```

**`detail` is always present on an `included` entry, `""` when there is none** —
never absent, never null. Same for `reason` on an eviction, which is never empty.

### How eviction is described

`next_evictions` is a **list, empty when nothing would be dropped** — never
`null`, never a string. Four `reason` strings exist in v0:

| Reason | When |
|---|---|
| `budget spent, oldest-first eviction` | history message past the budget |
| `does not fit: <n> tokens, <m> free` | attachment that will not fit |
| `reasoning block tagged '<x>', this request targets '<y>'` | provider mismatch |
| *(none — filtered items only)* | |

**An evicted item is not in `included` and not in `sections`.** So
`sum(sections.values()) == used_tokens` holds regardless of how much was evicted
— the card must not add evicted tokens back into the total.

### Three invariants the card may rely on

1. `sum(sections.values()) == used_tokens`, and `used_tokens + free_tokens == budget_tokens`.
2. `sum(counts.values()) == len(included)`.
3. **`report()` is side-effect free** (`record=False`). Two consecutive calls
   return byte-identical JSON, and calling it never causes the next `send` to
   skip a changed attachment. The card may poll it.

All three asserted directly against `--json` on 2026-08-26, not inferred.

`describe()` is the human rendering of the same data and is **CLI-only** — F6
should build from the dataclass or `to_dict()`, never parse `describe()`.

---

## Manual Test Log (Episode 8)

Run against a renamed-aside scratch profile; `%APPDATA%\Point` and
`%LOCALAPPDATA%\Point` restored in `finally`. Confirmed afterwards that only one
`Point` directory exists in each location.

1. **Sections and arithmetic.** `session new` + `send "hello"` + `context report`
   → all six sections present, `32 + 9 = 41`, `total + free = budget`. ✅
2. **Attachment, first send.** Report *before* the send → **51 tokens**,
   `first send`. ✅
3. **Attachment, unchanged.** Report after the send → **0**,
   `unchanged since last sent`. ✅
4. **Attachment, changed.** File rewritten → **44 tokens**, `changed, re-sent —
   updated since earlier`. ✅
5. **Reference mode.** → **34 tokens**, `reference mode: descriptor only, body
   not included`; basename, byte size, line count, head, row id, no body. ✅
6. **Overflow.** `budget_tokens = 60`, 400-char message →
   `error: context_overflow: this message needs 100 tokens, but the entire
   context budget is 60 tokens`, **exit 1**, message count stayed at 4 — nothing
   persisted. ✅
7. **`stm_window`.** Same input under 2 vs 50 → **69 vs 78 tokens**, different
   message counts. ✅
8. **Reasoning filter.** Block tagged `anthropic` → present for `anthropic`,
   absent for `openai` with `filtered_out=1`; still 1 row in `messages`
   afterwards. ✅
9. **Report idempotence** (Done-when #7). Two consecutive reports byte-identical;
   running one did not stop the next `send` re-reading a changed file. ✅
10. **JSON contract** (re-run after the `to_dict()` completion). All 10 keys
    present; `sum(sections) == used_tokens`, `used + free == budget`,
    `sum(counts) == len(included)` asserted directly against `--json`; two
    consecutive `--json` calls byte-identical; `describe()` unaffected. Checks
    1–8 re-run against the same build and unchanged. ✅

**Note on check 7/8's evidence class.** Checks 1–6 were read directly off raw
output. Checks 7 and 8 were confirmed against `manual_context_test.py`'s **own
printed PASS/FAIL conditions** — the numbers (69 ≠ 78) and booleans (True/False)
did land as expected and were visible, but the judgment is the script's, not an
independent reading. Worth knowing if that script is ever changed.

---

## Bugs caught during manual testing

**1. `report()` mutated the thing it reported on.** *(the real one)*
`report()` re-runs `build()`, and `build()` records an attachment's `(mtime,
size)` as sent. So asking "what would the next turn cost?" was itself what
stopped the next turn from re-reading a changed file — the report silently
changed its own answer. Caught because the first-send check kept printing
`unchanged since last sent` on a file that had never been sent.
**Fix:** `build(..., record=False)`; `report()` passes it. Now anything calling
`build()` to ask a question rather than to send a turn must pass it. Became
Done-when #7.

**2. Attachment paths bypassed the path guard.**
The first implementation resolved `attachment.source_id` with a bare `Path()`.
B7's contract is that `resolve_path()` is the *only* sanctioned way to turn a
string into a filesystem path anywhere in this backend, and an attachment path is
exactly the case that matters: a string from a database row that becomes a file
read. **Caught by Aryan**, not by a test.
**Fix:** resolution goes through `pathguard.resolve_path()` with the session's
project as the containment root. `PathDeniedError` is caught and logged as
`refused by path guard: …` at 0 tokens — one bad attachment must not cost the
user their message. This is what moved B7 into `Depends on`.

**3. Windows cannot run the verification step as the task doc wrote it.**
`cli.py send ("x" * 400000)` fails before Python starts — Windows caps a command
line at ~32K characters, so `uv.exe` cannot be launched at all. Worse, PowerShell
reported `$LASTEXITCODE = 0` for the failed launch (a stale value; the process
never started), which reads exactly like a passing overflow check.
**Fix:** the check lowers `context.budget_tokens` to 60 and sends 400 characters,
exercising the identical path with numbers that fit. **Any future episode testing
a size limit through `cli.py` hits the same wall.**

**4. The context log read backwards.**
History items were appended during the newest-first eviction walk, so the report
listed the conversation in reverse — unreadable against the actual transcript.
**Fix:** history entries collected separately and reversed, so the log reads in
the order the messages are actually sent. The walk is still newest-first, because
that is the eviction order.

**5. History was fetched twice per send.**
`_handle_send_message` still passed `load_messages=True` after the builder began
reading history itself, so every send pulled the full history twice — and only
one of the two copies was the one actually sent.
**Fix:** dropped the flag.

**Not a bug: the `ù` in check 4's output.** `UPDATED_MARKER` holds U+2014 EM DASH
(UTF-8 `e2 80 94`, verified). PowerShell's console decoded those bytes under a
non-UTF-8 codepage. Display artifact only — the stored and transmitted string is
correct. `[Console]::OutputEncoding = [Text.Encoding]::UTF8` renders it properly.

---

## What the next episode inherits

**B9's `AgentConfig` must adapt to `ContextPolicy`, not replace it.**
`ContextPolicy` (`system_prompt`, `stm_window`, `default_attach_mode`, `effort`)
is the narrow set of fields the builder actually reads. B9's `AgentConfig`
mirrors the `agents` table row for row — twenty-odd fields covering tools, step
limits, timeouts, scope permissions, none of which the builder has any business
knowing. Give it a `to_context_policy()`. **Do not widen `build()`'s input to the
full agent row**, or the context builder grows a dependency on the agent system
and stops being buildable with a two-line literal. This is now a `CLAUDE.md` hard
rule, not just this episode's preference.

**The `_handle_send_message` / `_stream_chat_body` wiring is a placeholder, and
B9 is what removes it.** B9's `AgentExecutor` owns "resolve config → build
context → create task → stream" end to end. It supersedes this exact call site.
Anything built on top of the current wiring will move. In particular
`_context_policy()` reading `session.settings` is a stand-in for an agent config
and should not survive B9.

**Attachment resolution is behind the path guard, and every session today has
`project_id = NULL`** — which makes "no project scope" the *common* path, not the
edge case. With the default `project.allow_outside_project = false`, an
attachment outside a project root is refused and logged. **B12 must decide
whether attaching a file implies binding the session to a project**; this episode
only reports the refusal. If B12 skips this, `@file` on an unowned session will
appear to work and silently contribute nothing.

**`cli.py attach` still does not exist.** B12 owns `attach` / `detach` /
`set_mode`. The resolution path is built and verified, but the only way to create
a `context_attachments` row today is direct SQL. B12 also inherits an untested
`source_type` branch: anything other than `file` is logged as
`unsupported source_type … (B12)`.

**Memory is a stub with a complete interface.** B13 fills `search`, `summary`,
`propose`, `forget`. The two v0-empty sections (`memory`, `summary`) already have
their place in the assembly order and a line in every report, so B13 changes
their values, not the shape F6 renders.

**B13 should delete `MessageHistory.get_window()`, not implement it.** It was
written as the hook for token-budget windowing and its `NotImplementedError`
still points at "Episode 7 (Memory)" — but B8 is what actually added budgeting,
in `context.build()`, and `get_window()` now has no callers at all. Implementing
it would create a second windowing path competing with the builder — precisely
the drift that keeping `for_provider()` as the single reasoning filter was meant
to avoid. `get_recent()` is likewise uncalled, though only because
`SessionMemoryService.recent_messages()` slices at the repository boundary.
Recorded in `completed/backend/INDEX.md`'s carried-forward notes.

**Task working state contributes nothing, by choice.** There is genuinely no
working state in v0 — no tool calls, no scratchpad — and inventing a marker line
would be fabricating content. B10/B11 fill it; the section already has its slot
and its report line.

**Every token number in this episode is an estimate.** `len(text) // 4`, not
measured against any tokenizer. `estimate_tokens(text, model)` is the only place
to change that, and `model` is already in the signature so no call site moves.
B14 (Usage & Limits) should not build cost accounting on these numbers without
replacing the estimator first.

**No new dependencies.**
