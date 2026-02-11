package bus

const (
	TypeUserInput    = "user:input"
	TypeAssistant    = "assistant:output"
	TypeToolCall     = "tool:call"
	TypeToolResult   = "tool:result"
	TypeToolError    = "tool:error"
	TypeCommand      = "cmd:call"
	TypeCommandOK    = "cmd:ok"
	TypeCommandError = "cmd:error"
	TypeSessionNew   = "session:new"
	TypeSessionFork  = "session:fork"
	TypeAgentSpawn   = "agent:spawn"
	TypeAgentDone    = "agent:done"
	TypeContextShare = "context:share"

	TypeDelegate = "collab:delegate"
	TypeReport   = "collab:report"
	TypeShare    = "collab:share"
)
