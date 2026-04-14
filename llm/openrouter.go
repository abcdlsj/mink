package llm

import openrouter "github.com/revrost/go-openrouter"

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
