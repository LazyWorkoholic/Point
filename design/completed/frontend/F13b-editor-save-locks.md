# Episode F13b — Editor: Save, Locks and Autosave ✅

**Commits:** `65cb6dc` — "F13 complete: editing half (F13a) +
save/locks/autosave (F13b)" (the build) and `6e28f6b` — "F13b
verification repairs + F19" (four rounds of gate-driven fixes).

> The second commit's message names **F19**, an episode that no longer
> exists: its two shipped items were folded into `F16-polish.md` and its
> third was reverted and deferred there. The message is left as it was
> written — a commit message records what was true when it was made — so
> F16 is where to look, not a missing F19 doc.

**F13 is complete. The editor saves, and it saves correctly: a CRLF+BOM
file round-trips byte-identical using only the metadata B32's read
reported, a soft lock is taken on open and heartbeated every 20s against
B7's 60-second staleness, a second holder opens the file read-only with
the holder named, an external change refuses the save with
`[reload] [overwrite] [diff]`, closing a dirty tab routes through a
two-button confirm that discards rather than quietly saving, and autosave
fires once a second after typing stops with a PER-DOCUMENT timer. The
seven Done-whens F13a deliberately left — 2, 7, 8, 9, 10, 11, 12 — are
done, which closes F13.**

> Verified by Aryan across **five rounds** against
> `backend\tests\gate_f13b.ps1` (1,043 lines, 43 `Assert-That` calls, 0
> parse errors, UTF-8-with-BOM, ASCII-only, LF), in a scratch
> `%APPDATA%`/`%LOCALAPPDATA%` profile per CLAUDE.md's env-var-override
> pattern — the real profile was never named in any call, destructive or
> otherwise. Round 1 built it; rounds 2–5 are what running it found, and
> every fix was shown to fail against its own reverted code before being
> accepted (**11 of 11 red**).
>
> Final state at `6e28f6b`: `go build ./...`, `gofmt -l .` and `go vet
> ./frontend/...` clean; `go test -count=1 ./frontend/...` green at
> **1,251 tests, 0 failed** (editor 326, app 379, chat 360, protocol 92,
> layout 32, config 23, array 17, actions 13, theme 9).
>
> **Done-when 2's byte-exactness was only true for an ASCII file until
> B33 landed.** `backend\main.py` read stdin in the ANSI codepage, so any
> non-ASCII payload was mojibake'd or dropped below the wire. Nothing in
> this episode caused it and nothing here could fix it — see B33.

---

## Scope — the seven Done-whens F13a left

| # | Done-when | How it is gated |
|---|---|---|
| 2 | a CRLF file round-trips unchanged | gate step 1, SHA-256 before/after |
| 7 | a second holder opens read-only; take-over after it goes stale | gate step 3, `cli.py project lock acquire --holder someone-else@other-id` |
| 8 | external change then save → refused with three options | gate step 4 |
| 9 | closing a dirty file routes through `Confirm` | gate step 5 |
| 10 | `after_delay`: one save at the end, not thirty | gate step 6, FileSystemWatcher count |
| 11 | autosave does not fire on a file held by another holder | gate step 7 |
| 12 | autosave does not trip Point's own staleness check | gate step 8 |

Plus the save half of 1, and the five decisions C1–C5 Aryan settled before
the session.

## The five decisions, as built

- **C1 — the stale dialog is a narrow three-button overlay**, not a
  widened `Confirm`. `Confirm` is two buttons by construction (`focusYes`
  is a bool) and this refusal has three outcomes, none of which is "no".
- **C2 — closing `[diff]` returns to the dialog**, not the editor. The
  save is still refused and unresolved; `Overlay.Update` returning a
  different overlay swaps it in place, the mechanism S13d already used.
- **C3 — the dirty-close confirm is `[ Close ] [ Cancel ]`, Cancel
  focused**, Close discards, no implicit save and no third option. A
  button that sometimes writes depending on state the dialog does not
  show is one people stop trusting.
- **C4 — a mixed file writes its MAJORITY ending, LF on an exact tie.**
  The tie half is load-bearing: `describe_bytes` resolves its own
  ambiguous input (no line breaks at all) to `lf`, so LF-on-tie keeps the
  read and the write answering the same way about the one input each
  resolves alone. Writing CRLF on a tie would make a file flip its ending
  on every save. `encode_for_write` accepts only `lf` or `crlf`, so
  resolving before the wire is the contract, not a nicety.
- **C5 — the autosave timer is PER DOCUMENT.** Each `doc` carries its own
  `autosaveGen`; an edit bumps that document's counter and schedules a
  tick carrying it, and a tick whose value no longer matches is
  superseded. A single workspace-level counter passes every single-file
  test and fails the two-file one.

---

## Where the code went

```
frontend\internal\editor\
├── save.go      370  WriteProjectFile, all seven refusal slugs, writeEnding (C4)
├── lock.go      310  acquire on open, 20s heartbeat, release, f6 take-over
├── stale.go     420  the three-button dialog (C1) + the diff view (C2/B10)
├── reload.go    196  [reload] and [overwrite]; one splice, so one ctrl+z
├── autosave.go  192  the per-document debounce (C5) and the five rules
├── diff.go      111  a plain unified diff, prefix/suffix based
└── (doc.go, protocol.go, keys.go, status.go, tabs.go, editor.go extended)

frontend\internal\app\
├── contribute.go  +  OpenWorkspaceOverlayMsg (B9), LockHolder
├── confirm.go     +  OnCancel — the decline paths had no signal at all
├── root.go        +  routes OpenWorkspaceOverlayMsg
├── lifecycle.go   +  ReleaseLocks on unload
└── actions.go     +  ReleaseLocks on quit, every loaded workspace

frontend\internal\config\
├── holder.go   NEW   the composed <username>@<id>, generated once
├── state.go      +   Holder, and the keep-if-zero clause that preserves it
└── config.go     +   the [editor] table: autosave, autosave_delay_ms

frontend\cmd\point\main.go  +  generate/persist the holder BEFORE chatState
backend\tests\gate_f13b.ps1 NEW  1,043 lines, 43 assertions
```

### A2's erasure trap, closed on both sides

F13a's handoff recorded it precisely and both halves were required.
`config\state.go`'s `saveStateTo` keeps-what-is-on-disk for a named set of
fields; `chat.Workspace` writes the WHOLE struct from its startup
snapshot. A `Holder` generated **after** that snapshot is taken is erased
by chat's next debounced draft save — on the very launch that generated it
— and Point mints a new identity every start, unable to release or
re-acquire its own locks. So: generated in `main.go` **before**
`chatState := state`, cleared from chat's copy, and `Holder` added to the
keep-if-zero clause. Gate step 0c proves it by typing in the Chat input
and then checking `state.json`, and again across a relaunch.

---

## The rounds

Round 1 built it. Rounds 2–5 are what running the gate found. Every one is
named, because "all bugs fixed" is not a record.

### Round 2 — four findings, two of which were not product bugs

- **`ctrl+shift+l` was not a key.** The take-over was bound to it, and
  Windows produces the same control character for `Ctrl+L` and
  `Ctrl+Shift+L` — so it arrived as `ctrl+l`, which is `app.redraw`'s
  shipped chord, consulted before this workspace. **Done-when 7's
  take-over half had never run.** Rebound to `f6`. Exactly F13a's
  `ctrl+tab` finding, one key over.
- **Take-over judged staleness from a snapshot.** `d.lockedAge` is
  whatever the refusal reported and is never refreshed. It now sends
  `AcquireFileLock`, which is self-validating — steals a row past 60s,
  refuses a live one — so the backend is the only judge and
  `CmdTakeOverFileLock` is deliberately unused.
- **The dirty-close confirm did not pause autosave.** The dialog is open
  longer than the 1s debounce, so a tick armed by the keystroke that made
  the file dirty landed mid-dialog and wrote the edits — making Close
  discard a buffer already on disk and Cancel identical to it. Both
  buttons meaningless, which is the whole reason C3 exists. Fixed with
  `autosaveBlocked` **and** a generation bump (the flag alone is not
  enough: the dangerous tick was already in flight), and `Confirm` gained
  `OnCancel` because all three decline paths returned `(nil, nil)` and
  there was no signal to resume on.
- **The stale dialog was two rows shorter than its own output.** `MinSize`
  returned a flat 9 and `overlayRect` treats a plain Overlay's `MinSize`
  as its actual size, so the box got 7 content rows while `View` emitted
  9 — the bracketed button row and the key hint were both cut, leaving
  only the body's own plain-text option list. That is precisely the
  reported "three options render as plain unbracketed text with no focus
  indicator". Fixed with `PreferredSize`, which is what `Confirm` added
  for this identical defect on this identical axis.
- **"autosave stopped" never reached the status bar.** `armAutosave`
  returned at `!canSave()` *before* `saveDoc` — the only place that set
  `autosaveBlocked` — so a lock-held file was correctly never written and
  nothing said why.

### Round 3 — two reports, neither a product bug

- **Point "stopped refusing a fresh lock".** It had not. A `cli.py` lock
  is heartbeated by nothing, so it ages from the moment it is taken, and
  `go run` plus a person navigating a 389-entry tree routinely exceeds
  B7's 60 seconds — after which the editor's own acquire takes the stale
  row over **by design**. Measured: fresh row → `held by
  someone-else@other-id (1s)`; row aged 120s → `ok`, holder becomes the
  editor's. **A gate defect**, fixed with a background `HoldLock` job that
  heartbeats every 15s for the step's duration (verified: age sawtooths
  1–13s against a 60s window).
- **The FileSystemWatcher saw 0 events.** `_atomic_write` ends in
  `os.replace`, which on Windows fires `Deleted` + `Renamed` and **no
  `Changed` at all** — measured per NotifyFilter. The watcher was
  structurally incapable of seeing a save. Now watches `Renamed` with
  `NotifyFilters.FileName`; one replace fires exactly one, so the count
  is a count of saves.
- Two real UI bugs that **predate F13b**: arrow keys reached the tree
  while the buffer pane was drawn focused (F13a had `bufferKey` forward
  them), fixed by deriving `effectiveFocus`; and the empty state kept its
  own copy of the last note, which expired never, so the placeholder read
  `closed lf.py` permanently.

### Round 4 — opening a file no longer steals focus

Scope beyond F13b's doc, on Aryan's call: `clickTree` set `focusTree` and
`openPath` set it straight back. Both focus-stealing writes removed, and
`onFile` captures `keptByTree := w.effectiveFocus() == focusTree`
**before** the append — the one transition where the stored field changes
from ignored to authoritative. A derivation at that instant, not a flag
anyone has to maintain.

### Round 5 — the last gate failure was the gate's wording

Step 3c failed on "the edit is on disk". Traced rather than patched: the
take-over leaves dirty-tracking intact and the write carries the edit. The
instruction told the user to press `ctrl+o` in 3b *and again* in 3c, in
one continuous session — and `ctrl+o` is a toggle, so the second press
sent the marker to the tree. Measured: `after 3b's ctrl+o: focus=buffer,
edit landed` → `after 3c's ctrl+o: focus=tree, marker reached buffer?
false`. **Gate wording bug.** The step now warns against the second press,
and the assertion looks for a typed marker rather than a hash — because a
manual ctrl+s on a clean buffer still writes identical bytes, so a
successful save and a refused one are hash-indistinguishable.

---

## Bugs this episode's own fixes introduced, and caught

- **Pinning stdin fixed one problem and created a worse one** (became
  B33): an undecodable line killed the reader thread, so the backend went
  deaf and then hung with the single-instance lock held.
- **A stored focus field drifted immediately** — six tests caught it. A
  document arriving by any path that forgot to update it left the tree
  focused and swallowed every keystroke. Replaced with the derived
  `effectiveFocus`.
- **A regression test passed against its own reverted fix**, twice: the
  focus-ring assertion never checked the ring, and the pre-reveal clamp
  test had `reveal = true` so the post-reveal clamp covered it.
- **One change was removed for being unobservable.** Having `ctrl+o`
  toggle from `effectiveFocus` rather than the stored field could not be
  given a failing test in any sequence, so it was backed out with the
  reasoning recorded rather than kept with a test that could not fail.

---

## Still owed

- **`envelope_test.go` gained `EvProjectFileWritten`**, as B32's handoff
  asked. Done.
- **The unified diff is prefix/suffix based**, so two widely separated
  edits report as one hunk spanning both. Never wrong, sometimes coarse.
  F9b owns the real diff; this should call it once that exists rather
  than keeping a second implementation.
- **`lockStaleAfter` is a second copy of B7's `LOCK_STALE_AFTER_S`.** It
  only decides whether to *offer* take-over in the status bar; the
  backend is the only enforcer, so a drift shifts the offer, not the rule.
- **An autosave that hits `stale` does not open the modal** — it says one
  line and stops autosaving that file until `ctrl+s`. Not in F13b's doc;
  a modal appearing a second after you stop typing, for someone else's
  change, is how people turn autosave off.
- **`on_focus_change` means the active DOCUMENT changing**, not the
  Chat/Editor switch and not the buffer/tree pane toggle — `ctrl+o` would
  otherwise be a save key.
- **`[overwrite]` has unit coverage but no end-to-end gate step.**
  `TestOverwriteIsOnlyEverSentFromTheOverwriteButton` and
  `TestNoOtherCallerPassesOverwriteTrue` pin that it reaches the wire
  from exactly one message; the gate never presses the button, so the
  overwritten bytes are not proved on disk.

## What the next episode inherits

- **F14 (Terminal)** — `Compute` already hands the Editor a 3-row
  `Secondary` rect; it currently holds a titled box naming F14. `f8` is
  the next free function key (f5 re-index, f6 take over, f7 close).
- **F9b (Diff Card)** — `doc.ApplyRegion` is still the entry point, and
  the save path it applies into now exists.
- **F15 (Explainer)** — needs F13a's buffer and F9b's card; it writes
  nothing to disk, so F13b is a scheduling prerequisite only.
- **F16 (Polish)** now carries this episode's deferred items — see
  "Carried forward from F13b" there.

---

## Patched post-close

**Two regressions in this episode's own save/autosave behaviour, found by
manual re-testing after B33 and F16 landed, and fixed without reopening
the episode. Both are races against something the user does with the
hands — a workspace switch, and a dialog — which is exactly why
`gate_f13b.ps1` caught neither: the gate drives one thing at a time and
waits.**

> Not a new episode, and deliberately so. Neither changes a Done-when,
> neither adds surface, and both are defects in what this doc already
> claims. The record belongs where the claim is.

### Regression 1 — a save wedged across the idle-unload skip

**Root cause, and it is one layer up from the editor.** Root routes
`protocol.EnvelopeMsg` to `r.current()` and nowhere else (`root.go`'s
`EnvelopeMsg` case). So:

1. autosave starts a write and sets `doc.saving` (`save.go`);
2. the user switches to Chat inside the one-second debounce;
3. `EvProjectFileWritten` arrives, is delivered to Chat, which has no
   matching `corr_id`, and is dropped;
4. `doc.saving` is cleared ONLY from `onWritten`/`onError`, so it stays
   true for ever.

Every symptom follows from that one stuck flag: the status bar pinned on
`saving…` (`status.go`), the dirty marker never clearing because nothing
cleared it, and — the part that makes it more than cosmetic —
`saveDoc`'s "a write is already in flight" guard then refusing **every**
later write, autosave and `ctrl+s` alike. **The file became permanently
un-saveable, and the only thing that put it there was changing workspace
at the wrong instant.**

The idle-unload skip is in the repro but is **not** the bug and is
correct behaviour: F13a taught the sweep to leave a dirty workspace
loaded rather than discard an unsaved buffer, and the
`Editor stays loaded — N file(s) have unsaved changes` notice is how
that becomes visible. The skip is what keeps the Editor backgrounded for
long enough that the reply certainly arrives while something else is
active.

**Recovering on the way back in was considered and rejected**, which is
worth recording because it is the obvious fix: once the reply is gone
this side cannot tell whether the write landed, and both assumptions are
wrong. Re-sending with the old `expected_mtime` draws a `stale` refusal
if it DID land; clearing the flag and trusting the buffer keeps an mtime
that no longer matches the file, which is autosave rule 2 broken in the
quietest possible way. The reply is the only thing that knows, so the
fix is to stop throwing it away.

**The fix.** A narrow `app.BackgroundReplies` interface
(`contribute.go`), following the `DirtyBuffers`/`LockHolder` pattern this
episode already established — Root asks "do you need this", never "are
you the editor". `Root.deliverBackgroundReply` (`root.go`) runs AFTER
`r.current().Update` so the active workspace keeps first claim, skips
`r.active` so nothing is delivered twice, and hands the envelope to every
loaded workspace that answers yes. The Editor's `WantsBackgroundReplies`
(`autosave.go`) is true exactly while a write is outstanding, derived by
scanning the open documents rather than kept as a counter — a counter has
two paths that must agree for ever, including `protocol.go`'s Error path,
which is the one that would be forgotten.

**The returned `tea.Cmd` is dropped on purpose.** What a write reply
produces is `say()`, which becomes a `WorkspaceStatusMsg` — and that
message carries no workspace id, so Root would print `saved main.go` over
the status bar of whatever the user is actually looking at. A background
workspace may reconcile its own state; it may not take the status line.

### Regression 2 — autosave racing UI-13's unload confirm

**Root cause.** `requestUnload` (`lifecycle.go`) opened its Confirm with
no pause and no `OnCancel`. The dialog asks whether these buffers
survive, and it is open far longer than the one-second debounce, so a
tick armed by the keystroke that made the file dirty landed while the
user was still reading it and wrote the edits — making **Unload discard a
buffer that was already on disk, and Cancel indistinguishable from it.**

This is round 2's finding one dialog over. `confirmCloseDirty`
(`tabs.go`) already fixed exactly this for the editor's OWN close
confirm; UI-13's dialog is opened by Root and so had no way to ask for
the same pause.

**The fix.** An `app.AutosavePauser` interface (`contribute.go`).
`requestUnload` pauses before opening and attaches
`OnCancel` → `unloadCancelledMsg` → `ResumeAutosave`; `OnCancel` covers
all three decline paths (button, esc, click outside), which is the signal
`Confirm` gained in round 2 for precisely this shape of bug.

Editor side (`autosave.go`, `editor.go`): a workspace-level
`autosavePaused` flag plus a per-document **generation bump**. Both halves
are required, and the reason is round 2's restated — the flag stops a NEW
tick being scheduled, and the bump retires the one already in flight,
because `armAutosave` is consulted when a timer is SET and the dangerous
tick was set a moment ago by the keystroke that made the file dirty.
`ResumeAutosave` re-arms every still-dirty document rather than waiting
for the next keystroke, or a cancelled dialog would leave autosave
looking dead until one more character was typed.

**`autosavePaused` is deliberately NOT `doc.autosaveBlocked`.** That flag
is a document's own refusal — read-only, held by another holder — and must
survive a resume untouched; folding the two together is the obvious
simplification and would silently re-arm autosave on a file the backend
has already refused, once per cancelled dialog. There is a test pinning
that. It is workspace-level rather than per-document, which is **not** the
C5 trap: C5 is about TIMERS and per-entity deadlines, and this sets no
deadline for any file — the dialog counts every dirty buffer in the
workspace, so the pause is workspace-wide too.

**Unload supersedes the pending save.** That was the open half of the
decision and it is the same call C3 makes one dialog over: a confirm that
discards must not quietly write first, or the button does not mean what it
says. No resume on the confirmed path — the workspace is gone, and
`docByPath` then finds nothing for any tick still outstanding.

### How it was verified

`backend\tests\gate_f13b_hotfix.ps1` (NEW) — two scenarios only, Step A
and Step B, in a scratch `APPDATA`/`LOCALAPPDATA` profile with
`idle_unload_minutes = 1`, per CLAUDE.md's env-var-override rule. It is
**not** a replacement for `gate_f13b.ps1` and re-verifies none of F13's
Done-whens; it exists because both bugs need two things happening at once.
0 parse errors, UTF-8-with-BOM, ASCII-only, LF — and both existing gates
still parse at 0 afterwards.

**11 new unit tests**, in `editor\hotfix_test.go` and `app\hotfix_test.go`.
The suite went **1,236 → 1,247, 0 failed**; `go build ./...`, `gofmt -l .`
and `go vet ./frontend/...` clean.

**Every fix was shown to fail against its own reverted code**, this
episode's own standing rule:

```
drop r.deliverBackgroundReply      -> "received 0 envelopes, want 1 -- the
                                      document is wedged"
drop requestUnload's PauseAutosave -> "PauseAutosave called 0 times, want 1
                                      -- autosave is racing the dialog"
drop armAutosave's paused check    -> "an edit scheduled a new autosave while
                                      the dialog was open"
```

**Confirmed on the real application by manual spot-check**, four scenarios
beyond the two the gate drives:

| scenario | result |
|---|---|
| normal-path autosave, no race at all | saves and clears exactly as before — the fix is inert when nothing is backgrounded |
| the race, watching Chat's status bar | no leak: the background Editor reconciles silently and Chat's status line is untouched |
| two documents with writes in flight at once | no cross-talk — each reply lands on its own document, so C5's per-document isolation holds through the background path too |
| repeated idle cycles | the `stays loaded` notice fires once per period, not once per tick — the `dirtyNoticed` latch still behaves |

### Done-when 8 — confirmed intact, not re-investigated

Retested independently on the real application: edit the file externally in
another editor, then `ctrl+s` on a buffer that is **locally clean but now
stale on disk**. The `[reload] [overwrite] [diff]` dialog fired correctly.

Recorded as **confirmed intact**. Nothing about it was changed or
re-derived by either fix; this is a spot-check that it still holds, not a
reopening of the Done-when.

> Worth naming what that check also makes visible, because it is now its
> own episode: the stale check runs ONLY when something tries to write, so
> a file changed externally while Point's copy is locally clean shows no
> signal at all until a save is attempted. That gap is **F20** — see
> `../../tasks/frontend/F20-live-stale-detection.md`. It is a new TRIGGER
> into this episode's refusal machinery, not a second mechanism, and it
> does not reopen Done-when 8.

### Still open, and deliberately untouched by this patch

Both were found while fixing the above and both are outside its scope.

**This project tracks open frontend follow-ups in
`../../tasks/frontend/F16-polish.md`**, which already carries this
episode's earlier deferred items under "Carried forward from F13b" — so
that is where these two now live. **No new tracking file was created.**
(The `Parking lot` in `../../tasks/frontend/INDEX.md` is the wrong home:
CLAUDE.md is explicit that a parked item is deferred *by design* and is
not work, and both of these are defects.)

- **Lock heartbeats stop while the Editor is backgrounded.**
  `heartbeatTickMsg` goes through the same active-workspace-only routing
  that caused regression 1, so sitting in Chat for more than B7's 60
  seconds lets this process's own locks go stale and become
  take-over-able by anything — including Point's own next acquire. Not
  fixed here because the write reply is the case that WEDGES a document
  while an expired heartbeat merely expires, and because extending
  `BackgroundReplies` to cover heartbeats means first deciding whether a
  backgrounded workspace should hold locks at all. That is a question
  about the lock lifecycle, not about this bug.
- **`tabs.go:264`'s `if d.autosaveBlocked` is always true.** Line 258 of
  the same function sets it two lines earlier, so the dirty-close dialog
  unconditionally appends "Autosave is stopped for this file — see the
  status bar", including when it is not. Cosmetic, pre-dates this patch,
  and a one-line fix that belongs with the other polish items rather than
  inside an unrelated hotfix.

### Documentation changed alongside the code

Three files moved in the same working tree and were not accounted for in
this patch's first report. Recorded here rather than left for
`git diff --stat` to raise:

- **`design/tasks/backend/INDEX.md`** — a row for **B35**, a new backend
  episode found by a repo-wide verification pass (`CLAUDE.md` has been
  mojibake'd since `790e0c2`, plus two stray UTF-16LE files committed in
  that same commit). B34's row also gained a "run after B35" note.
- **`design/WORKFLOW.md`** — a `SESSION 18g` block for B35, placed above
  18f per this file's own rule that position, not letter, is the order,
  with that reasoning written into both blocks.
- **`design/model_usage.md`** — an 18g row, Sonnet high.

None of the three touches frontend code or any claim this episode makes.
They are listed because they were in the same diff, not because they are
related.
