r"""Project: a durable identity for a folder on disk (B7).

The row in `projects` maps `id -> root_path`, but `id` is the only thing
anything else is allowed to remember. `root_path` is "last known" and
gets overwritten on every resolve -- that is what lets a rename or a
move survive: the `.point\project.json` file travels with the folder,
still carrying the same `project_id`, so resolve() finds the marker at
its new location and updates root_path to match rather than losing the
project's memories.

    id              -> UUID, generated once at `project init` and never
                       reissued. Every memory, session, and lock keys
                       off this, never off root_path.
    name            -> Defaults to the directory's name at init time.
                       Cosmetic; renaming the project does not change
                       the folder name or vice versa.
    root_path       -> Absolute path, last known. Overwritten whenever
                       resolve() finds the marker somewhere else.
    created_at      -> When `project init` ran.
    last_opened_at  -> Bumped on every successful resolve. Nothing
                       reads it yet; it exists so a future "recent
                       projects" list does not need a migration.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

# The marker directory and the identity file inside it. `resolve()`
# walks up from a path looking for exactly this.
MARKER_DIR = ".point"
PROJECT_FILE = "project.json"


@dataclass
class Project:
    id: str
    name: str
    root_path: str
    created_at: float
    last_opened_at: Optional[float] = None

    @staticmethod
    def new(name: str, root_path: Path) -> "Project":
        now = time.time()
        return Project(
            id=str(uuid.uuid4()),
            name=name,
            root_path=str(root_path),
            created_at=now,
            last_opened_at=now,
        )

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "name": self.name,
            "root_path": self.root_path,
            "created_at": self.created_at,
            "last_opened_at": self.last_opened_at,
        }
