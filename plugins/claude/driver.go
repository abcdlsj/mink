package claude

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/plugins/external"
)

func itoa(n int) string { return strconv.Itoa(n) }

func driver() external.Driver {
	return external.Driver{
		Name:    "claude",
		Command: "claude",
		BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
			args := []string{
				"--print",
				"--output-format", "stream-json",
				"--verbose",
				"--permission-mode", "bypassPermissions",
				"--dangerously-skip-permissions",
			}
			if sessionID != "" {
				if resume {
					args = append(args, "--resume", sessionID)
				} else {
					args = append(args, "--session-id", sessionID)
				}
			}
			if workDir != "" && workDir != "." {
				args = append(args, "--add-dir", workDir)
			}
			args = append(args, "-p", prompt)
			return args
		},
		ParseOutput:   parseOutput,
		FormatHistory: formatHistory,
	}
}

func formatHistory(messages []msg.Message) string {
	return external.FormatHistory(messages)
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type claudeModelUsage struct {
	CostUSD          float64 `json:"costUSD"`
	ContextWindow    int     `json:"contextWindow"`
	MaxOutputTokens  int     `json:"maxOutputTokens"`
}

type claudeToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Type      string `json:"type"`
	Content   any    `json:"content"`
	IsError   bool   `json:"is_error"`
}

type claudeToolUseResult struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`
}

func parseOutput(line string) *external.Message {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var env struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Model   string                  `json:"model"`
			Role    string                  `json:"role"`
			Usage   *claudeUsage            `json:"usage"`
			Content []json.RawMessage       `json:"content"`
		} `json:"message"`
		Event struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				ID    string `json:"id"`
				Input any    `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		} `json:"event"`
		Result         string                      `json:"result"`
		Error          struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		IsError            bool                        `json:"is_error"`
		DurationMs         int                         `json:"duration_ms"`
		TotalCostUSD       float64                     `json:"total_cost_usd"`
		Usage              *claudeUsage                `json:"usage"`
		ModelUsage         map[string]claudeModelUsage `json:"modelUsage"`
		TerminalReason     string                      `json:"terminal_reason"`
		ToolUseResult      *claudeToolUseResult        `json:"tool_use_result"`
		Cwd                string                      `json:"cwd"`
		ClaudeCodeVersion  string                      `json:"claude_code_version"`
		Model              string                      `json:"model"`
		PermissionMode     string                      `json:"permissionMode"`
		Tools              []string                    `json:"tools"`
		MCPServers         []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"mcp_servers"`
	}

	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}

	switch env.Type {
	case "system":
		if env.Subtype != "init" {
			return nil
		}
		meta := map[string]string{"runtime": "claude"}
		if env.Model != "" {
			meta["model"] = env.Model
		}
		if env.ClaudeCodeVersion != "" {
			meta["version"] = env.ClaudeCodeVersion
		}
		if env.PermissionMode != "" {
			meta["permission_mode"] = env.PermissionMode
		}
		if n := len(env.Tools); n > 0 {
			meta["tools_count"] = itoa(n)
		}
		if n := len(env.MCPServers); n > 0 {
			meta["mcp_servers_count"] = itoa(n)
		}
		return &external.Message{Type: external.MsgRuntimeMeta, Meta: meta}
	case "stream_event":
		switch env.Event.Type {
		case "content_block_delta":
			switch env.Event.Delta.Type {
			case "text_delta":
				if env.Event.Delta.Text != "" {
					return &external.Message{Type: external.MsgStreamChunk, Text: env.Event.Delta.Text}
				}
			case "thinking_delta":
				if env.Event.Delta.Thinking != "" {
					return &external.Message{Type: external.MsgThinkingChunk, Text: env.Event.Delta.Thinking}
				}
			}
		case "content_block_start":
			if env.Event.ContentBlock.Type == "tool_use" {
				return &external.Message{
					Type:     external.MsgToolCall,
					ToolName: env.Event.ContentBlock.Name,
					ToolID:   env.Event.ContentBlock.ID,
					ToolArgs: marshalInput(env.Event.ContentBlock.Input),
				}
			}
		}
	case "assistant":
		if env.Subtype == "message" || env.Subtype == "" {
			var text strings.Builder
			var thinking strings.Builder
			for _, raw := range env.Message.Content {
				var block struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					Thinking string `json:"thinking"`
					Name     string `json:"name"`
					ID       string `json:"id"`
					Input    any    `json:"input"`
				}
				if err := json.Unmarshal(raw, &block); err != nil {
					continue
				}
				switch block.Type {
				case "text":
					text.WriteString(block.Text)
				case "thinking":
					if block.Thinking != "" {
						thinking.WriteString(block.Thinking)
					}
				case "tool_use":
					return &external.Message{
						Type:     external.MsgToolCall,
						ToolName: block.Name,
						ToolID:   block.ID,
						ToolArgs: marshalInput(block.Input),
					}
				}
			}
			if thinking.Len() > 0 && text.Len() == 0 {
				return &external.Message{Type: external.MsgThinkingChunk, Text: thinking.String()}
			}
			if text.Len() > 0 {
				return &external.Message{
					Type:     external.MsgAssistantText,
					Text:     text.String(),
					Snapshot: true,
					Model:    env.Message.Model,
				}
			}
		}
	case "user":
		for _, raw := range env.Message.Content {
			var block claudeToolResultBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			if block.Type != "tool_result" {
				continue
			}
			text := flattenToolResultContent(block.Content)
			out := &external.Message{
				Type:    external.MsgToolResult,
				ToolID:  block.ToolUseID,
				Text:    text,
				IsError: block.IsError,
			}
			if env.ToolUseResult != nil {
				out.Stderr = env.ToolUseResult.Stderr
			}
			return out
		}
	case "result":
		if env.IsError {
			return &external.Message{Type: external.MsgError, Text: resultError(env.Result, env.Subtype, env.Error.Type, env.Error.Message)}
		}
		done := &external.Message{
			Type:    external.MsgTurnDone,
			Text:    env.Result,
			Reason:  env.TerminalReason,
			CostUSD: env.TotalCostUSD,
			Usage:   tokenUsage(env.Usage),
		}
		for model, mu := range env.ModelUsage {
			if mu.CostUSD > done.CostUSD {
				done.CostUSD = mu.CostUSD
			}
			if done.Model == "" {
				done.Model = model
			}
			if done.Usage != nil {
				if mu.ContextWindow > done.Usage.ContextWindow {
					done.Usage.ContextWindow = mu.ContextWindow
				}
				if mu.MaxOutputTokens > done.Usage.MaxTokens {
					done.Usage.MaxTokens = mu.MaxOutputTokens
				}
			}
		}
		return done
	}

	return nil
}

func flattenToolResultContent(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
					continue
				}
			}
			data, _ := json.Marshal(item)
			b.Write(data)
		}
		return b.String()
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func tokenUsage(u *claudeUsage) *msg.TokenUsage {
	if u == nil {
		return nil
	}
	out := &msg.TokenUsage{
		Input:  u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		Output: u.OutputTokens,
		Source: "claude",
	}
	out.Total = out.Input + out.Output
	return out
}

func resultError(result, subtype, typ, message string) string {
	for _, s := range []string{result, message, typ, subtype} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "claude returned an error without details"
}

func marshalInput(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
