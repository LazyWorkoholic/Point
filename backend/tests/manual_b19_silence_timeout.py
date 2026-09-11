r"""B19 regression check: a SILENCE-triggered timeout must persist its
outcome and its error_code, together with whatever partial text arrived.

This is what the episode's own gate (Test 2) exists to catch, and the
first real run of that gate reported it failing -- the timeout fired
correctly on the wire, but the `messages` row read as empty outcome and
empty error_code.

Two different things live behind that symptom, and this file pins both:

  1. Silence AFTER partial output. The row IS written, and must carry
     outcome=timed_out / error_code=bounds_exceeded with its partial
     text. What matters here is WHERE the timeout fires: `async for`
     blocks in `__anext__` until the provider speaks, so a per-chunk
     deadline check can only run at the moment the silence ENDS -- and it
     ran before that chunk reached the accumulator, discarding it. Timing
     the WAIT out instead (asyncio.wait_for around __anext__) fires
     during the silence, so nothing in hand is thrown away.

  2. Silence BEFORE any output -- a cold model load. Nothing is ever
     accumulated, so B12's empty-content policy writes NO assistant row
     (an empty AssistantMessage fails B3 validation), and the `tasks` row
     is the only carrier of how it ended -- which is exactly what S11b's
     `orphanReply` join reads. A gate that queries
     `messages ORDER BY rowid DESC LIMIT 1` with no role filter lands on
     the USER row here and reads its legitimately-NULL columns as a bug.

Driven in-process against the mock provider, the same shape
`manual_b12_concurrency.py` uses, so it needs no Ollama, no key and no
network: a regression test for a termination path is worth nothing if
running it depends on a live model being warm.

Run it from inside the backend directory, against a scratch profile:

    uv run python tests\manual_b19_silence_timeout.py

Exits non-zero if any expectation failed, so a gate can branch on the
exit code rather than on a human reading the output.
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

# POINT_MOCK must be set before bootstrap builds the provider registry --
# the mock is registered only when it is 1. POINT_MOCK_FIXTURE is re-set
# per scenario below, because MockProvider resolves its path once, in
# __init__, so a second fixture genuinely needs a second runtime.
os.environ["POINT_MOCK"] = "1"

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from managers.message import MessageRole  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

# One second of budget, doubled to two while no chunk has arrived yet,
# sits comfortably under both fixtures' gaps and comfortably over the
# 0.05s spacing of the chunks before them -- so scenario 1 gets real text
# AND a real timeout, which is the combination the bug hid behind.
TIMEOUT_S = 1

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    # The detail prints on FAILURE only: it is written as an explanation
    # of what going wrong would mean, so printing it beside a PASS reads
    # as though the check had failed.
    suffix = ("  -- " + detail) if detail and not ok else ""
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{suffix}")
    if not ok:
        FAILURES.append(label)


async def drain_until(runtime, wanted: set[str], limit_s: float = 30.0) -> list[dict]:
    """Collect envelopes until one of `wanted` arrives, or time runs out.

    Draining is not optional: `_emit` awaits `put()` on a bounded queue,
    so a driver that stops reading eventually blocks the runtime it is
    testing.
    """
    seen: list[dict] = []
    loop = asyncio.get_running_loop()
    deadline = loop.time() + limit_s
    while loop.time() < deadline:
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=0.2)
        except asyncio.TimeoutError:
            continue
        payload = event.to_dict()
        seen.append(payload)
        if payload["type"] in wanted:
            return seen
    return seen


async def run_scenario(fixture: str, title: str, text: str):
    """One runtime per fixture -- see POINT_MOCK_FIXTURE's note above."""
    os.environ["POINT_MOCK_FIXTURE"] = str(FIXTURES / fixture)
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    try:
        session = await runtime.sessions.create(
            title=title, provider="mock", model="mock",
        )
        await runtime.receive({
            "v": ENVELOPE_VERSION,
            "id": str(uuid.uuid4()),
            "type": "SendMessage",
            "payload": {
                "session": session.id,
                "text": text,
                "bounds": {"timeout_s": TIMEOUT_S},
            },
        })
        events = await drain_until(runtime, {"TaskDone", "TaskFailed", "TaskCancelled"})
        terminal = [
            e for e in events
            if e["type"] in ("TaskDone", "TaskFailed", "TaskCancelled")
        ]
        rows = await runtime.sessions.get_messages(session.id)
        tasks = await runtime.task_repo.list_recent(limit=50, session_id=session.id)
        return terminal, rows, tasks
    finally:
        for task in runtime.tasks.list_active():
            task.cancel()
        await asyncio.sleep(0.2)
        await shutdown_runtime(runtime)


def describe(rows) -> list:
    return [(m.role.value, len(m.text), m.outcome, m.error_code) for m in rows]


async def main() -> int:
    print(f"database   {resolve_db_path()}")
    print(f"timeout_s  {TIMEOUT_S} (silence budget, doubled before the first chunk)")

    # -------------------------------------------------------------------
    # 1. Silence AFTER partial output.
    # -------------------------------------------------------------------
    print("\n=== partial output, then 6s of dead air")
    terminal, rows, _tasks = await run_scenario(
        "mock_silent_gap.json", "b19 partial then silence", "anything",
    )
    print(f"  terminal: {[e['type'] for e in terminal] or 'NONE'}")
    check("the silence ended the task", bool(terminal), "no terminal event arrived at all")
    if not terminal:
        return 1

    payload = terminal[-1]["payload"]
    check(
        "it ended as TaskFailed, not TaskDone",
        terminal[-1]["type"] == "TaskFailed",
        f"got {terminal[-1]['type']} -- the 6s gap should not have been survivable",
    )
    check(
        "the wire carries code=bounds_exceeded",
        payload.get("code") == "bounds_exceeded",
        f"code={payload.get('code')!r}",
    )
    check(
        "the wire carries outcome=timed_out",
        payload.get("outcome") == "timed_out",
        f"outcome={payload.get('outcome')!r} -- `bound` splits this from bounds_exceeded",
    )
    check(
        "it timed out on SILENCE, not on elapsed time",
        "silence" in (payload.get("error") or ""),
        f"error={payload.get('error')!r} -- the hard ceiling firing here would mean the "
        "silence budget never ran at all, which is the pre-fix behaviour",
    )

    # The actual regression: the PERSISTED row. Everything above was
    # already true while the bug was live.
    print(f"  rows: {describe(rows)}")
    assistant = [m for m in rows if m.role == MessageRole.ASSISTANT]
    check(
        "an assistant row was persisted at all",
        bool(assistant),
        "none written -- partial text was lost, so there are no columns to check",
    )
    if assistant:
        row = assistant[-1]
        check(
            "the partial text before the gap survived",
            "before-the-gap" in row.text,
            f"content={row.text!r}",
        )
        check(
            "the row's outcome is non-null and reads timed_out",
            (row.outcome.value if row.outcome else None) == "timed_out",
            f"outcome={row.outcome!r}",
        )
        check(
            "the row's error_code is non-null and reads bounds_exceeded",
            row.error_code == "bounds_exceeded",
            f"error_code={row.error_code!r}",
        )
        check(
            "the row is marked interrupted",
            bool(row.interrupted),
            f"interrupted={row.interrupted!r}",
        )

    # -------------------------------------------------------------------
    # 2. Silence BEFORE any output -- the cold-model-load case.
    # -------------------------------------------------------------------
    print("\n=== dead air from the first moment (nothing ever accumulates)")
    terminal, rows, tasks = await run_scenario(
        "mock_cold_silence.json", "b19 cold silence", "anything",
    )
    print(f"  terminal: {[e['type'] for e in terminal] or 'NONE'}")
    print(f"  rows: {describe(rows)}")
    print(f"  tasks: {[(t['status'], t['error_code']) for t in tasks]}")

    check(
        "the cold-start silence still ends the task",
        bool(terminal) and terminal[-1]["type"] == "TaskFailed",
        f"got {[e['type'] for e in terminal]}",
    )
    check(
        "no assistant row is written when nothing was accumulated",
        not [m for m in rows if m.role == MessageRole.ASSISTANT],
        "an empty AssistantMessage fails B3 validation -- B12's documented policy",
    )
    check(
        "the last messages row is therefore the USER row, with NULL columns",
        bool(rows)
        and rows[-1].role == MessageRole.USER
        and rows[-1].outcome is None
        and rows[-1].error_code is None,
        "this is what a role-blind ORDER BY rowid DESC LIMIT 1 reads as a missing outcome",
    )
    check(
        "the tasks row carries the ending instead",
        bool(tasks) and tasks[0]["error_code"] == "bounds_exceeded",
        f"tasks={[(t['status'], t['error_code']) for t in tasks]} -- if this is empty too, "
        "the ending really is unrecorded anywhere",
    )

    # -------------------------------------------------------------------
    # 3. NEVER silent -- the runaway-spiral case B11 built the original
    #    check for. A silence budget alone cannot fire against continuous
    #    output, by construction, so this is the scenario that proves
    #    _HARD_CEILING_FACTOR is load-bearing rather than decorative.
    #    Neither max_steps (bounds the number of provider.stream() calls,
    #    not the length of any one of them) nor max_tool_calls (unrelated
    #    to text volume) bounds this either.
    # -------------------------------------------------------------------
    print("")
    print("=== 10s of continuous output, never a gap (the runaway spiral)")
    started = time.monotonic()
    terminal, rows, _spiral_tasks = await run_scenario(
        "mock_never_silent.json", "b19 never silent", "anything",
    )
    elapsed = time.monotonic() - started
    print(f"  terminal: {[e['type'] for e in terminal] or 'NONE'}  after {elapsed:.1f}s")
    print(f"  rows: {describe(rows)}")

    check(
        "a stream that never goes quiet is still terminated",
        bool(terminal) and terminal[-1]["type"] == "TaskFailed",
        f"got {[e['type'] for e in terminal]} after {elapsed:.1f}s -- if this COMPLETED, "
        "the ceiling bounds nothing and B11's defense is gone",
    )
    if terminal and terminal[-1]["type"] == "TaskFailed":
        spiral = terminal[-1]["payload"]
        check(
            "the TOTAL-duration ceiling fired, not the silence budget",
            "total" in (spiral.get("error") or ""),
            f"error={spiral.get('error')!r} -- a silence message here would mean the "
            "fixture had a gap in it and this scenario proves nothing",
        )
        check(
            "it fired at the ceiling, before the stream would have ended on its own",
            elapsed < 9.0,
            f"{elapsed:.1f}s -- the fixture runs 10s, so at or past that the task "
            "simply finished rather than being bounded",
        )
        check(
            "the partial text up to the cut is preserved",
            any("spiral01" in m.text for m in rows if m.role == MessageRole.ASSISTANT),
            "a bounded stop loses the REMAINING work, never the work already done",
        )


    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
