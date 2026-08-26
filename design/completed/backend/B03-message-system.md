# Episode 3 — Message System ✅

**Message types, history, translation**

> **This file records what was actually built and manually verified.**
> It is the only place completion is claimed. Task docs describe intent; this
> describes reality. If the code and this file disagree, **the code wins** —
> report the mismatch, do not silently edit either side.
>
> Original source: `completed_backend_task.md`, split per episode. Content is
> unchanged apart from heading levels.

---

## `managers/message.py` (NEW)
The structured `Message` model — the "common language" the whole
project speaks instead of passing bare strings around.

- `MessageRole` enum: `SYSTEM`, `USER`, `ASSISTANT`, `TOOL`, `ERROR`.
- `ContentType` enum: `TEXT`, `CODE`, `IMAGE`, `TOOL_RESULT`,
  `STRUCTURED`. Only `TEXT` and `TOOL_RESULT` are actually produced
  right now; the rest exist so the shape doesn't need to change later.
- `MessageContent` — `type` + `text` (for TEXT) + `data` (free-form
  dict, used by every other content type).
- `TokenUsage` — `input_tokens`/`output_tokens`/`total_tokens`. Data
  model only; always zero until Episode 5 wires up real provider
  responses.
- `InvocationMetadata` — `workspace`/`capability`/`agent`/`provider`/
  `model`. Kept as its own typed field on `Message` (not inside the
  generic `metadata` dict) because "which workspace/capability/agent
  produced this" is a specific, recurring question that deserves a
  stable shape. Always `None` until Episodes 5/8/11 populate it.
- `Message` — `id`, `session_id`, `role`, `content`, `task_id`
  (nullable — user messages leave it `None`, assistant/tool/error
  messages set it once their task exists), `timestamp`, `metadata`,
  `invocation`, `usage`. `.validate()` fails fast on missing
  id/role/content/session_id. `.to_dict()`/`.from_dict()` for JSON
  round-tripping.
- Message type factories: `SystemMessage()`, `UserMessage()`,
  `AssistantMessage()` (plain text builders), `ToolMessage()` (builds
  proper `ContentType.TOOL_RESULT` content with `tool_name`/
  `tool_call_id`/`result` — not a plain string), `ErrorMessage()`
  (carries `error_code`/`error_type` in metadata alongside the
  human-readable text).

## `managers/message_history.py` (NEW)
Ordered-message abstraction over a single session's messages.

- `add()`, `all()`, `last()`, `get()`, `clear()`, `serialize()`,
  `deserialize()`.
- History querying (plain filtering): `filter_by_role()`, `since()`,
  `by_task()`.
- `get_recent(limit)` — naive recency slice, usable today.
- `get_window(max_tokens)` / `search(query)` — both intentionally
  raise `NotImplementedError`. This is deliberate: it makes it obvious
  at the call site that Episode 7 (Memory) / Episode 15 (RAG) are
  responsible for these, instead of silently returning a wrong answer.

## `managers/message_translator.py` (NEW)
`MessageTranslator` — an abstract base class only (`to_provider_format`
/ `from_provider_format`), no implementation. This is the seam where
our internal `Message` meets each provider's wire format. Episode 5
supplies one concrete subclass per provider family
(Claude/OpenAI/Gemini/Ollama/OpenRouter). Defined now so Episode 5
plugs in without touching `Message`, `MessageHistory`, or
`SessionManager` — and so the internal `Message` shape is never
designed around any single vendor's schema.

## `managers/session.py` — updated
The old local `Message` dataclass (from Episode 2) was removed.
`Session.messages` now holds the real `Message` type from
`managers/message.py`. All other `Session` fields unchanged.

## `database/connection.py` — schema updated
`messages` table changed shape:
- Primary key is now `TEXT` (UUID) instead of `INTEGER AUTOINCREMENT`,
  matching every other entity's ID scheme.
- Added `task_id TEXT` column + `idx_messages_task` index — this is
  what makes task-to-message tracing actually queryable, not just an
  in-memory property.
- `content` is now a JSON-serialized blob (was plain `TEXT`), so it
  can hold any `ContentType`, not just plain text.
- Added `metadata TEXT` column — stores a JSON blob containing both
  the free-form `metadata` dict *and* the optional `InvocationMetadata`
  dict together (no separate `invocation` column was added — kept the
  schema change small since both are optional/JSON anyway).
- Split `total_tokens` into `input_tokens` / `output_tokens` /
  `total_tokens` columns.

## `database/session_repository.py` — updated
- `insert_message(message)` now takes a full `Message` object (was
  `insert_message(session_id, message)` with a locally-defined simple
  `Message`). Serializes `content` and the combined
  `metadata`+`invocation` blob to JSON.
- `fetch_messages(session_id)` now orders by `created_at ASC, rowid ASC` 
  (rowid as a tiebreaker, since `created_at` floats from
  `time.time()` can theoretically collide).
- `_row_to_message(row)` — new helper, deserializes a DB row back into
  a full `Message`, unpacking the combined metadata/invocation blob.

## `managers/session_manager.py` — updated
- `append_message(message: Message)` — signature changed from the
  Episode 2 version (`append_message(session_id, role, content)`).
  Callers now build a fully-validated `Message` via the factory
  functions in `managers/message.py` and pass the whole object.
- `get_history(session_id)` — new method, returns a `MessageHistory`
  wrapper around the session's messages. Not yet called from
  `Runtime` — exists as ready-to-use infrastructure for Episode 7.

## `runtime/runtime.py` — updated
- `_handle_send_message` — now builds `UserMessage(session_id, text)`
  and passes the `Message` object to `sessions.append_message()`,
  instead of passing `role="user", content=text` (which no longer
  matches `SessionManager`'s signature).
- `_make_sink`'s `done` branch — now builds
  `AssistantMessage(session_id, full_reply, task_id=task.id)`,
  explicitly attaching the task's own id so the message is traceable
  back to the task that produced it (confirmed working in manual
  testing — see below).
- `_session_to_dict` — `messages` serialization now calls
  `m.to_dict()` on each `Message` directly, rather than manually
  building a dict per message.

## Bug caught and fixed during manual testing
First pass at wiring `runtime.py` to the new `Message` model missed
updating the actual call sites — `_handle_send_message` and
`_make_sink` were still calling the old Episode 2 signature
(`append_message(session_id, role="assistant", content=full_reply)`).
This surfaced immediately as a `TaskFailed` event
(`SessionManager.append_message() got an unexpected keyword argument
'role'`) during testing, not silently. Fixed by updating both call
sites to build proper `Message` objects via the `UserMessage()`/
`AssistantMessage()` factories first.

## Manual Test Log (Episode 3)
`CreateSession` → `SendMessage` → `GetSession` sequence confirmed:
- User message correctly has `task_id: null` (it triggers a task,
  it isn't a task's output).
- Assistant message's `task_id` exactly matches the `task_id` seen in
  that message's `TaskStarted`/`TaskToken`/`TaskDone` events — proving
  a message can be traced back to the specific concurrent task that
  produced it, which was the core architectural point of this episode.
- Both messages round-trip `"invocation": null` through storage
  without error.

---
