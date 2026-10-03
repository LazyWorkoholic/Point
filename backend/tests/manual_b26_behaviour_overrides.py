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

Sections #1b-#1e were added after a live-testing report (during F10b)
that chat.queue_depth's override appeared to have no effect on real
scheduler behavior. Traced and reproduced: the override mechanism
itself was never broken -- `config_value_live()` is consulted fresh on
every check at every one of the five call sites, and there is only one
`_OVERRIDES` dict, imported identically everywhere. Two independent
test-methodology confounds produced the appearance of a bug:
  - #1b (chat.queue_depth): turn-lane occupancy is CUMULATIVE across
    sends until a task genuinely finishes -- testing two depths back
    to back in the SAME session, without letting the first burst's
    turns drain, starts the second burst from whatever occupancy the
    first one left behind, which can make a LARGER depth look like it
    has no effect.
  - #1c (limits.max_concurrent_tasks): the "echo" provider is
    deliberately exempt from the provider lane entirely
    (agents\executor.py's run()), so testing a concurrency cap against
    it can never observe one. "mock" (POINT_MOCK=1) is used instead.
#1d (research.stale_after_days) and #1e (project.allow_outside_project)
passed cleanly on the first attempt -- recorded here as permanent
regression coverage anyway, now that #1b/#1c's confounds are known, so
a NINTH override key or a changed call site has something real to run
against rather than "it should work."
"""

from __future__ import annotations

import asyncio
import json
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import os
import tempfile
import time

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402
from runtime.scheduler import provider_lane  # noqa: E402
from runtime.task import TaskState  # noqa: E402

# #1c's own fixture, sized deliberately for its own timing -- NOT
# mock_slow.json (6s: the first version of #1c used it and flaked
# intermittently, because 6s is also far longer than this test's
# inter-phase drain allowed, leaving the cap=1 phase's tasks still
# running when the cap=3 phase began and corrupting its occupancy
# count) and NOT the 0.25s default mock_stream.json either (too SHORT
# -- a task could finish between this script's own send+drain round
# trip and the moment it checks task.state, making the two sessions
# never actually overlap, which would make the cap=1 "queues behind"
# assertion pass by accident rather than by actually observing
# contention). ~1.2s is comfortably longer than one send+drain round
# trip and comfortably shorter than the phases' own inter-phase sleep.
#
# MockProvider reads POINT_MOCK_FIXTURE exactly once, in its own
# __init__ -- which runs inside build_runtime()'s
# build_registry_from_env(), so this must be set, and the file must
# already exist, before build_runtime() is ever called.
_mock_fixture = tempfile.NamedTemporaryFile(
    mode="w", suffix=".json", delete=False, encoding="utf-8",
)
json.dump({
    "model": "mock-1c",
    "chunks": [{"type": "text", "text": f"word{i} ", "delay": 0.15} for i in range(8)]
               + [{"type": "done"}],
}, _mock_fixture)
_mock_fixture.close()

os.environ["POINT_MOCK"] = "1"
os.environ["POINT_MOCK_FIXTURE"] = _mock_fixture.name

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
        # #1b (added after a live-testing report that queue_depth's
        # override appeared to have "no effect" -- the refusal point sat
        # at the same message regardless of whether the override was 2 or
        # 5). Traced and reproduced: `turn_queue_depth()` DOES call
        # `config_value_live()` fresh on every check (runtime\scheduler.py
        # itself is unchanged by that trace), and there is only ONE
        # `_OVERRIDES` dict (appconfig_overrides.py), imported identically
        # everywhere -- no second, desynced copy of the override store,
        # the class of bug this project has hit before (DiagnosticsMsg.Seq,
        # taskCardWasVisible). The actual explanation: occupancy in a turn
        # lane is CUMULATIVE across sends until a task genuinely finishes
        # (scheduler.py's own `_release_when_done`), so testing "depth=2"
        # and "depth=5" back to back IN THE SAME SESSION, WITHOUT letting
        # the first burst's own turns finish, starts the second burst from
        # whatever occupancy the first one left behind -- which can make a
        # LARGER depth look like it has no effect purely because the lane
        # was already most of the way to ITS ceiling before the second
        # burst's first message was even sent. This is the queue behaving
        # exactly as designed (occupancy = running + queued, B12's own
        # rule); it only looks like "no effect" when two bursts share one
        # lane with nothing draining between them.
        #
        # This check uses a FRESH session per depth specifically to rule
        # that confound out, and asserts the refusal point at two
        # DIFFERENT depths rather than only ever proving depth=0 moves it
        # off depth=1 (which #1 above already did, and which cannot by
        # itself distinguish "the override governs the ceiling" from "the
        # override merely turns some OTHER hardcoded low ceiling on").
        # ---------------------------------------------------------------
        print("\n=== #1b  the refusal point ACTUALLY MOVES between two different depths")

        async def burst_and_find_refusal(depth: int, expected_ceiling: int) -> None:
            fresh = await runtime.sessions.create(
                title=f"b26 depth={depth}", provider="echo", model="echo",
            )
            await send_event(runtime, "SetBehaviourOverride", key="chat.queue_depth", value=depth)
            await drain(runtime)

            # expected_ceiling + 1 sends, EVERY ONE a long prompt, not just
            # the first -- echo's reply is word-chunked at a fixed pace
            # per word (agents\executor.py's _echo_body), so a SHORT
            # message ("message 2") finishes and frees its slot in well
            # under 100ms. With depth>=5 the burst needs 6+ messages still
            # occupying the lane by the time the last one arrives, and a
            # short filler text finishes faster than this loop's own
            # send+drain round trip -- the first version of this check
            # used short filler text for messages 2+ and the occupancy
            # drained out from under it before the burst finished, which
            # is a flaw in the TEST's own timing, not a sign the override
            # stopped working (depth=2's shorter burst happened to still
            # pass by coincidence). Every message long enough to still be
            # streaming for the whole burst removes that confound --
            # 60 words at echo's own 0.05s/word pacing is 3s, comfortably
            # longer than seven sequential send+drain round trips even
            # under load (an earlier, 14-word version of this text was
            # *itself* intermittently flaky for exactly this margin
            # reason: 0.7s of streaming against ~0.7s of loop overhead is
            # not a real margin, it is a coin flip).
            long_text = "river mountain ocean forest " * 15
            texts = [long_text] * (expected_ceiling + 1)
            refused_at = None
            for i, text in enumerate(texts, start=1):
                await send_event(runtime, "SendMessage", session=fresh.id, text=text)
                events = await drain(runtime)
                codes = [e["payload"].get("code") for e in events if e["type"] == "Error"]
                if "queue_full" in codes and refused_at is None:
                    refused_at = i

            check(
                f"depth={depth}: refusal lands at message {expected_ceiling + 1} "
                f"(ceiling {expected_ceiling} = 1 running + {depth} waiting), not earlier",
                refused_at == expected_ceiling + 1,
                f"refused_at={refused_at}, expected={expected_ceiling + 1}",
            )

            # Drain fully before the NEXT depth's fresh session starts --
            # this is the one rule a methodologically clean comparison
            # needs that the original live-testing report's own repro
            # skipped (see this section's docstring above).
            await asyncio.sleep(0.5)
            await drain(runtime)

        await burst_and_find_refusal(depth=2, expected_ceiling=3)
        await burst_and_find_refusal(depth=5, expected_ceiling=6)

        await send_event(runtime, "SetBehaviourOverride", key="chat.queue_depth")
        await drain(runtime)

        # ---------------------------------------------------------------
        # #1c (added alongside #1b): is the queue_depth confound
        # systemic to config_value_live()/the override store, or
        # specific to scheduler.py's two call sites? Same methodology,
        # a DIFFERENT key, governing a DIFFERENT lane (provider, not
        # turn).
        #
        # Deliberately NOT the "echo" provider: agents\executor.py's
        # run() explicitly exempts echo from the provider lane
        # ("a local dev path with no resource to contend for, and
        # capping it would make the mock slower than the real thing"),
        # so limits.max_concurrent_tasks can never show an effect
        # against it -- this test's own first draft used echo and
        # looked exactly like the queue_depth bug (cap=1 let a second
        # session's send run immediately), which was that SAME
        # documented exemption, not a second bug. "mock" is a real
        # registered provider for lane purposes.
        # ---------------------------------------------------------------
        print("\n=== #1c  limits.max_concurrent_tasks governs the PROVIDER lane across sessions")
        long_text = "write a very long essay about rivers and mountains and oceans and forests please"
        for cap, expect_queued in ((1, True), (3, False)):
            await send_event(runtime, "SetBehaviourOverride", key="limits.max_concurrent_tasks", value=cap)
            await drain(runtime)

            s1 = await runtime.sessions.create(title=f"b26 cap={cap}-a", provider="mock", model="mock")
            s2 = await runtime.sessions.create(title=f"b26 cap={cap}-b", provider="mock", model="mock")
            await send_event(runtime, "SendMessage", session=s1.id, text=long_text)
            await drain(runtime)
            await send_event(runtime, "SendMessage", session=s2.id, text=long_text)
            await drain(runtime)

            task2_state = next(
                (t.state for t in runtime.tasks.list_all() if t.session_id == s2.id), None
            )
            check(
                f"cap={cap}: a second session's send is "
                f"{'queued behind' if expect_queued else 'admitted alongside'} the first",
                (task2_state is TaskState.QUEUED) == expect_queued,
                f"task2.state={task2_state}, running_in(provider_lane)={runtime.tasks.scheduler.running_in(provider_lane('mock'))}",
            )
            # The fixture's own ~1.2s total must be fully finished before
            # the NEXT cap's phase starts, or its leftover occupancy
            # corrupts that phase's count -- the exact confound #1b's own
            # docstring names, now guarded against here with a margin.
            await asyncio.sleep(2.0)
            await drain(runtime)

        await send_event(runtime, "SetBehaviourOverride", key="limits.max_concurrent_tasks")
        await drain(runtime)

        # ---------------------------------------------------------------
        # #1d: research.stale_after_days, tested directly against
        # Runtime._build_reference_index rather than a real research
        # run (no network/tools needed to exercise the staleness MATH
        # itself) -- a synthetic COMPLETED task row, 10 days old,
        # inserted the same "write the required state directly" way
        # F9a did for state a later episode owned.
        # ---------------------------------------------------------------
        print("\n=== #1d  research.stale_after_days changes which past results are flagged stale")
        stale_session = await runtime.sessions.create(title="b26 staleness", provider="mock", model="mock")
        research_agent = await runtime.agents.get("research")
        ten_days_ago = time.time() - 10 * 86400
        await runtime.task_repo.record(
            task_id=str(uuid.uuid4()), session_id=stale_session.id, kind="agent", status="completed",
            created_at=ten_days_ago, started_at=ten_days_ago, finished_at=ten_days_ago,
            agent_id=research_agent.id,
        )
        for stale_after, expect_flagged in ((5, True), (15, False)):
            await send_event(runtime, "SetBehaviourOverride", key="research.stale_after_days", value=stale_after)
            await drain(runtime)
            text = await runtime._build_reference_index(stale_session, research_agent)
            flagged = text is not None and "older than" in text
            check(
                f"stale_after_days={stale_after}: a 10-day-old result is "
                f"{'flagged' if expect_flagged else 'NOT flagged'} stale",
                flagged == expect_flagged,
                repr(text),
            )
        await send_event(runtime, "SetBehaviourOverride", key="research.stale_after_days")
        await drain(runtime)

        # ---------------------------------------------------------------
        # #1e: project.allow_outside_project, tested against a REAL
        # AttachContext call -- a project bound to a scratch root, and a
        # file genuinely outside it. Clarification (asked for
        # explicitly): this key does NOT affect the @ picker's
        # fuzzy-vs-browse mode, which is frontend-only (session
        # project-binding and explicit @?/leading-? commands) and reads
        # nothing from this override. What it DOES gate is exactly this
        # attach/read guard -- `managers\pathguard.py`'s resolve_path,
        # consulted at attach time via Runtime._attachment_scope.
        # ---------------------------------------------------------------
        print("\n=== #1e  project.allow_outside_project gates attaching a file outside the project root")
        with tempfile.TemporaryDirectory() as tmp:
            project_root = Path(tmp) / "proj"
            project_root.mkdir()
            outside = Path(tmp) / "outside.txt"
            outside.write_text("outside content", encoding="utf-8")

            project = await runtime.projects.init(str(project_root))
            attach_session = await runtime.sessions.create(
                title="b26 outside-project", provider="mock", model="mock", project_id=project.id,
            )

            async def try_attach() -> tuple[bool, list[str]]:
                await send_event(runtime, "AttachContext", session=attach_session.id, path=str(outside))
                events = await drain(runtime)
                attached = [e for e in events if e["type"] == "Attachment"]
                errs = [e["payload"].get("error", "") for e in events if e["type"] == "Error"]
                for a in attached:
                    await send_event(runtime, "DetachContext", session=attach_session.id,
                                      attachment_id=a["payload"]["attachment"]["id"])
                    await drain(runtime)
                return bool(attached), errs

            await send_event(runtime, "SetBehaviourOverride", key="project.allow_outside_project", value=False)
            await drain(runtime)
            ok, errs = await try_attach()
            check("override=false: an outside-project attach is refused", not ok, str(errs))

            await send_event(runtime, "SetBehaviourOverride", key="project.allow_outside_project", value=True)
            await drain(runtime)
            ok, errs = await try_attach()
            check("override=true: the SAME attach now succeeds", ok, str(errs))

            await send_event(runtime, "SetBehaviourOverride", key="project.allow_outside_project")
            await drain(runtime)
            ok, errs = await try_attach()
            check("clearing the override: the SAME attach is refused again", not ok, str(errs))

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
    try:
        sys.exit(asyncio.run(main()))
    finally:
        os.unlink(_mock_fixture.name)
