r"""ProjectManager: business rules for project identity, the file index,
and soft file locks (B7). Delegates all SQL to ProjectRepository, and
all path safety to managers.pathguard -- it never touches a filesystem
path without going through resolve_path() first.

Resolution order (project init/resolve):
    explicit project_id -> walk up from `path` looking for `.point\` -> None

No `.point\` means no project scope: resolve() returns None rather than
guessing, and callers must treat that as "project features are
unavailable", not as an error.
"""

from __future__ import annotations

import asyncio
import json
import os
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

import aiosqlite

import appconfig
import appconfig_overrides
from database.project_repository import ProjectRepository
from logging_setup import get_logger
from managers import gitignore
from managers.pathguard import is_outside_project, resolve_path
from managers.project import MARKER_DIR, PROJECT_FILE, Project
from runtime.errors import InvalidEventError, LockNotHeldError, ProjectNotFoundError

_log = get_logger("project")

# "heartbeat older than 60s -> free" (B7 spec, verbatim).
LOCK_STALE_AFTER_S = 60.0

# The file index is built lazily and invalidated on this coarse timer or
# an explicit refresh -- there is no filesystem watcher in v1.
_INDEX_TTL_S = 30.0

_ALWAYS_SKIP_DIRS = {".venv", "node_modules", "__pycache__", ".git"}


@dataclass
class LockResult:
    ok: bool
    holder: Optional[str] = None
    age_seconds: Optional[float] = None

    def to_dict(self) -> dict:
        return {"ok": self.ok, "holder": self.holder, "age_seconds": self.age_seconds}


# B32's seven refusal slugs. There is deliberately NO `denied` slug:
# resolve_path already raises PathDeniedError and _require_project raises
# ProjectNotFoundError, both of which Runtime.receive turns into an Error
# envelope with no new code. Two ways to report one condition is worse
# than one.
WRITE_REASONS = frozenset({
    "stale", "exists", "locked", "read_only", "too_large", "no_parent_dir", "io_error",
})


@dataclass
class WriteResult:
    """A refusal is a REPLY, not an exception -- the one real design call
    in B32.

    A path denial or an unresolvable project means the editor should
    never have offered the save, so those stay exceptions and arrive as
    Error envelopes. But `stale` is a normal outcome of a correct
    program: F13 renders three specific buttons off it ("changed on disk
    since you opened it -- [reload] [overwrite] [diff]"), and `locked`
    has to name a holder and an age in the status bar. Neither is
    possible from a prose string inside an Error.

    to_dict() emits ONLY the fields that mean something for the outcome
    it is reporting, so a consumer never has to guess whether a null
    `holder` on a `stale` refusal is meaningful.
    """

    ok: bool
    reason: Optional[str] = None
    path: Optional[str] = None
    mtime: Optional[float] = None
    size: Optional[int] = None
    line_ending: Optional[str] = None
    bom: Optional[bool] = None
    trailing_newline: Optional[bool] = None
    holder: Optional[str] = None
    age_seconds: Optional[float] = None
    limit_kb: Optional[int] = None
    message: Optional[str] = None

    def __post_init__(self) -> None:
        if not self.ok and self.reason not in WRITE_REASONS:
            raise ValueError(f"unknown write refusal reason: {self.reason!r}")

    def to_dict(self) -> dict:
        if self.ok:
            return {
                "ok": True,
                "path": self.path,
                "mtime": self.mtime,
                "size": self.size,
                "line_ending": self.line_ending,
                "bom": self.bom,
                "trailing_newline": self.trailing_newline,
            }
        out: dict = {"ok": False, "reason": self.reason}
        if self.reason in ("stale", "exists"):
            out["mtime"] = self.mtime
        elif self.reason == "locked":
            out["holder"] = self.holder
            out["age_seconds"] = self.age_seconds
        elif self.reason == "too_large":
            out["size"] = self.size
            out["limit_kb"] = self.limit_kb
        elif self.reason == "io_error":
            out["message"] = self.message
        return out


@dataclass
class _FileIndex:
    built_at: float
    files: list[str]


class ProjectManager:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._repo = ProjectRepository(conn)
        self._index_cache: dict[str, _FileIndex] = {}

    # -- identity -----------------------------------------------------------

    async def init(self, path: str) -> Project:
        """`project init` a folder: write `.point\\project.json` and a
        row in `projects`. If the folder is already initialized, this is
        just resolve() plus a touch -- init is not destructive."""
        root = Path(path).expanduser().resolve(strict=False)
        if not root.is_dir():
            raise InvalidEventError(f"Not a directory: {root}")

        project_file = root / MARKER_DIR / PROJECT_FILE
        if project_file.exists():
            existing = await self._read_marker_and_resolve(project_file, root)
            if existing is not None:
                return existing

        (root / MARKER_DIR).mkdir(parents=True, exist_ok=True)
        project = Project.new(name=root.name, root_path=root)
        project_file.write_text(
            json.dumps(
                {"project_id": project.id, "name": project.name, "created_at": project.created_at},
                indent=2,
            ),
            encoding="utf-8",
        )
        await self._repo.upsert(project)
        self._apply_gitignore(root)
        _log.info("Project initialized: %s at %s", project.id, root)
        return project

    async def exists(self, project_id: str) -> bool:
        """A validation-only check -- no touch, no root_path rewrite, no
        write of any kind on a hit or a miss. `resolve(explicit_project_id=
        ...)` was considered and rejected for this: a hit calls `_touch()`,
        which unconditionally bumps `last_opened_at` and rewrites `root_path`
        -- a real DB write this check must not cause on every CreateSession/
        SetSessionProvider call that merely carries a project_id (B31)."""
        return (await self._repo.fetch_by_id(project_id)) is not None

    async def resolve(
        self, path: Optional[str] = None, explicit_project_id: Optional[str] = None
    ) -> Optional[Project]:
        """explicit project_id -> walk up from `path` (default cwd)
        looking for `.point\\` -> None. Never raises for "no project
        found" -- that is a legitimate, common answer."""
        if explicit_project_id:
            project = await self._repo.fetch_by_id(explicit_project_id)
            if project is None:
                raise ProjectNotFoundError(f"No project with id {explicit_project_id!r}")
            return await self._touch(project, Path(project.root_path))

        start = Path(path).expanduser().resolve(strict=False) if path else Path.cwd()
        marker = self._walk_up_for_marker(start)
        if marker is None:
            return None

        project_file = marker / PROJECT_FILE
        root = marker.parent
        resolved = await self._read_marker_and_resolve(project_file, root)
        return resolved

    async def _read_marker_and_resolve(self, project_file: Path, root: Path) -> Optional[Project]:
        try:
            data = json.loads(project_file.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            _log.warning("Unreadable project marker at %s: %s", project_file, exc)
            return None
        project_id = data.get("project_id")
        if not project_id:
            return None

        existing = await self._repo.fetch_by_id(project_id)
        if existing is None:
            # A marker with no matching row -- a fresh copy of the
            # database, or a project moved before its first resolve()
            # since init. Recreate the row from the marker; the id in
            # the file is the identity, not the row.
            project = Project(
                id=project_id,
                name=data.get("name", root.name),
                root_path=str(root),
                created_at=data.get("created_at", time.time()),
                last_opened_at=time.time(),
            )
            await self._repo.upsert(project)
            return project
        return await self._touch(existing, root)

    async def _touch(self, project: Project, current_root: Path) -> Project:
        """Match on id, not path (B7): root_path is overwritten to
        wherever the marker actually is now, so a rename or a move
        updates the row instead of orphaning it."""
        project.root_path = str(current_root)
        project.last_opened_at = time.time()
        await self._repo.upsert(project)
        return project

    @staticmethod
    def _walk_up_for_marker(start: Path) -> Optional[Path]:
        current = start
        while True:
            candidate = current / MARKER_DIR
            if candidate.is_dir() and (candidate / PROJECT_FILE).is_file():
                return candidate
            parent = current.parent
            if parent == current:
                return None
            current = parent

    def _apply_gitignore(self, root: Path) -> None:
        if appconfig.config_value("git.share_project_memory", False):
            return
        gitignore_path = root / ".gitignore"
        entry = f"{MARKER_DIR}/"
        try:
            existing = gitignore_path.read_text(encoding="utf-8") if gitignore_path.exists() else ""
        except OSError as exc:
            _log.warning("Could not read .gitignore at %s: %s", root, exc)
            return
        if any(line.strip().rstrip("/") == MARKER_DIR for line in existing.splitlines()):
            return
        if existing and not existing.endswith("\n"):
            existing += "\n"
        existing += entry + "\n"
        try:
            gitignore_path.write_text(existing, encoding="utf-8")
        except OSError as exc:
            _log.warning("Could not update .gitignore at %s: %s", root, exc)

    # -- file index -----------------------------------------------------------

    async def list_files(
        self, project: Project, prefix: str = "", limit: int = 200, refresh: bool = False
    ) -> list[str]:
        cache = self._index_cache.get(project.id)
        now = time.time()
        if refresh or cache is None or (now - cache.built_at) > _INDEX_TTL_S:
            files = await asyncio.to_thread(self._build_index, project)
            cache = _FileIndex(built_at=now, files=files)
            self._index_cache[project.id] = cache
        matched = [f for f in cache.files if f.startswith(prefix)] if prefix else cache.files
        return matched[:limit]

    @staticmethod
    def _build_index(project: Project) -> list[str]:
        root = Path(project.root_path)
        respect_gitignore = appconfig_overrides.config_value_live("index.respect_gitignore", True)
        max_kb = appconfig.config_value("index.max_file_kb", 100)

        rules: list[gitignore.IgnoreRule] = []
        if respect_gitignore:
            gitignore_path = root / ".gitignore"
            if gitignore_path.exists():
                try:
                    rules = gitignore.parse(gitignore_path.read_text(encoding="utf-8", errors="ignore"))
                except OSError:
                    rules = []

        results: list[str] = []
        for dirpath, dirnames, filenames in os.walk(root):
            base = Path(dirpath)
            kept_dirs = []
            for d in sorted(dirnames):
                if d in _ALWAYS_SKIP_DIRS:
                    continue
                rel = (base / d).relative_to(root).as_posix()
                if rules and gitignore.is_ignored(rel, True, rules):
                    continue
                kept_dirs.append(d)
            dirnames[:] = kept_dirs

            for name in sorted(filenames):
                full = base / name
                rel = full.relative_to(root).as_posix()
                if rules and gitignore.is_ignored(rel, False, rules):
                    continue
                try:
                    size = full.stat().st_size
                except OSError:
                    continue
                if size > max_kb * 1024:
                    continue
                if _looks_binary(full):
                    continue
                results.append(rel)
        return results

    # -- guarded file read -----------------------------------------------------

    async def read_file(
        self,
        project: Optional[Project],
        raw_path: str,
        *,
        allow_outside_project: bool = False,
        max_kb: Optional[int] = None,
    ) -> dict:
        r"""Guarded, size-capped read (B7), with B32's byte-level metadata.

        B32 changed two things here, both because the editor's save has
        to reproduce a file byte for byte and the old version threw away
        the information needed to do it:

        1. **Reads bytes, not text.** `Path.read_text()` opens in
           universal-newlines mode, which collapses `\r\n` to `\n`
           before any caller sees a single byte. Verified:
           `b"\xef\xbb\xbfline1\r\nline2\r\n"` came back as
           `'﻿line1\nline2\n'` -- the BOM survived and the line
           ending did not. So the frontend had nowhere to learn a file's
           line ending from, and F13's CRLF round-trip Done-when could
           not pass. Decoding by hand is what makes the counts below
           knowable.

        2. **The BOM is stripped from `content` and reported in `bom`.**
           This is B32's one NON-ADDITIVE change. Previously a BOM
           arrived as a literal U+FEFF inside the string, which meant
           every attachment of a BOM'd file fed a zero-width character
           to the model, and F13 would have had to strip it itself --
           a second place knowing how to handle a BOM. The size cap is
           still measured against the file's real on-disk size,
           including the BOM.

        `content` stays LF-normalised on the wire in both directions.
        The backend owns the translation at both ends, so exactly one
        place in Point knows about `\r\n`.

        `max_kb` DEFAULTS to `index.max_file_kb` (100), which is what the
        `read_file` TOOL keeps using. Only the editor's wire path
        (_handle_read_project_file) passes `editor.max_file_kb` (512).

        Defaulting the other way would have quietly widened the AGENT's
        read fivefold, which is the exact thing the two separate numbers
        exist to prevent: a 400KB file is reasonable to EDIT and
        unreasonable to pull into a prompt. The caller that wants the
        bigger cap asks for it.
        """
        resolved = resolve_path(raw_path, project, allow_outside_project=allow_outside_project)
        if not resolved.is_file():
            raise InvalidEventError(f"Not a file: {resolved}")
        if max_kb is None:
            max_kb = appconfig.config_value("index.max_file_kb", 100)
        stat = resolved.stat()
        if stat.st_size > max_kb * 1024:
            raise InvalidEventError(
                f"File too large to read: {stat.st_size} bytes (limit {max_kb}KB)"
            )
        raw = resolved.read_bytes()
        meta = describe_bytes(raw)
        return {
            "path": str(resolved),
            "content": meta["content"],
            "mtime": stat.st_mtime,
            "size": stat.st_size,
            "outside_project": is_outside_project(resolved, project),
            # B32's additive metadata. F13 displays and preserves these
            # rather than re-deriving any of them.
            "line_ending": meta["line_ending"],
            "crlf_count": meta["crlf_count"],
            "lf_count": meta["lf_count"],
            "bom": meta["bom"],
            "trailing_newline": meta["trailing_newline"],
            "decode_replacements": meta["decode_replacements"],
            "writable": _is_writable(resolved),
        }

    # -- guarded file write (B32) --------------------------------------------

    async def write_file(
        self,
        project: Project,
        raw_path: str,
        content: str,
        *,
        holder: str,
        expected_mtime: Optional[float],
        overwrite: bool = False,
        line_ending: str = "lf",
        bom: bool = False,
        trailing_newline: bool = True,
    ) -> WriteResult:
        r"""The editor's save. The first write path in the backend.

        `project` is REQUIRED, unlike read_file's Optional -- and there
        is deliberately no `allow_outside_project` flag. An
        out-of-project path cannot take a lock at all (`_lock_key` calls
        resolve_path with the flag off), so accepting one here would
        manufacture exactly the writable-but-unlockable case F13's
        "no `.point\` means read-only" decision rules out. An outside
        path raises PathDeniedError, as everywhere else.

        `expected_mtime=None` MEANS "I expect no file here" -- it is the
        creation signal, not a missing value. Without it the only way to
        create a file would be `overwrite=True`, which would make
        "create a new file" and "clobber someone else's concurrent
        change" the same request. Three intentions, three encodings:

            expected_mtime=<float>  exists with this mtime  -> stale
            expected_mtime=None     does not exist          -> exists
            overwrite=True          already decided         -> no check

        NOT REACHABLE FROM A TOOL OR AN AGENT. This is a manager method
        behind a runtime event handler, like project init. There is no
        `write_file` tool and there must not be one -- the absence is how
        CLAUDE.md's first hard rule is enforced structurally.
        """
        resolved = resolve_path(raw_path, project)        # 1 -- PathDeniedError escapes

        if not resolved.parent.is_dir():                  # 2
            return WriteResult(ok=False, reason="no_parent_dir")

        data = encode_for_write(
            content, line_ending=line_ending, bom=bom, trailing_newline=trailing_newline
        )
        max_kb = appconfig.editor_max_file_kb()
        if len(data) > max_kb * 1024:                     # 3
            return WriteResult(
                ok=False, reason="too_large", size=len(data), limit_kb=max_kb
            )

        rel_key = self._lock_key(project, raw_path)
        row = await self._repo.get_lock(project.id, rel_key)
        held_by_other = False
        if row is not None and row["holder"] != holder:   # 4
            age = time.time() - row["heartbeat_at"]
            if age <= LOCK_STALE_AFTER_S:
                return WriteResult(
                    ok=False, reason="locked", holder=row["holder"], age_seconds=age
                )
            held_by_other = True  # stale: writable, but NOT ours to heartbeat

        exists = resolved.exists()
        if not overwrite:                                 # 5
            if expected_mtime is None:
                if exists:
                    return WriteResult(
                        ok=False, reason="exists", mtime=resolved.stat().st_mtime
                    )
            elif not exists:
                # Deleted since open. check_stale calls this changed, and
                # so do we -- a save is not a silent re-create.
                return WriteResult(ok=False, reason="stale", mtime=None)
            elif abs(resolved.stat().st_mtime - expected_mtime) > 1e-6:
                return WriteResult(
                    ok=False, reason="stale", mtime=resolved.stat().st_mtime
                )

        if exists and not _is_writable(resolved):         # 6
            return WriteResult(ok=False, reason="read_only")

        try:                                              # 7
            _atomic_write(resolved, data)
        except OSError as exc:
            _log.warning("write_file failed for %s: %s", resolved, exc)
            return WriteResult(ok=False, reason="io_error", message=str(exc))

        # 8 -- the write IS the liveness proof, and it is better evidence
        # than a timer tick. After the bytes land, never before: touching
        # it first would extend a lock for a write that then failed. Only
        # for a row this holder actually owns -- a write by a holder with
        # no lock row must not create one, and a stale row belonging to
        # someone else is not ours to refresh.
        if row is not None and not held_by_other:
            await self._repo.touch_heartbeat(project.id, rel_key, time.time())

        stat = resolved.stat()                            # 9
        return WriteResult(
            ok=True,
            path=str(resolved),
            mtime=stat.st_mtime,
            size=stat.st_size,
            line_ending=line_ending,
            bom=bom,
            trailing_newline=trailing_newline,
        )

    # -- plain directory browse (B23) ------------------------------------
    # Not list_files()/  _build_index(): that is an indexed, gitignore-
    # aware, 30s-cached tree for one resolved project. This is a single,
    # uncached readdir of whatever folder the caller names, project or
    # no project, for F8's `@` picker to page through live.

    async def list_directory(
        self, project: Optional[Project], raw_path: str, *, cwd: Optional[str] = None
    ) -> dict:
        """List `raw_path`'s immediate children.

        B23 Gap 2's decision: a directory *listing* is exempt from the
        `allow_outside_project` gate a file *read* is subject to --
        `allow_outside_project=True` is passed unconditionally below,
        never read from `project.allow_outside_project` config. Listing
        exposes names, not content, and F8's spec wants `@C:\\` to work
        as ordinary filesystem browsing with no project open and no
        config flip at all; the config gate keeps guarding the read/
        attach path exactly as before (read_file, check_stale,
        AttachmentManager all still default to False). Browsing to find
        a file is not the same permission as reading it.
        """
        resolved = self._resolve_browse_target(raw_path, project, cwd)
        if not resolved.exists():
            raise InvalidEventError(f"No such path: {resolved}")
        if not resolved.is_dir():
            raise InvalidEventError(f"Not a directory: {resolved}")
        entries = await asyncio.to_thread(self._read_dir, resolved)
        return {
            "path": str(resolved),
            "entries": entries,
            "outside_project": is_outside_project(resolved, project),
        }

    @staticmethod
    def _resolve_browse_target(raw_path: str, project: Optional[Project], cwd: Optional[str]) -> Path:
        """resolve_path() refuses a relative path outright when there is
        no project -- there is nothing to resolve it against. F8's "no
        project: browse cwd" fallback needs one anyway, so a relative
        `raw_path` is joined onto the caller-supplied `cwd` *before*
        resolve_path ever sees it. `~` is left to resolve_path's own
        `os.path.expanduser` -- this only decides whether a join happens.

        `cwd` must be the caller's (the frontend's) own working
        directory, not the backend's: per CLAUDE.md the backend always
        runs from inside `backend\\`, which is essentially never where
        the user launched point.exe from.
        """
        expanded = os.path.expandvars(os.path.expanduser(raw_path))
        candidate = Path(expanded)
        if project is None and not candidate.is_absolute():
            if not cwd:
                raise InvalidEventError(
                    f"{raw_path!r} is relative and no project is open -- 'cwd' is required"
                )
            base = Path(os.path.expandvars(os.path.expanduser(cwd)))
            candidate = base / candidate
        return resolve_path(str(candidate), project, allow_outside_project=True)

    @staticmethod
    def _read_dir(path: Path) -> list[dict]:
        entries: list[dict] = []
        try:
            with os.scandir(path) as it:
                for entry in it:
                    try:
                        is_dir = entry.is_dir()
                    except OSError:
                        continue
                    if is_dir and entry.name in _ALWAYS_SKIP_DIRS:
                        continue
                    entries.append({"name": entry.name, "is_dir": is_dir})
        except OSError as exc:
            raise InvalidEventError(f"Cannot list directory: {path} ({exc})") from exc
        entries.sort(key=lambda e: (not e["is_dir"], e["name"].casefold()))
        return entries

    async def check_stale(
        self,
        project: Optional[Project],
        raw_path: str,
        recorded_mtime: float,
        *,
        allow_outside_project: bool = False,
    ) -> bool:
        """True if the file changed on disk since `recorded_mtime` (the
        mtime read_file() reported at open) -- including if it was
        deleted. Saving over a changed file must be refused, not
        silently done; this is the check that refusal is built on."""
        resolved = resolve_path(raw_path, project, allow_outside_project=allow_outside_project)
        if not resolved.exists():
            return True
        current = resolved.stat().st_mtime
        return abs(current - recorded_mtime) > 1e-6

    # -- soft file locks -----------------------------------------------------
    # rel_path is always resolved and made project-relative first, so
    # "main.py" and an absolute path to the same file hit the same lock
    # row, and a path outside the project can never take one out at all.

    def _lock_key(self, project: Project, raw_path: str) -> str:
        resolved = resolve_path(raw_path, project)
        root = Path(project.root_path).resolve(strict=False)
        return resolved.relative_to(root).as_posix()

    async def acquire_lock(self, project: Project, raw_path: str, holder: str) -> LockResult:
        rel_key = self._lock_key(project, raw_path)
        now = time.time()
        row = await self._repo.get_lock(project.id, rel_key)
        if row is None:
            await self._repo.put_lock(project.id, rel_key, holder, now, now)
            return LockResult(ok=True)
        if row["holder"] == holder:
            await self._repo.touch_heartbeat(project.id, rel_key, now)
            return LockResult(ok=True)
        age = now - row["heartbeat_at"]
        if age > LOCK_STALE_AFTER_S:
            await self._repo.put_lock(project.id, rel_key, holder, now, now)
            return LockResult(ok=True)
        return LockResult(ok=False, holder=row["holder"], age_seconds=age)

    async def heartbeat_lock(self, project: Project, raw_path: str, holder: str) -> LockResult:
        rel_key = self._lock_key(project, raw_path)
        row = await self._repo.get_lock(project.id, rel_key)
        if row is None:
            raise LockNotHeldError(f"No lock held on {rel_key!r} to heartbeat")
        if row["holder"] != holder:
            age = time.time() - row["heartbeat_at"]
            return LockResult(ok=False, holder=row["holder"], age_seconds=age)
        await self._repo.touch_heartbeat(project.id, rel_key, time.time())
        return LockResult(ok=True)

    async def release_lock(self, project: Project, raw_path: str, holder: str) -> bool:
        """No-op (returns False) if this holder does not currently hold
        the lock -- releasing something you never held is not this
        holder's business to force."""
        rel_key = self._lock_key(project, raw_path)
        row = await self._repo.get_lock(project.id, rel_key)
        if row is None or row["holder"] != holder:
            return False
        await self._repo.delete_lock(project.id, rel_key)
        return True

    async def take_over_lock(self, project: Project, raw_path: str, holder: str) -> LockResult:
        """Explicit; steals a stale or held lock unconditionally. The
        staleness check belongs to acquire() -- calling take_over is
        the caller declaring they already decided to steal it."""
        rel_key = self._lock_key(project, raw_path)
        now = time.time()
        await self._repo.put_lock(project.id, rel_key, holder, now, now)
        return LockResult(ok=True)


# ---------------------------------------------------------------------------
# B32: the byte layer. One place in Point knows about \r\n, and it is here.
# ---------------------------------------------------------------------------

def describe_bytes(raw: bytes) -> dict:
    r"""Decode a file's bytes and report what was in them.

    Separate from read_file so the round-trip property
    (describe_bytes -> encode_for_write == the original bytes) can be
    tested on bytes directly, with no filesystem and no project.

    `line_ending` is "mixed" when both styles appear. B32 reports the
    COUNTS and does not decide what to do about a mixed file: counting
    bytes is resolution, choosing a normalisation is a product decision,
    and that one is F13's. This is the backend-resolves /
    frontend-renders line drawn where B20 and B23 drew it, not one step
    past it.
    """
    bom = raw.startswith(b"\xef\xbb\xbf")
    body = raw[3:] if bom else raw

    crlf_count = body.count(b"\r\n")
    lf_count = body.count(b"\n") - crlf_count

    if crlf_count and lf_count:
        line_ending = "mixed"
    elif crlf_count:
        line_ending = "crlf"
    else:
        line_ending = "lf"

    text = body.decode("utf-8", errors="replace")
    # errors="replace" is lossy: a non-UTF-8 file round-trips as U+FFFD
    # and saving it would corrupt it silently -- the exact failure B32
    # exists to prevent, arriving by the back door. Count them so F13
    # can open such a file strictly read-only. No encoding detection and
    # no second codec: that is a larger decision than this episode
    # should make, and refusing to write what Point cannot round-trip is
    # the honest small answer.
    decode_replacements = text.count("�")

    # Normalise to LF for `content`. A lone \r is a classic-Mac ending;
    # nothing in this project claims to support one, so it is left as a
    # literal character rather than silently treated as a line break --
    # which keeps the round trip exact for it too.
    content = text.replace("\r\n", "\n")

    return {
        "content": content,
        "line_ending": line_ending,
        "crlf_count": crlf_count,
        "lf_count": lf_count,
        "bom": bom,
        "trailing_newline": body.endswith(b"\n"),
        "decode_replacements": decode_replacements,
    }


def encode_for_write(
    content: str, *, line_ending: str = "lf", bom: bool = False, trailing_newline: bool = True
) -> bytes:
    r"""describe_bytes' inverse. `content` is LF-normalised going in.

    Order matters and is the spec: expand \n to the target ending, apply
    the trailing-newline flag, prepend the BOM, encode. Doing the
    trailing-newline step after the expansion is what makes a CRLF
    file's final newline \r\n rather than a bare \n.
    """
    if line_ending not in ("lf", "crlf"):
        raise InvalidEventError(
            f"line_ending must be 'lf' or 'crlf', not {line_ending!r}"
        )

    text = content.replace("\r\n", "\n")
    if trailing_newline:
        if not text.endswith("\n"):
            text += "\n"
    else:
        text = text.rstrip("\n")

    if line_ending == "crlf":
        text = text.replace("\n", "\r\n")

    data = text.encode("utf-8")
    return b"\xef\xbb\xbf" + data if bom else data


def _atomic_write(target: Path, data: bytes) -> None:
    r"""Temp file in the SAME directory, then os.replace.

    Same directory is mandatory: os.replace is only atomic within a
    volume. The temp-plus-rename shape matches what config\state.go
    already does on the Go side.

    On any failure the temp is removed, so a failed save never leaves a
    half-written target AND never leaves a stray tmp beside it -- a
    leftover temp per save is the kind of defect that surfaces three
    weeks later as a confused `git status`.

    On Windows os.replace raises PermissionError when the target is open
    by another process. That is a real case and the caller turns it into
    `io_error`, not a traceback.
    """
    import tempfile

    handle = tempfile.NamedTemporaryFile(
        mode="wb", dir=str(target.parent), prefix=".point-write-", suffix=".tmp", delete=False
    )
    tmp = Path(handle.name)
    try:
        with handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, target)
    except BaseException:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            _log.warning("could not remove temp file %s after a failed write", tmp)
        raise


def _is_writable(path: Path) -> bool:
    """The read-only ATTRIBUTE, not a full ACL evaluation.

    os.access(..., W_OK) does not evaluate Windows ACLs. That is enough
    for F13's autosave rule 1 ("never autosave a read-only file")
    without a probe write, and an ACL-denied write still surfaces as
    io_error at save time. Do not document this as a guarantee.
    """
    try:
        return os.access(path, os.W_OK)
    except OSError:
        return False


def _looks_binary(path: Path) -> bool:
    try:
        with open(path, "rb") as handle:
            chunk = handle.read(8192)
    except OSError:
        return True  # unreadable -- keep it out of the index rather than crash later
    return b"\x00" in chunk
