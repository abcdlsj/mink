package llm

import (
	"testing"

	"github.com/abcdlsj/sumi/msg"
)

func TestRepairToolPairsFillsMissingResults(t *testing.T) {
	in := []msg.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []msg.ToolCall{
			{ID: "a", Name: "bash"},
			{ID: "b", Name: "read"},
			{ID: "c", Name: "grep"},
		}},
		{Role: "tool", ToolResults: []msg.ToolResult{{ToolCallID: "a", Content: "ok"}}},
		{Role: "user", Content: "next"},
	}
	out := repairToolPairs(in)
	if len(out) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(out), out)
	}
	covered := map[string]bool{}
	for _, m := range out {
		if m.Role != "tool" {
			continue
		}
		for _, tr := range m.ToolResults {
			covered[tr.ToolCallID] = true
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if !covered[id] {
			t.Fatalf("tool_call %s not covered: %+v", id, out)
		}
	}
}

func TestRepairToolPairsNoopWhenComplete(t *testing.T) {
	in := []msg.Message{
		{Role: "assistant", ToolCalls: []msg.ToolCall{{ID: "a"}}},
		{Role: "tool", ToolResults: []msg.ToolResult{{ToolCallID: "a", Content: "ok"}}},
	}
	out := repairToolPairs(in)
	if len(out) != 2 {
		t.Fatalf("unexpected inject: %+v", out)
	}
}

func TestRepairToolPairsHandlesSplitToolMessages(t *testing.T) {
	in := []msg.Message{
		{Role: "assistant", ToolCalls: []msg.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolResults: []msg.ToolResult{{ToolCallID: "a", Content: "ok"}}},
		{Role: "tool", ToolResults: []msg.ToolResult{{ToolCallID: "b", Content: "ok"}}},
	}
	out := repairToolPairs(in)
	if len(out) != 3 {
		t.Fatalf("should not inject when all covered via split tool msgs: %+v", out)
	}
}
