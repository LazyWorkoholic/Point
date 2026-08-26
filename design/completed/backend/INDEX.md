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
| B10–B17 | — | not started | — |

## Verified file structure — end of Episode 9

Confirmed against disk on 2026-08-26, after B9's verification gate passed. This
supersedes the end-of-Episode-8 snapshot.

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
│   ├── events.py             envelope v1 — the Go↔Python contract;
│   │                           + 9 project-system event types      [B6][B7]
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
│   ├── task.py               + recorder, started_at/finished_at,
│   │                           PAUSED/AWAITING_APPROVAL (reserved)     [B6]
│   │                         + agent_id                                  [B9]
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
│   ├── memory.py             EMPTY  → B13 fills it
│   └── tool.py               EMPTY  → B10 fills it
│       provider.py stays DELETED — do not recreate it
│
├── database\                                       [B2 ✅ B6 ✅ B7 ✅]
│   ├── __init__.py
│   ├── connection.py         pragmas; schema delegated to migrations   [B6]
│   ├── migrations.py         NEW — forward-only runner, v1 + v2        [B6]
│   ├── session_repository.py + the three new session columns           [B6]
│   ├── task_repository.py    NEW — durable task mirror + recovery      [B6]
│   │                         agent_id now actually supplied by the
│   │                           caller (Runtime._record_task)             [B9]
│   ├── project_repository.py NEW — raw SQL for projects + file_locks   [B7]
│   ├── attachment_repository.py  NEW — read + update-recorded-pair
│   │                          ONLY; no insert/delete/set_mode (B12)     [B8]
│   └── agent_repository.py   NEW — raw SQL; update_fields()/delete()
│                              reject builtin=True before any write       [B9]
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
│   ├── anthropic\  gemini\  ollama\  openai\  openrouter\
│   │                         gemini gained real stream teardown        [B6]
│   └── mock\                 NEW — POINT_MOCK=1 only, never selectable [B6]
│
├── streaming\                ALL FILLED                             [B5 ✅]
│   ├── __init__.py  accumulator.py  adapter.py  manager.py
│   └── events.py             three dead terminal members removed       [B6]
│
└── tests\
    ├── manual_provider_test.py
    ├── manual_context_test.py  NEW — stm_window + reasoning filter, the
    │                          two checks the CLI cannot reach           [B8]
    └── fixtures\             NEW — mock_stream.json, mock_slow.json    [B6]
```

**`memory\` and `schemas\` do not exist.** The original log listed them as
planned directories alongside `agents\`; `agents\` was created in B9 (above).
`memory\` and `schemas\` are still not created — B13 and (per the original
plan) a later episode create what they need.

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
