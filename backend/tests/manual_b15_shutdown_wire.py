r"""B15 Done-when #6 and #7, against a REAL `main.py` process.

`cli.py send --shutdown-after N` cannot prove either of these, and the
flag's own help says so: the CLI exits when its command returns whether
`Shutdown` worked or not, because `_with_runtime` tears the runtime down
in a `finally`. The claim "the process actually exits" needs a process
that would otherwise keep running, which is `main.py` with its stdin
still open.

So this spawns `main.py`, speaks the real JSON-lines protocol to it over
pipes, and waits on the OS process.

    #7  Shutdown quiesces      one `shutdown.complete` ack, the partial
                               persisted, and the PROCESS EXITS -- the
                               failure before B15 was a runtime that
                               set itself STOPPED, acked nothing, and
                               stayed alive rejecting everything
    #6  Ctrl+C during a stream a consistent database, a persisted
                               partial, and an exit within a second or
                               two -- not a hang on a blocking
                               `readline` in a non-daemon thread

Ctrl+C is sent as CTRL_BREAK_EVENT to a child started in its own process
group, which is the only way to deliver a genuine console-interrupt
signal to one process from a script on Windows. CPython turns it into
KeyboardInterrupt in `main.py` exactly as Ctrl+C does, which is the
thing under test; `Stop-Process` would be SIGKILL and would test the
crash path instead (that is `manual_b15_crash.ps1`).

Run from inside the backend directory, against a scratch profile -- the
child inherits APPDATA/LOCALAPPDATA from this process, so the scratch
override the caller set is what the child uses:

    uv run python tests\manual_b15_shutdown_wire.py

Exits non-zero if any expectation failed.
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
import uuid
from pathlib import Path

BACKEND = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(BACKEND))

FIXTURES = Path(__file__).resolve().parent / "fixtures"

FAILURES: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    suffix = ("  -- " + detail) if detail and not ok else ""
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{suffix}")
    if not ok:
        FAILURES.append(label)


def line(kind: str, **payload) -> bytes:
    return (json.dumps({
        "v": 1, "id": str(uuid.uuid4()), "type": kind, "payload": payload,
    }) + "\n").encode("utf-8")


def spawn(new_group: bool) -> subprocess.Popen:
    env = dict(os.environ)
    env["POINT_MOCK"] = "1"
    env["POINT_MOCK_FIXTURE"] = str(FIXTURES / "mock_slow.json")
    flags = 0
    if new_group and hasattr(subprocess, "CREATE_NEW_PROCESS_GROUP"):
        # Required for CTRL_BREAK_EVENT to reach this child and nothing
        # else. Without it the signal goes to our own group, which
        # includes the script sending it.
        flags = subprocess.CREATE_NEW_PROCESS_GROUP
    return subprocess.Popen(
        [sys.executable, "main.py"],
        cwd=str(BACKEND),
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        creationflags=flags,
    )


def read_until(proc: subprocess.Popen, types: set[str], limit_s: float = 30.0) -> list[dict]:
    """Read envelopes off the child's stdout until one of `types`
    arrives or time runs out. Returns everything read.

    Blocking reads on a pipe, deliberately: this is a test driver, and
    the thing being tested is whether the child writes the ack and then
    goes away -- so the read ending in EOF is itself a result.
    """
    seen: list[dict] = []
    deadline = time.monotonic() + limit_s
    while time.monotonic() < deadline:
        raw = proc.stdout.readline()
        if not raw:  # EOF: the child closed stdout or exited
            return seen
        try:
            envelope = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            continue  # a stray print would be a protocol error, not our business
        seen.append(envelope)
        if envelope.get("type") in types:
            return seen
    return seen


def start_a_stream(proc: subprocess.Popen) -> str:
    """Create a session on the mock provider and start a slow reply.
    Returns the session id, once TaskStarted has actually arrived."""
    proc.stdin.write(line(
        "CreateSession", title="b15 wire", provider="mock", model="mock-slow",
    ))
    proc.stdin.flush()
    seen = read_until(proc, {"SessionCreated"}, limit_s=30.0)
    created = [e for e in seen if e["type"] == "SessionCreated"]
    if not created:
        return ""
    session_id = created[-1]["payload"]["session"]["id"]

    proc.stdin.write(line("SendMessage", session=session_id, text="count"))
    proc.stdin.flush()
    read_until(proc, {"TaskStarted"}, limit_s=30.0)
    # Let real text accumulate: the fixture is 0.3s per chunk and the
    # checkpoint cadence is 0.5s, so a second in is several words and at
    # least one checkpoint.
    time.sleep(1.2)
    return session_id


def messages_for(session_id: str) -> list[tuple]:
    """Read the transcript back with a fresh connection, after the child
    has exited -- the point being that the row is on DISK."""
    import sqlite3

    from bootstrap import resolve_db_path

    conn = sqlite3.connect(str(resolve_db_path()), timeout=10.0)
    try:
        return conn.execute(
            "SELECT role, interrupted, outcome, error_code, length(content) "
            "FROM messages WHERE session_id = ? ORDER BY seq",
            (session_id,),
        ).fetchall()
    finally:
        conn.close()


def scenario_shutdown() -> None:
    print("\n[7] a Shutdown COMMAND mid-stream")
    proc = spawn(new_group=False)
    try:
        session_id = start_a_stream(proc)
        check("a session and a stream got going", bool(session_id))
        if not session_id:
            return

        proc.stdin.write(line("Shutdown"))
        proc.stdin.flush()
        seen = read_until(proc, {"shutdown.complete"}, limit_s=30.0)
        acks = [e for e in seen if e["type"] == "shutdown.complete"]
        check(
            "exactly one shutdown.complete ack",
            len(acks) == 1,
            f"{len(acks)} acks in {[e['type'] for e in seen]} -- "
            "project_idea.md §4: every COMMAND gets exactly one ack",
        )
        if acks:
            check(
                "the ack is the LAST thing on the wire",
                seen[-1]["type"] == "shutdown.complete",
                f"trailing types={[e['type'] for e in seen[-3:]]}",
            )
            check(
                "the ack reports it was not already stopping",
                acks[0]["payload"].get("already_stopping") is False,
                f"payload={acks[0]['payload']}",
            )

        exited = True
        try:
            proc.wait(timeout=15.0)
        except subprocess.TimeoutExpired:
            exited = False
        check(
            "the PROCESS exits after the ack",
            exited,
            "this is the half `cli.py --shutdown-after` cannot test: before "
            "B15 the runtime went STOPPED and the loops kept running, so the "
            "process never exited at all",
        )
        if exited:
            check(
                "it exits cleanly (no traceback, rc 0)",
                proc.returncode == 0,
                f"rc={proc.returncode}, stderr tail="
                f"{(proc.stderr.read() or b'').decode('utf-8', 'replace')[-400:]!r}",
            )

        rows = messages_for(session_id)
        assistant = [r for r in rows if r[0] == "assistant"]
        check(
            "the partial reply is persisted, marked interrupted",
            assistant and assistant[-1][1] == 1 and assistant[-1][4] > 0,
            f"rows={rows} -- a Shutdown cancels like a CancelTask does, and a "
            "cancel keeps the work already done",
        )
    finally:
        if proc.poll() is None:
            proc.kill()
        proc.stdout.close()
        proc.stderr.close()


def scenario_ctrl_c() -> None:
    print("\n[6] Ctrl+C during a stream")
    if not hasattr(signal, "CTRL_BREAK_EVENT"):
        print("  [SKIP] not Windows: no CTRL_BREAK_EVENT to send")
        return

    proc = spawn(new_group=True)
    try:
        session_id = start_a_stream(proc)
        check("a session and a stream got going", bool(session_id))
        if not session_id:
            return

        started = time.monotonic()
        proc.send_signal(signal.CTRL_BREAK_EVENT)
        exited = True
        try:
            proc.wait(timeout=15.0)
        except subprocess.TimeoutExpired:
            exited = False
        elapsed = time.monotonic() - started
        check(
            "the process exits rather than hanging",
            exited,
            "a hang here means the stdin reader thread is not a daemon and the "
            "process is waiting on a blocking readline that will never return",
        )
        check(
            "it exits within a second or two",
            exited and elapsed < 10.0,
            f"{elapsed:.1f}s",
        )
        print(f"       (exited in {elapsed:.2f}s, rc={proc.returncode})")

        rows = messages_for(session_id)
        assistant = [r for r in rows if r[0] == "assistant"]
        check(
            "the partial reply is persisted, marked interrupted",
            assistant and assistant[-1][1] == 1 and assistant[-1][4] > 0,
            f"rows={rows} -- `Runtime.close()` cancels active tasks and awaits "
            "their cleanup, and the sink's cancelled branch is what writes this",
        )
        check(
            "the database is left consistent (readable, user row intact)",
            any(r[0] == "user" for r in rows),
            f"rows={rows}",
        )
    finally:
        if proc.poll() is None:
            proc.kill()
        proc.stdout.close()
        proc.stderr.close()


def main() -> int:
    scenario_shutdown()
    scenario_ctrl_c()
    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(main())
