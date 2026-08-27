# Episode 11 — The Three Agents ✅

**Explainer, Filler, and Research now differ from `general` for structural
reasons — an `EditorContext` slot and a memory-first reference index both
render through the `task_state` section B8 reserved for exactly this, mode
(explain vs. fill) is inferred from which CLI flag was given, Research writes
capped proposals through a deliberately narrow stopgap table (not B13's real
memory system), and a real, general shutdown-ordering bug plus a real,
general mid-stream timeout gap were found and fixed along the way.**

> Verified by Aryan on 2026-08-27 against `tasks/backend/B11-three-agents.md`'s
> seven "Done when" criteria. Self-tested first end to end against throwaway
> scratch profiles under `%TEMP%` (`APPDATA`/`LOCALAPPDATA` both redirected in
> one script each time — never the live profile), with real Ollama calls
> throughout (`qwen2.5:1.5b-8k`), not mocks.
>
> **The memory-first check was not accepted on the first pass.** The initial
> verification script only proved the reference index was *offered* (a log
> line), not that it reached the model or was acted on. Aryan pushed back
> twice before signing off: first for proof the index was actually in the
> request (not just logged), then for a test design that could not be
> satisfied by the model already knowing the answer from pretraining. The
> second push surfaced a real, general bug — `timeout_s` was not enforced
> while a single generation was actively streaming — which was fixed and
> reproduced under control before this doc was written. See "Bugs caught"
> below and the three separately-graded findings under "Memory-first" for the
> full account; none of this was accepted on a first plausible-looking run.

---

## Where the code went

No new top-level package this time — B11 is entirely additive to `agents\`,
`managers\`, `database\`, `runtime\`, and `cli.py`, plus one new module:

```
backend\
├── managers\
│   ├── editor_context.py      NEW — EditorContext, MODE_EXPLAIN/MODE_FILL,
│   │                          resolve_scope() (the enclosing-scope heuristic)
│   ├── context.py             + editor_context / research_reference_index on
│   │                          ContextRequest and report(); task_state section
│   │                          now actually renders something
│   └── memory.py              FILLED — Proposal (B11's stopgap; NOT B13's
│                              memory_records), PROPOSAL_STATUS_PENDING
├── database\
│   ├── migrations.py          + v4: agent_proposals (SCHEMA_VERSION now 4)
│   └── proposal_repository.py NEW — insert / count_for_session /
│                              list_by_status / fetch_for_session; no update,
│                              no delete, no review path (B13's)
├── agents\
│   └── executor.py            + editor_context/research_reference_index on
│                              run(); session title generation
│                              (_generate_title, fire-and-forget, tracked in
│                              _background_tasks + drain_background());
│                              _mentions_other_file() (the Filler's structural
│                              one-file guard); _maybe_propose() +
│                              _parse_fact_list() (proposal extraction,
│                              gated on agent.writes_proposals — no
│                              `if agent.id == ...`); PROPOSAL_CAP_PER_SESSION
│                              = 3; _check_deadline() now called once per
│                              chunk inside the streaming loop, not only
│                              between steps
├── runtime\
│   ├── runtime.py             + `agent` and `editor_context` on SendMessage;
│                              _resolve_agent_for_send() (per-message agent
│                              override); _build_reference_index()
│                              (session-scoped, same reasoning as
│                              load_task_result); `max_steps > 1` heuristic
│                              drives both inline-vs-background and whether
│                              a reference index is built; `background` on
│                              TaskStarted's payload; close() now awaits
│                              agent_executor.drain_background() and every
│                              cancelled Task's wait() (bounded 5s) instead
│                              of a bare sleep(0)
│   └── task.py                + Task.wait() — awaits the real asyncio.Task,
│                              swallows the CancelledError back at its caller
└── cli.py                     + --file/--select/--cursor/--mode (send AND
                               context report — EditorContext is per-turn,
                               never remembered on the session); --wait
                               (send); @agent-id prefix stripping
                               (_strip_agent_prefix); memory list
                               [--status STATUS] [--limit N]
```

`agents\builtins.py` is **unchanged** — every structural difference this
episode adds (mode, editor context, memory-first, proposals) is driven by
fields the three `AgentConfig`s already had (`max_steps`, `writes_proposals`,
`reads_project`) or by data supplied per-request (`EditorContext`,
`research_reference_index`), never by a new field on the config or a branch
on which agent it is.

---

## The exact per-agent shape B12/F9 branch on

Recorded because both need it and neither should have to re-read three files
to get it. Every field below is what is actually in `agents\builtins.py` and
`database\migrations.py` today — not what the spec originally said.

| | `general` | `filler-explainer` | `research` |
|---|---|---|---|
| `tools` | `()` | `()` | `("read_file", "list_files", "web_search", "load_task_result")` |
| `default_attach_mode` | `"full"` | `"full"` | `"reference"` |
| `reads_session` | `True` | `True` | `True` |
| `reads_project` | `False` | `True` | `True` |
| `reads_global` | `False` | `False` | `False` |
| `writes_proposals` | `False` | `False` | `True` |
| `stm_window` | `20` | `20` | `20` |
| `max_steps` | `1` | `1` | `8` |
| `max_tool_calls` | `0` | `0` | `20` |
| `timeout_s` | `60` | `60` | `180` |
| `provider`/`model`/`effort` | `None`/`None`/`None` (inherit, all three) | | |

`MODE_FULL = "full"`, `MODE_REFERENCE = "reference"` (`managers\attachment.py`
constants — unchanged, just the values `default_attach_mode` actually holds).

**The `inline`-vs-background split has no field of its own** — it is derived
in `runtime.py` as `agent.max_steps > 1`, true only for `research` today. See
"What the next episode inherits" for exactly where this breaks.

---

## `managers\editor_context.py` (NEW) — the episode's own EditorContext

Mirrors F11/F15's Go `EditorContext` by name and by field — the same concept
crossing the process boundary, the same way `Tool.to_schema()` does. The
frontend's `enclosing` wins outright when supplied; `resolve_scope()` is the
fallback for everyone without a real editor yet (today, that is `cli.py`).

**No parser**, exactly as speced: nearest enclosing `def`/`class`
(indentation) or a brace-matched block (`{` header, naive character
counting — no lexer), capped at ~2000 tokens (the same 4-chars/token
approximation everywhere else in this codebase uses); past the cap, or when
nothing encloses the target line, falls back to the target ± 20 lines.

**Known failure cases, as promised in the episode's own Do-NOT-defer
instruction:**

1. **A UTF-8 BOM defeats it outright.** Found during this episode's own
   self-test, not hypothesized. Windows PowerShell 5.1's
   `Set-Content -Encoding utf8` (and Notepad's default "UTF-8" save) both
   prepend U+FEFF. Python's `Path.read_text(encoding="utf-8")` does not
   strip it, so it survives as a literal character before line 0's `def` —
   inside what `^\s*` is supposed to match, but U+FEFF is not whitespace, so
   the regex never matches. The file's first `def`/`class` was invisible to
   the heuristic; `resolve_scope()` silently fell back to the ± 20 line
   range instead of the real 3-line function. **Fixed**: one BOM is stripped
   in `managers\context.py`'s `_render_editor_context()` after decoding,
   before `splitlines()`.
2. **No parser means no real understanding of nesting.** A decorator line
   directly above a `def` is not pulled in — the upward header search stops
   at the first line with *smaller* indentation that opens a scope, and a
   decorator's indentation equals the `def`'s, not less. Not hit in
   self-testing (no decorators in the test fixture); a straightforward
   consequence of indentation-only matching, not a surprise.
3. **Brace-matching is naive character counting, not lexing.** A `{` or `}`
   inside a string literal or a comment is counted as if it opened or closed
   a real block. Not exercised (only a Python fixture was used in
   self-testing); F15 will hit this the first time someone selects inside a
   JS/Go/C-family file containing a brace character in a string.

---

## `managers\context.py` — `task_state` actually renders something

`ContextRequest` gained `editor_context: Optional[EditorContext]` and
`research_reference_index: Optional[str]` — at most one set per request in
practice (Research has no editor; Filler-Explainer builds no reference
index). `_render_task_state()` dispatches between them, falling back to the
same v0 placeholder line (`"v0: no working state until B10/B11"`) when
neither is given — `general`'s context is byte-for-byte what it was before
this episode.

**Editor-context resolution always passes `allow_outside_project=True`** —
deliberately, and asymmetric with attachments/tools. The editor already has
this exact file open in a real buffer; the flag is set by the invocation
site (F11/F15/`cli.py`), never typed by the model, so it does not sit behind
the same gate `@file` attachments and tool reads do. This is a boundary
decision, not an oversight — recorded here so a later episode doesn't
"harden" it back to the attachment gate without knowing why it was chosen.

**`task_state` now competes for budget** like attachments already did
(`tokens > remaining` → evicted, logged with a reason) — v0's fixed 0-token
placeholder is gone as a universal fact; it is only 0 when there is genuinely
nothing to show.

`report()` gained the same `editor_context` parameter for exactly one
reason: `EditorContext` is per-turn, like a `BoundsOverride`, never
persisted on the session — so `cli.py context report --last` with no `--file`
shows nothing even immediately after a `send --file` that used one. Passing
the same flags again projects what a follow-up turn at that spot would cost.
This is documented in `report()`'s own docstring, not just here.

---

## `agents\executor.py` — mode selection, the one-file guard, title generation, proposals

### Mode: inferred from CLI flags, not typed or guessed from prose

Decided with Aryan mid-episode: `--select` alone → `explain`; `--cursor`
alone → `fill`; `--mode explain|fill` always wins; both `--select` and
`--cursor` with no `--mode` is refused (`"pass --mode ... to disambiguate"`).
Implemented in `cli.py`'s `_resolve_mode()`/`_build_editor_context()` — **F15
must read this exact rule from here, not reinvent one**, resolving to the
same two `managers\editor_context.py` constants (`MODE_EXPLAIN`/`MODE_FILL`)
through the same shape even though the real editor has an actual distinct
command (Explain vs. Fill) to key off instead of a CLI flag.

### The Filler's one-file rule needed a structural backstop, not just a prompt

Self-tested against a real local model: asked to "refactor this across
main.py and utils.py," with the system prompt's refusal instruction already
in place, the model did **not** refuse — it produced a syntactically valid
JSON region that silently moved the function into `utils.py`, passing every
structural check (real JSON, real line numbers) while violating the rule it
was told to hold. Prompting alone did not satisfy Done-when #4.

**Fixed with a deterministic, model-independent guard**
(`_mentions_other_file()`), not a stronger prompt: in fill mode, the request
text is scanned for a filename-looking token (`\b\w{2,}[\w.\\/-]*\.[A-Za-z0-9]
{1,6}\b`) that is not the current file's own basename, **before the model is
ever called**. A match short-circuits to a canned refusal delivered through
the same `_echo_body` chunk shape the dev-mode `echo` provider already uses
— it renders as a normal completed reply (`TaskDone`, no `code`, nothing
error-shaped), never a model call. Verified both directions: the two-file
request now refuses deterministically every time, and a genuine one-file
request that happens to mention the current file's own name (e.g. "add a
docstring" against `main.py`) is not a false positive.
**Known false-positive risk, accepted**: any dotted token 2+ characters long
that is not the current file's name reads as "another file" — a request
mentioning an unrelated dotted identifier (a version number, a config key)
could be refused unnecessarily. Judged the right trade against a false
negative on the actual boundary this exists to hold.

### Session title generation, and the fire-and-forget task it exposed

One cheap, non-streaming `provider.generate()` call on the true first
exchange (`existing messages == []`, checked before the user message is
persisted), reusing the session's own resolved provider/model — **no "small
model" tier exists anywhere in this codebase** (`providers\models.py` has no
such flag), so "small model where available" degrades to "the session's own
model" rather than inventing a selection mechanism nothing else needs. The
`title_generated` flag is set on `session.settings` **before** the network
call, not after — two sends arriving close together can both observe "no
messages yet" (the triggering user message isn't persisted until later in
`run()`), and claiming the slot immediately is what stops both from firing.
`echo` sessions are skipped entirely (no real model to ask).

**Found and fixed during self-testing**: `asyncio.run()`'s own cleanup
(inside `cli.py`'s per-invocation process) cancels whatever is still
pending the moment its coroutine returns. A background (`max_steps > 1`)
agent's first send, without `--wait`, let the whole CLI process exit ~1-2s
later — nowhere near enough time for the title call's network round trip.
Sessions created this way (the *common* case for Research, since
`session new` → `send "@research ..."` is the natural first exchange)
stayed permanently untitled: the flag was already claimed, so nothing ever
retried. **Fixed**: `AgentExecutor` tracks its own fire-and-forget tasks in
`_background_tasks` and exposes `drain_background()` (bounded, 5s), called
from `Runtime.close()` before the database closes. Confirmed with the exact
failure case: a `--wait`-less first send to a fresh session, title present
immediately after, no extra wait needed.

### Proposals: extraction, the cap, and where it deliberately does NOT write

On a clean loop completion (never on `bounds_exceeded` or an exception —
a run that stopped short of an answer is not "complete work" to extract
durable facts from), gated on `agent_context.config.writes_proposals` (a
config flag, not `agent.id == "research"` — the one other agent this could
ever apply to would get it automatically if it ever set the flag). One
bounded (`asyncio.wait_for`, 30s — this runs *before* the task's own `done`
event, so a hang here must not become a hang in the reply the user is
already waiting on) extraction call asks the model for "at most `remaining`
short, standalone facts... reply with a JSON array of strings, or `[]`."
`_parse_fact_list()` does one salvage attempt (find `[`...`]` in the reply)
before giving up and returning nothing — this is the model's own output,
never user input, so a parse failure is "nothing worth keeping," never
raised.

**The number of proposals a given run produces is NOT deterministic, and is
not supposed to be.** `_maybe_propose()`'s extraction call explicitly asks
the model to "reply with a JSON array of strings, or `[]` if nothing is
worth keeping" — zero is a legitimate, designed outcome of that contract,
not a failure. Observed directly, both ends of the range, across separate
live sessions during this episode's testing: two successful research
completions in one session produced **zero** proposals (the extraction call
ran, the model returned nothing worth keeping); other sessions produced 1,
and separately 3. All are correct outcomes of the same mechanism — **do not
read a specific count as the expected result of a re-run.**

**What IS deterministic, and what is actually proven, is the cap: never
more than 3 per session, enforced as a `COUNT(*)` against `agent_proposals`
checked before every insert — never a prompt instruction.** Verified two
ways: bypassing the LLM entirely (a unit-style check directly against
`ProposalRepository` — inserting a 4-fact batch into an empty session
yields exactly 3 rows and `remaining` correctly reads `0` for the next
batch), and against live runs that happened to produce facts (never
observed exceeding 3, in any session, across the whole episode).

`SessionMemoryService.propose()` is **untouched**, still raising
`NotImplementedError` — proposal writes go through `_maybe_propose()` and
`ProposalRepository` directly, never through that method, so B13's Protocol
contract is not polluted with a stopgap implementation it would have to
unwind later.

### The mid-stream deadline bug — the one that blocked the gate

`_check_deadline(deadline, limits)` was called once at the top of
`for step in range(1, limits.max_steps + 1)` and **nowhere inside** the
`async for provider_chunk in stream:` loop that actually consumes a single
step's generation. Once a step started streaming, nothing interrupted it —
not `_check_deadline` (never called again until the next step), and not
`httpx`'s client timeout in `providers\ollama\provider.py` (a **per-chunk
read timeout**, reset by every token the model emits, not a cap on total
stream duration). A single generation that degenerates into an unrelated,
very long ramble — a real local-model failure mode, not a hypothetical: hit
live during this episode's own memory-first testing, where the model
spiralled into unrelated web-search-result regurgitation after a confused
tool attempt — had **no bound at all** short of the provider's own stream
ending on its own.

**Reproduced under control before fixing anything**: a prompt engineered to
produce a very long single-step answer, run with `--max-seconds 8`, streamed
for the full duration with zero interruption. **Fixed**: `_check_deadline()`
is now also called once per chunk inside the `async for` loop — a single
`time.monotonic()` comparison, negligible cost. **Reproduced again after the
fix, identical prompt, identical bound**: `TaskFailed code=bounds_exceeded —
stopped after 8s: this agent's time limit`, at exactly the configured
boundary. This is a **general** fix — it does not special-case tool-call
failures or any particular kind of runaway output; it makes `timeout_s` an
actual wall-clock cap on the whole task, the way its own name already
claimed.

**What this was not**: the transcript that surfaced this also showed four
lines that look like tool-validation errors
(`error: 'load_task_result' requires exactly 1 argument, but got 0`, etc.).
`grep -r "requires exactly" backend/` returns nothing — that phrase is not
in this codebase anywhere. Those lines, and the "One Piece filler episode"
web-search spam that followed, are the model's own hallucinated text, never
routed through `ToolExecutor` (confirmed: no `tool.started`/`tool.completed`
pair wraps them, unlike every real tool call in the same transcript). The
bounds machinery for actual tool calls was never in question — `tool_calls_
used += 1` fires for every real `ToolCall`, malformed or not, before
`ToolExecutor.execute()` even runs, so a genuinely malformed *real* call
already counted correctly. The bug was the missing wall-clock check around
a single ungoverned generation, not a hole in tool-call counting.

---

## Memory-first: three separately-graded findings, not one verdict

Aryan's explicit instruction after two rounds of pushback: keep these three
visibly separate, because they carry different confidence levels and
different owners.

### 1. PROVEN — the reference index reaches the model

Not inferred from a log line. `context.build()`'s own `built.log.included`
(the authoritative section-by-section record, not text-matching) was
inspected directly against a real session where the first research exchange
had been pushed out of `stm_window=20` by 12 seeded filler turns (24
messages, seeded directly via `SessionRepository` — test scaffolding, not a
product path, no LLM calls spent on it). Result:

```
history section item count: 21   (all filler messages + this turn's own pending message)
task_state section item count: 1  (the reference index)
```

None of the 21 history items reference the old exchange's message ids. The
full `built.messages` list, printed in order, shows the reference index as
message `[21]` — a real `system`-role message containing the old answer's
opening sentence verbatim — immediately before the new question at `[22]`.
`FOUND: True` when checked against the raw string.

### 2. PROVEN AND FIXED — see "The mid-stream deadline bug" above

Not a memory-first-specific bug — a general gap in `timeout_s` enforcement,
surfaced while testing memory-first, fixed for every agent and every
provider.

### 3. DOCUMENTED, NOT RESOLVED — whether the model acts on the index

Tried four times against `qwen2.5:1.5b`. Each attempt failed a **different**
way — this is itself the finding, not four unrelated bugs:

1. **The Filler's two-file violation** (see above) — complied with a
   malformed rewrite instead of refusing.
2. **A same-session repeat of "what is a Bloom filter"** — zero tool calls
   on *either* ask. The model never needed search or memory either time
   (Bloom filters are common pretraining knowledge), so this test could not
   distinguish "ignored the index" from "didn't need anything at all." Also
   revealed the test design flaw: the first reconstruction attempt
   accidentally queried DB state *after* the second live call had already
   completed, showing the model its own just-given answer back as if it
   were prior history — caught and corrected (see finding 1's methodology)
   before drawing any conclusion from it.
3. **The essay-spiral prompt** used to reproduce the deadline bug — an
   unprompted ramble, not a considered use (or non-use) of anything.
4. **A forced-lookup repeat** (a question about this project's own source,
   unfindable in any pretraining, so answering it *requires* a real tool
   call): the model invented a placeholder-looking path
   (`/path/to/backend/agents/executor.py`) instead of using `list_files`'
   real result, then produced two more turns of hallucinated,
   plausible-but-fake tool-error text before giving up — verified against
   the literal `tool_calls` table: exactly two real calls, both genuinely
   malformed/refused, nothing after that ever reached `ToolExecutor` again.
   It never successfully read the file, so the reference index it later
   received carried nothing to test recall against either.

**Verdict: untestable against this model, not disproven.** Finding 1 is not
in question. A parking-lot entry was added to `INDEX.md`
("Local-model capability tiers...") rather than chasing a fifth attempt with
a bigger local model in this episode — that is a model-capability question,
and answering it needs `send --provider/--model` CLI surface this episode
did not otherwise need.

---

## Manual Test Log

1. **Explainer sends the enclosing function, not one line** (Done-when #2).
   `send "@filler-explainer explain" --file main.py --select 2` against a
   3-line `def parse_config` fixture → `context report`'s `task_state`
   detail reads `"editor context: main.py mode=explain lines 1-3"` — all
   three lines, not just line 2. ✅
2. **Filler returns a region and writes nothing** (Done-when #3). `mtime`
   before/after a `send "@filler-explainer add error handling..."` —
   identical. Response: a real JSON object with `file`/`start_line`/
   `end_line`/`replacement`/`note`. ✅
3. **Two-file refusal, as a normal response** (Done-when #4). After the
   structural-guard fix: `send "@filler-explainer refactor this across
   main.py and utils.py"` → plain-text refusal naming both files, `TaskDone`,
   no `code`, not an error envelope. ✅
4. **Research runs in the background, emits step k/n, respects bounds,
   produces a persisted result plus ≤3 proposals** (Done-when #5). A
   `--wait`-less send returns in ~1-2s wall time with `background: true` on
   `TaskStarted`; `--wait` shows `[progress: step 1/8]` and streams to a
   real answer. **The proposal count is not deterministic** — observed 0,
   1, and 3 across different live sessions in this episode's testing (the
   extraction call is explicitly allowed to return `[]`, and did, at least
   once, after two genuinely successful completions). What's proven is the
   ceiling: never observed to exceed 3 in any live run, plus a separate
   LLM-free unit check of the cap arithmetic itself. Re-running this gate
   should not be expected to reproduce any specific count, including this
   doc's own examples. ✅
5. **Repeat consults memory before searching** (Done-when #6). See "Memory
   -first" finding 1 above — proven at the code level, not just logged.
   Deterministic log evidence also present: `"Session ...: agent research
   offered N past result(s) before this turn"`, an `INFO`-level line that
   fires regardless of what the model does with the offer. ✅ (wiring);
   see finding 3 for the still-open model-behaviour question.
6. **New session gets a generated title; a killed title call leaves the
   session usable** (Done-when #7). Confirmed both directions: a normal
   send produces a real title (`"Skip List Basics"`, `"Hash Table Basics"`,
   etc., never the literal first message); after the `drain_background()`
   fix, a background-early-return first send *also* gets one. Before the
   fix, the same case left a session permanently untitled but never broken
   — `session list`/`send` on it worked normally throughout, satisfying
   "a killed title call leaves the session usable" even in the buggy state;
   the fix is about *reliability*, not about a session ever becoming
   unusable. ✅
7. **Structurally different context, visible in the log** (Done-when #1).
   Cumulative across B9/B10 (tool lists, `reads_project`, `stm_window`) plus
   this episode's new evidence: `task_state` differs by agent purpose
   (editor context vs. reference index vs. the untouched v0 placeholder),
   visible in every `context report`. ✅
8. **The mid-stream deadline fix, reproduced under control** (not a
   Done-when item, but blocked the gate). Before: an engineered long-answer
   prompt at `--max-seconds 8` ran the full 8 seconds, no interruption.
   After: identical prompt, identical bound → `TaskFailed
   code=bounds_exceeded` at exactly 8s. ✅

---

## Bugs caught during manual testing

**1. A UTF-8 BOM silently defeated the enclosing-scope heuristic.** See
`managers\editor_context.py`'s section above for the mechanism.
**Fixed** in `managers\context.py`'s `_render_editor_context()`.

**2. The Filler's one-file refusal did not hold against a real model.** See
`agents\executor.py`'s section above. **Fixed** with a deterministic
pre-model guard (`_mentions_other_file()`), not a stronger prompt.

**3. A latent B1/B6-era shutdown-ordering bug, exposed (not caused) by this
episode.** `Runtime.close()` cancelled active tasks then did a bare
`asyncio.sleep(0)` before closing the database — one loop turn is nowhere
near enough for a task cancelled mid-network-I/O to finish unwinding
(`CancelledError` has to propagate up through `httpx` → `httpcore` → `anyio`,
each layer needing its own turn, then the sink's own cleanup on top of
that). Nothing before B11 exercised a task being cancelled while the process
tore down microseconds later — every prior episode's CLI usage always
drained to the terminal event first. B11's `--wait`-less background send
does exactly that by design, and the first time it ran, the cancelled
task's own `set_active_task` DB query threw `ValueError: no active
connection` instead of finishing cleanly.
**Fixed**: `Task.wait()` (new) awaits the real `asyncio.Task`, swallowing
its own `CancelledError`; `Runtime.close()` awaits every cancelled task's
`wait()` (bounded 5s) instead of `sleep(0)`.

**4. The same class of bug, for fire-and-forget work outside TaskManager
entirely.** Session title generation's `asyncio.ensure_future(...)` call was
untracked, so `asyncio.run()`'s own cleanup (inside `cli.py`'s per-invocation
process) cancelled it before its one network call could return, whenever the
first send to a session was also a background/early-return one — silently
leaving the session permanently untitled (the claiming flag is set before
the call, so nothing retries). **Fixed**: `AgentExecutor._background_tasks`
+ `drain_background()` (bounded 5s), called from the same `Runtime.close()`.

**5. `timeout_s` was not a real wall-clock cap on the whole task.** The
episode's central finding — see "The mid-stream deadline bug" above for the
full account, the reproduction, and the fix. **This is the bug that
actually blocked the gate**, not the memory-first question that led to
finding it.

---

## What the next episode inherits

**B12 (Routing, Attachments, Concurrency Caps)** inherits:
- The per-agent tool/`default_attach_mode` table above, verbatim — no need
  to re-derive it from three files.
- `@agent rest` parsing already exists, narrowly, in `cli.py`'s
  `_strip_agent_prefix()` (exact-id/name match only, no `@path`, no
  `@C:\...`) — B12's own `Invocation` parser is the real, general version;
  this one is a CLI-only convenience B12 should treat as prior art for the
  exact-match ambiguity rule, not as something to preserve or build on top
  of.
- The `max_steps > 1` inline/background heuristic (below) — if B12's turn
  queue needs to know "is this a background task" for real (not just for
  `cli.py`'s early-return UX), it should read `background` off
  `TaskStarted`'s payload, which already carries the right answer, rather
  than re-deriving `max_steps > 1` itself.

**B13 (Memory System)** inherits `agent_proposals` (migration v4),
`Proposal` in `managers\memory.py`, and `database\proposal_repository.py` —
all deliberately narrower than `memory_records`/`memory_scopes` (already in
the schema since v2, still B13's to activate). A "Read before starting" note
was added directly to `B13-memory-system.md` pointing at this doc; B13 must
decide how its real system relates to what already exists here
(migrate-and-drop, dual-write, or leave both) rather than building a second,
competing proposal path. `SessionMemoryService.propose()` is untouched and
still raises `NotImplementedError`.

**`AgentConfig` has no `background: bool` field.** `runtime.py`'s
`_handle_send_message` reads `agent.max_steps > 1` to decide both whether to
run in the background and whether to build Research's reference index. This
breaks the moment a future agent needs `max_steps > 1` while running inline,
or background execution with `max_steps == 1` — silently, no error, just the
wrong classification. Warnings are on both the "Dynamic agents" and "Coding
Assistant capability" parking-lot entries in `INDEX.md` for whichever
episode adds a new built-in or user agent first; the real fix is a
`background: bool` column (a migration).

**Whether Research's memory-first index changes model behaviour is an open
product question, not an architecture one.** The wiring is proven; the
model-compliance question needs either a more capable model or accepting
that `qwen2.5:1.5b` cannot answer it. See the new `INDEX.md` parking-lot
entry.

**`inline: bool` now has its first real branch on the caller side**
(`runtime.py`, not `agents\executor.py`, which stays free of any
`if agent.id == ...`). No episode after B9 had actually passed `False`
before this one.

**No new dependencies.**
