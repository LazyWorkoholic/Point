r"""B8's two checks the CLI cannot reach.

Run from inside `backend\`:

    uv run python tests\manual_context_test.py <session-id>

Why these two are not in the CLI script:

  * **stm_window.** Nothing sets it from the command line. `session new`
    has no `--stm-window`, and adding one would be building B9's surface
    early -- the policy is supposed to come from an agent config, which
    does not exist yet. Calling `build()` twice with two literals is the
    honest way to show the knob works.

  * **Reasoning-block filtering.** No B4 provider emits a thinking block
    yet, and on a keyless profile the only usable provider is `echo`,
    which never reaches the provider layer at all. So the block has to
    be written into the history directly, then built against two
    different target providers.

Read-only apart from one appended thinking message, which is the point
of the second check. Run it against the scratch profile the B8
verification script sets up, not a session you care about.
"""

from __future__ import annotations

import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import bootstrap
from managers.context import ContextPolicy, ContextRequest, build, default_budget_tokens
from managers.message import ContentType, ThinkingMessage


SYSTEM_PROMPT = "You are a test harness."


async def check_stm_window(runtime, session_id: str, budget: int) -> None:
    print("\n-- stm_window: same input, two policies " + "-" * 30)
    request = ContextRequest(session_id=session_id, provider="anthropic", model="m")
    for window in (2, 50):
        built = await build(
            request,
            ContextPolicy(system_prompt=SYSTEM_PROMPT, stm_window=window),
            budget,
            runtime.memory,
        )
        history = built.log.section_tokens()["history"]
        print(
            f"  stm_window={window:<4} messages={len(built.messages):<4} "
            f"history_tokens={history:<6} used={built.log.used_tokens:<6} "
            f"evicted={len(built.log.evicted)}"
        )
    print("  PASS if the two rows differ. FAIL if they are identical.")


async def check_reasoning_filter(runtime, session_id: str, budget: int) -> None:
    print("\n-- reasoning blocks: tagged anthropic " + "-" * 32)
    marker = "deliberating-in-a-b8-check"
    await runtime.sessions.append_message(
        ThinkingMessage(session_id, marker, "anthropic")
    )

    for target in ("anthropic", "openai"):
        built = await build(
            ContextRequest(session_id=session_id, provider=target, model="m"),
            ContextPolicy(system_prompt=SYSTEM_PROMPT, stm_window=50),
            budget,
            runtime.memory,
        )
        present = any(
            m.content.type is ContentType.THINKING and m.content.text == marker
            for m in built.messages
        )
        filtered = [e for e in built.log.evicted if e.kind == "reasoning"]
        print(f"  target={target:<10} thinking_block_present={present!s:<6} "
              f"filtered_out={len(filtered)}")

    print("  PASS if anthropic=True and openai=False.")
    print("  The block is still in the database either way -- filtered on the")
    print("  way out, never deleted. Confirm with:")
    print(f"    sqlite3 \"$env:LOCALAPPDATA\\Point\\runtime.db\" "
          f"\"select count(*) from messages where content like '%{marker}%';\"")


async def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    session_id = sys.argv[1]

    runtime = await bootstrap.build_runtime(
        single_instance=False, say_hello=False, heartbeat=False
    )
    try:
        session = await runtime.memory.session(session_id)
        if session is None:
            print(f"No session with id {session_id!r}", file=sys.stderr)
            return 1
        budget = default_budget_tokens()
        print(f"session {session_id}  budget {budget} tokens")
        await check_stm_window(runtime, session_id, budget)
        await check_reasoning_filter(runtime, session_id, budget)
    finally:
        await bootstrap.shutdown_runtime(runtime)
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
