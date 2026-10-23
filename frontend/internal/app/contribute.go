package app

import tea "github.com/charmbracelet/bubbletea"

// F13's two additions to what Root asks of the ACTIVE workspace.
//
// Both follow the pattern FocusAware, OverlayProvider, DirtyBuffers and
// ContextExpandable already established: Root asks a question through a
// narrow interface and never reads a workspace's fields to answer it
// itself. A workspace that does not implement one is unaffected — the type
// assertion fails harmlessly and the previous behaviour stands.

// KeyCapturer is implemented by a workspace that OWNS a key the chord
// registry has already claimed.
//
// # Why this exists at all
//
// dispatchKey consults r.actions.ByChord BEFORE forwarding anything to the
// workspace, and an action whose When predicate is false is SWALLOWED with
// a status line rather than falling through (see dispatchKey). That order is
// right for every chord shipped so far — a palette or a quit must not be
// shadowed by whatever has focus — and it makes one key in F13's Build
// section unreachable: `tab` has been pane.focus.next's chord since F4, so
// Tab could never indent a selection, and giving that action a When
// predicate would only change the failure from "focuses the wrong thing" to
// "prints Focus Next Pane is not available right now".
//
// The alternative was to move pane.focus.next off `tab`. That was rejected:
// it is a shipped binding in the workspace F4 built it for, and the
// collision is not a mistake in the binding — it is two workspaces wanting
// one key, which is the thing a per-workspace claim expresses and a global
// table cannot.
//
// # The contract
//
// `key` is tea.KeyMsg.String(), the same string ByChord is keyed on.
// Claiming a key means the workspace receives it as an ordinary KeyMsg and
// the action bound to it does not run WHILE THAT WORKSPACE IS ACTIVE.
//
// A claim is narrow by obligation, not by convention: every key claimed is
// a key whose global binding silently stops working here, so a workspace
// claims only keys it genuinely consumes on every press, and says in its
// own doc exactly which ones. The Editor claims "tab" and "shift+tab" and
// nothing else.
//
// The leader key is deliberately NOT capturable: it is checked before this,
// so leader → anything keeps working in every workspace, which is what
// keeps the switcher and the palette reachable from inside an editor that
// has claimed keys.
type KeyCapturer interface {
	CapturesKey(key string) bool
}

// StatusContributor is implemented by a workspace that owns the left half
// of the status bar.
//
// LAYOUT.md gives the two workspaces two different status lines. Chat's is
// bindings on the left and state on the right; the Editor's is
// `task.py  12:4  python  CRLF  lock: you   ^P ^W ^E tree` — the file's own
// state first, and the bindings reduced to what that workspace needs. Those
// cannot both come from Root: the file, the cursor's line and column, the
// detected line ending and the lock holder are all facts only the workspace
// has.
//
// The returned parts go through fitHints, which drops WHOLE parts from the
// end when the line is narrow and appends "+N". That is deliberate and the
// reason this returns a slice rather than a formatted string: a status
// segment is a claim about the file in front of you, and half of one is a
// claim with the wrong number in it — the standing rule F04's Bug 12 set.
//
// While a workspace contributes segments, Root's own bindingsHint is NOT
// shown: the contributor owns the whole left half and includes whatever
// bindings it wants advertised, exactly as LAYOUT.md's Editor line does.
// A transient status message, a connection banner and the leader hint all
// still take priority over this — see statusBar's switch, which is ordered
// most-urgent-first and unchanged.
type StatusContributor interface {
	StatusSegments() []string
}

// WorkspaceStatusMsg is how a workspace says ONE transient thing.
//
// A workspace's own panes are the wrong place for a message about an
// action: the Editor's note ("save is not wired yet", "readonly.py is
// read-only", "re-indexing the project", "big.py has unsaved changes") was
// rendered only by its empty state, so with a file open -- which is
// whenever any of those can happen -- it was set and then never shown
// anywhere. Four reports of "no message at all" were one bug: there was no
// route from a workspace to the status bar.
//
// This is that route. It lands on r.status, which already outranks the
// StatusSegments above and already expires on its own, so a note behaves
// exactly like one Root set itself -- and a workspace needs no second
// message surface of its own to maintain.
//
// Deliberately NOT a field a workspace exposes for Root to poll, the way
// StatusSegments is: segments describe a state that is true for as long as
// it is true, and a note is an event. Polling an event means deciding when
// it stops being true, which is what expiringStatus already knows how to
// do.
type WorkspaceStatusMsg struct{ Text string }

// OpenWorkspaceOverlayMsg is B9: how a WORKSPACE asks Root to open an
// arbitrary overlay.
//
// # Why this did not already exist
//
// A workspace could reach the overlay slot three ways before F13b, and all
// three put the CONSTRUCTION in app: OpenAttachmentPickerMsg names a
// picker app builds, ContextExpandable hands back a card app wraps, and
// MouseOverlayTarget is a hit-test. None of them lets a workspace say
// "open THIS". The Editor's stale-save dialog needs exactly that: it is
// three buttons (Confirm is two by construction -- see confirm.go), its
// body names a file and an mtime only the Editor knows, and its [diff]
// button swaps in a view of a buffer app cannot see.
//
// # Why it is a separate type from openOverlayMsg
//
// openOverlayMsg (tray.go) is an internal convenience for code already
// inside this package that happens to be running inside an Overlay's own
// Update, where assigning r.overlay directly would break setOverlay's
// "the ONE place" rule. This one is a CONTRACT with another package, and
// the distinction is the same one WorkspaceStatusMsg draws against
// statusMsg: an exported message is a promise about what a workspace may
// send, and an unexported one is an implementation detail this package
// may change in any episode. Both land on OpenOverlay, which still
// applies the MinSize fit check -- a workspace cannot force an overlay
// onto a terminal too small for it, and gets the refusal on the status
// line exactly as an internal caller does.
type OpenWorkspaceOverlayMsg struct{ Overlay Overlay }

// LockHolder is implemented by a workspace holding something on the
// BACKEND that has to be given back before it stops receiving messages.
//
// The Editor's soft file locks (B7) are the only case: a lock row survives
// the workspace that took it, and the fallback is a 60-second staleness
// timeout -- so without this, unloading the Editor and reopening the same
// file would make Point wait out its own lock.
//
// Called from the same two places FlushDraft is, and for the same reason
// that interface exists: unloadWorkspace, and doQuit before tea.Quit. The
// release is written to the backend's stdin synchronously; main.go's
// deferred client.Close() then closes that stdin, which is the shutdown
// main.py drains in-flight handlers for, so a release sent here is
// processed rather than racing the exit.
type LockHolder interface {
	ReleaseLocks()
}

// BackgroundReplies is implemented by a workspace whose backend replies
// must still reach it while it is LOADED BUT NOT ACTIVE.
//
// Root routes protocol.EnvelopeMsg to r.current() and nowhere else, which
// is right for every workspace whose queries are driven by what is on
// screen: a reply nobody is looking at can wait for the next visit,
// because the query that produced it will be re-issued on the way back in
// (the Editor re-resolves and re-lists its tree from the ConnectedMsg
// switchWorkspace replays).
//
// A WRITE is the exception, and it is the one that bites. The Editor sets
// doc.saving when a CmdWriteProjectFile goes out and clears it ONLY from
// the EvProjectFileWritten reply. Autosave can start that write a moment
// before the user switches away -- a one-second debounce against a
// workspace switch is not a rare race, it is the ordinary one -- and the
// reply then lands while Chat is active and is dropped on the floor. The
// document is left saving forever: the status bar reads "saving…", the
// dirty marker never clears because nothing cleared it, and saveDoc's
// "a write is already in flight" guard refuses every later save, autosave
// and manual alike. The file becomes permanently un-saveable, and the
// only thing that put it in that state was changing workspace at the
// wrong instant.
//
// Recovering on the way back in is not an option that exists: once the
// reply is gone this side cannot tell whether the write landed, and both
// answers are wrong to assume. Re-sending with the old expected_mtime
// gets a `stale` refusal if it DID land; clearing the flag and trusting
// the buffer silently keeps a mtime that no longer matches the file. The
// reply is the only thing that knows, so the fix is to not throw it away.
//
// Declared as an interface rather than special-casing the Editor in Root
// for the reason every narrow interface in this file exists: the root asks
// "do you need this", never "are you the editor".
type BackgroundReplies interface {
	// WantsBackgroundReplies reports whether this workspace has work in
	// flight whose reply it cannot afford to miss. Asked per envelope, so
	// a workspace that answers false while idle costs nothing.
	WantsBackgroundReplies() bool
}

// AutosavePauser is implemented by a workspace that runs a timer which
// must not fire while a modal question about those same buffers is on
// screen.
//
// The Editor's dirty-close confirm (F13b's C3) already pauses its own
// autosave, and had to: gate_f13b step 5 found that a tick armed by the
// keystroke that made the file dirty lands while the user is still
// reading the dialog, writes the edits, and makes both buttons
// meaningless -- Close would discard a buffer already on disk.
//
// UI-13's unload confirm asks the SAME question about the SAME buffers
// and did not pause anything, because it is opened by Root rather than by
// the workspace and had no way to ask. This is that way.
//
// Pause/Resume are a pair and Root owns the pairing: every path that
// opens the dialog pauses, and every path that closes it WITHOUT
// unloading resumes. An unload needs no resume -- the workspace and its
// documents are gone.
type AutosavePauser interface {
	// PauseAutosave stops scheduled saves and retires any tick already in
	// flight. Idempotent.
	PauseAutosave()
	// ResumeAutosave restores the normal schedule and re-arms anything
	// still dirty. Idempotent, and never un-blocks a document stopped for
	// a reason of its own (read-only, held by another holder).
	//
	// Returns the re-armed timers. They are tea.Cmds and have to be run,
	// not discarded: a dirty document whose tick was retired by the pause
	// has no other edit coming, so dropping this is the difference
	// between "autosave resumes" and "autosave resumes after you type one
	// more character".
	ResumeAutosave() tea.Cmd
}

// ---------------------------------------------------------------------------
// F15: what a BACKGROUNDED workspace's commands may do
// ---------------------------------------------------------------------------

// BackgroundDelivery is one verdict on one message a workspace produced
// while it was loaded but not active.
//
// Three values rather than a bool, because "may this survive" and "who is
// it for" are different questions and both have to be answered: an overlay
// to open is Root's business, while a timer tick is the workspace's own and
// has to find its way back to a workspace that is not the active one -- which
// Root's ordinary fall-through (`r.current().Update(msg)`) would never do.
type BackgroundDelivery int

const (
	// DropWhileBackgrounded is the DEFAULT and the zero value: the
	// message is discarded exactly as it was before this seam existed.
	// A workspace that does not implement BackgroundCmdFilter at all gets
	// this for everything, so nothing changes for anyone who has not
	// reasoned about it.
	DropWhileBackgrounded BackgroundDelivery = iota
	// DeliverToRoot hands the message to Root's own Update, where a type
	// it handles itself (an overlay to open) is acted on.
	DeliverToRoot
	// DeliverToWorkspace routes the message back to the workspace that
	// produced it, whether or not that workspace is active by the time it
	// arrives.
	DeliverToWorkspace
)

// BackgroundCmdFilter is implemented by a workspace that produces commands
// while inactive which must not all be thrown away.
//
// # Why this exists
//
// deliverBackgroundReply DROPPED the returned tea.Cmd outright, and said so
// deliberately: "A background workspace may reconcile its own state; it may
// not take the status line." That reasoning is right for say() -- a
// WorkspaceStatusMsg carries no workspace id, so Root would print "saved
// main.go" over the status bar of whatever the user is actually looking at.
//
// It is wrong for two things F15 added, and the cost was two user-visible
// faults found by driving a real Root: the overlay command that OPENS a
// finished fill's diff card, and the timer that decides a fill produced no
// proposal at all. Both were produced while the Editor was inactive and
// both went in the bin, so a fill completed in the background left a frozen
// ghost, no card, and a ctrl+z that did nothing.
//
// # Default-DENY, and the allow-list lives with the workspace
//
// The verdict is the WORKSPACE's to give, for the reason every narrow
// interface in this file exists -- Root asks "what is this of yours", never
// "are you the editor" -- and because only the workspace can name its own
// unexported message types. Root's own rule is the simple half: no filter
// implemented, or any verdict other than the two Deliver values, means
// dropped.
//
// A workspace implementing this is making a claim about a SPECIFIC, listed
// set of its own messages. The obligation that goes with it is that the
// list is enumerated in one place with its reasons, so what has been
// reasoned about is visible and everything else is visibly not -- see
// editor\invoke.go's AllowBackgroundMsg.
type BackgroundCmdFilter interface {
	AllowBackgroundMsg(tea.Msg) BackgroundDelivery
}

// WorkspaceActivatedMsg tells a workspace it is now the one on screen.
//
// Sent by switchWorkspace on every switch IN, to an already-loaded
// workspace as well as a freshly loaded one -- which is what distinguishes
// it from the ProviderDefaultSetMsg/ConnectedMsg/WindowSizeMsg replays
// beside it, all three of which exist to catch a workspace up on what it
// missed by not existing yet.
//
// A workspace that does not handle it is unaffected. The ID is carried so a
// handler can assert it is the one being talked about rather than inferring
// it.
type WorkspaceActivatedMsg struct{ ID WorkspaceID }

// CloseWorkspaceOverlayMsg asks Root to close an overlay a workspace
// opened, IF that exact overlay is still the one on screen.
//
// The identity check is the point: between a workspace asking and Root
// acting, something else may have opened an overlay of its own, and
// closing whatever happens to be up would be a workspace reaching into a
// surface it does not own.
//
// # Why a workspace needs this at all
//
// An open overlay takes every key, esc included (dispatchKey's first
// branch). So an overlay that has finished its job and stays up does not
// merely look stale -- it holds the keyboard. F15's diff card finishes its
// job the instant an apply lands: it then reads "Applied", and the undo it
// promises in its own status line ("ctrl+z undoes it in one press") CANNOT
// REACH THE BUFFER while it is still on screen, because review mode binds
// no ctrl+z and the card swallows the key. F9b's Done-when #6 is only true
// through the UI if the card gets out of the way, and this is how it does.
type CloseWorkspaceOverlayMsg struct{ Overlay Overlay }

// WorkspaceDeactivatedMsg tells a workspace it is no longer the one on
// screen. ID is the workspace LEAVING -- itself.
//
// # Why both halves have to exist
//
// This one was missing, and its absence was not a gap a workspace could
// work around: a workspace is told nothing about the others, so without
// it "am I on screen" could only ever latch TRUE on the first switch in.
// The Editor gated its deferred diff card on that flag; the flag was
// never cleared, so the gate never engaged and a card that finished while
// the user was in Chat opened over Chat -- trapping every key they typed,
// in a workspace the card had nothing to do with. Worse, the card's own
// apply message then fell through Root to the ACTIVE workspace and was
// silently ignored, so pressing Enter on it did nothing at all.
//
// The lesson, written here because it is the sort that repeats: a
// workspace cannot infer a transition it is not told about. An "activated"
// message alone describes half a transition, and the half it leaves out is
// the one a workspace needs in order to stop doing things to the screen.
type WorkspaceDeactivatedMsg struct{ ID WorkspaceID }

// backgroundWorkspaceMsg is a DeliverToWorkspace message on its way back to
// the workspace that produced it.
//
// Addressed, because the alternative does not work: Root's fall-through
// hands an unrecognised message to `r.current()`, so a timer armed by an
// inactive workspace would be delivered to whichever workspace happened to
// be on screen when it fired -- which is how the settle tick was being lost
// even in the cases where the command itself survived.
type backgroundWorkspaceMsg struct {
	id  WorkspaceID
	msg tea.Msg
}
