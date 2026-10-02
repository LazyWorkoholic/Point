"""tests/manual_b25_key_validation.py

Manual, human-run regression test for B25's validation call.

The bug class this guards against: a provider's "validate this key"
call that doesn't actually validate the key.

  - AnthropicProvider.list_models() (providers/anthropic/provider.py)
    is a hardcoded static list -- it never touches the network or looks
    at the key at all. This is why B25 validates with health() instead
    of list_models().
  - OpenRouterProvider.health() (providers/openrouter/provider.py) USED
    TO call OpenRouter's `/models` endpoint -- which, confirmed live, is
    PUBLIC: it returns the full 465-model catalog for a garbage key with
    no auth check whatsoever. Same shape of bug, surfacing through a
    real HTTP 200 instead of a hardcoded return. Fixed by switching to
    OpenRouter's `/auth/key` endpoint, which does 401 on an invalid key.

Both bugs let `Runtime._handle_set_provider_api_key` persist a garbage
key to credentials.toml, because the call it used to decide "did this
validate" reported success regardless of the key. A regression test
that only ever exercises ONE provider's garbage-key path cannot catch
this class of bug in the others -- which is exactly how the OpenRouter
case above shipped past the first version of this file, which skipped
every provider with no real key configured instead of still checking
that a WRONG key gets rejected (rejecting garbage never needs a real
key to compare against).

So this file checks two independent things per key-based provider,
mirroring exactly what Runtime._handle_set_provider_api_key now does
(construct a provider, await health(), inspect .status):

  1. UNCONDITIONALLY, for every one of anthropic / openai / gemini /
     openrouter: a garbage key must come back with status != HEALTHY.
     This needs no real key and is never skipped -- rejecting a wrong
     key doesn't depend on knowing a right one.
  2. For whichever of anthropic / openai / gemini / openrouter / ollama
     you have a REAL key configured for (env var or credentials.toml --
     same resolution appconfig.resolve_api_key() always uses): that key
     must still come back HEALTHY, proving the validation call doesn't
     newly reject a key that used to work. Skipped (and reported as
     such) for a provider you have no real key for.

This hits real provider APIs over the network: one deliberately-wrong
request per key-based provider (each expected to be rejected), and, for
whichever providers you have real keys for, one real health check each.
No message is generated and no account/billing-relevant action is taken
in either case. Read-only against appconfig -- it never calls
save_api_key(), so it is safe to run against your real profile rather
than a scratch one.

Run from inside backend\\:

    uv run python tests\\manual_b25_key_validation.py

Paste the FULL output back here.
"""

from __future__ import annotations

import asyncio
import sys
from dataclasses import asdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import appconfig  # noqa: E402
from providers.bootstrap import create_provider  # noqa: E402
from providers.models import ProviderConfig, ProviderHealthStatus  # noqa: E402

GARBAGE_KEY = "garbage-not-a-real-key-0000000000"

# Every provider SetProviderApiKey can be asked to persist a key for.
# ollama excluded here -- it takes no key at all (local, no auth), so
# "garbage key rejected" isn't a meaningful check for it.
KEY_BASED_PROVIDERS = ("anthropic", "openai", "gemini", "openrouter")


async def check_garbage_key_rejected(name: str) -> bool:
    print(f"\n{'=' * 60}\n{name.upper()} -- garbage key must be rejected\n{'=' * 60}")
    provider = create_provider(name, ProviderConfig(provider=name, api_key=GARBAGE_KEY))
    health = await provider.health()
    print(f"health: {asdict(health)}")
    if health.status == ProviderHealthStatus.HEALTHY:
        print(f"FAIL: a garbage {name} key reported HEALTHY -- this provider's health() isn't validating.")
        return False
    print(f"PASS: garbage key rejected with status={health.status.value!r}")
    return True


async def check_good_key_still_accepted(name: str) -> bool | None:
    api_key = appconfig.resolve_api_key(name)
    if not api_key and name != "ollama":
        print(f"\n{name.upper()}: no real key configured (env var or credentials.toml) -- skipped")
        return None
    print(f"\n{'=' * 60}\n{name.upper()} -- real key must still be accepted\n{'=' * 60}")
    config = ProviderConfig.from_env(name)
    provider = create_provider(name, config)
    health = await provider.health()
    print(f"health: {asdict(health)}")
    if health.status != ProviderHealthStatus.HEALTHY:
        print(f"FAIL: a previously-working {name} key is no longer accepted by health().")
        return False
    print("PASS: still HEALTHY")
    return True


async def main() -> None:
    results: list[bool] = []

    for name in KEY_BASED_PROVIDERS:
        results.append(await check_garbage_key_rejected(name))

    for name in (*KEY_BASED_PROVIDERS, "ollama"):
        result = await check_good_key_still_accepted(name)
        if result is not None:
            results.append(result)

    print(f"\n{'=' * 60}")
    if all(results):
        print(f"ALL PASS ({len(results)} check(s) ran)")
    else:
        print(f"FAILURES PRESENT ({results.count(False)} of {len(results)} check(s) failed)")


if __name__ == "__main__":
    asyncio.run(main())
