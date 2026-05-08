package llm

import (
	"context"

	"github.com/abcdlsj/sumi/msg"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client *anthropic.Client
	model  string
	cfg    Config
}

func newAnthropic(cfg Config) *anthropicProvider {
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(newRetryHTTPClient(nil)),
		option.WithMaxRetries(0),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := anthropic.NewClient(opts...)
	return &anthropicProvider{
		client: &client,
		model:  cfg.Model,
		cfg:    cfg,
	}
}

func (p *anthropicProvider) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	resp, err := p.client.Messages.New(ctx, p.buildRequest(msgs, tools))
	if err != nil {
		return nil, err
	}
	return parseAnthropicResponse(resp), nil
}

func toAnthropicTokenUsage(u anthropic.Usage) *TokenUsage {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:  int(u.InputTokens),
		OutputTokens: int(u.OutputTokens),
		TotalTokens:  int(u.InputTokens + u.OutputTokens),
		InputSource:  "anthropic.usage",
	}
}
