// Package chat is the Chat workspace: F3's transcript.
//
// It lives outside app because its Update method has to return app.Workspace
// — see chat.go's doc comment on the import direction this forces. Nothing
// else in this package reaches into app beyond that.
package chat

// wireMessage mirrors backend\managers\message.py's Message.to_dict()
// exactly. Field names are the wire's JSON names, not Go convention, so a
// diff against message.py stays a visual diff.
type wireMessage struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`

	// Seq is B18's monotonic per-session message number -- the ONLY cursor
	// F18 pages on and the only value its scroll anchor stores. Not a row
	// index (meaningless the instant anything is inserted above) and not
	// created_at (B18's migration broke created_at ties with rowid during
	// a one-time backfill and never again).
	//
	// B18 added this key to every Message.to_dict() output, including the
	// unbounded GetSession path, and noted that this struct's "mirrors
	// to_dict() exactly" claim went stale the moment it did -- Go ignores
	// unknown JSON fields, so nothing broke, but the comment was no longer
	// true. F18 is the episode that actually reads it, so it is also the
	// one that declares it and makes the comment true again.
	//
	// Zero means "this row did not come from the wire": the placeholder
	// reply applyTaskRows inserts for a task that produced no message has
	// no seq because it has no row, which is exactly why eviction and the
	// anchor both have to skip it rather than address it.
	Seq int `json:"seq"`

	TaskID     string          `json:"task_id"`
	Role       string          `json:"role"`
	Content    wireContent     `json:"content"`
	Timestamp  float64         `json:"timestamp"`
	Metadata   map[string]any  `json:"metadata"`
	Invocation *wireInvocation `json:"invocation"`
	Usage      wireUsage       `json:"usage"`

	// B12's three terminal-outcome columns (migration v5). They are real
	// columns, not metadata keys, precisely so a reloaded transcript can
	// re-render the footer a live one showed — see reload.go.
	//
	// Outcome is "" ONLY on rows written before v5. That is not the same
	// claim as "done": SessionRepository._row_to_message leaves it None
	// rather than defaulting, and reload.go treats the empty string as
	// "this predates the marker" and renders no footer, rather than
	// asserting an ending it does not know.
	Outcome     string `json:"outcome"`
	Interrupted bool   `json:"interrupted"`
	ErrorCode   string `json:"error_code"`
}

// The Outcome vocabulary, transcribed from managers\message.py's Outcome.
// Plain strings for the same reason the role names below are: an outcome
// this build does not recognise must render as *something*.
const (
	outcomeDone           = "done"
	outcomeCancelled      = "cancelled"
	outcomeFailed         = "failed"
	outcomeTimedOut       = "timed_out"
	outcomeBoundsExceeded = "bounds_exceeded"
)

// wireContent mirrors MessageContent.to_dict(). Data is left as a generic map
// — F3 only reads Text; a structured/tool_result reader is a later episode's
// job.
type wireContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	Data     map[string]any `json:"data"`
	Provider string         `json:"provider"`
}

type wireInvocation struct {
	Workspace  string `json:"workspace"`
	Capability string `json:"capability"`
	Agent      string `json:"agent"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Role names, transcribed from MessageRole. Kept as plain strings for the
// same reason protocol/types.go keeps event names as strings: an unknown role
// must render as *something*, never crash the workspace.
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
	roleError     = "error"
)

// wireSession mirrors Runtime._session_to_dict's output, trimmed to the
// fields this workspace reads.
type wireSession struct {
	ID string `json:"id"`
	// ProjectID is F8's: which project's tree the @ picker should fuzzy
	// over, and what ListDirectory/AttachContext scope this session's
	// files against. The wire has always sent it (runtime.py's
	// _session_to_dict); nothing decoded it until F8 needed it.
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	// Provider is the session's pinned provider name, empty when the
	// session inherits the global config. The backend has always sent it
	// (runtime.py's _session_to_dict); nothing decoded it until the
	// startup check in providers.go needed it.
	Provider string `json:"provider"`
	// Model is F6's: the Status card's "provider · model" line. The wire has
	// always sent it (runtime.py's _session_to_dict); nothing decoded it
	// until now.
	Model string `json:"model"`
	// Workspace is which workspace OWNS this session, and decoding it is
	// F15's fix for a real leak rather than completeness for its own
	// sake. The wire has always sent it (runtime.py's _session_to_dict,
	// on every SessionList row); nothing read it until the Editor started
	// creating a session of its own.
	//
	// Empty for every session this workspace has ever created -- chat's
	// CreateSession sends no `workspace` key, so the column is NULL --
	// which is why the filter in onSessionList is "not someone ELSE's"
	// rather than "mine". A rule of "only workspace == chat" would make
	// every pre-F15 session invisible.
	Workspace string        `json:"workspace"`
	UpdatedAt float64       `json:"updated_at"`
	Messages  []wireMessage `json:"messages"`

	// TotalMessageCount is B18's, and it is present ONLY on a bounded
	// (message_limit) GetSession reply -- runtime.py's _session_to_dict
	// adds the key only when session.total_message_count is not None, so
	// the unbounded path's payload shape is unchanged. F18 always asks
	// bounded, so zero here means an older backend or a reply that was
	// not a session load, and history.go reads it that way rather than as
	// "this session has no messages".
	TotalMessageCount int `json:"total_message_count"`
}

// wireContextReport mirrors backend\managers\context.py's
// ContextReport.to_dict() exactly, field for field, no subset. S13d reads
// this off both GetContextReport's reply and the ContextReport push — see
// protocol.go's onContextReport, the one decoder for both.
type wireContextReport struct {
	SessionID     string            `json:"session_id"`
	Provider      string            `json:"provider"`
	Model         string            `json:"model"`
	BudgetTokens  int               `json:"budget_tokens"`
	Sections      map[string]int    `json:"sections"`
	Counts        map[string]int    `json:"counts"`
	UsedTokens    int               `json:"used_tokens"`
	FreeTokens    int               `json:"free_tokens"`
	Included      []wireContextItem `json:"included"`
	NextEvictions []wireContextItem `json:"next_evictions"`
}

// wireContextItem serves both IncludedItem (Detail populated, Reason not)
// and EvictedItem (Reason populated, Detail not) from context.py -- the two
// dataclasses share every field but their last, so one Go shape decodes
// both rather than two near-identical types.
type wireContextItem struct {
	Section string `json:"section"`
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Tokens  int    `json:"tokens"`
	Detail  string `json:"detail"`
	Reason  string `json:"reason"`
}

// contextSectionOrder mirrors context.py's SECTION_ORDER exactly -- the
// fixed key order every ContextReport's Sections/Counts maps are guaranteed
// to carry, zero-valued entries included, which is what lets the
// composition card sum them without a special case for "nothing there yet".
var contextSectionOrder = []string{
	"system", "attachments", "memory", "summary", "history", "task_state",
}

// contextSectionLabel is the mockup's own wording (F06-right-pane.md) for
// each SECTION_ORDER key -- six entries, not worth a shared type.
var contextSectionLabel = map[string]string{
	"system":      "system prompt",
	"attachments": "attachments",
	"memory":      "memory",
	"summary":     "summary",
	"history":     "history",
	"task_state":  "task state",
}
