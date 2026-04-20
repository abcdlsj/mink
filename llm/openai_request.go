package llm

import (
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/msg"
	"github.com/sashabaranov/go-openai"
)

func patchReasoning(body []byte) []byte {
	obj, ok := patchableBody(body)
	if !ok {
		return body
	}
	if _, ok := obj["reasoning"]; ok {
		return body
	}
	obj["reasoning"] = json.RawMessage(`{"enabled":true}`)
	out, _ := json.Marshal(obj)
	return out
}

func patchAssistantContent(body []byte) []byte {
	obj, ok := patchableBody(body)
	if !ok {
		return body
	}
	raw, ok := obj["messages"]
	if !ok {
		return body
	}
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(raw, &msgs) != nil {
		return body
	}
	patched := false
	for _, m := range msgs {
		if string(m["role"]) == `"assistant"` {
			if _, ok := m["content"]; !ok {
				m["content"] = json.RawMessage("null")
				patched = true
			}
		}
	}
	if !patched {
		return body
	}
	obj["messages"], _ = json.Marshal(msgs)
	out, _ := json.Marshal(obj)
	return out
}

func patchableBody(body []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return nil, false
	}
	return obj, true
}

func (o *openAI) buildRequest(msgs []msg.Message, tools []Tool) openai.ChatCompletionRequest {
	req := openai.ChatCompletionRequest{
		Model:               o.model,
		Messages:            toOpenAIMessages(msgs),
		MaxCompletionTokens: o.cfg.MaxTokens,
	}
	if ts := toOpenAITools(tools); len(ts) > 0 {
		req.Tools = ts
	}
	return req
}

func toOpenAIMessages(msgs []msg.Message) []openai.ChatCompletionMessage {
	var out []openai.ChatCompletionMessage
	for _, m := range msgs {
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				out = append(out, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    tr.Content,
					ToolCallID: tr.ToolCallID,
				})
			}
			continue
		}
		out = append(out, openAIMessage(m))
	}
	return out
}

func openAIMessage(m msg.Message) openai.ChatCompletionMessage {
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
	return cm
}

func toOpenAITools(tools []Tool) []openai.Tool {
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

func parseOpenAIResponse(resp openai.ChatCompletionResponse) (*Response, error) {
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
