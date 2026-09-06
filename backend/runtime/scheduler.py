r"""TaskScheduler: the thing that makes concurrency honest (B12).

The rule the UI depends on: **nothing is rejected for being over a
concurrency cap, and nothing lies about running.** A task that cannot
start yet enters `QUEUED`, is visible as queued, is cancellable while
queued, and starts by itself when a slot frees. The alternative -- start
everything and let the provider serialize it -- produces a UI claiming
three research runs are in flight while Ollama processes one, which is
the specific dishonesty this file exists to remove.

## Two lanes, not one

    ("provider", "ollama")   capacity from [limits.ollama], else [limits]
    ("turn", <session_id>)   capacity 1, conversational turns only

A task waits for a slot in **every** lane it names. Agent tasks take
only a provider lane; a plain conversational turn takes both. That is
what "agent tasks never enter this queue" means concretely: three
background research runs can saturate the provider lane without the
user losing the ability to ask a question, and a slow question cannot
jump ahead of research already waiting.

## First-fit, not strict FIFO

The queue is scanned in submission order and the first task whose lanes
are *all* free is started. Order is therefore preserved **within** a
lane, which is the only place order is meaningful, while a chat turn
blocked on its session's turn lane does not also block an unrelated
research task behind it. Strict FIFO across lanes would make one stuck
session able to stall the whole runtime -- head-of-line blocking between
things that share no resource.

## In-process, deliberately

The cap is a per-process semaphore, and that is correct rather than a
shortcut: `lockfile.py` makes the backend single-instance, so one
process owns every task in the app. A second `cli.py` is a second
*process* by design (WAL exists so it can read and write alongside a
live backend), and it is not covered by this cap -- driving three sends
through three CLI processes bypasses the scheduler entirely and will
show three RUNNING. That is a property of the test, not a hole in the
cap: verify the cap through one backend, which is the only topology the
product actually has. Making the cap hold across processes would mean
inventing lease expiry and crash recovery for a distributed lock the
product has no use for.
"""

from __future__ import annotations

import asyncio
from typing import Callable, Iterable

import appconfig
from logging_setup import get_logger
from runtime.task import Task, TaskState

_log = get_logger("scheduler")

# A lane is (kind, id). Two kinds exist; both are strings so a third can
# be added without a new type.
LaneKey = tuple[str, str]

LANE_PROVIDER = "provider"
LANE_TURN = "turn"

# `[limits] max_concurrent_tasks`, the fallback for every provider that
# does not name its own.
DEFAULT_MAX_CONCURRENT = 3

# One conversational turn in flight per session. Not configurable:
# `chat.queue_depth` configures how many may *wait*, which is a UI
# question, while "two replies streaming into the same transcript at
# once" is not a thing the transcript can represent.
TURN_LANE_CAPACITY = 1


def provider_capacity(provider: str) -> int:
    r"""`[limits.<provider>] max_concurrent_tasks`, falling back to
    `[limits] max_concurrent_tasks`, falling back to 3.

    The per-provider override exists because the right number is a
    property of the provider, not of the app: Ollama runs local
    inference that is serialized anyway, so a cap of 1 makes the queue
    honest instead of pretending three requests are progressing. A
    hosted provider has no such constraint.
    """
    specific = appconfig.config_value(f"limits.{provider}.max_concurrent_tasks", None)
    general = appconfig.config_value("limits.max_concurrent_tasks", DEFAULT_MAX_CONCURRENT)
    for value, source in ((specific, f"limits.{provider}"), (general, "limits")):
        if value is None:
            continue
        try:
            parsed = int(value)
        except (TypeError, ValueError):
            _log.warning(
                "config %s.max_concurrent_tasks is not an integer (%r); ignoring",
                source, value,
            )
            continue
        if parsed < 1:
            _log.warning(
                "config %s.max_concurrent_tasks must be >= 1 (got %d); ignoring",
                source, parsed,
            )
            continue
        return parsed
    return DEFAULT_MAX_CONCURRENT


def capacity_for(lane: LaneKey) -> int:
    kind, name = lane
    if kind == LANE_TURN:
        return TURN_LANE_CAPACITY
    return provider_capacity(name)


class TaskScheduler:
    """Admission control in front of `Task.start()`.

    Holds no database handle and knows nothing about sessions, agents,
    or providers beyond the opaque lane strings it is handed --
    `TaskManager` owns task lifetime, this owns *when* a task is allowed
    to begin, and neither owns what the task does.
    """

    def __init__(self, capacity: Callable[[LaneKey], int] = capacity_for) -> None:
        self._capacity = capacity
        # Lane -> the ids currently occupying a slot in it.
        self._running: dict[LaneKey, set[str]] = {}
        # Submission order. Entries are (task, lanes); dropped on pump
        # once the task is no longer QUEUED (i.e. it was cancelled).
        self._waiting: list[tuple[Task, tuple[LaneKey, ...]]] = []
        # The watcher coroutines that release lanes. Held so a
        # short-lived process cannot garbage-collect one mid-flight.
        self._watchers: set[asyncio.Task] = set()

    # -- submission --------------------------------------------------------

    def submit(self, task: Task, lanes: Iterable[LaneKey]) -> None:
        """Start `task` now, or queue it. Never blocks, never rejects.

        Rejection is the caller's decision and happens earlier: the turn
        queue's depth limit is enforced at the SendMessage boundary,
        where a `queue_full` refusal can still be a clean answer to a
        request. Once a task exists, this file's contract is that it
        will run.
        """
        keys = tuple(lanes)
        if self._has_room(keys):
            self._start(task, keys)
            return
        task.state = TaskState.QUEUED
        task._record_soon()
        self._waiting.append((task, keys))
        _log.info(
            "Task %s queued behind %s",
            task.id, ", ".join(f"{k}:{v}" for k, v in keys),
        )

    # -- introspection, for the admission decision above -------------------

    def running_in(self, lane: LaneKey) -> int:
        return len(self._running.get(lane, ()))

    def waiting_in(self, lane: LaneKey) -> int:
        return sum(1 for task, keys in self._waiting if lane in keys and task.state is TaskState.QUEUED)

    def occupancy(self, lane: LaneKey) -> int:
        """Running plus queued. This is the number the turn queue's depth
        check compares against, because a user asking "can I send?" is
        asking about everything already ahead of them, not only what is
        moving."""
        return self.running_in(lane) + self.waiting_in(lane)

    # -- internals ---------------------------------------------------------

    def _has_room(self, lanes: tuple[LaneKey, ...]) -> bool:
        return all(self.running_in(lane) < self._capacity(lane) for lane in lanes)

    def _start(self, task: Task, lanes: tuple[LaneKey, ...]) -> None:
        for lane in lanes:
            self._running.setdefault(lane, set()).add(task.id)
        task.start()
        watcher = asyncio.get_event_loop().create_task(
            self._release_when_done(task, lanes), name=f"lane-release-{task.id}"
        )
        self._watchers.add(watcher)
        watcher.add_done_callback(self._watchers.discard)

    async def _release_when_done(self, task: Task, lanes: tuple[LaneKey, ...]) -> None:
        """Hold the slots until `_run()` has genuinely finished.

        `Task.wait()` rather than awaiting the asyncio task directly: it
        swallows the CancelledError a cancelled task re-raises at its
        awaiter, and it guarantees `_run()`'s cleanup (the terminal sink
        call, the recorder write) has completed. Releasing a slot before
        that would let the next task start while the previous one is
        still writing its own terminal state -- which is how a cap of 1
        ends up with two tasks briefly overlapping.
        """
        try:
            await task.wait()
        finally:
            for lane in lanes:
                occupants = self._running.get(lane)
                if occupants is not None:
                    occupants.discard(task.id)
                    if not occupants:
                        self._running.pop(lane, None)
            self._pump()

    def _pump(self) -> None:
        """Start every waiting task whose lanes have all come free.

        Runs to a fixed point rather than starting one task per release:
        freeing a provider slot can unblock a task that also needed a
        turn slot which was already free, and stopping after the first
        start would leave the second waiting for an unrelated event that
        may never come.
        """
        while True:
            started = False
            for index, (task, lanes) in enumerate(list(self._waiting)):
                if task.state is not TaskState.QUEUED:
                    # Cancelled while queued. Its terminal state and
                    # terminal event were written by Task.cancel().
                    self._waiting.remove((task, lanes))
                    started = True
                    break
                if self._has_room(lanes):
                    self._waiting.remove((task, lanes))
                    _log.info("Task %s dequeued, starting", task.id)
                    self._start(task, lanes)
                    started = True
                    break
            if not started:
                return

    async def drain(self, timeout: float = 5.0) -> None:
        """Wait (bounded) for the lane-release watchers.

        Same reason `AgentExecutor.drain_background()` exists: a
        short-lived CLI process's `asyncio.run()` cancels whatever is
        still pending the moment its own coroutine returns, and a
        watcher killed mid-release leaves a lane permanently occupied in
        a runtime that is about to exit anyway -- harmless there, but it
        also means the release's `_pump()` never ran, so a queued task
        silently never starts.
        """
        pending = list(self._watchers)
        if not pending:
            return
        try:
            await asyncio.wait_for(
                asyncio.gather(*pending, return_exceptions=True), timeout=timeout,
            )
        except asyncio.TimeoutError:
            _log.warning("Timed out waiting for %d lane watcher(s)", len(pending))


def turn_queue_depth() -> int:
    r"""`chat.queue_depth`, default 1.

    How many conversational turns may WAIT behind the one in flight.
    Depth 1 therefore means: one streaming, one queued, and a third
    refused with `queue_full`. It is not the number that may run --
    that is fixed at one per session by `TURN_LANE_CAPACITY`.
    """
    value = appconfig.config_value("chat.queue_depth", 1)
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        _log.warning("config chat.queue_depth is not an integer (%r); using 1", value)
        return 1
    if parsed < 0:
        _log.warning("config chat.queue_depth must be >= 0 (got %d); using 1", parsed)
        return 1
    return parsed


def provider_lane(provider: str) -> LaneKey:
    return (LANE_PROVIDER, provider)


def turn_lane(session_id: str) -> LaneKey:
    return (LANE_TURN, session_id)
