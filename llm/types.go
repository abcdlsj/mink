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
	Content          string
	ReasoningContent string
	ToolCalls        []msg.ToolCall
}

type Provider interface {
	Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error)
}

type Config struct {
	Provider    string
	APIKey      string
	BaseURL     string
	Model       string
	Headers     map[string]string
	MaxTokens   int
	Temperature float32
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
