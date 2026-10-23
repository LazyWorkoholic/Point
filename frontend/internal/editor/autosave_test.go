package editor

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/config"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// Autosave: the debounce, the five rules, and C5's per-document timer.

// autosaveWorkspace is two open, writable, lock-held documents with a
// short delay so a test can wait out a real tick without parking the
// suite -- the same substitution Root.StatusLinger makes for the status
// expiry.
func autosaveWorkspace(t *testing.T) (*Workspace, *recorder, *doc, *doc) {
	t.Helper()
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.holder = "aryan@id"
	ws.autosaveMode = config.AutosaveAfterDelay
	ws.autosaveDelay = 10 * time.Millisecond

	mk := func(name string) *doc {
		d := newDoc(wireFile{
			Path: "C:\\p\\" + name, Content: "a\nb", Mtime: 100, Size: 4,
			LineEnding: lineEndingLF, TrailingNewline: true, Writable: true,
		}, "C:\\p", true)
		d.lock = lockHeld
		ws.docs = append(ws.docs, d)
		return d
	}
	return ws, rec, mk("a.py"), mk("b.py")
}

// runTick executes a tea.Cmd that is expected to be a debounce tick and
// feeds the resulting message back through Update, which is what a real
// run does. Returns whatever Update returned.
//
// It EXECUTES the command rather than synthesising the message by hand.
// F13a's round 5 shipped a paste test that passed against unfixed code
// precisely because it built statusExpireMsg itself and so never
// exercised the scheduling it was meant to be testing.
func runTick(t *testing.T, ws *Workspace, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run — nothing was scheduled")
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, out := ws.Update(msg)
	return out
}

func writesIn(rec *recorder) []sentQuery {
	var out []sentQuery
	for _, q := range rec.sent {
		if q.cmd == protocol.CmdWriteProjectFile {
			out = append(out, q)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// C5 -- THE per-document timer test
// ---------------------------------------------------------------------------

// Editing file B must not delay or cancel file A's already-pending
// autosave.
//
// This is the test C5 asks for by name, and it is here because the bug
// it guards against is invisible until two files are dirty at once. A
// single workspace-level generation counter passes every single-file
// test in this package and fails this one: B's edit would bump the
// shared counter, A's pending tick would find a mismatch, and A's save
// would silently never happen.
//
// The class is a standing risk in this codebase rather than a one-off.
// Three shipped instances: the idle-notice latch (one flag standing in
// for per-workspace state), theme propagation (one workspace updated
// standing in for every loaded one), and status expiry (one assignment
// standing in for a per-message timer).
func TestEditingOneFileDoesNotDelayAnothersAutosave(t *testing.T) {
	ws, rec, a, b := autosaveWorkspace(t)

	// A is edited and its timer armed.
	a.insertRune('x')
	aTick := ws.armAutosave(a)
	if aTick == nil {
		t.Fatal("A's autosave was not armed")
	}

	// B is then edited, which arms B's own timer.
	b.insertRune('y')
	bTick := ws.armAutosave(b)
	if bTick == nil {
		t.Fatal("B's autosave was not armed")
	}

	// A's tick, which was armed FIRST, still fires.
	runTick(t, ws, aTick)
	writes := writesIn(rec)
	if len(writes) != 1 {
		t.Fatalf("A's pending autosave produced %d writes, want 1 — "+
			"editing B cancelled it", len(writes))
	}
	if writes[0].payload["path"] != a.path {
		t.Fatalf("the write was for %v, not A", writes[0].payload["path"])
	}

	// And B's still fires afterwards, on its own schedule.
	runTick(t, ws, bTick)
	writes = writesIn(rec)
	if len(writes) != 2 {
		t.Fatalf("B's autosave produced %d writes in total, want 2", len(writes))
	}
	if writes[1].payload["path"] != b.path {
		t.Errorf("the second write was for %v, not B", writes[1].payload["path"])
	}
}

// The negative half, stated as its own test: a SECOND edit to the SAME
// file does supersede its own earlier tick. That is the debounce
// working, and it is what the test above must not be confused with.
func TestASecondEditToTheSameFileSupersedesItsOwnTimer(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	first := ws.armAutosave(a)
	a.insertRune('y')
	second := ws.armAutosave(a)

	runTick(t, ws, first)
	if n := len(writesIn(rec)); n != 0 {
		t.Fatalf("the superseded tick produced %d writes, want 0", n)
	}
	runTick(t, ws, second)
	if n := len(writesIn(rec)); n != 1 {
		t.Fatalf("the current tick produced %d writes, want 1", n)
	}
}

// Done-when 10, restated as the property it actually is: N edits inside
// the window produce ONE save, not N.
func TestContinuousTypingProducesExactlyOneSave(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	var last tea.Cmd
	for i := 0; i < 30; i++ {
		a.insertRune('x')
		last = ws.armAutosave(a)
	}
	// Every earlier tick is stale; only the last one matches.
	runTick(t, ws, last)

	if n := len(writesIn(rec)); n != 1 {
		t.Fatalf("30 keystrokes produced %d writes, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// The debounce is armed from the BUFFER VERSION, not from keystrokes
// ---------------------------------------------------------------------------

// Rule 4, both halves, and neither needed a special case: an applied
// region and an undo both splice, so both move Version(), so both arm
// the timer through the same probe every key goes through.
func TestUndoAndApplyRegionBothArmTheAutosave(t *testing.T) {
	cases := []struct {
		name string
		act  func(*doc)
	}{
		{"ApplyRegion", func(d *doc) { d.ApplyRegion(Pos{}, Pos{}, "zzz") }},
		{"Undo after it", func(d *doc) {
			d.ApplyRegion(Pos{}, Pos{}, "zzz")
			d.Undo()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws, _, a, _ := autosaveWorkspace(t)
			probe := ws.probeActive()
			c.act(a)
			if cmd := ws.autosaveAfter(probe, "test"); cmd == nil {
				t.Error("no autosave armed — rule 4 would be unmet")
			}
		})
	}
}

// A key that does NOT change the buffer does not arm anything. Without
// this, scrolling or moving the caret would reschedule a save forever
// and a file edited once would never be written while the user was
// reading it.
func TestAKeyThatDoesNotEditArmsNothing(t *testing.T) {
	ws, _, _, _ := autosaveWorkspace(t)
	probe := ws.probeActive()
	ws.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if cmd := ws.autosaveAfter(probe, "test"); cmd != nil {
		t.Error("a caret move armed an autosave")
	}
}

// A tab switch inside one Update must not be read as an edit. The probe
// matches on PATH for this reason: comparing w.docs[w.active] before and
// after would compare two different documents' versions.
func TestATabSwitchIsNotMistakenForAnEdit(t *testing.T) {
	ws, _, _, b := autosaveWorkspace(t)
	b.insertRune('q') // B's version is now ahead of A's
	ws.active = 0

	probe := ws.probeActive() // A
	ws.cycleTab(+1)           // now B is active
	if cmd := ws.autosaveAfter(probe, "test"); cmd != nil {
		t.Error("a tab switch armed an autosave for an unedited file")
	}
}

// ---------------------------------------------------------------------------
// The modes
// ---------------------------------------------------------------------------

func TestAutosaveOffArmsNothing(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	ws.autosaveMode = config.AutosaveOff

	a.insertRune('x')
	if cmd := ws.armAutosave(a); cmd != nil {
		t.Error("autosave = off still armed a timer")
	}
}

// ...but it still bumps the generation, so flipping the mode cannot
// leave a stale tick able to fire against a later edit.
func TestAutosaveOffStillAdvancesTheGeneration(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	ws.autosaveMode = config.AutosaveOff
	before := a.autosaveGen
	ws.armAutosave(a)
	if a.autosaveGen == before {
		t.Error("the generation did not advance with autosave off")
	}
}

// on_focus_change saves the document being left, not the one arrived at.
func TestOnFocusChangeSavesTheDocumentBeingLeft(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)
	ws.autosaveMode = config.AutosaveOnFocusChange
	a.insertRune('x')
	ws.active = 0

	ws.cycleTab(+1)

	writes := writesIn(rec)
	if len(writes) != 1 {
		t.Fatalf("a tab switch produced %d writes, want 1", len(writes))
	}
	if writes[0].payload["path"] != a.path {
		t.Errorf("saved %v, want the document being left", writes[0].payload["path"])
	}
}

// after_delay does NOT also save on a tab switch. The two modes are
// alternatives in the config; a mode that did both would make
// "after_delay" mean "after a delay, or sooner if you switch tabs".
func TestAfterDelayDoesNotAlsoSaveOnFocusChange(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)
	a.insertRune('x')
	ws.active = 0

	ws.cycleTab(+1)

	if n := len(writesIn(rec)); n != 0 {
		t.Errorf("after_delay saved %d times on a tab switch", n)
	}
}

// ---------------------------------------------------------------------------
// Done-when 11 -- no refusal storm
// ---------------------------------------------------------------------------

// A locked file's autosave fires once, learns it is locked, and stops.
// The second edit must not re-arm it, or the user gets one refusal per
// second for as long as the other holder keeps the lock.
func TestAutosaveStopsAfterALockedRefusalAndDoesNotReArm(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	runTick(t, ws, ws.armAutosave(a))
	if n := len(writesIn(rec)); n != 1 {
		t.Fatalf("the first autosave produced %d writes", n)
	}

	replyFor(t, ws, queryWriteFile, a.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": false, "reason": "locked",
			"holder": "sam@other", "age_seconds": 3.0})

	if !a.autosaveBlocked {
		t.Fatal("autosave was not stopped by the locked refusal")
	}
	a.insertRune('y')
	if cmd := ws.armAutosave(a); cmd != nil {
		t.Fatal("autosave re-armed after a locked refusal — one refusal per second")
	}
	if n := len(writesIn(rec)); n != 1 {
		t.Errorf("%d writes in total, want 1", n)
	}
}

// A successful write clears the block, so a transient refusal does not
// disable autosave for the rest of the session.
func TestASuccessfulWriteUnblocksAutosave(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	a.autosaveBlocked = true
	a.insertRune('x')

	ws.saveDoc(a, saveManual, false)
	replyFor(t, ws, queryWriteFile, a.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": true, "path": a.path, "mtime": 200.0, "size": 5,
			"line_ending": lineEndingLF, "bom": false, "trailing_newline": true})

	if a.autosaveBlocked {
		t.Error("still blocked after a successful write")
	}
}

// ---------------------------------------------------------------------------
// Done-when 12 -- Point does not trip its own staleness check
// ---------------------------------------------------------------------------

// Two autosaves in a row, with the first one's reply applied in between.
// Without rule 2 the second carries the mtime from the original READ,
// the file on disk has the one Point just wrote, and the backend
// refuses `stale` against Point's own change.
func TestTwoAutosavesInARowDoNotTripTheStalenessCheck(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)

	a.insertRune('x')
	runTick(t, ws, ws.armAutosave(a))
	replyFor(t, ws, queryWriteFile, a.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": true, "path": a.path, "mtime": 555.0, "size": 5,
			"line_ending": lineEndingLF, "bom": false, "trailing_newline": true})

	a.insertRune('y')
	runTick(t, ws, ws.armAutosave(a))

	writes := writesIn(rec)
	if len(writes) != 2 {
		t.Fatalf("%d writes, want 2", len(writes))
	}
	if got := writes[1].payload["expected_mtime"]; got != 555.0 {
		t.Errorf("the second autosave sent expected_mtime %v, want 555 — "+
			"Point would refuse its own write", got)
	}
}

// ---------------------------------------------------------------------------
// Rule 5 and the dirty marker
// ---------------------------------------------------------------------------

// Rule 5: autosave does not clear undo history. Stated as a rule
// because it is the obvious thing to get wrong if saving were ever
// implemented as "reload what was written".
func TestAutosaveDoesNotClearUndoHistory(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	a.insertRune('x')
	runTick(t, ws, ws.armAutosave(a))
	replyFor(t, ws, queryWriteFile, a.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": true, "path": a.path, "mtime": 200.0, "size": 5,
			"line_ending": lineEndingLF, "bom": false, "trailing_newline": true})

	before := a.buf.Text()
	if !a.Undo() {
		t.Fatal("undo had nothing to do after an autosave")
	}
	if a.buf.Text() == before {
		t.Error("undo changed nothing")
	}
}

// The dirty marker appears between the edit and the save, so the status
// bar never lies about the current state.
func TestTheDirtyMarkerIsShownBetweenTheEditAndTheSave(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	a.insertRune('x')

	if !a.dirty {
		t.Fatal("not dirty immediately after an edit")
	}
	if !hasSegment(ws, "• modified") {
		t.Error("the status bar does not show the dirty marker before the save")
	}

	runTick(t, ws, ws.armAutosave(a))
	replyFor(t, ws, queryWriteFile, a.path, protocol.EvProjectFileWritten,
		map[string]any{"ok": true, "path": a.path, "mtime": 200.0, "size": 5,
			"line_ending": lineEndingLF, "bom": false, "trailing_newline": true})

	if a.dirty {
		t.Error("still dirty after the save landed")
	}
}

// A file whose autosave is stopped says so, because the whole promise
// of autosave is that unsaved work does not stay unsaved.
func TestAStoppedAutosaveIsVisibleInTheStatusBar(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	a.insertRune('x')
	a.autosaveBlocked = true

	if !hasSegment(ws, "autosave stopped") {
		t.Errorf("segments = %q", ws.StatusSegments())
	}
}

func hasSegment(ws *Workspace, want string) bool {
	for _, s := range ws.StatusSegments() {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// An unrecognised autosave value falls back to after_delay rather than
// to off: a misspelt key must not silently stop saving your work.
func TestAnUnrecognisedAutosaveValueKeepsTheDefault(t *testing.T) {
	for _, v := range []string{"", "yes", "ON", "after-delay"} {
		c := config.Config{Editor: config.Editor{Autosave: v}}
		if got := c.AutosaveMode(); got != config.AutosaveAfterDelay {
			t.Errorf("Autosave %q -> %q, want %q", v, got, config.AutosaveAfterDelay)
		}
	}
	c := config.Config{Editor: config.Editor{Autosave: "off"}}
	if got := c.AutosaveMode(); got != config.AutosaveOff {
		t.Errorf("an exact \"off\" -> %q", got)
	}
}

// A non-positive delay falls back too, rather than producing a timer
// that fires on the next tick -- which would be a save per keystroke.
func TestANonPositiveDelayFallsBackToTheDefault(t *testing.T) {
	for _, ms := range []int{0, -1} {
		c := config.Config{Editor: config.Editor{AutosaveDelayMs: ms}}
		if got := c.AutosaveDelay(); got != config.DefaultAutosaveDelayMs*time.Millisecond {
			t.Errorf("delay %d -> %v", ms, got)
		}
	}
}

// A zero Options gets the same behaviour the default config gives, not
// a silently different one.
func TestAZeroOptionsGetsTheDefaultAutosaveBehaviour(t *testing.T) {
	ws := New(Options{Theme: testTheme()})
	if ws.autosaveMode != config.AutosaveAfterDelay {
		t.Errorf("mode = %q", ws.autosaveMode)
	}
	if ws.autosaveDelay != config.DefaultAutosaveDelayMs*time.Millisecond {
		t.Errorf("delay = %v", ws.autosaveDelay)
	}
}

// The workspace satisfies app.LockHolder, or nothing would ever release
// a lock on unload or quit.
func TestWorkspaceIsALockHolder(t *testing.T) {
	var ws any = New(Options{Theme: testTheme()})
	if _, ok := ws.(app.LockHolder); !ok {
		t.Error("not an app.LockHolder — locks would survive every unload")
	}
}
