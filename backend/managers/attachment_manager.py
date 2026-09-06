r"""AttachmentManager: the business rules `database\attachment_repository.py`
deliberately has none of (B12).

B8 built the read half of attachments -- what is attached, and
remembering what was already sent -- and left `attach` / `detach` /
`set_mode` here, unbuilt, rather than guessing at their shape an episode
early. This is that half.

Three things live here and nowhere else:

**The guards.** Every refusal returns a *reason*, never a traceback. A
40MB log, a `.exe`, and a directory are three different mistakes, and a
user who is told "attachment failed" learns nothing about which one they
made. `AttachmentRefusedError` carries a slug (`too_large`, `binary`,
`is_directory`, `path_denied`, `not_found`) and, where one exists, the
fallback the refusal comes with.

**The estimate.** `resolve()` reports what the attachment will cost
*before* the user sends, by calling the context builder's own
`_render_attachment()` against a throwaway Attachment and estimating the
text it produces. Deliberately not a parallel size/4 calculation: the
number the UI shows and the number the turn actually spends have to come
from the same code, or the estimate is a second implementation that
drifts from the first -- and the one that drifts is always the one the
user was shown.

**What "detach never deletes" means.** It removes the binding. The file
on disk is untouched, and so is any task result that quoted it -- the
history that already contains the file is history, and rewriting it
because a binding went away would be a lie about what was sent.

Path resolution goes through B7's `resolve_path()`, never `Path()`, for
the reason `_render_attachment` already documents: an attachment path is
a string that becomes a file read, and two path implementations drift.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any, Optional

import aiosqlite

import appconfig
from database.attachment_repository import AttachmentRepository
from logging_setup import get_logger
from managers.attachment import (
    MODE_DETACHED,
    MODE_FULL,
    MODE_REFERENCE,
    SOURCE_TYPE_FILE,
    Attachment,
)
from managers.context import estimate_tokens
# The builder's own renderer. Imported private-to-private on purpose:
# see this module's docstring -- an estimate produced by different code
# than the spend is not an estimate of the spend.
from managers.context import _render_attachment
from managers.pathguard import resolve_path
from managers.project import Project
from managers.project_manager import _looks_binary
from runtime.errors import AttachmentNotFoundError, AttachmentRefusedError, PathDeniedError

_log = get_logger("attachments")

# Config `attachments.max_kb`, default 100 -- deliberately the same
# default as B7's `index.max_file_kb` and deliberately a SEPARATE key.
# "How big a file may the indexer walk past" and "how big a file may the
# user spend context on" are different questions that happen to have the
# same answer today, and collapsing them means tuning one silently
# retunes the other.
DEFAULT_MAX_KB = 100

# How many lines the size refusal offers instead, and how many entries
# the directory refusal lists.
HEAD_LINES = 200
LISTING_ENTRIES = 20

VALID_MODES = (MODE_FULL, MODE_REFERENCE, MODE_DETACHED)


def max_attachment_kb() -> int:
    value = appconfig.config_value("attachments.max_kb", DEFAULT_MAX_KB)
    try:
        value = int(value)
    except (TypeError, ValueError):
        _log.warning(
            "config attachments.max_kb is not an integer (%r); using %d",
            value, DEFAULT_MAX_KB,
        )
        return DEFAULT_MAX_KB
    if value <= 0:
        _log.warning(
            "config attachments.max_kb must be positive (got %d); using %d",
            value, DEFAULT_MAX_KB,
        )
        return DEFAULT_MAX_KB
    return value


class AttachmentResolution:
    """What one attachment would cost, resolved against the filesystem
    right now.

    `tokens` is an estimate and is labelled one everywhere it surfaces.
    `managers\\context.py:estimate_tokens` is `len(text) // 4` and is not
    measured against any real tokenizer -- what this class guarantees is
    that the estimate and the eventual spend are the *same computation*,
    not that either is exact.
    """

    __slots__ = ("path", "size", "mtime", "tokens", "mode", "outside_project")

    def __init__(
        self,
        path: Path,
        size: int,
        mtime: int,
        tokens: int,
        mode: str,
        outside_project: bool,
    ) -> None:
        self.path = path
        self.size = size
        self.mtime = mtime
        self.tokens = tokens
        self.mode = mode
        self.outside_project = outside_project

    def to_dict(self) -> dict[str, Any]:
        return {
            "path": str(self.path),
            "size": self.size,
            "mtime": self.mtime,
            "tokens_estimate": self.tokens,
            "mode": self.mode,
            "outside_project": self.outside_project,
        }


class AttachmentManager:
    def __init__(self, conn: aiosqlite.Connection) -> None:
        self._repo = AttachmentRepository(conn)

    # -- resolution + guards ---------------------------------------------

    def resolve(
        self,
        raw_path: str,
        *,
        project: Optional[Project],
        allow_outside_project: bool = False,
        mode: str = MODE_FULL,
        model: Optional[str] = None,
        attachment_id: Optional[str] = None,
    ) -> AttachmentResolution:
        """Guard `raw_path` and report what attaching it would cost.

        Raises `AttachmentRefusedError` with a reason for every refusal.
        Purely synchronous and side-effect free -- nothing is written,
        which is what lets the UI call this on every keystroke of an `@`
        picker without attaching anything.

        Guard order is not arbitrary, and it is ordered by *which
        refusal is most useful*, not by which check is cheapest:

          1. the path guard, because a path we are not allowed to touch
             must not be stat'ed;
          2. existence;
          3. directory, before size, because a directory's `st_size` is
             meaningless and refusing it as "too large" would be a
             nonsense answer to a sensible mistake;
          4. **binary, before size.** A 200KB `.exe` is over the limit
             *and* binary, and only one of those is worth telling the
             user: "too large, here are the first 200 lines" is an
             offer that cannot be taken for a file that has no lines.
             Costs a bounded 8KB read even on a 40MB file, which is not
             a price worth trading a wrong answer for;
          5. size, which is now the last thing a plain text file can
             fail.

        **A file that is both binary and oversized always reports
        `binary`, never `too_large`.** Decided, not emergent -- the
        consequence was surfaced by a 40MB file created with
        `SetLength()`, which is sparse and therefore all null bytes, so
        it refused as binary rather than as oversized.

        `binary` wins because it is the reason that stays true. A
        binary file is refused whatever its size, so making the message
        depend on the size would mean the same mistake is explained two
        different ways depending on how much of it there is -- and the
        oversize explanation is the useless one of the pair, since its
        whole value is the first-200-lines offer that a binary cannot
        take. `too_large` remains fully reachable, and reachable for
        the case it is about: a large *text* file, which is what a 40MB
        log actually is.
        """
        if mode not in VALID_MODES:
            raise AttachmentRefusedError(
                f"{mode!r} is not an attachment mode "
                f"(expected one of: {', '.join(VALID_MODES)})",
                reason="bad_mode",
            )

        try:
            path = resolve_path(
                raw_path, project, allow_outside_project=allow_outside_project
            )
        except PathDeniedError as exc:
            # B7's own wording is "no project is open", which reads as a
            # fact about the DIRECTORY -- and is actively misleading
            # here, because the usual way to hit this is standing inside
            # a perfectly real project with a SESSION that was never
            # bound to it. Say which of the two it actually is, and name
            # the fix. There is no `--allow-outside` on `attach`: the
            # guard has to match what the context builder will do at send
            # time, and a per-command override is exactly what lets
            # `attach` quote a cost the builder then refuses to spend.
            if project is None:
                offer = (
                    "this session is not bound to a project, and the file is not "
                    "inside one either. Run `project init` in the file's directory, "
                    "then `session new` from there -- or attach a file that lives "
                    "inside an existing project, which binds the session to it."
                )
            else:
                offer = (
                    f"the file is outside this session's project "
                    f"({project.root_path}). Attach a file inside it, or set "
                    f"project.allow_outside_project = true in config.toml -- the "
                    f"same switch the context builder reads at send time."
                )
            raise AttachmentRefusedError(
                f"{raw_path} refused: {exc}",
                reason="path_denied",
                offer=offer,
            ) from exc

        if not path.exists():
            raise AttachmentRefusedError(
                f"{raw_path} refused: no such file ({path})",
                reason="not_found",
            )

        if path.is_dir():
            raise AttachmentRefusedError(
                f"{raw_path} refused: {path} is a directory, not a file. "
                f"Attach a file, or ask for a listing instead.",
                reason="is_directory",
                offer=f"a listing of the first {LISTING_ENTRIES} entries",
            )

        try:
            stat = path.stat()
        except OSError as exc:
            raise AttachmentRefusedError(
                f"{raw_path} refused: unreadable ({exc.strerror or exc})",
                reason="unreadable",
            ) from exc

        if _looks_binary(path):
            raise AttachmentRefusedError(
                f"{raw_path} refused: {path.name} looks binary "
                f"(null bytes in the first 8KB). A binary file costs the "
                f"whole context budget and tells the model nothing.",
                reason="binary",
            )

        max_kb = max_attachment_kb()
        if stat.st_size > max_kb * 1024:
            raise AttachmentRefusedError(
                f"{raw_path} refused: {stat.st_size} bytes exceeds the "
                f"{max_kb}KB attachment limit (attachments.max_kb). "
                f"The first {HEAD_LINES} lines can be attached instead.",
                reason="too_large",
                offer=f"the first {HEAD_LINES} lines",
            )

        # The estimate, produced by the builder's own renderer against a
        # throwaway row. `recorded_mtime`/`recorded_size` are NULL, which
        # is what a not-yet-sent attachment really looks like -- so this
        # renders the first-send body, exactly what the next turn spends.
        preview = Attachment(
            id=attachment_id or _PREVIEW_ID,
            session_id="",
            source_type=SOURCE_TYPE_FILE,
            source_id=str(path),
            mode=mode,
        )
        rendered = _render_attachment(preview, model, project, allow_outside_project)

        return AttachmentResolution(
            path=path,
            size=int(stat.st_size),
            mtime=int(stat.st_mtime),
            tokens=estimate_tokens(rendered.text, model),
            mode=mode,
            outside_project=_is_outside(path, project),
        )

    def preview(self, raw_path: str, *, project: Optional[Project],
                allow_outside_project: bool = False) -> str:
        """Make good on whatever the refusal offered.

        A refusal that offers a fallback nobody can accept is a refusal
        with a decoration on it. This is the one function that honours
        both offers: the first `HEAD_LINES` lines of an oversized file,
        or the first `LISTING_ENTRIES` entries of a directory.

        Reads through the same guard as everything else, so "offer a
        preview" never becomes a way around the path rules.
        """
        path = resolve_path(raw_path, project, allow_outside_project=allow_outside_project)
        if path.is_dir():
            entries = sorted(p.name + ("\\" if p.is_dir() else "") for p in path.iterdir())
            shown = entries[:LISTING_ENTRIES]
            more = len(entries) - len(shown)
            listing = "\n".join(f"  {name}" for name in shown)
            tail = f"\n  ... and {more} more" if more > 0 else ""
            return f"{path} ({len(entries)} entries):\n{listing}{tail}"

        lines: list[str] = []
        with open(path, "r", encoding="utf-8", errors="replace") as handle:
            for index, line in enumerate(handle):
                if index >= HEAD_LINES:
                    break
                lines.append(line.rstrip("\n"))
        return "\n".join(lines)

    # -- attach / detach / set_mode ---------------------------------------

    async def attach(
        self,
        session_id: str,
        raw_path: str,
        *,
        project: Optional[Project],
        mode: str,
        allow_outside_project: bool = False,
        model: Optional[str] = None,
    ) -> tuple[Attachment, AttachmentResolution]:
        """Guard, then bind. Idempotent per (session, resolved path).

        Re-attaching the same file is not a second attachment: it would
        put two rows in front of the builder, and the file would enter
        every future request twice. Re-attaching an existing binding
        updates its mode instead, which is the only thing that could
        have changed.

        `mode` comes from the agent's `default_attach_mode` at the call
        site and is overridable per invocation -- this function does not
        reach for an agent, because the same attach has to work from the
        CLI, where there is no invocation to take a default from.
        """
        resolution = self.resolve(
            raw_path, project=project, allow_outside_project=allow_outside_project,
            mode=mode, model=model,
        )
        source_id = str(resolution.path)

        existing = await self._repo.fetch_by_source(session_id, source_id)
        if existing is not None:
            if existing.mode != mode:
                await self._repo.set_mode(existing.id, mode)
                existing = await self._repo.fetch_one(existing.id)
            _log.info("Attachment %s already bound to session %s", source_id, session_id)
            return existing, resolution

        attachment = await self._repo.insert(session_id, source_id, mode)
        _log.info(
            "Attached %s to session %s (mode=%s, ~%d tokens)",
            source_id, session_id, mode, resolution.tokens,
        )
        return attachment, resolution

    async def detach(self, session_id: str, attachment_id: str) -> Attachment:
        """Remove the binding. **Never touches the file**, and never
        touches a task result that quoted it.

        The row itself does go, which is not the same thing: `detached`
        as a *mode* means "keep the binding, stop spending tokens on it"
        (B8's third mode, still reachable through `set_mode`), while
        `detach` means "this is no longer attached at all". Both exist
        because they answer different questions, and collapsing them
        would leave no way to say the second one.
        """
        attachment = await self._require(session_id, attachment_id)
        await self._repo.delete(attachment.id)
        _log.info(
            "Detached %s from session %s (file untouched)",
            attachment.source_id, session_id,
        )
        return attachment

    async def set_mode(self, session_id: str, attachment_id: str, mode: str) -> Attachment:
        if mode not in VALID_MODES:
            raise AttachmentRefusedError(
                f"{mode!r} is not an attachment mode "
                f"(expected one of: {', '.join(VALID_MODES)})",
                reason="bad_mode",
            )
        attachment = await self._require(session_id, attachment_id)
        await self._repo.set_mode(attachment.id, mode)
        updated = await self._repo.fetch_one(attachment.id)
        assert updated is not None  # just written, same transaction scope
        return updated

    async def list(self, session_id: str) -> list[Attachment]:
        return await self._repo.fetch_for_session(session_id)

    # -- helpers -----------------------------------------------------------

    async def _require(self, session_id: str, attachment_id: str) -> Attachment:
        """Scoped by session on purpose: an attachment id is not a
        capability. The same rule B10's `load_task_result` follows, for
        the same reason -- knowing an id must not be enough to act on
        something in a session you are not in.
        """
        attachment = await self._repo.fetch_one(attachment_id)
        if attachment is None or attachment.session_id != session_id:
            raise AttachmentNotFoundError(
                f"No attachment {attachment_id!r} in session {session_id!r}"
            )
        return attachment


# A fixed, uuid-shaped id for the estimate's throwaway row. Fixed rather
# than random so two `resolve()` calls on the same file report the same
# number: the id is inside the rendered header, and a variable-length id
# would make the estimate jitter for no reason a user could explain.
_PREVIEW_ID = "00000000-0000-0000-0000-000000000000"


def _is_outside(resolved: Path, project: Optional[Project]) -> bool:
    from managers.pathguard import is_outside_project

    return is_outside_project(resolved, project)
