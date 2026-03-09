package llm

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/msg"
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
