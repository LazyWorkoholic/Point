# Completed — Frontend

**This directory is the only place completion is claimed.** Task docs in
`../../tasks/frontend/` describe intent; these files describe reality. If the
code and these files disagree, **the code wins** — report the mismatch, do not
silently edit either side.

Written only after Aryan has run an episode's manual verification and confirmed
it passed.

## Status

| # | Episode | State | File |
|---|---|---|---|
| F1 | Bootstrap, Layout, Registry, Theme | ✅ built + manually verified | `F01-bootstrap.md` |
| F2 | Backend Client | ✅ built + manually verified | `F02-backend-client.md` |
| F3–F17 | — | not started | — |

**F1 and F2 were built and verified in one session** (`batchable: yes`).

The two contracts the rest of the frontend is built on:

- **`layout.Compute(w, h int, s State) Layout`** — every episode from F3 renders
  into the rects it returns. Full field list in `F01-bootstrap.md`.
- **`protocol.Envelope` and `protocol.Transport`** — every episode from F3 sends
  and receives through them. Full definitions in `F02-backend-client.md`.

**F3 can start now.** It needs F2 (done) and B2–B3 (done).

## How to add an entry

When an episode passes its verification gate, create `F##-<slug>.md`:

```
# Episode F# — <Title> ✅
**<one-line summary>**

## <package / file path>       ← one section per package or file created
<what it does, why it exists, what depends on it>

## Layout / theme decisions made here
<anything that changed LAYOUT.md or THEME.md, and why>

## Manual Test Log (F#)
<numbered, specific behaviours confirmed AT SPECIFIC TERMINAL SIZES —
 not "it rendered">

## Bugs caught during manual testing
<what broke, why, and the fix — the most valuable section>

## What the next episode inherits
<state, interfaces, and anything deliberately left unfinished>
```

Then add a row to the table above.

## Frontend-specific things worth recording

These recur in TUI work, and writing them down the first time saves the second:

- **Terminal sizes tested** — the actual dimensions, not "it worked".
- **Wide-character bugs** (CJK, emoji) — where display width was used incorrectly.
  The signature is a right border sitting one or two cells off on one line.
- **Keys Windows Terminal swallowed or encoded oddly**, and which ones arrived
  fine. This list is useful for every later episode.
- **CRLF / BOM handling** — anything that made `git status` show a file as fully
  modified after a no-op open-and-save.
- **Render performance** — what was cached, what was measured, at what size.
- **Anything that behaved differently over SSH** versus locally.
- **Breakpoint adjustments.** `LAYOUT.md` marks the numbers provisional; separate
  bordered boxes plus outer margins cost more rows and columns than the original
  estimate. **F1 and F16 should both record the values actually settled on.**
