# Episode B29 — Remove the Legacy `backend\runtime.db` Copy-Forward ✅

**`backend\runtime.db` is gone, and so is `resolve_db_path()`'s silent,
always-on-by-default mechanism that copied it into any fresh profile with
zero signal that it happened. Found live during F10c's own verification,
when a brand-new scratch profile's "fresh" database turned out to already
hold 12 real-looking sessions. The move out of the source tree (B6) had
been proven for this repo's entire visible history — its own first commit
is titled "B1-B6 already done" — so the removal condition both
`bootstrap.py`'s own comment and `design/decisions.md`'s D96 named up
front ("removing it is a human's call once the move is proven") was met,
with evidence, not assumption.**

> Confirmed by Aryan: `backend\runtime.db` is gone (`Test-Path` false).
> The decoy test — a dummy file planted at the legacy path, a never-used
> scratch profile migrated against it — produced a database with **0**
> sessions, run both by Claude Code and again independently by Aryan,
> proving the *mechanism* is gone, not merely that today's missing file
> happens to make the old guard false. `cli.py doctor`'s output has no
> "legacy" line, confirmed in both a scratch profile and on the real
> profile. `go test ./frontend/internal/app/...` passes with
> `doctor_test.go`'s fixture no longer carrying `legacy_db_path`. This
> doc was written after the fact — the episode itself was built and
> verified in the same session that found it (spun out of F10c, same as
> B23–B28), committed as `b270433`, with no completed doc written at the
> time; this doc closes that gap, against the real commit's own diff, not
> from memory.

---

## `backend\bootstrap.py`

`resolve_db_path()` lost its `migrate_legacy: bool = True` parameter
entirely — not merely defaulted to `False`. `LEGACY_DB_PATH` (the
module-level `Path(__file__).resolve().parent / "runtime.db"` constant)
and `_copy_legacy_database()` (the function that copied the file plus any
`-wal`/`-shm` sidecars and logged the move) are both deleted outright, and
the now-unused `shutil` import goes with them. `resolve_db_path()` is now
exactly:

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

Built exactly as the task doc specified — removing the parameter, not
just its default, so there is nothing left for a future caller to flip
back on by accident or by copying an older call site as a template.

## `backend\cli.py`

The `LEGACY_DB_PATH` import is gone. `build_doctor_report()`'s returned
dict no longer has a `"legacy_db_path"` key at all (not `None` — the key
itself is absent). `_print_doctor_report`'s `if report["legacy_db_path"]:`
block is removed along with it. Every other field in the doctor report and
every other line of its printed output is untouched — confirmed by the
real diff, which touches exactly these two spots in the file.

## `frontend\internal\app\doctor.go` / `doctor_test.go`

The matching `if legacy := str("legacy_db_path"); legacy != "" { ... }`
render block in `formatDoctorReport` is removed. `doctor_test.go`'s
`sampleDoctorReport()` fixture drops its own `"legacy_db_path": nil`
entry — the formatter no longer expects a key the real payload no longer
sends.

## `.gitignore`

The `backend/runtime.db` entry is removed (it sat just above `*.db-wal`
and `.point/`) — nothing writes there again, so an ignore rule for a path
nothing produces was dead weight.

## `design/decisions.md` — D96

D96 originally ended mid-decision ("removing it is a human's call once
the move is proven"). The real commit tightens that sentence to past
tense ("removing it **was** left as a human's call once the move **was**
proven") and appends a dated note recording that the call was made, the
evidence behind it (the 12-session discovery, the 0%-adoption-rate grep
across every caller including four scratch-profile manual scripts, the
repo's own first-commit history proving the move's lifetime), and that
the mechanism was removed outright rather than merely defaulted off.

## `CLAUDE.md`

The "Actual repo layout" section's aside about `backend\runtime.db` now
reads, verbatim, per the real commit: the pre-B6 file "is GONE — B29
removed it, and the silent copy-into-any-fresh-profile mechanism with it,
once it was confirmed every scratch-profile script in this repo had been
getting a non-empty 'fresh' database with no signal that it happened.
Nothing resolves a database path from anywhere but these two env vars
now."

---

## Manual Test Log (Episode B29)

Confirmed by Aryan, matching the task doc's own verification gate:

1. **The file is gone.** `Test-Path "D:\Projects\Point\backend\runtime.db"`
   → `False`.
2. **The decoy test — the stronger form, proving the mechanism, not just
   the file's absence.** A dummy file planted back at the exact legacy
   path, then a genuinely **never-used** scratch profile (not a reused
   directory name) migrated against it:
   ```
   sqlite3 "<scratch>\Point\runtime.db" "select count(*) from sessions;"
   ```
   → `0`, even with a non-empty file sitting at the legacy path. Run once
   by Claude Code during the original build session, and run again,
   independently, by Aryan — same result both times.
3. **`cli.py doctor`'s output has no "legacy" line** — confirmed by
   searching the printed output for the literal string `"legacy"`, in
   **both** a scratch profile and on Aryan's own real profile. No match in
   either.
4. **`go test ./frontend/internal/app/...` passes** with
   `doctor_test.go`'s fixture no longer carrying `legacy_db_path` —
   confirmed clean.

`git status`/`git diff` for the "nothing else touched" claim: the real
commit (`b270433`) changes exactly `.gitignore`, `CLAUDE.md`,
`backend/bootstrap.py`, `backend/cli.py`,
`frontend/internal/app/doctor.go`, `frontend/internal/app/doctor_test.go`,
`design/decisions.md`, plus the new task doc
`design/tasks/backend/B29-remove-legacy-runtime-db.md` — matching the task
doc's own Done-when #8 exactly, re-confirmed against the actual commit
diff while writing this doc, not assumed from the task doc alone.

---

## Bugs caught during manual testing

None. The one thing worth recording precisely is what the decoy test
actually rules out, since it is easy to understate: a naive "is the file
gone" check would have passed the moment `backend\runtime.db` was deleted,
even if `resolve_db_path()`'s copy-forward logic had been left in place
(defaulted off, or merely guarded by a check that happened to be false
today). The decoy test — deliberately putting the file back and proving a
fresh profile still starts empty — is what actually distinguishes "the
file happens to be missing right now" from "the mechanism that would have
copied it no longer exists," which is the real claim this episode makes.

---

## What the next episode inherits

- **Every scratch-profile verification script written before this episode
  implicitly assumed a fresh profile starts with zero sessions.** None
  were wrong about the *outcome* on a machine that never had
  `backend\runtime.db` — but on one that did, they were silently trusting
  an assumption this episode now makes true unconditionally, for every
  machine, regardless of history. No existing completed doc needs
  retroactive correction — none of their actual Done-when evidence
  depended on session *count* being zero in a way the legacy copy would
  have contradicted. Flagged here only so a future reader who notices old
  scratch output mentioning unexpected sessions on some other machine
  knows this is why, and that it is fixed, not open.
- **`POINT_DB` is still the one and only override.** Nothing about this
  episode touches the normal resolution chain a real, non-legacy install
  uses — `resolve_db_path()`'s three-step chain (`POINT_DB` → config
  `database.path` → `%LOCALAPPDATA%\Point\runtime.db`) is unchanged except
  for the deleted fourth step.
- **F10c is unaffected beyond having discovered this** — its own
  verification already re-ran cleanly once env vars were set explicitly in
  every command, independent of whatever this episode does. See
  `design/completed/frontend/F10c-settings-demo-mode.md`.
- **`sessions.project_id` having no `FOREIGN KEY` constraint** (found
  later, during the B30/F10c recording session, not this one) is an
  unrelated gap in the same schema file this episode touched nothing in —
  see `design/tasks/backend/B31-validate-project-id.md` and the parking-lot
  entry in `design/tasks/backend/INDEX.md`. Noted here only to avoid a
  future reader conflating the two "a stale id slipped through" findings;
  they are independent, found in different sessions, with different fixes.
