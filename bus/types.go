package bus

import "context"

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

	TypeDelegate       = "delegate:request"
	TypeDelegateAck    = "delegate:ack"
	TypeDelegateResult = "delegate:result"
)

type delegationDepthKey struct{}

func WithDelegationDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, delegationDepthKey{}, depth)
}

func DelegationDepth(ctx context.Context) int {
	v, _ := ctx.Value(delegationDepthKey{}).(int)
	return v
}
