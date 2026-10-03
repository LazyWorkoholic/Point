# Episode B30 — Let `SetSessionProvider` Also Bind a Session's Project

**`SessionManager.set_project(session_id, project_id)` already exists —
built for B12's attachment-adoption flow, already tested, already the
"deliberate same shape as `set_provider`" by its own doc comment. It has
never been reachable from anything but that one internal adoption path.
F10c's Demo mode needs exactly this capability for a different reason
(binding a session to the shipped sample project) and found, by tracing
real evidence rather than assuming, that there is no realistic timing
window in which a fresh session's `CreateSession` payload can ever carry
a human-chosen project id — so a session created today can only ever be
told its project **after** it already exists. This episode closes that
gap the minimal way: `SetSessionProvider`'s existing payload gains one
optional field, its handler calls the manager method that is already
there, and its reply echoes the result back — no new event, no new
reply type.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B30-set-session-project.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. One optional field through an existing handler,
calling an existing, already-tested manager method. No new logic.

**Depends on:** B12 (`session_manager.set_project`, shipped and
exercised by the attachment-adoption path), B4/B6 (`SetSessionProvider`
itself).

**Found during:** F10c (Settings — Demo Mode), while fixing a live bug
in the frontend's own project-binding sequencing. Spun out as its own
episode, same discipline B23–B29 already established for a mid-episode
backend need — not folded into F10c, which explicitly declared "no
backend changes" as its own scope boundary.

---

## Why this episode exists

### The bug that surfaced it, traced with real evidence, not reasoned about

F10c's own Decision 1 assumed a session created with an empty session
list (`CreateSession`, fired from `chat\protocol.go`'s `onSessionList`)
could carry a freshly-resolved Demo project id in its own payload, if the
id resolved before that one `CreateSession` fired. Live testing against a
genuinely fresh profile proved this false — every time:

```
sessions:  432a4250-...|demo|point-demo-v1|            (project_id EMPTY)
projects:  b93dbd75-...|sample_project|D:\...\demo\sample_project
```

`ProjectInit` succeeded; the session never got told about it. Traced to
the actual cause, found already written down in this codebase's own
prior work rather than freshly discovered: `chat\providers.go`'s
`applyProviderDefault` doc, from **F10**, already states — about every
provider, not Demo specifically — "the session already open was created
**before this choice existed**." Chat's own bootstrap needs two fully
automatic round trips (`ListProviders`, then `ListSessions`) with no
human action before `CreateSession` fires; the chooser cannot even
become clickable until the first of those same two round trips has
already landed (health info has to arrive first). A human cannot
realistically win a race against one more local IPC round trip. This is
structural, not a timing coincidence this episode's sequencing could
have avoided by being cleverer.

**Consequence:** `CreateSession`'s payload is not, and was never going
to be, a reachable path for binding a session's project to anything a
human chooses at selection time. The only mechanism that can ever work
is telling an **already-created** session about its project after the
fact — exactly the shape `set_provider`'s own retrofit already uses for
provider/model (`chat\providers.go`'s `applyProviderDefault`,
`w.sessionID != ""` branch), which already fires reliably every time a
corrected default arrives, with no race to win: by the time that second,
corrected message lands, the session has always already been created.

### The capability already exists; it was never exposed

`managers\session_manager.py:112`:

```python
async def set_project(self, session_id: str, project_id: str) -> None:
    """Bind a session to a project (B12).

    `project_id` was write-once-at-CreateSession until now, which
    left a session created outside a project permanently unable to
    attach anything... The counterpart of `set_provider`, and
    deliberately the same shape -- a column the session owns, changed
    through the manager that owns the session.
    """
```

Its only caller today is `runtime.py:1354`'s `_commit_adoption`, reached
only through a successful `AttachContext` — i.e. a session only ever
gets a project today as an incidental side effect of attaching a file,
never directly. Checked directly, not assumed: `grep` for `set_project`
across `runtime\` and `managers\` shows exactly these two sites and no
third.

### The alternative considered and rejected: reuse `AttachContext`'s adoption side effect

Auto-attaching a file from the sample project (e.g. `README.md`) the
moment Demo mode is selected would bind the project through the EXISTING
adoption path with **zero backend changes** — tempting, since F10c's own
scope forbids touching `backend\`. Rejected: it produces a real, visible
attachment chip nobody asked the user to create, the first time they
pick a provider — a new, user-facing behavior invented to work around a
plumbing gap, not a wiring job. It would also only bind on the FIRST
attach, meaning the exact same session that currently shows no binding
would now show an unexplained chip instead of an explained line — trading
one surprise for another. `SetSessionProvider`'s own extension is the
correct fix precisely because it has no visible side effect beyond the
status line already built for this (F10c Build §5), which already
exists and already does the right thing once the data underneath it is
correct.

---

## Build

### 1. `backend\runtime\runtime.py` — `_handle_set_session_provider`

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

`project_id` is optional and additive — every existing caller (there is
exactly one today, `applyProviderDefault`) that never sends it keeps
working exactly as before. The reply now always echoes the session's
**real, current** `project_id` (re-read after the write, not the raw
input parroted back) — the same "the backend's own echo is the only
claim this package treats as true" discipline
`onSessionProviderUpdated`'s own Go-side doc already states, now actually
honored: a request with no `project_id` field still returns whatever the
session's project already was, which lets the frontend learn the real
value even on a plain provider/model retrofit that never asked to change
it.

`session_manager.set_project()` is called unconditionally when
`project_id` is present — no guard against "only if currently NULL" the
way `_commit_adoption`'s own comment insists on for the attach-adoption
path ("Only ever fills a NULL... re-pointing is a security boundary").
This is a deliberate difference: `AttachContext`'s adoption is implicit,
inferred from a file path a model or a user typed, so re-pointing it
silently would be dangerous. `SetSessionProvider`'s caller is **always**
Point's own UI acting on the user's own explicit choice (this project's
own provider selection) — an explicit `project_id` here is as trusted as
the explicit `provider`/`model` already sent alongside it, and always has
been.

### 2. Frontend — forward and read back the field

`frontend\internal\chat\providers.go`'s `applyProviderDefault`:

```go
func (w *Workspace) applyProviderDefault(d config.ProviderDefault) {
	w.state.Provider = &d
	if w.sessionID != "" {
		payload := map[string]any{
			"session_id": w.sessionID, "provider": d.Provider, "model": d.Model,
		}
		if d.ProjectID != "" {
			payload["project_id"] = d.ProjectID
		}
		w.send(protocol.CmdSetSessionProvider, querySetSessionProvider, payload)
	}
	...
}
```

`frontend\internal\chat\protocol.go`'s `onSessionProviderUpdated`:

```go
func (w *Workspace) onSessionProviderUpdated(env protocol.Envelope) {
	var p struct {
		SessionID string `json:"session_id"`
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		ProjectID string `json:"project_id"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		return
	}
	if p.SessionID != w.sessionID {
		return
	}
	w.sessionProvider = p.Provider
	w.sessionModel = p.Model
	w.projectID = p.ProjectID
}
```

This is the only Go-side change this episode needs — F10c's own status
line (`cards.go`'s `statusCardLines`, already built, already correct
once the underlying data is) starts reading a true value with no further
change to it at all.

---

## Do NOT

- **Do not guard `set_project` against overwriting an existing, different
  project_id on this path.** Unlike `_commit_adoption`'s inferred binding,
  this path is always an explicit user choice, carried alongside
  `provider`/`model` which already overwrite unconditionally the same
  way.
- **Do not add a `SetSessionProject`-only event.** The existing
  `SetSessionProvider` retrofit call site is already correct for this —
  one optional field through one event this project already has, not a
  second one with its own reply type to keep in sync.
- **Do not touch `AttachContext`, `_adoptable_project_for`, or
  `_commit_adoption`.** Nothing about the attach-adoption path changes;
  this is a second, independent way to reach `set_project`, not a
  refactor of the first.
- **Do not build any Demo-mode-specific logic here.** This episode fixes
  a general capability gap (a session cannot be told its own project
  after creation, except by accident via attach) that F10c's own
  investigation happened to find. Nothing here should mention Demo,
  `DemoProvider`, or the sample project by name.
- **Do not change F10c's own frontend files beyond the two edits shown
  above.** The badge, the catalog entry, the `ProjectInit` firing on
  selection, the status line's own condition — all of F10c's own work —
  are unchanged; this episode only makes the data they already read
  correct.

---

## Done when

1. `SetSessionProvider` sent with `project_id` set on a session whose
   `project_id` is currently NULL binds it — confirmed by a direct
   session-table query, not by the UI alone.
2. `SetSessionProvider` sent **without** `project_id` (every existing
   caller before this episode) behaves exactly as before — `provider`/
   `model` change, `project_id` is left untouched — confirmed by sending
   it against a session that already has a project bound and checking it
   survives.
3. `SetSessionProvider`'s reply always includes the session's **current,
   re-read** `project_id` — including when the request itself didn't
   send one, and including when it did.
4. Every other existing caller of `SetSessionProvider`/consumer of
   `SessionProviderUpdated` is unaffected — `go test
   ./frontend/internal/chat/...` passes unmodified, confirming the new
   `ProjectID` field on the reply decode is additive, not breaking.
5. F10c's own Bug A repro (fresh profile, first-run chooser, select Demo
   mode, quit, direct DB query) now shows a non-empty, correct
   `project_id` on the resulting session.
6. F10c's own status line (`cards.go`) stops showing "no sample project
   bound" the moment this lands, with no further Go-side change needed
   beyond the two call sites in Build §2.

---

## Verification gate

Same shape as B26/B27's own: a direct `Runtime.receive()` script for
#1–#4 (one long-lived `Runtime`, the same `build_runtime(single_instance=False,
say_hello=False, heartbeat=False)` pattern every prior manual script in
this repo uses), against a scratch `APPDATA`/`LOCALAPPDATA` profile per
CLAUDE.md's hard rule, env vars set explicitly in every command block.

```python
# backend\tests\manual_b30_set_session_project.py sketch:
#
# 1. CreateSession with no project. SetSessionProvider with
#    project_id=<real id from a prior ProjectInit>. GetSession ->
#    project_id matches.
# 2. SetSessionProvider AGAIN, no project_id this time, different
#    provider/model. GetSession -> project_id UNCHANGED from step 1,
#    provider/model changed.
# 3. Confirm step 1 and step 2's SessionProviderUpdated replies both
#    carry the session's real, current project_id -- step 2's reply
#    shows the value from step 1, not null, even though step 2's
#    REQUEST never sent one.
```

Then F10c's own repro, re-run end to end once this lands (not this
episode's own job to run, but what it unblocks):

1. Fresh scratch profile, first-run chooser, select Demo mode, quit.
2. `select provider, project_id from sessions order by created_at desc limit 1;`
   — expect a real project id, not empty.
3. Only then is F10c's own Step 10 (`@research`) safe to test, since it
   resolves tool calls against `session.project_id` directly
   (`agents\executor.py:487`) with no fallback if it is empty.

---

## Handoff

- **F10c can now re-verify Bug A's exact repro and, once it passes, Step
  10 (`@research`).** Nothing else in F10c changes because of this
  episode — its own Decision 1 needs a correction recorded (the
  mechanism it described never had a working path; this episode is the
  real one), but no code F10c already built needs to change beyond the
  two call sites this episode's own Build section touches.
- **Bug B** (the status line not appearing when retrofitting an existing
  session away from Demo and back) was traced separately and no defect
  was found in the writer path (`onSessionProviderUpdated`) or the
  render condition (`cards.go`) by static reading alone — both look
  correct. The leading hypothesis is that the session under test in that
  specific repro already had a non-empty `project_id` from something
  unrelated, making the line's absence correct rather than a bug, but
  this was not confirmed with a captured query at the time. Re-verify
  with the query once this episode lands, since a correct `project_id`
  flowing through for the first time might also be what was actually
  missing there.
- **`set_project`'s "only fills a NULL" rule, from `_commit_adoption`, is
  deliberately NOT inherited by this path** — see Build §1's own note.
  Anyone reusing `session_manager.set_project()` a third time must decide
  this question fresh for whatever new caller they're building, not
  assume either precedent.
