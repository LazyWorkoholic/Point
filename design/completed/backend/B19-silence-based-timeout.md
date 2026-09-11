# Episode B19 — Silence-Based Agent Timeout ✅

**`timeout_s` is a SILENCE budget now, not a wall clock. It resets on every
sign of life — a provider chunk, a tool event, a rate-limit retry notice, a
tool call in flight — so a task dies only on genuine dead air and never on
continuous output, however long that output takes. A separate, much larger
total-duration ceiling keeps B11's runaway-spiral defense intact, because a
silence reset alone cannot provide one.**

> Verified by Aryan against `tasks/backend/B19-silence-based-timeout.md`'s
> five "Done when" criteria and its own three-part gate, on a real Ollama
> model. A research task ran a full **6:45** without being killed — past the
> old 180s ceiling by more than three times — and a mid-run tool failure was
> correctly recovered from within budget. Test 3's new backstop wording
> (`stopped after 48s total: this agent never finished, and never went quiet
> enough to time out on its own`) confirmed correct.
>
> **The gate's Test 2 reported a failure that turned out not to be one.** A
> silence timeout appeared to persist empty `outcome` and `error_code`. It
> did not: the query was `messages ORDER BY rowid DESC LIMIT 1` with no role
> filter, run against a scratch DB several sends had already written to, so
> it landed on a row that legitimately has NULL in both columns. Chasing it
> anyway was worth it — it produced the automated regression test below, and
> it found a real defect in WHERE the timeout fired (see "The fix is about
> when, not what").

---

## Where the code went

Backend only. The frontend's own watchdog (`chat\stream.go`) was already
correct and stays untouched — this episode brings the backend in line with
it, which is the direction the task doc's Do-NOT names explicitly.

```
backend\
├── agents\executor.py          _FIRST_TOKEN_GRACE_FACTOR, _HARD_CEILING_FACTOR;
│                               last_activity + touch() replacing the fixed
│                               deadline; the stream iterated by hand under
│                               asyncio.wait_for; _check_deadline rewritten;
│                               + _silence_budget, _silence_exceeded
└── tests\
    ├── fixtures\mock_silent_gap.json    NEW — partial output, then dead air
    ├── fixtures\mock_cold_silence.json  NEW — dead air from the first moment
    ├── fixtures\mock_never_silent.json  NEW — continuous output, never a gap
    └── manual_b19_silence_timeout.py    NEW — all three, mock-driven
```

---

## The mechanism

`deadline = time.monotonic() + limits.timeout_s`, set once at task start, is
gone. In its place:

| Name | What it is | Reset by activity? |
|---|---|---|
| `last_activity` | the clock the silence budget measures against | **yes** — `touch()` |
| `first_token_seen` | one-way flag, set by the first provider chunk | n/a |
| `hard_deadline` | `_HARD_CEILING_FACTOR × timeout_s` from task start | **no** |

`touch()` fires at every point the loop already treats as "the task is
alive": each provider chunk, each tool event drained from `pending_events`,
each poll tick while a tool call is in flight, and each rate-limit retry
notice. A rate-limited backoff and a slow tool call are both time passing
without provider output, but neither is dead air — the task is actively
managing a known, bounded wait, and ToolExecutor has its own internal
timeout governing the second one.

`_silence_budget` is `timeout_s`, doubled until the first chunk arrives.
That mirrors `chat\stream.go`'s `firstTokenGraceFactor` deliberately, and by
the same reasoning: a cold local model being loaded from disk is the one
silence that is expected.

## The fix is about WHEN the check fires, not what it does

This is the part worth keeping.

The obvious implementation — keep the per-chunk `_check_deadline` call, just
compare against `last_activity` instead of a fixed deadline — is wrong, and
subtly so. `async for` blocks inside `__anext__` until the provider speaks,
so **the only moment a per-chunk check can run is the moment the silence
ends.** A check that fires there kills a task at the instant it proved
itself alive, and it fires *before* that chunk has reached the accumulator,
so the chunk is discarded too.

On a cold start that is not a cosmetic difference. Nothing has accumulated,
`StreamAccumulator.finalize_interrupted` returns `None` for empty text (B12's
documented policy — an empty `AssistantMessage` fails B3 validation), so **no
assistant row is written at all**, and the `outcome`/`error_code` columns the
whole three-way split depends on are never reached. That is what Test 2 was
circling, even though the row it actually read was a different one.

So the stream is iterated by hand:

```python
chunks = stream.__aiter__()
while True:
    try:
        provider_chunk = await asyncio.wait_for(
            chunks.__anext__(), timeout=_silence_budget(first_token_seen, limits),
        )
    except StopAsyncIteration:
        break
    except asyncio.TimeoutError:
        raise _silence_exceeded(first_token_seen, limits) from None

    touch(first_token=True)
    _check_deadline(last_activity, first_token_seen, hard_deadline, limits)
```

Timing the **wait** out means the budget fires *during* the silence rather
than on its recovery, and whatever arrived before the gap is already in the
accumulator and persists with its outcome intact. The check that remains in
the loop body is the hard ceiling, which is the one thing a chunk's arrival
cannot invalidate.

`_silence_exceeded` exists so the `bound="timeout_s"` that `runtime.py`'s
`_outcome_for` keys `Outcome.TIMED_OUT` on is constructed in exactly one
place. Two constructions would be two chances for it to drift, and the
outcome columns going missing is precisely the failure this episode's own
gate was written to catch.

## What still catches a runaway spiral — confirmed, not assumed

**B11's defense holds. The mechanism is `_HARD_CEILING_FACTOR`, and nothing
else.** Verified by execution, not by argument — see scenario 3 of
`tests\manual_b19_silence_timeout.py`, which streams 10 seconds of
continuous output with no gap longer than 0.25s (a silence budget cannot
fire against that, by construction):

```
=== 10s of continuous output, never a gap (the runaway spiral)
  terminal: ['TaskFailed']  after 6.3s
  rows: [('user', 8, None, None),
         ('assistant', 198, <Outcome.TIMED_OUT: 'timed_out'>, 'bounds_exceeded')]
  [PASS] a stream that never goes quiet is still terminated
  [PASS] the TOTAL-duration ceiling fired, not the silence budget
  [PASS] it fired at the ceiling, before the stream would have ended on its own
  [PASS] the partial text up to the cut is preserved
```

Cut at 6.3s against a 6s ceiling (`timeout_s=1`), with its partial text kept
and the ordinary outcome vocabulary intact. Had the ceiling not existed, the
task would have run the full 10s to completion and B11's defense would have
been silently gone — which is exactly the regression this scenario exists to
catch, and why it is worth a fixture of its own rather than a paragraph.

The task doc's Build §3 asked whether the existing bounds already covered
this. They do not: **neither `max_steps` nor `max_tool_calls` covers it.**

- `max_steps` bounds the number of separate `provider.stream()` calls, not
  the length of any one of them. A single degenerate generation is one step.
- `max_tool_calls` is unrelated to raw text volume.

A pure silence reset therefore leaves a model that emits tokens in a tight,
meaningless loop — B11's self-tested qwen2.5:1.5b ramble — running forever,
because it never goes quiet. `_HARD_CEILING_FACTOR = 6` is the backstop: a
total-duration cap from task start, never reset, generous enough that no
healthy generation should reach it (general 6min, research 18min; the real
6:45 research run above sits comfortably inside research's ceiling) but
present so a stream that never goes silent also never runs unbounded.

**6 specifically**, not something larger: `tests\manual_b11_deadline_check.ps1`
exercises this backstop with `--max-seconds 8` under its own CLI `--timeout
60` wait budget. 6× resolves that scenario at 48s, still inside 60s, so that
existing script keeps working unmodified.

**A behaviour change worth knowing:** that same `--max-seconds 8` scenario
used to be cut off at ~8s and is now cut off at ~48s. The property B11 built
the check for — a runaway spiral terminates — holds; the moment it
terminates moved, by design, because the ordinary budget no longer fires on
elapsed time at all.

## Outcome vocabulary: unchanged

Both raise paths produce the same `BoundsExceededError(bound="timeout_s",
code="bounds_exceeded")` they did before, so `_outcome_for` maps them to
`Outcome.TIMED_OUT` exactly as it did, and the frontend's three-way
rendering split is untouched. Only the error *prose* changed, and it had to:
"stopped after 60s" was a lie about a task that ran five minutes with one
60-second gap in it.

## The regression test

`tests\manual_b19_silence_timeout.py`, driven in-process against the mock
provider — no Ollama, no key, no network. A regression test for a
termination path is worth nothing if running it depends on a live model
being warm. Exits non-zero on failure, so a gate can branch on it.

Two scenarios, because the symptom Test 2 reported has two different things
behind it:

1. **Silence after partial output** (`mock_silent_gap.json`): three quick
   chunks, then a 6s gap. Asserts the wire carries `code=bounds_exceeded` +
   `outcome=timed_out`, that the error names *silence* rather than elapsed
   time, and — the actual regression — that the persisted row carries
   `outcome=timed_out`, `error_code=bounds_exceeded`, `interrupted=1` and
   the partial text.
2. **Silence before any output** (`mock_cold_silence.json`): dead air from
   the first moment. Asserts that no assistant row is written (B12's policy),
   that the last `messages` row is therefore the USER row with NULL in both
   columns — *the exact shape a role-blind gate query misreads as a bug* —
   and that the `tasks` row carries the ending instead, which is what S11b's
   `orphanReply` join already reads.

**The test was confirmed RED before it was trusted.** Run against the
pre-fix ordering it fails on "it timed out on SILENCE, not on elapsed time"
— the hard ceiling fires instead, because the silence budget could only be
evaluated when a chunk arrived and the next chunk was on the far side of the
gap. A regression test never seen red proves nothing.

## Note for whoever writes the next gate

`SELECT ... FROM messages ORDER BY rowid DESC LIMIT 1` has no role filter.
Against a scratch DB that more than one send has written to, it does not
mean "the reply I just made" — it means "whatever row was written last",
which is frequently the *user* row, whose NULL `outcome`/`error_code` are
correct. Filter by `role='assistant'`, and for a task that produced no text
at all read `tasks` instead, since by design there is no assistant row to
read.

## What the next episode inherits

- **`timeout_s` means silence everywhere now.** Frontend and backend use the
  same mechanism, the same first-token grace concept, and the same word for
  it. The frontend's 120s budget remains the second, independent layer for a
  failure the backend cannot see (a hung connection between backend and
  provider, where `__anext__` never returns *and* `wait_for` is the only
  thing that can notice — which it now is).
- **The hard ceiling is the only remaining wall-clock bound**, and it is
  deliberately far from any healthy run. If a legitimate task ever hits it,
  that is a signal to raise the factor, not to reintroduce an elapsed-time
  check on the ordinary path.
- **Nothing about the outcome vocabulary moved**, so F5/S11b/B12's rendering
  and reload paths need no attention.
