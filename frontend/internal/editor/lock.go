package editor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The soft file lock lifecycle (B7, F13's "open / save / held" table).
//
//	open   acquire a soft lock; heartbeat while open; record mtime
//	save   refused with `locked` if a DIFFERENT holder's beat is fresh
//	held   open read-only, status bar names the holder and the age
//	close  release
//
// # Why soft
//
// Nothing here stops anyone editing anything. The lock is a claim in a
// table that the backend consults in write_file's step 4, and a stale
// one (no beat for 60s) can be taken over by anyone who asks. F13's own
// words: "this protects you from your own second terminal as much as
// from an SSH guest". A hard lock would need a crash to be impossible,
// and it is not.

// heartbeatEvery is how often a held lock is refreshed.
//
// B7's LOCK_STALE_AFTER_S is 60. Twenty seconds puts THREE beats inside
// that window, so one dropped beat -- a slow backend, a GC pause, a
// reconnect -- does not make Point look dead to itself and invite its
// own lock to be taken over. Decision B7 of F13a's pre-code set, settled
// before any of this was written.
const heartbeatEvery = 20 * time.Second

// lockStaleAfter mirrors B7's LOCK_STALE_AFTER_S.
//
// A SECOND copy of a backend constant, which this package otherwise
// refuses to keep (see save.go on too_large's limit_kb). The difference
// is what it is used for: nothing here enforces staleness -- the backend
// does, in acquire_lock and write_file -- this only decides whether to
// OFFER take-over in the status bar. A copy that drifts makes the offer
// appear slightly early or late; it cannot make a lock be stolen that
// the backend would have protected. The backend is still the only
// enforcer, and take_over_lock steals unconditionally when asked, so the
// offer is advisory by construction.
const lockStaleAfter = 60 * time.Second

// acquireLock asks for one document's lock, if it can have one.
//
// Called once per open. A document with no project never asks: it cannot
// take a lock (`_lock_key` resolves against a project root), which is
// precisely why "no .point\ above it" means read-only rather than
// writable-but-unlockable.
func (w *Workspace) acquireLock(d *doc) tea.Cmd {
	if !w.lockable(d) {
		return nil
	}
	d.lock = lockPending
	w.send(protocol.CmdAcquireFileLock, queryAcquireLock, w.lockPayload(d))
	return w.startHeartbeat()
}

// lockable reports whether this document can hold a lock at all.
//
// A read-only document still takes one when the reason is the FILE's own
// state rather than the absence of a project: a file whose read-only
// attribute is set, or one that did not decode cleanly, is still a file
// this editor has open and still worth telling a second Point about. A
// document with no project cannot, and neither can one with no holder
// key to take it under.
func (w *Workspace) lockable(d *doc) bool {
	return d != nil && d.path != "" && w.holder != "" && d.roReason != roNoProject
}

func (w *Workspace) lockPayload(d *doc) map[string]any {
	p := map[string]any{"path": d.path, "holder": w.holder}
	if w.projectID != "" && relativeTo(w.projectRoot, d.path) != "" {
		p["project_id"] = w.projectID
	}
	return p
}

// wireLock mirrors LockResult.to_dict(): `{ok, holder, age_seconds}`.
// It names no path, which is why every lock reply is correlated back to
// its document through pendingReq rather than through its payload.
type wireLock struct {
	OK         bool    `json:"ok"`
	Holder     string  `json:"holder"`
	AgeSeconds float64 `json:"age_seconds"`
}

// onLockResult handles AcquireFileLock and TakeOverFileLock.
//
// take_over_lock steals unconditionally and so always reports ok; acquire
// reports false when a DIFFERENT holder's beat is still fresh.
func (w *Workspace) onLockResult(req pendingReq, env protocol.Envelope) tea.Cmd {
	var p wireLock
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a FileLockResult reply", "err", err)
		return nil
	}
	d := w.docByPath(req.path)
	if d == nil {
		// Closed since the acquire went out. The lock row exists and
		// this process owns it, so it has to be given back rather than
		// left to go stale -- closeTab released a lock that had not
		// arrived yet.
		if p.OK && req.path != "" {
			w.releaseByPath(req.path)
		}
		return nil
	}

	if p.OK {
		d.lock = lockHeld
		d.lockedBy, d.lockedAge = "", 0
		if req.kind == queryRetakeLock {
			// A take-over is the one lock transition worth announcing:
			// the user asked for it explicitly, and the file goes from
			// refusing saves to accepting them.
			d.autosaveBlocked = false
			return w.say("took over the lock on " + d.name())
		}
		return nil
	}

	// Refused: someone else holds it and their beat is fresh.
	d.lock = lockHeldByOther
	d.lockedBy = p.Holder
	d.lockedAge = p.AgeSeconds
	if req.kind == queryRetakeLock {
		// The backend just re-checked the age against its own clock and
		// said no. This is the authoritative answer, unlike the one the
		// frontend used to give from d.lockedAge.
		return w.say(d.name() + " is " + d.lockLabel() +
			" — still live, so the lock was not taken")
	}
	return w.say(d.name() + " is " + d.lockLabel() + " — opened read-only")
}

// onHeartbeatResult is the beat's reply.
//
// A beat that comes back refused means the lock was TAKEN -- someone
// else called take_over on it while this editor held it. That is a real
// state change and the document has to learn about it, or the next save
// is refused by a `locked` slug the status bar never warned about.
//
// heartbeat_lock also raises LockNotHeldError (an Error envelope, handled
// in onError) when the row is gone entirely, which is what a release from
// another process looks like.
func (w *Workspace) onHeartbeatResult(req pendingReq, env protocol.Envelope) tea.Cmd {
	var p wireLock
	if err := env.UnmarshalPayload(&p); err != nil {
		return nil
	}
	d := w.docByPath(req.path)
	if d == nil || p.OK {
		return nil
	}
	d.lock = lockHeldByOther
	d.lockedBy = p.Holder
	d.lockedAge = p.AgeSeconds
	d.autosaveBlocked = true
	return w.say(d.name() + " was taken over — now " + d.lockLabel())
}

// ---------------------------------------------------------------------------
// The heartbeat
// ---------------------------------------------------------------------------

type heartbeatTickMsg struct{}

// startHeartbeat arms the single 20s ticker, once.
//
// ONE ticker for every held lock, not one per document, and that is not
// the per-entity-timer trap C5 names. The distinction is what the timer
// MEANS: an autosave timer is a per-file deadline that a per-file event
// resets, so sharing one would let file B cancel file A's save. A
// heartbeat is a liveness signal with no per-file deadline at all -- the
// tick refreshes every held lock, so no file's beat depends on any other
// file's activity. Sharing it is correct here for the same reason
// sharing it would be wrong there.
func (w *Workspace) startHeartbeat() tea.Cmd {
	if w.heartbeatOn {
		return nil
	}
	w.heartbeatOn = true
	return tea.Tick(heartbeatEvery, func(time.Time) tea.Msg { return heartbeatTickMsg{} })
}

// onHeartbeatTick beats every lock this editor holds, and stops when
// there are none left.
//
// Re-armed from here rather than unconditionally in Update: a workspace
// with no open files should not keep a ticker alive, and an unloaded one
// stops receiving the message anyway (Root routes only to loaded
// workspaces), which is what makes this safe to leave running across an
// unload rather than something that has to be torn down.
func (w *Workspace) onHeartbeatTick() tea.Cmd {
	held := 0
	for _, d := range w.docs {
		if d.lock != lockHeld {
			continue
		}
		held++
		w.send(protocol.CmdHeartbeatFileLock, queryHeartbeatLock, w.lockPayload(d))
	}
	if held == 0 {
		w.heartbeatOn = false
		return nil
	}
	return tea.Tick(heartbeatEvery, func(time.Time) tea.Msg { return heartbeatTickMsg{} })
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

// releaseLock gives one document's lock back.
//
// The local state is cleared FIRST and the reply is ignored, because the
// same call has to work on the way out of the process, where no reply
// will ever be read -- see ReleaseLocks.
func (w *Workspace) releaseLock(d *doc) {
	if d == nil || d.lock != lockHeld {
		return
	}
	d.lock = lockNone
	w.releaseByPath(d.path)
}

func (w *Workspace) releaseByPath(path string) {
	if path == "" || w.holder == "" {
		return
	}
	p := map[string]any{"path": path, "holder": w.holder}
	if w.projectID != "" && relativeTo(w.projectRoot, path) != "" {
		p["project_id"] = w.projectID
	}
	w.send(protocol.CmdReleaseFileLock, queryReleaseLock, p)
}

// ReleaseLocks is app.LockHolder: give everything back before this
// workspace stops receiving messages.
//
// Called synchronously from unloadWorkspace and from doQuit, in both
// cases after the point where a tea.Cmd could still be delivered -- which
// is why this writes to the wire directly and returns nothing. The bytes
// land in the backend's stdin; main.go's deferred client.Close() then
// closes that stdin, which is the shutdown main.py drains in-flight
// handlers for, so a release sent here is processed rather than racing
// the exit.
//
// If it is ever missed anyway -- a kill, a panic, a backend that died
// first -- B7's 60-second staleness is the backstop and acquire_lock
// takes a stale row over without asking. That is the designed failure
// mode, not a gap: it is the same mechanism that recovers a lock left by
// a crashed Point.
func (w *Workspace) ReleaseLocks() {
	for _, d := range w.docs {
		w.releaseLock(d)
	}
	w.heartbeatOn = false
}

// takeOverActive is f6: take a lock whose holder has gone quiet.
//
// # Two bugs the gate found here, and what replaced them
//
//  1. IT WAS BOUND TO ctrl+shift+l, WHICH DOES NOT EXIST. On Windows,
//     bubbletea's console-record reader produces the same control
//     character for Ctrl+L and Ctrl+Shift+L (0x0C), so both arrive as
//     `ctrl+l` -- and ctrl+l is app.redraw's shipped chord, which
//     dispatchKey consults BEFORE the workspace. So this function was
//     unreachable by its own advertised binding, and Done-when 7's
//     take-over half had never actually run. Exactly the class of finding
//     F13a recorded for ctrl+tab, which collapses onto `tab` for the same
//     reason. f6 is free in the registry, needs no KeyCapturer claim, and
//     is reachable -- the same reasoning that put re-index on f5.
//
//  2. IT JUDGED STALENESS FROM A SNAPSHOT. d.lockedAge is whatever the
//     refusal reported when it arrived and is never refreshed, so the
//     comparison against lockStaleAfter was against a number that could
//     be minutes out of date in either direction. The frontend had to
//     make that judgement at all only because take_over_lock steals
//     unconditionally.
//
// So the action now sends ACQUIRE, not take-over. acquire_lock is
// self-validating: it steals a row older than B7's 60s window and
// refuses a live one, which is precisely this feature's semantics with
// the backend as the single judge. Nothing here reads lockedAge.
//
// lockStaleAfter survives for the status bar's OFFER only, which is
// advisory by construction -- see its own comment.
func (w *Workspace) takeOverActive() tea.Cmd {
	if len(w.docs) == 0 {
		return nil
	}
	d := w.docs[w.active]
	if d.lock != lockHeldByOther {
		return w.say("no other holder has " + d.name())
	}
	if !w.lockable(d) {
		return nil
	}
	d.lock = lockPending
	w.send(protocol.CmdAcquireFileLock, queryRetakeLock, w.lockPayload(d))
	return w.startHeartbeat()
}
