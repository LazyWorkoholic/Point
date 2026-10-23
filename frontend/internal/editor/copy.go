package editor

import (
	"fmt"
	"io"
	"os"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// Copy, and why ctrl+c means ONLY this in the Editor.
//
// # The Editor had no copy at all until now
//
// F13a built selection -- anchor, cursor, the painted highlight, every
// motion that extends it -- and nothing that could copy what was
// selected. There was no clipboard call anywhere in this package. What
// F15's gate reported as "ctrl+c breaks copy" was therefore not a
// regression: `ctrl+c` is chat.cancel's chord with a Chat-only `When`, so
// `dispatchKey` answered "Cancel Stream is not available right now" and
// the Editor never saw the key, while the selection had no way to be
// copied by any key.
//
// # ctrl+c is copy, with no exceptions, and esc is the only cancel
//
// F15's Build names both keys for cancelling ("Cancelling (`esc` or
// `ctrl+c`) discards the ghost"). This diverges from it deliberately, and
// the reasoning is worth keeping because the alternative is superficially
// more faithful:
//
// Scoping `ctrl+c` -- cancel while something streams, copy otherwise --
// keeps the spec's letter and makes the key's meaning depend on state the
// user cannot see. That is the failure class this episode has already paid
// for four times (a chord arriving as a letter, `esc` not reaching the
// workspace, an applied card swallowing `ctrl+z`, a deferred card gated on
// a flag that never cleared). `ctrl+c` is the worst candidate for a fifth,
// because its failure is SILENT and in the dangerous direction: a ctrl+c
// that cancelled instead of copying leaves the user believing they have
// the text, and the next paste puts something stale somewhere else.
//
// `esc` already cancels, in both modes, and is F15's own primary wording
// ("`esc` cancels"); ctrl+c appears only in that parenthetical. So esc
// keeps cancel, ctrl+c keeps copy, and neither has a second meaning.
//
// Chat is unaffected: CapturesKey is consulted for the ACTIVE workspace
// only, so ctrl+c still cancels a stream there.
//
// # OSC 52, not a system clipboard call
//
// The same mechanism chat\copy.go uses, for the same reason its own doc
// gives: OSC 52 is what lets a copy work over SSH, where the terminal
// owns no clipboard that a mouse selection could reach. Duplicated rather
// than shared -- four lines of escape sequence, in two packages that must
// not import each other, the same call localID and deltaText already
// make.

// clipWriter is where the escape sequence goes. A field so a test can
// read what was copied: the sequence is not visible text and never
// reaches a frame, so there is nothing else to assert against.
//
// Defaults to stderr, which is where chat sends its own and is otherwise
// silent from this side (backend logs go to their own file).
func (w *Workspace) clipTarget() io.Writer {
	if w.clipOut != nil {
		return w.clipOut
	}
	return os.Stderr
}

// copySelection copies the active document's selection.
//
// SELECTION ONLY. Many editors copy the whole line when nothing is
// selected, and that is deliberately not done here: a ctrl+c pressed by
// accident would then replace the clipboard with a line the user never
// chose, which is the same silent-clobber problem this key is being
// simplified to avoid. Nothing selected says so and copies nothing.
func (w *Workspace) copySelection() tea.Cmd {
	d := w.activeDoc()
	if d == nil {
		return w.say("nothing open to copy from")
	}
	if !d.hasSelection() {
		return w.say("nothing selected — select with shift and the arrows, then ctrl+c")
	}
	from, to := d.selectionRange()
	text := d.buf.Slice(from, to)
	if text == "" {
		return w.say("nothing selected to copy")
	}
	if _, err := fmt.Fprint(w.clipTarget(), osc52.New(text)); err != nil {
		w.log.Warn("editor: writing the clipboard escape", "err", err)
		return w.say("could not reach the terminal's clipboard")
	}
	w.log.Debug("editor: copied a selection", "file", d.name(), "bytes", len(text))
	return w.say(fmt.Sprintf("copied %d byte(s)", len(text)))
}
