# Point — Project Idea

> The architecture and the reasoning behind it. No episode lists here
> (see `tasks/`), no completion claims (see `completed/`), no decision
> rationale (see `decisions.md`).

---

## 1. What Point is

A local-first AI workspace that lives in the terminal.

```
Go + Bubble Tea   terminal UI, workspaces, rendering, input
Python            runtime, agents, providers, memory, tools
SQLite            sessions, messages, tasks, memory, usage
stdin/stdout      JSON-lines protocol between them
```

**One sentence:** the UI is a workspace, the runtime is a separate execution
system, agents are capabilities, tasks are executions, providers are model
adapters, and memory is persistent context.

### What it is not

Not a VS Code replacement. Not a hosted service. Not a product with a business
model. It is a tool its author uses daily, and a system whose design can be
defended in depth.

### Design principle

Every feature must justify its existence: what problem it solves, whether it is
useful in a real workflow, what it costs in complexity and runtime, and whether
it should exist at all. A feature that exists because another tool has it, or
because it demos well, is rejected. This principle is applied to Point's own
feature list — the ML/GPU agent and the Trading workspace were both cut by it.

---

## 2. Domain concepts

These names mean exactly one thing throughout the codebase.

| Concept | Definition | Owns |
|---|---|---|
| **Runtime** | Execution engine. Creates and supervises tasks. | Task lifecycle, concurrency |
| **Task** | One concrete execution. Has a lifecycle and an ID. | Its own state and events |
| **Session** | A persistent conversation identity. | Message history, title |
| **Message** | The common language between everything. | Role, content, metadata |
| **Agent** | A *capability*. Stateless configuration. | Nothing persistent |
| **Provider** | An adapter for one LLM service. | API translation only |
| **Tool** | An external capability exposed to agents. | One operation |
| **Memory** | Durable knowledge, scoped and reviewed. | Facts, not conversations |
| **Context** | What is assembled for *this* request. | Nothing — it is computed |
| **Workspace** | A UI environment. | Its own view state |
| **Capability** | An agent + optionally a dedicated workspace. | — |
| **Project** | A directory on disk with a `.point/` marker. | Project-scoped data |

### Distinctions that matter

```
Agent = capability          Task = execution
Session = conversation      Message = one event in it
Memory = durable knowledge  Context = this turn's assembled window
Config = declarative, user-owned, authoritative
Memory = derived, system-written, revisable, needs provenance
```

**Agents do not have memory.** A session owns its history; an agent owns a
*policy* for using it. Every message goes through an agent — "general chat" is
simply the agent with everything switched off. This is one execution path, not
a special case.

---

## 3. Architecture

```
                            USER
                              |
                    GO / BUBBLE TEA
                              |
              Root Model ── Layout Engine ── Action Registry
                              |
        +---------------------+---------------------+
        |                     |                     |
   Chat Workspace      Editor Workspace     (future capabilities)
        |                     |                     |
        +---------------------+---------------------+
                              |
                     Backend Client
                    (transport, envelope,
                     reader goroutine)
                              |
                 ══ stdin / stdout, JSON lines ══
                              |
                      PYTHON RUNTIME
                              |
        +---------------------+---------------------+
        |          |          |          |          |
     Runtime    Sessions   Messages   Context    Providers
        |          |          |       Builder        |
      Tasks        |          |          |      Ollama/OpenAI/
        |          |          |       Agents    Claude/Gemini/
      Events       |          |          |      OpenRouter
        |          |          |        Tools
        +----------+----------+----------+
                              |
                        Repositories
                              |
                          SQLite (WAL)
```

### Boundary rules

- Go **never** calls an LLM API, implements agent reasoning, or contains SQL.
- Python **never** renders, positions, or knows terminal dimensions.
- Workspaces never talk to each other — they emit, the root routes.
- Agents never own providers, storage, task queues, or file writes.
- All filesystem access passes through the project path guard.

---

## 4. The Go↔Python contract

One envelope, both directions, newline-delimited JSON.

```json
{ "v": 1, "id": "...", "type": "task.create", "corr_id": "...",
  "session_id": "...", "task_id": "...", "connection_id": "...",
  "ts": "...", "payload": {} }
```

Three message classes:

```
COMMAND   Go → Python, exactly one ack        task.create, session.rename
QUERY     Go → Python, exactly one result     session.list, memory.search
EVENT     Python → Go, zero or many           task.token, task.completed
```

Rules:

1. `corr_id` carries the causing request's `id` — that is how Go routes events.
2. Per-task `seq` numbers, so a gap is detectable rather than silently corrupting.
3. **Exactly one terminal event per task** — `completed`, `failed`, or `cancelled`.
   Never zero, never two. The UI can therefore trust that a spinner always stops.
4. Errors are events (`{code, message, retryable}`), never crashes.
5. `hello` handshake on startup carries `v` and `capabilities`; version mismatch
   refuses to proceed with both versions shown.
6. `ping` every 5s while idle. Two missed pings = the crash-recovery path, which
   also catches a *hung* backend that process-exit detection would miss.

**Transport discipline:** logs to stderr only; a dedicated reader goroutine feeds a
bounded channel; token events may be dropped under backpressure, lifecycle events
never may.

---

## 5. Context — the centre of the system

Storage is the easy half. The hard half is deciding what enters a finite window
on *this* turn.

```
build_context(request, agent_config, budget) -> messages

    system prompt              always
  + attachments                resolved from references, re-read only if changed
  + retrieved memory           manual-attach in v1; automatic is parked
  + session summary            only once a session is long
  + last N messages            fills the remaining budget
  + task working state         only while a task is running

eviction order is fixed and logged
```

**Everything is a reference, resolved at build time.** Attachments store a path or
an ID, never a copy — so editing a memory updates every place it appears, and a
changed file is re-sent only when it actually changed (mtime + size).

The same pattern appears three times: attached files, saved memories, and past
task results. Reference-then-load, with an explicit cost shown before paying it.

### Why this matters

API billing charges for the entire conversation on every request, so a long
session costs roughly quadratically. Token budgeting, reference mode, and
change-detection are all mitigations for that single billing mechanic.

---

## 6. Memory

Five things wear the word "memory". They are separated by *who writes them* and
*when they are read*:

| | Written by | Read when | Lives |
|---|---|---|---|
| **Config** | the user, explicitly | at startup / on change | `config.toml` |
| **Session memory** | the system, automatically | every turn of that session | DB, per session |
| **Project memory** | the system, on promotion | when retrieval says so | DB, per project |
| **Global memory** | the system, on promotion | nearly every turn (it's small) | DB, unscoped |
| **Task working state** | the running task | during that task only | RAM, dies at end |

### Storage

```
memory_records   id, content, tags, status, source, agent_id,
                 origin_session_id, origin_task_id, embedding (nullable), created_at
memory_scopes    memory_id, scope (session|project|global), scope_id
```

One record, many scope bindings — so saving to both session and project is one
row, deduplicated at retrieval, edited in one place.

- `status`: `pending | accepted | rejected`. **Retrieval reads `accepted` only.**
- `source`: `auto_extractor | user_card | user_manual` — provenance is always visible.
- Precedence when scopes conflict: **session > project > global**.

### Why user-gated writes

An agent silently persisting a wrong inference is unrecoverable: it poisons every
future retrieval and the user never learns why answers degraded. So the extractor
proposes; the user accepts. Proposals are asynchronous and batched (2–3 per
session, not per message) so nothing blocks and approval fatigue does not set in.

Agents carry `agent_id` on what they produce, for provenance and filtering —
never for retrieval.

---

## 7. Agents

Three in v1. They differ **structurally**, not by prompt.

```
                General      Filler-Explainer     Research
tools           —            —                    web, fs, load_task_result
reads project   no           explainer mode only  yes
writes memory   no           no                   proposals only
runs as         inline       depends on origin    background always
multi-step      no           no                   yes, bounded
default attach  —            —                    reference
context         STM          STM + EditorContext  STM + project + tool output
```

One `AgentConfig` struct, zero subclasses. That table *is* the proof the
policy-as-config design does real work.

### Filler-Explainer — one agent, two modes

- **Explainer**: explains selection → enclosing function → block → file. Reads
  project scope. Stateless. Project-wide context only on explicit request, and
  then as a file list, not contents.
- **Filler**: writes code at a location. One file, one region. Never creates
  files, never runs commands, never touches two files — it refuses and says so.

Invocation site decides presentation: from chat, a background task and a result
card; from the editor cursor, an inline diff. Same agent, same config.

### Research

The only structurally complex agent, and the reason the rest of the architecture
exists — it takes 60+ seconds, so it *must* be a background task, which forces
the task panel, result cards, attach modes, and cancellation to all genuinely
work. Bounded by config (`max_steps`, `max_tool_calls`, `timeout`) with the step
count visible on the card, because unbounded agent loops burning tokens with no
visible progress is the standard failure of this category.

**Memory-first:** it searches stored memory before the web, sees the age of what
it finds, and decides whether to re-research. No LTM subsystem — a tool and
`created_at` arithmetic.

### Diffs, always

Agents never write to disk in any phase. The model returns the **rewritten
region**; Go computes the diff locally (models miscount line numbers, so a
model-authored patch often will not apply). The card offers `⏎ apply · e edit ·
d discard`, and `e` opens the *proposed result* in a scratch buffer — editing a
new version is typing; editing a patch is punishment.

---

## 8. Workspaces and lifecycle

One `tea.Program`, one root model, nested workspace models behind an interface.

```go
type Workspace interface {
    Init() tea.Cmd
    Update(tea.Msg) (Workspace, tea.Cmd)
    View(Rect) string
    Suspend() ([]byte, error)
    Resume([]byte) error
    ID() WorkspaceID
}
```

The lifecycle falls out of the data structure:

```
NOT_LOADED         key absent from the workspaces map
LOAD               construct + Init()
UNLOAD             Suspend() → store ~2KB → delete key → GC frees the rest
AUTO-UNLOAD        tea.Tick comparing lastActive against a configured idle timeout
QUIT               same code path, wider scope
```

**Unloading a view never cancels a task.** The task is in another process; it
cannot be touched by a UI decision. A view with a running task *can* be unloaded,
and its tasks remain visible from every workspace, because the **root** owns the
task list.

**Resume rehydrates from the database**, not from what the view remembered.
`Suspend()` keeps only position, draft text, open files, cursor — a couple of
kilobytes, written to `state.json` on a debounce so a crash does not eat an
unsent draft. Rebuilding from the DB is the same path as opening a week-old
session and the same path as recovering from a crash: one code path, constantly
exercised, so it cannot silently rot.

Two rules that keep it from rotting: the root never reads a workspace's internal
fields, and workspaces never talk to each other directly.

### Backend-side unload

Separate mechanism, keyed by workspace: drop file trees, open buffers, symbol
caches, cached project memory, provider clients. **The backend refuses to free
anything a running task holds a reference to.**

Honest caveat: CPython does not reliably return freed memory to the OS, so RSS
may not drop after an unload. The measurable and truthful claim is *bounded
growth* — stable object counts and cache sizes across repeated load/unload
cycles, verified with `tracemalloc`.

---

## 9. The frontend

### Layout engine

One pure function decides everything:

```go
func Compute(w, h int, s State) Layout   // returns rectangles
```

**A component never hides itself.** Components render into whatever rect they are
handed. Breakpoints therefore live in one place, are unit-testable without a
terminal, and are cheap to change later.

```
WIDTH   ≥120 full · 90–119 narrower right pane · 70–89 right pane auto-hides
        <70 single column, overlays refuse · <50 "too narrow"
HEIGHT  ≥30 header + animation · 24–29 title bar only
        <24 header gone, dim corner watermark · <12 "too short"
```

Overlays declare `MinSize()`; the root refuses to open one that does not fit and
shows the actual numbers (`memory needs 60×16 — terminal is 48×20`).

### Action registry

Every action is registered once with an ID, name, description, optional leader
key, optional chord, and an availability predicate. The **command palette, leader
hints, help screen, and config rebinding all read from that one registry** —
adding a feature is one entry, not edits in four files.

```
Leader (Ctrl+W …)  regular navigation
Direct chords      a handful of high-frequency actions
Command palette    everything, with descriptions, always reachable
```

The palette is the fallback when a terminal swallows a chord, when the window is
too small for hints, and when someone has never used Point before.

### Component vocabulary

`transcript · input · list · table · editor · terminal · status · tasks · tree ·
markdown`

Every component is **data-driven from birth** — it renders a source it is handed,
never reaching into specific global state. This is what later makes declarative
user-defined layouts possible; it cannot be retrofitted cheaply.

Two shared primitives cover most of the UI: `Popup` (tab-scoped, filterable list)
and `Confirm`. The palette, memory browser, settings, `@` picker, task tray and
help screen are all instances of the first; every destructive action routes
through the second.

### Chat behaviour

- **Two lanes.** A *turn* is sequential and streams inline. A *task* is
  concurrent, runs in the right pane, and reserves its slot in the transcript at
  send time so nothing ever jumps.
- **Queue, don't lock.** A message sent while a reply streams becomes an editable
  pending bubble; depth 1 by default, and only when the queue is full does the
  input lock — with the reason shown.
- **Mode chip** in the input shows what Enter will do before you press it; the
  border colour changes in agent mode.
- **Input growth** overlays the viewport *and auto-scrolls it*, so the newest text
  stays visible above the input, with no re-wrap of the transcript.
- **Enter sends. Alt+Enter inserts a newline.** (`Ctrl+Enter` is not portable —
  most terminals cannot distinguish it from `Enter`.)
- The input is a real editor: cursor movement, selection, mid-text editing,
  multi-line, bracketed paste as one draft.

### Theme

~20 semantic roles; components reference roles, never raw colours. Four palettes
(turquoise default, butterfly, green, gold). Background transparent by default —
the terminal's own background shows through, so the palette applies to text,
borders and accents only. Three depths always: dim structure, primary text, one
accent per screen. Error and warning are fixed colours **outside** the palette,
because a gold error is not an error. 256-colour fallback and `NO_COLOR` honoured.

---

## 10. Projects, config, secrets

**A project is a directory** marked by `.point/project.json` containing a
generated `project_id`. A registry maps ID → last known path, so moving or
renaming the folder does not orphan its memories. No `.point/` means no project
scope, and project-scoped UI is greyed out rather than silently doing nothing.
`.point/` is gitignored by default (a config flag shares it).

```
config.toml        user-owned. Comments preserved. Point never rewrites it.
credentials.toml   0600, gitignored, temp-file-then-rename. Keys only.
state.json         machine-written: last workspace, drafts, positions.
```

Keys resolve **env var → credentials.toml → prompt**, and are masked everywhere —
logs, errors, UI. `config.toml` holds only non-secret settings, so it is safe to
share, commit, or screenshot.

**Model/provider/effort resolution:** task override > session override > agent
default > global config. Effort is normalized (`off|low|medium|high`) and each
adapter translates or ignores it, with a `supports_effort` capability so the UI
greys out what a provider cannot do. Reasoning blocks are filtered per provider
on send — never deleted, always still rendered.

---

## 11. First run

Zero-config boot. Ollama is **detected silently but selected explicitly** — a
local model is a commitment of the user's CPU, GPU and battery, and detection is
not consent. If nothing is configured, Point still runs: the input is disabled
with the reason and the fix shown inline, and everything else stays explorable.
Credentials are requested at the moment a cloud provider is first chosen, then
validated immediately with `list_models()` so a mistyped key fails now rather
than inside a task ten minutes later.

`point doctor` prints versions, backend reachability, provider credentials,
Ollama status, DB path and schema version, config paths, and permissions.

---

## 12. SSH

**Topology B:** each connection gets its own TUI and its own Python runtime;
they share one SQLite database in WAL mode. That yields most of the *value* of
collaboration — shared session list, shared project memory, visible presence —
without live shared execution, which would require connection-scoped identity on
every runtime event and a permission model.

Identity comes free: SSH public-key fingerprints mapped to names and roles in
config. Unknown key, rejected at connect. No passwords, no account system.

An SSH session is a **restricted** session, enforced in the backend:

```
                    LOCAL          GUEST
terminal pane       yes            no
ExecProcess hatch   yes            no
file access         anywhere       project root, jailed
editor write        yes            configurable, default read-only
token budget        unlimited      hard daily cap on the owner's keys
memory writes       yes            proposals only, never global
```

Keys are hybrid: Ollama free for everyone, the guest's own key if they add one,
otherwise the owner's key up to a cap that **refuses before spending** but never
kills a task already running. A guest can reverse-tunnel their own Ollama
(`ssh -R`) so inference runs on their hardware.

Concurrent connections are capped, because each runtime is 150–300MB before any
model loads.

**Soft file locks** (v1): opening a file for edit takes a heartbeat-backed lock;
a second person sees "aryan is editing — [read-only] [take over]". Plus an mtime
check on save that refuses to clobber a file changed since it was opened — which
also protects you from your own second terminal.

---

## 13. What is deliberately not here

Cut, with the reason:

- **Trading and Data agents as workspaces** — a capability earns a workspace when
  its primary surface is not a transcript. Trading is out of domain for a
  developer tool and stays an agent permanently. Data is the only candidate, and
  waits for evidence of real use.
- **ML/GPU agent** — it existed to signal capability, not to solve a workflow
  problem. Split into its own project.
- **Blog Writer agent** — a prompt wrapper. Three structurally different agents
  demonstrate more than six that differ only by system prompt.
- **Real PTY terminal** — `tea.ExecProcess` gives full interactivity for free by
  suspending the TUI and handing over the real terminal. A VT100 emulator would
  be weeks of work, doubled by Windows/ConPTY.
- **Live co-editing** — CRDTs. Months. Parked, revisited after v1.
- **Docker/Kubernetes** — Point is one local process needing a TTY, the
  filesystem, and the host's Ollama. Containerizing it makes startup *slower* and
  breaks SSH.
- **Telemetry, i18n, auto-update, plugin sandboxing** — real work, no payoff here.

Everything else deferred lives in the parking lots at the end of each task index,
each with a hook, an anchor episode, and what it would take to unpark it.

---

## 14. Evolution

The architecture is meant to be grown, not rewritten:

```
1  single process pair (Go + Python)          ← now
2  modular internal components                 ← the episode structure
3  background workers                          ← already true, tasks
4  process isolation where useful              ← SSH, per-connection runtimes
5  optional distributed execution              ← not needed, not made impossible
```

The insertion rule for anything new:

```
Does it change an existing CONTRACT?  (event schema · DB schema · an interface)
   NO  → add it any time. Zero cost to defer.
   YES → schedule it, or add the column/field now and the logic later.
```

This is why the schema episode front-loads columns that stay unused for months:
schema changes are the expensive ones; forms and logic are not.
