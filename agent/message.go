package agent

import (
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
)

func NewUserMessage(text string) msg.Message {
	return NewUserMessageWithAttachments(text, nil)
}

func NewUserMessageWithAttachments(text string, attachments []msg.Attachment) msg.Message {
	return msg.Message{
		ID:          newID(),
		Role:        "user",
		Content:     text,
		Attachments: attachments,
		Timestamp:   now(),
	}
}

func newAssistantMessage(resp *llm.Response) msg.Message {
	return msg.Message{
		ID:        newID(),
		Role:      "assistant",
		Content:   resp.Content,
		Reasoning: resp.Reasoning,
		ToolCalls: resp.ToolCalls,
		Usage:     tokenUsage(resp.Usage),
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

func tokenUsage(u *llm.TokenUsage) *msg.TokenUsage {
	if u == nil {
		return nil
	}
	return &msg.TokenUsage{
		Total:  u.TotalTokens,
		Input:  u.InputTokens,
		Output: u.OutputTokens,
		Source: u.InputSource,
	}
}
