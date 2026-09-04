package chat

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/layout"
)

// A verification round's QUESTION: is rapid repeated Ctrl+V's lag doing
// worse-than-linear work (repeated string concatenation, re-wrapping the
// whole buffer per chunk, re-rendering per keystroke)? Measured, not
// guessed at, per the instruction not to change anything first.
//
// BenchmarkInsertLargePaste isolates the DATA side: one InsertText call for
// a ~14KB, 391-line paste — splice.go always allocates one fresh slice
// (input.go's splice), so this is O(buffer size) once per paste, not per
// character; no quadratic string concatenation anywhere in the path.
func BenchmarkInsertLargePaste(b *testing.B) {
	text := strings.Repeat("this is one line of pasted content\n", 391)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := newInputBuffer()
		buf.InsertText(text)
	}
}

// BenchmarkViewDuringGrownInputAndBigSession isolates the RENDER side: one
// full Workspace.View call — markdown rendering, the transcript's windowed
// scroll, and the query input's own wrap — at a realistic size (2,000
// messages, input grown to several lines). This is what actually runs once
// per KeyMsg while a burst is still assembling (app\paste.go delivers one
// KeyMsg per pasted character on Windows; Bubble Tea calls View again after
// every Update, whether or not the message changed anything visible), so
// its cost here times roughly one call per pasted character is the real
// budget for "does a big paste feel laggy," not the insertion itself.
func BenchmarkViewDuringGrownInputAndBigSession(b *testing.B) {
	w := New(Options{Theme: testTheme()})
	w.bootstrapped = true
	w.all = fakeMessages(2000)
	w.input.buf.InsertText(strings.Repeat("one line of typed text\n", 60))
	l := layout.Compute(120, 30, layout.State{Active: layout.WorkspaceChat, HeaderAllowed: true})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.View(l)
	}
}
