# Episode B30 — Let `SetSessionProvider` Also Bind a Session's Project ✅

**`session_manager.set_project(session_id, project_id)` existed since B12,
already tested, reachable from exactly one place — `_commit_adoption`'s
implicit attach-adoption path — and from nothing else. F10c needed a
session to be told its project *after* it already exists, for a reason
B12 never had: live tracing during F10c proved `CreateSession`'s payload
is not, and was never going to be, a reachable path for a human-chosen
project id, for any provider, not a Demo-specific timing gap. This episode
closes that the minimal way: `SetSessionProvider`'s existing payload gains
one optional `project_id` field, its handler calls the manager method that
was already there, and its reply always echoes the session's real,
re-read `project_id` — no new event, no new reply type.**

> Confirmed by Aryan: `backend\tests\manual_b30_set_session_project.py`,
> run twice against a fresh scratch `APPDATA`/`LOCALAPPDATA` profile per
> CLAUDE.md's hard rule (both env vars set explicitly in the same
> PowerShell command) — see "Manual Test Log" below for both full PASS
> runs, the second one covering the A→B re-point case added after the
> first review pass. `go test ./frontend/internal/chat/...` passes
> unmodified, confirming the reply's new `project_id` field is additive.
> F10c's own live repro (fresh profile, first-run chooser, select Demo
> mode, quit, direct DB query) now shows a non-empty, correct `project_id`
> — see F10c's own completed doc.

---

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
        await self.sessions.set_project(session_id, project_id)
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

Built exactly as the task doc specified, with no deviation. `project_id`
is optional and additive — the one existing caller before this episode
(`applyProviderDefault`'s retrofit, which never sent it) keeps working
unchanged. The reply's `project_id` is always the session's **re-read**,
current value — never the raw input parroted back — so a plain
provider/model retrofit that sends no `project_id` at all still tells the
frontend what the session's real project is.

`session_manager.set_project()` is called **unconditionally** when
`project_id` is present — deliberately not guarded against overwriting an
existing, different value the way `_commit_adoption`'s own comment insists
on for the attach-adoption path ("Only ever fills a NULL… re-pointing is a
security boundary"). `SetSessionProvider`'s caller is always Point's own
UI acting on the user's explicit choice, the same trust level
`provider`/`model` already carry on this exact path — confirmed live by
the A→B re-point case in the manual test log below, which this episode's
own task doc called for explicitly and the first draft of the
verification script did not cover.

## Frontend — the two call sites named in the task doc, nothing else

`frontend\internal\chat\providers.go`'s `applyProviderDefault` forwards
`project_id` when `d.ProjectID != ""`, alongside the existing
`session_id`/`provider`/`model` payload. `frontend\internal\chat\protocol.go`'s
`onSessionProviderUpdated` decodes a `ProjectID` field off the reply and
sets `w.projectID` from it. Both are exactly the two edits the task doc's
Build §2 specified, byte-for-byte. No other frontend file was touched —
in particular, `onSessionList`'s own `project_id`-on-`CreateSession`
forwarding (F10c's own Decision 1 code) was left exactly as F10c built it.

**Correction, found live after this episode first shipped (recorded in
full in F10c's own completed doc's "What the next episode inherits"):
that forwarding is not dead code.** It does not need to win the
first-run-chooser race this episode's own "Bugs caught" section traces —
it fires on **any** empty session list paired with a `state.json` that
already holds a stored Demo default with a `ProjectID`, which a genuinely
fresh profile's first launch is only one way to produce. A real repro
found a second: delete `runtime.db*` only (not `state.json`) and
relaunch — the session list comes back empty again, `onSessionList` fires
`CreateSession` with the persisted `project_id` from a project row that
no longer exists in the now-fresh database, and the backend accepts it
with no error, no validation, and no matching `projects` row. See the
parking-lot entry this finding produced (`design/tasks/backend/
INDEX.md`'s parking lot) — `CreateSession`/`SetSessionProvider` neither
has ever validated `project_id` against the `projects` table, on this
path or on this episode's own. `go test ./frontend/internal/chat/...`
passes with no modification to any existing test.

## `backend\tests\manual_b30_set_session_project.py` (NEW)

One long-lived `Runtime`, `build_runtime(single_instance=False,
say_hello=False, heartbeat=False)` — the same shape every prior
`manual_*` script in this repo uses, for the same reason: this needs to
observe its own writes across several `SetSessionProvider` calls against
one session, which a fresh `cli.py` process per call cannot do. Covers:

1. `project_id` sent on a session whose `project_id` is currently `NULL`
   binds it — confirmed by `runtime.sessions.get()`, not the reply alone.
2. `SetSessionProvider` sent again, with **no** `project_id`, changes
   `provider`/`model` and leaves the bound `project_id` untouched.
3. Both replies above carry the session's real, current `project_id` —
   including step 2's, whose own request never sent one.
4. `project_id` sent for a session already bound to a **different**
   project (A → B) rebinds it unconditionally — no `NULL`-only guard,
   confirming the task doc's own stated design call live rather than only
   in prose.

---

## Manual Test Log (Episode B30)

Scratch `APPDATA`/`LOCALAPPDATA` profile per CLAUDE.md's hard rule, both
env vars set explicitly in the same PowerShell command, run twice (the
second run after adding the A→B case):

```
database   ...\Point\runtime.db
  [PASS] fixture: the session starts with no project bound

=== #1  SetSessionProvider(project_id=<real id>) binds a NULL project_id
  [PASS] SessionProviderUpdated reply arrived
  [PASS] the session row now carries the project id
  [PASS] provider/model were also applied
  [PASS] step 1's reply carries the bound project_id

=== #2  SetSessionProvider with NO project_id leaves an existing binding untouched
  [PASS] second SessionProviderUpdated reply arrived
  [PASS] project_id survives a project_id-less retrofit
  [PASS] provider/model changed again
  [PASS] step 2's reply echoes the EXISTING project_id even though the request never sent one

=== A->B  SetSessionProvider re-points a session already bound to project A
  [PASS] the re-point reply arrived
  [PASS] project_id moved from A to B with no NULL-only guard
  [PASS] the reply echoes B, not A
  [PASS] provider/model were applied alongside the re-point

ALL CHECKS PASSED
```

Exit code 0, both runs.

```powershell
cd D:\Projects\Point
go test ./frontend/internal/chat/...
```
```
ok  	github.com/lazyworkoholic/point/frontend/internal/chat	...s
```

F10c's own live repro, re-run after this episode landed (not this
episode's own job, but what it unblocked): fresh scratch profile,
first-run chooser, select Demo mode, quit —
`select provider, project_id from sessions order by created_at desc limit 1;`
now returns a real project id, not empty. Full account in F10c's own
completed doc.

---

## Bugs caught during manual testing

None. The one thing worth recording is not a bug in this episode's own
code but the finding that made the episode necessary in the first place:
F10c's own Decision 1 assumed a human could choose a provider before
bootstrap's one `CreateSession` call fires. Traced with real evidence
(`beginBootstrap`'s own doc comment, `onConnected`'s guard, the chooser's
own render timing against the child-process/handshake/double-round-trip
latency `CreateSession` is actually gated behind) rather than reasoned
about: that race is won by the UI's structural head start on every
process start Point has ever made, for every provider, not a Demo-
specific timing coincidence a cleverer sequencing could have fixed. The
only mechanism that ever works is telling an **already-created** session
its project after the fact — exactly `SetSessionProvider`'s existing
retrofit shape, now extended to carry it.

---

## What the next episode inherits

- **F10c is unblocked.** Its Done-when #3/#4 and its own Handoff's
  "blocked on B30" note are both resolved — see F10c's own completed doc
  for the full live re-verification.
- **`set_project`'s "only fills a NULL" rule, from `_commit_adoption`, is
  deliberately NOT inherited by this path.** Confirmed live by the A→B
  case above, not only asserted in the task doc. Anyone reusing
  `session_manager.set_project()` a third time must decide this question
  fresh for whatever new caller they're building, not assume either
  precedent (the implicit-attach path's NULL-only guard, or this
  explicit-UI path's unconditional overwrite) by default.
- **`onSessionList`'s own `project_id`-on-`CreateSession` forwarding
  (F10c's Decision 1 code) still exists, unchanged — and is NOT dead
  code, a claim this doc made before a later live test disproved it.**
  It fires whenever a `CreateSession` happens (empty session list) while
  `state.json` already holds a stored Demo default carrying a
  `ProjectID` — the first-run chooser race is only one way to reach that
  state. A live repro (delete `runtime.db*` only, relaunch) reached it a
  second way, with a real-world consequence: the backend accepted a
  `project_id` for a project row that no longer existed, silently,
  because neither `CreateSession` nor `SetSessionProvider` has ever
  validated `project_id` against the `projects` table — on this episode's
  own path as much as on the pre-existing one. See F10c's own completed
  doc for the full repro and the parking-lot entry
  (`design/tasks/backend/INDEX.md`) this produced. **Not fixed by this
  episode** — this episode added a second way to reach `set_project`, it
  did not add validation to either path.
- **`manual_b30_set_session_project.py` is a real regression script, kept
  in the repo.** If a later episode changes `SetSessionProvider`'s payload
  shape or `set_project`'s overwrite semantics, re-run it rather than
  assuming these checks still hold.
