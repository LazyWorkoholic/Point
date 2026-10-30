package chat

import (
	"testing"
	"time"
)

// #1, as reported: a task that finishes while Chat is NOT the active
// workspace must still reach finish(), or its slot keeps counting from a
// start time whose task is over.
//
// Before WantsBackgroundReplies existed, Root skipped Chat entirely in
// deliverBackgroundReply, so the terminal envelope never arrived and the
// slot read `⟳ running 1:02` for a task that took 15 seconds.

// The gate Root asks: true exactly while something is in flight.
func TestChatWantsRepliesOnlyWhileWorkIsInFlight(t *testing.T) {
	w := &Workspace{}
	if w.WantsBackgroundReplies() {
		t.Error("an idle Chat asks for background replies")
	}

	running := &liveEntry{kind: liveReply, status: statusRunning}
	w.live = append(w.live, running)
	if !w.WantsBackgroundReplies() {
		t.Fatal("a Chat with a running reply does NOT ask for background replies -- its completion would be dropped")
	}

	// Once it is over, it stops asking.
	running.status = statusDone
	if w.WantsBackgroundReplies() {
		t.Error("a Chat with nothing in flight still asks")
	}

	// A task-lane reply counts too: that is the @research case.
	w.live = append(w.live, &liveEntry{kind: liveReply, lane: laneTask, status: statusRunning})
	if !w.WantsBackgroundReplies() {
		t.Error("a running task slot does not keep the subscription open")
	}
}

// The actual symptom: with the completion applied, the slot renders the
// REAL duration; without it, it renders wall-clock-since-start.
func TestAFinishedSlotRendersItsRealDurationNotWallClock(t *testing.T) {
	w := &Workspace{}
	e := &liveEntry{
		kind:      liveReply,
		lane:      laneTask,
		status:    statusRunning,
		startedAt: time.Now().Add(-62 * time.Second), // the reported 1:02
	}
	w.live = append(w.live, e)

	th := testTheme()
	before := renderTaskEntry(th, e, 60, false)
	if len(before) == 0 || !containsAny(before, "1:0") {
		t.Fatalf("a running slot should be counting wall-clock: %q", before)
	}

	// What the dropped envelope would have done.
	e.status = statusDone
	e.finishedAt = e.startedAt.Add(15 * time.Second)

	after := renderTaskEntry(th, e, 60, false)
	if !containsAny(after, "0:15") {
		t.Errorf("a finished slot must show its real duration, got %q", after)
	}
	if containsAny(after, "1:0") {
		t.Errorf("a finished slot is still counting wall-clock: %q", after)
	}
}

func containsAny(lines []string, want string) bool {
	for _, l := range lines {
		if len(l) > 0 && contains(stripANSI(l), want) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
