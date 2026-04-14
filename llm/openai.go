package llm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type openAI struct {
	client *openai.Client
	model  string
	cfg    Config
}

func newOpenAI(cfg Config) *openAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	clientCfg := openai.DefaultConfig(cfg.APIKey)
	clientCfg.BaseURL = cfg.BaseURL
	clientCfg.HTTPClient = newRetryHTTPClient((&openAITransport{
		headers:   cfg.Headers,
		reasoning: cfg.Reasoning,
	}).prepare)

	return &openAI{
		client: openai.NewClientWithConfig(clientCfg),
		model:  cfg.Model,
		cfg:    cfg,
	}
}

func wrapErr(err error) error {
	var re *openai.RequestError
	if errors.As(err, &re) {
		body := strings.TrimSpace(string(re.Body))
		if body == "" {
			body = re.HTTPStatus
		}
		return fmt.Errorf("%s (HTTP %d)", body, re.HTTPStatusCode)
	}
	return err
}
