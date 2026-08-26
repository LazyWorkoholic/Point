"""Normalized provider errors.

Every provider (Ollama/OpenAI/Anthropic/Gemini/OpenRouter) fails in
its own vendor-specific way. Nothing outside providers/ should ever
need to know that -- each concrete provider catches its own SDK's/
HTTP client's native exceptions and re-raises one of these instead.
"""

from __future__ import annotations

from runtime.errors import RuntimeException


class ProviderError(RuntimeException):
    """Base class for all normalized provider errors."""


class ProviderNotRegisteredError(ProviderError):
    pass


class AuthenticationError(ProviderError):
    pass


class RateLimitError(ProviderError):
    pass


class ModelNotFoundError(ProviderError):
    pass


class ProviderConnectionError(ProviderError):
    pass


class ProviderTimeoutError(ProviderError):
    pass


class InvalidRequestError(ProviderError):
    pass


class ProviderUnavailableError(ProviderError):
    pass