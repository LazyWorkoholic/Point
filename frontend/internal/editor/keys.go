package editor

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Key handling.
//
// # What reaches here, and what does not
//
// dispatchKey (app\root.go) consults an open overlay, then the leader, then
// KeyCapturer, then the chord registry, and only then this workspace. So
// `tab` and `shift+tab` arrive because CapturesKey claims them; everything
// else arrives only if no chord owns it.
//
// Four chords are owned by Chat-only actions whose When predicate is false
// here, and dispatchKey SWALLOWS those with a status line rather than
// falling through: esc (chat.browse), ctrl+c (chat.cancel), ctrl+e
// (chat.queue.edit) and ctrl+r (chat.retry). None is load-bearing for this
// episode -- the bindings below are chosen from what is actually free --
// but it is why the tree key is ctrl+o rather than LAYOUT.md's ^E, and it
// is recorded in the handoff rather than worked around by claiming more
// keys.
//
// # ctrl+tab does not exist on Windows
//
// F13 asks for ctrl+tab to cycle tabs. It cannot: bubbletea's Key struct
// carries only Type, Runes, Alt and Paste (no Ctrl field), and its Windows
// console-record reader maps VK_TAB to KeyTab whether or not Ctrl is held
// (key_windows.go). So ctrl+tab and tab are the same KeyMsg and String()
// returns "tab" for both -- there is nothing to distinguish, and claiming
// it would break Tab-indents. ctrl+n / ctrl+b cycle instead, both free in
// the chord registry, and the empty state and the status bar advertise
// them. This is exactly the class of finding F13's Handoff asks to record.

func (w *Workspace) handleKey(k tea.KeyMsg) tea.Cmd {
	key := k.String()

	// COPY, always, before anything else can claim the key.
	//
	// Above the modal surfaces on purpose: ctrl+c means copy in this
	// workspace whatever is on screen, and a prompt or panel that took it
	// as a cancel would be the scoped-ctrl+c design copy.go rejects.
	if key == "ctrl+c" {
		return w.copySelection()
	}

	// F15's two modal surfaces come FIRST, before the keys that work in
	// either pane and before the pane split. Both are things the user just
	// opened and is looking at, and neither can share the keyboard with a
	// text buffer: the prompt is being typed into, and the panel scrolls
	// with the same keys that would otherwise move the caret.
	//
	// The prompt is checked before the panel because only one of the two
	// can be open at a time in practice (fill opens the prompt, explain
	// opens the panel) and a fixed order is still worth having over
	// whichever-happens-to-be-non-nil.
	if w.prompt != nil {
		switch w.prompt.key(k) {
		case promptSubmit:
			return w.submitPrompt()
		case promptCancel:
			w.prompt = nil
			return w.say("fill cancelled — nothing was sent")
		}
		return nil
	}
	if w.panel != nil {
		return w.panelKey(k)
	}

	// esc with a fill STREAMING and no surface open: the ghost is on
	// screen and the task is running, which is Done-when 3c. Reached
	// because CapturesKey claims esc while a surface is open -- and the
	// ghost counts as one, since it is what esc is cancelling.
	if key == "esc" && w.liveInvocation() {
		return w.cancelInvocation()
	}

	// Keys that work in either pane.
	switch key {
	case "ctrl+o":
		// Reads the STORED focus, not effectiveFocus().
		//
		// Reading the derived value was tried and removed: the two
		// differ only while nothing is open, and in that state
		// effectiveFocus ignores the stored field entirely -- while
		// onFile re-derives it through keptByTree the moment a
		// document arrives. So the change had no observable effect in
		// any sequence, could not be given a failing test, and was a
		// divergence from the field for no gain. Noted so it is not
		// reintroduced as an obvious tidy-up.
		if w.focus == focusTree {
			w.focus = focusBuffer
		} else {
			w.focus = focusTree
		}
		return nil
	case "ctrl+n":
		return w.cycleTab(+1)
	case "ctrl+b":
		return w.cycleTab(-1)
	case "ctrl+w":
		// Not reachable: ctrl+w is the LEADER (actions.Leader) and is
		// intercepted before this workspace ever sees a key. Listed so
		// the next person to look for a close-tab binding finds the
		// reason here instead of wondering why it does nothing.
		return nil
	case "ctrl+s":
		return w.saveActive()
	case "f6":
		// Take over a lock whose holder has gone quiet.
		//
		// f6, NOT ctrl+shift+l, which is what this was first bound to
		// and which does not exist: Ctrl+L and Ctrl+Shift+L produce the
		// same control character on Windows, so both arrive as `ctrl+l`
		// -- app.redraw's shipped chord, consulted before this
		// workspace. The take-over was unreachable. Same shape as
		// F13a's ctrl+tab finding; see lock.go's takeOverActive.
		//
		// f6 is free in the registry (the chords are ctrl+c, ctrl+e,
		// ctrl+l, ctrl+p, ctrl+q, ctrl+r, esc, f1 and tab), needs no
		// KeyCapturer claim, and keeps this episode's claim list at
		// F13a's two keys.
		//
		// A binding rather than a clickable "[take over]" in the status
		// bar: the status bar is rendered by Root from parts a
		// workspace supplies (StatusContributor) and has no hit-testing
		// of its own, so a bracketed label there would look pressable
		// and not be. The segment names the key instead.
		return w.takeOverActive()
	case "f7":
		// Close the active tab, through the SAME path the tab strip's
		// x uses -- so a dirty file still routes through C3's
		// [ Close ] [ Cancel ] confirm (Done-when 9) rather than
		// discarding.
		//
		// # Why f7 and not ctrl+w or ctrl+shift+w
		//
		// ctrl+w is the LEADER (actions.Leader), intercepted before
		// this workspace ever sees a key -- that is what makes
		// ctrl+w e and ctrl+w w work, so it cannot also mean close.
		//
		// ctrl+shift+w is not a key on Windows either, and for the
		// reason f6 exists: bubbletea's console-record reader produces
		// the same control character for Ctrl+W and Ctrl+Shift+W, so
		// ctrl+shift+w arrives as `ctrl+w` -- the leader again, which
		// would make close-tab open the leader hint instead. Measured
		// for Ctrl+Shift+L in this episode and the same mechanism.
		//
		// So: a function key, and f7 keeps the editor's function-key
		// block contiguous and in a memorable order -- f5 re-index,
		// f6 take over the lock, f7 close the tab. Nothing in the
		// chord registry claims it (the chords are ctrl+c, ctrl+e,
		// ctrl+l, ctrl+p, ctrl+q, ctrl+r, esc, f1 and tab), so it
		// needs no KeyCapturer claim and this episode's claim list
		// stays at F13a's two keys.
		if len(w.docs) == 0 {
			return nil
		}
		note, cmd := w.closeTab(w.active)
		return tea.Batch(w.say(note), cmd)
	case "f5":
		// Re-index the tree. f5 rather than ctrl+r, which has been
		// chat.retry's chord since F5 and is therefore swallowed here
		// with "Retry is not available right now" -- see the header. f5
		// is free in the registry, is what every other tool uses for
		// refresh, and needs no claim.
		//
		// There is no filesystem watcher (F13's own "Do NOT"), and
		// _build_index caches for 30 seconds, so this is the only way to
		// see a file created outside Point without waiting.
		w.refreshTree(true)
		return w.say("re-indexing the project")
	}

	if w.effectiveFocus() == focusTree {
		return w.treeKey(key)
	}
	return w.bufferKey(k, key)
}

// treeKey is the file tree's keymap.
//
// # The vim motions are DELIBERATE, and this is where they are written down
//
// Reported as "`l` behaves like Enter -- intentional or a stray keymap
// entry?". Intentional, and it is a complete set rather than one stray
// binding: k/j move, l expands a folder or opens a file, h collapses.
// They mirror chat's own browse keys (keys_test.go's
// TestTreeNavigationKeys says so), which is why they are kept rather
// than removed -- dropping them would be a regression for anyone who
// has been using them since F13a.
//
//	up    k     down  j
//	right l     expand a folder, or open a file
//	left  h     collapse
//	enter       open a file, or expand a folder
//	pgup/pgdown home/end
//
// `l` and Enter really are equivalent on a FILE (toggle() reports
// false, so both fall through to openSelected) and on a FOLDER (both
// expand it). The two spellings exist for the hand, not for different
// behaviour.
func (w *Workspace) treeKey(key string) tea.Cmd {
	rows := w.treeRect.H - 2
	if rows < 1 {
		rows = 1
	}
	switch key {
	case "up", "k":
		w.tree.move(-1)
	case "down", "j":
		w.tree.move(+1)
	case "pgup":
		w.tree.move(-rows)
	case "pgdown":
		w.tree.move(+rows)
	case "home":
		w.tree.sel = 0
	case "end":
		w.tree.sel = len(w.tree.rows) - 1
	case "right", "l":
		if !w.tree.toggle() {
			return w.openSelected()
		}
	case "left", "h":
		w.tree.collapse()
	case "enter":
		return w.openSelected()
	}
	return nil
}

func (w *Workspace) bufferKey(k tea.KeyMsg, key string) tea.Cmd {
	if len(w.docs) == 0 {
		// Nothing open and nothing to type into. This used to forward
		// to treeKey, which was F13a's answer to "an editor that
		// ignores every key until a file is open reads as frozen" --
		// and the symptom was worse than the disease: the arrow keys
		// moved the TREE's selection while the buffer pane was the one
		// drawn as focused, so the focus indicator and the keys
		// disagreed and ctrl+o appeared to do nothing.
		//
		// The frozen-editor problem is real, and the fix is to give the
		// tree the focus for real rather than to route one pane's keys
		// into another: focusWhereThereIsSomething does that, so the
		// arrows work, the focused border is on the pane they move, and
		// routing stays scoped to whoever actually has the keyboard.
		return nil
	}
	d := w.docs[w.active]
	rows := w.textArea.H
	if rows < 1 {
		rows = 1
	}

	switch key {
	// -- movement -------------------------------------------------------
	case "left":
		d.moveLeft(false)
	case "right":
		d.moveRight(false)
	case "up":
		d.moveVertical(-1, false)
	case "down":
		d.moveVertical(+1, false)
	case "shift+left":
		d.moveLeft(true)
	case "shift+right":
		d.moveRight(true)
	case "shift+up":
		d.moveVertical(-1, true)
	case "shift+down":
		d.moveVertical(+1, true)
	case "ctrl+left":
		d.moveWord(-1, false)
	case "ctrl+right":
		d.moveWord(+1, false)
	case "ctrl+shift+left":
		d.moveWord(-1, true)
	case "ctrl+shift+right":
		d.moveWord(+1, true)
	case "home":
		d.moveHome(false)
	case "end":
		d.moveEnd(false)
	case "shift+home":
		d.moveHome(true)
	case "shift+end":
		d.moveEnd(true)
	case "pgup":
		d.moveVertical(-rows, false)
	case "pgdown":
		d.moveVertical(+rows, false)
	case "shift+pgup":
		d.moveVertical(-rows, true)
	case "shift+pgdown":
		d.moveVertical(+rows, true)
	case "ctrl+home":
		d.moveDocStart(false)
	case "ctrl+end":
		d.moveDocEnd(false)
	case "ctrl+shift+home":
		d.moveDocStart(true)
	case "ctrl+shift+end":
		d.moveDocEnd(true)

	// -- selection ------------------------------------------------------
	case "ctrl+a":
		d.selectAll()

	// -- indent ---------------------------------------------------------
	case "tab":
		d.indentSelection()
	case "shift+tab":
		d.dedentSelection()

	// -- edit -----------------------------------------------------------
	case "enter":
		d.newline()
	case "backspace":
		d.backspace()
	case "delete":
		d.deleteForward()
	case "ctrl+h":
		// Windows reports ctrl+backspace as ctrl+h on the console path;
		// both spellings get the word delete.
		d.deleteWordLeft()
	case "ctrl+z":
		d.Undo()
	case "ctrl+y", "ctrl+shift+z":
		d.Redo()

	default:
		if k.Type == tea.KeyRunes && !k.Alt {
			for _, r := range k.Runes {
				d.insertRune(r)
			}
			return nil
		}
		if k.Type == tea.KeySpace {
			d.insertRune(' ')
		}
	}
	return nil
}
