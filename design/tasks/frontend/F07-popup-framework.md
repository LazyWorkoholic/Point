# F7 — Popup Framework

**Phase:** P1 · **Batchable:** no · **Depends on:** F1 · **Backend:** —

## Purpose

Build the widget **once**. The palette, memory browser, settings, `@` picker, task
tray and help screen are all the same component with different data. This is the
honest justification for hand-writing widgets instead of using `bubbles` — that
tab-scoped filterable popup does not exist there.

## Start from what exists

`frontend\internal\array\` already has `item.go` and `menu.go` — a scrollable,
non-destructively filterable list with display-width truncation. **Fold it into
the shared `Popup`; do not write a second list.** Keep `array` as the internal
list model and build `Popup` around it (tabs, chrome, actions, MinSize), or
promote it wholesale — either is fine, but say which in the handoff.

## Build

### The Popup component
```go
type Popup struct {
    Title    string
    Tabs     []Tab            // optional; empty = single list
    Active   int
    Filter   string
    Items    []Item           // supplied by the caller — data-driven
    OnSelect func(Item) tea.Cmd
    Actions  []ItemAction     // per-item keys, e.g. accept / reject / delete
    Min      Size
}
```

Rendered as a **rounded box with the title on the top edge**, matching the panes
(`LAYOUT.md`):

```
╭─ Memory ───────────────────────────────╮
│ =PENDING (3)=   SESSION   PROJECT      │
│ filter: run_                           │
│                                        │
│ ⌸ Runtime uses stdin/stdout, not HTTP  │
│   research · Task #12 · 3d · ipc, arch │
│ ⌸ Ollama is capped at one task         │
│   user_card · "runtime bug" · 1d       │
╰────────────────────────────────────────╯
```

- Tab row: active one bracketed and in the accent role; `tab` / `shift+tab` cycle.
- Filter always live; `esc` clears a non-empty filter first, closes on the second
  press.
- Fuzzy match (subsequence, case-insensitive) with matched characters
  highlighted. **Rank exact-prefix above scattered matches** — otherwise typing
  `run` puts `runtime` below `bar_run_x`.
- Sections with dim headers inside a tab, for grouped sources.
- `↑/↓` / `j/k` move, `⏎` selects, mouse click selects, click outside closes.
- Scroll position indicator when items exceed the height.
- Truncation by **display width** (`go-runewidth`), as `menu.go` already does.

### The Confirm component
```go
type Confirm struct { Title, Body string; Yes, No string; OnConfirm tea.Cmd }
```
Every destructive action routes through it: unload, quit with tasks running,
discard a draft, delete a memory, close an unsaved file, take over a file lock.
`←/→` move, `⏎` confirms, `esc` cancels. **No is focused by default.**

### MinSize enforcement
The root checks `MinSize()` before opening and refuses with the real numbers:
`memory needs 60×16 — terminal is 48×20`. Below 70 columns, overlays refuse
entirely.

### Overlay chrome
Centred, rounded border with edge title, dim scrim over the content behind, `esc`
closes, **focus trapped** while open — keys must not leak to the workspace
underneath.

### First two consumers

**Command palette (UI-01)** — reads the F1 action registry. Shows name,
description, and binding; greys out actions whose `When` predicate is false.
`ctrl+p`.

**Help screen (UI-11)** — generated from the same registry, grouped by category.
**Never a hand-maintained list**, which would drift on the first rename.

## Do NOT

- Build memory (F11), settings (F10), the `@` picker (F8), or the task tray (F9).
- Let a consumer fork the component. If one needs something, extend the shared one.
- Write a second list implementation alongside `array`.

## Done when

1. Palette and help are **the same component** with different data — one type
   definition, many call sites.
2. Filtering is live, fuzzy, highlighted, and ranks prefix matches first.
3. Tabs cycle with `tab`; filter behaviour across tabs is consistent (persist or
   reset — pick one).
4. Opening at 48×20 shows the real numbers; at 65 columns overlays refuse.
5. Keys do not leak: `j` typed into the filter types `j`, it does not scroll the
   transcript underneath.
6. `esc` clears a non-empty filter first, then closes on the second press.
7. Confirm defaults to No, and every destructive action routes through it.
8. The popup's border is rounded with its title on the top edge, matching panes.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the app:
- `ctrl+p`, type a few letters. **Expect:** fuzzy hits with highlighted
  characters, descriptions, bindings, and prefix matches ranked first.
- Type `j` in the filter. **Expect:** it types, it does not scroll.
- Press `esc` once with a filter. **Expect:** filter clears, popup stays. Press
  again. **Expect:** closes.
- Resize to 48 columns, then `ctrl+p`. **Expect:** a refusal naming both the
  requirement and the current size.

```powershell
Select-String -Path .\frontend\internal\**\*.go -Pattern "type Popup struct"
```
**Expect:** exactly one match.

```powershell
Select-String -Path .\frontend\internal\**\*.go -Pattern "Popup\{"
```
**Expect:** several call sites.

## Handoff

State after this episode: one popup widget and one confirm widget exist, with the
palette and help proving them. Every later UI surface is now cheap.

Record: how `array` was folded in — kept as the internal list model, or promoted —
and anything from `menu.go` that had to change.

Do not proceed until every check above passes.
