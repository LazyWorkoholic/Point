"""ProviderRegistry: a name -> BaseProvider lookup, so nothing in the
application ever writes `if provider == "openai": ...`."""

from __future__ import annotations

from providers.base import BaseProvider
from providers.errors import ProviderNotRegisteredError


class ProviderRegistry:
    def __init__(self) -> None:
        self._providers: dict[str, BaseProvider] = {}

    def register(self, name: str, provider: BaseProvider) -> None:
        self._providers[name] = provider

    def get(self, name: str) -> BaseProvider:
        provider = self._providers.get(name)
        if provider is None:
            raise ProviderNotRegisteredError(f"No provider registered as {name!r}")
        return provider

    def list(self) -> list[str]:
        return list(self._providers.keys())

    def remove(self, name: str) -> None:
        self._providers.pop(name, None)