# F3 — Chat Viewport

**Phase:** P1 · **Batchable:** no · **Depends on:** F2 · **Backend:** B2, B3 ✅

## Purpose

The transcript: readable, fast at length, and correct with real-world text — wide
characters, code, long lines.

## Build

### Message rendering
Padding is specified exactly in `LAYOUT.md` and stacks in two layers: the
**viewport** pads its contents 2 cells horizontally and 1 row top/bottom, and a
**user message box** pads its own text a further 1 cell. Text therefore sits 3
cells clear of the viewport wall.

**Wrap width = viewport inner width − 4.** Without this, long lines touch the
right wall even though the padding is "correct".

- **User**: rounded box with 1-cell horizontal padding, right-aligned `:User`
  label above it.
- **AI**: left accent bar `▎` in `ai.bar` at the padded position — **no box**.
  Wrapped continuation lines align **under the bar**, not under the wall. Boxing
  costs no tokens; it pollutes copy-paste with `│` on every line and
  double-borders code blocks.
- **System/error**: dim text, `error` role, no decoration.
- **Task slots**: a collapsed one-line placeholder occupying the position a task
  reserved at send time. State transitions are F5; the *slot* is rendered here.

### Markdown and syntax
- Glamour (or equivalent) for markdown, **themed from the semantic roles**, not
  its default stylesheet.
- Chroma for fenced code blocks, in a rounded `code.border` box with `code.text`.
  Unknown languages render plain, never error.
- Inline code uses `inline.code`.

### Wide characters
All width math uses **display width**, not `len()` — `github.com/mattn/go-runewidth`,
the same dependency `menu.go` now uses. CJK and emoji are two cells; getting this
wrong breaks every border and truncation in the app.

### Scrollback
- Cap **rendered** history (default 500, `ui.scrollback`); older messages load on
  scroll via `session.messages(before, limit)`.
- A 10k-message session must not melt the renderer.
- Pinned-to-bottom: auto-scroll while pinned; **stop the moment the user scrolls
  up**, and show a `↓ jump to latest` affordance.

### Render caching
Re-rendering the whole transcript on every token is the obvious performance trap.
Cache rendered blocks per message id; invalidate on content change or a width
change. **Only the streaming message re-renders per token.**

### Selection and copy
- `y` on the focused message copies its **raw** text (not the rendered box) via
  OSC 52 — which works over SSH, unlike mouse selection.
- Shift-drag falls through to Windows Terminal's native selection.
- `j/k` or arrows move message focus; the focused message gets `border.focus`.

### Empty and loading states
Empty session: a centred hint naming exactly two things — `type a message` and
`^P palette`. Loading older messages: a dim one-line spinner at the top.

## Do NOT

- Build the input (F4) or streaming logic (F5).
- Implement the right pane (F6).
- Box AI messages.

## Done when

1. A 10,000-message session opens and scrolls smoothly; memory stays flat while
   scrolling.
2. Markdown renders themed: headings, lists, bold, links, fenced code with syntax
   colouring inside a rounded box.
3. A message containing CJK text and emoji renders with correct borders and
   truncation at every width.
4. Scrolling up stops auto-scroll; `↓ jump to latest` returns and re-pins.
5. `y` copies raw text — pasting elsewhere shows no `│` or `▎`.
6. Resizing re-wraps correctly and invalidates the render cache.
7. An unknown-language code block renders as plain text, no error.

## Manual verification (PowerShell)

```powershell
cd D:\Projects\Point\backend
uv run python cli.py seed-session --messages 10000
cd D:\Projects\Point
go run .\frontend\cmd\point
```
**Expect:** instant open, smooth scroll, flat memory.
(If `seed-session` does not exist, add it to `cli.py` in this episode — a fixture
generator is worth the ten lines.)

Send a message containing exactly this, to exercise everything at once:
```
# heading
**bold** and `inline` and 日本語テキスト and 🎉

```unknownlang
some text
```
```
**Expect:** correct rendering, and the CJK/emoji line's border aligned with every
other line.
**Fails if:** the right border of that line sits one or two cells off — that is
`len()` being used where display width was needed.

Focus an AI message, press `y`, paste into Notepad.
**Expect:** clean text, no decoration characters.

## Handoff

State after this episode: the transcript renders correctly and stays fast at
length. Nothing can be typed yet.

Do not proceed until every check above passes.
