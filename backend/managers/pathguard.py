r"""The path guard -- one function, used by everything that touches the
filesystem (B7).

Every `@file` attachment, every tool, every editor open, and the SSH
jail all go through `resolve_path()`. There is exactly one
implementation on purpose: two would drift, and the second one to drift
is the one that lets something through.

    resolve_path(raw_path, project) -> Path
        Expands `~` and environment variables, resolves symlinks and
        `..` against the real filesystem, and returns an absolute
        Windows path -- or raises PathDeniedError.

Containment is checked **case-folded**, not with a naive `startswith`.
Windows paths are case-insensitive, so a raw string comparison lets
`D:\PROJECTS\Point\..\other` slide past a guard that only checked
`D:\Projects\Point`. Both sides are `Path.resolve()`d first (which
collapses `..` against the real filesystem), and only *then* compared
case-folded -- so the case-insensitivity fix and the `..`-collapsing fix
are two different steps, and dropping either one reopens an escape.
This is Windows-specific and easy to regress if this function is ever
ported or "simplified".
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Optional

from managers.project import Project
from runtime.errors import PathDeniedError


def resolve_path(
    raw_path: str,
    project: Optional[Project],
    *,
    allow_outside_project: bool = False,
) -> Path:
    """Resolve `raw_path` against `project.root_path` (or, with no
    project, require it to already be absolute) and guard it.

    With a project: a relative path is joined onto the project root; an
    absolute path is resolved as given. Either way, the result must land
    inside the root unless `allow_outside_project` is set.

    With no project: relative paths have nothing to resolve against and
    are always refused. An absolute path is allowed only when
    `allow_outside_project` is set -- "no project scope" means every
    guarded operation is off by default, not "everything is local so
    it's fine".
    """
    expanded = os.path.expandvars(os.path.expanduser(raw_path))
    candidate = Path(expanded)

    if project is None:
        if not candidate.is_absolute():
            raise PathDeniedError(
                f"{raw_path!r} refused: no project is open, and a relative "
                "path has nothing to resolve against"
            )
        if not allow_outside_project:
            raise PathDeniedError(
                f"{raw_path!r} refused: no project is open "
                "(allow_outside_project is off)"
            )
        return candidate.resolve(strict=False)

    root = Path(project.root_path).resolve(strict=False)
    if not candidate.is_absolute():
        candidate = root / candidate
    resolved = candidate.resolve(strict=False)

    if not _contains(root, resolved) and not allow_outside_project:
        raise PathDeniedError(
            f"{raw_path!r} resolves to {resolved}, outside project root {root}"
        )
    return resolved


def is_outside_project(resolved: Path, project: Optional[Project]) -> bool:
    """For callers that let allow_outside_project through resolve_path()
    and still need to report the fact (B7 done-when #3: an allowed
    outside path must be reported as outside, not silently treated as
    local)."""
    if project is None:
        return True
    root = Path(project.root_path).resolve(strict=False)
    return not _contains(root, resolved)


def _contains(root: Path, candidate: Path) -> bool:
    """Case-insensitive containment. Both arguments must already be
    Path.resolve()'d -- this function only case-folds the comparison,
    it does not collapse `..` itself."""
    root_cf = str(root).casefold()
    candidate_cf = str(candidate).casefold()
    if candidate_cf == root_cf:
        return True
    sep = os.sep
    if not root_cf.endswith(sep):
        root_cf += sep
    return candidate_cf.startswith(root_cf)
