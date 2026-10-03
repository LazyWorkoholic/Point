package protocol

// Event type names, transcribed from backend\runtime\events.py.
//
// Go holds these as plain strings, not an enum: an unknown type must be
// logged and ignored, never crash, so there is no value in a closed set that
// a new backend release could fall outside of.

// Commands and queries — Go to Python (IncomingEventType).
const (
	CmdSendMessage           = "SendMessage"
	CmdCancelTask            = "CancelTask"
	CmdListTasks             = "ListTasks"
	CmdShutdown              = "Shutdown"
	CmdCreateSession         = "CreateSession"
	CmdGetSession            = "GetSession"
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
	CmdAcquireFileLock       = "AcquireFileLock"
	CmdHeartbeatFileLock     = "HeartbeatFileLock"
	CmdReleaseFileLock       = "ReleaseFileLock"
	CmdTakeOverFileLock      = "TakeOverFileLock"
	CmdCheckFileStale        = "CheckFileStale"
	CmdGetContextReport      = "GetContextReport"
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

	EvSessionCreated         = "SessionCreated"
	EvSession                = "Session"
	EvSessionList            = "SessionList"
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

	EvProject          = "Project"
	EvProjectFileList  = "ProjectFileList"
	EvProjectFile      = "ProjectFile"
	EvFileLockResult   = "FileLockResult"
	EvFileLockReleased = "FileLockReleased"
	EvFileStaleResult  = "FileStaleResult"

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
