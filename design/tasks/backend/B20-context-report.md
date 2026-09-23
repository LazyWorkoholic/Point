# Episode B20 — `context.report` on the Wire

**B8 already computes everything the Status card needs — budget, per-section
token counts, what the next turn would evict. It is reachable from `cli.py`
and from nowhere else: there is no request that asks for it and no event that
carries it. Put it on the wire, pushed at turn boundaries, calling B8's
existing calculation and never a second copy of it.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/backend/B20-context-report.md`) claims reality and
> completion. Code wins on any disagreement.

**Model:** Sonnet. Two envelope types and one push site over a function that
already exists and is already tested. The hard thinking was B8's.

**Depends on:** B8 (the calculation and `ContextReport.to_dict()`, both
already built), B6 (the envelope and the dispatcher this registers into).

**Consumed by:** F6's Status card, which ships a **marked stub** in its place
today — `"context: not wired yet (B20)"`. A frontend follow-up replaces that
line and builds the expanded context composition card (UI-09), which F6 did
not build at all because there was no data to put in it. See
`design/completed/frontend/F06-right-pane.md`.

---

## Why this episode exists

F6's Build section says the Status card and the expanded context card are
"driven by `usage.updated` and `context.report` events — push, never
polling". Half of that is true. `usage.updated` exists and F6 uses it.
`context.report` does not exist at all:

- `IncomingEventType` (`runtime/events.py:53`) has no `GetContextReport`.
- `OutgoingEventType` (`runtime/events.py:142`) has no `ContextReport`.
- `context.report()` (`managers/context.py:999`) is called from exactly one
  place: `cli.py:656`, backing `context report [--session|--last|--json]`.

So the frontend has no way to ask for it and no way to be told. F6 stopped
before writing code, reported this, and shipped a stub rather than fabricating
a bar B8 never sent — a fake fill bar is indistinguishable from a real one
that is lying about how full the context is.

Everything needed already exists. `ContextReport.to_dict()` was written in B8
with exactly this consumer in mind, and its own docstring says so: *"F6's
status card is the only consumer of this type, so a serialization that drops
fields `describe()` still renders is not a smaller payload, it is F6 being
handed an incomplete contract."* This episode is the delivery, not the
calculation.

---

## Build

### 1. `GetContextReport` request

Register in `IncomingEventType` and the dispatcher. Payload mirrors what
`cmd_context_report` already accepts: an optional `session_id` (absent means
the most recent session, the same `--last` behaviour `latest_session()`
already implements).

The handler calls `context.report(...)` — the existing function, unchanged —
and replies with a `ContextReport` event carrying `ContextReport.to_dict()`
verbatim. **Every field, no subset.** `counts` and `included` are what the
card needs for "6 messages, 2 attachments" and for a per-item breakdown, and
`describe()` is a human rendering that is allowed to change, so nothing may
parse it.

### 2. `ContextReport` push event

Register in `OutgoingEventType`. The same payload shape as the reply above,
so a consumer has one decoder and not two — a reply and a push that differ by
one field is the kind of near-match F6's own handoff warns about.

### 3. Push at turn boundaries

The report changes when the context does, which is when a turn completes and
its messages enter history. Emit one after an assistant message is persisted
— the same point `usage.updated` already rides — with `corr_id` null, because
it is caused by no request.

**Do not emit per token.** The numbers only move at turn granularity, and
F6's Status card is redrawn every frame regardless; a per-delta push would be
hundreds of envelopes carrying an identical payload.

### 4. Reuse, do not reimplement

`context.py`'s own header and F6's spec both say the calculation is not to be
duplicated. This episode adds transport around `report()`. If something the
card needs is missing from `ContextReport`, add it **to `ContextReport`**, in
`context.py`, where `describe()` and `to_dict()` stay in step — not to the
handler.

---

## Do NOT

- **Do not reimplement the token arithmetic**, anywhere, for any reason. Two
  implementations of the same arithmetic disagree eventually and the UI is
  where you find out — `ContextReport`'s own docstring says exactly this.
- **Do not emit on every delta.** Turn boundaries only.
- **Do not parse or ship `describe()`.** It is a human rendering.
- **Do not change `context.build()`'s behaviour.** This episode reads what is
  already computed; it does not alter what enters a prompt. (B21 is the
  episode that changes what enters a prompt, and the two must not be
  combined — see below.)
- **Do not build the frontend half.** The Status card row and the expanded
  card are a frontend follow-up with its own gate.

---

## Done when

1. `GetContextReport` returns a `ContextReport` whose numbers match
   `cli.py context report --json` for the same session, field for field.
2. A `ContextReport` push arrives after a turn completes, with a null
   `corr_id`, and carries the same shape as the reply.
3. Sending a message visibly moves `used_tokens` and `free_tokens` between
   two consecutive pushes.
4. `next_evictions` names real messages once the window is over budget.
5. No push arrives per token — one per completed turn.
6. `cli.py context report` still works exactly as before.

---

## Verification gate (PowerShell, scratch profile)

Per CLAUDE.md: one combined script, and Point's process never sees the real
`%APPDATA%\Point` / `%LOCALAPPDATA%\Point` path at all -- `$env:APPDATA` and
`$env:LOCALAPPDATA` are pointed at a scratch directory for the one script's
duration, restored at the end. `$env:POINT_DB` alone is not enough: it moves
the database but config.toml/state.json/logs still resolve under the real
profile unless APPDATA/LOCALAPPDATA are overridden too. (The old
rename-aside-and-restore pattern is retired -- see CLAUDE.md's hard rules.)

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-b20"
New-Item -ItemType Directory -Force $scratch | Out-Null
$realAppData = $env:APPDATA
$realLocalAppData = $env:LOCALAPPDATA
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend

    uv run python cli.py migrate
    $sid = (uv run python cli.py session new "b20").Split(" ")[0]

    # 1. the CLI's own numbers, as the baseline the wire must match
    uv run python cli.py send "first question" --session $sid
    uv run python cli.py context report --session $sid --json

    # 2. the wire answer for the same session must agree field for field
    #    (drive GetContextReport over stdin, or add a --via-wire flag to the
    #    CLI command if that is cleaner -- record which you chose and why)

    # 3. the numbers must MOVE
    uv run python cli.py send "second, much longer question ..." --session $sid
    uv run python cli.py context report --session $sid --json
    # EXPECT: used_tokens up, free_tokens down, history count up
}
finally {
    $env:APPDATA      = $realAppData
    $env:LOCALAPPDATA = $realLocalAppData
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
```

Eyes-on: with the frontend follow-up not yet built, confirm the push arrives
at turn boundaries and not per token by watching the backend's own log rather
than the UI.

---

## Handoff

- **F6's Status card row stays a stub until a FRONTEND follow-up lands**, not
  when this episode closes. That follow-up is **WORKFLOW.md's SESSION 13d**
  (`S13d-wire-context-card`), and **its task doc is deliberately unwritten
  until this episode closes** — its whole content is "read this event", so it
  cannot be specced until this doc's Record step has said what the event
  actually looks like. Writing it in advance would spec it against a shape
  that may not survive. This gives the data a way to travel; something
  still has to read it. That follow-up also owns the expanded context
  composition card (UI-09), which F6 left unbuilt — and which is the reason
  F6 closed on seven of its eight "Done when" criteria rather than all eight.
- **B21 also touches context assembly and the two must not be merged.** B21
  changes *what enters a prompt* (excluding a user message orphaned by a
  failed task); this episode changes *who can see what already entered*. They
  share `context.py` and nothing else. If B21 lands first, this episode's
  reports will simply reflect the corrected history — no coordination needed
  beyond not doing both in one session.
- `to_dict()` was built for this consumer and should need no changes. If it
  does, that is a signal worth recording: it means B8 guessed wrong about what
  the card needs, and the completed doc should say which field and why.
