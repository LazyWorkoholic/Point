# Episode 5 — Streaming Engine ✅

**Normalized stream events, accumulator, partial persistence**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Original source: `completed_backend_task.md`, split per episode. Content is
> unchanged apart from heading levels.

---

## `streaming/events.py` (NEW)
The unified event vocabulary this episode introduces -- MESSAGE ≠
EVENT. A `Message` (Episode 3) is conversation data; a `StreamEvent`
is something happening DURING execution.

- `StreamEventType` enum -- `TEXT_DELTA`, `USAGE_UPDATED` are the only
  types actually emitted right now. `THINKING_DELTA`,
  `STATUS_CHANGED`, `PROGRESS_UPDATED`, `TOOL_STARTED`,
  `TOOL_COMPLETED` are reserved in the enum so later episodes (Agent
  system, Tool system) don't require a schema change to start using
  them.
- `StreamEvent` dataclass -- `event_id`, `task_id`, `session_id`,
  `message_id`, `sequence`, `timestamp`, `type`, `data`. `.to_dict()`
  for serialization, matching the same pattern as `OutgoingEvent` from
  Episode 1.

## `streaming/adapter.py` (NEW)
`StreamAdapter.adapt()` -- converts a Episode 4 `ProviderStreamChunk`
into a `StreamEvent`. **Deliberate deviation from the original spec**:
one shared adapter instead of one per provider
(`OpenAIStreamAdapter`, `AnthropicStreamAdapter`, etc.). Justification:
Episode 4 already normalized every provider's raw wire format into the
shared `ProviderStreamChunk` shape, so there's no provider-specific
translation left to do at this layer -- a second adapter layer would
just duplicate work Episode 4 already did. Returns `None` for
`ProviderChunkType.DONE` (a signal to stop iterating, not an event of
its own -- `Completed`/`Failed`/`Cancelled` semantics are handled by
the caller based on how the stream actually ended, not by a chunk
type).

## `streaming/accumulator.py` (NEW)
`StreamAccumulator` -- builds a `Message` from a stream of events
without ever mutating a `Message` object in place (per spec item 4).

- `apply(event)` -- appends text on `TEXT_DELTA`, replaces `_usage` on
  `USAGE_UPDATED`.
- `finalize() -> Optional[Message]` -- normal completion path. Returns
  `None` if zero text was accumulated.
- `finalize_interrupted(reason, error=None) -> Optional[Message]` --
  failure/cancellation path. Preserves whatever partial text arrived
  (per spec item 19 -- don't silently discard partial output) and
  tags the resulting `Message.metadata` with `{"interrupted": True,
  "reason": ..., "error": ...}`. Also returns `None` if zero text
  arrived (e.g. cancelled before any token came through).
- **Bug found and fixed during manual testing:** `finalize()` originally
  had no empty-text guard, while `finalize_interrupted()` did -- an
  inconsistency that meant a provider completing with genuinely zero
  text (content filtering, a model quirk) would hit
  `MessageValidationError` inside the `"done"` branch of the sink,
  which then got caught by `Task._run()`'s generic exception handler
  and surfaced to the client as a spurious `TaskFailed`, even though
  the task had actually *completed*, just with an empty body. Fixed by
  giving `finalize()` the same `if not self.text: return None` guard,
  and adding the matching `if msg is not None:` check in
  `_make_stream_sink`'s `"done"` branch before calling
  `sessions.append_message()`.

## `streaming/manager.py` (NEW)
`StreamManager` -- per-task bookkeeping only. Deliberately has **no
consumer loop of its own**: `Task`'s existing async-generator loop
(Episode 1) already is the consumer of a stream's events, so a second
queue/loop here would just duplicate that responsibility.

- `register_stream(task_id, session_id, message_id)` -- creates a
  fresh `StreamAccumulator`, resets the sequence counter to 0.
- `next_sequence(task_id)` -- increments and returns, giving each task
  its own independent 1, 2, 3... counter (confirmed in concurrency
  testing: two simultaneous tasks each started their own sequence at 1
  with no cross-task collision).
- `has_stream(task_id)`, `get_accumulator(task_id)`, `close_stream
  (task_id)`, `active_task_ids()`.

## `managers/message.py` -- addition
`Message.new()` gained an optional `message_id` parameter. This is
what lets a reply's identity be fixed *before* streaming starts (spec
item 8) -- `Runtime` generates a `message_id` up front, threads it
through every `TextDelta`/`UsageUpdated` event, and the final
`AssistantMessage` built by the accumulator uses that same id.
Confirmed end-to-end in manual testing: every streamed event's
`message_id` exactly matched the persisted `AssistantMessage.id`.

## `runtime/task_manager.py` -- addition
`create_task()` gained an optional `task_id` parameter (the
underlying `Task` class already supported a pre-assigned id since
Episode 1 -- this just exposes it). Needed because the streaming task
body has to know its own `task_id` in order to register itself with
`StreamManager` before the provider call even starts.

## `runtime/events.py` -- additions
New `OutgoingEventType` members: `TEXT_DELTA`, `THINKING_DELTA`,
`STATUS_CHANGED`, `PROGRESS_UPDATED`, `USAGE_UPDATED`. `TASK_TOKEN`
(from Episode 1) is kept defined for backward compatibility but is now
only emitted by the `provider="echo"` dev fallback path, not by real
provider streaming.

## `runtime/runtime.py` -- major rewrite of the send-message path

- **`_PROVIDER_ALIASES`** -- a small class-level map (`{"claude":
  "anthropic", "gpt": "openai", "chatgpt": "openai"}`) plus
  `_resolve_provider_name()`. Fixes a real gap found while wiring this
  episode in: existing test sessions and casual naming used
  `provider="claude"`, but the `ProviderRegistry` key from Episode 4
  is `"anthropic"` -- without this alias, every such session would
  hit `ProviderNotRegisteredError` the moment real streaming replaced
  the placeholder.
- **`_handle_send_message`** -- now loads the session's message
  history (`load_messages=True`), generates a `task_id` and
  `message_id` up front, and branches on `session.provider == "echo"`
  to decide between the real streaming body and the Episode 1
  placeholder (kept intentionally as a no-network dev/test
  convenience, not a real registered provider).
- **`_stream_chat_body`** (NEW) -- the real streaming task body.
  Registers with `StreamManager`, resolves the provider via the alias
  map, builds a `GenerationRequest` from the session's full message
  history, and iterates `provider.stream()`, converting each
  `ProviderStreamChunk` through `StreamAdapter` and applying it to the
  accumulator before yielding it onward to the sink. Docstring
  explicitly documents the cancellation mechanism: `CancelledError`
  raised at whichever `await` this generator is suspended on (almost
  always inside the provider's own `async for`), which is what allows
  each provider's `async with` client wrapper to actually close the
  underlying connection during teardown -- confirmed working in manual
  cancellation testing (a handful of already-in-flight chunks arriving
  after `CancelTask` was sent is expected network-buffering behavior,
  not a bug).
- **`_make_stream_sink`** (replaces `_make_sink`) -- routes `chunk`
  payloads either through the new `StreamEvent`-based path (real
  streaming) or the old `TASK_TOKEN` path (echo fallback), and on
  terminal states (`done`/`failed`/`cancelled`) calls the
  accumulator's `finalize()`/`finalize_interrupted()` and persists the
  result via `sessions.append_message()` if non-`None`, then closes
  the stream via `StreamManager.close_stream()`.
- **Bounded outbox queue** -- `self.outbox` changed from an unbounded
  `asyncio.Queue()` to `asyncio.Queue(maxsize=1000)`. Since `_emit()`
  already `await`s `.put()`, this makes a slow consumer (e.g. Go not
  reading fast enough) naturally apply backpressure to the producer
  instead of growing memory without bound -- the concrete
  implementation of spec item 27.

## `providers/openai/provider.py` / `providers/openrouter/provider.py` -- fix
Both providers' `stream()` methods wrapped the streaming response in
`async with stream:` (matching what Ollama and Anthropic's SDKs
already did implicitly via their own client context managers). Without
this, cancelling a task mid-stream on OpenAI/OpenRouter could leak the
underlying HTTP connection instead of genuinely closing it -- caught
and fixed before it was ever exercised in production, since testing so
far has used Ollama.

## Manual Test Log (Episode 5)
- **Message-ID consistency** -- every `TextDelta`/`UsageUpdated`
  event's `message_id` for a given reply exactly matched the final
  persisted `AssistantMessage.id`, confirmed via `GetSession` after a
  full send/stream/complete cycle.
- **Concurrency** -- two sessions, two simultaneous `SendMessage`
  calls (a counting task and a spelling task on a small local Ollama
  model). Confirmed genuinely interleaved `TextDelta` events by
  distinct `task_id`/`session_id`/`message_id`, independent per-task
  `sequence` counters each starting at 1, and both final persisted
  messages were clean and self-contained with zero cross-contamination
  between the two concurrent streams.
- **Cancellation** -- `CancelTask` sent mid-stream on a long
  generation produced two `TaskCancelled` events (one direct reply to
  the cancel request, one the task's own terminal event), a handful of
  already-in-flight chunks arriving after cancellation (expected
  network buffering, not a bug), and a persisted partial
  `AssistantMessage` with substantial real text, `metadata:
  {"interrupted": true, "reason": "cancelled"}`, and correctly zeroed
  usage (no `UsageUpdated` had arrived before the stream was torn
  down).
- **Empty-completion bug** -- caught via code review before it could
  be reproduced live (Ollama/real models rarely return genuinely empty
  completions, which is exactly why this class of bug tends to survive
  casual testing) -- see the `streaming/accumulator.py` entry above for
  the fix.

---
