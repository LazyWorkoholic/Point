# Episode B23 — Agent Listing and Directory Browsing on the Wire

**F8's `@` picker needs to know two things live, per keystroke, that only the
backend can answer: which agent names exist, and what is in an arbitrary
directory. Neither is reachable from Go today. Put both on the wire, over
logic that mostly already exists.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B23-agent-and-directory-listing.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. Gap 1 is plumbing over a function that already exists,
tested, and correct — the same shape as B20's `context.report` wire-up. Gap
2 needs one real design decision (see Build §2) but no new business logic
beyond that.

**Depends on:** B7 (`managers/pathguard.py`'s `resolve_path()` /
`is_outside_project()` — directory browsing must go through these, not a
second containment check), B9 (`AgentRegistry`, whose `list()` already does
almost all of Gap 1).

**Consumed by:** F8 (`design/tasks/frontend/F08-at-picker.md`). Its Agents
section, and its ambiguity rule ("an exact agent name wins, otherwise treat
it as a path"), and its filesystem-browse mode (`@`, `@C:\`, `@~\`, `@\`,
`@.\`, `@..\`) cannot be built without both endpoints below.

---

## Why this episode exists

Found while starting F8 — a session that stopped before writing any code,
per CLAUDE.md's "if anything seems to assume something not true of this
repo, stop and tell me" rule, rather than guessing at either shape below.

### Gap 1 — no `ListAgents` wire event

- `AgentRegistry.list()` exists (`agents/registry.py:64`) and returns
  `list[AgentConfig]` (`id`, `name`, `description`, among other fields —
  `managers/agent.py:34`).
- `cli.py agents list` already calls it (`cli.py:945`) — but that is the CLI
  calling `runtime.agents.list()` **in-process**. It proves the data exists
  and is already correct; it proves nothing about the wire.
- `IncomingEventType` (`runtime/events.py:53`) has no `ListAgents` member.
  `OutgoingEventType` (`runtime/events.py:146`) has no `AgentList` member.
  Checked against the full enum, not grepped for a name that might have
  drifted.
- Without this, Go cannot render the picker's Agents section, and cannot
  decide the `@<name>` ambiguity rule client-side. `parse_invocation()`
  (B12) already implements that exact rule, but only at *send* time, for a
  fully-typed message — not as a live "which names exist" query a picker
  can call on every keystroke.

### Gap 2 — no directory-browse wire event

- `ListProjectFiles` (`runtime.py:640`) calls `_require_project()`
  (`runtime.py:612`), which raises `ProjectNotFoundError` when no project
  resolves. It lists only a resolved project's indexed file tree —
  never an arbitrary path, and it does not work in the exact case F8's own
  spec names as the fallback: "with no project: filesystem browse rooted at
  cwd".
- `ReadProjectFile` reads one file's contents. It is not a directory
  listing.
- `AttachmentManager.preview()` (`managers/attachment_manager.py:301`) can
  list a directory's first 20 entries — but only as the fallback offer
  *after* `ResolveAttachment` refuses a specific path with `is_directory`.
  That is "here is what you tried and failed to attach", a one-shot
  courtesy, not "list this folder's children", which is what a picker needs
  to page through live as the user types `@C:\Projects\`.
- No CLI command covers this either — checked every `add_parser(` call in
  `cli.py`; `project list-files` is the closest, and it is the same
  project-only call as `ListProjectFiles`.

F8's own spec text — "Go never reads files — the backend resolves every
path" — already assumes a directory-browse primitive exists on the backend.
It does not.

---

## Build

### 1. `ListAgents` (incoming) / `AgentList` (outgoing)

Two new envelope types, one handler wrapping `AgentRegistry.list()`, same
pattern as every other read-only listing handler in `runtime.py`. No new
manager logic — the registry already does the work. Which `AgentConfig`
fields to serialize is this episode's own call; `id`, `name`, `description`
are the obvious minimum a picker needs (matching, and the label to show).

### 2. A directory-listing wire event for a path that may or may not be inside a resolved project

This is the part with a real decision in it, not just plumbing:

- Must resolve through B7's `resolve_path()` / `is_outside_project()` — B7's
  own handoff says it is "the only sanctioned way to turn a string into a
  filesystem path anywhere in this backend"; this episode is exactly the
  kind of caller that line was written for.
- **What should `allow_outside_project` mean for a *listing* versus a
  *read*?** F8's spec wants `@C:\` to work as ordinary browsing with no
  project open at all. Today's `project.allow_outside_project` config gate
  (default `false`) exists to stop a file **read** from escaping a
  project's root — refusing that by default is right for content. Whether
  a directory **listing** (names only, no content) should be exempt from
  that same gate is not decided by anything already built, and the two
  answers lead to genuinely different UX (browsing always works vs.
  browsing is config-gated exactly like attaching). Decide deliberately and
  record the reasoning here or in the completed doc — do not let listing
  and reading silently diverge on what the gate means.
- "Rooted at cwd" (F8's no-project fallback) needs a caller-supplied
  root/path on the request. Decide whether that is the *backend* process's
  cwd or the *frontend*'s — they are not the same directory: per
  `CLAUDE.md`, the backend always runs from inside `backend\`, while the
  user's actual working context is wherever they launched `point.exe` from.
  The frontend almost certainly has to pass its own cwd explicitly; do not
  default to the backend process's cwd without checking this reasoning
  first.
- Resolve `~` the same way `resolve_path()` already does
  (`os.path.expanduser`) — do not write a second home-directory
  resolution.
- Skip `.venv`, `node_modules`, `__pycache__`, `.git` the way
  `ProjectManager.list_files` already does (`managers/project_manager.py`,
  B7) — a raw, unfiltered directory listing would dump noise no picker
  should show. **But this is a plain, uncached directory browse, not the
  indexed/gitignore-aware/30s-TTL-cached file index `list_files` builds.**
  Do not route this through that cache for a different question; a picker
  browsing `C:\` a keystroke at a time needs live directory contents, not
  a 30-second-stale index built for one project's tree.
- Binaries and oversized files are not this episode's concern — that guard
  order already lives in `AttachmentManager.resolve()` and stays there.
  This endpoint answers "what is in this folder" (name, is-directory),
  nothing about whether any entry could be attached.

---

## Do NOT

- Build any picker UI, fuzzy matching, sections, or chip rendering — all of
  that is F8's, not this episode's.
- Implement memory search or a Memory section — B13's, untouched here, same
  as every other episode's note about it.
- Fold `PathDeniedError` or any other `RuntimeException` subclass into
  `PointError` — that is B17's, and B7's handoff already says not to do
  this piecemeal.
- Reuse `ProjectManager.list_files`'s cached file index for the new
  directory-browse endpoint. Different question, and reusing the cache
  would make a picker show stale directory contents while browsing.
- Add a new dependency for directory walking. `os` / `pathlib` cover
  everything this needs.
- Guess at the `allow_outside_project` question above rather than deciding
  it and writing down why.

---

## Done when

1. A wire request lists every registered agent (at minimum `id`, `name`,
   `description`), with no session and no project required.
2. A wire request lists the immediate children of an arbitrary absolute
   path (e.g. `C:\`, `D:\Projects\`), distinguishing files from
   directories, applying the same always-skip list as the project index,
   without requiring a resolved project.
3. The same request works relative to a caller-supplied root (the "no
   project: browse cwd" case) and resolves `~`.
4. Both refuse cleanly — a reason, never a traceback — for a path that does
   not exist or is not a directory.
5. Both are reachable from `cli.py`, matching every other episode's
   pattern, for manual testing independent of a Go client existing yet.
6. Existing `ListProjectFiles` / `ReadProjectFile` / `ResolveAttachment`
   behavior is unchanged — this episode adds wire surface, it does not
   touch B7's or B12's.
7. The `allow_outside_project`-for-listing decision (Build §2) is written
   down somewhere durable — this doc, or the completed doc — not left
   implicit in the diff.

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md: one combined script, `APPDATA` and `LOCALAPPDATA` redirected
to a scratch directory for the script's whole duration, never the live
profile, never a per-call env var (shell state does not survive between
tool calls).

Sketch only — the exact CLI verb names are this episode's own naming
choice, not fixed here; keep them consistent with the existing `cli.py`
subcommand style (`agents list`, `project list-files`, etc.).

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b23"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate

    # 1. agent listing -- no session, no project
    uv run python cli.py <agent-list-verb>
    # EXPECT: the built-in agents by id and name, e.g. general/research/... —
    # whichever three ship per B11 -- with no session or project argument.

    # 2. directory browse, absolute path, no project open
    uv run python cli.py <browse-verb> "C:\Windows"
    # EXPECT: entries, files vs directories distinguished, no
    # ProjectNotFoundError.

    # 3. directory browse, caller-supplied root, no project open
    uv run python cli.py <browse-verb> "."
    # EXPECT: entries under wherever this episode decided "cwd" means for a
    # no-project browse -- confirms which answer to the Build §2 "whose cwd"
    # question was actually picked.

    # 4. refusal, not a traceback
    uv run python cli.py <browse-verb> "C:\Windows\win.ini"
    # EXPECT: a clean "not a directory" reason, no stack trace.

    # 5. skip list still applies with no project open
    uv run python cli.py <browse-verb> "D:\Projects\Point"
    # EXPECT: .venv, node_modules, __pycache__, .git absent from the listing.
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

---

## Handoff

- This doc exists because F8 stopped before writing code rather than
  guessing at either endpoint's shape — the pattern CLAUDE.md requires, and
  the same one that produced B20 and B21 out of F6's session.
- **Gap 1 is nearly free.** `AgentRegistry.list()` and `cli.py agents list`
  already do the hard part; this episode's actual work is two envelope
  types and a dispatcher registration over a function that is already
  tested and correct, matching B20's own "the hard thinking was already
  done" framing.
- **Gap 2 has one real open design question** — whether a directory
  *listing* shares the `allow_outside_project` gate a file *read* uses, or
  is exempt from it since it exposes names, not content — and this doc
  deliberately does not answer it. That is this episode's own ambiguity to
  resolve (and write down), not something decided in advance by the
  session that found the gap.
- F8 (`design/tasks/frontend/F08-at-picker.md`) resumes in its own fresh
  session once this episode is built and verified — same three-step chain
  as F6 → B20/B21 → S13d.
