package agent

import (
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
)

func NewUserMessage(text string) msg.Message {
	return msg.Message{
		ID:        newID(),
		Role:      "user",
		Content:   text,
		Timestamp: now(),
	}
}

func newAssistantMessage(resp *llm.Response) msg.Message {
	return msg.Message{
		ID:        newID(),
		Role:      "assistant",
		Content:   resp.Content,
		Reasoning: resp.Reasoning,
		ToolCalls: resp.ToolCalls,
		Timestamp: now(),
	}
}

func newToolMessage(result msg.ToolResult) msg.Message {
	return msg.Message{
		ID:          newID(),
		Role:        "tool",
		ToolResults: []msg.ToolResult{result},
		Timestamp:   now(),
	}
}

func newID() string {
	return uuid.New().String()[:8]
}

func now() time.Time {
	return time.Now()
}
