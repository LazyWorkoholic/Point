r"""B24 manual check: `parse_fill_proposal()` and `StreamManager`'s
editor-context registration, in isolation.

Unit-level on purpose (episode doc's own call): these are the checks
whose failure mode is a destructive write to the wrong lines, and a
gate that can only exercise them by hoping a small local model
misbehaves on cue is not a gate.

Run from inside `backend\`:

    uv run python tests\manual_fill_proposal_test.py

Exits non-zero if any row broke, so the gate can branch on it.
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from managers.editor_context import MODE_FILL, EditorContext  # noqa: E402
from managers.fill_proposal import (  # noqa: E402
    FillParseFailure,
    FillProposal,
    REASON_BAD_SHAPE,
    REASON_FILE_MISMATCH,
    REASON_NOT_JSON,
    REASON_RANGE_INVERTED,
    REASON_RANGE_OUTSIDE_SHOWN,
    parse_fill_proposal,
)
from streaming.manager import StreamManager  # noqa: E402

fails = []


def check(label, got, want):
    ok = got == want
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}  -> {got!r}"
          if ok else f"  [FAIL] {label}\n       got={got!r}\n       want={want!r}")
    if not ok:
        fails.append(label)


EXPECTED_FILE = "D:\\Projects\\Point\\backend\\sample.py"

# B36: dummy stat values -- parse_fill_proposal() never validates these,
# only carries them, so any fixed pair exercises the plumbing.
STAT_MTIME = 1_700_000_000.5
STAT_SIZE = 4096


def parse(
    payload: dict, *, file=EXPECTED_FILE, shown_start=1, shown_end=100,
    mtime=STAT_MTIME, size=STAT_SIZE, raw=None,
):
    text = raw if raw is not None else json.dumps(payload)
    return parse_fill_proposal(
        text, file=file, shown_start=shown_start, shown_end=shown_end,
        mtime=mtime, size=size,
    )


print("=== validation table")

result = parse({
    "file": "sample.py", "start_line": 10, "end_line": 5,
    "replacement": "x", "note": None,
})
check("inverted range rejected", isinstance(result, FillParseFailure) and result.reason,
      REASON_RANGE_INVERTED)

result = parse(
    {"file": "sample.py", "start_line": 1, "end_line": 5, "replacement": "x", "note": None},
    shown_start=10, shown_end=20,
)
check("range outside shown rejected", isinstance(result, FillParseFailure) and result.reason,
      REASON_RANGE_OUTSIDE_SHOWN)

result = parse({
    "file": "other/sample.py", "start_line": 2, "end_line": 4,
    "replacement": "x", "note": None,
})
check("a different file (even same basename, with a dir component) rejected",
      isinstance(result, FillParseFailure) and result.reason, REASON_FILE_MISMATCH)

result = parse({
    "file": "unrelated.py", "start_line": 2, "end_line": 4,
    "replacement": "x", "note": None,
})
check("a plainly different file rejected",
      isinstance(result, FillParseFailure) and result.reason, REASON_FILE_MISMATCH)

result = parse({
    "file": "sample.py", "start_line": 2, "end_line": 4, "replacement": "x", "note": None,
})
check("bare basename matching the context's own filename accepted",
      isinstance(result, FillProposal), True)

print("\n=== shape")

result = parse({
    "file": "sample.py", "start_line": 2, "end_line": 4, "replacement": "", "note": None,
})
check("empty replacement accepted (deleting a region is legitimate)",
      result.replacement if isinstance(result, FillProposal) else result, "")

result = parse({
    "file": "sample.py", "start_line": 12.0, "end_line": 20, "replacement": "x", "note": None,
})
check("a float start_line rejected bad_shape", isinstance(result, FillParseFailure) and result.reason,
      REASON_BAD_SHAPE)

result = parse({
    "file": "sample.py", "start_line": "12", "end_line": 20, "replacement": "x", "note": None,
})
check("a string start_line rejected bad_shape", isinstance(result, FillParseFailure) and result.reason,
      REASON_BAD_SHAPE)

result = parse({
    "file": "sample.py", "start_line": 2, "end_line": 4,
    "replacement": "x", "note": None, "confidence": 0.9,
})
check("an unknown extra key is ignored, not rejected", isinstance(result, FillProposal), True)

result = parse({
    "file": "sample.py", "start_line": 2, "end_line": 4, "replacement": "x", "note": None,
})
check("B36: mtime/size travel through to_dict() unchanged",
      isinstance(result, FillProposal) and (result.to_dict()["mtime"], result.to_dict()["size"]),
      (STAT_MTIME, STAT_SIZE))

print("\n=== fences and prose")

fenced = "```json\n" + json.dumps({
    "file": "sample.py", "start_line": 2, "end_line": 4, "replacement": "x", "note": None,
}) + "\n```"
result = parse({}, raw=fenced)
check("a reply that is ONE fenced JSON block is accepted", isinstance(result, FillProposal), True)

with_prose = (
    "Here is the proposal:\n```json\n"
    + json.dumps({"file": "sample.py", "start_line": 2, "end_line": 4, "replacement": "x", "note": None})
    + "\n```\nLet me know if this works."
)
result = parse({}, raw=with_prose)
check("JSON with commentary around it rejected not_json",
      isinstance(result, FillParseFailure) and result.reason, REASON_NOT_JSON)

result = parse({}, raw="I can only write one region in one file at a time.")
check("a plain-text refusal rejected not_json",
      isinstance(result, FillParseFailure) and result.reason, REASON_NOT_JSON)

print("\n=== StreamManager: reachable from the echo path, and no leak")

sm = StreamManager()
ec = EditorContext(file=EXPECTED_FILE, mode=MODE_FILL)
sm.register_editor_context("task-1", ec, 1, 50, STAT_MTIME, STAT_SIZE)

check("registered without ever registering a stream (the echo/refusal path)",
      sm.has_stream("task-1"), False)
check("editor_context_for reachable regardless", sm.editor_context_for("task-1") is not None, True)
check("B36: mtime/size carried on RegisteredEditorContext beside shown_start/shown_end",
      (sm.editor_context_for("task-1").mtime, sm.editor_context_for("task-1").size),
      (STAT_MTIME, STAT_SIZE))

sm.close_stream("task-1")
check("close_stream clears it -- no leak per fill turn", sm.editor_context_for("task-1"), None)

print("\n" + ("ALL FILL PROPOSAL CHECKS PASSED" if not fails else "FAILED: " + "; ".join(fails)))
sys.exit(1 if fails else 0)
