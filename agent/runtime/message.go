package runtime

type Message struct {
	Type         MessageType
	Text         string
	ToolName     string
	ToolArgs     string
	ToolID       string
	SessionID    string
	InputTokens  int
	OutputTokens int
	Error        error
}

type MessageType int

const (
	MsgAssistantText MessageType = iota
	MsgStreamChunk
	MsgThinkingChunk
	MsgToolCall
	MsgToolResult
	MsgTurnDone
	MsgError
)
