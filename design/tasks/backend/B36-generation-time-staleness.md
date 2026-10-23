# Episode B36 — Generation-Time Staleness for the Filler's Proposal

**`FillProposal` must carry the `(mtime, size)` pair captured at the
region-generation disk read, and `CheckFileStale` must accept `session_id`
and `size` as real comparison fields. Two additive changes to a built,
verified event — nothing else.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B36-generation-time-staleness.md`) claims
> reality and completion. Code wins on any disagreement.

> **Written after the episode ran.** B36 was built from WORKFLOW.md's
> Session 19a block directly and never got a task doc, while B19, B21,
> B23 and B25 — interleaved episodes of exactly the same shape — all have
> one. This doc exists so there is a document stating what B36 alone was
> asked to do, in the words that were reviewed. Its Build section is
> WORKFLOW.md Session 19a's Paste block **verbatim**, and its Done-when
> section is that session's "You do:" steps verbatim, per "Splitting an
> episode" step 2: *"each carrying its requirements verbatim from the
> combined doc. Do not re-derive or re-word them — the original wording is
> what was reviewed."* Nothing here is derived from the finished code;
> what the code actually does is the completed doc's claim, not this
> one's. **F9a is the counterexample that rule exists for** — it never got
> a task doc, so nothing anywhere states its own scope.

**Model:** Sonnet high. Two additive changes, both named in advance by
F9b's own handoff, over an event B24 already built, parsed and validated.
The judgment calls are where exactly the stat is taken (one descriptor,
not a stat-then-read pair) and what a caller supplying only `mtime` gets —
both stated in Build.

**Depends on:** **B24** (Session 17b, done) — this extends its event.
**B7** (done) for `check_stale` and the path guard.

**Consumed by:** F15 (Session 19), which writes the Go `EvFillProposal`
decoder and populates F9b's `DiffCardOptions.RecordedMtime`/`RecordedSize`
— F9's Done-when #7. And F9d (Session 19b), which uses the session-scoped
`CheckFileStale` for F9a's hazard-1 guard order. Both are frontend work and
neither is in this episode's scope.

---

## Why this episode exists

F9b (Session 18d) built the diff card's apply and **declined to build
Done-when #7** — refuse the apply when the file changed on disk since the
region was generated — because it cannot be built honestly against this
backend. The prerequisite check is recorded in
`design/completed/frontend/F09b-diff-card.md`; the short version, all three
verified in code:

1. **Nothing captures `(mtime, size)` at region-generation time.**
   `_render_editor_context` (`managers\context.py`) reads the file with
   `path.read_text(...)` and **never stats it**. `RegisteredEditorContext`
   (`streaming\manager.py`) carries `context`, `shown_start`, `shown_end`
   and nothing else. `FillProposal.to_dict()`
   (`managers\fill_proposal.py`) is seven fields with no stat among them,
   and its own docstring says all seven travel together, no subset. So the
   moment the model saw the file is unrecoverable downstream.
2. **`CheckFileStale` cannot express the comparison.**
   `_handle_check_file_stale` (`runtime\runtime.py:860`) requires `path` and
   `mtime` only — no `size`, no `session_id` — and `check_stale`
   (`managers\project_manager.py:590`) compares `st_mtime` against the
   supplied value with a 1e-6 tolerance. Size is not a parameter.
3. **The pair that IS available is the wrong moment.** `doc.mtime` /
   `doc.size` are from the read that opened the file, replaced from every
   successful write's reply (autosave rule 2). F9b refused to substitute it:
   it fails in BOTH directions — a false refusal when the region was
   generated from content newer than the open, and a missed refusal when a
   save refreshed the pair after generation. That is B12's `set_mode`
   lesson and F8's chip staleness, which is exactly why F9b's own paste
   forbade it.

**It also unblocks F9a's hazard-1 guard ORDER**, which F9b could not
complete for the same reason: `session_id` is half of it. Nothing in F9b
fell back to `_resolve_optional_project` to fake it — the apply makes no
backend call at all — but the guard becomes load-bearing the moment an
applied buffer is **saved**, because F13b's `WriteProjectFile`
`_require_project`s the EDITOR's path-resolved `project_id` rather than the
session that produced the proposal. That is B12's bug #2 on a destructive
path, and F15 is what makes it reachable.

**Why it runs before Session 19 rather than after:** F15 adds the Go
`EvFillProposal` decoder, which does not exist yet. If B36 lands first, that
decoder is written **once** against the final payload shape. If it lands
after, F15 ships a decoder that B36 then has to extend, and F9b's deferred
half has to wait for a second frontend pass to carry two fields it could
have had from the start.

---

## Build

Verbatim from WORKFLOW.md's Session 19a Paste block:

```
Read CLAUDE.md, design/completed/backend/B24-filler-proposal-event.md,
design/completed/frontend/F09b-diff-card.md's "#7 is absent, not
approximated" and "The guard order (F9a hazard 1) is also incomplete"
sections, and design/completed/backend/B07-project-system.md for
check_stale.

Build two additions and nothing else.

1. FillProposal must carry (mtime, size) CAPTURED AT THE
   REGION-GENERATION DISK READ. That is the `path.read_text()` in
   `_render_editor_context` (managers\context.py) -- stat the file at
   the moment it is read, not before and not after, and carry the pair
   forward on RegisteredEditorContext beside shown_start/shown_end,
   then into FillProposal.to_dict(). Respect that dataclass's own rule:
   all fields travel together, no subset, so this is a wire-shape
   change and every consumer sees both fields or neither.

   The pair is a fact about ONE MOMENT. Do not re-stat it later in the
   turn to "refresh" it, and do not derive it from a second read --
   that is the same error B24 avoided by carrying shown_start/shown_end
   from the context build rather than recomputing them at the terminal
   event, and the error F9b refused to make on the frontend side.

2. CheckFileStale must accept session_id AND size as real comparison
   fields. Today `_handle_check_file_stale` (runtime\runtime.py) takes
   (path, mtime) and `check_stale` (managers\project_manager.py)
   compares mtime alone with a 1e-6 tolerance.

   - `size` must be compared, not merely accepted and ignored.
   - `session_id`, when supplied, must resolve the SESSION's scope --
     the `_attachment_scope` shape, from session.project_id -- and NOT
     `_resolve_optional_project`, which walks up from the path to
     auto-discover a project and answers a different question. That
     drift is B12's bug #2; 374baa2 established the corrected order for
     AttachContext. Follow that one.
   - Decide deliberately, and write down which: whether `mtime` and
     `size` are both required, and what a caller that supplies only
     mtime gets. An existing caller must not silently change meaning.

Both are ADDITIVE to a built, verified episode's event. Do not change
what B24 already emits beyond adding fields, and do not touch the
parse/validation rules -- B24's five reason slugs are what F9b's
frontend is written against.

Never a direct disk write from an agent path. No WriteProjectFile is
added or changed here.

Stop at the verification gate with PowerShell checks.

If anything here assumes something not true of this repo, stop and tell
me before writing code.
```

---

## Do NOT

The Paste block's own prohibitions, pulled out so they are not buried in
prose:

- Re-stat the file later in the turn to "refresh" the pair, or derive it
  from a second read. It is a fact about one moment.
- Ship a subset of `FillProposal`'s fields. All of them travel together —
  every consumer sees both new fields or neither.
- Accept `size` and ignore it. It must be compared.
- Resolve a session-scoped `CheckFileStale` through
  `_resolve_optional_project`. That walks up from the path and answers a
  different question — B12's bug #2.
- Change what B24 already emits beyond adding fields, or touch its
  parse/validation rules. Its five reason slugs are what F9b's frontend is
  written against.
- Add or change any `WriteProjectFile`. Never a direct disk write from an
  agent path.
- Build any part of F9's Done-when #7 or F9a's hazard-1 guard order. Both
  are frontend work; this episode supplies the primitives only.

---

## Done when

Verbatim from WORKFLOW.md's Session 19a "You do:" steps — the manual
verification Aryan runs at the gate:

1. Run a fill-mode turn and confirm the emitted `FillProposal` carries
   `mtime` and `size`, and that they match the file **as it was when the
   context was built** — change the file on disk *after* the build but
   before the terminal event, and confirm the pair still describes the
   pre-change file.
2. `CheckFileStale` with a matching `(mtime, size)` → not stale. With the
   same mtime but a different size → **stale**. That is the check mtime
   alone cannot make, and the reason `size` is in this episode.
3. `CheckFileStale` with a `session_id` whose session has no project, over a
   path that has a `.point\` above it → confirm it is guarded against the
   SESSION's (empty) scope, not the path-resolved one. This is B12 bug #2's
   shape and the one check that proves the guard order.

---

## Verification gate (PowerShell, scratch profile)

One combined script. `APPDATA` and `LOCALAPPDATA` point at a scratch
directory for the script's whole duration; nothing under the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` is ever named. See CLAUDE.md's
hard rule for why a rename-aside pattern must never be reconstructed here.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b36"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    # the fill-mode turn and the CheckFileStale scenarios above
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Fails if:** the emitted pair describes the file *after* a post-build
change rather than the moment the context was built; a matching `mtime`
with a mismatched `size` reports `changed=False`; or a `session_id`-bearing
call is answered against a path-walked project instead of the session's own
scope.

---

## Handoff

- This episode is the backend half of two frontend gaps F9b named and
  deliberately did not approximate. It supplies primitives; it builds
  neither consumer.
- **F9's Done-when #7** is consumed by F15 (Session 19), the only episode
  positioned to populate `DiffCardOptions.RecordedMtime`/`RecordedSize`,
  because F15 writes the `EvFillProposal` decoder and constructs the first
  `DiffCard`.
- **F9a's hazard-1 guard order** is consumed by F9d (Session 19b), which
  runs after F15 because the call site it guards does not exist until F15
  adds it.
- F20 (Live Stale Detection) is a `session_id`/`size`-omitted caller and
  must keep getting its pre-B36 answer unchanged. That is the "an existing
  caller must not silently change meaning" clause in Build, step 2.
