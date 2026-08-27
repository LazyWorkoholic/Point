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
    """B10 gave this one an attribute the others do not need:
    `retry_after`, in seconds, when the vendor sent a Retry-After header.
    It rides as an attribute rather than inside the message because
    `providers.retry` has to do arithmetic with it, and re-parsing a
    number back out of a sentence is how a backoff ends up coupled to an
    error string's wording."""

    def __init__(self, *args, retry_after: float | None = None) -> None:
        super().__init__(*args)
        self.retry_after = retry_after


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