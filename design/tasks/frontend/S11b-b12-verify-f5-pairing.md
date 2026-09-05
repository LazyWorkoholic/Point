# Session 10b — B12 verification + F5 re-verification and pairing fix

**Goal:** close every gap F5 found that was actually B12's to fill, then use the
new data to make transcript reload correct in **all** cases — not just the ones
that happen to land in order.

> This is a **verification-and-repair** session, not a new feature episode. It
> runs immediately after B12 closes. It touches the frontend only; every backend
> change it depends on is B12's and must already be verified through B12's own
> gate before this session starts.

**Model:** Sonnet. The hard thinking was done in F5 and B12; this is applying
known data to a known defect, with a well-specified correct outcome.

**Depends on:** B12 complete and its gate passed — specifically `invocation` on
persisted assistant messages, terminal `outcome` + error `code`, and `task_id`
on **user** messages.

---

## Why this session exists

F5 shipped correct for a live session but not for a reloaded one, because the
data needed to reconstruct a transcript faithfully did not exist. B12 adds that
data. This session spends it.

Three things were deferred out of F5 with that reasoning, and all three are now
unblocked:

1. **Slots and failure footers were live-session-only** — `invocation` was never
   persisted, so a reloaded `@research` answer became an ordinary assistant
   message and a failed task lost its footer entirely.
2. **A question could not be paired to its answer** — `executor.py:418` built the
   `UserMessage` before `task_id` existed at `:421`, so only assistant messages
   carried a task link.
3. **Overlapping sends reload grouped, not paired** — see below.

---

## The pairing defect, stated precisely

F5's verification established the exact rule, and it is **not** "background tasks
misorder." It is:

> **Anything in flight at the same time splits on reload.** Two sends that
> overlap in time both persist their questions before either answer arrives, so
> the database holds `Q, Q, A, A` and reload renders it that way.

Measured cases, all confirmed:

| How it was sent | Reload result | Correct? |
|---|---|---|
| One at a time, each allowed to finish (plain **or** research) | `Q,A,Q,A` | ✅ |
| Plain send **queued** behind a plain send | `Q1,A1,Q2,A2` | ✅ |
| Two `@research` fired together | `R1,R2,A1,A2` | ❌ split |
| Plain + `@research` fired together | `Q1,R1,A1,A1` | ❌ split |

The queued case only looks correct because a queued turn genuinely **waits** —
its rows land in order by accident of timing, not by design. **No timing-based
heuristic can fix this**, which is exactly why F5's adjacency-based retry fold
was removed: it inferred structure from position and corrupted correct
transcripts (orphaned answers, invented "retried" labels).

Reload must pair by the **explicit `task_id` link B12 now persists**, never by
adjacency, ordering, or elapsed time.

---

## Build

### 1. Pair on reload by `task_id`

- Group each persisted user message with the assistant message carrying the same
  `task_id`, and render them adjacent as one exchange, ordered by the exchange's
  own start.
- **Every** send path must reload identically: sequential, queued, background
  task, and two-at-once. There is no "usually correct" here — the four rows in
  the table above must all read `Q,A,Q,A`.
- A user message with no matching answer (cancelled before any output, or a task
  that produced nothing) renders as a question with no reply — **not** dropped,
  and **not** silently attached to someone else's answer.

### 2. Reconstruct slots from `invocation`

- A reloaded assistant message whose `invocation.agent` names a non-default agent
  renders as the **collapsed slot** F5 already builds, not as an ordinary
  message. `o` expands it, `n`/`p` focus it, exactly as in a live session.
- A plain turn's `invocation.agent` (which B12 records as the session's current
  agent, never null) must **not** produce a slot — only a non-default agent does.

### 3. Re-attach terminal state from `outcome`

- A reloaded message whose `outcome` is not `done` renders with the same footer
  the live session shows, using F5's existing three states:
  - `bounds_exceeded` / `timed_out` → `⚠ stopped at its limit — …`
  - `failed` → `✗ failed — …`
  - `cancelled` → `⚠ interrupted [Retry ^R]`
- Partial text that was persisted stays visible. The footer describes **how it
  ended**; it never hides content.

### 4. Retry, only if the data supports it

- If B12 persisted a link identifying a retry (a parent/attempt reference), fold
  the retried attempts into one exchange as originally intended.
- **If B12 did not persist such a link, do nothing here.** Render both questions
  verbatim. Do not reintroduce an adjacency heuristic — that is the bug F5
  removed, and restoring a duplicate question is strictly better than inventing a
  label or orphaning an answer.

---

## Do NOT

- **Do not infer pairing from adjacency, ordering, or timing.** The only
  permitted key is the persisted `task_id`.
- **Do not modify the backend.** Every field this session needs is B12's and must
  already exist. If something is missing, stop and report it rather than adding
  it here.
- **Do not build accept/reject/edit** — F9 owns that (chain: F5 → F7 → F8 → F9).
- **Do not change live-session behaviour.** F5's live path is verified; this
  session only makes reload match it.
- **Do not fix B12's timeout policy here.** `timeout_s` killing healthy long
  generations is a backend policy decision, tracked separately.

---

## Done when

1. All four cases in the table above reload as `Q,A,Q,A` — sequential, queued,
   two research fired together, and plain + research fired together.
2. A reloaded `@research` answer is a collapsed slot: `n`/`p` focuses it, `o`
   expands it, identical to live.
3. A reloaded failed / timed-out / bounds-stopped answer carries its correct
   footer, with any partial text preserved.
4. A reloaded plain answer is **not** a slot, despite carrying an `invocation`.
5. A question whose task produced nothing reloads as a question with no reply —
   present, and not attached to another answer.
6. No "retried" label appears unless B12 persisted a real retry link.
7. Live-session behaviour is unchanged: F5's ordering, scroll, auto-follow,
   queue, cancel, and slot interactions all still pass.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$scratch = "$env:TEMP\point-10b-verify"
Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$scratch\roaming","$scratch\local" | Out-Null
$env:APPDATA = "$scratch\roaming"; $env:LOCALAPPDATA = "$scratch\local"
cd D:\Projects\Point\backend
uv run python cli.py migrate
cd D:\Projects\Point
go run .\frontend\cmd\point
```

Run **all four** send patterns, then `Ctrl+Q` and relaunch after each:

1. Two plain questions, one at a time, each allowed to finish.
2. A plain question, then a second sent **while the first streams** (queued).
3. Two `@research` questions fired **at the same time**.
4. A plain question and an `@research` question fired **at the same time**.

**Expect after every one:** `Q,A,Q,A` — each answer directly under its own
question. **Fails if:** any pattern reloads grouped (`Q,Q,A,A`), or an answer
appears under the wrong question.

Then:

```powershell
# slots and outcomes survive reload
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" `
  "select role, json_extract(content,'$.invocation.agent'), outcome, task_id
   from messages order by rowid desc limit 8;"
```
**Expect:** user rows carry a `task_id`; assistant rows carry both `task_id` and
a non-null `invocation.agent`; any non-clean ending carries its `outcome`.
**Fails if:** any is null — the frontend cannot pair or rebuild from a null, and
that is B12's gate failing, not this session's.

Eyes-on, after a restart:
- A completed `@research` reloads as a **collapsed slot**; `o` expands it.
- A failed / timed-out one reloads **with its footer**, partial text intact.
- A plain answer reloads as a **plain message**, no slot.
- Cancel a send before any output, restart → the question is present with **no**
  reply, not attached to anything else.

Finally, re-run F5's live gate unchanged (ordering, scroll, auto-follow, queue,
lock, cancel, slot focus/expand/retry, idle CPU) to confirm nothing regressed.

---

## Handoff

- **Reload and live now render identically.** Any future episode reading the
  transcript can assume one shape, not two.
- **Pairing is by `task_id`, never by position.** Any later feature that groups
  messages (F6's task panel, F9's result cards) must use the same key.
- **Still open, deliberately:** B12's `timeout_s` wall-clock cap killing healthy
  long generations (backend policy, tracked separately); accept/reject/edit (F9).
- If B12 did not persist a retry link, record here that retried attempts still
  render as separate questions, and which episode would add it.
