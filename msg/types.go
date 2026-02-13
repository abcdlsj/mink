package msg

import (
	"encoding/json"
	"time"
)

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	Error      string `json:"error,omitempty"`
}

type Message struct {
	ID                 string       `json:"id,omitempty"`
	Role               string       `json:"role"`
	Content            string       `json:"content,omitempty"`
	ReasoningContent   string       `json:"reasoning_content,omitempty"`
	ReasoningSignature string       `json:"reasoning_signature,omitempty"`
	ToolCalls          []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults        []ToolResult `json:"tool_results,omitempty"`
	Timestamp          time.Time    `json:"timestamp,omitempty"`
}
