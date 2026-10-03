r"""B28 manual check: the Demo provider and its fixture library, driven
through ONE backend process.

Why one process, not sequential `cli.py send` calls (same reasoning
`manual_b26_behaviour_overrides.py` already documents for itself):
`cli.py` builds a brand-new Runtime per invocation, and both the
queued-turn check (two tasks contending for the same turn lane) and the
research run's step-by-step DemoProvider state need one long-lived
Runtime that actually has two tasks, or one multi-step run, alive in it
at once. A `cli.py send` subprocess cannot observe a lane another
subprocess already released by the time it starts.

Run it from inside `backend\`, against a scratch profile:

    uv run python tests\manual_b28_demo_mode.py

Prints PASS/FAIL per check and exits non-zero if anything failed.

Covers Done-when #2-#9 from
design/tasks/backend/B28-demo-mode-provider-fixtures.md. #1 (demo is
selectable with no env var) and #10 (providers\mock\ and
backend\tests\fixtures\ untouched) are checked separately -- #1 by
`build_registry_from_env()` directly (no process/profile needed), #10 by
`git status`/`git diff` (not something a Python script inside the
process can observe).
"""

from __future__ import annotations

import asyncio
import json
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import os  # noqa: E402

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from managers.message import MessageRole  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402
from runtime.task import TaskState  # noqa: E402

SAMPLE_PROJECT = str((Path(__file__).resolve().parent.parent / "demo" / "sample_project"))

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


async def drain(runtime, limit: int = 400, idle_timeout: float = 0.05) -> list[dict]:
    seen = []
    for _ in range(limit):
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=idle_timeout)
        except asyncio.TimeoutError:
            break
        seen.append(event.to_dict())
    return seen


_TERMINAL = {"TaskDone", "TaskFailed", "TaskCancelled"}


async def drain_until_terminal(runtime, max_wait: float = 30.0) -> list[dict]:
    """Keeps draining until a terminal event for ANY task has appeared,
    or `max_wait` elapses -- this script runs exactly one task at a time
    except in the queued-turn section, which drains for itself instead."""
    collected: list[dict] = []
    elapsed = 0.0
    step = 0.1
    while elapsed < max_wait:
        batch = await drain(runtime, idle_timeout=step)
        collected.extend(batch)
        if any(e["type"] in _TERMINAL for e in batch):
            return collected
        elapsed += step
    return collected


async def last_assistant_message(runtime, session_id: str):
    messages = await runtime.sessions.get_messages(session_id)
    for message in reversed(messages):
        if message.role == MessageRole.ASSISTANT:
            return message
    return None


async def main() -> int:
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    db_path = resolve_db_path()
    print(f"database   {db_path}")
    print(f"sample project   {SAMPLE_PROJECT}")

    try:
        project = await runtime.projects.init(SAMPLE_PROJECT)

        # ---------------------------------------------------------------
        # #2: a timed chat reply with non-zero usage.
        # ---------------------------------------------------------------
        print("\n=== #2  chat reply streams over real time, with usage")
        session = await runtime.sessions.create(
            title="b28 chat", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        start = asyncio.get_event_loop().time()
        await send_event(runtime, "SendMessage", session=session.id, text="tell me about this project")
        events = await drain_until_terminal(runtime)
        elapsed = asyncio.get_event_loop().time() - start
        check("reached a terminal event", any(e["type"] in _TERMINAL for e in events), str([e["type"] for e in events]))
        check(f"took several real seconds ({elapsed:.1f}s), not near-instant", elapsed > 3.0, f"elapsed={elapsed:.2f}s")
        reply = await last_assistant_message(runtime, session.id)
        check("reply persisted with non-zero usage", bool(reply) and reply.usage.total_tokens > 0,
              str(reply.usage.to_dict()) if reply else "no assistant message")

        # ---------------------------------------------------------------
        # #3: queued turn -- a second send while the first is still
        # streaming must be admitted as QUEUED, not refused, not run
        # alongside the first (TURN_LANE_CAPACITY=1).
        # ---------------------------------------------------------------
        print("\n=== #3  a second send while the first streams is QUEUED, then both finish")
        q_session = await runtime.sessions.create(
            title="b28 queue", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        await send_event(runtime, "SendMessage", session=q_session.id, text="tell me about this project")
        await asyncio.sleep(0.2)
        await drain(runtime)  # TaskStarted etc -- first turn now occupies the turn lane
        await send_event(runtime, "SendMessage", session=q_session.id, text="second question, please queue")
        await asyncio.sleep(0.2)
        await drain(runtime)
        tasks_for_session = [t for t in runtime.tasks.list_all() if t.session_id == q_session.id]
        states = [(t.id, t.state) for t in tasks_for_session]
        check(
            "the second task is QUEUED while the first still runs",
            any(t.state == TaskState.QUEUED for t in tasks_for_session),
            f"states={states}",
        )
        await drain_until_terminal(runtime, max_wait=15.0)
        await drain_until_terminal(runtime, max_wait=15.0)  # drain the second terminal too
        messages = await runtime.sessions.get_messages(q_session.id)
        assistant_count = sum(1 for m in messages if m.role == MessageRole.ASSISTANT)
        check("both turns eventually completed and persisted", assistant_count == 2, f"assistant_count={assistant_count}")

        # ---------------------------------------------------------------
        # #4: cancellation leaves a sensible partial at ANY reasonable
        # cancellation timing -- tried at three different delays, not
        # one precisely-tuned moment.
        # ---------------------------------------------------------------
        print("\n=== #4  cancellation leaves a sensible partial, at several different timings")
        for cancel_after in (0.3, 2.0, 5.0, 8.5):
            c_session = await runtime.sessions.create(
                title=f"b28 cancel@{cancel_after}", provider="demo", model="point-demo-v1",
                project_id=project.id,
            )
            await send_event(runtime, "SendMessage", session=c_session.id, text="please cancel this one")
            await asyncio.sleep(0.1)
            events = await drain(runtime)
            task_id = next((e.get("task_id") for e in events if e["type"] == "TaskStarted"), None)
            await asyncio.sleep(cancel_after)
            await send_event(runtime, "CancelTask", task_id=task_id)
            await drain_until_terminal(runtime, max_wait=15.0)
            partial = await last_assistant_message(runtime, c_session.id)
            text = partial.text.strip() if partial else ""
            sensible = (text == "") or text.endswith(".")
            check(
                f"cancel_after={cancel_after}s: partial is empty or ends at a complete sentence",
                sensible, repr(text[-60:]),
            )

        # ---------------------------------------------------------------
        # #5: @research's 4-step tool loop, real tool.started/completed
        # pairs against the shipped sample project, and exactly 2
        # agent_proposals rows via the REAL _maybe_propose code path
        # (no row inserted directly -- see the task doc's amendment #1).
        # ---------------------------------------------------------------
        print("\n=== #5  @research: 4 steps, real tool calls, 2 proposals via the real path")
        r_session = await runtime.sessions.create(
            title="b28 research", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        await send_event(
            runtime, "SendMessage", session=r_session.id,
            text="summarize the sample project", agent="research",
        )
        events = await drain_until_terminal(runtime, max_wait=30.0)
        progress = [e for e in events if e["type"] == "ProgressUpdated" and "step" in e["payload"].get("data", {})]
        steps_seen = sorted({e["payload"]["data"]["step"] for e in progress})
        tool_started = [e for e in events if e["type"] == "ToolStarted"]
        tool_completed = [e for e in events if e["type"] == "ToolCompleted"]
        check("4 steps observed via ProgressUpdated", steps_seen == [1, 2, 3, 4], f"steps_seen={steps_seen}")
        check(
            "3 real tool.started/tool.completed pairs (list_files, read_file x2)",
            len(tool_started) == 3 and len(tool_completed) == 3,
            f"started={len(tool_started)} completed={len(tool_completed)} "
            f"tools={[e['payload']['data'].get('tool') for e in tool_started]}",
        )
        check(
            "every real tool call succeeded against the shipped sample project",
            all(e["payload"]["data"].get("ok") for e in tool_completed),
            str([e["payload"]["data"] for e in tool_completed]),
        )
        research_reply = await last_assistant_message(runtime, r_session.id)
        check(
            "research's final reply persisted with non-zero usage",
            bool(research_reply) and research_reply.usage.total_tokens > 0,
            str(research_reply.usage.to_dict()) if research_reply else "none",
        )
        proposal_count = await runtime.proposals.count_for_session(r_session.id)
        check(
            "exactly 2 agent_proposals rows, written by the REAL _maybe_propose "
            "path (a second provider.generate() call, confirmed by reading "
            "agents\\executor.py:913-972 before writing this fixture)",
            proposal_count == 2, f"count={proposal_count}",
        )
        proposals = await runtime.proposals.fetch_for_session(r_session.id)
        check(
            "the 2 proposals' content matches research_tool_loop.json's own 'proposals' list "
            "(proving they came from DemoProvider.generate(), not a coincidence)",
            {p.content for p in proposals} == set(
                json.loads(Path("demo/fixtures/research_tool_loop.json").read_text(encoding="utf-8"))["proposals"]
            ),
            str([p.content for p in proposals]),
        )

        # ---------------------------------------------------------------
        # #7/#8: filler-explainer, both modes, against the real shipped
        # sample file -- explain (selection) and fill (cursor).
        # ---------------------------------------------------------------
        print("\n=== #7  @filler-explainer explain mode")
        e_session = await runtime.sessions.create(
            title="b28 explain", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        await send_event(
            runtime, "SendMessage", session=e_session.id, text="explain this",
            agent="filler-explainer",
            editor_context={"file": "app.py", "mode": "explain", "selection": [11, 13]},
        )
        await drain_until_terminal(runtime, max_wait=15.0)
        explain_reply = await last_assistant_message(runtime, e_session.id)
        check(
            "explain mode produced a plain-text reply about add()",
            bool(explain_reply) and "add(a, b)" in explain_reply.text,
            repr(explain_reply.text if explain_reply else None),
        )

        print("\n=== #8  @filler-explainer fill mode -- plain reply, no proposal event, no apply path")
        f_session = await runtime.sessions.create(
            title="b28 fill", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        await send_event(
            runtime, "SendMessage", session=f_session.id, text="add a type check",
            agent="filler-explainer",
            editor_context={"file": "app.py", "mode": "fill", "cursor": 7},
        )
        events = await drain_until_terminal(runtime, max_wait=15.0)
        fill_reply = await last_assistant_message(runtime, f_session.id)
        parsed = None
        try:
            parsed = json.loads(fill_reply.text) if fill_reply else None
        except json.JSONDecodeError:
            pass
        check(
            "fill mode's reply is the JSON shape context.py's prompt asks for",
            isinstance(parsed, dict) and parsed.get("file") == "app.py",
            repr(fill_reply.text if fill_reply else None),
        )
        check(
            "start_line/end_line fall inside the shown region (4-8, computed by "
            "resolve_scope for cursor=7 against app.py's real content)",
            isinstance(parsed, dict) and parsed.get("start_line") == 4 and parsed.get("end_line") == 8,
            str(parsed),
        )
        check(
            "no FillProposal event exists on the wire -- none was emitted, by construction",
            not any(e["type"] == "FillProposal" for e in events),
            str([e["type"] for e in events]),
        )

        # ---------------------------------------------------------------
        # #6: no shipped fixture ever requests web_search -- proven by
        # deliberately violating the guard once, then removing it.
        # ---------------------------------------------------------------
        print("\n=== #6  the web_search guard fires on a deliberately bad fixture, and only then")
        from providers.demo.provider import _forbid_web_search  # noqa: E402

        guard_failed = False
        try:
            _forbid_web_search({"tool_call": {"name": "web_search", "arguments": {}}}, Path("synthetic"))
        except Exception:  # noqa: BLE001 - any raise is the point
            guard_failed = True
        check("a fixture naming web_search is refused at load time", guard_failed)

        real_fixture_dir = Path("demo/fixtures")
        all_clean = True
        for fixture_file in real_fixture_dir.glob("*.json"):
            if "web_search" in fixture_file.read_text(encoding="utf-8"):
                all_clean = False
        check("none of the shipped fixtures actually mention web_search", all_clean)

        # ---------------------------------------------------------------
        # #9: a scripted failure produces TaskFailed with a real-looking
        # provider error, not a raw traceback.
        # ---------------------------------------------------------------
        print("\n=== #9  the error-case fixture produces TaskFailed with a real error")
        err_session = await runtime.sessions.create(
            title="b28 error", provider="demo", model="point-demo-v1", project_id=project.id,
        )
        await send_event(runtime, "SendMessage", session=err_session.id, text="trigger the scripted failure")
        events = await drain_until_terminal(runtime, max_wait=15.0)
        failed = [e for e in events if e["type"] == "TaskFailed"]
        check("TaskFailed was emitted", bool(failed), str([e["type"] for e in events]))
        check(
            "the error text is the scripted message, not a traceback",
            bool(failed) and "scripted failure" in failed[-1]["payload"].get("error", ""),
            str(failed[-1]["payload"]) if failed else "",
        )
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
