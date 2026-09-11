# Episode B19 — Silence-Based Agent Timeout

**Replace `executor.py`'s wall-clock `timeout_s` deadline — set once at task
start, checked once per chunk, with zero relationship to whether output is
flowing — with a silence-based deadline that resets on every sign of life. A
task dies only on genuine dead air, never on continuous output, while still
defending against the runaway local-model spiral B11 built this check to
catch.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B19-silence-based-timeout.md`) claims reality and
> completion. Code wins on any disagreement.

**Model:** Sonnet. One mechanism change in one function, with a well-specified
before/after and an existing test fixture (`--max-seconds`) to verify against.

**Depends on:** B11 (the deadline check itself), F5/F7 (which found and traced
this bug — read both completed docs before starting, they contain the exact
line numbers and the full diagnostic trace).

**Consumed by:** Nothing new. This is a correctness fix to a mechanism already
in use by every agent send.

---

## Why this episode exists

`agents/builtins.py` sets `general.timeout_s = 60` and `research.timeout_s =
180`. `agents/executor.py:680` sets `deadline = time.monotonic() +
limits.timeout_s` once, at task start. `_check_deadline` (`executor.py:931`) is
checked once per streamed chunk (`executor.py:749`) as a pure wall-clock
comparison — it does not look at whether a chunk actually arrived, only at
whether the clock has passed the deadline.

The consequence, traced and confirmed during F7's verification: a healthy
generation that streams continuously for longer than 60s (general) or 180s
(research) is killed with `code: "bounds_exceeded"`, indistinguishable from a
task that produced nothing at all. A cold Ollama model load that takes over 60s
to produce its first token is killed before generation even starts. This has
been present since B11 added the per-chunk check — deliberately, to stop a
degenerate local-model spiral, tested at the time with a synthetic
`--max-seconds 8` — and has been sitting unaddressed through F5, S11b, B12, and
B18, each of which correctly diagnosed it as out of scope rather than fixing it.

The frontend already solved the equivalent problem correctly: `chat/stream.go`'s
`touch()` resets a silence budget on every `TextDelta`/`ThinkingDelta`/
`ProgressUpdated`/tool event, with a first-token grace factor for a cold model
load. This episode gives the backend the same mechanism, so a task is only
killed for genuine inactivity, not for elapsed time.

---

## Build

### 1. Silence-based deadline

- Replace the once-computed `deadline = time.monotonic() + limits.timeout_s`
  with a `last_activity` timestamp, initialized at task start.
- Every point that currently represents "the task is alive" must reset
  `last_activity`: each streamed chunk (the existing per-chunk check site,
  `executor.py:749`), and — check the equivalent of the frontend's
  `ThinkingDelta`/`ProgressUpdated`/tool events on the backend side — anything
  the backend itself treats as a sign of life for a bounded loop, not only raw
  provider text.
- `_check_deadline` compares `time.monotonic() - last_activity` against
  `limits.timeout_s`, not against a fixed deadline. A task is only ended when
  **no activity of any kind** has occurred for `timeout_s`.

### 2. First-token grace

- Before the first chunk arrives, allow a longer silence budget than
  `timeout_s` alone — mirror the frontend's `firstTokenGraceFactor` concept
  (e.g. 2× `timeout_s`) so a cold model load is not killed before it starts.
  Once the first chunk arrives, subsequent silence is judged against the
  ordinary `timeout_s` budget.

### 3. Preserve the runaway-spiral defense

- **This is the property B11 built the check to protect, and it must not be
  lost.** A degenerate local model that emits tokens in a tight, meaningless
  loop with no real silence between them — the case B11's synthetic
  `--max-seconds 8` test targeted — must still be caught. A pure silence reset
  does **not**, by itself, bound total generation length or total tool-call
  count; verify whether B10/B11's separate `max_steps`/`max_tool_calls` bounds
  already cover this case independently of `timeout_s`, or whether this
  episode needs an additional total-duration ceiling (much larger than
  `timeout_s`, e.g. a multiple of it) as a backstop against a task that never
  goes silent but also never finishes.
- State explicitly in the completed doc which mechanism (existing step/
  tool-call bounds, or a new backstop ceiling added here) is what still catches
  a runaway spiral, and why.

### 4. Outcome and message wording stay as-is

- The existing three-way split (`bounds_exceeded` / `timed_out` / genuine
  `failed`) and their distinct frontend renderings are correct and unaffected
  by this change — this episode changes **when** the check fires, not what
  happens once it does.

---

## Do NOT

- **Do not touch the frontend.** `chat/stream.go`'s watchdog is already correct
  and stays as-is; this episode brings the backend in line with it, not the
  other way around.
- **Do not remove the per-chunk check entirely** and fall back to checking only
  between agent steps. That reopens exactly the risk B11's per-chunk check was
  added to close — a single step running unbounded.
- **Do not change `timeout_s`'s configured values** (60/180) as a substitute
  fix. Raising the constants only delays the same failure for a slower model;
  it does not remove the underlying wall-clock-vs-activity mismatch.
- **Do not touch B10/B11's `max_steps`/`max_tool_calls` bounds** unless Build
  step 3 determines a backstop ceiling is genuinely needed here — if the
  existing bounds already cover the runaway case, leave them untouched.

---

## Done when

1. A generation that streams continuously for well over 60s (general) or 180s
   (research) completes normally — never interrupted by `timeout_s` while
   output is flowing.
2. A task that receives its first chunk more than 60s/180s after start, but
   within the first-token grace window, is not killed before that first chunk
   arrives.
3. A task that genuinely goes silent for `timeout_s` (no chunk, no
   thinking/progress/tool event) is still ended with `timed_out`, with the
   correct partial content preserved, exactly as before.
4. B11's original runaway-spiral scenario (`--max-seconds 8` against a
   degenerate model) still terminates the task — verify this explicitly, don't
   assume the silence reset alone is sufficient.
5. The three outcome states and their frontend renderings are unchanged.

---

## Verification gate (PowerShell, scratch profile)

```powershell
$scratch = "$env:TEMP\point-b19-verify"
Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$scratch\roaming","$scratch\local" | Out-Null
$env:APPDATA = "$scratch\roaming"; $env:LOCALAPPDATA = "$scratch\local"
cd D:\Projects\Point\backend
uv run python cli.py migrate

# 1. a healthy long generation must not be killed at the old ceiling
uv run python cli.py send "write a very long, detailed essay" --wait
# expect: completes normally, well past 60s if the model is slow, no
# bounds_exceeded/timed_out outcome while tokens were flowing

# 2. genuine silence still terminates correctly (reuse B11's own case)
uv run python cli.py send "@research anything" --max-seconds 1 --wait
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" `
  "SELECT outcome, error_code, length(content) FROM messages ORDER BY rowid DESC LIMIT 1;"
# expect: timed_out, partial content preserved (unchanged from before)

# 3. the runaway-spiral defense B11 built this for must still hold
uv run python cli.py send "@research anything" --max-seconds 8 --wait
# expect: still terminates -- confirm this is the same behavior B11's
# original synthetic test verified, not a regression opened by the
# silence-based change
```

Eyes-on: trigger a real research task on a local model and let it run past its
old 180s ceiling while visibly producing output — confirm it completes rather
than failing at 3:00. (F7's verification already saw this succeed once at
3:24 by accident of a partial fix; this gate should make it reliable.)

---

## Handoff

- **F5/F7/S11b/B12/B18's completed docs each carry a note that this was known
  and deferred** — none needs further editing once this closes; they already
  point at "backend policy decision, tracked separately," and this episode is
  that decision being made.
- The frontend's own silence watchdog (`chat/stream.go`) is unaffected and
  remains the second, independent layer — a task that somehow evades the
  backend's silence check (e.g. a hung connection between backend and
  provider) is still caught by the frontend's 120s budget.
