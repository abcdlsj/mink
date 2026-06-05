package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/plugins/external"
)

func TestDriverFormatsSessionHistory(t *testing.T) {
	d := driver()
	if d.FormatHistory == nil {
		t.Fatal("FormatHistory is nil")
	}

	out := d.FormatHistory([]msg.Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好，有什么可以帮你的"},
		{Role: "assistant", ToolCalls: []msg.ToolCall{{
			Name: "bash",
			Args: []byte(`{"cmd":"pwd"}`),
		}}},
		{Role: "tool", ToolResults: []msg.ToolResult{{
			Content: "/tmp/demo",
		}}},
	})

	for _, want := range []string{
		"<conversation_history>",
		`[user]: "你好"`,
		`[assistant]: "你好，有什么可以帮你的"`,
		`[tool_call]: bash({"cmd":"pwd"})`,
		`[tool_result]: "/tmp/demo"`,
		"</conversation_history>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("history missing %q:\n%s", want, out)
		}
	}
}

func TestDriverBuildArgsNeverAskForInteractiveApproval(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "", false)
	for _, arg := range args {
		if arg == "--dangerously-bypass-approvals-and-sandbox" {
			return
		}
	}
	t.Fatalf("approval/sandbox bypass not found in args: %v", args)
}

func TestParseOutputAgentMessageEmitsAssistantText(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_1",
			"type": "agent_message",
			"text": "hello",
		},
	})
	m := parseOutput(line)
	if m == nil || m.Type != external.MsgAssistantText || m.Text != "hello" || m.Snapshot {
		t.Fatalf("got %#v", m)
	}
}

func TestParseOutputCommandExecutionCarriesExitCode(t *testing.T) {
	exitCode := 1
	line := mustJSON(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":                "item_0",
			"type":              "command_execution",
			"command":           "false",
			"aggregated_output": "",
			"exit_code":         exitCode,
			"status":            "completed",
		},
	})
	m := parseOutput(line)
	if m == nil || m.Type != external.MsgToolResult || m.ToolID != "item_0" {
		t.Fatalf("got %#v", m)
	}
	if !m.IsError || m.ExitCode != 1 {
		t.Fatalf("error flag = %v exit = %d", m.IsError, m.ExitCode)
	}
}

func TestParseOutputTurnCompletedCarriesUsage(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type": "turn.completed",
		"usage": map[string]any{
			"input_tokens":          100,
			"cached_input_tokens":   50,
			"output_tokens":         20,
			"reasoning_output_tokens": 5,
		},
	})
	m := parseOutput(line)
	if m == nil || m.Type != external.MsgTurnDone {
		t.Fatalf("got %#v", m)
	}
	if m.Usage == nil || m.Usage.Input != 150 || m.Usage.Output != 25 || m.Usage.Total != 175 {
		t.Fatalf("usage = %#v", m.Usage)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
