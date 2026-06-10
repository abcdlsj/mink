package task

import "time"

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusFinished    Status = "finished"
	StatusFailed      Status = "failed"
	StatusCanceled    Status = "canceled"
	StatusEmptyOutput Status = "empty_output"
)

type Lifecycle string

const (
	LifecycleActive   Lifecycle = "active"
	LifecycleArchived Lifecycle = "archived"
)

func (s Status) Lifecycle() Lifecycle {
	switch s {
	case StatusQueued, StatusRunning, Status("todo"), Status("in_progress"), Status("in-review"), Status("in_review"):
		return LifecycleActive
	case StatusFinished, StatusFailed, StatusCanceled, StatusEmptyOutput,
		Status("done"), Status("closed"), Status("cancelled"), Status("no_output"), Status("error"):
		return LifecycleArchived
	default:
		return LifecycleArchived
	}
}

func (s Status) Active() bool {
	return s.Lifecycle() == LifecycleActive
}

func (s Status) Archived() bool {
	return s.Lifecycle() == LifecycleArchived
}

func (s Status) Terminal() bool {
	return s.Archived()
}

type KeyStepKind string

const (
	KindRead    KeyStepKind = "read"
	KindWrite   KeyStepKind = "write"
	KindRun     KeyStepKind = "run"
	KindSubtask KeyStepKind = "subtask"
	KindSummary KeyStepKind = "summary"
	KindError   KeyStepKind = "error"
)

const (
	MaxTitleLen   = 80
	MaxOutcomeLen = 200
	MaxKeySteps   = 8
)

type Task struct {
	ID               string    `json:"id"`
	SpaceID          string    `json:"space_id"`
	TriggerMessageID string    `json:"trigger_message_id"`
	InitiatorID      string    `json:"initiator_id"`
	WorkerID         string    `json:"worker_id"`
	Title            string    `json:"title"`
	Status           Status    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ResultMessageID  string    `json:"result_message_id,omitempty"`
	Outcome          string    `json:"outcome,omitempty"`
	Source           string    `json:"source,omitempty"`
	State            TaskState `json:"state,omitempty"`
}

type Run struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Status    Status    `json:"status"`
	KeySteps  []KeyStep `json:"key_steps,omitempty"`
	State     TaskState `json:"state,omitempty"`
}

type KeyStep struct {
	Kind  KeyStepKind `json:"kind"`
	Title string      `json:"title"`
	At    time.Time   `json:"at"`
	OK    bool        `json:"ok"`
}

type TaskState struct {
	Goal       string   `json:"goal,omitempty"`
	Todo       []string `json:"todo,omitempty"`
	Checkpoint string   `json:"checkpoint,omitempty"`
	Artifacts  []string `json:"artifacts,omitempty"`
	Blockers   []string `json:"blockers,omitempty"`
	RelatedIDs []string `json:"related_ids,omitempty"`
}
