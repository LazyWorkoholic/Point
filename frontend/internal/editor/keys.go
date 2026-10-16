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

	// Keys that work in either pane.
	switch key {
	case "ctrl+o":
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
	case "ctrl+shift+l":
		// Take over a stale lock. `l` for lock; ctrl+shift+l is free in
		// the chord registry and needs no KeyCapturer claim, which
		// keeps this episode's claim list at F13a's two keys.
		//
		// A binding rather than a clickable "[take over]" in the status
		// bar: the status bar is rendered by Root from parts a
		// workspace supplies (StatusContributor) and has no hit-testing
		// of its own, so a bracketed label there would look pressable
		// and not be. The segment names the key instead.
		return w.takeOverActive()
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

	if w.focus == focusTree {
		return w.treeKey(key)
	}
	return w.bufferKey(k, key)
}

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
		// With nothing open, the arrow keys move the tree instead of
		// doing nothing: it is the only thing on screen that can be
		// navigated, and an editor that ignores every key until a file is
		// open reads as frozen.
		return w.treeKey(key)
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
