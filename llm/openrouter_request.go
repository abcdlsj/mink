package llm

import (
	"io"

	"github.com/abcdlsj/sumi/msg"
	openrouter "github.com/revrost/go-openrouter"
)

func (o *openRouter) buildRequest(msgs []msg.Message, tools []Tool) openrouter.ChatCompletionRequest {
	req := openrouter.ChatCompletionRequest{
		Model:               o.model,
		Messages:            toOpenRouterMessages(msgs),
		MaxCompletionTokens: o.cfg.MaxTokens,
	}
	if o.cfg.Reasoning {
		enabled := true
		req.Reasoning = &openrouter.ChatCompletionReasoning{Enabled: &enabled}
	}
	if ts := toOpenRouterTools(tools); len(ts) > 0 {
		req.Tools = ts
	}
	return req
}

func toOpenRouterMessages(msgs []msg.Message) []openrouter.ChatCompletionMessage {
	var out []openrouter.ChatCompletionMessage
	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				out = append(out, openrouter.ChatCompletionMessage{
					Role:       openrouter.ChatMessageRoleTool,
					Content:    openrouter.Content{Text: tr.Content},
					ToolCallID: tr.ToolCallID,
				})
			}
			continue
		}
		out = append(out, openRouterMessage(m))
	}
	return out
}

func openRouterMessage(m msg.Message) openrouter.ChatCompletionMessage {
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
	return cm
}

func toOpenRouterTools(tools []Tool) []openrouter.Tool {
	out := make([]openrouter.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openrouter.Tool{
			Type: openrouter.ToolTypeFunction,
			Function: &openrouter.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

func parseOpenRouterResponse(resp openrouter.ChatCompletionResponse) (*Response, error) {
	if len(resp.Choices) == 0 {
		return nil, io.EOF
	}
	choice := resp.Choices[0]
	res := &Response{
		Content:   choice.Message.Content.Text,
		Reasoning: openRouterReasoning(choice.Message),
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

func openRouterReasoning(m openrouter.ChatCompletionMessage) string {
	if m.Reasoning != nil {
		return *m.Reasoning
	}
	if m.ReasoningContent != nil {
		return *m.ReasoningContent
	}
	return ""
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
