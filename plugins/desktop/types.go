package desktop

import "time"

type SessionItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	PersonaID    string    `json:"persona_id,omitempty"`
	PersonaName  string    `json:"persona_name,omitempty"`
	Runtime      string    `json:"runtime"`
	Model        string    `json:"model"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	EventCount   int       `json:"event_count"`
	Running      bool      `json:"running"`
	Pinned       bool      `json:"pinned"`
}

type SessionDetail struct {
	Item     SessionItem   `json:"item"`
	Messages []MessageView `json:"messages"`
	Summary  string        `json:"summary,omitempty"`
}

type MessageView struct {
	ID            string       `json:"id"`
	Role          string       `json:"role"`
	AuthorID      string       `json:"author_id,omitempty"`
	AuthorName    string       `json:"author_name,omitempty"`
	Content       string       `json:"content,omitempty"`
	Reasoning     string       `json:"reasoning,omitempty"`
	Time          time.Time    `json:"time"`
	Events        []EventBlock `json:"events,omitempty"`
	Usage         *TokenUsage  `json:"usage,omitempty"`
	ThreadID      string       `json:"thread_id,omitempty"`
	ThreadSummary string       `json:"thread_summary,omitempty"`
}

type EventBlock struct {
	Kind         string    `json:"kind"`
	ToolName     string    `json:"tool_name,omitempty"`
	Args         string    `json:"args,omitempty"`
	Output       string    `json:"output,omitempty"`
	Status       string    `json:"status,omitempty"`
	Err          string    `json:"err,omitempty"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
	Time         time.Time `json:"time"`
	AgentID      string    `json:"agent_id,omitempty"`
	AgentDisplay string    `json:"agent_display,omitempty"`
	Task         string    `json:"task,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	Reply        string    `json:"reply,omitempty"`
}

type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}

type PersonaItem struct {
	ID          string   `json:"id"`
	Display     string   `json:"display"`
	Runtime     string   `json:"runtime"`
	Description string   `json:"description"`
	Tools       []string `json:"tools,omitempty"`
}

type ModelItem struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	MaxTokens     int    `json:"max_tokens"`
	ContextWindow int    `json:"context_window"`
	Ready         bool   `json:"ready"`
}

type ToolItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type CommandItem struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Usage   string `json:"usage,omitempty"`
}

type WorkspaceState struct {
	Workspace string `json:"workspace"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Runtime   string `json:"runtime"`
	Ready     bool   `json:"ready"`
	DataDir   string `json:"data_dir"`
}

type SendRequest struct {
	SessionID string `json:"session_id"`
	PersonaID string `json:"persona_id,omitempty"`
	Input     string `json:"input"`
}

type BusEvent struct {
	Type       string    `json:"type"`
	Source     string    `json:"source,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	Text       string    `json:"text,omitempty"`
	Err        string    `json:"err,omitempty"`
	Time       time.Time `json:"time"`
}
