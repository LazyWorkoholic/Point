r"""F9d manual check: WriteProjectFile's guard ORDER (F9a hazard 1).

F9b built the diff card's apply with no backend call, so the hazard was
dormant. F15 added the first caller of `WriteProjectFile` from an
agent-applied diff, which makes it live: before F9d, that save resolved
its project through `_require_project` -> `_resolve_optional_project` --
the EDITOR's path-walked project, whatever `.point\` happens to sit above
the open file, never the session that produced the proposal. That is B12's
bug #2 on the one destructive path in this backend.

F9d makes `_handle_write_project_file` accept `session_id` and resolve the
SESSION's own scope through `_attachment_scope`, by exactly the rule B36
established for `CheckFileStale`:

  1. No `session_id` -> the pre-F9d path, byte for byte.
  2. `session_id` -> the session's own project; `project_id` in the
     payload is IGNORED OUTRIGHT, not merged and not a fallback.
  3. A `session_id` whose session has NO project is REFUSED, never
     downgraded to the path walk-up.

Driven end to end through one long-lived `Runtime` (`runtime.receive()` +
`runtime.outbox`), the same shape as manual_b36_check.py and for the same
reason: a real event in, a real reply out, and the real file on disk
inspected afterwards.

Run from inside `backend\`, against a scratch profile (CLAUDE.md's
APPDATA/LOCALAPPDATA override pattern):

    uv run python tests\manual_f9d_check.py
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
HOLDER = "f9d@check"


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


async def drain(runtime, limit: int = 400, timeout: float = 0.05) -> list[dict]:
    seen = []
    for _ in range(limit):
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=timeout)
        except asyncio.TimeoutError:
            break
        seen.append(event.to_dict())
    return seen


async def write(runtime, **payload) -> tuple[list[dict], dict | None, dict | None]:
    """One WriteProjectFile, returning (events, written, error).

    Both outcomes are read rather than only the happy one: an
    out-of-scope path raises PathDeniedError, which arrives as an Error
    envelope and not as a `WriteResult` slug, and that refusal IS the
    behaviour this episode is about.
    """
    await send_event(runtime, "WriteProjectFile", holder=HOLDER, **payload)
    events = await drain(runtime)
    written = next((e for e in events if e["type"] == "ProjectFileWritten"), None)
    error = next((e for e in events if e["type"] == "Error"), None)
    return events, written, error


async def main() -> int:
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    print(f"database   {resolve_db_path()}")

    try:
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)

            # Two real projects, each with its own `.point\`. The file
            # under test lives in A; B is the project a path walk-up or a
            # stale `project_id` would reach instead.
            proj_a_dir = tmp_path / "proj_a"
            proj_b_dir = tmp_path / "proj_b"
            proj_a_dir.mkdir()
            proj_b_dir.mkdir()
            proj_a = await runtime.projects.init(str(proj_a_dir))
            proj_b = await runtime.projects.init(str(proj_b_dir))

            target = proj_a_dir / "region.txt"
            target.write_text("line one\nline two\nline three", encoding="utf-8")
            mtime = target.stat().st_mtime

            # The session an agent-applied region came from, bound to A --
            # which is what F15's Editor session is (invoke.go binds its
            # CreateSession to the Editor's own resolved project).
            session_a = await runtime.sessions.create(
                title="f9d scope test", provider="echo", model="echo",
                project_id=proj_a.id,
            )
            # A session with no project at all: the state the Editor is in
            # before it has resolved one.
            session_none = await runtime.sessions.create(
                title="f9d no project", provider="echo", model="echo",
            )

            # =================================================================
            # 1 -- no session_id: EXACTLY the pre-F9d behaviour
            # =================================================================
            print("\n=== 1: no session_id -> the pre-F9d path, unchanged")

            _, written, error = await write(
                runtime, path=str(target), content="A\nB\nC",
                expected_mtime=mtime, project_id=proj_a.id,
            )
            check(
                "project_id only, correct project -> written",
                written is not None and written["payload"]["ok"] is True,
                str(error or written),
            )
            check(
                "the bytes actually landed",
                target.read_text(encoding="utf-8").startswith("A\nB\nC"),
                target.read_text(encoding="utf-8"),
            )
            mtime = target.stat().st_mtime

            # The pre-F9d drift itself, demonstrated -- and it is worse
            # than a refusal, which is why this check is written the way
            # it is rather than the way it was first written.
            #
            # The same relative path under project B's scope does not
            # refuse as a path denial: it resolves to a DIFFERENT FILE,
            # `proj_b\region.txt`, and is answered about that one. With a
            # recorded `expected_mtime` that comes back `stale` (there is
            # no file there to have that mtime), so the first version of
            # this check asserted an Error envelope and failed on a
            # correct backend.
            _, written, error = await write(
                runtime, path="region.txt", content="WRONG",
                expected_mtime=mtime, project_id=proj_b.id,
            )
            check(
                "project_id naming the WRONG project -> answered about B's own "
                "region.txt (`stale`), not A's file",
                written is not None and written["payload"]["ok"] is False
                and written["payload"]["reason"] == "stale",
                str(error or written),
            )
            check(
                "A's file is untouched by it",
                target.read_text(encoding="utf-8").startswith("A\nB\nC"),
                target.read_text(encoding="utf-8"),
            )

            # And the dangerous direction of the same thing: drop the
            # precondition to the creation signal and the wrong scope does
            # not refuse at all -- it CREATES a file under project B. This
            # is B12's bug #2 with bytes on disk at the end of it, and it
            # is exactly what sending a session instead of a path-resolved
            # project prevents.
            _, written, error = await write(
                runtime, path="region.txt", content="WRONG SCOPE",
                expected_mtime=None, project_id=proj_b.id,
            )
            stray = proj_b_dir / "region.txt"
            check(
                "the same request with no precondition SUCCEEDS under B -- a file "
                "written under the wrong project, silently (the hazard itself)",
                written is not None and written["payload"]["ok"] is True
                and stray.exists(),
                str(error or written),
            )
            check(
                "A's file is still untouched by that too",
                target.read_text(encoding="utf-8").startswith("A\nB\nC"),
                target.read_text(encoding="utf-8"),
            )
            stray.unlink()

            # The same creation request, scoped by the session that owns
            # the region instead: answered about A's real file, which
            # already exists, so it refuses `exists` rather than creating
            # anything anywhere. The scope decided WHICH FILE the question
            # was about -- which is the whole of hazard 1.
            _, written, error = await write(
                runtime, path="region.txt", content="WRONG SCOPE",
                expected_mtime=None, project_id=proj_b.id,
                session_id=session_a.id,
            )
            check(
                "with session_id=A the same request is answered about A's file "
                "(`exists`), and creates nothing under B",
                written is not None and written["payload"]["ok"] is False
                and written["payload"]["reason"] == "exists"
                and not stray.exists(),
                str(error or written),
            )

            # =================================================================
            # 2 -- session_id resolves the SESSION's scope and WINS
            # =================================================================
            print("\n=== 2: session_id resolves the session's own scope, "
                  "and project_id is ignored outright")

            # The hazard-1 shape, exactly: a relative path, a `project_id`
            # that points at the wrong project (what a path-walked editor
            # would have supplied), and the session that actually produced
            # the proposal. The session must win.
            _, written, error = await write(
                runtime, path="region.txt", content="FROM SESSION A",
                expected_mtime=mtime, project_id=proj_b.id,
                session_id=session_a.id,
            )
            check(
                "session_id=A beside project_id=B -> written under A's scope",
                written is not None and written["payload"]["ok"] is True,
                str(error or written),
            )
            check(
                "the write landed in project A's file, not anywhere under B",
                target.read_text(encoding="utf-8").startswith("FROM SESSION A"),
                target.read_text(encoding="utf-8"),
            )
            check(
                "nothing was created under project B's root",
                not (proj_b_dir / "region.txt").exists(),
                str(sorted(p.name for p in proj_b_dir.iterdir())),
            )
            mtime = target.stat().st_mtime

            # And the refusal direction, which is the half that proves the
            # scope is a GUARD and not merely a lookup: a session bound to
            # B cannot write A's file, even though a walk-up from that
            # path would have resolved A and allowed it.
            session_b = await runtime.sessions.create(
                title="f9d scope test b", provider="echo", model="echo",
                project_id=proj_b.id,
            )
            _, written, error = await write(
                runtime, path=str(target), content="FROM SESSION B",
                expected_mtime=mtime, session_id=session_b.id,
            )
            check(
                "session_id=B over a file in A -> refused (the session's scope guards it)",
                written is None and error is not None,
                str(error["payload"] if error else written or "nothing came back"),
            )
            check(
                "the file is untouched by the out-of-scope write",
                target.read_text(encoding="utf-8").startswith("FROM SESSION A"),
                target.read_text(encoding="utf-8"),
            )

            # =================================================================
            # 3 -- a session with NO project is refused, never downgraded
            # =================================================================
            print("\n=== 3: a session with no project is REFUSED, "
                  "not downgraded to the path walk-up")

            _, written, error = await write(
                runtime, path=str(target), content="NO PROJECT",
                expected_mtime=mtime, session_id=session_none.id,
            )
            check(
                "session with no project -> refused",
                written is None and error is not None,
                str(error["payload"] if error else written or "nothing came back"),
            )
            # The Error envelope's own key is `error`, not `message`
            # (runtime.py's `_emit` for a RuntimeException) -- the Go side
            # reads `message` for the same thing, which is a pre-existing
            # asymmetry this episode does not touch.
            check(
                "the refusal names the session, not a missing path",
                error is not None and "has no project" in error["payload"].get("error", ""),
                str(error["payload"] if error else None),
            )
            check(
                "the file is untouched -- no fallback write happened",
                target.read_text(encoding="utf-8").startswith("FROM SESSION A"),
                target.read_text(encoding="utf-8"),
            )

            # =================================================================
            # 4 -- the precondition still does its job under session scope
            # =================================================================
            print("\n=== 4: B32's own guards are unchanged under a session scope")

            _, written, error = await write(
                runtime, path="region.txt", content="STALE ATTEMPT",
                expected_mtime=mtime - 100.0, session_id=session_a.id,
            )
            check(
                "a wrong expected_mtime is still refused `stale` under session scope",
                written is not None and written["payload"]["ok"] is False
                and written["payload"]["reason"] == "stale",
                str(error or written),
            )
            check(
                "and nothing was written",
                target.read_text(encoding="utf-8").startswith("FROM SESSION A"),
                target.read_text(encoding="utf-8"),
            )
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL F9D CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
