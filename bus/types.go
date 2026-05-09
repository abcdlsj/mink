package bus

import "time"

const (
	TurnStarted      = "turn.started"
	TurnQueued       = "turn.queued"
	TurnChunk        = "turn.chunk"
	TurnReasoning    = "turn.reasoning"
	TurnFinished     = "turn.finished"
	TurnError        = "turn.error"
	ToolCallStarted  = "tool.call.started"
	ToolCallFinished = "tool.call.finished"
	ToolCallFailed   = "tool.call.failed"
	SessionCreated   = "session.created"
	SessionSaved     = "session.saved"
	SessionSwitched  = "session.switched"
	SessionUpdated   = "session.updated"
	SessionCompacted = "session.compacted"
	CommandHandled   = "command.handled"
	ModelChanged     = "model.changed"
	ServiceNotice    = "service.notice"
	DelegateStarted  = "delegate.started"
	DelegateFinished = "delegate.finished"
	DelegateFailed   = "delegate.failed"
	DelegateCanceled = "delegate.canceled"
	DelegateQueued   = "delegate.queued"
)

type Event struct {
	Type       string
	Source     string
	SessionID  string
	TaskID     string
	ToolCallID string
	Text       string
	Tool       string
	Input      string
	Output     string
	Err        string
	Time       time.Time
}
