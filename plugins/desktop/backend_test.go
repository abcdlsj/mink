package desktop

import (
	"testing"
	"time"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

func TestBackendNilAppFallsBackToMock(t *testing.T) {
	b := newBackend(nil)
	if got := b.WorkspaceInfo().Workspace; got == "" {
		t.Errorf("WorkspaceInfo empty: %#v", b.WorkspaceInfo())
	}
	if got := b.ListChannels(); len(got) == 0 {
		t.Error("ListChannels empty in mock mode")
	}
	if got := b.ListThreads(); len(got) == 0 {
		t.Error("ListThreads empty in mock mode")
	}
	if got := b.ListAgents(); len(got) == 0 {
		t.Error("ListAgents empty in mock mode")
	}
	if got := b.ListPersonas(); len(got) == 0 {
		t.Error("ListPersonas empty in mock mode")
	}
	if got := b.ListModels(); len(got) == 0 {
		t.Error("ListModels empty in mock mode")
	}
	if got := b.ListTools(); len(got) == 0 {
		t.Error("ListTools empty in mock mode")
	}
	if got := b.ListCommands(); len(got) == 0 {
		t.Error("ListCommands empty in mock mode")
	}
}

func TestBackendStopWithoutSendIsSafe(t *testing.T) {
	b := newBackend(nil)
	if err := b.StopTurn("missing"); err != nil {
		t.Errorf("StopTurn returned: %v", err)
	}
}

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in       string
		provider string
		model    string
	}{
		{"anthropic / claude-sonnet-4", "anthropic", "claude-sonnet-4"},
		{"openai / gpt-4.1-mini", "openai", "gpt-4.1-mini"},
		{"(unconfigured)", "(unconfigured)", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		p, m := splitModel(c.in)
		if p != c.provider || m != c.model {
			t.Errorf("splitModel(%q) = %q,%q want %q,%q", c.in, p, m, c.provider, c.model)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := fallback("", "default"); got != "default" {
		t.Errorf("fallback empty: %q", got)
	}
	if got := fallback("  ", "default"); got != "default" {
		t.Errorf("fallback whitespace: %q", got)
	}
	if got := fallback("real", "default"); got != "real" {
		t.Errorf("fallback non-empty: %q", got)
	}
}

func TestConvertMessagesIncludesToolEvents(t *testing.T) {
	now := time.Now()
	s := session.New("desktop")
	s.Add(msg.Message{ID: "u1", Role: "user", Content: "hi", Timestamp: now})
	s.Add(msg.Message{
		ID: "a1", Role: "assistant", AgentID: "coder", Content: "ran ls", Timestamp: now,
		ToolCalls:   []msg.ToolCall{{ID: "tc1", Name: "shell", Args: []byte(`{"cmd":"ls"}`)}},
		ToolResults: []msg.ToolResult{{ToolCallID: "tc1", Content: "a b c"}},
	})
	views := convertMessages(s)
	if len(views) != 2 {
		t.Fatalf("convertMessages: want 2 views, got %d", len(views))
	}
	if views[0].Role != "user" || views[1].Role != "agent" {
		t.Errorf("role mapping wrong: %q %q", views[0].Role, views[1].Role)
	}
	if len(views[1].Events) != 1 {
		t.Fatalf("expected 1 tool event on assistant message, got %d", len(views[1].Events))
	}
	ev := views[1].Events[0]
	if ev.ToolName != "shell" || ev.Output != "a b c" || ev.Status != "done" {
		t.Errorf("tool event mapped wrong: %+v", ev)
	}
}

func TestConvertMessagesMarksToolErrorEvents(t *testing.T) {
	now := time.Now()
	s := session.New("desktop")
	s.Add(msg.Message{
		Role: "assistant", AgentID: "coder", Timestamp: now,
		ToolCalls:   []msg.ToolCall{{ID: "tc1", Name: "shell", Args: []byte(`{}`)}},
		ToolResults: []msg.ToolResult{{ToolCallID: "tc1", Error: "boom"}},
	})
	views := convertMessages(s)
	if got := views[0].Events[0].Status; got != "error" {
		t.Errorf("error tool result must mark event status error, got %q", got)
	}
	if views[0].Events[0].Err != "boom" {
		t.Errorf("error message lost: %+v", views[0].Events[0])
	}
}

func TestIsThreadIDDistinguishesChannel(t *testing.T) {
	if isThreadID(defaultChannelID) {
		t.Error("default channel id must not look like a thread id")
	}
	if !isThreadID("20260528-desktop-abcdef12") {
		t.Error("session-shaped id should be a thread id")
	}
}
