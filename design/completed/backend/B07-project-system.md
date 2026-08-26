# Episode 7 — Project System ✅

**Projects have a durable identity that survives a rename or a move, every
filesystem path the backend will ever touch goes through exactly one guard,
and soft file locks plus a stale-save check exist and are testable from the
CLI.**

> Verified by Aryan on 2026-08-26 against `tasks/backend/B07-project-system.md`'s
> six "Done when" criteria, plus two ("Done when" #3 and the second half of #4)
> that the task doc's own PowerShell script never actually exercises. Built and
> self-tested first, then **independently re-run end to end by Aryan himself in
> a separate terminal** and confirmed passing — the same closure bar as B6.

---

## Resolved before building: `PointError` does not exist yet

The task doc writes the path guard as
`resolve(path, project) -> Path  # raises PointError(path_denied)`. `PointError`
(`code` / `message` / `retryable` / `detail`) is `B17` — Phase P2, **depends
on: all** — and does not exist in this repo. Building it now would be exactly
the kind of adjacent-but-out-of-scope work CLAUDE.md says not to do.

**Confirmed with Aryan before writing code:** `PathDeniedError` is a plain
`RuntimeException` subclass in `runtime/errors.py`, matching every other error
in this codebase today — a message, nothing structured. **This is a
stopgap.** B17 folds `PathDeniedError`, along with every other
`RuntimeException` subclass in that file, into `PointError(code="path_denied")`.
Do not build `PointError` piecemeal before then.

## `managers/project.py` (NEW)

`Project` — `id`, `name`, `root_path` ("last known"), `created_at`,
`last_opened_at`. `id` is the only field anything else may remember;
`root_path` is overwritten on every successful resolve. `MARKER_DIR = ".point"`,
`PROJECT_FILE = "project.json"`.

## `managers/pathguard.py` (NEW) — the one function, used by everything

`resolve_path()`. Full contract below, in its own section — this is the part
B10 and F8 need without re-reading this file.

## `managers/gitignore.py` (NEW)

A **deliberately non-spec-complete** `.gitignore` matcher: comments, blank
lines, `!` negation, a leading `/` to anchor a pattern to the project root, a
trailing `/` for directory-only, `*`/`?`/`**` globs. No nested `.gitignore`
files. Covers what `index.respect_gitignore` needs today; if a real project
hits a gitignore feature this does not cover, the fix is the `pathspec`
package (a new dependency worth naming when it happens), not growing this
file into a second gitignore parser.

## `managers/project_manager.py` (NEW)

`ProjectManager` — business rules, no raw SQL (that lives in
`ProjectRepository`).

- **`init(path)`** — writes `.point\project.json`
  (`{project_id, name, created_at}`), upserts a `projects` row, and appends
  `.point/` to the project's `.gitignore` unless
  `git.share_project_memory = true`. Idempotent: if the marker already exists,
  `init` behaves like `resolve()` plus a touch rather than overwriting it.
- **`resolve(path, explicit_project_id)`** — explicit id → walk up from `path`
  (default cwd) looking for `.point\` → `None`. **Matches on id, not path**:
  when the marker's `project_id` has no matching DB row (a moved project
  resolved for the first time since a DB reset, or a fresh copy of the repo),
  the row is recreated from the id *in the marker*, never reassigned. Every
  successful resolve overwrites `root_path` and bumps `last_opened_at` — this
  is the entire mechanism by which a rename or a move survives.
- **File index** — `list_files(project, prefix, limit, refresh)`. Built lazily
  on first use with `asyncio.to_thread` (a large tree should not block the
  event loop), cached per `project.id` for `_INDEX_TTL_S = 30s`, or rebuilt on
  `refresh=True`. No filesystem watcher. `.venv`, `node_modules`,
  `__pycache__`, `.git` are skipped **unconditionally**, before the gitignore
  check ever runs — turning `index.respect_gitignore` off must not resurrect
  them. Binaries (a null byte in the first 8KB) and files over
  `index.max_file_kb` (default 100) are skipped regardless of gitignore.
- **`read_file`** — guarded through `resolve_path`, size-capped at the same
  `index.max_file_kb`, returns `{path, content, mtime, size, outside_project}`.
- **`check_stale(project, path, recorded_mtime)`** — `True` if the file's
  current mtime differs from what was recorded at open (tolerance `1e-6`), **or
  the file no longer exists**. This is the check a save-refusal is built on;
  it does not itself refuse anything.
- **Soft locks** — `acquire_lock` / `heartbeat_lock` / `release_lock` /
  `take_over_lock`. Full contract below.

## `database/project_repository.py` (NEW)

Raw SQL only, same shape as `session_repository.py` / `task_repository.py`.
`upsert()` is `ON CONFLICT(id) DO UPDATE` (matches the "match on id" resolve
behaviour above); lock rows are a single `put_lock()` upsert used for a fresh
acquire, a stale takeover, and an explicit `take_over` alike — the caller has
already decided the write is allowed by the time it reaches the repository.

## `runtime/errors.py`

`PathDeniedError`, `ProjectNotFoundError`, `LockNotHeldError` — all plain
`RuntimeException` subclasses (see the `PointError` note above). `Runtime.receive()`
already caught `RuntimeException` generically and turns it into
`{"error": str(exc)}`; none of the three needed a special case there.

## `runtime/events.py`, `runtime/runtime.py`, `cli.py` — wired like everything else

New `IncomingEventType` / `OutgoingEventType` members
(`ProjectInit`/`ProjectResolve`/`ListProjectFiles`/`ReadProjectFile`/
`AcquireFileLock`/`HeartbeatFileLock`/`ReleaseFileLock`/`TakeOverFileLock`/
`CheckFileStale`, and `Project`/`ProjectFileList`/`ProjectFile`/
`FileLockResult`/`FileLockReleased`/`FileStaleResult`). Six new handlers on
`Runtime`, following the existing dispatcher pattern exactly — `project=None`
is a legitimate answer everywhere a project is merely *optional* (`read_file`,
`check_stale`), and `_require_project()` is the one place that turns "no
project resolved" into `ProjectNotFoundError` for the operations that
genuinely cannot proceed without one (the file index, locks).

`cli.py project init|resolve|list-files|read-file|lock
acquire|heartbeat|release|take-over|check-stale`, each taking `--path`
(default cwd, re-resolved fresh on every invocation — there is no "current
project" remembered between CLI processes) and routing through the same
`call()` helper as every other command. `project lock <verb>` is a third
level of subcommand, one deeper than the `(command, X_command)` shape every
other tree uses, so `main()` special-cases it rather than forcing the generic
lookup to grow a third tuple slot for one command.

## `appconfig.py` — a bug found and fixed while testing this episode

Not part of B7's Build section, but fixed inline at Aryan's direction rather
than parked, because it made B7's own config-driven toggles
(`index.respect_gitignore`, `project.allow_outside_project`) untestable. Full
writeup in "Bugs caught during manual testing" below.

---

## Contract: `resolve_path()` — read this instead of re-reading the episode

`managers/pathguard.py`:

```python
resolve_path(raw_path: str, project: Optional[Project], *,
             allow_outside_project: bool = False) -> Path
```

This is **the** function. `@file` attachments, tool file access (B10), the
editor (F13), and the SSH jail are all meant to call through this one
implementation — not write a second containment check.

1. `raw_path` has `~` and environment variables expanded first
   (`os.path.expandvars(os.path.expanduser(...))`).
2. **`project is None`:**
   - a relative `raw_path` is **always refused** (`PathDeniedError`) —
     regardless of `allow_outside_project`. There is nothing to resolve a
     relative path against when no project is open.
   - an absolute `raw_path` is refused unless `allow_outside_project=True`; if
     allowed, returns `Path(raw_path).resolve(strict=False)` with **no**
     containment check (there is no root to be inside of).
3. **`project` given:**
   - relative → joined onto `Path(project.root_path)` before resolving.
   - absolute → used as given.
   - Either way, `.resolve(strict=False)` runs next — this is what collapses
     `..` against the real filesystem and follows symlinks. `strict=False`
     means the path does **not** need to exist; `resolve_path()` only answers
     "is this location in bounds", never "is this a real file". (`read_file`
     checks `.is_file()` afterward; `check_stale` checks `.exists()`.)
   - Containment: `str(resolved).casefold()` compared against
     `str(Path(project.root_path).resolve(strict=False)).casefold()`, exact
     match or `root + os.sep` prefix. **Case-folded on purpose** — Windows
     paths are case-insensitive, so a raw `startswith` lets a differently-cased
     drive path slide past a guard that only ever saw the canonical casing.
     Both sides are `.resolve()`d *before* the fold; dropping either the
     resolve step or the fold step reopens an escape the other one was
     covering, and this is exactly the kind of thing that regresses quietly
     if the function is ever "simplified" or ported off Windows.
   - Outside the root and **not** `allow_outside_project` → `PathDeniedError`
     naming both the resolved path and the project root — every refusal names
     the root, never a bare "denied", so it can be surfaced verbatim in a UI.
   - Outside the root and `allow_outside_project=True` → returned anyway.
     **`resolve_path()` itself never reports that it's outside** — the
     signature is `Path`, not a tuple, matching the task doc exactly. Call
     `is_outside_project(resolved, project)` separately to learn the fact
     (this is how `read_file`'s `outside_project` field and `check_stale`'s
     equivalent get populated).
4. Never touches the filesystem beyond what `Path.resolve()` itself does to
   follow symlinks — no directory creation, no read, no write.

`is_outside_project(resolved: Path, project: Optional[Project]) -> bool` —
`True` if `project is None`, or if `resolved` fails the same case-folded
containment check above. Purely informational; never raises.

**Today's callers, for reference:** `read_file` and `check_stale` call
`resolve_path()` with whatever `allow_outside_project` the caller passed
through (config-driven, defaults `False`). **Locks never pass
`allow_outside_project=True`** — a lock can never be taken on a path outside
the project, full stop; there is no override for that one.

## Contract: soft file locks — acquire / heartbeat / release / take-over

All four live on `ProjectManager`, backed by one row per
`(project_id, rel_path)` in `file_locks` — no in-memory state, so a lock
survives a backend restart exactly like a task or session row, and a live
backend and a `cli.py` invocation see the same table through WAL.

**Lock identity.** `rel_path` is always derived by calling `resolve_path(raw_path,
project)` (project-scoped, `allow_outside_project` is never true for this call)
and then taking the result relative to the resolved project root, as a
forward-slash POSIX string. Two different spellings of the same file that
resolve to the same `Path` (e.g. `main.py` vs `.\main.py`, or an absolute path
to the same file) hit the same row. **Open question, not exercised by this
episode's tests:** whether two different *casings* of the same existing file
(`Main.PY` vs `main.py`) collide onto the same row depends on whether
`Path.resolve()` corrects to the on-disk canonical casing for an existing
file on this Python/Windows combination — believed to hold in practice, not
verified here. A future caller that cares should verify this directly rather
than assume it.

- **`acquire_lock(project, raw_path, holder) -> LockResult(ok, holder, age_seconds)`**
  - No row → insert, `ok=True`.
  - Row held by the **same** holder → treated as a renewal (heartbeat_at
    bumped to now), `ok=True`. You do not need to release-then-reacquire to
    renew your own lock.
  - Row held by a **different** holder, `now - heartbeat_at <= 60s` →
    `ok=False`, `holder=<other>`, `age_seconds=<elapsed>`.
  - Row held by a **different** holder, `now - heartbeat_at > 60s` (stale) →
    silently overwritten in favor of the caller, `ok=True`. **This is
    indistinguishable from a fresh acquire in the return value** — a caller
    cannot tell "nobody had it" from "the old holder went stale" from
    `LockResult` alone.
- **`heartbeat_lock(project, raw_path, holder) -> LockResult`**
  - No row at all → raises `LockNotHeldError` — a genuine caller bug (you
    cannot heartbeat a lock you never held).
  - Row held by a **different** holder → `LockResult(ok=False, holder=<other>,
    age_seconds=...)`, **not** an exception — you were preempted. Callers must
    check `.ok` on every heartbeat, not just catch exceptions.
  - Row held by the **same** holder → heartbeat_at bumped to now, `ok=True`.
  - Heartbeat **never steals** — there is no staleness check inside it. Only
    `acquire()` and `take_over()` can transfer a lock.
- **`release_lock(project, raw_path, holder) -> bool`**
  - Row held by the **same** holder → deleted, returns `True`.
  - No row, or held by someone else → no-op, returns `False`. Never raises,
    never touches a lock you don't hold.
- **`take_over_lock(project, raw_path, holder) -> LockResult`**
  - Unconditional overwrite regardless of current state (no row / stale /
    actively held) — always `ok=True`. **The only operation that can steal an
    actively-held, non-stale lock.** There is no confirmation inside this
    layer; the caller has already decided that's warranted.
- **Staleness**: `LOCK_STALE_AFTER_S = 60.0`, a literal in
  `managers/project_manager.py`. Not configurable — a future episode wanting
  this tunable needs to add a config key.
- **Nothing calls `heartbeat_lock` on a timer.** The spec's "every 15s while
  the file is open" describes what a *caller* (the eventual editor, a B10
  tool, F13) must do; this episode only exposes the primitive.

---

## Manual Test Log (Episode 7)

Run against isolated scratch profiles (renamed-aside `%APPDATA%\Point` /
`%LOCALAPPDATA%\Point`, restored after), all in single combined PowerShell
scripts after the lesson below was learned the hard way. Then independently
re-run in full by Aryan.

1. **Init** — `project init --path $TEMP\proj` (containing `.gitignore` with
   `secrets/`, `secrets\a.txt`, `main.py`) produced a `project_id` and appended
   `.point/` to the existing `.gitignore` (now `secrets/` + `.point/`).
2. **Gitignore respected** — `list-files` returned exactly `.gitignore` and
   `main.py`; `secrets\a.txt` absent.
3. **Identity survives a rename** — after `Rename-Item proj proj-renamed`,
   `project resolve --path $TEMP\proj-renamed` returned the **same**
   `project_id` as step 1.
4. **Path guard — three refusals**, each naming the project root, none a
   traceback: a relative `..\..\Windows\win.ini`, an absolute
   `C:\Windows\win.ini`, and the same relative escape prefixed with an
   upper-cased drive path (`$TEMP.ToUpper()`) — proving the containment check
   is genuinely case-folded, not a lucky `startswith`.
5. **Locks** — holder `aryan` acquired `main.py`; holder `guest` was told
   `held by aryan (0s)`; `heartbeat_at` was manually backdated 61s during
   self-testing (standing in for a real wait) and `guest` then acquired
   cleanly. Aryan's independent re-run used the real 61-second wait.
6. **Stale-buffer check** — `read-file` reported an mtime; after
   `Add-Content` touched the file, `check-stale` with the old mtime reported
   `changed`; with the file's actual current mtime it reported `unchanged`.
7. **`allow_outside_project = true`** (done-when #3, not in the task doc's own
   script) — with the config flag set and `--allow-outside` passed,
   `read-file` on `C:\Windows\win.ini` succeeded and reported
   `outside_project: True`.
8. **`index.respect_gitignore = false`** (done-when #4's second half, also not
   in the task doc's own script) — with the toggle off, `secrets/a.txt` (and
   `.point/project.json`, itself gitignored) appeared in `list-files`, while
   `.venv`, `node_modules`, and `__pycache__` — populated with one file each
   for this check — **still did not**, proving the always-skip list is
   independent of the gitignore toggle.

---

## Bugs caught during manual testing

### 1. A separate PowerShell tool call does not inherit an earlier call's `$env:` vars — two rows landed in the real `runtime.db`

Each `PowerShell` tool invocation starts a fresh shell; env vars set in one
call are gone by the next. `$env:POINT_DB` was set once, then several
`cli.py project ...` commands were run in *separate* calls expecting it to
still apply — instead every one of them silently fell back to the default
`%LOCALAPPDATA%\Point\runtime.db`, the real production database. A `projects`
row and a `file_locks` row for the scratch test project landed there. Nothing
touched `sessions`, `messages`, or `tasks`; still, deleting the rows required
Aryan's explicit go-ahead (given, then executed and independently verified at
0 rows by Aryan) rather than assuming it was fine to clean up unilaterally.

**Fixed by process, not code**: every later verification ran as **one
combined script** in a single tool call, and `CLAUDE.md`'s Hard Rules now say
so explicitly — manual verification touching `%APPDATA%\Point` or
`%LOCALAPPDATA%\Point` must run as one script against a renamed-aside scratch
profile, never by patching env vars per command.

### 2. `cat > config.toml` overwrote a real, user-owned file without reading it first

To test `index.respect_gitignore` / `project.allow_outside_project`, a `cat >`
was used to write a scratch `config.toml` at `%APPDATA%\Point\config.toml` —
truncating unconditionally, without checking whether the file already existed
or had content. It turned out to be empty/nonexistent, so nothing was lost,
but the process was wrong regardless: `config.toml` is explicitly "user-owned,
NEVER rewritten" (`appconfig.py`'s own module docstring), and the rule to look
before overwriting applies doubly to a file the docs themselves call
never-rewritten. Caught only because Aryan was asked before any further action
was taken on the assumption it was fine.

**Fixed by the same process change as bug 1** — isolation against a
renamed-aside profile means test config never touches the real file at all.

### 3. `appconfig._load_toml()` silently swallowed a UTF-8 BOM as "no config"

Found *because of* bug 2's replacement testing process: writing the scratch
`config.toml` with `Set-Content -Encoding utf8` (Windows PowerShell 5.1, which
always adds a BOM despite the flag's name) produced a file that
`index.respect_gitignore = false` had no visible effect from —
`appconfig.config_value()` was silently returning the default. Root cause:
Python's `tomllib` **rejects a UTF-8 BOM outright**
(`Invalid statement (at line 1, column 1)`) and does not strip one itself, and
`_load_toml()` caught `tomllib.TOMLDecodeError` and returned `{}` with no
logging at all — "no config" and "broken config" were indistinguishable from
the outside.

**Fixed** (in `appconfig.py`, at Aryan's direction — not parked, since it
blocked verifying this episode's own "Done when" items, and scoped to just the
TOML-loading functions):

- `_strip_bom()` strips a leading `b'\xef\xbb\xbf'` before decoding, shared by
  both `_load_toml()` and `config_error()` so the two can never disagree about
  whether a BOM'd-but-otherwise-valid file counts as broken.
- A genuine parse failure is now **logged** (`appconfig` logger, WARNING)
  instead of swallowed — matching every other failure mode in this project,
  which is visible rather than silent. `logging_setup` is imported *inside*
  the failure path, not at module top level, because `logging_setup` itself
  imports `appconfig` — a top-level import the other way would be circular.

Confirmed: a config.toml written with a BOM now parses identically to one
without; a genuinely malformed file still falls back to `{}` but now logs
`Could not parse <path>: <reason>` to stderr/the log file instead of silence.

---

## What the next episode inherits

**Path guard is load-bearing everywhere now.** `managers/pathguard.py`'s
`resolve_path()` is the only sanctioned way to turn a string into a filesystem
path anywhere in this backend. See the contract section above before writing
a second one — **B10's `read_file` tool and F8's `@` picker are both expected
to call through this exact function**, not reimplement containment checking.

**`PathDeniedError` is a stopgap.** B17 folds it, and every other
`RuntimeException` subclass, into `PointError(code=...)`. Do not build
`PointError` piecemeal before B17 — that includes not giving `PathDeniedError`
a `code`/`retryable`/`detail` shape ahead of schedule just because it would be
convenient for one caller.

**Soft locks are a complete, tested primitive with no scheduler around
them.** See the contract section above. Nothing in this codebase calls
`heartbeat_lock` periodically, and nothing surfaces a lock UI — that is F13's
job (or B10's, for a tool that opens a file for writing). The
acquire-return-value ambiguity noted above (fresh acquire vs. stale takeover
both report `ok=True`) is worth resolving before a UI needs to show the
difference to a user.

**Config keys now read and honoured**: `git.share_project_memory` (default
`false`), `index.respect_gitignore` (default `true`), `index.max_file_kb`
(default `100`), `project.allow_outside_project` (default `false`). F10's
settings screen is the first thing expected to actually set these from a UI.

**No filesystem watcher, by design.** The file index is a 30s TTL cache,
rebuilt lazily; nothing pushes invalidation on write. If a future episode
needs live invalidation, that is new scope, not a bug in this one.

**`appconfig.py`'s TOML reader is now BOM-tolerant and non-silent on
failure**, for every caller of `load_config()` / `config_value()`, not just
B7's — this was a pre-existing gap in B6-era infrastructure, fixed here
because B7 was the first thing to actually need a working config toggle.

**Still open, not exercised by this episode's tests**: whether the lock key
(`rel_path`) dedups correctly across two different casings of the same
existing file (see the lock contract section). Verify directly before relying
on it.
