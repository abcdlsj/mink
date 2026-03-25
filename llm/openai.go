package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/sashabaranov/go-openai"
)

type openAITransport struct {
	headers   map[string]string
	reasoning bool
}

func (t *openAITransport) prepare(req *http.Request) error {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return err
		}
		body = patchAssistantContent(body)
		if t.reasoning {
			body = patchReasoning(body)
		}
		resetRequestBody(req, body)
	}
	return nil
}

func patchReasoning(body []byte) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	if _, ok := obj["reasoning"]; ok {
		return body
	}
	obj["reasoning"] = json.RawMessage(`{"enabled":true}`)
	out, _ := json.Marshal(obj)
	return out
}

func patchOpenRouterReasoning(body []byte) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
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
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
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

type openAI struct {
	client *openai.Client
	model  string
	cfg    Config
}

func newOpenAI(cfg Config) *openAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	config := openai.DefaultConfig(cfg.APIKey)
	config.BaseURL = cfg.BaseURL
	config.HTTPClient = newRetryHTTPClient((&openAITransport{
		headers:   cfg.Headers,
		reasoning: cfg.Reasoning,
	}).prepare)

	return &openAI{
		client: openai.NewClientWithConfig(config),
		model:  cfg.Model,
		cfg:    cfg,
	}
}

func wrapErr(err error) error {
	var re *openai.RequestError
	if errors.As(err, &re) {
		body := strings.TrimSpace(string(re.Body))
		if body == "" {
			body = re.HTTPStatus
		}
		return fmt.Errorf("%s (HTTP %d)", body, re.HTTPStatusCode)
	}
	return err
}

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

func (o *openAI) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
	req := o.buildRequest(msgs, tools)
	req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		req.StreamOptions = nil
		stream, err = o.client.CreateChatCompletionStream(ctx, req)
	}
	if err != nil {
		return nil, wrapErr(err)
	}

	ch := make(chan Chunk, 32)

	go func() {
		defer stream.Close()
		defer close(ch)

		var fullContent strings.Builder
		var reasoningContent strings.Builder
		var usage *TokenUsage
		toolCallsMap := make(map[int]*msg.ToolCall)

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err.Error() != "EOF" {
					select {
					case ch <- Chunk{Type: ChunkError, Error: err}:
					case <-ctx.Done():
					}
				}
				break
			}

			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = toTokenUsage(*chunk.Usage)
				}
				continue
			}

			if chunk.Usage != nil {
				usage = toTokenUsage(*chunk.Usage)
			}

			delta := chunk.Choices[0].Delta

			if delta.ReasoningContent != "" {
				reasoningContent.WriteString(delta.ReasoningContent)
				select {
				case ch <- Chunk{Type: ChunkReasoning, ReasoningDelta: delta.ReasoningContent}:
				case <-ctx.Done():
					return
				}
			}

			if delta.Content != "" {
				fullContent.WriteString(delta.Content)
				select {
				case ch <- Chunk{Type: ChunkText, Delta: delta.Content}:
				case <-ctx.Done():
					return
				}
			}

			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}

				if _, exists := toolCallsMap[idx]; !exists {
					toolCallsMap[idx] = &msg.ToolCall{}
				}

				if tc.ID != "" {
					toolCallsMap[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					toolCallsMap[idx].Name = tc.Function.Name
				}
				toolCallsMap[idx].Args = append(toolCallsMap[idx].Args, []byte(tc.Function.Arguments)...)
			}
		}

		idxs := make([]int, 0, len(toolCallsMap))
		for idx := range toolCallsMap {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		for _, idx := range idxs {
			tc := toolCallsMap[idx]
			if tc == nil || tc.Name == "" || len(tc.Args) == 0 {
				continue
			}
			select {
			case ch <- Chunk{Type: ChunkToolCall, ToolCall: tc}:
			case <-ctx.Done():
				return
			}
		}

		select {
		case ch <- Chunk{
			Type:      ChunkDone,
			Usage:     usage,
			Reasoning: reasoningContent.String(),
		}:
		case <-ctx.Done():
		}
	}()

	return ch, nil
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
		} else {
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
