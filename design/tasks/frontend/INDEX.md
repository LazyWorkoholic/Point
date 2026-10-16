# Frontend — Episode Index

Read `../../project_idea.md` first, then `LAYOUT.md` and `THEME.md` as the
episode requires. `TEMPLATES.md` is where Aryan adds new UI specs.
Load **one** episode file per session.

## Environment

```
Go module     github.com/lazyworkoholic/point
Frontend pkgs github.com/lazyworkoholic/point/frontend/internal/...
Entry point   frontend\cmd\point\main.go          (F1 creates it)
Run           cd D:\Projects\Point ; go run .\frontend\cmd\point
Test          go test ./frontend/...
Backend spawn working dir D:\Projects\Point\backend, command `uv run python main.py`
```

`frontend\internal\array\` already contains `item.go` and `menu.go` — a
hand-written scrollable, filterable list. **F7 folds it into the shared `Popup`.**
Do not write a second list component.

All printed commands must be **PowerShell**.

## Status

| # | Episode | Phase | Batchable | Depends on | Backend needed |
|---|---|---|---|---|---|
| F1 | Bootstrap, Layout, Registry, Theme | P1 | yes (with F2) | — | none |
| F2 | Backend Client | P1 | yes | F1 | B1–B5 ✅ |
| F3 | Chat Viewport | P1 | no | F2 | B2, B3 ✅ |
| F4 | Query Input | P1 | no | F3 | — |
| F5 | Streaming, Queue, Cancel | P1 | no | F4 | B5 ✅, B12 |
| F6 | Right Pane | P1 | no | F5, F7 | B8, B12 |
| F7 | Popup Framework | P1 | no | F1 | — |
| F8 | @ Picker + Attachments | P1 | no | F7 | B7, B12 |
| F9a | Task Tray + Result Cards | P1 | no | F7, F8 | B11, B12 |
| F9b | Diff Card | P1 | no | F9a, F13a | B24 |
| F9c | Result Card Attach + Save | P1 | no | F9a | B13 + a B12 attachment-source ext (unspecced) |
| F10 | Provider Chooser, Credentials, Providers+Appearance Tabs (F10, narrowed) | P1 | no | F7 | B6, B25 |
| F10b | Settings — Behaviour Overrides + `doctor` in About | P1 | no | F10 | a not-yet-scoped backend episode (runtime override layer + a `doctor` wire event) |
| F10c | Settings — Demo Mode | P1 | no | F10 | B28 ✅ (provider + fixtures + sample project), B30 ✅ (project binding via `SetSessionProvider`, found mid-episode) |
| F11 | Memory Popup | P2 | no | F7 | B13 |
| F12 | Workspace Manager + Lifecycle | P1 | no | F6 | B15 |
| **F13a** | **Editor Core — the editing half (F13, SPLIT)** | P1 | no | F12 | B7, B32 | <!-- F13a-editor-core-editing.md; DONE, recorded in completed/frontend/F13a-editor-core-editing.md -->
| F13b | Editor — Save, Locks, Autosave (F13, SPLIT) | P1 | no | **F13a (done)** | B7, B32 |
| F14 | Editor Terminal | P2 | no | F13a | — |
| F15 | Filler-Explainer Integration | P1 | no | F13a, F9b | B11, B24 |
| F16 | Polish | P2 | no | all | — | <!-- also owns F9a's deferred result-card code-block rendering: syntax colour + box frame, blocked on markdown-renderer packaging, see F16-polish.md -->
| F17 | Web Preview | P2 | no | F14 | — |
| F18 | windowed history | P1 | no | F5+B18 | B18 |
| F19 | Editor keymap docs, one-key close, scroll bound | P1 | no | F13a, F13b | — |

**F1–F5 can start today** — they need only B1–B5, which are built.

**F9 is split.** F9a (tray + result cards) ships against what exists today;
F9b needs F13a's editor buffer (now built — `doc.ApplyRegion` is its entry
point, one splice and one undo step, deliberately with no caller) and B24's
parsed Filler proposal on the wire;
F9c needs B13 and an extension to `context_attachments.source_type`, which is
`file`-only today and has no episode yet. See WORKFLOW.md sessions 15, 17b,
18d and 23b, and `design/completed/frontend/F09a-task-tray-cards.md` for the
four blockers with their evidence.

**F13 is split, and unlike F9 and F10 it split MID-BUILD.** Its own task doc
sanctioned it ("The largest frontend episode. It may take two sessions — if so,
stop at a working point, record what is done, `/clear`, and resume with a
note."). F13a is the editing half — buffer, cursor, selection, indent, undo,
tab strip, file tree, highlighting, status bar — and is **done and verified**:
Done-when 3, 4, 5, 6 and the open half of 1. F13b is the save half: Done-when
2, 7, 8, 9, 10, 11, 12. The seam is the save path, which is exactly what needs
the holder key, the lock lifecycle and three overlays that do not exist yet.
**Both halves have a task doc**, `F13a-editor-core-editing.md` and
`F13b-editor-save-locks.md`, each carrying its requirements verbatim from
the combined doc rather than re-derived; `F13a`'s is retroactive, written
after the session it describes, because the split happened mid-build. The
combined `F13-editor-core.md` was retired once both existed — see
WORKFLOW.md's "Splitting an episode".

**F10 is split, the same shape as F9, stopped before any code was written.**
Session 16 started against F10's full spec (`F10-settings-first-run.md`) and
stopped once three of its assumptions turned out not to hold:

1. **A validated key does not survive a restart.** `SetProviderApiKey`
   (`backend\runtime\runtime.py:544`) only registers a provider in memory;
   `appconfig.save_api_key()` (`backend\appconfig.py:226`, which does the
   temp-file-then-rename write F10 describes) has no caller anywhere in the
   backend — its own docstring says so. **B25** closes this one, narrowly,
   with its own restart-across-process verification gate. F10 (narrowed,
   this row) cannot honestly claim Done-when #4–#6 until B25 lands.
2. **`point doctor` has no wire event.** It is a separate CLI subcommand
   (`backend\cli.py:1194`) that prints straight to stdout — a different
   process invocation, not an event over the JSON-lines protocol the
   running app actually speaks. The About tab's `point doctor` action has
   nothing to call. Deferred to **F10b**, behind a backend episode that does
   not exist yet.
3. **The Behaviour tab's settings have no live-override path.** Queue
   depth, concurrency caps, `allow_outside_project`, `index.respect_
   gitignore`, `stale_after_days` are real `config.toml`-sourced values
   (e.g. `chat.queue_depth` read live via `appconfig.config_value()` in
   `backend\runtime\scheduler.py:269`), with no in-memory override layer and
   no wire event to push one, and the frontend's `state.json` is a Go-only
   file the backend process never reads. Given "Settings never writes
   `config.toml`," a Behaviour tab as specced would be cosmetic only.
   Deferred to **F10b** alongside the `doctor` gap, since both need the same
   kind of new backend surface (a runtime-override/reporting layer).
4. **Demo mode's fixture set assumes an apply path that does not exist.**
   The `@filler-explainer` fixture requires "an applicable diff" — but
   `design/completed/frontend/F09a-task-tray-cards.md` already established,
   with evidence, that there is no Editor workspace and no apply target
   anywhere in this repo yet (F13/F9b's job). Demo mode also needs a new
   provider type in the backend registry (distinct from the test-only
   `MockProvider`, `backend\providers\mock\provider.py:87`), a shipped
   sample project, and a real fixture library. Deferred to **F10c**, behind
   a backend episode that does not exist yet — there is no existing hook
   for it the way B15 or B24 are hooks for other deferred frontend work.

**This row (F10) is narrowed to:** the provider chooser (first run),
credential entry and validation, the no-provider state, and the
Providers + Appearance tabs of Settings — everything the wire protocol
already supports end to end once B25 lands. See WORKFLOW.md sessions 15c
(B25) and 16, and `design/tasks/backend/B25-provider-key-persistence.md`'s
Handoff for the full account.

**Ordering:** F1–F5 are strictly serial. F7 before F6 (the right pane's expanded
context card is a popup). F12 needs F6. F13a–F15 are an independent block,
and F13b is scheduled between F13a and F15 — **not** because F15 calls
the save path (it writes nothing to disk), but because F15 is the last
P1 episode and v1 is not feature-complete while F13 is still open.
F18 needs B18 and F5; must land before F12 (anchor lives in the Suspend blob).

## Cross-cutting rules — apply in every episode

1. **Components never hide themselves.** `Compute` decides; components render
   into the rect they are handed (`LAYOUT.md`).
2. **Components are data-driven.** A component renders a source it is given and
   never reaches into global state. Required for declarative layouts later;
   cannot be retrofitted cheaply.
3. **Every action is registered** in the action registry — palette, leader hints,
   help screen and rebinding all read from it.
4. **Every colour is a semantic role**, never a literal (`THEME.md`).
5. **Rounded borders everywhere**, with the padding in `LAYOUT.md`.
6. **UI state lives in the model**; persistence goes through `state.json` (F12).
7. **Nothing is mouse-only.**
8. **Display width, not `len()`**, for every truncation and padding calculation.
   Use `github.com/mattn/go-runewidth`. CJK and emoji are two cells; getting this
   wrong breaks every border in the app.

## Windows notes

- Test at **Windows Terminal** defaults and at least one small size. Windows
  Terminal supports truecolor and mouse; the legacy console host does not — if
  something looks wrong, check which one is running.
- `Ctrl+Alt+<letter>` is unreliable here. The leader key plus the palette is the
  supported path.
- Bracketed paste works in Windows Terminal; verify it, since a multi-line paste
  firing N sends is a real bug.
- Line endings: the editor detects on open and preserves on save (F13). Without
  it, `git status` shows every saved file as fully modified.

---

## Parking lot

- [ ] **Agent manager UI** (create / clone / edit / delete user agents, TOML
      export-import) — hook: `agents` table (B6), registry reads DB (B9).
      Anchor: after F10.
- [ ] **Capability workspaces** (Coding, Data) — hook: the `Capability`
      registration struct in `TEMPLATES.md`. Anchor: post-v1, evidence-gated.
- [ ] **Declarative TOML layouts, Tier 2** — hook: rule 2 above. Anchor: post-v1.
      Done when Chat and Editor can be expressed in it.
- [ ] **Editor split panes** — anchor: post-v1.
- [ ] **AI autocomplete layer** — syntax/rule layers are F13/F16; the AI layer is
      parked. Anchor: post-v1.
- [ ] **Dev-server supervision (web preview tier ③)** — framework auto-detection,
      restart policy, port sniffing, zombie cleanup, HMR error parsing into TUI
      diagnostics. Parked because it is a process supervisor that replaces typing
      `npm run dev` once per session. Hook: F17's action + config block.
      Anchor: after F17.
- [ ] **Plugin / scripting system** — REJECTED for v1 and v2 (D88). Go's `plugin`
      package does not work on Windows; RPC is too slow per keystroke. The five
      existing seams (action registry, tool registry, capability registry, TOML
      layouts, agent configs) cover customization for someone who owns the source.
      If ever wanted for other people: embedded Lua. Anchor: not scheduled.
- [ ] **Tier 2 auto-indent** (language rules table) — anchor: F16 if time.
- [ ] **Tree-sitter / incremental parsing** — anchor: post-v1, and the natural
      first C++ component in the Track-2 book.
- [ ] **File watcher** — a dependency and a platform difference; the save-time
      mtime check covers v1.
- [ ] **Shared-screen SSH attach** — anchor: post-v1.
- [ ] **LSP / code intelligence** — go-to-definition, diagnostics, hover, real
      completion. This is the largest genuine gap between Point's editor and a
      professional one (D89) — bigger than plugins. It re-imports exactly the cost
      that makes VS Code slow on large Python projects (D77), so the mitigation is
      mandatory if unparked: start language servers **lazily, per language, on
      first file open** — never at startup. Anchor: post-v1.

- [ ] **Oversized input rollover** — once the query area passes a threshold
      (~200 lines), the earlier portion automatically becomes an attached
      `.txt`/`.md` and the input continues empty, so a very long message never
      fills the visible input. The rolled-over portion stays accessible and can
      be folded back in. Applies to BOTH a large paste and a message typed past
      the threshold progressively — more ambitious than Claude/ChatGPT, which
      convert pastes only, not actively-typed text.

      The hard parts are all in the mid-typing case: cursor placement after a
      rollover, whether undo can reverse one, and what happens when the user
      edits a line that now lives in an attachment rather than the buffer. That
      is a redesign of the input's data model, not an addition to it.

      **Explicitly NOT a workaround for F4's paste ceiling.** A rollover at 200
      lines is only safe while the input's real ceiling stays above 200, so
      shipping it before that ceiling is fixed and understood would mean tuning
      a feature threshold to dodge a defect — and it would fire past the ceiling
      the moment conditions change (a different terminal, different content,
      F5's streaming load), losing data again with an extra layer of machinery
      hiding it.

      Hook: B12's attachment resolution (built) + F8's `@` picker UI (not built).
      Anchor: after F8, and only once F4's paste ceiling is fixed and understood.

- [ ] **`G` clears the transcript's entry focus, so `o` silently stops
      working on a task slot** — found during F18's verification, reported as
      a task-tray bug. It is **not** the tray's and **not** F9a's: in
      `app\popup.go`'s `handleKey`, every plain rune goes into the popup's
      FILTER (F07's own Done-when #5, so a typed `j` cannot read as
      "scroll"), so `n`/`o`/`j`/`k`/`G` never reach the tray at all. The keys
      in the repro are the CHAT TRANSCRIPT's browse-mode keys
      (`chat\scroll.go`'s `handleKeyString`), acting on a reloaded
      `@research` slot in the transcript — which looks enough like a tray row
      to be mistaken for one.

      Repro: with the input unfocused, `n`/`p` onto a research task slot; `o`
      opens and closes it correctly. Press `Shift+G`. Press `o` again — it
      does nothing. Only `p` then `n` restores it.

      Cause, one line: `case "G"` sets `w.pinned = true` **and
      `w.focus = -1`**. `focusedIndex()` then falls back to the newest entry
      (F3's convention, so `y` works without pressing a focus key first), and
      `o` resolves `liveAt()` against that entry instead — which is usually
      an ordinary reply with no slot, so the toggle is a silent no-op.
      `p` then `n` re-establishes an explicit focus, which is why it "fixes"
      it.

      **Pre-existing since F3**, not introduced by F18: `w.focus = -1` on `G`
      was added in commit `68a1cbc` ("F3: chat viewport") and is byte-identical
      at `96feddc` (the commit F9a closed on) — verified by
      `git show 96feddc:frontend/internal/chat/scroll.go`. F18 only appended
      `w.jumpToBottom()` after those two lines. F18 also confirmed the
      adjacent `j`/`k` spellings of the same symptom were its OWN and fixed
      them (see `design/completed/frontend/F18-windowed-history.md`, bugs
      4–6); this `G` one is the residue that is not.

      Not fixed in F18: out of that episode's Build section, and the fix is a
      genuine UX decision rather than a repair — should `G` keep the focus
      where it is (and then the highlight sits off screen), move the focus to
      the newest entry explicitly, or keep clearing it and have `o` say why it
      did nothing? `y`, `t` and `^R` all read the same fallback, so whichever
      answer is chosen governs four keys, not one.

      Owner: `chat\scroll.go`'s `handleKeyString`/`focusedIndex` (F3).
      Anchor: **F16 (polish)**, unless the chosen answer turns out to need its
      own episode.


- [x] **`go test ./frontend/...` wrote the user's real
      `%LOCALAPPDATA%\Point\state.json`** — found during F12's verification,
      **predates F12**, fixed in that session. **Not an F12 build item** and
      deliberately not recorded in F12's completed doc: F12 only happened to
      be the episode that looked at the file's timestamp.

      CLAUDE.md is explicit that nothing automated may touch `%APPDATA%\Point`
      or `%LOCALAPPDATA%\Point` outside a scratch profile, so this was a
      standing violation in the test suite rather than a near miss. It had
      already wiped `last_workspace` out of the live profile by the time it
      was noticed; the file was restored by hand from a backup taken during
      the same session.

      Confirmed on a **clean tree** before any fix: `git stash push -u`,
      record the real file's `LastWriteTime`, `go test
      ./frontend/internal/chat/`, mtime moved. So it was not F12's.

      Cause: `chat.New` defaults `w.saveStateFn` to the real
      `config.SaveState` (the seam exists for the reason `chat\draft.go`'s
      `persistDraft` doc gives), and `persistDraft` only declines when
      `w.sessionID` is empty. Any test that sets a session id — `setSession`,
      `onSessionCreated`, or a direct assignment — and then sends (`onSend`
      calls `clearDraft`) or types (`draftSaveMsg` → `saveDraftIfDue`) wrote
      the user's own file. Five sites substituted `saveStateFn`
      (`input_workspace_test.go`, `providers_test.go`); every other one did
      not, and did not need to for its own assertions to pass — which is
      exactly why nothing caught it.

      Fixed by a package-level `TestMain` guard in **both**
      `frontend\internal\chat` and `frontend\internal\app`
      (`redirectProfileToScratch`), pointing `APPDATA`, `LOCALAPPDATA`,
      `XDG_CONFIG_HOME` and `XDG_STATE_HOME` at an `os.MkdirTemp` directory
      for the whole package run, removed between `m.Run()` and `os.Exit`.
      It self-verifies — if `ConfigDir`/`StateDir` ever stop resolving from
      those variables the guard refuses to run the suite instead of quietly
      guarding nothing — and `TestTheProfileIsRedirectedAwayFromTheRealOne`
      fails outright if the redirect is removed, which an mtime check done
      once by hand cannot do. Proven by reverting the redirect and watching
      that test fail, then by two consecutive full `go test ./frontend/...`
      runs leaving the real file's mtime **and SHA256** identical.

      `app` was guarded too even though nothing there reaches the real file
      today: `saveWorkspaceStateIfDue` declines when no workspaces are
      registered, which every current test satisfies, but that safety rests
      on every workspace-registering fixture remembering to substitute a
      recorder — the exact shape of the assumption that failed in `chat`.

      **The production default of `saveStateFn`/`SaveWorkspaceStateFn` was
      deliberately NOT changed.** The running app absolutely should write the
      user's `state.json`; making the shipped path safer for tests would be
      making the product worse to make the suite better. The test
      environment was redirected, not the code path.


- [ ] **No transcript presence for a task that is running while its view is
      reloaded** -- found during F12's verification gate, diagnosed there,
      **deliberately not fixed in F12**. Aryan's call, after the diagnosis
      came back: do not lift `orphanReply`'s decline, and do not force a
      rushed static marker in either.

      Repro: unload a workspace with `\research` running, reload it BEFORE
      the task finishes. The right pane and F9a's tray track the task
      correctly throughout -- that is F12's whole demo beat and it works --
      but the chat transcript shows the question with no reply and no slot.
      The inline entry appears only once the task has completed and the
      session is reloaded again, and that is the ordinary paired path (a
      finished `\research` has a real assistant row), not this one.

      **F12's resume path is not at fault**, and that is pinned by test
      rather than asserted: `TestReloadMidTaskReachesTheJoinAndOrphanReply
      Declines` and `TestTheSameJoinInsertsASlotOnceTheTaskIsTerminal` in
      `chat\history_test.go`. A resumed session runs F18's chain exactly
      (`setSession` -> `rebuildWindow` -> `pairReloaded` ->
      `decorateReloaded` -> `applyTaskRows`), the window reaches the tail so
      the join is not short-circuited, and it reaches the unanswered
      question with the running tasks row in hand. `orphanReply` then
      declines because the task is non-terminal.

      **Why a static "-> running elsewhere" marker was investigated and
      rejected.** It looked cheap -- source it from the tasks table's status
      column only, no event stream, no clock, nothing that could stick. It
      is not, and the blocker is not the renderer:

      - `requestTaskRows` fires **exactly once**, from `setSession`
        (`chat\protocol.go`). Nothing re-fires it -- not a reconnect, not a
        task lifecycle event.
      - `entryFor` (`chat\stream.go`) resolves task envelopes against
        `w.live` **only**, by corr id and by task id. A reloaded task has no
        live entry, so its own `TaskDone` returns nil from
        `handleStreamEvent` and is dropped before anything can see it.

      So a marker set at load would say "running elsewhere" for the whole
      life of the session, however long ago the task actually ended. A
      permanent lie is worse than a missing footer, and this codebase
      already has a standing rule against a hint that lies (F04's Bug 12).
      Making it honest needs a correction path, and both available ones are
      real work rather than a marker:

      1. Re-fire `requestTaskRows` on task lifecycle -- but those envelopes
         are dropped by `entryFor` first, so this means adding a branch to
         the live event router for task events with no live entry. That is
         the live/streaming machinery, which was the explicit line not to
         cross.
      2. Drive it off Root's `DiagnosticsMsg.Tasks`, which IS kept current.
         But `refreshTasks` is gated on `tasksOnScreen()` (F6's Done-when
         #6, which `chat\diagnostics_race_test.go` asserts directly), so
         with the right pane hidden it goes stale again -- and `w.tasks` and
         `w.taskRows` are deliberately two different things with two
         different lifetimes. Merging them changes F18's "`w.all` has
         exactly one writer path" invariant.

      Also corrected while here: `orphanReply`'s own doc gave the WRONG
      reason for the decline (`Streaming()` kept true via `w.hist`).
      `Streaming()` and `tickInterval` scan `w.live` only, so that never
      applied; the reasoning had been transplanted from `newUserEntry`,
      where it does. The conclusion was right for two other reasons, now
      written down in that doc -- a wrong reason beside a right conclusion
      is the worst thing to leave for whoever picks this up.

      Owner: whoever gives the frontend a way to learn task lifecycle for a
      task it holds no live entry for. Root already has that capability at
      the tray level (F9a); the transcript does not.
      Anchor: **F16 (polish)**, unless the chosen answer needs the new
      refresh cycle above, in which case it wants its own episode -- the
      same hedge as the `G`-clears-the-focus entry.


- [ ] **A scrolled-up resume lands above content that arrived while the view
      was unloaded** -- found on F12's second gate run, reported as "the
      transcript does NOT show the research task's row on this reload".
      Diagnosed, **deliberately not fixed in F12**: the cheap fix needs
      information F18's blob shape deliberately does not carry, and that
      shape was fixed by prior decision.

      Repro (the gate's own step 2): `\research <topic>`, wait for Running,
      scroll the transcript up a screen or two, `^W u` to unload. The task
      completes in the background -- tray and `cli.py tasks list` both say
      Completed. `^W w` -> Chat to reload. The research row is not on screen.
      It appears after a scroll, a `G`, or a reload that happened to be
      pinned, which is the "further action" the report could not pin down.

      **The row is not missing.** It is in `w.all`, fully decorated, with
      `laneTask` and a terminal status, from the first frame and with no
      ListTasks reply involved at all. What is wrong is the VIEWPORT:
      `applyResumeAnchor`'s non-pinned branch restores the reading position
      from the blob, which on this sequence is above the answer, so the row
      is ~40 entries below the fold. Pinned resumes are correct -- F18's
      `PinnedToBottom` is "the newest message, resolved at Resume time",
      written for exactly this case.

      Ruled out by test, not by argument (`chat\history_test.go`,
      `TestAfterUnloadAndCompletionTheReloadedRowAndSlotArePresent` plus the
      two beside it): `requestTaskRows` firing once from `setSession` before
      the terminal row is queryable -- that fixture has no client, so the
      request never even succeeds and the slot is still correct; an
      `entryFor` `w.live`/`w.hist` resolution gap -- the entry comes from
      `decorateReloaded`, off the message's own columns; and a
      `historyWindow`/`slotUI` re-keying gap -- the entry is rebuilt from
      `w.raw` each time and carries its lane and status. Also ruled out: the
      two fetch paths returning different shapes. `GetSession` and
      `SessionMessageList` both serialise through `Message.to_dict`
      (`backend\managers\message.py`), so `invocation` is present on both.

      **Attribution: F18 owns the policy, F12 made it reachable.**
      `applyResumeAnchor` arrived in `75aab79` ("F18: windowed history"), and
      `git grep` on HEAD finds **no callers of Suspend/Resume at all** -- F12
      added the only ones. Same shape as the `orphanReply` entry above.

      It is also arguably not a defect: F12's own Done-when #3 asks for
      "scroll position ... from the suspended bytes", and `!pinned` already
      makes `viewportBody` draw `↓ jump to latest (G)` in the corner. What
      that affordance cannot say is that something arrived WHILE YOU WERE
      GONE, which is the part that made a user look for a row and conclude
      it was absent.

      Candidate fixes and costs:

      1. Resume to the live edge when newer content exists than the blob
         knew about. Needs the session's newest-seq at suspend time, i.e. a
         fourth field on `blobAnchor` -- which is redefining F18's shape,
         explicitly ruled out.
      2. Say "N new below" instead of "jump to latest". Same missing field,
         same objection.
      3. Leave the position and make the existing affordance louder. No new
         state, purely presentational, and genuinely F16's.

      Anchor: **F16 (polish)** for option 3. Options 1 and 2 need the blob
      shape reopened, which is a decision above this episode.


## Rejected

| Item | Why |
|---|---|
| Real PTY terminal | `tea.ExecProcess` covers it; VT100 + ConPTY is weeks |
| CPU/RAM/GPU in the status panel | cross-platform pain, misleading numbers |
| Mouse-only affordances | breaks over SSH and for keyboard users |
| Per-element colour config | a named-palette set is enough |
