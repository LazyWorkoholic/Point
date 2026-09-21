# Episode B21 — Orphaned Messages Must Not Enter the Next Prompt

**A task that fails before producing a single token leaves its user message
persisted with no answer after it. `context.py`'s history window then hands
that unanswered question to the NEXT unrelated send, and the model answers
it instead of — or as well as — what was actually asked. Exclude a user
message whose task failed without output, by joining on the `task_id` the
rows already carry.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B21-orphaned-message-context.md`) claims reality
> and completion. Code wins on any disagreement.

**Model:** Sonnet. One filter in one function, against data that already
exists, with an exact reproduction to verify against.

**Depends on:** B8 (the context builder being changed), B12 (which put
`task_id` on user messages and gave the `tasks` table its terminal status),
S11b (which established the pairing key and the two-table join this reuses).
**Read S11b's completed doc before starting** — it already implements this
exact join for the transcript, and this episode must not invent a second one.

**Consumed by:** Every send. This is a correctness fix to context assembly,
which every turn and every agent invocation goes through.

---

## Why this episode exists

Found during F6's verification gate, reproduced deterministically with no
concurrency involved. Full diagnostic trace in
`design/completed/frontend/F06-right-pane.md`'s "Bug C".

The chain, three steps, each correct in isolation:

1. **`agents/executor.py:464-465`** persists the user's message
   unconditionally, before the task runs:
   ```python
   user_message = UserMessage(session.id, text, task_id=task_id)
   await self._sessions.append_message(user_message)
   ```
2. **A task that fails before its first token persists no assistant row at
   all.** `StreamAccumulator.finalize_interrupted` returns `None` for empty
   text, because an empty `AssistantMessage` fails B3's validation. B12
   accepted this deliberately and recorded it; the `tasks` row carries the
   outcome instead.
3. **`managers/context.py:886`** builds the next prompt from
   `recent_messages(session_id, policy.stm_window)`, and
   `MessageHistory.for_provider` filters only reasoning blocks by provider.
   **Nothing filters a user message that was never answered.**

So the failed task's question is carried forward as ordinary conversation.
Confirmed end to end:

```
$ send "@research importance of sleep in human life" --max-seconds 1 --wait
  -> fails immediately, zero output (bounds_exceeded, 2s silence)
  -> nothing in flight; waited
$ send "Hello write a short paragraph about oceans"
  -> a paragraph entirely about SLEEP. No mention of oceans.
```

Severity: the backend can silently answer a question the user did not ask,
and persist that answer as the reply to one they did. It is not cosmetic and
it is not rare — any failed agent send poisons the next turn in that session.

---

## Build

### 1. Exclude user messages orphaned by a failed task

In `context.py`'s history-window assembly (step 5, around line 886), drop a
user message when **all** of the following hold:

- it carries a `task_id`,
- that task's row in `tasks` has a terminal, unsuccessful status
  (`failed` / `cancelled` — `LIVE_STATUSES` is already the complement), and
- no assistant message in the session carries the same `task_id`.

The third condition is what keeps this narrow. A task that failed *after*
producing output has a real assistant row; that exchange happened and
belongs in history exactly as it is. Only the empty-output case leaves an
orphan.

### 2. Reuse S11b's join, do not invent a second one

`reload.go` already pairs messages to tasks on `task_id` and reads terminal
status off the `tasks` row to rebuild transcript footers. The same two-table
join answers this question. Route it through `SessionMemoryService` /
`TaskRepository` rather than adding SQL to `context.py`, which
`context.py`'s own header says must never touch the database directly.

### 3. Log what was excluded

An excluded message must appear in `ContextLog.evicted` with its own reason
(`"orphaned"` or similar), the same way a provider-filtered reasoning block
already does. `context report` is the tool for "why is this not in my
prompt", and a silent exclusion is the same class of defect as the silent
inclusion this episode is fixing.

---

## Do NOT

- **Do not guess whether a message was a follow-up.** No "is this related to
  the previous turn" heuristic, no similarity check, no time window. This
  project already deleted one adjacency heuristic for corrupting correct
  transcripts (F5's retry-fold); the pairing key is `task_id` and nothing
  else.
- **Do not skip a trailing unanswered user message generically.** A
  genuinely in-flight turn has no assistant row yet either, and dropping it
  would break the normal case to fix the broken one. The task's terminal
  status is what distinguishes them.
- **Do not persist placeholder or error text** to give the orphan an answer.
  B12 considered and rejected fabricating an assistant row; this episode
  does not reopen that.
- **Do not change when the user message is persisted.** Writing it before
  the task runs is what makes a send recoverable and what S11b's pairing
  depends on.
- **Do not touch the retry path.** `ctrl+r` is the explicit way to re-ask a
  failed question and already works; this episode must leave it exactly as
  it is.

---

## Done when

1. The reproduction above produces a paragraph **about oceans**, with no
   sleep content.
2. A normal multi-turn conversation is unaffected — ask a question, get an
   answer, ask a follow-up that depends on it, and the follow-up still has
   the earlier exchange in context.
3. A task that failed **after** producing partial output keeps its exchange
   in history: both the question and the partial answer are still there.
4. A genuinely in-flight turn's user message is still included — a second
   send while the first is streaming sees the first question in its history,
   as it does today.
5. `context report` names every excluded message and why.
6. Existing context tests still pass (`backend/tests/manual_context_test.py`).

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md: one combined script against a renamed-aside profile, never
the live one — `$env:POINT_DB` set once inside the same script, because
separate tool calls do not share shell state.

```powershell
cd D:\Projects\Point\backend

$env:POINT_DB = "$env:TEMP\point-b21\runtime.db"
New-Item -ItemType Directory -Force (Split-Path $env:POINT_DB) | Out-Null
uv run python cli.py migrate

# 1. the exact reproduction
uv run python cli.py session new --title "b21"
uv run python cli.py send "@research importance of sleep in human life" --max-seconds 1 --wait
uv run python cli.py send "Hello write a short paragraph about oceans" --wait
# EXPECT: a paragraph about OCEANS. Fails if it mentions sleep.

# 2. the orphan is named as excluded, not silently dropped
uv run python cli.py context report --last

# 3. normal multi-turn history still works
uv run python cli.py send "What is the capital of France?" --wait
uv run python cli.py send "What is its population?" --wait
# EXPECT: answers about PARIS -- the follow-up still sees its own history
```

---

## Handoff

- **F6's completed doc carries the full diagnostic trace** for this, including
  the four hypotheses that were eliminated before the real one was found
  (backend prompt concatenation, frontend entry mis-routing, two tasks sharing
  an accumulator, and a display/repaint fault). Read its "Bug C" section
  before starting — the eliminations are as useful as the conclusion, because
  three of them looked more likely than the cause did.
- **This is why the orphan exists at all**, and it is worth stating plainly
  for whoever reads B12 next: B12's decision that an empty failure persists no
  assistant row is still correct for the transcript. What was missing was that
  the same decision has a second consumer — the next prompt — and nobody had
  asked what an unanswered question does there.
- Nothing in F6 needs re-editing when this closes; its handoff already records
  this as deferred to B21 by name.
