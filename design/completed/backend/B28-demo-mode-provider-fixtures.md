# Episode B28 — Demo Mode: Provider, Fixture Library, Sample Project ✅

**A new, always-selectable `DemoProvider` (`backend\providers\demo\provider.py`),
a shipped fixture library (`backend\demo\fixtures\`) covering every row of
F10's own fixture-coverage table, and a shipped sample project
(`backend\demo\sample_project\`) for those fixtures to act on. Chat reply,
queued turn, cancellation, `@research`'s 4-step tool loop (with 2 real
memory proposals), `@filler-explainer`'s explain and fill modes, usage
events, and one scripted error — all timed, all replayed with no network
and no live model. The `@filler-explainer` FILL-mode fixture is
deliberately narrower than F10's literal spec: it replays a plain
JSON-shaped text reply, not a parsed `FillProposal`, because B24/F9b/F13 —
the backend event and the two frontend episodes a real diff card needs —
are all still unbuilt. Replaying a capability Point does not have yet
would be exactly the theater D55 exists to rule out; see "Why this episode
exists" below for the full account.**

> Confirmed by Aryan: all ten Done-when items, via one script
> (`tests\manual_b28_demo_mode.py`) plus `git status`/`git diff` for #10 —
> see "Manual Test Log" below for the full PASS output. Run against a
> scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule; the
> real profile was never a target of anything in this episode. Confirmed
> explicitly: demo's selectability via the correct command shape
> (`session new --provider demo`, then `send --session`) — `cli.py send`
> has no `--provider` flag of its own; provider is set once, at session
> creation.

---

## Why this episode exists

Checked live before writing the task doc, same discipline F10's own split
note used: **F9b (Diff Card) and F13 (Editor Core) have not landed** — no
completed docs past F10b in `design/completed/frontend/`. **B24 (the
Filler's proposal on the wire) has not landed either** — no completed doc,
and `backend\runtime\events.py`'s `OutgoingEventType` has no `FillProposal`
member; `cli.py` has no `--json-proposal` flag. Today, a real (non-demo)
fill-mode turn against a real model persists raw reply text as an ordinary
`AssistantMessage` and nothing else — no parse, no validation, no second
event.

F10's fixture-coverage table asks for "@filler-explainer ... a FILL region
... that yields an **applicable diff**." Replaying that in Demo mode would
mean showing a capability Point does not actually have — the exact thing
D55 (Demo mode wears a visible badge, "Try it free" is answered with real
inference, not theater) exists to rule out. So `filler_fill.json` plays
back a plain JSON-shaped text reply — the literal shape
`managers\context.py:686-694` already asks every real model for — with no
`FillProposal` event and no apply path, because that is the truth of what
Point does today. Its `start_line`/`end_line` are still correct against
the shipped sample file's real content (Done-when #8, verified below), so
the fixture becomes a true, valid proposal with zero rework the day B24
ships — only a frontend pass (F9b/F13, then a later F10c pass) needs to
start reading an event that will, from that point on, actually exist.

---

## `backend\providers\demo\provider.py` (NEW)

`DemoProvider(BaseProvider)` — `selectable = True`, `supports_tools =
True`, registered unconditionally in `providers\bootstrap.py` (no env
var), the structural opposite of `MockProvider`'s `selectable = False` /
`POINT_MOCK=1` gate.

- **Fixture selection needs no new wire field.** `_classify()` reads the
  same information a real model would: `request.tools` non-empty means
  `@research` (the only agent in this fixture set that grants tools);
  the literal `mode=fill`/`mode=explain` substrings
  `_render_editor_context` already writes into the prompt
  (`managers\context.py:684`/`696`) say which Filler mode; a plain chat
  turn is routed by a literal trigger phrase in the user's own latest
  message ("scripted failure", "cancel") so a demo presenter can reach
  the error/cancellation beats on command.
- **Research's 4-step loop needs no turn counter.** Each step's
  `GenerationRequest.messages` is the full accumulated history for that
  run (`agents\executor.py:_loop_body` appends one `ToolMessage` per
  completed call), so counting `MessageRole.TOOL` entries already in
  `request.messages` says exactly which step this is — self-resetting
  per run, with no state to leak across concurrent runs. See
  `_research_step`.
- **`generate()` — the two helper calls, recognized by their own prompt
  text, never by a special code path `agents\executor.py` would have to
  know about.** `_generate_title` (`executor.py:584-598`) and
  `_maybe_propose` (`executor.py:913-972`) are the only two call sites in
  this codebase that ever call `provider.generate()` instead of
  `.stream()` — confirmed by reading `executor.py` in full before writing
  this method, not assumed. `DemoProvider.generate()` matches
  `_maybe_propose`'s prompt by the substring `"standalone fact(s) worth
  remembering"` and replies with the JSON array of exactly 2 facts stored
  in `research_tool_loop.json`'s own `"proposals"` key; it matches
  `_generate_title`'s prompt by `"short 3-6 word title"` and replies with
  a fixed demo title. **This is the plainly-stated answer to the
  `_maybe_propose` question the task doc flagged as unknown: it issues a
  SECOND `provider.generate()` call, not a direct parse of the final
  reply text** — so the 2-proposal count had to come from that real call
  path, and does (Done-when #5, verified below).
- **Structurally independent of `providers\mock\`, on purpose — the call
  the task doc left open, now made.** `_play()` (the flat
  `{"type": ..., "delay": ...}` chunk player used by every fixture except
  `research_tool_loop.json`) is a ~20-line duplicate of
  `MockProvider.stream()`'s own loop, not an import from it, and not a
  shared extracted helper either. Reasoning, recorded here rather than
  only in a comment: Mock is test-only and may grow a test-only chunk
  kind (`rate_limit`, `hang`) this provider must never need to
  understand; Demo is user-facing and always registered. Keeping them
  independent means a change to one's fixture vocabulary can never
  silently become available to, or required by, the other — the same
  discipline this project already applied to `ContextPolicy` vs. the real
  `AgentConfig` (decisions.md D97). The duplication is small (one ~20-line
  method) and was judged cheaper than the coupling.
- **The `web_search` guard is structural, not a fixture-authoring
  convention.** `_forbid_web_search()` runs on every fixture load
  (`_fixture()`, cached per name) and scans the WHOLE loaded JSON — not
  only its `tool_call` entries — for the literal string `web_search`,
  raising `InvalidRequestError` if found. D55/F10 both promise "no
  network"; this makes that promise fail loudly at load time rather than
  rely on every fixture author remembering it.

## `backend\providers\bootstrap.py`

Two additions, both small: a `name == "demo"` branch in `create_provider`
(lazy import, same pattern as every other provider), and one
unconditional `registry.register("demo", ...)` call in
`build_registry_from_env()`, placed after the existing `POINT_MOCK` gate
with a comment explaining the two providers are gated in opposite
directions for opposite reasons. No other line in this file changed —
`git diff` on it shows exactly these two additions.

## `backend\demo\fixtures\` (NEW directory — a shipped asset, not a test fixture)

Six files, one per row of F10's fixture-coverage table that needs its own
content (usage rides inside all of them as non-zero `USAGE` chunks, so it
is not a seventh file):

- `chat_turns.json` — two turns (`chat_reply`, then the shorter
  `queued_turn`), reusing the same `{"model", "turns": [[...], [...]]}`
  shape `MockProvider`'s own fixtures already use — not because this
  provider imports that logic, but because it is the right shape for
  "replay one of several canned turns in sequence" regardless of which
  provider is doing the replaying.
- `cancellation.json` — every chunk is one complete sentence, delayed
  before being yielded. This is what makes Done-when #4 ("a sensible
  partial at ANY reasonable timing") true by construction rather than by
  luck: `Task.cancel()` raises `CancelledError` at whatever `await` the
  generator is suspended on, almost always `asyncio.sleep(delay)` before
  the next chunk is yielded — so the accumulator only ever holds a
  concatenation of *fully delivered* chunks, and since every delivered
  chunk ends with a period, every possible prefix does too.
- `error_case.json` — a short bit of text, then a `type: "error"` chunk
  that raises `ProviderError`, the same normalized exception type (via
  `_play()`, mirroring `MockProvider`'s own `"error"` chunk) a real
  provider failure would raise.
- `research_tool_loop.json` — 3 tool-call steps (`list_files`, then two
  `read_file`s, all against the real shipped sample project) plus a final
  text+usage step with no tool call, and a `"proposals"` list of exactly
  2 facts that `generate()` reads back for `_maybe_propose`.
- `filler_explain.json` / `filler_fill.json` — plain text and
  JSON-shaped-text respectively, both pinned to `app.py`'s real line
  numbers (see the sample project section below).

## `backend\demo\sample_project\` (NEW directory — a shipped asset)

`app.py`, `utils.py`, `README.md`, `web/index.html`, `web/style.css` —
small, deliberately boring, real files for the fixtures to list, read, and
reference by line number against. `app.py`'s exact content was fixed
**before** writing `filler_fill.json`/`filler_explain.json`, and the shown
line ranges were hand-derived against `managers\editor_context.py`'s real
`resolve_scope()` logic, then confirmed by the verification script against
the live function — not guessed and hoped to match:

- `mode=fill`, `cursor=7` (inside `greet`'s body) → shown lines **4–8**
  (the whole `greet` function). `filler_fill.json`'s reply targets exactly
  this range.
- `mode=explain`, `selection=[11,13]` (the `add` function) → shown lines
  **11–13**, matching the selection exactly (a short, brace-free function
  whose whole body is 3 lines).

`project_manager.init()` was run against this real path during
verification (not inside the scratch profile — project state is written
into the project's own `.point\` directory, by design), confirming the
existing project system works against this shipped tree unmodified. The
resulting `backend\demo\sample_project\.point\` directory is covered by
the repo's existing root `.gitignore` (`.point/`) and was left in place as
evidence, not committed.

## `backend\tests\manual_b28_demo_mode.py` (NEW)

One long-lived `Runtime`, built via `build_runtime(single_instance=False,
say_hello=False, heartbeat=False)` — the same construction
`manual_b26_behaviour_overrides.py` and `manual_b12_concurrency.py` use,
for the identical reason: `cli.py` builds a brand-new `Runtime` per
invocation, so a queued second task and a multi-step research run (DemoProvider
needs to see the SAME growing `request.messages` across steps) both need one
process that stays alive long enough to observe its own effects. Drives every
section through `runtime.receive()` directly, `check()`-and-collect like
every other `manual_*` script in this repo, exits non-zero on any failure.

---

## Manual Test Log (Episode B28)

Scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule, one
PowerShell session: `cli.py migrate`, then
`uv run python tests\manual_b28_demo_mode.py`.

```
=== #2  chat reply streams over real time, with usage
  [PASS] reached a terminal event
  [PASS] took several real seconds (5.9s), not near-instant  -- elapsed=5.92s
  [PASS] reply persisted with non-zero usage  -- {'input_tokens': 142, 'output_tokens': 96, 'total_tokens': 238}

=== #3  a second send while the first streams is QUEUED, then both finish
  [PASS] the second task is QUEUED while the first still runs  -- states=[(..., RUNNING), (..., QUEUED)]
  [PASS] both turns eventually completed and persisted  -- assistant_count=2

=== #4  cancellation leaves a sensible partial, at several different timings
  [PASS] cancel_after=0.3s: partial is empty or ends at a complete sentence  -- ''
  [PASS] cancel_after=2.0s: partial is empty or ends at a complete sentence  -- '...one full sentence per chunk.'
  [PASS] cancel_after=5.0s: partial is empty or ends at a complete sentence  -- '...the moment, not the fixture.'
  [PASS] cancel_after=8.5s: partial is empty or ends at a complete sentence  -- '...let it run all the way through.'

=== #5  @research: 4 steps, real tool calls, 2 proposals via the real path
  [PASS] 4 steps observed via ProgressUpdated  -- steps_seen=[1, 2, 3, 4]
  [PASS] 3 real tool.started/tool.completed pairs (list_files, read_file x2)  -- started=3 completed=3
  [PASS] every real tool call succeeded against the shipped sample project
  [PASS] research's final reply persisted with non-zero usage  -- total_tokens=796
  [PASS] exactly 2 agent_proposals rows, written by the REAL _maybe_propose path  -- count=2
  [PASS] the 2 proposals' content matches research_tool_loop.json's own 'proposals' list

=== #7  @filler-explainer explain mode
  [PASS] explain mode produced a plain-text reply about add()

=== #8  @filler-explainer fill mode -- plain reply, no proposal event, no apply path
  [PASS] fill mode's reply is the JSON shape context.py's prompt asks for
  [PASS] start_line/end_line fall inside the shown region (4-8, computed by resolve_scope for cursor=7 against app.py's real content)
  [PASS] no FillProposal event exists on the wire -- none was emitted, by construction

=== #6  the web_search guard fires on a deliberately bad fixture, and only then
  [PASS] a fixture naming web_search is refused at load time
  [PASS] none of the shipped fixtures actually mention web_search

=== #9  the error-case fixture produces TaskFailed with a real error
  [PASS] TaskFailed was emitted
  [PASS] the error text is the scripted message, not a traceback

ALL CHECKS PASSED
```
Exit code 0.

**#1** (demo is selectable with no env var set) was checked separately,
against `providers.bootstrap.build_registry_from_env()` directly — no
profile needed, since provider registration does not touch
`APPDATA`/`LOCALAPPDATA`:

```
registered: ['ollama', 'demo']
demo.selectable: True
demo.supports_tools: True
```

Confirmed again by Aryan via the correct command shape: `cli.py session
new --provider demo --model point-demo-v1`, then `cli.py send <text>
--session <id>` — `send` itself carries no `--provider` flag; provider is
fixed at session creation (`agents\executor.py`'s resolution chain reads
`session.provider`, never a per-send override that does not exist in v1).

**#10** (`providers\mock\` and `backend\tests\fixtures\` untouched):

```
$ git status --porcelain
 M backend/providers/bootstrap.py
 M design/WORKFLOW.md
 M design/model_usage.md
?? backend/demo/
?? backend/providers/demo/
?? backend/tests/manual_b28_demo_mode.py
?? design/tasks/backend/B28-demo-mode-provider-fixtures.md
```
No entry for `providers/mock/` or `backend/tests/fixtures/` — neither
modified nor touched.

---

## Bugs caught during manual testing

**None, on the one real run.** Worth stating plainly rather than
inventing drama: the script above passed clean on its first full
execution. The reason is `B26-behaviour-overrides-doctor-wire.md`'s own
"Bugs caught" section, read and applied *before* writing this episode's
verification script rather than discovered the hard way a second time —
its generalizable lesson ("a verification gate for in-memory,
process-lifetime state... cannot be proven across separate `cli.py`
invocations, only a single held-open `Runtime` can observe its own
writes") is exactly why `manual_b28_demo_mode.py` was written as one
long-lived `Runtime` from the start, rather than as a PowerShell sequence
of `cli.py send` calls that would have silently failed to observe either
the queued second task or the research run's own step-by-step state, for
the identical reason B26 already documented.

---

## What the next episode inherits

- **F10c's frontend pass has three open questions this episode
  deliberately leaves open** (unchanged from the task doc, now confirmed
  still open after building):
  1. *When/how a demo session's `.point\` gets created* against the
     shipped sample project — automatically on provider selection, or via
     an explicit `ProjectInit` call F10c makes itself. This episode's own
     verification called `runtime.projects.init()` directly; that is a
     test convenience, not a precedent for what the real app should do
     automatically.
  2. *How F10c discovers the shipped sample project's path at all* — a
     hardcoded relative path next to the backend (`backend\demo\
     sample_project\`, confirmed stable by this episode), or a new field
     this episode deliberately did not add.
  3. *Whether `DemoProvider` and `MockProvider` ever end up sharing an
     extracted fixture-replay helper* — **resolved for this episode: no.**
     `DemoProvider._play()` is an independent ~20-line duplicate of
     `MockProvider.stream()`'s own chunk loop, not an import, not a shared
     module. See the provider section above for the full reasoning. A
     later episode that wants to extract one must re-justify it against
     this record, not assume the duplication was an oversight.
- **`filler_fill.json` is deliberately not a `FillProposal`.** The day
  B24 ships, re-read "Why this episode exists" before touching this
  fixture — its JSON content was built to become a valid proposal with
  zero edits (`start_line`/`end_line` already correct against `app.py`'s
  real content), but emitting the actual event is B24's wire change, not
  a Demo-mode change.
- **The `_maybe_propose` finding is load-bearing for any later episode
  that touches `writes_proposals` agents**: it is a second
  `provider.generate()` call with its own distinguishable prompt text, not
  a parse of the final streamed reply. Any new agent that sets
  `writes_proposals=True` will drive the same second call against
  whichever provider it uses — a real provider's `generate()` must answer
  it the way `DemoProvider`'s does here, or that agent's proposals will
  silently never materialize.
- **Demo mode's visible badge (F10 Done-when #9, D55) is enforced by
  nothing this episode built, on purpose.** The backend has no concept of
  a badge — it only guarantees every message produced under `demo` is
  tagged with provider `"demo"` in its existing `InvocationMetadata`,
  exactly as every other provider already is. F10c must read that field;
  this episode added nothing new for F10c to read.
- **`tests\manual_b28_demo_mode.py` is a real regression script, kept in
  the repo.** If a later episode changes `chat.queue_depth`'s default,
  `_loop_body`'s step-progress payload shape, or `_maybe_propose`'s prompt
  text, re-run it rather than assuming these ten checks still hold.
