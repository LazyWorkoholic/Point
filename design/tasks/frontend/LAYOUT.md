# Layout Reference

Authoritative for sizing, breakpoints, padding, and pane composition.
Referenced by F1, F3, F4, F6, F7, F9, F12, F13, F14.

## The engine

```go
type State struct {
    Active        WorkspaceID
    RightPaneOpen bool
    TreeOpen      bool
    TerminalMode  TermMode   // small | overlay | full
    OverlayOpen   bool
    HeaderAllowed bool
}

type Layout struct {
    Header    Rect      // unboxed text line
    Main      Rect      // viewport / code file
    Secondary Rect      // query area / terminal
    RightPane []Rect    // one rect PER CARD — heights are NOT even
    StatusBar Rect
    ShowHeader, ShowRightPane, ShowWatermark bool
}

func Compute(w, h int, s State) Layout
```

Pure. No I/O, no globals, no terminal calls. Table-testable without a terminal —
the cheapest safety net in the project.

**Two rules that must hold:**

1. **A component never decides its own visibility.** It renders into the rect it
   is handed; a zero-size rect means "not drawn this frame".
2. **Each component draws its OWN rounded border, inside its rect.** Borders are
   never shared between neighbours. `Compute` returns rects with the gaps already
   subtracted — components do not add margins themselves.

`RightPane` is a **slice of rects with different heights**, not an even split:
Status is short, Task is medium, the last card takes the remainder.

## Spacing — exact

```
terminal edge → everything          1 cell horizontal, 1 row vertical
header line → first box             0 rows      (two border lines separate them)
between stacked boxes, either pane  0 rows      (same)
left pane ↔ right pane              1 cell
box border → content                1 cell horizontal, 0 rows vertical
viewport inner padding              2 cells horizontal, 1 row top and bottom
message box border → its text       1 cell horizontal
```

Terminal cells are about twice as tall as they are wide, so a **1-cell horizontal
gap already reads as roughly half a row** vertically. That is why horizontal gaps
are 1 and vertical gaps between boxes are 0 — visually they balance, and 0-row
gaps buy back rows that matter at 24-row terminals.

Adjacent boxes still read as separate because you see two border lines (`╰──╯`
directly above `╭──╮`), not one shared line.

**Wrap width inside the viewport = inner width − 4.** Without this, long lines
touch the right wall even though the padding is "correct". This is a real and
common TUI bug; treat the number as part of the spec.

All borders rounded (`lipgloss.RoundedBorder()`), with the **title on the top
edge**: `╭─ View Port ────╮`. This applies to every popup, card, and diff too.

## Chat workspace

```
┌───────────────────────────────────────────────────────────────┐
│                                                               │
│   (animation)       P O I N T           ◈ github              │
│  ╭─ View Port ──────────────────────────╮╭─ Status ─────────╮ │
│  │                                      ││                  │ │
│  │                               :User  │╰──────────────────╯ │
│  │  ╭────────────────────────────────╮  │╭─ Task ───────────╮ │
│  │  │ Your message you gave          │  ││                  │ │
│  │  ╰────────────────────────────────╯  ││                  │ │
│  │                                      │╰──────────────────╯ │
│  │  AI:                                 │╭─ Sessions ───────╮ │
│  │  ▎ reply streams here with no box    ││                  │ │
│  │  ▎ and wraps well before the wall    ││                  │ │
│  │                                      ││                  │ │
│  ╰──────────────────────────────────────╯│                  │ │
│  ╭─ Query Area ─────────────────────────╮│                  │ │
│  │                                      ││                  │ │
│  ╰──────────────────────────────────────╯╰──────────────────╯ │
│   ^P palette   ^W workspace   2 tasks · 12.4k                 │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

- **Header is not a box** — a single unboxed line: animation left, POINT centre,
  trademark right.
- **No wrapper box around the left pane.** View Port and Query Area are two
  independent bordered boxes stacked with no gap.
- Right pane **fixed 32 columns** (26 at 90–119 width). Its content is
  fixed-width labels, so a percentage is absurd at 200 columns.
- Toggling the right pane off reflows Main and Secondary to full width, **no gap
  left behind**.
- **User messages** get a rounded box. **AI replies** get a left accent bar `▎`
  plus padding, no box — boxing pollutes copy-paste with `│` on every line and
  double-borders code blocks. It costs zero LLM tokens either way; the reason is
  copy-paste, not cost.
- Wrapped AI continuation lines align **under the accent bar**, not under the wall.
- Query area growth: **overlay + auto-scroll** (F4) — it grows upward over the
  viewport and the viewport scrolls to compensate, so the newest text stays
  visible. The viewport is never resized.
- Status bar is an unboxed line at the bottom.

## Editor workspace

```
┌───────────────────────────────────────────────────────────────┐
│                                                               │
│   (animation)       P O I N T           ◈ github              │
│  ╭─ Code File ──────────────────────────╮╭─ File Tree ──────╮ │
│  │ ‹ task.py │ executor.py │ events.py ›││ runtime\         │ │
│  │                                      ││  ├ task.py       │ │
│  │  1  # CODE                           ││  ├ executor.py   │ │
│  │  2  def greet():                     ││  └ events.py     │ │
│  │  3      return "Hello World"         ││                  │ │
│  │  4                                   ││                  │ │
│  │  5                                   │╰──────────────────╯ │
│  │  6                                   │╭─ Task ───────────╮ │
│  │  7                                   ││                  │ │
│  │  8                                   ││                  │ │
│  ╰──────────────────────────────────────╯│                  │ │
│  ╭─ Terminal ───────────────────────────╮│                  │ │
│  │ $ pytest -q                          ││                  │ │
│  ╰──────────────────────────────────────╯╰──────────────────╯ │
│   task.py  12:4  python  CRLF  lock: you   ^P ^W ^E tree      │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

- **Tabs are the first content row inside the Code File box**, not a nested box.
  Active tab in the accent role, inactive dim, separated by `│`. Overflow scrolls
  horizontally with `‹ ›` markers. A box directly inside a parent border reads as
  clutter and costs two rows.
- File tree on the **right** — a deliberate inversion of VS Code. Hides below 90
  columns.
- Below 70 columns the tab strip collapses to the active filename only.
- Terminal is the Secondary rect with three modes (F14): `small` (3–4 rows),
  `overlay` (grows upward over Code File to about half), `full` (fills the left
  column). The mode is remembered per workspace and survives Suspend/Resume.
- The status bar shows the **line-ending style** (CRLF/LF) — on Windows this is
  the difference between a clean `git status` and a fully-modified file.

## Breakpoints

Width and height evaluated independently.

```
WIDTH
  ≥120    full layout · right pane 32 cols
  90–119  right pane 26 cols · session titles truncate
  70–89   right pane auto-hides (still openable as an overlay)
  <70     single column · overlays refuse to open
  <50     "terminal too narrow — 50 columns minimum" and nothing else

HEIGHT
  ≥30     header line + animation
  24–29   header collapses to a title-only line
  <24     no header · dim `◈ point` watermark, bottom-right, overlaid
  <12     "terminal too short — 12 rows minimum"
```

**These numbers are provisional.** Separate bordered boxes plus outer margins cost
more rows and columns than a shared-border layout, and the true minimums will only
be visible once F3–F6 render. Expect to raise them slightly, and record the
adjustment in `completed/frontend/F01`. Changing them is one function and one test
table — the cheapest thing in the project to revise.

Windows Terminal's default is roughly 120×30, so the full layout is what you will
see day to day; the narrow paths need deliberate testing.

## Overlays

```go
type Overlay interface { MinSize() (w, h int) }

palette 40×10 · memory 60×16 · settings 60×20 · task tray 50×12
diff card 70×20 · result card 60×18 · confirm 40×7
```

Every overlay is a **rounded box with its title on the top edge**, matching the
panes: `╭─ Proposed change · config.py ─────╮`.

The root refuses to open one that does not fit and shows the **actual numbers**:
`memory needs 60×16 — terminal is 48×20`.

Overlays are centred, drop a dim scrim over the content behind, close on `esc` or
a click outside, and **trap focus** while open.
