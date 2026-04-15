package external

import (
	"encoding/json"
	"fmt"
	"strings"

	agrt "github.com/abcdlsj/mink/agent/runtime"
	"github.com/abcdlsj/mink/msg"
)

func CodexDriver() agrt.Driver {
	return agrt.Driver{
		Name:        "codex",
		Command:     "codex",
		StdinPrompt: true,
		BuildArgs: func(prompt, mcpConfigPath, workDir, sessionID string) []string {
			args := []string{"exec", "--json", "--full-auto"}
			if workDir != "" && workDir != "." {
				args = append(args, "-C", workDir)
			}
			args = append(args, "-")
			return args
		},
		ParseOutput:   parseCodexOutput,
		FormatHistory: formatCodexHistory,
	}
}

func formatCodexHistory(messages []msg.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<conversation_history>\n")
	for _, m := range messages {
		switch {
		case m.Role == "user" && m.Content != "":
			fmt.Fprintf(&sb, "[user]: %s\n", m.Content)
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&sb, "[tool_call]: %s(%s)\n", tc.Name, string(tc.Args))
			}
		case m.Role == "tool" && len(m.ToolResults) > 0:
			for _, tr := range m.ToolResults {
				result := tr.Content
				if len(result) > 500 {
					result = result[:500] + "...(truncated)"
				}
				fmt.Fprintf(&sb, "[tool_result]: %s\n", result)
			}
		case m.Role == "assistant" && m.Content != "":
			fmt.Fprintf(&sb, "[assistant]: %s\n", m.Content)
		}
	}
	sb.WriteString("</conversation_history>")
	return sb.String()
}

func parseCodexOutput(line string) *agrt.Message {
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
			return &agrt.Message{Type: agrt.MsgTurnDone, SessionID: ev.ThreadID}
		}
	case "item.started":
		if ev.Item.Type == "command_execution" {
			return &agrt.Message{
				Type:     agrt.MsgToolCall,
				ToolName: "shell",
				ToolID:   ev.Item.ID,
				ToolArgs: ev.Item.Command,
			}
		}
	case "item.completed":
		switch ev.Item.Type {
		case "agent_message":
			text := ev.Item.Text
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			return &agrt.Message{Type: agrt.MsgStreamChunk, Text: text}
		case "command_execution":
			return &agrt.Message{
				Type:   agrt.MsgToolResult,
				ToolID: ev.Item.ID,
				Text:   ev.Item.AggregatedOutput,
			}
		}
	case "turn.completed":
		return &agrt.Message{
			Type:         agrt.MsgTurnDone,
			InputTokens:  ev.Usage.InputTokens,
			OutputTokens: ev.Usage.OutputTokens,
		}
	}

	return nil
}
