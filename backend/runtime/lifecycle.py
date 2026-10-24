r"""Release what is no longer used; refuse to release what is in use (B15).

This file is the refusal rule, and nothing else. It holds no database
handle, builds no context, and knows nothing about workspaces as a UI
concept -- it is handed a set of live tasks and a set of releasers, and
it decides which releasers are allowed to run.

## What "workspace" means here, and what it does not

**The backend has no workspace registry and this episode did not invent
one.** `sessions.workspace` is a nullable grouping LABEL (see
`managers\session.py`: 'e.g. a project folder or "chat" vs "coding"
workspace in the Go TUI'); nothing is keyed by it. So of the three
things B15's Build section lists for `WorkspaceContext.close()`:

  * **file tree** -- real, but it is `ProjectManager._index_cache`,
    keyed by PROJECT id with a 30s TTL. Reachable from a workspace only
    by resolving workspace -> sessions -> project ids, which is what
    `WorkspaceContext` does.
  * **open buffers** -- live in Go (`frontend\internal\editor\`). The
    backend holds none. Its only editor-adjacent state is
    `StreamManager._editor_contexts`, keyed by TASK id and already
    cleared by `close_stream()` at the terminal event.
  * **symbol/parse caches** -- do not exist. `editor_context.py` is
    "EditorContext + resolve_scope (no parser)" and this episode did not
    add one.

The report says all of that out loud rather than reporting an empty
success, because "unload dropped 0 of 3 things and did not say which"
is the shape of a feature nobody can tell is broken.

## QUEUED is not RUNNING

B12 added `TaskState.QUEUED` and `Task.is_active()` folds PENDING,
QUEUED and RUNNING into one boolean. That boolean is the wrong input for
this decision, because the two states hold different things:

    RUNNING   a provider connection, an open stream, an accumulator, a
              registered EditorContext, and the project data its context
              was built from
    QUEUED    a lane reservation and its own `tasks` row. No stream (the
              executor calls `register_stream` when it STARTS, not when
              it is submitted), no provider client, no accumulator

So a release decision is made per RESOURCE CLASS, not per task:

    STREAM     refused by a RUNNING holder only. A queued task has no
               stream to tear out from under it.
    PROVIDER   refused by a RUNNING holder only -- same reason. Closing
               an idle pool while a task queues behind it is exactly the
               case closing one is FOR.
    PROJECT    refused by RUNNING *and* QUEUED holders. A queued turn's
               context is built from this project's data when it starts;
               dropping the index now is correct-but-wasteful (the next
               `list_files` rebuilds it), and reporting it as released
               while something is still waiting to use it is the kind of
               half-truth this episode exists to remove.

PENDING is treated as RUNNING. It is the sub-millisecond gap between
construction and the loop's next turn (`task.py`'s own note), and a task
that is about to hold a stream must not have it dropped in that window.
"""

from __future__ import annotations

import inspect
from dataclasses import dataclass, field
from enum import Enum
from typing import Awaitable, Callable, Iterable, Optional, Union

from logging_setup import get_logger
from runtime.task import Task, TaskState

_log = get_logger("lifecycle")


class ResourceClass(str, Enum):
    """What kind of thing a releaser would free. The refusal rule is a
    function of this and the holder's state, never of the holder alone."""

    STREAM = "stream"
    PROVIDER = "provider"
    PROJECT = "project"


# Which task states block a release of each class. See the module
# docstring for why these differ rather than all reading `is_active()`.
_BLOCKING_STATES: dict[ResourceClass, tuple[TaskState, ...]] = {
    ResourceClass.STREAM: (TaskState.PENDING, TaskState.RUNNING),
    ResourceClass.PROVIDER: (TaskState.PENDING, TaskState.RUNNING),
    ResourceClass.PROJECT: (TaskState.PENDING, TaskState.QUEUED, TaskState.RUNNING),
}


@dataclass(frozen=True)
class Holder:
    """One live task standing in the way of a release, with the state
    that made it count. The state is carried because "held by 2 tasks"
    and "held by 1 running, 1 queued" are different answers to the
    question the user actually asked."""

    task_id: str
    state: str
    session_id: str
    kind: str

    def to_dict(self) -> dict:
        return {
            "task": self.task_id,
            "state": self.state,
            "session": self.session_id,
            "kind": self.kind,
        }


@dataclass(frozen=True)
class Released:
    """One thing that was actually dropped, with a number beside it. The
    number is what makes the bounded-growth harness meaningful -- a
    release that reports `true` tells you nothing across 20 cycles."""

    resource: str
    resource_class: ResourceClass
    detail: str
    items: int = 0

    def to_dict(self) -> dict:
        return {
            "resource": self.resource,
            "class": self.resource_class.value,
            "detail": self.detail,
            "items": self.items,
        }


@dataclass(frozen=True)
class Refused:
    """One thing that was NOT dropped, and every holder that blocked it.
    `close()` on a context holding a running task's resources is a no-op
    that reports what it is holding -- B15's Done-when #2 is this type
    being non-empty and correct."""

    resource: str
    resource_class: ResourceClass
    holders: tuple[Holder, ...]

    def to_dict(self) -> dict:
        return {
            "resource": self.resource,
            "class": self.resource_class.value,
            "holders": [h.to_dict() for h in self.holders],
        }


@dataclass(frozen=True)
class Absent:
    """A resource B15's Build section names that this backend does not
    have. Reported rather than silently skipped: see the module
    docstring. An absent resource is neither released nor refused, and
    counting it as either would be a lie in one direction or the other.
    """

    resource: str
    reason: str

    def to_dict(self) -> dict:
        return {"resource": self.resource, "reason": self.reason}


@dataclass
class ReleaseReport:
    """What `close()` returns. Never a bool: the whole point of the
    refusal rule is that a caller can tell "nothing to do" from "I am
    not allowed to do it", and from "that thing does not exist here".

    **No RSS claim anywhere, deliberately.** CPython does not reliably
    return arenas to the OS, so `items` counts objects dropped and
    nothing in this type, its serialization, or any log line it produces
    says a byte of memory was returned (B15's "Do NOT", and CLAUDE.md's
    measured-claims rule).
    """

    target: str
    released: list[Released] = field(default_factory=list)
    refused: list[Refused] = field(default_factory=list)
    absent: list[Absent] = field(default_factory=list)

    @property
    def fully_released(self) -> bool:
        return not self.refused

    @property
    def items_dropped(self) -> int:
        return sum(r.items for r in self.released)

    def to_dict(self) -> dict:
        return {
            "target": self.target,
            "released": [r.to_dict() for r in self.released],
            "refused": [r.to_dict() for r in self.refused],
            "absent": [a.to_dict() for a in self.absent],
            "fully_released": self.fully_released,
            "items_dropped": self.items_dropped,
        }

    def describe(self) -> str:
        parts = [f"{self.target}:"]
        if self.released:
            parts.append(
                "released " + ", ".join(f"{r.resource}({r.items})" for r in self.released)
            )
        if self.refused:
            parts.append(
                "refused " + ", ".join(
                    f"{r.resource}[{', '.join(h.state for h in r.holders)}]"
                    for r in self.refused
                )
            )
        if self.absent:
            parts.append("absent " + ", ".join(a.resource for a in self.absent))
        if len(parts) == 1:
            parts.append("nothing to release")
        return " ".join(parts)


# A releaser drops one resource and returns how many things it dropped.
# It is only ever called when the refusal rule has already allowed it.
#
# Sync or async, because the two real releasers differ: dropping a cache
# entry is a dict `pop`, and closing an SDK client's connection pool is
# `await client.close()`. Forcing either into the other's shape would
# mean a thread hop for a dict operation or a fire-and-forget coroutine
# whose result the report then cannot count.
Releaser = Callable[[], Union[int, Awaitable[int]]]


@dataclass(frozen=True)
class Resource:
    """One releasable thing: what it is called, what class it belongs to,
    which live tasks could be holding it, and how to drop it."""

    name: str
    resource_class: ResourceClass
    detail: str
    release: Releaser
    # Which of this release's candidate tasks actually touch THIS
    # resource. A project's index is held by the tasks in that project's
    # sessions; a provider pool is held by the tasks running against
    # that provider. Returning every live task here would make one busy
    # session refuse every unrelated release.
    holder_filter: Callable[[Task], bool] = lambda task: True


class LiveReferenceRegistry:
    """The registry of live references, computed on demand rather than
    maintained incrementally.

    Derived, not tracked: `TaskManager._tasks` already IS the live set
    (B6's note -- the `tasks` table is its durable mirror, not a second
    lifecycle), and a second copy updated by hand on every transition is
    a second copy that can disagree. The one thing it has to be is
    correct at the instant `close()` is called, and a read of the real
    set is the cheapest way to be exactly that.
    """

    def __init__(self, live_tasks: Callable[[], Iterable[Task]]) -> None:
        self._live_tasks = live_tasks

    def holders_of(self, resource: Resource) -> tuple[Holder, ...]:
        blocking = _BLOCKING_STATES[resource.resource_class]
        return tuple(
            Holder(
                task_id=task.id,
                state=task.state.value,
                session_id=task.session_id,
                kind=task.kind,
            )
            for task in self._live_tasks()
            if task.state in blocking and resource.holder_filter(task)
        )

    async def release(self, target: str, resources: Iterable[Resource],
                      absent: Iterable[Absent] = ()) -> ReleaseReport:
        """Run every releaser the rule allows; report the rest.

        A refusal is a no-op, not an error: unloading a view is a UI
        event and the task is in another process's lifetime, so refusing
        must leave the caller with a normal answer it can show, never an
        exception it has to catch.
        """
        report = ReleaseReport(target=target, absent=list(absent))
        for resource in resources:
            holders = self.holders_of(resource)
            if holders:
                report.refused.append(
                    Refused(resource.name, resource.resource_class, holders)
                )
                _log.info(
                    "Release of %s refused: held by %s",
                    resource.name,
                    ", ".join(f"{h.task_id[:8]}({h.state})" for h in holders),
                )
                continue
            try:
                dropped = resource.release()
                if inspect.isawaitable(dropped):
                    dropped = await dropped
            except Exception:  # noqa: BLE001
                # A releaser that throws must not take the whole unload
                # with it: the remaining resources are independent, and
                # a half-run unload that reports success for what it did
                # not do is worse than one that reports the failure.
                _log.exception("Releaser for %s failed", resource.name)
                report.refused.append(
                    Refused(resource.name, resource.resource_class, ())
                )
                continue
            report.released.append(
                Released(
                    resource.name, resource.resource_class, resource.detail,
                    items=int(dropped),
                )
            )
        return report


class ProjectContext:
    """The project-scoped half of an unload: cached project data and the
    B7 file index.

    There is exactly one cache in the backend to drop
    (`ProjectManager._index_cache`, verified by grepping for every
    `_cache`/`lru_cache` in `managers\\`, `agents\\`, `tools\\` and
    `runtime\\`), so this class is thin on purpose. It exists as a named
    thing because B15's Build section names it and because B16 gives
    each SSH connection its own runtime with this same lifecycle -- a
    release path reached through a type is one B16 can instantiate per
    connection, where a method on ProjectManager is not.
    """

    def __init__(self, project_id: str, projects, registry: LiveReferenceRegistry,
                 session_ids: Optional[Iterable[str]] = None) -> None:
        self.project_id = project_id
        self._projects = projects
        self._registry = registry
        # Which sessions belong to this project, so a task in an
        # unrelated session does not refuse this release.
        self._session_ids = set(session_ids or ())

    def _resources(self) -> list[Resource]:
        return [
            Resource(
                name=f"file_index[{self.project_id[:8]}]",
                resource_class=ResourceClass.PROJECT,
                detail="ProjectManager._index_cache (B7's file index, 30s TTL)",
                release=lambda: self._projects.release_index(self.project_id),
                holder_filter=lambda task: task.session_id in self._session_ids,
            ),
        ]

    async def close(self) -> ReleaseReport:
        return await self._registry.release(
            target=f"project:{self.project_id}", resources=self._resources()
        )


class WorkspaceContext:
    """The backend-side unload of one workspace, keyed by workspace id.

    `workspace.unload("chat")` arrives from Go naming a workspace. The
    backend resolves that to sessions (`sessions.workspace == id`), and
    those to project ids, and releases what is project-scoped plus the
    provider pools nothing is streaming through. The wire shape is the
    one F12 and B16 want; the resolution is this class's job, because
    nothing else in the backend knows that a workspace label implies a
    set of sessions.

    A workspace id that matches no session is not an error. It is a view
    Go unloaded that this backend never held anything for, and the report
    says so with an empty `released` and an empty `refused` -- which is
    the honest answer, and distinguishable from a refusal.
    """

    def __init__(
        self,
        workspace_id: str,
        *,
        projects,
        registry: LiveReferenceRegistry,
        session_ids: Iterable[str],
        project_ids: Iterable[str],
        provider_names: Iterable[str],
        close_provider: Callable[[str], Union[int, Awaitable[int]]],
        include_provider_pools: Optional[bool] = None,
    ) -> None:
        self.workspace_id = workspace_id
        self._projects = projects
        self._registry = registry
        self._session_ids = set(session_ids)
        self._project_ids = list(dict.fromkeys(project_ids))
        self._provider_names = list(dict.fromkeys(provider_names))
        self._close_provider = close_provider
        # A provider pool is PROCESS-wide, not workspace-scoped, so
        # whether an unload should touch one is a real question rather
        # than an implementation detail. The answer: only when this
        # unload is for a workspace the backend actually holds something
        # for. Unloading a workspace id this backend has never seen must
        # not reach out and close pools another workspace is about to
        # use -- it is a view Go dropped that meant nothing here, and
        # the honest reply to it is an empty report.
        #
        # Teardown and the bench harness pass True explicitly, because
        # "release everything" is exactly what they mean even on a
        # profile with no sessions at all.
        self._include_provider_pools = (
            bool(self._session_ids) if include_provider_pools is None
            else include_provider_pools
        )

    # What this backend does not have. Named here rather than omitted --
    # see the module docstring.
    _ABSENT = (
        Absent(
            "open_buffers",
            "buffers live in Go (frontend\\internal\\editor\\); the backend "
            "holds none. Its only per-editor state is "
            "StreamManager._editor_contexts, keyed by task id and already "
            "cleared at the terminal event",
        ),
        Absent(
            "symbol_parse_caches",
            "none exist: managers\\editor_context.py is resolve_scope with no "
            "parser, and this episode did not add one",
        ),
    )

    def _resources(self) -> list[Resource]:
        resources: list[Resource] = []
        for project_id in self._project_ids:
            resources.append(
                Resource(
                    name=f"file_index[{project_id[:8]}]",
                    resource_class=ResourceClass.PROJECT,
                    detail="ProjectManager._index_cache (B7's file index, 30s TTL)",
                    release=lambda pid=project_id: self._projects.release_index(pid),
                    holder_filter=lambda task: task.session_id in self._session_ids,
                )
            )
        for provider_name in (
            self._provider_names if self._include_provider_pools else ()
        ):
            resources.append(
                Resource(
                    name=f"provider_pool[{provider_name}]",
                    resource_class=ResourceClass.PROVIDER,
                    detail="idle SDK client / httpx connection pool "
                           "(process-wide, not workspace-scoped)",
                    release=lambda name=provider_name: self._close_provider(name),
                    # A provider pool is shared by the whole process, so
                    # ANY running task can be streaming through it --
                    # not only one of this workspace's sessions. Scoping
                    # this to the workspace would close a pool out from
                    # under another workspace's stream.
                    holder_filter=lambda task: True,
                )
            )
        return resources

    async def close(self) -> ReleaseReport:
        return await self._registry.release(
            target=f"workspace:{self.workspace_id}",
            resources=self._resources(),
            absent=self._ABSENT,
        )
