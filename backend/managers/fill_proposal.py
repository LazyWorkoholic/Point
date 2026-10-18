r"""FillProposal: the parsed, validated shape of a fill-mode reply (B24).

B11 already asks the Filler-Explainer for a structured region (a JSON
object with `file`, `start_line`, `end_line`, `replacement`, `note` --
`managers\context.py:_render_editor_context`'s fill-mode instructions)
and then throws the structure away, persisting the model's raw text as
an ordinary assistant message. This module is the response side of
that contract: parse it, validate it against the region the Filler was
actually shown, and hand back one of two ordinary outcomes.

Deliberately **not** in `editor_context.py` -- that module is the
*request* side (what the Filler is told); this is the *response* side,
and `editor_context.py` is already imported by `managers\context.py`,
so putting the response type there invites a circular import the
moment parsing needs anything from the context builder (it does not,
today, but the module boundary should not depend on that staying true).

A failure here is not an exception and is never routed through an
`Error` event -- the Filler is explicitly allowed to refuse in plain
text (`context.py`'s fill-mode instructions: "If the request needs a
second file, a new file, or a command, refuse in plain text instead of
guessing at scope"), and B11's one-file refusal also delivers a normal
reply via the echo path. A fill-mode turn whose reply does not parse is
simply a turn with no proposal -- the reply still persists and still
renders as text, exactly as a plain chat turn's would.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Optional, Union

# Reason slugs -- stable strings, logged and (for the unit-level gate)
# asserted on directly. Not an Enum: nothing on the wire carries one of
# these (a parse failure produces no event at all, only a log line), so
# there is no serialization boundary that would benefit from one.
REASON_NOT_JSON = "not_json"
REASON_BAD_SHAPE = "bad_shape"
REASON_RANGE_INVERTED = "range_inverted"
REASON_RANGE_OUTSIDE_SHOWN = "range_outside_shown"
REASON_FILE_MISMATCH = "file_mismatch"

# "the offending text's first ~200 characters for the log -- never the
# whole reply, which can be large."
_EXCERPT_CHARS = 200

_FENCE_RE = re.compile(r"^```[a-zA-Z]*$")


@dataclass(frozen=True)
class FillProposal:
    """The parsed, validated shape. All nine fields travel together on
    the wire (`to_dict()`), no subset -- B20's rule, for B20's reason: a
    consumer that only gets some fields cannot tell a missing one from
    an omitted one.

    B36 added `mtime`/`size`: the stat captured at the SAME disk read
    that produced `shown_start`/`shown_end`'s region
    (`_render_editor_context`, via `RegisteredEditorContext`), never
    recomputed here -- same reasoning as `shown_start`/`shown_end`
    themselves."""

    file: str              # as the model returned it, before resolution
    start_line: int        # 1-based inclusive
    end_line: int           # 1-based inclusive
    replacement: str
    note: Optional[str]
    shown_start: int       # the region the model was actually shown
    shown_end: int
    mtime: float           # stat of the file at the region-generation read
    size: int

    def to_dict(self) -> dict[str, Any]:
        return {
            "file": self.file,
            "start_line": self.start_line,
            "end_line": self.end_line,
            "replacement": self.replacement,
            "note": self.note,
            "shown_start": self.shown_start,
            "shown_end": self.shown_end,
            "mtime": self.mtime,
            "size": self.size,
        }


@dataclass(frozen=True)
class FillParseFailure:
    """Not an exception. `reason` is one of the `REASON_*` slugs above;
    `detail` is already truncated to `_EXCERPT_CHARS` and safe to log
    verbatim."""

    reason: str
    detail: str


FillParseResult = Union[FillProposal, FillParseFailure]


def parse_fill_proposal(
    text: str, *, file: str, shown_start: int, shown_end: int, mtime: float, size: int,
) -> FillParseResult:
    """Parse `text` (the Filler's full reply) against the region it was
    actually shown (`shown_start`/`shown_end`, carried from the context
    build that produced this turn's prompt -- never recomputed here:
    `resolve_scope()` is deterministic given the same lines, but the
    file may have changed on disk between the build and this call, and
    recomputing would then validate against a different region than the
    model saw).

    `mtime`/`size` (B36) are carried the same way and for the same
    reason -- not validated against anything here (that is
    `CheckFileStale`'s job, against a LATER read), only passed through
    onto the resulting `FillProposal` unchanged."""
    stripped = _strip_single_fence(text)

    try:
        data = json.loads(stripped)
    except (json.JSONDecodeError, ValueError):
        return FillParseFailure(REASON_NOT_JSON, _excerpt(text))

    shape_failure = _check_shape(data)
    if shape_failure is not None:
        return FillParseFailure(REASON_BAD_SHAPE, f"{shape_failure}: {_excerpt(text)}")

    start_line = data["start_line"]
    end_line = data["end_line"]
    candidate_file = data["file"]

    if start_line > end_line:
        return FillParseFailure(
            REASON_RANGE_INVERTED,
            f"start_line={start_line} > end_line={end_line}",
        )
    if not (shown_start <= start_line and end_line <= shown_end):
        return FillParseFailure(
            REASON_RANGE_OUTSIDE_SHOWN,
            f"{start_line}-{end_line} outside shown {shown_start}-{shown_end}",
        )
    if not _same_file(candidate_file, file):
        return FillParseFailure(
            REASON_FILE_MISMATCH, f"reply named {candidate_file!r}, expected {file!r}",
        )

    note = data.get("note")
    return FillProposal(
        file=candidate_file,
        start_line=start_line,
        end_line=end_line,
        replacement=data["replacement"],
        note=note,
        shown_start=shown_start,
        shown_end=shown_end,
        mtime=mtime,
        size=size,
    )


def _strip_single_fence(text: str) -> str:
    """Strip a leading ` ```[a-z]* ` line and a trailing ` ``` ` line,
    and nothing else -- only when the WHOLE reply is one fenced block.
    Models wrap JSON in fences constantly despite being told not to; a
    reply with commentary around the object is a reply that did not
    follow the instruction, and silently hunting for embedded JSON in
    prose teaches nobody anything (so that case is left to fail as
    `not_json` below, on purpose)."""
    stripped = text.strip()
    lines = stripped.split("\n")
    if len(lines) >= 2 and _FENCE_RE.match(lines[0].strip()) and lines[-1].strip() == "```":
        return "\n".join(lines[1:-1])
    return stripped


def _check_shape(data: Any) -> Optional[str]:
    """None if `data` has every required key, correctly typed. Otherwise
    the name of the first offending key, for the log.

    `type(x) is int` (not `isinstance`) deliberately: `bool` is a
    subclass of `int` in Python, and a model that returns `true` for
    `start_line` has not answered the question any more than one that
    returned `"12"` has."""
    if not isinstance(data, dict):
        return "root: not a JSON object"
    if type(data.get("file")) is not str or not data["file"].strip():
        return "file"
    if type(data.get("start_line")) is not int:
        return "start_line"
    if type(data.get("end_line")) is not int:
        return "end_line"
    if type(data.get("replacement")) is not str:
        # Empty is fine -- deleting a region is a legitimate proposal.
        # Missing, or the wrong type, is not.
        return "replacement"
    note = data.get("note")
    if note is not None and not isinstance(note, str):
        return "note"
    return None


def _same_file(candidate: str, expected: str) -> bool:
    """`expected` is the `EditorContext.file` the invocation was built
    against; `candidate` is whatever the model put in its reply's
    `file` key.

    A bare filename (no directory component) is compared by basename
    only -- the model will return `config.py` where the context said
    `D:\\Projects\\Point\\backend\\config.py`, and that is the intended
    accept. Anything WITH a directory component is compared as a fully
    resolved, normalized path, so `other_module/config.py` (a genuinely
    different file that happens to share a basename) is correctly
    rejected rather than waved through by a basename-only check. Always
    case-insensitive: this project runs on Windows only (CLAUDE.md), and
    Windows paths are case-insensitive on every filesystem it ships on.
    """
    candidate_path = Path(candidate.replace("\\", "/"))
    expected_path = Path(expected.replace("\\", "/"))

    if candidate_path.parent == Path("."):
        return candidate_path.name.lower() == expected_path.name.lower()
    return str(candidate_path.resolve()).lower() == str(expected_path.resolve()).lower()


def _excerpt(text: str) -> str:
    if len(text) <= _EXCERPT_CHARS:
        return text
    return text[:_EXCERPT_CHARS] + "…"
