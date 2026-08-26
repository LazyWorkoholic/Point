r"""The first logging in this backend. stderr only, plus a rotating file.

Stdout is the protocol channel -- one stray print() there corrupts the
JSON-lines stream Go is parsing, so this module never touches it.

Every record passes through the redactor, so a key cannot reach the log
even when a provider SDK embeds it in an exception message.
"""

from __future__ import annotations

import logging
import re
import sys
from logging.handlers import RotatingFileHandler
from typing import Optional

import appconfig

_MAX_BYTES = 2 * 1024 * 1024
_BACKUP_COUNT = 3
_configured = False

# Shapes that are keys no matter who produced them. The known-secret list
# in appconfig catches the rest; this catches keys we never resolved
# ourselves (a user pasting one into a prompt, an SDK echoing a header).
_KEY_PATTERNS = [
    re.compile(r"sk-ant-[A-Za-z0-9_\-]{8,}"),
    re.compile(r"sk-or-v1-[A-Za-z0-9_\-]{8,}"),
    re.compile(r"sk-[A-Za-z0-9_\-]{16,}"),
    re.compile(r"AIza[A-Za-z0-9_\-]{20,}"),
]


class RedactingFilter(logging.Filter):
    """Rewrites the formatted message rather than the args, because the
    interesting case is a key that arrived already interpolated into a
    third-party exception string."""

    def filter(self, record: logging.LogRecord) -> bool:
        try:
            message = record.getMessage()
        except Exception:  # noqa: BLE001 - a broken format string must not kill logging
            return True
        redacted = redact(message)
        if redacted != message:
            record.msg = redacted
            record.args = ()
        return True


def redact(text: str) -> str:
    for secret in appconfig.known_secrets():
        if secret in text:
            text = text.replace(secret, appconfig.mask_secret(secret))
    for pattern in _KEY_PATTERNS:
        text = pattern.sub(lambda m: appconfig.mask_secret(m.group(0)), text)
    return text


def configure_logging(verbose: bool = False, to_file: bool = True) -> logging.Logger:
    """Idempotent: calling it twice does not double every line."""
    global _configured
    root = logging.getLogger("point")
    if _configured:
        root.setLevel(logging.DEBUG if verbose else logging.INFO)
        return root

    root.setLevel(logging.DEBUG if verbose else logging.INFO)
    root.propagate = False
    formatter = logging.Formatter(
        "%(asctime)s %(levelname)-7s %(name)s: %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    redactor = RedactingFilter()

    stderr_handler = logging.StreamHandler(sys.stderr)
    stderr_handler.setLevel(logging.DEBUG if verbose else logging.WARNING)
    stderr_handler.setFormatter(formatter)
    stderr_handler.addFilter(redactor)
    root.addHandler(stderr_handler)

    if to_file:
        path = appconfig.log_path()
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            file_handler = RotatingFileHandler(
                path, maxBytes=_MAX_BYTES, backupCount=_BACKUP_COUNT, encoding="utf-8"
            )
            file_handler.setLevel(logging.DEBUG)
            file_handler.setFormatter(formatter)
            file_handler.addFilter(redactor)
            root.addHandler(file_handler)
        except OSError as exc:
            # An unwritable log directory is not a reason to refuse to run.
            root.warning("File logging disabled: %s", exc)

    _configured = True
    return root


def get_logger(name: Optional[str] = None) -> logging.Logger:
    return logging.getLogger(f"point.{name}" if name else "point")
