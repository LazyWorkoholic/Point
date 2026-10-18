# Episode B36 — Generation-Time Staleness for the Filler's Proposal ✅

**Two additive wire changes, both named by F9b's own handoff as belonging
here. (1) `FillProposal` now carries `(mtime, size)` stat'd from the exact
file descriptor `_render_editor_context` reads the region from — not a
separate `stat()` call before or after the read, which would leave a race
window an external write could land in. (2) `CheckFileStale` accepts
`session_id` and `size` as real, additively-optional comparison fields:
`session_id`, when given, resolves the SESSION's own scope
(`_attachment_scope`) instead of `_resolve_optional_project`'s path-walk-up
— the same correction `374baa2` made for `AttachContext`/`ResolveAttachment`
after B12's bug #2, now extended here. Neither change touches B24's five
reason slugs, its validation rules, or what any pre-existing caller gets
when it supplies only what it always supplied.**

> Verified by this session, in a scratch `%APPDATA%`/`%LOCALAPPDATA%`
> profile per CLAUDE.md's hard rule: `tests\manual_fill_proposal_test.py`
> (unit-level, 16/16 PASS) and a new `tests\manual_b36_check.py` (end to
> end, through one long-lived `Runtime` — real `SendMessage` → fill-mode
> task → real `FillProposal` event, and six `CheckFileStale` scenarios).
> Full logs in "Manual Test Log" below. **Aryan has not yet re-run this
> independently** — this doc records the build-and-self-test half of the
> usual closure bar, same as every episode's first pass.

---

## Where the code went

```
backend\
├── managers\
│   ├── context.py           _render_editor_context (context.py:642) now
│   │                        reads via `path.open("rb")` + `os.fstat(f.
│   │                        fileno())` on the SAME descriptor (context.py:
│   │                        672-676), instead of `path.read_text()` with
│   │                        no stat at all. (mtime, size) folded into the
│   │                        existing `detail` string (context.py:726-727),
│   │                        the only channel this function has ever had
│   │                        to report what it read
│   ├── fill_proposal.py     FillProposal (fill_proposal.py:54) gained
│   │                        `mtime: float`, `size: int` (lines 73-74) —
│   │                        nine fields now, all travel together in
│   │                        to_dict(). parse_fill_proposal() (line 103)
│   │                        takes them as two new required keyword
│   │                        params, passed through unvalidated (B36 does
│   │                        not validate them — CheckFileStale does, on a
│   │                        LATER read)
│   └── project_manager.py   check_stale() (project_manager.py:590) gained
│                            `recorded_size: Optional[int] = None`. When
│                            given, compared; when omitted, the function is
│                            byte-for-byte what it was before this episode
├── streaming\
│   └── manager.py           RegisteredEditorContext (manager.py:21) gained
│                            `mtime: float`, `size: int` (lines 38-39).
│                            register_editor_context() (line 71) takes them
│                            as two new positional params
├── agents\
│   └── executor.py          `_shown_range_from_built` renamed
│                            `_region_stat_from_built` (executor.py:109) —
│                            same technique (regex over the task_state
│                            IncludedItem's `detail`), extended with
│                            `_REGION_STAT_RE` (line 106) to also pull
│                            `mtime=`/`size=` out of the same string. All
│                            four values or none — a `detail` matching one
│                            pattern but not the other is treated as "no
│                            match", never a partial proposal
├── runtime\
│   └── runtime.py           `_push_fill_proposal` forwards `ec_record.
│                            mtime`/`.size` into parse_fill_proposal().
│                            `_handle_check_file_stale` (runtime.py:860)
│                            branches on `session_id`: present →
│                            `_attachment_scope(session_id)`; absent →
│                            exactly the pre-B36 `_resolve_optional_project`
│                            + payload `allow_outside_project` path
├── cli.py                   `project check-stale` gained `--size` and
│                            `--session`, both optional, for manual use
└── tests\
    ├── manual_fill_proposal_test.py   updated for the new required
    │                        `mtime`/`size` params on parse_fill_proposal()
    │                        and register_editor_context(); added rows
    │                        asserting the pair travels through to_dict()
    │                        and RegisteredEditorContext unchanged
    └── manual_b36_check.py  NEW — end-to-end, one long-lived Runtime
                             (same construction as manual_b26/b30/b31, for
                             the same reason: needs to observe a real
                             fill-mode task run and several CheckFileStale
                             calls against state it itself set up)
```

---

## The wire contract — read this, not the diff, before building against it

F9b's deferred half (WORKFLOW.md Session 19/F15) and F20 (Live Stale
Detection) both call these. This section is the contract; the code above is
the implementation.

### `FillProposal` — now nine fields, all-or-nothing

```python
{
    "file": str,            # as the model returned it, before resolution
    "start_line": int,      # 1-based inclusive
    "end_line": int,        # 1-based inclusive
    "replacement": str,
    "note": Optional[str],
    "shown_start": int,      # unchanged since B24
    "shown_end": int,        # unchanged since B24
    "mtime": float,          # NEW (B36) — os.fstat().st_mtime, same units/
                             # precision as ReadProjectFile's own `mtime`
                             # (B7/B32) and CheckFileStale's own `mtime`
                             # param: a float count of seconds, compare
                             # with the existing 1e-6 tolerance, never
                             # truncate to int
    "size": int,             # NEW (B36) — os.fstat().st_size, bytes
}
```

Confirmed live by this session's own fixture (`manual_b36_check.py`): a
3-line file, fill-mode turn via the `echo` provider, the printed
`FillProposal.proposal` carried `mtime=1792360523.4474747`,
`size=30` — matching `os.stat()` run independently, in a separate
process-local check, against the same untouched file.

**Both fields are captured once, at the SAME region-generation disk read
that produced `shown_start`/`shown_end`.** Never re-stat the file to
"refresh" them, and never substitute a value from any other moment (a
buffer's open-time or last-save-time stat, an editor's own cached mtime).
F9b's own doc records in detail why that specific substitution fails in
both directions — read "Done-when #7 is absent, not approximated" there
before building a consumer that is tempted to shortcut this.

### `CheckFileStale` — the full signature, and what each caller shape gets

**Payload (all keys optional except `path`/`mtime`):**

```python
{
    "path": str,                        # REQUIRED
    "mtime": float,                      # REQUIRED — the recorded mtime
    "size": Optional[int] = None,        # NEW (B36)
    "session_id": Optional[str] = None,  # NEW (B36)
    "project_id": Optional[str] = None,  # unchanged since B7 — ignored
                                         # when session_id is given, see below
    "allow_outside_project": Optional[bool] = False,  # unchanged since B7
                                         # — ignored when session_id is
                                         # given, see below
}
```

**Reply:** unchanged, `{"changed": bool}`.

**The three cases, decided once, in this order — a caller supplying only
what it has always supplied gets EXACTLY its pre-B36 answer:**

1. **`session_id` omitted (every caller before B36, and
   `cli.py project check-stale` whenever `--session` is not passed):**
   scope resolves via `_resolve_optional_project(payload)` — explicit
   `project_id` if given, else a path-walk-up from `path`, else no project
   — and `allow_outside_project` is read straight off the payload, exactly
   as `_handle_check_file_stale` has always worked. **Zero behaviour
   change for this shape of call.**
2. **`session_id` given:** scope resolves via `_attachment_scope
   (session_id)` — the session's OWN `project_id` (`SessionMemoryService.
   project()`), and `allow_outside_project` from the live config key
   `project.allow_outside_project`, exactly like `AttachContext`/
   `ResolveAttachment`'s own calls to the same helper. **`project_id` and
   `allow_outside_project` in the payload are both ignored outright** —
   not merged, not used as a fallback. A caller that knows which session
   produced the recorded stat is answered against that session's real
   scope, never a different one a stale or unrelated `project_id` would
   walk to. This is the fix for the B12 bug #2 shape: a file priced (or, on
   this event, refused) under one scope and answered under a different one.
3. **`size` (either case above):** omitted → mtime alone, 1e-6 tolerance,
   identical to every pre-B36 comparison. Given → current size must ALSO
   match (`stat().st_size`, byte-exact, no tolerance) — a false "unchanged"
   that `size` exists specifically to rule out is a filesystem whose mtime
   granularity let a rewrite reuse the old timestamp while the length moved.

**What this means concretely for F9b's deferred half:** a caller that wants
the full B36 guarantee sends `session_id` (the session that produced the
`FillProposal`), `mtime` and `size` straight from that event's `proposal`
object, and `path` resolved relative to that same file. A caller that only
has `mtime` (F20, today — see "What the next episode inherits" below) gets
a real, unchanged answer; it is simply answering a narrower question.

---

## Key decisions

### 1. `os.fstat(f.fileno())`, not `path.stat()` before or after `read()`

The task's own charter named the hazard precisely: "stat the file at the
moment it is read, not before and not after." `_render_attachment`
(`context.py`, unchanged by this episode) stats, THEN reads — two separate
syscalls, a window between them. For the editor context this episode
closes that window structurally: `path.open("rb")` opens one file
descriptor, `f.read()` and `os.fstat(f.fileno())` both operate on that same
descriptor, so the bytes decoded into `text` and the stat captured are
guaranteed to describe the same moment, not merely usually the same moment.

Switching from `path.read_text(encoding="utf-8", errors="replace")` to
manual `bytes.decode("utf-8", errors="replace")` drops Python's universal-
newline translation, but nothing downstream notices: `str.splitlines()`
already treats `\r\n`/`\r`/`\n` identically, and every consumer of the
split lines rejoins them with a literal `"\n"` regardless of what the
original line ending was. Confirmed by the unchanged BOM-stripping check
(`manual_b28_demo_mode.py` #7/#8, both still pass unmodified — see "Found
during verification, out of scope" below for the one check in that script
that does NOT still pass, and why).

### 2. All four values travel together in `_region_stat_from_built`, not independently

`_shown_range_from_built` became `_region_stat_from_built` rather than
gaining a sibling function, because `shown_start`/`shown_end` and `mtime`/
`size` live in the exact same `detail` string, found by the exact same
loop over `built.log.included`. Two functions walking that list twice to
extract two halves of one fact would invite the two ever disagreeing about
which `task_state` item they found. A `detail` that matches the "lines"
pattern but not the "mtime=/size=" pattern (or vice versa) is now treated
identically to no match at all — logged once, no `FillProposal` — the same
discipline B24 already applied to the unparsed-reply case.

### 3. `CheckFileStale`'s `session_id` wins outright over `project_id`, never merges

Modelled directly on `_attachment_scope`'s own docstring reasoning: "Not
the caller's choice, and deliberately not a per-request flag." Letting a
caller supply both and have `session_id` "win" by precedence would still
let `allow_outside_project` leak through from the payload on a
session-scoped call, re-opening exactly the drift this episode exists to
close. Ignoring both payload fields outright when `session_id` is present
is a visible, auditable rule rather than a quiet precedence order a future
reader has to reverse-engineer from behaviour.

### 4. `recorded_size`, not `size`, as `check_stale`'s own parameter name

`project_manager.py` already uses `recorded_mtime` for the caller-supplied
value compared against a fresh `stat()`. Naming the new parameter to match
(`recorded_size`) keeps the pairing obvious at the one call site that reads
both; the wire payload key stays the shorter `size`, matching `mtime`'s own
wire-vs-parameter asymmetry already established by this function.

---

## Manual Test Log (Episode B36)

Run from inside `backend\`, against a scratch `%APPDATA%`/`%LOCALAPPDATA%`
profile (both env vars set in the same PowerShell script, per CLAUDE.md):

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b36"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    uv run python tests\manual_fill_proposal_test.py
    uv run python tests\manual_b36_check.py
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

| Check | Result |
|---|---|
| `manual_fill_proposal_test.py` — full B24 validation table, shape, fences/prose, unaffected by the new required params | ✅ 16/16 PASS |
| `manual_fill_proposal_test.py` — `mtime`/`size` travel through `to_dict()` unchanged | ✅ |
| `manual_fill_proposal_test.py` — `mtime`/`size` carried on `RegisteredEditorContext` beside `shown_start`/`shown_end` | ✅ |
| `manual_b36_check.py` Part 1 — real `SendMessage` (echo provider, fill mode, `--cursor`) reaches `TaskDone` then a real `FillProposal` | ✅ |
| `manual_b36_check.py` Part 1 — proposal's `shown_start`/`shown_end` unchanged (1, 3) | ✅ |
| `manual_b36_check.py` Part 1 — proposal's `mtime` equals the file's real `os.stat().st_mtime`, read independently after the task finished | ✅ (`1792360523.4474747` both sides) |
| `manual_b36_check.py` Part 1 — proposal's `size` equals the file's real `os.stat().st_size` | ✅ (`30` both sides) |
| `manual_b36_check.py` Part 1 — exactly nine `FillProposal` fields, none missing, none extra | ✅ |
| `manual_b36_check.py` Part 2a — no `session_id`/`size`: unchanged file → `changed=False`; wrong `mtime` → `changed=True` (pre-B36 behaviour, byte-for-byte) | ✅ both |
| `manual_b36_check.py` Part 2b — real `mtime` + deliberately WRONG `size` → `changed=True` (size is actually compared, not accepted and ignored) | ✅ |
| `manual_b36_check.py` Part 2b — real `mtime` + real `size` → `changed=False` | ✅ |
| `manual_b36_check.py` Part 2c — `project_id` pointing at the WRONG project (B), no `session_id` → `changed=True` (file doesn't exist under B's root — the pre-B36 code path, which this proves still misbehaves exactly as B12 bug #2 predicts when nobody supplies a session) | ✅ |
| `manual_b36_check.py` Part 2c — same request, `session_id` naming a session bound to the RIGHT project (A) → `changed=False` (session scope wins outright over the wrong `project_id`) | ✅ |

All thirteen B36-specific checks PASS. Full raw envelope dumps are in this
session's own transcript, not reproduced here — the table above is the
complete set of assertions, not a summary of a larger log.

---

## Found during verification, out of scope — flagged, not touched

**`tests\manual_b28_demo_mode.py` check #8 ("no FillProposal event exists on
the wire — none was emitted, by construction") is now FALSE, and was
already false on clean `main` before this episode touched anything.**

Confirmed by `git stash`: with every B36 change stashed, re-running
`manual_b28_demo_mode.py` against clean `main` still fails that exact
check, with the exact same evidence — a real `FillProposal` event, parsed
correctly, `start_line=4, end_line=8`, matching `app.py`'s real content.

The root cause predates this episode entirely. `B28-demo-mode-provider-
fixtures.md` (2026, before B24 shipped) built `filler_fill.json` to
deliberately replay a plain JSON-shaped reply with **no** `FillProposal`
event, because B24 did not exist yet — its own words: "The day B24 ships,
re-read 'Why this episode exists' before touching this fixture... emitting
the actual event is B24's wire change, not a Demo-mode change." **B24 has
since shipped** (`completed/backend/B24-filler-proposal-event.md`), and
nobody revisited this assertion when it did. The fixture's own JSON content
was built to become a valid proposal with zero edits — it says so in its
own completed doc — so the moment B24's `_push_fill_proposal` started
running unconditionally for every fill-mode task (gated only on
`editor_context.mode == MODE_FILL`, never on which provider is running),
this check's "none was emitted" became false. This is the same class of
drift CLAUDE.md:338 is wrong about (claims no file-write event exists;
`WriteProjectFile` has existed since B32) and the same class as
`tasks/backend/INDEX.md`'s own Status column drift the file's own header
now warns about ("B12 through B23 all read 'not started' while their
completed docs were on disk") — a claim that was true when written,
contradicted by a LATER episode, and never revisited.

**Not fixed here.** Fixing a different episode's test assertion is not
this episode's Build section, and B28's own test is explicitly kept "as
regression coverage" — changing its expectation needs a decision about
whether `manual_b28_demo_mode.py` should now assert a real `FillProposal`
arrives (matching reality) or whether Demo mode's `@filler-explainer` fill
path should be special-cased to suppress one (preserving D55's "no
capability Point doesn't have" framing for a provider that, as of B24,
genuinely does have it). That is a real design question, not a one-line
fix, and it belongs to whoever next touches B28 or this check — your call
when to address it.

---

## What the next episode inherits

### F9's Done-when #7 — UNBLOCKED, still NOT BUILT (frontend work)

The backend half of #7 ("refuse to apply when the file changed on disk
since the region was generated") is now real: `FillProposal` carries the
exact `(mtime, size)` pair, and `CheckFileStale` can compare it. **Nothing
on the Go side has been changed.** `frontend\internal\editor\diffcard.go`'s
`DiffCardOptions.RecordedMtime`/`RecordedSize` still exist, still default to
zero, and are still read by nothing — F9b's own tripwire test
(`TestTheStalenessPairIsCarriedAndUnpopulated`) still asserts exactly that,
unchanged by this episode. `WORKFLOW.md` Session 19 (F15) already has this
scheduled and already names the shape precisely ("Done-when 7b is this
episode's proof that F9's Done-when #7... is finally closed"), because F15
is the first (and only) episode that writes the Go `EvFillProposal` decoder
and constructs a real `DiffCard` — there is no earlier point in the Go tree
where a real `(mtime, size)` pair could be populated. **This episode is
exactly what Session 19 was waiting on; nothing further is needed from the
backend for #7.**

### F9a's hazard-1 guard order — UNBLOCKED, still NOT BUILT, and NOT yet scheduled anywhere

This is a different gap from #7 and must not be conflated with it. F9b's
own doc named it precisely: the moment an applied diff is **saved**,
`WriteProjectFile`'s handler (`runtime.py`'s `_handle_write_project_file`)
resolves its project via `_require_project` → `_resolve_optional_project` —
the editor's own path-walked project, not the session that produced the
proposal. That is B12 bug #2 on a destructive path, and it was "not
reachable" from F9b only because nothing called `WriteProjectFile` from an
agent-applied diff yet.

**This episode gives the GUARD a correctly-scoped tool to call
(`CheckFileStale(session_id=...)`), but does not call it from anywhere, and
does not touch `_handle_write_project_file`'s own resolution.** Two
separate things remain open, and neither is scheduled in `WORKFLOW.md`
Session 19 (F15)'s current Paste block, which was checked directly and
mentions neither "hazard" nor a guard-order fix:

1. Whoever wires the apply-then-save path (F15, since it is the first real
   producer) must decide whether the fix is a pre-write guard call (resolve
   the session's scope, call the now-session-scoped `CheckFileStale`,
   refuse before any write is attempted) or a direct fix to
   `_handle_write_project_file`'s own project resolution (resolve via the
   session that owns the open document, not the path it happens to sit
   under). F9b's own "guard order" framing ("resolve the SESSION's scope
   first, guard against that, and commit nothing until the guard passes")
   describes the pre-write-guard shape; this episode does not pick between
   the two, because picking requires the real call site F15 adds, which
   does not exist yet.
2. Nothing in this repo's `WORKFLOW.md` currently schedules doing either.
   A new session has been added for exactly this — see below.

**`WORKFLOW.md` gained SESSION 19b**, immediately after Session 19 (F15)
and before Session 20, scoped narrowly to this one guard-order fix (not a
restatement of F9's packaging decision, which stays explicitly unscheduled
— "F9's to settle, on F9's own schedule" — Session 19's own text, unchanged
by this episode).

### `design/completed/frontend/INDEX.md`'s F9b row

Updated to point at this episode by name as the backend unblocker for #7,
and to state the hazard-1 status precisely (unblocked, not built, not yet
scheduled until this episode's own Session 19b addition) rather than
leaving the prior wording ("That gap is now B36") to go stale the moment
this episode landed, the way CLAUDE.md's own layout section and
`tasks/backend/INDEX.md`'s Status column have each gone stale before.

### F20 (Live Stale Detection, Session 22b) — a narrower but real contract user

F20's own task doc describes "zero backend work" because `CheckFileStale`
already existed end to end before this episode. That remains true — F20
does not need `session_id` or `size` to do its job (a polled external-edit
notice for an OPEN document, which already has its own `doc.mtime` from the
read that opened it and needs no byte-count to detect "something changed").
**F20 is one of the "`session_id`/`size` omitted" callers this episode's
contract section above describes explicitly**, and gets the unchanged,
pre-B36 answer. Recorded here only so a later reader of F20's own completed
doc does not wonder whether it should have adopted the new fields and
didn't.

### `manual_b36_check.py` is a real regression script, kept in the repo

If a later episode changes `FillProposal`'s field set, `CheckFileStale`'s
payload shape, or `_attachment_scope`'s own resolution rule, re-run it
rather than assuming these thirteen checks still hold — the same posture
`manual_b30_set_session_project.py`'s own handoff already states for itself.
