# Episode B26 — Behaviour Overrides + `doctor` on the Wire

**Two gaps, found while scoping F10b, both transport-only. (1) The Behaviour
tab's settings (`chat.queue_depth`, `limits.*.max_concurrent_tasks`,
`project.allow_outside_project`, `index.respect_gitignore`,
`research.stale_after_days`) are real `config.toml`-sourced values read live
by the scheduler and three managers, with no in-memory layer a UI change can
sit in front of — "Settings never writes `config.toml`" currently means
Settings cannot do anything to these values at all. (2) `point doctor`'s
report exists only as `print()` statements inside a CLI subcommand, with no
caller that gets the data back as data. Give the Behaviour tab a wire event
that sets/clears a named override in memory, give every `config_value()` call
site Behaviour owns a layer to check first, and give the About tab a wire
event that returns `doctor`'s own report, unchanged, as a payload instead of
stdout. Nothing else.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B26-behaviour-overrides-doctor-wire.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. Both halves move existing values and existing print
statements across the wire boundary; neither invents a computation. The one
real landmine is documented below under "Correction before you start" —
read it before touching `cli.py`.

**Depends on:** B6 (envelope, dispatcher), `appconfig.config_value` (every
episode since B8), `cli.py`'s `cmd_doctor` (current form, pre-this-episode).

**Consumed by:** F10b (`design/tasks/frontend/INDEX.md`'s F10 split note,
rows 40–41) — the Behaviour tab and the About tab's `point doctor` action,
both of which are currently unbuildable against real backend state. F10b's
own session stopped before scoping itself further until this episode exists.

---

## Why this episode exists

Found while scoping F10 (Session 16), same pattern as B20/B21/B23/B24/B25:
a frontend episode's spec assumed backend surface that does not exist, so the
frontend session stopped and named the gap rather than building a stub
against it.

`design/tasks/frontend/F10-settings-first-run.md`'s Settings popup spec
(UI-03) says:

- **Behaviour** — queue depth, idle-unload timeout, concurrency caps,
  scrollback, `allow_outside_project`, `index.respect_gitignore`,
  `stale_after_days`.
- **About** — versions, paths, and a **`point doctor`** action rendering the
  backend's report in a scrollable card.
- **Settings never writes `config.toml`.** Values changed in the UI are
  written to `state.json` as overrides.

`state.json` is a Go-only file (`frontend\internal\config\config.go`); the
Python backend process never reads it. So "written to `state.json` as
overrides" describes what Go does with the value for its own persistence —
it says nothing about how the *backend* ever finds out the override exists.
Today it has no way to: every one of these values is read with
`appconfig.config_value()`, which only ever looks at `config.toml`, every
single call:

```
backend\runtime\scheduler.py:87   limits.<provider>.max_concurrent_tasks
backend\runtime\scheduler.py:88   limits.max_concurrent_tasks
backend\runtime\scheduler.py:269  chat.queue_depth
backend\managers\context.py:842   project.allow_outside_project
backend\runtime\runtime.py:1349   project.allow_outside_project
backend\runtime\runtime.py:981    research.stale_after_days
backend\managers\project_manager.py:204  index.respect_gitignore
```

`point doctor` is a separate problem with the same shape: `cli.py:1197`'s
`cmd_doctor` is a synchronous function, invoked directly from `main()`
(`cli.py:1635-1636`, **not** through `_with_runtime`/`asyncio.run` the way
every event-driven command is), that builds its report with a sequence of
`print()` calls straight to stdout. There is no function anywhere that
*returns* the report; the only way to get it is to spawn the process and
capture its stdout, which is a CLI affordance, not a wire one. The About
tab's `point doctor` action has nothing to call.

Both gaps need "the same kind of new backend surface" (INDEX.md's own words
for them) — an in-memory layer the wire can read and write, sitting in front
of a report/lookup that already exists and is not to be recomputed.

**Scrollback** and **idle-unload timeout** are in F10's Behaviour list but
are not `config_value()`-backed settings today — `grep -rn "scrollback"
backend\` and `grep -rn "idle.unload\|idle_unload" backend\` both return
nothing. Scrollback is frontend-only rendering state; idle-unload timeout
belongs to B15 (Lifecycle & Resources), which is not built. **Neither is in
this episode's scope.** See "Do NOT".

---

## Correction before you start: `_ollama_report()` cannot be reused as-is

`cmd_doctor`'s providers section calls `_ollama_report()`
(`cli.py:1302-1316`), which wraps its real work in `asyncio.run(probe())`.
That is fine for `cmd_doctor` today because `cmd_doctor` runs synchronously,
outside any event loop, called directly from `main()` before `asyncio.run`
is reached for anything else.

The new wire handler does **not** have that luxury: it runs as a dispatcher
callback inside the backend's own running event loop (`main.py`'s loop,
already started before any event is received). Calling `asyncio.run()` from
inside a running loop raises `RuntimeError: asyncio.run() cannot be called
from a running event loop` — this will fail the very first time the handler
is exercised, not silently misbehave.

Fix by extracting the coroutine, not by rewriting the probe logic:

```python
async def _ollama_probe() -> str:
    ...  # the body currently inside _ollama_report()'s nested probe()

def _ollama_report() -> str:
    """CLI-only: owns the event loop, so it may create one."""
    try:
        return asyncio.run(_ollama_probe())
    except Exception as exc:
        return f"not reachable ({exc})"
```

The doctor-report builder (see Build §2) calls `await _ollama_probe()`
directly — no second event loop, same probe, same exception handling moved
to the builder's own try/except so both callers get identical failure text.

---

## Build

### 1. Behaviour overrides — in-memory layer

New module: `backend\appconfig_overrides.py` (sibling to `appconfig.py`, not
inside it — `appconfig.py`'s own docstring says it "never asks anyone
anything" and is about files on disk; an in-memory runtime layer is a
different kind of thing and belongs next to it, not folded in).

```python
_OVERRIDES: dict[str, Any] = {}

# The only keys the Behaviour tab controls live. Not a general
# config-override mechanism -- an unlisted key is rejected, not silently
# accepted, so a frontend typo fails loudly instead of being ignored.
ALLOWED_KEYS = {
    "chat.queue_depth",
    "limits.max_concurrent_tasks",
    "project.allow_outside_project",
    "index.respect_gitignore",
    "research.stale_after_days",
    # Per-provider caps are a family, not a fixed set of keys -- validated
    # by prefix instead of exact membership. See set_override().
}
_PROVIDER_CAP_PREFIX = "limits."
_PROVIDER_CAP_SUFFIX = ".max_concurrent_tasks"

def set_override(key: str, value: Any) -> None: ...
def clear_override(key: str) -> None: ...
def get_override(key: str) -> tuple[bool, Any]:
    """(present, value). A tri-state lookup, not `.get(key, default)` --
    the caller needs to distinguish "not overridden" from "overridden to
    a falsy value" (0 is a legitimate queue depth)."""
def all_overrides() -> dict[str, Any]:
    """For the doctor report -- which values are overridden, read-only."""
```

`set_override` raises `ValueError` (becomes `InvalidEventError` at the wire
boundary — see §1b) for a key that is neither in `ALLOWED_KEYS` nor matches
`limits.<provider>.max_concurrent_tasks` for a provider
`appconfig.known_providers()` plus `"ollama"` would recognize. Validate the
*shape* of a value where `config_value`'s own callers already do (queue
depth and concurrency caps must parse as `int`, `allow_outside_project` and
`respect_gitignore` as `bool`, `stale_after_days` as `int`) — reuse the
existing parse-and-warn pattern each call site already has (e.g.
`scheduler.py:269-278`'s `int(value)` / warn-and-fallback shape) rather than
inventing a second validation vocabulary. An override that fails to parse is
rejected at `set_override` time with the same message a bad `config.toml`
value would log, not accepted and left to fail later inside the scheduler.

**Add a lookup wrapper, not a replacement for `config_value`:**

```python
# appconfig_overrides.py
def config_value_live(dotted_key: str, default: Any = None) -> Any:
    present, value = get_override(dotted_key)
    if present:
        return value
    return appconfig.config_value(dotted_key, default)
```

Change the seven call sites listed in "Why this episode exists" from
`appconfig.config_value(...)` to `appconfig_overrides.config_value_live(...)`.
This is the entire "business logic" change this episode makes to the
scheduler/managers: a different function with the same signature and the
same fallback value, checked first. No call site's own parsing, warning, or
clamping logic changes.

Leave every other `config_value()` call site alone (`bootstrap.py`'s
`database.path`, `agents\executor.py`'s provider/model/effort defaults,
`attachment_manager.py`'s `attachments.max_kb`, `project_manager.py`'s
`index.max_file_kb`, `project_manager.py`'s `git.share_project_memory`) —
none of these are in F10's Behaviour list, and widening the override surface
to "every config key" is explicitly not what was asked for.

### 1b. Wire event: `SetBehaviourOverride`

New `IncomingEventType.SET_BEHAVIOUR_OVERRIDE = "SetBehaviourOverride"`.
Payload: `{"key": str, "value": Any}` to set, or `{"key": str}` with no
`value` field to clear (mirrors `DetachContext`'s shape of "same key,
presence of a field changes the verb" rather than adding a second event
type for clearing one override).

Handler `_handle_set_behaviour_override`, registered beside
`SET_PROVIDER_API_KEY` (same neighborhood — both are Settings-tab writes):

```python
async def _handle_set_behaviour_override(self, event: IncomingEvent) -> None:
    key = event.payload.get("key")
    if not key:
        raise InvalidEventError("SetBehaviourOverride requires 'key'")
    if "value" in event.payload:
        try:
            appconfig_overrides.set_override(key, event.payload["value"])
        except ValueError as exc:
            raise InvalidEventError(str(exc)) from exc
    else:
        appconfig_overrides.clear_override(key)
    await self._emit(OutgoingEvent(
        type=OutgoingEventType.BEHAVIOUR_OVERRIDE_SET,
        request_id=event.request_id,
        payload={"key": key, "overrides": appconfig_overrides.all_overrides()},
    ))
```

New `OutgoingEventType.BEHAVIOUR_OVERRIDE_SET = "BehaviourOverrideSet"`.
Payload carries the full current override map back, not just the one key
just changed — so the About tab (which shows "which values are overridden")
and the Behaviour tab (which needs to reflect the current state after a
change) both have one event to listen for, matching `ProviderApiKeySet`'s
precedent of echoing back full resulting state rather than just the delta.

**No `GetBehaviourOverrides` pull event.** The doctor report (§2) already
carries `all_overrides()` for the About tab's display of "which values are
overridden"; a Behaviour tab freshly opened reads its current effective
values from the same place it always has — `config.toml` plus whatever
`BehaviourOverrideSet` pushes it have already told it this session. Adding a
second read path for the same map is the kind of thing B20's own doc warned
against: two ways to learn the same fact disagree eventually.

### 2. `doctor` report, structured and reusable

Extract `cmd_doctor`'s body into an async function that **returns** a dict
instead of printing, with `cmd_doctor` becoming a thin formatter over it:

```python
async def build_doctor_report() -> dict[str, Any]:
    """Every fact cmd_doctor currently prints, as data. No print() calls
    live here -- cmd_doctor formats this dict to stdout; the wire handler
    ships it verbatim, both reading the same facts once."""
    ...

def cmd_doctor(args: argparse.Namespace) -> int:
    report = asyncio.run(build_doctor_report())
    _print_doctor_report(report)   # the existing print() sequence, now
                                    # reading from `report` instead of
                                    # calling each helper inline
    return EXIT_OK
```

`build_doctor_report()` calls the same helpers `cmd_doctor` calls today
(`_go_binary_status`, `_dependency_report`, `_ollama_probe` per the
Correction above, `appconfig.config_error`, `appconfig.key_source`,
`appconfig.resolve_api_key`, `appconfig.mask_secret`, `mock_provider.
is_enabled`, schema version lookups) and assembles their results into a
dict with the same facts the current print sequence states, one key per
printed line/section — version info, go frontend status, python version,
dependency report, database path/schema state, config/credentials/state/log
paths and existence, per-provider key source (masked), ollama report, mock
provider status. Add one more key this episode needs that `cmd_doctor`
doesn't currently print: `"behaviour_overrides": appconfig_overrides.
all_overrides()`, so the About tab's doctor card is also where "which
values are overridden" (F10's own requirement, quoted above) becomes
visible, without a second event.

`_print_doctor_report(report)` reproduces `cmd_doctor`'s exact current
stdout output from the dict's fields — **Done-when #4 requires this to be
byte-identical to today's output**, not merely "equivalent."

### 2b. Wire event: `GetDoctorReport`

New `IncomingEventType.GET_DOCTOR_REPORT = "GetDoctorReport"`, no payload.
New `OutgoingEventType.DOCTOR_REPORT = "DoctorReport"`, payload is
`build_doctor_report()`'s dict verbatim — B20's rule, for B20's reason: one
shape, no subset.

```python
async def _handle_get_doctor_report(self, event: IncomingEvent) -> None:
    report = await build_doctor_report()
    await self._emit(OutgoingEvent(
        type=OutgoingEventType.DOCTOR_REPORT,
        request_id=event.request_id,
        payload=report,
    ))
```

### 3. CLI surface, for the gate

Add `cli.py doctor --via-wire`, same shape as B20's `context report
--via-wire`: drives `GetDoctorReport` through `call()` over the event pipe
instead of calling `build_doctor_report()` in-process, printed as JSON. This
is the only way the verification gate can compare the wire payload against
`build_doctor_report()`'s direct output without Go. Add `"GetDoctorReport":
{"DoctorReport"}` to `_EXPECTED_REPLY`.

Also add `cli.py behaviour set <key> <value>` / `cli.py behaviour clear
<key>`, driving `SetBehaviourOverride` through `call()` the same way —
there is no non-wire path to exercise this feature at all (unlike doctor,
which has one), so this is the *only* CLI surface for it, not a parity
check. Parse `<value>` as JSON if it parses, else treat as a raw string, so
`behaviour set chat.queue_depth 3` and `behaviour set project.
allow_outside_project true` both work from a shell without a `--json` flag.

---

## Do NOT

- **Do not add `scrollback` or an idle-unload-timeout key.** Scrollback is
  render-only frontend state with no backend concept to override.
  Idle-unload timeout belongs to B15 (Lifecycle & Resources), which does
  not exist; inventing a config key for a feature with no backend behind
  it yet would be exactly CLAUDE.md's "narrower local type" trap in
  reverse — a name claimed before the episode that owns it exists. If F10b
  needs a placeholder for either in the UI, that is F10b's call to make
  with no corresponding wire plumbing from this episode.
- **Do not make the override layer a general config-override mechanism.**
  `ALLOWED_KEYS` (plus the one provider-cap-prefix family) is the whole
  list. A ninth key is a decision for whichever episode needs it, not a
  door left open by this one.
- **Do not persist overrides anywhere in the backend.** No table, no row in
  `state.json` (which the backend doesn't read anyway), no file. An
  override lives exactly as long as the process does — restarting the
  backend returns every Behaviour value to whatever `config.toml` says,
  by design: "Settings never writes `config.toml`" means a restart must
  fall back to the user's real file, not to a shadow copy of the override.
- **Do not change what `config_value()` itself does.** It still reads only
  `config.toml`, unmodified, for every one of its existing callers. The
  override layer sits in front of seven call sites; it is not a change to
  the function those seven (and every other) call site shares.
- **Do not touch `cmd_doctor`'s printed output format.** Refactor it to
  read from `build_doctor_report()`'s dict; do not reword, reorder, or
  add/remove a line while doing so. A format change and a transport change
  landing together make it impossible to tell which one a diff describes.
- **Do not build the Settings UI, the Behaviour tab, or the About tab's
  card.** F10b's job. This episode is the wire and the override layer only.
- **Do not add a `GetBehaviourOverrides` pull event.** See §1b.
- **Do not wire a push** for `BehaviourOverrideSet` the way B20 pushes
  `ContextReport` after every turn. This event only ever fires as a direct
  reply to `SetBehaviourOverride` — nothing else in the backend changes an
  override, so there is no second trigger to push from.

---

## Done when

1. `SetBehaviourOverride` with `{"key": "chat.queue_depth", "value": 0}`
   returns `BehaviourOverrideSet` with that key in its `overrides` map, and
   a new conversational turn submitted immediately after is governed by
   `0`, not whatever `config.toml` says — observable via
   `turn_queue_depth()`'s actual refusal behavior (a second queued send
   gets `queue_full` immediately where it would have queued before).
   **Requires one long-lived process** (`tests\manual_b26_behaviour_overrides.py`)
   — see "Verification gate" below for why a sequence of separate `cli.py`
   calls cannot observe this.
2. `SetBehaviourOverride` with `{"key": "chat.queue_depth"}` (no `value`)
   clears the override; the next lookup falls back to `config.toml`
   (or the hardcoded default if `config.toml` has no `chat.queue_depth`
   either). **Requires one long-lived process**, same reason as #1.
3. `SetBehaviourOverride` with an unlisted key (e.g. `"database.path"`)
   is rejected with `Error`, and no override is recorded for it.
4. `cli.py doctor` (no `--via-wire`) prints **exactly** what it printed
   before this episode — same lines, same order, same wording — on a
   scratch profile with no config at all. Diffed, not eyeballed.
5. `cli.py doctor --via-wire` and `cli.py doctor` report the identical set
   of facts (modulo JSON vs. formatted-text presentation) for the same
   scratch profile in the same process lifetime.
6. `GetDoctorReport` does not raise `RuntimeError: asyncio.run() cannot be
   called from a running event loop` — proof that the Correction above was
   actually applied, not just acknowledged.
7. After at least one `SetBehaviourOverride` call, `GetDoctorReport`'s
   payload includes it under `behaviour_overrides`. **Requires one
   long-lived process**, same reason as #1 — a `SetBehaviourOverride` from
   one `cli.py` invocation is invisible to a `GetDoctorReport` from another.
8. Every `config_value()` call site **not** in this episode's seven-item
   list is provably untouched — `git diff` shows no change to
   `bootstrap.py`, `agents\executor.py`, `attachment_manager.py`'s
   `max_kb` line, or `project_manager.py`'s `max_file_kb`/
   `share_project_memory` lines.
9. Restarting the backend process clears every override — a fresh
   `GetDoctorReport` after restart shows an empty `behaviour_overrides`,
   even though the prior process had set one.

---

## Verification gate (PowerShell, scratch profile)

**Two scripts, not one, and that split is load-bearing.** `cli.py` builds a
brand-new `Runtime` on every invocation (`build_runtime(single_instance=False,
...)` inside `_with_runtime`) — a second `uv run python cli.py ...` call is a
second process with its own empty in-memory state, not a second request to
the first one. A behaviour override lives exactly as long as the process
that set it (Do-NOT #3, by design), and `runtime\scheduler.py`'s turn-queue
occupancy is exactly as process-local — that file's own docstring already
says so: "driving three sends through three CLI processes bypasses the
scheduler entirely." Concretely: `cli.py behaviour set chat.queue_depth 0`
followed by a *separate* `cli.py doctor --via-wire` call will **not** show
that override. The two are different processes; nothing carries over.

So:
- Done-when **#3, #4, #5, #6, #8, #9** are each self-contained within one
  invocation, or deliberately test what a fresh process (a restart) sees —
  the **PowerShell script** below checks these.
- Done-when **#1, #2, #7** each require setting an override and then
  observing its effect *in the same process* — the **long-lived-process
  script**, `tests\manual_b26_behaviour_overrides.py`, checks these, in the
  same convention B11/B12/B19/B22/F8 already established for exactly this
  class of check (see `manual_b12_concurrency.py`'s own docstring for the
  identical argument about the provider-lane cap).

Per CLAUDE.md: both scripts point `$env:APPDATA`/`$env:LOCALAPPDATA` at a
scratch directory for their whole duration, nothing under the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` ever named. The rename-aside
pattern is retired; do not reconstruct it even from an older doc.

### PowerShell script (Done-when #3, #4, #5, #6, #8, #9)

```powershell
$ErrorActionPreference = "Stop"
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
$scratch = Join-Path $env:TEMP "point-scratch-b26"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # --- 4: doctor's printed output, captured before any override exists
    uv run python cli.py doctor | Out-File -Encoding utf8 "$scratch\doctor-before.txt"

    # --- 5, 6: doctor over the wire must match and must not crash on the
    #     ollama probe inside a running loop
    uv run python cli.py doctor --via-wire | Out-File -Encoding utf8 "$scratch\doctor-wire.json"
    Get-Content "$scratch\doctor-wire.json"
    # EXPECT: valid JSON, no traceback, same facts as doctor-before.txt

    # --- 3: an unlisted key is rejected
    uv run python cli.py behaviour set database.path "C:\nope"
    # EXPECT: Error, not BehaviourOverrideSet

    # --- 9: restart clears overrides that were never cleared explicitly
    uv run python cli.py behaviour set chat.queue_depth 7
    # (new process, same scratch profile, nothing re-sent)
    uv run python cli.py doctor --via-wire | Select-String "chat.queue_depth"
    # EXPECT: absent -- the override lived only in the process that set it
    # (this is the SAME fact that makes #1/#2/#7 need the other script --
    # here it is the thing being tested, there it is the obstacle)

    # --- 8: confirm the untouched call sites really are untouched
    git diff --stat -- backend\bootstrap.py backend\agents\executor.py `
        backend\managers\attachment_manager.py backend\managers\project_manager.py
    # EXPECT: no output, or only unrelated hunks this episode did not touch
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

**Eyes-on:** diff `doctor-before.txt` against a doctor run from before this
episode's changes (or against the formatted text embedded in
`doctor-wire.json` once reconstructed) to confirm Done-when #4 by actual
comparison, not by reading the refactor and assuming it preserved output.

### Long-lived-process script (Done-when #1, #2, #7)

```powershell
$ErrorActionPreference = "Stop"
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
$scratch = Join-Path $env:TEMP "point-scratch-b26-manual"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate | Out-Null
    uv run python tests\manual_b26_behaviour_overrides.py
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

EXPECT: `ALL CHECKS PASSED` and exit code 0. The script builds one `Runtime`
and, inside that single process, sets `chat.queue_depth=0`, sends two turns
back to back and confirms the second gets `queue_full` immediately (#1),
clears the override and confirms a second turn now queues instead (#2), and
sets a second override that `GetDoctorReport` — called in that same process
— reports under `behaviour_overrides` (#7).

---

## Handoff

- **F10b can now be scoped for real**: the Behaviour tab writes through
  `SetBehaviourOverride` (and its own `state.json` for persistence across
  restarts, which is a Go-side concern this episode does not touch), and
  the About tab's `point doctor` action calls `GetDoctorReport` and renders
  its dict in a scrollable card.
- **Restart behavior is a frontend decision, not a backend gap.** Because
  overrides are process-lifetime-only by design, F10b's Behaviour tab needs
  to decide what to show after a backend restart: either re-send every
  override from its own persisted `state.json` on reconnect, or show the
  user that an override reverted and let them re-apply it. This episode
  does not pick for F10b, but it does guarantee the fact F10b has to design
  around: the backend never remembers a Behaviour override on its own.
- **Scrollback and idle-unload timeout remain unspecced.** Scrollback has
  no backend concept and may never need one (it could be entirely
  frontend). Idle-unload timeout is B15's to define — F10b should treat it
  as not-yet-functional in the UI rather than wiring a key this episode
  deliberately did not create.
- **Demo mode (F10c) is unaffected** — a new provider type, fixture
  library, and sample project, none of which this episode touches.
- **CLI invocations cannot observe each other's overrides, by design — and
  this will not bite F10b.** `cli.py` builds a fresh `Runtime` per
  invocation, so a behaviour override set by one `cli.py behaviour set` call
  is invisible to the next `cli.py` call; only a script holding one `Runtime`
  for multiple operations (`tests\manual_b26_behaviour_overrides.py`) can see
  its own override take effect. This is not a gap: F10b's frontend drives
  `SetBehaviourOverride` and `GetDoctorReport` through the one long-lived
  backend connection the real app always has, so it will never hit this.
  It matters only for a human re-verifying this episode by hand with
  sequential CLI commands — exactly the trap the first attempt at this
  episode's own verification gate fell into. If a future episode's doc
  proposes proving backend state across two separate `cli.py` calls, check
  first whether the state in question is in-memory-only (like this one) or
  DB-backed (like sessions/tasks, which do survive across `cli.py`
  invocations via the shared SQLite file) — only the latter can actually be
  tested that way.
