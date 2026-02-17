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
	ReasoningContent   string
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
	Delta              string        // 增量文本
	ReasoningDelta     string        // 增量思考内容
	ToolCall           *msg.ToolCall // 工具调用（完整后发送）
	ReasoningContent   string        // 完整思考内容（与 ToolCall 一起发送）
	ReasoningSignature string        // thinking block signature (Anthropic)
	Usage              *TokenUsage
	Error              error
}

type Provider interface {
	Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error)
	ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error)
}

type Config struct {
	Provider            string
	APIKey              string
	BaseURL             string
	Model               string
	Headers             map[string]string
	MaxTokens           int
	Temperature         float32
	OpenRouterReasoning bool
}

func NewProvider(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "openai":
		return newOpenAI(cfg), nil
	case "anthropic":
		return newAnthropic(cfg), nil
	default:
		return nil, fmt.Errorf("unknown: %s", cfg.Provider)
	}
}
