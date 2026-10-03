# Episode B27 — Resolved Behaviour Values in `GetDoctorReport`

**`GetDoctorReport`'s payload already carries `behaviour_overrides` —
which keys currently have an active override and what they're set to
(B26). It has no way to answer the question underneath that: "if nothing
were overridden, what would `chat.queue_depth` actually read as right
now?" Nothing on the wire exposes `config.toml`'s real value or the
hardcoded fallback for any of the five Behaviour-tab keys — not because
B26 forgot, but because B26 never built a path to ask (F10b's own
Decision 2, found live: the frontend cannot read `config.toml` at all,
and deliberately did not get a `GetBehaviourOverrides` pull event either).
This episode adds exactly one new field to `build_doctor_report()`'s
existing dict — `behaviour_resolved`, one entry per key, each carrying
its resolved (pre-override) value and whether an override currently
masks it. No new wire event, no new CLI surface, no frontend change.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B27-doctor-resolved-behaviour-values.md`,
> once it exists) claims reality and completion. Code wins on any
> disagreement.

**Model:** Sonnet. One new field, computed from functions that already
exist (`appconfig.config_value`, `appconfig_overrides.get_override`); no
new computation, no new event type.

**Depends on:** B26 (`appconfig_overrides.py`, `GetDoctorReport`,
`build_doctor_report()` — all shipped and verified).

**Consumed by:** a *future*, separate frontend pass on F10b's Behaviour
tab (not scoped here, not this episode's job — see "Do NOT"). That
pass's whole reason to exist is F10b's own Decision 2: the Behaviour
tab currently shows `default`/`overridden → X` and can never show what
`default` numerically *means*, because nothing on the wire says so. This
episode is what makes that later pass possible; it does not build it.

---

## Why this episode exists

Found while re-verifying F10b live. The Behaviour tab's rows read
`default` or `overridden → X`, and `default` is deliberately never a
guessed number (F10b's Decision 2 — Go's `config.Load()` parses only the
`[ui]` table, so this process cannot read `chat.queue_depth` et al. from
`config.toml` itself, and B26 explicitly declined to add a
`GetBehaviourOverrides` pull event for the same reason B20's own doc
already gives: two ways to learn the same fact disagree eventually).
That was the right call for B26's own scope — but it leaves a real,
named gap: there is **no way at all**, from the frontend or from a human
running `cli.py doctor`, to see what a key currently resolves to when
no override is active. `GetDoctorReport`'s `behaviour_overrides` field
only ever lists what's overridden; a key with nothing set is simply
absent from it, which answers "is anything overridden" and nothing about
"what governs right now."

`build_doctor_report()` (`backend\cli.py:1202`) is the one place that
already assembles facts like this for exactly this purpose — it exists
so `point doctor` and `GetDoctorReport` read the same facts once. Adding
the resolved value here, rather than inventing a second wire event, is
the same call B26 itself made for `behaviour_overrides`.

---

## Build

### 1. `backend\appconfig_overrides.py` — the five keys' own hardcoded fallbacks, named once

```python
# The hardcoded fallback each of the five ALLOWED_KEYS' own real call
# site already falls back to when config.toml has no value for it --
# duplicated here, deliberately, not imported from each call site
# (scheduler.py's DEFAULT_MAX_CONCURRENT / turn_queue_depth's own literal
# `1`, runtime.py's `7` and two call sites' `False`, project_manager.py's
# `True`), because none of those four modules exists to be a shared
# source of truth for a doctor-only reporting need, and importing from
# all four to save one constant each would be a worse coupling than the
# duplication. If any of these five changes, its own call site AND this
# line must change together -- the same discipline credential.go's
# pasteSettleWindow already documents, frontend-side, for an identical
# cross-boundary duplication ("this package cannot import chat... so the
# value is reproduced rather than shared").
RESOLVED_DEFAULTS: dict[str, Any] = {
    "chat.queue_depth": 1,                    # scheduler.py:269 turn_queue_depth()
    "limits.max_concurrent_tasks": 3,         # scheduler.py:68 DEFAULT_MAX_CONCURRENT
    "project.allow_outside_project": False,   # context.py:843, runtime.py:1386
    "index.respect_gitignore": True,          # project_manager.py:205
    "research.stale_after_days": 7,           # runtime.py:1018
}


def resolved_behaviour_settings() -> dict[str, dict[str, Any]]:
    """For GetDoctorReport (B27): each of the five Behaviour-tab keys'
    actual config.toml value -- or RESOLVED_DEFAULTS' own fallback when
    config.toml has none -- plus whether an override currently masks it.

    Deliberately NOT config_value_live()'s own resolution: that function
    already applies the override, which is the one thing this is here to
    look PAST. "What does config.toml say, independent of anything
    layered on top of it" and "what governs the next real decision" are
    two different questions; config_value_live() already answers the
    second one everywhere it's called. This answers the first, plus a
    separate bit for whether the second currently differs from it.

    Scoped to exactly these five keys -- NOT the
    limits.<provider>.max_concurrent_tasks family _provider_cap_name()
    also recognizes. F10b's own Behaviour tab exposes only the general
    cap (its Decision 3); this field mirrors that tab's five rows
    exactly, not every key SetBehaviourOverride will accept.
    """
    out: dict[str, dict[str, Any]] = {}
    for key, default in RESOLVED_DEFAULTS.items():
        present, _ = get_override(key)
        out[key] = {
            "resolved": appconfig.config_value(key, default),
            "overridden": present,
        }
    return out
```

Placed below `all_overrides()`, above `config_value_live()` — reads
naturally as "the read-only reporting helpers" grouped together, which
`all_overrides()`'s own one-line doc already calls itself ("For the
doctor report").

### 2. `backend\cli.py` — one new key in the existing dict

In `build_doctor_report()` (`cli.py:1202`), immediately after the
existing `"behaviour_overrides"` line (`cli.py:1276`):

```python
"behaviour_overrides": appconfig_overrides.all_overrides(),
"behaviour_resolved": appconfig_overrides.resolved_behaviour_settings(),
```

That is the entire change to `cli.py`. `_print_doctor_report` (`cli.py:1280`)
is **not touched** — it does not print `behaviour_overrides` today and
will not print `behaviour_resolved` either, so Done-when #4 (byte-identical
`cli.py doctor` output) holds by construction, not by care. `GetDoctorReport`'s
handler (`runtime.py:617`, `_handle_get_doctor_report`) emits
`build_doctor_report()`'s dict verbatim already — no change needed there
at all; the new field reaches the wire for free the moment it exists in
the dict.

---

## Do NOT

- **Do not add a new wire event.** `behaviour_resolved` rides inside the
  existing `GetDoctorReport`/`DoctorReport` pair. If a *pull* event for
  just this field is ever wanted, that is a decision for whoever asks for
  it, not a default to reach for here — B26's own `behaviour_overrides`
  precedent already rejected a dedicated pull event for the identical
  reason (B26 §1b: "two ways to learn the same fact disagree eventually").
- **Do not touch `_print_doctor_report` or `cmd_doctor`'s printed output.**
  `point doctor`'s stdout stays byte-identical before and after this
  episode, verified the same way B26's own Done-when #4 was.
- **Do not add the per-provider `limits.<provider>.max_concurrent_tasks`
  family to `RESOLVED_DEFAULTS`.** F10b's Behaviour tab exposes only the
  general cap (Decision 3); this field mirrors exactly what that tab
  shows, not everything `SetBehaviourOverride` will accept. A later
  episode that actually builds per-provider rows can extend this map
  then.
- **Do not build the frontend half.** No Go changes, no rendering of the
  real number in the Behaviour tab, no change to `formatDoctorReport`
  (`frontend\internal\app\doctor.go`) or `behaviourRowDecor`
  (`behaviour.go`). This episode produces the fact; a separate pass
  consumes it once this is confirmed shipped. Do not add a TODO comment
  in the Go code referencing a field that does not exist in that process
  yet either — nothing on the frontend needs to know this episode
  happened until the pass that reads it exists.
- **Do not make `resolved_behaviour_settings()` call `config_value_live()`.**
  That would make `resolved` identical to the override's own value
  whenever one is active, defeating the entire point — Done-when #3
  below is specifically the check that this distinction holds.
- **Do not change `config_value()` or `config_value_live()` themselves.**
  Both are read, unmodified, by the one new function this episode adds.

---

## Done when

1. `GetDoctorReport`'s payload includes `behaviour_resolved`, a dict with
   exactly five keys — `chat.queue_depth`, `limits.max_concurrent_tasks`,
   `project.allow_outside_project`, `index.respect_gitignore`,
   `research.stale_after_days` — never the per-provider cap family, never
   a sixth key.
2. On a profile with no `config.toml` value for any of the five (a fresh
   scratch profile with an empty or absent file), every entry's
   `resolved` equals that key's own hardcoded fallback (`1`, `3`,
   `False`, `True`, `7` respectively) and every `overridden` is `false`.
3. With a REAL `config.toml` value set for at least one key (no override
   active for it), that entry's `resolved` reflects the FILE's value —
   not the hardcoded fallback — and `overridden` is still `false`.
4. With an override active for a key whose `config.toml` either has no
   value or a different value, that entry's `resolved` reflects
   `config.toml`'s value (or the fallback) — **never the override's own
   value** — while `overridden` is `true`. This is the one check that
   actually proves `behaviour_resolved` is not secretly
   `config_value_live()` wearing a new name.
5. `cli.py doctor`'s printed stdout is byte-identical before and after
   this episode, on the same scratch profile — diffed, not eyeballed,
   the same discipline B26's own Done-when #4 used.
6. Every other field already in `build_doctor_report()`'s dict —
   `behaviour_overrides` very much included — is unchanged in shape and
   value. `git diff` on `cli.py` shows exactly the one new line inside
   the returned dict, plus the new `appconfig_overrides.py` additions;
   nothing else moves.

---

## Verification gate

Same shape as B26's own: a direct `Runtime.receive()` script for the
in-process checks (#1–#4, which need one Runtime so an override set in
the same process is what `resolved_behaviour_settings()` is asked about),
plus a PowerShell byte-diff for the CLI-output check (#5), against a
scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule.

### Script (Done-when #1–#4), e.g. `backend\tests\manual_b27_doctor_resolved.py`

```python
# Pattern: backend\tests\manual_b26_behaviour_overrides.py -- one
# long-lived Runtime, built via build_runtime(single_instance=False,
# say_hello=False, heartbeat=False), driven via runtime.receive() with
# GetDoctorReport, draining the outbox between steps.

# 1. Fresh scratch profile, no config.toml value for any of the five
#    keys. GetDoctorReport -> behaviour_resolved has exactly 5 keys,
#    each `resolved` matching RESOLVED_DEFAULTS, each `overridden=False`.

# 2. Write a REAL config.toml value for research.stale_after_days (e.g.
#    14) directly to the scratch profile's config.toml, reload config if
#    needed (or construct a fresh Runtime against the same profile).
#    GetDoctorReport -> behaviour_resolved["research.stale_after_days"]
#    == {"resolved": 14, "overridden": False}.

# 3. SetBehaviourOverride(research.stale_after_days=3) in the SAME
#    process. GetDoctorReport -> behaviour_resolved["research.stale_after_days"]
#    == {"resolved": 14, "overridden": True} -- resolved UNCHANGED from
#    step 2, overridden now True. (behaviour_overrides, unchanged by this
#    episode, separately shows the override's own value, 3 -- confirm
#    both fields are present and don't contradict each other's OWN
#    documented meaning.)
```

### PowerShell (Done-when #5), scratch profile

```powershell
$ErrorActionPreference = "Stop"
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
$scratch = Join-Path $env:TEMP "point-scratch-b27"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    uv run python cli.py doctor | Out-File -Encoding utf8 "$scratch\doctor-after.txt"
    # Compare against a doctor run from BEFORE this episode's changes
    # (git show HEAD~N:backend/cli.py, same approach B26's own Done-when
    # #4/#5 proof used) -- Compare-Object, expect no output.
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

---

## Handoff

- **The frontend pass this unblocks** (not scoped, not started): F10b's
  Behaviour tab rows currently read `default`/`overridden → X`. Once
  `behaviour_resolved` ships, a Go-side pass can show the REAL number for
  the `default` case too (e.g. `default (3)`) by reading this new field
  off a `GetDoctorReport` the Settings popup already knows how to send
  (`onAboutSelect`, `settings.go`) — but that pass still has to decide
  WHEN to fetch it (on tab open? on popup open? only on demand via the
  existing "Run doctor" action?) and how to keep it from going stale
  while the popup is open, which this episode deliberately leaves open
  rather than guessing.
- **`RESOLVED_DEFAULTS` is a second copy of five literals that already
  exist elsewhere.** Whoever next changes one of chat.queue_depth's,
  limits.max_concurrent_tasks's, project.allow_outside_project's,
  index.respect_gitignore's, or research.stale_after_days's own
  hardcoded fallback at its REAL call site must update this map in the
  same change, or `point doctor`/`GetDoctorReport` will report a stale
  "default" that no longer matches what actually governs. Not wired to
  fail loudly if missed — a comment at each of the two definitions is
  the only guard, the same as `pasteSettleWindow`'s own precedent.
- **Demo mode (F10c) and the Keys tab remain unaffected and unscoped**,
  same as every prior F10b-adjacent note.
