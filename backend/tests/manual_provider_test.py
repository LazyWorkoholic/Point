"""tests/manual_provider_test.py

Manual, human-run integration test across every provider you have
credentials for. Run it, paste the FULL output back here.

What "good" output looks like per provider:
  - health: {'status': 'healthy'}  (any provider you haven't set a
    key for simply won't appear at all -- that's expected, not a bug)
  - list_models: a non-empty list of model ids
  - generate: a short, coherent reply + usage with nonzero
    input_tokens/output_tokens (Ollama's usage may be less precise)
  - stream: a similarly coherent reply, rebuilt from multiple chunks,
    with "chunks received: N" where N > 1 -- that N > 1 is what
    proves it actually streamed rather than returning once.

If something errors, you'll see the specific error type
(AuthenticationError, ModelNotFoundError, RateLimitError, etc.) --
paste that too. It tells us exactly what's wrong: a bad key vs a bad
model name vs the provider being unreachable are all different fixes.
"""

from __future__ import annotations

import asyncio
from dataclasses import asdict

from managers.message import UserMessage
from providers.bootstrap import build_registry_from_env
from providers.errors import ProviderError
from providers.models import GenerationRequest

# If a provider errors with ModelNotFoundError below, replace its
# entry here with an id from that provider's own list_models() output
# (printed just above the error).
TEST_MODELS = {
    "ollama": None,  # auto-picked from list_models() below
    # "anthropic": "claude-haiku-4-5-20251001",
    # "openai": "gpt-4o-mini",             # adjust if this errors
    "gemini": "gemini-2.0-flash",        # adjust if this errors
    "openrouter": "openai/gpt-oss-20b",  # adjust if this errors
}

TEST_PROMPT = "Reply with exactly five words describing the color blue."


async def test_provider(name: str, provider) -> None:
    print(f"\n{'=' * 60}\n{name.upper()}\n{'=' * 60}")

    health = await provider.health()
    print(f"health: {asdict(health)}")

    models = []
    try:
        models = await provider.list_models()
        print(f"list_models: {len(models)} total, sample: {[m.id for m in models][:8]}")
    except ProviderError as exc:
        print(f"list_models FAILED: {type(exc).__name__}: {exc}")

    model_id = TEST_MODELS.get(name) or (models[0].id if models else None)
    if model_id is None:
        print("generate/stream SKIPPED: no model id available")
        return
    print(f"using model: {model_id}")

    try:
        response = await provider.generate(GenerationRequest(
            model=model_id,
            messages=[UserMessage("__pending__", TEST_PROMPT)],
        ))
        print(f"generate: {response.text!r}")
        print(f"generate usage: {asdict(response.usage)}")
    except ProviderError as exc:
        print(f"generate FAILED: {type(exc).__name__}: {exc}")

    try:
        chunks = []
        async for chunk in provider.stream(GenerationRequest(
            model=model_id,
            messages=[UserMessage("__pending__", TEST_PROMPT)],
        )):
            if chunk.type.value == "text":
                chunks.append(chunk.text)
        print(f"stream: {''.join(chunks)!r}")
        print(f"stream chunks received: {len(chunks)}")
    except ProviderError as exc:
        print(f"stream FAILED: {type(exc).__name__}: {exc}")


async def main() -> None:
    registry = build_registry_from_env()
    print(f"Registered providers: {registry.list()}")
    for name in registry.list():
        await test_provider(name, registry.get(name))


if __name__ == "__main__":
    asyncio.run(main())