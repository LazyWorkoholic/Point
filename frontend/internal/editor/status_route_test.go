package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// Reported four times as "no message at all": ctrl+s on a read-only file,
// the refused close of a dirty tab, f5, and an opened read-only file.
//
// One cause. w.note was rendered ONLY by the empty state, and the empty
// state is by definition not on screen whenever a file is open -- which is
// whenever any of those can happen. The note is now emitted as an
// app.WorkspaceStatusMsg, which Root puts in the status bar.

// saidBy runs a workspace action and returns the text it emitted, or "".
func saidBy(cmd tea.Cmd) string {
	if cmd == nil {
		return ""
	}
	msg := cmd()
	if m, ok := msg.(app.WorkspaceStatusMsg); ok {
		return m.Text
	}
	return ""
}

func TestCtrlSOnAReadOnlyFileSaysWhy(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:/p/readonly.py", Content: "y = 1",
		LineEnding: lineEndingLF, TrailingNewline: true, Writable: false,
	}, "C:/p", true))

	said := saidBy(ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS}))
	if !strings.Contains(said, "read-only") {
		t.Errorf("said %q, want a read-only message", said)
	}
	if !strings.Contains(said, string(roNotWritable)) {
		t.Errorf("said %q, want the reason", said)
	}
}

// F13a's TestCtrlSOnAWritableFileSaysTheSavePathIsNotWired asserted the
// refusal this episode exists to remove. Replaced rather than deleted:
// the thing worth pinning at this call site is that ctrl+s reaches the
// wire at all, and that it does NOT when there is no holder to send.
func TestCtrlSOnAWritableFileSendsAWrite(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.holder = "aryan@id"
	ws.docs = append(ws.docs, testDoc(t, "x"))

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})

	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdWriteProjectFile {
		t.Fatalf("sent %+v, want one WriteProjectFile", rec.sent)
	}
}

// With no holder key nothing is sent, because WriteProjectFile refuses an
// empty holder with an Error envelope -- so sending one would turn a
// configuration problem into a wire error per save.
func TestCtrlSWithNoHolderSendsNothingAndSaysWhy(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "x"))

	said := saidBy(ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS}))

	if len(rec.sent) != 0 {
		t.Fatalf("sent %+v with no holder", rec.sent)
	}
	if !strings.Contains(said, "lock identity") {
		t.Errorf("said %q", said)
	}
}

func TestF5SaysItIsReindexing(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	said := saidBy(ws.handleKey(tea.KeyMsg{Type: tea.KeyF5}))
	if !strings.Contains(said, "re-index") {
		t.Errorf("said %q", said)
	}
	// And it actually asked, with refresh forced past the backend's own
	// 30-second index cache.
	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdListProjectFiles {
		t.Fatalf("sent %+v", rec.sent)
	}
	if rec.sent[0].payload["refresh"] != true {
		t.Error("f5 did not force a refresh")
	}
}

// Done-when 9, through the × on the tab strip rather than a key: the
// click opens C3's Confirm and closes nothing until it is answered.
//
// F13a's version of this test asserted a flat REFUSAL, which was that
// session's honest "not yet". The close path is the same; what changed
// is that there is now somewhere for the answer to come from.
func TestClickingCloseOnADirtyTabOpensTheConfirm(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.docs[0].insertRune('x')
	ws.View(ws.lastLayout)
	if len(ws.tabSpans) == 0 || ws.tabSpans[0].closeAt < 0 {
		t.Fatal("precondition: no × was drawn")
	}

	base := ws.tabStrip.X
	cmd := ws.handleMouse(mouse(base+ws.tabSpans[0].closeAt, ws.tabStrip.Y,
		tea.MouseButtonLeft, tea.MouseActionPress))

	if len(ws.docs) != 1 {
		t.Fatal("the dirty tab was closed without a confirmation")
	}
	if !openedConfirm(t, cmd) {
		t.Error("no Confirm was opened")
	}
}

func TestOpeningAReadOnlyFileSaysSo(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:/p"

	raw := `{"file":{"path":"C:/p/bad.py","content":"x","line_ending":"lf",` +
		`"trailing_newline":true,"writable":true,"decode_replacements":2}}`
	ws.pending["c1"] = pendingReq{kind: queryReadFile}
	said := saidBy(ws.handleEnvelope(protocol.Envelope{
		CorrID: "c1", Type: protocol.EvProjectFile, Payload: []byte(raw),
	}))
	if !strings.Contains(said, "read-only") || !strings.Contains(said, string(roLossyDecode)) {
		t.Errorf("said %q, want the read-only reason", said)
	}
}

func TestABackendRefusalReachesTheStatusBar(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.pending["c1"] = pendingReq{kind: queryReadFile}
	said := saidBy(ws.handleEnvelope(protocol.Envelope{
		CorrID: "c1", Type: protocol.EvError,
		Payload: []byte(`{"code":"invalid_event","message":"Not a file: C:/p/nope"}`),
	}))
	if !strings.Contains(said, "Not a file") {
		t.Errorf("said %q", said)
	}
}

// A refusal from the TREE's own query stays on the tree card: it is about
// the card's contents, not about an action the user just took, and the
// card has somewhere to put it.
func TestATreeRefusalStaysOnTheCard(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.pending["c1"] = pendingReq{kind: queryListFiles}
	said := saidBy(ws.handleEnvelope(protocol.Envelope{
		CorrID: "c1", Type: protocol.EvError,
		Payload: []byte(`{"code":"x","message":"index refused"}`),
	}))
	if said != "" {
		t.Errorf("said %q; a tree refusal should not take the status bar", said)
	}
	if ws.tree.note != "index refused" {
		t.Errorf("tree note = %q", ws.tree.note)
	}
}

// say() keeps a copy for the empty state, so a refusal that leaves nothing
// open is still explained by the screen the user ends up looking at.
func TestTheEmptyStateStillRepeatsTheLastNote(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:/p"
	ws.pending["c1"] = pendingReq{kind: queryReadFile}
	ws.handleEnvelope(protocol.Envelope{
		CorrID: "c1", Type: protocol.EvError,
		Payload: []byte(`{"code":"x","message":"Not a file: C:/p/nope"}`),
	})
	frame := stripANSI(ws.View(layoutFor(ws, 120, 30)))
	if !strings.Contains(frame, "Not a file") {
		t.Error("the empty state does not repeat the refusal")
	}
}
