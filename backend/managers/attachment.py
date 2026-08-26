"""Attachment: one row of `context_attachments`, as a domain object.

Its own module for the same reason Session and Message have theirs --
`database\\attachment_repository.py` imports this, and this imports
nothing from `database\\`, so the repository stays swappable and
`managers\\context.py` never has to reach through a repository module to
name the type it works with.

Field-by-field:

id              -> UUID. What `context.report` prints and what a future
                   `detach` will address.
session_id      -> Attachments are per-session, not per-message: the
                   same file stays attached across turns, which is the
                   whole reason `(mtime, size)` below is worth recording.
source_type     -> `file` today. The column is deliberately not an enum
                   in SQLite because B12 adds more kinds (project files
                   by index id, URLs, prior task output) without a
                   migration.
source_id       -> What `source_type` says it is. For `file`, an
                   absolute path.
mode            -> full | reference | detached. Set at attach time from
                   the agent's `default_attach_mode` (B12); B8 only
                   reads it.
recorded_mtime  -> Whole seconds, matching the INTEGER column. This plus
recorded_size      `recorded_size` is the whole change-detection scheme:
                   equal pair means the file's content is already in the
                   history verbatim, so re-sending it is pure waste.
                   Nullable, and a NULL pair reads as "never sent" --
                   which is the correct answer for a row nothing has
                   resolved yet.
created_at      -> When the attachment was made, not when it was last
                   resolved. Resolution updates the recorded pair; it
                   does not touch this.

`detached` deliberately keeps its row. Detaching is "stop spending
tokens on this", not "forget I ever mentioned it" -- the history that
already quoted the file is untouched either way.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional


# The three values `mode` may take. B8 reads all three; B12 is what lets
# a user set them.
MODE_FULL = "full"
MODE_REFERENCE = "reference"
MODE_DETACHED = "detached"

SOURCE_TYPE_FILE = "file"


@dataclass(frozen=True)
class Attachment:
    id: str
    session_id: str
    source_type: str
    source_id: str
    mode: str
    recorded_mtime: Optional[int] = None
    recorded_size: Optional[int] = None
    created_at: float = 0.0

    @property
    def recorded_stat(self) -> Optional[tuple[int, int]]:
        """The recorded `(mtime, size)` pair, or None when either half is
        missing. Half a pair is not a usable comparison, and treating it
        as one is how an unchanged file gets re-sent forever."""
        if self.recorded_mtime is None or self.recorded_size is None:
            return None
        return (self.recorded_mtime, self.recorded_size)

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "session_id": self.session_id,
            "source_type": self.source_type,
            "source_id": self.source_id,
            "mode": self.mode,
            "recorded_mtime": self.recorded_mtime,
            "recorded_size": self.recorded_size,
            "created_at": self.created_at,
        }
