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


def config_value_live(dotted_key: str, default: Any = None) -> Any:
    present, value = get_override(dotted_key)
    if present:
        return value
    return appconfig.config_value(dotted_key, default)
