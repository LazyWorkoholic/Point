r"""B32's unit-level gate: the byte-exact round trip and the refusal paths.

Run from inside `backend\`:

    uv run python tests\manual_b32_write_roundtrip.py --root <a project dir>
    uv run python tests\manual_b32_write_roundtrip.py --root <dir> --case bad-project

WHY THESE CHECKS ARE UNIT-LEVEL AND NOT IN THE POWERSHELL GATE
--------------------------------------------------------------
A byte-exact round trip CANNOT be driven through a shell pipeline:
capturing stdout and writing it back is itself a line-ending
transformation, so a gate that tried would be testing PowerShell's
redirection rather than this episode. Same for `bad-project`: the CLI
resolves its own project from `--path` and so cannot send a garbage
`project_id` at all.

COVERAGE LIST -- every row below maps to a Done-when in
design/tasks/backend/B32-write-project-file.md. The two marked NEW are
Aryan's Decision 2: Done-when #11 and #12 each have two clauses, and the
clause the PowerShell gate exercised did not imply the other one.

    CASES[...] name                     Done-when
    ---------------------------------   ---------
    crlf-bom-roundtrip                  1
    lf-no-bom-roundtrip                 2
    no-trailing-newline-roundtrip       2
    mixed-endings-reported              5 (read side)
    lock-before-create                  9
    create-via-null-mtime               9
    second-create-refused-exists        9
    no-parent-dir-creates-nothing       10
    too-large-refused                   11(a)   <-- NEW (Decision 2)
    between-caps-opens-and-saves        11(b)
    tool-cap-still-narrow               11(b), the other half: the
                                        `read_file` TOOL's cap must stay
                                        index.max_file_kb (100) while the
                                        editor's is 512
    outside-root-raises                 12(a)
    bad-project-raises                  12(b)   <-- NEW (Decision 2)
    failed-replace-leaves-original      13
    stale-refused-file-unchanged        4
    overwrite-succeeds                  5
    locked-refused-no-heartbeat         6, 7
    unlocked-write-creates-no-lock      8
    read-only-refused                   (read_only slug)
"""

from __future__ import annotations

import argparse
import asyncio
import os
import stat as stat_mod
import sys
import time
from pathlib import Path
from typing import Callable, Optional

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import appconfig
from bootstrap import build_runtime, resolve_db_path, shutdown_runtime
from managers.project_manager import describe_bytes, encode_for_write
from runtime.errors import InvalidEventError, PathDeniedError, ProjectNotFoundError
# The TOOL's own entry point, not the manager method behind it --
# `tool-cap-still-narrow` is a regression test for the tool's call site.
from tools.builtins.filesystem import _read_file

_FAILURES: list[str] = []
_PASSES = 0


def check(name: str, ok: bool, detail: str = "") -> None:
    global _PASSES
    if ok:
        _PASSES += 1
        print(f"PASS  {name}" + (f"  ({detail})" if detail else ""))
    else:
        _FAILURES.append(name)
        print(f"FAIL  {name}  {detail}")


# ---------------------------------------------------------------------------
# Pure byte-layer round trips -- no filesystem, no project, no runtime
# ---------------------------------------------------------------------------

def _roundtrip(raw: bytes) -> tuple[bool, dict]:
    meta = describe_bytes(raw)
    # "mixed" has no single correct target by design: B32 reports the
    # counts and F13 picks. For the round-trip property we feed back the
    # style the file actually was.
    ending = "crlf" if meta["line_ending"] == "crlf" else "lf"
    back = encode_for_write(
        meta["content"],
        line_ending=ending,
        bom=meta["bom"],
        trailing_newline=meta["trailing_newline"],
    )
    return back == raw, meta


def byte_layer_checks() -> None:
    print("\n-- byte layer (Done-when 1, 2, and the read-side metadata) --")

    raw = b"\xef\xbb\xbfdef a():\r\n    return 1\r\n"
    exact, meta = _roundtrip(raw)
    check(
        "crlf-bom-roundtrip",
        exact and meta["line_ending"] == "crlf" and meta["bom"] and meta["crlf_count"] == 2,
        f"line_ending={meta['line_ending']} crlf={meta['crlf_count']} bom={meta['bom']}",
    )
    check(
        "crlf-bom-content-has-no-feff",
        not meta["content"].startswith("﻿"),
        "the BOM must be reported in `bom`, not left inside content",
    )

    raw = b"def a():\n    return 1\n"
    exact, meta = _roundtrip(raw)
    check(
        "lf-no-bom-roundtrip",
        exact and meta["line_ending"] == "lf" and not meta["bom"] and meta["lf_count"] == 2,
        f"line_ending={meta['line_ending']} lf={meta['lf_count']}",
    )

    raw = b"no trailing newline"
    exact, meta = _roundtrip(raw)
    check(
        "no-trailing-newline-roundtrip",
        exact and meta["trailing_newline"] is False,
        f"trailing_newline={meta['trailing_newline']}",
    )

    raw = b"a\r\nb\nc\r\n"
    _, meta = _roundtrip(raw)
    check(
        "mixed-endings-reported",
        meta["line_ending"] == "mixed" and meta["crlf_count"] == 2 and meta["lf_count"] == 1,
        f"crlf={meta['crlf_count']} lf={meta['lf_count']} -- B32 reports counts, F13 decides",
    )

    meta = describe_bytes(b"\xff\xfe\x00bad utf8")
    check(
        "decode-replacements-counted",
        meta["decode_replacements"] > 0,
        f"{meta['decode_replacements']} U+FFFD -- F13 opens such a file read-only",
    )


# ---------------------------------------------------------------------------
# write_file against a real runtime
# ---------------------------------------------------------------------------

async def write_checks(root: Path, only: Optional[str]) -> None:
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    print(f"database   {resolve_db_path()}")
    try:
        project = await runtime.projects.init(str(root))
        pm = runtime.projects
        me = "aryan@b32-unit-id"

        if only == "bad-project":
            await bad_project_check(runtime, root)
            return

        print("\n-- the write path --")

        # --- Done-when 9: a lock on a path that does NOT exist yet.
        # An emergent property of resolve(strict=False) + a string
        # relative_to + a DB row. Pinned here because a later
        # strict=True anywhere in that chain breaks creation silently.
        fresh = "brand_new.py"
        assert not (root / fresh).exists()
        lock = await pm.acquire_lock(project, fresh, me)
        check("lock-before-create", lock.ok, "lock acquired on a non-existent path")

        # --- Done-when 9: create via expected_mtime None
        r = await pm.write_file(
            project, fresh, "x = 1\n", holder=me, expected_mtime=None, line_ending="lf"
        )
        check(
            "create-via-null-mtime",
            r.ok and (root / fresh).read_bytes() == b"x = 1\n",
            f"ok={r.ok} reason={r.reason}",
        )
        created_mtime = r.mtime

        # --- Done-when 9: a second create is refused `exists`
        r = await pm.write_file(
            project, fresh, "x = 2\n", holder=me, expected_mtime=None
        )
        check(
            "second-create-refused-exists",
            not r.ok and r.reason == "exists" and (root / fresh).read_bytes() == b"x = 1\n",
            f"reason={r.reason}, file unchanged",
        )

        # --- Done-when 4: stale refusal leaves the file byte-identical
        before = (root / fresh).read_bytes()
        r = await pm.write_file(
            project, fresh, "x = 3\n", holder=me, expected_mtime=created_mtime - 500.0
        )
        check(
            "stale-refused-file-unchanged",
            not r.ok
            and r.reason == "stale"
            and (root / fresh).read_bytes() == before
            and r.mtime is not None
            and abs(r.mtime - (created_mtime - 500.0)) > 1e-6,
            f"reason={r.reason}, reported mtime is the CURRENT one, not the input",
        )

        # --- Done-when 5: overwrite skips the precondition
        r = await pm.write_file(
            project, fresh, "x = 3\n", holder=me, expected_mtime=created_mtime - 500.0,
            overwrite=True,
        )
        on_disk = (root / fresh).stat().st_mtime
        check(
            "overwrite-succeeds",
            r.ok and abs(r.mtime - on_disk) < 1e-6,
            "reported mtime matches a fresh stat",
        )

        # --- Done-when 6, 7: a different holder's fresh lock refuses,
        # and a REFUSED write must not touch the heartbeat.
        other = "someone-else@other-id"
        await pm.take_over_lock(project, fresh, other)
        rel = pm._lock_key(project, fresh)
        hb_before = (await pm._repo.get_lock(project.id, rel))["heartbeat_at"]
        before = (root / fresh).read_bytes()
        r = await pm.write_file(
            project, fresh, "x = 9\n", holder=me, expected_mtime=None, overwrite=True
        )
        hb_after = (await pm._repo.get_lock(project.id, rel))["heartbeat_at"]
        check(
            "locked-refused-no-heartbeat",
            not r.ok
            and r.reason == "locked"
            and r.holder == other
            and (root / fresh).read_bytes() == before
            and hb_after == hb_before,
            f"reason={r.reason} holder={r.holder} heartbeat untouched={hb_after == hb_before}",
        )

        # --- Done-when 7: a SUCCESSFUL write refreshes our own heartbeat
        await pm.take_over_lock(project, fresh, me)
        await pm._repo.touch_heartbeat(project.id, rel, time.time() - 30.0)
        hb_before = (await pm._repo.get_lock(project.id, rel))["heartbeat_at"]
        r = await pm.write_file(
            project, fresh, "x = 4\n", holder=me, expected_mtime=None, overwrite=True
        )
        hb_after = (await pm._repo.get_lock(project.id, rel))["heartbeat_at"]
        check(
            "success-refreshes-own-heartbeat",
            r.ok and hb_after > hb_before,
            f"+{hb_after - hb_before:.1f}s",
        )

        # --- Done-when 8: a write by a holder with NO lock row creates none
        await pm.release_lock(project, fresh, me)
        assert await pm._repo.get_lock(project.id, rel) is None
        r = await pm.write_file(
            project, fresh, "x = 5\n", holder=me, expected_mtime=None, overwrite=True
        )
        check(
            "unlocked-write-creates-no-lock",
            r.ok and await pm._repo.get_lock(project.id, rel) is None,
            "the write path never acquires a lock it was not handed",
        )

        # --- Done-when 10: no parent dir -> refusal, and NO directory made
        missing_dir = root / "nope"
        r = await pm.write_file(
            project, "nope/deep.py", "x\n", holder=me, expected_mtime=None
        )
        check(
            "no-parent-dir-creates-nothing",
            not r.ok and r.reason == "no_parent_dir" and not missing_dir.exists(),
            f"reason={r.reason}, directory not created",
        )

        # --- NEW (Decision 2) -- Done-when 11(a): above editor.max_file_kb
        limit_kb = appconfig.editor_max_file_kb()
        target = "crlf_bom.py" if (root / "crlf_bom.py").exists() else fresh
        before = (root / target).read_bytes()
        huge = "y" * ((limit_kb + 64) * 1024)
        r = await pm.write_file(
            project, target, huge, holder=me, expected_mtime=None, overwrite=True
        )
        check(
            "too-large-refused",
            not r.ok
            and r.reason == "too_large"
            and r.limit_kb == limit_kb
            and r.size > limit_kb * 1024
            and (root / target).read_bytes() == before,
            f"reason={r.reason} size={r.size} limit_kb={r.limit_kb}, file unchanged",
        )

        # --- Done-when 11(b): BETWEEN the two caps, opens and saves
        index_kb = appconfig.config_value("index.max_file_kb", 100)
        between = "between_caps.py"
        body = "\r\n".join(f"x{i} = {i}" for i in range(12000)) + "\r\n"
        (root / between).write_bytes(body.encode("utf-8"))
        size_kb = (root / between).stat().st_size / 1024
        # max_kb explicitly, mirroring what _handle_read_project_file
        # passes. read_file DEFAULTS to index.max_file_kb (100) so the
        # `read_file` TOOL keeps its own narrower cap -- only the
        # editor's wire path opts into 512.
        read = await pm.read_file(
            project, between, max_kb=appconfig.editor_max_file_kb()
        )
        r = await pm.write_file(
            project, between, read["content"], holder=me,
            expected_mtime=read["mtime"], line_ending=read["line_ending"],
            bom=read["bom"], trailing_newline=read["trailing_newline"],
        )
        check(
            "between-caps-opens-and-saves",
            index_kb < size_kb < limit_kb
            and r.ok
            and (root / between).read_bytes() == body.encode("utf-8"),
            f"{size_kb:.0f}KB is between {index_kb} and {limit_kb}; byte-identical after save",
        )

        # The other half of that boundary, and the REGRESSION TEST for a
        # real defect found while building this episode: pointing
        # read_file at editor.max_file_kb also widened the AGENT's
        # read_file TOOL from 100KB to 512KB -- the exact thing two
        # separate numbers exist to prevent.
        #
        # This goes through the TOOL's own entry point
        # (tools\builtins\filesystem.py `_read_file`), not through
        # pm.read_file with a default argument, because the defect was
        # in what the tool's call site resolves to. And it asserts the
        # SIZE BAND itself rather than inheriting it from the check
        # above: a fixture that drifted over 512KB would otherwise still
        # "pass" here while testing nothing.
        size_bytes = (root / between).stat().st_size
        in_band = (index_kb * 1024) < size_bytes < (limit_kb * 1024)

        class _Services:
            def __init__(self, projects, project):
                self.projects = projects
                self.project = project

        class _Ctx:
            def __init__(self, services):
                self.services = services

        tool_result = await _read_file(
            {"path": between}, _Ctx(_Services(pm, project))
        )
        check(
            "tool-cap-still-narrow",
            in_band
            and tool_result.ok is False
            and f"limit {index_kb}KB" in (tool_result.error or "")
            and str(size_bytes) in (tool_result.error or ""),
            f"{size_bytes}B is in ({index_kb}KB, {limit_kb}KB) and the TOOL refused it: "
            f"{(tool_result.error or '')[-52:]}",
        )

        # --- Done-when 12(a): outside the project root RAISES
        outside = str(root.parent / "outside.py")
        try:
            await pm.write_file(
                project, outside, "x\n", holder=me, expected_mtime=None
            )
            check("outside-root-raises", False, "no exception -- it returned a result")
        except PathDeniedError as exc:
            check("outside-root-raises", True, f"PathDeniedError: {str(exc)[:48]}")

        # --- Done-when 13: a failed os.replace leaves the original and NO temp
        locked_target = root / "held_open.py"
        locked_target.write_bytes(b"original\n")
        handle = open(locked_target, "rb")  # Windows: blocks os.replace
        try:
            r = await pm.write_file(
                project, "held_open.py", "replacement\n", holder=me,
                expected_mtime=None, overwrite=True,
            )
            temps = list(root.glob(".point-write-*"))
            ok_or_skip = (
                not r.ok and r.reason == "io_error"
                and locked_target.read_bytes() == b"original\n"
                and not temps
            )
            if r.ok:
                # Some Windows configurations permit the replace while a
                # reader holds the handle. Then the only assertion left
                # that matters is the one about temps.
                check(
                    "failed-replace-leaves-original",
                    not temps,
                    "SKIPPED the refusal half: this host allowed the replace; "
                    "no temp left behind, which is the part that can regress",
                )
            else:
                check(
                    "failed-replace-leaves-original",
                    ok_or_skip,
                    f"reason={r.reason}, original intact, temps={[t.name for t in temps]}",
                )
        finally:
            handle.close()

        # --- the read_only slug
        ro = root / "read_only.py"
        ro.write_bytes(b"locked down\n")
        os.chmod(ro, stat_mod.S_IREAD)
        try:
            r = await pm.write_file(
                project, "read_only.py", "nope\n", holder=me,
                expected_mtime=None, overwrite=True,
            )
            check(
                "read-only-refused",
                not r.ok and r.reason == "read_only" and ro.read_bytes() == b"locked down\n",
                f"reason={r.reason}",
            )
        finally:
            os.chmod(ro, stat_mod.S_IWRITE | stat_mod.S_IREAD)

        # --- end-to-end: a real CRLF+BOM file through read_file -> write_file
        rt = root / "crlf_bom.py"
        if not rt.exists():
            rt.write_bytes(b"\xef\xbb\xbfdef a():\r\n    return 1\r\n")
        original = rt.read_bytes()
        read = await pm.read_file(project, "crlf_bom.py")
        r = await pm.write_file(
            project, "crlf_bom.py", read["content"], holder=me,
            expected_mtime=read["mtime"], line_ending=read["line_ending"],
            bom=read["bom"], trailing_newline=read["trailing_newline"],
        )
        check(
            "end-to-end-crlf-bom-roundtrip",
            r.ok and rt.read_bytes() == original,
            "read_file -> write_file with only the reported metadata, byte-identical",
        )
    finally:
        await shutdown_runtime(runtime)


async def bad_project_check(runtime, root: Path) -> None:
    r"""NEW (Decision 2) -- Done-when 12(b).

    An unresolvable `project_id` must arrive as an Error envelope, not
    as an `ok:false` reason slug. This is a DIFFERENT raiser from 12(a):
    12(a) is resolve_path refusing a contained path, this is
    _require_project failing to find a project at all -- and neither
    exercises the other.

    Unit-level because the CLI resolves its own project from `--path`
    and cannot send a garbage id.
    """
    print("\n-- Done-when 12(b): unresolvable project_id --")
    from runtime.events import IncomingEvent, IncomingEventType

    emitted: list = []
    runtime._emit = lambda ev: emitted.append(ev) or asyncio.sleep(0)  # type: ignore[assignment]

    event = IncomingEvent(
        type=IncomingEventType.WRITE_PROJECT_FILE,
        request_id="b32-bad-project",
        payload={
            "project_id": "definitely-not-a-real-project-id",
            "path": "whatever.py",
            "content": "x\n",
            "holder": "aryan@b32-unit-id",
            "expected_mtime": None,
        },
    )
    try:
        await runtime._handle_write_project_file(event)
        check(
            "bad-project-raises",
            False,
            f"no exception -- it emitted {[e.type for e in emitted]}",
        )
    except ProjectNotFoundError as exc:
        check(
            "bad-project-raises",
            not emitted,
            f"ProjectNotFoundError (-> Error envelope), no ProjectFileWritten emitted: "
            f"{str(exc)[:56]}",
        )


def main() -> int:
    ap = argparse.ArgumentParser(description="B32 write-path gate")
    ap.add_argument("--root", required=True, help="a project directory inside the scratch tree")
    ap.add_argument(
        "--case", default=None, choices=("bad-project",),
        help="run only one case (bad-project is Done-when 12(b))",
    )
    args = ap.parse_args()

    root = Path(args.root).resolve()
    if not root.is_dir():
        print(f"error: --root {root} is not a directory", file=sys.stderr)
        return 1

    if args.case is None:
        byte_layer_checks()
    asyncio.run(write_checks(root, args.case))

    print(f"\n{_PASSES} passed, {len(_FAILURES)} failed")
    if _FAILURES:
        for name in _FAILURES:
            print(f"  FAILED: {name}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
