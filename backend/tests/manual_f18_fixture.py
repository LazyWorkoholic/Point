r"""F18 fixture builder: the ONE session shape the windowed transcript's
pairing behaviour cannot be verified without.

`cli.py seed-session` writes plain alternating user/assistant rows with no
`task_id` on any of them, so pairReloaded is a no-op over it and
decorateReloaded produces an all-nil `hist`. That session is exactly right
for F18's memory proof and exercises NONE of the slot-reconstruction logic
the episode's decision 3 is about -- window-edge pairing, and the orphan
placeholder that must not be invented away from the tail.

This builds the three exchanges that do, deep inside a padded history so a
50-row resident window cannot hold them all at once:

  A. a research task that FAILED AFTER PARTIAL OUTPUT -- a real assistant row
     exists, carrying its own `outcome`. This is the row whose eviction would
     make an ungated tasks join invent a "failed" placeholder for a question
     that has a perfectly good answer one batch below. It cannot be produced
     on demand by a real send (nothing can make the demo provider drop a
     connection halfway), which is why it is written here rather than driven
     through the wire.

  B. a research task that produced NOTHING, cancelled before its first token.
     No assistant row at all -- B12 accepted that deliberately
     (StreamAccumulator.finalize_interrupted returns None for empty text),
     and the outcome lives on the `tasks` row instead. This is the ONE case
     applyTaskRows' two-table join exists for, so it is what proves the join
     still fires at the true tail after F18 gated it.

  C. a clean research exchange, so a correctly paired collapsed slot is on
     screen beside the two awkward ones.

Everything goes through SessionManager.append_message and
TaskRepository.record -- the same paths a real turn uses -- so `seq` is
assigned by the database (B18: never by a caller) and the stored
content/metadata encoding is the real one rather than hand-written JSON.

Run from inside `backend\`, against a scratch profile whose APPDATA and
LOCALAPPDATA are already overridden:

    uv run python tests\manual_f18_fixture.py <session-id>

It prints the seq of every row it wrote, which is what the verification gate
tells you to scroll to, and exits non-zero if anything it expected to be
true of its own output was not.
"""

from __future__ import annotations

import asyncio
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from managers.message import (  # noqa: E402
    InvocationMetadata,
    Message,
    MessageRole,
    Outcome,
)

RESEARCH = "research"

# How much filler sits between the exchanges. Chosen against the gate's own
# config (ui.scrollback = 50, ui.historyBatch = 10): one gap is five batches,
# so no resident window can ever hold two exchanges at once and every one of
# them is reached across a seam.
GAP = 60

FAILURES: list[str] = []


def check(ok: bool, what: str) -> None:
    if ok:
        print(f"  ok    {what}")
    else:
        print(f"  FAIL  {what}")
        FAILURES.append(what)


async def filler(runtime, session_id: str, count: int, tag: str) -> None:
    """Plain rows with no task_id, the same shape seed-session writes."""
    for i in range(count):
        role = MessageRole.USER if i % 2 == 0 else MessageRole.ASSISTANT
        await runtime.sessions.append_message(Message.new(
            session_id, role,
            text=f"{tag} filler {i + 1}/{count} -- padding so the exchanges "
                 "below sit deep enough in history that one resident window "
                 "cannot hold two of them.",
        ))


async def exchange(
    runtime,
    session_id: str,
    question: str,
    *,
    answer: str | None,
    status: str,
    outcome: Outcome | None,
    error_code: str | None = None,
    error_text: str | None = None,
) -> tuple[str, int, int | None]:
    """One question, its task row, and (unless `answer` is None) its answer.

    Returns (task_id, question seq, answer seq or None). The question is
    appended BEFORE the answer because that is the order the backend really
    persists them -- executor.py generates the task_id, appends the
    UserMessage, then runs -- and pairReloaded's "an answer is always
    downstream of its question" rule depends on it.
    """
    task_id = str(uuid.uuid4())
    now = time.time()

    q = Message.new(
        session_id, MessageRole.USER, text=question, task_id=task_id,
        invocation=InvocationMetadata(agent=RESEARCH),
    )
    await runtime.sessions.append_message(q)

    answer_seq = None
    if answer is not None:
        meta = {}
        if error_text:
            # The same key StreamAccumulator.finalize_interrupted writes, and
            # the one reload.go's messageErrText prefers, so a reloaded
            # failure names the real error exactly as the live footer did.
            meta["error"] = error_text
        a = Message.new(
            session_id, MessageRole.ASSISTANT, text=answer, task_id=task_id,
            invocation=InvocationMetadata(agent=RESEARCH, provider="demo", model="demo"),
            outcome=outcome, error_code=error_code, metadata=meta,
        )
        await runtime.sessions.append_message(a)
        answer_seq = a.seq

    await runtime.task_repo.record(
        task_id=task_id, session_id=session_id, kind="agent", status=status,
        created_at=now, started_at=now, finished_at=now + 42,
        error_code=error_code, agent_id=RESEARCH,
    )
    return task_id, q.seq, answer_seq


async def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__)
        print("error: exactly one argument is required, the session id", file=sys.stderr)
        return 2
    session_id = sys.argv[1]

    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    print(f"database   {resolve_db_path()}")
    try:
        session = await runtime.sessions.get(session_id)
        print(f"session    {session.id}  {session.title!r}  [{session.provider}/{session.model}]")

        await filler(runtime, session_id, GAP, "pre-A")
        a_task, a_q, a_a = await exchange(
            runtime, session_id,
            "dig into how the runtime dispatches events",
            answer="Partial answer: the dispatcher routes by event type, and then"
                   " the connection dropped mid-sentence so this row is all there is",
            status="failed", outcome=Outcome.FAILED,
            error_code="provider_error",
            error_text="the provider closed the stream after 1.2 KB",
        )
        print(f"\nA  failed AFTER partial output   task={a_task}")
        print(f"   question seq={a_q}   answer seq={a_a}")

        await filler(runtime, session_id, GAP, "A-to-B")
        b_task, b_q, b_a = await exchange(
            runtime, session_id,
            "dig into the scheduler's lane caps",
            answer=None,
            status="cancelled", outcome=None,
        )
        print(f"\nB  produced NOTHING (cancelled)  task={b_task}")
        print(f"   question seq={b_q}   answer seq={b_a}  (there is no answer row, by design)")

        await filler(runtime, session_id, GAP, "B-to-C")
        c_task, c_q, c_a = await exchange(
            runtime, session_id,
            "dig into the streaming accumulator",
            answer="A complete research answer, which live rendered as a collapsed"
                   " slot and must reload as one too.",
            status="completed", outcome=Outcome.DONE,
        )
        print(f"\nC  clean research exchange       task={c_task}")
        print(f"   question seq={c_q}   answer seq={c_a}")

        await filler(runtime, session_id, GAP, "post-C")

        total = await runtime.sessions._repo.fetch_message_count(session_id)
        print(f"\ntotal messages in this session: {total}")

        # Prove the fixture is what it claims before anyone scrolls to it --
        # a gate that verifies a fixture it never checked is B18's own
        # "green while verifying almost nothing" failure, one layer down.
        print("\nself-checks:")
        check(a_a is not None, "A has a real assistant row (the eviction case needs one)")
        check(b_a is None, "B has NO assistant row (the orphan-join case needs none)")
        check(c_a is not None, "C has a real assistant row")
        check(a_q < a_a < b_q < c_q, "every exchange is in order and A's answer follows its question")
        check(b_q - a_a > GAP, "A and B are more than one resident window apart")
        check(c_q - b_q > GAP, "B and C are more than one resident window apart")
        for name, tid, want in (("A", a_task, "failed"), ("B", b_task, "cancelled"), ("C", c_task, "completed")):
            row = await runtime.task_repo.fetch_one(tid)
            check(row is not None and row["status"] == want,
                  f"{name}'s tasks row says status={want}")
    finally:
        await shutdown_runtime(runtime)

    print(f"\n{'FIXTURE OK' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
