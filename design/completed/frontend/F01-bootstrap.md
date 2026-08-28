# Episode F1 — Bootstrap, Layout Engine, Action Registry, Theme ✅

**The four foundations, and no user-visible feature: a pure layout function, a
validated action registry, four palettes as data, and a shell that runs.**

Built and verified in one session together with F2 (`batchable: yes`). Where a
decision was shared between the two episodes it is recorded in whichever file
owns the code.

---

## `frontend\cmd\point\main.go` — entry point

Resolves everything before Bubble Tea starts, so a failure has somewhere to be
reported: directories, `config.toml`, `state.json`, the two log files, the
backend directory, the client, then the root. A duplicate action ID or binding
returns an error from `app.New` and the process exits **before** the alt screen
is entered — a boot failure you can read, not one painted over by a TUI.

Program options: `WithAltScreen()`, `WithMouseCellMotion()`. Bracketed paste is
left enabled but does not work on Windows — see F2's doc and
`frontend\internal\app\paste.go`.

`defer program.Kill()` covers a panic in `run()` itself. Bubble Tea already
catches panics inside its own loop and restores the terminal before re-panicking
(`WithoutCatchPanics` turns that off, and is not used).

## `frontend\internal\layout\` — the layout engine

`layout.go` (326 lines), `layout_test.go` (399 lines, 16 test functions).

**Pure. No globals, no I/O, no terminal calls.** This is the contract every
frontend episode from F3 onward renders into.

### The signature — exact

```go
func Compute(w, h int, s State) Layout
```

### `Layout` — every field

```go
type Layout struct {
    Header    Rect     // unboxed text line, height 1
    Main      Rect     // viewport (chat) / code file (editor)
    Secondary Rect     // query area (chat) / terminal (editor)
    RightPane []Rect   // ONE RECT PER CARD; heights are NOT even
    StatusBar Rect     // unboxed line, height 1

    ShowHeader    bool
    ShowRightPane bool
    ShowWatermark bool

    HeaderCompact bool // 24–29 rows: title-only, no animation, no trademark

    OverlaysAllowed bool // false below 70 columns

    TooSmall bool   // every rect is zero; Message is the sentence to print
    Message  string

    RightPaneWidth int // 0 when hidden; 32 at ≥120, 26 at 90–119

    W, H int // the terminal size this Layout was computed for
}

type Rect struct{ X, Y, W, H int }
func (r Rect) IsZero() bool   // W <= 0 || H <= 0 — "not drawn this frame"
```

### `State` — what Compute is told

```go
type State struct {
    Active        WorkspaceID  // "chat" | "editor"
    RightPaneOpen bool
    TreeOpen      bool
    TerminalMode  TermMode     // "small" | "overlay" | "full"
    OverlayOpen   bool
    HeaderAllowed bool
    RightCards    []CardSlot   // nil/empty ⇒ no right pane, whatever RightPaneOpen says
}

type CardSlot struct {
    MinHeight int  // rows this card wants, borders included
    Flex      bool // may absorb leftover rows; the LAST Flex card gets them
}
```

`WorkspaceID` lives in `layout`, not in `app`, because `Compute` genuinely
branches on which workspace is active. Putting it in `app` would make `layout`
import `app`, which imports `layout`. `app` aliases it: `type WorkspaceID =
layout.WorkspaceID`.

`CardSlot` is the F1-owned stopgap for a shape F6 formally owns (D97 precedent).
The note is written into `../../tasks/frontend/F06-right-pane.md` itself, not
only here, because F6's session will not read this file.

### Exported constants — the breakpoint table

```go
MinWidth = 50   MinHeight = 12
WideWidth = 120        RightPaneWide   = 32
MediumWidth = 90       RightPaneMedium = 26
NarrowWidth = 70   (= OverlayFloor)
HeaderFullHeight = 30  HeaderCompactHeight = 24
MinBoxHeight = 3
MarginX = 1  MarginY = 1  PaneGapX = 1  BoxGapY = 0
```

### Rules `Compute` enforces, that callers must not re-implement

- Width is checked before height, so a terminal that is both too narrow and too
  short reports **narrow** — deterministically.
- The right pane needs BOTH `RightPaneOpen` and a non-empty `RightCards`. An
  open pane with nothing in it is not a layout the app can produce.
- Card distribution: each card gets its `MinHeight`; leftover rows go to the
  **last** `Flex` card; when the pane is too short, **trailing cards drop to a
  zero rect** rather than every card being squeezed below `MinBoxHeight`.
- Closing the right pane reflows `Main` and `Secondary` to full width with no
  gap left behind, and returns an empty `RightPane` slice.
- `Compute` returns a fresh `RightPane` slice each call and never mutates
  `s.RightCards` — asserted by `TestComputeIsPure`.

## `frontend\internal\theme\` — roles and palettes

`theme.go` (191), `palettes.go` (155), `theme_test.go` (140), `main_test.go` (17).
10 test functions.

24 semantic roles; `palettes.go` is **the only file in the frontend containing a
colour literal**. `TestEveryPaletteDefinesEveryRole` fails if a palette is
missing a role *or* defines an extra one, which is what makes "adding a fifth
palette is a data change" true rather than hopeful.

```go
func New(paletteName string, bg Background) Theme
func (t Theme) Style(r Role) lipgloss.Style   // the ONLY way to get a foreground
func (t Theme) Fill(fg, bg Role) lipgloss.Style
func (t Theme) Box(border Role) lipgloss.Style
func (t Theme) Color(r Role) lipgloss.Color
func (t Theme) BackgroundColor() (lipgloss.Color, bool)  // false in transparent mode
```

An unknown palette name falls back to turquoise rather than failing; `main.go`
surfaces the fallback in the View Port so a typo is visible instead of silent.
An unknown `background` value falls back to transparent.

The two background fills (`#0F1214` dark, `#F7F7F5` light) are **values F1
picked** — `THEME.md` names the three modes but no colours for them. They live
in `theme.go`, so the no-hex-outside-theme rule still holds.

## `frontend\internal\actions\` — the registry

`registry.go` (221), `registry_test.go` (229), 13 test functions.

Generic over the context type: `Registry[T]` / `Action[T]`. The root uses
`Registry[*Root]`. **This is what keeps `actions` from importing `app`, which
imports `actions`** — the alternative was `any` plus a type assertion at every
call site.

Registration conflicts are **collected, not panicked**, so `Validate()` reports
all of them at once with both offenders named:

```
action registry has 2 conflict(s):
  · duplicate action ID "app.quit": already registered as "Quit" (Application), now again as "Exit" (Application)
  · duplicate chord "ctrl+p": bound to "app.palette" ("Command Palette") and "ui.print" ("Print")
```

Chords and leader sequences are **separate namespaces** — a leader `p` and a
chord `ctrl+p` do not collide, because you cannot type one where the other is
expected. A leader sequence that is a **prefix** of another is a conflict: the
shorter one can never fire.

Seeded actions (`frontend\internal\app\actions.go`): `app.quit` (`ctrl+c`,
`ctrl+w q`), `app.palette` (`ctrl+p`), `ui.help` (`ctrl+w ?`), `ui.redraw`
(`ctrl+l`), `pane.focus.next` (`tab`), and `backend.retry` (`ctrl+w r`) — the
last is F2's, because a failure banner with no way to act on it is a complaint.

## `frontend\internal\config\` — config.toml and state.json

`config.go` (139), `state.go` (109), `config_test.go` (225), 13 test functions.

Reads `%APPDATA%\Point\config.toml`, **never writes it**. Reads and writes
`%LOCALAPPDATA%\Point\state.json` with temp-file-then-rename.

Both paths read the same two environment variables Python's `appconfig.py`
reads, so **one pair of `APPDATA` / `LOCALAPPDATA` overrides moves both sides of
the app** — this is what makes the scratch-profile rule in `CLAUDE.md`
workable in a single script.

Config keys were **chosen here** — nothing in `design\` had named them:

```toml
[ui]
theme = "turquoise"      # turquoise | butterfly | green | gold
background = "transparent"  # transparent | dark | light
```

A UTF-8 BOM is stripped before parsing, matching `appconfig._strip_bom`. Notepad
and PowerShell 5.1's `Set-Content -Encoding utf8` both write one, and without
this the same file parses on the Python side and not the Go side.

Missing config → defaults, nil error. Broken config → defaults **plus the parse
error**, which `main.go` surfaces in the View Port. Same for `state.json`, and a
`version` from a future Point is ignored rather than half-read.

`State` deliberately holds almost nothing (`Version`, `LastWorkspace`).
Workspace state is F12's; what F1 owns is the versioned, atomic, missing-file-
tolerant read/write path itself.

## `frontend\internal\app\` — root model, rendering, placeholder

`root.go` (500), `render.go` (301), `box.go` (237), `placeholder.go` (318),
`paste.go` (222), `actions.go` (110), `overlay.go` (107), `workspace.go` (67).
32 test functions across `app_test.go` and `regression_test.go`.

### The `Workspace` interface — one deliberate change from `project_idea.md` §8

```go
type Workspace interface {
    Init() tea.Cmd
    Update(tea.Msg) (Workspace, tea.Cmd)
    View(layout.Layout) string        // §8 says View(Rect)
    Suspend() ([]byte, error)
    Resume([]byte) error
    ID() WorkspaceID
    Cards() []layout.CardSlot         // added: what this workspace wants in the right pane
}
```

`View` takes the whole `Layout`, not one `Rect`. A workspace owns `Main`,
`Secondary` **and** one rect per card; a single `Rect` cannot express that, and
handing it one would force it to derive the others — which is exactly the
authority `Compute` is supposed to hold. The two rules that keep this from
rotting are unchanged: the root never reads a workspace's internal fields, and
workspaces never talk to each other.

`Cards()` is how "one rect per card" stays a layout decision made from data the
workspace supplies, rather than from knowledge `Compute` should not have.

### `Overlay`

```go
type Overlay interface {
    MinSize() (w, h int)
    Title() string
    View(th theme.Theme, w, h int) string
    Update(tea.Msg) (Overlay, tea.Cmd)
}
```

F1 owns the interface, the fit check, the scrim and focus trapping. **F7 builds
the shared `Popup` on top of it.** `Root.OpenOverlay` refuses one that does not
fit and names the actual numbers: `Help needs 60×14 — terminal is 75×13`, or
`Help needs 60 columns — overlays are off below 70`.

The `th theme.Theme` parameter is not cosmetic — see the bug log below.

### Rendering

`box.go` draws the rounded border with the title on the top edge, because
lipgloss v1 has no notion of a border title and every box in Point has one.
Every width is a **display** width via `go-runewidth`; `padCells`,
`truncateCells` and `visibleWidth` all skip ANSI escapes so a styled string
still lands on the right column.

`render.go` composes: top margin, header, workspace body, status bar, bottom
margin — then pads or clips to exactly `h` rows, so a stale frame cannot leave
artefacts after a resize.

### The placeholder workspace

F1's spec says to expect an empty screen. What ships instead renders the
**connection**: state, handshake, capabilities, restart count, task lifecycle,
paste counters, theme, action count, config/state errors. Agreed with Aryan
before building.

Without it neither episode's gate is checkable — F2's verification asks for a
header showing connected, a banner on crash, and a task marked interrupted, and
none of those has a renderer until F3. It is data-driven from birth: the root
pushes a `Diagnostics` snapshot as a message; the placeholder holds no reference
to the root, the client, or any global.

---

## Layout / theme decisions made here

- **Breakpoints held exactly as `LAYOUT.md` specifies them.** Nothing needed
  raising. `LAYOUT.md` marks the numbers provisional and predicts they will need
  to go up once real content lands; at F1 there is no real content, so this is
  weak evidence. **F16 should re-check after F3–F6.**
- `HeaderCompact` and `OverlaysAllowed` are new `Layout` fields, not new
  breakpoints — they name bands `LAYOUT.md` already describes so that components
  do not re-derive them from `W`/`H`.
- Dark and light background fills are values F1 chose (above).
- Right-pane card heights used by the placeholder: Status `{MinHeight: 5}`, Task
  `{MinHeight: 8}`, Sessions `{MinHeight: 6, Flex: true}`. F6 owns the real
  numbers.

## Manual Test Log (F1)

Automated, no terminal required: **95 test functions across six packages**,
`go build ./... ; go vet ./... ; go test ./frontend/...` all green.

1. **Resize sweep, 200×50 → 45×10 → 200×50**, dragging the Windows Terminal
   window. Observed in order: full layout with a 32-column right pane → 26
   columns at 90–119 → pane hidden at 70–89 → single column below 70 →
   `terminal too narrow — 50 columns minimum` → and correctly back. No panic, no
   artefacts.
2. **Header bands at specific heights.** 30 rows: animation slot, `P O I N T`
   centred, trademark right. 24–29: title-only. 20 rows: header gone, dim
   `◈ point` bottom-right in the status row, costing no extra row.
3. **Every rendered line is exactly the terminal width** at 14 sizes from 200×50
   down to 45×10 (`TestEveryLineIsExactlyTheTerminalWidth`).
4. **Wide characters.** Box titles of `セッション一覧`, `🎉 tasks`, and 40× `長`
   all keep the right border on column `W`. Content of 20× `ありがとう` in a
   20-cell box truncates rather than pushing the wall.
5. **Palette switch** via `[ui] theme` in `config.toml`: turquoise, butterfly,
   green, gold each repaint every element. `chartreuse` falls back to turquoise
   and says so in the View Port.
6. **`NO_COLOR=1`**: readable plain text, borders intact, no escape sequences.
7. **`ctrl+w` then wait ~0.4 s**: hint bar appears, built from the registry.
   `ctrl+w z` clears it with `no action bound to ctrl+w z`. `esc` cancels.
8. **Overlay fit refusal** at 75×13 and at 60×30, each naming the actual numbers.
9. **Terminal restore**: quit with `ctrl+c`, cursor visible, no stuck alt screen.
10. **Hex-literal grep** outside `theme\`: no matches.

Terminal: Windows Terminal at defaults (~120×30) plus the sizes above.

## Bugs caught during manual testing

### 1. The Help overlay could not be themed at all

`helpOverlay.View` returned bare text, and the `Overlay` interface had **no
theme parameter**, so no overlay *could* be themed. Bare text renders in the
terminal's own default foreground — reported as "stays fixed blue regardless of
active theme". The border repainted correctly because borders are drawn by
`box()`, which does read roles, which is what made it look like a partial bug.

Fix: `View(th theme.Theme, w, h int)`. Help now uses `AccentPrimary` for
category headings, `TextPrimary` for names, `AccentSecondary` for bindings,
`TextDim` for descriptions.

**A component that returns unstyled text is not theme-neutral — it is hardcoded
to whatever the terminal happens to use.** Worth stating because it will not
look like a bug in review.

### 2. The overlay compositor corrupted rows it only partly covered

Colour corruption appeared on the View Port's left border and the Task card's
right border — both **outside** the overlay's bounds, on exactly the rows the
overlay spanned.

`overlayAt` did `stripANSI(base)`, `runewidth.Truncate(plain, x)`, then filled
everything after the panel with **spaces**. So on those rows the left segment
lost its colour and the right pane was blanked outright:

```
row 13: columns 100–140 changed
 before "     │ │                              │ "
  after "                                        "
```

Fix: `spliceRow` + `sliceCells` in `render.go`. The scrim already flattens the
frame to one dim colour, so everything behind the panel is composited as **plain
text**, cut by **visible column**, and re-styled once per segment — no escape
sequence exists while the cutting happens, so none can be severed. A
double-width rune straddling a boundary becomes spaces for the cells inside the
range only.

Bugs 1 and 2 are different defects with the same origin: **both treated styled
strings as if they were plain text.**

### 3. Column padding applied after styling

In the same file: `fmt.Sprintf("%-20s", styled)` counts escape bytes as width,
so the help columns lined up against invisible bytes. Padding now happens with
`padCells` **before** `Render`.

### 4. The restart counter ignored manual retries

`Client.Restarts()` was incremented only by the automatic restart path, so
pressing `ctrl+w r` started a real child process and the number on screen never
moved. Confirmed across three tests by Aryan.

Fix: derive it from `connects` (successful handshakes − 1). Any reconnect
counts. The 60-second auto-retry budget stays a separate `failures` slice —
`TestRestartCounterIsNotTheFailureBudget` asserts they cannot leak into each
other. Full detail in F2's doc; recorded here because the counter is rendered by
F1's placeholder.

### 5. The test suite inherited `NO_COLOR` from the shell

`go test ./frontend/...` was **green under Bash and red under PowerShell for
identical code**. The shell had `NO_COLOR=1` left over from F1's own
verification step, `theme.New` honours it by dropping every colour, and the
colour assertions silently stopped asserting anything.

Fix: `TestMain` in both `app` and `theme` calls `os.Unsetenv("NO_COLOR")`; the
one test that wants it sets it with `t.Setenv`. Verified green with `NO_COLOR=1`
set in the environment.

Related: lipgloss picks its colour profile from whether its output is a
terminal, and `go test` is not one — so **every style renders bare in a test
binary unless the profile is pinned**. That is why bug 1 was invisible to the
suite. `app`'s `TestMain` now calls `lipgloss.SetColorProfile(termenv.TrueColor)`.

### 6. `gofmt -w frontend/` reformatted `frontend\internal\array\`

Which F1 was told to leave alone. Content was unchanged — a trailing newline on
`menu.go` — and it was reverted with `git checkout`. Noted because
`core.autocrlf=true` means the working tree is CRLF and `gofmt -l` flags every
file in the repo, so a broad `gofmt -w` is never safe here. **Format specific
directories.**

## Notes for F7, on `frontend\internal\array\menu.go`

Read but deliberately not modified. What would change when F7 folds it into the
shared `Popup`:

- `SetItems` stores the caller's slice without copying, so a later append by the
  caller mutates the menu.
- `applyFilter` aliases `m.items = m.all` for an empty query, so a write through
  one is visible in the other.
- `Update` has a `tea.Model` signature but `Menu` is used by value with pointer
  mutators, so the returned model is the only correct one to keep.
- `SetSize` silently clamps height into 3 / 7 / passthrough bands, ignoring the
  rect it was handed — incompatible with "components render into the rect they
  are given".
- `renderList` pads short rows with `"  "` rather than to full width, so a
  shorter row under a longer one leaves stale cells.
- `AppendItems` does not reset `selected` when the filtered view shifts.
- Filtering is substring-only; the palette will want subsequence/fuzzy.
- The file has no trailing newline, which is why `gofmt` flags it.

## What the next episode inherits

- **`layout.Compute` is the contract.** Every episode from F3 renders into the
  rects above. It is pure and table-tested; changing a breakpoint is one table
  and one test table.
- **A validated action registry.** Register in `app.registerActions`; the
  palette (F7), hint bar, help screen and future rebinding all read it. Adding a
  feature is one entry.
- **Four working palettes** and a hard rule enforced by test and by grep: no hex
  outside `theme\`.
- **A themed overlay contract** with a working fit check, scrim and focus trap.
  F7 builds `Popup` against `Overlay`; it does not need to touch the compositor.
- **`box()`, `padCells`, `truncateCells`, `visibleWidth`, `sliceCells`** live in
  `app` and are unexported. **F3 or F7 should lift them into a shared `ui`
  package** — F1 kept them local rather than inventing a package the episode did
  not list.
- **Left deliberately unfinished, both recorded in
  `../../tasks/frontend/F16-polish.md` itself:** the header's animation slot is
  a static `◇ ◈ ◇`, and header padding degrades via a 1-cell clamp below ~90
  columns instead of dropping segments.
- **Not built, by instruction:** any real workspace, viewport or input (F2–F4);
  workspace state persistence (F12); a second list component.
