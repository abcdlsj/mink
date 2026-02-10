package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type openAI struct {
	cfg    Config
	client *http.Client
}

func newOpenAI(cfg Config) *openAI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	return &openAI{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *openAI) Chat(ctx context.Context, msgs []Message, tools []Tool) (*Response, error) {
	body, err := json.Marshal(o.buildReq(msgs, tools))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.cfg.BaseURL+"/chat/completions", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	for k, v := range o.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: %s", resp.Status, b)
	}

	var r openAIResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}

	if len(r.Choices) == 0 {
		return nil, fmt.Errorf("no response")
	}

	c := r.Choices[0]
	res := &Response{Content: c.Message.Content}

	for _, tc := range c.Message.ToolCalls {
		if tc.Type == "function" {
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: []byte(tc.Function.Arguments),
			})
		}
	}
	return res, nil
}

func (o *openAI) buildReq(msgs []Message, tools []Tool) map[string]any {
	req := map[string]any{
		"model":       o.cfg.Model,
		"max_tokens":  o.cfg.MaxTokens,
		"temperature": o.cfg.Temperature,
		"messages":    o.convertMsgs(msgs),
	}
	if len(tools) > 0 {
		req["tools"] = o.convertTools(tools)
	}
	return req
}

func (o *openAI) convertMsgs(msgs []Message) []map[string]any {
	var r []map[string]any
	for _, m := range msgs {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.ToolCalls) > 0 {
			var tcs []map[string]any
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": string(tc.Args),
					},
				})
			}
			msg["tool_calls"] = tcs
		}
		if m.Role == "tool" && len(m.ToolResults) > 0 {
			for _, tr := range m.ToolResults {
				r = append(r, map[string]any{
					"role":         "tool",
					"tool_call_id": tr.ToolCallID,
					"content":      tr.Content,
				})
			}
			continue
		}
		r = append(r, msg)
	}
	return r
}

func (o *openAI) convertTools(tools []Tool) []map[string]any {
	var r []map[string]any
	for _, t := range tools {
		r = append(r, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			},
		})
	}
	return r
}

type openAIResp struct {
	Choices []struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}
