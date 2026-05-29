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
}

type Run struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Status    Status    `json:"status"`
	KeySteps  []KeyStep `json:"key_steps,omitempty"`
}

type KeyStep struct {
	Kind  KeyStepKind `json:"kind"`
	Title string      `json:"title"`
	At    time.Time   `json:"at"`
	OK    bool        `json:"ok"`
}
