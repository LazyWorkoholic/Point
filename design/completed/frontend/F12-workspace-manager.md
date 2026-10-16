# Episode F12 — Workspace Manager and Lifecycle ✅

**Workspaces are now lazy, unloadable and independent of task execution.
Nothing is in memory at startup: a registry holds factories, the chooser
(UI-12) asks which one to build, and a key absent from `Root.workspaces` is
NOT_LOADED with no second representation of that fact anywhere. Each loaded
workspace carries its own idle timer, and unloading one NEVER touches a
task — the task is in another process, F9a's tray proves it, and the demo
beat (`unloading view — Research task continues in background`) was verified
by hand against a real `@research` run. Quit is the deliberate asymmetry:
it kills the backend, so UI-14 lists what it will cancel and says so in
words. Heap returns flat across 20 load/unload cycles.**

> Verified by Aryan on 2026-10-10 against all six steps of
> `backend\tests\manual_f12_lifecycle.ps1`, in a scratch
> `%APPDATA%`/`%LOCALAPPDATA%` profile per CLAUDE.md's env-var-override
> pattern — the real profile was never named in any call, destructive or
> otherwise.
>
> **Four of this episode's Build-section assumptions did not hold against
> this repo and were resolved with Aryan before any code was written** —
> see "Four assumptions that did not hold" below. One of them (B15) would
> have been unbuildable; two more would have been boot failures.
>
> **Done-when #4b is proven by test, not by eyes-on, and that is the
> correct gate rather than a shortfall.** It requires three loaded
> workspaces; this repo has one, the Editor is F13's (which depends on
> F12), and the only other registry entries would be the explicitly parked
> capability workspaces. Same call F18 made for Suspend/Resume.
>
> **The second gate run found two further issues, both triaged before this
> doc was written**: a reload-position question (diagnosed, pinned by test,
> parked to F16 — not a missing row) and a pprof port that had never been
> right in F12's own task doc (fixed, and Step 6 run successfully twice).
> Both are recorded below so neither has to be re-derived from session
> history.

---

## Where the code went

```
frontend\internal\app\
├── registry.go       NEW — WorkspaceDef/Size, registerWorkspaces (one
│                     derived switch action per workspace, validated with
│                     every other binding), WorkspaceStatus + Label +
│                     WorkspaceStatuses, activeTooSmall
├── lifecycle.go      NEW — the whole state machine: switchWorkspace,
│                     loadWorkspace, unloadWorkspace, anyLoaded,
│                     unloadNotice, runningTasks, idleTimeout,
│                     unloadableLoaded, ensureIdleWalk, walkIdle,
│                     keepWarnedAlive, persistWorkspaceState,
│                     saveWorkspaceStateIfDue, schedulePositionSave,
│                     flushWorkspaceState, restoreSuspended,
│                     DirtyBuffers, requestUnload, unloadAndReport,
│                     requestTasksOffScreen, openQuitConfirm
├── chooser.go        NEW — UI-12 (Chooser) and the leader → w switcher
│                     (newSwitcherOverlay + workspaceSwitcher)
├── lifecycle_test.go NEW — 42 tests
├── root.go           + the lifecycle fields; New loads NOTHING;
│                     current()/storeCurrent() (the F1 invariant edit);
│                     idleTickMsg/suspendSaveMsg/statusExpireMsg/
│                     unloadConfirmedMsg/quitConfirmTimeoutMsg cases;
│                     ensureStartupChooser; expiringStatus;
│                     Options.Config + Options.Workspaces
├── actions.go        + workspace.switcher (leader w), workspace.unload
│                     (leader u); app.quit rebuilt as UI-14 (quitBody);
│                     doQuit also flushes workspace state
├── confirm.go        MinSize is the refusal floor; PreferredSize added so
│                     a Confirm can grow to its own body
├── render.go         statusBar builds `right` first; fitHints/hintGap/
│                     hintFloor; bindingsHint/leaderHint take a width;
│                     View enforces the active workspace's own MinSize
├── placeholder.go    + Diagnostics.NoWorkspaceLoaded and the two rows the
│                     vacant screen draws for it
└── picker.go         the one write-back site outside root.go

frontend\internal\config\
├── config.go         + Config.Workspace (the raw [workspace] table) and
│                     IdleUnload(id) with its narrowest-first resolution
├── state.go          + State.WorkspaceState; SaveWorkspaceState;
│                     saveStateTo's two-owner rule
└── config_test.go    + 8 tests

frontend\internal\chat\
├── workspace_lifecycle_test.go  NEW — 5 tests against the REAL workspace
└── history_test.go              + 5 tests (reload diagnosis ×2, the
                                 unload→complete→reload trio)

frontend\cmd\point\
├── main.go           chat is a FACTORY, not an instance; registers the
│                     WorkspaceDef; zeroes the two Root-owned state.json
│                     fields in chat's copy
└── diagnostics.go    startPprof listens SYNCHRONOUSLY; pprofAddr named

backend\tests\
└── manual_f12_lifecycle.ps1     NEW — the six-step gate
```

---

## The architecture

### The map IS the state machine

```
NOT_LOADED    id absent from Root.workspaces
LOAD          def.New() + Resume(blob) + Init()     on first switch to it
ACTIVE        the id Root.active names AND that is in the map
INACTIVE      in the map, not active, lastActive[id] running
UNLOAD        Suspend() → store bytes → delete key → GC frees the rest
```

There is no enum, deliberately. An enum is a second representation of a
fact the map already holds, and the two eventually disagree. "Is it loaded"
is a map lookup, everywhere, with no cached answer anyone can forget to
update.

### `Root.active` and "is it loaded" are separate facts

`active` is **which workspace the frame is for**, not what is in memory. It
names a workspace at startup (from `state.json`'s `last_workspace`, so
UI-12 pre-selects the right button and the `chat.*` actions' `When`
predicates read the same as before this episode) while the map is empty,
and it keeps naming one after the last workspace unloads. `current()`
returns the vacant diagnostics screen in that state.

Keeping them separate is what makes NOT_LOADED observable at all.

### The F1 invariant edit — `current()` no longer resurrects a key

`current()` used to CREATE a Placeholder and store it under `Root.active`.
That was invisible while a workspace was always loaded and is wrong the
moment one can be unloaded: **every read of the active workspace put the key
back**, so "absent from the map is NOT_LOADED" became untrue one frame after
any unload, and Done-when #1 could never have been observed however the
unload happened.

The vacant screen now lives in its own field, outside the map, and
`storeCurrent` writes back only to a key that is already there — there were
ten unconditional `r.workspaces[r.active] = ws` call sites (nine in
`root.go`, one in `picker.go`) and all ten now go through it. The vacant screen still receives `DiagnosticsMsg`,
because it is the screen a user with a broken backend spends their whole
session on.

Named here as an edit to F1's contract rather than a tweak, at Aryan's
explicit instruction, and pinned by
`TestReadingTheCurrentWorkspaceDoesNotResurrectTheKey`.

### One timer per workspace (D87), and nothing at rest

Each loaded workspace has its own `lastActive`, and one 1-second tick walks
whatever is in the map — so the count is dynamic, with no constant
anywhere. The tick arms only while at least one loaded workspace is both
inactive and configured to unload, and a tick that finds nothing simply does
not re-arm: the same `ensureAnimation` shape, for the same idle-CPU reason.
With one workspace loaded it never runs at all.

A second is not arbitrary. The grace warning fires at a 30-second boundary
and the readout counts down live, so a minute-grained tick would overshoot
the warning entirely at the one-minute timeout F12's own verification sets.

### Unload touches nothing task-shaped

Nothing in `lifecycle.go` sends a cancel, waits for one, or reads a task's
state except to compose the notice. The proof surface is F9a's tray, which
is Root's precisely because the list is global and a task whose originating
view is gone still has to appear and still has to open.

**Nothing about the tray is in `Suspend()`** — not the open/closed state,
not the filter text, and not Root's `backgroundSeen` map, which records
`TaskStarted.background` flags seen in THIS process and is meaningless once
reloaded. Persisting any of it would be persisting a view of state the
database already holds, which is the thing the ~2KB budget exists to avoid.

---

## The Workspace interface, exactly as implemented

F13's Editor and F17's preview both implement this and must match it method
for method. **F12 changed none of these signatures** — F1 chose them and
they were right; what F12 added is the first caller of `Suspend`/`Resume`
and the registry that constructs implementors.

```go
type Workspace interface {
	Init() tea.Cmd
	Update(tea.Msg) (Workspace, tea.Cmd)
	View(layout.Layout) string

	Suspend() ([]byte, error)
	Resume([]byte) error

	ID() WorkspaceID
	Cards() []layout.CardSlot
}
```

`View` takes the whole computed `layout.Layout`, not a `Rect` — F1's own
deliberate departure from `project_idea.md` §8, because a workspace owns
more than one rect and deriving the others is authority `Compute` holds.

The optional interfaces Root type-asserts, all pre-existing except the
last: `ThemeAware`, `FocusAware`, `OverlayProvider`, `ContextExpandable`,
`ContextRefresher`, `DraftFlusher`, `StreamAware`, `QueueAware`,
`PickerTarget`, `MouseOverlayTarget`, and **`DirtyBuffers`** (new, nothing
implements it yet — see Decision 6).

### The registry

```go
type Size struct{ W, H int }

type WorkspaceDef struct {
	ID        WorkspaceID
	Name      string
	LeaderKey string            // optional; chat's is empty — see Decision 3
	New       func() Workspace  // called on FIRST SWITCH, never at startup
	MinSize   Size
}
```

`New` is a func because app cannot import a workspace's package: a real
`Workspace`'s `Update` returns `app.Workspace`, so chat imports app, and app
importing chat back is a cycle. `main.go` imports both and closes over
whatever each constructor needs. **The factory is the whole of "do not
auto-load", expressed structurally rather than as a rule to remember.**

Registration derives one `workspace.switch.<id>` action per entry, before
the single `actions.Validate()` — so a workspace's leader key colliding with
a hand-registered binding is a boot failure naming both offenders, not a key
that quietly does the other thing six weeks later.

---

## Four assumptions in the Build section that did not hold

All four were checked against the repo and resolved with Aryan before any
code was written. Two were boot failures; one was unbuildable.

### 1. `workspace.unload(id)` has no backend — B15 is not started

F12's Build section says "Backend-side: send `workspace.unload(id)` so
Python releases its contexts (B15)". `tasks/backend/INDEX.md` records B15 as
**P2, not started**; there is no `workspace` command in
`frontend\internal\protocol`, and the only `workspace` strings in the
backend are `Session.workspace`, a column.

**Resolved (Aryan's decision 1): build unload frontend-only.** B15 must add
`workspace.unload`; until it does, Python holds the context until the
session is next touched. Everything else in F12 is frontend-only, and the
"never cancel a task" rule is strictly safer without a backend call than
with one.

### 2. There is no second workspace, so three Done-whens could not be run

Done-when #1 ("picking Chat leaves the Editor NOT_LOADED"), #4 ("switch to
the Editor, wait") and #4b ("three workspaces loaded") all name workspaces
that do not exist. F13 depends on F12. What exists is `chat.Workspace` and
F1's `Placeholder` — and `layout.Compute` already branches on
`WorkspaceEditor`.

**Resolved (Aryan's decision 2): register only `chat`.** #1 and #4 are
proven by Go tests that register test-only workspaces through the REAL
registry, on a substitutable clock. No stub "Editor" appears in the chooser,
switcher, palette or help, per CLAUDE.md's rule against building a later
episode's surface. #4b is recorded as **test-proven only**, with the manual
gate impossible until F13 lands — the chooser shows one button today, and
that is the honest rendering of one registered workspace.

### 3. Two of F12's leader keys were already bound, and a duplicate is a boot failure

F12 asks for leader → `c` (switch to Chat) and leader → `x` (unload). `c`
has been `pane.right.toggle` since F6 and `x` has been `context.expand`
since S13d. `actions.Registry.Validate` rejects a duplicate leader sequence,
`app.New` returns that error and `main.go` refuses to start — so this would
not have been a UX wrinkle, it would have been an app that does not boot.

**Resolved (Aryan's decision 3): leave both shipped bindings alone.** New:
leader → `w` opens the switcher (one key listing every workspace with its
idle readout, which also scales to the parked capability workspaces without
needing a free letter each), leader → `u` unloads the current one. Chat's
`WorkspaceDef.LeaderKey` is therefore **empty**; the field is wired and
exercised by test, and what is missing is a free key, not the mechanism.

### 4. UI-14 needs a task list that is not kept fresh

`refreshTasks` is gated on `tasksOnScreen()` — F6's Done-when #6, asserted
directly by chat's own `TestReopeningTheOverlayRefreshesWorkStartedWhileIt
WasClosed`. So `Root.tasks` is stale or empty unless the right pane or the
tray is open, and UI-14's mockup lists running tasks by name with a
duration while its spec says "Loading state: n/a".

**Resolved (Aryan's decision 4): ask, then draw.** `app.quit` sends one
`ListTasks` through `requestTasksOffScreen` (the gate deliberately
bypassed, for a DIALOG rather than a pane) and `openQuitConfirm` draws UI-14
when the snapshot lands, with a 500ms fallback so a `ctrl+q` can never
visibly do nothing. F6's gate is untouched.

A first attempt removed that gate outright. **chat's own test caught it** —
which is the test doing exactly its job, and why the narrower reading was
the right one.

---

## Decisions

### 1. `state.json` has two owners, and the rule is one line

`chat.Workspace` owns `Drafts`, `Provider`, `UI` and `BehaviourOverrides`
and writes them by serialising a WHOLE `State` taken at startup
(`chat\draft.go`'s `persistDraft`). F12 gives `app.Root` `LastWorkspace` and
`WorkspaceState`. Two writers each serialising their own snapshot means the
loser of any pair erases the winner's fields: **type one character and the
scroll anchor is gone; unload and the draft you just typed is gone.**

Rather than a second file, a shared mutable store or a lock: a save that
leaves Root's two fields at their zero value keeps whatever the file already
holds for them. `main.go` zeroes both in chat's copy, so chat's writes
always take that branch; Root's own writes go through
`saveWorkspaceStateTo`, which re-reads first. **This supersedes `chat.go`'s
note that `LastWorkspace` "round-trips" through the workspace's copy** — it
no longer does, and must not, because a round trip IS a stale copy.

Tested in both directions. A related finding: the blob round-trips by
CONTENT, not bytes — `MarshalIndent` re-indents an embedded
`json.RawMessage` — so nothing downstream may compare these bytes, and a
test pins that.

### 2. The blob is opaque to Root, and F18's shape is untouched

`WorkspaceState map[string]json.RawMessage`. Root stores what `Suspend`
returned under the workspace's id and hands it back to `Resume` unread. It
never unmarshals one, which is the only way "the root never reads a
workspace's internal fields" survives being given a persistence mechanism.
A workspace versions its own shape inside its own blob, so `StateVersion`
does not move when one changes.

`chat.suspendBlob`, `blobAnchor`, `suspendVersion`, `currentBlobAnchor` and
`applyResumeAnchor` were **not touched**: one anchor representation, one
home, and both of F18's behaviours (five-case degrade-to-bottom; Resume
positions nothing itself) preserved by not reimplementing them.

**`session_id` was deliberately left out of the blob.** There is exactly one
caller of `getSession` and no session switching anywhere, so Resume already
lands on the same session by the same most-recently-updated rule that opened
it. Adding it would change bootstrap behaviour and need its own
"that session is gone" degradation, for no benefit until session switching
exists. Whoever builds that adds the field.

### 3. The grace warning is an expiring status line, not a toast

F12 asks for a toast. There is no toast in this codebase — only `r.status`,
which is PERMANENT until something overwrites it, and `actions.go`'s
`pane.right.toggle` already records a round of debugging spent on a stale
status string that read as a compositor bug. "Editor unloads in 30s" left on
screen after the Editor unloaded would be the same lie.

So the status line gained an expiry: `statusExpireMsg` carries the TEXT and
clears `r.status` only if it still matches. Compared by text rather than by
a generation counter because `r.status` is assigned directly from a dozen
places and a counter would be a rule every one of them has to remember.

> **Changed by F13 (Session 18a).** Two things in this decision no longer
> hold as written: the warning's wording no longer promises "any key to
> keep", and "assigned directly from a dozen places" is no longer the end
> of the story — the expiry is now DERIVED in `Update` so that every one
> of those assignments gets one, instead of each author remembering.
> The text comparison is what makes that safe, exactly as argued here.
> See **Superseded by F13 (Session 18a)** below.

### 4. `[workspace]` is the first table outside `[ui]` the Go side parses

Held as a RAW `map[string]any`, not a struct, and that is the whole reason
it is a map: the table mixes a scalar default with per-workspace sub-tables,
and a struct would have to name every workspace id as a field — the opposite
of F12's own "no constant anywhere; a capability workspace added later
inherits this for free". `IdleUnload(id)` resolves narrowest-first
(`[workspace.<id>]`, then `[workspace]`, then 5 minutes), clamps negatives
to "never", and ignores a non-number so a typo cannot stop the app booting.

Python never reads this table and Settings never writes `config.toml`
(F10b's same rule), so it is hand-edited or absent. One test writes it with
the exact bytes `Add-Content -Encoding utf8` produces in PowerShell 5.1 —
BOM, CRLF, leading blank line — because that is how the gate writes it, and
a gate that silently configured nothing would measure nothing.

### 5. Surviving a kill needs the blob written in advance

A kill never calls `Suspend`, so `saveWorkspaceStateIfDue` asks the ACTIVE
workspace for a fresh blob before writing — the only path that writes one
for a workspace still loaded. It is re-armed from the places a POSITION can
move (a key the workspace actually receives, a mouse wheel), not from every
key: a chord an action consumed scrolls nothing, and arming on it also
wrapped every such key's command in a `tea.Batch` for no reason, which
`TestCtrlCDoesNotQuit` caught.

Coalesced on a flag as well as F4's generation counter: a burst of scrolling
is hundreds of keys, and while a stale tick is already a harmless no-op by
generation, scheduling one `tea.Tick` per keystroke is hundreds of
goroutines for one write.

`doQuit` flushes synchronously for app.quit's own Bug 17 reason — once
bubbletea has `tea.Quit`, a sleeping Tick's message is never delivered.

### 6. `DirtyBuffers` is declared with no implementors

UI-13 exists to "prevent losing an unsaved editor buffer by reflex", so the
count has to come from the workspace rather than be guessed by Root. Nothing
implements it: the Editor is F13's, and
`F09a-task-tray-cards.md` already established with evidence that there is no
editor buffer anywhere in this repo. So leader → `u` never confirms today,
which is exactly UI-13's own empty state ("if nothing is dirty and no task
runs, unload without asking"), and **a running task is deliberately not a
reason to confirm** — F12 is explicit that the notice is the point and a
block would be the app asking permission for the thing this episode exists
to prove is safe.

Declared now rather than when F13 arrives, in the shape every other narrow
interface here already uses: Root asks "can you tell me this", never reaches
into a workspace's fields. UI-13's dirty branch is proven by test only.

### 7. `Confirm` and the switcher became `MaximisedOverlay`s

Both for the reason `F06-right-pane.md`'s "Known limitation" note gives:
`MinSize` is also the refusal threshold, so raising it stops terminals that
can open a surface today from being able to. F9a built the
preferred-vs-minimum distinction for the tray and the result card; F12 is
the third and fourth consumer. See bugs 3 and 4.

---

## The bugs

Six found on the first gate run, two more on the second. Every fix was
reverted individually to watch its own regression fail.

### 1. A lazily loaded workspace missed the handshake, and chat never bootstrapped

**The one genuinely new failure mode lazy loading introduces**, and it was
found by looking for it rather than by the gate.

`chat.Workspace` does not bootstrap in `Init()` — its own doc says `Update`
watches for `protocol.ConnectedMsg` and starts the
ListSessions/GetSession/CreateSession chain from there, because a query
fails while the client is not connected. The backend handshake almost always
completes while the chooser is still on screen, so the real `ConnectedMsg`
arrived before the workspace existed and **no second one was ever coming**:
Chat would sit at "loading" forever, no session, no transcript. Before F12 a
workspace existed before the first frame, so this was unreachable.

`switchWorkspace` replays the handshake (and the window size) from the
connection state Root already holds, so there is no second copy of "are we
connected". Proven by reverting the replay and watching
`TestChatBootstrapsWhenTheHandshakeHappenedBeforeItWasChosen` fail.

### 2. The provider chosen before any workspace existed was lost entirely

Reported from the gate: on a fresh profile, pick a provider, esc, choose
Chat — and the Status card reads `— · —` until the user re-picks the same
provider from inside Chat.

`ProviderDefaultSetMsg` routes to the ACTIVE WORKSPACE. On a first run the
chooser opens before anything is loaded (it has to — it is a prerequisite,
not a peer), so the message reached the vacant screen, which ignores it, and
was gone. chat was then built from the `State` `main.go` captured at
STARTUP, where `Provider` is nil, so `onSessionList` sent `CreateSession`
with an empty payload.

**And one consequence the report had not seen:** `chat.applyProviderDefault`
is the only writer of that `state.json` field, so the choice never reached
disk either — a restart would have re-prompted for a provider already
chosen.

Root now replays the choice to a newly loaded workspace, BEFORE the
handshake replay, because the handshake is what starts the chain that reads
it. Confirmed F12's and not F6's or F10's: re-selecting from inside Chat
always worked, which proves both of those surfaces correct.

**Same class, not fixed, recorded:** Root keeps no copy of F10's UI
override (it applies the theme to `r.theme` directly and hands it over via
`SetTheme`), so an Appearance change made while nothing is loaded still
misses `state.json`. There is no state to replay from.

### 3. The quit dialog showed no buttons and no focus

`Confirm.MinSize` returned a flat height of 7 — five content rows — however
long its body was, and `overlayRect` treats a plain Overlay's `MinSize` as
the ACTUAL rendered size. F12's quit body wraps to four lines, so the button
row and the key hint were rows six and seven and were simply cut off: no
labels, no focus marker, while ←/→ and Enter still worked, which is what
made it read as a frozen box rather than a clipped one.

**The width axis of this same bug was already fixed once** (F9a's "Cancel
it" / "Leave it running" overflowing a 40-cell row) and the height was left
a constant. Same defect, one axis over.

Fixed with `PreferredSize`, not a bigger `MinSize`: `MinSize` is the refusal
threshold, and for a dialog that exists to warn about losing work, a short
terminal getting no dialog is the worst possible trade.

**This is NOT the limitation F6/F7 flagged.** That note is about `MinSize`
never GROWING a content surface; this is the opposite — a surface too small
for its own content.

### 4. The switcher's idle readout rendered nowhere

`Popup.renderRow` draws exactly two columns, the title and the NOTE; the
description column was dropped from Popup outright long ago, with its own
doc explaining why it was not chased with more width. The readout was set
with `SetDescription`, so it rendered nowhere at all and the row showed its
binding — `—` for the one workspace that has none.

Moved to `Decor`, which is what F9a's tray uses and for the same reason (the
value changes while the popup is open), plus a state glyph. A second layer
surfaced while fixing it: at the 50-cell floor `rowWidths` caps the note at
a third of the row, so the readout elided to `idle 1m30s of…` — hence the
`workspaceSwitcher` wrapper asking for 72 cells, with `MinSize` left at the
floor. The leader key is deliberately NOT prefixed to the note: at 24 cells
it cannot fit beside the readout, and bindings already have a home in the
palette and Help.

> **Changed by F13 (Session 18a).** The readout's FORMAT changed — it is
> now clamped, and says `· pinned` when it is sitting at its limit. The
> plumbing described here (`Decor`, the 72-cell wrapper, the unprefixed
> note) is untouched. See **Superseded by F13 (Session 18a)** below.
>
> The note in that section about the rows being a snapshot taken when the
> popup opens is also worth reading beside "the value changes while the
> popup is open" above: `Decor` is re-read per render, but nothing
> re-renders the switcher while it is up, so in practice the figure is
> correct at open and then still.

### 5. The chooser was one cell too narrow for its own `last:` line

Rendered as `last: Chat · position restored on op`. `MinSize` measured only
the button row, and the `last:` line is the widest thing in the box (39
cells against the 36 a 40-column box leaves). `padCells` then cut it,
silently and with no ellipsis, which is why it read as a typo rather than as
an overflow. Now measured from every line the box draws, with the three
fixed strings moved to fields so `MinSize` and `View` cannot disagree.

### 6. The leader hint bar cut a binding name in half

Rendered as `… w Workspac` at 74 columns. The bar was built blind and then
cut by `truncateCells`, which pads to exactly N cells and elides nothing.
**F12 caused the visible truncation**: the list was 93 cells before this
episode and fitted at 100; the two new leader actions took it to ~129.

A hint is a claim about what a key does, and a cut one is that claim with
the wrong word in it — strictly worse than the hint being absent (F04's Bug
12 is this codebase's standing rule against a hint that lies). `fitHints`
now drops whole hints and appends `+N`, and `statusBar` builds its right
half first so the hint builders know their real budget.

Bugs 5 and 6 are **two independent overflows with one shared aggravator**:
`truncateCells` elides nothing, so both read as typos. It was left alone —
`padCells` builds on it for column-exact rendering everywhere, where an
ellipsis would be wrong — and both call sites were fixed instead.

### 7. (second gate run) A scrolled-up resume lands above content that arrived while away

Reported as "the transcript does NOT show the research task's row on this
reload". **The row is not missing**: it is in `w.all`, fully decorated, with
`laneTask` and a terminal status, from the first frame and with no
`ListTasks` reply involved at all. What is wrong is the VIEWPORT —
`applyResumeAnchor`'s non-pinned branch restores the reading position from
the blob, which on this sequence is above the answer.

**Pinned resumes are correct**, which Aryan confirmed by hand on the second
run (`pinned_to_bottom: true` at unload, row present immediately). That is
F18's `PinnedToBottom` rule — "the newest message, resolved at Resume time"
— doing the job it was specified for, and until F12 nothing called
Suspend/Resume so nothing had exercised it end to end.

Three hypotheses from the report were ruled out **by test, not argument**:
`requestTaskRows` timing (the fixture has no client, so that request never
even succeeds, and the slot is still correct); an `entryFor`
`w.live`/`w.hist` resolution gap (the entry comes from `decorateReloaded`,
off the message's own columns); and a `historyWindow`/`slotUI` re-keying gap
(the entry is rebuilt from `w.raw` each time, carrying its lane and status).
A fourth of our own went too: both fetch paths serialise through
`Message.to_dict`, so `invocation` is present on each.

**Attribution: F18 owns the policy, F12 made it reachable.**
`applyResumeAnchor` arrived in `75aab79`, and `git grep` on HEAD finds no
callers of Suspend/Resume at all.

**Parked to F16, not fixed.** The cheap fixes — resume to the live edge when
newer content exists, or say "N new below" — both need the session's
newest-seq at suspend time, a fourth field on `blobAnchor`, which is
redefining F18's shape and was ruled out. The option that needs no new state
(make the existing `↓ jump to latest (G)` affordance louder) is purely
presentational and genuinely F16's. Full entry with costs in
`tasks/frontend/INDEX.md`'s parking lot. Three tests pin it, one explicitly
marked as characterising the parked behaviour so it must change if the
decision does.

### 8. (second gate run) `POINT_PPROF` appeared never to listen

Step 6 reported "actively refused" on `localhost:6060` and `127.0.0.1:6060`.
**The server binds `127.0.0.1:6061`, and has since F3** —
`F03-chat-viewport.md` records that number. **F12's own task doc asks for
`localhost:6060` in its Done-when #7 command**, and this episode's gate
script copied the wrong number from it. Nothing has ever bound 6060, so the
refusal was correct. Per CLAUDE.md the code is the truth: the port stays and
the script was corrected. **F12's task doc still says 6060 and was not
edited** — flagged here instead.

A second trap in the same place: the bind is v4-loopback-only, so
`localhost` resolving to `::1` can be refused while the server is healthy.
Both the script and the log line now use the literal address.

**One real latent defect, fixed:** `ListenAndServe` ran in a goroutine and
`log.Info("pprof enabled", …)` fired unconditionally, so the one line anyone
would check claimed success before the bind had been attempted. `net.Listen`
is now synchronous and the log reports what actually happened — verified
both ways, including a second instance on the same port logging
`ERROR pprof NOT started — nothing will be listening` with the real error.

---

## Manual Test Log (F12)

All six steps of `backend\tests\manual_f12_lifecycle.ps1`, in a scratch
profile, confirmed by Aryan on 2026-10-10.

| # | Done-when | Result |
|---|---|---|
| 1 | Startup shows the chooser; the other workspace stays NOT_LOADED | ✅ clean. One button, which is the honest rendering of one registered workspace (assumption 2) |
| 2 | Unload with a task running → notice, task continues, result opens from the tray | ✅ the demo beat verbatim; tray and `cli.py tasks list` both say RUNNING→COMPLETED, never CANCELLED |
| 3 | Resume rehydrates transcript from the DB, position and draft from the blob | ✅ clean |
| 4 | Idle timeout unloads an inactive workspace | ✅ the half that needs no second workspace (the active one never unloads). The rest by test |
| 4b | Per-workspace timers are independent | ✅ **by test** — needs three workspaces; impossible by hand until F13 |
| 5 | `ctrl+q` lists the running tasks and warns they will be cancelled | ✅ clean |
| 6 | Kill without unloading, restart → drafts and position return | ✅ clean |
| 7 | Heap returns near its pre-load level over 20 load/unload cycles | ✅ `delta=+5464 bytes over 20 cycles (+273/cycle)`, run by Aryan |

### Also verified, and expected

Reloading Chat while the task is **still Running** shows no row for it until
it completes. That is the already-parked `orphanReply` decline — a
non-terminal task has nothing to render that would not either lie or get
stuck — settled earlier in the same session and parked under F16. Verified
as reproducing per the documented reasoning, not a new finding.

### Done-when #7 took catching a trap in its own test first

`TestTwentyLoadUnloadCyclesReturnTheHeapNearBaseline` drives the real
chooser-Enter / leader-`u` path with a 400-message transcript per cycle. Its
first version reported a constant ~930 KB **whatever the app did**:
injecting a deliberate retention of all 21 workspaces changed nothing, and
nor did hanging 20 MiB of ballast off Root.

Go's GC is precise about liveness, and after the loop nothing read `r`
again — so the whole Root graph was collectable before the final
`ReadMemStats`, inside a function that had not returned. With
`runtime.KeepAlive` the injected retention fails by **+28 MB**, so the
measured number is a measurement rather than a tautology. Without that check
this would have been B18's "green while verifying almost nothing", aimed
squarely at the one Done-when that is purely a measurement.

Two independent runs: `+11920 bytes / 20 cycles` and Aryan's `+5464 bytes /
20 cycles`. Flat.

### A standing test-suite violation, found here and fixed separately

`go test ./frontend/internal/chat/` was writing the real
`%LOCALAPPDATA%\Point\state.json` — confirmed against a clean tree with no
F12 changes in it, and it had already wiped a field from a live profile by
the time it was noticed. **It predates F12 and is recorded in
`tasks/frontend/INDEX.md`'s parking lot, not here**, at Aryan's instruction.
Fixed by a package-level `TestMain` scratch redirect in both `chat` and
`app`, with `TestTheProfileIsRedirectedAwayFromTheRealOne` failing outright
if the redirect is ever removed.

---

## Superseded by F13 (Session 18a)

**Two of this episode's shipped behaviours were changed by a later
episode, both on Aryan's call during F13's verification rounds, and both
with one of this doc's own tests replaced.** Recorded here rather than
only in F13's notes, because a reader arriving at this file to learn how
the lifecycle behaves would otherwise be reading two things that are no
longer true.

### 1. The global "any key to keep" rescue is gone (round 3)

**Was:** `keepWarnedAlive`, called from `Root.Update`'s `KeyMsg` case,
reset the idle clock of every workspace currently inside its grace
window — from any key, in any workspace. This episode argued the scoping
was already narrow: only the WARNED workspaces, not every loaded one,
"which would make the whole mechanism unreachable while the user types
anywhere in the app".

**Why that argument proved too little.** It makes the mechanism
unreachable anyway, just later. Once a backgrounded workspace enters its
grace window, one keystroke anywhere resets it to a FULL timeout — so
while anybody is typing, the warned workspace cycles idle → warn →
rescued → idle → warn → rescued and can never unload. Reproduced by
hand: open the Editor, visit Chat, come back, wait for "Chat unloads in
29s", then keep typing in the Editor; Chat's countdown returns to full,
indefinitely.

A keypress in the Editor is not evidence of interest in Chat at 29
seconds remaining for exactly the reason it is not at four seconds.

**Now:** `keepWarnedAlive` is deleted. A key affects only the ACTIVE
workspace's own accounting (`touchActive`, unchanged in intent).
**Switching to a workspace is the only rescue** — `switchWorkspace`
already clears its clock, and that is the one act that evidences
interest. The grace warning names the switcher's own binding instead of
promising a key:

```
Chat unloads in 29s — ctrl+w w to keep it
```

resolved from the action registry rather than hardcoded, so the sentence
cannot drift from the binding.

**Test replaced:** `TestAnyKeyKeepsAWarnedWorkspaceAndOnlyThatOne` →
`TestAKeyInOneWorkspaceDoesNotRescueAnother`, which asserts the inverse
and that the backgrounded workspace goes on to unload *while* the active
one is being typed in. Added alongside it:
`TestSwitchingToAWarnedWorkspaceKeepsIt`, and
`TestTheGraceWarningNamesTheSwitcherNotAKey`.

### 2. The idle readout is a clamped countdown (rounds 4 and 5)

**Was:** `idle 1m30s of 5m00s`, raw elapsed against the budget.

**The problem.** Elapsed can legitimately exceed the budget, so the
readout could print a number past its own denominator — `idle 2m35s of
1m00s` — which reads as the app being broken when nothing is wrong.
Two things produce it, and F13 introduced the first:

- a workspace **pinned by unsaved work** never unloads however long it
  idles. F13 taught `walkIdle` to skip a workspace whose
  `DirtyBufferCount()` is non-zero rather than discard its edits — with
  no save path in 18a, an idle unload would have been the only
  unrecoverable action in the app. `DirtyBuffers`, declared by this
  episode with no implementors (Decision 6), is implemented by the
  Editor;
- the switcher's rows are a **snapshot** taken when the popup opens and
  nothing re-renders them while it is up (`app\chooser.go`, this
  episode's) — so the figure is also still. **That part is unchanged and
  remains this episode's**, deliberately out of F13's scope.

**Now, after two passes.** Round 4 replaced the countdown with plain
`active` / `loaded` / `not loaded`. Round 5 reinstated the countdown with
a clamp, which is what ships:

```
active                           the one on screen
not loaded                       no instance in memory
loaded · stays loaded            idle_unload_minutes = 0
idle 0m23s of 1m00s              counting up, normally
idle 1m00s of 1m00s · pinned     clamped, and said to be clamped
```

Both sides carry a minutes field at every value — `idle 0m01s of 1m00s`,
not `idle 1s of 1m00s`. `shortDuration` had an under-a-minute special
case (`if total < 60`) that contradicted its own doc comment and, because
the readout formats both sides with it, produced a line in two formats.
It is now unconditional `%dm%02ds`.

That briefly moved the grace warning with it, to `unloads in 0m29s`, and
that part was **reverted**: the warning is a countdown to an imminent
event, bounded by `idleGrace` and so never more than seconds, and `in 29s`
is how a countdown that short reads. It has its own `graceSeconds` at its
own single call site — NOT the under-a-minute branch put back inside
`shortDuration`, which is the special case that caused the bug. Two
formatters for two purposes; the paired one stays unconditional because
its output is read as a pair, and the other has no pair.

The readout also needed a note column of its own. `idle 1m00s of 1m00s ·
pinned` is 28 cells against `rowWidths`' 24-cell ceiling, so Popup
truncated the pinned label away and the row read `idle 1m00s of 1m00s …`.
`PopupConfig.NoteW` lets a popup declare what its note needs, clamped
against the NAME column rather than against a third of the row — the
third-of-the-row rule stays the default for the palette and Help, whose
names are the content. Measured while fixing it: `rowWidths(86)` still
answered 24, so extra popup width could never have fixed it.

The clamp and the `· pinned` label come as a pair: clamping alone would
read as "about to unload" for the one workspace that definitely will
not. The clamp is unconditional rather than pinned-only, because a
workspace is also briefly over budget between the timeout elapsing and
the next one-second tick unloading it.

`WorkspaceStatus` gained a `Pinned bool`, populated by
`WorkspaceStatuses` through the same `DirtyBuffers` interface `walkIdle`
asks, so the readout and the sweep cannot disagree about which
workspaces are pinned. `Idle` and `Timeout` are unchanged and still
carry the RAW values — only the rendering clamps.

**Test replaced:** `TestTheIdleReadoutDistinguishesActiveIdleAndNotLoaded`
→ `TestTheIdleReadoutCountsDownAndNamesTheOtherStates`. Added:
`TestTheIdleReadoutClampsElapsedToTheBudget`,
`...ClampsEvenWhenNotPinned`, `...NeverRendersElapsedPastItsBudget`
(every budget × elapsed × pinned combination), and
`TestTheIdleReadoutSaysSoWhenAWorkspaceNeverUnloads`.

### 3. Three smaller changes to this episode's files, for the record

- **`resetIdle`** — `switchWorkspace`, `touchActive` and
  `unloadWorkspace` each cleared `lastActive` and `idle.warned` by hand.
  F13 added a second latch (`idle.dirtyNoticed`, for the pinned notice)
  to only one of those three places, so the notice fired once per loaded
  lifetime instead of once per idle period. All three now call one
  `resetIdle(id)`, which is what "this workspace's idle accounting
  starts over" means in one place.
- **The status expiry is derived in `Update`** — see the note on
  Decision 3 above. `r.status` is still assigned directly from about
  forty places; `expireStatusIfChanged` compares the value before and
  after each message and arms a tick when it changed, so none of those
  sites has to remember. `Root.StatusLinger` makes the duration
  substitutable, for the same reason `Root.Now` is: the derived tick
  rides in every status-changing message's command batch, and a test
  that executes what `Update` returned would otherwise park for the real
  linger.
- **`propagateTheme` reaches every loaded workspace**, not just
  `r.current()`. A loaded-but-inactive workspace never heard about a
  theme change, and `loadWorkspace` (construction) was the only other
  caller of `SetTheme` — so the Editor kept the palette it was last
  active under. Not strictly this episode's bug, but it is this
  episode's file and this episode's lifecycle that made a non-active
  loaded workspace possible at all.

## Handoff — what later episodes inherit

### F13 (Editor Core) — the first second workspace

- **Implement `app.Workspace` exactly as recorded above.** Nothing else is
  required to be registrable.
- **Register through `Options.Workspaces`**, with a real `MinSize` — the
  Editor's is going to exceed layout's 50×12 floor, and `View` already
  refuses below a workspace's own declared minimum with the actual numbers.
- **`LeaderKey` is free for `e`.** Chat's is empty because `c` is F6's; `e`
  collides with nothing.
- **Implementing `DirtyBuffers` is what turns UI-13 on.** One method, no
  edit to `lifecycle.go`, and the dirty branch starts appearing.
- **Three Done-whens become manually verifiable the moment F13 registers**:
  #1's NOT_LOADED check, #4's idle unload, and #4b's independent timers.
  They are green by test today; run them by hand then.
- A per-workspace `[workspace.editor] idle_unload_minutes` already resolves
  with no code change — F12's own worked example uses 60 for exactly the
  reason F13 will want it.

### F16 (polish) — two items anchored here

- **The scrolled-up resume** (bug 7). The no-new-state option is making the
  existing `↓ jump to latest (G)` affordance louder. The other two options
  need `blobAnchor` reopened, which is a decision above F16.
- **`orphanReply`'s non-terminal decline** — the earlier parked item in the
  same area. Both have full entries in `tasks/frontend/INDEX.md`.

### B15 — what F12 did not send

`workspace.unload(id)` is NOT sent; B15 does not exist. Until it does,
Python holds a session's contexts until that session is next touched.
Nothing in the frontend waits on it, so adding it is additive: one send in
`unloadWorkspace`, after `Suspend` and before the key is deleted.

### B16 (SSH serving) and anyone streaming into an unloaded workspace

F18 specified `PinnedToBottom` as earning its keep "only once F5 streams
into unloaded workspaces", and bug 7 is the first evidence of that
mattering in practice. A pinned resume lands on whatever arrived while away;
a scrolled-up one does not. If B16 makes content arrive while unloaded
routine rather than occasional, bug 7's park should be revisited first.

### Anyone adding something Root announces to the active workspace

`switchWorkspace`'s replay block is the pattern: anything Root tells the
active workspace by MESSAGE rather than by being asked must be replayed to a
workspace loaded later, or it is silently lost (bugs 1 and 2 were both this,
found separately). The handshake and the provider default are replayed
there. F10's UI override is the known gap — Root keeps no copy to replay.

### Anyone touching `state.json`

Two owners, one rule, in `saveStateTo`'s own doc: a save that leaves
`LastWorkspace`/`WorkspaceState` at their zero value keeps what is on disk.
`main.go` zeroes both in chat's copy. **A third owner would need this rule
generalised, not copied.**

### Not built here, on purpose

- Backend-side unload (B15, above).
- Capability workspaces (Coding, Data) — parked, evidence-gated, and the
  registry is the hook `TEMPLATES.md` names.
- Declarative TOML layouts — parked; the registry is rule 2's hook.
- Anything that cancels, pauses or waits for a task on unload. That is the
  property this episode exists to disprove the need for.
