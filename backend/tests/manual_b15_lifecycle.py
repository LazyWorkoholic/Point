r"""B15 lifecycle check: unload refuses what is in use, releases what is
not, replays from a seq cursor, and checkpoints a partial reply to disk
while it is still arriving.

Four of B15's seven Done-whens are deterministic and live here. The
other three need a real OS process to kill or signal and live in
`manual_b15_crash.ps1` and `manual_b15_shutdown_wire.ps1`.

  #1  unload does not cancel     a task runs through an unload and
                                 completes; its result is retrievable
  #2  close() on a held context  a no-op that REPORTS its holders, with
                                 their states -- not a bool
  #4  replay from a seq cursor   exactly the messages after the mark, in
                                 order, no gaps
  #5  bounded growth             load/unload cycles leave object counts
                                 and cache sizes flat (the `bench`
                                 command is the full harness; this file
                                 asserts the same property in four)

Plus two things no Done-when names but both depend on:

  * the QUEUED/RUNNING distinction per resource class. B15's Build
    section says "RUNNING or QUEUED" as one rule; `Task.is_active()`
    folds PENDING/QUEUED/RUNNING into one boolean, and the three
    resource classes do NOT agree about which of them block. Asserted
    directly against `LiveReferenceRegistry` with constructed tasks,
    because a QUEUED task with nothing RUNNING cannot be produced
    through the scheduler (a task queues BECAUSE a lane is full).
  * the checkpoint existing mid-stream. Every other check in this
    episode observes a checkpoint by its absence (cleared) or its
    promotion (after a crash). This one looks at the row while the
    stream is still running, which is the only moment it is the only
    copy of that text.

Driven in-process against the mock provider -- no Ollama, no key, no
network. Run from inside the backend directory, against a scratch
profile:

    uv run python tests\manual_b15_lifecycle.py

Exits non-zero if any expectation failed.
"""

from __future__ import annotations

import asyncio
import gc
import os
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

FIXTURES = Path(__file__).resolve().parent / "fixtures"

# Before bootstrap builds the provider registry: the mock is registered
# only when POINT_MOCK is 1.
os.environ["POINT_MOCK"] = "1"
os.environ["POINT_MOCK_FIXTURE"] = str(FIXTURES / "mock_slow.json")

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
from managers.message import MessageRole  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402
from runtime.lifecycle import (  # noqa: E402
    LiveReferenceRegistry,
    Resource,
    ResourceClass,
)
from runtime.task import Task, TaskState  # noqa: E402

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    # The detail prints on FAILURE only: it is written as an explanation
    # of what going wrong would mean, so printing it beside a PASS reads
    # as though the check had failed.
    suffix = ("  -- " + detail) if detail and not ok else ""
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{suffix}")
    if not ok:
        FAILURES.append(label)


class Collector:
    r"""One drain loop for the whole run, and every wait reads from what
    it collected.

    There is deliberately not a second draining helper beside it. The
    first version of this file had both -- a background pump during the
    stream and a `pump(wanted)` that pulled from the outbox directly --
    and the pump silently ate two replies the direct pull was waiting
    for, which failed as "released={}" rather than as "something else
    took your envelope". One reader, one place envelopes arrive.

    Draining itself is not optional: `_emit` awaits `put()` on a bounded
    queue, so a driver that stops reading eventually stalls the runtime
    it is testing.
    """

    def __init__(self, runtime) -> None:
        self._runtime = runtime
        self.seen: list[dict] = []
        self._task = asyncio.create_task(self._loop(), name="b15-collector")

    async def _loop(self) -> None:
        while True:
            event = await self._runtime.outbox.get()
            self.seen.append(event.to_dict())

    async def wait_for(self, types: set[str], limit_s: float = 30.0,
                       after: int = 0) -> dict | None:
        """The first envelope of one of `types` collected at or past
        index `after`. `after` is what makes a second identical request
        answerable: two unloads produce two `WorkspaceUnloaded`
        envelopes, and the second caller must not be handed the first
        one.
        """
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


async def send(runtime, session_id: str, text: str) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "SendMessage",
        "payload": {"session": session_id, "text": text},
    })


async def unload(runtime, collector: Collector, workspace: str) -> dict:
    mark = len(collector.seen)
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "WorkspaceUnload",
        "payload": {"workspace": workspace},
    })
    reply = await collector.wait_for({"WorkspaceUnloaded"}, limit_s=10.0, after=mark)
    return reply["payload"] if reply else {}


# ---------------------------------------------------------------------------
# The refusal rule, at the level the rule lives at
# ---------------------------------------------------------------------------

def fake_task(state: TaskState, session_id: str = "s1") -> Task:
    """A Task object in a chosen state, with no coroutine behind it.

    Constructed rather than produced, because the state combination that
    matters cannot be produced: a task is QUEUED precisely because a
    lane is full, so "one QUEUED task and nothing RUNNING" does not
    occur through the scheduler. The rule still has to be right about
    it -- a lane freed by a cancel, with the queue not yet pumped, is
    exactly that state for a few milliseconds.
    """
    async def never():  # pragma: no cover - never started
        yield {}

    async def sink(_task, _chunk):  # pragma: no cover - never called
        return None

    task = Task(body=never, session_id=session_id, kind="chat", sink=sink)
    task.state = state
    return task


def check_refusal_rule() -> None:
    print("\n[rule] QUEUED and RUNNING are not the same holder")

    def registry_for(*states: TaskState) -> LiveReferenceRegistry:
        tasks = [fake_task(s) for s in states]
        return LiveReferenceRegistry(lambda: tasks)

    def resource(cls: ResourceClass) -> Resource:
        return Resource(
            name=f"r[{cls.value}]", resource_class=cls, detail="test",
            release=lambda: 1,
        )

    running = registry_for(TaskState.RUNNING)
    queued = registry_for(TaskState.QUEUED)
    idle = registry_for(TaskState.COMPLETED, TaskState.CANCELLED)

    check(
        "a RUNNING task blocks all three classes",
        all(running.holders_of(resource(c)) for c in ResourceClass),
        "a running task holds a stream, a provider connection and the project "
        "data its context was built from",
    )
    check(
        "a QUEUED task blocks PROJECT only",
        (
            queued.holders_of(resource(ResourceClass.PROJECT))
            and not queued.holders_of(resource(ResourceClass.STREAM))
            and not queued.holders_of(resource(ResourceClass.PROVIDER))
        ),
        "a queued task holds a lane reservation and nothing else -- no stream "
        "(registered at START, not at submit), no provider client",
    )
    check(
        "a finished task blocks nothing",
        not any(idle.holders_of(resource(c)) for c in ResourceClass),
        "COMPLETED/CANCELLED tasks are not live references",
    )
    holders = queued.holders_of(resource(ResourceClass.PROJECT))
    check(
        "a holder carries its state, not just its id",
        holders and holders[0].state == "queued",
        f"holders={[h.to_dict() for h in holders]} -- 'held by 2 tasks' and "
        "'held by 1 running, 1 queued' are different answers",
    )


# ---------------------------------------------------------------------------
# Live: unload during a stream, replay, checkpoints, bounded growth
# ---------------------------------------------------------------------------

async def main() -> int:
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    collector: Collector | None = None
    try:
        check_refusal_rule()

        project = await runtime.projects.resolve(str(Path(__file__).resolve().parents[2]))
        session = await runtime.sessions.create(
            title="b15", provider="mock", model="mock-slow",
            workspace="chat", project_id=project.id if project else None,
        )
        # Populate the thing an unload releases. Without this the
        # refusal has nothing to refuse and the check proves nothing.
        if project is not None:
            await runtime.projects.list_files(project, limit=10_000, refresh=True)

        print("\n[1] unload during a RUNNING task")
        collector = Collector(runtime)
        await send(runtime, session.id, "count")
        # Let the first chunks land so the task is genuinely RUNNING and
        # the accumulator holds text.
        await asyncio.sleep(1.2)

        states = {t.id: t.state.value for t in runtime.tasks.list_all()}
        check(
            "the task is RUNNING when the unload arrives",
            "running" in states.values(),
            f"task states={states} -- this check is meaningless otherwise",
        )

        checkpoint_rows = await runtime.checkpoints.count()
        check(
            "a checkpoint exists while the reply is still arriving",
            checkpoint_rows >= 1,
            f"{checkpoint_rows} rows -- at 0.3s per chunk and a 0.5s cadence "
            "there should be at least one by 1.2s, and it is the only copy of "
            "that text until the task ends",
        )

        report = await unload(runtime, collector, "chat")

        refused = {r["resource"]: r for r in report.get("refused", [])}
        released = {r["resource"]: r for r in report.get("released", [])}
        check(
            "the project's file index is REFUSED, not released",
            any(name.startswith("file_index") for name in refused),
            f"refused={list(refused)} released={list(released)} -- a running "
            "task's context was built from this project's data",
        )
        check(
            "the refusal names the holder and its state",
            any(
                h["state"] == "running"
                for r in refused.values() for h in r["holders"]
            ),
            f"refused={refused} -- Done-when #2 is this being correct, not "
            "merely non-empty",
        )
        check(
            "the unload is not fully_released while something is held",
            report.get("fully_released") is False,
            f"fully_released={report.get('fully_released')}",
        )
        check(
            "absent resources are reported, not silently skipped",
            {a["resource"] for a in report.get("absent", [])}
            == {"open_buffers", "symbol_parse_caches"},
            f"absent={report.get('absent')} -- B15 names three things to drop "
            "and this backend has one of them",
        )
        check(
            "nothing in the report claims freed memory",
            "rss" not in str(report).lower() and "freed" not in str(report).lower(),
            "CPython does not return arenas to the OS; B15's Do-NOT list "
            "forbids the claim anywhere",
        )

        print("\n[1b] the task survived the unload and completed")
        terminal = await collector.wait_for(
            {"TaskDone", "TaskFailed", "TaskCancelled"}, limit_s=30.0,
        )
        check(
            "the unload did not cancel the task",
            terminal is not None and terminal["type"] == "TaskDone",
            f"terminal={terminal['type'] if terminal else None} -- a CANCELLED "
            "here is the exact failure B15's Do-NOT list leads with",
        )
        rows = await runtime.sessions.get_messages(session.id)
        replies = [m for m in rows if m.role == MessageRole.ASSISTANT]
        check(
            "its result is retrievable afterwards",
            replies and replies[-1].text and not replies[-1].interrupted,
            f"rows={[(m.role.value, m.interrupted) for m in rows]}",
        )
        check(
            "the checkpoint was cleared once the real row existed",
            await runtime.checkpoints.count() == 0,
            f"{await runtime.checkpoints.count()} checkpoint(s) left -- a "
            "survivor is promoted at the next startup into an `interrupted` "
            "row for a task that finished cleanly",
        )

        print("\n[2] unload with nothing running releases")
        if project is not None:
            await runtime.projects.list_files(project, limit=10_000, refresh=True)
        report = await unload(runtime, collector, "chat")
        released = {r["resource"]: r for r in report.get("released", [])}
        index = next(
            (r for name, r in released.items() if name.startswith("file_index")), None
        )
        check(
            "the index is released now that nothing holds it",
            index is not None and index["items"] > 0,
            f"released={released} -- and with a COUNT, which is what makes "
            "bounded growth measurable",
        )
        unknown = await unload(runtime, collector, "no-such-workspace")
        check(
            "an unknown workspace is an empty report, not an error",
            unknown.get("released") == [] and unknown.get("refused") == []
            and unknown.get("sessions") == [],
            f"report={unknown} -- a view Go unloaded that this backend held "
            "nothing for, which is not the same answer as a refusal",
        )

        print("\n[4] replay from a seq cursor")
        mark = len(collector.seen)
        await runtime.receive({
            "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
            "type": "SessionReplaySince",
            "payload": {"session": session.id, "since_seq": 1},
        })
        envelope = await collector.wait_for({"SessionReplay"}, limit_s=10.0, after=mark)
        check("a SessionReplay reply arrived", envelope is not None)
        replay = envelope["payload"] if envelope else {"messages": [], "tasks": []}

        seqs = [m["seq"] for m in replay["messages"]]
        all_seqs = [m.seq for m in rows]
        check(
            "exactly the messages after the mark, in order",
            seqs == sorted(seqs) and seqs == [s for s in all_seqs if s > 1],
            f"replayed {seqs} from a session holding {all_seqs}",
        )
        check(
            "no gaps: consecutive seqs",
            all(b - a == 1 for a, b in zip(seqs, seqs[1:])),
            f"seqs={seqs}",
        )
        check(
            "the mechanism is stated on the wire, not assumed",
            replay.get("mechanism") == "messages.seq" and replay.get("replay") is True,
            f"mechanism={replay.get('mechanism')} replay={replay.get('replay')} "
            "-- this is message-level replay, not B40's event log",
        )
        check(
            "the task's state rides along so an UNKNOWN task can be resolved",
            replay["tasks"] and all("state" in t for t in replay["tasks"]),
            f"tasks={replay.get('tasks')}",
        )

        print("\n[5] bounded growth across load/unload cycles")
        if project is None:
            check("a project was resolvable for the cycle test", False,
                  "run this from inside the repo, which is itself a Point project")
        else:
            session_ids = [session.id]
            gc.collect()
            sizes: set[int] = set()
            dropped: set[int] = set()
            counts: list[int] = []
            for _ in range(4):
                await runtime.projects.list_files(project, limit=10_000, refresh=True)
                sizes.add(len(runtime.projects.index_sizes()))
                rep = await runtime.project_context(project.id, session_ids).close()
                dropped.add(rep.items_dropped)
                gc.collect()
                counts.append(len(gc.get_objects()))
            check(
                "the cache returns to the same size every cycle",
                len(sizes) == 1 and len(dropped) == 1,
                f"index entries per cycle={sizes}, dropped per cycle={dropped}",
            )
            check(
                "object counts do not climb monotonically",
                not all(b > a for a, b in zip(counts, counts[1:])),
                f"objects={counts} -- a monotonic climb is a real leak, and it "
                "is a different thing from RSS not shrinking",
            )

        print("\n[7] a second Shutdown acks again and does not error")
        # Last, because the first Shutdown closes the database: every
        # check above has to have already run against a live runtime.
        acks = []
        for attempt in (1, 2):
            mark = len(collector.seen)
            await runtime.receive({
                "v": ENVELOPE_VERSION, "id": str(uuid.uuid4()),
                "type": "Shutdown", "payload": {},
            })
            envelope = await collector.wait_for(
                {"shutdown.complete", "Error"}, limit_s=15.0, after=mark,
            )
            check(
                f"Shutdown #{attempt} got exactly one terminal ack",
                envelope is not None and envelope["type"] == "shutdown.complete",
                f"got {envelope['type'] if envelope else None} -- before B15 the "
                "handler acked NOTHING, which broke project_idea.md §4's "
                "one-ack-per-command rule",
            )
            acks.append(envelope)
        check(
            "the second ack says it was already stopping",
            acks[1] is not None and acks[1]["payload"].get("already_stopping") is True,
            f"payload={acks[1]['payload'] if acks[1] else None} -- a no-op ack, "
            "not an error, and not a second teardown of a closed database",
        )
        check(
            "the runtime reports itself stopped",
            runtime.state.status.value == "stopped",
            f"status={runtime.state.status.value}",
        )
        check(
            "and it asked the process to exit",
            runtime.shutdown_requested.is_set(),
            "`main.py` waits on this alongside the pipes -- without it a "
            "Shutdown leaves a runtime that rejects everything and never exits",
        )

        print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
        return 0 if not FAILURES else 1
    finally:
        if collector is not None:
            collector.cancel()
        for task in runtime.tasks.list_active():
            task.cancel()
        await asyncio.sleep(0.2)
        await shutdown_runtime(runtime)


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
