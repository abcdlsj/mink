package agent

import (
	"encoding/json"
	"strings"
)

func CodexDriver() ExternalDriver {
	return ExternalDriver{
		Name:    "codex",
		Command: "codex",
		BuildArgs: func(prompt, mcpConfigPath, workDir, sessionID string) []string {
			args := []string{"exec", "--json", "--full-auto"}
			if workDir != "" && workDir != "." {
				args = append(args, "-C", workDir)
			}
			args = append(args, prompt)
			return args
		},
		ParseOutput: parseCodexOutput,
	}
}

func parseCodexOutput(line string) *RuntimeMessage {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var ev struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Text             string `json:"text"`
			Command          string `json:"command"`
			AggregatedOutput string `json:"aggregated_output"`
			ExitCode         *int   `json:"exit_code"`
			Status           string `json:"status"`
		} `json:"item"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "thread.started":
		if ev.ThreadID != "" {
			return &RuntimeMessage{Type: MsgTurnDone, SessionID: ev.ThreadID}
		}
	case "item.started":
		if ev.Item.Type == "command_execution" {
			return &RuntimeMessage{
				Type:     MsgToolCall,
				ToolName: "shell",
				ToolID:   ev.Item.ID,
				ToolArgs: ev.Item.Command,
			}
		}
	case "item.completed":
		switch ev.Item.Type {
		case "agent_message":
			return &RuntimeMessage{Type: MsgStreamChunk, Text: ev.Item.Text}
		case "command_execution":
			return &RuntimeMessage{
				Type:   MsgToolResult,
				ToolID: ev.Item.ID,
				Text:   ev.Item.AggregatedOutput,
			}
		}
	case "turn.completed":
		return &RuntimeMessage{
			Type:         MsgTurnDone,
			InputTokens:  ev.Usage.InputTokens,
			OutputTokens: ev.Usage.OutputTokens,
		}
	}

	return nil
}
