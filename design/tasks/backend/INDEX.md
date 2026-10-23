# Backend — Episode Index

Read `../../project_idea.md` first. Load **one** episode file per session.

**Everything runs from inside `backend\`** — imports are `from runtime.runtime
import ...`, not package-absolute. Do not change that.

```powershell
cd D:\Projects\Point\backend
uv run python main.py
```

## Status

**State is derived from `design/completed/backend/`, not maintained by hand.**
An episode is built when, and only when, it has a completed doc there. This
column had drifted badly -- B12 through B23 all read "not started" while their
completed docs were on disk, and B19 and B22 were missing from the table
altogether -- which is the same failure CLAUDE.md's layout section had: a file
every session reads first, stating something the repo contradicts. Re-derived
2026-10-02. If it disagrees with `completed/` again, **`completed/` is right**;
fix this table.

| # | Episode | Phase | Batchable | Depends on | State |
|---|---|---|---|---|---|
| B1 | Runtime | — | — | — | ✅ built |
| B2 | Session System | — | — | B1 | ✅ built |
| B3 | Message System | — | — | B2 | ✅ built |
| B4 | Provider System | — | — | B3 | ✅ built |
| B5 | Streaming Engine | — | — | B4 | ✅ built |
| B6 | Schema v2 + Envelope v1 + Bootstrap | P1 | no | B5 | ✅ built |
| B7 | Project System | P1 | no | B6 | ✅ built |
| B8 | Context Builder v0 | P1 | no | B6 | ✅ built |
| B9 | Agent Foundation | P1 | no | B8 | ✅ built |
| B10 | Tool System | P1 | no | B9 | ✅ built |
| B11 | The Three Agents | P1 | no | B10 | ✅ built |
| B12 | Routing, Attachments, Caps | P1 | no | B11 | ✅ built |
| B13 | Memory System | P2 | no | B12 + real usage | not started — `memory_records`/`memory_scopes` exist since schema v2 and NOTHING writes them; B11's `agent_proposals` is the insert-only stopgap |
| B14 | Usage & Limits | P2 | no | B12 | not started — `usage_records` exists and is written per turn, but has no repository and no wire event |
| B15 | Lifecycle & Resources | P2 | no | B12 | not started — **demo-critical** |
| B16 | SSH Serving | P2 | no | B15, F12 | not started |
| B17 | Hardening | P2 | no | all | not started |
| B18 | Message Pagination | P2 | no | B6 | ✅ built |
| B19 | Silence-Based Agent Timeout | P1 | no | B11 | ✅ built |
| B20 | `context.report` on the Wire | P1 | no | B8, B6 | ✅ built |
| B21 | Orphaned Messages in Context | P1 | no | B8, B12, S11b | ✅ built |
| B22 | Concurrent Task Completion Race | P1 | no | B12 | ✅ built |
| B23 | Agent + Directory Listing on the Wire | P1 | no | B7, B9 | ✅ built |
| B24 | The Filler's Proposal on the Wire | P1 | no | B11, B8, B12 | not started — unblocks F9's diff card (WORKFLOW.md session 17b) |
| B25 | Persist a Validated Provider Key | P1 | no | B6, B4 | not started — unblocks F10's credential-entry Done-when #4-6 (WORKFLOW.md session 15c) |
| B30 | Let `SetSessionProvider` Also Bind a Session's Project | P1 | no | B12, B4/B6 | ✅ built |
| B31 | Validate `project_id` in `CreateSession`/`SetSessionProvider` | P1 | no | B7, B12, B30 | ✅ built |
| B32 | `WriteProjectFile` — the Editor's Save Path | P1 | no | B7, B6 | ✅ built |
| B33 | The protocol channel is UTF-8 | P1 | no | B6 | ✅ built — stdin/stdout ran in the ANSI codepage, so a non-ASCII save was silently mojibake'd (reported as success) or dropped, and an undecodable line killed the reader thread and hung the process with the lock held. Found from F13b's outside-in testing; live since B6 |
| B34 | A declared line-ending policy for this repo | P2 | no | — | not started — no `.gitattributes` and `core.autocrlf=true` from the SYSTEM config, so every blob is LF while 45 tracked files are CRLF in the working tree and `backend\runtime\errors.py` is MIXED. `gofmt -l` cannot be a gate on a fresh Windows checkout. Found during the B33 review. **Run after B35**, whose two stray UTF-16LE files are the binary blobs B34's draft says do not exist |
| B35 | Two artefacts B20 committed and nobody read | P2 | no | — | not started — `CLAUDE.md` has been mojibake'd since `790e0c2` (B20, 2026-09-24): 24 lines carry `â€”` for an em-dash, plus an undeliberate BOM. Same bug class as B33, but a pre-existing instance B33's after-check could not see because it compared the working tree, never committed history. The same commit also added `backend\direct.json` and `backend\wire.json` — byte-identical UTF-16LE `context.report` dumps that git stores as binary and nothing reads. Two root causes, one commit. Found during a repo-wide verification pass over B33/F13b at `fdc6cf7`. Does not reopen B33 |

| B36 | Generation-time staleness for the Filler's proposal | P1 | no | B24, B7 | ✅ built — see `completed/backend/B36-generation-time-staleness.md`. `FillProposal` now carries `(mtime, size)` stat'd from the exact file descriptor `_render_editor_context` reads the region from; `CheckFileStale` accepts `session_id` (resolves the SESSION's own scope, `_attachment_scope`, over `_resolve_optional_project`'s path walk-up — B12 bug #2) and `size` (a real, additively-optional comparison) as real fields. **Unblocks F9's Done-when #7 and F9a's hazard-1 guard order — neither is built yet; both are frontend work, now scheduled (#7 in Session 19/F15, hazard-1 in the new Session 19b).** Full account, including what each caller shape gets, in the completed doc |
| B37 | Delimit a multi-line fill request in the rendered prompt | P2 | no | B24, B11 | not started — **spec only, the backend half of F21**. The user's request travels as `text` in `CmdSendMessage` and `_render_editor_context` drops it into a prompt that already contains a newline-separated instruction block (`Respond with exactly one JSON object…`). A single-line request cannot collide with that; a multi-line one can, and not only from an attacker — an ordinary second line reading "ignore the shown region" or "answer in plain text" is enough. **This episode decides how a multi-line request is delimited** so the model can tell the request from Point's own instructions, and what happens to a request containing the delimiter. F21 must NOT work around a missing B37 by flattening newlines before sending — that would ship the bug invisibly. See `../frontend/F21-multiline-fill-prompt.md`. **Scheduled after v1 ships, time permitting — otherwise deferred to a later session.** |
| B38 | Multi-turn explain: history, region re-read, token bound | P2 | no | B11, B24, B36 | not started — **spec only, the backend half of F22, and the larger half**. Three decisions, none of which exists today: (1) whether a follow-up replays the prior exchange as message history or as a summary; (2) whether `editor_context` is re-sent per turn — the region shown with answer 1 may not be the region on screen at question 3, which is the same pair-a-region-with-a-stale-read failure **B36** exists to prevent, one layer up; (3) the token bound and what happens at it. F15 already created the Editor-owned session these turns would accumulate in (lazily, because `SendMessage` requires one) and `chat\protocol.go` filters it out of Chat's list — so the session exists and nothing decides what goes in it. See `../frontend/F22-explain-as-conversation.md`. **Scheduled after v1 ships, time permitting — otherwise deferred to a later session.** |
| B39 | A generation mode for the Filler (no region to rewrite) | P2 | no | B24, B11, B36 | not started — **spec only, the backend half of F23, and the larger half of that episode**. Fill assumes a region exists: `resolve_scope` finds an enclosing scope or falls back to ±20 lines, the prompt asks for a `replacement` for `start_line`-`end_line`, and B24 rejects a range outside the shown region. On a blank line with no enclosing scope none of that describes the request. Owns: a THIRD `EditorContext.mode` (not a flag on fill — a flag means auditing every existing branch, which is how B11's `AgentConfig.background` retrofit trap worked); what the model is shown and how it is marked read-only; a generation instruction block that does **not** copy the `"<path>"` placeholder line (its own tracked bug, in the parking lot below and in `../frontend/F16-polish.md`); and **validation as an insertion point plus a length bound rather than a range inside a shown region — which must NOT be done by loosening the existing rewrite check**. The `(mtime, size)` pair is captured the same way B36 does, from the same descriptor as the read. See `../frontend/F23-fill-on-a-blank-line.md`. **Scheduled after v1 ships, time permitting — otherwise deferred to a later session.** |

**B1–B5 are frozen.** Additive changes only — new columns, new optional fields.
Never refactors.

**B6 and B7 are no longer batchable.** B6 grew a new `tasks` table, a `sessions`
rebuild, envelope v1, and six B1–B5 correctness fixes; one session cannot verify
both honestly. `F1+F2` is now the only batchable pair in the project.

**B1–B12 is v1, and it is built.** B12 closed on 2026-09-06; everything since
(B18–B23) has been a correctness or wire-surface follow-up found by a frontend
episode, not new v1 scope. The checkpoint this note used to describe -- "~3
weeks left → B15 + frontend, then B13, drop B16" -- is a scheduling call for
Aryan, not a fact about the code; it is left to `WORKFLOW.md`, which is where
session order actually lives.

## Current backend layout

**`CLAUDE.md`'s "Actual repo layout" is the one copy. Read it there.**

A second tree lived here and went stale exactly as you would expect a second
copy to: it still showed `backend\runtime.db` in the source tree (B6 moved it
to `%LOCALAPPDATA%\Point\`), still called `agent.py`, `memory.py` and
`tool.py` "EMPTY FILES" (B9 filled the first with `AgentConfig`, B11 filled the
second with `Proposal`, and B10 deleted the third), still said
`providers\__init__.py` was absent (B6 added it), and still said `agents\` did
not exist (B9 created it, B10 created `tools\`). Every one of those was
corrected in `CLAUDE.md` and not here, which is the argument for not having
this section at all.

## Additive changes to frozen episodes

Do these inside the episode that needs them, not as a separate pass.

| Episode | Change | Required by | Do it in |
|---|---|---|---|
| B3 | `provider` tag on reasoning/thinking blocks | D59 | B6 |
| B4 | normalized `effort` field + `supports_effort` capability | D58 | B6 |
| B4 | `MockProvider`, test-only, `POINT_MOCK=1` | D54 | B6 |
| B4 | 429 handling: backoff + jitter + `Retry-After` | D56 | B10 |
| B1/B5 | envelope v1 — `v`, `id`, `corr_id`, `connection_id`, `trace_id`, `seq`, nested `payload`, plus `hello` and `ping` | `project_idea.md` §4, SSH, F2 | B6 |
| B1 | cancel ack becomes non-terminal `task.cancel.accepted`, so the "exactly one terminal event" rule is true | `project_idea.md` §4 | B6 |
| B2 | `unarchive` event type + handler — `SessionManager.unarchive()` exists but is unreachable from Go | F3 | B6 |
| B1/B5 | `_handle_send_message` builds context + checks budget before creating the task; `_stream_chat_body` sends `BuiltContext.messages` instead of `session.messages + [user_message]` | B8's own verification — an oversized message must fail as `context_overflow`, on the `echo` path too | B8 |

**Struck from this table:** `session.archive` and `session.delete`. Both already
exist end to end — event types, registered handlers, manager methods, and the
`ON DELETE CASCADE` behind the repository. Verified on disk 2026-08-19.

B6 also folds in four small corrections that are not additive changes so much as
things B1–B5 left behind: the Gemini stream teardown, the doubled `outbox`
assignment, three dead `StreamEventType` members, and the `asyncio.gather` that
never returns on stdin EOF. See `B06-schema-bootstrap.md` §9.

## Windows notes for every episode

- All printed commands must be **PowerShell**. No `rm -rf`, no `~`, no `/tmp`.
- Temp paths: `$env:TEMP`. App data: `$env:LOCALAPPDATA\Point`.
- `sqlite3.exe` **is** on PATH, so direct DB inspection is allowed in checks —
  but prefer a CLI command when one exists, because that tests the app.
- `make` is installed but shells out to `cmd` on Windows. **Verification steps
  must not depend on `make`.**

---

## Parking lot

Format: `[ ] item — why parked · hook · anchor · what unparking takes`
`[✅] items - means it is written as an episode in above table and as a .md file.`

- [ ] **The Filler's fill prompt shows the model a placeholder it copies
      verbatim** — MEASURED during F15's gate work, against `qwen2.5:3b`
      through Ollama's own `/api/chat`, five trials per arm with
      byte-identical messages:

      | arm | accepted | verdicts |
      |---|---|---|
      | as today | **2/5** | `file_mismatch` ×3, ACCEPTED ×2 |
      | `+ format: "json"` | **0/5** | `file_mismatch` ×5 |

      The dominant failure is not malformed JSON. It is that the model
      returns `"file": "<path>"` -- the literal placeholder out of the
      instruction's own schema line in `_render_editor_context`
      (`managers\context.py`):

      ```
      {"file": "<path>", "start_line": <int>, "end_line": <int>, ...}
      ```

      It fills in the integers and echoes `<path>` as a string, and B24's
      parser then correctly rejects it as `file_mismatch`. A raw reply from
      each arm is in F15's completed doc.
      · why parked: the fix is a PROMPT change -- name the file, or say
      "use exactly this file name", instead of showing a placeholder -- and
      a prompt change has to land alone to be measurable, which is B24's
      own rule ("a prompt change and a new parser landing together make it
      impossible to tell which one the numbers describe"). F15 is a
      frontend episode and does not touch `managers\context.py`.
      · this supersedes the three options that were assumed before the
      measurement. **Ollama's `format: "json"` is not the cheap fix it
      looked like**: it did not help here and made it worse, which is
      consistent with constraining the decoder to valid JSON while leaving
      the model free to put the wrong STRING in a valid field. It also
      applies to the whole turn, so it would constrain explain-mode prose
      too unless the provider is told which mode it is in -- and the
      provider deliberately knows nothing about agents or modes
      (`managers\context.py`'s business). A more tolerant parser remains
      rejected by B24's own design ("never hunts embedded JSON").
      · not comparable to B24's own 0/5: that run measured a different
      failure (`end_line` reported as the replacement's length rather than
      the shown region's span, giving `range_outside_shown`) from a
      different cursor position. Both are the model's output format; they
      are not the same defect, and a fix for one will not show up in the
      other's number.
      · hook: `backend\managers\context.py`'s fill-mode `instructions`
      string.
      · anchor: whichever episode first needs the fill path to work against
      a REAL model rather than Demo's fixture. F15 does not -- it ships a
      deterministic 5-line fixture and a 46-line one
      (`POINT_DEMO_LONG_FILL=1`).
      · what unparking takes: the prompt edit, alone, plus a before/after
      against the same model and the same prompt. The arms above are
      reproducible; a change claimed to improve compliance without a number
      is not worth making.

- [ ] **`manual_b28_demo_mode.py` asserts a `FillProposal` event does NOT
      exist** — the assertion is `"no FillProposal event exists on the wire
      -- none was emitted, by construction"`, and the event is on the wire
      now, so step #8 of that script fails. Observed while running the
      script during F15's work; NOT caused by F15 and not caused by F15's
      demo-provider changes.

      The dates settle it: the test file is at its committed state from
      B28 (`fa6e88c`, `git status --porcelain` on it is empty), and
      `FillProposal` was added later by B24 (`ab1743d`), which never came
      back to update B28's assertion. So this has been failing since B24.
      The event is CORRECT -- it is what F15 consumes -- and the assertion
      is what is stale.
      · why parked: B28's test is not F15's scope (F15 is a frontend
      episode), and the fix is a judgement call someone should make
      deliberately rather than in passing: either the assertion becomes
      "a FillProposal IS emitted, after TaskDone" (which turns a stale
      negative into a real check of B24's ordering rule, and is the more
      useful test), or step #8 drops the clause and says nothing about the
      event at all. Changing it mid-episode would also mean an episode's
      diff touching another episode's verification script.
      · hook: `backend\tests\manual_b28_demo_mode.py`, step #8
      (`@filler-explainer fill mode -- plain reply, no proposal event, no
      apply path`), including its heading, which makes the same claim.
      · anchor: whichever episode next needs `manual_b28_demo_mode.py` to
      pass end to end, or the next backend episode that touches the
      proposal path.
      · what unparking takes: one of the two edits above and a re-run of
      the whole script -- the other eight steps pass today, so a green run
      is the evidence. If the assertion is rewritten as a positive, it
      should check the ORDER (`FillProposal` after `TaskDone`), which is
      B24's own invariant and the one thing the current wording gets
      backwards.

- [✅] **RESOLVED — `CreateSession`/`SetSessionProvider` accept a `project_id`
      matching no `projects` row** — built and manually verified as B31; see
      `design/completed/backend/B31-validate-project-id.md` for the full
      evidence (all seven Done-when items confirmed, including independent
      log-file verification). why parked: found live during F10c/B30's own
      verification (`design/completed/frontend/F10c-settings-demo-mode.md`'s
      Decision 1 correction), one finding among several, not either episode's
      scope to fix (F10c's own "no backend changes" rule; B30 was already
      scoped narrowly to one optional field). Repro: select Demo mode (binds a
      session to the shipped sample project), delete `runtime.db*` only
      (leave `state.json` alone), run `cli.py migrate`, relaunch. The session
      list comes back empty, `onSessionList` fires `CreateSession` with the
      `project_id` still stored in `state.json`, and the backend accepts it
      even though the fresh database's `projects` table is empty — no error,
      no validation, the app loads normally with a session
      (`provider=demo`, `project_id=b93dbd75-...`) whose `project_id`
      matches no row. Confirmed: `database\migrations.py`'s `sessions.
      project_id` column (lines 58, 109) has no `FOREIGN KEY`/`REFERENCES`
      constraint — every other cross-table id in that schema that needs one
      has one (e.g. `session_id TEXT NOT NULL REFERENCES sessions(id) ON
      DELETE CASCADE` at lines 74, 145, 223, 324); `sessions.project_id` is
      the one exception. Both call sites that write it pass the value
      straight through with no existence check: `runtime.py:374-386`
      (`_handle_create_session`) and `runtime.py:631-642`
      (`_handle_set_session_provider`, B30's own handler). Hook: none — no
      migration or interface already anticipates this; it is a plain gap.
      Anchor: whichever backend episode next touches `CreateSession` or
      `SetSessionProvider`, or B17 (Hardening). Resolved by B31: an unknown
      `project_id` is never refused outright — `CreateSession` treats it as
      no project (nulled, logged); `SetSessionProvider` leaves whatever
      binding already existed untouched (logged) — via a new, narrow,
      side-effect-free `ProjectManager.exists()` rather than reusing
      `resolve()` (which touches/writes on a hit). See
      `design/completed/backend/B31-validate-project-id.md` for the full
      decision, build, and verification evidence. A database-level
      `FOREIGN KEY`/`REFERENCES` constraint remains a separate, still-open
      idea (B31's own Handoff), not folded into it.
      Note: the dangling id in the repro above is
      self-healing if Demo is *reselected* (`ProjectManager.init()`'s marker
      file at `backend\demo\sample_project\.point\project.json` recreates
      the `projects` row under the same id — `project_manager.py:120-145`,
      confirmed by reading the code, not by a live repro) — a mere relaunch
      does not trigger this, since `onSessionList`'s `CreateSession` path
      never calls `ProjectInit` itself.
- [ ] **Semantic retrieval (embeddings)** — keyword + tags closes most of the gap
      for zero cost. Hook: nullable `embedding` BLOB (B6), retrieval behind one
      interface. Anchor: B13. Unpark: embed model, vector search, backfill.
- [ ] **Automatic per-turn retrieval** — a per-turn token tax with no evidence it
      earns its cost. Hook: retrieval interface, called manually. Anchor: B13.
- [ ] **Session summarization** — needs observed long-session failures. Anchor: B13.
- [ ] **Human-in-the-loop approvals** — the diff card covers the v1 case. Hook:
      `PAUSED` / `AWAITING_APPROVAL` reserved in the task state enum and
      `approval.*` reserved in the event namespace (B6). Anchor: B13.
- [ ] **MCP** — a `ToolProvider` behind the B10 registry. No hook needed.
      Anchor: B10.
- [ ] **RAG** — depends on embeddings. Anchor: after B13.
- [ ] **Prompt caching** — the biggest cost lever, provider-specific. Hook:
      `supports_caching` capability. Anchor: B14.
- [ ] **Guardrails / eval** — hook: no-op middleware chain in B9. Anchor: B17.
- [ ] **Tracing** — hook: `trace_id` in the envelope, propagated. Anchor: B17.
- [ ] **Coding Assistant capability** — multi-file, creates files, **writes
      directly**, runs allowlisted commands. Gated on git (D80): clean repo, a
      checkpoint commit before the run, `git restore` to revert. Three approval
      modes in config (D82) — `ask` / `accept_edits` (writes free, commands still
      ask) / `accept_all`. Per-edit diffs stream informationally during the run.
      **One commit per step**, so the end-of-run review reverts by STEP — always
      consistent — with per-file revert available but warned (D83, D83b).
      Hooks, none of which exist yet — **B6 creates all three**: the `tasks`
      table itself (tasks are in-memory only today) carrying
      `git_checkpoint_sha`, and the `PAUSED` / `AWAITING_APPROVAL` task states.
      Then B7's path guard and B10's tool registry. Anchor: post-v1. Unpark: one
      full episode, written first.
      **⚠ Also read the "Dynamic agents" entry above before unparking this**:
      the Coding Assistant is a new built-in agent, and the same
      `max_steps > 1` inline/background heuristic (B11) applies to it.
- [ ] **Data Analysis capability** — only if `--advanced` usage demands a
      non-transcript surface (D72). Anchor: post-v1.
- [ ] **Dynamic agents (user-created)** — hook: `agents` table with `builtin`
      flag (B6), registry reads DB (B9). Anchor: B9 + a frontend episode.
      **⚠ Read before unparking (B11):** `AgentConfig` has no `background: bool`
      field. `runtime.py`'s `_handle_send_message` decides inline-vs-background
      (and whether to build Research's memory-first reference index) from the
      heuristic `agent.max_steps > 1`, because that was cheaper than a migration
      when only one agent (`research`) needed it. **A new agent that wants
      `max_steps > 1` while running inline, or background execution with
      `max_steps == 1`, is silently misclassified by this heuristic — no error,
      just the wrong `inline`/reference-index behavior.** Give `AgentConfig` a
      real `background: bool` column (a migration) before or alongside adding
      such an agent, and update `_handle_send_message` to read it instead of
      inferring. See `B11-three-agents.md`'s Handoff for the full account.
- [ ] **Dynamic capabilities (declarative TOML layouts, Tier 2)** — hook:
      data-driven components from F3. Anchor: post-v1.
- [✅] **Message pagination / lazy scrollback loading** — see
      `design/tasks/backend/B18-message-pagination.md` (written).
      F3 assumed a `session.messages(before, limit)` query that does
      not exist; `GetSession` fetches the full unpaginated message list via
      `SessionRepository.fetch_messages`. F3 caps rendering at 500 messages
      client-side as the interim stopgap. Anchor: post-v1, or sooner if a real
      session grows large enough for the full-fetch cost to be felt.
- [ ] **Shared-runtime SSH (topology C) and live co-editing** — needs
      connection-scoped identity on every event, plus CRDTs. Anchor: post-v1.
- [ ] **ClaudeCodeProvider (Agent SDK via Claude Code CLI)** —
      **DO NOT IMPLEMENT. Owner-only. Ask Aryan explicitly before touching this
      entry; do not begin work on it if it comes up.** Bypasses the Context
      Builder and tool system; ToS forbids exposing it to other users;
      credit-limited. Anchor: B4.
- [ ] **Auth beyond SSH keys, billing, telemetry, i18n, auto-update, plugin
      sandboxing** — rejected for v1 and v2.
- [ ] **Local-model capability tiers and their effect on multi-step agent
      reliability** — B11 tried, four separate times, to get a definitive
      answer on whether Research's memory-first reference index actually
      changes model behaviour (not just whether it's wired in — that part is
      proven). Every attempt against `qwen2.5:1.5b` was confounded by the
      same underlying trait: unreliable multi-step tool orchestration,
      manifesting a different way each time (the Filler's two-file refusal,
      a Bloom-filter recall test the model didn't need any memory to answer,
      an essay that spiralled into an unrelated ramble, a malformed-path/
      hallucinated-tool-error cascade). Not chased further in B11 on purpose
      — a bigger local model (`qwen2.5:3b`, `qwen2.5-coder:3b`, both already
      pulled) might behave differently, but that is a model-capability
      question, not a B11 architecture question, and answering it needs new
      CLI surface (`send --provider/--model`) this episode didn't otherwise
      need. Anchor: whichever episode first needs to reason about "is this
      agent's behaviour good enough" as a product question, not an
      architecture one — plausibly B14 (Usage & Limits, where model choice
      already has to be surfaced) or a frontend settings episode. See
      `completed/backend/B11-three-agents.md`'s "Documented, not resolved"
      section for the full account of what was tried.

## Rejected outright

| Item | Why |
|---|---|
| Blog Writer agent | prompt wrapper; no structural difference |
| Trading workspace | out of domain for a developer tool, permanently |
| ML/GPU agent | existed to signal, not to solve; split into its own project |
| Real PTY terminal | `tea.ExecProcess` covers it; VT100 + ConPTY is weeks |
| Docker / Kubernetes | needs a TTY, filesystem, host Ollama; slower, breaks SSH |
| ADK / A2A | would absorb the layers built from scratch on purpose |
