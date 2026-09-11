package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/lazyworkoholic/point/frontend/internal/theme"
)

// One frame, one height.
//
// renderLive's two staleness gates are both CLOCK-based (the 50ms throttle,
// and per-second staleness for a running slot's timer), and a frame renders
// each entry several times: bottomAnchored walks backward measuring, then
// visibleWindow walks forward composing, with every other visible entry
// rendered in between. Straddle the throttle boundary and the measuring
// pass gets stale, SHORTER lines while the composing pass does a real,
// taller render -- so (topAbs, topRow), an address computed against the
// first height, is applied to the second and rows fall off the window.
//
// The window is widest exactly where an entry's height JUMPS, which is the
// moment a code fence closes: a raw tail becomes a boxed code block. It is
// also most durable at the end of a stream, because armTick stops once
// Streaming() goes false, so nothing repaints the bad frame.
func TestEntryHeightIsStableWithinAFrame(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	w := &Workspace{theme: th, focus: -1}

	open := "Here is how:\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	e := &liveEntry{
		kind: liveReply, lane: laneTurn,
		text: open, status: statusRunning, startedAt: time.Now(),
	}
	w.live = []*liveEntry{e}

	// Frame 1 renders the open fence and caches it -- the state a live
	// stream leaves behind on every token.
	w.frame++
	if h := len(w.entryBlock(0, 70)); h == 0 {
		t.Fatal("nothing rendered")
	}

	// The closing fence arrives, and the cache is left sitting just inside
	// the throttle window, as it would be a few tokens into a reply.
	e.text = open + "```\n"
	e.touch()
	e.renderedAt = time.Now().Add(-(streamThrottle - time.Millisecond))

	// Both of these are ONE frame: the measuring pass and the composing
	// pass, with the throttle boundary falling between them.
	w.frame++
	measured := len(w.entryBlock(0, 70))
	time.Sleep(3 * time.Millisecond)
	composed := len(w.entryBlock(0, 70))

	if measured != composed {
		t.Fatalf("height changed mid-frame: measured %d rows, composed %d -- "+
			"(topAbs, topRow) is computed against the first and applied to the second",
			measured, composed)
	}

	// Across frames it must still be free to change: the whole point of the
	// throttle is that an update lands on a LATER frame, not never.
	w.frame++
	if next := len(w.entryBlock(0, 70)); next == measured {
		t.Fatalf("height never updated across frames (%d rows): the closed fence "+
			"should have become a boxed block", next)
	}
}

// The height change this is all about is the fence closing, so assert the
// box itself actually arrives -- a stable height that is stably WRONG would
// pass the test above.
func TestClosedFenceBecomesABoxedBlock(t *testing.T) {
	th := theme.New("turquoise", theme.BackgroundTransparent)
	w := &Workspace{theme: th, focus: -1}

	open := "Here is how:\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	e := &liveEntry{
		kind: liveReply, lane: laneTurn,
		text: open, status: statusRunning, startedAt: time.Now(),
	}
	w.live = []*liveEntry{e}

	w.frame++
	if got := stripSGR(strings.Join(w.entryBlock(0, 70), "\n")); strings.Contains(got, "╭") {
		t.Fatalf("an UNCLOSED fence must stay a raw tail, not a box:\n%s", got)
	}

	e.text = open + "```\n"
	e.touch()
	e.renderedAt = time.Time{} // let it render; the throttle is not what is under test

	w.frame++
	got := stripSGR(strings.Join(w.entryBlock(0, 70), "\n"))
	if !strings.Contains(got, "╭") {
		t.Fatalf("a CLOSED fence did not become a boxed code block:\n%s", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Fatalf("the code itself went missing:\n%s", got)
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == 0x1b {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
