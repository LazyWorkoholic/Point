# F23 — Fill on a Blank Line (Generation, not Rewriting)

**Phase:** P2 · **Batchable:** no · **Depends on:** F15 (done), F9b (done) ·
**Backend:** **B39** — new, required, and it is the larger half.
See `../backend/INDEX.md`

> **Spec only. Nothing here is built.** This doc describes intent; a
> completed doc under `../../completed/frontend/` describes reality, and
> there is none yet.
>
> **Scheduled after v1 ships, time permitting — otherwise deferred to a
> later session.**

## Goal

**Fill works on an empty line, where there is no existing code to rewrite.**

Every part of the fill path today assumes there is a region. `resolve_scope`
finds the enclosing function, or falls back to ±20 lines; the prompt asks the
model for a `replacement` for `start_line`–`end_line`; B24 validates that the
proposed range falls inside the region the model was shown; F9b's diff card
renders a line diff of **old versus new**.

On a blank line in an empty area none of that describes the request. The user
is not asking for a rewrite — they are asking for something that is not there
yet. Invoking fill there today gets one of two wrong answers: the ±20-line
fallback hands the model a slab of unrelated code and asks it to rewrite all
of it, or the enclosing-scope heuristic attaches the request to whatever
function happens to be above the cursor.

**This episode is about naming that case and handling it as what it is:
generation.**

## What "fill on a blank line" means — scope this before anything else

**It is pure generation from a prompt, with no diff-against-existing-code
semantics.**

| | rewrite (today) | generate (this episode) |
|---|---|---|
| what the model is shown | the region, and asked to replace it | surrounding code as **read-only context**, and asked to produce new code |
| what comes back | `replacement` for lines *a*–*b* | code to **insert at** line *a* |
| what the card shows | a line diff, old vs new | the proposed insertion — **additions only** |
| what apply does | splice over *a*–*b* | splice **at** *a*, replacing nothing |
| what `ctrl+z` removes | the whole splice | the whole insertion (unchanged) |

Three consequences worth stating plainly, because each is a thing not to
build:

1. **There is no "old side" to diff.** A diff card showing every line as an
   addition is correct and is not a degraded diff. Do not fabricate an old
   side by diffing against the blank line.
2. **Range validation changes meaning.** B24 rejects a proposal whose range
   falls outside the shown region. In generation mode the shown region is a
   single empty line; a proposal *must* be allowed to be longer than it, and
   must still be pinned to that insertion point. **This is B39's central
   decision and the thing most likely to be got wrong** — loosening the check
   rather than giving generation its own check would weaken the rewrite path
   too.
3. **Indentation is part of the request, not a detail.** Code generated for
   an empty line inside a class body must arrive at the body's indentation.
   Decide whether the frontend sends the expected indent, the backend infers
   it, or the model is told — and say so. The buffer will not fix it
   afterwards.

### What is NOT in scope

- **A blank line inside an existing function is still a rewrite.** If
  `resolve_scope` finds a real enclosing scope, nothing changes — the user is
  adding to code that exists, and the existing path handles it correctly. This
  episode is for a cursor with **no enclosing scope**, which in practice means
  between top-level definitions, in an empty file, or at the end of a file.
- **Creating a new file.** `F16-polish.md` carries file creation; fill does
  not create files, and B24's "one file only, refuse if a second file is
  needed" rule stands.
- **Multi-location generation.** One insertion point, one proposal.

## Build

### 1. Detecting the case — decide WHERE, and do not duplicate it

`resolveScope` exists on both sides: `editor\context.go`'s Go copy and
`managers\editor_context.py`'s `resolve_scope`. The Go side computes
`enclosing` and the backend honours it outright when given ("a real editor
already computed it against the actual buffer").

So the frontend already knows whether there is an enclosing scope. **The
frontend decides the mode** and sends it; the backend must not re-derive it
from the text and risk disagreeing. That keeps one answer to "is this
generation?" — the same single-source discipline F15's row index applied to
row mapping.

`EditorContext.mode` is `fill` / `explain` today. Generation is a **third
mode**, not a flag on fill: a flag means every existing branch has to be
audited for whether it still applies, which is how B11's retrofit trap worked.
Name it in B39, used by both sides.

Suggested trigger, to be confirmed against the real heuristic: the caret line
is blank (or whitespace-only) **and** `findEnclosing` returns nothing.

### 2. The ghost, which mostly already works

Ghost text is already a `[]string` injected as rows after an anchor line
(`editor\ghost.go`, `editor\viewrows.go`). An insertion at line *a* is a
block anchored at *a-1* with no region being replaced — which is what the
ghost already draws, every time, because **it has never overlaid the code it
sits next to; it has always pushed real lines down**.

`partialReplacement` reads the `replacement` field out of a partial reply for
the preview. If B39 names the field differently for generation mode, this
needs the second key; if it keeps `replacement`, nothing changes here.
**Prefer keeping the field name** — a different name buys nothing and splits
the decoder.

### 3. The diff card, additions-only

`frontend\internal\diff\Region()` takes old and new lines and returns an LCS
diff. With an empty old side it already returns all-additions — verify rather
than assume, and if it does, **call it unchanged**. F9b's `Region(nil, ...)`
panic is on record, so pass an empty slice with intent and test it.

The card's footer and its wording change: "apply" is right, "re-generate" is
right, and anything that reads as "replace" is not.

### 4. Apply — `ApplyRegion`, or its sibling

`doc.ApplyRegion` splices over a line range as ONE undoable edit. An
insertion is the degenerate case: a range of zero lines at *a*. Check whether
`ApplyRegion` already handles an empty range correctly as one `editRecord`,
and if it does, **use it** rather than adding `ApplyInsertion` beside it. Two
near-identical splice entry points is how the undo guarantee gets broken for
one of them.

The two-phase apply stays exactly as F15 built it: guards 1–5 synchronously,
then `CheckFileStale` with B36's recorded pair, then splice only on
`changed:false`. **Nothing about generation mode makes the staleness guard
optional** — the file can still have changed under an insertion point.

### 5. The backend half — B39

- the third mode, named, and `resolve_scope`'s behaviour when it is set;
- what the model is shown (surrounding code as read-only context, how much,
  and how it is marked read-only so the model does not rewrite it);
- the instruction block for generation — and note that the existing fill
  instructions contain the `"<path>"` placeholder bug tracked in
  `F16-polish.md`; **do not copy that line into a new mode**;
- validation: an insertion point plus a length bound, instead of a range
  inside a shown region;
- the `(mtime, size)` pair, captured the same way B36 does — from the same
  descriptor as the read, never a separate stat.

## Do NOT

- Loosen B24's range check for the existing rewrite path.
- Fabricate an old side so the diff looks like a normal diff.
- Treat generation as a flag on fill mode.
- Create files, touch more than one file, or write anything to disk. F15's
  rule stands: an agent-proposed change reaches disk only through a buffer
  the user saves.
- Re-derive the mode in the backend from the file text.
- Add a second splice entry point if `ApplyRegion` already covers an empty
  range.
- Copy the `"<path>"` placeholder line into the new mode's prompt.

## Done when

1. Cursor on a blank line with no enclosing scope → fill is offered and the
   prompt opens, with the status line or the prompt footer making clear this
   is generation rather than a rewrite.
2. The ghost streams the generated code at the cursor, pushing real lines
   down, and nothing is overlaid.
3. The diff card shows **additions only**, with no fabricated old side, and
   its wording never says "replace".
4. `⏎` inserts at the cursor, replacing nothing, and **one** `ctrl+z` removes
   the whole insertion.
5. Generated code arrives at the correct indentation for its position —
   verified inside a class body and at top level.
6. The file's mtime is unchanged until the user saves.
7. A blank line **inside** an existing function still takes the rewrite path
   unchanged — shown by a test, because this is the regression this episode is
   most likely to cause.
8. Modifying the file on disk after generating and before applying is still
   refused on the card, with a reason (F15's 7b, re-verified for insertion).
9. A request needing a second file or a new file is still refused in plain
   text.

## Verification gate (PowerShell)

Scratch profile, both env vars, per CLAUDE.md. Demo mode will need a
generation-mode fixture; the sample project will need a blank area with no
enclosing scope. Both are additions this episode owns — note in the completed
doc which Done-whens were checked against a fixture and which against a real
model.

```powershell
$ErrorActionPreference = "Stop"
$scratch = Join-Path $env:TEMP "point-scratch-f23"
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

Then, for Done-when 6, with a file open and a generation applied but NOT
saved:

```powershell
(Get-Item D:\Projects\Point\.point\sample\app.py).LastWriteTime
```

**Expect:** identical before and after apply-then-undo.

## Handoff

Record: the mode's name as B39 settled it; the indentation decision and which
side owns it; whether `ApplyRegion` took the empty range unchanged; and
whether `diff.Region` needed anything for an empty old side.

**State explicitly whether generation mode made the rewrite path's validation
weaker.** That is the one way this episode can do real damage, and a completed
doc that does not answer it leaves the question open for whoever reads B24's
checks next.
