r"""B30 manual check: `SetSessionProvider`'s new optional `project_id`
field, driven through ONE backend process (same shape as B26/B27 --
`build_runtime(single_instance=False, say_hello=False, heartbeat=False)`,
one long-lived `Runtime`, events sent via `runtime.receive()` and
inspected off `runtime.outbox`).

Covers Done-when #1-#4 from design\tasks\backend\B30-set-session-project.md:

  1. SetSessionProvider with project_id set on a session whose
     project_id is currently NULL binds it -- confirmed by a direct
     session-table read (runtime.sessions.get), not the reply alone.
  2. SetSessionProvider WITHOUT project_id leaves an existing binding
     untouched while provider/model still change.
  3. The SessionProviderUpdated reply always carries the session's
     real, current project_id -- including on step 2's request, which
     never sent one itself.
  4. go test ./frontend/internal/chat/... is this episode's Done-when
     #4 and is run separately, not from this script.

Also covers the re-point case the task doc's Build §1 calls out as
deliberate: project_id sent for a session already bound to a DIFFERENT
project (A -> B) rebinds it unconditionally, with no NULL-only guard --
unlike _commit_adoption's inferred attach-adoption path, this one is
always an explicit UI-driven choice.

Run from inside `backend\`, against a scratch profile:

    uv run python tests\manual_b30_set_session_project.py
"""

from __future__ import annotations

import asyncio
import sys
import tempfile
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
        with tempfile.TemporaryDirectory() as tmp:
            project_root = Path(tmp) / "proj"
            project_root.mkdir()
            project = await runtime.projects.init(str(project_root))

            session = await runtime.sessions.create(
                title="b30 set session project", provider="echo", model="echo",
            )
            check(
                "fixture: the session starts with no project bound",
                (await runtime.sessions.get(session.id)).project_id is None,
            )

            # -----------------------------------------------------------
            # #1: SetSessionProvider WITH project_id binds a currently-
            # NULL project_id -- confirmed by a direct session read.
            # -----------------------------------------------------------
            print("\n=== #1  SetSessionProvider(project_id=<real id>) binds a NULL project_id")
            await send_event(
                runtime, "SetSessionProvider",
                session_id=session.id, provider="echo", model="echo-v2", project_id=project.id,
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("SessionProviderUpdated reply arrived", bool(updated), str(events))
            reply1 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(session.id)
            check(
                "the session row now carries the project id",
                row.project_id == project.id,
                f"row.project_id={row.project_id!r}, expected={project.id!r}",
            )
            check(
                "provider/model were also applied",
                row.provider == "echo" and row.model == "echo-v2",
                f"provider={row.provider!r}, model={row.model!r}",
            )

            # -----------------------------------------------------------
            # #3 (first half): step 1's OWN reply echoes the project_id
            # it was just given, re-read rather than parroted -- same
            # value either way here, so this alone does not distinguish
            # the two; step 2 below is the real proof.
            # -----------------------------------------------------------
            check(
                "step 1's reply carries the bound project_id",
                reply1.get("project_id") == project.id,
                str(reply1),
            )

            # -----------------------------------------------------------
            # #2: SetSessionProvider WITHOUT project_id leaves an
            # existing binding untouched, while provider/model still
            # change.
            # -----------------------------------------------------------
            print("\n=== #2  SetSessionProvider with NO project_id leaves an existing binding untouched")
            await send_event(
                runtime, "SetSessionProvider",
                session_id=session.id, provider="echo", model="echo-v3",
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("second SessionProviderUpdated reply arrived", bool(updated), str(events))
            reply2 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(session.id)
            check(
                "project_id survives a project_id-less retrofit",
                row.project_id == project.id,
                f"row.project_id={row.project_id!r}, expected={project.id!r}",
            )
            check(
                "provider/model changed again",
                row.provider == "echo" and row.model == "echo-v3",
                f"provider={row.provider!r}, model={row.model!r}",
            )

            # -----------------------------------------------------------
            # #3 (the real proof): step 2's REQUEST never sent
            # project_id at all, yet its REPLY still carries the real,
            # current value -- not null, not omitted.
            # -----------------------------------------------------------
            check(
                "step 2's reply echoes the EXISTING project_id even though "
                "the request never sent one",
                reply2.get("project_id") == project.id,
                str(reply2),
            )

            # -----------------------------------------------------------
            # Re-point: project_id sent for a session already bound to a
            # DIFFERENT project. The task doc's Build §1 is explicit that
            # this path does NOT inherit _commit_adoption's "only ever
            # fills a NULL" guard -- SetSessionProvider's caller is always
            # Point's own UI acting on the user's explicit choice, so an
            # explicit project_id here is as trusted as provider/model,
            # which already overwrite unconditionally the same way.
            # -----------------------------------------------------------
            print("\n=== A->B  SetSessionProvider re-points a session already bound to project A")
            project_b_root = Path(tmp) / "proj-b"
            project_b_root.mkdir()
            project_b = await runtime.projects.init(str(project_b_root))

            await send_event(
                runtime, "SetSessionProvider",
                session_id=session.id, provider="echo", model="echo-v4", project_id=project_b.id,
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("the re-point reply arrived", bool(updated), str(events))
            reply3 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(session.id)
            check(
                "project_id moved from A to B with no NULL-only guard",
                row.project_id == project_b.id and row.project_id != project.id,
                f"row.project_id={row.project_id!r}, A={project.id!r}, B={project_b.id!r}",
            )
            check(
                "the reply echoes B, not A",
                reply3.get("project_id") == project_b.id,
                str(reply3),
            )
            check(
                "provider/model were applied alongside the re-point",
                row.provider == "echo" and row.model == "echo-v4",
                f"provider={row.provider!r}, model={row.model!r}",
            )
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
