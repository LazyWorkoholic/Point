# Episode F3 — Chat Viewport ✅

**The transcript renders: markdown, fenced code, CJK/emoji-safe wrapping, a
virtualized scroll that stays O(rows) at 10,000 messages, and the real Chat
workspace replacing F1's placeholder.**

---

## `frontend\internal\chat\` — the whole episode

| File | Lines | What |
|---|---|---|
| `chat.go` | 114 | `Workspace` struct, `Options`, `New`, `ID`/`Cards`/`Init`/`Suspend`/`Resume` |
| `protocol.go` | 211 | bootstrap (`ListSessions` → `GetSession`/`CreateSession`), `Update` |
| `model.go` | 65 | `wireMessage`/`wireContent`/`wireSession` — mirrors `managers\message.py` |
| `markdown.go` | 244 | goldmark AST walked by hand into styled, wrapped lines |
| `codeblock.go` | 160 | chroma tokenising, mapped onto existing theme roles |
| `wrap.go` | 106 | word-level wrap that never splits a styled run mid-escape |
| `render.go` | 244 | per-role message rendering, the viewport/query-area panes |
| `scroll.go` | 219 | the windowed scroll/focus/pin state machine, exchange spacing |
| `cache.go` | 45 | the render cache — see "The message → block pipeline" below |
| `box.go` | 96 | titled pane box + titleless message box (local copy of `app\box.go`'s algorithm) |
| `textutil.go` | 103 | display-width pad/truncate/strip (local copy, see below) |
| `copy.go` | 34 | OSC 52 raw-text copy |

24 test functions across `main_test.go`, `markdown_test.go`, `workspace_test.go`
(2,347 lines total including tests). `go build ./...`, `go vet ./...`,
`go test ./frontend/...` all green throughout.

**Duplication, noted on purpose:** `box.go` and `textutil.go` re-implement
`app\box.go`'s border-drawing and display-width primitives rather than
importing them (they are unexported, and `chat` cannot import `app`'s
internals without the same import-direction problem `chat.Workspace` itself
has — see below). F1's handoff already flagged lifting these into a shared
`ui` package as "F3 or F7" work; it is now owed twice. Left alone here
because it is bigger than this episode's Build section, not because it was
missed.

### Why `chat` imports `app`, not the other way around

`app.Workspace.Update` is declared `Update(tea.Msg) (Workspace, tea.Cmd)` —
the return type is `app.Workspace` by name, not a structural interface, so
whatever package implements it has to import `app` to spell that type.
`app` therefore cannot import `chat` back (import cycle), which means `app.New`
cannot construct the Chat workspace itself the way it constructs the
placeholder. The fix is `Root.SetWorkspace(id, ws)` (new, in `root.go`):
`main.go` builds `chat.New(...)` and installs it after `app.New` returns,
before `program.Run()`. **Any future workspace package — F9's editor
workspace included — needs the same treatment**: it will not be able to live
inside `app`, and `main.go` is where it gets wired in.

---

## The message → block pipeline

This is the structure F5 (streaming) and F9 (task slots) both need to plug
into, not reinvent. Four layers, each with one job:

**1. `wireMessage` → `[]string` — `renderMessage` in `render.go`.**
The single entry point. Dispatches on `msg.Role`:

```go
func renderMessage(th theme.Theme, msg wireMessage, width int, focused bool) []string
```

- `roleUser` → `renderUserMessage`: a titleless rounded box (`messageBox`,
  `box.go`) at `userBoxWidthRatio` (0.72) of `width`, right-aligned — blank
  margin on the left only — with a `"User:"` label line prepended, right-
  aligned to the same edge the box is.
- `roleAssistant` → `renderAssistantMessage`: a `"Point:"` label line
  prepended, left-aligned, then every content line prefixed with the
  `▎ ` accent bar (`theme.AIBar`, or `theme.BorderFocus` when `focused`).
- `roleSystem` / `roleError` / `roleTool` / anything unrecognised →
  `renderPlainRole`: dim/error-styled plain text, no box, no bar.

**This switch is the entire AI-bar-vs-user-box decision.** Nothing above it
— caching, scrolling, spacing — knows or needs to know which shape a
message took; everything above only ever deals in an opaque `[]string`
block and its line count. That is the seam: producing a `[]string` through
something shaped like `renderMessage` is what it takes to participate in
scroll/cache/spacing, whatever the message actually is.

**2. Caching — `cache.go`.** `renderCached(msg, width)` is a read-through
cache keyed by **`msg.ID` alone** (not width — see below), at one shared
`w.cacheWidth` for the whole workspace:

- A width change (`width != w.cacheWidth`) invalidates the **entire** cache
  and resets `cacheWidth` — there is no per-entry width variant, because
  the viewport only ever renders at one width per frame.
- A cache hit returns the stored block outright. A miss calls
  `renderMessage(..., focused=false)`, stores it, appends the id to
  `cacheOrder`, and evicts if `len(cacheOrder) > scrollback * 2`
  (`cacheEvictFactor`), trimming back down to exactly `scrollback` entries.
- **The focused message is never cached.** `visibleWindow` (`scroll.go`)
  calls `renderMessage(..., focused=true)` directly for whichever index
  equals `w.focus`, bypassing `renderCached` entirely, so moving focus by
  one message never has to invalidate (and re-render) two cache entries —
  only the one gaining focus and the one losing it get a single fresh
  render each, on the frame it happens.

**3. Windowing — `scroll.go`.** `visibleWindow(rows, width)` is the only
place that ever measures more than one message: it walks from `topAbs`
(pinned: computed backward from the newest message by `bottomAnchoredTop`;
unpinned: slid by `ensureFocusInWindow` to keep focus in view) and
concatenates blocks until `rows` lines are filled. Cost is **O(rows)
`renderCached` calls per frame, never O(len(all))** — the property that
keeps a 10,000-message session's scroll smooth, verified directly (see
Manual Test Log).

**4. Exchange spacing — `gapLines`, also `scroll.go`.** The 1-row/2-row gap
between messages (Design Change C, below) is **not part of a cached
block** — it is inserted between blocks at windowing time, computed fresh
from the `(previous role, this role)` pair on every boundary. This is
deliberate: a message's own rendered block is independent of its
neighbours, so the cache key stays just `msg.ID`; only the *gap around* a
message depends on context, and recomputing that per boundary is O(1) and
cheap enough to never need caching itself. `bottomAnchoredTop` and
`ensureFocusInWindow` both fold the gap into their row-budget accounting so
a screen near a spacing boundary does not overflow once the blank rows are
actually drawn.

### What F5 (streaming) needs from this

A message mid-stream is not a `wireMessage` yet — it is accumulating token
deltas. F3.md's own line, *"Only the streaming message re-renders per
token,"* describes exactly the **focus-bypass pattern above, generalised**:
F5's in-flight message should render fresh on every delta (never entering
`w.cache`) the same way the focused message already does, while everything
else in the transcript keeps reading from cache untouched. Once the reply
reaches a terminal state it becomes an ordinary `wireMessage` and should
enter `w.all`/the cache exactly like any message loaded from `GetSession` —
no separate code path needed at that point.

### What F9 (task slots) needs from this

`renderTaskSlot(th, taskID, status, width) string` already exists in
`render.go`, unwired — F3.md asks for the slot's own rendering to live here,
but nothing in this episode can create one (that needs F4's input to send
from). It produces one line for a given `taskID`/`status`. **F9 has to
decide how a slot takes a place in the message sequence** — either as a
synthetic entry mixed into `w.all` (giving it a stable id for the cache and
a role for `gapLines` to reason about), or a parallel list the windowing
code also has to know how to interleave and measure. Either way, the same
three invariants the pipeline above already relies on have to hold: a
stable id per slot (cache key), *some* role or role-like value `gapLines`
can switch on, and a block whose height is just `len(block)` — nothing else
in `scroll.go` cares what produced it.

---

## Design Change C — applied mid-episode

Requested by Aryan after F3's initial build and verification; supersedes
F3.md's original wording (a full-width user box titled `:User`).

1. User box: rightmost `userBoxWidthRatio` (0.72, inside the requested
   70–75% range) of the viewport's content width, blank margin left only.
   `"User:"` label right-aligned to the same edge.
2. AI replies: `"Point:"` label, left-aligned, added **above** the existing
   accent bar — the bar was not replaced, every content line still carries
   it. Reply content still wraps at the full content width, unchanged.
3. Spacing (`gapLines`): 1 blank row user→assistant (same exchange), 2 blank
   rows assistant→user (new exchange). Any other role transition (a
   system/error/tool message involved, or two same-role messages in a row)
   was not specified — defaulted to 1 blank row, the least surprising
   choice, and documented as a default rather than a spec'd number.

Tests: `TestUserBoxIsRightAlignedAndNarrower`,
`TestAssistantMessageHasPointLabelAndKeepsBar`, `TestExchangeSpacing`.

---

## Layout / theme decisions made here

- **View Port's own 2-cell horizontal padding**, on top of the generic
  1-cell "box border → content" every other box uses — LAYOUT.md names both
  numbers but not which boxes get which; F3 reads "viewport inner padding:
  2 cells" as View-Port-specific (`paneHorizontalPadding`), and everything
  else (Query Area, a user message's own box, a code block's own box) as
  the generic 1 (`genericBoxPadding`). Wrap width for message content is
  `(viewport inner width) − 2×2`, matching F3.md's "inner width − 4" rule
  exactly.
- **Code blocks get real syntax colouring without a new theme role.**
  THEME.md gives code exactly three roles (`code.border`, `code.text`,
  `inline.code`) — no keyword/string/comment role exists, and adding one is
  a THEME.md change (a column on all four palettes) outside this episode's
  scope. `codeblock.go`'s `codeTokenRole` instead reuses existing
  general-purpose roles for chroma's token categories: keywords →
  `accent.primary`, strings → `done` (green across all four palettes),
  numbers → `accent.second`, function/builtin names → `inline.code`,
  comments → `text.dim`, everything else → `code.text`. A judgment call,
  not a spec'd mapping — noted here so a later episode does not mistake it
  for a role definition.
- **`ui.scrollback`** (`config.go`, default 500) — new key, F1's precedent
  for "chosen here, nothing in `design\` had named it." See "Where this
  diverged from F3.md's spec" for what it actually bounds.

---

## Where this diverged from F3.md's spec

Three places, each raised with Aryan before being built the way it was
built (see the session's own record for the exact exchange):

**1. Glamour, replaced with a direct goldmark AST walk.** F3.md says
"Glamour (or equivalent)". Glamour's own fenced-code rendering draws its
own margin/background chrome, not a title-less rounded `code.border` box
matching every other box in Point — reconciling the two looked like more
work than walking goldmark's parser output by hand. `markdown.go`'s header
comment carries the full reasoning.

**2. No `session.messages(before, limit)` pagination exists on the
backend — `GetSession` fetches every message in one shot.** Confirmed
before writing any code: no such `IncomingEventType`, no `before`/`limit`
anywhere in `session_manager.py` or `session_repository.py`. Per Aryan's
decision, `ui.scrollback` was kept as a config key but **does not cap what
is rendered** — the windowed-scroll design above (§ "The message → block
pipeline") measures at most `rows` messages per frame regardless of
session size, so an artificial hard cap turned out to be unnecessary for
either correctness or the "10k session opens instantly" requirement.
`scrollback` instead bounds the **render cache's** size (`cache.go`'s
`evictIfNeeded`), which is the literal reading of "cap rendered history"
that survived contact with a virtualized renderer. The real fix —
`session.messages(before, limit)` — is `B18-message-pagination.md`
(not written), pointed to from `design/tasks/backend/INDEX.md`'s parking
lot.

**3. Session selection: auto most-recent-or-create.** F6 (sessions list)
and F12 (workspace-state persistence) do not exist yet, so there was no
session to open. On `protocol.ConnectedMsg`, `Workspace.beginBootstrap`
calls `ListSessions`; if any exist it opens the most recently updated one
(`SessionRepository.fetch_all` already orders that way), else it calls
`CreateSession` with an empty payload (`title` defaults server-side to
`"New Session"`). No switcher UI. **F6/F12 replace this outright** —
`protocol.go`'s header comment says so explicitly.

**Also new, not a divergence but not named in F3.md either:**
`backend\cli.py`'s `seed-session` command (F3.md invited this: "a fixture
generator is worth the ten lines"). Generates N alternating user/assistant
messages directly through `SessionManager.append_message` — no provider
call, no task, no envelope round trip.

---

## Manual Test Log (F3)

**Automated (exact sizes, `go test`):** `TestEveryLineIsExactlyTheRectWidth`
sweeps the Chat workspace's `View()` output — mixed roles, a code block, the
CJK/emoji fixture together — at 200×50, 120×30, 100×26, 90×24, 89×23, 70×20,
50×12, asserting every drawn line is exactly `l.Main.W` cells. Markdown
fixtures (`TestVerificationFixtureNeverExceedsWidth`,
`TestEveryCodeBoxLineIsExactlyTheGivenWidth`) swept at 20/40/41/60/76/100/120.

**Interactive (Windows Terminal, live sessions against scratch profiles —
`seed-session --messages 10000`, real backend, real handshake):**

1. 10,000-message session: instant open, pinned to the bottom by default.
2. `k`/`up` scrolls and unpins.
3. `G` (**Shift+G**, confirmed — a lowercase `g` press was the source of an
   earlier false "doesn't work" report, corrected by Aryan) re-pins to the
   bottom.
4. Memory, watched live via `POINT_MEMLOG` (see Bugs, below) across a full
   2-minute organic scroll session (mixed `j`/`k`/held-`G`, not a brief
   sample): `heap_alloc` oscillated 1.245M–1.262M with no trend. Flat,
   confirmed — the earlier Task Manager reading that looked like growth was
   Windows Terminal's own overhead, not Point's heap.
5. A message containing a heading, **bold**, `inline code`, CJK
   (日本語テキスト), emoji (🎉), and a fenced block in an unrecognised
   language — sent for real through the (now newline-fixed, see Bugs)
   `echo` provider — rendered correctly: heading/bold/inline-code styled,
   the CJK/emoji line's border landed on the same column as every other
   line, and the unknown-language fence rendered as plain text inside one
   proper rounded `code.border` box (confirmed directly — an earlier report
   of it splitting or not being detected was a misread screenshot, formally
   withdrawn).
6. A second, real multi-line reply — heading, prose, a **separate-lines**
   fenced Python block, more prose — confirmed as ONE code box, not split
   across several and not flattened onto one line. This is the case Bug 1
   below made impossible before the fix.
7. Focused a message with `k`, pressed `y`, pasted into Notepad: clean raw
   markdown source, no `│` or `▎` decoration characters.
8. Fresh scratch profile (zero sessions): centred `type a message` /
   `^P palette` hint, nothing else.

---

## Bugs caught during manual testing

### 1. Every query logged as `"unroutable event"`

Caught by me, in this episode's own first smoke test, before Aryan's
retest round. `Workspace.doSend` originally called `Client.Send`, which
registers no correlation waiter — so `Client.deliver`'s `routeToWaiter`
never matched, and every `ListSessions`/`GetSession`/`CreateSession` reply
logged as unroutable: `client.go`'s own signal for *"this is a contract
bug you want to see,"* firing on completely normal operation.

**Fix:** `doSend` calls `Client.Request` instead, discarding the returned
channel — `client.go`'s own doc says routing does not consume, so the
reply still reaches `Update` via the ordinary `EnvelopeMsg` path regardless.
`handleEnvelope` calls `Client.Release` once it has consumed a reply, so
the unread waiter does not linger in the client's pending map forever.

### 2. The echo provider flattened every reply's newlines

Reported by Aryan, confirmed directly against the database: a message sent
with real `\n` characters came back from the `echo` provider with every
newline replaced by a single space — corruption present in the **stored**
row, before `cli.py`'s printing or anything in this package's rendering
ever touched it.

Root cause: `AgentExecutor._echo_body` (`backend\agents\executor.py`)
chunked with `text.split()`, which discards every whitespace run it splits
on — a blank line, a fenced block's line breaks and a single space were all
indistinguishable by the time a chunk left the function. Traced to its true
origin — `_demo_echo_body`, `backend\runtime\runtime.py`, **Episode 1** —
predating F3 (and B9's later refactor that moved and renamed it) entirely;
invisible until now because nothing before F3 rendered a multi-line echo in
a way where the flattening was obvious.

**Fix:** `re.finditer(r"\S+|\s+", text)`, yielding each whitespace run
verbatim alongside each word, so concatenating every chunk reconstructs the
original text exactly. Correction note is in
`design/completed/backend/B01-runtime.md` (not here), per the instruction
to attribute a pre-existing defect to the episode that actually built it.

### 3. `G` did not re-pin — a held key's auto-repeat looked like a paste

Reported by Aryan against the 10,000-message session (later narrowed to a
real, separate confirmation: `G` — Shift+G — does work; the *original*
report conflated it with a lowercase `g` mistake, see Manual Test Log #3).
The real defect this surfaced: **`Root.Update`'s `PasteMsg` case never
forwarded to the active workspace at all**, and even once it did,
`chat.Workspace.Update` had no case for it.

Root cause: `app\paste.go`'s burst detector (built for F4's eventual text
input) treats any two plain keystrokes arriving under `pasteBurstGap` (6ms)
apart as one paste. A **held** key's OS auto-repeat can be batched by
Windows Console the same way a real paste is — paste.go's own comment says
a paste "is delivered ... as a single batch of records," and a held key's
repeats are drained identically. A held `G` can arrive as
`app.PasteMsg{Text: "GGGG..."}` instead of individual `KeyMsg`s.

**Fix, two parts:**
- `root.go`: `PasteMsg` now also forwards to `r.current().Update(m)`, the
  same pattern already used for `ConnectedMsg`/`EnvelopeMsg`.
- `chat\scroll.go`: `handlePastedBurst` recovers a burst that is **entirely
  one repeated character** by replaying it as that many keystrokes through
  `handleKeyString` (the switch `handleKey` used to own directly, factored
  out for this). A **mixed** burst — genuine pasted prose — is left alone:
  F3 has no text input for a real paste to go into yet, and replaying
  arbitrary characters would let a stray `j`/`k`/`y`/`G` inside it move
  focus or fire a clipboard copy, which is worse than doing nothing.

### 4. Not a bug: the memory investigation

Aryan's report of a slow Task-Manager climb during `G` testing turned out
to have two separate, unrelated explanations, neither of which was a leak
in this package:

- While `G` was silently swallowed (bug 3), repeated failed attempts cycled
  through `paste.go`'s hold-then-release machinery repeatedly without ever
  reaching the actual (already leak-free) re-pin code — plausible source
  of *some* of what was seen, though not confirmed as the specific cause.
- The remainder, confirmed directly: **Task Manager reports the whole
  process, or was being read at a scale that conflated Windows Terminal's
  own scrollback/rendering overhead with Point's process.** Settled, not
  guessed at — see the diagnostics built specifically to answer this,
  below.

`TestRepeatedGReRepinsAndDoesNotLeak` first measured this in isolation
(direct `Update` calls, forced GC before/after): 40 re-pins against a
10,000-message session, heap delta a few KB. That result held up against
Aryan's live, organic 2-minute session too (Manual Test Log #4), read
through the same measurement — see next section.

---

## Diagnostics added — `frontend\cmd\point\diagnostics.go`

Built specifically to settle the memory question with a direct measurement
instead of continued inference from Task Manager. Both are no-ops unless
their environment variable is set; a normal `go run` is unaffected.

- **`POINT_MEMLOG=<seconds>`** (or `=1` for the 5s default) — logs
  `runtime.MemStats` to `frontend.log` on a timer, **forcing a GC before
  every reading** — the exact same measurement
  `TestRepeatedGReRepinsAndDoesNotLeak` uses, so a live organic session and
  that test's result are directly comparable instead of argued from two
  different numbers.
- **`POINT_PPROF=1`** — serves `net/http/pprof` on `127.0.0.1:6061` only
  (never `0.0.0.0`) — `go tool pprof http://127.0.0.1:6061/debug/pprof/heap`
  points at an exact allocation site if a future memlog reading ever does
  show real growth.

Both are general-purpose and outlive this episode — reusable by any later
episode (F5's streaming loop, F6's sessions list) that needs the same kind
of "is this actually growing" answer.

---

## What the next episode inherits

- **The message → block pipeline** (above) is the contract. `renderMessage`
  is the AI-bar-vs-user-box decision point; `renderCached`/`w.cache` is
  keyed on `msg.ID` alone at one shared width; the focused message bypasses
  the cache by direct re-render, which is the pattern F5's streaming
  message should follow (never entering the cache while in flight); gaps
  between messages are computed at windowing time from role pairs, not
  stored per message.
- **`renderTaskSlot`** exists, unwired — F9 has to decide how a slot takes
  a place in `w.all`'s sequence (or a parallel structure the windowing code
  also has to know about) while preserving the pipeline's three invariants:
  stable id, a role `gapLines` can switch on, height = `len(block)`.
- **`Root.SetWorkspace`** is now the established way a workspace built
  outside `app` gets installed — any future workspace package (F9's
  editor) that returns `app.Workspace` from its own `Update` will hit the
  same import-direction problem and need the same wiring in `main.go`.
- **`Root.Update` forwards `ConnectedMsg`, `EnvelopeMsg`, and `PasteMsg`**
  to the active workspace now — it forwarded none of these before F3.
  Anything else a future workspace needs forwarded (task lifecycle events,
  for F5) is the same one-line pattern in each `case`.
- **`ui.scrollback`** bounds the render cache, not the transcript — nothing
  is ever unreachable by scrolling. Real pagination
  (`session.messages(before, limit)`) is still `B18`, unwritten, pointed to
  from `design/tasks/backend/INDEX.md`.
- **`seed-session`** (`cli.py`) and the diagnostics (`POINT_MEMLOG`,
  `POINT_PPROF`) are reusable dev tooling, not one-off test scaffolding.
- **The echo provider's newline handling is fixed** — any later episode
  that seeds or manually tests through `echo` now gets faithful multi-line
  content, not flattened text. The correction lives in
  `design/completed/backend/B01-runtime.md`.
- **Deliberately unfinished, unchanged from F3.md's own "Do NOT":** the
  query input (F4), streaming (F5), the right pane (F6) — `Cards()` returns
  `nil` on purpose — and workspace-state persistence (F12), for which
  `Suspend`/`Resume` are still no-ops.
- **Still owed, not done here:** lifting `box.go`/`textutil.go`'s
  primitives (now duplicated in both `app` and `chat`) into a shared `ui`
  package, as F1's handoff first flagged.
