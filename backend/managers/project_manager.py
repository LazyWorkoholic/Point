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
        self, project: Optional[Project], raw_path: str, *, allow_outside_project: bool = False
    ) -> dict:
        resolved = resolve_path(raw_path, project, allow_outside_project=allow_outside_project)
        if not resolved.is_file():
            raise InvalidEventError(f"Not a file: {resolved}")
        max_kb = appconfig.config_value("index.max_file_kb", 100)
        stat = resolved.stat()
        if stat.st_size > max_kb * 1024:
            raise InvalidEventError(
                f"File too large to read: {stat.st_size} bytes (limit {max_kb}KB)"
            )
        content = resolved.read_text(encoding="utf-8", errors="replace")
        return {
            "path": str(resolved),
            "content": content,
            "mtime": stat.st_mtime,
            "size": stat.st_size,
            "outside_project": is_outside_project(resolved, project),
        }

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


def _looks_binary(path: Path) -> bool:
    try:
        with open(path, "rb") as handle:
            chunk = handle.read(8192)
    except OSError:
        return True  # unreadable -- keep it out of the index rather than crash later
    return b"\x00" in chunk
