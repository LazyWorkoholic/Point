package editor

import (
	"strings"
	"testing"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The lock lifecycle: acquire on open, heartbeat, release, take over.

func lockWorkspace(t *testing.T) (*Workspace, *recorder) {
	t.Helper()
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	ws.holder = "aryan@7f3c1d2e4b5a69780000000000000000"
	return ws, rec
}

// openFile drives a real ReadProjectFile reply through onFile, which is
// what makes the acquire happen -- rather than calling acquireLock
// directly, which would test the function and not the wiring.
func openFile(t *testing.T, ws *Workspace, name string, f wireFile) *doc {
	t.Helper()
	if f.Path == "" {
		f.Path = "C:\\p\\" + name
	}
	if f.LineEnding == "" {
		f.LineEnding = lineEndingLF
	}
	replyFor(t, ws, queryReadFile, "", protocol.EvProjectFile,
		map[string]any{"file": f})
	for _, d := range ws.docs {
		if d.path == f.Path {
			return d
		}
	}
	t.Fatalf("%s did not open", name)
	return nil
}

// ---------------------------------------------------------------------------
// Acquire
// ---------------------------------------------------------------------------

func TestOpeningAFileAcquiresItsLock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})

	var acq *sentQuery
	for i := range rec.sent {
		if rec.sent[i].cmd == protocol.CmdAcquireFileLock {
			acq = &rec.sent[i]
		}
	}
	if acq == nil {
		t.Fatalf("no AcquireFileLock; sent %+v", rec.sent)
	}
	if acq.payload["holder"] != ws.holder {
		t.Errorf("holder = %v, want the whole composed string", acq.payload["holder"])
	}
	if acq.payload["path"] != d.path {
		t.Errorf("path = %v", acq.payload["path"])
	}
	if d.lock != lockPending {
		t.Errorf("lock = %v, want lockPending while the acquire is in flight", d.lock)
	}
}

// A file with no project above it cannot take a lock at all -- that is
// WHY "no .point\" means read-only rather than writable-but-unlockable.
func TestAFileWithNoProjectNeverAsksForALock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "out.py", wireFile{
		Path: "C:\\elsewhere\\out.py", OutsideProject: true, Writable: true,
	})

	if d.roReason != roNoProject {
		t.Fatalf("roReason = %q, want %q", d.roReason, roNoProject)
	}
	for _, q := range rec.sent {
		if q.cmd == protocol.CmdAcquireFileLock {
			t.Fatal("asked for a lock on a file with no project")
		}
	}
	if seg := lockSegment(d); seg != "" {
		t.Errorf("lock segment = %q; a file that cannot be locked has no lock state", seg)
	}
}

// With no holder key, nothing is locked -- the same reason nothing is
// saved. An empty holder is refused by the backend outright.
func TestWithNoHolderNothingIsLocked(t *testing.T) {
	ws, rec := newWorkspaceWithRecorder()
	ws.projectID, ws.projectRoot = "p1", "C:\\p"
	openFile(t, ws, "x.py", wireFile{Writable: true})

	for _, q := range rec.sent {
		if q.cmd == protocol.CmdAcquireFileLock {
			t.Fatal("asked for a lock with no holder key")
		}
	}
}

// A read-only FILE still takes a lock: the reason is the file's own
// state, not the absence of a project, and a second Point still wants
// to know this one has it open.
func TestAReadOnlyFileStillTakesALock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	openFile(t, ws, "ro.py", wireFile{Writable: false})

	found := false
	for _, q := range rec.sent {
		if q.cmd == protocol.CmdAcquireFileLock {
			found = true
		}
	}
	if !found {
		t.Error("a read-only file did not take a lock")
	}
}

// ---------------------------------------------------------------------------
// Done-when 7 -- a second holder
// ---------------------------------------------------------------------------

func TestASecondHolderOpensReadOnlyWithTheHolderNamed(t *testing.T) {
	ws, _ := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})

	said := saidBy(replyFor(t, ws, queryAcquireLock, d.path, protocol.EvFileLockResult,
		map[string]any{"ok": false, "holder": "sam@other-id", "age_seconds": 12.0}))

	if d.lock != lockHeldByOther {
		t.Fatalf("lock = %v", d.lock)
	}
	if !strings.Contains(said, "held by sam") || !strings.Contains(said, "12s ago") {
		t.Errorf("said %q, want the holder and the age", said)
	}
	// F13's spec writes the segment as "held by aryan (12s ago)".
	seg := lockSegment(d)
	if !strings.Contains(seg, "held by sam (") || !strings.Contains(seg, "12s ago") {
		t.Errorf("lock segment = %q", seg)
	}
	// The display form splits on the LAST '@' and shows the first eight
	// of the id -- A2's rule, because a Windows username may contain '@'.
	if !strings.Contains(seg, "sam (other-id)") {
		t.Errorf("segment = %q, want the username and a short id", seg)
	}
}

// Take-over is refused while the other holder's beat is FRESH.
// take_over_lock steals unconditionally by design, so the staleness
// judgement belongs to whoever offers the button -- offering it on a
// live lock makes it a way to yank a file from a working colleague.
func TestTakeOverIsRefusedWhileTheOtherHoldersBeatIsFresh(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 12
	rec.sent = nil

	said := saidBy(ws.takeOverActive())

	for _, q := range rec.sent {
		if q.cmd == protocol.CmdTakeOverFileLock {
			t.Fatal("took over a live lock")
		}
	}
	if !strings.Contains(said, "still live") {
		t.Errorf("said %q", said)
	}
	if strings.Contains(lockSegment(d), "takes over") {
		t.Error("the status bar offered take-over on a live lock")
	}
}

// Once it goes stale the offer appears and the take-over goes out.
func TestTakeOverWorksOnceTheLockIsStale(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock, d.lockedBy, d.lockedAge = lockHeldByOther, "sam@other", 61
	d.autosaveBlocked = true
	rec.sent = nil

	if !strings.Contains(lockSegment(d), "ctrl+shift+l takes over") {
		t.Errorf("stale lock segment = %q, want the offer", lockSegment(d))
	}

	ws.takeOverActive()
	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdTakeOverFileLock {
		t.Fatalf("sent %+v", rec.sent)
	}

	said := saidBy(replyFor(t, ws, queryTakeOverLock, d.path, protocol.EvFileLockResult,
		map[string]any{"ok": true}))

	if d.lock != lockHeld {
		t.Errorf("lock = %v after a successful take-over", d.lock)
	}
	if d.autosaveBlocked {
		t.Error("autosave still blocked after taking the lock back")
	}
	if !strings.Contains(said, "took over") {
		t.Errorf("said %q", said)
	}
}

// ---------------------------------------------------------------------------
// Heartbeat
// ---------------------------------------------------------------------------

// Three beats inside B7's 60s window, so one dropped beat does not make
// Point look dead to itself (decision B7).
func TestTheHeartbeatFitsThreeBeatsInsideTheStalenessWindow(t *testing.T) {
	if heartbeatEvery*3 > lockStaleAfter {
		t.Fatalf("heartbeat %v x3 exceeds the %v staleness window", heartbeatEvery, lockStaleAfter)
	}
	if heartbeatEvery*4 <= lockStaleAfter {
		t.Errorf("heartbeat %v is more often than the three beats B7 settled on", heartbeatEvery)
	}
}

// One tick beats EVERY held lock. That is not C5's per-entity trap: a
// heartbeat has no per-file deadline for another file's activity to
// reset, so sharing the ticker is correct here in a way it would not be
// for autosave.
func TestOneTickBeatsEveryHeldLock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	a := openFile(t, ws, "a.py", wireFile{Writable: true})
	b := openFile(t, ws, "b.py", wireFile{Writable: true})
	a.lock, b.lock = lockHeld, lockHeld
	rec.sent = nil

	ws.onHeartbeatTick()

	beats := map[string]bool{}
	for _, q := range rec.sent {
		if q.cmd == protocol.CmdHeartbeatFileLock {
			beats[q.payload["path"].(string)] = true
		}
	}
	if !beats[a.path] || !beats[b.path] {
		t.Fatalf("beat %v, want both files", beats)
	}
}

// With nothing held the ticker stops rather than running forever.
func TestTheHeartbeatStopsWhenNothingIsHeld(t *testing.T) {
	ws, _ := lockWorkspace(t)
	ws.heartbeatOn = true

	if cmd := ws.onHeartbeatTick(); cmd != nil {
		t.Error("re-armed the heartbeat with no locks held")
	}
	if ws.heartbeatOn {
		t.Error("heartbeatOn still set")
	}
}

// A REFUSED beat means the lock was taken while this editor held it.
// Without noticing, the next save is refused by a `locked` slug the
// status bar never warned about.
func TestARefusedHeartbeatMeansTheLockWasTaken(t *testing.T) {
	ws, _ := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock = lockHeld

	said := saidBy(replyFor(t, ws, queryHeartbeatLock, d.path, protocol.EvFileLockResult,
		map[string]any{"ok": false, "holder": "sam@other", "age_seconds": 2.0}))

	if d.lock != lockHeldByOther {
		t.Fatalf("lock = %v after a refused beat", d.lock)
	}
	if !d.autosaveBlocked {
		t.Error("autosave not stopped after losing the lock")
	}
	if !strings.Contains(said, "taken over") {
		t.Errorf("said %q", said)
	}
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

func TestClosingATabReleasesItsLock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	d.lock = lockHeld
	rec.sent = nil

	ws.dropTab(0)

	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdReleaseFileLock {
		t.Fatalf("sent %+v, want one ReleaseFileLock", rec.sent)
	}
	if rec.sent[0].payload["holder"] != ws.holder {
		t.Errorf("released under %v", rec.sent[0].payload["holder"])
	}
}

// app.LockHolder: everything goes back before the workspace stops
// receiving messages.
func TestReleaseLocksGivesBackEveryHeldLock(t *testing.T) {
	ws, rec := lockWorkspace(t)
	a := openFile(t, ws, "a.py", wireFile{Writable: true})
	b := openFile(t, ws, "b.py", wireFile{Writable: true})
	c := openFile(t, ws, "c.py", wireFile{Writable: true})
	a.lock, b.lock = lockHeld, lockHeld
	c.lock = lockHeldByOther // never ours; must not be released
	rec.sent = nil

	ws.ReleaseLocks()

	released := map[string]bool{}
	for _, q := range rec.sent {
		if q.cmd == protocol.CmdReleaseFileLock {
			released[q.payload["path"].(string)] = true
		}
	}
	if !released[a.path] || !released[b.path] {
		t.Errorf("released %v, want both held locks", released)
	}
	if released[c.path] {
		t.Error("released a lock belonging to another holder")
	}
}

// A lock whose acquire lands after the tab closed still has to be given
// back, or this process waits out its own row on the next open.
func TestALockThatArrivesAfterTheTabClosedIsReleased(t *testing.T) {
	ws, rec := lockWorkspace(t)
	d := openFile(t, ws, "x.py", wireFile{Writable: true})
	path := d.path
	ws.dropTab(0)
	rec.sent = nil

	replyFor(t, ws, queryAcquireLock, path, protocol.EvFileLockResult,
		map[string]any{"ok": true})

	if len(rec.sent) != 1 || rec.sent[0].cmd != protocol.CmdReleaseFileLock {
		t.Fatalf("sent %+v, want the orphaned lock released", rec.sent)
	}
}

// ---------------------------------------------------------------------------
// The status segment
// ---------------------------------------------------------------------------

func TestTheLockSegmentSaysWhichStateItIsIn(t *testing.T) {
	cases := []struct {
		name string
		d    *doc
		want string
	}{
		{"ours", &doc{lock: lockHeld}, "lock: you"},
		{"in flight", &doc{lock: lockPending}, "lock: …"},
		{"cannot be locked", &doc{lock: lockNone}, ""},
		{"someone else's, live",
			&doc{lock: lockHeldByOther, lockedBy: "sam@abcdefghij", lockedAge: 12},
			"held by sam (abcdefgh) (12s ago)"},
		{"someone else's, stale",
			&doc{lock: lockHeldByOther, lockedBy: "sam@abcdefghij", lockedAge: 240},
			"held by sam (abcdefgh) (4m ago) · ctrl+shift+l takes over"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lockSegment(c.d); got != c.want {
				t.Errorf("lockSegment = %q, want %q", got, c.want)
			}
		})
	}
}
