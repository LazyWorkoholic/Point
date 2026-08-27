r"""429 handling: exponential backoff with jitter, respecting Retry-After
(B10, the additive change B4's handoff parked as D56).

A rate limit is a **retry, not a failure**. Before this module a burst of
concurrent tasks -- which is exactly what a bounded tool loop produces --
surfaced as a scatter of unrelated-looking TaskFailed events, and the only
way to tell "the provider is throttling you" from "the provider is broken"
was to read the error strings.

Two rules shape the whole file:

  * **Only a stream that has produced nothing yet may be retried.** Once a
    token has reached the accumulator, restarting the stream would replay
    text the user already saw. A 429 mid-stream is therefore a genuine
    failure, and is re-raised. In practice providers reject at connect
    time, so this costs nothing real and removes the duplication bug
    entirely.
  * **Retry-After wins over the computed backoff, always.** The server
    knows when it will serve you; the formula is only for when it does not
    say.
"""

from __future__ import annotations

import asyncio
import random
from typing import Any, AsyncIterator, Awaitable, Callable, Optional

from logging_setup import get_logger
from providers.errors import RateLimitError

_log = get_logger("providers.retry")

# The exact backoff parameters, recorded here rather than scattered as
# literals so B10's handoff can quote one place.
#
#   attempt 1 fails -> sleep ~1s     attempt 3 fails -> sleep ~4s
#   attempt 2 fails -> sleep ~2s     attempt 4 fails -> sleep ~8s
#
# then give up. Jitter is +/-25% of the computed delay, applied
# multiplicatively, so N tasks throttled by the same burst do not all
# wake up in the same millisecond and re-throttle each other.
MAX_ATTEMPTS = 5
BASE_DELAY_S = 1.0
FACTOR = 2.0
JITTER = 0.25
MAX_DELAY_S = 60.0


def next_delay(attempt: int, retry_after: Optional[float] = None) -> float:
    """Delay before attempt number `attempt + 1` (attempt is 1-based).

    `retry_after` short-circuits the formula but is still capped: a
    misconfigured proxy answering `Retry-After: 86400` must not park a
    task for a day.
    """
    if retry_after is not None and retry_after >= 0:
        return min(float(retry_after), MAX_DELAY_S)
    raw = BASE_DELAY_S * (FACTOR ** (attempt - 1))
    jittered = raw * random.uniform(1.0 - JITTER, 1.0 + JITTER)
    return min(jittered, MAX_DELAY_S)


def retry_after_seconds(exc: BaseException) -> Optional[float]:
    """Pull Retry-After off whatever the SDK handed us.

    Normalized RateLimitError carries it as an attribute when the adapter
    could find it; otherwise this digs through a `response.headers` the
    way both the openai and anthropic SDKs expose one. Everything is
    best-effort -- a header that is missing, non-numeric, or an HTTP-date
    simply means "use the formula".
    """
    direct = getattr(exc, "retry_after", None)
    if direct is not None:
        try:
            return float(direct)
        except (TypeError, ValueError):
            pass
    headers = getattr(getattr(exc, "response", None), "headers", None)
    if headers is None:
        return None
    try:
        raw = headers.get("retry-after") or headers.get("Retry-After")
        return float(raw) if raw is not None else None
    except (AttributeError, TypeError, ValueError):
        return None


# Called with (attempt, delay_seconds, error_text) before each sleep, so
# the caller can surface "rate limited -- retrying in 12s" as progress
# rather than leaving the user staring at a stalled task.
RetryNotice = Callable[[int, float, str], Awaitable[None]]


async def stream_with_retry(
    open_stream: Callable[[], AsyncIterator[Any]],
    *,
    on_retry: Optional[RetryNotice] = None,
) -> AsyncIterator[Any]:
    """Iterate `open_stream()`, retrying a 429 raised before the first
    chunk. `open_stream` is a zero-arg factory, not an iterator, because a
    retry needs a *fresh* stream -- an exhausted async generator cannot be
    restarted.

    CancelledError is never caught here: cancelling a task while it waits
    out a backoff must stop it immediately, not after the sleep.
    """
    attempt = 1
    while True:
        produced = False
        try:
            async for chunk in open_stream():
                produced = True
                yield chunk
            return
        except RateLimitError as exc:
            if produced or attempt >= MAX_ATTEMPTS:
                raise
            delay = next_delay(attempt, retry_after_seconds(exc))
            _log.warning(
                "Rate limited (attempt %d/%d); retrying in %.1fs",
                attempt, MAX_ATTEMPTS, delay,
            )
            if on_retry is not None:
                await on_retry(attempt, delay, str(exc))
            await asyncio.sleep(delay)
            attempt += 1
