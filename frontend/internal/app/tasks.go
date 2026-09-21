package app

// TaskRow mirrors one entry of ListTasks' payload (backend\runtime\runtime.py
// _handle_list_tasks), trimmed to what the right pane's Task card needs.
//
// Root fetches this, not any one workspace: F06-right-pane.md's Task card is
// explicit that the list is "Global list: tasks from every workspace,
// because the root owns it" — the same reason conn.inFlight already lives
// here rather than in chat.Workspace.
type TaskRow struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	Session    string  `json:"session"`
	Agent      string  `json:"agent"`
	CreatedAt  float64 `json:"created_at"`
	StartedAt  float64 `json:"started_at"`
	FinishedAt float64 `json:"finished_at"`
	ErrorCode  string  `json:"error_code"`
}

// Task state values, transcribed from backend\runtime\task.py's TaskState.
// Held as plain strings for the same reason protocol/types.go keeps event
// names as strings: a state this build does not recognise must still render
// as *something*.
const (
	TaskStateQueued    = "queued"
	TaskStateRunning   = "running"
	TaskStateCompleted = "completed"
	TaskStateFailed    = "failed"
	TaskStateCancelled = "cancelled"
)
