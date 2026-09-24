r"""Prompt-isolation regression: every task's PROMPT answers its own question.

The defect this guards (found live, confirmed from raw persisted rows):
three sends fired back-to-back into one session -- "perform bubble sorting
with code", "tell me importance of bees", "hi" -- and the task created for
"hi" came back with a fresh bubble-sort answer. Its persisted row had the
right task_id. The PROMPT behind it did not have the right question at the
end of it alone: context.build() ran at task creation, when the two earlier
questions were already persisted and still unanswered, and B21's orphan
filter deliberately kept unanswered questions from IN-FLIGHT tasks in the
history window. So the "hi" prompt was [bubble sort?, bees?, hi] with no
answers between them, and the model answered the first unanswered question
it saw.

A persisted task_id cannot prove this is fixed -- the broken rows had
correct task_ids. So this script checks the only thing that can: the exact
message list each task handed to provider.stream(). MockProvider.stream is
wrapped at the class level, before the runtime is built, so every task's
real outgoing prompt is recorded exactly as the provider receives it.

Run from inside backend\, against a SCRATCH profile (CLAUDE.md's
APPDATA/LOCALAPPDATA override -- never the real one):

    uv run python tests\manual_prompt_isolation.py

Exits non-zero if any check fails, and prints the raw persisted rows for
each scenario so the gate has unedited database evidence, not a summary.
"""

from __future__ import annotations

import asyncio
import os
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

# Must be set BEFORE the provider registry is built: the mock only
# registers under POINT_MOCK=1 (providers/mock/provider.py).
os.environ["POINT_MOCK"] = "1"
os.environ.setdefault(
    "POINT_MOCK_FIXTURE",
    str(Path(__file__).resolve().parent / "fixtures" / "mock_slow.json"),
)

from bootstrap import build_runtime, shutdown_runtime  # noqa: E402
from managers.message import MessageRole  # noqa: E402
from providers.mock.provider import MockProvider  # noqa: E402
import runtime.runtime as runtime_module  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

FAILURES: list[str] = []

# Every prompt any task sends, exactly as MockProvider.stream receives it:
# a list of (role, text) per stream() call, in call order.
CAPTURED: list[list[tuple[str, str]]] = []

_original_stream = MockProvider.stream


def _recording_stream(self, request):
    CAPTURED.append([(m.role.value, m.content.text or "") for m in request.messages])
    return _original_stream(self, request)


MockProvider.stream = _recording_stream  # type: ignore[method-assign]

# chat.queue_depth defaults to 1 (one streaming + one waiting, a third
# refused with queue_full). Raised IN-PROCESS for this driver only, so an
# all-plain burst of four can be accepted without writing a config file
# into anyone's profile. runtime.py imported the function by name, so the
# name it calls is the one patched.
runtime_module.turn_queue_depth = lambda: 4


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}{('  -- ' + detail) if detail else ''}")
    if not ok:
        FAILURES.append(label)


async def send(runtime, session_id: str, text: str) -> None:
    await runtime.receive({
        "v": ENVELOPE_VERSION,
        "id": str(uuid.uuid4()),
        "type": "SendMessage",
        "payload": {"session": session_id, "text": text},
    })


async def drain_until_idle(runtime, timeout: float = 90.0) -> None:
    """Drain the outbox (an undrained bounded queue blocks the runtime)
    until no task is active, bounded so a hang fails instead of spinning."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            await asyncio.wait_for(runtime.outbox.get(), timeout=0.1)
            continue
        except asyncio.TimeoutError:
            pass
        if not runtime.tasks.list_active():
            # One last drain for anything emitted after the final task.
            for _ in range(200):
                try:
                    await asyncio.wait_for(runtime.outbox.get(), timeout=0.05)
                except asyncio.TimeoutError:
                    break
            return
    FAILURES.append(f"tasks still active after {timeout}s")


def user_turns(prompt: list[tuple[str, str]]) -> list[str]:
    return [text for role, text in prompt if role == MessageRole.USER.value]


async def scenario(runtime, title: str, sends: list[tuple[str, str]]) -> None:
    """`sends` is (raw text as typed, the question text a prompt should end
    on). They differ only for @research, whose mention is stripped."""
    print(f"\n=== {title}")
    session = await runtime.sessions.create(title=title, provider="mock", model="mock-slow")
    before = len(CAPTURED)

    # ZERO wait between sends: every task is created, and its context
    # built, while the earlier ones are still queued or streaming.
    for raw, _ in sends:
        await send(runtime, session.id, raw)

    await drain_until_idle(runtime)
    prompts = CAPTURED[before:]
    questions = [q for _, q in sends]

    print(f"  captured {len(prompts)} prompt(s) sent to the provider:")
    for i, prompt in enumerate(prompts, 1):
        print(f"    prompt {i}: user turns = {user_turns(prompt)!r}")

    check(f"{len(sends)} sends -> {len(sends)} prompts", len(prompts) == len(sends),
          f"got {len(prompts)}")

    for question in questions:
        owning = [p for p in prompts if user_turns(p) and user_turns(p)[-1] == question]
        check(f"a prompt ends on its own question {question!r}", len(owning) == 1,
              f"{len(owning)} prompt(s) end on it")
        for prompt in owning:
            leaked = [q for q in questions if q != question and q in user_turns(prompt)]
            check(f"prompt for {question!r} contains no sibling's unanswered question",
                  not leaked, f"leaked: {leaked!r}")

    rows = await runtime.memory.recent_messages(session.id, 100)
    print("  raw persisted rows (role | task_id | text[:60]):")
    for m in rows:
        text = (m.content.text or "").replace("\n", " ")[:60]
        print(f"    {m.role.value:<9} | {(m.task_id or '-')[:8]} | {text!r}")


async def main() -> int:
    runtime = await build_runtime(single_instance=False, say_hello=False, heartbeat=False)
    try:
        # The live repro's exact shape: one background @research plus two
        # plain turns, none waited on, none failing.
        await scenario(runtime, "mixed: research + 2 plain", [
            ("@research perform bubble sorting with code", "perform bubble sorting with code"),
            ("tell me importance of bees", "tell me importance of bees"),
            ("hi", "hi"),
        ])
        # Reported as reproducing with all-plain sends too -- the turn
        # lane alone, no background task involved.
        await scenario(runtime, "all plain: four back-to-back", [
            ("perform bubble sorting with code", "perform bubble sorting with code"),
            ("tell me importance of bees", "tell me importance of bees"),
            ("hi", "hi"),
            ("what is 2+2", "what is 2+2"),
        ])
        # Continuity must survive the fix: a question sent AFTER an earlier
        # exchange completed still sees that completed exchange.
        print("\n=== continuity: a completed exchange stays in later prompts")
        session = await runtime.sessions.create(title="continuity", provider="mock", model="mock-slow")
        await send(runtime, session.id, "first question")
        await drain_until_idle(runtime)
        before = len(CAPTURED)
        await send(runtime, session.id, "second question")
        await drain_until_idle(runtime)
        prompt = CAPTURED[before] if len(CAPTURED) > before else []
        turns = user_turns(prompt)
        print(f"    prompt user turns = {turns!r}")
        check("the answered first question is still in the second prompt",
              turns == ["first question", "second question"], repr(turns))
        has_answer = any(role == MessageRole.ASSISTANT.value for role, _ in prompt)
        check("and so is its answer", has_answer)
    finally:
        await shutdown_runtime(runtime)
        MockProvider.stream = _original_stream  # type: ignore[method-assign]

    print(f"\n{'ALL CHECKS PASSED' if not FAILURES else 'FAILED: ' + '; '.join(FAILURES)}")
    return 0 if not FAILURES else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
