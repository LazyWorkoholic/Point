r"""B15 / decisions.md D98: the heartbeat pings unconditionally, and a
silent task is not proof of life.

`Runtime._heartbeat_loop` used to skip its ping whenever
`self.tasks.active_count() > 0`, on the reasoning in its own docstring:
"tokens are already proving liveness". That is true of a streaming reply
and false of a tool call. B10's `web_search` against a slow endpoint
emits `ToolStarted` and then nothing at all until `ToolCompleted`; a
large `read_file` behaves the same. Inside that window the backend
produced no tokens AND no pings, so from Go the wire was
indistinguishable from a deadlocked process -- during precisely the
operation most likely to deadlock, the one waiting on something
external.

Go carried the workaround: `busyGrace` in
`frontend\internal\protocol\client.go` stretched the watchdog deadline
by 60s whenever it believed a task was in flight, so a genuine hang
mid-task took ~72s to notice rather than ~12s. B15 deletes `busyGrace`,
and this file is the half of that pair that has to be true first.

Two things are checked, and the second matters as much as the first:

  1. a ping arrives WHILE a task is running;
  2. it reports the REAL active count. Leaving the old hardcoded
     `"active_tasks": 0` in place would have replaced one lie with
     another -- a ping during a task that claims nothing is running.

Driven in-process with a 0.4s heartbeat interval so the check takes a
second rather than fifteen. The interval is a parameter of
`start_heartbeat`, not a constant this file reaches past.

    uv run python tests\manual_b15_heartbeat.py

Exits non-zero if any expectation failed.
"""

from __future__ import annotations

import asyncio
import os
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

FIXTURES = Path(__file__).resolve().parent / "fixtures"

os.environ["POINT_MOCK"] = "1"
os.environ["POINT_MOCK_FIXTURE"] = str(FIXTURES / "mock_slow.json")

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

HEARTBEAT_S = 0.4

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    suffix = ("  -- " + detail) if detail and not ok else ""
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{suffix}")
    if not ok:
        FAILURES.append(label)


async def main() -> int:
    # heartbeat=False, then started by hand below at a shorter interval:
    # build_runtime's own call uses HEARTBEAT_INTERVAL_S (5s), and a
    # check that waits out three of those is a check nobody runs.
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    collected: list[dict] = []

    async def drain() -> None:
        while True:
            event = await runtime.outbox.get()
            collected.append(event.to_dict())

    pump = asyncio.create_task(drain(), name="b15-heartbeat-pump")
    try:
        session = await runtime.sessions.create(
            title="b15 heartbeat", provider="mock", model="mock-slow",
        )
        runtime.start_heartbeat(interval=HEARTBEAT_S)

        print("\n[1] a ping arrives while nothing is running")
        await asyncio.sleep(HEARTBEAT_S * 2.5)
        idle_pings = [e for e in collected if e["type"] == "ping"]
        check(
            "the idle heartbeat still works",
            len(idle_pings) >= 1,
            f"{len(idle_pings)} pings in {HEARTBEAT_S * 2.5:.1f}s -- this is the "
            "behaviour that already existed; the change is what follows",
        )

        print("\n[2] a ping arrives while a task IS running")
        mark = len(collected)
        await runtime.receive({
            "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
            "type": "SendMessage",
            "payload": {"session": session.id, "text": "count"},
        })
        # The fixture runs several seconds at 0.3s a chunk; three
        # heartbeat intervals inside that is comfortably mid-stream.
        await asyncio.sleep(HEARTBEAT_S * 3.5)

        busy_states = [t.state.value for t in runtime.tasks.list_all()]
        check(
            "a task is genuinely running during this window",
            "running" in busy_states,
            f"states={busy_states} -- this check means nothing otherwise",
        )

        busy_pings = [
            e for e in collected[mark:]
            if e["type"] == "ping" and e["payload"].get("active_tasks", 0) > 0
        ]
        any_pings = [e for e in collected[mark:] if e["type"] == "ping"]
        check(
            "a ping was emitted during the task",
            len(any_pings) >= 1,
            f"{len(any_pings)} pings while streaming -- zero is the pre-B15 "
            "behaviour, and it is what made a slow tool call look like a hang",
        )
        check(
            "and it reports the real active count, not a hardcoded 0",
            len(busy_pings) >= 1,
            f"pings during the task: "
            f"{[e['payload'].get('active_tasks') for e in any_pings]} -- a ping "
            "claiming 0 active during a stream would be a fresh lie in place "
            "of the one just removed",
        )

        print("\n[3] the ping stops once the runtime is no longer accepting")
        await runtime.receive({
            "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
            "type": "Shutdown", "payload": {},
        })
        await asyncio.sleep(HEARTBEAT_S * 2.5)
        mark = len(collected)
        await asyncio.sleep(HEARTBEAT_S * 2.5)
        check(
            "no pings after a Shutdown",
            not [e for e in collected[mark:] if e["type"] == "ping"],
            "the loop checks is_accepting_events(), and a stopped runtime "
            "claiming liveness would be the worst version of this bug",
        )

        print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
        return 0 if not FAILURES else 1
    finally:
        pump.cancel()
        for task in runtime.tasks.list_active():
            task.cancel()
        await asyncio.sleep(0.2)
        await shutdown_runtime(runtime)


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
