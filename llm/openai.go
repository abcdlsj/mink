package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/sashabaranov/go-openai"
)

type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

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

	config := openai.DefaultConfig(cfg.APIKey)
	config.BaseURL = cfg.BaseURL

	if len(cfg.Headers) > 0 {
		config.HTTPClient = &http.Client{
			Transport: &headerTransport{
				headers: cfg.Headers,
				base:    http.DefaultTransport,
			},
		}
	}

	return &openAI{
		client: openai.NewClientWithConfig(config),
		model:  cfg.Model,
		cfg:    cfg,
	}
}

func (o *openAI) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	req := o.buildRequest(msgs, tools)

	resp, err := o.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response")
	}

	choice := resp.Choices[0]
	res := &Response{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
	}

	for _, tc := range choice.Message.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, msg.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: []byte(tc.Function.Arguments),
		})
	}

	return res, nil
}

func (o *openAI) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)

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
		toolCallsMap := make(map[int]*msg.ToolCall)

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err.Error() != "EOF" {
					select {
					case ch <- Chunk{Type: ChunkError, Error: err}:
					case <-ctx.Done():
					}
				}
				break
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			if delta.ReasoningContent != "" {
				reasoningContent.WriteString(delta.ReasoningContent)
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

		reasoning := reasoningContent.String()
		for i := 0; i < len(toolCallsMap); i++ {
			if tc, ok := toolCallsMap[i]; ok {
				select {
				case ch <- Chunk{Type: ChunkToolCall, ToolCall: tc, ReasoningContent: reasoning}:
				case <-ctx.Done():
					return
				}
				reasoning = "" // 只在第一个 ToolCall 中发送
			}
		}

		select {
		case ch <- Chunk{Type: ChunkDone, ReasoningContent: reasoningContent.String()}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
}

func (o *openAI) buildRequest(msgs []msg.Message, tools []Tool) openai.ChatCompletionRequest {
	var chatMsgs []openai.ChatCompletionMessage

	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				chatMsgs = append(chatMsgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    tr.Content,
					ToolCallID: tr.ToolCallID,
				})
			}
		} else {
			cm := openai.ChatCompletionMessage{
				Role:             m.Role,
				Content:          m.Content,
				ReasoningContent: m.ReasoningContent,
			}
			for _, tc := range m.ToolCalls {
				cm.ToolCalls = append(cm.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Args),
					},
				})
			}
			chatMsgs = append(chatMsgs, cm)
		}
	}

	var openAITools []openai.Tool
	for _, t := range tools {
		openAITools = append(openAITools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:     o.model,
		Messages:  chatMsgs,
		MaxTokens: o.cfg.MaxTokens,
	}

	if len(openAITools) > 0 {
		req.Tools = openAITools
	}

	return req
}
