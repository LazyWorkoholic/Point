r"""What `context report` says about a question whose task is still running.

The prompt-isolation fix (context.py's history filter) excludes EVERY
unanswered question from another task, whatever its status, and names why
in the report: `orphaned: ... ended 'failed' ...` for a dead task,
`unanswered: task X (running) has no reply` for a live one. This script
produces the second wording on demand and prints the report's raw JSON.

Why one process, not three terminals of `cli.py`: every process that builds
a runtime runs crash recovery (bootstrap.py) unless a real backend holds
the single-instance lock, and a CLI process never takes that lock. Two CLI
`send`s plus a CLI `context report` therefore sweep each OTHER's running
tasks to failed/backend_died, and the report reads a state the test itself
just corrupted -- found the hard way while writing this. (With the real app
running and holding the lock, `cli.py context report` beside it is safe:
recovery is skipped.) Same reasoning as manual_b12_concurrency.py.

Run from inside backend\, against a SCRATCH profile:

    uv run python tests\manual_eviction_wording.py

Real Ollama by default (POINT_EW_PROVIDER / POINT_EW_MODEL override).
Exits non-zero if any check fails.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402
from runtime.task import TaskState  # noqa: E402

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


class Outbox:
    """Keeps the runtime's bounded outbox drained (an undrained one blocks
    the runtime) and hands back the ContextReport reply for a request id."""

    def __init__(self, runtime) -> None:
        self.runtime = runtime
        self.replies: dict[str, dict] = {}

    async def pump(self, seconds: float) -> None:
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                event = await asyncio.wait_for(self.runtime.outbox.get(), timeout=0.05)
            except asyncio.TimeoutError:
                continue
            d = event.to_dict()
            if d["type"] == "ContextReport" and d.get("corr_id"):
                self.replies[d["corr_id"]] = d["payload"]

    async def report(self, session_id: str) -> dict:
        """GetContextReport over the event pipe -- the exact request the
        frontend sends and the reply UI-09 renders."""
        rid = str(uuid.uuid4())
        await self.runtime.receive({
            "v": ENVELOPE_VERSION, "id": rid,
            "type": "GetContextReport", "payload": {"session_id": session_id},
        })
        for _ in range(100):
            await self.pump(0.05)
            if rid in self.replies:
                return self.replies.pop(rid)
        raise RuntimeError("no ContextReport reply")


async def send(runtime, session_id: str, text: str) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
        "type": "SendMessage", "payload": {"session": session_id, "text": text},
    })


def evictions_for(report: dict, task_id: str) -> list[dict]:
    return [e for e in report["next_evictions"] if task_id[:8] in e["reason"]]


async def main() -> int:
    provider = os.environ.get("POINT_EW_PROVIDER", "ollama")
    model = os.environ.get("POINT_EW_MODEL", "qwen2.5:1.5b-8k")
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    out = Outbox(runtime)
    print(f"provider   {provider}/{model}")
    try:
        session = await runtime.sessions.create(title="eviction wording", provider=provider, model=model)

        # -- 1. ONE in-flight task, nothing else sent -----------------------
        print("\n=== one @research in flight, nothing else sent")
        await send(runtime, session.id,
                   "@research explain in great detail how bubble sort works, with code and complexity analysis")
        research = None
        for _ in range(200):
            await out.pump(0.05)
            live = [t for t in runtime.tasks.list_all() if t.state is TaskState.RUNNING]
            if live:
                research = live[0]
                break
        check("the research task is RUNNING", research is not None)
        if research is None:
            return 1

        solo = await out.report(session.id)
        print("  raw next_evictions:", json.dumps(solo["next_evictions"], indent=2))
        hits = evictions_for(solo, research.id)
        check("its question is already named as unanswered, with no sibling send at all",
              len(hits) == 1 and hits[0]["reason"].startswith(f"unanswered: task {research.id[:8]} (running)"),
              json.dumps(hits))

        # -- 2. second real send in the SAME session, report immediately ------
        print("\n=== second send while the first still runs; report before its reply")
        await send(runtime, session.id, "tell me importance of bees")
        await out.pump(0.3)
        state_now = {t.id[:8]: t.state.value for t in runtime.tasks.list_all()}
        print(f"  live task states at report time: {state_now}")
        both = await out.report(session.id)
        print("  RAW ContextReport payload:")
        print(json.dumps(both, indent=2))
        hits = evictions_for(both, research.id)
        check("the FIRST task's question appears with the running-specific wording",
              len(hits) == 1 and hits[0]["reason"].startswith(f"unanswered: task {research.id[:8]} (running)"),
              json.dumps(hits))
        check("research is genuinely still RUNNING at report time",
              research.state is TaskState.RUNNING, research.state.value)

        # -- 3. once everything has answered, nothing is evicted ---------------
        print("\n=== after both tasks finish")
        deadline = time.monotonic() + 600
        while runtime.tasks.list_active() and time.monotonic() < deadline:
            await out.pump(0.5)
        done = await out.report(session.id)
        print("  raw next_evictions:", json.dumps(done["next_evictions"]))
        final = {t.id[:8]: t.state.value for t in runtime.tasks.list_all()}
        print(f"  final task states: {final}")
        answered = all(s == "completed" for s in final.values())
        if answered:
            check("answered exchanges are no longer evicted", done["next_evictions"] == [],
                  json.dumps(done["next_evictions"]))
        else:
            print("  (a task did not complete -- its question is correctly reported as orphaned instead)")
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
