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

	TypeStreamChunk = "stream:chunk" // 流式片段
	TypeStreamEnd   = "stream:end"   // 流式结束

	TypeThinkingChunk = "thinking:chunk" // thinking 内容片段
	TypeThinkingEnd   = "thinking:end"   // thinking 结束

	TypeInterrupt   = "agent:interrupt" // 用户打断
	TypeCronTrigger = "cron:trigger"    // cron 定时触发
)
