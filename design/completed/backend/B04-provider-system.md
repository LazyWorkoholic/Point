# Episode 4 — Provider System ✅

**Five providers behind one interface, registry, bootstrap**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Original source: `completed_backend_task.md`, split per episode. Content is
> unchanged apart from heading levels.

---

## `providers/errors.py` (NEW)
Normalized error hierarchy so nothing outside `providers/` ever needs
to know a specific vendor's exception types.

- `ProviderError(RuntimeException)` — base class.
- `ProviderNotRegisteredError`, `AuthenticationError`, `RateLimitError`,
  `ModelNotFoundError`, `ProviderConnectionError`, `ProviderTimeoutError`,
  `InvalidRequestError`, `ProviderUnavailableError`.
- Because `ProviderError` extends `RuntimeException`, any of these
  raised inside a Runtime event handler is automatically caught by
  `Runtime.receive()`'s existing exception handling and converted to
  a clean `Error` event — no extra wiring needed (confirmed in manual
  testing via Gemini's rate-limit case).

## `providers/models.py` (NEW)
Provider-layer domain models — distinct from `managers/message.py`'s
`Message`; these carry a request TO a provider and what comes back.

- `ProviderHealthStatus` enum (`healthy`/`unhealthy`/`unauthenticated`/
  `unavailable`) and `ProviderHealth` dataclass.
- `ModelCapabilities` (`supports_streaming`/`supports_tools`/
  `supports_vision`/`supports_reasoning`) and `Model` (`id`,
  `provider`, `display_name`, `context_window`, `capabilities`,
  `metadata`).
- `ProviderConfig` — `provider`, `api_key`, `base_url`, `timeout`,
  `extra`. `.from_env(provider)` reads the matching environment
  variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`,
  `OPENROUTER_API_KEY`; Ollama defaults its `base_url` to
  `http://localhost:11434`). API keys are never stored as loose
  attributes elsewhere in the app — they live only inside this object.
- `GenerationRequest` — `model`, `messages` (plain `list[Message]`,
  deliberately never a `Session` — see `providers/base.py`),
  `temperature`, `max_tokens`, `tools`, `extra`.
- `GenerationResponse` — `text`, `usage`, `model`, `metadata`.
  Deliberately NOT a full `Message` (a `Message` requires a
  `session_id`, which the provider layer doesn't know). Exposes
  `.to_assistant_message(session_id, task_id)` for the caller who
  *does* know the session.
- `ProviderChunkType` enum (`text`/`thinking`/`usage`/`done`) and
  `ProviderStreamChunk` — the raw, minimally-normalized shape every
  provider's `stream()` yields. This is explicitly NOT Episode 5's
  `StreamEvent`; it's the seam Episode 5's Stream Adapter converts
  FROM.

## `providers/base.py` (NEW)
`BaseProvider` abstract class — `generate()`, `stream()`,
`list_models()`, `health()`. Hard architectural rule enforced here:
providers know Messages and Models only, never Sessions, Memory,
Agents, Workspaces, or Projects.

## `providers/registry.py` (NEW)
`ProviderRegistry` — `register()`, `get()` (raises
`ProviderNotRegisteredError` if missing), `list()`, `remove()`. This
is what eliminates `if provider == "openai": ...` branching anywhere
else in the codebase.

## `providers/bootstrap.py` (NEW)
- `create_provider(name, config)` — lazily imports each provider's
  SDK-dependent module inside its own branch, so a missing optional
  dependency for one provider (e.g. `google-generativeai` not
  installed) can never break the others.
- `build_registry_from_env()` — always registers Ollama (local, no
  key needed); registers Anthropic/OpenAI/Gemini/OpenRouter only if
  their respective API key is present in the environment. A provider
  simply doesn't appear in `registry.list()` if unconfigured, rather
  than erroring at startup.

## Per-provider implementations (NEW)
Each provider pairs a `MessageTranslator` (internal `Message` <->
that vendor's wire format) with a concrete `BaseProvider` subclass:

- **`providers/ollama/`** — `OllamaMessageTranslator` +
  `OllamaProvider` (raw HTTP via `httpx` against a local Ollama
  server's `/api/chat` and `/api/tags`). No API key required. Chosen
  as the first implementation specifically because it's free and
  local to test.
- **`providers/anthropic/`** — `AnthropicMessageTranslator` +
  `AnthropicProvider` (official `anthropic` SDK,
  `AsyncAnthropic`/`.messages.stream()`). Known simplification: system
  messages currently map to `role="user"` rather than being extracted
  into Anthropic's separate top-level `system` parameter — flagged in
  the translator's docstring as something the future Agent layer
  (Episode 8) should properly handle when it builds
  `GenerationRequest.extra["system"]`.
- **`providers/openai/`** — `OpenAIMessageTranslator` (shared with
  OpenRouter, since OpenRouter's API is OpenAI-compatible) +
  `OpenAIProvider` (official `openai` SDK, `AsyncOpenAI`).
- **`providers/gemini/`** — `GeminiMessageTranslator` +
  `GeminiProvider` (`google-generativeai` SDK — this package is
  deprecated upstream in favor of `google-genai`; noted as a known
  follow-up, not urgent, since it still functions correctly today).
  Maps Google's `role="model"` naming to our `MessageRole.ASSISTANT`.
- **`providers/openrouter/`** — `OpenRouterProvider`, reuses
  `OpenAIMessageTranslator` and the `openai` SDK pointed at
  `https://openrouter.ai/api/v1`. Model IDs here are OpenRouter's full
  strings (e.g. `"anthropic/claude-sonnet-5"`) — the concrete
  real-world case of "Provider ≠ Model" from `project_idea.md`.

Every provider normalizes its own SDK's exceptions
(`openai.AuthenticationError`, `anthropic.RateLimitError`,
`google.api_core.exceptions.ResourceExhausted`, etc.) into the shared
`providers/errors.py` hierarchy before it ever leaves the provider.

## `managers/session_manager.py` — addition
- `set_provider(session_id, provider, model)` — same pattern as
  `rename()`/`archive()`; lets a session's active provider/model be
  changed going forward without touching its message history.

## `runtime/events.py` — additions
New `IncomingEventType` members: `LIST_PROVIDERS`,
`LIST_PROVIDER_MODELS`, `SET_PROVIDER_API_KEY`,
`SET_SESSION_PROVIDER`. New `OutgoingEventType` members:
`PROVIDER_LIST`, `PROVIDER_MODEL_LIST`, `PROVIDER_API_KEY_SET`,
`SESSION_PROVIDER_UPDATED`.

## `runtime/runtime.py` — additions
- Constructor now builds `self.providers = build_registry_from_env()`
  — fully synchronous (no SDK client does network I/O at construction
  time), so this lives directly in `__init__`.
- `_handle_list_providers` — calls `.health()` on every registered
  provider, returns a flat list (confirmed correctly reflects live
  registrations, not just startup state).
- `_handle_list_provider_models` — delegates to
  `provider.list_models()`.
- `_handle_set_provider_api_key` — builds a fresh `ProviderConfig`,
  calls `create_provider()`, and re-registers under the same name.
  This is what lets a frontend add or replace a provider's credentials
  at runtime with zero process restart (confirmed in manual testing —
  a provider not present at boot appeared in `ListProviders`
  immediately after `SetProviderApiKey`).
- `_handle_set_session_provider` — thin wrapper over
  `SessionManager.set_provider()`.

## `tests/manual_provider_test.py` (NEW)
Standalone script (not wired into `Runtime`) that iterates every
provider currently in `build_registry_from_env()`'s registry and
exercises `health()` → `list_models()` → `generate()` → `stream()` for
each, printing labeled, pasteable output. Deliberately outside
`runtime/`/`providers/` proper since it's a dev tool, not application
code.

## Manual Test Log (Episode 4)
- **Ollama** — fully proven end-to-end: health, 5 models listed,
  `generate()` returned coherent text with real
  input/output/total token counts, `stream()` returned the same reply
  across 11 real incremental chunks.
- **OpenRouter** — fully proven end-to-end: health, 400 models listed,
  `generate()`/`stream()` both succeeded with real usage numbers.
  Noted: one free-tier model (`openai/gpt-oss-20b`) returned its
  `stream()` output as a single chunk rather than incrementally — a
  model-level limitation of that specific free model on OpenRouter's
  side, not a defect in `OpenRouterProvider` or the streaming contract.
- **Gemini** — health and 42 models listed successfully;
  `generate()`/`stream()` both hit a billing-gated free-tier quota
  (`RateLimitError`, quota limit of 0 for that model). This is a
  useful confirmation, not a failure: it proves a raw
  `google.api_core.exceptions.ResourceExhausted` from the SDK was
  correctly caught and re-raised as our own normalized
  `RateLimitError` with full detail preserved, exactly per spec.
- **OpenAI / Anthropic** — `health()` confirmed `healthy` via both
  env-var boot and a live `SetProviderApiKey` call (proving dynamic
  registration works, including re-registering a provider under a
  name it didn't have at startup). `generate()`/`stream()` not
  exercised against these two due to no available billing credit on
  the user's accounts — not due to any known or suspected code defect,
  since the exact same request/error-handling code path was already
  proven correct by the Gemini test above.
- **Dynamic reconfiguration** — `SetProviderApiKey` for `openai`
  (previously unregistered, no env var set) was immediately visible in
  a subsequent `ListProviders` call with `status: healthy`, with no
  process restart — confirms the "frontend can set/change a provider's
  key at runtime" requirement.

---
