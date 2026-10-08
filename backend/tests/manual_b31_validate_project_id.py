r"""B31 manual check: `CreateSession`/`SetSessionProvider` reject an
unknown `project_id` instead of persisting it, driven through ONE
backend process (same shape as B30 -- `build_runtime(single_instance=
False, say_hello=False, heartbeat=False)`, one long-lived `Runtime`,
events sent via `runtime.receive()` and inspected off `runtime.outbox`).

Covers Done-when #1-#5 from
design\tasks\backend\B31-validate-project-id.md:

  1. CreateSession with an unknown project_id creates the session with
     project_id NULL (direct session read), logs a warning naming the
     rejected id, and the reply's own project_id is empty.
  2. CreateSession with a REAL project_id still binds it exactly as
     before this episode -- a plain regression check.
  3. SetSessionProvider on an UNBOUND session with an unknown
     project_id leaves project_id None, warns, reply echoes None.
  4. SetSessionProvider on a session already bound to project A, with
     an unknown project_id, leaves project_id at A, UNCHANGED, warns,
     reply echoes A.
  5. SetSessionProvider with a REAL, different project B still rebinds
     A -> B exactly as manual_b30_set_session_project.py's own A->B
     case already proved -- a plain regression run.
  6. ProjectManager.exists() causes no write on a hit or a miss --
     last_opened_at/root_path unchanged before and after, for both a
     real and a fake id.

#6 (go test ./frontend/internal/chat/... ./frontend/internal/app/...)
is run separately, not from this script.

Run from inside `backend\`, against a scratch profile:

    uv run python tests\manual_b31_validate_project_id.py
"""

from __future__ import annotations

import asyncio
import logging
import sys
import tempfile
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

FAILURES: list[str] = []
UNKNOWN_PROJECT_ID = "does-not-exist"


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


class _ListHandler(logging.Handler):
    def __init__(self) -> None:
        super().__init__(level=logging.WARNING)
        self.records: list[str] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.records.append(record.getMessage())


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

    log_capture = _ListHandler()
    logging.getLogger("point").addHandler(log_capture)

    try:
        with tempfile.TemporaryDirectory() as tmp:
            project_root = Path(tmp) / "proj"
            project_root.mkdir()
            project_a = await runtime.projects.init(str(project_root))

            # -----------------------------------------------------------
            # #1: CreateSession with an unknown project_id creates the
            # session with project_id NULL, logs a warning naming the
            # rejected id, and the reply echoes empty.
            # -----------------------------------------------------------
            print("\n=== #1  CreateSession(project_id=<unknown>) creates session unbound")
            log_capture.records.clear()
            await send_event(
                runtime, "CreateSession",
                title="b31 unknown on create", provider="echo", model="echo",
                project_id=UNKNOWN_PROJECT_ID,
            )
            events = await drain(runtime)
            created = [e for e in events if e["type"] == "SessionCreated"]
            check("SessionCreated reply arrived", bool(created), str(events))
            reply1 = created[-1]["payload"]["session"] if created else {}
            session1_id = reply1.get("id")

            row = await runtime.sessions.get(session1_id) if session1_id else None
            check(
                "the session row's project_id is None",
                row is not None and row.project_id is None,
                f"row.project_id={getattr(row, 'project_id', '<no row>')!r}",
            )
            check(
                "the reply's own project_id is empty",
                not reply1.get("project_id"),
                str(reply1),
            )
            check(
                "a warning naming the rejected id was logged",
                any(UNKNOWN_PROJECT_ID in msg for msg in log_capture.records),
                str(log_capture.records),
            )

            # -----------------------------------------------------------
            # #2: CreateSession with a REAL project_id still binds it --
            # a plain regression check, no behaviour change.
            # -----------------------------------------------------------
            print("\n=== #2  CreateSession(project_id=<real>) still binds normally")
            log_capture.records.clear()
            await send_event(
                runtime, "CreateSession",
                title="b31 real on create", provider="echo", model="echo",
                project_id=project_a.id,
            )
            events = await drain(runtime)
            created = [e for e in events if e["type"] == "SessionCreated"]
            check("second SessionCreated reply arrived", bool(created), str(events))
            reply2 = created[-1]["payload"]["session"] if created else {}
            session2_id = reply2.get("id")

            row = await runtime.sessions.get(session2_id) if session2_id else None
            check(
                "the session row carries the real project_id",
                row is not None and row.project_id == project_a.id,
                f"row.project_id={getattr(row, 'project_id', '<no row>')!r}",
            )
            check(
                "the reply echoes the real project_id",
                reply2.get("project_id") == project_a.id,
                str(reply2),
            )
            check(
                "no warning logged for a valid id",
                not any(project_a.id in msg for msg in log_capture.records),
                str(log_capture.records),
            )

            # -----------------------------------------------------------
            # #3: SetSessionProvider on an UNBOUND session with an
            # unknown project_id leaves it unbound, warns, reply echoes
            # None.
            # -----------------------------------------------------------
            print("\n=== #3  SetSessionProvider(project_id=<unknown>) on an unbound session")
            unbound_session = await runtime.sessions.create(
                title="b31 unbound", provider="echo", model="echo",
            )
            check(
                "fixture: the session starts with no project bound",
                (await runtime.sessions.get(unbound_session.id)).project_id is None,
            )
            log_capture.records.clear()
            await send_event(
                runtime, "SetSessionProvider",
                session_id=unbound_session.id, provider="echo", model="echo-v2",
                project_id=UNKNOWN_PROJECT_ID,
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("SessionProviderUpdated reply arrived", bool(updated), str(events))
            reply3 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(unbound_session.id)
            check(
                "project_id stays None",
                row.project_id is None,
                f"row.project_id={row.project_id!r}",
            )
            check(
                "provider/model were still applied",
                row.provider == "echo" and row.model == "echo-v2",
                f"provider={row.provider!r}, model={row.model!r}",
            )
            check(
                "the reply echoes None",
                not reply3.get("project_id"),
                str(reply3),
            )
            check(
                "a warning naming the rejected id was logged",
                any(UNKNOWN_PROJECT_ID in msg for msg in log_capture.records),
                str(log_capture.records),
            )

            # -----------------------------------------------------------
            # #4: SetSessionProvider on a session already bound to A,
            # with an unknown project_id, leaves it at A UNCHANGED,
            # warns, reply echoes A.
            # -----------------------------------------------------------
            print("\n=== #4  SetSessionProvider(project_id=<unknown>) on a session bound to A")
            bound_session = await runtime.sessions.create(
                title="b31 bound to A", provider="echo", model="echo",
                project_id=project_a.id,
            )
            check(
                "fixture: the session starts bound to A",
                (await runtime.sessions.get(bound_session.id)).project_id == project_a.id,
            )
            log_capture.records.clear()
            await send_event(
                runtime, "SetSessionProvider",
                session_id=bound_session.id, provider="echo", model="echo-v3",
                project_id=UNKNOWN_PROJECT_ID,
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("SessionProviderUpdated reply arrived", bool(updated), str(events))
            reply4 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(bound_session.id)
            check(
                "project_id stays A, unchanged",
                row.project_id == project_a.id,
                f"row.project_id={row.project_id!r}, expected A={project_a.id!r}",
            )
            check(
                "provider/model were still applied",
                row.provider == "echo" and row.model == "echo-v3",
                f"provider={row.provider!r}, model={row.model!r}",
            )
            check(
                "the reply echoes A, unchanged",
                reply4.get("project_id") == project_a.id,
                str(reply4),
            )
            check(
                "a warning naming the rejected id was logged",
                any(UNKNOWN_PROJECT_ID in msg for msg in log_capture.records),
                str(log_capture.records),
            )

            # -----------------------------------------------------------
            # #5: SetSessionProvider with a REAL, different project B
            # still rebinds A -> B -- a plain regression run against
            # B30's own A->B case.
            # -----------------------------------------------------------
            print("\n=== #5  SetSessionProvider(project_id=<real B>) still re-points A -> B")
            project_b_root = Path(tmp) / "proj-b"
            project_b_root.mkdir()
            project_b = await runtime.projects.init(str(project_b_root))

            log_capture.records.clear()
            await send_event(
                runtime, "SetSessionProvider",
                session_id=bound_session.id, provider="echo", model="echo-v4",
                project_id=project_b.id,
            )
            events = await drain(runtime)
            updated = [e for e in events if e["type"] == "SessionProviderUpdated"]
            check("the re-point reply arrived", bool(updated), str(events))
            reply5 = updated[-1]["payload"] if updated else {}

            row = await runtime.sessions.get(bound_session.id)
            check(
                "project_id moved from A to B",
                row.project_id == project_b.id and row.project_id != project_a.id,
                f"row.project_id={row.project_id!r}, A={project_a.id!r}, B={project_b.id!r}",
            )
            check(
                "the reply echoes B, not A",
                reply5.get("project_id") == project_b.id,
                str(reply5),
            )
            check(
                "no warning logged for a valid id",
                not any(project_b.id in msg for msg in log_capture.records),
                str(log_capture.records),
            )

            # -----------------------------------------------------------
            # #6 (Done-when #5 in the task doc): ProjectManager.exists()
            # causes no write on either a hit or a miss.
            # -----------------------------------------------------------
            print("\n=== #6  ProjectManager.exists() causes no write, hit or miss")
            before = await runtime.projects._repo.fetch_by_id(project_a.id)
            hit = await runtime.projects.exists(project_a.id)
            after = await runtime.projects._repo.fetch_by_id(project_a.id)
            check("exists() returns True on a hit", hit is True)
            check(
                "last_opened_at unchanged across a hit",
                before.last_opened_at == after.last_opened_at,
                f"before={before.last_opened_at!r}, after={after.last_opened_at!r}",
            )
            check(
                "root_path unchanged across a hit",
                before.root_path == after.root_path,
                f"before={before.root_path!r}, after={after.root_path!r}",
            )

            miss = await runtime.projects.exists(UNKNOWN_PROJECT_ID)
            after_miss = await runtime.projects._repo.fetch_by_id(UNKNOWN_PROJECT_ID)
            check("exists() returns False on a miss", miss is False)
            check("a miss inserts no row", after_miss is None, str(after_miss))
    finally:
        logging.getLogger("point").removeHandler(log_capture)
        await shutdown_runtime(runtime)

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
