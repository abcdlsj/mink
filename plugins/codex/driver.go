package codex

import (
	"encoding/json"
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/plugins/external"
)

func driver() external.Driver {
	return external.Driver{
		Name:        "codex",
		Command:     "codex",
		StdinPrompt: true,
		BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
			args := []string{
				"exec",
				"--json",
				"--dangerously-bypass-approvals-and-sandbox",
			}
			if workDir != "" && workDir != "." {
				args = append(args, "-C", workDir)
			}
			args = append(args, "-")
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

	var ev struct {
		Type string `json:"type"`
		Item struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Text             string `json:"text"`
			Command          string `json:"command"`
			AggregatedOutput string `json:"aggregated_output"`
		} `json:"item"`
	}

	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "item.started":
		if ev.Item.Type == "command_execution" {
			return &external.Message{
				Type:     external.MsgToolCall,
				ToolName: "bash",
				ToolID:   ev.Item.ID,
				ToolArgs: marshalCommand(ev.Item.Command),
			}
		}
	case "item.completed":
		switch ev.Item.Type {
		case "agent_message":
			text := ev.Item.Text
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			return &external.Message{Type: external.MsgStreamChunk, Text: text}
		case "command_execution":
			return &external.Message{
				Type:   external.MsgToolResult,
				ToolID: ev.Item.ID,
				Text:   ev.Item.AggregatedOutput,
			}
		}
	}

	return nil
}

func marshalCommand(cmd string) string {
	data, _ := json.Marshal(map[string]string{"cmd": cmd})
	return string(data)
}
