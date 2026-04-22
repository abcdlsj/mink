package codex

import (
	"strings"
	"testing"

	"github.com/abcdlsj/mink/msg"
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
