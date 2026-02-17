package llm

import (
	"context"
	"encoding/json"
	"strings"

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
	params := p.buildRequest(msgs, tools)

	stream := p.client.Messages.NewStreaming(ctx, params)

	ch := make(chan Chunk, 32)

	go func() {
		defer close(ch)

		type pendingTool struct {
			id   string
			name string
			args strings.Builder
		}

		var pending []pendingTool
		var usage *TokenUsage
		var reasoning strings.Builder
		var signature string

		for stream.Next() {
			event := stream.Current()

			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				switch block := ev.ContentBlock.AsAny().(type) {
				case anthropic.ToolUseBlock:
					pending = append(pending, pendingTool{id: block.ID, name: block.Name})
				}

			case anthropic.ContentBlockDeltaEvent:
				switch delta := ev.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					select {
					case ch <- Chunk{Type: ChunkText, Delta: delta.Text}:
					case <-ctx.Done():
						return
					}

				case anthropic.ThinkingDelta:
					reasoning.WriteString(delta.Thinking)
					select {
					case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: delta.Thinking}:
					case <-ctx.Done():
						return
					}

				case anthropic.InputJSONDelta:
					if len(pending) > 0 {
						pending[len(pending)-1].args.WriteString(delta.PartialJSON)
					}

				case anthropic.SignatureDelta:
					signature = delta.Signature
				}

			case anthropic.MessageDeltaEvent:
				usage = &TokenUsage{
					InputTokens:  int(ev.Usage.InputTokens),
					OutputTokens: int(ev.Usage.OutputTokens),
					TotalTokens:  int(ev.Usage.InputTokens + ev.Usage.OutputTokens),
					InputSource:  "anthropic.stream",
				}
			}
		}

		if err := stream.Err(); err != nil {
			select {
			case ch <- Chunk{Type: ChunkError, Error: err}:
			case <-ctx.Done():
			}
			return
		}

		for _, pt := range pending {
			tc := msg.ToolCall{
				ID:   pt.id,
				Name: pt.name,
				Args: json.RawMessage(pt.args.String()),
			}
			select {
			case ch <- Chunk{Type: ChunkToolCall, ToolCall: &tc}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case ch <- Chunk{
			Type:               ChunkDone,
			Usage:              usage,
			Reasoning:          reasoning.String(),
			ReasoningSignature: signature,
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
				content := tr.Content
				if content == "" {
					content = "(no output)"
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, content, false))
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropic.NewTextBlock("(empty)"))
			}
			apiMessages = append(apiMessages, anthropic.NewUserMessage(blocks...))
		} else if m.Role == "assistant" {
			var blocks []anthropic.ContentBlockParamUnion
			if m.ReasoningSignature != "" && m.Reasoning != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock(m.ReasoningSignature, m.Reasoning))
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
				content := tr.Content
				if content == "" {
					content = "(no output)"
				}
				blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, content, false))
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
	var sig string
	var toolCalls []msg.ToolCall

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.ThinkingBlock:
			reasoning = b.Thinking
			sig = b.Signature
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
		Reasoning:          reasoning,
		ReasoningSignature: sig,
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
