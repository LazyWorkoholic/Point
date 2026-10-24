# Point — Decisions

Every non-obvious decision, what was considered, and why. This is the file to
re-read before an interview: it is the difference between "I built it" and
"I chose this over that, for this reason, and it cost me that."

Format: **decision** · alternatives · why · what it costs.

---

## Process & architecture

### D1 — Go frontend, Python backend, separate processes
**Alternatives:** single Python app with a Python TUI; single Go app.
**Why:** Go's concurrency and TUI ecosystem (Bubble Tea) are the best available;
Python has the provider SDKs and is faster to iterate on for agent logic. Process
separation means a backend crash cannot take the UI with it.
**Cost:** an IPC layer, a packaging problem, and version-skew risk. Honest note:
**Go-only would also have worked** and would have removed all three — it is a
serious alternative, not a strawman, and is treated as such in the book.

### D2 — stdin/stdout JSON lines, not gRPC or a socket
**Alternatives:** gRPC; Unix socket; HTTP; shared memory.
**Why:** the backend is a child process; pipes need no ports, no service
discovery, no cleanup on crash, no auth surface. SSH uses one runtime per
connection, which fits pipes exactly. gRPC would add a schema compiler and a
network stack for a link that is neither networked nor a bottleneck.
**Cost:** no multiplexing to a shared daemon. Mitigated by putting it behind a
`Transport` interface so a socket is a drop-in later.

### D3 — Freeze backend Episodes 1–5, rework only what is unbuilt
**Alternatives:** redesign everything before continuing.
**Why:** the built layers work and are manually tested. Redesign before anything
is user-visible is how solo projects die with beautiful docs and no product.
**Cost:** some decisions inherited rather than chosen. Additive changes only.

### D4 — Frontend can start immediately
**Why:** F1–F5 need only B1–B5, which exist. Point currently has a working brain
and no body; nothing is demonstrable until a token appears on screen.

---

## Protocol

### D5 — One envelope, three message classes
Commands get a fast ack then a stream of events; queries get one result. Waiting
for task completion on a command would make cancellation impossible.

### D6 — `corr_id` on every event
Without it, routing a token to the right transcript is guesswork the moment two
things run at once.

### D7 — Per-task sequence numbers
A gap is then *detectable* rather than silently rendering corrupted text.

### D8 — Exactly one terminal event per task
Never zero, never two. This is what lets the UI trust that a spinner always
stops — a guarantee, not a hope.

### D9 — Errors as events, never exceptions across the boundary
Go should never see a crash where it expected a token. `{code, message, retryable}`.

### D10 — Handshake with version + capabilities
Version mismatch refuses to start with both versions shown. `capabilities` lets
the UI grey out features a backend build lacks, so the frontend can ship ahead of
the backend without lying to the user.

### D11 — 5s heartbeat
Process-exit detection misses a *hung* backend. Two missed pings routes to the
same recovery path.

### D12 — Bounded channel; drop tokens, never lifecycle events
Under backpressure, dropped intermediate tokens are invisible (the accumulated
string re-renders); a dropped `completed` is a permanent spinner.

### D13 — Logs to stderr only
One stray `print()` on stdout corrupts the protocol and produces a parse error
that looks like nothing else.

### D14 — Dedicated reader goroutine
Reading on the render goroutine lets a slow render fill the OS pipe buffer,
blocking Python's write and freezing the backend.

---

## Context & memory

### D15 — The Context Builder is a first-class component
**Why:** storage is the easy half; deciding what enters a finite window is what
determines answer quality, and no other tool exposes it. Explicit budget, fixed
eviction order, and a log of what went in and what was dropped.

### D16 — Build agents before the real memory system
**Alternatives:** the original plan (memory first).
**Why:** designing summarization and promotion against imagined conversations
means paying for it twice. A deliberately dumb context (last N messages) ships
first; the memory system is then built against *observed* failures, each with a
story attached.
**Cost:** some throwaway code — small, since the interface stays and only the
body changes.

### D17 — One `memory_records` table with `status` + `source`
**Alternatives:** separate `proposals` and `memories` tables.
**Why:** two tables means duplicating query, delete, and audit logic forever, and
being unable to answer "was this vetted, and by whom?".
**Cost:** one extra column. Retrieval reads `accepted` only — one WHERE clause,
and it is the guardrail against memory poisoning.

### D18 — Separate `memory_scopes` binding table
Saving a fact to session *and* project must not create two rows; duplicates get
injected twice (paid twice) and drift when edited.

### D19 — Memory writes are user-gated
An agent silently persisting a wrong inference is unrecoverable — it poisons
every future retrieval invisibly. Proposals are async and batched (2–3 per
session) so nothing blocks and approval fatigue does not set in. Escape hatch:
per-project `auto_accept`.

### D20 — Manual retrieval in v1; automatic behind a flag
Automatic retrieval is a per-turn token tax. Opt-in until there is evidence it
earns its cost.

### D21 — Keyword + LLM-generated tags, not embeddings
Tags at write time close most of the semantic gap for near-zero cost. A nullable
`embedding` column exists so the upgrade touches one file.

### D22 — Agents have no memory of their own
They carry `agent_id` on what they produce, for provenance. "The Research agent's
memory" is a query, not a place — which makes policy a config flag and deletion
a single row.

### D23 — Config is not memory
Config is declarative and authoritative; memory is derived, revisable, and needs
provenance. Shared UI, separate stores — otherwise "forget this" could delete an
API key.

### D24 — Attachments store references, resolved at build time
A copy goes stale when the source is edited. References mean one truth and make
per-turn re-resolution free.

### D25 — Files re-sent only when changed (mtime + size)
An attached file is part of the message it was attached to, so it is already in
history. Re-sending every turn is waste; never re-sending is a correctness bug —
the model answers about code that no longer exists. So: resend on change, and
show the cost on the chip before paying it.

### D26 — Three attach modes; reference is the default
A 4000-token research dump entering every subsequent turn destroys a local
model's context window.

---

## Agents

### D27 — Three agents that differ structurally
**Alternatives:** the original six.
**Why:** six agents differing only by system prompt demonstrate nothing an
interviewer will respect. Three that differ in tools, scopes, and execution shape
prove the policy-as-config design does real work.

### D28 — Filler-Explainer is one agent, two modes
Same config, same tools; the *invocation site* decides presentation (card from
chat, inline diff from the editor cursor).

### D29 — Agents never write to disk *without a recovery path* (REVISED)
**Original rule:** agents never write to disk, in any phase.
**Revised:** the rule was right for the Filler and wrong for the Coding Assistant.
It over-generalized one policy across two very different things.

The real gate is not "does the agent write" but **"is every write reversible in
one command"**:

```
no git    a bad write is unrecoverable        → per-edit approval is required
git       every write is a diff you can throw → approval moves to the END
```

So:
- **Filler** — one region, in a file already open in a buffer. Ghost text while
  streaming, then a diff card, applied into the **unsaved buffer** so `Ctrl+Z`
  undoes it. No git needed. Unchanged from the original spec.
- **Coding Assistant** (v2) — **writes files directly**, gated on a git repo and
  a checkpoint commit taken before the run. See D80.

A Coding Assistant that needs approval per region is not an assistant; it is
autocomplete with extra steps. Codex and Claude Code both write directly, and
they are usable precisely because of that.

### D30 — Model returns the region, Go computes the diff
Models miscount line numbers and emit patches that will not apply. Also makes
"edit before applying" trivial — you edit the proposed result, not a patch.

### D31 — A hard boundary between Filler and Coding Assistant
One file / never creates / never runs commands / you point at the spot. If a
request would span two files it refuses and says so. The refusal is the feature.

### D32 — Research is always a background task, with bounds
It takes 60+ seconds, so it forces the task panel, cards, and cancellation to
genuinely work. `max_steps`/`max_tool_calls`/`timeout` with the step count
visible, because unbounded loops burning tokens with no visible progress is how
this category fails.

### D33 — Research searches memory before the web
Cheaper and better, and it needs no LTM subsystem — a tool plus `created_at`
arithmetic, with the age shown so the model can decide to re-research.

---

## Frontend

### D34 — One pure `Compute(w,h,state)`; components never hide themselves
Breakpoints live in one place, are unit-testable with no terminal, and are the
cheapest thing in the project to change later.

### D35 — One action registry feeding palette, keymap, help, and config
Adding a feature is one entry, not edits in four files. Also the only thing that
makes rebinding coherent.

### D36 — Leader + a few chords + palette holding everything
`Ctrl+Alt+<letter>` is not reliably delivered across terminals, and direct chords
collide and stay invisible. The palette is the fallback when a chord is swallowed
and the teaching tool for anyone new.

### D37 — Enter sends, Alt+Enter newline
`Ctrl+Enter`/`Shift+Enter` are indistinguishable from `Enter` in most terminals
without the kitty keyboard protocol. Rebindable via the registry.

### D38 — Queue, don't lock, at depth 1
Locking loses the thought; a naive queue fires a question the conversation has
moved past. Editable-until-sent gets both. Depth >1 is where it becomes a blunder.

### D39 — Tasks reserve their transcript slot at send time
Two replies interleaving token-by-token is unreadable. A collapsed card holds the
position, so nothing jumps and ordering is deterministic.

### D40 — Input growth overlays *and* auto-scrolls
Plain overlay hides the newest text — exactly what you are replying to. Push-up
re-wraps the transcript on every line typed. Auto-scroll looks like push-up with
none of the reflow.

### D41 — Two shared primitives: `Popup` and `Confirm`
Palette, memory, settings, `@` picker, task tray and help are one widget; every
destructive action is the other. This — not performance — is the honest reason
for building custom widgets instead of using `bubbles`.

### D42 — Components are data-driven from birth
Required for declarative user layouts later; cannot be retrofitted cheaply.

### D43 — Mouse on by default, toggleable, never the only path
Mouse capture disables native text selection, which matters in a tool where
people copy output constantly. Shift-drag restores it, the terminal's own
shortcuts handle the clipboard, and `y` + OSC 52 works over SSH where mouse
selection does not.

### D44 — Transparent background by default
Respects the user's terminal, including blur/transparency, and needs no
background detection (which is unreliable over SSH and tmux). Palette applies to
text, borders, accents only.

### D45 — Error/warning colours sit outside the palette
A gold error is not readable as an error.

---

## Lifecycle

### D46 — Composed root + nested workspace models
A flat model makes unloading impossible — you cannot unload a struct field.

### D47 — Unloading a view never cancels a task
Structural, not a rule: the task is in another process. The opposite rule would
pin a view in memory for the entire 20 minutes when unloading actually matters.

### D48 — Resume rehydrates from the database
The alternative is buffering events in Go while unloaded, which re-creates the
memory you were freeing. Rehydration is also the cold-start path and the
crash-recovery path: one code path, constantly exercised.

### D49 — Backend refuses to free anything a running task holds
The one hard rule that makes backend unload safe.

### D50 — Claim bounded growth, not freed memory
CPython does not reliably return arenas to the OS, so RSS may not drop. Object
counts and cache sizes across repeated cycles are measurable and true.

### D51 — Drafts and scroll persisted to disk, debounced
Auto-unload can fire while you are away from the keyboard; a discarded draft is a
bug nobody reports and nobody forgives.

---

## Reliability

### D52 — Crash-tolerant, not crash-free
Most crashes will not be Point's bug: Ollama OOM-killed, laptop sleep, an SDK
raising in a thread. Auto-restart once, then a banner with manual retry; on
reconnect, missed events replay from the DB. Partial messages are already
persisted with an `interrupted` flag, so crash recovery and user cancellation
share a rendering path.

### D53 — A second Point instance refuses to start
Two processes on one database with two sets of file locks is a logical conflict
WAL does not solve.

### D54 — `MockProvider` early, not with the test suite
Without it every agent test needs a live model: slow, flaky, unrepeatable, and it
costs money. With it, streaming, cancellation, queueing and recovery are testable
in milliseconds.

### D55 — Demo mode wears a visible badge
A deterministic replay mode is genuinely useful (presentations, wifi failure).
One that hides what it is would be a liability. "Try it free" is answered with
real inference via OpenRouter's free models and Ollama, not with theatre.

### D56 — 429s are retries, not failures
Backoff with jitter, respect `Retry-After`, surface it on the card. Otherwise a
burst of concurrent tasks looks like random crashes.

---

## Providers, projects, secrets

### D57 — Secrets never in `config.toml`
Env var → `credentials.toml` (0600, gitignored) → prompt. One `git add .` away
from a leaked key is a real and common failure. Masked in logs, errors, and UI;
written temp-file-then-rename.

### D58 — Effort normalized and translated per adapter
`reasoning_effort`, thinking budgets, and "nothing at all" are not the same
field. Normalize to `off|low|medium|high`, translate or ignore per provider, and
advertise `supports_effort` so the UI does not offer a control that does nothing.

### D59 — Provider-aware filtering of reasoning blocks
Blocks are tagged with the provider that produced them and included only for that
provider. Nothing is deleted — the UI still shows them. Blanket stripping could
break Claude's own thinking+tool loops.

### D60 — Model resolution: task > session > agent > global
Mid-session switching is safe because messages are provider-neutral. That is the
payoff of a stable message layer.

### D61 — A project is a directory with a `.point/` marker
A named-in-app project would store a path anyway, less honestly. The ID inside
survives renames and moves.

### D62 — Development against Ollama
Point is a five-provider app; developing against paid APIs spends real money
proving code paths Ollama proves for free.

### D63 — `uv` in development, PyInstaller `--onedir` at release
`--onefile` re-extracts to temp on *every* launch — roughly a second of pure
waste. Packaging early costs build-debug cycles for three months and buys
nothing until there is someone to hand it to.

### D64 — Startup solved by lazy imports + concurrent spawn
The cost is importing SDKs, not the interpreter. Go paints in ~20ms while Python
warms in a goroutine, so perceived startup is the Go binary's.

### D65 — Docker/Kubernetes rejected
Point needs a TTY, the filesystem, and the host's Ollama. Containerizing makes
startup slower and breaks SSH. Kubernetes orchestrates replicas of network
services; Point is one process on one laptop.

---

## SSH

### D66 — Topology B: own runtime per connection, shared database
Most of collaboration's value (shared sessions, memory, presence) without
connection-scoped identity on every runtime event. Shared-screen attach and true
co-editing are parked.

### D67 — An SSH session is a restricted session, enforced in the backend
A terminal pane, an ExecProcess hatch and a file tree served over SSH is a remote
shell with your permissions. UI checks are bypassable; backend checks are not.

### D68 — Hybrid credentials with a hard cap that refuses before spending
Guests otherwise bill the owner's account with no ceiling. Killing a task
mid-stream wastes what is already spent, so the cap blocks the *next* request
instead. A guest's own key is uncapped — the cap protects a wallet, not behaviour.

### D69 — Soft file locks in v1, not v2
Two people editing one file under topology B loses work silently at save. A
heartbeat lock plus an mtime check on save is ~2 days and also protects the user
from their own second terminal.

---

## Scope

### D70 — B1–B12 shippable, B13–B17 stretch
Named consequence: v1 may ship with no memory *system*. That is survivable
because manual attachment, reference mode and an explicit token budget are
already a defensible context story — and stronger than a half-tuned summarizer.
Checkpoint rule: at B12, if three weeks remain build B13 and drop SSH; if six
remain, do both.

### D71 — Editor terminal: Tier 0 + `tea.ExecProcess`
Tier 0 already runs `go run` and `pytest`; only *interactive* programs need a
PTY, and `ExecProcess` provides those by handing over the real terminal. A VT100
emulator plus Windows ConPTY would be weeks for something a second terminal
already does. Agents never need a PTY either — they need capture and an exit code.

### D72 — A capability earns a workspace only when its surface is not a transcript
Coding qualifies (diffs, plans, test output). Trading does not, permanently.
Data is a candidate awaiting evidence.

### D73 — Dynamic capabilities: build Tier 2, ship Tier 1 as data
A "preset" is a shipped TOML layout file, so the declarative composer gives both
for one implementation. The test that it is done: **Chat and Editor can be
expressed in it.** If Point's own workspaces need escape hatches, users' will too.

### D74 — ML/GPU agent cut from Point
By Point's own anti-slop rule: it existed to signal capability, not to solve a
workflow problem. Better as its own project where GPU work is the point.

### D75 — ADK / A2A dropped from the all-Go plan
ADK would take over the Runtime, Agent and Tool layers — the parts built from
scratch on purpose. A2A is a protocol for agents across process boundaries; Point
has one runtime and in-process agents, so adopting it would mean inventing
distributed structure to justify the protocol.

### D76 — Language choice matters only where Point's own code is the bottleneck
This system is dominated by network latency to providers and human typing speed.
The genuinely language-sensitive components are the text buffer, incremental
parsing, file/symbol indexing and embedding math — that, and not "the backend",
is the C++ boundary. Supporting evidence: Bun's full Zig→Rust rewrite, done by
the language's own author, yielded ~10% startup (517ms→464ms) and, in his words,
barely anyone noticed — and Claude Code, the agent layer, remains TypeScript.

### D77 — "Fast" and "code intelligence" are in direct tension
Point will feel fast partly because it does no indexing. Every intelligence
feature re-imports exactly the cost that makes VS Code slow on large Python
projects. Each one must pay for itself against that.

### D78 — Documentation split by role, one fact one home
The old docs had three files listing the same episodes and already disagreed
about which were done. Task docs describe intent; `completed/` describes reality;
`project_idea.md` describes structure; this file describes rationale. Phase tags
replace separate "first half" files.

### D79 — One episode per Claude Code session, with a manual verification gate
A long session accumulating failed attempts is where drift and hallucination come
from. Per-episode task files keep each session's context to a few KB.

---

## Revisions and additions (post-v1 design round)

### D80 — Git is the Coding Assistant's safety mechanism, and it is mandatory
**Alternatives:** per-edit approval (rejected — kills autonomy); a sandbox copy
of the tree (rejected — divergence and merge pain); trusting the model (no).
**Why:** git makes every write reversible with one command, which is what buys
the autonomy. Preconditions, enforced before a run starts:
```
git repo + clean tree   → run
git repo + dirty tree   → [commit] [stash] [run anyway]
no git repo             → REFUSE. Offer [git init + initial commit] [cancel]
```
Point never runs `git init` silently. `.point\` is not git and `uv init` is not
git — only git gives you `git restore`.
**Cost:** the assistant is unusable outside a repo. Accepted deliberately: if
someone declines git and loses work, that is a stated consequence, not a
surprise.

### D81 — A checkpoint commit before every Coding Assistant run
Its sha is stored on the task (`git_checkpoint_sha`, added in B6). `revert all`
is `git restore` against it. Without the checkpoint, "undo this run" means the
user hunting for a sha themselves — which is what Claude Code leaves you to do.

### D82 — Three approval modes, config-controlled
```toml
[coding]
approval = "ask" | "accept_edits" | "accept_all"   # default: accept_edits
```
The line between the two permissive modes is **file writes vs command
execution**:

```
ask            every file write pauses for approval   (task → PAUSED)
               every command pauses too
accept_edits   file writes happen freely; diffs stream past you,
               informational, never a prompt
               COMMANDS STILL ASK      ← "you may edit; ask before you run"
accept_all     file writes AND allowlisted commands, no pauses
```

The distinction matters because **a file write is reversible via the checkpoint
and a command is not** — `npm install`, a migration, a deploy script all touch
things git does not track. `ask` is what the reserved `PAUSED` /
`AWAITING_APPROVAL` task states (B6) are for. `accept_edits` is the default.

### D83 — Streaming per-edit diffs *and* an end-of-run review
Per-edit diffs streaming past you is the terminal norm (Claude Code, Codex); a
review pane with per-file accept/reject is the GUI norm (Cursor). Point does
both, because the checkpoint commit makes the review pane nearly free:
```
DURING  each write renders a diff in the transcript as it lands — informational
AFTER   "7 files changed, +142 −38"
        [keep] [revert all] [revert file…] [continue with feedback]
```
**Named caveat:** per-file revert is not always safe. Reverting `api.py` while
keeping a test written against it leaves an inconsistent tree — Cursor has the
same problem. Allowed, but warned: `reverting api.py may leave 2 other changed
files inconsistent`. `revert all` is always safe.

### D83b — Commit per STEP, and make step the primary revert axis
**The improvement over Cursor**, and it exists only because git is mandatory and
the agent loop is bounded.

Cursor offers per-*file* accept/reject because it has no notion of what the agent
was doing. Point's agent loop does — it is bounded by `max_steps` and already
emits `step k/n`. So each step gets its own commit:

```
"point: checkpoint before run"
"point: step 1/4 — add validation to api.py"
"point: step 2/4 — tests for validation"
"point: step 3/4 — update README"
```

**Reverting a step is always consistent**, because a step is a coherent unit of
work by construction. Reverting a *file* may not be — and now the UI can say why,
because it knows which later steps touched the same files:

```
⚠ reverting step 1 also reverts steps 2 and 4, which touched the same files
```

Review pane:
```
7 files changed · +142 −38 · 4 steps
  ▸ step 1  add validation        api.py                    [revert]
  ▸ step 2  tests for validation  test_api.py               [revert]
  ▸ step 3  update docs           README.md                 [revert]
  ▸ step 4  fix lint              api.py  test_api.py       [revert]
  [keep all]  [revert all]  [revert file…]  [continue with feedback]
```

**Cost:** one `git commit` per step — milliseconds. On `keep all`, offer to squash
the run into a single commit so history is not littered with WIP commits.

Per-file revert stays available and stays warned. Per-step is the default axis.

### D84 — Command execution is unlocked for the Coding Assistant only
B10 forbids agents running commands, and that was correct for v1. The Coding
Assistant needs it to self-correct — run the tests, read the failure, fix it —
so the prohibition lifts for that agent alone, with an allowlist (`git`, `npm`,
`pytest`, `go`, project scripts) and a hard deny outside the project root
(B7's existing path guard). **Not** per-command approval: the checkpoint covers it.

### D85 — Ghost text while an agent streams a region
The agent's output renders as dim ghost text in the editor buffer as it arrives —
nothing on disk, nothing in the real buffer — and the diff card appears when the
stream completes. Gives the "watch it write" feeling of Codex without giving up
the guarantee that nothing lands without a keypress. Switching workspace mid-run
and returning rebuilds the ghost from the task's accumulated events, reusing the
same rehydrate property F12 already needs.

### D86 — Web preview: Point serves static projects, delegates npm projects
```
① static (no package.json)  Point IS the dev server: net/http + fsnotify +
                            an injected websocket reload script. ~150 lines.
                            Nothing else gives you this for free.
② npm project               Point runs the project's own dev script in the
                            terminal pane and opens the browser at its port.
                            The framework's HMR is better than ours would be.
③ framework detection, server supervision, restart policy, HMR error parsing
                            PARKED — a process supervisor that replaces typing
                            one command once per session.
```
**HTTP does not touch the stdin/stdout protocol.** The preview server is a Go
goroutine on its own socket; Python never sees it, and when the preview is off
the goroutine does not exist. Reload fires on **save** for human edits and
**immediately** for agent writes — a half-typed CSS rule should not flash a
broken page.
**Watcher discipline:** `fsnotify` (OS notifications, not polling), scoped to the
preview folder, stopped entirely when the preview closes — the same rule as the
right pane dropping subscriptions when hidden.

### D87 — Per-workspace idle timers, not one global timer
Each loaded workspace has its own `lastActive`; the tick walks whatever is
loaded, so the count is dynamic — four workspaces or twelve, no constant
anywhere. The active workspace never unloads. A single global timer would unload
three workspaces at once for no reason.
Requires two affordances, or unloading feels like the app losing your work:
idle state visible per workspace (`Editor · idle 3m of 5m`), and a ~30s grace
warning (`Editor unloads in 30s — any key to keep`). Per-workspace overrides,
because the Editor is more expensive to reload than Chat.

### D88 — No plugin system
**Alternatives:** Go's `plugin` package (does not work on Windows); subprocess +
RPC (too slow per keystroke); embedded Lua/Starlark (viable, but premature).
**Why:** a plugin API is the price of serving millions of people with divergent
needs. Point serves one person who owns the source. A plugin system means
writing every feature through a keyhole designed before you knew what you needed,
and it costs API stability, crash isolation, versioning, and docs.
**What replaces it:** five existing seams — the action registry, tool registry,
capability registry, TOML layouts (Tier 2), and agent configs as database rows.
If scripting is ever wanted for other people, embedded Lua is the answer, and by
then the API surface will be known rather than guessed.

### D89 — LSP is the real gap, and it is in tension with the speed goal
What separates a real editor from a text box with colours is go-to-definition,
diagnostics, and hover — not plugins. LSP is parked under "code intelligence",
and unparking it re-imports exactly the cost that makes VS Code slow on large
Python projects (D77). The mitigation, if it is unparked: start language servers
**lazily, per language, on first file open** — never at startup.

### D90 — Autosave, with a 1000ms idle delay
```toml
[editor]
autosave = "off" | "after_delay" | "on_focus_change"   # default: after_delay
autosave_delay_ms = 1000
```
**Why 1000ms:** it is VS Code's number, chosen so a save rarely lands mid-token.
That matters more here than there, because with the preview running (F17) every
save reloads the browser — a 200ms delay would flash a broken page constantly.

**Why it needed a decision entry at all:** autosave touches four things already
decided, and each interaction is a bug if unwritten.

```
× soft locks (B7)       never autosave a read-only or externally-held file,
                        or autosave hammers the refusal path once per second
× stale-save check      Point must record its OWN write mtimes, or its autosave
                        trips its own "changed on disk" check
× agent diff apply      apply → autosave is correct (apply WAS the keypress),
                        but Ctrl+Z must also save, or buffer and disk disagree
× clean-tree gate (D80) autosave leaves the tree near-permanently dirty, so the
                        Coding Assistant's precondition prompts often. Accepted:
                        its [commit]/[stash]/[run anyway] choice handles it.
```

Autosave never clears undo history, and never fires for a buffer with no path.

---

## B6 — schema, envelope, bootstrap (implemented, not aspirational)

### D91 — Envelope v1, exactly as it goes on the wire
Adopted in B6, while **no Go client existed** — one side to change, zero
compatibility burden. Every line in both directions is now:

```json
{ "v": 1, "id": "0f0e…", "type": "TextDelta", "corr_id": "t1",
  "session_id": "…", "task_id": "…", "connection_id": null,
  "trace_id": null, "seq": 4, "ts": 1787681546.33, "payload": { } }
```

What changed, and nothing else did:

| before | after |
|---|---|
| `event` | `type` |
| `request_id` | `corr_id` |
| `timestamp` | `ts` |
| `task_id`, present only sometimes | `task_id`, always present, may be null |
| payload keys flattened onto the top level | nested under `payload` |
| — | `v`, `id`, `session_id`, `connection_id`, `trace_id`, `seq` |

Decisions inside that, each one a thing F2 can rely on:

- **This was a serialization-boundary change only.** `IncomingEvent` and
  `OutgoingEvent` kept their fields and names; `from_dict()` and `to_dict()` are
  what moved. No handler signature changed. That is why it cost one file.
- **`ts` is still a float epoch**, not an ISO string. Only the key was renamed;
  renaming the field's *type* as well would have been a second change wearing
  the first one's clothes.
- **`id` is a fresh UUID per envelope**, carried as a real field
  (`OutgoingEvent.event_id`) rather than minted inside `to_dict()` — serializing
  the same event twice must not produce two different ids.
- **`corr_id` is null for `hello` and `ping`**, which are caused by no request.
- **`connection_id` / `trace_id` are echoed, not threaded.** They are remembered
  per request id in `Runtime._correlation` (a bounded OrderedDict) and filled in
  at `_emit`. Passing them through seventeen handler signatures to reach the same
  boundary would have been the wrong shape for two fields nothing reads yet.
- **`seq` is the per-task stream sequence, lifted out of the payload** — the same
  number `StreamManager.next_sequence()` already produced, now printed in both
  places so `StreamAccumulator` is untouched. It is **null on envelopes that are
  not part of a task's token stream**: a lifecycle event is identified by its
  type, not by counting, and inventing a second counter for them would have made
  the token sequence unreadable.
- **A version mismatch refuses and shows both versions**, in the error text:
  `backend speaks v1, client sent v2`.
- **`hello` is the first envelope on the wire**, emitted before anything else and
  even when nothing is configured. Its `capabilities.providers` lists what is
  *registered*, not what is reachable — Ollama registers without a key, so a
  keyless profile reports `["ollama"]`, and whether the daemon is actually up is
  a health question `ListProviders` answers.
- **`ping` every 5s while idle**, from the same loop that sweeps finished tasks.
  Two missed pings is how Go will catch a *hung* backend — the failure mode
  process-exit detection misses entirely.

### D92 — The cancel ack stopped being terminal
Before B6 a cancel produced **two** `TaskCancelled`: one ack from the handler,
one real terminal event from the sink. B05's test log recorded this and called it
expected; it was not, and it made D8 false.
`CancelTask` now acks with **`task.cancel.accepted`** carrying `{"accepted":
bool}`, and the sink is the only producer of `TaskCancelled`. `accepted: false`
covers both "already finished" and "no such task" — from the caller's side those
are the same answer: nothing was stopped, and no terminal event is coming.
**Cost:** one more event type. **Bought:** D8 is now true, and F2 may rely on it.

### D93 — NULL provider/model means "inherit", and B6 does not resolve it
`sessions.provider` and `sessions.model` became nullable (a full table rebuild —
SQLite has no `ALTER COLUMN`). NULL is not "missing": it is how a session says
*inherit from global config*, which is the resolution order in §10.
`CreateSession`'s literal defaults (`"claude"` / `"claude-sonnet-5"`) are gone —
they made every session claim a vendor whose key might not exist, and "claude"
resolves to anthropic, which does not register at all on a keyless profile.
**B6 only made NULL storable. B8 owns resolution.** Until B8, `SendMessage`
against a NULL-provider session returns a clean `Error` saying so. Refusing is
not resolving; guessing would have been.

### D94 — `approval.*` is a reserved namespace, and nothing produces it
`TaskState` gained `PAUSED` and `AWAITING_APPROVAL`, and `tasks.status` accepts
them. **Nothing sets either**, and the `approval.*` event namespace
(`approval.requested`, `approval.granted`, `approval.denied`) is reserved, not
implemented. They are in the enum because a status column is a migration and an
enum member is not.
Related and deliberate: **there is no `QUEUED`.** `PENDING` is that state today.
`QUEUED` arrives in B12, where it will mean something `PENDING` does not —
blocked behind a per-provider concurrency cap. Adding it early would have made
two names for one state and no way to tell them apart later.

### D95 — Tasks are durable, and a task cannot survive its process
`TaskManager._tasks` remains the in-memory index; the `tasks` table is a
**mirror** of it, written through an optional recorder on every state transition,
from the one place transitions already happen. Every write is an upsert, because
`create_task()` is synchronous and schedules its row without awaiting it.
**Crash recovery runs before a single event is accepted:** every row still
`pending` or `running` at startup is marked `failed` / `backend_died` with a
`finished_at`, and the count is logged. A row in a live state at startup is by
definition a corpse.
`error_code` is a short stable slug for Go to branch on, not the human string —
`Task.result` already carries that. **First and only member in v1:
`backend_died`.**
The one place this is conditional: the CLI, which does *not* take the
single-instance lock, only runs recovery when no live process holds it. Burying
a running backend's tasks from a second process would be worse than the problem.

### D96 — The database left the source tree, and migrations are forward-only
`backend\runtime.db` became `%LOCALAPPDATA%\Point\runtime.db` (override:
`POINT_DB`, then config `database.path`). The old file was **copied, not
moved** — removing it was left as a human's call once the move was proven.
Migrations are versioned, forward-only, recorded in `schema_migrations`, and run
automatically on startup. **There is no `down`:** a rollback is restoring the
copy the runner takes before every migration, as `runtime.db.bak-<version>`.
`.bak-2` therefore holds the schema as it was at v1 — the thing you want when v2
went wrong. The copy is preceded by a WAL checkpoint, because under WAL a plain
file copy can miss everything still in the `-wal` file, which is the difference
between a backup and a file that looks like one.
The rebuild in v2 runs with `foreign_keys=OFF` and `legacy_alter_table=ON`: with
foreign keys on, `DROP TABLE sessions` would have cascade-deleted every message.

**B29 made the removal call.** Found live during F10c's verification: a
brand-new scratch profile's "fresh" database already had 12 real sessions,
because `resolve_db_path()`'s `migrate_legacy` default silently copied
`backend\runtime.db` into it — and grep showed every caller in this
codebase, including four scratch-profile manual test scripts (B12, B19,
B26, B28), called it with no arguments, so the opt-out had a 0% adoption
rate in practice, not just in theory. The move itself had been proven for
this repo's entire visible history (its very first commit is titled
"B1-B6 already done"); `backend\runtime.db` was gone and the copy-forward
mechanism removed outright, not merely defaulted off, so there is nothing
left for a future caller to forget to disable.

### D97 — A stopgap type gets its own name, never the future episode's real one
**Alternatives:** build the later episode's real thing early, scoped down; wait
and block the current episode on the later one; reuse the later episode's name
for a narrower shape now and rename in place later.
**Why:** confirmed as a repeated pattern, not improvised once: B9 needed the
Context Builder's knobs before the agent system existed and defined
`ContextPolicy` rather than widening B8's `build()` to take the future
`AgentConfig`; B10 needed a per-send bounds override and defined `BoundsOverride`
rather than making `AgentConfig` mutable; B11 needed Research to emit durable,
listable proposals before B13 (memory system) exists — B13 has its own
precondition (three observed failures from real usage) and cannot simply be
pulled forward — so B11 defined a new `agent_proposals` table and a `Proposal`
dataclass rather than writing into the `memory_records`/`memory_scopes` tables
B13 already has reserved in the schema (since v2) but does not yet own the
business rules for. Each time, the later episode reads the earlier one's
completed doc and decides how its real type adapts to what already shipped —
dual-write, migrate-and-drop, or leave both standing — rather than discovering a
half-built, wrongly-named version of its own feature already occupying the name
it needed.
**Cost:** two names for what will eventually be one concept, for a while, and a
later episode has to actively reconcile them instead of finding nothing there.
Judged worth it every time so far: the alternative is building the real thing
against imagined requirements, which is exactly what B13's own precondition
exists to prevent.

### D98 — The heartbeat pings unconditionally; a silent task is not proof of life
**Alternatives:** keep suppressing `ping` while tasks are active and rely on
tokens as the liveness signal; suppress only while a *streaming* task is active;
have Go send the ping and the backend reply; emit `ping` on a fixed interval
regardless of what the backend is doing.
**Why:** `Runtime._heartbeat_loop` skips its ping whenever
`self.tasks.active_count() > 0`, and its docstring gives the reason — "tokens are
already proving liveness". That is true of a streaming reply and false of a tool
call. B10's `web_search` against a slow endpoint emits `ToolStarted`, then
nothing at all until `ToolCompleted`; a long `read_file` on a large file behaves
the same. Inside that window the backend is producing no tokens *and* no pings,
so the wire is indistinguishable from a deadlocked process — and it is
indistinguishable during precisely the operation most likely to deadlock, which
is the one waiting on something external.

Detection cost is the visible half. F2 could not simply shorten its watchdog, so
`Client.run` stretches the deadline by `busyGrace` (60s) whenever it believes a
task is in flight. A genuine hang mid-task therefore takes ~72s to notice rather
than ~12s. The alternative — not stretching it — would kill and restart a
perfectly healthy backend every time a tool call ran longer than twelve seconds,
which is worse: a false positive here cancels real work.

Having Go drive the ping instead was rejected for the same reason the backend
owns every other timer: it puts liveness on the render loop, and a frontend busy
enough to be late with a ping is not evidence about the backend at all.
**Cost:** one envelope every five seconds during a task that is already
streaming thousands — negligible on the wire, and it removes a special case
rather than adding one. B15 makes the change and deletes `busyGrace` with it;
until then the frontend carries a workaround for a backend decision, which is
the wrong side for it to live on.

### D99 — The event log holds lifecycle events, not the token stream

**B40.** `events` (migration v8) persists every session-scoped envelope
that leaves `Runtime._emit`, **except** `TextDelta`, `ThinkingDelta` and
`TaskToken`.

**Why:** a write per envelope on the streaming hot path goes through
B22's `SerializedConnection`, which takes a connection-wide
`asyncio.Lock` and commits per statement. Measured with
`cli.py bench event-log` over a 2,000-delta fixture, four runs: logging
everything costs ~2ms per envelope and **15-20x** the baseline wall-clock
of a turn (~4.0s vs ~0.23s); logging only lifecycle events is four writes
a turn and sits inside run-to-run noise. **(c)'s cost is not claimed to
be zero** — its measured spread straddles zero, which is the honest
reading of a sub-300ms measurement.

It is also the correct answer and not merely the cheap one. Those three
types are exactly `IsDroppable` in
`frontend\internal\protocol\types.go`: the live path may already discard
them under backpressure, because the accumulated string re-renders from
the next token. An event the live path may drop is not one a replay can
be expected to have. And the text is not lost by its absence — it
rebuilds from `messages`, or from B15's `message_checkpoints` for a reply
the process died mid-way through. Persisting the deltas would be a third
copy of the same string, written per delta.

So a replay reconstructs the **structure** of what happened — the tool
call that started and completed while the client was away, progress
steps, usage updates, a `FillProposal` after a terminal event, none of
which B15's message-level interim could express — with the prose coming
from the tables that already own prose.

**Cost:** a reconnect cannot replay a half-finished sentence
token-by-token. It gets the finished text, or the checkpointed partial,
and the event structure around it. Option (b) (buffer and flush) was not
built: (c) made it unnecessary rather than preferable, since the volume
(b) exists to batch is the volume (c) does not write.

**Corollary, found while building:** the reply to `EventsSince` must NOT
be logged. `_emit` is the one persistence boundary and the reply goes
through it, so the first version logged a row whose payload was a copy of
a page of the log — growing super-linearly in replays, and making
`events_since(session, max_seq)` never settle. `REPLAY_TYPES` excludes it
unconditionally. **An envelope that carries log contents is not log
content.**

### D100 — `event_seq` is a new envelope field; `seq` keeps its meaning

**B40.** The durable per-session event cursor rides on the envelope as
`event_seq`. The existing `seq` still means `StreamManager`'s
per-task stream counter, unchanged.

**Why:** additive is the rule B1-B5 are frozen under and the pattern
B18, B24 and B36 all followed, and here it is also a concrete count —
**four consumers already decode `seq`** (`StreamAccumulator`, F5's stream
ordering, F18's pagination, B24's `FillProposal` note). Redefining it
would be one field instead of two, bought with a contract change
arriving disguised as the same field: the kind a frontend episode finds
six weeks later, because nothing fails at the boundary.

There are now three distinct per-something counters in this repo and
conflating any two is a bug: the per-task stream `seq` (in-memory,
dropped at `close_stream()`, null on every lifecycle event),
`messages.seq` (v6/B18, per-session message insertion order), and
`events.session_seq` / `event_seq` (v8/B40, per-session event order).

`event_seq` is null on any envelope the log does not hold — one with no
session, and the three token types of D99. A client marks the non-null
ones with `MarkSeen`; the nulls are not gaps.

**Cost:** one more nullable integer on every envelope, and B24's note
that *"`seq` is null on BOTH envelopes and cannot order them"* is now
incomplete rather than wrong — `seq` is still null there, and `event_seq`
is what can order them.

`MarkSeen` stays the **consumer's** to call even though `event_seq` is a
routing field the protocol package now parses. The mark means "has been
rendered", not "has arrived": auto-marking in `deliver()` would advance
it for events still sitting in a channel, and a reconnect would then skip
exactly the events the UI never drew.

### D101 — The event log is per-session, so session-less envelopes are not logged

**B40.** `events.session_id` is `NOT NULL REFERENCES sessions(id) ON
DELETE CASCADE`. `hello`, `ping`, and every reply that carries no session
are not written.

**Why:** B40's own Build section drafted the column nullable, reasoning
that "an event log that cannot hold them is not a log of what went over
the wire" — and that contradicted its own next column. `session_seq` is a
**per-session** cursor, so an envelope with no session has no position in
any cursor space for a `NOT NULL` column to take, and the one query
against the table (`WHERE session_id = ? AND session_seq > ?`) can never
return such a row. The contradiction resolves in favour of the cursor,
because the cursor is the only reason the table exists.

This is wider than `hello`/`ping`. `_fill_envelope` backfills
`session_id` only when the *request* carried one, so `SessionList`,
`ProviderList`, `ProviderModelList`, `AgentList`, `DirectoryList`,
`DoctorReport`, `BehaviourOverrideSet`, `TaskList`, `shutdown.complete`
and any `Error` raised before a session is known all emit session-less.
Writing them would be unreplayable rows — at one `ping` every five
seconds forever, given B15's unconditional heartbeat (D98).

**Cost:** the table's claim is narrower than "everything on the wire".
It is what went over the wire **for a session**, and the narrower claim
is the true one. Wire-completeness would need a second global cursor
column and a nullable `session_id`, buying a `ping` row every five
seconds that nothing reads.

### D102 — Event retention: 7 days or 2,000 rows per session, swept from the heartbeat

**B40.** Two caps, both enforced by `EventRepository.sweep()`, called
from `_heartbeat_loop` every 60th beat (~5 min).

**Why:** an append-only log on a long-lived local backend is a disk-full
bug with a long fuse, and B40 required the policy decided in-episode
rather than deferred to B17. Age (7 days) is the cap that matches what
replay is *for* — a client away a week restarted, it is not
reconnecting — and still leaves the log readable as a "what did the
backend send yesterday" diagnostic. The per-session row cap (2,000)
bounds one very busy session inside that window; per-session rather than
global so a chatty session cannot evict a quiet one's replayable
history, and 2,000 is an order of magnitude above `client.go`'s
`replayLimit` of 200. `TaskManager.sweep_finished` is the precedent for
running it from the heartbeat; the throttle keeps a DELETE scan off a
5-second cadence.

**A sweep does not renumber.** `session_seq` has gaps afterwards and that
is correct: the cursor is a high-water mark, `events_since` asks `> seq`
rather than for a dense run, and renumbering would invalidate every
client's existing mark. A client whose mark was swept past gets the
oldest surviving events plus `has_more`, which is the most a bounded log
can honestly offer.

**Cost:** replay has a horizon. Beyond it a reconnect falls back to the
transcript, which is what B15's interim did for every case.

