package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ClaudeCodeDriver() ExternalDriver {
	return ExternalDriver{
		Name:    "claude",
		Command: "claude",
		BuildArgs: func(prompt, mcpConfigPath, workDir, sessionID string) []string {
			args := []string{
				"--print",
				"--output-format", "stream-json",
				"--verbose",
				"--include-partial-messages",
				"--permission-mode", "acceptEdits",
				"--mcp-config", mcpConfigPath,
			}
			if workDir != "" && workDir != "." {
				args = append(args, "--add-dir", workDir)
			}
			if sessionID != "" {
				args = append(args, "--resume", sessionID)
			}
			args = append(args, "-p", prompt)
			return args
		},
		ParseOutput: parseClaudeCodeOutput,
	}
}

func parseClaudeCodeOutput(line string) *RuntimeMessage {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var env struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Name  string `json:"name"`
				ID    string `json:"id"`
				Input any    `json:"input"`
			} `json:"content"`
		} `json:"message"`
		Event struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Name  string `json:"name"`
				ID    string `json:"id"`
				Input any    `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		} `json:"event"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Result    string `json:"result"`
		IsError   bool   `json:"is_error"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}

	switch env.Type {
	case "stream_event":
		switch env.Event.Type {
		case "content_block_delta":
			if env.Event.Delta.Type == "text_delta" && env.Event.Delta.Text != "" {
				return &RuntimeMessage{Type: MsgStreamChunk, Text: env.Event.Delta.Text}
			}
		case "content_block_start":
			if env.Event.ContentBlock.Type == "tool_use" {
				return &RuntimeMessage{
					Type:     MsgToolCall,
					ToolName: env.Event.ContentBlock.Name,
					ToolID:   env.Event.ContentBlock.ID,
					ToolArgs: marshalClaudeToolInput(env.Event.ContentBlock.Input),
				}
			}
		}
	case "assistant":
		if env.Subtype == "message" || env.Subtype == "" {
			var text strings.Builder
			for _, c := range env.Message.Content {
				switch c.Type {
				case "text":
					text.WriteString(c.Text)
				case "tool_use":
					return &RuntimeMessage{
						Type:     MsgToolCall,
						ToolName: c.Name,
						ToolID:   c.ID,
						ToolArgs: marshalClaudeToolInput(c.Input),
					}
				}
			}
			if text.Len() > 0 {
				return &RuntimeMessage{Type: MsgAssistantText, Text: text.String()}
			}
		}
	case "content_block_delta":
		if env.Delta.Type == "text_delta" && env.Delta.Text != "" {
			return &RuntimeMessage{Type: MsgStreamChunk, Text: env.Delta.Text}
		}
	case "tool_use":
		for _, c := range env.Message.Content {
			if c.Type == "tool_use" {
				return &RuntimeMessage{
					Type:     MsgToolCall,
					ToolName: c.Name,
					ToolID:   c.ID,
					ToolArgs: marshalClaudeToolInput(c.Input),
				}
			}
		}
	case "tool_result":
		return &RuntimeMessage{Type: MsgToolResult, Text: env.Content.Text}
	case "result":
		if env.IsError {
			return &RuntimeMessage{Type: MsgError, Text: env.Result, SessionID: env.SessionID}
		}
		return &RuntimeMessage{Type: MsgTurnDone, Text: env.Result, SessionID: env.SessionID}
	case "error":
		return &RuntimeMessage{Type: MsgError, Text: env.Result, SessionID: env.SessionID}
	}

	return nil
}

func marshalClaudeToolInput(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
