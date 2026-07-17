package bus

import "time"

const (
	TurnStarted       = "turn.started"
	TurnQueued        = "turn.queued"
	TurnChunk         = "turn.chunk"
	TurnReasoning     = "turn.reasoning"
	TurnFinished      = "turn.finished"
	TurnError         = "turn.error"
	ToolCallStarted   = "tool.call.started"
	ToolCallFinished  = "tool.call.finished"
	ToolCallFailed    = "tool.call.failed"
	SessionCreated    = "session.created"
	SessionSaved      = "session.saved"
	SessionSwitched   = "session.switched"
	SessionUpdated    = "session.updated"
	SessionCompacted  = "session.compacted"
	CommandHandled    = "command.handled"
	ModelChanged      = "model.changed"
	ServiceNotice     = "service.notice"
	DelegateStarted   = "delegate.started"
	DelegateFinished  = "delegate.finished"
	DelegateFailed    = "delegate.failed"
	DelegateCanceled  = "delegate.canceled"
	DelegateQueued    = "delegate.queued"
	SpaceTitleChanged = "space.title.changed"
	SpaceCreated      = "space.created"
	SpaceUpdated      = "space.updated"
	SpaceMessageAdded = "space.message.appended"
	TaskCreated       = "task.created"
	TaskUpdated       = "task.updated"
	RunStarted        = "run.started"
	RunFinished       = "run.finished"
	RuntimeInfo       = "runtime.info"
	SkillListed       = "skill.listed"
	SkillDescribed    = "skill.described"
	SkillUsed         = "skill.used"
	ActionProposal    = "action.proposal"
)

type Event struct {
	Type            string
	Source          string
	SessionID       string
	TaskID          string
	RunID           string
	MessageID       string
	ToolCallID      string
	Text            string
	Tool            string
	Input           string
	Output          string
	Err             string
	Time            time.Time
	SpaceID         string
	ParentMessageID string
	AgentID         string
	StreamID        string
	// DeliveryID is set on routed turn events (TurnStarted/Chunk/Finished/Error)
	// that a durable delivery worker drives. When present, the desktop backend
	// binds to the delivery's pre-created assistant placeholder (MessageID) rather
	// than appending a new pending message, and must NOT delete that message on
	// TurnFinished — the worker owns the placeholder's terminal state via the
	// Delivery. Empty for the direct single-agent path, which keeps its original
	// zero-Delivery projection.
	DeliveryID string
}
