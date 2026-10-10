# Episode B24 — The Filler's Proposal on the Wire ✅

**Parse the Filler-Explainer's fill-mode reply, validate it against the region
it was actually shown, and push the result as its own `FillProposal` event —
after the terminal `TaskDone`, never instead of it. No diff logic, no apply
path, no UI, no persistence, and no change to the fill-mode prompt. Measured
compliance against the real local model: 0/5, root-caused below — recorded
as a finding, not fixed here, per the episode's own Do-NOT list.**

> Verified by Aryan on 2026-10-10 against
> `tasks/backend/B24-filler-proposal-event.md`'s ten "Done when" criteria, in
> a scratch `%APPDATA%`/`%LOCALAPPDATA%` profile per CLAUDE.md's
> env-var-override pattern. One stray row was left in the real profile
> during verification — see "Minor finding" below; the real profile was
> otherwise never named in any destructive call.

---

## Where the code went

```
backend\
├── managers\
│   └── fill_proposal.py      NEW — FillProposal, FillParseFailure,
│                              parse_fill_proposal(). Fence-stripping (whole-
│                              reply-is-one-fence only, never hunts embedded
│                              JSON), strict shape checks (`type(x) is int`
│                              rejects bool/float/str), the three range/file
│                              validations, bare-basename vs. full-resolve
│                              file matching. Not in editor_context.py (the
│                              request side) -- this is the response side,
│                              and that module is already imported by
│                              context.py
├── streaming\
│   └── manager.py            + RegisteredEditorContext (frozen dataclass:
│                              context, shown_start, shown_end) and
│                              register_editor_context()/editor_context_for(),
│                              keyed by task_id exactly like _invocations --
│                              the echo/refusal path never registers a
│                              stream either. Cleared in close_stream()
├── agents\
│   └── executor.py           + _shown_range_from_built() -- regexes
│                              `lines (\d+)-(\d+)` out of the task_state
│                              IncludedItem's .detail that context.build()
│                              already produced. Zero changes to
│                              resolve_scope()/_render_editor_context()
│                              (both stayed on the Do-NOT list). Registered
│                              beside register_invocation(), gated on
│                              `editor_context.mode == MODE_FILL`
├── runtime\
│   ├── events.py              + FILL_PROPOSAL = "FillProposal"
│   └── runtime.py             sink captures ec_record/reply text BEFORE
│                              close_stream() pops them (both branches
│                              already called close_stream() before the
│                              terminal event emit); pushes FillProposal
│                              AFTER the terminal event, only on
│                              kind == "done"; _push_fill_proposal() parses
│                              and logs one WARNING on failure, emits
│                              nothing on failure
├── cli.py                     + send --json-proposal, and
│                              _drain_for_fill_proposal() -- a ~2s bounded
│                              peek on runtime.outbox filtered by task_id,
│                              because call()'s until() already stops
│                              draining at TaskDone, one envelope before
│                              FillProposal arrives
└── tests\
    └── manual_fill_proposal_test.py   NEW -- 14 unit rows, no DB, no
                              filesystem beyond EditorContext literals;
                              see Manual Test Log
```

---

## Key decisions

### 1. `shown_start`/`shown_end` reach the sink without touching `resolve_scope()` or `_render_editor_context()`

Both are on the Do-NOT list, and neither returns the shown range in any
structured form — only as substrings of the `detail` string
`_render_editor_context` already returns (`managers\context.py:704`):
`f"editor context: {name} mode={editor_context.mode} lines {start}-{end}"`.

`agents\executor.py`'s `_shown_range_from_built()` finds the single
`task_state`-section `IncludedItem` in `built.log.included` (confirmed
structurally — see "Guardrails" below — that there is never more than one)
and regexes the range out of its `.detail`. This is the same technique
`providers\demo\provider.py` already uses on the same function's rendered
output (`_FILL_MARKER`/`_EXPLAIN_MARKER` scanning `mode=fill`/`mode=explain`)
— reading what the function decided, never touching it.

`EditorContext` itself gained no fields. `RegisteredEditorContext`
(`streaming\manager.py`) is a separate, backend-only pairing of the
request-side `EditorContext` with the shown range — the same
distinct-name discipline B9's `ContextPolicy` and B10's `BoundsOverride`
established (decisions.md D97).

### 2. `cli.py send --json-proposal`'s grace-drain, not a `call()` timeout

`FillProposal` is pushed after `TaskDone`, but `call()`'s `until()` already
returns `True` on `TaskDone` — the drain loop exits one envelope before
`FillProposal` would arrive. `_drain_for_fill_proposal()` does one small
bounded peek (~2s) directly on `runtime.outbox` after `call()` returns,
filtered by `task_id` exactly like `call()`'s own `match()` — confirmed
necessary, not paranoid: a background (`max_steps>1`) agent started by an
earlier `--wait`-less send can still be running in the same long-lived
process, sharing the same outbox.

---

## Doc-vs-reality mismatches found — in B24's own verification script and "eyes-on" instructions, not in the implementation

1. **`--select` does not trigger fill mode; `--cursor` does.**
   `cli.py:_resolve_mode` (lines 273–291, pre-existing B11-era code):
   `--select` alone resolves `MODE_EXPLAIN`; only `--cursor` alone resolves
   `MODE_FILL`. B24's own verification script (and its Done-when #10) used
   `--select` for every fill-mode exercise — as literally written, none of
   them ever entered the fill path at all. Confirmed by direct testing
   (identical echo-provider turn: no-ops under `--select 1`, produces a
   parsed proposal under `--cursor 1`). The gate Aryan actually ran used
   `--cursor` for the fill-mode steps and kept `--select` for the
   explain-mode step, which is correct there.
2. **The log file is `point.log`, not `backend.log`.**
   `appconfig.log_path()` returns `state_dir() / "logs" / "point.log"`
   (`appconfig.py:78-79`). The gate's original `Select-String` step named
   `backend.log`, which does not exist under any profile.
3. **`seq` cannot be compared between `TaskDone` and `FillProposal` —
   it is structurally `null` on both.** `runtime\events.py`'s own
   docstring (lines 30-33): "`seq` is the per-task stream sequence... It
   is null on envelopes that are not part of a task's token stream; a
   lifecycle event is identified by its type, not by counting." Confirmed
   with a raw envelope dump: both envelopes carried `"seq": null` and the
   same `ts` to the microsecond (emitted in the same synchronous call
   stack, no intervening `await`). The real ordering guarantee is FIFO
   arrival order from a single producer on a single `asyncio.Queue` —
   `runtime.py`'s sink issues `await self._emit(TaskDone)` to completion
   before it ever reaches `_push_fill_proposal`'s own `await
   self._emit(FillProposal)` — confirmed both by reading that code and by
   the dump's arrival order.

---

## Guardrails re-confirmed directly from the code (not inferred from test behavior)

1. **No singular `"line N"` form exists.** `_render_editor_context`
   (`managers\context.py:641-705`) has exactly three `return` statements;
   the two failure paths (`PathDeniedError`, unreadable file) return no
   "lines" text at all, and the one success path's `detail`
   (line 704) is a single unconditional f-string built *after* the
   fill/explain `header` branches rejoin — always `"lines {start}-{end}"`,
   even when `start == end`. `resolve_scope()` is typed
   `-> tuple[int, int]` and every one of its return paths
   (`_find_enclosing`, `_fallback_range`, `_clamp`) returns a 2-tuple,
   never a collapsed single int.
2. **`built.log.included` can carry at most one `task_state` item per
   build.** `SECTION_TASK_STATE` appears at exactly four sites in
   `managers\context.py`: the constant, its `SECTION_ORDER` slot (used only
   for zero-initializing report dicts), and two mutually exclusive
   `log.included.append(...)` branches (lines 1041, 1058) of one
   `if/else` guarding the single call to `_render_task_state()`
   (line 1037) — not inside a loop, and that function has exactly one
   call site. The budget-eviction branch (line 1050) appends to
   `log.evicted` instead, which `_shown_range_from_built()`'s "no match
   found" path already treats as a legitimate, non-raising "nothing to
   find" outcome, not a second candidate to choose between.

---

## Manual Test Log (Episode B24)

| Check | Result |
|---|---|
| Unit-level validation table, shape, fences/prose (`tests\manual_fill_proposal_test.py`) | ✅ 14/14 rows PASS — inverted range, range outside shown, file mismatch (same-basename-different-dir and plainly different), bare-basename accept, empty replacement accepted, float/string line numbers rejected `bad_shape`, unknown extra key ignored, fenced-JSON accepted, prose-wrapped JSON and plain-text refusal both rejected `not_json`, `StreamManager` reachable without `register_stream` (echo-path proof) and leak-free after `close_stream` |
| `shown_start`/`shown_end` wiring | ✅ confirmed correct via the `echo` provider's deterministic accept case, in both the original dry run and the later raw-envelope dump — printed `shown_start`/`shown_end` matched the actual 3-line sample file's resolved enclosing range |
| Rejection-slug list, confirmed exhaustive against `fill_proposal.py` | ✅ `not_json`, `bad_shape`, `range_inverted`, `range_outside_shown`, `file_mismatch` — no sixth reason exists in the module |
| `FillProposal` pushed after `TaskDone`, never instead of it | ✅ confirmed by raw envelope dump (arrival order) and by the structural call-sequencing argument above |
| Explain-mode and plain-chat turns produce no proposal | ✅ gated in `agents\executor.py` on `editor_context.mode == MODE_FILL`; never reaches `parse_fill_proposal` otherwise |
| Real-model compliance against `qwen2.5:3b` | ❌ **0/5** across two full runs (identical failure distribution both times): 4× `range_outside_shown`, 1× `file_mismatch`. Root cause: the model reports `end_line` as the **length of its own replacement text**, not the span of the original shown region — e.g. shown `lines 1-3`, a 7-line replacement comes back with `end_line: 7`. This is a prompt-compliance finding, not a parser bug: `parse_fill_proposal` rejected every one of these correctly, exactly per spec (§3's validation). Per the episode's own Do-NOT list ("do not change the fill-mode prompt... a prompt change and a new parser landing together make it impossible to tell which one the numbers describe"), the prompt is untouched; this number is left for whoever next tunes `managers\context.py`'s fill-mode instructions to improve on |

---

## Minor finding: a stray session in the real profile

`b24-seqcheck` was created directly against the real `%APPDATA%`/
`%LOCALAPPDATA%` profile when a scratch-wrapper command was run outside its
`try` block during the seq-ordering verification. `cli.py` has no `session
delete` subcommand (`{new, list, messages}` only), so it was left in place
rather than worked around with an unsupported deletion path. It is an empty
row (no messages, `echo`/`echo` provider) and does not affect anything —
recorded here so it isn't mistaken for a mystery session later. Did not
warrant adding a `session delete` command to clear it; that is out of this
episode's Build section, and a destructive new CLI surface built solely to
clean up one row would be exactly the kind of parked-item scope creep
CLAUDE.md's hard rules warn against.

---

## What the next episode inherits

- **F9's diff card stays deferred**, exactly as the task doc anticipated.
  This episode clears one of its four blockers (the structured
  `FillProposal` event now exists on the wire). The other three are
  unchanged: no Editor workspace to apply into (F13), `context_attachments.
  source_type` is still `file`-only, and nothing writes `memory_records`.
- **Fill-mode prompt compliance is now a measured, not assumed, problem.**
  0/5 against `qwen2.5:3b`, with a specific, reproducible failure pattern
  (the model conflates `end_line` with replacement length rather than
  original-region span). Whoever revisits `managers\context.py`'s fill-mode
  instructions should consider making the "must fall within the shown
  region" instruction state the region's own line count explicitly, or
  restate `end_line`'s meaning more defensively — but that is a prompt
  change, deliberately not made in this episode, and should land alone so
  its effect on this number is measurable in isolation.
- **The `--select`/`--cursor` distinction is easy to get wrong when
  constructing a fill-mode invocation from the CLI or from a script.**
  Anyone writing a new verification script or tool against `cli.py send`
  should use `--cursor` for fill mode and `--select` for explain mode;
  `--mode fill|explain` always wins if ambiguity needs to be forced
  explicitly.
- **`RegisteredEditorContext` and `register_editor_context`/
  `editor_context_for` are reusable plumbing** for any future per-task
  state that needs to survive the echo/refusal path the same way
  `_invocations` and now this do — keyed by `task_id`, cleared in
  `close_stream()`.
