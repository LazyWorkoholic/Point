package protocol

// Event type names, transcribed from backend\runtime\events.py.
//
// Go holds these as plain strings, not an enum: an unknown type must be
// logged and ignored, never crash, so there is no value in a closed set that
// a new backend release could fall outside of.

// Commands and queries — Go to Python (IncomingEventType).
const (
	CmdSendMessage   = "SendMessage"
	CmdCancelTask    = "CancelTask"
	CmdListTasks     = "ListTasks"
	CmdShutdown      = "Shutdown"
	CmdCreateSession = "CreateSession"
	CmdGetSession    = "GetSession"
	// CmdSessionMessages is B18's keyset scrollback query, and F18 is its
	// first and only Go consumer: {session_id, before|after, limit} ->
	// EvSessionMessageList, oldest-first within the slice, keyed on `seq`
	// and never on a row index or a timestamp. At most one of
	// before/after may be non-null; BOTH NULL MEANS THE TAIL, not the
	// start of history -- B18's own completed doc records a verification
	// script that walked almost nothing because it assumed otherwise.
	CmdSessionMessages       = "SessionMessages"
	CmdListSessions          = "ListSessions"
	CmdRenameSession         = "RenameSession"
	CmdArchiveSession        = "ArchiveSession"
	CmdUnarchiveSession      = "UnarchiveSession"
	CmdDeleteSession         = "DeleteSession"
	CmdUpdateSessionMetadata = "UpdateSessionMetadata"
	CmdUpdateSessionSettings = "UpdateSessionSettings"
	CmdListProviders         = "ListProviders"
	CmdListProviderModels    = "ListProviderModels"
	CmdSetProviderAPIKey     = "SetProviderApiKey"
	CmdSetSessionProvider    = "SetSessionProvider"
	CmdProjectInit           = "ProjectInit"
	CmdProjectResolve        = "ProjectResolve"
	CmdListProjectFiles      = "ListProjectFiles"
	CmdReadProjectFile       = "ReadProjectFile"
	// B32: the editor's save. The backend owns the write; config\state.go
	// stays the only file the FRONTEND writes. Declared here as the fixed
	// name F13 consumes -- no Go caller yet, by design, the same way the
	// lock commands above waited for theirs.
	CmdWriteProjectFile  = "WriteProjectFile"
	CmdAcquireFileLock   = "AcquireFileLock"
	CmdHeartbeatFileLock = "HeartbeatFileLock"
	CmdReleaseFileLock   = "ReleaseFileLock"
	CmdTakeOverFileLock  = "TakeOverFileLock"
	CmdCheckFileStale    = "CheckFileStale"
	CmdGetContextReport  = "GetContextReport"
	// Attachments (B12). B8 built the read half; these are the writes.
	// F8's @ picker is the first Go consumer.
	CmdAttachContext     = "AttachContext"
	CmdDetachContext     = "DetachContext"
	CmdSetAttachmentMode = "SetAttachmentMode"
	CmdListAttachments   = "ListAttachments"
	CmdResolveAttachment = "ResolveAttachment"
	// F8's @ picker (B23). ListAgents needs no session and no project;
	// ListDirectory is a plain, uncached, single-level listing, distinct
	// from ListProjectFiles' indexed/gitignore-aware/cached file tree.
	CmdListAgents    = "ListAgents"
	CmdListDirectory = "ListDirectory"
	// F10b's Behaviour tab and About tab (B26). SetBehaviourOverride's
	// payload is {"key": str, "value": any} to set, or {"key": str} with
	// no "value" field to clear -- the same "same key, presence of a field
	// changes the verb" shape DetachContext already uses, mirrored here
	// rather than invented twice.
	CmdSetBehaviourOverride = "SetBehaviourOverride"
	CmdGetDoctorReport      = "GetDoctorReport"
	// B15: the backend-side unload of one workspace, keyed by the
	// workspace id this side names ("chat", "editor"). It NEVER cancels a
	// task — unloading a view is a UI event and the task is in the
	// backend's lifetime, not the view's. The reply says which resources
	// a live task's existence protected.
	CmdWorkspaceUnload = "WorkspaceUnload"
	// CmdEventsSince is the replay rung of F2's ladder, read from the
	// backend's durable event log (B40, migration v8) — the command F2 was
	// specified against.
	//
	// B15 could not serve it: nothing persisted an OutgoingEvent, so it
	// shipped CmdSessionReplaySince instead, message-level replay keyed on
	// `messages.seq` with `mechanism` stated on the wire. **B40 deleted
	// that command rather than keeping it beside this one**, because two
	// replay commands with two different cursors is a second copy that can
	// disagree.
	//
	// The cursor here is `event_seq` (the backend's `events.session_seq`),
	// which is neither `messages.seq` nor the per-task stream `seq`. What
	// it buys over message-level replay is everything that is not a
	// message: the tool call that started AND completed while this client
	// was away, progress steps, usage updates, a FillProposal pushed after
	// a terminal event.
	//
	// The token stream is deliberately absent from the log, so a replayed
	// turn arrives as its structure and the prose comes from `messages`.
	// That is the same trade IsDroppable already makes live.
	//
	// Payload: {session, since_seq, limit}. since_seq 0 means everything
	// the log still holds — retention bounds it, so `has_more` is how a
	// caller learns its mark was swept past.
	CmdEventsSince = "EventsSince"
)

// Events — Python to Go (OutgoingEventType).
const (
	EvHello = "hello"
	EvPing  = "ping"

	EvTaskStarted        = "TaskStarted"
	EvTaskToken          = "TaskToken"
	EvTaskDone           = "TaskDone"
	EvTaskFailed         = "TaskFailed"
	EvTaskCancelled      = "TaskCancelled"
	EvTaskCancelAccepted = "task.cancel.accepted"
	EvTaskList           = "TaskList"

	EvError = "Error"

	EvSessionCreated = "SessionCreated"
	EvSession        = "Session"
	EvSessionList    = "SessionList"
	// EvSessionMessageList is CmdSessionMessages' reply: {session_id,
	// messages, has_more_before, has_more_after}. The two flags mean "does
	// history exist beyond THIS slice", not "beyond this batch".
	EvSessionMessageList     = "SessionMessageList"
	EvSessionRenamed         = "SessionRenamed"
	EvSessionArchived        = "SessionArchived"
	EvSessionUnarchived      = "SessionUnarchived"
	EvSessionDeleted         = "SessionDeleted"
	EvSessionMetadataUpdated = "SessionMetadataUpdated"
	EvSessionSettingsUpdated = "SessionSettingsUpdated"

	EvProviderList           = "ProviderList"
	EvProviderModelList      = "ProviderModelList"
	EvProviderAPIKeySet      = "ProviderApiKeySet"
	EvSessionProviderUpdated = "SessionProviderUpdated"

	EvTextDelta       = "TextDelta"
	EvThinkingDelta   = "ThinkingDelta"
	EvStatusChanged   = "StatusChanged"
	EvProgressUpdated = "ProgressUpdated"
	EvUsageUpdated    = "UsageUpdated"

	EvToolStarted   = "ToolStarted"
	EvToolCompleted = "ToolCompleted"

	EvProject         = "Project"
	EvProjectFileList = "ProjectFileList"
	EvProjectFile     = "ProjectFile"
	// B32: reply to WriteProjectFile. {ok:true,...} carries the NEW mtime
	// (F13's autosave rule 2); {ok:false,reason} carries one of seven
	// slugs. A path denial or an unresolvable project is an EvError, not a
	// slug.
	EvProjectFileWritten = "ProjectFileWritten"
	EvFileLockResult     = "FileLockResult"
	EvFileLockReleased   = "FileLockReleased"
	EvFileStaleResult    = "FileStaleResult"

	// EvFillProposal is B24's FillProposal, carrying B36's generation-time
	// (mtime, size): the Filler's fill-mode reply, already PARSED and
	// VALIDATED by the backend against the region the model was actually
	// shown. Nine fields, all of which travel together
	// (managers\fill_proposal.py's own docstring: no subset).
	//
	// Three things about it that a consumer must not get wrong, each one
	// recorded because the wrong version is the plausible one:
	//
	//   - It arrives AFTER the terminal TaskDone for its task, never
	//     instead of it, and only for `kind == "done"`. So a consumer that
	//     tears down its per-task state on TaskDone has already forgotten
	//     where to put this. B24: "pushed after the terminal TaskDone".
	//   - `seq` is null on BOTH envelopes and cannot order them; the
	//     guarantee is FIFO arrival from one producer on one queue.
	//   - There is NO event for a reply that did not parse. A plain-text
	//     refusal, or a model that confused end_line with its replacement's
	//     length (B24 measured that at 0/5 against the real local model),
	//     produces one backend WARNING and nothing on the wire. "No
	//     proposal arrived" is therefore the normal outcome of a fill, not
	//     an error state -- F15's Done-when 6 depends on that.
	EvFillProposal = "FillProposal"

	// EvContextReport is B20's ContextReport -- one wire type serving both
	// the GetContextReport reply (corr_id set) and the push after a turn's
	// assistant message persists (corr_id null, session_id always set).
	EvContextReport = "ContextReport"

	// Attachments (B12).
	EvAttachment           = "Attachment"
	EvAttachmentList       = "AttachmentList"
	EvAttachmentDetached   = "AttachmentDetached"
	EvAttachmentResolution = "AttachmentResolution"

	// F8's @ picker (B23): the replies to ListAgents / ListDirectory.
	EvAgentList     = "AgentList"
	EvDirectoryList = "DirectoryList"

	// F10b's Behaviour tab and About tab (B26). BehaviourOverrideSet
	// echoes back the FULL current override map on every change, not just
	// the key just touched -- matching ProviderApiKeySet's own precedent.
	// DoctorReport's payload is build_doctor_report()'s dict verbatim.
	EvBehaviourOverrideSet = "BehaviourOverrideSet"
	EvDoctorReport         = "DoctorReport"

	// B15: the reply to CmdWorkspaceUnload — a ReleaseReport. Three
	// lists, not a bool: `released` (dropped, each with a count),
	// `refused` (a RUNNING or QUEUED task holds it, with which tasks and
	// their states) and `absent` (something B15 named that the backend
	// does not have, e.g. symbol caches, which do not exist). Nothing in
	// it claims freed memory, and nothing in this client should either.
	EvWorkspaceUnloaded = "WorkspaceUnloaded"
	// EvSessionEvents is the reply to CmdEventsSince: {session, since_seq,
	// mechanism, replay, events, tasks, has_more}.
	//
	// It is routed as RouteReplay, never RouteSession, so a consumer can
	// never mistake a replayed ToolCompleted for a tool completing now.
	// Each entry in `events` is the envelope as it originally went out,
	// carrying its own `event_seq`; merge by that and by id rather than
	// appending, and a replay overlapping what is already rendered costs
	// nothing.
	EvSessionEvents = "SessionEvents"
	// EvShutdownComplete is the ONE terminal ack for CmdShutdown, and the
	// last envelope this client ever reads from that backend process.
	// Before B15 the backend acked nothing and did not exit.
	EvShutdownComplete = "shutdown.complete"
)

// Client-side pseudo-events. These never appear on the wire; the client
// synthesises them so a consumer handles a protocol failure through the same
// channel as everything else.
const (
	EvProtocolError = "client.protocol_error"
)

// IsTerminal reports whether this event ends a task.
//
// Exactly one of these arrives per task — never zero, never two — which is
// what lets the UI trust that a spinner always stops. Note that
// task.cancel.accepted is NOT terminal: it is the ack for a CancelTask
// request, and the sink still emits TaskCancelled afterwards.
func IsTerminal(eventType string) bool {
	switch eventType {
	case EvTaskDone, EvTaskFailed, EvTaskCancelled:
		return true
	}
	return false
}

// IsDroppable reports whether this event may be discarded under backpressure.
//
// Only token streams qualify. The accumulated string re-renders from the next
// token anyway, so a dropped one costs a frame of smoothness. Dropping
// anything else — a lifecycle event above all — costs a permanent spinner.
func IsDroppable(eventType string) bool {
	switch eventType {
	case EvTaskToken, EvTextDelta, EvThinkingDelta:
		return true
	}
	return false
}

// IsConnectionLevel reports whether this event is caused by no request.
//
// hello and ping carry a null corr_id by design, so they are not unroutable —
// they are addressed to the connection itself, and logging them as routing
// failures would bury the ones that matter.
func IsConnectionLevel(eventType string) bool {
	switch eventType {
	case EvHello, EvPing:
		return true
	}
	return false
}

// Capabilities is the hello payload's capability block.
//
// The root stores this so later episodes can grey out what the backend cannot
// do rather than offering it and failing. `Providers` lists what is
// REGISTERED, not what is reachable — Ollama registers without a key, so a
// keyless profile reports ["ollama"] whether or not the daemon is up.
type Capabilities struct {
	Providers     []string `json:"providers"`
	SchemaVersion int      `json:"schema_version"`
	Streaming     bool     `json:"streaming"`
	Cancel        bool     `json:"cancel"`
}

// Hello is the handshake payload.
type Hello struct {
	V            int          `json:"v"`
	Capabilities Capabilities `json:"capabilities"`
}
