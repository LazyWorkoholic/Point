# F21 — Multi-line Fill-Prompt Input

**Phase:** P2 · **Batchable:** no · **Depends on:** F15 (done) ·
**Backend:** **B37** — new, required. See `../backend/INDEX.md`

> **Spec only. Nothing here is built.** This doc describes intent; a
> completed doc under `../../completed/frontend/` describes reality, and
> there is none yet.
>
> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.**

## Goal

**The fill prompt accepts a request longer than one line.**

F15 shipped the inline prompt as a single-line field: `fillPrompt` holds
`runes []rune` and a `col`, renders one row at the caret, and
`flattenToOneLine` collapses any newline that arrives by paste. That was the
right size for F15 — "`add a docstring`", "`handle the nil case`" — and it is
the wrong size for the requests people actually have for a 40-line region:
constraints, a style to follow, two or three things at once.

Today those must be typed as one run-on line that scrolls sideways behind
`‹`/`›` markers, with no way to see the whole request while writing it.

**This episode is about the INPUT only.** What the agent does with a longer
request, and whether a longer request produces a better proposal, is not this
episode's claim and must not be asserted in its completed doc.

## Build

### 1. The prompt becomes a small multi-line editor

`frontend\internal\editor\prompt.go`.

- `runes []rune` + `col int` becomes a line model. **Reuse the existing
  types rather than inventing a second one**: F13a's `Buffer` already owns
  lines, a `Pos`, selection and an undo history with a coalescing rule this
  prompt had to re-implement once already (and got wrong — see F15's
  completed doc, root cause 2). Decide explicitly whether the prompt now
  holds a `Buffer` or keeps its own slice; if it keeps its own, the
  coalescing rule must be the SAME code, not a second copy.
- Soft-wrap at the prompt's content width. Not horizontal scroll: a request
  being composed should be readable in full, which is the whole point.
- Height is **bounded**. Grow with the content up to a cap (suggest 6 rows),
  then scroll within the prompt. A prompt that can grow to fill the pane
  hides the code the request is about.

### 2. `enter` versus newline — decide this first, it is the whole UX

Two keys, two meanings, and whichever way it goes the other one must exist:

| | submit | newline |
|---|---|---|
| A | `enter` | `shift+enter` or `alt+enter` |
| B | `ctrl+enter` | `enter` |

**A is the recommendation**, for one reason: `enter` already submits today,
and every existing user's muscle memory would break under B. But **check
`shift+enter` arrives at all on Windows before committing to it** — this is
precisely the class of assumption F15 paid for three times. `tea.Key` carries
no Shift field; `key_windows.go` maps `VK_RETURN` to `KeyEnter` and the
shift bit is not in `KeyMsg`. **If `shift+enter` is indistinguishable from
`enter`, use `alt+enter`** — Alt *is* carried on `KeyMsg`, unconditionally
from `ControlKeyState`.

> ⚠ The AltGr hazard that rules out Alt+\<letter\> bindings (see
> `actions.Leader`'s doc comment and `F16-polish.md`) does **not** apply
> here: AltGr composes *characters*, and `alt+enter` is not a character a
> layout can produce. Confirm it, do not assume it.

The chosen keys must appear in the prompt's own footer. A multi-line input
whose newline key is undiscoverable is worse than a single-line one.

### 3. Paste stops flattening

`flattenToOneLine` exists because the prompt could not hold a newline.
Remove it. Pasted newlines become real lines.

**Keep a bound**: a paste of 500 lines into a prompt capped at 6 visible rows
should be accepted and scrollable, not truncated silently and not allowed to
make the pane unusable. Decide the limit and say so in the footer or a status
line when it bites.

### 4. The row budget — this is already handled, do not rebuild it

F15's unified row index (`editor\viewrows.go`) models injected surfaces as
**blocks** of N rows in the document's one row sequence. `viewIndexAt` adds
the prompt as a 1-row block today:

```go
if w.prompt != nil {
    add(d.cursor.Line, 1)
}
```

A multi-line prompt is the **same block with a different row count**.
Scrolling, the caret reveal, the hit-test and the "click on an injected row
does nothing" rule all follow for free, because they all read the index.

**Do not** emit prompt rows from the renderer and teach the hit-test about
them separately. That is the two-derivations mistake F15 removed twice.

Two things do need attention:

- `injectedRowView(d, line, blockRow, ...)` must return the `blockRow`-th
  row of the prompt, not the whole prompt.
- `ensureVisible`'s `extra` argument is `1` today, hard-coded in
  `codeFileBox` precisely because the prompt was one row. It becomes the
  prompt's real height. The comment there explains why `extra` is not
  "all of the caret line's injected rows" — that reasoning still holds and
  the ghost must still be excluded.

### 5. The backend half — B37

The question travels as `text` in `CmdSendMessage` (`writeInvocation`), and
`_render_editor_context` renders it into a prompt that **already contains a
newline-separated instruction block**:

```
[editor context — app.py, mode=fill, cursor at line 7, shown lines 4-8]
<the region>

Respond with exactly one JSON object and nothing else, matching:
{"file": "<path>", ...}
start_line/end_line must fall within the shown region, one file only. …
```

A multi-line user request dropped into that unfenced is a **prompt-injection
surface against Point's own instructions** — not from an attacker, but from
an ordinary user whose second line happens to read like an instruction
("ignore the shown region", "respond in plain text"). **B37 defines how a
multi-line request is delimited** so the model can tell the request from the
instructions. Frontend sends; backend decides the rendering. See
`../backend/INDEX.md`.

**F21 must not work around a missing B37 by flattening the text before
sending it.** That would be this episode shipping the bug it was written to
avoid, invisibly.

## Do NOT

- Make the prompt a full editor. No syntax highlighting, no search, no
  multiple cursors. It is a text box.
- Let the prompt grow unbounded.
- Re-implement undo coalescing a third time.
- Change what the agent does with the request. Input only.
- Flatten newlines anywhere on the send path as a substitute for B37.

## Done when

1. A request of several lines can be typed, seen in full, and edited with the
   arrows, `home`/`end` and `backspace` across line boundaries.
2. The submit key and the newline key are **both** bound, both discoverable
   in the prompt's footer, and the newline key is one verified to arrive
   distinctly on Windows — stated in the completed doc with the evidence.
3. `ctrl+z` in the prompt removes one word-run, not the whole request, across
   a newline as well as within a line (F15's root cause 2, re-verified against
   the new line model).
4. A paste containing newlines produces real lines, up to the documented
   bound.
5. The prompt at its maximum height does not push the caret line off the pane,
   and a click on any row of the prompt still moves nothing.
6. A multi-line request reaches the backend **delimited** (B37), and the
   rendered prompt is shown in the completed doc for a request whose second
   line reads like an instruction.
7. `esc` still cancels with nothing sent, and the buffer is untouched.

## Verification gate (PowerShell)

Use the F15 gate's scratch-profile pattern — both `APPDATA` and
`LOCALAPPDATA` to a temp directory for the script's duration (CLAUDE.md's
rule; do not reintroduce rename-aside). Demo mode is enough for every check
except #6, which needs the rendered prompt, so read it from the backend log
rather than from a model's reply.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f21"
New-Item -ItemType Directory -Force $scratch | Out-Null
try {
    $env:APPDATA      = $scratch
    $env:LOCALAPPDATA = $scratch
    $env:POINT_DEMO_LONG_FILL = "1"
    cd D:\Projects\Point\backend
    uv run python cli.py migrate
    cd D:\Projects\Point
    go run .\frontend\cmd\point
}
finally {
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
```

**Expect:** the mtime of every file under the sample project is unchanged
after the run — this episode writes nothing, same as F15.

## Handoff

Record: the newline key actually chosen and **what was measured** about the
rejected one; the height cap and the paste bound; and whether the prompt
ended up holding a `Buffer` or its own line slice, with the reason.

If the prompt does end up holding a `Buffer`, say so loudly — F22's explain
input and any future inline text box should then use the same thing rather
than a third implementation.
