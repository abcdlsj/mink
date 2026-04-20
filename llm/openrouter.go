package llm

import (
	"context"

	"github.com/abcdlsj/mink/msg"
	openrouter "github.com/revrost/go-openrouter"
)

type openRouter struct {
	client *openrouter.Client
	model  string
	cfg    Config
}

func newOpenRouter(cfg Config) *openRouter {
	clientCfg := openrouter.DefaultConfig(cfg.APIKey)
	clientCfg.XTitle = "Mink"
	clientCfg.HTTPClient = newRetryHTTPClient(nil)
	if cfg.BaseURL != "" {
		clientCfg.BaseURL = cfg.BaseURL
	}

	client := openrouter.NewClientWithConfig(*clientCfg)
	return &openRouter{
		client: client,
		model:  cfg.Model,
		cfg:    cfg,
	}
}

func (o *openRouter) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	resp, err := o.client.CreateChatCompletion(ctx, o.buildRequest(msgs, tools))
	if err != nil {
		return nil, err
	}
	return parseOpenRouterResponse(resp)
}
