package desktop

import (
	"testing"

	"github.com/abcdlsj/sumi/bus"
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

func TestConvertMessages_Removed(t *testing.T) {
	// convertMessages / personaFromSource / collectEvents were removed
	// in P3.6 once every desktop API switched to space.Manager. The
	// previous tests asserted behaviors that the new helpers
	// (spaceMessagesToView in particular) cover indirectly through
	// the live API tests in this package and in app/.
	t.Skip("convertMessages removed in P3.6 — see backend.go header")
}

func TestIsThreadIDDistinguishesChannel(t *testing.T) {
	if isThreadID(defaultChannelID) {
		t.Error("default channel id must not look like a thread id")
	}
	if !isThreadID("20260528-desktop-abcdef12") {
		t.Error("session-shaped id should be a thread id")
	}
}

func TestToBusEventMapsCollab(t *testing.T) {
	cases := []struct {
		name string
		in   bus.Event
		want string
	}{
		{"queued", bus.Event{Type: bus.DelegateQueued, TaskID: "t1", Text: "audit retry"}, "agent.delegate.started"},
		{"started", bus.Event{Type: bus.DelegateStarted, TaskID: "t1"}, "agent.delegate.progress"},
		{"finished", bus.Event{Type: bus.DelegateFinished, TaskID: "t1", Output: "done"}, "agent.delegate.finished"},
		{"failed", bus.Event{Type: bus.DelegateFailed, TaskID: "t1", Err: "oops"}, "agent.delegate.failed"},
		{"mention call", bus.Event{Type: bus.ToolCallStarted, Tool: "mention", Input: `{"target":"coder"}`}, "agent.mention"},
		{"mention reply", bus.Event{Type: bus.ToolCallFinished, Tool: "mention", Output: "ok", Input: `{"target":"coder"}`}, "agent.mention.reply"},
		{"plain tool", bus.Event{Type: bus.ToolCallStarted, Tool: "shell"}, bus.ToolCallStarted},
	}
	for _, c := range cases {
		got := toBusEvent(c.in).Type
		if got != c.want {
			t.Errorf("%s: toBusEvent type = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMentionTargetReadsArgs(t *testing.T) {
	got := mentionTarget(bus.Event{Tool: "mention", Input: `{"target":"reviewer"}`})
	if got != "reviewer" {
		t.Errorf("mentionTarget = %q, want reviewer", got)
	}
	got = mentionTarget(bus.Event{Tool: "mention", Input: `{"agent":"coder"}`})
	if got != "coder" {
		t.Errorf("mentionTarget agent fallback = %q", got)
	}
	got = mentionTarget(bus.Event{Tool: "mention", Input: ""})
	if got != "mention" {
		t.Errorf("mentionTarget empty fallback = %q", got)
	}
}

// Tests for the removed legacy reader (personaFromSource,
// convertMessages, collectEvents) were dropped together with the
// helpers themselves in P3.6 — see backend.go header comment.

func TestParseTaskID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"scheduled next team turn for coder, task_id=task-abc123", "task-abc123"},
		{"foo task_id=task-xyz bar", "task-xyz"},
		{"no task here", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := parseTaskID(c.in)
		if got != c.want {
			t.Errorf("parseTaskID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanizeStep(t *testing.T) {
	ws := "/Users/me/repo"
	cases := []struct {
		tool string
		args string
		want string
	}{
		{"read", `{"path":"/Users/me/repo/llm/anthropic.go"}`, "read llm/anthropic.go"},
		{"list_files", `{"path":"/Users/me/repo/plugins/collab"}`, "listed plugins/collab/"},
		{"bash", `{"cmd":"ls /Users/me/repo/cmd"}`, "ran ls cmd"},
		{"bash", `{"cmd":"go test ./..."}`, "ran go test ./..."},
		{"grep", `{"query":"retry behaviour"}`, "searched for retry behaviour"},
		{"read", `{"path":"/etc/passwd"}`, "read passwd"},
		{"unknown_tool", `{}`, "unknown_tool"},
	}
	for _, c := range cases {
		got := humanizeStep(c.tool, c.args, ws)
		if got != c.want {
			t.Errorf("humanizeStep(%s, %s) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}
