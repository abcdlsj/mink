package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/abcdlsj/mink/msg"
)

type anthropic struct {
	cfg    Config
	client *http.Client
}

func newAnthropic(cfg Config) *anthropic {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	return &anthropic{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *anthropic) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
	body, err := json.Marshal(a.buildReq(msgs, tools))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.cfg.BaseURL+"/messages", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range a.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: %s", resp.Status, b)
	}

	var r anthropicResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}

	res := &Response{}
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			res.Content += c.Text
		case "tool_use":
			args, _ := json.Marshal(c.Input)
			res.ToolCalls = append(res.ToolCalls, msg.ToolCall{
				ID:   c.ID,
				Name: c.Name,
				Args: args,
			})
		}
	}
	return res, nil
}

func (a *anthropic) buildReq(msgs []msg.Message, tools []Tool) map[string]any {
	sys := ""
	var amsgs []map[string]any

	for _, m := range msgs {
		if m.Role == "system" {
			sys = m.Content
			continue
		}
		amsgs = append(amsgs, a.convertMsg(m))
	}

	req := map[string]any{
		"model":       a.cfg.Model,
		"max_tokens":  a.cfg.MaxTokens,
		"temperature": a.cfg.Temperature,
		"messages":    amsgs,
	}
	if sys != "" {
		req["system"] = sys
	}
	if len(tools) > 0 {
		req["tools"] = a.convertTools(tools)
	}
	return req
}

func (a *anthropic) convertMsg(m msg.Message) map[string]any {
	role := m.Role
	if role == "tool" {
		role = "user"
	}

	content := []map[string]any{}
	if m.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": m.Content})
	}
	for _, tc := range m.ToolCalls {
		var input map[string]any
		json.Unmarshal(tc.Args, &input)
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": input,
		})
	}
	for _, tr := range m.ToolResults {
		content = append(content, map[string]any{
			"type":        "tool_result",
			"tool_use_id": tr.ToolCallID,
			"content":     tr.Content,
		})
	}

	return map[string]any{"role": role, "content": content}
}

func (a *anthropic) convertTools(tools []Tool) []map[string]any {
	var r []map[string]any
	for _, t := range tools {
		r = append(r, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		})
	}
	return r
}

type anthropicResp struct {
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text"`
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"content"`
}
