package llm

import (
	"context"
	"fmt"

	"github.com/abcdlsj/mink/msg"
	"github.com/sashabaranov/go-openai"
)

func (o *openAI) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	req := o.buildRequest(msgs, tools)

	resp, err := o.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, wrapErr(err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response")
	}

	choice := resp.Choices[0]
	res := &Response{
		Content:   choice.Message.Content,
		Reasoning: choice.Message.ReasoningContent,
		Usage:     toTokenUsage(resp.Usage),
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
			continue
		}

		content := m.Content
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			content = assistantToolCallReplayContent()
		}
		cm := openai.ChatCompletionMessage{
			Role:             m.Role,
			Content:          content,
			ReasoningContent: m.Reasoning,
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      tc.Name,
					Arguments: replayToolCallArgs(tc.Args),
				},
			})
		}
		chatMsgs = append(chatMsgs, cm)
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
		Model:               o.model,
		Messages:            chatMsgs,
		MaxCompletionTokens: o.cfg.MaxTokens,
	}
	if len(openAITools) > 0 {
		req.Tools = openAITools
	}
	return req
}

func toTokenUsage(u openai.Usage) *TokenUsage {
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return &TokenUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  total,
		InputSource:  "openai.usage",
	}
}
