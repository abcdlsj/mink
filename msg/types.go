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

func (t ToolCall) MarshalJSON() ([]byte, error) {
	type Alias ToolCall
	aux := &struct {
		*Alias
		Args json.RawMessage `json:"args"`
	}{
		Alias: (*Alias)(&t),
	}

	if len(t.Args) > 0 && json.Valid(t.Args) {
		aux.Args = t.Args
	} else {
		aux.Args = json.RawMessage("null")
	}

	return json.Marshal(aux)
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	Error      string `json:"error,omitempty"`
}

type TokenUsage struct {
	Messages       int
	Total          int
	Input          int
	Output         int
	System         int
	Tool           int
	CompactTrigger int
	ContextWindow  int
	MaxTokens      int
	Reserve        int
	CostUSD        float64
	Model          string
	Source         string
}

type Attachment struct {
	Kind     string `json:"kind"`
	Label    string `json:"label,omitempty"`
	Name     string `json:"name,omitempty"`
	MIME     string `json:"mime,omitempty"`
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	Telegram string `json:"telegram,omitempty"`
}

type Message struct {
	ID                 string            `json:"id,omitempty"`
	Role               string            `json:"role"`
	AgentID            string            `json:"agent_id,omitempty"`
	Content            string            `json:"content,omitempty"`
	Attachments        []Attachment      `json:"attachments,omitempty"`
	Reasoning          string            `json:"reasoning,omitempty"`
	ReasoningSignature string            `json:"reasoning_signature,omitempty"`
	ToolCalls          []ToolCall        `json:"tool_calls,omitempty"`
	ToolResults        []ToolResult      `json:"tool_results,omitempty"`
	Usage              *TokenUsage       `json:"usage,omitempty"`
	RuntimeMeta        map[string]string `json:"runtime_meta,omitempty"`
	Timestamp          time.Time         `json:"timestamp,omitempty"`
}
