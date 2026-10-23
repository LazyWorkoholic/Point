# F9d — Apply-Time Guard Order (F9a hazard 1) ✅

**A save that carries an agent-applied region is now resolved against the
SESSION that proposed it, not against whatever `.point\` happens to sit
above the file. `_handle_write_project_file` accepts `session_id` and
resolves the session's own scope through `_attachment_scope`, ignoring the
payload's `project_id` outright when it is present and REFUSING a session
with no project rather than downgrading to the path walk-up; the Editor
records the proposal's session on the document when an apply's splice is
proven, and sends it as `session_id` with no `project_id` beside it.
F9a's hazard 1 — "resolve the SESSION's scope first, guard against that,
and commit nothing until the guard passes" — is FULLY CLOSED.**

> **This is a doc of its own rather than a section appended to
> `F15-explainer-integration.md`, and the choice matters.** F15's doc
> claims F15's reality, and `completed/` is the only place completion is
> claimed — a doc must be entirely true or entirely absent (WORKFLOW.md's
> "Splitting an episode"). Folding this episode's claims into F15's would
> make F15's doc assert work F15 did not do, over a **backend** handler
> F15 never touched, with its own gate script, its own manual check and
> its own closure claim about F9's hazard list. The file is named
> `F09d-…` rather than `F9d-…` to match `F09a-task-tray-cards.md` and
> `F09b-diff-card.md`, which is this directory's existing convention for
> single-digit episodes.
>
> ✅ **Gate green, all four steps, re-run and confirmed interactively by
> Aryan** after the gate's own regex defect was fixed (see "The gate
> script's own two defects" below). Steps 3 and 4 were driven by hand
> through the real app.

---

## The hazard, as F9a named it, and why it stayed dormant

F9a's own hazard list requires, for any backend call an applied diff
makes: **"resolve the SESSION's scope first, guard against that, and
commit nothing until the guard passes."** The forbidden alternative is
named just as precisely: `_resolve_optional_project`'s walk-up from the
path, which auto-discovers a project from the file's own location and
therefore answers a *different question* from the one the proposal came
from. That is **B12's bug #2** — the drift `374baa2` corrected for
`AttachContext`/`ResolveAttachment` after a file was priced at ~718
tokens under one scope and refused under another.

**F9b could not close it, and did not fake it.** Its own doc
(`F09b-diff-card.md`, "The guard order (F9a hazard 1) is also
incomplete") states the position exactly: the apply splices an in-memory
buffer and makes **no backend call at all**, so there was no project
resolution in the apply path to get into the wrong order; there was no
session scope available to resolve even if there had been ("The Editor
has no session and therefore no project_id" — `protocol.go`); and Go
never received `FillProposal`, so the proposal's own `session_id` was not
in hand either. Nothing fell back to `_resolve_optional_project` to make
anything "work".

**It becomes load-bearing the moment the applied buffer is SAVED.**
F13b's write goes to `WriteProjectFile`, whose handler `_require_project`s
the editor's path-resolved project. So a saved agent-applied change would
be guarded against whatever `.point\` sits above the file rather than
against the session that produced the proposal — B12's bug #2 **on a
destructive path**. Not reachable from F9b, because nothing constructed a
`DiffCard`; reachable the moment F15 did.

**B36 (Session 19a) fixed the PRIMITIVE, not the call site.**
`CheckFileStale` gained `session_id` and resolves the session's own scope
via `_attachment_scope`. Its handoff is explicit that it did not touch
`_handle_write_project_file` and wired nothing to call the new primitive
from the apply/save path. This episode is where those two were picked up.

**F15 had already closed half of the frontend side, which is what made
this episode small enough to be sharp.** Read fresh rather than taken
from F15's task doc: F15 carries `DiffCard.SessionID` off the
`EvFillProposal` envelope and already sends it on the pre-apply
`CheckFileStale`, the apply deliberately does **not** arm autosave, and
the card closes itself on success — so the write happens only when the
user presses `ctrl+s` afterwards. **The remaining gap was the save, and
only the save.**

---

## Shape (b), not shape (a) — the decision, and why

WORKFLOW.md Session 19b offered two shapes and asked for the choice to be
made by reading F15's real save path rather than guessing ahead of it:

- **(a)** a pre-write guard call — resolve the session's scope, call the
  session-scoped `CheckFileStale` (or an equivalent containment check)
  before `WriteProjectFile` is sent, and refuse the save if it fails.
- **(b)** a direct fix to `_handle_write_project_file`'s own project
  resolution, so every write from an editor buffer with a known
  originating session resolves against that session.

**(b), for three reasons read off the real code:**

1. **(a) leaves the commit itself resolving by path.** A Go-side guard
   can refuse, but the write that follows would still be performed
   against the path-walked project. F9a's hazard is about which scope the
   *commit* happens under; a guard in front of a wrongly-scoped write is
   advice, not a guard. Under (b) the guard is not a separate call that
   could be skipped or raced — it IS which project `write_file` is
   performed against, resolved before any byte is encoded.
2. **Autosave goes through the same `saveDoc`.** A pre-write async round
   trip would have to be repeated on the timer path, which is the path
   nobody is watching when it fires. One resolution rule in the handler
   covers `ctrl+s`, autosave, and any later caller.
3. **`CheckFileStale` answers a different question.** It reports
   "changed on disk", and it reports a file outside the scope as
   *changed* — B36's own Part 2c check. Using it as a containment guard
   would conflate two answers in one boolean, and the staleness check
   already runs at apply time (F15's guard 6). (b) also follows the
   precedent exactly: B36 and `374baa2` both took `session_id` into the
   handler and resolved `_attachment_scope` there.

---

## What was built

### Backend — `runtime\runtime.py`, `_handle_write_project_file`

Additive, by the same three-case rule B36 established one event over:

1. **No `session_id`** → EXACTLY the pre-F9d path: `_require_project`
   (explicit `project_id`, else a walk-up from `path`, else refused).
   Every caller that has ever sent this event keeps its own answer, byte
   for byte — `cli.py project write-file`, F13b's ordinary saves,
   `manual_transport_encoding.py`.
2. **`session_id` given** → the project is the SESSION's own, via
   `_attachment_scope`, and **`project_id` in the payload is ignored
   outright** — not merged, not a fallback. A visible, auditable rule
   rather than a precedence order a later reader has to reverse-engineer
   from behaviour, which is the same call B36 made and for the same
   reason.
3. **A `session_id` whose session has NO project is REFUSED**
   (`ProjectNotFoundError`, naming the session), never downgraded to the
   path walk-up. Falling back would hand the caller precisely the
   resolution it asked to avoid — that is the drift, not a lenient
   version of it. It is also already this event's own rule for a bare
   path ("no project means read-only").

`_attachment_scope`'s second return value, `allow_outside_project`, is
deliberately **discarded**: `write_file` has no such parameter and must
not gain one — "an out-of-project path cannot take a lock at all…
accepting one here would manufacture exactly the writable-but-unlockable
case F13's `no .point\ means read-only` decision rules out" (its own
docstring). An outside path raises `PathDeniedError`, which is the
refusal this episode wants, not a case to permit.

`cli.py`'s `project write-file` gained `--session`, mirroring B36's
`check-stale --session`. With it, no project is resolved from `--path` at
all — requiring one would make the command stricter than the event it
sends, over a directory that may legitimately have no `.point\` above it
— and exactly one of `project_id` / `session_id` is put on the wire,
never both, because a key the receiver drops is how a reader comes to
believe the pair is merged.

### Frontend — `internal\editor`

| File | Change |
|---|---|
| `doc.go` | `agentSession string` — the session that proposed the region last applied into this buffer |
| `diffcard.go` | `applyNow` records it **after the splice is proven** (the version actually moved), never in `onDiffApply` where it was only intended; the type header's long "the guard order is incomplete" block is kept verbatim as history with a dated note saying both halves are now built |
| `reload.go` | `applyReload` clears it — the buffer is the file again, so there is no applied region left for a session to scope |
| `save.go` | `saveDoc` sends `session_id` and **no `project_id` beside it**; `scopeLabel()`; the write log line now names the scope; the file header states the three-state rule |

**`doc.agentSession` is narrow on purpose.** The Editor has its own
session (`w.sessionID`, bound to the project it resolved from the working
directory), and sending *that* on every save would have been the easy
version and a regression: a file opened from outside the Editor's project
saves correctly today through the backend's walk-up, and would have
started being refused. The field is empty for every ordinary buffer, so
every ordinary save is byte-for-byte what F13b sent.

**It is NOT cleared by `ctrl+z`.** An undo is a Splice like any other and
leaves the content matching disk, where autosave declines (`matchesDisk`)
and a manual save writes the same bytes under the same session's scope —
the correct scope either way. A reload is what clears it.

### The `onError` decode bug — found here, fixed here

`editor\protocol.go`'s `onError` decoded only `message`. The backend
sends **`error`** (`runtime.py`'s `_emit` for a `RuntimeException`:
`{"error", "code", "offer"}`), and `chat\protocol.go` has always decoded
that one. So **every** Error envelope reaching the Editor rendered as the
generic "the backend refused the request" instead of what the backend
actually said.

A pre-existing F13b defect, **found here rather than introduced here, and
fixed here rather than filed**, because this episode's own two refusals
are Error envelopes — a session with no project, and a path outside the
session's scope. F9b's own rule is that a refusal the user cannot read is
a surface that looks inert, and "the backend refused the request" over a
wrong-scope save is exactly that. `message` is kept as a second source
rather than replaced, so the older tests that send it stay meaningful
rather than merely passing.

**The sibling at `editor\invoke.go:583` was deliberately left alone.** It
is the same one-field bug on F15's agent-invocation error path (the
`EvError` branch that fills `inv.failed`). It is **not** on this episode's
write-guard path, and CLAUDE.md's "build only what the episode's Build
section lists" applies to a defect found in passing exactly as it applies
to scope creep. Fixing one and filing the other is the deliberate
asymmetry: the one on the refusal path this episode creates had to work
for the episode's end state to be true. **Open item for whoever next
touches F15's invocation path** — one line, same shape as the fix above.

---

## Verification

### Backend — `backend\tests\manual_f9d_check.py`, 16/16 PASS

Two real projects, one long-lived `Runtime`, real `WriteProjectFile`
events, and the file on disk inspected after each. **The hazard is
demonstrated both ways, which is the half that makes this more than a
lookup test:**

| | |
|---|---|
| no `session_id`, correct `project_id` | written; bytes land (pre-F9d path, unchanged) |
| no `session_id`, WRONG `project_id`, with a precondition | answered about **project B's own** `region.txt` (`stale`) — not A's file |
| **no `session_id`, WRONG `project_id`, no precondition** | **SUCCEEDS — a file created under the wrong project, silently.** This is B12's bug #2 with bytes on disk at the end of it |
| same request **with** `session_id=A` | answered about A's real file (`exists`), and creates nothing under B — the scope decided WHICH FILE the question was about |
| `session_id=A` beside `project_id=B` | written under A's scope; nothing appears under B's root |
| `session_id=B` over a file in A | **refused** — the session's scope guards it, even though a walk-up from that path would have allowed it |
| `session_id` whose session has no project | **refused**, naming the session; no fallback write happened |
| a wrong `expected_mtime` under session scope | still `stale` — B32's own guards are unchanged underneath |

`manual_b36_check.py` was re-run alongside it and still passes: this
episode leans on B36's primitive, so if `_attachment_scope`'s resolution
rule ever moves, both events are wrong together.

### Frontend — `go test .\frontend\... -count=1` green

`gofmt -l .\frontend\` empty, `go vet .\frontend\...` clean. **412 tests
in `editor`** (F15 closed at 404; the 8 new ones are
`guardorder_test.go`), 9 in `diff`, all ten packages green.

`guardorder_test.go` asserts which scope each case sends, and the three
cases that must NOT send one:

1. a save after an agent apply carries `session_id` **and no
   `project_id`**;
2. an ordinary save is unchanged — `project_id`, no session;
3. **a file outside the Editor's project still walks up** — the
   regression that sending `w.sessionID` on every save would have caused;
4. a REFUSED apply records nothing (the origin is set where the splice is
   proven — F9a's hazard 3 applied to this field);
5. a reload drops it and the ordinary rule comes back;
6. autosave carries the same scope (the same `saveDoc`, asserted rather
   than assumed);
7. a refused write says what the backend said, for both payload keys;
8. `scopeLabel` names all three states.

### The gate — `backend\tests\gate_f9d.ps1`, all four steps PASS

Step 0 runs gofmt/vet/the Go suite; Step 1 runs both Python checks; Step 2
builds into a scratch profile; **Steps 3 and 4 were driven by hand** in
the real app under Demo mode. The two log lines that are this episode's
end-to-end evidence, from Aryan's own green run:

```
Step 3 (apply, then ctrl+s)   scope="session a1565a58-... (agent-applied region)"
Step 4 (ordinary edit + save) scope="project 8e6fb1c3-..."    (no session anywhere)
```

**What the gate deliberately does NOT cover, stated in its own footer:**
the cross-project mismatch itself has no path through the UI — the
Editor's tree lists one project and its session is bound to the project
resolved from the working directory, so no keystroke sequence produces
the mismatch. Step 3 proves the real app *sends* the session scope;
`manual_f9d_check.py` proves that scope is the *correct* one. Two
different claims, two instruments.

### The gate script's own two defects, both found before the green run

Recorded because the gate is this episode's own artifact, and because
both are cheap mistakes with expensive failure modes.

1. **A backtick mis-escape made the file unparseable.**
   `Expect @("ctrl+s reports `saved app.py`")` — **backtick is
   PowerShell's escape character**, so the trailing one escaped the
   closing quote, the string ran on, and PowerShell reported "Unexpected
   token 'editor:'" four lines later plus a missing string terminator
   fifty lines after that: two errors, neither at the line responsible,
   both pointing at correct code. Diagnosed by reading the actual bytes
   (`Format-Hex` and a raw read) rather than the console, per WORKFLOW's
   encoding policy — which also **ruled out** the first hypothesis, a
   Unicode smart quote: the only non-ASCII bytes in the file were the
   deliberate BOM at offset 0. It was Markdown emphasis written into a
   PowerShell string literal. The file now carries that rule in its own
   header, and a sweep of every other `.ps1` in `backend\tests\`
   confirmed their backticks are all deliberate escapes (`` `r`n ``,
   `` `" ``, `` `$ ``).
2. **A false FAIL from four inconsistent regexes for one field.** All
   three scope values contain a space, so the logger quotes the value:
   `scope="project 400f5426-..."`. Step 3's two patterns used `.*` and so
   swallowed the opening quote **by luck**; Step 4's literal
   `scope=project ` could therefore never match a correct run and
   reported FAIL over behaviour that was right — the worst failure mode
   an assertion has — and its second pattern, a bare `session `, would
   have matched that word anywhere on the line. Fixed by replacing all
   four with **one parser**, `ScopeOf`, and comparing its result; a
   change to the logger's quoting now breaks one function and nothing
   else. Both steps also print the parsed value beside each raw line, so
   a future failure is readable without re-deriving what the assertion
   saw.

The lesson that generalises, and it is the same one `WroteAnything`
already carries in `gate_f15.ps1`: **an assertion that cannot match is
worse than no assertion**, in both directions — F15's could never fail
and silently certified three rounds of claimed fixes; this one could
never pass and condemned a correct run.

---

## What this closes, and what it does not

### F9a's hazard 1 is FULLY CLOSED

Both halves are built, verified and green: the scope is resolved from the
session that produced the proposal, and the write commits under that scope
or not at all. There is no remaining part of hazard 1 deferred, stubbed,
approximated or scheduled elsewhere. `completed/frontend/INDEX.md`'s F9b
row, which pointed at this session as the open item, now says closed.

With it, **both things B36 unblocked are now built**: Done-when #7 by F15
(as its 7b), and hazard 1 here.

### F9's packaging decision was NOT touched and NOT re-argued

One diff card shared by Chat and the Editor — "same card, same keys, in
both workspaces" — **remains OPEN on its own schedule, F9's to settle**,
exactly as F9b's handoff, F15's own note and `tasks/frontend/INDEX.md`
already state. Nothing in this episode read, moved or re-litigated it:
the fix is a project-resolution rule in one backend handler plus one
field on `doc`, and it is indifferent to where the card eventually lives.

### Open items this episode leaves behind

1. **`editor\invoke.go:583`'s `message`-vs-`error` decode** — the
   sibling of the bug fixed here, on F15's agent-invocation path, left
   alone deliberately (see above). One line.
2. **`manual_b28_demo_mode.py` check #8** is still false and still
   predates everything — B24 made a `FillProposal` real and nobody
   revisited B28's "none was emitted, by construction". Flagged by B36,
   by F15, and now a third time. Unchanged here; it is neither episode's
   scope and wants a decision, not a one-line edit.
3. **F9 itself is still not complete**: F9c (Session 23b — attach and
   save) and the packaging decision remain. F9a's and F9b's rows stay 🟡.

### For anyone adding a write path later

`session_id` on `WriteProjectFile` is for a buffer that holds content a
**specific session's agent proposed**, and the originating session is
recorded where the mutation is proven, not where it is attempted. If a
later surface applies agent content into a buffer by some other route, it
sets `doc.agentSession` at that point or the write silently reverts to
the path-walked scope — the very drift this episode closed. The three
states are listed in `save.go`'s own header, and `scopeLabel` names each
one in the log so a run can be audited after the fact.
