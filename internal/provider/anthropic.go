package provider

import (
	"context"
	"encoding/json"
	"errors"
)

type AnthropicMessages struct {
	config HTTPConfig
}

func NewAnthropicMessages(config HTTPConfig) (*AnthropicMessages, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &AnthropicMessages{config: config}, nil
}

func (c *AnthropicMessages) Complete(ctx context.Context, r Request) (Response, error) {
	msgs := make([]anthropicMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		switch m.Role {
		case RoleUser:
			msgs = append(msgs, anthropicMessage{Role: string(m.Role), Content: []anthropicContent{{Type: "text", Text: m.Text}}})
		case RoleAssistant:
			content := make([]anthropicContent, 0, 1+len(m.ToolCalls))
			if m.Text != "" {
				content = append(content, anthropicContent{Type: "text", Text: m.Text})
			}
			for _, call := range m.ToolCalls {
				content = append(content, anthropicContent{Type: "tool_use", ID: call.ID, Name: call.Name, Input: call.Arguments})
			}
			if len(content) == 0 {
				return Response{}, errors.New("anthropic assistant continuation is empty")
			}
			msgs = append(msgs, anthropicMessage{Role: "assistant", Content: content})
		case RoleTool:
			msgs = append(msgs, anthropicMessage{Role: "user", Content: []anthropicContent{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Text}}})
		default:
			return Response{}, errors.New("anthropic request message role is invalid")
		}
	}
	tools := make([]anthropicTool, 0, len(r.Tools))
	for _, t := range r.Tools {
		tools = append(tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	payload := anthropicRequest{Model: c.config.Model, MaxTokens: 8192, System: r.System, Messages: msgs, Tools: tools}
	var wire anthropicResponse
	if err := postJSON(ctx, c.config.Client, endpointWithDefaultPath(c.config.Endpoint, "/v1/messages"), map[string]string{
		"x-api-key": c.config.APIKey, "anthropic-version": "2023-06-01",
	}, payload, &wire); err != nil {
		return Response{}, err
	}
	out := Response{ID: wire.ID, Usage: Usage{InputUnits: wire.Usage.InputTokens, OutputUnits: wire.Usage.OutputTokens}}
	for _, ct := range wire.Content {
		switch ct.Type {
		case "text":
			out.Text += ct.Text
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: ct.ID, Name: ct.Name, Arguments: ct.Input})
		}
	}
	if err := out.Validate(); err != nil {
		return Response{}, err
	}
	return out, nil
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens uint32             `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID      string             `json:"id"`
	Content []anthropicContent `json:"content"`
	Usage   struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
	} `json:"usage"`
}
