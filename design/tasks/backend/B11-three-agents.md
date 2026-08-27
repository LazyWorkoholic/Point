# B11 — The Three Agents

**Phase:** P1 · **Batchable:** no · **Depends on:** B10

## Purpose

Behaviour for the three configs seeded in B9. They must differ **structurally**,
not by prompt — that difference is the proof the architecture does real work.

**Build order inside this episode: Explainer → General → Research.** Explainer
exercises the whole pipeline with the fewest moving parts; Research is the
integration test for everything.

## Build

### Filler-Explainer (one agent, two modes)

`EditorContext`, supplied by the frontend (F11) or synthesized by the CLI:
```python
EditorContext(file, language, cursor, selection, enclosing, imports)
```

**Enclosing-scope extraction** (backend helper, used when the frontend does not
supply it): nearest enclosing `def` / `class` / `func` / brace block by
indentation or brace matching. **No parser.** Cap at ~2k tokens; if the enclosing
scope is larger, fall back to selection ± 20 lines. One highlighted line is
unanswerable without its function.

**Explainer mode** — `reads_project=True`, no writes, no tools. Scope ladder:
selection → enclosing → block → file. Project-wide only on explicit request, and
then as a **file list**, never contents.

**Filler mode** — returns a **rewritten region**. Never a patch, never a file
write. Output contract:
```json
{ "file": "...", "start_line": 12, "end_line": 18,
  "replacement": "...", "note": "..." }
```
Refuses, with a message, if the request would need a second file, a new file, or
a command. That refusal is the boundary against the parked Coding Assistant.

### General Chat
The null agent: no tools, no project reads, no writes, STM only. Its purpose is
to be the daily driver **and the control case** — anything it does well comes
from the Context Builder, not from agent cleverness.

### Research
- `reads_project=True`, `writes_proposals=True`, tools: `web_search`,
  `read_file`, `list_files`, `load_task_result`.
- **Memory-first:** before searching, consult accepted memory records plus a
  reference index of past Research task results, **each with its age**. The model
  then decides: answer from memory, re-research because it is stale, or search.
  `stale_after_days` is config. In v1 memory is mostly empty, so this path is
  mainly exercised by past task results — that is fine and still worth building.
- Always background. Bounded, with `task.progress` carrying `step k/n`.
- Result persisted as a task result; `default_attach_mode = reference`.
- Emits **proposals** (`status=pending`) for facts worth keeping — at most 2–3
  per session, enforced by a counter, **never** by prompt instruction.

### Session title generation
On the first exchange in a session, one cheap call (small model where available)
produces a short title, stored on the session. **Failure is non-fatal** — an
untitled session is fine.

Add CLI support: `send` must accept `--file`, `--select`, `--cursor` so Explainer
and Filler are testable before any editor exists.

## Do NOT

- Give the Filler file-write or command capability, in any form.
- Implement retrieval ranking, summarization, or promotion logic (B13).
- Add Blog Writer, Trading, or Data agents.
- Build the result-card UI (F9) — this episode ends at events and stored results.

## Done when

1. Each agent produces a structurally different context for the same input,
   visible in the B8 log (tools present/absent, project reads, window size).
2. Explainer: a one-line selection inside a function yields a context containing
   the **whole enclosing function**.
3. Filler: returns a region whose line numbers map onto the real file, and
   **writes nothing** — proven by mtime.
4. Filler: a two-file request is refused with a message naming the limitation.
5. Research: runs in the background, emits `step k/n`, respects its bounds, and
   produces a persisted result plus **at most 3** pending proposals.
6. Research: on a repeat question, the log shows it consulted memory/past results
   before searching.
7. A new session gets a generated title; a killed title call leaves the session
   usable.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

# set up a file to work on
@"
def parse_config(path):
    with open(path) as f:
        return json.load(f)
"@ | Set-Content "$env:TEMP\proj\main.py"
```

```powershell
# 2. Explainer sends the enclosing function, not one line
uv run python cli.py send "@filler-explainer explain" --file "$env:TEMP\proj\main.py" --select 2
uv run python cli.py context report --last
```
**Expect:** the context contains all three lines of `parse_config`, not just
line 2.

```powershell
# 3. Filler writes NOTHING
$before = (Get-Item "$env:TEMP\proj\main.py").LastWriteTime
uv run python cli.py send "@filler-explainer add error handling for a missing file" --file "$env:TEMP\proj\main.py" --cursor 1
$after = (Get-Item "$env:TEMP\proj\main.py").LastWriteTime
$before -eq $after
```
**Expect:** a JSON region in the response, and `True` printed.
**Fails if:** `False` — the agent wrote to disk, which must never happen.

```powershell
# 4. two-file refusal
uv run python cli.py send "@filler-explainer refactor this across main.py and utils.py" --file "$env:TEMP\proj\main.py"
```
**Expect:** an explicit refusal naming the one-file limit — as a normal response,
not an error.

```powershell
# 5. Research
uv run python cli.py send "@research what is a rope data structure" --wait
uv run python cli.py memory list --status pending
uv run python cli.py tasks list
```
**Expect:** a completed task with a stored result, `step k/n` in the event log,
and **≤3** pending proposals.

```powershell
# 6. memory-first on a repeat
uv run python cli.py send "@research what is a rope data structure" --wait
```
**Expect:** the log shows it consulted stored results first, and it either
answers from them or states they are stale.

```powershell
# 7. title
uv run python cli.py session list
```
**Expect:** the session has a short generated title, not a UUID.

## Handoff

State after this episode: three agents behave differently for structural reasons,
nothing writes to disk, and Research produces reviewable proposals that nothing
yet reviews (B13 + F11).

Record in `completed/`: the enclosing-scope heuristic's known failure cases —
you will hit them, and knowing them is worth more than pretending it is a parser.

**Notes added while building, ahead of the completed/ writeup Aryan has not yet
verified — carry all of the below into `completed/backend/B11-three-agents.md`
when that gets written, do not re-derive them:**

### The explain/fill mode-inference rule — read this exact rule, do not reinvent one

Decided with Aryan mid-episode (option (a) of the choice put to him): mode is
inferred from which flag is given, not typed or guessed from prose.

```
--select given, --cursor absent   -> explain
--cursor given, --select absent   -> fill
--mode explain|fill given         -> always wins, overrides inference
both --select and --cursor, no --mode -> refused: "pass --mode ... to disambiguate"
neither given (no --file at all)  -> no EditorContext at all
```

Implemented in `cli.py`'s `_resolve_mode()` / `_build_editor_context()` — those
two functions ARE the rule; read them rather than re-deriving it. **F15 must
read this rule from here (or from `completed/backend/B11-three-agents.md` once
written) and reuse it, not invent its own** — the real editor has an actual
distinction available (an Explain command vs. a Fill command bound to different
keys, per F15's own doc), and should still resolve to the same two mode strings
(`managers/editor_context.py`'s `MODE_EXPLAIN` / `MODE_FILL`) through the same
inference shape, so a session started from the CLI and one started from the
editor are talking about the same thing.

### `AgentConfig` has no `background: bool` field — a heuristic stands in, and it breaks

`max_steps > 1` is what `runtime.py`'s `_handle_send_message` reads to decide two
things at once: whether to run `agent_executor.run(inline=...)` in the
background, and whether to build Research's memory-first reference index
(`_build_reference_index`, gated the same way). Both live in `runtime.py`, not
`agents\executor.py`, which must stay free of any `if agent.id == ...` — a
config-derived boolean computed by the caller is not that.

**This breaks the moment a future built-in or user agent needs `max_steps > 1`
while running inline, or needs to run in the background with `max_steps == 1`.**
Nothing in v1 needs that combination (`general`/`filler-explainer` are both
`max_steps=1`+inline, `research` is the only `max_steps>1` agent and is meant to
be background) — but the day one exists, this heuristic silently misclassifies
it, with no error, just the wrong `inline`/reference-index behavior. The real
fix is a `background: bool` column on `agents` (a migration) that `AgentConfig`
carries explicitly, the same way `writes_proposals` already does for proposals.
**A matching warning has been left in `design/tasks/backend/INDEX.md`'s parking
lot, on the "Dynamic agents (user-created)" and "Coding Assistant capability"
entries** — the two places a new agent is most likely to be added — so whichever
episode does that reads this warning before hitting it, not after.

### `agent_proposals` (B11's stopgap) — confirmed pattern, not a one-off

Built now, in this episode, not deferred: a new table (migration v4,
`agent_proposals` — deliberately not `memory_records`/`memory_scopes`, which
already exist since v2 and are B13's to activate), a `Proposal` dataclass filling
the previously-empty `managers\memory.py`, `database\proposal_repository.py`
(insert / count_for_session / list_by_status / fetch_for_session — no update, no
delete, no review path), and `cli.py memory list --status pending`. Tested
against the ≤3-per-session cap directly (a unit-style check against
`ProposalRepository`, bypassing the LLM) and against two live Research
completions producing 1 and 3 proposals respectively, correctly capped.

`SessionMemoryService.propose()` is untouched and still raises
`NotImplementedError` — Research's proposal-writing goes through
`AgentExecutor._maybe_propose()` and the repository directly, never through that
method, so B13's Protocol contract is not polluted with a stopgap implementation
it would have to unwind.

This is now a confirmed pattern in this codebase (decisions.md D97), not
improvised for B11: B9's `ContextPolicy` (not `AgentConfig`), B10's
`BoundsOverride` (not a mutated `AgentConfig`), and this. **When B13 starts, it
must read this section (or the completed doc once written) and decide how its
real `memory_records`-based system adapts to what `agent_proposals` already
established — dual-write, one-time migrate-and-drop, or leave both — not build a
second, competing table.** A matching note is in `B13-memory-system.md`.

### The enclosing-scope heuristic's known failure cases (as promised above)

1. **A UTF-8 BOM defeats it outright.** Windows PowerShell 5.1's
   `Set-Content -Encoding utf8` (and Notepad's default "UTF-8" save) both
   prepend `﻿`. Python's `str.read_text(encoding="utf-8")` does not strip
   it, so it survives as a literal character at the very start of line 0's text
   — inside what `^\s*` is supposed to match before `def`/`class`, but `﻿`
   is not whitespace, so the regex never matches and the file's very first
   `def`/`class` is invisible to the heuristic. Hit during this episode's own
   self-test (the enclosing function was reported as the ± 20 line fallback
   range, not the real one, until this was found and fixed).
   **Fixed** in `managers/context.py`'s `_render_editor_context()`: one BOM is
   stripped after decoding, before `splitlines()`.
2. **No parser means no real understanding of nesting or language.** A
   decorator line immediately above a `def` is not included as part of the
   header search (the walk stops at the first line with smaller indentation
   that opens a scope, and a decorator's indentation equals the def's, not
   less) — so `@property` above a method is left out of the "enclosing
   function" shown to the model. Not hit in self-testing (the test fixture had
   none), but a straightforward consequence of the indentation-only approach
   and worth expecting.
3. **Brace-matching is naive string counting, not lexing.** A `{` or `}`
   inside a string literal or a comment is counted as if it opened or closed a
   real block. Not exercised in self-testing (only a Python fixture was used);
   flagged here because F15 will hit this the first time someone selects
   inside a JS/Go/C-family file with a brace character in a string.

### The Filler's one-file refusal needed a structural backstop, not just a prompt

Self-tested against a real local model (`qwen2.5:1.5b-8k` via Ollama): asked to
"refactor this across main.py and utils.py" with the system prompt's existing
refusal instruction in place, the model did not refuse — it produced a
syntactically valid JSON region that silently *moved* the function into
`utils.py`, passing every structural check (real JSON, real line numbers) while
violating the one-file rule the prompt asked it to hold. Prompting alone was not
enough for Done-when #4.

**Fixed with a deterministic, model-independent guard**
(`agents/executor.py`'s `_mentions_other_file()`), not a stronger prompt: in fill
mode, the request text is scanned for a filename-looking token that is not the
current file's own basename, before the model is ever called. A match short-
circuits straight to a canned refusal delivered through the same `_echo_body`
path the dev-mode `echo` provider already uses — so it renders as a normal
completed reply (`TaskDone`, no `code`, nothing error-shaped), never a model
call and never an exception. Verified both directions: the two-file request now
refuses deterministically, and a genuine one-file request that happens to
mention the current file's own name (e.g. "add a docstring" against `main.py`
while `main.py` is the file in scope) is not a false positive.
**Known false-positive risk, left as-is**: any dotted token 2+ characters long
that is not the current file's name reads as "another file" — a request that
happens to mention an unrelated dotted identifier (a version number, a
config key) could be refused unnecessarily. Judged an acceptable trade against
the alternative (a false negative on the actual boundary this exists to hold).

### A latent B1/B6-era shutdown bug, exposed by this episode (not caused by it)

`Runtime.close()` cancelled active tasks then did a bare `asyncio.sleep(0)`
before closing the database, on the assumption that one loop turn was enough
for a cancelled task to finish unwinding. Nothing before B11 exercised the case
where a task is cancelled while genuinely suspended mid-network-I/O and the
process tears down microseconds later — every prior episode's CLI usage always
drained to the task's own terminal event first. B11's `--wait`-less background
send does exactly that (by design), and it turned a cancelled task's own
cleanup (the sink's `set_active_task` call, a DB query) into
`ValueError: no active connection` instead of a clean `cancelled` row, the
first time it ran.
**Fixed**: `Task.wait()` (new) awaits the real `asyncio.Task` and swallows the
`CancelledError` it raises back at its own caller; `Runtime.close()` now awaits
every cancelled task's `wait()` (bounded to 5s) instead of `sleep(0)`. The same
episode also found and fixed the same class of problem for fire-and-forget work
that is not a TaskManager `Task` at all: session title generation, which
`asyncio.run()`'s own cleanup was silently cancelling before its one network
call could return whenever the first send to a session was also a
background/early-return one — see `AgentExecutor.drain_background()`, now
called from the same `Runtime.close()`.

### Memory-first, closed out: three separately-graded findings, not one verdict

Pushed hard on after the first self-test pass looked plausible but wasn't
actually proven. Full detail, including every transcript, lives in
`completed/backend/B11-three-agents.md`; this is the summary Aryan asked kept
visibly separate, because the three have different confidence levels and
different owners going forward.

1. **PROVEN — the reference index reaches the model.** Not inferred from a
   log line: `context.build()`'s own `built.log.included` (the authoritative
   section-by-section record) was inspected directly against a session where
   the first research exchange had been pushed out of `stm_window` by 12
   seeded filler turns. `history` section: 21 items, none of them the old
   exchange. `task_state` section: exactly 1 item, the reference index,
   containing the old answer verbatim. The full `built.messages` list was
   printed in order and the reference index is a real message in it,
   immediately before the user's new question — not a log-only artifact.
2. **PROVEN AND FIXED — `timeout_s` was not a real wall-clock cap.**
   `_check_deadline()` was only called between steps, never while a single
   step was actively streaming. A local model degenerating into an
   unrelated, very long ramble mid-generation had nothing stopping it short
   of the provider's own stream ending — not `httpx`'s timeout (a per-chunk
   read timeout, reset by every token) and not the task's configured
   `timeout_s`. **Reproduced under control**: a prompt engineered to produce
   a long single-step answer, capped at `--max-seconds 8`, ran the full 8
   seconds with **zero** interruption before the fix. After adding a
   `_check_deadline()` call inside the `async for provider_chunk in stream`
   loop (once per chunk, one `time.monotonic()` comparison), the identical
   prompt stopped at exactly `TaskFailed code=bounds_exceeded — stopped
   after 8s: this agent's time limit`. This is a **general** gap, not
   specific to the scenario that surfaced it: any agent, any provider, any
   single generation that runs long is now actually bounded by its
   configured `timeout_s`, not just by the gaps between tool-calling steps.
3. **DOCUMENTED, NOT RESOLVED — whether the model acts on the index.**
   Tried four times against `qwen2.5:1.5b`, each attempt confounded by a
   *different* symptom of the same underlying trait (unreliable multi-step
   tool orchestration), not four unrelated bugs:
   - the Filler's two-file refusal needed a structural backstop because the
     model complied with a JSON-shaped rewrite instead of refusing (see
     above);
   - a same-session repeat of "what is a Bloom filter" produced zero tool
     calls on **both** asks — the model never needed search or memory
     either time, because Bloom filters are common pretraining knowledge,
     so nothing about that test could distinguish "ignored the index" from
     "didn't need anything at all";
   - the essay prompt used to reproduce the deadline bug also spiralled,
     unprompted, into an extended, unrelated ramble (this is what the
     deadline fix above closes off, not what it explains);
   - a repeat, forced to require a real lookup (a question about this
     project's own source, unfindable in any pretraining), showed the
     model inventing a placeholder-looking path (`/path/to/...`) instead of
     using `list_files`' real result, then hallucinating plausible-but-fake
     tool-error text for two more turns before giving up — never
     successfully reading the file at all, so the reference index it later
     received carried nothing useful to test recall against either.
   **Verdict: untestable against this model, not disproven.** The wiring
   (finding 1) is not in question; the model's reliability at using it is.
   Not chased with a bigger local model in this episode — see the new
   parking-lot entry in `INDEX.md` ("Local-model capability tiers...").

Do not proceed until every check above passes.
