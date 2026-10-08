"""In-memory overrides for the handful of `config.toml` values the
Behaviour tab controls (B26).

This is not a general config-override mechanism and must never become
one: `ALLOWED_KEYS` (plus the `limits.<provider>.max_concurrent_tasks`
family) is the whole list a client may set. An unlisted key is rejected,
not silently accepted, so a frontend typo fails loudly.

Overrides live exactly as long as this process does -- nothing here ever
touches disk. `appconfig.py` says "Settings never writes config.toml";
this module is the other half of that promise: Settings has somewhere to
put a change that is not config.toml, and a restart falls back to the
user's real file, not to a shadow copy of an override.
"""

from __future__ import annotations

from typing import Any

import appconfig

_OVERRIDES: dict[str, Any] = {}

# The only keys the Behaviour tab controls live. Not a general
# config-override mechanism -- an unlisted key is rejected, not silently
# accepted, so a frontend typo fails loudly instead of being ignored.
ALLOWED_KEYS = {
    "chat.queue_depth",
    "limits.max_concurrent_tasks",
    "project.allow_outside_project",
    "index.respect_gitignore",
    "research.stale_after_days",
    # Per-provider caps are a family, not a fixed set of keys -- see
    # _provider_cap_name() below.
}

_BOOL_KEYS = {
    "project.allow_outside_project",
    "index.respect_gitignore",
}

_PROVIDER_CAP_PREFIX = "limits."
_PROVIDER_CAP_SUFFIX = ".max_concurrent_tasks"


def _provider_cap_name(key: str) -> bool:
    """True when `key` is `limits.<provider>.max_concurrent_tasks` for a
    provider `appconfig.known_providers()` plus "ollama" would recognize."""
    if not (key.startswith(_PROVIDER_CAP_PREFIX) and key.endswith(_PROVIDER_CAP_SUFFIX)):
        return False
    provider = key[len(_PROVIDER_CAP_PREFIX):-len(_PROVIDER_CAP_SUFFIX)]
    if not provider:
        return False
    known = set(appconfig.known_providers()) | {"ollama"}
    return provider in known


def set_override(key: str, value: Any) -> None:
    """Raises `ValueError` for an unlisted key, or a value that does not
    match the shape every call site for that key already expects --
    `int` for a count/depth, `bool` for a flag. Reusing each call site's
    own parsing is explicitly out of scope: this validates shape only,
    the same way a `config.toml` value of the right TOML type would
    arrive already shaped. Range/clamping stays each call site's job."""
    if key not in ALLOWED_KEYS and not _provider_cap_name(key):
        raise ValueError(f"Unknown behaviour override key: {key!r}")

    if key in _BOOL_KEYS:
        if not isinstance(value, bool):
            raise ValueError(f"{key} must be a boolean (got {value!r})")
        _OVERRIDES[key] = value
        return

    try:
        parsed = int(value)
    except (TypeError, ValueError):
        raise ValueError(f"{key} must be an integer (got {value!r})")
    _OVERRIDES[key] = parsed


def clear_override(key: str) -> None:
    _OVERRIDES.pop(key, None)


def get_override(key: str) -> tuple[bool, Any]:
    """(present, value). A tri-state lookup, not `.get(key, default)` --
    the caller needs to distinguish "not overridden" from "overridden to
    a falsy value" (0 is a legitimate queue depth)."""
    if key in _OVERRIDES:
        return True, _OVERRIDES[key]
    return False, None


def all_overrides() -> dict[str, Any]:
    """For the doctor report -- which values are overridden, read-only."""
    return dict(_OVERRIDES)


# The hardcoded fallback each of the five ALLOWED_KEYS' own real call
# site already falls back to when config.toml has no value for it --
# duplicated here, deliberately, not imported from each call site
# (scheduler.py's DEFAULT_MAX_CONCURRENT / turn_queue_depth's own literal
# `1`, runtime.py's `7` and two call sites' `False`, project_manager.py's
# `True`), because none of those four modules exists to be a shared
# source of truth for a doctor-only reporting need, and importing from
# all four to save one constant each would be a worse coupling than the
# duplication. If any of these five changes, its own call site AND this
# line must change together -- the same discipline credential.go's
# pasteSettleWindow already documents, frontend-side, for an identical
# cross-boundary duplication ("this package cannot import chat... so the
# value is reproduced rather than shared").
RESOLVED_DEFAULTS: dict[str, Any] = {
    "chat.queue_depth": 1,                    # scheduler.py:269 turn_queue_depth()
    "limits.max_concurrent_tasks": 3,         # scheduler.py:68 DEFAULT_MAX_CONCURRENT
    "project.allow_outside_project": False,   # context.py:843, runtime.py:1393
    "index.respect_gitignore": True,          # project_manager.py:205
    "research.stale_after_days": 7,           # runtime.py:1025
}


def resolved_behaviour_settings() -> dict[str, dict[str, Any]]:
    """For GetDoctorReport (B27): each of the five Behaviour-tab keys'
    actual config.toml value -- or RESOLVED_DEFAULTS' own fallback when
    config.toml has none -- plus whether an override currently masks it.

    Deliberately NOT config_value_live()'s own resolution: that function
    already applies the override, which is the one thing this is here to
    look PAST. "What does config.toml say, independent of anything
    layered on top of it" and "what governs the next real decision" are
    two different questions; config_value_live() already answers the
    second one everywhere it's called. This answers the first, plus a
    separate bit for whether the second currently differs from it.

    Scoped to exactly these five keys -- NOT the
    limits.<provider>.max_concurrent_tasks family _provider_cap_name()
    also recognizes. F10b's own Behaviour tab exposes only the general
    cap (its Decision 3); this field mirrors that tab's five rows
    exactly, not every key SetBehaviourOverride will accept.
    """
    out: dict[str, dict[str, Any]] = {}
    for key, default in RESOLVED_DEFAULTS.items():
        present, _ = get_override(key)
        out[key] = {
            "resolved": appconfig.config_value(key, default),
            "overridden": present,
        }
    return out


def config_value_live(dotted_key: str, default: Any = None) -> Any:
    present, value = get_override(dotted_key)
    if present:
        return value
    return appconfig.config_value(dotted_key, default)
