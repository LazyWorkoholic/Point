# Episode B14 — Usage & Limits ✅

**Every completed request now writes exactly one `usage_records` row —
schema v2's table existed since B6 and nothing wrote it until this
episode. Pricing comes from config.toml's `[pricing.<provider>."<model>"]`
table, in dollars per million tokens, and is `NULL` whenever a model is
not priced there — never a guessed figure. A single global, no-identity
daily token cap (`[limits] daily_token_cap`) refuses the *next* request
once today's already-recorded total is at or past it; it cannot stop the
request that crosses the cap, because that request's own cost is not
knowable until the provider responds. The UI's push event
(`UsageTotalsUpdated`) and the CLI (`usage session|today|by-agent`) both
read the same numbers F9a's result card already sums from
`messages.input_tokens`/`output_tokens`, so there is no second arithmetic
to disagree with it.**

> Verified in an isolated scratch `%APPDATA%`/`%LOCALAPPDATA%` profile
> per CLAUDE.md's hard rule (never the live profile), through
> `tests\manual_b14_usage.ps1`'s five sections, all against the mock
> provider for determinism plus one additional real send through a
> locally running Ollama model for the pricing check below. The script
> was run fresh from its committed path, with full raw output reviewed
> line by line against each section's stated EXPECT, not just reported
> as passing — see "Manual Test Log" below for the actual numbers.
>
> **The episode's own spec assumed infrastructure this repo does not
> have, and that was raised before any code was written.** B14's task
> doc (`tasks/backend/B14-usage-limits.md`) specifies `limits.users.guest`,
> a `--as guest` CLI flag, "the owner is exempt", and "a guest's own key
> does not count" — all of which require a request to carry an identity.
> This backend has none: grepped for `guest`/`owner`/`user_id`/
> `current_user` across the whole backend, zero hits outside comments.
> That design belongs to **B16 (SSH Serving)**, which depends on B15 and
> F12, not on B12 — two episodes after this one in the dependency graph.
> Aryan confirmed: drop the guest/owner model from this episode entirely,
> do not build a stub role for it, and build a single global cap instead
> — the no-identity case B16 adds a user filter on top of, not a
> placeholder for B16 to tear out. See "The no-identity scope" below.
>
> **The task doc's premise about `usage_records` was also wrong, and so
> was `INDEX.md`'s.** Both said the table "exists and is written per
> turn". Checked against the live database directly: 112 `messages` rows,
> **0** `usage_records` rows, and no file in the backend contains
> `usage_records`/`cost_micros`/`INSERT INTO usage` outside the migration
> that creates the table. `INDEX.md`'s B14 row has been corrected to say
> so, in line with this file's own standing rule that the tree is truth.

---

## Where the code went

No new top-level package. `UsageRepository` is storage, `UsageManager` is
the pricing/cap policy above it — the same split `AttachmentRepository`/
`attachment_manager.py` already established.

```
backend\
├── agents\executor.py        + `usage: Optional[UsageManager]` ctor param.
│                              `check_cap()` called FIRST in `run()` --
│                              before `context.build()`, before the user
│                              message is persisted, before a task exists.
│                              `echo` exempt (no provider call, nothing to
│                              protect a wallet from)
├── runtime\
│   ├── errors.py              + UsageCapExceededError. Same `code`/
│   │                          attribute convention as ContextOverflowError
│   ├── events.py              + USAGE_TOTALS_UPDATED ("UsageTotalsUpdated"),
│   │                          deliberately NOT a reuse of the existing
│   │                          USAGE_UPDATED (that one is a per-chunk
│   │                          passthrough of one provider turn's own
│   │                          usage delta, mid-stream; this is a push of
│   │                          running totals after the fact -- same
│   │                          reasoning PROJECT_FILE_WRITTEN's own comment
│   │                          gives for not reusing PROJECT_FILE)
│   └── runtime.py             + `self.usage_repo` / `self.usage`, wired
│                              into AgentExecutor's constructor.
│                              `_record_usage(task, msg)` called right
│                              after both `persisted = True` sites (the
│                              streamed-accumulator branch and the
│                              echo/refusal branch); `_push_usage_totals`
│                              alongside the existing `_push_context_report`
├── database\
│   └── usage_repository.py    NEW -- UsageRecord, UsageRepository: insert,
│                              session_totals, task_totals, totals_since,
│                              by_agent, by_provider, latest_for_session
├── managers\
│   └── usage.py                NEW -- UsageManager: cost_micros_for()
│                              (config lookup, NULL on no price),
│                              daily_cap_tokens(), check_cap(),
│                              remaining_cap(), the aggregation pass-
│                              throughs
├── cli.py                     + `usage session [--session] [--last]`
│                              + `usage today`
│                              + `usage by-agent`
└── tests\
    └── manual_b14_usage.ps1   NEW -- 5 sections, scratch profile, mock
                              provider (determinism) + real Ollama
                              (pricing check below)
```

`database/migrations.py` is untouched — `usage_records` has existed since
schema v2; this episode is the first thing that writes to it.

---

## Why `check_cap()` runs where it does

`AgentExecutor.run()` already had a precedent for "refuse before a task
exists": `ToolsUnsupportedError` is raised there when an agent is granted
tools a provider cannot take, specifically **before** `context.build()`,
before the user message is persisted, and before `task_id` is generated.
`check_cap()` is the first line in the method, ahead of even that check —
cheapest first, and nothing downstream should run at all if today's
spend is already over the line.

`echo` is exempt by the same reasoning the provider-lane code already
uses for it: no real provider call happens, so there is nothing a cap
could be protecting. Exempting it also keeps `usage_records` consistent
with `messages.input_tokens`/`output_tokens`, which are genuinely `0` for
an echoed reply — see the estimation fallback below for why that
distinction mattered in practice, not just in theory.

## The cap's actual guarantee, stated precisely

This is a limitation of the mechanism, not a footnote on it.

The real cost of a call — in tokens, let alone `cost_micros` — is not
knowable until the provider responds. `GenerationRequest.max_tokens` is
`Optional[int] = None` (`providers\models.py`), and only
`anthropic\provider.py` defaults an unset one to 4096; `openai` and
`openrouter` pass `None` straight through to the API, so there is no
upper bound to check against before most calls even go out.

So `check_cap()` enforces exactly one thing: **before a task is created,
before the user's message is persisted, and before any provider call is
made, compare today's already-recorded total (exact, from
`usage_records`, not an estimate) to the cap.** If that alone is at or
past the cap, refuse.

What that means in practice, demonstrated by the gate's section 4:

- A send that starts **under** the cap is allowed to run, even if its own
  cost — unknown until it returns — pushes the running total **past**
  the cap. That send is not refused. Nothing could have refused it in
  advance, because its size was not known in advance.
- The **next** send, once the recorded total is already at or past the
  cap, is refused before anything happens — no task, no persisted user
  message, no provider call, no new `usage_records` row.

This is "stops the next request once a completed turn's actual usage
pushed the total over" — not "never crosses the cap on the request that
pushes it over". `UsageCapExceededError`'s own docstring states the same
distinction, so a reader hitting the exception does not have to find this
doc to know what it actually promises.

## The no-identity scope

There is no per-user attribution anywhere in this backend yet. B14's task
doc specified `limits.users.guest` / `--as guest` / "the owner is exempt"
— B16 (SSH Serving)'s design, written two episodes early into a backend
that has no connection-level identity to hang a cap on. Per Aryan's
decision, none of that was built here, including a stub role: inventing
one would be identity this codebase does not have, and the real one
belongs in B16's own task doc (`B16-ssh-serving.md`) and `decisions.md`
D66–D69.

What shipped instead is `[limits] daily_token_cap`, global, no `WHERE` on
any user column, summed over `usage_records` since server-local
midnight. **This is not a placeholder B16 tears out.** When B16 adds
per-user attribution (a `user_id` column, presumably), it adds a second,
narrower cap that filters the same query by user — this global one keeps
working underneath it as the overall ceiling on the wallet, exactly the
role `decisions.md` D68 describes ("a hard cap that refuses before
spending... the cap protects a wallet, not behaviour").

`daily_token_cap` absent, or `0`, means **no cap**, not a zero budget —
an empty `[limits]` table, or one written as `daily_token_cap = 0`, must
not silently block every request the moment the key exists, since nobody
configures a real cap of zero tokens. Verified in the gate's section 5.

## Capture: deriving from the same number F9a's card already sums

`_record_usage(task, msg)` runs right after `msg` is persisted via
`sessions.append_message(msg)`, in both places that happens (the
streamed-reply branch and the echo/refusal branch). Its numbers come from
`msg.usage.input_tokens`/`output_tokens` — the **same** `TokenUsage` that
becomes `messages.input_tokens`/`output_tokens`/`total_tokens`, which is
what F9a's result card sums to show "12.4k tok" on a finished task.
There is no second computation of a turn's tokens anywhere; this episode
could not have introduced a second number that disagrees with the first,
because it reads the first one.

### The Ollama estimation fallback, and why `echo` is exempt from it

A completed reply with text in it necessarily spent at least one real
output token. So `output_tokens == 0` on a message that has text can only
mean the provider never reported usage at all — not that nothing was
spent. Falls back to `managers\context.py`'s existing `estimate_tokens()`
(`len(text) // 4`), the same estimator the context builder already uses,
and sets `estimated = 1`.

Every real provider in this repo today **does** report usage —
`providers\anthropic\provider.py`, `openai`, `gemini`, `openrouter` and
`ollama` all construct a real `TokenUsage` from their own response —
so in practice this path is for the case the episode's own verification
names explicitly: some Ollama responses omit `eval_count`. It has not
been observed in this repo; it is built because the task doc's Done-when
#4 and its manual verification both call it out by name.

**`echo` is exempted from this fallback, not just from the cap.**
`output_tokens == 0` for an echoed reply is not an omission — no provider
was ever called, so `0` is the true, correct value, and
`messages.output_tokens` for that same row is genuinely `0` too (echo
never populates `TokenUsage`). Applying the fallback there would
estimate a non-zero output count from the echoed text while
`messages.output_tokens` stayed at `0` — the exact kind of disagreement
this episode's own design goal (one source for F9a's card and this table)
exists to prevent. Checked by provider name at the capture site, not
inferred.

## The wire event: `UsageTotalsUpdated`, not a reuse of `UsageUpdated`

`OutgoingEventType.USAGE_UPDATED` ("UsageUpdated") already existed and is
used for exactly one thing: a per-chunk passthrough of a single provider
turn's own usage delta, mapped straight off `StreamEventType.USAGE_UPDATED`
mid-stream. This episode's push — session totals plus the cap's remaining
headroom, sent once per completed turn, right after `_push_context_report`
— is a different payload answering a different question. Overloading the
existing type would make every decoder branch on which keys actually
arrived, which is the same reason `PROJECT_FILE_WRITTEN`'s own comment
gives for not reusing `PROJECT_FILE`. A new type, `UsageTotalsUpdated`,
instead.

## CLI: `usage session|today|by-agent`

All three read `runtime.usage` directly in-process — the same shape
`context report`'s default path uses for `context.report()` — rather than
a new wire request/reply pair. The task doc asked only for a push event
(`usage.updated`) plus these three CLI verbs; no `GetUsage` command was
specified, so none was added. `by-provider` exists on `UsageManager`/
`UsageRepository` (the task doc's "Aggregation queries" section lists it
alongside `by_agent`) but was not wired to a CLI verb, because the CLI
line in Build only names `session`, `today`, `by-agent`.

---

## Real-world pricing: one dated, measured entry, not five placeholders

The task doc's Build section asks to "ship the file with the five
providers' model names present and prices at 0.00" — but **"the file"**,
`config.toml`, is explicitly "user-owned, NEVER rewritten"
(`CLAUDE.md`'s repo-layout rule), and this repo ships no example/template
`config.toml` at all; nothing auto-writes one today. Writing a five-row
stub into Aryan's real file would have been exactly the kind of silent
rewrite that rule forbids.

What is actually in the real `%APPDATA%\Point\config.toml` as of this
episode's close is **one** entry, for the **one** model actually
configured there:

```toml
[pricing.ollama."qwen2.5:1.5b-8k"]
input_per_mtok  = 0.00  # as of 2026-10-25 -- self-hosted, no metered API
output_per_mtok = 0.00  # as of 2026-10-25 -- self-hosted, no metered API
```

This is a **measured** `$0.00`, not an unfilled placeholder: Ollama runs
locally with no metered API to price, so zero is the actual cost, not a
number nobody has looked up yet. It is dated, per the task doc's own
closing instruction ("record the pricing source you used and the date").
Verified against a real send through the real local Ollama daemon (not
the mock provider) in an isolated scratch copy of the profile — see
"Manual Test Log" below. `anthropic`/`openai`/`gemini`/`openrouter` are
deliberately left out of `[pricing]` until one is actually configured and
used; adding priced entries for providers nobody has a key for would be
inventing numbers ahead of the thing CLAUDE.md's hard rule already warns
against ("a hardcoded table becomes a lie").

---

## Manual Test Log (Episode B14)

`tests\manual_b14_usage.ps1`, one scratch profile, five sections, all
PASS, reviewed line by line against each section's stated EXPECT:

| Section | Covers | Result |
|---|---|---|
| 1 | Capture: one send -> exactly one priced `usage_records` row | PASS — `mock\|mock-1\|11\|5\|21\|0` (cost = 11×1.00 + 5×2.00 = 21 micros) |
| 2 | Session totals equal the sum of that session's rows | PASS — manager's `session_totals` (11, 5) exactly matches `SUM(input_tokens)`/`SUM(output_tokens)` over the raw rows |
| 3 | Unknown model -> `NULL` cost, no crash | PASS — `mock-slow\|` (empty `cost_micros`); noted in the script's own output that this send goes through the real capture path end to end, not a direct row insert |
| 4 | The daily cap — before vs after | PASS, both halves: send #2 (16 tok, starting at 16/30) is **allowed** and pushes the total to 32/30 — not refused, because its own size could not have been known in advance; send #3 (starting at 32/30, already over) is refused **before** any provider call — `usage_records` count unchanged (2 before, 2 after), exit code 1, error text names both numbers (`"today's usage (32 tokens) has reached the daily cap (30 tokens)"`) |
| 5 | `daily_token_cap = 0` means unlimited, not a zero budget | PASS — with today's total already at 32 (section 4's leftover state, far past any real nonzero cap this script used), a send with `daily_token_cap = 0` configured went through cleanly: exit code 0, total advanced 32 → 48, `usage today` reported "no daily cap configured" |

Plus, outside the gate script: one real send through the real, locally
running Ollama daemon (`qwen2.5:1.5b-8k`, no mock, no script) in a
second isolated scratch profile carrying the same pricing entry as the
real `config.toml`. Resulting row: `ollama|qwen2.5:1.5b-8k|49|12|0|0` —
real, non-mock token counts (`estimated = 0`, meaning Ollama actually
reported them), and `cost_micros = 0` as the **priced** zero, distinct
from section 3's **unpriced** `NULL`.

Done-when, against the task doc's own list (adjusted per Aryan's
decision to drop guest/owner — see above):

1. **A completed request writes exactly one `usage_records` row with the
   correct session, task, agent, provider, and model.** PASS — section 1.
2. **Session totals equal the sum of that session's rows.** PASS —
   section 2.
3. **For a provider that reports usage, the recorded numbers match its
   own dashboard within a few percent.** N/A as stated — Ollama has no
   dashboard. Confirmed instead that the recorded numbers are Ollama's
   own reported `prompt_eval_count`/`eval_count` verbatim
   (`estimated = 0`), not an estimate.
4. **Rows from a provider that omits usage are flagged `estimated = 1`.**
   Mechanism built and exempts `echo` correctly (see above); not
   observed against a real Ollama response in this repo, since this
   machine's Ollama reported real counts every time it was exercised.
5. **A guest ... refused before the request is sent, with both numbers
   in the message.** SUPERSEDED by Aryan's decision — there is no guest
   in this backend. Re-scoped and re-verified as: the single global cap
   refuses before the request is sent, with both numbers in the message
   — section 4.
6. **A task already running when the cap is hit completes normally; the
   next is refused.** PASS — section 4's send #2 (allowed, completes
   normally, pushes the total over) followed by send #3 (refused).
7. **An unknown model yields NULL cost and no crash.** PASS — section 3.

---

## What the next episode inherits

### B16 (SSH Serving) — adds identity on top of this cap, does not replace it

`UsageManager.check_cap()`/`today_totals()` query `usage_records` with no
`WHERE` on any user column, because there is no such column. B16 is
where a request first gets an identity; when it adds one (presumably a
`user_id` on `usage_records`, or a lookup from connection to owner/guest),
the per-user cap it needs is a second, narrower query filtered by that
column — this episode's global cap stays underneath it unchanged, as the
overall ceiling on the owner's wallet that a guest's activity must never
exceed regardless of per-user bookkeeping.

### The `max_tokens` gap is real and is not this episode's to close

`request.max_tokens` is `None` through most of this codebase's call
sites (`providers\models.py`; only `anthropic\provider.py` defaults it).
Setting it everywhere would let a future episode check a real upper
bound on a call's cost *before* it is sent, turning the cap's guarantee
from "blocks the next request" into something closer to "never crosses
the cap at all". That is out of scope here — the task doc did not ask
for it, and CLAUDE.md's "no new dependencies/scope without saying why"
rule applies to pulling it in uninvited. Flagged here so the gap is a
named, inherited fact rather than something the next person has to
re-derive from reading `providers\models.py` cold.

### `by-provider` exists but is not on the CLI

`UsageManager.by_provider()`/`UsageRepository.by_provider()` are built
and match the shape `by_agent()` uses. Nothing wires them to `cli.py`,
because the task doc's CLI line did not ask for it. A future episode
that wants it can add the subparser and handler directly — the
aggregation query is already there.

### `INDEX.md` and this episode's own task doc both had stale/incorrect
### premises, found before any code was written

`usage_records` was not "written per turn" — it had zero rows before
this episode, confirmed against the live database rather than assumed
from the doc. `INDEX.md`'s B14 row is corrected. And the guest/owner
model in `tasks\backend\B14-usage-limits.md` is B16's design, written
into this episode's spec two episodes early; this doc exists partly to
record that the spec and the build diverged here, deliberately, with
Aryan's sign-off, rather than silently.
