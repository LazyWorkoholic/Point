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
| F9 | Task Tray, Cards, Diff Card | P1 | no | F7, F8 | B11, B12 |
| F10 | Settings, Providers, First Run | P1 | no | F7 | B6 |
| F11 | Memory Popup | P2 | no | F7 | B13 |
| F12 | Workspace Manager + Lifecycle | P1 | no | F6 | B15 |
| F13 | Editor Core | P1 | no | F12 | B7 |
| F14 | Editor Terminal | P2 | no | F13 | — |
| F15 | Filler-Explainer Integration | P1 | no | F13, F9 | B11 |
| F16 | Polish | P2 | no | all | — |
| F17 | Web Preview | P2 | no | F14 | — |

**F1–F5 can start today** — they need only B1–B5, which are built.

**Ordering:** F1–F5 are strictly serial. F7 before F6 (the right pane's expanded
context card is a popup). F12 needs F6. F13–F15 are an independent block.

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
