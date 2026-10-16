package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/app"
	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The four bugs gate_f13b found on its first run, each with the
// assertion that would have caught it.

// ---------------------------------------------------------------------------
// 1 -- the take-over binding did not exist
// ---------------------------------------------------------------------------

// ctrl+shift+l is not a key on Windows. bubbletea's console-record
// reader produces the same control character for Ctrl+L and
// Ctrl+Shift+L, so both arrive as `ctrl+l` -- which is app.redraw's
// shipped chord, consulted by dispatchKey BEFORE the workspace. The
// take-over was unreachable by its advertised binding, and Done-when 7's
// take-over half had never run.
//
// Asserted as a PROPERTY of the key map rather than as a string: any
// binding this workspace advertises must be one the workspace can
// actually receive.
func TestTheTakeOverBindingIsReachableAndNotAChord(t *testing.T) {
	// The chords dispatchKey consults first. An editor binding that
	// collides with one of these is swallowed with a status line and
	// never arrives -- unless CapturesKey claims it, and this episode's
	// claim list is deliberately F13a's two keys.
	chords := []string{"ctrl+c", "ctrl+e", "ctrl+l", "ctrl+p", "ctrl+q",
		"ctrl+r", "esc", "f1", "tab"}

	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 90

	// The binding reaches takeOverActive and produces a lock request.
	rec.sent = nil
	ws.handleKey(tea.KeyMsg{Type: tea.KeyF6})
	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdAcquireFileLock {
		t.Fatalf("f6 sent %+v, want one AcquireFileLock", rec.sent)
	}

	// And it is not one of the keys that would never arrive.
	key := tea.KeyMsg{Type: tea.KeyF6}.String()
	for _, c := range chords {
		if key == c {
			t.Fatalf("the take-over is bound to %q, which is a chord -- "+
				"dispatchKey swallows it and the workspace never sees it", key)
		}
	}
	if ws.CapturesKey(key) {
		t.Errorf("%q is claimed by CapturesKey; it does not need to be", key)
	}
}

// Nothing advertises a key the terminal cannot deliver.
// ctrl+shift+<letter> is the shape that caught this one and ctrl+tab is
// the shape that caught F13a's, so both are checked against every
// message and segment this package can produce for a locked file.
func TestNoMessageAdvertisesAnUndeliverableKey(t *testing.T) {
	ws, _ := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 90

	surfaces := []string{
		strings.Join(ws.StatusSegments(), " | "),
		lockSegment(d),
		saidBy(ws.saveDoc(d, saveManual, false)),
	}
	for _, s := range surfaces {
		for _, bad := range []string{"ctrl+shift+", "ctrl+tab"} {
			if strings.Contains(s, bad) {
				t.Errorf("%q advertises %q, which Windows cannot deliver", s, bad)
			}
		}
	}
}

// The ACTION re-validates with the backend instead of judging staleness
// from d.lockedAge, which is a snapshot taken when the refusal arrived
// and never refreshed. acquire_lock steals a stale row and refuses a
// live one, so the backend is the only judge.
func TestTakeOverAsksTheBackendRatherThanTrustingItsOwnSnapshot(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	// A snapshot that says LIVE. The old code refused locally on this
	// alone; the lock may well have gone stale since it was taken.
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 2
	rec.sent = nil

	ws.takeOverActive()

	if len(rec.sent) != 1 {
		t.Fatalf("sent %+v, want one request -- a stale snapshot must not "+
			"be the thing that decides", rec.sent)
	}
	if rec.sent[0].cmd != protocol.CmdAcquireFileLock {
		t.Errorf("sent %q; AcquireFileLock is the self-validating one "+
			"(TakeOverFileLock steals unconditionally)", rec.sent[0].cmd)
	}
	if d.lock != lockPending {
		t.Errorf("lock = %v while the request is in flight", d.lock)
	}

	// The backend's refusal is what reports "still live".
	said := saidBy(replyFor(t, ws, queryRetakeLock, d.path, protocol.EvFileLockResult,
		map[string]any{"ok": false, "holder": "sam@other", "age_seconds": 5.0}))
	if !strings.Contains(said, "still live") {
		t.Errorf("said %q", said)
	}
	if d.lock != lockHeldByOther {
		t.Errorf("lock = %v after a refused retake", d.lock)
	}
}

// ---------------------------------------------------------------------------
// 2 -- the dirty-close Confirm did not pause autosave
// ---------------------------------------------------------------------------

// C3 says Close DISCARDS and there is no implicit save. The dialog is
// open for longer than the 1s debounce, so a tick armed by the keystroke
// that made the file dirty would land mid-dialog and write the edits --
// making Close discard a buffer already on disk, and Cancel identical to
// it. Both buttons meaningless, which is the whole reason C3 exists.
func TestOpeningTheDirtyCloseConfirmPausesThatDocumentsAutosave(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)
	a.insertRune('x')
	// The tick the keystroke armed, already in flight.
	pending := ws.armAutosave(a)
	if pending == nil {
		t.Fatal("precondition: no autosave was armed by the edit")
	}

	_, cmd := ws.closeTab(0)
	if !openedConfirm(t, cmd) {
		t.Fatal("no Confirm was opened")
	}
	if !a.autosaveBlocked {
		t.Error("autosave was not paused while the dialog is open")
	}

	// The IN-FLIGHT tick must be retired too. The flag alone is not
	// enough: armAutosave is consulted when a timer is SET, and this one
	// was set before the dialog opened.
	runTick(t, ws, pending)
	if n := len(writesIn(rec)); n != 0 {
		t.Fatalf("an autosave fired while the confirm was open (%d writes) -- "+
			"Close would discard edits that are already on disk", n)
	}
	if a.saving {
		t.Error("a write went out while the confirm was open")
	}
}

// Cancel resumes it. The user chose to keep the edits, so they must be
// written on the ordinary schedule rather than sitting unsaved until
// some later keystroke happens to arm a new timer.
func TestCancellingTheCloseResumesAutosave(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)
	a.insertRune('x')
	ws.closeTab(0)
	if !a.autosaveBlocked {
		t.Fatal("precondition: autosave was not paused")
	}

	resumed := ws.onCloseCancelled(closeCancelledMsg{path: a.path})
	if a.autosaveBlocked {
		t.Error("still paused after Cancel")
	}
	if resumed == nil {
		t.Fatal("Cancel did not re-arm the autosave")
	}
	runTick(t, ws, resumed)
	if n := len(writesIn(rec)); n != 1 {
		t.Errorf("%d writes after Cancel, want 1 -- the kept edits were never saved", n)
	}
}

// Confirm carries the cancel command at all. Before F13b all three
// decline paths returned (nil, nil), so there was no signal to resume
// on -- which is why this fix needed a change to the shared widget and
// not just to the editor.
func TestConfirmReportsItsDeclinePaths(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyEnter}, // No is focused by default
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
	} {
		fired := false
		c := app.NewConfirm("t", "b", "Yes", "No", nil).
			OnCancel(func() tea.Msg { fired = true; return nil })
		_, cmd := c.Update(k)
		if cmd == nil {
			t.Errorf("%v returned no command", k)
			continue
		}
		cmd()
		if !fired {
			t.Errorf("%v did not run onCancel", k)
		}
	}
}

// ---------------------------------------------------------------------------
// 3 -- the stale dialog was two rows shorter than its own output
// ---------------------------------------------------------------------------

// MinSize was a flat 9, and for a plain Overlay overlayRect treats
// MinSize as the ACTUAL rendered size -- so the box got 7 content rows
// while View emitted 9. The bracketed button row and the key hint were
// rows 8 and 9: both cut. What was left on screen was the body's own
// plain-text option list, which is exactly the reported "three options
// render as plain unbracketed text with no focus indicator".
//
// Asserted against View's real output rather than against the constant,
// so adding a body line cannot reintroduce it.
func TestTheStaleDialogIsTallAndWideEnoughForItsOwnOutput(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	dlg := newStaleDialog(d, 123)

	w, h := dlg.PreferredSize(200, 60)
	rows := strings.Split(strings.TrimRight(dlg.View(ws.theme, w-2, h-2), "\n"), "\n")

	if len(rows) > h-2 {
		t.Fatalf("View emits %d rows but PreferredSize offers %d content rows -- "+
			"the last %d would be cut", len(rows), h-2, len(rows)-(h-2))
	}

	// The two rows that were being lost, by content.
	all := stripANSI(strings.Join(rows, "\n"))
	if !strings.Contains(all, "[ reload ]") {
		t.Error("the focused button is not bracketed in the rendered output")
	}
	if !strings.Contains(all, "esc") {
		t.Error("the key-hint footer is missing from the rendered output")
	}

	// And no row is truncated at the preferred width.
	for i, r := range rows {
		if got := visibleWidth(stripANSI(r)); got > w-2 {
			t.Errorf("row %d is %d cells wide, over the %d offered", i, got, w-2)
		}
	}
}

// Focus is visible without pressing anything: exactly one label is
// bracketed, and it moves with the arrow keys.
func TestTheStaleDialogShowsWhichButtonHasFocus(t *testing.T) {
	ws, _, d := savableWorkspace(t)
	dlg := newStaleDialog(d, 123)
	w, h := dlg.PreferredSize(200, 60)

	render := func() string { return stripANSI(dlg.View(ws.theme, w-2, h-2)) }

	first := render()
	if n := strings.Count(first, "["); n != 1 {
		t.Fatalf("%d bracketed labels, want exactly 1 -- focus must be visible "+
			"without pressing a key", n)
	}
	if !strings.Contains(first, "[ reload ]") {
		t.Error("reload is not focused by default; it is the only choice that " +
			"cannot destroy work belonging to someone not in the room")
	}

	dlg.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !strings.Contains(render(), "[ overwrite ]") {
		t.Error("the focus marker did not move with the arrow key")
	}
}

// MinSize stays LAYOUT.md's floor. Raising it would mean terminals that
// can open the dialog today stop being able to -- and a dialog that
// warns about losing work must not vanish instead of clipping.
func TestTheStaleDialogsMinSizeStaysTheRefusalFloor(t *testing.T) {
	_, _, d := savableWorkspace(t)
	dlg := newStaleDialog(d, 123)
	_, minH := dlg.MinSize()
	_, prefH := dlg.PreferredSize(200, 60)
	if minH != confirmFloorRows {
		t.Errorf("MinSize height = %d, want LAYOUT.md's %d floor", minH, confirmFloorRows)
	}
	if prefH <= minH {
		t.Errorf("PreferredSize height %d is not above the floor %d", prefH, minH)
	}
}

// ---------------------------------------------------------------------------
// 4 -- "autosave stopped" never reached the status bar
// ---------------------------------------------------------------------------

// armAutosave returned at !canSave() BEFORE saveDoc -- the only place
// that set autosaveBlocked -- so a file held by another holder was
// correctly never written and the segment gated on that flag never
// appeared. The file stayed safe and nothing said why.
// A LOCKED file is the only reachable case, and that is worth stating
// rather than leaving the test looking narrow.
//
// canSave() is false for three reasons -- read-only, held by another
// holder, no path -- but only the lock one can coexist with a dirty
// buffer. A read-only document refuses every edit at the source
// (edit.go guards insertRune, newline, backspace, deleteForward and
// insertText), so it can never become dirty and there is nothing to
// autosave or to announce; a document with no path does not exist yet.
// A first version of this test asserted the read-only case too and
// failed -- correctly, because the setup it needed is unreachable.
func TestAutosaveStoppedReachesTheStatusBarForALockedFile(t *testing.T) {
	ws, rec, a, _ := autosaveWorkspace(t)
	a.lock, a.lockedBy, a.lockedAge = lockHeldByOther, "sam@other", 3

	// One ordinary edit, through the funnel Update uses.
	probe := ws.probeActive()
	a.insertRune('x')
	ws.autosaveAfter(probe)

	if !a.dirty {
		t.Fatal("precondition: a lock-held buffer is still editable and must be dirty")
	}
	if !a.autosaveBlocked {
		t.Fatal("autosaveBlocked was not set -- the segment is gated on it")
	}
	if !hasSegment(ws, "autosave stopped") {
		t.Errorf("segments = %q, want 'autosave stopped'", ws.StatusSegments())
	}
	if n := len(writesIn(rec)); n != 0 {
		t.Errorf("%d writes to a file held by another holder", n)
	}
}

// The unreachable half, pinned so it stays unreachable: a read-only
// document cannot be made dirty, which is why it needs no announcement.
func TestAReadOnlyDocumentCannotBecomeDirtyAtAll(t *testing.T) {
	_, _, _, _ = autosaveWorkspace(t)
	d := newDoc(wireFile{
		Path: "C:\\p\\ro.py", Content: "a", LineEnding: lineEndingLF, Writable: false,
	}, "C:\\p", true)
	if !d.readOnly {
		t.Fatal("precondition: the fixture is not read-only")
	}
	before := d.buf.Version()
	d.insertRune('x')
	d.newline()
	d.backspace()
	d.deleteForward()
	d.insertText("zzz", editPaste)
	if d.buf.Version() != before || d.dirty {
		t.Error("a read-only document was edited; the autosave-stopped segment " +
			"would then be reachable for it and this test should be replaced")
	}
}

// ...and it clears again when the reason goes away, so the segment is a
// claim about now rather than a latch -- the failure mode F13a's
// dirty-notice latch already shipped once.
func TestAutosaveStoppedClearsWhenTheLockComesBack(t *testing.T) {
	ws, _, a, _ := autosaveWorkspace(t)
	a.lock, a.lockedBy, a.lockedAge = lockHeldByOther, "sam@other", 90
	probe := ws.probeActive()
	a.insertRune('x')
	ws.autosaveAfter(probe)
	if !hasSegment(ws, "autosave stopped") {
		t.Fatal("precondition: the segment is not showing")
	}

	ws.takeOverActive()
	replyFor(t, ws, queryRetakeLock, a.path, protocol.EvFileLockResult,
		map[string]any{"ok": true})

	if hasSegment(ws, "autosave stopped") {
		t.Errorf("still stopped after the lock came back: %q", ws.StatusSegments())
	}
}
