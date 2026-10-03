# Episode B26 — Behaviour Overrides + `doctor` on the Wire ✅

**Two gaps closed, both transport-only, neither inventing a computation.
(1) The Behaviour tab's five `config.toml`-sourced values
(`chat.queue_depth`, `limits.max_concurrent_tasks` plus the per-provider
`limits.<provider>.max_concurrent_tasks` family, `project.allow_outside_project`,
`index.respect_gitignore`, `research.stale_after_days`) now have an
in-memory override layer — `appconfig_overrides.py` — that every one of the
seven call sites Behaviour owns checks first, wired to the wire through
`SetBehaviourOverride`/`BehaviourOverrideSet`. (2) `point doctor`'s report,
previously only a sequence of `print()` calls inside `cmd_doctor`, is now
`build_doctor_report()` — a function that returns the same facts as a dict
— reused verbatim by both `cmd_doctor` (formats it to stdout, byte-identical
to before) and the new `GetDoctorReport`/`DoctorReport` wire event. The one
real landmine, `_ollama_report()`'s `asyncio.run()` crashing inside the wire
handler's already-running event loop, was fixed by extracting the probe
coroutine, exactly as the task doc's "Correction before you start" section
specified — not by rewriting the probe.**

> Confirmed by Aryan: all nine Done-when items, across two separate scripts
> (see "Manual Test Log" below) — this episode's own verification gate
> needed a second script because the originally-specified one assumed
> something untrue of this repo's CLI (see "Bugs caught," below, for why).
> Two things needed correcting in Aryan's own final pass: Done-when #1's
> long-lived-process script had to be invoked against the correct
> subdirectory paths before it would run clean, and Done-when #4 was
> reconfirmed with an actual byte-level `Compare-Object` between the real
> pre-B26 `cmd_doctor` output and the current one (empty diff) — not a
> source-level read of the refactor, not an assumption that it preserved
> behavior. Run against a scratch `APPDATA`/`LOCALAPPDATA` profile per
> CLAUDE.md's hard rule — the real profile was never a target of anything in
> this episode.

---

## `backend\appconfig_overrides.py` (NEW)

The in-memory override layer, sibling to `appconfig.py` rather than folded
into it — `appconfig.py`'s own docstring says it "never asks anyone
anything" and is about files on disk, while this is a different kind of
thing: runtime state with no file behind it, by design.

- `ALLOWED_KEYS` — the five fixed keys named in the task doc. Not a general
  config-override mechanism: `set_override()` raises `ValueError` for
  anything not in this set and not matching the provider-cap family below.
- `_provider_cap_name(key)` — recognizes `limits.<provider>.max_concurrent_tasks`
  for a provider in `appconfig.known_providers()` plus `"ollama"`, by
  prefix/suffix, not a fixed list — concurrency caps are a family, exactly
  as the task doc specified.
- `set_override(key, value)` — validates *shape* only (the five bool/int
  keys split by `_BOOL_KEYS`), the same way a `config.toml` value of the
  right TOML type would already arrive shaped. Range/clamping (e.g.
  `max_concurrent_tasks >= 1`) stays each call site's own job, unchanged —
  this layer does not duplicate it.
- `get_override(key)` returns `(present, value)`, not `.get(key, default)` —
  a tri-state lookup, because `0` is a legitimate queue depth and must be
  distinguishable from "not set."
- `clear_override(key)`, `all_overrides()` (read-only, for the doctor
  report), and `config_value_live(dotted_key, default)` — checks the
  override first, falls back to `appconfig.config_value()` otherwise. This
  is the only new call shape; `config_value()` itself is untouched.
- Nothing here ever touches disk. An override lives exactly as long as the
  process that set it — restarting the backend returns every value to
  whatever `config.toml` says, by design (Do-NOT #3 in the task doc).

## The seven call sites

Each one now calls `appconfig_overrides.config_value_live(...)` instead of
`appconfig.config_value(...)`, same key, same fallback default, no other
change to the call site's own parsing/warning/clamping logic:

- `backend\runtime\scheduler.py` — `provider_capacity()` (the `specific` and
  `general` lookups, lines formerly 87–88) and `turn_queue_depth()` (line
  formerly 269). `import appconfig` replaced with `import appconfig_overrides`
  — nothing else in this file used `appconfig` directly.
- `backend\managers\context.py` — the `project.allow_outside_project` lookup
  inside `build()` (line formerly 842). The unrelated `context.budget_tokens`
  lookup a few lines up is untouched; it is not one of the seven.
- `backend\runtime\runtime.py` — the `research.stale_after_days` lookup
  inside `_build_reference_index()` (line formerly 981) and the
  `project.allow_outside_project` lookup inside the attachment-scope helper
  (line formerly 1349).
- `backend\managers\project_manager.py` — the `index.respect_gitignore`
  lookup inside `_build_index()` (line formerly 204). The adjacent
  `index.max_file_kb` lookup on the very next line is untouched.

`git diff --stat` against `bootstrap.py`, `agents\executor.py`,
`attachment_manager.py`, and `project_manager.py`'s `max_file_kb`/
`share_project_memory` lines confirms zero changes outside these seven —
Done-when #8.

## `backend\runtime\events.py`

Two new `IncomingEventType` members (`SET_BEHAVIOUR_OVERRIDE =
"SetBehaviourOverride"`, `GET_DOCTOR_REPORT = "GetDoctorReport"`) and two new
`OutgoingEventType` members (`BEHAVIOUR_OVERRIDE_SET = "BehaviourOverrideSet"`,
`DOCTOR_REPORT = "DoctorReport"`). No change to `IncomingEvent`/`OutgoingEvent`
themselves or to envelope serialization.

## `backend\runtime\runtime.py`

- `_handle_set_behaviour_override` (registered beside
  `_handle_set_provider_api_key` — both are Settings-tab writes): reads
  `key` (required) and `value` (optional — its *presence*, not its value,
  decides set vs. clear, mirroring `DetachContext`'s shape). Calls
  `appconfig_overrides.set_override`/`clear_override`, converts a raised
  `ValueError` into `InvalidEventError` at the wire boundary, and always
  emits `BehaviourOverrideSet` carrying the *full* current override map —
  not just the key that changed — matching `ProviderApiKeySet`'s precedent
  of echoing back resulting state.
- `_handle_get_doctor_report`: `import cli` **inside the handler**, not at
  module level — `cli.py` imports `Runtime` from this module, so a
  top-level import here would be circular. By the time this handler runs,
  both modules are already fully loaded, so the deferred import (same
  pattern `appconfig.py` already uses for `logging_setup`) resolves cleanly.
  Calls `await cli.build_doctor_report()` and emits it verbatim as
  `DoctorReport`.
- Both registered in `_register_handlers()`.

## `backend\cli.py`

- `build_doctor_report()` (NEW, `async`) — every fact `cmd_doctor` used to
  print, as a dict, keyed one-per-fact (not one-per-printed-line — the
  provider list is a list of dicts, not pre-formatted strings). Calls
  `await _ollama_probe()` directly, wrapped in its own `try/except` so
  `GetDoctorReport` and `cmd_doctor` get identical failure text without a
  second `asyncio.run()`.
- `_print_doctor_report(report)` (NEW) — reproduces `cmd_doctor`'s exact
  pre-episode stdout, reading from the dict. `cmd_doctor` is now three lines:
  build the report (`asyncio.run(build_doctor_report())`), print it, return
  `EXIT_OK`.
- `_ollama_probe()` (NEW, `async`) — the coroutine that used to live nested
  inside `_ollama_report()`'s `probe()`. `_ollama_report()` itself is
  unchanged in behavior (`asyncio.run(_ollama_probe())`, same exception
  handling) and stays CLI-only, exactly as the task doc's Correction
  section specifies — it is not called from `build_doctor_report()`.
- `cmd_doctor_via_wire(runtime, args)` (NEW) — drives `GetDoctorReport`
  through `call()`, same pattern as B20's `cmd_context_report`'s
  `--via-wire` path, printed as JSON.
- `cmd_behaviour_set`/`cmd_behaviour_clear` (NEW) — drive
  `SetBehaviourOverride` through `call()`. `_parse_cli_value(raw)` tries
  `json.loads` first (so `0`, `true`, `"a string"` all work without a
  `--json` flag) and falls back to the raw string.
- `_exists_text(bool)` (NEW) replaces the now-dead `_exists(path)` helper
  (deleted — `build_doctor_report()` stores `*_exists` as booleans, not
  pre-formatted strings, since the dict has to be JSON-serializable).
- Parser: `doctor --via-wire`; a new `behaviour` subcommand tree (`set
  <key> <value>`, `clear <key>`) — this episode's *only* CLI surface for
  overrides, since (unlike doctor) there is no pre-existing non-wire path.
- `_EXPECTED_REPLY` gained `"GetDoctorReport": {"DoctorReport"}` and
  `"SetBehaviourOverride": {"BehaviourOverrideSet"}`.
- `main()`'s special-cased `doctor` branch now checks `args.via_wire` before
  falling through to the synchronous `cmd_doctor(args)` path; `behaviour
  set`/`behaviour clear` were added to the ordinary `handlers` dict,
  dispatched through `_with_runtime` like every other event-backed command.

## `backend\tests\manual_b26_behaviour_overrides.py` (NEW)

See "Bugs caught," below, for why this file exists at all. One long-lived
`Runtime`, built once via `build_runtime(single_instance=False, say_hello=False,
heartbeat=False)` — the exact same construction `manual_b12_concurrency.py`
uses, for the identical reason. Drives three sections via `runtime.receive()`
directly (no CLI, no separate processes), draining the outbox between steps:

1. `SetBehaviourOverride(chat.queue_depth=0)`, then two `SendMessage`s back
   to back on one session (echo provider — no network, but still takes the
   turn lane for an inline send; only the *provider* lane is waived for
   `"echo"`, per `agents\executor.py`). Confirms the second is refused with
   `queue_full` immediately.
2. `SetBehaviourOverride(chat.queue_depth)` with no `value` (clear), then
   two more `SendMessage`s. Confirms the second now queues instead of
   erroring — proving the clear actually fell back to the default.
3. `SetBehaviourOverride(research.stale_after_days=3)`, then
   `GetDoctorReport` in the same process. Confirms the override appears
   under `behaviour_overrides` — and, incidentally, that `GetDoctorReport`
   does not raise inside this process's running event loop.

Exits non-zero if any `check()` fails, matching B11/B12/B19/B22/F8's
existing convention for a deterministic, human- or gate-run script.

---

## Manual Test Log (Episode B26)

Two scripts, because Done-when #1/#2/#7 cannot be checked the same way as
#3/#4/#5/#6/#8/#9 — see "Bugs caught" for the full reasoning.

**PowerShell CLI-sequence script** (Done-when #3, #4, #5, #6, #8, #9), run
against a scratch `APPDATA`/`LOCALAPPDATA` profile:

1. `cli.py doctor` (no override set) printed cleanly on a profile with no
   config at all.
2. `cli.py doctor --via-wire` returned valid JSON with no traceback, from
   inside the wire handler's own running event loop — Done-when #6.
3. `cli.py behaviour set database.path "C:\nope"` returned `Error`
   (`Unknown behaviour override key: 'database.path'`), not
   `BehaviourOverrideSet` — Done-when #3.
4. `cli.py behaviour set chat.queue_depth 7` in one process, followed by
   `cli.py doctor --via-wire` in a fresh process: `behaviour_overrides` came
   back empty — Done-when #9, restart (a fresh process) clears every
   override.
5. `git diff --stat` against `bootstrap.py`, `agents\executor.py`,
   `attachment_manager.py`, and `project_manager.py`'s `max_file_kb`/
   `share_project_memory` lines: no output — Done-when #8.
6. Done-when #4 and #5, confirmed by direct comparison, not inspection:
   the real pre-B26 `cmd_doctor` (loaded from `git show HEAD:backend/cli.py`
   at the correct repo-relative path, so `_repo_root()` resolves correctly)
   was run against the same scratch profile and diffed with `Compare-Object`
   against the post-episode `cli.py doctor` output — **empty diff,
   byte-identical**. `cli.py doctor --via-wire`'s JSON payload carries the
   same facts as the formatted text, modulo presentation.

**Long-lived-process script** (Done-when #1, #2, #7),
`uv run python tests\manual_b26_behaviour_overrides.py` against a scratch
profile:

```
=== #1  chat.queue_depth=0 makes a second send refused immediately
  [PASS] BehaviourOverrideSet echoes the override back  -- {'key': 'chat.queue_depth', 'overrides': {'chat.queue_depth': 0}}
  [PASS] the second turn is refused with queue_full at depth 0  -- codes=['queue_full']

=== #2  clearing the override restores the default (depth 1)
  [PASS] BehaviourOverrideSet with no 'value' clears it  -- {'key': 'chat.queue_depth', 'overrides': {}}
  [PASS] at the default depth the second turn queues rather than erroring  -- codes=[]

=== #7  GetDoctorReport reflects an active override
  [PASS] GetDoctorReport did not raise (proves the Correction was applied)
  [PASS] behaviour_overrides includes the still-active override  -- {'research.stale_after_days': 3}

ALL CHECKS PASSED
```
Exit code 0.

Confirmed again by Aryan in his own final pass, after correcting the
subdirectory paths used to invoke this script (the script's own source did
not change — only how it was run did).

---

## Bugs caught during manual testing

One real one, and it is a gap in the task doc's own verification gate, not
in the code it was meant to check.

### The verification gate assumed CLI invocations share process state. They don't, by design.

The task doc's original PowerShell script tested Done-when #1/#2/#7 with
sequences like:

```powershell
uv run python cli.py behaviour set chat.queue_depth 0
uv run python cli.py send "first" --session $sid
uv run python cli.py send "second" --session $sid
# EXPECT: the second send is refused with queue_full
```

Running this literally: both sends completed normally, `TaskDone` both
times, never `queue_full`. Separately, `cli.py behaviour set chat.queue_depth
7` followed by `cli.py doctor --via-wire` in a new command showed
`behaviour_overrides: {}` — the override appeared to vanish instantly.

Neither is a bug in the override layer. `cli.py` builds a **brand-new
`Runtime`** on every invocation (`build_runtime(single_instance=False, ...)`
inside `_with_runtime`) — a second `uv run python cli.py ...` call is a
second process with its own empty in-memory state, not a second request to
a shared one. A behaviour override is deliberately process-lifetime-only
(this episode's own Do-NOT #3), and `runtime\scheduler.py`'s turn-queue
occupancy is exactly as process-local — that file's own docstring already
says so, about a different cap: *"driving three sends through three CLI
processes bypasses the scheduler entirely."* The same sentence is true of
every piece of in-memory state this episode added. The gate's own two
`send` lines, and its `behaviour set` + `doctor --via-wire` pairs, each
spanned two processes and so could never have demonstrated what their
`EXPECT` comments claimed — independent of whether the backend code was
correct.

Found by actually running the gate as written (not by reading it and
spotting the flaw) and observing the no-op result in both the `send`
sequence and the `doctor` reflection. Fixed by writing
`tests\manual_b26_behaviour_overrides.py`: one `Runtime`, held open for the
whole check, so a `SetBehaviourOverride` and the `SendMessage`/
`GetDoctorReport` that is supposed to observe its effect run inside the
*same* process — exactly the shape `manual_b12_concurrency.py` already
needed, and says why, for the provider-lane cap. The task doc's own
Verification gate section was rewritten to split Done-when items by which
script can actually prove them, rather than leaving the original script's
false `EXPECT` comments in place for the next person to trust.

**The generalizable lesson:** a verification gate for in-memory,
process-lifetime state (an override, a scheduler's lane occupancy, anything
not in the database) cannot be proven across separate `cli.py` invocations,
no matter how the commands are sequenced — only a single held-open `Runtime`
can observe its own writes. State that lives in the database (sessions,
tasks, messages) *does* survive across `cli.py` invocations, via the shared
SQLite file under WAL — that's what makes most of this project's existing
CLI-sequence gates valid. The distinction is not "does this episode add a
new wire event," it's "where does the state this event touches actually
live."

---

## What the next episode inherits

- **F10b can now be scoped for real.** `SetBehaviourOverride`/
  `BehaviourOverrideSet` and `GetDoctorReport`/`DoctorReport` both exist and
  are verified: the Behaviour tab writes through the former (plus its own
  `state.json` for persistence across restarts — a Go-side concern this
  episode does not touch), and the About tab's `point doctor` action calls
  the latter and renders its dict in a scrollable card. F10b's prompt can
  reference this file instead of a placeholder.
- **CLI invocations cannot observe each other's in-memory overrides, by
  design — and this will not bite F10b.** F10b's frontend drives both wire
  events through the *one* long-lived backend connection the real app
  always has, so it will never hit the gap this episode's own first-draft
  verification gate fell into. It matters only for a human re-verifying
  this episode by hand with sequential CLI commands. If a future episode's
  verification gate proposes proving backend state across two separate
  `cli.py` calls, check first whether that state is in-memory-only (like
  this episode's overrides, or `scheduler.py`'s lane occupancy) or
  DB-backed (like sessions/tasks, which do survive across `cli.py`
  invocations) — only the latter can actually be tested that way.
- **Restart behavior is a frontend decision, not a backend gap.** Because
  overrides are process-lifetime-only by design, F10b's Behaviour tab needs
  to decide what to show after a backend restart: either re-send every
  override from its own persisted `state.json` on reconnect, or show the
  user that an override reverted and let them re-apply it. This episode
  guarantees the fact F10b has to design around — the backend never
  remembers a Behaviour override on its own — without picking for it.
- **Scrollback and idle-unload timeout remain unspecced**, deliberately.
  Scrollback has no backend concept and may never need one. Idle-unload
  timeout is B15's (Lifecycle & Resources) to define; F10b should treat it
  as not-yet-functional in the UI rather than wiring a key this episode did
  not create.
- **Demo mode (F10c) is unaffected** — a new provider type, fixture
  library, and sample project, none of which this episode touches.
- `tests\manual_b26_behaviour_overrides.py` is a real regression script,
  kept in the repo (not a one-off) — if a later episode adds an eighth
  override key or changes how `chat.queue_depth` governs the turn queue,
  re-run it rather than assuming the existing three Done-when checks still
  hold.
