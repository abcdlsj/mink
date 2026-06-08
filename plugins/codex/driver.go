package codex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

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
		RuntimeMeta:   runtimeMeta,
	}
}

var (
	versionOnce sync.Once
	versionText string
)

func runtimeMeta(ctx context.Context) map[string]string {
	meta := map[string]string{"runtime": "codex"}
	versionOnce.Do(func() {
		versionText = external.CommandVersion(ctx, "codex")
	})
	if versionText != "" {
		meta["cli_version"] = versionText
	}
	return meta
}

func formatHistory(messages []msg.Message) string {
	return external.FormatHistory(messages)
}

type codexUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

func parseOutput(line string) *external.Message {
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
		Usage *codexUsage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "thread.started":
		meta := map[string]string{"runtime": "codex"}
		if ev.ThreadID != "" {
			meta["thread_id"] = ev.ThreadID
		}
		return &external.Message{Type: external.MsgRuntimeMeta, Meta: meta}
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
			return &external.Message{
				Type:     external.MsgAssistantText,
				Text:     ev.Item.Text,
				Snapshot: false,
			}
		case "command_execution":
			out := &external.Message{
				Type:   external.MsgToolResult,
				ToolID: ev.Item.ID,
				Text:   ev.Item.AggregatedOutput,
			}
			if ev.Item.ExitCode != nil {
				out.ExitCode = *ev.Item.ExitCode
				out.IsError = *ev.Item.ExitCode != 0
			}
			return out
		}
	case "turn.completed":
		done := &external.Message{Type: external.MsgTurnDone}
		if ev.Usage != nil {
			done.Usage = &msg.TokenUsage{
				Input:  ev.Usage.InputTokens + ev.Usage.CachedInputTokens,
				Output: ev.Usage.OutputTokens + ev.Usage.ReasoningOutputTokens,
				Total:  ev.Usage.InputTokens + ev.Usage.CachedInputTokens + ev.Usage.OutputTokens + ev.Usage.ReasoningOutputTokens,
				Source: "codex",
			}
		}
		return done
	}

	return nil
}

func marshalCommand(cmd string) string {
	data, _ := json.Marshal(map[string]string{"cmd": cmd})
	return string(data)
}
