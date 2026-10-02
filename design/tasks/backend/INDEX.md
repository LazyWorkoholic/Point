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
