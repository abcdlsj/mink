package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/msg"
)

type Tool struct {
	Type     string
	Function *FunctionDef
}

type FunctionDef struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Response struct {
	Content            string
	Reasoning          string
	ReasoningSignature string
	ToolCalls          []msg.ToolCall
	Usage              *TokenUsage
}

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	InputSource  string
}

type ChunkType int

const (
	ChunkText ChunkType = iota
	ChunkToolCall
	ChunkReasoning
	ChunkDone
	ChunkError
)

type Chunk struct {
	Type               ChunkType
	Delta              string
	ReasoningDelta     string
	ToolCall           *msg.ToolCall
	Reasoning          string
	ReasoningSignature string
	Usage              *TokenUsage
	Error              error
}

type Provider interface {
	Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error)
	ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error)
}

type Config struct {
	Provider    string
	APIKey      string
	BaseURL     string
	Model       string
	Headers     map[string]string
	MaxTokens   int
	Temperature float32
	Reasoning   bool
}

func NewProvider(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "openai":
		return newOpenAI(cfg), nil
	case "anthropic":
		return newAnthropic(cfg), nil
	case "openrouter":
		return newOpenRouter(cfg), nil
	default:
		return nil, fmt.Errorf("unknown: %s", cfg.Provider)
	}
}

func replayToolCallArgs(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if json.Valid(raw) {
		return trimmed
	}
	return "{}"
}

func assistantToolCallReplayContent() string {
	return " "
}

func repairToolPairs(msgs []msg.Message) []msg.Message {
	out := make([]msg.Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		seen := map[string]bool{}
		j := i + 1
		for ; j < len(msgs); j++ {
			next := msgs[j]
			if next.Role != "tool" {
				break
			}
			for _, tr := range next.ToolResults {
				seen[tr.ToolCallID] = true
			}
		}
		var missing []msg.ToolResult
		for _, tc := range m.ToolCalls {
			if !seen[tc.ID] {
				missing = append(missing, msg.ToolResult{
					ToolCallID: tc.ID,
					Content:    "(no result captured; run ended before the tool reported back)",
				})
			}
		}
		if len(missing) > 0 {
			out = append(out, msg.Message{Role: "tool", ToolResults: missing})
		}
	}
	return out
}

func toolResultContent(tr msg.ToolResult) string {
	if tr.Error != "" {
		if tr.Content != "" {
			return tr.Content + "\nerror: " + tr.Error
		}
		return "error: " + tr.Error
	}
	if tr.Content != "" {
		return tr.Content
	}
	return "(no output)"
}
