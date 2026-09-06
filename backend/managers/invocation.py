r"""Invocation: what the user typed, as a structure (B12).

The one rule this file exists to enforce is that parsing returns a
**structured Invocation, never a half-parsed string**. Before B12 the
CLI did its own `@`-prefix strip (`cli.py:_strip_agent_prefix`) and
handed the remainder onward as text -- which works for exactly one case
and has nowhere to put a second one. A path, an agent, a flag, and the
message itself are four different things, and a function that returns
"the rest of the string" can only ever carry one of them.

The grammar, in full:

    plain text            -> a TURN, inline, on the session's current agent
    @agent rest           -> a TASK, agent resolved by name
    @path\to\file         -> an attachment reference
    @C:\... @~\... @..\   -> an attachment reference, filesystem mode
    @agent --advanced     -> parsed, `advanced` set, and REFUSED by the
                             caller (`AdvancedUnavailableError`)

**Ambiguity rule: an exact agent name wins; otherwise it is a path.**
This is F8's own rule, and it is decided here so the picker and the
typed form cannot disagree. `@research` with an agent called `research`
is that agent even if a file named `research` sits beside you; rename
one or the other, because guessing per-invocation is worse than a rule
you can state in a sentence.

Both `\` and `/` are accepted in typed paths. Users type both on
Windows, `resolve_path()` normalizes anyway, and refusing one of them
would be a rule invented here that exists nowhere else in the product.

What this file does NOT do: resolve anything. It takes predicates --
"is this a known agent", "is this in the project index" -- and asks
them. It never touches the database, the filesystem, or the agent
registry. That is what makes it testable with a dict, and what stops
the routing rule from quietly acquiring an I/O dependency.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable, Optional


class InvocationKind(str, Enum):
    """A TURN streams inline into the conversation; a TASK runs in its
    own lane and never enters the session's turn queue.

    The distinction is the whole reason B12 has two queues: three
    background research runs must not be able to block the user from
    asking a plain question, and a plain question must not be able to
    jump ahead of research the user started first.
    """

    TURN = "turn"
    TASK = "task"


@dataclass(frozen=True)
class AttachmentRef:
    r"""One `@`-token that resolved to a path rather than an agent.

    `raw` is exactly what the user typed, separators and all -- kept
    because it is what a refusal message has to quote back at them. A
    refusal that names a normalized path the user never typed reads as
    a refusal of something else.
    """

    raw: str
    filesystem: bool = False   # True for @C:\..., @~\..., @..\..., @./...


# `--advanced` counts only when it stands alone as a token.
# `--advanced-mode` is not it, and neither is the word inside a
# sentence -- refusing on a substring match would make the word
# unusable in prose.
_ADVANCED = "--advanced"

# A token is an `@` followed by anything that is not whitespace.
_TOKEN = re.compile(r"@(\S+)")

# What makes a token look like a path rather than a bare name, checked
# only AFTER the exact-agent-name rule has had its turn.
_DRIVE = re.compile(r"^[A-Za-z]:[\\/]")


@dataclass(frozen=True)
class Invocation:
    """The parsed form of one thing the user typed."""

    kind: InvocationKind
    text: str
    raw: str
    agent: Optional[str] = None
    attachments: tuple[AttachmentRef, ...] = field(default_factory=tuple)
    advanced: bool = False

    @property
    def is_task(self) -> bool:
        return self.kind is InvocationKind.TASK

    def to_dict(self) -> dict[str, Any]:
        return {
            "kind": self.kind.value,
            "text": self.text,
            "raw": self.raw,
            "agent": self.agent,
            "attachments": [
                {"raw": a.raw, "filesystem": a.filesystem} for a in self.attachments
            ],
            "advanced": self.advanced,
        }


def looks_like_path(token: str) -> bool:
    r"""True for anything with path shape: a separator either way, a
    drive letter, a `~`, a bare `.`/`..`, or a filename with an
    extension.

    Deliberately generous. This runs only after the exact-agent-name
    rule has already claimed everything it wants, so the cost of a false
    positive is a clean "no such file" refusal naming the token -- while
    a false negative silently swallows an `@` the user meant as a path
    and treats it as prose.
    """
    if not token:
        return False
    if "/" in token or "\\" in token:
        return True
    if _DRIVE.match(token):
        return True
    if token.startswith("~"):
        return True
    if token in (".", ".."):
        return True
    # `main.py`, but not `v1.` and not a bare `.hidden` (no extension).
    stem, dot, ext = token.rpartition(".")
    return bool(stem and dot and ext and ext.isalnum())


def is_filesystem_path(token: str) -> bool:
    r"""True for the forms that name a location on disk directly rather
    than one relative to the project: `C:\...`, `~\...`, `..\...`,
    `.\...`, and absolute `/...` or `\...`.

    The distinction matters because these are the ones that can land
    outside the project root, and the guard has to be told to expect
    that rather than discovering it.
    """
    if not token:
        return False
    return bool(
        _DRIVE.match(token)
        or token.startswith("~")
        or token.startswith(("../", "..\\", "./", ".\\"))
        or token.startswith(("/", "\\"))
    )


def parse_invocation(
    raw: str,
    *,
    is_agent: Optional[Callable[[str], bool]] = None,
    is_indexed_path: Optional[Callable[[str], bool]] = None,
) -> Invocation:
    r"""Parse one line of typed input.

    `is_agent(name)` must answer with an EXACT match only -- no prefix
    matching, no fuzzy matching. That is what makes the ambiguity rule
    ("an exact agent name wins") a rule rather than a heuristic, and it
    is why resolution is injected instead of performed here.

    `is_indexed_path(token)` is the B7 project index: a token with no
    separator and no extension (`@makefile`) is still an attachment if
    the index knows it. Optional -- without it, only path-SHAPED tokens
    become attachments, which is the correct conservative answer when
    no project is open.

    Only the FIRST `@`-token can name the agent. A second `@agent` later
    in the sentence is prose about an agent, not a second routing
    decision: there is no such thing as running one message on two
    agents.
    """
    text = raw.strip()
    advanced = _ADVANCED in text.split()
    if advanced:
        # Removed from the text so the model never sees a flag meant for
        # us. It is recorded on the Invocation; the caller is what
        # refuses.
        text = " ".join(t for t in text.split() if t != _ADVANCED)

    agent: Optional[str] = None
    attachments: list[AttachmentRef] = []

    tokens = list(_TOKEN.finditer(text))
    for index, match in enumerate(tokens):
        token = _strip_trailing_punctuation(match.group(1))
        if not token:
            continue
        # The ambiguity rule, applied exactly once, to the first token.
        if index == 0 and agent is None and is_agent is not None and is_agent(token):
            agent = token
            continue
        if looks_like_path(token) or (is_indexed_path is not None and is_indexed_path(token)):
            attachments.append(
                AttachmentRef(raw=token, filesystem=is_filesystem_path(token))
            )

    if agent is not None:
        # Only the agent token is stripped, and only when it led the
        # line -- mirroring what `cli.py` already did, so nothing that
        # worked before B12 parses differently after it. Attachment
        # tokens stay in the text on purpose: "@main.py what does this
        # do" reads as a sentence to the model, and deleting the noun
        # out of the user's own question to leave "what does this do" is
        # a worse message than the one they wrote.
        text = _TOKEN.sub("", text, count=1).strip()

    kind = InvocationKind.TASK if agent is not None else InvocationKind.TURN
    return Invocation(
        kind=kind,
        text=text,
        raw=raw,
        agent=agent,
        attachments=tuple(attachments),
        advanced=advanced,
    )


def _strip_trailing_punctuation(token: str) -> str:
    r"""`@research, and also...` names `research`, not `research,`.

    Only sentence punctuation is stripped, and only from the end. A
    trailing `\` or `/` is left alone: it is how a user says "this is a
    directory", which the attachment guard refuses with a listing offer
    -- and stripping it would turn that clear refusal into a confusing
    "no such file".
    """
    return token.rstrip(",;:!?\"')")
