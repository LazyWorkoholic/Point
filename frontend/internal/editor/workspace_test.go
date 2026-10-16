package editor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/layout"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// recorder is the sendFn seam: what the tests exercise is the decision
// logic in onProject/onFileList/onFile, not protocol.Client's send path,
// which that package tests directly. Same shape chat uses.
type recorder struct {
	sent []sentQuery
}

type sentQuery struct {
	cmd     string
	kind    queryKind
	payload map[string]any
}

func newWorkspaceWithRecorder() (*Workspace, *recorder) {
	ws := New(Options{Theme: testTheme()})
	rec := &recorder{}
	ws.sendFn = func(cmd string, kind queryKind, payload map[string]any) {
		rec.sent = append(rec.sent, sentQuery{cmd, kind, payload})
	}
	return ws, rec
}

// reply feeds a payload in as if it had arrived correlated to the last
// query of that kind.
func reply(t *testing.T, ws *Workspace, kind queryKind, evType string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	id := "corr-" + evType
	ws.pending[id] = pendingReq{kind: kind}
	ws.handleEnvelope(protocol.Envelope{CorrID: id, Type: evType, Payload: raw})
}

// replyFor is reply() for the F13b queries, which are about one DOCUMENT
// and are correlated back to it through pendingReq.path -- a
// FileLockResult payload names no file at all, so without the path there
// is nothing to match on.
func replyFor(t *testing.T, ws *Workspace, kind queryKind, path, evType string, payload any) tea.Cmd {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	id := "corr-" + evType + "-" + path
	ws.pending[id] = pendingReq{kind: kind, path: path}
	return ws.handleEnvelope(protocol.Envelope{CorrID: id, Type: evType, Payload: raw})
}

// ---------------------------------------------------------------------------
// The Workspace contract
// ---------------------------------------------------------------------------

// Every narrow interface this workspace is supposed to satisfy, asserted at
// compile time in one place. A workspace that stops implementing one fails
// silently otherwise -- Root's type assertion just falls through, and the
// status bar or the key goes quiet for no visible reason.
func TestWorkspaceImplementsTheInterfacesRootAsksFor(t *testing.T) {
	var ws any = New(Options{Theme: testTheme()})
	if _, ok := ws.(app.Workspace); !ok {
		t.Error("not an app.Workspace")
	}
	if _, ok := ws.(app.KeyCapturer); !ok {
		t.Error("not an app.KeyCapturer — tab would focus the next pane")
	}
	if _, ok := ws.(app.StatusContributor); !ok {
		t.Error("not an app.StatusContributor — the status bar would show bindings")
	}
	if _, ok := ws.(app.DirtyBuffers); !ok {
		t.Error("not an app.DirtyBuffers — an idle unload would discard edits")
	}
	if _, ok := ws.(app.ThemeAware); !ok {
		t.Error("not an app.ThemeAware — Settings could not repaint it")
	}
}

func TestIDIsTheEditorWorkspace(t *testing.T) {
	if got := New(Options{Theme: testTheme()}).ID(); got != layout.WorkspaceEditor {
		t.Errorf("ID = %q", got)
	}
}

// CapturesKey claims EXACTLY two keys. This is the test that fails if the
// claim list ever grows quietly: every captured key is a key whose global
// binding stops working in this workspace, so the list is a decision and
// not an implementation detail.
func TestCapturesKeyClaimsExactlyTabAndShiftTab(t *testing.T) {
	ws := New(Options{Theme: testTheme()})

	for _, key := range []string{"tab", "shift+tab"} {
		if !ws.CapturesKey(key) {
			t.Errorf("%q is not claimed; Tab would not indent", key)
		}
	}
	// Everything else keeps its global meaning here -- the palette, quit,
	// the leader, redraw, the tray, and the four Chat-only chords.
	for _, key := range []string{
		"esc", "enter", "ctrl+a", "ctrl+b", "ctrl+c", "ctrl+e", "ctrl+l",
		"ctrl+n", "ctrl+o", "ctrl+p", "ctrl+q", "ctrl+r", "ctrl+s", "ctrl+w",
		"ctrl+z", "f1", "up", "down", "left", "right", "a", "x",
	} {
		if ws.CapturesKey(key) {
			t.Errorf("%q is claimed; its global binding would stop working here", key)
		}
	}
}

// The FILE TREE takes the larger share and flexes; Task is the smaller
// fixed box. A deliberate divergence from LAYOUT.md's sketch -- see
// Cards' own doc for why (the tray already shows a task in full; the tree
// is the only way to open a file here).
func TestCardsGiveTheRemainderToTheFileTree(t *testing.T) {
	cards := New(Options{Theme: testTheme()}).Cards()
	if len(cards) != 2 {
		t.Fatalf("%d cards, want 2 (File Tree, Task)", len(cards))
	}
	if !cards[0].Flex {
		t.Error("the File Tree card does not flex; it should take the remainder")
	}
	if cards[1].Flex {
		t.Error("the Task card flexes; it should be the smaller fixed box")
	}
	if cards[0].MinHeight <= cards[1].MinHeight {
		t.Errorf("File Tree asks for %d rows, Task for %d — the tree should ask for more",
			cards[0].MinHeight, cards[1].MinHeight)
	}

	// And the laid-out rects agree: at a full-size terminal the tree is
	// taller than the Task card, which is the thing the user sees.
	l := layout.Compute(120, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: true,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: cards,
	})
	if len(l.RightPane) != 2 {
		t.Fatalf("%d rects", len(l.RightPane))
	}
	if l.RightPane[0].H <= l.RightPane[1].H {
		t.Errorf("tree rect %d rows, task rect %d rows", l.RightPane[0].H, l.RightPane[1].H)
	}
}

// ---------------------------------------------------------------------------
// The wire sequence
// ---------------------------------------------------------------------------

// The Editor has no session and so no project_id: it resolves one itself
// from the working directory. That is the first thing it does on connect,
// and the tree follows from the answer.
func TestConnectResolvesTheProjectThenListsFiles(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.onConnected()

	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdProjectResolve {
		t.Fatalf("sent %+v, want one ProjectResolve", rec.sent)
	}
	if _, ok := rec.sent[0].payload["path"]; !ok {
		t.Error("ProjectResolve carries no path")
	}

	reply(t, ws, queryResolveProject, protocol.EvProject, map[string]any{
		"project": map[string]any{"id": "p1", "name": "Point", "root_path": "C:\\p"},
	})

	if ws.projectID != "p1" || ws.projectRoot != "C:\\p" {
		t.Fatalf("project = %q at %q", ws.projectID, ws.projectRoot)
	}
	if len(rec.sent) != 2 || rec.sent[1].cmd != protocol.CmdListProjectFiles {
		t.Fatalf("sent %+v, want a ListProjectFiles", rec.sent)
	}
	// The backend defaults limit to 200 and does matched[:limit], which
	// would silently truncate this repo's own tree.
	if got := rec.sent[1].payload["limit"]; got != treeLimit {
		t.Errorf("limit = %v, want %d", got, treeLimit)
	}
}

// resolve() never raises for "no project found" -- it is a legitimate,
// common answer, and the tree says so rather than looking broken.
func TestNoProjectIsReportedNotRetried(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.onConnected()
	reply(t, ws, queryResolveProject, protocol.EvProject, map[string]any{"project": nil})

	if ws.projectID != "" {
		t.Errorf("projectID = %q, want empty", ws.projectID)
	}
	if len(rec.sent) != 1 {
		t.Errorf("sent %d queries; a missing project must not trigger a list", len(rec.sent))
	}
	if !strings.Contains(ws.tree.note, ".point") {
		t.Errorf("tree note = %q, want it to name the missing marker", ws.tree.note)
	}
}

func TestFileListBuildsTheTree(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	reply(t, ws, queryListFiles, protocol.EvProjectFileList, map[string]any{
		"files": []string{"a.py", "pkg/b.py", "pkg/sub/c.py"},
	})

	if !ws.tree.loaded {
		t.Fatal("the tree did not load")
	}
	// pkg is collapsed by default, so the visible rows are a.py and pkg\.
	if len(ws.tree.rows) != 2 {
		t.Fatalf("%d visible rows, want 2", len(ws.tree.rows))
	}
}

// A truncated list is said out loud: the backend does matched[:limit], so a
// project at the cap has a tree missing its tail that looks complete.
func TestATruncatedFileListSaysSo(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	files := make([]string, treeLimit)
	for i := range files {
		files[i] = "f" + itoa(i) + ".py"
	}
	reply(t, ws, queryListFiles, protocol.EvProjectFileList,
		map[string]any{"files": files})

	if !strings.Contains(ws.tree.note, "first") {
		t.Errorf("note = %q, want a truncation notice", ws.tree.note)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestOpeningAFileSendsTheProjectIDForAnInProjectPath(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"

	ws.openPath("C:\\p\\a.py")
	last := rec.sent[len(rec.sent)-1]
	if last.cmd != protocol.CmdReadProjectFile {
		t.Fatalf("cmd = %q", last.cmd)
	}
	if last.payload["project_id"] != "p1" {
		t.Errorf("project_id = %v, want p1", last.payload["project_id"])
	}

	// A path outside the project carries no project_id — it is not that
	// project's file, and claiming it was would scope a lock wrongly.
	ws.openPath("D:\\elsewhere\\b.py")
	last = rec.sent[len(rec.sent)-1]
	if _, ok := last.payload["project_id"]; ok {
		t.Error("an out-of-project path carried a project_id")
	}
}

// Opening a file already open focuses its tab rather than loading it twice.
func TestOpeningAnAlreadyOpenFileSwitchesToItsTab(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "C:\\p\\a.py", "content": "x", "line_ending": "lf",
			"trailing_newline": true, "writable": true,
		},
	})
	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "C:\\p\\b.py", "content": "y", "line_ending": "lf",
			"trailing_newline": true, "writable": true,
		},
	})
	if len(ws.docs) != 2 || ws.active != 1 {
		t.Fatalf("%d docs, active %d", len(ws.docs), ws.active)
	}

	before := len(rec.sent)
	ws.openPath("C:\\p\\a.py")
	if len(rec.sent) != before {
		t.Error("a second read was sent for an already-open file")
	}
	if ws.active != 0 {
		t.Errorf("active = %d, want the existing tab", ws.active)
	}
}

// An Error envelope is shown and dropped. A path denial or an unresolvable
// project means the editor should not have offered the open, so nothing
// retries.
func TestAnErrorEnvelopeIsReportedAndNotRetried(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	before := len(rec.sent)

	reply(t, ws, queryReadFile, protocol.EvError, map[string]any{
		"code": "invalid_event", "message": "Not a file: C:\\p\\nope",
	})
	if !strings.Contains(ws.note, "Not a file") {
		t.Errorf("note = %q", ws.note)
	}
	if len(rec.sent) != before {
		t.Error("the refusal triggered another query")
	}
	if len(ws.docs) != 0 {
		t.Error("a document was created from an Error")
	}
}

// An envelope this workspace did not ask for is ignored. Replies are
// correlated by the id it minted, which is what keeps another workspace's
// reply from landing here.
func TestAnUncorrelatedEnvelopeIsIgnored(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.handleEnvelope(protocol.Envelope{
		CorrID: "somebody-else", Type: protocol.EvProjectFile,
		Payload: json.RawMessage(`{"file":{"path":"x","content":"y"}}`),
	})
	if len(ws.docs) != 0 {
		t.Error("an uncorrelated reply opened a document")
	}
}

// index.respect_gitignore is B26's key and F10b's Behaviour tab owns the
// control. The tree does not own the toggle -- it re-indexes when it
// moves, which is what makes the toggle visible without a second control
// over one backend key.
func TestABehaviourOverrideChangeRefreshesTheTree(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	before := len(rec.sent)

	ws.Update(app.BehaviourOverridesSetMsg{})

	if len(rec.sent) != before+1 {
		t.Fatalf("sent %d queries, want one refresh", len(rec.sent)-before)
	}
	last := rec.sent[len(rec.sent)-1]
	if last.cmd != protocol.CmdListProjectFiles {
		t.Fatalf("cmd = %q", last.cmd)
	}
	if last.payload["refresh"] != true {
		t.Error("the refresh was not forced; the 30s index cache would answer")
	}
}

// ---------------------------------------------------------------------------
// Save, this session
// ---------------------------------------------------------------------------

// ctrl+s sends exactly ONE WriteProjectFile, carrying the metadata the
// read reported rather than anything re-derived from the buffer.
//
// This is Done-when 2's mechanism: a CRLF file round-trips because
// line_ending, bom and trailing_newline go back out exactly as they came
// in, and expected_mtime is the mtime from the read rather than a fresh
// stat -- which would reopen the race the precondition exists to close.
func TestCtrlSSendsTheMetadataTheReadReported(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.holder = "aryan@id"
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:\\p\\x.py", Content: "a\nb", Mtime: 1712345678.25, Size: 4,
		LineEnding: lineEndingCRLF, CRLFCount: 2, Bom: true,
		TrailingNewline: true, Writable: true,
	}, "C:\\p", true))

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})

	if len(rec.sent) != 1 {
		t.Fatalf("sent %+v, want one", rec.sent)
	}
	q := rec.sent[0]
	if q.cmd != protocol.CmdWriteProjectFile || q.kind != queryWriteFile {
		t.Fatalf("cmd = %q kind = %v", q.cmd, q.kind)
	}
	want := map[string]any{
		"path":             "C:\\p\\x.py",
		"content":          "a\nb",
		"holder":           "aryan@id",
		"expected_mtime":   1712345678.25,
		"overwrite":        false,
		"line_ending":      lineEndingCRLF,
		"bom":              true,
		"trailing_newline": true,
		"project_id":       "p1",
	}
	for k, v := range want {
		if got := q.payload[k]; got != v {
			t.Errorf("payload[%q] = %#v, want %#v", k, got, v)
		}
	}
}

// On a read-only file ctrl+s reports the READ-ONLY reason instead, which is
// the more specific truth.
func TestCtrlSOnAReadOnlyFileReportsTheReason(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:\\p\\x.py", Content: "a", LineEnding: lineEndingLF, Writable: false,
	}, "C:\\p", true))

	ws.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(ws.note, string(roNotWritable)) {
		t.Errorf("note = %q", ws.note)
	}
}

// ---------------------------------------------------------------------------
// Dirty buffers
// ---------------------------------------------------------------------------

func TestDirtyBufferCount(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"), testDoc(t, "b"))
	if got := ws.DirtyBufferCount(); got != 0 {
		t.Fatalf("count = %d, want 0", got)
	}
	ws.docs[1].insertRune('x')
	if got := ws.DirtyBufferCount(); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Tabs
// ---------------------------------------------------------------------------

func TestTabStripMarksTheDirtyFileAndHighlightsTheActiveOne(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.docs = append(ws.docs, testDoc(t, "b"))
	ws.docs[1].path = "C:\\p\\second.py"
	ws.docs[1].rel = "second.py"
	ws.docs[1].insertRune('x')

	strip, spans := ws.renderTabs(60)
	plain := stripANSI(strip)
	if !strings.Contains(plain, "• second.py") {
		t.Errorf("strip = %q, want a dirty marker on second.py", plain)
	}
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2", len(spans))
	}
	// Every span has a close cell inside its own bounds.
	for _, s := range spans {
		if s.closeAt < s.from || s.closeAt >= s.to {
			t.Errorf("span %+v: close cell outside the tab", s)
		}
	}
}

// LAYOUT.md: "below 70 columns the tab strip collapses to the active
// filename only".
func TestTabStripCollapsesWhenNarrow(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	ws.docs = append(ws.docs, testDoc(t, "b"))
	ws.docs[1].rel = "second.py"
	ws.active = 1

	strip, spans := ws.renderTabs(20)
	plain := stripANSI(strip)
	if strings.Contains(plain, "sample.py") {
		t.Errorf("strip = %q, want only the active filename", plain)
	}
	if len(spans) != 1 || spans[0].index != 1 {
		t.Errorf("spans = %+v, want only the active tab", spans)
	}
}

// The strip never draws wider than it was given, at any width, with any
// number of tabs -- the overflow markers are part of the budget, not added
// on top of it.
func TestTabStripNeverExceedsItsWidth(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	for i := 0; i < 12; i++ {
		d := testDoc(t, "x")
		d.rel = "file-with-a-long-name-" + itoa(i) + ".py"
		ws.docs = append(ws.docs, d)
	}
	for _, width := range []int{10, 20, 44, 60, 80, 120, 200} {
		for _, active := range []int{0, 3, 6, 12} {
			ws.active = active
			strip, _ := ws.renderTabs(width)
			if got := visibleWidth(strip); got > width {
				t.Errorf("width %d active %d: strip is %d cells", width, active, got)
			}
		}
	}
}

// Overflow keeps the active tab visible -- a strip that scrolls away from
// the file you are editing is worse than no strip.
func TestTabStripKeepsTheActiveTabVisible(t *testing.T) {
	ws := testWorkspace(t, "a", 120, 30)
	for i := 0; i < 12; i++ {
		d := testDoc(t, "x")
		d.rel = "name-" + itoa(i) + ".py"
		ws.docs = append(ws.docs, d)
	}
	for active := 0; active < len(ws.docs); active++ {
		ws.active = active
		_, spans := ws.renderTabs(50)
		found := false
		for _, s := range spans {
			if s.index == active {
				found = true
			}
		}
		if !found {
			t.Errorf("active tab %d is not in the strip", active)
		}
	}
}

func TestCycleTabWraps(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"), testDoc(t, "b"), testDoc(t, "c"))
	ws.cycleTab(+1)
	if ws.active != 1 {
		t.Fatalf("active = %d", ws.active)
	}
	ws.cycleTab(-2)
	if ws.active != 2 {
		t.Errorf("active = %d, want a wrap to 2", ws.active)
	}
}

// Closing a DIRTY file is refused this session: F13's Done-when #9 routes
// it through Confirm, and that needs the save path's overlay. Refusing and
// saying why is the honest "not yet"; a dialog offering only to discard
// would be worse.
func TestClosingADirtyTabRoutesThroughConfirm(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"))
	ws.docs[0].insertRune('x')

	note, cmd := ws.closeTab(0)
	if len(ws.docs) != 1 {
		t.Fatal("a dirty tab was closed without a confirmation")
	}
	if note != "" {
		t.Errorf("note = %q; the dirty path speaks through the Confirm, not the status bar", note)
	}
	// F13b: it opens C3's Confirm rather than refusing outright.
	m, ok := cmd().(app.OpenWorkspaceOverlayMsg)
	if !ok {
		t.Fatalf("closeTab returned %T, want an OpenWorkspaceOverlayMsg", cmd())
	}
	if _, ok := m.Overlay.(*app.Confirm); !ok {
		t.Fatalf("overlay = %T, want *app.Confirm", m.Overlay)
	}
}

func TestClosingACleanTab(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "a"), testDoc(t, "b"))
	ws.active = 1
	if _, cmd := ws.closeTab(1); cmd != nil {
		t.Error("a CLEAN tab must close without a confirmation")
	}
	if len(ws.docs) != 1 || ws.active != 0 {
		t.Errorf("%d docs, active %d", len(ws.docs), ws.active)
	}
}

// ---------------------------------------------------------------------------
// Suspend / Resume
// ---------------------------------------------------------------------------

// Position, never content. The blob holds paths, carets and which
// directories were expanded; the content comes back from the backend.
func TestSuspendStoresPositionAndNotContent(t *testing.T) {
	ws := testWorkspace(t, strings.Repeat("a long line of code here\n", 200), 120, 30)
	d := ws.docs[0]
	d.moveTo(Pos{42, 7}, false)
	d.top = 30
	ws.tree.toggle() // expand whatever is selected

	raw, err := ws.Suspend()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "a long line of code here") {
		t.Fatal("the blob carries file content")
	}
	if len(raw) > 2048 {
		t.Errorf("blob is %d bytes; the contract is roughly 2KB of position", len(raw))
	}

	var blob suspendBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	if len(blob.Open) != 1 || blob.Open[0].Line != 42 || blob.Open[0].Col != 7 {
		t.Errorf("blob.Open = %+v", blob.Open)
	}
	if blob.Open[0].Top != 30 {
		t.Errorf("Top = %d, want 30", blob.Open[0].Top)
	}
}

// Every Resume failure degrades to an empty editor rather than erroring:
// an unload must never be blocked by a blob, and a first launch starts in
// exactly the same place.
func TestResumeDegradesOnEveryBadBlob(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		[]byte(""),
		[]byte("not json"),
		[]byte(`{"version":99,"open":[{"path":"x"}]}`),
		[]byte(`{}`),
	} {
		ws, _ := newWorkspaceWithRecorder()
		if err := ws.Resume(raw); err != nil {
			t.Errorf("Resume(%q) returned %v", raw, err)
		}
		if len(ws.docs) != 0 {
			t.Errorf("Resume(%q) opened something", raw)
		}
	}
}

// A resumed blob reopens its files once the tree lands, and restores each
// caret as its content comes back.
func TestResumeReopensAndRestoresTheCaret(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	blob, _ := json.Marshal(suspendBlob{
		Version: suspendVersion,
		Open:    []suspendDoc{{Path: "C:\\p\\a.py", Line: 2, Col: 3, Top: 1}},
		Active:  0,
	})
	if err := ws.Resume(blob); err != nil {
		t.Fatal(err)
	}

	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	reply(t, ws, queryListFiles, protocol.EvProjectFileList,
		map[string]any{"files": []string{"a.py"}})

	last := rec.sent[len(rec.sent)-1]
	if last.cmd != protocol.CmdReadProjectFile || last.payload["path"] != "C:\\p\\a.py" {
		t.Fatalf("last query = %+v, want a read of the resumed file", last)
	}

	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "C:\\p\\a.py", "content": "one\ntwo\nthree\nfour",
			"line_ending": "lf", "trailing_newline": true, "writable": true,
		},
	})

	if len(ws.docs) != 1 {
		t.Fatalf("%d docs", len(ws.docs))
	}
	if got := ws.docs[0].cursor; got != (Pos{2, 3}) {
		t.Errorf("cursor = %+v, want {2 3}", got)
	}
	if ws.docs[0].top != 1 {
		t.Errorf("top = %d, want 1", ws.docs[0].top)
	}
	if ws.resume != nil {
		t.Error("the resume blob was not consumed")
	}
}

// A caret past the end of a file that changed on disk while Point was
// away is clamped, not trusted.
func TestResumeClampsACaretPastTheEnd(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	blob, _ := json.Marshal(suspendBlob{
		Version: suspendVersion,
		Open:    []suspendDoc{{Path: "C:\\p\\a.py", Line: 900, Col: 900}},
	})
	_ = ws.Resume(blob)
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	reply(t, ws, queryListFiles, protocol.EvProjectFileList,
		map[string]any{"files": []string{"a.py"}})
	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "C:\\p\\a.py", "content": "short",
			"line_ending": "lf", "trailing_newline": true, "writable": true,
		},
	})
	if got := ws.docs[0].cursor; got != (Pos{0, 5}) {
		t.Errorf("cursor = %+v, want the clamped end", got)
	}
}

// ---------------------------------------------------------------------------
// Status segments
// ---------------------------------------------------------------------------

func TestStatusSegmentsReportTheFileAndItsBytes(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.docs = append(ws.docs, newDoc(wireFile{
		Path: "C:\\p\\pkg\\a.py", Content: "def f():\n    return 1",
		LineEnding: lineEndingCRLF, CRLFCount: 2, Bom: true,
		TrailingNewline: false, Writable: true,
	}, "C:\\p", true))
	ws.docs[0].moveTo(Pos{1, 4}, false)

	segs := strings.Join(ws.StatusSegments(), " | ")
	for _, want := range []string{"pkg/a.py", "2:5", "python", "CRLF", "BOM", "no eol"} {
		if !strings.Contains(segs, want) {
			t.Errorf("segments %q do not contain %q", segs, want)
		}
	}
	// No lock segment: nothing acquires a lock this session, so claiming
	// one would be a hint that lies.
	if strings.Contains(strings.ToLower(segs), "lock") {
		t.Errorf("segments %q claim a lock state", segs)
	}
}

func TestStatusSegmentsShowTheDirtyMarkerAndTheReadOnlyReason(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "x"))
	ws.docs[0].insertRune('y')
	if !strings.Contains(strings.Join(ws.StatusSegments(), " "), "modified") {
		t.Error("a dirty file is not marked")
	}

	ws2, _ := newWorkspaceWithRecorder()
	ws2.docs = append(ws2.docs, newDoc(wireFile{
		Path: "C:\\p\\x.py", Content: "\ufffd", LineEnding: lineEndingLF,
		DecodeReplacements: 1, Writable: true,
	}, "C:\\p", true))
	segs := strings.Join(ws2.StatusSegments(), " ")
	if !strings.Contains(segs, "read-only") || !strings.Contains(segs, string(roLossyDecode)) {
		t.Errorf("segments = %q, want the read-only reason", segs)
	}
}

// The column is a DISPLAY column: it is the number a compiler or a linter
// reports, and a rune index would disagree with both the moment a tab
// appears.
func TestStatusColumnIsADisplayColumn(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.docs = append(ws.docs, testDoc(t, "\tx"))
	ws.docs[0].moveTo(Pos{0, 1}, false) // after the tab
	segs := ws.StatusSegments()
	want := "1:" + itoa(tabStop+1)
	found := false
	for _, s := range segs {
		if s == want {
			found = true
		}
	}
	if !found {
		t.Errorf("segments %v do not contain %q", segs, want)
	}
}

func TestStatusSegmentsWithNothingOpen(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	segs := strings.Join(ws.StatusSegments(), " ")
	if !strings.Contains(segs, "no project") {
		t.Errorf("segments = %q", segs)
	}
	ws.projectID = "p1"
	segs = strings.Join(ws.StatusSegments(), " ")
	if !strings.Contains(segs, "no file open") {
		t.Errorf("segments = %q", segs)
	}
}

// Init and switchWorkspace's ConnectedMsg replay both call onConnected on
// every load after the handshake. Without the guard that is two
// ProjectResolve and two ListProjectFiles round trips per open.
func TestASecondConnectDoesNotResolveTwice(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.onConnected()
	ws.onConnected()
	ws.onConnected()
	if len(rec.sent) != 1 {
		t.Fatalf("sent %d resolves, want 1", len(rec.sent))
	}

	// Once the reply lands, a LATER reconnect must resolve again: a
	// backend restart has a new process and possibly a new database.
	reply(t, ws, queryResolveProject, protocol.EvProject, map[string]any{
		"project": map[string]any{"id": "p1", "root_path": "C:\\p"},
	})
	before := len(rec.sent)
	ws.onConnected()
	if len(rec.sent) != before+1 {
		t.Error("a reconnect after a completed resolve did not re-resolve")
	}
}

// outside_project is B32's own report of whether a project was found for
// THIS file. A file in a different project than the tree's still opens
// editable, which re-deriving the answer from the tree's root would get
// wrong in the direction that matters.
func TestAFileInAnotherProjectIsNotForcedReadOnly(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"

	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "D:\\other\\x.py", "content": "x", "line_ending": "lf",
			"trailing_newline": true, "writable": true, "outside_project": false,
		},
	})
	if ws.docs[0].readOnly {
		t.Errorf("read-only (%s) for a file the backend scoped to a project",
			ws.docs[0].roReason)
	}
}

func TestAFileReportedOutsideAnyProjectIsReadOnly(t *testing.T) {
	ws, _ := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"

	reply(t, ws, queryReadFile, protocol.EvProjectFile, map[string]any{
		"file": map[string]any{
			"path": "D:\\loose\\x.py", "content": "x", "line_ending": "lf",
			"trailing_newline": true, "writable": true, "outside_project": true,
		},
	})
	if !ws.docs[0].readOnly || ws.docs[0].roReason != roNoProject {
		t.Errorf("readOnly=%v reason=%q", ws.docs[0].readOnly, ws.docs[0].roReason)
	}
}

// The rects mouse.go hit-tests against are cleared each frame, so a click
// where the tree used to be does not focus a tree that is no longer drawn.
func TestHiddenPaneLeavesNoStaleHitTarget(t *testing.T) {
	ws := testWorkspace(t, "x", 120, 30)
	if ws.treeRect.IsZero() {
		t.Fatal("precondition: the tree was not drawn")
	}

	// Re-render with the right pane closed.
	l := layout.Compute(120, 30, layout.State{
		Active: layout.WorkspaceEditor, RightPaneOpen: false,
		HeaderAllowed: true, TerminalMode: layout.TermSmall, RightCards: ws.Cards(),
	})
	ws.View(l)

	if !ws.treeRect.IsZero() {
		t.Error("treeRect survived the pane being hidden")
	}
}
