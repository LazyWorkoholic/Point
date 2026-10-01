# Episode F8 — @ Picker and Attachments ✅

**One key opens agents, files and memory. The picker is Root-owned (not the
workspace's) because an open Overlay never receives wire replies; typed
input never navigates; browse location has exactly one writer; and every
command-like character is leading-position only. Nine rounds of manual
verification found ten defects — four in the picker's own async wiring, one
real backend ordering bug in B12's attachment adoption, and five in one
single mechanism (browse-mode filter typing) that turned out to be one
cause with five symptoms.**

> Verified by Aryan on 2026-10-01 against
> `tasks/frontend/F08-at-picker.md`'s eight "Done when" criteria, across
> nine rounds of live manual verification in scratch
> `%APPDATA%`/`%LOCALAPPDATA%` profiles per CLAUDE.md's env-var-override
> pattern — the real profile was never named in any destructive call.
>
> **Done-when #8's `.gitignore` toggle is the one item NOT met.** B7's
> `ListProjectFiles` has no per-request override for
> `index.respect_gitignore`, so the toggle cannot be built without a
> backend change; its first half (gitignored paths absent, `.venv` always
> absent) does hold. Recorded under "Known gaps" below rather than built,
> per CLAUDE.md's rule against unscoped adjacent work.
>
> Every fix in this document was proven by reverting it to watch its
> regression test fail and restoring it to watch the test pass. Defect E
> (the backend ordering bug) was additionally proven by direct database
> query, not by a unit test alone.

---

## Where the code went

Root owns the picker. One new file in each package, plus the shared widget's
own extensions.

```
frontend\internal\app\
├── picker.go          NEW — the whole controller: OpenAttachmentPickerMsg,
│                       PickerTarget, the wire calls (ListAgents,
│                       ListProjectFiles, ListDirectory, AttachContext),
│                       THE MODEL (the interaction contract, stated once),
│                       pickerLeadingCommand, browseTo, pickerShowRefusal,
│                       pickerHint
├── picker_test.go     NEW — 20 tests, including one per numbered defect
├── popup.go           + OnSelect returns (tea.Cmd, bool); + SetItems;
│                       + QueryFor; + OnFilterChange; + Hint
├── popup_test.go      + 7 tests proving palette/Help are unaffected
├── overlay.go         palette's OnSelect updated for the new signature
└── root.go            + picker pickerState; + the OpenAttachmentPickerMsg
                        case; + handlePickerEnvelope in EnvelopeMsg;
                        + retryPickerIfAwaiting in ConnectedMsg

frontend\internal\chat\
├── picker.go          NEW — PickerSession (app.PickerTarget), and the two
│                       write-backs: agent chosen -> text, file chosen -> chip
├── attachments.go     NEW — detachChip, /detach, the staleness sweep
│                       (armChipTick/onChipTick/ResolveAttachment), and
│                       refreshAttachmentFreshness/onAttachmentList
├── attachments_test.go NEW
├── input_keys.go      the '@' trigger now carries Leading; backspace-on-chip
│                       actually detaches; the no-live-recompute decision
├── input_render.go    AttachmentChip gained ID/Path/RecordedMtime/RecordedSize
├── protocol.go        + 2 query kinds, + the write-back cases
├── model.go           wireSession decodes project_id
├── chat.go            + projectID, chip-tick and resolving-path state
├── context_report.go  triggers the freshness refresh
└── stream.go          clearInput no longer wipes chips; /detach intercept

frontend\internal\protocol\types.go
    + ListAgents/ListDirectory + the whole B12 attachment command set,
      and their reply events

backend\
├── runtime\runtime.py  _adopt_project_for split into
│                       _adoptable_project_for (resolve) + _commit_adoption
│                       (write); _handle_attach_context reordered
└── tests\manual_f8_adopt_after_guard.py  NEW — the DB-level proof
```

**No new dependencies.** `go-runewidth` was already in use by this package.

---

## The architecture, and why it is shaped this way

### Root owns the picker, not chat.Workspace

`protocol.EnvelopeMsg` is routed by Root to the ACTIVE WORKSPACE only — an
open `Overlay` never sees one (root.go's `update`). Filesystem browse mode
needs fresh `ListDirectory` calls as the user descends, so something has to
own that request/reply cycle and push results into a popup that is already
open. Root already does exactly this for the Task card
(`refreshTasks`/`handleTaskListEnvelope`, its own correlation id,
independent of any workspace), so the picker follows that shape rather than
inventing a second one.

The consequences, all deliberate:

- `OpenAttachmentPickerMsg` lives in `app`, not `chat` — the same reason
  `EnterBrowseMsg`/`FocusInputMsg` do. `app` cannot import `chat`.
- chat exposes `PickerTarget.PickerSession()` (narrow interface, the
  `FocusAware`/`OverlayProvider`/`DraftFlusher` pattern) for the one thing
  only it can answer: session id, project id, and whether a project is bound.
- The picker writes back through messages (`AgentPickedMsg`,
  `AttachmentPickedMsg`), which Root's generic fallthrough already delivers.

### THE MODEL — the interaction contract

Stated in full at `pickerLeadingCommand` in picker.go. In brief:

1. **Typed text never navigates.** Once browse mode is engaged, a keystroke
   only ever filters the listing already on screen.
2. **Location changes only from a chosen row** (Enter/click), including the
   `..` row. Every row id is an ABSOLUTE path built from the reply that
   listed it, so no navigation is ever resolved against ambient state.
3. **One leading-slot command evaluator**, consulted from exactly one call
   site. Leading position only — every check is an exact match or a
   `HasPrefix`, the same discipline `@` itself follows in F04's
   `agentTokenState`.
4. **Every command target is self-contained** — absolute, `~`-rooted, or
   relative to the session's fixed base. Never relative to the current
   browse location, and nothing joins it against one.

**Single source of truth for "where is browse mode rooted":**
`r.picker.root`, written in EXACTLY ONE place — `onPickerListing`, from the
reply's own `path`. Both the render side (the `..` row, every item id) and
the navigation side (Enter's absolute targets) are built from that same
reply in that same function, so they cannot disagree. A failed navigation
leaves it untouched.

### Mode triggering — the complete rule set

| Input | Effect | Modes |
|---|---|---|
| bare `@` | fuzzy (Agents + project file index) if a project is bound; browse rooted at the frontend's own cwd if not | — |
| `@?` / leading `?` | force browse, rooted at the project root (or cwd) | fuzzy only |
| exact `D:\` `D:/` `C:\` … | jump to that drive root | **both** |
| `~\` `~/` | jump to the home directory | **both** |
| `.\` `./` `..\` `../` | browse relative to the session's fixed base | fuzzy only |
| bare `\`, bare `/` | **nothing — ordinary filter text** | — |
| anything else | filter | — |

**The drive root is an EXACT match, not a prefix.** The filter must be
precisely `<letter>:\` or `<letter>:/` — three characters — to jump. This
was `len(raw) >= 3` and that was a bug with a precise symptom: typing
`C:\Windows\` character by character fired the moment the third keystroke
completed `C:\`, navigating away before `Windows\` could be typed. Under
exact equality a longer path sharing the prefix is never EQUAL to the bare
pattern, only ever longer, so `C:\Windows\`, `D:\Projects\` and `C:\W` all
stay ordinary filter text throughout, in both modes.

**`~\` / `~/` are deliberately still PREFIX matches** — confirmed, not
overlooked. `~\Desktop\` firing at `~\` lands in the home directory with
the filter cleared, and "Desktop" then filters that listing, which is what
a home shortcut is for. `C:\` differs only because it is itself typed as
the prefix of a longer path someone is mid-way through writing.

**Neither bare `/` nor bare `\` is a command**, revising F08's own original
table (which listed both). `/` went first: it is realistic fuzzy-search
text, and claiming it made that text unreachable. `\` followed once it was
found jumping to a drive root on its own. Reaching a drive root now
requires naming the drive.

**Agents' visibility is governed SOLELY by `leading`** — whether the
triggering `@` sat at buffer index 0 (B12: only the leading @token can ever
name an agent). Completely independent of fuzzy-vs-browse.

**Esc dismisses without touching the chat buffer.** The `@` stays as
ordinary text; typing more does not reopen the picker — only a fresh `@`
keystroke re-arms it. Decided, not a gap: re-deriving "should the picker be
open" from buffer content every keystroke would require tracking a second
fact ("has the user already dismissed THIS `@`") independent of the buffer
itself, which is precisely the two-sources-of-truth shape this codebase has
been bitten by three times (`DiagnosticsMsg.Seq`, `taskCardWasVisible`, B23
existing at all).

### Popup gained five additive capabilities

F07's own rule is "extend the shared one, never fork it". All five are
optional; the palette and Help are byte-for-byte unaffected, with seven
tests proving it.

| Addition | Why |
|---|---|
| `OnSelect` returns `(tea.Cmd, bool)` | descend into a directory without closing |
| `SetItems` | swap items in place (async fill, directory swap); clears filter, resets selection to top |
| `QueryFor` | override what the fuzzy matcher sees (now the identity — kept wired, see its doc) |
| `OnFilterChange` | the only hook that can see the leading slot change |
| `Hint` | one dim consumer-specific line above the footer |

`Hint` is a **func, not a string**, deliberately: it is called at render
time, so what it returns is always current with the state it reads. The
picker's hint is mode-aware (`?` is advertised only while it is actually a
command) and a stored string would be a second copy of the mode to keep in
sync. F04's Bug 12 — a browse indicator advertising an Esc binding that did
not exist — is this codebase's standing rule against hints that lie.

---

## The ten defects, and what each one actually was

### A. The Agents section vanished a few seconds after loading

`handlePickerEnvelope` matched replies with
`switch env.CorrID { case r.picker.agentsReqID: ... }` and did not guard
`env.CorrID == ""`. Once a reply lands its reqID field clears to `""` — a
valid switch case value. The backend's 5-second idle heartbeat
(`runtime.py`'s `start_heartbeat`, `request_id=None` → `corr_id: null` →
`""` in Go) then matched it forever after and was handled as an empty
`ListAgents` reply, wiping the section. `handleTaskListEnvelope` already
guards the identical case (`env.CorrID != "" && ...`); this did not, and
should have. Also added fan-in gating so the two initial fetches cannot
reset the selection out from under a user mid-navigation.

### B. The picker stuck on "Loading…" forever at startup

`'@'` pressed before the handshake completed: `client.Request` returns
"backend is not connected", `pickerSend` returned `("", false)`, no reqID
was ever set, and nothing was in flight to replace the placeholder.
`awaitingConnection` now records that a send never went out, and
`retryPickerIfAwaiting` (called from Root's own `ConnectedMsg` case, beside
`refreshTasks`) retries.

### C. Choosing a file inserted text instead of creating a chip

No separate defect was ever found — every mechanism traced back to A.
`SetItems` resets the selection to row 0 by design, and A was calling it
every ~5 seconds with Agents wiped, so a selection made shortly before a
heartbeat landed resolved against row 0 (usually an agent, since Agents
sort first). Fixing A removed the spurious calls. Confirmed live afterward
across multiple repetitions.

### D. A freshly attached file flipped to "changed" after ~2 seconds

Two compounding mistakes. `PickedAttachment.RecordedMtime/RecordedSize`
were plain `int`, but `managers\attachment.py` inserts a fresh row with
those columns NULL — populated only by a real send
(`record_attachment_sent`), never by attach. Decoding JSON `null` into an
`int` silently yields `0`, so the periodic sweep compared a real file's
mtime against a bogus zero baseline and always reported "changed". Both are
`*int` now. Separately, `Current: true` immediately after attach was wrong
on its own: B12's own verification is explicit that an unsent attachment
costs its full estimate, which is what "changed / will send" means.

The post-send transition was also rebuilt. The first design matched
`ContextReport`'s `detail` string for "unchanged since last sent" — fragile
by that field's own doc ("a human rendering that is allowed to change") and
worse, it never updated the baseline, so the next sweep undid whatever it
had just corrected. It now reads the attachment rows directly
(`ListAttachments`), matched by exact id.

### E. (backend, B12) A REFUSED attach still bound the session's project

Found during F8 verification, twice, with a correct repro the second time:
a fresh unbound session, a genuinely binary file (a copy of notepad.exe)
inside `D:\Projects\Point`'s tree, refused with "looks binary (null bytes
in the first 8KB)" — and `project_id` set anyway.

`_handle_attach_context` resolved-AND-wrote the adopted project at line
1067; `attachments.attach()`, which raises `AttachmentRefusedError`, was
line 1069. The write could not simply move after the guard, because the
guard NEEDS the project to resolve the path against — that is B12's own bug
#1 ("no project is open" while standing in one). So the function was split:
`_adoptable_project_for` resolves without writing, `_commit_adoption`
writes, and the handler calls the latter only after the attach returns.

**This is a B12 backend fix, surfaced by F8.** It is recorded here because
this is the episode that found it; B12's own completed doc predates it.

### 1–5. One cause, five symptoms (browse-mode filter typing)

`onPickerFilterChange` had a second branch that treated typed text as a
path: `splitPickerPath` + `filepath.Join(r.picker.root, dir)`. That single
branch produced, in order:

1. typing `backend\` or `backend/` silently descended, with no Enter;
2. typing `..\` silently ascended; a mid-string separator acted like a
   command;
3. an unresolvable path appeared to reset navigation;
4. **a freshly typed `D:\` was JOINED onto the previous location** —
   measured directly: `filepath.Join("D:\Projects\Point\backend", "D:\")` =
   `"D:\Projects\Point\backend\D:"`, which is exactly the reported "landed
   inside Point\backend";
5. a refusal appeared stale against the list.

The branch is deleted. Restoring it reproduces every reported spelling at
once — `backend\`, `backend/`, `..\`, `../`, `sub\dir\`, `D:\`,
`C:\Windows\`, `~\` — which is the evidence that it was one cause and not
five bugs.

Item 5 had a second, real half: `renderRow` truncates a row's name at 40
display cells, so long backend refusals were being cut mid-sentence and
read as cryptic fragments ("No path available") over a list that was
actually correct. Refusals now wrap across rows (`wrapPickerNotice`) and
name the still-current location.

### 6. Agents disappeared when browse mode engaged

`onPickerFilterChange` unconditionally set `agentItems = nil` the moment any
browse prefix matched, coupling Agents to fuzzy-vs-browse instead of to
`leading` alone. One line removed.

### Discoverability (this round)

Every path shortcut worked and was tested, but nothing in the UI said so.
One dim line above the key-binding footer now names them, mode-aware.
Confirmed by rendering: it is its own row, below the list and above the
footer, so it never competes with refusal notices or the "still showing
`<path>`" line, which both live in the list region.

---

## Manual Test Log (F8)

| Item | Result |
|---|---|
| Bug A — heartbeat wiping Agents | ✅ fixed; revert-to-fail proven (`agentItems = 0 ... want 1`), restore-to-pass confirmed |
| Bug B — stuck on Loading at startup | ✅ fixed; verified live, repeated attempts, never stuck |
| Bug C — file selection inserting text | ✅ no separate defect; resolved by A, verified live across repetitions |
| Bug D — staleness false positive, and the post-send transition | ✅ all three halves verified live: no false positive untouched, flips on a real edit, flips back after a resend |
| Bug E — refused attach binding the project | ✅ fixed; **direct DB query**: two `f8-refusal` sessions `project_id` NULL after refusing the real binary and the 50MB file; `f8-success` bound after a valid attach |
| Items 1–5 — browse-mode typing | ✅ one cause removed; revert reproduced all eight spellings; the `filepath.Join` corruption measured directly |
| Item 6 — Agents vs browse mode | ✅ fixed; revert-to-fail proven |
| Exact-match drive root | ✅ fixed; revert to `>= 3` reproduced the reported symptom verbatim, restore confirmed |
| Fuzzy/browse symmetry | ✅ resolved as (a) for self-contained targets; identical behaviour from both modes proven by test |
| Guard refusals (oversized / binary / directory) | ✅ all three individually verified via `cli.py attach` with real test files |
| Parent navigation (`..\`) | ✅ added; correctly absent at a true filesystem root |
| `.gitignore` appearing in listings | ✅ expected, not a bug — a `.gitignore` does not exclude itself unless self-referenced |
| Cross-launch mode swap | ✅ not a bug — B12's `_adopt_project_for`, confirmed by DB query across relaunches |
| Discoverability hint | ✅ added; render inspected; does not crowd the refusal/location display |

Gate: `gofmt -l` empty, `go vet` clean, `go test ./frontend/...` green,
`manual_f8_adopt_after_guard.py` exit 0.

### The project-binding question, answered

There is exactly ONE writer of `session.project_id`
(`SessionManager.set_project`), reachable from exactly one place
(`_adoptable_project_for`/`_commit_adoption`) via exactly one handler
(`AttachContext`). But it is reachable through three front doors, which is
why it looked like something else was binding sessions:

1. the app's own picker;
2. **`cli.py attach` with no `--session`**, which targets the most recently
   updated session (`_session_for`) — i.e. the app's own;
3. **`cli.py session new`**, which creates an ALREADY-bound session from
   `--path` (default cwd), and which the app then opens next launch as the
   newest usable session.

Confirmed NOT writers: app startup (`CreateSession` takes `project_id` from
the payload only, and the Go bootstrap sends `{}`), `cli.py migrate`,
`GetSession`, `ListSessions`, `ListDirectory`, `ResolveAttachment`.

---

## Known gaps — recorded, not built

Each is deliberate. CLAUDE.md's rule against adjacent/unscoped work applies.

- **Chips do not survive a session reload.** The attachment stays correctly
  bound server-side and is still sent; nothing calls `ListAttachments` on
  load to repopulate the chip row. Reproduced and confirmed real.
- **No keybinding toggles the `.gitignore`-respecting setting.** B7's
  `ListProjectFiles` has no per-request override — it reads
  `index.respect_gitignore` from config — so the toggle F08's Build section
  asks for cannot be built without a backend change. Editing `config.toml`
  is the only way today. **Done-when #8's first half holds** (gitignored
  paths absent, `.venv` always absent); the toggle half does not.
- **No chip-focused Esc-to-detach.** `esc` is already bound to browse-mode
  entry for everything else in Chat, and this episode did not carve out an
  exception. Backspace-at-position-0 and `/detach` both work.
- **An absolute path typed while already browsing is the ONE exception to
  "typed text never navigates."** Allowed only because a drive-root or `~`
  target names exactly one location and cannot be corrupted by ambient
  state. Base-relative prefixes and `?` get no such exception.
- **The staleness sweep's cadence is 5s while chips exist**, zero when
  none do. Chosen, not measured against anything.

---

## What the next episode inherits

- **Popup's five extensions are shared infrastructure now.** F9 (task
  tray), F10 (settings) and F11 (memory) all inherit them. `OnSelect`'s
  close bool, `SetItems`' clear-filter-and-reset semantics, `Hint` being a
  func read at render time — each has a reason recorded at its own
  definition; extend rather than fork, per F07.
- **`handlePickerEnvelope`'s `env.CorrID == ""` guard is load-bearing.**
  Any future Root-owned request/reply cycle needs the same guard: a cleared
  correlation-id field is a valid switch case, and the backend's heartbeat
  carries a null corr_id forever.
- **A nullable wire integer must be `*int`.** Bug D was JSON `null`
  decoding into `int` as `0`, indistinguishable from a real zero. Anything
  reading `recorded_mtime`/`recorded_size` has to keep this.
- **`r.picker.root` has exactly one writer, and that is the property that
  keeps render and navigation in agreement.** Do not add a second.
- **`pickerLeadingCommand` is the only place the mode table lives.** Adding
  a command means adding a kind there, deciding which modes honour it, and
  nothing else.
- **F8's own Do-NOTs held:** memory search is still B13's (the Memory
  section renders only when the backend returns results, which it never
  does yet), and result/diff cards are still F9's.
