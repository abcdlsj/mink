package llm

import (
	"context"
	"io"
	"strings"

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
	req := o.buildRequest(msgs, tools)

	resp, err := o.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, io.EOF
	}

	choice := resp.Choices[0]
	var reasoning string
	if choice.Message.Reasoning != nil {
		reasoning = *choice.Message.Reasoning
	} else if choice.Message.ReasoningContent != nil {
		reasoning = *choice.Message.ReasoningContent
	}

	res := &Response{
		Content:   choice.Message.Content.Text,
		Reasoning: reasoning,
		Usage:     toTokenUsageFromOR(resp.Usage),
	}

	for _, tc := range choice.Message.ToolCalls {
		if tc.Function.Name == "" || tc.Function.Arguments == "" {
			continue
		}
		res.ToolCalls = append(res.ToolCalls, msg.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: []byte(tc.Function.Arguments),
		})
	}

	return res, nil
}

func (o *openRouter) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)
	req.Stream = true
	req.StreamOptions = &openrouter.StreamOptions{IncludeUsage: true}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 32)

	go func() {
		defer stream.Close()
		defer close(ch)

		var fullContent strings.Builder
		var reasoningContent strings.Builder
		var usage *TokenUsage
		toolCallsMap := make(map[int]*msg.ToolCall)

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				select {
				case ch <- Chunk{Type: ChunkError, Error: err}:
				case <-ctx.Done():
				}
				break
			}

			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = toTokenUsageFromOR(chunk.Usage)
				}
				continue
			}

			if chunk.Usage != nil {
				usage = toTokenUsageFromOR(chunk.Usage)
			}

			delta := chunk.Choices[0].Delta

			if delta.Reasoning != nil && *delta.Reasoning != "" {
				reasoningContent.WriteString(*delta.Reasoning)
				select {
				case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: *delta.Reasoning}:
				case <-ctx.Done():
					return
				}
			}

			if delta.ReasoningContent != "" {
				reasoningContent.WriteString(delta.ReasoningContent)
				select {
				case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: delta.ReasoningContent}:
				case <-ctx.Done():
					return
				}
			}

			if delta.Content != "" {
				fullContent.WriteString(delta.Content)
				select {
				case ch <- Chunk{Type: ChunkText, Delta: delta.Content}:
				case <-ctx.Done():
					return
				}
			}

			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				if _, exists := toolCallsMap[idx]; !exists {
					toolCallsMap[idx] = &msg.ToolCall{}
				}
				if tc.ID != "" {
					toolCallsMap[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					toolCallsMap[idx].Name = tc.Function.Name
				}
				toolCallsMap[idx].Args = append(toolCallsMap[idx].Args, []byte(tc.Function.Arguments)...)
			}
		}

		for i := 0; i < len(toolCallsMap); i++ {
			if tc, ok := toolCallsMap[i]; ok {
				if tc.Name == "" || len(tc.Args) == 0 {
					continue
				}
				select {
				case ch <- Chunk{Type: ChunkToolCall, ToolCall: tc}:
				case <-ctx.Done():
					return
				}
			}
		}

		select {
		case ch <- Chunk{
			Type:      ChunkDone,
			Usage:     usage,
			Reasoning: reasoningContent.String(),
		}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}

func (o *openRouter) buildRequest(msgs []msg.Message, tools []Tool) openrouter.ChatCompletionRequest {
	var chatMsgs []openrouter.ChatCompletionMessage

	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				chatMsgs = append(chatMsgs, openrouter.ChatCompletionMessage{
					Role:       openrouter.ChatMessageRoleTool,
					Content:    openrouter.Content{Text: tr.Content},
					ToolCallID: tr.ToolCallID,
				})
			}
		} else {
			content := m.Content
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				content = ""
			}
			cm := openrouter.ChatCompletionMessage{
				Role:    m.Role,
				Content: openrouter.Content{Text: content},
			}
			if m.Reasoning != "" {
				cm.Reasoning = &m.Reasoning
			}
			for _, tc := range m.ToolCalls {
				cm.ToolCalls = append(cm.ToolCalls, openrouter.ToolCall{
					ID:   tc.ID,
					Type: openrouter.ToolTypeFunction,
					Function: openrouter.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Args),
					},
				})
			}
			chatMsgs = append(chatMsgs, cm)
		}
	}

	var openRouterTools []openrouter.Tool
	for _, t := range tools {
		openRouterTools = append(openRouterTools, openrouter.Tool{
			Type: openrouter.ToolTypeFunction,
			Function: &openrouter.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}

	enabled := true
	req := openrouter.ChatCompletionRequest{
		Model:     o.model,
		Messages:  chatMsgs,
		MaxTokens: o.cfg.MaxTokens,
	}

	if o.cfg.Reasoning {
		req.Reasoning = &openrouter.ChatCompletionReasoning{
			Enabled: &enabled,
		}
	}

	if len(openRouterTools) > 0 {
		req.Tools = openRouterTools
	}

	return req
}

func toTokenUsageFromOR(u *openrouter.Usage) *TokenUsage {
	if u == nil {
		return nil
	}
	return &TokenUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
		InputSource:  "openrouter.usage",
	}
}
