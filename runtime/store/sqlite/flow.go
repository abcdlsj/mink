package sqlite

type TaskStatus string

const (
	TaskQueued  TaskStatus = "queued"
	TaskRunning TaskStatus = "running"
	TaskWaiting TaskStatus = "waiting"
	TaskFailed  TaskStatus = "failed"
	TaskDone    TaskStatus = "done"
)

func (s TaskStatus) Resumable() bool {
	return s == TaskQueued || s == TaskWaiting
}

type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunInterrupted RunStatus = "interrupted"
)

type RunState struct {
	TaskID string
	RunID  string
}

type TaskInfo struct {
	ID           string
	Kind         string
	Title        string
	Status       TaskStatus
	Priority     int
	SourceKind   string
	SourceID     string
	ThreadID     string
	ParentTaskID string
	CurrentRunID string
	CreatedAt    string
	UpdatedAt    string
	ClosedAt     string
}

type TaskListOptions struct {
	Status       TaskStatus
	SourceKind   string
	SourceID     string
	ParentTaskID string
	Limit        int
}

type sourceKey struct {
	Kind     string
	ID       string
	ThreadID string
}
