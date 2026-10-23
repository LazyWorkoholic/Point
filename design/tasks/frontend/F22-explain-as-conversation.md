# F22 — Explain as an Ongoing Conversation

**Phase:** P2 · **Batchable:** no · **Depends on:** F15 (done) ·
**Backend:** **B38** — new, required. See `../backend/INDEX.md`

> **Spec only. Nothing here is built.** This doc describes intent; a
> completed doc under `../../completed/frontend/` describes reality, and
> there is none yet.
>
> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.**

## Goal

**The explain panel becomes a real back-and-forth, instead of one question
and one answer.**

F15 shipped explain as a single exchange: select code, invoke, an explanation
streams into a panel that splits the Code File pane, `esc` dismisses it, `c`
prefills it into Chat. There is no way to ask *"why does that matter?"* or
*"what breaks if I remove it?"* without starting over — and starting over
re-sends the same region with none of the previous exchange, so the second
answer cannot build on the first.

That is the wrong shape for what explaining code actually is. The first answer
is almost never the question you had.

**The panel is the right place for it, not Chat.** The exchange is *about the
region on screen*, beside the region on screen. Moving it into Chat loses the
adjacency, which is the one thing the panel has. `c` (send to chat) stays as
the deliberate "this belongs in the project's history" gesture.

## Build

### 1. The panel gains entries and an input

`frontend\internal\editor\explainpanel.go`.

Today `explainPanel` holds one text body plus a scroll offset and a wrap
cache. It becomes a short list of entries:

```
┌ explain ───────────────────────────────────────────┐
│ function add(a, b) · lines 11-13                   │   <- unchanged header
│ "explain this"                                     │
│                                                    │
│ It sums its two arguments…                         │
│                                                    │
│ › why no type check?                               │   <- the user's follow-up
│                                                    │
│ Because the function is…                           │
│                                                    │
│ ▏ask a follow-up                        ⏎ send     │   <- new input row
└────────────────────────────────────────────────────┘
```

- **The header does not change.** It names the scope and the original
  question, and it must keep naming the *original* one: the exchange is still
  anchored to one region, and a header that tracked the latest question would
  lose what the panel is about.
- Entries are `(role, text)`. The user's own turns are visually distinct —
  reuse the panel's existing `TextSecondary` / `AccentSecondary` roles rather
  than introducing new ones.
- **Reuse the input.** If F21 lands first and factors the fill prompt into a
  reusable inline text box, use it. If not, this episode's input is
  single-line and says so.

### 2. Scroll, and the one thing that will be got wrong

The panel scrolls with `up`/`down`/`pgup`/`pgdn` today. With a growing
exchange it needs the same thing Chat's transcript and F15's ghost both have:
**follow the live edge while a reply streams, stop following when the user
scrolls away, resume when they come back.**

**Read `editor\viewrows.go`'s header before writing this.** F15 built this
behaviour twice — the first version gave the streaming region its own nested
scroll offset, which produced a sub-viewport with a pinned anchor and content
the user could not scroll past. The panel is a bounded box rather than part
of the file's row sequence, so it does not need the row index; it *does* need
the same single-position discipline. One offset over the whole wrapped
exchange, not one per entry.

### 3. The backend half — B38, and this is the real work

A follow-up is **not** a second independent invocation. For the second answer
to build on the first, the model has to see the first.

Today `writeInvocation` sends `{session, text, agent, editor_context}` and the
backend renders `_render_editor_context` fresh each time. F15 already created
a lazily-created, Editor-owned session (because `SendMessage` requires one),
and `chat\protocol.go`'s `onSessionList` filters it out of Chat's list — so
there *is* a session to accumulate turns in. What does not exist is any
decision about:

- whether the prior exchange is replayed as message history, or summarised;
- whether `editor_context` is re-sent on every follow-up (the file may have
  changed under it — the region shown with answer 1 may not be the region on
  screen at question 3);
- what the token bound is, and what happens when it is hit.

**B38 owns all three.** They are backend questions — `managers\context.py`
and the agent's message assembly — and getting them wrong is how an explain
exchange silently starts answering about stale code.

> ⚠ **The staleness question is the sharp one and it is not hypothetical.**
> B36 exists because pairing a region with a stat taken at a different moment
> is wrong in both directions. The same reasoning applies here: an exchange
> whose turn 1 showed lines 4-8 of a file that has since been edited is an
> exchange about code that no longer exists. **Decide, in B38, whether a
> follow-up re-reads the region, and what the user is told when it changed.**
> F22 must surface that decision in the panel, not hide it.

### 4. Cancel, dismiss, and what `esc` means now

`esc` currently cancels a running task if one is running (`taskRunning()`),
and otherwise closes the panel — a deliberate pairing recorded in
`panelKey`'s comment, because dismissing a reply you will not read *is*
cancelling the work.

With an exchange, `esc` has a third possible meaning: clear the input. Decide
the precedence and write it in `panelKey` beside the existing reasoning.
**Suggested:** running task → cancel; non-empty input → clear it; otherwise →
close the panel, discarding the exchange.

**`ctrl+c` stays copy and only copy.** F15's completed doc, root cause 3, has
the reasoning; a scoped `ctrl+c` fails silently in the dangerous direction.
The panel still has no selection of its own, so `ctrl+c` copies the code
selection — if this episode gives the panel selectable text, that becomes a
real decision and must be stated, not slid in.

### 5. Discarding, and `c` (send to chat)

- Closing the panel **discards** the exchange. There is no persistence story
  here and this episode must not invent one — that is B13's territory.
- `c` sends the **whole exchange** to Chat as a prefill, not just the last
  answer. Still a prefill (F15's tracked limitation), still appended rather
  than replacing the draft.

## Do NOT

- Move the exchange into Chat. The adjacency to the code is the point.
- Persist the exchange. B13 owns memory; this panel is transient.
- Give the panel its own nested scroll offset per entry.
- Re-send the region silently on a follow-up if B38 decides it should be
  re-read — the user must be able to tell which code an answer is about.
- Add a second inline text-box implementation if F21 has already produced one.
- Change the header to track the latest question.

## Done when

1. An explanation arrives; a follow-up can be typed and sent; the second
   answer streams into the same panel below the first.
2. The second answer **demonstrably** sees the first — shown in the completed
   doc by a follow-up that only makes sense with the prior turn (e.g. "why?"),
   plus the rendered prompt from the backend log.
3. The exchange scrolls as ONE sequence, follows a streaming reply, stops on
   a manual scroll and resumes at the live edge.
4. `esc`'s three meanings are ordered, documented in `panelKey`, and each one
   verified.
5. A follow-up after the file changed on disk behaves the way B38 decided, and
   the panel says which region the answer is about.
6. `c` prefills the whole exchange into Chat's draft, appended.
7. Closing the panel discards it; nothing is written anywhere, and the buffer
   is never marked dirty by any path in this episode.
8. The Editor's session still does not appear in Chat's session list.

## Verification gate (PowerShell)

Scratch profile, both env vars, per CLAUDE.md. Demo mode covers 1, 3, 4, 6,
7, 8; #2 and #5 need the backend log and a real multi-turn reply, so Demo's
fixtures will need a second explain turn adding — say so in the completed doc
rather than claiming #2 from a single-turn fixture.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f22"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    cd D:\Projects\Point
    go run .\frontend\cmd\point
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Expect:** no file under the sample project changes mtime.

## Handoff

Record: B38's three decisions as they were actually settled (history replay
vs. summary, region re-read, the token bound and what happens at it); the
`esc` precedence; and whether the input was shared with F21's or built here.

If this episode gives the panel selectable text, that reopens what `ctrl+c`
means in the Editor — F15 settled it as *copy, always* on the grounds that a
state-dependent `ctrl+c` fails silently. A panel with its own selection does
not break that rule, but it does mean "copy what" now has two answers, and
the completed doc must say which one wins and why.
