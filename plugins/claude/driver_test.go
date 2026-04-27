package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/plugins/external"
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

func TestDriverBuildArgsDoesNotRequestPartialMessages(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "", false)
	for _, arg := range args {
		if arg == "--include-partial-messages" {
			t.Fatalf("unexpected arg %q", arg)
		}
	}
}

func TestDriverBuildArgsWithSessionID(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "test-session-123", false)
	found := false
	for i, arg := range args {
		if arg == "--session-id" && i+1 < len(args) && args[i+1] == "test-session-123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("--session-id not found in args: %v", args)
	}
}

func TestDriverBuildArgsResumesExistingSession(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "test-session-123", true)
	for i, arg := range args {
		if arg == "--resume" && i+1 < len(args) && args[i+1] == "test-session-123" {
			return
		}
	}
	t.Fatalf("--resume not found in args: %v", args)
}

func TestParseOutputMarksAssistantSnapshots(t *testing.T) {
	tests := []string{
		mustJSON(t, map[string]any{
			"type":    "assistant",
			"subtype": "message",
			"message": map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "hello",
				}},
			},
		}),
		mustJSON(t, map[string]any{
			"type":   "result",
			"result": "hello",
		}),
	}

	for _, line := range tests {
		m := parseOutput(line)
		if m == nil {
			t.Fatalf("parseOutput(%s) = nil", line)
		}
		if m.Type != external.MsgAssistantText || !m.Snapshot || m.Text != "hello" {
			t.Fatalf("got %#v", m)
		}
	}
}

func TestParseOutputCapturesThinking(t *testing.T) {
	m := parseOutput(mustJSON(t, map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": "think",
			},
		},
	}))
	if m == nil || m.Type != external.MsgThinkingChunk || m.Text != "think" {
		t.Fatalf("got %#v", m)
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
