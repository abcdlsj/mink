package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/plugins/external"
	"github.com/abcdlsj/mink/textutil"
)

func driver() external.Driver {
	return external.Driver{
		Name:        "codex",
		Command:     "codex",
		StdinPrompt: true,
		BuildArgs: func(prompt, workDir string) []string {
			args := []string{"exec", "--json", "--full-auto"}
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
				result := textutil.Ellipsis(tr.Content, 500)
				fmt.Fprintf(&sb, "[tool_result]: %s\n", result)
			}
		case m.Role == "assistant" && m.Content != "":
			fmt.Fprintf(&sb, "[assistant]: %s\n", textutil.Valid(m.Content))
		}
	}
	sb.WriteString("</conversation_history>")
	return sb.String()
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
