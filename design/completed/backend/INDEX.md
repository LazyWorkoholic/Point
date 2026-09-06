# Completed — Backend

**This directory is the only place completion is claimed.** Task docs in
`../../tasks/backend/` describe intent; these files describe reality. If the code
and these files disagree, **the code wins** — report the mismatch, do not
silently edit either side.

Written only after Aryan has run an episode's manual verification and confirmed
it passed.

## Status

| # | Episode | State | File |
|---|---|---|---|
| B1 | Runtime | ✅ built + manually verified | `B01-runtime.md` |
| B2 | Session System | ✅ built + manually verified | `B02-session-system.md` |
| B3 | Message System | ✅ built + manually verified | `B03-message-system.md` |
| B4 | Provider System | ✅ built + manually verified | `B04-provider-system.md` |
| B5 | Streaming Engine | ✅ built + manually verified | `B05-streaming-engine.md` |
| B6 | Schema v2 + Envelope v1 + Bootstrap | ✅ built + manually verified | `B06-schema-bootstrap.md` |
| B7 | Project System | ✅ built + manually verified | `B07-project-system.md` |
| B8 | Context Builder v0 | ✅ built + manually verified | `B08-context-builder.md` |
| B9 | Agent Foundation | ✅ built + manually verified | `B09-agent-foundation.md` |
| B10 | Tool System | ✅ built + manually verified | `B10-tool-system.md` |
| B11 | The Three Agents | ✅ built + manually verified | `B11-three-agents.md` |
| B12 | Routing, Attachments, Concurrency Caps | ✅ built + manually verified | `B12-routing-attachments.md` |
| B13–B17 | — | not started | — |

## Verified file structure — end of Episode 11

Confirmed against disk on 2026-08-27, after B11's verification gate passed. This
supersedes the end-of-Episode-10 snapshot.

```
backend\
├── main.py                   transport only: stdin reader thread, outbox
│                             writer, teardown. Construction moved out   [B6]
├── bootstrap.py              resolve_db_path / run_migrations /
│                             build_runtime / shutdown_runtime           [B6]
├── cli.py                    doctor · migrate · session new|list ·
│                             send · tasks list · project init|resolve|
│                             list-files|read-file|lock *|check-stale    [B7]
│                             + context report [--session|--last|--json] [B8]
│                             + agents list|show|set-model;
│                               session new --agent <id>                 [B9]
│                             + send --script <name> (mock fixtures) and
│                               --max-steps/--max-tool-calls/--max-seconds
│                               (the BoundsOverride wire form);
│                               session new --project <id>              [B10]
│                             + send/context report --file/--select/
│                               --cursor/--mode (EditorContext, per-turn on
│                               BOTH commands -- never remembered on the
│                               session); send --wait (background early-
│                               return is the default for max_steps>1
│                               agents); @agent-id prefix stripping;
│                               memory list [--status] [--limit]         [B11]
├── appconfig.py              paths, config.toml, key resolution, masking;
│                             + BOM-tolerant, non-silent TOML parsing    [B7]
├── logging_setup.py          rotating file + stderr, key redaction      [B6]
├── lockfile.py               single-instance lock, safe pid probe       [B6]
│
│   runtime.db is GONE from here. It now lives at
│   %LOCALAPPDATA%\Point\runtime.db — override with POINT_DB, then
│   config `database.path`. The original was COPIED, not moved: the
│   old backend\runtime.db is left in place deliberately.               [B6]
│
├── agents\                    NEW package                               [B9]
│   ├── __init__.py
│   ├── builtins.py            the three seed AgentConfigs (general,
│   │                          filler-explainer, research), as code
│   ├── registry.py            AgentRegistry — seed_builtins, get, list,
│   │                          resolve, resolve_for_session
│   └── executor.py            AgentExecutor, AgentContext, ProviderOverride,
│                              resolve_provider_model_effort(), the
│                              request/response middleware chain
│                              + BoundsOverride/Bounds/resolve_bounds(),
│                                ToolServices, and _loop_body — the bounded
│                                tool loop that REPLACED _stream_body.
│                                Still exactly one task body: no tools +
│                                max_steps=1 is the old shape, by config  [B10]
│                              builtins.py: research's grants renamed
│                                web/fs -> read_file/list_files/web_search
│                                (no migration — built-ins re-upsert)     [B10]
│                              registry.py: + validate_all(), and grants
│                                validated BEFORE the first upsert        [B10]
│                              executor.py: + editor_context/
│                                research_reference_index params on run();
│                                _generate_title (fire-and-forget, tracked
│                                in _background_tasks + drain_background());
│                                _mentions_other_file() (the Filler's
│                                structural one-file guard — prompting
│                                alone did not hold against a real model);
│                                _maybe_propose()/_parse_fact_list()
│                                (proposal extraction, gated on
│                                agent.writes_proposals); _check_deadline()
│                                now called once per chunk INSIDE the
│                                streaming loop, not only between steps —
│                                timeout_s was not previously a real
│                                wall-clock cap on a single generation     [B11]
│                              builtins.py: UNCHANGED — every B11 behaviour
│                                difference is config- or request-driven,
│                                never a new field or an `if agent.id`     [B11]
│
├── tools\                     NEW package                              [B10]
│   ├── __init__.py
│   ├── models.py              Tool, ToolResult, ToolCallRecord — data only
│   ├── schema.py              hand-written JSON Schema subset; no new dep
│   ├── registry.py            ToolRegistry — register, get, list_for,
│   │                          resolve_for (refuses ungranted by name),
│   │                          validate_grants (LOAD time, not call time)
│   ├── executor.py            ToolExecutor — validate, run, time out,
│   │                          emit the started/completed pair, record the
│   │                          row, THEN raise if the tool was broken
│   ├── faults.py              POINT_MOCK-only __mock_fault raise/hang.
│   │                          Injection, not a mock tool — a mock tool
│   │                          would have to be granted, and that would
│   │                          make the grant list lie
│   └── builtins\              filesystem.py (read_file, list_files),
│                              web.py (web_search — DuckDuckGo keyless
│                              HTML endpoint, stdlib parser, no new dep),
│                              task_results.py (load_task_result, scoped
│                              to the current session on purpose)
│
├── runtime\                  ALL FILLED                    [B1 ✅ B7 ✅]
│   ├── __init__.py  dispatcher.py  state.py
│   ├── errors.py             + PathDeniedError, ProjectNotFoundError,
│   │                           LockNotHeldError — stopgaps for B17's
│   │                           PointError; do not build that piecemeal  [B7]
│   │                         + ContextOverflowError — same stopgap;
│   │                           carries both numbers as attributes       [B8]
│   │                         + AgentNotFoundError,
│   │                           BuiltinAgentImmutableError                [B9]
│   │                         + ToolError family, the THIRD B17 stopgap:
│   │                           BoundsExceededError(bounds_exceeded),
│   │                           ToolExecutionError(tool_error),
│   │                           ToolTimeoutError(tool_timeout),
│   │                           ToolNotGrantedError(tool_not_granted),
│   │                           ToolNotFoundError(tool_not_found),
│   │                           ToolsUnsupportedError(tools_unsupported).
│   │                           Unlike the other two stopgaps these ALREADY
│   │                           carry their codes — B17 folds them in, it
│   │                           does not invent codes for them           [B10]
│   ├── events.py             envelope v1 — the Go↔Python contract;
│   │                           + 9 project-system event types      [B6][B7]
│   │                         + ToolStarted / ToolCompleted. `task.progress`
│   │                           is the EXISTING ProgressUpdated — no third
│   │                           progress vocabulary was invented        [B10]
│   ├── runtime.py            + envelope echo, hello, ping, cancel ack,
│   │                           unarchive, task recorder;
│   │                           + 6 project-system handlers         [B6][B7]
│   │                         + memory service, build()+budget check
│   │                           before the task — this was a PLACEHOLDER,
│   │                           now REPLACED: _handle_send_message
│   │                           delegates to AgentExecutor.run();
│   │                           _context_policy/_demo_echo_body/
│   │                           _stream_chat_body deleted; sink now runs
│   │                           response middleware before persisting;
│   │                           _record_task passes agent_id              [B9]
│   │                         + tool registry built BEFORE the agent
│   │                           registry (seeding validates grants);
│   │                           SendMessage accepts an optional `bounds`;
│   │                           TaskFailed carries `code`; _record_task
│   │                           writes tasks.error_code for LIVE failures,
│   │                           not just backend_died.
│   │                           TaskStarted MOVED into the sink's
│   │                           "started" branch — emitting it from the
│   │                           handler raced the task body and dropped
│   │                           every event before it (see B10 bug #1)  [B10]
│   │                         + `agent`/`editor_context` on SendMessage;
│   │                           _resolve_agent_for_send() (per-message
│   │                           agent override, never written to
│   │                           session.settings); _build_reference_index()
│   │                           (session-scoped, same reasoning as
│   │                           load_task_result); `agent.max_steps > 1`
│   │                           drives BOTH inline-vs-background and
│   │                           whether a reference index is built — no
│   │                           `background: bool` field exists on
│   │                           AgentConfig, this is a heuristic that
│   │                           breaks for a future agent needing
│   │                           max_steps>1 inline (see INDEX.md parking
│   │                           lot); `background` now rides TaskStarted's
│   │                           payload; close() awaits agent_executor.
│   │                           drain_background() and every cancelled
│   │                           Task's wait() (bounded 5s) instead of a
│   │                           bare sleep(0) — the sleep(0) was not
│   │                           enough for a task cancelled mid-network-
│   │                           I/O to finish unwinding cleanly           [B11]
│   ├── task.py               + recorder, started_at/finished_at,
│   │                           PAUSED/AWAITING_APPROVAL (reserved)     [B6]
│   │                         + agent_id                                  [B9]
│   │                         + TaskResult.code, copied off the exception
│   │                           with getattr(exc, "code", None). Task still
│   │                           knows nothing about tools               [B10]
│   │                         + wait() — awaits the real asyncio.Task,
│   │                           swallows the CancelledError back at its
│   │                           own caller. Backs runtime.py's close() fix [B11]
│   └── task_manager.py       + recorder passthrough, sweep now wired   [B6]
│                             + agent_id parameter on create_task()       [B9]
│
├── managers\                                       [B2 ✅ B3 ✅ B7 ✅]
│   ├── __init__.py  message_translator.py
│   ├── session.py            provider/model now Optional; + effort,
│   │                           created_by, archived_at                 [B6]
│   ├── session_manager.py    + archived_at on archive/unarchive        [B6]
│   ├── message.py            + ContentType.THINKING, content.provider  [B6]
│   ├── message_history.py    + for_provider() reasoning filter         [B6]
│   ├── project.py            NEW — Project identity dataclass           [B7]
│   ├── pathguard.py          NEW — resolve_path(), the one path guard;
│   │                           every @file/tool/editor/SSH path goes
│   │                           through this, not a second check         [B7]
│   ├── gitignore.py          NEW — minimal .gitignore matcher (not
│   │                           spec-complete — see the file's docstring)[B7]
│   ├── project_manager.py    NEW — init/resolve, file index, guarded
│   │                           read, soft locks, stale check            [B7]
│   ├── context.py            NEW — build(), report(), ContextRequest,
│   │                           ContextPolicy, ContextLog, MemoryService.
│   │                           ContextPolicy is NOT AgentConfig — B9's
│   │                           AgentConfig adapts TO it via
│   │                           to_context_policy(), never replaces it   [B8]
│   ├── attachment.py         NEW — Attachment domain type               [B8]
│   ├── agent.py              FILLED — AgentConfig (frozen dataclass,
│   │                           mirrors the `agents` table row for row)
│   │                           + to_context_policy()                     [B9]
│   ├── message.py            + ToolMessage(text=...) — every translator
│   │                           renders through Message.text, so a
│   │                           TOOL_RESULT with only `data` reached the
│   │                           provider as an empty string             [B10]
│   ├── message_translator.py + tools_to_provider_format() — NON-abstract;
│   │                           None means "takes no tools"             [B10]
│   ├── context.py            + SessionMemoryService.task_messages(),
│   │                           backing load_task_result                [B10]
│   ├── editor_context.py     NEW — EditorContext, MODE_EXPLAIN/MODE_FILL,
│   │                           resolve_scope() (the no-parser enclosing-
│   │                           scope heuristic — indentation or brace
│   │                           matching, ~2k token cap, then +/-20 lines).
│   │                           Known failure cases: a UTF-8 BOM defeats it
│   │                           outright (found + fixed, see context.py
│   │                           below); no real nesting understanding
│   │                           (a decorator above a def is not pulled in);
│   │                           brace matching is naive character counting,
│   │                           not lexing                                [B11]
│   ├── context.py            + editor_context/research_reference_index on
│   │                           ContextRequest and report(); task_state
│   │                           now actually renders (both compete for
│   │                           budget like attachments, evicted with a
│   │                           reason if they don't fit); a BOM is
│   │                           stripped before splitlines() in
│   │                           _render_editor_context()                  [B11]
│   ├── memory.py             FILLED — Proposal (B11's own narrow stopgap,
│   │                           NOT B13's real memory_records/
│   │                           memory_scopes, which stay untouched since
│   │                           v2), PROPOSAL_STATUS_PENDING. B13 must read
│   │                           this before writing its own type — see
│   │                           decisions.md D97 and B13-memory-system.md's
│   │                           new "Read before starting" section         [B11]
│   └── tool.py               DELETED — B10 did NOT fill it. A registry,
│                             an executor, a schema checker and four tools
│                             are not one manager; see tools\ above.
│                             Do not recreate it, same as provider.py  [B10]
│       provider.py stays DELETED — do not recreate it
│
├── database\                                       [B2 ✅ B6 ✅ B7 ✅]
│   ├── __init__.py
│   ├── connection.py         pragmas; schema delegated to migrations   [B6]
│   ├── migrations.py         NEW — forward-only runner, v1 + v2        [B6]
│   │                         + v3: the tool_calls ledger. SCHEMA_VERSION
│   │                           is now 3. No FK to tasks — evidence should
│   │                           survive its subject                     [B10]
│   │                         + v4: agent_proposals. SCHEMA_VERSION is now
│   │                           4. Deliberately NOT memory_records/
│   │                           memory_scopes (unchanged, still B13's)    [B11]
│   ├── proposal_repository.py  NEW — insert / count_for_session (the
│   │                           WHOLE enforcement for "at most 3 per
│   │                           session") / list_by_status /
│   │                           fetch_for_session. No update, no delete,
│   │                           no review path — that is B13's, against
│   │                           memory_records, not this table            [B11]
│   ├── session_repository.py + the three new session columns           [B6]
│   ├── task_repository.py    NEW — durable task mirror + recovery      [B6]
│   │                         agent_id now actually supplied by the
│   │                           caller (Runtime._record_task)             [B9]
│   ├── project_repository.py NEW — raw SQL for projects + file_locks   [B7]
│   ├── attachment_repository.py  NEW — read + update-recorded-pair
│   │                          ONLY; no insert/delete/set_mode (B12)     [B8]
│   ├── agent_repository.py   NEW — raw SQL; update_fields()/delete()
│   │                          reject builtin=True before any write       [B9]
│   ├── session_repository.py + fetch_messages_for_task(), the first
│   │                          reader idx_messages_task ever had        [B10]
│   └── tool_call_repository.py  NEW — insert-only ledger; record,
│                              fetch_for_task, list_recent. No update and
│                              no delete path, deliberately             [B10]
│
├── providers\                                            [B4 ✅ B6 ✅]
│   ├── __init__.py           NEW — was missing; namespace cleanup      [B6]
│   ├── base.py               + selectable flag                         [B6]
│   ├── bootstrap.py  errors.py  registry.py
│   ├── models.py             + Effort, supports_effort, supports_caching,
│   │                           credentials.toml key resolution         [B6]
│   │                         Effort now actually reaches
│   │                           GenerationRequest.effort (via B9's
│   │                           resolve_provider_model_effort())          [B9]
│   ├── retry.py              NEW — 429 backoff: 5 attempts, 1.0s base,
│   │                         x2.0, +/-25% jitter, 60s cap, Retry-After
│   │                         honoured AND capped. Only a stream that has
│   │                         produced nothing may be retried            [B10]
│   ├── anthropic\  gemini\  ollama\  openai\  openrouter\
│   │                         gemini gained real stream teardown        [B6]
│   │                         all five now translate the common tool
│   │                         schema and normalize tool calls back;
│   │                         BaseProvider.supports_tools (per PROVIDER,
│   │                         distinct from ModelCapabilities') is checked
│   │                         at INVOCATION, before a token streams     [B10]
│   └── mock\                 NEW — POINT_MOCK=1 only, never selectable [B6]
│                             + `turns` fixtures and tool_call/rate_limit/
│                               hang chunks, so a bounded tool loop is
│                               testable without a live model           [B10]
│
├── streaming\                ALL FILLED                             [B5 ✅]
│   ├── __init__.py  manager.py
│   ├── accumulator.py        usage is now SUMMED, not replaced — a tool
│   │                         loop streams several turns into ONE
│   │                         accumulator                              [B10]
│   ├── adapter.py            ProviderChunkType.TOOL_CALL maps to NOTHING
│   │                         (returns None, like DONE). A model asking
│   │                         for a tool is not a tool starting        [B10]
│   └── events.py             three dead terminal members removed       [B6]
│                             TOOL_STARTED/TOOL_COMPLETED, reserved since
│                             B5, finally have producers               [B10]
│
└── tests\
    ├── manual_provider_test.py
    ├── manual_context_test.py  NEW — stm_window + reasoning filter, the
    │                          two checks the CLI cannot reach           [B8]
    ├── fixtures\             NEW — mock_stream.json, mock_slow.json    [B6]
    │                         + seven tool-loop scripts, replayed by
    │                           `send --script <name>`: two-tool-calls,
    │                           three-tool-calls, tool-raises, tool-hangs,
    │                           rate-limited-then-ok, read-outside-project,
    │                           ungranted-tool                          [B10]
    ├── manual_b11_verify.ps1  NEW — the full episode gate: explainer scope
    │                          ladder, filler writes-nothing + two-file
    │                          refusal, research (background early-return,
    │                          --wait, proposals, the memory-first repeat),
    │                          session titles. Redirects APPDATA/
    │                          LOCALAPPDATA in one script; real Ollama       [B11]
    ├── manual_b11_deadline_check.ps1  NEW — the controlled before/after
    │                          reproduction of the mid-stream timeout_s
    │                          bug: an engineered long answer at
    │                          --max-seconds 8, unbounded before the fix,
    │                          cut off at exactly 8s after it                [B11]
    └── manual_b11_memory_recall.ps1  NEW — the decisive (not just
                               "offered") memory-first test: seeds 12
                               filler exchanges directly (no LLM calls) so
                               the first research answer falls out of
                               stm_window, then asks a reworded question
                               the model can only answer via a real lookup.
                               Result recorded as "documented, not
                               resolved" — see B11-three-agents.md          [B11]
```

**`memory\` and `schemas\` do not exist.** The original log listed them as
planned directories alongside `agents\`; `agents\` was created in B9 and
`tools\` in B10 (both above). `memory\` and `schemas\` are still not created
— B13 and (per the original plan) a later episode create what they need.

## Delta — Episode 12

Recorded as a delta rather than a rewritten tree: B12 added no top-level
package, so restating 330 lines to insert five entries would risk more than it
documents. Full reasoning in `B12-routing-attachments.md`.

```
backend\
├── managers\
│   ├── invocation.py         NEW — Invocation / AttachmentRef /
│   │                          parse_invocation(). The ambiguity rule
│   │                          ("an exact agent name wins, otherwise it is
│   │                          a path") lives here and nowhere else, so the
│   │                          CLI and F8's picker cannot disagree. Zero
│   │                          I/O: takes is_agent / is_indexed_path as
│   │                          predicates                                  [B12]
│   ├── attachment_manager.py NEW — the guards (path, exists, directory,
│   │                          BINARY, size — in that order), the token
│   │                          estimate produced by context.py's own
│   │                          _render_attachment, preview(), and
│   │                          attach / detach / set_mode                  [B12]
│   ├── session_manager.py    + set_project() — project_id was write-once
│   │                          at CreateSession, which left a session
│   │                          unable to attach anything                   [B12]
│   └── message.py            + Outcome enum; outcome / interrupted /
│                              error_code on Message                       [B12]
├── runtime\
│   ├── scheduler.py          NEW — TaskScheduler. Two lanes
│   │                          (provider, turn), first-fit, in-process by
│   │                          design (lockfile.py makes the backend
│   │                          single-instance)                            [B12]
│   ├── task.py               + TaskState.QUEUED (NEW — not a B1 state),
│   │                          cancel-while-queued, `bound` on the failed
│   │                          chunk                                       [B12]
│   ├── task_manager.py       create_task() submits to the scheduler
│   │                          instead of starting                         [B12]
│   ├── errors.py             + AdvancedUnavailableError, QueueFullError,
│   │                          AttachmentRefusedError,
│   │                          AttachmentNotFoundError — all with `code`   [B12]
│   ├── events.py             + AttachContext / DetachContext /
│   │                          SetAttachmentMode / ListAttachments /
│   │                          ResolveAttachment, and the four replies     [B12]
│   └── runtime.py            input parsing, --advanced refusal, turn-queue
│                              admission, attachment handlers,
│                              _attachment_scope(), _adopt_project_for(),
│                              _outcome_for(), tasks.list(scope);
│                              Error payloads now carry `code` + `offer`   [B12]
├── database\
│   ├── migrations.py         SCHEMA_VERSION 4 -> 5                        [B12]
│   ├── attachment_repository.py + insert / delete / set_mode /
│   │                          fetch_by_source (the writes B8 left)        [B12]
│   ├── session_repository.py writes the three new message columns         [B12]
│   └── task_repository.py    LIVE_STATUSES gained "queued"                [B12]
├── streaming\
│   ├── accumulator.py        finalize() takes invocation + outcome;
│   │                          stops writing metadata["interrupted"]       [B12]
│   └── manager.py            + register_invocation / invocation_for,
│                              keyed per TASK (the echo path persists a
│                              reply without ever registering a stream)    [B12]
└── tests\
    ├── manual_b12_verify.ps1      the whole gate, one script              [B12]
    ├── manual_b12_concurrency.py  cap + queue, ONE backend, exit code     [B12]
    └── manual_b12_parser.py       routing rules, no I/O, exit code        [B12]
```

`cli.py` gained `attach` / `detach` / `set-mode` / `attachments list` /
`task cancel [--last]`, `tasks list --scope`, `send --agent`, and
`session new --path/--no-project` (which resolves and binds the project, so no
uuid is threaded by hand). `_strip_agent_prefix` was **deleted** — the backend
parses now.

## ⚠ One inaccuracy inherited from the original log

`B02-session-system.md` contains a test entry labelled **"(Episode 1)"**. It sat
between the Episode 2 and Episode 3 sections in the source file and was carried
across with the split. Move it to `B01-runtime.md` if you want strict per-episode
accuracy. The per-episode files are otherwise unedited history — do not tidy them.

## Known drift — B1–B5 docs vs. the code, audited 2026-08-19

Every file `B01`–`B05` claim exists **does** exist and does roughly what it says.
All 28 backend modules import cleanly (at the time of the 2026-08-19 audit — B6
added 18 more; see the file-structure snapshot above). No episode is fabricated.
What follows is the list of places where the per-episode files and the code had
come apart as of that audit.

**The per-episode files are unedited history and stay that way.** This section is
where the corrections live. Rows B6 has since fixed are marked **fixed** rather
than removed, so the audit stays a complete record of what B1–B5 actually shipped
with.

### Code is right, the doc is stale

Unaffected by B6 — none of these were in its scope.

| Where | Doc says | Code does | Status |
|---|---|---|---|
| `managers\provider.py` | B02-era trees list it as an existing empty file | deleted | — (recorded above) |
| B02 schema block | `messages.id INTEGER AUTOINCREMENT`, `content TEXT`, no `task_id` | the B3 shape — TEXT pk, `task_id`, JSON `content`, metadata + three token columns | — (B03 documents the change; B02 read alone misleads) |
| B01 on `Runtime.__init__` | "wires all managers, registers all handlers, sets status to READY" | sets `READY` **before** building `providers`, `streams`, `stream_adapter` | harmless; sync `__init__` |
| B04 on `bootstrap.py` | "lazily imports each provider's SDK-dependent module inside its own branch" | true for four; **Ollama is imported at module top level** | harmless; its dep is `httpx` |
| B01 on `_demo_echo_body` | docstring says "replaced by ChatAgent.run() in Episode 4" | still present as the `provider == "echo"` path; B05 is what added real streaming | B6 §14 leaned on it deliberately — `cli.py`'s default-provider fallback is built on this exact path |

### Code was wrong, or carried a claim nothing backed — all fixed in B6

Every row below was confirmed fixed in `B06-schema-bootstrap.md`'s manual test log
(2026-08-26).

| Where | What was wrong | Fixed by |
|---|---|---|
| `runtime\runtime.py` | `self.outbox` was assigned **twice** — unbounded, then `maxsize=1000`. Bounded won, so behaviour matched B05; the dead line remained. | B6 §9.3 — dead assignment deleted |
| `runtime\task_manager.py` | `sweep_finished`'s own docstring claimed "Called periodically by Runtime." Nothing called it. | B6 §9.6 — wired into the heartbeat loop; docstring corrected |
| `streaming\events.py` | `StreamEventType` carried `COMPLETED`, `FAILED`, `CANCELLED` — never mentioned in B05, never produced, never consumed. | B6 §9.4 — all three deleted |
| `providers\gemini\provider.py` | B05 documented the `async with stream:` teardown fix for OpenAI/OpenRouter and noted Ollama and Anthropic already had it. Gemini was never mentioned and never fixed — it iterated the stream bare, leaking the connection on cancel. | B6 §9.2 — `_close_stream()` closes the async iterator in a `finally` |
| `managers\session_manager.py` | `unarchive()` existed with no event type and no handler — unreachable from Go. | B6 §9.1 — `UnarchiveSession` / `SessionUnarchived` added, `_handle_unarchive_session` registered |
| `main.py` | `write_stdout_loop` was never cancelled, so `asyncio.gather` never returned after stdin EOF. The backend hung instead of exiting when Go closed the pipe; the DB connection was never closed. | B6 §9.5, then hardened further after a bug found in verification (broken stdout also skipped shutdown — see `B06-schema-bootstrap.md`, "Bugs caught during manual testing") |
| `providers\__init__.py` | Missing, while every other package had one. Worked as a namespace package regardless. | B6 — added |

**`runtime\runtime.py`'s `_handle_shutdown` was out of scope for B6 and remains
unfixed.** It still emits no ack, does not stop the process, and flips status away
from `READY` so every later event fails. **The two shutdown paths are
deliberately split**: B6 fixed only the stdin-EOF hang above; **B15 owns the
`Shutdown` event**, because quiescing correctly (stop accepting work, cancel with
partial persistence, release contexts, close the DB, ack, exit) needs the context
registry and the refusal rule that B15 builds. `Shutdown` quiesces; it does not
terminate. See `tasks/backend/B15-lifecycle-resources.md`, "The `Shutdown` event
— this episode owns it". Do not fold it back into B6.

### Contract-level drift from `project_idea.md` — all closed in B6

- **The §4 envelope was never implemented**, as of the 2026-08-19 audit.
  Documented: `{v, id, type, corr_id, session_id, task_id, connection_id, ts,
  payload}`. What shipped in B1–B5: `{event, request_id, timestamp, task_id?}`
  with payload keys flattened to the top level — no `v`, no `corr_id`, no
  `hello`, no `ping`, no envelope-level `seq`. **B6 adopted the documented
  envelope.** The before/after shape is recorded in full in
  `B06-schema-bootstrap.md` and in `decisions.md` D91 — that is the reference
  F2 is written against.
- **"Exactly one terminal event per task" (§4 rule 3) was false.** A cancel
  emitted `TaskCancelled` twice — once as the ack, once from the sink — and
  B05's test log recorded it as expected. **B6 §7** made the ack a non-terminal
  `task.cancel.accepted`; confirmed in verification check 10.
- **WAL was assumed and not enabled.** §3 and §12 rest on it; `open_database` set
  only `foreign_keys=ON` and the live DB reported `journal_mode=delete`.
  **B6 §11** turned it on; verification check 5 confirmed `journal_mode = wal`.
- **"Never log to stdout" was satisfied vacuously** — there was no logging at all
  in `backend\`. Malformed input was dropped silently. **B6 §16** added the
  first logging (`logging_setup.py`); verification check 13 confirmed no raw key
  reaches it.

### Episode numbering in B1–B5 predates the current INDEX

Code comments and completion docs point at an older plan. Only "Episode 10 = Tool
System" still lines up. Unaffected by B6 beyond confirming its own slot.

| Referenced | Actually |
|---|---|
| "Episode 7 (Memory)" — `MessageHistory.get_window()`, `search()` | B7 is Project System; memory is **B13** |
| "Episode 8's Agent layer" — Anthropic translator, reserved stream types | B8 is Context Builder; agents are **B9** |
| "Episode 6 (Persistence Expansion)" — B05's carried-forward notes | B6 is **Schema v2 + Envelope v1 + Bootstrap**, confirmed by this file |
| "Episodes 7/15" for RAG | no B15 = RAG; B15 is Lifecycle |
| "Episode 18" — `ProviderConfig.from_env` docstring | there is no B18; secrets are **B6 §12**, confirmed by this file |

Treat every episode number inside `backend\*.py` and inside `B01`–`B05` as
referring to that older plan. `tasks/backend/INDEX.md` is authoritative.

### Git tracking — resolved

As of the 2026-08-19 audit, `backend\` was untracked (`git status` reported
`?? backend/`, `git ls-files backend` was empty) despite five episodes being
marked verified here. **`backend\` has been tracked since commit `170dd8e`**
("backend: episode 1-5"); `git ls-files backend | wc -l` now reports 46 files
after B6. There is no longer a gap between what this file claims and what git
holds.

---

## How to add an entry

When an episode passes its verification gate, create `B##-<slug>.md` with the same
shape as the existing files:

```
# Episode N — <Title> ✅
**<one-line summary>**

## <file path>            ← one section per file created or changed
<what it does, why it exists, what connects to it>

## Manual Test Log (Episode N)
<numbered, specific behaviours confirmed — not "it ran">

## Bugs caught during manual testing
<what broke, why, and the fix — the most valuable section>

## What the next episode inherits
<state, interfaces, and anything deliberately left unfinished>
```

Then add a row to the table above, and **update the verified file structure** —
that snapshot going stale is exactly what happened to the original log.

**Do not** mark anything here from a task doc alone.

---

## Notes carried forward from Episode 5

- `StreamManager`/`StreamAccumulator` currently live only inside
  `Runtime`'s in-memory state -- Episode 6 (Persistence Expansion)
  should decide whether stream/task history needs its own repository,
  or whether the existing `messages` table (with its `task_id` column
  from Episode 3) is sufficient as the durable record. No schema
  change is expected to be required simply to support this.
- `providers/errors.py`'s normalized error hierarchy already flows
  correctly into `_stream_chat_body` -- a `ProviderError` raised
  mid-stream propagates up through the async generator, is caught by
  `Task._run()`'s existing exception handling, and reaches the sink as
  a `"failed"` chunk, which now correctly calls
  `finalize_interrupted("failed", error=...)`. Episode 6 doesn't need
  to touch this path.
- `StreamEventType` already reserves `THINKING_DELTA`,
  `STATUS_CHANGED`, and `PROGRESS_UPDATED` -- once Episode 8's Agent
  layer needs to report intermediate status (e.g. "searching",
  "reading source"), it can emit these through the exact same
  `StreamAdapter`/`StreamManager`/sink pipeline built here, with no
  new plumbing required.
- `TOOL_STARTED`/`TOOL_COMPLETED` are reserved but genuinely unused --
  Episode 10 (Tool System) is what will give them real producers.
- ~~Session's message history is now loaded in full
  (`load_messages=True`) on every `SendMessage` and passed whole into
  `GenerationRequest.messages` -- this works fine at current
  conversation lengths but has no context-window budgeting yet.
  `MessageHistory.get_window()` still correctly raises
  `NotImplementedError` -- Episode 7 (Memory) is what's expected to
  replace the "send the whole history" behavior with something bounded.~~
  **fixed in B8.** `managers/context.py`'s `build()` replaced
  "send the whole history": `_handle_send_message` now sends
  `BuiltContext.messages` under an explicit `context.budget_tokens`
  budget, and dropped its `load_messages=True` (the builder reads
  history itself through its own `stm_window`, so loading it here as
  well fetched everything twice and only one copy was sent).
  **The episode number in the original note was wrong** — B7 is the
  Project System and B13 is Memory; budgeting landed in **B8**, the
  Context Builder. See the "Episode numbering in B1–B5 predates the
  current INDEX" table above, which already flagged
  "Episode 7 (Memory)" as one of these stale references.
  **`MessageHistory.get_window()` is now permanently redundant, not a
  B13 stub.** `context.build()` supersedes it, and `grep` confirms no
  caller remains (only its own definition and the module docstring);
  its `NotImplementedError` still names "Episode 7 (Memory)". B13
  should delete it rather than implement it — implementing it would
  create a second windowing path competing with the builder, which is
  exactly the drift `for_provider()` was kept single to avoid.
  `MessageHistory.get_recent()` is **also uncalled** as of B8, but for
  a duller reason: `SessionMemoryService.recent_messages()` slices
  `messages[-n:]` at the repository boundary rather than building a
  `MessageHistory` just to window it. Not superseded, merely unused —
  either wire it up or drop it, but do not read its existence as
  evidence of a second windowing policy.
- Known follow-ups, not blocking: `StreamManager`'s accumulators are
  never explicitly capped or swept (unlike `TaskManager.sweep_finished
  ()` from Episode 1) -- worth revisiting if long-running sessions with
  many completed streams start showing memory growth, but not an issue
  at current testing scale.
