# F9b — Diff Card (F9, SPLIT) 🟡

**The diff card (UI-07) ships: Go computes a real line diff from B24's
validated proposal, `e` opens the proposed RESULT in a scratch editing buffer
with the diff re-rendering live against the original as you type, and `⏎`
applies it into an editor buffer as ONE undoable splice. Done-when #5 and #6
are met. Done-when #7 — refuse to apply when the file changed on disk since
the region was generated — is NOT met, and is absent rather than
approximated: the `(mtime, size)` pair it needs is captured nowhere in the
backend at region-generation time. F9's packaging requirement ("same card,
same keys, in both workspaces") is also not met and is left open as a
decision, not settled here.**

> 🟡 **SPLIT, not complete.** F9 closes only when **F9b's deferred half**
> (#7, and the packaging decision) and **F9c** (Session 23b — attach and
> save) land. Of F9's eight Done-when criteria, F9a closed #1 and #8, this
> session closes #5 and #6, and four remain: #2, #3, #4 (F9c) and #7 (this
> episode's own deferral, now tracked as **B36**).
>
> **Verified by the Go test suite, NOT by an interactive app walkthrough.**
> See **Nothing constructs a DiffCard yet** below — this is the single most
> important fact about this episode and the one most likely to be misread
> later. The three "You do:" steps in WORKFLOW.md Session 18d were NOT
> performed, because they cannot be: there is no way to make a diff card
> appear in the running app.

---

## Done when — two closed, one deferred, by number

From F09's original eight:

| # | Criterion | State |
|---|---|---|
| #5 | Diff apply lands the **edited** text, not the original | ✅ closed, structurally — see **One source of truth** |
| #6 | Applying in the Editor is undone by a **single** `ctrl+z` | ✅ closed, structurally — `editOther` never coalesces |
| #7 | Modify the file on disk after generation, then apply → refused with a reason | ❌ **NOT built. Absent, not approximated.** See below |
| #1, #8 | tray, discard | ✅ closed by F9a (Session 15) |
| #2, #3, #4 | attach, save-to-memory, tray labels | ❌ deferred to F9c (Session 23b) / B13 |

### #7 is absent, not approximated — and exactly why

The episode was authorised to build #5 and #6 only, after a prerequisite
check found that #7 cannot be built honestly against this repo. Three facts,
each verified in the code rather than inferred from a doc:

1. **Nothing captures `(mtime, size)` at region-generation time, anywhere in
   the backend.** `_render_editor_context` (`backend\managers\context.py`)
   reads the file with `path.read_text(...)` and **never stats it**.
   `RegisteredEditorContext` (`backend\streaming\manager.py`) carries
   `context`, `shown_start`, `shown_end` and nothing else.
   `FillProposal.to_dict()` (`backend\managers\fill_proposal.py`) is seven
   fields — `file`, `start_line`, `end_line`, `replacement`, `note`,
   `shown_start`, `shown_end` — with no stat among them, and its own docstring
   says all seven travel together, no subset. So the only
   moment-of-generation fact carried forward is the shown region.
2. **`CheckFileStale` cannot express the comparison.**
   `_handle_check_file_stale` (`backend\runtime\runtime.py:860`) requires
   `path` and `mtime` only — **no `size`**, **no `session_id`** — and
   `ProjectManager.check_stale` (`backend\managers\project_manager.py:590`)
   compares `st_mtime` against the supplied value with a 1e-6 tolerance.
   Size is not a parameter and is never compared.
3. **The pair that IS available is the wrong moment.** `doc.mtime` /
   `doc.size` (`frontend\internal\editor\doc.go`) are from the read that
   opened the file, replaced from every successful write's reply — autosave
   rule 2. That is the open/last-save moment, not the generation moment.

Substituting (3) for (1) is precisely the "cache a stat and trust it" error
B12's `set_mode` *clears* its recorded pair to avoid, and F8's chip staleness
repeated one layer up. It fails in **both** directions, which is why it is
not a conservative approximation:

- a **false refusal** when the region was generated from content newer than
  the open (the backend re-reads from disk at generation, so an external edit
  between open and generation yields a region built from content `doc.mtime`
  does not describe); and
- a **missed refusal** when a successful save refreshed the pair *after*
  generation.

`DiffCardOptions.RecordedMtime` / `RecordedSize` exist, are carried onto the
card, and stay **zero** this session. They are not read by anything.
`TestTheStalenessPairIsCarriedAndUnpopulated` is the tripwire against someone
"finishing" #7 by wiring `doc.mtime` into them, and it also asserts that the
apply is **not gated** on the unpopulated pair — #7 is absent, not half-built.

**Tracked as B36** (`design/tasks/backend/INDEX.md`): `FillProposal` must
carry `(mtime, size)` captured at the region-generation disk read, and
`CheckFileStale` must accept `session_id` and `size` as real comparison
fields. B24's own handoff already said this belongs there — "a small addition
here or in F9's own backend dependency, **not something F9 invents locally**".

---

## Nothing constructs a DiffCard yet

**`NewDiffCard` has no production caller anywhere in the Go tree.** Verified
twice — at build time and again at record time:

```
$ grep -rn "NewDiffCard" --include=*.go .
frontend\internal\editor\diffcard.go:237   // DiffCardOptions is NewDiffCard's input.
frontend\internal\editor\diffcard.go:262   // NewDiffCard builds the card from ...
frontend\internal\editor\diffcard.go:268   func NewDiffCard(o DiffCardOptions) *DiffCard {
frontend\internal\editor\diffcard_test.go:46    return ws, NewDiffCard(DiffCardOptions{
frontend\internal\editor\diffcard_test.go:352   c := NewDiffCard(DiffCardOptions{
```

Two call sites, both in the test file. The only other `DiffCard` mentions
outside `diffcard.go`/`diffcard_test.go` are two comments (`editor.go:291`,
`stale.go:307`).

This is **deliberate and expected**, not an oversight:

- `FillProposal` does not exist on the Go side of the wire. There is no
  `EvFillProposal` in `frontend\internal\protocol\types.go`, and nothing in
  the Go tree ever sends an `editor_context`.
- `tasks\frontend\INDEX.md` puts **F15 downstream of F9b**, not upstream:
  `| F15 | Filler-Explainer Integration | P1 | no | F13a, F9b | B11, B24 |`.
  F9a's handoff says it in words — "Reads B24's payload and calls F9b's diff
  function rather than re-implementing it. Its dependency is F9b, not 'F9'".
- It is the same position `doc.ApplyRegion` shipped in at 18a, with its own
  comment saying so: "Nothing in this session calls it -- there is no apply
  surface in the repo yet, exactly as F09a recorded -- and it is built now
  rather than later because the undo model either supports it by construction
  or has to be rebuilt to."

### What this means for verification, stated plainly

**No manual app test of the diff card was performed, because none is
possible.** WORKFLOW.md Session 18d's "You do:" lists three interactive steps
(`e` → edit → `⏎` → `ctrl+z`; modify on disk then `⏎`; force a failed apply
and watch the card). Step 2 is #7 and is not built. Steps 1 and 3 are built
and **could not be exercised through the UI** — pressing keys in `point` will
never produce a diff card in this build, because nothing constructs one.

The Go test suite is therefore this episode's **actual** verification
surface, and the three behaviours those steps were meant to confirm are
asserted there instead:

| WORKFLOW "You do:" step | Replaced by |
|---|---|
| 1. `e`, edit a line, `⏎`, confirm the edited text landed | `TestApplyLandsTheEditedTextNotTheProposal` |
| 1. `ctrl+z` once, confirm it fully undid | `TestApplyingIsUndoneBySingleCtrlZ`, `TestAnEditedApplyIsAlsoOneUndoStep` |
| 3. force a failed apply, watch the CARD not the log | `TestARefusedApplyLeavesTheCardUnapplied`, `TestTheCardDoesNotTouchTheBufferFromItsOwnUpdate` |
| 2. modify on disk, confirm refusal | **not replaced — that is #7, deferred to B36** |

An interactive walkthrough becomes possible at **Session 19 (F15)**, which
adds the producer. F15 should re-run WORKFLOW.md 18d's steps 1 and 3 by hand
the first time a card can actually be opened — the same obligation
`F12-workspace-manager.md` handed F13 for its three test-proven Done-whens.

**Gate result:** `gofmt -l .\frontend\` clean, `go vet .\frontend\...` clean,
26 new tests (9 in `internal\diff`, 17 in `internal\editor`), full frontend
suite green with `-count=1` under a scratch `APPDATA`/`LOCALAPPDATA` profile
(`internal\diff` 9/9, `internal\editor` 305/305, every other package `ok`).

---

## Where the code went

Four files. Three new, one five-line edit. **Zero backend changes.**

### `frontend\internal\diff\` — new package (diff.go, diff_test.go)

The real line diff, and the ninth internal package. Deliberately depends on
**nothing** — not `editor`, `app`, `chat`, Bubble Tea or `theme` — only
`strings`.

It is a separate package rather than a function in `editor` for one reason:
the diff is a pure function of two line slices, while *which* buffer is "the
original" and *where the card lives* are the packaging questions this episode
does not answer (below). A dependency-free package means resolving them later
costs nothing here, and F15 can call it without importing a workspace.

`editor\diff.go`'s `unifiedDiff` (B10's coarse prefix/suffix hunk for the
stale-save evidence view) **named this episode in its own header**: "F9b owns
the real diff (its card computes one from B24's validated proposal and F15
reuses it), and when that function exists this should call it instead of
keeping a second implementation." The function now exists. **The swap was NOT
made** — see **Deliberate limitations**.

### `frontend\internal\editor\diffcard.go` — `DiffCard` + the apply handler

The card (an `app.MaximisedOverlay`) and `Workspace.onDiffApply`. The name
`DiffCard` was reserved for this episode by `stale.go:307`, which declined it
for B10's read-only `diffView`: "F9b owns `DiffCard`, which is an interactive
review surface with an apply button and an edit mode. The two will coexist,
and the one that got here first must not take the name."

### `frontend\internal\editor\diffcard_test.go` — 17 tests

### `frontend\internal\editor\editor.go` — one `Update` case

```go
case diffApplyMsg:
    // F9b: the DiffCard asks, this workspace applies. The card never
    // touches a document itself -- see diffcard.go's onDiffApply.
    return w, w.onDiffApply(m)
```

---

## How the diff is computed — the signature F15 calls

This section exists because WORKFLOW.md 18d's Record step asks for it
explicitly: F15 reuses this mechanism and needs a real function to call, not
a description to re-implement.

```go
package diff // github.com/lazyworkoholic/point/frontend/internal/diff

func Region(original []string, startLine, endLine int,
            replacement string, context int) Result

const DefaultContext = 3   // diff -u's default

type Result struct {
    Rows      []Row
    Added     int    // Insert rows
    Removed   int    // Delete rows
    Coarse    bool   // LCS skipped past maxLCSLines; one delete-then-insert hunk
    Truncated bool   // hit maxRows (2000)
}

type Row struct {
    Op   Op      // Equal | Delete | Insert
    Old  int     // 1-based ABSOLUTE line in the original file; 0 on an Insert
    New  int     // 1-based ABSOLUTE line in the proposed result; 0 on a Delete
    Text string
}
```

**What it takes as input, and what it must never take.** `startLine`,
`endLine` and `replacement` come straight from B24's **validated**
`FillProposal` — never from raw model text, and never from a unified diff
asked of a model (F09's own "Do NOT"). B24 has already rejected inverted
ranges, ranges outside the shown region, and file mismatches before an event
is emitted, so this function does not re-validate them; it **clamps** the
range to `original` instead, because `original` is whatever the caller
considers authoritative — in this card's case an editor buffer the user may
have been typing in, which can be shorter than the file the context builder
read. Clamping stops an out-of-range index; noticing the range *is* stale is
a different job and not this function's.

**Algorithm.** Common prefix and suffix are trimmed, then LCS over the
differing middle, then `context` unchanged lines from `original` are added
either side. Trimming first is what keeps the LCS table small for the
overwhelmingly common proposal (one edited line in an otherwise untouched
function) and is also what makes `maxLCSLines` (600) a bound on the
**differing middle** rather than on the region. Past that bound the result
degrades to one delete-then-insert hunk and says so in `Coarse` — surfaced
rather than silent, because a card reporting "+340 −338" for a three-line
change is telling the truth about what it computed and the reader is owed the
reason. The bound exists **for control**, not as a measured performance
claim: the table is `len(old) × len(new)` cells and an unbounded one is an
unbounded allocation driven by a number that arrived from a model's reply.

**Line numbering.** Both numbers on every row are absolute in their own
version of the file, so a gutter prints what the editor would show without
re-deriving anything from a region offset. The trailing context carries the
region's net line delta, which is the arithmetic the two fields exist to keep
straight (`TestTrailingContextRenumbersAfterADeletion`).

**One trailing newline is stripped**, matching `editor\buffer.go`'s
`newLineBuffer` exactly. That is not cosmetic: the card seeds a scratch
buffer with the same string and diffs the buffer's own text, so a mismatch
would show a phantom trailing line the apply would never produce
(`TestASingleTrailingNewlineIsStripped`).

---

## How the apply works, and who owns the mutation

```go
func NewDiffCard(o DiffCardOptions) *DiffCard   // editor
func (c *DiffCard) ResultText() string          // what an apply would land
func (c *DiffCard) Applied() bool
func (w *Workspace) onDiffApply(m diffApplyMsg) tea.Cmd
```

**The card never touches a document.** `⏎` returns a `tea.Cmd` yielding
`diffApplyMsg{card}` and `Workspace.onDiffApply` does the work, for the
reason `stale.go` already recorded for the three-button dialog: "an Overlay's
Update runs inside Root's Update and any callback it fired would be mutating
a workspace from code that does not own it. The editor handles these in its
own Update, where it does."

The message carries the card **pointer**, unlike `staleReloadMsg`'s path,
because the handler has to mark that exact card applied after the splice and
the card is owned by Root's overlay slot. The precedent is `reload.go`
filling in `staleDialog.disk` after a re-read: the workspace built the
overlay and may complete it.

### The order, which is the whole point

Every guard runs and every refusal returns **before** the splice:

1. card is nil, or already applied → no-op
2. target document is no longer open → refuse
3. target is read-only → refuse, naming `roReason`
4. target buffer's `Version()` ≠ the card's `baseVersion` → refuse
5. the diff is empty (`Added == 0 && Removed == 0`) → refuse, "the proposal
   matches the file as it is"

then, and only then:

6. take an `editProbe` on the target
7. `d.ApplyRegion(from, to, c.ResultText())`
8. **if `d.buf.Version()` did not move → refuse**, "the buffer did not
   change"
9. activate the target tab and focus the buffer
10. `c.markApplied()`
11. arm autosave via `w.autosaveAfter(probe)`

Step 8 is not belt-and-braces. `ApplyRegion` returns **nothing** and no-ops
silently on a read-only document, so the buffer's version is the only
evidence available that it did anything. Marking the card before it would be
F9a's hazard 3 — F8's chip bug exactly, "a card that looks applied when it
was not". Since every apply goes into an in-memory buffer and never to disk,
a failed apply is atomic by construction, but only with the ordering this way
round (`TestTheCardDoesNotTouchTheBufferFromItsOwnUpdate`,
`TestARefusedApplyLeavesTheCardUnapplied`).

### The region → positions mapping

```go
func (c *DiffCard) regionRange(b Buffer) (Pos, Pos)
// from = Pos{Line: startLine-1, Col: 0}
// to   = END of line endLine-1  (clamped to the buffer)
```

`to` is the end of the last line rather than the start of the next one, and
the replacement text carries no trailing newline (`Buffer.Text` never does).
That pairing leaves the newline after the region intact and removes the need
for a special case on the file's last line, where there is no next line to
point at.

### Why the apply activates the tab and moves the keyboard

Done-when #6 is "applying in the Editor is undone by a **single** `ctrl+z`",
and that key reaches `w.docs[w.active]` and only when the buffer pane has the
keyboard. An apply into a document that is open but not active, or with the
tree focused, would put the change in one buffer and the undo in another —
#6 would be false through the UI while passing a doc-level test.

This is a deliberate divergence from `onFile`'s "opening does not move the
keyboard": opening a file is the user asking to see something, applying a
change is the user having just changed it. Done **after** the guards, so a
refused apply leaves the user exactly where they were
(`TestApplyActivatesTheDocumentItEdited`, `TestASuccessfulApplyFocusesTheBuffer`,
`TestARefusedApplyDoesNotMoveTheUser`).

---

## What state the card tracks, what is read fresh, what a failed apply shows

Also asked for explicitly by WORKFLOW.md 18d's Record step — "that is the
part most likely to rot".

### Tracked from construction, never re-read

| Field | What it is |
|---|---|
| `path`, `name`, `note` | the target file, and the Filler's own `note` |
| `startLine`, `endLine` | B24's **validated** range. Never recomputed — B24 checked it against the region the model was actually shown, and re-deriving it here would validate against a different moment than the model saw |
| `original []string` | a **COPY** of the target buffer's lines at construction |
| `baseVersion uint64` | the target buffer's `Version()` at that same moment |
| `RecordedMtime`, `RecordedSize` | #7's inputs. **Zero. Read by nothing.** |

`original` is copied, not retained by reference: the caller's slice is a
buffer's line list in every real case, and keeping it would make the card's
"original" change as the user types in the file behind it. The diff on screen
has to be a statement about one moment.

### Read fresh at apply time

Only three things, all from the live target document — resolved by `path`, not
held as a pointer:

- whether it is **still open** (`docIndexByPath`)
- `d.readOnly` / `d.roReason`
- `d.buf.Version()`, compared against `baseVersion`, and again after the
  splice

**Nothing stats the file. Nothing calls `CheckFileStale`. Nothing resolves a
project.** That is #7 and the guard order, both deferred.

### Mutable card state

`mode` (review/edit), `applied`, `refusal`, `scroll`, and the scratch buffer
itself — plus a diff memoised per `scratch.buf.Version()`, because `View`
runs every frame and recomputing an LCS to render the same rows is work with
no reader.

### What a failed apply shows

The card **stays open and stays unapplied**. `refuse(why)` sets `refusal` and
clears `applied`; the status line renders it in the `Error` role as
`not applied — <why>`, the title stays "Proposed change · <name>" (it only
becomes "Applied · <name>" on success), and a status line is emitted through
`w.say` as well. A refusal the user cannot read is a card that looks inert,
which is the failure mode WORKFLOW.md 18d step 3 was written to catch.

### The `baseVersion` guard is NOT #7

It is a fact about the **in-memory buffer being spliced**, not about the file
on disk, and it is checkable here precisely because it needs no stat and no
round trip. It matters for F15, where a proposal arrives from a background
task and the buffer genuinely can move between arrival and apply. It is
recorded separately from #7 in the code and in
`TestApplyIsRefusedWhenTheBufferMovedSinceTheProposal`'s own comment so the
two are never conflated.

---

## One source of truth — why #5 and #6 hold structurally

**#5 (the edited text lands, not the original).** The proposal's
`replacement` is seeded into the scratch buffer once, at construction, and is
**never kept separately**. The scratch buffer is the only input to both
`result()` (the diff) and `ResultText()` (the apply). There is no code path
that could apply the original instead of the edit, because the original is
not retained anywhere to apply. The diff on screen is therefore always a
statement about what an apply would land
(`TestTheDiffRerendersAgainstTheOriginalAsYouEdit`).

**#6 (one `ctrl+z`).** `ApplyRegion` is one `Buffer.Splice`, recorded as one
`editOther` history record, and `editOther` "never coalesces with anything,
including itself" (`undo.go`). F13a built it for exactly this and tested the
property at the doc level; this episode tests it through the workspace's own
key handling, which is where "a single `ctrl+z`" is actually a claim
(`TestApplyingIsUndoneBySingleCtrlZ`). An **edited** apply is also one step,
because the typing happened in the scratch buffer's own history and the
target sees a single splice however much editing produced it
(`TestAnEditedApplyIsAlsoOneUndoStep`).

---

## The scratch buffer

`e` opens the proposed **result**, not the patch — F09: "Editing a new
version is typing; editing a patch is punishment."

It is a real `doc`, built by `newScratchDoc(content, langPath)`, so the
region is edited with the same cursor, selection, auto-pair, indent and undo
the editor itself has. `editKey` routes keystrokes onto it with `keys.go`'s
`bufferKey` mapping.

**Its `path` is empty**, which is what makes it safe rather than a new flag
to remember: `canSave()` already tests `d.path != ""`, so every save, lock,
autosave and reload path declines it by the field they all already check. It
can never be written anywhere by any path
(`TestTheScratchBufferCanNeverBeSaved`). CLAUDE.md's first hard rule holds
structurally — no `WriteProjectFile` was added, nothing in this episode
writes to disk, and the one `WriteProjectFile` string in the new code is
inside a comment explaining the deferred guard order.

**`⏎` is a newline in edit mode, not apply.** This is the one place the card
diverges from F09's footer (`⏎ apply · e edit · d discard`), and it is
forced: a text pane in which Return does not insert a line is broken. Apply
moves to review mode, one `esc` away, and the footer says so —
`esc review · ⏎ newline · ctrl+z undo edit · (apply from review)`. `ctrl+z`
in edit mode undoes the **scratch** buffer; undoing an applied diff is
`ctrl+z` in the editor, where the apply landed.

---

## The guard order (F9a hazard 1) is also incomplete — and why nothing was faked

F9a's hazard 1 and B24's handoff both require: "resolve the SESSION's scope
first, guard against that, and commit nothing until the guard passes," and
explicitly forbid `_resolve_optional_project`'s path walk-up (B12's bug #2 —
a file priced at ~718 tokens under one scope and refused under the other).

**It is blocked by the same backend gap, and `_resolve_optional_project` was
NOT used to make anything "work".** Precisely where it stands:

1. **This episode's apply makes no backend call and no disk write.** It
   splices an in-memory buffer. So there is no project resolution *in the
   apply path* to get into the wrong order yet, and nothing falls back to
   anything.
2. **There is no session scope available to resolve even if there were.**
   `frontend\internal\editor\protocol.go:20`, verbatim: "The Editor has no
   session and therefore no project_id -- B30 binds a project to a SESSION,
   and this workspace has none. So it resolves one itself, exactly as
   ProjectManager.resolve does for a bare path." And the proposal's own
   `session_id` is not in hand either, because Go never receives
   `FillProposal`.
3. **The scope a target file *was* resolved under is the path-walked one.**
   `openPath` resolves a file opened from outside the tree's project from its
   own path — which is `_resolve_optional_project`'s behaviour, and the drift
   B12's bug #2 names. It is already live in the editor and predates this
   episode.

**Where it becomes load-bearing:** the moment an applied buffer is **saved**.
F13b's write goes to `WriteProjectFile`, which `_require_project`s the
*editor's* path-resolved `project_id` — so a saved agent-applied change would
be guarded against whatever `.point\` sits above the file rather than against
the session that produced the proposal. That is B12's bug #2 on a destructive
path. **Not reachable from this episode** (there is no producer), and
reachable as soon as F15 adds one.

Folded into **B36** with #7, because both need the same backend addition and
`session_id` is half of it.

> **✅ CLOSED SINCE, by F9d (Session 19b) — `F09d-apply-time-guard-order.md`.**
> The section above is kept verbatim because it is the record of a guard
> left ABSENT rather than approximated, and of why each cheap substitute
> was refused. What has changed: B36 gave `CheckFileStale` `session_id`
> (the primitive), F15 gave Go the `FillProposal` decoder and the
> Editor its own session (so points 2 and 3 above stopped being true),
> and **F9d fixed the call site** — `_handle_write_project_file` now
> accepts `session_id` and resolves that session's own scope via
> `_attachment_scope`, ignoring the payload's `project_id` outright and
> refusing a session with no project rather than falling back to the
> path walk-up. `doc.agentSession` carries the proposing session from a
> proven apply to the save. **F9's packaging decision below is
> untouched by that episode and remains open.**

---

## The packaging collision — recorded, left OPEN

F09 says **"Same card, same keys, in both workspaces"** (Chat and Editor).
**That is not buildable as things stand.** The blocker, named specifically
rather than gestured at — the same treatment F9a gave its own markdown
problem:

- **`doc.ApplyRegion` is a method on `doc`, which is UNEXPORTED**
  (`doc.go:82`: `type doc struct`), and **`Workspace` exposes no apply
  wrapper** — verified, there is no exported `Apply*` method on `*Workspace`.
  So the apply entry point F13a built for this card is reachable only from
  inside `package editor`.
- **`app` imports neither `chat` nor `editor`; both import `app`.** Verified:
  `grep -rn "internal/editor" frontend\internal\app\` returns nothing. So a
  card placed in `app` — beside F9a's `ResultCard`, next to the tray that
  opens it — **cannot reach `ApplyRegion` at all.**
- **A card in `editor`, which is this one, cannot be opened by `chat`**
  without `chat` importing the Editor workspace package.

This is F9a's own problem one package over. Its words, about the result
card's markdown: "`chat` imports `app` — so this package cannot reach it, and
the only ways to get it here are to move it into a third package or to move
this card into `chat`."

**Left open as a decision, not settled under this episode's scope.** The two
options already identified:

1. **An exported apply seam on `Workspace`** — e.g. an exported method or a
   narrow interface (the `FocusAware`/`DraftFlusher`/`DiagnosticsMsg` pattern
   this codebase already uses five times) that lets a card outside `editor`
   ask the Editor to apply into a named buffer.
2. **A third package holding the card, with apply passed in as a function** —
   the card becomes workspace-agnostic and each workspace supplies its own
   apply callback.

What this episode did do is **keep the choice cheap**: the diff lives in a
dependency-free package, and the card holds **no `*doc`** for its target —
only a line snapshot and a path. The card is a pure review surface over data,
so whichever way the packaging goes, the computation and the review logic
move with it rather than being rewritten.

---

## Bugs found during the build

Two real defects, both caught by tests written for this episode before any
gate run, and both of a kind this repo has paid for before.

### 1. `Region(nil, ...)` panicked — slice bounds out of range

`clampRange` treats a zero-line file as one line (`Buffer.Lines`' own
convention: "an empty file is one empty line, which is the only
representation in which a cursor has somewhere to be"), returned the range
`1–1`, and then `original[0:1]` indexed a zero-length slice.

Found by `TestAnOutOfRangeRegionIsClampedNotPanicked`, which sweeps
`{0,0}, {1,99}, {99,99}, {5,2}, {-3,-1}` and `nil`. Fixed by normalising
`original` to `[]string{""}` at the top of `Region` — in the function that
slices, not in the one that returns numbers, because clamping a *number* into
range does not make the *line* exist.

### 2. `View` rendered 5 rows at every height below 6

The row budget was `rows - len(head) - 2`, clamped to a minimum of 1 — but
the head (note + status) and the chrome (rule + footer) were emitted
unconditionally, so at `h=3` the card emitted 2 + 1 + 1 + 1 = 5 rows into a
2-row budget.

**This is F13b's stale-dialog defect exactly** — "MinSize returned a FLAT
height of 9 … while View emits nine", whose two reported symptoms were both
that one number, with the button row and the key hint below the fold.

Fixed by spending an explicit budget in priority order: the **footer first**
and dropped last, because it is the keys; then the rule, only if content
survives above it; then the head trimmed **from the front**, so the status
line — which carries "applied" and any refusal — outlives the note. Total is
now exactly `rows` at every height.
`TestTheCardRendersBothModesAtItsDeclaredSize` holds it across
`{3,4,5,6,7,8,19,20,31}` in all four card states, checking both the row count
and that every row is exactly `w-2` cells.

`editPanes` had the same latent shape (it could return `editRows + 1 +
diffRows > body` at small heights) and now guarantees the sum, with a
`body < 3` early return for "no room for a split and a rule between them".

### Not a bug: one wrong test expectation

`TestTheDiffRendersMarkedRowsInTheProposedNumbering` failed first time
because the expected strings were hand-written without the gutter width or
the original lines' own indentation. The renderer was correct — it uses
`render.go`'s own `gutterWidth`, so the diff's numbers line up with the
editor's behind it. Recorded because "the test was wrong, not the code" is a
real outcome and writing it down keeps the next reader from hunting for a fix
that was never needed.

---

## Deliberate limitations

### `unifiedDiff` was not switched over to `diff.Region`

`editor\diff.go` asks for it in its own header. Not done: it would change a
surface F13b shipped and verified, in an episode whose Build list is two
Done-whens. The hook is in place and the swap is now a small, isolated
change. Recorded in the handoff rather than done in passing — CLAUDE.md's
"not a refactor you think would be nice".

### The scratch pane is plain text, not syntax-highlighted

The reason is specific rather than "it was easier": the highlighter **is**
reachable here (`c.scratch.hl` is a real `*highlighter`), but painting a
caret and a selection over its styled runes is `render.go`'s `paintRow` and
`sliceCells`, **both methods on `Workspace`**, both needing `w.theme`, the
selection range and the focus state. Reusing them means either duplicating
that logic for a second surface or lifting it off `Workspace` — a refactor of
F13a's shipped render path. Deferred alongside F9a's own plain-text result
card, which is the same shape of problem and plausibly the same fix (an
extracted render/markdown package, already F16's candidate).

### No horizontal scrolling limitation

`scratchRow` honours `d.left` with cell-accurate skipping, so a tab or a wide
rune cannot be half-skipped. `ensureVisible` is called from `View` and is
reveal-gated and self-clearing, which is the pattern `motion.go` documents.

### The diff gutter on deleted rows

Context and inserted rows show the **proposed** number; deleted rows show the
**original** one, because a deleted line has no number in the result and the
reader's question about it is which line it was. F09's mockup has no
deletions to disambiguate; this is the rule it implies, extended to the case
it does not show.

---

## Handoff

### B36 — the deferred backend half (NEW, stubbed in `tasks/backend/INDEX.md`)

Two additions, both named by B24's own handoff as belonging on the backend
side:

1. **`FillProposal` must carry `(mtime, size)` captured at the
   region-generation disk read** — i.e. stat the file in
   `_render_editor_context` at the moment it is read, carry the pair on
   `RegisteredEditorContext` beside `shown_start`/`shown_end`, and put it in
   `FillProposal.to_dict()`. Note that docstring's rule: all fields travel
   together, no subset.
2. **`CheckFileStale` must accept `session_id` and `size` as real comparison
   fields.** Today it takes `(path, mtime)` and resolves its project through
   `_resolve_optional_project`. `session_id` is what lets the apply guard
   resolve the SESSION's scope first (F9a hazard 1, B12 bug #2); `size` is
   what makes the recorded pair comparable at all.

Then F9b's deferred half can close #7: `DiffCardOptions` already has the two
fields, and a Go `EvFillProposal` decoder (F15's) carries them in.

### Session 19 — F15

- **Constructs the first `DiffCard`.** Use `NewDiffCard`; do not
  re-implement the diff — `diff.Region` is the function, signature above.
- **Add `EvFillProposal`** to `protocol\types.go` and a decoder; there is
  none today.
- **Re-run WORKFLOW.md 18d's "You do:" steps 1 and 3 by hand** the first time
  a card can be opened. They were not performed this session and could not
  be.
- F15's own ghost-text apply should go through `ApplyRegion` too, for the
  same single-undo-step reason.

### Anyone resolving the packaging question

Read **The packaging collision** above. Two options, both still open. The
card holds no `*doc` and the diff has no dependencies, which is what keeps
either one cheap.

### Anyone touching `internal\diff`

It depends on nothing on purpose. Keep it that way — `editor`, `app` and
`chat` can all import it today, which is the property that makes the
packaging decision reversible.

### CLAUDE.md's layout section

Updated in this pass: `internal\diff` makes **nine** internal packages, and
the layout block said "~110 files across 8 internal packages". Factual drift
this episode created; fixed in the same commit as this doc.
