package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIResponsesEncodesAssistantToolContinuation(t *testing.T) {
	var payload map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://provider.invalid/v1/responses" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request = %s, authorization = %q", request.URL, request.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"id":"response-2","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":7,"output_tokens":3}}`), nil
	})}
	provider, err := NewOpenAIResponses(HTTPConfig{Endpoint: "https://provider.invalid", Model: "model", APIKey: "secret", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), continuationRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "done" || response.Usage.InputUnits != 7 || response.Usage.OutputUnits != 3 {
		t.Fatalf("response = %+v", response)
	}
	input := payload["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input = %#v", input)
	}
	functionCall := input[1].(map[string]any)
	toolResult := input[2].(map[string]any)
	if functionCall["type"] != "function_call" || functionCall["call_id"] != "call-1" || functionCall["name"] != "lookup" || functionCall["arguments"] != `{"query":"current"}` {
		t.Fatalf("function call continuation = %#v", functionCall)
	}
	if toolResult["type"] != "function_call_output" || toolResult["call_id"] != "call-1" || toolResult["output"] != `{"fact":"yes"}` {
		t.Fatalf("tool result continuation = %#v", toolResult)
	}
}

func TestAnthropicMessagesEncodesAssistantToolContinuation(t *testing.T) {
	var payload map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://provider.invalid/v1/messages" || request.Header.Get("x-api-key") != "secret" {
			t.Fatalf("request = %s, api key = %q", request.URL, request.Header.Get("x-api-key"))
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(`{"id":"message-2","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":9,"output_tokens":4}}`), nil
	})}
	provider, err := NewAnthropicMessages(HTTPConfig{Endpoint: "https://provider.invalid", Model: "model", APIKey: "secret", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), continuationRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "done" || response.Usage.InputUnits != 9 || response.Usage.OutputUnits != 4 {
		t.Fatalf("response = %+v", response)
	}
	messages := payload["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	assistant := messages[1].(map[string]any)
	content := assistant["content"].([]any)
	toolUse := content[0].(map[string]any)
	if assistant["role"] != "assistant" || toolUse["type"] != "tool_use" || toolUse["id"] != "call-1" || toolUse["name"] != "lookup" {
		t.Fatalf("assistant continuation = %#v", assistant)
	}
	toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call-1" || toolResult["content"] != `{"fact":"yes"}` {
		t.Fatalf("tool result continuation = %#v", toolResult)
	}
}

func continuationRequest() Request {
	return Request{
		System: "system",
		Messages: []Message{
			{Role: RoleUser, Text: "question"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"query":"current"}`)}}},
			{Role: RoleTool, ToolCallID: "call-1", Text: `{"fact":"yes"}`},
			{Role: RoleUser, Text: "continue"},
		},
		Tools: []Tool{{Name: "lookup", Description: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
