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
| F9b | Diff Card | P1 | no | F9a, F13 | B24 |
| F9c | Result Card Attach + Save | P1 | no | F9a | B13 + a B12 attachment-source ext (unspecced) |
| F10 | Provider Chooser, Credentials, Providers+Appearance Tabs (F10, narrowed) | P1 | no | F7 | B6, B25 |
| F10b | Settings — Behaviour Overrides + `doctor` in About | P1 | no | F10 | a not-yet-scoped backend episode (runtime override layer + a `doctor` wire event) |
| F10c | Settings — Demo Mode | P1 | no | F10 | a not-yet-scoped backend episode (new provider type + fixture library + sample project) |
| F11 | Memory Popup | P2 | no | F7 | B13 |
| F12 | Workspace Manager + Lifecycle | P1 | no | F6 | B15 |
| F13 | Editor Core | P1 | no | F12 | B7 |
| F14 | Editor Terminal | P2 | no | F13 | — |
| F15 | Filler-Explainer Integration | P1 | no | F13, F9b | B11, B24 |
| F16 | Polish | P2 | no | all | — | <!-- also owns F9a's deferred result-card code-block rendering: syntax colour + box frame, blocked on markdown-renderer packaging, see F16-polish.md -->
| F17 | Web Preview | P2 | no | F14 | — |
| F18 | windowed history | P1 | no | F5+B18 | B18 |

**F1–F5 can start today** — they need only B1–B5, which are built.

**F9 is split.** F9a (tray + result cards) ships against what exists today;
F9b needs F13's editor buffer and B24's parsed Filler proposal on the wire;
F9c needs B13 and an extension to `context_attachments.source_type`, which is
`file`-only today and has no episode yet. See WORKFLOW.md sessions 15, 17b,
18b and 23b, and `design/completed/frontend/F09a-task-tray-cards.md` for the
four blockers with their evidence.

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
context card is a popup). F12 needs F6. F13–F15 are an independent block.
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


## Rejected

| Item | Why |
|---|---|
| Real PTY terminal | `tea.ExecProcess` covers it; VT100 + ConPTY is weeks |
| CPU/RAM/GPU in the status panel | cross-platform pain, misleading numbers |
| Mouse-only affordances | breaks over SSH and for keyboard users |
| Per-element colour config | a named-palette set is enough |
