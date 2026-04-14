package llm

import (
	"context"
	"io"

	"github.com/abcdlsj/mink/msg"
	openrouter "github.com/revrost/go-openrouter"
)

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
			continue
		}

		content := m.Content
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			content = assistantToolCallReplayContent()
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
					Arguments: replayToolCallArgs(tc.Args),
				},
			})
		}
		chatMsgs = append(chatMsgs, cm)
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
		Model:               o.model,
		Messages:            chatMsgs,
		MaxCompletionTokens: o.cfg.MaxTokens,
	}
	if o.cfg.Reasoning {
		req.Reasoning = &openrouter.ChatCompletionReasoning{Enabled: &enabled}
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
