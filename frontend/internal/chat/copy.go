package chat

import (
	"fmt"
	"os"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// copyFocused sends the focused message's RAW text — msg.Content.Text, the
// source, never a rendered box or accent-bar line — via OSC 52, which is
// what lets `y` work over SSH where the terminal owns no clipboard mouse
// selection could reach.
//
// The escape sequence is written straight to stderr rather than routed
// through Bubble Tea's renderer: it is not visible text, so it cannot
// corrupt a frame, and stderr is otherwise silent from this side (backend
// logs go to their own file, never here).
func (w *Workspace) copyFocused() {
	idx := w.focus
	if idx < 0 {
		// Pinned mode has no explicit focus yet — same convention as
		// moveFocus: "focused" defaults to whatever is on screen at the
		// bottom, so `y` works immediately without pressing k first.
		idx = len(w.all) - 1
	}
	if idx < 0 || idx >= len(w.all) {
		w.copyNote = "nothing focused to copy"
		return
	}
	text := w.all[idx].Content.Text
	fmt.Fprint(os.Stderr, osc52.New(text))
	w.copyNote = fmt.Sprintf("copied %d byte(s)", len(text))
}
