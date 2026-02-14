package bus

const (
	TypeUserInput      = "user:input"
	TypeAssistant      = "assistant:output"
	TypeTurnDone       = "turn:done"
	TypeToolCall       = "tool:call"
	TypeToolResult     = "tool:result"
	TypeToolError      = "tool:error"
	TypeSessionNew = "session:new"
	TypeSessionFork    = "session:fork"
	TypeSessionReset   = "session:reset"
	TypeSessionCompact = "session:compact"
	TypeAgentSpawn     = "agent:spawn"
	TypeAgentDone      = "agent:done"
	TypeContextShare   = "context:share"
	TypeTaskStart      = "task:start"
	TypeTaskDone       = "task:done"

	TypeDelegate = "collab:delegate"
	TypeReport   = "collab:report"
	TypeShare    = "collab:share"

	TypeStreamChunk = "stream:chunk" // 流式片段
	TypeStreamEnd   = "stream:end"   // 流式结束

	TypeThinkingChunk = "thinking:chunk" // thinking 内容片段
	TypeThinkingEnd   = "thinking:end"   // thinking 结束

	TypeInterrupt = "agent:interrupt" // 用户打断
)
