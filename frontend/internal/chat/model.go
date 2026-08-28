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
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	TaskID     string          `json:"task_id"`
	Role       string          `json:"role"`
	Content    wireContent     `json:"content"`
	Timestamp  float64         `json:"timestamp"`
	Metadata   map[string]any  `json:"metadata"`
	Invocation *wireInvocation `json:"invocation"`
	Usage      wireUsage       `json:"usage"`
}

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
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	UpdatedAt float64       `json:"updated_at"`
	Messages  []wireMessage `json:"messages"`
}
