# B14 — Usage & Limits

**Phase:** P2 · **Batchable:** no · **Depends on:** B12

## Purpose

Know what every request cost, and stop a guest spending the owner's money. Cheap
to build, highly visible in the UI, directly demonstrable.

## Build

Location: `backend\managers\usage.py` (new) +
`backend\database\usage_repository.py` (new).

### Capture
- Read `usage` from each provider response (input/output tokens). Where a
  provider omits it, fall back to the estimator and set `estimated = 1`.
  **Never present an estimate as measured.**
- One `usage_records` row per request: session, task, agent, provider, model,
  input, output, `cost_micros`, `estimated`, created_at.
- Streaming: usage usually arrives in the final chunk — record on the terminal
  event, not per token.

### Cost table — config, never hardcoded
```toml
[pricing.openai."gpt-x"]
input_per_mtok  = 0.00
output_per_mtok = 0.00
```
Unknown model → `cost_micros = NULL` and the UI shows tokens only. **Do not
invent a price.** Prices change; a hardcoded table becomes a lie.

Ship the file with the five providers' model names present and prices at `0.00`,
plus a comment telling the user where to find current rates. A zero is honest;
a guess is not.

### Aggregation queries
`usage.session(id)`, `usage.task(id)`, `usage.today(user)`, `usage.by_agent()`,
`usage.by_provider()`. Plain SQL over `usage_records`.

### Limits
```toml
[limits]
warn_at_context_pct = 80

[limits.users.guest]
daily_token_cap = 50000
```
- **Pre-flight check:** before starting a task, estimate
  `context_tokens + max_tokens`. If `remaining < estimate`, refuse with the
  numbers: `would exceed your daily cap (2.1k left, needs ~4k)`.
- **Never kill a running task** for cap reasons. What is spent is spent; block the
  *next* request.
- The owner is exempt. **Usage against a guest's own key does not count** — the
  cap protects the owner's wallet, not behaviour.
- Reset at **server-local midnight**.
- Enforced in the backend, in the same place usage is recorded, so no UI path can
  bypass it.

### Events
`usage.updated` after each request, carrying session totals and remaining cap, so
the status panel is push-driven rather than polling.

Add `cli.py usage session|today|by-agent`.

## Do NOT

- Implement payments, provider-account quotas, or billing.
- Implement prompt caching (parked — hook: `supports_caching`, added in B6).
- Hardcode prices.

## Done when

1. A completed request writes exactly one `usage_records` row with the correct
   session, task, agent, provider, and model.
2. Session totals equal the sum of that session's rows.
3. For a provider that reports usage, the recorded numbers match its own
   dashboard within a few percent — check this once, manually.
4. Rows from a provider that omits usage are flagged `estimated = 1`.
5. A guest with 2k remaining requesting a ~4k task is refused **before** the
   request is sent, with both numbers in the message.
6. A task already running when the cap is hit completes normally; the next is
   refused.
7. An unknown model yields NULL cost and no crash.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend

uv run python cli.py send "hello"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select provider, model, input_tokens, output_tokens, cost_micros, estimated from usage_records order by rowid desc limit 1;"
```
**Expect:** one row with sensible numbers. On Ollama, `estimated` should be `1`
unless Ollama reports counts, and `cost_micros` NULL or 0.

```powershell
uv run python cli.py usage session --last
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select sum(input_tokens), sum(output_tokens) from usage_records where session_id='<id>';"
```
**Expect:** the two agree exactly.

```powershell
# 5-6. cap behaviour — set limits.users.guest.daily_token_cap = 2000 first
uv run python cli.py send "<a long prompt>" --as guest
```
**Expect:** refusal naming remaining and required, **before** any request is
sent.
**Fails if:** the request goes out and is then aborted, or the refusal has no
numbers.

```powershell
# 7. unknown model
uv run python cli.py send "hi" --model "some-model-not-in-pricing"
sqlite3 "$env:LOCALAPPDATA\Point\runtime.db" "select cost_micros from usage_records order by rowid desc limit 1;"
```
**Expect:** empty/NULL, no crash.

## Handoff

State after this episode: every request's cost is recorded and attributable; caps
refuse before spending; the UI has a push event to render. Pricing lives in
config so it can be corrected without a code change.

Record the pricing source you used and the date, so it can be re-checked.
