# Episode F7 — Popup Framework ✅

**One filterable list widget and one confirmation dialog, built once. The
command palette and the help screen are the same `Popup` with different data,
and F8's `@` picker, F9's task tray, F10's settings and F11's memory browser
are meant to be too — which is why this file records the struct field-for-field
rather than describing it.**

> Verified by Aryan across three rounds against
> `tasks/frontend/F07-popup-framework.md`'s eight "Done when" criteria, on a
> real terminal. Committed as `e2e9fdd`.
>
> **One ambiguity stopped the session before code, as the episode's own
> instructions require.** `Confirm` has no consumer in the Build section's
> "first two consumers" list, yet Done-when #7 says "every destructive action
> routes through it" — and two such actions already existed unconfirmed (quit
> with tasks running, discard a queued draft). Aryan's call: wire **only**
> quit-with-tasks-running, because a draft persists to disk and is trivially
> recoverable while an abandoned background task is not. That asymmetry is the
> rule, not the specific action.
>
> **Two findings were mine, reported before they were asked about**, and both
> mattered: the description column cannot be made truncation-free against an
> open-ended registry (so it was removed, not chased), and F07's own
> verification script greps for `Popup\{`, which a constructor-plus-config
> shape never produces.

---

## Where the code went

```
frontend\internal\
├── app\
│   ├── popup.go        NEW — Popup, PopupConfig, PopupTab, ItemAction;
│   │                   all rendering, tabs, filter, mouse hit-testing
│   ├── confirm.go      NEW — Confirm; word-wrapped body, centred buttons
│   ├── overlay.go      newPaletteOverlay / newHelpOverlay / bindingLabel
│   │                   (pendingOverlay and helpOverlay DELETED)
│   ├── root.go         dispatchMouse, overlayRect, overlayFits,
│   │                   closeOverlayIfTooSmall; esc now reaches the overlay
│   ├── render.go       withOverlay uses overlayRect (one rect formula)
│   └── actions.go      app.quit routes through Confirm when tasks are
│                       in flight; doQuit / quitCmd
└── array\
    ├── item.go         Item gains Description, Binding, Section, Disabled
    ├── menu.go         REWRITTEN headless — no tea.Model, no rendering
    └── fuzzy.go        NEW — MatchKind, Match, fuzzyMatch, lessMatch
```

Tests: `popup_test.go`, `confirm_test.go`, `mouse_test.go` (app);
`menu_test.go`, `fuzzy_test.go` (array); plus updates to `app_test.go` and
`regression_test.go` where they referenced the deleted placeholders.

---

## The Popup API, exactly as built

**F8, F9, F10 and F11 are all consumers of this.** A near-match that compiles
but drifts on one field name is worse than an obvious break, so this is copied
from the code, not described.

```go
// Constructed through NewPopup — never as a struct literal. Popup's own
// fields are unexported because they are live state (the embedded
// array.Menu, the active tab, the filter text), not caller input.
func NewPopup(cfg PopupConfig) *Popup

type PopupConfig struct {
    Title string
    Tabs  []PopupTab // len(Tabs) <= 1 draws no tab row

    // Fires when an item is chosen (Enter, or a click) and is not
    // disabled, and closes the popup. nil means there is nothing TO
    // choose: Enter/click is then a no-op that leaves the popup OPEN.
    OnSelect func(*array.Item) tea.Cmd

    Actions []ItemAction

    MinW, MinH int
}

type PopupTab struct {
    Label string
    Items []*array.Item
}

type ItemAction struct {
    Key   string                   // a CHORD, never a bare rune — see below
    Label string                   // shown in the footer hint
    When  func(*array.Item) bool   // optional; nil means always available
    Run   func(*array.Item) tea.Cmd
}
```

`Popup` satisfies `app.Overlay` with exactly the four methods F1 defined:

```go
func (p *Popup) MinSize() (int, int)
func (p *Popup) Title() string
func (p *Popup) Update(msg tea.Msg) (Overlay, tea.Cmd)
func (p *Popup) View(th theme.Theme, w, h int) string
```

### Four things a consumer must know before writing one

1. **`Update` returns `nil` to close itself.** That is the whole closing
   protocol — Root's dispatch assigns whatever comes back, so a `nil` Overlay
   *is* "close me". There is no separate Close method and no flag to set.

2. **`ItemAction.Key` must be a chord, not a printable rune.** The filter is
   always live, so every plain rune types into it and can never reach an
   action. `"ctrl+d"`, `"delete"`, `"alt+a"` work; `"d"` never will. This is
   enforced by construction, not by validation — a rune key simply never
   matches.

3. **`View`'s `w` is not the text budget.** `box.go`'s `fitCells` pads one
   more cell on each side after Popup returns, so the real budget is `w-2`.
   Filling `w` silently loses the last two cells.

4. **Items are built fluently and carry more than a title:**

```go
array.NewItem(id, title).
    SetDescription(d).   // stored; NOT rendered in rows (see below)
    SetBinding(b).       // right-flush column
    SetSection(s).       // dim header row, unfiltered view only
    SetDisabled(true)    // greyed, still reachable, cannot be chosen
```

`Description` is deliberately still on `Item` even though rows no longer draw
it — F9's result cards and F11's memory rows are the likely consumers, and
dropping the field would make them re-add it.

## The Confirm API, exactly as built

```go
func NewConfirm(title, body, yes, no string, onConfirm tea.Cmd) *Confirm

func (c *Confirm) MinSize() (int, int)   // 40×7, LAYOUT.md's own number
func (c *Confirm) Title() string
func (c *Confirm) Update(msg tea.Msg) (Overlay, tea.Cmd)
func (c *Confirm) View(th theme.Theme, w, h int) string
```

Positional, not a config struct: five fields, all required, none optional —
a struct would add ceremony without adding clarity. Empty `yes`/`no` default
to `"Yes"`/`"No"`.

- **No is focused by default**, so an Enter pressed out of habit never fires
  the destructive path. `←`/`→` move, `⏎` confirms the focused button, `esc`
  cancels, and `y`/`n` are shortcuts.
- **`onConfirm` runs only on confirm**, and it is a `tea.Cmd`, so its side
  effects happen when Bubble Tea runs it — not when the dialog is built. That
  is why `app.quit` splits into `doQuit` (synchronous, for the no-confirm
  path) and `quitCmd` (deferred, for this one).
- The body is **word-wrapped**, not truncated.

---

## Bugs caught during verification

Six, across three rounds. Each has a regression test; the three worth reading
are explained after the table.

| # | Symptom | Cause | Fix |
|---|---|---|---|
| 1 | Caps Lock / Ctrl / Alt typed into the filter | Windows decodes a bare modifier keydown as `KeyRunes` with a zero rune | `insertableRunes`, the same rule `chat\input.go`'s `filterInsertable` already applied |
| 2 | Click did nothing on help, worked on the palette | A wheel tick reports `Action == MouseActionPress`, same as a click | Only a LEFT click outside closes — see below |
| 3 | Enter/click closed help, which has nothing to run | `chooseSelected` closed on a nil `OnSelect` | nil `OnSelect` is now a no-op that stays open |
| 4 | `"1/"`, `"2/"` instead of `"1/10"` | The footer was truncated from the right; the indicator was the first casualty | The indicator is placed last and given its width FIRST |
| 5 | An open overlay shrank past its own MinSize | Nothing re-checked fit after the initial open | `closeOverlayIfTooSmall` on every resize, sharing `overlayFits` with `OpenOverlay` |
| 6 | `"Leave Poin"` — descriptions cut, no ellipsis | The description column cannot be bounded against an open-ended registry | Column removed; rows are name + binding, flush left and flush right |

**Bug 2 is the one worth remembering.** It presented as "clicks don't work on
the help screen but do on the palette", which points at the hit-test. It was
neither: bubbletea's decode never sets any `Action` other than `MouseActionPress`
for a wheel event, and scrolling does not require the pointer to be over the
panel. So a wheel tick with the cursor resting elsewhere closed the overlay
out from under itself, and the click that followed landed on nothing. It
looked consumer-specific only because help's taller box made the pointer more
likely to drift outside it.

**Bug 6 is a decision, not just a fix.** The registry's longest description
("Stop the reply that is streaming, keeping what has arrived so far") cannot
fit beside a name and a binding at any width a terminal should be asked to
spare, and any width chosen today is one long description away from
truncating again. Name and binding *can* be bounded — the longest of each is
20 and 17 cells — so those two are guaranteed complete rather than usually
complete. Aryan's instruction was "zero truncated text, not 'usually fits'",
and removal is the only thing that delivers that.

**A seventh, found by a test rather than by Aryan:** `renderHighlighted`
originally styled every rune separately, so the raw output contained an escape
sequence between every character and `strings.Contains(out, "Command Palette")`
could never match. Consecutive runes of the same style are now batched into
one `Render` call. Worth recording because the symptom is invisible on screen
— it only shows up when something searches the styled output.

---

## Decisions worth carrying forward

- **`array` is headless.** `Menu` owns selection, scrolling and fuzzy ranking
  and renders nothing, because a matched character needs the accent role and a
  selected row needs a `Fill` — and `array` must not import `theme`. `Popup`
  turns a `Row` into text. The old `menu.go`'s `tea.Model` plumbing, its
  clamp bands and its substring-only filter are gone.
- **Headers and items are ONE ordered row list**, not two — the same fix F5
  made to the transcript. A header consumes a row exactly like an item, so
  scrolling and the `N/M` indicator stay correct without separate accounting.
- **Sections show only when unfiltered.** Ranking interleaves items from
  different sections, so a header could not label a contiguous run of them.
- **The filter persists across tabs** (F07 left the choice open). A query that
  vanished because you brushed `tab` would be worse.
- **`j`/`k` do NOT move.** The Build section mentions them, but Done-when #5
  requires `j` typed into the filter to type `j`. Only non-printable keys
  navigate: `↑`/`↓`, PgUp/PgDn, Home/End, Tab/Shift+Tab.
- **Palette and help are 70×16**, raised twice during verification from
  LAYOUT.md's literal `palette 40×10`. That number is a floor an 80-column
  terminal clears, never the box's resting size. 70 also lines up with
  `layout.OverlayFloor`, so nothing sits between "too narrow for any overlay"
  and "fits this one".
- **Focus is an accent foreground, not a `Fill`**, on Confirm's buttons —
  `box.go`'s own convention, and THEME.md rule 3 (focused border, active tab
  and the streaming indicator are the same accent). A list row still uses a
  `Fill`, which THEME.md names explicitly.

---

## What F7 changed outside its own files

F1 owned "only the interface, the fit check, and the scrim" and left the rest
to whichever episode first needed it. F7 was that episode:

- **Mouse events never reached an overlay.** `Root.update` had no
  `tea.MouseMsg` case, so a click fell through to the workspace even with an
  overlay open. `dispatchMouse` now routes them, translated to the overlay's
  own content origin.
- **`esc` closed any overlay unconditionally**, before the overlay saw the
  key — which no two-stage esc can survive. Every key now goes through
  `Overlay.Update`.
- **The overlay rect was computed in two places.** `overlayRect` is now the
  single formula both `withOverlay` (render) and `dispatchMouse` (hit-test)
  use, so a click and the panel it appears to hit can never disagree.

---

## What the next episode inherits

**F8, F9, F10, F11 — build your surface as a `Popup`, not beside one.** The
API above is complete and stable; the four gotchas in "Four things a consumer
must know" are the whole learning curve.

Specific notes per consumer:

- **Tabs are built and tested but have no live consumer yet.** Neither the
  palette nor help uses more than one tab, so tab rendering, cycling and
  click-to-switch are covered by unit tests only. **F11's memory browser is
  the first real exercise** — expect to be the one who finds anything wrong
  with them.
- **`Actions` is likewise untested against a live consumer.** F9's result card
  (`a` attach · `s` summary · `m` memory · `r` re-run · `d` discard) is the
  obvious first user, and note that those single-letter keys **cannot work in
  a filterable Popup** — every plain rune belongs to the filter. Either give
  the card chords, or give it a Popup variant with no filter, and decide that
  deliberately rather than discovering it.
- **`Confirm` has exactly one consumer.** Every later destructive action —
  F9's discard, F10's settings reset, F11's delete, the editor's unsaved-file
  close, the file-lock takeover — should route through it. Chat's
  discard-at-capacity (`esc` at queue depth 1) is deliberately left
  unconfirmed, per the recoverability rule above; that is a decision, not an
  oversight.

**Still owed, now from three episodes.** `chat\textutil.go` duplicates
`app\box.go`'s width primitives. F1 flagged lifting them into a shared `ui`
package as F3-or-F7 work; F3 deferred, F5 deferred and added `expandTabs` to
the duplicate, and F7 deferred again and added `wrapLine` to `confirm.go`.
Three copies of the same idea now.

**Three defects found by this episode's verification are NOT recorded here,
on purpose.** The frame-scoped render memo, `renderList` dropping fenced and
indented code inside list items (live since F3), and provider-aware session
selection at startup are all `frontend\internal\chat\` — none of them F7's
code, which is `app/` and `array/`. They have their own file,
`S12b-f7-verification-repairs.md`, because a session working in `chat/` has
no reason to open an episode doc about popups. Commits `4d8aef6`, `d3133ba`.
