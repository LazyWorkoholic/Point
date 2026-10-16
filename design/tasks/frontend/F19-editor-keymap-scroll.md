# Episode F19 — Editor keymap documentation, a one-key close, and the scroll bound

**Three findings from manual testing after F13b, none of them on F13's
Done-when list. (a) The file tree's `h`/`j`/`k`/`l` vim motions are a
deliberate, complete set and were written down nowhere, so `l` read as a
stray keymap entry duplicating Enter. (b) Closing a tab had no one-key
binding — `ctrl+w` is the leader and cannot double as close, and
`ctrl+shift+w` is not a key Windows can deliver. (c) The scroll offset
in BOTH the code pane and the file tree was bounded by `count-1` instead
of `count-rows`, so scrolling to the end of any list left ONE item
visible however tall the viewport, and a wheel notch back flashed four.
Not a shared viewport component with one bug — the same off-by-one
written three times.**

> Task doc — describes **intent**. The completed doc
> (`design/completed/frontend/F19-editor-keymap-scroll.md`, once it
> exists) claims reality and completion. Code wins on any disagreement.

**Model:** Sonnet. One extracted helper, one new key case, one doc
comment and two hint strings. The diagnosis of (c) was the work; the
change is a four-line function.

**Depends on:** F13a (the tree, the code pane, the tab strip and
`closeTab`), F13b (C3's `[ Close ] [ Cancel ]` confirm, which (b)
routes through rather than reimplementing). No backend.

**Found during:** F13b's manual verification, **and not part of F13b's
own Done-when list** — F13's twelve Done-whens are the editor's buffer,
metadata, selection, indent, undo, save, locks and autosave, and none of
these three is any of those. Spun out as its own episode, the same
discipline B23–B31 established. F13b's own doc carries a pointer here.

---

## Why this episode exists

### (a) `l` looked like a stray binding

Reported as "`l` behaves like Enter — intentional vim-motion binding
that isn't documented, or a stray keymap entry?". **Intentional, and a
complete set rather than one stray case** — `treeKey` has had all four
since F13a, and `keys_test.go`'s `TestTreeNavigationKeys` already
records that they mirror chat's own browse keys:

```
up    k        down  j
right l        expand a folder, or open a file
left  h        collapse
enter          open a file, or expand a folder
```

`l` and Enter really are equivalent — on a file `toggle()` reports false
so both fall through to `openSelected`, and on a folder both expand it.
Two spellings for the hand, not two behaviours.

So the keymap is right and the documentation was absent. Removing the
motions would be a regression for anyone who has used them since F13a;
the fix is to write them down.

### (b) `ctrl+w` is the leader, and `ctrl+shift+w` does not exist

`ctrl+w` is `actions.Leader`, intercepted before this workspace ever
sees a key — that is what makes `ctrl+w e` and `ctrl+w w` work, so it
cannot also mean close.

`ctrl+shift+w` cannot work either, for the reason F13b's `f6` binding
exists: bubbletea's Windows console-record reader produces the **same
control character** for `Ctrl+W` and `Ctrl+Shift+W`, so `ctrl+shift+w`
arrives as `ctrl+w` — the leader again — and close-tab would open the
leader hint. Measured for `Ctrl+Shift+L` during F13b (`tea.KeyCtrlL`'s
`String()` is `"ctrl+l"`, which is `app.redraw`'s chord) and the same
mechanism applies.

### (c) The same off-by-one, three times

Measured before the fix, which is the only way the "flash" made sense:

```
10-line file in a 20-row viewport (should not scroll at all):
  scrolled to the end -> top=9, 1 line visible
  one wheel notch up  -> top=6, 4 lines visible      <- the reported flash
tree, 6 rows in a 4-row viewport -> top=5, 1 visible
```

The wheel moves three lines at a time, which is where "four" comes from.
All three sites bounded `top` by `count-1`:

- `motion.go` `scrollBy`: `if max := d.buf.Lines() - 1`
- `motion.go` `ensureVisible`: `if d.top > d.buf.Lines()-1`
- `tree.go` `ensureVisible`: `if max := len(t.rows) - 1`

The report guessed a shared list/viewport component. There isn't one —
the tree and the doc each own their `top`. It is one expression written
three times, which is the same class of problem F13b hit with
`effectiveFocus` and answered the same way: derive it once.

---

## Build

### (a) Write the motions down

`treeKey`'s own doc comment gets the table above, plus why the bindings
stay (they mirror chat's browse keys; `keys_test.go` says so) and why
`l` and Enter are equivalent. The empty-state key list
(`render.go`'s `emptyState`) gains a line naming them, because that is
the screen a new user reads.

### (b) `f7` closes the active tab

Routed through the **same `closeTab`** the tab strip's `×` uses, so a
dirty file still gets C3's `[ Close ] [ Cancel ]` with Cancel focused
(F13's Done-when 9). No second close path.

`f7` and not a leader sub-chord: the editor already owns `f5`
(re-index) and `f6` (take over a stale lock), so this keeps the
function-key block contiguous and in a memorable order — **f5 re-index,
f6 take over, f7 close**. Nothing in the chord registry claims it (the
chords are `ctrl+c`, `ctrl+e`, `ctrl+l`, `ctrl+p`, `ctrl+q`, `ctrl+r`,
`esc`, `f1`, `tab`), so it needs no `KeyCapturer` claim and F13a's
claim list stays at two keys.

Advertised in the empty-state list and appended **last** to the status
line, so `fitHints` — which drops whole parts from the end — sheds it
first on a narrow terminal rather than a segment describing the file.

### (c) One `clampTop`

```go
func clampTop(top, count, rows int) int
```

In `textutil.go`, used by all three sites, so the bound cannot be
written three ways again. `rows < 1` is treated as 1 — a viewport with
no rows is a layout mid-resize, and a negative maximum would put `top`
past the end.

`scrollBy` takes `rows` as a parameter rather than remembering it: the
viewport height is the layout's, recorded during `View`, and a scroll
clamped against a stale copy would be the drifting-state bug again. The
caller has `w.textArea.H` at the moment of the scroll.

`ensureVisible` keeps a clamp **before** its reveal gate as well as
after, and that is load-bearing on two paths `scrollBy` never touches:
a **resize** (the viewport grows; nothing calls `scrollBy`) and
**Resume** (`applyResumeTo` assigns `d.top` straight from the suspend
blob, written against whatever height that session had).

---

## Do NOT

- Remove the `h`/`j`/`k`/`l` motions. They are deliberate and shipped.
- Bind close to `ctrl+w` or `ctrl+shift+w`. Both land on the leader.
- Give close its own confirm. It routes through C3's.
- Build a shared viewport/list component for (c). The tree and the doc
  own their own `top` for good reasons (different content, different
  reveal semantics); what they must share is the *bound*, which is four
  lines, not a component.
- Reopen anything on F13's Done-when list. The buffer, metadata,
  selection, indent, undo, save, locks and autosave are F13a/F13b's and
  verified.

---

## Done when

1. `treeKey`'s doc comment and the empty state both name `h`/`j`/`k`/`l`,
   and the keymap is unchanged.
2. `f7` closes a clean tab outright and routes a dirty one through C3's
   confirm; with nothing open it is a no-op, not a panic.
3. `f7` is not a registry chord and is not claimed by `CapturesKey`.
4. After **any** scroll, a full viewport of content is showing unless
   there is not enough content to fill one — in the code pane and in
   the tree.
5. A file that FITS the viewport does not scroll at all.
6. A `top` set without scrolling — by a resize, or by Resume — is pulled
   back to a full window on the next frame.

---

## Verification gate (PowerShell)

```powershell
cd D:\Projects\Point
go build ./... ; gofmt -l .\frontend\ ; go vet ./frontend/...
go test ./frontend/...
```

**Expect:** build, `gofmt` and `vet` clean; the suite green.

```powershell
cd D:\Projects\Point
go run .\frontend\cmd\point
```

Leader → `e`. Then, by hand:

- In the tree, press `j`/`k`, then `l` on a folder and on a file, then
  `h`. **Expect:** the documented behaviour, and the empty state's own
  hint line naming them.
- Open a file, press `f7`. **Expect:** it closes. Edit a file, press
  `f7`. **Expect:** `[ Close ] [ Cancel ]`, Cancel focused.
- Open a file SHORTER than the code pane and scroll down hard.
  **Expect:** it does not scroll at all. **Fails if:** it comes to rest
  showing one line, and scrolling back flashes four.
- Same in the tree with fewer entries than the pane is tall.
- Resize the terminal taller while scrolled to the end of a long file.
  **Expect:** the window fills with content rather than leaving blank
  rows under the last line.

---

## Handoff

- **This episode's diff is interleaved with F13b's verification
  repairs**, which were never committed: `65cb6dc "F13 complete"`
  predates four rounds of gate-driven fixes (the `f6` binding and
  backend-validated take-over, the close-confirm autosave pause and
  `Confirm.OnCancel`, the stale dialog's `PreferredSize`, the
  "autosave stopped" segment, `effectiveFocus`, the empty-state note
  removal, `keptByTree` and the static `ctrl+o` hint). Five files carry
  hunks from BOTH — `keys.go`, `render.go`, `status.go` — so the two
  cannot be separated by path, only by `git add -p`. Those repairs are
  **F13b's own work and must not be reverted or folded in here**; see
  the per-file mapping in this episode's report.
- `f6`'s and `f7`'s existence makes the editor's function keys a block
  worth keeping contiguous. F14 (Terminal) will want one; `f8` is free.
- The `ctrl+o` hint is a single static `ctrlOHint` const naming both
  targets, deliberately not focus-dependent — a label that tracked
  focus would be a third place focus state has to be right, and this
  episode's own (c) plus F13b's `effectiveFocus` are both that class of
  bug.
