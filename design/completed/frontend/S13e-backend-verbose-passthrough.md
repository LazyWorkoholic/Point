# Session 13e — Backend Verbose Passthrough (`POINT_BACKEND_VERBOSE`) ✅

**A Go-launched app can now run its backend with `--verbose`, so the
backend's DEBUG lines — B20's context-report push confirmation among them —
land in `point.log` without launching the backend by hand.**

> Verified by Aryan on 2026-09-25 against
> `tasks/frontend/S13e-backend-verbose-passthrough.md`, on a scratch profile
> (CLAUDE.md's APPDATA/LOCALAPPDATA override). Full Go and Python regression
> sweep clean.
>
> Same shape as S11b/S12b/S13d: a wiring-and-repair session, no F number.
> Found during S13d's verification gate; closes the gap S13d's handoff
> flagged.

---

## Where the code went

```
frontend\internal\protocol\
├── spawn.go        + BackendVerboseEnv = "POINT_BACKEND_VERBOSE"
│                     buildCommand appends --verbose when, and only when,
│                     os.Getenv(BackendVerboseEnv) == "1"
└── spawn_test.go    NEW — TestBuildCommandVerboseEnv: unset / "1" / "true" / "0"
```

No backend change. No CLI flags added to `frontend\cmd\point`.

---

## Done when — closed

1. **With `POINT_BACKEND_VERBOSE=1`**, a Go-launched app's `point.log`
   contained exactly one `B20: pushing context report` line per completed
   turn.
2. **Without it** (checked with the var unset, after a real completed send,
   verified before the scratch profile was deleted), zero DEBUG lines —
   same as today.
3. **The argv test passes for all four cases** — unset, `"1"`, `"true"`,
   `"0"` — confirming the exact-match-on-`"1"` rule holds in both
   directions (on for `"1"`, off for everything else, including strings
   that look truthy).
4. **`backend-stderr.log`'s growth is measured:** 1284 bytes after one
   message, 3790 bytes after five messages sent sequentially in the same
   session — confirms the stream is genuinely appended per-message, not
   truncated or rotated (none was added here; that stays a later episode's
   call).
5. **A deliberate backend exit under verbose still reports its real error.**
   Killed the backend twice in one run. First kill: banner
   `! backend not connected - retrying...` and footer
   `! backend lost - retrying...`. Second kill inside a minute correctly
   escalated to `! backend not connected - retry from the status bar` and
   `! backend has failed twice in a minute`. Both fully legible despite the
   `stderrTail` ring buffer being full of DEBUG noise under verbose — the
   real error still surfaces, because it's written last and the buffer
   keeps the tail, not the head.

---

## Handoff

- **`POINT_BACKEND_VERBOSE=1` is the one supported way to see backend DEBUG
  from the real app.** Exactly `"1"` — anything else, including `"true"` and
  `"0"`, is off. Name it in any future gate that greps `point.log` for a
  DEBUG line, so no one repeats S13d's dead end (spawning the app with no
  way to ask for `--verbose`, and `go run … --verbose` being silently
  ignored because `frontend\cmd\point` parses no flags).
- **`backend-stderr.log` measured growth: 1284 → 3790 bytes across 1 → 5
  messages**, not rotated, `O_APPEND`. Every verbose DEBUG line is written
  twice — once to `point.log` (rotated), once here (not rotated). Acceptable
  for a debugging switch; rotation is still a later episode's decision, not
  taken here.
- **`stderrTail` stays legible under verbose DEBUG noise**, confirmed with
  two deliberate kills in one run:
  - single failure: banner `! backend not connected - retrying...`, footer
    `! backend lost - retrying...`
  - second failure within a minute: banner
    `! backend not connected - retry from the status bar`, footer
    `! backend has failed twice in a minute`
  The real error is written last into the ring buffer, so it survives being
  crowded by DEBUG lines. The buffer was not widened.
