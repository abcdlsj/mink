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

func (c *OpenAIResponses) Complete(ctx context.Context, r Request) (Response, error) {
	input := make([]openAIInput, 0, len(r.Messages))
	for _, m := range r.Messages {
		switch m.Role {
		case RoleUser:
			input = append(input, openAIInput{Role: string(m.Role), Content: m.Text})
		case RoleAssistant:
			if m.Text != "" {
				input = append(input, openAIInput{Role: string(m.Role), Content: m.Text})
			}
			for _, call := range m.ToolCalls {
				input = append(input, openAIInput{
					Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: string(call.Arguments),
				})
			}
		case RoleTool:
			input = append(input, openAIInput{Type: "function_call_output", CallID: m.ToolCallID, Output: m.Text})
		default:
			return Response{}, errors.New("OpenAI request message role is invalid")
		}
	}
	tools := make([]openAITool, 0, len(r.Tools))
	for _, t := range r.Tools {
		tools = append(tools, openAITool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Schema, Strict: true})
	}
	payload := openAIRequest{Model: c.config.Model, Instructions: r.System, Input: input, Tools: tools}
	var wire openAIResponse
	if err := postJSON(ctx, c.config.Client, endpointWithDefaultPath(c.config.Endpoint, "/v1/responses"), map[string]string{
		"Authorization": "Bearer " + c.config.APIKey,
	}, payload, &wire); err != nil {
		return Response{}, err
	}
	out := Response{ID: wire.ID, Usage: Usage{InputUnits: wire.Usage.InputTokens, OutputUnits: wire.Usage.OutputTokens}}
	for _, o := range wire.Output {
		switch o.Type {
		case "message":
			for _, ct := range o.Content {
				if ct.Type == "output_text" {
					out.Text += ct.Text
				}
			}
		case "function_call":
			args := json.RawMessage(o.Arguments)
			if !json.Valid(args) {
				return Response{}, fmt.Errorf("OpenAI tool call %q returned invalid arguments", o.Name)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: o.CallID, Name: o.Name, Arguments: args})
		}
	}
	if err := out.Validate(); err != nil {
		return Response{}, err
	}
	return out, nil
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
