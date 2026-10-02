# Episode B25 — Persist a Validated Provider Key ✅

**`SetProviderApiKey` registers a provider in memory for the current process
only and never called the writer (`appconfig.save_api_key`) that already
existed for this exact purpose — a key a user entered and saw validated was
gone the next time the backend started. `_handle_set_provider_api_key` now
validates the submitted key with `provider.health()` before persisting it,
and only calls `save_api_key()` once that validation actually passes.
Getting "actually passes" right took two corrections mid-episode, both found
before Aryan's confirmed-clean gate run: `list_models()` doesn't validate
Anthropic's key at all, and OpenRouter's `health()` didn't either, for a
different reason. Both are detailed below — they're the most load-bearing
part of this episode, more than the one-line wiring itself.**

> Confirmed by Aryan: full gate run, single paste, all four checkpoints
> matching — wrong key returns `Error` with no file written, good key
> persists and is visible in `credentials.toml`, the key is absent from
> logs, and a fresh `cli.py doctor` invocation (no `SetProviderApiKey` call
> in that process) correctly resolves it from disk with proper masking. Run
> against a scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard
> rule — the real profile was never a target of anything in this episode.

---

## `backend\runtime\runtime.py`

`_handle_set_provider_api_key` (the one call site this episode touches):
builds the provider instance exactly as before from the submitted
`(name, api_key, base_url)`, then `await`s `instance.health()` and checks
`.status`. Anything other than `HEALTHY` raises the `ProviderError`
subclass that best matches the status (`UNAUTHENTICATED` →
`AuthenticationError`, `UNAVAILABLE` → `ProviderUnavailableError`,
`UNHEALTHY` → `ProviderError` itself — there is no closer-fitting
subclass), carrying `health.detail` as the message. Only once `health()`
reports `HEALTHY` does it call `appconfig.save_api_key(name, api_key)`,
then register the instance with `self.providers`, then emit
`ProviderApiKeySet` — same payload, same shape as before this episode. The
new `_HEALTH_FAILURE_ERRORS` module-level mapping (status → exception
class) lives just above the handler. `SetProviderApiKey`'s request shape
and `ProviderApiKeySet`'s reply shape are both byte-for-byte unchanged.

## `backend\appconfig.py`

No behavior change. `save_api_key`'s docstring, which said "nothing calls
this yet" (accurate until this episode), now names its caller.

## `backend\providers\openrouter\provider.py`

`health()` no longer calls `self._client.models.list()` — OpenRouter's
`/models` endpoint is **public** and returns the full catalog for any key,
garbage or real, with zero auth check (confirmed live: a garbage key got
back all 465 models, HTTP 200). `health()` now makes a direct `httpx` call
to OpenRouter's `/auth/key` endpoint instead — their documented
key-introspection endpoint, confirmed live to return 401 for an invalid
key — and maps the response to `HEALTHY` / `UNAUTHENTICATED` /
`UNHEALTHY`. `list_models()` is untouched; the public `/models` endpoint is
correct for *its* job of listing available models. `__init__` now stores
`self._base_url` (previously only passed straight into the OpenAI SDK
client) so `health()`'s raw `httpx` call can reuse it.

## `backend\tests\manual_b25_key_validation.py` (NEW)

Manual, human-run regression script. Checks two independent things per
key-based provider (anthropic/openai/gemini/openrouter), mirroring exactly
what the handler now does — construct a provider, `await health()`, inspect
`.status`:

1. **Unconditionally**, for all four: a garbage key must come back
   `status != HEALTHY`. Needs no real key and is never skipped — this is
   the check that would have caught both bugs below, and the first version
   of this file only ran it for Anthropic.
2. For whichever of the five providers (the four above, plus Ollama) you
   have a real key configured for (env var or `credentials.toml`, via
   `appconfig.resolve_api_key()`): that key must still come back `HEALTHY`,
   proving `health()` doesn't newly reject something that used to work.
   Skipped and reported as such for a provider with no real key configured
   — that's expected, not a failure.

---

## Manual Test Log (Episode B25)

Confirmed by Aryan, scratch `APPDATA`/`LOCALAPPDATA` profile:

1. `SetProviderApiKey` with a deliberately wrong key returns `{"type":
   "Error", ...}` over the wire (not `ProviderApiKeySet`), and
   `credentials.toml` is not created.
2. `SetProviderApiKey` with a real, valid key returns `ProviderApiKeySet`,
   and `credentials.toml` immediately contains `[<provider>]` with an
   `api_key` line — confirmed readable in the file right after the call.
3. The raw key does not appear anywhere in `logs\point.log` — only masked.
4. **The check that matters:** the backend process is restarted (`cli.py
   doctor`, a fresh process, no `SetProviderApiKey` call in it) and
   `key_source` reports `"credentials.toml"` — proving the fresh process
   resolved the persisted key from disk alone, with proper masking in
   `doctor`'s own output.

All four re-run and reconfirmed specifically against **OpenRouter** after
the bug below was fixed (see "Bugs caught," second entry) — the provider
the live bug actually reproduced on.

Also run directly in this session, against the live OpenRouter API (not
mocked, no scratch profile needed — read-only):

- `OpenRouterProvider.health()` with a garbage key now returns
  `status=UNAUTHENTICATED` (previously `HEALTHY`).
- `manual_b25_key_validation.py`, full run: all four key-based providers'
  garbage-key rejection — `PASS`; Ollama's real local key — `PASS`
  (`HEALTHY`); the other three real-key checks skipped, no real cloud keys
  configured in that environment.
- The actual wire trace for the fixed OpenRouter wrong-key case, captured
  directly:
  ```
  {"type": "Error", "corr_id": "test-wrong-key",
   "payload": {"error": "openrouter key failed validation (unauthenticated)",
   "code": null, "offer": null}}
  ```
  `credentials.toml` confirmed absent afterward.

---

## Bugs caught during manual testing

Two, both in the "validation call doesn't actually validate" shape — found
in two different ways, which matters for what this episode leaves behind
(see below).

### 1. `AnthropicProvider.list_models()` — found before writing the handler

The episode's own Build section originally said to validate with
`list_models()` ("or `health()` — pick whichever"), on the premise that the
two were interchangeable for "does this key work." They are not.
`AnthropicProvider.list_models()` (`providers\anthropic\provider.py`) is a
**hardcoded static list** of three known model ids — it never touches the
network and never looks at `self.config.api_key`. A garbage Anthropic key
would have passed it every time, meaning a bad key would have been
persisted for the one provider this app is built around. Caught by reading
the provider source before wiring the handler, not by running anything —
flagged to Aryan, who chose `health()` uniformly over a per-provider
special case (the special case was rejected specifically because it leaves
a trap for the next provider added: nothing stops its own `list_models()`
from being just as non-validating, silently reopening the same gap).

### 2. `OpenRouterProvider.health()` — found live, during the verification gate itself

Switching to `health()` was not sufficient by itself.
`OpenRouterProvider.health()` called `self._client.models.list()` — the
exact same call `list_models()` uses — and OpenRouter's `/models` endpoint
is **public**: a garbage key got back the full 465-model catalog, HTTP 200,
no auth check at all. This is the same bug shape as bug #1, just surfacing
through a real HTTP success instead of a hardcoded return, and it shipped
past the *first* version of `manual_b25_key_validation.py` because that
version only ever exercised Anthropic's garbage-key path and skipped every
other provider when no real key was configured for it — a gap in the test,
not just the code, that let this through until Aryan's own manual gate run
reproduced it live with a real OpenRouter key.

Fixed by switching `health()` to OpenRouter's `/auth/key` endpoint, which
actually 401s on an invalid key (confirmed live). `manual_b25_key_validation.py`
was then rewritten so garbage-key rejection runs unconditionally for every
key-based provider, never gated on a real key being available — rejecting
a wrong key never needed a right one to compare against.

### The generalizable lesson

**A provider's `health()` is not a validating call by construction, for
every provider, just because of its name.** Each one has to be checked
individually against what it actually does over the wire — a hardcoded
static list (Anthropic's `list_models()`), a public endpoint that happens
to return 200 for anything (OpenRouter's old `health()`), or some other
local-only check are all shapes that look like validation from the call
site and are not. The fix for "does this key work" always has to bottom out
in evidence that a *wrong* key was actually rejected by this specific
provider's specific implementation, not an assumption that a method named
`health` or `list_models` necessarily checks the key at all.

---

## What the next episode inherits

- `Runtime._handle_set_provider_api_key` validates with `health()`,
  uniformly, for every provider. Any **new** provider added to the registry
  must have its `health()` checked the same way these five were — run (or
  extend) `manual_b25_key_validation.py` against it before trusting that a
  bad key gets rejected. Do not assume it from the method's name.
- `manual_b25_key_validation.py` is a real regression test, kept in the
  repo, read-only against `appconfig` (never calls `save_api_key`, safe to
  run against a real profile). It checks garbage-key rejection
  unconditionally for anthropic/openai/gemini/openrouter, and good-key
  acceptance for whichever of those four plus ollama you have a real key
  configured for.
- F10 (`design/tasks/frontend/F10-settings-first-run.md`) resumes in a
  fresh session, scoped to: the provider chooser (first run), credential
  entry and validation, the no-provider state, and the Providers +
  Appearance tabs of Settings. It can now build its credential-entry flow
  against a backend that actually persists a validated key — the gap that
  stopped F10's own session before any frontend code was written no longer
  exists.
- `OpenRouterProvider.__init__` still raises if constructed with
  `api_key=None` (an `openai.AsyncOpenAI` client requirement, unrelated to
  this episode) — not reachable through `SetProviderApiKey`, which already
  refuses a missing `api_key` before constructing anything, so left as-is.
