r"""B27 manual check: `behaviour_resolved` in `GetDoctorReport`, driven
through ONE backend process.

Pattern: `tests\manual_b26_behaviour_overrides.py` -- one long-lived
Runtime (`build_runtime(single_instance=False, say_hello=False,
heartbeat=False)`), driven via `runtime.receive()`, draining the outbox
between steps. Reused here because Done-when #4 needs an override set
in the SAME process that `GetDoctorReport` is then asked about, exactly
the shape B26's own Done-when #7 needed for `behaviour_overrides`.

Run it from inside `backend\`, against a scratch profile (APPDATA and
LOCALAPPDATA both already pointed at a scratch dir by the invoking
PowerShell script -- see this episode's verification gate):

    uv run python tests\manual_b27_doctor_resolved.py

It prints what it observed and exits non-zero if any expectation
failed.

Scope: Done-when #1-#4 only. #5 (byte-identical `cli.py doctor` stdout)
is a separate PowerShell diff, not an in-process check.
"""

from __future__ import annotations

import asyncio
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import appconfig  # noqa: E402
from bootstrap import build_runtime, resolve_db_path, shutdown_runtime  # noqa: E402
from runtime.events import ENVELOPE_VERSION  # noqa: E402

EXPECTED_DEFAULTS = {
    "chat.queue_depth": 1,
    "limits.max_concurrent_tasks": 3,
    "project.allow_outside_project": False,
    "index.respect_gitignore": True,
    "research.stale_after_days": 7,
}

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


async def drain(runtime, limit: int = 400) -> list[dict]:
    seen = []
    for _ in range(limit):
        try:
            event = await asyncio.wait_for(runtime.outbox.get(), timeout=0.05)
        except asyncio.TimeoutError:
            break
        seen.append(event.to_dict())
    return seen


async def get_doctor_report(runtime) -> dict:
    await send_event(runtime, "GetDoctorReport")
    events = await drain(runtime)
    reports = [e for e in events if e["type"] == "DoctorReport"]
    check("GetDoctorReport returned a DoctorReport", bool(reports), str(events))
    return reports[-1]["payload"] if reports else {}


async def main() -> int:
    runtime = await build_runtime(
        single_instance=False, say_hello=False, heartbeat=False,
    )
    db_path = resolve_db_path()
    print(f"database     {db_path}")
    print(f"config.toml  {appconfig.config_path()}")

    try:
        # ---------------------------------------------------------------
        # Done-when #1 + #2: fresh scratch profile, no config.toml value
        # for any of the five keys -- behaviour_resolved has exactly 5
        # keys, each `resolved` matching RESOLVED_DEFAULTS, each
        # `overridden=False`.
        # ---------------------------------------------------------------
        print("\n=== #1/#2  fresh profile: behaviour_resolved == hardcoded fallbacks, nothing overridden")
        report = await get_doctor_report(runtime)
        resolved = report.get("behaviour_resolved", {})

        check(
            "behaviour_resolved has exactly the five expected keys",
            set(resolved.keys()) == set(EXPECTED_DEFAULTS.keys()),
            str(sorted(resolved.keys())),
        )
        for key, default in EXPECTED_DEFAULTS.items():
            entry = resolved.get(key, {})
            check(
                f"{key}: resolved == {default!r} (hardcoded fallback)",
                entry.get("resolved") == default,
                str(entry),
            )
            check(
                f"{key}: overridden is False",
                entry.get("overridden") is False,
                str(entry),
            )

        # ---------------------------------------------------------------
        # Done-when #3: a REAL config.toml value for one key, no override
        # active -- resolved reflects the FILE's value, overridden stays
        # False. load_config() re-reads config.toml fresh on every call
        # (appconfig.py has no cache), so writing the file directly and
        # re-querying the SAME runtime is enough; no restart needed.
        # ---------------------------------------------------------------
        print("\n=== #3  a real config.toml value is reflected, with no override active")
        config_path = appconfig.config_path()
        config_path.parent.mkdir(parents=True, exist_ok=True)
        config_path.write_text(
            "[research]\nstale_after_days = 14\n", encoding="utf-8",
        )

        report = await get_doctor_report(runtime)
        entry = report.get("behaviour_resolved", {}).get("research.stale_after_days", {})
        check(
            "research.stale_after_days: resolved == 14 (config.toml's value, not the fallback)",
            entry.get("resolved") == 14,
            str(entry),
        )
        check(
            "research.stale_after_days: overridden is still False",
            entry.get("overridden") is False,
            str(entry),
        )

        # ---------------------------------------------------------------
        # Done-when #4: with an override active, resolved reflects
        # config.toml's value -- NEVER the override's own value -- while
        # overridden is True. Confirms behaviour_resolved and
        # behaviour_overrides don't contradict each other's own
        # documented meaning.
        # ---------------------------------------------------------------
        print("\n=== #4  an active override masks the key, but resolved stays config.toml's value")
        await send_event(runtime, "SetBehaviourOverride", key="research.stale_after_days", value=3)
        await drain(runtime)

        report = await get_doctor_report(runtime)
        entry = report.get("behaviour_resolved", {}).get("research.stale_after_days", {})
        overrides = report.get("behaviour_overrides", {})
        check(
            "research.stale_after_days: resolved STILL == 14 (config.toml's value, unchanged from #3)",
            entry.get("resolved") == 14,
            str(entry),
        )
        check(
            "research.stale_after_days: overridden is now True",
            entry.get("overridden") is True,
            str(entry),
        )
        check(
            "behaviour_overrides (unchanged by this episode) separately shows the override's own value, 3",
            overrides.get("research.stale_after_days") == 3,
            str(overrides),
        )
        check(
            "behaviour_resolved['resolved'] (14) and behaviour_overrides' value (3) disagree on purpose -- "
            "proves behaviour_resolved is not config_value_live() wearing a new name",
            entry.get("resolved") != overrides.get("research.stale_after_days"),
            f"resolved={entry.get('resolved')!r} override={overrides.get('research.stale_after_days')!r}",
        )

    finally:
        await shutdown_runtime(runtime)

    print()
    if FAILURES:
        print(f"FAILED: {len(FAILURES)} check(s) -- {FAILURES}")
        return 1
    print("ALL CHECKS PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
