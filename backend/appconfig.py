r"""Where Point keeps its files, and how a key is found.

Nothing under `backend\` is a storage location any more (B6 12). The
split is deliberate:

    config      %APPDATA%\Point\config.toml       user-owned, NEVER rewritten
    credentials %APPDATA%\Point\credentials.toml  keys only, gitignored
    state       %LOCALAPPDATA%\Point\state.json   machine-written
    database    %LOCALAPPDATA%\Point\runtime.db
    logs        %LOCALAPPDATA%\Point\logs\point.log

config.toml is user-owned: it holds only non-secret settings, so it is
safe to share, commit, or screenshot. Everything the machine writes for
itself goes to LOCALAPPDATA, which is not roamed and not backed up.

Key resolution is env var -> credentials.toml -> not found. There is no
third step: prompting is the frontend's job, and this module never asks
anyone anything.
"""

from __future__ import annotations

import os
import sys
import tomllib
from pathlib import Path
from typing import Any, Optional

APP_NAME = "Point"

# Every secret value this process has resolved. logging_setup's redactor
# reads it, so a key cannot reach a log record verbatim no matter which
# f-string or third-party error message it travelled inside.
_KNOWN_SECRETS: set[str] = set()

_KEY_ENV_VARS = {
    "anthropic": "ANTHROPIC_API_KEY",
    "openai": "OPENAI_API_KEY",
    "gemini": "GEMINI_API_KEY",
    "openrouter": "OPENROUTER_API_KEY",
}


# ---------------------------------------------------------------------------
# Directories
# ---------------------------------------------------------------------------

def config_dir() -> Path:
    """User-owned configuration. Roams with the profile on Windows."""
    if sys.platform == "win32":
        base = os.environ.get("APPDATA") or str(Path.home() / "AppData" / "Roaming")
    else:
        base = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return Path(base) / APP_NAME


def state_dir() -> Path:
    """Machine-written state. Never roams, never needs backing up."""
    if sys.platform == "win32":
        base = os.environ.get("LOCALAPPDATA") or str(Path.home() / "AppData" / "Local")
    else:
        base = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
    return Path(base) / APP_NAME


def config_path() -> Path:
    return config_dir() / "config.toml"


def credentials_path() -> Path:
    return config_dir() / "credentials.toml"


def state_path() -> Path:
    return state_dir() / "state.json"


def log_path() -> Path:
    return state_dir() / "logs" / "point.log"


def lock_path() -> Path:
    return state_dir() / "point.lock"


def default_db_path() -> Path:
    return state_dir() / "runtime.db"


def ensure_dirs() -> None:
    """Create every directory Point writes to. Safe to call repeatedly."""
    config_dir().mkdir(parents=True, exist_ok=True)
    state_dir().mkdir(parents=True, exist_ok=True)
    log_path().parent.mkdir(parents=True, exist_ok=True)


def is_writable(directory: Path) -> bool:
    """The probe name carries this process's pid: a fixed name means two
    Point processes starting at the same moment delete each other's probe
    and both conclude the directory is read-only. Two processes starting
    at once is not an edge case here -- it is the CLI beside a running
    backend, which is the whole reason WAL is on."""
    probe = directory / f".point-write-probe-{os.getpid()}"
    try:
        directory.mkdir(parents=True, exist_ok=True)
        probe.write_text("", encoding="utf-8")
        return True
    except OSError:
        return False
    finally:
        try:
            probe.unlink()
        except OSError:
            pass


# ---------------------------------------------------------------------------
# config.toml  (read-only, always)
# ---------------------------------------------------------------------------

def load_config() -> dict[str, Any]:
    """Returns {} when there is no config at all -- that is a supported
    state, not an error. Point must boot from nothing."""
    return _load_toml(config_path())


def config_value(dotted_key: str, default: Any = None) -> Any:
    """`config_value("database.path")` -> config.toml [database] path."""
    node: Any = load_config()
    for part in dotted_key.split("."):
        if not isinstance(node, dict) or part not in node:
            return default
        node = node[part]
    return node


# B32: the editor's own size cap, deliberately separate from
# `index.max_file_kb`.
#
# `index.max_file_kb` (default 100) prices `@file` attachments against a
# token budget and bounds index build time; it must not move. The editor
# wants a different number for a different reason: a 400KB file is
# reasonable to EDIT and unreasonable to ATTACH.
#
# ONE number governs the editor's read and its write, through this one
# accessor, because two numbers would mean a file you can open is a file
# you cannot save.
#
# 512 is CHOSEN, NOT MEASURED. It clears F13's 5,000-line Done-when with
# headroom. It is not a performance figure and must not be described as
# one.
#
# Not in `appconfig_overrides.ALLOWED_KEYS` on purpose: that set is the
# five keys the Behaviour tab controls live, and B32 is not the episode
# that expands B26's surface.
EDITOR_MAX_FILE_KB_DEFAULT = 512


def editor_max_file_kb() -> int:
    """config.toml `[editor] max_file_kb`, else 512."""
    raw = config_value("editor.max_file_kb", EDITOR_MAX_FILE_KB_DEFAULT)
    try:
        value = int(raw)
    except (TypeError, ValueError):
        return EDITOR_MAX_FILE_KB_DEFAULT
    return value if value > 0 else EDITOR_MAX_FILE_KB_DEFAULT


def _strip_bom(raw: bytes) -> bytes:
    """tomllib rejects a UTF-8 BOM outright ("Invalid statement (at line
    1, column 1)") -- it does not strip one, by design. Users get a BOM
    for free from Notepad and from Windows PowerShell's
    `Set-Content -Encoding utf8` (which, on 5.1, always means UTF-8 *with*
    BOM), so a config.toml saved by either currently failed to parse with
    no visible error anywhere. Stripping it here matches what every real
    editor considers "still valid UTF-8"."""
    return raw[3:] if raw.startswith(b"\xef\xbb\xbf") else raw


def _load_toml(path: Path) -> dict[str, Any]:
    """{} when there is no config at all -- that is a supported state,
    not an error. A parse failure also returns {} so a broken config
    cannot stop the app from starting, but it is no longer *silent*: it
    is logged, the same way every other failure in this project is
    visible rather than swallowed. `config_error()` is doctor's
    equivalent of this same check."""
    try:
        raw = path.read_bytes()
    except FileNotFoundError:
        return {}
    except OSError as exc:
        _warn_toml_failure(path, exc)
        return {}
    try:
        return tomllib.loads(_strip_bom(raw).decode("utf-8"))
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as exc:
        _warn_toml_failure(path, exc)
        return {}


def _warn_toml_failure(path: Path, exc: Exception) -> None:
    # Deferred import: logging_setup imports appconfig itself (for
    # log_path()), so a top-level import here would be circular.
    from logging_setup import get_logger

    get_logger("appconfig").warning("Could not parse %s: %s", path, exc)


def config_error() -> Optional[str]:
    """The parse error for config.toml, if it has one. doctor only --
    shares _strip_bom with _load_toml so the two never disagree about
    whether a BOM'd-but-otherwise-valid file counts as broken."""
    path = config_path()
    if not path.exists():
        return None
    try:
        tomllib.loads(_strip_bom(path.read_bytes()).decode("utf-8"))
    except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError) as exc:
        return str(exc)
    return None


# ---------------------------------------------------------------------------
# Secrets
# ---------------------------------------------------------------------------

def resolve_api_key(provider: str) -> Optional[str]:
    """env var -> credentials.toml -> None. No prompting, ever."""
    env_var = _KEY_ENV_VARS.get(provider)
    if env_var:
        value = os.environ.get(env_var)
        if value:
            return register_secret(value)

    section = _load_toml(credentials_path()).get(provider)
    if isinstance(section, dict):
        value = section.get("api_key")
        if isinstance(value, str) and value:
            return register_secret(value)
    return None


def key_source(provider: str) -> Optional[str]:
    """Where the key came from, for doctor. Never returns the key."""
    env_var = _KEY_ENV_VARS.get(provider)
    if env_var and os.environ.get(env_var):
        return f"env:{env_var}"
    section = _load_toml(credentials_path()).get(provider)
    if isinstance(section, dict) and section.get("api_key"):
        return "credentials.toml"
    return None


def known_providers() -> list[str]:
    return list(_KEY_ENV_VARS)


def save_api_key(provider: str, api_key: str) -> Path:
    """Temp file, then rename -- an interrupted write leaves the previous
    credentials intact rather than a half-written file.

    On Windows there is no chmod. The ACL is restricted to the current
    user with icacls when that is available; when it is not, the file
    inherits the user-profile directory's permissions, which is
    acceptable for v1 and is what `doctor` reports.

    Called by Runtime._handle_set_provider_api_key (B25), after the
    submitted key has already validated via health().
    """
    ensure_dirs()
    target = credentials_path()
    existing = _load_toml(target)
    section = existing.get(provider)
    if not isinstance(section, dict):
        section = {}
    section["api_key"] = api_key
    existing[provider] = section

    temp = target.with_name(target.name + ".tmp")
    temp.write_text(_dump_toml(existing), encoding="utf-8")
    os.replace(temp, target)
    _restrict_to_current_user(target)
    register_secret(api_key)
    return target


def _dump_toml(data: dict[str, Any]) -> str:
    """Minimal writer: credentials.toml is only ever [provider] api_key
    pairs, so a full TOML serializer would be a dependency bought for
    nothing -- tomllib reads, and nothing in the stdlib writes."""
    lines = [
        "# Written by Point. Keys only.",
        "# Never put a key in config.toml -- that file is meant to be shareable.",
        "",
    ]
    for section, values in sorted(data.items()):
        if not isinstance(values, dict):
            continue
        lines.append(f"[{section}]")
        for key, value in sorted(values.items()):
            if isinstance(value, str):
                lines.append(f'{key} = "{_escape_toml(value)}"')
        lines.append("")
    return "\n".join(lines)


def _escape_toml(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"')


def _restrict_to_current_user(path: Path) -> None:
    if sys.platform != "win32":
        try:
            path.chmod(0o600)
        except OSError:
            pass
        return
    import subprocess

    user = os.environ.get("USERNAME")
    if not user:
        return
    try:
        subprocess.run(
            ["icacls", str(path), "/inheritance:r", "/grant:r", f"{user}:F"],
            capture_output=True, check=False, timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        pass  # best effort -- doctor reports which fallback applied


# ---------------------------------------------------------------------------
# Masking
# ---------------------------------------------------------------------------

def mask_secret(value: Optional[str]) -> str:
    """`sk-...4f2a`. No code path may emit a raw key -- logs, error
    messages, and any payload sent to Go all go through here."""
    if not value:
        return "(none)"
    if len(value) <= 8:
        return "..." + value[-2:]
    return f"{value[:3]}...{value[-4:]}"


def register_secret(value: str) -> str:
    """Remember a resolved secret so the log redactor catches it even
    when it is buried inside someone else's error string."""
    if value and len(value) >= 8:
        _KNOWN_SECRETS.add(value)
    return value


def known_secrets() -> list[str]:
    return list(_KNOWN_SECRETS)
