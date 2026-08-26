# F1 — Bootstrap, Layout Engine, Action Registry, Theme

**Phase:** P1 · **Batchable:** yes (with F2) · **Depends on:** — · **Backend:** none

## Purpose

The four foundations everything else plugs into. Deliberately produces **no
user-visible feature** — it produces the structure that makes every later episode
one file instead of four. Expect the screen to look empty at the end. That is
correct.

## Build

### Package layout
```
frontend\cmd\point\main.go           entry point (create)
frontend\internal\app\               root model
frontend\internal\layout\            Compute + Rect + breakpoints
frontend\internal\actions\           registry
frontend\internal\theme\             roles + four palettes
frontend\internal\array\             EXISTING item.go, menu.go — leave alone
```
Import prefix: `github.com/lazyworkoholic/point/frontend/internal/...`

### Root model
```go
type Root struct {
    active     WorkspaceID
    workspaces map[WorkspaceID]Workspace   // absent = NOT_LOADED
    suspended  map[WorkspaceID][]byte
    overlay    Overlay // nil when none
    layout     layout.Layout
    theme      theme.Theme
    actions    *actions.Registry
    w, h       int
}
```
Two rules that keep this from rotting: **the root never reads a workspace's
internal fields**, and **workspaces never talk to each other** — they emit
messages the root routes.

`Workspace` interface as in `project_idea.md` §8 (Init / Update / View / Suspend /
Resume / ID). Ship one stub workspace rendering a placeholder so the shell runs.

### Layout engine
`Compute(w, h, State) Layout` exactly as `LAYOUT.md` specifies. Pure — no globals,
no terminal calls. Called on `tea.WindowSizeMsg` and whenever a `State` flag
changes.

### Action registry
```go
type Action struct {
    ID, Name, Description, Category string
    Leader string      // "we" → leader then e
    Chord  string      // "ctrl+p"; most actions have none
    When   func(*Root) bool
    Run    func(*Root) tea.Cmd
}
```
- One registration point; palette, leader hints, help and rebinding all read it.
- **Startup validation: no duplicate IDs, no duplicate bindings.** Fail loudly at
  boot, not silently at keypress.
- Seed the minimum: `app.quit`, `app.palette`, `ui.help`, `ui.redraw`,
  `pane.focus.next`.
- Leader is `ctrl+w`; a leader-pending state shows a hint bar after a short delay.

### Theme
Load `THEME.md`'s four palettes as data. `background = transparent|dark|light`,
default transparent. 256-colour fallback, `NO_COLOR` honoured. Every style helper
takes a role, never a literal.

### Terminal setup
- Alt screen; mouse enabled (`tea.WithMouseCellMotion()`) with a palette toggle.
- **Bracketed paste handled** — a multi-line paste is one event, never N key
  events.
- Graceful restore on exit **and on panic** (deferred restore), or a crash leaves
  the terminal broken and you have to close the tab.

### Config loading
Read `%APPDATA%\Point\config.toml` (never write it). Read/write
`%LOCALAPPDATA%\Point\state.json`. Missing config is normal — defaults apply and
nothing crashes.

## Do NOT

- Build any workspace, viewport, or input (F2–F4).
- Rewrite `frontend\internal\array\` — F7 folds it into the shared Popup.
- Persist workspace state (F12).
- Add a component that decides its own visibility.

## Done when

1. `Compute` has a table-driven test covering every breakpoint in `LAYOUT.md`,
   passing **with no terminal**.
2. Duplicate action IDs or bindings fail at startup with a message naming both.
3. Switching theme in config changes every colour; grepping the UI packages for
   hex literals returns nothing outside `theme\`.
4. Resizing from wide to 45×10 and back produces, in order: full → narrower right
   pane → right pane hidden → single column → "too narrow" → and correctly back,
   with no panic and no artefacts.
5. `NO_COLOR=1` renders readable plain text with borders intact.
6. Pasting 30 lines into the placeholder produces one paste event.
7. Quitting restores the terminal; a forced panic also restores it.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go build ./... ; go test ./frontend/internal/layout/...
```
**Expect:** green, no terminal needed. This is the cheapest safety net you have.

```powershell
go run .\frontend\cmd\point
```
Drag the Windows Terminal window narrow and wide.
**Expect:** the breakpoint sequence above, smoothly, no artefacts.

```powershell
Select-String -Path .\frontend\internal\*\*.go -Pattern "#[0-9A-Fa-f]{6}" |
  Where-Object { $_.Path -notlike "*\theme\*" }
```
**Expect:** no matches.

```powershell
$env:NO_COLOR = "1" ; go run .\frontend\cmd\point ; Remove-Item Env:\NO_COLOR
```
**Expect:** readable plain text, borders intact.

```powershell
# panic restore: add a temporary panic behind a key, press it, then:
go run .\frontend\cmd\point
```
**Expect:** the terminal still works normally afterwards — no invisible cursor,
no stuck alt screen.
**Fails if:** you have to close the tab.

## Handoff

State after this episode: a running shell with a placeholder workspace, a tested
layout function, a validated action registry, and four working themes. Nothing
talks to the backend yet.

Record: anything about `frontend\internal\array\menu.go` you would change when F7
folds it in.

Do not proceed until every check above passes.
