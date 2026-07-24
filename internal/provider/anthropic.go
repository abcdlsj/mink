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

func (client *AnthropicMessages) Complete(ctx context.Context, request Request) (Response, error) {
	messages := make([]anthropicMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		switch message.Role {
		case RoleUser:
			messages = append(messages, anthropicMessage{Role: string(message.Role), Content: []anthropicContent{{Type: "text", Text: message.Text}}})
		case RoleAssistant:
			content := make([]anthropicContent, 0, 1+len(message.ToolCalls))
			if message.Text != "" {
				content = append(content, anthropicContent{Type: "text", Text: message.Text})
			}
			for _, call := range message.ToolCalls {
				content = append(content, anthropicContent{Type: "tool_use", ID: call.ID, Name: call.Name, Input: call.Arguments})
			}
			if len(content) == 0 {
				return Response{}, errors.New("anthropic assistant continuation is empty")
			}
			messages = append(messages, anthropicMessage{Role: "assistant", Content: content})
		case RoleTool:
			messages = append(messages, anthropicMessage{Role: "user", Content: []anthropicContent{{Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Text}}})
		default:
			return Response{}, errors.New("anthropic request message role is invalid")
		}
	}
	tools := make([]anthropicTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, anthropicTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Schema})
	}
	payload := anthropicRequest{Model: client.config.Model, MaxTokens: 8192, System: request.System, Messages: messages, Tools: tools}
	var wire anthropicResponse
	if err := postJSON(ctx, client.config.Client, endpointWithDefaultPath(client.config.Endpoint, "/v1/messages"), map[string]string{
		"x-api-key": client.config.APIKey, "anthropic-version": "2023-06-01",
	}, payload, &wire); err != nil {
		return Response{}, err
	}
	result := Response{ID: wire.ID, Usage: Usage{InputUnits: wire.Usage.InputTokens, OutputUnits: wire.Usage.OutputTokens}}
	for _, content := range wire.Content {
		switch content.Type {
		case "text":
			result.Text += content.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: content.ID, Name: content.Name, Arguments: content.Input})
		}
	}
	if err := result.Validate(); err != nil {
		return Response{}, err
	}
	return result, nil
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
