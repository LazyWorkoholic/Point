r"""EventRepository: every session-scoped envelope the backend sent, on
disk, so a reconnect can be told what it missed (B40).

Raw SQL only, same shape as every other repository here.

## What this exists for

`events_since(session_id, seq)` -- the replay rung of F2's recovery
ladder. B15 was asked for it and could not serve it: nothing persisted
an `OutgoingEvent`, so it shipped message-level replay over
`messages.seq` as an explicit interim (`SessionReplaySince`) and
deferred the real thing to B40. This is the real thing, and B40 deletes
the interim rather than keeping both -- two replay commands with two
different cursors is the "second copy that can disagree" this repo
removes on sight.

Message-level replay answers *what does this transcript look like now*.
It cannot answer *what happened while I was gone*: a tool call that
started and completed, a progress step, a usage update, a `FillProposal`
pushed after a terminal event. Those are events. This table holds them.

## What is written, and what is deliberately not

**Session-scoped envelopes only.** `session_id` is NOT NULL (see
migration v8's note): the one query against this table is `WHERE
session_id = ? AND session_seq > ?`, so a row with no session is a row
no replay can ever return.

**Not the token stream.** `TextDelta`, `ThinkingDelta` and `TaskToken`
are excluded by `VOLATILE_TYPES` below. This is B40's section-2
decision, option (c), and it was measured rather than assumed -- see
`cli.py bench event-log` and B40's completed doc for the numbers. Two
independent reasons it is also the *correct* answer and not only the
cheap one:

  * Those three are exactly `IsDroppable` in
    `frontend\internal\protocol\types.go`, which already says they may
    be discarded under backpressure because *"the accumulated string
    re-renders from the next token anyway"*. An event the live path is
    allowed to drop is an event a replay cannot be expected to have.
  * The text is not lost by not being here. It is rebuilt from
    `messages` for a finished reply, or from B15's `message_checkpoints`
    for one the process died mid-way through. Persisting the deltas too
    would be a third copy of the same string, written per delta, on the
    hot path.

**Not the replay reply.** `SessionEvents` is excluded unconditionally --
see `REPLAY_TYPES` below for what logging it actually did when this
module first did.

What a replay therefore reconstructs is the *structure* of what
happened -- which is the thing message-level replay could not do -- with
the prose coming from the tables that already own prose.

## The cursor

`session_seq` is a durable, per-session, monotonic event counter. It is
neither of the two `seq`s that already exist here, and B40 forbids
conflating them:

  * the envelope's `seq` is `StreamManager`'s per-TASK stream counter,
    in-memory, dropped at `close_stream()`, null on every lifecycle
    event. It is kept, in `stream_seq`, unchanged.
  * `messages.seq` (v6/B18) is per-session MESSAGE insertion order. It
    counts messages, not events.

Assignment is a single statement -- `INSERT ... VALUES (..., (SELECT
COALESCE(MAX(session_seq), 0) + 1 FROM events WHERE session_id = ?),
...) RETURNING session_seq`. Never read-then-write. B18's note says
why this is sufficient and a read-then-write is not: *two coroutines on
this connection can only interleave between whole statements, never
inside one.* `RETURNING` is what lets the assigned value come back
without a second statement to interleave with, and `SerializedConnection`
drains the cursor before releasing its lock (B22), so one `execute()`
here is one indivisible assignment.

The unique index on `(session_id, session_seq)` means a bug that tried
to hand out a cursor twice would fail the INSERT rather than produce a
replay with a duplicate.
"""

from __future__ import annotations

import json
import logging
import time
from typing import Any, Optional

import aiosqlite

logger = logging.getLogger(__name__)

# The token stream. Excluded from the log -- see the module docstring for
# the two reasons, and `bench event-log` for the measurement. Kept as the
# string values of `OutgoingEventType` rather than the enum members so
# this module stays importable from the migration/retention side without
# dragging in `runtime.events`.
VOLATILE_TYPES = frozenset({"TextDelta", "ThinkingDelta", "TaskToken"})

# The replay reply itself, excluded UNCONDITIONALLY -- `persist_volatile`
# does not reach it, because logging it is not a cost trade like the
# deltas are, it is a defect.
#
# Found by B40's own check, not reasoned about in advance. `_emit` is the
# one persistence boundary, and the reply to `EventsSince` goes out
# through `_emit` like everything else -- so the first version of this
# module logged it, and every replay appended a row whose payload was a
# verbatim copy of a page of the log. Three consequences, all observed:
# the log grew super-linearly in replays (a page of N events becomes one
# row containing N events, which the next replay then returns and copies
# again); `events_since(session, max_seq)` never settled, because asking
# "anything after the end?" itself created something after the end; and a
# reconnecting client would replay its own previous replies back to
# itself.
#
# The general rule this is the instance of: an envelope that CARRIES log
# contents is not log content. Anything added later with `replay: true`
# in its payload belongs in this set.
REPLAY_TYPES = frozenset({"SessionEvents"})

# Retention (B40 section 5). Two caps, both of them bounds on a table
# that is otherwise append-only forever on a long-lived local backend.
#
# Age first, because it is the one that actually describes replay's
# usefulness: a client that has been away for a week is not reconnecting,
# it restarted, and it will ask from a cursor this table no longer needs
# to answer. Seven days is well past any reconnect and still leaves the
# log readable as a diagnostic for "what did the backend send yesterday".
#
# Then a per-session row cap, because age alone does not bound a single
# very busy session inside the window. 2,000 events per session is far
# more than any replay reads -- `replayLimit` in `client.go` is 200 -- and
# it is per-session rather than global so a chatty session cannot evict
# a quiet one's replayable history.
#
# Run from the heartbeat, not the hot path: `TaskManager.sweep_finished`
# is the precedent for the shape. The heartbeat is every 5s, which is far
# too often for a DELETE scan, so `RETENTION_EVERY_N_BEATS` throttles it
# to roughly every five minutes.
RETENTION_MAX_AGE_S = 7 * 24 * 60 * 60
RETENTION_MAX_PER_SESSION = 2_000
RETENTION_EVERY_N_BEATS = 60

_INSERT = """
INSERT INTO events
    (id, session_id, task_id, corr_id, type, session_seq, stream_seq, ts, payload)
VALUES (
    ?, ?, ?, ?, ?,
    (SELECT COALESCE(MAX(session_seq), 0) + 1 FROM events WHERE session_id = ?),
    ?, ?, ?
)
RETURNING session_seq
"""


class EventRepository:
    """One instance per Runtime, over the shared `SerializedConnection`.

    `persist_volatile` and `enabled` exist for one caller only: `bench
    event-log`, which is how B40 chose its write strategy by measuring
    rather than preferring. The three arms it needs are exactly the
    three the doc put up for decision:

        enabled=False                      -- no log at all, the baseline
        enabled=True,  volatile=False      -- option (c), what ships
        enabled=True,  volatile=True       -- option (a), every envelope

    Nothing in the product passes either one, and saying so here is
    cheaper than a reader wondering whether the policy is configurable
    at runtime. It is not.
    """

    def __init__(
        self,
        conn: aiosqlite.Connection,
        *,
        persist_volatile: bool = False,
        enabled: bool = True,
    ) -> None:
        self._conn = conn
        self._persist_volatile = persist_volatile
        self._enabled = enabled
        # Counted so the measurement has a denominator and the bench does
        # not have to infer one from row counts it is also changing.
        self.written = 0
        self.skipped = 0
        self._beats = 0

    def should_persist(self, event_type: str, session_id: Optional[str]) -> bool:
        """The whole policy, in one predicate, so there is one place to
        read it and one place to change it."""
        if not self._enabled:
            return False
        if not session_id:
            return False
        if event_type in REPLAY_TYPES:
            return False
        if not self._persist_volatile and event_type in VOLATILE_TYPES:
            return False
        return True

    async def append(
        self,
        *,
        event_id: str,
        session_id: Optional[str],
        task_id: Optional[str],
        corr_id: Optional[str],
        event_type: str,
        stream_seq: Optional[int],
        ts: float,
        payload: dict[str, Any],
    ) -> Optional[int]:
        """Persist one envelope and return the `session_seq` it was
        assigned, or None if policy says this envelope is not logged.

        Returning the cursor is the point: it rides back out on the
        envelope as `event_seq`, which is what lets a client mark its
        high-water mark from live traffic and ask for exactly the gap
        after a reconnect.

        **A logging failure must never fail the emit.** The event log is
        a recovery aid; the envelope itself is the product. An
        `OperationalError` here (a locked database, a disk full) would
        otherwise propagate out of `Runtime._emit` and take down the
        reply the user is waiting for, which trades a degraded replay
        for a broken session. So it is caught, counted and logged to
        stderr, and the envelope goes out with a null `event_seq` --
        indistinguishable, to a client, from an envelope policy never
        logged in the first place.
        """
        if not self.should_persist(event_type, session_id):
            self.skipped += 1
            return None

        try:
            cursor = await self._conn.execute(
                _INSERT,
                (
                    event_id, session_id, task_id, corr_id, event_type,
                    session_id, stream_seq, ts, json.dumps(payload),
                ),
            )
            row = await cursor.fetchone()
            await self._conn.commit()
        except Exception:
            logger.warning(
                "event log write failed for %s (session %s) -- the envelope "
                "still goes out, with no event_seq",
                event_type, session_id, exc_info=True,
            )
            return None

        self.written += 1
        if row is None:
            return None
        return int(row["session_seq"])

    async def events_since(
        self, session_id: str, since_seq: int, limit: int
    ) -> tuple[list[dict[str, Any]], bool]:
        """The events after `since_seq` for one session, oldest first,
        bounded by `limit`. Returns (events, has_more).

        `limit + 1` is fetched and the extra row discarded, which is how
        `has_more` is answered without a second COUNT query -- the same
        trick `fetch_messages_keyset` uses (B18).

        Each event comes back as the envelope it was: the payload
        re-parsed, `event_seq` set to the stored cursor, and `replay`
        true on it. That last field is not decoration -- it is what lets
        Go route these through `deliver()` as `RouteReplay` and never
        mistake a replayed `ToolCompleted` for a tool completing now.
        """
        cursor = await self._conn.execute(
            """
            SELECT id, session_id, task_id, corr_id, type, session_seq,
                   stream_seq, ts, payload
            FROM events
            WHERE session_id = ? AND session_seq > ?
            ORDER BY session_seq ASC
            LIMIT ?
            """,
            (session_id, since_seq, limit + 1),
        )
        rows = await cursor.fetchall()

        has_more = len(rows) > limit
        rows = rows[:limit]

        events: list[dict[str, Any]] = []
        for row in rows:
            try:
                payload = json.loads(row["payload"])
            except (TypeError, ValueError):
                # A row this module wrote cannot fail to parse, so this
                # means hand-editing or corruption. Surface the event
                # with an empty payload rather than dropping it: a gap in
                # a replay keyed on a monotonic cursor is worse than a
                # thin event, because the gap is what a client checks for.
                logger.warning(
                    "event %s has unparseable payload; replaying it empty", row["id"]
                )
                payload = {}
            events.append({
                "id": row["id"],
                "type": row["type"],
                "session_id": row["session_id"],
                "task_id": row["task_id"],
                "corr_id": row["corr_id"],
                "event_seq": int(row["session_seq"]),
                "seq": row["stream_seq"],
                "ts": row["ts"],
                "payload": payload,
                "replay": True,
            })
        return events, has_more

    async def max_seq(self, session_id: str) -> int:
        cursor = await self._conn.execute(
            "SELECT COALESCE(MAX(session_seq), 0) AS n FROM events WHERE session_id = ?",
            (session_id,),
        )
        row = await cursor.fetchone()
        return int(row["n"]) if row else 0

    async def count(self, session_id: Optional[str] = None) -> int:
        if session_id:
            cursor = await self._conn.execute(
                "SELECT COUNT(*) AS n FROM events WHERE session_id = ?", (session_id,)
            )
        else:
            cursor = await self._conn.execute("SELECT COUNT(*) AS n FROM events")
        row = await cursor.fetchone()
        return int(row["n"]) if row else 0

    async def maybe_sweep(self) -> Optional[dict[str, int]]:
        """Called from the heartbeat; does the work on one beat in
        `RETENTION_EVERY_N_BEATS` and nothing on the others.

        Returns the sweep's counts when it ran, None when it was
        throttled -- so a caller can log a sweep without having to know
        the cadence.
        """
        self._beats += 1
        if self._beats % RETENTION_EVERY_N_BEATS:
            return None
        return await self.sweep()

    async def sweep(
        self,
        *,
        max_age_s: float = RETENTION_MAX_AGE_S,
        max_per_session: int = RETENTION_MAX_PER_SESSION,
    ) -> dict[str, int]:
        """Both retention caps, oldest-first, in two statements.

        Deliberately NOT one clever statement. The age cap is an indexed
        range delete; the row cap is a per-session rank. Fusing them
        would produce a query whose plan nobody checks, to save one
        round trip on a path that runs every five minutes.

        The row cap keeps the NEWEST `max_per_session` rows, because
        replay reads forward from a recent cursor -- the rows a client
        could still ask for are the recent ones, and the old ones are
        exactly what a cap is for.

        It does not renumber anything. `session_seq` has gaps after a
        sweep, and that is correct: the cursor is a high-water mark, not
        a count, and `events_since` asks `> seq` rather than for a dense
        run. A client whose mark has been swept past gets the oldest
        surviving events instead of the ones that are gone, which is the
        most a bounded log can offer and is why `has_more` exists.
        """
        cutoff = time.time() - max_age_s
        aged = await self._conn.execute(
            "DELETE FROM events WHERE ts < ?", (cutoff,)
        )
        by_age = aged.rowcount or 0

        capped = await self._conn.execute(
            """
            DELETE FROM events
            WHERE id IN (
                SELECT id FROM (
                    SELECT id, ROW_NUMBER() OVER (
                        PARTITION BY session_id ORDER BY session_seq DESC
                    ) AS rn
                    FROM events
                )
                WHERE rn > ?
            )
            """,
            (max_per_session,),
        )
        by_cap = capped.rowcount or 0

        await self._conn.commit()
        if by_age or by_cap:
            logger.info(
                "event log swept: %d by age (>%.0fs), %d by per-session cap (>%d)",
                by_age, max_age_s, by_cap, max_per_session,
            )
        return {"by_age": by_age, "by_cap": by_cap}
