"""Builds a ProviderRegistry from environment configuration, and
supports registering/reconfiguring providers at runtime (e.g. when
the frontend sets an API key through SetProviderApiKey).
"""

from __future__ import annotations

from providers.base import BaseProvider
from providers.mock import provider as mock_provider
from providers.models import ProviderConfig
from providers.ollama.provider import OllamaProvider
from providers.registry import ProviderRegistry


def create_provider(name: str, config: ProviderConfig) -> BaseProvider:
    """Lazily imports each provider's SDK-dependent module so a
    missing optional dependency for one provider never breaks the
    others."""
    if name == "ollama":
        return OllamaProvider(config)
    if name == "anthropic":
        from providers.anthropic.provider import AnthropicProvider
        return AnthropicProvider(config)
    if name == "openai":
        from providers.openai.provider import OpenAIProvider
        return OpenAIProvider(config)
    if name == "gemini":
        from providers.gemini.provider import GeminiProvider
        return GeminiProvider(config)
    if name == "openrouter":
        from providers.openrouter.provider import OpenRouterProvider
        return OpenRouterProvider(config)
    if name == "mock":
        from providers.mock.provider import MockProvider
        return MockProvider(config)
    if name == "demo":
        from providers.demo.provider import DemoProvider
        return DemoProvider(config)
    raise ValueError(f"Unknown provider: {name!r}")


def build_registry_from_env() -> ProviderRegistry:
    registry = ProviderRegistry()

    # Ollama always registers -- local, no key required.
    registry.register("ollama", create_provider("ollama", ProviderConfig.from_env("ollama")))

    for name in ("anthropic", "openai", "gemini", "openrouter"):
        config = ProviderConfig.from_env(name)
        if config.api_key:
            registry.register(name, create_provider(name, config))

    # POINT_MOCK=1 and nothing else. A scripted provider that can be
    # reached without opting in is a scripted provider that will
    # eventually answer a real question with a fixture.
    if mock_provider.is_enabled():
        registry.register("mock", create_provider("mock", ProviderConfig(provider="mock")))

    # Demo mode (B28): always registered, no env var -- the opposite of
    # Mock's gate above, and for the opposite reason. Mock must never be
    # reachable from the UI; Demo is meant to be (F10's provider chooser,
    # D55's visible badge), so it is selectable the same way any real
    # provider is, not behind a developer-only flag.
    registry.register("demo", create_provider("demo", ProviderConfig(provider="demo")))

    return registry