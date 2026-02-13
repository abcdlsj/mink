package llm

import (
	"context"
	"encoding/json"

	"github.com/abcdlsj/mink/msg"
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
	params := p.buildRequest(msgs, tools)

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	return p.parseResponse(resp), nil
}

func (p *anthropicProvider) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	ch := make(chan Chunk, 32)

	go func() {
		defer close(ch)

		resp, err := p.Chat(ctx, msgs, tools)
		if err != nil {
			select {
			case ch <- Chunk{Type: ChunkError, Error: err}:
			case <-ctx.Done():
			}
			return
		}

		if resp.Content != "" {
			select {
			case ch <- Chunk{Type: ChunkText, Delta: resp.Content}:
			case <-ctx.Done():
				return
			}
		}

		for _, tc := range resp.ToolCalls {
			tcCopy := tc
			select {
			case ch <- Chunk{Type: ChunkToolCall, ToolCall: &tcCopy}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case ch <- Chunk{
			Type:               ChunkDone,
			Usage:              resp.Usage,
			ReasoningContent:   resp.ReasoningContent,
			ReasoningSignature: resp.ReasoningSignature,
		}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}

func (p *anthropicProvider) buildRequest(msgs []msg.Message, tools []Tool) anthropic.MessageNewParams {
	var apiMessages []anthropic.MessageParam
	var systemContent string

	for _, m := range msgs {
		if m.Role == "system" {
			systemContent = m.Content
			continue
		}

		if m.Role == "user" {
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tr := range m.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, false))
			}
			if len(blocks) > 0 {
				apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))
			}
		} else if m.Role == "assistant" {
			var blocks []anthropic.ContentBlockParamUnion
			if m.ReasoningSignature != "" && m.ReasoningContent != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock(m.ReasoningSignature, m.ReasoningContent))
			}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				json.Unmarshal(tc.Args, &args)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
			}
			if len(blocks) > 0 {
				apiMessages = append(apiMessages, anthropic.NewAssistantMessage(blocks...))
			}
		} else if m.Role == "tool" {
			var blocks []anthropic.ContentBlockParamUnion
			for _, tr := range m.ToolResults {
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, false))
			}
			if len(blocks) > 0 {
				apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))
			}
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(p.cfg.MaxTokens),
		Messages:  apiMessages,
	}

	if systemContent != "" {
		params.System = []anthropic.TextBlockParam{{
			Type: "text",
			Text: systemContent,
		}}
	}

	if len(tools) > 0 {
		toolUnions := make([]anthropic.ToolUnionParam, len(tools))
		for i, t := range tools {
			schemaBytes, _ := json.Marshal(t.Function.Parameters)
			var inputSchema anthropic.ToolInputSchemaParam
			json.Unmarshal(schemaBytes, &inputSchema)

			toolParam := anthropic.ToolParam{
				Name:        t.Function.Name,
				Description: anthropic.String(t.Function.Description),
				InputSchema: inputSchema,
			}
			toolUnions[i] = anthropic.ToolUnionParam{OfTool: &toolParam}
		}
		params.Tools = toolUnions
	}

	return params
}

func (p *anthropicProvider) parseResponse(resp *anthropic.Message) *Response {
	var content string
	var reasoning string
	var signature string
	var toolCalls []msg.ToolCall

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.ThinkingBlock:
			reasoning = b.Thinking
			signature = b.Signature
		case anthropic.TextBlock:
			content += b.Text
		case anthropic.ToolUseBlock:
			argsJSON, _ := json.Marshal(b.Input)
			toolCalls = append(toolCalls, msg.ToolCall{
				ID:   b.ID,
				Name: b.Name,
				Args: argsJSON,
			})
		}
	}

	return &Response{
		Content:            content,
		ReasoningContent:   reasoning,
		ReasoningSignature: signature,
		ToolCalls:          toolCalls,
		Usage:              toAnthropicTokenUsage(resp.Usage),
	}
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
