r"""F8 verification: a REFUSED attach must not bind a session's project.

Found live, twice. `_handle_attach_context` resolved-and-wrote the adopted
project BEFORE calling `attachments.attach()` -- the thing that raises
AttachmentRefusedError -- so a refused attach still permanently set
`session.project_id`. Reproduced with a genuinely binary file (a copy of
notepad.exe) inside `D:\Projects\Point`'s own tree: refused with "looks
binary (null bytes in the first 8KB)", `project_id` set anyway.

Two cases, both against a real database:

  1. REFUSED attach (binary, then oversized) on an unbound session whose
     path IS inside a `.point\` tree -> project_id stays NULL.
  2. SUCCESSFUL attach of a valid text file in that same tree -> project_id
     IS bound. The fix only reorders adopt-after-success; it must not
     remove binding-on-success.

Run from inside backend\, against a scratch profile (CLAUDE.md's
APPDATA/LOCALAPPDATA override -- never the real one). Exits non-zero on
failure so a gate can branch on it rather than on a human reading output.
"""

from __future__ import annotations

import asyncio
import os
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bootstrap import build_runtime, shutdown_runtime
from runtime.errors import AttachmentRefusedError

FAILURES: list[str] = []


def check(ok: bool, label: str) -> None:
    print(f"{'ok  ' if ok else 'FAIL'}  {label}")
    if not ok:
        FAILURES.append(label)


async def project_id_of(runtime, session_id: str) -> str | None:
    """Read it back from the DATABASE, not from an in-memory object --
    the whole bug was a write nobody asked for."""
    session = await runtime.memory.session(session_id)
    return session.project_id if session else None


async def attach(runtime, session_id: str, path: str) -> tuple[bool, str]:
    """Drive AttachContext the way a client does. Returns (accepted, note)."""
    try:
        await runtime.receive({
            "v": 1, "id": f"attach-{os.urandom(4).hex()}", "type": "AttachContext",
            "payload": {"session": session_id, "path": path},
        })
    except AttachmentRefusedError as exc:  # pragma: no cover - belt and braces
        return False, str(exc)
    # receive() converts a RuntimeException into an Error envelope rather
    # than raising, so the outbox is where a refusal actually shows up.
    while not runtime.outbox.empty():
        event = await runtime.outbox.get()
        payload = event.payload or {}
        if event.type.value == "Error":
            return False, str(payload.get("error", ""))
        if event.type.value == "Attachment":
            return True, "attached"
    return False, "no reply"


async def main() -> int:
    root = Path(__file__).resolve().parent.parent.parent  # D:\Projects\Point
    marker = root / ".point"
    if not marker.is_dir():
        print(f"SKIP: no project marker at {marker} -- this test needs one", file=sys.stderr)
        return 0

    # single_instance=False: this is a second process alongside whatever
    # else may be running, exactly like cli.py. build_runtime does the
    # migrations itself.
    scratch_dir: Path | None = None
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    try:
        # Files that WILL be refused, inside the project tree.
        binary = root / "real_binary_test.exe"
        oversized = root / "oversized_test.bin"
        refusals = [p for p in (binary, oversized) if p.exists()]
        if not refusals:
            # Make our own binary so the test stands alone -- INSIDE the
            # project tree, which is the whole point (the walk-up has to
            # find `.point\`), and cleaned up in the finally below: a test
            # that leaves debris in the repo it is testing is its own
            # small bug, and this one did exactly that once.
            scratch_dir = Path(tempfile.mkdtemp(dir=root, prefix="f8-probe-"))
            made = scratch_dir / "f8_binary_probe.bin"
            made.write_bytes(b"\x00\x01\x02" * 512)
            refusals = [made]

        # -- case 1: every refusal leaves project_id alone ----------------
        for path in refusals:
            session = await runtime.sessions.create(title="f8-refusal")
            before = await project_id_of(runtime, session.id)
            check(before is None, f"fresh session starts unbound ({path.name})")

            accepted, note = await attach(runtime, session.id, str(path))
            check(not accepted, f"{path.name} refused: {note[:70]}")

            after = await project_id_of(runtime, session.id)
            check(
                after is None,
                f"project_id still NULL after the refusal ({path.name}) -- got {after!r}",
            )

        # -- case 2: a successful attach still binds ----------------------
        session = await runtime.sessions.create(title="f8-success")
        check(await project_id_of(runtime, session.id) is None, "fresh session starts unbound (success case)")

        valid = root / "README.md"
        if not valid.exists():
            valid = Path(__file__)  # also inside the tree
        accepted, note = await attach(runtime, session.id, str(valid))
        check(accepted, f"{valid.name} attached: {note[:70]}")

        bound = await project_id_of(runtime, session.id)
        check(bound is not None, f"project_id IS bound after a successful attach -- got {bound!r}")
    finally:
        await shutdown_runtime(runtime)
        if scratch_dir is not None:
            shutil.rmtree(scratch_dir, ignore_errors=True)

    print()
    if FAILURES:
        print(f"{len(FAILURES)} check(s) failed:", file=sys.stderr)
        for f in FAILURES:
            print(f"  - {f}", file=sys.stderr)
        return 1
    print("all checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
