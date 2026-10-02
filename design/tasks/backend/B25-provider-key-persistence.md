# Episode B25 — Persist a Validated Provider Key

**`SetProviderApiKey` registers a provider in memory for the current process
only. It never calls the writer that already exists for this exact purpose.
A key a user enters and sees validated today is gone the next time the
backend starts. Wire the one call that is missing — nothing else.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B25-provider-key-persistence.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. One call site, calling a function that already exists,
is already correct, and is already tested in isolation (temp-file-then-
rename, ACL restriction, secret registration). The only judgment call is
ordering persistence against validation (see Build), and the verification
gate — a real process restart — is what actually has to be trusted over
"the diff looks right."

**Depends on:** B6 (`appconfig.save_api_key`, `appconfig.credentials_path`,
the `%APPDATA%\Point\credentials.toml` convention — all already built), B4
(`providers.bootstrap.create_provider`, `ProviderConfig`).

**Consumed by:** F10 (`design/tasks/frontend/F10-settings-first-run.md`)'s
credential-entry flow, Done-when #4–#6, which assume a key that validates
survives a restart. F10's own session stopped before writing any frontend
code once this gap surfaced, per CLAUDE.md's "stop and tell me" rule.

---

## Why this episode exists

Found while scoping F10 (Session 16) — stopped before any frontend code,
because F10's Build section assumes "the backend writes
`%APPDATA%\Point\credentials.toml` (temp-file-then-rename)" when the UI
sends a key, and that is not what the current wire handler does.

- `appconfig.save_api_key()` (`appconfig.py:226`) is the exact writer F10
  describes: loads existing `credentials.toml`, merges in `[provider]
  api_key = "..."`, writes to a `.tmp` sibling, `os.replace`s it over the
  real file, restricts its ACL to the current user, and registers the key
  with the redactor (`register_secret`, `appconfig.py:253`).
- **Its own docstring says nothing calls it yet:** *"Nothing calls this
  yet: SetProviderApiKey (B4) still registers a provider for the current
  process only. Persisting from the UI is a behaviour change no episode
  has asked for."* (`appconfig.py:235-238`). That is this project's own
  record of the gap, written in advance of the episode that would close it.
- `Runtime._handle_set_provider_api_key` (`runtime.py:544-557`) only does
  `create_provider(name, config)` and `self.providers.register(name,
  instance)` — an in-memory registration for the life of the current
  process. It never touches disk.
- A second, smaller gap rides along with the first: `create_provider`
  (`providers/bootstrap.py:15`) never calls `appconfig.register_secret`
  either, so a key entered this way is not even masked in logs today —
  only a key resolved from an environment variable or from
  `credentials.toml` at startup (`resolve_api_key`, `appconfig.py:195-208`,
  both of which call `register_secret`) gets that protection currently.
  Routing through `save_api_key` closes both gaps with the same call,
  because `save_api_key` already ends with `register_secret(api_key)`.

Grepped `save_api_key` across `backend\` to confirm: the only hit is its
own definition. No caller anywhere.

---

## Build

### Correction found during implementation (read before `list_models()`)

This section originally said to call `list_models()` (or `health()` —
"pick whichever") on the grounds that the two were interchangeable for
"does this key work." They are not. `AnthropicProvider.list_models()`
(`providers/anthropic/provider.py:134-152`) is a hardcoded static list of
three known model ids — it never touches the network and never looks at
`self.config.api_key`. A garbage Anthropic key would pass it every time,
which means Done-when #3 ("a key that fails validation is never written")
would have been false for Anthropic specifically, the one provider this
app is built around.

`health()` is the call that actually validates all five providers,
including Anthropic (it calls `messages.create` and catches
`anthropic.AuthenticationError`). Its contract is different from
`list_models()`'s, though: instead of raising on failure, every
provider's `health()` returns a `ProviderHealth(status, detail)`, with
`status` one of `HEALTHY` / `UNHEALTHY` / `UNAUTHENTICATED` /
`UNAVAILABLE` (`providers/models.py:32-42`). So the handler checks
`health.status` itself rather than just awaiting and letting
`ProviderError` propagate.

A per-provider special case (list_models() for four, health() only for
Anthropic) was considered and rejected — it matches each provider's own
"most truthful" validating call, but leaves a trap for the next provider
added to the registry: whichever shape it's given, nothing stops its
`list_models()` from being non-validating like Anthropic's, silently
reopening this exact gap. Calling `health()` uniformly closes the gap
structurally rather than per-instance.

### Second correction, found during live manual verification of the above

Switching to `health()` was not sufficient by itself.
`OpenRouterProvider.health()` called `self._client.models.list()` — the
exact same call `list_models()` uses — and OpenRouter's `/models`
endpoint turns out to be **public**: confirmed live, a garbage key got
back the full 465-model catalog with no auth check at all. Same bug
shape as Anthropic's `list_models()`, just surfacing through a real HTTP
200 instead of a hardcoded return, and it shipped past the first version
of `manual_b25_key_validation.py` because that version only exercised
Anthropic's garbage-key path and skipped every other provider when no
real key was configured — exactly the gap in the *test*, not just the
code, that let this through.

Fixed in `providers/openrouter/provider.py`: `health()` now calls
OpenRouter's `/auth/key` endpoint directly via `httpx` instead of
`models.list()`. `/auth/key` is OpenRouter's documented
key-introspection endpoint and does 401 on an invalid key (confirmed
live). `list_models()` itself is untouched — it still uses the public
`/models` endpoint, which is correct for its own job of listing
available models; the bug was only ever in using that same call to
answer "is this key valid."

The lesson that generalizes: **`health()` is not a validating call by
construction, for every provider, just because it has that name.** The
guarantee has to be checked per provider, against what its `health()`
actually does over the wire — hardcoded data (Anthropic's old
`list_models()`), a public endpoint (OpenRouter's old `health()`), or
local-only checks are all shapes that can look like validation and not
be it. `manual_b25_key_validation.py` was rewritten to check
garbage-key rejection unconditionally for every key-based provider
(anthropic/openai/gemini/openrouter), rather than only for Anthropic and
only when no real key made the check "skippable" — rejecting a wrong
key never needed a real key to compare against.

### Wire persistence into `_handle_set_provider_api_key`

In `runtime.py`'s `_handle_set_provider_api_key`:

1. Validate the key works **before** persisting it. Build a provider
   instance from the submitted `(name, api_key, base_url)` exactly as
   today, and call `health()`. If `health.status != HEALTHY`, raise the
   `ProviderError` subclass that best matches the status
   (`UNAUTHENTICATED` → `AuthenticationError`, `UNAVAILABLE` →
   `ProviderUnavailableError`, `UNHEALTHY` → `ProviderError` itself, there
   being no closer-fitting subclass) carrying `health.detail` as the
   message, so a bad key still surfaces the same kind of error a provider
   that raises on its own would have produced. This episode adds no new
   *error path* for the client — the client still only ever sees `Error`
   with a `ProviderError`-shaped message — it adds one `if` that turns a
   status value into that same shape.
2. Only once that succeeds, call `appconfig.save_api_key(name, api_key)`.
3. Register the now-validated instance with `self.providers` as today.
4. Emit `ProviderApiKeySet` as today — same payload, same shape. This
   episode does not change the wire contract, only what happens on the
   backend between receiving the event and emitting the reply.

**Order matters and is the one real decision in this episode:** validate,
*then* persist. Writing an unvalidated key to `credentials.toml` first and
rolling it back on failure would mean a brief window where a bad key is on
disk, and rollback-on-failure is exactly the kind of "undo a write" logic
`save_api_key`'s atomic temp-file-then-rename was built to make
unnecessary elsewhere in this codebase. Validate-then-write needs no
rollback path at all.

### Nothing else moves

- `resolve_api_key`'s precedence (env var → `credentials.toml` → not
  found) is untouched. A key saved this way becomes resolvable through the
  **same** `credentials.toml` path every other consumer already reads
  through — there is no second resolution path to keep in sync.
- `doctor`'s `key_source()` (`appconfig.py:211`) already reports
  `"credentials.toml"` once a key lands there; no change needed for it to
  start being accurate about a key saved via the UI.

---

## Do NOT

- Change `SetProviderApiKey`'s request or `ProviderApiKeySet`'s response
  payload shape. Frontend code against the existing shape must keep
  working unmodified.
- Add a new wire event for this. The existing `SetProviderApiKey` is the
  correct, already-specified place for this behavior — F10's own text
  describes it as a single step, not two.
- Touch `config.toml` in any way. This is `credentials.toml` only.
- Build any masking/redaction machinery. `register_secret`/`mask_secret`/
  `redact` already exist and already work; `save_api_key` already calls
  `register_secret`. Confirm it fires, do not re-implement it.
- Add a rollback/delete path for a key that failed validation. Validating
  before writing makes one unnecessary — do not build one anyway "to be
  safe."
- Touch the Settings UI, the provider chooser, or any other part of F10.
  This episode is the backend persistence gap only.

---

## Done when

1. A key submitted via `SetProviderApiKey` that passes validation is on
   disk in `credentials.toml`, written atomically (temp file, then
   rename), immediately — observable without restarting anything.
2. **The backend process is restarted** (not just re-asked) and the same
   provider resolves the persisted key with no `SetProviderApiKey` call in
   the new process — `resolve_api_key(provider)` returns it from
   `credentials.toml` alone.
3. A key that fails validation (`ProviderError`) is never written to
   `credentials.toml` — the file is untouched, byte-for-byte, compared
   before and after the failed attempt.
4. The key is masked everywhere `CLAUDE.md`'s hard rule requires: a log
   line, an error message, and `doctor`'s output that happen to mention it
   show only the last 4 characters or `***`, never the raw value — proven
   by actually grepping the full key string against stderr/log output
   after the call, not by reading the code and assuming `register_secret`
   fired.
5. `SetProviderApiKey`'s and `ProviderApiKeySet`'s payload shapes are
   byte-for-byte unchanged from before this episode.
6. Existing callers of `resolve_api_key` / `key_source` (`doctor`, every
   provider's own startup resolution) are unaffected for a key that came
   from an env var or was already in `credentials.toml` before this
   episode — this episode only adds a new way a key can arrive in that
   file, it does not change how any key already there is read.

---

## Verification gate (PowerShell, scratch profile)

One combined script. `APPDATA` and `LOCALAPPDATA` point at a scratch
directory for the script's whole duration; nothing under the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` is ever named. See CLAUDE.md's
hard rule for why a rename-aside pattern must never be reconstructed here.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b25"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # 1. a deliberately wrong key must not touch credentials.toml at all
    $credsPath = Join-Path $scratch "Point\credentials.toml"
    uv run python main.py --script <send-set-provider-api-key-wrong-key-script>
    # EXPECT: ProviderApiKeySet is NOT emitted; an error is; $credsPath does
    # not exist (or, if it already existed from an earlier step, is
    # byte-identical to its state before this call).

    # 2. a real key persists immediately
    uv run python main.py --script <send-set-provider-api-key-real-key-script>
    # EXPECT: ProviderApiKeySet emitted; $credsPath now contains
    # [<provider>] with an api_key line.
    Get-Content $credsPath
    # EXPECT: the key is readable here (this file is meant to hold it) --
    # this step is confirming the WRITE happened, not testing masking.

    # 3. masking -- the raw key must never appear in logs/stderr
    $logPath = Join-Path $scratch "Point\logs\point.log"
    Select-String -Path $logPath -Pattern "<the-real-key-value>" -SimpleMatch
    # EXPECT: no matches. The key may appear masked (e.g. ending in the
    # real last 4 characters preceded by ***), never in full.

    # 4. THE CHECK THAT MATTERS: restart and resolve with no SetProviderApiKey call
    #    (new process, same scratch profile, nothing re-sent)
    uv run python cli.py doctor
    # EXPECT: doctor reports this provider's key_source as
    # "credentials.toml", proving a fresh process resolved it from disk
    # alone -- not from any in-memory state the first process held.
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Fails if:** step 1 finds anything written for a key that never
validated; step 3 finds the raw key anywhere in the log; step 4 shows
`key_source` as anything other than `"credentials.toml"` (meaning the
fresh process could not actually resolve the persisted key).

---

## Handoff

- This episode exists because F10 (Session 16) stopped before writing
  frontend code against an assumption the backend did not actually
  implement — the same pattern that produced B20, B21, and B23 out of
  earlier frontend sessions.
- **This is deliberately not bundled into F10 itself.** F10 is a frontend
  episode; CLAUDE.md's "build only what the episode's Build section lists"
  rule applies to backend work surfacing mid-session exactly as it does to
  scope creep within one side. A correctness fix to a named wire handler,
  with its own restart-across-process verification gate, earns its own
  session rather than being absorbed into a larger one — the same
  reasoning S13e's handoff gives for not folding a small, independently
  verifiable fix into whichever episode happened to find it.
- Once this is built and verified, F10 resumes in a fresh session, scoped
  to: the provider chooser (first run), credential entry and validation,
  the no-provider state, and the Providers + Appearance tabs of Settings.
  The `point doctor`-in-About action and the Behaviour tab's real,
  live-override effect, and Demo mode, stay deferred — each needs backend
  surface that does not exist yet (no wire event for `doctor`; no runtime
  override layer for `config.toml`-sourced scheduler/guard settings; no
  Demo provider type or fixture library). See
  `design/tasks/frontend/INDEX.md`'s F10 split note for the full account.
