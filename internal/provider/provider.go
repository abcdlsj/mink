package provider

import (
	"context"
	"encoding/json"
	"errors"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Text       string
	ToolCallID string
	ToolCalls  []ToolCall
}

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

type Usage struct {
	InputUnits  uint64
	OutputUnits uint64
}

type Response struct {
	ID        string
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

func (response Response) Validate() error {
	if response.Text == "" && len(response.ToolCalls) == 0 {
		return errors.New("provider response contains neither text nor tool calls")
	}
	for _, call := range response.ToolCalls {
		if call.ID == "" || call.Name == "" || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			return errors.New("provider tool call is invalid")
		}
	}
	return nil
}

type Client interface {
	Complete(context.Context, Request) (Response, error)
}
