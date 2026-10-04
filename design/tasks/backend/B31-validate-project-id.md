# Episode B31 — Validate `project_id` in `CreateSession`/`SetSessionProvider`

**Neither `CreateSession` nor `SetSessionProvider` (B30) has ever checked
that an incoming `project_id` actually matches a row in `projects`. Found
live, during the same session that recorded B30/F10c: deleting
`backend`'s working database (`runtime.db*`) while `state.json` still held
a stored Demo default from before produced a session —
`a7b1a727|demo|b93dbd75-...` — bound to a `project_id` that matched
nothing, with no error anywhere. This episode adds the missing check to
both handlers, with two different, deliberately asymmetric behaviours for
an unknown id: `CreateSession` treats it as no project at all (never
blocks a session — including the very first one — from being created);
`SetSessionProvider` leaves whatever binding already existed untouched.
Neither refuses the request outright.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B31-validate-project-id.md`, once it exists)
> claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. One new, narrow, side-effect-free existence check and
two call sites that use it. No new wire event, no new reply field, no
schema change.

**Depends on:** B7 (`ProjectManager`), B12
(`session_manager.set_project`), B30 (`SetSessionProvider`'s `project_id`
field — this episode closes the same gap in both of B30's own call
sites, not just the one `CreateSession` already had before B30 existed).

**Found during:** the B30/F10c recording session, via a live test run
*after* both were already recorded as complete — not part of either
episode's own verification gate, and does not reopen either. Spun out as
its own episode, the same discipline B23–B30 already established.
Recorded as a parking-lot entry first
(`design/tasks/backend/INDEX.md`), then written up as its own task doc
once a concrete fix shape was decided.

---

## Why this episode exists

### The repro, and what it actually proved

Select Demo mode (binds a session to the shipped sample project via
`ProjectInit` + the B30 retrofit path). Delete `runtime.db*` only — not
`state.json`. Run `cli.py migrate` against the now-empty database.
Relaunch. Result: `sessions = a7b1a727|demo|b93dbd75-...`, `projects` is
empty. The app loaded normally, with no error anywhere. Full account in
`design/completed/frontend/F10c-settings-demo-mode.md`'s Decision 1
correction and `design/completed/backend/B30-set-session-project.md`'s
own "What the next episode inherits."

The session reached `CreateSession` through `onSessionList` (F10c's
Decision 1 forwarding — `frontend\internal\chat\protocol.go:391-398`),
which fires on any empty session list while `state.json` still holds a
stored Demo default. That path was never the bug; the bug is that nothing
on the receiving end ever checked the id it was handed.

### Read first, as asked: does either handler, or `session_manager`,
### validate today? No — confirmed by reading all three, not assumed.

**`_handle_create_session`** (`backend\runtime\runtime.py:374-396`) reads
`p.get("project_id")` at line 386 and passes it straight into
`self.sessions.create(...)` with no check in between.

**`SessionManager.create()`** (`backend\managers\session_manager.py:51-68`)
passes `project_id` straight into `Session.new(...)` (line 65) and inserts
— no check there either.

**`_handle_set_session_provider`** (`backend\runtime\runtime.py:631-650`,
B30's own handler) checks only truthiness:

```python
project_id = event.payload.get("project_id")
if project_id:                                    # line 639
    await self.sessions.set_project(session_id, project_id)
```

— `if project_id:` is a non-empty-string check, not an existence check.
B30's own task doc never claimed otherwise (its own Build §1 is explicit
that the one guard it deliberately omits is `_commit_adoption`'s
NULL-only rule — existence was never discussed, by either doc, until this
one). **Confirms the instruction's own suspicion: yes, B30's path has the
identical gap.**

**`SessionManager.set_project()`** (`backend\managers\
session_manager.py:112-125`) calls `self._ensure_exists(session_id)` —
checking the **session** exists — then unconditionally `UPDATE`s
`project_id`. Nothing checks the **project** exists.

**The schema itself has no safety net either** — confirmed already in the
parking-lot entry this episode formalizes:
`database\migrations.py`'s `sessions.project_id` column (lines 58, 109)
carries no `FOREIGN KEY`/`REFERENCES` constraint, unlike every
`session_id` column in this schema that references `sessions(id)`
(`database\migrations.py:74, 145, 223, 324`, each `... REFERENCES
sessions(id) ON DELETE CASCADE`). SQLite would not have caught this even
if every Python-level check above were skipped.

### The one existing validation primitive, and why it is not reused as-is

`ProjectManager.resolve(explicit_project_id=...)`
(`backend\managers\project_manager.py:104-108`) already raises
`ProjectNotFoundError` for an unknown explicit id — the obvious thing to
reach for. **Rejected for this check, specifically:** a *hit* on
`resolve()` calls `self._touch()` (`project_manager.py:147-154`), which
unconditionally `UPDATE`s the project's `last_opened_at` and overwrites
`root_path` — a real write, every time, on every `CreateSession` or
`SetSessionProvider` call that merely carries a *valid* `project_id`.
That may or may not be desirable as its own feature, but it is not what
"does this id exist" should silently also do, and introducing it as a
side effect of a validation check this episode was not asked to design
would be exactly the kind of scope creep CLAUDE.md's hard rules warn
against. See Build §1 for the narrow alternative.

---

## Decision — behaviour for an unknown `project_id`, asymmetric by design

**`CreateSession`: treat it as no project.** Null the field before
`SessionManager.create()` is ever called (not after — the row must never
contain the bad value even transiently), log one `_log.warning(...)`
naming the rejected id, and let the reply's `project_id` reflect the
session's real, persisted value (empty), the same "the backend's own echo
is the only claim this package treats as true" discipline B30 already
applied to `SetSessionProvider`'s reply. **Why not refuse:** `CreateSession`
is the one event that creates a profile's very first session
(`onSessionList`'s empty-list branch) — exactly the path the repro above
exercised. Refusing it outright over a stale, dangling `project_id` would
mean a wiped or restored database can never bootstrap its first session
again until a human notices and fixes `state.json` by hand, which is a far
worse failure than silently starting that one session unbound. An unbound
session is a known, already-handled state (F10c's own status line,
`chat\cards.go:139`); a profile that cannot create a session at all is not
a state this codebase has any recovery path for.

**`SetSessionProvider`: leave the existing binding untouched.** Skip the
`set_project()` call entirely, log a warning naming the rejected id, and
let the reply's `project_id` — already re-read from the session after the
write, per B30 — reflect whatever it already was. **Why not refuse (raise
an `Error` event) and why not null an EXISTING binding either:** this
handler's whole reason for existing is retrofitting an **already-running**
session non-disruptively; a bad `project_id` arriving alongside a good
`provider`/`model` should not turn a routine retrofit into a wire-level
error, and it should certainly not use a bad incoming value as a reason to
destroy a binding that already worked. "Leave it exactly as it was"
is the one behaviour that can never make an existing session worse off.

Both behaviours share the same rule, stated once: **a `project_id` that
does not exist is treated as if it had not been sent at all**, on both
paths, never as grounds to refuse the surrounding request.

---

## Build

### 1. `backend\managers\project_manager.py` — `ProjectManager.exists()` (NEW)

A pure existence check, no touch, no write, placed near `resolve()`:

```python
async def exists(self, project_id: str) -> bool:
    """A validation-only check -- no touch, no root_path rewrite, no
    write of any kind on a hit or a miss. `resolve(explicit_project_id=
    ...)` was considered and rejected for this: a hit calls `_touch()`,
    which unconditionally bumps `last_opened_at` and rewrites `root_path`
    -- a real DB write this check must not cause on every CreateSession/
    SetSessionProvider call that merely carries a project_id (B31)."""
    return (await self._repo.fetch_by_id(project_id)) is not None
```

### 2. `backend\runtime\runtime.py` — `_handle_create_session`

```python
async def _handle_create_session(self, event: IncomingEvent) -> None:
    """..."""
    p = event.payload
    project_id = p.get("project_id")
    if project_id and not await self.projects.exists(project_id):
        _log.warning(
            "CreateSession: project_id %r matches no project; creating without one",
            project_id,
        )
        project_id = None
    session = await self.sessions.create(
        title=p.get("title", "New Session"),
        provider=p.get("provider"),
        model=p.get("model"),
        workspace=p.get("workspace"),
        project_id=project_id,
        settings=p.get("settings"),
        effort=p.get("effort"),
        created_by=p.get("created_by"),
    )
    await self._emit(OutgoingEvent(
        type=OutgoingEventType.SESSION_CREATED,
        request_id=event.request_id,
        payload={"session": self._session_to_dict(session)},
    ))
```

The reply needs no separate re-read the way `SetSessionProvider`'s does —
`_session_to_dict(session)` already reads the in-memory `session` object
`SessionManager.create()` returned, and that object's `project_id` is
already the corrected (possibly nulled) value, since the null happens
*before* `.create()` is called.

### 3. `backend\runtime\runtime.py` — `_handle_set_session_provider`

```python
async def _handle_set_session_provider(self, event: IncomingEvent) -> None:
    session_id = event.payload.get("session_id")
    provider = event.payload.get("provider")
    model = event.payload.get("model")
    if not session_id or not provider or not model:
        raise InvalidEventError("SetSessionProvider requires 'session_id', 'provider', and 'model'")
    await self.sessions.set_provider(session_id, provider, model)
    project_id = event.payload.get("project_id")
    if project_id:
        if await self.projects.exists(project_id):
            await self.sessions.set_project(session_id, project_id)
        else:
            _log.warning(
                "SetSessionProvider: project_id %r matches no project; binding left unchanged",
                project_id,
            )
    session = await self.sessions.get(session_id)
    await self._emit(OutgoingEvent(
        type=OutgoingEventType.SESSION_PROVIDER_UPDATED,
        request_id=event.request_id,
        payload={
            "session_id": session_id, "provider": provider, "model": model,
            "project_id": session.project_id,
        },
    ))
```

The re-read (`session = await self.sessions.get(session_id)`) already
existed from B30 and needs no change — it already echoes "whatever the
session's real, current value is," which is exactly "unchanged" when the
`exists()` check fails.

---

## Do NOT

- **Do not touch the frontend at all.** Neither handler's wire shape
  changes (same fields in, same fields out) — only what happens to an
  invalid `project_id` internally. Nothing Go-side needs to change or be
  re-tested beyond confirming it still isn't touched.
- **Do not raise/refuse on an unknown `project_id` in `CreateSession`.**
  Decided and justified above — refusing would block a profile's very
  first session on exactly the kind of wiped/restored database this
  episode's own repro produced.
- **Do not null or otherwise touch an EXISTING binding in
  `SetSessionProvider` when the incoming `project_id` is invalid.** Only
  skip writing the new one; a session that already had a valid binding
  must keep it, untouched, warning or not.
- **Do not reuse `ProjectManager.resolve()` for this check.** It touches
  (`_touch()`'s unconditional `last_opened_at`/`root_path` write) on every
  hit — a side effect this validation-only path must not introduce. Use
  the new `exists()` method instead.
- **Do not add a `FOREIGN KEY`/`REFERENCES` constraint or a migration.**
  The parking-lot entry this episode formalizes named that as one possible
  direction; this episode's own scope (set by the instruction that started
  it) is the two handlers' own validation, not a schema change. A
  database-level constraint, if ever wanted, is a separate, later episode
  — note it as still open in Handoff, do not fold it in here.
- **Do not change `SetSessionProvider`'s existing behaviour for a VALID
  `project_id`.** B30's own "no NULL-only guard, unconditional overwrite
  for an explicit UI-driven choice" stands exactly as built — this
  episode only gates the existence check in front of it, it does not
  reintroduce `_commit_adoption`'s semantics.
- **Do not touch `title`/`provider`/`model` validation, or anything else
  in either handler unrelated to `project_id`.**

---

## Done when

1. `CreateSession` with a `project_id` matching no `projects` row creates
   the session with `project_id` **NULL** (confirmed by a direct session
   read, not the reply alone), logs a warning naming the rejected id, and
   the `SessionCreated` reply's own `project_id` is empty — not the
   invalid value echoed back.
2. `CreateSession` with a **valid** `project_id` still binds it exactly as
   before this episode — a plain regression check, no behaviour change
   for the valid case.
3. `SetSessionProvider` with a `project_id` matching no row leaves
   whatever the session's `project_id` already was **completely
   unchanged** (both when it was previously NULL and when it was
   previously bound to something else), logs a warning, and the reply's
   `project_id` reflects that unchanged, real value.
4. `SetSessionProvider` with a **valid** `project_id` still binds it
   exactly as B30 built it — including the A→B re-point case
   (`manual_b30_set_session_project.py`'s own coverage) — a plain
   regression check against B30, not a behaviour change.
5. `ProjectManager.exists()` causes no write on either a hit or a miss —
   confirmed directly: a project's `last_opened_at` and `root_path` are
   byte-identical before and after a call, on both outcomes.
6. A long-lived-`Runtime` script (same shape as
   `manual_b30_set_session_project.py`/`manual_b28_demo_mode.py`) covers
   #1–#5 in one process, against a scratch `APPDATA`/`LOCALAPPDATA`
   profile.
7. Nothing under `frontend\` is touched; `git status` after this episode
   shows changes to exactly: `backend\managers\project_manager.py`,
   `backend\runtime\runtime.py`, the new manual test script, plus
   whichever docs this episode's own Record step touches.

---

## Verification gate (PowerShell)

Per CLAUDE.md's hard rule — both env vars set explicitly in the same
command, the real profile never a target of anything:

```python
# backend\tests\manual_b31_validate_project_id.py sketch:
#
# One long-lived Runtime (build_runtime(single_instance=False,
# say_hello=False, heartbeat=False)), same shape as manual_b30_set_
# session_project.py -- reuses its own session/project setup where it
# helps (a real project via runtime.projects.init(), a real session via
# runtime.sessions.create()).
#
# 1. CreateSession with project_id="does-not-exist". GetSession/the
#    reply itself -> project_id is None/empty. Check the log output (or
#    capture via caplog/a log handler) names the rejected id.
# 2. CreateSession with a REAL project_id (from step 0's init()) ->
#    binds normally, byte-for-byte the same as before this episode.
# 3. SetSessionProvider on an existing, UNBOUND session with
#    project_id="does-not-exist" -> session.project_id stays None, reply
#    echoes None.
# 4. SetSessionProvider on a session already bound to project A, with
#    project_id="does-not-exist" -> session.project_id stays A,
#    UNCHANGED, reply echoes A.
# 5. SetSessionProvider with a REAL, different project B -> still
#    rebinds A -> B exactly as manual_b30_set_session_project.py's own
#    A->B case already proved -- a plain regression run, not new
#    behaviour.
# 6. ProjectManager.exists() called directly against a real project id:
#    read last_opened_at/root_path before and after, assert unchanged.
#    Same against a fake id (returns False, obviously no row to compare,
#    but confirm it doesn't raise and doesn't insert anything).
```

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b31"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    uv run python tests\manual_b31_validate_project_id.py
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

```powershell
cd D:\Projects\Point
go test ./frontend/internal/chat/... ./frontend/internal/app/...
```
Expect: passes unmodified — this episode touches no frontend file, so this
is a plain confirmation nothing regressed, not a check against new
behaviour.

---

## Handoff

- **A database-level `FOREIGN KEY` on `sessions.project_id` remains
  open**, deliberately not built here (see Do NOT). If a future episode
  (B17 Hardening, or whatever next touches this schema) wants defense in
  depth below the application layer, this episode's own parking-lot entry
  and its file:line evidence (`database\migrations.py:58, 109` vs. every
  `session_id` column's own `REFERENCES`) are the starting point — it
  does not need to re-derive the gap.
- **`ProjectManager.exists()` is a narrow, single-purpose addition.**
  Anyone tempted to reuse it for something beyond "does this id exist,
  with no side effect" should re-read Build §1's own reasoning for why it
  is not `resolve()` before assuming it is a general-purpose project
  lookup.
- **The warning log lines are not surfaced anywhere in the UI.** They are
  `_log.warning(...)` only (stderr/`logs\point.log`, per CLAUDE.md's "never
  log to stdout" rule) — a human debugging a dangling-id report would need
  to check logs, not the running app. Not a gap this episode was asked to
  close; `GetDoctorReport` surfacing "the last N rejected project_ids," if
  ever wanted, is a separate, unscoped idea, not an implicit requirement
  here.
- **`manual_b31_validate_project_id.py` is a real regression script, kept
  in the repo.** If a later episode changes `CreateSession`'s or
  `SetSessionProvider`'s payload shape, or `ProjectManager`'s identity
  model, re-run it rather than assuming these checks still hold.
