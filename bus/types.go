package bus

const (
	TypeUserInput    = "user:input"
	TypeAssistant    = "assistant:output"
	TypeToolCall     = "tool:call"
	TypeToolResult   = "tool:result"
	TypeToolError    = "tool:error"
	TypeSessionNew   = "session:new"
	TypeSessionFork  = "session:fork"
	TypeAgentSpawn   = "agent:spawn"
	TypeAgentDone    = "agent:done"
	TypeContextShare = "context:share"

	TypeDelegate = "collab:delegate"
	TypeReport   = "collab:report"
	TypeShare    = "collab:share"
)
