package chat

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
)

// pasteSettle is how long after a paste an Enter is read as that
// paste's own trailing newline rather than as Send. Long enough to cover
// a burst the detector ended early under load; short enough that a
// deliberate Enter after reading what landed still sends. If it does
// swallow one, the cost is a stray newline and a second Enter -- against
// losing the whole composed message, which is what it replaces.
const pasteSettle = 200 * time.Millisecond

// handleInputKey is every editing keystroke the query input recognises,
// reached only while the input has focus (protocol.go's Update routes
// tea.KeyMsg here or to handleKey/scroll.go depending on w.input.focused —
// see EnterBrowseMsg/FocusInputMsg for how focus changes).
//
// Windows Terminal note (F04-query-input.md asks this be recorded): ctrl+←,
// ctrl+→, ctrl+home and ctrl+end all arrive as their own distinct KeyTypes
// on Windows (bubbletea's console-record reader decodes them from the
// virtual-key code directly, unlike bracketed paste — see apppaste.go).
// ctrl+backspace does NOT: VK_BACK maps to KeyBackspace unconditionally,
// with no ctrl-modified variant in bubbletea v1.3.10's key_windows.go at
// all (confirmed by reading it — every OTHER named key there does check
// ctrlPressed; backspace's case just doesn't) — so plain Backspace and
// Ctrl+Backspace are indistinguishable in this environment, on any
// platform this library version runs on, not just Windows. alt+backspace
// IS distinguishable (Alt is read into KeyMsg.Alt independently of key
// type, for every key), so that is word-delete's actual, working binding
// here; ctrl+backspace quietly falls through to plain single-character
// delete instead of failing outright. (A verification round separately
// found alt+backspace itself sometimes needs a second press to register —
// the FIRST press deletes only one character, as if Alt were not yet seen
// as held; every other alt-modified key here reads k.Alt correctly on the
// first press, so this looks like the console reporting Alt's own
// key-down slightly behind Backspace's, a timing race in Windows' own
// modifier-state bookkeeping — the same family as the well-known "first
// keystroke after refocus is swallowed" quirk, not a bug this file can
// see or fix: there is no way to ask "is Alt held right now" independently
// of the current event's own Alt field.)
//
// Alt+Enter is Windows Console's OWN fullscreen-toggle shortcut and never
// reaches this process at all — a verification round confirmed it.
// Ctrl+J is newline's actual working binding: '\n' decodes to KeyCtrlJ
// unconditionally in key_windows.go's control-character table, with no OS
// binding to compete with. Alt+Enter is kept alongside it — harmless here,
// and it may still work on a terminal/OS that does not intercept it.
func (w *Workspace) handleInputKey(k tea.KeyMsg) tea.Cmd {
	in := &w.input.buf
	width := w.input.textWidth

	// F5's ONE lock: at queue capacity, editing is refused until the
	// pending message is discarded (esc) or pulled back (ctrl+e). Both of
	// those are chords the root owns, so they never arrive here — which is
	// why refusing everything that does is safe. The reason is on screen
	// the whole time (render.go's queryLines); a lock the user cannot
	// explain is the failure F05-streaming-queue.md checks for by name.
	if w.inputLocked() {
		return nil
	}

	switch k.Type {
	case tea.KeyEnter:
		if k.Alt {
			in.InsertText("\n")
			break
		}
		// An Enter arriving right after a paste is that paste's own trailing
		// newline, not a send. On Windows a paste is replayed as keystrokes,
		// one console record per character, and a multi-line clipboard ends
		// with a real Enter; if the burst detector (app\paste.go) ended
		// the burst a beat early -- which a render backlog makes likely, see
		// app\render.go's View -- that final Enter is delivered on its
		// own and reaches Send, clearing the whole composed message. That is
		// a verification round's Bug 1, and this guard makes it structurally
		// impossible rather than merely unlikely: within pasteSettle of the
		// last PasteMsg, Enter inserts a newline (what the clipboard actually
		// said) instead of sending.
		if time.Since(w.input.lastPasteAt) < pasteSettle {
			in.InsertText("\n")
			break
		}
		return w.onSend()
	case tea.KeyCtrlJ:
		in.InsertText("\n")

	case tea.KeyLeft:
		in.MoveLeft(false, width)
	case tea.KeyShiftLeft:
		in.MoveLeft(true, width)
	case tea.KeyCtrlLeft:
		in.MoveWordLeft(false)
	case tea.KeyCtrlShiftLeft:
		in.MoveWordLeft(true)

	case tea.KeyRight:
		in.MoveRight(false, width)
	case tea.KeyShiftRight:
		in.MoveRight(true, width)
	case tea.KeyCtrlRight:
		in.MoveWordRight(false)
	case tea.KeyCtrlShiftRight:
		in.MoveWordRight(true)

	case tea.KeyUp:
		in.MoveUp(false, width)
	case tea.KeyShiftUp:
		in.MoveUp(true, width)
	case tea.KeyDown:
		in.MoveDown(false, width)
	case tea.KeyShiftDown:
		in.MoveDown(true, width)

	case tea.KeyHome:
		in.MoveHome(false, width)
	case tea.KeyShiftHome:
		in.MoveHome(true, width)
	case tea.KeyEnd:
		in.MoveEnd(false, width)
	case tea.KeyShiftEnd:
		in.MoveEnd(true, width)

	case tea.KeyBackspace:
		switch {
		case k.Alt:
			in.DeleteWordLeft() // the working alias — see doc above
		case in.cursor == 0 && !in.hasSelection() && len(w.input.chips) > 0:
			// "Backspace at position 0 removes the last chip." — and now
			// (F8) actually detaches it on the backend too; see
			// attachments.go's detachChip doc for why a local-only splice
			// stopped being correct the moment a chip became a real row.
			return w.detachChip(len(w.input.chips) - 1)
		default:
			in.Backspace()
		}
	case tea.KeyDelete:
		in.Delete()

	case tea.KeyCtrlA:
		in.SelectAll()
	case tea.KeyCtrlZ:
		in.Undo()
	case tea.KeyCtrlY:
		in.Redo()

	case tea.KeyRunes, tea.KeySpace:
		text := string(k.Runes)
		if k.Type == tea.KeySpace {
			text = " "
		}
		// Captured BEFORE InsertText moves it: whether this '@' landed at
		// buffer index 0 decides whether the picker it opens may offer
		// agents at all (app.OpenAttachmentPickerMsg's own doc — only the
		// LEADING @token can ever name one, per B12's invocation rule).
		// The trigger itself fires identically either way: an '@' typed
		// here is an '@' typed here, position never gates whether the
		// picker opens — see input_workspace_test.go's
		// TestOpenAttachmentPickerFiresAtIndexZeroAndElsewhere for the
		// regression this guards, the picker-opening counterpart to F04's
		// own Bug 3 (agentTokenState's cursor-bounded pending token).
		leading := in.cursor == 0
		in.InsertText(text)
		if text == "@" {
			return tea.Batch(w.scheduleDraftSave(), openPickerCmd(leading))
		}
		// DECIDED, not a gap: the picker opens on the '@' KEYSTROKE EVENT,
		// never on a live recompute of "is there an unclosed @ token near
		// the cursor" the way agentTokenState recomputes the mode chip on
		// every keystroke. Esc (Popup's own handleKey, routed through
		// Root — this method never sees it while the popup is open)
		// dismisses without touching this buffer at all, so the '@'
		// stays, as ordinary typed text — exactly as it would if the
		// picker had never existed. Typing more after a dismissal (e.g.
		// '?', making the buffer read "@?") does NOT reopen it: a
		// verification round asked this be confirmed as intentional
		// rather than silently assumed. It is. Re-deriving "should the
		// picker be open" from buffer content on every keystroke would
		// mean tracking a SECOND fact — "has the user already dismissed
		// THIS '@'" — independent from the buffer text itself, which is
		// precisely the two-sources-of-truth shape this codebase has
		// already been bitten by three times (DiagnosticsMsg.Seq,
		// taskCardWasVisible, B23 existing at all). The '@?' explorer
		// gesture the spec describes needs no such mechanism: typed
		// without an intervening Esc, the '?' lands in the POPUP's own
		// filter box (focus is trapped while it is open), reaching
		// onPickerFilterChange directly — see picker.go. Esc truly means
		// "dismiss"; a fresh '@' is what reopens it, exactly as backspacing
		// the stray text and retyping '@' already does today.

	default:
		return nil
	}

	return w.scheduleDraftSave()
}

// handleInputPaste is app.PasteMsg's destination while the input is
// focused: the whole pasted block lands as one InsertText call — one undo
// step, one draft, never N keystrokes and never a send.
func (w *Workspace) handleInputPaste(text string) tea.Cmd {
	if w.inputLocked() {
		return nil // the lock covers pasting, not just typing
	}
	w.input.lastPasteAt = time.Now()
	w.input.buf.InsertText(text)
	return w.scheduleDraftSave()
}

// onSend moved to stream.go in F5 — this is the seam F4's own stub named,
// and it is now the real send: CmdSendMessage, a live entry, and the queue.
// The clear is still its last step, exactly as that stub asked.

// openPickerCmd emits app.OpenAttachmentPickerMsg — F4's whole
// implementation of "ask for the picker when @ is typed" was a local type
// nothing consumed; F8 moved it to app (see that type's own doc for why:
// Root, not this workspace, owns OpenOverlay and the backend round trips
// the picker needs).
func openPickerCmd(leading bool) tea.Cmd {
	return func() tea.Msg { return app.OpenAttachmentPickerMsg{Leading: leading} }
}

// InputFocused satisfies app.FocusAware — see app\focus.go and the
// pane.focus.next / chat.browse actions in app\actions.go.
func (w *Workspace) InputFocused() bool { return w.input.focused }

// enterBrowseMode and enterInputMode back app.EnterBrowseMsg/FocusInputMsg.
// Deliberately minimal: they flip exactly one bool each and touch nothing
// else — F3's existing lazy "first j/k press focuses the last message"
// behaviour (scroll.go's moveFocus) already covers what happens next in
// browse mode, and re-entering input mode should not disturb wherever the
// transcript was scrolled to.
func (w *Workspace) enterBrowseMode() tea.Cmd {
	w.input.focused = false
	return nil
}

func (w *Workspace) enterInputMode() tea.Cmd {
	w.input.focused = true
	return nil
}
