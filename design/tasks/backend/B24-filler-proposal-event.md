# Episode B24 — The Filler's Proposal on the Wire

**B11 already asks the Filler-Explainer for a structured region — a JSON object
with `file`, `start_line`, `end_line`, `replacement`, `note` — and then throws
the structure away, persisting the model's raw text as an ordinary assistant
message. Nothing parses it. Parse it in the backend, validate it against the
region that was actually shown, and put the result on the wire as its own
event. Nothing else.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B24-filler-proposal-event.md`) claims reality and
> completion. Code wins on any disagreement.

**Model:** Sonnet high. One parse, one validation set, one envelope type, one
push site, over a prompt and a terminal boundary that both already exist. The
hard thinking was B11's (what to ask the model for) and B8's (what region was
shown). This is the delivery. Escalate to Opus only if the validation rules
turn out to interact with `resolve_scope()` in ways this doc did not
anticipate.

**Depends on:** B11 (the fill-mode prompt and `MODE_FILL`, both built), B8
(`managers\context.py`'s `_render_editor_context`, which decides and knows the
shown region), B6 (the envelope and the dispatcher), B12 (the terminal-event
site this pushes from, and `Outcome`).

**Consumed by:** F9's diff card, which is **deferred and unbuilt** precisely
because this event does not exist. See "Why this episode exists".

---

## Why this episode exists

F9 (WORKFLOW.md's Session 15) was narrowed to the task tray and the result
card's render/discard half, and **stopped before writing any diff code**, per
its own "stop and tell me if ambiguous" instruction. Four blockers were found;
this episode clears exactly one of them.

F9's spec says "the backend returns a **rewritten region**, not a patch; Go
computes the diff locally". The first half is not true today. What the backend
returns is the model's raw reply text, which in fill mode is *hoped* to be a
JSON object:

- `managers\context.py:686-694` is the only place the shape is stated, and it
  states it **to the model, in a prompt string**:
  ```
  '{"file": "<path>", "start_line": <int>, "end_line": <int>, '
  '"replacement": "<the rewritten region>", "note": "<short note>"}\n'
  ```
- Nothing anywhere parses that. `grep -rn "replacement" backend/` finds the
  prompt and nothing else.
- The reply is persisted as a plain `AssistantMessage` —
  `runtime\runtime.py:1421` (streamed) or `:1468` (echo/refusal) — and emitted
  as `TaskDone` with an **empty payload** (`runtime.py:1515`: `payload` is
  `{}` for `kind == "done"`).

So Go's only access to a proposed change is the assistant message body: raw,
unvalidated, model-authored text.

**Having Go parse that is the wrong answer, and this project has already
decided so three times.** `parse_invocation` (B12) resolves typed input to a
structured `Invocation` in the backend rather than letting each caller
re-derive it. B20 put `ContextReport` on the wire rather than letting F6
recompute token arithmetic, on the explicit grounds that "two implementations
of the same arithmetic disagree eventually and the UI is where you find out".
B23 added `ListDirectory` rather than letting the `@` picker read the
filesystem itself. The rule those three share: **the backend resolves, the
frontend renders.** A Go-side JSON parser over LLM output would be the first
exception, and it would be the exception in the worst possible place — the
input is adversarially unreliable by nature, and the consumer is a destructive
file-write path.

This episode is the same move B20 made: an internal shape that already exists
gets a way to travel, and the calculation stays where it is.

---

## Build

### 1. `FillProposal` — the parsed, validated shape

New module: `backend\managers\fill_proposal.py`. A frozen dataclass plus a
parse function. **Not** in `editor_context.py` — that module is the *request*
side (what the Filler is told); this is the *response* side, and
`editor_context.py` is already imported by `managers\context.py`, so putting
the response type there invites a circular import the moment parsing needs
anything from the context builder.

```
@dataclass(frozen=True)
class FillProposal:
    file: str            # as the model returned it, before resolution
    start_line: int      # 1-based inclusive
    end_line: int        # 1-based inclusive
    replacement: str
    note: Optional[str]
    shown_start: int     # the region the model was actually shown
    shown_end: int
```

`shown_start`/`shown_end` are **not** redundant with `start_line`/`end_line`.
They are what makes the validation in §3 checkable by the consumer rather than
only by the producer, and F9's diff card needs them anyway to render context
lines around the change.

`to_dict()` carries every field, no subset — B20's rule, for B20's reason.

### 2. `parse_fill_proposal(text) -> FillProposal | FillParseFailure`

Two outcomes, both ordinary. A failure is **not** an exception and **not** an
`Error` event: the Filler is explicitly allowed to refuse in plain text
("If the request needs a second file, a new file, or a command, refuse in
plain text instead of guessing at scope" — `context.py:692`), and B11 also
delivers its one-file refusal as a *normal* reply via the echo path
(`agents\executor.py:383-399`). A refusal that surfaced as an error would make
correct behaviour look broken.

So: a fill-mode turn whose reply does not parse is simply a turn with no
proposal. The reply still persists and still renders as text, exactly as today.

Parsing rules, in order:

1. **Strip one fenced code block if the whole reply is one.** Models wrap JSON
   in ```` ```json ```` constantly, and the prompt's "exactly one JSON object
   and nothing else" does not reliably prevent it. Strip a leading
   ` ```[a-z]* ` line and a trailing ` ``` ` line, and nothing else — do not
   go hunting for JSON embedded in prose. A reply with commentary around the
   object is a reply that did not follow the instruction, and silently
   salvaging it teaches nobody anything.
2. **`json.loads`.** Any `JSONDecodeError` is a parse failure with reason
   `not_json`.
3. **Required keys present and correctly typed**: `file` (non-empty str),
   `start_line` / `end_line` (int — reject a float or a numeric string;
   `"12"` means the model did not follow the schema), `replacement` (str,
   **may be empty** — deleting a region is a legitimate proposal). `note` is
   optional; absent or null becomes `None`. Reason: `bad_shape`, naming the
   offending key.
4. **Unknown extra keys are ignored, not rejected.** A model that adds
   `"confidence"` has still answered the question.

`FillParseFailure` carries a `reason` slug and the offending text's first
~200 characters for the log — never the whole reply, which can be large.

Reason slugs: `not_json`, `bad_shape`, `range_inverted`, `range_outside_shown`,
`file_mismatch`. The last three come from §3.

### 3. Validation against the region that was actually shown

This is the half that cannot live in Go, and the reason this episode is not
"just move the parse across the boundary".

The prompt tells the model `start_line`/`end_line` "must fall within the shown
region, one file only". **Nothing enforces it.** The shown region is computed
by `resolve_scope()` (`managers\editor_context.py:84`) and rendered by
`_render_editor_context` (`managers\context.py`), and the frontend does not
know what it was — F9's diff card would be trusting a model-supplied line
range against a buffer, with no way to tell a correct range from a
hallucinated one.

Validate, and reject as a parse failure if violated:

- **`start_line <= end_line`** → `range_inverted`.
- **`shown_start <= start_line` and `end_line <= shown_end`** →
  `range_outside_shown`. A region outside what the model was shown is a region
  it did not read.
- **`file` names the same file as the `EditorContext`.** Compare resolved,
  normalized paths — `Path(...).resolve()` on both, case-insensitively on
  Windows — not raw strings: the model will return `config.py` where the
  context said `D:\Projects\Point\backend\config.py`, and a bare-basename
  match against the context's own filename is the intended accept. A path
  naming a *different* file is `file_mismatch`, which is the structural
  backstop behind `_mentions_other_file`'s prompt-level check
  (`agents\executor.py:383`) — that one catches the *request* naming two
  files; this catches the *reply* doing it.

**Carry `shown_start`/`shown_end` from the context build; do not recompute
them.** `resolve_scope()` is deterministic given the same lines, but the file
may have changed on disk between the build and the terminal event, and a
second call would then validate against a different region than the model saw.
This is the same class of mistake as B12's bug #2 (an estimate computed under
one scope and spent under another) and F8's chip staleness. The region shown is
a fact about a past moment; it must be captured, not re-derived.

### 4. Knowing it was a fill turn, at the terminal boundary

The parse runs where the reply is finalized — `runtime.py`'s sink, at the
terminal `done` branch, beside the existing `_push_context_report` call
(`runtime.py:1522`). That site has the reply text on both paths (`msg.text`
from `accumulator.finalize()`, or `full_reply` on the echo path) and nothing
else does.

What it does **not** have is the `EditorContext`. `editor_context` is a local
in `_handle_send_message` (`runtime.py:810`), passed down into the executor,
and never recorded anywhere the sink can reach.

**Add `register_editor_context(task_id, ec)` / `editor_context_for(task_id)`
to `StreamManager`**, directly symmetric with the existing
`register_invocation` / `invocation_for` pair (`streaming\manager.py:36-40`),
and call it from the same place in `agents\executor.py:477` — which is
deliberately chosen: that call site's own comment says it registers there "so
the echo and refusal paths, which never register a stream, still have an
answer". A fill turn that gets refused takes the echo path, so anything keyed
off a registered *stream* would miss it. Clear it in `close_stream`, where
`_invocations` is already cleared (`manager.py:55`), or the dict leaks one
entry per fill turn for the life of the process.

Do **not** put `mode` on `InvocationMetadata`. That type is "which
workspace/capability/agent/provider/model produced this message" and is
persisted into every message row; editor mode is per-task transport state, not
message provenance, and adding it would change a schema-adjacent shape for a
reason unrelated to it.

Gate the parse on `editor_context is not None and editor_context.mode ==
MODE_FILL`. An explain-mode turn and a plain chat turn must not be parsed at
all — a plain reply that happens to be JSON is not a proposal.

### 5. `FillProposal` push event

Register in `OutgoingEventType`. Emitted **after** the terminal
`TaskDone`/`TaskFailed`/`TaskCancelled` event, not instead of it and not
before it:

- B6's rule is exactly one terminal event per task, and this is not one. A
  client that stops listening at the terminal event is not broken; it just has
  no diff card.
- Ordering matters the other way round too: F9's tray marks a task finished on
  the terminal event, and a proposal arriving first would describe a task the
  client still thinks is running.

Payload: `{"proposal": FillProposal.to_dict()}`, with `task_id` and
`session_id` set on the envelope as the terminal event already does.
`request_id` is the originating `SendMessage`'s, like the terminal event — this
is caused by a request, so unlike B20's push it is not a null `corr_id`.

**Emit nothing on a parse failure.** No `FillProposal` with a `null` proposal,
no failure event. Log it at WARNING with the reason slug and the truncated
text — that log line is the whole diagnostic surface for "why did no diff card
appear", and it needs to exist. A client distinguishes "no proposal" from "a
proposal" by whether the event arrived; a nullable payload would make every
consumer write the same `if proposal == nil` branch to express the same thing.

Emit a proposal only for a `done` turn. A failed or cancelled fill turn may
have salvaged partial text (`accumulator.finalize_interrupted`), and partial
JSON that happens to parse is the one case where a syntactically valid
proposal is semantically worthless — the model was cut off mid-`replacement`.

### 6. CLI surface, for the gate

`cli.py send` already takes `--file` and `--select` (`cli.py:9`). Add
`--json-proposal` (or print the parsed proposal in the existing `--wait`
output — record which you chose and why). This exists so the verification gate
can see the parsed structure without a frontend, the same way
`context report --json` served B20's gate. **One flag on an existing command,
not a new command.**

---

## Do NOT

- **Do not write any diff logic.** No line matching, no hunk computation, no
  three-way merge, nothing. F9's own spec says Go computes the diff, and that
  stays true. This episode produces the *input* to that computation.
- **Do not write any apply path.** No file writes, no buffer protocol, no
  `WriteProjectFile` event. There is deliberately no disk-write event in this
  backend and this episode does not add the first one. CLAUDE.md's first hard
  rule.
- **Do not build any UI.** The diff card is F9's, deferred until the Editor
  exists.
- **Do not change the fill-mode prompt.** If the model's compliance rate turns
  out to be poor, record the measured rate in the completed doc and leave the
  prompt alone — a prompt change and a new parser landing together make it
  impossible to tell which one the numbers describe.
- **Do not touch `resolve_scope()` or `_render_editor_context`.** This episode
  reads what they decided. If the shown region is not reachable from the sink
  without changing them, say so and stop rather than widening the episode.
- **Do not raise on a parse failure**, and do not route one through `Error`.
  §2's reasoning.
- **Do not reuse `agent_proposals`** (B11's table, migration v4). That is one
  row per candidate *fact* for memory, status `pending`, and B13's to
  reconcile. A fill proposal is a transient per-task artifact of a live turn —
  it is not durable state and **this episode persists nothing**. Same
  distinct-name discipline B9's `ContextPolicy` and B10's `BoundsOverride`
  established (decisions.md D97).
- **Do not persist the proposal.** No new table, no new column, no JSON blob
  in `messages.metadata`. The reply text is already persisted and the proposal
  is derivable from it; a stored copy is a second source of truth that goes
  stale the moment anyone edits the region. If a later episode needs a
  reloaded session to re-offer a diff card, that is its decision to make with
  its own reasons.

---

## Done when

1. A fill-mode turn whose reply is a well-formed JSON object produces exactly
   one `FillProposal` event, after the `TaskDone`, carrying all seven fields.
2. The same turn still produces its `TaskDone` and still persists its
   assistant message, byte-identical to what it persists today.
3. A fill-mode reply that is **not** JSON (a plain-text refusal) produces **no**
   `FillProposal` event, no `Error`, a normal `TaskDone`, and one WARNING log
   line naming reason `not_json`.
4. B11's one-file refusal (`agents\executor.py`'s `refusal_text`, which takes
   the **echo** path and never registers a stream) still arrives as a normal
   reply, and produces no proposal — proving `register_editor_context` is
   reachable from the echo path, not just the streamed one.
5. A reply whose `start_line`/`end_line` fall **outside** the shown region is
   rejected with reason `range_outside_shown` and produces no event — verified
   by hand-feeding the parser, since a live model will not reliably produce
   this on demand.
6. A reply naming a **different file** is rejected `file_mismatch`; a reply
   naming the same file by **bare basename** is accepted.
7. An **explain**-mode turn produces no proposal even if its reply happens to
   be valid JSON.
8. A plain chat turn (no `editor_context` at all) produces no proposal and
   takes no new code path.
9. `StreamManager`'s editor-context dict is empty after the task closes — no
   leak per fill turn.
10. `cli.py send --file <f> --select <n>` prints the parsed proposal, and its
    `start_line`/`end_line` are inside the `shown lines N-M` range the prompt
    header reported for the same turn.

Checks 5 and 6 are **unit-level on `parse_fill_proposal` directly**, not
end-to-end. That is deliberate: they are the checks whose failure mode is a
destructive write to the wrong lines, and a gate that can only exercise them
by hoping a 1.5B model misbehaves on cue is not a gate.

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md: one combined script, and Point's process never sees the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` path at all — `$env:APPDATA` and
`$env:LOCALAPPDATA` both point at a scratch directory for the one script's
duration. `$env:POINT_DB` alone is not enough. (The rename-aside pattern is
retired — CLAUDE.md's hard rules.)

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b24"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData      = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend

    uv run python cli.py migrate

    # --- a real file to fill into, inside the scratch tree, never the repo
    $target = Join-Path $scratch "sample.py"
    @'
def parse_config(path):
    with open(path) as f:
        return f.read()
'@ | Set-Content -LiteralPath $target -Encoding utf8

    $sid = (uv run python cli.py session new "b24").Split(" ")[0]

    # --- 1, 2, 10: a real fill turn against a real model
    uv run python cli.py send "@filler-explainer add an existence check" `
        --session $sid --file $target --select 1 --wait --json-proposal
    # EXPECT: one proposal printed, with start_line/end_line inside the
    #         `shown lines N-M` the prompt header reported, and a normal
    #         TaskDone. The assistant message still in the transcript:
    uv run python cli.py session messages $sid | Select-Object -Last 5

    # --- 3: a plain-text refusal produces no proposal
    uv run python cli.py send "@filler-explainer also rewrite utils.py" `
        --session $sid --file $target --select 1 --wait --json-proposal
    # EXPECT: no proposal, a normal reply, and in the log:
    Select-String -Path (Join-Path $scratch "Point\logs\backend.log") `
        -Pattern "fill proposal" | Select-Object -Last 5

    # --- 7: explain mode never parses
    uv run python cli.py send "@filler-explainer explain this" `
        --session $sid --file $target --select 1 --wait --json-proposal
    # EXPECT: no proposal, whatever the reply looks like

    # --- 8: a plain chat turn
    uv run python cli.py send "hello" --session $sid --wait --json-proposal
    # EXPECT: no proposal, no new code path entered

    # --- 4, 5, 6, 9: the validation table and the leak check, unit-level
    uv run python tests\manual_fill_proposal_test.py
    # EXPECT: every row PASS. This script is the episode's own, written here,
    #         and must cover at minimum: inverted range, range outside shown,
    #         different file, bare-basename same file, empty replacement
    #         (ACCEPTED), float/string line numbers (rejected bad_shape),
    #         fenced-JSON reply (accepted), JSON with commentary around it
    #         (rejected not_json), unknown extra key (accepted), and
    #         StreamManager empty after close_stream.
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

**Eyes-on:** confirm the `FillProposal` envelope arrives **after** the
`TaskDone` envelope, not before it — read the two `seq` values, do not infer
the order from the code.

**Record the measured compliance rate.** Run the fill turn five times against
the local model and write down how many replies parsed. That number is what
tells a later episode whether the prompt needs work, and it is unrecoverable
once the session is over.

---

## Handoff

- **F9's diff card stays deferred after this episode closes.** This clears one
  of its four blockers. The other three are unchanged: there is no Editor
  workspace to apply into (F13, Session 18), `context_attachments.source_type`
  is `file`-only so a task result cannot be attached (`managers\context.py:564`
  refuses anything else outright), and nothing writes `memory_records` so
  `m save to memory` has no backend (B13, Session 23). F9's diff-card session
  needs **this event and F13** both landed; its attach/save half needs **B13
  and a B12 attachment-source extension that is not yet an episode.**
- **The apply path's guard ordering is F9's to get right, and it is not
  obvious.** `CheckFileStale` resolves its project through
  `_resolve_optional_project`, which walks up from the **path** to
  auto-discover a project (`runtime.py:752`). That answers a different
  question from `_attachment_scope`, which answers "what will the builder
  actually use" from `session.project_id`. B12's bug #2 was exactly this
  drift — a path-resolved guard priced a file at ~718 tokens that the
  session-scoped guard then refused, spending 0 — and B23 explicitly declined
  to inherit `_resolve_optional_project` for `ListDirectory` for the same
  reason. For an **estimate** that drift costs a wrong number. For a
  **destructive apply** it means the write is guarded against whatever
  `.point\` happens to sit above the file rather than the root the session is
  bound to. The corrected order, which 374baa2 established for `AttachContext`
  and which F9's apply must follow: **resolve the session's scope first, guard
  against that, and commit nothing until the guard has passed.** F9 may
  therefore need `CheckFileStale` to accept a `session_id` — a small addition
  here or in F9's own backend dependency, not something F9 invents locally.
- **Staleness is a fact about a moment, and must be captured, not
  re-derived.** This episode already applies the rule once:
  `shown_start`/`shown_end` are carried from the context build rather than
  recomputed at the terminal event. F9's diff card inherits the same
  obligation on the other end — it must record the file's `(mtime, size)` **as
  of region generation**, re-`stat` at apply time, and compare, because
  `CheckFileStale` takes a caller-supplied `mtime` and will cheerfully confirm
  whatever it is handed. B12's `set_mode` *clears* its recorded `(mtime, size)`
  pair for precisely this reason, and F8's chip-staleness bug was the same
  mistake in the UI layer. Three episodes have now paid for this lesson; it
  belongs in the diff card's own spec, not in a handoff note.
- **The card's state transition must follow the buffer edit, not accompany
  it.** Since every apply goes into an in-memory buffer and never to disk, a
  failed apply is atomic by construction — but only if the card marks itself
  applied **after** the buffer edit returns. Marking it optimistically
  reproduces F8's chip bug exactly: a card that looks applied when it was not.
- **If the compliance rate is poor, that is a finding, not a bug in this
  episode.** Record it; do not fix it by changing the prompt in the same
  session (see Do NOT).
