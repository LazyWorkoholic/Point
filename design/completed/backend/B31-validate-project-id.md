# Episode B31 — Validate `project_id` in `CreateSession`/`SetSessionProvider` ✅

**Neither handler ever checked an incoming `project_id` against the `projects`
table — found live during the B30/F10c recording session (full repro in this
episode's own task doc and in `B30-set-session-project.md`'s "What the next
episode inherits"). This episode adds one new, narrow, side-effect-free
`ProjectManager.exists()` check and gates both handlers on it, with two
deliberately asymmetric behaviours for an unknown id: `CreateSession` nulls
it and still creates the session (never blocks a profile's first session);
`SetSessionProvider` leaves whatever binding already existed completely
untouched. Neither refuses the request outright, and a valid `project_id`
behaves exactly as it did before this episode on both paths.**

> Confirmed by Aryan: `backend\tests\manual_b31_validate_project_id.py`, run
> against a scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard
> rule (both env vars set explicitly in the same PowerShell command) — see
> "Manual Test Log" below for the full PASS run, all 23 checks across the
> seven Done-when items. Each of the three warning log lines (Done-when #1,
> #3, #4) was additionally confirmed directly in `logs\point.log` under the
> scratch profile — independent of the script's own in-process log-capture
> handler, not just the script's own claim. `go test
> ./frontend/internal/chat/... ./frontend/internal/app/...` passes
> unmodified — this episode touches no frontend file. `git status` after the
> change showed exactly the three files Done-when #7 names.

---

## `backend\managers\project_manager.py` — `ProjectManager.exists()` (NEW)

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

Built exactly as the task doc specified, placed next to `resolve()`.
`resolve(explicit_project_id=...)` was deliberately not reused — it calls
`_touch()` on a hit, which unconditionally rewrites `last_opened_at` and
`root_path`, a write this validation-only check must never cause. Confirmed
live, not just by reading the code: Done-when #5/#6 below re-read a real
project's `last_opened_at`/`root_path` before and after both a hit and a
miss and found them byte-identical.

## `backend\runtime\runtime.py` — `_handle_create_session`

```python
async def _handle_create_session(self, event: IncomingEvent) -> None:
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

`project_id` is nulled **before** `SessionManager.create()` is ever called,
not after — the row never carries the bad value even transiently. The reply
needed no separate re-read the way `SetSessionProvider`'s does:
`_session_to_dict(session)` already reads the in-memory `session` object
`.create()` returned, and that object's `project_id` is already the
corrected value. An unknown id never refuses the request — refusing would
block exactly the path this episode's own repro exercised: a profile's very
first session, created from a wiped-or-restored database via
`onSessionList`'s empty-list branch.

## `backend\runtime\runtime.py` — `_handle_set_session_provider`

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

An unknown `project_id` skips `set_project()` entirely rather than nulling
an existing binding — this handler's whole reason for existing is
retrofitting an already-running session non-disruptively, and "leave it
exactly as it was" is the one behaviour that can never make an existing
session worse off. The re-read (`session = await self.sessions.get
(session_id)`), already present since B30, needed no change — it already
echoes the session's real, current value, which is exactly "unchanged" when
the `exists()` check fails. B30's own unconditional-overwrite behaviour for
a **valid** `project_id` (no `NULL`-only guard, unlike `_commit_adoption`)
is untouched — confirmed live by Done-when #4's own A→B re-point case below.

## `backend\tests\manual_b31_validate_project_id.py` (NEW)

One long-lived `Runtime`, `build_runtime(single_instance=False,
say_hello=False, heartbeat=False)` — the same shape every prior `manual_*`
script in this repo uses, plus a small in-process `logging.Handler`
attached to the `"point"` logger to capture warning text per-step (cleared
between steps so each check only sees its own step's warnings). Covers:

1. `CreateSession` with an unknown `project_id` creates the session with
   `project_id` **NULL** — confirmed by `runtime.sessions.get()`, not the
   reply alone — logs a warning naming the rejected id, and the
   `SessionCreated` reply's own `project_id` is empty.
2. `CreateSession` with a **real** `project_id` still binds it exactly as
   before this episode, with no warning logged.
3. `SetSessionProvider` on a session that starts **unbound**, given an
   unknown `project_id`, leaves `project_id` `None`, still applies
   `provider`/`model`, warns, and the reply echoes `None`.
4. `SetSessionProvider` on a session already bound to project **A**, given
   an unknown `project_id`, leaves `project_id` at **A, unchanged**, still
   applies `provider`/`model`, warns, and the reply echoes **A**.
5. `SetSessionProvider` with a **real, different** project **B** still
   re-points **A → B** exactly as `manual_b30_set_session_project.py`'s own
   A→B case already proved — a plain regression run, no new behaviour, no
   warning logged.
6. `ProjectManager.exists()` returns `True`/`False` correctly and causes no
   write on either outcome — a real project's `last_opened_at`/`root_path`
   re-read before and after a hit are byte-identical, and a miss inserts no
   row.

---

## Manual Test Log (Episode B31)

Scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule, both env
vars set explicitly in the same PowerShell command:

```
database   ...\Point\runtime.db
2026-10-09 00:36:06 WARNING point.runtime: CreateSession: project_id 'does-not-exist' matches no project; creating without one
2026-10-09 00:36:07 WARNING point.runtime: SetSessionProvider: project_id 'does-not-exist' matches no project; binding left unchanged
2026-10-09 00:36:07 WARNING point.runtime: SetSessionProvider: project_id 'does-not-exist' matches no project; binding left unchanged

=== #1  CreateSession(project_id=<unknown>) creates session unbound
  [PASS] SessionCreated reply arrived
  [PASS] the session row's project_id is None
  [PASS] the reply's own project_id is empty
  [PASS] a warning naming the rejected id was logged

=== #2  CreateSession(project_id=<real>) still binds normally
  [PASS] second SessionCreated reply arrived
  [PASS] the session row carries the real project_id
  [PASS] the reply echoes the real project_id
  [PASS] no warning logged for a valid id

=== #3  SetSessionProvider(project_id=<unknown>) on an unbound session
  [PASS] fixture: the session starts with no project bound
  [PASS] SessionProviderUpdated reply arrived
  [PASS] project_id stays None
  [PASS] provider/model were still applied
  [PASS] the reply echoes None
  [PASS] a warning naming the rejected id was logged

=== #4  SetSessionProvider(project_id=<unknown>) on a session bound to A
  [PASS] fixture: the session starts bound to A
  [PASS] SessionProviderUpdated reply arrived
  [PASS] project_id stays A, unchanged
  [PASS] provider/model were still applied
  [PASS] the reply echoes A, unchanged
  [PASS] a warning naming the rejected id was logged

=== #5  SetSessionProvider(project_id=<real B>) still re-points A -> B
  [PASS] the re-point reply arrived
  [PASS] project_id moved from A to B
  [PASS] the reply echoes B, not A
  [PASS] no warning logged for a valid id

=== #6  ProjectManager.exists() causes no write, hit or miss
  [PASS] exists() returns True on a hit
  [PASS] last_opened_at unchanged across a hit
  [PASS] root_path unchanged across a hit
  [PASS] exists() returns False on a miss
  [PASS] a miss inserts no row

ALL CHECKS PASSED
```

Exit code 0. Each of the three warning lines shown at the top (captured by
the stderr handler `configure_logging` wires up) was cross-checked directly
against `logs\point.log` under the scratch profile, independent of the
script's own in-process `logging.Handler` — not merely the script's own
internal claim that a warning fired.

```powershell
cd D:\Projects\Point
go test ./frontend/internal/chat/... ./frontend/internal/app/...
```
```
ok  	github.com/lazyworkoholic/point/frontend/internal/chat	...s
ok  	github.com/lazyworkoholic/point/frontend/internal/app	...s
```

`git status` after the change: exactly `backend\managers\project_manager.py`,
`backend\runtime\runtime.py`, `backend\tests\manual_b31_validate_project_id.py`
— matching Done-when #7, confirmed against the real diff, not assumed from
the task doc alone.

---

## Bugs caught during manual testing

None. The implementation matched the task doc's own Build section
byte-for-byte on the first pass, and the verification script passed clean on
its first run — the task doc's own upfront reasoning (rejecting `resolve()`
for `_touch()`'s side effect, nulling before `.create()` rather than after,
skipping rather than nulling an existing `SetSessionProvider` binding) had
already resolved the design questions a live test could otherwise have
caught, before any code was written.

---

## What the next episode inherits

- **A database-level `FOREIGN KEY` on `sessions.project_id` remains open**,
  deliberately not built here. If a future episode (B17 Hardening, or
  whatever next touches this schema) wants defense in depth below the
  application layer, this episode's own task doc and the parking-lot
  entry's file:line evidence (`database\migrations.py:58, 109` vs. every
  `session_id` column's own `REFERENCES`) are the starting point.
- **`ProjectManager.exists()` is a narrow, single-purpose addition.** Anyone
  tempted to reuse it for something beyond "does this id exist, with no
  side effect" should re-read the task doc's Build §1 reasoning for why it
  is not `resolve()` before assuming it is a general-purpose project
  lookup.
- **The warning log lines are not surfaced anywhere in the UI.** They are
  `_log.warning(...)` only (stderr/`logs\point.log`, per CLAUDE.md's "never
  log to stdout" rule) — a human debugging a dangling-id report needs to
  check logs, not the running app. `GetDoctorReport` surfacing "the last N
  rejected project_ids," if ever wanted, is a separate, unscoped idea.
- **`manual_b31_validate_project_id.py` is a real regression script, kept in
  the repo.** If a later episode changes `CreateSession`'s or
  `SetSessionProvider`'s payload shape, or `ProjectManager`'s identity
  model, re-run it rather than assuming these checks still hold.
- **Nothing in the frontend was touched or needed to be.** Both handlers'
  wire shape is unchanged (same fields in, same fields out) — confirmed by
  the unmodified `go test ./frontend/internal/chat/...
  ./frontend/internal/app/...` pass.
