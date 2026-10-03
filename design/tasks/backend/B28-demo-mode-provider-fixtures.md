# Episode B28 — Demo Mode: Provider, Fixture Library, Sample Project

**F10's own spec asks for a "Demo mode" selectable provider that replays
pre-recorded fixtures with no network and no Ollama, badged for its entire
duration (D55 — a replay mode that hides what it is would be theater). F10
(narrowed, Session 16) explicitly deferred it: "a provider type that does not
exist, plus a shipped sample project and fixture library that is backend-and-
asset work, not a Go frontend change" (`design/tasks/frontend/INDEX.md`
F10-split note #4). This episode is exactly that backend-and-asset work. It
builds no UI.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B28-demo-mode-provider-fixtures.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet high. One new provider class (mechanically close to
`MockProvider`, but it must stay structurally separate — see Do NOT), a
fixture library with real timing data, a shipped sample project whose exact
file contents several fixtures must line up against by line number, and one
non-obvious interaction with `_maybe_propose` (§5) that needs reading before
assuming it's free.

**Depends on:** B4 (provider system, `providers\base.py`/`registry.py`/
`bootstrap.py`), B6 (envelope, bootstrap), B7 (project system,
`project_manager.init`/`resolve`), B9 (`AgentConfig`), B10 (tool system —
`ToolExecutor`, `tool.started`/`tool.completed`), B11 (the research agent,
`writes_proposals`, fill/explain modes in `editor_context.py`), B12
(scheduler lane caps — what actually produces a queued turn).

**Explicitly NOT a dependency, by design:** B24 (the Filler's proposal on the
wire), F9b (diff card), F13 (editor core). None of the three exist yet. §4
below narrows the `@filler-explainer` fixture specifically so this episode
does not need any of them — see "Why this episode exists" for the evidence
and the reasoning, not just the conclusion.

**Consumed by:** F10c (Settings — Demo Mode), a *future*, separate frontend
pass, not scoped here. F10c's whole job is wiring Demo mode into the
provider chooser and the `DEMO` header badge; this episode's job is making
sure there is a real provider and a real fixture set for it to point at.

---

## Why this episode exists

Checked before writing anything, per the instruction that started this
episode:

- **F9b (Diff Card) has not landed.** No completed doc in
  `design/completed/frontend/` past F10b; `INDEX.md` lists F9b as depending
  on F9a + F13, neither closed.
- **F13 (Editor Core) has not landed.** Same evidence — not in
  `design/completed/frontend/`, and F10's own INDEX note #4 already says so
  explicitly: "there is no Editor workspace and no apply target anywhere in
  this repo yet (F13/F9b's job)."
- **B24 (the Filler's proposal on the wire) has not landed either** — no
  completed doc, and grepping `backend\runtime\events.py` confirms
  `OutgoingEventType` has no `FillProposal` member. Today, a real (non-demo)
  fill-mode turn against a real model just persists raw reply text as an
  ordinary `AssistantMessage`; nothing parses it, nothing validates it,
  nothing emits a second event for it (`cli.py` has no `--json-proposal`
  flag anywhere — confirmed absent).

That third fact is the one F10's own task doc didn't anticipate and this
episode has to resolve on its own: F10's fixture-coverage table asks for
"@filler-explainer ... a FILL region that streams as ghost text and yields
an **applicable diff**." An applicable diff is not just an unbuilt frontend
feature (F9b/F13) — it is an unbuilt *backend* event (B24). Demo mode
replaying one anyway would mean the demo shows a capability Point does not
actually have, which is the one thing D55 exists to rule out ("Try it free"
is answered with real inference, not with theater — the same discipline
applies to faking a feature that isn't real yet).

**So the FILL-mode fixture in this episode is narrowed to what today's real
backend actually does on a fill turn: stream back a plausible JSON-shaped
reply (the exact shape `managers\context.py:686-694` already asks every
real model for) that persists as an ordinary assistant message, with no
`FillProposal` event and no apply path — because that is the truth of what
Point does today, demo or not.** The fixture's JSON content still has
correct `start_line`/`end_line` values against the shipped sample file (§6),
so the moment B24 ships, this fixture needs zero rework — only a frontend
pass (F9b/F13, then F10c again) to start reading an event that will, from
that point on, actually exist. Until then, Demo mode is honest about what
it's showing.

---

## Build

### 1. `DemoProvider` — a new, structurally separate provider

New module: `backend\providers\demo\provider.py`. Implements
`BaseProvider` (`providers\base.py:25-66`: `generate`, `stream`,
`list_models`, `health`) the same way every real provider does.

- `name = "demo"`, `selectable = True` — the opposite of `MockProvider`'s
  `selectable = False` (`providers\mock\provider.py:87`). Demo mode is a
  real, user-facing choice; Mock is not.
- **Registered unconditionally** in `providers\bootstrap.py`'s
  `create_provider` dispatch (`bootstrap.py:15-36`), not behind an env var.
  `MockProvider` is gated by `POINT_MOCK=1` (`mock_provider.is_enabled()`,
  `bootstrap.py:53-54`) specifically because it must never appear in the UI
  (F10's own Do-NOT list: "Expose `MockProvider` in the UI"). Demo must
  appear in the UI by design (D55) — the two providers are gated in
  opposite directions for the same reason, and that difference must be
  visible in the code, not just in a comment.
- `list_models()` returns one fixed pseudo-model (e.g. `point-demo-v1`) —
  there is no real model backing this provider, and the name must not look
  like it could be a real one from any configured provider.
- `health()` always reports healthy — there is nothing to fail outside the
  chosen fixture (the error-case fixture, §7, is modeled as a *scripted*
  failure during `stream()`, not a down health check).

**Do not put `DemoProvider` under `providers\mock\` or import from it.**
`MockProvider`'s own fixture-turn-walking logic
(`_load_turns`/`_next_turn`, `provider.py:102-136`) and its chunk shapes
(flat list / `{"model","chunks"}` / `{"model","turns":[...]}`,
`provider.py:9-44`) are the right *shape* to reuse — `ProviderChunkType`
(TEXT/THINKING/USAGE/TOOL_CALL/DONE, `providers\models.py:130-135`) is
shared machinery every provider already speaks, and reinventing a second
chunk vocabulary for Demo specifically would be new machinery nothing
needs. But the two providers exist for opposite reasons (one must never
reach a user, one must only ever reach a user) and this project's own
precedent (`ContextPolicy` vs. the real `AgentConfig`, decisions.md D97) is
to keep such pairs **structurally** separate, not merely flag-separated.
Whether `DemoProvider` duplicates a small turn-walking loop or the two
providers share one extracted helper module (e.g.
`providers\_fixture_replay.py`) imported by both is this episode's call to
make — record which, and why, in the completed doc; either is acceptable,
a silent re-import of `providers\mock\provider.py` into a selectable,
always-registered provider is not.

### 2. Timed replay — reuse the existing precedent, add nothing new

`MockProvider.stream()` already honors a per-chunk `"delay"` field via
`await asyncio.sleep(delay)` before yielding (`provider.py:156-158`) —
precisely because "a stream that arrives instantly does not exercise
cancellation" (the module's own docstring, `provider.py:43-44`). `Demo-
Provider` uses the identical per-chunk `delay` field, for the identical
reason F10 states explicitly: "an instantly-complete response looks fake
and demonstrates none of the streaming, queueing, or cancellation work."
No new timing machinery anywhere in `streaming\adapter.py` or
`accumulator.py` — both are confirmed delay-agnostic today and stay that
way.

### 3. Fixture library — ship it outside `backend\tests\`

New directory: `backend\demo\fixtures\` (sibling to `backend\demo\
sample_project\`, §6 — not under `backend\tests\fixtures\`, which holds
`POINT_MOCK`-only developer/test assets and is the wrong place to ship a
product feature's data). One fixture file per row of F10's own
fixture-coverage table:

| Fixture file | Row | Shape |
|---|---|---|
| `chat_reply.json` | chat reply | multi-paragraph markdown, one fenced code block, TEXT chunks paced to stream for several seconds |
| `queued_turn.json` | queued turn | a second, shorter TEXT-chunk reply — see §4 for why no special event is needed |
| `cancellation.json` | cancellation | TEXT chunks paced slowly enough to interrupt mid-stream, chosen so the already-emitted prefix reads as a sensible partial sentence, not a mid-word cut |
| `research_tool_loop.json` | @research | TOOL_CALL chunks requesting `read_file`/`list_files`/`load_task_result` **only**, four steps, against the shipped sample project (§6) — see §5 for the proposal interaction |
| `filler_explain.json` | @filler-explainer EXPLANATION | plain TEXT-chunk reply, explain-mode, against a function in the shipped sample project |
| `filler_fill.json` | @filler-explainer FILL | TEXT chunks forming the exact JSON shape `context.py:686-694` specifies, `start_line`/`end_line` correct against the shipped sample file — see "Why this episode exists" for why this stops at a plain reply, not a `FillProposal` |
| `usage` | usage events | not a separate file — every fixture above carries real USAGE chunks (existing `ProviderChunkType.USAGE`, already wired to the `UsageUpdated` outgoing event) with plausible, non-zero token counts, so no fixture is exempt |
| `error_case.json` | error case | raises during `stream()` — see §7 |

### 4. Queued turn needs no new wire event

Confirmed: `runtime\events.py` has no `task.queued` or equivalent —
`TaskState.QUEUED` (`runtime\task.py:42`) is scheduler-internal state, not
pushed as its own event. A queued-turn demo does not need `DemoProvider` or
this episode to invent one: `chat.queue_depth` (default 1,
`scheduler.py:261-278`) already lets a second `send` wait behind a running
one inside the same turn lane (`LANE_TURN`, `scheduler.py:64`,
`TURN_LANE_CAPACITY=1`, `scheduler.py:74`). `queued_turn.json` only needs
to exist as a second fixture a demo session can send while
`chat_reply.json` is still streaming; the queueing itself is the
scheduler's existing job, unmodified.

### 5. `@research`'s tool loop and the "2 pending memory proposals" row

The research agent's loop (`agents\executor.py:_loop_body`, 649-909) emits
`PROGRESS_UPDATED` per step with `{step, max_steps, ...}` (741-747) and
real `tool.started`/`tool.completed` pairs around each tool call (868-892)
— all of that comes for free from the existing executor once
`DemoProvider`'s fixture requests tool calls, because `ToolExecutor` does
not know or care whether the provider behind it is real. **`read_file`/
`list_files`/`load_task_result` genuinely execute against the shipped
sample project** (§6) — this is real tool execution on real shipped files,
which is the honest half of the demo (D55's "not theater" applies here
too).

What is *not* free: "2 pending memory proposals" is not part of any done-
event payload (confirmed — `TaskDone`'s payload is `{}`,
`runtime.py:1577-1588`). It is rows written to `agent_proposals` by
`_maybe_propose` (`executor.py:913-972`), called only when
`agent.writes_proposals` is true and only after the loop's final non-tool-
call reply. **Read `_maybe_propose` before building `research_tool_loop
.json`** — if it issues its own provider call to extract proposal
candidates from the final reply (rather than parsing the reply text
directly), `DemoProvider`'s research fixture needs a *second* scripted
turn for that call, not just the four tool-loop steps. Do not assume
either way; confirm from the code and record which it is in the completed
doc.

**Hard guard: no fixture may ever request the `web_search` tool.** D55 and
F10 both promise "no network" for Demo mode; `web_search` is a real
network call (`tools\builtins\web.py`). Validate this at fixture-load
time — `DemoProvider`'s loader rejects (refuses to start, loud error, not
a silent skip) any `TOOL_CALL` chunk naming `web_search` in any shipped
fixture. This is a load-time structural guarantee, not a reviewer's
promise to keep remembering not to add one.

### 6. Shipped sample project

New directory: `backend\demo\sample_project\` — a few small source files
plus a plain HTML/CSS folder, per F10's own description. Nothing like this
exists anywhere in the repo today (checked — no `sample_project`,
`fixtures\project`, or similar under `backend\tests\` or the repo root).

- **Every fixture that references a file or line range (`research_tool
  _loop.json`, `filler_explain.json`, `filler_fill.json`) must point at
  real files in this directory with real line numbers** — not invented
  numbers that happen to parse. This is the direct consequence of "Why
  this episode exists": `filler_fill.json`'s `start_line`/`end_line` must
  already be correct against the real shipped file, so B24 landing later
  turns this fixture into a true proposal with no edit required.
- This episode ships the directory and confirms `project_manager.init()`
  / `.resolve()` (`managers\project_manager.py:70-96`, `98-118`) work
  against it via the existing `cli.py project init`/`resolve` commands —
  no new project-system surface. **How a demo session's `.point\` gets
  created against this shipped path** (at Demo-provider-selection time,
  automatically, vs. left for F10c's frontend pass to call the existing
  `project.init` event against a path it's told about) is explicitly
  F10c's decision, not this episode's — see Handoff. Do not add a new
  wire event or CLI flag for it here.

### 7. Error-case fixture

`error_case.json` makes `DemoProvider.stream()` raise partway through, the
same way a real provider failure would (check `providers\errors.py` for
the real exception type every adapter already expects and raise that one
— not a bespoke Demo-only exception), so the turn lands on `TaskFailed`
with a real-looking error message. This exercises the actual error path
(`events.py`'s `TaskFailed`, `runtime.py:1577-1588`'s failed-branch
payload), not a Demo-specific shortcut around it.

---

## Do NOT

- **Do not touch `MockProvider`, its fixtures, or `POINT_MOCK`.** Mock
  stays test-only and invisible in the UI (F10's own Do-NOT list). This
  episode adds a sibling, not a modification.
- **Do not add a `FillProposal` event, any apply/diff wire surface, or
  anything named after what B24/F9b/F13 will eventually own.** See "Why
  this episode exists." If this episode's own fixture work makes B24's
  eventual shape obvious, write that down in the Handoff — do not build it
  early.
- **Do not add a `task.queued` (or similarly named) wire event.** §4 — not
  needed, and inventing one here would be guessing at a shape B12's own
  owner never asked for.
- **Do not let any shipped fixture request `web_search`, or any other
  network-touching tool.** §5's guard is a hard requirement, not a nice-to-
  have.
- **Do not build any Go/frontend change.** No provider-chooser row, no
  `DEMO` badge, no Settings wiring. All of that is F10c.
- **Do not gate `DemoProvider` behind an environment variable.** Unlike
  `MockProvider`, it must be selectable the same way any real provider is
  — through config, not a flag only a developer would set.
- **Do not place fixtures under `backend\tests\fixtures\`.** That
  directory is `POINT_MOCK`-scoped test data; Demo's fixtures are a
  shipped product asset.

---

## Done when

1. `cli.py provider list` (or the equivalent existing introspection
   command) shows `demo` as a selectable provider with no env var set.
2. A chat-reply turn against `demo` streams a multi-paragraph markdown
   reply with a fenced code block, taking several real seconds (timed,
   not instant), and persists with non-zero usage.
3. Sending a second, shorter turn while the first is still streaming
   results in the second task reaching `QUEUED` state before it starts
   (`chat.queue_depth` honored) — no code change needed to prove this,
   only the two fixtures existing.
4. A turn against the cancellation fixture, cancelled mid-stream (same
   `task.cancel`/`task.cancel.accepted` path every provider already
   supports), leaves a partial assistant message that reads as a sensible
   sentence fragment, not a mid-word cut.
5. A turn against `research_tool_loop.json` run through the `research`
   agent produces four `PROGRESS_UPDATED` steps, matching
   `tool.started`/`tool.completed` pairs for each real tool call against
   the shipped sample project, a final reply, and exactly 2 rows in
   `agent_proposals` for that session afterward.
6. No fixture, loaded at startup, ever contains a `TOOL_CALL` chunk naming
   `web_search` — enforced by a loud failure if one is (hand-added
   temporarily to prove the guard fires, then removed).
7. A turn against `filler_explain.json` in explain mode produces a plain
   text reply describing a real function in the shipped sample project.
8. A turn against `filler_fill.json` in fill mode produces a reply whose
   JSON body's `start_line`/`end_line` are correct against the shipped
   sample file's real content, persists as an ordinary assistant message,
   and produces **no** `FillProposal` event (none exists to produce) and
   **no** apply/diff artifact of any kind.
9. A turn against `error_case.json` produces `TaskFailed` with a real
   provider-shaped error, not a raw traceback or a Demo-specific message
   format.
10. `backend\tests\fixtures\` and `providers\mock\` are untouched —
    `git diff` shows no changes to either.

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md's hard rule: one combined session, `$env:APPDATA` and
`$env:LOCALAPPDATA` both pointed at a scratch directory for its whole
duration, nothing renamed, nothing deleted by the real profile's name.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b28"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData      = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # 1. demo is selectable with no env var set
    uv run python cli.py provider list

    $sid = (uv run python cli.py session new "b28").Split(" ")[0]

    # 2, 10 (usage): a timed chat reply
    Measure-Command {
        uv run python cli.py send "tell me about this project" `
            --session $sid --provider demo --wait
    }
    # EXPECT: several real seconds elapsed, not near-instant.

    # 3: queue depth — send a second turn while believing the first still
    #    running (script this with a short --wait on the first and an
    #    immediate second send, or inspect TaskState via session/task
    #    introspection — record which this episode used).

    # 4: cancellation — start a send against cancellation.json, cancel
    #    partway (existing task.cancel CLI path), inspect the persisted
    #    partial message.

    # 5: research tool loop
    uv run python cli.py send "@research summarize the sample project" `
        --session $sid --provider demo --wait
    uv run python cli.py session messages $sid | Select-Object -Last 3
    # EXPECT: 4 step PROGRESS_UPDATED events (inspect via --verbose/log),
    #         real tool.started/tool.completed pairs, 2 agent_proposals rows:
    sqlite3 "$scratch\Point\runtime.db" `
        "select count(*) from agent_proposals where session_id = '$sid';"

    # 7, 8: filler-explainer, both modes, against the shipped sample file
    $sample = "D:\Projects\Point\backend\demo\sample_project\<chosen file>"
    uv run python cli.py send "@filler-explainer explain this" `
        --session $sid --provider demo --file $sample --select <n> --wait
    uv run python cli.py send "@filler-explainer add a check" `
        --session $sid --provider demo --file $sample --cursor <n> --wait
    # EXPECT: explain -> plain text. fill -> JSON-shaped text reply with
    #         correct start_line/end_line, no proposal event of any kind.

    # 9: error case
    uv run python cli.py send "trigger the scripted failure" `
        --session $sid --provider demo --wait
    # EXPECT: TaskFailed, real-looking error text.
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

**Record the exact commands used for checks 3 and 4** in the completed
doc — both depend on CLI introspection this doc deliberately leaves to the
implementing session, since the right flag/command may already exist and
guessing it here risks copying something stale.

---

## Handoff

- **F10c's frontend pass still has three open questions this episode
  deliberately leaves open:** (1) when/how a demo session's `.point\`
  gets created against the shipped sample project (§6) — automatically on
  provider selection, or via an explicit `project.init` call F10c makes
  itself; (2) how F10c discovers the shipped sample project's path at all
  (a hardcoded relative path next to the backend, vs. a new field this
  episode could have added but didn't); (3) whether `DemoProvider` and
  `MockProvider` ended up sharing an extracted fixture-replay helper or
  staying independently duplicated (§1) — read the completed doc for
  which, before assuming either.
- **`filler_fill.json` is deliberately not a `FillProposal`.** The day B24
  ships, re-read "Why this episode exists" before touching this fixture —
  its JSON content was built to become a valid proposal with zero edits,
  but emitting the actual event is B24's wire change, not a Demo-mode
  change. Do not let a future session quietly upgrade this fixture as a
  drive-by inside an unrelated episode.
- **The `_maybe_propose` finding from §5** (whether it issues its own
  provider call) is load-bearing for any later episode that touches
  `writes_proposals` agents — record the answer plainly in the completed
  doc, not just encoded in the fixture file's shape.
- **Demo mode's badge, and the fact that it must be visible for its
  entire duration (F10 Done-when #9, D55), is enforced by nothing this
  episode builds.** The backend has no concept of "badge" — it only
  guarantees that every message produced under `demo` is honestly tagged
  with provider `"demo"` in its existing invocation metadata, which is
  already how every provider is tagged today. F10c must read that field;
  this episode does not need to add anything for F10c to read it.
