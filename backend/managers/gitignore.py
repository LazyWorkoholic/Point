r"""A minimal .gitignore matcher for the project file index (B7).

This is deliberately not a spec-complete implementation of gitignore
semantics -- no nested .gitignore files, no `**` edge cases beyond the
common ones. It covers what index.respect_gitignore actually needs:
comments, blank lines, `!` negation, a leading `/` to anchor a pattern
to the project root, a trailing `/` for directory-only patterns, and
`*` / `?` / `**` globs. If real projects hit a gitignore feature this
does not cover, reach for the `pathspec` package rather than extending
this by hand -- that is a new dependency worth naming in a handoff, not
a reason to grow this file into a second gitignore parser.
"""

from __future__ import annotations

import re
from dataclasses import dataclass


@dataclass(frozen=True)
class IgnoreRule:
    regex: "re.Pattern[str]"
    negate: bool
    dir_only: bool
    anchored: bool


def parse(text: str) -> list[IgnoreRule]:
    rules: list[IgnoreRule] = []
    for raw_line in text.splitlines():
        line = raw_line.rstrip()
        if not line or line.lstrip().startswith("#"):
            continue
        negate = line.startswith("!")
        if negate:
            line = line[1:]
        dir_only = line.endswith("/") and not line.endswith("\\/")
        if dir_only:
            line = line[:-1]
        if not line:
            continue
        # A slash anywhere but the trailing one anchors the pattern to
        # where the .gitignore lives (git's own rule); no slash at all
        # means "match this name at any depth".
        anchored = "/" in line
        pattern = line[1:] if line.startswith("/") else line
        rules.append(IgnoreRule(re.compile(_translate(pattern)), negate, dir_only, anchored))
    return rules


def is_ignored(rel_posix: str, is_dir: bool, rules: list[IgnoreRule]) -> bool:
    """Later rules win, matching git's own precedence. A `!` rule can
    only un-ignore what an earlier rule ignored."""
    ignored = False
    segments = rel_posix.split("/")
    for rule in rules:
        if rule.dir_only and not is_dir:
            continue
        if rule.anchored:
            hit = rule.regex.match(rel_posix) is not None
        else:
            hit = any(rule.regex.match(seg) for seg in segments)
        if hit:
            ignored = not rule.negate
    return ignored


def _translate(pattern: str) -> str:
    """Glob -> regex, anchored at both ends. `**` crosses `/`; `*` and
    `?` do not."""
    out = ["^"]
    i, n = 0, len(pattern)
    while i < n:
        c = pattern[i]
        if c == "*":
            if i + 1 < n and pattern[i + 1] == "*":
                out.append(".*")
                i += 2
                continue
            out.append("[^/]*")
        elif c == "?":
            out.append("[^/]")
        else:
            out.append(re.escape(c))
        i += 1
    out.append("$")
    return "".join(out)
