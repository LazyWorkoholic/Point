# Episode B27 — Resolved Behaviour Values in `GetDoctorReport` ✅

**`GetDoctorReport`'s payload already carried `behaviour_overrides` (B26) —
which of the five Behaviour-tab keys currently have an active override and
what they're set to. It had no way to answer the question underneath that:
"if nothing were overridden, what would `chat.queue_depth` actually read as
right now?" This episode adds exactly one new field to
`build_doctor_report()`'s existing dict — `behaviour_resolved`, one entry
per key, each carrying its resolved (pre-override) value and whether an
override currently masks it. No new wire event, no new CLI surface, no
frontend change — `appconfig_overrides.py` gains `RESOLVED_DEFAULTS` and
`resolved_behaviour_settings()`; `cli.py`'s `build_doctor_report()` gains
one new line.**

> Confirmed by Aryan: all five Done-when items. Done-when #1–#4 via
> `backend\tests\manual_b27_doctor_resolved.py`, driven through one
> long-lived `Runtime` (the same shape B26's own verification used), run
> against **three separate config.toml starting states** — absent,
> empty-but-present, and present-with-unrelated-keys — via
> `backend\tests\manual_b27_verify.ps1`. All three cases: `ALL CHECKS
> PASSED`, exit code 0. Done-when #5 (byte-identical `cli.py doctor`
> stdout) diffed by Aryan with `Compare-Object` against his own
> pre-episode capture — empty diff, not eyeballed. Done-when #6 confirmed
> by `git diff --stat`: exactly one new line in `cli.py`, a pure addition
> in `appconfig_overrides.py`. A real bug was found and fixed during this
> episode's own verification tooling — not in `cli.py` or
> `appconfig_overrides.py` — see "Bugs caught," below.

---

## `backend\appconfig_overrides.py`

Two additions, placed between `all_overrides()` and `config_value_live()`
exactly as the task doc specified — nothing existing in this file was
touched (`git diff` confirms a pure addition, no line moved or changed).

- `RESOLVED_DEFAULTS` — the five Behaviour-tab keys' own hardcoded
  fallbacks, duplicated here by deliberate design (not imported from each
  real call site, for the reasons the task doc's own comment gives).
  Verified against each call site's own literal before writing it, per
  Aryan's instruction, not assumed from the task doc's claim:

  | Key | Fallback | Verified at |
  |---|---|---|
  | `chat.queue_depth` | `1` | `backend\runtime\scheduler.py:269`, inside `turn_queue_depth()` |
  | `limits.max_concurrent_tasks` | `3` | `backend\runtime\scheduler.py:68` `DEFAULT_MAX_CONCURRENT`, used at line 88 |
  | `project.allow_outside_project` | `False` | `backend\managers\context.py:843` and `backend\runtime\runtime.py:1393` |
  | `index.respect_gitignore` | `True` | `backend\managers\project_manager.py:205` |
  | `research.stale_after_days` | `7` | `backend\runtime\runtime.py:1025` |

  All five matched the task doc's claimed map exactly — no stop-and-tell
  was needed.

- `resolved_behaviour_settings()` — for each of the five keys, returns
  `{"resolved": appconfig.config_value(key, default), "overridden":
  present}`, where `present` comes from `get_override(key)`. Deliberately
  calls `appconfig.config_value()` (reads `config.toml`/fallback only),
  never `config_value_live()` (which would apply the override and defeat
  the entire point — Done-when #4 is exactly the check that this
  distinction holds, and it does: see the Manual Test Log below, where
  `resolved` stays `14` while `behaviour_overrides` separately shows `3`).

## `backend\cli.py`

One new line inside `build_doctor_report()`'s returned dict, immediately
after the existing `"behaviour_overrides"` line (now at `cli.py:1274` —
the task doc's cited line numbers, 1202/1276/1280, predate B29, which
removed lines from the same report; everything was located by name, not
by the stale numbers):

```python
"behaviour_overrides": appconfig_overrides.all_overrides(),
"behaviour_resolved": appconfig_overrides.resolved_behaviour_settings(),
```

`_print_doctor_report` is untouched — it did not print `behaviour_overrides`
before this episode and does not print `behaviour_resolved` either, so
Done-when #5 (byte-identical `cli.py doctor` stdout) holds by construction.
`GetDoctorReport`'s handler (`_handle_get_doctor_report`, `runtime.py:617`)
needed no change — it already emits `build_doctor_report()`'s dict
verbatim.

## `backend\tests\manual_b27_doctor_resolved.py` (NEW)

One long-lived `Runtime` (`build_runtime(single_instance=False,
say_hello=False, heartbeat=False)`), driven via `runtime.receive()`,
draining the outbox between steps — the same shape
`manual_b26_behaviour_overrides.py` already established, reused here
because Done-when #4 needs an override set in the same process that
`GetDoctorReport` is then asked about. Three sections:

1. **Done-when #1/#2**: fresh profile, no config.toml value for any of
   the five keys — `behaviour_resolved` has exactly the five expected
   keys, each `resolved` matching `RESOLVED_DEFAULTS`, each `overridden`
   is `False`.
2. **Done-when #3**: writes a REAL `config.toml` value
   (`research.stale_after_days = 14`) directly via
   `appconfig.config_path()` (re-read fresh on every `GetDoctorReport`
   call — `appconfig.py` has no cache, confirmed before relying on it) —
   `resolved` becomes `14`, `overridden` stays `False`.
3. **Done-when #4**: `SetBehaviourOverride(research.stale_after_days=3)`
   in the same process — `resolved` stays `14` (config.toml's value,
   unchanged from step 2), `overridden` becomes `True`, and
   `behaviour_overrides["research.stale_after_days"]` separately shows
   `3` — the two fields deliberately disagree, proving
   `behaviour_resolved` is not `config_value_live()` wearing a new name.

## `backend\tests\manual_b27_verify.ps1` (NEW)

Scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule (both
env vars set for the script's duration only, never touching the real
`%APPDATA%\Point`/`%LOCALAPPDATA%\Point`). Runs
`manual_b27_doctor_resolved.py` three times, once per config.toml starting
state named in the task doc's Done-when #2 ("the most realistic case" —
present with other keys, none of the five — included alongside absent and
empty-but-present), then captures `cli.py doctor`'s stdout to a file for
Aryan's own byte-diff.

---

## Manual Test Log (Episode B27)

All three config.toml cases, same scratch profile pattern, run via
`manual_b27_verify.ps1`:

```
=== config.toml case: absent ===
=== #1/#2  fresh profile: behaviour_resolved == hardcoded fallbacks, nothing overridden
  [PASS] GetDoctorReport returned a DoctorReport
  [PASS] behaviour_resolved has exactly the five expected keys
  [PASS] chat.queue_depth: resolved == 1 (hardcoded fallback)            [PASS] overridden is False
  [PASS] limits.max_concurrent_tasks: resolved == 3 (hardcoded fallback) [PASS] overridden is False
  [PASS] project.allow_outside_project: resolved == False (hardcoded)   [PASS] overridden is False
  [PASS] index.respect_gitignore: resolved == True (hardcoded fallback) [PASS] overridden is False
  [PASS] research.stale_after_days: resolved == 7 (hardcoded fallback)  [PASS] overridden is False

=== #3  a real config.toml value is reflected, with no override active
  [PASS] research.stale_after_days: resolved == 14 (config.toml's value, not the fallback)
  [PASS] research.stale_after_days: overridden is still False

=== #4  an active override masks the key, but resolved stays config.toml's value
  [PASS] research.stale_after_days: resolved STILL == 14 (config.toml's value, unchanged from #3)
  [PASS] research.stale_after_days: overridden is now True
  [PASS] behaviour_overrides (unchanged by this episode) separately shows the override's own value, 3
  [PASS] behaviour_resolved['resolved'] (14) and behaviour_overrides' value (3) disagree on purpose --
         proves behaviour_resolved is not config_value_live() wearing a new name

ALL CHECKS PASSED

=== config.toml case: empty-but-present ===
[identical pass sequence to "absent" -- all checks PASS, ALL CHECKS PASSED]

=== config.toml case: present-with-other-keys ===
[identical pass sequence -- all checks PASS, ALL CHECKS PASSED]
  (config content: "[ui]\ntheme = \"dark\"\n" -- a real top-level table
   the Go frontend owns, which the Python backend never reads -- see
   "Bugs caught" for why this case's content matters)
```

Exit code 0, all three cases, full run. The raw `GetDoctorReport` payloads
captured during this run additionally confirm `database_path` stayed
pointed at the scratch profile (`...\point-scratch-b27\Point\runtime.db`)
in every case — relevant only because of the bug below, not a Done-when
item itself.

**Done-when #5** (byte-identical `cli.py doctor` stdout): Aryan captured
`cli.py doctor`'s output from unmodified code himself, pre-episode. This
episode's own script captures the post-episode output to
`$env:TEMP\point-b27-doctor-after.txt` on the same scratch-profile
pattern. Aryan diffed the two with `Compare-Object` — empty diff,
byte-identical, confirming by actual comparison rather than by the
construction argument alone (which also holds, since `_print_doctor_report`
was never touched).

**Done-when #6**: `git diff --stat` shows exactly one new line in
`cli.py` and a pure addition (51 new lines, zero removed/changed) in
`appconfig_overrides.py`. No other field in `build_doctor_report()`'s dict
moved or changed.

---

## Bugs caught during manual testing

One real one — in this episode's own verification tooling, never in
`cli.py` or `appconfig_overrides.py`.

### The first version of the "other keys" config.toml fixture accidentally set a real, interpreted key

`manual_b27_verify.ps1`'s third case ("present-with-other-keys") needs
config.toml content that is realistic filler — present, non-empty, but
touching none of the five Behaviour keys. The first version used:

```toml
[database]
path = "runtime.db"
```

Running it: `cli.py migrate` (which runs *before* this config.toml is
written, each case) printed the correct scratch absolute path. But
`GetDoctorReport`'s payload from the same case, moments later, showed
`database_path` as the bare string `"runtime.db"` — not the scratch path,
unlike the other two cases in the same run. This produced six real files
on disk at `backend\runtime.db` and `backend\runtime.db.bak-2` through
`-6` (0 sessions in each, confirmed by direct `sqlite3` query — ruled out
as anything to do with B29's own decoy test, which used a marker row).
Deleted; never committed.

Traced, not assumed: `database.path` is not inert filler — it is a real
key `resolve_db_path()` (`backend\bootstrap.py:47-57`) reads:

```python
def resolve_db_path() -> Path:
    """POINT_DB -> config `database.path` -> %LOCALAPPDATA%\\Point\\runtime.db."""
    override = os.environ.get("POINT_DB")
    if override:
        path = Path(override).expanduser()
    else:
        configured = appconfig.config_value("database.path")
        path = Path(configured).expanduser() if configured else appconfig.default_db_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    return path
```

No fallback resolves to `backend\runtime.db` by construction —
`appconfig.default_db_path()` (`backend\appconfig.py:86-87`) is
`state_dir() / "runtime.db"`, i.e. `%LOCALAPPDATA%\Point\runtime.db`. The
`backend\runtime.db` files came from `Path("runtime.db").expanduser()` — a
*relative* path — resolving against the calling process's own cwd, which
is `backend\` (the PowerShell script `cd`s there). `build_doctor_report()`
(`cli.py:1213`) calls `resolve_db_path()` fresh on every `GetDoctorReport`,
and `appconfig.config_value()` has no cache, so once the case's
config.toml existed (written *after* `migrate`, *before*
`manual_b27_doctor_resolved.py` ran), every `GetDoctorReport` in that
process re-read it and got rerouted. The repeated `.bak-2` through `.bak-6`
files are `migrations.py`'s ordinary backup-before-migrate behavior firing
against the same accidental `backend\runtime.db` across repeated debugging
runs — not a new mechanism.

**Verdict: test-script bug only, not a production fallback.**
`resolve_db_path()` did exactly what its own docstring says. Nothing
silently resolves to a path inside the repo; a relative `database.path`
resolving against cwd is standard, documented `pathlib` behavior, not a
hidden fallback — it only surfaced here because this episode's own test
fixture picked, by accident, the one real key that redirects the database
itself, with a relative value, while running from `backend\` (the
directory CLAUDE.md's own "How to run each side" section says to run
from). Fixed by changing case 3's fixture to `[ui]\ntheme = "dark"\n` — a
real top-level table the Go frontend owns (CLAUDE.md: "Go's
`config.Load()` parses only the `[ui]` table") that the Python backend
never reads (grep-confirmed: no `appconfig.config_value("ui...` call site
anywhere in `backend\`), so it is genuinely inert for this purpose. Re-run
after the fix, standalone and then as part of the full three-case run
above: `database_path` in every `GetDoctorReport` payload for case 3 now
matches the scratch path exactly like cases 1 and 2. Confirmed no stray
`backend\runtime.db*` files remained after either run.

**The generalizable lesson:** a "give me some realistic unrelated config
content" fixture is not actually unrelated until it's checked against
every real call site that reads `config.toml`, not just the five keys the
episode under test cares about — `database.path` is read by
`resolve_db_path()`, completely orthogonal to the Behaviour tab, but still
a real, interpreted key that can silently redirect where the whole
process's database lives if the filler picks it by coincidence.

---

## What the next episode inherits

- **The frontend pass this unblocks remains unscoped**, as the task doc
  specified. F10b's Behaviour tab rows currently read
  `default`/`overridden → X`; once this episode shipped,
  `behaviour_resolved` is available on any `GetDoctorReport` the Settings
  popup already knows how to send, so a later Go-side pass can show the
  real number for the `default` case too (e.g. `default (3)`). That pass
  still has to decide WHEN to fetch it and how to keep it from going
  stale while the popup is open — this episode deliberately leaves that
  open rather than guessing, per its own task doc.
- **`RESOLVED_DEFAULTS` is a second copy of five literals that already
  exist elsewhere**, verified matching at write time (see the table
  above) but not wired to fail loudly if a real call site's fallback ever
  changes without this map changing too. Whoever next changes
  `chat.queue_depth`'s, `limits.max_concurrent_tasks`'s,
  `project.allow_outside_project`'s, `index.respect_gitignore`'s, or
  `research.stale_after_days`'s own hardcoded fallback at its real call
  site must update this map in the same change.
- **A test fixture standing in for "unrelated config content" needs to be
  checked against every real `config.toml` reader, not just the episode's
  own five keys** — see "Bugs caught" above. `manual_b27_verify.ps1`'s
  case 3 now uses `[ui]` for this reason; any future test needing similar
  filler should default to the same table rather than reinventing one.
- **Demo mode (F10c) and the Keys tab remain unaffected and unscoped**,
  same as every prior F10b-adjacent note.
