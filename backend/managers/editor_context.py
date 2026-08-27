r"""EditorContext: what the Filler-Explainer needs beyond STM
(project_idea.md §7's `context: STM + EditorContext` row), plus the
backend's own enclosing-scope extraction (B11).

Mirrors F11/F15's Go `EditorContext` by name and by field, on purpose --
the two are the same concept crossing the process boundary, the same
way `Tool.to_schema()` crosses into `providers\`. **The frontend's
version wins when supplied** (`enclosing` non-None): it knows the real
cursor. This module's `resolve_scope()` is the fallback for everyone
who does not have a real editor yet -- today, that is `cli.py`.

**No parser.** Nearest enclosing `def`/`class` (indentation) or
`func`/`class ... {` (brace matching), capped at ~2k tokens; past the
cap, or when nothing encloses the target line at all, falls back to
the target line +/- 20. One highlighted line without its function is
unanswerable -- see B11's completed doc for the failure cases this
heuristic actually hit.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Optional

MODE_EXPLAIN = "explain"
MODE_FILL = "fill"

# Same 4-chars-per-token approximation B8/B10 already use everywhere
# else a token count is estimated. Not imported from managers.context:
# that module imports *this* one (to render an EditorContext into the
# task_state section), and the other direction would be circular.
_ENCLOSING_TOKEN_CAP = 2000
_FALLBACK_MARGIN = 20

_DEF_RE = re.compile(r"^\s*(async\s+)?(def|class)\s+\w")
_BRACE_HEADER_RE = re.compile(r"^\s*(func|class|type)\b")


@dataclass(frozen=True)
class EditorContext:
    file: str
    mode: str  # MODE_EXPLAIN | MODE_FILL -- decided by the invocation site
    language: Optional[str] = None
    cursor: Optional[int] = None                  # 1-based line number
    selection: Optional[tuple[int, int]] = None    # 1-based, inclusive
    enclosing: Optional[tuple[int, int]] = None     # supplied by a real editor; None => computed here
    imports: tuple[str, ...] = field(default_factory=tuple)

    def to_dict(self) -> dict[str, Any]:
        return {
            "file": self.file,
            "mode": self.mode,
            "language": self.language,
            "cursor": self.cursor,
            "selection": list(self.selection) if self.selection else None,
            "enclosing": list(self.enclosing) if self.enclosing else None,
            "imports": list(self.imports),
        }

    @staticmethod
    def from_dict(d: dict[str, Any]) -> "EditorContext":
        selection = d.get("selection")
        enclosing = d.get("enclosing")
        return EditorContext(
            file=d["file"],
            mode=d.get("mode") or MODE_EXPLAIN,
            language=d.get("language"),
            cursor=d.get("cursor"),
            selection=tuple(selection) if selection else None,
            enclosing=tuple(enclosing) if enclosing else None,
            imports=tuple(d.get("imports") or ()),
        )


# ---------------------------------------------------------------------------
# Enclosing-scope extraction -- Tier 1, no parser
# ---------------------------------------------------------------------------

def resolve_scope(
    lines: list[str],
    *,
    selection: Optional[tuple[int, int]],
    cursor: Optional[int],
    enclosing: Optional[tuple[int, int]],
) -> tuple[int, int]:
    """1-based inclusive (start, end) to render.

    `enclosing`, if given, wins outright -- a real editor (F11/F15)
    already computed it against the actual buffer, which is more
    accurate than anything re-derived from a text dump here.
    """
    if enclosing is not None:
        return _clamp(enclosing, len(lines))
    target = (selection[0] if selection else cursor) or 1
    found = _find_enclosing(lines, target)
    if found is not None:
        start, end = found
        chars = sum(len(l) for l in lines[start - 1:end])
        if chars // 4 <= _ENCLOSING_TOKEN_CAP:
            return found
    return _fallback_range(len(lines), selection, cursor)


def _find_enclosing(lines: list[str], target_line: int) -> Optional[tuple[int, int]]:
    n = len(lines)
    if n == 0:
        return None
    target_idx = max(0, min(target_line - 1, n - 1))

    ref_idx = target_idx
    while ref_idx < n and not lines[ref_idx].strip():
        ref_idx += 1
    if ref_idx >= n:
        ref_idx = target_idx
    ref_indent = _indent_of(lines[ref_idx])

    if _opens_scope(lines[ref_idx]):
        header_idx = ref_idx
        header_indent = ref_indent
    else:
        header_idx = None
        header_indent = None
        idx = ref_idx - 1
        while idx >= 0:
            line = lines[idx]
            if line.strip() and _opens_scope(line) and _indent_of(line) < ref_indent:
                header_idx = idx
                header_indent = _indent_of(line)
                break
            idx -= 1

    if header_idx is None:
        return None

    if _brace_style(lines[header_idx]):
        end_idx = _match_brace_end(lines, header_idx)
    else:
        end_idx = _indent_style_end(lines, header_idx, header_indent)
    return (header_idx + 1, end_idx + 1)


def _opens_scope(line: str) -> bool:
    stripped = line.strip()
    if not stripped:
        return False
    if _DEF_RE.match(line):
        return True
    if stripped.endswith("{") and (
        _BRACE_HEADER_RE.match(line) or re.search(r"\b(function|class)\b", stripped)
    ):
        return True
    return False


def _brace_style(line: str) -> bool:
    return line.rstrip().endswith("{")


def _match_brace_end(lines: list[str], header_idx: int) -> int:
    depth = 0
    started = False
    for i in range(header_idx, len(lines)):
        for ch in lines[i]:
            if ch == "{":
                depth += 1
                started = True
            elif ch == "}":
                depth -= 1
        if started and depth <= 0:
            return i
    return len(lines) - 1


def _indent_style_end(lines: list[str], header_idx: int, header_indent: int) -> int:
    end_idx = header_idx
    for i in range(header_idx + 1, len(lines)):
        line = lines[i]
        if not line.strip():
            end_idx = i
            continue
        if _indent_of(line) <= header_indent:
            break
        end_idx = i
    while end_idx > header_idx and not lines[end_idx].strip():
        end_idx -= 1
    return end_idx


def _indent_of(line: str) -> int:
    return len(line) - len(line.lstrip(" \t"))


def _fallback_range(
    n: int, selection: Optional[tuple[int, int]], cursor: Optional[int]
) -> tuple[int, int]:
    if selection:
        start, end = selection
    elif cursor:
        start = end = cursor
    else:
        start = end = 1
    return _clamp((start - _FALLBACK_MARGIN, end + _FALLBACK_MARGIN), n)


def _clamp(rng: tuple[int, int], n: int) -> tuple[int, int]:
    start, end = rng
    start = max(1, start)
    end = min(max(n, 1), end)
    if end < start:
        end = start
    return (start, end)
