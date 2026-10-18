r"""B36 manual check: the two additions to B24/F9b's deferred backend half.

  1. `FillProposal` carries `(mtime, size)` captured at the SAME disk
     read `_render_editor_context` used to build the shown region --
     driven end to end (SendMessage -> fill-mode task -> FillProposal),
     through one long-lived `Runtime`, the same shape as B26/B30/B31's
     scripts (`runtime.receive()` + `runtime.outbox`), rather than
     through `cli.py`'s own argv -- a JSON message containing literal
     `"` characters does not survive Windows PowerShell 5.1's native-exe
     argument quoting intact, so this avoids that entirely rather than
     fighting it.

  2. `CheckFileStale` accepts `session_id` and `size` as real comparison
     fields, additively: no `session_id`/`size` must behave exactly as
     before B36 (mtime alone), `size` given must actually be compared,
     and `session_id` given must resolve the SESSION's own scope
     (`_attachment_scope`) rather than `_resolve_optional_project`'s
     path-walk-up -- B12's bug #2, corrected for AttachContext by
     374baa2, now extended to this event too.

Run from inside `backend\`, against a scratch profile (CLAUDE.md's
APPDATA/LOCALAPPDATA override pattern):

    uv run python tests\manual_b36_check.py
"""

from __future__ import annotations

import asyncio
import json
import os
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


async def drain(runtime, limit: int = 400, timeout: float = 0.05) -> list[dict]:
    seen = []
    for _ in range(limit):
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=timeout)
        except asyncio.TimeoutError:
            break
        seen.append(event.to_dict())
    return seen


async def drain_through_terminal(runtime, extra_wait: float = 2.0) -> list[dict]:
    """Like `drain()`, but for a task whose body paces itself (the echo
    provider's word-by-word sleep) -- polls with a short per-item
    timeout until a terminal event is seen, then keeps draining for
    `extra_wait` more seconds to also catch `FillProposal`, which B24
    pushes immediately after the terminal event in the same sink call."""
    seen: list[dict] = []
    terminal_types = {"TaskDone", "TaskFailed", "TaskCancelled"}
    saw_terminal = False
    loop = asyncio.get_event_loop()
    deadline = loop.time() + 30.0
    while loop.time() < deadline:
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=0.5)
        except asyncio.TimeoutError:
            if saw_terminal:
                break
            continue
        d = event.to_dict()
        seen.append(d)
        if d["type"] in terminal_types:
            saw_terminal = True
            deadline = min(deadline, loop.time() + extra_wait)
    return seen


async def main() -> int:
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    db_path = resolve_db_path()
    print(f"database   {db_path}")

    try:
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)

            # =================================================================
            # Part 1 -- FillProposal carries (mtime, size)
            # =================================================================
            print("\n=== Part 1: FillProposal carries (mtime, size) from the region-generation read")

            fill_file = tmp_path / "region.txt"
            fill_file.write_text("line one\nline two\nline three", encoding="utf-8")

            fill_session = await runtime.sessions.create(
                title="b36 fill test", provider="echo", model="echo",
            )

            proposal = {
                "file": "region.txt", "start_line": 1, "end_line": 3,
                "replacement": "line one\nEDITED\nline three", "note": "b36 check",
            }
            await send_event(
                runtime, "SendMessage",
                session=fill_session.id, text=json.dumps(proposal),
                editor_context={"file": str(fill_file), "mode": "fill", "cursor": 2},
            )
            events = await drain_through_terminal(runtime)

            done = [e for e in events if e["type"] == "TaskDone"]
            check("fill-mode turn reached TaskDone", bool(done), str(events))

            fill_proposals = [e for e in events if e["type"] == "FillProposal"]
            check("a FillProposal event arrived", bool(fill_proposals), str(events))

            # Stat taken in THIS process, after the task already ran --
            # the file was never touched in between, so this must equal
            # what the backend captured at its own read.
            real_stat = os.stat(fill_file)

            if fill_proposals:
                p = fill_proposals[-1]["payload"]["proposal"]
                check(
                    "proposal carries shown_start/shown_end (B24, unchanged)",
                    (p.get("shown_start"), p.get("shown_end")) == (1, 3),
                    str(p),
                )
                check(
                    "proposal carries mtime (B36), matching the real file's stat",
                    p.get("mtime") == real_stat.st_mtime,
                    f"proposal mtime={p.get('mtime')!r}, real={real_stat.st_mtime!r}",
                )
                check(
                    "proposal carries size (B36), matching the real file's stat",
                    p.get("size") == real_stat.st_size,
                    f"proposal size={p.get('size')!r}, real={real_stat.st_size!r}",
                )
                check(
                    "all nine FillProposal fields are present (none missing, none extra)",
                    set(p.keys()) == {
                        "file", "start_line", "end_line", "replacement", "note",
                        "shown_start", "shown_end", "mtime", "size",
                    },
                    str(sorted(p.keys())),
                )

            # =================================================================
            # Part 2 -- CheckFileStale: size and session_id as real fields
            # =================================================================
            print("\n=== Part 2a: CheckFileStale with neither session_id nor size "
                  "behaves exactly as before B36")

            proj_a_dir = tmp_path / "proj_a"
            proj_a_dir.mkdir()
            proj_b_dir = tmp_path / "proj_b"
            proj_b_dir.mkdir()
            proj_a = await runtime.projects.init(str(proj_a_dir))
            proj_b = await runtime.projects.init(str(proj_b_dir))

            stale_file = proj_a_dir / "region2.txt"
            stale_file.write_text("hello world", encoding="utf-8")
            real = os.stat(stale_file)

            await send_event(
                runtime, "CheckFileStale",
                path=str(stale_file), mtime=real.st_mtime, project_id=proj_a.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "unchanged file, real mtime, no size/session -> changed=False",
                bool(results) and results[-1]["payload"]["changed"] is False,
                str(events),
            )

            await send_event(
                runtime, "CheckFileStale",
                path=str(stale_file), mtime=real.st_mtime - 100.0, project_id=proj_a.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "wrong mtime, no size/session -> changed=True (unchanged pre-B36 behaviour)",
                bool(results) and results[-1]["payload"]["changed"] is True,
                str(events),
            )

            print("\n=== Part 2b: `size` is a REAL comparison field, not merely accepted")

            await send_event(
                runtime, "CheckFileStale",
                path=str(stale_file), mtime=real.st_mtime, size=real.st_size + 12345,
                project_id=proj_a.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "real mtime but WRONG size -> changed=True (size actually compared)",
                bool(results) and results[-1]["payload"]["changed"] is True,
                str(events),
            )

            await send_event(
                runtime, "CheckFileStale",
                path=str(stale_file), mtime=real.st_mtime, size=real.st_size,
                project_id=proj_a.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "real mtime AND real size -> changed=False",
                bool(results) and results[-1]["payload"]["changed"] is False,
                str(events),
            )

            print("\n=== Part 2c: `session_id` resolves the SESSION's own scope, "
                  "not a path-walk-up guess (B12 bug #2, extended to CheckFileStale)")

            session_a = await runtime.sessions.create(
                title="b36 scope test", provider="echo", model="echo", project_id=proj_a.id,
            )

            # No session_id: `project_id` (sent by every caller that
            # resolved ITS OWN notion of "current project", exactly like
            # `cli.py`'s `_require_project` does from `--path`) wins --
            # here that is deliberately project B, which does not have
            # this file. This is the pre-B36 code path, unchanged.
            await send_event(
                runtime, "CheckFileStale",
                path="region2.txt", mtime=real.st_mtime, project_id=proj_b.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "wrong scope (project B) with no session_id -> changed=True "
                "(file does not exist under B's root)",
                bool(results) and results[-1]["payload"]["changed"] is True,
                str(events),
            )

            # Same request, but with session_id naming the session bound
            # to project A. The handler must use session_a's OWN scope
            # (project A) and ignore project_id=proj_b.id entirely.
            await send_event(
                runtime, "CheckFileStale",
                path="region2.txt", mtime=real.st_mtime, project_id=proj_b.id,
                session_id=session_a.id,
            )
            events = await drain(runtime)
            results = [e for e in events if e["type"] == "FileStaleResult"]
            check(
                "same request WITH session_id=session_a -> changed=False "
                "(resolved against session A's real project, not project B)",
                bool(results) and results[-1]["payload"]["changed"] is False,
                str(events),
            )
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'ALL B36 CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
