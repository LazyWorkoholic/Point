# F4 — Query Input

**Phase:** P1 · **Batchable:** no · **Depends on:** F3 · **Backend:** —

## Purpose

A real editing surface, not a text field. This is the component you touch most,
so its details matter more than most features.

## Build

### Editing behaviour — all required
```
cursor        ←/→, ↑/↓ across wrapped lines, home/end, word-wise (ctrl+←/→)
selection     shift+movement, ctrl+a select all
delete        backspace/delete, word-delete (ctrl+backspace), delete selection
multi-line    Alt+Enter inserts a newline; line breaks preserved
paste         bracketed paste → one draft, never N sends
undo          ctrl+z / ctrl+y within the input
```
Width math uses display width (`go-runewidth`), same as F3.

On Windows Terminal, `ctrl+←/→` and `ctrl+backspace` are delivered normally —
verify each one rather than assuming.

### Send
**Enter sends. Alt+Enter inserts a newline.** `Ctrl+Enter` and `Shift+Enter` are
indistinguishable from `Enter` in most terminals without the kitty keyboard
protocol, so they are not bindable. Both are registered actions and rebindable.

### Growth — overlay + auto-scroll
```
base height 1 row, grows with content
past the base, the input OVERLAYS the viewport upward, to about half of it
  and the viewport AUTO-SCROLLS down by the same amount, so the newest text
  stays visible above the input
past that maximum, the input becomes internally scrollable — no more growth
```
Auto-scroll applies **only when the viewport is pinned to the bottom**; if the
user has scrolled up to read, do not yank the view. Restore the previous offset
when the input shrinks. **The viewport is never resized**, so the transcript never
re-wraps while typing.

### Mode chip
```
▸ chat        dim border      Enter queues if a reply is streaming
▸ @research   accent border   Enter fires a background task immediately
```
The chip shows what Enter will do **before** it is pressed. Entering agent mode
changes the input border to the accent role — make that the only moving colour on
screen at that moment.

### Attachment chips
Pinned at the **top of the input**, above the typed text:
```
╭──────────────────────────────────────────────╮
│ @task.py ✓ current  ·  @notes.md ⚠ changed   │
│ ▸ chat │ explain the runtime                 │
╰──────────────────────────────────────────────╯
```
Each chip shows current/changed state and a token estimate; `⚠ changed` means it
will be re-sent. Backspace at position 0 removes the last chip.

### Drafts
Draft text **and** cursor position persist into
`%LOCALAPPDATA%\Point\state.json`, debounced ~500ms after typing stops. Scope:
per session. Auto-unload can fire while you are away from the keyboard, so a lost
draft is a bug nobody reports and nobody forgives.

## Do NOT

- Implement the `@` picker (F8) — render chips you are handed, and emit an
  "open picker" message when `@` is typed.
- Implement queueing behaviour (F5) — only the chip's display of it.
- Resize the viewport on growth.

## Done when

1. Every editing behaviour above works, including word-wise movement and
   select-all-then-type replacing the selection.
2. Pasting 40 lines lands as one draft; no message is sent.
3. Growth overlays and auto-scrolls: with the viewport pinned, the newest AI line
   stays visible as the input grows to half height; past that, the input scrolls
   internally.
4. Scroll up manually, then type — the view does not jump.
5. The transcript does not re-wrap while the input grows.
6. Kill the app with text in the input, restart → the text and cursor return.
7. A CJK/emoji line renders with a correct border and correct cursor position.
8. The mode chip changes on `@`, and the border colour follows.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

In the app:
- Type 20 lines with Alt+Enter. Watch the growth and the auto-scroll.
- Scroll up mid-typing. **Expect:** no jump.
- Copy a 40-line block from Notepad and paste it. **Expect:** one draft.
- Watch a long wrapped line above while the input grows. **Expect:** it does not
  re-wrap.

```powershell
# 6. draft survival — type a half-finished question, then in a second tab:
Get-Process point -ErrorAction SilentlyContinue | Stop-Process -Force
go run .\frontend\cmd\point
```
**Expect:** the draft and cursor position restored.
**Fails if:** the draft is gone, or comes back without the cursor position.

```powershell
Get-Content "$env:LOCALAPPDATA\Point\state.json"
```
**Expect:** the draft text visible in the file while the app is running.

## Handoff

State after this episode: text can be composed comfortably and survives a crash.
Sending does nothing useful yet — F5 wires it.

Record: which Windows Terminal key combinations did **not** arrive as expected,
if any. That list is useful for the whole rest of the project.

Do not proceed until every check above passes.
