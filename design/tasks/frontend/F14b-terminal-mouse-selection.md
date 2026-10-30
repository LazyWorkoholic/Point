# F14b — Terminal Mouse Scrollback and Selection

**Phase:** P2 · **Batchable:** no · **Depends on:** F14 ·
**Backend:** — **no backend work**

**Cuttable.** Nothing depends on it. Scheduled **after B17 (Hardening)** —
Aryan's own call, recorded here rather than inferred from the table's
order.

## Goal

**Use the mouse in the terminal pane: wheel-scroll the scrollback, and
select output to copy it.**

F14 shipped the pane with keyboard-only navigation, which is exactly what
its Build section asked for — `↑`/`↓` for command history and PgUp/PgDn
for the scrollback, and nothing else. Using it on real output immediately
wants two things it does not have:

- the **wheel** over the pane scrolls the scrollback, the way it already
  does over the file tree and the code buffer (`editor\mouse.go`'s
  `wheelBy`);
- **dragging** selects output text, and a copy key puts it on the
  clipboard — so a stack trace or a failing assertion can be pasted
  somewhere, which today requires re-running the command in a real shell.

> Task doc — describes **intent**. The completed doc
> (`../../completed/frontend/F14b-terminal-mouse-selection.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Found during:** F14's verification rounds, as the pane came under real
use. Recorded as a separate episode rather than absorbed, because it is
**new scope F14's Build section never asked for** — and F14 had already
run long enough on defects in what it did specify.

## Why it needs its own episode rather than a patch

Three things make this bigger than it looks, and all three are reasons
not to have smuggled it into F14:

1. **Mouse capture is already global and already costs something.**
   `main.go` starts the program with `tea.WithMouseCellMotion()`, which
   disables Windows Terminal's own selection app-wide — F13's own note
   says so and accepts it, because the editor needs drag-selection
   anyway. So the terminal pane cannot fall back on the terminal
   emulator's selection: either this episode implements selection, or the
   pane has none.

2. **Selection needs a coordinate model the pane does not have.** The
   code buffer hit-tests through `viewrows.go`'s row sequence, because a
   screen row is not a buffer line (ghost text, the fill prompt). The
   terminal pane now has the same problem for a different reason: output
   lines **wrap** (F14's own fix for silently-cut long lines), so one
   buffer line can occupy several screen rows, and a click resolves to
   `(line, cell-within-wrapped-segment)`. That mapping has to be built
   and recorded during render the way `textArea`/`treeRect`/`termRect`
   already are.

3. **Copy has an existing owner and an existing argument.** `copy.go`
   writes OSC 52 and `ctrl+c` in this workspace means copy **always**
   (`keys.go`'s first branch) — except, since F14, when the terminal has
   focus and a command is running, where it kills the child. Adding
   "copy the terminal selection" means deciding what `ctrl+c` does in a
   pane that can be both *selecting* and *running*, and that is a
   keybinding decision, not an implementation detail.

## Build

### 1. Wheel scrolling over the pane

```
wheel up / down over termRect   scrolls the scrollback by wheelStep
at the live edge, wheel down    re-pins to the bottom
while scrolled, the header      keeps F14's `↑N` marker
```

Routed in `editor\mouse.go`'s `wheelBy`, beside the tree's branch, using
the `termRect` F14 already records during render. `scrollTerminal` and
the `scrollBack` offset already exist and already clamp — this is a new
*caller*, not a new mechanism.

### 2. A row/cell model for the pane

```
during render, record for each drawn output row:
    which buffer line it came from, and the rune offset the row starts at
```

One slice, rebuilt per frame beside the rows themselves, the same shape
and for the same reason as `tabSpans`. **Never re-derived at click time**
— `mouse.go`'s own header states that rule and F15's caret-off-by-one
bug is why it exists.

### 3. Drag selection and copy

```
press        anchor the selection, focus the pane
drag         extend; dragging off the top/bottom edge scrolls by one row
release      keep the selection
a copy key   put the selected text on the clipboard via copy.go
esc          clear the selection
```

Selection is **display-only state on the pane**, never written into the
scrollback, and it must survive new output arriving (an anchor is a
`(line, offset)` pair, not a screen position).

### Keybinding decision this episode must settle and record

`ctrl+c` in this workspace is copy, except in the terminal pane with a
command running, where F14 made it the kill. With a selection present
there are three claimants. Decide and record in the Handoff; do not leave
it implicit. The obvious candidate is "a selection wins over the kill,
because a selection is explicit and recent" — but it has to be chosen,
and the status bar has to say which.

## Do NOT

- Implement a PTY or VT100 emulator. F14's rejection stands; none of this
  needs one.
- Make the mouse the only way to do anything — F14's keyboard paths stay,
  and `LAYOUT.md`'s rejected-ideas table lists "mouse-only affordances"
  as breaking over SSH and for keyboard users.
- Touch `tea.WithMouseCellMotion()`. It is already on, app-wide.
- Add a second scrollback or a second clipboard path. `scrollBack` and
  `copy.go` are the ones.

## Done when

1. The wheel scrolls the pane's scrollback, and the `↑N` marker tracks it.
2. Wheel-down at the live edge re-pins to the bottom and the marker clears.
3. A drag selects across **wrapped** output lines and the highlight matches
   the cells selected.
4. The copy key puts exactly the selected text on the clipboard, with its
   own line breaks and spacing — no reflow.
5. A selection survives new output arriving while it is held.
6. `esc` clears the selection; the keyboard paths (`↑`/`↓`, PgUp/PgDn,
   Enter, ctrl+c-as-kill) all still behave as F14 left them.
7. The `ctrl+c` precedence decision is implemented, visible in the status
   bar, and recorded in the Handoff.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the Editor's terminal pane, run something long and wrapped:

```
uv run python cli.py --help
```

**Expect:** wheel scrolls it; `↑N` appears in the header; wheel-down to the
bottom clears it.

Drag across two wrapped rows of that output and copy.
**Expect:** the clipboard holds exactly those cells, spacing intact.
**Fails if:** the copy is reflowed, or the highlight and the copied text
disagree.

Start a long-running command, select some of its output, then press
`ctrl+c`.
**Expect:** whatever the recorded decision says, and the status bar says
the same thing.

## Handoff

Record: the row/cell model's shape, the `ctrl+c` precedence decision and
why, and whether OSC 52 carried a multi-line selection intact on Windows
Terminal.

Do not proceed until every check above passes.
