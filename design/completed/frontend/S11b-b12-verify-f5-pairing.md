# Session 10b (S11b) — B12 Verification + F5 Reload Pairing ✅

**A reloaded transcript now renders exactly like the live one it replaces.
Every answer sits under its own question, keyed on the `task_id` B12
persists and on nothing else; a research answer comes back as a collapsed
slot; a failed, cancelled or bounds-stopped reply comes back with its
footer and its partial text; and a task that produced nothing at all still
leaves its question present, unattached, and honestly marked. Frontend
only — not one line of backend changed.**

> Verified by Aryan on 2026-09-08 against
> `tasks/frontend/S11b-b12-verify-f5-pairing.md`'s seven "Done when"
> criteria, on a scratch profile under `%TEMP%` with `APPDATA` and
> `LOCALAPPDATA` redirected in **one** PowerShell window — one window
> because `protocol\spawn.go`'s `buildCommand` never sets `cmd.Env`, so the
> Go frontend hands its own environment to the Python backend it spawns.
> All four send patterns, a retry chain, and F5's full live gate.
>
> **Two things in the session doc were wrong and were raised before any code
> was written**, per the standing rule that the code and the database are
> the truth. Both changed what got built.
>
> 1. **The slot rule.** Build §2 said a reloaded answer becomes a slot when
>    `invocation.agent` names a **non-default** agent. The repo decides the
>    lane on `agent.max_steps > 1` alone — `runtime.py`'s `is_bounded_loop`,
>    ridden out on `TaskStarted.background`, which F5's own contract makes
>    the *sole* authority. The two rules disagree in both directions, and
>    `filler-explainer` (a non-default agent at `max_steps=1`) is the case
>    that breaks visibly: live runs it inline, and the spec's rule would
>    have reloaded it as a slot it never was — failing Done-when #7, the
>    point of the session. Aryan confirmed the spec was wrong, chose the
>    background rule, and had `tasks/frontend/S11b-b12-verify-f5-pairing.md`
>    corrected at Build §2 and Done-when #4.
> 2. **The gate's SQL.** It read
>    `json_extract(content,'$.invocation.agent')`. `invocation` lives in the
>    **`metadata`** column (`database\session_repository.py:88`), so the
>    query returned NULL for every row — and the gate's own stated failure
>    condition is "fails if any is null". It would have failed a correct
>    build. **This is verbatim the third assumption B12's own session caught
>    and corrected in its gate**; this doc predates that fix. Corrected in
>    place.
>
> The gate passed on the first run. Aryan additionally exercised a retry
> chain the gate does not list — cancel, retry, cancel, retry, research,
> retry — which reloads correctly.

---

## Files

### `frontend\internal\chat\reload.go` — new (≈330 lines)

The whole of spending B12's data. Three transforms, in the order they run:

| Function | What |
|---|---|
| `pairReloaded` | Reorders persisted rows so an answer sits under its own question. Keyed on `task_id`. |
| `decorateReloaded` / `reloadedEntry` | Builds the live-shaped `liveEntry` for each row that must render as a slot or carry a footer; `nil` for every row that stays an ordinary message. |
| `reloadLane` | Slot-or-inline, from the agent id. See the stopgap below. |
| `reloadStatus` | B12's `Outcome` → F5's `liveStatus`. |
| `messageErrText` / `reloadErrText` | The footer's text, preferring the error the backend actually reported. |
| `requestTaskRows` / `onTaskList` / `applyTaskRows` / `orphanReply` | The two-table join for a task that persisted no message at all. |

### Edited

| File | Change |
|---|---|
| `chat\entries.go` | `histAt`; `entryLines`, `entryRole` and `liveAt` consult it. |
| `chat\chat.go` | The `hist` field, and the `all` field's doc updated — the fold stays gone, the reorder is new. |
| `chat\model.go` | `outcome`, `interrupted`, `error_code` on `wireMessage`; the `Outcome` vocabulary. |
| `chat\protocol.go` | `queryListTasks`; `setSession` pairs, decorates and requests task rows; the error path exempts `ListTasks`. |
| `chat\input_render.go` | `knownAgentIDs map[string]bool` → `knownAgents map[string]lane`, plus `isKnownAgent` / `agentLane`. |
| `chat\stream.go` | `splitAgent` reads the new map; `retryTarget`'s fallback scans `w.hist` **after** `w.live`. |

### Tests

`chat\reload_pairing_test.go` (13 tests, one per "Done when" criterion),
`chat\reload_wire_test.go` (1), and `chat\reload_test.go`'s F5 tests updated
in their comments only — they still pass unchanged, which is the point:
they lock in that rows with **no** `task_id` are still rendered verbatim.

---

## The pairing contract — F6 and F9 must use this exact key

> **Reload groups a user message with the assistant message carrying the
> same `task_id`, and renders them adjacent as one exchange. Adjacency,
> arrival order and elapsed time are never used, for anything.**

The rules, each load-bearing:

- A user row with a `task_id` claims the **first later** assistant row
  carrying the same `task_id`. Later, because `agents\executor.py` generates
  the task id, appends the `UserMessage`, and only then runs — so an answer
  is always downstream of its question.
- **At most one** assistant row exists per task. `runtime.py`'s terminal
  handler appends exactly one, or none. The pairing is 1:1, never 1:many.
- Everything else keeps its database position: rows with no `task_id` (every
  row written before B12's migration v5), assistant rows nobody claimed,
  tool and system rows. Nothing is dropped and nothing is invented.
- A question whose answer does not exist **stays a question with no reply**.
  It is not quietly given the next unclaimed answer.

It is a stable reorder: one send at a time, or a pre-B12 transcript, comes
out byte-identical to what went in.

### Why no heuristic is permitted here, ever

F5's verification established the defect precisely, and it is **not**
"background tasks misorder":

> Anything in flight at the same time splits on reload. Two sends that
> overlap both persist their questions before either answer arrives, so the
> database holds `Q,Q,A,A`.

Sequential and queued sends reloaded correctly *by accident of timing* — a
queued turn genuinely waits, so its rows happen to land in order. That is
why F5's adjacency-based retry fold was **deleted rather than refined**: it
inferred structure from position, and on two successful `@research` sends of
the same question it merged the questions, orphaned an answer under the
survivor, and labelled a send that was never retried as "retried". It
corrupted a correct transcript.

**F6's task panel and F9's result cards both group messages. Both must use
`task_id`.** Anything keyed on position will be wrong the moment two things
run at once, which is the entire premise of this runtime.

---

## The lane rule, and the stopgap behind it

A reloaded reply is a **collapsed slot iff its agent is a background
agent**. Live, that is decided by one thing only: `TaskStarted.background`,
which the backend computes as `agent.max_steps > 1` (`runtime.py`'s
`is_bounded_loop`). The frontend reserves optimistically from the `@token`
at send time and then **reconciles** — `background: true` stays `laneTask`,
`background: false` collapses to `laneTurn` in place. A reload has no
`TaskStarted` to reconcile against, so it must answer the same question from
the agent id alone.

`max_steps` is not on the wire, and **there is no `ListAgents`** — only
`ListProviders` / `ListProviderModels` exist. So the one agent that
satisfies the rule is written down:

```go
var knownAgents = map[string]lane{
	"general":          laneTurn,  // max_steps = 1
	"filler-explainer": laneTurn,  // max_steps = 1
	"research":         laneTask,  // max_steps = 8
}
```

**This hardcoding is deliberate, not an oversight.** The correct long-term
rule is *slot iff the agent is a background agent (`max_steps > 1`)*, read
from a real registry. This map stands in for a backend capability that does
not exist yet. See "What the next episode inherits".

### What the rule is NOT

"The agent is not the session default" is wrong in both directions, and the
session doc said it before this session corrected it:

| Send | Live renders | The "non-default" rule would render |
|---|---|---|
| `@filler-explainer …` | inline turn (`background=false`) | **collapsed slot** ✗ |
| plain send, session `settings.agent_id="research"` | **slot** (`background=true`) | plain message ✗ |

The first is observable today with a built-in agent, and Aryan verified it
after the fix: `@filler-explainer` reloads as an ordinary inline reply with
no `o`/expand, and its row reads `assistant | filler-explainer | done`. The
second is latent — nothing in the frontend writes `settings.agent_id` yet —
but `agents\registry.py`'s `resolve_for_session` reads it, and B12 records
that resolved agent on every plain turn.

---

## Reusing F5's renderers rather than writing reload's own

A decorated row renders through `renderTaskEntry` / `renderTurnEntry` /
`liveFooter` — F5's own functions, unchanged. `liveAt` answers for it, which
is what makes `o`, `t`, `n`/`p` and `^R` work on a reloaded entry with **no
reload-aware copy of any of them**.

"A reloaded `@research` answer is a collapsed slot, identical to live" is
then true *by construction*. Written as a second renderer it would have been
a claim needing its own tests, and one that drifts the first time either
side is touched. This is the same call B9 and B10 made in deleting
`managers\provider.py` and never filling `managers\tool.py`: a second
abstraction that agrees with the first only by discipline does not stay
agreeing.

Three consequences worth knowing:

- **An ordinary clean reply is left `nil`** and keeps F3's shared render
  cache. Only rows that need a slot or a footer pay for a per-entry memo.
  `BenchmarkScrollReloaded` is 3957 ns/op against `BenchmarkScrollLive`'s
  5450 — the reload path is still the cheaper of the two, unchanged.
- **`hist` is parallel to `all`, not a fourth source.** `entries.go`'s rule
  — one ordered sequence, display position is creation order — is untouched;
  pairing is a reorder of `all` itself.
- **Every `hist` entry is terminal, always.** A non-terminal one would keep
  `Streaming()` true and re-arm the watchdog tick forever, which is F5's
  recorded idle-CPU leak. `orphanReply` returns `nil` for a task that is
  `pending`, `queued` or `running` for exactly this reason, and a test
  asserts `Streaming()` stays false.

---

## The tasks join — B12's accepted limitation, spent

**A task cancelled or failed before its first token persists no assistant
row at all**, because `StreamAccumulator.finalize_interrupted` returns
`None` for empty text (an empty `AssistantMessage` fails B3's validation).
B12 accepted that deliberately and recorded where the data went instead: the
user message carries `task_id` (B12's own change), and the `tasks` row
carries `status`, `error_code` and `agent_id`.

`ListTasks` is fired **after** the transcript is on screen, not before — the
transcript is already correct without it, and blocking a whole session load
on a decoration would be the wrong trade. A refused or unreadable reply logs
a warning and returns; it deliberately never sets `loadErr`, because losing
one footer must not replace a correct transcript with an error banner.

---

## Manual Test Log (S11b)

Run against the app at the default terminal size, on a `%TEMP%` scratch
profile with a fresh `config.toml` naming Ollama.

| Done-when | Result |
|---|---|
| #1 all four send patterns reload `Q,A,Q,A` | ✅ sequential, queued, two `@research` fired together, and plain + `@research` fired together — **including the two F5 measured as split** |
| #2 a reloaded `@research` answer is a collapsed slot | ✅ `n`/`p` focuses it, `o` expands and re-collapses it, identical to live |
| #3 failed / timed-out / bounds-stopped carry their footer | ✅ partial text preserved behind `o` |
| #4 a non-background agent's answer is not a slot | ✅ `@filler-explainer` renders as an ordinary inline reply, no `o`/expand; row reads `assistant \| filler-explainer \| done` |
| #5 a question whose task produced nothing | ✅ present, unattached, carrying `⚠ interrupted [Retry ^R]` |
| #6 no invented "retried" label | ✅ a retry chain (cancel · retry · cancel · retry · research · retry) reloads correctly, both questions verbatim |
| #7 live behaviour unchanged | ✅ F5's live gate re-run clean — ordering, scroll, auto-follow, queue, lock, cancel, slot focus/expand/retry, idle CPU |
| rows | ✅ user rows carry `task_id`; assistant rows carry `task_id` and a non-null `invocation.agent`; non-clean endings carry their `outcome` |

Automated: 73 Go tests pass, 14 of them new here.

---

## Bugs caught during this session

### 1. The slot rule and the gate SQL — caught before code

Both raised and corrected before a line was written; see the header block.
Recorded again here because the pattern is the point: the gate SQL error is
**the same one B12's session caught in its own gate**, in a doc written
before that fix landed. A stale doc reproduces its own mistakes downstream.

### 2. `questionFor` was O(n²)

The first cut scanned backwards from every assistant row to index 0 looking
for its question. `pairReloaded` has already run by then and places the
answer immediately under its own question, so the only place its question
can be is `i-1`. Narrowed to that single check — still exact, because the
`task_id` comparison is what decides, so an unclaimed answer that merely
happens to follow some other question correctly finds nothing.

### 3. A reloaded failure said "the task failed" when the backend knew better

The first cut used one generic fallback for every failed row. But
`finalize_interrupted` writes the real error to `metadata["error"]` — the
same string `TaskFailed`'s payload carried to the live session. A reloaded
footer now names the provider's actual error exactly as the live one did.
The generic fallback is confined to the case that genuinely has no message
to read from: the zero-token orphan, whose `tasks` row keeps a code and no
prose. Where it does fire, it uses **live's own wording**, not a second one
invented here — with the same information, the two paths must produce the
same line.

### 4. A failed slot has no clock — found by the wire test

`renderTaskEntry` puts the elapsed time on `✓ done` alone; a `✗ failed` slot
shows none. The test asserting the reconstructed clock was written against
a failed row and correctly failed. The clock itself is real and asserted on
a done slot: it spans the question's timestamp to the answer's, which is the
send-to-finish interval the live clock was counting.

### 5. Two test-authoring defects worth not repeating

- A helper located a question by substring and matched **the answer**,
  because an answer's own text contains the question's words. Restricted to
  `entryRole(i) == roleUser`. A pairing test that matches the wrong row
  asserts nothing.
- Footer assertions were made at width 70, where
  `⚠ stopped at its limit — … [Retry ^R]` wraps. The test was asserting
  against the wrap, not the footer. Rendered at 100 instead.

### 6. The mode chip does not recognise `@filler-explainer` — investigated, NOT this session's

Found by Aryan while verifying case #4. Typing `@research` flips the mode
chip; typing `@filler-explainer` shows no agent chip at all, even though the
send routes correctly and the backend records the agent.

**It is not the lane-map gap, and it is not a fourth reader of it.**
`agentTokenState` resolves all three built-ins — probed directly:

```
"@research x"          id="research"          exact=true  mode=modeResearch
"@filler-explainer x"  id="filler-explainer"  exact=true  mode=modeChat
"@general x"           id="general"           exact=true  mode=modeChat
```

The chip stops one layer later, at `currentMode()`
(`chat\input_render.go:148`), which hardcodes `exact && id == "research"`
because `editorMode` has exactly three states and F4 defined exactly two
chips — `▸ chat` and `▸ @research`. That function's own doc comment says so
and names `general` and `filler-explainer` as the cases that fall through
deliberately. It predates this session and was not touched by it.

It **is** a fourth symptom of the same underlying absence — no `ListAgents`,
so nothing can enumerate the agents or ask what one is — which is why it is
recorded alongside the stopgap below. **Deliberately not fixed here**: the
fix is a fourth `editorMode`, a prefix in `modeChipPrefix`, and a border
role in `queryBorderRole`, all inside F4's query input, and it needs F4's
own gate re-run rather than this session's. Open decision for Aryan.

---

## Why `reload_wire_test.go` uses a generated payload

Every other test in the package builds a `wireMessage` in Go. A field name
that Go and Python disagree about passes all of them and fails against the
real backend — **which is exactly how F5 shipped a reload path that could
not see `invocation` at all.**

So `reload_wire_test.go` asserts against the literal output of

```powershell
uv run python -c "... UserMessage(...).to_dict(), AssistantMessage(...).to_dict()"
```

run against `backend\managers\message.py`, with only the ids shortened. It
pins the shapes that are easy to get wrong: `invocation` is `null` on a user
row and an object on an assistant row, `capability` inside it is `null`,
`outcome`/`error_code` are `null` on the user row, and the error prose lives
in `metadata.error` — not in `content`. Any episode changing `Message`'s
serialisation should expect this test to be the one that notices.

---

## What the next episode inherits

**Reload and live now render identically.** Any future episode reading the
transcript can assume one shape, not two.

### Flagged: the agent-list stopgap — F8 must replace ALL of it together

There is no `ListAgents` command. Four places therefore hardcode a fact
about the backend's agents, and **F8's `@` picker is the episode that adds
the real fetch. It must replace them together, not one at a time** — a
partial replacement leaves two lists that disagree, which is worse than one
list that is merely stale.

| Reader | File | What it hardcodes |
|---|---|---|
| `agentTokenState` | `chat\input_render.go` | which tokens are agent names at all |
| `splitAgent` | `chat\stream.go` | the same, at send time |
| `reloadLane` | `chat\reload.go` | **which agents are background** (the lane) |
| `currentMode` | `chat\input_render.go` | which agents get a mode chip — `research` only, see Bug 6 |

The first three read one map, `knownAgents`. The fourth is F4's separate
`id == "research"` test and is an open decision, not a defect this session
introduced.

**The correct long-term rule for the lane is: slot iff the agent is a
background agent (`max_steps > 1`).** That is the backend's own rule
(`runtime.py`'s `is_bounded_loop`), ridden out on `TaskStarted.background`,
and F5's contract makes it the sole authority. The hardcoding is deliberate:
`max_steps` is not on the wire and cannot be fetched. F8's list also needs
agents **and** files together, since an `@token` can name either.

### Flagged: the tasks join is a lookup, and one degradation is accepted

- **`ListTasks` spans all sessions** (`runtime.py`'s `_handle_list_tasks`
  says so, deliberately — F6's task panel is global) and is asked for **500
  rows**, not its default 50. A busy history at 50 would push this session's
  own older tasks out, and the rows that fell out are exactly the ones whose
  footers would go missing.
- **A zero-token failure reconstructs as `✗ failed — the task failed`, with
  no detail**, because the `tasks` row carries `error_code` and no error
  prose. This is an **accepted degradation**, and it is narrow: it applies
  only to replies that produced *no text at all*. A partial-text failure
  persists a real message row and now names the **real** error via
  `metadata["error"]`, identical to live.

### Flagged: there is no retry link, and a retried question still reloads twice

B12 persists **no** parent/attempt reference. `task_id` is the
*prerequisite* for one — B12's own handoff says so — not the link itself: it
makes "same text, same session" distinguishable in principle, but nothing
writes down that task B was app-issued in place of task A.

So Build §4 was correctly a no-op, and **a retried question renders as two
questions.** That is the honest rendering of what the database holds, and it
is strictly better than the alternative F5 already tried and deleted.

**To change it**, a later episode must persist an explicit reference — a
`retry_of` / `parent_task_id` column on `tasks`, or the same field on the
message — written at the point the retry is issued. `chat\stream.go`'s
`retryFocused` already knows it is retrying and which entry it is retrying;
nothing carries that across the wire. Once it exists, `reload.go` folds the
attempts into one exchange, and it will be the only fold in the file that is
keyed on something real.

### Everything else

- **Pairing is by `task_id`, never by position.** Any later feature that
  groups messages — F6's task panel, F9's result cards — must use the same
  key. See "The pairing contract" above.
- **Still open, deliberately, and not this session's:** B12's `timeout_s`
  wall-clock cap killing healthy long generations (backend policy, tracked
  separately); accept/reject/edit (F9, chain F5 → F7 → F8 → F9).
- **The `outcome` vocabulary is transcribed in `chat\model.go`.** An empty
  `outcome` means the row predates migration v5, which is deliberately *not*
  the same claim as `done` — it renders no footer rather than asserting an
  ending nobody recorded.
- **`^R` prefers a live entry over a reloaded one** and always will:
  `retryTarget` scans `w.live` first, then `w.hist`. "The most recent
  retryable" has to keep meaning the most recent.
