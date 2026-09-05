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
	// F5: "focused" spans entries.go's three lists now, so a streaming or
	// finished live reply is as copyable as a persisted one. focusedIndex
	// keeps the pinned-mode convention — no explicit focus means the
	// newest entry, so `y` works without pressing k first.
	text, ok := w.entryText(w.focusedIndex())
	if !ok {
		w.copyNote = "nothing focused to copy"
		return
	}
	fmt.Fprint(os.Stderr, osc52.New(text))
	w.copyNote = fmt.Sprintf("copied %d byte(s)", len(text))
}
