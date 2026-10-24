"""UsageManager: what every request cost, and the one spending cap that
exists before B16 gives a request an identity (B14).

Two things live here, and both read from config.toml, never from a
hardcoded table (the episode's own hard rule):

  * Pricing -- `[pricing.<provider>.<model>]` -> cost_micros, or NULL
    for any provider/model config does not price. A guess is worse
    than NULL: NULL shows up in the UI as "tokens only", a guessed
    price shows up looking exactly like a real one.

  * The daily cap -- `[limits] daily_token_cap`, checked against the
    running total of TODAY's `usage_records` (reset at server-local
    midnight), with no per-user split. There is no user identity
    anywhere in this backend yet -- B16 (SSH Serving) is what adds
    one, and it depends on B15 and F12, not on this episode. Building
    a stub "guest" role here just to satisfy B14's original spec would
    be inventing identity this codebase does not have; dropped per
    decision, see decisions.md D66-D69 and B16-ssh-serving.md for where
    the real thing belongs. This cap is not a placeholder for B16 to
    tear out -- it is the no-identity case B16 adds a user filter on
    top of, using the same `usage_records` rows.

### What "refuses before spending" actually means here

The real cost of a call -- in tokens, let alone cost_micros -- is not
knowable until the provider responds (see `request.max_tokens` in
providers\\models.py: only anthropic\\provider.py defaults an unset one
to 4096; openai and openrouter pass `None` straight through, so there
is no upper bound to check against before most calls go out). So
`check_cap()` cannot enforce "this call will not cross the cap" -- no
budget figure for the upcoming call exists to compare against.

What it DOES enforce, exactly: before a task is created, before the
user's message is persisted, and before any provider call is made,
compare today's ALREADY-RECORDED total (exact, from disk, not a guess)
to the cap. If that alone is at or past the cap, refuse. This is
"stops the next request once a completed turn's actual usage pushed
the total over" -- not "never crosses the cap on the request that
pushes it over", because that request's size is the thing that is not
knowable in advance. See runtime\\errors.py's UsageCapExceededError for
the same point made where the UI meets it.
"""

from __future__ import annotations

import time
from datetime import datetime
from typing import Optional

import appconfig
from database.usage_repository import UsageRecord, UsageRepository
from runtime.errors import UsageCapExceededError


def _local_midnight_ts(now: Optional[float] = None) -> float:
    """Server-local midnight, as an epoch timestamp. Local, not UTC --
    the episode's own wording ("server-local midnight") and the one
    that matches what a human reading "today" on this machine means."""
    moment = datetime.fromtimestamp(now if now is not None else time.time())
    midnight = moment.replace(hour=0, minute=0, second=0, microsecond=0)
    return midnight.timestamp()


def _pricing_for(provider: Optional[str], model: Optional[str]) -> Optional[dict]:
    """`[pricing.<provider>.<model>]`, read without appconfig.config_value's
    dotted-path split -- a model id can itself contain a dot (`gpt-4.1`,
    `claude-3.5-sonnet`), which `"pricing.openai.gpt-4.1".split(".")`
    would cut in the wrong place. Indexed by hand instead."""
    if not provider or not model:
        return None
    pricing = appconfig.load_config().get("pricing")
    if not isinstance(pricing, dict):
        return None
    provider_table = pricing.get(provider)
    if not isinstance(provider_table, dict):
        return None
    entry = provider_table.get(model)
    return entry if isinstance(entry, dict) else None


def cost_micros_for(
    provider: Optional[str], model: Optional[str], input_tokens: int, output_tokens: int,
) -> Optional[int]:
    """NULL whenever the model is not in config.toml's `[pricing]`
    table -- never a guessed figure. `input_per_mtok`/`output_per_mtok`
    are dollars per million tokens; cost_micros is a dollar millionth,
    chosen so a $0.00 model (every one of the five shipped at 0.00 by
    the episode's own instruction) still records an honest 0 rather
    than forcing a float column for fractions of a cent.
    """
    entry = _pricing_for(provider, model)
    if entry is None:
        return None
    try:
        input_rate = float(entry.get("input_per_mtok", 0.0))
        output_rate = float(entry.get("output_per_mtok", 0.0))
    except (TypeError, ValueError):
        return None
    cost_dollars = (input_tokens * input_rate + output_tokens * output_rate) / 1_000_000.0
    return round(cost_dollars * 1_000_000)


class UsageManager:
    def __init__(self, repo: UsageRepository) -> None:
        self._repo = repo

    # -- capture --------------------------------------------------------

    async def record(
        self,
        *,
        session_id: Optional[str],
        task_id: Optional[str],
        agent_id: Optional[str],
        provider: Optional[str],
        model: Optional[str],
        input_tokens: int,
        output_tokens: int,
        estimated: bool,
    ) -> UsageRecord:
        cost_micros = cost_micros_for(provider, model, input_tokens, output_tokens)
        return await self._repo.insert(
            session_id=session_id, task_id=task_id, agent_id=agent_id,
            provider=provider, model=model,
            input_tokens=input_tokens, output_tokens=output_tokens,
            cost_micros=cost_micros, estimated=estimated,
        )

    # -- aggregation ------------------------------------------------------

    async def session_totals(self, session_id: str) -> dict:
        return await self._repo.session_totals(session_id)

    async def task_totals(self, task_id: str) -> dict:
        return await self._repo.task_totals(task_id)

    async def today_totals(self) -> dict:
        return await self._repo.totals_since(_local_midnight_ts())

    async def by_agent(self) -> list[dict]:
        return await self._repo.by_agent()

    async def by_provider(self) -> list[dict]:
        return await self._repo.by_provider()

    # -- the cap ----------------------------------------------------------

    @staticmethod
    def daily_cap_tokens() -> Optional[int]:
        """`[limits] daily_token_cap`. Absent, or 0, means no cap --
        zero tokens is not a real limit anyone would configure, and
        treating it as one would mean an empty `[limits]` table (which
        config.toml ships without, since this file is never written
        for the user -- see its own 'NEVER rewritten' rule) silently
        blocked every request the moment the section existed."""
        raw = appconfig.config_value("limits.daily_token_cap")
        if raw is None:
            return None
        try:
            cap = int(raw)
        except (TypeError, ValueError):
            return None
        return cap if cap > 0 else None

    async def check_cap(self) -> None:
        """Raises UsageCapExceededError if today's recorded total is
        already at or past the configured cap. See this module's
        docstring for exactly what guarantee this is and is not."""
        cap = self.daily_cap_tokens()
        if cap is None:
            return
        totals = await self.today_totals()
        used = totals["input_tokens"] + totals["output_tokens"]
        if used >= cap:
            raise UsageCapExceededError(used_tokens=used, cap_tokens=cap)

    async def remaining_cap(self) -> Optional[dict]:
        """None when no cap is configured; otherwise {cap, used,
        remaining} for the status panel's push event."""
        cap = self.daily_cap_tokens()
        if cap is None:
            return None
        totals = await self.today_totals()
        used = totals["input_tokens"] + totals["output_tokens"]
        return {"cap": cap, "used": used, "remaining": max(cap - used, 0)}
