# Session 12b — F7 Verification Repairs (chat/ render + startup) ✅

**Three defects found while verifying F7, none of them F7's, all of them in
`frontend\internal\chat\`. One had been live since F3. They are recorded here
rather than inside F07's doc because a future session working in `chat/` has
no reason to open an episode doc about popups in `app/`.**

> Commits: `4d8aef6` (the two render fixes), `d3133ba` (startup robustness).
> F7 itself is `e2e9fdd` and shares none of these files.
>
> **The session number is an assumption.** `WORKFLOW.md` names Session 12 as
> F7 Popup Framework, and Sessions 11b/11c set the precedent for a letter
> suffix on follow-on work. Renumber freely if that is wrong — nothing
> depends on the label.

Same shape as S11b: a verification-and-repair session, not a numbered
episode. It consumes no F number, and F6 is still the next episode.

---

## Why this file exists at all

Two of these were found by chasing ONE user-visible symptom — "a code block
streams correctly and then disappears the instant its closing fence arrives"
— and the chase produced a wrong answer before it produced the right one.
That sequence is the most useful thing here, so it is recorded in order
rather than tidied into a conclusion.

---

## 1. The frame-scoped render memo (`4d8aef6`)

**A real defect, fixed, and NOT the cause of the reported symptom.** Recorded
first because the reasoning that found it was sound and the reasoning that
concluded it was the cause was not.

### The mechanism

`renderLive`'s two staleness gates are both **clock**-based: a 50ms throttle,
and per-second staleness so a running slot's timer redraws. A single frame
renders each entry **more than once** — `bottomAnchored` walks backward
measuring heights, `visibleWindow` then walks forward composing, and
`clampScroll` and `moveFocus` measure too, with real time passing in between.

A frame that straddled the throttle boundary therefore got **stale, shorter**
lines for the measuring pass and a **real, taller** render for the composing
pass. `(topAbs, topRow)` is an address computed against the first height and
applied to the second, so rows fell off the window.

The window is widest exactly where an entry's height JUMPS — a raw tail
becoming a boxed code block — and most durable at the end of a stream, where
`armTick` stops once `Streaming()` goes false and nothing repaints the bad
frame.

### The fix

One frame, one render per entry, decided **before any clock rule**:

```go
sameFrame := e.rendered != nil &&
    e.renderedFrame == w.frame &&
    e.servedVersion == e.version &&
    e.renderedWidth == width &&
    e.renderedFocused == focused
```

Two details that are load-bearing, both found by tests rather than by
inspection:

- **The frame boundary is `visibleWindow`, not `View`.** `View` composes the
  query area too, and a key handler measuring between frames should see the
  heights the user is looking at rather than re-decide them. Putting the
  counter in `View` broke `TestStreamingAutoFollowAndScrollBack`, which
  composes via `visibleWindow` directly — not a test artifact, a real
  statement about where a frame begins.
- **The guard keys on `servedVersion`, not `renderedVersion`.** A throttled
  call answers with lines built at an OLDER version and never updates
  `renderedVersion`, so keying on it meant the guard could never hold after a
  throttled serve — reopening the exact hole. `servedVersion` is the version
  the answer was FOR; `renderedVersion` stays the version the lines were
  BUILT at. They differ precisely while the throttle holds an update back.

State still wins over the frame: a token, an opened slot or a toggled
thinking block bumps `version` and re-renders regardless. Only the clock is
stopped from re-deciding mid-frame. In the running app state cannot change
mid-frame at all (Update runs, then View), so this costs nothing there.

### Test

`chat\frame_test.go` — `entryBlock` twice within one frame with the throttle
boundary between them, asserting identical height; then a third call on the
next frame asserting it *does* update, so the throttle is not quietly
disabled. Confirmed red against the pre-fix code: `measured 7 rows, composed 8`.

---

## 2. `renderList` dropped every non-paragraph block child (`4d8aef6`)

**This was the actual cause of the disappearing code blocks, it is
deterministic, and it had been live since F3 built the markdown walker.**

### The mechanism

A list item's children are **block** nodes. `renderList` handled exactly two
cases: a nested `*ast.List`, and "assume it is inline-bearing" →
`collectInlineWords`. A `*ast.FencedCodeBlock` has **no inline children** —
its content lives in `Lines()` segments — so it contributed zero words and
vanished silently, leaving the surrounding prose looking intact. Indented
code went the same way.

Since models format nearly every multi-step answer as a numbered list with
code inside the steps, that was most of the code in a code-heavy reply.

### Why it looked like a streaming race

It is invisible while streaming. Until the closing fence arrives,
`splitOpenFence` routes everything from the fence onward into the raw tail,
which renders as **plain visible text**. The block disappeared at the exact
moment the fence closed and the text first went through the walker. Hence
"streams correctly, vanishes on fence-close" — and hence the initial,
incorrect conclusion that it was a render race.

The correction came from the user, not from the code: *a race fix cannot
explain a failure that became more reproducible after it landed.* That was
right, and it forced the restart that found this.

### The fix

Every block child renders as the block it is, via `renderBlock` at the
**item's** content width, interleaved with inline words in document order,
one blank row between siblings (the same rule `renderChildrenBlocks` applies
at the top level). The code box is indented under the bullet — content aligns
to its container, not to the terminal edge.

### Scope, measured

| shape | before | after |
|---|---|---|
| fenced code in an ordered list | **dropped** | renders |
| fenced code in a bullet list | **dropped** | renders |
| indented code in a list item | **dropped** | renders |
| paragraph → code → paragraph in ONE item | code **dropped** | all three, in order |
| two paragraphs in a list item | fine | fine |
| blockquote in a list item | fine | fine |
| nested list | fine | fine |
| code at top level (control) | fine | fine |

`chat\markdown_list_test.go` is that table, asserting presence **and document
order**, plus a separate test pinning the box's indent and width. Confirmed
red against the old `renderList`: exactly the four dropped cases fail.

### One line of tidying, same commit

A fenced block's content is now read with `Lines().Value(source)` rather than
the deprecated `Text(source)`, matching the `*ast.CodeBlock` case directly
below it. They agree in goldmark 1.8.5 — which is why this read correctly —
but `Text()` walks inline children and its behaviour for block nodes has
already changed once across versions.

---

## 3. A stale session row could strand the app (`d3133ba`)

### The mechanism

Three things true at once, none a bug alone:

1. A session is pinned to a provider (`sessions.provider`, nullable).
2. The backend resolves that name **only when something is SENT** — nothing
   looks at it when the session is opened.
3. `CreateSession` is reachable **only from an EMPTY session list**
   (`onSessionList`), and the frontend opens the most-recently-updated
   session at startup.

So a stale row at the top of the list opened fine, failed every send with
`No provider registered as 'x'`, and offered **no way to any other session
from inside the app**. Not a crash — a dead end.

Found from three real rows: `provider='mock'`, `model='mock-slow'`, titled
"cancel check", left in the live profile by B5/B6-era cancellation testing
instead of a scratch one — the exact accident CLAUDE.md's B7 note warns
about. `mock` registers only under `POINT_MOCK=1`. It looked intermittent
because it self-heals the moment any newer session is written and returns as
soon as those rows are newest again: a stable failure state, not a flaky one.

### The fix

Bootstrap asks `ListProviders` first, then opens the newest session whose
provider the backend actually registered, falling through to `CreateSession`
when none qualifies — which is what guarantees the app always reaches a
usable state. `ListSessions` **already carried `provider`** on every session
(`runtime.py`'s `_session_to_dict`); the Go `wireSession` simply never
decoded it. Frontend-only, no wire change, no backend change.

Three deliberately permissive cases, each tested, each with a reason:

- An **empty** provider means "inherit the global config" (NULL is not
  "missing") and is always resolvable.
- An **unknown** provider set — the request failed, or the backend reported
  none — rejects **nothing** and behaves exactly as before the check existed.
  A check that could not be run must never become a reason to withhold the
  user's own most recent session.
- **Health is not consulted.** A registered-but-unreachable Ollama is still
  worth opening; that send failure is honest and temporary, and skipping past
  it would hide recent work for a reason that fixes itself.

A refused `ListProviders` is a **soft dependency**: it logs, still asks for
the session list, and never sets `loadErr`.

### A claim corrected along the way

An earlier reading of mine — that any backend `Error` wipes the transcript
behind a full-screen banner — was **wrong**, and checking rather than
assuming is what found the real trap. An untracked `Error` returns early, and
a failed send is matched to its live entry by `handleStreamEvent`, so the
transcript survives and the failure renders on the reply where it belongs.
`loadErr` is reachable only from a tracked load query. The trap was narrower
than "everything breaks" and entirely about **reachability**.

### Tests

`chat\providers_test.go`, seven of them: newest-unresolvable is skipped;
all-unresolvable starts fresh; inherit-global is never skipped; unknown set
opens the newest; an empty provider list does not reject everything;
bootstrap orders the two queries; a refused `ListProviders` still opens a
session and never sets `loadErr`. The two load-bearing ones confirmed red
against the previous `onSessionList`.

---

## What a future session inherits

**Do not undo any of these three by accident.** Each has a shape that looks
like it could be simplified and cannot:

- **The frame counter is not redundant with the throttle.** The throttle
  limits parses ACROSS frames; the counter guarantees consistency WITHIN one.
  Removing either reopens a different failure. If `renderLive` grows a third
  staleness rule, it must go **after** the `sameFrame` guard, not before it.
- **`renderList` must render block children as blocks.** Any future "simplify
  by collecting words" refactor reintroduces the F3 bug exactly. The table
  test is there to make that loud.
- **A session's provider is not validated anywhere else.** If F6 adds session
  switching, it inherits the same question — switching *to* a session with an
  unresolvable provider has the same dead end, and `knownProvider` is the
  existing answer to reuse rather than re-derive.

**Where the reasoning lives:** all three carry their full explanation in code
comments at the site — `chat.go`'s `frame` field, `markdown.go`'s
`renderList`, and `providers.go`'s file header, which is written as the
narrative of the whole mock-session failure. A session reading `chat/` code
should find the reasoning before it finds this file.

**Not fixed, found in passing:** `w.copyNote` is written by `copy.go` and
read by nothing, so the `y`-copy confirmation never renders anywhere. It
belongs to whichever episode next touches copy behaviour.
