r"""B26 manual check: behaviour overrides and `GetDoctorReport`, driven
through ONE backend process.

Why this file exists rather than sequential `cli.py behaviour set` /
`cli.py send` / `cli.py doctor --via-wire` calls: `cli.py` builds a
brand-new Runtime per invocation (`build_runtime(single_instance=False,
...)` in `_with_runtime`), and a behaviour override lives exactly as
long as the process that set it -- that is this episode's own Do-NOT
#3 ("do not persist overrides anywhere"), not an oversight. Two
separate CLI processes can no more share a behaviour override than
they can share `runtime/scheduler.py`'s in-process turn-queue
occupancy -- that file's own docstring already says so: "driving three
sends through three CLI processes bypasses the scheduler entirely."
Proving an override actually governs scheduler behavior, and that
GetDoctorReport sees one still active, needs one long-lived process
doing all three things -- the exact shape B12's own concurrency check
already needed, for the same reason.

Run it from inside `backend\`, against a scratch profile:

    uv run python tests\manual_b26_behaviour_overrides.py

It prints what it observed and exits non-zero if any expectation
failed, so the verification gate can branch on the exit code instead
of on a human reading the output.

Scope: Done-when #1, #2, #7 only. #3/#4/#6/#8/#9 are correctly scoped
to single-invocation or cross-restart behavior and stay in the
PowerShell CLI-sequence script -- see B26's verification gate.
"""

from __future__ import annotations

import asyncio
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


async def send_event(runtime, event_type: str, **payload) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": event_type,
        "payload": payload,
    })


async def drain(runtime, limit: int = 400) -> list[dict]:
    """Take whatever is in the outbox right now.

    Draining matters for more than inspection: `_emit` awaits `put()` on
    a bounded queue, so a driver that never drains eventually blocks the
    runtime it is testing.
    """
    seen = []
    for _ in range(limit):
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=0.05)
        except asyncio.TimeoutError:
            break
        seen.append(event.to_dict())
    return seen


async def main() -> int:
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    db_path = resolve_db_path()
    print(f"database   {db_path}")

    try:
        # The echo provider needs no network and no key, and still takes
        # the turn lane for an inline send (agents\executor.py: only the
        # provider lane is waived for "echo", never the turn lane) -- so
        # it is the queue-depth check's own lane that gets exercised,
        # the same lane `chat.queue_depth` governs for a real provider.
        session = await runtime.sessions.create(
            title="b26 behaviour overrides", provider="echo", model="echo",
        )

        # ---------------------------------------------------------------
        # Done-when #1: SetBehaviourOverride(chat.queue_depth=0) governs
        # real scheduler behavior -- a second queued send gets
        # queue_full immediately where it would have queued before.
        # ---------------------------------------------------------------
        print("\n=== #1  chat.queue_depth=0 makes a second send refused immediately")
        await send_event(runtime, "SetBehaviourOverride", key="chat.queue_depth", value=0)
        events = await drain(runtime)
        set_events = [e for e in events if e["type"] == "BehaviourOverrideSet"]
        check(
            "BehaviourOverrideSet echoes the override back",
            bool(set_events) and set_events[-1]["payload"]["overrides"].get("chat.queue_depth") == 0,
            str(set_events[-1]["payload"] if set_events else events),
        )

        # Word-chunked with a 0.05s pacing sleep per word (agents\executor.py
        # _echo_body) -- six words keeps the first turn RUNNING long enough
        # for the second send below to observe it still occupying the lane.
        await send_event(runtime, "SendMessage", session=session.id, text="write a long essay about rivers")
        await asyncio.sleep(0.15)
        await drain(runtime)  # TaskStarted etc -- the first turn now occupies the turn lane

        await send_event(runtime, "SendMessage", session=session.id, text="second question")
        await asyncio.sleep(0.15)
        events = await drain(runtime)
        codes = [e["payload"].get("code") for e in events if e["type"] == "Error"]
        check(
            "the second turn is refused with queue_full at depth 0",
            "queue_full" in codes,
            f"codes={codes} (a silent drop or a queue would both be wrong at depth 0)",
        )

        await asyncio.sleep(0.5)  # let the first turn finish before moving on
        await drain(runtime)

        # ---------------------------------------------------------------
        # Done-when #2: clearing the override falls back to config.toml
        # (here, the hardcoded default of 1) -- a second send now queues
        # instead of being refused.
        # ---------------------------------------------------------------
        print("\n=== #2  clearing the override restores the default (depth 1)")
        await send_event(runtime, "SetBehaviourOverride", key="chat.queue_depth")
        events = await drain(runtime)
        set_events = [e for e in events if e["type"] == "BehaviourOverrideSet"]
        check(
            "BehaviourOverrideSet with no 'value' clears it",
            bool(set_events) and "chat.queue_depth" not in set_events[-1]["payload"]["overrides"],
            str(set_events[-1]["payload"] if set_events else events),
        )

        await send_event(runtime, "SendMessage", session=session.id, text="write another long essay please")
        await asyncio.sleep(0.15)
        await drain(runtime)

        await send_event(runtime, "SendMessage", session=session.id, text="second question again")
        await asyncio.sleep(0.15)
        events = await drain(runtime)
        codes = [e["payload"].get("code") for e in events if e["type"] == "Error"]
        check(
            "at the default depth the second turn queues rather than erroring",
            "queue_full" not in codes,
            f"codes={codes}",
        )

        await asyncio.sleep(1.0)  # let both turns drain before the next section
        await drain(runtime)

        # ---------------------------------------------------------------
        # Done-when #7: GetDoctorReport's payload includes an override
        # that is still active.
        # ---------------------------------------------------------------
        print("\n=== #7  GetDoctorReport reflects an active override")
        await send_event(runtime, "SetBehaviourOverride", key="research.stale_after_days", value=3)
        await drain(runtime)

        await send_event(runtime, "GetDoctorReport")
        events = await drain(runtime)
        reports = [e for e in events if e["type"] == "DoctorReport"]
        overrides = reports[-1]["payload"].get("behaviour_overrides") if reports else {}
        check(
            "GetDoctorReport did not raise (proves the Correction was applied)",
            bool(reports),
            str(events) if not reports else "",
        )
        check(
            "behaviour_overrides includes the still-active override",
            overrides.get("research.stale_after_days") == 3,
            str(overrides),
        )
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
