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
	MaxTitleLen              = 80
	MaxOutcomeLen            = 200
	MaxExpectedOutcomeLen    = 500
	MaxAcceptanceCriteriaLen = 1200
	MaxKeySteps              = 8
)

type Task struct {
	ID                 string    `json:"id"`
	SpaceID            string    `json:"space_id"`
	TriggerMessageID   string    `json:"trigger_message_id"`
	SourceThreadID     string    `json:"source_thread_id,omitempty"`
	InitiatorID        string    `json:"initiator_id"`
	CreatedBy          string    `json:"created_by,omitempty"`
	WorkerID           string    `json:"worker_id"`
	AssignedBy         string    `json:"assigned_by,omitempty"`
	Title              string    `json:"title"`
	ExpectedOutcome    string    `json:"expected_outcome,omitempty"`
	AcceptanceCriteria string    `json:"acceptance_criteria,omitempty"`
	Status             Status    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ResultMessageID    string    `json:"result_message_id,omitempty"`
	Outcome            string    `json:"outcome,omitempty"`
	Source             string    `json:"source,omitempty"`
	State              TaskState `json:"state,omitempty"`
	// ExecutionIntent is the immutable execution contract for an async-delegate
	// Task, persisted so a crash between the Task write and the Delivery write
	// can be recovered from the Task fact alone. It holds only what Task does not
	// already carry (Source/SpaceID/TriggerMessageID/WorkerID live above).
	ExecutionIntent *ExecutionIntent `json:"execution_intent,omitempty"`
}

// ExecutionIntent captures the immutable inputs needed to re-run a delegated
// turn after a restart. The Delivery references the Task by ID; the full prompt
// is stored here rather than only in the Delivery, so it survives a crash that
// happens before the Delivery record is written.
type ExecutionIntent struct {
	Input        string `json:"input"`
	Runtime      string `json:"runtime,omitempty"`
	ShareContext bool   `json:"share_context"`
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
