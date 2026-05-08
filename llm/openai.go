package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/abcdlsj/sumi/msg"
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

func (o *openAI) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	resp, err := o.client.CreateChatCompletion(ctx, o.buildRequest(msgs, tools))
	if err != nil {
		return nil, wrapOpenAIErr(err)
	}
	return parseOpenAIResponse(resp)
}

func wrapOpenAIErr(err error) error {
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

type openAITransport struct {
	headers   map[string]string
	reasoning bool
}

func (t *openAITransport) prepare(req *http.Request) error {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}
	body = patchAssistantContent(body)
	if t.reasoning {
		body = patchReasoning(body)
	}
	resetRequestBody(req, body)
	return nil
}
