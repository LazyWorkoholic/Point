r"""B40 event-log check: the log fills, the cursor is monotonic and
per-session, replay returns exactly the gap, retention bounds the table,
and a tool call that started AND completed while the client was away is
replayable afterwards.

Four of B40's six Done-whens are deterministic and live here. #3 (the
measured cost) is `cli.py bench event-log`, because a measurement is not
an assertion. #4 (`SessionReplaySince` is gone from four files) is a
property of the tree, checked by grep in the gate.

  #1  every persisted envelope      id, type, correlation fields and a
                                    monotonic per-session cursor
  #2  events_since is exact         the events after the cursor, in
                                    order, no gaps, has_more honest
  #5  retention                     both caps actually delete, and a
                                    swept log still replays
  #6  a reconnect rebuilds the UI    ToolStarted AND ToolCompleted land
                                    in one gap and both replay -- the
                                    thing B15's message-level replay
                                    structurally could not do

Plus two properties no Done-when names and the design depends on:

  * **the cursor is per-session, not global.** Two sessions interleaving
    must each count from 1. A global counter would still be monotonic
    and would still pass a one-session test, while making every client's
    mark meaningless the moment a second session existed.
  * **the token stream is absent.** B40 section 2 chose option (c) on a
    measurement, and the only thing that keeps that true is that nothing
    later starts logging deltas. Asserted, so it fails loudly if it
    does.

Driven in-process against the mock provider -- no Ollama, no key, no
network. Run from inside the backend directory, against a scratch
profile:

    uv run python tests\manual_b40_event_log.py

Exits non-zero if any expectation failed.
"""

from __future__ import annotations

import asyncio
import os
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

FIXTURES = Path(__file__).resolve().parent / "fixtures"

os.environ["POINT_MOCK"] = "1"
os.environ["POINT_MOCK_FIXTURE"] = str(FIXTURES / "two-tool-calls.json")

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
from database.event_repository import VOLATILE_TYPES  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

FAILURES: list[str] = []

VOLATILE = set(VOLATILE_TYPES)
LIFECYCLE = {"TaskStarted", "TaskDone", "TaskFailed", "TaskCancelled"}


def check(label: str, ok: bool, detail: str = "") -> None:
    suffix = ("  -- " + detail) if detail and not ok else ""
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{suffix}")
    if not ok:
        FAILURES.append(label)


class Collector:
    """One drain loop for the whole run -- `_emit` awaits `put()` on a
    bounded queue, so a driver that stops reading stalls the runtime it
    is testing. Same shape as B15's, and deliberately the same shape:
    two different drain helpers in one repo is two things to get wrong.
    """

    def __init__(self, runtime) -> None:
        self._runtime = runtime
        self.seen: list[dict] = []
        self._task = asyncio.create_task(self._loop(), name="b40-collector")

    async def _loop(self) -> None:
        while True:
            event = await self._runtime.outbox.get()
            self.seen.append(event.to_dict())

    async def wait_for(self, types: set[str], limit_s: float = 30.0,
                       after: int = 0) -> dict | None:
        loop = asyncio.get_running_loop()
        deadline = loop.time() + limit_s
        while loop.time() < deadline:
            for envelope in self.seen[after:]:
                if envelope["type"] in types:
                    return envelope
            await asyncio.sleep(0.05)
        return None

    def cancel(self) -> None:
        self._task.cancel()


def rewind_mock(runtime) -> None:
    """Put the mock provider back on turn 0.

    `two-tool-calls.json` is a multi-TURN fixture and the provider's
    turn cursor is per-instance, advancing on every send and then
    clamping to the last turn (`min(self._turn, len(turns) - 1)`). The
    tool pair is in turn 0, so a second send replays the final
    tool-free turn instead -- which is exactly how check [6] first
    failed, with a TaskDone and no ToolStarted anywhere. Reaching into
    `_turn` is deliberate: the alternative is a second fixture whose
    only difference is that it repeats itself, and a check that needs
    the same turn twice should say so rather than encode it in data.
    """
    provider = runtime.providers.get("mock")
    provider._turn = 0


async def send_and_wait(runtime, collector: Collector, session_id: str,
                        text: str) -> list[dict]:
    """One turn, drained to its terminal event. Returns the envelopes
    that turn produced, so a check can compare what went over the wire
    against what the log kept."""
    mark = len(collector.seen)
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "SendMessage",
        "payload": {
            "session": session_id,
            "text": text,
            # `research` is the only built-in with tool grants, and the
            # bounds raise its one-step default. The first run of this
            # file used the default `general` agent and the turn died as
            # `tool_not_granted` before emitting a pair, which made
            # check [6] vacuous -- caught by the "really did produce a
            # tool pair" guard immediately above it, which is why that
            # guard is there.
            "agent": "research",
            "bounds": {"max_steps": 6, "max_tool_calls": 6},
        },
    })
    await collector.wait_for(LIFECYCLE, limit_s=60.0, after=mark)
    # The ContextReport push and a trailing usage event can land just
    # after the terminal one; give them the loop briefly so the log and
    # the wire are compared at the same point in time rather than one
    # event apart.
    await asyncio.sleep(0.3)
    return collector.seen[mark:]


async def events_since(runtime, collector: Collector, session_id: str,
                       since_seq: int, limit: int = 200) -> dict:
    mark = len(collector.seen)
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "EventsSince",
        "payload": {"session": session_id, "since_seq": since_seq, "limit": limit},
    })
    reply = await collector.wait_for({"SessionEvents"}, limit_s=15.0, after=mark)
    return reply["payload"] if reply else {}


async def main() -> int:
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    collector: Collector | None = None
    try:
        collector = Collector(runtime)
        project = await runtime.projects.resolve(str(Path(__file__).resolve().parents[2]))
        session = await runtime.sessions.create(
            title="b40", provider="mock", model="mock-1",
            workspace="chat", project_id=project.id if project else None,
        )

        print("\n[1] the log fills, with the envelope's own identity")
        wire = await send_and_wait(runtime, collector, session.id, "list and read")
        rows, _ = await runtime.events.events_since(session.id, 0, 500)
        check(
            "the log is not empty after a turn",
            bool(rows),
            "nothing was persisted -- _emit is not writing",
        )
        check(
            "every row carries its own envelope id and type",
            all(r["id"] and r["type"] for r in rows),
            f"{sum(1 for r in rows if not (r['id'] and r['type']))} row(s) missing one",
        )
        wire_ids = {e["id"] for e in wire}
        check(
            "each row's id is the id that actually went over the wire",
            all(r["id"] in wire_ids for r in rows),
            "a logged id no client ever saw means the log was written from a "
            "different envelope than the one emitted",
        )
        check(
            "the cursor is monotonic and dense from 1",
            [r["event_seq"] for r in rows] == list(range(1, len(rows) + 1)),
            f"event_seqs={[r['event_seq'] for r in rows]}",
        )
        check(
            "a task-scoped row carries its task_id, and the correlation id",
            any(r["task_id"] and r["corr_id"] for r in rows),
            f"rows={[(r['type'], bool(r['task_id']), bool(r['corr_id'])) for r in rows]}",
        )

        print("\n[2] the token stream is absent, by design (section 2, option (c))")
        logged_types = [r["type"] for r in rows]
        wire_volatile = sum(1 for e in wire if e["type"] in VOLATILE)
        check(
            "deltas went over the wire in this turn",
            wire_volatile > 0,
            "the fixture produced no deltas, so the next check proves nothing",
        )
        check(
            "and none of them are in the log",
            not any(t in VOLATILE for t in logged_types),
            f"logged={sorted(set(logged_types) & VOLATILE)} -- option (c) was "
            "chosen on a measurement; this is what keeps it true",
        )
        check(
            "every logged envelope carries event_seq on the wire too",
            all(e.get("event_seq") is not None
                for e in wire
                if e["type"] not in VOLATILE and e.get("session_id")),
            "a persisted envelope emitted with a null event_seq would be "
            "skipped by MarkSeen and replayed forever",
        )
        check(
            "and a volatile one carries none",
            all(e.get("event_seq") is None for e in wire if e["type"] in VOLATILE),
            "a delta with a cursor would advance a mark past events that "
            "were never logged",
        )

        print("\n[6] a tool call that started AND completed in the gap replays")
        mark_seq = await runtime.events.max_seq(session.id)
        rewind_mock(runtime)
        wire2 = await send_and_wait(runtime, collector, session.id, "again")
        replay = await events_since(runtime, collector, session.id, mark_seq)
        replayed = replay.get("events", [])
        replayed_types = [e["type"] for e in replayed]
        check(
            "the second turn really did produce a tool pair",
            sum(1 for e in wire2 if e["type"] == "ToolStarted") > 0
            and sum(1 for e in wire2 if e["type"] == "ToolCompleted") > 0,
            f"wire types={sorted({e['type'] for e in wire2})} -- without a "
            "tool pair this check is vacuous",
        )
        check(
            "both halves of the pair are in the replay",
            "ToolStarted" in replayed_types and "ToolCompleted" in replayed_types,
            f"replayed={replayed_types} -- this is B40's reason to exist: "
            "message-level replay could express neither half",
        )
        check(
            "a terminal event is in the replay too",
            any(t in LIFECYCLE for t in replayed_types),
            f"replayed={replayed_types}",
        )
        check(
            "exactly the gap: nothing at or before the mark",
            bool(replayed) and all(e["event_seq"] > mark_seq for e in replayed),
            f"mark={mark_seq} replayed={[e['event_seq'] for e in replayed]}",
        )
        check(
            "in order, with no gaps",
            [e["event_seq"] for e in replayed]
            == list(range(mark_seq + 1, mark_seq + 1 + len(replayed))),
            f"event_seqs={[e['event_seq'] for e in replayed]}",
        )
        check(
            "every replayed event says it is a replay",
            all(e.get("replay") is True for e in replayed),
            "Go routes on this; without it a replayed ToolCompleted is "
            "indistinguishable from a tool completing now",
        )
        check(
            "the mechanism is named on the wire",
            replay.get("mechanism") == "events.session_seq",
            f"mechanism={replay.get('mechanism')}",
        )

        print("\n[2b] has_more is honest, not silent truncation")
        total = await runtime.events.max_seq(session.id)
        page = await events_since(runtime, collector, session.id, 0, limit=2)
        check(
            "a short limit returns exactly that many",
            len(page.get("events", [])) == 2,
            f"got {len(page.get('events', []))}",
        )
        check(
            "and says there is more",
            page.get("has_more") is True,
            f"has_more={page.get('has_more')} with {total} events logged",
        )
        tail = await events_since(runtime, collector, session.id, total)
        check(
            "a cursor at the end returns nothing and says so",
            tail.get("events") == [] and tail.get("has_more") is False,
            f"events={tail.get('events')} has_more={tail.get('has_more')}",
        )

        print("\n[1b] the cursor is PER-SESSION, not global")
        other = await runtime.sessions.create(
            title="b40 second", provider="mock", model="mock-1",
            workspace="chat", project_id=project.id if project else None,
        )
        await send_and_wait(runtime, collector, other.id, "second session")
        other_rows, _ = await runtime.events.events_since(other.id, 0, 500)
        check(
            "the second session's log starts at 1",
            bool(other_rows) and other_rows[0]["event_seq"] == 1,
            f"first event_seq={other_rows[0]['event_seq'] if other_rows else None} "
            f"-- the first session had already reached {total}, so a global "
            "counter would start this one well above 1",
        )
        check(
            "and replay does not leak the other session's events",
            all(e["session_id"] == other.id for e in other_rows),
            "events_since is keyed on session_id; a leak here is a privacy "
            "bug as much as a correctness one",
        )
        first_still = await runtime.events.max_seq(session.id)
        check(
            "the first session's cursor was not moved by the second's traffic",
            first_still == total,
            f"was {total}, now {first_still}",
        )

        print("\n[5] retention bounds the table")
        before = await runtime.events.count()
        swept = await runtime.events.sweep(max_age_s=7 * 24 * 60 * 60,
                                           max_per_session=10_000)
        check(
            "a sweep with caps nothing exceeds deletes nothing",
            swept == {"by_age": 0, "by_cap": 0}
            and await runtime.events.count() == before,
            f"swept={swept} -- retention must not be destructive by default",
        )
        swept = await runtime.events.sweep(max_age_s=7 * 24 * 60 * 60,
                                           max_per_session=2)
        after_cap = await runtime.events.count(session.id)
        check(
            "the per-session row cap deletes the oldest and keeps the newest",
            swept["by_cap"] > 0 and after_cap == 2,
            f"swept={swept}, session now holds {after_cap} (wanted 2)",
        )
        kept, _ = await runtime.events.events_since(session.id, 0, 500)
        check(
            "a swept log still replays, with gaps in the cursor and no lies",
            len(kept) == 2 and [e["event_seq"] for e in kept] == sorted(
                e["event_seq"] for e in kept),
            f"kept event_seqs={[e['event_seq'] for e in kept]} -- the cursor "
            "is a high-water mark, not a count, so gaps after a sweep are "
            "correct; `> seq` does not need a dense run",
        )
        check(
            "the cursor does NOT restart after a sweep",
            await runtime.events.max_seq(session.id) == total,
            "renumbering would make every client's existing mark wrong",
        )
        swept = await runtime.events.sweep(max_age_s=-1.0, max_per_session=10_000)
        check(
            "the age cap deletes everything older than the cutoff",
            swept["by_age"] > 0 and await runtime.events.count() == 0,
            f"swept={swept}, {await runtime.events.count()} row(s) left",
        )

        print("\n[1c] a write failure does not fail the emit")
        # The envelope is the product; the log is a recovery aid. Forced
        # rather than waited for, because the real trigger is a locked
        # database or a full disk and neither is reproducible on demand.
        broken = runtime.events

        class Exploding:
            """Delegates everything except the write, so the only thing
            under test is what a failing `append` does to the emit."""

            def __init__(self, real):
                self._real = real

            def __getattr__(self, name):
                return getattr(self._real, name)

            async def append(self, **kwargs):
                raise RuntimeError("disk on fire")

        runtime.events = Exploding(broken)
        try:
            mark = len(collector.seen)
            reply = await events_since(runtime, collector, session.id, 0)
            check(
                "an envelope still goes out when the log write raises",
                reply != {},
                "the reply never arrived -- a logging failure took down the "
                "request, which is the wrong trade",
            )
        finally:
            runtime.events = broken

        print()
        if FAILURES:
            print("FAILED: " + "; ".join(FAILURES))
            return 1
        print("ALL CHECKS PASSED")
        return 0
    finally:
        if collector is not None:
            collector.cancel()
        await shutdown_runtime(runtime)


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
