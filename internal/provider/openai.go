package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type OpenAIResponses struct {
	config HTTPConfig
}

func NewOpenAIResponses(config HTTPConfig) (*OpenAIResponses, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &OpenAIResponses{config: config}, nil
}

func (client *OpenAIResponses) Complete(ctx context.Context, request Request) (Response, error) {
	input := make([]openAIInput, 0, len(request.Messages))
	for _, message := range request.Messages {
		switch message.Role {
		case RoleUser:
			input = append(input, openAIInput{Role: string(message.Role), Content: message.Text})
		case RoleAssistant:
			if message.Text != "" {
				input = append(input, openAIInput{Role: string(message.Role), Content: message.Text})
			}
			for _, call := range message.ToolCalls {
				input = append(input, openAIInput{
					Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: string(call.Arguments),
				})
			}
		case RoleTool:
			input = append(input, openAIInput{Type: "function_call_output", CallID: message.ToolCallID, Output: message.Text})
		default:
			return Response{}, errors.New("OpenAI request message role is invalid")
		}
	}
	tools := make([]openAITool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, openAITool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.Schema, Strict: true})
	}
	payload := openAIRequest{Model: client.config.Model, Instructions: request.System, Input: input, Tools: tools}
	var wire openAIResponse
	if err := postJSON(ctx, client.config.Client, endpointWithDefaultPath(client.config.Endpoint, "/v1/responses"), map[string]string{
		"Authorization": "Bearer " + client.config.APIKey,
	}, payload, &wire); err != nil {
		return Response{}, err
	}
	result := Response{ID: wire.ID, Usage: Usage{InputUnits: wire.Usage.InputTokens, OutputUnits: wire.Usage.OutputTokens}}
	for _, output := range wire.Output {
		switch output.Type {
		case "message":
			for _, content := range output.Content {
				if content.Type == "output_text" {
					result.Text += content.Text
				}
			}
		case "function_call":
			arguments := json.RawMessage(output.Arguments)
			if !json.Valid(arguments) {
				return Response{}, fmt.Errorf("OpenAI tool call %q returned invalid arguments", output.Name)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{ID: output.CallID, Name: output.Name, Arguments: arguments})
		}
	}
	if err := result.Validate(); err != nil {
		return Response{}, err
	}
	return result, nil
}

type openAIRequest struct {
	Model        string        `json:"model"`
	Instructions string        `json:"instructions"`
	Input        []openAIInput `json:"input"`
	Tools        []openAITool  `json:"tools,omitempty"`
}

type openAIInput struct {
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Type      string `json:"type,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type openAITool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type openAIResponse struct {
	ID     string `json:"id"`
	Output []struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
	} `json:"usage"`
}
