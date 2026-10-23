package editor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F15's backgrounded-workspace path, driven through the REAL Root.
//
// # Why this harness exists, and why the earlier test was not enough
//
// TestTheGhostPathNeverWritesTheFile asked `WantsBackgroundReplies()` as a
// PREDICATE and then kept streaming into the same workspace object. That
// proves the workspace would ACCEPT a backgrounded event; it proves nothing
// about what Root actually delivers to an inactive workspace, or about what
// happens to the commands that workspace returns. Those are different code
// paths and the difference is where two real defects were living.
//
// So this file builds a Root with both workspaces registered, switches
// between them with the actual leader chords (ctrl+w c / ctrl+w e), and
// dispatches every envelope through Root.Update -- the same entry point the
// Bubble Tea runtime uses. Nothing here calls a workspace method directly.

// rootRig is a Root with a real Editor and a stand-in Chat.
type rootRig struct {
	root *app.Root
	ed   *Workspace
	sent *[]sentQuery
}

// chatStub is the minimum app.Workspace Root will switch to. It is NOT a
// chat.Workspace: `chat` imports `app`, `editor` imports `app`, and a test
// in this package importing `chat` would pull the whole transcript renderer
// in to stand in for "somewhere else to be".
type chatStub struct{}

func (c *chatStub) Init() tea.Cmd                           { return nil }
func (c *chatStub) Update(tea.Msg) (app.Workspace, tea.Cmd) { return c, nil }
func (c *chatStub) View(layout.Layout) string               { return "" }
func (c *chatStub) Suspend() ([]byte, error)                { return nil, nil }
func (c *chatStub) Resume([]byte) error                     { return nil }
func (c *chatStub) ID() app.WorkspaceID                     { return app.WorkspaceChat }
func (c *chatStub) Cards() []layout.CardSlot                { return nil }

// newRootRig builds the rig. settle is this workspace's proposal-settle
// delay, and each test picks it deliberately -- see the callers.
func newRootRig(t *testing.T, content string, settle time.Duration) *rootRig {
	t.Helper()

	ed := New(Options{Theme: testTheme()})
	var sent []sentQuery
	ed.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmd, kind, payload})
	}
	ed.writeFn = func(id, cmd string, payload map[string]any) error {
		sent = append(sent, sentQuery{cmd, queryUncorrelated, payload})
		return nil
	}
	ed.projectID, ed.projectRoot = "p1", `C:\p`
	ed.sessionID = "sess-editor"
	ed.holder = "tester@1"
	ed.autosaveMode = config.AutosaveAfterDelay
	ed.autosaveDelay = time.Millisecond
	ed.settleDelay = settle
	ed.docs = append(ed.docs, testDoc(t, content))
	ed.focus = focusBuffer

	root, err := app.New(app.Options{
		Theme: testTheme(),
		State: config.State{Provider: &config.ProviderDefault{Provider: "demo", Model: "m"}},
		Workspaces: []app.WorkspaceDef{
			// LeaderKey "z", and NOT the "c" the gate script's own DO
			// list said: `c` is pane.right.toggle's sequence and Chat
			// has NO leader key in the real app (main.go says so in
			// words -- "F12 asks for `c`, which [is taken] ... use the
			// switcher instead"), so a registry using it fails
			// Validate at boot. The real route is ctrl+w w and the
			// switcher popup.
			//
			// What this harness needs is a REAL Root-level switch, not
			// that particular chord, so it binds an unused key and
			// says so. The gate's own instruction was corrected
			// separately.
			{ID: app.WorkspaceChat, Name: "Chat", LeaderKey: "z",
				New: func() app.Workspace { return &chatStub{} }},
			{ID: app.WorkspaceEditor, Name: "Editor", LeaderKey: "e",
				New: func() app.Workspace { return ed }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root.StatusLinger = time.Millisecond

	rig := &rootRig{root: root, ed: ed, sent: &sent}
	rig.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	// UI-12 opens with nothing loaded and an open overlay takes every
	// key, leader included (dispatchKey's first branch). A real user
	// either picks a workspace there or dismisses it; app's own
	// lifecycle_test does the same esc for the same reason.
	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	// Into the Editor, the way a user gets there.
	rig.leader('e')
	if !rig.editorActive() {
		t.Fatal("the Editor did not become active")
	}
	return rig
}

// send dispatches one message through Root and keeps delivering whatever
// comes back, the way the Bubble Tea runtime does.
//
// Two things here are fidelity, not convenience, and the first one hid a
// bug from this very harness for one round:
//
//  1. tea.BatchMsg is EXPANDED. Running a batched command yields a
//     tea.BatchMsg carrying more commands, and the runtime expands it;
//     Root does not handle that type, so a harness that passed it to
//     Root.Update instead let every command inside the batch go
//     unexecuted. Root returns the background commands inside exactly
//     such a batch, so the first version of this file was still not
//     running the thing it was written to test.
//
//  2. A command that does not produce a message PROMPTLY is abandoned
//     rather than waited on. The real runtime runs each command in its
//     own goroutine and delivers whenever it finishes; a synchronous
//     harness instead BLOCKS for the full duration of any tea.Tick it
//     touches, and Root arms several long ones on a workspace switch
//     (the 2s suspend debounce among them). Waiting for those cost 58
//     seconds across three tests. Abandoning them models the truth for a
//     test this short: in a one-second session a two-second debounce has
//     not fired yet either.
func (rig *rootRig) send(msg tea.Msg) { rig.deliver(msg, 0) }

// cmdPatience is how long a command gets to produce its message before the
// harness stops waiting. Long enough for the timers these tests shrink to
// a millisecond, far short of the ones they do not care about.
const cmdPatience = 100 * time.Millisecond

func (rig *rootRig) deliver(msg tea.Msg, depth int) {
	if msg == nil || depth > 12 {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			rig.run(c, depth)
		}
		return
	}
	_, cmd := rig.root.Update(msg)
	rig.run(cmd, depth)
}

func (rig *rootRig) run(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 12 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case produced := <-done:
		rig.deliver(produced, depth+1)
	case <-time.After(cmdPatience):
		// Still pending. The runtime would deliver it later; this test
		// is over before later.
	}
}

// leader presses the real leader chord and then one key.
func (rig *rootRig) leader(key rune) {
	rig.send(tea.KeyMsg{Type: tea.KeyCtrlW})
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
}

// editorActive reads it off the FRAME rather than from Root's own field,
// which is unexported here -- and is the better question anyway: what the
// user is looking at.
func (rig *rootRig) editorActive() bool {
	return strings.Contains(stripANSI(rig.root.View()), "Code File")
}

func (rig *rootRig) envelope(evType string, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	corr := ""
	if rig.ed.inv != nil {
		corr = rig.ed.inv.requestID
	}
	rig.send(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: corr, TaskID: "task-1", SessionID: "sess-editor",
		Type: evType, Payload: raw,
	}})
}

// staleReply delivers the CheckFileStale answer for whichever card asked.
func (rig *rootRig) staleReply(changed bool) {
	raw, _ := json.Marshal(map[string]any{"changed": changed})
	id := "stale-rig"
	rig.ed.pending[id] = pendingReq{
		kind: queryStaleCheck, path: rig.ed.card.path, card: rig.ed.card,
	}
	rig.send(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: id, Type: protocol.EvFileStaleResult, Payload: raw,
	}})
}

// cardOnScreen reads it off the FRAME, which is the only honest place: a
// card can exist in the workspace's own state and not be on screen, and
// the difference between those two is what this file exists to test.
func (rig *rootRig) cardOnScreen() bool {
	return strings.Contains(stripANSI(rig.root.View()), "Proposed change")
}

func (rig *rootRig) wroteAnything() (sentQuery, bool) {
	for _, q := range *rig.sent {
		if q.cmd == protocol.CmdWriteProjectFile {
			return q, true
		}
	}
	return sentQuery{}, false
}

// ---------------------------------------------------------------------------
// The repro
// ---------------------------------------------------------------------------

// The exact human sequence: fill, switch to Chat mid-stream, let the task
// finish and the proposal arrive while the Editor is INACTIVE, switch back.
//
// Asserts both things at once, because the gate reported both and they
// might have been one fault: does a card open, and did anything reach disk.
func TestAProposalArrivingWhileBackgroundedStillOpensItsCard(t *testing.T) {
	const original = "def f():\n    pass"
	// A settle delay LONGER than the harness's patience, on purpose.
	//
	// In production the ordering is not in doubt: B24 emits TaskDone and
	// FillProposal from one synchronous call stack with no intervening
	// await, so the proposal is already queued when TaskDone is handled,
	// microseconds later -- which is what the real one-second delay
	// exists to guarantee. A synchronous harness cannot reproduce that by
	// shrinking the delay: it BLOCKS on each command, so any settle
	// shorter than cmdPatience fires before the test's next line runs,
	// which would be testing a race the backend rules out. Making it
	// longer than patience reproduces the real order instead -- the tick
	// is left pending, the proposal lands, and settleGen supersedes it.
	rig := newRootRig(t, original, 10*cmdPatience)

	// leader → f, type an instruction, Enter.
	rig.leader('f')
	if rig.ed.prompt == nil {
		t.Fatal("leader f did not open the prompt")
	}
	for _, r := range "add a docstring" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	if rig.ed.inv == nil {
		t.Fatal("Enter did not invoke")
	}
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{
		"text": `{"replacement": "def f():\n`,
	})

	// ctrl+w c -- away, for real.
	rig.leader('z')
	if rig.editorActive() {
		t.Fatal("the switch to Chat did not take")
	}

	// The rest of the stream, the terminal event, and THEN the proposal --
	// which is the order the backend guarantees: FillProposal is pushed
	// after TaskDone, never instead of it (B24).
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `    return 1"`})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.envelope(protocol.EvFillProposal, map[string]any{
		"proposal": map[string]any{
			"file": "sample.py", "start_line": 1, "end_line": 2,
			"replacement": "def f():\n    return 1", "note": "a note",
			"shown_start": 1, "shown_end": 2,
			"mtime": 1791623519.07509, "size": 4096,
		},
	})

	// THE ASSERTION THAT WAS MISSING, and the reason this test passed for
	// a round while the behaviour was plainly broken:
	//
	// an overlay is Root's and survives a workspace switch, so a card
	// that opened wrongly over Chat and was never closed produces an
	// IDENTICAL end state to one correctly opened on return. Asserting
	// only the end state cannot tell the two apart. So the state WHILE
	// STILL IN CHAT is asserted first.
	if rig.cardOnScreen() {
		t.Fatal("the diff card opened over CHAT. An overlay takes every key, so " +
			"the user's next keystroke in Chat goes into a card about another " +
			"workspace's file -- and the card's own apply message then falls " +
			"through Root to Chat and is dropped, so Enter does nothing")
	}
	if rig.ed.deferredCard == nil {
		t.Fatal("the card was not held for the switch back either, so it is lost")
	}

	// ctrl+w e -- back.
	rig.leader('e')

	// NOT editorActive() here: the diff card is a MaximisedOverlay and
	// covers the Code File box, so "is the Editor on screen" cannot be
	// read off that box once the card is up. What the frame must show is
	// the card itself, asserted below.
	if rig.ed.card == nil {
		t.Fatal("no diff card was built for a proposal that arrived while " +
			"the Editor was backgrounded")
	}
	if !rig.cardOnScreen() {
		t.Errorf("the card was built but never OPENED on return -- the ghost is " +
			"left frozen and ctrl+z does nothing, because nothing was ever spliced")
	}
	if rig.ed.deferredCard != nil {
		t.Error("the card is still held as deferred, so it would open a second time")
	}

	// And nothing may have reached disk at any point in that sequence.
	if q, ok := rig.wroteAnything(); ok {
		t.Errorf("a WriteProjectFile went out during a backgrounded fill: %v", q.payload)
	}
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer changed to %q with no apply", got)
	}
}

// The same sequence with NO proposal (the fixture rejected it, or the model
// refused): the settle timer has to fire even though it was armed while the
// workspace was inactive, or the ghost is left on screen forever.
func TestASettleArmedWhileBackgroundedStillFires(t *testing.T) {
	// SHORTER than the harness's patience: this test's whole subject is
	// the settle tick firing, so it must arrive inside the window the
	// harness waits for.
	rig := newRootRig(t, "def f():\n    pass", cmdPatience/4)

	rig.leader('f')
	for _, r := range "do it" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": "I cannot do that."})

	rig.leader('z')
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.leader('e')

	if rig.ed.ghost != nil {
		t.Error("the ghost was left on screen after a reply that proposed nothing")
	}
	if rig.ed.panel == nil {
		t.Error("the reply was never shown anywhere")
	}
	if _, ok := rig.wroteAnything(); ok {
		t.Error("a write went out on a refusal path")
	}
}

// The ghost keeps growing while the Editor is inactive -- Done-when 3b,
// through Root this time rather than by asking the predicate.
func TestTheGhostAccumulatesThroughRootWhileBackgrounded(t *testing.T) {
	// No terminal event here, so the settle never arms; long so it
	// cannot interfere either way.
	rig := newRootRig(t, "def f():\n    pass", 10*cmdPatience)

	rig.leader('f')
	for _, r := range "do it" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `{"replacement": "a`})

	rig.leader('z')
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `\nb\nc"`})
	rig.leader('e')

	if rig.ed.ghost == nil {
		t.Fatal("the ghost is gone")
	}
	if got := rig.ed.ghost.lines; len(got) != 3 {
		t.Errorf("ghost = %#v, want everything that arrived while away", got)
	}
}

// The reported six-step sequence, end to end, with assertions at every
// step rather than only at the end.
//
// This is the test the two previous rounds needed and did not have. Each
// earlier version asserted a state that was reachable both correctly and
// incorrectly: first "the workspace would accept a backgrounded event"
// (a predicate, never routed through Root), then "the card is on screen
// after coming back" (true as well of a card that wrongly opened over
// Chat and was never closed). What follows asserts the WHOLE path,
// including the two places it was wrong.
func TestTheWholeBackgroundedFillSequence(t *testing.T) {
	const original = "def f():\n    pass"
	rig := newRootRig(t, original, 10*cmdPatience)

	// 1-2. In the Editor: leader f, an instruction, Enter.
	rig.leader('f')
	for _, r := range "add a type check" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	if !rig.ed.onScreen {
		t.Fatal("the Editor does not know it is on screen")
	}

	// 3. Away to Chat.
	rig.leader('z')
	if rig.ed.onScreen {
		t.Fatal("the Editor still believes it is on screen after a switch away -- " +
			"which is the bug that made every deferral a no-op")
	}

	// 4. It finishes while the user is in Chat.
	rig.envelope(protocol.EvTextDelta, map[string]any{
		"text": `{"replacement": "def f():\n    return 1"`,
	})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.envelope(protocol.EvFillProposal, map[string]any{
		"proposal": map[string]any{
			"file": "sample.py", "start_line": 1, "end_line": 2,
			"replacement": "def f():\n    return 1", "note": "n",
			"shown_start": 1, "shown_end": 2, "mtime": 1.5, "size": 2,
		},
	})
	if rig.cardOnScreen() {
		t.Fatal("the card opened over Chat")
	}
	if rig.ed.ghost != nil {
		t.Error("the ghost was left on screen behind the deferred card")
	}

	// 5. Back to the Editor: now it opens.
	rig.leader('e')
	if !rig.cardOnScreen() {
		t.Fatal("the card did not open on return")
	}

	// 6. Enter applies -- through the real key path, so the card must
	// actually be the thing receiving the key.
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	if rig.ed.docs[0].buf.Text() != original {
		t.Error("the buffer was spliced before the on-disk check answered")
	}
	if _, asked := lastQueryOf(rig.sent, protocol.CmdCheckFileStale); !asked {
		t.Fatal("Enter on the card did nothing: no CheckFileStale was sent. " +
			"The apply message fell through Root to whichever workspace was " +
			"active instead of reaching the Editor")
	}
	rig.staleReply(false)
	if !rig.ed.card.Applied() {
		t.Fatalf("the apply was refused: %q", rig.ed.card.refusal)
	}
	if !strings.Contains(rig.ed.docs[0].buf.Text(), "return 1") {
		t.Errorf("buffer = %q, want the applied region", rig.ed.docs[0].buf.Text())
	}
	if rig.cardOnScreen() {
		t.Error("the applied card is still on screen, holding the keyboard -- " +
			"so the ctrl+z it promises cannot reach the buffer")
	}

	// 7. One ctrl+z, and nothing ever reached disk.
	rig.send(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("one ctrl+z left %q, want %q", got, original)
	}
	if q, wrote := rig.wroteAnything(); wrote {
		t.Errorf("something reached disk with no ctrl+s: %v", q.payload)
	}
}

func lastQueryOf(sent *[]sentQuery, cmdType string) (sentQuery, bool) {
	for i := len(*sent) - 1; i >= 0; i-- {
		if (*sent)[i].cmd == cmdType {
			return (*sent)[i], true
		}
	}
	return sentQuery{}, false
}
