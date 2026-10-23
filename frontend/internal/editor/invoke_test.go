package editor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// F15's gate at the unit level: the two invocation paths, the ghost, the
// decoder, and the on-disk staleness guard that closes F9's Done-when #7.
//
// Three of F15's Done-whens are interactive by nature (3 "streams ghost
// text as tokens arrive", 5 "mtime unchanged until the user saves" measured
// against the real filesystem, 6 "a readable refusal, not an error state")
// and are checked by hand at the gate as well. What is asserted here is
// every part of them that is a decision rather than a pixel.

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// invokeWorkspace is a workspace with one file open, a recorder in place of
// the wire, and a session already assigned -- so a test that is about the
// invocation does not have to drive CreateSession first. The session
// bootstrap has its own test below.
func invokeWorkspace(t *testing.T, content string) (*Workspace, *[]sentQuery) {
	t.Helper()
	ws := New(Options{Theme: testTheme()})
	var sent []sentQuery
	ws.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmd, kind, payload})
	}
	// The uncorrelated write seam too: SendMessage and CancelTask go
	// through writeFn, not sendFn (editor.go's field has the reason), so a
	// harness that substituted only one of the two would be driving a
	// workspace whose every invocation failed with "no backend
	// connection" -- which is how this one was first written, and what the
	// ghost assertions caught.
	ws.writeFn = func(id, cmd string, payload map[string]any) error {
		sent = append(sent, sentQuery{cmd, queryUncorrelated, payload})
		return nil
	}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.sessionID = "sess-editor"
	ws.docs = append(ws.docs, testDoc(t, content))
	ws.focus = focusBuffer
	// On screen, which is what every test in this file is about. The
	// backgrounded case is rootdispatch_test.go's, driven through a real
	// Root -- and the two differ: a card built while backgrounded is held
	// for the switch back rather than opened over another workspace.
	ws.onScreen = true
	return ws, &sent
}

// invokeAndCapture builds the EditorContext an invocation WOULD send, from
// the same function the send path calls.
//
// Reading it from the recorded payload instead would mean decoding a
// map[string]any back into the struct, which tests the JSON round trip
// rather than the extraction -- and the extraction is what Done-when 1 is
// about. The payload's own shape is asserted separately (the session tests
// read it directly).
func invokeAndCapture(t *testing.T, ws *Workspace, kind invokeKind, question string) EditorContext {
	t.Helper()
	d := ws.activeDoc()
	if d == nil {
		t.Fatal("no document open")
	}
	return editorContextFor(d, kind.mode())
}

// stream feeds one token event for the live invocation.
func stream(ws *Workspace, text string) {
	payload, _ := json.Marshal(map[string]any{"text": text})
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: ws.inv.requestID, TaskID: "task-1", SessionID: "sess-editor",
		Type: protocol.EvTextDelta, Payload: payload,
	}})
}

func taskDone(ws *Workspace) {
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: ws.inv.requestID, TaskID: "task-1", SessionID: "sess-editor",
		Type: protocol.EvTaskDone, Payload: json.RawMessage(`{}`),
	}})
}

// proposalEnvelope is B24's event with B36's pair, as the backend emits it.
func proposalEnvelope(ws *Workspace, start, end int, replacement string,
	mtime float64, size int) protocol.EnvelopeMsg {

	payload, _ := json.Marshal(map[string]any{
		"proposal": map[string]any{
			"file": "sample.py", "start_line": start, "end_line": end,
			"replacement": replacement, "note": "a note",
			"shown_start": start, "shown_end": end,
			"mtime": mtime, "size": size,
		},
	})
	return protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: ws.inv.requestID, TaskID: "task-1", SessionID: "sess-editor",
		Type: protocol.EvFillProposal, Payload: payload,
	}}
}

// ---------------------------------------------------------------------------
// Done-when 1 -- the whole enclosing function is sent
// ---------------------------------------------------------------------------

// "Select one line inside a function, invoke explain -> the sent context
// contains the WHOLE enclosing function."
//
// "Sending one highlighted line without its function makes the question
// unanswerable" (F15's Build).
func TestExplainSendsTheWholeEnclosingFunction(t *testing.T) {
	const file = "import os\n" + // 1
		"\n" + // 2
		"def outer():\n" + // 3
		"    x = 1\n" + // 4
		"    y = 2\n" + // 5
		"    return x + y\n" + // 6
		"\n" + // 7
		"\n" + // 8
		"def other():\n" + // 9
		"    pass" // 10

	ws, _ := invokeWorkspace(t, file)
	ws.docs[0].cursor = Pos{Line: 4} // the `y = 2` line, 1-based line 5

	ec := invokeAndCapture(t, ws, invokeExplain, "explain this")

	if ec.Mode != modeExplain {
		t.Errorf("mode = %q, want explain", ec.Mode)
	}
	if got := ec.Enclosing; len(got) != 2 || got[0] != 3 || got[1] != 6 {
		t.Errorf("enclosing = %v, want [3 6] — the whole of outer(), "+
			"trailing blank lines trimmed", got)
	}
	if ec.Cursor != 5 {
		t.Errorf("cursor = %d, want 5", ec.Cursor)
	}
	if ec.File == "" {
		t.Error("no file in the context")
	}
}

// The gate's own case, pinned as a test so a change to the heuristic cannot
// quietly break the one interactive path that produces a real proposal.
//
// B28's `filler_fill.json` hardcodes `start_line: 4, end_line: 8` against
// the shipped sample project's `app.py`, and B24 REJECTS a proposal whose
// range falls outside the region the model was shown. So this exact answer
// -- cursor on line 6, enclosing 4-8 -- is what makes the Demo-mode fill
// produce a card at all.
func TestTheDemoSampleFunctionResolvesToTheFixturesRange(t *testing.T) {
	const appPy = `"""A tiny demo Flask-less app, shipped with Point's Demo mode."""


def greet(name):
    """Return a friendly greeting for name."""
    if not name:
        name = "friend"
    return f"Hello, {name}!"


def add(a, b):
    """Return the sum of a and b."""
    return a + b`

	ws, _ := invokeWorkspace(t, appPy)
	ws.docs[0].cursor = Pos{Line: 5} // 1-based line 6, `if not name:`

	ec := invokeAndCapture(t, ws, invokeFill, "add a type check")
	if got := ec.Enclosing; len(got) != 2 || got[0] != 4 || got[1] != 8 {
		t.Errorf("enclosing = %v, want [4 8] — the range B28's fixture "+
			"proposes against and B24 validates", got)
	}
	if ec.Mode != modeFill {
		t.Errorf("mode = %q, want fill", ec.Mode)
	}
}

// A selection wins over the caret for choosing the target line, and the
// selection itself travels -- `resolve_scope`'s fallback centres on it when
// nothing encloses.
func TestASelectionTravelsWithTheContext(t *testing.T) {
	ws, _ := invokeWorkspace(t, "a = 1\nb = 2\nc = 3")
	d := ws.docs[0]
	d.anchor, d.cursor, d.selecting = Pos{Line: 0}, Pos{Line: 2}, true

	ec := invokeAndCapture(t, ws, invokeExplain, "explain this")
	if got := ec.Selection; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("selection = %v, want [1 3]", got)
	}
	// Top-level code encloses nothing, so the fallback is selection ± 20
	// clamped to the file.
	if got := ec.Enclosing; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("enclosing = %v, want the clamped fallback [1 3]", got)
	}
}

// A collapsed selection is NOT a selection. doc.hasSelection already draws
// that line for the renderer; sending a zero-width range as a selection
// would make the backend's fallback centre on a span instead of the caret.
func TestACollapsedSelectionIsNotSent(t *testing.T) {
	ws, _ := invokeWorkspace(t, "a = 1\nb = 2")
	d := ws.docs[0]
	d.anchor, d.cursor, d.selecting = Pos{Line: 1}, Pos{Line: 1}, true

	if ec := invokeAndCapture(t, ws, invokeExplain, "x"); ec.Selection != nil {
		t.Errorf("selection = %v, want none", ec.Selection)
	}
}

// ---------------------------------------------------------------------------
// The fill prompt -- nothing is sent until Enter
// ---------------------------------------------------------------------------

func TestFillSendsNothingUntilEnter(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")

	if cmd := ws.BeginFill(); cmd != nil {
		// A status line is fine; what must not happen is an invocation.
		cmd()
	}
	if ws.prompt == nil {
		t.Fatal("leader → f did not open the inline prompt")
	}
	if ws.inv != nil {
		t.Fatal("fill invoked before Enter")
	}

	for _, r := range "add a docstring" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := ws.prompt.text(); got != "add a docstring" {
		t.Fatalf("prompt text = %q", got)
	}
	if ws.inv != nil {
		t.Fatal("typing in the prompt sent something")
	}

	ws.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ws.prompt != nil {
		t.Error("the prompt stayed open after Enter")
	}
	if ws.inv == nil {
		t.Fatal("Enter did not invoke")
	}
	if ws.inv.question != "add a docstring" {
		t.Errorf("question = %q", ws.inv.question)
	}
	if ws.ghost == nil {
		t.Error("no ghost was prepared for the reply")
	}
}

func TestEscInThePromptSendsNothing(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	ws.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if ws.prompt != nil {
		t.Error("esc did not close the prompt")
	}
	if ws.inv != nil {
		t.Error("esc sent the invocation anyway")
	}
}

// The prompt claims the keys it is typed into, and the buffer must not also
// receive them -- a fill instruction typed into the file would be an edit
// nobody asked for.
func TestTypingInThePromptDoesNotReachTheBuffer(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	before := ws.docs[0].buf.Text()
	ws.BeginFill()
	for _, r := range "hello" {
		ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := ws.docs[0].buf.Text(); got != before {
		t.Errorf("the buffer changed to %q", got)
	}
	if ws.docs[0].dirty {
		t.Error("the document was marked dirty by the prompt")
	}
}

// esc and ctrl+c must actually ARRIVE. Both are Chat-only chords that
// dispatchKey swallows before this workspace is asked (keys.go's header),
// so the claim is the only thing that makes either key reachable here --
// esc to cancel, ctrl+c to copy.
func TestTheModalSurfacesClaimEsc(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")

	// esc is claimed only while a surface is on screen.
	if ws.CapturesKey("esc") {
		t.Error("esc is claimed with nothing open")
	}
	ws.BeginFill()
	if !ws.CapturesKey("esc") {
		t.Error("esc is not claimed while the prompt is open")
	}

	// ctrl+c is claimed either way, because it is COPY here and has no
	// second meaning -- see copy.go. F15's Build names it as a cancel
	// key; that divergence is deliberate and recorded there.
	for _, state := range []string{"with the prompt open", "with nothing open"} {
		if !ws.CapturesKey("ctrl+c") {
			t.Errorf("ctrl+c is not claimed %s; the chord table would answer "+
				"that cancel is unavailable and the selection could not be "+
				"copied", state)
		}
		ws.prompt = nil
	}

	// And the two unconditional ones are untouched.
	for _, k := range []string{"tab", "shift+tab"} {
		if !ws.CapturesKey(k) {
			t.Errorf("%s stopped being claimed", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Done-when 3 / 3b -- the ghost
// ---------------------------------------------------------------------------

// The ghost grows as tokens arrive, and it is rebuilt from the WHOLE
// accumulated reply each time -- which is what makes Done-when 3b hold
// with no second mechanism.
func TestTheGhostGrowsFromTheAccumulatedReply(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("rewrite f")
	ws.Update(tea.KeyMsg{Type: tea.KeyEnter})

	stream(ws, `{"file": "sample.py", "start_line": 1, "end_line": 2, "replacement": "def f():\n`)
	if got := ws.ghost.lines; len(got) != 2 || got[0] != "def f():" {
		t.Fatalf("ghost after one token = %#v", got)
	}
	stream(ws, `    return 1"`)
	if got := ws.ghost.lines; len(got) != 2 || got[1] != "    return 1" {
		t.Fatalf("ghost after two tokens = %#v", got)
	}
	if ws.ghost.literal {
		t.Error("a proposal's region was shown as literal reply text")
	}

	// The accumulation is a pure function of inv.text: re-applying it
	// produces the same ghost, which is the property a workspace switch
	// relies on.
	ws.applyStreamText(ws.inv)
	if got := ws.ghost.lines; len(got) != 2 || got[1] != "    return 1" {
		t.Errorf("re-applying the accumulated reply changed the ghost: %#v", got)
	}
}

// The mechanism Done-when 3b actually turns on: Root only forwards
// envelopes to the ACTIVE workspace unless the workspace asks for
// background delivery. Both kinds must ask.
func TestBothKindsWantBackgroundReplies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		begin func(*Workspace) tea.Cmd
	}{
		{"fill", func(w *Workspace) tea.Cmd {
			w.BeginFill()
			return w.submitPrompt()
		}},
		{"explain", (*Workspace).BeginExplain},
	} {
		ws, _ := invokeWorkspace(t, "def f():\n    pass")
		if ws.WantsBackgroundReplies() {
			t.Fatalf("%s: wants background replies while idle", tc.name)
		}
		if ws.prompt != nil || tc.name != "fill" {
			tc.begin(ws)
		} else {
			tc.begin(ws)
		}
		if tc.name == "fill" {
			// submitPrompt needs text; drive it the normal way instead.
			ws.BeginFill()
			ws.prompt.runes = []rune("do it")
			ws.submitPrompt()
		}
		if !ws.WantsBackgroundReplies() {
			t.Errorf("%s: a live invocation does not ask for background replies — "+
				"the stream would stop the moment the user switched to Chat", tc.name)
		}
		taskDone(ws)
		if tc.name == "explain" && ws.WantsBackgroundReplies() {
			t.Error("explain: still asking after the task finished")
		}
	}
}

// Done-when 3c: "esc mid-stream discards the ghost and cancels the task;
// the buffer is untouched and not marked dirty."
func TestEscMidStreamDiscardsTheGhostAndLeavesTheBufferClean(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	before := ws.docs[0].buf.Text()
	beforeVer := ws.docs[0].buf.Version()

	ws.BeginFill()
	ws.prompt.runes = []rune("add a docstring")
	ws.submitPrompt()
	ws.inv.taskID = "task-1"
	stream(ws, `{"replacement": "def f():\n    \"\"\"Doc.\"\"\"`)
	if ws.ghost.rows() == 0 {
		t.Fatal("nothing streamed into the ghost")
	}

	ws.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if ws.ghost != nil {
		t.Error("the ghost survived esc")
	}
	if !ws.inv.cancelled {
		t.Error("the invocation was not marked cancelled")
	}
	d := ws.docs[0]
	if got := d.buf.Text(); got != before {
		t.Errorf("the buffer changed to %q", got)
	}
	if d.buf.Version() != beforeVer {
		t.Error("the buffer's version moved — something spliced")
	}
	if d.dirty {
		t.Error("the document was marked dirty by a cancelled fill")
	}
	if len(d.hist.undo) != 0 {
		t.Errorf("%d undo record(s) were written by a cancelled fill", len(d.hist.undo))
	}
}

// The preview extractor, directly. It is not authoritative (the applied
// region comes from B24's validated event), but a wrong answer here is a
// wrong thing on screen, so its failure modes are pinned.
func TestThePartialReplacementPreview(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		want  string
		wantK bool
	}{
		{"nothing yet", `{"file": "a.py", `, "", false},
		{"key but no value", `{"replacement"`, "", false},
		{"opening quote only", `{"replacement": "`, "", true},
		{"mid-string", `{"replacement": "def f():\n    pa`, "def f():\n    pa", true},
		{"complete", `{"replacement": "a\nb", "note": "x"}`, "a\nb", true},
		{"whitespace after the colon", "{\"replacement\" :  \"a\"", "a", true},
		{"escaped quote and backslash", `{"replacement": "s = \"x\\y\""`, `s = "x\y"`, true},
		{"truncated escape", `{"replacement": "a\`, "a", true},
		{"unicode escape", `{"replacement": "\u00e9t\u00e9"`, "été", true},
		{"a plain-text refusal", "I cannot do that across two files.", "", false},
	} {
		got, ok := partialReplacement(tc.in)
		if ok != tc.wantK || got != tc.want {
			t.Errorf("%s: partialReplacement(%q) = %q,%v want %q,%v",
				tc.name, tc.in, got, ok, tc.want, tc.wantK)
		}
	}
}

// A reply that is not a proposal streams as the agent's own words rather
// than as a proposed region -- the first half of Done-when 6.
func TestANonProposalStreamsAsLiteralText(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("refactor across two files")
	ws.submitPrompt()

	stream(ws, "I can only change one file at a time.")
	if !ws.ghost.literal {
		t.Error("a plain-text refusal was shown as a proposed region")
	}
	if got := strings.Join(ws.ghost.lines, "\n"); !strings.Contains(got, "one file") {
		t.Errorf("ghost = %q, want the refusal text", got)
	}
}

// Done-when 6, the rest of it: the refusal ends up readable, as a response,
// with no diff card and no error state.
func TestARefusalSettlesIntoThePanelNotAnErrorState(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("refactor across task.py and state.py")
	ws.submitPrompt()

	stream(ws, "That needs two files, so I have not changed anything.")
	cmd := ws.handleEnvelopeForTest(protocol.Envelope{
		CorrID: ws.inv.requestID, TaskID: "task-1", Type: protocol.EvTaskDone,
		Payload: json.RawMessage(`{}`),
	})
	// TaskDone arms the settle timer; the proposal never comes.
	if cmd == nil {
		t.Fatal("TaskDone produced no settle timer, so a refusal would never be shown")
	}
	msg, ok := cmd().(proposalSettleMsg)
	if !ok {
		t.Fatalf("TaskDone produced %T, want proposalSettleMsg", cmd())
	}
	ws.Update(msg)

	if ws.card != nil {
		t.Error("a diff card was built from a refusal")
	}
	if ws.ghost != nil {
		t.Error("the ghost was left on screen")
	}
	if ws.panel == nil {
		t.Fatal("the refusal was not shown anywhere")
	}
	if ws.panel.failure != "" {
		t.Errorf("the refusal was rendered as a failure: %q", ws.panel.failure)
	}
	if !strings.Contains(ws.panel.body, "two files") {
		t.Errorf("panel body = %q, want the agent's own words", ws.panel.body)
	}
}

// A superseded settle tick does nothing -- autosave's own generation rule,
// and the thing that stops a proposal that DID arrive being overwritten by
// a timer armed before it.
func TestASupersededSettleTickDoesNothing(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("x")
	ws.submitPrompt()
	stream(ws, `{"replacement": "def f():\n    return 1"`)
	taskDone(ws)
	stale := proposalSettleMsg{gen: ws.settleGen}

	ws.Update(proposalEnvelope(ws, 1, 2, "def f():\n    return 1", 1791623519.07509, 24))
	if ws.card == nil {
		t.Fatal("no card was built")
	}
	ws.Update(stale) // the tick armed before the proposal landed
	if ws.panel != nil {
		t.Error("a superseded settle tick opened the refusal panel over a real proposal")
	}
}

// ---------------------------------------------------------------------------
// Done-when 7b -- the (mtime, size) pair, populated and GATING
// ---------------------------------------------------------------------------

// This replaces F9b's TestTheStalenessPairIsCarriedAndUnpopulated, which
// asserted the opposite and was the tripwire against filling these fields
// from the wrong moment. F15 is where they are filled -- from B36's real
// FillProposal fields -- so the tripwire now asserts the new invariant:
// populated, non-zero, and GATING the apply.
func TestTheStalenessPairIsPopulatedFromTheProposal(t *testing.T) {
	const mtime, size = 1791623519.07509, 4096
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("add a docstring")
	ws.submitPrompt()
	ws.Update(proposalEnvelope(ws, 1, 2, "def f():\n    return 1", mtime, size))

	c := ws.card
	if c == nil {
		t.Fatal("no card was built from the proposal")
	}
	if !c.HasRecordedPair {
		t.Error("the card does not claim a generation-time pair, so the disk " +
			"guard will be skipped on apply")
	}
	if c.RecordedMtime != mtime || c.RecordedSize != size {
		t.Errorf("RecordedMtime/Size = %v/%v, want %v/%v from the event",
			c.RecordedMtime, c.RecordedSize, mtime, size)
	}
	if c.SessionID != "sess-editor" {
		t.Errorf("SessionID = %q, want the session the proposal arrived on", c.SessionID)
	}

	// And NOT the document's own pair, which is the substitution F9b's
	// tripwire existed to prevent. The document's mtime is deliberately a
	// different number here.
	d := ws.docs[0]
	d.mtime, d.size = 1700000000.0, 11
	if c.RecordedMtime == d.mtime || c.RecordedSize == d.size {
		t.Error("the card took the document's open-time pair")
	}
}

// The apply ASKS, with everything B36 needs to answer the right question.
func TestApplyAsksCheckFileStaleWithThePairAndTheSession(t *testing.T) {
	ws, sent := invokeWorkspace(t, "def f():\n    pass")
	c := fillCard(t, ws, 1791623519.07509, 4096)

	before := ws.docs[0].buf.Text()
	applyCard(t, ws, c)

	q := lastQuery(t, sent, protocol.CmdCheckFileStale)
	if q.kind != queryStaleCheck {
		t.Errorf("query kind = %v", q.kind)
	}
	if got := q.payload["mtime"]; got != 1791623519.07509 {
		t.Errorf("mtime = %v, want the generation-time value", got)
	}
	if got := q.payload["size"]; got != 4096 {
		t.Errorf("size = %v, want the generation-time value", got)
	}
	if got := q.payload["session_id"]; got != "sess-editor" {
		t.Errorf("session_id = %v — without it CheckFileStale walks up from "+
			"the path instead of resolving the session's scope (B12 bug #2)", got)
	}

	// NOTHING has been spliced yet. The whole point of the two-phase
	// apply is that the buffer is untouched until the answer arrives.
	if got := ws.docs[0].buf.Text(); got != before {
		t.Errorf("the buffer was spliced before the staleness answer: %q", got)
	}
	if c.Applied() {
		t.Error("the card was marked applied before the answer arrived")
	}
}

// Done-when 7b: changed on disk -> REFUSED, on the card, with the file
// named -- and emphatically not applied.
func TestApplyIsRefusedWhenTheFileChangedOnDisk(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	c := fillCard(t, ws, 1791623519.07509, 4096)
	before := ws.docs[0].buf.Text()

	applyCard(t, ws, c)
	answerStale(ws, c, true)

	if c.Applied() {
		t.Fatal("the apply went ahead after the file changed on disk")
	}
	if got := ws.docs[0].buf.Text(); got != before {
		t.Errorf("the buffer was spliced anyway: %q", got)
	}
	if ws.docs[0].dirty {
		t.Error("the document was marked dirty by a refused apply")
	}
	if c.refusal == "" {
		t.Fatal("the refusal is not on the card — only a log line would be")
	}
	if !strings.Contains(c.refusal, "on disk") {
		t.Errorf("refusal = %q, want it to name the file changing ON DISK. "+
			"The gate fails a 7b that refuses for the buffer's reason, because "+
			"that is Done-when 7 and would pass either way", c.refusal)
	}
	if strings.Contains(c.refusal, "buffer") {
		t.Errorf("refusal = %q, want wording distinct from the buffer guard's", c.refusal)
	}
	if title := c.Title(); !strings.HasPrefix(title, "Proposed change") {
		t.Errorf("title = %q, want it still to read Proposed change", title)
	}
	if !strings.Contains(c.statusLine(), "not applied") {
		t.Errorf("status line = %q", c.statusLine())
	}
	if !strings.Contains(c.footer(), "re-generate") {
		t.Errorf("footer = %q, want the re-generate offer", c.footer())
	}
}

// The other half: unchanged on disk -> it applies, as one undoable splice.
func TestApplyProceedsWhenTheFileIsUnchangedOnDisk(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	c := fillCard(t, ws, 1791623519.07509, 4096)

	applyCard(t, ws, c)
	answerStale(ws, c, false)

	if !c.Applied() {
		t.Fatalf("the apply was refused: %q", c.refusal)
	}
	if got := ws.docs[0].buf.Text(); !strings.Contains(got, "return 1") {
		t.Errorf("buffer = %q, want the proposed region", got)
	}
	if !ws.docs[0].dirty {
		t.Error("the buffer is not dirty after an apply")
	}
	// One undo step, F13a's requirement, re-asserted through the new path.
	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.docs[0].buf.Text(); got != "def f():\n    pass" {
		t.Errorf("one ctrl+z left %q", got)
	}
}

// An unanswerable staleness question refuses. "A staleness check that
// permits the dangerous apply is worse than no check at all."
func TestARefusedStaleCheckRefusesTheApply(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	c := fillCard(t, ws, 1791623519.07509, 4096)
	applyCard(t, ws, c)

	// An Error envelope, which is what a path denial arrives as -- a file
	// outside the Editor session's own project.
	payload, _ := json.Marshal(map[string]any{
		"message": "path outside the project", "code": "path_denied",
	})
	ws.pending["stale-err"] = pendingReq{kind: queryStaleCheck, path: c.path, card: c}
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "stale-err", Type: protocol.EvError, Payload: payload,
	}})

	if c.Applied() {
		t.Fatal("the apply went ahead on an unanswered staleness question")
	}
	if !strings.Contains(c.refusal, "could not check") {
		t.Errorf("refusal = %q", c.refusal)
	}
}

// Done-when 5 at the unit level: the apply must not arm autosave, or an
// agent-proposed region reaches disk a second later with no keypress.
func TestApplyingDoesNotArmAutosave(t *testing.T) {
	ws, sent := invokeWorkspace(t, "def f():\n    pass")
	ws.autosaveMode = "after_delay"
	c := fillCard(t, ws, 1791623519.07509, 4096)

	applyCard(t, ws, c)
	answerStale(ws, c, false)
	if !c.Applied() {
		t.Fatalf("the apply was refused: %q", c.refusal)
	}

	for _, q := range *sent {
		if q.cmd == protocol.CmdWriteProjectFile {
			t.Fatal("the apply wrote the file — nothing may reach disk until the user saves")
		}
	}
	if gen := ws.docs[0].autosaveGen; gen != 0 {
		t.Errorf("autosaveGen = %d, want 0: the apply armed the autosave timer, "+
			"so the region would be written one second later with no keypress", gen)
	}
	// The user is still one keystroke away from a save, which is the half
	// that must NOT change.
	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if ws.docs[0].autosaveGen == 0 {
		t.Error("typing after an apply no longer arms autosave")
	}
}

// ---------------------------------------------------------------------------
// Done-when 8 -- a Chat-invoked fill produces no inline diff card
// ---------------------------------------------------------------------------

// The gate for it: a FillProposal this workspace did not ask for is not
// its proposal. That is the whole mechanism by which "invoked from Chat
// produces a result card instead of an inline diff" holds -- the Editor
// builds a card only for a proposal matching its OWN corr_id.
func TestAForeignProposalBuildsNoCard(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")

	payload, _ := json.Marshal(map[string]any{
		"proposal": map[string]any{
			"file": "sample.py", "start_line": 1, "end_line": 2,
			"replacement": "def f():\n    return 1", "shown_start": 1, "shown_end": 2,
			"mtime": 1.0, "size": 2,
		},
	})
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "someone-elses-request", TaskID: "task-chat",
		Type: protocol.EvFillProposal, Payload: payload,
	}})

	if ws.card != nil {
		t.Error("a proposal from another workspace's task built an inline diff card")
	}

	// And with an invocation of its own in flight, a DIFFERENT task's
	// proposal still does not land on it.
	ws.BeginFill()
	ws.prompt.runes = []rune("x")
	ws.submitPrompt()
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "someone-elses-request", TaskID: "task-chat",
		Type: protocol.EvFillProposal, Payload: payload,
	}})
	if ws.card != nil {
		t.Error("a foreign proposal landed on a live Editor invocation")
	}
}

// ---------------------------------------------------------------------------
// The session
// ---------------------------------------------------------------------------

func TestTheEditorCreatesOneSessionBoundToItsProject(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	var sent []sentQuery
	ws.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmd, kind, payload})
	}
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, testDoc(t, "def f():\n    pass"))

	ws.BeginExplain()
	q := lastQuery(t, &sent, protocol.CmdCreateSession)
	if got := q.payload["project_id"]; got != "p1" {
		t.Errorf("project_id = %v, want the Editor's own resolved project — "+
			"it is what CheckFileStale later resolves its scope from", got)
	}
	if ws.pendingSend == nil {
		t.Fatal("the invocation was not held for the session")
	}

	// The reply, and the held invocation goes out.
	payload, _ := json.Marshal(map[string]any{
		"session": map[string]any{"id": "sess-new"},
	})
	ws.pending["mk"] = pendingReq{kind: queryCreateSession}
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: "mk", Type: protocol.EvSessionCreated, Payload: payload,
	}})
	if ws.sessionID != "sess-new" {
		t.Fatalf("sessionID = %q", ws.sessionID)
	}
	if ws.pendingSend != nil {
		t.Error("the held invocation was not released")
	}

	// A second invocation reuses it rather than creating another.
	sent = nil
	ws.inv = nil
	ws.BeginExplain()
	for _, q := range sent {
		if q.cmd == protocol.CmdCreateSession {
			t.Error("a second session was created")
		}
	}
}

// The provider the user picked travels, or Demo mode silently runs against
// config.toml's provider instead.
func TestTheSessionCarriesTheChosenProvider(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	var sent []sentQuery
	ws.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		sent = append(sent, sentQuery{cmd, kind, payload})
	}
	ws.writeFn = func(string, string, map[string]any) error { return nil }
	ws.projectID = "p1"
	ws.docs = append(ws.docs, testDoc(t, "x = 1"))
	ws.Update(app.ProviderDefaultSetMsg{Default: providerDefault("demo", "point-demo-v1")})

	ws.BeginExplain()
	q := lastQuery(t, &sent, protocol.CmdCreateSession)
	if got := q.payload["provider"]; got != "demo" {
		t.Errorf("provider = %v, want the chosen default", got)
	}
	if got := q.payload["model"]; got != "point-demo-v1" {
		t.Errorf("model = %v", got)
	}
}

// ---------------------------------------------------------------------------
// send to chat
// ---------------------------------------------------------------------------

func TestSendToChatHandsOverTheExchangeAndClosesThePanel(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginExplain()
	stream(ws, "It returns nothing.")
	taskDone(ws)

	cmd := ws.panelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("c produced no command")
	}
	msg, ok := cmd().(app.SendToChatMsg)
	if !ok {
		t.Fatalf("c produced %T, want app.SendToChatMsg", cmd())
	}
	if !strings.Contains(msg.Text, "It returns nothing.") {
		t.Errorf("text = %q, want the answer", msg.Text)
	}
	if !strings.Contains(msg.Text, "sample.py") {
		t.Errorf("text = %q, want the file it is about", msg.Text)
	}
	if ws.panel != nil {
		t.Error("the panel stayed open")
	}
}

func TestEscClosesTheExplainPanel(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginExplain()
	stream(ws, "something")
	taskDone(ws)
	ws.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if ws.panel != nil {
		t.Error("esc did not dismiss the panel")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fillCard drives a whole fill up to the card, with the given recorded pair.
func fillCard(t *testing.T, ws *Workspace, mtime float64, size int) *DiffCard {
	t.Helper()
	ws.BeginFill()
	ws.prompt.runes = []rune("add a docstring")
	ws.submitPrompt()
	stream(ws, `{"replacement": "def f():\n    return 1"`)
	taskDone(ws)
	ws.Update(proposalEnvelope(ws, 1, 2, "def f():\n    return 1", mtime, size))
	if ws.card == nil {
		t.Fatal("no card was built")
	}
	return ws.card
}

// applyCard presses enter in review mode, through both halves.
func applyCard(t *testing.T, ws *Workspace, c *DiffCard) {
	t.Helper()
	_, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg, ok := cmd().(diffApplyMsg)
	if !ok {
		t.Fatalf("enter produced %T", cmd())
	}
	ws.Update(msg)
}

// answerStale delivers the CheckFileStale reply for a card.
func answerStale(ws *Workspace, c *DiffCard, changed bool) {
	payload, _ := json.Marshal(map[string]any{"changed": changed})
	id := "stale-" + c.path
	ws.pending[id] = pendingReq{kind: queryStaleCheck, path: c.path, card: c}
	ws.Update(protocol.EnvelopeMsg{Envelope: protocol.Envelope{
		CorrID: id, Type: protocol.EvFileStaleResult, Payload: payload,
	}})
}

func lastQuery(t *testing.T, sent *[]sentQuery, cmdType string) sentQuery {
	t.Helper()
	for i := len(*sent) - 1; i >= 0; i-- {
		if (*sent)[i].cmd == cmdType {
			return (*sent)[i]
		}
	}
	t.Fatalf("no %s was sent (sent: %v)", cmdType, *sent)
	return sentQuery{}
}

// handleEnvelopeForTest reaches the envelope path without wrapping it in a
// tea.Msg, so a test can read the command TaskDone returns.
func (w *Workspace) handleEnvelopeForTest(env protocol.Envelope) tea.Cmd {
	return w.handleEnvelope(env)
}

// providerDefault builds a config.ProviderDefault for the session tests.
func providerDefault(provider, model string) config.ProviderDefault {
	return config.ProviderDefault{Provider: provider, Model: model}
}

// queryUncorrelated marks a recorded write that went through writeFn
// rather than sendFn, which registers no queryKind at all.
const queryUncorrelated queryKind = -1

// ---------------------------------------------------------------------------
// Done-when 5, end to end at the wire boundary -- the regression the gate
// caught and the unit tests did not
// ---------------------------------------------------------------------------
//
// These drive the gate's own two scripted sequences through the REAL key
// handler, with autosave in its real default mode, and fire every timer
// those keystrokes arm -- which is the step the previous test missed.
// TestApplyingDoesNotArmAutosave asserted that the APPLY arms nothing and
// then typed a character to prove typing still does; it never pressed
// ctrl+z, and ctrl+z is a Splice like any other, so the undo armed the
// timer the apply had been stopped from arming and the file was written one
// second later with identical bytes and a new mtime.
//
// "No CmdWriteProjectFile ever leaves this workspace" is the honest
// frontend-side form of "the file on disk is untouched": there is exactly
// one wire write in this package (save.go's single send) and the frontend
// cannot touch a file any other way -- CLAUDE.md's first hard rule,
// enforced structurally. The gate's own LastWriteTime comparison is the
// other half and stays.

// realAutosaveWorkspace is invokeWorkspace with autosave configured the way
// a default profile configures it -- after_delay, which is the mode the
// regression needed and which the earlier tests left at a zero value.
func realAutosaveWorkspace(t *testing.T, content string) (*Workspace, *[]sentQuery) {
	t.Helper()
	ws, sent := invokeWorkspace(t, content)
	ws.autosaveMode = config.AutosaveAfterDelay
	ws.autosaveDelay = time.Millisecond
	ws.holder = "tester@1"
	return ws, sent
}

// drain runs a command and feeds whatever it yields back into the
// workspace, repeatedly, so an armed timer actually fires inside the test
// instead of being discarded as "a command we did not run".
func drain(t *testing.T, ws *Workspace, cmd tea.Cmd) {
	t.Helper()
	for i := 0; cmd != nil && i < 8; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = ws.Update(msg)
	}
}

func assertNoWrite(t *testing.T, sent *[]sentQuery, when string) {
	t.Helper()
	for _, q := range *sent {
		if q.cmd == protocol.CmdWriteProjectFile {
			t.Fatalf("%s: a WriteProjectFile went out -- the file was written "+
				"with no ctrl+s. payload: %v", when, q.payload)
		}
	}
}

// The gate's Step 4 sequence: fill, edit the proposal, apply, one ctrl+z.
// Nothing may reach disk, and the buffer must end byte-identical to what
// it started as.
func TestApplyThenUndoNeverWritesTheFile(t *testing.T) {
	const original = "def f():\n    pass"
	ws, sent := realAutosaveWorkspace(t, original)
	c := fillCard(t, ws, 1791623519.07509, 4096)

	applyCard(t, ws, c)
	answerStale(ws, c, false)
	if !c.Applied() {
		t.Fatalf("the apply was refused: %q", c.refusal)
	}
	assertNoWrite(t, sent, "after the apply")

	ws.focus = focusBuffer
	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	// The undo ARMS the timer -- F13b's autosave rule 4, unchanged -- so
	// this is the step that matters: fire it.
	drain(t, ws, cmd)

	assertNoWrite(t, sent, "after ctrl+z and its autosave tick")
	if got := ws.docs[0].buf.Text(); got != original {
		t.Errorf("buffer = %q, want it back to %q", got, original)
	}
	if ws.docs[0].dirty {
		t.Error("the buffer is still marked modified although it matches the file")
	}
}

// The gate's Step 3 sequence: fill, stream, switch away and back, esc.
// Nothing is spliced, so nothing can be written -- asserted at the wire
// rather than inferred from the buffer.
func TestTheGhostPathNeverWritesTheFile(t *testing.T) {
	const original = "def f():\n    pass"
	ws, sent := realAutosaveWorkspace(t, original)

	ws.BeginFill()
	ws.prompt.runes = []rune("add a docstring")
	ws.submitPrompt()
	ws.inv.taskID = "task-1"
	stream(ws, `{"replacement": "def f():\n    \"\"\"Doc.\"\"\"`)

	// Away and back: Root delivers to a backgrounded workspace, which is
	// what WantsBackgroundReplies asks for.
	if !ws.WantsBackgroundReplies() {
		t.Fatal("the workspace stopped accepting its own events mid-stream")
	}
	stream(ws, `\n    pass"`)

	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyEsc})
	drain(t, ws, cmd)

	assertNoWrite(t, sent, "after a cancelled fill")
	if got := ws.docs[0].buf.Text(); got != original {
		t.Errorf("buffer = %q, want it untouched", got)
	}
	if ws.docs[0].dirty {
		t.Error("a cancelled fill marked the buffer modified")
	}
}

// And the half that must NOT change: a real divergence still autosaves.
// The guard above is "nothing to persist", not "autosave is off".
func TestARealEditStillAutosaves(t *testing.T) {
	ws, sent := realAutosaveWorkspace(t, "def f():\n    pass")
	ws.focus = focusBuffer

	_, cmd := ws.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	drain(t, ws, cmd)

	found := false
	for _, q := range *sent {
		if q.cmd == protocol.CmdWriteProjectFile {
			found = true
		}
	}
	if !found {
		t.Fatal("typing no longer autosaves -- the content guard is too broad")
	}
}

// An apply the user then saves on purpose DOES reach disk. Done-when 5 is
// "unchanged until the user saves", not "unwritable".
func TestAnAppliedRegionReachesDiskOnAnExplicitSave(t *testing.T) {
	ws, sent := realAutosaveWorkspace(t, "def f():\n    pass")
	c := fillCard(t, ws, 1791623519.07509, 4096)
	applyCard(t, ws, c)
	answerStale(ws, c, false)
	assertNoWrite(t, sent, "after the apply alone")

	ws.focus = focusBuffer
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	q := lastQuery(t, sent, protocol.CmdWriteProjectFile)
	if got, _ := q.payload["content"].(string); !strings.Contains(got, "return 1") {
		t.Errorf("content = %q, want the applied region", got)
	}
}

// ---------------------------------------------------------------------------
// Paste goes to whichever surface owns the keyboard
// ---------------------------------------------------------------------------

// F15's gate: a paste while the fill prompt was open went into the FILE at
// the caret, invisibly, and autosave then persisted it one second later --
// which is where the "nothing wrote app.py" failures came from. The write
// was the correct response to a wrong edit.
func TestAPasteWhileThePromptIsOpenLandsInThePrompt(t *testing.T) {
	const original = "def f():\n    pass"
	ws, sent := realAutosaveWorkspace(t, original)
	ws.BeginFill()
	if ws.prompt == nil {
		t.Fatal("no prompt")
	}

	_, cmd := ws.Update(app.PasteMsg{Text: "add a type check"})
	drain(t, ws, cmd)

	if got := ws.prompt.text(); got != "add a type check" {
		t.Errorf("prompt = %q, want the pasted instruction", got)
	}
	if got := ws.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer changed to %q -- the paste fell through to the file", got)
	}
	if ws.docs[0].dirty {
		t.Error("the paste marked the file dirty")
	}
	assertNoWrite(t, sent, "after a paste into the prompt")

	// And it is still editable afterwards: the caret is at the end.
	ws.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := ws.prompt.text(); got != "add a type chec" {
		t.Errorf("prompt = %q after backspace", got)
	}
}

// A multi-line paste is flattened rather than truncated or sent with
// embedded newlines a one-line field cannot show.
func TestAMultiLinePasteIsFlattenedIntoThePrompt(t *testing.T) {
	ws, _ := realAutosaveWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.Update(app.PasteMsg{Text: "add a type check\nand a docstring\r\nplease"})

	got := ws.prompt.text()
	if strings.ContainsAny(got, "\r\n\t") {
		t.Errorf("prompt = %q, want no line breaks in a one-line field", got)
	}
	for _, word := range []string{"type check", "docstring", "please"} {
		if !strings.Contains(got, word) {
			t.Errorf("prompt = %q, lost %q", got, word)
		}
	}
}

// The explain panel owns the keyboard while it is open, so a paste does
// not fall past it into the file either.
func TestAPasteWhileThePanelIsOpenTouchesNothing(t *testing.T) {
	const original = "def f():\n    pass"
	ws, sent := realAutosaveWorkspace(t, original)
	ws.BeginExplain()
	if ws.panel == nil {
		t.Fatal("no panel")
	}

	_, cmd := ws.Update(app.PasteMsg{Text: "stray clipboard text"})
	drain(t, ws, cmd)

	if got := ws.docs[0].buf.Text(); got != original {
		t.Errorf("the buffer changed to %q", got)
	}
	assertNoWrite(t, sent, "after a paste into the panel")
}

// And the ordinary case is untouched: a paste into the buffer with nothing
// open still lands, still marks the file dirty, and is still ONE ctrl+z.
//
// This is the half of Part 2 #3 that turned out not to be a bug: the
// misrouted paste went through insertText(editPaste) like any other, so it
// was always undoable. What was not undoable was anything -- while a diff
// card had the keyboard, review mode binds no ctrl+z at all (see
// reviewKey's own note).
func TestAPasteIntoTheBufferIsStillOneUndo(t *testing.T) {
	const original = "def f():\n    pass"
	ws, _ := realAutosaveWorkspace(t, original)
	ws.focus = focusBuffer

	ws.Update(app.PasteMsg{Text: "XYZ"})
	if got := ws.docs[0].buf.Text(); !strings.HasPrefix(got, "XYZ") {
		t.Fatalf("buffer = %q, want the paste", got)
	}
	ws.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got := ws.docs[0].buf.Text(); got != original {
		t.Errorf("one ctrl+z left %q, want %q", got, original)
	}
}

// A proposal that arrives while the Editor IS on screen opens its card
// immediately -- the deferral is for the backgrounded case only, and must
// not quietly become the only path.
func TestAProposalArrivingWhileActiveOpensItsCardAtOnce(t *testing.T) {
	ws, _ := invokeWorkspace(t, "def f():\n    pass")
	ws.BeginFill()
	ws.prompt.runes = []rune("add a docstring")
	ws.submitPrompt()
	stream(ws, `{"replacement": "def f():\n    return 1"`)
	taskDone(ws)

	_, cmd := ws.Update(proposalEnvelope(ws, 1, 2, "def f():\n    return 1", 1.5, 2))
	if cmd == nil {
		t.Fatal("no command: the card was built but nothing asked for it to open")
	}
	if _, ok := cmd().(app.OpenWorkspaceOverlayMsg); !ok {
		t.Fatalf("produced %T, want app.OpenWorkspaceOverlayMsg", cmd())
	}
	if ws.deferredCard != nil {
		t.Error("the card was ALSO held for a switch back, so it would open twice")
	}
}
