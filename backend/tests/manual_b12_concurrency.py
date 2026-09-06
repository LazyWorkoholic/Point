r"""B12 manual check: the concurrency cap and the turn queue, driven
through ONE backend process.

Why this file exists rather than three `Start-Job { cli.py send ... }`:
the cap is a per-process semaphore, and that is the correct design --
`lockfile.py` makes the real backend single-instance, so one process
owns every task in the app. Three CLI processes are three independent
runtimes with three independent TaskManagers; nothing serializes them,
and `tasks list` would truthfully report three RUNNING because three
genuinely are. That would be a property of the test, not a hole in the
cap. So the check drives one Runtime, which is the topology the product
actually has.

Run it from inside `backend\`, against a scratch profile:

    uv run python tests\manual_b12_concurrency.py

It prints what it observed and exits non-zero if any expectation failed,
so the verification gate can branch on the exit code instead of on a
human reading the output.
"""

from __future__ import annotations

import asyncio
import os
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import appconfig  # noqa: E402
from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402
from runtime.task import TaskState  # noqa: E402

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


async def send(runtime, session_id: str, text: str, **extra) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "SendMessage",
        "payload": {"session": session_id, "text": text, **extra},
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


def states(runtime) -> dict[str, int]:
    counts: dict[str, int] = {}
    for task in runtime.tasks.list_all():
        counts[task.state.value] = counts.get(task.state.value, 0) + 1
    return counts


async def main() -> int:
    # single_instance=False and no heartbeat, for the same reason
    # `cli.py` uses those: this is a driver process, not a second copy of
    # the app, and a heartbeat would only add noise to the outbox this
    # script has to drain.
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    db_path = resolve_db_path()

    provider = os.environ.get("POINT_B12_PROVIDER", "ollama")
    model = os.environ.get("POINT_B12_MODEL", "qwen2.5:1.5b-8k")

    print(f"database   {db_path}")
    print(f"provider   {provider}/{model}")
    print(f"ollama cap {appconfig.config_value('limits.ollama.max_concurrent_tasks', '(unset)')}")
    print(f"queue depth {appconfig.config_value('chat.queue_depth', 1)}")

    try:
        # ---------------------------------------------------------------
        # Done-when #2: three @research runs, Ollama capped at 1.
        # ---------------------------------------------------------------
        print("\n=== #2  concurrency cap: three @research, ollama capped at 1")
        session = await runtime.sessions.create(
            title="b12 concurrency", provider=provider, model=model,
        )
        for letter in ("a", "b", "c"):
            await send(runtime, session.id, f"@research question {letter}")
            await asyncio.sleep(0.2)
        await drain(runtime)
        await asyncio.sleep(1.0)
        await drain(runtime)

        counts = states(runtime)
        print(f"  observed states: {counts}")
        running = counts.get(TaskState.RUNNING.value, 0)
        queued = counts.get(TaskState.QUEUED.value, 0)
        check(
            "exactly one @research RUNNING",
            running == 1,
            f"running={running} (three RUNNING would mean the UI is lying)",
        )
        check("the other two are QUEUED", queued == 2, f"queued={queued}")

        # And the same answer through the wire, not just the objects --
        # the UI reads ListTasks, so ListTasks is what has to be honest.
        await runtime.receive({
            "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
            "type": "ListTasks", "payload": {"scope": "active"},
        })
        listed = [e for e in await drain(runtime) if e["type"] == "TaskList"]
        wire_states = [t["state"] for t in listed[-1]["payload"]["tasks"]] if listed else []
        print(f"  ListTasks says: {wire_states}")
        check(
            "ListTasks reports 1 running + 2 queued",
            sorted(wire_states) == ["queued", "queued", "running"],
            str(wire_states),
        )

        for task in runtime.tasks.list_active():
            task.cancel()
        await asyncio.sleep(0.5)
        await drain(runtime)

        # ---------------------------------------------------------------
        # Done-when #3: turn queue at chat.queue_depth = 1.
        # ---------------------------------------------------------------
        print("\n=== #3  turn queue: 1 streaming + 1 queued, third rejected")
        chat = await runtime.sessions.create(
            title="b12 queue", provider=provider, model=model,
        )
        await send(runtime, chat.id, "write a long essay about rivers")
        await asyncio.sleep(0.3)
        await drain(runtime)

        await send(runtime, chat.id, "second question")
        await asyncio.sleep(0.3)
        events = await drain(runtime)
        errors = [e for e in events if e["type"] == "Error"]
        check(
            "the second turn queues rather than erroring",
            not errors,
            f"got {[e['payload'].get('code') for e in errors]}",
        )
        counts = states(runtime)
        print(f"  observed states: {counts}")

        await send(runtime, chat.id, "third question")
        await asyncio.sleep(0.3)
        events = await drain(runtime)
        codes = [e["payload"].get("code") for e in events if e["type"] == "Error"]
        check(
            "the third turn is rejected with queue_full",
            "queue_full" in codes,
            f"codes={codes} (a silent drop would leave the user believing they asked)",
        )

        # ---------------------------------------------------------------
        # "Queued turns are cancellable before they start."
        # ---------------------------------------------------------------
        print("\n=== queued turns are cancellable before they start")
        queued_tasks = [
            t for t in runtime.tasks.list_all() if t.state is TaskState.QUEUED
        ]
        check("a queued turn exists to cancel", bool(queued_tasks))
        if queued_tasks:
            victim = queued_tasks[0]
            accepted = runtime.tasks.cancel(victim.id)
            await asyncio.sleep(0.2)
            terminal = [
                e for e in await drain(runtime)
                if e["type"] in ("TaskDone", "TaskFailed", "TaskCancelled")
                and e["task_id"] == victim.id
            ]
            check("cancel of a queued task is accepted", accepted)
            check(
                "exactly one terminal event for the cancelled queued task",
                len(terminal) == 1,
                f"{[e['type'] for e in terminal]}",
            )

        for task in runtime.tasks.list_active():
            task.cancel()
        await asyncio.sleep(0.5)
        await drain(runtime)
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
