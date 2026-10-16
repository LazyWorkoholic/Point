r"""The protocol channel is UTF-8, proved by driving the REAL pipe.

Run from inside `backend\`:

    uv run python tests\manual_transport_encoding.py

WHY THIS SPAWNS main.py INSTEAD OF CALLING THE RUNTIME
------------------------------------------------------
Every other manual_* script here imports the manager or the runtime and
calls it directly, which is the right shape for a manager's own rules.
It cannot work for this one: the bug these checks exist for lives in the
stdin/stdout TextIOWrapper, and an in-process call never crosses it.
`cli.py` has the same blind spot -- it builds a Runtime in its own
process -- which is exactly why this defect survived B6, B32 and the
whole of F13a and F13b's gates while corrupting files.

So this starts `main.py` as a child with pipes and writes raw bytes at
it, the way Go does.

WHAT WENT WRONG, AND WHY EACH CHECK EXISTS
------------------------------------------
On Windows, sys.stdin/sys.stdout over a PIPE default to the ANSI
codepage, not UTF-8 -- cp1252 on an en-GB install. Replies survived only
by accident: json.dumps defaults to ensure_ascii=True, so every reply
happened to be pure ASCII. Requests did not, because Go's
encoding/json writes raw UTF-8.

Three distinct failures came out of that one default, and all three were
SILENT:

  1 em-dash round trip   U+2014 is e2 80 94. Every one of those bytes is
                         defined in cp1252, so the line decoded to
                         mojibake and the write REPORTED ok:True while
                         putting c3 a2 e2 82 ac e2 80 9d on disk. This
                         is what happened to design\GIT_STRATEGY.md:
                         every em-dash in a tracked file was corrupted
                         by a save that said it succeeded.

  2 cp1252-undefined     U+240F is e2 90 8f. Bytes 0x90 and 0x8f have no
                         cp1252 mapping at all, so the decode raised and
                         the line was dropped -- no reply, process
                         alive. This is why design\project_idea.md would
                         not save at all: it contains both bytes.

  3 invalid UTF-8        Once stdin was pinned to utf-8/strict, a line
                         that is not valid UTF-8 in ANY encoding raised
                         inside the reader THREAD, which had nothing to
                         catch it. Measured: the thread died, so every
                         later request was ignored, and the EOF signal
                         (the last statement in that function) never
                         ran -- so the process hung for ever, the
                         database was never closed and the
                         single-instance lock stayed held by a live pid.
                         Every later launch would then refuse to start.

Check 3 is the reason this file is not just "assert a round trip": the
fix for 1 and 2 introduced 3, and only running it showed that.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from pathlib import Path

_PASSES = 0
_FAILURES: list[str] = []


def check(ok: bool, name: str, detail: str = "") -> None:
    global _PASSES
    if ok:
        _PASSES += 1
        print(f"PASS  {name}" + (f"  ({detail})" if detail else ""))
    else:
        _FAILURES.append(name)
        print(f"FAIL  {name}" + (f"  ({detail})" if detail else ""))


class Backend:
    """main.py as a child process, with stdout drained on a thread.

    The drain is not a convenience. readline() on a live pipe with no
    data blocks for ever, so a timeout checked only between lines never
    fires -- a first version of this harness hung instead of reporting
    check 3, which is the failure it was written to catch.
    """

    def __init__(self, backend_dir: Path, scratch: Path) -> None:
        env = dict(os.environ)
        env["APPDATA"] = str(scratch)
        env["LOCALAPPDATA"] = str(scratch)
        # Deliberately NOT set: the point is what Point does on its own.
        env.pop("PYTHONIOENCODING", None)
        self._env = env
        self._dir = backend_dir

        subprocess.run([sys.executable, "cli.py", "migrate"],
                       cwd=backend_dir, env=env, capture_output=True)

        self.proc = subprocess.Popen(
            [sys.executable, "main.py"], cwd=backend_dir, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE)
        self._out: queue.Queue = queue.Queue()
        self.err: list[str] = []
        threading.Thread(target=self._drain_out, daemon=True).start()
        threading.Thread(target=self._drain_err, daemon=True).start()

    def _drain_out(self) -> None:
        for line in self.proc.stdout:
            self._out.put(line)
        self._out.put(None)

    def _drain_err(self) -> None:
        for line in self.proc.stderr:
            self.err.append(line.decode("utf-8", "replace"))

    def send_raw(self, data: bytes) -> None:
        self.proc.stdin.write(data)
        self.proc.stdin.flush()

    def send(self, type_: str, payload: dict) -> None:
        # ensure_ascii=False so the bytes on the wire are raw UTF-8 --
        # which is what Go's encoding/json produces. Writing escaped
        # ASCII here would test nothing.
        env = {"v": 1, "id": str(uuid.uuid4()), "type": type_, "payload": payload}
        self.send_raw(json.dumps(env, ensure_ascii=False).encode("utf-8") + b"\n")

    def wait_for(self, want: str, timeout: float = 25.0):
        end = time.time() + timeout
        while time.time() < end:
            try:
                line = self._out.get(timeout=0.3)
            except queue.Empty:
                continue
            if line is None:
                return None
            try:
                env = json.loads(line.decode("utf-8", "replace"))
            except ValueError:
                continue
            if env.get("type") == want:
                return env
        return None

    def close(self, timeout: float = 15.0):
        """Returns the exit code, or None if it had to be killed."""
        try:
            self.proc.stdin.close()
        except OSError:
            pass
        try:
            return self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            return None


EM_DASH = "—"          # e2 80 94 -- defined in cp1252, so: mojibake
CP1252_UNDEFINED = "␏"  # e2 90 8f -- 0x90/0x8f have no cp1252 mapping
INVALID_UTF8 = b"\xff\xfe"   # not a valid sequence in any position


def run(repo: Path) -> None:
    scratch = Path(tempfile.gettempdir()) / "point-transport-encoding-test"
    fixdir = repo / "backend" / "tests" / "fixtures" / "transport"
    shutil.rmtree(scratch, ignore_errors=True)
    shutil.rmtree(fixdir, ignore_errors=True)
    scratch.mkdir(parents=True, exist_ok=True)
    fixdir.mkdir(parents=True, exist_ok=True)

    target = fixdir / "dash.md"
    original = ("# heading " + EM_DASH + " subtitle\n").encode("utf-8")
    target.write_bytes(original)

    be = Backend(repo / "backend", scratch)
    try:
        be.send("ProjectResolve", {"path": str(repo)})
        pr = be.wait_for("Project")
        check(pr is not None, "the harness can talk to main.py at all")
        if pr is None:
            return
        project = pr["payload"].get("project")
        check(project is not None, "the repo resolves as a project")
        if project is None:
            return
        pid = project["id"]

        # ---- 1. the read direction -------------------------------
        be.send("ReadProjectFile", {"path": str(target), "project_id": pid})
        rf = be.wait_for("ProjectFile")
        check(rf is not None, "ReadProjectFile answers")
        if rf is None:
            return
        content = rf["payload"]["file"]["content"]
        mtime = rf["payload"]["file"]["mtime"]
        check(EM_DASH in content,
              "an em-dash survives Python -> Go",
              f"content={content!r}")

        # ---- 2. the write direction, which is where it broke -----
        be.send("WriteProjectFile", {
            "path": str(target), "project_id": pid,
            "content": "# heading " + EM_DASH + " subtitle",
            "holder": "transport-test@id", "expected_mtime": mtime,
            "line_ending": "lf", "bom": False, "trailing_newline": True,
        })
        wr = be.wait_for("ProjectFileWritten")
        check(wr is not None, "WriteProjectFile answers an em-dash payload")
        check(bool(wr and wr["payload"].get("ok")),
              "...and reports ok")
        after = target.read_bytes()
        check(after == original,
              "an em-dash file is BYTE-IDENTICAL after read -> write",
              f"{after!r}")
        check(b"\xc3\xa2" not in after,
              "no mojibake on disk (c3 a2 is the cp1252 tell)")

        # ---- 3. bytes cp1252 cannot map at all -------------------
        be.send("WriteProjectFile", {
            "path": str(target), "project_id": pid,
            "content": "undef " + CP1252_UNDEFINED + " here",
            "holder": "transport-test@id", "overwrite": True,
            "line_ending": "lf", "bom": False, "trailing_newline": True,
        })
        wr2 = be.wait_for("ProjectFileWritten", timeout=15)
        check(wr2 is not None,
              "a payload with cp1252-UNDEFINED bytes is not dropped")
        check(CP1252_UNDEFINED in target.read_text(encoding="utf-8"),
              "...and lands on disk intact")

        # ---- 4. invalid UTF-8 must not take the reader down ------
        be.send_raw(b'{"v":1,"id":"bad","type":"ProjectResolve","payload":'
                    b'{"path":"' + INVALID_UTF8 + b'"}}\n')
        time.sleep(1.5)

        be.send("ProjectResolve", {"path": str(repo)})
        check(be.wait_for("Project", timeout=10) is not None,
              "the backend STILL ANSWERS after an invalid-UTF-8 line",
              "a dead reader thread makes it deaf in silence")

        err = "".join(be.err)
        check("Discarded undecodable line" in err,
              "...and says so on stderr (never stdout -- that is the protocol)")
        check("UnicodeDecodeError" not in err and "Exception in thread" not in err,
              "...with no traceback escaping the reader thread")
    finally:
        code = be.close()
        # The EOF signal is the LAST statement in the reader function. An
        # exception escaping that loop skipped it, read_stdin_loop stayed
        # blocked for ever, and the single-instance lock was never
        # released -- so every later launch would refuse to start.
        check(code == 0,
              "closing stdin still shuts the process down cleanly",
              f"exit={code}" if code is not None else "HAD TO BE KILLED")
        shutil.rmtree(fixdir, ignore_errors=True)
        shutil.rmtree(scratch, ignore_errors=True)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--repo", default=str(Path(__file__).resolve().parents[2]),
        help="the Point repo root (defaults to this file's own)",
    )
    args = ap.parse_args()
    repo = Path(args.repo).resolve()
    if not (repo / "backend" / "main.py").is_file():
        print(f"error: --repo {repo} has no backend\\main.py", file=sys.stderr)
        return 1
    if not (repo / ".point" / "project.json").is_file():
        print(f"error: --repo {repo} has no .point\\project.json to resolve",
              file=sys.stderr)
        return 1

    run(repo)

    print(f"\n{_PASSES} passed, {len(_FAILURES)} failed")
    if _FAILURES:
        for name in _FAILURES:
            print(f"  FAILED: {name}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
