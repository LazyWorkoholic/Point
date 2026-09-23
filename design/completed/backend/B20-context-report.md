# Episode B20 — `context.report` on the Wire ✅

**B8 already computed everything the Status card needs — budget, per-section
token counts, what the next turn would evict. It was reachable from `cli.py`
and from nowhere else. This episode puts it on the wire: `GetContextReport`
(on-demand pull) and `ContextReport` (the reply, and the push after a turn's
assistant message is persisted), both carrying `ContextReport.to_dict()`
verbatim, both calling B8's existing `context.report()` and never a second
implementation of the arithmetic.**

> Verified by Aryan on 2026-09-24 against `tasks/backend/B20-context-report.md`'s
> six "Done when" criteria, on scratch profiles using the env-var-override
> pattern below — never the retired rename-aside pattern (see "Incident: a
> real profile was deleted mid-episode" below, which happened during this
> episode's own verification work and is recorded here in full because it
> changed how every future episode's gate must be written, not just this
> one's).
>
> **Two follow-up findings were pushed back on before this doc was written,
> and both were re-answered with direct evidence, not description:**
>
> 1. The first eyes-on log check ("exactly 2 pushes for 2 sends") reported 2
>    matching log lines, but the run that produced them used `--verbose` on
>    `send` while an earlier, non-verbose run of the same check had reported
>    **zero** matches — a real discrepancy, not a flaky test. Root cause:
>    `logging_setup.configure_logging()` sets the root `point` logger itself
>    to `INFO` unless `--verbose` is passed; the file handler's own threshold
>    is `DEBUG`, but a logger call below the *logger's* effective level never
>    reaches any handler at all, file included. `_push_context_report`'s
>    confirmation line is `_log.debug(...)`, so it is invisible without
>    `--verbose` — not evidence the push didn't happen. Re-run with
>    `--verbose` on both `send` calls, against the **new, safe** scratch-env
>    profile (not the since-retired rename-aside one the first two runs
>    used): `Select-String -Pattern "context report"` against
>    `point.log` returned exactly 2 matches, one per turn, full output below.
> 2. Whether the swallow-and-log path for a failed push is visible without
>    `--verbose` — confirmed directly against the source, not by description:
>    [`backend/runtime/runtime.py:1088`](../../../backend/runtime/runtime.py)
>    is `_log.warning(...)`, not `.debug(...)`. `WARNING` clears both
>    thresholds unconditionally (`stderr_handler` at `WARNING` even in
>    non-verbose mode, file handler always at `DEBUG`), so a failed push is
>    always visible; only the success confirmation needed `--verbose`.

---

## Where the code went

Transport only, three files, no new package.

```
backend\
├── runtime\
│   ├── events.py     + GetContextReport (incoming) -- optional `session_id`,
│   │                   absent meaning the most recently updated session
│   │                 + ContextReport (outgoing) -- ONE type, reused for both
│   │                   the reply (corr_id set) and the push (corr_id null),
│   │                   so a consumer has one decoder and not two
│   └── runtime.py    + `from managers import context`,
│                       `SessionNotFoundError` imported alongside
│                       `SessionManager`
│                     + _handle_get_context_report -- session resolution
│                       mirrors `cmd_context_report` exactly (`memory.session()`
│                       / `memory.latest_session()`, not `self.sessions.get()`),
│                       so the wire answer and the CLI's own numbers are the
│                       same lookup, not two that happen to usually agree
│                     + _push_context_report -- called from the stream sink's
│                       terminal-state branch, only when `persisted` is true
│                       (an assistant message actually got appended -- done,
│                       failed, or cancelled with salvaged partial text all
│                       count; a chunk that persisted nothing does not push).
│                       Best-effort: swallows `RuntimeException` and logs at
│                       WARNING, because this runs inside the same `await`
│                       `task.py::_run()` wraps to convert an exception into a
│                       `failed` terminal event -- a report failing to build
│                       here must never retroactively turn an
│                       already-persisted, already-`TaskDone` turn into a
│                       second, contradictory terminal event
└── cli.py            + `context report --via-wire` -- drives GetContextReport
                        through `call()` over the same event pipe, instead of
                        calling `context.report()` in-process. This is the
                        wire-vs-CLI parity check the episode's own Build
                        section left open-ended ("drive over stdin, or add a
                        --via-wire flag ... record which you chose and why").
                        Always prints JSON; ignores editor-context flags,
                        since GetContextReport's payload is session_id only.
                      + "GetContextReport": {"ContextReport"} added to
                        _EXPECTED_REPLY, so `call()` knows what terminates
                        the drain
```

`managers\context.py`'s `report()` and `ContextReport.to_dict()` are
**unchanged** — every field ships verbatim, no subset, exactly as B8's own
docstring asked of this episode.

---

## The mechanism

`_push_context_report` is wired into the sink's terminal-state handling in
`runtime.py`, not into `_handle_send_message`: the report can only have moved
once history actually gained a message, and the sink is the one place that
knows whether it did.

```python
persisted = False
if self.streams.has_stream(task.id):
    ...
    if msg is not None:
        msg = await self.agent_executor.apply_response_middleware(msg)
        await self.sessions.append_message(msg)
        persisted = True
    ...
else:
    if kind == "done" and echo_collected:
        ...
        await self.sessions.append_message(msg)
        persisted = True
    ...

await self._emit(OutgoingEvent(type=out_type, ...))  # TaskDone/Failed/Cancelled

if persisted:
    await self._push_context_report(session_id)
```

`_push_context_report` re-resolves the session and its agent (the same
`memory.session()` + `agents.resolve_for_session()` pair `GetContextReport`
uses), calls `context.report()`, and emits with `request_id=None` — which
`OutgoingEvent.to_dict()` maps straight to `"corr_id": null`, the same
"caused by no request" convention `hello`/`ping` already use.

---

## Manual Test Log (Episode B20)

All three runs below used the **new, safe verification pattern** — `$env:APPDATA`
and `$env:LOCALAPPDATA` pointed at a scratch directory for one script's
duration, restored at the end, the real profile never named in a destructive
call. See "Incident" below for why this replaced rename-aside mid-episode.

| Done-when | Result |
|---|---|
| #1 `GetContextReport` matches `context report --json` field for field | ✅ `--via-wire` output byte-identical (after JSON normalization) to the direct call, same session, confirmed twice (once under the retired pattern before the incident, once under the new pattern after) |
| #2 push arrives after a turn completes, `corr_id` null, same shape as the reply | ✅ confirmed directly against the raw envelope dict (not the CLI's unwrapped payload): `ContextReport corr_id=None`, payload keys == the full 10-field `ContextReport.to_dict()` shape, in an isolated one-off run against a throwaway temp profile |
| #3 `used_tokens`/`free_tokens` visibly move between two consecutive pushes | ✅ `used_tokens: 53 -> 82`, `free_tokens: 7947 -> 7918`, `history count: 2 -> 4`, from the safe-pattern re-run |
| #4 `next_evictions` names real messages once the window is over budget | ✅ by construction — `next_evictions` is `ContextLog.evicted` shipped verbatim through `to_dict()`, unmodified by this episode; B8's own gate already proved the arithmetic populates it correctly, and #1's field-for-field match proves this episode's transport does not touch it |
| #5 no push arrives per token — one per completed turn | ✅ re-run with `--verbose` against the safe scratch pattern: `Select-String -Path point.log -Pattern "context report"` → **exactly 2 matches** for 2 sends, literal output: <br>`2026-09-24 04:47:00 DEBUG point.runtime: B20: pushing context report for session 1f18...d5 (used=43 free=7957)` <br>`2026-09-24 04:47:03 DEBUG point.runtime: B20: pushing context report for session 1f18...d5 (used=87 free=7913)` |
| #6 `cli.py context report` still works exactly as before | ✅ non-`--via-wire` output unchanged in shape and content across every run |

---

## Incident: a real profile was deleted mid-episode

**While this episode's own verification was in progress, Aryan reported that
his real `%APPDATA%\Point` had been permanently deleted** (config.toml and
credentials.toml gone; Recycle Bin empty, no Windows Previous Versions
available). This is recorded here, in B20's own handoff, because the root
cause was a verification-script pattern used across multiple prior episodes'
docs — not anything in B20's product code — and fixing it changes how every
future episode's gate must be written.

**What was actually lost, and what wasn't.** Only `%APPDATA%\Point`
(config.toml) was empty. `%LOCALAPPDATA%\Point\runtime.db` was intact — 20
sessions, 49 messages, through 2026-08-25 — a strict superset of the
12-session/42-message legacy copy at `backend\runtime.db` (Aug 10; confirmed
by differing SHA-256 hashes, so it wasn't a re-copy of that older file). The
legacy `backend\runtime.db` was judged not worth restoring on its own merits
(pre-B18, superseded by the live database's own history either way) and
nothing was restored or merged — Aryan's explicit instruction.

**Root cause, traced to the actual script, not inferred:**
`design/tasks/frontend/F10-settings-first-run.md`'s manual verification
section (before this episode's fix) read:

```powershell
# "fresh machine simulation"
Rename-Item "$env:APPDATA\Point" "Point.bak" -ErrorAction SilentlyContinue
Rename-Item "$env:LOCALAPPDATA\Point" "Point.bak" -ErrorAction SilentlyContinue
# ... interactive `go run` session, driven by hand ...
# "restore your profile"
Remove-Item "$env:APPDATA\Point" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item "$env:LOCALAPPDATA\Point" -Recurse -Force -ErrorAction SilentlyContinue
Rename-Item "$env:APPDATA\Point.bak" "Point" -ErrorAction SilentlyContinue
Rename-Item "$env:LOCALAPPDATA\Point.bak" "Point" -ErrorAction SilentlyContinue
```

`-ErrorAction SilentlyContinue` on the rename-aside step means a failed
rename (a lock, a running process, anything) is indistinguishable from a
successful one — nothing downstream ever checks. The "restore" block's own
`Remove-Item "$env:APPDATA\Point" -Recurse -Force` is unconditional: if the
first rename never actually moved the real folder, this line deletes the
live profile outright, then "restores" nothing over it because the matching
`Rename-Item ... Point.bak ... Point` also silently no-ops (there was no
`.bak` to rename back). This is precisely CLAUDE.md's own B7 warning
("B7: this is exactly how a test project landed two rows in the real
`runtime.db`...") recurring in a second, more severe form — the earlier
guidance (rename-aside, one combined script) reduced the *frequency* of this
class of bug but did not close the *mechanism*: a destructive call keyed on
the real path's name, gated on an unchecked precondition.

**The fix, adopted mid-episode and now CLAUDE.md policy:** rename-aside is
retired outright, not patched. Both `backend/appconfig.py`
(`config_dir()`/`state_dir()`) and `frontend/internal/config/config.go`
(`ConfigDir()`/`StateDir()`) already read `APPDATA`/`LOCALAPPDATA` from the
environment alone — the Go source says so directly: *"a scratch profile made
by setting APPDATA and LOCALAPPDATA moves both sides of the app at once."*
Pointing both env vars at a scratch directory for one script's duration means
the real `Point` folder is never named in any rename or delete call, success
or failure — there is no longer a mechanism by which a failure cascades into
a real delete, because nothing destructive ever targets the real path.
CLAUDE.md's hard-rules section now mandates this pattern with a copy-paste
template and explicitly retires rename-aside, including from any doc a
future session might grep for as precedent before reading CLAUDE.md itself.
Three files were fixed to match: `CLAUDE.md` itself,
`design/tasks/frontend/F10-settings-first-run.md` (the actual culprit
script), and this episode's own verification gate above (which had already
been run once under the old pattern before the fix landed — every check in
the table above was subsequently re-run under the new pattern, so nothing in
this doc's evidence rests on the retired method).

**Recovery performed:** `%APPDATA%\Point\config.toml` recreated minimally —
`[provider] default = "ollama"`, `[model] default = "qwen2.5:1.5b-8k"`, the
two real keys `agents/executor.py::resolve_provider_model_effort()` reads —
nothing else, no credentials fabricated. `cli.py migrate` confirmed schema
v6; `cli.py doctor` confirmed a clean, startable profile against the real
(non-scratch) directories, ollama reachable, credentials.toml correctly
reported missing.

**Left deliberately unfixed:** `design/completed/backend/B06-09/B18/B21`'s
own manual-verification sections still show the old rename-aside pattern.
These are historical execution records (what was actually run, already
passed, already recorded), not live guidance — CLAUDE.md's updated hard rule
is what any future session must follow regardless of what an old doc shows,
and CLAUDE.md now says so explicitly. Sanitizing those docs' text is a
separate, optional cleanup, not a blocker on anything.

---

## `--via-wire`: kept permanently, not removed

**Decision: `context report --via-wire` stays in the shipped CLI as a
permanent wire-layer debugging tool.** Reasoning:

- `context report` is the **only** CLI command that deliberately bypasses the
  event pipe (`cmd_context_report`'s own docstring: "Goes straight to the
  builder rather than through an event"). Every other command already goes
  through `call()`. `--via-wire` is what restores the ability to exercise
  *this one command's* actual wire behavior — envelope shape, dispatcher
  registration, corr_id handling, the RuntimeException-to-Error-event path —
  from the CLI without Go, which is exactly `cli.py`'s stated purpose ("so
  every later episode can be verified without Go", B6).
- It has ongoing value past this episode's own gate: S13d (the frontend
  follow-up that wires F6's Status card to this event) and any future
  refactor of `_handle_get_context_report` both benefit from a standing way
  to confirm "the wire agrees with the CLI" without hand-rolling a new script
  each time.
- Cost of keeping it is low and self-contained: one flag, no change to the
  default path, same category as `--script` (replays mock fixtures) and
  `--verbose` — existing permanent debugging conveniences in this same file.
- The alternative — deleting it once this doc is written — would leave
  `GetContextReport` as the only event type in the whole backend with no CLI
  path to drive it directly, which is a worse position for whoever next
  touches this code than a two-line flag left as reference below.

---

## What the next episode inherits

- **F6's Status card row stays a stub until S13d** (per the task doc's
  Handoff) — this episode only put the data on the wire; nothing reads it in
  the frontend yet. `S13d-wire-context-card`'s task doc is still
  deliberately unwritten until now — this doc is what tells it the event's
  actual shape: `ContextReport`, full `ContextReport.to_dict()` payload,
  pushed with `corr_id: null` after `TaskDone`/`TaskFailed`/`TaskCancelled`
  whenever that terminal event persisted a message.
- **B21 and this episode both touched `context.py`'s neighborhood and
  neither needed to coordinate with the other** — B21 changed what enters
  the next prompt (excluded orphaned messages from `build()`'s history
  window); this episode only reads what B8's `report()` already computes
  over whatever B21 leaves in. No merge conflict, no shared surface beyond
  the module itself.
- **The rename-aside verification pattern is retired project-wide, as of
  this episode.** Any future episode's manual-verification section must use
  the `$env:APPDATA`/`$env:LOCALAPPDATA` scratch-override pattern in
  CLAUDE.md's hard rules — copy the template there, not an old episode doc.
- **`--via-wire` is real, permanent CLI surface now**, not one-off
  verification scaffolding — see the dedicated section above for why.
