package claude

import (
	"encoding/json"
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/plugins/external"
)

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

func parseOutput(line string) *external.Message {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var env struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
				Name     string `json:"name"`
				ID       string `json:"id"`
				Input    any    `json:"input"`
			} `json:"content"`
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
		Result string `json:"result"`
		Error  struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		IsError bool `json:"is_error"`
	}

	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return nil
	}

	switch env.Type {
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
			for _, c := range env.Message.Content {
				switch c.Type {
				case "text":
					text.WriteString(c.Text)
				case "thinking":
					if c.Thinking != "" {
						thinking.WriteString(c.Thinking)
					}
				case "tool_use":
					return &external.Message{
						Type:     external.MsgToolCall,
						ToolName: c.Name,
						ToolID:   c.ID,
						ToolArgs: marshalInput(c.Input),
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
				}
			}
		}
	case "result":
		if env.IsError {
			return &external.Message{Type: external.MsgError, Text: resultError(env.Result, env.Subtype, env.Error.Type, env.Error.Message)}
		}
		if env.Result != "" {
			return &external.Message{
				Type:     external.MsgAssistantText,
				Text:     env.Result,
				Snapshot: true,
			}
		}
	}

	return nil
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
