# Episode B29 — Remove the Legacy `backend\runtime.db` Copy-Forward

**`backend\runtime.db` is a pre-B6 leftover, kept on disk since B6 moved the
database out of the source tree, explicitly as a rollback safety net
pending a human's decision to delete it ("removing it is Aryan's call once
he is satisfied the move worked" — `bootstrap.py`'s own comment, decisions.md
D96). That decision can now be made, with evidence, not assumption: the
move has been proven for the entire visible history of this repository.
But the real fix this episode makes is not "delete an inert file" — it is
removing `resolve_db_path()`'s silent, always-on-by-default behavior of
copying that file into ANY fresh profile, test or production, with zero
signal that it happened. Found live, during F10c's own verification, when
a brand-new scratch profile's "fresh" database turned out to already
contain 12 real-looking sessions.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B29-remove-legacy-runtime-db.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. Deleting a file, deleting dead code behind it, and
updating two docs to match. No new logic anywhere.

**Depends on:** B6 (shipped, long since) — nothing else.

**Found during:** F10c (Settings — Demo Mode), while verifying Done-when
#3 against a scratch profile. Spun out as its own episode rather than
folded into F10c, matching how B23–B28 were each split out from whatever
surfaced them.

---

## Why this episode exists

### The original reason the file is still here, found by reading, not guessing

`backend\bootstrap.py:37-40`'s own comment, word for word:

```
# Where the database lived before B6: inside the source tree, where it
# would be committed by accident and would break the moment the app was
# packaged. Copied out once, never deleted -- removing it is Aryan's call
# once he is satisfied the move worked.
LEGACY_DB_PATH = Path(__file__).resolve().parent / "runtime.db"
```

`design/decisions.md` D96 says the same thing from the decision side: "The
old file is **copied, not moved** — removing it is a human's call once the
move is proven." Both are explicit: this was never meant to be permanent.
It is a safety net for a transition, with the removal condition named
up front — "once the move is proven" — not left for someone to guess at
later.

**There is no git history to check for B6 itself** — `f0f9da1`, this
repo's oldest commit, is titled "B7: project system and B1-B6 already done.
No git history for anything before B7 because of some branch problem." So
the question the instruction asked — check git blame/commit messages
around B6's migration — has no earlier commits to inspect; D96 and the
code comment above are the only record, and both already give the actual
stated reason directly, with no need to infer one.

### The move has been proven — with evidence, not assumption

- `f0f9da1` ("B1-B6 already done") is the **first** commit in this
  repository's entire history. Every one of the other 40 commits —
  every backend episode from B7 through B28, every frontend episode from
  F1 through F10c, every manual verification script in
  `design/completed/**` — ran **after** B6 was already finished.
- The real database this repo has been using the whole time,
  `%LOCALAPPDATA%\Point\runtime.db`, has **20+ real sessions** spanning
  providers used across that entire span (`ollama`, `mock`, `claude`) —
  confirmed by direct inspection. It has been this project's one working
  database for the entire development history visible in this repo, not
  a tentative, just-moved, unproven location.
- `backend\runtime.db` itself is untouched since **2026-08-10** (`ls -la`
  confirms its mtime) — roughly two months of continuous, active
  development on every side of this app, during which nothing has read
  from or written to it directly. Its only remaining function is the one
  this episode removes: silently feeding `resolve_db_path()`'s copy.
- It is `.gitignore`d (`backend/runtime.db`, `.gitignore:18`) and was
  never a tracked, shared artifact — just a local leftover file.
- It has exactly two readers in the whole codebase: the copy-forward
  mechanism itself (`bootstrap.py`), and one `doctor`-report field that
  only ever states whether the file still exists
  (`cli.py:1261`/`1296-1297`, `frontend\internal\app\doctor.go:109-111`).

**The transition period D96 and the bootstrap.py comment both name as the
condition for removal is over, by every piece of evidence above, not by
assumption.**

### The real footgun is the silent copy, not the file's existence

Confirmed directly: every single call site in this codebase —
`bootstrap.py`'s own `build_runtime`, `cli.py`'s `doctor` and `migrate`
commands, and every one of `manual_b12_concurrency.py`,
`manual_b19_silence_timeout.py`, `manual_b26_behaviour_overrides.py`, and
`manual_b28_demo_mode.py` — calls `resolve_db_path()` with **no
arguments**, meaning `migrate_legacy`'s `True` default has never once been
overridden, by any caller, in this entire codebase (checked by grep, not
sampled). Not one of the four manual test scripts written for B12, B19,
B26, or B28 — each one explicitly built around a scratch `APPDATA`/
`LOCALAPPDATA` profile per CLAUDE.md's own hard rule — opted out of the
legacy copy. **The opt-out has a 0% adoption rate across every caller this
codebase has ever written, including four scripts whose entire purpose was
exercising an isolated, "fresh" profile.** That is not a close call between
three options; it is the rename-aside lesson CLAUDE.md already encodes,
recurring in a new shape: a pattern that depends on every future caller
remembering to do something extra is not a fix, because the evidence here
is that nobody has, not once, across every real opportunity to do so so
far.

Flagging this live would have cost nothing if the copied data were
distinguishable from a real fresh profile. It is not: a copied session row
looks exactly like a real one, with no marker saying "this arrived via the
legacy copy, not from something that happened on this profile." A
verification script that counts sessions, lists providers, or asserts "a
brand-new profile starts empty" (several already do, implicitly, in this
project's own Done-when lists) can be silently wrong on any machine where
`backend\runtime.db` happens to still exist — which, until this episode,
is every development machine that has ever run this backend before B6's
move, forever, with no way to tell from the symptom alone.

---

## Build

### 1. Delete `backend\runtime.db`

The file itself. No sidecars exist today (`-wal`/`-shm` checked, absent) —
`ls -la backend\runtime.db*` to confirm again at build time, in case the
backend is ever accidentally pointed at it between now and then.

### 2. `backend\bootstrap.py` — remove the copy-forward mechanism entirely, not just its trigger

Remove `LEGACY_DB_PATH`, `_copy_legacy_database()`, and the
`migrate_legacy` parameter from `resolve_db_path()` — not merely flip its
default. A default that still exists is still a lever some future caller
could flip back on by accident or imitation; removing the parameter and
the function it guards means there is nothing left to silently resolve to
a stale file, now or if a `backend\runtime.db` ever reappears on someone's
machine by accident (a restored backup, an old binary, a copy-paste from
another checkout).

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

`shutil` becomes an unused import once `_copy_legacy_database` is gone —
remove it too; confirm nothing else in the file still needs it (checked:
nothing else does).

### 3. `backend\cli.py` — remove `legacy_db_path` from the doctor report

- Remove the `LEGACY_DB_PATH` import (`cli.py:62`).
- Remove the `"legacy_db_path": ...` line from `build_doctor_report()`'s
  returned dict (`cli.py:1261`).
- Remove the `if report["legacy_db_path"]:` block from
  `_print_doctor_report` (`cli.py:1296-1297`).

### 4. `frontend\internal\app\doctor.go` — remove the matching render line

Remove the `if legacy := str("legacy_db_path"); ...` block
(`doctor.go:109-111`). `frontend\internal\app\doctor_test.go`'s
`sampleDoctorReport()` fixture drops its own `"legacy_db_path": nil` entry
(`doctor_test.go:26`) — the formatter must not require a key that no
longer exists in the real payload.

### 5. `.gitignore` — drop the now-meaningless entry

Remove `backend/runtime.db` from `.gitignore` (currently line 18). Nothing
ever writes there again once step 2 lands; an ignore rule for a path
nothing produces is dead weight, the same reasoning CLAUDE.md's repo-layout
section already applies to deleted managers.

### 6. `design/decisions.md` — close D96's own open question

D96 currently ends mid-decision: "The old file is **copied, not moved** —
removing it is a human's call once the move is proven." Append a short,
dated note recording that the call was made, by whom, and the evidence —
matching how other decisions in this file get amended rather than
rewritten when a later episode acts on them.

### 7. `CLAUDE.md` — the note the original instruction asked for

The "Actual repo layout" section's aside (currently lines ~204-208):

```
│   │                           (no database here — B6 moved it to
│   │                           %LOCALAPPDATA%\Point\runtime.db. Override with
│   │                           POINT_DB, then config `database.path`. The old
│   │                           backend\runtime.db was COPIED, not moved, and
│   │                           is deliberately still on disk.)
```

becomes (fact-checked against the tree, the same discipline this section's
own header already demands):

```
│   │                           (no database here — B6 moved it to
│   │                           %LOCALAPPDATA%\Point\runtime.db. Override with
│   │                           POINT_DB, then config `database.path`. The
│   │                           pre-B6 backend\runtime.db this used to be
│   │                           copied from is GONE — B29 removed it, and the
│   │                           silent copy-into-any-fresh-profile mechanism
│   │                           with it, once it was confirmed every scratch-
│   │                           profile script in this repo had been getting
│   │                           a non-empty "fresh" database with no signal
│   │                           that it happened. Nothing resolves a database
│   │                           path from anywhere but these two env vars now.)
```

This is also the note CLAUDE.md's hard-rules section on verification
scripts should cross-reference once, briefly — not a new rule (there is
nothing left to remember after this episode), just enough of a trail that
a reader who finds an OLD completed doc's scratch script mentioning
`backend\runtime.db` understands why it no longer matters.

---

## Do NOT

- **Do not keep `migrate_legacy` as a parameter defaulting to `False`.**
  A lever that defaults off is still a lever — the point is that nothing
  should exist for a future caller to flip, by accident or by copying an
  older call site as a template. Remove the parameter, not just its
  default.
- **Do not keep `LEGACY_DB_PATH` "just in case" as a constant with no
  reader.** An unused path constant is exactly the kind of leftover this
  project's own repo-layout section already calls out and corrects when
  found (`managers\provider.py`, `managers\tool.py` — both deleted
  outright, not kept inert).
- **Do not add a migration-aid CLI command** ("one-time import your old
  database") as a replacement for the automatic copy. Nobody has asked for
  one, the automatic version's entire problem was running with no signal
  and no opt-in, and a new, deliberately invoked command is a different
  feature this episode was not asked to design. If a real user ever needs
  this, it is a request to answer then, not to pre-build now.
- **Do not touch any other field in `build_doctor_report()`'s dict**, or
  `_print_doctor_report`'s formatting of anything besides the one removed
  line. `cli.py doctor`'s output must otherwise be byte-identical before
  and after, same discipline B26/B27 already held themselves to.
- **Do not touch F10c or anything else discovered alongside it.** This
  episode is scoped to exactly the file, the mechanism, and the two docs
  above.

---

## Done when

1. `backend\runtime.db` no longer exists on disk.
2. `resolve_db_path()` takes no `migrate_legacy` argument, has no
   `LEGACY_DB_PATH` reference anywhere in `bootstrap.py`, and `shutil` is
   no longer imported there (confirm by grep, not by reading the diff
   alone — an unused import left behind is the kind of thing a reviewer
   skims past).
3. A **brand-new** scratch `APPDATA`/`LOCALAPPDATA` profile — one that has
   genuinely never existed before, not a reused directory name — produces
   a database with **zero** sessions after `cli.py migrate`, regardless of
   whether `backend\runtime.db` is present or absent on the machine running
   it (confirm by actually leaving a dummy `backend\runtime.db` in place
   during this check and showing the new database stays empty anyway —
   proving the mechanism is gone, not just that today's missing file
   happens to make the old guard false).
4. `cli.py doctor`'s printed output is byte-identical before and after,
   apart from the `legacy_db_path`-related line disappearing entirely (not
   printing `None` or an empty value — the line itself is gone).
5. `GetDoctorReport`'s payload no longer has a `legacy_db_path` key at all.
6. `go test ./frontend/internal/app/...` passes with `doctor_test.go`'s
   fixture no longer carrying `legacy_db_path`.
7. `design/decisions.md` D96 and `CLAUDE.md`'s repo-layout aside both
   reflect the file's removal — reading either one alone gives a correct,
   current account, not a stale one contradicted by `git status`.
8. `git status` after this episode shows the deletion of
   `backend/runtime.db` itself, plus edits to exactly:
   `backend\bootstrap.py`, `backend\cli.py`,
   `frontend\internal\app\doctor.go`,
   `frontend\internal\app\doctor_test.go`, `.gitignore`,
   `design\decisions.md`, `CLAUDE.md` — nothing else.

---

## Verification gate (PowerShell)

Per CLAUDE.md's hard rule: one session, env vars set explicitly at the top
of every single command block (not assumed to persist from an earlier
one — the exact mistake this episode's own discovery process made once,
corrected immediately once noticed), the real profile never named in any
destructive call.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b29"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData      = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    Write-Output "APPDATA:      $env:APPDATA"
    Write-Output "LOCALAPPDATA: $env:LOCALAPPDATA"

    # --- 1: the file is gone.
    Test-Path "D:\Projects\Point\backend\runtime.db"
    # EXPECT: False.

    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # --- 3 (the stronger form): put a DUMMY file back at the legacy path,
    # confirm a fresh profile still starts empty -- proving the MECHANISM
    # is gone, not merely that the usual file is currently missing.
    Copy-Item "$scratch\Point\runtime.db" "D:\Projects\Point\backend\runtime.db"
    $scratch2 = Join-Path $env:TEMP "point-scratch-b29-decoy"
    New-Item -ItemType Directory -Force $scratch2 | Out-Null
    $env:APPDATA      = $scratch2
    $env:LOCALAPPDATA = $scratch2
    Write-Output "APPDATA (decoy run):      $env:APPDATA"
    Write-Output "LOCALAPPDATA (decoy run): $env:LOCALAPPDATA"
    uv run python cli.py migrate
    sqlite3 "$scratch2\Point\runtime.db" "select count(*) from sessions;"
    # EXPECT: 0 -- even with a non-empty file sitting at the exact legacy
    # path, a fresh profile must not inherit anything from it.
    Remove-Item "D:\Projects\Point\backend\runtime.db" -Force
    Remove-Item -LiteralPath $scratch2 -Recurse -Force -ErrorAction SilentlyContinue

    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch

    # --- 4-5: doctor report has no legacy_db_path, printed output has no
    # legacy line.
    uv run python cli.py doctor | Out-File -Encoding utf8 "$scratch\doctor-after.txt"
    Select-String -Path "$scratch\doctor-after.txt" -Pattern "legacy"
    # EXPECT: no match.

    # --- 6: Go side.
    cd D:\Projects\Point
    go test .\frontend\internal\app\... -count=1 -race
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

Do not proceed until every check above passes.

---

## Handoff

- **Every scratch-profile verification script written before this
  episode implicitly assumed a fresh profile starts with zero sessions.**
  None of them were actually wrong about the OUTCOME on a machine without
  a `backend\runtime.db` — but on one that had it, they were silently
  trusting an assumption this episode is what finally makes true
  unconditionally. No existing completed doc needs correcting
  retroactively; none of their actual Done-when evidence depended on
  session *count* being zero in a way the legacy copy would have
  contradicted, as far as this episode checked. Flagged here only so a
  future reader who notices old scratch output mentioning unexpected
  sessions knows this is why, and that it is now fixed, not open.
- **`POINT_DB` is still the one and only override**, unchanged. Nothing
  about this episode touches the normal resolution chain a real,
  non-legacy install uses.
- **F10c is unaffected** beyond having discovered this — its own
  verification already re-ran cleanly once env vars were set explicitly
  in every command, independent of whatever this episode does.
