package bus

const (
	TypeUserInput      = "user:input"
	TypeAssistant      = "assistant:output"
	TypeTurnDone       = "turn:done"
	TypeToolCall       = "tool:call"
	TypeToolResult     = "tool:result"
	TypeToolError      = "tool:error"
	TypeSessionNew     = "session:new"
	TypeSessionReset   = "session:reset"
	TypeSessionCompact = "session:compact"

	TypeSubtaskRun = "subtask:run"
	TypeAgentSpawn = "agent:spawn"
	TypeAgentDone  = "agent:done"

	TypeTaskStart = "task:start"
	TypeTaskDone  = "task:done"

	TypeStreamChunk = "stream:chunk"
	TypeStreamEnd   = "stream:end"

	TypeThinkingChunk = "thinking:chunk"
	TypeThinkingEnd   = "thinking:end"

	TypeInterrupt   = "agent:interrupt"
	TypeCronTrigger = "cron:trigger"

	// P1: multi-agent collaboration
	TypeDelegate       = "delegate:request"
	TypeDelegateAck    = "delegate:ack"
	TypeDelegateResult = "delegate:result"
	TypePresence       = "presence:update"
)
