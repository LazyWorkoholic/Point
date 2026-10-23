package editor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// gate_f15.ps1, automated.
//
// # Why this exists
//
// Manual testing has been unreliable for exactly the class of thing it was
// being used to find. Three rounds of this episode's gate produced: a write
// assertion that could never fail, a chord key arriving as a literal
// character (a timing artefact of human keystrokes), and a caret offset
// that could not be reproduced. Human timing is both the trigger for some
// of these and the thing that makes "the UI is broken" hard to tell apart
// from "I mistyped".
//
// So the gate's own sequences are driven here, through the real Root, with
// real key events and the real envelope path -- the harness
// rootdispatch_test.go built, pointed at the gate's steps rather than at
// one bug.
//
// # What stays manual, and why
//
// Three things: whether something LOOKS right (alignment, colour, the
// accent frame), whether scrolling FEELS smooth, and whether the demo
// provider's own prose reads sensibly. Everything else below was a human
// Ask prompt and is now an assertion.

// gateRig is newRootRig with the gate's own fixture shape: greet() at lines
// 4-8 and add() at 12-14, which is what B28's fixtures act on.
func gateRig(t *testing.T) *rootRig {
	t.Helper()
	lines := []string{
		`"""A tiny demo app."""`,  // 1
		``,                        // 2
		``,                        // 3
		`def greet(name):`,        // 4
		`    """Greet name."""`,   // 5
		`    if not name:`,        // 6
		`        name = "friend"`, // 7
		`    return name`,         // 8
		``,                        // 9
		``,                        // 10
		`def add(a, b):`,          // 11
		`    """Sum."""`,          // 12
		`    return a + b`,        // 13
	}
	return newRootRig(t, strings.Join(lines, "\n"), 10*cmdPatience)
}

// fillProposal is B28's fixture as B24 would deliver it: greet() rewritten,
// against lines 4-8, with B36's generation-time pair.
func (rig *rootRig) fillProposal(mtime float64, size int) {
	rig.envelope(protocol.EvFillProposal, map[string]any{
		"proposal": map[string]any{
			"file":       "sample.py",
			"start_line": 4, "end_line": 8,
			"replacement": "def greet(name):\n    if not isinstance(name, str) or not name:\n" +
				"        name = \"friend\"\n    return name",
			"note":        "Added a type check.",
			"shown_start": 4, "shown_end": 8,
			"mtime": mtime, "size": size,
		},
	})
}

// ---------------------------------------------------------------------------
// Step 2 -- explain (Done-when 1 and 2)
// ---------------------------------------------------------------------------

func TestGateStep2Explain(t *testing.T) {
	rig := gateRig(t)
	rig.ed.docs[0].cursor = Pos{Line: 11} // 1-based line 12, inside add()

	rig.leader('a')
	if rig.ed.inv == nil {
		t.Fatal("leader a did not invoke")
	}
	// Done-when 1: the WHOLE enclosing function, not the one line.
	if got := [2]int{rig.ed.inv.shownStart, rig.ed.inv.shownEnd}; got != [2]int{11, 13} {
		t.Errorf("shown region = %v, want lines 11-13 (the whole of add())", got)
	}
	if rig.ed.panel == nil {
		t.Fatal("no explain panel opened")
	}

	// Done-when 2: it streams, and the panel's three fields are distinct.
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": "It sums its two arguments."})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	frame := stripANSI(rig.root.View())
	for _, want := range []string{"function add(a, b)", "lines 11-13", "It sums its two arguments."} {
		if !strings.Contains(frame, want) {
			t.Errorf("the panel does not show %q", want)
		}
	}

	// esc dismisses it -- and reaches the workspace at all, which it only
	// does because CapturesKey claims it while a surface is open.
	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	if rig.ed.panel != nil {
		t.Error("esc did not dismiss the panel")
	}
}

// ---------------------------------------------------------------------------
// Step 3 -- the ghost, a switch away and back, esc (Done-when 3, 3b, 3c)
// ---------------------------------------------------------------------------

func TestGateStep3Ghost(t *testing.T) {
	rig := gateRig(t)
	original := rig.ed.docs[0].buf.Text()
	rig.ed.docs[0].cursor = Pos{Line: 5}

	rig.leader('f')
	if rig.ed.prompt == nil {
		t.Fatal("leader f did not open the prompt")
	}
	for _, r := range "add a type check" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if rig.ed.inv != nil {
		t.Fatal("typing in the prompt sent the invocation")
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})

	// Done-when 3.
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `{"replacement": "def greet(name):\n`})
	if rig.ed.ghost == nil || rig.ed.ghost.rows() == 0 {
		t.Fatal("nothing streamed into the ghost")
	}

	// Done-when 3b: away and back shows everything that arrived while away.
	rig.leader('z')
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": `    return name"`})
	rig.leader('e')
	if n := rig.ed.ghost.rows(); n != 2 {
		t.Errorf("the ghost has %d row(s) after a switch away and back, want 2 -- "+
			"it is a snapshot rather than the accumulated reply", n)
	}

	// Done-when 3c.
	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	if rig.ed.ghost != nil {
		t.Error("esc left the ghost on screen")
	}
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer changed to %q", got)
	}
	if rig.ed.docs[0].dirty {
		t.Error("a cancelled fill marked the buffer dirty")
	}
	if q, wrote := rig.wroteAnything(); wrote {
		t.Errorf("the ghost path wrote to disk: %v", q.payload)
	}
}

// ---------------------------------------------------------------------------
// Step 4 -- edit, apply, undo (Done-when 4 and 5)
// ---------------------------------------------------------------------------

func TestGateStep4ApplyAndUndo(t *testing.T) {
	rig := gateRig(t)
	original := rig.ed.docs[0].buf.Text()
	rig.ed.docs[0].cursor = Pos{Line: 5}

	rig.leader('f')
	for _, r := range "add a type check" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.fillProposal(1791623519.07509, 374)
	if rig.ed.card == nil {
		t.Fatal("no diff card was built")
	}
	if !rig.cardOnScreen() {
		t.Fatal("the card did not open")
	}

	// `e` edits the proposed RESULT, and the EDIT is what lands.
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if rig.ed.card.mode != diffEdit {
		t.Fatal("e did not enter edit mode")
	}
	for _, r := range "#mine" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	if _, asked := lastQueryOf(rig.sent, protocol.CmdCheckFileStale); !asked {
		t.Fatal("Enter did not ask whether the file changed on disk")
	}
	rig.staleReply(false)

	if !rig.ed.card.Applied() {
		t.Fatalf("the apply was refused: %q", rig.ed.card.refusal)
	}
	if got := rig.ed.docs[0].buf.Text(); !strings.Contains(got, "#mine") {
		t.Error("the apply landed the original proposal rather than the edited text")
	}
	if rig.cardOnScreen() {
		t.Error("the applied card is still holding the keyboard, so ctrl+z cannot reach the buffer")
	}

	// Done-when 4: ONE ctrl+z.
	rig.send(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("one ctrl+z left %q", got)
	}
	// Done-when 5: nothing reached disk, including after the undo's own
	// autosave tick.
	if q, wrote := rig.wroteAnything(); wrote {
		t.Errorf("something was written with no ctrl+s: %v", q.payload)
	}
}

// ---------------------------------------------------------------------------
// Step 6 -- the file changed ON DISK (Done-when 7b)
// ---------------------------------------------------------------------------

func TestGateStep6DiskStaleness(t *testing.T) {
	rig := gateRig(t)
	original := rig.ed.docs[0].buf.Text()
	rig.ed.docs[0].cursor = Pos{Line: 5}

	rig.leader('f')
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.fillProposal(1791623519.07509, 374)

	c := rig.ed.card
	if c == nil {
		t.Fatal("no card")
	}
	if !c.HasRecordedPair || c.RecordedMtime == 0 {
		t.Fatal("the card has no generation-time pair, so the disk guard cannot run")
	}

	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	q, asked := lastQueryOf(rig.sent, protocol.CmdCheckFileStale)
	if !asked {
		t.Fatal("no CheckFileStale was sent")
	}
	if got := q.payload["mtime"]; got != 1791623519.07509 {
		t.Errorf("mtime sent = %v, want the generation-time value", got)
	}
	if got := q.payload["session_id"]; got != "sess-editor" {
		t.Errorf("session_id = %v, want the session that produced the proposal", got)
	}

	rig.staleReply(true)
	if c.Applied() {
		t.Fatal("the apply went ahead after the file changed on disk")
	}
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer was spliced anyway: %q", got)
	}
	if !strings.Contains(c.refusal, "on disk") {
		t.Errorf("refusal = %q, want it to name the file changing ON DISK", c.refusal)
	}
	if strings.Contains(c.refusal, "buffer") {
		t.Errorf("refusal = %q, want wording distinct from the buffer guard", c.refusal)
	}
	if title := c.Title(); !strings.HasPrefix(title, "Proposed change") {
		t.Errorf("title = %q, want it still to read Proposed change", title)
	}
	if !strings.Contains(c.footer(), "re-generate") {
		t.Errorf("footer = %q, want the re-generate offer", c.footer())
	}
}

// ---------------------------------------------------------------------------
// Step 7 -- a non-proposal reply is a plain response (Done-when 6)
// ---------------------------------------------------------------------------

func TestGateStep7NonProposalIsAResponse(t *testing.T) {
	rig := gateRig(t)
	rig.ed.docs[0].cursor = Pos{Line: 5}

	rig.leader('f')
	for _, r := range "refactor across two files" {
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{
		"text": "I can only change one file at a time, so I have not changed anything at all here.",
	})

	// It streams as the agent's own words, WRAPPED, not as a region.
	g := rig.ed.ghost
	if g == nil || !g.literal {
		t.Fatal("a plain-text reply was shown as a proposed region")
	}
	for _, row := range rig.ed.ghostRows(rig.ed.textArea.W) {
		if visibleWidth(row) > rig.ed.textArea.W {
			t.Errorf("a streamed row is %d cells wide, pane content is %d",
				visibleWidth(row), rig.ed.textArea.W)
		}
	}

	// The settle timer has to fire inside the harness's patience for this
	// step, because concluding "no proposal is coming" IS the step. The
	// other gate steps want it long, so that they can deliver a proposal
	// before it fires -- see newRootRig's own note.
	rig.ed.settleDelay = cmdPatience / 4
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	if rig.ed.card != nil {
		t.Error("a diff card was built from a refusal")
	}
	if rig.ed.panel == nil {
		t.Fatal("the refusal was not shown anywhere")
	}
	if rig.ed.panel.failure != "" {
		t.Errorf("the refusal was rendered as a failure: %q", rig.ed.panel.failure)
	}
	if rig.ed.ghost != nil {
		t.Error("the ghost was left on screen")
	}
}

// ---------------------------------------------------------------------------
// The gate's own chord sequences, driven programmatically
// ---------------------------------------------------------------------------

// Every chord the gate's steps press, repeated, with the buffer asserted
// clean throughout.
//
// The gate's human runs saw literal 'w', 'q' and 'W' characters appear in
// app.py. That happens inside bubbletea's Windows key reader, which returns
// KeyRunes when a CTRL bit is set but neither the control character nor the
// virtual-key code matches its tables (key_windows.go's final `return
// KeyRunes`) -- Ctrl+Shift+W reports Char 'W', and KeyMsg has no Ctrl field
// to carry the modifier. It therefore CANNOT be reproduced from constructed
// events, because a constructed KeyCtrlW is correct by construction.
//
// What this test pins is the half that is ours: a correctly delivered chord
// never reaches the buffer, however fast it is repeated and whatever is on
// screen at the time.
func TestGateChordSequencesNeverReachTheBuffer(t *testing.T) {
	rig := gateRig(t)
	original := rig.ed.docs[0].buf.Text()

	for round := 0; round < 6; round++ {
		rig.send(tea.KeyMsg{Type: tea.KeyCtrlW})
		rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
		rig.send(tea.KeyMsg{Type: tea.KeyEsc}) // close the switcher
		rig.leader('e')
		rig.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
		rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	}
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("a chord leaked into the buffer: %q", got)
	}
	if rig.ed.docs[0].dirty {
		t.Error("the buffer is dirty after nothing but chords")
	}
	if q, wrote := rig.wroteAnything(); wrote {
		t.Errorf("chords alone caused a write: %v", q.payload)
	}
}

// ---------------------------------------------------------------------------
// Step 5 -- a refused apply shows ON THE CARD (WORKFLOW 18d's step 3)
// ---------------------------------------------------------------------------

// The reachable version of "force an apply to fail and watch the CARD, not
// the log": a read-only target. The gate drives it by making app.py
// read-only on disk; here the document carries the same state the backend's
// reply would have set.
func TestGateStep5RefusalShowsOnTheCard(t *testing.T) {
	rig := gateRig(t)
	rig.ed.docs[0].cursor = Pos{Line: 5}

	// A card first, while the document is writable...
	rig.leader('f')
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.fillProposal(1791623519.07509, 374)
	c := rig.ed.card
	if c == nil {
		t.Fatal("no card")
	}

	// ...then the target becomes read-only, as B32's reply would make it.
	rig.ed.docs[0].readOnly = true
	rig.ed.docs[0].roReason = roNotWritable
	original := rig.ed.docs[0].buf.Text()

	rig.send(tea.KeyMsg{Type: tea.KeyEnter})

	// Refused BEFORE any round trip: a read-only buffer needs no disk
	// check to know the answer.
	if _, asked := lastQueryOf(rig.sent, protocol.CmdCheckFileStale); asked {
		t.Error("a read-only target still asked the backend about the file")
	}
	if c.Applied() {
		t.Fatal("the apply went ahead on a read-only document")
	}
	if got := rig.ed.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer changed to %q", got)
	}
	// ON THE CARD, which is the whole point of 18d's step 3 -- a refusal
	// only in the log is a card that looks inert.
	if c.refusal == "" {
		t.Fatal("the refusal is not on the card")
	}
	if !strings.Contains(c.refusal, "read-only") {
		t.Errorf("refusal = %q, want it to name the reason", c.refusal)
	}
	if !strings.Contains(c.statusLine(), "not applied") {
		t.Errorf("status line = %q", c.statusLine())
	}
	if title := c.Title(); !strings.HasPrefix(title, "Proposed change") {
		t.Errorf("title = %q, want it NOT to read Applied", title)
	}
	if rig.cardOnScreen() == false {
		t.Error("the card closed on a refusal; it must stay open and unapplied")
	}

	// And fill is refused UP FRONT on a read-only file, rather than
	// spending a model call to arrive at a refusal that was knowable.
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}) // close the card
	rig.ed.inv = nil
	rig.leader('f')
	if rig.ed.prompt != nil {
		t.Error("the fill prompt opened on a read-only file")
	}
	if rig.ed.inv != nil {
		t.Error("a fill was invoked against a read-only file")
	}
}

// ---------------------------------------------------------------------------
// Step 8 -- the automatable half: no inline diff card from Chat
// ---------------------------------------------------------------------------

// Done-when 8's real content, and the half that does not need eyes: a
// proposal belonging to someone else's task never becomes an inline diff
// card in the Editor.
//
// The other half -- that Chat renders the reply in its transcript and the
// right pane's Task card shows the finished task -- is a look-at-it check
// and stays manual, as does the tray's own "no background tasks" (the tray
// is background-only by design; see F15's Done-when 8 note).
func TestGateStep8NoInlineCardFromAnotherTask(t *testing.T) {
	rig := gateRig(t)

	foreign := func() {
		raw := map[string]any{
			"proposal": map[string]any{
				"file": "sample.py", "start_line": 4, "end_line": 8,
				"replacement": "def greet(name):\n    return name",
				"shown_start": 4, "shown_end": 8, "mtime": 1.0, "size": 2,
			},
		}
		rawBytes, _ := json.Marshal(raw)
		rig.send(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
			CorrID: "chat-request-id", TaskID: "task-from-chat",
			SessionID: "chat-session", Type: protocol.EvFillProposal,
			Payload: rawBytes,
		}})
	}

	// Idle Editor: a Chat-invoked proposal builds nothing.
	foreign()
	if rig.ed.card != nil {
		t.Error("a proposal from another workspace's task built an inline diff card")
	}
	if rig.cardOnScreen() {
		t.Error("a card opened for a task the Editor did not start")
	}

	// And with an Editor invocation of its own in flight, a DIFFERENT
	// task's proposal still does not land on it.
	rig.ed.docs[0].cursor = Pos{Line: 5}
	rig.leader('f')
	rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rig.send(tea.KeyMsg{Type: tea.KeyEnter})
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	foreign()
	if rig.ed.card != nil {
		t.Error("a foreign proposal landed on a live Editor invocation")
	}
	// The Editor's own, by contrast, does.
	rig.envelope(protocol.EvTaskDone, map[string]any{})
	rig.fillProposal(1791623519.07509, 374)
	if rig.ed.card == nil {
		t.Error("the Editor's OWN proposal did not build a card")
	}
}

// ---------------------------------------------------------------------------
// esc means the same thing in both modes
// ---------------------------------------------------------------------------

// F15's gate: esc on a streaming EXPLAIN left its Task card row reading
// "completed", while esc on a streaming FILL read "cancelled". esc was
// only hiding the panel; the task ran on to completion, so "completed" was
// technically true and the gesture was a lie.
func TestEscCancelsAStreamingTaskInBothModes(t *testing.T) {
	for _, mode := range []struct {
		name  string
		begin func(*rootRig)
	}{
		{"explain", func(rig *rootRig) { rig.leader('a') }},
		{"fill", func(rig *rootRig) {
			rig.leader('f')
			rig.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			rig.send(tea.KeyMsg{Type: tea.KeyEnter})
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			rig := gateRig(t)
			rig.ed.docs[0].cursor = Pos{Line: 5}
			mode.begin(rig)
			rig.envelope(protocol.EvTaskStarted, map[string]any{})
			rig.envelope(protocol.EvTextDelta, map[string]any{"text": "partial..."})
			if !rig.ed.taskRunning() {
				t.Fatal("the task is not running, so esc has nothing to cancel")
			}

			rig.send(tea.KeyMsg{Type: tea.KeyEsc})

			if !rig.ed.inv.cancelled {
				t.Error("esc did not cancel the invocation")
			}
			// The backend is TOLD, which is what makes the Task card say
			// "cancelled" rather than letting the turn run to completion.
			if _, sent := lastQueryOf(rig.sent, protocol.CmdCancelTask); !sent {
				t.Error("no CancelTask was sent, so the task runs to completion " +
					"and the tray will read `completed` for a dismissed reply")
			}
			if rig.ed.panel != nil {
				t.Error("the panel is still open")
			}
			if rig.ed.ghost != nil {
				t.Error("the ghost is still on screen")
			}
		})
	}
}

// And esc on a FINISHED reply closes without cancelling anything -- no
// CancelTask for a task the backend has already ended.
func TestEscOnAFinishedReplyJustCloses(t *testing.T) {
	rig := gateRig(t)
	rig.ed.docs[0].cursor = Pos{Line: 5}
	rig.leader('a')
	rig.envelope(protocol.EvTaskStarted, map[string]any{})
	rig.envelope(protocol.EvTextDelta, map[string]any{"text": "all of it"})
	rig.envelope(protocol.EvTaskDone, map[string]any{})

	rig.send(tea.KeyMsg{Type: tea.KeyEsc})
	if rig.ed.panel != nil {
		t.Error("esc did not close the panel")
	}
	if _, sent := lastQueryOf(rig.sent, protocol.CmdCancelTask); sent {
		t.Error("a CancelTask was sent for a task that had already finished")
	}
}
